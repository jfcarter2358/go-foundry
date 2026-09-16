package ui

import "testing"

// grid lays a 3x3 of buttons out named r0c0 .. r2c2, 100x100 on a 150 pitch.
func grid() *Screen {
	var kids []Node
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			kids = append(kids, at(
				"r"+itoa(r+1)+"c"+itoa(c+1), KindButton,
				float32(c)*150, float32(r)*150, 100, 100,
			))
		}
	}
	return solved(kids...)
}

func TestNeighbourFindsTheGridCellInEachDirection(t *testing.T) {
	s := grid()
	cases := []struct {
		from string
		dir  Dir
		want string
	}{
		{"r2c2", DirUp, "r1c2"},
		{"r2c2", DirDown, "r3c2"},
		{"r2c2", DirLeft, "r2c1"},
		{"r2c2", DirRight, "r2c3"},
		{"r1c1", DirRight, "r1c2"},
		{"r1c1", DirDown, "r2c1"},
		{"r3c3", DirUp, "r2c3"},
		{"r3c3", DirLeft, "r3c2"},
		// Nothing that way at all.
		{"r1c1", DirUp, ""},
		{"r1c1", DirLeft, ""},
		{"r3c3", DirDown, ""},
		{"r3c3", DirRight, ""},
	}
	for _, tc := range cases {
		got := Neighbour(s, s.Find(tc.from), tc.dir)
		if idOrEmpty(got) != tc.want {
			t.Errorf("%s %s -> %q, want %q", tc.from, DirNames[tc.dir], idOrEmpty(got), tc.want)
		}
	}
}

func TestNeighbourPrefersStraightAheadOverDiagonallyNearer(t *testing.T) {
	// The cross-axis weight exists for exactly this: a diagonal neighbour that
	// is closer in a straight line than the one directly below must lose,
	// because "down" is what the player pressed.
	s := solved(
		at("from", KindButton, 400, 400, 100, 40),
		at("straight", KindButton, 400, 600, 100, 40),
		at("diagonal", KindButton, 700, 480, 100, 40),
	)
	if got := Neighbour(s, s.Find("from"), DirDown); idOrEmpty(got) != "straight" {
		t.Errorf("down went to %q, want straight", idOrEmpty(got))
	}
}

func TestExplicitNavBeatsGeometry(t *testing.T) {
	s := grid()
	from := s.Find("r2c2")
	from.Nav.Down = "r1c1" // deliberately backwards
	if got := Neighbour(s, from, DirDown); idOrEmpty(got) != "r1c1" {
		t.Errorf("wired nav gave %q, want the wired target", idOrEmpty(got))
	}
}

func TestNavFallsBackWhenTheWiredTargetCannotTakeFocus(t *testing.T) {
	// A link pointing at something hidden or deleted must not trap focus where
	// it is; the search takes over.
	s := grid()
	from := s.Find("r2c2")
	from.Nav.Down = "r3c1"
	s.Find("r3c1").Visible = false
	s.SolveInto(Rect{0, 0, 1000, 800})

	if got := Neighbour(s, from, DirDown); idOrEmpty(got) != "r3c2" {
		t.Errorf("fallback gave %q, want the geometric answer r3c2", idOrEmpty(got))
	}

	from.Nav.Down = "does-not-exist"
	if got := Neighbour(s, from, DirDown); idOrEmpty(got) != "r3c2" {
		t.Errorf("a dangling link gave %q, want the geometric answer", idOrEmpty(got))
	}
}

func TestNeighbourFromNothingIsTheFirstFocus(t *testing.T) {
	s := grid()
	if got := Neighbour(s, nil, DirDown); idOrEmpty(got) != "r1c1" {
		t.Errorf("moving from nowhere gave %q, want the first focusable", idOrEmpty(got))
	}
}

func TestNeighbourSkipsNonFocusableNodes(t *testing.T) {
	s := grid()
	s.Find("r2c2").Enabled = false
	s.SolveInto(Rect{0, 0, 1000, 800})
	// From the top-middle, down should skip the disabled middle cell.
	if got := Neighbour(s, s.Find("r1c2"), DirDown); idOrEmpty(got) != "r3c2" {
		t.Errorf("down over a disabled cell gave %q, want r3c2", idOrEmpty(got))
	}
}

func idOrEmpty(n *Node) string {
	if n == nil {
		return ""
	}
	return n.ID
}

// A widget inside a hidden panel — a settings tab that is not showing — must
// be out of reach of the arrow keys, focus() and an explicit nav link alike.
func TestHiddenPanelHidesItsChildrenFromFocus(t *testing.T) {
	tab := at("tab", KindPanel, 0, 200, 400, 200)
	tab.Children = []Node{at("inner", KindButton, 0, 0, 100, 100)}
	top := at("top", KindButton, 0, 0, 100, 100)
	top.Nav.Down = "inner"
	s := solved(top, tab)

	inner := s.Find("inner")
	if !s.CanFocus(inner) || Neighbour(s, s.Find("top"), DirDown) != inner {
		t.Fatal("a visible panel's child should be reachable")
	}
	s.Find("tab").Visible = false
	if s.CanFocus(inner) {
		t.Error("CanFocus is true inside a hidden panel")
	}
	if got := Neighbour(s, s.Find("top"), DirDown); got != nil {
		t.Errorf("down from top reached %q inside a hidden panel", got.ID)
	}
	for _, n := range s.Focusables() {
		if n.ID == "inner" {
			t.Error("Focusables lists a node inside a hidden panel")
		}
	}
}
