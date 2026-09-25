package editor

// editor_3d_pick_cycle_test.go — repeated clicks reach what is behind.

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// stackedBoxLevel installs two boxes on the -Z axis, one at z=-3 and one at
// z=-10, so a ray down -Z from the origin crosses both: the near one twice (its
// front and back faces) and then the far one twice.
func stackedBoxLevel(t *testing.T) {
	t.Helper()
	brushGeomLevel(t,
		world.NewBoxBrush("near", mgl32.Vec3{0, 0, -3}, mgl32.Vec3{2, 2, 1}, "concrete"),
		world.NewBoxBrush("far", mgl32.Vec3{0, 0, -10}, mgl32.Vec3{2, 2, 1}, "concrete"),
	)
}

// TestPick3DCandidatesAreOrderedNearToFar is the property everything else here
// rests on: the picker reports every hit along the ray, nearest first, and
// pick3D is still its first entry.
func TestPick3DCandidatesAreOrderedNearToFar(t *testing.T) {
	defer saveEditorState()()
	mx, my := setupPickCamera()
	stackedBoxLevel(t)

	cands := pick3DCandidates(mx, my)
	if len(cands) < 4 {
		t.Fatalf("a ray through two boxes found %d hits, want at least 4 (two faces each)", len(cands))
	}
	for i := 1; i < len(cands); i++ {
		if cands[i].t < cands[i-1].t {
			t.Fatalf("hit %d is at %v, nearer than hit %d at %v — the list is not sorted",
				i, cands[i].t, i-1, cands[i-1].t)
		}
	}
	if cands[0].brushIdx != 0 {
		t.Errorf("the first hit is on brush %d, want the near one, 0", cands[0].brushIdx)
	}
	if got := pick3D(mx, my); got != cands[0] {
		t.Errorf("pick3D returned %+v, want the first candidate %+v", got, cands[0])
	}
}

// TestObjectClicksCycleThroughOverlappingBrushes is the whole point: click the
// near box, click again without moving, and the box behind it is selected.
func TestObjectClicksCycleThroughOverlappingBrushes(t *testing.T) {
	defer saveEditorState()()
	mx, my := setupPickCamera()
	stackedBoxLevel(t)
	Editor.EditContext = editorObjectMode

	want := []int{0, 1, 0}
	for i, brushIdx := range want {
		editor3DObjectClick(mx, my, false)
		if len(Editor.SelectedBrushes) != 1 {
			t.Fatalf("click %d selected %d brushes, want exactly 1", i+1, len(Editor.SelectedBrushes))
		}
		if got := Editor.SelectedBrushes[0].brushIdx; got != brushIdx {
			t.Fatalf("click %d selected brush %d, want %d (the cycle is near, far, back to near)",
				i+1, got, brushIdx)
		}
	}
}

// A box's near and far faces are both under the cursor, so face mode cycles
// between them — object mode must not, because both are the same solid and the
// selection would appear not to change at all.
func TestObjectClicksDoNotCycleThroughOneBrushesOwnFaces(t *testing.T) {
	defer saveEditorState()()
	mx, my := setupPickCamera()
	brushGeomLevel(t, world.NewBoxBrush("only", mgl32.Vec3{0, 0, -3}, mgl32.Vec3{2, 2, 1}, "concrete"))
	Editor.EditContext = editorObjectMode

	cands := pickObjectCandidates(mx, my)
	if len(cands) != 1 {
		t.Fatalf("one box gave object mode %d candidates, want 1 — its faces should collapse to the solid", len(cands))
	}

	editor3DObjectClick(mx, my, false)
	editor3DObjectClick(mx, my, false)
	if len(Editor.SelectedBrushes) != 1 || Editor.SelectedBrushes[0].brushIdx != 0 {
		t.Fatalf("clicking the only brush twice left %+v selected, want it still selected",
			Editor.SelectedBrushes)
	}
}

// TestFaceClicksCycleThroughTheFacesOnTheRay covers the other context: in face
// mode the near and far faces of one box are two different things, and the
// second click takes the far one.
func TestFaceClicksCycleThroughTheFacesOnTheRay(t *testing.T) {
	defer saveEditorState()()
	mx, my := setupPickCamera()
	brushGeomLevel(t, world.NewBoxBrush("only", mgl32.Vec3{0, 0, -3}, mgl32.Vec3{2, 2, 1}, "concrete"))
	Editor.EditContext = editorEditMode
	Editor.SelMode = editorSelFace

	first := pickFaceClick(mx, my, false)
	if first.kind != pickBrushFace {
		t.Fatalf("the first click landed on %q, want a brush face", first.kind)
	}
	f, _ := faceSelFromHit(first)
	editorSelectFace(f, false)

	second := pickFaceClick(mx, my, false)
	if second.kind != pickBrushFace {
		t.Fatalf("the second click landed on %q, want a brush face", second.kind)
	}
	if second.faceIdx == first.faceIdx {
		t.Fatalf("both clicks landed on face %d; the second should have gone past it", first.faceIdx)
	}
}

// Ctrl+click builds a multi-selection, so it must keep taking the nearest hit:
// every face it adds is selected by the time the next one lands, and a cycling
// ctrl+click would walk off down the ray instead of adding the face the author
// was pointing at.
func TestCtrlClickDoesNotCycle(t *testing.T) {
	defer saveEditorState()()
	mx, my := setupPickCamera()
	brushGeomLevel(t, world.NewBoxBrush("only", mgl32.Vec3{0, 0, -3}, mgl32.Vec3{2, 2, 1}, "concrete"))
	Editor.EditContext = editorEditMode
	Editor.SelMode = editorSelFace

	nearest := pickFaceClick(mx, my, true)
	f, ok := faceSelFromHit(nearest)
	if !ok {
		t.Fatalf("ctrl+click landed on %q, want a brush face", nearest.kind)
	}
	editorSelectFace(f, true)

	if again := pickFaceClick(mx, my, true); again != nearest {
		t.Errorf("a second ctrl+click on the same pixel gave %+v, want the same nearest hit %+v",
			again, nearest)
	}
}

// The cycle anchors on the selection rather than on a remembered index, so
// selecting something else and clicking again starts over at the nearest hit.
func TestTheCycleRestartsWhenTheSelectionChanges(t *testing.T) {
	defer saveEditorState()()
	mx, my := setupPickCamera()
	stackedBoxLevel(t)
	Editor.EditContext = editorObjectMode

	editor3DObjectClick(mx, my, false)
	editor3DObjectClick(mx, my, false)
	if Editor.SelectedBrushes[0].brushIdx != 1 {
		t.Fatalf("two clicks left brush %d selected, want the far one", Editor.SelectedBrushes[0].brushIdx)
	}

	// Something else entirely — as the Explorer's rows do.
	editor3DClearSelection()
	editor3DObjectClick(mx, my, false)
	if got := Editor.SelectedBrushes[0].brushIdx; got != 0 {
		t.Errorf("after the selection was dropped a click took brush %d, want the nearest, 0", got)
	}
}
