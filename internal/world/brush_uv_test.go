package world

// brush_uv_test.go — a brush face's UVs stated per corner, and the atlas unwrap
// that states them.

import (
	"math"
	"testing"

	"foundry/internal/geom"

	"github.com/go-gl/mathgl/mgl32"
)

// NewBoxBrush's faces, in the order it emits them.
const (
	uvBoxTop  = 3
	uvBoxPosZ = 5
)

func uvNear(a, b mgl32.Vec2) bool { return a.Sub(b).Len() < 1e-4 }

// uvRect is the UV bounding box of a face's stated UVs.
func uvRect(f *BrushFace) (lo, hi mgl32.Vec2) {
	lo = mgl32.Vec2{float32(math.MaxFloat32), float32(math.MaxFloat32)}
	hi = lo.Mul(-1)
	for _, uv := range f.UVs {
		lo = mgl32.Vec2{min(lo.X(), uv.X()), min(lo.Y(), uv.Y())}
		hi = mgl32.Vec2{max(hi.X(), uv.X()), max(hi.Y(), uv.Y())}
	}
	return lo, hi
}

// A painted face is only painted if the layout it was painted against comes
// back: through a save, and through a copy that must not share it.
func TestBrushFaceUVsSurviveSaveLoadAndClone(t *testing.T) {
	b := NewBoxBrush("b", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "")
	want := []mgl32.Vec2{{0.1, 0.2}, {0.3, 0.2}, {0.3, 0.4}, {0.1, 0.4}}
	b.Faces[0].UVs = append([]mgl32.Vec2(nil), want...)

	back, err := BrushFromRaw(BrushToRaw(&b))
	if err != nil {
		t.Fatalf("BrushFromRaw: %v", err)
	}
	if len(back.Faces[0].UVs) != len(want) {
		t.Fatalf("loaded %d UVs, saved %d", len(back.Faces[0].UVs), len(want))
	}
	for i := range want {
		if back.Faces[0].UVs[i] != want[i] {
			t.Errorf("corner %d: loaded %v, saved %v", i, back.Faces[0].UVs[i], want[i])
		}
	}
	if back.Faces[1].UVs != nil {
		t.Errorf("a face that was never unwrapped came back with UVs %v", back.Faces[1].UVs)
	}

	c := CloneBrush(&b, "_copy")
	c.Faces[0].UVs[0] = mgl32.Vec2{9, 9}
	if b.Faces[0].UVs[0] != want[0] {
		t.Error("CloneBrush shares the UV slice with the original")
	}
}

// A face whose UVs no longer have one entry per corner — an edit rewired its
// loop — is drawn from its projection as if it had none, and saves none.
func TestMismatchedUVsFallBackToTheProjection(t *testing.T) {
	b := NewBoxBrush("b", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "")
	f := &b.Faces[0]
	want := b.FaceUVs(f)
	f.UVs = []mgl32.Vec2{{0.5, 0.5}, {0.6, 0.5}, {0.6, 0.6}} // three, for four corners

	got := b.FaceUVs(f)
	for i := range want {
		if !uvNear(got[i], want[i]) {
			t.Errorf("corner %d draws at %v, want the projection's %v", i, got[i], want[i])
		}
	}
	if raw := BrushToRaw(&b); raw.Faces[0].UVs != nil {
		t.Errorf("mismatched UVs were saved: %v", raw.Faces[0].UVs)
	}
}

// Stated UVs replace the projection, and the face's offset still moves them —
// the panel's fields have to mean the same thing on a painted face.
func TestStatedUVsAreDrawnWithTheTransformOnTop(t *testing.T) {
	b := NewBoxBrush("b", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "")
	f := &b.Faces[0]
	stated := []mgl32.Vec2{{0.1, 0.2}, {0.3, 0.2}, {0.3, 0.4}, {0.1, 0.4}}
	f.UVs = stated
	f.UVOffset = mgl32.Vec2{1, 0.5}

	got := b.FaceUVs(f)
	for i := range stated {
		if want := stated[i].Add(f.UVOffset); !uvNear(got[i], want) {
			t.Errorf("corner %d draws at %v, want %v", i, got[i], want)
		}
	}
}

