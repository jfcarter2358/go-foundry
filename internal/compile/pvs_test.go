package compile

// pvs_test.go — visibility, on levels whose answer can be worked out on paper.
//
// Every fixture here is SEALED, because that is the contract: the void outside
// a level is filled in (outside.go), and a level with a hole in its shell has
// no meaningful visibility to compute. The fixtures are built out of solid
// slabs rather than NewHollowRoom for the same reason — a shell assembled by
// hand is one whose seams can be checked by reading it.

import (
	"testing"

	"foundry/internal/bsp"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// boxFromTo is a solid brush spanning two corners, which is how a shell is
// easier to think about than as a centre and half-extents.
func boxFromTo(id string, lo, hi mgl32.Vec3) world.Brush {
	return world.NewBoxBrush(id, lo.Add(hi).Mul(0.5), hi.Sub(lo).Mul(0.5), "concrete")
}

// compiled runs the whole geometry pipeline on a level.
func compiled(t *testing.T, lvl *world.Level) (*BSPTree, []Portal, *bsp.PVS) {
	t.Helper()
	faces, err := CSG(lvl)
	if err != nil {
		t.Fatalf("CSG: %v", err)
	}
	tr := BuildBSP(faces)
	portals, vis := ComputeVisibility(tr)
	return tr, portals, vis
}

// corridorLevel is two rooms joined by a narrow corridor on the centre line,
// the whole thing sealed in a shell.
//
//	+----------------+----------------+
//	|                |                |
//	|      room A    ==== corridor ====   room B
//	|                |                |
//	+----------------+----------------+
func corridorLevel() *world.Level {
	return levelOf(
		boxFromTo("floor", mgl32.Vec3{-22, -1, -12}, mgl32.Vec3{12, 0, 12}),
		boxFromTo("ceil", mgl32.Vec3{-22, 4, -12}, mgl32.Vec3{12, 5, 12}),
		boxFromTo("west", mgl32.Vec3{-21, 0, -11}, mgl32.Vec3{-20, 4, 11}),
		boxFromTo("east", mgl32.Vec3{10, 0, -11}, mgl32.Vec3{11, 4, 11}),
		boxFromTo("south", mgl32.Vec3{-21, 0, -11}, mgl32.Vec3{11, 4, -10}),
		boxFromTo("north", mgl32.Vec3{-21, 0, 10}, mgl32.Vec3{11, 4, 11}),
		// The wall between the rooms, in two pieces, leaving a gap on the
		// centre line.
		boxFromTo("mid_s", mgl32.Vec3{-10, 0, -10}, mgl32.Vec3{0, 4, -1}),
		boxFromTo("mid_n", mgl32.Vec3{-10, 0, 1}, mgl32.Vec3{0, 4, 10}),
	)
}

// uLevel is a corridor bent twice, so that its two ends face each other across
// a solid block. The interesting fixture: both bends lie ON THE SAME PLANE,
// which is exactly the case a forward test that accepts a coplanar portal gets
// wrong — the two ends come out visible to each other straight through the
// block between them.
//
//	|  centre block  |
//	|                |
//	+----------------+
//	  the bottom arm
func uLevel() *world.Level {
	return levelOf(
		boxFromTo("floor", mgl32.Vec3{-14, -1, -10}, mgl32.Vec3{14, 0, 10}),
		boxFromTo("ceil", mgl32.Vec3{-14, 4, -10}, mgl32.Vec3{14, 5, 10}),
		boxFromTo("west", mgl32.Vec3{-13, 0, -9}, mgl32.Vec3{-12, 4, 9}),
		boxFromTo("east", mgl32.Vec3{12, 0, -9}, mgl32.Vec3{13, 4, 9}),
		boxFromTo("south", mgl32.Vec3{-12, 0, -9}, mgl32.Vec3{12, 4, -8}),
		boxFromTo("north", mgl32.Vec3{-12, 0, 8}, mgl32.Vec3{12, 4, 9}),
		boxFromTo("centre", mgl32.Vec3{-8, 0, -4}, mgl32.Vec3{8, 4, 8}),
	)
}

// twoSealedRooms is two hollow rooms with nothing between them but the void.
func twoSealedRooms() *world.Level {
	var bs []world.Brush
	bs = append(bs, world.NewHollowRoom("a", mgl32.Vec3{-4, 0, -4}, mgl32.Vec3{4, 4, 4}, 0.5, "concrete")...)
	bs = append(bs, world.NewHollowRoom("b", mgl32.Vec3{20, 0, -4}, mgl32.Vec3{28, 4, 4}, 0.5, "concrete")...)
	return levelOf(bs...)
}

// The whole point of the stage: a room you are not in costs nothing.
func TestASealedRoomSeesOnlyItself(t *testing.T) {
	tr, _, vis := compiled(t, twoSealedRooms())
	a := tr.FindLeaf(mgl32.Vec3{0, 2, 0})
	b := tr.FindLeaf(mgl32.Vec3{24, 2, 0})
	if a == b {
		t.Fatal("both rooms came back as the same cell")
	}
	if vis.Visible(a, b) {
		t.Error("one sealed room can see into another")
	}
	if n := vis.VisibleCount(a); n != 1 {
		t.Errorf("a sealed room sees %d cells, want only itself", n)
	}
}

// The void around the level is filled in and takes no part in anything. Without
// it the exterior is one huge region carved into dozens of mutually visible
// cells, and the flood over it takes minutes for a level of two rooms.
func TestTheVoidAroundTheLevelIsFilledIn(t *testing.T) {
	tr, portals, vis := compiled(t, twoSealedRooms())
	if tr.InteriorLeafCount() != 2 {
		t.Errorf("%d interior cells for two sealed rooms, want one each", tr.InteriorLeafCount())
	}
	if tr.EmptyLeafCount() <= tr.InteriorLeafCount() {
		t.Fatal("the fixture has no exterior cells; the test proves nothing")
	}
	if len(portals) != 0 {
		t.Errorf("%d portals between two rooms that do not connect", len(portals))
	}
	// A cell in the void is somewhere nothing can be, so it sees nothing at
	// all — not even itself, which would invite a runtime to draw from it.
	out := tr.FindLeaf(mgl32.Vec3{0, 40, 0})
	if !tr.Leaves[out].Outside {
		t.Fatal("well above the level did not come back as void")
	}
	if n := vis.VisibleCount(out); n != 0 {
		t.Errorf("a void cell sees %d cells, want none", n)
	}
	if tr.LeaksAt(mgl32.Vec3{0, 2, 0}) {
		t.Error("the inside of a sealed room reads as leaked")
	}
}

// A hole in the shell lets the fill in, and what it reaches stops being part of
// the level. That is what a leak IS, and saying so is more use than compiling a
// level whose visibility means nothing.
func TestAHoleInTheShellReadsAsALeak(t *testing.T) {
	lvl := corridorLevel()
	if tr, _, _ := compiled(t, lvl); tr.LeaksAt(mgl32.Vec3{-15, 2, 0}) {
		t.Fatal("the sealed fixture already leaks")
	}
	// Take the north wall away.
	lvl = corridorLevel()
	lvl.Chunks[0].Brushes = append(lvl.Chunks[0].Brushes[:5], lvl.Chunks[0].Brushes[6:]...)
	tr, _, _ := compiled(t, lvl)
	if !tr.LeaksAt(mgl32.Vec3{-15, 2, 0}) {
		t.Error("a level with a wall missing did not read as leaked")
	}
}

// Two cells joined by a corridor can see each other, and the visibility is
// mutual — an asymmetric answer would mean one of them draws a room the other
// says is not there.
func TestConnectedCellsSeeEachOther(t *testing.T) {
	tr, portals, vis := compiled(t, corridorLevel())
	if len(portals) == 0 {
		t.Fatal("a level with a corridor in it produced no portals")
	}
	a := tr.FindLeaf(mgl32.Vec3{-15, 2, 0})
	b := tr.FindLeaf(mgl32.Vec3{5, 2, 0})
	if a == b {
		t.Fatal("the two rooms came back as one cell")
	}
	if !vis.Visible(a, b) {
		t.Error("two rooms joined by a straight corridor cannot see each other")
	}
	assertSymmetric(t, tr, vis)
}

// The case a naive forward test gets wrong. Both corners of the U lie on one
// plane, so the two arms face each other edge-on through the block between
// them: connected, and not visible.
func TestABendBlocksSight(t *testing.T) {
	tr, _, vis := compiled(t, uLevel())
	west := tr.FindLeaf(mgl32.Vec3{-10, 2, 7})
	east := tr.FindLeaf(mgl32.Vec3{10, 2, 7})
	bottom := tr.FindLeaf(mgl32.Vec3{0, 2, -6})
	if west == east || west == bottom || east == bottom {
		t.Fatal("the U did not come out as three cells")
	}
	if !vis.Visible(west, bottom) || !vis.Visible(east, bottom) {
		t.Fatal("an arm of the U cannot see the arm it opens into")
	}
	if vis.Visible(west, east) {
		t.Error("the two ends of the U see each other through the block between them")
	}
	assertSymmetric(t, tr, vis)
}

// assertSymmetric checks the whole matrix. Visibility is a property of a pair
// of cells, so a row and a column that disagree is a bug in the flood, not a
// conservative approximation.
func assertSymmetric(t *testing.T, tr *BSPTree, vis *bsp.PVS) {
	t.Helper()
	for a := range tr.Leaves {
		for b := range tr.Leaves {
			if vis.Visible(a, b) != vis.Visible(b, a) {
				t.Fatalf("leaf %d sees %d but not the other way round", a, b)
			}
		}
	}
}

// Nothing is ever in a solid or a filled-in cell, so nothing sees one.
func TestNothingSeesASolidOrFilledCell(t *testing.T) {
	tr, _, vis := compiled(t, corridorLevel())
	for a := range tr.Leaves {
		for b := range tr.Leaves {
			if !vis.Visible(a, b) {
				continue
			}
			if tr.Leaves[b].Solid || tr.Leaves[b].Outside {
				t.Fatalf("leaf %d can see leaf %d (solid=%v outside=%v)",
					a, b, tr.Leaves[b].Solid, tr.Leaves[b].Outside)
			}
		}
	}
}

// The separating-plane clip on its own, on numbers that can be worked out by
// hand: a 2x2 window at x=0 seen through a 2x2 aperture at x=1 offset two units
// along +Z casts a wedge onto the wall at x=2. A point at z0 in the source seen
// through z1 in the aperture lands at 2*z1 - z0, so the wedge runs from
// 2*2 - 1 = 3 to 2*4 + 1 = 9.
func TestTheSeparatorClipIsAProjectedWedge(t *testing.T) {
	source := []mgl32.Vec3{{0, -1, -1}, {0, 1, -1}, {0, 1, 1}, {0, -1, 1}}
	pass := []mgl32.Vec3{{1, -1, 2}, {1, 1, 2}, {1, 1, 4}, {1, -1, 4}}
	target := []mgl32.Vec3{{2, -10, -10}, {2, 10, -10}, {2, 10, 10}, {2, -10, 10}}

	got := clipToSeparators(source, pass, target)
	if len(got) < 3 {
		t.Fatal("the whole target was clipped away")
	}
	lo, hi := bsp.Bounds(got)
	if bsp.Abs(lo.Z()-3) > 0.01 || bsp.Abs(hi.Z()-9) > 0.01 {
		t.Errorf("the wedge covers z %v..%v, want 3..9", lo.Z(), hi.Z())
	}
}

// A target entirely out of the wedge is gone, not merely smaller.
func TestTheSeparatorClipCanRemoveTheTargetEntirely(t *testing.T) {
	source := []mgl32.Vec3{{0, -1, -1}, {0, 1, -1}, {0, 1, 1}, {0, -1, 1}}
	pass := []mgl32.Vec3{{1, -1, 2}, {1, 1, 2}, {1, 1, 4}, {1, -1, 4}}
	// Behind the source rather than in the wedge in front of the aperture.
	target := []mgl32.Vec3{{2, -1, -20}, {2, 1, -20}, {2, 1, -10}, {2, -1, -10}}
	if got := clipToSeparators(source, pass, target); len(got) >= 3 {
		t.Errorf("a target outside the sight cone survived with %d points", len(got))
	}
}

// A compile has to be reproducible — a bsp.PVS that came out differently twice
// would make every diff of a compiled level unreadable.
func TestVisibilityIsDeterministic(t *testing.T) {
	_, pa, va := compiled(t, corridorLevel())
	_, pb, vb := compiled(t, corridorLevel())
	if len(pa) != len(pb) {
		t.Fatalf("two runs produced %d and %d portals", len(pa), len(pb))
	}
	if va.Leaves != vb.Leaves {
		t.Fatalf("two runs produced %d and %d leaves", va.Leaves, vb.Leaves)
	}
	for i := range va.Bits {
		if va.Bits[i] != vb.Bits[i] {
			t.Fatalf("the visibility bits differ between runs at byte %d", i)
		}
	}
}
