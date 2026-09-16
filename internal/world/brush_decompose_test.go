package world

// brush_decompose_test.go — DecomposeConvex, and the IsConvex fix it leans on.

import (
	"testing"

	"foundry/internal/bsp"

	"github.com/go-gl/mathgl/mgl32"
)

// orientedQuad appends a rectangular face to m from four corners given in any
// planar order, choosing the winding that gives it outward. Test-only: real
// authored faces are wound by hand or by the editor's own tools, but getting
// four hand-picked corners' winding right by inspection is exactly the kind of
// thing worth deriving instead of trusting.
func orientedQuad(m *BrushMesh, outward mgl32.Vec3, pts [4]mgl32.Vec3) {
	n := pts[1].Sub(pts[0]).Cross(pts[2].Sub(pts[0]))
	order := pts
	if n.Dot(outward) < 0 {
		order = [4]mgl32.Vec3{pts[0], pts[3], pts[2], pts[1]}
	}
	idx := make([]int, 4)
	for i, p := range order {
		idx[i] = weldVert(m, p)
	}
	m.Faces = append(m.Faces, NewBrushFace(idx, ""))
}

// meshVolume is the signed volume of a closed mesh, by the divergence
// theorem: a fan-triangulation of every face (valid — each face is a planar
// convex n-gon) summed as signed tetrahedra from the origin. Works for any
// origin as long as the mesh is watertight, which is what makes it a useful
// correctness check on a split: the pieces' volumes must sum to the whole's.
func meshVolume(m *BrushMesh) float32 {
	var vol float32
	for fi := range m.Faces {
		pts := m.FacePoints(&m.Faces[fi])
		for i := 1; i < len(pts)-1; i++ {
			vol += pts[0].Dot(pts[i].Cross(pts[i+1]))
		}
	}
	return vol / 6
}

// stepBrush is an L-shaped (step) solid: a wide low box with a narrower tall
// box stacked on its left half, one reflex edge along the step. Every face is
// individually a convex rectangle — the invariant DecomposeConvex assumes —
// while the solid as a whole is not, exactly the shape the docs describe
// authors splitting into two brushes by hand today.
//
//	y
//	2  +--+
//	   |  |
//	1  |  +-----+
//	   |        |
//	0  +--------+
//	   0  1     2   x
//
// Depth (z) runs 0 to 1 throughout.
func stepBrush() BrushMesh {
	var m BrushMesh
	p := func(x, y, z float32) mgl32.Vec3 { return mgl32.Vec3{x, y, z} }

	orientedQuad(&m, mgl32.Vec3{0, -1, 0}, [4]mgl32.Vec3{p(0, 0, 0), p(2, 0, 0), p(2, 0, 1), p(0, 0, 1)})   // bottom
	orientedQuad(&m, mgl32.Vec3{0, 1, 0}, [4]mgl32.Vec3{p(0, 2, 0), p(1, 2, 0), p(1, 2, 1), p(0, 2, 1)})    // top of the tall part
	orientedQuad(&m, mgl32.Vec3{0, 1, 0}, [4]mgl32.Vec3{p(1, 1, 0), p(2, 1, 0), p(2, 1, 1), p(1, 1, 1)})    // step tread
	orientedQuad(&m, mgl32.Vec3{1, 0, 0}, [4]mgl32.Vec3{p(1, 1, 0), p(1, 2, 0), p(1, 2, 1), p(1, 1, 1)})    // riser
	orientedQuad(&m, mgl32.Vec3{-1, 0, 0}, [4]mgl32.Vec3{p(0, 0, 0), p(0, 2, 0), p(0, 2, 1), p(0, 0, 1)})   // left
	orientedQuad(&m, mgl32.Vec3{1, 0, 0}, [4]mgl32.Vec3{p(2, 0, 0), p(2, 1, 0), p(2, 1, 1), p(2, 0, 1)})    // right
	orientedQuad(&m, mgl32.Vec3{0, 0, -1}, [4]mgl32.Vec3{p(0, 0, 0), p(2, 0, 0), p(2, 1, 0), p(0, 1, 0)})   // front, lower
	orientedQuad(&m, mgl32.Vec3{0, 0, -1}, [4]mgl32.Vec3{p(0, 1, 0), p(1, 1, 0), p(1, 2, 0), p(0, 2, 0)})   // front, upper
	orientedQuad(&m, mgl32.Vec3{0, 0, 1}, [4]mgl32.Vec3{p(0, 0, 1), p(2, 0, 1), p(2, 1, 1), p(0, 1, 1)})    // back, lower
	orientedQuad(&m, mgl32.Vec3{0, 0, 1}, [4]mgl32.Vec3{p(0, 1, 1), p(1, 1, 1), p(1, 2, 1), p(0, 2, 1)})    // back, upper
	return m
}

func TestStepBrushIsWatertightAndConcave(t *testing.T) {
	m := stepBrush()
	if m.IsConvex() {
		t.Fatal("stepBrush should be concave — it has a reflex edge along the riser")
	}
	// 2x1 base slab plus 1x1 tower on top, depth 1: (2*1 + 1*1) * 1 = 3.
	if got, want := meshVolume(&m), float32(3); !near(got, want, 1e-3) {
		t.Fatalf("stepBrush volume = %g, want %g — the hand-built fixture itself is wrong", got, want)
	}
}

