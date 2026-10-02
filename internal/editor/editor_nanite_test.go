package editor

// editor_nanite_test.go — the editor's Nanite actions, with no window and no
// GL: "Cook and use" cooks a placement's model beside itself and points the
// placement at the cooked file (undoably), the scenery picker offers cooked
// meshes, Cook Nanite Meshes cooks the glTF under resources/, and a slot
// override round-trips through the level file.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/model"
	"foundry/internal/nanitecook"
	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func naniteTestResources(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = dir
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	models := filepath.Join(dir, model.ModelsDirName)
	if err := os.MkdirAll(models, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(testenv.RepoRoot(t), "resources", "models", "dropship.fmdl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(models, "ship.fmdl"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCookAndUsePointsThePlacementAtTheCook(t *testing.T) {
	dir := naniteTestResources(t)
	s := world.NewScenery("ship", "models/ship.fmdl", mgl32.Vec3{})
	lvl := sceneryTestLevel(t, s)
	p := &lvl.Scenery[0]
	if err := p.Load(); err != nil {
		t.Fatal(err)
	}
	editorCookSceneryModel(p)
	if p.Model != "models/ship.fnm" || p.Nanite == nil {
		t.Fatalf("placement model %q, cooked mesh %v", p.Model, p.Nanite != nil)
	}
	if p.NaniteGPU {
		t.Fatal("a headless placement claims the GPU path")
	}
	if _, err := os.Stat(filepath.Join(dir, "models", "ship.fnm")); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(sceneryModelFileNames(), "models/ship.fnm") {
		t.Fatalf("the scenery picker does not offer the cooked mesh: %v", sceneryModelFileNames())
	}
	if slices.Contains(modelFileNames(), "models/ship.fnm") {
		t.Fatal("an entity attachment's picker offers a cooked mesh it cannot load")
	}
}

func TestCookAllCooksTheGLTF(t *testing.T) {
	dir := naniteTestResources(t)
	glb, err := model.EncodeGLB(nanitecook.SampleRock(2, 0.1))
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "models", "rock.glb")
	if err := os.WriteFile(src, glb, 0o644); err != nil {
		t.Fatal(err)
	}
	editorCookAllNanite()
	fresh, err := nanitecook.UpToDate(src, nanitecook.CookedPath(src))
	if err != nil || !fresh {
		t.Fatalf("after Cook Nanite Meshes the rock is not cooked: %v %v", fresh, err)
	}
}

func TestSceneryMaterialOverridesRoundTrip(t *testing.T) {
	s := world.NewScenery("rock", "models/rock.fnm", mgl32.Vec3{1, 2, 3})
	s.Materials = map[string]string{"stone": "granite", "moss": ""}
	back := world.SceneryFromRaw(world.SceneryToRaw(&s))
	if len(back.Materials) != 2 || back.Materials["stone"] != "granite" {
		t.Fatalf("overrides came back as %v", back.Materials)
	}
	if v, ok := back.Materials["moss"]; !ok || v != "" {
		t.Fatal("an override to untextured was lost")
	}
}
