package ui

// ui_hit.go — which node is under a point.
//
// The walk is the exact reverse of the draw: later siblings before earlier
// ones, children before their parent. That is not a detail — it is the whole
// correctness condition. Whatever is painted last is on top, so whatever is
// visited first must be what a click lands on, or the thing you can see is not
// the thing you can press.

// HitMode selects which nodes are eligible.
type HitMode int

const (
	// HitInput is what the running game asks: skip anything invisible, fully
	// clipped, or neither interactive nor explicitly blocking.
	HitInput HitMode = iota
	// HitAuthor is what the editor's canvas asks: hit anything with area, so a
	// hidden panel or an inert label can still be selected and dragged.
	HitAuthor
)

// HitTest returns the topmost node at a point in canvas units, or nil.
//
// A disabled interactive node is still returned. It has to be: a greyed-out
// button in a menu must swallow the click rather than let it through to
// whatever is behind, and deciding what to do about Enabled is the caller's
// business rather than a question of geometry.
func (s *Screen) HitTest(x, y float32, mode HitMode) *Node {
	return hitNode(&s.Root, x, y, mode)
}

func hitNode(n *Node, x, y float32, mode HitMode) *Node {
	if mode == HitInput && (!n.Visible || n.culled) {
		return nil
	}

	for i := len(n.Children) - 1; i >= 0; i-- {
		if got := hitNode(&n.Children[i], x, y, mode); got != nil {
			return got
		}
	}

	// A node outside the clip in force for it was never drawn, so it cannot be
	// what was clicked, however much its own rect contains the point.
	if !n.solvedClip.Contains(x, y) || !n.solved.Contains(x, y) {
		return nil
	}
	if mode == HitInput && !n.Kind.Interactive() && !n.Blocks {
		return nil
	}
	return n
}

// Focusable reports whether keyboard or gamepad focus can rest on a node.
func (n *Node) Focusable() bool {
	return n.Kind.Interactive() && n.Visible && n.Enabled && !n.culled && !n.solved.Empty()
}

// FirstFocus is the node focus starts on: the one named by InitialFocus if it
// is eligible, otherwise the first focusable node in tree order — which is
// reading order, because tree order is draw order.
func (s *Screen) FirstFocus() *Node {
	if s.InitialFocus != "" {
		if n := s.Find(s.InitialFocus); n != nil && n.Focusable() {
			return n
		}
	}
	var found *Node
	s.Root.Walk(func(_, n *Node) bool {
		if found != nil {
			return false
		}
		if n.Focusable() {
			found = n
		}
		return true
	})
	return found
}

// Focusables returns every node focus can reach, in tree order.
func (s *Screen) Focusables() []*Node {
	var out []*Node
	s.Root.Walk(func(_, n *Node) bool {
		if n.Focusable() {
			out = append(out, n)
		}
		return true
	})
	return out
}
