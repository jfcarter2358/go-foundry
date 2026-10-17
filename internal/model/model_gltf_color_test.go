package model

// model_gltf_color_test.go — vertex colours (COLOR_0) through glTF: written
// by EncodeGLB as normalised RGBA8, read back exactly, VEC3 float colours
// read as opaque, and a mesh with none still encodes to the bytes it did
// before colours existed (every cook hash made from a GLB depends on that).

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

func TestGLBVertexColoursRoundTrip(t *testing.T) {
	src := twoSlotQuad()
	src.Colors = [][4]uint8{{255, 0, 0, 255}, {0, 255, 0, 128}, {0, 0, 255, 0}, {10, 20, 30, 40}, {255, 255, 255, 255}, {1, 2, 3, 4}}
	glb, err := EncodeGLB(src)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(glb, []byte(`"COLOR_0"`)) || !bytes.Contains(glb, []byte(`"normalized":true`)) {
		t.Fatal("the GLB has no normalised COLOR_0")
	}
	got, err := ParseGLTF(glb, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Colors) != len(got.Positions) {
		t.Fatalf("%d colours for %d positions", len(got.Colors), len(got.Positions))
	}
	// Vertices come back per primitive, so match them by position.
	want := map[[3]float32][4]uint8{}
	for i, p := range src.Positions {
		want[[3]float32(p)] = src.Colors[i]
	}
	for i, p := range got.Positions {
		if c := want[[3]float32(p)]; got.Colors[i] != c {
			t.Errorf("vertex at %v: colour %v, want %v", p, got.Colors[i], c)
		}
	}
}

func TestGLBWithoutColoursIsUnchanged(t *testing.T) {
	src := twoSlotQuad()
	glb, err := EncodeGLB(src)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(glb, []byte("COLOR_0")) || bytes.Contains(glb, []byte("normalized")) {
		t.Fatal("a mesh without colours wrote COLOR_0")
	}
	got, err := ParseGLTF(glb, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got.Colors != nil {
		t.Fatal("a mesh without COLOR_0 came back with colours")
	}
}

func TestGLTFFloatVec3ColoursAreOpaque(t *testing.T) {
	// One triangle; COLOR_0 as float VEC3 (0.5, 1, 0) on every vertex.
	var bin bytes.Buffer
	put := func(v any) { _ = binary.Write(&bin, binary.LittleEndian, v) }
	for _, p := range [][3]float32{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}} {
		put(p)
	}
	for i := 0; i < 3; i++ {
		put([3]float32{0.5, 1, 0})
	}
	uri := "data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString(bin.Bytes())
	js := `{"asset":{"version":"2.0"},"meshes":[{"primitives":[{"attributes":{"POSITION":0,"COLOR_0":1}}]}],
		"accessors":[{"bufferView":0,"componentType":5126,"count":3,"type":"VEC3"},
		             {"bufferView":0,"byteOffset":36,"componentType":5126,"count":3,"type":"VEC3"}],
		"bufferViews":[{"buffer":0,"byteLength":72}],"buffers":[{"byteLength":72,"uri":"` + uri + `"}]}`
	m, err := ParseGLTF([]byte(js), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := [4]uint8{uint8(math.Round(0.5 * 255)), 255, 0, 255}
	for i, c := range m.Colors {
		if c != want {
			t.Errorf("vertex %d: %v, want %v", i, c, want)
		}
	}
	// Two components is not a colour.
	bad := strings.Replace(js, `"count":3,"type":"VEC3"}]`, `"count":3,"type":"VEC2"}]`, 1)
	if _, err := ParseGLTF([]byte(bad), t.TempDir()); err == nil || !strings.Contains(err.Error(), "COLOR_0") {
		t.Fatalf("a VEC2 COLOR_0 was taken (err %v)", err)
	}
}
