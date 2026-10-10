package editor

// editor_entity_marker_test.go — the marker's placement, which is the part of
// it a headless test can reach. The cage meshes themselves need a GL context;
// where they end up does not, and that is where the mistake would be.

import (
	"foundry/internal/gfx"
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// The unit cage under entityMarkerMatrix has to land exactly on the volume the
// entity is culled and picked by. A marker that drew somewhere else would be a
// box the author aims at and misses.
func TestEntityMarkerMatrixMatchesTheBounds(t *testing.T) {
	cases := []struct {
		name string
		e    world.Entity
	}{
		{"blank, as placed", world.NewBlankEntity("x")},
		{"offset volume", world.Entity{
			Position: mgl32.Vec3{1, 2, 3}, Scale: mgl32.Vec3{1, 1, 1},
			BoundHalfX: 0.4, BoundHalfY: 0.9, BoundHalfZ: 0.4,
			BoundOffset: mgl32.Vec3{0, 0.9, 0},
		}},
		{"scaled", world.Entity{
			Position: mgl32.Vec3{-5, 0, 2}, Scale: mgl32.Vec3{2, 2, 2},
			BoundHalfX: 0.5, BoundHalfY: 0.5, BoundHalfZ: 0.5,
			BoundOffset: mgl32.Vec3{0, 0.5, 0},
		}},
		{"unscaled zero value", world.Entity{
			BoundHalfX: 1, BoundHalfY: 1, BoundHalfZ: 1,
		}},
	}

	for _, tc := range cases {
		e := tc.e
		m := entityMarkerMatrix(&e)
		corner := m.Mul4x1(mgl32.Vec4{1, 1, 1, 1})
		got := mgl32.Vec3{corner.X(), corner.Y(), corner.Z()}
		want := e.BoundCenter().Add(e.BoundHalfExtents())
		if got.Sub(want).Len() > 1e-4 {
			t.Errorf("%s: cage corner at %v, bounds corner at %v", tc.name, got, want)
		}
	}
}

// The marker exists for entities the colour pass drew nothing for, so the two
// have to agree on what "drew nothing" means.
func TestEntityHasVisibleGeometry(t *testing.T) {
	var e world.Entity
	if entityHasVisibleGeometry(&e) {
		t.Error("an entity with no attachments has nothing drawn")
	}

	// An attachment with no uploaded submeshes — a mesh-less one, or one whose
	// file would not load — still draws nothing.
	e.Models = append(e.Models, world.EntityModel{Name: "body", Visible: true})
	if entityHasVisibleGeometry(&e) {
		t.Error("an attachment with nothing uploaded draws nothing")
	}

	e.Models[0].Submeshes = []gfx.Geometry{{Vertices: []float32{0, 0, 0, 0, 0}}}
	if !entityHasVisibleGeometry(&e) {
		t.Error("an uploaded, visible attachment draws")
	}

	e.Models[0].Visible = false
	if entityHasVisibleGeometry(&e) {
		t.Error("a hidden attachment draws nothing")
	}
}
