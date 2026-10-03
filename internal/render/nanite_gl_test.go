package render

// nanite_gl_test.go — the GPU-driven cluster cull (nanite_cull.go) against the
// CPU paths it stands in front of. Phase 1 of the Nanite plan.
//
//   - A compiled level's opaque surfaces drawn by the GPU cull, over several
//     frames of the two-pass occlusion (first frame: pass 2 draws it all; later
//     frames: pass 1 draws last frame's, pass 2 the rest), are the CPU path's
//     picture pixel for pixel.
//   - The occlusion pass culls: clusters wholly behind a wall are recorded as
//     not visible and drawn by neither pass, and come back when it goes.
//   - The normal cone culls a cluster seen from behind, and only then.
//   - The shadow pass culled per cluster writes the CPU path's depth.
//   - Cooked Nanite scenery (a .fmdl cooked, and a dense procedural rock)
//     drawn by the GPU cull is the classic path's picture and shadow.
//
//	FOUNDRY_GL_TEST=1 go test -run Nanite ./internal/render/

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"foundry/internal/gfx"
	"foundry/internal/glext"
	"foundry/internal/model"
	"foundry/internal/nanite"
	"foundry/internal/nanitecook"
	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

const naniteTestSize = 192

// naniteTestTarget binds a fresh colour + depth framebuffer of naniteTestSize.
func naniteTestTarget(t *testing.T) {
	t.Helper()
	var color, depth, fbo uint32
	gl.GenTextures(1, &color)
	gl.BindTexture(gl.TEXTURE_2D, color)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, naniteTestSize, naniteTestSize, 0, gl.RGBA, gl.UNSIGNED_BYTE, nil)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
	gl.GenRenderbuffers(1, &depth)
	gl.BindRenderbuffer(gl.RENDERBUFFER, depth)
	gl.RenderbufferStorage(gl.RENDERBUFFER, gl.DEPTH_COMPONENT24, naniteTestSize, naniteTestSize)
	gl.GenFramebuffers(1, &fbo)
	gl.BindFramebuffer(gl.FRAMEBUFFER, fbo)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, color, 0)
	gl.FramebufferRenderbuffer(gl.FRAMEBUFFER, gl.DEPTH_ATTACHMENT, gl.RENDERBUFFER, depth)
	if s := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); s != gl.FRAMEBUFFER_COMPLETE {
		t.Fatalf("test framebuffer incomplete: 0x%x", s)
	}
	gl.Viewport(0, 0, naniteTestSize, naniteTestSize)
	gl.Enable(gl.DEPTH_TEST)
	gl.DepthFunc(gl.LEQUAL)
	t.Cleanup(func() {
		gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
		gl.DeleteFramebuffers(1, &fbo)
		gl.DeleteRenderbuffers(1, &depth)
		gl.DeleteTextures(1, &color)
	})
}

func naniteProbe(t *testing.T) {
	t.Helper()
	newNormalProbe(t)
	naniteTestTarget(t)
	gfx.InstancedUniform = world.ProgUniformLoc(gfx.Program, "instanced\x00")
	if !naniteCull.ready() {
		t.Fatal("the GPU cull's programs did not build")
	}
	gfx.TextureNames = gfx.ScanTextures(filepath.Join(testenv.RepoRoot(t), "resources", "textures"))
	for _, tex := range []string{"concrete", "brick"} {
		if err := gfx.EnsureTextureLoaded(tex); err != nil {
			t.Fatalf("load %s: %v", tex, err)
		}
	}
	t.Cleanup(func() { naniteTwoPass = false })
	// The tests below exercise the GPU path, so they pin the switches to
	// their defaults: a run with FOUNDRY_NANITE_*=0 in the environment (the
	// "flags off" check of the rest of the suite) must not turn them into
	// tests of the classic path. Each test still flips one where it compares.
	pinNaniteFlags(t)
}

// pinNaniteFlags sets every Nanite switch to its default for t, restoring the
// environment's values afterwards.
func pinNaniteFlags(t *testing.T) {
	t.Helper()
	type flag struct {
		p   *bool
		def bool
	}
	flags := []flag{
		{&NaniteClusters, true}, {&NaniteOcclusion, true}, {&NaniteOcclusionBrushes, false},
		{&NaniteCone, true}, {&NaniteShadowCone, true}, {&NaniteShadows, true},
		{&NaniteCompact, true}, {&NaniteStats, false}, {&NanitePrepass, true},
		{&NanitePrepassMeshes, false}, {&NaniteChunks, true}, {&world.NaniteMeshes, true},
		// LOD selection off: the Phase 1 tests compare the cull against the
		// full-detail classic path exactly. The LOD tests
		// (nanite_lod_gl_test.go) switch it on.
		{&NaniteLOD, false}, {&naniteLODWalk, true},
		// Phase 3's page pool, on with everything preloaded: what the
		// earlier tests compare is then the same picture through the pool
		// (nanite_stream_gl_test.go compares the two).
		{&NaniteStream, true}, {&NaniteStreamPreload, true}, {&NanitePersistent, true},
		// A cooked mesh on the classic path casts its full detail here, not
		// its fallback cut (world.NaniteClassicShadowLOD): the shadow tests
		// compare the classic path's map against the cull's sample for
		// sample. scenery_shadow_count_gl_test.go switches it on.
		{&world.NaniteClassicShadowLOD, false},
	}
	saved := make([]bool, len(flags))
	for i, f := range flags {
		saved[i] = *f.p
		*f.p = f.def
	}
	t.Cleanup(func() {
		for i, f := range flags {
			*f.p = saved[i]
		}
	})
}

