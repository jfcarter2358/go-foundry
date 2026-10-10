package render

// nfoliage_gl_test.go — the Nanite foliage path on a real GPU.
//
//	FOUNDRY_GL_TEST=1 go test -run NaniteFoliage ./internal/render/
//
// Set FOUNDRY_TEST_DUMP to a directory to keep the pictures compared.

import (
	"fmt"
	"math"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/glext"
	"foundry/internal/gltest"
	"foundry/internal/platform"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// nfProbeSize is the probe's picture, square; nfSuper its supersampled
// reference's factor.
const (
	nfProbeSize = 192
	nfSuper     = 4
)

// nfProbe draws a level's terrain and foliage into a target of its own, lit
// by a sun and an ambient.
type nfProbe struct {
	t     *testing.T
	lvl   *world.Level
	tr    *world.Terrain
	fbo   uint32
	super uint32
}

// nfTarget makes a colour + depth framebuffer size square.
func nfTarget(t *testing.T, size int32) uint32 {
	t.Helper()
	var color, depth, fbo uint32
	gl.GenTextures(1, &color)
	gl.BindTexture(gl.TEXTURE_2D, color)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, size, size, 0, gl.RGBA, gl.UNSIGNED_BYTE, nil)
	gl.GenTextures(1, &depth)
	gl.BindTexture(gl.TEXTURE_2D, depth)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.DEPTH_COMPONENT24, size, size, 0, gl.DEPTH_COMPONENT, gl.UNSIGNED_INT, nil)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	gl.GenFramebuffers(1, &fbo)
	gl.BindFramebuffer(gl.FRAMEBUFFER, fbo)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, color, 0)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.DEPTH_ATTACHMENT, gl.TEXTURE_2D, depth, 0)
	if s := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); s != gl.FRAMEBUFFER_COMPLETE {
		t.Fatalf("probe framebuffer incomplete: 0x%x", s)
	}
	t.Cleanup(func() {
		gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
		gl.DeleteFramebuffers(1, &fbo)
		gl.DeleteTextures(1, &color)
		gl.DeleteTextures(1, &depth)
	})
	return fbo
}

func newNFProbe(t *testing.T, tr *world.Terrain) *nfProbe {
	t.Helper()
	gltest.Context(t)
	withRepoResources(t)
	gfx.TextureNames = gfx.ScanTextures(fileutil.TexturesDir())
	if err := InitShadows(repoShader(t, "shadow_vert.glsl"), repoShader(t, "shadow_frag.glsl"),
		repoShader(t, "shadow_cube_vert.glsl"), repoShader(t, "shadow_cube_frag.glsl")); err != nil {
		t.Fatalf("shadow programs: %v", err)
	}
	// The path on, whatever the run's switch says: these tests are its.
	prevNF := NaniteFoliage
	NaniteFoliage = true
	TerrainInit()
	FoliageInit()
	if !terrains.initialized || !foliage.initialized {
		t.Fatal("the terrain or foliage pass did not come up")
	}
	t.Cleanup(func() {
		ReleaseTerrainMeshes()
		ReleaseFoliage()
		terrains.initialized = false
		foliage.initialized = false
		NaniteFoliage = prevNF
	})
	p := &nfProbe{t: t, tr: tr}
	p.fbo = nfTarget(t, nfProbeSize)
	p.super = nfTarget(t, nfProbeSize*nfSuper)
	// The LOD's k is the frame's height (nanite_lod.go): the probe's.
	pw, ph := platform.WinWidth, platform.WinHeight
	platform.WinWidth, platform.WinHeight = nfProbeSize, nfProbeSize
	t.Cleanup(func() { platform.WinWidth, platform.WinHeight = pw, ph })
	gl.Viewport(0, 0, nfProbeSize, nfProbeSize)
	gl.Enable(gl.DEPTH_TEST)
	gl.DepthFunc(gl.LEQUAL)

	var ls world.LightSet
	ls.Global.Color = mgl32.Vec3{1, 1, 1}
	ls.Global.Brightness = 0.6
	sun := world.DirectionalLight{ID: "sun", Yaw: 35, Pitch: -45, Color: mgl32.Vec3{1, 0.96, 0.9}, Brightness: 0.8}
	sun.SyncDirection()
	ls.Directional = append(ls.Directional, sun)
	ch := world.Chunk{ID: "ground", Terrain: tr}
	ch.RecomputeBounds()
	p.lvl = &world.Level{Chunks: []world.Chunk{ch}, Lights: ls}
	return p
}

