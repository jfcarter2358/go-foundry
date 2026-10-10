package render

// vsm.go — virtual shadow maps for the sun. Phase 5 of the Nanite plan
// (docs/nanite/PHASE_5.md), component 10.
//
// Three cascades of 2048² share one sun's shadow between them, so a
// cascade's texel grows with its slice: centimetres at the camera's feet,
// a quarter of a metre at the far end, and every hand-over a step in
// softness the filter has to hide. The virtual shadow map instead gives the
// sun one shadow of very large resolution — an eleven-level clipmap of 4096²
// levels, every point reading the level whose texel is about its own pixel
// (vsm_pages.go has the geometry) — and pays only for the parts of it some
// pixel reads, and only when they change.
//
// A frame, with FOUNDRY_NANITE_VSM on, in a scene-target frame:
//
//  1. vsmBegin, in RenderShadowPass where the cascades would be drawn: the
//     light's basis; what changed since the last frame (vsm_track.go) marks
//     its pages stale; the pages to draw are the requested ones that are not
//     valid — requested by the receivers of a frame or two ago (step 3,
//     read back without waiting) and, so nothing in view is ever without a
//     shadow, a coarse level's pages over the whole view from the CPU.
//     They are drawn a rectangle at a time (vsmPager.rects): an orthographic
//     view of the rectangle, its casters walked into a scratch depth target
//     exactly as for a cascade — the GPU cluster cull, the compiled surfaces,
//     the chunks and terrain, the props (the STATIC layer), then entities,
//     sprites, scatter and foliage (the MOVERS) — the static layer copied
//     out into each page's static copy after the first walk and the whole
//     depth into its physical page after the second. A page whose static
//     layer still holds and only something moving changed over it starts
//     from its static copy instead and walks the movers alone. Then the page
//     table goes up.
//  2. Every program that shades with the sun reads it (vsm.glsl, through
//     UploadShadowUniformsTo): the sun's cascades are not drawn at all.
//     Other casting directional lights keep theirs; point lights keep their
//     cubes.
//  3. vsmMarkReceivers, once the opaque world is drawn: a compute pass over
//     the frame's depth asks for the pages its pixels read
//     (vsm_mark_comp.glsl), copied into a readback ring with a fence.
//
// A page is drawn once and kept until something casting into it changes, so
// a still scene draws no shadow at all, and a character walking through it
// redraws itself — not the room — on the few pages under it on each level.
//
// What it keeps of the cascades' behaviour:
//
//   - the baked sun on compiled surfaces (fragment.glsl's bakedSunLight path)
//     swaps the bake's share for the live, shadowed sun within the shadow
//     distance exactly as before — calcDirShadowUnfaded answers from the
//     virtual map for its light, and ShadowFrame.BakedSunCast is set when the
//     sun it draws is the baked one;
//   - the shadow distance and its fade (dirShadowReach, ShadowMaxDistance);
//   - the cloud shadow, which is its own map multiplied on top;
//   - the cascades' code, untouched: a frame without the virtual map
//     (FOUNDRY_NANITE_VSM=0, MSAA or "off" anti-aliasing, the editor's
//     view) draws and reads them as it always did.
//
// The receivers' marks need the frame's depth in a texture, which only the
// scene target has (taa.go): a forward frame draws cascades.
//
// # Switches
//
//	FOUNDRY_NANITE_VSM=0/1         the virtual map (default: see NaniteVSM)
//	FOUNDRY_NANITE_VSM_PAGES=n     physical pages (128² texels, 64 KB each,
//	                               twice: the depth and the static copy)
//	FOUNDRY_NANITE_VSM_RES=r       texels a pixel across (1: one; 0.5: half
//	                               as fine, a quarter of the pages)
//	FOUNDRY_NANITE_VSM_FINISH=auto/0/1  wait for the GPU after drawing pages
//	                               (auto: on Mesa's D3D12 driver; see
//	                               NaniteVSMFinish)

