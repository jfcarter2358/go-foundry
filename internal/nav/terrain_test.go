package nav

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// hillLevel is a 20x16x20 block terrain whose ground is at Y 4, with a
// stepped hill in the middle (smoothed into a slope when smooth), a 5-block
// cliff along x >= 15, and a pit mined out at x,z in [3,7).
func hillLevel(smooth bool) *world.Level {
	tr := world.NewTerrain(mgl32.Vec3{0, 0, 0}, 1, 20, 16, 20)
	for z := 0; z < 20; z++ {
		for x := 0; x < 20; x++ {
			h := 4
			dx, dz := x-10, z-10
			if d := dx*dx + dz*dz; d < 16 {
				h += (16 - d) / 6 // at most 2 blocks, in steps a few blocks apart
			}
			if x >= 15 {
				h = 9
			}
			if x >= 3 && x < 7 && z >= 3 && z < 7 {
				h = 0
			}
			for y := 0; y < h; y++ {
				tr.SetBlock(x, y, z, 1)
			}
		}
	}
	tr.Smooth = smooth
	tr.Touch()
	ch := world.Chunk{ID: "ground", Terrain: tr}
	ch.RecomputeBounds()
	return &world.Level{Chunks: []world.Chunk{ch}}
}

func TestSmoothTerrainIsWalkableOverTheHill(t *testing.T) {
	lvl := hillLevel(true)
	g := gridFor(t, lvl, playerParams)
	top := mgl32.Vec3{10.5, 6, 10.5}
	n, ok := g.Locate(top, 0.6)
	if !ok || g.Nodes[n].Floor < 5.4 {
		t.Fatalf("no node on the hilltop near %v", top)
	}
	if !g.Reachable(mgl32.Vec3{1.5, 4, 12.5}, top) {
		t.Fatal("the hilltop is not reachable from the flat")
	}
}

func TestTerrainPitsAndCliffsAreNotWalked(t *testing.T) {
	for _, smooth := range []bool{false, true} {
		g := gridFor(t, hillLevel(smooth), playerParams)
		for _, nd := range g.Nodes {
			x, z := g.HF.ColumnCenter(int(nd.Col))
			if x > 4 && x < 6 && z > 4 && z < 6 && nd.Floor > 1 {
				t.Fatalf("smooth=%v: a node floats over the pit at %v,%v,%v", smooth, x, nd.Floor, z)
			}
		}
		if g.Reachable(mgl32.Vec3{1.5, 4, 12.5}, mgl32.Vec3{17.5, 9, 12.5}) {
			t.Fatalf("smooth=%v: walked up a 5-block cliff", smooth)
		}
	}
}

// A cave stays open to navigation: it has a floor to walk and the ground
// above it has one too.
func TestATerrainCaveIsNavigable(t *testing.T) {
	tr := world.NewTerrain(mgl32.Vec3{0, 0, 0}, 1, 20, 16, 20)
	for z := 0; z < 20; z++ {
		for x := 0; x < 20; x++ {
			for y := 0; y < 10; y++ {
				if y >= 2 && y < 6 && x >= 4 && x < 16 && z >= 4 && z < 16 {
					continue
				}
				tr.SetBlock(x, y, z, 1)
			}
		}
	}
	ch := world.Chunk{ID: "ground", Terrain: tr}
	ch.RecomputeBounds()
	g := gridFor(t, &world.Level{Chunks: []world.Chunk{ch}}, playerParams)
	if n, ok := g.Locate(mgl32.Vec3{10, 2, 10}, 0.5); !ok || g.Nodes[n].Floor > 2.5 {
		t.Fatal("no node on the cave floor")
	}
	if n, ok := g.Locate(mgl32.Vec3{10, 10, 10}, 0.5); !ok || g.Nodes[n].Floor < 9.5 {
		t.Fatal("no node on the ground over the cave")
	}
}

func TestATerrainEditRebuildsNavigation(t *testing.T) {
	lvl := hillLevel(false)
	a := Signature(lvl)
	lvl.Chunks[0].Terrain.SetBlock(2, 4, 2, 1)
	if Signature(lvl) == a {
		t.Fatal("an edited terrain kept its navigation signature")
	}
}