// look puts the camera at pos looking along dir, with a 60° lens.
func (p *nfProbe) look(pos, dir mgl32.Vec3) {
	gfx.Projection = mgl32.Perspective(mgl32.DegToRad(60), 1, 0.1, 3000)
	gfx.Cam.Position = pos
	gfx.Cam.Direction = dir.Normalize()
	gfx.UpdateCamMatrix()
}

// frame syncs and draws the terrain and foliage, and reads the picture.
func (p *nfProbe) frame() []uint8 { return p.frameAt(p.fbo, nfProbeSize) }

// superFrame is the frame drawn at nfSuper times the size and boxed down:
// what the picture should be, every pixel's coverage and colour averaged
// over nfSuper² samples.
func (p *nfProbe) superFrame() []uint8 {
	big := p.frameAt(p.super, nfProbeSize*nfSuper)
	return boxDown(big, nfProbeSize*nfSuper, nfSuper)
}

func (p *nfProbe) frameAt(fbo uint32, size int32) []uint8 {
	p.t.Helper()
	SyncTerrainMeshes(p.lvl)
	gl.Finish()
	gl.BindFramebuffer(gl.FRAMEBUFFER, fbo)
	gl.Viewport(0, 0, size, size)
	gl.Disable(gl.CULL_FACE)
	gl.ClearColor(0.55, 0.7, 0.9, 1)
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	pv := gfx.Projection.Mul4(gfx.Cam.Matrix)
	f := ExtractFrustum(pv)
	RenderTerrain(p.lvl, pv, &f, ShadowFrame{})
	RenderFoliage(p.lvl, pv, &f, ShadowFrame{})
	if e := gl.GetError(); e != gl.NO_ERROR {
		p.t.Fatalf("GL error 0x%x drawing foliage", e)
	}
	px := make([]uint8, size*size*4)
	glext.ReadPixels(0, 0, size, size, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
	return px
}

// nfForest is a flat field size across, painted with preset, its near edge
// at x0.
func nfForest(t *testing.T, preset string, x0 float32, size int) *world.Terrain {
	t.Helper()
	tr := world.NewTerrain(mgl32.Vec3{x0, -2, -float32(size) / 2}, 1, size, 6, size)
	tr.Generate(world.TerrainFill{Height: 2, Top: 1, Under: 1, Deep: 1})
	tr.Biomes = []world.TerrainBiome{{Preset: preset}}
	for z := 0; z < size; z++ {
		for x := 0; x < size; x++ {
			tr.SetPaint(tr.FaceKey(x, 1, z, world.FaceYPos), 1)
		}
	}
	return tr
}

// nfMeanOver is the mean colour of the pixels of px that differ from bare
// (the same view without the foliage).
func nfMeanOver(px, bare []uint8) (mean [3]float64, n int) {
	for i := 0; i+3 < len(px); i += 4 {
		d := 0
		for k := 0; k < 3; k++ {
			e := int(px[i+k]) - int(bare[i+k])
			d = max(d, e, -e)
		}
		if d <= 6 {
			continue
		}
		for k := 0; k < 3; k++ {
			mean[k] += float64(px[i+k])
		}
		n++
	}
	if n > 0 {
		for k := range mean {
			mean[k] /= float64(n)
		}
	}
	return
}

// nfOnlyPreset is a biome of one of preset's species alone (the first of
// shape), named "nf_only".
func nfOnlyPreset(t *testing.T, preset string, shape world.FoliageShape) string {
	t.Helper()
	src := world.FoliagePresetByName(preset)
	if src == nil {
		t.Fatalf("no preset %q", preset)
	}
	p := world.FoliagePreset{Name: "nf_only", Label: "only", GroundTint: src.GroundTint}
	for _, sp := range src.Species {
		if sp.Shape == shape {
			p.Species = append(p.Species, sp)
			break
		}
	}
	world.PutFoliagePreset(p)
	t.Cleanup(func() { world.LoadBiomes(t.TempDir()) })
	return "nf_only"
}

// nfMass is how much px differs from bare in all: the sum over pixels and
// channels of bare − px — coverage times contrast, which a supersampled
// reference and an aliased frame agree on where they agree on coverage.
func nfMass(px, bare []uint8) float64 {
	m := 0.0
	for i := 0; i+3 < len(px); i += 4 {
		for k := 0; k < 3; k++ {
			m += float64(int(bare[i+k]) - int(px[i+k]))
		}
	}
	return m
}

// nfStill stops the wind for a test, so the classic sway and the bones —
// which bend differently — leave every plant where it stands.
func nfStill(t *testing.T) {
	t.Helper()
	prev := foliageWind.strength
	foliageWind.strength = 0
	t.Cleanup(func() { foliageWind.strength = prev })
}

// nfBare draws the probe's view without the foliage: the terrain's paint
// taken away for the frame and put back.
func (p *nfProbe) bare(super bool) []uint8 {
	paint := p.tr.Paint
	p.tr.Paint = nil
	p.tr.Touch()
	defer func() {
		p.tr.Paint = paint
		p.tr.Touch()
	}()
	if super {
		return p.superFrame()
	}
	return p.frame()
}

// pixelsOver counts the pixels whose largest channel difference passes by.
func pixelsOver(a, b []uint8, by int) int {
	n := 0
	for i := 0; i+3 < len(a); i += 4 {
		d := 0
		for k := 0; k < 3; k++ {
			e := int(a[i+k]) - int(b[i+k])
			d = max(d, e, -e)
		}
		if d > by {
			n++
		}
	}
	return n
}

// Up close a forest on the Nanite path is the forest the classic path draws:
// the same trees at full detail — parts placed by their transforms, the core
// as it was merged — in the same light, so the pictures agree but for
// rounding where a card's corners are worked out by a matrix on the GPU
// rather than added up on the CPU.
func TestNaniteFoliageDrawsTheClassicNearField(t *testing.T) {
	nfStill(t)
	// Every tree within 30 m: the triangles' range at this size (the
	// voxels' agreement is the next test's).
	tr := nfForest(t, nfOnlyPreset(t, "forest", world.FoliageBroadleaf), -6, 18)
	p := newNFProbe(t, tr)
	if !foliage.nan.ok {
		t.Fatal("the Nanite path did not come up")
	}
	for i, pose := range []struct{ pos, dir mgl32.Vec3 }{
		{mgl32.Vec3{-10, 3, 0}, mgl32.Vec3{1, 0.1, 0}},
		{mgl32.Vec3{-12, 8, 6}, mgl32.Vec3{1, -0.35, -0.3}},
	} {
		p.look(pose.pos, pose.dir)
		NaniteFoliage = false
		p.frame()
		classic := p.frame()
		NaniteFoliage = true
		p.frame()
		nanite := p.frame()
		bare := p.bare(false)
		covered := pixelsOver(classic, bare, 8)
		dumpPNG(fmt.Sprintf("nf_near_%d_classic", i), classic, nfProbeSize)
		dumpPNG(fmt.Sprintf("nf_near_%d_nanite", i), nanite, nfProbeSize)
		if covered < nfProbeSize*nfProbeSize/8 {
			t.Fatalf("pose %d: the forest covers only %d pixels", i, covered)
		}
		over8, over32 := pixelsOver(classic, nanite, 8), pixelsOver(classic, nanite, 32)
		t.Logf("pose %d: %d pixels of forest; %d differ by more than 8, %d by more than 32", i, covered, over8, over32)
		if over8 > covered/50 || over32 > covered/200 {
			t.Errorf("pose %d: the Nanite near field differs from the classic one in %d pixels (%d past 32) of %d", i, over8, over32, covered)
		}
	}
}

// nfSweep is what nfMass measures a forest at for each distance in ds: the
// probe's own frame against its bare frame, and (ref) the full detail
// supersampled against its own bare frame. look places the camera.
func nfSweep(p *nfProbe, ds []float32, look func(d float32), ref bool) (cut, full []float64) {
	saved := naniteFoliagePixels
	defer func() { naniteFoliagePixels = saved }()
	for _, d := range ds {
		look(d)
		if ref {
			naniteFoliagePixels = 0.001
			bareS := p.bare(true)
			p.superFrame()
			full = append(full, nfMass(p.superFrame(), bareS))
			naniteFoliagePixels = saved
		}
		bare := p.bare(false)
		p.frame()
		cut = append(cut, nfMass(p.frame(), bare))
	}
	return
}

// The voxels keep the forest's look. A row of broadleaf trees seen against
// the sky, pulled away from 4 m to 400 m: its picture — how much of the sky
// it hides, times how dark it is (nfMass) — against the same trees at full
// detail supersampled 4x4, the picture they ought to make, stays within a
// fifth of it at every distance, triangles, the hand-over and voxels alike;
// and from one step to the next (15% further) it never jumps: no band where
// a tier hands over, no pop where an impostor would have.
//
// The classic tiers, measured the same way, shift where the trees hand over
// to their impostors: the forest is a third lighter past it than before it
// (the band the spec's "done when" names). The Nanite path's two sides of
// that distance agree.
//
// A forest a kilometre off is still drawn, and drawn in voxels.
func TestNaniteFoliageKeepsTheForestsLookToTheHorizon(t *testing.T) {
	tr := nfForest(t, nfOnlyPreset(t, "forest", world.FoliageBroadleaf), 0, 48)
	p := newNFProbe(t, tr)
	var ds []float32
	for d := float32(4); d < 400; d *= 1.15 {
		ds = append(ds, d)
	}
	look := func(d float32) { p.look(mgl32.Vec3{-d, 1, 0}, mgl32.Vec3{1, 0.02, 0}) }
	cut, full := nfSweep(p, ds, look, true)
	prev := -1.0
	for i, d := range ds {
		if full[i] < 40000 {
			// Too few pixels to say anything (a dozen trees a few pixels
			// each).
			continue
		}
		r := cut[i] / full[i]
		t.Logf("at %5.1f m: the cut's picture is %.3f of the supersampled full detail's", d, r)
		if r < 0.8 || r > 1.25 {
			t.Errorf("at %.1f m the forest's picture is %.3f of what it should be", d, r)
		}
		if prev > 0 && math.Abs(r-prev) > 0.1 {
			t.Errorf("at %.1f m the forest's picture jumps from %.3f to %.3f of what it should be", d, prev, r)
		}
		prev = r
	}
	// Either side of the classic impostor hand-over: the trees' far tier
	// starts at foliageShapes' far (scaled), the forest 48 m deep behind its
	// edge.
	hand := foliageShapes[world.FoliageBroadleaf].far * FoliageDistanceScale()
	var side []float32
	for d := hand * 0.35; d < hand*0.75; d *= 1.1 {
		side = append(side, d)
	}
	before := len(side)
	for d := hand * 1.1; d < hand*1.9; d *= 1.1 {
		side = append(side, d)
	}
	nanite, ref := nfSweep(p, side, look, true)
	NaniteFoliage = false
	classic, _ := nfSweep(p, side, look, false)
	NaniteFoliage = true
	shift := func(m []float64) float64 {
		var a, b float64
		for i := range side {
			if i < before {
				a += m[i] / ref[i] / float64(before)
			} else {
				b += m[i] / ref[i] / float64(len(side)-before)
			}
		}
		return b - a
	}
	cs, ns := shift(classic), shift(nanite)
	t.Logf("across the classic impostor hand-over (%.0f m) the picture shifts by %.3f classic, %.3f Nanite", hand, cs, ns)
	if math.Abs(ns) > 0.08 || math.Abs(ns) > math.Abs(cs)/2 {
		t.Errorf("across %.0f m the Nanite forest shifts by %.3f (the classic by %.3f)", hand, ns, cs)
	}

	// A kilometre off.
	far := nfForest(t, nfOnlyPreset(t, "forest", world.FoliageBroadleaf), 1000, 48)
	pf := newNFProbe(t, far)
	pf.look(mgl32.Vec3{0, 1, 0}, mgl32.Vec3{1, 0.01, 0})
	cutFar, fullFar := nfSweep(pf, []float32{0}, func(float32) {}, true)
	for i := 0; i < 4; i++ {
		pf.frame()
		gl.Finish()
	}
	if foliage.nan.last.voxRefs == 0 {
		t.Error("the forest a kilometre off was not drawn in voxels")
	}
	if fullFar[0] <= 0 || cutFar[0] < 0.5*fullFar[0] || cutFar[0] > 2*fullFar[0] {
		t.Errorf("a forest a kilometre off draws %.0f against the full detail's %.0f", cutFar[0], fullFar[0])
	}
}

// The bones the GPU poses are poseFoliageSlot's: every slot of a tree, a
// grass tuft and a palm, both wind directions, within float rounding.
func TestNaniteFoliageBonesArePosedLikeTheCPU(t *testing.T) {
	p := newNFProbe(t, nfForest(t, "forest", 0, 16))
	r := &foliage.nan
	prevTime := BiomeWindTime
	t.Cleanup(func() { BiomeWindTime = prevTime })
	BiomeWindTime = 3.25
	foliage.frame++
	r.poseBones()
	_ = p
	data := make([]float32, r.pose[r.poseCur].size/4)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, r.pose[r.poseCur].buf)
	glext.GetBufferSubData(gl.SHADER_STORAGE_BUFFER, 0, len(data)*4, gl.Ptr(&data[0]))
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	worst := float32(0)
	for _, sv := range [][2]int{{int(world.FoliageBroadleaf), 1}, {int(world.FoliageGrass), 0}, {int(world.FoliagePalm), 2}} {
		bucket := nfBucketOf(world.FoliageShape(sv[0]), sv[1])
		a := recordFoliageAssembly(world.FoliageShape(sv[0]), sv[1])
		first := r.data.buckets[bucket].firstBone
		for g := 0; g < nfGusts; g++ {
			for f := 0; f < nfFlutters; f++ {
				for dir := 0; dir < 2; dir++ {
					want := poseFoliageSlot(a.bones, foliageWind.strength, g, f, BiomeWindTime, dir)
					for b := range a.bones {
						for which := 0; which < 2; which++ {
							m := want[2*b+which]
							i := ((((first+b)*nfSlots+g*nfFlutters+f)*2+dir)*2 + which) * 3 * 4
							for row := 0; row < 3; row++ {
								for col := 0; col < 4; col++ {
									d := data[i+row*4+col] - m[col*4+row]
									worst = max(worst, d, -d)
								}
							}
						}
					}
				}
			}
		}
	}
	if worst > 1e-4 {
		t.Fatalf("the GPU's poses differ from the CPU's by up to %v", worst)
	}
}

