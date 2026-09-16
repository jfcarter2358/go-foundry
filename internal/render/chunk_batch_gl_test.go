package render

// chunk_batch_gl_test.go — a batched chunk looks exactly like the same chunk
// drawn a face at a time, on a GPU.
//
// The batch sets each material's uniforms once where a Geometry sets them per
// surface, so the way this goes wrong is a uniform the batch forgot or set from
// the wrong material: a textured wall painted in normals, a blend that leaks, a
// smooth surface shaded flat. Drawing one scene both ways and comparing every
// pixel catches all of them without having to name each.
//
//	FOUNDRY_GL_TEST=1 go test -run TestABatchedChunk ./internal/render/

import (
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// perFaceGeometry re-uploads a batched face as a standalone Geometry, the way
// every brush face was drawn before batching.
func perFaceGeometry(t *testing.T, f *world.BrushFace) *gfx.Geometry {
	t.Helper()
	src := &f.Geometry
	n := len(src.Vertices) / 5
	verts := make([][]float32, n)
	idx := make([]int, n)
	for i := 0; i < n; i++ {
		verts[i] = src.Vertices[i*5 : i*5+5]
		idx[i] = i
	}
	g := &gfx.Geometry{Smooth: src.Smooth, SmoothAngle: src.SmoothAngle}
	if err := g.Load(verts, idx, src.Texture, mgl32.Vec3{}); err != nil {
		t.Fatal(err)
	}
	g.Alpha, g.BlendTexture, g.BlendMask = src.Alpha, src.BlendTexture, src.BlendMask
	return g
}

func TestABatchedChunkDrawsExactlyWhatPerFaceDrawingDid(t *testing.T) {
	newNormalProbe(t) // the world program, its uniforms and a target
	gfx.TextureNames = gfx.ScanTextures(fileutil.TexturesDir())
	for _, tex := range []string{"concrete", "brick", "square", "stucco"} {
		if err := gfx.EnsureTextureLoaded(tex); err != nil {
			t.Fatalf("load %s: %v", tex, err)
		}
	}
	// A directional light as well as the ambient, so a normal that came out
	// wrong — smooth shaded flat, or the reverse — changes the pixels.
	var ls world.LightSet
	ls.Global.Color, ls.Global.Brightness = mgl32.Vec3{1, 1, 1}, 0.3
	ls.Directional = []world.DirectionalLight{{Color: mgl32.Vec3{1, 1, 1}, Brightness: 1, Yaw: 0.7, Pitch: -0.8}}
	ls.Directional[0].SyncDirection()
	world.UploadLightsTo(gfx.Program, ls, false)
	UploadShadowUniformsTo(gfx.Program, ls, ShadowFrame{})
	gl.UseProgram(gfx.Program)

	gfx.Cam.Position = mgl32.Vec3{1.5, 2, 9}
	gfx.Cam.Direction = mgl32.Vec3{0, -0.2, -1}.Normalize()
	gfx.UpdateCamMatrix()

	ch := world.Chunk{ID: "scene", Scale: 1}
	add := func(center, half mgl32.Vec3, tex string, edit func(f *world.BrushFace)) {
		b := world.NewBoxBrush("b", center, half, tex)
		for fi := range b.Faces {
			if edit != nil {
				edit(&b.Faces[fi])
			}
		}
		ch.Brushes = append(ch.Brushes, b)
	}
	add(mgl32.Vec3{-2, 0, 0}, mgl32.Vec3{1, 1, 1}, "concrete", nil)
	add(mgl32.Vec3{0, 0, -1}, mgl32.Vec3{1, 1, 1}, "", nil) // untextured: painted in normals
	add(mgl32.Vec3{2.5, 0, 0}, mgl32.Vec3{1, 1, 1}, "stucco", func(f *world.BrushFace) {
		f.Smooth, f.SmoothAngle = true, 180
	})
	// Smooth normals are welded within a face, so a planar face shades the
	// same either way: lift one top corner, bending the faces that share it.
	smooth := &ch.Brushes[len(ch.Brushes)-1]
	for i, v := range smooth.Verts {
		if v.X() > 3 && v.Y() > 0.5 && v.Z() > 0.5 {
			smooth.Verts[i] = v.Add(mgl32.Vec3{0, 0.8, 0})
		}
	}
	add(mgl32.Vec3{0, -1.5, 0}, mgl32.Vec3{5, 0.25, 3}, "brick", func(f *world.BrushFace) {
		f.BlendTexture, f.BlendMask = "concrete", "square"
	})
	add(mgl32.Vec3{0.5, 0.5, 2}, mgl32.Vec3{0.75, 0.75, 0.75}, "concrete", func(f *world.BrushFace) {
		f.Alpha = 0.5
	})
	add(mgl32.Vec3{4, 1, 1}, mgl32.Vec3{0.5, 0.5, 0.5}, "brick", func(f *world.BrushFace) {
		f.DoubleSided = true
	})
	for bi := range ch.Brushes {
		if err := ch.Brushes[bi].Load(); err != nil {
			t.Fatal(err)
		}
	}
	ch.RecomputeBounds()
	lvl := &world.Level{Chunks: []world.Chunk{ch}}
	t.Cleanup(func() { ReleaseChunkBatches(lvl) })
	frustum := ExtractFrustum(gfx.Projection.Mul4(gfx.Cam.Matrix))

	capture := func(draw func(opaque bool)) []uint8 {
		gl.ClearColor(0, 1, 0, 1)
		gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
		gl.Disable(gl.CULL_FACE)
		draw(true)
		gl.Enable(gl.BLEND)
		gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)
		gl.DepthMask(false)
		draw(false)
		gl.DepthMask(true)
		gl.Disable(gl.BLEND)
		px := make([]uint8, skyTestSize*skyTestSize*4)
		gl.ReadPixels(0, 0, skyTestSize, skyTestSize, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
		return px
	}

	var perFace []*gfx.Geometry
	for bi := range lvl.Chunks[0].Brushes {
		b := &lvl.Chunks[0].Brushes[bi]
		for fi := range b.Faces {
			perFace = append(perFace, perFaceGeometry(t, &b.Faces[fi]))
		}
	}
	t.Cleanup(func() {
		for _, g := range perFace {
			g.Delete()
		}
	})
	want := capture(func(opaque bool) {
		for _, g := range perFace {
			if g.IsTransparent() != opaque {
				g.DrawWithTransform(mgl32.Ident4())
			}
		}
	})

	var queued []BatchDraw
	got := capture(func(opaque bool) {
		if opaque {
			queued = queued[:0]
			DrawChunkBrushes(lvl, &lvl.Chunks[0], frustum, mgl32.Ident4(), gfx.Cam.Position,
				func(d BatchDraw, _ float32) { queued = append(queued, d) })
			return
		}
		for i := range queued {
			queued[i].Draw()
		}
	})
	if len(queued) != 1 {
		t.Errorf("queued %d transparent draws, want the one translucent box", len(queued))
	}

	green, diff := 0, 0
	for i := 0; i < len(want); i += 4 {
		if want[i] == 0 && want[i+1] == 255 && want[i+2] == 0 {
			green++
		}
		for c := 0; c < 3; c++ {
			d := int(want[i+c]) - int(got[i+c])
			if d > 2 || d < -2 {
				diff++
				break
			}
		}
	}
	if green > len(want)/4*9/10 {
		t.Fatalf("the per-face render is %d%% clear colour; the scene is not in view", green*100/(len(want)/4))
	}
	if diff > 0 {
		t.Errorf("%d of %d pixels differ between per-face and batched drawing", diff, len(want)/4)
	}
}

// A compiled level's surfaces carry baked light, which is the one attribute and
// uniform chunk batches never use — so the static batch path gets the same
// comparison, with light baked unevenly across the corners so a batch that
// dropped or misaligned it could not match.
func TestABatchedCompiledLevelDrawsExactlyWhatPerSurfaceDrawingDid(t *testing.T) {
	newNormalProbe(t)
	gfx.TextureNames = gfx.ScanTextures(fileutil.TexturesDir())
	for _, tex := range []string{"concrete", "brick"} {
		if err := gfx.EnsureTextureLoaded(tex); err != nil {
			t.Fatalf("load %s: %v", tex, err)
		}
	}
	gfx.Cam.Position = mgl32.Vec3{0, 1, 8}
	gfx.Cam.Direction = mgl32.Vec3{0, -0.1, -1}.Normalize()
	gfx.UpdateCamMatrix()

	quad := func(x0, x1, y0, y1, z float32, tex string, alpha float32, cpuOnly bool) world.StaticSurface {
		verts := [][]float32{{x0, y0, z, 0, 0}, {x1, y0, z, 1, 0}, {x1, y1, z, 1, 1}, {x0, y1, z, 0, 1}}
		var g gfx.Geometry
		g.CPUOnly = cpuOnly
		g.VertexLight = [][]float32{{1, 0.2, 0.2}, {0.2, 1, 0.2}, {0.2, 0.2, 1}, {1, 1, 1}}
		if err := g.Load(verts, []int{0, 1, 2, 0, 2, 3}, tex, mgl32.Vec3{}); err != nil {
			t.Fatal(err)
		}
		g.Alpha = alpha
		return world.StaticSurface{Geometry: g, Chunk: "c", Lo: mgl32.Vec3{x0, y0, z}, Hi: mgl32.Vec3{x1, y1, z}}
	}
	build := func(cpuOnly bool) *world.StaticGeometry {
		return &world.StaticGeometry{Surfaces: []world.StaticSurface{
			quad(-3, -0.5, -1, 2, 0, "concrete", 1, cpuOnly),
			quad(0.5, 3, -1, 2, 0, "brick", 1, cpuOnly),
			quad(-1, 1, -0.5, 1.5, 1, "concrete", 0.5, cpuOnly),
		}}
	}
	capture := func(draw func(opaque bool)) []uint8 {
		gl.ClearColor(0, 1, 0, 1)
		gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
		gl.Disable(gl.CULL_FACE)
		draw(true)
		gl.Enable(gl.BLEND)
		gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)
		gl.DepthMask(false)
		draw(false)
		gl.DepthMask(true)
		gl.Disable(gl.BLEND)
		px := make([]uint8, skyTestSize*skyTestSize*4)
		gl.ReadPixels(0, 0, skyTestSize, skyTestSize, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
		return px
	}

	perSurface := build(false)
	t.Cleanup(perSurface.Release)
	want := capture(func(opaque bool) {
		for i := range perSurface.Surfaces {
			if g := &perSurface.Surfaces[i].Geometry; g.IsTransparent() != opaque {
				g.Draw()
			}
		}
	})

	lvl := &world.Level{Static: build(true)}
	t.Cleanup(func() { ReleaseChunkBatches(lvl) })
	frustum := ExtractFrustum(gfx.Projection.Mul4(gfx.Cam.Matrix))
	var queued []BatchDraw
	got := capture(func(opaque bool) {
		if !opaque {
			for i := range queued {
				queued[i].Draw()
			}
			return
		}
		n, calls := DrawStaticSurfaces(lvl, []int{0, 1, 2}, frustum, gfx.Cam.Position,
			func(d BatchDraw, _ float32) { queued = append(queued, d) })
		if n != 3 || calls != 2 || len(queued) != 1 {
			t.Errorf("drew %d surfaces in %d calls with %d queued; want 3 in 2 with the translucent one queued", n, calls, len(queued))
		}
	})

	diff := 0
	for i := 0; i < len(want); i += 4 {
		for c := 0; c < 3; c++ {
			if d := int(want[i+c]) - int(got[i+c]); d > 2 || d < -2 {
				diff++
				break
			}
		}
	}
	if diff > 0 {
		t.Errorf("%d of %d pixels differ between per-surface and batched drawing of baked surfaces", diff, len(want)/4)
	}
}
