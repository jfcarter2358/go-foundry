package render

// material_fx_pertile_gl_test.go — a region terrain's PER-TILE materials
// (procgen/stages/region_material_tiles.go, docs/regions/PHASE_6.md
// "Per-tile materials") drawn for real: a four-layer tile beside an
// eight-layer band tile must draw what both would on the eight-layer
// union, so the boundary between them has no seam.
//
// Three flat, gently rolling tiles in a row — west (x −6..−2), band
// (−2..2), east (2..6) — under one 32² weights map over ±8 m whose west
// texels weigh only "biome A's" layers and east texels only "biome B's",
// with the band between. Eight patterned layers with distinct height maps
// (so the height blend decides between them), repeats, a triplanar layer
// in each biome, a tint, an anti-tiled layer and one in relief. The frame
// with all three tiles on the eight-layer union is the reference; then the
// west and east tiles wear four-layer materials built the way the stage
// builds them — their layers in the union's order with the union's
// settings, their weights the union's bytes for those layers, the union's
// atlas through `atlas1..8` — and the frame must match it within two
// levels at every pixel, on the visibility buffer (variants and generic
// program) and the forward path. Two weight layouts: the sets aligned with
// the union's maps (layers 1–4 and 5–8, whose weights ARE weights and
// weights2), and mixed sets ({1, 6} and {3, 8}), whose weights are
// remapped images.
//
//	FOUNDRY_GL_TEST=1 go test -run TestMaterialFxPerTile ./internal/render/

