package collision

// beachhead_walk_test.go — the shipped beachhead level's routes stay walkable,
// and its cliffs stay unclimbable.
//
// Loading the level (levelio's own test) proves it parses. Only walking it
// proves the parts that are geometry-by-derivation actually work: a gate the
// player can go through, doorways cut through two units of concrete, a ramp
// that arrives level with the roof it serves, and a walkway that joins the two
// blocks. Each of those is a height agreeing with a neighbour's height, and
// nothing in the file says so out loud.

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// beachheadLevel loads resources/levels/beachhead.yaml without going through
// package levelio, which imports this one.
//
// Unlike rawCanyonChunk it carries each edge's own Collision flag: the roof
// slabs deliberately set it false, so that walking off a roof is a fall rather
// than a bump into an invisible rail, and a loader that forced it true would
// test a level nobody plays.
func beachheadLevel(t *testing.T) *world.Level {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	if gfx.Textures == nil {
		gfx.Textures = map[string]uint32{}
	}
	for _, n := range []string{"", "default", "stone_floor", "concrete", "dirt",
		"rock", "grass", "sand", "water"} {
		gfx.Textures[n] = 1
	}
	path := filepath.Join(testenv.RepoRoot(t), "resources", "levels", "beachhead.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read beachhead.yaml: %v", err)
	}
	var raw world.RawLevel
	if err := yaml.Unmarshal(b, &raw); err != nil {
		t.Fatalf("parse beachhead.yaml: %v", err)
	}

	rc := raw.Chunks[0]
	ch := world.Chunk{ID: rc.ID, Scale: 1}
	idx := map[string]int{}
	names := make([]string, 0, len(rc.Points))
	for n := range rc.Points {
		names = append(names, n)
	}
	sortStrings(names)
	for _, n := range names {
		idx[n] = len(ch.Points)
		ch.Points = append(ch.Points, mgl32.Vec2{rc.Points[n].X, rc.Points[n].Y})
		ch.PointNames = append(ch.PointNames, n)
	}
	for _, rs := range rc.Sectors {
		s := world.Sector{
			ID:    rs.ID,
			Layer: rs.Layer,
			Floor: world.Floor{Enabled: rs.Floor.Enabled, Height: rs.Floor.Height,
				VertexOffsets: rs.Floor.VertexOffsets, DoubleSided: true, Alpha: 1},
			Ceiling: world.Ceiling{Enabled: rs.Ceiling.Enabled, Height: rs.Ceiling.Height,
				DoubleSided: true, Alpha: 1},
		}
		for _, e := range rs.Edges {
			s.PointIndices = append(s.PointIndices, idx[e.From])
			s.EdgeKinds = append(s.EdgeKinds, "wall")
			s.Walls = append(s.Walls, world.Wall{
				StartPoint: ch.Points[idx[e.From]], EndPoint: ch.Points[idx[e.To]],
				Collision: e.Collision, Visible: !e.Hidden, DoubleSided: true, Alpha: 1,
				Pinned: e.Pinned,
			})
		}
		ch.Sectors = append(ch.Sectors, s)
	}
	ch.InferPortals()
	for si := range ch.Sectors {
		_ = world.ReloadSector(&ch, &ch.Sectors[si])
	}
	ch.RecomputeBounds()

	lvl := &world.Level{Chunks: []world.Chunk{ch}}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatalf("indexes: %v", err)
	}
	return lvl
}

// The main route: up the beach, through the open gate under the walkway, and
// out into the hills.
func TestBeachheadWalkThroughTheGate(t *testing.T) {
	lvl := beachheadLevel(t)
	visited := walkRoute(t, lvl, mgl32.Vec3{0, 2, -88}, [][2]float32{
		{0, -60}, // up the beach
		{0, -42}, // the apron below the bunker line
		{0, -30}, // into the gate's mouth
		{0, -22}, // under the walkway
		{0, -10}, // out the far side
		{0, 8},   // into the hills
	})
	t.Logf("visited: %v", visited)
	if missing, ok := visitedAll(visited, "gate_s", "gate_m", "gate_n"); !ok {
		t.Errorf("the route never reached %q; visited %v", missing, visited)
	}
	last := visited[len(visited)-1]
	if last == "gate_n" {
		t.Errorf("the route stopped in the gate rather than reaching the hills: %v", visited)
	}
}

