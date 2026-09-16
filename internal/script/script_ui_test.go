package script

// script_ui_test.go — screens and widgets through the real event loop.
//
// Headless: the .fui goes into a temp ui/ directory, the stack is a local one,
// and nothing here needs a window. What is being asserted is that a script can
// reach a widget at all — the six binding sites have to agree for that to work,
// and each of them is easy to forget.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/flang"
	"foundry/internal/hooks"
	"foundry/internal/ui"
	"foundry/internal/world"
)

// withUIStack points ref resolution at a stack of this test's own, so screens
// do not leak into the next test through the package global.
func withUIStack(t *testing.T) *ui.Stack {
	t.Helper()
	prev := uiStack
	st := &ui.Stack{}
	uiStack = st
	t.Cleanup(func() { uiStack = prev; hooks.TakeUIEvents() })
	return st
}

// writeScreen puts a .fui in the temp ui/ directory.
func writeScreen(t *testing.T, name, body string) {
	t.Helper()
	dir := filepath.Join(fileutil.ResourcesPath, ui.UIDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+ui.UIExt), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ui.InvalidateScreen(name)
}

// pinned is a widget at an absolute rect, so a click can be aimed at it.
func pinnedWidget(id string, kind ui.Kind, x, y, w, h float32) ui.Node {
	n := ui.NewNode(id, kind)
	n.Rect.AnchorMin = [2]float32{0, 0}
	n.Rect.AnchorMax = [2]float32{0, 0}
	n.Rect.Offset = [4]float32{x, y, -(x + w), -(y + h)}
	return n
}

// screenWithButton builds a one-button screen carrying a script.
func screenWithButton(id, scriptPath string) *ui.Screen {
	s := ui.NewScreen(id)
	s.Canvas = ui.Canvas{Mode: ui.ScaleConstantPixel}
	btn := pinnedWidget("ok", ui.KindButton, 0, 0, 100, 40)
	btn.Script = scriptPath
	s.Root.Children = []ui.Node{btn}
	return s
}

// TestScriptCanReachAWidget is the whole binding chain in one assertion: the
// ref kind, refFor, scriptDataFor, the property table and the runtime.
func TestScriptCanReachAWidget(t *testing.T) {
	root := withTempScriptDir(t)
	st := withUIStack(t)
	path := writeScript(t, root, "w.flang", `def on_spawn(:spawn:) -> null
    @self.prop.text = "pressed"
    @self.prop.enabled = false
end`)

	st.Push(screenWithButton("menu", path))
	st.Tick(0, 800, 600)

	lvl := tickTestLevel(t)
	runTicks(t, lvl, 3)

	btn := st.Find("menu").Find("ok")
	if btn.Text.Value != "pressed" {
		t.Errorf("text is %q; the script's write did not reach the widget", btn.Text.Value)
	}
	if btn.Enabled {
		t.Error("the write to .prop.enabled did not reach the widget")
	}
}

func TestClickPostsToTheWidget(t *testing.T) {
	root := withTempScriptDir(t)
	st := withUIStack(t)
	path := writeScript(t, root, "w.flang", `def on_click(:click:, src) -> null
    @self.attr.clicks = 1
end`)

	st.Push(screenWithButton("menu", path))
	st.Tick(0, 800, 600)
	st.PointerDown(50, 20)
	st.PointerUp(50, 20)

	lvl := tickTestLevel(t)
	runTicks(t, lvl, 2)

	btn := st.Find("menu").Find("ok")
	if v, ok := btn.Attr("clicks"); !ok {
		t.Fatal("the click never reached a handler")
	} else if n, _ := v.Int(); n != 1 {
		t.Errorf("clicks is %d, want 1", n)
	}
}

// TestClickBubblesToTheScreen is the one level of routing there is: a widget
// with no script of its own is its screen's business.
func TestClickBubblesToTheScreen(t *testing.T) {
	root := withTempScriptDir(t)
	st := withUIStack(t)
	path := writeScript(t, root, "s.flang", `def on_click(:click:, src) -> null
    @self.attr.last = src.id
end`)

	s := ui.NewScreen("menu")
	s.Canvas = ui.Canvas{Mode: ui.ScaleConstantPixel}
	s.Script = path
	s.Root.Children = []ui.Node{pinnedWidget("ok", ui.KindButton, 0, 0, 100, 40)}
	st.Push(s)
	st.Tick(0, 800, 600)
	st.PointerDown(50, 20)
	st.PointerUp(50, 20)

	lvl := tickTestLevel(t)
	runTicks(t, lvl, 2)

	if v, ok := st.Find("menu").Attr("last"); !ok {
		t.Fatal("an unscripted widget's click did not reach its screen")
	} else if id, _ := v.Str(); id != "ok" {
		t.Errorf("the handler saw src.id %q, want the widget's own id", id)
	}
}

