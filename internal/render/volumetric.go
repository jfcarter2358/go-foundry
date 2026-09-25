package render

// volumetric.go — the volumetric pass: cloud volumes, the sky's cloud deck,
// volume particle layers (fireballs and smoke) and explosion effects,
// ray-marched rather than rasterised.
//
// One pass for all three because they are one idea — light scattered by a
// density field — and one shader marches them (volume_frag.glsl over
// volume_common.glsl). What differs is only where the density comes from: a
// volume's shape filled with noise, a slab of it, or a handful of simulated
// particles each treated as a soft ball.
//
// WHERE IT SITS IN THE FRAME. After the world's transparent pass and before
// the alpha and additive sprites (see app.go). After the opaque pass because
// the march is clipped by the scene's depth; after water and glass so a cloud
// behind a window is seen through it; before the sprites so smoke puffs and
// sparks draw over a fireball rather than under it. The cost of that order is
// that a cloud IN FRONT of a window has the window drawn over it — the same
// trade the sprite pass already makes, for the same reason.
//
// HOW IT FITS A RENDERER WITH NO SCENE FRAMEBUFFER. Everything else draws
// straight into the window's multisampled back buffer (antialias.go), so there
// is no depth texture to read. The pass makes one: the frame's depth is blitted
// into a single-sample texture of the same format (a multisample resolve, which
// glBlitFramebuffer allows for depth). From there, per frame:
//
//  1. depth copy   — the scene depth, one texel per viewport pixel.
//  2. downsample   — distances from the eye at the march's resolution
//     (volume_depth_frag.glsl), nearest of each block.
//  3. march        — every draw into an offscreen RGBA16F target at half or
//     full resolution, premultiplied, back to front. A volume
//     draws its proxy box front-face-culled with depth clamping,
//     so exactly one fragment runs per pixel it could cover,
//     from inside the box or out, however near or far.
//  4. composite    — a depth-aware upsample over the frame
//     (volume_composite_frag.glsl).
//
// A blit the driver refuses — a format it will not match, a virtualised GL that
// does not implement the resolve — is logged once, and the copy is cleared to
// the far plane instead: volumes then draw unclipped by the scene, which is
// wrong at the edges but still draws them, rather than drawing nothing.
//
// PER CONTEXT. Framebuffers and vertex arrays are never shared between GL
// contexts; programs and textures are. So the render targets live in a
// VolumeTargets the CALLER owns — MainVolumeTargets for the main window, one
// each for the particle and entity editors' previews — made in that caller's
// context and released in it.