// Into the west block by its beach door, across it through the internal
// doorway, and out of the back into the hills. Every one of those openings is a
// short sector cut through two units of concrete, and each has to connect at
// both ends.
func TestBeachheadWalkThroughTheWestBunker(t *testing.T) {
	lvl := beachheadLevel(t)
	visited := walkRoute(t, lvl, mgl32.Vec3{-38, 7, -46}, [][2]float32{
		{-38, -41}, // up to the door in the south face
		{-38, -36}, // inside the west room
		{-31, -30}, // across to the internal doorway
		{-26, -30}, // through it into the east room
		{-20, -12}, // up to the rear door
		{-20, -4},  // out into the hills
	})
	t.Logf("visited: %v", visited)
	if missing, ok := visitedAll(visited, "w_door_s", "w_room_w", "w_door_mid", "w_room_e", "w_door_n"); !ok {
		t.Errorf("the route never reached %q; visited %v", missing, visited)
	}
}

// Up the ramp cut into the block's north face, onto the roof, and across the
// walkway to the other block. The ramp's top has to arrive exactly level with
// the roof or the last step is one nothing can climb.
func TestBeachheadWalkUpTheRampAndAcrossTheWalkway(t *testing.T) {
	lvl := beachheadLevel(t)
	visited := walkRoute(t, lvl, mgl32.Vec3{-37, 7, -2}, [][2]float32{
		{-37, -10}, // into the ramp trench
		{-37, -19}, // up it, arriving at roof height
		{-37, -26}, // onto the roof over the west room
		{-14, -22}, // east across the roof
		{0, -22},   // out onto the walkway over the gate
		{14, -22},  // and onto the east block's roof
	})
	t.Logf("visited: %v", visited)
	if missing, ok := visitedAll(visited, "w_ramp", "walkway"); !ok {
		t.Errorf("the route never reached %q; visited %v", missing, visited)
	}

	ch := &lvl.Chunks[0]
	p := world.NewPlayer(ch.LocalToWorld(mgl32.Vec3{0, 12, -22}))
	if !settle(p, lvl, 400) {
		t.Fatalf("nothing to land on over the gate; y=%v", ch.WorldToLocal(p.Position).Y())
	}
	if got := ch.WorldToLocal(p.Position).Y(); got < 10 {
		t.Errorf("the walkway rests at y=%.2f; it should be on the deck at 10", got)
	}
}

// The cliffs are ground, not walls: they hold the player in because they are
// steeper than ResolveSlopeCollision will walk up. If the blend that makes them
// steep is ever softened, this is what notices.
func TestBeachheadCliffsCannotBeClimbed(t *testing.T) {
	lvl := beachheadLevel(t)
	ch := &lvl.Chunks[0]

	for _, tc := range []struct {
		name  string
		start mgl32.Vec3
		dir   mgl32.Vec3
		limit func(x float32) bool
	}{
		{"west", mgl32.Vec3{-42, 6, -60}, mgl32.Vec3{-0.18, 0, 0}, func(x float32) bool { return x > -53 }},
		{"east", mgl32.Vec3{42, 6, -60}, mgl32.Vec3{0.18, 0, 0}, func(x float32) bool { return x < 53 }},
	} {
		p := world.NewPlayer(ch.LocalToWorld(tc.start))
		settle(p, lvl, 300)
		for i := 0; i < 800; i++ {
			MovePlayer(p, lvl, walkMove(tc.dir))
		}
		l := ch.WorldToLocal(p.Position)
		if !tc.limit(l.X()) {
			t.Errorf("%s cliff: walked to x=%.2f — the face is climbable", tc.name, l.X())
		}
		if l.Y() > 14 {
			t.Errorf("%s cliff: climbed to y=%.2f", tc.name, l.Y())
		}
	}
}

// The map's rim is a hidden wall, and walking into an invisible barrier across
// open grass reads as a bug. The ridge in front of it is what the player
// actually meets, and it only works while it stays steeper than
// ResolveSlopeCollision admits — which a coarser cell there would quietly undo
// by averaging the rise over a wider ramp.
func TestBeachheadRimRidgeTurnsThePlayerBack(t *testing.T) {
	lvl := beachheadLevel(t)
	ch := &lvl.Chunks[0]

	for _, tc := range []struct {
		name  string
		start mgl32.Vec3
		dir   mgl32.Vec3
		stop  func(l mgl32.Vec3) bool
	}{
		{"north", mgl32.Vec3{0, 8, 60}, mgl32.Vec3{0, 0, 0.18},
			func(l mgl32.Vec3) bool { return l.Z() < 96 }},
		{"east", mgl32.Vec3{80, 10, 50}, mgl32.Vec3{0.18, 0, 0},
			func(l mgl32.Vec3) bool { return l.X() < 106 }},
	} {
		p := world.NewPlayer(ch.LocalToWorld(tc.start))
		settle(p, lvl, 400)
		for i := 0; i < 900; i++ {
			MovePlayer(p, lvl, walkMove(tc.dir))
		}
		if l := ch.WorldToLocal(p.Position); !tc.stop(l) {
			t.Errorf("%s rim: walked to (%.1f, %.1f, %.1f) — the ridge is climbable",
				tc.name, l.X(), l.Y(), l.Z())
		}
	}
}
