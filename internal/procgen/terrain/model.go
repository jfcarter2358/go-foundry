package terrain

// model.go — the MATERIAL MODEL the shared passes read: what the ground is
// made of at each sample, and so how fast it erodes, how it terraces, what
// angle its talus stands at, what texture it takes and what colour it is.
//
// In a classic one-square terrain there is one model: the Settings' strata
// stack, its strata offset per sample, the talus angles, the detail. A
// union of environment regions (union.go) has one per region — a canyon's
// hard sandstone stack beside a savannah's soft beds — and every sample is
// owned by one region or, inside a transition band, shared by a few. So the
// passes never touch a compiledStrata directly any more; they ask this file
// about SAMPLE k:
//
//	erod(k, h)          the erosion factor at height h (droplets)
//	drainK(k, h)        the stream-power constant × that factor (drainage)
//	talus(k, h)         the allowed drop per cell before scree slides (thermal)
//	terraceDelta(...)   how far the terrace remap moves the surface
//	hardness(k, h)      the layer's hardness (detail)
//
// and each is evaluated per region — at that region's OWN strata coordinate
// h + off_r, since every region's beds wander and dip their own way — and
// blended by the sample's region weights.
//
// The storage is sparse, because almost every sample is owned outright:
//
//	reg[k] < mixed     sample k belongs to region reg[k] alone; off[k] is its
//	                   strata offset. The answer is that region's, with NO
//	                   blending arithmetic — the classic terrain is this case
//	                   everywhere, which is how it stays byte-identical.
//	reg[k] >= mixed    a band sample: reg[k]-mixed entries from mix[at[k]],
//	                   each a region, its weight (the few largest, summing to
//	                   1) and its strata offset there.
//
// A model is a view the tiled erosion (tiled.go) can cut a window out of by
// copying reg/off/at rows: the mix entries are shared, never copied.

import (
	"math"

	"foundry/internal/procgen/noise"
)

// mixed tags a band sample in fieldModel.reg (see the file comment); the
// region count is therefore below it.
const mixed = 0xF0

// MaxRegions is the most providers one union may blend.
const MaxRegions = mixed

// maxMix is the most regions one sample blends: the weights are trimmed to
// the largest few and renormalised.
const maxMix = 4

// material is one region's compiled material model.
type material struct {
	c *compiledStrata
	// terrace gate: tangents of the slopes the stepping fades in over.
	terraceLo, terraceHi float64
	// drainK is the stream-power constant per iteration (Strength·0.015).
	drainK float32
	// talus: tangents of the soft layers' repose angle and the hard layers'
	// cliff angle.
	tanLo, tanHi float64
	// detail: amplitude and noises (detail.go).
	detailAmp float32
	swell     noise.Fractal
	crumb     noise.Fractal
	crackSeed uint64
	jointSp   float64
	// masks: the cliff mask's angles, the sand colour, the albedo variation.
	cliffLo, cliffHi float64
	sand             [3]float64
	vary             noise.Fractal
	// anchor is subtracted from world XZ before any of the noises above:
	// they run in the region's own (preset) coordinates, as its shape does.
	anchor [2]float64
}

// compileMaterial is s's material model, its noises run at world - anchor.
func compileMaterial(s *Settings, anchor [2]float64) *material {
	t := s.Thermal
	return &material{
		c:         s.Strata.compile(),
		terraceLo: math.Tan(s.Strata.SlopeLo * math.Pi / 180),
		terraceHi: math.Tan(max(s.Strata.SlopeHi, s.Strata.SlopeLo+1) * math.Pi / 180),
		drainK:    float32(s.Drainage.Strength * 0.015),
		tanLo:     math.Tan(t.TalusAngle * math.Pi / 180),
		tanHi:     math.Tan(min(t.CliffAngle, 89.5) * math.Pi / 180),
		detailAmp: float32(s.Detail.Amplitude),
		swell:     noise.Fractal{Seed: noise.Hash64(s.Seed, 0x534C_4B52), Octaves: 3, Frequency: 1 / 14.0, Gain: 0.5, Shape: noise.ShapeRidged},
		crumb:     noise.Fractal{Seed: noise.Hash64(s.Seed, 0x4352_554D), Octaves: 3, Frequency: 1 / 5.0, Gain: 0.55},
		crackSeed: noise.Hash64(s.Seed, 0x4352_4B53),
		jointSp:   max(s.Detail.JointSpacing, 0.5),
		cliffLo:   s.CliffAngle - 6, cliffHi: s.CliffAngle + 6,
		sand:   s.SandColor,
		vary:   noise.Fractal{Seed: noise.Hash64(s.Seed, 0x414C_4244), Octaves: 4, Frequency: 1.0 / 40, Gain: 0.55},
		anchor: anchor,
	}
}

