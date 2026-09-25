package script

// script_pointer_test.go — the mouse pointing at the world, and a script
// driving the camera, from a script's side of the boundary.
//
// The level is hitscan's aiming room: the camera at <1,2,5> looking down +X,
// so the middle of the default 1600x900 window is straight down the room, and
// a crate at x=4 is what the cursor is on when it sits there.

import (
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
	"foundry/internal/gfx"
	"foundry/internal/hooks"
	"foundry/internal/savefile"
	"foundry/internal/world"
)

const midX, midY = 800, 450

// pointerLevel is the aiming room with a scripted crate in the middle of the
// view and, optionally, a level script.
func pointerLevel(t *testing.T, crateSrc, levelSrc string) *world.Level {
	t.Helper()
	root := withTempScriptDir(t)
	lvl := aimingLevel(t)
	gfx.UpdateCamMatrix()

	crate := target("crate", 4)
	crate.Attributes = world.AttrSet{
		"hit":   world.StringAttr(""),
		"seen":  world.StringAttr(""),
		"n":     world.IntAttr(0),
		"x":     world.FloatAttr(0),
		"level": world.StringAttr(""),
	}
	if crateSrc != "" {
		crate.Script = writeScript(t, root, "crate.flang", crateSrc)
	}
	lvl.Entities = []world.Entity{crate}
	if levelSrc != "" {
		lvl.Scripts = []string{writeScript(t, root, "level.flang", levelSrc)}
	}

	prevRT, prevEv := *Scripts, scriptEvents
	t.Cleanup(func() {
		*Scripts = prevRT
		scriptEvents = prevEv
		resetPointerTrack()
		gfx.ResetCameraRig()
		hooks.ScriptWantsCursor = false
	})
	ScriptEventsReset()
	ScriptRuntimeLoad(lvl)
	// The room's camera, again: loading resets the script camera, which is
	// exactly what it should do and not what this test wants undone.
	gfx.UpdateCamMatrix()
	return lvl
}

func ptick(lvl *world.Level, n int) {
	for i := 0; i < n; i++ {
		ScriptTick(lvl, 1.0/60.0, true)
	}
}

func freeCursor(x, y float32) {
	hooks.SetCursor(hooks.CursorState{X: x, Y: y, Free: true})
}

func crateAttr(t *testing.T, lvl *world.Level, name string) world.AttrValue {
	t.Helper()
	a, ok := lvl.Entities[0].Attr(name)
	if !ok {
		t.Fatalf("crate has no attribute %q", name)
	}
	return a
}

func TestPointerClickReachesTheObjectAndTheLevel(t *testing.T) {
	lvl := pointerLevel(t, `
def on_click(:pointer_click:, :left:, hit, at) -> null
    @self.attr.hit = hit.id
    @self.attr.x = at.x
on_click(:pointer_click:, button, hit, at)
    @self.attr.hit = "wrong button"
end`, `
def on_down(:pointer_down:, button, hit, at) -> null
    @crate.attr.level = hit.id
end`)
	ptick(lvl, 1)

	freeCursor(midX, midY)
	hooks.RecordPointer(hooks.PointerEvent{Kind: hooks.PointerDown, Button: hooks.PointerLeft, X: midX, Y: midY})
	hooks.RecordPointer(hooks.PointerEvent{Kind: hooks.PointerUp, Button: hooks.PointerLeft, X: midX, Y: midY})
	ptick(lvl, 2)

	if got := crateAttr(t, lvl, "hit").S; got != "crate" {
		t.Errorf("the crate's :pointer_click: saw hit %q, want itself", got)
	}
	// The crate's near face is at x=3.5; `at` is where the ray met the body.
	if x := crateAttr(t, lvl, "x").F; x < 3.4 || x > 3.6 {
		t.Errorf("at.x = %g, want the crate's near face at 3.5", x)
	}
	if got := crateAttr(t, lvl, "level").S; got != "crate" {
		t.Errorf("the level's :pointer_down: saw %q, want the crate", got)
	}
}

