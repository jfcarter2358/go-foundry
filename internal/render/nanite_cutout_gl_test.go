package render

// nanite_cutout_gl_test.go — alpha-cutout, double-sided cooked meshes (the
// Poly Haven plants' leaves and twig cards, docs/canyon/PHASE_4.md) on every
// path a cooked placement is drawn by: the visibility buffer (whose ID pass,
// nanite_vis_frag.glsl, decides coverage from the albedo's alpha), the GPU
// cull's forward draw, and the classic path; page pool and flat pool.
//
// A 2 m card, its texture red and opaque on the left half and fully
// transparent on the right, stands in front of a brick card: the left half
// must show red, the right half the brick behind it — and both from the
// card's back, since the slot is double-sided.
//
//	FOUNDRY_GL_TEST=1 go test -timeout 600s -run NaniteCutout ./internal/render/

import (
	"image"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/model"
	"foundry/internal/nanitecook"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// cutoutCard cooks a w×h vertical card at z, facing +Z, one double-sided
// slot textured tex.
func cutoutCard(t *testing.T, name, tex string, w, h, z float32) string {
	t.Helper()
	m := &model.ImportMesh{
		Positions: []mgl32.Vec3{{-w / 2, 0, z}, {w / 2, 0, z}, {w / 2, h, z}, {-w / 2, h, z}},
		Normals:   []mgl32.Vec3{{0, 0, 1}, {0, 0, 1}, {0, 0, 1}, {0, 0, 1}},
		// The engine's V runs down the image: v = 0 at the card's top.
		UVs:     []mgl32.Vec2{{0, 1}, {1, 1}, {1, 0}, {0, 0}},
		Indices: []uint32{0, 1, 2, 0, 2, 3},
		TriSlot: []int32{0, 0},
		Slots:   []model.ImportSlot{{Name: "leaf", Texture: tex, BaseColor: [4]float32{1, 1, 1, 1}, DoubleSided: true}},
	}
	out := filepath.Join(t.TempDir(), name+".fnm")
	if _, err := nanitecook.CookMeshFile(m, out, true, nanitecook.Options{}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestNaniteCutoutShowsThroughOnEveryPath(t *testing.T) {
	w := newVisWorld(t)
	// The leaf texture: left half opaque red, right half alpha 0.
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			o := img.PixOffset(x, y)
			if x < 32 {
				img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = 230, 30, 30, 255
			} else {
				img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = 30, 230, 30, 0
			}
		}
	}
	if err := gfx.UpdateTextureFromRGBA("cutout_test_leaf", img); err != nil {
		t.Fatal(err)
	}
	gfx.SetTextureHasAlpha("cutout_test_leaf", true)
	leaf := cutoutCard(t, "leaf", "cutout_test_leaf", 2, 2, 0)
	wall := cutoutCard(t, "wall", "brick", 6, 4, -1.5)

	prevStream := NaniteStream
	t.Cleanup(func() { NaniteStream = prevStream })
	for _, p := range []struct {
		name        string
		gpu, vis    bool
		stream      bool
		back        bool
		wantVisBins bool
	}{
		{"vis, page pool", true, true, true, false, true},
		{"vis, flat pool", true, true, false, false, true},
		{"vis, from behind", true, true, true, true, true},
		{"forward, page pool", true, false, true, false, false},
		{"forward, from behind", true, false, true, true, false},
		{"classic", false, false, true, false, false},
		{"classic, from behind", false, false, true, true, false},
	} {
		func() {
			NaniteStream, NaniteVisBuffer = p.stream, p.vis
			camZ, dirZ := float32(3), float32(-1)
			if p.back {
				camZ, dirZ = -3, 1 // behind the card (the wall left out), looking at its back
			}
			gfx.Cam.Position, gfx.Cam.Direction = mgl32.Vec3{0, 1, camZ}, mgl32.Vec3{0, 0, dirZ}
			gfx.UpdateCamMatrix()
			lvl := &world.Level{}
			for _, f := range []string{leaf, wall} {
				s := world.NewScenery("card", f, mgl32.Vec3{})
				s.NoNanite = !p.gpu
				if err := s.Load(); err != nil {
					t.Fatal(err)
				}
				if p.back && f == wall {
					continue // behind the camera then
				}
				lvl.Scenery = append(lvl.Scenery, s)
			}
			lvl.Lights.Global = world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.8,
				Wrap: world.DefaultLightWrap, Hemisphere: world.DefaultAmbientHemisphere}
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
			// (From behind, this one-card frame was observed to shade with
			// no material bins — the forward fallback — while still
			// cutting correctly; only the front poses pin the path.)
			if bins := DebugStats.NaniteVisBins; !p.back && (bins > 0) != p.wantVisBins {
				t.Errorf("%s: %d material bins shaded", p.name, bins)
			}
			dumpPNG("cutout_"+strings.NewReplacer(",", "", " ", "_").Replace(p.name), px, int(w.size))
			// The card's red half is at x < 0 from either side (meanAround
			// projects the world point).
			solid, clear := mgl32.Vec3{-0.5, 1, 0}, mgl32.Vec3{0.5, 1, 0}
			a, b := meanAround(px, int(w.size), solid, 3), meanAround(px, int(w.size), clear, 3)
			t.Logf("%s: opaque half %.0f, cut half %.0f", p.name, a, b)
			if !(a[0] > a[1]*2 && a[0] > 60) {
				t.Errorf("%s: the opaque half is not the red leaf: %.0f", p.name, a)
			}
			if b[0] > b[1]*2 && b[0] > 60 && b[1] < 60 {
				t.Errorf("%s: the cut half still shows the leaf's red: %.0f", p.name, b)
			}
			if !p.back && b[1] > b[0]*1.5 {
				t.Errorf("%s: the cut half shows the texture's hidden green: %.0f", p.name, b)
			}
		}()
	}
}
