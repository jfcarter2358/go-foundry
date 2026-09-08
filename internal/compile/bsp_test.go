package compile

// bsp_test.go — the partition, checked on shapes whose cells can be named.
//
// The assertions are deliberately about what the tree MEANS rather than about
// its shape: how many nodes a level produces depends on the splitter heuristic
// and would change the day anyone tuned it, but "the middle of the room is an
// empty cell and the inside of the wall is a solid one" is true of any correct
// build.

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// roomLevel is a 8x4x8 room with half-unit walls, hollow from (-4,0,-4) to
// (4,4,4). The standard fixture: a single NewBoxBrush has no interior to have
// cells in.
func roomLevel() *world.Level {
	return levelOf(world.NewHollowRoom("r", mgl32.Vec3{-4, 0, -4}, mgl32.Vec3{4, 4, 4}, 0.5, "concrete")...)
}

func buildRoom(t *testing.T) *BSPTree {
	t.Helper()
	tr := BuildBSP(mustCSG(t, roomLevel()))
	if len(tr.Leaves) == 0 {
		t.Fatal("the build produced no leaves at all")
	}
	return tr
}

// The three places a point can be, and the tree has to tell them apart: open
// air inside the room, material inside a wall, and open air outside.
func TestALeafKnowsWhetherItIsSolid(t *testing.T) {
	tr := buildRoom(t)

	inside := tr.Leaves[tr.FindLeaf(mgl32.Vec3{0, 2, 0})]
	if inside.Solid {
		t.Error("the middle of the room came back solid")
	}
	wall := tr.Leaves[tr.FindLeaf(mgl32.Vec3{4.25, 2, 0})]
	if !wall.Solid {
		t.Error("the inside of the +X wall came back empty")
	}
	outside := tr.Leaves[tr.FindLeaf(mgl32.Vec3{20, 2, 0})]
	if outside.Solid {
		t.Error("open air well outside the room came back solid")
	}
}

// The room is convex and its six inner surfaces bound exactly one cell, so the
// cell the camera stands in has the room's own extent — not the world box, and
// not some sliver of it.
func TestTheRoomIsOneCell(t *testing.T) {
	tr := buildRoom(t)
	l := tr.Leaves[tr.FindLeaf(mgl32.Vec3{0, 2, 0})]

	want := [2]mgl32.Vec3{{-4, 0, -4}, {4, 4, 4}}
	for i := 0; i < 3; i++ {
		if geomAbs(l.Lo[i]-want[0][i]) > 0.01 || geomAbs(l.Hi[i]-want[1][i]) > 0.01 {
			t.Fatalf("cell bounds %v..%v, want the room's %v..%v", l.Lo, l.Hi, want[0], want[1])
		}
	}
	if len(l.Faces) != 6 {
		t.Errorf("the cell is bounded by %d faces, want the room's 6 inner surfaces", len(l.Faces))
	}
	// Every corner of the room is inside the cell's own half-spaces, which is
	// the representation portal generation clips against — it has to agree with
	// the tree walk FindLeaf did.
	for _, p := range []mgl32.Vec3{{-3.9, 0.1, -3.9}, {3.9, 3.9, 3.9}, {0, 2, 0}} {
		for _, pl := range l.Planes {
			if pl.Distance(p) < -onPlaneEps {
				t.Errorf("%v is outside the cell's half-spaces but FindLeaf put it in", p)
				break
			}
		}
	}
}

// Every face the cell lists faces INTO it. A face attached to the leaf on its
// back side would be drawn from behind — invisible, or worse, visible as a
// hole once backface culling is off for a double-sided surface.
func TestACellsFacesPointIntoIt(t *testing.T) {
	tr := buildRoom(t)
	li := tr.FindLeaf(mgl32.Vec3{0, 2, 0})
	l := tr.Leaves[li]
	for _, fi := range l.Faces {
		f := tr.Faces[fi]
		// The centre of the room is in front of every inward-facing surface.
		if f.Plane.Distance(mgl32.Vec3{0, 2, 0}) < 0 {
			t.Errorf("face %d (%v) is attached to the cell but faces away from it", fi, f.Plane.N)
		}
	}
}

// A splitting plane cuts the faces that cross it, and the halves are still
// faces: same texture, same source, and between them the same area. A build
// that dropped a sliver rather than splitting would be a hole in the level.
func TestSplittingAFaceKeepsAllOfIt(t *testing.T) {
	// A long floor slab with a pillar sunk into the middle of it, which forces
	// planes through the slab that its own faces have to be cut by.
	lvl := levelOf(
		world.NewBoxBrush("floor", mgl32.Vec3{0, -1, 0}, mgl32.Vec3{10, 1, 10}, "concrete"),
		world.NewBoxBrush("pillar", mgl32.Vec3{3, 1.75, 3}, mgl32.Vec3{1, 2.25, 1}, "concrete"),
	)
	faces := mustCSG(t, lvl)
	tr := BuildBSP(faces)

	before := totalArea(faces)
	// Only the faces the tree actually kept count: a split face is replaced by
	// its halves, and the original stays in the list unreferenced.
	var after float32
	seen := map[int]bool{}
	for li := range tr.Leaves {
		for _, fi := range tr.Leaves[li].Faces {
			if seen[fi] {
				continue
			}
			seen[fi] = true
			after += PolygonArea(tr.Faces[fi].Points)
		}
	}
	if after < before-0.05 {
		t.Errorf("the tree accounts for %v of the %v the compiler produced", after, before)
	}
	for fi := range seen {
		if tr.Faces[fi].Texture != "concrete" {
			t.Errorf("fragment %d lost its texture", fi)
		}
	}
}

// A compile has to be reproducible: the BSP's plane choice feeds the PVS's leaf
// numbering, which feeds the baked light, so a tree that came out differently
// twice would make every diff of a compiled level unreadable.
func TestTheBuildIsDeterministic(t *testing.T) {
	a := BuildBSP(mustCSG(t, roomLevel()))
	b := BuildBSP(mustCSG(t, roomLevel()))
	if len(a.Nodes) != len(b.Nodes) || len(a.Leaves) != len(b.Leaves) {
		t.Fatalf("two builds of the same level: %d/%d nodes, %d/%d leaves",
			len(a.Nodes), len(b.Nodes), len(a.Leaves), len(b.Leaves))
	}
	for i := range a.Nodes {
		if !a.Nodes[i].Plane.SameAs(b.Nodes[i].Plane) {
			t.Fatalf("node %d split on a different plane the second time", i)
		}
	}
}

// An empty level still has to produce a walkable tree rather than a nil one:
// the editor compiles whatever is open, including a level someone has just
// emptied out.
func TestAnEmptyLevelStillBuilds(t *testing.T) {
	tr := BuildBSP(nil)
	if len(tr.Leaves) != 1 {
		t.Fatalf("got %d leaves for an empty level, want the one cell of open world", len(tr.Leaves))
	}
	if tr.Leaves[tr.FindLeaf(mgl32.Vec3{})].Solid {
		t.Error("the origin of an empty level came back solid")
	}
}