func TestDecomposeConvexAlreadyConvexIsUnchanged(t *testing.T) {
	b := NewBoxBrush("b", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "concrete")
	pieces, err := DecomposeConvex(b.BrushMesh)
	if err != nil {
		t.Fatalf("DecomposeConvex: %v", err)
	}
	if len(pieces) != 1 {
		t.Fatalf("got %d pieces for an already-convex brush, want 1", len(pieces))
	}
	if len(pieces[0].Verts) != len(b.Verts) || len(pieces[0].Faces) != len(b.Faces) {
		t.Errorf("an already-convex mesh should come back unchanged, got %d verts/%d faces from %d/%d",
			len(pieces[0].Verts), len(pieces[0].Faces), len(b.Verts), len(b.Faces))
	}
}

// DecomposeConvex only ever splits along one of the brush's OWN face planes
// (see the package comment on why), so it is not guaranteed to find the
// geometric minimum: the step's slab genuinely violates the riser's plane at
// its far corner, so the slab comes back as two pieces rather than staying
// whole — still correct (convex, volume-conserving), just not minimal. What
// this test asserts is exactly that correctness, not a specific piece count.
func TestDecomposeConvexSplitsStepIntoConvexPieces(t *testing.T) {
	m := stepBrush()
	pieces, err := DecomposeConvex(m)
	if err != nil {
		t.Fatalf("DecomposeConvex: %v", err)
	}
	if len(pieces) < 2 {
		t.Fatalf("got %d pieces, want at least 2 — the mesh is concave and must be split", len(pieces))
	}

	var total float32
	for i, p := range pieces {
		if !p.IsConvex() {
			t.Errorf("piece %d is not convex", i)
		}
		if len(p.Faces) < 4 {
			t.Errorf("piece %d has only %d faces, too few to bound a volume", i, len(p.Faces))
		}
		total += meshVolume(&p)
	}
	if want := meshVolume(&m); !near(total, want, 1e-2) {
		t.Errorf("piece volumes sum to %g, want %g (the original's) — the split lost or gained volume", total, want)
	}
}

func TestDecomposeConvexBudgetExceededErrors(t *testing.T) {
	m := stepBrush()
	var planes []bsp.Plane
	for fi := range m.Faces {
		if pl, ok := bsp.PlaneFromPoints(m.FacePoints(&m.Faces[fi])); ok {
			planes = append(planes, pl)
		}
	}
	var leaves [][]bsp.Plane
	budget := 0
	if err := partitionPlanes(m.Verts, planes, nil, &leaves, &budget); err == nil {
		t.Fatal("partitionPlanes with a zero budget on a concave mesh should error, not silently give up or loop")
	}
}

// TestIsConvexToleratesLargeSlantedFaces is the regression the float32 fix in
// firstConcavePlane exists for: IsConvex used to test an UNNORMALIZED face
// normal against a fixed epsilon, so a large or slanted face's normal
// (magnitude growing with edge length) blew past that tolerance on rounding
// alone and a legitimately coplanar corner read as a convexity violation. A
// few hundred units is a plausible large brush at this engine's authored
// scale (grid units in tenths — see bsp.OnPlaneEps's own comment).
func TestIsConvexToleratesLargeSlantedFaces(t *testing.T) {
	const s = float32(400)
	// A box, uniformly scaled up and then given one slanted (non-axis-aligned)
	// pair of faces by shearing the top corners sideways — still planar on
	// each face, still convex, just no longer axis-aligned.
	b := NewBoxBrush("big", mgl32.Vec3{}, mgl32.Vec3{s, s, s}, "concrete")
	shear := mgl32.Vec3{s * 0.6, 0, 0}
	for i, v := range b.Verts {
		if v.Y() > 0 {
			b.Verts[i] = v.Add(shear)
		}
	}
	if !b.IsConvex() {
		t.Error("a large, uniformly sheared (still convex) box read as concave — the float32 tolerance is still scaling with face size")
	}
}

func TestConvexBrushPiecesPassesThroughConvexAndDynamic(t *testing.T) {
	convex := NewBoxBrush("box", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "concrete")
	dynamicConcave := Brush{ID: "door", Dynamic: true, Solid: true, BrushMesh: stepBrush()}
	ch := &Chunk{Brushes: []Brush{convex, dynamicConcave}}

	if got := ch.ConvexBrushPieces(0); len(got) != 1 || len(got[0].Verts) != len(convex.Verts) {
		t.Errorf("an already-convex brush should pass through as its own single piece, got %d piece(s)", len(got))
	}
	if got := ch.ConvexBrushPieces(1); len(got) != 1 || len(got[0].Verts) != len(dynamicConcave.Verts) {
		t.Errorf("a Dynamic brush must never be decomposed, even when concave — got %d piece(s)", len(got))
	}
}

func TestConvexBrushPiecesDecomposesAndCachesConcaveSolid(t *testing.T) {
	concave := Brush{ID: "step", Solid: true, BrushMesh: stepBrush()}
	ch := &Chunk{Brushes: []Brush{concave}}

	first := ch.ConvexBrushPieces(0)
	if len(first) < 2 {
		t.Fatalf("a concave solid brush should decompose into multiple pieces, got %d", len(first))
	}
	for _, p := range first {
		if !p.IsConvex() {
			t.Error("ConvexBrushPieces returned a non-convex piece")
		}
	}

	second := ch.ConvexBrushPieces(0)
	if len(second) != len(first) || &second[0] != &first[0] {
		t.Error("a second call before any mesh edit should return the exact cached slice, not recompute")
	}

	MarkBrushMeshesChanged()
	third := ch.ConvexBrushPieces(0)
	if len(third) != len(first) {
		t.Errorf("after invalidation, got %d pieces, want the same decomposition (%d) again", len(third), len(first))
	}
	if &third[0] == &first[0] {
		t.Error("a call after MarkBrushMeshesChanged should have recomputed, not reused the stale slice")
	}
}

func near(a, b, tol float32) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}
