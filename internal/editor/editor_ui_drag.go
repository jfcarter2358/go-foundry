package editor

// editor_ui_drag.go — selecting, moving, resizing and re-anchoring on the
// canvas.
//
// Every gesture pushes exactly one undo entry, on mouse-down, through
// uiBeginEdit. The maths is separated from the SDL plumbing so that the parts
// worth getting wrong — which offsets a handle owns, what a drag does to a
// stretched axis — are ordinary functions a test can call.

import (
	"foundry/internal/ui"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// uiGesture is what a press on the canvas started.
type uiGesture int

const (
	uiGestureNone uiGesture = iota
	uiGestureMove
	uiGestureResize
	uiGestureAnchor
	uiGesturePan
)

// uiHandle names the eight resize grips, in the order uiHandleRects returns
// them.
type uiHandle int

const (
	uiHandleTopLeft uiHandle = iota
	uiHandleTop
	uiHandleTopRight
	uiHandleLeft
	uiHandleRight
	uiHandleBottomLeft
	uiHandleBottom
	uiHandleBottomRight
)

// uiAnchorHandle names the four anchor markers.
type uiAnchorHandle int

const (
	uiAnchorMinMin uiAnchorHandle = iota
	uiAnchorMaxMin
	uiAnchorMinMax
	uiAnchorMaxMax
)

// uiSnapTolerance is how close, in document units, an edge has to come to
// another before it snaps to it.
const uiSnapTolerance = float32(6)

var (
	uiDragGesture uiGesture
	uiDragHandle  uiHandle
	uiDragAnchor  uiAnchorHandle
	uiDragLastX   int32
	uiDragLastY   int32
	uiDragNoSnap  bool
)

// uiCanvasMouseButton starts and ends canvas gestures.
func uiCanvasMouseButton(t *sdl.MouseButtonEvent) {
	w := uiEditorWindow
	if w == nil || uiDoc == nil {
		return
	}
	if t.State == sdl.RELEASED {
		uiDragGesture = uiGestureNone
		uiCanvasDragging = false
		return
	}
	// A press over a panel belongs to the panel.
	if imgui.CurrentIO().WantCaptureMouse() {
		return
	}
	canvas := uiCanvasRect(w)
	if !canvas.Contains(float32(t.X), float32(t.Y)) {
		return
	}

	uiDragLastX, uiDragLastY = t.X, t.Y
	uiDragNoSnap = sdl.GetModState()&sdl.KMOD_ALT != 0

	if t.Button == sdl.ButtonMiddle {
		uiDragGesture = uiGesturePan
		uiCanvasDragging = true
		return
	}
	if t.Button != sdl.ButtonLeft {
		return
	}

	area := uiDesignArea(w)
	pt := mgl32.Vec2{float32(t.X), float32(t.Y)}

	// Handles first: they sit on top of the selection's own edges, so hit
	// testing the node before them would make the corners unreachable.
	if sel := uiSelected(); sel != nil {
		r := uiToScreen(sel.Solved(), canvas, area)
		if h, ok := uiHandleAt(r, pt); ok {
			uiBeginEdit("canvas.resize")
			uiDragGesture, uiDragHandle = uiGestureResize, h
			uiCanvasDragging = true
			return
		}
		if parent := uiParentOf(sel); parent != nil {
			pr := uiToScreen(parent.Solved(), canvas, area)
			if a, ok := uiAnchorAt(sel, pr, pt); ok {
				uiBeginEdit("canvas.anchor")
				uiDragGesture, uiDragAnchor = uiGestureAnchor, a
				uiCanvasDragging = true
				return
			}
		}
	}

	d := uiView.screenToDesign(pt, canvas, area)
	hit := uiDoc.HitTest(d[0], d[1], ui.HitAuthor)
	// Alt+click walks to the parent, for reaching a container under its own
	// children.
	if hit != nil && sdl.GetModState()&sdl.KMOD_ALT != 0 {
		if p := uiParentOf(hit); p != nil {
			hit = p
		}
	}
	if hit != nil && hit.FromInclude() {
		// Spliced nodes are read-only; select the include that produced them.
		for n := hit; n != nil; n = uiParentOf(n) {
			if !n.FromInclude() {
				hit = n
				break
			}
		}
	}
	uiSelect(hit)

	if uiSelected() != nil && uiSel != uiDoc.Root.ID {
		uiBeginEdit("canvas.move")
		uiDragGesture = uiGestureMove
		uiCanvasDragging = true
	}
}

// uiCanvasMouseMotion applies the gesture in progress.
func uiCanvasMouseMotion(t *sdl.MouseMotionEvent) {
	w := uiEditorWindow
	if w == nil || uiDragGesture == uiGestureNone {
		return
	}
	dxPx := float32(t.X - uiDragLastX)
	dyPx := float32(t.Y - uiDragLastY)
	uiDragLastX, uiDragLastY = t.X, t.Y

	if uiDragGesture == uiGesturePan {
		uiView.pan[0] += dxPx
		uiView.pan[1] += dyPx
		return
	}

	sel := uiSelected()
	if sel == nil {
		return
	}
	canvas, area := uiCanvasRect(w), uiDesignArea(w)
	dx := uiView.screenToDesignSize(dxPx, canvas, area)
	dy := uiView.screenToDesignSize(dyPx, canvas, area)

	switch uiDragGesture {
	case uiGestureMove:
		uiMoveNode(sel, dx, dy)
		if !uiDragNoSnap {
			uiSnapNode(sel, uiView.screenToDesignSize(uiSnapTolerance, canvas, area))
		}
	case uiGestureResize:
		uiResizeNode(sel, uiDragHandle, dx, dy)
	case uiGestureAnchor:
		uiDragAnchorTo(sel, uiDragAnchor, dx, dy)
	}
	uiEdited()
}

// uiCanvasWheel zooms about the cursor, so the point under the pointer stays
// under it — the behaviour the texture editor's canvas already has, and the one
// that makes a zoom feel like a zoom rather than a jump.
func uiCanvasWheel(t *sdl.MouseWheelEvent) {
	w := uiEditorWindow
	if w == nil || imgui.CurrentIO().WantCaptureMouse() {
		return
	}
	mx, my, _ := sdl.GetMouseState()
	canvas, area := uiCanvasRect(w), uiDesignArea(w)
	before := uiView.screenToDesign(mgl32.Vec2{float32(mx), float32(my)}, canvas, area)

	factor := float32(1.1)
	if t.Y < 0 {
		factor = 1 / 1.1
	}
	uiView.zoom = clampF(uiView.zoom*factor, 0.05, 20)

	after := uiView.screenToDesign(mgl32.Vec2{float32(mx), float32(my)}, canvas, area)
	s := uiView.scale(canvas, area)
	uiView.pan[0] += (after[0] - before[0]) * s
	uiView.pan[1] += (after[1] - before[1]) * s
}

// ---------- the maths, separated so it can be tested ----------

// uiMoveNode slides a node by a delta in document units.
//
// One formula, whether the axis is pinned or stretched. The solve reads the
// left offset outward from the anchor rect's left edge and the right offset
// *inward* from its right, so moving the rect right means adding to one and
// subtracting from the other — and the anchor rect itself does not move either
// way. A stretched banner therefore stays stretched as it slides, with no
// special case for it.
func uiMoveNode(n *ui.Node, dx, dy float32) {
	n.Rect.Offset[ui.OffLeft] += dx
	n.Rect.Offset[ui.OffRight] -= dx
	n.Rect.Offset[ui.OffTop] += dy
	n.Rect.Offset[ui.OffBottom] -= dy
}

// uiResizeNode moves the edges one handle owns.
//
// Left and top offsets grow inward from their edge; right and bottom offsets
// grow inward from theirs, which is why the sign differs. Getting this backwards
// makes a rectangle that grows when dragged smaller, so each is spelled out
// rather than shared.
func uiResizeNode(n *ui.Node, h uiHandle, dx, dy float32) {
	left := func() { n.Rect.Offset[ui.OffLeft] += dx }
	right := func() { n.Rect.Offset[ui.OffRight] -= dx }
	top := func() { n.Rect.Offset[ui.OffTop] += dy }
	bottom := func() { n.Rect.Offset[ui.OffBottom] -= dy }

	switch h {
	case uiHandleTopLeft:
		left()
		top()
	case uiHandleTop:
		top()
	case uiHandleTopRight:
		right()
		top()
	case uiHandleLeft:
		left()
	case uiHandleRight:
		right()
	case uiHandleBottomLeft:
		left()
		bottom()
	case uiHandleBottom:
		bottom()
	case uiHandleBottomRight:
		right()
		bottom()
	}
}

// uiDragAnchorTo moves one anchor marker, keeping the node exactly where it is.
func uiDragAnchorTo(n *ui.Node, a uiAnchorHandle, dx, dy float32) {
	parent := uiParentOf(n)
	if parent == nil {
		return
	}
	pr := parent.Solved()
	if pr.W <= 0 || pr.H <= 0 {
		return
	}
	fx, fy := dx/pr.W, dy/pr.H

	min, max := n.Rect.AnchorMin, n.Rect.AnchorMax
	switch a {
	case uiAnchorMinMin:
		min[0] += fx
		min[1] += fy
	case uiAnchorMaxMin:
		max[0] += fx
		min[1] += fy
	case uiAnchorMinMax:
		min[0] += fx
		max[1] += fy
	case uiAnchorMaxMax:
		max[0] += fx
		max[1] += fy
	}
	ui.SetAnchorsPreservingRect(n, pr, min, max)
}

// uiSnapNode pulls a node's edges and centre onto its parent's and its
// siblings', when they come within tolerance.
//
// Snapping to siblings as well as the parent is what makes a column of buttons
// line up without anyone typing a number. Held off with Alt, the convention
// everywhere else in the editor.
func uiSnapNode(n *ui.Node, tolerance float32) {
	parent := uiParentOf(n)
	if parent == nil || tolerance <= 0 {
		return
	}
	r := n.Solved()
	pr := parent.Solved()

	xs := []float32{pr.X, pr.X + pr.W/2, pr.X + pr.W}
	ys := []float32{pr.Y, pr.Y + pr.H/2, pr.Y + pr.H}
	for i := range parent.Children {
		s := &parent.Children[i]
		if s == n {
			continue
		}
		sr := s.Solved()
		xs = append(xs, sr.X, sr.X+sr.W/2, sr.X+sr.W)
		ys = append(ys, sr.Y, sr.Y+sr.H/2, sr.Y+sr.H)
	}

	if dx, ok := uiNearestSnap([]float32{r.X, r.X + r.W/2, r.X + r.W}, xs, tolerance); ok {
		uiMoveNode(n, dx, 0)
	}
	if dy, ok := uiNearestSnap([]float32{r.Y, r.Y + r.H/2, r.Y + r.H}, ys, tolerance); ok {
		uiMoveNode(n, 0, dy)
	}
}

// uiNearestSnap is the smallest movement that puts one of mine onto one of
// theirs, if any is close enough.
func uiNearestSnap(mine, theirs []float32, tolerance float32) (float32, bool) {
	best, found := float32(0), false
	for _, m := range mine {
		for _, t := range theirs {
			d := t - m
			if absF32(d) > tolerance {
				continue
			}
			if !found || absF32(d) < absF32(best) {
				best, found = d, true
			}
		}
	}
	return best, found
}

// uiHandleAt is which resize grip a point is on, if any.
func uiHandleAt(r ui.Rect, p mgl32.Vec2) (uiHandle, bool) {
	for i, h := range uiHandleRects(r) {
		if h.Contains(p[0], p[1]) {
			return uiHandle(i), true
		}
	}
	return 0, false
}

// uiAnchorAt is which anchor marker a point is on, if any.
func uiAnchorAt(n *ui.Node, parent ui.Rect, p mgl32.Vec2) (uiAnchorHandle, bool) {
	for i, a := range uiAnchorRects(n, parent) {
		if a.Contains(p[0], p[1]) {
			return uiAnchorHandle(i), true
		}
	}
	return 0, false
}

func clampF(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
