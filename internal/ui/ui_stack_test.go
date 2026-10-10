package ui

import (
	"testing"

	"foundry/internal/hooks"
)

// stackWith builds a stack over already-loaded screens, solved at 1000x800 so
// the pointer coordinates below are pixels and canvas units at once.
func stackWith(screens ...*Screen) *Stack {
	hooks.TakeUIEvents() // start from a clean buffer
	st := &Stack{}
	for _, s := range screens {
		st.Push(s)
	}
	hooks.TakeUIEvents() // discard the :shown: events the pushes recorded
	st.Tick(0, 1000, 800)
	return st
}

// screenOf builds a screen whose canvas is 1:1 with a 1000x800 framebuffer, so
// that a test can talk in pixels.
func screenOf(id string, kids ...Node) *Screen {
	s := NewScreen(id)
	s.Canvas = Canvas{Mode: ScaleConstantPixel}
	s.Root.Children = kids
	return s
}

func atoms(evs []hooks.UIEvent) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Atom
	}
	return out
}

func TestShowRaisesRatherThanDuplicating(t *testing.T) {
	a, b := screenOf("a"), screenOf("b")
	st := &Stack{Load: func(name string) (*Screen, error) {
		if name == "a" {
			return a, nil
		}
		return b, nil
	}}
	if _, err := st.Show("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Show("b"); err != nil {
		t.Fatal(err)
	}
	hooks.TakeUIEvents()

	if _, err := st.Show("a"); err != nil {
		t.Fatal(err)
	}
	if n := len(st.Screens()); n != 2 {
		t.Fatalf("%d screens after re-showing one, want 2", n)
	}
	if st.Top().ID != "a" {
		t.Errorf("top is %q, want the raised screen", st.Top().ID)
	}
	if evs := hooks.TakeUIEvents(); len(evs) != 0 {
		t.Errorf("raising re-posted %v; a screen already up was not shown again", atoms(evs))
	}
}

func TestShowAndHidePostEvents(t *testing.T) {
	hooks.TakeUIEvents()
	st := &Stack{}
	st.Push(screenOf("menu"))
	if got := atoms(hooks.TakeUIEvents()); len(got) != 1 || got[0] != "shown" {
		t.Fatalf("push posted %v, want one shown", got)
	}
	st.Hide("menu")
	if got := atoms(hooks.TakeUIEvents()); len(got) != 1 || got[0] != "hidden" {
		t.Fatalf("hide posted %v, want one hidden", got)
	}
	if st.Hide("menu") {
		t.Error("hiding a screen that is not up reported success")
	}
}

func TestResetIsSilent(t *testing.T) {
	// Play beginning and ending clears the stack. A :hidden: per screen there
	// would be posted at a runtime that is about to be torn down anyway.
	st := stackWith(screenOf("a"), screenOf("b"))
	st.Reset()
	if evs := hooks.TakeUIEvents(); len(evs) != 0 {
		t.Errorf("Reset posted %v", atoms(evs))
	}
	if len(st.Screens()) != 0 {
		t.Error("Reset left screens up")
	}
}

func TestDerivedSemantics(t *testing.T) {
	hud := screenOf("hud")
	pause := screenOf("pause")
	pause.Modal, pause.Pauses, pause.ShowsCursor = true, true, true

	st := stackWith(hud)
	if st.CapturesInput() || st.Pauses() || st.WantsCursor() {
		t.Error("a plain HUD claimed input, pause or the cursor")
	}

	st = stackWith(hud, pause)
	if !st.CapturesInput() || !st.Pauses() || !st.WantsCursor() {
		t.Error("a modal pausing screen did not take input, pause and the cursor")
	}

	st.Hide("pause")
	if st.CapturesInput() || st.Pauses() || st.WantsCursor() {
		t.Error("closing the menu did not give gameplay its input back")
	}
}

