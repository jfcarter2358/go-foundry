package ui

import (
	"testing"

	"foundry/internal/hooks"

	"github.com/go-gl/mathgl/mgl32"
)

func draggableAt(id string, kind Kind, x, y, w, h float32) Node {
	n := at(id, kind, x, y, w, h)
	n.Draggable = true
	return n
}

func TestDraggingAnInertWidget(t *testing.T) {
	// An inventory icon is an image: inert, but draggable makes it something
	// the pointer can pick up.
	st := stackWith(screenOf("inv",
		draggableAt("icon", KindImage, 10, 10, 50, 50),
		at("slot", KindButton, 300, 10, 50, 50),
	))

	if !st.PointerDown(20, 20) {
		t.Fatal("a press on a draggable image went through to the world")
	}
	st.PointerMove(22, 21)
	st.Tick(0, 1000, 800)
	if evs := hooks.TakeUIEvents(); len(evs) != 0 {
		t.Fatalf("two pixels of travel posted %v; a hand's wobble is not a drag", atoms(evs))
	}

	st.PointerMove(40, 20)
	st.PointerMove(60, 25) // several moves in one frame are one :drag:
	st.Tick(0, 1000, 800)
	evs := hooks.TakeUIEvents()
	if got := atoms(evs); len(got) != 2 || got[0] != "drag_start" || got[1] != "drag" {
		t.Fatalf("dragging posted %v, want drag_start then one drag", got)
	}
	if evs[0].X != 20 || evs[0].Y != 20 {
		t.Errorf("drag_start at %g,%g, want where the press began (20,20)", evs[0].X, evs[0].Y)
	}
	if evs[1].X != 60 || evs[1].DX != 40 || evs[1].DY != 5 {
		t.Errorf("drag at %g with delta %g,%g; want 60 and 40,5", evs[1].X, evs[1].DX, evs[1].DY)
	}
	if tgt, on := st.Dragging(); !on || !tgt.Is("inv", "icon") {
		t.Errorf("Dragging() = %v, %v", tgt, on)
	}

	// Carried under the pointer, the icon is over the slot when it is let go.
	// The drop must name the slot, not the icon itself.
	icon := st.Find("inv").Find("icon")
	icon.SetTranslate(mgl32.Vec2{300, 0})
	st.Tick(0, 1000, 800)
	hooks.TakeUIEvents()
	st.PointerUp(320, 30)
	evs = hooks.TakeUIEvents()
	if len(evs) != 1 || evs[0].Atom != "drop" {
		t.Fatalf("letting go posted %v, want one drop", atoms(evs))
	}
	if evs[0].Other != "slot" || evs[0].Widget != "icon" {
		t.Errorf("dropped %q onto %q, want icon onto slot", evs[0].Widget, evs[0].Other)
	}
	if _, on := st.Dragging(); on {
		t.Error("still dragging after the drop")
	}
}

func TestADraggableButtonStillClicks(t *testing.T) {
	st := stackWith(screenOf("s", draggableAt("b", KindButton, 0, 0, 100, 100)))
	st.PointerDown(50, 50)
	st.PointerMove(51, 50)
	st.Tick(0, 1000, 800)
	st.PointerUp(51, 50)
	if got := atoms(hooks.TakeUIEvents()); len(got) != 1 || got[0] != "click" {
		t.Fatalf("a press and release with no drag posted %v, want click", got)
	}
}

func TestDropOverNothing(t *testing.T) {
	st := stackWith(screenOf("s", draggableAt("b", KindPanel, 0, 0, 100, 100)))
	st.PointerDown(50, 50)
	st.PointerMove(500, 500)
	st.Tick(0, 1000, 800)
	hooks.TakeUIEvents()
	st.PointerUp(500, 500)
	evs := hooks.TakeUIEvents()
	if len(evs) != 1 || evs[0].Atom != "drop" || evs[0].Other != "" {
		t.Fatalf("a drop over empty space posted %+v", evs)
	}
}

func TestTranslateMovesTheWidgetAndItsChildren(t *testing.T) {
	parent := at("p", KindPanel, 0, 0, 100, 100)
	parent.Children = []Node{at("c", KindButton, 10, 10, 20, 20)}
	st := stackWith(screenOf("s", parent))
	st.Find("s").Find("p").SetTranslate(mgl32.Vec2{200, 50})
	st.Tick(0, 1000, 800)
	r := st.Find("s").Find("c").Solved()
	if r.X != 210 || r.Y != 60 {
		t.Fatalf("the child of a translated panel is at %g,%g, want 210,60", r.X, r.Y)
	}
}

func TestClickThroughLetsEmptySpaceReachTheWorld(t *testing.T) {
	s := screenOf("build", at("ok", KindButton, 0, 0, 100, 100))
	s.Modal = true
	st := stackWith(s)
	if !st.PointerDown(500, 500) {
		t.Fatal("a modal screen let a click through without click_through")
	}
	st.PointerUp(500, 500)

	s.ClickThrough = true
	if st.PointerDown(500, 500) {
		t.Fatal("click_through kept a click on empty space from the world")
	}
	if !st.PointerDown(50, 50) {
		t.Fatal("click_through let a click on its own button through to the world")
	}
	st.PointerUp(50, 50)
	if st.CapturesPointer() {
		t.Error("CapturesPointer under a click-through modal screen")
	}
	if !st.CapturesInput() {
		t.Error("click_through released the keys too; only the pointer goes through")
	}
	if st.Wheel(1) {
		t.Error("the wheel over a click-through screen did not reach the world")
	}
}

func TestPointerOver(t *testing.T) {
	st := stackWith(screenOf("hud", at("b", KindButton, 0, 0, 100, 100), at("label", KindText, 200, 0, 100, 100)))
	if !st.PointerOver(50, 50) {
		t.Error("not over a button the pointer is on")
	}
	if st.PointerOver(250, 50) {
		t.Error("an inert label counts as UI under the pointer; clicks pass through it")
	}
	if st.PointerOver(500, 500) {
		t.Error("over UI on empty space")
	}
}
