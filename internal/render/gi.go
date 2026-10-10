package render

// gi.go — live global illumination: bounce light worked out every frame from
// the lights as they are, in place of the flat ambient term and the bake's
// own bounce.
//
// THE SHAPE. Two halves, both on the GPU, both around the camera:
//
//  1. A distance field of the level (gi_sdf.go): a clipmap of signed distance
//     and surface colour, built brick by brick from the brushes, terrain and
//     models (gi_sources.go), and rebuilt only where something changed.
//  2. A grid of irradiance probes (this file): a few nested cascades of
//     giDimX × giDimY × giDimZ probes, coarser further out. Each frame a
//     probe sends rays through the field (gi_probe_trace_comp.glsl); a ray
//     that hits picks up the surface's colour times the light reaching it —
//     the scene's lights, each shadowed by its own ray through the field, and
//     last frame's probes, which is how the bounces accumulate — and one that
//     misses sees the sky. The results fold into the probe's octahedral
//     irradiance and visibility tiles, blended with what they held.
//
// Every pass that lights a surface reads the probes through ambientLight in
// light_model.glsl, which is where the ambient term used to come from. On a
// compiled level's baked surfaces the bake's own indirect part
// (compile.Face.Indirect) is taken back off first, so the bake keeps the
// static lights' direct light and GI supplies the rest.
//
// WHEN. gfx.GIQuality is the player's tier, Off by default on an integrated
// GPU; GlobalLight.GI.Disabled is a level saying no. Either way off means the
// world programs see giEnabled 0 — exactly the lighting there was before GI.
//
// COST. Everything a frame does is bounded: a brick budget for the field, a
// rotating subset of the outer cascades' probes, a fixed ray count. The F3
// profiler shows it as "gi: field" and "gi: probes".

