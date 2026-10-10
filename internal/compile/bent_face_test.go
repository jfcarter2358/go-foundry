package compile

// bent_face_test.go — a brush whose faces are not quite flat.
//
// Nothing stops an author making one. Dragging corners together to taper the
// end of a rail to a point is a reasonable thing to do in the editor, and it
// leaves quads whose corners no longer lie on one plane. The brush is still
// convex, so validation lets it through, and the build has to cope with it.

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// taperedRailEnd is the end cap of a diamond-section rail with its far corners
// pinched together: two of its quads bend by about 0.15 across their own
// planes, and a third has folded flat to no area at all. Lifted from a real
// level, moved to the middle of the room.
func taperedRailEnd() world.Brush {
	at := func(x, y, z float32) mgl32.Vec3 { return mgl32.Vec3{x - 4, y, z + 14.75} }
	return world.Brush{
		ID:    "rail_end",
		Solid: true,
		BrushMesh: world.BrushMesh{
			Verts: []mgl32.Vec3{
				at(4, 1.5, -15), at(4.25, 1.75, -15), at(3.75, 1.75, -15), at(4, 2, -15),
				at(4, 1.5, -14.5), at(4.25, 1.5, -14.75), at(3.75, 1.5, -14.75), at(4, 1.5, -14.5),
			},
			Faces: []world.BrushFace{
				{Indices: []int{0, 4, 6, 2}, Alpha: 1},
				{Indices: []int{5, 1, 3, 7}, Alpha: 1},
				{Indices: []int{0, 1, 5, 4}, Alpha: 1},
				{Indices: []int{2, 6, 7, 3}, Alpha: 1},
				{Indices: []int{1, 0, 2, 3}, Alpha: 1},
				{Indices: []int{4, 5, 7, 6}, Alpha: 1},
			},
		},
	}
}

// A bent face used to be split by its own plane, hand both halves back with
// that plane, and be split by it again — down one branch to the depth limit,
// with a copy of the plane in every cell below. Portals are found between cell
// boundaries pairwise, so the copies multiplied: a level with this brush in it
// made millions of portals and ran the visibility stage out of memory.
func TestABentFaceDoesNotRecurseToTheDepthLimit(t *testing.T) {
	bs := world.NewHollowRoom("room", mgl32.Vec3{-4, 0, -4}, mgl32.Vec3{4, 4, 4}, 0.5, "concrete")
	lvl := levelOf(append(bs, taperedRailEnd())...)

	tr := BuildBSP(mustCSG(t, lvl))
	deepest := 0
	for _, l := range tr.Leaves {
		deepest = max(deepest, len(l.Planes))
	}
	// Six for the world box, a handful for the room and a handful for the rail.
	if deepest > 40 {
		t.Errorf("a cell is bounded by %d planes; the build is cutting with the same plane over and over", deepest)
	}
	if n := len(BuildPortals(tr)); n > 2000 {
		t.Errorf("%d portals for a room with one small brush in it", n)
	}

	tr, _, _ = compiled(t, lvl)
	if tr.InteriorLeafCount() == 0 {
		t.Error("the sealed room has no interior cells")
	}
	if tr.LeaksAt(mgl32.Vec3{0, 3, 2}) {
		t.Error("the inside of a sealed room reads as leaked")
	}
}
