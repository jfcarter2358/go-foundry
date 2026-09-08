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

// overlappingDockedLevel slides the deck 0.6 toward the ship, so the two spaces
// overlap by exactly the crossing band. That is the authoring mistake the band's
// own doc comment warns about, drawn as small as it can be drawn: every point in
// the overlap is within the band of BOTH portal lines, so each side is willing
// to hand the walker to the other.
func overlappingDockedLevel(t *testing.T) *world.Level {
	t.Helper()
	lvl := dockedLevel(t)
	for i := range lvl.Chunks {
		if lvl.Chunks[i].ID != "deck" {
			continue
		}
		lvl.Chunks[i].Origin = mgl32.Vec3{-portalCrossingBand, 0, 0}
		lvl.Chunks[i].RecomputeBounds()
	}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatalf("RebuildIndexes: %v", err)
	}
	return lvl
}

// A doorway whose halves are too close together used to trade the walker across
// once per tick for as long as they stood there. Nothing about the movement
// looked wrong; what it did was rewrite CurrentChunk every frame, and that is
// where portal-visibility culling starts its search — so the ship's interior and
// the bay's were culled on alternate frames and the screen strobed between them.
//
// Standing in the overlap now holds instead: the crossing has to go deeper than
// the way back, and in the overlap it does not.
func TestASharedDoorwayDoesNotTradeTheWalkerBackAndForth(t *testing.T) {
	lvl := overlappingDockedLevel(t)

	// Dead centre of the overlap: 0.3 from the deck's line and, once carried
	// over, 0.3 from the ship's.
	p := world.NewPlayer(mgl32.Vec3{10 - portalCrossingBand/2, 0.9, 5})
	lvl.Entities = append(lvl.Entities, *p)
	pl := world.FindEntity(lvl, "player")
	pl.UpdateLocation(lvl)
	if pl.Frame != "" || pl.CurrentSector != "B" {
		t.Fatalf("the walker did not start on the deck: frame %q, sector %q", pl.Frame, pl.CurrentSector)
	}

	for i := 0; i < 8; i++ {
		c := FindFramePortalCrossing(lvl, pl)
		if c == nil {
			continue
		}
		CrossFramePortal(lvl, pl, c, 0)
		t.Fatalf("tick %d: handed from %q to %q while standing still", i, c.FromID, c.ToID)
	}
}

// …and holding is not refusing. One more step in and the ship's side is the
// deeper of the two, so the walker boards — once, and stays aboard.
func TestWalkingThroughASharedDoorwayStillBoards(t *testing.T) {
	lvl := overlappingDockedLevel(t)

	p := world.NewPlayer(mgl32.Vec3{10 - portalCrossingBand/2, 0.9, 5})
	lvl.Entities = append(lvl.Entities, *p)
	pl := world.FindEntity(lvl, "player")
	pl.UpdateLocation(lvl)

	crossings := 0
	for i := 0; i < 200; i++ {
		MovePlayer(pl, lvl, walkMove(mgl32.Vec3{-step, 0, 0})) // on into the cabin
		if c := FindFramePortalCrossing(lvl, pl); c != nil && !c.Sealed {
			CrossFramePortal(lvl, pl, c, 0)
			crossings++
		}
	}
	if crossings != 1 {
		t.Errorf("crossed the doorway %d times walking through it once", crossings)
	}
	if pl.Frame != "ship" {
		t.Errorf("ended in frame %q, want aboard the ship", pl.Frame)
	}
	if pl.CurrentSector != "A" {
		t.Errorf("ended in sector %q, want the cabin", pl.CurrentSector)
	}
}

// ---------- unpartnered doorways ----------

// landedShipLevel is a ship set down on open ground with NOTHING authored to
// meet it. The ground is plain sectors; the only edge in the level that names a
// frame is the ship's own doorway. This is the arrangement a dock cannot
// describe, because the level never knew where the ship would come to rest.
//
// The cabin is 10 long with its doorway at local x=10, and the ship is parked at
// x=-1 — so the cabin covers world x -1..9, its doorway line falls at world x=9,
// and the ground runs underneath it the whole way. Walking from x=9.5 toward the
// ship steps over that line and inside.
func landedShipLevel(t *testing.T) *world.Level {
	t.Helper()
	lvl := collisionTestLevel() // one chunk, sectors A (x 0..10) and B (x 10..20)

	ground := lvl.Chunks[0]
	ground.ID = "ground"

	cabin := lvl.Chunks[0]
	cabin.ID = "cabin"
	cabin.FrameOwner = "ship"
	cabin.Sectors = []world.Sector{lvl.Chunks[0].Sectors[0]} // A only
	cabin.Sectors[0].ID = "hold"                             // the ground has an "A" too; keep them apart
	linkPortal(&cabin.Sectors[0], "", world.WorldFrameName)

	lvl.Chunks = []world.Chunk{ground, cabin}
	lvl.Entities = []world.Entity{{
		ID: "ship", Position: mgl32.Vec3{-1, 0, 0}, Scale: mgl32.Vec3{1, 1, 1},
		Vehicle: &world.VehicleSpec{Mode: world.VehicleSpace, Interiors: []string{"cabin"},
			Drive: world.DefaultDriveSpec(world.VehicleSpace),
			Seats: []world.VehicleSeat{{Name: "driver", Position: mgl32.Vec3{40, 0, 5}}}},
	}}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatalf("RebuildIndexes: %v", err)
	}
	return lvl
}

