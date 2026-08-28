package uidraw

// uidraw_pass.go — the whole of drawing a screen, in one call.
//
// Solve, build, render. Kept apart from the GL file so that the ordering — and
// the fact that there is only one correct one — is stated in one place rather
// than reconstructed at each call site.

import (
	"foundry/internal/ui"
)

// RenderScreen solves a screen for a framebuffer, builds its geometry and draws
// it.
//
// The list is a parameter rather than a package global so that a caller can
// keep one per window: two GL contexts drawing in the same frame would
// otherwise trample each other's vertices.
func RenderScreen(l *List, s *ui.Screen, in *ui.Interaction, fbW, fbH int32) {
	if s == nil || fbW <= 0 || fbH <= 0 {
		return
	}
	w, h := float32(fbW), float32(fbH)
	scale := s.Canvas.Scale(w, h)

	s.Solve(w, h)
	l.Reset(s.Canvas.RootRect(w, h))
	Build(l, s, in, scale)
	Render(l, fbW, fbH, scale)
}

// BuildInto solves a screen into an explicit design-space area and builds it,
// without drawing.
//
// This is what the UI editor's canvas uses: it lays a screen out at the design
// resolution chosen in its toolbar rather than at the size of the window it
// happens to be in, which is what makes the numbers on its properties panel the
// numbers the game will use.
func BuildInto(l *List, s *ui.Screen, in *ui.Interaction, area ui.Rect, scale float32) {
	if s == nil {
		return
	}
	s.SolveInto(area)
	l.Reset(area)
	Build(l, s, in, scale)
}
