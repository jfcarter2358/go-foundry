// Package terrain is the heightfield generator behind the `terrain` stage:
// desert canyon country made from a handful of numbers, on the CPU, in
// parallel, deterministically.
//
// It is pure computation — no files, no meshes, no engine — so the stage
// (procgen/stages/terrain.go) owns the outputs, the editor owns the
// drawing, and later stages (scatter, cliff walls, materials) read the
// product through Terrain (meta.go) without importing anything heavier.
//
// # The pipeline
//
// A field is (N+1)² height samples over a square centred on the origin,
// one every Cell metres. Generate builds it in this order, and the order is
// the design:
//
//  1. BASE SHAPE (shape.go). Domain-warped FBM for the lowlands, and a
//     MESA FIELD: a low-frequency, warped noise thresholded in tiers into
//     flat-topped plateaus with steep, ragged rims. This is what makes the
//     big landforms read as tablelands rather than hills.
//  2. CANYONS (canyon.go). Each `spline` path, densified, meandered by noise
//     and optionally branched into tributaries, is cut into the surface with
//     a cross-section profile (V, U, slot, stepped or a custom curve) down to
//     a floor graded downstream along the path.
//  3. DRAINAGE (fluvial.go). Stream-power erosion on a coarse grid: water is
//     routed to the map edge (priority flood), drainage area accumulates
//     (shared among every downhill neighbour) and channels incise in
//     proportion to sqrt(area), slowed by the strata hardness. This is what makes washes and DENDRITIC gully networks: big
//     channels from many small ones, the way real drainage organises.
//  4. STRATA (strata.go). The surface is terraced through the strata list
//     where it is steep: every hard layer becomes a cliff with a bench on top
//     of it, every soft layer a slope — the stepped ledges of the Colorado
//     Plateau — while mesa tops and lowlands stay smooth. The strata heights
//     are warped by noise and may dip, so the ledges wander.
//  5. DROPLETS (droplet.go). Particle hydraulic erosion at full resolution:
//     rills and gullies, deposition in the hollows, erodibility from the
//     strata hardness so hard ledges survive and soft slopes are cut.
//     Parallel by checkerboard blocks so the result is the same on any
//     number of workers.
//  6. THERMAL (thermal.go). Talus: material above its angle of repose slides
//     down, with the angle taken from the layer — soft layers relax to scree
//     slopes, hard layers keep near-vertical cliffs, and what falls piles up
//     as aprons at their feet.
//  7. DETAIL (detail.go). Fine texture the erosion is too coarse to make:
//     slickrock with its joint cracks on the hard layers, crumbly bumps on
//     the soft ones, nothing on sand.
//
// Then the masks (masks.go): flow, slope, deposition, cliff, strata index and
// height, curvature, and an albedo hint, which the material and placement
// phases read.
//
// # Determinism and cancellation
//
// Same Settings, same bytes, on any machine of one architecture and any
// worker count: every parallel loop writes disjoint data, droplet RNG streams
// are keyed by (seed, pass, block), and no step depends on scheduling. Every
// loop checks the Pool's Err so a cancelled run stops within a row.
package terrain

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// Pool is the worker pool and cancellation a generation runs on.
// procgen.Env is one (its Parallel and Err); NewPool makes a plain one.
type Pool interface {
	// Parallel runs fn(i) for i in [0, n) on the pool's workers and returns
	// the first error, ErrCancelled-like errors included.
	Parallel(n int, fn func(i int) error) error
	// Err is non-nil once the work has been called off.
	Err() error
}

// NewPool is a Pool of workers goroutines (0: every core) cancelled by ctx.
func NewPool(ctx context.Context, workers int) Pool {
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return &plainPool{ctx: ctx, workers: workers}
}

type plainPool struct {
	ctx     context.Context
	workers int
}

func (p *plainPool) Err() error { return p.ctx.Err() }

func (p *plainPool) Parallel(n int, fn func(i int) error) error {
	if n <= 0 {
		return nil
	}
	var (
		next  atomic.Int64
		once  sync.Once
		first error
		stop  atomic.Bool
		wg    sync.WaitGroup
	)
	for w := 0; w < min(p.workers, n); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				i := int(next.Add(1) - 1)
				if i >= n {
					return
				}
				if err := p.ctx.Err(); err != nil {
					once.Do(func() { first = err })
					stop.Store(true)
					return
				}
				if err := fn(i); err != nil {
					once.Do(func() { first = err })
					stop.Store(true)
					return
				}
			}
		}()
	}
	wg.Wait()
	return first
}

// Grid is a square heightfield: (N+1)² samples, row-major from MinZ, sample
// (i, j) at (MinX + i·Cell, MinZ + j·Cell).
type Grid struct {
	N          int
	Cell       float64
	MinX, MinZ float64
	H          []float32
}

