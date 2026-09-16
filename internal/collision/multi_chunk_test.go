package collision

// multi_chunk_test.go — a body is solid against every world chunk it touches.
//
// Collision used to find the one chunk a body's centre was inside and resolve
// against that chunk's brushes alone. That holds for a level of closed rooms
// and fails the moment a level is terrain split into streamed pieces: a body on
// the seam ignores the neighbour's floor and walls, and a body standing on a
// chunk that is only a floor is above that chunk's bounds and inside no chunk
// at all. These walk bodies across exactly those places.

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// slabChunk is a chunk holding one ground slab over x in [x0, x1], z in
// [-10, 10], with its top at topY — a floor and nothing else, so a body
// standing on it is above the chunk's bounds.
func slabChunk(id string, x0, x1, topY float32, extra ...world.Brush) world.Chunk {
	cx, hx := (x0+x1)/2, (x1-x0)/2
	ch := world.Chunk{ID: id, Scale: 1, Brushes: append([]world.Brush{
		world.NewBoxBrush(id+"_ground", mgl32.Vec3{cx, topY - 0.5, 0}, mgl32.Vec3{hx, 0.5, 10}, "concrete"),
	}, extra...)}
	ch.RecomputeBounds()
	return ch
}

func seamLevel(t *testing.T, chunks ...world.Chunk) *world.Level {
	t.Helper()
	lvl := &world.Level{Chunks: chunks}
	lvl.Player.StepHeight = 0.5
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	return lvl
}

func TestABodyStandsOnAChunkThatIsOnlyAFloor(t *testing.T) {
	lvl := seamLevel(t, slabChunk("ground", -10, 10, 0))
	p := newTestPlayer(mgl32.Vec3{0, 0.95, 0})
	if !settle(p, lvl, 200) {
		t.Fatalf("never stood on the slab; fell to y=%v", p.Position.Y())
	}
	if got := feet(p); got < -0.01 || got > 0.02 {
		t.Errorf("feet at %v, want on the slab's top at 0", got)
	}
}

func TestWalkingAcrossASeamOntoAHigherChunkDoesNotFall(t *testing.T) {
	// West ground at y=0, east ground a curb higher. Neither chunk's bounds
	// contain a standing body, and at the seam its centre is in neither.
	lvl := seamLevel(t, slabChunk("west", -10, 0, 0), slabChunk("east", 0, 10, 0.3))
	p := newTestPlayer(mgl32.Vec3{-4, 0.95, 0})
	settle(p, lvl, 100)

	lowest := feet(p)
	for i := 0; i < 120; i++ {
		MovePlayer(p, lvl, walkMove(mgl32.Vec3{0.07, 0, 0}))
		if f := feet(p); f < lowest {
			lowest = f
		}
	}
	settle(p, lvl, 60)
	if lowest < -0.05 {
		t.Errorf("the body dropped to feet=%v crossing the seam; neither floor is below 0", lowest)
	}
	if p.Position.X() < 2 {
		t.Fatalf("the body stopped at x=%v and never crossed the seam", p.Position.X())
	}
	if got := feet(p); got < 0.28 || got > 0.33 {
		t.Errorf("feet at %v on the east side, want on its floor at 0.3", got)
	}

	// And back down again.
	for i := 0; i < 120; i++ {
		MovePlayer(p, lvl, walkMove(mgl32.Vec3{-0.07, 0, 0}))
		if f := feet(p); f < -0.05 {
			t.Fatalf("fell to feet=%v stepping back down across the seam", f)
		}
	}
}

