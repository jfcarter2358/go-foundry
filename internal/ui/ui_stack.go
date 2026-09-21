package ui

// ui_stack.go — the screens a game currently has up, and everything input does
// to them.
//
// A HUD is a non-modal, non-pausing screen at the bottom. A pause menu is a
// modal, pausing screen pushed on top of it. A loading screen is the same thing
// pushed by the load job. One mechanism, three uses — which is why there is no
// application-state enum anywhere in the engine, and why "is the game paused"
// is a question about the stack rather than a variable somebody has to remember
// to set.
//
// Input arrives as intents (pointer moved, navigate up, activate) rather than
// as SDL events, so this package never learns which device asked. That is what
// makes a gamepad a translation layer in internal/app rather than a change in
// here, and it is what lets every test below run with no window at all.

import (
	"foundry/internal/hooks"
)

// OnScreenShown is called when a screen enters the stack, before its :shown:
// event is recorded. internal/script installs it to load the screen's .flang
// on demand — a screen shown by a verb is not in the level's list, so nothing
// would otherwise know to read its script. Nil is correct for a game with no
// scripts at all.
var OnScreenShown func(*Screen)

// OnScreenHidden is called as a screen leaves the stack, before its :hidden:
// event is recorded. internal/script installs it to keep the departing screen
// reachable for one tick: :hidden: and :cancel: are delivered on the tick
// after the screen has gone, and a message to a ref that no longer resolves is
// dropped — so without it neither event ever reached the screen it was about.
var OnScreenHidden func(*Screen)

// VideoDriver advances the clip behind one video node and reports where it got
// to. internal/uidraw installs it: opening the file, decoding a frame and
// getting that frame onto a texture are all things this package must not know
// how to do, and the alternative to a function variable is an import that runs
// the wrong way.
//
// key names the player. It is built here rather than by the driver because only
// the stack knows which stack a node is on — the game's and the UI editor's
// preview can both have a screen called "cutscene" up at once, and one player
// serving both would have them fighting over one playhead.
//
// Nil is correct for a headless test and for the editor before its renderer has
// initialised. A video node with no driver is a widget showing nothing, which is
// exactly what it looks like before its first frame decodes anyway.
var VideoDriver func(key string, n *Node, dt float32)

// nextStackScope numbers stacks. A counter rather than a name because a stack
// is not addressable by one: there are exactly two in the process and neither
// is anything an author writes down.
var nextStackScope uint64

// Stack is the screens currently up, bottom to top.
type Stack struct {
	screens []*Screen
	inter   Interaction

	// Resolve evaluates a binding expression. A field rather than a package
	// global so that the editor's preview can hand its own stack sample data
	// while the game's reads the level; installing one globally would make the
	// two fight over whichever ran last.
	Resolve func(expr string) (Value, bool)

	// Load reads a screen by name. Nil uses the cache over LoadScreen; tests
	// and the editor's preview supply their own.
	Load func(name string) (*Screen, error)

	fbW, fbH float32

	// scopeID distinguishes this stack's video players from another stack's.
	// Assigned on first use rather than at construction, because Game is a
	// zero-value package variable and there is nowhere to construct it.
	scopeID uint64
}

// Game is the running game's stack. The UI editor holds its own.
var Game Stack

// ---------- membership ----------

// Show puts a screen on the stack, or raises it to the top if it is already
// there. The returned screen is the live instance, not the cached document.
func (st *Stack) Show(name string) (*Screen, error) {
	if s := st.Find(name); s != nil {
		st.raise(s)
		return s, nil
	}
	load := st.Load
	if load == nil {
		load = cachedScreenClone
	}
	s, err := load(name)
	if err != nil {
		return nil, err
	}
	st.Push(s)
	return s, nil
}

// Push puts an already-loaded screen on top.
func (st *Stack) Push(s *Screen) {
	if s == nil {
		return
	}
	st.screens = append(st.screens, s)
	if OnScreenShown != nil {
		OnScreenShown(s)
	}
	hooks.RecordUIEvent(hooks.UIEvent{Atom: "shown", Screen: s.ID})
}

// Hide takes a screen off the stack by name.
func (st *Stack) Hide(name string) bool {
	for i, s := range st.screens {
		if s.ID == name {
			st.removeAt(i)
			return true
		}
	}
	return false
}

// PopTop takes the topmost screen off, and returns it.
func (st *Stack) PopTop() *Screen {
	if len(st.screens) == 0 {
		return nil
	}
	s := st.screens[len(st.screens)-1]
	st.removeAt(len(st.screens) - 1)
	return s
}

