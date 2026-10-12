package terrain

// far.go — the far backdrop: the terrain's landforms continued past the map's
// edge, coarse, out to the horizon (docs/canyon/PHASE_7.md, "The map edge").
//
// A generated canyon is a square: past its edge there was nothing but the
// sky's painted landscape and, standing on a rim, the edge tiles' skirts — a
// kilometre of canyon country ending at a cliff into nothing. The backdrop is
// a heightfield around the square, many times its size at a coarse spacing,
// that the stage cooks into a ring of cheap tiles (stages/terrain_far.go).
// What it must do is be the SAME country:
//
//   - Past the edge it is the same BASE SHAPE (shape.go's baseShaper: the
//     lowland noise and the mesa field, with the map's own thresholds and rim
//     widths), so mesas run on to the horizon with the same size, height and
//     ragged rims, terraced through the same strata (strata.go) where they
//     are steep, so their walls step the same way.
//   - At the edge it is the map itself: within Blend metres of the square
//     the height is eased from the map's edge sample (the nearest point of
//     the square, so a canyon that leaves the map is extruded outward) into
//     the far shape. The first ring of far samples is met by a stitch strip
//     (the stage's) whose inner edge is the map's own rim vertices.
//   - The CANYONS run on: each authored canyon whose end reaches the map's
//     edge is extended out along its last heading, its floor held, and cut
//     into the far shape with a flat-floored profile — so looking
//     down-canyon the canyon continues instead of ending in a wall at the
//     map's edge. It wanders off the heading once clear of the map, narrows,
//     and shallows out over the far half of the reach (a straight trench to
//     the horizon read as a ditch).
//   - Toward the outer edge (the last eighth of the reach) the relief is
//     eased down to the lowland, so the ring's own edge, deep in the haze,
//     is no silhouette against the sky.
//
// There is no erosion out here: at the spacing the backdrop is sampled at
// (16 m by default) and the range it is seen from, the aerial haze carries it.
// A few metres of ridged relief, faded in with the blend, stand in for the
// gullies and swells the coarse lattice cannot hold.
// Pure function of position, so any sample can be asked for in any order,
// on any worker: the stage samples tile by tile.

import (
	"math"

	"foundry/internal/procgen/noise"
)

// FarField is the backdrop's height function.
type FarField struct {
	shape  *baseShaper
	strata *compiledStrata
	stOff  noise.Fractal
	dip    [2]float64
	warp   float64
	g      *Grid
	// Blend is the easing width at the map's edge; Reach how far past the
	// edge the backdrop runs (the outer fade is on its last eighth).
	Blend, Reach float64
	slopeLo      float64
	slopeHi      float64
	cell         float64
	paths        []farPath
	lowland      float64
	// detail is the relief the backdrop's coarse lattice would otherwise
	// lack: ridged swells and gullies, a few metres, out past the blend.
	detail  noise.Fractal
	meander noise.Fractal
}

// farPath is a canyon's extension past the map: a ray from its end.
type farPath struct {
	x, z, dx, dz float64 // the end, and the unit heading outward
	half, floor  float64
}

// NewFarField is the backdrop of the field res made from s, sampled every
// cell metres, easing over blend metres from the edge and running reach
// metres past it.
func NewFarField(s *Settings, res *Result, cell, blend, reach float64) *FarField {
	f := &FarField{
		shape:  newBaseShaper(s),
		strata: s.Strata.compile(),
		stOff:  noise.Fractal{Seed: noise.Hash64(s.Seed, 0x5354_5241), Octaves: 4, Frequency: 1.0 / 260, Gain: 0.5},
		dip:    s.Strata.Dip, warp: s.Strata.Warp,
		g: res.Grid, Blend: math.Max(blend, cell), Reach: reach, cell: cell,
		// Terraced only where it is steep (a mesa's rise): on a 16 m lattice
		// the gentle lowland's partial terraces became rows of stripes.
		slopeLo: math.Tan(math.Max(s.Strata.SlopeLo, 22) * math.Pi / 180),
		slopeHi: math.Tan(math.Max(s.Strata.SlopeHi, 40) * math.Pi / 180),
		lowland: s.Base.Height,
		detail:  noise.Fractal{Seed: noise.Hash64(s.Seed, 0x4641_5244), Octaves: 3, Frequency: 1.0 / 160, Gain: 0.5, Shape: noise.ShapeRidged},
		meander: noise.Fractal{Seed: noise.Hash64(s.Seed, 0x4641_524D), Octaves: 2, Frequency: 1.0 / 420, Gain: 0.5},
	}
	g := res.Grid
	x0, z0 := g.MinX, g.MinZ
	x1, z1 := g.MinX+g.Size(), g.MinZ+g.Size()
	near := func(x, z, w float64) bool {
		d := math.Min(math.Min(x-x0, x1-x), math.Min(z-z0, z1-z))
		return d < math.Max(w, 4*g.Cell)
	}
	for _, p := range res.Paths {
		if p.Tributary || len(p.Points) < 4 {
			continue
		}
		n := len(p.Points)
		// Each end that reaches the edge, heading out: the mean direction
		// over the last few points, which the meander's last wiggle does
		// not swing.
		for _, end := range []int{0, n - 1} {
			q := p.Points[end]
			if !near(q[0], q[1], p.Width[end]) {
				continue
			}
			k := min(max(n/20, 3), n-1)
			from := p.Points[min(max(end-k, 0), n-1)]
			if end == 0 {
				from = p.Points[k]
			}
			dx, dz := q[0]-from[0], q[1]-from[1]
			l := math.Hypot(dx, dz)
			if l < 1e-6 {
				continue
			}
			f.paths = append(f.paths, farPath{x: q[0], z: q[1], dx: dx / l, dz: dz / l, half: p.Width[end] / 2, floor: p.Floor[end]})
		}
	}
	return f
}

