package script

// script_tick_test.go — the event loop, end to end.
//
// The other script tests exercise one layer each: the language against a fake
// world, the host against a real level. This drives the whole thing — derive
// events, post them, drain to a fixpoint, apply the deferred work — because the
// seams between those layers are where a scripting engine actually breaks.
//
// Headless throughout. Scripts are written to a temp scripts directory rather
// than read from resources/, so a test cannot be broken by an example being
// edited, and editing an example cannot be broken by a test.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/fileutil"
	"foundry/internal/flang"
	"foundry/internal/leveltest"
	"foundry/internal/testenv"
	"foundry/internal/world"
)

// withTempScriptDir points the scripts folder at a temp directory, in the style
// of withTempTextureDir.
func withTempScriptDir(t *testing.T) string {
	t.Helper()
	prev := fileutil.ExecutablePath
	dir := t.TempDir()
	fileutil.ExecutablePath = dir
	t.Cleanup(func() { fileutil.ExecutablePath = prev })

	root := filepath.Join(dir, fileutil.ScriptsDirName)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// writeScript puts a script in the temp scripts folder and returns the path a
// level would name it by.
func writeScript(t *testing.T, root, name, src string) string {
	t.Helper()
	full := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return fileutil.ScriptsDirName + "/" + name
}

// tickTestLevel builds a level with a scripted crate and a scripted trigger.
func tickTestLevel(t *testing.T) *world.Level {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	ch := leveltest.EdgeIDChunk(t)
	ch.ID = "chunk"
	return &world.Level{Chunks: []world.Chunk{*ch}}
}

// runTicks binds the runtime to lvl, loads its scripts, and runs n frames.
func runTicks(t *testing.T, lvl *world.Level, n int) {
	t.Helper()
	prevRT := *Scripts
	prevEv := scriptEvents
	t.Cleanup(func() { *Scripts = prevRT; scriptEvents = prevEv })

	ScriptEventsReset()
	ScriptRuntimeLoad(lvl)
	for i := 0; i < n; i++ {
		ScriptTick(lvl, 1.0/60.0, true)
	}
}

// ---------- the loop ----------

func TestSpawnFiresOncePerObject(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "counter.flang", `def on_spawn(:spawn:) -> null
    @self.attr.spawns += 1
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "crate", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Attributes: world.AttrSet{"spawns": world.IntAttr(0)},
	})

	runTicks(t, lvl, 5)

	a, _ := lvl.Entities[0].Attr("spawns")
	if a.I != 1 {
		t.Errorf("spawn fired %d times over 5 frames, want 1", a.I)
	}
}

func TestTickFiresEveryFrame(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "ticker.flang", `def on_tick(:tick:, dt float) -> null
    @self.attr.ticks += 1
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "crate", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Attributes: world.AttrSet{"ticks": world.IntAttr(0)},
	})

	runTicks(t, lvl, 5)

	a, _ := lvl.Entities[0].Attr("ticks")
	if a.I != 5 {
		t.Errorf("tick fired %d times over 5 frames, want 5", a.I)
	}
}

