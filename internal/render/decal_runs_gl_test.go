package render

// decal_runs_gl_test.go — decals drawn in runs draw what they drew one at a
// time, pixel for pixel.
//
//	FOUNDRY_GL_TEST=1 go test -run TestDecalRunsDrawWhatPiecesDrew ./internal/render/

import (
	"bytes"
	"fmt"
	"testing"

	"foundry/internal/glext"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

func TestDecalRunsDrawWhatPiecesDrew(t *testing.T) {
	newNormalProbe(t)
	lvl := decalGLLevel(t)
	// A wall of overlapping marks: most alike — the runs — and among them a
	// few tinted differently, faded, or ordered over the rest, each of which
	// has to break a run where it falls.
	for i := 0; i < 40; i++ {
		x, y := float32(i%8)*0.4-1.4, float32(i/8)*0.4-0.9
		d := world.NewDecal(fmt.Sprintf("hole%d", i), mgl32.Vec3{x, y, 0}, mgl32.Vec3{0, 0, 1})
		d.Width, d.Height, d.Depth = 0.6, 0.6, 1
		d.Unlit = true
		d.Tint = mgl32.Vec3{0.2, 0.2, 0.2}
		switch {
		case i%7 == 3:
			d.Tint = mgl32.Vec3{0.8, 0.1, 0.1}
		case i%11 == 5:
			d.Alpha = 0.5
		case i%13 == 4:
			d.Order = 1
		}
		lvl.Decals = append(lvl.Decals, d)
	}
	// Every mark built before either picture: the pass rebuilds only a few a
	// frame (DecalRebuildBudget).
	world.SyncDecals(lvl, len(lvl.Decals))
	draw := func(batched bool) []byte {
		prev := decalBatching
		decalBatching = batched
		defer func() { decalBatching = prev }()
		gl.Disable(gl.CULL_FACE)
		gl.Enable(gl.DEPTH_TEST)
		gl.ClearColor(0, 1, 0, 1)
		gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
		drawGLWall(lvl)
		RenderDecals(lvl, nil)
		px := make([]byte, skyTestSize*skyTestSize*4)
		glext.ReadPixels(0, 0, skyTestSize, skyTestSize, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
		return px
	}
	alone := draw(false)
	runs := draw(true)
	if !bytes.Equal(alone, runs) {
		diff := 0
		for i := range alone {
			if alone[i] != runs[i] {
				diff++
			}
		}
		t.Errorf("decals drawn in runs differ from drawn alone in %d bytes", diff)
		shown := 0
		for i := 0; i+3 < len(alone) && shown < 8; i += 4 {
			if !bytes.Equal(alone[i:i+4], runs[i:i+4]) {
				t.Logf("pixel %d,%d: alone %v, runs %v", (i/4)%skyTestSize, (i/4)/skyTestSize, alone[i:i+4], runs[i:i+4])
				shown++
			}
		}
	}
	if len(decalRuns.runs) == 0 {
		t.Error("no run had more than one piece; the test is not measuring the runs")
	}
	// And a second frame reuses the runs' buffers rather than remaking them.
	vao := decalRuns.runs[0].batch.Vao
	again := draw(true)
	if !bytes.Equal(alone, again) {
		t.Error("the second frame's runs differ from the pieces drawn alone")
	}
	if decalRuns.runs[0].batch.Vao != vao {
		t.Error("a run was rebuilt with nothing changed")
	}
	if e := gl.GetError(); e != gl.NO_ERROR {
		t.Errorf("GL error 0x%x", e)
	}
}