// Reset clears the stack with no events. Used when play begins and ends, where
// a :hidden: for every screen would be noise posted at a runtime that is about
// to be torn down anyway.
func (st *Stack) Reset() {
	st.screens = nil
	st.inter = Interaction{}
}

// Screens are the screens up, bottom to top — which is draw order.
func (st *Stack) Screens() []*Screen { return st.screens }

// Top is the topmost screen, or nil.
func (st *Stack) Top() *Screen {
	if len(st.screens) == 0 {
		return nil
	}
	return st.screens[len(st.screens)-1]
}

// Find is the screen with an ID, or nil.
func (st *Stack) Find(name string) *Screen {
	for _, s := range st.screens {
		if s.ID == name {
			return s
		}
	}
	return nil
}

// Interaction is the pointer and focus state, for the drawing layer.
func (st *Stack) Interaction() *Interaction { return &st.inter }

func (st *Stack) removeAt(i int) {
	s := st.screens[i]
	st.screens = append(st.screens[:i], st.screens[i+1:]...)
	st.clearTargetsFor(s.ID)
	if OnScreenHidden != nil {
		OnScreenHidden(s)
	}
	hooks.RecordUIEvent(hooks.UIEvent{Atom: "hidden", Screen: s.ID})
}

func (st *Stack) raise(s *Screen) {
	for i, cur := range st.screens {
		if cur == s {
			st.screens = append(append(st.screens[:i], st.screens[i+1:]...), s)
			return
		}
	}
}

func (st *Stack) clearTargetsFor(screen string) {
	if st.inter.Hover.Screen == screen {
		st.inter.Hover = Target{}
	}
	if st.inter.Press.Screen == screen {
		st.inter.Press = Target{}
	}
	if st.inter.Focus.Screen == screen {
		st.inter.Focus = Target{}
		st.inter.Caret, st.inter.Sel = 0, 0
	}
}

// ---------- derived semantics ----------

// CapturesInput reports whether any screen up is modal, in which case gameplay
// sees no input at all.
func (st *Stack) CapturesInput() bool {
	for _, s := range st.screens {
		if s.Modal {
			return true
		}
	}
	return false
}

// Pauses reports whether gameplay should stop ticking.
func (st *Stack) Pauses() bool {
	for _, s := range st.screens {
		if s.Pauses {
			return true
		}
	}
	return false
}

// WantsCursor reports whether mouse-look should be released.
func (st *Stack) WantsCursor() bool {
	for _, s := range st.screens {
		if s.ShowsCursor {
			return true
		}
	}
	return false
}

// ---------- the frame ----------

// Tick solves every screen for the framebuffer and refreshes every binding.
//
// Bindings are polled here, once, rather than at draw time, because the layout
// solve measures the text a binding produced: a label that auto-sizes has to
// know its string before its rect is decided.
func (st *Stack) Tick(dt, fbW, fbH float32) {
	st.fbW, st.fbH = fbW, fbH
	for _, s := range st.screens {
		s.Root.Walk(func(_, n *Node) bool {
			bindText(st.Resolve, n)
			bindValue(st.Resolve, n)
			if n.Kind == KindVideo {
				st.tickVideo(s, n, dt)
			}
			return true
		})
		s.Solve(fbW, fbH)
	}
	st.dropStaleTargets()
}

// tickVideo drives one video node's player and posts `:finished:` when a
// non-looping clip runs out.
//
// A hidden video keeps playing. That is deliberate: a HUD element faded out for
// a second and a background loop behind a menu both expect to come back where
// they would have been, and "stop the world while you cannot see it" is the
// behaviour nobody can predict. A video that should stop when it is hidden is a
// script writing `playing: false`, which is one line and says so.
func (st *Stack) tickVideo(s *Screen, n *Node, dt float32) {
	if VideoDriver == nil {
		return
	}
	VideoDriver(st.videoKey(s, n), n, dt)
	if n.VideoJustEnded() {
		hooks.RecordUIEvent(hooks.UIEvent{Atom: "finished", Screen: s.ID, Widget: n.ID})
	}
}

// videoKey names one widget's player, uniquely across every stack in the
// process.
func (st *Stack) videoKey(s *Screen, n *Node) string {
	if st.scopeID == 0 {
		nextStackScope++
		st.scopeID = nextStackScope
	}
	return itoa(int(st.scopeID)) + "/" + s.ID + "/" + n.ID
}

