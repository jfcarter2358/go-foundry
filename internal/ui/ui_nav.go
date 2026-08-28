package ui

// ui_nav.go — moving focus with a direction rather than a pointer.
//
// Written against four directions and an activate rather than against a
// keyboard, so a gamepad d-pad is the same code path: internal/ui never learns
// which device asked. That is the whole reason this is a separate file with a
// pure signature — adding controller support later is a dozen lines in the
// event handler and no change at all in here.

// Dir is one of the four directions focus can move.
type Dir int

const (
	DirUp Dir = iota
	DirDown
	DirLeft
	DirRight
)

// DirNames is each direction's name, for the editor's navigation dropdowns.
var DirNames = []string{"up", "down", "left", "right"}

// crossWeight is how much harder it is to move sideways than forwards.
//
// A pure nearest-centre search picks whatever is closest in a straight line,
// which on a form means pressing Down from a wide field jumps diagonally to
// whichever neighbour happens to be nearer. Weighting the cross axis makes the
// search prefer things that are actually in the direction pressed, which is
// what a player means by "down".
const crossWeight = 2

// Neighbour returns the node focus moves to from a node in a direction, or nil
// when there is nothing that way.
//
// An explicit Nav entry wins outright: an author who has wired a menu up by
// hand is describing intent the geometry cannot see. Otherwise it is a
// geometric search among the focusable nodes, which is deterministic — the same
// layout always produces the same answer, so a test can assert a whole grid.
func Neighbour(s *Screen, from *Node, d Dir) *Node {
	if from == nil {
		return s.FirstFocus()
	}

	if id := from.Nav.inDirection(d); id != "" {
		if n := s.Find(id); n != nil && n.Focusable() {
			return n
		}
		// A wired link pointing at something hidden or deleted falls through to
		// the search rather than trapping focus where it is.
	}

	fx, fy := from.solved.Center()
	var best *Node
	bestScore := float32(0)

	for _, n := range s.Focusables() {
		if n == from {
			continue
		}
		cx, cy := n.solved.Center()
		dx, dy := cx-fx, cy-fy

		var forward, cross float32
		switch d {
		case DirUp:
			forward, cross = -dy, absF(dx)
		case DirDown:
			forward, cross = dy, absF(dx)
		case DirLeft:
			forward, cross = -dx, absF(dy)
		default:
			forward, cross = dx, absF(dy)
		}
		if forward <= 0 {
			continue
		}

		score := forward + crossWeight*cross
		if best == nil || score < bestScore {
			best, bestScore = n, score
		}
	}
	return best
}

func (nv Nav) inDirection(d Dir) string {
	switch d {
	case DirUp:
		return nv.Up
	case DirDown:
		return nv.Down
	case DirLeft:
		return nv.Left
	}
	return nv.Right
}

func absF(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
