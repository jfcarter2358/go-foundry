package ui

// ui_layout.go — turning anchors and offsets into rectangles.
//
// The whole algorithm is one depth-first walk with parents solved before their
// children, because an anchor is measured against a parent that is already
// solved. There is no constraint solver and no second pass: a node's rect
// depends only on its parent's, which is what keeps the cost linear and the
// result predictable enough to drag by hand in the editor.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// Scale is the factor from canvas units to device pixels.
//
// The geometric blend is what keeps a square element square. Interpolating the
// two scales linearly would make a circle on a 21:9 monitor into an ellipse,
// because the width-driven and height-driven scales differ by a ratio, not by
// a difference — so the average that matters is the one taken in log space.
func (c Canvas) Scale(fbW, fbH float32) float32 {
	if c.Mode == ScaleConstantPixel {
		return 1
	}
	// A reference of zero is a document that was never given one. Falling back
	// beats dividing by it.
	refW, refH := c.RefW, c.RefH
	if refW <= 0 || refH <= 0 {
		refW, refH = 1920, 1080
	}
	if fbW <= 0 || fbH <= 0 {
		return 1
	}
	m := float64(clamp01(c.Match))
	lw := math.Log2(float64(fbW / refW))
	lh := math.Log2(float64(fbH / refH))
	return float32(math.Exp2(lw*(1-m) + lh*m))
}

// RootRect is the screen's design-space rectangle in canvas units: the
// framebuffer divided by the scale, so that a document authored at the
// reference resolution sees exactly its reference size.
func (c Canvas) RootRect(fbW, fbH float32) Rect {
	s := c.Scale(fbW, fbH)
	if s <= 0 {
		s = 1
	}
	return Rect{W: fbW / s, H: fbH / s}
}

// WorldProject is where a world-anchored node (Node.SetWorldAnchor) lands on
// screen: the point's position as fractions of the view, 0,0 top-left to 1,1
// bottom-right, and false when it is behind the camera. Installed by the host
// that owns the camera — this package draws nothing in 3D and must not import
// what does, the same reason VideoDriver is a hook. Nil leaves anchored nodes
// where they were authored.
var WorldProject func(p mgl32.Vec3) (u, v float32, ok bool)

// Solve writes every node's rect and clip rect for a framebuffer of this size.
func (s *Screen) Solve(fbW, fbH float32) {
	root := s.Canvas.RootRect(fbW, fbH)
	solveNode(&s.Root, root, root, root)
}

// SolveInto solves against an explicit design-space rect rather than a
// framebuffer size. The UI editor uses it to lay a screen out at the design
// resolution picked in its toolbar, so the numbers on the panel are the numbers
// the game will use rather than whatever size the editor window happens to be.
func (s *Screen) SolveInto(area Rect) { solveNode(&s.Root, area, area, area) }

func solveNode(n *Node, root, parent, clip Rect) {
	x := parent.X
	y := parent.Y
	ax0 := x + n.Rect.AnchorMin[0]*parent.W
	ax1 := x + n.Rect.AnchorMax[0]*parent.W
	ay0 := y + n.Rect.AnchorMin[1]*parent.H
	ay1 := y + n.Rect.AnchorMax[1]*parent.H

	x0 := ax0 + n.Rect.Offset[OffLeft]
	y0 := ay0 + n.Rect.Offset[OffTop]
	x1 := ax1 - n.Rect.Offset[OffRight]
	y1 := ay1 - n.Rect.Offset[OffBottom]

	r := Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
	// An edge dragged past its opposite collapses rather than inverting. A
	// negative width would flip the node inside out and make every hit test on
	// it fail in a way that looks like the node has vanished.
	if r.W < 0 {
		r.W = 0
	}
	if r.H < 0 {
		r.H = 0
	}

	if sc := n.Rect.Scale; sc != (mgl32.Vec2{1, 1}) && sc != (mgl32.Vec2{}) {
		px := r.X + n.Rect.Pivot[0]*r.W
		py := r.Y + n.Rect.Pivot[1]*r.H
		r.W *= sc[0]
		r.H *= sc[1]
		r.X = px - n.Rect.Pivot[0]*r.W
		r.Y = py - n.Rect.Pivot[1]*r.H
	}

	// A world anchor moves the rect, never resizes it: the pivot goes to where
	// the point is on screen. Measured against the root rather than the
	// parent, because a point in the world is somewhere on the whole screen.
	behind := false
	if n.anchored && WorldProject != nil {
		if u, v, ok := WorldProject(n.worldAnchor); ok {
			r.X = root.X + u*root.W - n.Rect.Pivot[0]*r.W
			r.Y = root.Y + v*root.H - n.Rect.Pivot[1]*r.H
		} else {
			behind = true
		}
	}

	n.solved = r
	// Clip rects intersect rather than replace, so a clipping panel inside a
	// clipping panel cannot draw outside its grandparent.
	n.solvedClip = clip
	if n.Clip {
		n.solvedClip = clip.Intersect(r)
	}
	n.culled = n.solvedClip.Empty() || behind

	// Children anchor to the parent's solved rect, not its clip rect. A list
	// inside a clipping panel has to keep its positions when the panel is
	// partly off-screen; anchoring to the clip would make the items shuffle as
	// the panel slid.
	for i := range n.Children {
		solveNode(&n.Children[i], root, r, n.solvedClip)
	}
}

