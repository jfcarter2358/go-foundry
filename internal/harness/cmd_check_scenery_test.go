package harness

// cmd_check_scenery_test.go — `foundry check`'s warnings about a prop's
// collision (checkSceneryCollision, world.SceneryCollisionWarnings), run
// as the command line on a level saved to disk with real cooks in it.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/levelio"
	"foundry/internal/meshcollide"
	"foundry/internal/nanite"
	"foundry/internal/nanitecook"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func TestCheckWarnsAboutPropCollision(t *testing.T) {
	// Pinned on: FOUNDRY_NANITE_COLLISION=0 in the environment would make
	// this a test of the box path.
	prevNC := world.NaniteCollision
	world.NaniteCollision = true
	t.Cleanup(func() { world.NaniteCollision = prevNC })
	dir := scratchProject(t, "scratch")
	models := filepath.Join(dir, "resources", "models")

	// A closed mesa and an open 4 x 4 sheet, cooked.
	writeMesaOBJ(t, filepath.Join(models, "mesa.obj"))
	var b strings.Builder
	for z := 0; z <= 4; z++ {
		for x := 0; x <= 4; x++ {
			fmt.Fprintf(&b, "v %d %g %d\n", x, 0.1*float32(x*z%3), z)
		}
	}
	for z := 0; z < 4; z++ {
		for x := 0; x < 4; x++ {
			a := z*5 + x + 1
			fmt.Fprintf(&b, "f %d %d %d\nf %d %d %d\n", a, a+5, a+1, a+1, a+5, a+6)
		}
	}
	if err := os.WriteFile(filepath.Join(models, "sheet.obj"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"mesa.obj", "sheet.obj"} {
		if _, err := nanitecook.CookFile(filepath.Join(models, m), "", true, nanitecook.Options{}); err != nil {
			t.Fatal(err)
		}
	}

	room := world.Chunk{ID: "room"}
	room.Brushes = world.NewHollowRoom("room", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{30, 6, 30}, 0.5, "concrete")
	mesa := world.NewScenery("mesa_ok", "models/mesa.fnm", mgl32.Vec3{2, 0, 2})
	sheet := world.NewScenery("sheet_hulls", "models/sheet.fnm", mgl32.Vec3{20, 0, 20})
	sheet.Collision = meshcollide.ModeHulls
	none := world.NewScenery("solid_none", "models/mesa.fnm", mgl32.Vec3{2, 3, 2})
	none.Collision = meshcollide.ModeNone
	lvl := &world.Level{Chunks: []world.Chunk{room}, Scenery: []world.Scenery{mesa, sheet, none}}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	if err := levelio.SaveLevel(lvl, levelio.LevelPath("props")); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	code := CheckMain(dir, []string{"props"}, &out)
	got := out.String()
	if code != 0 {
		t.Errorf("warnings failed the check (exit %d):\n%s", code, got)
	}
	for _, want := range []string{
		"WARNING scenery sheet_hulls", "open edges",
		"WARNING scenery solid_none", "collision mode is none",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the check printed no %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "mesa_ok") {
		t.Errorf("a watertight mesa in the mesh mode was warned about:\n%s", got)
	}
}

// The hull cap and the vertex cap are warned about in the hulls mode only.
func TestSceneryCollisionWarningsForTheCaps(t *testing.T) {
	// Pinned on: FOUNDRY_NANITE_COLLISION=0 in the environment would make
	// this a test of the box path.
	prevNC := world.NaniteCollision
	world.NaniteCollision = true
	t.Cleanup(func() { world.NaniteCollision = prevNC })
	d := &meshcollide.Data{Hulls: make([]meshcollide.Hull, 16), HullCapHit: true, Concavity: 0.3, VertCapped: 3, VertCapError: 0.2,
		Tris: []uint32{0, 1, 2}}
	s := world.NewScenery("rock", "models/rock.fnm", mgl32.Vec3{})
	s.LocalMin, s.LocalMax = mgl32.Vec3{-1, 0, -1}, mgl32.Vec3{1, 2, 1}
	s.Nanite = nil
	if w := world.SceneryCollisionWarnings(&s); len(w) != 0 {
		t.Fatalf("an uncooked box prop warned: %v", w)
	}
	s.Nanite = cookedStub(d)
	s.Collision = meshcollide.ModeHulls
	w := strings.Join(world.SceneryCollisionWarnings(&s), "\n")
	if !strings.Contains(w, "hull cap") || !strings.Contains(w, "capped to") {
		t.Errorf("hulls mode warnings: %s", w)
	}
	s.Collision = meshcollide.ModeMesh
	if w := world.SceneryCollisionWarnings(&s); len(w) != 0 {
		t.Errorf("the mesh mode warned about hull caps: %v", w)
	}
}

func cookedStub(d *meshcollide.Data) *nanite.Mesh { return &nanite.Mesh{Collision: d} }