import (
	"fmt"
	"log"
	"math"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// Probes per cascade on each axis. Flatter than wide: a level spreads out
// further than it stacks up.
const (
	giDimX = 24
	giDimY = 12
	giDimZ = 24
)

// Tile sizes in the atlases, border included — light_model.glsl's GI_IRR_TILE
// and GI_VIS_TILE.
const (
	giIrrTile = 8
	giVisTile = 16
)

// GIMaxCascades is light_model.glsl's GI_MAX_CASCADES.
const GIMaxCascades = 3

// The texture units GI's atlases sit on in every world program. Past
// everything else's (see gfx.BlendTexUnit's note and foliage's 18/19).
const (
	GIIrradianceUnit = 20
	GIVisibilityUnit = 21
	GIProbeDataUnit  = 22
	// GIProbeSurfaceUnit holds each probe's nearest surface, where it has one
	// close by (gi_probe_update_comp.glsl).
	GIProbeSurfaceUnit = 23
	// GIProbePackedUnit holds the last two packed into one texel, which is
	// what the shading reads (light_model.glsl's giProbePacked). Past
	// nfoliage's units, the highest anything else takes.
	GIProbePackedUnit = 44
	// giSdfUnitBase is where the GI compute programs find the field's eight
	// textures: units 24..31.
	giSdfUnitBase = 24
)

// giWarmFrames is how many probe updates a level gets before its first frame,
// at a low hysteresis, so it does not open in the flat ambient and brighten.
const giWarmFrames = 12

// giTier is what one quality setting buys.
//
// The probe trace is nearly all of GI's cost, and it is rays a frame: probes
// updated × rays each. period spreads a cascade's probes over that many
// frames (the hysteresis averages the result anyway), and resting probes —
// in open air, read by nothing on a surface — wait restMul times longer. The
// numbers were set after Medium at one cascade a frame and 128 rays measured
// most of a GTX 1070's frame in an open canyon.
type giTier struct {
	cascades   int
	rays       int
	sdfLevels  int
	budget     int     // bricks rebuilt a frame
	spacing    float32 // multiplies the level's probe spacing
	period     [GIMaxCascades]int
	restMul    int
	hysteresis float32
}

var giTiers = map[gfx.GIQualityLevel]giTier{
	gfx.GILow: {cascades: 2, rays: 32, sdfLevels: 3, budget: 256, spacing: 1,
		period: [GIMaxCascades]int{4, 8, 8}, restMul: 8, hysteresis: 0.92},
	gfx.GIMedium: {cascades: 3, rays: 64, sdfLevels: 4, budget: 512, spacing: 1,
		period: [GIMaxCascades]int{2, 4, 8}, restMul: 8, hysteresis: 0.94},
	gfx.GIHigh: {cascades: 3, rays: 128, sdfLevels: 4, budget: 1024, spacing: 0.5,
		period: [GIMaxCascades]int{1, 2, 4}, restMul: 4, hysteresis: 0.96},
}

// GIDebugMode is what `gi_debug` shows.
type GIDebugMode int

const (
	GIDebugOff GIDebugMode = iota
	// GIDebugLighting draws every surface white, so only the light is left.
	GIDebugLighting
	// GIDebugProbes draws each probe as a dot of its own light.
	GIDebugProbes
	// GIDebugField draws the distance field as the camera would see it.
	GIDebugField
	giDebugModeCount
)

var giDebugNames = [giDebugModeCount]string{"off", "lighting", "probes", "field"}

func (m GIDebugMode) String() string {
	if m < 0 || m >= giDebugModeCount {
		return "off"
	}
	return giDebugNames[m]
}

// ParseGIDebugMode reads a mode's name.
func ParseGIDebugMode(s string) (GIDebugMode, bool) {
	for i, n := range giDebugNames {
		if n == s {
			return GIDebugMode(i), true
		}
	}
	return GIDebugOff, false
}

// GIDebug is the current debug view.
var GIDebug GIDebugMode

// GIStats is what the overlay shows of GI's last frame.
type GIStats struct {
	Active        bool
	Tier          string
	Sources       int
	BricksBuilt   int
	BricksPending int
	ProbesTraced  int
}

// GIStatsLast is GIStats for the frame just drawn.
var GIStatsLast GIStats

type giRenderer struct {
	failed bool
	tier   gfx.GIQualityLevel
	t      giTier

	update, trace, sdfProg, debugProg progUniforms

	irr, vis, probes uint32
	// surf is each probe's nearest surface as a plane — the normal facing
	// the probe and the distance to it — where one is within half a
	// spacing, zero where none is. The shading ignores a probe for any point
	// on the far side of it: see giSampleCascade.
	surf uint32
	// packed is probes and surf in one RGBA32UI texel, half floats two to a
	// word: one fetch a probe in the shading instead of two.
	packed uint32
	dummy  uint32
	// probeList is the probes the trace takes this frame, listed by the
	// placement pass a cascade at a time (giProbesPerCascade words each), and
	// probeDispatch the trace's indirect dispatch for each cascade, its
	// count of them: see updateProbes.
	probeList, probeDispatch uint32

	field   *giField
	sources *giSourceSet

	lvl     *world.Level
	lvlName string
	spacing float32
	min     [GIMaxCascades][3]int32
	prevMin [GIMaxCascades][3]int32
	frame   int
	reset   bool
	warm    int
	active  bool
	cam     mgl32.Vec3
}

var gi giRenderer

// giShaderSource reads a GI shader with the shared files it needs spliced
// after the #version line.
func giShaderSource(file string, defines string, shared ...string) string {
	src := "#version 430\n" + defines
	for _, s := range shared {
		src += mustReadShader(fileutil.ShaderPath(s)) + "\n"
	}
	return src + mustReadShader(fileutil.ShaderPath(file))
}

func giComputeProgram(src string) (progUniforms, error) {
	cs, err := gfx.CompileShader(src, gl.COMPUTE_SHADER)
	if err != nil {
		return progUniforms{}, err
	}
	prog, err := linkStages(cs)
	if err != nil {
		return progUniforms{}, err
	}
	return progUniforms{prog: prog, loc: map[string]int32{}}, nil
}

// ensure builds (or rebuilds, on a tier change) everything the tier needs.
func (g *giRenderer) ensure(q gfx.GIQualityLevel) bool {
	if g.failed {
		return false
	}
	if g.field != nil && g.tier == q {
		return true
	}
	g.release()
	t, ok := giTiers[q]
	if !ok {
		return false
	}
	if err := g.build(t); err != nil {
		log.Printf("gi: %v — GI is off", err)
		g.release()
		g.failed = true
		return false
	}
	g.tier, g.t = q, t
	return true
}

func (g *giRenderer) build(t giTier) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	shared := []string{"gi_sdf_common.glsl", "light_model.glsl"}
	perCascade := fmt.Sprintf("#define GI_PROBES_PER_CASCADE %d\n", giProbesPerCascade)
	if g.sdfProg, err = giComputeProgram(giShaderSource("gi_sdf_comp.glsl", "")); err != nil {
		return fmt.Errorf("field shader: %w", err)
	}
	if g.update, err = giComputeProgram(giShaderSource("gi_probe_update_comp.glsl", perCascade, shared...)); err != nil {
		return fmt.Errorf("probe update shader: %w", err)
	}
	if g.trace, err = giComputeProgram(giShaderSource("gi_probe_trace_comp.glsl",
		fmt.Sprintf("#define GI_RAYS %d\n", t.rays)+perCascade, shared...)); err != nil {
		return fmt.Errorf("probe trace shader: %w", err)
	}
	if g.debugProg, err = giComputeProgram(giShaderSource("gi_debug_comp.glsl", "", "gi_sdf_common.glsl")); err != nil {
		return fmt.Errorf("debug shader: %w", err)
	}

	tilesX, tilesY := giDimX*giDimZ, giDimY*GIMaxCascades
	g.irr = newGITexture2D(gl.R11F_G11F_B10F, tilesX*giIrrTile, tilesY*giIrrTile, gl.LINEAR)
	g.vis = newGITexture2D(gl.RG16F, tilesX*giVisTile, tilesY*giVisTile, gl.LINEAR)
	g.probes = newGITexture2D(gl.RGBA16F, tilesX, tilesY, gl.NEAREST)
	g.surf = newGITexture2D(gl.RGBA16F, tilesX, tilesY, gl.NEAREST)
	g.packed = newGITexture2D(gl.RGBA32UI, tilesX, tilesY, gl.NEAREST)
	gl.GenBuffers(1, &g.probeList)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, g.probeList)
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, GIMaxCascades*giProbesPerCascade*4, nil, gl.DYNAMIC_COPY)
	gl.GenBuffers(1, &g.probeDispatch)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, g.probeDispatch)
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, len(giProbeDispatchReset)*4, gl.Ptr(&giProbeDispatchReset[0]), gl.DYNAMIC_COPY)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)

	g.field = newGIField(t.sdfLevels, 0, g.sdfProg) // voxel size set per level in start
	g.sources = newGISourceSet()
	g.lvl = nil
	return nil
}