// nfShadowDepth draws the casting foliage into a depth target with an
// orthographic sun and reads the depth back.
func nfShadowDepth(t *testing.T, p *nfProbe, lsm mgl32.Mat4) []float32 {
	t.Helper()
	const size = 256
	var depth, fbo uint32
	gl.GenTextures(1, &depth)
	gl.BindTexture(gl.TEXTURE_2D, depth)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.DEPTH_COMPONENT32F, size, size, 0, gl.DEPTH_COMPONENT, gl.FLOAT, nil)
	gl.GenFramebuffers(1, &fbo)
	gl.BindFramebuffer(gl.FRAMEBUFFER, fbo)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.DEPTH_ATTACHMENT, gl.TEXTURE_2D, depth, 0)
	gl.DrawBuffer(gl.NONE)
	defer func() {
		gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
		gl.DeleteFramebuffers(1, &fbo)
		gl.DeleteTextures(1, &depth)
	}()
	gl.Viewport(0, 0, size, size)
	gl.ClearDepth(1)
	gl.Clear(gl.DEPTH_BUFFER_BIT)
	gl.Enable(gl.DEPTH_TEST)
	gl.DepthFunc(gl.LESS)
	SyncTerrainMeshes(p.lvl)
	RenderFoliageDirShadow(p.lvl, lsm, 0, nil)
	if e := gl.GetError(); e != gl.NO_ERROR {
		t.Fatalf("GL error 0x%x casting foliage", e)
	}
	out := make([]float32, size*size)
	glext.ReadPixels(0, 0, size, size, gl.DEPTH_COMPONENT, gl.FLOAT, gl.Ptr(&out[0]))
	gl.DepthFunc(gl.LEQUAL)
	return out
}

