package ui

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// at builds a node pinned to a rect in a 1000x800 parent, so the tests below
// can talk about pixel coordinates directly.
func at(id string, kind Kind, x, y, w, h float32) Node {
	n := NewNode(id, kind)
	n.Rect.AnchorMin, n.Rect.AnchorMax = mgl32.Vec2{0, 0}, mgl32.Vec2{0, 0}
	n.Rect.Offset = [4]float32{x, y, -(x + w), -(y + h)}
	return n
}

func solved(children ...Node) *Screen {
	s := NewScreen("t")
	s.Root.Children = children
	s.SolveInto(Rect{0, 0, 1000, 800})
	return s
}

func TestHitTestPicksTheTopmost(t *testing.T) {
	// Two overlapping buttons. The later one draws on top, so it must be the
	// one that gets clicked.
	s := solved(
		at("under", KindButton, 0, 0, 100, 100),
		at("over", KindButton, 50, 50, 100, 100),
	)
	if got := s.HitTest(75, 75, HitInput); got == nil || got.ID != "over" {
		t.Errorf("overlap hit %v, want the later sibling", idOf(got))
	}
	if got := s.HitTest(25, 25, HitInput); got == nil || got.ID != "under" {
		t.Errorf("non-overlapping area hit %v, want under", idOf(got))
	}
}

func TestHitTestPrefersAChildOverItsParent(t *testing.T) {
	parent := at("parent", KindButton, 0, 0, 200, 200)
	parent.Children = []Node{at("kid", KindButton, 50, 50, 50, 50)}
	s := solved(parent)

	if got := s.HitTest(75, 75, HitInput); got == nil || got.ID != "kid" {
		t.Errorf("hit %v, want the child", idOf(got))
	}
	if got := s.HitTest(10, 10, HitInput); got == nil || got.ID != "parent" {
		t.Errorf("hit %v outside the child, want the parent", idOf(got))
	}
}

func TestHitTestSkipsInertNodesButNotBlockingOnes(t *testing.T) {
	label := at("label", KindText, 0, 0, 100, 100)
	blocker := at("blocker", KindPanel, 200, 0, 100, 100)
	blocker.Blocks = true
	s := solved(label, blocker)

	if got := s.HitTest(50, 50, HitInput); got != nil {
		t.Errorf("a plain label swallowed a click as %v", idOf(got))
	}
	if got := s.HitTest(250, 50, HitInput); got == nil || got.ID != "blocker" {
		t.Errorf("a Blocks panel let a click through: %v", idOf(got))
	}
}

func TestHitTestSkipsInvisibleInInputModeButNotInAuthorMode(t *testing.T) {
	n := at("hidden", KindButton, 0, 0, 100, 100)
	n.Visible = false
	s := solved(n)

	if got := s.HitTest(50, 50, HitInput); got != nil {
		t.Errorf("a hidden button took input as %v", idOf(got))
	}
	// The editor must still be able to select it on the canvas, or a widget
	// that has been hidden can only be reached from the hierarchy tree.
	if got := s.HitTest(50, 50, HitAuthor); got == nil || got.ID != "hidden" {
		t.Errorf("the editor could not select a hidden node: %v", idOf(got))
	}
}

func TestHitTestReturnsADisabledNodeSoItStillBlocks(t *testing.T) {
	// A greyed-out button in a menu has to swallow the click rather than let it
	// reach whatever is behind. What to *do* about Enabled is the caller's
	// business; geometry says the click landed here.
	under := at("under", KindButton, 0, 0, 200, 200)
	over := at("over", KindButton, 0, 0, 200, 200)
	over.Enabled = false
	s := solved(under, over)

	if got := s.HitTest(50, 50, HitInput); got == nil || got.ID != "over" {
		t.Errorf("hit %v, want the disabled button on top", idOf(got))
	}
}

func TestClippedChildIsNotHit(t *testing.T) {
	panel := at("panel", KindPanel, 0, 0, 100, 100)
	panel.Clip = true
	panel.Children = []Node{at("kid", KindButton, 50, 50, 200, 200)}
	s := solved(panel)

	if got := s.HitTest(75, 75, HitInput); got == nil || got.ID != "kid" {
		t.Errorf("inside the clip hit %v, want kid", idOf(got))
	}
	if got := s.HitTest(150, 150, HitInput); got != nil {
		t.Errorf("outside the clip hit %v, want nothing", idOf(got))
	}
}

func TestHitTestExcludesTheRightAndBottomEdges(t *testing.T) {
	// Two abutting rects must not both claim the seam between them.
	s := solved(at("a", KindButton, 0, 0, 100, 100), at("b", KindButton, 100, 0, 100, 100))
	if got := s.HitTest(100, 50, HitInput); got == nil || got.ID != "b" {
		t.Errorf("the seam went to %v, want b", idOf(got))
	}
}

func TestFirstFocusPrefersInitialFocusThenTreeOrder(t *testing.T) {
	s := solved(
		at("one", KindButton, 0, 0, 100, 50),
		at("two", KindButton, 0, 60, 100, 50),
	)
	if got := s.FirstFocus(); got == nil || got.ID != "one" {
		t.Errorf("default focus is %v, want the first in tree order", idOf(got))
	}

	s.InitialFocus = "two"
	if got := s.FirstFocus(); got == nil || got.ID != "two" {
		t.Errorf("InitialFocus was ignored, got %v", idOf(got))
	}

	// A named node that cannot take focus falls back rather than trapping it.
	s.Find("two").Enabled = false
	if got := s.FirstFocus(); got == nil || got.ID != "one" {
		t.Errorf("a disabled InitialFocus gave %v, want the fallback", idOf(got))
	}
}

func TestFocusableRefusesTheUnreachable(t *testing.T) {
	s := solved(at("btn", KindButton, 0, 0, 100, 50))
	n := s.Find("btn")
	if !n.Focusable() {
		t.Fatal("a plain visible enabled button is not focusable")
	}
	for _, tc := range []struct {
		name string
		set  func()
	}{
		{"hidden", func() { n.Visible = false }},
		{"disabled", func() { n.Enabled = false }},
		{"zero size", func() { n.Rect.Offset = [4]float32{0, 0, 0, 0} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s2 := solved(at("btn", KindButton, 0, 0, 100, 50))
			n = s2.Find("btn")
			tc.set()
			s2.SolveInto(Rect{0, 0, 1000, 800})
			if n.Focusable() {
				t.Errorf("a %s node is focusable", tc.name)
			}
		})
	}
}

func idOf(n *Node) string {
	if n == nil {
		return "<nil>"
	}
	return n.ID
}
