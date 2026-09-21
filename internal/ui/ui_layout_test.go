package ui

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

const eps = 1e-3

func closeF(a, b float32) bool { return math.Abs(float64(a-b)) < eps }

func rectClose(a, b Rect) bool {
	return closeF(a.X, b.X) && closeF(a.Y, b.Y) && closeF(a.W, b.W) && closeF(a.H, b.H)
}

// child builds a one-child screen whose root is exactly parent-sized, so a test
// can talk about the child's rect in absolute numbers.
func child(rect RectXf) (*Screen, *Node) {
	s := NewScreen("t")
	c := NewNode("c", KindPanel)
	c.Rect = rect
	s.Root.Children = []Node{c}
	return s, &s.Root.Children[0]
}

func TestSolve(t *testing.T) {
	const w, h = 1000, 800

	cases := []struct {
		name string
		rect RectXf
		want Rect
	}{
		{
			"stretch to fill",
			RectXf{AnchorMax: mgl32.Vec2{1, 1}, Scale: mgl32.Vec2{1, 1}},
			Rect{0, 0, w, h},
		},
		{
			"stretch with an inset on every side",
			RectXf{AnchorMax: mgl32.Vec2{1, 1}, Offset: [4]float32{10, 20, 30, 40}, Scale: mgl32.Vec2{1, 1}},
			Rect{10, 20, w - 40, h - 60},
		},
		{
			"pinned top-left, 200x50",
			RectXf{Offset: [4]float32{16, 16, -216, -66}, Scale: mgl32.Vec2{1, 1}},
			Rect{16, 16, 200, 50},
		},
		{
			"pinned bottom-right, 200x50",
			RectXf{
				AnchorMin: mgl32.Vec2{1, 1}, AnchorMax: mgl32.Vec2{1, 1},
				Offset: [4]float32{-216, -66, 16, 16}, Scale: mgl32.Vec2{1, 1},
			},
			Rect{w - 216, h - 66, 200, 50},
		},
		{
			"centred, 100x100",
			RectXf{
				AnchorMin: mgl32.Vec2{0.5, 0.5}, AnchorMax: mgl32.Vec2{0.5, 0.5},
				Offset: [4]float32{-50, -50, -50, -50}, Scale: mgl32.Vec2{1, 1},
			},
			Rect{w/2 - 50, h/2 - 50, 100, 100},
		},
		{
			"stretched horizontally, pinned vertically",
			RectXf{
				AnchorMin: mgl32.Vec2{0, 1}, AnchorMax: mgl32.Vec2{1, 1},
				Offset: [4]float32{24, -80, 24, 24}, Scale: mgl32.Vec2{1, 1},
			},
			Rect{24, h - 80, w - 48, 56},
		},
		{
			"edges dragged through each other collapse rather than invert",
			RectXf{AnchorMax: mgl32.Vec2{1, 1}, Offset: [4]float32{600, 500, 600, 500}, Scale: mgl32.Vec2{1, 1}},
			Rect{600, 500, 0, 0},
		},
		{
			"scaled 2x about a centred pivot",
			RectXf{
				AnchorMin: mgl32.Vec2{0.5, 0.5}, AnchorMax: mgl32.Vec2{0.5, 0.5},
				Offset: [4]float32{-50, -50, -50, -50},
				Pivot:  mgl32.Vec2{0.5, 0.5}, Scale: mgl32.Vec2{2, 2},
			},
			Rect{w/2 - 100, h/2 - 100, 200, 200},
		},
		{
			"scaled 2x about a top-left pivot grows down and right only",
			RectXf{
				AnchorMin: mgl32.Vec2{0.5, 0.5}, AnchorMax: mgl32.Vec2{0.5, 0.5},
				Offset: [4]float32{-50, -50, -50, -50},
				Pivot:  mgl32.Vec2{0, 0}, Scale: mgl32.Vec2{2, 2},
			},
			Rect{w/2 - 50, h/2 - 50, 200, 200},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, c := child(tc.rect)
			s.SolveInto(Rect{0, 0, w, h})
			if !rectClose(c.Solved(), tc.want) {
				t.Errorf("got %+v, want %+v", c.Solved(), tc.want)
			}
		})
	}
}

func TestSolveNestsThreeDeep(t *testing.T) {
	s := NewScreen("t")
	// Each level insets 10 on every side and stretches.
	inset := func(id string) Node {
		n := NewNode(id, KindPanel)
		n.Rect.Offset = [4]float32{10, 10, 10, 10}
		return n
	}
	a, b, c := inset("a"), inset("b"), inset("c")
	b.Children = []Node{c}
	a.Children = []Node{b}
	s.Root.Children = []Node{a}
	s.SolveInto(Rect{0, 0, 400, 300})

	want := Rect{30, 30, 340, 240}
	if got := s.Find("c").Solved(); !rectClose(got, want) {
		t.Errorf("three-deep inset gave %+v, want %+v", got, want)
	}
}