import (
	"fmt"
	"foundry/internal/gfx"
	"foundry/internal/noise3d"
	"foundry/internal/particle"
	"foundry/internal/world"
	"log"
	"math"
	"sort"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// MaxVolumeBlobs is how many particles one blob draw marches. MUST match
// MAX_VOLUME_BLOBS in volume_common.glsl.
//
// Sixteen because the cost of a sample is a loop over every blob, and a
// fireball reads as one from about nine. A layer with more live particles is
// split into groups of this many (MaxVolumeBlobDraws of them at most), each
// its own draw, composited far to near — unless it overlaps others, which it is
// then marched together with while the sixteen last (mergeLocalDraws).
const MaxVolumeBlobs = 16

// MaxVolumeBlobDraws caps how many groups one layer is split into. Past it the
// nearest particles are the ones dropped — the same truncation MaxFogVolumes
// does — which is what keeps a runaway smoke layer from costing the frame.
const MaxVolumeBlobDraws = 4

// Texture units the volume programs bind. Their own programs, so the world
// shader's unit plan (gfx/graphics.go) does not constrain them; spelled out so
// the three programs agree.
const (
	volUnitScene  = 0
	volUnitShape  = 1
	volUnitDetail = 2
	volUnitAux    = 3
)

// VolumetricSettings are the pass's cost knobs, toggled live (F4, see
// CycleVolumetricQuality) so each one's cost can be read against the F3
// overlay's frame time.
type VolumetricSettings struct {
	// Enabled draws the pass at all.
	Enabled bool
	// HalfRes marches at half the viewport's resolution on each axis and
	// upsamples — a quarter of the cost, and the reason the upsample is
	// depth-aware.
	HalfRes bool
	// Steps is how many samples a ray takes through a volume, and LightSteps
	// how many each of those takes toward the sun.
	Steps      int32
	LightSteps int32
	// CloudShadows bakes and applies the clouds' shadow on the world.
	CloudShadows bool
	// CloudShadowSize is the bake's resolution, and CloudShadowRange the half
	// width of the square around the camera it covers, in world units.
	CloudShadowSize  int32
	CloudShadowRange float32
}

// Volumetrics is the live settings block.
var Volumetrics = VolumetricSettings{
	Enabled:          true,
	HalfRes:          true,
	Steps:            48,
	LightSteps:       5,
	CloudShadows:     true,
	CloudShadowSize:  256,
	CloudShadowRange: 600,
}

// CycleVolumetricQuality steps half resolution -> full resolution -> off ->
// half resolution. F4.
func CycleVolumetricQuality() {
	switch {
	case !Volumetrics.Enabled:
		Volumetrics.Enabled, Volumetrics.HalfRes = true, true
	case Volumetrics.HalfRes:
		Volumetrics.HalfRes = false
	default:
		Volumetrics.Enabled = false
	}
}

// VolumetricQualityName is what the F3 overlay reports.
func VolumetricQualityName() string {
	switch {
	case !Volumetrics.Enabled:
		return "off"
	case Volumetrics.HalfRes:
		return "half resolution"
	default:
		return "full resolution"
	}
}

// VolumeTime is the clock, in seconds, that cloud wind reads. Set once a frame
// by the frame loop before the cloud shadow bake, so a cloud and its shadow
// drift in step. float64 because it is multiplied by a wind speed and wrapped,
// and a float32 of an hour's uptime is already coarser than a noise texel.
var VolumeTime float64

// ---------- init ----------

type volumeRenderer struct {
	ready     bool
	march     progUniforms
	depth     progUniforms
	composite progUniforms
	bake      progUniforms
	shapeTex  uint32
	detailTex uint32

	// Scratch, reused every frame.
	draws  []volumeDraw
	blobs  []particle.Blob
	flat   []float32
	clears []cloudClear
}

var volumes volumeRenderer

// VolumetricInit compiles the four programs and uploads the noise volumes. Must
// be called once with the main context current. Like SkyInit it logs and
// disables itself rather than panicking: a level without its clouds is still a
// level.
func VolumetricInit() {
	if volumes.ready {
		return
	}
	vert := mustReadShader(biomeShaderPath("volume_vert.glsl"))
	shared := mustReadShader(biomeShaderPath("fog.glsl")) + "\n" +
		mustReadShader(biomeShaderPath("volume_common.glsl"))

	build := func(name string, withCommon bool) (progUniforms, bool) {
		frag := mustReadShader(biomeShaderPath(name))
		if withCommon {
			frag = gfx.InjectAfterVersion(frag, shared)
		}
		vs, err := gfx.CompileShader(vert, gl.VERTEX_SHADER)
		if err != nil {
			log.Printf("volume_vert.glsl: %v — volumetrics disabled", err)
			return progUniforms{}, false
		}
		fs, err := gfx.CompileShader(frag, gl.FRAGMENT_SHADER)
		if err != nil {
			log.Printf("%s: %v — volumetrics disabled", name, err)
			return progUniforms{}, false
		}
		prog, err := linkStages(vs, fs)
		if err != nil {
			log.Printf("volumetric program (%s): %v — volumetrics disabled", name, err)
			return progUniforms{}, false
		}
		return progUniforms{prog: prog, loc: map[string]int32{}}, true
	}

	var ok bool
	if volumes.march, ok = build("volume_frag.glsl", true); !ok {
		return
	}
	if volumes.depth, ok = build("volume_depth_frag.glsl", false); !ok {
		return
	}
	if volumes.composite, ok = build("volume_composite_frag.glsl", false); !ok {
		return
	}
	if volumes.bake, ok = build("cloud_shadow_bake_frag.glsl", true); !ok {
		return
	}

	volumes.shapeTex = gfx.UploadTexture3D(noise3d.ShapeSize, 2, noise3d.Shape(noise3d.ShapeSize))
	volumes.detailTex = gfx.UploadTexture3D(noise3d.DetailSize, 1, noise3d.Detail(noise3d.DetailSize))

	volumes.ready = true
	if gfx.Program != 0 {
		gl.UseProgram(gfx.Program)
	}
}

// VolumetricsReady reports whether the pass initialised.
func VolumetricsReady() bool { return volumes.ready }

// ---------- render targets ----------

// VolumeTargets are one GL context's framebuffers for the pass. The zero value
// is ready to use; everything is made on first draw, in whatever context is
// current then, which must be the same context every later call is made in.
type VolumeTargets struct {
	vao uint32

	// The depth copy.
	depthFBO, depthTex uint32
	depthW, depthH     int32
	depthInternal      int32
	srcFBO             uint32
	srcKnown           bool
	srcInternal        int32
	srcHasDepth        bool
	copyFailed, warned bool

	// The march target and its distances.
	lowFBO, lowColor  uint32
	distFBO, lowDist  uint32
	lowW, lowH, scale int32
	lowBroken         bool
}

// MainVolumeTargets are the main window's.
var MainVolumeTargets = &VolumeTargets{}

// Delete releases every object. Call with the owning context current.
func (t *VolumeTargets) Delete() {
	if t == nil {
		return
	}
	if t.vao != 0 {
		gl.DeleteVertexArrays(1, &t.vao)
	}
	t.deleteDepth()
	t.deleteLow()
	*t = VolumeTargets{}
}

func (t *VolumeTargets) deleteDepth() {
	if t.depthFBO != 0 {
		gl.DeleteFramebuffers(1, &t.depthFBO)
	}
	if t.depthTex != 0 {
		gl.DeleteTextures(1, &t.depthTex)
	}
	t.depthFBO, t.depthTex, t.depthW, t.depthH = 0, 0, 0, 0
}

func (t *VolumeTargets) deleteLow() {
	for _, f := range []*uint32{&t.lowFBO, &t.distFBO} {
		if *f != 0 {
			gl.DeleteFramebuffers(1, f)
		}
	}
	for _, tex := range []*uint32{&t.lowColor, &t.lowDist} {
		if *tex != 0 {
			gl.DeleteTextures(1, tex)
		}
	}
	t.lowFBO, t.distFBO, t.lowColor, t.lowDist, t.lowW, t.lowH = 0, 0, 0, 0, 0, 0
}

// sourceDepthFormat reads back the depth attachment of the framebuffer the
// frame is being drawn into, and returns the internal format a texture must
// have for a depth blit from it to be legal — glBlitFramebuffer refuses any
// mismatch outright. hasDepth is false when there is nothing to copy.
func sourceDepthFormat(src uint32) (internal int32, hasDepth bool) {
	gl.BindFramebuffer(gl.READ_FRAMEBUFFER, src)
	depthAtt, stencilAtt := uint32(gl.DEPTH_ATTACHMENT), uint32(gl.STENCIL_ATTACHMENT)
	if src == 0 {
		depthAtt, stencilAtt = gl.DEPTH, gl.STENCIL
	}
	param := func(att, pname uint32) int32 {
		var v int32
		gl.GetFramebufferAttachmentParameteriv(gl.READ_FRAMEBUFFER, att, pname, &v)
		return v
	}
	if param(depthAtt, gl.FRAMEBUFFER_ATTACHMENT_OBJECT_TYPE) == gl.NONE {
		return gl.DEPTH_COMPONENT24, false
	}
	bits := param(depthAtt, gl.FRAMEBUFFER_ATTACHMENT_DEPTH_SIZE)
	float := param(depthAtt, gl.FRAMEBUFFER_ATTACHMENT_COMPONENT_TYPE) == gl.FLOAT
	stencil := int32(0)
	if param(stencilAtt, gl.FRAMEBUFFER_ATTACHMENT_OBJECT_TYPE) != gl.NONE {
		stencil = param(stencilAtt, gl.FRAMEBUFFER_ATTACHMENT_STENCIL_SIZE)
	}
	switch {
	case float && stencil > 0:
		return gl.DEPTH32F_STENCIL8, true
	case float:
		return gl.DEPTH_COMPONENT32F, true
	case bits <= 16:
		return gl.DEPTH_COMPONENT16, true
	case bits <= 24 && stencil > 0:
		return gl.DEPTH24_STENCIL8, true
	case bits <= 24:
		return gl.DEPTH_COMPONENT24, true
	default:
		return gl.DEPTH_COMPONENT32, true
	}
}

// depthUpload is the format/type pair a nil TexImage2D of internal needs.
func depthUpload(internal int32) (format, xtype uint32, attach uint32) {
	switch internal {
	case gl.DEPTH24_STENCIL8:
		return gl.DEPTH_STENCIL, gl.UNSIGNED_INT_24_8, gl.DEPTH_STENCIL_ATTACHMENT
	case gl.DEPTH32F_STENCIL8:
		return gl.DEPTH_STENCIL, gl.FLOAT_32_UNSIGNED_INT_24_8_REV, gl.DEPTH_STENCIL_ATTACHMENT
	default:
		return gl.DEPTH_COMPONENT, gl.FLOAT, gl.DEPTH_ATTACHMENT
	}
}

// ensure (re)builds the targets for a viewport of w x h drawn into src.
func (t *VolumeTargets) ensure(w, h, scale int32, src uint32) bool {
	if t.vao == 0 {
		gl.GenVertexArrays(1, &t.vao)
	}
	if !t.srcKnown || t.srcFBO != src {
		t.srcFBO, t.srcKnown = src, true
		t.srcInternal, t.srcHasDepth = sourceDepthFormat(src)
		t.copyFailed, t.warned = false, false
	}

	if t.depthTex == 0 || t.depthW != w || t.depthH != h || t.depthInternal != t.srcInternal {
		t.deleteDepth()
		t.depthW, t.depthH, t.depthInternal = w, h, t.srcInternal
		format, xtype, attach := depthUpload(t.srcInternal)
		gl.GenTextures(1, &t.depthTex)
		gl.BindTexture(gl.TEXTURE_2D, t.depthTex)
		gl.TexImage2D(gl.TEXTURE_2D, 0, t.srcInternal, w, h, 0, format, xtype, nil)
		setNearestClamp()
		gl.GenFramebuffers(1, &t.depthFBO)
		gl.BindFramebuffer(gl.FRAMEBUFFER, t.depthFBO)
		gl.FramebufferTexture2D(gl.FRAMEBUFFER, attach, gl.TEXTURE_2D, t.depthTex, 0)
		gl.DrawBuffer(gl.NONE)
		gl.ReadBuffer(gl.NONE)
		if s := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); s != gl.FRAMEBUFFER_COMPLETE {
			log.Printf("volumetrics: depth copy framebuffer incomplete (0x%x) — volumetrics disabled", s)
			t.deleteDepth()
			return false
		}
	}

	lowW, lowH := (w+scale-1)/scale, (h+scale-1)/scale
	if t.lowFBO == 0 || t.lowW != lowW || t.lowH != lowH || t.scale != scale {
		t.deleteLow()
		t.lowW, t.lowH, t.scale = lowW, lowH, scale
		t.lowColor, t.lowFBO = colorTarget(gl.RGBA16F, gl.RGBA, lowW, lowH)
		t.lowDist, t.distFBO = colorTarget(gl.R32F, gl.RED, lowW, lowH)
		if t.lowFBO == 0 || t.distFBO == 0 {
			if !t.lowBroken {
				log.Printf("volumetrics: float render targets unavailable — volumetrics disabled")
			}
			t.lowBroken = true
			t.deleteLow()
			return false
		}
	}
	return true
}