// dropStaleTargets forgets a hover, press or focus whose widget has gone —
// hidden by a script, or replaced when its screen was reloaded. Without it a
// press could be released onto a node that no longer exists.
func (st *Stack) dropStaleTargets() {
	check := func(tgt *Target, needFocusable bool) {
		if !tgt.Valid() {
			return
		}
		s := st.Find(tgt.Screen)
		if s == nil {
			*tgt = Target{}
			return
		}
		n := s.Find(tgt.Node)
		if n == nil || (needFocusable && !s.CanFocus(n)) {
			*tgt = Target{}
		}
	}
	check(&st.inter.Hover, false)
	check(&st.inter.Press, false)
	check(&st.inter.Focus, true)
}

// ---------- input ----------

// inputScreens are the screens eligible for input, top-most first.
//
// Everything below the topmost modal screen is inert: the HUD under a pause
// menu stops hovering. That is what Modal means, and it is the only thing it
// means — a modal screen does not stop what is beneath it from *drawing*, which
// is what a full-screen translucent panel is for.
func (st *Stack) inputScreens() []*Screen {
	first := 0
	for i, s := range st.screens {
		if s.Modal {
			first = i
		}
	}
	return st.screens[first:]
}

// canvasPoint converts device pixels to one screen's canvas units. Per screen,
// because two screens up at once can have different reference resolutions.
func (st *Stack) canvasPoint(s *Screen, px, py float32) (float32, float32) {
	scale := s.Canvas.Scale(st.fbW, st.fbH)
	if scale <= 0 {
		scale = 1
	}
	return px / scale, py / scale
}

// hitAt is the topmost interactive or blocking widget under a device-pixel
// point, and the screen it belongs to.
func (st *Stack) hitAt(px, py float32) (*Screen, *Node) {
	eligible := st.inputScreens()
	for i := len(eligible) - 1; i >= 0; i-- {
		s := eligible[i]
		x, y := st.canvasPoint(s, px, py)
		if n := s.HitTest(x, y, HitInput); n != nil {
			return s, n
		}
	}
	return nil, nil
}

// PointerMove updates hover and drags a held slider. It reports whether the UI
// consumed the motion, which for a mouse means "do not also turn the camera".
func (st *Stack) PointerMove(px, py float32) bool {
	if s, n := st.pressed(); n != nil && n.Kind == KindSlider {
		x, y := st.canvasPoint(s, px, py)
		st.dragSlider(s, n, x, y)
		return true
	}

	s, n := st.hitAt(px, py)
	var now Target
	if n != nil {
		now = Target{Screen: s.ID, Node: n.ID}
	}
	if now != st.inter.Hover {
		if st.inter.Hover.Valid() {
			hooks.RecordUIEvent(hooks.UIEvent{Atom: "unhover",
				Screen: st.inter.Hover.Screen, Widget: st.inter.Hover.Node})
		}
		if now.Valid() {
			hooks.RecordUIEvent(hooks.UIEvent{Atom: "hover", Screen: now.Screen, Widget: now.Node})
		}
		st.inter.Hover = now
		st.inter.NavDriven = false
	}
	return st.CapturesInput()
}

// PointerDown presses a widget and moves focus to it.
func (st *Stack) PointerDown(px, py float32) bool {
	s, n := st.hitAt(px, py)
	if n == nil {
		return st.CapturesInput()
	}

	st.inter.NavDriven = false
	if n.Enabled && n.Kind.Interactive() {
		st.inter.Press = Target{Screen: s.ID, Node: n.ID}
		st.setFocus(s, n)
		if n.Kind == KindSlider {
			x, y := st.canvasPoint(s, px, py)
			st.dragSlider(s, n, x, y)
		}
		if n.Kind == KindInput {
			x, _ := st.canvasPoint(s, px, py)
			st.placeCaret(n, x)
		}
	}
	// A disabled or merely blocking widget still swallows the click: it must
	// not reach whatever is behind it.
	return true
}

// PointerUp activates a widget when the release lands on the one that was
// pressed, and clears the press either way.
func (st *Stack) PointerUp(px, py float32) bool {
	press := st.inter.Press
	st.inter.Press = Target{}
	if !press.Valid() {
		return st.CapturesInput()
	}

	s, n := st.hitAt(px, py)
	if n != nil && press.Is(s.ID, n.ID) && n.Kind != KindSlider {
		st.activate(s, n)
	}
	return true
}

