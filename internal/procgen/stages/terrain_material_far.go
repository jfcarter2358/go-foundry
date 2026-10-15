package stages

// terrain_material_far.go — the far backdrop's material (the terrain's
// terrain_far.go): the same splat as the map's, its layers, tints, strata
// and varnish, over weights of its own laid on the backdrop's coarse lattice.
//
// The map's weights come from its masks, which stop at its edge (the splat
// clamps there, which over the one-cell stitch is right and over kilometres
// would paint the edge's colour out to the horizon). The backdrop has no
// masks — it is not eroded — so its weights come from its heights alone:
// rock on its steep ground (by the same rock_slope band, eased two degrees
// lower because a slope measured over a 16 m cell is gentler than the same
// wall measured over the map's), cracked ground in patches on its flats,
// sand everywhere else. Gravel needs washes, which it has none of. Seen from
// a kilometre and through the haze, what matters is that rock walls and sand
// slopes sit where they do on the map, in the same colours: they do, because
// they are the same textures.
//
// Written as `<material>_far` (gen_<generator>_<id>_far) beside the map's,
// which the terrain stage's far_material names; the macro variation is
// spread over three times the distance, the scale the backdrop is seen at.

import (
	"math"

	"foundry/internal/material"
	"foundry/internal/procgen"
	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/terrain"
)

// farSlopeEase is how much lower the backdrop's rock band sits (degrees).
const farSlopeEase = 2

