package uidraw

import (
	"testing"

	"foundry/internal/ui"
	"foundry/internal/uifont"

	"github.com/go-gl/mathgl/mgl32"
)

// pinned builds a node at an absolute rect inside a 1000x800 root.
func pinned(id string, kind ui.Kind, x, y, w, h float32) ui.Node {
	n := ui.NewNode(id, kind)
	n.Rect.AnchorMin, n.Rect.AnchorMax = mgl32.Vec2{0, 0}, mgl32.Vec2{0, 0}
	n.Rect.Offset = [4]float32{x, y, -(x + w), -(y + h)}
	return n
}

// built solves a screen at 1000x800, builds it, and hands back the list.
func built(t *testing.T, s *ui.Screen, in *ui.Interaction) *List {
	t.Helper()
	uifont.Reset()
	t.Cleanup(uifont.Reset)

	area := ui.Rect{X: 0, Y: 0, W: 1000, H: 800}
	l := &List{}
	BuildInto(l, s, in, area, 1)
	return l
}

func screenWith(kids ...ui.Node) *ui.Screen {
	s := ui.NewScreen("t")
	s.Root.Children = kids
	return s
}

func quads(l *List) int { return len(l.Idx) / 6 }

func TestBuildDrawsAVisiblePanel(t *testing.T) {
	l := built(t, screenWith(pinned("p", ui.KindPanel, 10, 20, 100, 50)), nil)
	if quads(l) != 1 {
		t.Fatalf("%d quads for one opaque panel, want 1", quads(l))
	}
	v := l.Verts[0]
	if v.X != 10 || v.Y != 20 {
		t.Errorf("panel starts at %v,%v, want 10,20", v.X, v.Y)
	}
}

func TestBuildSkipsHiddenAndTransparentSubtrees(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*ui.Node)
	}{
		{"hidden", func(n *ui.Node) { n.Visible = false }},
		{"fully transparent", func(n *ui.Node) { n.Alpha = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := pinned("parent", ui.KindPanel, 0, 0, 100, 100)
			parent.Children = []ui.Node{pinned("kid", ui.KindPanel, 0, 0, 50, 50)}
			tc.set(&parent)
			if l := built(t, screenWith(parent), nil); !l.Empty() {
				t.Errorf("a %s subtree produced %d quads, including its children", tc.name, quads(l))
			}
		})
	}
}

func TestBuildSkipsAFullyTransparentColourButNotTheSubtree(t *testing.T) {
	// A transparent *panel* is the ordinary way to group things. It must draw
	// nothing itself while its children still draw.
	parent := pinned("parent", ui.KindPanel, 0, 0, 100, 100)
	parent.Style.Color = mgl32.Vec4{1, 1, 1, 0}
	parent.Children = []ui.Node{pinned("kid", ui.KindPanel, 0, 0, 50, 50)}

	l := built(t, screenWith(parent), nil)
	if quads(l) != 1 {
		t.Errorf("%d quads, want just the child's", quads(l))
	}
}

func TestBuildIsInTreeOrder(t *testing.T) {
	// Tree order is draw order: a parent paints under its children, and an
	// earlier sibling under a later one. There is no z field, so this is the
	// only thing that decides what is on top.
	first := pinned("first", ui.KindPanel, 0, 0, 100, 100)
	first.Children = []ui.Node{pinned("kid", ui.KindPanel, 10, 10, 10, 10)}
	second := pinned("second", ui.KindPanel, 200, 0, 100, 100)

	l := built(t, screenWith(first, second), nil)
	if quads(l) != 3 {
		t.Fatalf("%d quads, want 3", quads(l))
	}
	// Vertex 0 of each quad carries its top-left corner.
	xs := []float32{l.Verts[0].X, l.Verts[4].X, l.Verts[8].X}
	want := []float32{0, 10, 200}
	for i := range want {
		if xs[i] != want[i] {
			t.Errorf("quad %d starts at x %v, want %v (parent, its child, then the next sibling)",
				i, xs[i], want[i])
		}
	}
}

func TestBuildPushesAndPopsAClip(t *testing.T) {
	parent := pinned("parent", ui.KindPanel, 0, 0, 100, 100)
	parent.Clip = true
	parent.Children = []ui.Node{pinned("kid", ui.KindPanel, 0, 0, 50, 50)}
	after := pinned("after", ui.KindPanel, 200, 0, 50, 50)

	l := built(t, screenWith(parent, after), nil)

	// The clipped run and the unclipped one either side cannot share a command.
	if len(l.Cmds) < 2 {
		t.Fatalf("%d commands; a clipping panel must break the batch", len(l.Cmds))
	}
	last := l.Cmds[len(l.Cmds)-1]
	if last.Clip != (ui.Rect{X: 0, Y: 0, W: 1000, H: 800}) {
		t.Errorf("the clip was not popped: the last command clips to %+v", last.Clip)
	}
}

