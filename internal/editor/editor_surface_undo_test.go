package editor

// editor_surface_undo_test.go — Ctrl+Z while shaping a surface.
//
// Undo restores the whole level from a snapshot, so every editor selection that
// names something by index has to be dropped with it. Surface shaping was
// dropped wholesale, mode included — which meant a correction to the shape you
// were working on threw you out of the tool you were working in.

import (
	"foundry/internal/gfx"
	"foundry/internal/testenv"
	"foundry/internal/world"
	"testing"
)

// withTestTextures registers the names stackedTestChunk paints with, so the
// undo round trip through RawLevel — which resolves every texture by name —
// can rebuild the level headlessly.
func withTestTextures(t *testing.T) {
	t.Helper()
	prev := gfx.Textures
	if gfx.Textures == nil {
		gfx.Textures = map[string]uint32{}
	}
	for _, n := range []string{"", "default", "concrete"} {
		gfx.Textures[n] = 1
	}
	t.Cleanup(func() { gfx.Textures = prev })
}

// shapingFixture puts the editor into surface shaping on the ground sector's
// floor, with a fresh undo history, and returns the face being shaped.
func shapingFixture(t *testing.T) faceSel {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	withTestTextures(t)
	stackedTestChunk(t)
	Editor.Mode = Mode3D
	EditorClearUndoHistory()
	t.Cleanup(EditorClearUndoHistory)

	sel := surfaceFaceSel(0, 0, "floor")
	EnterSurfaceEdit(sel)
	if !Editor.SurfaceEditing {
		t.Fatal("could not enter surface shaping")
	}
	return sel
}

func TestUndoKeepsYouInSurfaceShaping(t *testing.T) {
	defer saveEditorSelection()()
	sel := shapingFixture(t)

	// A real shaping edit: subdivide the seeded face, which is what an author
	// would then want to take back.
	_, s, mesh, _, ok := surfaceEditTarget()
	if !ok {
		t.Fatal("the shaped surface did not resolve")
	}
	before := len(mesh.Faces)
	EditorPushUndo()
	mesh.SurfaceSubdivideFaces([]int{0})
	_ = world.ReloadSector(&Editor.level.Chunks[0], s)
	if len(mesh.Faces) <= before {
		t.Fatalf("the subdivide did nothing: %d faces before, %d after", before, len(mesh.Faces))
	}
	Editor.SurfaceFaces = []int{0, 1, 2, 3}

	EditorUndo()

	if !Editor.SurfaceEditing {
		t.Fatal("undo dropped out of surface shaping")
	}
	if Editor.SurfaceFace != sel {
		t.Errorf("shaping moved to %+v, want the face it was on (%+v)", Editor.SurfaceFace, sel)
	}
	// The mesh selection must NOT survive: it named faces the restored mesh
	// does not have.
	if len(Editor.SurfaceFaces) != 0 || len(Editor.SurfaceVerts) != 0 || len(Editor.SurfaceEdges) != 0 {
		t.Errorf("a stale mesh selection survived the undo: %v / %v / %v",
			Editor.SurfaceFaces, Editor.SurfaceVerts, Editor.SurfaceEdges)
	}
	// And the edit itself was actually undone.
	_, _, mesh, _, ok = surfaceEditTarget()
	if !ok {
		t.Fatal("the shaped surface no longer resolves after the undo")
	}
	if len(mesh.Faces) != before {
		t.Errorf("the mesh has %d faces after the undo, want the %d it had before the subdivide",
			len(mesh.Faces), before)
	}
	// The level face stays selected, so the properties panel still shows it.
	if !isFaceSelected(sel) {
		t.Error("the shaped face is no longer part of the level selection")
	}
}

// Redo has to behave the same way — it goes through the identical snapshot
// path, and coming back to an edit you just took back is no more a reason to
// leave the tool than taking it back was.
func TestRedoAlsoKeepsYouInSurfaceShaping(t *testing.T) {
	defer saveEditorSelection()()
	shapingFixture(t)

	_, s, mesh, _, _ := surfaceEditTarget()
	EditorPushUndo()
	mesh.SurfaceSubdivideFaces([]int{0})
	_ = world.ReloadSector(&Editor.level.Chunks[0], s)
	after := len(mesh.Faces)

	EditorUndo()
	EditorRedo()

	if !Editor.SurfaceEditing {
		t.Fatal("redo dropped out of surface shaping")
	}
	_, _, mesh, _, ok := surfaceEditTarget()
	if !ok {
		t.Fatal("the shaped surface no longer resolves after the redo")
	}
	if len(mesh.Faces) != after {
		t.Errorf("the mesh has %d faces after the redo, want %d", len(mesh.Faces), after)
	}
}

// Undoing back past the seed EnterSurfaceEdit lays down is undoing the entry
// into shaping itself: there is no mesh left, so the mode goes.
func TestUndoingPastTheSeedLeavesSurfaceShaping(t *testing.T) {
	defer saveEditorSelection()()
	testenv.WithoutGeometryUploads(t)
	withTestTextures(t)
	stackedTestChunk(t)
	Editor.Mode = Mode3D
	EditorClearUndoHistory()
	defer EditorClearUndoHistory()

	// EnterSurfaceEdit pushes its own undo entry before seeding a flat face.
	EnterSurfaceEdit(surfaceFaceSel(0, 0, "floor"))
	if !Editor.SurfaceEditing {
		t.Fatal("could not enter surface shaping")
	}

	EditorUndo()

	if Editor.SurfaceEditing {
		t.Error("still shaping a surface whose mesh the undo removed")
	}
}
