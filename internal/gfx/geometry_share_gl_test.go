package gfx_test

// geometry_share_gl_test.go — opt-in check that instances sharing a mesh share
// its buffers, each with a VAO of its own, and that the buffers go back to the
// driver with the last instance and not before.
//
//	FOUNDRY_GL_TEST=1 go test -run TestSharedMesh ./internal/gfx/

import (
	"testing"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/gfx"
	"foundry/internal/gltest"
)

func TestSharedMeshBuffersOutliveAllButTheLastInstance(t *testing.T) {
	gltest.Context(t)

	verts := [][]float32{{0, 0, 0, 0, 0}, {1, 0, 0, 1, 0}, {0, 1, 0, 0, 1}, {1, 1, 0, 1, 1}}
	tmpl := gfx.Geometry{AutoSmooth: true, Indexed: true, CPUOnly: true}
	if err := tmpl.Load(verts, []int{0, 1, 2, 2, 1, 3}, "", mgl32.Vec3{}); err != nil {
		t.Fatal(err)
	}
	var live *gfx.MeshShare
	a, b := tmpl, tmpl
	a.UploadShared(&live)
	b.UploadShared(&live)

	if a.Vao == 0 || b.Vao == 0 || a.Vao == b.Vao {
		t.Fatalf("VAOs %d and %d: each instance needs its own", a.Vao, b.Vao)
	}
	if a.Vbo == 0 || a.Vbo != b.Vbo || a.Ebo != b.Ebo || a.Nbo != b.Nbo {
		t.Fatalf("buffers not shared: vbo %d/%d ebo %d/%d nbo %d/%d", a.Vbo, b.Vbo, a.Ebo, b.Ebo, a.Nbo, b.Nbo)
	}
	if a.SharedWith() != 2 {
		t.Fatalf("SharedWith = %d, want 2", a.SharedWith())
	}
	vbo, bVao := a.Vbo, b.Vao

	a.Delete()
	if !gl.IsBuffer(vbo) {
		t.Fatal("the buffers went with the first instance")
	}
	if !gl.IsVertexArray(bVao) {
		t.Fatal("deleting one instance took the other's VAO")
	}
	if b.SharedWith() != 1 || !live.Live() {
		t.Fatalf("SharedWith = %d after one delete", b.SharedWith())
	}

	b.Delete()
	if gl.IsBuffer(vbo) {
		t.Error("the buffers outlived the last instance")
	}
	if live.Live() {
		t.Error("the share still reads as live")
	}

	// A new instance after the last one went uploads afresh.
	c := tmpl
	c.UploadShared(&live)
	if c.Vbo == 0 || !gl.IsBuffer(c.Vbo) || c.SharedWith() != 1 {
		t.Fatalf("a fresh instance got vbo %d, shared with %d", c.Vbo, c.SharedWith())
	}
	c.Delete()
}
