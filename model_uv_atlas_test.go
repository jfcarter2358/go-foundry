package main

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// uvBounds returns the axis-aligned bounds of a face's UVs.
func uvBounds(f *ModelFace) (min, max mgl32.Vec2) {
	min = mgl32.Vec2{float32(math.MaxFloat32), float32(math.MaxFloat32)}
	max = mgl32.Vec2{-float32(math.MaxFloat32), -float32(math.MaxFloat32)}
	for _, uv := range f.UVs {
		min[0], max[0] = minF(min[0], uv.X()), maxF(max[0], uv.X())
		min[1], max[1] = minF(min[1], uv.Y()), maxF(max[1], uv.Y())
	}
	return min, max
}

func minF(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func maxF(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

// uvRectsOverlap reports whether two half-open UV boxes intersect.
func uvRectsOverlap(aMin, aMax, bMin, bMax mgl32.Vec2) bool {
	return aMin.X() < bMax.X() && bMin.X() < aMax.X() &&
		aMin.Y() < bMax.Y() && bMin.Y() < aMax.Y()
}

// A cube's six sides differ by 90°, so the default 60° limit cuts every one of
// them into its own island — the case that most obviously has to work, since a
// cube is what the editor opens with.
func TestAtlasUnwrapCubeGivesSixIslands(t *testing.T) {
	m := NewCubeDoc()
	n, err := m.AtlasUnwrap(modelAllFaceIndicesOf(m), AtlasUnwrapOptions{})
	if err != nil {
		t.Fatalf("AtlasUnwrap: %v", err)
	}
	if n != 6 {
		t.Errorf("islands = %d, want 6 (one per cube side)", n)
	}
}

// Every UV must land inside the texture. A coordinate outside 0..1 samples a
// wrapped or clamped texel that belongs to some other face, which is exactly
// the aliasing the atlas exists to prevent.
func TestAtlasUnwrapStaysInUnitSquare(t *testing.T) {
	m := NewCubeDoc()
	if _, err := m.AtlasUnwrap(modelAllFaceIndicesOf(m), AtlasUnwrapOptions{}); err != nil {
		t.Fatalf("AtlasUnwrap: %v", err)
	}
	for fi := range m.Faces {
		for i, uv := range m.Faces[fi].UVs {
			if uv.X() < 0 || uv.X() > 1 || uv.Y() < 0 || uv.Y() > 1 {
				t.Errorf("face %d corner %d UV %v is outside 0..1", fi, i, uv)
			}
		}
	}
}

// The whole point: no two islands may claim the same texels. A cube's faces are
// each their own island, so comparing face bounding boxes is comparing island
// bounding boxes.
func TestAtlasUnwrapIslandsDoNotOverlap(t *testing.T) {
	m := NewCubeDoc()
	if _, err := m.AtlasUnwrap(modelAllFaceIndicesOf(m), AtlasUnwrapOptions{}); err != nil {
		t.Fatalf("AtlasUnwrap: %v", err)
	}
	type box struct{ min, max mgl32.Vec2 }
	boxes := make([]box, len(m.Faces))
	for fi := range m.Faces {
		lo, hi := uvBounds(&m.Faces[fi])
		boxes[fi] = box{lo, hi}
	}
	for a := range boxes {
		for b := a + 1; b < len(boxes); b++ {
			if uvRectsOverlap(boxes[a].min, boxes[a].max, boxes[b].min, boxes[b].max) {
				t.Errorf("islands %d %v and %d %v overlap",
					a, boxes[a], b, boxes[b])
			}
		}
	}
}

// Islands must be held apart by at least the gutter, or bilinear filtering and
// the mip chain bleed one island's colour into its neighbour.
func TestAtlasUnwrapKeepsGutterBetweenIslands(t *testing.T) {
	m := NewCubeDoc()
	const pad = float32(0.05)
	if _, err := m.AtlasUnwrap(modelAllFaceIndicesOf(m), AtlasUnwrapOptions{PaddingUV: pad}); err != nil {
		t.Fatalf("AtlasUnwrap: %v", err)
	}
	type box struct{ min, max mgl32.Vec2 }
	boxes := make([]box, len(m.Faces))
	for fi := range m.Faces {
		lo, hi := uvBounds(&m.Faces[fi])
		boxes[fi] = box{lo, hi}
	}
	for a := range boxes {
		// Every island is at least a gutter away from the atlas edge too.
		if boxes[a].min.X() < pad-1e-5 || boxes[a].min.Y() < pad-1e-5 {
			t.Errorf("island %d starts at %v, inside the %v edge gutter", a, boxes[a].min, pad)
		}
		for b := a + 1; b < len(boxes); b++ {
			// Two boxes separated on either axis are fine; the gap on that axis
			// is what has to clear 2*pad (a gutter from each).
			gapX := maxF(boxes[a].min.X(), boxes[b].min.X()) - minF(boxes[a].max.X(), boxes[b].max.X())
			gapY := maxF(boxes[a].min.Y(), boxes[b].min.Y()) - minF(boxes[a].max.Y(), boxes[b].max.Y())
			if maxF(gapX, gapY) < 2*pad-1e-5 {
				t.Errorf("islands %d and %d are %v/%v apart, want at least %v",
					a, b, gapX, gapY, 2*pad)
			}
		}
	}
}

// Texel density is uniform: a face twice the size gets twice the UV extent. A
// per-island normalisation would give both the same area, which is the bug this
// guards against.
func TestAtlasUnwrapKeepsUniformTexelDensity(t *testing.T) {
	// Two coplanar quads in separate islands — far apart in space and pointing
	// opposite ways, so no edge joins them. The second is twice as wide.
	m := &ModelDoc{
		Verts: []mgl32.Vec3{
			{0, 0, 0}, {1, 0, 0}, {1, 0, 1}, {0, 0, 1},
			{10, 0, 0}, {12, 0, 0}, {12, 0, 1}, {10, 0, 1},
		},
		Faces: []ModelFace{
			{Verts: []int{0, 1, 2, 3}, UVs: make([]mgl32.Vec2, 4)},
			{Verts: []int{4, 5, 6, 7}, UVs: make([]mgl32.Vec2, 4)},
		},
	}
	m.invalidate()
	if _, err := m.AtlasUnwrap([]int{0, 1}, AtlasUnwrapOptions{PaddingUV: -1}); err != nil {
		t.Fatalf("AtlasUnwrap: %v", err)
	}
	// Compare areas, not one axis: which world axis the projection basis sends
	// to U and which to V depends on the island's normal, so an axis-specific
	// assertion would be testing geom.PerpAxis rather than the packing.
	area := func(fi int) float32 {
		lo, hi := uvBounds(&m.Faces[fi])
		return (hi.X() - lo.X()) * (hi.Y() - lo.Y())
	}
	aA, aB := area(0), area(1)
	if aA <= 0 || aB <= 0 {
		t.Fatalf("degenerate UV areas: %v, %v", aA, aB)
	}
	if ratio := aB / aA; math.Abs(float64(ratio-2)) > 1e-3 {
		t.Errorf("UV area ratio = %v, want 2 — texel density is not uniform", ratio)
	}
}

// Adjacent faces within the angle limit share an island, and where they meet
// they must agree on the UV of the vertices they share — otherwise a brush
// stroke tears at every interior edge.
func TestAtlasUnwrapIslandIsContinuousAcrossInteriorEdges(t *testing.T) {
	// Two coplanar quads sharing the edge 1-2.
	m := &ModelDoc{
		Verts: []mgl32.Vec3{
			{0, 0, 0}, {1, 0, 0}, {1, 0, 1}, {0, 0, 1}, {2, 0, 0}, {2, 0, 1},
		},
		Faces: []ModelFace{
			{Verts: []int{0, 1, 2, 3}, UVs: make([]mgl32.Vec2, 4)},
			{Verts: []int{1, 4, 5, 2}, UVs: make([]mgl32.Vec2, 4)},
		},
	}
	m.invalidate()
	n, err := m.AtlasUnwrap([]int{0, 1}, AtlasUnwrapOptions{})
	if err != nil {
		t.Fatalf("AtlasUnwrap: %v", err)
	}
	if n != 1 {
		t.Fatalf("islands = %d, want 1 — coplanar neighbours must not be cut apart", n)
	}
	// Vertex 1 is corner 1 of face 0 and corner 0 of face 1; vertex 2 is
	// corner 2 of face 0 and corner 3 of face 1.
	for _, pair := range [][4]int{{0, 1, 1, 0}, {0, 2, 1, 3}} {
		a := m.Faces[pair[0]].UVs[pair[1]]
		b := m.Faces[pair[2]].UVs[pair[3]]
		if a.Sub(b).Len() > 1e-6 {
			t.Errorf("shared vertex mapped to %v and %v — the island is torn", a, b)
		}
	}
}

// Unwrapping the same mesh twice must produce the same layout, or a re-unwrap
// would scramble whatever had been painted against the previous one.
func TestAtlasUnwrapIsDeterministic(t *testing.T) {
	for i := 0; i < 8; i++ {
		a := NewCubeDoc()
		b := NewCubeDoc()
		if _, err := a.AtlasUnwrap(modelAllFaceIndicesOf(a), AtlasUnwrapOptions{}); err != nil {
			t.Fatalf("AtlasUnwrap: %v", err)
		}
		if _, err := b.AtlasUnwrap(modelAllFaceIndicesOf(b), AtlasUnwrapOptions{}); err != nil {
			t.Fatalf("AtlasUnwrap: %v", err)
		}
		for fi := range a.Faces {
			for c := range a.Faces[fi].UVs {
				if a.Faces[fi].UVs[c] != b.Faces[fi].UVs[c] {
					t.Fatalf("run %d: face %d corner %d: %v vs %v",
						i, fi, c, a.Faces[fi].UVs[c], b.Faces[fi].UVs[c])
				}
			}
		}
	}
}

// A tighter angle limit cuts more seams; a looser one cuts fewer. This is the
// one knob the dialog exposes, so its direction has to be right.
func TestAtlasUnwrapAngleLimitControlsIslandCount(t *testing.T) {
	m := NewPrimitiveDoc(ModelPrimSphere, 8)
	tight, err := m.Clone().AtlasUnwrap(modelAllFaceIndicesOf(m), AtlasUnwrapOptions{AngleLimit: 5 * math.Pi / 180})
	if err != nil {
		t.Fatalf("tight: %v", err)
	}
	loose, err := m.Clone().AtlasUnwrap(modelAllFaceIndicesOf(m), AtlasUnwrapOptions{AngleLimit: 175 * math.Pi / 180})
	if err != nil {
		t.Fatalf("loose: %v", err)
	}
	if tight <= loose {
		t.Errorf("tight limit gave %d islands, loose gave %d — want tight > loose", tight, loose)
	}
	if loose != 1 {
		t.Errorf("a 175° limit gave %d islands, want 1 (everything joins)", loose)
	}
}

// A mesh with nothing projectable must say so rather than silently reporting a
// successful unwrap onto a blank texture.
func TestAtlasUnwrapRejectsUnprojectableMesh(t *testing.T) {
	m := &ModelDoc{
		Verts: []mgl32.Vec3{{0, 0, 0}, {1, 0, 0}},
		Faces: []ModelFace{{Verts: []int{0, 1}, UVs: make([]mgl32.Vec2, 2)}},
	}
	m.invalidate()
	if _, err := m.AtlasUnwrap([]int{0}, AtlasUnwrapOptions{}); err == nil {
		t.Error("unwrapping a mesh with no projectable face reported success")
	}
}

// A face that cannot be projected must be left exactly as it was, not written
// with garbage or NaN.
func TestAtlasUnwrapLeavesDegenerateFacesAlone(t *testing.T) {
	m := NewCubeDoc()
	m.Faces = append(m.Faces, ModelFace{
		Verts: []int{0, 1},
		UVs:   []mgl32.Vec2{{0.25, 0.5}, {0.75, 0.5}},
	})
	m.invalidate()
	if _, err := m.AtlasUnwrap(modelAllFaceIndicesOf(m), AtlasUnwrapOptions{}); err != nil {
		t.Fatalf("AtlasUnwrap: %v", err)
	}
	got := m.Faces[len(m.Faces)-1].UVs
	if got[0] != (mgl32.Vec2{0.25, 0.5}) || got[1] != (mgl32.Vec2{0.75, 0.5}) {
		t.Errorf("degenerate face UVs were rewritten to %v", got)
	}
}

// Unwrapping must never leave a NaN behind: a NaN UV is not caught by a
// 0..1 range check and renders as a garbage texel or nothing at all.
func TestAtlasUnwrapProducesNoNaN(t *testing.T) {
	for _, kind := range modelPrimitiveKinds {
		m := NewPrimitiveDoc(kind, 8)
		if _, err := m.AtlasUnwrap(modelAllFaceIndicesOf(m), AtlasUnwrapOptions{}); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		for fi := range m.Faces {
			for i, uv := range m.Faces[fi].UVs {
				if math.IsNaN(float64(uv.X())) || math.IsNaN(float64(uv.Y())) {
					t.Errorf("%s face %d corner %d: NaN UV %v", kind, fi, i, uv)
				}
			}
		}
	}
}

// Aspect keeps a texel square on a non-square atlas: at 2:1, a shape's UV
// height must be twice its width for the same world extent.
func TestAtlasUnwrapAspectCompensatesNonSquareTexture(t *testing.T) {
	square := func() *ModelDoc {
		m := &ModelDoc{
			Verts: []mgl32.Vec3{{0, 0, 0}, {1, 0, 0}, {1, 0, 1}, {0, 0, 1}},
			Faces: []ModelFace{{Verts: []int{0, 1, 2, 3}, UVs: make([]mgl32.Vec2, 4)}},
		}
		m.invalidate()
		return m
	}
	m := square()
	if _, err := m.AtlasUnwrap([]int{0}, AtlasUnwrapOptions{PaddingUV: -1, Aspect: 2}); err != nil {
		t.Fatalf("AtlasUnwrap: %v", err)
	}
	lo, hi := uvBounds(&m.Faces[0])
	w, h := hi.X()-lo.X(), hi.Y()-lo.Y()
	if w <= 0 {
		t.Fatalf("degenerate width %v", w)
	}
	if ratio := h / w; math.Abs(float64(ratio-2)) > 1e-3 {
		t.Errorf("height/width = %v on a 2:1 atlas, want 2", ratio)
	}
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
			if uvRectsOverlap(aMin, aMax, bMin, bMax) {
				t.Errorf("packed rects %d and %d overlap", a, b)
			}
		}
	}
}
