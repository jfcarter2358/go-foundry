package devui

// console_draw.go — the console's pixels.
//
// The panel is measured from the bottom of the window up, because the one row
// that must never move is the one being typed into. Everything else — how many
// rows of scrollback fit, where the rule goes — falls out of that.
//
// Wrapping is done here rather than when a line is appended, and cached, for a
// reason that only shows up on a resize: a line wrapped at the old width and
// stored that way is wrong for the new one, and a console that has to be
// cleared after moving a window is not a console anyone trusts.

import (
	"fmt"

	"foundry/internal/ui"
	"foundry/internal/uidraw"
	"foundry/internal/uifont"

	"github.com/veandco/go-sdl2/sdl"
)

// How much of the window the console covers, and the floor under that. The
// fraction is the Quake one, near enough: enough rows to read a stack of log
// lines, not so many that the game behind it is hidden.
const (
	consoleFraction = 0.45
	minConsoleRows  = 6
)

// viewRows is how many rows of scrollback the panel showed last time it was
// drawn. Read by the scroll keys, which run in the event pump before the frame
// draws — so it is last frame's answer, which is right except on the frame the
// window was resized.
var viewRows = 10

func visibleRows() int { return viewRows }

// pageRows is what Page Up and Page Down move: a screenful less two rows of
// overlap, so that the line you were reading is still on screen afterwards.
func pageRows() int {
	if n := visibleRows() - 2; n > 1 {
		return n
	}
	return 1
}

// consoleRect is where the panel sits in a window of the given size.
func consoleRect(w, h float32) ui.Rect {
	lh := lineHeight()
	ph := h * consoleFraction
	if min := float32(minConsoleRows+2)*lh + 3*padding; ph < min {
		ph = min
	}
	if max := h - 2*margin; ph > max {
		ph = max
	}
	return ui.Rect{X: margin, Y: h - margin - ph, W: w - 2*margin, H: ph}
}

// rewrap lays the scrollback out for a panel of the given text width, reusing
// the last answer when neither the width nor the contents have changed.
func rewrap(textW float32) {
	if console.wrapW == textW && console.wrapSeen == console.gen {
		return
	}
	grew := console.wrapSeen != console.gen
	was := len(console.wrapped)

	console.wrapW = textW
	console.wrapSeen = console.gen
	console.wrapped = console.wrapped[:0]

	a := face()
	var lines []uifont.Line
	for _, ln := range console.back {
		lines = uifont.Layout(a, ln.text, textW, lines)
		if len(lines) == 0 {
			console.wrapped = append(console.wrapped, outLine{kind: ln.kind})
			continue
		}
		for i := range lines {
			// Start and End are byte offsets into the line that was laid out;
			// uifont.Line carries no text of its own, because every other
			// caller already has the string in hand.
			console.wrapped = append(console.wrapped,
				outLine{text: ln.text[lines[i].Start:lines[i].End], kind: ln.kind})
		}
	}

	// A view scrolled back stays on the rows it was showing. The scroll is
	// measured from the bottom, so output arriving underneath it would
	// otherwise slide what is being read up off the panel — in a game that
	// logs every frame, which is the one this exists for, that makes scrolling
	// back useless. Following the tail (scroll 0) is left alone, because there
	// the whole point is to move.
	//
	// Only for new content: a rewrap because the window changed width changes
	// the row count for a reason that has nothing to do with the position.
	if grew && console.scroll > 0 {
		if n := len(console.wrapped) - was; n > 0 {
			console.scroll += n
		}
	}
}

// colorOf is the ink one kind of row is drawn in.
func colorOf(k lineKind) uint32 {
	switch k {
	case kindEcho:
		return colEcho
	case kindDim:
		return colDim
	case kindWarn:
		return colWarn
	case kindErr:
		return colErr
	case kindOK:
		return colOK
	}
	return colText
}

// buildConsole appends the console to l.
func buildConsole(l *uidraw.List, w, h float32) {
	r := consoleRect(w, h)
	panel(l, r)

	lh := lineHeight()
	textX := r.X + padding
	textW := r.W - 2*padding

	// Three fixed rows — a header at the top, a rule and the input line at the
	// bottom — and the scrollback gets whatever is left between them.
	headerY := r.Y + padding
	topY := headerY + lh
	inputY := r.Y + r.H - padding - lh
	ruleY := inputY - lh/2

	rows := int((ruleY - lh/2 - topY) / lh)
	if rows < 1 {
		rows = 1
	}
	viewRows = rows

	drawHeader(l, textX, headerY, textW)
	rewrap(textW)
	drawScrollback(l, textX, topY, textW, rows, lh)

	rule(l, textX, ruleY, textW)
	drawInput(l, textX, inputY, textW)
}

// drawScrollback paints the rows the scroll position selects, oldest at the
// top. Clipped to the panel, so a row that only half fits is cut rather than
// drawn over the border.
func drawScrollback(l *uidraw.List, x, y, w float32, rows int, lh float32) {
	end := len(console.wrapped) - console.scroll
	if end > len(console.wrapped) {
		end = len(console.wrapped)
	}
	start := end - rows
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}

	l.PushClip(ui.Rect{X: x, Y: y, W: w, H: float32(rows) * lh})
	for i := start; i < end; i++ {
		text(l, x, y+float32(i-start)*lh, console.wrapped[i].text, colorOf(console.wrapped[i].kind))
	}
	l.PopClip()
}

// drawInput paints the prompt, the line and the caret.
func drawInput(l *uidraw.List, x, y, w float32) {
	p := prompt()
	text(l, x, y, p, colPrompt)

	lineX := x + measure(p)
	l.PushClip(ui.Rect{X: lineX, Y: y, W: w - (lineX - x), H: lineHeight()})
	text(l, lineX, y, string(console.line), colText)

	// Half a second on, half a second off. A caret that did not blink would be
	// indistinguishable from an `|` someone typed.
	if sdl.GetTicks()%1000 < 500 {
		caretX := lineX + measure(string(console.line[:console.cursor]))
		a := face()
		l.AddRect(ui.Rect{X: caretX, Y: y, W: 2, H: a.Ascent + a.Descent}, uidraw.White(), colCaret)
	}
	l.PopClip()
}

// drawHeader is the row above the scrollback: what this is, on the left, and
// what is true about it right now on the right — how far it has been scrolled,
// or, when it is following the tail, the keys worth knowing.
func drawHeader(l *uidraw.List, x, y, w float32) {
	text(l, x, y, "Foundry developer console", colDim)

	msg := "Tab completes   PgUp/PgDn scrolls   Esc closes"
	col := colDim
	if console.scroll > 0 {
		msg = fmt.Sprintf("scrolled back %d of %d rows — PgDn to follow", console.scroll, len(console.wrapped))
		col = colWarn
	}
	rx := x + w - measure(msg)
	// A window too narrow to hold both loses the hint rather than overlapping
	// the title with it.
	if rx > x+measure("Foundry developer console  ") {
		text(l, rx, y, msg, col)
	}
}
