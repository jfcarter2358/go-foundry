package terrain

// stamps.go — authored landforms (docs/adventure/PHASE_C.md): a level
// designer's mountains, plateaus, ledges, ramps and cuts, placed where the
// layout needs them rather than wherever the noise put them.
//
// A stamp is a kind and a list of points, each with a radius and a height:
//
//   - MOUNTAIN: a massif whose crest runs through the points (one point is a
//     single peak). Each point's Height is its rise above the ground and
//     Radius the flank's reach. The profile is concave (steepest near the
//     crest, easing out at the foot) and broken by a ridged noise into spurs
//     and gullies (Ridges) and a ragged outline (Rough). Segments combine by
//     the larger rise, so a crest through several points is one massif.
//   - PLATEAU: a flat-topped rise of Height over the ground, its top Radius
//     out from the crest line (one point: a round table), its rim falling
//     over Falloff metres. Strata and talus turn the rim into cliffs.
//   - PLATFORM: each point is a flat disc of Radius at the ABSOLUTE height
//     Height — a ledge on a mountainside, a terrace for ruins — blended into
//     the ground over Falloff metres. Ground is cut down and filled up to it.
//   - RAMP: the points are a path; its surface is Radius either side of the
//     path at the ABSOLUTE height interpolated along it, blended out over
//     Falloff — a road up a slope, a long descent for the ruins.
//   - CUT: the path (or point) lowers the ground by Height (a depth), Radius
//     either side, easing out over Falloff — a notch, a pit, a pass.
//
// Stamps are part of the BASE SHAPE (applied by the landform shaper after
// every other landform, in list order), so drainage, droplets and talus work
// them over like any other ground and the far backdrop continues them. A
// stamp's Hold (0..1) then keeps its footprint's shape through that erosion:
// the eroded height is blended back toward the post-carve one by Hold within
// the footprint (holdMask; applied before the detail pass). A ledge the
// player must stand on wants Hold 1; a mountain wants 0 and the erosion.
//
// An empty stamp list is no stamps: Landform.Any is unchanged and the classic
// base shape's bytes do not move.

import (
	"math"

	"foundry/internal/procgen/noise"
)

// StampKind is what a stamp makes.
type StampKind int

const (
	StampMountain StampKind = iota
	StampPlateau
	StampPlatform
	StampRamp
	StampCut
)

// StampKindNames are the UI spellings, indexed by StampKind.
var StampKindNames = []string{"mountain", "plateau", "platform", "ramp", "cut"}

// StampKindFromName parses a StampKindNames entry; unknown is mountain.
func StampKindFromName(s string) StampKind {
	for i, n := range StampKindNames {
		if n == s {
			return StampKind(i)
		}
	}
	return StampMountain
}

// StampPoint is one of a stamp's points.
type StampPoint struct {
	X, Z float64
	// Radius is the point's reach (m); Height its rise, absolute height or
	// depth by the kind (file comment).
	Radius, Height float64
}

// Stamp is one authored landform.
type Stamp struct {
	Kind   StampKind
	Points []StampPoint
	// Falloff is the blend width outside Radius (m): a plateau's rim, a
	// platform's or ramp's edge, a cut's bank. Unused by a mountain.
	Falloff float64
	// Rough is 0..1: ragged outlines and, on a mountain, lumpy flanks.
	Rough float64
	// Ridges is 0..1: a mountain's spurs and gullies.
	Ridges float64
	// Strength is 0..1: how fully a platform or ramp flattens the ground to
	// its height (1: exactly).
	Strength float64
	// Hold is 0..1: how much of the footprint's shape survives the erosion.
	Hold float64
	Seed uint64
}

// stampShaper is one stamp, compiled.
type stampShaper struct {
	st           Stamp
	minX, minZ   float64
	maxX, maxZ   float64
	rough, ridge noise.Fractal
	reach        float64 // the largest radius + falloff
}

