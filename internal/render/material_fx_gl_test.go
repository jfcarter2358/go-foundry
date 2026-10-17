package render

// material_fx_gl_test.go — the terrain and rock material effects
// (gfx/material_fx.go, surface_fx.glsl) drawn for real: a row of cooked
// rocks, each wearing a material with one effect, on every path a cooked
// placement can take — the visibility buffer's material pass (page and flat
// pools), the forward draw, and the classic path — checking that each effect
// does what it says, that the paths agree with each other, and that the
// material pass still builds with the effects' samplers inside the 32 a
// fragment stage may have.

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/material"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// fxWritePNG writes a size² PNG painted by f to dir/name.png and returns the
// path without its extension (what a material names a map by).
func fxWritePNG(t *testing.T, dir, name string, size int, f func(x, y int) color.NRGBA) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.SetNRGBA(x, y, f(x, y))
		}
	}
	p := filepath.Join(dir, name)
	fh, err := os.Create(p + ".png")
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	if err := png.Encode(fh, img); err != nil {
		t.Fatal(err)
	}
	return p
}

// fxSolid is a solid 2D world texture under name, uploaded directly.
func fxSolid(t *testing.T, name string, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	if err := gfx.UpdateTextureFromRGBA(name, img); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if tex, ok := gfx.Textures[name]; ok {
			gl.DeleteTextures(1, &tex)
			delete(gfx.Textures, name)
		}
	})
}

// fxMaterial installs a surface material with one effect.
func fxMaterial(t *testing.T, name, texture, kind string, params map[string]material.Value) {
	t.Helper()
	d := &material.Doc{Name: name, Domain: material.DomainSurface, Texture: texture}
	e := d.AddEffect(kind)
	for k, v := range params {
		e.Set(k, v)
	}
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("%s: %v", name, probs)
	}
	material.Put(d)
	t.Cleanup(func() { material.Remove(name) })
}

