package render

// nanite_vis_gl_test.go — Phase 4 (docs/nanite/PHASE_4.md): the visibility
// buffer and TAA, through DrawWorld itself on a level that has every kind of
// geometry the cull draws (compiled surfaces with baked light, live chunk
// brushes — one double-sided, one moved, one translucent — and cooked rocks)
// beside what it does not (a classic prop in front of them, a translucent
// pane), lit by an ambient, a shadowing sun and a shadowing point light.
//
//   - The visibility buffer's picture is the forward path's, both drawn into
//     the scene target with the TAA resolve off.
//   - The material pass shades each pixel once, however many surfaces the ID
//     pass drew behind it.
//   - TAA converges on the supersampled picture (closer to it than the
//     aliased frame), its motion vectors are the camera's and a moving
//     chunk's, a cut forgets the history, and a cutout's dithered coverage
//     resolves into a soft edge.
//
//	FOUNDRY_GL_TEST=1 go test -timeout 300s -run 'VisBuffer|TAA' ./internal/render/

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/glext"
	"foundry/internal/gltest"
	"foundry/internal/model"
	"foundry/internal/nanitecook"
	"foundry/internal/platform"
	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// visWorld is the world stood up for DrawWorld into a test target.
type visWorld struct {
	t    *testing.T
	fbo  uint32
	size int32
}

// newVisWorld stands the world up (as a headless session does) at
// naniteTestSize, binds a target that size, and pins the switches: TAA mode,
// the visibility buffer on, live GI off.
func newVisWorld(t *testing.T) *visWorld {
	t.Helper()
	gltest.Context(t)
	withRepoResources(t)
	pw, ph := platform.WinWidth, platform.WinHeight
	platform.WinWidth, platform.WinHeight = naniteTestSize, naniteTestSize
	if err := StandUpWorld(naniteTestSize, naniteTestSize); err != nil {
		t.Fatalf("StandUpWorld: %v", err)
	}
	if !naniteCull.ready() {
		t.Fatal("the GPU cull's programs did not build")
	}
	gfx.TextureNames = gfx.ScanTextures(filepath.Join(testenv.RepoRoot(t), "resources", "textures"))
	for _, tex := range []string{"concrete", "brick"} {
		if err := gfx.EnsureTextureLoaded(tex); err != nil {
			t.Fatalf("load %s: %v", tex, err)
		}
	}
	pinNaniteFlags(t)
	pm, pr, pv, pg, pe, ps, pc := gfx.AntiAliasMode, TAAResolve, NaniteVisBuffer, gfx.GIQuality, AntiAliasEnabled, gfx.TAASharpen, chunkGPUMinTriangles
	gfx.AntiAliasMode, TAAResolve, NaniteVisBuffer, gfx.GIQuality, AntiAliasEnabled = gfx.AAModeTAA, false, true, gfx.GIOff, true
	// The test's chunk is small; it takes the GPU path all the same.
	chunkGPUMinTriangles = 0
	t.Cleanup(func() {
		gfx.AntiAliasMode, TAAResolve, NaniteVisBuffer, gfx.GIQuality, AntiAliasEnabled, gfx.TAASharpen, chunkGPUMinTriangles = pm, pr, pv, pg, pe, ps, pc
		platform.WinWidth, platform.WinHeight = pw, ph
		naniteTwoPass = false
		gfx.Cam.Direction = mgl32.Vec3{0, 0, -1}
	})
	w := &visWorld{t: t, size: naniteTestSize}
	w.fbo = w.target(naniteTestSize)
	gfx.Cam.Position = mgl32.Vec3{0.2, 0.3, 4.5}
	gfx.Cam.Direction = mgl32.Vec3{-0.03, -0.08, -1}.Normalize()
	gfx.UpdateCamMatrix()
	ForgetTAAHistory()
	taa.prevValid = false
	return w
}