func setNearestClamp() {
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
}

// colorTarget makes a one-attachment framebuffer, or returns zeros if the
// driver will not render to the format.
func colorTarget(internal int32, format uint32, w, h int32) (tex, fbo uint32) {
	gl.GenTextures(1, &tex)
	gl.BindTexture(gl.TEXTURE_2D, tex)
	gl.TexImage2D(gl.TEXTURE_2D, 0, internal, w, h, 0, format, gl.FLOAT, nil)
	setNearestClamp()
	gl.GenFramebuffers(1, &fbo)
	gl.BindFramebuffer(gl.FRAMEBUFFER, fbo)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, tex, 0)
	gl.DrawBuffer(gl.COLOR_ATTACHMENT0)
	if gl.CheckFramebufferStatus(gl.FRAMEBUFFER) != gl.FRAMEBUFFER_COMPLETE {
		gl.DeleteFramebuffers(1, &fbo)
		gl.DeleteTextures(1, &tex)
		return 0, 0
	}
	return tex, fbo
}

// copyDepth fills the depth copy from the viewport rect of the source.
func (t *VolumeTargets) copyDepth(vx, vy, w, h int32) {
	gl.BindFramebuffer(gl.DRAW_FRAMEBUFFER, t.depthFBO)
	if !t.srcHasDepth || t.copyFailed {
		// Nothing to copy, or a driver that will not: the far plane everywhere,
		// which draws every volume unclipped rather than none of them.
		gl.ClearBufferfv(gl.DEPTH, 0, &[]float32{1}[0])
		return
	}
	for i := 0; i < 16 && gl.GetError() != gl.NO_ERROR; i++ {
	}
	gl.BindFramebuffer(gl.READ_FRAMEBUFFER, t.srcFBO)
	gl.BlitFramebuffer(vx, vy, vx+w, vy+h, 0, 0, w, h, gl.DEPTH_BUFFER_BIT, gl.NEAREST)
	if err := gl.GetError(); err != gl.NO_ERROR {
		t.copyFailed = true
		if !t.warned {
			t.warned = true
			log.Printf("volumetrics: the driver refused the depth copy (GL error 0x%x) — volumes will draw unclipped by the scene", err)
		}
		gl.BindFramebuffer(gl.DRAW_FRAMEBUFFER, t.depthFBO)
		gl.ClearBufferfv(gl.DEPTH, 0, &[]float32{1}[0])
	}
}

// ---------- what gets drawn ----------

// Draw kinds. MUST match VOL_KIND_* in volume_frag.glsl.
const (
	volKindCloud int32 = 0
	// volKindLocal is particle blobs and/or one explosion effect
	// (world/effect.go) in a box: everything that goes off in one place,
	// marched together.
	volKindLocal int32 = 1
	volKindDeck  int32 = 2
)

// cloudParams is one CloudParams struct, as volume_common.glsl declares it.
type cloudParams struct {
	shape                 int32
	basis                 mgl32.Mat3
	center                mgl32.Vec3
	slabBottom, slabTop   float32
	softness              float32
	coverage, density     float32
	noiseFreq, detailFreq float32
	erosion               float32
	windOffset            mgl32.Vec3
	detailOffset          mgl32.Vec3
	albedo                mgl32.Vec3
	absorption, ambient   float32
}

// volShapeSlab is VOL_SHAPE_SLAB.
const volShapeSlab = 3

// newCloudParams fills in everything a look decides. The wind offsets are
// wrapped by their own noise's repeat: the noise is identical one repeat on,
// so the wrap is invisible, and the offset stays a small number however long
// the session runs.
func newCloudParams(look world.CloudLook, time float64) cloudParams {
	offset := func(period float32, rate float64) mgl32.Vec3 {
		p := float64(period)
		wrap := func(w float32) float32 { return float32(math.Mod(float64(w)*time*rate, p)) }
		return mgl32.Vec3{wrap(look.Wind.X()), wrap(look.Wind.Y()), wrap(look.Wind.Z())}
	}
	return cloudParams{
		coverage:     look.Coverage,
		density:      look.Density,
		noiseFreq:    1 / look.Scale,
		detailFreq:   1 / look.Detail,
		erosion:      look.Erosion,
		windOffset:   offset(look.Scale, 1),
		detailOffset: offset(look.Detail, 1.6),
		albedo:       look.Color,
		absorption:   look.Absorption,
		ambient:      look.Ambient,
		softness:     1,
	}
}

// volumeCloudParams is a cloud volume's.
func volumeCloudParams(v *world.Volume, lvl *world.Level, time float64) cloudParams {
	c := newCloudParams(v.Cloud.CloudLook, time)
	c.shape = int32(v.Shape)
	c.basis = v.WorldFogBasis(lvl) // the same normalised space a fog bank is solved in
	c.center = v.WorldPosition(lvl)
	c.softness = v.Cloud.Softness
	return c
}

