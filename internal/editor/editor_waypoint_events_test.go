package editor

// editor_waypoint_events_test.go — giving a waypoint events, headless.
//
// Checked through the point they leave behind: what the add row accepts and
// how it spells it, what it refuses, and that adding one is undoable.

import (
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func TestAddingAWaypointEventNormalisesAndRefusesWhatNoHandlerCouldMatch(t *testing.T) {
	waypointEditorLevel(t)
	placeWaypointAt(mgl32.Vec3{0, 1, 0})
	p := activeWaypoint()

	if !addWaypointEvent(p, " :Lift_Top: ") {
		t.Fatal("a valid event name was refused")
	}
	if !reflect.DeepEqual(p.Events, []string{"lift_top"}) {
		t.Fatalf("the point posts %v, want [lift_top]", p.Events)
	}
	for _, bad := range []string{"", "lift_top", "tick", "two words", "floor2"} {
		if addWaypointEvent(p, bad) {
			t.Errorf("%q was added; it should be refused", bad)
		}
	}
	if len(p.Events) != 1 {
		t.Errorf("after the refusals the point posts %v", p.Events)
	}

	removeWaypointEvent(p, 0)
	if p.Events != nil {
		t.Errorf("removing the only event left %v, want none", p.Events)
	}
}

func TestUndoingAnAddedWaypointEventTakesItBack(t *testing.T) {
	lvl := waypointEditorLevel(t)
	// Undo rebuilds the level, which resolves the boxes' textures by name.
	withSpawnMarkerTexture(t)
	placeWaypointAt(mgl32.Vec3{0, 1, 0})

	addWaypointEvent(activeWaypoint(), "lift_top")
	EditorUndo()
	if got := lvl.WaypointAnimations[0].Points[0].Events; got != nil {
		t.Errorf("undo left the point posting %v", got)
	}
	if activeWaypoint() == nil {
		t.Error("undo dropped the point from the selection")
	}
}