func newGITexture2D(internal int32, w, h int, filter int32) uint32 {
	var tex uint32
	gl.GenTextures(1, &tex)
	gl.BindTexture(gl.TEXTURE_2D, tex)
	gl.TexStorage2D(gl.TEXTURE_2D, 1, uint32(internal), int32(w), int32(h))
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, filter)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, filter)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	return tex
}

func (g *giRenderer) release() {
	if g.field != nil {
		g.field.release()
		g.field = nil
	}
	for _, t := range []*uint32{&g.irr, &g.vis, &g.probes, &g.surf, &g.packed} {
		if *t != 0 {
			gl.DeleteTextures(1, t)
			*t = 0
		}
	}
	for _, b := range []*uint32{&g.probeList, &g.probeDispatch} {
		if *b != 0 {
			gl.DeleteBuffers(1, b)
			*b = 0
		}
	}
	for _, p := range []*progUniforms{&g.update, &g.trace, &g.sdfProg, &g.debugProg} {
		if p.prog != 0 {
			gl.DeleteProgram(p.prog)
			*p = progUniforms{}
		}
	}
	g.sources = nil
	g.lvl = nil
	g.active = false
}

// giWanted is whether GI should run for lvl this frame.
func giWanted(lvl *world.Level, o WorldDraw) bool {
	return lvl != nil && gfx.GIQuality != gfx.GIOff && !lvl.Lights.Global.GI.Disabled && !o.Wireframe
}

