package devui

// console.go — the tilde console: what it holds, and what the keys do to it.
//
// Two rules shape everything here.
//
// The console owns the keyboard while it is up. Not "mostly owns" — every
// keyboard and text event is consumed, and internal/app blanks the held-key
// snapshot for the frame as well, so that typing `destroy` does not also walk
// the player forward on the D and the E. A debug tool that moves the thing you
// are debugging is worse than no debug tool.
//
// Nothing a line asks for happens while the line is being read. Submitting puts
// the text on a queue; Tick runs it, once, at a defined point in the frame with
// the level in hand. Running it inline would mean spawning entities and loading
// levels from inside the event pump, halfway through a frame that has already
// decided what the world contains.

import (
	"fmt"
	"strings"
	"unicode"

	"foundry/internal/gfx"
	"foundry/internal/script"

	"github.com/veandco/go-sdl2/sdl"
)

// scrollbackLimit is how many lines the console remembers. Beyond it the
// oldest go, the same bounded-buffer bargain the script log makes: a `log()` in
// a tick handler produces sixty lines a second and must cost memory that is
// already allocated.
const scrollbackLimit = 2000

// seedLines is how much of the log already written the console shows the first
// time it is opened. Enough to carry the level load and whatever went wrong
// during it, not so much that the interesting part is a thousand rows up.
const seedLines = 200

// lineKind colours one row of scrollback.
type lineKind uint8

const (
	kindOut  lineKind = iota // ordinary output
	kindEcho                 // the command as it was typed
	kindDim                  // engine log, and other background noise
	kindWarn                 // a warning from the script log
	kindErr                  // a failed command, or a script error
	kindOK                   // a command reporting that it did something
)

// outLine is one row of scrollback.
type outLine struct {
	text string
	kind lineKind
}

// consoleState is the whole of the tool's state.
type consoleState struct {
	open bool

	// The line being edited, as runes: the cursor is a rune index, and a byte
	// index would put it inside a multi-byte character the moment anyone
	// pasted one.
	line   []rune
	cursor int

	// history is every line submitted, oldest first. pos indexes it while
	// browsing with Up and Down, and equals len(history) when the line being
	// edited is a new one; draft is what that new line held before browsing
	// started, so that Down returns to it rather than to nothing.
	history []string
	pos     int
	draft   string

	// scrollback, and how far up it the view has been scrolled — in wrapped
	// rows, from the bottom. Zero is "following the tail", which is what any
	// new output snaps it back to.
	back   []outLine
	scroll int

	// logSeq is how far into the engine's log ring the tail has read. seeded
	// says whether the first open has happened, since that is when the tail
	// starts rather than at startup: a console opened an hour in should show
	// the last few hundred lines, not nothing.
	logSeq uint64
	seeded bool

	// repl is the sticky flang mode: every line is an expression rather than a
	// command. See eval.go.
	repl bool

	// cont holds the lines of a continued entry: a line ending in a backslash
	// is joined to the next one. flang's blocks are three lines at the least
	// (`for`, a body, `end`), and a prompt that could not type one could not
	// loop — which is most of what makes a language prompt worth having.
	cont []string

	// pending is what has been submitted and not yet run. A queue rather than a
	// single line because a paste can submit several, and because a command
	// that queues another (an alias) must not run inside the first.
	pending []string

	// swallowText drops the next text-input event. The keystroke that opens or
	// closes the console also arrives as the character `~`, and without this it
	// lands in the line every single time.
	swallowText bool

	// god re-applies the player's health every tick while it is on. Sticky
	// state rather than a one-shot, because a one-shot top-up lasts until the
	// next rocket. See applyStickies.
	god bool

	// The tab-completion cycle in progress: the candidates it found, and which
	// one is showing. Kept between presses so that a second Tab offers the
	// next candidate rather than re-deriving the same first one; any other key
	// ends it (resetCompletion).
	compAll    []string
	compIdx    int
	compActive bool

	// wrapped is the scrollback laid out for the current panel width, and the
	// two things that invalidate it: a width change (wrapW) and anything
	// appended (gen, against the wrapSeen the last wrap was built from).
	wrapped  []outLine
	wrapW    float32
	wrapSeen int
	gen      int
}

