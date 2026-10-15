package render

// material_fx_palette_gl_test.go — a region terrain's ground PALETTE
// (procgen/stages/region_material_palette.go, docs/regions/PRESETS_1.md):
// more than eight ground layers in one world, packed into one shared atlas
// 8 across (gfx material_fx_textures.go), each tile wearing a splat of at
// most eight of them through `atlas1..32`.
//
// Twenty patterned layers (so the palette atlas is 8×3: rows past the
// eight-layer atlas's two), 64² each, so every atlas packs them at the same
// size and gutter. A tile wearing palette layers 17–20 and 10–13 (rows 1
// and 2 of the palette atlas) is drawn against the REFERENCE: the same
// layers, settings and weights as an ordinary splat on its own atlas (the
// 4×2, or the 2×2 for four layers) — what the tile would draw if it were a
// world of eight. The frames must match within two levels at every pixel,
// on the visibility buffer (variants and generic program) and the forward
// path, for an eight-layer and a four-layer tile. (Not to the bit: the two
// atlases are different sizes, so the texel a coordinate lands on is
// worked out in different floating-point sums, and their NPOT mip chains
// round differently past level 2.) No layer is anti-tiled here: the
// anti-tiling's patch noise is seeded by the layer's ATLAS TILE
// (surface_fx.glsl fxSplatTap), which a palette numbers differently from an
// eight-layer atlas — a different, equally valid arrangement of patches
// (every tile of a palette world numbers a layer alike, so neighbours agree).
// The sampler budget is untouched: the
// palette is the same two atlas units (TestMaterialFxSamplersFitTheMaterial
// Pass).
//
//	FOUNDRY_GL_TEST=1 go test -run TestMaterialFxPalette ./internal/render/