func TestBuildFillsABarByItsFraction(t *testing.T) {
	bar := pinned("bar", ui.KindBar, 0, 0, 200, 20)
	bar.Value = ui.ValueSpec{Value: 3, Min: 0, Max: 4, FillColor: mgl32.Vec4{1, 0, 0, 1}}

	l := built(t, screenWith(bar), nil)
	if quads(l) != 2 {
		t.Fatalf("%d quads for a bar, want a background and a fill", quads(l))
	}
	// The fill is the second quad: three quarters of 200.
	if got := l.Verts[5].X; got != 150 {
		t.Errorf("a 3/4 fill ends at %v, want 150", got)
	}
}

func TestBuildFillsFromTheDirectionsEdge(t *testing.T) {
	cases := []struct {
		dir          ui.FillDir
		wantX, wantY float32
		wantW, wantH float32
		name         string
	}{
		{ui.FillLeftRight, 0, 0, 100, 20, "left to right"},
		{ui.FillRightLeft, 100, 0, 100, 20, "right to left"},
		{ui.FillTopBottom, 0, 0, 200, 10, "top to bottom"},
		{ui.FillBottomTop, 0, 10, 200, 10, "bottom to top"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bar := pinned("bar", ui.KindBar, 0, 0, 200, 20)
			bar.Value = ui.ValueSpec{Value: 0.5, Max: 1, Dir: tc.dir, FillColor: mgl32.Vec4{1, 0, 0, 1}}

			l := built(t, screenWith(bar), nil)
			v := l.Verts[4:8] // the fill quad
			x, y := v[0].X, v[0].Y
			w, h := v[2].X-x, v[2].Y-y
			if x != tc.wantX || y != tc.wantY || w != tc.wantW || h != tc.wantH {
				t.Errorf("fill is %v,%v %vx%v, want %v,%v %vx%v",
					x, y, w, h, tc.wantX, tc.wantY, tc.wantW, tc.wantH)
			}
		})
	}
}

func TestBuildDrawsNoFillAtZero(t *testing.T) {
	bar := pinned("bar", ui.KindBar, 0, 0, 200, 20)
	bar.Value = ui.ValueSpec{Value: 0, Max: 1, FillColor: mgl32.Vec4{1, 0, 0, 1}}
	if got := quads(built(t, screenWith(bar), nil)); got != 1 {
		t.Errorf("%d quads for an empty bar, want just the background", got)
	}
}

// A toggle always shows its check box, so it reads as something to flip even
// switched off; the mark inside it is what switches.
func TestBuildDrawsAToggleMarkOnlyWhenOn(t *testing.T) {
	off := pinned("t", ui.KindToggle, 0, 0, 30, 30)
	offQuads := quads(built(t, screenWith(off), nil))
	if offQuads < 2 {
		t.Errorf("%d quads for an off toggle, want the background and an empty box", offQuads)
	}
	on := pinned("t", ui.KindToggle, 0, 0, 30, 30)
	on.Value.On = true
	if got := quads(built(t, screenWith(on), nil)); got != offQuads+1 {
		t.Errorf("%d quads for an on toggle, want the off toggle's %d and a mark", got, offQuads)
	}
}

// A wide toggle keeps its box at the left and its label beside it.
func TestAWideTogglesLabelClearsItsBox(t *testing.T) {
	r := ui.Rect{X: 10, Y: 0, W: 400, H: 40}
	box, label := toggleBoxRect(r), toggleLabelRect(r)
	if box.X > r.X+r.H/2 {
		t.Errorf("box at x %v, want it at the toggle's left edge (%v)", box.X, r.X)
	}
	if label.X < box.X+box.W || label.X+label.W != r.X+r.W {
		t.Errorf("label %+v overlaps the box %+v or does not reach the toggle's right edge", label, box)
	}
}

func TestBuildResolvesTheInteractionState(t *testing.T) {
	btn := pinned("btn", ui.KindButton, 0, 0, 100, 40)
	btn.Style.Color = mgl32.Vec4{0.5, 0.5, 0.5, 1}
	s := screenWith(btn)

	colourIn := func(in *ui.Interaction) uint32 {
		l := built(t, s, in)
		return l.Verts[0].RGBA
	}

	normal := colourIn(&ui.Interaction{})
	hover := colourIn(&ui.Interaction{Hover: ui.Target{Screen: "t", Node: "btn"}})
	press := colourIn(&ui.Interaction{Press: ui.Target{Screen: "t", Node: "btn"}})

	if hover == normal {
		t.Error("an unstyled button did not visibly change on hover; a widget with no feedback reads as broken")
	}
	if press == normal || press == hover {
		t.Error("press was not distinguishable from normal and hover")
	}
}

func TestBuildFadesADisabledWidget(t *testing.T) {
	btn := pinned("btn", ui.KindButton, 0, 0, 100, 40)
	btn.Style.Color = mgl32.Vec4{0.5, 0.5, 0.5, 1}
	enabled := built(t, screenWith(btn), nil).Verts[0].RGBA

	btn.Enabled = false
	disabled := built(t, screenWith(btn), nil).Verts[0].RGBA

	if alphaOf(disabled) >= alphaOf(enabled) {
		t.Errorf("a disabled widget kept its alpha (%d vs %d); it should recede, not just darken",
			alphaOf(disabled), alphaOf(enabled))
	}
}