// Wheel is consumed only under a modal screen, so that the wheel does not zoom
// or switch weapons behind an open menu. There is no scroll view in this
// version for it to do anything else with.
func (st *Stack) Wheel(dy float32) bool { return st.CapturesInput() }

// Navigate moves focus in a direction.
func (st *Stack) Navigate(d Dir) bool {
	eligible := st.inputScreens()
	if len(eligible) == 0 {
		return false
	}
	s := eligible[len(eligible)-1]

	from := st.focusedOn(s)
	next := Neighbour(s, from, d)
	if next == nil {
		// Nothing that way. Still consumed under a modal screen, or the arrow
		// key would also strafe the player behind the menu.
		return st.CapturesInput()
	}
	st.setFocus(s, next)
	st.inter.NavDriven = true
	hooks.RecordUIEvent(hooks.UIEvent{Atom: "hover", Screen: s.ID, Widget: next.ID})
	return true
}

// Activate presses the focused widget, as Enter or a gamepad's south button.
func (st *Stack) Activate() bool {
	s, n := st.focused()
	if n == nil {
		return st.CapturesInput()
	}
	st.activate(s, n)
	return true
}

// Cancel closes the topmost modal screen, after posting :cancel: so a script
// can react to the dismissal.
//
// Escape always closes rather than deferring to whether a script handled it:
// message delivery is asynchronous, so "was it handled" is not a question this
// can answer in the frame the key was pressed, and a menu that sometimes
// ignores Escape is worse than one that always closes. A script that wants
// otherwise can show the screen again.
func (st *Stack) Cancel() bool {
	for i := len(st.screens) - 1; i >= 0; i-- {
		if !st.screens[i].Modal {
			continue
		}
		hooks.RecordUIEvent(hooks.UIEvent{Atom: "cancel", Screen: st.screens[i].ID})
		st.removeAt(i)
		return true
	}
	return false
}

// TypeText inserts into the focused text field.
func (st *Stack) TypeText(text string) bool {
	s, n := st.focusedInput()
	if n == nil {
		return false
	}
	if n.TextIsBound() {
		// A bound field shows what the level says; typing into it would be
		// overwritten on the next tick by the poll.
		return true
	}
	v := n.Text.Value
	caret := clampInt(st.inter.Caret, 0, len(v))
	n.Text.Value = v[:caret] + text + v[caret:]
	st.inter.Caret = caret + len(text)
	st.postChange(s, n)
	return true
}

// EditAction is one of the editing keys a text field understands.
type EditAction int

const (
	EditNone EditAction = iota
	EditBackspace
	EditDelete
	EditLeft
	EditRight
	EditHome
	EditEnd
	EditSubmit
)

// EditText applies an editing key to the focused text field.
func (st *Stack) EditText(a EditAction) bool {
	s, n := st.focusedInput()
	if n == nil {
		return false
	}
	v := n.Text.Value
	caret := clampInt(st.inter.Caret, 0, len(v))

	switch a {
	case EditBackspace:
		if n.TextIsBound() || caret == 0 {
			return true
		}
		prev := prevRune(v, caret)
		n.Text.Value = v[:prev] + v[caret:]
		st.inter.Caret = prev
		st.postChange(s, n)
	case EditDelete:
		if n.TextIsBound() || caret >= len(v) {
			return true
		}
		n.Text.Value = v[:caret] + v[nextRune(v, caret):]
		st.postChange(s, n)
	case EditLeft:
		st.inter.Caret = prevRune(v, caret)
	case EditRight:
		st.inter.Caret = nextRune(v, caret)
	case EditHome:
		st.inter.Caret = 0
	case EditEnd:
		st.inter.Caret = len(v)
	case EditSubmit:
		hooks.RecordUIEvent(hooks.UIEvent{Atom: "submit",
			Screen: s.ID, Widget: n.ID, Str: n.Text.Shown()})
	}
	return true
}

// ---------- the pieces the input methods share ----------

func (st *Stack) activate(s *Screen, n *Node) {
	if !n.Enabled {
		return
	}
	switch n.Kind {
	case KindToggle:
		n.Value.On = !n.Value.On
		hooks.RecordUIEvent(hooks.UIEvent{Atom: "change",
			Screen: s.ID, Widget: n.ID, Bool: n.Value.On, Num: boolNum(n.Value.On), Toggle: true})
	case KindInput:
		st.setFocus(s, n)
		return
	}
	hooks.RecordUIEvent(hooks.UIEvent{Atom: "click", Screen: s.ID, Widget: n.ID})
}

