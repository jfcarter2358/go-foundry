package nav

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// A player-sized body with the engine's default step.
var playerParams = Params{Radius: 0.3, Height: 1.8, StepHeight: 0.5}

func roomLevel(extra ...world.Brush) *world.Level {
	ch := world.Chunk{ID: "main"}
	ch.Brushes = world.NewHollowRoom("room", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{10, 4, 10}, 0.5, "concrete")
	ch.Brushes = append(ch.Brushes, extra...)
	return &world.Level{Chunks: []world.Chunk{ch}}
}

func gridFor(t *testing.T, lvl *world.Level, p Params) *Grid {
	t.Helper()
	Invalidate()
	t.Cleanup(Invalidate)
	g := For(lvl, p)
	if g == nil || len(g.Nodes) == 0 {
		t.Fatal("the level built no navigation at all")
	}
	return g
}

// pathWalkable asserts every leg of a path stays on the grid, which is the
// promise string-pulling makes.
func pathWalkable(t *testing.T, g *Grid, from mgl32.Vec3, p Path) {
	t.Helper()
	prev, ok := g.Locate(from, 2)
	if !ok {
		t.Fatalf("the path's start %v is not on the grid", from)
	}
	for i, pt := range p.Points {
		n, ok := g.Locate(pt, 0.1)
		if !ok {
			t.Fatalf("corner %d at %v is off the grid", i, pt)
		}
		if !g.WalkableLine(prev, n) {
			t.Fatalf("leg %d, %v to %v, leaves the grid", i, g.NodePos(prev), pt)
		}
		prev = n
	}
}

func TestARoomsFloorIsWalkableAndItsWallsAreNot(t *testing.T) {
	g := gridFor(t, roomLevel(), playerParams)

	if _, ok := g.Locate(mgl32.Vec3{5, 0, 5}, 0.01); !ok {
		t.Fatal("the middle of the room's floor is not walkable")
	}
	n, _ := g.Locate(mgl32.Vec3{5, 0, 5}, 0.01)
	if f := g.Nodes[n].Floor; f < -0.01 || f > 0.01 {
		t.Errorf("the floor was found at y=%v, want 0", f)
	}
	// The top of a wall is flat and has open sky over it... except that this
	// room has a ceiling slab over the walls, so there is no headroom there.
	for _, nd := range g.Nodes {
		if nd.Floor > 0.1 && nd.Floor < 4.4 {
			x, z := g.HF.ColumnCenter(int(nd.Col))
			t.Errorf("a node stands at %v,%v,%v, inside the room's shell", x, nd.Floor, z)
			break
		}
	}
	// Erosion keeps a radius clear of the walls.
	for _, nd := range g.Nodes {
		if nd.Floor > 0.1 {
			continue
		}
		x, z := g.HF.ColumnCenter(int(nd.Col))
		if x < 0.3-0.01 || x > 9.7+0.01 || z < 0.3-0.01 || z > 9.7+0.01 {
			t.Fatalf("a floor node at %v,%v is closer to a wall than the body is wide", x, z)
		}
	}
}

func TestAnOpenRoomIsCrossedInOneStraightLine(t *testing.T) {
	g := gridFor(t, roomLevel(), playerParams)
	from, to := mgl32.Vec3{1, 0, 1}, mgl32.Vec3{9, 0, 8}
	p, ok := g.FindPath(from, to)
	if !ok || p.Partial {
		t.Fatalf("no full path across an empty room: ok=%v partial=%v", ok, p.Partial)
	}
	if len(p.Points) != 1 {
		t.Errorf("crossing an empty room took %d corners (%v), want the goal alone", len(p.Points), p.Points)
	}
	if end := p.Points[len(p.Points)-1]; end.Sub(to).Len() > 0.01 {
		t.Errorf("the path ends at %v, want the goal %v", end, to)
	}
}