// A shadow pass casts each variant's shadow proxy — the classic far mesh on
// the full plant's bones — so in still air the Nanite path's shadow is the
// classic path's, texel for texel but for rounding.
func TestNaniteFoliageCastsTheClassicShadow(t *testing.T) {
	nfStill(t)
	tr := nfForest(t, nfOnlyPreset(t, "forest", world.FoliageBroadleaf), -16, 32)
	p := newNFProbe(t, tr)
	p.look(mgl32.Vec3{-20, 8, 0}, mgl32.Vec3{1, -0.2, 0})
	p.frame()
	lsm := mgl32.Ortho(-20, 20, -20, 20, -60, 60).Mul4(mgl32.LookAtV(mgl32.Vec3{6, 20, 4}, mgl32.Vec3{}, mgl32.Vec3{0, 0, 1}))
	NaniteFoliage = false
	classic := nfShadowDepth(t, p, lsm)
	NaniteFoliage = true
	nanite := nfShadowDepth(t, p, lsm)
	cast, both, differ := 0, 0, 0
	for i := range classic {
		a, b := classic[i] < 1, nanite[i] < 1
		if a || b {
			cast++
		}
		if a && b {
			both++
			if d := classic[i] - nanite[i]; d > 1e-3 || d < -1e-3 {
				differ++
			}
		}
	}
	if cast < 2000 {
		t.Fatalf("the forest casts only %d texels", cast)
	}
	iou := float64(both) / float64(cast)
	t.Logf("%d texels cast, %.4f of them by both paths, %d of those at a different depth", cast, iou, differ)
	if iou < 0.98 || differ > cast/100 {
		t.Errorf("the Nanite path's shadow overlaps the classic one's by %.4f, %d texels at a different depth", iou, differ)
	}
}

