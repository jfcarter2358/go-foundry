package render

// point_shadow_cache_gl_test.go — a kept or layered point shadow, on a GPU.
//
// The planner deciding what to redraw is tested without GL; what is left is
// whether the cubemap it produces is the one a full redraw would have. The
// layered path is a static cube, a depth blit per face and the moving casters
// drawn over it, and a blit between mismatched framebuffers fails as a GL
// error with the target untouched — a shadow frozen at whatever it last was,
// with every test on the planner still green. So each path's cube is read back
// and compared with a full redraw of the same scene.
//
//	FOUNDRY_GL_TEST=1 go test -run TestLayeredPointShadow ./internal/render/

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/glext"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// readCube reads every face of a depth cubemap, a coarse sample of each so
// the comparison is quick.
func readCube(t *testing.T, cube uint32) []float32 {
	t.Helper()
	full := make([]float32, PointShadowMapSize*PointShadowMapSize)
	var out []float32
	gl.BindTexture(gl.TEXTURE_CUBE_MAP, cube)
	for face := 0; face < 6; face++ {
		glext.GetTexImage(uint32(gl.TEXTURE_CUBE_MAP_POSITIVE_X+face), 0, gl.DEPTH_COMPONENT, gl.FLOAT, gl.Ptr(full))
		for y := 0; y < PointShadowMapSize; y += 16 {
			for x := 0; x < PointShadowMapSize; x += 16 {
				out = append(out, full[y*PointShadowMapSize+x])
			}
		}
	}
	gl.BindTexture(gl.TEXTURE_CUBE_MAP, 0)
	if e := gl.GetError(); e != gl.NO_ERROR {
		t.Fatalf("reading the cube: GL error 0x%X", e)
	}
	return out
}

func TestLayeredPointShadowMatchesAFullRedraw(t *testing.T) {
	newNormalProbe(t)

	// A floor and a pillar that never move, and a character-sized box that
	// does, all within reach of one shadowed light.
	floor := world.NewBoxBrush("floor", mgl32.Vec3{0, -0.5, 0}, mgl32.Vec3{6, 0.5, 6}, "")
	pillar := world.NewBoxBrush("pillar", mgl32.Vec3{2, 1, 0}, mgl32.Vec3{0.3, 1, 0.3}, "")
	ch := world.Chunk{ID: "room", Scale: 1, Brushes: []world.Brush{floor, pillar}}
	for bi := range ch.Brushes {
		if err := ch.Brushes[bi].Load(); err != nil {
			t.Fatal(err)
		}
	}
	ch.RecomputeBounds()

	var body gfx.Geometry
	h := float32(0.4)
	quad := [][]float32{{-h, 0, -h, 0, 0}, {h, 0, -h, 1, 0}, {h, 1.8, h, 1, 1}, {-h, 1.8, h, 0, 1}}
	if err := body.Load(quad, []int{0, 1, 2, 0, 2, 3}, "", mgl32.Vec3{}); err != nil {
		t.Fatal(err)
	}
	body.Alpha = 1
	t.Cleanup(body.Delete)

	lvl := &world.Level{
		Chunks: []world.Chunk{ch},
		Entities: []world.Entity{{ID: "walker", Visible: true, Position: mgl32.Vec3{-1, 0, 0},
			BoundHalfX: 0.5, BoundHalfY: 1, BoundHalfZ: 0.5,
			Models: []world.EntityModel{{Visible: true, Submeshes: []gfx.Geometry{body}}}}},
	}
	t.Cleanup(func() { ReleaseChunkBatches(lvl) })
	lights := world.LightSet{Points: []world.PointLight{{
		Position: mgl32.Vec3{0, 3, 0}, Radius: 10, CastShadows: true, ShadowStrength: 1,
		Color: mgl32.Vec3{1, 1, 1}, Brightness: 1,
	}}}

	pointShadowStates = nil
	t.Cleanup(func() {
		pointShadowStates = nil
		PointShadowCaching = true
		EnsurePointShadowMaps(0)
	})
	full := func() []float32 {
		t.Helper()
		PointShadowCaching = false
		RenderPointShadowPass(lights, lvl)
		PointShadowCaching = true
		pointShadowStates = nil
		return readCube(t, Shadows.PointMaps[0].Cubemap)
	}
	cached := func(what string, want func(pointShadowPlan) bool) []float32 {
		t.Helper()
		// The plan is re-derived here only to check the scene exercised the
		// path it was meant to; the pass makes its own.
		saved := append([]pointShadowState(nil), pointShadowStates...)
		if got := planPointShadow(0, lvl, lights.Points[0], &Shadows.PointMaps[0]); !want(got) {
			t.Fatalf("%s: the scene planned %+v, not the path this case is for", what, got)
		}
		pointShadowStates = saved
		RenderPointShadowPass(lights, lvl)
		if e := gl.GetError(); e != gl.NO_ERROR {
			t.Fatalf("%s: GL error 0x%X", what, e)
		}
		return readCube(t, Shadows.PointMaps[0].Cubemap)
	}
	same := func(what string, got, want []float32) {
		t.Helper()
		diff := 0
		for i := range want {
			if d := got[i] - want[i]; d > 1e-5 || d < -1e-5 {
				diff++
			}
		}
		if diff > 0 {
			t.Fatalf("%s: %d of %d sampled texels differ from a full redraw", what, diff, len(want))
		}
	}

	gl.GetError()
	EnsurePointShadowMaps(1)
	want := full()
	same("first render, static layer and walker", cached("first render", func(p pointShadowPlan) bool {
		return p.static && p.faces == allPointShadowFaces
	}), want)
	same("kept", cached("nothing changed", pointShadowPlan.keep), want)

	// The walker moves: only the moving layer redraws, over the kept static
	// cube — which must still hold the room, not the walker's old position —
	// and only in the faces that see the walker, then or now. The faces that
	// do not are kept from before, and have to match a full redraw too.
	lvl.Entities[0].Position = mgl32.Vec3{0, 0, 2}
	moved := cached("walker moved", func(p pointShadowPlan) bool {
		return !p.static && !p.all && !p.keep() && p.faces != allPointShadowFaces
	})
	pointShadowStates = nil
	same("walker moved, drawn over the kept static layer in its faces alone", moved, full())
}