func TestPointerDragIsNotAClick(t *testing.T) {
	lvl := pointerLevel(t, `
def on_start(:pointer_drag_start:, button, hit, at) -> null
    @self.attr.seen = @self.attr.seen + "start "
end
def on_drag(:pointer_drag:, button, delta vec, at) -> null
    @self.attr.seen = @self.attr.seen + "drag "
    @self.attr.x = delta.x
end
def on_end(:pointer_drag_end:, button, hit, at) -> null
    @self.attr.seen = @self.attr.seen + "end "
end
def on_click(:pointer_click:, button, hit, at) -> null
    @self.attr.seen = @self.attr.seen + "click "
end`, "")
	ptick(lvl, 1)

	freeCursor(midX, midY)
	hooks.RecordPointer(hooks.PointerEvent{Kind: hooks.PointerDown, Button: hooks.PointerLeft, X: midX, Y: midY})
	ptick(lvl, 1)
	freeCursor(midX+2, midY) // a wobble, not a drag
	ptick(lvl, 1)
	freeCursor(midX+30, midY)
	ptick(lvl, 1)
	hooks.RecordPointer(hooks.PointerEvent{Kind: hooks.PointerUp, Button: hooks.PointerLeft, X: midX + 30, Y: midY})
	ptick(lvl, 2)

	if got := strings.TrimSpace(crateAttr(t, lvl, "seen").S); got != "start drag end" {
		t.Errorf("the drag was heard as %q, want \"start drag end\" and no click", got)
	}
	if dx := crateAttr(t, lvl, "x").F; dx != 30 {
		t.Errorf("the drag's delta.x = %g, want the 30 pixels travelled since the press", dx)
	}
}

func TestPointerHoverEntersAndExits(t *testing.T) {
	lvl := pointerLevel(t, `
def on_enter(:pointer_enter:, hit ref) -> null
    @self.attr.seen = @self.attr.seen + "enter "
end
def on_exit(:pointer_exit:, hit ref) -> null
    @self.attr.seen = @self.attr.seen + "exit "
end`, "")
	freeCursor(midX, midY)
	ptick(lvl, 2)
	hooks.SetCursor(hooks.CursorState{X: midX, Y: midY, Free: true, OverUI: true})
	ptick(lvl, 2)
	if got := strings.TrimSpace(crateAttr(t, lvl, "seen").S); got != "enter exit" {
		t.Errorf("hover, then a widget over it, was heard as %q, want \"enter exit\"", got)
	}
}

func TestACapturedCursorPointsAtNothing(t *testing.T) {
	lvl := pointerLevel(t, `
def on(:pointer_down:, button, hit, at) -> null
    @self.attr.n = @self.attr.n + 1
end`, "")
	hooks.SetCursor(hooks.CursorState{X: midX, Y: midY})
	hooks.RecordPointer(hooks.PointerEvent{Kind: hooks.PointerDown, Button: hooks.PointerLeft, X: midX, Y: midY})
	ptick(lvl, 2)
	if n := crateAttr(t, lvl, "n").I; n != 0 {
		t.Errorf("a press with the cursor captured posted %d :pointer_down:; it is the fire button's", n)
	}
}

func TestPickCastsThroughTheCursor(t *testing.T) {
	lvl := pointerLevel(t, `
def on_spawn(:spawn:) -> null
    pick(:picked:)
end
def on(:picked:, hit, at vec) -> null
    @self.attr.hit = hit.id
end`, "")
	freeCursor(midX, midY)
	ptick(lvl, 2)
	if got := crateAttr(t, lvl, "hit").S; got != "crate" {
		t.Fatalf("pick through the middle of the window hit %q, want the crate", got)
	}
}

func TestScreenRayAndWorldToScreen(t *testing.T) {
	lvl := pointerLevel(t, `
def on_spawn(:spawn:) -> null
    r = screen_ray(<800.0, 450.0, 0.0>)
    @self.attr.x = r["direction"].x
    p = world_to_screen(@self)
    @self.attr.n = round(p.x)
    behind = <1.0, 2.0, 5.0> - <50.0, 0.0, 0.0>
    if world_to_screen(behind) == null
        @self.attr.seen = "behind"
    end
end`, "")
	ptick(lvl, 2)
	if x := crateAttr(t, lvl, "x").F; x < 0.999 {
		t.Errorf("the ray through the middle points along x=%g, want the view's +X", x)
	}
	if n := crateAttr(t, lvl, "n").I; n != midX {
		t.Errorf("the crate is at x=%d on screen, want the middle, %d", n, midX)
	}
	if got := crateAttr(t, lvl, "seen").S; got != "behind" {
		t.Error("a point behind the camera came back on screen")
	}
}

func TestCameraVerbsDriveTheRig(t *testing.T) {
	lvl := pointerLevel(t, `
def on_spawn(:spawn:) -> null
    camera_orbit(@self, {"distance": 6.0, "yaw": 90.0, "pitch": 0.0})
end`, "")
	ptick(lvl, 1)
	if !gfx.CameraDetached() {
		t.Fatal("camera_orbit did not detach the camera")
	}
	gfx.StepCameraRig(1.0/60.0, true, true)
	want := mgl32.Vec3{4, 2, -1} // six units back from the crate, looking along +Z
	if gfx.Cam.Position.Sub(want).Len() > 1e-3 {
		t.Fatalf("orbiting the crate put the camera at %v, want %v", gfx.Cam.Position, want)
	}

	// The crate moves; the frame's refresh follows it.
	lvl.Entities[0].Position = mgl32.Vec3{4, 2, 9}
	ptick(lvl, 1)
	RefreshCameraTargets(lvl)
	gfx.StepCameraRig(1.0/60.0, true, true)
	want = mgl32.Vec3{4, 2, 3}
	if gfx.Cam.Position.Sub(want).Len() > 1e-3 {
		t.Fatalf("the orbit did not follow its target: camera at %v, want %v", gfx.Cam.Position, want)
	}
}

