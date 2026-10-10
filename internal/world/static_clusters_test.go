package world

// static_clusters_test.go — the draw groups a compiled level's surfaces are
// cut into (static_clusters.go), and VisibleSerial, which the GPU cull's
// candidate cache keys on.

import (
	"testing"

	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

func quadSurface(chunk, tex string, x float32, double bool) StaticSurface {
	g := gfx.Geometry{Texture: tex, Alpha: 1, Vertices: []float32{
		x, 0, 0, 0, 0, x + 1, 0, 0, 1, 0, x + 1, 1, 0, 1, 1,
		x, 0, 0, 0, 0, x + 1, 1, 0, 1, 1, x, 1, 0, 0, 1,
	}}
	return StaticSurface{Geometry: g, Chunk: chunk, DoubleSided: double,
		Lo: mgl32.Vec3{x, 0, 0}, Hi: mgl32.Vec3{x + 1, 1, 0}}
}

func TestStaticGroupsSplitByWhatADrawShares(t *testing.T) {
	s := &StaticGeometry{}
	for i := 0; i < 40; i++ {
		s.Surfaces = append(s.Surfaces, quadSurface("a", "brick", float32(i), false))
	}
	s.Surfaces = append(s.Surfaces,
		quadSurface("b", "brick", 0, false),    // another chunk
		quadSurface("a", "concrete", 0, false), // another texture
		quadSurface("a", "brick", 50, true),    // double-sided
		StaticSurface{Chunk: "a"},              // nothing to draw
		quadSurface("a", "brick", 60, false))   // back in the first group
	groups, groupOf := StaticGroups(s)
	if len(groups) != 4 {
		t.Fatalf("%d groups, want 4", len(groups))
	}
	if groupOf[43] != -1 {
		t.Fatal("an empty surface was grouped")
	}
	if groupOf[44] != groupOf[0] || len(groups[groupOf[0]].Surfaces) != 41 {
		t.Fatal("a surface sharing the first group's key did not join it")
	}
	ids := map[string]bool{}
	for _, g := range groups {
		ids[g.Key.ID()] = true
	}
	if len(ids) != len(groups) {
		t.Fatal("two groups share an ID")
	}
	// The cut covers a group's triangles exactly, with a signature that
	// changes when a member's triangle count does.
	g := &groups[groupOf[0]]
	cg := CutStaticGroup(s, g)
	if len(cg.Order) != 82 || len(cg.Clusters) == 0 {
		t.Fatalf("cut of %d triangles in %d clusters", len(cg.Order), len(cg.Clusters))
	}
	before := StaticGroupSignature(s, g)
	s.Surfaces[0].Geometry.Vertices = s.Surfaces[0].Geometry.Vertices[:15]
	if StaticGroupSignature(s, g) == before {
		t.Fatal("the signature did not change with a member's triangles")
	}
}

func TestVisibleSerialChangesWithTheAnswer(t *testing.T) {
	s := twoCellTree()
	for i := range s.Surfaces {
		s.Surfaces[i] = quadSurface("c", "", float32(i), false)
		s.Surfaces[i].Chunk = []string{"a", "a", "b"}[i]
	}
	var out []int
	out = s.VisibleSurfaces(mgl32.Vec3{1, 0, 0}, out)
	a := s.VisibleSerial()
	out = s.VisibleSurfaces(mgl32.Vec3{2, 0, 0}, out)
	if s.VisibleSerial() != a {
		t.Fatal("the serial moved without the answer changing (same cell)")
	}
	out = s.VisibleSurfaces(mgl32.Vec3{-1, 0, 0}, out)
	b := s.VisibleSerial()
	if b == a {
		t.Fatal("the serial did not move with a change of cell")
	}
	s.SetChunkHidden("b", true)
	_ = s.VisibleSurfaces(mgl32.Vec3{-1, 0, 0}, out)
	if s.VisibleSerial() == b {
		t.Fatal("the serial did not move when a chunk was hidden")
	}
}
