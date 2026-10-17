package terrain

// union.go — one terrain grown from several environment regions
// (docs/regions/SPEC.md, "Field providers and the union grid").
//
// A level generation doc's regions each name an environment preset, and a
// preset's terrain stage is that region's FIELD PROVIDER: its Settings say
// how its ground is shaped (lowland, mesas, canyons, strata) and what it is
// made of (erodibility, terrace, talus, detail, colours). GenerateUnion
// makes one terrain of all of them:
//
//  1. WEIGHTS. Every sample of the union grid asks the regions' weight
//     function (procgen/regions, handed in as Union.Weights — the very
//     function the regions stage's preview overlay paints) for its shares,
//     keeps the largest few (maxMix) and renormalises. A sample owned by
//     one region outright is stored as that region alone, weight exactly 1:
//     no blending arithmetic ever touches it. Each provider's SUPPORT — the
//     samples it has any share of — is measured on the way.
//  2. PROVIDERS (provider.go). Each provider is evaluated only over its
//     support, on a square sub-grid aligned to the union's samples, in its
//     OWN coordinates (world minus its anchor, so a preset's canyon paths
//     land where the region puts them): base shape, mesas (their quantiles
//     and rim gradient measured over the region's footprint rather than a
//     classic square), strata offsets, canyons.
//  3. BLEND. Heights blend by weight; each region keeps its own strata
//     offset per sample, and the material model (model.go) answers the
//     shared passes per region at that offset, blended by the same weights.
//  4. SHARED PASSES over the one grid (field.go): drainage, terrace,
//     droplets, thermal, detail — so water routes from a canyon across a
//     seam to the union's edge, and a canyon wall wears down into the
//     savannah through erosion rather than a ramp. Either GLOBALLY (one run
//     over the union) or TILED (tiled.go: windows of a few tiles with an
//     overlap margin, each with a fixed seed, cross-faded).
//  5. MASKS over the union, the cliff angle, strata and albedo from the
//     model (masks.go).
//
// The per-pass numbers no region can own per sample (drainage iterations,
// droplet run, talus iterations...) are the LEAD provider's: the one owning
// the most samples (globally; per window when tiled, so a far region never
// changes a window it does not touch).
//
// One provider whose footprint is the classic square (anchor at the origin,
// the union grid = the classic grid) is exactly Generate: every sample is
// owned outright, the provider's grid is the union's, and the passes run
// the classic arithmetic. union_test.go and the stages package pin that.

import (
	"fmt"
	"sort"
	"time"
)

// Rect is a world XZ rectangle.
type Rect struct {
	MinX, MinZ, MaxX, MaxZ float64
}

// CentredSquare is the square of side size centred on the origin: the
// classic terrain's.
func CentredSquare(size float64) Rect {
	half := size / 2
	return Rect{MinX: -half, MinZ: -half, MaxX: half, MaxZ: half}
}

// RegionWeight is one provider's share of a point.
type RegionWeight struct {
	Region int
	Weight float64
}

// WeightFunc answers every provider's share of world (x, z), appended to
// out[:0]: shares above zero summing to 1 (procgen/regions' Set.Weights).
// It must be safe for concurrent use and a pure function of the point.
type WeightFunc func(x, z float64, out []RegionWeight) []RegionWeight

// Provider is one region's field.
type Provider struct {
	// Name is for logs (the region's key).
	Name string
	// Settings is the region's terrain, from its preset. Size is ignored
	// (the region supplies its extent), Cell must be the union's, Seed is
	// the region's own.
	Settings Settings
	// Anchor is the world XZ of the preset's origin: the provider runs at
	// world - Anchor, so the preset's canyon paths are offsets from it.
	Anchor [2]float64
	// Measure is the WORLD rectangle the mesa field's quantiles and rim
	// gradient are measured over: the region's footprint bounds.
	Measure Rect
	// Extend carries the provider's canyons on past Measure's edge by this
	// many metres (an authored end within reach of the edge continues
	// straight along its heading), so a canyon drawn to a region's edge runs
	// on through the transition band into its neighbour. Only in a union of
	// more than one provider; 0 never.
	Extend float64
	// Key fingerprints everything the provider's material model compiles
	// from (its settings, seed and anchor), for the tiled erosion's window
	// cache (tiled_cache.go). Empty: windows it reaches are not cached.
	Key string
}

// Erosion modes.
const (
	ErosionGlobal = "global"
	ErosionTiled  = "tiled"
)