func TestAPathGoesAroundAPillar(t *testing.T) {
	pillar := world.NewBoxBrush("pillar", mgl32.Vec3{5, 2, 5}, mgl32.Vec3{2, 2, 2}, "concrete")
	g := gridFor(t, roomLevel(pillar), playerParams)
	from, to := mgl32.Vec3{5, 0, 1}, mgl32.Vec3{5, 0, 9}

	a, _ := g.Locate(from, 0.1)
	b, _ := g.Locate(to, 0.1)
	if g.WalkableLine(a, b) {
		t.Fatal("a straight line through the pillar was called walkable")
	}
	p, ok := g.FindPath(from, to)
	if !ok || p.Partial {
		t.Fatalf("no way round the pillar: ok=%v partial=%v", ok, p.Partial)
	}
	if len(p.Points) < 2 {
		t.Fatalf("the path around the pillar has %d corners, want it to turn: %v", len(p.Points), p.Points)
	}
	pathWalkable(t, g, from, p)
	for _, pt := range p.Points {
		if pt.X() > 2.7 && pt.X() < 7.3 && pt.Z() > 2.7 && pt.Z() < 7.3 {
			t.Errorf("corner %v is inside the pillar's footprint", pt)
		}
	}
}

func TestSeparateRoomsAreUnreachableButStillAnswered(t *testing.T) {
	ch := world.Chunk{ID: "rooms"}
	ch.Brushes = append(ch.Brushes, world.NewHollowRoom("A", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{10, 4, 10}, 0.5, "c")...)
	ch.Brushes = append(ch.Brushes, world.NewHollowRoom("B", mgl32.Vec3{12, 0, 0}, mgl32.Vec3{22, 4, 10}, 0.5, "c")...)
	g := gridFor(t, &world.Level{Chunks: []world.Chunk{ch}}, playerParams)

	from, to := mgl32.Vec3{5, 0, 5}, mgl32.Vec3{17, 0, 5}
	if g.Reachable(from, to) {
		t.Fatal("two sealed rooms were called reachable from each other")
	}
	p, ok := g.FindPath(from, to)
	if !ok || !p.Partial {
		t.Fatalf("an unreachable goal gave ok=%v partial=%v, want a partial path", ok, p.Partial)
	}
	end := p.Points[len(p.Points)-1]
	if end.X() < 9 || end.X() > 10 {
		t.Errorf("the partial path ends at %v, want the wall nearest the goal", end)
	}
}

// doorLevel is two rooms joined by a doorway of the given width in the wall
// between them at x=10.
func doorLevel(width float32) *world.Level {
	ch := world.Chunk{ID: "door"}
	floor := world.NewBoxBrush("floor", mgl32.Vec3{10, -0.25, 5}, mgl32.Vec3{10.5, 0.25, 5.5}, "c")
	ch.Brushes = append(ch.Brushes, floor)
	for _, w := range []world.Brush{
		world.NewBoxBrush("w_negx", mgl32.Vec3{-0.25, 2, 5}, mgl32.Vec3{0.25, 2, 5}, "c"),
		world.NewBoxBrush("w_posx", mgl32.Vec3{20.25, 2, 5}, mgl32.Vec3{0.25, 2, 5}, "c"),
		world.NewBoxBrush("w_negz", mgl32.Vec3{10, 2, -0.25}, mgl32.Vec3{10, 2, 0.25}, "c"),
		world.NewBoxBrush("w_posz", mgl32.Vec3{10, 2, 10.25}, mgl32.Vec3{10, 2, 0.25}, "c"),
	} {
		ch.Brushes = append(ch.Brushes, w)
	}
	// The dividing wall, in two halves either side of a gap centred on z=5.
	lo, hi := 5-width/2, 5+width/2
	ch.Brushes = append(ch.Brushes,
		world.NewBoxBrush("div_a", mgl32.Vec3{10, 2, lo / 2}, mgl32.Vec3{0.25, 2, lo / 2}, "c"),
		world.NewBoxBrush("div_b", mgl32.Vec3{10, 2, (hi + 10) / 2}, mgl32.Vec3{0.25, 2, (10 - hi) / 2}, "c"),
	)
	return &world.Level{Chunks: []world.Chunk{ch}}
}

