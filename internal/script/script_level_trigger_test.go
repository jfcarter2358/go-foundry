package script

// script_level_trigger_test.go — the volume that ends the level.
//
// A trigger is a Volume carrying a LoadLevel, and the whole of it is derived in
// the same pass `:enter:` is: the level change is what happens when the PLAYER
// crosses into one, and nothing else in the level can cause it. These tests
// pin all three halves of that sentence — that it fires, that it fires only for
// the player, and that it fires once rather than every frame the player stands
// inside.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/hooks"
	"foundry/internal/world"
)

// withNoPendingLoad clears any request left by an earlier test and drains
// whatever this one produces, so the package-level buffer cannot leak between
// tests. Returns nothing: read the request with hooks.TakeLevelLoadRequest.
func withNoPendingLoad(t *testing.T) {
	t.Helper()
	hooks.TakeLevelLoadRequest()
	t.Cleanup(func() { hooks.TakeLevelLoadRequest() })
}

// withPlayerAt installs an engine player, restoring whatever was there.
func withPlayerAt(t *testing.T, p mgl32.Vec3) *world.Entity {
	t.Helper()
	prev := world.PlayerEntity
	world.PlayerEntity = world.NewPlayer(p)
	t.Cleanup(func() { world.PlayerEntity = prev })
	return world.PlayerEntity
}

// levelTriggerLevel returns a level with one unscripted exit volume at the
// origin, targeting "canyon".
func levelTriggerLevel(t *testing.T) *world.Level {
	t.Helper()
	lvl := tickTestLevel(t)
	v := world.NewVolume("exit", mgl32.Vec3{0, 0, 0})
	v.HalfX, v.HalfY, v.HalfZ = 2, 2, 2
	v.LoadLevel = "canyon"
	lvl.Volumes = append(lvl.Volumes, v)
	return lvl
}

func TestLevelTriggerFiresWhenThePlayerEntersIt(t *testing.T) {
	withNoPendingLoad(t)
	lvl := levelTriggerLevel(t)
	player := withPlayerAt(t, mgl32.Vec3{10, 0, 0})

	prevRT, prevEv := *Scripts, scriptEvents
	t.Cleanup(func() { *Scripts = prevRT; scriptEvents = prevEv })
	ScriptEventsReset()
	ScriptRuntimeLoad(lvl)

	ScriptTick(lvl, 1.0/60.0, true)
	if name, ok := hooks.TakeLevelLoadRequest(); ok {
		t.Fatalf("a load for %q was requested while the player was outside", name)
	}

	player.Position = mgl32.Vec3{0, 0, 0}
	ScriptTick(lvl, 1.0/60.0, true)

	name, ok := hooks.TakeLevelLoadRequest()
	if !ok {
		t.Fatal("walking the player in requested no level load")
	}
	if name != "canyon" {
		t.Errorf("requested %q, want %q", name, "canyon")
	}

	// And not again on the next frame: entering is an edge, so a trigger the
	// player is standing in must not keep asking. (It would be harmless while
	// the load is in flight, but a trigger whose target a script has since
	// changed would then fire the new one from inside the old volume.)
	ScriptTick(lvl, 1.0/60.0, true)
	if name, ok := hooks.TakeLevelLoadRequest(); ok {
		t.Errorf("standing inside the trigger re-requested %q", name)
	}
}

func TestLevelTriggerIgnoresEverythingButThePlayer(t *testing.T) {
	// A guard on patrol crossing the exit must not end the level.
	withNoPendingLoad(t)
	lvl := levelTriggerLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "guard", Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Position: mgl32.Vec3{0, 0, 0}, Attributes: world.AttrSet{},
	})

	prevRT, prevEv := *Scripts, scriptEvents
	t.Cleanup(func() { *Scripts = prevRT; scriptEvents = prevEv })
	ScriptEventsReset()
	ScriptRuntimeLoad(lvl)

	ScriptTick(lvl, 1.0/60.0, true)
	ScriptTick(lvl, 1.0/60.0, true)
	if name, ok := hooks.TakeLevelLoadRequest(); ok {
		t.Errorf("an entity other than the player requested %q", name)
	}
}

func TestDisabledLevelTriggerDoesNotFire(t *testing.T) {
	// `enabled` is what arms and disarms a trigger, and a level exit is a
	// trigger like any other — a locked door is a disabled one.
	withNoPendingLoad(t)
	lvl := levelTriggerLevel(t)
	lvl.Volumes[0].Enabled = false
	withPlayerAt(t, mgl32.Vec3{0, 0, 0})

	prevRT, prevEv := *Scripts, scriptEvents
	t.Cleanup(func() { *Scripts = prevRT; scriptEvents = prevEv })
	ScriptEventsReset()
	ScriptRuntimeLoad(lvl)

	ScriptTick(lvl, 1.0/60.0, true)
	ScriptTick(lvl, 1.0/60.0, true)
	if name, ok := hooks.TakeLevelLoadRequest(); ok {
		t.Errorf("a disabled trigger requested %q", name)
	}
}

func TestLevelTriggerRunsTheVolumeScriptToo(t *testing.T) {
	// A trigger may carry both. The handler runs in the frame the load is
	// requested — the load itself happens between frames — which is what makes
	// "save progress, then move on" writable as one volume.
	withNoPendingLoad(t)
	root := withTempScriptDir(t)
	path := writeScript(t, root, "exit.flang", `def on_enter(:enter:, other ref) -> null
    @self.attr.entered += 1
end`)

	lvl := levelTriggerLevel(t)
	lvl.Volumes[0].Script = path
	lvl.Volumes[0].SetAttr("entered", world.IntAttr(0))
	withPlayerAt(t, mgl32.Vec3{0, 0, 0})

	prevRT, prevEv := *Scripts, scriptEvents
	t.Cleanup(func() { *Scripts = prevRT; scriptEvents = prevEv })
	ScriptEventsReset()
	ScriptRuntimeLoad(lvl)
	ScriptTick(lvl, 1.0/60.0, true)

	if a, _ := lvl.Volumes[0].Attr("entered"); a.I != 1 {
		t.Errorf("the volume's own enter handler fired %d times, want 1", a.I)
	}
	if _, ok := hooks.TakeLevelLoadRequest(); !ok {
		t.Error("a scripted trigger requested no level load")
	}
}
