package editor

// The entity editor's menu is built as data (entityContextMenuItems) for the
// reason the other two viewports' are: what a selection offers is a question a
// test can ask with no GL context and no ImGui frame.
//
// What is asserted here is the rule the menu exists to enforce — the rows
// follow the selection, and a row acts on THIS window's workspace rather than
// on the level open in the main window.

import (
	"testing"

	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// entityMenuFixture installs a document, a one-brush scratch level and this
// window's workspace, and clears the gesture between tests.
func entityMenuFixture(t *testing.T) *world.EntityDef {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	def := entityTestDoc(t)
	t.Cleanup(entityUseWorkspace())

	ch := world.Chunk{ID: entityGeomChunkID, Scale: 1}
	ch.Brushes = []world.Brush{
		world.NewBoxBrush("hull", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "concrete"),
	}
	ch.RecomputeBounds()
	prevLevel, prevWS, prevPress := entityGeomLevel, entityWS, entityRightPress
	entityGeomLevel = &world.Level{Chunks: []world.Chunk{ch}}
	_ = world.RebuildIndexes(entityGeomLevel)
	entityWS.level = entityGeomLevel
	entityWS.ActiveChunkIdx = 0
	entityWS.SelectedBrushes, entityWS.SelectedFaces, entityWS.SelectedVertices = nil, nil, nil
	entityWS.EditContext = editorObjectMode
	entityRightPress = ctxGesture{}
	t.Cleanup(func() {
		entityGeomLevel, entityWS, entityRightPress = prevLevel, prevWS, prevPress
	})
	return def
}

// TestEntityMenuFollowsTheSelection is the menu's whole contract: a brush is
// offered the things you do to a solid, a face the things you do to a surface,
// and nothing at all is offered a hint about what a click would pick up.
func TestEntityMenuFollowsTheSelection(t *testing.T) {
	entityMenuFixture(t)

	if got := entityCtxSelection(); got != entityCtxNone {
		t.Fatalf("a fresh fixture reads as selection %v", got)
	}
	if _, ok := findMenuItem(entityContextMenuItems(), "Duplicate"); ok {
		t.Error("an empty selection was offered Duplicate")
	}

	entityWS.SelectedBrushes = []brushSel{{chunkIdx: 0, brushIdx: 0}}
	if got := entityCtxSelection(); got != entityCtxBrush {
		t.Fatalf("a selected brush reads as %v", got)
	}
	items := entityContextMenuItems()
	if items[0].Label != "Brush hull" {
		t.Errorf("the heading is %q, want the brush's name", items[0].Label)
	}
	mustMenuItem(t, items, "Duplicate")
	mustMenuItem(t, items, "Edit this brush")

	entityWS.SelectedBrushes = nil
	entityWS.EditContext = editorEditMode
	entityWS.SelectedFaces = []faceSel{{chunkIdx: 0, brushIdx: 0, faceIdx: 0}}
	if got := entityCtxSelection(); got != entityCtxFaces {
		t.Fatalf("a selected face reads as %v", got)
	}
	items = entityContextMenuItems()
	mustMenuItem(t, items, "Extrude [E]")
	mustMenuItem(t, items, "Texture")
	mustMenuItem(t, items, "Collision")
}

// TestEntityMenuActsOnThisWindowsWorkspace is the failure the whole workspace
// split exists to prevent: a row here must edit the TYPE's geometry, never the
// level open in the main window.
func TestEntityMenuActsOnThisWindowsWorkspace(t *testing.T) {
	def := entityMenuFixture(t)

	entityWS.EditContext = editorEditMode
	entityWS.SelectedFaces = []faceSel{{chunkIdx: 0, brushIdx: 0, faceIdx: 0}}
	before := len(Editor.SelectedFaces)

	row := mustMenuItem(t, entityContextMenuItems(), "Collision")
	row.Do()

	f := &entityGeomLevel.Chunks[0].Brushes[0].Faces[0]
	if !f.NoCollide {
		t.Error("the row did not reach the type's own face")
	}
	if len(Editor.SelectedFaces) != before {
		t.Error("the row touched the level editor's face selection")
	}
	if !entityDefDirty {
		t.Error("an edit from the menu left the document clean")
	}
	if !EntityDefCanUndo() {
		t.Error("an edit from the menu pushed nothing onto the type's undo stack")
	}
	_ = def
}

// TestEntityMenuGestureIsADragOrAClick pins the shared gesture: travel between
// press and release is a camera turn and opens nothing.
func TestEntityMenuGestureIsADragOrAClick(t *testing.T) {
	entityMenuFixture(t)

	entityContextPressed(100, 100)
	entityContextMoved(140, 100)
	if entityContextReleased(140, 100) {
		t.Error("a right-drag opened the menu instead of turning the camera")
	}

	entityContextPressed(100, 100)
	entityContextMoved(101, 101)
	if !entityContextReleased(101, 101) {
		t.Error("a right-click that barely moved did not open the menu")
	}
	if !entityRightPress.pending {
		t.Error("the menu was not queued for the next frame")
	}
}

// TestEntityMenuStampSeedsGeometry: the stamp row has to work on a type with no
// brushes yet, which is the state every new type starts in.
func TestEntityMenuStampSeedsGeometry(t *testing.T) {
	entityMenuFixture(t)
	entityGeomLevel = nil
	entityWS.level = nil

	row := entityCtxStampMenuItem()
	if row.Disabled {
		t.Fatal("the stamp row is dead on a type with no geometry yet")
	}
	if len(row.Sub) == 0 {
		t.Fatal("the stamp row offers no primitives")
	}
}

// TestEntityDeleteTakesTheGeometrySelection: the Delete key and the menu's
// Delete row are one function, and a selected FACE names the solid it belongs
// to — which is what the same key does in the 3D view.
func TestEntityDeleteTakesTheGeometrySelection(t *testing.T) {
	entityMenuFixture(t)
	ch := &entityGeomLevel.Chunks[0]
	ch.Brushes = append(ch.Brushes,
		world.NewBoxBrush("hold", mgl32.Vec3{4, 0, 0}, mgl32.Vec3{1, 1, 1}, "concrete"))

	entityWS.EditContext = editorEditMode
	entityWS.SelectedFaces = []faceSel{{chunkIdx: 0, brushIdx: 1, faceIdx: 0}}
	entityDeleteSelection()

	if len(ch.Brushes) != 1 || ch.Brushes[0].ID != "hull" {
		t.Fatalf("Delete on a face left %d brushes: %v", len(ch.Brushes), ch.Brushes)
	}

	// And both selected brushes go together, so the row that says "(2 brushes)"
	// is telling the truth.
	ch.Brushes = append(ch.Brushes,
		world.NewBoxBrush("hold", mgl32.Vec3{4, 0, 0}, mgl32.Vec3{1, 1, 1}, "concrete"))
	entityWS.EditContext = editorObjectMode
	entityWS.SelectedFaces = nil
	entityWS.SelectedBrushes = []brushSel{{chunkIdx: 0, brushIdx: 0}, {chunkIdx: 0, brushIdx: 1}}
	entityDeleteSelectedBrushes()
	if len(ch.Brushes) != 0 {
		t.Errorf("deleting a two-brush selection left %d", len(ch.Brushes))
	}
}
