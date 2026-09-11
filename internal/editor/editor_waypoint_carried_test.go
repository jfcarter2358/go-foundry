package editor

// editor_waypoint_carried_test.go — a path an entity's type carries, seen from
// the level editor: drawn and picked where the entity is, selectable and
// previewable, and refused every edit, since its points belong to the type.

import (
	"strings"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// carriedWaypointLevel is waypointEditorLevel plus an entity "ship" at
// {10 0 0} carrying a two-point path, materialised as the loader would.
func carriedWaypointLevel(t *testing.T) *world.Level {
	t.Helper()
	lvl := waypointEditorLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "ship", Position: mgl32.Vec3{10, 0, 0}, Scale: mgl32.Vec3{1, 1, 1},
		WaypointAnimations: []world.WaypointAnimation{{ID: "close", Points: []world.Waypoint{
			world.NewWaypoint(mgl32.Vec3{0, 1, 0}),
			{Position: mgl32.Vec3{0, 1, -2}, Scale: mgl32.Vec3{1, 1, 1}, Time: 1},
		}}},
	})
	world.MaterializeEntityWaypointAnimations(lvl, &lvl.Entities[len(lvl.Entities)-1])
	return lvl
}

func TestACarriedPathIsPickedWhereItsEntityIsAndRefusesEdits(t *testing.T) {
	lvl := carriedWaypointLevel(t)

	origin := mgl32.Vec3{10, 1, 5}
	ai, pi, _ := pickWaypointMarker(origin, mgl32.Vec3{0, 0, -1}, origin, lvl)
	if ai != 0 || pi != 0 {
		t.Fatalf("a ray at the ship's first arrow picked animation %d point %d; want 0, 0", ai, pi)
	}

	editorSelectWaypoint(ai, pi)
	if activeWaypoint() == nil {
		t.Fatal("a carried path's point could not be selected")
	}
	if Editor.GizmoKind != gizmoKindNone || activeGizmoWaypoint() != nil {
		t.Errorf("a carried point took the gizmo (%q), which would move it in the wrong space", Editor.GizmoKind)
	}

	deleteActiveWaypoint()
	insertWaypointAt(mgl32.Vec3{0, 0, 0})
	a := &lvl.WaypointAnimations[0]
	if len(lvl.WaypointAnimations) != 1 || len(a.Points) != 2 {
		t.Errorf("editing a carried path changed it: %+v", lvl.WaypointAnimations)
	}
	if !strings.Contains(Editor.StatusMsg, "entity editor") {
		t.Errorf("the refusal said %q; it should point at the entity editor", Editor.StatusMsg)
	}
}

// Re-stamping an entity drops its paths and materialises them again, which moves
// them to the end of the list; the selection has to follow the path, not stay
// on its old index.
func TestReStampingAnEntityKeepsItsPathSelected(t *testing.T) {
	lvl := carriedWaypointLevel(t)
	lvl.WaypointAnimations = append(lvl.WaypointAnimations, world.WaypointAnimation{
		ID: "lift", Points: []world.Waypoint{world.NewWaypoint(mgl32.Vec3{})},
	})
	editorSelectWaypoint(0, 1)

	editorSyncEntityContent(lvl, &lvl.Entities[len(lvl.Entities)-1])
	if a := activeWaypointAnimation(); a == nil || a.ID != "ship.close" || Editor.ActiveWaypointIdx != 1 {
		t.Errorf("after the re-stamp the selection is %+v point %d; want ship.close point 1", a, Editor.ActiveWaypointIdx)
	}

	editorReleaseEntityContent(lvl, "ship")
	if activeWaypointAnimation() != nil {
		t.Errorf("the selection outlived the path it named: %+v", activeWaypointAnimation())
	}
}
