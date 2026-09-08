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

// SplitChunk builds two square sectors sharing one edge:
//
//	3---------2=====5
//	|    A    |  B  |
//	0---------1=====4
//
// A walks 0→1→2→3, so its edge 1 is (1→2). B walks 1→4→5→2, so its edge 3 is
// (2→1) — the same segment in the opposite winding, which is how InferPortals
// recognises a shared edge.
func SplitChunk() *world.Chunk {
	ch := &world.Chunk{
		Points: []mgl32.Vec2{
			{0, 0}, {10, 0}, {10, 10}, {0, 10}, {20, 0}, {20, 10},
		},
	}
	ch.PointNames = make([]string, len(ch.Points))

	mkWalls := func(indices []int) []world.Wall {
		var walls []world.Wall
		for i := range indices {
			walls = append(walls, world.Wall{
				StartPoint: ch.Points[indices[i]],
				EndPoint:   ch.Points[indices[(i+1)%len(indices)]],
				Texture:    "concrete",
				Collision:  true,
				Visible:    true,
				Alpha:      1,
			})
		}
		return walls
	}

	idxA := []int{0, 1, 2, 3}
	idxB := []int{1, 4, 5, 2}
	ch.Sectors = []world.Sector{
		{
			ID:           "A",
			PointIndices: idxA,
			EdgeKinds:    []string{"wall", "wall", "wall", "wall"},
			Walls:        mkWalls(idxA),
			Floor:        world.Floor{Enabled: true, VertexOffsets: []float32{0, 0, 0, 0}},
			Ceiling:      world.Ceiling{Enabled: true, Height: 4, VertexOffsets: []float32{0, 0, 0, 0}},
		},
		{
			ID:           "B",
			PointIndices: idxB,
			EdgeKinds:    []string{"wall", "wall", "wall", "wall"},
			Walls:        mkWalls(idxB),
			Floor:        world.Floor{Enabled: true, VertexOffsets: []float32{0, 0, 0, 0}},
			Ceiling:      world.Ceiling{Enabled: true, Height: 4, VertexOffsets: []float32{0, 0, 0, 0}},
		},
	}
	return ch
}

// EdgeIDChunk builds a single square sector with named edges, GL-free.
func EdgeIDChunk(t *testing.T) *world.Chunk {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	ch := &world.Chunk{
		Points:     []mgl32.Vec2{{0, 0}, {10, 0}, {10, 10}, {0, 10}},
		PointNames: []string{"P0", "P1", "P2", "P3"},
	}
	idx := []int{0, 1, 2, 3}
	var walls []world.Wall
	for i := range idx {
		walls = append(walls, world.Wall{
			StartPoint: ch.Points[idx[i]],
			EndPoint:   ch.Points[idx[(i+1)%len(idx)]],
			Texture:    "concrete",
			Collision:  true,
			Visible:    true,
			Alpha:      1,
		})
	}
	s := world.Sector{
		ID:           "room",
		PointIndices: idx,
		EdgeKinds:    []string{"wall", "wall", "wall", "wall"},
		Walls:        walls,
		Floor:        world.Floor{Enabled: true, VertexOffsets: []float32{0, 0, 0, 0}},
		Ceiling:      world.Ceiling{Enabled: true, Height: 4, VertexOffsets: []float32{0, 0, 0, 0}},
	}
	world.EnsureEdgeIDs(&s)
	world.SyncEdgeRecordIDs(&s)
	ch.Sectors = []world.Sector{s}
	return ch
}

// LoadGeometry builds the CPU mesh for every sector in ch — the walls, floors
// and ceilings a triangle test needs.
//
// Sector.Load is what a level load calls, and Geometry.Load's upload half is
// gated on gfx.GeometryUploadsEnabled, so this leaves real vertices behind with
// no GL context. Anything testing against what is *drawn* rather than against
// the sector's numbers has to come through here first.
func LoadGeometry(t *testing.T, ch *world.Chunk) {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		if err := s.Load(world.SectorPoints(ch, s)); err != nil {
			t.Fatalf("loading sector %q: %v", s.ID, err)
		}
	}
}
