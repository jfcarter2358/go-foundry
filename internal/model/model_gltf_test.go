package model

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// twoSlotQuad is two quads side by side, one per material slot.
func twoSlotQuad() *ImportMesh {
	m := &ImportMesh{
		Positions: []mgl32.Vec3{{0, 0, 0}, {1, 0, 0}, {1, 1, 0}, {0, 1, 0}, {2, 0, 0}, {2, 1, 0}},
		Normals:   []mgl32.Vec3{{0, 0, 1}, {0, 0, 1}, {0, 0, 1}, {0, 0, 1}, {0, 0, 1}, {0, 0, 1}},
		UVs:       []mgl32.Vec2{{0, 0}, {1, 0}, {1, 1}, {0, 1}, {2, 0}, {2, 1}},
		Indices:   []uint32{0, 1, 2, 0, 2, 3, 1, 4, 5, 1, 5, 2},
		TriSlot:   []int32{0, 0, 1, 1},
		Slots: []ImportSlot{
			{Name: "stone", Texture: "rock_albedo", BaseColor: [4]float32{1, 1, 1, 1}},
			{Name: "moss", BaseColor: [4]float32{0.2, 0.6, 0.1, 1}, DoubleSided: true},
		},
	}
	return m
}

func TestGLBRoundTrip(t *testing.T) {
	src := twoSlotQuad()
	glb, err := EncodeGLB(src)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseGLTF(glb, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got.TriangleCount() != 4 || len(got.Slots) != 2 {
		t.Fatalf("%d triangles, %d slots", got.TriangleCount(), len(got.Slots))
	}
	if got.Slots[0].Texture != "rock_albedo" || got.Slots[0].Name != "stone" || got.Slots[1].Texture != "" || !got.Slots[1].DoubleSided {
		t.Fatalf("slots %+v", got.Slots)
	}
	// Triangles come back per slot, each with its slot and the same corners.
	type tri struct {
		a, b, c mgl32.Vec3
		slot    int32
	}
	key := func(m *ImportMesh, t int) tri {
		return tri{m.Positions[m.Indices[3*t]], m.Positions[m.Indices[3*t+1]], m.Positions[m.Indices[3*t+2]], m.TriSlot[t]}
	}
	want := map[tri]bool{}
	for i := 0; i < src.TriangleCount(); i++ {
		want[key(src, i)] = true
	}
	for i := 0; i < got.TriangleCount(); i++ {
		if !want[key(got, i)] {
			t.Fatalf("triangle %d %+v was not in the source", i, key(got, i))
		}
	}
	// Each primitive carries the whole shared vertex list, so a vertex can
	// come back more than once; every copy has its source's attributes.
	for v := range got.Positions {
		w := v % len(src.Positions)
		if got.Positions[v] != src.Positions[w] || got.UVs[v] != src.UVs[w] || got.Normals[v] != src.Normals[w] {
			t.Fatalf("vertex %d: %v uv %v normal %v, want %v %v %v", v, got.Positions[v], got.UVs[v], got.Normals[v],
				src.Positions[w], src.UVs[w], src.Normals[w])
		}
	}
}

// minimalGLTF is a .gltf JSON with an embedded buffer: one triangle strip of
// four vertices under a translated, scaled node inside a rotated parent,
// UVs as normalised unsigned shorts and indices as bytes.
func minimalGLTF(t *testing.T) string {
	t.Helper()
	var buf []byte
	f32 := func(v float32) { buf = binary.LittleEndian.AppendUint32(buf, math.Float32bits(v)) }
	for _, p := range [][3]float32{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}, {1, 1, 0}} {
		f32(p[0])
		f32(p[1])
		f32(p[2])
	}
	// 48 bytes of positions; UVs (normalised u16) at 48.
	for _, uv := range [][2]uint16{{0, 65535}, {65535, 65535}, {0, 0}, {65535, 0}} {
		buf = binary.LittleEndian.AppendUint16(buf, uv[0])
		buf = binary.LittleEndian.AppendUint16(buf, uv[1])
	}
	// Indices (bytes) at 64.
	buf = append(buf, 0, 1, 2, 3)
	uri := "data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString(buf)
	return `{
  "asset": {"version": "2.0"},
  "scene": 0,
  "scenes": [{"nodes": [0]}],
  "nodes": [
    {"rotation": [0, 0.7071068, 0, 0.7071068], "children": [1]},
    {"mesh": 0, "translation": [0, 5, 0], "scale": [2, 2, 2]}
  ],
  "meshes": [{"primitives": [{"attributes": {"POSITION": 0, "TEXCOORD_0": 1}, "indices": 2, "mode": 5}]}],
  "accessors": [
    {"bufferView": 0, "componentType": 5126, "count": 4, "type": "VEC3"},
    {"bufferView": 1, "componentType": 5123, "normalized": true, "count": 4, "type": "VEC2"},
    {"bufferView": 2, "componentType": 5121, "count": 4, "type": "SCALAR"}
  ],
  "bufferViews": [
    {"buffer": 0, "byteOffset": 0, "byteLength": 48},
    {"buffer": 0, "byteOffset": 48, "byteLength": 16},
    {"buffer": 0, "byteOffset": 64, "byteLength": 4}
  ],
  "buffers": [{"uri": "` + uri + `", "byteLength": 68}]
}`
}

