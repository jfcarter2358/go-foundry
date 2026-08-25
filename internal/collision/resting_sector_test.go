package collision

// resting_sector_test.go — the consequence of the vertical-bounds tolerance,
// checked where it actually bites.
//
// world.chunkContainsY is where the fix lives and where its edge cases are
// covered. This is the behaviour that motivated it: something standing still on
// the ground has to know which sector it is standing in, or every script that
// reacts to a sector reacts to nothing.
//
// The floor heights below are not arbitrary. A resting position is reached by
// adding a clamped volume centre and subtracting a bound offset, and whether the
// leftover rounds a few nanometres above or below the floor depends on the
// magnitudes involved. Most heights land exactly on it — which is inside, since
// the test is inclusive — and only some land under. A floor at y=0 is one of the
// ones that lands under, and it is also the most common thing an author builds.

import (
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// restingLevel is collisionTestLevel with its floors moved to floorY.
func restingLevel(t *testing.T, floorY float32) *world.Level {
	t.Helper()
	lvl := collisionTestLevel()
	if floorY != 0 {
		for si := range lvl.Chunks[0].Sectors {
			lvl.Chunks[0].Sectors[si].Floor.Height = floorY
			lvl.Chunks[0].Sectors[si].Ceiling.Height = floorY + 8
		}
		lvl.Chunks[0].RecomputeBounds()
		if err := world.RebuildIndexes(lvl); err != nil {
			t.Fatal(err)
		}
	}
	return lvl
}

func TestAnEntityAtRestKnowsWhichSectorItIsIn(t *testing.T) {
	withCollisionMasters(t, true, true)

	for _, floorY := range []float32{0, 0.1, 1, -3.5, 137.25} {
		lvl := restingLevel(t, floorY)

		e := solidEntity("crate", mgl32.Vec3{5, floorY + 4, 5})
		e.BoundOffset = mgl32.Vec3{0, 0.5, 0}
		lvl.Entities = append(lvl.Entities, e)
		for i := 0; i < 500; i++ {
			EntityUpdate(lvl, 1.0/60.0, true, nil)
		}

		got := world.FindEntity(lvl, "crate")
		if !got.Collision.OnGround {
			t.Errorf("floor %v: never came to rest (y=%v)", floorY, got.Position.Y())
			continue
		}
		if got.CurrentSector == "" {
			t.Errorf("floor %v: at rest on the floor at y=%.12g but in no sector "+
				"(the chunk's own MinBound.Y is %.12g)",
				floorY, got.Position.Y(), lvl.Chunks[0].MinBound.Y())
		}
	}
}

func TestARestingEntityStaysInItsSectorForever(t *testing.T) {
	// Not a different bug, a different symptom: the lookup runs every frame, so
	// something that settles below the boundary is not momentarily lost, it is
	// permanently in no sector while it sits still.
	withCollisionMasters(t, true, true)
	lvl := restingLevel(t, 0)

	e := solidEntity("crate", mgl32.Vec3{5, 4, 5})
	e.BoundOffset = mgl32.Vec3{0, 0.5, 0}
	lvl.Entities = append(lvl.Entities, e)
	for i := 0; i < 200; i++ {
		EntityUpdate(lvl, 1.0/60.0, true, nil)
	}
	for i := 0; i < 300; i++ {
		EntityUpdate(lvl, 1.0/60.0, true, nil)
		if world.FindEntity(lvl, "crate").CurrentSector == "" {
			t.Fatalf("frame %d after settling: the crate dropped out of its sector at y=%.12g",
				i, world.FindEntity(lvl, "crate").Position.Y())
		}
	}
}

// TestAPlayerAtRestKnowsWhichSectorItIsIn is a guard rather than a reproduction:
// it passes with or without the tolerance. The player is safe from this bug by
// accident of the floor clamp, whose two halves disagree about what a position
// means — its floor half treats the position as the body *centre*, so a standing
// player rests a half-height above the floor rather than on it. Entities get
// their own symmetric clamp and land exactly on it, which is why they were the
// ones that broke. The test stays because "a resting body knows its sector" is
// worth holding for both of them, however each one gets there.
func TestAPlayerAtRestKnowsWhichSectorItIsIn(t *testing.T) {
	for _, floorY := range []float32{0, -3.5, 137.25} {
		lvl := restingLevel(t, floorY)

		p := world.NewPlayer(mgl32.Vec3{5, floorY + 5, 5})
		landed := false
		for i := 0; i < 600 && !landed; i++ {
			landed = MovePlayer(p, lvl, walkMove(mgl32.Vec3{}))
		}
		if !landed {
			t.Errorf("floor %v: the player never landed", floorY)
			continue
		}
		// Stand still for a while: this is the state the bug broke.
		for i := 0; i < 60; i++ {
			MovePlayer(p, lvl, walkMove(mgl32.Vec3{}))
		}
		if p.CurrentSector == "" {
			t.Errorf("floor %v: standing still at y=%.12g but in no sector", floorY, p.Position.Y())
		}
	}
}