func readColour() []uint8 {
	px := make([]uint8, naniteTestSize*naniteTestSize*4)
	glext.ReadPixels(0, 0, naniteTestSize, naniteTestSize, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
	return px
}

func readDepth() []float32 {
	out := make([]float32, naniteTestSize*naniteTestSize)
	glext.ReadPixels(0, 0, naniteTestSize, naniteTestSize, gl.DEPTH_COMPONENT, gl.FLOAT, gl.Ptr(&out[0]))
	return out
}

// staticQuad is a CPU-only compiled surface: the quad (x0..x1, y0..y1) at
// depth z, facing +Z, in chunk, with light baked unevenly over its corners.
func staticQuad(t *testing.T, chunk, tex string, x0, x1, y0, y1, z, alpha float32) world.StaticSurface {
	t.Helper()
	verts := [][]float32{{x0, y0, z, 0, 0}, {x1, y0, z, 1, 0}, {x1, y1, z, 1, 1}, {x0, y1, z, 0, 1}}
	var g gfx.Geometry
	g.CPUOnly = true
	g.VertexLight = [][]float32{{1, 0.3, 0.3}, {0.3, 1, 0.3}, {0.3, 0.3, 1}, {1, 1, 0.8}}
	if err := g.Load(verts, []int{0, 1, 2, 0, 2, 3}, tex, mgl32.Vec3{}); err != nil {
		t.Fatal(err)
	}
	g.Alpha = alpha
	return world.StaticSurface{Geometry: g, Chunk: chunk, Normal: mgl32.Vec3{0, 0, 1},
		Lo: mgl32.Vec3{x0, y0, z}, Hi: mgl32.Vec3{x1, y1, z}}
}

// naniteStaticLevel is a wall of tiles in chunk "wall" at z = 0 covering the
// middle of the view, a field of tiles in chunk "field" behind it, a frame of
// tiles round the edges in front, and a translucent pane: three textures, so
// several groups, and enough surfaces for many clusters.
func naniteStaticLevel(t *testing.T, wallWidth float32) *world.Level {
	t.Helper()
	s := &world.StaticGeometry{}
	add := func(surf world.StaticSurface) { s.Surfaces = append(s.Surfaces, surf) }
	// The field: 24 x 24 small tiles at z = -6, behind the wall.
	for i := 0; i < 24; i++ {
		for j := 0; j < 24; j++ {
			x, y := -3+float32(i)*0.25, -3+float32(j)*0.25
			tex := []string{"concrete", "brick"}[(i+j)%2]
			add(staticQuad(t, "field", tex, x, x+0.24, y, y+0.24, -6-float32((i*7+j*3)%5)*0.1, 1))
		}
	}
	// The wall, tiles of 0.5 covering ±wallWidth.
	for x := -wallWidth; x < wallWidth-1e-4; x += 0.5 {
		for y := -wallWidth; y < wallWidth-1e-4; y += 0.5 {
			add(staticQuad(t, "wall", "concrete", x, x+0.5, y, y+0.5, 0, 1))
		}
	}
	// A frame in front, at the view's edges.
	for k := 0; k < 8; k++ {
		f := -1.4 + float32(k)*0.35
		add(staticQuad(t, "frame", "brick", f, f+0.3, -1.45, -1.25, 1, 1))
		add(staticQuad(t, "frame", "brick", f, f+0.3, 1.25, 1.45, 1, 1))
	}
	add(staticQuad(t, "frame", "concrete", -0.3, 0.3, -0.3, 0.3, 1.5, 0.5))
	lvl := &world.Level{Static: s}
	t.Cleanup(func() { ReleaseChunkBatches(lvl) })
	return lvl
}

// drawStaticFrame draws the level's compiled surfaces as the frame does:
// opaque (in two passes round the occlusion pass, when twoPass), then the
// queued translucent ones.
func drawStaticFrame(lvl *world.Level, twoPass bool) []uint8 {
	gl.ClearColor(0, 1, 0, 1)
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	gl.Disable(gl.CULL_FACE)
	gl.UseProgram(gfx.Program)
	gl.UniformMatrix4fv(gfx.ProjectionUniform, 1, false, &gfx.Projection[0])
	gfx.UpdateCamMatrix()
	frustum := ExtractFrustum(gfx.Projection.Mul4(gfx.Cam.Matrix))
	visible := lvl.Static.VisibleSurfaces(gfx.Cam.Position, nil)
	var queued []BatchDraw
	naniteTwoPass = twoPass
	DrawStaticSurfaces(lvl, visible, frustum, gfx.Cam.Position, func(d BatchDraw, _ float32) { queued = append(queued, d) })
	naniteOcclusionPass(lvl)
	gl.Enable(gl.BLEND)
	gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)
	gl.DepthMask(false)
	for i := range queued {
		queued[i].Draw()
	}
	gl.DepthMask(true)
	gl.Disable(gl.BLEND)
	return readColour()
}

