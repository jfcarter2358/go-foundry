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
	return hitNode(&s.Root, x, y, mode, nil)
}

// hitTestSkipping is HitTest as though one node, and everything in it, were
// not there: what a dragged icon is being dropped ON, when the icon itself is
// following the pointer and would otherwise always be the answer.
func (s *Screen) hitTestSkipping(x, y float32, skip *Node) *Node {
	return hitNode(&s.Root, x, y, HitInput, skip)
}

func hitNode(n *Node, x, y float32, mode HitMode, skip *Node) *Node {
	if n == skip && skip != nil {
		return nil
	}
	if mode == HitInput && (!n.Visible || n.culled) {
		return nil
	}

	for i := len(n.Children) - 1; i >= 0; i-- {
		if got := hitNode(&n.Children[i], x, y, mode, skip); got != nil {
			return got
		}
	}

	// A node outside the clip in force for it was never drawn, so it cannot be
	// what was clicked, however much its own rect contains the point.
	if !n.solvedClip.Contains(x, y) || !n.solved.Contains(x, y) {
		return nil
	}
	if mode == HitInput && !n.Kind.Interactive() && !n.Blocks && !n.Draggable {
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
		if n := s.Find(s.InitialFocus); n != nil && s.CanFocus(n) {
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
		return n.Visible
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
		// A hidden panel hides everything in it — a settings tab that is
		// not showing must not be reachable by the arrow keys.
		return n.Visible
	})
	return out
}

// CanFocus is Focusable for a node in its place in the screen: false inside a
// hidden ancestor too, which Focusable alone cannot see.
func (s *Screen) CanFocus(target *Node) bool {
	if target == nil || !target.Focusable() {
		return false
	}
	found := false
	s.Root.Walk(func(_, n *Node) bool {
		if found || !n.Visible {
			return false
		}
		if n == target {
			found = true
			return false
		}
		return true
	})
	return found
}