func TestModalHidesInputFromWhatIsBelow(t *testing.T) {
	hud := screenOf("hud", at("hudbtn", KindButton, 0, 0, 100, 100))
	menu := screenOf("menu", at("menubtn", KindButton, 500, 0, 100, 100))
	menu.Modal = true
	st := stackWith(hud, menu)

	st.PointerMove(50, 50) // over the HUD's button, under the modal menu
	if st.Interaction().Hover.Valid() {
		t.Errorf("the HUD under a modal screen hovered at %+v", st.Interaction().Hover)
	}
	st.PointerMove(550, 50)
	if !st.Interaction().Hover.Is("menu", "menubtn") {
		t.Errorf("the modal screen's own button did not hover: %+v", st.Interaction().Hover)
	}
}

func TestClickPostsThroughHooks(t *testing.T) {
	st := stackWith(screenOf("s", at("btn", KindButton, 0, 0, 100, 100)))
	hooks.TakeUIEvents()

	st.PointerDown(50, 50)
	st.PointerUp(50, 50)
	if got := atoms(hooks.TakeUIEvents()); len(got) != 1 || got[0] != "click" {
		t.Errorf("press and release posted %v, want one click", got)
	}
}

func TestReleasingOffTheNodePostsNothing(t *testing.T) {
	// Dragging off a button and letting go is how a player cancels a press.
	st := stackWith(screenOf("s", at("btn", KindButton, 0, 0, 100, 100)))
	hooks.TakeUIEvents()

	st.PointerDown(50, 50)
	st.PointerUp(500, 500)
	if got := atoms(hooks.TakeUIEvents()); len(got) != 0 {
		t.Errorf("releasing away from the button posted %v", got)
	}
	if st.Interaction().Press.Valid() {
		t.Error("the press was not cleared")
	}
}

func TestHoverAndUnhoverPostOnChangeOnly(t *testing.T) {
	st := stackWith(screenOf("s",
		at("a", KindButton, 0, 0, 100, 100),
		at("b", KindButton, 200, 0, 100, 100)))
	hooks.TakeUIEvents()

	st.PointerMove(50, 50)
	st.PointerMove(60, 60) // still over a
	if got := atoms(hooks.TakeUIEvents()); len(got) != 1 || got[0] != "hover" {
		t.Errorf("moving within one widget posted %v, want one hover", got)
	}

	st.PointerMove(250, 50)
	if got := atoms(hooks.TakeUIEvents()); len(got) != 2 || got[0] != "unhover" || got[1] != "hover" {
		t.Errorf("moving between widgets posted %v, want unhover then hover", got)
	}
}

func TestDisabledWidgetSwallowsTheClickWithoutActivating(t *testing.T) {
	btn := at("btn", KindButton, 0, 0, 100, 100)
	btn.Enabled = false
	st := stackWith(screenOf("s", btn))
	hooks.TakeUIEvents()

	if !st.PointerDown(50, 50) {
		t.Error("a disabled button let the click through to gameplay")
	}
	st.PointerUp(50, 50)
	if got := atoms(hooks.TakeUIEvents()); len(got) != 0 {
		t.Errorf("a disabled button posted %v", got)
	}
}

func TestBlockingPanelSwallowsTheClick(t *testing.T) {
	panel := at("bg", KindPanel, 0, 0, 1000, 800)
	panel.Blocks = true
	st := stackWith(screenOf("s", panel))

	if !st.PointerDown(50, 50) {
		t.Error("a Blocks panel let the click through")
	}
}

func TestToggleFlipsAndPostsBoth(t *testing.T) {
	st := stackWith(screenOf("s", at("t", KindToggle, 0, 0, 40, 40)))
	hooks.TakeUIEvents()

	st.PointerDown(20, 20)
	st.PointerUp(20, 20)

	evs := hooks.TakeUIEvents()
	if got := atoms(evs); len(got) != 2 || got[0] != "change" || got[1] != "click" {
		t.Fatalf("toggling posted %v, want change then click", got)
	}
	if !evs[0].Bool {
		t.Error("the change event did not carry the new state")
	}
	if !st.Find("s").Find("t").Value.On {
		t.Error("the toggle did not flip")
	}
}