// withNanite runs fn with the switches set, putting them back after.
// The brushes' two-pass switch follows occlusion, so the tests exercise both
// passes on compiled surfaces and chunks whatever the shipping default; the
// chunk size floor is lifted so the tests' small chunks take the GPU path.
func withNanite(clusters, occlusion, cone bool, fn func()) {
	pc, po, pn, pb, pm := NaniteClusters, NaniteOcclusion, NaniteCone, NaniteOcclusionBrushes, chunkGPUMinTriangles
	NaniteClusters, NaniteOcclusion, NaniteCone, NaniteOcclusionBrushes, chunkGPUMinTriangles = clusters, occlusion, cone, occlusion, 0
	defer func() {
		NaniteClusters, NaniteOcclusion, NaniteCone, NaniteOcclusionBrushes, chunkGPUMinTriangles = pc, po, pn, pb, pm
	}()
	fn()
}

func countDiff(t *testing.T, what string, want, got []uint8) {
	t.Helper()
	diff, drawn := 0, 0
	for i := 0; i < len(want); i += 4 {
		if want[i] != got[i] || want[i+1] != got[i+1] || want[i+2] != got[i+2] {
			diff++
		}
		if !(want[i] == 0 && want[i+1] == 255 && want[i+2] == 0) {
			drawn++
		}
	}
	if drawn < len(want)/4/10 {
		t.Fatalf("%s: only %d pixels drawn; the scene is not in view", what, drawn)
	}
	if diff > 0 {
		t.Errorf("%s: %d of %d drawn pixels differ from the CPU path", what, diff, drawn)
	}
}

func TestNaniteStaticMatchesCPUPathOverFrames(t *testing.T) {
	naniteProbe(t)
	poses := []mgl32.Vec3{{0, 0, 4}, {0, 0, 4}, {0.6, 0.3, 3.5}, {-0.8, -0.2, 4.5}, {-0.8, -0.2, 4.5}}
	ref := make([][]uint8, len(poses))
	withNanite(false, false, false, func() {
		lvl := naniteStaticLevel(t, 1)
		for i, p := range poses {
			gfx.Cam.Position = p
			ref[i] = drawStaticFrame(lvl, false)
		}
	})
	for _, mode := range []struct {
		name            string
		occlusion, cone bool
	}{{"single pass", false, false}, {"two pass", true, false}, {"two pass + cone", true, true}} {
		withNanite(true, mode.occlusion, mode.cone, func() {
			lvl := naniteStaticLevel(t, 1)
			sb := staticBatchesFor(lvl)
			if sb == nil || sb.gpu == nil {
				t.Fatal("the GPU path did not build")
			}
			for i, p := range poses {
				gfx.Cam.Position = p
				got := drawStaticFrame(lvl, mode.occlusion)
				countDiff(t, mode.name+" frame "+string(rune('0'+i)), ref[i], got)
			}
		})
	}
}

// historyOf reads the camera batch's visibility history.
func historyOf(b *cullBatch) []uint32 {
	out := make([]uint32, b.histLen)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, b.histBuf)
	glext.GetBufferSubData(gl.SHADER_STORAGE_BUFFER, 0, len(out)*4, gl.Ptr(&out[0]))
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	return out
}

// commandsOf reads the last pass's indirect commands.
func commandsOf(b *cullBatch) []uint32 {
	out := make([]uint32, b.slots*5)
	if len(out) == 0 {
		return out
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, b.cmdBuf)
	glext.GetBufferSubData(gl.SHADER_STORAGE_BUFFER, 0, len(out)*4, gl.Ptr(&out[0]))
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	return out
}

