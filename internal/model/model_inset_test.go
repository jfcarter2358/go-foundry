package model

// model_inset_test.go — InsetFaces: the frame operation.
//
// The two things worth asserting are the two things the cheap version gets
// wrong: the border is the SAME WIDTH all the way round whatever shape the face
// is, and the face's own outline does not move — so nothing sharing its corners
// is dragged along with it.

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// insetQuad is one face in the XZ plane, of the given half-extents, wound CCW
// seen from +Y so its normal points up.
func insetQuad(hx, hz float32) *ModelDoc {
	return &ModelDoc{
		Verts: []mgl32.Vec3{
			{-hx, 0, -hz}, {hx, 0, -hz}, {hx, 0, hz}, {-hx, 0, hz},
		},
		Faces: []ModelFace{{
			Verts: []int{0, 3, 2, 1},
			UVs:   []mgl32.Vec2{{0, 0}, {0, 1}, {1, 1}, {1, 0}},
		}},
	}
}

func TestInsetMakesARingAndKeepsTheOutline(t *testing.T) {
	m := insetQuad(2, 2)
	before := append([]mgl32.Vec3(nil), m.Verts...)

	inner, ring := m.InsetFaces([]int{0}, 0.5)

	if len(inner) != 1 || inner[0] != 0 {
		t.Fatalf("inner = %v, want the face that went in", inner)
	}
	if len(ring) != 4 {
		t.Fatalf("ring = %v, want one quad per edge", ring)
	}
	if len(m.Faces) != 5 {
		t.Fatalf("mesh has %d faces, want the inner one plus four ring quads", len(m.Faces))
	}
	// The outline is exactly where it was: a real inset adds a loop inside,
	// it does not drag the corners the neighbours share.
	for i := range before {
		if m.Verts[i] != before[i] {
			t.Errorf("original corner %d moved to %v, was %v", i, m.Verts[i], before[i])
		}
	}
	// Every ring face is a quad.
	for _, fi := range ring {
		if n := len(m.Faces[fi].Verts); n != 4 {
			t.Errorf("ring face %d has %d corners, want 4", fi, n)
		}
		if len(m.Faces[fi].UVs) != 4 {
			t.Errorf("ring face %d has %d UVs, want 4", fi, len(m.Faces[fi].UVs))
		}
	}
}

// TestInsetBorderIsUniformOnALongFace is the whole reason this is not "pull the
// corners toward the centre": on a 10x1 strip that version leaves a border ten
// times wider along one axis than the other.
func TestInsetBorderIsUniformOnALongFace(t *testing.T) {
	m := insetQuad(5, 0.5)
	const dist = 0.2

	inner, _ := m.InsetFaces([]int{0}, dist)
	if len(inner) != 1 {
		t.Fatalf("the inset was refused on a face that has room for it: %v", inner)
	}

	// The inner loop should be the outline brought in by dist on all four
	// sides: x within ±4.8, z within ±0.3.
	for _, vi := range m.Faces[0].Verts {
		p := m.Verts[vi]
		if d := math.Abs(math.Abs(float64(p.X())) - (5 - dist)); d > 1e-4 {
			t.Errorf("inner corner %v is %.4f off the wanted x=±%.2f", p, d, 5-dist)
		}
		if d := math.Abs(math.Abs(float64(p.Z())) - (0.5 - dist)); d > 1e-4 {
			t.Errorf("inner corner %v is %.4f off the wanted z=±%.2f", p, d, 0.5-dist)
		}
	}
}

// TestInsetKeepsTheFaceInItsOwnPlane matters because the inner face is what
// gets extruded next: if the inset drifted off the plane, the recess would be
// skewed before it was even started.
func TestInsetKeepsTheFaceInItsOwnPlane(t *testing.T) {
	m := insetQuad(2, 2)
	m.InsetFaces([]int{0}, 0.5)
	for _, vi := range m.Faces[0].Verts {
		if y := m.Verts[vi].Y(); math.Abs(float64(y)) > 1e-5 {
			t.Errorf("inner corner left the face's plane: y=%v", y)
		}
	}
}

// TestInsetRefusesWhatWillNotFit is the case that would otherwise turn the loop
// inside out: a border wider than the face is deep.
func TestInsetRefusesWhatWillNotFit(t *testing.T) {
	m := insetQuad(2, 0.25)
	before := len(m.Faces)

	inner, ring := m.InsetFaces([]int{0}, 1.0)
	if len(inner) != 0 || len(ring) != 0 {
		t.Errorf("inset by 1.0 on a face 0.5 deep produced %v / %v", inner, ring)
	}
	if len(m.Faces) != before {
		t.Errorf("the mesh changed anyway: %d faces, was %d", len(m.Faces), before)
	}
}

// TestInsetWindsTheRingWithItsFace keeps the new quads facing the same way the
// face they came from does — a ring wound the other way is a hole in the model
// that only shows up under backface culling.
func TestInsetWindsTheRingWithItsFace(t *testing.T) {
	m := insetQuad(2, 2)
	want := m.faceNormal(&m.Faces[0])

	_, ring := m.InsetFaces([]int{0}, 0.5)
	for _, fi := range ring {
		if got := m.faceNormal(&m.Faces[fi]); got.Dot(want) < 0.99 {
			t.Errorf("ring face %d faces %v, want %v", fi, got, want)
		}
	}
	if got := m.faceNormal(&m.Faces[0]); got.Dot(want) < 0.99 {
		t.Errorf("the inner face faces %v, want %v", got, want)
	}
}

// TestInsetThenExtrudeIsARecess is the workflow the operation exists for, and
// the assertion that its output is something the next operation can take.
func TestInsetThenExtrudeIsARecess(t *testing.T) {
	m := insetQuad(2, 2)
	inner, _ := m.InsetFaces([]int{0}, 0.5)
	caps, sides := m.ExtrudeFaces(inner, -0.75)

	if len(caps) != 1 || len(sides) == 0 {
		t.Fatalf("extruding the inset face gave caps=%v sides=%v", caps, sides)
	}
	for _, vi := range m.Faces[caps[0]].Verts {
		if y := m.Verts[vi].Y(); math.Abs(float64(y+0.75)) > 1e-4 {
			t.Errorf("the recess floor is at y=%v, want -0.75", y)
		}
	}
}