// deckCloudParams is the sky deck's.
func deckCloudParams(d world.SkyClouds, time float64) cloudParams {
	c := newCloudParams(d.CloudLook, time)
	c.shape = volShapeSlab
	c.basis = mgl32.Ident3()
	c.slabBottom = d.Altitude
	c.slabTop = d.Altitude + d.Thickness
	return c
}

// cloudClear is one region cloud is kept out of (Volume.ClearsClouds), in the
// normalised shape space a fog bank is solved in: basis takes a world offset
// from center to the unit box, sphere or cylinder shape names.
type cloudClear struct {
	shape  int32
	basis  mgl32.Mat3
	center mgl32.Vec3
}

// gatherCloudClears collects the frame's cloud-clearing regions: every enabled
// one in view, in level order, up to world.MaxCloudClearVolumes.
//
// Culled before the cap rather than after, so four ships parked across the map
// do not use up the slots the one on screen needed. The cull is exact enough:
// every sample the march takes is on a ray through the view, so a region wholly
// outside the frustum can contain none of them.
func gatherCloudClears(lvl *world.Level, cull *Frustum) []cloudClear {
	out := volumes.clears[:0]
	for i := range lvl.Volumes {
		v := &lvl.Volumes[i]
		if !v.ClearsCloudsNow() {
			continue
		}
		if cull != nil {
			if lo, hi, ok := v.WorldAABB(lvl); ok && !AABBInFrustum(*cull, lo, hi) {
				continue
			}
		}
		out = append(out, cloudClear{
			shape:  int32(v.Shape),
			basis:  v.WorldFogBasis(lvl), // the same space the cloud itself is solved in
			center: v.WorldPosition(lvl),
		})
		if len(out) == world.MaxCloudClearVolumes {
			break
		}
	}
	volumes.clears = out
	return out
}

// volumeDraw is one march.
type volumeDraw struct {
	kind       int32
	depth      float32 // distance from the eye, for ordering
	fullscreen bool
	proxy      mgl32.Mat4
	lightReach float32
	deckRange  float32

	cloud cloudParams

	// A local draw's box, in world space, and what is in it: up to
	// MaxVolumeBlobs blobs in runs that share a layer's look, and at most one
	// explosion.
	lo, hi       mgl32.Vec3
	proxyBasis   mgl32.Mat3
	proxyCenter  mgl32.Vec3
	parts        [MaxVolumeBlobs]blobPart
	nParts       int
	nBlobs       int
	hasExplosion bool

	explosion explosionParams
}

// blobPart is a run of one layer's blobs in a local draw.
type blobPart struct {
	blobs []particle.Blob
	look  *particle.VolumeLook
}

// explosionParams is one explosion draw, as volume_common.glsl's explosion
// uniforms read it.
type explosionParams struct {
	center     mgl32.Vec3
	radius     float32
	reach      float32
	age        float32
	heat       float32
	density    float32
	fade       float32
	jets       float32
	turbulence float32
	brightness float32
	seed       float32
	rotation   mgl32.Mat3 // world -> the explosion's own axes, for its noise
	fireTint   mgl32.Vec3
	smoke      mgl32.Vec3
}

// explosionDraw builds the march for one live explosion effect.
func explosionDraw(e *world.Effect, lvl *world.Level, camPos mgl32.Vec3) (volumeDraw, bool) {
	st, ok := e.ExplosionState(lvl)
	if !ok {
		return volumeDraw{}, false
	}
	x := &e.Explosion
	reach := x.Reach(st)
	if reach < 1e-3 {
		return volumeDraw{}, false
	}
	c := st.Center
	ext := mgl32.Vec3{reach, reach, reach}
	dr := volumeDraw{
		kind:         volKindLocal,
		hasExplosion: true,
		explosion: explosionParams{
			center: c,
			radius: st.Radius,
			reach:  reach,
			age:    st.Age,
			heat:   st.Heat,
			// Thinned partly by density and partly by eating the surface away
			// (volExpFadeK), so the last smoke breaks up rather than dimming
			// as a ball.
			density:    x.Density * (0.35 + 0.65*st.Fade),
			fade:       st.Fade,
			jets:       st.Jets,
			turbulence: x.Turbulence,
			brightness: x.Brightness,
			// Seeded by ID, so two explosions side by side do not billow alike.
			seed:     float32(effectSeed(e.ID)%1024) / 1024,
			rotation: e.WorldRotation(lvl).Transpose(),
			fireTint: x.FireTint,
			smoke:    x.SmokeColor,
		},
	}
	dr.setBox(c.Sub(ext), c.Add(ext), camPos)
	return dr, true
}

// setBox makes lo..hi a local draw's box: the proxy drawn, the space its march
// is confined to, and the distance it is sorted by.
func (dr *volumeDraw) setBox(lo, hi, camPos mgl32.Vec3) {
	center := lo.Add(hi).Mul(0.5)
	half := hi.Sub(lo).Mul(0.5)
	for a := 0; a < 3; a++ {
		if half[a] < 1e-3 {
			half[a] = 1e-3
		}
	}
	dr.lo, dr.hi = lo, hi
	dr.depth = center.Sub(camPos).Len()
	dr.proxy = mgl32.Translate3D(center.X(), center.Y(), center.Z()).Mul4(mgl32.Scale3D(half.X(), half.Y(), half.Z()))
	dr.proxyBasis = mgl32.Mat3{1 / half.X(), 0, 0, 0, 1 / half.Y(), 0, 0, 0, 1 / half.Z()}
	dr.proxyCenter = center
	dr.lightReach = half.Len() * 2
}

// mergeLocalDraws marches local draws that overlap as one.
//
// Two volumes drawn separately are composited whole, in the order of their
// centres: a smoke layer wrapped round a fireball — or an explosion effect
// going off inside a particle effect's smoke — was drawn wholly over the fire
// or wholly under it, and flipped as the eye moved. One march integrates both
// in the order the ray meets them. Merging stops at what one draw holds:
// MaxVolumeBlobs blobs and one explosion; past that the rest stay separate and
// are stacked the old way.
func mergeLocalDraws(draws []volumeDraw, camPos mgl32.Vec3) []volumeDraw {
	for merged := true; merged; {
		merged = false
		for i := 0; i < len(draws); i++ {
			for j := i + 1; j < len(draws); j++ {
				a, b := &draws[i], &draws[j]
				if !canMergeLocal(a, b) {
					continue
				}
				copy(a.parts[a.nParts:], b.parts[:b.nParts])
				a.nParts += b.nParts
				a.nBlobs += b.nBlobs
				if b.hasExplosion {
					a.hasExplosion = true
					a.explosion = b.explosion
				}
				lo, hi := a.lo, a.hi
				for k := 0; k < 3; k++ {
					lo[k] = float32(math.Min(float64(lo[k]), float64(b.lo[k])))
					hi[k] = float32(math.Max(float64(hi[k]), float64(b.hi[k])))
				}
				a.setBox(lo, hi, camPos)
				draws = append(draws[:j], draws[j+1:]...)
				j--
				merged = true
			}
		}
	}
	return draws
}