func TestGLTFNodesStripsAndNormalisedUVs(t *testing.T) {
	m, err := ParseGLTF([]byte(minimalGLTF(t)), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if m.TriangleCount() != 2 {
		t.Fatalf("a 4-index strip made %d triangles", m.TriangleCount())
	}
	if m.Normals != nil {
		t.Fatal("a mesh with no NORMAL came back with normals")
	}
	if len(m.Slots) != 1 || m.Slots[0].Name != "default" {
		t.Fatalf("slots %+v", m.Slots)
	}
	// Vertex 1 is (1,0,0): scaled by 2, lifted 5, then turned 90° about Y
	// (+X goes to -Z).
	p := m.Positions[1]
	if p.Sub(mgl32.Vec3{0, 5, -2}).Len() > 1e-5 {
		t.Fatalf("vertex 1 at %v, want (0, 5, -2)", p)
	}
	// UV (0, 65535) normalised is (0, 1) in glTF, (0, 0) once flipped.
	if m.UVs[0].Sub(mgl32.Vec2{0, 0}).Len() > 1e-6 || m.UVs[2].Sub(mgl32.Vec2{0, 1}).Len() > 1e-6 {
		t.Fatalf("uvs %v", m.UVs)
	}
	// The strip's second triangle is wound (2, 1, 3) to keep the facing.
	if m.Indices[3] != 2 || m.Indices[4] != 1 || m.Indices[5] != 3 {
		t.Fatalf("strip triangles %v", m.Indices)
	}
}

func TestGLTFSidecarBufferAndErrors(t *testing.T) {
	dir := t.TempDir()
	src := minimalGLTF(t)
	// Move the buffer out into a sidecar .bin.
	start := strings.Index(src, "data:")
	end := strings.Index(src[start:], `"`) + start
	raw, err := base64.StdEncoding.DecodeString(src[strings.Index(src, "base64,")+len("base64,") : end])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "strip data.bin"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	side := src[:start] + "strip%20data.bin" + src[end:]
	path := filepath.Join(dir, "strip.gltf")
	if err := os.WriteFile(path, []byte(side), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadGLTF(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.TriangleCount() != 2 || len(m.Files) != 2 {
		t.Fatalf("%d triangles from %d files", m.TriangleCount(), len(m.Files))
	}

	for name, doc := range map[string]string{
		"version 1":  strings.Replace(src, `"version": "2.0"`, `"version": "1.0"`, 1),
		"sparse":     strings.Replace(src, `"count": 4, "type": "VEC3"}`, `"count": 4, "type": "VEC3", "sparse": {"count": 1}}`, 1),
		"overrun":    strings.Replace(src, `"byteLength": 68}`, `"byteLength": 680}`, 1),
		"short view": strings.Replace(src, `"count": 4, "type": "SCALAR"`, `"count": 5, "type": "SCALAR"`, 1),
		"not json":   src + "x",
	} {
		if _, err := ParseGLTF([]byte(doc), dir); err == nil {
			t.Errorf("%s: parsed without complaint", name)
		}
	}
}
