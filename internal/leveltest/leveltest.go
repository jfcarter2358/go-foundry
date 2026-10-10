package leveltest

// Package leveltest holds level fixtures that more than one package's tests
// build on.
//
// It is an ordinary package rather than a _test.go file because a test helper
// cannot cross a package boundary: once the editor and the collision resolver
// live in different packages, a chunk they both want to test against has to be
// somewhere both can import. Everything here is deterministic and GL-free.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/testenv"
	"foundry/internal/world"
)

// TwoRoomChunk builds two hollow rooms side by side, sharing no geometry:
// one spanning x:[0,10], the other x:[10,20], both z:[0,10], both floors at
// y=0 and ceilings at y=8.
func TwoRoomChunk() *world.Chunk {
	ch := &world.Chunk{ID: "two_room_chunk"}
	ch.Brushes = append(ch.Brushes,
		world.NewHollowRoom("A", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{10, 8, 10}, 0.5, "concrete")...)
	ch.Brushes = append(ch.Brushes,
		world.NewHollowRoom("B", mgl32.Vec3{10, 0, 0}, mgl32.Vec3{20, 8, 10}, 0.5, "concrete")...)
	return ch
}

// RoomChunk builds a single hollow room, GL-free: 10x10 footprint, floor at
// y=0, ceiling at y=4, walls all round.
func RoomChunk(t *testing.T) *world.Chunk {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	ch := &world.Chunk{ID: "room_chunk"}
	ch.Brushes = world.NewHollowRoom("room", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{10, 4, 10}, 0.5, "concrete")
	return ch
}

// LoadGeometry builds the CPU mesh for every brush face in ch — what a
// triangle test needs.
//
// Brush.Load is what a level load calls, and Geometry.Load's upload half is
// gated on gfx.GeometryUploadsEnabled, so this leaves real vertices behind
// with no GL context. Anything testing against what is *drawn* rather than
// against a brush's own numbers has to come through here first.
func LoadGeometry(t *testing.T, ch *world.Chunk) {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	for bi := range ch.Brushes {
		b := &ch.Brushes[bi]
		if err := b.Load(); err != nil {
			t.Fatalf("loading brush %q: %v", b.ID, err)
		}
	}
	ch.RecomputeBounds()
}