func canMergeLocal(a, b *volumeDraw) bool {
	if a.kind != volKindLocal || b.kind != volKindLocal {
		return false
	}
	if a.nBlobs+b.nBlobs > MaxVolumeBlobs || (a.hasExplosion && b.hasExplosion) {
		return false
	}
	for k := 0; k < 3; k++ {
		if a.lo[k] > b.hi[k] || b.lo[k] > a.hi[k] {
			return false
		}
	}
	return true
}

// effectSeed is an FNV-1a of an effect's ID.
func effectSeed(id string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(id); i++ {
		h ^= uint32(id[i])
		h *= 16777619
	}
	return h
}

// gatherLevelVolumes collects the frame's draws from a level: the deck, every
// cloud volume in view, and every volume particle layer in view.
func gatherLevelVolumes(lvl *world.Level, camPos mgl32.Vec3, cull *Frustum) []volumeDraw {
	draws := volumes.draws[:0]
	volumes.blobs = volumes.blobs[:0]

	if deck, ok := lvl.Sky.EffectiveClouds(); ok {
		draws = append(draws, volumeDraw{
			kind:       volKindDeck,
			depth:      float32(math.Inf(1)),
			fullscreen: true,
			cloud:      deckCloudParams(deck, VolumeTime),
			lightReach: deck.Thickness * 3,
			deckRange:  deck.Range,
		})
	}

	for _, v := range world.CloudVolumes(lvl.Volumes) {
		lo, hi, ok := v.WorldAABB(lvl)
		if !ok || (cull != nil && !AABBInFrustum(*cull, lo, hi)) {
			continue
		}
		draws = append(draws, volumeDraw{
			kind:       volKindCloud,
			depth:      v.WorldPosition(lvl).Sub(camPos).Len(),
			proxy:      v.WorldMatrix(lvl).Mul4(mgl32.Scale3D(v.HalfX, v.HalfY, v.HalfZ)),
			cloud:      volumeCloudParams(v, lvl, VolumeTime),
			lightReach: hi.Sub(lo).Len(),
		})
	}

	for ei := range lvl.Emitters {
		e := &lvl.Emitters[ei]
		if !e.Enabled || !e.Loaded() {
			continue
		}
		if cull != nil {
			if lo, hi, ok := e.WorldAABB(lvl); ok && !AABBInFrustum(*cull, lo, hi) {
				continue
			}
		}
		for _, s := range e.Systems {
			draws = appendBlobDraws(draws, s, camPos)
		}
	}

	for i := range lvl.Effects {
		dr, ok := explosionDraw(&lvl.Effects[i], lvl, camPos)
		if !ok {
			continue
		}
		if cull != nil {
			r := dr.explosion.reach
			ext := mgl32.Vec3{r, r, r}
			if !AABBInFrustum(*cull, dr.explosion.center.Sub(ext), dr.explosion.center.Add(ext)) {
				continue
			}
		}
		draws = append(draws, dr)
	}

	draws = mergeLocalDraws(draws, camPos)
	volumes.draws = draws
	return draws
}

// appendBlobDraws adds one volume layer's live particles as groups of
// MaxVolumeBlobs.
func appendBlobDraws(draws []volumeDraw, s *particle.System, camPos mgl32.Vec3) []volumeDraw {
	if s == nil || s.Layer.Render != particle.RenderVolume || s.Layer.Muted || s.Count() == 0 {
		return draws
	}
	want := s.Count()
	if want > MaxVolumeBlobs*MaxVolumeBlobDraws {
		want = MaxVolumeBlobs * MaxVolumeBlobDraws
	}
	start := len(volumes.blobs)
	if cap(volumes.blobs)-start < want {
		grown := make([]particle.Blob, start, start+want+64)
		copy(grown, volumes.blobs)
		volumes.blobs = grown
	}
	buf := volumes.blobs[start : start+want]
	n := s.FillBlobs(buf, camPos)
	volumes.blobs = volumes.blobs[:start+n]
	// The layer's look with its material's constants folded in — the same
	// answer FillBlobs used for the blobs themselves, so the box the group is
	// marched in and the balls inside it agree about displacement.
	resolved := s.Layer.DrawVolume()
	look := &resolved

	for g := 0; g < n; g += MaxVolumeBlobs {
		end := g + MaxVolumeBlobs
		if end > n {
			end = n
		}
		group := buf[g:end]
		// The group's box, wide enough for the noise to push a surface out.
		lo := mgl32.Vec3{float32(math.Inf(1)), float32(math.Inf(1)), float32(math.Inf(1))}
		hi := lo.Mul(-1)
		for _, b := range group {
			r := b.Radius * (1 + look.Displacement)
			for a := 0; a < 3; a++ {
				lo[a] = float32(math.Min(float64(lo[a]), float64(b.Pos[a]-r)))
				hi[a] = float32(math.Max(float64(hi[a]), float64(b.Pos[a]+r)))
			}
		}
		dr := volumeDraw{kind: volKindLocal, nParts: 1, nBlobs: len(group)}
		dr.parts[0] = blobPart{blobs: group, look: look}
		dr.setBox(lo, hi, camPos)
		draws = append(draws, dr)
	}
	return draws
}

// ---------- light ----------

// volumeLight is the lighting a march is given.
type volumeLight struct {
	toSun, sun        mgl32.Vec3
	ambTop, ambBottom mgl32.Vec3
}

// volumeSun is the first lit directional light within the shader's count: the
// one clouds are lit by and cast their shadow from. idx is its index in the
// uploaded dirLight arrays, which is what cloud_shadow.glsl compares against.
func volumeSun(ls world.LightSet) (idx int, toSun, color mgl32.Vec3, ok bool) {
	n := len(ls.Directional)
	if n > world.MaxDirLights {
		n = world.MaxDirLights
	}
	for i := 0; i < n; i++ {
		d := ls.Directional[i]
		b := d.EffectiveBrightness()
		if b <= 0 || d.Direction.Len() < 1e-6 {
			continue
		}
		return i, d.Direction.Normalize().Mul(-1), d.Color.Mul(b), true
	}
	return 0, mgl32.Vec3{0, 1, 0}, mgl32.Vec3{}, false
}

