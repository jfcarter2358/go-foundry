package nav

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// hillLevel is a 20x20-unit terrain with a gentle hill in the middle, a steep
// cliff along x >= 15, and a pit of hole cells at x,z in [4,7).
func hillLevel() *world.Level {
	tr := world.NewTerrain(mgl32.Vec3{0, 0, 0}, 1, 20, 20)
	for z := 0; z <= 20; z++ {
		for x := 0; x <= 20; x++ {
			h := float32(0)
			dx, dz := float32(x)-10, float32(z)-10
			if d := dx*dx + dz*dz; d < 16 {
				h = (16 - d) * 0.08 // at most 1.28 high, about 20 degrees at the steepest
			}
			if x >= 15 {
				h = 5
			}
			tr.Heights[tr.Index(x, z)] = h
		}
	}
	for z := 4; z < 7; z++ {
		for x := 4; x < 7; x++ {
			tr.SetHole(x, z, true)
		}
	}
	ch := world.Chunk{ID: "ground", Terrain: tr}
	ch.RecomputeBounds()
	return &world.Level{Chunks: []world.Chunk{ch}}
}

func TestTerrainIsWalkableOverTheHill(t *testing.T) {
	lvl := hillLevel()
	g := gridFor(t, lvl, playerParams)
	top := mgl32.Vec3{10, 1.28, 10}
	n, ok := g.Locate(top, 0.3)
	if !ok || g.Nodes[n].Floor < 1.1 {
		t.Fatalf("no node on the hilltop near %v", top)
	}
	if !g.Reachable(mgl32.Vec3{2, 0, 12}, top) {
		t.Fatal("the hilltop is not reachable from the flat")
	}
}

func TestTerrainHolesAndCliffsAreNotWalked(t *testing.T) {
	g := gridFor(t, hillLevel(), playerParams)
	for _, nd := range g.Nodes {
		x, z := g.HF.ColumnCenter(int(nd.Col))
		if x > 4.1 && x < 6.9 && z > 4.1 && z < 6.9 {
			t.Fatalf("a node stands in the pit at %v,%v,%v", x, nd.Floor, z)
		}
		if x > 14.2 && x < 14.8 {
			t.Fatalf("a node stands on the cliff face at %v,%v,%v", x, nd.Floor, z)
		}
	}
	if g.Reachable(mgl32.Vec3{2, 0, 12}, mgl32.Vec3{17, 5, 12}) {
		t.Fatal("walked up a 5-unit cliff")
	}
}

func TestATerrainEditRebuildsNavigation(t *testing.T) {
	lvl := hillLevel()
	a := Signature(lvl)
	tr := lvl.Chunks[0].Terrain
	tr.Heights[tr.Index(2, 2)] = 3
	tr.Touch()
	if Signature(lvl) == a {
		t.Fatal("a sculpted terrain kept its navigation signature")
	}
}