// A guard on an event handler is how a script says "only on the frames where
// this is true". The frames where it is not are the expected case, not an error
// — before this was fixed, a guarded `:tick:` handler logged an error on every
// frame its guard declined and was disabled by the repeated-failure rule within
// a second of play.
func TestGuardedTickHandlerDoesNotErrorOnDeclinedFrames(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "door.flang", `def on_tick(:tick:, dt float) when @self.attr.opening == true -> null
    @self.attr.ticks += 1
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "door", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Attributes: world.AttrSet{"opening": world.BoolAttr(false), "ticks": world.IntAttr(0)},
	})

	runTicks(t, lvl, 5)

	if n := ScriptStatsSnapshot().Errors; n != 0 {
		t.Errorf("%d error(s) from a guard that simply declined", n)
	}
	if a, _ := lvl.Entities[0].Attr("ticks"); a.I != 0 {
		t.Errorf("the guarded body ran %d times with its guard false", a.I)
	}

	// The same handler still fires once the guard holds.
	lvl.Entities[0].SetAttr("opening", world.BoolAttr(true))
	runTicks(t, lvl, 3)

	if n := ScriptStatsSnapshot().Errors; n != 0 {
		t.Errorf("%d error(s) with the guard satisfied", n)
	}
	if a, _ := lvl.Entities[0].Attr("ticks"); a.I != 3 {
		t.Errorf("tick fired %d times over 3 frames, want 3", a.I)
	}
}

func TestVolumeEnterAndExit(t *testing.T) {
	// The event the whole volume type exists for.
	root := withTempScriptDir(t)
	path := writeScript(t, root, "trigger.flang", `def on_enter(:enter:, other ref) -> null
    @self.attr.entered += 1
end
def on_exit(:exit:, other ref) -> null
    @self.attr.exited += 1
end`)

	lvl := tickTestLevel(t)
	v := world.NewVolume("trigger", mgl32.Vec3{0, 0, 0})
	v.HalfX, v.HalfY, v.HalfZ = 2, 2, 2
	v.Script = path
	v.SetAttr("entered", world.IntAttr(0))
	v.SetAttr("exited", world.IntAttr(0))
	lvl.Volumes = append(lvl.Volumes, v)

	// Outside to start with.
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "walker", Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Position: mgl32.Vec3{10, 0, 0}, Attributes: world.AttrSet{},
	})

	prevRT := *Scripts
	prevEv := scriptEvents
	t.Cleanup(func() { *Scripts = prevRT; scriptEvents = prevEv })
	ScriptEventsReset()
	ScriptRuntimeLoad(lvl)

	ScriptTick(lvl, 1.0/60.0, true)
	if a, _ := lvl.Volumes[0].Attr("entered"); a.I != 0 {
		t.Fatalf("entered fired while the walker was outside")
	}

	// Walk in. Two frames: one to enter, one to confirm it does not re-fire.
	lvl.Entities[0].Position = mgl32.Vec3{0, 0, 0}
	ScriptTick(lvl, 1.0/60.0, true)
	ScriptTick(lvl, 1.0/60.0, true)
	if a, _ := lvl.Volumes[0].Attr("entered"); a.I != 1 {
		t.Errorf("entered fired %d times, want 1 — enter must be an edge, not a state", a.I)
	}

	// Walk out.
	lvl.Entities[0].Position = mgl32.Vec3{10, 0, 0}
	ScriptTick(lvl, 1.0/60.0, true)
	if a, _ := lvl.Volumes[0].Attr("exited"); a.I != 1 {
		t.Errorf("exited fired %d times, want 1", a.I)
	}
}

func TestMessageCascadeResolvesInOneFrame(t *testing.T) {
	// The reason delivery drains to a fixpoint: `hit → damage → die` has to
	// resolve within the frame, not over three variable-length ones.
	root := withTempScriptDir(t)
	path := writeScript(t, root, "chain.flang", `def on_spawn(:spawn:) -> null
    @self <- (:step_a:,)
end
def on_a(:step_a:) -> null
    @self.attr.stage = 1
    @self <- (:step_b:,)
end
def on_b(:step_b:) -> null
    @self.attr.stage = 2
    @self <- (:step_c:,)
end
def on_c(:step_c:) -> null
    @self.attr.stage = 3
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "chain", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Attributes: world.AttrSet{"stage": world.IntAttr(0)},
	})

	runTicks(t, lvl, 1)

	a, _ := lvl.Entities[0].Attr("stage")
	if a.I != 3 {
		t.Errorf("the cascade reached stage %d in one frame, want 3", a.I)
	}
}