// target makes and binds a colour + depth-stencil framebuffer size square.
func (w *visWorld) target(size int32) uint32 {
	var color, depth, fbo uint32
	gl.GenTextures(1, &color)
	gl.BindTexture(gl.TEXTURE_2D, color)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, size, size, 0, gl.RGBA, gl.UNSIGNED_BYTE, nil)
	setNearestClamp()
	gl.GenRenderbuffers(1, &depth)
	gl.BindRenderbuffer(gl.RENDERBUFFER, depth)
	gl.RenderbufferStorage(gl.RENDERBUFFER, gl.DEPTH24_STENCIL8, size, size)
	gl.GenFramebuffers(1, &fbo)
	gl.BindFramebuffer(gl.FRAMEBUFFER, fbo)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, color, 0)
	gl.FramebufferRenderbuffer(gl.FRAMEBUFFER, gl.DEPTH_STENCIL_ATTACHMENT, gl.RENDERBUFFER, depth)
	if s := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); s != gl.FRAMEBUFFER_COMPLETE {
		w.t.Fatalf("test framebuffer incomplete: 0x%x", s)
	}
	w.t.Cleanup(func() {
		gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
		gl.DeleteFramebuffers(1, &fbo)
		gl.DeleteRenderbuffers(1, &depth)
		gl.DeleteTextures(1, &color)
	})
	return fbo
}

// frame draws one frame of lvl through DrawWorld into fbo (size square) and
// reads it back.
func (w *visWorld) frameInto(lvl *world.Level, fbo uint32, size int32) []uint8 {
	DrawWorld(lvl, WorldDraw{Now: 1, Skip: func(p string) bool { return p == "gi" },
		Target: &FrameTarget{FBO: fbo, Viewport: [4]int32{0, 0, size, size}}})
	return w.readFrame(fbo, size)
}

// frameIntoWith is frame with the caller's WorldDraw, drawn into the world's
// own target.
func (w *visWorld) frameIntoWith(lvl *world.Level, o WorldDraw) []uint8 {
	o.Target = &FrameTarget{FBO: w.fbo, Viewport: [4]int32{0, 0, w.size, w.size}}
	DrawWorld(lvl, o)
	return w.readFrame(w.fbo, w.size)
}