func TestMaterialFxOnEveryPath(t *testing.T) {
	w := newVisWorld(t)
	dir := t.TempDir()
	tv, fv, cv, v2 := material.TextureValue, material.FloatValue, material.ColorValue, material.Vec2Value

	// The maps: a splat whose weights are all its first layer, a pure red
	// layer with a height and a normal map; a ground blend whose ground is
	// green and whose height is 0 m over a ±10 m range (the 16 bits split).
	weights := fxWritePNG(t, dir, "weights", 8, func(x, y int) color.NRGBA { return color.NRGBA{255, 0, 0, 0} })
	red := fxWritePNG(t, dir, "red", 64, func(x, y int) color.NRGBA { return color.NRGBA{220, 30, 30, 255} })
	fxWritePNG(t, dir, "red_height", 64, func(x, y int) color.NRGBA { return color.NRGBA{128, 128, 128, 255} })
	fxWritePNG(t, dir, "red_normal", 64, func(x, y int) color.NRGBA { return color.NRGBA{128, 128, 255, 255} })
	ground := fxWritePNG(t, dir, "ground", 8, func(x, y int) color.NRGBA { return color.NRGBA{40, 200, 40, 255} })
	height := fxWritePNG(t, dir, "height", 8, func(x, y int) color.NRGBA { return color.NRGBA{128, 0, 0, 255} }) // 32768/65535 ≈ 0.5 → 0 m
	detail := fxWritePNG(t, dir, "detail", 32, func(x, y int) color.NRGBA {
		return color.NRGBA{uint8(128 + 100*math.Sin(float64(x)/2)), 128, 230, 255}
	})
	fxSolid(t, "fxt_blue", color.RGBA{30, 40, 230, 255})

	fxMaterial(t, "fxt_splat", "", material.KindSplat, map[string]material.Value{
		"weights": tv(weights), "bounds_min": v2(mgl32.Vec2{-10, -10}), "bounds_max": v2(mgl32.Vec2{10, 10}),
		"layer1": tv(red), "layer1_scale": fv(1),
	})
	// The same splat anti-tiled and in relief: its patches are shifts of one
	// red layer and its parallax moves over one red, so it is still red, and
	// every path must take its extra reads alike.
	fxMaterial(t, "fxt_splat_anti", "", material.KindSplat, map[string]material.Value{
		"weights": tv(weights), "bounds_min": v2(mgl32.Vec2{-10, -10}), "bounds_max": v2(mgl32.Vec2{10, 10}),
		"layer1": tv(red), "layer1_scale": fv(1), "layer1_anti_tile": material.BoolValue(true), "layer1_relief": fv(0.05),
	})
	fxMaterial(t, "fxt_tri", "fxt_blue", material.KindTriplanar, map[string]material.Value{"scale": fv(0.5)})
	fxMaterial(t, "fxt_grade", "", material.KindColorGrade, map[string]material.Value{
		"match": fv(1), "target": cv(mgl32.Vec3{0.1, 0.8, 0.1})})
	fxMaterial(t, "fxt_ground", "", material.KindGroundBlend, map[string]material.Value{
		"ground": tv(ground), "height": tv(height), "bounds_min": v2(mgl32.Vec2{-10, -10}), "bounds_max": v2(mgl32.Vec2{10, 10}),
		"height_range": v2(mgl32.Vec2{-10, 10}), "band": fv(0.3), "breakup": fv(0), "strength": fv(1), "contact": fv(0),
	})
	fxMaterial(t, "fxt_streaks", "", material.KindStreaks, map[string]material.Value{
		"strength": fv(1), "scale": fv(0.05), "length": fv(0.5), "color": cv(mgl32.Vec3{0.05, 0.05, 0.05}), "overhang": fv(1)})
	fxMaterial(t, "fxt_macro", "", material.KindMacroVariation, map[string]material.Value{"scale": fv(0.5), "strength": fv(0.5)})
	fxMaterial(t, "fxt_detail", "", material.KindDetailNormal, map[string]material.Value{
		"map": tv(detail), "scale": fv(0.2), "strength": fv(1), "distance": fv(100)})

	mats := []string{"", "fxt_splat", "fxt_tri", "fxt_grade", "fxt_ground", "fxt_streaks", "fxt_macro", "fxt_detail", "fxt_splat_anti"}
	rock := colorTestRock(t, "fxrock", nil)
	// The row's width stays what the camera frames (8 rocks at 0.8).
	step := float32(5.6) / float32(len(mats)-1)
	x0 := -step * float32(len(mats)-1) / 2
	gfx.Cam.Position, gfx.Cam.Direction = mgl32.Vec3{0, 0.3, 7.6}, mgl32.Vec3{0, -0.04, -1}.Normalize()
	gfx.UpdateCamMatrix()

	makeLevel := func(gpu bool) *world.Level {
		lvl := &world.Level{}
		for i, m := range mats {
			s := world.NewScenery("rock", rock, mgl32.Vec3{x0 + step*float32(i), 0, 0})
			s.NoNanite = !gpu
			s.Materials = map[string]string{"stone": "concrete", "moss": "concrete"}
			s.Material = m
			if err := s.Load(); err != nil {
				t.Fatal(err)
			}
			s.Scale = mgl32.Vec3{0.36, 0.36, 0.36}
			lvl.Scenery = append(lvl.Scenery, s)
		}
		lvl.Lights.Global = world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.55,
			Wrap: world.DefaultLightWrap, Hemisphere: world.DefaultAmbientHemisphere}
		sun := world.DirectionalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.5, Yaw: 10, Pitch: -30, Dynamic: true}
		sun.SyncDirection()
		lvl.Lights.Directional = []world.DirectionalLight{sun}
		if err := world.RebuildIndexes(lvl); err != nil {
			t.Fatal(err)
		}
		world.UploadLights(lvl.Lights)
		return lvl
	}
	t.Cleanup(func() { world.UploadLights(world.LightSet{}) })

	prevStream, prevVariants := NaniteStream, NaniteMatVariants
	t.Cleanup(func() { NaniteStream, NaniteMatVariants = prevStream, prevVariants })
	type path struct {
		name             string
		gpu, vis, stream bool
		// generic shades every bin with the one uber-program, not its
		// variant (nanite_vis_variants.go).
		generic bool
	}
	at := func(i int, dy float32) mgl32.Vec3 { return mgl32.Vec3{x0 + step*float32(i), dy, 0.3} }
	results := map[string][][2][3]float64{}
	for _, p := range []path{
		{"vis, page pool", true, true, true, false},
		{"vis, generic program", true, true, true, true},
		{"vis, flat pool", true, true, false, false},
		{"forward", true, false, true, false},
		{"classic", false, false, true, false},
	} {
		func() {
			NaniteStream, NaniteVisBuffer, NaniteMatVariants = p.stream, p.vis, !p.generic
			lvl := makeLevel(p.gpu)
			defer func() {
				world.DeleteScenery(lvl)
				naniteScenery.lvl = nil
			}()
			w.frame(lvl)
			px := w.frame(lvl)
			if bins := DebugStats.NaniteVisBins; (bins > 0) != p.vis {
				t.Errorf("%s: %d material bins shaded", p.name, bins)
			}
			dumpPNG("matfx_"+strings.NewReplacer(",", "", " ", "_").Replace(p.name), px, int(w.size))
			var m [][2][3]float64
			for i := range mats {
				m = append(m, [2][3]float64{meanAround(px, int(w.size), at(i, 0.25), 2), meanAround(px, int(w.size), at(i, -0.2), 2)})
			}
			results[p.name] = m
			t.Logf("%s: %v", p.name, m)
		}()
	}
	f := results["forward"]
	lum := func(c [3]float64) float64 { return 0.3*c[0] + 0.59*c[1] + 0.11*c[2] }
	plain := f[0][0]
	if c := f[1][0]; !(c[0] > 1.6*c[1] && c[0] > 1.6*c[2]) {
		t.Errorf("splat: the rock is not its red layer: %.0f", c)
	}
	if c := f[8][0]; !(c[0] > 1.6*c[1] && c[0] > 1.6*c[2]) {
		t.Errorf("anti-tiled splat: the rock is not its red layer: %.0f", c)
	}
	if c := f[2][0]; !(c[2] > 1.6*c[0] && c[2] > 1.4*c[1]) {
		t.Errorf("triplanar: the rock is not its blue texture: %.0f", c)
	}
	if c := f[3][0]; !(c[1] > 1.3*c[0] && c[1] > 1.3*c[2]) {
		t.Errorf("color_grade: concrete matched toward green is not green: %.0f (plain %.0f)", c, plain)
	}
	if lo, hi := f[4][1], f[4][0]; !(lo[1] > 1.3*lo[0] && hi[1] < 1.2*hi[0]) {
		t.Errorf("ground_blend: the foot (%.0f) should be the ground's green and the top (%.0f) not", lo, hi)
	}
	if !(lum(f[5][1]) < 0.9*lum(f[0][1])) {
		t.Errorf("streaks: the rock's underside (%.0f) is no darker than plain's (%.0f)", f[5][1], f[0][1])
	}
	for i := 6; i <= 7; i++ {
		if math.Abs(lum(f[i][0])-lum(plain)) < 0.5 && math.Abs(lum(f[i][1])-lum(f[0][1])) < 0.5 {
			t.Errorf("%s: no change from plain", mats[i])
		}
	}
	// The variants shade what the generic program does: they only drop code
	// a surface's uniforms would have skipped.
	g, v := results["vis, generic program"], results["vis, page pool"]
	for i := range mats {
		for k := 0; k < 2; k++ {
			for c := 0; c < 3; c++ {
				if d := math.Abs(g[i][k][c] - v[i][k][c]); d > 1 {
					t.Errorf("variant: %q differs from the generic program by %.1f (%.1f against %.1f)", mats[i], d, v[i][k], g[i][k])
					c, k = 3, 2
				}
			}
		}
	}
	// The paths agree: the material pass reconstructs the forward pass's
	// derivatives, and the classic path is the same shader on the same mesh.
	for _, name := range []string{"vis, page pool", "vis, flat pool", "classic"} {
		r := results[name]
		for i := range mats {
			for k := 0; k < 2; k++ {
				for c := 0; c < 3; c++ {
					if d := math.Abs(r[i][k][c] - f[i][k][c]); d > 14 {
						t.Errorf("%s: %q differs from forward by %.0f (%.0f against %.0f)", name, mats[i], d, r[i][k], f[i][k])
						c, k = 3, 2
					}
				}
			}
		}
	}
}

