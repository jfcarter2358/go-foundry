package compile

// dynamic_test.go — a brush a script moves takes no part in the bake.
//
// Three separate things have to be true and each fails differently. If a
// dynamic brush's own faces were compiled, a door would be baked into the world
// at whatever position it happened to be authored at and drawn twice as soon as
// it moved. If it CLIPPED its neighbours, the floor under a lift would have a
// hole in it. And if it were a solid in the partition, the space a door fills
// would be a solid leaf of the BSP for the whole life of the level.

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// A dynamic brush contributes no compiled faces of its own.
func TestDynamicBrushIsNotBaked(t *testing.T) {
	door := world.NewBoxBrush("door", mgl32.Vec3{0, 1, 0}, mgl32.Vec3{1, 1, 1}, "concrete")
	door.Dynamic = true
	lvl := levelOf(door)

	if got := mustCSG(t, lvl); len(got) != 0 {
		t.Fatalf("CSG produced %d faces from a level of one dynamic brush, want none", len(got))
	}
	if got := sourceFaceCount(lvl); got != 0 {
		t.Errorf("sourceFaceCount = %d, want 0 — a dynamic brush never reaches CSG", got)
	}
}

// It does not clip its neighbours either: the floor under a lift keeps the
// patch the lift is sitting on, because the lift will not be sitting there.
func TestDynamicBrushDoesNotClipItsNeighbours(t *testing.T) {
	floor := world.NewBoxBrush("floor", mgl32.Vec3{0, -0.5, 0}, mgl32.Vec3{8, 0.5, 8}, "concrete")

	// The same level twice: once with the lift static, once dynamic. It is
	// sunk INTO the floor rather than resting on it, so the patch underneath it
	// is genuinely buried — two flush faces are the coplanar case, which is a
	// different rule and would not exercise the clip at all.
	lift := world.NewBoxBrush("lift", mgl32.Vec3{0, 0.5, 0}, mgl32.Vec3{2, 1, 2}, "concrete")
	staticFaces := mustCSG(t, levelOf(floor, lift))

	lift.Dynamic = true
	dynamicFaces := mustCSG(t, levelOf(floor, lift))

	up := mgl32.Vec3{0, 1, 0}
	staticTop := totalArea(facesOn(staticFaces, up))
	dynamicTop := totalArea(facesOn(dynamicFaces, up))

	// The area comes out the same either way — a static lift buries a 4x4
	// patch of the 16x16 floor and adds its own 4x4 top back — so area alone
	// proves nothing. What separates the two is the CUT: a static lift leaves
	// the floor as a ring of fragments around the patch it buried, and a
	// dynamic one leaves it whole.
	if staticTop < 255 || staticTop > 257 || dynamicTop < 255 || dynamicTop > 257 {
		t.Fatalf("up-facing area: static %v, dynamic %v; expected 256 both ways", staticTop, dynamicTop)
	}
	if n := len(facesOn(staticFaces, up)); n < 2 {
		t.Fatalf("a static lift left %d up-facing faces; the fixture is not exercising the clip", n)
	}
	if n := len(facesOn(dynamicFaces, up)); n != 1 {
		t.Errorf("%d up-facing faces, want the one uncut floor top", n)
	}
}

// A room sealed except for a doorway a dynamic brush fills is NOT sealed. This
// is the consequence an author has to know about, so it is asserted rather than
// only documented: a moving brush cannot be part of the shell.
func TestADynamicBrushDoesNotSealTheLevel(t *testing.T) {
	// A hollow room with one wall replaced by a dynamic slab.
	room := world.NewHollowRoom("r", mgl32.Vec3{-4, 0, -4}, mgl32.Vec3{4, 4, 4}, 0.5, "concrete")
	for i := range room {
		if room[i].ID == "r_wall_posx" {
			room[i].Dynamic = true
		}
	}
	lvl := &world.Level{
		Chunks: []world.Chunk{{ID: "c0", Brushes: room}},
		Spawn:  &world.SpawnPoint{Position: mgl32.Vec3{0, 1, 0}},
	}

	r, err := Compile(lvl, Options{SkipLight: true})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(r.Stats.Leaks) == 0 {
		t.Error("a room whose fourth wall is dynamic compiled as sealed; " +
			"the dynamic brush was treated as part of the shell")
	}
}
