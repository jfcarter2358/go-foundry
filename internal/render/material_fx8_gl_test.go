package render

// material_fx8_gl_test.go — the eight-layer splat (docs/regions/PHASE_3.md:
// a region_material's two biomes, four ground layers each, on one tile)
// drawn for real on every path a cooked placement can take, and the
// visibility buffer's material program built with the eight-layer branch.
//
// The rocks:
//
//   - a four-layer splat whose weights are all its red first layer — the
//     classic splat, beside the others so a regression in the shared code
//     shows;
//   - an eight-layer splat with six distinct layers (so the layers are in
//     the 4×2 atlas) whose first weights map is empty and whose second is
//     all layer 6, a blue texture: it must be blue, which it can only be
//     if layers 5–8's weights, uniforms and atlas tiles are all right;
//   - a TWO-BIOME tile: the same eight layers with half the weight on
//     layer 1 (red, "biome A's sand") and half on layer 6 (blue, "biome
//     B's grass"), equal height maps — the blend across the seam band —
//     which must be both red and blue, and the same on every path.
//
//	FOUNDRY_GL_TEST=1 go test -run 'TestMaterialFxEight|TestMaterialPassBuildsTheEightLayer' ./internal/render/

import (
	"image/color"
	"math"
	"strings"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/material"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func TestMaterialFxEightLayerSplatOnEveryPath(t *testing.T) {
	w := newVisWorld(t)
	dir := t.TempDir()
	tv, fv, v2 := material.TextureValue, material.FloatValue, material.Vec2Value
	solid := func(name string, c color.NRGBA) string {
		p := fxWritePNG(t, dir, name, 64, func(x, y int) color.NRGBA { return c })
		fxWritePNG(t, dir, name+"_height", 64, func(x, y int) color.NRGBA { return color.NRGBA{128, 128, 128, 255} })
		fxWritePNG(t, dir, name+"_normal", 64, func(x, y int) color.NRGBA { return color.NRGBA{128, 128, 255, 255} })
		return p
	}
	red := solid("red8", color.NRGBA{220, 30, 30, 255})
	grey := solid("grey8", color.NRGBA{120, 120, 120, 255})
	yellow := solid("yellow8", color.NRGBA{210, 200, 40, 255})
	green := solid("green8", color.NRGBA{40, 200, 40, 255})
	white := solid("white8", color.NRGBA{235, 235, 235, 255})
	blue := solid("blue8", color.NRGBA{30, 40, 230, 255})
	w1 := func(r, g, b, a uint8) string {
		return fxWritePNG(t, dir, "w1_"+string(rune('a'+int(r)%26)), 8, func(x, y int) color.NRGBA { return color.NRGBA{r, g, b, a} })
	}
	only1 := w1(255, 0, 0, 0)
	none1 := fxWritePNG(t, dir, "w1_none", 8, func(x, y int) color.NRGBA { return color.NRGBA{0, 0, 0, 0} })
	half1 := fxWritePNG(t, dir, "w1_half", 8, func(x, y int) color.NRGBA { return color.NRGBA{128, 0, 0, 0} })
	only6 := fxWritePNG(t, dir, "w2_only6", 8, func(x, y int) color.NRGBA { return color.NRGBA{0, 255, 0, 0} })
	half6 := fxWritePNG(t, dir, "w2_half6", 8, func(x, y int) color.NRGBA { return color.NRGBA{0, 127, 0, 0} })
	rect := map[string]material.Value{"bounds_min": v2(mgl32.Vec2{-10, -10}), "bounds_max": v2(mgl32.Vec2{10, 10})}
	with := func(m map[string]material.Value) map[string]material.Value {
		for k, v := range rect {
			m[k] = v
		}
		return m
	}
	eight := func(weights, weights2 string) map[string]material.Value {
		m := with(map[string]material.Value{"weights": tv(weights), "weights2": tv(weights2)})
		for i, l := range []string{red, grey, yellow, green, white, blue} {
			m[material.SplatLayerParam(i, "")] = tv(l)
			m[material.SplatLayerParam(i, "scale")] = fv(1)
		}
		return m
	}
	fxMaterial(t, "fxt8_four", "", material.KindSplat, with(map[string]material.Value{"weights": tv(only1), "layer1": tv(red), "layer1_scale": fv(1)}))
	fxMaterial(t, "fxt8_blue", "", material.KindSplat, eight(none1, only6))
	fxMaterial(t, "fxt8_seam", "", material.KindSplat, eight(half1, half6))
	for _, m := range []string{"fxt8_blue", "fxt8_seam"} {
		if s, ok := material.ResolveSurface(m); !ok || !s.Fx.HasSplat || !s.Fx.SplatEight {
			t.Fatalf("%s does not resolve to an eight-layer splat: %+v", m, s.Fx)
		}
	}

	mats := []string{"fxt8_four", "fxt8_blue", "fxt8_seam"}
	rock := colorTestRock(t, "fx8rock", nil)
	step := float32(1.6)
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
			s.Scale = mgl32.Vec3{0.5, 0.5, 0.5}
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
	at := func(i int) mgl32.Vec3 { return mgl32.Vec3{x0 + step*float32(i), 0.3, 0.4} }
	results := map[string][][3]float64{}
	for _, p := range []struct {
		name                      string
		gpu, vis, stream, generic bool
	}{
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
			dumpPNG("matfx8_"+strings.NewReplacer(",", "", " ", "_").Replace(p.name), px, int(w.size))
			var m [][3]float64
			for i := range mats {
				m = append(m, meanAround(px, int(w.size), at(i), 2))
			}
			results[p.name] = m
			t.Logf("%s: %.0f", p.name, m)
		}()
	}
	f := results["forward"]
	if c := f[0]; !(c[0] > 1.6*c[1] && c[0] > 1.6*c[2]) {
		t.Errorf("four-layer splat: not its red layer: %.0f", c)
	}
	if c := f[1]; !(c[2] > 1.6*c[0] && c[2] > 1.4*c[1]) {
		t.Errorf("eight-layer splat, all layer 6: not blue: %.0f", c)
	}
	if c := f[2]; !(c[0] > 0.45*c[2] && c[2] > 0.45*c[0] && c[0] > 1.3*c[1] && c[2] > 1.3*c[1]) {
		t.Errorf("two-biome tile (half layer 1 red, half layer 6 blue): not both: %.0f", c)
	}
	for _, name := range []string{"vis, page pool", "vis, generic program", "vis, flat pool", "classic"} {
		r := results[name]
		for i := range mats {
			for c := 0; c < 3; c++ {
				if d := math.Abs(r[i][c] - f[i][c]); d > 14 {
					t.Errorf("%s: %q differs from forward by %.0f (%.0f against %.0f)", name, mats[i], d, r[i], f[i])
					break
				}
			}
		}
	}
}

