package render

// nanite_color_gl_test.go — per-vertex colour and cavity on cooked meshes
// (docs/canyon/PHASE_2.md), against a real context, on every path a cooked
// placement can be drawn by:
//
//   - the visibility buffer's material pass (nanite_material_frag.glsl reads
//     the pool's colours from binding 7),
//   - the GPU cull's forward draw (the pool VAO's attribute 15),
//   - the classic path (a placement the cull does not draw: gfx.Geometry's
//     colour buffer),
//
// each with the page pool and with the flat pool. And the material program
// still BUILDS with the colour fetch in it: a failed build is not an error
// anywhere — the frame silently falls back to forward shading — so only a
// test that asks can tell.
//
//	FOUNDRY_GL_TEST=1 go test -timeout 600s -run 'NaniteColor|VisMaterialProgram' ./internal/render/

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/nanitecook"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func TestVisMaterialProgramBuilds(t *testing.T) {
	newVisWorld(t)
	var v naniteVisPass
	if err := v.build(); err != nil {
		t.Fatalf("the visibility buffer's programs do not build: %v", err)
	}
	if v.material.prog == 0 || v.matLocs == (gfx.SurfaceLocs{}) {
		t.Fatal("no material program")
	}
	// And the one the frame uses is up.
	if !naniteVis.ready() {
		t.Fatal("the frame's visibility buffer is not ready")
	}
	vert, frag, err := gfx.WorldProgramSources(filepath.Join(gfxShaderDir(t), "vertex.glsl"), filepath.Join(gfxShaderDir(t), "fragment.glsl"))
	if err != nil {
		t.Fatal(err)
	}
	src, err := materialFragmentSource(vert, frag, mustReadShader(biomeShaderPath("nanite_material_frag.glsl")))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"colorData[v]", "vx_fragVertColor", "uniform int vertexColors"} {
		if !strings.Contains(src, want) {
			t.Errorf("the material pass's source has no %q", want)
		}
	}
}

func gfxShaderDir(t *testing.T) string {
	t.Helper()
	d := filepath.Dir(biomeShaderPath("vertex.glsl"))
	if _, err := os.Stat(filepath.Join(d, "fragment.glsl")); err != nil {
		t.Fatal(err)
	}
	return d
}

// colorTestRock cooks the sample rock with every vertex one colour (nil:
// none) and returns the .fnm's path.
func colorTestRock(t *testing.T, name string, c *[4]uint8) string {
	t.Helper()
	m := nanitecook.SampleRock(4, 0.4)
	if c != nil {
		m.Colors = make([][4]uint8, len(m.Positions))
		for i := range m.Colors {
			m.Colors[i] = *c
		}
	}
	out := filepath.Join(t.TempDir(), name+".fnm")
	if _, err := nanitecook.CookMeshFile(m, out, true, nanitecook.Options{}); err != nil {
		t.Fatal(err)
	}
	return out
}

// colorTestLevel is three rocks in a row, plain, green and green fully
// occluded, textured with concrete, lit by an ambient and a sun.
func colorTestLevel(t *testing.T, rocks [3]string, gpu bool) *world.Level {
	t.Helper()
	lvl := &world.Level{}
	for i, r := range rocks {
		s := world.NewScenery("rock", r, mgl32.Vec3{-1.25 + 1.25*float32(i), 0, 0})
		s.NoNanite = !gpu
		s.Materials = map[string]string{"stone": "concrete", "moss": "concrete"}
		if err := s.Load(); err != nil {
			t.Fatal(err)
		}
		if s.NaniteGPU != gpu {
			t.Fatalf("placement NaniteGPU=%v, want %v", s.NaniteGPU, gpu)
		}
		s.Scale = mgl32.Vec3{0.5, 0.5, 0.5}
		lvl.Scenery = append(lvl.Scenery, s)
	}
	lvl.Lights.Global = world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.45,
		Wrap: world.DefaultLightWrap, Hemisphere: world.DefaultAmbientHemisphere}
	sun := world.DirectionalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.6, Yaw: 20, Pitch: -40, Dynamic: true}
	sun.SyncDirection()
	lvl.Lights.Directional = []world.DirectionalLight{sun}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	world.UploadLights(lvl.Lights)
	t.Cleanup(func() {
		world.DeleteScenery(lvl)
		world.UploadLights(world.LightSet{})
	})
	return lvl
}

