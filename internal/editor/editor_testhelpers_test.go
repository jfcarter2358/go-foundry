package editor

// editor_testhelpers_test.go — what the editor's tests share: float
// comparisons, a snapshot of the global editor state, and a brush fixture.
//
// internal/world and internal/collision carry the same few float helpers. A
// test helper cannot cross a package boundary without becoming part of that
// package's API, and a tolerance check is not worth exporting for. The level
// fixtures that ARE worth sharing live in internal/leveltest.

import (
	"testing"

	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// vec3Close reports whether two vectors agree to within eps on every axis.
func vec3Close(a, b mgl32.Vec3, eps float32) bool {
	for i := 0; i < 3; i++ {
		d := a[i] - b[i]
		if d < -eps || d > eps {
			return false
		}
	}
	return true
}

// vecNear is vec3Close under the name internal/world's tests use.
func vecNear(a, b mgl32.Vec3, tol float32) bool { return vec3Close(a, b, tol) }

// nearlyEqual reports whether two scalars agree within tol.
func nearlyEqual(a, b, tol float32) bool {
	d := a - b
	return d <= tol && d >= -tol
}

// ---------- editor state ----------

// saveEditorState snapshots the whole EditorState and returns a restore
// function, for a test that has to reach into the global editor.
//
// The whole struct, not a field-by-field copy. The field-by-field version this
// replaces listed thirty of them and silently stopped restoring each one the
// migration renamed — a test that leaked state into the next one, in a package
// whose tests all share a single global. Copying the struct cannot fall behind
// it. (Its slices are shared with the copy, which is exactly what is wanted:
// the point is to put the *same* selection back, not a duplicate of it.)
func saveEditorState() func() {
	snapshot := Editor
	return func() { Editor = snapshot }
}

// saveEditorSelection is saveEditorState under the name the tests already use.
func saveEditorSelection() func() { return saveEditorState() }

// ---------- level fixtures ----------

// brushTestLevel installs a level holding one chunk of two box brushes, side
// by side and touching at x=2, and returns the chunk.
//
// Two solids rather than one, because most of what these tests check is about
// telling one from another: which brush a face belongs to, which of two
// selections is the primary, whether an edit reached the second brush as well.
// Boxes rather than a hollow room because a box has six faces of known
// orientation and eight corners of known position, which is what an assertion
// can be written against.
func brushTestLevel(t *testing.T) *world.Chunk {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	ch := world.Chunk{ID: "test_chunk"}
	ch.Brushes = []world.Brush{
		world.NewBoxBrush("a", mgl32.Vec3{1, 1, 1}, mgl32.Vec3{1, 1, 1}, "concrete"),
		world.NewBoxBrush("b", mgl32.Vec3{3, 1, 1}, mgl32.Vec3{1, 1, 1}, "concrete"),
	}
	for bi := range ch.Brushes {
		if err := ch.Brushes[bi].Load(); err != nil {
			t.Fatalf("loading brush %q: %v", ch.Brushes[bi].ID, err)
		}
	}
	ch.RecomputeBounds()

	Editor.level = &world.Level{Chunks: []world.Chunk{ch}}
	Editor.Mode = Mode3D
	Editor.ActiveChunkIdx = 0
	Editor.ActiveBrushIdx = -1
	Editor.ActiveFaceIdx = -1
	Editor.SelectedFaces = nil
	Editor.SelectedVertices = nil
	Editor.SpawnSelected = false
	Editor.ChunkSelected = false
	return &Editor.level.Chunks[0]
}

// boxFace* name the faces NewBoxBrush emits, in the order it emits them, so a
// test can say which side of a box it means instead of counting.
const (
	boxFaceNegX   = 0
	boxFacePosX   = 1
	boxFaceBottom = 2
	boxFaceTop    = 3
	boxFaceNegZ   = 4
	boxFacePosZ   = 5
)