func newStampShaper(st Stamp) *stampShaper {
	sh := &stampShaper{st: st}
	if st.Falloff < 0 {
		sh.st.Falloff = 0
	}
	sh.minX, sh.minZ = math.Inf(1), math.Inf(1)
	sh.maxX, sh.maxZ = math.Inf(-1), math.Inf(-1)
	rmax := 1.0
	for _, p := range st.Points {
		r := math.Max(p.Radius, 0.5)
		rmax = math.Max(rmax, r)
		reach := r + sh.st.Falloff
		if st.Kind == StampMountain {
			reach = r * 1.35
		}
		reach += st.Rough * math.Max(sh.st.Falloff, 0.25*r)
		sh.reach = math.Max(sh.reach, reach)
		sh.minX, sh.maxX = math.Min(sh.minX, p.X-reach), math.Max(sh.maxX, p.X+reach)
		sh.minZ, sh.maxZ = math.Min(sh.minZ, p.Z-reach), math.Max(sh.maxZ, p.Z+reach)
	}
	sh.rough = noise.Fractal{Seed: noise.Hash64(st.Seed, 0x5354_524F), Octaves: 4, Frequency: 2.5 / rmax, Gain: 0.5}
	sh.ridge = noise.Fractal{Seed: noise.Hash64(st.Seed, 0x5354_5249), Octaves: 5, Frequency: 2.2 / rmax, Gain: 0.5,
		Shape: noise.ShapeRidged}
	return sh
}

// inside reports whether (x, z) can be touched at all.
func (sh *stampShaper) inside(x, z float64) bool {
	return len(sh.st.Points) > 0 && x >= sh.minX && x <= sh.maxX && z >= sh.minZ && z <= sh.maxZ
}

// segment is the nearest point of the stamp's path to (x, z) on segment k
// (k = -1 with one point): its distance, and the radius and height there.
func (sh *stampShaper) segment(k int, x, z float64) (d, r, h float64) {
	pts := sh.st.Points
	if len(pts) == 1 || k < 0 {
		p := pts[0]
		return math.Hypot(x-p.X, z-p.Z), p.Radius, p.Height
	}
	a, b := pts[k], pts[k+1]
	dx, dz := b.X-a.X, b.Z-a.Z
	l2 := dx*dx + dz*dz
	t := 0.0
	if l2 > 0 {
		t = noise.Clamp(((x-a.X)*dx+(z-a.Z)*dz)/l2, 0, 1)
	}
	px, pz := a.X+dx*t, a.Z+dz*t
	return math.Hypot(x-px, z-pz), a.Radius + (b.Radius-a.Radius)*t, a.Height + (b.Height-a.Height)*t
}

// segments is how many segments the path has (1 for a single point).
func (sh *stampShaper) segments() int { return max(len(sh.st.Points)-1, 1) }

// weight is 0..1: the stamp's footprint at distance d of a point of radius
// r (its flat part 1, easing to 0 over Falloff), with the ragged outline.
func (sh *stampShaper) weight(d, r, x, z float64) float64 {
	f := math.Max(sh.st.Falloff, 0.01)
	if sh.st.Rough > 0 {
		d += sh.st.Rough * math.Max(f, 0.25*r) * 0.6 * sh.rough.At2(x, z)
	}
	return 1 - noise.SmoothStep(r, r+f, d)
}