func (w *visWorld) readFrame(fbo uint32, size int32) []uint8 {
	gl.BindFramebuffer(gl.FRAMEBUFFER, fbo)
	px := make([]uint8, size*size*4)
	glext.ReadPixels(0, 0, size, size, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
	return px
}

func (w *visWorld) frame(lvl *world.Level) []uint8 { return w.frameInto(lvl, w.fbo, w.size) }

// visTestRock cooks the bumpy sample rock and returns its path.
func visTestRock(t *testing.T) string {
	t.Helper()
	glb, err := model.EncodeGLB(nanitecook.SampleRock(4, 0.4))
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "rock.glb")
	if err := os.WriteFile(src, glb, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := nanitecook.CookFile(src, "", true, nanitecook.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return res.Out
}

// visTestLevel is the parity scene: naniteStaticLevel's compiled wall, field,
// frame and pane; naniteChunkLevel's chunk below them; a ring of cooked rocks
// in front, textured; and one rock drawn the classic way, nearest of all; lit
// by an ambient, a shadowing sun and a shadowing point light.
func visTestLevel(t *testing.T, rock string) *world.Level {
	t.Helper()
	lvl := naniteStaticLevel(t, 1)
	ch := naniteChunkLevel(t).Chunks[0]
	ch.Origin = mgl32.Vec3{0.3, -2.1, 0.6}
	lvl.Chunks = []world.Chunk{ch}
	add := func(p mgl32.Vec3, gpu bool, tex string, i int) {
		s := world.NewScenery("rock", rock, p)
		s.NoNanite = !gpu
		s.Materials = map[string]string{"stone": tex, "moss": tex, "": tex}
		if err := s.Load(); err != nil {
			t.Fatal(err)
		}
		if s.NaniteGPU != gpu {
			t.Fatalf("placement NaniteGPU=%v, want %v", s.NaniteGPU, gpu)
		}
		s.Rotation = mgl32.Vec3{0.4 + 0.3*float32(i), 0.7 * float32(i), 0.1}
		s.Scale = mgl32.Vec3{0.4, 0.4, 0.4}
		lvl.Scenery = append(lvl.Scenery, s)
	}
	for i := 0; i < 7; i++ {
		a := float64(i) * 0.9
		add(mgl32.Vec3{float32(math.Cos(a)) * 1.2, 0.2 + float32(math.Sin(a))*0.9, 1.6 - 0.1*float32(i)}, true,
			[]string{"concrete", "brick"}[i%2], i)
	}
	add(mgl32.Vec3{-0.5, -0.4, 2.6}, false, "brick", 9)
	lvl.Lights.Global = world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.35,
		Wrap: world.DefaultLightWrap, Hemisphere: world.DefaultAmbientHemisphere}
	sun := world.DirectionalLight{Color: mgl32.Vec3{1, 0.95, 0.9}, Brightness: 0.9, Yaw: 40, Pitch: -50,
		CastShadows: true, ShadowStrength: 0.8, Dynamic: true}
	sun.SyncDirection()
	lvl.Lights.Directional = []world.DirectionalLight{sun}
	lvl.Lights.Points = []world.PointLight{{Position: mgl32.Vec3{1.5, 1, 2.5}, Color: mgl32.Vec3{1, 0.6, 0.3},
		Brightness: 1.5, Radius: 6, CastShadows: true, ShadowStrength: 1, Dynamic: true}}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	world.UploadLights(lvl.Lights)
	t.Cleanup(func() {
		ReleaseChunkBatches(lvl)
		world.DeleteScenery(lvl)
		world.UploadLights(world.LightSet{})
	})
	return lvl
}

// pixelDiff counts the pixels whose largest channel difference passes 8 and
// 32 levels, and the largest difference.
func pixelDiff(a, b []uint8) (over8, over32, worst int) {
	for i := 0; i+3 < len(a); i += 4 {
		d := 0
		for c := 0; c < 3; c++ {
			v := int(a[i+c]) - int(b[i+c])
			if v < 0 {
				v = -v
			}
			d = max(d, v)
		}
		if d > 8 {
			over8++
		}
		if d > 32 {
			over32++
		}
		worst = max(worst, d)
	}
	return
}

// The visibility buffer draws the forward path's picture: at three poses,
// with the resolve off so both are one sample a pixel, every pixel within a
// few levels (the material pass's interpolation is not the rasteriser's to
// the last bit). And it is the visibility buffer that drew it: bins shaded.
func TestVisBufferShadesTheForwardPicture(t *testing.T) {
	w := newVisWorld(t)
	lvl := visTestLevel(t, visTestRock(t))
	poses := []struct{ pos, dir mgl32.Vec3 }{
		{mgl32.Vec3{0.2, 0.3, 4.5}, mgl32.Vec3{-0.03, -0.08, -1}},
		{mgl32.Vec3{-1.5, -0.6, 3.5}, mgl32.Vec3{0.35, -0.05, -1}},
		{mgl32.Vec3{1.2, 1.4, 3}, mgl32.Vec3{-0.3, -0.45, -1}},
	}
	for pi, p := range poses {
		gfx.Cam.Position, gfx.Cam.Direction = p.pos, p.dir.Normalize()
		gfx.UpdateCamMatrix()
		shots := map[bool][]uint8{}
		for _, vis := range []bool{false, true} {
			NaniteVisBuffer = vis
			w.frame(lvl) // the occlusion history's first frame
			shots[vis] = w.frame(lvl)
			if bins := DebugStats.NaniteVisBins; (bins > 0) != vis {
				t.Fatalf("pose %d, visibility buffer %v: %d material bins shaded", pi, vis, bins)
			}
		}
		drawn := 0
		for i := 0; i < len(shots[false]); i += 4 {
			if shots[false][i] != shots[true][i] || shots[false][i+1] != shots[true][i+1] {
				drawn++
			}
		}
		over8, over32, worst := pixelDiff(shots[false], shots[true])
		dumpPNG(fmt.Sprintf("parity%d_fwd", pi), shots[false], int(w.size))
		dumpPNG(fmt.Sprintf("parity%d_vis", pi), shots[true], int(w.size))
		total := int(w.size * w.size)
		t.Logf("pose %d: %d pixels differ at all, %d by more than 8 levels, %d by more than 32, worst %d",
			pi, drawn, over8, over32, worst)
		// The bar: a handful of pixels past 8 levels — a texel or a shadow
		// tap picked differently where the reconstruction lands a hair
		// across a boundary — and none past 32.
		if over8 > total/500 || over32 > 0 {
			t.Errorf("pose %d: %d pixels differ by more than 8 levels (allowed %d), %d by more than 32 (worst %d)",
				pi, over8, total/500, over32, worst)
		}
	}
}

// The material pass shades each covered pixel exactly once: one wall in view
// or eight stacked behind it, the shaded count is the screen's pixel count,
// while the ID pass rasterised eight times as much.
func TestVisBufferShadesEachPixelOnce(t *testing.T) {
	w := newVisWorld(t)
	gfx.Cam.Position, gfx.Cam.Direction = mgl32.Vec3{0, 0, 4}, mgl32.Vec3{0, 0, -1}
	gfx.UpdateCamMatrix()
	naniteVisCountShading = true
	defer func() { naniteVisCountShading = false }()
	walls := func(n int) *world.Level {
		s := &world.StaticGeometry{}
		for k := 0; k < n; k++ {
			s.Surfaces = append(s.Surfaces, staticQuad(t, "w", []string{"concrete", "brick"}[k%2], -6, 6, -6, 6, -float32(k), 1))
		}
		lvl := &world.Level{Static: s}
		lvl.Lights.Global = world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 1}
		world.UploadLights(lvl.Lights)
		t.Cleanup(func() { ReleaseChunkBatches(lvl) })
		return lvl
	}
	for _, n := range []int{1, 8} {
		lvl := walls(n)
		w.frame(lvl)
		var fragments uint32
		var q uint32
		gl.GenQueries(1, &q)
		gl.BeginQuery(gl.SAMPLES_PASSED, q)
		NaniteVisBuffer = false
		NanitePrepass = false
		w.frame(lvl)
		gl.EndQuery(gl.SAMPLES_PASSED)
		gl.Finish()
		gl.GetQueryObjectuiv(q, gl.QUERY_RESULT, &fragments)
		gl.DeleteQueries(1, &q)
		NaniteVisBuffer, NanitePrepass = true, true
		naniteVisShadedPixels = 0
		w.frame(lvl)
		if want := int(w.size * w.size); naniteVisShadedPixels != want {
			t.Errorf("%d walls: the material pass shaded %d pixels, want each of the %d once", n, naniteVisShadedPixels, want)
		}
		t.Logf("%d walls: material pass shaded %d pixels; the forward frame without a prepass passed %d samples in all",
			n, naniteVisShadedPixels, fragments)
	}
}

