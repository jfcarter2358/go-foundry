package script

// script_entity_waypoints_test.go — a type's script sending its own brush along
// its own path.
//
// Every placement's hatch and path are qualified by the placement, so the type's
// script cannot spell either level ID. It names them through @self, or names the
// path by the name the type gave it; both have to reach this placement's.

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// carriedPathLevel is the dynamic-brush fixture with its brush made the hatch
// of an entity "ship" — as the loader materialises one — and the ship carrying
// a path "ship.close".
func carriedPathLevel(t *testing.T) (*world.Level, *world.Brush) {
	t.Helper()
	lvl := dynamicBrushLevel(t)
	ch := &lvl.Chunks[0]
	ch.FromEntity = "ship"
	b := &ch.Brushes[0]
	b.ID = world.EntityBrushID("ship", "hatch")
	lvl.Entities = append(lvl.Entities, world.Entity{ID: "ship", Scale: mgl32.Vec3{1, 1, 1}})
	lvl.WaypointAnimations = []world.WaypointAnimation{{
		ID: world.EntityWaypointAnimationID("ship", "close"), FromEntity: "ship",
		Points: []world.Waypoint{{Position: mgl32.Vec3{0, 1, 0}, Scale: mgl32.Vec3{1, 1, 1}, Time: 1}},
	}}
	return lvl, b
}

func TestATypesScriptSendsItsHatchAlongItsPathThroughSelf(t *testing.T) {
	lvl, hatch := carriedPathLevel(t)
	runScript(t, lvl, "ship", script(`    play_waypoints(@self.hatch, @self.close)`))
	if hatch.Waypoints == nil || hatch.Waypoints.Animation != "ship.close" {
		t.Fatalf("@self.hatch along @self.close left the hatch following %+v; want ship.close", hatch.Waypoints)
	}
}

func TestATypesScriptNamesItsPathByTheTypesName(t *testing.T) {
	lvl, hatch := carriedPathLevel(t)
	runScript(t, lvl, "ship", script(`    play_waypoints(@self.hatch, "close")`))
	if hatch.Waypoints == nil || hatch.Waypoints.Animation != "ship.close" {
		t.Fatalf(`"close" from the ship's script left the hatch following %+v; want ship.close`, hatch.Waypoints)
	}
}
