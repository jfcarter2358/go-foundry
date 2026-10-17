package sdf

// strata.go — sedimentary layering: a shape weathered back layer by layer.
//
// Canyon country is a stack of beds that resist weathering differently. A
// hard sandstone stands as a near-vertical face; a soft shale or siltstone
// beneath it weathers back faster, so the hard bed overhangs it — an UNDERCUT
// — and the soft bed's face slopes back up under the overhang into an alcove.
// On top of the hard bed the next soft one starts flush and recedes again: a
// ledge, a bench. Within every bed the sand was laid down in thin
// LAMINATIONS, which weather into fine grooves; in dune sandstones (Navajo,
// Wingate) they run at an angle across the bed — cross-bedding — in a
// direction that changes from one bed to the next.
//
// StrataInset models all of that as an inset added to the shape's distance:
// the surface moves in by inset(p) wherever it is, so a vertical face takes
// the profile of the layers as it is, and a top or bottom face only where it
// crosses a soft bed. The inset is a function of the STRATA COORDINATE — the
// height, tilted by the beds' dip and wavered by a warp field — and, for the
// laminations, of the position within the bed.
//
// The inset changes steeply at a bed boundary (that is the ledge), but never
// faster than MaxSlope metres per metre of height: the function then stays a
// bound on the distance with Lipschitz 1 + MaxSlope, which the mesher's block
// skipping relies on, and the overhang's ceiling slopes up at 1/MaxSlope
// rather than being a perfect horizontal — which is what a real one does.

import (
	"math"

	"foundry/internal/procgen/noise"
)

// Layer is one bed.
type Layer struct {
	// Thickness in metres.
	Thickness float64
	// Hardness 0..1: 0 is a shale that weathers back the full Inset, 1 a
	// sandstone that stands.
	Hardness float64
}

// Strata is a stack of beds, repeating upward and downward.
type Strata struct {
	Layers []Layer
	// Base is the strata coordinate of the first layer's bottom.
	Base float64
	// Dip is how the beds tilt: the coordinate is y + Dip[0]·x + Dip[1]·z.
	Dip [2]float64
	// Warp wavers the beds by WarpAmp metres (nil: flat beds).
	Warp    *Field
	WarpAmp float64
	// Inset is how far a fully soft bed weathers back, metres.
	Inset float64
	// Variation scales each bed's inset along its length by 1 ± Variation
	// (nil: uniform), so an alcove deepens and pinches out.
	Variation    *Field
	VariationAmt float64
	// Undercut 0..1 is how a softer bed under a harder one recedes: 0 a
	// uniform recess, 1 flush at its base and deepest under the overhang.
	Undercut float64
	// BedJitter 0..1 varies each bed's recession from the next (by up to
	// that fraction either way), so two soft beds are not the same notch.
	BedJitter float64
	// MaxSlope bounds the inset's change per metre of height (0: 3).
	MaxSlope float64
	// Lamination is the laminations' groove depth (metres) and LamPeriod
	// their spacing; CrossBed the steepest cross-bedding angle (radians).
	// LamPatch, when set, shows them only where it is positive: weathering
	// etches laminations out in patches, not as an even ruling.
	Lamination, LamPeriod, CrossBed float64
	LamPatch                        *Field
	// Seed picks each bed's cross-bedding direction and lamination phase.
	Seed uint64
}

// period is the stack's total thickness.
func (st *Strata) period() float64 {
	p := 0.0
	for _, l := range st.Layers {
		p += l.Thickness
	}
	return p
}

// Coord is p's strata coordinate.
func (st *Strata) Coord(p Vec3) float64 {
	c := p[1] + st.Dip[0]*p[0] + st.Dip[1]*p[2]
	if st.Warp != nil {
		c += st.WarpAmp * st.Warp.F(p)
	}
	return c
}

// LayerAt is the bed at strata coordinate c: its index in Layers, the
// repetition it is in, how far up it c is (0..1) and its bottom coordinate.
func (st *Strata) LayerAt(c float64) (idx, rep int, t, bottom float64) {
	per := st.period()
	if per <= 0 || len(st.Layers) == 0 {
		return 0, 0, 0, c
	}
	u := c - st.Base
	rep = int(math.Floor(u / per))
	u -= float64(rep) * per
	b := st.Base + float64(rep)*per
	for i, l := range st.Layers {
		if u < l.Thickness || i == len(st.Layers)-1 {
			return i, rep, Clamp(u/l.Thickness, 0, 1), b
		}
		u -= l.Thickness
		b += l.Thickness
	}
	return 0, rep, 0, b
}