import (
	"log"
	"math"
	"os"
	"strconv"
	"strings"

	"foundry/internal/gfx"
	"foundry/internal/glext"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// NaniteVSM switches the virtual shadow map; see the file comment.
var NaniteVSM = gfx.EnvFlag("FOUNDRY_NANITE_VSM", true)

// NaniteVSMRects is the most rectangles of pages a frame draws; the rest wait
// for the frames after (vsm_pages.go, "The budget"). 0: no limit.
var NaniteVSMRects = func() int {
	// 0 is a value here (no limit), which envInt would read as unset.
	if v, err := strconv.Atoi(os.Getenv("FOUNDRY_NANITE_VSM_RECTS")); err == nil && v >= 0 {
		return v
	}
	return 6
}()

// NaniteVSMPages is the physical page pool, read when it is first made.
var NaniteVSMPages = envInt("FOUNDRY_NANITE_VSM_PAGES", 2048)

// NaniteVSMResolution is the texels a pixel across a receiver reads; see the
// file comment.
var NaniteVSMResolution = envFloat("FOUNDRY_NANITE_VSM_RES", 1)

// NaniteVSMFinish waits for the GPU after a frame's pages are drawn: "auto"
// (the default) on Mesa's D3D12 driver only, "1" always, "0" never.
//
// On Mesa's D3D12 driver (the GL WSLg gives a Windows GPU) a frame that
// draws many pages — a rectangle a scene walk, each with its own cluster
// culls of the cooked meshes' streamed page pool — left so much in flight
// that a later glBufferData in the same frame (the foliage cull's, measured)
// never returned: 8 of 9 headless runs of the composite level hung so,
// none with this wait (docs/nanite/PHASE_5.md). Phase 1 met the same
// family (passStore). A still frame draws no page and does not wait, and
// natively the wait is never needed.
var NaniteVSMFinish = strings.ToLower(strings.TrimSpace(os.Getenv("FOUNDRY_NANITE_VSM_FINISH")))

// The texture units the map and its table are bound on, in every program
// that reads it: past the 32 the GI and the material pass use, inside the 80
// combined units GL 4.3 promises.
const (
	VSMAtlasUnit = 32
	VSMTableUnit = 33
)

const (
	// vsmDepthRange is the light-space depth the map holds, in world units,
	// about the camera; vsmDepthSnap the step its centre moves in (every
	// page is redrawn when it does). Casters nearer the light than its near
	// end are clamped onto it (GL_DEPTH_CLAMP) and still cast.
	vsmDepthRange = 2048.0
	vsmDepthSnap  = 256.0
	// vsmCullReach is how far toward the light a rectangle's casters are
	// gathered from.
	vsmCullReach = 1024.0
	// vsmSafetyLevel is the level whose pages the CPU asks for over the
	// whole view every frame, so a receiver whose own pages have not been
	// asked for yet (a frame or two after it came into view) reads a
	// coarse shadow rather than none: 25 cm texels, the far cascade's.
	vsmSafetyLevel = 9
	// vsmReadbacks is the depth of the marks' readback ring.
	vsmReadbacks = 3
	// The receiver's bias and normal offset, in its level's texels.
	vsmBiasTexels   = 1.5
	vsmNormalTexels = 1.0
)

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

// VSMFrame is a frame's virtual shadow map as the shaders read it, carried
// on ShadowFrame.VSM.
type VSMFrame struct {
	// Light is the directional light it shadows.
	Light       int
	rot         mgl32.Mat3
	camPos      mgl32.Vec3
	camTexel    [vsmLevels][2]float32
	levelBias   float32
	depthOffset float32
	depthScale  float32
}

// vsmSlot is one readback of the receivers' marks, with the windows it was
// made against.
type vsmSlot struct {
	rb     readbackSlot
	origin [vsmLevels][2]int32
}

// vsmRenderer is the virtual map's state; see the file comment.
type vsmRenderer struct {
	tried, ok bool
	pager     *vsmPager
	tracker   vsmTracker
	// The physical pages (a depth array, a page a layer), their static
	// layers (the same), the scratch rectangles are drawn into, and the page
	// table (R32UI, a layer a level).
	atlas, static, scratch, scratchFBO, table uint32
	// Dummies of the right types for a program's units when the map is off.
	dummyAtlas, dummyTable uint32
	mark                   progUniforms
	req                    uint32
	slots                  [vsmReadbacks]vsmSlot
	next                   int
	// requests is the newest marks read back, as pages.
	requests []vsmKey
	// The light's basis: right, up, and the direction it travels.
	basis [3][3]float64
	// zNear is the light-space depth the map's 0 is at.
	zNear     float64
	haveBasis bool
	camLS     [3]float64
	frame     VSMFrame
	tableData []uint32
	// finish: wait for the GPU after drawing pages (NaniteVSMFinish).
	finish bool
	// active: this frame's sun is the virtual map's (vsmBegin drew it).
	// lapsed: some frame since the last one that drew it did not (the
	// switch, the light, the editor's own view): nothing tracked what
	// changed in between, so the next one starts every page over.
	active, lapsed bool
	// Counts for the overlay.
	drawnPages, rects int
	// The sprites', scatter's and foliage's footprints across the light this
	// frame (empty: lo past hi), which a movers' rectangle must touch for
	// their walkers to be called.
	spriteRect, scatterRect, folRect [2][2]float64
}

var vsm vsmRenderer

func (v *vsmRenderer) ready() bool {
	if v.tried {
		return v.ok
	}
	v.tried = true
	pool := min(NaniteVSMPages, 2048)
	var maxLayers int32
	gl.GetIntegerv(gl.MAX_ARRAY_TEXTURE_LAYERS, &maxLayers)
	pool = min(pool, int(maxLayers))
	src := "#version 430\n" + mustReadShader(biomeShaderPath("vsm.glsl")) + "\n" +
		reVersion.ReplaceAllString(mustReadShader(biomeShaderPath("vsm_mark_comp.glsl")), "")
	cs, err := gfx.CompileShader(src, gl.COMPUTE_SHADER)
	if err != nil {
		log.Printf("nanite: virtual shadow map unavailable: marks: %v — the sun keeps its cascades", err)
		return false
	}
	prog, err := linkStages(cs)
	if err != nil {
		log.Printf("nanite: virtual shadow map unavailable: marks: %v — the sun keeps its cascades", err)
		return false
	}
	v.mark = progUniforms{prog: prog, loc: map[string]int32{}}

	for _, t := range []*uint32{&v.atlas, &v.static} {
		gl.GenTextures(1, t)
		gl.BindTexture(gl.TEXTURE_2D_ARRAY, *t)
		gl.TexImage3D(gl.TEXTURE_2D_ARRAY, 0, gl.DEPTH_COMPONENT32F, vsmPageTexels, vsmPageTexels, int32(pool), 0,
			gl.DEPTH_COMPONENT, gl.FLOAT, nil)
		vsmShadowSampling(gl.TEXTURE_2D_ARRAY)
	}
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, 0)

	gl.GenTextures(1, &v.scratch)
	gl.BindTexture(gl.TEXTURE_2D, v.scratch)
	sz := int32(vsmRectPages * vsmPageTexels)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.DEPTH_COMPONENT32F, sz, sz, 0, gl.DEPTH_COMPONENT, gl.FLOAT, nil)
	setNearestClamp()
	gl.BindTexture(gl.TEXTURE_2D, 0)
	gl.GenFramebuffers(1, &v.scratchFBO)
	prev := boundFramebuffer()
	gl.BindFramebuffer(gl.FRAMEBUFFER, v.scratchFBO)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.DEPTH_ATTACHMENT, gl.TEXTURE_2D, v.scratch, 0)
	gl.DrawBuffer(gl.NONE)
	gl.ReadBuffer(gl.NONE)
	st := gl.CheckFramebufferStatus(gl.FRAMEBUFFER)
	gl.BindFramebuffer(gl.FRAMEBUFFER, prev)
	if st != gl.FRAMEBUFFER_COMPLETE {
		log.Printf("nanite: virtual shadow map unavailable: scratch framebuffer incomplete (0x%x) — the sun keeps its cascades", st)
		return false
	}

	gl.GenTextures(1, &v.table)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, v.table)
	gl.TexImage3D(gl.TEXTURE_2D_ARRAY, 0, gl.R32UI, vsmWindowPages, vsmWindowPages, vsmLevels, 0,
		gl.RED_INTEGER, gl.UNSIGNED_INT, nil)
	setIntegerNearest(gl.TEXTURE_2D_ARRAY)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, 0)

	gl.GenBuffers(1, &v.req)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, v.req)
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, vsmReqWords*4, nil, gl.DYNAMIC_COPY)
	zero := uint32(0)
	gl.ClearBufferData(gl.SHADER_STORAGE_BUFFER, gl.R32UI, gl.RED_INTEGER, gl.UNSIGNED_INT, gl.Ptr(&zero))
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	glext.Init()
	persistent := NanitePersistent && glext.BufferStorageAvailable()
	for i := range v.slots {
		v.slots[i].rb.init(vsmReqWords*4, persistent)
	}
	v.pager = newVSMPager(pool)
	v.tracker.reset()
	switch NaniteVSMFinish {
	case "1", "on", "true", "yes":
		v.finish = true
	case "0", "off", "false", "no":
		v.finish = false
	default:
		v.finish = strings.Contains(gl.GoStr(gl.GetString(gl.RENDERER)), "D3D12")
	}
	v.ok = true
	log.Printf("nanite: virtual shadow map on (%d physical pages of %d², %d MB with their static copies; %d levels of %d²)",
		pool, vsmPageTexels, 2*pool*vsmPageTexels*vsmPageTexels*4>>20, vsmLevels, vsmLevelTexels)
	return true
}