// UpdateGI brings the field and the probes up to date for this frame. Called
// from DrawWorld after the shadow passes and before any program's lighting
// uniforms go up — uploadGITo, which those uploads call, reads what it sets.
func UpdateGI(lvl *world.Level, o WorldDraw) {
	g := &gi
	g.active = false
	GIStatsLast = GIStats{}
	if !giWanted(lvl, o) || !g.ensure(gfx.GIQuality) {
		return
	}
	g.cam = gfx.Cam.Position
	settings := lvl.Lights.Global.GI
	spacing := max(settings.ProbeSpacing, world.GIProbeSpacingMin) * g.t.spacing
	switch {
	case g.lvl == nil || lvl.Name != g.lvlName || spacing != g.spacing:
		g.start(lvl, spacing)
	case lvl != g.lvl:
		// The same level in a new struct — an editor undo, a respawn's
		// reload. What it holds is mostly what it held, so GI carries on and
		// the sources' diff finds what differs, rather than every undo
		// starting the field and the probes over. Its chunks are re-derived
		// from scratch, though: a chunk's stamp says its content is unchanged
		// only for as long as it is the same chunk, and the diff below is
		// what then decides what really moved.
		g.lvl = lvl
		g.sources.forgetStamps()
	}

	// The field: what changed, then the bricks that need it.
	ProfileBegin("gi: field")
	// Started on a goroutine at the top of the frame where it could be
	// (frame_prep.go); giCollect sets the smallest model traced.
	giCollect(lvl)
	if g.warm == 0 && g.sources.churn > max(64, g.sources.count/2) {
		// Most of the level changed at once: as good as a new one, so the
		// probes start over rather than fading from the old one's light.
		g.reset = true
		g.warm = giWarmFrames
	}
	if g.sources.changed {
		g.field.uploadSources(g.sources.flat, g.sources.nStatic, g.sources.staticChanged)
		g.sources.changed, g.sources.staticChanged = false, false
	}
	for _, d := range g.sources.dirty {
		g.field.invalidate(d[0], d[1])
	}
	g.field.recentre(g.cam)
	budget := g.t.budget
	if g.warm > 0 {
		budget = -1 // the whole field before the level is first seen
	}
	built := g.field.build(g.cam, budget)
	ProfileEnd()

	// The probes.
	ProfileBegin("gi: probes")
	g.placeCascades()
	runs := 1
	if g.warm > 0 {
		runs = g.warm
		g.warm = 0
	}
	traced := 0
	for r := 0; r < runs; r++ {
		h := g.t.hysteresis
		if runs > 1 {
			h = 0.6
		}
		traced += g.updateProbes(lvl, h, runs > 1)
		g.frame++
	}
	g.prevMin = g.min
	g.reset = false
	ProfileEnd()

	g.active = true
	GIStatsLast = GIStats{
		Active: true, Tier: g.tier.String(), Sources: len(g.sources.flat),
		BricksBuilt: built, BricksPending: g.field.pending, ProbesTraced: traced,
	}
}

// start points GI at a new level (or a new spacing): every brick and every
// probe starts over, and the next update warms them up.
func (g *giRenderer) start(lvl *world.Level, spacing float32) {
	g.lvl, g.lvlName, g.spacing = lvl, lvl.Name, spacing
	g.sources = newGISourceSet()
	voxel0 := spacing / 8
	for i := range g.field.levels {
		g.field.levels[i].voxel = voxel0 * float32(int(1)<<i)
	}
	g.field.meshSig, g.field.blockSig = nil, nil
	g.field.invalidateAll()
	g.reset = true
	g.warm = giWarmFrames
	g.placeCascades()
	g.prevMin = g.min
}

// cascadeSpacing is cascade c's probe spacing.
func (g *giRenderer) cascadeSpacing(c int) float32 { return g.spacing * float32(int(1)<<c) }

// placeCascades centres each cascade's grid on the camera, snapped to its
// spacing so a probe stays where it is while the camera moves within a cell.
func (g *giRenderer) placeCascades() {
	dims := [3]int32{giDimX, giDimY, giDimZ}
	for c := 0; c < g.t.cascades; c++ {
		s := g.cascadeSpacing(c)
		for k := 0; k < 3; k++ {
			g.min[c][k] = int32(math.Floor(float64(g.cam[k]/s))) - dims[k]/2
		}
	}
}

// giProbesPerCascade is the probes in one cascade, and the room each
// cascade has in the trace's list.
const giProbesPerCascade = giDimX * giDimY * giDimZ

// giProbeDispatchReset is the trace's indirect dispatches before the
// placement pass counts into them: no probes, one by one.
var giProbeDispatchReset = [GIMaxCascades * 3]uint32{0, 1, 1, 0, 1, 1, 0, 1, 1}