func TestChangeCarriesTheNewValue(t *testing.T) {
	root := withTempScriptDir(t)
	st := withUIStack(t)
	path := writeScript(t, root, "s.flang", `def on_change(:change:, src, v) -> null
    @self.attr.seen = v
end`)

	s := ui.NewScreen("menu")
	s.Canvas = ui.Canvas{Mode: ui.ScaleConstantPixel}
	s.Script = path
	sl := pinnedWidget("vol", ui.KindSlider, 0, 0, 200, 20)
	sl.Value = ui.ValueSpec{Min: 0, Max: 10}
	s.Root.Children = []ui.Node{sl}
	st.Push(s)
	st.Tick(0, 800, 600)
	st.PointerDown(100, 10) // halfway

	lvl := tickTestLevel(t)
	runTicks(t, lvl, 2)

	v, ok := st.Find("menu").Attr("seen")
	if !ok {
		t.Fatal("the slider's change never reached a handler")
	}
	if f, _ := v.Float(); f != 5 {
		t.Errorf("the handler saw %v, want the slider's new value 5", f)
	}
}

func TestShowAndHideScreenFromAScript(t *testing.T) {
	root := withTempScriptDir(t)
	st := withUIStack(t)
	writeScreen(t, "pause", "id: pause\nroot: {id: root, kind: panel}\n")

	path := writeScript(t, root, "lvl.flang", `def on_spawn(:spawn:) -> null
    show_screen("pause")
end`)
	lvl := tickTestLevel(t)
	lvl.Scripts = []string{path}
	runTicks(t, lvl, 2)

	if st.Find("pause") == nil {
		t.Fatal("show_screen did not put the screen up")
	}

	// And a second show raises rather than duplicating.
	if _, err := st.Show("pause"); err != nil {
		t.Fatal(err)
	}
	if n := len(st.Screens()); n != 1 {
		t.Errorf("%d screens after showing twice, want 1", n)
	}
	if !st.Hide("pause") {
		t.Error("hide_screen's underlying call failed")
	}
}

func TestShowScreenWarnsRatherThanFailingOnAMissingFile(t *testing.T) {
	root := withTempScriptDir(t)
	withUIStack(t)
	path := writeScript(t, root, "lvl.flang", `def on_spawn(:spawn:) -> null
    show_screen("not-a-screen")
end`)
	lvl := tickTestLevel(t)
	lvl.Scripts = []string{path}

	ScriptLog.Clear()
	runTicks(t, lvl, 2)

	found := false
	for _, e := range ScriptLog.Snapshot() {
		if containsStr(e.Text, "not-a-screen") {
			found = true
		}
	}
	if !found {
		t.Error("a missing screen was swallowed without a word in the log")
	}
}

// TestWritingABoundWidgetIsRefusedAndSaysWhy is the precedence rule. A script
// write and a poll racing each other would be decided by frame timing, which is
// not something anyone can debug.
func TestWritingABoundWidgetIsRefusedAndSaysWhy(t *testing.T) {
	root := withTempScriptDir(t)
	st := withUIStack(t)
	path := writeScript(t, root, "w.flang", `def on_spawn(:spawn:) -> null
    @self.prop.text = "script wrote this"
end`)

	s := ui.NewScreen("hud")
	s.Canvas = ui.Canvas{Mode: ui.ScaleConstantPixel}
	label := pinnedWidget("hp", ui.KindText, 0, 0, 200, 40)
	label.Script = path
	label.Text.Value = "authored"
	label.Text.Bind = "@player.attr.health"
	s.Root.Children = []ui.Node{label}
	st.Push(s)
	st.Tick(0, 800, 600)

	ScriptLog.Clear()
	lvl := tickTestLevel(t)
	runTicks(t, lvl, 2)

	if got := st.Find("hud").Find("hp").Text.Value; got != "authored" {
		t.Errorf("the bound label was overwritten with %q", got)
	}
	named := false
	for _, e := range ScriptLog.Snapshot() {
		if containsStr(e.Text, "@player.attr.health") {
			named = true
		}
	}
	if !named {
		t.Error("the refusal did not name the binding, which is the whole fix")
	}
}