// levelVolumeLighting resolves the level's sun, its global light and its sky
// into the four numbers the march reads. The sky's colour is the ambient's
// tint — a cloud's shadowed side is lit by the sky around it, and a cloud under
// a dusk sky that stayed white-grey underneath would read as pasted on.
func levelVolumeLighting(lvl *world.Level) volumeLight {
	ls := lvl.Lights
	amb := ls.Global.Color.Mul(ls.Global.EffectiveBrightness())
	l := volumeLight{ambTop: amb, ambBottom: amb.Mul(0.7)}
	if s := lvl.Sky; s != nil {
		tint := s.Tint.Mul(s.Intensity)
		top, bottom := s.Zenith, s.Horizon
		if s.Kind == world.SkyCubemap {
			top, bottom = mgl32.Vec3{0.8, 0.8, 0.8}, mgl32.Vec3{0.6, 0.6, 0.6}
		}
		mul := func(a, b mgl32.Vec3) mgl32.Vec3 { return mgl32.Vec3{a[0] * b[0], a[1] * b[1], a[2] * b[2]} }
		l.ambTop = l.ambTop.Add(mul(top, tint).Mul(0.4))
		l.ambBottom = l.ambBottom.Add(mul(bottom, tint).Mul(0.25))
	}
	_, l.toSun, l.sun, _ = volumeSun(ls)
	return l
}

// previewVolumeLighting is the light rig the editors' previews use. A preview
// has no scene to take light from, and smoke authored against no light at all
// would be dialled in wrong — a fireball's smoke is lit by the sky it rises
// into, and only its core glows on its own.
var previewVolumeLighting = volumeLight{
	toSun:     mgl32.Vec3{0.4, 1, 0.3}.Normalize(),
	sun:       mgl32.Vec3{1.1, 1.05, 1},
	ambTop:    mgl32.Vec3{0.45, 0.5, 0.6},
	ambBottom: mgl32.Vec3{0.25, 0.25, 0.27},
}

// ---------- drawing ----------

// RenderVolumetrics draws a level's clouds, deck and volume particle layers
// over what is in the current framebuffer's viewport. See the file comment for
// where in the frame it belongs.
func RenderVolumetrics(lvl *world.Level, projView mgl32.Mat4, camPos mgl32.Vec3, cull *Frustum, t *VolumeTargets) {
	DebugStats.VolumeDraws, DebugStats.VolumeBlobs = 0, 0
	if !volumes.ready || !Volumetrics.Enabled || lvl == nil || t == nil {
		return
	}
	draws := gatherLevelVolumes(lvl, camPos, cull)
	if len(draws) == 0 {
		return
	}
	gatherCloudClears(lvl, cull)
	renderVolumeDraws(t, draws, projView, camPos, levelVolumeLighting(lvl), func(prog uint32) {
		// With the effects' flashes, so an explosion's smoke is lit by its own
		// fire and by the one that went off beside it.
		world.UploadLightsTo(prog, world.SceneLights(lvl), false)
		world.UploadFogTo(prog, lvl.Fog, lvl.Volumes, lvl)
	})
}

// DrawVolumeParticlePreview draws the volume layers among systems through the
// same pass, for an editor preview with no level behind it: the preview light
// rig, no fog, and t owned by the preview's own window.
func DrawVolumeParticlePreview(t *VolumeTargets, systems []*particle.System, projView mgl32.Mat4, camPos mgl32.Vec3) {
	if !volumes.ready || !Volumetrics.Enabled || t == nil {
		return
	}
	draws := volumes.draws[:0]
	volumes.blobs = volumes.blobs[:0]
	// Nothing to clear, and the program is shared with the main window's pass,
	// so a count left over from the level would otherwise carry into this one.
	volumes.clears = volumes.clears[:0]
	for _, s := range systems {
		draws = appendBlobDraws(draws, s, camPos)
	}
	draws = mergeLocalDraws(draws, camPos)
	volumes.draws = draws
	if len(draws) == 0 {
		return
	}
	renderVolumeDraws(t, draws, projView, camPos, previewVolumeLighting, func(prog uint32) {
		world.UploadLightsTo(prog, world.LightSet{}, false)
		world.UploadFogTo(prog, nil, nil, nil)
	})
}