// The Nanite path takes the tall shapes — the ones that had an impostor —
// and leaves the ground cover to the classic tiers unless asked; switched
// off, it takes nothing.
func TestNaniteFoliageSharesTheFieldWithTheTiers(t *testing.T) {
	p := newNFProbe(t, nfForest(t, "forest", -8, 32))
	p.look(mgl32.Vec3{-12, 4, 0}, mgl32.Vec3{1, -0.1, 0})
	p.frame()
	shapes := func() map[world.FoliageShape]bool {
		out := map[world.FoliageShape]bool{}
		v := nfCameraView(gfx.Projection.Mul4(gfx.Cam.Matrix), nil)
		foliage.nan.gather(v)
		for i := 0; i+3 < len(foliage.nan.tiles); i += 4 {
			b := int(foliage.nan.tiles[i+2])
			for s := world.FoliageShape(0); s < world.FoliageShapeCount; s++ {
				if b >= nfBucketOf(s, 0) && b < nfBucketOf(s, 0)+world.FoliageVariants[s] {
					out[s] = true
				}
			}
		}
		return out
	}
	got := shapes()
	if !got[world.FoliageBroadleaf] || got[world.FoliageGrass] || got[world.FoliageFern] {
		t.Fatalf("the Nanite path takes %v; want the trees and not the ground cover", got)
	}
	NaniteFoliageGround = true
	t.Cleanup(func() { NaniteFoliageGround = false })
	if got := shapes(); !got[world.FoliageGrass] || !got[world.FoliageBroadleaf] {
		t.Fatalf("with the ground cover asked for, the Nanite path takes %v", got)
	}
	NaniteFoliage = false
	if got := shapes(); len(got) != 0 || foliage.nan.takes(world.FoliageBroadleaf) {
		t.Fatalf("switched off, the Nanite path still takes %v", got)
	}
}