// NewGrid is a flat grid of n cells a side, cell metres each, centred on the
// origin.
func NewGrid(n int, cell float64) *Grid {
	s := float64(n) * cell
	return &Grid{N: n, Cell: cell, MinX: -s / 2, MinZ: -s / 2, H: make([]float32, (n+1)*(n+1))}
}

// S is the samples a side.
func (g *Grid) S() int { return g.N + 1 }

// Size is the side in metres.
func (g *Grid) Size() float64 { return float64(g.N) * g.Cell }

// X and Z are sample columns' and rows' world coordinates.
func (g *Grid) X(i int) float64 { return g.MinX + float64(i)*g.Cell }
func (g *Grid) Z(j int) float64 { return g.MinZ + float64(j)*g.Cell }

// At is sample (i, j), clamped into the grid.
func (g *Grid) At(i, j int) float32 {
	s := g.N + 1
	i = min(max(i, 0), s-1)
	j = min(max(j, 0), s-1)
	return g.H[j*s+i]
}

// Sample is the bilinear height at world (x, z), clamped to the edge.
func (g *Grid) Sample(x, z float64) float32 {
	return bilinear(g.H, g.N+1, (x-g.MinX)/g.Cell, (z-g.MinZ)/g.Cell)
}

// Range is the lowest and highest sample.
func (g *Grid) Range() (lo, hi float32) {
	lo, hi = float32(math.Inf(1)), float32(math.Inf(-1))
	for _, v := range g.H {
		lo, hi = min(lo, v), max(hi, v)
	}
	return
}

// Clone is a deep copy.
func (g *Grid) Clone() *Grid {
	c := *g
	c.H = append([]float32(nil), g.H...)
	return &c
}

// bilinear samples an s x s array at fractional sample coordinates.
func bilinear(h []float32, s int, fx, fz float64) float32 {
	fx = min(max(fx, 0), float64(s-1))
	fz = min(max(fz, 0), float64(s-1))
	i, j := min(int(fx), s-2), min(int(fz), s-2)
	if s < 2 {
		return h[0]
	}
	u, v := float32(fx-float64(i)), float32(fz-float64(j))
	k := j*s + i
	a := h[k] + (h[k+1]-h[k])*u
	b := h[k+s] + (h[k+s+1]-h[k+s])*u
	return a + (b-a)*v
}

// Phase names a step of Generate, for progress and timings.
type Phase string

const (
	PhaseShape    Phase = "shape"
	PhaseCanyons  Phase = "canyons"
	PhaseDrainage Phase = "drainage"
	PhaseStrata   Phase = "strata"
	PhaseDroplets Phase = "droplets"
	PhaseThermal  Phase = "thermal"
	PhaseDetail   Phase = "detail"
	PhaseMasks    Phase = "masks"
)

// phaseWeights are each phase's rough share of a full-resolution run, for a
// progress bar that moves evenly.
var phaseWeights = []struct {
	p Phase
	w float32
}{
	{PhaseShape, 0.06}, {PhaseCanyons, 0.04}, {PhaseDrainage, 0.10}, {PhaseStrata, 0.02},
	{PhaseDroplets, 0.48}, {PhaseThermal, 0.18}, {PhaseDetail, 0.02}, {PhaseMasks, 0.10},
}

// Result is a generated field and everything measured on it.
type Result struct {
	Grid *Grid
	// Deposit is how much material each sample gained from erosion (metres):
	// droplet and talus deposition, and drainage filling its pits.
	Deposit []float32
	// Masks are the 0..1 masks by name (MaskNames), (N+1)² each.
	Masks map[string][]float32
	// StrataIndex is the strata list index of the surface at each sample.
	StrataIndex []uint8
	// Albedo is an RGB colour hint per sample, 0..255.
	Albedo []uint8
	// Paths are the canyon centrelines as carved, tributaries included, in
	// world XZ: for the preview, and for later stages that follow a canyon.
	Paths []CarvedPath
	// Timings are each phase's wall time.
	Timings map[Phase]time.Duration
	// Droplets is how many droplets were simulated.
	Droplets int
	// WindowsCached and WindowsEroded count a tiled union's erosion windows
	// taken from Union.Cache and eroded this time (tiled_cache.go).
	WindowsCached, WindowsEroded int

	// For a union (union.go): PathRegions is the provider each of Paths
	// came from, Lead the provider whose knobs the global erosion ran
	// with. model is the per-sample material model (RegionWeights reads
	// it); nil for a classic terrain.
	PathRegions []int
	Lead        int
	model       *fieldModel

	// Vents are the volcanic vents the providers' landforms placed (world
	// XZ; landform.go), and VentRegions the provider each came from (0 for
	// a classic terrain). Nil without volcanoes.
	Vents       []Vent
	VentRegions []int
	// Lobes are the vents' lava lobes (world XZ; landform.go Lobes), for the
	// ground material's lava rule. Nil without volcanoes or lobes.
	Lobes []LobeMeta
}