func placeWalker(t *testing.T, lvl *world.Level, at mgl32.Vec3) *world.Entity {
	t.Helper()
	p := world.NewPlayer(at)
	lvl.Entities = append(lvl.Entities, *p)
	pl := world.FindEntity(lvl, "player")
	pl.UpdateLocation(lvl)
	return pl
}

// The whole point of the feature: a ship that nothing was authored to meet is
// still boardable, because standing inside its doorway sector IS being aboard.
func TestWalkingIntoAnUnpartneredDoorwayBoards(t *testing.T) {
	lvl := landedShipLevel(t)
	pl := placeWalker(t, lvl, mgl32.Vec3{9.5, 0.9, 5}) // on the ground, short of the lip

	if c := FindFramePortalCrossing(lvl, pl); c != nil {
		t.Fatalf("boarded from outside the doorway: %+v", c)
	}

	crossings := 0
	for i := 0; i < 200; i++ {
		MovePlayer(pl, lvl, walkMove(mgl32.Vec3{-step, 0, 0})) // on toward the ship
		if c := FindFramePortalCrossing(lvl, pl); c != nil && !c.Sealed {
			CrossFramePortal(lvl, pl, c, 0)
			crossings++
		}
	}
	if crossings != 1 {
		t.Errorf("crossed %d times walking in once", crossings)
	}
	if pl.Frame != "ship" {
		t.Errorf("ended in frame %q, want aboard the ship", pl.Frame)
	}
	if pl.CurrentSector != "hold" {
		t.Errorf("ended in sector %q, want the cabin", pl.CurrentSector)
	}
}

// And out again: off the end of every sector the frame owns is off the ship.
func TestWalkingOutOfAnUnpartneredDoorwayStepsOff(t *testing.T) {
	lvl := landedShipLevel(t)
	pl := placeWalker(t, lvl, mgl32.Vec3{8.5, 0.9, 5}) // inside the cabin's footprint

	c := FindFramePortalCrossing(lvl, pl)
	if c == nil {
		t.Fatal("standing in the doorway sector found no way aboard")
	}
	CrossFramePortal(lvl, pl, c, 0)
	if pl.Frame != "ship" {
		t.Fatalf("frame after boarding: %q", pl.Frame)
	}

	crossings := 0
	for i := 0; i < 200; i++ {
		MovePlayer(pl, lvl, walkMove(mgl32.Vec3{step, 0, 0})) // back out the way in
		if c := FindFramePortalCrossing(lvl, pl); c != nil && !c.Sealed {
			CrossFramePortal(lvl, pl, c, 0)
			crossings++
		}
	}
	if crossings != 1 {
		t.Errorf("crossed %d times walking out once", crossings)
	}
	if pl.Frame != "" {
		t.Errorf("ended in frame %q, want back on the ground", pl.Frame)
	}
	if pl.CurrentSector == "" {
		t.Error("stepped off into no sector at all")
	}
}

// Containment is antisymmetric — inside a sector and inside no sector cannot
// both be true — so unlike proximity it cannot trade an occupant back and
// forth. Standing still in the doorway has to settle after one crossing.
func TestAnUnpartneredDoorwayDoesNotStrobe(t *testing.T) {
	lvl := landedShipLevel(t)
	pl := placeWalker(t, lvl, mgl32.Vec3{8.9, 0.9, 5}) // just inside, well within the band

	crossings := 0
	for i := 0; i < 30; i++ {
		if c := FindFramePortalCrossing(lvl, pl); c != nil && !c.Sealed {
			CrossFramePortal(lvl, pl, c, 0)
			crossings++
		}
	}
	if crossings != 1 {
		t.Errorf("crossed %d times standing still; want exactly one, then settled", crossings)
	}
	if pl.Frame != "ship" {
		t.Errorf("settled in frame %q, want aboard", pl.Frame)
	}
}

