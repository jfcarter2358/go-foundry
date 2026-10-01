package render

// scenery_instanced_gl_test.go — instanced scenery draws what placement-by-
// placement drawing drew, pixel for pixel.
//
//	FOUNDRY_GL_TEST=1 go test -run SceneryInstanc ./internal/render/

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// sceneryProbeLevel is a level of n placements of one small quad "model",
// each moved, turned and scaled differently, with the given alpha.
func sceneryProbeLevel(t *testing.T, alpha float32) *world.Level {
	t.Helper()
	const h = 0.5
	verts := [][]float32{{-h, -h, 0, 0, 0}, {h, -h, 0, 1, 0}, {h, h, 0, 1, 1}, {-h, h, 0, 0, 1}}
	lvl := &world.Level{}
	for i, p := range []mgl32.Vec3{{-1, 0.5, 0}, {0.2, -0.4, -0.5}, {1, 0.6, 0.3}} {
		g := gfx.Geometry{Alpha: alpha}
		if err := g.Load(verts, []int{0, 1, 2, 0, 2, 3}, "", mgl32.Vec3{}); err != nil {
			t.Fatal(err)
		}
		s := world.NewScenery("rock", "models/rock.fmdl", p)
		s.Rotation = mgl32.Vec3{0, 0.3 * float32(i), 0.2 * float32(i)}
		s.Scale = mgl32.Vec3{1 + 0.3*float32(i), 1, 1}
		s.Submeshes = []gfx.Geometry{g}
		s.LocalMin, s.LocalMax = mgl32.Vec3{-h, -h, -0.01}, mgl32.Vec3{h, h, 0.01}
		lvl.Scenery = append(lvl.Scenery, s)
	}
	t.Cleanup(func() {
		for i := range lvl.Scenery {
			lvl.Scenery[i].Submeshes[0].Delete()
		}
	})
	return lvl
}

// drawSceneryFrame clears, draws lvl's scenery through RenderScenery (queued
// draws drawn at once, as the frame's transparent pass would) and reads the
// whole target back.
func drawSceneryFrame(t *testing.T, lvl *world.Level, instanced bool) []uint8 {
	t.Helper()
	prev := sceneryInstancing
	sceneryInstancing = instanced
	defer func() { sceneryInstancing = prev }()

	gl.Disable(gl.CULL_FACE)
	gl.ClearColor(0, 1, 0, 1)
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	RenderScenery(lvl, nil, func(g *gfx.Geometry, model mgl32.Mat4, _ bool) {
		g.DrawWithTransform(model)
	})
	if DebugStats.Scenery != len(lvl.Scenery) {
		t.Errorf("instanced=%v counted %d placements drawn, want %d", instanced, DebugStats.Scenery, len(lvl.Scenery))
	}
	px := make([]uint8, skyTestSize*skyTestSize*4)
	gl.ReadPixels(0, 0, skyTestSize, skyTestSize, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
	return px
}

func samePixels(t *testing.T, what string, a, b []uint8) {
	t.Helper()
	diff, background := 0, 0
	for i := 0; i < len(a); i += 4 {
		if a[i] != b[i] || a[i+1] != b[i+1] || a[i+2] != b[i+2] {
			diff++
		}
		if a[i] == 0 && a[i+1] == 255 && a[i+2] == 0 {
			background++
		}
	}
	if diff > 0 {
		t.Errorf("%s: %d pixels differ between the instanced and the one-by-one draw", what, diff)
	}
	if background == len(a)/4 {
		t.Errorf("%s: nothing was drawn at all", what)
	}
}

func TestSceneryInstancedDrawsWhatOneByOneDrew(t *testing.T) {
	newNormalProbe(t)
	gfx.InstancedUniform = world.ProgUniformLoc(gfx.Program, "instanced\x00")
	scenery = sceneryInstancer{}

	opaque := sceneryProbeLevel(t, 1)
	samePixels(t, "opaque", drawSceneryFrame(t, opaque, false), drawSceneryFrame(t, opaque, true))

	// A transparent model stays on the per-placement path; the picture must
	// not change either way.
	gl.Enable(gl.BLEND)
	gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)
	glass := sceneryProbeLevel(t, 0.5)
	samePixels(t, "transparent", drawSceneryFrame(t, glass, false), drawSceneryFrame(t, glass, true))
	gl.Disable(gl.BLEND)

	// Moving a placement moves its instance: the cached matrix notices.
	opaque.Scenery[0].Position = mgl32.Vec3{-0.5, -0.8, 0.2}
	samePixels(t, "after a move", drawSceneryFrame(t, opaque, false), drawSceneryFrame(t, opaque, true))
}

// The shadow pass draws the same depth instanced as one by one: drawn through
// the cascade program with the probe's own camera as the light, and the depth
// buffers compared.
func TestSceneryInstancedShadowsMatchOneByOne(t *testing.T) {
	newNormalProbe(t)
	scenery = sceneryInstancer{}
	lvl := sceneryProbeLevel(t, 1)
	lsm := gfx.Projection.Mul4(gfx.Cam.Matrix)
	identity := mgl32.Ident4()

	depth := func(instanced bool) []float32 {
		prev := sceneryInstancing
		sceneryInstancing = instanced
		defer func() { sceneryInstancing = prev }()
		gl.UseProgram(Shadows.Program)
		gl.UniformMatrix4fv(Shadows.LSMUniform, 1, false, &lsm[0])
		gl.UniformMatrix4fv(Shadows.ModelUniform, 1, false, &identity[0])
		gl.Disable(gl.CULL_FACE)
		gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
		RenderSceneryShadow(lvl, Shadows.Program, &gfx.ShadowSkinUniforms, Shadows.ModelUniform, nil)
		out := make([]float32, skyTestSize*skyTestSize)
		gl.ReadPixels(0, 0, skyTestSize, skyTestSize, gl.DEPTH_COMPONENT, gl.FLOAT, gl.Ptr(&out[0]))
		return out
	}
	one, inst := depth(false), depth(true)
	diff, drawn := 0, 0
	for i := range one {
		if one[i] != inst[i] {
			diff++
		}
		if one[i] < 1 {
			drawn++
		}
	}
	if drawn == 0 {
		t.Fatal("no depth was written either way")
	}
	if diff > 0 {
		t.Errorf("%d depth samples differ between the instanced and the one-by-one shadow draw", diff)
	}
	var m mgl32.Mat4
	gl.GetUniformfv(Shadows.Program, Shadows.ModelUniform, &m[0])
	if m != identity {
		t.Error("the shadow draw left a placement's matrix in `model` for the walls drawn after it")
	}
}