// updateProbes runs the placement and the trace for every cascade, and
// returns how many probes it traced.
//
// The placement pass lists the probes the trace takes (a live probe one frame
// in its cascade's period, a resting one in period × restMul), and the trace
// is dispatched indirectly over that list: a workgroup a probe it traces,
// where it used to be one for every probe of every cascade — 6912 a cascade,
// most of them only to read the probe's state and stop.
func (g *giRenderer) updateProbes(lvl *world.Level, hysteresis float32, warm bool) int {
	periods := func(c int) (int, int) {
		if warm {
			return 1, 1
		}
		return g.t.period[c], g.t.period[c] * g.t.restMul
	}

	// Placement, and the trace's list.
	u := &g.update
	g.uploadProbeUniformsTo(u)
	g.field.uploadSdfUniformsTo(u, giSdfUnitBase)
	gl.UseProgram(u.prog)
	gl.BindImageTexture(0, g.probes, 0, false, 0, gl.READ_WRITE, gl.RGBA16F)
	gl.BindImageTexture(1, g.surf, 0, false, 0, gl.WRITE_ONLY, gl.RGBA16F)
	gl.BindImageTexture(2, g.packed, 0, false, 0, gl.WRITE_ONLY, gl.RGBA32UI)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, g.probeDispatch)
	gl.BufferSubData(gl.SHADER_STORAGE_BUFFER, 0, len(giProbeDispatchReset)*4, gl.Ptr(&giProbeDispatchReset[0]))
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 0, g.probeList)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 1, g.probeDispatch)
	reset := int32(0)
	if g.reset {
		reset = 1
	}
	gl.Uniform1i(u.U("resetAll"), reset)
	gl.Uniform1i(u.U("frameIndex"), int32(g.frame))
	total := giProbesPerCascade
	for c := 0; c < g.t.cascades; c++ {
		period, rest := periods(c)
		gl.Uniform1i(u.U("cascade"), int32(c))
		gl.Uniform1i(u.U("period"), int32(period))
		gl.Uniform1i(u.U("restPeriod"), int32(rest))
		pm := g.prevMin[c]
		gl.Uniform3i(u.U("prevMin"), pm[0], pm[1], pm[2])
		gl.DispatchCompute(uint32((total+63)/64), 1, 1)
	}
	gl.MemoryBarrier(gl.SHADER_IMAGE_ACCESS_BARRIER_BIT | gl.TEXTURE_FETCH_BARRIER_BIT |
		gl.SHADER_STORAGE_BARRIER_BIT | gl.COMMAND_BARRIER_BIT)
	g.reset = false

	// Trace.
	t := &g.trace
	g.uploadProbeUniformsTo(t)
	g.field.uploadSdfUniformsTo(t, giSdfUnitBase)
	world.UploadLightsTo(t.prog, world.SceneLights(lvl), false)
	g.uploadTraceLights(t, world.SceneLights(lvl))
	gl.UseProgram(t.prog)
	gl.BindImageTexture(0, g.irr, 0, false, 0, gl.READ_WRITE, gl.R11F_G11F_B10F)
	gl.BindImageTexture(1, g.vis, 0, false, 0, gl.READ_WRITE, gl.RG16F)
	gl.BindImageTexture(2, g.probes, 0, false, 0, gl.READ_WRITE, gl.RGBA16F)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 0, g.probeList)
	gl.BindBuffer(gl.DISPATCH_INDIRECT_BUFFER, g.probeDispatch)
	gl.Uniform1i(t.U("frameIndex"), int32(g.frame))
	gl.Uniform1f(t.U("hysteresis"), hysteresis)
	rot := giRayRotation(g.frame)
	gl.UniformMatrix3fv(t.U("rayRotation"), 1, false, &rot[0])
	traced := 0
	for c := 0; c < g.t.cascades; c++ {
		period, _ := periods(c)
		gl.Uniform1i(t.U("cascade"), int32(c))
		gl.Uniform1f(t.U("maxDistance"), g.cascadeSpacing(c)*16)
		gl.DispatchComputeIndirect(c * 12)
		traced += total / period
	}
	gl.BindBuffer(gl.DISPATCH_INDIRECT_BUFFER, 0)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 0, 0)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 1, 0)
	gl.MemoryBarrier(gl.SHADER_IMAGE_ACCESS_BARRIER_BIT | gl.TEXTURE_FETCH_BARRIER_BIT)
	return traced
}

