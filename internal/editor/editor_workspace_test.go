package editor

// editor_workspace_test.go — the seam that lets two windows share one set of
// brush tools.
//
// What is worth pinning is not that the tools work — they have their own tests
// — but that they act on the RIGHT geometry. The failure the workspace exists
// to prevent is silent and looks like something else: a tool that read the
// level editor's state while the entity editor was driving it would reshape the
// open level from the entity editor's viewport, and the author would see the
// main window losing brushes for no reason.

import (
	"testing"

	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// twoWorkspaces installs a level in the level editor's workspace and a
// one-chunk scratch level in a second, and returns them.
func twoWorkspaces(t *testing.T) (levelLvl, otherLvl *world.Level, other *brushWorkspace) {
	t.Helper()
	testenv.WithoutGeometryUploads(t)

	levelLvl = &world.Level{Chunks: []world.Chunk{{ID: "level", Scale: 1}}}
	levelLvl.Chunks[0].Brushes = []world.Brush{
		world.NewBoxBrush("in_level", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "concrete"),
	}
	levelLvl.Chunks[0].RecomputeBounds()

	otherLvl = &world.Level{Chunks: []world.Chunk{{ID: "other", Scale: 1}}}
	otherLvl.Chunks[0].Brushes = []world.Brush{
		world.NewBoxBrush("in_type", mgl32.Vec3{10, 0, 0}, mgl32.Vec3{1, 1, 1}, "concrete"),
	}
	otherLvl.Chunks[0].RecomputeBounds()

	prevLevel, prevChunk := Editor.level, Editor.ActiveChunkIdx
	prevMode, prevSel := Editor.Mode, Editor.SelectedBrushes
	t.Cleanup(func() {
		Editor.level, Editor.ActiveChunkIdx = prevLevel, prevChunk
		Editor.Mode, Editor.SelectedBrushes = prevMode, prevSel
	})
	Editor.level, Editor.ActiveChunkIdx, Editor.Mode = levelLvl, 0, Mode3D
	Editor.SelectedBrushes = nil

	other = &brushWorkspace{
		level: otherLvl, ActiveChunkIdx: 0,
		ActiveBrushIdx: -1, ActiveFaceIdx: -1,
		editingFn: func() bool { return true },
		undoFn:    func() {},
		statusFn:  func(string, ...any) {},
	}
	return levelLvl, otherLvl, other
}

// TestWsDefaultsToTheLevelEditor: the resting state is the one every existing
// caller assumed, so a file that has not been retargeted still means the level.
func TestWsDefaultsToTheLevelEditor(t *testing.T) {
	lvl, _, _ := twoWorkspaces(t)
	if ws().level != lvl {
		t.Error("with no workspace installed, ws() is not the level editor's")
	}
}

// TestAToolActsOnTheInstalledWorkspace is the whole point: the same function,
// two different sets of brushes.
func TestAToolActsOnTheInstalledWorkspace(t *testing.T) {
	lvl, other, otherWS := twoWorkspaces(t)

	before := len(lvl.Chunks[0].Brushes)
	restore := otherWS.use()
	editorAddBrushAt(brushKindBox, mgl32.Vec3{4, 0, 0})
	restore()

	if got := len(lvl.Chunks[0].Brushes); got != before {
		t.Errorf("stamping into the second workspace changed the level: %d brushes, was %d",
			got, before)
	}
	if len(other.Chunks[0].Brushes) != 2 {
		t.Errorf("the stamp did not reach the installed workspace: %d brushes",
			len(other.Chunks[0].Brushes))
	}
}

// TestUseRestoresThePreviousWorkspace pins the mechanism the whole seam rests
// on. A workspace left installed after the window that installed it returned
// would point the level editor's own tools at the wrong geometry, and the
// failure would look like the main window being haunted rather than like a
// missing restore.
func TestUseRestoresThePreviousWorkspace(t *testing.T) {
	lvl, other, otherWS := twoWorkspaces(t)

	restore := otherWS.use()
	if ws().level != other {
		t.Fatal("use() did not install the workspace")
	}
	restore()
	if ws().level != lvl {
		t.Error("the restore did not put the level editor's workspace back")
	}
}

// TestSelectionsDoNotLeakBetweenWorkspaces: each holds its own, so picking a
// brush in one window does not put a gizmo on a brush in the other.
func TestSelectionsDoNotLeakBetweenWorkspaces(t *testing.T) {
	_, _, otherWS := twoWorkspaces(t)

	restore := otherWS.use()
	setBrushSelection([]brushSel{{chunkIdx: 0, brushIdx: 0}})
	restore()

	if len(Editor.SelectedBrushes) != 0 {
		t.Errorf("selecting in the second workspace selected %d brushes in the level editor",
			len(Editor.SelectedBrushes))
	}
	if len(otherWS.SelectedBrushes) != 1 {
		t.Errorf("the selection did not land in the installed workspace: %d",
			len(otherWS.SelectedBrushes))
	}
}

// TestTheEntityEditorsWorkspaceIsItsOwn is the same claim about the real one,
// rather than a fixture — the entity editor's tools must not be able to reach
// the open level even when both are non-nil.
func TestTheEntityEditorsWorkspaceIsItsOwn(t *testing.T) {
	lvl, _, _ := twoWorkspaces(t)
	entityTestDoc(t)

	prev := entityGeomLevel
	scratch := &world.Level{Chunks: []world.Chunk{{ID: entityGeomChunkID, Scale: 1}}}
	entityGeomLevel, entityWS.level, entityWS.ActiveChunkIdx = scratch, scratch, 0
	t.Cleanup(func() { entityGeomLevel, entityWS.level = prev, prev })

	restore := entityUseWorkspace()
	if ws().level != scratch {
		t.Fatal("the entity editor's workspace is not the one its tools would act on")
	}
	editorAddBrushAt(brushKindBox, mgl32.Vec3{})
	restore()

	if len(lvl.Chunks[0].Brushes) != 1 {
		t.Errorf("a stamp in the entity editor reached the open level: %d brushes",
			len(lvl.Chunks[0].Brushes))
	}
	if len(scratch.Chunks[0].Brushes) != 1 {
		t.Errorf("the stamp did not reach the type: %d brushes", len(scratch.Chunks[0].Brushes))
	}
}

// TestPickingInOneWindowDoesNotDeselectTheOther is the leak that made
// clearLevelObjectSelectionsFn necessary. Selecting a brush clears every OTHER
// kind of selection, and those live on Editor outright — so from the entity
// editor it dropped whatever the main window was showing, once per click.
func TestPickingInOneWindowDoesNotDeselectTheOther(t *testing.T) {
	_, _, otherWS := twoWorkspaces(t)
	otherWS.clearLevelObjectSelectionsFn = func() {}

	prev := Editor.SpawnSelected
	t.Cleanup(func() { Editor.SpawnSelected = prev })
	Editor.SpawnSelected = true

	restore := otherWS.use()
	setBrushSelection([]brushSel{{chunkIdx: 0, brushIdx: 0}})
	restore()

	if !Editor.SpawnSelected {
		t.Error("picking a brush in the second workspace deselected the level editor's spawn marker")
	}
}
