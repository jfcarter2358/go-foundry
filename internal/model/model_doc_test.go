package model

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// vec3Close reports whether two positions are equal to within eps.
func vec3Close(a, b mgl32.Vec3, eps float32) bool {
	return a.Sub(b).Len() <= eps
}

func TestNewCubeDocIsAClosedSolid(t *testing.T) {
	m := NewCubeDoc()

	if len(m.Verts) != 8 {
		t.Fatalf("cube vertices = %d, want 8", len(m.Verts))
	}
	if len(m.Faces) != 6 {
		t.Fatalf("cube faces = %d, want 6", len(m.Faces))
	}
	for i, f := range m.Faces {
		if len(f.Verts) != 4 {
			t.Errorf("face %d has %d verts, want a quad", i, len(f.Verts))
		}
		if len(f.UVs) != len(f.Verts) {
			t.Errorf("face %d has %d UVs for %d verts", i, len(f.UVs), len(f.Verts))
		}
	}

	// Every edge shared by exactly two faces is what "closed solid" means; it
	// is also the precondition the extrude/subdivide boundary logic relies on.
	for _, e := range m.Edges() {
		if got := len(m.FacesAtEdge(e)); got != 2 {
			t.Errorf("edge %v shared by %d faces, want 2", e, got)
		}
	}
	if len(m.Edges()) != 12 {
		t.Errorf("cube edges = %d, want 12", len(m.Edges()))
	}
}

func TestNewCubeDocNormalsPointOutward(t *testing.T) {
	m := NewCubeDoc()
	for fi := range m.Faces {
		n := m.FaceNormal(fi)
		// The cube is centred on the origin, so "outward" is simply "away from
		// the centre" — a wrongly wound face fails this immediately.
		outward := m.FaceCenter(fi)
		if n.Dot(outward) <= 0 {
			t.Errorf("face %d normal %v points inward (centre %v)", fi, n, outward)
		}
	}
}

func TestFaceTrianglesQuad(t *testing.T) {
	m := NewCubeDoc()
	tris := m.FaceTriangles(0)
	if len(tris) != 2 {
		t.Fatalf("quad triangulated into %d triangles, want 2", len(tris))
	}
	for _, tri := range tris {
		for _, li := range tri {
			if li < 0 || li >= 4 {
				t.Errorf("triangle index %d out of face-local range", li)
			}
		}
	}
}

func TestFaceTrianglesConcaveNgon(t *testing.T) {
	// An L-shaped hexagon in the XZ plane. A fan from vertex 0 would put a
	// triangle outside the polygon; ear clipping must not.
	m := &ModelDoc{
		Verts: []mgl32.Vec3{
			{0, 0, 0}, {2, 0, 0}, {2, 0, 1}, {1, 0, 1}, {1, 0, 2}, {0, 0, 2},
		},
		Faces: []ModelFace{{
			Verts: []int{0, 1, 2, 3, 4, 5},
			UVs:   make([]mgl32.Vec2, 6),
		}},
	}
	tris := m.FaceTriangles(0)
	if len(tris) != 4 {
		t.Fatalf("hexagon triangulated into %d triangles, want n-2 = 4", len(tris))
	}
	// Total triangle area must equal the polygon's area (3 square units for
	// this L), which it cannot if any triangle strayed outside.
	var area float64
	for _, tri := range tris {
		a := m.Verts[m.Faces[0].Verts[tri[0]]]
		b := m.Verts[m.Faces[0].Verts[tri[1]]]
		c := m.Verts[m.Faces[0].Verts[tri[2]]]
		area += float64(b.Sub(a).Cross(c.Sub(a)).Len()) / 2
	}
	if math.Abs(area-3) > 1e-4 {
		t.Errorf("triangulated area = %v, want 3 (triangles escaped the polygon)", area)
	}
}