// console is the one instance, because there is one keyboard and one screen.
var console consoleState

// Open reports whether the console is up. internal/app reads it to take the
// keyboard away from gameplay for the frame.
func Open() bool { return console.open }

// Toggle opens or closes the console. The tilde key, and nothing else.
func Toggle() {
	if !enabled {
		return
	}
	console.open = !console.open
	// The keypress that did this is also a `~` on its way to the line.
	console.swallowText = true

	if !console.open {
		// Mouse capture is not restored here. internal/app's uiApplyCursor
		// settles it from whatever the game wants now, which may well be a
		// menu that came up while the console was open.
		return
	}

	// Every glyph the console can draw, baked before anything asks to draw it.
	bake()
	console.scroll = 0
	if !console.seeded {
		console.seeded = true
		seedFromLog()
		printf(kindDim, "Foundry developer console — %s is set.", EnvVar)
		printf(kindDim, "`help` lists the commands. `repl` evaluates flang. Escape or ~ closes.")
	}
	// Typing with the mouse captured means the view spins while you look at
	// the keyboard. Release it now; closing hands the question back to the
	// game.
	gfx.SetMouseLock(false)
}

// seedFromLog fills the scrollback with the tail of the engine log, so that the
// first open shows what has already happened rather than an empty box.
func seedFromLog() {
	all, seq := script.ScriptLog.Since(0)
	console.logSeq = seq
	if len(all) > seedLines {
		all = all[len(all)-seedLines:]
	}
	for _, e := range all {
		appendLogEntry(e)
	}
}

// ---------- output ----------

// printf appends one line of output.
func printf(k lineKind, format string, args ...any) {
	for _, s := range strings.Split(fmt.Sprintf(format, args...), "\n") {
		console.back = append(console.back, outLine{text: s, kind: k})
	}
	if n := len(console.back) - scrollbackLimit; n > 0 {
		console.back = append(console.back[:0], console.back[n:]...)
	}
	console.gen++
}

// clearBack empties the scrollback.
func clearBack() {
	console.back = console.back[:0]
	console.scroll = 0
	console.gen++
}

// appendLogEntry turns one entry from the engine's log ring into a row.
func appendLogEntry(e script.LogEntry) {
	k := kindDim
	switch e.Level {
	case script.LogWarn:
		k = kindWarn
	case script.LogError:
		k = kindErr
	}
	prefix := ""
	if e.Source != "" {
		prefix = e.Source + ": "
	}
	if e.HasPos() {
		prefix += e.Pos.String() + ": "
	}
	printf(k, "%s%s", prefix, e.Text)
}

// drainLog pulls whatever the engine and the scripts have logged since the last
// call into the scrollback. Called once a frame while the console is open: the
// console is a log viewer as well as a prompt, which is most of what it is for
// in a build that has no log window.
func drainLog() {
	entries, seq := script.ScriptLog.Since(console.logSeq)
	console.logSeq = seq
	for _, e := range entries {
		appendLogEntry(e)
	}
}

// ---------- events ----------

