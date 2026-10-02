package nav

// scenery_test.go — cooked Nanite meshes in navigation
// (appendScenerySolids): a hull is a solid an agent walks round, a mesh-mode
// ramp is a floor it walks up, and a mesh-mode cliff is a wall it does not.

import (
	"testing"

	"foundry/internal/meshcollide"
	"foundry/internal/nanite"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func navBox(lo, hi mgl32.Vec3) ([]float32, []uint32) {
	var pos []float32
	for i := 0; i < 8; i++ {
		p := lo
		if i&1 != 0 {
			p[0] = hi[0]
		}
		if i&2 != 0 {
			p[1] = hi[1]
		}
		if i&4 != 0 {
			p[2] = hi[2]
		}
		pos = append(pos, p[0], p[1], p[2])
	}
	var idx []uint32
	for _, q := range [][4]uint32{{0, 4, 6, 2}, {1, 3, 7, 5}, {0, 1, 5, 4}, {2, 6, 7, 3}, {0, 2, 3, 1}, {4, 5, 7, 6}} {
		idx = append(idx, q[0], q[1], q[2], q[0], q[2], q[3])
	}
	return pos, idx
}

// navCooked is a placement of a mesh with cooked collision.
func navCooked(t *testing.T, id string, at mgl32.Vec3, pos []float32, idx []uint32, mode meshcollide.Mode) world.Scenery {
	t.Helper()
	// Pinned on: FOUNDRY_NANITE_COLLISION=0 in the environment would make
	// this a test of the box path.
	prevNC := world.NaniteCollision
	world.NaniteCollision = true
	t.Cleanup(func() { world.NaniteCollision = prevNC })
	d, err := meshcollide.Build(meshcollide.Input{Positions: pos, Stride: 3, Indices: idx}, meshcollide.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	lo := mgl32.Vec3{pos[0], pos[1], pos[2]}
	hi := lo
	for i := 0; i+2 < len(pos); i += 3 {
		p := mgl32.Vec3{pos[i], pos[i+1], pos[i+2]}
		lo = mgl32.Vec3{min(lo[0], p[0]), min(lo[1], p[1]), min(lo[2], p[2])}
		hi = mgl32.Vec3{max(hi[0], p[0]), max(hi[1], p[1]), max(hi[2], p[2])}
	}
	s := world.NewScenery(id, "models/"+id+".fnm", at)
	s.Nanite = &nanite.Mesh{Lo: lo, Hi: hi, Collision: d}
	s.LocalMin, s.LocalMax = lo, hi
	s.Collision = mode
	return s
}

func bigRoom() *world.Level {
	ch := world.Chunk{ID: "main"}
	ch.Brushes = world.NewHollowRoom("room", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{30, 8, 30}, 0.5, "concrete")
	return &world.Level{Chunks: []world.Chunk{ch}}
}

// A rock in the hulls mode is in the heightfield: no floor at ground level
// under it, and the far side reached round it.
func TestAHullRockIsWalkedRound(t *testing.T) {
	lvl := bigRoom()
	pos, idx := navBox(mgl32.Vec3{-1.5, 0, -1.5}, mgl32.Vec3{1.5, 2, 1.5})
	lvl.Scenery = []world.Scenery{navCooked(t, "rock", mgl32.Vec3{15, 0, 15}, pos, idx, meshcollide.ModeHulls)}
	g := gridFor(t, lvl, playerParams)
	for _, nd := range g.Nodes {
		x, z := g.HF.ColumnCenter(int(nd.Col))
		if x > 14 && x < 16 && z > 14 && z < 16 && nd.Floor < 1 {
			t.Fatalf("a ground node inside the rock at %v,%v,%v", x, nd.Floor, z)
		}
	}
	if !g.Reachable(mgl32.Vec3{15, 0, 10}, mgl32.Vec3{15, 0, 20}) {
		t.Fatal("the far side of the rock is unreachable")
	}

	// Mutation check: the same rock in the box mode is not rasterised (box
	// scenery never has been), so the ground under it is a floor.
	lvl.Scenery[0].Collision = meshcollide.ModeBox
	g = gridFor(t, lvl, playerParams)
	if n, ok := g.Locate(mgl32.Vec3{15, 0, 15}, 0.3); !ok || g.Nodes[n].Floor > 0.1 {
		t.Fatal("a box-mode rock was rasterised after all")
	}
}

// A mesh-mode ramp is floor all the way up, and a mesh-mode cliff is not
// climbed.
func TestAMeshRampIsWalkedUpAndACliffIsNot(t *testing.T) {
	lvl := bigRoom()
	// A ramp sheet 4 wide rising 2.5 over 10 (14 degrees), x 5..15, meeting
	// a platform block 2.5 high at x 15..20 flush with its top; and apart
	// from them, a block 5 high with no way up.
	ramp := []float32{0, 0, -2, 10, 2.5, -2, 0, 0, 2, 10, 2.5, 2}
	rampIdx := []uint32{0, 2, 1, 1, 2, 3}
	plat, platIdx := navBox(mgl32.Vec3{0, 0, -3}, mgl32.Vec3{5, 2.5, 3})
	cliff, cliffIdx := navBox(mgl32.Vec3{0, 0, -4}, mgl32.Vec3{6, 5, 4})
	lvl.Scenery = []world.Scenery{
		navCooked(t, "ramp", mgl32.Vec3{5, 0, 8}, ramp, rampIdx, meshcollide.ModeMesh),
		navCooked(t, "plat", mgl32.Vec3{15, 0, 8}, plat, platIdx, meshcollide.ModeMesh),
		navCooked(t, "cliff", mgl32.Vec3{15, 0, 22}, cliff, cliffIdx, meshcollide.ModeMesh),
	}
	for i := range lvl.Scenery {
		if m := lvl.Scenery[i].CollisionMode(); m != meshcollide.ModeMesh {
			t.Fatalf("%s resolves to %v", lvl.Scenery[i].ID, m)
		}
	}
	g := gridFor(t, lvl, playerParams)
	if n, ok := g.Locate(mgl32.Vec3{10, 1.25, 8}, 0.6); !ok || g.Nodes[n].Floor < 1 || g.Nodes[n].Floor > 1.5 {
		t.Fatal("no floor on the middle of the ramp")
	}
	if !g.Reachable(mgl32.Vec3{2, 0, 8}, mgl32.Vec3{18, 2.5, 8}) {
		t.Fatal("the platform at the top of the ramp is unreachable")
	}
	if g.Reachable(mgl32.Vec3{2, 0, 22}, mgl32.Vec3{18, 5, 22}) {
		t.Fatal("walked up a 5 m cliff")
	}
}

// Moving a cooked placement changes the level's navigation signature, so the
// grid is rebuilt; switching it to box mode does too.
func TestCookedSceneryIsInTheSignature(t *testing.T) {
	lvl := bigRoom()
	pos, idx := navBox(mgl32.Vec3{-1, 0, -1}, mgl32.Vec3{1, 2, 1})
	lvl.Scenery = []world.Scenery{navCooked(t, "rock", mgl32.Vec3{15, 0, 15}, pos, idx, meshcollide.ModeHulls)}
	a := Signature(lvl)
	lvl.Scenery[0].Position = mgl32.Vec3{12, 0, 15}
	b := Signature(lvl)
	lvl.Scenery[0].Collision = meshcollide.ModeBox
	c := Signature(lvl)
	if a == b || b == c {
		t.Errorf("signatures %x %x %x: a move or a mode change went unseen", a, b, c)
	}
}