func TestBroadcastReachesEveryTaggedObject(t *testing.T) {
	root := withTempScriptDir(t)
	sender := writeScript(t, root, "sender.flang", `def on_spawn(:spawn:) -> null
    #breakable <- (:damage:, @self, 5)
end`)
	target := writeScript(t, root, "target.flang", `def on_damage(:damage:, src ref, amount int) -> null
    @self.attr.health -= amount
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities,
		world.Entity{ID: "gun", Script: sender, Visible: true, Scale: mgl32.Vec3{1, 1, 1}, Attributes: world.AttrSet{}},
		world.Entity{ID: "a", Script: target, Tags: []string{"breakable"}, Visible: true,
			Scale: mgl32.Vec3{1, 1, 1}, Attributes: world.AttrSet{"health": world.IntAttr(10)}},
		world.Entity{ID: "b", Script: target, Tags: []string{"breakable"}, Visible: true,
			Scale: mgl32.Vec3{1, 1, 1}, Attributes: world.AttrSet{"health": world.IntAttr(10)}},
		world.Entity{ID: "c", Script: target, Visible: true,
			Scale: mgl32.Vec3{1, 1, 1}, Attributes: world.AttrSet{"health": world.IntAttr(10)}},
	)

	runTicks(t, lvl, 1)

	for _, id := range []string{"a", "b"} {
		e := world.FindEntity(lvl, id)
		if a, _ := e.Attr("health"); a.I != 5 {
			t.Errorf("%s has %d health, want 5", id, a.I)
		}
	}
	// The untagged one must not have been hit.
	if a, _ := world.FindEntity(lvl, "c").Attr("health"); a.I != 10 {
		t.Errorf("an untagged entity was hit: %d health", a.I)
	}
}

func TestPingPongIsBoundedRatherThanHanging(t *testing.T) {
	// The hop cap. Without it, drain-to-fixpoint would spin forever; with it,
	// the frame ends and the work is deferred rather than lost.
	root := withTempScriptDir(t)
	path := writeScript(t, root, "pong.flang", `def on_spawn(:spawn:) -> null
    @self <- (:ping:,)
end
def on_ping(:ping:) -> null
    @self.attr.count += 1
    @self <- (:ping:,)
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "pong", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Attributes: world.AttrSet{"count": world.IntAttr(0)},
	})

	// If this hangs, the cap is broken — which is exactly the failure worth
	// having a test for.
	runTicks(t, lvl, 1)

	a, _ := lvl.Entities[0].Attr("count")
	if a.I == 0 {
		t.Fatal("the loop never ran at all")
	}
	if a.I > scriptMaxHops+2 {
		t.Errorf("the loop ran %d times in one frame; the hop cap is %d", a.I, scriptMaxHops)
	}
	if Scripts.stats.Deferred == 0 {
		t.Error("nothing was deferred; the capped message should carry to next frame, not vanish")
	}
}

func TestBrokenScriptIsReportedAndDisabled(t *testing.T) {
	// A script that will not compile must not half-run: dispatching into an
	// unparsed program would be behaviour nobody could predict from the source.
	root := withTempScriptDir(t)
	path := writeScript(t, root, "broken.flang", "def oops( -> null\n    return\nend")

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "crate", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1}, Attributes: world.AttrSet{},
	})

	runTicks(t, lvl, 2)

	if diags := ScriptDiagnostics(path); len(diags) == 0 {
		t.Error("a broken script produced no diagnostics")
	}
	if u := Scripts.units[path]; u == nil || !u.disabled {
		t.Error("a broken script was not disabled")
	}
}