// HandleEvent offers one SDL event to the console and reports whether it took
// it. Called before anything else in the frame's event pump.
func HandleEvent(event sdl.Event) bool {
	if !enabled {
		return false
	}
	if !console.open {
		// The key that opens it is taken here rather than in the frame loop's
		// own key switch, because this runs ahead of the game's UI and that
		// switch runs behind it. A modal screen consumes every key it does not
		// recognise, so a console opened from down there could not be opened
		// over a main menu or a pause screen — which is exactly where someone
		// debugging a menu needs it.
		//
		// The cost is that `~` cannot be typed into a game's own text field
		// while the tools are on. Worth it: the only person who has them on is
		// the one who would rather have the console.
		if t, ok := event.(sdl.KeyboardEvent); ok &&
			t.State == sdl.PRESSED && t.Keysym.Scancode == sdl.SCANCODE_GRAVE {
			Toggle()
			return true
		}
		return false
	}
	switch t := event.(type) {
	case sdl.TextInputEvent:
		if console.swallowText {
			console.swallowText = false
			return true
		}
		insert(t.GetText())
		return true

	case sdl.KeyboardEvent:
		if t.State != sdl.PRESSED {
			// Consumed with its key-down: a key released after the console
			// closes must not read as a gameplay key coming up.
			return true
		}
		handleKey(t)
		return true

	case sdl.MouseWheelEvent:
		scrollBy(-int(t.Y) * 3)
		return true

	case sdl.MouseMotionEvent, sdl.MouseButtonEvent:
		// Swallowed so that a click while the console is up is not also a
		// shot. The console has nothing clickable in it.
		return true
	}
	return false
}

// handleKey is one key going down while the console is up.
func handleKey(t sdl.KeyboardEvent) {
	ctrl := t.Keysym.Mod&sdl.KMOD_CTRL != 0
	shift := t.Keysym.Mod&sdl.KMOD_SHIFT != 0

	switch t.Keysym.Scancode {
	case sdl.SCANCODE_GRAVE, sdl.SCANCODE_ESCAPE:
		Toggle()

	case sdl.SCANCODE_RETURN, sdl.SCANCODE_KP_ENTER:
		submit()

	case sdl.SCANCODE_BACKSPACE:
		if ctrl {
			deleteWordLeft()
		} else if console.cursor > 0 {
			console.line = append(console.line[:console.cursor-1], console.line[console.cursor:]...)
			console.cursor--
		}
		resetCompletion()

	case sdl.SCANCODE_DELETE:
		if console.cursor < len(console.line) {
			console.line = append(console.line[:console.cursor], console.line[console.cursor+1:]...)
		}
		resetCompletion()

	case sdl.SCANCODE_LEFT:
		if ctrl {
			console.cursor = wordLeft(console.line, console.cursor)
		} else if console.cursor > 0 {
			console.cursor--
		}

	case sdl.SCANCODE_RIGHT:
		if ctrl {
			console.cursor = wordRight(console.line, console.cursor)
		} else if console.cursor < len(console.line) {
			console.cursor++
		}

	case sdl.SCANCODE_HOME:
		console.cursor = 0
	case sdl.SCANCODE_END:
		console.cursor = len(console.line)

	case sdl.SCANCODE_UP:
		historyStep(-1)
	case sdl.SCANCODE_DOWN:
		historyStep(1)

	case sdl.SCANCODE_PAGEUP:
		scrollBy(pageRows())
	case sdl.SCANCODE_PAGEDOWN:
		scrollBy(-pageRows())

	case sdl.SCANCODE_TAB:
		complete(shift)

	case sdl.SCANCODE_A:
		if ctrl {
			console.cursor = 0
		}
	case sdl.SCANCODE_E:
		if ctrl {
			console.cursor = len(console.line)
		}
	case sdl.SCANCODE_U:
		if ctrl {
			console.line = console.line[:0]
			console.cursor = 0
			resetCompletion()
		}
	case sdl.SCANCODE_K:
		if ctrl {
			console.line = console.line[:console.cursor]
			resetCompletion()
		}
	case sdl.SCANCODE_W:
		if ctrl {
			deleteWordLeft()
			resetCompletion()
		}
	case sdl.SCANCODE_L:
		if ctrl {
			clearBack()
		}
	case sdl.SCANCODE_C:
		if ctrl {
			// Abandon the line, the way a shell does: echoed so the
			// scrollback records that it was not run.
			if len(console.line) > 0 {
				printf(kindDim, "%s%s^C", prompt(), string(console.line))
			}
			console.line = console.line[:0]
			console.cursor = 0
			console.cont = nil
			endHistoryBrowse()
			resetCompletion()
		}
	case sdl.SCANCODE_V:
		if ctrl {
			if s, err := sdl.GetClipboardText(); err == nil {
				insert(s)
			}
		}
	}
}