// local is world (x, z) in the material's own coordinates.
func (m *material) local(x, z float64) (float64, float64) { return x - m.anchor[0], z - m.anchor[1] }

// talusLimit is the allowed drop per cell of distance (before the cell
// factor) at strata coordinate y.
func (m *material) talusLimit(y float64) float32 {
	hd := float64(m.c.hardness(y))
	f := noise.SmoothStep(0.35, 0.8, hd)
	return float32(m.tanLo + (m.tanHi-m.tanLo)*f)
}

// mixEntry is one region's share of a band sample.
type mixEntry struct {
	r   uint8
	w   float32
	off float32
}

// fieldModel is a grid's per-sample material model (file comment).
type fieldModel struct {
	mats []*material
	reg  []uint8
	off  []float32
	at   []int32 // nil when no sample is mixed
	mix  []mixEntry
}

// singleModel is the model of a field owned by one material throughout.
func singleModel(m *material, off []float32) *fieldModel {
	return &fieldModel{mats: []*material{m}, reg: make([]uint8, len(off)), off: off}
}

// entries is band sample k's regions.
func (m *fieldModel) entries(k int) []mixEntry {
	n := int(m.reg[k]) - mixed
	a := int(m.at[k])
	return m.mix[a : a+n]
}

// dominant is the region with the largest share of sample k, and its
// strata offset there.
func (m *fieldModel) dominant(k int) (int, float32) {
	if r := m.reg[k]; r < mixed {
		return int(r), m.off[k]
	}
	best := mixEntry{w: -1}
	for _, e := range m.entries(k) {
		if e.w > best.w {
			best = e
		}
	}
	return int(best.r), best.off
}

// erod is the erosion factor at height h.
func (m *fieldModel) erod(k int, h float32) float32 {
	if r := m.reg[k]; r < mixed {
		return m.mats[r].c.erodibility(float64(h + m.off[k]))
	}
	sum := float32(0)
	for _, e := range m.entries(k) {
		sum += e.w * m.mats[e.r].c.erodibility(float64(h+e.off))
	}
	return sum
}

// drainK is the stream-power constant times the erosion factor at h.
func (m *fieldModel) drainK(k int, h float32) float32 {
	if r := m.reg[k]; r < mixed {
		mat := m.mats[r]
		return mat.drainK * mat.c.erodibility(float64(h+m.off[k]))
	}
	sum := float32(0)
	for _, e := range m.entries(k) {
		mat := m.mats[e.r]
		sum += e.w * (mat.drainK * mat.c.erodibility(float64(h+e.off)))
	}
	return sum
}

// hardness is the layer hardness at h.
func (m *fieldModel) hardness(k int, h float32) float32 {
	if r := m.reg[k]; r < mixed {
		return m.mats[r].c.hardness(float64(h + m.off[k]))
	}
	sum := float32(0)
	for _, e := range m.entries(k) {
		sum += e.w * m.mats[e.r].c.hardness(float64(h+e.off))
	}
	return sum
}

// talus is the allowed drop per cell of distance at h (times the cell size
// by the caller).
func (m *fieldModel) talus(k int, h float32) float32 {
	if r := m.reg[k]; r < mixed {
		return m.mats[r].talusLimit(float64(h + m.off[k]))
	}
	sum := float32(0)
	for _, e := range m.entries(k) {
		sum += e.w * m.mats[e.r].talusLimit(float64(h+e.off))
	}
	return sum
}

// terraceDelta is how far the terrace remap moves a sample at height h on
// ground of the given slope (gradient magnitude), and whether it moves at
// all.
func (m *fieldModel) terraceDelta(k int, h float32, slope float64) (float32, bool) {
	one := func(mat *material, off float32) (float32, bool) {
		if mat.c.terrace <= 0 {
			return 0, false
		}
		w := noise.SmoothStep(mat.terraceLo, mat.terraceHi, slope)
		if w <= 0 {
			return 0, false
		}
		y := float64(h) + float64(off)
		return float32((mat.c.terraceHeight(y) - y) * w), true
	}
	if r := m.reg[k]; r < mixed {
		return one(m.mats[r], m.off[k])
	}
	sum, any := float32(0), false
	for _, e := range m.entries(k) {
		if d, ok := one(m.mats[e.r], e.off); ok {
			sum += e.w * d
			any = true
		}
	}
	return sum, any
}