func TestCameraPropsAndCursor(t *testing.T) {
	lvl := pointerLevel(t, `
def on_spawn(:spawn:) -> null
    @self.attr.x = @level.prop.camera_yaw
    @level.prop.cursor = true
    @level.prop.camera_position = <0.0, 10.0, 0.0>
    @level.prop.camera_pitch = -90.0
end`, "")
	ptick(lvl, 2)
	if yaw := crateAttr(t, lvl, "x").F; yaw > 1e-3 || yaw < -1e-3 {
		t.Errorf("camera_yaw looking along +X read %g, want 0", yaw)
	}
	if !hooks.ScriptWantsCursor {
		t.Error("@level.prop.cursor = true did not ask for the cursor")
	}
	if !gfx.CameraDetached() || gfx.Cam.Position != (mgl32.Vec3{0, 10, 0}) {
		t.Errorf("writing camera_position left the camera at %v (detached %v)", gfx.Cam.Position, gfx.CameraDetached())
	}
	if gfx.Cam.Direction.Y() > -0.99 {
		t.Errorf("camera_pitch -90 looks along %v, want straight down", gfx.Cam.Direction)
	}
}

func TestOrbitOptionsCatchTypos(t *testing.T) {
	d := flang.NewDict()
	d.M["distanse"] = flang.FloatVal(3)
	_, _, _, err := orbitOptions(d)
	if err == nil || !strings.Contains(err.Error(), `"distance"`) {
		t.Fatalf("a misspelt option gave %v, want a suggestion of \"distance\"", err)
	}
	if _, err := easeValue("camera_zoom", flang.AtomVal("bouncy")); err == nil {
		t.Fatal("an unknown ease was accepted")
	}
	if e, err := easeValue("camera_zoom", flang.AtomVal("ease_out")); err != nil || e != gfx.EaseOut {
		t.Fatalf(":ease_out: read as %v, %v", e, err)
	}
}

// TestTheCameraAndCursorSurviveASave is the strategy level's case: the camera
// detached and orbiting a unit, and the cursor freed, all once in :spawn: —
// which a restore does not post again.
func TestTheCameraAndCursorSurviveASave(t *testing.T) {
	lvl := pointerLevel(t, `
def on_spawn(:spawn:) -> null
    @level.prop.cursor = true
    camera_orbit(@self, {"distance": 6.0, "yaw": 90.0, "pitch": -30.0})
end`, "")
	ptick(lvl, 1)
	RefreshCameraTargets(lvl)
	gfx.StepCameraRig(1, true, true)
	want := gfx.Cam.Position

	data, err := savefile.Encode(&savefile.Snapshot{Script: CaptureScriptState(nil)})
	if err != nil {
		t.Fatal(err)
	}
	gfx.ResetCameraRig()
	hooks.ScriptWantsCursor = false
	snap, err := savefile.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	RestoreScriptState(snap.Script)

	if !hooks.ScriptWantsCursor {
		t.Error("the freed cursor did not come back")
	}
	on, d, _, _ := gfx.CameraOrbiting()
	if !on || d < 5.99 || d > 6.01 {
		t.Fatalf("the orbit came back as %v at %g, want on at 6", on, d)
	}
	if gfx.Cam.Position.Sub(want).Len() > 1e-3 {
		t.Errorf("the camera came back at %v, want %v", gfx.Cam.Position, want)
	}
	// Still following the crate, not a frozen point.
	lvl.Entities[0].Position = mgl32.Vec3{4, 2, 9}
	ptick(lvl, 1)
	RefreshCameraTargets(lvl)
	gfx.StepCameraRig(1.0/60.0, true, true)
	if gfx.Cam.Position.Sub(want.Add(mgl32.Vec3{0, 0, 4})).Len() > 1e-3 {
		t.Errorf("the restored orbit did not follow the crate: %v", gfx.Cam.Position)
	}

	// A save from a game that never touched either writes nothing for them.
	gfx.ResetCameraRig()
	hooks.ScriptWantsCursor = false
	if v := CaptureScriptState(nil).View; v != nil {
		t.Errorf("an untouched camera wrote a view: %+v", v)
	}
}
