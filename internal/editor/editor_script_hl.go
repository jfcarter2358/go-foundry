package editor

// editor_script_hl.go — the script buffer's colouring.
//
// The buffer is still an ImGui InputTextMultiline: it owns the cursor, the
// selection, the clipboard and the undo history, and none of that is worth
// reimplementing for the sake of colour. What is done instead is to draw the
// widget's glyphs in a fully transparent colour and paint coloured ones over
// the top, in the widget's own child window so that the two share a coordinate
// space exactly. See drawScriptEditorText for how that alignment is kept.
//
// The classification itself belongs to the language (internal/flang), not to
// this file: a keyword added to flang has to be a keyword here on the same day,
// and the only way to guarantee that is for both to read the same table.

import (
	"strings"

	"foundry/internal/flang"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// scriptHLChunk is a run of one line drawn in a single colour.
type scriptHLChunk struct {
	Text  string
	Class flang.Class
}

// scriptHighlightLines classifies src and cuts the result at line boundaries.
//
// Lines are what the drawing wants: it places text a line at a time, and it
// skips the lines that are scrolled out of sight, which is only cheap if the
// work is already divided that way.
func scriptHighlightLines(src string) [][]scriptHLChunk {
	spans := flang.Highlight(src)
	lines := strings.Split(src, "\n")
	out := make([][]scriptHLChunk, len(lines))

	next := 0 // the first span that may still touch this line
	at := 0   // byte offset of the current line's start
	for i, line := range lines {
		start, end := at, at+len(line)
		at = end + 1 // past the newline

		var chunks []scriptHLChunk
		cut := start
		for next < len(spans) && spans[next].Start < end {
			s := spans[next]
			from, to := max(s.Start, start), min(s.End, end)
			if to <= from {
				next++
				continue
			}
			if from > cut {
				chunks = append(chunks, scriptHLChunk{Text: src[cut:from]})
			}
			chunks = append(chunks, scriptHLChunk{Text: src[from:to], Class: s.Class})
			cut = to
			if s.End > end {
				// A span that runs past this line — which nothing in flang
				// currently does, since both comments and strings stop at a
				// newline — is left for the next line to finish.
				break
			}
			next++
		}
		if cut < end {
			chunks = append(chunks, scriptHLChunk{Text: src[cut:end]})
		}
		out[i] = chunks
	}
	return out
}

// HighlightLines returns the buffer split into lines of coloured chunks,
// reclassifying only when the text has actually changed.
//
// The cache is keyed on the text rather than on the debounce the diagnostics
// use: an error list that flickered through every half-typed state would be
// noise, but colour that lagged the cursor by a third of a second would just
// look broken.
func (d *scriptDoc) HighlightLines() [][]scriptHLChunk {
	if d.hlLines == nil || d.hlSrc != d.Text {
		d.hlLines = scriptHighlightLines(d.Text)
		d.hlSrc = d.Text
		d.hlWidth = -1
	}
	return d.hlLines
}

// LineWidth returns the width in pixels of the longest line, measured with the
// font the buffer is drawn in.
//
// Measured once per edit and kept, because it is wanted every frame — it is
// what the text area has to be sized to for the buffer to scroll horizontally
// — and measuring every line of a long file every frame would not be.
func (d *scriptDoc) LineWidth() float32 {
	d.HighlightLines() // whose rebuild is what invalidates the measurement
	if d.hlWidth >= 0 {
		return d.hlWidth
	}
	var w float32
	for _, line := range strings.Split(d.Text, "\n") {
		if x := imgui.CalcTextSize(line, false, 0).X; x > w {
			w = x
		}
	}
	d.hlWidth = w
	return w
}

// scriptHLPalette is the colour per class.
//
// Chosen so that the four things a reader of a flang script is actually looking
// for are the four that stand furthest apart: what is a message (atoms), what
// is addressed (`@id`, `#tag`), what the engine provides (builtins), and what
// is only a comment. Types and keywords are deliberately near neighbours —
// `int` in a signature is structure, not content — and operators sit just below
// plain text so that punctuation recedes.
var scriptHLPalette = map[flang.Class]imgui.Vec4{
	flang.ClassComment:  {X: 0.42, Y: 0.46, Z: 0.58, W: 1},
	flang.ClassString:   {X: 0.76, Y: 0.91, Z: 0.55, W: 1},
	flang.ClassNumber:   {X: 0.97, Y: 0.55, Z: 0.42, W: 1},
	flang.ClassKeyword:  {X: 0.78, Y: 0.57, Z: 0.92, W: 1},
	flang.ClassType:     {X: 0.51, Y: 0.67, Z: 1.00, W: 1},
	flang.ClassAtom:     {X: 0.94, Y: 0.44, Z: 0.47, W: 1},
	flang.ClassBuiltin:  {X: 1.00, Y: 0.80, Z: 0.42, W: 1},
	flang.ClassAddress:  {X: 0.54, Y: 0.87, Z: 1.00, W: 1},
	flang.ClassOperator: {X: 0.62, Y: 0.68, Z: 0.75, W: 1},
	flang.ClassInvalid:  {X: 1.00, Y: 0.35, Z: 0.35, W: 1},
}

// scriptHLPacked is the same table in the form a draw list takes, packed once
// rather than per chunk per frame.
var scriptHLPacked = func() map[flang.Class]imgui.PackedColor {
	out := make(map[flang.Class]imgui.PackedColor, len(scriptHLPalette))
	for class, col := range scriptHLPalette {
		out[class] = imgui.PackedColorFromVec4(col)
	}
	return out
}()

// scriptHLColor returns the colour for a class, falling back to plainCol —
// which is whatever the theme's text colour happens to be — for ClassPlain and
// for any class the palette has not been told about.
func scriptHLColor(class flang.Class, plainCol imgui.PackedColor) imgui.PackedColor {
	if c, ok := scriptHLPacked[class]; ok {
		return c
	}
	return plainCol
}

// isBlank reports whether s is only spaces and tabs, and so has nothing worth
// drawing — indentation is most of a script's plain runs.
func isBlank(s string) bool {
	return strings.TrimLeft(s, " \t") == ""
}