// TAA converges on the supersampled picture: sixteen frames at a still
// camera land nearer the frame drawn at four times the resolution and boxed
// down than the one-sample frame does, by a clear margin, and with far fewer
// pixels a long way off it.
func TestTAAConvergesTowardTheSupersampledPicture(t *testing.T) {
	w := newVisWorld(t)
	lvl := visTestLevel(t, visTestRock(t))
	big := int32(4 * naniteTestSize)
	bigFBO := w.target(big)
	platform.WinWidth, platform.WinHeight = big, big
	TAAResolve = false
	w.frameInto(lvl, bigFBO, big)
	hi := w.frameInto(lvl, bigFBO, big)
	platform.WinWidth, platform.WinHeight = naniteTestSize, naniteTestSize
	ref := boxDown(hi, big, 4)
	aliased := w.frame(lvl)
	TAAResolve = true
	gfx.TAASharpen = 0
	ForgetTAAHistory()
	var resolved []uint8
	for i := 0; i < TAAShotFrames(); i++ {
		resolved = w.frame(lvl)
	}
	dumpPNG("conv_aliased", aliased, int(w.size))
	dumpPNG("conv_taa", resolved, int(w.size))
	dumpPNG("conv_ref", ref, int(w.size))
	ea, ra := meanAbs(aliased, ref)
	et, rt := meanAbs(resolved, ref)
	t.Logf("mean error against 4x4 supersampling: one sample %.2f (%d pixels > 32 levels), TAA %.2f (%d)", ea, ra, et, rt)
	// Most of either error is texture filtering — the reference's mips are
	// four times finer — which no anti-aliasing changes; the edges are
	// where they part, and there TAA must leave a fraction of the far-off
	// pixels.
	if et > 0.9*ea {
		t.Errorf("TAA's mean error %.2f is not under the one-sample frame's %.2f", et, ea)
	}
	if rt*4 > ra {
		t.Errorf("TAA left %d pixels more than 32 levels off the supersampled picture, the one-sample frame %d", rt, ra)
	}

	// The sharpening: it changes the converged picture, and only within
	// each pixel's neighbourhood, so it brings back detail without ringing
	// past the one-sample frame's error.
	gfx.TAASharpen = 0.5
	sharp := w.frame(lvl)
	gfx.TAASharpen = 0
	changed := 0
	for i := range sharp {
		if sharp[i] != resolved[i] {
			changed++
		}
	}
	es, _ := meanAbs(sharp, ref)
	t.Logf("sharpened: %d channel values changed, error %.2f", changed, es)
	if changed == 0 || es > ea {
		t.Errorf("sharpening changed %d values and left an error of %.2f (one sample: %.2f)", changed, es, ea)
	}
}

