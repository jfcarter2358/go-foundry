package main

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// objectPickTestSetup pins the view transform so screenToWorldSize (and hence
// the minimum footprint size) is deterministic, and restores the editor
// selection afterwards.
func objectPickTestSetup(t *testing.T) {
	t.Helper()
	prevZoom, prevW, prevH := Editor.Zoom2D, WinWidth, WinHeight
	prevEnt, prevPrim := Editor.ActiveEntityIdx, Editor.ActivePrimitiveIdx
	prevFocus, prevGizmo := Editor.PanelFocus, Editor.GizmoKind
	// 60 px/unit → the 6 px minimum half-extent is 0.1 world units, well under
	// the object sizes the cases below use.
	Editor.Zoom2D = 60
	WinWidth, WinHeight = 1280, 720
	Editor.ActiveEntityIdx = -1
	Editor.ActivePrimitiveIdx = -1
	t.Cleanup(func() {
		Editor.Zoom2D, WinWidth, WinHeight = prevZoom, prevW, prevH
		Editor.ActiveEntityIdx, Editor.ActivePrimitiveIdx = prevEnt, prevPrim
		Editor.PanelFocus, Editor.GizmoKind = prevFocus, prevGizmo
	})
}

func TestEditor2DObjectHalfExtentsFloorsAtScreenSize(t *testing.T) {
	objectPickTestSetup(t)

	// At 60 px/unit the floor is 6/60 = 0.1 world units.
	hx, hz := editor2DObjectHalfExtents(0, 0)
	if hx != 0.1 || hz != 0.1 {
		t.Errorf("zero-size object: got (%v,%v), want (0.1,0.1)", hx, hz)
	}
	// Anything already larger is left alone.
	hx, hz = editor2DObjectHalfExtents(2, 0.05)
	if hx != 2 {
		t.Errorf("large half-X was clamped: got %v, want 2", hx)
	}
	if hz != 0.1 {
		t.Errorf("small half-Z was not floored: got %v, want 0.1", hz)
	}
}

func TestEditor2DPickObject(t *testing.T) {
	objectPickTestSetup(t)

	ch := &Chunk{
		Primitives: []Primitive{
			{ID: "far", Position: mgl32.Vec3{10, 0, 10}, BoundHalfX: 1, BoundHalfY: 1, BoundHalfZ: 1},
			{ID: "near", Position: mgl32.Vec3{2, 0, 2}, BoundHalfX: 1, BoundHalfY: 1, BoundHalfZ: 1},
		},
	}
	lvl := &Level{
		Entities: []Entity{
			{ID: "ent", Position: mgl32.Vec3{-5, 0, -5}, BoundHalfX: 0.5, BoundHalfY: 1, BoundHalfZ: 0.5},
		},
	}

	// Inside the second primitive's footprint.
	if !editor2DPickObject(lvl, ch, 2.4, 1.7) {
		t.Fatal("click inside a primitive footprint should hit")
	}
	if Editor.ActivePrimitiveIdx != 1 || Editor.ActiveEntityIdx != -1 {
		t.Errorf("got prim=%d ent=%d, want prim=1 ent=-1",
			Editor.ActivePrimitiveIdx, Editor.ActiveEntityIdx)
	}
	if Editor.PanelFocus != panelFocusPrimitive {
		t.Errorf("PanelFocus = %q, want %q", Editor.PanelFocus, panelFocusPrimitive)
	}

	// Entities are hit too, and the previous primitive selection is dropped.
	if !editor2DPickObject(lvl, ch, -5.2, -4.8) {
		t.Fatal("click inside an entity footprint should hit")
	}
	if Editor.ActiveEntityIdx != 0 || Editor.ActivePrimitiveIdx != -1 {
		t.Errorf("got ent=%d prim=%d, want ent=0 prim=-1",
			Editor.ActiveEntityIdx, Editor.ActivePrimitiveIdx)
	}

	// A miss leaves the selection untouched so the caller can fall through to
	// sector selection.
	if editor2DPickObject(lvl, ch, 40, 40) {
		t.Error("click on empty space should not hit an object")
	}
	if Editor.ActiveEntityIdx != 0 {
		t.Errorf("a miss changed the selection: ent=%d, want 0", Editor.ActiveEntityIdx)
	}
}

func TestEditor2DPickObjectPrefersNearestCentre(t *testing.T) {
	objectPickTestSetup(t)

	// Two overlapping primitives: the click sits inside both, closer to #1.
	ch := &Chunk{
		Primitives: []Primitive{
			{ID: "wide", Position: mgl32.Vec3{0, 0, 0}, BoundHalfX: 4, BoundHalfZ: 4},
			{ID: "tight", Position: mgl32.Vec3{3, 0, 3}, BoundHalfX: 1, BoundHalfZ: 1},
		},
	}
	lvl := &Level{}

	if !editor2DPickObject(lvl, ch, 3.1, 3.1) {
		t.Fatal("click inside both footprints should hit")
	}
	if Editor.ActivePrimitiveIdx != 1 {
		t.Errorf("ActivePrimitiveIdx = %d, want 1 (nearest centre)", Editor.ActivePrimitiveIdx)
	}
}

func TestEditor2DFloorHeightAt(t *testing.T) {
	ch := splitTestChunk()
	ch.Sectors[0].Floor.Height = 3
	ch.Sectors[1].Floor.Height = -2

	cases := []struct {
		name   string
		wx, wz float32
		want   float32
	}{
		{"inside sector A", 5, 5, 3},
		{"inside sector B", 15, 5, -2},
		{"outside every sector", -5, -5, 0},
	}
	for _, c := range cases {
		if got := editor2DFloorHeightAt(ch, c.wx, c.wz); got != c.want {
			t.Errorf("%s: editor2DFloorHeightAt(%v,%v) = %v, want %v",
				c.name, c.wx, c.wz, got, c.want)
		}
	}
}