// TestSetAnchorsPreservesTheSolvedRect is the single most valuable test here.
// Dragging an anchor handle re-expresses where a node is; if it also moves the
// node, anchors are unusable and every author avoids them.
func TestSetAnchorsPreservesTheSolvedRect(t *testing.T) {
	parent := Rect{0, 0, 1000, 800}

	for _, from := range AnchorPresets {
		for _, to := range AnchorPresets {
			t.Run(from.Name+"->"+to.Name, func(t *testing.T) {
				rect := DefaultRectXf()
				rect.AnchorMin, rect.AnchorMax = from.Min, from.Max
				rect.Offset = [4]float32{31, 17, -231, -97} // 200x80 somewhere
				s, c := child(rect)
				s.SolveInto(parent)
				was := c.Solved()

				SetAnchorsPreservingRect(c, parent, to.Min, to.Max)
				s.SolveInto(parent)

				if !rectClose(c.Solved(), was) {
					t.Errorf("node moved: %+v -> %+v", was, c.Solved())
				}
				if c.Rect.AnchorMin != to.Min || c.Rect.AnchorMax != to.Max {
					t.Errorf("anchors are %v/%v, want %v/%v",
						c.Rect.AnchorMin, c.Rect.AnchorMax, to.Min, to.Max)
				}
			})
		}
	}
}

func TestSetAnchorsPreservesTheRectUnderScale(t *testing.T) {
	// The solved rect has scale folded in. Writing offsets from it without
	// undoing that would apply the scale twice, and the node would creep every
	// time an anchor was nudged.
	parent := Rect{0, 0, 1000, 800}
	rect := DefaultRectXf()
	rect.AnchorMin, rect.AnchorMax = mgl32.Vec2{0, 0}, mgl32.Vec2{0, 0}
	rect.Offset = [4]float32{100, 100, -300, -200}
	rect.Scale = mgl32.Vec2{1.5, 2}
	s, c := child(rect)
	s.SolveInto(parent)
	was := c.Solved()

	for i := 0; i < 5; i++ {
		SetAnchorsPreservingRect(c, parent, mgl32.Vec2{0.5, 0.5}, mgl32.Vec2{0.5, 0.5})
		s.SolveInto(parent)
		SetAnchorsPreservingRect(c, parent, mgl32.Vec2{0, 0}, mgl32.Vec2{1, 1})
		s.SolveInto(parent)
	}
	if !rectClose(c.Solved(), was) {
		t.Errorf("node crept over ten anchor changes: %+v -> %+v", was, c.Solved())
	}
}

func TestSetAnchorsRefusesAnInvertedPair(t *testing.T) {
	s, c := child(DefaultRectXf())
	s.SolveInto(Rect{0, 0, 100, 100})
	SetAnchorsPreservingRect(c, Rect{0, 0, 100, 100}, mgl32.Vec2{0.8, 0.9}, mgl32.Vec2{0.2, 0.1})
	if c.Rect.AnchorMax[0] < c.Rect.AnchorMin[0] || c.Rect.AnchorMax[1] < c.Rect.AnchorMin[1] {
		t.Errorf("anchors inverted: %v > %v", c.Rect.AnchorMin, c.Rect.AnchorMax)
	}
}

func TestChildrenAnchorToTheUnclippedParent(t *testing.T) {
	// A list inside a clipping panel must keep its positions when the panel is
	// partly off-screen. Anchoring to the clip would make the items shuffle as
	// the panel slid.
	s := NewScreen("t")
	panel := NewNode("panel", KindPanel)
	panel.Clip = true
	panel.Rect.AnchorMin, panel.Rect.AnchorMax = mgl32.Vec2{0, 0}, mgl32.Vec2{0, 0}
	panel.Rect.Offset = [4]float32{-100, 0, -100, -200} // half off the left edge

	item := NewNode("item", KindPanel)
	item.Rect.AnchorMin, item.Rect.AnchorMax = mgl32.Vec2{0, 0}, mgl32.Vec2{0, 0}
	item.Rect.Offset = [4]float32{10, 10, -110, -60}
	panel.Children = []Node{item}
	s.Root.Children = []Node{panel}
	s.SolveInto(Rect{0, 0, 500, 500})

	// The item sits 10 in from the panel's own left edge at -100, i.e. at -90 —
	// not 10 in from the visible edge at 0.
	if got := s.Find("item").Solved().X; !closeF(got, -90) {
		t.Errorf("item x is %v, want -90 (relative to the unclipped parent)", got)
	}
}

