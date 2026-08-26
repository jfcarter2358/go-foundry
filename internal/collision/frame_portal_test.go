package collision

// frame_portal_test.go — walking out of a moving thing, and being stopped when
// that would mean walking onto a wall.

import (
	"foundry/internal/world"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// dockedLevel is a ship whose cabin has a ramp onto a world-space deck. The
// cabin is sector A of one chunk and the deck is sector B of another, joined by
// a frame portal in each direction along the x=10 line.
func dockedLevel(t *testing.T) *world.Level {
	t.Helper()
	lvl := collisionTestLevel() // one chunk, sectors A (x 0..10) and B (x 10..20)
	base := lvl.Chunks[0]

	cabin := base
	cabin.ID = "cabin"
	cabin.FrameOwner = "ship"
	cabin.Sectors = []world.Sector{base.Sectors[0]} // A only

	deck := base
	deck.ID = "deck"
	deck.FrameOwner = ""
	deck.Sectors = []world.Sector{base.Sectors[1]} // B only

	// The shared edge becomes a frame portal on each side.
	linkPortal(&cabin.Sectors[0], "B", world.WorldFrameName)
	linkPortal(&deck.Sectors[0], "A", "ship")

	lvl.Chunks = []world.Chunk{cabin, deck}
	lvl.Entities = []world.Entity{{ID: "ship", Scale: mgl32.Vec3{1, 1, 1},
		Vehicle: &world.VehicleSpec{Mode: world.VehicleSpace, Interiors: []string{"cabin"},
			Drive: world.DefaultDriveSpec(world.VehicleSpace)}}}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatalf("RebuildIndexes: %v", err)
	}
	return lvl
}

// linkPortal turns the wall on the x=10 line into a portal to another frame.
func linkPortal(s *world.Sector, toSector, toFrame string) {
	var walls []world.Wall
	for _, w := range s.Walls {
		if w.StartPoint.X() == 10 && w.EndPoint.X() == 10 {
			s.Portals = append(s.Portals, world.Portal{
				ID: "link", StartPoint: w.StartPoint, EndPoint: w.EndPoint,
				ToSector: toSector, ToFrame: toFrame, Visible: true,
			})
			continue
		}
		walls = append(walls, w)
	}
	s.Walls = walls
	for i := range s.EdgeKinds {
		s.EdgeKinds[i] = "wall"
	}
}

func TestFramesAlignedFollowsTheUpVectors(t *testing.T) {
	lvl := dockedLevel(t)
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
	lvl := dockedLevel(t)
	ship := world.FindEntity(lvl, "ship")
	st := ship.EnsureVehicleState()
	// Docked somewhere well away from the origin and turned, but level.
	ship.Position = mgl32.Vec3{300, 25, -140}
	st.Orient = mgl32.QuatRotate(1.3, mgl32.Vec3{0, 1, 0})
	world.RefreshFrames(lvl, lvl.Frames)

	p := world.NewPlayer(mgl32.Vec3{9.8, 0.9, 5}) // in the cabin, at the ramp
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

	c := FindFramePortalCrossing(lvl, pl)
	if c == nil {
		t.Fatal("standing in the doorway found no crossing")
	}
	if c.Sealed {
		t.Fatal("a level ship's ramp was sealed")
	}
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

func TestAMisalignedPortalSealsAndStopsTheWalker(t *testing.T) {
	lvl := dockedLevel(t)
	ship := world.FindEntity(lvl, "ship")
	st := ship.EnsureVehicleState()
	// Rolled hard: the ramp now points at the sky.
	st.Orient = mgl32.QuatRotate(math.Pi/2, mgl32.Vec3{0, 0, 1})
	world.RefreshFrames(lvl, lvl.Frames)

	p := world.NewPlayer(mgl32.Vec3{5, 0.9, 5})
	p.Frame = "ship"
	lvl.Entities = append(lvl.Entities, *p)
	pl := world.FindEntity(lvl, "player")
	pl.UpdateLocation(lvl)

	if c := FindFramePortalCrossing(lvl, pl); c != nil && !c.Sealed {
		t.Error("a portal on a rolled ship reported itself open")
	}

	// Walk hard at the doorway. It has to hold.
	for i := 0; i < 200; i++ {
		MovePlayer(pl, lvl, walkMove(mgl32.Vec3{step, 0, 0}))
	}
	if pl.Frame != "ship" {
		t.Errorf("the walker got out through a sealed portal (frame now %q)", pl.Frame)
	}
	if pl.Position.X() > 10-pl.BoundHalfX+1e-2 {
		t.Errorf("the walker passed the sealed doorway to x=%v", pl.Position.X())
	}
}

func TestAnAlignedPortalDoesNotSeal(t *testing.T) {
	lvl := dockedLevel(t)
	ship := world.FindEntity(lvl, "ship")
	ship.EnsureVehicleState().Orient = mgl32.QuatRotate(0.7, mgl32.Vec3{0, 1, 0})
	world.RefreshFrames(lvl, lvl.Frames)

	p := world.NewPlayer(mgl32.Vec3{5, 0.9, 5})
	p.Frame = "ship"
	lvl.Entities = append(lvl.Entities, *p)
	pl := world.FindEntity(lvl, "player")
	pl.UpdateLocation(lvl)

	if segs := SealedPortalSegments(lvl, "ship", &lvl.Chunks[0].Sectors[0]); len(segs) != 0 {
		t.Errorf("a level, yawed ship sealed %d portals", len(segs))
	}
	// And walking at it hands the player over rather than stopping them.
	crossed := false
	for i := 0; i < 200 && !crossed; i++ {
		MovePlayer(pl, lvl, walkMove(mgl32.Vec3{step, 0, 0}))
		if c := FindFramePortalCrossing(lvl, pl); c != nil && !c.Sealed {
			CrossFramePortal(lvl, pl, c, 0)
			crossed = true
		}
	}
	if !crossed {
		t.Fatalf("never reached the doorway; ended at %v", pl.Position)
	}
	if pl.Frame != "" {
		t.Errorf("crossed but is still in frame %q", pl.Frame)
	}
}
