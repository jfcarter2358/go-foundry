package levelio

// scenery_save_test.go — props through the YAML boundary.
//
// The load half matters more here than for any other placed object, because a
// prop's model is loaded per placement and a brush makes hundreds of them: a
// level naming one mesh that has since been renamed must still open, with the
// other 299 props intact. That rule is one `log.Printf` where every other kind
// has a `return err`, and nothing but a test keeps it that way.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// TestSceneryLevelRoundTripsThroughYAML takes the long way round — Level to
// RawLevel to text and back — because that is the path a save and an open
// actually take, and a key that marshals but does not unmarshal looks fine at
// every shorter stop.
func TestSceneryLevelRoundTripsThroughYAML(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	lvl := world.Level{Scenery: []world.Scenery{
		{
			ID: "rock_01", Model: "models/rock.fmdl",
			Position: mgl32.Vec3{3, 0, -4}, Rotation: mgl32.Vec3{0, 1.5, 0},
			Scale: mgl32.Vec3{1.5, 1.5, 1.5}, Solid: true,
		},
		{
			ID: "fern_01", Model: "models/fern.fmdl",
			Position: mgl32.Vec3{-2, 1, 8},
			Scale:    mgl32.Vec3{1, 1, 1}, Solid: false,
		},
	}}

	raw, err := LevelToRaw(&lvl)
	if err != nil {
		t.Fatalf("LevelToRaw: %v", err)
	}
	text, err := yaml.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back world.RawLevel
	if err := yaml.Unmarshal(text, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back.Scenery) != 2 {
		t.Fatalf("got %d props back, want 2:\n%s", len(back.Scenery), text)
	}

	rock := world.SceneryFromRaw(back.Scenery[0])
	if rock.ID != "rock_01" || rock.Model != "models/rock.fmdl" ||
		rock.Position != (mgl32.Vec3{3, 0, -4}) || !rock.Solid ||
		rock.Scale != (mgl32.Vec3{1.5, 1.5, 1.5}) {
		t.Errorf("rock changed across the file: %+v", rock)
	}
	if fern := world.SceneryFromRaw(back.Scenery[1]); fern.Solid {
		t.Error("a prop marked not solid came back solid")
	}
}

// A level with no props must not grow a `scenery:` key. Every level already on
// disk goes through this path on the next save.
func TestPropLessLevelGainsNoSceneryKey(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	var lvl world.Level
	raw, err := LevelToRaw(&lvl)
	if err != nil {
		t.Fatalf("LevelToRaw: %v", err)
	}
	if raw.Scenery != nil {
		t.Fatalf("an empty level produced %d props", len(raw.Scenery))
	}
	out, err := yaml.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "scenery:") {
		t.Fatalf("an empty level wrote a scenery key:\n%s", out)
	}
}

// The rule this file exists for: one missing mesh costs its own placement's
// geometry and nothing else. The level opens, the other props are there, and
// the broken one is still in the list to be repaired.
func TestOneMissingModelDoesNotFailTheLoad(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	dir := t.TempDir()
	good := filepath.Join(dir, "box.obj")
	if err := os.WriteFile(good, []byte("v 0 0 0\nv 1 0 0\nv 1 1 1\nf 1 2 3\n"), 0o644); err != nil {
		t.Fatalf("write obj: %v", err)
	}

	raw := world.RawLevel{Scenery: []world.RawScenery{
		{ID: "gone", Model: filepath.Join(dir, "not_here.obj")},
		{ID: "here", Model: good},
	}}

	var lvl world.Level
	if err := ApplyRawLevel(raw, &lvl); err != nil {
		t.Fatalf("a level naming one missing model refused to open: %v", err)
	}
	if len(lvl.Scenery) != 2 {
		t.Fatalf("got %d props, want 2", len(lvl.Scenery))
	}
	if lvl.Scenery[0].ID != "gone" {
		t.Errorf("the broken placement was dropped: %+v", lvl.Scenery)
	}
	if !lvl.Scenery[1].HasBounds() {
		t.Error("the prop beside the broken one lost its bounds too")
	}
}

// A level file with two props claiming the same name gets the second renamed
// rather than left ambiguous — the guarantee every other placed kind gets.
func TestDuplicateSceneryIDsAreMadeUnique(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	raw := world.RawLevel{Scenery: []world.RawScenery{
		{ID: "rock", Model: "models/rock.fmdl"},
		{ID: "rock", Model: "models/rock.fmdl"},
	}}
	var lvl world.Level
	if err := ApplyRawLevel(raw, &lvl); err != nil {
		t.Fatalf("ApplyRawLevel: %v", err)
	}
	if lvl.Scenery[0].ID == lvl.Scenery[1].ID {
		t.Fatalf("two props kept the same ID: %q", lvl.Scenery[0].ID)
	}
}