// farGroundWeights is the backdrop's four weights (sand, cracked, gravel —
// always 0 — and rock) at every sample of its lattice, h being its heights,
// by a terrain_material's params p and the map's height range (for the
// caprock). parallel runs the rows (RunContext.Parallel). Shared with
// region_material, which evaluates it once per environment.
func farGroundWeights(parallel func(n int, f func(i int) error) error, fm *terrain.FarMeta, h []float32, p procgen.Params,
	seed uint64, heightLo, heightHi float32) ([]float32, error) {
	n, c := fm.Samples, fm.Cell
	rs, fs := p.Vec2("rock_slope"), p.Vec2("flat_slope")
	cover := p.Float("cracked_cover")
	brk, bs := p.Float("breakup"), math.Max(p.Float("breakup_scale"), 0.5)*3
	// Caprock as on the map, by the map's height range.
	capAmt, capH := p.Float("cap_rock"), p.Vec2("cap_height")
	hspan := math.Max(float64(heightHi-heightLo), 1e-3)
	w := make([]float32, 4*n*n)
	at := func(i, j int) float64 { return float64(h[min(max(j, 0), n-1)*n+min(max(i, 0), n-1)]) }
	if err := parallel(n, func(j int) error {
		z := fm.Bounds.MinZ + float64(j)*c
		for i := 0; i < n; i++ {
			x := fm.Bounds.MinX + float64(i)*c
			gx := (at(i+1, j) - at(i-1, j)) / (2 * c)
			gz := (at(i, j+1) - at(i, j-1)) / (2 * c)
			nz := func(s uint64) float64 { return noise.FBM2(noise.Hash64(seed, s), x/bs, z/bs, 3, 0.5) }
			slope := math.Atan(math.Hypot(gx, gz))*180/math.Pi + nz(1)*brk*8
			rock := noise.SmoothStep(rs[0]-farSlopeEase, rs[1]-farSlopeEase, slope)
			if capAmt > 0 {
				hf := (at(i, j)-float64(heightLo))/hspan + nz(5)*brk*0.05
				rock = math.Max(rock, capAmt*noise.SmoothStep(capH[0], capH[1], hf)*noise.SmoothStep(-0.35, 0.15, nz(6)))
			}
			flat := 1 - noise.SmoothStep(fs[0], fs[1], slope)
			patch := noise.SmoothStep(-0.25, 0.25, nz(4)+(cover-0.5)*1.2)
			cracked := flat * patch * 0.7
			k := j*n + i
			rem := 1 - rock
			wc := rem * cracked
			w[4*k], w[4*k+1], w[4*k+2], w[4*k+3] = float32(rem-wc), float32(wc), 0, float32(rock)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return w, nil
}

func writeFarMaterial(rc *procgen.RunContext, t *terrain.Terrain, p procgen.Params, layers [4]*TextureLayer, covers [3]*TextureLayer,
	tints [4][3]float64, a *TerrainMaterialArtifact) error {
	fm := t.Meta.Far
	h, err := t.FarHeights()
	if err != nil {
		return err
	}
	n, c := fm.Samples, fm.Cell
	w, err := farGroundWeights(rc.Parallel, fm, h, p, rc.Seed, a.HeightLo, a.HeightHi)
	if err != nil {
		return err
	}
	// Snow and ash on the backdrop too (terrain_material_cover.go).
	w, nc, err := farCoverWeights(rc.Parallel, fm, h, w, p, t.Meta.Vents, rc.Seed, snowRangeOf(p, t, nil))
	if err != nil {
		return err
	}
	pix := make([]uint8, 4*n*n)
	var pix2 []uint8
	if nc > 4 {
		pix2 = make([]uint8, 4*n*n)
	}
	for k := 0; k < n*n; k++ {
		for c := 0; c < nc; c++ {
			v := uint8(noise.Clamp(float64(w[nc*k+c]), 0, 1)*255 + 0.5)
			if c < 4 {
				pix[4*k+c] = v
			} else {
				pix2[4*k+c-4] = v
			}
		}
	}
	png, err := terrain.EncodeRGBA(n, n, pix)
	if err != nil {
		return err
	}
	if err := rc.WriteFile("far_weights.png", png); err != nil {
		return err
	}
	fa := *a
	fa.Material = a.Material + "_far"
	fa.Weights = mapRef(rc, "far_weights.png")
	fa.Weights2 = ""
	if pix2 != nil {
		png2, err := terrain.EncodeRGBA(n, n, pix2)
		if err != nil {
			return err
		}
		if err := rc.WriteFile("far_weights2.png", png2); err != nil {
			return err
		}
		fa.Weights2 = mapRef(rc, "far_weights2.png")
		a.FarWeights2 = fa.Weights2
	}
	fa.Min = [2]float64{fm.Bounds.MinX - c/2, fm.Bounds.MinZ - c/2}
	fa.Max = [2]float64{fm.Bounds.MaxX + c/2, fm.Bounds.MaxZ + c/2}
	d := terrainMaterialDoc(&fa, p, layers, covers, tints)
	farMaterialTweaks(d, p)
	d.Doc = "Generated terrain backdrop: the map's layers splatted by the backdrop's slopes"
	if err := writeMaterial(rc, d); err != nil {
		return err
	}
	a.FarMaterial, a.FarWeights = fa.Material, fa.Weights
	rc.Logf("far material %s (set the terrain stage's far_material to it): %d² weights at %g m", fa.Material, n, c)
	return nil
}

// farMaterialTweaks turns a map's splat material d (made with params p) into
// its backdrop's: a softer blend, no anti-tiling or relief, the macro
// variation three times wider, no detail normal.
func farMaterialTweaks(d *material.Doc, p procgen.Params) {
	// A softer height blend than the map's: over weights a lattice cell
	// apart, a crisp one cuts the layers into the lattice's squares.
	if e := d.Find(material.KindSplat); e != nil {
		e.Set("blend", material.FloatValue(float32(math.Max(p.Float("blend"), 0.7))))
		// No anti-tiling: a kilometre off, the repeat is below a pixel and
		// the extra reads would buy nothing. (Layers 5–8 too: an eight-layer
		// region material's; at their defaults they are not written.)
		for i := 0; i < material.SplatLayersMax; i++ {
			e.Set(material.SplatLayerParam(i, "anti_tile"), material.BoolValue(false))
			e.Set(material.SplatLayerParam(i, "relief"), material.FloatValue(0))
		}
	}
	// The layers' repeat, scaled up (far_scale): a 10 m rock repeat seen up
	// a kilometre-high far peak tiles into a regular stipple.
	if k := p.Float("far_scale"); k > 0 && k != 1 {
		if e := d.Find(material.KindSplat); e != nil {
			for i := 0; i < material.SplatLayersMax; i++ {
				name := material.SplatLayerParam(i, "scale")
				if _, ok := e.Params[name]; ok {
					e.Set(name, material.FloatValue(float32(round3(float64(e.Float(name))*k))))
				}
			}
		}
	}
	if e := d.Find(material.KindMacroVariation); e != nil {
		e.Set("scale", material.FloatValue(float32(p.Float("macro_scale")*3)))
	}
	// No close-up grain: nothing out there is seen close up.
	d.RemoveEffect(material.KindDetailNormal)
}