// renderVolumeDraws is the four steps, with every piece of GL state it touches
// put back on the way out.
func renderVolumeDraws(t *VolumeTargets, draws []volumeDraw, projView mgl32.Mat4, camPos mgl32.Vec3, light volumeLight, uploadScene func(prog uint32)) {
	var vp [4]int32
	gl.GetIntegerv(gl.VIEWPORT, &vp[0])
	var boundFBO int32
	gl.GetIntegerv(gl.DRAW_FRAMEBUFFER_BINDING, &boundFBO)
	src := uint32(boundFBO)
	w, h := vp[2], vp[3]
	if w <= 0 || h <= 0 {
		return
	}
	scale := int32(1)
	if Volumetrics.HalfRes {
		scale = 2
	}

	state := saveVolumeGLState()
	defer state.restore(src, vp)

	if !t.ensure(w, h, scale, src) {
		return
	}
	gl.BindVertexArray(t.vao)
	gl.Disable(gl.DEPTH_TEST)
	gl.DepthMask(false)
	gl.Disable(gl.SAMPLE_ALPHA_TO_COVERAGE)

	invPV := projView.Inv()

	// 1. The depth copy.
	t.copyDepth(vp[0], vp[1], w, h)

	// 2. Distances at the march's resolution.
	gl.BindFramebuffer(gl.FRAMEBUFFER, t.distFBO)
	gl.Viewport(0, 0, t.lowW, t.lowH)
	gl.Disable(gl.BLEND)
	gl.Disable(gl.CULL_FACE)
	d := &volumes.depth
	gl.UseProgram(d.prog)
	bindTex2D(volUnitScene, t.depthTex)
	gl.Uniform1i(d.U("volSceneDepth"), volUnitScene)
	gl.Uniform2i(d.U("volDepthSize"), w, h)
	gl.Uniform1i(d.U("volScale"), scale)
	gl.UniformMatrix4fv(d.U("volInvProjView"), 1, false, &invPV[0])
	gl.Uniform3f(d.U("volCamPos"), camPos.X(), camPos.Y(), camPos.Z())
	gl.Uniform1i(d.U("volFullscreen"), 1)
	gl.DrawArrays(gl.TRIANGLES, 0, 3)

	// 3. The march, far to near, with the deck first: it is behind or around
	// everything else in the sky.
	sort.SliceStable(draws, func(a, b int) bool { return draws[a].depth > draws[b].depth })

	gl.BindFramebuffer(gl.FRAMEBUFFER, t.lowFBO)
	zero := [4]float32{}
	gl.ClearBufferfv(gl.COLOR, 0, &zero[0])
	gl.Enable(gl.BLEND)
	gl.BlendFuncSeparate(gl.ONE, gl.ONE_MINUS_SRC_ALPHA, gl.ONE, gl.ONE_MINUS_SRC_ALPHA)
	gl.Enable(gl.DEPTH_CLAMP)

	m := &volumes.march
	uploadScene(m.prog)
	gl.UseProgram(m.prog)
	bindTex2D(volUnitScene, t.lowDist)
	bindTex3D(volUnitShape, volumes.shapeTex)
	bindTex3D(volUnitDetail, volumes.detailTex)
	gl.Uniform1i(m.U("volSceneDist"), volUnitScene)
	gl.Uniform1i(m.U("volNoiseShape"), volUnitShape)
	gl.Uniform1i(m.U("volNoiseDetail"), volUnitDetail)
	gl.UniformMatrix4fv(m.U("projView"), 1, false, &projView[0])
	gl.UniformMatrix4fv(m.U("volInvProjView"), 1, false, &invPV[0])
	gl.Uniform3f(m.U("volCamPos"), camPos.X(), camPos.Y(), camPos.Z())
	gl.Uniform2f(m.U("volTargetSize"), float32(t.lowW), float32(t.lowH))
	gl.Uniform1i(m.U("volSteps"), Volumetrics.Steps)
	gl.Uniform1i(m.U("volLightSteps"), Volumetrics.LightSteps)
	gl.Uniform1f(m.U("volPixelFootprint"), pixelFootprint(invPV, camPos, t.lowH))
	uniformVec3(m.U("volSunDir"), light.toSun)
	uniformVec3(m.U("volSunColor"), light.sun)
	uniformVec3(m.U("volAmbientTop"), light.ambTop)
	uniformVec3(m.U("volAmbientBottom"), light.ambBottom)
	uploadCloudClears(m, volumes.clears)

	for i := range draws {
		dr := &draws[i]
		gl.Uniform1i(m.U("volKind"), dr.kind)
		gl.Uniform1f(m.U("volLightReach"), dr.lightReach)
		gl.Uniform1f(m.U("volDeckRange"), dr.deckRange)
		if dr.kind == volKindLocal {
			uploadBlobs(m, dr)
			DebugStats.VolumeBlobs += dr.nBlobs
			// Written every draw, on or off: the program keeps the last one's.
			if dr.hasExplosion {
				gl.Uniform1i(m.U("volExpOn"), 1)
				uploadExplosion(m, &dr.explosion)
			} else {
				gl.Uniform1i(m.U("volExpOn"), 0)
			}
		} else {
			uploadCloudParams(m, "volCloud", &dr.cloud)
		}
		if dr.fullscreen {
			gl.Disable(gl.CULL_FACE)
			gl.Uniform1i(m.U("volFullscreen"), 1)
			gl.DrawArrays(gl.TRIANGLES, 0, 3)
		} else {
			gl.Enable(gl.CULL_FACE)
			gl.CullFace(gl.FRONT)
			gl.UniformMatrix4fv(m.U("proxyModel"), 1, false, &dr.proxy[0])
			gl.Uniform1i(m.U("volFullscreen"), 0)
			gl.DrawArrays(gl.TRIANGLES, 0, 36)
		}
		DebugStats.VolumeDraws++
	}
	gl.Disable(gl.DEPTH_CLAMP)
	gl.Disable(gl.CULL_FACE)

	// 4. Over the frame.
	gl.BindFramebuffer(gl.FRAMEBUFFER, src)
	gl.Viewport(vp[0], vp[1], w, h)
	c := &volumes.composite
	gl.UseProgram(c.prog)
	bindTex2D(volUnitScene, t.lowColor)
	bindTex2D(volUnitShape, t.lowDist)
	bindTex2D(volUnitDetail, t.depthTex)
	gl.Uniform1i(c.U("volColor"), volUnitScene)
	gl.Uniform1i(c.U("volDist"), volUnitShape)
	gl.Uniform1i(c.U("volSceneDepth"), volUnitDetail)
	gl.Uniform2i(c.U("volLowSize"), t.lowW, t.lowH)
	gl.Uniform2i(c.U("volFullSize"), w, h)
	gl.Uniform2f(c.U("volViewportOrigin"), float32(vp[0]), float32(vp[1]))
	gl.Uniform1i(c.U("volScale"), scale)
	gl.UniformMatrix4fv(c.U("volInvProjView"), 1, false, &invPV[0])
	gl.Uniform3f(c.U("volCamPos"), camPos.X(), camPos.Y(), camPos.Z())
	gl.Uniform1i(c.U("volFullscreen"), 1)
	gl.DrawArrays(gl.TRIANGLES, 0, 3)
}

// pixelFootprint is the world size of one target pixel one unit in front of
// the eye: the angle between the rays through the centre pixel and the one
// above it. What turns a sample's distance into a noise mip.
func pixelFootprint(invPV mgl32.Mat4, camPos mgl32.Vec3, targetH int32) float32 {
	ray := func(y float32) mgl32.Vec3 {
		p := invPV.Mul4x1(mgl32.Vec4{0, y, 1, 1})
		return p.Vec3().Mul(1 / p.W()).Sub(camPos).Normalize()
	}
	if targetH <= 0 {
		targetH = 1
	}
	return ray(2 / float32(targetH)).Sub(ray(0)).Len()
}

func uploadBlobs(m *progUniforms, dr *volumeDraw) {
	n := dr.nBlobs
	parts := dr.parts[:dr.nParts]
	flat := volumes.flat[:0]
	for _, p := range parts {
		for _, b := range p.blobs {
			flat = append(flat, b.Pos.X(), b.Pos.Y(), b.Pos.Z(), b.Radius)
		}
	}
	for _, p := range parts {
		for _, b := range p.blobs {
			flat = append(flat, b.Emission.X(), b.Emission.Y(), b.Emission.Z(), b.Density)
		}
	}
	for _, p := range parts {
		for _, b := range p.blobs {
			flat = append(flat, b.LifeT, b.Erosion, b.Seed, 0)
		}
	}
	for _, p := range parts {
		for range p.blobs {
			flat = append(flat, p.look.NoiseScale, p.look.Displacement, p.look.Billow, p.look.Absorption)
		}
	}
	for _, p := range parts {
		for range p.blobs {
			flat = append(flat, p.look.Albedo.X(), p.look.Albedo.Y(), p.look.Albedo.Z())
		}
	}
	volumes.flat = flat
	gl.Uniform1i(m.U("volBlobCount"), int32(n))
	if n > 0 {
		gl.Uniform4fv(m.U("volBlobSphere[0]"), int32(n), &flat[0])
		gl.Uniform4fv(m.U("volBlobGlow[0]"), int32(n), &flat[4*n])
		gl.Uniform4fv(m.U("volBlobState[0]"), int32(n), &flat[8*n])
		gl.Uniform4fv(m.U("volBlobLook[0]"), int32(n), &flat[12*n])
		gl.Uniform3fv(m.U("volBlobAlbedo[0]"), int32(n), &flat[16*n])
	}
	gl.UniformMatrix3fv(m.U("volProxyBasis"), 1, false, &dr.proxyBasis[0])
	uniformVec3(m.U("volProxyCenter"), dr.proxyCenter)
}

// uploadCloudClears writes the regions cloud is kept out of. The count is
// written even at zero, for the reason numFogVolumes is: the program holds
// whatever the last frame left in it.
func uploadCloudClears(m *progUniforms, clears []cloudClear) {
	gl.Uniform1i(m.U("volClearCount"), int32(len(clears)))
	for i := range clears {
		c := &clears[i]
		n := &cloudClearNames[i]
		gl.Uniform1i(m.U(n.shape), c.shape)
		gl.UniformMatrix3fv(m.U(n.basis), 1, false, &c.basis[0])
		uniformVec3(m.U(n.center), c.center)
	}
}