func TestADoorwayWideEnoughForTheBodyIsPassable(t *testing.T) {
	g := gridFor(t, doorLevel(1.0), playerParams)
	from, to := mgl32.Vec3{3, 0, 2}, mgl32.Vec3{17, 0, 8}
	p, ok := g.FindPath(from, to)
	if !ok || p.Partial {
		t.Fatalf("a one-metre doorway did not let a 0.6-wide body through: ok=%v partial=%v", ok, p.Partial)
	}
	pathWalkable(t, g, from, p)
}

func TestADoorwayNarrowerThanTheBodyIsNot(t *testing.T) {
	// 0.6 is the body's whole width, and there is no room either side of it.
	g := gridFor(t, doorLevel(0.6), playerParams)
	if g.Reachable(mgl32.Vec3{3, 0, 2}, mgl32.Vec3{17, 0, 8}) {
		t.Fatal("a 0.6-wide gap was called passable for a body 0.6 across")
	}
	// A small body fits where the big one did not.
	small := gridFor(t, doorLevel(0.6), Params{Radius: 0.05, Height: 0.5, StepHeight: 0.2})
	if !small.Reachable(mgl32.Vec3{3, 0, 2}, mgl32.Vec3{17, 0, 8}) {
		t.Error("a body a tenth of a metre across could not pass a 0.6 gap")
	}
}

func TestAStepIsClimbedOnlyWithinTheStepHeight(t *testing.T) {
	// A raised platform filling the far half of the room, 0.4 up.
	platform := world.NewBoxBrush("platform", mgl32.Vec3{7.5, 0.2, 5}, mgl32.Vec3{2.5, 0.2, 5}, "c")
	lvl := roomLevel(platform)
	from, to := mgl32.Vec3{2, 0, 5}, mgl32.Vec3{8, 0.4, 5}

	if !gridFor(t, lvl, playerParams).Reachable(from, to) {
		t.Error("a 0.4 riser was not climbable with a 0.5 step")
	}
	low := playerParams
	low.StepHeight = 0.2
	if gridFor(t, lvl, low).Reachable(from, to) {
		t.Error("a 0.4 riser was climbable with a 0.2 step")
	}
}

func TestATunnelLowerThanTheBodyIsNotWalkable(t *testing.T) {
	// A slab hanging 1.2 above the floor across the whole middle of the room.
	lintel := world.NewBoxBrush("lintel", mgl32.Vec3{5, 2.6, 5}, mgl32.Vec3{1, 1.4, 5}, "c")
	lvl := roomLevel(lintel)
	from, to := mgl32.Vec3{2, 0, 5}, mgl32.Vec3{8, 0, 5}
	if gridFor(t, lvl, playerParams).Reachable(from, to) {
		t.Error("a 1.2-high gap was passable for a body 1.8 tall")
	}
	short := playerParams
	short.Height = 1.0
	if !gridFor(t, lvl, short).Reachable(from, to) {
		t.Error("a body 1.0 tall could not pass under a 1.2-high slab")
	}
}

// wedge is a ramp from x=4 up to x=4+run, rising rise, across z 3..7.
func wedge(run, rise float32) world.Brush {
	x1 := 4 + run
	return world.Brush{ID: "ramp", Solid: true, BrushMesh: world.BrushMesh{
		Verts: []mgl32.Vec3{
			{4, 0, 3}, {x1, 0, 3}, {x1, rise, 3},
			{4, 0, 7}, {x1, 0, 7}, {x1, rise, 7},
		},
		Faces: []world.BrushFace{
			world.NewBrushFace([]int{0, 2, 1}, "c"),    // front
			world.NewBrushFace([]int{3, 4, 5}, "c"),    // back
			world.NewBrushFace([]int{0, 1, 4, 3}, "c"), // bottom
			world.NewBrushFace([]int{1, 2, 5, 4}, "c"), // upright
			world.NewBrushFace([]int{0, 3, 5, 2}, "c"), // slope
		},
	}}
}

// A short body, so the ramp's headroom under the ceiling is never why a node
// is missing.
var shortParams = Params{Radius: 0.3, Height: 0.5, StepHeight: 0.3}

