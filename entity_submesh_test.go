package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// Entity.Load needs a GL context, so these tests exercise the split that
// decides how many submeshes it will build, which is the part with the logic in
// it. The upload itself is a straight loop over the result.

func TestModelSubmeshesSplitMatchesMaterialCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "two.obj")

	m := NewCubeDoc()
	m.SetFaceTexture([]int{0, 1}, "brick")
	if err := SaveOBJ(m, path); err != nil {
		t.Fatal(err)
	}

	doc, err := LoadOBJMesh(path)
	if err != nil {
		t.Fatal(err)
	}
	subs := doc.modelSubmeshes()
	if len(subs) != 2 {
		t.Fatalf("submeshes = %d, want 2 — an entity would render this with one texture", len(subs))
	}
	total := 0
	for _, s := range subs {
		total += len(s.Indices)
	}
	// Six quads, two triangles each, three indices per triangle.
	if total != 36 {
		t.Errorf("total indices = %d, want 36", total)
	}
}

func TestUnmaterialledOBJYieldsOneSubmesh(t *testing.T) {
	// The backward-compatibility case: every .obj the engine shipped before the
	// model editor names no material, and must still produce exactly one
	// submesh so the entity renders as it always did.
	dir := t.TempDir()
	path := filepath.Join(dir, "plain.obj")
	src := "v 0 0 0\nv 1 0 0\nv 1 0 1\nv 0 0 1\nf 1 2 3 4\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := LoadOBJMesh(path)
	if err != nil {
		t.Fatal(err)
	}
	subs := doc.modelSubmeshes()
	if len(subs) != 1 {
		t.Fatalf("submeshes = %d, want 1", len(subs))
	}
	// "default" is the marker Entity.Load swaps for the entity's own
	// TexturePath, which is what preserves the old single-texture behaviour.
	if subs[0].Texture != "default" {
		t.Errorf("submesh texture = %q, want %q", subs[0].Texture, "default")
	}
}

func TestEntitySetAlphaReachesEverySubmesh(t *testing.T) {
	e := Entity{
		Alpha:     1,
		Submeshes: []Geometry{{Alpha: 1}, {Alpha: 1}},
	}
	e.SetAlpha(0.4)
	if e.Alpha != 0.4 {
		t.Errorf("entity alpha = %v, want 0.4", e.Alpha)
	}
	for i, g := range e.Submeshes {
		if g.Alpha != 0.4 {
			t.Errorf("submesh %d alpha = %v, want 0.4 — it would stay opaque", i, g.Alpha)
		}
	}
}

func TestEntityHasGeometry(t *testing.T) {
	var e Entity
	if e.HasGeometry() {
		t.Error("empty entity reports geometry; picking would ray-cast nothing")
	}
	e.Submeshes = []Geometry{{}}
	if e.HasGeometry() {
		t.Error("a submesh with no vertices counts as geometry")
	}
	e.Submeshes = []Geometry{{}, {Vertices: []float32{0, 0, 0, 0, 0}}}
	if !e.HasGeometry() {
		t.Error("entity with a loaded submesh reports no geometry")
	}
}

func TestLoadModelDocumentPicksReaderByExtension(t *testing.T) {
	dir := t.TempDir()

	fmdl := filepath.Join(dir, "a.fmdl")
	m := NewCubeDoc()
	m.ID = "a"
	if err := SaveModel(m, fmdl); err != nil {
		t.Fatal(err)
	}
	obj := filepath.Join(dir, "b.obj")
	if err := SaveOBJ(m, obj); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{fmdl, obj, filepath.Join(dir, "A.FMDL")} {
		if path == filepath.Join(dir, "A.FMDL") {
			// Extension matching must be case-insensitive, or a model saved on
			// a case-preserving filesystem would go through the OBJ reader.
			if err := os.Rename(fmdl, path); err != nil {
				t.Fatal(err)
			}
		}
		doc, err := loadModelDocument(path)
		if err != nil {
			t.Fatalf("%s: %v", filepath.Base(path), err)
		}
		if len(doc.Faces) != 6 || len(doc.Verts) != 8 {
			t.Errorf("%s: %d faces, %d verts; want 6, 8",
				filepath.Base(path), len(doc.Faces), len(doc.Verts))
		}
		min, max := doc.Bounds()
		if !vec3Close(min, mgl32.Vec3{-0.5, -0.5, -0.5}, 1e-5) ||
			!vec3Close(max, mgl32.Vec3{0.5, 0.5, 0.5}, 1e-5) {
			t.Errorf("%s: bounds %v..%v", filepath.Base(path), min, max)
		}
	}
}