// bedInset is bed idx's inset at fraction t up it, without the boundary
// blend.
func (st *Strata) bedInset(idx int, t float64) float64 {
	n := len(st.Layers)
	l := st.Layers[idx]
	base := st.Inset * math.Pow(1-Clamp(l.Hardness, 0, 1), 1.5)
	if st.BedJitter > 0 {
		u := float64(noise.Hash64(st.Seed, 0x4245, uint64(idx))>>11) / (1 << 53)
		base *= 1 + st.BedJitter*(2*u-1)
	}
	above := st.Layers[(idx+1)%n]
	if above.Hardness > l.Hardness {
		// Softer under harder: flush at the base, receding up under the
		// overhang.
		u := st.Undercut * (above.Hardness - l.Hardness)
		return base * (1 - u*(1-t)*(1-t))
	}
	return base
}

// InsetAt is the inset at p: the beds' recession blended across their
// boundaries, and the laminations.
func (st *Strata) InsetAt(p Vec3) float64 {
	if len(st.Layers) == 0 {
		return 0
	}
	c := st.Coord(p)
	idx, rep, t, bottom := st.LayerAt(c)
	n := len(st.Layers)
	th := st.Layers[idx].Thickness
	slope := st.MaxSlope
	if slope <= 0 {
		slope = 3
	}
	v := st.bedInset(idx, t)
	// The boundary nearer c, blended over a width that keeps the slope.
	if t > 0.5 {
		next := (idx + 1) % n
		w := st.blendWidth(st.bedInset(idx, 1), st.bedInset(next, 0), th, st.Layers[next].Thickness, slope)
		top := bottom + th
		if c > top-w/2 {
			v = Mix(st.bedInset(idx, 1), st.bedInset(next, 0), SmoothStep(top-w/2, top+w/2, c))
		}
	} else {
		prev := (idx + n - 1) % n
		w := st.blendWidth(st.bedInset(prev, 1), st.bedInset(idx, 0), st.Layers[prev].Thickness, th, slope)
		if c < bottom+w/2 {
			v = Mix(st.bedInset(prev, 1), st.bedInset(idx, 0), SmoothStep(bottom-w/2, bottom+w/2, c))
		}
	}
	if st.Variation != nil {
		v *= 1 + st.VariationAmt*st.Variation.F(p)
	}
	if st.Lamination > 0 && st.LamPeriod > 0 {
		v += st.laminationAt(p, c, idx, rep)
	}
	return v
}

// blendWidth is the boundary blend's width for a step of a to b.
func (st *Strata) blendWidth(a, b, thBelow, thAbove, slope float64) float64 {
	w := 1.5 * math.Abs(a-b) / slope
	return Clamp(w, 0.02*math.Min(thBelow, thAbove), 0.8*math.Min(thBelow, thAbove))
}

// laminationAt is the lamination grooves at p, in bed idx of repetition
// rep: thin grooves every LamPeriod, inclined across the bed by up to
// CrossBed in the bed's own direction.
func (st *Strata) laminationAt(p Vec3, c float64, idx, rep int) float64 {
	h := noise.Hash64(st.Seed, uint64(idx), uint64(int64(rep)+1<<32))
	a := float64(h&0xffff) / 65536 * 2 * math.Pi
	tilt := math.Tan(st.CrossBed * float64(h>>16&0xffff) / 65536)
	phase := float64(h>>32&0xffff) / 65536
	period := st.LamPeriod * (0.7 + 0.6*float64(h>>48&0xff)/256)
	s, co := math.Sincos(a)
	u := (c+tilt*(co*p[0]+s*p[2]))/period + phase
	g := 1 - math.Abs(math.Sin(math.Pi*u))
	amp := st.Lamination
	if st.LamPatch != nil {
		amp *= SmoothStep(-0.05, 0.35, st.LamPatch.F(p))
	}
	return amp * g * g * g
}

// StrataInset is s weathered back by the strata st (see the file comment).
func StrataInset(s Shape, st Strata) Shape {
	f := s.F
	slope := st.MaxSlope
	if slope <= 0 {
		slope = 3
	}
	l := s.Lip + slope
	if st.Lamination > 0 && st.LamPeriod > 0 {
		l += 3 * math.Pi * st.Lamination / st.LamPeriod
		if st.LamPatch != nil {
			l += st.Lamination * st.LamPatch.Lip * 4
		}
	}
	return Shape{
		F:   func(p Vec3) float64 { return f(p) + st.InsetAt(p) },
		Box: s.Box,
		Lip: l,
	}
}
