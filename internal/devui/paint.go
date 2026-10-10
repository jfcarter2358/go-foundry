package devui

// paint.go — the handful of drawing primitives the overlay and the console
// share.
//
// uidraw is a list builder, not a widget toolkit: it knows about rectangles,
// images and laid-out strings, and everything above that is the caller's. These
// are that layer, kept in one file so the two tools look like each other.

import (
	"foundry/internal/gfx"
	"foundry/internal/ui"
	"foundry/internal/uidraw"
	"foundry/internal/uifont"
)

// fontTex is the GL texture the current face's glyph page lives in.
//
// Looked up per call rather than cached: uifont bakes a glyph the first time it
// is asked for, and a page that grew past its texture is re-registered under
// the same name with a new id. Caching it is how a console ends up drawing
// yesterday's atlas.
func fontTex() uint32 { return gfx.Textures[face().TexName] }

// text draws one line with its top-left corner at x, y.
//
// The rectangle handed to AddText is a line tall and as wide as it needs to be:
// alignment is left and top, and nothing wraps, so only the corner matters.
func text(l *uidraw.List, x, y float32, s string, col uint32) {
	if s == "" {
		return
	}
	a := face()
	l.AddText(a, fontTex(),
		ui.Rect{X: x, Y: y, W: 1 << 14, H: a.LineHeight},
		s, ui.AlignLeft, ui.AlignTop, false, col)
}

// panel draws the background every tool sits on: a dark fill and a hairline
// border, so that the text has contrast over whatever the game is drawing
// behind it.
func panel(l *uidraw.List, r ui.Rect) {
	l.AddRect(r, uidraw.White(), colPanel)
	l.AddRectOutline(r, 1, uidraw.White(), colBorder)
}

// rule draws the separator between groups of rows.
func rule(l *uidraw.List, x, y, w float32) {
	l.AddRect(ui.Rect{X: x, Y: y, W: w, H: 1}, uidraw.White(), colRule)
}

// measure is the width one line of text takes in the tools' face.
func measure(s string) float32 {
	w, _ := uifont.Measure(face(), s)
	return w
}

// bake lays every printable ASCII character out once, so that the glyph pages
// they live on are complete before anything is drawn.
//
// uifont bakes a glyph the first time it is measured, and internal/uidraw
// uploads the page well before the UI pass — so a character typed this frame
// would otherwise draw as an empty box for one frame. One frame of boxes under
// the cursor is exactly the kind of small wrongness that makes a debug tool
// feel broken, and the fix is to have asked for them all before the console
// opened.
func bake() {
	const printable = " !\"#$%&'()*+,-./0123456789:;<=>?@" +
		"ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`" +
		"abcdefghijklmnopqrstuvwxyz{|}~"
	uifont.Measure(face(), printable)
}