// boxDown averages k x k blocks of a size-square RGBA picture.
func boxDown(px []uint8, size int32, k int) []uint8 {
	n := int(size) / k
	out := make([]uint8, n*n*4)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			for c := 0; c < 4; c++ {
				s := 0
				for dy := 0; dy < k; dy++ {
					for dx := 0; dx < k; dx++ {
						s += int(px[((y*k+dy)*int(size)+x*k+dx)*4+c])
					}
				}
				out[(y*n+x)*4+c] = uint8((s + k*k/2) / (k * k))
			}
		}
	}
	return out
}

// meanAbs is the mean absolute channel difference, and how many pixels are
// more than 32 levels off.
func meanAbs(a, b []uint8) (float64, int) {
	s, far := 0, 0
	for i := 0; i+3 < len(a); i += 4 {
		d := 0
		for c := 0; c < 3; c++ {
			v := int(a[i+c]) - int(b[i+c])
			if v < 0 {
				v = -v
			}
			s += v
			d = max(d, v)
		}
		if d > 32 {
			far++
		}
	}
	return float64(s) / float64(3*len(a)/4), far
}

// readMotion reads the frame's final motion vectors (taa.motion) at pixel
// (x, y), in texture units.
func readMotion(x, y int32) mgl32.Vec2 {
	// RGBA and FLOAT: the one format and type every float colour buffer
	// can be read back as.
	var out [4]float32
	gl.BindFramebuffer(gl.READ_FRAMEBUFFER, taa.motionFBO)
	glext.ReadPixels(x, y, 1, 1, gl.RGBA, gl.FLOAT, gl.Ptr(&out[0]))
	gl.BindFramebuffer(gl.READ_FRAMEBUFFER, 0)
	return mgl32.Vec2{out[0], out[1]}
}