func (st *Stack) postChange(s *Screen, n *Node) {
	ev := hooks.UIEvent{Atom: "change", Screen: s.ID, Widget: n.ID}
	switch n.Kind {
	case KindInput:
		ev.Str = n.Text.Shown()
		ev.Text = true
	case KindToggle:
		ev.Bool = n.Value.On
		ev.Num = boolNum(n.Value.On)
		ev.Toggle = true
	default:
		ev.Num = float64(n.Value.Current())
	}
	hooks.RecordUIEvent(ev)
}

// dragSlider maps a pointer position onto a slider's value, honouring its fill
// direction and its step.
func (st *Stack) dragSlider(s *Screen, n *Node, x, y float32) {
	r := n.Solved()
	if r.W <= 0 || r.H <= 0 {
		return
	}
	var f float32
	switch n.Value.Dir {
	case FillRightLeft:
		f = 1 - (x-r.X)/r.W
	case FillBottomTop:
		f = 1 - (y-r.Y)/r.H
	case FillTopBottom:
		f = (y - r.Y) / r.H
	default:
		f = (x - r.X) / r.W
	}

	span := n.Value.Ceiling() - n.Value.Min
	v := n.Value.Min + clamp01(f)*span
	if n.Value.Step > 0 {
		steps := float32(int((v-n.Value.Min)/n.Value.Step + 0.5))
		v = n.Value.Min + steps*n.Value.Step
	}
	if v == n.Value.Value {
		return
	}
	if !n.SetValue(v) {
		// A bound slider is showing the level's number, not holding one.
		return
	}
	st.postChange(s, n)
}

func (st *Stack) placeCaret(n *Node, x float32) {
	// Left-aligned approximation: the drawing layer offsets by alignment, and
	// duplicating that here would need the atlas, which this package does have
	// but which would tie caret placement to a font lookup on every click. A
	// text field is left-aligned in every real use.
	st.inter.Caret = len(n.Text.Value)
	_ = x
}

// Focus moves keyboard and gamepad focus to a widget by name, and reports
// whether it landed. A hidden or disabled widget refuses it rather than trapping
// focus somewhere the player cannot see.
func (st *Stack) Focus(screen, node string) bool {
	s := st.Find(screen)
	if s == nil {
		return false
	}
	n := s.Find(node)
	if !s.CanFocus(n) {
		return false
	}
	st.setFocus(s, n)
	st.inter.NavDriven = true
	return true
}

// ClearFocus takes focus off whatever has it.
func (st *Stack) ClearFocus() {
	st.inter.Focus = Target{}
	st.inter.Caret, st.inter.Sel = 0, 0
}

func (st *Stack) setFocus(s *Screen, n *Node) {
	if st.inter.Focus.Is(s.ID, n.ID) {
		return
	}
	st.inter.Focus = Target{Screen: s.ID, Node: n.ID}
	st.inter.Caret = len(n.Text.Value)
	st.inter.Sel = st.inter.Caret
}

func (st *Stack) focused() (*Screen, *Node) { return st.nodeAt(st.inter.Focus) }
func (st *Stack) pressed() (*Screen, *Node) { return st.nodeAt(st.inter.Press) }

func (st *Stack) focusedInput() (*Screen, *Node) {
	s, n := st.focused()
	if n == nil || n.Kind != KindInput || !n.Enabled {
		return nil, nil
	}
	return s, n
}

func (st *Stack) focusedOn(s *Screen) *Node {
	if st.inter.Focus.Screen != s.ID {
		return nil
	}
	return s.Find(st.inter.Focus.Node)
}

func (st *Stack) nodeAt(t Target) (*Screen, *Node) {
	if !t.Valid() {
		return nil, nil
	}
	s := st.Find(t.Screen)
	if s == nil {
		return nil, nil
	}
	n := s.Find(t.Node)
	if n == nil {
		return nil, nil
	}
	return s, n
}

func boolNum(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// prevRune and nextRune move a byte offset by one whole rune, so a caret never
// lands inside a multi-byte character.
func prevRune(s string, i int) int {
	if i <= 0 {
		return 0
	}
	i--
	for i > 0 && s[i]&0xC0 == 0x80 {
		i--
	}
	return i
}

func nextRune(s string, i int) int {
	if i >= len(s) {
		return len(s)
	}
	i++
	for i < len(s) && s[i]&0xC0 == 0x80 {
		i++
	}
	return i
}
