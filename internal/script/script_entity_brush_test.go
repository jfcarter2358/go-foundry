package script

// script_entity_brush_test.go — addressing the brushes an entity is built out
// of, from a script.
//
// SCRIPTING.md's "A brush an entity carries" describes `@dropship.hatch` — an
// entity ID, then the definition-local name of one of its brushes — and that is
// what these check. The materialised brush's level ID is the two joined
// (world.EntityBrushID), and it is deliberately NOT what a script writes: a
// script names the placement and the part, and the qualification is the
// engine's business.

import (
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
	"foundry/internal/world"
)

// shipLevel is hostTestLevel with an entity whose brushes have been
// materialised the way levelio does it: one frame-owned chunk, brush IDs
// qualified by the entity.
func shipLevel(t *testing.T) *world.Level {
	t.Helper()
	lvl := hostTestLevel(t)

	ship := world.Entity{ID: "ship", Type: "dropship", Visible: true,
		Position: mgl32.Vec3{100, 0, 0}, Scale: mgl32.Vec3{1, 1, 1}}
	ship.Brushes = world.NewHollowRoom("hold", mgl32.Vec3{-2, 0, -2}, mgl32.Vec3{2, 3, 2}, 0.25, "")
	ship.Brushes[0].ID = "hatch"
	ship.Brushes[0].Dynamic = true
	ship.Brushes[1].ID = "hull"
	lvl.Entities = append(lvl.Entities, ship)

	geo := world.Chunk{
		ID:         world.EntityGeometryChunkID("ship"),
		FrameOwner: "ship",
		FromEntity: "ship",
		Scale:      1,
	}
	for i := range ship.Brushes {
		b := ship.Brushes[i]
		b.ID = world.EntityBrushID("ship", b.ID)
		geo.Brushes = append(geo.Brushes, b)
	}
	lvl.Chunks = append(lvl.Chunks, geo)
	world.BuildFrames(lvl)
	world.RebuildSpatialHash(lvl)
	return lvl
}

func TestAnEntityBrushIsAddressedByItsDefinitionName(t *testing.T) {
	lvl := shipLevel(t)

	got := runScript(t, lvl, "", "def main() -> null\n    return @ship.hatch.id\nend")
	if !flang.Equal(got, flang.StrVal("ship.hatch")) {
		t.Errorf("@ship.hatch.id = %v, want the qualified level ID %q", got, "ship.hatch")
	}
	got = runScript(t, lvl, "", "def main() -> null\n    return @ship.hatch.kind\nend")
	if !flang.Equal(got, flang.AtomVal("brush")) {
		t.Errorf("@ship.hatch.kind = %v, want brush", got)
	}
}

func TestAnEntityBrushWritesInTheEntitysOwnCoordinates(t *testing.T) {
	lvl := shipLevel(t)

	runScript(t, lvl, "", script("    @ship.hatch.prop.position = <0.0, 1.0, 0.0>"))
	got := lvl.Chunks[1].Brushes[0].Position()
	if !vecNear(got, mgl32.Vec3{0, 1, 0}, 1e-4) {
		t.Fatalf("the hatch is at %v, want the entity-local <0,1,0> the script wrote", got)
	}
}

func TestEntityBrushesListsThemAll(t *testing.T) {
	lvl := shipLevel(t)

	got := runScript(t, lvl, "", "def main() -> null\n    return len(@ship.brushes)\nend")
	if !flang.Equal(got, flang.IntVal(len(lvl.Chunks[1].Brushes))) {
		t.Errorf("len(@ship.brushes) = %v, want %d", got, len(lvl.Chunks[1].Brushes))
	}

	got = runScript(t, lvl, "", "def main() -> null\n    return @ship.brushes[0].id\nend")
	if !flang.Equal(got, flang.StrVal("ship.hatch")) {
		t.Errorf("@ship.brushes[0].id = %v, want %q", got, "ship.hatch")
	}
}

func TestEntityGeometryNamesItsChunk(t *testing.T) {
	lvl := shipLevel(t)
	got := runScript(t, lvl, "", "def main() -> null\n    return @ship.geometry.kind\nend")
	if !flang.Equal(got, flang.AtomVal("chunk")) {
		t.Errorf("@ship.geometry.kind = %v, want chunk", got)
	}
}

// A name that is not one of the entity's brushes is still an error rather than
// a ref to nothing: the fallback resolves against the level's brushes, so a
// typo cannot produce a ref whose every property write silently does nothing.
func TestAnUnknownEntityMemberIsStillAnError(t *testing.T) {
	lvl := shipLevel(t)
	err := runScriptErr(t, lvl, "", script("    return @ship.hatchh.id"))
	if err == nil {
		t.Fatal("a misspelled brush name was accepted")
	}
	if !strings.Contains(err.Error(), "hatchh") {
		t.Errorf("the error is %q, want it to name the member that was written", err)
	}
}

// The linter has to follow the same step, or `@ship.hatch.prop.dynamic` is
// checked against the entity property table and flagged as a typo.
func TestLintFollowsAnEntityBrushStep(t *testing.T) {
	lvl := shipLevel(t)

	if got := lintSource(t, lvl, script("    x = @ship.hatch.prop.dynamic")); len(got) != 0 {
		t.Errorf("reading a brush property through an entity was flagged: %v", got)
	}
	if got := lintSource(t, lvl, script("    x = @ship.geometry.prop.enabled")); len(got) != 0 {
		t.Errorf("reading a chunk property through @ship.geometry was flagged: %v", got)
	}
	// And it still catches a real typo on the brush.
	got := lintSource(t, lvl, script("    x = @ship.hatch.prop.dynamick"))
	wantOneIssue(t, got, "brush", "dynamick")
}

// The doc example: a loop over an entity's brushes, writing through the
// element ref. SCRIPTING.md's "A brush an entity carries" prints this.
func TestLoopingOverAnEntitysBrushes(t *testing.T) {
	lvl := shipLevel(t)
	runScript(t, lvl, "", script("    for b in @ship.brushes\n        b.prop.solid = false\n    end"))
	for i := range lvl.Chunks[1].Brushes {
		if lvl.Chunks[1].Brushes[i].Solid {
			t.Errorf("brush %q is still solid", lvl.Chunks[1].Brushes[i].ID)
		}
	}
}