// vsmReqWords is the marks buffer's words: a 32-page row of a level a word.
const vsmReqWords = vsmLevels * vsmWindowPages

// vsmShadowSampling sets the bound depth array up for vsm.glsl's lookups:
// read as plain depths (vsmCompare gathers four and compares them itself —
// see vsm.glsl for why not a comparison sampler), clamped to its own page.
func vsmShadowSampling(target uint32) {
	gl.TexParameteri(target, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
	gl.TexParameteri(target, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
	gl.TexParameteri(target, gl.TEXTURE_COMPARE_MODE, gl.NONE)
	gl.TexParameteri(target, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(target, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
}

func setIntegerNearest(target uint32) {
	gl.TexParameteri(target, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
	gl.TexParameteri(target, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
	gl.TexParameteri(target, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(target, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
}

// reset forgets everything (tests; the switch flipped back on).
func (v *vsmRenderer) reset() {
	if v.tried && v.ok {
		for _, t := range []*uint32{&v.atlas, &v.static, &v.scratch, &v.table} {
			gl.DeleteTextures(1, t)
		}
		gl.DeleteFramebuffers(1, &v.scratchFBO)
		gl.DeleteBuffers(1, &v.req)
		gl.DeleteProgram(v.mark.prog)
		for i := range v.slots {
			v.slots[i].rb.release()
		}
	}
	dummyAtlas, dummyTable := v.dummyAtlas, v.dummyTable
	*v = vsmRenderer{dummyAtlas: dummyAtlas, dummyTable: dummyTable}
}

// vsmLightFor is the directional light the virtual map shadows this frame,
// or -1: the compiled level's baked sun if it casts, else the first that
// casts — in a scene-target frame with the switch on.
func vsmLightFor(lights world.LightSet, level *world.Level) int {
	if !NaniteVSM || level == nil || !sceneFrameActive() {
		return -1
	}
	n := min(len(lights.Directional), world.MaxDirLights)
	if s := lights.BakedSun(); level.Static != nil && s >= 0 && s < n && lights.Directional[s].CastsShadows() {
		return s
	}
	for i := 0; i < n; i++ {
		if lights.Directional[i].CastsShadows() {
			return i
		}
	}
	return -1
}

// vsmBegin draws what the virtual map needs this frame for light li and
// fills frame's share of it; see the file comment. view and projection are
// the camera's; frame's cascade splits are what the scatter's casting range
// per level is borrowed from (cascadeFor). False when the map is not
// available, for the caller to draw the light's cascades instead.
func vsmBegin(lights world.LightSet, level *world.Level, li int, view, projection mgl32.Mat4, frame *ShadowFrame) bool {
	v := &vsm
	v.active = false
	if li < 0 || !v.ready() {
		return false
	}
	ProfileBegin("vsm")
	defer ProfileEnd()
	dl := lights.Directional[li]
	cam := gfx.Cam.Position
	v.setBasis(dl.Direction, cam)
	// Which level a point d away reads: log2(d) + levelBias.
	levelBias := float32(math.Log2(vsmPixelAngle(projection) / max(float64(NaniteVSMResolution), 1e-3) / vsmTexel(0)))
	v.frame.levelBias = levelBias

	// What changed: the static world (its pages drawn again whole), and
	// what moves (their movers over their static layers).
	all, boxes := v.tracker.collect(level, cam, ShadowMaxDistance)
	if all || v.lapsed {
		v.pager.invalidateAll()
		v.lapsed = false
	}
	for _, b := range boxes {
		lo, hi := v.lightRect(b)
		v.pager.invalidateRect(lo, hi, b.static)
	}
	// The movers' footprints across the light, for drawMovers.
	t := &v.tracker
	for i := range t.draws {
		t.draws[i].lo, t.draws[i].hi = v.lightRect(t.draws[i].box)
	}
	v.spriteRect = v.footprint(t.spriteBox, t.anySprites)
	v.scatterRect = v.footprint(t.scatterBox, t.anyScatter)
	v.folRect = v.footprint(t.folBox, t.anyFol)

	// What is asked for: the newest marks read back, and the safety level
	// over the view.
	v.pollMarks()
	keys := append([]vsmKey(nil), v.requests...)
	keys = v.appendSafety(keys, view, projection)
	whole, movers := v.pager.request(keys)
	whole, movers = v.pager.budget(whole, movers, NaniteVSMRects)
	v.drawnPages, v.rects = len(whole)+len(movers), 0
	if v.drawnPages > 0 {
		v.drawPages(level, whole, movers, dl, frame)
	}

	// The table.
	v.tableData = v.pager.table(v.tableData)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, v.table)
	gl.TexSubImage3D(gl.TEXTURE_2D_ARRAY, 0, 0, 0, 0, vsmWindowPages, vsmWindowPages, vsmLevels,
		gl.RED_INTEGER, gl.UNSIGNED_INT, gl.Ptr(&v.tableData[0]))
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, 0)

	v.frame = VSMFrame{Light: li, camPos: cam, depthScale: 1 / vsmDepthRange, levelBias: levelBias}
	r, u, d := v.basis[0], v.basis[1], v.basis[2]
	v.frame.rot = mgl32.Mat3FromRows(
		mgl32.Vec3{float32(r[0]), float32(r[1]), float32(r[2])},
		mgl32.Vec3{float32(u[0]), float32(u[1]), float32(u[2])},
		mgl32.Vec3{float32(d[0]), float32(d[1]), float32(d[2])})
	for l := 0; l < vsmLevels; l++ {
		t := vsmTexel(l)
		o := v.pager.origin[l]
		v.frame.camTexel[l] = [2]float32{float32(v.camLS[0]/t - float64(o[0])*vsmPageTexels),
			float32(v.camLS[1]/t - float64(o[1])*vsmPageTexels)}
	}
	v.frame.depthOffset = float32(v.camLS[2] - v.zNear)
	frame.VSM = &v.frame
	v.active = true
	DebugStats.VSMPages, DebugStats.VSMDrawn, DebugStats.VSMRects, DebugStats.VSMStarved, DebugStats.VSMDeferred =
		v.pager.resident(), v.drawnPages, v.rects, v.pager.starved, v.pager.deferred
	return true
}

// vsmPixelAngle is a pixel's footprint at unit distance: the frame's height
// over its vertical field of view.
func vsmPixelAngle(projection mgl32.Mat4) float64 {
	tanHalfV, _, _ := perspectiveParams(projection)
	h := float64(scene.h)
	if h < 1 {
		h = 1
	}
	return 2 * float64(tanHalfV) / h
}

// setBasis takes the light's direction of travel and the camera: a turned
// light (or the depth window's centre moving) makes every page stale.
func (v *vsmRenderer) setBasis(dir, cam mgl32.Vec3) {
	d := [3]float64{float64(dir[0]), float64(dir[1]), float64(dir[2])}
	n := math.Sqrt(d[0]*d[0] + d[1]*d[1] + d[2]*d[2])
	if n == 0 {
		d, n = [3]float64{0, -1, 0}, 1
	}
	d = [3]float64{d[0] / n, d[1] / n, d[2] / n}
	if !v.haveBasis || math.Abs(d[0]-v.basis[2][0])+math.Abs(d[1]-v.basis[2][1])+math.Abs(d[2]-v.basis[2][2]) > 1e-7 {
		up := [3]float64{0, 1, 0}
		if math.Abs(d[1]) > 0.99 {
			up = [3]float64{0, 0, 1}
		}
		r := cross64(up, d)
		r = norm64(r)
		u := cross64(d, r)
		v.basis = [3][3]float64{r, u, d}
		v.haveBasis = true
		v.zNear = math.NaN()
		if v.pager != nil {
			v.pager.invalidateAll()
		}
	}
	c := [3]float64{float64(cam[0]), float64(cam[1]), float64(cam[2])}
	v.camLS = [3]float64{dot64(v.basis[0], c), dot64(v.basis[1], c), dot64(v.basis[2], c)}
	zn := math.Floor((v.camLS[2]-vsmDepthRange/2)/vsmDepthSnap) * vsmDepthSnap
	if zn != v.zNear {
		v.zNear = zn
		v.pager.invalidateAll()
	}
	v.pager.setWindows(v.camLS[0], v.camLS[1])
}

func cross64(a, b [3]float64) [3]float64 {
	return [3]float64{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}

func dot64(a, b [3]float64) float64 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }

func norm64(a [3]float64) [3]float64 {
	n := math.Sqrt(dot64(a, a))
	if n == 0 {
		return [3]float64{1, 0, 0}
	}
	return [3]float64{a[0] / n, a[1] / n, a[2] / n}
}

// lightRect is a world box's footprint across the light: the light-space x
// and y its corners span.
func (v *vsmRenderer) lightRect(b vsmBox) (lo, hi [2]float64) {
	lo = [2]float64{math.Inf(1), math.Inf(1)}
	hi = [2]float64{math.Inf(-1), math.Inf(-1)}
	for i := 0; i < 8; i++ {
		p := [3]float64{float64(b.lo[0]), float64(b.lo[1]), float64(b.lo[2])}
		if i&1 != 0 {
			p[0] = float64(b.hi[0])
		}
		if i&2 != 0 {
			p[1] = float64(b.hi[1])
		}
		if i&4 != 0 {
			p[2] = float64(b.hi[2])
		}
		x, y := dot64(v.basis[0], p), dot64(v.basis[1], p)
		lo = [2]float64{min(lo[0], x), min(lo[1], y)}
		hi = [2]float64{max(hi[0], x), max(hi[1], y)}
	}
	return lo, hi
}

// appendSafety asks for vsmSafetyLevel's pages under the view out to the
// shadow distance: the view's slice corners across the light.
func (v *vsmRenderer) appendSafety(keys []vsmKey, view, projection mgl32.Mat4) []vsmKey {
	tanHalfV, aspect, near := perspectiveParams(projection)
	corners := frustumSliceCorners(view, tanHalfV, aspect, near, ShadowMaxDistance)
	lo := [2]float64{math.Inf(1), math.Inf(1)}
	hi := [2]float64{math.Inf(-1), math.Inf(-1)}
	for _, c := range corners {
		p := [3]float64{float64(c[0]), float64(c[1]), float64(c[2])}
		x, y := dot64(v.basis[0], p), dot64(v.basis[1], p)
		lo = [2]float64{min(lo[0], x), min(lo[1], y)}
		hi = [2]float64{max(hi[0], x), max(hi[1], y)}
	}
	// The slice's box across the light; its corners' hull would be tighter,
	// but this is a few dozen 32 m pages.
	l := vsmSafetyLevel
	w := vsmPageWidth(l)
	o := v.pager.origin[l]
	x0 := max(int32(math.Floor(lo[0]/w)), o[0])
	y0 := max(int32(math.Floor(lo[1]/w)), o[1])
	x1 := min(int32(math.Floor(hi[0]/w)), o[0]+vsmWindowPages-1)
	y1 := min(int32(math.Floor(hi[1]/w)), o[1]+vsmWindowPages-1)
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			keys = append(keys, vsmKey{int8(l), x, y})
		}
	}
	return keys
}

// pollMarks takes the newest readback that has landed, if any. Fences pass
// in the order they were set, so the slots are asked oldest first and the
// asking stops at the first still in flight — a fence's query is a driver
// call, and on NVIDIA's not a cheap one.
func (v *vsmRenderer) pollMarks() {
	best := -1
	for {
		oldest := -1
		for i := range v.slots {
			s := &v.slots[i]
			if s.rb.pending && i != best && (best < 0 || s.rb.frame > v.slots[best].rb.frame) &&
				(oldest < 0 || s.rb.frame < v.slots[oldest].rb.frame) {
				oldest = i
			}
		}
		if oldest < 0 || !v.slots[oldest].rb.ready() {
			break
		}
		if best >= 0 {
			// Superseded by a newer one that has landed too.
			v.slots[best].rb.done()
			v.slots[best].rb.pending = false
		}
		best = oldest
	}
	if best < 0 {
		return
	}
	s := &v.slots[best]
	words := s.rb.read()
	v.requests = v.requests[:0]
	for l := 0; l < vsmLevels; l++ {
		for y := 0; y < vsmWindowPages; y++ {
			w := words[l*vsmWindowPages+y]
			for w != 0 {
				x := bitsTrailing(w)
				w &= w - 1
				v.requests = append(v.requests, vsmKey{int8(l), s.origin[l][0] + int32(x), s.origin[l][1] + int32(y)})
			}
		}
	}
	s.rb.done()
	s.rb.pending = false
}

func bitsTrailing(w uint32) int {
	n := 0
	for w&1 == 0 {
		w >>= 1
		n++
	}
	return n
}

// drawPages draws the pages, a rectangle at a time, and copies them out:
// whole ones from nothing, movers ones over their static copies.
func (v *vsmRenderer) drawPages(level *world.Level, whole, movers []*vsmPage, dl world.DirectionalLight, frame *ShadowFrame) {
	prevFBO := boundFramebuffer()
	prevViewport := boundViewport()
	gl.BindFramebuffer(gl.FRAMEBUFFER, v.scratchFBO)
	gl.UseProgram(Shadows.Program)
	gl.Disable(gl.CULL_FACE)
	gl.Enable(gl.POLYGON_OFFSET_FILL)
	gl.PolygonOffset(1.1, 4.0)
	gl.Enable(gl.DEPTH_CLAMP)
	gl.Enable(gl.DEPTH_TEST)
	gl.DepthMask(true)
	gl.Enable(gl.SCISSOR_TEST)
	copyOut := func(r *vsmRect, dst uint32) {
		for _, pg := range r.pages {
			gl.CopyImageSubData(v.scratch, gl.TEXTURE_2D, 0, (pg.key.x-r.x0)*vsmPageTexels, (pg.key.y-r.y0)*vsmPageTexels, 0,
				dst, gl.TEXTURE_2D_ARRAY, 0, 0, 0, pg.layer, vsmPageTexels, vsmPageTexels, 1)
		}
	}
	begin := func(r *vsmRect) (mgl32.Mat4, Frustum) {
		wpx, hpx := (r.x1-r.x0)*vsmPageTexels, (r.y1-r.y0)*vsmPageTexels
		gl.Viewport(0, 0, wpx, hpx)
		gl.Scissor(0, 0, wpx, hpx)
		gl.Clear(gl.DEPTH_BUFFER_BIT)
		shadowLODTexel = float32(vsmTexel(r.level))
		return v.rectMatrices(r)
	}
	defer func() { shadowLODTexel = 0 }()
	for _, r := range v.pager.rects(whole) {
		lsm, cull := begin(&r)
		v.drawStatic(level, lsm, &cull)
		copyOut(&r, v.static)
		v.drawMovers(level, &r, lsm, &cull, dl, frame)
		copyOut(&r, v.atlas)
		v.pager.markDrawn(r.pages)
		v.rects++
	}
	for _, r := range v.pager.rects(movers) {
		lsm, cull := begin(&r)
		// Each page's static layer into its place in the rectangle; what
		// lies between them is not copied out.
		for _, pg := range r.pages {
			gl.CopyImageSubData(v.static, gl.TEXTURE_2D_ARRAY, 0, 0, 0, pg.layer,
				v.scratch, gl.TEXTURE_2D, 0, (pg.key.x-r.x0)*vsmPageTexels, (pg.key.y-r.y0)*vsmPageTexels, 0,
				vsmPageTexels, vsmPageTexels, 1)
		}
		v.drawMovers(level, &r, lsm, &cull, dl, frame)
		copyOut(&r, v.atlas)
		v.pager.markDrawn(r.pages)
		v.rects++
	}
	gl.Disable(gl.SCISSOR_TEST)
	gl.Disable(gl.DEPTH_CLAMP)
	gl.Disable(gl.POLYGON_OFFSET_FILL)
	if v.finish {
		// See NaniteVSMFinish.
		gl.Finish()
	}
	gl.BindFramebuffer(gl.FRAMEBUFFER, prevFBO)
	gl.Viewport(prevViewport[0], prevViewport[1], prevViewport[2], prevViewport[3])
	gl.UseProgram(gfx.Program)
}

// drawStatic walks the static layer's casters into the bound rectangle: the
// compiled surfaces, the chunks and their terrain, the props classic and
// cooked — renderSceneShadowLayers' static half and its scenery.
func (v *vsmRenderer) drawStatic(level *world.Level, lsm mgl32.Mat4, cull *Frustum) {
	gl.UseProgram(Shadows.Program)
	gl.UniformMatrix4fv(Shadows.LSMUniform, 1, false, &lsm[0])
	renderSceneShadowLayers(level, Shadows.Program, &gfx.ShadowSkinUniforms, Shadows.ModelUniform, cull, shadowStaticLayer)
	ProfileBegin("scenery")
	RenderSceneryShadow(level, Shadows.Program, &gfx.ShadowSkinUniforms, Shadows.ModelUniform, cull)
	ProfileEnd()
	ProfileBegin("nanite: scenery")
	renderNaniteSceneryShadow(level, Shadows.Program, cull)
	ProfileEnd()
}

// drawMovers walks what moves into the bound rectangle r, over whatever
// depth is there: entities, sprites, scatter and foliage. The entities are
// the tracker's list of them for the frame (RenderEntitiesShadow's walk,
// worked out once a frame), each drawn where its footprint across the light
// touches r; the other three are walked only if theirs does.
func (v *vsmRenderer) drawMovers(level *world.Level, r *vsmRect, lsm mgl32.Mat4, cull *Frustum, dl world.DirectionalLight, frame *ShadowFrame) {
	w := vsmPageWidth(r.level)
	rect := [2][2]float64{{float64(r.x0) * w, float64(r.y0) * w}, {float64(r.x1) * w, float64(r.y1) * w}}
	touches := func(lo, hi [2]float64) bool {
		return lo[0] <= rect[1][0] && hi[0] >= rect[0][0] && lo[1] <= rect[1][1] && hi[1] >= rect[0][1]
	}
	gl.UseProgram(Shadows.Program)
	gl.UniformMatrix4fv(Shadows.LSMUniform, 1, false, &lsm[0])
	ProfileBegin("entities")
	drew := false
	for i := range v.tracker.draws {
		ed := &v.tracker.draws[i]
		if !touches(ed.lo, ed.hi) {
			continue
		}
		for mi := range ed.models {
			md := &ed.models[mi]
			gl.UniformMatrix4fv(Shadows.ModelUniform, 1, false, &md.m[0])
			drew = true
			for si := range md.meshes {
				drawGeometryShadow(&md.meshes[si], Shadows.Program, &gfx.ShadowSkinUniforms)
			}
		}
	}
	identity := mgl32.Ident4()
	gl.UniformMatrix4fv(Shadows.ModelUniform, 1, false, &identity[0])
	if drew {
		gfx.ApplySkinUniforms(&gfx.ShadowSkinUniforms, Shadows.Program, nil)
	}
	ProfileEnd()
	c, split := v.cascadeFor(r.level, frame)
	if touches(v.spriteRect[0], v.spriteRect[1]) {
		ProfileBegin("sprites")
		RenderSpriteDirShadow(level, lsm, dl.Direction, gfx.Cam.Position, cull)
		ProfileEnd()
	}
	if touches(v.scatterRect[0], v.scatterRect[1]) {
		ProfileBegin("scatter")
		RenderScatterDirShadow(level, lsm, c, split, gfx.Cam.Position, BiomeWindTime, cull)
		ProfileEnd()
	}
	if touches(v.folRect[0], v.folRect[1]) {
		ProfileBegin("foliage")
		RenderFoliageDirShadow(level, lsm, c, cull)
		ProfileEnd()
	}
}

// footprint is box b's footprint across the light, or an empty one (lo past
// hi) when there is none (has false).
func (v *vsmRenderer) footprint(b vsmBox, has bool) [2][2]float64 {
	if !has {
		return [2][2]float64{{math.Inf(1), math.Inf(1)}, {math.Inf(-1), math.Inf(-1)}}
	}
	lo, hi := v.lightRect(b)
	return [2][2]float64{lo, hi}
}

// rectMatrices is rectangle r's light-space matrix — its pages' extent across
// the light onto the viewport, the map's depth window onto [0, 1] — and the
// frustum its casters are gathered by, reaching vsmCullReach toward the
// light.
func (v *vsmRenderer) rectMatrices(r *vsmRect) (mgl32.Mat4, Frustum) {
	w := vsmPageWidth(r.level)
	x0, x1 := float64(r.x0)*w, float64(r.x1)*w
	y0, y1 := float64(r.y0)*w, float64(r.y1)*w
	build := func(zLo, zHi float64) mgl32.Mat4 {
		var m mgl32.Mat4
		row := func(i int, axis [3]float64, lo, hi float64) {
			s := 2 / (hi - lo)
			t := -(hi + lo) / (hi - lo)
			// Column-major: m[col*4 + row].
			m[0*4+i] = float32(s * axis[0])
			m[1*4+i] = float32(s * axis[1])
			m[2*4+i] = float32(s * axis[2])
			m[3*4+i] = float32(t)
		}
		row(0, v.basis[0], x0, x1)
		row(1, v.basis[1], y0, y1)
		row(2, v.basis[2], zLo, zHi)
		m[3*4+3] = 1
		return m
	}
	lsm := build(v.zNear, v.zNear+vsmDepthRange)
	cull := ExtractFrustum(build(v.zNear-vsmCullReach, v.zNear+vsmDepthRange))
	return lsm, cull
}

// cascadeFor is the cascade whose slice the distances level l is read at
// fall in — what the scatter and foliage casting ranges are keyed on.
func (v *vsmRenderer) cascadeFor(l int, frame *ShadowFrame) (int, float32) {
	d := float32(math.Exp2(float64(l) - float64(v.frame.levelBias)))
	for c := 0; c < ShadowCascades; c++ {
		if d <= frame.Splits[c] {
			return c, frame.Splits[c]
		}
	}
	return ShadowCascades - 1, frame.Splits[ShadowCascades-1]
}

// vsmMarkReceivers asks for the pages the frame's opaque pixels read: after
// the opaque world and before anything translucent, in a frame vsmBegin drew.
func vsmMarkReceivers() {
	v := &vsm
	if !v.active || !sceneFrameActive() || scene.depth == 0 {
		return
	}
	ProfileBegin("vsm: mark")
	defer ProfileEnd()
	p := &v.mark
	gl.UseProgram(p.prog)
	projView := gfx.Projection.Mul4(gfx.Cam.Matrix)
	inv := projView.Inv()
	gl.UniformMatrix4fv(p.U("invProjView"), 1, false, &inv[0])
	gl.Uniform2i(p.U("size"), scene.w, scene.h)
	gl.Uniform1i(p.U("sceneDepth"), 0)
	uploadVSMUniforms(p.prog, &v.frame, func(name string) int32 { return p.U(name) })
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, scene.depth)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 0, v.req)
	gl.DispatchCompute(uint32((scene.w+15)/16), uint32((scene.h+15)/16), 1)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 0, 0)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	// Into the ring, without waiting; a slot still in flight is skipped.
	s := &v.slots[v.next]
	if !s.rb.pending {
		v.next = (v.next + 1) % vsmReadbacks
		s.origin = v.pager.origin
		s.rb.copyFrom(v.req, vsmReqWords*4, uint32(v.pager.frame))
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, v.req)
	zero := uint32(0)
	gl.ClearBufferData(gl.SHADER_STORAGE_BUFFER, gl.R32UI, gl.RED_INTEGER, gl.UNSIGNED_INT, gl.Ptr(&zero))
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	gl.UseProgram(gfx.Program)
}

// uploadVSMTo sends the frame's virtual map to prog (bound), or says there is
// none: every program with vsm.glsl in it is told one way or the other, and
// its two samplers always point at textures of their own types.
func uploadVSMTo(prog uint32, frame ShadowFrame) {
	loc := func(name string) int32 { return gfx.UniformLocName(prog, name) }
	if loc("vsmLightSlot") < 0 {
		return
	}
	// The uploads before this one (the clouds', GI's) may have bound
	// another program.
	gl.UseProgram(prog)
	v := &vsm
	v.ensureDummies()
	atlas, table := v.dummyAtlas, v.dummyTable
	if frame.VSM != nil && v.ok {
		atlas, table = v.atlas, v.table
	}
	gl.ActiveTexture(gl.TEXTURE0 + VSMAtlasUnit)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, atlas)
	gl.ActiveTexture(gl.TEXTURE0 + VSMTableUnit)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, table)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.Uniform1i(loc("vsmAtlas"), VSMAtlasUnit)
	gl.Uniform1i(loc("vsmPageTable"), VSMTableUnit)
	if frame.VSM == nil {
		gl.Uniform1i(loc("vsmLightSlot"), 0)
		return
	}
	uploadVSMUniforms(prog, frame.VSM, loc)
}

