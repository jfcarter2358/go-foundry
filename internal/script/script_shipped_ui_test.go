package script

// script_shipped_ui_test.go — the menu scripts that ship with the engine must
// pass the linter.
//
// They are the one pair of shipped scripts nothing else exercises: they run
// only when a player clicks a button, so a name the engine does not provide is
// a warning in a log file at the moment the game is meant to quit, which is the
// worst place to find out. The `quit_game` verb was exactly that — implemented
// in the runtime's verb table but not listed in flang's worldVerbs, so it
// linked as an undefined function and the button did nothing.
//
// Linted against an empty level on purpose: a menu belongs to the game, not to
// whatever level happens to be behind it, so anything in one of these that
// needs a particular level's objects is itself the bug.

import (
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/flang"
	"foundry/internal/testenv"
	"foundry/internal/ui"
	"foundry/internal/world"
)

func TestShippedScreenScriptsLintClean(t *testing.T) {
	for _, name := range []string{"menu.flang", "pause.flang"} {
		t.Run(name, func(t *testing.T) {
			src := testenv.RepoFile(t, "resources", "scripts", name)
			f, errs := flang.Parse(name, []byte(src))
			if len(errs) > 0 {
				t.Fatalf("%s does not parse: %v", name, errs)
			}
			scope := map[string]bool{}
			for _, g := range f.Funcs {
				scope[g.Name] = true
			}
			issues := LintScript(&world.Level{}, f, scope, nil)
			for _, i := range issues {
				t.Errorf("%s:%d: %s", name, i.Pos.Line, i.Msg)
			}
		})
	}
}

// The two verbs the menus are built out of. Their absence is silent — a call to
// something the host does not provide lints as an undefined function, and a
// button with an undefined function behind it is a button that does nothing.
func TestMenuVerbsExist(t *testing.T) {
	for _, verb := range []string{"show_screen", "hide_screen", "load_level", "quit_game"} {
		if !flang.IsBuiltin(verb) {
			t.Errorf("the language does not know %q; a menu calling it lints as undefined", verb)
		}
		if _, ok := scriptVerbs[verb]; !ok {
			t.Errorf("the runtime has no implementation of %q", verb)
		}
	}
}

// quit_game takes no arguments, and says so rather than ignoring the extras.
func TestQuitGameRefusesArguments(t *testing.T) {
	_, err := scriptVerbs["quit_game"](nil, []flang.Value{flang.StrVal("now")}, flang.Pos{})
	if err == nil {
		t.Fatal("quit_game accepted an argument")
	}
	if !strings.Contains(err.Error(), "no arguments") {
		t.Errorf("the error does not say what is wrong: %v", err)
	}
}

// The whole chain the player actually exercises, with nothing stubbed: the
// shipped menu.fui off disk, the shipped menu.flang behind it, a real pointer
// press and release on the real Play button's rect, and the real tick that
// drains the click into the handler.
//
// Every piece of this was individually correct while the menu was completely
// dead, because the one broken link was in a package neither of them can see —
// internal/app's event type switch. A test that mocked the click would have
// passed then too, so this one presses the button.
func TestShippedMenuPlayButtonHidesTheMenu(t *testing.T) {
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join(testenv.RepoRoot(t), "resources")
	t.Cleanup(func() { fileutil.ResourcesPath = prev })

	st := withUIStack(t)
	lvl := tickTestLevel(t)
	prevRT := *Scripts
	prevEv := scriptEvents
	t.Cleanup(func() { *Scripts = prevRT; scriptEvents = prevEv; ui.OnScreenShown = nil })

	// The release build's order: the runtime first, so ui.OnScreenShown is
	// installed, then the screen — which is what loads its script.
	ScriptRuntimeLoad(lvl)
	ScriptEventsReset()
	if _, err := st.Show("menu"); err != nil {
		t.Fatalf("show the shipped menu: %v", err)
	}

	// At the reference resolution the canvas scale is 1, so the button's
	// authored offsets are window pixels: 380 wide, centred, 20 below the
	// middle.
	st.Tick(0, 1920, 1080)
	const cx, cy = 960, 556
	st.PointerDown(cx, cy)
	// Asserted separately from the outcome so a coordinate that misses the
	// button reads as "the click landed nowhere" rather than as a wiring bug.
	if press := st.Interaction().Press; !press.Is("menu", "play") {
		t.Fatalf("(%g, %g) pressed %+v, not the Play button", float32(cx), float32(cy), press)
	}
	st.PointerUp(cx, cy)

	for i := 0; i < 3; i++ {
		ScriptTick(lvl, 1.0/60.0, true)
	}
	if st.Find("menu") != nil {
		t.Error("Play did not take the menu off the stack")
	}
}

