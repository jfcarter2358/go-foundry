// Package wallsheet builds the rock faces of a heightfield's cliffs as real
// geometry: a shell of signed-distance rock laid over every wall, with the
// structure a weathered sandstone cliff has — hard beds standing as ledges
// over soft ones that have weathered back under them, vertical joints that
// cut the face into panels set back by different amounts, alcoves hollowed
// at the foot of thick beds, a rough surface with its edges rounded — so a
// canyon's walls have silhouettes, overhangs and shadow instead of a smooth
// extruded surface with a texture on it (docs/canyon/PHASE_8.md).
//
// # Why a shell, and why only outward
//
// A heightfield cannot overhang, and its walls are a few samples stretched
// over tens of metres. The shell's surface is the terrain's own surface
// pushed OUT along its normal by up to Depth on steep ground, less a recess
// function R(p) in 0..Depth: a ledge is where R is 0 (the full Depth out), a
// soft bed's hollow where R is near Depth (nearly back to the terrain). The
// shell therefore always lies in front of the terrain, which never has to
// change: no hole to cut in the tiles, no seam between the two, and a later
// stage that recooks the tiles (drift) is unaffected. On flat ground — the
// canyon floor in front of a wall, the mesa top behind its rim — the shell
// sits Hidden (12 cm) UNDER the terrain, so it is hidden there and comes out of the
// ground exactly where the wall starts to rise.
//
// # The field
//
// F(p) = (p.y − h) / √(1 + |∇h|²) − offset(p), the first-order signed
// distance to the heightfield (exact on a plane, close on the walls the
// shell is built for) less the offset steep(p)·(Depth − R(p)) −
// (1 − steep(p))·Hidden. steep is the slope mask (SteepDeg), zero where the
// local relief is under MinWall (a dune face is steep and is not a cliff),
// softened over a metre or two so the shell eases out of the ground. The
// rock is kept to a BAND within Margin of the steep ground, so a shell is a
// strip along its wall and not a slab under the whole map; where the band
// ends the shell's side goes down through the ground, unseen.
//
// # Sections
//
// The band is cut into Section-metre squares on a world-aligned grid; each
// is meshed alone (sdf.Polygonize, its grid's origin snapped to the same
// world lattice, so neighbours sample the field at the same points) and
// closed by the square's sides, which meet the next section's inside the
// rock.
package wallsheet

import (
	"math"

	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/sdf"
	"foundry/internal/procgen/terrain"
)

// Settings shape the walls.
type Settings struct {
	// Depth is how far the shell stands out of the terrain at most (m): the
	// deepest recess there can be.
	Depth float64
	// SteepDeg: ground steeper than the first starts to be wall, by the
	// second it is all wall.
	SteepDeg [2]float64
	// MinWall is the relief (m, over Reach) steep ground needs to be a wall.
	MinWall, Reach float64
	// Margin is how far the band runs past the steep ground (m).
	Margin float64
	// BedSpacing is a bed's typical thickness (m); SoftShare the share of
	// beds that are soft and weather back; SoftDepth their recess as a
	// share of Depth.
	BedSpacing, SoftShare, SoftDepth float64
	// JointSpacing is the joints' typical spacing (m), JointDepth how deep
	// an open joint cuts (share of Depth), PanelDepth how far a panel
	// between joints can stand back (share of Depth).
	JointSpacing, JointDepth, PanelDepth float64
	// Alcoves is the chance a panel's bed has an alcove at its foot.
	Alcoves float64
	// Rough is the surface's roughness (m).
	Rough float64
	// Section is a section's side (m).
	Section float64
	Seed    uint64
	// Weight, when set, scales how far the shell stands out at world
	// (x, z), 0..1: an environment region's share
	// (docs/regions/PHASE_4.md), so its walls stand out of the terrain
	// fully where it owns the ground and sink back under it across its
	// transition band. The wall mask, the band and the sections are the
	// unweighted ones (a mask cut at the weight's half would end the shell
	// in a cut face); sections the region has no share of are not meshed.
	// Nil: 1 everywhere.
	Weight func(x, z float64) float64
}

// Defaults is a sandstone canyon's walls.
func Defaults() Settings {
	return Settings{Depth: 2.4, SteepDeg: [2]float64{40, 55}, MinWall: 6, Reach: 8, Margin: 3.5,
		BedSpacing: 3.2, SoftShare: 0.45, SoftDepth: 0.75, JointSpacing: 17, JointDepth: 0.55, PanelDepth: 0.35,
		Alcoves: 0.06, Rough: 0.14, Section: 32}
}