// uploadVSMUniforms sends f's values through loc.
func uploadVSMUniforms(prog uint32, f *VSMFrame, loc func(string) int32) {
	gl.Uniform1i(loc("vsmLightSlot"), int32(f.Light)+1)
	gl.UniformMatrix3fv(loc("vsmRot"), 1, false, &f.rot[0])
	gl.Uniform3f(loc("vsmCamPos"), f.camPos[0], f.camPos[1], f.camPos[2])
	gl.Uniform2fv(loc("vsmCamTexel"), vsmLevels, &f.camTexel[0][0])
	gl.Uniform1f(loc("vsmTexel0"), float32(vsmTexel(0)))
	gl.Uniform1f(loc("vsmLevelBias"), f.levelBias)
	gl.Uniform1f(loc("vsmDepthOffset"), f.depthOffset)
	gl.Uniform1f(loc("vsmDepthScale"), f.depthScale)
	gl.Uniform1f(loc("vsmBias"), vsmBiasTexels)
	gl.Uniform1f(loc("vsmNormalOffset"), vsmNormalTexels)
}

// ensureDummies makes the 1x1 stand-ins.
func (v *vsmRenderer) ensureDummies() {
	if v.dummyAtlas != 0 {
		return
	}
	gl.GenTextures(1, &v.dummyAtlas)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, v.dummyAtlas)
	gl.TexImage3D(gl.TEXTURE_2D_ARRAY, 0, gl.DEPTH_COMPONENT32F, 1, 1, 1, 0, gl.DEPTH_COMPONENT, gl.FLOAT, gl.Ptr([]float32{1}))
	vsmShadowSampling(gl.TEXTURE_2D_ARRAY)
	gl.GenTextures(1, &v.dummyTable)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, v.dummyTable)
	gl.TexImage3D(gl.TEXTURE_2D_ARRAY, 0, gl.R32UI, 1, 1, 1, 0, gl.RED_INTEGER, gl.UNSIGNED_INT, gl.Ptr([]uint32{0}))
	setIntegerNearest(gl.TEXTURE_2D_ARRAY)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, 0)
}

// vsmShadowPass draws the frame's virtual shadow map, if it has one, and
// marks its light in frame as casting through it: called by RenderShadowPass
// before the cascades, which skip the light it returns (-1: none).
func vsmShadowPass(lights world.LightSet, level *world.Level, view, projection mgl32.Mat4, frame *ShadowFrame) int {
	DebugStats.VSMOn = false
	li := vsmLightFor(lights, level)
	if li < 0 || !vsmBegin(lights, level, li, view, projection, frame) {
		vsm.active = false
		vsm.lapsed = true
		return -1
	}
	DebugStats.VSMOn = true
	// Casting, as far as the uniforms say (dirLightCastShadows); its
	// cascades' layer is never read — calcDirShadowUnfaded answers from the
	// map first.
	frame.Lights[li].BaseLayer = 0
	if s := lights.BakedSun(); level.Static != nil && s == li {
		frame.BakedSunCast, frame.BakedSun = true, s
	}
	return li
}
