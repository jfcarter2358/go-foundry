package editor

// editor_entity_editor_waypoints_test.go — a type's paths in the entity editor,
// headless.

import (
	"math"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// entityWaypointTestDoc is entityTestDoc with the path preview reset and a
// scratch level holding one dynamic brush, "hatch", centred on {2 1 0}. No GL:
// the brush is never uploaded, which is all the preview needs of it.
func entityWaypointTestDoc(t *testing.T) *world.EntityDef {
	t.Helper()
	def := entityTestDoc(t)
	prevLevel, prevWSLevel := entityGeomLevel, entityWS.level
	prevGizmo := entityGizmo
	t.Cleanup(func() {
		entityGeomLevel, entityWS.level = prevLevel, prevWSLevel
		entityWaypointPreview = entityWaypointPreviewState{}
		entityGizmo = prevGizmo
	})
	hatch := world.NewBoxBrush("hatch", mgl32.Vec3{2, 1, 0}, mgl32.Vec3{0.1, 1, 1}, "")
	hatch.Dynamic = true
	entityGeomLevel = &world.Level{Chunks: []world.Chunk{{ID: entityGeomChunkID, Scale: 1, Brushes: []world.Brush{hatch}}}}
	entityWS.level = entityGeomLevel
	entityWaypointPreview = entityWaypointPreviewState{}
	return def
}

func TestAddingEditingAndUndoingATypesPath(t *testing.T) {
	entityWaypointTestDoc(t)

	entityAddWaypointAnimation()
	if len(entityDef.WaypointAnimations) != 1 || entitySel.Kind != entitySelWaypoints || entitySel.Sub != 0 {
		t.Fatalf("adding a path left %+v selected over %+v; want its first point", entitySel, entityDef.WaypointAnimations)
	}
	if !entityDefDirty {
		t.Error("adding a path did not mark the type changed")
	}

	entityAddWaypointAhead()
	a := entityActiveWaypointAnimation()
	if len(a.Points) != 2 || entitySel.Sub != 1 {
		t.Fatalf("Add Point made %d points with %d selected; want 2 with the new one selected", len(a.Points), entitySel.Sub)
	}
	if a.Points[1].Time <= a.Points[0].Time {
		t.Errorf("the added point is reached at %v s, not after the first's %v s", a.Points[1].Time, a.Points[0].Time)
	}

	entityDeleteActiveWaypoint()
	if n := len(entityActiveWaypointAnimation().Points); n != 1 || entitySel.Sub != 0 {
		t.Errorf("deleting the point left %d points with %d selected; want 1 with the one before it selected", n, entitySel.Sub)
	}
	EntityDefUndo()
	if a := entityActiveWaypointAnimation(); a == nil || len(a.Points) != 2 {
		t.Errorf("undo did not bring the deleted point back: %+v", entityDef.WaypointAnimations)
	}

	entityRenameWaypointAnimation(0, "door.close")
	if entityDef.WaypointAnimations[0].ID == "door.close" {
		t.Error("a path was renamed to something with a dot in it, which @self.<name> could not reach")
	}
}

func TestANewPathStartsOnThePreviewBrushAndThePreviewPutsItBack(t *testing.T) {
	entityWaypointTestDoc(t)
	ch := entityGeomChunk()
	hatch := &ch.Brushes[0]

	entityWaypointPreview.Brush = "hatch"
	entityAddWaypointAnimation()
	a := entityActiveWaypointAnimation()
	if got := a.Points[0].Position; !vecNear(got, mgl32.Vec3{2, 1, 0}, 1e-4) {
		t.Fatalf("the new path starts at %v; want the hatch's centre {2 1 0}", got)
	}
	entityInsertWaypointAt(mgl32.Vec3{-2, 1, 0})
	entitySetWaypointLength(a, 2)
	a.Points[0].Time, a.Points[1].Time = 0, 2

	entityWaypointPreviewPlay(a.ID)
	entityWaypointPreviewTick(1)
	if got := hatch.Position(); !vecNear(got, mgl32.Vec3{0, 1, 0}, 1e-4) {
		t.Errorf("a second into the preview the hatch is at %v, want {0 1 0}", got)
	}
	if !world.EntityBrushesEqual(entityDef.Brushes, entityDef.Brushes) || hatch.Offset == (mgl32.Vec3{}) {
		t.Fatal("the preview did not move the hatch through its run-time placement")
	}

	entityWaypointPreviewStop()
	if hatch.Offset != (mgl32.Vec3{}) || hatch.Waypoints != nil {
		t.Errorf("stopping the preview left the hatch offset %v, following %v", hatch.Offset, hatch.Waypoints)
	}
}

func TestAPathPointsGizmoKeepsCountingPastHalfATurn(t *testing.T) {
	entityWaypointTestDoc(t)
	entityAddWaypointAnimation()

	target, ok := entityGizmoTargetOf()
	if !ok || !target.Waypoint || target.Rot == nil || target.Scale == nil {
		t.Fatalf("a selected point offers %+v; want all three channels, marked as a waypoint", target)
	}
	entityGizmo = entityGizmoDrag{StartRot: mgl32.Vec3{0, mgl32.DegToRad(170), 0}}
	entityGizmoApplyRotate(1, mgl32.DegToRad(30))
	if got := entityActiveWaypoint().Rotation.Y(); math.Abs(float64(got-mgl32.DegToRad(200))) > 1e-4 {
		t.Errorf("turning a point from 170° by 30° gave %.1f°; want 200°, not wrapped", mgl32.RadToDeg(got))
	}
}

func TestClampingKeepsAPathButDropsAPointItNoLongerHas(t *testing.T) {
	entityWaypointTestDoc(t)
	entityAddWaypointAnimation()
	entitySel.Sub = 5
	entityClampSelection()
	if entitySel.Kind != entitySelWaypoints || entitySel.Sub != -1 {
		t.Errorf("clamping a stale point left %+v; want the path selected whole", entitySel)
	}
}