func TestNaniteOcclusionCullsWhatTheWallHides(t *testing.T) {
	naniteProbe(t)
	gfx.Cam.Position = mgl32.Vec3{0, 0, 4}
	withNanite(true, true, false, func() {
		// A wall wide enough to hide the whole field from the camera.
		lvl := naniteStaticLevel(t, 3)
		sb := staticBatchesFor(lvl)
		g := sb.gpu
		inGroups := func(chunk string) (lo, hi int32) {
			lo, hi = math.MaxInt32, -1
			for _, grp := range sb.groups {
				if grp.key.chunk == chunk && grp.clusterCount > 0 {
					lo, hi = min(lo, grp.clusterFirst), max(hi, grp.clusterFirst+grp.clusterCount)
				}
			}
			return lo, hi
		}
		count := func(h []uint32, lo, hi int32) int {
			n := 0
			for c := lo; c < hi; c++ {
				n += int(h[c])
			}
			return n
		}
		fieldLo, fieldHi := inGroups("field")
		wallLo, wallHi := inGroups("wall")
		// Frame 0 has nothing drawn ahead of the occlusion pass and no
		// history, so the pyramid is empty and everything in the frustum is
		// drawn — and recorded visible. From frame 1 the wall, drawn by pass
		// 1, hides the field from pass 2.
		drawStaticFrame(lvl, true)
		if n := count(historyOf(&g.main), fieldLo, fieldHi); n != int(fieldHi-fieldLo) {
			t.Fatalf("frame 0: %d of %d field clusters recorded visible; with nothing drawn yet, all are", n, fieldHi-fieldLo)
		}
		for frame := 1; frame < 4; frame++ {
			drawStaticFrame(lvl, true)
			h := historyOf(&g.main)
			if n := count(h, fieldLo, fieldHi); n != 0 {
				t.Fatalf("frame %d: %d of %d field clusters behind the wall recorded visible", frame, n, fieldHi-fieldLo)
			}
			if n := count(h, wallLo, wallHi); n == 0 {
				t.Fatalf("frame %d: no wall cluster recorded visible", frame)
			}
			// Pass 2's commands draw none of the field either.
			cmds := commandsOf(&g.main)
			for _, tl := range g.main.tiles {
				for k := uint32(0); k < tl.A[1]; k++ {
					c := tl.A[0] + k
					if int32(c) >= fieldLo && int32(c) < fieldHi && cmds[(tl.A[3]+k)*5+1] != 0 {
						t.Fatalf("frame %d: field cluster %d drawn", frame, c)
					}
				}
			}
		}
		// The wall's chunk switched off: the field comes into view, in the
		// very frame it does.
		want := func() []uint8 {
			var px []uint8
			withNanite(false, false, false, func() {
				ref := naniteStaticLevel(t, 3)
				ref.Static.SetChunkHidden("wall", true)
				px = drawStaticFrame(ref, false)
			})
			return px
		}()
		lvl.Static.SetChunkHidden("wall", true)
		got := drawStaticFrame(lvl, true)
		countDiff(t, "the frame the wall went", want, got)
		h := historyOf(&g.main)
		if n := count(h, fieldLo, fieldHi); n < int(fieldHi-fieldLo)/2 {
			t.Fatalf("with the wall gone only %d of %d field clusters are visible", n, fieldHi-fieldLo)
		}
	})
}

func TestNaniteConeCullsAClusterSeenFromBehind(t *testing.T) {
	naniteProbe(t)
	build := func() *world.Level {
		s := &world.StaticGeometry{Surfaces: []world.StaticSurface{staticQuad(t, "c", "concrete", -1, 1, -1, 1, 0, 1)}}
		lvl := &world.Level{Static: s}
		t.Cleanup(func() { ReleaseChunkBatches(lvl) })
		return lvl
	}
	drawn := func(cone bool, eye mgl32.Vec3) bool {
		var any bool
		withNanite(true, false, cone, func() {
			lvl := build()
			gfx.Cam.Position = eye
			gfx.Cam.Direction = mgl32.Vec3{0, 0, -eye[2]}.Normalize()
			drawStaticFrame(lvl, false)
			cmds := commandsOf(&staticBatchesFor(lvl).gpu.main)
			for i := 0; i+1 < len(cmds); i += 5 {
				any = any || cmds[i+1] != 0
			}
		})
		return any
	}
	defer func() { gfx.Cam.Direction = mgl32.Vec3{0, 0, -1} }()
	if !drawn(true, mgl32.Vec3{0, 0, 4}) {
		t.Fatal("a quad facing the camera was culled")
	}
	if drawn(true, mgl32.Vec3{0, 0, -4}) {
		t.Fatal("a quad seen from behind survived the cone test")
	}
	if !drawn(false, mgl32.Vec3{0, 0, -4}) {
		t.Fatal("with the cone off, a quad seen from behind was culled")
	}
}