// shapeAt is the far landform at (x, z): the base shape, terraced through
// the strata in proportion to its slope over a cell, as terrace does.
func (f *FarField) shapeAt(x, z float64, share []float64) float64 {
	h := f.shape.at(x, z, share)
	if f.strata.terrace <= 0 {
		return h
	}
	r := f.cell
	gx := (f.shape.at(x+r, z, share) - f.shape.at(x-r, z, share)) / (2 * r)
	gz := (f.shape.at(x, z+r, share) - f.shape.at(x, z-r, share)) / (2 * r)
	w := noise.SmoothStep(f.slopeLo, f.slopeHi, math.Hypot(gx, gz))
	if w <= 0 {
		return h
	}
	off := f.dip[0]*x/100 + f.dip[1]*z/100
	if f.warp > 0 {
		off += f.warp * f.stOff.At2(x, z)
	}
	y := h + off
	return h + (f.strata.terraceHeight(y)-y)*w
}

// Outside is how far (x, z) lies outside the map's square (0 inside).
func (f *FarField) Outside(x, z float64) float64 {
	g := f.g
	dx := math.Max(math.Max(g.MinX-x, x-(g.MinX+g.Size())), 0)
	dz := math.Max(math.Max(g.MinZ-z, z-(g.MinZ+g.Size())), 0)
	return math.Hypot(dx, dz)
}

// Height is the backdrop's height at (x, z). Inside the square it is the
// map's own.
func (f *FarField) Height(x, z float64) float64 {
	g := f.g
	d := f.Outside(x, z)
	cx := math.Min(math.Max(x, g.MinX), g.MinX+g.Size())
	cz := math.Min(math.Max(z, g.MinZ), g.MinZ+g.Size())
	edge := float64(g.Sample(cx, cz))
	if d <= 0 {
		return edge
	}
	share := make([]float64, max(f.shape.s.Mesa.Tiers, 1))
	far := f.shapeAt(x, z, share)
	// The canyons carried on: the nearest extension's cut.
	for _, p := range f.paths {
		// Along the ray from the end (behind it is the map's own canyon).
		t := (x-p.x)*p.dx + (z-p.z)*p.dz
		if t < 0 {
			continue
		}
		// It wanders off its heading as it goes (not at all where it leaves
		// the map, so it meets the map's canyon), narrows a little, and
		// shallows out over the far half of the reach: a ruler-straight
		// trench to the horizon read as a ditch.
		wander := 0.0
		if f.Reach > 0 {
			wander = 160 * f.meander.At2(t, float64(len(f.paths))) * noise.SmoothStep(0, 300, t)
		}
		across := math.Abs((x-p.x)*p.dz - (z-p.z)*p.dx + wander)
		half := math.Max(p.half, 1) * (1 - 0.35*noise.SmoothStep(0, 1500, t))
		u := across / half
		if u >= 1 {
			continue
		}
		// A flat floor over the middle third, walls steepening to the rim;
		// the floor falls gently on downstream.
		prof := 1 - noise.SmoothStep(0.3, 1, u)
		if f.Reach > 0 {
			prof *= 1 - noise.SmoothStep(0.4*f.Reach, 0.8*f.Reach, t)
		}
		floor := p.floor - 0.004*t
		if far > floor {
			far = floor + (far-floor)*(1-prof)
		}
	}
	// Eased out of the map's edge.
	w := noise.SmoothStep(0, f.Blend, d)
	// The relief the coarse lattice cannot carry, where the backdrop is its
	// own: ridged, a few metres, softer on the flat tops.
	far += 2.5 * (f.detail.At2(x, z) - 0.5) * w
	h := edge + (far-edge)*w
	// Eased down to the lowland at the very edge of the reach.
	if f.Reach > 0 {
		o := noise.SmoothStep(f.Reach*0.875, f.Reach, d)
		h += (math.Min(f.lowland, h) - h) * o
	}
	return h
}
