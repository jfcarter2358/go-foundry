package editor

// The 3D menu is built as data for the same reason the model editor's is: what
// a wall, a vertex, the spawn point or empty space offers is a question about
// the item tree, and answering it needs neither a GL context nor an ImGui
// frame.
//
// Rows whose actions rebuild geometry (a texture change, a flag toggle) are
// checked for presence and enabled-ness only — running them would upload to a
// GL context these tests do not have.

import (
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// withEditor3DMenu installs a level, puts the editor in 3D mode, and restores
// every global the menu reads or writes.
func withEditor3DMenu(t *testing.T, lvl *world.Level) {
	t.Helper()
	restore := saveEditorSelection()
	prevSurface, prevEdgeKind, prevEdgeIdx := Editor.ActiveSurface, Editor.ActiveEdgeKind, Editor.ActiveEdgeIdx
	prevSectorIdx, prevFocus, prevFloorVerts := Editor.ActiveSectorIdx, Editor.PanelFocus, Editor.EditFloorVerts
	prevBoxes, prevPress := Editor.ShowPointSelectRadius, editor3DRightPress
	prevUndo, prevRedo := levelUndoStack, levelRedoStack

	levelUndoStack.clear()
	levelRedoStack.clear()
	editor3DRightPress = ctxGesture{}
	Editor.Mode = Mode3D
	Editor.level = lvl
	Editor.ActiveChunkIdx = 0
	Editor.ActiveSectorIdx = -1
	Editor.SpawnSelected = false
	Editor.ActiveSurface = ""
	Editor.ActiveEdgeKind = ""
	Editor.ActiveEdgeIdx = -1
	Editor.SelectedVertices = nil
	Editor.StepSize = 0.25
	Editor.SnapToGrid = false

	t.Cleanup(func() {
		Editor.ActiveSurface, Editor.ActiveEdgeKind, Editor.ActiveEdgeIdx = prevSurface, prevEdgeKind, prevEdgeIdx
		Editor.ActiveSectorIdx, Editor.PanelFocus, Editor.EditFloorVerts = prevSectorIdx, prevFocus, prevFloorVerts
		Editor.ShowPointSelectRadius, editor3DRightPress = prevBoxes, prevPress
		levelUndoStack, levelRedoStack = prevUndo, prevRedo
		restore()
	})
}

// menuLevel is a one-chunk level with a square sector — four points, four
// walls — plus a spawn point, so every selection kind has something to resolve
// to.
func menuLevel() *world.Level {
	sector := world.Sector{
		ID:           "s0",
		PointIndices: []int{0, 1, 2, 3},
		Walls:        []world.Wall{{Texture: "concrete", Visible: true, Collision: true}},
		Portals:      []world.Portal{{ToSector: "s1", Visible: true}},
	}
	sector.Floor.Enabled = true
	sector.Floor.Texture = "concrete"
	return &world.Level{
		Chunks: []world.Chunk{{
			ID:      "c0",
			Points:  []mgl32.Vec2{{0, 0}, {4, 0}, {4, 4}, {0, 4}},
			Sectors: []world.Sector{sector},
		}},
		Spawn: &world.SpawnPoint{Position: mgl32.Vec3{1.13, 0, 2.4}},
	}
}

// selectSurface points the editor at one of the sector's surfaces the way a
// click on it would. It goes through setFaceSelection rather than writing the
// Active* fields, since those are derived from the face selection now
// (editor_face_sel.go) and setting them by hand would leave the two disagreeing.
func selectSurface(kind string, edgeIdx int) {
	if kind != "wall" && kind != "portal" {
		edgeIdx = -1
	}
	setFaceSelection([]faceSel{{chunkIdx: 0, sectorIdx: 0, kind: kind, idx: edgeIdx}})
}

func TestEditor3DMenuOffersTheWallsOwnActions(t *testing.T) {
	withEditor3DMenu(t, menuLevel())
	selectSurface("wall", 0)

	items := editor3DContextMenuItems()
	if items[0].Label != "Wall #0 — sector s0" {
		t.Fatalf("heading is %q, want the wall named", items[0].Label)
	}
	for _, label := range []string{"Texture", "Edit wall in 2D", "Visible", "Collision",
		"Smooth", "Convert to portal", "Select this sector's vertices"} {
		if it := mustMenuItem(t, items, label); it.Disabled {
			t.Errorf("%q should be live on a wall", label)
		}
	}
	if _, ok := findMenuItem(items, "Enabled"); ok {
		t.Error("a wall has Visible, not Enabled — the menu should follow the schema")
	}
	if it := mustMenuItem(t, items, "Collision"); !it.Checked {
		t.Error("the wall is solid, so Collision should be ticked")
	}
}

func TestEditor3DMenuFloorDropsTheEdgeOnlyRows(t *testing.T) {
	withEditor3DMenu(t, menuLevel())
	selectSurface("floor", -1)

	items := editor3DContextMenuItems()
	if items[0].Label != "Floor — sector s0" {
		t.Fatalf("heading is %q", items[0].Label)
	}
	mustMenuItem(t, items, "Enabled")
	mustMenuItem(t, items, "Edit floor in 2D")
	if _, ok := findMenuItem(items, "Convert to portal"); ok {
		t.Error("a floor is not an edge and cannot become a portal")
	}
	if it := mustMenuItem(t, items, "Collision"); !it.Disabled {
		t.Error("a floor has no collision flag, so the row should be greyed out")
	}
}

func TestEditor3DMenuPortalGreysOutWhatItLacks(t *testing.T) {
	withEditor3DMenu(t, menuLevel())
	selectSurface("portal", 0)

	items := editor3DContextMenuItems()
	// A portal's only surface is its optional gap quad — there is no face to
	// smooth and nothing to open in the 2D surface
	if it := mustMenuItem(t, items, "Smooth"); !it.Disabled {
		t.Error("a portal has no smoothing")
	}
	if it := mustMenuItem(t, items, "Edit portal in 2D"); !it.Disabled {
		t.Error("a portal has no 2D surface view")
	}
	mustMenuItem(t, items, "Gap texture")
	mustMenuItem(t, items, "Convert to wall")
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
	if _, ok := findMenuItem(items, "Edit wall in 2D"); ok {
		t.Error("surface rows have no business in the spawn menu")
	}
}

func TestEditor3DMenuVertexBlock(t *testing.T) {
	withEditor3DMenu(t, menuLevel())
	Editor.SelectedVertices = []vertexSel{
		{chunkIdx: 0, sectorIdx: 0, vertIdx: 1, pointIdx: 1, isFloor: true},
	}
	Editor.ActiveSectorIdx = 0

	items := editor3DContextMenuItems()
	if items[0].Label != "Vertex #1 (floor)" {
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

func TestEditor3DMenuSelectsOneRingOfTheSector(t *testing.T) {
	withEditor3DMenu(t, menuLevel())
	selectSurface("floor", -1)
	Editor.EditFloorVerts = true

	mustMenuItem(t, editor3DContextMenuItems(), "Select this sector's vertices").Do()

	if len(Editor.SelectedVertices) != 4 {
		t.Fatalf("selected %d vertices, want the sector's 4", len(Editor.SelectedVertices))
	}
	// One ring only: a floor handle and the ceiling handle above it share a
	// chunk point, and a selection holding both would move that point twice
	// per drag.
	seen := map[int]bool{}
	for _, sel := range Editor.SelectedVertices {
		if !sel.isFloor {
			t.Fatal("the ceiling ring should not be in the selection")
		}
		if seen[sel.pointIdx] {
			t.Fatalf("chunk point %d is in the selection twice", sel.pointIdx)
		}
		seen[sel.pointIdx] = true
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
		func() { Editor.SpawnSelected = false; selectSurface("wall", 0) },
		func() { selectSurface("", -1) },
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
	lvl := menuLevel()
	// A wall quad straight ahead of the picking camera.
	lvl.Chunks[0].Sectors[0].Walls[0].Geometry.Vertices = quadVerts(-3, 4)
	withEditor3DMenu(t, lvl)
	mx, my := setupPickCamera()

	editor3DContextPressed(mx, my)
	editor3DContextReleased(mx, my)
	if Editor.ActiveSurface != "wall" || Editor.ActiveEdgeIdx != 0 {
		t.Fatalf("selection is %q #%d, want the wall under the cursor",
			Editor.ActiveSurface, Editor.ActiveEdgeIdx)
	}

	// With something already selected the click is a menu and nothing else.
	Editor.ActiveSurface, Editor.ActiveEdgeKind, Editor.ActiveEdgeIdx = "", "", -1
	Editor.SpawnSelected = true
	editor3DContextPressed(mx, my)
	editor3DContextReleased(mx, my)
	if !Editor.SpawnSelected {
		t.Fatalf("spawn selection was replaced by the right-click (surface %q)", Editor.ActiveSurface)
	}

	// Nor does it pick while a placement mode is armed — the click that
	// resolves placement is the left one.
	Editor.SpawnSelected = false
	Editor.SpawnPlacementMode = true
	t.Cleanup(func() { Editor.SpawnPlacementMode = false })
	editor3DContextPressed(mx, my)
	editor3DContextReleased(mx, my)
	if Editor.ActiveSurface != "" {
		t.Fatalf("selection is %q, want nothing picked while placing", Editor.ActiveSurface)
	}
}