// insert types text into the line at the cursor.
//
// Newlines in a paste are submit: pasting three lines runs three commands, in
// order, which is what pasting into a shell does.
func insert(s string) {
	resetCompletion()
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r':
			submit()
		case r == '\t' || unicode.IsPrint(r):
			console.line = append(console.line, 0)
			copy(console.line[console.cursor+1:], console.line[console.cursor:])
			console.line[console.cursor] = r
			console.cursor++
		}
	}
}

// submit queues the line and clears it.
func submit() {
	s := strings.TrimSpace(string(console.line))
	console.line = console.line[:0]
	console.cursor = 0
	resetCompletion()
	endHistoryBrowse()

	printf(kindEcho, "%s%s", prompt(), s)
	if s != "" {
		// Recorded as it was typed, one fragment at a time: Up has to put
		// something back in a single-line editor, and half of a continued
		// entry is something. A joined entry with a newline in it is not.
		if n := len(console.history); n == 0 || console.history[n-1] != s {
			console.history = append(console.history, s)
		}
		console.pos = len(console.history)
	}
	console.scroll = 0

	// A trailing backslash means "and the next line too".
	if strings.HasSuffix(s, "\\") {
		console.cont = append(console.cont, strings.TrimSuffix(s, "\\"))
		return
	}
	if len(console.cont) > 0 {
		s = strings.Join(append(console.cont, s), "\n")
		console.cont = nil
	}
	if strings.TrimSpace(s) == "" {
		return
	}
	console.pending = append(console.pending, s)
}

// prompt is what precedes the line being typed, and what a submitted line is
// echoed behind.
func prompt() string {
	switch {
	case len(console.cont) > 0:
		return "...    "
	case console.repl:
		return "flang> "
	}
	return "] "
}

// ---------- history ----------

// historyStep walks the history: -1 towards older lines, +1 towards newer.
func historyStep(dir int) {
	if len(console.history) == 0 {
		return
	}
	if console.pos == len(console.history) {
		if dir > 0 {
			return
		}
		// Leaving the line being edited: keep it, so Down comes back to it.
		console.draft = string(console.line)
	}
	p := console.pos + dir
	if p < 0 {
		p = 0
	}
	if p > len(console.history) {
		p = len(console.history)
	}
	console.pos = p

	s := console.draft
	if p < len(console.history) {
		s = console.history[p]
	}
	console.line = append(console.line[:0], []rune(s)...)
	console.cursor = len(console.line)
	resetCompletion()
}

// endHistoryBrowse puts the history cursor back on the line being edited.
func endHistoryBrowse() {
	console.pos = len(console.history)
	console.draft = ""
}

// ---------- scrolling ----------

// scrollBy moves the view up (positive) or down (negative) by rows, clamped so
// that it can neither pass the top of the scrollback nor leave the tail.
func scrollBy(rows int) {
	console.scroll += rows
	if console.scroll < 0 {
		console.scroll = 0
	}
	if max := len(console.wrapped) - visibleRows(); console.scroll > max {
		if max < 0 {
			max = 0
		}
		console.scroll = max
	}
}

// ---------- word motion ----------

func wordLeft(s []rune, i int) int {
	for i > 0 && isWordSep(s[i-1]) {
		i--
	}
	for i > 0 && !isWordSep(s[i-1]) {
		i--
	}
	return i
}

func wordRight(s []rune, i int) int {
	for i < len(s) && !isWordSep(s[i]) {
		i++
	}
	for i < len(s) && isWordSep(s[i]) {
		i++
	}
	return i
}

func deleteWordLeft() {
	to := wordLeft(console.line, console.cursor)
	console.line = append(console.line[:to], console.line[console.cursor:]...)
	console.cursor = to
}

func isWordSep(r rune) bool { return r == ' ' || r == '\t' || r == '.' || r == ',' }
