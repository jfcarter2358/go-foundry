package editor

// editor_log_window.go — the log window's lifecycle.
//
// Same shape as the script editor and the model editor: a handle that is nil
// when closed, an Open that raises rather than duplicating, a Close that requests
// rather than acts, and an OnClose that runs with this window's GL context
// current. RenderSecondaryWindows and RouteWindowEvent find it on their own once
// NewUIWindow returns.
//
// Being a separate window rather than a panel in the main one is the point: the
// reason script output was useless before is that it went somewhere you could
// not look at while the game had the screen. A pop-out can sit on a second
// monitor, or beside the script editor, and keep scrolling while you play.

import (
	"foundry/internal/script"
	"foundry/internal/uiwin"
	"log"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

const (
	logWindowW = 820
	logWindowH = 460
)

// logWindow is nil when the window is closed.
var logWindow *uiwin.UIWindow

// LogWindowOpen reports whether the window is up.
func LogWindowOpen() bool { return logWindow != nil }

// OpenLogWindow opens the log viewer, or raises it if it is already up.
func OpenLogWindow() {
	if logWindow != nil {
		logWindow.Win.Raise()
		return
	}
	w, err := uiwin.NewUIWindow("Script Log", logWindowW, logWindowH,
		drawLogWindow, onLogWindowClose)
	if err != nil {
		log.Printf("open log window: %v", err)
		return
	}
	w.ClearColor = [4]float32{0.09, 0.09, 0.11, 1}
	w.HandleEvent = logWindowHandleEvent
	// Assigned last: until this is set LogWindowOpen has to keep saying no, or a
	// Draw during construction would find a half-built window.
	logWindow = w
}

// CloseLogWindow asks for the window to go away at the end of the frame. Safe to
// call from inside its own Draw — see UIWindow.CloseRequested.
func CloseLogWindow() {
	if logWindow != nil {
		logWindow.CloseRequested = true
	}
}

// onLogWindowClose runs with this window's GL context current. The ring is
// engine-wide and keeps filling; closing the window only stops looking at it.
func onLogWindowClose() {
	logWindow = nil
}

// logWindowHandleEvent handles this window's shortcuts.
//
// Runs after ImGui has seen the event, with this window's context current, so
// WantCaptureKeyboard refers to this window's own widgets — the filter box wants
// the keyboard, and a shortcut firing mid-word would be maddening.
func logWindowHandleEvent(event sdl.Event) {
	t, ok := event.(sdl.KeyboardEvent)
	if !ok || t.Type != sdl.KEYDOWN {
		return
	}
	if sdl.GetModState()&sdl.KMOD_CTRL != 0 {
		switch t.Keysym.Scancode {
		case sdl.SCANCODE_L:
			script.ScriptLog.Clear()
		case sdl.SCANCODE_F:
			logWindowFocusFilter = true
		}
		return
	}
	if imgui.CurrentIO().WantCaptureKeyboard() {
		return
	}
	// Escape is deliberately not here: a window closed by a stray keypress is a
	// window you have to keep re-opening, and the title bar's close button says
	// what it does. Same rule as the script editor.
	switch t.Keysym.Scancode {
	case sdl.SCANCODE_SPACE:
		// Pause is the one shortcut worth having bare: the moment something
		// interesting scrolls past is the moment you want it to stop.
		logWindowPaused = !logWindowPaused
	}
}
