package render

// normal_map_constant_uv_gl_test.go — a normal-mapped surface whose UVs do
// not change across it must still light, on every path a cooked placement
// is drawn by (docs/regions/PRESETS_4.md, bug 1).
//
// fragment.glsl builds a normal map's tangent frame from screen derivatives
// of position and UV (cotangentFrame). Where the UV is the same at every
// corner the derivatives are zero, and the frame's scale was
// inversesqrt(0) = infinity: 0 * inf = NaN, a NaN normal, NaN light. The
// GTX 1070 drew those pixels white and Mesa d3d12 black — the biome
// showcase's fir_tree_01 trunks (its trunk_c primitive has no TEXCOORD_0, so
// every vertex cooks at UV 0,0), and with TAA and bloom spreading the NaN,
// white blocks over the scanned scenery seen from 600 m up.
//
// A 2 m card with every UV at (0.25, 0.25), textured with a mid-grey albedo
// that has a TILTED normal map, stands in front of the camera beside a
// control card with ordinary UVs. Both must come out a lit grey: neither
// black (Mesa's NaN) nor white (NVIDIA's), and within a margin of each other
// — they face the same way, and a tilt about any frame shifts the light by
// little.
//
//	FOUNDRY_GL_TEST=1 go test -timeout 600s -run NormalMapConstantUV ./internal/render/

import (
	"image"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/model"
	"foundry/internal/nanitecook"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// constUVCard cooks a w×h card centred on x, facing +Z, one slot textured
// tex; constUV sets every corner's UV to (0.25, 0.25).
func constUVCard(t *testing.T, name, tex string, x float32, constUV bool) string {
	t.Helper()
	uvs := []mgl32.Vec2{{0, 1}, {1, 1}, {1, 0}, {0, 0}}
	if constUV {
		for i := range uvs {
			uvs[i] = mgl32.Vec2{0.25, 0.25}
		}
	}
	m := &model.ImportMesh{
		Positions: []mgl32.Vec3{{x - 1, 0, 0}, {x + 1, 0, 0}, {x + 1, 2, 0}, {x - 1, 2, 0}},
		Normals:   []mgl32.Vec3{{0, 0, 1}, {0, 0, 1}, {0, 0, 1}, {0, 0, 1}},
		UVs:       uvs,
		Indices:   []uint32{0, 1, 2, 0, 2, 3},
		TriSlot:   []int32{0, 0},
		Slots:     []model.ImportSlot{{Name: "bark", Texture: tex, BaseColor: [4]float32{1, 1, 1, 1}, DoubleSided: true}},
	}
	out := filepath.Join(t.TempDir(), name+".fnm")
	if _, err := nanitecook.CookMeshFile(m, out, true, nanitecook.Options{}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestNormalMapConstantUVLightsOnEveryPath(t *testing.T) {
	w := newVisWorld(t)
	const tex = "constuv_test_grey"
	albedo := image.NewRGBA(image.Rect(0, 0, 32, 32))
	normal := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for i := 0; i < len(albedo.Pix); i += 4 {
		copy(albedo.Pix[i:i+4], []uint8{150, 150, 150, 255})
		// Tilted about 25° off the surface's normal: a frame of NaN turns
		// this into NaN light; a flat map would hide it (its xy is zero).
		copy(normal.Pix[i:i+4], []uint8{128 + 54, 128 + 20, 128 + 115, 255})
	}
	if err := gfx.UpdateTextureFromRGBA(tex, albedo); err != nil {
		t.Fatal(err)
	}
	gfx.SetNormalMap(tex, normal)
	gfx.DrainTextureUploads()
	t.Cleanup(func() { gfx.ClearNormalMap(tex) })
	if !gfx.HasNormalMap(tex) {
		t.Fatal("the test texture's normal map is not resident")
	}
	flat := constUVCard(t, "constuv", tex, -1.1, true)
	ctrl := constUVCard(t, "control", tex, 1.1, false)

	prevStream := NaniteStream
	t.Cleanup(func() { NaniteStream = prevStream })
	for _, p := range []struct {
		name     string
		gpu, vis bool
	}{
		{"vis buffer", true, true},
		{"forward", true, false},
		{"classic", false, false},
	} {
		func() {
			NaniteStream, NaniteVisBuffer = true, p.vis
			gfx.Cam.Position, gfx.Cam.Direction = mgl32.Vec3{0, 1, 4}, mgl32.Vec3{0, 0, -1}
			gfx.UpdateCamMatrix()
			lvl := &world.Level{}
			for _, f := range []string{flat, ctrl} {
				s := world.NewScenery("card", f, mgl32.Vec3{})
				s.NoNanite = !p.gpu
				if err := s.Load(); err != nil {
					t.Fatal(err)
				}
				lvl.Scenery = append(lvl.Scenery, s)
			}
			lvl.Lights.Global = world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.6,
				Wrap: world.DefaultLightWrap, Hemisphere: world.DefaultAmbientHemisphere}
			lvl.Lights.Directional = []world.DirectionalLight{{ID: "sun", Yaw: -60, Pitch: -40,
				Color: mgl32.Vec3{1, 1, 1}, Brightness: 1, Dynamic: true}}
			lvl.Lights.Directional[0].SyncDirection()
			if err := world.RebuildIndexes(lvl); err != nil {
				t.Fatal(err)
			}
			world.UploadLights(lvl.Lights)
			defer func() {
				world.DeleteScenery(lvl)
				naniteScenery.lvl = nil
				world.UploadLights(world.LightSet{})
			}()
			w.frame(lvl)
			px := w.frame(lvl)
			dumpPNG("constuv_"+strings.ReplaceAll(p.name, " ", "_"), px, int(w.size))
			a := meanAround(px, int(w.size), mgl32.Vec3{-1.1, 1, 0}, 3)
			b := meanAround(px, int(w.size), mgl32.Vec3{1.1, 1, 0}, 3)
			t.Logf("%s: constant-UV card %.0f, control card %.0f", p.name, a, b)
			for k, v := range [][3]float64{a, b} {
				lum := (v[0] + v[1] + v[2]) / 3
				if lum < 25 || lum > 245 {
					t.Errorf("%s: card %d is %.0f — black or white, the NaN normal's colours", p.name, k, v)
				}
			}
			if d := math.Abs((a[0]+a[1]+a[2])/3 - (b[0]+b[1]+b[2])/3); d > 60 {
				t.Errorf("%s: the constant-UV card %.0f is far from the control %.0f", p.name, a, b)
			}
		}()
	}
}
