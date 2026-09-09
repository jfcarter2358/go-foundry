package editor

// editor_compile_test.go — the compiled preview.
//
// What is checked is the SWAP, in both directions: while the preview is up the
// level is in exactly the state a release build loads into, and when it comes
// down the level is back to being editable. Anything less and an author is one
// keystroke away from editing a level that is not drawing what they edit.

import (
	"os"
	"testing"

	"foundry/internal/levelio"
	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// previewLevel is a sealed room plus a chunk the compiler will not bake, so the
// swap has both kinds to get right.
func previewLevel() *world.Level {
	return &world.Level{
		Chunks: []world.Chunk{
			{ID: "static", Scale: 1,
				Brushes: world.NewHollowRoom("room", mgl32.Vec3{-4, 0, -4}, mgl32.Vec3{4, 4, 4}, 0.5, "concrete")},
			{ID: "later", Scale: 1, Disabled: true,
				Brushes: []world.Brush{world.NewBoxBrush("block", mgl32.Vec3{40, 0, 0}, mgl32.Vec3{1, 1, 1}, "concrete")}},
		},
		Lights: world.LightSet{Global: world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.3}},
	}
}

func withPreviewLevel(t *testing.T) *world.Level {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	restore := saveEditorState()
	lvl := previewLevel()
	for ci := range lvl.Chunks {
		for bi := range lvl.Chunks[ci].Brushes {
			if err := lvl.Chunks[ci].Brushes[bi].Load(); err != nil {
				t.Fatal(err)
			}
		}
	}
	Editor.level = lvl
	t.Cleanup(restore)
	return lvl
}

func meshedFaces(ch *world.Chunk) int {
	n := 0
	for bi := range ch.Brushes {
		for fi := range ch.Brushes[bi].Faces {
			if len(ch.Brushes[bi].Faces[fi].Geometry.Vertices) > 0 {
				n++
			}
		}
	}
	return n
}

func TestThePreviewPutsTheLevelInTheStateAReleaseBuildLoads(t *testing.T) {
	lvl := withPreviewLevel(t)
	if EditorPreviewingCompiled() {
		t.Fatal("the preview was already up")
	}
	before := meshedFaces(&lvl.Chunks[0])
	if before == 0 {
		t.Fatal("the fixture has no meshes to lose")
	}

	if _, err := EditorSetCompiledPreview(true); err != nil {
		t.Fatalf("turning the preview on: %v", err)
	}
	if !EditorPreviewingCompiled() {
		t.Fatal("the preview did not come up")
	}
	if lvl.Static == nil || len(lvl.Static.Surfaces) == 0 {
		t.Fatal("no compiled surfaces")
	}
	if !lvl.Chunks[0].Compiled {
		t.Error("the baked chunk is not marked Compiled; it would be drawn twice")
	}
	if n := meshedFaces(&lvl.Chunks[0]); n != 0 {
		t.Errorf("the baked chunk kept %d meshes; they are a second copy of the static surfaces", n)
	}
	if lvl.Chunks[1].Compiled {
		t.Error("a disabled chunk was marked Compiled; the compiler never baked it")
	}
}

func TestTurningThePreviewOffPutsTheBrushesBack(t *testing.T) {
	lvl := withPreviewLevel(t)
	before := meshedFaces(&lvl.Chunks[0])

	if _, err := EditorSetCompiledPreview(true); err != nil {
		t.Fatal(err)
	}
	if _, err := EditorSetCompiledPreview(false); err != nil {
		t.Fatalf("turning it off: %v", err)
	}

	if EditorPreviewingCompiled() || lvl.Static != nil {
		t.Error("the preview is still up")
	}
	if lvl.Chunks[0].Compiled {
		t.Error("the chunk is still marked Compiled; nothing would draw it")
	}
	if got := meshedFaces(&lvl.Chunks[0]); got != before {
		t.Errorf("%d meshes came back, want the %d there were", got, before)
	}
}

// A preview is a picture of the level as it was when it was built, and an edit
// is the moment it stops being that. Leaving it up would show an author their
// last compile while they edited past it.
func TestAnEditDropsThePreview(t *testing.T) {
	lvl := withPreviewLevel(t)
	levelUndoStack.clear()
	levelRedoStack.clear()

	if _, err := EditorSetCompiledPreview(true); err != nil {
		t.Fatal(err)
	}
	EditorPushUndo()
	if EditorPreviewingCompiled() {
		t.Error("the preview survived an edit")
	}
	if lvl.Chunks[0].Compiled {
		t.Error("the chunk is still marked Compiled after the preview came down")
	}
}

// The compile itself, from the editor: a sealed room does not leak, and the
// artefact is written where a release build looks for it.
func TestCompilingFromTheEditorWritesBesideTheLevel(t *testing.T) {
	withPreviewLevel(t)
	Editor.SavePath = t.TempDir() + "/room.yaml"

	msg, err := EditorCompileLevel()
	if err != nil {
		t.Fatalf("EditorCompileLevel: %v", err)
	}
	if msg == "" {
		t.Error("the compile said nothing")
	}
	if _, err := os.Stat(levelio.CompiledPath(Editor.SavePath)); err != nil {
		t.Errorf("no artefact beside the level: %v", err)
	}
}
