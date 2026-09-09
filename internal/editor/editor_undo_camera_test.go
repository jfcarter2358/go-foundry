package editor

// The author's viewpoint is not part of the level, so a snapshot swap must not
// move it. It used to: restoreLevelSnapshot applied the spawn point, and the
// editor camera *is* gfx.Cam, so every Ctrl+Z threw the view to the spawn and
// away from the edit being undone.

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// undoCamLevel is a level whose spawn is nowhere near where the test parks the
// camera, so a teleport to it is unmistakable.
func undoCamLevel() *world.Level {
	ch := world.Chunk{
		ID:      "c0",
		Brushes: []world.Brush{world.NewBoxBrush("b0", mgl32.Vec3{2, 2, 2}, mgl32.Vec3{2, 2, 2}, "concrete")},
	}
	ch.RecomputeBounds()
	return &world.Level{
		Chunks: []world.Chunk{ch},
		Spawn:  &world.SpawnPoint{Position: mgl32.Vec3{200, 0, -150}},
	}
}

// withSpawnMarkerTexture makes the spawn point loadable with no GL context.
// Restoring a snapshot re-loads the marker model, which resolves its texture by
// name, and an unregistered name fails the whole level rebuild.
func withSpawnMarkerTexture(t *testing.T) {
	t.Helper()
	if gfx.Textures == nil {
		gfx.Textures = map[string]uint32{}
	}
	if gfx.TextureNames == nil {
		gfx.TextureNames = map[string]string{}
	}
	for _, n := range []string{"", "default", "concrete"} {
		if _, ok := gfx.Textures[n]; ok {
			continue
		}
		gfx.Textures[n], gfx.TextureNames[n] = 1, n
		t.Cleanup(func() { delete(gfx.Textures, n); delete(gfx.TextureNames, n) })
	}
}

func TestUndoRedoLeaveTheCameraWhereTheAuthorPutIt(t *testing.T) {
	withEditor3DMenu(t, undoCamLevel())
	withSpawnMarkerTexture(t)

	eye := mgl32.Vec3{4, 9, 11}
	gfx.Cam.Position, gfx.Cam.Yaw, gfx.Cam.Pitch = eye, 30, -12

	EditorPushUndo()
	Editor.level.Chunks[0].Brushes = nil

	EditorUndo()
	if gfx.Cam.Position != eye {
		t.Errorf("undo moved the eye to %v, want it left at %v", gfx.Cam.Position, eye)
	}
	if len(Editor.level.Chunks[0].Brushes) != 1 {
		t.Fatalf("undo restored %d brushes, want 1", len(Editor.level.Chunks[0].Brushes))
	}

	EditorRedo()
	if gfx.Cam.Position != eye {
		t.Errorf("redo moved the eye to %v, want it left at %v", gfx.Cam.Position, eye)
	}
	if len(Editor.level.Chunks[0].Brushes) != 0 {
		t.Fatalf("redo left %d brushes, want 0", len(Editor.level.Chunks[0].Brushes))
	}
}