// Union is a generation over several providers.
type Union struct {
	// The grid: N cells a side (N+1 samples) of Cell metres from (MinX,
	// MinZ), world-aligned.
	N          int
	Cell       float64
	MinX, MinZ float64
	Providers  []Provider
	// Weights is the regions' weight function; nil only with one provider
	// (which then owns every sample).
	Weights WeightFunc
	// Seed keys the tiled erosion's windows (each window's seed is
	// Hash(Seed, window)); the global erosion uses the lead provider's.
	Seed uint64
	// Erosion is ErosionGlobal or ErosionTiled. Window is the tiled
	// erosion's window side and Margin its overlap, in cells; the window
	// grid starts at the union's corner.
	Erosion        string
	Window, Margin int
	// Lead, when > 0, names the lead provider (Lead−1): the one whose
	// global knobs (drainage iterations, droplets, talus rounds) the global
	// erosion runs with, whose strata the masks index and whose material
	// knobs a region material takes. 0 is the default: the provider owning
	// the most samples. Tiled windows always take their own window's lead.
	Lead int
	// FlowMFD draws the flow MASK by multiple flow directions (Quinn) rather
	// than one receiver per sample. A blend band and a canyon's mouth leave
	// broad, nearly flat ground; single-receiver area crosses it in dead
	// straight 0°/45° lines, which every consumer of the mask (washes in the
	// material, stones in the scatter) then draws as a ruled line. Shared
	// area spreads over flats and gathers in hollows. Heights are untouched.
	FlowMFD bool
	// Cache, with tiled erosion, keeps eroded windows between runs: a
	// window whose inputs hash the same is read back instead of eroded
	// (tiled_cache.go). Nil: every window erodes.
	Cache WindowCache
}

// Grid is the union's (flat) grid.
func (u *Union) Grid() *Grid {
	return &Grid{N: u.N, Cell: u.Cell, MinX: u.MinX, MinZ: u.MinZ, H: make([]float32, (u.N+1)*(u.N+1))}
}

// Check reports a union that cannot run.
func (u *Union) Check() error {
	if u.N < 4 || u.Cell <= 0 {
		return errorf("a union needs at least 4 cells of a positive size")
	}
	if u.N > 16384 {
		return errorf("%d cells a side is more than 16384", u.N)
	}
	if len(u.Providers) == 0 {
		return errorf("a union needs a provider")
	}
	if len(u.Providers) > MaxRegions {
		return errorf("%d providers; at most %d", len(u.Providers), MaxRegions)
	}
	if u.Weights == nil && len(u.Providers) > 1 {
		return errorf("%d providers and no weight function", len(u.Providers))
	}
	for i := range u.Providers {
		if c := u.Providers[i].Settings.Cell; c != u.Cell {
			return errorf("provider %s: cell %g, the union's is %g", u.Providers[i].Name, c, u.Cell)
		}
	}
	if u.Erosion == ErosionTiled && u.Window < 4 {
		return errorf("tiled erosion needs a window of at least 4 cells")
	}
	return nil
}

// PhaseWeights is the union's weighting phase, and PhaseErosion the tiled
// erosion (all its windows' passes).
const (
	PhaseWeights Phase = "weights"
	PhaseErosion Phase = "erosion"
)

func init() {
	// Not stepped by Generate, so its progress is unchanged.
	phaseWeights = append(phaseWeights, struct {
		p Phase
		w float32
	}{PhaseErosion, 0.78})
}