import (
	"fmt"
	"image/color"
	"math"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/material"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func TestMaterialFxPaletteTileMatchesItsOwnAtlas(t *testing.T) {
	w := newVisWorld(t)
	dir := t.TempDir()
	tv, fv, bv, v2 := material.TextureValue, material.FloatValue, material.BoolValue, material.Vec2Value

	const nl = 20
	var names [material.SplatAtlasMax]string
	for l := 0; l < nl; l++ {
		l := l
		hue := float64(l) / nl * 2 * math.Pi
		c := [3]float64{140 + 100*math.Sin(hue), 140 + 100*math.Sin(hue+2.1), 140 + 100*math.Sin(hue+4.2)}
		names[l] = fxWritePNG(t, dir, fmt.Sprintf("pal_l%d", l), 256, func(x, y int) color.NRGBA {
			s := 0.75 + 0.25*math.Sin(float64(x+3*y)*0.025*float64(l%7+1))
			return color.NRGBA{uint8(c[0] * s), uint8(c[1] * s), uint8(c[2] * s), 255}
		})
		fxWritePNG(t, dir, fmt.Sprintf("pal_l%d_height", l), 256, func(x, y int) color.NRGBA {
			v := uint8(128 + 120*math.Sin(float64(x)*0.025*float64(l%5+2)+float64(y)*0.03*float64(9-l%8)))
			return color.NRGBA{v, v, v, 255}
		})
		fxWritePNG(t, dir, fmt.Sprintf("pal_l%d_normal", l), 256, func(x, y int) color.NRGBA {
			return color.NRGBA{uint8(128 + 50*math.Sin(float64(y)*0.075)), uint8(128 + 40*math.Cos(float64(x)*0.06)), 230, 255}
		})
	}
	// The palette atlas: 20 distinct layers, 8 across, 3 down.
	if a := gfx.SplatArraysForAtlas(names); a == nil || a.Cols != 8 || a.Rows != 3 || a.Size != 256 {
		t.Fatalf("palette atlas %+v", a)
	} else if a.Layer[19] != 19 || a.Layer[20] != -1 {
		t.Fatalf("palette tiles %v", a.Layer)
	}

	// The tile's layers: palette entries 17–20 and 10–13 (0-based 16..19,
	// 9..12), each with settings of its own.
	pal := []int{16, 17, 18, 19, 9, 10, 11, 12}
	layerParams := func(m map[string]material.Value, slot, l int) {
		m[material.SplatLayerParam(slot, "")] = tv(names[l])
		m[material.SplatLayerParam(slot, "scale")] = fv(2.4 + 0.2*float32(l%5))
		switch l {
		case 18, 11:
			m[material.SplatLayerParam(slot, "triplanar")] = bv(true)
		case 10:
			m[material.SplatLayerParam(slot, "relief")] = fv(0.03)
		}
	}
	const n = 32
	wts := make([][8]uint8, n*n)
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			var f [8]float64
			sum := 0.0
			for k := range f {
				f[k] = 0.5 + 0.5*math.Sin(float64(i)*0.45+float64(j)*0.3*float64(k%3+1)+float64(k)*0.9)
				f[k] *= f[k]
				sum += f[k]
			}
			for k := range f {
				wts[j*n+i][k] = uint8(math.Round(255 * f[k] / sum))
			}
		}
	}
	mapOf := func(name string, ch [4]int) string {
		return fxWritePNG(t, dir, name, n, func(x, y int) color.NRGBA {
			var c [4]uint8
			for k, l := range ch {
				if l >= 0 {
					c[k] = wts[y*n+x][l]
				}
			}
			return color.NRGBA{c[0], c[1], c[2], c[3]}
		})
	}
	w1, w2 := mapOf("pal_w1", [4]int{0, 1, 2, 3}), mapOf("pal_w2", [4]int{4, 5, 6, 7})
	mk := func(name string, slots int, palette bool) string {
		m := map[string]material.Value{"weights": tv(w1), "bounds_min": v2(mgl32.Vec2{-8, -8}), "bounds_max": v2(mgl32.Vec2{8, 8}),
			"blend": fv(0.2)}
		if slots > 4 {
			m["weights2"] = tv(w2)
		}
		for s := 0; s < slots; s++ {
			layerParams(m, s, pal[s])
		}
		if palette {
			for i := 0; i < nl; i++ {
				m[material.SplatAtlasParam(i)] = tv(names[i])
			}
		}
		fxMaterial(t, name, "", material.KindSplat, m)
		s, ok := material.ResolveSurface(name)
		if !ok || !s.Fx.HasSplat || s.Fx.SplatEight != (slots > 4) || (palette && s.Fx.SplatAtlas != names) {
			t.Fatalf("%s: %+v", name, s.Fx)
		}
		return name
	}
	cases := []struct{ name, pal, own string }{
		{"eight", mk("pal8", 8, true), mk("own8", 8, false)},
		{"four", mk("pal4", 4, true), mk("own4", 4, false)},
	}

	meshes := [3]string{perTileTileMesh(t, "pal_w", -4), perTileTileMesh(t, "pal_b", 0), perTileTileMesh(t, "pal_e", 4)}
	gfx.Cam.Position, gfx.Cam.Direction = mgl32.Vec3{0, 7, 6.5}, mgl32.Vec3{0, -7, -6.5}.Normalize()
	gfx.UpdateCamMatrix()
	makeLevel := func(mat string) *world.Level {
		lvl := &world.Level{}
		for i, mesh := range meshes {
			s := world.NewScenery("tile", mesh, mgl32.Vec3{-4 + 4*float32(i), 0, 0})
			s.Material = mat
			if err := s.Load(); err != nil {
				t.Fatal(err)
			}
			lvl.Scenery = append(lvl.Scenery, s)
		}
		lvl.Lights.Global = world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.55,
			Wrap: world.DefaultLightWrap, Hemisphere: world.DefaultAmbientHemisphere}
		sun := world.DirectionalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.6, Yaw: 30, Pitch: -40, Dynamic: true}
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
	t.Cleanup(func() { NaniteStream, NaniteMatVariants, NaniteVisBuffer = prevStream, prevVariants, true })
	draw := func(mat string) []uint8 {
		lvl := makeLevel(mat)
		defer func() {
			world.DeleteScenery(lvl)
			naniteScenery.lvl = nil
		}()
		w.frame(lvl)
		return w.frame(lvl)
	}
	for _, c := range cases {
		for _, p := range []struct {
			name         string
			vis, generic bool
		}{
			{"vis", true, false},
			{"vis generic", true, true},
			{"forward", false, false},
		} {
			NaniteStream, NaniteVisBuffer, NaniteMatVariants = true, p.vis, !p.generic
			ref := draw(c.own)
			got := draw(c.pal)
			dumpPNG("palette_"+c.name+"_"+p.name+"_own", ref, int(w.size))
			dumpPNG("palette_"+c.name+"_"+p.name+"_palette", got, int(w.size))
			maxD, over, lit := 0, 0, 0
			var lo, hi [3]int
			lo = [3]int{255, 255, 255}
			bg := len(ref) - 4
			for k := 0; k < len(ref); k += 4 {
				past := false
				for ch := 0; ch < 3; ch++ {
					d := absInt(int(ref[k+ch]) - int(got[k+ch]))
					maxD = max(maxD, d)
					past = past || d > 1
					lo[ch], hi[ch] = min(lo[ch], int(ref[k+ch])), max(hi[ch], int(ref[k+ch]))
				}
				if past {
					over++
				}
				if absInt(int(ref[k])-int(ref[bg]))+absInt(int(ref[k+1])-int(ref[bg+1]))+absInt(int(ref[k+2])-int(ref[bg+2])) > 30 {
					lit++
				}
			}
			t.Logf("%s %s: max difference %d, %d of %d pixels past 1 level; %d terrain; channel ranges %v..%v",
				c.name, p.name, maxD, over, len(ref)/4, lit, lo, hi)
			if lit < len(ref)/4/8 || hi[0]-lo[0] < 30 {
				t.Fatalf("%s %s: the frame shows too little terrain to compare (%d terrain pixels, red %d..%d)", c.name, p.name, lit, lo[0], hi[0])
			}
			if maxD > 2 {
				t.Errorf("%s %s: the palette tile differs from the same layers on their own atlas by %d levels (%d pixels past 1)",
					c.name, p.name, maxD, over)
			}
		}
	}
}