// The reason the feature exists: the level authored nothing, so the ship may be
// anywhere and at any yaw and boarding still has to work.
func TestBoardingFollowsTheShipWhereverItParks(t *testing.T) {
	for _, tc := range []struct {
		name string
		at   mgl32.Vec3
		yaw  float32
	}{
		{"moved", mgl32.Vec3{3, 0, -7}, 0},
		{"yawed", mgl32.Vec3{-1, 0, 0}, 0.7},
		{"both", mgl32.Vec3{-6, 0, 4}, 2.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lvl := landedShipLevel(t)
			ship := world.FindEntity(lvl, "ship")
			ship.Position = tc.at
			ship.EnsureVehicleState().Orient = mgl32.QuatRotate(tc.yaw, mgl32.Vec3{0, 1, 0})
			world.RefreshFrames(lvl, lvl.Frames)

			// The middle of the cabin, carried out to wherever the ship now is.
			f := lvl.Frames.Frame("ship")
			pl := placeWalker(t, lvl, f.ToWorld(mgl32.Vec3{5, 0.9, 5}))

			c := FindFramePortalCrossing(lvl, pl)
			if c == nil {
				t.Fatal("standing in the cabin found no way aboard")
			}
			CrossFramePortal(lvl, pl, c, 0)
			if pl.Frame != "ship" {
				t.Errorf("frame after boarding: %q", pl.Frame)
			}
			if pl.CurrentSector != "hold" {
				t.Errorf("landed in sector %q, want the cabin", pl.CurrentSector)
			}
		})
	}
}

// A seat is a constraint, not a place. This one is authored 40 units away from
// any sector the ship owns — a cockpit out on a nose — so an occupant pinned to
// it is in no sector at all, which is exactly what steps a walker off the deck.
// It must not step a passenger out of their chair.
func TestASeatedOccupantIsNeverSteppedOff(t *testing.T) {
	lvl := landedShipLevel(t)
	pl := placeWalker(t, lvl, mgl32.Vec3{8.5, 0.9, 5})
	ship := world.FindEntity(lvl, "ship")

	if !Mount(lvl, pl, ship, "driver") {
		t.Fatal("mount failed")
	}
	pl.UpdateLocation(lvl) // Mount pins the occupant; the frame loop derives the rest
	if pl.CurrentSector != "" {
		t.Fatalf("the seat is inside sector %q; this test needs one outside the interior", pl.CurrentSector)
	}
	for i := 0; i < 30; i++ {
		if c := FindFramePortalCrossing(lvl, pl); c != nil && !c.Sealed {
			CrossFramePortal(lvl, pl, c, 0)
			t.Fatalf("tick %d: handed out of the seat to frame %q", i, pl.Frame)
		}
	}
	if pl.Frame != "ship" || pl.Seat != "driver" {
		t.Errorf("ended frame=%q seat=%q, want still strapped in", pl.Frame, pl.Seat)
	}
}

// A doorway pointing at the sky is not a way in. There is no wall on the outside
// to be pushed off — the ship is not solid — so it simply does not admit.
func TestAMisalignedShipAdmitsNobody(t *testing.T) {
	lvl := landedShipLevel(t)
	ship := world.FindEntity(lvl, "ship")
	ship.EnsureVehicleState().Orient = mgl32.QuatRotate(math.Pi/2, mgl32.Vec3{0, 0, 1})
	world.RefreshFrames(lvl, lvl.Frames)

	f := lvl.Frames.Frame("ship")
	pl := placeWalker(t, lvl, f.ToWorld(mgl32.Vec3{5, 0.9, 5}))

	if c := FindFramePortalCrossing(lvl, pl); c != nil && !c.Sealed {
		t.Errorf("a rolled ship handed the walker aboard: %+v", c)
	}
}

// A frame with no doorway at all is sealed, and keeps its contents however far
// they wander off its floors — otherwise every hold with a gap in the deck
// would quietly empty itself into the world.
func TestASealedFrameKeepsItsOccupants(t *testing.T) {
	lvl := landedShipLevel(t)
	// Take the doorway away: the cabin now names no frame at all.
	for i := range lvl.Chunks {
		if lvl.Chunks[i].ID == "cabin" {
			lvl.Chunks[i].Sectors[0].Portals = nil
		}
	}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatalf("RebuildIndexes: %v", err)
	}

	pl := placeWalker(t, lvl, mgl32.Vec3{8.5, 0.9, 5})
	pl.Frame = "ship"
	pl.Position = mgl32.Vec3{40, 0.9, 5} // off the end of every sector it owns
	pl.UpdateLocation(lvl)
	if pl.CurrentSector != "" {
		t.Fatalf("expected to be off the cabin's floors, but stood in %q", pl.CurrentSector)
	}

	if c := FindFramePortalCrossing(lvl, pl); c != nil {
		t.Errorf("a sealed frame let its occupant out: %+v", c)
	}
}