// anyTerrace reports whether some material terraces at all.
func (m *fieldModel) anyTerrace() bool {
	for _, mat := range m.mats {
		if mat.c.terrace > 0 {
			return true
		}
	}
	return false
}

// offOf is region r's strata offset at sample k, if r has a share there.
func (m *fieldModel) offOf(k, r int) (float32, bool) {
	if rr := m.reg[k]; rr < mixed {
		return m.off[k], int(rr) == r
	}
	for _, e := range m.entries(k) {
		if int(e.r) == r {
			return e.off, true
		}
	}
	return 0, false
}

// window is the model of the s x s square of an fs-sample field whose
// corner is sample (i0, j0): rows copied, the mix entries shared.
func (m *fieldModel) window(fs, i0, j0, s int) *fieldModel {
	w := &fieldModel{mats: m.mats, reg: make([]uint8, s*s), off: make([]float32, s*s), mix: m.mix}
	if m.at != nil {
		w.at = make([]int32, s*s)
	}
	for j := 0; j < s; j++ {
		src := (j0+j)*fs + i0
		copy(w.reg[j*s:(j+1)*s], m.reg[src:src+s])
		copy(w.off[j*s:(j+1)*s], m.off[src:src+s])
		if m.at != nil {
			copy(w.at[j*s:(j+1)*s], m.at[src:src+s])
		}
	}
	return w
}

// downsample is the model of the coarse grid the drainage phase routes on:
// coarse sample (I, J) is fine sample (I·f, J·f) (clamped), and each of its
// regions' strata offsets is the box average of that region's offset over
// the same window the heights are averaged over (fluvial.go), samples where
// the region has no share counting as the centre's. For a field owned by one
// region that is exactly the classic box average.
func (m *fieldModel) downsample(fs, f, sc int, pool Pool) (*fieldModel, error) {
	c := &fieldModel{mats: m.mats, reg: make([]uint8, sc*sc), off: make([]float32, sc*sc)}
	// Coarse band samples' entries are made per row, then joined in row
	// order, so the layout does not depend on scheduling.
	rowsMix := make([][]mixEntry, sc)
	rowsAt := make([][]int32, sc)
	avg := func(I, J, r int, centre float32) float32 {
		so, n := float32(0), float32(0)
		for dj := -f / 2; dj <= f/2; dj++ {
			for di := -f / 2; di <= f/2; di++ {
				i, j := min(max(I*f+di, 0), fs-1), min(max(J*f+dj, 0), fs-1)
				v, ok := m.offOf(j*fs+i, r)
				if !ok {
					v = centre
				}
				so += v
				n++
			}
		}
		return so / n
	}
	err := rows(pool, sc, func(J int) {
		for I := 0; I < sc; I++ {
			kc := min(J*f, fs-1)*fs + min(I*f, fs-1)
			ci := J*sc + I
			if r := m.reg[kc]; r < mixed {
				c.reg[ci] = r
				c.off[ci] = avg(I, J, int(r), m.off[kc])
				continue
			}
			es := m.entries(kc)
			c.reg[ci] = m.reg[kc]
			if rowsAt[J] == nil {
				rowsAt[J] = make([]int32, sc)
			}
			rowsAt[J][I] = int32(len(rowsMix[J]))
			for _, e := range es {
				rowsMix[J] = append(rowsMix[J], mixEntry{r: e.r, w: e.w, off: avg(I, J, int(e.r), e.off)})
			}
		}
	})
	if err != nil {
		return nil, err
	}
	for J := 0; J < sc; J++ {
		if rowsAt[J] == nil {
			continue
		}
		if c.at == nil {
			c.at = make([]int32, sc*sc)
		}
		base := int32(len(c.mix))
		for I := 0; I < sc; I++ {
			if c.reg[J*sc+I] >= mixed {
				c.at[J*sc+I] = base + rowsAt[J][I]
			}
		}
		c.mix = append(c.mix, rowsMix[J]...)
	}
	return c, nil
}