// Lists too small for a frame are cut, noted and grown: within a few frames
// the picture is the one roomy lists draw.
func TestNaniteFoliageGrowsItsLists(t *testing.T) {
	nfStill(t)
	p := newNFProbe(t, nfForest(t, "forest", -8, 48))
	p.look(mgl32.Vec3{-20, 6, 0}, mgl32.Vec3{1, -0.1, 0})
	p.frame()
	want := p.frame()
	r := &foliage.nan
	r.itemCap, r.refCap = 32, 128
	r.items.make(gl.RGBA32UI, nil, r.itemCap*16)
	gl.BindBuffer(gl.ARRAY_BUFFER, r.refBuf)
	gl.BufferData(gl.ARRAY_BUFFER, r.refCap*8, nil, gl.DYNAMIC_DRAW)
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)
	starved := p.frame()
	if pixelsOver(want, starved, 8) == 0 {
		t.Fatal("lists of 32 items and 128 references drew the whole forest")
	}
	var got []uint8
	for i := 0; i < 24; i++ {
		got = p.frame()
		if r.itemCap > 32 && r.refCap > 128 && r.last.overflow == 0 {
			break
		}
	}
	got = p.frame()
	if r.itemCap <= 32 || r.refCap <= 128 {
		t.Fatalf("the lists did not grow: %d items, %d references", r.itemCap, r.refCap)
	}
	if n := pixelsOver(want, got, 8); n > nfProbeSize*nfProbeSize/500 {
		t.Fatalf("after growing, %d pixels differ from the roomy picture", n)
	}
}

// nfObjectMotion reads the scene target's object-motion attachment (its
// clear value, taaNoObjectMotion, is "the camera's motion").
func nfObjectMotion(w, h int32) []float32 {
	out := make([]float32, w*h*4)
	gl.BindFramebuffer(gl.READ_FRAMEBUFFER, scene.fbo)
	gl.ReadBuffer(sceneAttachVelocity)
	glext.ReadPixels(0, 0, w, h, gl.RGBA, gl.FLOAT, gl.Ptr(&out[0]))
	gl.ReadBuffer(sceneAttachColour)
	gl.BindFramebuffer(gl.READ_FRAMEBUFFER, 0)
	return out
}

