package main

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// entityEditorLevel points the editor at a level holding two entities and
// selects the first. Nothing here uploads geometry — that needs a GL context —
// so the entities carry bounds and transforms but no models.
func entityEditorLevel(t *testing.T) *Level {
	t.Helper()
	lvl := &Level{Entities: []Entity{
		{ID: "a", Type: "crate", Position: mgl32.Vec3{1, 0, 1}, Scale: mgl32.Vec3{1, 1, 1},
			BoundHalfX: 0.5, BoundHalfY: 0.5, BoundHalfZ: 0.5, Visible: true},
		{ID: "b", Type: "crate", Position: mgl32.Vec3{5, 0, 5}, Scale: mgl32.Vec3{1, 1, 1},
			BoundHalfX: 0.5, BoundHalfY: 0.5, BoundHalfZ: 0.5, Visible: true},
	}}
	Editor.level = lvl
	Editor.Mode = Mode3D
	Editor.SelectedVertices = nil
	Editor.SpawnSelected = false
	Editor.GizmoKind = gizmoKindNone
	Editor.SnapToGrid = false
	editorSelectEntity(0)
	t.Cleanup(func() {
		Editor.level = nil
		editorClearEntitySelection()
		Editor.Mode = ModeGame
	})
	return lvl
}

func TestEditorSelectEntityDrivesGizmoAndPanel(t *testing.T) {
	entityEditorLevel(t)

	if Editor.GizmoKind != gizmoKindEntity {
		t.Errorf("gizmo kind: got %q, want %q", Editor.GizmoKind, gizmoKindEntity)
	}
	if Editor.PanelFocus != panelFocusEntity {
		t.Errorf("panel focus: got %q", Editor.PanelFocus)
	}
	if got := activeGizmoEntity(); got == nil || got.ID != "a" {
		t.Errorf("activeGizmoEntity: got %+v", got)
	}

	// Clearing takes the gizmo with it, so a drag cannot keep moving something
	// that is no longer selected.
	editorClearEntitySelection()
	if Editor.GizmoKind != gizmoKindNone || activeEntity() != nil {
		t.Errorf("selection survived clearing: kind=%q entity=%+v", Editor.GizmoKind, activeEntity())
	}
}

func TestActiveEntityIndexIsBoundsChecked(t *testing.T) {
	// Undo swaps the whole level out, which is why the selection is an index
	// and every accessor has to tolerate a stale one.
	lvl := entityEditorLevel(t)
	Editor.ActiveEntityIdx = 99
	if got := activeEntity(); got != nil {
		t.Errorf("stale index resolved to %+v", got)
	}
	Editor.ActiveEntityIdx = len(lvl.Entities) - 1
	if got := activeEntity(); got == nil || got.ID != "b" {
		t.Errorf("got %+v, want b", got)
	}
}

func TestGizmoRotateTurnsTheEntityInPlace(t *testing.T) {
	// A vertex selection rotates by moving its points; an entity has an
	// orientation of its own, so it turns and stays put.
	lvl := entityEditorLevel(t)
	e := &lvl.Entities[0]
	start := e.Position

	Editor.GizmoDragStartRot = e.Rotation
	applyRotationToSelection(mgl32.Vec3{0, 1, 0}, e.Position, 0.5)

	if e.Position != start {
		t.Errorf("entity moved while rotating: %v", e.Position)
	}
	if abs32(e.Rotation.Y()-0.5) > 1e-5 {
		t.Errorf("rotation: got %v, want Y=0.5", e.Rotation)
	}
	// A drag about one world axis must not leak into the other two channels —
	// the panel's numbers are what the author reads back.
	if e.Rotation.X() != 0 || e.Rotation.Z() != 0 {
		t.Errorf("rotation leaked across axes: %v", e.Rotation)
	}
}

