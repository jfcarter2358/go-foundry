package harness

// pointer_test.go — the script camera and the world pointer, through the real
// frame.
//
// Everything they do is ordering: the rig is laid over the camera after the
// script tick (sim.Step), and a click is picked through wherever the camera is
// by then. Unit tests call the pieces in the order they were written to be
// called; this runs the frame that actually calls them.

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/hooks"
	"foundry/internal/levelio"
	"foundry/internal/leveltest"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func TestAClickOnTheFloorFromAnOverheadCamera(t *testing.T) {
	dir := scratchProject(t, "pointroom")
	res := filepath.Join(dir, "resources")
	src := `def on_spawn(:spawn:) -> null
    @level.prop.cursor = true
    camera_move_to(<5.0, 3.5, 5.0>, 0.5)
    camera_turn_to(0.0, -89.0, 0.5)
end
def on_click(:pointer_click:, button, hit, at) -> null
    @player.attr.clicked = hit.kind
    @player.attr.clicked_y = at.y
end`
	if err := os.WriteFile(filepath.Join(res, "scripts", "pointer.flang"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	// The scratch room again, now with the level script on it.
	lvl := &world.Level{Chunks: []world.Chunk{*leveltest.RoomChunk(t)}, Scripts: []string{"scripts/pointer.flang"}}
	lvl.Lights.Global = world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 1}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	if err := levelio.SaveLevel(lvl, levelio.LevelPath("pointroom")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		gfx.ResetCameraRig()
		hooks.UnpinCursor()
		hooks.ScriptWantsCursor = false
	})

	s, err := Open(Options{Project: dir, Level: "pointroom"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.Teleport(mgl32.Vec3{2, 1, 2}) // out from under the camera's aim
	s.RunFor(1)

	if !gfx.CameraDetached() {
		t.Fatal("the level's camera_move_to did not detach the camera")
	}
	if d := gfx.Cam.Position.Sub(mgl32.Vec3{5, 3.5, 5}).Len(); d > 1e-3 {
		t.Fatalf("after its blend the camera is at %v, want <5,3.5,5>", gfx.Cam.Position)
	}
	if gfx.Cam.Direction.Y() > -0.99 {
		t.Fatalf("the camera looks along %v, want straight down", gfx.Cam.Direction)
	}
	if !hooks.ScriptWantsCursor {
		t.Fatal("@level.prop.cursor did not ask for the cursor")
	}

	if _, err := s.Play("cursor center; tap; wait 0.2", 10); err != nil {
		t.Fatalf("Play: %v", err)
	}
	p := world.PlayerEntity
	kind, ok := p.Attr("clicked")
	if !ok || kind.S != "brush" {
		t.Fatalf("clicking the middle of an overhead view hit %+v, want the room's floor brush", kind)
	}
	// leveltest's room has its floor at y=0, and the camera is under its
	// ceiling (4) — so this is the floor, picked from where the picture is
	// taken, and not from the player's eye across the room.
	if y, _ := p.Attr("clicked_y"); y.F > 0.05 || y.F < -0.05 {
		t.Errorf("the click met the world at y=%g, want the floor at 0", y.F)
	}
}
