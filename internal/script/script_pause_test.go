package script

// script_pause_test.go — `@level.prop.paused`, and the two screen events a
// pause menu needs in order to lift one.
//
// The pause is the hard one: the world stops, and so do the level's scripts —
// no `:tick:`, no derived events, no timers coming due, no `time()` moving —
// while the screen stack keeps running, because the menu that will unpause it
// is made of scripts. What has to hold is exactly that split.
//
// The `:hidden:` and `:cancel:` tests are here rather than beside the other UI
// events because this is what made them matter. Both are recorded as the screen
// leaves the stack and delivered on the next tick, by which time the screen is
// no longer anything a ref resolves to — so both were dropped, silently, and a
// pause menu lifting the pause on `:hidden:` would have left Escape freezing
// the game for good.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/ui"
	"foundry/internal/world"
)

// pauseTestReader is an unscripted entity with somewhere for a handler to
// write, so a handler on a screen that has since gone can still be observed.
func pauseTestReader(attrs ...string) world.Entity {
	set := world.AttrSet{}
	for _, a := range attrs {
		set[a] = world.FloatAttr(0)
	}
	return world.Entity{ID: "reader", Visible: true, Scale: mgl32.Vec3{1, 1, 1}, Attributes: set}
}

func readerAttr(t *testing.T, lvl *world.Level, name string) float64 {
	t.Helper()
	e := world.FindEntity(lvl, "reader")
	if e == nil {
		t.Fatal("the reader entity is gone")
	}
	a, ok := e.Attr(name)
	if !ok {
		t.Fatalf("the reader has no attribute %q", name)
	}
	return a.F
}

func tick(lvl *world.Level, n int) {
	for i := 0; i < n; i++ {
		ScriptTick(lvl, 1.0/60.0, true)
	}
}

func TestHiddenReachesTheScreenThatWasTakenOff(t *testing.T) {
	root := withTempScriptDir(t)
	st := withUIStack(t)
	path := writeScript(t, root, "menu.flang", `def on_hidden(:hidden:) -> null
    @reader.attr.hidden = 1.0
end`)

	s := ui.NewScreen("pause")
	s.Script = path
	st.Push(s)

	lvl := tickTestLevel(t)
	lvl.Entities = []world.Entity{pauseTestReader("hidden")}
	runTicks(t, lvl, 1)

	st.Hide("pause")
	tick(lvl, 2)

	if got := readerAttr(t, lvl, "hidden"); got != 1 {
		t.Error(":hidden: never reached the screen's script: it was dropped because the screen had already left the stack")
	}
}

func TestCancelReachesTheScreenEscapeClosed(t *testing.T) {
	root := withTempScriptDir(t)
	st := withUIStack(t)
	path := writeScript(t, root, "menu.flang", `def on_cancel(:cancel:) -> null
    @reader.attr.cancelled = 1.0
end`)

	s := ui.NewScreen("pause")
	s.Script = path
	s.Modal = true
	st.Push(s)

	lvl := tickTestLevel(t)
	lvl.Entities = []world.Entity{pauseTestReader("cancelled")}
	runTicks(t, lvl, 1)

	if !st.Cancel() {
		t.Fatal("Escape did not close the modal screen")
	}
	tick(lvl, 2)

	if got := readerAttr(t, lvl, "cancelled"); got != 1 {
		t.Error(":cancel: never reached the screen Escape closed")
	}
}

// spawn and destroy rebuild the ref cache partway through a drain. :spawn: is
// posted ahead of UI events, so a level whose :spawn: destroys something
// rebuilds the cache before the :hidden: in the same tick is delivered — and a
// departed screen handed only to the first cache of the tick is lost with it.
func TestHiddenSurvivesACacheRebuildEarlierInTheDrain(t *testing.T) {
	root := withTempScriptDir(t)
	st := withUIStack(t)
	menu := writeScript(t, root, "menu.flang", `def on_hidden(:hidden:) -> null
    @reader.attr.hidden = 1.0
end`)
	level := writeScript(t, root, "lvl.flang", `def on_spawn(:spawn:) -> null
    destroy(@victim)
end`)

	lvl := tickTestLevel(t)
	lvl.Scripts = []string{level}
	victim := world.Entity{ID: "victim", Visible: true, Scale: mgl32.Vec3{1, 1, 1}}
	lvl.Entities = []world.Entity{pauseTestReader("hidden"), victim}

	prevRT := *Scripts
	prevEv := scriptEvents
	t.Cleanup(func() {
		*Scripts = prevRT
		scriptEvents = prevEv
		ui.OnScreenShown, ui.OnScreenHidden = nil, nil
	})
	ScriptRuntimeLoad(lvl)
	ScriptEventsReset()

	// Up and straight back down before the first tick, so that tick carries
	// both the level's :spawn: and the screen's :hidden:.
	s := ui.NewScreen("pause")
	s.Script = menu
	st.Push(s)
	st.Hide("pause")
	tick(lvl, 2)

	if world.FindEntity(lvl, "victim") != nil {
		t.Fatal("the level's :spawn: did not destroy the victim, so nothing rebuilt the cache")
	}
	if got := readerAttr(t, lvl, "hidden"); got != 1 {
		t.Error(":hidden: was dropped after a verb rebuilt the ref cache earlier in the same drain")
	}
}

