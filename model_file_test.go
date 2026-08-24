package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

func TestFmdlRoundTripPreservesEverything(t *testing.T) {
	m := NewCubeDoc()
	m.ID = "crate"
	m.SmoothAngle = 45
	m.SetTexture("brick")
	m.SetFaceSmooth([]int{1}, true)
	m.Faces[0].UVs[2] = mgl32.Vec2{0.25, 0.75}

	data, err := yaml.Marshal(modelDocToRaw(m))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw RawModel
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := validateRawModel(raw); err != nil {
		t.Fatalf("validate: %v", err)
	}
	got := rawToModelDoc(raw)

	if got.ID != "crate" || got.SmoothAngle != 45 {
		t.Errorf("header lost: ID=%q SmoothAngle=%v", got.ID, got.SmoothAngle)
	}
	if got.Texture != "brick" {
		t.Errorf("texture: %q -> %q", m.Texture, got.Texture)
	}
	if len(got.Verts) != len(m.Verts) || len(got.Faces) != len(m.Faces) {
		t.Fatalf("shape lost: %d verts %d faces", len(got.Verts), len(got.Faces))
	}
	for i := range m.Verts {
		if !vec3Close(m.Verts[i], got.Verts[i], 1e-6) {
			t.Errorf("vertex %d: %v -> %v", i, m.Verts[i], got.Verts[i])
		}
	}
	for i := range m.Faces {
		a, b := m.Faces[i], got.Faces[i]
		if a.Smooth != b.Smooth {
			t.Errorf("face %d smooth: %v -> %v", i, a.Smooth, b.Smooth)
		}
		if len(b.UVs) != len(b.Verts) {
			t.Errorf("face %d has %d UVs for %d verts", i, len(b.UVs), len(b.Verts))
		}
	}
	if got.Faces[0].UVs[2] != (mgl32.Vec2{0.25, 0.75}) {
		t.Errorf("edited UV lost: %v", got.Faces[0].UVs[2])
	}
}

func TestFmdlOmitsDefaultedFields(t *testing.T) {
	// A face that is not smooth and has no UVs must not write those keys, or
	// every model file carries a screenful of noise.
	m := &ModelDoc{
		ID:    "plain",
		Verts: []mgl32.Vec3{{0, 0, 0}, {1, 0, 0}, {0, 0, 1}},
		Faces: []ModelFace{{Verts: []int{0, 1, 2}, UVs: make([]mgl32.Vec2, 3)}},
	}
	data, err := yaml.Marshal(modelDocToRaw(m))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(data)
	for _, key := range []string{"smooth:", "uv:", "smooth_angle:", "texture:"} {
		if strings.Contains(out, key) {
			t.Errorf("zero-valued %s written to YAML:\n%s", key, out)
		}
	}
}

func TestValidateRawModelRejectsBadIndices(t *testing.T) {
	cases := []struct {
		name string
		raw  RawModel
	}{
		{"no vertices", RawModel{ID: "x"}},
		{"out of range", RawModel{
			ID:       "x",
			Vertices: [][]float32{{0, 0, 0}, {1, 0, 0}, {0, 0, 1}},
			Faces:    []RawModelFace{{Verts: []int{0, 1, 9}}},
		}},
		{"negative index", RawModel{
			ID:       "x",
			Vertices: [][]float32{{0, 0, 0}, {1, 0, 0}, {0, 0, 1}},
			Faces:    []RawModelFace{{Verts: []int{0, 1, -1}}},
		}},
		{"degenerate face", RawModel{
			ID:       "x",
			Vertices: [][]float32{{0, 0, 0}, {1, 0, 0}, {0, 0, 1}},
			Faces:    []RawModelFace{{Verts: []int{0, 1}}},
		}},
		{"uv length mismatch", RawModel{
			ID:       "x",
			Vertices: [][]float32{{0, 0, 0}, {1, 0, 0}, {0, 0, 1}},
			Faces:    []RawModelFace{{Verts: []int{0, 1, 2}, UVs: [][]float32{{0, 0}}}},
		}},
		{"bad vertex arity", RawModel{
			ID:       "x",
			Vertices: [][]float32{{0, 0}},
		}},
	}
	for _, c := range cases {
		if err := validateRawModel(c.raw); err == nil {
			t.Errorf("%s: validateRawModel accepted an invalid model", c.name)
		}
	}
}

func TestSaveAndLoadModelFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crate.fmdl")

	m := NewCubeDoc()
	m.ID = "crate"
	m.SetTexture("brick")

	if err := SaveModel(m, path); err != nil {
		t.Fatalf("SaveModel: %v", err)
	}
	got, err := LoadModel(path)
	if err != nil {
		t.Fatalf("LoadModel: %v", err)
	}
	if got.ID != "crate" {
		t.Errorf("ID = %q, want crate", got.ID)
	}
	if got.Path != path {
		t.Errorf("Path = %q, want %q", got.Path, path)
	}
	if got.Texture != "brick" {
		t.Errorf("texture = %q, want brick", got.Texture)
	}
	if !got.Dirty() {
		t.Error("freshly loaded document is not dirty; it would never reach the GPU")
	}
}

func TestLoadModelRejectsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.fmdl")
	// Structurally valid YAML, semantically impossible mesh.
	if err := os.WriteFile(path, []byte("id: bad\nvertices:\n  - [0,0,0]\nfaces:\n  - v: [0,1,2]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadModel(path); err == nil {
		t.Error("LoadModel accepted a face referencing vertices that do not exist")
	}
}

// A .fmdl written before the move to one texture per document named a texture
// per face. Those files must still open wearing the material they were saved
// with, rather than silently reverting to the default.
func TestLoadModelPromotesLegacyPerFaceTexture(t *testing.T) {
	raw := RawModel{
		ID:       "old",
		Vertices: [][]float32{{0, 0, 0}, {1, 0, 0}, {1, 1, 0}},
		Faces: []RawModelFace{
			{Verts: []int{0, 1, 2}, Texture: "default"},
			{Verts: []int{0, 1, 2}, Texture: "brick"},
		},
	}
	got := rawToModelDoc(raw)
	if got.Texture != "brick" {
		t.Errorf("legacy texture = %q, want brick", got.Texture)
	}
}

// The legacy field is read, never written: a document saved after the migration
// must not re-emit a per-face texture for the next reader to promote again.
func TestSaveModelOmitsPerFaceTexture(t *testing.T) {
	m := NewCubeDoc()
	m.SetTexture("brick")
	raw := modelDocToRaw(m)
	if raw.Texture != "brick" {
		t.Errorf("raw.Texture = %q, want brick", raw.Texture)
	}
	for i, f := range raw.Faces {
		if f.Texture != "" {
			t.Errorf("face %d still carries texture %q", i, f.Texture)
		}
	}
}

// "Never chose a texture" and "chose the default" have to serialise the same
// way, or a load/save round-trip would invent a change.
func TestDefaultTextureRoundTripsAsEmpty(t *testing.T) {
	m := NewCubeDoc()
	m.SetTexture(defaultModelTexture)
	if m.Texture != "" {
		t.Errorf("Texture = %q, want empty", m.Texture)
	}
	if got := m.TextureName(); got != defaultModelTexture {
		t.Errorf("TextureName() = %q, want %q", got, defaultModelTexture)
	}
	if raw := modelDocToRaw(m); raw.Texture != "" {
		t.Errorf("raw.Texture = %q, want empty", raw.Texture)
	}
}
