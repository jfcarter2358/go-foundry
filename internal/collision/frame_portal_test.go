package collision

// frame_portal_test.go — walking out of a moving thing.
//
// TODO(brush migration, Phase 5): this used to cover the whole doorway
// detection/crossing/sealing behaviour (FindFramePortalCrossing), fixtured
// against sectors joined by an authored world.Portal. That detection is
// stubbed out until Volume.FrameDoorway replaces it — see frame_portal.go's
// file comment — so those tests have no code left to exercise and are gone
// until Phase 5 rebuilds both. What remains here is what still has real
// behaviour behind it: frame alignment, and CrossFramePortal's handoff math
// given a crossing constructed by hand rather than found by a query.

import (
	"math"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// shipVehicleLevel is a ship entity carrying one frame-owned chunk (its cabin),
// alongside the world.
func shipVehicleLevel(t *testing.T) *world.Level {
	t.Helper()
	lvl := &world.Level{
		Chunks: []world.Chunk{{ID: "cabin", FrameOwner: "ship"}},
		Entities: []world.Entity{{
			ID: "ship", Scale: mgl32.Vec3{1, 1, 1},
			Vehicle: &world.VehicleSpec{Mode: world.VehicleSpace, Interiors: []string{"cabin"},
				Drive: world.DefaultDriveSpec(world.VehicleSpace)},
		}},
	}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatalf("RebuildIndexes: %v", err)
	}
	return lvl
}

func TestFramesAlignedFollowsTheUpVectors(t *testing.T) {
	lvl := shipVehicleLevel(t)
	ship := world.FindEntity(lvl, "ship")
	st := ship.EnsureVehicleState()
	worldFrame := lvl.Frames.World()

	// Level, and yawed: both fine.
	for _, q := range []mgl32.Quat{mgl32.QuatIdent(), mgl32.QuatRotate(2.0, mgl32.Vec3{0, 1, 0})} {
		st.Orient = q
		world.RefreshFrames(lvl, lvl.Frames)
		if !FramesAligned(lvl.Frames.Frame("ship"), worldFrame) {
			t.Errorf("a yaw broke alignment: %v", q)
		}
	}
	// Rolled onto its side, and inverted: not fine.
	for _, q := range []mgl32.Quat{
		mgl32.QuatRotate(math.Pi/2, mgl32.Vec3{0, 0, 1}),
		mgl32.QuatRotate(math.Pi, mgl32.Vec3{1, 0, 0}),
	} {
		st.Orient = q
		world.RefreshFrames(lvl, lvl.Frames)
		if FramesAligned(lvl.Frames.Frame("ship"), worldFrame) {
			t.Errorf("a roll of %v was still considered aligned", q)
		}
	}
}

func TestCrossingAFramePortalPreservesWorldPositionAndFacing(t *testing.T) {
	lvl := shipVehicleLevel(t)
	ship := world.FindEntity(lvl, "ship")
	st := ship.EnsureVehicleState()
	// Docked somewhere well away from the origin and turned, but level.
	ship.Position = mgl32.Vec3{300, 25, -140}
	st.Orient = mgl32.QuatRotate(1.3, mgl32.Vec3{0, 1, 0})
	world.RefreshFrames(lvl, lvl.Frames)

	p := world.NewPlayer(mgl32.Vec3{2, 0.9, 1})
	p.Frame = "ship"
	lvl.Entities = append(lvl.Entities, *p)
	pl := world.FindEntity(lvl, "player")
	pl.Velocity = mgl32.Vec3{2, 0, 0}
	pl.UpdateLocation(lvl)

	beforePos := lvl.Frames.WorldPosition(pl)
	beforeVel := lvl.Frames.FrameFor(pl).DirToWorld(pl.Velocity)
	const yaw = float32(0.4)
	beforeDir := lvl.Frames.FrameFor(pl).DirToWorld(mgl32.Vec3{
		float32(math.Cos(float64(yaw))), 0, float32(math.Sin(float64(yaw)))})

	// Constructed by hand rather than found by FindFramePortalCrossing, which
	// no longer detects anything — see the file comment.
	c := &FramePortalCrossing{FromID: "ship", ToID: ""}
	newYaw := CrossFramePortal(lvl, pl, c, yaw)

	if pl.Frame != "" {
		t.Errorf("after crossing to the world the player is in frame %q", pl.Frame)
	}
	if got := lvl.Frames.WorldPosition(pl); !vecNear(got, beforePos, 1e-3) {
		t.Errorf("the crossing moved the player: %v -> %v", beforePos, got)
	}
	if got := lvl.Frames.FrameFor(pl).DirToWorld(pl.Velocity); !vecNear(got, beforeVel, 1e-3) {
		t.Errorf("the crossing changed world velocity: %v -> %v", beforeVel, got)
	}
	afterDir := mgl32.Vec3{float32(math.Cos(float64(newYaw))), 0, float32(math.Sin(float64(newYaw)))}
	if !vecNear(afterDir, beforeDir, 1e-3) {
		t.Errorf("the crossing turned the player: facing %v -> %v", beforeDir, afterDir)
	}
}