func TestAFloorTooSteepIsNotStoodOn(t *testing.T) {
	// A step tall enough to climb each column of the slope, so a node missing
	// from it is missing because the surface is too steep and not because the
	// rise between columns is.
	p := shortParams
	p.StepHeight = 1
	g := gridFor(t, roomLevel(wedge(2, 3)), p) // about 56 degrees
	for _, nd := range g.Nodes {
		// The room's roof, above 4, is open sky and a floor like any other.
		if nd.Floor > 0.5 && nd.Floor < 4 {
			x, z := g.HF.ColumnCenter(int(nd.Col))
			t.Fatalf("a node stands on the 56-degree slope at %v,%v,%v", x, nd.Floor, z)
		}
	}
}

func TestAGentleRampIsWalkedUp(t *testing.T) {
	g := gridFor(t, roomLevel(wedge(4, 1.2)), shortParams) // about 17 degrees
	top := mgl32.Vec3{7.5, 1.05, 5}
	n, ok := g.Locate(top, 0.1)
	if !ok || g.Nodes[n].Floor < 0.9 {
		t.Fatalf("no node on the upper ramp near %v", top)
	}
	if !g.Reachable(mgl32.Vec3{2, 0, 5}, top) {
		t.Error("the top of a 17-degree ramp is not reachable from the floor")
	}
}

func TestSyncRebuildsWhenTheShapeChanges(t *testing.T) {
	pillar := world.NewBoxBrush("pillar", mgl32.Vec3{5, 2, 5}, mgl32.Vec3{2, 2, 2}, "concrete")
	lvl := roomLevel(pillar)
	g := gridFor(t, lvl, playerParams)
	if _, ok := g.Locate(mgl32.Vec3{5, 0, 5}, 0.01); ok {
		t.Fatal("the pillar's footprint is walkable before it moves — the test proves nothing")
	}

	Sync(lvl)
	if For(lvl, playerParams) != g {
		t.Fatal("an unchanged level rebuilt its grid")
	}

	b := &lvl.Chunks[0].Brushes[len(lvl.Chunks[0].Brushes)-1]
	for i := range b.Verts {
		b.Verts[i] = b.Verts[i].Add(mgl32.Vec3{0, 10, 0}) // lift it clear out of the room
	}
	Sync(lvl)
	g2 := For(lvl, playerParams)
	if g2 == g {
		t.Fatal("moving a brush left the old grid in place")
	}
	if _, ok := g2.Locate(mgl32.Vec3{5, 0, 5}, 0.01); !ok {
		t.Error("where the pillar stood is still unwalkable after it was moved away")
	}
}

func TestDynamicBrushesAndDisabledChunksAreNotBaked(t *testing.T) {
	door := world.NewBoxBrush("pillar", mgl32.Vec3{5, 2, 5}, mgl32.Vec3{2, 2, 2}, "concrete")
	door.Dynamic = true
	lvl := roomLevel(door)
	if _, ok := gridFor(t, lvl, playerParams).Locate(mgl32.Vec3{5, 0, 5}, 0.01); !ok {
		t.Error("a dynamic brush was baked into the grid as a wall")
	}

	lvl.Chunks[0].Disabled = true
	Invalidate()
	if g := For(lvl, playerParams); g != nil && len(g.Nodes) > 0 {
		t.Error("a disabled chunk still has navigation")
	}
}

func TestRandomPointStaysInsideTheBoxAndTheRegion(t *testing.T) {
	g := gridFor(t, roomLevel(), playerParams)
	lo, hi := mgl32.Vec3{2, -1, 2}, mgl32.Vec3{4, 1, 4}
	p, ok := g.RandomPoint(lo, hi, g.RegionAt(mgl32.Vec3{5, 0, 5}), nil, nil)
	if !ok {
		t.Fatal("no point found in a box on an open floor")
	}
	if p.X() < 2-g.HF.Cell || p.X() > 4+g.HF.Cell || p.Z() < 2-g.HF.Cell || p.Z() > 4+g.HF.Cell {
		t.Errorf("the random point %v is outside the box", p)
	}
}
