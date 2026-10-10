package world

// model_indexed_test.go — models load indexed (gfx.IndexedModels) and draw
// exactly the corners they drew de-indexed. Phase 0 of the Nanite plan.
//
// Every shipped .fmdl in the repo is checked, through both front doors a
// model comes in by (a Scenery placement and an entity attachment), plus a
// rigged cube for the skin attributes. FOUNDRY_NANITE_MODELS_DIR adds a game
// project's models directory to the sweep, which is how docs/nanite/PHASE_0.md
// measured the vertex savings on the level1-3 cast:
//
//	FOUNDRY_NANITE_MODELS_DIR=/path/to/project/resources/models \
//	    go test -run IndexedModels -v ./internal/world/

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/model"
	"foundry/internal/testenv"

	"github.com/go-gl/mathgl/mgl32"
)

func bitsEqual(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Float32bits(a[i]) != math.Float32bits(b[i]) {
			return false
		}
	}
	return true
}

// loadBothWays loads the submeshes of one model through load with the switch
// off and then on, uploads off throughout.
func loadBothWays(t *testing.T, load func() []gfx.Geometry) (soup, indexed []gfx.Geometry) {
	t.Helper()
	prevI, prevU := gfx.IndexedModels, gfx.GeometryUploadsEnabled
	t.Cleanup(func() { gfx.IndexedModels, gfx.GeometryUploadsEnabled = prevI, prevU })
	gfx.GeometryUploadsEnabled = false
	gfx.IndexedModels = false
	soup = load()
	gfx.IndexedModels = true
	indexed = load()
	return soup, indexed
}

// compareSubmeshes fails unless every indexed submesh de-indexes to its soup
// twin, attribute for attribute, and returns the corner and pool totals.
func compareSubmeshes(t *testing.T, name string, soup, indexed []gfx.Geometry) (corners, pool int) {
	t.Helper()
	if len(soup) != len(indexed) {
		t.Fatalf("%s: %d submeshes de-indexed, %d indexed", name, len(soup), len(indexed))
	}
	for i := range soup {
		s, x := &soup[i], &indexed[i]
		if s.IsIndexed() {
			t.Errorf("%s[%d]: indexed with the switch off", name, i)
		}
		if !x.IsIndexed() {
			t.Errorf("%s[%d]: not indexed with the switch on", name, i)
			continue
		}
		xm := x.Mesh()
		if err := xm.Validate(); err != nil {
			t.Errorf("%s[%d]: %v", name, i, err)
			continue
		}
		back, sm := xm.Deindex(), s.Mesh()
		for _, c := range []struct {
			what string
			a, b []float32
		}{
			{"vertices", sm.Vertices, back.Vertices},
			{"normals", sm.Normals, back.Normals},
			{"bone indices", sm.SkinIdx, back.SkinIdx},
			{"bone weights", sm.SkinWt, back.SkinWt},
		} {
			if !bitsEqual(c.a, c.b) {
				t.Errorf("%s[%d]: %s differ between the indexed and the de-indexed load", name, i, c.what)
			}
		}
		if x.Smooth != s.Smooth || x.Texture != s.Texture {
			t.Errorf("%s[%d]: surface state differs", name, i)
		}
		corners += x.CornerCount()
		pool += len(x.Vertices) / 5
	}
	return corners, pool
}

func modelFiles(t *testing.T) []string {
	t.Helper()
	dirs := []string{filepath.Join(testenv.RepoRoot(t), "resources", "models")}
	if d := os.Getenv("FOUNDRY_NANITE_MODELS_DIR"); d != "" {
		dirs = append(dirs, d)
	}
	var out []string
	for _, d := range dirs {
		ents, err := os.ReadDir(d)
		if err != nil {
			t.Fatalf("models dir: %v", err)
		}
		for _, e := range ents {
			if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), model.ModelExt) {
				out = append(out, filepath.Join(d, e.Name()))
			}
		}
	}
	return out
}

func TestIndexedModelsDrawTheSameCorners(t *testing.T) {
	files := modelFiles(t)
	if len(files) == 0 {
		t.Fatal("no .fmdl to check")
	}
	var totalCorners, totalPool int
	for _, path := range files {
		name := filepath.Base(path)
		soup, ind := loadBothWays(t, func() []gfx.Geometry {
			s := Scenery{ID: "probe", Model: path}
			if err := s.Load(); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			return s.Submeshes
		})
		c, p := compareSubmeshes(t, name+" (scenery)", soup, ind)

		soup, ind = loadBothWays(t, func() []gfx.Geometry {
			m := EntityModel{Name: "probe", Path: path, Visible: true}
			if err := m.Load("probe"); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			return m.Submeshes
		})
		compareSubmeshes(t, name+" (attachment)", soup, ind)

		if c > 0 {
			t.Logf("%-28s %7d corners -> %7d vertices (%.2fx)", name, c, p, float64(c)/float64(max(p, 1)))
		}
		totalCorners += c
		totalPool += p
	}
	t.Logf("all %d models: %d corners -> %d vertices (%.2fx fewer vertex records)",
		len(files), totalCorners, totalPool, float64(totalCorners)/float64(max(totalPool, 1)))
}

// A rigged model's bone indices and weights weld with it: an attachment that
// animates must still find every corner's influences.
func TestIndexedModelsKeepSkinAttributes(t *testing.T) {
	doc := model.NewCubeDoc()
	lo := doc.AddBone(mgl32.Vec3{0, -0.5, 0}, mgl32.Vec3{0, 0, 0})
	hi := doc.AddBone(mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 0.5, 0})
	doc.RenameBone(lo, "lo")
	doc.RenameBone(hi, "hi")
	var top, bottom []int
	for i, v := range doc.Verts {
		if v.Y() > 0 {
			top = append(top, i)
		} else {
			bottom = append(bottom, i)
		}
	}
	doc.AssignVertsToBone(bottom, lo, 1)
	doc.AssignVertsToBone(top, hi, 1)
	ci := doc.AddClip("idle")
	doc.Clip(ci).Duration = 1
	doc.Clip(ci).SetKey(lo, 0, model.IdentBonePose())
	path := filepath.Join(t.TempDir(), "rig"+model.ModelExt)
	if err := model.SaveModel(doc, path); err != nil {
		t.Fatal(err)
	}

	soup, ind := loadBothWays(t, func() []gfx.Geometry {
		m := EntityModel{Name: "rig", Path: path, Visible: true}
		if err := m.Load("probe"); err != nil {
			t.Fatal(err)
		}
		if m.Anim == nil {
			t.Fatal("the rigged cube did not come up animated")
		}
		return m.Submeshes
	})
	compareSubmeshes(t, "rigged cube", soup, ind)
	for i := range ind {
		if len(ind[i].Mesh().SkinIdx) == 0 {
			t.Errorf("submesh %d lost its skin attributes in the weld", i)
		}
	}
}