func TestEulerRotatedAboutIsPerAxis(t *testing.T) {
	start := mgl32.Vec3{0.1, 0.2, 0.3}
	if got := eulerRotatedAbout(start, mgl32.Vec3{1, 0, 0}, 0.5); abs32(got.X()-0.6) > 1e-5 ||
		got.Y() != start.Y() || got.Z() != start.Z() {
		t.Errorf("X axis: got %v", got)
	}
	if got := eulerRotatedAbout(start, mgl32.Vec3{0, 0, 1}, 0.5); abs32(got.Z()-0.8) > 1e-5 {
		t.Errorf("Z axis: got %v", got)
	}
	// Wrapped, so a long drag cannot run the angle away.
	got := eulerRotatedAbout(mgl32.Vec3{0, 3, 0}, mgl32.Vec3{0, 1, 0}, 1)
	if got.Y() < -math.Pi || got.Y() > math.Pi {
		t.Errorf("angle escaped [-pi, pi]: %v", got.Y())
	}
}

func TestGizmoScaleResizesTheEntityInPlace(t *testing.T) {
	lvl := entityEditorLevel(t)
	e := &lvl.Entities[0]
	start := e.Position

	Editor.GizmoDragStartScale = e.effectiveScale()
	applyScaleToSelection(e.Position, mgl32.Vec3{2, 1, 1})

	if e.Position != start {
		t.Errorf("entity moved while scaling: %v", e.Position)
	}
	if !vecNear(e.Scale, mgl32.Vec3{2, 1, 1}, 1e-5) {
		t.Errorf("scale: got %v, want {2 1 1}", e.Scale)
	}
	// Scaling the entity scales its bounds with it.
	if got := e.BoundHalfExtents(); !vecNear(got, mgl32.Vec3{1, 0.5, 0.5}, 1e-5) {
		t.Errorf("bounds: got %v", got)
	}
}

func TestEntityScaleClampKeepsSignAndAvoidsZero(t *testing.T) {
	// A zero scale makes the model matrix singular, which the picker inverts.
	// A negative one legitimately mirrors a model, so the sign is kept.
	if got := entityScaleClamp(0); got != entityScaleMin {
		t.Errorf("got %v, want %v", got, entityScaleMin)
	}
	if got := entityScaleClamp(-0.0000001); got != -entityScaleMin {
		t.Errorf("got %v, want %v", got, -entityScaleMin)
	}
	if got := entityScaleClamp(-3); got != -3 {
		t.Errorf("a legitimate mirror was clamped: %v", got)
	}
}

func TestGizmo2DRotateMatchesTheMapsAngleSense(t *testing.T) {
	// The map measures angles from +X through +Z; a Y Euler rotation carries +X
	// toward -Z. So a positive map delta is a negative yaw, and the arrow drawn
	// on the map has to follow the cursor rather than run away from it.
	lvl := entityEditorLevel(t)
	e := &lvl.Entities[0]
	Editor.Gizmo2DStartEntityRot = mgl32.Vec3{}

	gizmo2DRotateEntity(e, math.Pi/2)

	// Forward (+X local) should now point along +Z in world space, which is the
	// direction the cursor was dragged toward.
	d := eulerMatrixYXZ(e.Rotation).Mul4x1(mgl32.Vec4{1, 0, 0, 0})
	if !vecNear(mgl32.Vec3{d.X(), d.Y(), d.Z()}, mgl32.Vec3{0, 0, 1}, 1e-5) {
		t.Errorf("forward: got {%v %v %v}, want {0 0 1}", d.X(), d.Y(), d.Z())
	}
}

func TestGizmo2DScaleLeavesHeightAlone(t *testing.T) {
	// The map cannot express height, so a drag up here must not silently
	// flatten what it is scaling.
	lvl := entityEditorLevel(t)
	e := &lvl.Entities[0]
	Editor.Gizmo2DStartEntityScale = mgl32.Vec3{1, 3, 1}

	gizmo2DScaleEntity(e, mgl32.Vec2{2, 4})

	if !vecNear(e.Scale, mgl32.Vec3{2, 3, 4}, 1e-5) {
		t.Errorf("got %v, want {2 3 4}", e.Scale)
	}
}

