package geom

// uv_atlas_test.go — the parts of the atlas that are about the packing and the
// orientation rather than about any one document. The model's own mesh-level
// cases (islands, gutters, density, determinism) are in
// model/model_uv_atlas_test.go, and a level's in world/brush_uv_test.go.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// rectsOverlap reports whether two half-open UV boxes intersect.
func rectsOverlap(aMin, aMax, bMin, bMax mgl32.Vec2) bool {
	return aMin.X() < bMax.X() && bMin.X() < aMax.X() &&
		aMin.Y() < bMax.Y() && bMin.Y() < aMax.Y()
}

// The packer must refuse rather than silently dropping islands it cannot fit.
func TestAtlasFitRefusesImpossiblePadding(t *testing.T) {
	sizes := make([][2]float32, 10)
	order := make([]int, len(sizes))
	for i := range order {
		order[i] = i
	}
	// A gutter of 0.3 makes each island 0.6 wide before any content, so three
	// fill a row and four rows overflow — ten cannot fit at any scale.
	if _, _, ok := atlasFit(sizes, order, 0.3); ok {
		t.Error("atlasFit accepted a layout that cannot fit")
	}
}

// atlasPack must place islands without overlap at whatever scale it is given.
func TestAtlasPackPlacesWithoutOverlap(t *testing.T) {
	sizes := [][2]float32{{0.4, 0.3}, {0.2, 0.3}, {0.3, 0.2}, {0.1, 0.1}}
	order := []int{0, 1, 2, 3}
	origins, _, ok := atlasPack(sizes, order, 1, 0.01)
	if !ok {
		t.Fatal("atlasPack refused a layout that fits")
	}
	for a := range sizes {
		aMin := mgl32.Vec2{origins[a][0], origins[a][1]}
		aMax := aMin.Add(mgl32.Vec2{sizes[a][0], sizes[a][1]})
		for b := a + 1; b < len(sizes); b++ {
			bMin := mgl32.Vec2{origins[b][0], origins[b][1]}
			bMax := bMin.Add(mgl32.Vec2{sizes[b][0], sizes[b][1]})
			if rectsOverlap(aMin, aMax, bMin, bMax) {
				t.Errorf("packed rects %d and %d overlap", a, b)
			}
		}
	}
}

// wallQuad is a 2 by 1 wall facing +Z, wound CCW seen from in front: bottom
// left, bottom right, top right, top left.
func wallQuad() ([]mgl32.Vec3, [][]int) {
	return []mgl32.Vec3{{0, 0, 0}, {2, 0, 0}, {2, 1, 0}, {0, 1, 0}}, [][]int{{0, 1, 2, 3}}
}

// VDown is what makes a wall read the right way up in the texture editor, where
// row 0 is the top: the wall's top edge has to come out at the smaller V. And
// it must only flip V — a U that also turned round would show the wall
// mirrored instead of upside down, which is no better.
func TestAtlasVDownPutsTheTopOfAWallAtTheTopOfTheImage(t *testing.T) {
	verts, faces := wallQuad()
	lay, err := AtlasUnwrap(verts, faces, AtlasOptions{PaddingUV: -1, VDown: true})
	if err != nil {
		t.Fatalf("AtlasUnwrap: %v", err)
	}
	uv := lay.UVs[0]
	if !(uv[3].Y() < uv[0].Y() && uv[2].Y() < uv[1].Y()) {
		t.Errorf("top corners V %v,%v are not above bottom corners %v,%v",
			uv[3].Y(), uv[2].Y(), uv[0].Y(), uv[1].Y())
	}
	if !(uv[1].X() > uv[0].X() && uv[2].X() > uv[3].X()) {
		t.Errorf("seen from in front, the wall's right side is not at the larger U: %v", uv)
	}

	// Without it, V runs up the wall — the model editor's orientation, which
	// this option must leave alone.
	up, err := AtlasUnwrap(verts, faces, AtlasOptions{PaddingUV: -1})
	if err != nil {
		t.Fatalf("AtlasUnwrap: %v", err)
	}
	if !(up.UVs[0][3].Y() > up.UVs[0][0].Y()) {
		t.Errorf("without VDown the top of the wall should be at the larger V: %v", up.UVs[0])
	}
}

// Faces that merely touch in space — each with its own copy of the corner — are
// not neighbours. Two brushes meeting at a seam are exactly this, and flattening
// them into one island would make the layout depend on where things happen to
// have been placed rather than on what they are.
func TestAtlasOnlyJoinsFacesThatShareVertexIndices(t *testing.T) {
	verts := []mgl32.Vec3{
		{0, 0, 0}, {1, 0, 0}, {1, 0, 1}, {0, 0, 1},
		{1, 0, 0}, {2, 0, 0}, {2, 0, 1}, {1, 0, 1}, // the same edge, as new vertices
	}
	faces := [][]int{{0, 3, 2, 1}, {4, 7, 6, 5}}
	lay, err := AtlasUnwrap(verts, faces, AtlasOptions{})
	if err != nil {
		t.Fatalf("AtlasUnwrap: %v", err)
	}
	if lay.Islands != 2 {
		t.Errorf("islands = %d, want 2 — faces joined through positions, not indices", lay.Islands)
	}
}

// A loop naming a vertex that does not exist is left out, not a panic, and does
// not stop the faces around it being laid out.
func TestAtlasSkipsLoopsOutsideTheVertexPool(t *testing.T) {
	verts, faces := wallQuad()
	faces = append(faces, []int{0, 1, 99})
	lay, err := AtlasUnwrap(verts, faces, AtlasOptions{})
	if err != nil {
		t.Fatalf("AtlasUnwrap: %v", err)
	}
	if lay.UVs[0] == nil {
		t.Error("the usable face was not laid out")
	}
	if lay.UVs[1] != nil {
		t.Errorf("a face with a corner outside the pool got UVs %v", lay.UVs[1])
	}
}

// Scale is what the editor turns into "texels per unit", so it has to be the
// factor the layout was actually drawn at.
func TestAtlasScaleIsTheFactorTheLayoutUsed(t *testing.T) {
	verts, faces := wallQuad()
	lay, err := AtlasUnwrap(verts, faces, AtlasOptions{PaddingUV: -1})
	if err != nil {
		t.Fatalf("AtlasUnwrap: %v", err)
	}
	width := lay.UVs[0][1].X() - lay.UVs[0][0].X()
	if got, want := width, 2*lay.Scale; got < want-1e-5 || got > want+1e-5 {
		t.Errorf("a 2-unit-wide wall is %v wide in UV at scale %v, want %v", got, lay.Scale, want)
	}
}
