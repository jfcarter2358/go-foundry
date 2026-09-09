package world

// static_test.go — which compiled surfaces a point draws.
//
// The tree here is built by hand rather than by the compiler, because what is
// being checked is the LOOKUP and its two fallbacks, not the partition. A hand
// tree is one whose right answer can be read off the fixture.

import (
	"testing"

	"foundry/internal/bsp"

	"github.com/go-gl/mathgl/mgl32"
)

// twoCellTree splits the world at x=0. Cell 0 (front, x>0) bounds surfaces 0
// and 1; cell 1 (back) bounds surface 2. Both are interior open space.
func twoCellTree() *StaticGeometry {
	t := &bsp.Tree{
		Nodes: []bsp.Node{{
			Plane: bsp.Plane{N: mgl32.Vec3{1, 0, 0}, D: 0},
			Front: bsp.LeafRef(0),
			Back:  bsp.LeafRef(1),
		}},
		Leaves: []bsp.Leaf{
			{Faces: []int{0, 1}},
			{Faces: []int{2}},
		},
		Root: bsp.NodeRef(0),
		Lo:   mgl32.Vec3{-10, -10, -10},
		Hi:   mgl32.Vec3{10, 10, 10},
	}
	v := bsp.NewPVS(2)
	v.Set(0, 0)
	v.Set(1, 1)
	return &StaticGeometry{Surfaces: make([]StaticSurface, 3), Tree: t, Vis: v}
}

func TestOnlyTheVisibleCellsSurfacesAreDrawn(t *testing.T) {
	s := twoCellTree()
	got := s.VisibleSurfaces(mgl32.Vec3{1, 0, 0}, nil)
	if len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Errorf("from the front cell: got %v, want its own two surfaces", got)
	}
	if got := s.VisibleSurfaces(mgl32.Vec3{-1, 0, 0}, nil); len(got) != 1 || got[0] != 2 {
		t.Errorf("from the back cell: got %v, want [2]", got)
	}
}

// A surface bounds more than one cell whenever a splitting plane cut the space
// in front of it, so the same index arrives from several rows. It must be drawn
// once.
func TestASurfaceSeenFromTwoCellsIsDrawnOnce(t *testing.T) {
	s := twoCellTree()
	s.Tree.Leaves[1].Faces = []int{1, 2}
	s.Vis.Set(0, 1)
	s.Vis.Set(1, 0)

	got := s.VisibleSurfaces(mgl32.Vec3{1, 0, 0}, nil)
	seen := map[int]int{}
	for _, i := range got {
		seen[i]++
	}
	if len(got) != 3 {
		t.Fatalf("got %v, want all three surfaces", got)
	}
	for i, n := range seen {
		if n != 1 {
			t.Errorf("surface %d appears %d times", i, n)
		}
	}
}

// The camera clipped into a wall, or floated outside a level that leaked. The
// compiler computed nothing for there, so there is no row to trust — and the
// answer has to be everything, because slow and correct beats a hole in the
// world.
func TestACameraSomewhereImpossibleDrawsEverything(t *testing.T) {
	for _, tc := range []struct {
		name string
		mark func(l *bsp.Leaf)
	}{
		{"inside material", func(l *bsp.Leaf) { l.Solid = true }},
		{"out in the void", func(l *bsp.Leaf) { l.Outside = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := twoCellTree()
			tc.mark(&s.Tree.Leaves[0])
			if got := s.VisibleSurfaces(mgl32.Vec3{1, 0, 0}, nil); len(got) != 3 {
				t.Errorf("got %v, want every surface", got)
			}
		})
	}
}

// A level compiled without visibility still draws.
func TestNoVisibilitySetDrawsEverything(t *testing.T) {
	s := twoCellTree()
	s.Vis = nil
	if got := s.VisibleSurfaces(mgl32.Vec3{1, 0, 0}, nil); len(got) != 3 {
		t.Errorf("got %v, want every surface", got)
	}
}

// The frame loop hands the same slice back every frame; it must not accumulate.
func TestTheResultSliceIsReused(t *testing.T) {
	s := twoCellTree()
	buf := s.VisibleSurfaces(mgl32.Vec3{1, 0, 0}, nil)
	for i := 0; i < 3; i++ {
		buf = s.VisibleSurfaces(mgl32.Vec3{1, 0, 0}, buf)
	}
	if len(buf) != 2 {
		t.Errorf("after four frames the list is %d long, want 2", len(buf))
	}
}

// Switching a compiled chunk off has to take its surfaces off the screen.
//
// A compiled chunk's surfaces are not its own any more, so releasing its vertex
// buffers reaches nothing — without this, `@chunk.prop.enabled = false` takes
// an area out of collision and out of the spatial hash and leaves it visible,
// which is worse than either being on or off.
func TestSwitchingAChunkOffHidesItsCompiledSurfaces(t *testing.T) {
	s := twoCellTree()
	s.Surfaces[0].Chunk = "north"
	s.Surfaces[1].Chunk = "main"
	s.Surfaces[2].Chunk = "main"

	if got := s.VisibleSurfaces(mgl32.Vec3{1, 0, 0}, nil); len(got) != 2 {
		t.Fatalf("before switching anything off: %v", got)
	}
	s.SetChunkHidden("north", true)
	got := s.VisibleSurfaces(mgl32.Vec3{1, 0, 0}, nil)
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("with the north chunk off: got %v, want only surface 1", got)
	}
	// And the fallback path, which is a different loop.
	s.Tree.Leaves[0].Solid = true
	if got := s.VisibleSurfaces(mgl32.Vec3{1, 0, 0}, nil); len(got) != 2 {
		t.Errorf("drawing everything with the north chunk off: got %v, want the two that are left", got)
	}
	s.SetChunkHidden("north", false)
	if got := s.VisibleSurfaces(mgl32.Vec3{1, 0, 0}, nil); len(got) != 3 {
		t.Errorf("after switching it back on: got %v, want everything", got)
	}
}