func TestRuntimeErrorDisablesAfterRepeatedFailures(t *testing.T) {
	// A handler erroring every frame fills the log and slows the game without
	// ever telling the author anything new.
	root := withTempScriptDir(t)
	path := writeScript(t, root, "boom.flang", `def on_tick(:tick:, dt float) -> null
    x = 1 / 0
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "crate", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1}, Attributes: world.AttrSet{},
	})

	runTicks(t, lvl, scriptMaxErrorsPerScript+2)

	u := Scripts.units[path]
	if u == nil || !u.disabled {
		t.Error("a repeatedly-failing script was not disabled")
	}
}

func TestScriptWritesGeometryThroughTheDeferredQueue(t *testing.T) {
	// A height write is GL work, so it must be recorded during the tick and
	// applied by DrainScriptWork — never inline.
	root := withTempScriptDir(t)
	path := writeScript(t, root, "raise.flang", `def on_spawn(:spawn:) -> null
    @room.floor.prop.height = 4.0
end`)

	lvl := tickTestLevel(t)
	lvl.Scripts = []string{path}

	prevRT := *Scripts
	prevEv := scriptEvents
	t.Cleanup(func() { *Scripts = prevRT; scriptEvents = prevEv })
	ScriptEventsReset()
	ScriptRuntimeLoad(lvl)
	ScriptTick(lvl, 1.0/60.0, true)

	if lvl.Chunks[0].Sectors[0].Floor.Height != 4 {
		t.Errorf("the floor height is %v, want 4", lvl.Chunks[0].Sectors[0].Floor.Height)
	}
	if len(Scripts.dirtySectors) != 1 {
		t.Fatalf("got %d queued rebuilds, want 1", len(Scripts.dirtySectors))
	}
	DrainScriptWork(lvl)
	if len(Scripts.dirtySectors) != 0 {
		t.Error("DrainScriptWork left rebuilds queued")
	}
}

func TestScriptsDoNotRunWhileEditing(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "ticker.flang", `def on_tick(:tick:, dt float) -> null
    @self.attr.ticks += 1
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "crate", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Attributes: world.AttrSet{"ticks": world.IntAttr(0)},
	})

	prevRT := *Scripts
	t.Cleanup(func() { *Scripts = prevRT })
	ScriptRuntimeLoad(lvl)
	for i := 0; i < 5; i++ {
		ScriptTick(lvl, 1.0/60.0, false) // play == false
	}

	if a, _ := lvl.Entities[0].Attr("ticks"); a.I != 0 {
		t.Errorf("a script ran %d times while the editor was up", a.I)
	}
}

func TestTimerDeliversAfterItsDelay(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "timer.flang", `def on_spawn(:spawn:) -> null
    @self <- after(100.0, :later:)
end
def on_later(:later:) -> null
    @self.attr.fired = 1
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "crate", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Attributes: world.AttrSet{"fired": world.IntAttr(0)},
	})

	prevRT := *Scripts
	prevEv := scriptEvents
	t.Cleanup(func() { *Scripts = prevRT; scriptEvents = prevEv })
	ScriptEventsReset()
	ScriptRuntimeLoad(lvl)

	// One frame arms the timer; it must not have fired yet at 1/60 s.
	ScriptTick(lvl, 1.0/60.0, true)
	if a, _ := lvl.Entities[0].Attr("fired"); a.I != 0 {
		t.Fatal("the timer fired immediately")
	}
	// Well past 100 ms.
	for i := 0; i < 10; i++ {
		ScriptTick(lvl, 1.0/60.0, true)
	}
	if a, _ := lvl.Entities[0].Attr("fired"); a.I != 1 {
		t.Errorf("the timer did not fire: %v", a.I)
	}
}

// The point of the send form: a timer can be aimed at something other than the
// object that set it, so one script can arrange another's behaviour later.
func TestTimerDeliversToAnotherObject(t *testing.T) {
	root := withTempScriptDir(t)
	sender := writeScript(t, root, "sender.flang", `def on_spawn(:spawn:) -> null
    @crate <- after(100.0, :later:)
end`)
	receiver := writeScript(t, root, "receiver.flang", `def on_later(:later:) -> null
    @self.attr.fired = 1
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities,
		world.Entity{
			ID: "crate", Script: receiver, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
			Attributes: world.AttrSet{"fired": world.IntAttr(0)},
		},
		world.Entity{
			ID: "clock", Script: sender, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		},
	)

	prevRT := *Scripts
	prevEv := scriptEvents
	t.Cleanup(func() { *Scripts = prevRT; scriptEvents = prevEv })
	ScriptEventsReset()
	ScriptRuntimeLoad(lvl)

	for i := 0; i < 11; i++ {
		ScriptTick(lvl, 1.0/60.0, true)
	}
	if a, _ := lvl.Entities[0].Attr("fired"); a.I != 1 {
		t.Errorf("the crate never heard the timer the clock set: %v", a.I)
	}
}

