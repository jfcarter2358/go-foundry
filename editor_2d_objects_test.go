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
	prevSpawn := Editor.SpawnSelected
	prevFocus, prevGizmo := Editor.PanelFocus, Editor.GizmoKind
	// 60 px/unit → the 6 px minimum half-extent is 0.1 world units, well under
	// the object sizes the cases below use.
	Editor.Zoom2D = 60
	WinWidth, WinHeight = 1280, 720
	Editor.SpawnSelected = false
	t.Cleanup(func() {
		Editor.Zoom2D, WinWidth, WinHeight = prevZoom, prevW, prevH
		Editor.SpawnSelected = prevSpawn
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

func TestEditor2DPickObjectHitsTheSpawnPoint(t *testing.T) {
	objectPickTestSetup(t)

	ch := &Chunk{}
	lvl := &Level{Spawn: &SpawnPoint{Position: mgl32.Vec3{2, 0, 2}}}

	// Inside the marker's footprint (half-extents 0.5 x 0.25, both above the
	// 0.1 screen-size floor).
	if !editor2DPickObject(lvl, ch, 2.4, 2.1) {
		t.Fatal("click inside the spawn footprint should hit")
	}
	if !Editor.SpawnSelected {
		t.Error("a hit did not select the spawn point")
	}
	if Editor.PanelFocus != panelFocusSpawn {
		t.Errorf("PanelFocus = %q, want %q", Editor.PanelFocus, panelFocusSpawn)
	}
	if Editor.GizmoKind != gizmoKindSpawn {
		t.Errorf("GizmoKind = %q, want %q", Editor.GizmoKind, gizmoKindSpawn)
	}

	// A miss leaves the selection untouched so the caller can fall through to
	// sector selection.
	if editor2DPickObject(lvl, ch, 40, 40) {
		t.Error("click on empty space should not hit an object")
	}
	if !Editor.SpawnSelected {
		t.Error("a miss dropped the existing selection")
	}
}

func TestEditor2DPickObjectWithNoSpawnPoint(t *testing.T) {
	objectPickTestSetup(t)

	if editor2DPickObject(&Level{}, &Chunk{}, 0, 0) {
		t.Error("a level with no spawn point has nothing to pick")
	}
	if Editor.SpawnSelected {
		t.Error("picking nothing selected the spawn point anyway")
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