func TestSliderDragsToTheValue(t *testing.T) {
	sl := at("sl", KindSlider, 0, 0, 200, 20)
	sl.Value = ValueSpec{Min: 0, Max: 10}
	st := stackWith(screenOf("s", sl))
	hooks.TakeUIEvents()

	st.PointerDown(100, 10) // halfway
	n := st.Find("s").Find("sl")
	if n.Value.Value != 5 {
		t.Errorf("a click halfway set %v, want 5", n.Value.Value)
	}
	st.PointerMove(150, 10)
	if n.Value.Value != 7.5 {
		t.Errorf("dragging to three quarters set %v, want 7.5", n.Value.Value)
	}
	// Past the end clamps rather than overshooting.
	st.PointerMove(9999, 10)
	if n.Value.Value != 10 {
		t.Errorf("dragging past the end set %v, want the maximum", n.Value.Value)
	}

	for _, e := range hooks.TakeUIEvents() {
		if e.Atom != "change" {
			t.Errorf("a slider drag posted %q, want only change", e.Atom)
		}
	}
}

func TestSliderHonoursItsStep(t *testing.T) {
	sl := at("sl", KindSlider, 0, 0, 200, 20)
	sl.Value = ValueSpec{Min: 0, Max: 10, Step: 2}
	st := stackWith(screenOf("s", sl))

	st.PointerDown(110, 10) // 5.5 raw
	if got := st.Find("s").Find("sl").Value.Value; got != 6 {
		t.Errorf("a step of 2 gave %v, want 6", got)
	}
}

func TestSliderReleaseDoesNotAlsoClick(t *testing.T) {
	// A drag already reported itself with change events; a click on release
	// would fire a second, different handler for one gesture.
	st := stackWith(screenOf("s", at("sl", KindSlider, 0, 0, 200, 20)))
	st.PointerDown(100, 10)
	hooks.TakeUIEvents()
	st.PointerUp(100, 10)
	for _, a := range atoms(hooks.TakeUIEvents()) {
		if a == "click" {
			t.Error("releasing a slider also posted a click")
		}
	}
}

func TestNavigateMovesFocusAndActivateFires(t *testing.T) {
	st := stackWith(screenOf("s",
		at("a", KindButton, 0, 0, 100, 40),
		at("b", KindButton, 0, 100, 100, 40)))
	hooks.TakeUIEvents()

	if !st.Navigate(DirDown) {
		t.Fatal("navigating with nothing focused did nothing")
	}
	if !st.Interaction().Focus.Is("s", "a") {
		t.Errorf("focus went to %+v, want the first widget", st.Interaction().Focus)
	}
	if !st.Interaction().NavDriven {
		t.Error("NavDriven was not set; the focus ring would not draw")
	}

	st.Navigate(DirDown)
	if !st.Interaction().Focus.Is("s", "b") {
		t.Errorf("focus is %+v, want b", st.Interaction().Focus)
	}

	hooks.TakeUIEvents()
	st.Activate()
	if got := atoms(hooks.TakeUIEvents()); len(got) != 1 || got[0] != "click" {
		t.Errorf("activate posted %v, want one click", got)
	}
}

func TestPointerMoveClearsNavDriven(t *testing.T) {
	// Two highlighted buttons at once — one under the pointer and one where the
	// stick left focus — is the thing NavDriven exists to prevent.
	st := stackWith(screenOf("s", at("a", KindButton, 0, 0, 100, 40)))
	st.Navigate(DirDown)
	if !st.Interaction().NavDriven {
		t.Fatal("nav did not set the flag")
	}
	st.PointerMove(50, 20)
	if st.Interaction().NavDriven {
		t.Error("moving the pointer left the focus ring on")
	}
}

func TestCancelClosesTheTopmostModal(t *testing.T) {
	hud := screenOf("hud")
	menu := screenOf("menu")
	menu.Modal = true
	st := stackWith(hud, menu)
	hooks.TakeUIEvents()

	if !st.Cancel() {
		t.Fatal("Escape did nothing with a modal screen up")
	}
	if got := atoms(hooks.TakeUIEvents()); len(got) != 2 || got[0] != "cancel" || got[1] != "hidden" {
		t.Errorf("cancel posted %v, want cancel then hidden", got)
	}
	if st.Find("menu") != nil {
		t.Error("the menu is still up")
	}
	if st.Find("hud") == nil {
		t.Error("Escape closed the HUD as well")
	}

	// With nothing modal up, Escape belongs to gameplay.
	if st.Cancel() {
		t.Error("Escape was consumed with no modal screen up")
	}
}