// cloudClearNames are the array elements' uniform names, built once rather
// than formatted every frame.
var cloudClearNames = func() (out [world.MaxCloudClearVolumes]struct{ shape, basis, center string }) {
	for i := range out {
		out[i].shape = fmt.Sprintf("volClearShape[%d]", i)
		out[i].basis = fmt.Sprintf("volClearBasis[%d]", i)
		out[i].center = fmt.Sprintf("volClearCenter[%d]", i)
	}
	return out
}()

func uploadExplosion(m *progUniforms, x *explosionParams) {
	uniformVec3(m.U("volExpCenter"), x.center)
	gl.Uniform1f(m.U("volExpRadius"), x.radius)
	gl.Uniform1f(m.U("volExpReach"), x.reach)
	gl.Uniform1f(m.U("volExpAge"), x.age)
	gl.Uniform1f(m.U("volExpHeat"), x.heat)
	gl.Uniform1f(m.U("volExpDensity"), x.density)
	gl.Uniform1f(m.U("volExpFadeK"), x.fade)
	gl.Uniform1f(m.U("volExpJets"), x.jets)
	gl.Uniform1f(m.U("volExpTurbulence"), x.turbulence)
	gl.Uniform1f(m.U("volExpBrightness"), x.brightness)
	gl.Uniform1f(m.U("volExpSeed"), x.seed)
	gl.UniformMatrix3fv(m.U("volExpRotation"), 1, false, &x.rotation[0])
	uniformVec3(m.U("volExpFireTint"), x.fireTint)
	uniformVec3(m.U("volExpSmoke"), x.smoke)
}

// uploadCloudParams writes one CloudParams struct, under prefix ("volCloud",
// or "shadowClouds[i]").
func uploadCloudParams(p *progUniforms, prefix string, c *cloudParams) {
	gl.Uniform1i(p.U(prefix+".shape"), c.shape)
	gl.UniformMatrix3fv(p.U(prefix+".basis"), 1, false, &c.basis[0])
	uniformVec3(p.U(prefix+".center"), c.center)
	gl.Uniform1f(p.U(prefix+".slabBottom"), c.slabBottom)
	gl.Uniform1f(p.U(prefix+".slabTop"), c.slabTop)
	gl.Uniform1f(p.U(prefix+".softness"), c.softness)
	gl.Uniform1f(p.U(prefix+".coverage"), c.coverage)
	gl.Uniform1f(p.U(prefix+".density"), c.density)
	gl.Uniform1f(p.U(prefix+".noiseFreq"), c.noiseFreq)
	gl.Uniform1f(p.U(prefix+".detailFreq"), c.detailFreq)
	gl.Uniform1f(p.U(prefix+".erosion"), c.erosion)
	uniformVec3(p.U(prefix+".windOffset"), c.windOffset)
	uniformVec3(p.U(prefix+".detailOffset"), c.detailOffset)
	uniformVec3(p.U(prefix+".albedo"), c.albedo)
	gl.Uniform1f(p.U(prefix+".absorption"), c.absorption)
	gl.Uniform1f(p.U(prefix+".ambient"), c.ambient)
}

func uniformVec3(loc int32, v mgl32.Vec3) { gl.Uniform3f(loc, v.X(), v.Y(), v.Z()) }

func bindTex2D(unit uint32, tex uint32) {
	gl.ActiveTexture(gl.TEXTURE0 + unit)
	gl.BindTexture(gl.TEXTURE_2D, tex)
}

func bindTex3D(unit uint32, tex uint32) {
	gl.ActiveTexture(gl.TEXTURE0 + unit)
	gl.BindTexture(gl.TEXTURE_3D, tex)
}

// volumeGLState is what the pass changes and has to hand back: the frame loop
// and ImGui both carry on drawing on the assumption nothing moved.
type volumeGLState struct {
	depthTest, blend, cull, depthClamp, a2c bool
	depthMask                               bool
	cullMode                                int32
	blendSrcRGB, blendDstRGB                int32
	blendSrcA, blendDstA                    int32
	program, vao                            int32
}

func saveVolumeGLState() volumeGLState {
	s := volumeGLState{
		depthTest:  gl.IsEnabled(gl.DEPTH_TEST),
		blend:      gl.IsEnabled(gl.BLEND),
		cull:       gl.IsEnabled(gl.CULL_FACE),
		depthClamp: gl.IsEnabled(gl.DEPTH_CLAMP),
		a2c:        gl.IsEnabled(gl.SAMPLE_ALPHA_TO_COVERAGE),
	}
	gl.GetBooleanv(gl.DEPTH_WRITEMASK, &s.depthMask)
	gl.GetIntegerv(gl.CULL_FACE_MODE, &s.cullMode)
	gl.GetIntegerv(gl.BLEND_SRC_RGB, &s.blendSrcRGB)
	gl.GetIntegerv(gl.BLEND_DST_RGB, &s.blendDstRGB)
	gl.GetIntegerv(gl.BLEND_SRC_ALPHA, &s.blendSrcA)
	gl.GetIntegerv(gl.BLEND_DST_ALPHA, &s.blendDstA)
	gl.GetIntegerv(gl.CURRENT_PROGRAM, &s.program)
	gl.GetIntegerv(gl.VERTEX_ARRAY_BINDING, &s.vao)
	return s
}

func setCap(c uint32, on bool) {
	if on {
		gl.Enable(c)
	} else {
		gl.Disable(c)
	}
}

func (s volumeGLState) restore(fbo uint32, vp [4]int32) {
	gl.BindFramebuffer(gl.FRAMEBUFFER, fbo)
	gl.Viewport(vp[0], vp[1], vp[2], vp[3])
	setCap(gl.DEPTH_TEST, s.depthTest)
	setCap(gl.BLEND, s.blend)
	setCap(gl.CULL_FACE, s.cull)
	setCap(gl.DEPTH_CLAMP, s.depthClamp)
	setCap(gl.SAMPLE_ALPHA_TO_COVERAGE, s.a2c)
	gl.DepthMask(s.depthMask)
	gl.CullFace(uint32(s.cullMode))
	gl.BlendFuncSeparate(uint32(s.blendSrcRGB), uint32(s.blendDstRGB), uint32(s.blendSrcA), uint32(s.blendDstA))
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindVertexArray(uint32(s.vao))
	gl.UseProgram(uint32(s.program))
}

// String is for the debug overlay.
func (s VolumetricSettings) String() string {
	shadows := "off"
	if s.CloudShadows {
		shadows = fmt.Sprintf("%dpx over %.0fu", s.CloudShadowSize*1, s.CloudShadowRange*2)
	}
	return fmt.Sprintf("%s, %d steps (%d to the sun), cloud shadows %s", VolumetricQualityName(), s.Steps, s.LightSteps, shadows)
}