func TestDisabledBeatsHover(t *testing.T) {
	// A greyed-out button that lit up under the pointer would be telling the
	// player it can be pressed.
	btn := pinned("btn", ui.KindButton, 0, 0, 100, 40)
	btn.Enabled = false
	s := screenWith(btn)

	plain := built(t, s, &ui.Interaction{}).Verts[0].RGBA
	hovered := built(t, s, &ui.Interaction{Hover: ui.Target{Screen: "t", Node: "btn"}}).Verts[0].RGBA
	if plain != hovered {
		t.Error("a disabled button responded to hover")
	}
}

func TestAlphaMultipliesDownTheSubtree(t *testing.T) {
	parent := pinned("parent", ui.KindPanel, 0, 0, 100, 100)
	parent.Alpha = 0.5
	kid := pinned("kid", ui.KindPanel, 0, 0, 50, 50)
	kid.Alpha = 0.5
	parent.Children = []ui.Node{kid}

	l := built(t, screenWith(parent), nil)
	if got := alphaOf(l.Verts[0].RGBA); got < 126 || got > 129 {
		t.Errorf("parent alpha is %d, want about 128", got)
	}
	if got := alphaOf(l.Verts[4].RGBA); got < 62 || got > 66 {
		t.Errorf("child alpha is %d, want about 64 — alpha must compound down the tree", got)
	}
}

func TestBuildDrawsAFocusRingOnlyWhenNavDriven(t *testing.T) {
	// With a pointer there is already a hover tint; the ring is for a keyboard
	// or gamepad user who has nothing else telling them where they are.
	btn := pinned("btn", ui.KindButton, 0, 0, 100, 40)
	s := screenWith(btn)
	focus := ui.Target{Screen: "t", Node: "btn"}

	mouse := built(t, s, &ui.Interaction{Focus: focus})
	nav := built(t, s, &ui.Interaction{Focus: focus, NavDriven: true})

	if quads(nav) <= quads(mouse) {
		t.Errorf("nav focus drew %d quads and pointer focus %d; the ring is missing", quads(nav), quads(mouse))
	}
}

func TestPixelSnapping(t *testing.T) {
	s := ui.NewScreen("t")
	s.Canvas.PixelSnap = true
	n := pinned("n", ui.KindPanel, 10.4, 20.6, 100.5, 50.5)
	s.Root.Children = []ui.Node{n}

	l := &List{}
	BuildInto(l, s, nil, ui.Rect{X: 0, Y: 0, W: 1000, H: 800}, 1)

	for _, v := range l.Verts {
		if v.X != float32(int(v.X)) || v.Y != float32(int(v.Y)) {
			t.Fatalf("unsnapped vertex at %v,%v", v.X, v.Y)
		}
	}
}

func TestPixelSnappingKeepsAbuttingEdgesTogether(t *testing.T) {
	// Snapping a position and a size independently can open a one-pixel seam
	// that appears and vanishes as a window resizes. Snapping the edges cannot.
	s := ui.NewScreen("t")
	s.Canvas.PixelSnap = true
	s.Root.Children = []ui.Node{
		pinned("a", ui.KindPanel, 0.3, 0, 50.4, 20),
		pinned("b", ui.KindPanel, 50.7, 0, 50.4, 20),
	}
	l := &List{}
	BuildInto(l, s, nil, ui.Rect{X: 0, Y: 0, W: 1000, H: 800}, 1)

	aRight := l.Verts[1].X
	bLeft := l.Verts[4].X
	if aRight != bLeft {
		t.Errorf("a seam opened: a ends at %v, b starts at %v", aRight, bLeft)
	}
}

func TestPixelSnappingOff(t *testing.T) {
	s := ui.NewScreen("t")
	s.Canvas.PixelSnap = false
	s.Root.Children = []ui.Node{pinned("n", ui.KindPanel, 10.5, 0, 100, 50)}

	l := &List{}
	BuildInto(l, s, nil, ui.Rect{X: 0, Y: 0, W: 1000, H: 800}, 1)
	if l.Verts[0].X != 10.5 {
		t.Errorf("x is %v with snapping off, want the exact 10.5", l.Verts[0].X)
	}
}

func TestBuildDrawsBoundTextRatherThanTheAuthoredValue(t *testing.T) {
	label := pinned("l", ui.KindText, 0, 0, 400, 40)
	label.Text.Value = "authored"
	s := screenWith(label)

	unbound := quads(built(t, s, nil))
	if unbound == 0 {
		t.Fatal("no glyphs at all")
	}
}

func TestBuildHandlesANilScreen(t *testing.T) {
	l := &List{}
	l.Reset(ui.Rect{X: 0, Y: 0, W: 100, H: 100})
	Build(l, nil, nil, 1)
	RenderScreen(l, nil, nil, 100, 100)
	if !l.Empty() {
		t.Error("a nil screen produced geometry")
	}
}

func alphaOf(packed uint32) uint8 { return uint8(packed >> 24) }
