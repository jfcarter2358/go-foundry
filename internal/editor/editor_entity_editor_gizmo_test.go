package editor

// editor_entity_editor_gizmo_test.go — the entity editor's gizmos, exercised
// without a window.
//
// What is testable here is the half that has no camera in it: which channels a
// selection exposes, and what a drag of a given size writes to them. The
// picking and the drawing need a viewport and a projection, and are kept out of
// these functions for that reason — the split editor_entity_editor_test.go
// documents.

import (
	"foundry/internal/testenv"
	"foundry/internal/world"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// entityGizmoTestDrag installs a document with one of each kind of part in it
// and restores the drag state afterwards.
func entityGizmoTestDrag(t *testing.T) *world.EntityDef {
	t.Helper()
	def := entityTestDoc(t)

	// The gizmo reads ws() for snapping and for what geometry is selected, so
	// the tests have to install this window's workspace exactly as the window
	// does — otherwise they would be asking the level editor's questions.
	t.Cleanup(entityUseWorkspace())

	prevGizmo, prevTool, prevSnap := entityGizmo, entityGizmoTool, entityWS.SnapToGrid
	t.Cleanup(func() {
		entityGizmo, entityGizmoTool, entityWS.SnapToGrid = prevGizmo, prevTool, prevSnap
	})
	entityGizmo = entityGizmoDrag{}
	// Off by default: a drag of a third of a unit landing on a grid line would
	// hide every arithmetic mistake these tests are here to catch.
	entityWS.SnapToGrid = false
	entityClearGeometrySelection()
	t.Cleanup(entityClearGeometrySelection)

	def.Models = []world.EntityModel{{Name: "hull", Visible: true}}
	def.Vehicle = &world.VehicleSpec{Seats: []world.VehicleSeat{{Name: "pilot"}}}
	return def
}

func TestEntityGizmoTargetPerKind(t *testing.T) {
	entityGizmoTestDrag(t)

	cases := []struct {
		kind                 entitySelKind
		wantEuler, wantYaw   bool
		wantAxisScale, wantU bool
	}{
		{entitySelModel, true, false, true, false},
		// A seat is a point and a facing. There is nothing to scale: its extent
		// is the occupant's, not its own.
		{entitySelSeat, false, true, false, false},
	}
	for _, c := range cases {
		entitySel = entityEditorSelection{Kind: c.kind}
		tgt, ok := entityGizmoTargetOf()
		if !ok {
			t.Fatalf("kind %v resolved to nothing", c.kind)
		}
		if tgt.Pos == nil {
			t.Errorf("kind %v has no position to move", c.kind)
		}
		if (tgt.Rot != nil) != c.wantEuler || (tgt.RotY != nil) != c.wantYaw {
			t.Errorf("kind %v rotation channels: euler=%v yaw=%v", c.kind, tgt.Rot != nil, tgt.RotY != nil)
		}
		if (tgt.Scale != nil) != c.wantAxisScale || (tgt.ScaleU != nil) != c.wantU {
			t.Errorf("kind %v scale channels: axis=%v uniform=%v", c.kind, tgt.Scale != nil, tgt.ScaleU != nil)
		}
	}

	// A selection pointing past the end of a list resolves to nothing rather
	// than reading off it — an undo can shorten the document under the panel.
	entitySel = entityEditorSelection{Kind: entitySelModel, Idx: 7}
	if _, ok := entityGizmoTargetOf(); ok {
		t.Error("a stale selection index still resolved to a target")
	}
}

// TestEntityGizmoOffersOnlyRealChannels pins the rule the whole file is built
// on: a tool with nothing to write shows no handles, rather than offering a
// drag that would silently do nothing.
func TestEntityGizmoOffersOnlyRealChannels(t *testing.T) {
	entityGizmoTestDrag(t)

	entitySel = entityEditorSelection{Kind: entitySelSeat}
	if scales, _ := entityGizmoScales(); scales {
		t.Error("a seat offered a scale gizmo")
	}
	if rotates, yOnly := entityGizmoRotates(); !rotates || !yOnly {
		t.Errorf("a seat's rotation should be Y-only: rotates=%v yOnly=%v", rotates, yOnly)
	}

	entitySel = entityEditorSelection{Kind: entitySelModel}
	if _, yOnly := entityGizmoRotates(); yOnly {
		t.Error("a model attachment has a full Euler rotation")
	}
	if _, uniform := entityGizmoScales(); uniform {
		t.Error("a model attachment scales per axis")
	}
}

func TestEntityGizmoMoveWritesTheOffset(t *testing.T) {
	def := entityGizmoTestDrag(t)
	entitySel = entityEditorSelection{Kind: entitySelModel}

	entityGizmo.StartPos = mgl32.Vec3{1, 2, 3}
	entityGizmoApplyMove(2, 0.75)
	if got, want := def.Models[0].Offset, (mgl32.Vec3{1, 2, 3.75}); got != want {
		t.Errorf("Z drag put the attachment at %v, want %v", got, want)
	}
	// Absolute from the snapshot, never accumulated: a second event at the same
	// cursor position lands in the same place rather than moving twice.
	entityGizmoApplyMove(2, 0.75)
	if got, want := def.Models[0].Offset, (mgl32.Vec3{1, 2, 3.75}); got != want {
		t.Errorf("the drag accumulated: %v, want %v", got, want)
	}
	if !entityDefDirty {
		t.Error("a gizmo drag left the document clean")
	}

	// Snapping rounds the resulting coordinate, not the delta, so a drag lands
	// on absolute grid multiples.
	entityWS.SnapToGrid, entityWS.StepSize = true, 0.5
	entityGizmoApplyMove(2, 0.4)
	if got, want := def.Models[0].Offset.Z(), float32(3.5); got != want {
		t.Errorf("snapped drag landed on %v, want %v", got, want)
	}
}

func TestEntityGizmoRotateWritesEachKind(t *testing.T) {
	def := entityGizmoTestDrag(t)
	quarter := float32(math.Pi / 2)

	// A model attachment turns on one Euler channel, and only that one: the
	// three channels are the world axes here, so an X drag must not leak into
	// Y or Z.
	entitySel = entityEditorSelection{Kind: entitySelModel}
	entityGizmo.StartRot = mgl32.Vec3{0, 0.25, 0}
	entityGizmoApplyRotate(0, quarter)
	if got := def.Models[0].Rotation; !nearly(got.X(), quarter) || got.Y() != 0.25 || got.Z() != 0 {
		t.Errorf("X ring wrote %v, want only X changed from {0 0.25 0}", got)
	}

	// A seat carries a single angle, and only the Y ring is offered for it — an
	// X drag is a stale axis and writes nothing.
	entitySel = entityEditorSelection{Kind: entitySelSeat}
	entityGizmo.StartRot = mgl32.Vec3{}
	entityGizmoApplyRotate(1, quarter)
	if got := def.Vehicle.Seats[0].Yaw; !nearly(got, quarter) {
		t.Errorf("Y ring turned the seat to %v, want %v", got, quarter)
	}
}

func TestEntityGizmoScaleWritesEachKind(t *testing.T) {
	def := entityGizmoTestDrag(t)

	// Per axis for an attachment: only the dragged one changes.
	entitySel = entityEditorSelection{Kind: entitySelModel}
	entityGizmo.StartScale = mgl32.Vec3{1, 1, 1}
	entityGizmoApplyScale(1, 2)
	if got, want := def.Models[0].Scale, (mgl32.Vec3{1, 2, 1}); got != want {
		t.Errorf("Y handle wrote %v, want %v", got, want)
	}
	// The centre handle drives all three.
	entityGizmoApplyScale(modelScaleUniformAxis, 3)
	if got, want := def.Models[0].Scale, (mgl32.Vec3{3, 3, 3}); got != want {
		t.Errorf("uniform handle wrote %v, want %v", got, want)
	}

	// A seat has no size at all, and a drag on one writes nothing.
	entitySel = entityEditorSelection{Kind: entitySelSeat}
	before := def.Vehicle.Seats[0]
	entityGizmoApplyScale(modelScaleUniformAxis, 2)
	if def.Vehicle.Seats[0] != before {
		t.Error("a scale drag changed a seat, which has nothing to scale")
	}
}

// TestEntityGizmoScaleStartsFromOne pins the zero-vector rule: an attachment
// that has never been scaled reads as unscaled, not as collapsed to nothing.
func TestEntityGizmoScaleStartsFromOne(t *testing.T) {
	def := entityGizmoTestDrag(t)
	entitySel = entityEditorSelection{Kind: entitySelModel}
	def.Models[0].Scale = mgl32.Vec3{}

	entityGizmoBeginDrag(editorToolScale, modelScaleUniformAxis, 10, mgl32.Vec3{})
	if got, want := entityGizmo.StartScale, (mgl32.Vec3{1, 1, 1}); got != want {
		t.Fatalf("captured %v as the starting scale, want %v", got, want)
	}
	entityGizmoApplyScale(modelScaleUniformAxis, 2)
	if got, want := def.Models[0].Scale, (mgl32.Vec3{2, 2, 2}); got != want {
		t.Errorf("doubling an unscaled attachment gave %v, want %v", got, want)
	}
}

// TestEntityGizmoDragPushesOneUndoEntry pins the rule every gizmo in the editor
// follows: one entry for the whole drag, not one per motion event.
func TestEntityGizmoDragPushesOneUndoEntry(t *testing.T) {
	def := entityGizmoTestDrag(t)
	entitySel = entityEditorSelection{Kind: entitySelModel}
	EntityDefClearUndoHistory()

	entityGizmoBeginDrag(editorToolMove, 0, 0, mgl32.Vec3{})
	for i := 1; i <= 5; i++ {
		entityGizmoApplyMove(0, float32(i))
	}
	entityGizmoMouseUp()
	if n := entityUndoStack.len(); n != 1 {
		t.Errorf("a five-event drag pushed %d undo entries, want 1", n)
	}
	if def.Models[0].Offset.X() != 5 {
		t.Fatalf("the drag ended at %v", def.Models[0].Offset)
	}
	EntityDefUndo()
	if entityDef.Models[0].Offset.X() != 0 {
		t.Errorf("undo left the attachment at %v, want it back at the origin", entityDef.Models[0].Offset)
	}
}

// TestEntitySetGizmoToolEndsTheDrag pins that switching tools mid-drag cannot
// leave a half-finished translation to be completed as a rotation.
func TestEntitySetGizmoToolEndsTheDrag(t *testing.T) {
	entityGizmoTestDrag(t)
	entitySel = entityEditorSelection{Kind: entitySelModel}
	entityGizmoTool = editorToolMove

	entityGizmoBeginDrag(editorToolMove, 0, 0, mgl32.Vec3{})
	if !entityGizmoDragging() {
		t.Fatal("the drag never started")
	}
	entitySetGizmoTool(editorToolRotate)
	if entityGizmoDragging() {
		t.Error("switching tools left the drag running")
	}
}

func nearly(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-4 }

// ---------- geometry ----------
//
// A brush selection has no transform to write: the shape IS the corner list, so
// the gizmo resolves the selection down to the handles underneath it and moves
// those. These pin that the geometry branch is taken at all, and that it uses
// the same absolute-from-snapshot rule the transform branch does.

// entityGizmoTestGeometry installs a document with one box brush in it and
// selects that brush in object mode.
func entityGizmoTestGeometry(t *testing.T) *world.Brush {
	t.Helper()
	// The write-back reloads whatever it touched, and a reload is an upload.
	testenv.WithoutGeometryUploads(t)
	entityGizmoTestDrag(t)

	ch := world.Chunk{ID: entityGeomChunkID, Scale: 1}
	ch.Brushes = []world.Brush{
		world.NewBoxBrush("box", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{1, 1, 1}, "concrete"),
	}
	ch.RecomputeBounds()
	prev := entityGeomLevel
	entityGeomLevel = &world.Level{Chunks: []world.Chunk{ch}}
	_ = world.RebuildIndexes(entityGeomLevel)
	entityWS.level = entityGeomLevel
	entityWS.ActiveChunkIdx = 0
	t.Cleanup(func() { entityGeomLevel = prev; entityWS.level = prev })

	setBrushSelection([]brushSel{{chunkIdx: 0, brushIdx: 0}})
	return &entityGeomLevel.Chunks[0].Brushes[0]
}

func TestEntityGizmoMovesABrushByItsCorners(t *testing.T) {
	b := entityGizmoTestGeometry(t)
	if !entityGeometrySelected() {
		t.Fatal("a selected brush offered the gizmo nothing to drag")
	}
	before := append([]mgl32.Vec3(nil), b.Verts...)

	entityGizmoBeginDrag(editorToolMove, 0, 0, mgl32.Vec3{})
	if entityGizmo.StartWorld == nil {
		t.Fatal("the drag did not snapshot the corners; it is writing a transform instead")
	}
	entityGizmoApplyMove(0, 2)
	for i := range b.Verts {
		if got, want := b.Verts[i].X(), before[i].X()+2; !nearly(got, want) {
			t.Fatalf("corner %d moved to x=%v, want %v", i, got, want)
		}
		if b.Verts[i].Y() != before[i].Y() || b.Verts[i].Z() != before[i].Z() {
			t.Fatalf("an X drag moved corner %d off its own Y/Z", i)
		}
	}
	// Absolute from the snapshot: the same event twice lands in the same place.
	entityGizmoApplyMove(0, 2)
	if got, want := b.Verts[0].X(), before[0].X()+2; !nearly(got, want) {
		t.Errorf("the drag accumulated: x=%v, want %v", got, want)
	}
}

func TestEntityGizmoScalesABrushAboutTheDragOrigin(t *testing.T) {
	b := entityGizmoTestGeometry(t)
	center, ok := entityGizmoOrigin()
	if !ok {
		t.Fatal("a brush selection has no gizmo origin")
	}
	before := append([]mgl32.Vec3(nil), b.Verts...)

	entityGizmoBeginDrag(editorToolScale, modelScaleUniformAxis, 10, center)
	entityGizmoApplyScale(modelScaleUniformAxis, 2)
	for i := range b.Verts {
		want := before[i].Sub(center).Mul(2).Add(center)
		if !nearly(b.Verts[i].X(), want.X()) || !nearly(b.Verts[i].Y(), want.Y()) ||
			!nearly(b.Verts[i].Z(), want.Z()) {
			t.Fatalf("corner %d scaled to %v, want %v", i, b.Verts[i], want)
		}
	}
}

// TestEntityGizmoGeometryBeatsTheOutlineSelection pins the precedence: with a
// brush picked in the viewport the gizmo drags geometry, whatever row the
// outline happens to have highlighted. Otherwise clicking a brush and then
// dragging would silently move the last-selected model attachment.
func TestEntityGizmoGeometryBeatsTheOutlineSelection(t *testing.T) {
	b := entityGizmoTestGeometry(t)
	entitySel = entityEditorSelection{Kind: entitySelModel}
	before := b.Verts[0].Y()

	entityGizmoBeginDrag(editorToolMove, 1, 0, mgl32.Vec3{})
	entityGizmoApplyMove(1, 1)
	if entityDef.Models[0].Offset != (mgl32.Vec3{}) {
		t.Errorf("the drag wrote the model attachment's offset: %v", entityDef.Models[0].Offset)
	}
	if got, want := b.Verts[0].Y(), before+1; !nearly(got, want) {
		t.Errorf("the drag moved the brush's corner to y=%v, want %v", got, want)
	}
}