// The screen being gone for one tick must not make it look up for that tick:
// it is reachable to hear its own departure, not to be ticked.
func TestAHiddenScreenDoesNotTick(t *testing.T) {
	root := withTempScriptDir(t)
	st := withUIStack(t)
	path := writeScript(t, root, "menu.flang", `def on_tick(:tick:, dt) -> null
    @reader.attr.ticks += 1
end`)

	s := ui.NewScreen("pause")
	s.Script = path
	st.Push(s)

	lvl := tickTestLevel(t)
	lvl.Entities = []world.Entity{pauseTestReader("ticks")}
	runTicks(t, lvl, 1)
	before := readerAttr(t, lvl, "ticks")

	st.Hide("pause")
	tick(lvl, 3)

	if got := readerAttr(t, lvl, "ticks"); got != before {
		t.Errorf("a screen off the stack ticked %v more times", got-before)
	}
}

// ---------- the pause ----------

func TestPauseStopsTheLevelsScripts(t *testing.T) {
	root := withTempScriptDir(t)
	withUIStack(t)
	path := writeScript(t, root, "lvl.flang", `def on_spawn(:spawn:) -> null
    @level <- after(100, :ring:)
end

def on_tick(:tick:, dt) -> null
    @reader.attr.ticks += 1.0
end

def on_ring(:ring:) -> null
    @reader.attr.rung = 1.0
end`)

	lvl := tickTestLevel(t)
	lvl.Scripts = []string{path}
	lvl.Entities = []world.Entity{pauseTestReader("ticks", "rung")}
	runTicks(t, lvl, 1)

	scriptEvents.paused = true
	ticks, clock := readerAttr(t, lvl, "ticks"), scriptElapsed()
	// Half a second: the timer would have come due five times over.
	tick(lvl, 30)

	if got := readerAttr(t, lvl, "ticks"); got != ticks {
		t.Errorf("the level ticked %v times while paused", got-ticks)
	}
	if readerAttr(t, lvl, "rung") != 0 {
		t.Error("a level timer came due while paused")
	}
	if got := scriptElapsed(); got != clock {
		t.Errorf("time() moved from %v to %v while paused", clock, got)
	}

	scriptEvents.paused = false
	tick(lvl, 10)
	if readerAttr(t, lvl, "ticks") == ticks {
		t.Error("the level did not tick again once the pause lifted")
	}
	if readerAttr(t, lvl, "rung") != 1 {
		t.Error("the timer was lost across the pause rather than held")
	}
}

// The menu that lifts the pause is made of scripts, so everything on the stack
// has to keep running under it — its ticks, its clicks, and its own timers.
func TestThePauseLeavesTheScreenStackRunning(t *testing.T) {
	root := withTempScriptDir(t)
	st := withUIStack(t)
	path := writeScript(t, root, "menu.flang", `def on_tick(:tick:, dt) -> null
    @reader.attr.ticks += 1.0
end

def on_click(:click:, src) -> null
    @self <- after(100, :ring:)
end

def on_ring(:ring:) -> null
    @reader.attr.rung = 1.0
end`)

	// The button has no script of its own, so its click bubbles to the screen.
	s := screenWithButton("menu", "")
	s.Script = path
	st.Push(s)
	st.Tick(0, 800, 600)

	lvl := tickTestLevel(t)
	lvl.Entities = []world.Entity{pauseTestReader("ticks", "rung")}
	runTicks(t, lvl, 1)

	scriptEvents.paused = true
	ticks := readerAttr(t, lvl, "ticks")
	st.PointerDown(50, 20)
	st.PointerUp(50, 20)
	tick(lvl, 30)

	if readerAttr(t, lvl, "ticks") <= ticks {
		t.Error("a screen stopped ticking under the pause")
	}
	if readerAttr(t, lvl, "rung") != 1 {
		t.Error("a click or a screen's own timer did not get through the pause")
	}
}

func TestAScriptPausesAndALevelLoadLiftsIt(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "lvl.flang", `def on_spawn(:spawn:) -> null
    @level.prop.paused = true
end`)

	lvl := tickTestLevel(t)
	lvl.Scripts = []string{path}
	runTicks(t, lvl, 1)

	if !GamePaused() {
		t.Fatal("writing @level.prop.paused did not pause the game")
	}
	// What a level load, and entering or leaving play, all call.
	ScriptEventsReset()
	if GamePaused() {
		t.Error("the pause outlived the level; nothing would be left to lift it")
	}
}