// TestMaterialPassBuildsTheEightLayerVariant: the visibility buffer's
// material program — generic, and the variant a bin of eight-layer splat
// is shaded with — builds with the eight-layer branch in it, and a
// four-layer splat's variant folds that branch out.
func TestMaterialPassBuildsTheEightLayerVariant(t *testing.T) {
	newVisWorld(t)
	var v naniteVisPass
	if err := v.build(); err != nil {
		t.Fatalf("the material pass does not build: %v", err)
	}
	// (surface_fx.glsl is still an #include here; the compile resolves it.)
	if !strings.Contains(v.matSrc, "(matFx & FX_SPLAT8) != 0") {
		t.Fatal("the material pass's main does not test FX_SPLAT8")
	}
	prev := NaniteMatVariants
	NaniteMatVariants = true
	t.Cleanup(func() { NaniteMatVariants = prev })
	p8 := v.materialProgram(visKeySplat | visKeySplat8)
	if p8 == nil || p8.failed || p8.prog == v.material.prog {
		t.Fatal("the eight-layer splat's variant did not build")
	}
	p4 := v.materialProgram(visKeySplat)
	if p4 == nil || p4.failed || p4 == p8 {
		t.Fatal("the four-layer splat's variant did not build")
	}
	four, err := specializeMaterialSource(v.matSrc, visKeySplat, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(four, "(false && ((matFx & FX_SPLAT8) != 0))") {
		t.Error("a four-layer bin's variant does not fold the eight-layer branch out")
	}
	all, err := specializeMaterialSource(v.matSrc, visKeySplat|visKeySplat8, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(all, "(true && ((matFx & FX_SPLAT8) != 0))") {
		t.Error("an eight-layer bin's variant does not keep the eight-layer branch")
	}
	// The key: a surface's eight-layer splat sets the bit, a four-layer one
	// does not.
	st := &gfx.SurfaceState{}
	st.Fx.HasSplat = true
	if visShadeKey(st, false)&visKeySplat8 != 0 {
		t.Error("a four-layer splat keys the eight-layer variant")
	}
	st.Fx.SplatEight = true
	if visShadeKey(st, false)&visKeySplat8 == 0 {
		t.Error("an eight-layer splat does not key the eight-layer variant")
	}
}
