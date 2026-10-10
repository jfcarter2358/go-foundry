package uiwin

// keybind_help.go — the F1 keybinding reference overlay.
//
// The overlay renders whatever is in keybindSections. It does not know what is
// in there, and deliberately so: the table describes editor modes, so it has to
// be built above the editor, while this overlay is drawn by the multi-window
// plumbing that sits below it. Splitting the two is what lets both hold.
//
// keybindings.go fills the table in.

import (
	"foundry/internal/gfx"
	"foundry/internal/platform"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// keybindSections is the reference the overlay draws, installed by
// keybindings.go. Empty here so this file builds with no editor at all.
var KeybindSections []KeybindSection

// KeybindHelpVisible controls whether the F1 keybinding overlay is drawn.
var KeybindHelpVisible = false

// Keybind is one row of the reference: the chord and what it does.
type Keybind struct {
	Keys string
	Desc string
}

// KeybindSection groups bindings that share a context (gameplay, one editor
// mode, a placement sub-mode). Active reports whether that context is current;
// nil means "always applies".
type KeybindSection struct {
	Title  string
	Active func() bool
	Binds  []Keybind
}

// HelpPage is a tab of the reference beyond the keybindings — the engine's
// events, the scripting standard library, the object properties.
//
// Draw renders the page's body with whichever imgui context is current, inside
// a scrolling region the overlay has already opened. Like KeybindSections the
// list is filled in from above (app/script_reference.go): this file knows how
// to show a reference and deliberately not what is in one.
//
// It exists because the script editor had nowhere to put this. The window was
// showing the whole language reference folded into the strip under the
// diagnostics pane, which is where you look when something is wrong, and the
// reference is what you read when nothing is. F1 is already the answer to
// "what can I do here" everywhere else in the app.
type HelpPage struct {
	Title string
	Draw  func()
}

// HelpPages are the extra tabs, in the order they are shown.
var HelpPages []HelpPage

// keybindHelpRestoreMouseLock remembers that the overlay released a captured
// cursor, so closing it hands capture back.
var keybindHelpRestoreMouseLock bool

// keybindHelpDrawnIn is the SDL window ID the overlay was last drawn into, or
// 0 while it is closed. It is how the overlay knows it is *arriving* somewhere
// — on that frame it asks imgui to bring it to the front.
//
// The flag alone is not enough. NoFocusOnAppearing (below) stops imgui both
// focusing and raising the window when it reappears, so after the first close
// the overlay kept whatever depth it had in the window stack: anything clicked
// since — the script editor's full-window root, say — sat on top of it, and F1
// looked like it had done nothing at all. First open worked only because a
// window imgui has never seen starts at the front.
//
// Tracking the window rather than only the open/closed edge also covers
// carrying an open reference between windows: each one has its own imgui
// context, so appearing in a second window is another arrival with the same
// problem.
var keybindHelpDrawnIn uint32

// keybindHelpWindow is the SDL window ID the overlay draws into. There is one
// overlay, shown in whichever window has keyboard focus — the model editor and
// the 2D/texture editors are separate OS windows, and a reference that opened
// behind the window you were looking at may as well not have opened. It is
// sticky: losing focus to another application leaves it where it was rather
// than snapping the overlay back to the main window.
var keybindHelpWindow uint32

// KeybindHelpUpdateFocus repoints the overlay at the focused window. Called
// once per frame from the main loop, before anything renders, so F1 pressed in
// a secondary window opens the reference there and clicking to another window
// carries an open reference across with it.
func KeybindHelpUpdateFocus(mainID uint32) {
	var focused uint32
	if win := sdl.GetKeyboardFocus(); win != nil {
		if id, err := win.GetID(); err == nil {
			focused = id
		}
	}
	keybindHelpWindow = KeybindHelpTarget(keybindHelpWindow, focused, mainID, func(id uint32) bool {
		return id == mainID || UIWindowByID(id) != nil
	})
}

// KeybindHelpTarget resolves the overlay's window: the focused one when it is
// a window we still own, else the one it is already in (unless that window has
// since closed), else the main window.
func KeybindHelpTarget(current, focused, mainID uint32, valid func(uint32) bool) uint32 {
	if focused != 0 && valid(focused) {
		return focused
	}
	if current != 0 && valid(current) {
		return current
	}
	return mainID
}

// KeybindHelpVisibleIn reports whether the overlay should be drawn by the
// window with the given SDL ID this frame. Callers use it to decide whether an
// imgui frame is worth opening at all.
func KeybindHelpVisibleIn(id uint32) bool {
	return KeybindHelpVisible && keybindHelpWindow == id
}

// KeybindHelpToggle flips the keybinding overlay on/off.
func KeybindHelpToggle() { KeybindHelpSetVisible(!KeybindHelpVisible) }

// KeybindHelpSetVisible shows or hides the overlay. Opening it while the
// cursor is captured for mouse-look frees the cursor — otherwise the reference
// is on screen with no visible pointer and an unclickable close button — and
// closing it restores whatever capture the mode wanted.
func KeybindHelpSetVisible(visible bool) {
	if visible == KeybindHelpVisible {
		return
	}
	KeybindHelpVisible = visible
	if visible {
		keybindHelpRestoreMouseLock = gfx.Cam.MouseLockWanted
		if keybindHelpRestoreMouseLock {
			gfx.SetMouseLock(false)
		}
		return
	}
	// Closing forgets where it was drawn, so the next open is an arrival
	// again even if it lands in the same window.
	keybindHelpDrawnIn = 0
	if keybindHelpRestoreMouseLock {
		keybindHelpRestoreMouseLock = false
		gfx.SetMouseLock(true)
	}
}

// KeybindHelpHandleEvent processes F1 (toggle) and Escape (close while open).
// It runs before the editor and gameplay handlers in the main loop so the
// overlay opens from any mode, and so closing it with Escape doesn't also drop
// a pending polygon or exit a sub-editor. Returns true if it consumed the
// event.
func KeybindHelpHandleEvent(event sdl.Event) bool {
	t, ok := event.(sdl.KeyboardEvent)
	if !ok || t.Type != sdl.KEYDOWN {
		return false
	}
	switch t.Keysym.Scancode {
	case sdl.SCANCODE_F1:
		KeybindHelpToggle()
		return true
	case sdl.SCANCODE_ESCAPE:
		if KeybindHelpVisible {
			KeybindHelpSetVisible(false)
			return true
		}
	}
	return false
}

// keybind overlay palette
var (
	keybindActiveTitleColor = imgui.Vec4{X: 0.45, Y: 0.80, Z: 1.00, W: 1}
	keybindIdleTitleColor   = imgui.Vec4{X: 0.60, Y: 0.60, Z: 0.65, W: 1}
	keybindKeyColor         = imgui.Vec4{X: 1.00, Y: 0.85, Z: 0.45, W: 1}
	keybindIdleTextColor    = imgui.Vec4{X: 0.55, Y: 0.55, Z: 0.58, W: 1}
)

// keybindHelpArriving reports whether this is the overlay's first frame in the
// window with the given ID — the frame it has to be raised on — and records
// the window so the frames after it are not.
func keybindHelpArriving(id uint32) bool {
	if keybindHelpDrawnIn == id {
		return false
	}
	keybindHelpDrawnIn = id
	return true
}

// KeybindHelpRender draws the overlay in the main window. Must be called
// between ImGuiNewFrame and ImGuiRender; a no-op when the focused window is a
// secondary one, which draws it itself via KeybindHelpRenderInWindow.
func KeybindHelpRender() {
	if !KeybindHelpVisibleIn(MainWindowID) {
		return
	}
	keybindHelpRender(MainWindowID, float32(platform.WinWidth), float32(platform.WinHeight))
}

// KeybindHelpRenderInWindow draws the overlay inside a secondary window's
// imgui frame, sized to that window. Called from RenderSecondaryWindows.
func KeybindHelpRenderInWindow(uw *UIWindow) {
	if !KeybindHelpVisibleIn(uw.ID) {
		return
	}
	keybindHelpRender(uw.ID, float32(uw.W), float32(uw.H))
}

// keybindHelpRender draws the reference centred in a viewport of the given
// size, using whichever imgui context is current. id is the SDL window that
// context belongs to, so the overlay can tell an arrival from a frame it has
// already been drawing in.
func keybindHelpRender(id uint32, w, h float32) {
	// Wider than it was, and a larger share of a small window: the reference
	// now carries three-column tables of the standard library and the object
	// properties, and a cramped one wraps every summary onto four lines.
	winW := w * 0.90
	if winW > 1100 {
		winW = 1100
	}
	winH := h * 0.88

	imgui.SetNextWindowPosV(imgui.Vec2{X: w / 2, Y: h / 2}, imgui.ConditionAlways, imgui.Vec2{X: 0.5, Y: 0.5})
	imgui.SetNextWindowSizeV(imgui.Vec2{X: winW, Y: winH}, imgui.ConditionAlways)
	imgui.SetNextWindowBgAlpha(0.93)

	// Raise it on the frame it arrives in this window. SetNextWindowFocus is
	// an explicit request rather than the appearing heuristic, so it is
	// honoured despite NoFocusOnAppearing; the flag still keeps the overlay
	// from stealing focus on any later frame.
	if keybindHelpArriving(id) {
		imgui.SetNextWindowFocus()
	}

	flags := imgui.WindowFlagsNoResize |
		imgui.WindowFlagsNoMove |
		imgui.WindowFlagsNoCollapse |
		imgui.WindowFlagsNoSavedSettings |
		imgui.WindowFlagsNoFocusOnAppearing |
		imgui.WindowFlagsNoNav

	open := true
	if imgui.BeginV("Reference (F1)", &open, flags) {
		if imgui.BeginTabBarV("##helptabs", imgui.TabBarFlagsFittingPolicyScroll) {
			if imgui.BeginTabItem("Keybindings") {
				keybindHelpDrawBindings()
				imgui.EndTabItem()
			}
			for _, page := range HelpPages {
				if page.Draw == nil {
					continue
				}
				if imgui.BeginTabItem(page.Title) {
					// Each page scrolls in its own region, so a long one
					// cannot push the tab strip off the top of the overlay.
					imgui.BeginChildV("##helppage", imgui.Vec2{}, false, 0)
					page.Draw()
					imgui.EndChild()
					imgui.EndTabItem()
				}
			}
			imgui.EndTabBar()
		}
	}
	imgui.End()

	// The title-bar close button.
	if !open {
		KeybindHelpSetVisible(false)
	}
}

// keybindHelpDrawBindings draws the keybinding tab: every section, in two
// balanced columns.
func keybindHelpDrawBindings() {
	imgui.Text("Bindings for the current mode are highlighted; the rest are dimmed.")
	imgui.Separator()
	imgui.Spacing()

	// Two balanced columns — the full list is far taller than a screen in one
	// column, and scrolling a reference you opened mid-task is worse than
	// reading it.
	imgui.BeginChildV("##keybind_body", imgui.Vec2{}, false, 0)
	left, right := KeybindSplitSections(KeybindSections)
	imgui.ColumnsV(2, "##keybind_cols", false)
	for _, s := range left {
		keybindDrawSection(s)
	}
	imgui.NextColumn()
	for _, s := range right {
		keybindDrawSection(s)
	}
	imgui.Columns()
	imgui.EndChild()
}

// KeybindSplitSections balances the sections across two columns by row count,
// keeping display order within each column.
func KeybindSplitSections(secs []KeybindSection) (left, right []KeybindSection) {
	total := 0
	for _, s := range secs {
		total += keybindSectionRows(s)
	}
	half, running := total/2, 0
	for i, s := range secs {
		if running >= half && i > 0 {
			return secs[:i], secs[i:]
		}
		running += keybindSectionRows(s)
	}
	return secs, nil
}

// keybindSectionRows is a section's height in text rows: the bindings plus its
// header and trailing spacing.
func keybindSectionRows(s KeybindSection) int { return len(s.Binds) + 3 }

// keybindDrawSection renders one section, dimmed when its context is inactive.
func keybindDrawSection(s KeybindSection) {
	active := s.Active == nil || s.Active()

	titleColor := keybindIdleTitleColor
	if active {
		titleColor = keybindActiveTitleColor
	}
	imgui.PushStyleColor(imgui.StyleColorText, titleColor)
	imgui.Text(s.Title)
	imgui.PopStyleColor()
	imgui.Separator()

	// Keys and descriptions line up on a fixed offset rather than nested
	// columns — imgui columns don't nest inside the outer two-column layout.
	const descOffset = 175
	imgui.Indent()
	for _, b := range s.Binds {
		keyColor, descColor := keybindKeyColor, imgui.CurrentStyle().Color(imgui.StyleColorText)
		if !active {
			keyColor, descColor = keybindIdleTextColor, keybindIdleTextColor
		}
		imgui.PushStyleColor(imgui.StyleColorText, keyColor)
		imgui.Text(b.Keys)
		imgui.PopStyleColor()

		imgui.SameLineV(descOffset, 0)
		imgui.PushStyleColor(imgui.StyleColorText, descColor)
		imgui.Text(b.Desc)
		imgui.PopStyleColor()
	}
	imgui.Unindent()
	imgui.Spacing()
	imgui.Spacing()
}
