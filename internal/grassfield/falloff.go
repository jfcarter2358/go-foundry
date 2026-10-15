package grassfield

// falloff.go — how much of a field is drawn at a distance, and with which
// mesh. resources/shaders/grassfield_common.glsl is the same arithmetic for the
// vertex stage; the two MUST agree (falloff_test.go pins the Go side, the
// GL test the shader's), because the CPU decides how many of a run to draw
// and the vertex stage decides how much each drawn one shows.
//
// THINNING. Past Start the fraction of instances kept falls as
// (Start/d)^Thin, times a fade to nothing over the last 18% of Range, and
// times the player's density setting. Because a run is in rank order, "keep
// a fraction k" is "draw the first ceil(k·count)" — the CPU draws, per cell,
// the prefix its NEAREST point admits (KeepRank) and the vertex stage fades
// each drawn instance by its own distance (RankFade): an instance whose
// rank its distance no longer admits dissolves over a band RankBand of the
// survivors wide. A given instance always disappears at the same distance,
// and its neighbours at other distances — the thinning has no wavefront.
//
// COMPENSATION. The survivors grow to cover for the ones thinned out round
// them (Comp): their footprint goes as the square of their size, so growing
// each by keep^-½ holds the ground covered constant (Comp 1); CompUp is how
// much of that growth goes into height (a far tuft widens faster than it
// grows tall). Capped at MaxComp.
//
// TIERS. A kind has up to three meshes: LOD 0 to LOD1, LOD 1 to LOD2, LOD 2
// beyond (Tiers). Each tier draws over a distance window and fades in and
// out over TierBand of its boundary, the outgoing and incoming tiers
// dithering complementarily (grass_frag.glsl), so the switch is a soft band
// per instance rather than a line per cell. A cell whose distance span
// meets two windows draws both tiers; its instances each show the one
// their own distance picks.

import "math"

// RankBand is the fade band, as a fraction of the survivors.
const RankBand = 0.06

// MaxComp is the most a survivor grows.
const MaxComp = 3

// TierBand is the fraction of a tier boundary's distance the cross-fade
// spans (below the boundary).
const TierBand = 0.15

// Falloff is a field's distance rules, metres (already scaled by the
// player's distance setting).
type Falloff struct {
	Range, Start, Thin float32
	Comp, CompUp       float32
	// LOD1 and LOD2 are where the LOD 1 and LOD 2 meshes take over (0: that
	// tier is not used).
	LOD1, LOD2 float32
	// Density is the player's density setting: a multiplier on the kept
	// fraction, 0..1.
	Density float32
}

// DefaultFalloff is the renderer's rules before the player's settings.
var DefaultFalloff = Falloff{Range: 170, Start: 18, Thin: 1.4, Comp: 1, CompUp: 0.35, LOD1: 11, LOD2: 32, Density: 1}

// Scaled is f with its distances multiplied by dist and its density by
// density.
func (f Falloff) Scaled(dist, density float32) Falloff {
	f.Range *= dist
	f.Start *= dist
	f.LOD1 *= dist
	f.LOD2 *= dist
	f.Density *= density
	return f
}

func smoothstep(e0, e1, x float32) float32 {
	if e1 <= e0 {
		if x < e0 {
			return 0
		}
		return 1
	}
	t := min(max((x-e0)/(e1-e0), 0), 1)
	return t * t * (3 - 2*t)
}

// thin is the fraction the thinning keeps at d.
func (f Falloff) thin(d float32) float32 {
	if f.Thin > 0 && d > f.Start && f.Start > 0 {
		return float32(math.Pow(float64(f.Start/d), float64(f.Thin)))
	}
	return 1
}

// Keep is the fraction of instances drawn at d, for a kind drawn to the
// fraction rng of the range (0 or 1: all of it).
func (f Falloff) Keep(d, rng float32) float32 {
	R := f.Range
	if rng > 0 && rng < 1 {
		R *= rng
	}
	return f.thin(d) * (1 - smoothstep(R*0.82, R, d)) * min(max(f.Density, 0), 1)
}

// KeepRank is the highest rank (0..1) anything at d or further can still
// show: how much of a run a cell whose nearest point is d draws.
func (f Falloff) KeepRank(d, rng float32) float32 {
	return min(f.Keep(d, rng)*(1+RankBand), 1)
}

// RankFade is how much an instance of rank (0..1) shows when keep of them
// are kept: 1 well inside, 0 past the band.
func RankFade(keep, rank float32) float32 {
	band := RankBand * max(keep, 1e-3)
	return min(max((keep*(1+RankBand)-rank)/band, 0), 1)
}

// Grow is how much a survivor at d grows (width, height).
func (f Falloff) Grow(d float32) (w, h float32) {
	t := f.thin(d)
	g := min(float32(math.Pow(float64(t), float64(-0.5*f.Comp))), MaxComp)
	return g, float32(math.Pow(float64(g), float64(f.CompUp)))
}

// DrawCount is how many of a run of count instances a cell whose nearest
// point is dmin draws.
func (f Falloff) DrawCount(count int, dmin, rng float32) int {
	if count <= 0 {
		return 0
	}
	return min(int(math.Ceil(float64(float32(count)*f.KeepRank(dmin, rng)))), count)
}

// Tier is a tier's distance window: from, to (0: open).
type Tier struct{ From, To float32 }

// Tiers is the tier windows for a kind with lods meshes.
func (f Falloff) Tiers(lods int) []Tier {
	switch {
	case lods >= 3 && f.LOD1 > 0 && f.LOD2 > f.LOD1:
		return []Tier{{0, f.LOD1}, {f.LOD1, f.LOD2}, {f.LOD2, 0}}
	case lods >= 2 && f.LOD1 > 0:
		return []Tier{{0, f.LOD1}, {f.LOD1, 0}}
	}
	return []Tier{{0, 0}}
}

// Meets reports whether tier t is drawn anywhere in [dmin, dmax].
func (t Tier) Meets(dmin, dmax float32) bool {
	if t.To > 0 && dmin >= t.To {
		return false
	}
	if t.From > 0 && dmax < t.From-TierBand*t.From {
		return false
	}
	return true
}

// Weight is how much of an instance at d tier t shows (the two tiers either
// side of a boundary sum to 1).
func (t Tier) Weight(d float32) float32 {
	in := float32(1)
	if t.From > 0 {
		in = smoothstep(t.From-TierBand*t.From, t.From, d)
	}
	out := float32(1)
	if t.To > 0 {
		out = 1 - smoothstep(t.To-TierBand*t.To, t.To, d)
	}
	return min(in, out)
}