// at applies the stamp to height h at (x, z).
func (sh *stampShaper) at(x, z, h float64) float64 {
	if !sh.inside(x, z) {
		return h
	}
	st := &sh.st
	n := sh.segments()
	switch st.Kind {
	case StampMountain:
		best := 0.0
		for k := 0; k < n; k++ {
			d, r, hh := sh.segment(k, x, z)
			r = math.Max(r, 0.5)
			u := d / r
			if st.Rough > 0 {
				u *= 1 + 0.3*st.Rough*sh.rough.At2(x, z)
			}
			if u >= 1 {
				continue
			}
			prof := math.Pow(1-u, 1.7)
			if st.Ridges > 0 {
				rid := noise.Clamp((sh.ridge.At2(x, z)+1)*0.5, 0, 1)
				// Spurs stand out low on the flanks, the crest stays whole.
				amt := 0.55 * st.Ridges * noise.SmoothStep(0.05, 0.6, u)
				prof *= 1 - amt + amt*rid*1.3
			}
			best = math.Max(best, hh*prof)
		}
		return h + best
	case StampPlateau:
		best := 0.0
		for k := 0; k < n; k++ {
			d, r, hh := sh.segment(k, x, z)
			best = math.Max(best, hh*sh.weight(d, r, x, z))
		}
		return h + best
	case StampPlatform:
		// Each point its own disc: the strongest disc wins.
		w, target := 0.0, h
		for k := range st.Points {
			p := st.Points[k]
			d := math.Hypot(x-p.X, z-p.Z)
			if wk := sh.weight(d, p.Radius, x, z); wk > w {
				w, target = wk, p.Height
			}
		}
		return h + (target-h)*w*noise.Clamp(st.Strength, 0, 1)
	case StampRamp:
		bd, w, target := math.Inf(1), 0.0, h
		for k := 0; k < n; k++ {
			d, r, hh := sh.segment(k, x, z)
			if d < bd {
				bd, w, target = d, sh.weight(d, r, x, z), hh
			}
		}
		return h + (target-h)*w*noise.Clamp(st.Strength, 0, 1)
	case StampCut:
		best := 0.0
		for k := 0; k < n; k++ {
			d, r, hh := sh.segment(k, x, z)
			best = math.Max(best, hh*sh.weight(d, r, x, z))
		}
		return h - best
	}
	return h
}

// footprint is 0..1: how much of (x, z) the stamp owns, for its Hold.
func (sh *stampShaper) footprint(x, z float64) float64 {
	if !sh.inside(x, z) {
		return 0
	}
	best := 0.0
	for k := 0; k < sh.segments(); k++ {
		d, r, _ := sh.segment(k, x, z)
		f := math.Max(sh.st.Falloff, 2)
		if sh.st.Kind == StampMountain {
			f = 0.35 * r
			r *= 0.65
		}
		best = math.Max(best, 1-noise.SmoothStep(r, r+f, d))
	}
	return best
}

// stampsOn reports whether any stamp has points.
func (l *Landform) stampsOn() bool {
	for _, st := range l.Stamps {
		if len(st.Points) > 0 {
			return true
		}
	}
	return false
}

// compileStamps is the stamps with points, compiled.
func compileStamps(sts []Stamp) []*stampShaper {
	var out []*stampShaper
	for _, st := range sts {
		if len(st.Points) > 0 {
			out = append(out, newStampShaper(st))
		}
	}
	return out
}

// holds reports whether anything asks to survive the erosion: a stamp or a
// canyon with Hold.
func (s *Settings) holds() bool {
	for _, st := range s.Landform.Stamps {
		if st.Hold > 0 && len(st.Points) > 0 {
			return true
		}
	}
	for _, c := range s.Canyons {
		if c.Hold > 0 && len(c.Path) >= 2 {
			return true
		}
	}
	return false
}

// stampHold raises hold to each held stamp's Hold × footprint.
func stampHold(s *Settings, g *Grid, hold []float32, pool Pool) error {
	var held []*stampShaper
	for _, sh := range compileStamps(s.Landform.Stamps) {
		if sh.st.Hold > 0 {
			held = append(held, sh)
		}
	}
	if len(held) == 0 {
		return nil
	}
	sz := g.S()
	return rows(pool, sz, func(j int) {
		z := g.Z(j)
		for i := 0; i < sz; i++ {
			x := g.X(i)
			for _, sh := range held {
				if w := float32(noise.Clamp(sh.st.Hold, 0, 1) * sh.footprint(x, z)); w > hold[j*sz+i] {
					hold[j*sz+i] = w
				}
			}
		}
	})
}

// applyHold blends the eroded heights back toward the held ones.
func (f *field) applyHold(pool Pool) error {
	if f.hold == nil {
		return nil
	}
	g := f.g
	sz := g.S()
	return rows(pool, sz, func(j int) {
		for i := 0; i < sz; i++ {
			k := j*sz + i
			if w := f.hold[k]; w > 0 {
				g.H[k] += (f.held[k] - g.H[k]) * w
			}
		}
	})
}