func TestNaniteStaticShadowMatchesCPUPath(t *testing.T) {
	naniteProbe(t)
	// A cascade's way: the cull is the light matrix's own frustum, so
	// whatever either path keeps beyond what the other does lies outside the
	// map. The second, tight one cuts through the level.
	view := mgl32.LookAtV(mgl32.Vec3{2, 3, 6}, mgl32.Vec3{0, 0, -3}, mgl32.Vec3{0, 1, 0})
	wide := mgl32.Ortho(-5, 5, -5, 5, 0.1, 30).Mul4(view)
	narrow := mgl32.Ortho(-1.5, 1.5, -1.5, 1.5, 0.1, 30).Mul4(view)
	var lsm mgl32.Mat4
	depth := func(gpu bool, cull *Frustum) []float32 {
		var out []float32
		prev := NaniteShadows
		NaniteShadows = gpu
		defer func() { NaniteShadows = prev }()
		withNanite(true, false, false, func() {
			lvl := naniteStaticLevel(t, 1)
			identity := mgl32.Ident4()
			gl.UseProgram(Shadows.Program)
			gl.UniformMatrix4fv(Shadows.LSMUniform, 1, false, &lsm[0])
			gl.UniformMatrix4fv(Shadows.ModelUniform, 1, false, &identity[0])
			gl.Disable(gl.CULL_FACE)
			gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
			shadowStaticSurfaces(lvl, Shadows.Program, cull)
			out = readDepth()
		})
		return out
	}
	wideCull, narrowCull := ExtractFrustum(wide), ExtractFrustum(narrow)
	for _, c := range []struct {
		name string
		m    mgl32.Mat4
		cull *Frustum
	}{{"no cull", wide, nil}, {"wide cascade", wide, &wideCull}, {"tight cascade", narrow, &narrowCull}} {
		lsm = c.m
		a, b := depth(false, c.cull), depth(true, c.cull)
		diff, drawn := 0, 0
		for i := range a {
			if a[i] != b[i] {
				diff++
			}
			if a[i] < 1 {
				drawn++
			}
		}
		if drawn == 0 {
			t.Fatalf("%s: no depth written", c.name)
		}
		if diff > 0 {
			t.Errorf("%s: %d of %d depth samples differ between CPU and per-cluster shadow culling", c.name, diff, drawn)
		}
	}
}

// ---------- cooked scenery ----------

// cookedScenery cooks src into dir and returns placements of it (and of the
// source, for the classic reference when classicFrom is set).
func naniteSceneryLevel(t *testing.T, path string, gpu bool, poses []mgl32.Vec3, scale float32) *world.Level {
	t.Helper()
	lvl := &world.Level{}
	for i, p := range poses {
		s := world.NewScenery("n", path, p)
		s.NoNanite = !gpu
		// Every slot untextured, so the picture is each surface's own
		// normal colour, which a mis-welded or mis-transformed normal
		// changes.
		if nanite.IsCookedPath(path) {
			m, err := nanite.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			s.Materials = map[string]string{}
			for _, sl := range m.Slots {
				s.Materials[sl.Name] = ""
			}
		}
		if err := s.Load(); err != nil {
			t.Fatal(err)
		}
		if s.NaniteGPU != (gpu && s.Nanite != nil) {
			t.Fatalf("placement NaniteGPU=%v, want %v", s.NaniteGPU, gpu)
		}
		for si := range s.Submeshes {
			s.Submeshes[si].Texture = ""
		}
		s.Rotation = mgl32.Vec3{0.4 + 0.3*float32(i), 0.7 * float32(i), 0.1}
		s.Scale = mgl32.Vec3{scale, scale, scale}
		lvl.Scenery = append(lvl.Scenery, s)
	}
	t.Cleanup(func() { world.DeleteScenery(lvl) })
	return lvl
}

// drawSceneryBoth draws the level's scenery through both paths, the frame's
// way: classic (RenderScenery) then the GPU cull, round an occlusion pass.
func drawSceneryBoth(lvl *world.Level, twoPass bool) []uint8 {
	gl.ClearColor(0, 1, 0, 1)
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	gl.Disable(gl.CULL_FACE)
	gl.UseProgram(gfx.Program)
	gl.UniformMatrix4fv(gfx.ProjectionUniform, 1, false, &gfx.Projection[0])
	gfx.UpdateCamMatrix()
	projView := gfx.Projection.Mul4(gfx.Cam.Matrix)
	frustum := ExtractFrustum(projView)
	naniteTwoPass = twoPass
	scenery = sceneryInstancer{}
	RenderScenery(lvl, &frustum, func(g *gfx.Geometry, m mgl32.Mat4, _ bool) { g.DrawWithTransform(m) })
	RenderNaniteScenery(lvl, frustum, projView)
	naniteOcclusionPass(lvl)
	return readColour()
}

func TestNaniteCookedModelMatchesClassicScenery(t *testing.T) {
	naniteProbe(t)
	src := filepath.Join(testenv.RepoRoot(t), "resources", "models", "dropship.fmdl")
	cooked := filepath.Join(t.TempDir(), "dropship.fnm")
	if _, err := nanitecook.CookFile(src, cooked, true, nanitecook.Options{}); err != nil {
		t.Fatal(err)
	}
	poses := []mgl32.Vec3{{-0.9, 0.5, 0}, {0.3, -0.4, -0.6}, {0.9, 0.6, 0.3}}
	doc, _ := model.LoadModel(src)
	lo, hi := doc.Bounds()
	ext := hi.Sub(lo)
	k := 1.4 / max(ext[0], ext[1], ext[2])
	// The classic path drawing the same cooked file is the exact reference:
	// the cook quantises (Phase 3), so the source model's own picture is
	// only close — every normal within a twentieth of a degree, which in the
	// normal colours is a step or two of one channel on a few per cent of
	// the pixels.
	source := drawSceneryBoth(naniteSceneryLevel(t, src, false, poses, k), false)
	classic := naniteSceneryLevel(t, cooked, false, poses, k)
	ref := drawSceneryBoth(classic, false)
	far, worst := 0, 0
	for i := 0; i < len(ref); i += 4 {
		d := 0
		for c := 0; c < 3; c++ {
			if x := int(ref[i+c]) - int(source[i+c]); x > d || -x > d {
				d = max(x, -x)
			}
		}
		worst = max(worst, d)
		if d > 3 {
			far++
		}
	}
	t.Logf("cooked against its source: worst channel step %d, %d pixels off by more than 3", worst, far)
	if far > 10 {
		t.Errorf("the cooked dropship strays from its source: %d pixels off by more than 3/255", far)
	}
	for _, twoPass := range []bool{false, true} {
		gpu := naniteSceneryLevel(t, cooked, true, poses, k)
		for frame := 0; frame < 2; frame++ {
			got := drawSceneryBoth(gpu, twoPass)
			if DebugStats.NaniteInstances != len(poses) {
				t.Fatalf("%d placements went to the GPU cull, want %d", DebugStats.NaniteInstances, len(poses))
			}
			countDiffTol(t, "cooked dropship", ref, got, 2)
		}
	}
}