// A double-sided face's reverse copy is a second set of vertices, not a second
// view of the first. If the two shared their storage, the UV transform — which
// walks every vertex — would move the shared ones twice, and a double-sided face
// with an offset would draw at twice the offset (or scaled twice, or turned
// twice) on both sides.
func TestDoubleSidedFaceTakesItsUVTransformOnce(t *testing.T) {
	pts := []mgl32.Vec3{{0, 0, 0}, {1, 0, 0}, {1, 0, 1}, {0, 0, 1}}
	single, _ := BuildBrushFaceMesh(pts, false)
	double, _ := BuildBrushFaceMesh(pts, true)
	if len(double) != 2*len(single) {
		t.Fatalf("double-sided mesh has %d vertices, want %d", len(double), 2*len(single))
	}

	offset, scale, turn := mgl32.Vec2{0.25, 0.5}, mgl32.Vec2{2, 3}, float32(0.3)
	ApplyUVTransform(single, offset, scale, turn)
	ApplyUVTransform(double, offset, scale, turn)

	n := len(single)
	for i := 0; i < len(double); i++ {
		want := mgl32.Vec2{single[i%n][3], single[i%n][4]}
		got := mgl32.Vec2{double[i][3], double[i][4]}
		if !uvNear(got, want) {
			t.Errorf("vertex %d (side %d) has UV %v, want %v — transformed more than once",
				i, i/n, got, want)
		}
	}
}

// What an unwrap writes is an affine function of position on a flat face, so
// the compiler's map must reproduce it exactly — at the corners it was fitted
// to, and everywhere between them, which is where a cut face's pieces are.
func TestFitUVMapReproducesAnAffineLayout(t *testing.T) {
	// A sloped quad, off every axis the fit might otherwise get lucky on.
	pts := []mgl32.Vec3{{0, 0, 0}, {2, 1, 0}, {2, 1, 2}, {0, 0, 2}}
	uvAt := func(p mgl32.Vec3) mgl32.Vec2 {
		return mgl32.Vec2{0.3*p.X() + 0.1*p.Z() + 0.05, -0.2*p.X() + 0.4*p.Z() + 0.5}
	}
	uvs := make([]mgl32.Vec2, len(pts))
	for i, p := range pts {
		uvs[i] = uvAt(p)
	}

	m, ok := FitUVMap(pts, uvs)
	if !ok {
		t.Fatal("FitUVMap refused a flat quad")
	}
	for i, p := range pts {
		if got := m.At(p); !uvNear(got, uvs[i]) {
			t.Errorf("corner %d: map gives %v, stated %v", i, got, uvs[i])
		}
	}
	mid := mgl32.Vec3{1, 0.5, 1}
	if got, want := m.At(mid), uvAt(mid); !uvNear(got, want) {
		t.Errorf("mid-face: map gives %v, want %v", got, want)
	}
}

// Corners on a line have no plane to fit a map in.
func TestFitUVMapRefusesAFaceWithNoArea(t *testing.T) {
	pts := []mgl32.Vec3{{0, 0, 0}, {1, 0, 0}, {2, 0, 0}}
	uvs := []mgl32.Vec2{{0, 0}, {0.5, 0}, {1, 0}}
	if _, ok := FitUVMap(pts, uvs); ok {
		t.Error("FitUVMap accepted collinear corners")
	}
}

// atlasFacesOf names every face of every brush, untransformed.
func atlasFacesOf(brushes []Brush) []AtlasFace {
	var out []AtlasFace
	for bi := range brushes {
		for fi := range brushes[bi].Faces {
			out = append(out, AtlasFace{Mesh: &brushes[bi].BrushMesh, Face: fi})
		}
	}
	return out
}

