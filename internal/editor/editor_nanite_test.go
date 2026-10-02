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
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/gizmo"
	"foundry/internal/meshcollide"
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

// A placement's Collision group says what it collides as — the box for an
// uncooked model, the hulls or the cut for a cooked one — its mode
// round-trips through the level file, and a solid prop set to none is
// warned about.
func TestSceneryCollisionModeRoundTripsAndIsReported(t *testing.T) {
	// Pinned on: FOUNDRY_NANITE_COLLISION=0 in the environment would make
	// this a test of the box path.
	prevNC := world.NaniteCollision
	world.NaniteCollision = true
	t.Cleanup(func() { world.NaniteCollision = prevNC })
	naniteTestResources(t)
	s := world.NewScenery("ship", "models/ship.fmdl", mgl32.Vec3{})
	lvl := sceneryTestLevel(t, s)
	p := &lvl.Scenery[0]
	if err := p.Load(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(sceneryCollisionReport(p), "\n"); !strings.Contains(got, "Box from the model") {
		t.Fatalf("an uncooked prop's report: %s", got)
	}
	editorCookSceneryModel(p)
	if p.CollisionData() == nil {
		t.Fatal("the cooked ship carries no collision")
	}
	got := strings.Join(sceneryCollisionReport(p), "\n")
	want := "convex hulls"
	if p.CollisionMode() == meshcollide.ModeMesh {
		want = "collision cut"
	}
	if !strings.Contains(got, want) || !strings.Contains(got, "Cooked at 3 cm") {
		t.Fatalf("the cooked ship's report (%v): %s", p.CollisionMode(), got)
	}
	for _, m := range []meshcollide.Mode{meshcollide.ModeAuto, meshcollide.ModeBox, meshcollide.ModeHulls, meshcollide.ModeMesh, meshcollide.ModeNone} {
		p.Collision = m
		back := world.SceneryFromRaw(world.SceneryToRaw(p))
		if back.Collision != m {
			t.Errorf("mode %v came back as %v", m, back.Collision)
		}
	}
	p.Collision = meshcollide.ModeNone
	if got := strings.Join(sceneryCollisionReport(p), "\n"); !strings.Contains(got, "collision mode is none") {
		t.Errorf("a solid prop with no collision is not warned about: %s", got)
	}
}

// Show collision draws what the selected prop collides as: every edge of
// every hull in the hulls mode, the cut's edges in the mesh mode, the box's
// twelve in the box mode, nothing in none — and nothing at all while it is
// off.
func TestShowCollisionDrawsTheSelectedPropsCollision(t *testing.T) {
	// Pinned on: FOUNDRY_NANITE_COLLISION=0 in the environment would make
	// this a test of the box path.
	prevNC := world.NaniteCollision
	world.NaniteCollision = true
	t.Cleanup(func() { world.NaniteCollision = prevNC })
	naniteTestResources(t)
	s := world.NewScenery("ship", "models/ship.fmdl", mgl32.Vec3{})
	lvl := sceneryTestLevel(t, s)
	p := &lvl.Scenery[0]
	if err := p.Load(); err != nil {
		t.Fatal(err)
	}
	editorCookSceneryModel(p)
	editorSelectScenery(0)
	prev := sceneryShowCollision
	t.Cleanup(func() { sceneryShowCollision = prev; gizmo.Buf = nil })
	lines := func(on bool, m meshcollide.Mode) int {
		sceneryShowCollision = on
		p.Collision = m
		gizmo.Buf = nil
		drawSelectedSceneryCollision()
		return len(gizmo.Buf) / 12
	}
	d := p.CollisionData()
	hullEdges := 0
	for _, h := range d.Hulls {
		// A closed triangulated hull: E = 3F/2.
		hullEdges += len(h.Tris) / 3 * 3 / 2
	}
	if got := lines(true, meshcollide.ModeHulls); got != hullEdges {
		t.Errorf("hulls mode drew %d lines, want the hulls' %d edges", got, hullEdges)
	}
	if got := lines(true, meshcollide.ModeMesh); got == 0 || got > 3*d.TriangleCount() {
		t.Errorf("mesh mode drew %d lines for %d triangles", got, d.TriangleCount())
	}
	if got := lines(true, meshcollide.ModeBox); got != 12 {
		t.Errorf("box mode drew %d lines, want 12", got)
	}
	if got := lines(true, meshcollide.ModeNone); got != 0 {
		t.Errorf("none drew %d lines", got)
	}
	if got := lines(false, meshcollide.ModeHulls); got != 0 {
		t.Errorf("with Show collision off, %d lines were drawn", got)
	}
}