// SetAnchorsPreservingRect moves a node's anchors without moving the node.
//
// This is the single function that makes anchors usable rather than
// terrifying. Dragging an anchor handle should re-express *where a node is* in
// terms of a different parent-relative origin, not teleport it — so the offsets
// are recomputed to keep the solved rect exactly where it was.
//
// parent is the node's parent's solved rect. Anchors are clamped to 0..1 and
// min is kept at or below max, because an inverted anchor pair produces a
// negative anchor rect and every offset after it reads backwards.
func SetAnchorsPreservingRect(n *Node, parent Rect, min, max mgl32.Vec2) {
	keep := n.solved

	min = mgl32.Vec2{clamp01(min[0]), clamp01(min[1])}
	max = mgl32.Vec2{clamp01(max[0]), clamp01(max[1])}
	if max[0] < min[0] {
		max[0] = min[0]
	}
	if max[1] < min[1] {
		max[1] = min[1]
	}

	n.Rect.AnchorMin, n.Rect.AnchorMax = min, max

	// The solved rect already has any scale folded in, so undo it before
	// writing offsets — otherwise the scale would be applied twice on the next
	// solve and the node would creep every time an anchor was nudged.
	unscaled := keep
	if sc := n.Rect.Scale; sc != (mgl32.Vec2{1, 1}) && sc != (mgl32.Vec2{}) {
		px := keep.X + n.Rect.Pivot[0]*keep.W
		py := keep.Y + n.Rect.Pivot[1]*keep.H
		unscaled.W = keep.W / sc[0]
		unscaled.H = keep.H / sc[1]
		unscaled.X = px - n.Rect.Pivot[0]*unscaled.W
		unscaled.Y = py - n.Rect.Pivot[1]*unscaled.H
	}

	ax0 := parent.X + min[0]*parent.W
	ax1 := parent.X + max[0]*parent.W
	ay0 := parent.Y + min[1]*parent.H
	ay1 := parent.Y + max[1]*parent.H

	n.Rect.Offset[OffLeft] = unscaled.X - ax0
	n.Rect.Offset[OffTop] = unscaled.Y - ay0
	n.Rect.Offset[OffRight] = ax1 - (unscaled.X + unscaled.W)
	n.Rect.Offset[OffBottom] = ay1 - (unscaled.Y + unscaled.H)
}

// AnchorPreset is one cell of the editor's 3x3-plus-stretch anchor grid.
type AnchorPreset struct {
	Name     string
	Min, Max mgl32.Vec2
}

// AnchorPresets is the set the editor's preset grid offers, in reading order:
// the nine corner/edge/centre pins, then the stretch row, column and both.
//
// Presets exist because typing four anchor numbers to mean "bottom-left" is the
// step where anchors stop being worth it. Every one of these is a pair of
// vectors an author would otherwise have to know by heart.
var AnchorPresets = []AnchorPreset{
	{"top-left", mgl32.Vec2{0, 0}, mgl32.Vec2{0, 0}},
	{"top", mgl32.Vec2{0.5, 0}, mgl32.Vec2{0.5, 0}},
	{"top-right", mgl32.Vec2{1, 0}, mgl32.Vec2{1, 0}},
	{"left", mgl32.Vec2{0, 0.5}, mgl32.Vec2{0, 0.5}},
	{"center", mgl32.Vec2{0.5, 0.5}, mgl32.Vec2{0.5, 0.5}},
	{"right", mgl32.Vec2{1, 0.5}, mgl32.Vec2{1, 0.5}},
	{"bottom-left", mgl32.Vec2{0, 1}, mgl32.Vec2{0, 1}},
	{"bottom", mgl32.Vec2{0.5, 1}, mgl32.Vec2{0.5, 1}},
	{"bottom-right", mgl32.Vec2{1, 1}, mgl32.Vec2{1, 1}},
	{"stretch-x", mgl32.Vec2{0, 0.5}, mgl32.Vec2{1, 0.5}},
	{"stretch-y", mgl32.Vec2{0.5, 0}, mgl32.Vec2{0.5, 1}},
	{"stretch", mgl32.Vec2{0, 0}, mgl32.Vec2{1, 1}},
}