// Two boxes touching at a wall: every face gets a region of its own, inside the
// texture, with no two overlapping — and the faces of two different brushes
// stay apart even where they meet, because they share no corners.
func TestBrushAtlasGivesEveryFaceItsOwnRegion(t *testing.T) {
	brushes := []Brush{
		NewBoxBrush("a", mgl32.Vec3{1, 1, 1}, mgl32.Vec3{1, 1, 1}, ""),
		NewBoxBrush("b", mgl32.Vec3{3, 1, 1}, mgl32.Vec3{1, 1, 1}, ""),
	}
	faces := atlasFacesOf(brushes)
	lay, err := LayoutBrushAtlas(faces, geom.AtlasOptions{})
	if err != nil {
		t.Fatalf("LayoutBrushAtlas: %v", err)
	}
	if lay.Islands != 12 {
		t.Errorf("islands = %d, want 12 — one per side of two boxes", lay.Islands)
	}
	if brushes[0].Faces[0].UVs != nil {
		t.Fatal("LayoutBrushAtlas wrote to the faces; only ApplyBrushAtlas may")
	}

	brushes[0].Faces[0].UVOffset = mgl32.Vec2{3, 3}
	if n := ApplyBrushAtlas(faces, lay); n != 12 {
		t.Fatalf("ApplyBrushAtlas wrote %d faces, want 12", n)
	}
	if brushes[0].Faces[0].UVOffset != (mgl32.Vec2{}) {
		t.Error("an unwrapped face kept its UV offset, which pushes it off its island")
	}

	type rect struct{ lo, hi mgl32.Vec2 }
	var rects []rect
	for bi := range brushes {
		for fi := range brushes[bi].Faces {
			f := &brushes[bi].Faces[fi]
			if !f.HasUVs() {
				t.Fatalf("brush %d face %d has no UVs after the unwrap", bi, fi)
			}
			lo, hi := uvRect(f)
			if lo.X() < 0 || lo.Y() < 0 || hi.X() > 1 || hi.Y() > 1 {
				t.Errorf("brush %d face %d spans %v..%v, outside the texture", bi, fi, lo, hi)
			}
			rects = append(rects, rect{lo, hi})
		}
	}
	for a := range rects {
		for b := a + 1; b < len(rects); b++ {
			ra, rb := rects[a], rects[b]
			if ra.lo.X() < rb.hi.X() && rb.lo.X() < ra.hi.X() &&
				ra.lo.Y() < rb.hi.Y() && rb.lo.Y() < ra.hi.Y() {
				t.Errorf("regions %d %v and %d %v overlap", a, ra, b, rb)
			}
		}
	}
}

// Texel density is measured through the chunk's transform: the same face in a
// chunk at twice the scale is twice the wall on each axis, and owes four times
// the texture area.
func TestBrushAtlasMeasuresFacesInTheirChunksUnits(t *testing.T) {
	a := NewBoxBrush("a", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "")
	b := NewBoxBrush("b", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "")
	faces := []AtlasFace{
		{Mesh: &a.BrushMesh, Face: uvBoxTop},
		{Mesh: &b.BrushMesh, Face: uvBoxTop, Xform: mgl32.Scale3D(2, 2, 2)},
	}
	lay, err := LayoutBrushAtlas(faces, geom.AtlasOptions{})
	if err != nil {
		t.Fatalf("LayoutBrushAtlas: %v", err)
	}
	ApplyBrushAtlas(faces, lay)
	area := func(f *BrushFace) float32 {
		lo, hi := uvRect(f)
		return (hi.X() - lo.X()) * (hi.Y() - lo.Y())
	}
	if ratio := area(&b.Faces[uvBoxTop]) / area(&a.Faces[uvBoxTop]); math.Abs(float64(ratio-4)) > 1e-3 {
		t.Errorf("area ratio = %v, want 4 — the chunk's scale was not measured", ratio)
	}
}

// A wall has to hang the right way up on the canvas: its top edge at the top of
// its island, and its right-hand side (seen from in front) on the right.
func TestBrushAtlasHangsWallsTheRightWayUp(t *testing.T) {
	b := NewBoxBrush("b", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "")
	faces := []AtlasFace{{Mesh: &b.BrushMesh, Face: uvBoxPosZ}}
	lay, err := LayoutBrushAtlas(faces, geom.AtlasOptions{})
	if err != nil {
		t.Fatalf("LayoutBrushAtlas: %v", err)
	}
	ApplyBrushAtlas(faces, lay)

	// The +Z face's loop, seen from +Z: bottom-left, bottom-right, top-right,
	// top-left.
	uv := b.Faces[uvBoxPosZ].UVs
	if !(uv[3].Y() < uv[0].Y() && uv[2].Y() < uv[1].Y()) {
		t.Errorf("the wall's top edge is not above its bottom edge in the image: %v", uv)
	}
	if !(uv[1].X() > uv[0].X() && uv[2].X() > uv[3].X()) {
		t.Errorf("the wall comes out mirrored: %v", uv)
	}
}
