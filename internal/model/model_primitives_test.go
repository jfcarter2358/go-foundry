package model

import (
	"fmt"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func TestPrimitivesAreClosedSolidsWithOutwardNormals(t *testing.T) {
	// Plane is deliberately excluded: it is a single open face, so it has
	// boundary edges by definition.
	for _, kind := range []ModelPrimitiveKind{
		ModelPrimBox, ModelPrimCylinder, ModelPrimCone, ModelPrimSphere,
	} {
		t.Run(kind.String(), func(t *testing.T) {
			m := NewPrimitiveDoc(kind, 12)
			if len(m.Verts) == 0 || len(m.Faces) == 0 {
				t.Fatalf("%s produced an empty mesh", kind)
			}
			for _, e := range m.Edges() {
				if n := len(m.FacesAtEdge(e)); n != 2 {
					t.Errorf("edge %v shared by %d faces, want 2 — %s is not closed", e, n, kind)
				}
			}
			// Each primitive is centred on the origin, so a correctly wound
			// face has its normal pointing away from the centre.
			for fi := range m.Faces {
				n := m.FaceNormal(fi)
				if n.Len() < 0.5 {
					t.Errorf("%s face %d has a degenerate normal", kind, fi)
					continue
				}
				if n.Dot(m.FaceCenter(fi)) <= 0 {
					t.Errorf("%s face %d is wound inside out (normal %v, centre %v)",
						kind, fi, n, m.FaceCenter(fi))
				}
			}
			// Everything is sized to fit a unit cube so shapes arrive at
			// comparable scale.
			min, max := m.Bounds()
			for ax := 0; ax < 3; ax++ {
				if min[ax] < -0.5001 || max[ax] > 0.5001 {
					t.Errorf("%s escapes the unit cube: %v..%v", kind, min, max)
				}
			}
		})
	}
}

func TestPrimitivesKeepNgonFaces(t *testing.T) {
	// The whole reason these exist rather than reusing primitive.go: the faces
	// have to survive as faces, or they cannot be selected and extruded.
	cyl := NewPrimitiveDoc(ModelPrimCylinder, 8)
	// 8 side quads + 2 octagonal caps.
	if len(cyl.Faces) != 10 {
		t.Fatalf("cylinder faces = %d, want 10", len(cyl.Faces))
	}
	caps, sides := 0, 0
	for _, f := range cyl.Faces {
		switch len(f.Verts) {
		case 8:
			caps++
		case 4:
			sides++
		}
	}
	if caps != 2 || sides != 8 {
		t.Errorf("cylinder has %d octagonal caps and %d quad sides, want 2 and 8", caps, sides)
	}

	cone := NewPrimitiveDoc(ModelPrimCone, 8)
	if len(cone.Faces) != 9 {
		t.Errorf("cone faces = %d, want 9 (8 sides + 1 base)", len(cone.Faces))
	}

	box := NewPrimitiveDoc(ModelPrimBox, 8)
	for i, f := range box.Faces {
		if len(f.Verts) != 4 {
			t.Errorf("box face %d has %d verts, want a quad", i, len(f.Verts))
		}
	}
}

func TestPrimitiveSegmentsClamped(t *testing.T) {
	// A cylinder with two sides is not a solid; clamping keeps the invariants
	// the rest of the editor relies on.
	if m := NewPrimitiveDoc(ModelPrimCylinder, 0); len(m.Faces) != 5 {
		t.Errorf("segments 0 gave %d faces, want 5 (clamped to 3 sides)", len(m.Faces))
	}
	if m := NewPrimitiveDoc(ModelPrimCylinder, 5000); len(m.Verts) > 300 {
		t.Errorf("segments 5000 gave %d verts; the clamp did not apply", len(m.Verts))
	}
}

func TestPrimitivesHaveFullUVs(t *testing.T) {
	for _, kind := range ModelPrimitiveKinds {
		m := NewPrimitiveDoc(kind, 8)
		for i, f := range m.Faces {
			if len(f.UVs) != len(f.Verts) {
				t.Errorf("%s face %d has %d UVs for %d verts", kind, i, len(f.UVs), len(f.Verts))
			}
		}
	}
}

func TestMergeAppendsAndOffsets(t *testing.T) {
	base := NewCubeDoc()
	other := NewCubeDoc()
	other.SetTexture("brick")

	added := base.Merge(other, mgl32.Vec3{5, 0, 0})

	if len(added) != 6 {
		t.Fatalf("Merge reported %d added faces, want 6", len(added))
	}
	if len(base.Verts) != 16 || len(base.Faces) != 12 {
		t.Fatalf("after merge: %d verts, %d faces; want 16, 12", len(base.Verts), len(base.Faces))
	}
	// The added faces must reference the appended vertices, not the originals.
	for _, fi := range added {
		for _, v := range base.Faces[fi].Verts {
			if v < 8 {
				t.Fatalf("merged face %d references original vertex %d", fi, v)
			}
		}
	}
	// Two separate islands: every edge still has exactly two faces, and the
	// offset copy sits where it was put.
	for _, e := range base.Edges() {
		if n := len(base.FacesAtEdge(e)); n != 2 {
			t.Errorf("edge %v shared by %d faces after merge", e, n)
		}
	}
	min, max := base.Bounds()
	if !vec3Close(min, mgl32.Vec3{-0.5, -0.5, -0.5}, 1e-5) ||
		!vec3Close(max, mgl32.Vec3{5.5, 0.5, 0.5}, 1e-5) {
		t.Errorf("merged bounds %v..%v, want (-0.5,-0.5,-0.5)..(5.5,0.5,0.5)", min, max)
	}
}

func TestMergeIgnoresEmptyDocument(t *testing.T) {
	base := NewCubeDoc()
	if got := base.Merge(nil, mgl32.Vec3{}); got != nil {
		t.Errorf("merging nil returned %v", got)
	}
	if got := base.Merge(&ModelDoc{}, mgl32.Vec3{}); got != nil {
		t.Errorf("merging an empty document returned %v", got)
	}
	if len(base.Verts) != 8 || len(base.Faces) != 6 {
		t.Error("merging nothing changed the document")
	}
}

// modelAllFaceIndicesOf is the test-local form of the panel's helper, which
// reads the modelDoc global.
func modelAllFaceIndicesOf(m *ModelDoc) []int {
	out := make([]int, len(m.Faces))
	for i := range out {
		out[i] = i
	}
	return out
}

func TestPrimitiveKindNames(t *testing.T) {
	// The palette buttons are labelled from String(); a duplicate would give
	// two buttons the same ImGui ID.
	seen := map[string]bool{}
	for _, k := range ModelPrimitiveKinds {
		name := fmt.Sprint(k)
		if name == "" || seen[name] {
			t.Errorf("primitive kind %d has a missing or duplicate name %q", int(k), name)
		}
		seen[name] = true
	}
}

// A document wears one texture, so merging cannot bring a second one in: the
// combined mesh keeps the texture the destination already had.
func TestMergeKeepsDestinationTexture(t *testing.T) {
	base := NewCubeDoc()
	base.SetTexture("stone")
	other := NewCubeDoc()
	other.SetTexture("brick")

	base.Merge(other, mgl32.Vec3{5, 0, 0})

	if base.Texture != "stone" {
		t.Errorf("texture = %q, want stone — merge must not adopt the source's", base.Texture)
	}
}
