package editor

// The 3D menu is built as data for the same reason the model editor's is: what
// a face, a vertex, the spawn point or empty space offers is a question about
// the item tree, and answering it needs neither a GL context nor an ImGui
// frame.
//
// Rows whose actions rebuild geometry (a texture change, a flag toggle) are
// checked for presence and enabled-ness only — running them would upload to a
// GL context these tests do not have.

import (
	"testing"

	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// withEditor3DMenu installs a level, puts the editor in 3D mode, and restores
// every global the menu reads or writes.
func withEditor3DMenu(t *testing.T, lvl *world.Level) {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	restore := saveEditorState()
	prevPress := editor3DRightPress
	prevUndo, prevRedo := levelUndoStack, levelRedoStack

	levelUndoStack.clear()
	levelRedoStack.clear()
	editor3DRightPress = ctxGesture{}
	Editor.Mode = Mode3D
	Editor.level = lvl
	Editor.ActiveChunkIdx = 0
	Editor.ActiveBrushIdx = -1
	Editor.ActiveFaceIdx = -1
	Editor.SpawnSelected = false
	Editor.SelectedFaces = nil
	Editor.SelectedVertices = nil
	Editor.StepSize = 0.25
	Editor.SnapToGrid = false

	t.Cleanup(func() {
		editor3DRightPress = prevPress
		levelUndoStack, levelRedoStack = prevUndo, prevRedo
		restore()
	})
}

// menuLevel is a one-chunk level with one box brush plus a spawn point, so
// every selection kind has something to resolve to.
func menuLevel() *world.Level {
	ch := world.Chunk{
		ID:      "c0",
		Brushes: []world.Brush{world.NewBoxBrush("b0", mgl32.Vec3{2, 2, 2}, mgl32.Vec3{2, 2, 2}, "concrete")},
	}
	ch.RecomputeBounds()
	return &world.Level{
		Chunks: []world.Chunk{ch},
		Spawn:  &world.SpawnPoint{Position: mgl32.Vec3{1.13, 0, 2.4}},
	}
}

// selectFace points the editor at one of the brush's faces the way a click on
// it would. It goes through setFaceSelection rather than writing the Active*
// fields, since those are derived from the face selection (editor_face_sel.go)
// and setting them by hand would leave the two disagreeing.
func selectFace(faceIdx int) {
	setFaceSelection([]faceSel{brushFaceSel(0, 0, faceIdx)})
}

// Every brush face has every field, so there are no per-kind greyed-out rows
// left to test: a sector's six kinds of face each carried a different subset,
// which is what the three tests this replaces were about.
func TestEditor3DMenuOffersTheFacesActions(t *testing.T) {
	withEditor3DMenu(t, menuLevel())
	selectFace(boxFaceNegX)

	items := editor3DContextMenuItems()
	if items[0].Label != "Face #0 — brush b0" {
		t.Fatalf("heading is %q, want the face named", items[0].Label)
	}
	for _, label := range []string{"Texture", "Extrude [E]", "Inset [I]", "Subdivide [B]",
		"Drawn", "Collision", "Smooth", "Double sided", "Hollow this brush",
		"Duplicate brush", "Select this brush's faces", "Select this brush's sides",
		"Select this brush's vertices", "Delete brush"} {
		if it := mustMenuItem(t, items, label); it.Disabled {
			t.Errorf("%q should be live on a brush face", label)
		}
	}
	for _, it := range []struct{ label string }{{"Drawn"}, {"Collision"}} {
		if !mustMenuItem(t, items, it.label).Checked {
			t.Errorf("a fresh face is drawn and solid, so %q should be ticked", it.label)
		}
	}
}

// The rows that act on the brush as a whole are meaningless across a
// multi-selection that may span two brushes, so they are greyed out there.
func TestEditor3DMenuGreysWholeBrushRowsForAMultiSelection(t *testing.T) {
	withEditor3DMenu(t, menuLevel())
	setFaceSelection([]faceSel{brushFaceSel(0, 0, boxFaceTop), brushFaceSel(0, 0, boxFaceNegX)})

	items := editor3DContextMenuItems()
	if items[0].Label != "2 faces" {
		t.Fatalf("heading is %q, want the count", items[0].Label)
	}
	for _, label := range []string{"Hollow this brush", "Duplicate brush"} {
		if !mustMenuItem(t, items, label).Disabled {
			t.Errorf("%q should be greyed out across a multi-selection", label)
		}
	}
	// The per-face rows stay live, and say how many they will reach.
	mustMenuItem(t, items, "Texture (2 faces)")
	mustMenuItem(t, items, "Collision (2 faces)")
}

func TestEditor3DMenuSpawnBlock(t *testing.T) {
	withEditor3DMenu(t, menuLevel())

	Editor.SpawnSelected = true
	items := editor3DContextMenuItems()
	if items[0].Label != "Spawn point" {
		t.Fatalf("heading is %q", items[0].Label)
	}
	for _, label := range []string{"Properties", "Face the camera", "Snap to grid", "Delete"} {
		mustMenuItem(t, items, label)
	}
	if _, ok := findMenuItem(items, "Extrude [E]"); ok {
		t.Error("face rows have no business in the spawn menu")
	}
}

func TestEditor3DMenuVertexBlock(t *testing.T) {
	withEditor3DMenu(t, menuLevel())
	Editor.SelectedVertices = []vertexSel{{chunkIdx: 0, brushIdx: 0, vertIdx: 1}}

	items := editor3DContextMenuItems()
	if items[0].Label != "Vertex #1 — brush b0" {
		t.Fatalf("heading is %q", items[0].Label)
	}
	mustMenuItem(t, items, "Snap to grid")
	mustMenuItem(t, items, "Properties")

	mustMenuItem(t, items, "Deselect").Do()
	if len(Editor.SelectedVertices) != 0 {
		t.Fatal("Deselect should have dropped the vertex selection")
	}
	// And with it gone the menu is the empty-space one.
	if got := editor3DContextMenuItems()[0].Label; got != "Nothing selected" {
		t.Fatalf("heading is %q after deselecting", got)
	}
}

// Selecting a brush's vertices takes all eight corners of a box — not one
// "ring" of four. A sector needed the ring halved because a floor handle and
// the ceiling handle above it shared a chunk point, so a selection holding both
// would have moved that point twice per drag. A brush owns each corner
// outright, so there is nothing to halve.
func TestEditor3DMenuSelectsEveryCornerOfTheBrush(t *testing.T) {
	withEditor3DMenu(t, menuLevel())
	selectFace(boxFaceBottom)

	mustMenuItem(t, editor3DContextMenuItems(), "Select this brush's vertices").Do()

	if len(Editor.SelectedVertices) != 8 {
		t.Fatalf("selected %d vertices, want the box's 8 corners", len(Editor.SelectedVertices))
	}
	seen := map[int]bool{}
	for _, sel := range Editor.SelectedVertices {
		if seen[sel.vertIdx] {
			t.Fatalf("corner %d is in the selection twice", sel.vertIdx)
		}
		seen[sel.vertIdx] = true
	}
}

// "Select this brush's sides" is a threshold on the face normal rather than an
// authored kind, because a brush face has no kind: a box's four upright sides
// qualify and its top and bottom do not.
func TestEditor3DMenuSelectsOnlyTheUprightFaces(t *testing.T) {
	withEditor3DMenu(t, menuLevel())
	selectFace(boxFaceNegX)

	mustMenuItem(t, editor3DContextMenuItems(), "Select this brush's sides").Do()

	if len(Editor.SelectedFaces) != 4 {
		t.Fatalf("selected %d faces, want the box's 4 upright sides", len(Editor.SelectedFaces))
	}
	for _, sel := range Editor.SelectedFaces {
		if sel.faceIdx == boxFaceTop || sel.faceIdx == boxFaceBottom {
			t.Errorf("face %d is horizontal and should not be a side", sel.faceIdx)
		}
	}
}

func TestEditor3DSnapToGridRoundsOntoTheStep(t *testing.T) {
	lvl := menuLevel()
	withEditor3DMenu(t, lvl)
	Editor.StepSize = 0.25

	Editor.SpawnSelected = true
	mustMenuItem(t, editor3DContextMenuItems(), "Snap to grid").Do()
	want := mgl32.Vec3{1.25, 0, 2.5}
	if got := lvl.Spawn.Position; got.Sub(want).Len() > 1e-5 {
		t.Fatalf("spawn point snapped to %v, want %v", got, want)
	}
	if !EditorCanUndo() {
		t.Error("snapping moves geometry and must be undoable")
	}
}

func TestEditor3DSharedRowsAreAlwaysThere(t *testing.T) {
	withEditor3DMenu(t, menuLevel())

	for _, kind := range []func(){
		func() { Editor.SpawnSelected = true },
		func() { Editor.SpawnSelected = false; selectFace(boxFaceNegX) },
		func() { clearFaceSelection() },
	} {
		kind()
		items := editor3DContextMenuItems()
		for _, label := range []string{"Move spawn point here",
			"Snap to grid while dragging", "Show all point/edge boxes", "Undo", "Redo"} {
			mustMenuItem(t, items, label)
		}
	}

	items := editor3DContextMenuItems()
	if it := mustMenuItem(t, items, "Undo"); !it.Disabled {
		t.Error("Undo should be greyed out with an empty history")
	}
	mustMenuItem(t, items, "Show all point/edge boxes").Do()
	if !Editor.ShowPointSelectRadius {
		t.Fatal("the view toggles should act on the editor state")
	}
}

// ---------- the gesture ----------

func TestEditor3DRightClickOpensTheMenuAndRightDragDoesNot(t *testing.T) {
	withEditor3DMenu(t, menuLevel())
	setupPickCamera()
	Editor.SpawnSelected = true // something selected, so no picking runs

	editor3DContextPressed(400, 300)
	editor3DContextMoved(402, 301)
	if !editor3DContextReleased(402, 301) {
		t.Fatal("a right-click that did not drag should open the menu")
	}
	if !editor3DRightPress.pending || editor3DRightPress.menuX != 402 {
		t.Fatalf("menu pending=%v at %d, want it queued at the cursor",
			editor3DRightPress.pending, editor3DRightPress.menuX)
	}

	editor3DRightPress.pending = false
	editor3DContextPressed(400, 300)
	editor3DContextMoved(470, 330)
	if editor3DContextReleased(470, 330) {
		t.Fatal("a right-drag turns the camera and must not end in a menu")
	}
	if editor3DRightPress.pending {
		t.Error("no menu should be queued after a drag")
	}
}

func TestEditor3DRightClickSelectsUnderTheCursorOnlyWhenNothingIsSelected(t *testing.T) {
	// A brush straight ahead of the picking camera, with real geometry so the
	// ray has triangles to meet.
	lvl := &world.Level{
		Chunks: []world.Chunk{{
			ID:      "c0",
			Brushes: []world.Brush{world.NewBoxBrush("b0", mgl32.Vec3{0, 0, -3}, mgl32.Vec3{4, 4, 0.5}, "concrete")},
		}},
		// Somewhere behind the camera, so it is never what the ray finds — the
		// selection below is set directly, and the point is whether an existing
		// selection suppresses the pick.
		Spawn: &world.SpawnPoint{Position: mgl32.Vec3{0, 0, 40}},
	}
	withEditor3DMenu(t, lvl)
	for bi := range lvl.Chunks[0].Brushes {
		if err := lvl.Chunks[0].Brushes[bi].Load(); err != nil {
			t.Fatalf("loading: %v", err)
		}
	}
	lvl.Chunks[0].RecomputeBounds()
	mx, my := setupPickCamera()

	editor3DContextPressed(mx, my)
	editor3DContextReleased(mx, my)
	if len(Editor.SelectedFaces) != 1 || Editor.SelectedFaces[0].brushIdx != 0 {
		t.Fatalf("selection is %+v, want the brush face under the cursor", Editor.SelectedFaces)
	}

	// With something already selected the click is a menu and nothing else.
	clearFaceSelection()
	Editor.SpawnSelected = true
	editor3DContextPressed(mx, my)
	editor3DContextReleased(mx, my)
	if !Editor.SpawnSelected {
		t.Fatalf("spawn selection was replaced by the right-click (faces %+v)", Editor.SelectedFaces)
	}

	// Nor does it pick while a placement mode is armed — the click that
	// resolves placement is the left one.
	Editor.SpawnSelected = false
	Editor.SpawnPlacementMode = true
	t.Cleanup(func() { Editor.SpawnPlacementMode = false })
	editor3DContextPressed(mx, my)
	editor3DContextReleased(mx, my)
	if len(Editor.SelectedFaces) != 0 {
		t.Fatalf("selection is %+v, want nothing picked while placing", Editor.SelectedFaces)
	}
}