func TestFaceNormalNewellHandlesNonPlanarQuad(t *testing.T) {
	// One corner lifted out of plane. A cross product of the first three
	// vertices would ignore the fourth entirely; Newell's sum must not.
	m := &ModelDoc{
		Verts: []mgl32.Vec3{{0, 0, 0}, {1, 0, 0}, {1, 0.5, 1}, {0, 0, 1}},
		Faces: []ModelFace{{Verts: []int{0, 1, 2, 3}, UVs: make([]mgl32.Vec2, 4)}},
	}
	n := m.FaceNormal(0)
	if math.Abs(float64(n.Len()-1)) > 1e-5 {
		t.Fatalf("normal %v is not unit length", n)
	}
	// Rotating the vertex loop describes the same face, so the normal must not
	// change — the property a first-three-vertices cross product lacks.
	rot := &ModelDoc{
		Verts: m.Verts,
		Faces: []ModelFace{{Verts: []int{1, 2, 3, 0}, UVs: make([]mgl32.Vec2, 4)}},
	}
	if !vec3Close(n, rot.FaceNormal(0), 1e-5) {
		t.Errorf("normal changed under vertex rotation: %v vs %v", n, rot.FaceNormal(0))
	}
}

// A document wears one texture, so it compiles to exactly one submesh however
// many faces it has — the per-texture grouping this used to assert is gone.
func TestModelSubmeshesAreOneGroup(t *testing.T) {
	m := NewCubeDoc()
	m.SetTexture("brick")

	subs := m.ModelSubmeshes()
	if len(subs) != 1 {
		t.Fatalf("submeshes = %d, want 1", len(subs))
	}
	s := subs[0]
	if s.Texture != "brick" {
		t.Errorf("submesh texture = %q, want brick", s.Texture)
	}
	// Six quads -> 24 vertex records, 12 triangles -> 36 indices.
	if len(s.Vertices) != 24 {
		t.Errorf("vertices = %d, want 24", len(s.Vertices))
	}
	if len(s.Indices) != 36 {
		t.Errorf("indices = %d, want 36", len(s.Indices))
	}
	for _, v := range s.Vertices {
		if len(v) != 5 {
			t.Fatalf("vertex record has %d floats, want 5 [X,Y,Z,U,V]", len(v))
		}
	}
	for _, idx := range s.Indices {
		if idx < 0 || idx >= len(s.Vertices) {
			t.Fatalf("index %d out of range (%d verts)", idx, len(s.Vertices))
		}
	}
}

// An untextured document still names a texture, so the renderer never binds
// texture 0 and draws the mesh black.
func TestModelSubmeshFallsBackToDefaultTexture(t *testing.T) {
	m := NewCubeDoc()
	if got := m.ModelSubmeshes()[0].Texture; got != DefaultModelTexture {
		t.Errorf("submesh texture = %q, want %q", got, DefaultModelTexture)
	}
}

// An empty mesh compiles to nothing rather than to one empty submesh, so the
// renderer has no zero-vertex Geometry to upload.
func TestModelSubmeshesEmptyMesh(t *testing.T) {
	m := &ModelDoc{ID: "empty"}
	if subs := m.ModelSubmeshes(); len(subs) != 0 {
		t.Errorf("submeshes = %d, want 0 for an empty mesh", len(subs))
	}
}

func TestModelSubmeshSmoothFlagPropagates(t *testing.T) {
	m := NewCubeDoc()
	subs := m.ModelSubmeshes()
	if subs[0].Smooth {
		t.Error("submesh smooth before any face asked for it")
	}
	m.SetFaceSmooth([]int{2}, true)
	subs = m.ModelSubmeshes()
	if !subs[0].Smooth {
		t.Error("submesh not marked smooth after a face enabled smoothing")
	}
}

func TestCloneIsDeep(t *testing.T) {
	m := NewCubeDoc()
	c := m.Clone()
	c.Verts[0] = mgl32.Vec3{9, 9, 9}
	c.Faces[0].Verts[0] = 7
	c.Texture = "brick"

	if m.Verts[0] == (mgl32.Vec3{9, 9, 9}) {
		t.Error("clone shares the vertex slice")
	}
	if m.Faces[0].Verts[0] == 7 {
		t.Error("clone shares a face's vertex slice")
	}
	if m.Texture == "brick" {
		t.Error("clone shares the document texture")
	}
}