// countDiffTol is countDiff allowing a handful of pixels: a cooked mesh draws
// its triangles in cluster order, so where two of its own faces lie exactly in
// one plane the depth test may pick the other.
func countDiffTol(t *testing.T, what string, want, got []uint8, tol int) {
	t.Helper()
	diff, drawn := 0, 0
	for i := 0; i < len(want); i += 4 {
		if want[i] != got[i] || want[i+1] != got[i+1] || want[i+2] != got[i+2] {
			diff++
		}
		if !(want[i] == 0 && want[i+1] == 255 && want[i+2] == 0) {
			drawn++
		}
	}
	if drawn < 200 {
		t.Fatalf("%s: only %d pixels drawn", what, drawn)
	}
	if diff > tol {
		t.Errorf("%s: %d of %d drawn pixels differ (allowed %d)", what, diff, drawn, tol)
	}
}

func TestNaniteDenseRockMatchesClassicAndCastsTheSameShadow(t *testing.T) {
	naniteProbe(t)
	dir := t.TempDir()
	glb, err := model.EncodeGLB(nanitecook.SampleRock(5, 0.4)) // 20,480 triangles
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "rock.glb")
	if err := os.WriteFile(src, glb, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := nanitecook.CookFile(src, "", true, nanitecook.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var poses []mgl32.Vec3
	for i := 0; i < 12; i++ {
		a := float64(i) * 0.52
		poses = append(poses, mgl32.Vec3{float32(math.Cos(a)) * 1.3, float32(math.Sin(a)) * 1.1, -float32(i) * 0.4})
	}
	classic := naniteSceneryLevel(t, res.Out, false, poses, 0.45)
	gpu := naniteSceneryLevel(t, res.Out, true, poses, 0.45)
	ref := drawSceneryBoth(classic, false)
	for frame := 0; frame < 3; frame++ {
		countDiffTol(t, "rock field", ref, drawSceneryBoth(gpu, true), 0)
	}

	// The shadow: classic instanced scenery vs the GPU cull, into depth.
	lsm := gfx.Projection.Mul4(gfx.Cam.Matrix)
	tight := ExtractFrustum(mgl32.Perspective(mgl32.DegToRad(20), 1, 0.1, 100).Mul4(gfx.Cam.Matrix))
	depth := func(lvl *world.Level, cull *Frustum) []float32 {
		identity := mgl32.Ident4()
		scenery = sceneryInstancer{}
		gl.UseProgram(Shadows.Program)
		gl.UniformMatrix4fv(Shadows.LSMUniform, 1, false, &lsm[0])
		gl.UniformMatrix4fv(Shadows.ModelUniform, 1, false, &identity[0])
		gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
		RenderSceneryShadow(lvl, Shadows.Program, &gfx.ShadowSkinUniforms, Shadows.ModelUniform, cull)
		renderNaniteSceneryShadow(lvl, Shadows.Program, cull)
		return readDepth()
	}
	for _, cull := range []*Frustum{nil, &tight} {
		a, b := depth(classic, cull), depth(gpu, cull)
		diff, drawn := 0, 0
		for i := range a {
			if a[i] != b[i] {
				diff++
			}
			if a[i] < 1 {
				drawn++
			}
		}
		if drawn == 0 {
			t.Fatal("no shadow depth written")
		}
		// The tight frustum culls some placements whole on the classic path
		// and some clusters on the GPU path: what either keeps it draws
		// exactly, so the depth agrees inside the tight frustum. Compare the
		// whole map only without a cull.
		if cull == nil && diff > 0 {
			t.Errorf("%d of %d shadow depth samples differ", diff, drawn)
		}
	}
	// Placement count and cluster count on the record.
	if n := len(naniteScenery.batch.tiles); n == 0 {
		t.Fatal("no tiles")
	}
	_ = unsafe.Sizeof(gpuCluster{})
}

// ---------- chunk brushes ----------

// naniteChunkLevel is a chunk of boxes — a wall of them in front of a row of
// others, one translucent, one double-sided, one moved by a "script" — under
// a chunk matrix that is turned and lifted, as a ship's hangar is.
func naniteChunkLevel(t *testing.T) *world.Level {
	t.Helper()
	ch := world.Chunk{ID: "ship", Scale: 1}
	add := func(center, half mgl32.Vec3, tex string, edit func(b *world.Brush)) {
		b := world.NewBoxBrush("b", center, half, tex)
		if edit != nil {
			edit(&b)
		}
		ch.Brushes = append(ch.Brushes, b)
	}
	for i := 0; i < 6; i++ {
		add(mgl32.Vec3{-2.5 + float32(i), 0.2 * float32(i%2), -3}, mgl32.Vec3{0.4, 0.6, 0.4}, []string{"concrete", "brick"}[i%2], nil)
	}
	add(mgl32.Vec3{0, 0, 0}, mgl32.Vec3{1.2, 1.2, 0.1}, "brick", nil) // the wall
	add(mgl32.Vec3{-1.8, -0.8, 1}, mgl32.Vec3{0.3, 0.3, 0.3}, "concrete", func(b *world.Brush) {
		for fi := range b.Faces {
			b.Faces[fi].Alpha = 0.5
		}
	})
	add(mgl32.Vec3{1.8, -0.8, 1}, mgl32.Vec3{0.3, 0.3, 0.3}, "", func(b *world.Brush) {
		for fi := range b.Faces {
			b.Faces[fi].DoubleSided = true
		}
	})
	add(mgl32.Vec3{1.6, 1, 0.5}, mgl32.Vec3{0.3, 0.3, 0.3}, "concrete", func(b *world.Brush) {
		b.Dynamic = true
		b.Offset = mgl32.Vec3{-0.4, 0.2, 0}
	})
	for bi := range ch.Brushes {
		if err := ch.Brushes[bi].Load(); err != nil {
			t.Fatal(err)
		}
	}
	ch.RecomputeBounds()
	lvl := &world.Level{Chunks: []world.Chunk{ch}}
	t.Cleanup(func() { ReleaseChunkBatches(lvl) })
	return lvl
}

// drawChunkFrame draws the level's chunks as the frame does, opaque (round
// the occlusion pass when twoPass) then the queued translucent brushes.
func drawChunkFrame(lvl *world.Level, chunkModel mgl32.Mat4, twoPass, gpu bool) []uint8 {
	gl.ClearColor(0, 1, 0, 1)
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	gl.Disable(gl.CULL_FACE)
	gl.UseProgram(gfx.Program)
	gfx.UpdateCamMatrix()
	frustum := ExtractFrustum(gfx.Projection.Mul4(gfx.Cam.Matrix))
	var queued []BatchDraw
	naniteChunksActive = gpu
	defer func() { naniteChunksActive = false }()
	naniteTwoPass = twoPass
	naniteChunkPending = naniteChunkPending[:0]
	for ci := range lvl.Chunks {
		DrawChunkBrushes(lvl, &lvl.Chunks[ci], frustum, chunkModel, gfx.Cam.Position,
			func(d BatchDraw, _ float32) { queued = append(queued, d) })
	}
	naniteOcclusionPass(lvl)
	gl.Enable(gl.BLEND)
	gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)
	gl.DepthMask(false)
	for i := range queued {
		queued[i].Draw()
	}
	gl.DepthMask(true)
	gl.Disable(gl.BLEND)
	return readColour()
}

