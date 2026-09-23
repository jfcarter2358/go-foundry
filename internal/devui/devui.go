// Package devui is the developer's on-screen tools in a shipped game: the F3
// overlay and the tilde console.
//
// A development build has an editor, a log window, a script editor with a Run
// button and imgui to draw all three with. A release build has none of it —
// deliberately, and internal/app/devtools_release.go says why. But a game that
// has shipped is still a game that has to be debugged, and "rebuild it as a
// development binary" is not an answer when the level only misbehaves in the
// compiled build, on someone else's machine, an hour into a playthrough.
//
// So this package is the small, safe half: it can look at the world and drive
// the script runtime, and it cannot author anything. There is no way from here
// to save a level, write a texture or edit a script, which is why it does not
// need the seam that keeps the editor out of a release binary and can simply be
// compiled into every build.
//
// It is off unless FOUNDRY_DEBUG_ENABLED says otherwise. A player who never
// sets it has a binary whose tilde key does nothing at all.
//
//   - devui.go        — the gate, and the shared colours and metrics
//   - frame.go        — where it joins the engine's frame
//   - overlay.go      — the F3 overlay, drawn without imgui
//   - console.go      — the console's state, keys and history
//   - console_draw.go — the console's pixels
//   - paint.go        — the drawing primitives the two share
//   - commands.go     — what you can type into it
//   - complete.go     — what Tab does
//   - eval.go         — the flang read-eval-print loop
package devui

import (
	"log"
	"os"
	"strings"

	"foundry/internal/hooks"
	"foundry/internal/uidraw"
	"foundry/internal/uifont"
)

// EnvVar is the environment variable that turns the developer tools on.
//
// An environment variable rather than a setting in the game's own options,
// because a setting is a thing a player can find. This is meant to be a thing
// the person who built the game knows about and nobody else trips over.
const EnvVar = "FOUNDRY_DEBUG_ENABLED"

// enabled is the gate. Read at startup and never again, so nothing has to
// reason about the tools appearing halfway through a session.
var enabled bool

// Init reads the environment. Called once, early in the engine's startup.
//
// A development build stays off whatever the variable says. It has the editor,
// the log window and the script editor's Run button already; its F3 overlay is
// drawn with imgui, and turning this one on as well would draw the same rows
// twice, one set on top of the other. The variable is about the build that has
// none of that.
func Init() {
	asked := truthy(os.Getenv(EnvVar))
	enabled = asked && !hooks.DevBuild
	switch {
	case enabled:
		log.Printf("developer tools enabled by %s: F3 for the debug overlay, ~ for the console", EnvVar)
	case asked:
		log.Printf("%s is set, but this is a development build — the editor (~) and its log window already cover it", EnvVar)
	}
}

// Enabled reports whether the developer tools are on in this process.
func Enabled() bool { return enabled }

// SetEnabled is Init's answer, forced. For tests, and for nothing else.
func SetEnabled(on bool) { enabled = on }

// truthy accepts the spellings someone typing this at a shell would expect.
// "true" is what the documentation says; the rest are there so that an
// afternoon is not lost to having written "1".
func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}

// ---------- looks ----------
//
// The tools are drawn with the runtime's own screen-space pass (internal/uidraw),
// which is the only text renderer a release build has. Everything below is in
// device pixels: uidraw is asked to draw at scale 1 rather than at the canvas
// scale a game's HUD uses, so that the overlay is the same size on a 4K display
// as on a 1080p one. A debug readout that shrank with the resolution would be
// unreadable exactly where the frame time matters most.

const (
	// fontPx is the face size asked for. The bitmap fallback rounds it to a
	// whole multiple of its 8-pixel cell, so 16 is two device pixels per cell
	// pixel — crisp, and twice the size of the 1x face, which is too small to
	// read at 4K.
	fontPx = 16

	padding   = 8  // inside a panel, between its edge and its text
	lineExtra = 2  // added to the face's line height, so rows are not touching
	margin    = 10 // between a panel and the edge of the window
)

// fontName is the face the tools are drawn in: none, on purpose.
//
// uifont.Face("") is the compiled-in bitmap face (uifont_fallback.go), and it
// is the right one here for two reasons that a prettier font does not have. It
// is fixed-width, and both the profile table in the overlay and the columns
// every listing command prints are laid out with %-20s padding that a
// proportional face turns into a mess. And it is in the binary: a game that
// ships no font at all, or whose font fails to load, still has a console.
const fontName = ""

// face is the atlas the tools lay text out with.
func face() *uifont.Atlas { return uifont.Face(fontName, fontPx) }

// lineHeight is one row of text, including the gap under it.
func lineHeight() float32 { return face().LineHeight + lineExtra }

// Colours, packed the way uidraw wants them. Deliberately flat and unbranded:
// these are instruments, and a game's own palette showing up in them would make
// it harder to tell the debug layer from the game.
var (
	colPanel   = uidraw.PackColor([4]float32{0.04, 0.05, 0.07, 0.88})
	colBorder  = uidraw.PackColor([4]float32{0.35, 0.40, 0.48, 0.80})
	colRule    = uidraw.PackColor([4]float32{0.35, 0.40, 0.48, 0.55})
	colText    = uidraw.PackColor([4]float32{0.88, 0.91, 0.95, 1})
	colDim     = uidraw.PackColor([4]float32{0.58, 0.62, 0.68, 1})
	colEcho    = uidraw.PackColor([4]float32{0.60, 0.80, 1.00, 1})
	colWarn    = uidraw.PackColor([4]float32{1.00, 0.80, 0.35, 1})
	colErr     = uidraw.PackColor([4]float32{1.00, 0.45, 0.42, 1})
	colOK      = uidraw.PackColor([4]float32{0.55, 0.90, 0.60, 1})
	colPrompt  = uidraw.PackColor([4]float32{0.55, 0.90, 0.60, 1})
	colCaret   = uidraw.PackColor([4]float32{0.88, 0.91, 0.95, 1})
	colScrollT = uidraw.PackColor([4]float32{0.35, 0.40, 0.48, 0.70})
)