func TestRefForResolvesAWidgetAndRefusesAnAmbiguousOne(t *testing.T) {
	st := withUIStack(t)
	a := ui.NewScreen("a")
	a.Root.Children = []ui.Node{ui.NewNode("ok", ui.KindButton)}
	b := ui.NewScreen("b")
	b.Root.Children = []ui.Node{ui.NewNode("ok", ui.KindButton)}

	st.Push(a)
	c := buildRefCache(nil)
	r, ok := c.refFor("ok")
	if !ok || r.Kind != flang.RefWidget || r.A != "a" || r.B != "ok" {
		t.Fatalf("one screen up: refFor gave %+v (found=%v)", r, ok)
	}

	st.Push(b)
	c = buildRefCache(nil)
	if _, ok := c.refFor("ok"); ok {
		t.Error("a widget id two screens both carry resolved to one of them")
	}
	if !c.uiRefIsAmbiguous("ok") {
		t.Error("the ambiguity was not detectable, so nothing can explain the refusal")
	}

	// The screen itself is still reachable by name.
	if r, ok := c.refFor("a"); !ok || r.Kind != flang.RefScreen {
		t.Errorf("refFor on a screen name gave %+v (found=%v)", r, ok)
	}
}

func TestLevelIDsBeatWidgetIDs(t *testing.T) {
	// The level is the thing an author names deliberately; a widget id is
	// scoped to its screen in a way a level id is not.
	st := withUIStack(t)
	s := ui.NewScreen("menu")
	s.Root.Children = []ui.Node{ui.NewNode("crate", ui.KindButton)}
	st.Push(s)

	lvl := &world.Level{Entities: []world.Entity{{ID: "crate"}}}
	c := buildRefCache(lvl)
	r, ok := c.refFor("crate")
	if !ok || r.Kind != flang.RefEntity {
		t.Errorf("refFor gave %+v, want the entity", r)
	}
}

func TestScreenScriptsAreLoadedWhenAScreenIsShown(t *testing.T) {
	// A screen shown by a verb is not in the level's list, so nothing would
	// otherwise know to read its script.
	root := withTempScriptDir(t)
	st := withUIStack(t)
	handler := writeScript(t, root, "menu.flang", `def on_spawn(:spawn:) -> null
    @self.attr.ran = 1
end`)
	writeScreen(t, "pause", "id: pause\nscript: "+handler+"\nroot: {id: root, kind: panel}\n")

	opener := writeScript(t, root, "lvl.flang", `def on_spawn(:spawn:) -> null
    show_screen("pause")
end`)
	lvl := tickTestLevel(t)
	lvl.Scripts = []string{opener}
	runTicks(t, lvl, 4)

	s := st.Find("pause")
	if s == nil {
		t.Fatal("the screen was not shown")
	}
	if _, ok := s.Attr("ran"); !ok {
		t.Error("the screen's own script never ran; it was not loaded on show")
	}
}

func TestTagBroadcastReachesWidgets(t *testing.T) {
	root := withTempScriptDir(t)
	st := withUIStack(t)
	path := writeScript(t, root, "w.flang", `def on_dim(:dim:) -> null
    @self.prop.alpha = 0.5
end`)

	s := ui.NewScreen("hud")
	a := pinnedWidget("a", ui.KindPanel, 0, 0, 10, 10)
	a.Tags = []string{"fadeable"}
	a.Script = path
	b := pinnedWidget("b", ui.KindPanel, 20, 0, 10, 10)
	b.Tags = []string{"fadeable"}
	b.Script = path
	s.Root.Children = []ui.Node{a, b}
	st.Push(s)

	opener := writeScript(t, root, "lvl.flang", `def on_spawn(:spawn:) -> null
    #fadeable <- (:dim:,)
end`)
	lvl := tickTestLevel(t)
	lvl.Scripts = []string{opener}
	runTicks(t, lvl, 3)

	for _, id := range []string{"a", "b"} {
		if got := st.Find("hud").Find(id).Alpha; got != 0.5 {
			t.Errorf("widget %q alpha is %v; the tag broadcast missed it", id, got)
		}
	}
}

