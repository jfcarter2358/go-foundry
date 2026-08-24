package main

// F1 keybinding reference overlay.
//
// The bindings themselves live where they are handled (input.go, editor.go and
// the per-mode editor files); this file is the single place that *describes*
// them. Sections carry an Active predicate so the overlay can foreground the
// bindings that apply to whatever mode is on screen right now and dim the rest
// — the list is long enough that showing it flat is close to useless.

import (
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

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

// keybindSections is the full reference, in display order. Keep it in sync
// with the handlers: HandleEvent/HandleInput (input.go), EditorHandleEvent
// (editor.go), and the per-mode key handlers in editor_surface2d.go,
// editor_extrusion.go and editor_biome.go.
var keybindSections = []KeybindSection{
	{
		Title:  "Script log",
		Active: LogWindowOpen,
		Binds: []Keybind{
			{"Space", "Pause / resume the view (the log keeps filling)"},
			{"Ctrl+L", "Clear the buffer"},
			{"Ctrl+F", "Jump to the filter box"},
			{"Esc", "Close the window"},
		},
	},
	{
		Title:  "Script editor",
		Active: ScriptEditorOpen,
		Binds: []Keybind{
			{"Ctrl+S / Ctrl+Shift+S", "Save / save as"},
			{"Ctrl+O", "Open a script"},
			{"Ctrl+N", "New script"},
			{"Ctrl+R", "Reload every script from disk"},
			{"Esc", "Close the window (the buffer is kept)"},
		},
	},
	{
		Title:  "Texture editor",
		Active: TextureEditorOpen,
		Binds: []Keybind{
			{"Ctrl+Z / Ctrl+Y", "Undo / redo (Ctrl+Shift+Z redoes too)"},
			{"Left click / drag", "Paint with the current tool"},
			{"Middle-drag", "Pan the canvas"},
			{"Mouse wheel", "Zoom the canvas"},
			{"Click a swatch", "Set the primary colour"},
			{"Right-click a swatch", "Set the secondary colour"},
			{"Double-click a swatch", "Edit that palette slot's colour"},
		},
	},
	{
		Title:  "Model editor",
		Active: ModelEditorOpen,
		Binds: []Keybind{
			{"1 / 2 / 3 / 4", "Vertex / edge / face / bone select"},
			{"G / R / C", "Move / rotate / scale gizmo (C again: local axes)"},
			{"E / Shift+E", "Extrude (again: re-aim along the normal) / always extrude"},
			{"V", "Toggle orbit and fly camera"},
			{"Z", "Toggle wireframe"},
			{"F", "Frame the model"},
			{"P", "Toggle pose mode"},
			{"Delete", "Delete the selected bones or faces"},
			{"Ctrl+Z / Ctrl+Y", "Undo / redo"},
			{"Tab", "Hand focus back to the main window"},
		},
	},
	{
		Title:  "Model editor — pose mode",
		Active: func() bool { return ModelEditorOpen() && modelPoseMode },
		Binds: []Keybind{
			{"I", "Key every posed bone at the playhead"},
			{"Space", "Play / pause"},
			{"Left / Right", "Step one frame"},
			{"Delete", "Delete the selected keyframes"},
			{"Alt+G / Alt+R / Alt+S", "Clear translation / rotation / scale"},
			{"G / R / C", "Pose the selected bones (scale runs on the bone's own axes)"},
			{"Click a diamond", "Select that keyframe (Ctrl to add)"},
			{"Click / drag the timeline", "Scrub the playhead"},
		},
	},
	{
		Title: "Global",
		Binds: []Keybind{
			{"F1", "Toggle this keybinding reference"},
			{"F3", "Toggle debug overlay (FPS, memory, counts)"},
			{"F10", "Toggle anti-aliasing"},
			{"F11", "Toggle fullscreen"},
			{"~ / `", "Toggle the level editor"},
			{"Esc", "Close this reference"},
		},
	},
	{
		Title: "Biome lighting cost switches",
		Binds: []Keybind{
			{"F5", "Biome scatter receives shadows on/off"},
			{"F6", "Rock / moss shadow casting on/off"},
			{"F7", "Foliage shadow casting on/off"},
			{"F8", "Cycle how many cascades scatter casts into"},
			{"F9", "Cycle how many LOD tiers cast real shadows"},
		},
	},
	{
		Title:  "Gameplay",
		Active: func() bool { return !Editor.Active },
		Binds: []Keybind{
			{"W / A / S / D", "Move"},
			{"Space", "Jump (gravity on) / fly up (gravity off)"},
			{"Left Shift", "Fly down (gravity off)"},
			{"Arrow keys", "Turn / look"},
			{"Mouse", "Look (cursor is captured)"},
			{"E", "Interact with whatever you are standing in (fires :interact:)"},
			{"0", "Toggle gravity (fly mode)"},
			{"8", "Toggle entity-vs-world collision (engine-wide)"},
			{"9", "Toggle entity-vs-entity collision (engine-wide)"},
		},
	},
	{
		Title:  "Editor — any mode",
		Active: func() bool { return Editor.Active },
		Binds: []Keybind{
			{"Ctrl+Z", "Undo"},
			{"Ctrl+Y / Ctrl+Shift+Z", "Redo"},
			{"Delete", "Delete the current selection"},
		},
	},
	{
		Title:  "Editor — 3D view",
		Active: func() bool { return Editor.Active && Editor.Mode == Mode3D },
		Binds: []Keybind{
			{"1 / 2 / 3", "Point / edge / face select"},
			{"Z", "Toggle wireframe"},
			{"V", "Toggle orbit and fly camera"},
			{"F", "Frame the selection"},
			{"W / A / S / D", "Fly the camera (orbit: slide the pivot)"},
			{"Space / Left Shift", "Up / down"},
			{"Right-drag / Middle-drag", "Look around, or orbit the pivot"},
			{"Shift+Middle-drag", "Pan"},
			{"Mouse wheel", "Dolly in fly, zoom the orbit radius"},
			{"Left click", "Select whatever the current mode picks"},
			{"Left click (handle)", "Pick a point/edge; repeat clicks cycle overlaps"},
			{"Ctrl+Left click", "Add every point/edge under the cursor"},
			{"Left click (armed)", "Place the spawn point ahead of the camera"},
			{"Drag gizmo arm", "Translate the selection along that axis"},
		},
	},
	{
		Title: "Editor — 2D sector map (its own window)",
		// The map only exists in its own window, and Editor.Mode says Mode2D
		// only inside that window's draw brackets — never while the reference
		// is being rendered — so the section keys off the window instead.
		Active: func() bool { return Editor.Active && Editor2DPoppedOut() },
		Binds: []Keybind{
			{"W / A / S / D", "Pan the map"},
			{"Middle-drag", "Pan the map"},
			{"Mouse wheel", "Zoom"},
			{"Left click / drag", "Select and move a vertex"},
			{"Shift+Left click", "Split the nearest edge, else drop a free point"},
			{"Ctrl+Left click", "Connect two points"},
			{"Right click", "Remove the point under the cursor, else deselect"},
		},
	},
	{
		Title:  "Editor — biome brush (2D)",
		Active: func() bool { return Editor.Active && Editor2DPoppedOut() && Editor.BiomeLayerActive },
		Binds: []Keybind{
			{"Left click", "Add a polygon point; click the first point to close"},
			{"Right click", "Undo the last point while drawing, else delete a sector"},
			{"Enter", "Finish the polygon"},
			{"Esc", "Cancel the polygon"},
		},
	},
	{
		Title:  "Editor — 2D surface editor",
		Active: func() bool { return Editor.Active && Editor.Mode == ModeSurface2D },
		Binds: []Keybind{
			{"Middle-drag", "Pan"},
			{"Mouse wheel", "Zoom"},
			{"Left click / drag", "Select and move a point"},
			{"Shift+Left click", "Place a point (starts an extrusion if idle)"},
			{"Right click", "Delete the nearest terrain point"},
			{"Enter", "Finalize the pending extrusion"},
			{"Delete / Backspace", "Drop the last pending point"},
			{"Esc", "Cancel placement, else leave the surface editor"},
		},
	},
	{
		Title:  "Editor — extrusion placement",
		Active: func() bool { return Editor.Active && Editor.ExtrusionMode },
		Binds: []Keybind{
			{"Left click", "Place a polygon point on the wall plane"},
			{"Tab", "Switch between the outer and inner polygon"},
			{"Enter", "Finalize the extrusion"},
			{"Esc", "Cancel"},
		},
	},
}

// keybindHelpRestoreMouseLock remembers that the overlay released a captured
// cursor, so closing it hands capture back.
var keybindHelpRestoreMouseLock bool

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
	keybindHelpWindow = keybindHelpTarget(keybindHelpWindow, focused, mainID, func(id uint32) bool {
		return id == mainID || UIWindowByID(id) != nil
	})
}