import (
	"fmt"
	"image/color"
	"math"
	"path/filepath"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/material"
	"foundry/internal/model"
	"foundry/internal/nanitecook"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// perTileTileMesh cooks a 4 m flat tile, 16² quads, centred on (cx, 0):
// heights and normals from one world function, so neighbours meet.
func perTileTileMesh(t *testing.T, name string, cx float32) string {
	t.Helper()
	h := func(x, z float32) float32 { return 0.25 * float32(math.Sin(float64(x)*0.9)*math.Cos(float64(z)*0.7)) }
	const n = 16
	m := &model.ImportMesh{Slots: []model.ImportSlot{{Name: "ground", BaseColor: [4]float32{1, 1, 1, 1}}}}
	for j := 0; j <= n; j++ {
		for i := 0; i <= n; i++ {
			x, z := cx-2+4*float32(i)/n, -2+4*float32(j)/n
			const e = 0.01
			nx := -(h(x+e, z) - h(x-e, z)) / (2 * e)
			nz := -(h(x, z+e) - h(x, z-e)) / (2 * e)
			m.Positions = append(m.Positions, mgl32.Vec3{x - cx, h(x, z), z})
			m.Normals = append(m.Normals, mgl32.Vec3{nx, 1, nz}.Normalize())
			m.UVs = append(m.UVs, mgl32.Vec2{x / 4, z / 4})
		}
	}
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			a := uint32(j*(n+1) + i)
			b, c, d := a+1, a+uint32(n+1), a+uint32(n+2)
			m.Indices = append(m.Indices, a, c, b, b, c, d)
			m.TriSlot = append(m.TriSlot, 0, 0)
		}
	}
	out := filepath.Join(t.TempDir(), name+".fnm")
	if _, err := nanitecook.CookMeshFile(m, out, true, nanitecook.Options{}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMaterialFxPerTileMatchesTheEightLayerUnion(t *testing.T) {
	w := newVisWorld(t)
	dir := t.TempDir()
	tv, fv, bv, cv, v2 := material.TextureValue, material.FloatValue, material.BoolValue, material.ColorValue, material.Vec2Value

	// Eight patterned layers: a colour each, stripes in the albedo, a height
	// map of its own frequency (the blend's tie-breaker) and a tilted normal.
	var names [8]string
	base := [8][3]float64{{200, 60, 40}, {150, 110, 60}, {120, 120, 120}, {210, 190, 90},
		{70, 170, 60}, {160, 150, 70}, {90, 80, 70}, {60, 90, 200}}
	for l := 0; l < 8; l++ {
		l := l
		names[l] = fxWritePNG(t, dir, fmt.Sprintf("pt_l%d", l), 64, func(x, y int) color.NRGBA {
			s := 0.75 + 0.25*math.Sin(float64(x+3*y)*0.2*float64(l+1))
			c := base[l]
			return color.NRGBA{uint8(c[0] * s), uint8(c[1] * s), uint8(c[2] * s), 255}
		})
		fxWritePNG(t, dir, fmt.Sprintf("pt_l%d_height", l), 64, func(x, y int) color.NRGBA {
			v := uint8(128 + 120*math.Sin(float64(x)*0.1*float64(l+2)+float64(y)*0.13*float64(8-l)))
			return color.NRGBA{v, v, v, 255}
		})
		fxWritePNG(t, dir, fmt.Sprintf("pt_l%d_normal", l), 64, func(x, y int) color.NRGBA {
			return color.NRGBA{uint8(128 + 50*math.Sin(float64(y)*0.3)), uint8(128 + 40*math.Cos(float64(x)*0.25)), 230, 255}
		})
	}
	// Each layer's settings, the same in the union and in a four-layer
	// material (as the stage writes them).
	layerParams := func(m map[string]material.Value, slot, l int) {
		m[material.SplatLayerParam(slot, "")] = tv(names[l])
		m[material.SplatLayerParam(slot, "scale")] = fv(0.6 + 0.15*float32(l))
		switch l {
		case 2, 6:
			m[material.SplatLayerParam(slot, "triplanar")] = bv(true)
		case 1:
			m[material.SplatLayerParam(slot, "anti_tile")] = bv(true)
		case 4:
			m[material.SplatLayerParam(slot, "relief")] = fv(0.04)
		case 7:
			m[material.SplatLayerParam(slot, "tint")] = cv(mgl32.Vec3{0.9, 1.1, 1})
			m[material.SplatLayerParam(slot, "height")] = fv(0.6)
		}
	}
	rect := func(m map[string]material.Value) map[string]material.Value {
		m["bounds_min"], m["bounds_max"] = v2(mgl32.Vec2{-8, -8}), v2(mgl32.Vec2{8, 8})
		m["blend"] = fv(0.2)
		return m
	}

	const n = 32
	// biomeA is texel column i's share of biome A: all of it to x −0.75
	// (i 14), none from x 0.75 (i 17), the band between.
	biomeA := func(i int) float64 { return math.Max(0, math.Min(1, float64(17-i)/3)) }
	type layout struct {
		name   string
		a, b   []int // each biome's layers
		setA   []int // the west tile's set (the east's is b)
		setB   []int
		mixedA bool
	}
	for _, lay := range []layout{
		{"aligned", []int{0, 1, 2, 3}, []int{4, 5, 6, 7}, []int{0, 1, 2, 3}, []int{4, 5, 6, 7}, false},
		{"mixed", []int{0, 5}, []int{2, 7}, []int{0, 5}, []int{2, 7}, true},
	} {
		// Not t.Run: a subtest is another goroutine, and the GL context is
		// the test's thread's.
		func() {
			// The union's eight weights per texel, as bytes.
			wts := make([][8]uint8, n*n)
			for j := 0; j < n; j++ {
				for i := 0; i < n; i++ {
					var f [8]float64
					sum := 0.0
					for k, l := range lay.a {
						f[l] = biomeA(i) * (0.55 + 0.45*math.Sin(float64(i)*0.7+float64(j)*0.4+float64(k)*1.9))
					}
					for k, l := range lay.b {
						f[l] = (1 - biomeA(i)) * (0.55 + 0.45*math.Cos(float64(i)*0.5-float64(j)*0.6+float64(k)*2.3))
					}
					for _, v := range f {
						sum += v
					}
					for l := range f {
						wts[j*n+i][l] = uint8(math.Round(255 * f[l] / sum))
					}
				}
			}
			mapOf := func(name string, ch [4]int) string {
				return fxWritePNG(t, dir, lay.name+"_"+name, n, func(x, y int) color.NRGBA {
					var c [4]uint8
					for k, l := range ch {
						if l >= 0 {
							c[k] = wts[y*n+x][l]
						}
					}
					return color.NRGBA{c[0], c[1], c[2], c[3]}
				})
			}
			w1, w2 := mapOf("w1", [4]int{0, 1, 2, 3}), mapOf("w2", [4]int{4, 5, 6, 7})
			union := "pt_" + lay.name + "_union"
			um := rect(map[string]material.Value{"weights": tv(w1), "weights2": tv(w2)})
			for l := 0; l < 8; l++ {
				layerParams(um, l, l)
			}
			fxMaterial(t, union, "", material.KindSplat, um)
			four := func(name string, set []int) {
				ch := [4]int{-1, -1, -1, -1}
				copy(ch[:], set)
				var weights string
				switch {
				case !lay.mixedA && set[0] == 0:
					weights = w1
				case !lay.mixedA:
					weights = w2
				default:
					weights = mapOf(name, ch)
				}
				m := rect(map[string]material.Value{"weights": tv(weights)})
				for slot, l := range set {
					layerParams(m, slot, l)
				}
				for i := range names {
					m[material.SplatAtlasParam(i)] = tv(names[i])
				}
				fxMaterial(t, name, "", material.KindSplat, m)
				s, ok := material.ResolveSurface(name)
				if !ok || !s.Fx.HasSplat || s.Fx.SplatEight || s.Fx.SplatAtlas[7] != names[7] {
					t.Fatalf("%s is not a four-layer splat on the union's atlas: %+v", name, s.Fx)
				}
			}
			west, east := "pt_"+lay.name+"_west", "pt_"+lay.name+"_east"
			four(west, lay.setA)
			four(east, lay.setB)
			// The west tile's window (the bilinear texel round its edge
			// included) weighs none of biome B's layers, the east's none
			// of A's: the premise of a four-layer tile.
			for j := 0; j < n; j++ {
				for _, l := range lay.b {
					for i := 0; i <= 13; i++ {
						if wts[j*n+i][l] > 1 {
							t.Fatalf("west window weighs layer %d at texel %d", l+1, i)
						}
					}
				}
				for _, l := range lay.a {
					for i := 18; i < n; i++ {
						if wts[j*n+i][l] > 1 {
							t.Fatalf("east window weighs layer %d at texel %d", l+1, i)
						}
					}
				}
			}

			meshes := [3]string{perTileTileMesh(t, "pt_w", -4), perTileTileMesh(t, "pt_b", 0), perTileTileMesh(t, "pt_e", 4)}
			gfx.Cam.Position, gfx.Cam.Direction = mgl32.Vec3{0, 7, 6.5}, mgl32.Vec3{0, -7, -6.5}.Normalize()
			gfx.UpdateCamMatrix()
			makeLevel := func(gpu bool, mats [3]string) *world.Level {
				lvl := &world.Level{}
				for i, mesh := range meshes {
					s := world.NewScenery("tile", mesh, mgl32.Vec3{-4 + 4*float32(i), 0, 0})
					s.NoNanite = !gpu
					s.Material = mats[i]
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
			draw := func(gpu bool, mats [3]string) ([]uint8, int) {
				lvl := makeLevel(gpu, mats)
				defer func() {
					world.DeleteScenery(lvl)
					naniteScenery.lvl = nil
				}()
				w.frame(lvl)
				px := w.frame(lvl)
				return px, DebugStats.NaniteVisBins
			}
			for _, p := range []struct {
				name                      string
				gpu, vis, stream, generic bool
			}{
				{"vis", true, true, true, false},
				{"vis generic", true, true, true, true},
				{"forward", true, false, true, false},
			} {
				NaniteStream, NaniteVisBuffer, NaniteMatVariants = p.stream, p.vis, !p.generic
				ref, refBins := draw(p.gpu, [3]string{union, union, union})
				got, gotBins := draw(p.gpu, [3]string{west, union, east})
				dumpPNG("pertile_"+lay.name+"_"+p.name+"_union", ref, int(w.size))
				dumpPNG("pertile_"+lay.name+"_"+p.name+"_tiles", got, int(w.size))
				if p.vis && gotBins <= refBins {
					t.Errorf("%s %s: %d material bins with per-tile materials, %d without: the tiles did not get their own", lay.name, p.name, gotBins, refBins)
				}
				// The picture is the union's.
				maxD, over, lit := 0, 0, 0
				var lo, hi [3]int
				lo = [3]int{255, 255, 255}
				for k := 0; k < len(ref); k += 4 {
					for c := 0; c < 3; c++ {
						d := int(ref[k+c]) - int(got[k+c])
						if d < 0 {
							d = -d
						}
						maxD = max(maxD, d)
						lo[c], hi[c] = min(lo[c], int(ref[k+c])), max(hi[c], int(ref[k+c]))
					}
					// Terrain, not the sky (the last pixel read back is the
					// frame's top-right corner).
					bg := len(ref) - 4
					if absInt(int(ref[k])-int(ref[bg]))+absInt(int(ref[k+1])-int(ref[bg+1]))+absInt(int(ref[k+2])-int(ref[bg+2])) > 30 {
						lit++
					}
					for c := 0; c < 3; c++ {
						if d := int(ref[k+c]) - int(got[k+c]); d > 1 || d < -1 {
							over++
							break
						}
					}
				}
				t.Logf("%s %s: max difference %d, %d of %d pixels past 1 level; %d terrain; channel ranges %v..%v; bins %d → %d",
					lay.name, p.name, maxD, over, len(ref)/4, lit, lo, hi, refBins, gotBins)
				if lit < len(ref)/4/8 || hi[0]-lo[0] < 40 {
					t.Fatalf("%s %s: the frame shows too little terrain to compare (%d terrain pixels, red %d..%d)", lay.name, p.name, lit, lo[0], hi[0])
				}
				if maxD > 2 {
					t.Errorf("%s %s: a four-layer tile differs from the eight-layer union by %d levels (%d pixels past 1)", lay.name, p.name, maxD, over)
				}
			}
		}()
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
