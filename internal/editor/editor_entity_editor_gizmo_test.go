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

	prevGizmo, prevTool, prevSnap := entityGizmo, entityGizmoTool, Editor.SnapToGrid
	t.Cleanup(func() {
		entityGizmo, entityGizmoTool, Editor.SnapToGrid = prevGizmo, prevTool, prevSnap
	})
	entityGizmo = entityGizmoDrag{}
	// Off by default: snapping is the level editor's global, and a drag of a
	// third of a unit landing on a grid line would hide every arithmetic
	// mistake these tests are here to catch.
	Editor.SnapToGrid = false

	def.Models = []world.EntityModel{{Name: "hull", Visible: true}}
	def.Chunks = []world.EntityChunk{{Name: "hold"}}
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
		{entitySelChunk, false, true, false, true},
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

	entitySel = entityEditorSelection{Kind: entitySelChunk}
	if _, yOnly := entityGizmoRotates(); !yOnly {
		t.Error("a chunk rotates about Y only — its placement has no other angle")
	}
	if scales, uniform := entityGizmoScales(); !scales || !uniform {
		t.Errorf("a chunk scales uniformly only: scales=%v uniform=%v", scales, uniform)
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
	Editor.SnapToGrid, Editor.StepSize = true, 0.5
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

	// A chunk and a seat carry a single angle, and only the Y ring is offered
	// for them — an X drag is a stale axis and writes nothing.
	entitySel = entityEditorSelection{Kind: entitySelChunk}
	entityGizmo.StartRot = mgl32.Vec3{0, 0.25, 0}
	entityGizmoApplyRotate(0, quarter)
	if def.Chunks[0].Rotation != 0 {
		t.Errorf("an X drag turned a chunk to %v; only Y is offered", def.Chunks[0].Rotation)
	}
	entityGizmoApplyRotate(1, quarter)
	if got := def.Chunks[0].Rotation; !nearly(got, 0.25+quarter) {
		t.Errorf("Y ring turned the chunk to %v, want %v", got, 0.25+quarter)
	}

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

	// A chunk has one factor, and only the centre handle is offered.
	entitySel = entityEditorSelection{Kind: entitySelChunk}
	entityGizmo.StartScale = mgl32.Vec3{2, 2, 2}
	entityGizmoApplyScale(modelScaleUniformAxis, 1.5)
	if got := def.Chunks[0].Scale; !nearly(got, 3) {
		t.Errorf("uniform handle scaled the chunk to %v, want 3", got)
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