func TestImportsResolveThroughTheScriptsRoot(t *testing.T) {
	root := withTempScriptDir(t)
	writeScript(t, root, "shared/helpers.flang", `def doubled(n int) -> int
    return n * 2
end`)
	path := writeScript(t, root, "user.flang", `import("shared/helpers.flang")
def on_spawn(:spawn:) -> null
    @self.attr.value = doubled(21)
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "crate", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Attributes: world.AttrSet{"value": world.IntAttr(0)},
	})

	runTicks(t, lvl, 1)

	if a, _ := lvl.Entities[0].Attr("value"); a.I != 42 {
		t.Errorf("the imported function did not run: value is %v", a.I)
	}
}

func TestImportCannotEscapeTheScriptsRoot(t *testing.T) {
	// A script tree that can read arbitrary files becomes dangerous the moment
	// someone hands you a level.
	root := withTempScriptDir(t)
	_, err := ResolveScriptImport(filepath.Join(root, "a.flang"), "../../../etc/passwd")
	if err == nil {
		t.Fatal("an import escaped the scripts folder")
	}
	if !strings.Contains(err.Error(), "leaves the scripts folder") {
		t.Errorf("diagnostic does not explain the refusal: %v", err)
	}
}

func TestSpawnAndDestroyThroughAScript(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "reaper.flang", `def on_spawn(:spawn:) -> null
    destroy(@victim)
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities,
		world.Entity{ID: "reaper", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1}, Attributes: world.AttrSet{}},
		world.Entity{ID: "victim", Visible: true, Scale: mgl32.Vec3{1, 1, 1}, Attributes: world.AttrSet{}},
	)

	runTicks(t, lvl, 1)

	if world.FindEntity(lvl, "victim") != nil {
		t.Error("destroy did not remove the entity")
	}
	if world.FindEntity(lvl, "reaper") == nil {
		t.Error("destroy removed the wrong entity")
	}
}

func TestSendingToADestroyedObjectIsDropped(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "ghost.flang", `def on_tick(:tick:, dt float) -> null
    @gone <- (:hello:,)
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "sender", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1}, Attributes: world.AttrSet{},
	})

	// Must not panic, and must not disable the script: an object may legitimately
	// have been destroyed since the sender last looked.
	runTicks(t, lvl, 3)

	if u := Scripts.units[path]; u != nil && u.disabled {
		t.Error("sending to a missing object disabled the script")
	}
}

func TestRefsSurviveALevelRebuild(t *testing.T) {
	// The reason a ref is a path of names: undo rebuilds the level wholesale,
	// and anything positional would dangle.
	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "crate", Visible: true, Scale: mgl32.Vec3{1, 1, 1}, Attributes: world.AttrSet{},
	})

	prevRT := *Scripts
	t.Cleanup(func() { *Scripts = prevRT })
	ScriptRuntimeReset(lvl)
	r, ok := Scripts.host.Resolve("crate")
	if !ok {
		t.Fatal("crate did not resolve")
	}

	// Rebuild the entity slice with a different backing array — the same thing
	// rawToLevel does — and the ref must still resolve.
	rebuilt := make([]world.Entity, 0, 4)
	rebuilt = append(rebuilt, world.Entity{ID: "other"}, lvl.Entities[0])
	lvl.Entities = rebuilt
	Scripts.cache = buildRefCache(lvl)

	if !Scripts.host.Alive(r) {
		t.Error("a ref did not survive the level's entity slice being rebuilt")
	}
	if got, _ := Scripts.host.Attr(r, "nothing"); got != nil {
		t.Errorf("an absent attribute gave %v", got)
	}
}

func TestStatsCountDeliveries(t *testing.T) {
	// The F3 overlay reads these; a counter that never moved would make the
	// overlay lie about whether scripts are running.
	root := withTempScriptDir(t)
	path := writeScript(t, root, "ticker.flang", `def on_tick(:tick:, dt float) -> null
    @self.attr.n += 1
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "crate", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Attributes: world.AttrSet{"n": world.IntAttr(0)},
	})

	runTicks(t, lvl, 1)

	if s := ScriptStatsSnapshot(); s.Delivered == 0 {
		t.Errorf("stats report no deliveries: %+v", s)
	}
}