func TestClipIntersectsRatherThanReplaces(t *testing.T) {
	s := NewScreen("t")
	outer := NewNode("outer", KindPanel)
	outer.Clip = true
	outer.Rect.AnchorMin, outer.Rect.AnchorMax = mgl32.Vec2{0, 0}, mgl32.Vec2{0, 0}
	outer.Rect.Offset = [4]float32{0, 0, -100, -100} // 100x100 at the origin

	inner := NewNode("inner", KindPanel)
	inner.Clip = true
	inner.Rect.AnchorMin, inner.Rect.AnchorMax = mgl32.Vec2{0, 0}, mgl32.Vec2{0, 0}
	inner.Rect.Offset = [4]float32{50, 50, -250, -250} // 200x200 from (50,50)

	outer.Children = []Node{inner}
	s.Root.Children = []Node{outer}
	s.SolveInto(Rect{0, 0, 500, 500})

	// The inner panel wants to reach 250; the outer stops it at 100.
	want := Rect{50, 50, 50, 50}
	if got := s.Find("inner").SolvedClip(); !rectClose(got, want) {
		t.Errorf("nested clip is %+v, want %+v — a child clip replaced its parent's", got, want)
	}
}

func TestEmptyClipCullsTheSubtree(t *testing.T) {
	s := NewScreen("t")
	panel := NewNode("panel", KindPanel)
	panel.Clip = true
	panel.Rect.AnchorMin, panel.Rect.AnchorMax = mgl32.Vec2{0, 0}, mgl32.Vec2{0, 0}
	panel.Rect.Offset = [4]float32{0, 0, 0, 0} // collapses to zero size

	item := NewNode("item", KindPanel)
	panel.Children = []Node{item}
	s.Root.Children = []Node{panel}
	s.SolveInto(Rect{0, 0, 500, 500})

	if !s.Find("panel").Culled() {
		t.Error("a zero-size clipping panel was not culled")
	}
	if !s.Find("item").Culled() {
		t.Error("a child of a fully-clipped panel was not culled")
	}
}

func TestScaleWithScreenMatchesTheReferenceAtReferenceSize(t *testing.T) {
	c := DefaultCanvas()
	if got := c.Scale(c.RefW, c.RefH); !closeF(got, 1) {
		t.Errorf("scale at the reference size is %v, want 1", got)
	}
	r := c.RootRect(c.RefW, c.RefH)
	if !rectClose(r, Rect{0, 0, c.RefW, c.RefH}) {
		t.Errorf("root rect at reference size is %+v", r)
	}
	// Double the framebuffer, and a document authored at 1080p still sees 1080p
	// worth of canvas units — which is the whole point.
	if r2 := c.RootRect(c.RefW*2, c.RefH*2); !rectClose(r2, r) {
		t.Errorf("root rect at 2x is %+v, want the same %+v", r2, r)
	}
}

func TestMatchBlendsGeometrically(t *testing.T) {
	c := DefaultCanvas()
	// A framebuffer twice as wide and the same height: match 0 follows width,
	// match 1 follows height, match 0.5 is the geometric mean of the two — not
	// the arithmetic one, which is what keeps a circle a circle.
	w, h := c.RefW*2, c.RefH

	byWidth := Canvas{RefW: c.RefW, RefH: c.RefH, Match: 0}
	byHeight := Canvas{RefW: c.RefW, RefH: c.RefH, Match: 1}
	both := Canvas{RefW: c.RefW, RefH: c.RefH, Match: 0.5}

	if got := byWidth.Scale(w, h); !closeF(got, 2) {
		t.Errorf("match 0 gave %v, want 2", got)
	}
	if got := byHeight.Scale(w, h); !closeF(got, 1) {
		t.Errorf("match 1 gave %v, want 1", got)
	}
	if got := both.Scale(w, h); !closeF(got, float32(math.Sqrt2)) {
		t.Errorf("match 0.5 gave %v, want sqrt(2) = %v", got, math.Sqrt2)
	}
}

func TestConstantPixelIgnoresTheReference(t *testing.T) {
	c := Canvas{Mode: ScaleConstantPixel, RefW: 1920, RefH: 1080, Match: 0.5}
	if got := c.Scale(3840, 2160); got != 1 {
		t.Errorf("constant-pixel scale is %v, want 1", got)
	}
	if got := c.RootRect(3840, 2160); !rectClose(got, Rect{0, 0, 3840, 2160}) {
		t.Errorf("constant-pixel root rect is %+v", got)
	}
}