// giRayRotation is frame f's turn of the ray set: a fresh direction every
// frame, so over a few frames the probe sees between its rays.
func giRayRotation(f int) mgl32.Mat3 {
	h := uint32(f)*2654435761 + 0x9e3779b9
	next := func() float32 {
		h ^= h << 13
		h ^= h >> 17
		h ^= h << 5
		return float32(h) / float32(math.MaxUint32)
	}
	u1, u2, u3 := next(), next(), next()
	// A uniformly random rotation (Shoemake's quaternion).
	s1, s2 := float32(math.Sqrt(float64(1-u1))), float32(math.Sqrt(float64(u1)))
	a, b := 2*math.Pi*float64(u2), 2*math.Pi*float64(u3)
	q := mgl32.Quat{W: s2 * float32(math.Cos(b)), V: mgl32.Vec3{
		s1 * float32(math.Sin(a)), s1 * float32(math.Cos(a)), s2 * float32(math.Sin(b))}}
	return q.Normalize().Mat4().Mat3()
}

// uploadProbeUniformsTo sends the probe layout light_model.glsl reads — to a
// GI compute program, or (through uploadGITo) a world program.
func (g *giRenderer) uploadProbeUniformsTo(p *progUniforms) {
	gl.UseProgram(p.prog)
	g.uploadProbeUniforms(func(n string) int32 { return p.U(n) })
	gl.Uniform1i(p.U("giEnabled"), 1)
	g.bindAtlases(func(n string) int32 { return p.U(n) })
}

func (g *giRenderer) uploadProbeUniforms(loc func(string) int32) {
	gl.Uniform1i(loc("giCascades"), int32(g.t.cascades))
	gl.Uniform3i(loc("giDims"), giDimX, giDimY, giDimZ)
	for c := 0; c < GIMaxCascades; c++ {
		cc := min(c, max(g.t.cascades-1, 0))
		m := g.min[cc]
		gl.Uniform4f(loc(giCascadeNames[c]), float32(m[0]), float32(m[1]), float32(m[2]), g.cascadeSpacing(cc))
	}
	gl.Uniform3f(loc("giCamPos"), g.cam[0], g.cam[1], g.cam[2])
}

// giCascadeNames are the giCascade[] uniforms' names, built once: they are
// looked up for every lit program every frame.
var giCascadeNames = func() (n [GIMaxCascades]string) {
	for c := range n {
		n[c] = fmt.Sprintf("giCascade[%d]", c)
	}
	return n
}()

func (g *giRenderer) bindAtlases(loc func(string) int32) {
	for _, b := range []struct {
		unit uint32
		tex  uint32
		name string
	}{
		{GIIrradianceUnit, g.irr, "giIrradianceAtlas"},
		{GIVisibilityUnit, g.vis, "giVisibilityAtlas"},
		{GIProbeDataUnit, g.probes, "giProbeData"},
		{GIProbeSurfaceUnit, g.surf, "giProbeSurface"},
		{GIProbePackedUnit, g.packed, "giProbePacked"},
	} {
		tex := b.tex
		if tex == 0 {
			tex = giDummyTexture()
			if b.unit == GIProbePackedUnit {
				tex = giDummyUintTexture()
			}
		}
		gl.ActiveTexture(gl.TEXTURE0 + b.unit)
		gl.BindTexture(gl.TEXTURE_2D, tex)
		gl.Uniform1i(loc(b.name), int32(b.unit))
	}
	gl.ActiveTexture(gl.TEXTURE0)
}

// uploadTraceLights sends what the trace reads of the lights beyond what
// world.UploadLightsTo does: their shadow switches (which the world programs
// take with their shadow maps) and their GI switches (Light.NoGI).
func (g *giRenderer) uploadTraceLights(p *progUniforms, ls world.LightSet) {
	gl.UseProgram(p.prog)
	for i := 0; i < world.MaxDirLights; i++ {
		on, cast := int32(0), int32(0)
		if i < len(ls.Directional) {
			dl := ls.Directional[i]
			if !dl.NoGI {
				on = 1
			}
			if dl.CastsShadows() {
				cast = 1
			}
		}
		gl.Uniform1i(p.U(giTraceLightNames.dirOn[i]), on)
		gl.Uniform1i(p.U(giTraceLightNames.dirCast[i]), cast)
	}
	for i := 0; i < world.MaxPointLights; i++ {
		on, cast := int32(0), int32(0)
		if i < len(ls.Points) {
			pl := ls.Points[i]
			if !pl.NoGI {
				on = 1
			}
			if pl.CastsShadows() {
				cast = 1
			}
		}
		gl.Uniform1i(p.U(giTraceLightNames.pointOn[i]), on)
		gl.Uniform1i(p.U(giTraceLightNames.pointCast[i]), cast)
	}
}