func TestNaniteChunkBrushesMatchCPUPath(t *testing.T) {
	naniteProbe(t)
	gfx.Cam.Position = mgl32.Vec3{0.3, 0.6, 6}
	gfx.Cam.Direction = mgl32.Vec3{0, -0.1, -1}.Normalize()
	defer func() { gfx.Cam.Direction = mgl32.Vec3{0, 0, -1} }()
	models := []mgl32.Mat4{
		mgl32.Ident4(),
		mgl32.Translate3D(0.2, -0.3, 0).Mul4(mgl32.HomogRotate3DY(0.3)),
		mgl32.Translate3D(0.4, -0.2, 0.3).Mul4(mgl32.HomogRotate3DY(0.5)),
	}
	ref := naniteChunkLevel(t)
	var want [][]uint8
	for _, m := range models {
		want = append(want, drawChunkFrame(ref, m, false, false))
	}
	for _, twoPass := range []bool{false, true} {
		withNanite(true, twoPass, true, func() {
			lvl := naniteChunkLevel(t)
			for i, m := range models {
				got := drawChunkFrame(lvl, m, twoPass, true)
				countDiff(t, "chunk frame "+string(rune('0'+i)), want[i], got)
			}
			cb := chunkBatchFor(lvl, &lvl.Chunks[0])
			if cb.gpu == nil || cb.gpu.n == 0 {
				t.Fatal("the chunk did not go through the GPU cull")
			}
			// A brush starting to move leaves the GPU path for its own draw,
			// and the picture still matches.
			lvl.Chunks[0].Brushes[0].Offset = mgl32.Vec3{0, 0.3, 0}
			ref.Chunks[0].Brushes[0].Offset = mgl32.Vec3{0, 0.3, 0}
			countDiff(t, "a brush set moving", drawChunkFrame(ref, models[0], false, false), drawChunkFrame(lvl, models[0], twoPass, true))
			ref.Chunks[0].Brushes[0].Offset = mgl32.Vec3{}
		})
	}
}