func TestTextFieldTypingAndEditing(t *testing.T) {
	st := stackWith(screenOf("s", at("f", KindInput, 0, 0, 200, 30)))
	st.PointerDown(10, 10)
	st.PointerUp(10, 10)
	hooks.TakeUIEvents()

	st.TypeText("abc")
	n := st.Find("s").Find("f")
	if n.Text.Value != "abc" {
		t.Fatalf("typed %q", n.Text.Value)
	}
	st.EditText(EditBackspace)
	if n.Text.Value != "ab" {
		t.Errorf("backspace gave %q", n.Text.Value)
	}
	st.EditText(EditHome)
	st.TypeText("X")
	if n.Text.Value != "Xab" {
		t.Errorf("typing at the start gave %q", n.Text.Value)
	}
	st.EditText(EditEnd)
	st.EditText(EditDelete)
	if n.Text.Value != "Xab" {
		t.Errorf("delete at the end changed %q", n.Text.Value)
	}

	hooks.TakeUIEvents()
	st.EditText(EditSubmit)
	if got := atoms(hooks.TakeUIEvents()); len(got) != 1 || got[0] != "submit" {
		t.Errorf("Enter posted %v, want submit", got)
	}
}

func TestCaretNeverLandsInsideARune(t *testing.T) {
	st := stackWith(screenOf("s", at("f", KindInput, 0, 0, 200, 30)))
	st.PointerDown(10, 10)
	st.PointerUp(10, 10)
	st.TypeText("héllo")

	n := st.Find("s").Find("f")
	for i := 0; i < 10; i++ {
		st.EditText(EditLeft)
		c := st.Interaction().Caret
		if c > 0 && c < len(n.Text.Value) && n.Text.Value[c]&0xC0 == 0x80 {
			t.Fatalf("caret at %d is inside a multi-byte rune of %q", c, n.Text.Value)
		}
	}
}

func TestTypingIsIgnoredWithNoFocusedField(t *testing.T) {
	st := stackWith(screenOf("s", at("b", KindButton, 0, 0, 100, 40)))
	if st.TypeText("x") {
		t.Error("text was consumed with no field focused; the key would never reach gameplay")
	}
}

func TestBindingsAreRefreshedEachTick(t *testing.T) {
	label := at("l", KindText, 0, 0, 200, 40)
	label.Text.Value = "authored"
	label.Text.Bind = "@player.attr.name"
	label.Text.Format = ""

	bar := at("b", KindBar, 0, 100, 200, 20)
	bar.Value = ValueSpec{Max: 1, Bind: "hp", BindMax: "hpmax"}

	s := screenOf("s", label, bar)
	st := &Stack{}
	st.Push(s)

	health := 30.0
	st.Resolve = func(expr string) (Value, bool) {
		switch expr {
		case "@player.attr.name":
			return StrValue("Ada"), true
		case "hp":
			return NumValue(health), true
		case "hpmax":
			return NumValue(120), true
		}
		return Value{}, false
	}

	st.Tick(0, 1000, 800)
	if got := s.Find("l").Text.Shown(); got != "Ada" {
		t.Errorf("bound label shows %q, want the resolved value", got)
	}
	if got := s.Find("b").Value.Fraction(); got != 0.25 {
		t.Errorf("bar is %v full, want 30/120", got)
	}

	health = 90
	st.Tick(0, 1000, 800)
	if got := s.Find("b").Value.Fraction(); got != 0.75 {
		t.Errorf("bar is %v full after the value changed, want 0.75", got)
	}
}