// The motion vectors: a camera moving sideways past a wall gives the wall the
// camera's motion, read back from the depth; a chunk moving in front of a
// still camera gives its own pixels its own motion and leaves the wall's
// zero; and a camera that jumps four metres forgets the history.
func TestTAAMotionVectorsFollowTheCameraAndAMovingChunk(t *testing.T) {
	w := newVisWorld(t)
	TAAResolve = true
	lvl := &world.Level{Static: &world.StaticGeometry{Surfaces: []world.StaticSurface{
		staticQuad(t, "w", "concrete", -8, 8, -8, 8, 0, 1)}}}
	lvl.Lights.Global = world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 1}
	world.UploadLights(lvl.Lights)
	t.Cleanup(func() { ReleaseChunkBatches(lvl) })
	c := w.size / 2
	tanHalf := float32(math.Tan(float64(mgl32.DegToRad(gfx.FieldOfView)) / 2))

	gfx.Cam.Position, gfx.Cam.Direction = mgl32.Vec3{0, 0, 4}, mgl32.Vec3{0, 0, -1}
	gfx.UpdateCamMatrix()
	w.frame(lvl)
	w.frame(lvl)
	gfx.Cam.Position = mgl32.Vec3{0.1, 0, 4}
	gfx.UpdateCamMatrix()
	w.frame(lvl)
	// The wall point now at the centre is (0.1, 0, 0); last frame's camera,
	// 0.1 to the left, saw it 0.1/4 of the way along a half-width of tanHalf.
	want := -0.5 * (0.1 / 4) / tanHalf
	if got := readMotion(c, c); math.Abs(float64(got[0]-want)) > 2e-3 || math.Abs(float64(got[1])) > 2e-3 {
		t.Errorf("camera motion at the centre: %v, want (%.4f, 0)", got, want)
	}

	// A chunk in front of the wall, moved 0.1 to the right with the camera
	// still, far enough back that the wall shows round it.
	gfx.Cam.Position = mgl32.Vec3{0, 0, 8}
	gfx.UpdateCamMatrix()
	ch := naniteChunkLevel(t).Chunks[0]
	ch.Origin = mgl32.Vec3{0, 0, 1}
	lvl.Chunks = []world.Chunk{ch}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	w.frame(lvl)
	w.frame(lvl)
	lvl.Chunks[0].Origin = mgl32.Vec3{0.1, 0, 1}
	w.frame(lvl)
	// Its wall brush's face at z = 1.1 is at the centre; that point was 0.1
	// to the left last frame, 6.9 in front of the eye.
	wantChunk := 0.5 * (0.1 / 6.9) / tanHalf
	if got := readMotion(c, c); math.Abs(float64(got[0]-wantChunk)) > 2e-3 || math.Abs(float64(got[1])) > 2e-3 {
		t.Errorf("a moving chunk's motion at the centre: %v, want (%.4f, 0)", got, wantChunk)
	}
	if got := readMotion(2, 2); got.Len() > 1e-3 {
		t.Errorf("the still wall behind it moved by %v", got)
	}

	// A cut: the history is not resolved against.
	if !taa.historyValid {
		t.Fatal("no history after still frames")
	}
	gfx.Cam.Position = mgl32.Vec3{0, 0, 13}
	gfx.UpdateCamMatrix()
	w.frame(lvl)
	if taa.resolvedWithHistory {
		t.Error("a jump of five metres was resolved against the history from before it")
	}
	w.frame(lvl)
	if !taa.resolvedWithHistory {
		t.Error("the frame after the cut was not resolved against the history")
	}
}