func TestEditorToolActsOnEntitySelection(t *testing.T) {
	// Rotate and scale need something with an orientation or a size. An entity
	// has both; a spawn marker has neither.
	entityEditorLevel(t)
	Editor.GizmoTool = editorToolRotate
	if !editorToolActsOnSelection() {
		t.Error("rotate refused an entity selection")
	}
	editorClearEntitySelection()
	if editorToolActsOnSelection() {
		t.Error("rotate accepted an empty selection")
	}
	Editor.GizmoTool = editorToolMove
}

func TestDeleteActiveEntityRemovesAndDeselects(t *testing.T) {
	lvl := entityEditorLevel(t)

	deleteActiveEntity()

	if len(lvl.Entities) != 1 || lvl.Entities[0].ID != "b" {
		t.Fatalf("entities after delete: %v", levelEntityIDs(lvl))
	}
	if activeEntity() != nil || Editor.GizmoKind != gizmoKindNone {
		t.Errorf("selection survived the delete: idx=%d kind=%q", Editor.ActiveEntityIdx, Editor.GizmoKind)
	}
}

func TestDuplicateActiveEntityGivesItItsOwnIdentity(t *testing.T) {
	lvl := entityEditorLevel(t)
	lvl.Entities[0].Tags = []string{"prop"}
	lvl.Entities[0].Attributes = AttrSet{"health": IntAttr(5)}

	duplicateActiveEntity()

	if len(lvl.Entities) != 3 {
		t.Fatalf("got %d entities, want 3: %v", len(lvl.Entities), levelEntityIDs(lvl))
	}
	dup := &lvl.Entities[2]
	if dup.ID == "a" {
		t.Error("the duplicate reused the original's ID")
	}
	// Coincident copies cannot be told apart, let alone clicked apart.
	if dup.Position == lvl.Entities[0].Position {
		t.Error("the duplicate landed exactly on the original")
	}
	// Editing the copy must not reach the original.
	dup.SetAttr("health", IntAttr(99))
	dup.AddTag("copy")
	if lvl.Entities[0].Attributes["health"] != IntAttr(5) {
		t.Error("the duplicate shares its attribute set with the original")
	}
	if lvl.Entities[0].HasTag("copy") {
		t.Error("the duplicate shares its tag list with the original")
	}
	// The copy is the new selection.
	if got := activeEntity(); got == nil || got.ID != dup.ID {
		t.Errorf("selection: got %+v, want the duplicate", got)
	}
}

func levelEntityIDs(lvl *Level) []string {
	out := make([]string, len(lvl.Entities))
	for i := range lvl.Entities {
		out[i] = lvl.Entities[i].ID
	}
	return out
}

func TestEditor2DEntityPickTakesTheTopmost(t *testing.T) {
	// Later entities win, so the one just dropped onto a pile is the one a
	// click grabs.
	lvl := &Level{Entities: []Entity{
		{ID: "under", Position: mgl32.Vec3{0, 0, 0}, BoundHalfX: 1, BoundHalfY: 1, BoundHalfZ: 1},
		{ID: "over", Position: mgl32.Vec3{0, 0, 0}, BoundHalfX: 1, BoundHalfY: 1, BoundHalfZ: 1},
	}}
	Editor.level = lvl
	Editor.Zoom2D = 40
	t.Cleanup(func() { Editor.level = nil })

	idx, ok := editor2DEntityAt(lvl, 0, 0)
	if !ok || lvl.Entities[idx].ID != "over" {
		t.Errorf("got idx=%d ok=%v, want the topmost", idx, ok)
	}
	if _, ok := editor2DEntityAt(lvl, 50, 50); ok {
		t.Error("a click far from every entity hit one")
	}
}