func TestNaniteChunkShadowMatchesCPUPath(t *testing.T) {
	naniteProbe(t)
	prevMin := chunkGPUMinTriangles
	chunkGPUMinTriangles = 0
	defer func() { chunkGPUMinTriangles = prevMin }()
	view := mgl32.LookAtV(mgl32.Vec3{2, 4, 6}, mgl32.Vec3{0, 0, -1}, mgl32.Vec3{0, 1, 0})
	lsm := mgl32.Ortho(-3, 3, -3, 3, 0.1, 30).Mul4(view)
	cull := ExtractFrustum(lsm)
	chunkModel := mgl32.Translate3D(0.2, -0.3, 0).Mul4(mgl32.HomogRotate3DY(0.3))
	depth := func(gpu bool) []float32 {
		lvl := naniteChunkLevel(t)
		naniteChunksActive = gpu
		defer func() { naniteChunksActive = false }()
		identity := mgl32.Ident4()
		current := identity
		gl.UseProgram(Shadows.Program)
		gl.UniformMatrix4fv(Shadows.LSMUniform, 1, false, &lsm[0])
		gl.UniformMatrix4fv(Shadows.ModelUniform, 1, false, &identity[0])
		gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
		shadowChunkBrushes(lvl, &lvl.Chunks[0], chunkModel, &cull, Shadows.Program, Shadows.ModelUniform, &current)
		if gpu && chunkBatchFor(lvl, &lvl.Chunks[0]).gpu == nil {
			t.Fatal("the chunk's shadow did not go through the GPU cull")
		}
		return readDepth()
	}
	a, b := depth(false), depth(true)
	diff, drawn := 0, 0
	for i := range a {
		if a[i] != b[i] {
			diff++
		}
		if a[i] < 1 {
			drawn++
		}
	}
	if drawn == 0 {
		t.Fatal("no depth written")
	}
	if diff > 0 {
		t.Errorf("%d of %d depth samples differ between the CPU and per-cluster chunk shadow", diff, drawn)
	}
}

// The two ways a pass's commands reach the draw — packed and drawn by count
// where the driver has glMultiDrawElementsIndirectCount, the fixed list
// otherwise — are the same picture. Whichever the test context has is what
// the other tests ran; this runs both.
func TestNanitePackedAndFixedCommandListsMatch(t *testing.T) {
	naniteProbe(t)
	if !naniteCull.canCompact {
		t.Skip("this context has no indirect-count draw; only the fixed list can run")
	}
	gfx.Cam.Position = mgl32.Vec3{0.4, 0.2, 4}
	draw := func(compact bool) []uint8 {
		prev := NaniteCompact
		NaniteCompact = compact
		defer func() { NaniteCompact = prev }()
		var px []uint8
		withNanite(true, true, true, func() {
			lvl := naniteStaticLevel(t, 1)
			for f := 0; f < 3; f++ {
				px = drawStaticFrame(lvl, true)
			}
			if b := &staticBatchesFor(lvl).gpu.main; b.compacted != compact {
				t.Fatalf("compacted=%v with packing %v", b.compacted, compact)
			}
		})
		return px
	}
	countDiff(t, "packed vs fixed command list", draw(false), draw(true))
}

// The depth prepass writes the depth the colour pass will compute, so it must
// draw with the world program's own matrices — not gfx.Projection, which a
// frame can rebuild while another program is bound (a headless session's
// first frame did, and every surface failed its depth test).
func TestNanitePrepassUsesTheWorldProgramsMatrices(t *testing.T) {
	naniteProbe(t)
	gfx.Cam.Position = mgl32.Vec3{0, 0, 4}
	draw := func(gpu bool) []uint8 {
		var px []uint8
		withNanite(gpu, false, false, func() {
			lvl := naniteStaticLevel(t, 1)
			gl.ClearColor(0, 1, 0, 1)
			gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
			gl.UseProgram(gfx.Program)
			gfx.UpdateCamMatrix()
			// The program draws with a wider lens than gfx.Projection says.
			wide := mgl32.Perspective(mgl32.DegToRad(70), 1, 0.1, 100)
			gl.UniformMatrix4fv(gfx.ProjectionUniform, 1, false, &wide[0])
			frustum := ExtractFrustum(wide.Mul4(gfx.Cam.Matrix))
			visible := lvl.Static.VisibleSurfaces(gfx.Cam.Position, nil)
			DrawStaticSurfaces(lvl, visible, frustum, gfx.Cam.Position, func(BatchDraw, float32) {})
			px = readColour()
			gl.UniformMatrix4fv(gfx.ProjectionUniform, 1, false, &gfx.Projection[0])
		})
		return px
	}
	countDiff(t, "prepass under the program's own projection", draw(false), draw(true))
}
