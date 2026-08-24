package main

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// uvVSpan is the V range a face's mapping covers. Zero means the texture is
// stretched infinitely along V — and that no V scale can ever fix it, since
// scaling zero leaves zero.
func uvVSpan(f *ModelFace) float32 {
	if len(f.UVs) == 0 {
		return 0
	}
	minV, maxV := f.UVs[0].Y(), f.UVs[0].Y()
	for _, uv := range f.UVs[1:] {
		minV = min(minV, uv.Y())
		maxV = max(maxV, uv.Y())
	}
	return maxV - minV
}

func TestExtrudeByDistanceGivesSidesRealV(t *testing.T) {
	m := NewCubeDoc()
	top := cubeTopFace(t, m)

	_, sides := m.ExtrudeFaces([]int{top}, 2)

	if len(sides) != 4 {
		t.Fatalf("got %d side quads, want 4", len(sides))
	}
	for _, fi := range sides {
		if got := uvVSpan(&m.Faces[fi]); math.Abs(float64(got-2)) > 1e-5 {
			t.Errorf("side %d spans %v in V, want the extruded depth 2", fi, got)
		}
		// U follows the edge, which on a unit cube is 1 long.
		f := &m.Faces[fi]
		if got := f.UVs[1].X(); math.Abs(float64(got-1)) > 1e-5 {
			t.Errorf("side %d spans %v in U, want the edge length 1", fi, got)
		}
	}
}

// The reported bug: press E, drag out the depth, then try to scale V.
func TestInteractiveExtrudeSidesFollowTheDrag(t *testing.T) {
	m := NewCubeDoc()
	withModelDoc(t, m)
	withModelViewport(t)
	withGridSnap(t, false, 0.1)

	top := cubeTopFace(t, m)
	modelSelection.Mode = modelSelFace
	modelSelection.Faces = []int{top}

	modelStartExtrude()
	sides := modelPendingExtrudeSides()
	if len(sides) != 4 {
		t.Fatalf("pending extrude tracked %d sides, want 4", len(sides))
	}
	// Extruding by zero legitimately leaves a zero span; the drag is what has
	// to fix it.
	for _, fi := range sides {
		if got := uvVSpan(&m.Faces[fi]); got != 0 {
			t.Errorf("side %d already spans %v in V before the drag", fi, got)
		}
	}

	// Pull the cap up 1.5 units, the way the gizmo does.
	modelNudgeSelection(1, 1.5)

	for _, fi := range sides {
		if got := uvVSpan(&m.Faces[fi]); math.Abs(float64(got-1.5)) > 1e-4 {
			t.Errorf("side %d spans %v in V after a 1.5 drag, want 1.5", fi, got)
		}
	}
}

func TestScalingVOnAnExtrudedSideActuallyScales(t *testing.T) {
	m := NewCubeDoc()
	withModelDoc(t, m)
	withModelViewport(t)
	withGridSnap(t, false, 0.1)

	top := cubeTopFace(t, m)
	modelSelection.Mode = modelSelFace
	modelSelection.Faces = []int{top}
	modelStartExtrude()
	sides := append([]int(nil), modelPendingExtrudeSides()...)
	modelNudgeSelection(1, 1.5)

	// Select one side and scale its V by 2, as the UV panel does.
	side := sides[0]
	modelSelection.Faces = []int{side}
	modelUVInvalidate()
	modelUVSync(modelSelection.Faces)
	before := uvVSpan(&m.Faces[side])
	if before == 0 {
		t.Fatal("the side still has no V range to scale")
	}

	modelUVEdit.Xform.Scale = mgl32.Vec2{1, 2}
	modelUVApply(modelSelection.Faces)

	if got := uvVSpan(&m.Faces[side]); math.Abs(float64(got-before*2)) > 1e-4 {
		t.Errorf("V span %v -> %v, want it doubled to %v", before, got, before*2)
	}
}

func TestPendingExtrudeExpiresWithTheSelection(t *testing.T) {
	m := NewCubeDoc()
	withModelDoc(t, m)
	withModelViewport(t)
	withGridSnap(t, false, 0.1)

	top := cubeTopFace(t, m)
	modelSelection.Mode = modelSelFace
	modelSelection.Faces = []int{top}
	modelStartExtrude()
	sides := append([]int(nil), modelPendingExtrudeSides()...)
	modelNudgeSelection(1, 1.5)

	// Move on to some other face and hand-set its mapping, then drag it. The
	// stale extrude must not reach back and rewrite anything.
	other := sides[0]
	modelSelection.Faces = []int{other}
	if modelPendingExtrudeSides() != nil {
		t.Fatal("the pending extrude survived a selection change")
	}
	m.Faces[other].UVs = []mgl32.Vec2{{0, 0}, {3, 0}, {3, 7}, {0, 7}}

	modelNudgeSelection(0, 0.5)

	if got := uvVSpan(&m.Faces[other]); math.Abs(float64(got-7)) > 1e-5 {
		t.Errorf("hand-set V span became %v; a stale extrude rewrote it", got)
	}
}

func TestRefreshSkipsFacesThatAreNotQuads(t *testing.T) {
	// A subdivided side is no longer the four-cornered quad the U-along-edge,
	// V-across-depth convention describes, so the refresh has to leave anything
	// that is not a quad exactly as it found it.
	m := &ModelDoc{
		Verts: []mgl32.Vec3{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}},
		Faces: []ModelFace{{
			Verts:   []int{0, 1, 2},
			UVs:     []mgl32.Vec2{{0, 0}, {3, 0}, {0, 7}},
			Texture: "default",
		}},
	}
	before := append([]mgl32.Vec2(nil), m.Faces[0].UVs...)

	m.RefreshExtrudeSideUVs([]int{0})

	for i, uv := range m.Faces[0].UVs {
		if uv != before[i] {
			t.Errorf("UV %d of a triangle was rewritten: %v -> %v", i, before[i], uv)
		}
	}
}