// TestMaterialFxSamplersFitTheMaterialPass checks the visibility buffer's
// material program still builds with the effects' samplers, and inside the
// 32 a fragment stage may have.
func TestMaterialFxSamplersFitTheMaterialPass(t *testing.T) {
	newVisWorld(t)
	var v naniteVisPass
	if err := v.build(); err != nil {
		t.Fatalf("the material pass does not build: %v", err)
	}
	var count int32
	gl.GetProgramiv(v.material.prog, gl.ACTIVE_UNIFORMS, &count)
	samplers, fx := 0, 0
	for i := int32(0); i < count; i++ {
		buf := make([]uint8, 256)
		var l, size int32
		var typ uint32
		gl.GetActiveUniform(v.material.prog, uint32(i), 256, &l, &size, &typ, &buf[0])
		switch typ {
		case gl.SAMPLER_2D, gl.SAMPLER_2D_ARRAY, gl.SAMPLER_2D_ARRAY_SHADOW, gl.SAMPLER_CUBE_SHADOW,
			gl.UNSIGNED_INT_SAMPLER_2D, gl.SAMPLER_3D, gl.SAMPLER_CUBE, gl.UNSIGNED_INT_SAMPLER_2D_ARRAY:
			samplers += int(size)
			if strings.HasPrefix(string(buf[:l]), "fx") {
				fx++
			}
		}
	}
	var max int32
	gl.GetIntegerv(gl.MAX_TEXTURE_IMAGE_UNITS, &max)
	t.Logf("material pass: %d samplers (%d the effects'), the driver allows %d", samplers, fx, max)
	// Nine: the seven of docs/canyon/PHASE_5.md, the eight-layer splat's
	// second weights map (docs/regions/PHASE_3.md) — the layers 5–8 share
	// the two atlases, so that is the only one it adds — and the ground
	// trails' map (docs/trails/SPEC.md).
	if fx != 9 {
		t.Errorf("%d of the effects' samplers are active in the material pass, want 9", fx)
	}
	if samplers > 32 || int32(samplers) > max {
		t.Errorf("%d samplers: over the fragment stage's %d", samplers, max)
	}
}