// The pause menu's own chain, and the one button on it that is not just a
// hide: Quit to Menu has to take the pause screen down AND bring the main menu
// up, and getting only the first half is a game with no way back.
func TestShippedPauseQuitToMenuReturnsToTheMainMenu(t *testing.T) {
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join(testenv.RepoRoot(t), "resources")
	t.Cleanup(func() { fileutil.ResourcesPath = prev })

	st := withUIStack(t)
	lvl := tickTestLevel(t)
	prevRT := *Scripts
	prevEv := scriptEvents
	t.Cleanup(func() { *Scripts = prevRT; scriptEvents = prevEv; ui.OnScreenShown = nil })

	ScriptRuntimeLoad(lvl)
	ScriptEventsReset()
	if _, err := st.Show("pause"); err != nil {
		t.Fatalf("show the shipped pause menu: %v", err)
	}

	// Third button down the panel, at the reference resolution.
	st.Tick(0, 1920, 1080)
	const cx, cy = 960, 613
	st.PointerDown(cx, cy)
	if press := st.Interaction().Press; !press.Is("pause", "to_menu") {
		t.Fatalf("(%g, %g) pressed %+v, not Quit to Menu", float32(cx), float32(cy), press)
	}
	st.PointerUp(cx, cy)

	for i := 0; i < 3; i++ {
		ScriptTick(lvl, 1.0/60.0, true)
	}
	if st.Find("pause") != nil {
		t.Error("Quit to Menu left the pause screen up")
	}
	if st.Find("menu") == nil {
		t.Error("Quit to Menu did not bring the main menu up")
	}
}

// The pause menu really pauses, and Escape really unpauses. Escape is the case
// that matters: it closes the screen through Stack.Cancel without touching any
// button, so the only thing the script ever hears is :hidden: — delivered to a
// screen that is already off the stack.
func TestShippedPauseMenuPausesAndEscapeLiftsIt(t *testing.T) {
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join(testenv.RepoRoot(t), "resources")
	t.Cleanup(func() { fileutil.ResourcesPath = prev })

	st := withUIStack(t)
	lvl := tickTestLevel(t)
	prevRT := *Scripts
	prevEv := scriptEvents
	t.Cleanup(func() {
		*Scripts = prevRT
		scriptEvents = prevEv
		ui.OnScreenShown, ui.OnScreenHidden = nil, nil
	})

	ScriptRuntimeLoad(lvl)
	ScriptEventsReset()
	if _, err := st.Show("pause"); err != nil {
		t.Fatalf("show the shipped pause menu: %v", err)
	}
	for i := 0; i < 2; i++ {
		ScriptTick(lvl, 1.0/60.0, true)
	}
	if !GamePaused() {
		t.Fatal("putting the pause menu up did not pause the game")
	}

	if !st.Cancel() {
		t.Fatal("Escape did not close the pause menu")
	}
	for i := 0; i < 2; i++ {
		ScriptTick(lvl, 1.0/60.0, true)
	}
	if GamePaused() {
		t.Error("Escape closed the pause menu and left the game paused")
	}
}
