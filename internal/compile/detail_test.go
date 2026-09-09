package compile

// detail_test.go — what a brush's detail mesh may and may not do to a compile.
//
// The whole bargain is in three assertions: detail is DRAWN, it does not shape
// the TREE, and it does not stop the level SEALING. Each of those is a way the
// obvious implementation goes wrong — feed the detail faces to the build and
// they choose splitting planes and label the space behind themselves solid;
// leave the hull out because nothing draws it any more and the shell has a hole
// in it exactly where the detailed wall was.

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// detailedRoom is a sealed room whose north wall carries a detail mesh: the
// hull's own faces, plus a bump pulled off the inward side.
//
// The bump is deliberately DIAGONAL. An axis-aligned one would produce a plane
// the build might have chosen anyway, and the test could pass without the
// exclusion doing anything; a slanted plane appears in the tree only if
// something let it in.
func detailedRoom(t *testing.T, withDetail bool) *world.Level {
	t.Helper()
	lvl := levelOf(
		boxFromTo("floor", mgl32.Vec3{-12, -1, -12}, mgl32.Vec3{12, 0, 12}),
		boxFromTo("ceil", mgl32.Vec3{-12, 4, -12}, mgl32.Vec3{12, 5, 12}),
		boxFromTo("west", mgl32.Vec3{-12, 0, -12}, mgl32.Vec3{-11, 4, 12}),
		boxFromTo("east", mgl32.Vec3{11, 0, -12}, mgl32.Vec3{12, 4, 12}),
		boxFromTo("south", mgl32.Vec3{-11, 0, -12}, mgl32.Vec3{11, 4, -11}),
		boxFromTo("north", mgl32.Vec3{-11, 0, 11}, mgl32.Vec3{11, 4, 12}),
	)
	if !withDetail {
		return lvl
	}
	north := &lvl.Chunks[0].Brushes[5]
	north.SeedDetail()
	// A slanted shelf standing proud of the wall's inward face (z = 11),
	// hanging in front of it rather than joined to it: detail need not be
	// closed, convex or connected, and a lone quad is the shortest thing that
	// proves the pipeline carries one.
	d := north.Detail
	base := len(d.Verts)
	d.Verts = append(d.Verts,
		mgl32.Vec3{-4, 1, 10.5}, mgl32.Vec3{4, 1, 10.5},
		mgl32.Vec3{4, 2.5, 9.8}, mgl32.Vec3{-4, 2.5, 9.8})
	d.Faces = append(d.Faces, world.NewBrushFace(
		[]int{base, base + 1, base + 2, base + 3}, "concrete"))
	return lvl
}

// TestDetailIsDrawnButDoesNotShapeTheTree is the whole feature in one
// assertion: the partition of a level with detail in it is the partition it
// would have had without, and the detail is in a cell's draw list all the same.
func TestDetailIsDrawnButDoesNotShapeTheTree(t *testing.T) {
	plainTree, _, _ := compiled(t, detailedRoom(t, false))
	tree, _, _ := compiled(t, detailedRoom(t, true))

	if len(tree.Nodes) != len(plainTree.Nodes) || len(tree.Leaves) != len(plainTree.Leaves) {
		t.Fatalf("detail changed the partition: %d nodes/%d leaves with, %d/%d without",
			len(tree.Nodes), len(tree.Leaves), len(plainTree.Nodes), len(plainTree.Leaves))
	}

	// The bump is in some empty cell's draw list, which is what makes it
	// visible at run time.
	drawn := false
	for li := range tree.Leaves {
		l := &tree.Leaves[li]
		if l.Solid || l.Outside {
			continue
		}
		for _, fi := range l.Faces {
			if tree.Faces[fi].Detail {
				drawn = true
			}
		}
	}
	if !drawn {
		t.Error("no detail face reached an interior cell's draw list; the bump would not be drawn")
	}
}

// TestADetailedWallStillSeals is the other half. A detailed brush stops
// emitting its hull as drawable geometry, and if that were the whole story the
// shell would have a hole in it and the void fill would flood the room.
func TestADetailedWallStillSeals(t *testing.T) {
	tree, portals, _ := compiled(t, detailedRoom(t, true))
	MarkOutside(tree, portals)

	inside := tree.FindLeaf(mgl32.Vec3{0, 2, 0})
	if inside < 0 || inside >= len(tree.Leaves) {
		t.Fatalf("the middle of the room is not in a leaf (%d)", inside)
	}
	if l := &tree.Leaves[inside]; l.Solid || l.Outside {
		t.Errorf("the room leaked: the cell at its centre is solid=%v outside=%v", l.Solid, l.Outside)
	}
}

// TestStructuralHullFacesAreNeverDrawn checks the price of the assertion above
// is not paid twice: the hull faces a detailed brush contributes for the
// partition's sake must not end up in a draw list, or the solid would be drawn
// straight through the detail carved into it.
func TestStructuralHullFacesAreNeverDrawn(t *testing.T) {
	tree, _, _ := compiled(t, detailedRoom(t, true))

	structural := 0
	for fi := range tree.Faces {
		if tree.Faces[fi].NoDraw {
			structural++
		}
	}
	if structural == 0 {
		t.Fatal("a detailed brush contributed no structural faces; nothing shaped the tree where its hull is")
	}
	for li := range tree.Leaves {
		for _, fi := range tree.Leaves[li].Faces {
			if tree.Faces[fi].NoDraw {
				t.Fatalf("leaf %d draws structural face %d (brush %q)",
					li, fi, tree.Faces[fi].Brush)
			}
		}
	}
}

// TestDetailSurvivesTheLightBake is the trap the light stage sets: it decides
// which faces survived the build by reading the NODES, and a detail face was
// never assigned to one.
func TestDetailSurvivesTheLightBake(t *testing.T) {
	lvl := detailedRoom(t, true)
	res, err := Compile(lvl, DefaultOptions())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	for fi := range res.Tree.Faces {
		if res.Tree.Faces[fi].Detail {
			return
		}
	}
	t.Error("the light bake dropped every detail face")
}

// TestDetailDoesNotClipItsOwnBrush guards the case a naive CSG gets wrong: a
// detail face pressed INTO its own hull is inside that solid, and clipping a
// face against the brush it belongs to would eat it.
func TestDetailDoesNotClipItsOwnBrush(t *testing.T) {
	lvl := levelOf(boxFromTo("block", mgl32.Vec3{-4, 0, -4}, mgl32.Vec3{4, 4, 4}))
	b := &lvl.Chunks[0].Brushes[0]
	b.SeedDetail()
	// A recess: a quad sunk half a unit below the +Y face, well inside the
	// solid it belongs to.
	base := len(b.Detail.Verts)
	b.Detail.Verts = append(b.Detail.Verts,
		mgl32.Vec3{-1, 3.5, -1}, mgl32.Vec3{1, 3.5, -1},
		mgl32.Vec3{1, 3.5, 1}, mgl32.Vec3{-1, 3.5, 1})
	b.Detail.Faces = append(b.Detail.Faces, world.NewBrushFace(
		[]int{base, base + 3, base + 2, base + 1}, "concrete"))

	for _, f := range mustCSG(t, lvl) {
		if f.Detail && len(f.Points) == 4 && f.Points[0].Y() == 3.5 {
			return
		}
	}
	t.Error("the recessed detail face was clipped away by its own brush")
}