func TestAWallInTheNeighbouringChunkStopsTheBody(t *testing.T) {
	// The wall stands in the east chunk, starting just past the seam: a body
	// whose centre is still in the west chunk reaches it first.
	wall := world.NewBoxBrush("wall", mgl32.Vec3{0.6, 1.5, 0}, mgl32.Vec3{0.4, 1.5, 10}, "concrete")
	lvl := seamLevel(t, slabChunk("west", -10, 0, 0), slabChunk("east", 0, 10, 0, wall))
	p := newTestPlayer(mgl32.Vec3{-3, 0.95, 0})
	settle(p, lvl, 100)

	face := float32(0.2) // the wall's west face
	for i := 0; i < 150; i++ {
		MovePlayer(p, lvl, walkMove(mgl32.Vec3{0.06, 0, 0}))
		if reach := p.Position.X() + p.BoundHalfX; reach > face+0.02 {
			t.Fatalf("frame %d: the body reaches x=%v, into a wall whose face is at %v", i, reach, face)
		}
	}
	if p.Position.X()+p.BoundHalfX < face-0.05 {
		t.Errorf("the body stopped at x=%v, short of the wall", p.Position.X())
	}
}

func TestChunksOverlappingFindsEveryChunkTheBoxTouches(t *testing.T) {
	lvl := seamLevel(t, slabChunk("west", -10, 0, 0), slabChunk("east", 0, 10, 0.3), slabChunk("far", 40, 50, 0))
	lvl.Chunks = append(lvl.Chunks, slabChunk("off", -10, 10, 0))
	lvl.Chunks[3].Disabled = true
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}

	got := world.ChunksOverlapping(lvl, mgl32.Vec3{-0.4, 0, -0.4}, mgl32.Vec3{0.4, 1.8, 0.4}, nil)
	if len(got) != 2 || got[0].ID != "west" || got[1].ID != "east" {
		ids := []string{}
		for _, c := range got {
			ids = append(ids, c.ID)
		}
		t.Errorf("a box on the seam found %v, want [west east] in level order and nothing disabled", ids)
	}
	if got := world.ChunksOverlapping(lvl, mgl32.Vec3{20, 0, 0}, mgl32.Vec3{21, 2, 1}, nil); len(got) != 0 {
		t.Errorf("a box over open ground found %d chunks", len(got))
	}
}

func TestAParticleLandsOnTheFloorOfTheChunkItIsOver(t *testing.T) {
	// Starting over the west chunk and landing on the east one's higher floor.
	lvl := seamLevel(t, slabChunk("west", -10, 0, 0), slabChunk("east", 0, 10, 0.3))
	pos, n, hit := ParticleCollide(lvl, mgl32.Vec3{-0.1, 1, 0}, mgl32.Vec3{0.2, 0.1, 0}, 0.05)
	if !hit {
		t.Fatal("the particle fell through the east chunk's floor")
	}
	if pos.Y() < 0.34 || pos.Y() > 0.36 {
		t.Errorf("landed at y=%v, want resting on 0.3", pos.Y())
	}
	if n.Y() < 0.99 {
		t.Errorf("normal %v, want up", n)
	}
}

func TestAnEntityWalksAcrossASeamWithoutFalling(t *testing.T) {
	// Entities — AI agents, grenades, vehicles — go through resolveEntityWorld
	// rather than MovePlayer, and it looked chunks up the same single way.
	lvl := seamLevel(t, slabChunk("west", -10, 0, 0), slabChunk("east", 0, 10, 0))
	lvl.Entities = []world.Entity{{
		ID: "crate", Position: mgl32.Vec3{-3, 0.5, 0}, Scale: mgl32.Vec3{1, 1, 1},
		BoundHalfX: 0.4, BoundHalfY: 0.4, BoundHalfZ: 0.4, BoundOffset: mgl32.Vec3{0, 0.4, 0},
		Collision: world.EntityCollision{World: true, Gravity: true, Movable: true},
	}}
	e := &lvl.Entities[0]
	for i := 0; i < 60; i++ {
		EntityUpdate(lvl, 1.0/60, true, nil)
	}
	for i := 0; i < 240; i++ {
		e.Velocity[0] = 1.5
		EntityUpdate(lvl, 1.0/60, true, nil)
		if e.Position.Y() < -0.05 {
			t.Fatalf("frame %d: the entity fell to y=%v at x=%v", i, e.Position.Y(), e.Position.X())
		}
	}
	if e.Position.X() < 2 {
		t.Errorf("the entity stopped at x=%v and never crossed the seam", e.Position.X())
	}
}