// A cutout's coverage, which alpha-to-coverage cannot spend without samples,
// is dithered frame to frame in a TAA frame and resolved by the history: one
// frame's edge is all or nothing, the converged edge has the partial values
// a supersampled edge has, and it is closer to that edge than the one-sample
// frame is.
func TestTAAResolvesDitheredCutoutCoverage(t *testing.T) {
	w := newVisWorld(t)
	gfx.Cam.Position, gfx.Cam.Direction = mgl32.Vec3{0, 0, 3}, mgl32.Vec3{0, 0, -1}
	gfx.UpdateCamMatrix()
	// A white disc on a texture clear outside it.
	const ts = 128
	img := make([]uint8, ts*ts*4)
	for y := 0; y < ts; y++ {
		for x := 0; x < ts; x++ {
			dx, dy := float64(x)+0.5-ts/2, float64(y)+0.5-ts/2
			i := (y*ts + x) * 4
			img[i], img[i+1], img[i+2] = 255, 255, 255
			if dx*dx+dy*dy < (0.4*ts)*(0.4*ts) {
				img[i+3] = 255
			}
		}
	}
	var tex uint32
	gl.GenTextures(1, &tex)
	gl.BindTexture(gl.TEXTURE_2D, tex)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, ts, ts, 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&img[0]))
	gl.GenerateMipmap(gl.TEXTURE_2D)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR_MIPMAP_LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gfx.Textures["vis_test_disc"] = tex
	t.Cleanup(func() {
		delete(gfx.Textures, "vis_test_disc")
		gl.DeleteTextures(1, &tex)
	})
	disc := staticQuad(t, "d", "vis_test_disc", -1, 1, -1, 1, 0, 1)
	// Lit evenly white, so the disc is white and its edge the only grey.
	for i := range disc.Geometry.VertexLight {
		disc.Geometry.VertexLight[i] = []float32{1, 1, 1}
	}
	if err := disc.Geometry.Load([][]float32{{-1, -1, 0, 0, 0}, {1, -1, 0, 1, 0}, {1, 1, 0, 1, 1}, {-1, 1, 0, 0, 1}},
		[]int{0, 1, 2, 0, 2, 3}, "vis_test_disc", mgl32.Vec3{}); err != nil {
		t.Fatal(err)
	}
	lvl := &world.Level{Static: &world.StaticGeometry{Surfaces: []world.StaticSurface{disc}}}
	lvl.Lights.Global = world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 1}
	world.UploadLights(lvl.Lights)
	t.Cleanup(func() { ReleaseChunkBatches(lvl) })

	big := int32(4 * naniteTestSize)
	bigFBO := w.target(big)
	platform.WinWidth, platform.WinHeight = big, big
	TAAResolve = false
	w.frameInto(lvl, bigFBO, big)
	ref := boxDown(w.frameInto(lvl, bigFBO, big), big, 4)
	platform.WinWidth, platform.WinHeight = naniteTestSize, naniteTestSize
	TAAResolve = true
	gfx.TAASharpen = 0
	ForgetTAAHistory()
	first := w.frame(lvl)
	var resolved []uint8
	for i := 1; i < TAAShotFrames(); i++ {
		resolved = w.frame(lvl)
	}
	partial := func(px []uint8) int {
		n := 0
		for i := 0; i+3 < len(px); i += 4 {
			if px[i] > 70 && px[i] < 235 {
				n++
			}
		}
		return n
	}
	// The disc's area in pixels, coverage summed: the background's red is
	// 0, the disc's full 1.
	bg := float64(ref[0])
	area := func(px []uint8) float64 {
		a := 0.0
		for i := 0; i+3 < len(px); i += 4 {
			a += min(max((float64(px[i])-bg)/(255-bg), 0), 1)
		}
		return a
	}
	af, ar, aref := area(first), area(resolved), area(ref)
	t.Logf("disc area: one frame %.0f, resolved %.0f, supersampled %.0f pixels", af, ar, aref)
	// Coverage is the chance a pixel is kept, so the resolved disc has the
	// supersampled one's area; a plain cut keeps every pixel with any
	// coverage and grows it by half a pixel all round.
	if d := math.Abs(ar-aref) / aref; d > 0.006 {
		t.Errorf("the resolved disc's area %.0f is %.1f%% off the supersampled %.0f", ar, 100*d, aref)
	}
	pf, pr, pref := partial(first), partial(resolved), partial(ref)
	dumpPNG("disc_first", first, int(w.size))
	dumpPNG("disc_resolved", resolved, int(w.size))
	dumpPNG("disc_ref", ref, int(w.size))
	ef, _ := meanAbs(first, ref)
	er, _ := meanAbs(resolved, ref)
	t.Logf("edge pixels between: first frame %d, resolved %d, supersampled %d; error %.2f -> %.2f", pf, pr, pref, ef, er)
	if pref < 30 {
		t.Fatalf("the supersampled disc has only %d partial edge pixels; the disc is not in view", pref)
	}
	if pr < pref/2 || pf > pref/4 {
		t.Errorf("partial edge pixels: one frame %d, resolved %d, supersampled %d — want one frame's edge hard and the resolved one soft",
			pf, pr, pref)
	}
	if er > 0.8*ef {
		t.Errorf("the resolved disc's error %.2f is not clearly under one frame's %.2f", er, ef)
	}
}
