package model

import (
	"foundry/internal/testenv"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func TestOBJRoundTripPreservesNgonsAndMaterial(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crate.obj")

	m := NewCubeDoc()
	m.ID = "crate"
	m.SetTexture("brick")

	if err := SaveOBJ(m, path); err != nil {
		t.Fatalf("SaveOBJ: %v", err)
	}
	got, err := LoadOBJMesh(path)
	if err != nil {
		t.Fatalf("LoadOBJMesh: %v", err)
	}

	if len(got.Verts) != 8 {
		t.Errorf("verts = %d, want 8", len(got.Verts))
	}
	// The whole point of the editor-facing reader: quads stay quads.
	if len(got.Faces) != 6 {
		t.Fatalf("faces = %d, want 6 (n-gons must survive, not fan out)", len(got.Faces))
	}
	for i, f := range got.Faces {
		if len(f.Verts) != 4 {
			t.Errorf("face %d has %d verts, want a quad", i, len(f.Verts))
		}
	}
	if got.Texture != "brick" {
		t.Errorf("texture came back as %q, want brick", got.Texture)
	}
	// Geometry must be unchanged.
	minA, maxA := m.Bounds()
	minB, maxB := got.Bounds()
	if !vec3Close(minA, minB, 1e-5) || !vec3Close(maxA, maxB, 1e-5) {
		t.Errorf("bounds changed: %v..%v -> %v..%v", minA, maxA, minB, maxB)
	}
}

func TestSaveOBJWritesMTL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crate.obj")

	m := NewCubeDoc()
	m.SetTexture("brick")
	if err := SaveOBJ(m, path); err != nil {
		t.Fatalf("SaveOBJ: %v", err)
	}

	obj, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(obj), "mtllib crate.mtl") {
		t.Error("OBJ does not reference its material library")
	}
	if !strings.Contains(string(obj), "usemtl brick") {
		t.Error("OBJ has no usemtl line for the model's material")
	}
	// One material means one usemtl, not one per face.
	if n := strings.Count(string(obj), "usemtl "); n != 1 {
		t.Errorf("%d usemtl lines, want 1", n)
	}

	mtl, err := os.ReadFile(filepath.Join(dir, "crate.mtl"))
	if err != nil {
		t.Fatalf("no sibling .mtl written: %v", err)
	}
	for _, want := range []string{"newmtl brick", "map_Kd textures/brick.png"} {
		if !strings.Contains(string(mtl), want) {
			t.Errorf(".mtl missing %q:\n%s", want, mtl)
		}
	}
	if strings.Contains(string(mtl), "newmtl default") {
		t.Errorf(".mtl still writes a per-face fallback material:\n%s", mtl)
	}
}

func TestLoadOBJMeshHandlesNegativeIndices(t *testing.T) {
	// Relative indices are legal OBJ and common from exporters that stream.
	src := "v 0 0 0\nv 1 0 0\nv 1 0 1\nv 0 0 1\nf -4 -3 -2 -1\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "rel.obj")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadOBJMesh(path)
	if err != nil {
		t.Fatalf("LoadOBJMesh: %v", err)
	}
	if len(got.Faces) != 1 {
		t.Fatalf("faces = %d, want 1", len(got.Faces))
	}
	want := []int{0, 1, 2, 3}
	for i, v := range got.Faces[0].Verts {
		if v != want[i] {
			t.Errorf("negative indices resolved to %v, want %v", got.Faces[0].Verts, want)
			break
		}
	}
}

func TestLoadOBJMeshSkipsNormalsAndKeepsUVs(t *testing.T) {
	src := strings.Join([]string{
		"v 0 0 0", "v 1 0 0", "v 1 0 1",
		"vt 0 0", "vt 1 0", "vt 1 1",
		"vn 0 1 0",
		"f 1/1/1 2/2/1 3/3/1",
	}, "\n")
	dir := t.TempDir()
	path := filepath.Join(dir, "uv.obj")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadOBJMesh(path)
	if err != nil {
		t.Fatalf("LoadOBJMesh: %v", err)
	}
	f := got.Faces[0]
	if len(f.Verts) != 3 || len(f.UVs) != 3 {
		t.Fatalf("face has %d verts, %d UVs", len(f.Verts), len(f.UVs))
	}
	if f.UVs[2] != (mgl32.Vec2{1, 1}) {
		t.Errorf("UV 2 = %v, want (1,1)", f.UVs[2])
	}
}

func TestLoadOBJMeshRejectsOutOfRangeIndex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.obj")
	if err := os.WriteFile(path, []byte("v 0 0 0\nv 1 0 0\nf 1 2 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOBJMesh(path); err == nil {
		t.Error("LoadOBJMesh accepted a face referencing a vertex that does not exist")
	}
}

func TestLoadOBJMeshOnShippedModels(t *testing.T) {
	// Characterisation test. These numbers were captured from the original
	// hand-rolled parser in obj.go before it was replaced by a wrapper over
	// LoadOBJMesh, so they pin the new reader to the old one's behaviour on the
	// models the engine actually ships.
	cases := []struct {
		name              string
		tris, faces, vrts int
		min, max          mgl32.Vec3
	}{
		{"box.obj", 12, 12, 8, mgl32.Vec3{-0.5, -0.5, -0.5}, mgl32.Vec3{0.5, 0.5, 0.5}},
		{"car.obj", 24, 24, 16, mgl32.Vec3{-2, 0, -1}, mgl32.Vec3{2, 1.5, 1}},
		{"rock.obj", 16, 16, 10, mgl32.Vec3{-0.5, -0.3, -0.5}, mgl32.Vec3{0.5, 0.5, 0.5}},
	}
	for _, c := range cases {
		path := filepath.Join(testenv.RepoRoot(t), "resources", "models", c.name)
		if _, err := os.Stat(path); err != nil {
			t.Skipf("%s not present", path)
		}
		doc, err := LoadOBJMesh(path)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(doc.Faces) != c.faces || len(doc.Verts) != c.vrts {
			t.Errorf("%s: %d faces, %d verts; want %d, %d",
				c.name, len(doc.Faces), len(doc.Verts), c.faces, c.vrts)
		}
		tris := 0
		for fi := range doc.Faces {
			tris += len(doc.FaceTriangles(fi))
		}
		if tris != c.tris {
			t.Errorf("%s: %d triangles, want %d", c.name, tris, c.tris)
		}
		dmin, dmax := doc.Bounds()
		if !vec3Close(dmin, c.min, 1e-4) || !vec3Close(dmax, c.max, 1e-4) {
			t.Errorf("%s: bounds %v..%v, want %v..%v", c.name, dmin, dmax, c.min, c.max)
		}

		// The flat-buffer wrapper must agree with the document it delegates to:
		// 3 vertices per triangle, 5 floats each.
		flat, err := LoadOBJ(path)
		if err != nil {
			t.Fatalf("%s: LoadOBJ: %v", c.name, err)
		}
		if len(flat) != c.tris*15 {
			t.Errorf("%s: LoadOBJ returned %d floats, want %d", c.name, len(flat), c.tris*15)
		}
	}
}