func TestScaleSurvivesADegenerateCanvas(t *testing.T) {
	// A document with no reference resolution, or a window minimised to zero,
	// must not produce a NaN that then propagates into every rect on screen.
	for _, c := range []Canvas{
		{RefW: 0, RefH: 0, Match: 0.5},
		{RefW: 1920, RefH: 1080, Match: 0.5},
	} {
		for _, fb := range [][2]float32{{0, 0}, {1920, 1080}, {1, 1}} {
			s := c.Scale(fb[0], fb[1])
			if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) || s <= 0 {
				t.Errorf("canvas %+v at %v gave scale %v", c, fb, s)
			}
		}
	}
}

func TestPinnedAccessorsRoundTrip(t *testing.T) {
	for _, pivot := range []mgl32.Vec2{{0, 0}, {0.5, 0.5}, {1, 1}, {0.25, 0.75}} {
		r := DefaultRectXf()
		r.Pivot = pivot
		r.AnchorMin, r.AnchorMax = mgl32.Vec2{0.5, 0.5}, mgl32.Vec2{0.5, 0.5}
		r.SetPinnedX(120, 200)
		r.SetPinnedY(-40, 60)

		if pos, w := r.PinnedX(); !closeF(pos, 120) || !closeF(w, 200) {
			t.Errorf("pivot %v: x round-tripped to %v/%v, want 120/200", pivot, pos, w)
		}
		if pos, h := r.PinnedY(); !closeF(pos, -40) || !closeF(h, 60) {
			t.Errorf("pivot %v: y round-tripped to %v/%v, want -40/60", pivot, pos, h)
		}
	}
}

func TestStretchedReportsTheAxis(t *testing.T) {
	r := DefaultRectXf()
	if !r.StretchedX() || !r.StretchedY() {
		t.Error("the default rect should stretch on both axes")
	}
	r.AnchorMin, r.AnchorMax = mgl32.Vec2{0, 0}, mgl32.Vec2{0, 1}
	if r.StretchedX() {
		t.Error("x reported stretched with equal x anchors")
	}
	if !r.StretchedY() {
		t.Error("y reported pinned with differing y anchors")
	}
}

// A world-anchored node keeps its authored size and puts its pivot where the
// host says the point is on screen; behind the camera it is culled along with
// everything under it; with no host installed it stays where it was authored.
func TestSolveWorldAnchor(t *testing.T) {
	prev := WorldProject
	t.Cleanup(func() { WorldProject = prev })

	const w, h = 1000, 800
	rect := RectXf{Offset: [4]float32{16, 16, -216, -66}, Pivot: mgl32.Vec2{0.5, 1}, Scale: mgl32.Vec2{1, 1}}
	s, c := child(rect)
	c.Children = []Node{NewNode("label", KindText)}
	root := s.Canvas.RootRect(w, h)

	var at mgl32.Vec3
	WorldProject = func(p mgl32.Vec3) (float32, float32, bool) {
		at = p
		if p.Z() > 0 {
			return 0, 0, false
		}
		return 0.25, 0.75, true
	}
	c.SetWorldAnchor(mgl32.Vec3{1, 2, -3})
	s.Solve(w, h)
	want := Rect{root.W*0.25 - 100, root.H*0.75 - 50, 200, 50}
	if got := c.Solved(); !rectClose(got, want) {
		t.Errorf("anchored rect = %+v, want %+v", got, want)
	}
	if at != (mgl32.Vec3{1, 2, -3}) {
		t.Errorf("projected %v, want the anchor itself", at)
	}
	if c.Culled() {
		t.Error("an anchor in front of the camera culled the node")
	}

	c.SetWorldAnchor(mgl32.Vec3{0, 0, 5})
	s.Solve(w, h)
	if !c.Culled() {
		t.Error("an anchor behind the camera left the node drawn")
	}

	c.ClearWorldAnchor()
	s.Solve(w, h)
	if got := c.Solved(); !rectClose(got, Rect{16, 16, 200, 50}) || c.Culled() {
		t.Errorf("cleared anchor: rect %+v culled %v, want the authored rect", got, c.Culled())
	}

	WorldProject = nil
	c.SetWorldAnchor(mgl32.Vec3{1, 2, -3})
	s.Solve(w, h)
	if got := c.Solved(); !rectClose(got, Rect{16, 16, 200, 50}) {
		t.Errorf("no host: rect %+v, want the authored rect", got)
	}
}