// GenerateUnion builds the union's terrain (file comment).
func GenerateUnion(u *Union, pool Pool, progress Progress) (*Result, error) {
	if err := u.Check(); err != nil {
		return nil, err
	}
	g := u.Grid()
	res := &Result{Grid: g, Deposit: make([]float32, len(g.H)), Timings: map[Phase]time.Duration{}}
	step := res.stepper(pool, progress)

	mats := make([]*material, len(u.Providers))
	for i := range u.Providers {
		mats[i] = compileMaterial(&u.Providers[i].Settings, u.Providers[i].Anchor)
	}
	var model *fieldModel
	provs := make([]*providerField, len(u.Providers))
	if err := step(PhaseShape, func(pr func(float32)) error {
		t0 := time.Now()
		m, support, err := weigh(u, g, mats, pool)
		res.Timings[PhaseWeights] = time.Since(t0)
		if err != nil {
			return err
		}
		model = m
		for i := range u.Providers {
			if support[i].empty() {
				continue
			}
			pf, err := shapeProvider(&u.Providers[i], g, support[i], pool)
			if err != nil {
				return fmt.Errorf("provider %s: %w", u.Providers[i].Name, err)
			}
			// Its relief faded toward its neighbours' level (edge.go; off
			// unless the preset asks).
			if err := fadeEdges(u.Providers[i].Settings.Edge, i, pf, g, m, pool); err != nil {
				return err
			}
			provs[i] = pf
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := step(PhaseCanyons, func(pr func(float32)) error {
		for i, pf := range provs {
			if pf == nil {
				continue
			}
			var ext *canyonExtend
			if len(u.Providers) > 1 && u.Providers[i].Extend > 0 {
				ext = &canyonExtend{rect: pf.measure, dist: u.Providers[i].Extend}
			}
			paths, err := carveCanyonsExt(&u.Providers[i].Settings, pf.g, ext, pool)
			if err != nil {
				return err
			}
			for _, p := range paths {
				res.Paths = append(res.Paths, p.offset(u.Providers[i].Anchor))
				res.PathRegions = append(res.PathRegions, i)
			}
			pv := &u.Providers[i]
			for _, v := range Vents(&pv.Settings, pv.Measure, pv.Anchor) {
				res.Vents = append(res.Vents, v)
				res.VentRegions = append(res.VentRegions, i)
			}
			res.Lobes = append(res.Lobes, Lobes(&pv.Settings, pv.Measure, pv.Anchor)...)
		}
		return blend(g, model, provs, pool)
	}); err != nil {
		return nil, err
	}
	provs = nil
	res.Lead = leadOf(model, nil, len(mats))
	if u.Lead > 0 && u.Lead <= len(u.Providers) {
		res.Lead = u.Lead - 1
	}
	res.model = model
	fd := &field{g: g, m: model, deposit: res.Deposit, k: knobsOf(&u.Providers[res.Lead].Settings)}
	// Several providers: a sharp landform of one region (a canyon wall cut
	// into ground the band's softer rock lowers by metres) must keep its
	// drainage change on its own side of the wall (fluvial.go).
	fd.k.edgeAware = len(u.Providers) > 1
	fd.mfdFlow = u.FlowMFD
	if u.Erosion == ErosionTiled {
		if err := step(PhaseErosion, func(pr func(float32)) error {
			return erodeTiled(u, fd, res, pool, pr)
		}); err != nil {
			return nil, err
		}
	} else if err := erodeField(fd, res, step, pool); err != nil {
		return nil, err
	}
	if err := step(PhaseMasks, func(pr func(float32)) error {
		return computeMasks(fd, res, pool)
	}); err != nil {
		return nil, err
	}
	return res, nil
}

// supportRect is the union samples a provider has any share of, inclusive;
// empty when i1 < i0.
type supportRect struct{ i0, j0, i1, j1 int }

func (r supportRect) empty() bool { return r.i1 < r.i0 || r.j1 < r.j0 }

func (r supportRect) grow(i, j int) supportRect {
	if r.empty() {
		return supportRect{i, j, i, j}
	}
	return supportRect{min(r.i0, i), min(r.j0, j), max(r.i1, i), max(r.j1, j)}
}

func (r supportRect) union(o supportRect) supportRect {
	if r.empty() {
		return o
	}
	if o.empty() {
		return r
	}
	return supportRect{min(r.i0, o.i0), min(r.j0, o.j0), max(r.i1, o.i1), max(r.j1, o.j1)}
}

// minWeight is the smallest share a sample keeps: below it a region's
// contribution (well under a millimetre of height) is dropped and the rest
// renormalised.
const minWeight = 1e-4

// weigh is the union's material model (offsets still zero) and each
// provider's support.
func weigh(u *Union, g *Grid, mats []*material, pool Pool) (*fieldModel, []supportRect, error) {
	s := g.S()
	m := &fieldModel{mats: mats, reg: make([]uint8, s*s), off: make([]float32, s*s)}
	sup := make([]supportRect, len(mats))
	for i := range sup {
		sup[i] = supportRect{0, 0, -1, -1}
	}
	if len(mats) == 1 {
		// One provider owns everything: no weights to ask for.
		sup[0] = supportRect{0, 0, g.N, g.N}
		return m, sup, nil
	}
	rowsMix := make([][]mixEntry, s)
	rowsAt := make([][]int32, s)
	rowSup := make([][]supportRect, s)
	err := rows(pool, s, func(j int) {
		z := g.Z(j)
		var buf []RegionWeight
		rs := make([]supportRect, len(mats))
		for i := range rs {
			rs[i] = supportRect{0, 0, -1, -1}
		}
		for i := 0; i < s; i++ {
			k := j*s + i
			buf = trimWeights(u.Weights(g.X(i), z, buf))
			for _, w := range buf {
				rs[w.Region] = rs[w.Region].grow(i, j)
			}
			if len(buf) == 1 {
				m.reg[k] = uint8(buf[0].Region)
				continue
			}
			m.reg[k] = uint8(mixed + len(buf))
			if rowsAt[j] == nil {
				rowsAt[j] = make([]int32, s)
			}
			rowsAt[j][i] = int32(len(rowsMix[j]))
			for _, w := range buf {
				rowsMix[j] = append(rowsMix[j], mixEntry{r: uint8(w.Region), w: float32(w.Weight)})
			}
		}
		rowSup[j] = rs
	})
	if err != nil {
		return nil, nil, err
	}
	for j := 0; j < s; j++ {
		for r := range sup {
			sup[r] = sup[r].union(rowSup[j][r])
		}
		if rowsAt[j] == nil {
			continue
		}
		if m.at == nil {
			m.at = make([]int32, s*s)
		}
		base := int32(len(m.mix))
		for i := 0; i < s; i++ {
			if m.reg[j*s+i] >= mixed {
				m.at[j*s+i] = base + rowsAt[j][i]
			}
		}
		m.mix = append(m.mix, rowsMix[j]...)
	}
	return m, sup, nil
}

// trimWeights keeps the shares of at least minWeight, at most maxMix of the
// largest, renormalised, sorted by region.
func trimWeights(w []RegionWeight) []RegionWeight {
	n := 0
	for _, x := range w {
		if x.Weight >= minWeight {
			w[n] = x
			n++
		}
	}
	w = w[:n]
	if len(w) == 0 {
		return w
	}
	if len(w) > maxMix {
		sort.SliceStable(w, func(a, b int) bool { return w[a].Weight > w[b].Weight })
		w = w[:maxMix]
		sort.SliceStable(w, func(a, b int) bool { return w[a].Region < w[b].Region })
	}
	if len(w) == 1 {
		w[0].Weight = 1
		return w
	}
	sum := 0.0
	for _, x := range w {
		sum += x.Weight
	}
	for k := range w {
		w[k].Weight /= sum
	}
	return w
}

// blend writes the union's heights from the providers and fills the
// model's strata offsets. An owned sample copies its provider's height and
// offset; a band sample sums them by weight.
func blend(g *Grid, m *fieldModel, provs []*providerField, pool Pool) error {
	s := g.S()
	return rows(pool, s, func(j int) {
		for i := 0; i < s; i++ {
			k := j*s + i
			if r := m.reg[k]; r < mixed {
				p := provs[r]
				q := p.index(i, j)
				g.H[k] = p.g.H[q]
				m.off[k] = p.off[q]
				continue
			}
			h := float32(0)
			for e := range m.entries(k) {
				ent := &m.mix[int(m.at[k])+e]
				p := provs[ent.r]
				q := p.index(i, j)
				h += ent.w * p.g.H[q]
				ent.off = p.off[q]
			}
			g.H[k] = h
		}
	})
}

// leadOf is the provider owning the most samples (dominant share) of the
// model, over the given window's samples (nil: all); ties go to the lower
// index.
func leadOf(m *fieldModel, idx []int, n int) int {
	count := make([]int, n)
	if idx == nil {
		for k := range m.reg {
			r, _ := m.dominant(k)
			count[r]++
		}
	} else {
		for _, k := range idx {
			r, _ := m.dominant(k)
			count[r]++
		}
	}
	best := 0
	for r := range count {
		if count[r] > count[best] {
			best = r
		}
	}
	return best
}

// RegionWeights is provider r's share of every sample of a union's grid
// (row-major, (N+1)²): 1 where it owns a sample, 0 where it has none. For a
// classic terrain (one material) it is 1 everywhere.
func (res *Result) RegionWeights(r int) []float32 {
	out := make([]float32, len(res.Grid.H))
	m := res.model
	if m == nil {
		if r == 0 {
			for k := range out {
				out[k] = 1
			}
		}
		return out
	}
	for k := range out {
		if rr := m.reg[k]; rr < mixed {
			if int(rr) == r {
				out[k] = 1
			}
			continue
		}
		for _, e := range m.entries(k) {
			if int(e.r) == r {
				out[k] = e.w
			}
		}
	}
	return out
}

// Dominant is the provider with the largest share of sample k.
func (res *Result) Dominant(k int) int {
	if res.model == nil {
		return 0
	}
	r, _ := res.model.dominant(k)
	return r
}

// offset is the path moved by d (preset coordinates to world).
func (p CarvedPath) offset(d [2]float64) CarvedPath {
	if d == [2]float64{} {
		return p
	}
	q := p
	q.Points = make([][2]float64, len(p.Points))
	for k, pt := range p.Points {
		q.Points[k] = [2]float64{pt[0] + d[0], pt[1] + d[1]}
	}
	return q
}