func TestClearingTheResolverPutsTheAuthoredValueBack(t *testing.T) {
	// This is what the editor's "literal" preview mode is: a stack with no
	// resolver shows exactly what the document says.
	label := at("l", KindText, 0, 0, 200, 40)
	label.Text.Value = "authored"
	label.Text.Bind = "x"
	s := screenOf("s", label)

	st := &Stack{Resolve: func(string) (Value, bool) { return StrValue("live"), true }}
	st.Push(s)
	st.Tick(0, 1000, 800)
	if got := s.Find("l").Text.Shown(); got != "live" {
		t.Fatalf("bound label shows %q", got)
	}

	st.Resolve = nil
	st.Tick(0, 1000, 800)
	if got := s.Find("l").Text.Shown(); got != "authored" {
		t.Errorf("with no resolver the label shows %q, want the authored string", got)
	}
}

func TestAResolverThatDeclinesFallsBack(t *testing.T) {
	// An entity that has been destroyed stops answering. The HUD must show the
	// authored placeholder rather than freezing on the last number it saw.
	label := at("l", KindText, 0, 0, 200, 40)
	label.Text.Value = "--"
	label.Text.Bind = "x"
	s := screenOf("s", label)

	alive := true
	st := &Stack{Resolve: func(string) (Value, bool) {
		if !alive {
			return Value{}, false
		}
		return StrValue("42"), true
	}}
	st.Push(s)
	st.Tick(0, 1000, 800)
	alive = false
	st.Tick(0, 1000, 800)

	if got := s.Find("l").Text.Shown(); got != "--" {
		t.Errorf("a binding that stopped answering left %q on screen", got)
	}
}

func TestWritingABoundWidgetIsRefused(t *testing.T) {
	n := NewNode("n", KindText)
	n.Text.Bind = "x"
	if n.SetText("script wrote this") {
		t.Error("a script write to a bound label was accepted")
	}
	if n.Text.Value == "script wrote this" {
		t.Error("the write landed anyway")
	}

	b := NewNode("b", KindBar)
	b.Value.Bind = "x"
	if b.SetValue(5) {
		t.Error("a script write to a bound bar was accepted")
	}
}

func TestStaleTargetsAreDropped(t *testing.T) {
	// A script hiding the widget under the pointer must not leave a press that
	// a later release can complete.
	st := stackWith(screenOf("s", at("btn", KindButton, 0, 0, 100, 100)))
	st.PointerDown(50, 50)
	if !st.Interaction().Press.Valid() {
		t.Fatal("nothing was pressed")
	}

	st.Find("s").Root.Children = nil
	st.Tick(0, 1000, 800)
	if st.Interaction().Press.Valid() || st.Interaction().Hover.Valid() {
		t.Errorf("a deleted widget is still pressed or hovered: %+v", *st.Interaction())
	}
}

func TestHidingAScreenClearsItsTargets(t *testing.T) {
	st := stackWith(screenOf("s", at("btn", KindButton, 0, 0, 100, 100)))
	st.PointerDown(50, 50)
	st.Hide("s")
	in := st.Interaction()
	if in.Press.Valid() || in.Focus.Valid() || in.Hover.Valid() {
		t.Errorf("closing a screen left targets on it: %+v", *in)
	}
}

func TestPointerIsConvertedPerScreensCanvas(t *testing.T) {
	// Two screens up at once can have different reference resolutions. A single
	// conversion for the whole stack would put one of them's widgets in the
	// wrong place.
	big := NewScreen("big")
	big.Canvas = Canvas{Mode: ScaleWithScreen, RefW: 1000, RefH: 800, Match: 0.5}
	big.Root.Children = []Node{at("wide", KindButton, 0, 0, 500, 400)}

	small := NewScreen("small")
	small.Canvas = Canvas{Mode: ScaleWithScreen, RefW: 500, RefH: 400, Match: 0.5}
	small.Root.Children = []Node{at("narrow", KindButton, 0, 0, 100, 80)}

	st := stackWith(big, small)

	// In device pixels the small screen's widget covers 200x160 of a 1000x800
	// framebuffer, because its canvas is half the size.
	st.PointerMove(150, 100)
	if !st.Interaction().Hover.Is("small", "narrow") {
		t.Errorf("hover is %+v, want the top screen's widget", st.Interaction().Hover)
	}
	st.PointerMove(300, 100)
	if !st.Interaction().Hover.Is("big", "wide") {
		t.Errorf("hover is %+v, want the lower screen's widget beyond the small one",
			st.Interaction().Hover)
	}
}