func TestUnhandledAtomIsNotAnError(t *testing.T) {
	// A broadcast to a tag whose members mostly do not care is the normal case.
	root := withTempScriptDir(t)
	path := writeScript(t, root, "quiet.flang", `def on_spawn(:spawn:) -> null
    @self.attr.n = 1
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "crate", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1}, Attributes: world.AttrSet{},
	})

	// `tick` fires every frame and nothing handles it.
	runTicks(t, lvl, 3)

	if u := Scripts.units[path]; u != nil && u.failures > 0 {
		t.Errorf("an unhandled atom counted as %d failures", u.failures)
	}
	if a, _ := lvl.Entities[0].Attr("n"); a.I != 1 {
		t.Error("the handler that does exist did not run")
	}
}

func TestPropertyCatalogueMatchesTheDocumentation(t *testing.T) {
	// SCRIPTING.md lists a core surface per kind. If the table and the document
	// drift, the document is what an author trusts and the table is what runs.
	mustHave := map[flang.Kind_][]string{
		flang.RefEntity:  {"position", "rotation", "velocity", "visible", "on_ground"},
		flang.RefVolume:  {"position", "half", "shape", "enabled"},
		flang.RefEdge:    {"kind", "texture", "collision", "visible", "alpha"},
		flang.RefFloor:   {"enabled", "hidden", "height", "texture"},
		flang.RefCeiling: {"enabled", "height", "texture"},
		flang.RefLight:   {"color", "brightness", "cast_shadows"},
	}
	for kind, names := range mustHave {
		tbl := propTables[kind]
		for _, n := range names {
			if _, ok := tbl[n]; !ok {
				t.Errorf("%s has no %q property, but SCRIPTING.md lists one", kind, n)
			}
		}
	}
	// A ceiling has no `hidden`; a floor does. The document says so, and a table
	// that grew one would make the two disagree.
	if _, ok := propTables[flang.RefCeiling]["hidden"]; ok {
		t.Error("ceilings gained a `hidden` property; only floors have one")
	}
}

// ---------- animating geometry ----------

func TestDescendingFloorStaysFindable(t *testing.T) {
	// A lift is the obvious thing to script, and it exercises the one path that
	// rewrites geometry every frame.
	//
	// The bug this guards: FindChunkAndSectorSH tells stacked chunks apart by the
	// chunk's Y bounds, and those are recorded when the level loads. A floor a
	// script lowers past the recorded minimum stops being found at all — the
	// player rides the lift down and falls out of the world.
	//
	// Downward rather than upward because a rising floor stays inside the bounds
	// its own ceiling already established; going down is what leaves them.
	root := withTempScriptDir(t)
	path := writeScript(t, root, "lift.flang", `def on_tick(:tick:, dt float) -> null
    h = @self.floor.prop.height
    if h > -5.0
        @self.floor.prop.height = h - 0.5
    end
end`)

	lvl := tickTestLevel(t)
	lvl.Chunks[0].Sectors[0].Script = path
	// Settle the level the way loading would, so the bounds start honest.
	DrainScriptWorkSettle(t, lvl)
	if lvl.Chunks[0].MinBound.Y() > 0.01 {
		t.Fatalf("the chunk starts at Y %v; the test assumes it starts at 0", lvl.Chunks[0].MinBound.Y())
	}

	prevRT := *Scripts
	prevEv := scriptEvents
	t.Cleanup(func() { *Scripts = prevRT; scriptEvents = prevEv })
	ScriptEventsReset()
	ScriptRuntimeLoad(lvl)

	for i := 0; i < 20; i++ {
		ScriptTick(lvl, 0.016, true)
		DrainScriptWork(lvl)
	}

	h := lvl.Chunks[0].Sectors[0].Floor.Height
	if h > -4.9 {
		t.Fatalf("the floor only reached %v; the animation did not run", h)
	}
	if lvl.Chunks[0].MinBound.Y() > h+0.01 {
		t.Errorf("the chunk's Y bounds stop at %v but its floor is at %v",
			lvl.Chunks[0].MinBound.Y(), h)
	}
	if ch, s := world.FindChunkAndSectorSH(lvl.Spatial, lvl.Chunks, mgl32.Vec3{5, h + 0.1, 5}); ch == nil || s == nil {
		t.Error("standing on the lowered floor finds no sector — the player would fall out of the world")
	}
}

func TestAnimatingAFloorDoesNotRebuildTheSpatialHash(t *testing.T) {
	// The hash is indexed by XZ alone, and nothing a script can write moves a
	// sector's footprint. Rebuilding a whole-level structure every frame an
	// animated floor moves would make the obvious thing to script the expensive
	// thing to
	root := withTempScriptDir(t)
	path := writeScript(t, root, "lift.flang", `def on_tick(:tick:, dt float) -> null
    @self.floor.prop.height = @self.floor.prop.height - 0.1
end`)

	lvl := tickTestLevel(t)
	lvl.Chunks[0].Sectors[0].Script = path

	prevRT := *Scripts
	prevEv := scriptEvents
	t.Cleanup(func() { *Scripts = prevRT; scriptEvents = prevEv })
	ScriptEventsReset()
	ScriptRuntimeLoad(lvl)

	// One frame to settle: the hand-built test sector has no footprint recorded
	// until something loads it, and that first genuine change is allowed to
	// rebuild the hash.
	ScriptTick(lvl, 0.016, true)
	DrainScriptWork(lvl)
	lvl.Spatial = world.BuildSpatialHash(lvl.Chunks, 8.0)
	before := lvl.Spatial

	for i := 0; i < 5; i++ {
		ScriptTick(lvl, 0.016, true)
		DrainScriptWork(lvl)
	}
	if lvl.Spatial != before {
		t.Error("the spatial hash was rebuilt for a change that cannot affect it")
	}
}

// DrainScriptWorkSettle brings a hand-built test level to the state loading
// would have left it in: sector bounds computed, chunk bounds rolled up, and a
// spatial hash over the result.
func DrainScriptWorkSettle(t *testing.T, lvl *world.Level) {
	t.Helper()
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		for si := range ch.Sectors {
			_ = world.ReloadSector(ch, &ch.Sectors[si])
		}
		ch.RecomputeBounds()
	}
	lvl.Spatial = world.BuildSpatialHash(lvl.Chunks, 8.0)
}

// ---------- what a delete would break ----------

// The script editor asks ScriptUsage before deleting a file. It has to answer
// for both ways a script is reachable — the level naming it, and another script
// importing it — because only the first is written down anywhere the editor
// could look for itself.
func TestScriptUsageFindsNamedAndImported(t *testing.T) {
	root := withTempScriptDir(t)
	writeScript(t, root, "shared/helpers.flang", `def doubled(n int) -> int
    return n * 2
end`)
	writeScript(t, root, "unused.flang", `def unused() -> int
    return 1
end`)
	path := writeScript(t, root, "user.flang", `import("shared/helpers.flang")
def on_spawn(:spawn:) -> null
    @self.attr.value = doubled(21)
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "crate", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Attributes: world.AttrSet{"value": world.IntAttr(0)},
	})
	runTicks(t, lvl, 1)

	named, importedBy := ScriptUsage(path)
	if !named {
		t.Error("the script the entity names was not reported as named")
	}
	if len(importedBy) != 0 {
		t.Errorf("nothing imports it, but ScriptUsage says %v", importedBy)
	}

	named, importedBy = ScriptUsage("scripts/shared/helpers.flang")
	if named {
		t.Error("nothing in the level names the imported script")
	}
	if len(importedBy) != 1 || importedBy[0] != path {
		t.Errorf("importedBy = %v, want just %s", importedBy, path)
	}

	// A file nothing has loaded is the ordinary thing to delete, and it must
	// come back clean rather than being warned about.
	if named, importedBy = ScriptUsage("scripts/unused.flang"); named || len(importedBy) != 0 {
		t.Errorf("an unused script reported named=%v importedBy=%v", named, importedBy)
	}
}

// A level may name a script with or without the scripts/ prefix — a warning
// missed over a prefix is a file deleted out from under a level.
func TestScriptUsageComparesResolvedPaths(t *testing.T) {
	root := withTempScriptDir(t)
	writeScript(t, root, "door.flang", `def on_spawn(:spawn:) -> null
    @self.attr.value = 1
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "crate", Script: "door", Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Attributes: world.AttrSet{"value": world.IntAttr(0)},
	})
	runTicks(t, lvl, 1)

	for _, name := range []string{"door", "door.flang", "scripts/door.flang"} {
		if named, _ := ScriptUsage(name); !named {
			t.Errorf("ScriptUsage(%q) did not recognise the script the level names", name)
		}
	}
}