// keybindHelpTarget resolves the overlay's window: the focused one when it is
// a window we still own, else the one it is already in (unless that window has
// since closed), else the main window.
func keybindHelpTarget(current, focused, mainID uint32, valid func(uint32) bool) uint32 {
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
		keybindHelpRestoreMouseLock = Cam.MouseLockWanted
		if keybindHelpRestoreMouseLock {
			SetMouseLock(false)
		}
		return
	}
	if keybindHelpRestoreMouseLock {
		keybindHelpRestoreMouseLock = false
		SetMouseLock(true)
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

// KeybindHelpRender draws the overlay in the main window. Must be called
// between ImGuiNewFrame and ImGuiRender; a no-op when the focused window is a
// secondary one, which draws it itself via KeybindHelpRenderInWindow.
func KeybindHelpRender() {
	if !KeybindHelpVisibleIn(MainWindowID) {
		return
	}
	keybindHelpRender(float32(WinWidth), float32(WinHeight))
}

// KeybindHelpRenderInWindow draws the overlay inside a secondary window's
// imgui frame, sized to that window. Called from RenderSecondaryWindows.
func KeybindHelpRenderInWindow(uw *UIWindow) {
	if !KeybindHelpVisibleIn(uw.ID) {
		return
	}
	keybindHelpRender(float32(uw.W), float32(uw.H))
}

// keybindHelpRender draws the reference centred in a viewport of the given
// size, using whichever imgui context is current.
func keybindHelpRender(w, h float32) {
	winW := w * 0.82
	if winW > 980 {
		winW = 980
	}
	winH := h * 0.82

	imgui.SetNextWindowPosV(imgui.Vec2{X: w / 2, Y: h / 2}, imgui.ConditionAlways, imgui.Vec2{X: 0.5, Y: 0.5})
	imgui.SetNextWindowSizeV(imgui.Vec2{X: winW, Y: winH}, imgui.ConditionAlways)
	imgui.SetNextWindowBgAlpha(0.93)

	flags := imgui.WindowFlagsNoResize |
		imgui.WindowFlagsNoMove |
		imgui.WindowFlagsNoCollapse |
		imgui.WindowFlagsNoSavedSettings |
		imgui.WindowFlagsNoFocusOnAppearing |
		imgui.WindowFlagsNoNav

	open := true
	if imgui.BeginV("Keybindings (F1)", &open, flags) {
		imgui.Text("Bindings for the current mode are highlighted; the rest are dimmed.")
		imgui.Separator()
		imgui.Spacing()

		// Two balanced columns — the full list is far taller than a screen in
		// one column, and scrolling a reference you opened mid-task is worse
		// than reading it.
		left, right := keybindSplitSections(keybindSections)
		imgui.ColumnsV(2, "##keybind_cols", false)
		for _, s := range left {
			keybindDrawSection(s)
		}
		imgui.NextColumn()
		for _, s := range right {
			keybindDrawSection(s)
		}
		imgui.Columns()
	}
	imgui.End()

	// The title-bar close button.
	if !open {
		KeybindHelpSetVisible(false)
	}
}

// keybindSplitSections balances the sections across two columns by row count,
// keeping display order within each column.
func keybindSplitSections(secs []KeybindSection) (left, right []KeybindSection) {
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