// In a TAA frame a swaying tree writes its own motion — each vertex's, from
// last frame's bones and this frame's — where the classic path leaves its
// pixels to the camera's motion and the history drags behind every branch.
// Still air and a still camera write none.
func TestNaniteFoliageWritesItsOwnMotion(t *testing.T) {
	prevNF := NaniteFoliage
	NaniteFoliage = true
	w := newVisWorld(t)
	TAAResolve = true
	tr := nfForest(t, nfOnlyPreset(t, "forest", world.FoliageBroadleaf), -8, 24)
	ch := world.Chunk{ID: "ground", Terrain: tr, Scale: 1}
	ch.RecomputeBounds()
	lvl := &world.Level{Chunks: []world.Chunk{ch}}
	lvl.Lights.Global = world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.8}
	world.UploadLights(lvl.Lights)
	t.Cleanup(func() {
		ReleaseTerrainMeshes()
		NaniteFoliage = prevNF
	})
	gfx.Cam.Position, gfx.Cam.Direction = mgl32.Vec3{-12, 4, 0}, mgl32.Vec3{1, 0.05, 0}
	gfx.UpdateCamMatrix()
	now := 10.0
	frames := func(n int) []float32 {
		for i := 0; i < n; i++ {
			now += 1.0 / 30
			DrawWorld(lvl, WorldDraw{Now: now, Skip: func(p string) bool { return p == "gi" },
				Target: &FrameTarget{FBO: w.fbo, Viewport: [4]int32{0, 0, w.size, w.size}}})
		}
		return nfObjectMotion(w.size, w.size)
	}
	// Pixels with motion of their own, and the largest.
	own := func(m []float32) (n int, most float32) {
		for i := 0; i+3 < len(m); i += 4 {
			if m[i] > taaNoObjectMotion/2 {
				continue
			}
			n++
			most = max(most, mgl32.Vec2{m[i], m[i+1]}.Len())
		}
		return
	}
	n, most := own(frames(4))
	t.Logf("in the wind: %d pixels move by themselves, the most by %.5f", n, most)
	if n < int(w.size*w.size)/20 || most < 1e-4 {
		t.Fatalf("in the wind only %d pixels have motion of their own (the most %.5f)", n, most)
	}
	prev := foliageWind.strength
	foliageWind.strength = 0
	t.Cleanup(func() { foliageWind.strength = prev })
	n, most = own(frames(3))
	if n == 0 || most > 2e-5 {
		t.Fatalf("in still air %d pixels have motion of their own, the most %.6f", n, most)
	}
	foliageWind.strength = prev
	NaniteFoliage = false
	if n, _ = own(frames(3)); n != 0 {
		t.Fatalf("the classic tiers wrote motion of their own in %d pixels", n)
	}
}

// The compute rasteriser draws what the ray march draws: the same row of
// trees at a range of distances, the rasteriser's picture against the
// supersampled full detail within what the march holds to, and against the
// march itself. It needs 64-bit atomics (glext): run natively, skipped on
// Mesa's D3D12 driver.
func TestNaniteFoliageComputeRasterDrawsTheMarchedPicture(t *testing.T) {
	tr := nfForest(t, nfOnlyPreset(t, "forest", world.FoliageBroadleaf), 0, 48)
	p := newNFProbe(t, tr)
	if !glext.Int64AtomicsAvailable() {
		t.Skip("no 64-bit atomics: the compute rasteriser does not run here")
	}
	r := &foliage.nan
	prev := NaniteFoliageSWRaster
	set := func(mode string) {
		NaniteFoliageSWRaster = mode
		r.vox.release()
		r.vox.init(r)
	}
	t.Cleanup(func() { set(prev) })
	var ds []float32
	for d := float32(60); d < 300; d *= 1.25 {
		ds = append(ds, d)
	}
	look := func(d float32) { p.look(mgl32.Vec3{-d, 1, 0}, mgl32.Vec3{1, 0.02, 0}) }
	set("0")
	marched, full := nfSweep(p, ds, look, true)
	set("auto")
	if !r.vox.ok {
		t.Fatal("the compute rasteriser did not come up")
	}
	raster, _ := nfSweep(p, ds, look, false)
	if r.last.smallBricks == 0 {
		t.Fatal("no brick was small enough for the rasteriser")
	}
	for i, d := range ds {
		rr, rm := raster[i]/full[i], marched[i]/full[i]
		t.Logf("at %5.1f m: rasteriser %.3f, march %.3f of the supersampled full detail", d, rr, rm)
		if rr < 0.8 || rr > 1.25 || math.Abs(rr-rm) > 0.12 {
			t.Errorf("at %.1f m the rasteriser's picture is %.3f of what it should be, the march's %.3f", d, rr, rm)
		}
	}
}

