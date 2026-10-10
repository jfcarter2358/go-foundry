package devui

// overlay.go — the F3 overlay without imgui.
//
// The rows are render.DebugOverlayLines(), the same ones a development build
// paints with imgui. Only the painting is here, and deliberately so: there is
// one place that knows what the overlay says, and two that know how to put it
// on a screen.
//
// Where it sits is the one difference. imgui auto-sizes its window to its
// contents; here the panel is measured first — every row, for its width, and
// the count for its height — and then drawn, because a list builder has no
// second pass to fit a box to what went in it.

import (
	"foundry/internal/render"
	"foundry/internal/ui"
	"foundry/internal/uidraw"
)

// overlayVisible reports whether the overlay should be drawn this frame. The
// F3 key toggles render.DebugOverlayVisible in every build; the gate here is
// what keeps a player who pressed F3 by accident from finding it.
func overlayVisible() bool { return enabled && render.DebugOverlayVisible }

// buildOverlay appends the overlay to l. It sits at the top left; the console
// sits at the bottom, so the two never have to negotiate.
func buildOverlay(l *uidraw.List) {
	rows := render.DebugOverlayLines()
	if len(rows) == 0 {
		return
	}

	lh := lineHeight()
	w := float32(0)
	for _, r := range rows {
		if r.Rule {
			continue
		}
		if m := measure(r.Text); m > w {
			w = m
		}
	}
	r := ui.Rect{
		X: margin,
		Y: margin,
		W: w + 2*padding,
		H: float32(len(rows))*lh + 2*padding,
	}
	panel(l, r)

	y := r.Y + padding
	for _, row := range rows {
		if row.Rule {
			// Centred in the row it occupies, so a rule reads as a gap with a
			// line in it rather than as an underline on the row above.
			rule(l, r.X+padding, y+lh/2, r.W-2*padding)
		} else {
			text(l, r.X+padding, y, row.Text, colText)
		}
		y += lh
	}
}
