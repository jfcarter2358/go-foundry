package editor

// editor_generator_scatter_gl_test.go — instanced preview meshes draw every
// instance, each where its matrix puts it, against a real context:
//
//	FOUNDRY_GL_TEST=1 go test -run TestGeneratorGLInstances ./internal/editor/
//
// Three instances of one cube along X (scaled twice over), framed by their instance bounds: the
// left, middle and right thirds of the picture each hold one, and the view
// built one mesh with an instance buffer for them.

import (
	"testing"

	"foundry/internal/glctx"
	"foundry/internal/procgen"
	"foundry/internal/uiwin"

	"github.com/go-gl/gl/v4.3-core/gl"
)

// cubeMesh is a unit cube about the origin.
func cubeMesh() procgen.PreviewMesh {
	m := procgen.PreviewMesh{Name: "cube"}
	for k := 0; k < 8; k++ {
		m.Positions = append(m.Positions, [3]float32{float32(k&1) - 0.5, float32(k>>1&1) - 0.5, float32(k>>2&1) - 0.5})
	}
	quads := [][4]uint32{{0, 2, 3, 1}, {4, 5, 7, 6}, {0, 1, 5, 4}, {2, 6, 7, 3}, {0, 4, 6, 2}, {1, 3, 7, 5}}
	for _, q := range quads {
		m.Indices = append(m.Indices, q[0], q[1], q[2], q[0], q[2], q[3])
	}
	return m
}

func TestGeneratorGLInstancesDrawWhereTheirMatricesSay(t *testing.T) {
	generatorGLSetup(t)
	genSetDoc(scatterEditorDoc(), "")
	genSelected = 2 // the scatter stage: the preview below is its
	m := cubeMesh()
	for _, x := range []float32{-10, 0, 10} {
		m.Instances = append(m.Instances, [16]float32{2, 0, 0, 0, 0, 2, 0, 0, 0, 0, 2, 0, x, 0, 0, 1})
	}
	p := &procgen.PreviewData{Meshes: []procgen.PreviewMesh{m}}
	genPreview.data, genPreview.dataStage = p, "scatter"
	t.Cleanup(func() { genPreview.data, genPreview.dataStage = nil, "" })
	genV3.framed = false
	if !genEnsureView3D(p) {
		t.Fatal("the preview program did not compile")
	}
	if len(genV3.meshes) != 1 || len(genV3.instN) != 1 || genV3.instN[0] != 3 || genV3.instBuf[0] == 0 {
		t.Fatalf("%d meshes, instances %v", len(genV3.meshes), genV3.instN)
	}
	if lo, hi := genV3.lo, genV3.hi; lo[0] > -10.9 || hi[0] < 10.9 || hi[1] < 0.9 {
		t.Errorf("framed on %v..%v, the instances span x -11..11, y -1..1", lo, hi)
	}
	// Square on, from the front.
	genV3.cam.Yaw, genV3.cam.Pitch = -1.5707963, 0
	genV3.cam.Dist = 8
	genV3.grid = false
	for gl.GetError() != gl.NO_ERROR {
	}
	const w, h = 150, 50
	target, err := glctx.NewTarget(w, h)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Delete()
	target.Bind()
	gl.Viewport(0, 0, w, h)
	gl.ClearColor(0, 1, 0, 1)
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	genDrawView3D(&uiwin.UIWindow{W: w, H: h})
	img := target.Image()
	target.Unbind()
	if e := gl.GetError(); e != gl.NO_ERROR {
		t.Errorf("GL error 0x%x after the draw", e)
	}
	var thirds [3]int
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if c := img.NRGBAAt(x, y); !(c.R == 0 && c.G == 255 && c.B == 0) {
				thirds[x*3/w]++
			}
		}
	}
	for k, n := range thirds {
		if n < 30 {
			t.Errorf("third %d of the picture holds %d cube pixels (thirds %v)", k, n, thirds)
		}
	}
}