// Tree crowns go into the live GI's distance field: every tree of a unit
// near the camera is a mesh source — its variant's canopy grid placed where
// it stands — and that grid is solid in the crown and open round it, where
// a probe should see the sky. Switched off (the default), no tree is a
// source.
func TestNaniteFoliageCanopiesGoIntoTheGIField(t *testing.T) {
	tr := nfForest(t, nfOnlyPreset(t, "forest", world.FoliageBroadleaf), -16, 32)
	p := newNFProbe(t, tr)
	NaniteFoliageGI = true
	t.Cleanup(func() { NaniteFoliageGI = false })
	p.look(mgl32.Vec3{-20, 6, 0}, mgl32.Vec3{1, -0.1, 0})
	p.frame()
	trees := 0
	for di := range foliage.draws {
		if foliage.draws[di].d <= nfGIRange {
			trees += len(foliage.draws[di].u.trees)
		}
	}
	if trees < 10 {
		t.Fatalf("only %d trees near the camera", trees)
	}
	count := func() (n int, sdf *giMeshSDF) {
		s := newGISourceSet()
		s.collect(p.lvl)
		for k, g := range s.groups {
			if k.kind != 'f' {
				continue
			}
			for _, src := range g.srcs {
				if src.kind == giKindMesh {
					n++
					sdf = src.mesh
				}
			}
		}
		return
	}
	n, sdf := count()
	t.Logf("%d trees, %d canopy sources", trees, n)
	if n != trees || sdf == nil {
		t.Fatalf("%d trees near the camera gave %d canopy sources", trees, n)
	}
	// The grid's own sign: inside somewhere (the crown), outside at its
	// corners (the air round it).
	inside := 0
	for _, d := range sdf.Data {
		if d < 0 {
			inside++
		}
	}
	if inside < len(sdf.Data)/50 || sdf.Data[0] <= 0 || sdf.Data[len(sdf.Data)-1] <= 0 {
		t.Fatalf("the canopy grid has %d of %d cells inside, corners %v and %v", inside, len(sdf.Data), sdf.Data[0], sdf.Data[len(sdf.Data)-1])
	}
	NaniteFoliageGI = false
	if n, _ := count(); n != 0 {
		t.Fatalf("switched off, %d trees still went into the field", n)
	}
}

// And the field the GI builds from them has the crown in it: at a point the
// canopy grid holds inside a tree's crown, the field's distance is under a
// voxel, where without the canopies the nearest thing is the ground metres
// below.
func TestNaniteFoliageCanopiesShadeTheGIField(t *testing.T) {
	tr := nfForest(t, nfOnlyPreset(t, "forest", world.FoliageBroadleaf), -12, 24)
	p := newNFProbe(t, tr)
	NaniteFoliageGI = true
	t.Cleanup(func() { NaniteFoliageGI = false })
	p.look(mgl32.Vec3{0, 6, 0}, mgl32.Vec3{1, -0.1, 0})
	p.frame()
	// A tree and a point inside its crown.
	var tree *nfGITree
	var model mgl32.Mat4
	for di := range foliage.draws {
		if dr := &foliage.draws[di]; len(dr.u.trees) > 0 {
			tree, model = &dr.u.trees[0], *dr.model
			break
		}
	}
	if tree == nil {
		t.Fatal("no tree")
	}
	sdf := foliage.nan.canopy(tree.bucket)
	in := -1
	for i, d := range sdf.Data {
		if d < -0.5 && (in < 0 || d < sdf.Data[in]) {
			in = i
		}
	}
	if in < 0 {
		t.Fatal("the canopy has no inside")
	}
	n := sdf.N
	local := sdf.Lo.Add(mgl32.Vec3{float32(in % n), float32((in / n) % n), float32(in / (n * n))}.Mul(sdf.Cell))
	at := mulPoint(nfPlantMatrix(model, tree), local)
	t.Logf("inside the crown at %v (canopy %.2f cells in)", at, -sdf.Data[in])

	giTestSetup(t, gfx.GIMedium, nil)
	p.lvl.Lights.Global.GI = world.DefaultGISettings()
	field := func() float32 {
		gi.release()
		gi.failed = false
		giRunFrames(t, p.lvl, at, 2)
		return giFieldAt(t, 0, at)
	}
	with := field()
	NaniteFoliageGI = false
	without := field()
	voxel := gi.field.levels[0].voxel
	t.Logf("the field there: %.3f with the canopies, %.3f without (voxel %.2f)", with, without, voxel)
	if with > voxel || without < 3*voxel {
		t.Errorf("inside a crown the field reads %.3f with the canopies and %.3f without", with, without)
	}
}