// Progress is told how far through Generate is, 0..1, and the phase.
type Progress func(phase Phase, done float32)

// Generate builds the field from s on pool: the classic one-square terrain,
// which is the one-provider case of GenerateUnion (union.go) — one material
// owning every sample, the shared passes' knobs straight from s — and is
// pinned byte for byte by golden_test.go.
func Generate(s *Settings, pool Pool, progress Progress) (*Result, error) {
	if err := s.Check(); err != nil {
		return nil, err
	}
	n := s.Cells()
	g := NewGrid(n, s.Cell)
	res := &Result{Grid: g, Deposit: make([]float32, len(g.H)), Timings: map[Phase]time.Duration{}}
	step := res.stepper(pool, progress)
	mat := compileMaterial(s, [2]float64{})
	off := make([]float32, len(g.H)) // strata offset per sample (strata.go)

	if err := step(PhaseShape, func(pr func(float32)) error {
		if err := baseShape(s, g, CentredSquare(s.Size), pool); err != nil {
			return err
		}
		return strataOffsets(s, g, off, pool)
	}); err != nil {
		return nil, err
	}
	var hold []float32
	if s.holds() {
		hold = make([]float32, len(g.H))
	}
	if err := step(PhaseCanyons, func(pr func(float32)) error {
		paths, err := carveCanyonsHold(s, g, nil, pool, hold)
		if err == nil && hold != nil {
			err = stampHold(s, g, hold, pool)
		}
		res.Paths = paths
		res.Vents = Vents(s, CentredSquare(s.Size), [2]float64{})
		res.Lobes = Lobes(s, CentredSquare(s.Size), [2]float64{})
		if len(res.Vents) > 0 {
			res.VentRegions = make([]int, len(res.Vents))
		}
		return err
	}); err != nil {
		return nil, err
	}
	fd := &field{g: g, m: singleModel(mat, off), deposit: res.Deposit, k: knobsOf(s)}
	if hold != nil {
		fd.hold, fd.held = hold, append([]float32(nil), g.H...)
	}
	if err := runSharedPasses(fd, res, step, pool); err != nil {
		return nil, err
	}
	return res, nil
}

// stepper is Generate's phase runner: it times each phase into res.Timings
// and reports progress weighted by phaseWeights.
func (res *Result) stepper(pool Pool, progress Progress) func(Phase, func(func(float32)) error) error {
	var base float32
	report := func(p Phase, f float32) {
		if progress == nil {
			return
		}
		w := float32(0)
		for _, pw := range phaseWeights {
			if pw.p == p {
				w = pw.w
				break
			}
		}
		progress(p, base+w*min(max(f, 0), 1))
	}
	return func(p Phase, fn func(func(float32)) error) error {
		t0 := time.Now()
		err := fn(func(f float32) { report(p, f) })
		res.Timings[p] += time.Since(t0)
		for _, pw := range phaseWeights {
			if pw.p == p {
				base += pw.w
			}
		}
		if err == nil {
			err = pool.Err()
		}
		return err
	}
}

// runSharedPasses is the erosion and the masks over a field: drainage,
// terrace, droplets, thermal, detail, masks.
func runSharedPasses(fd *field, res *Result, step func(Phase, func(func(float32)) error) error, pool Pool) error {
	if err := erodeField(fd, res, step, pool); err != nil {
		return err
	}
	return step(PhaseMasks, func(pr func(float32)) error {
		return computeMasks(fd, res, pool)
	})
}

// erodeField is the shared passes before the masks.
func erodeField(fd *field, res *Result, step func(Phase, func(func(float32)) error) error, pool Pool) error {
	if err := step(PhaseDrainage, func(pr func(float32)) error {
		return drainage(fd, pool, pr)
	}); err != nil {
		return err
	}
	if err := step(PhaseStrata, func(pr func(float32)) error {
		return terrace(fd, pool)
	}); err != nil {
		return err
	}
	if err := step(PhaseDroplets, func(pr func(float32)) error {
		nd, err := droplets(fd, pool, pr)
		res.Droplets += nd
		return err
	}); err != nil {
		return err
	}
	if err := step(PhaseThermal, func(pr func(float32)) error {
		if err := thermal(fd, pool, pr); err != nil {
			return err
		}
		// Held ground (stamps.go) back to its carved shape before the
		// detail texture goes on.
		return fd.applyHold(pool)
	}); err != nil {
		return err
	}
	return step(PhaseDetail, func(pr func(float32)) error {
		return detail(fd, pool)
	})
}

// rows runs fn over every row of an s-sample grid in parallel.
func rows(pool Pool, s int, fn func(j int)) error {
	return pool.Parallel(s, func(j int) error {
		if err := pool.Err(); err != nil {
			return err
		}
		fn(j)
		return nil
	})
}

// errorf is fmt.Errorf with the package's prefix.
func errorf(format string, args ...any) error {
	return fmt.Errorf("terrain: "+format, args...)
}