// meanAround is the mean RGB of a (2r+1)² window of a size-square frame
// around the projection of p.
func meanAround(px []uint8, size int, p mgl32.Vec3, r int) [3]float64 {
	c := gfx.Projection.Mul4(gfx.Cam.Matrix).Mul4x1(p.Vec4(1))
	x := int((c[0]/c[3]*0.5 + 0.5) * float32(size))
	y := int((c[1]/c[3]*0.5 + 0.5) * float32(size))
	var sum [3]float64
	n := 0
	for j := y - r; j <= y+r; j++ {
		for i := x - r; i <= x+r; i++ {
			if i < 0 || j < 0 || i >= size || j >= size {
				continue
			}
			o := 4 * (j*size + i)
			for k := 0; k < 3; k++ {
				sum[k] += float64(px[o+k])
			}
			n++
		}
	}
	for k := range sum {
		sum[k] /= float64(max(n, 1))
	}
	return sum
}

func TestNaniteColorReachesPixelsOnEveryPath(t *testing.T) {
	w := newVisWorld(t)
	green := [4]uint8{50, 230, 50, 255}
	dark := [4]uint8{50, 230, 50, 25}
	rocks := [3]string{colorTestRock(t, "plain", nil), colorTestRock(t, "green", &green), colorTestRock(t, "dark", &dark)}
	gfx.Cam.Position, gfx.Cam.Direction = mgl32.Vec3{0, 0.4, 4}, mgl32.Vec3{0, -0.1, -1}.Normalize()
	gfx.UpdateCamMatrix()
	centres := [3]mgl32.Vec3{{-1.25, 0, 0.35}, {0, 0, 0.35}, {1.25, 0, 0.35}}

	prevStream := NaniteStream
	t.Cleanup(func() { NaniteStream = prevStream })
	type path struct {
		name        string
		gpu, vis    bool
		stream      bool
		wantVisBins bool
	}
	for _, p := range []path{
		{"vis, page pool", true, true, true, true},
		{"vis, flat pool", true, true, false, true},
		{"forward, page pool", true, false, true, false},
		{"forward, flat pool", true, false, false, false},
		{"classic", false, false, true, false},
	} {
		// Not a subtest: t.Run's goroutine is not the thread the context is
		// current on.
		func() {
			NaniteStream, NaniteVisBuffer = p.stream, p.vis
			lvl := colorTestLevel(t, rocks, p.gpu)
			defer func() {
				world.DeleteScenery(lvl)
				naniteScenery.lvl = nil
			}()
			w.frame(lvl)
			px := w.frame(lvl)
			if bins := DebugStats.NaniteVisBins; (bins > 0) != p.wantVisBins {
				t.Errorf("%s: %d material bins shaded", p.name, bins)
				return
			}
			dumpPNG("color_"+strings.NewReplacer(",", "", " ", "_").Replace(p.name), px, int(w.size))
			var m [3][3]float64
			for i := range centres {
				m[i] = meanAround(px, int(w.size), centres[i], 4)
			}
			t.Logf("%s: plain %.0f, green %.0f, occluded green %.0f", p.name, m[0], m[1], m[2])
			// The plain rock is the grey concrete; the green one is green.
			if !(m[1][1] > m[1][0]*1.8 && m[1][1] > m[1][2]*1.8) {
				t.Errorf("%s: the green rock is not green: %.0f", p.name, m[1])
			}
			if g := m[0][1] / max(m[0][0], 1); g > 1.3 || g < 0.75 {
				t.Errorf("%s: the plain rock is tinted: %.0f", p.name, m[0])
			}
			// The cavity takes the ambient and a share of the sun.
			if !(m[2][1] < m[1][1]*0.85) {
				t.Errorf("%s: the occluded rock (%.0f) is not darker than the open one (%.0f)", p.name, m[2], m[1])
			}
		}()
	}
}
