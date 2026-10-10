package render

// scenery_gpu_cull_gl_test.go — the scenery culled on the GPU draws what the
// CPU's cull drew, colour and shadow depth, pixel for pixel; and what each
// costs the CPU a pass, on a field of props.
//
//	FOUNDRY_GL_TEST=1 go test -run SceneryGPUCull -v ./internal/render/

import (
	"testing"
	"time"

	"foundry/internal/gfx"
	"foundry/internal/glext"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// sceneryFieldLevel is n placements of three quad "models" strewn through a
// field in front of the probe's camera, most of them out of its view.
func sceneryFieldLevel(t *testing.T, n int) *world.Level {
	t.Helper()
	const h = 0.08
	verts := [][]float32{{-h, -h, 0, 0, 0}, {h, -h, 0, 1, 0}, {h, h, 0, 1, 1}, {-h, h, 0, 0, 1}}
	lvl := &world.Level{}
	var meshes []gfx.Geometry
	for m := 0; m < 3; m++ {
		g := gfx.Geometry{Alpha: 1, Tint: mgl32.Vec3{1, 0.3 + 0.3*float32(m), 0.2}}
		if err := g.Load(verts, []int{0, 1, 2, 0, 2, 3}, "", mgl32.Vec3{}); err != nil {
			t.Fatal(err)
		}
		meshes = append(meshes, g)
	}
	rng := uint32(99)
	next := func() float32 {
		rng ^= rng << 13
		rng ^= rng >> 17
		rng ^= rng << 5
		return float32(rng&0xffff) / 65536
	}
	for i := 0; i < n; i++ {
		m := i % 3
		p := mgl32.Vec3{next()*16 - 8, next()*6 - 3, next()*-12 + 1}
		s := world.NewScenery("prop", []string{"models/a.fmdl", "models/b.fmdl", "models/c.fmdl"}[m], p)
		s.Rotation = mgl32.Vec3{next(), next() * 3, 0}
		s.Submeshes = []gfx.Geometry{meshes[m]}
		s.LocalMin, s.LocalMax = mgl32.Vec3{-h, -h, -0.01}, mgl32.Vec3{h, h, 0.01}
		lvl.Scenery = append(lvl.Scenery, s)
	}
	t.Cleanup(func() {
		for i := range meshes {
			meshes[i].Delete()
		}
	})
	return lvl
}

func TestSceneryGPUCullDrawsWhatTheCPUCullDrew(t *testing.T) {
	newNormalProbe(t)
	gfx.InstancedUniform = world.ProgUniformLoc(gfx.Program, "instanced\x00")
	if !sceneryCull.ready() {
		t.Fatal("the scenery cull's programs did not build")
	}
	lvl := sceneryFieldLevel(t, 3000)
	projView := gfx.Projection.Mul4(gfx.Cam.Matrix)
	cam := ExtractFrustum(projView)
	prev := SceneryGPUCull
	defer func() { SceneryGPUCull = prev }()

	colour := func(gpu bool) ([]uint8, int) {
		SceneryGPUCull = gpu
		scenery = sceneryInstancer{}
		gl.Disable(gl.CULL_FACE)
		gl.Enable(gl.DEPTH_TEST)
		gl.ClearColor(0, 1, 0, 1)
		gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
		RenderScenery(lvl, &cam, func(g *gfx.Geometry, model mgl32.Mat4, _ bool) { g.DrawWithTransform(model) })
		px := make([]uint8, skyTestSize*skyTestSize*4)
		glext.ReadPixels(0, 0, skyTestSize, skyTestSize, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
		return px, DebugStats.Scenery
	}
	cpuPx, cpuN := colour(false)
	gpuPx, gpuN := colour(true)
	samePixels(t, "colour", cpuPx, gpuPx)
	if cpuN != gpuN || cpuN == 0 || cpuN == len(lvl.Scenery) {
		t.Errorf("counted %d placements in view on the GPU path, %d on the CPU's, of %d", gpuN, cpuN, len(lvl.Scenery))
	}

	// A shadow pass, culled by a box that takes some of the field.
	lsm := mgl32.Ortho(-3, 3, -3, 3, -20, 20).Mul4(mgl32.LookAtV(mgl32.Vec3{0, 5, -3}, mgl32.Vec3{0, 0, -3}, mgl32.Vec3{0, 0, -1}))
	box := ExtractFrustum(lsm)
	identity := mgl32.Ident4()
	depth := func(gpu bool) []float32 {
		SceneryGPUCull = gpu
		scenery = sceneryInstancer{}
		gl.UseProgram(Shadows.Program)
		gl.UniformMatrix4fv(Shadows.LSMUniform, 1, false, &lsm[0])
		gl.UniformMatrix4fv(Shadows.ModelUniform, 1, false, &identity[0])
		gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
		RenderSceneryShadow(lvl, Shadows.Program, &gfx.ShadowSkinUniforms, Shadows.ModelUniform, &box)
		out := make([]float32, skyTestSize*skyTestSize)
		glext.ReadPixels(0, 0, skyTestSize, skyTestSize, gl.DEPTH_COMPONENT, gl.FLOAT, gl.Ptr(&out[0]))
		return out
	}
	one, two := depth(false), depth(true)
	diff, drawn := 0, 0
	for i := range one {
		if one[i] != two[i] {
			diff++
		}
		if one[i] < 1 {
			drawn++
		}
	}
	if diff > 0 || drawn == 0 {
		t.Errorf("shadow depth: %d texels differ, %d drawn", diff, drawn)
	}

	// And moved: the GPU's copy follows.
	for i := 0; i < len(lvl.Scenery); i += 7 {
		lvl.Scenery[i].Position = lvl.Scenery[i].Position.Add(mgl32.Vec3{0.3, 0.1, 0})
	}
	SceneryGPUCull = true
	scenery = sceneryInstancer{}
	colour(true) // the GPU path's tables built before the move...
	for i := 0; i < len(lvl.Scenery); i += 5 {
		lvl.Scenery[i].Position = lvl.Scenery[i].Position.Add(mgl32.Vec3{-0.2, 0, 0.1})
	}
	SceneryGPUCull = true
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	RenderScenery(lvl, &cam, func(g *gfx.Geometry, model mgl32.Mat4, _ bool) { g.DrawWithTransform(model) })
	moved := make([]uint8, skyTestSize*skyTestSize*4)
	glext.ReadPixels(0, 0, skyTestSize, skyTestSize, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&moved[0]))
	want, _ := colour(false)
	samePixels(t, "after a move", want, moved)

	// What a pass costs the CPU each way: a camera pass and four cascades.
	cost := func(gpu bool) time.Duration {
		SceneryGPUCull = gpu
		scenery = sceneryInstancer{}
		draw := func(g *gfx.Geometry, model mgl32.Mat4, _ bool) {}
		pass := func() {
			RenderScenery(lvl, &cam, draw)
			// The shadow walker's program, as the shadow pass binds it.
			gl.UseProgram(Shadows.Program)
			for c := 0; c < 4; c++ {
				RenderSceneryShadow(lvl, Shadows.Program, &gfx.ShadowSkinUniforms, Shadows.ModelUniform, &box)
			}
		}
		pass()
		gl.Finish()
		start := time.Now()
		const frames = 30
		for f := 0; f < frames; f++ {
			pass()
		}
		gl.Finish()
		return time.Since(start) / frames
	}
	t.Logf("3000 props, a camera pass and four cascades: CPU cull %v a frame, GPU cull %v", cost(false), cost(true))
	if e := gl.GetError(); e != gl.NO_ERROR {
		t.Errorf("GL error 0x%x", e)
	}
}