// TestOnlyVisibleScreensTick — a menu sitting in resources/ui that nobody has
// opened must not be receiving :tick: sixty times a second.
func TestOnlyScreensOnTheStackTick(t *testing.T) {
	root := withTempScriptDir(t)
	st := withUIStack(t)
	path := writeScript(t, root, "w.flang", `def on_tick(:tick:, dt) -> null
    @self.attr.ticks = 1
end`)

	s := ui.NewScreen("hud")
	n := pinnedWidget("w", ui.KindPanel, 0, 0, 10, 10)
	n.Script = path
	s.Root.Children = []ui.Node{n}

	lvl := tickTestLevel(t)
	runTicks(t, lvl, 3)
	if _, ok := s.Find("w").Attr("ticks"); ok {
		t.Fatal("a screen that was never shown received :tick:")
	}

	st.Push(s)
	runTicks(t, lvl, 3)
	if _, ok := st.Find("hud").Find("w").Attr("ticks"); !ok {
		t.Error("a screen on the stack did not receive :tick:")
	}
}

// ---------- bindings ----------

func TestBindingResolvesAnAttributeAndAProperty(t *testing.T) {
	withUIStack(t)
	InvalidateBindingCache()

	lvl := tickTestLevel(t)
	e := world.Entity{ID: "hero"}
	e.Attributes = world.AttrSet{"health": world.FloatAttr(37)}
	lvl.Entities = append(lvl.Entities, e)
	runTicks(t, lvl, 1)

	v, ok := ResolveUIBindingForEditor("@hero.attr.health")
	if !ok {
		t.Fatal("an attribute binding resolved to nothing")
	}
	if v.AsNum() != 37 {
		t.Errorf("got %v, want 37", v.AsNum())
	}
}

func TestBindingParsesLiterals(t *testing.T) {
	InvalidateBindingCache()
	cases := map[string]ui.Value{
		"42":    ui.NumValue(42),
		"0.5":   ui.NumValue(0.5),
		"true":  ui.BoolValue(true),
		"Ready": ui.StrValue("Ready"),
		`"Go"`:  ui.StrValue("Go"),
	}
	for expr, want := range cases {
		got, ok := ResolveUIBindingForEditor(expr)
		if !ok {
			t.Errorf("%q resolved to nothing", expr)
			continue
		}
		if got.Kind != want.Kind || got.AsStr() != want.AsStr() {
			t.Errorf("%q gave %+v, want %+v", expr, got, want)
		}
	}
}

func TestBindingDeclinesWhatItCannotRead(t *testing.T) {
	withUIStack(t)
	InvalidateBindingCache()
	lvl := tickTestLevel(t)
	runTicks(t, lvl, 1)

	for _, expr := range []string{
		"@nobody.attr.health", // no such object
		"@hero.attr",          // too few parts
		"@hero.wat.health",    // neither attr nor prop
		"@",                   // nothing at all
		"",                    // nothing at all
	} {
		if _, ok := ResolveUIBindingForEditor(expr); ok {
			t.Errorf("%q resolved to something", expr)
		}
	}
}

func containsStr(s, sub string) bool { return strings.Contains(s, sub) }

// A screen's images are asked for when it goes up. Nothing else loads a UI
// texture, and one never uploaded draws as a flat block of its tint — a scope
// overlay that is a black rectangle over the whole view. Both ways a screen can
// be up are covered: shown by a script during play, and already on the stack
// when play began.
func TestShownScreensQueueTheirTextures(t *testing.T) {
	root := withTempScriptDir(t)
	st := withUIStack(t)
	writeScreen(t, "scope", `id: scope
root:
  id: root
  kind: panel
  children:
    - id: lens
      kind: image
      texture: scope_overlay
    - id: knob
      kind: button
      texture: knob_up
      hover: {texture: knob_hover}
`)
	writeScreen(t, "hud", `id: hud
root:
  id: root
  kind: panel
  children:
    - id: badge
      kind: image
      texture: hud_badge
`)
	if _, err := st.Show("hud"); err != nil {
		t.Fatal(err)
	}

	path := writeScript(t, root, "lvl.flang", `def on_spawn(:spawn:) -> null
    show_screen("scope")
end`)
	lvl := tickTestLevel(t)
	lvl.Scripts = []string{path}
	runTicks(t, lvl, 2)

	for _, name := range []string{"scope_overlay", "knob_up", "knob_hover", "hud_badge"} {
		if !Scripts.dirtyTex[name] {
			t.Errorf("texture %q was never queued to load, so it would draw as a flat block", name)
		}
	}
}