// giTraceLightNames are uploadTraceLights' element names, built once for the
// reason giCascadeNames is. Bare (no NUL): progUniforms.U adds it on a miss.
var giTraceLightNames = func() (n struct {
	dirOn, dirCast     [world.MaxDirLights]string
	pointOn, pointCast [world.MaxPointLights]string
}) {
	for i := range n.dirOn {
		n.dirOn[i] = fmt.Sprintf("giDirLightOn[%d]", i)
		n.dirCast[i] = fmt.Sprintf("giDirLightCasts[%d]", i)
	}
	for i := range n.pointOn {
		n.pointOn[i] = fmt.Sprintf("giPointLightOn[%d]", i)
		n.pointCast[i] = fmt.Sprintf("giPointLightCasts[%d]", i)
	}
	return n
}()

var giDummy uint32

// giDummyTexture is a 1×1 black texture for the atlas samplers while GI is
// off, so no sampler is ever left on a unit with nothing (or something of
// another type) bound.
func giDummyTexture() uint32 {
	if giDummy == 0 {
		gl.GenTextures(1, &giDummy)
		gl.BindTexture(gl.TEXTURE_2D, giDummy)
		px := [4]float32{}
		gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA16F, 1, 1, 0, gl.RGBA, gl.FLOAT, gl.Ptr(&px[0]))
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
		gl.BindTexture(gl.TEXTURE_2D, 0)
	}
	return giDummy
}

var giDummyUint uint32

// giDummyUintTexture is giDummyTexture for an unsigned-integer sampler
// (giProbePacked), which a float texture would make incomplete.
func giDummyUintTexture() uint32 {
	if giDummyUint == 0 {
		gl.GenTextures(1, &giDummyUint)
		gl.BindTexture(gl.TEXTURE_2D, giDummyUint)
		px := [4]uint32{}
		gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA32UI, 1, 1, 0, gl.RGBA_INTEGER, gl.UNSIGNED_INT, gl.Ptr(&px[0]))
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
		gl.BindTexture(gl.TEXTURE_2D, 0)
	}
	return giDummyUint
}

// uploadGITo sends GI's state to a world program: on, with the probes, when
// GI ran this frame; off otherwise. Called from UploadShadowUniformsTo, which
// every program that lights a surface already calls — the clouds ride the
// same call for the same reason.
func uploadGITo(prog uint32) {
	g := &gi
	loc := func(n string) int32 { return gfx.UniformLocName(prog, n) }
	gl.UseProgram(prog)
	debug := int32(0)
	if GIDebug == GIDebugLighting {
		debug = 1
	}
	if !g.active {
		// The lighting view is the shader's, not GI's: with GI off it is the
		// direct light and the flat ambient alone, which is still the picture
		// asked for.
		gl.Uniform1i(loc("giEnabled"), 0)
		gl.Uniform1i(loc("giDebug"), debug)
		g.bindAtlases(loc)
		return
	}
	settings := world.GISettings{Intensity: 1}
	if g.lvl != nil {
		settings = g.lvl.Lights.Global.GI
	}
	gl.Uniform1i(loc("giEnabled"), 1)
	gl.Uniform1i(loc("giDebug"), debug)
	gl.Uniform1f(loc("giIntensity"), settings.Intensity)
	gl.Uniform1f(loc("giAmbientFloor"), settings.AmbientFloor)
	g.uploadProbeUniforms(loc)
	g.bindAtlases(loc)
}

// disableGITo switches GI off in prog, for a pass that lights something that
// is not in the level — the material preview's sphere.
func disableGITo(prog uint32) {
	gl.UseProgram(prog)
	gl.Uniform1i(world.ProgUniformLoc(prog, "giEnabled\x00"), 0)
	gl.Uniform1i(world.ProgUniformLoc(prog, "giDebug\x00"), 0)
}

// GIActive reports whether GI lit the frame just drawn.
func GIActive() bool { return gi.active }
