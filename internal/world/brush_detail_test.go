package world

// brush_detail_test.go — the detail mesh as data: what a brush is drawn from,
// what a copy of one carries, and what survives a save.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// detailedBox is a box brush with a quad of detail standing a quarter unit off
// its top face.
func detailedBox() Brush {
	b := NewBoxBrush("b", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{1, 1, 1}, "concrete")
	b.SeedDetail()
	base := len(b.Detail.Verts)
	b.Detail.Verts = append(b.Detail.Verts,
		mgl32.Vec3{-0.5, 1.25, -0.5}, mgl32.Vec3{0.5, 1.25, -0.5},
		mgl32.Vec3{0.5, 1.25, 0.5}, mgl32.Vec3{-0.5, 1.25, 0.5})
	f := NewBrushFace([]int{base, base + 3, base + 2, base + 1}, "metal")
	f.Alpha = 0.5
	f.Smooth = true
	b.Detail.Faces = append(b.Detail.Faces, f)
	return b
}

func TestDrawFacesPrefersTheDetailMesh(t *testing.T) {
	plain := NewBoxBrush("p", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "concrete")
	if got := plain.DrawMesh(); got != &plain.BrushMesh {
		t.Error("a brush with no detail mesh is not drawn from its own faces")
	}

	b := detailedBox()
	if got := b.DrawMesh(); got != b.Detail {
		t.Error("a detailed brush is not drawn from its detail mesh")
	}
	if len(b.DrawFaces()) != len(b.Faces)+1 {
		t.Errorf("DrawFaces returned %d faces; the detail mesh has %d",
			len(b.DrawFaces()), len(b.Detail.Faces))
	}

	// An EMPTY detail mesh does not count: a brush drawn from nothing at all
	// is indistinguishable from a bug, so it falls back to the solid.
	b.Detail.Faces = nil
	if got := b.DrawMesh(); got != &b.BrushMesh {
		t.Error("a brush whose detail mesh has no faces should draw as the solid it is")
	}
}

// TestLocalAABBCoversTheDetailMesh is what keeps a bump on a wall from being
// culled the moment the wall itself leaves the frustum.
func TestLocalAABBCoversTheDetailMesh(t *testing.T) {
	b := detailedBox()
	// Something well outside the hull, which spans -1..1 on every axis.
	b.Detail.Verts = append(b.Detail.Verts, mgl32.Vec3{0, 4, 0})
	_, hi := b.LocalAABB()
	if hi.Y() < 4 {
		t.Errorf("bounds top out at y=%v; the detail reaches y=4", hi.Y())
	}
}

func TestCloneBrushDeepCopiesTheDetailMesh(t *testing.T) {
	b := detailedBox()
	c := CloneBrush(&b, "_copy")
	if c.Detail == nil {
		t.Fatal("the copy has no detail mesh")
	}
	if c.Detail == b.Detail {
		t.Fatal("the copy shares the original's detail mesh")
	}
	c.Detail.Verts[0] = mgl32.Vec3{99, 99, 99}
	c.Detail.Faces[0].Indices[0] = 7
	if b.Detail.Verts[0] == c.Detail.Verts[0] || b.Detail.Faces[0].Indices[0] == 7 {
		t.Error("editing the copy's detail mesh reached the original")
	}
}

func TestDetailMeshRoundTripsThroughRaw(t *testing.T) {
	b := detailedBox()
	back, err := BrushFromRaw(BrushToRaw(&b))
	if err != nil {
		t.Fatalf("BrushFromRaw: %v", err)
	}
	if back.Detail == nil {
		t.Fatal("the detail mesh did not survive the round trip")
	}
	if len(back.Detail.Verts) != len(b.Detail.Verts) ||
		len(back.Detail.Faces) != len(b.Detail.Faces) {
		t.Fatalf("came back as %d verts/%d faces, was %d/%d",
			len(back.Detail.Verts), len(back.Detail.Faces),
			len(b.Detail.Verts), len(b.Detail.Faces))
	}
	for i := range b.Detail.Verts {
		if back.Detail.Verts[i] != b.Detail.Verts[i] {
			t.Fatalf("detail vertex %d came back as %v, was %v",
				i, back.Detail.Verts[i], b.Detail.Verts[i])
		}
	}
	last := len(b.Detail.Faces) - 1
	got, want := back.Detail.Faces[last], b.Detail.Faces[last]
	if got.Texture != want.Texture || got.Alpha != want.Alpha || got.Smooth != want.Smooth {
		t.Errorf("the detail face came back as %+v, was %+v", got, want)
	}

	// A brush with no detail writes no detail block at all, so a level that
	// never used the feature round-trips byte-identically.
	plain := NewBoxBrush("p", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "concrete")
	if raw := BrushToRaw(&plain); raw.Detail != nil {
		t.Error("a plain brush wrote a detail block")
	}
}

// TestClearAndResetDetail covers the two ways out of a detail mesh that has
// gone wrong, which the editor's panel offers side by side.
func TestClearAndResetDetail(t *testing.T) {
	b := detailedBox()
	carved := len(b.Detail.Faces)

	b.ResetDetail()
	if b.Detail == nil {
		t.Fatal("re-seeding left the brush with no detail mesh")
	}
	if len(b.Detail.Faces) != len(b.Faces) {
		t.Errorf("re-seeded mesh has %d faces; the hull has %d (it had %d carved)",
			len(b.Detail.Faces), len(b.Faces), carved)
	}

	if !b.ClearDetail() || b.Detail != nil {
		t.Error("clearing left a detail mesh behind")
	}
	if b.ClearDetail() {
		t.Error("clearing a brush that has no detail mesh reported a change")
	}
}

// TestEntityBrushesEqualSeesDetailEdits: the entity editor copies its live
// brushes into the document only when this says they differ, so a Shape Detail
// edit it calls equal is one Save never writes — which is how widening a
// ship's window in detail mode came back narrow on reopen.
func TestEntityBrushesEqualSeesDetailEdits(t *testing.T) {
	doc := []Brush{detailedBox()}
	if !EntityBrushesEqual(doc, CloneEntityBrushes(doc)) {
		t.Fatal("a clone of a detailed brush compares unequal")
	}

	moved := CloneEntityBrushes(doc)
	moved[0].Detail.Verts[0] = mgl32.Vec3{9, 9, 9}
	if EntityBrushesEqual(doc, moved) {
		t.Error("a moved detail vertex compares equal")
	}

	retextured := CloneEntityBrushes(doc)
	retextured[0].Detail.Faces[0].Texture = "glass"
	if EntityBrushesEqual(doc, retextured) {
		t.Error("a retextured detail face compares equal")
	}

	tagged := CloneEntityBrushes(doc)
	tagged[0].Tags = []string{"window"}
	if EntityBrushesEqual(doc, tagged) {
		t.Error("a brush given a tag compares equal")
	}
}
