package main

import "testing"

// A right-click that lands on nothing clears the vertex selection, so
// anyEdgeNear must only report true for clicks that actually hit a drawn line.
func TestAnyEdgeNearHitsSectorEdges(t *testing.T) {
	ch := splitTestChunk()

	cases := []struct {
		name   string
		wx, wz float32
		want   bool
	}{
		{"on outer edge", 5, 0.2, true},
		{"on shared edge", 10, 5, true},
		{"inside sector A", 5, 5, false},
		{"outside every sector", -5, -5, false},
		{"just past threshold", 5, 2, false},
	}
	for _, c := range cases {
		if got := anyEdgeNear(ch, c.wx, c.wz, 1); got != c.want {
			t.Errorf("%s: anyEdgeNear(%v,%v) = %v, want %v", c.name, c.wx, c.wz, got, c.want)
		}
	}
}

func TestAnyEdgeNearHitsPendingEdges(t *testing.T) {
	ch := splitTestChunk()
	prev := Editor.PendingEdges
	defer func() { Editor.PendingEdges = prev }()

	// A pending edge running from (0,0) to (20,10), plus a stale edge whose
	// point indices no longer exist — that one must be ignored, not panic.
	Editor.PendingEdges = [][2]int{{0, 5}, {0, 99}}

	if !anyEdgeNear(ch, 14, 7, 1) {
		t.Error("point on the pending edge should count as a line hit")
	}
	if anyEdgeNear(ch, 2, 8, 1) {
		t.Error("point away from the pending edge should not count as a line hit")
	}
}