// Field is a terrain's walls: the per-sample fields the shell is built from.
type Field struct {
	S      Settings
	g      *terrain.Grid
	gx, gz []float32 // the height's gradient
	ux, uz []float32 // the uphill direction, from the gradient blurred over a few metres
	steep  []float32 // 0..1, softened
	wt     []float32 // Settings.Weight per sample (nil: none)
	band   []float32 // metres to the nearest wall sample
	top    []float32 // the highest ground within TopReach: a wall's rim above
	lo, hi float32   // the height range
	// Carve, when set, is a void's signed distance (negative inside) cut out
	// of the shell: a cave's (stages/cave.go). Nil: none.
	Carve func(p sdf.Vec3) float64
}

// Hidden is how far under the terrain the shell lies on flat ground (m):
// enough that the terrain's triangles, the shell's bilinear field and the
// shell's simplification never bring it up through the ground.
const Hidden = 0.12

// TopReach is how far (m) a wall's rim is looked for, across a sample: about
// the plan width of the tallest walls.
const TopReach = 12

// NewField works out a terrain's walls.
func NewField(g *terrain.Grid, s Settings, pool terrain.Pool) (*Field, error) {
	sz := g.S()
	f := &Field{S: s, g: g, gx: make([]float32, sz*sz), gz: make([]float32, sz*sz), steep: make([]float32, sz*sz)}
	if s.Weight != nil {
		f.wt = make([]float32, sz*sz)
	}
	f.lo, f.hi = g.Range()
	// Relief: the height range over a Reach-square window (two separable
	// passes of a running max and min).
	r := max(int(math.Round(s.Reach/g.Cell/2)), 1)
	mx, mn := windowMaxMin(g.H, sz, r)
	err := pool.Parallel(sz, func(j int) error {
		for i := 0; i < sz; i++ {
			k := j*sz + i
			i0, i1 := max(i-1, 0), min(i+1, sz-1)
			j0, j1 := max(j-1, 0), min(j+1, sz-1)
			gx := float64(g.H[j*sz+i1]-g.H[j*sz+i0]) / (float64(i1-i0) * g.Cell)
			gz := float64(g.H[j1*sz+i]-g.H[j0*sz+i]) / (float64(j1-j0) * g.Cell)
			f.gx[k], f.gz[k] = float32(gx), float32(gz)
			slope := math.Atan(math.Hypot(gx, gz)) * 180 / math.Pi
			st := noise.SmoothStep(s.SteepDeg[0], s.SteepDeg[1], slope)
			st *= noise.SmoothStep(0.6*s.MinWall, s.MinWall, float64(mx[k]-mn[k]))
			f.steep[k] = float32(st)
			if f.wt != nil {
				f.wt[k] = float32(s.Weight(g.X(i), g.Z(j)))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// A wall sample is one at least half wall; the band is the distance to
	// the nearest, before the softening widens the mask.
	wall := make([]bool, sz*sz)
	for k, v := range f.steep {
		wall[k] = v >= 0.5
	}
	f.band = chamfer(wall, sz, g.Cell)
	f.top, _ = windowMaxMin(g.H, sz, max(int(math.Round(TopReach/g.Cell)), 1))
	// Uphill, smoothed: at a wall's foot and just past its rim it still
	// points into the wall, which the rim clamp in F needs.
	ur := max(int(math.Round(s.Depth/g.Cell)), 1)
	bx := boxBlur(boxBlur(f.gx, sz, ur), sz, ur)
	bz := boxBlur(boxBlur(f.gz, sz, ur), sz, ur)
	f.ux, f.uz = make([]float32, sz*sz), make([]float32, sz*sz)
	for k := range bx {
		if l := math.Hypot(float64(bx[k]), float64(bz[k])); l > 1e-4 {
			f.ux[k], f.uz[k] = float32(float64(bx[k])/l), float32(float64(bz[k])/l)
		}
	}
	sr := max(int(math.Round(1.0/g.Cell)), 1)
	f.steep = boxBlur(boxBlur(f.steep, sz, sr), sz, sr)
	return f, nil
}

// sample is a per-sample field bilinear at (x, z), clamped to the grid.
func (f *Field) sample(v []float32, x, z float64) float64 {
	g := f.g
	sz := g.S()
	fx := math.Max(0, math.Min((x-g.MinX)/g.Cell, float64(sz-1)))
	fz := math.Max(0, math.Min((z-g.MinZ)/g.Cell, float64(sz-1)))
	i, j := min(int(fx), sz-2), min(int(fz), sz-2)
	u, w := fx-float64(i), fz-float64(j)
	a := float64(v[j*sz+i])*(1-u) + float64(v[j*sz+i+1])*u
	b := float64(v[(j+1)*sz+i])*(1-u) + float64(v[(j+1)*sz+i+1])*u
	return a*(1-w) + b*w
}

// Steep is the wall mask at (x, z).
func (f *Field) Steep(x, z float64) float64 { return f.sample(f.steep, x, z) }

// Band is the distance (m) from (x, z) to the nearest wall sample.
func (f *Field) Band(x, z float64) float64 { return f.sample(f.band, x, z) }

// Recess is R(p): how far back from the shell's full Depth the rock face
// is at p, metres in 0..0.95·Depth (see the package comment). Also its bed
// and panel, for colouring.
func (f *Field) Recess(p sdf.Vec3) (r float64, bed int64, panel uint64) {
	s := &f.S
	seed := s.Seed
	x, y, z := p[0], p[1], p[2]
	d := s.Depth

	// BEDS: the height cut into beds of unequal thickness (the coordinate
	// bent by a slow noise of itself, monotone), the cut wandering across
	// the face.
	inv := 1 / s.BedSpacing
	yr := y * inv
	yr += 0.85*noise.Value2(noise.Hash64(seed, 1), yr*0.37, 2.9)*0.5 + 0.3*noise.Value2(noise.Hash64(seed, 2), yr*0.83, 5.3)*0.5
	// Formations: over tens of metres the beds thicken and thin — a massive
	// unit of a few thick beds over a thin-bedded one — so the ledges do
	// not come at one pitch up the whole wall. Value noise of y/18 changes
	// at most 2·1.875/18 a metre, so amplitude 2.6/BedSpacing keeps the
	// bend's slope under half the coordinate's own (1/BedSpacing): the beds
	// keep their order, between about half and twice their spacing thick.
	yr += 2.6 / s.BedSpacing * noise.Value2(noise.Hash64(seed, 9), y/18, 7.7)
	yb := yr + 0.9*noise.FBM2(noise.Hash64(seed, 3), x*0.012, z*0.012, 2, 0.5)
	bf := math.Floor(yb)
	bed = int64(bf)
	u := yb - bf // 0 at the bed's base, 1 at its top
	hb := noise.CellUnit(noise.Hash64(seed, 4, uint64(bed)))
	// Under the rim: the caprock, a hard unit a few metres thick that the
	// joints do not notch (they would crenellate the skyline).
	below := f.sample(f.top, x, z) - y
	capT := 2.5 + 2.5*noise.Clamp(0.5+0.5*noise.FBM2(noise.Hash64(seed, 10), x*0.03, z*0.03, 2, 0.5), 0, 1)
	rim := noise.SmoothStep(capT+2, capT, below) // 1 in the caprock
	soft := hb < s.SoftShare && rim < 0.5
	// How deep this bed weathers here: patchy along the face.
	along := 0.65 + 0.7*noise.Clamp(0.5+0.5*noise.FBM2(noise.Hash64(seed, 5, uint64(bed)), x*0.05, z*0.05, 2, 0.5), 0, 1)
	if soft {
		// A hollow with rounded ends: flush at the bed's base and top, back
		// most of the way between — the hard bed above overhangs it.
		k := noise.SmoothStep(0, 0.22, u) * noise.SmoothStep(1, 0.8, u)
		r = d * s.SoftDepth * (0.6 + 0.4*noise.CellUnit(noise.Hash64(seed, 6, uint64(bed)))) * along * k
	} else {
		// A ledge: out at the front, its lower edge eased back (an under-lip),
		// its top edge sharp.
		r = d * 0.08 * along
		r += d * 0.18 * (1 - noise.SmoothStep(0, 0.25, u))
	}

	// JOINTS: Voronoi cells in plan, a joint spacing across, are the panels;
	// each stands back its own amount, and the borders between them are
	// open joints — some cut through only some beds.
	js := s.JointSpacing
	c := noise.Cellular2(noise.Hash64(seed, 7), x/js, z/js, 0.9, noise.MetricEuclidean)
	panel = c.ID
	notch := 1 - rim
	r += d * s.PanelDepth * noise.CellUnit(noise.Hash64(c.ID, 1)) * notch
	edge := c.Edge() * js * 0.5 // about the metres to the border
	// A joint is open through a run of a few beds at a time — a fracture
	// that runs down the face and dies out — not bed by bed (that is a
	// brick wall).
	run := int64(math.Floor(float64(bed) / (2 + 4*noise.CellUnit(noise.Hash64(c.ID, 6)))))
	open := noise.CellUnit(noise.Hash64(c.ID, uint64(run), 2))
	if open < 0.4 {
		w := 0.3 + 0.6*noise.CellUnit(noise.Hash64(c.ID, 3))
		r += d * s.JointDepth * (1 - noise.SmoothStep(0, w, edge)) * notch
	}

	// ALCOVES: now and then a panel's soft bed is hollowed into a broad
	// scoop at the foot of the bed above.
	if soft && s.Alcoves > 0 && noise.CellUnit(noise.Hash64(c.ID, uint64(bed), 4)) < s.Alcoves {
		fp := c.Point
		dx, dz := x-fp[0]*js, z-fp[1]*js
		rw := js * (0.35 + 0.25*noise.CellUnit(noise.Hash64(c.ID, 5)))
		q := (dx*dx + dz*dz) / (rw * rw)
		uy := (u - 0.7) / 0.6
		if a := 1 - q - uy*uy; a > 0 {
			r += d * 0.9 * math.Sqrt(a)
		}
	}

	// ROUGHNESS: lumps at a metre, grain at a few decimetres.
	r += s.Rough * (noise.FBM2(noise.Hash64(seed, 8), x*0.9+y*0.3, z*0.9-y*0.7, 3, 0.55) + 0.5)
	return math.Max(0, math.Min(r, 0.95*d)), bed, panel
}

// Terrain is p's signed distance to the terrain's surface (first order:
// positive above it).
func (f *Field) Terrain(p sdf.Vec3) float64 {
	x, z := p[0], p[2]
	gx, gz := f.sample(f.gx, x, z), f.sample(f.gz, x, z)
	return (p[1] - f.sample(f.g.H, x, z)) / math.Sqrt(1+gx*gx+gz*gz)
}

// F is the shell's signed distance at p, unbounded by any section.
func (f *Field) F(p sdf.Vec3) float64 {
	x, z := p[0], p[2]
	h := f.sample(f.g.H, x, z)
	gx, gz := f.sample(f.gx, x, z), f.sample(f.gz, x, z)
	fh := (p[1] - h) / math.Sqrt(1+gx*gx+gz*gz)
	off := -Hidden
	st := f.sample(f.steep, x, z)
	if f.wt != nil && st > 0 {
		st *= f.sample(f.wt, x, z)
	}
	if st > 0 {
		r, _, _ := f.Recess(p)
		off = st*(f.S.Depth-r) - (1-st)*Hidden
	}
	d := math.Max(fh-off, f.Band(x, z)-f.S.Margin)
	// The rim clamp: pushed out along a rim's upturned normal the shell would
	// stand up over the mesa top as a parapet. It may rise at most RimLip
	// over the ground a little uphill of it (into the wall): nothing on a
	// face (the wall above is far higher), and on a top, the top.
	reach := 1.2 * f.S.Depth
	up := f.sample(f.g.H, x+f.sample(f.ux, x, z)*reach, z+f.sample(f.uz, x, z)*reach)
	d = math.Max(d, p[1]-up-RimLip)
	if f.Carve != nil {
		// A cave's void (stages/cave.go) cut out of the shell, so its mouth
		// opens through the wall.
		d = math.Max(d, -f.Carve(p))
	}
	return d
}

// RimLip is how far a caprock may stand above the top behind it (m).
const RimLip = 0.25

// Section is one square of the band to mesh: its plan rectangle and the
// heights its rock spans.
type Section struct {
	I, J   int // its index on the world-aligned section grid
	Lo, Hi sdf.Vec3
}

// Sections are the squares of the section grid holding any wall.
func (f *Field) Sections() []Section {
	g := f.g
	sz := g.S()
	sec := f.S.Section
	n := int(math.Ceil(g.Size() / sec))
	var out []Section
	for sj := 0; sj < n; sj++ {
		for si := 0; si < n; si++ {
			x0, z0 := g.MinX+float64(si)*sec, g.MinZ+float64(sj)*sec
			i0 := max(int(math.Floor((x0-g.MinX)/g.Cell)), 0)
			j0 := max(int(math.Floor((z0-g.MinZ)/g.Cell)), 0)
			i1 := min(int(math.Ceil((x0+sec-g.MinX)/g.Cell)), sz-1)
			j1 := min(int(math.Ceil((z0+sec-g.MinZ)/g.Cell)), sz-1)
			wall := false
			lo, hi := math.Inf(1), math.Inf(-1)
			for j := j0; j <= j1; j++ {
				for i := i0; i <= i1; i++ {
					k := j*sz + i
					if float64(f.band[k]) > f.S.Margin+f.S.Depth {
						continue
					}
					wall = wall || (f.steep[k] >= 0.5 && (f.wt == nil || f.wt[k] > 0.02))
					lo = math.Min(lo, float64(g.H[k]))
					hi = math.Max(hi, float64(g.H[k]))
				}
			}
			if !wall {
				continue
			}
			out = append(out, Section{I: si, J: sj,
				Lo: sdf.Vec3{x0, lo - 1, z0}, Hi: sdf.Vec3{x0 + sec, hi + f.S.Depth + 1, z0 + sec}})
		}
	}
	return out
}

// Shape is a section's rock: the shell cut to the section's box, with its
// box's low corner snapped so the mesher's lattice (two cells out from the
// box) lands on the world lattice of cell, as every other section's does.
func (f *Field) Shape(s Section, cell float64) sdf.Shape {
	lo, hi := s.Lo, s.Hi
	snap := func(v float64) float64 { return math.Floor(v/cell) * cell }
	blo := sdf.Vec3{snap(lo[0]), snap(lo[1]), snap(lo[2])}
	bhi := hi
	return sdf.Shape{
		F: func(p sdf.Vec3) float64 {
			// The section's box, exactly: neighbours share its sides.
			b := math.Max(math.Max(lo[0]-p[0], p[0]-hi[0]), math.Max(math.Max(lo[1]-p[1], p[1]-hi[1]), math.Max(lo[2]-p[2], p[2]-hi[2])))
			return math.Max(f.F(p), b)
		},
		Box: sdf.Box{Lo: blo, Hi: bhi},
		Lip: 2,
	}
}

// windowMaxMin is the running max and min of v over a (2r+1)² window.
func windowMaxMin(v []float32, sz, r int) (mx, mn []float32) {
	pass := func(in []float32, take func(a, b float32) float32, horiz bool) []float32 {
		out := make([]float32, len(in))
		for a := 0; a < sz; a++ {
			for b := 0; b < sz; b++ {
				var best float32
				first := true
				for d := max(b-r, 0); d <= min(b+r, sz-1); d++ {
					k := a*sz + d
					if !horiz {
						k = d*sz + a
					}
					if first {
						best, first = in[k], false
					} else {
						best = take(best, in[k])
					}
				}
				if horiz {
					out[a*sz+b] = best
				} else {
					out[b*sz+a] = best
				}
			}
		}
		return out
	}
	maxf := func(a, b float32) float32 { return max(a, b) }
	minf := func(a, b float32) float32 { return min(a, b) }
	return pass(pass(v, maxf, true), maxf, false), pass(pass(v, minf, true), minf, false)
}

// chamfer is each sample's distance (m) to the nearest set sample: a
// two-pass 3-4 chamfer transform, within a few percent of Euclidean.
func chamfer(set []bool, sz int, cell float64) []float32 {
	const inf = math.MaxFloat32 / 4
	d := make([]float32, sz*sz)
	for k, s := range set {
		if !s {
			d[k] = inf
		}
	}
	a, b := float32(cell), float32(cell*math.Sqrt2)
	at := func(i, j int) float32 {
		if i < 0 || j < 0 || i >= sz || j >= sz {
			return inf
		}
		return d[j*sz+i]
	}
	for j := 0; j < sz; j++ {
		for i := 0; i < sz; i++ {
			k := j*sz + i
			d[k] = min(d[k], at(i-1, j)+a, at(i, j-1)+a, at(i-1, j-1)+b, at(i+1, j-1)+b)
		}
	}
	for j := sz - 1; j >= 0; j-- {
		for i := sz - 1; i >= 0; i-- {
			k := j*sz + i
			d[k] = min(d[k], at(i+1, j)+a, at(i, j+1)+a, at(i+1, j+1)+b, at(i-1, j+1)+b)
		}
	}
	return d
}

// boxBlur is a (2r+1)² box mean of v (sz² samples), edges clamped.
func boxBlur(v []float32, sz, r int) []float32 {
	tmp := make([]float32, len(v))
	out := make([]float32, len(v))
	for j := 0; j < sz; j++ {
		for i := 0; i < sz; i++ {
			var s float32
			for d := -r; d <= r; d++ {
				s += v[j*sz+min(max(i+d, 0), sz-1)]
			}
			tmp[j*sz+i] = s / float32(2*r+1)
		}
	}
	for j := 0; j < sz; j++ {
		for i := 0; i < sz; i++ {
			var s float32
			for d := -r; d <= r; d++ {
				s += tmp[min(max(j+d, 0), sz-1)*sz+i]
			}
			out[j*sz+i] = s / float32(2*r+1)
		}
	}
	return out
}
