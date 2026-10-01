package render

// cloud_shadow.go — the shadow clouds cast on the world.
//
// A cloud cannot go in the cascaded shadow maps: those hold one depth per
// texel, "lit or not", and a cloud is neither — it is a partial, soft, moving
// dimming that depends on how much of it the sunlight crossed. So it gets a map
// of its own, baked once a frame by marching every shadow-casting cloud along
// the sun from above (cloud_shadow_bake_frag.glsl), and read by every stage
// that lights world geometry through cloud_shadow.glsl.
//
// The map is a square of CloudShadowRange either side of the camera, in the
// sun's own orthographic view, snapped to whole texels so it does not swim as
// the camera moves — the same snapping CascadeLightMatrix does for the
// cascades, and for the same shimmer.
//
// The uniforms ride on UploadShadowUniformsTo. Every program that includes
// cloud_shadow.glsl already calls it once a frame, so that is the one call that
// cannot be forgotten, and it is also what guarantees the sampler is always
// bound to a real texture — a white dummy on a frame with no clouds.

import (
	"fmt"
	"foundry/internal/world"
	"log"
	"math"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// CloudShadowTexUnit is the unit cloudShadowMap binds to in every lighting
// program: past the albedo (0), the cascade array (1), the point-light cubes
// (2..9), the blend pair (10, 11) and the normal map (12). A sampler2D on a
// unit of its own, so it can never share one with a differently-typed sampler.
const CloudShadowTexUnit = 13

// MaxShadowClouds is how many clouds one bake marches. MUST match
// MAX_SHADOW_CLOUDS in cloud_shadow_bake_frag.glsl. The deck, when it casts,
// takes the first slot; volumes nearest the camera take the rest.
const MaxShadowClouds = 8

type cloudShadowState struct {
	fbo, tex uint32
	size     int32
	dummy    uint32
	broken   bool

	on     bool
	light  int32
	matrix mgl32.Mat4
}

var cloudShadow cloudShadowState

// CloudShadowActive reports whether this frame has a cloud shadow map.
func CloudShadowActive() bool { return cloudShadow.on }

// RenderCloudShadowMap bakes this frame's map. Called from the frame loop after
// the cascade pass and before the shadow uniforms are uploaded, with the main
// context current and its framebuffer bound; it hands both back.
func RenderCloudShadowMap(lvl *world.Level, camPos mgl32.Vec3) {
	cloudShadow.on = false
	if !volumes.ready || !Volumetrics.Enabled || !Volumetrics.CloudShadows || lvl == nil || cloudShadow.broken {
		return
	}
	sunIdx, toSun, _, ok := volumeSun(lvl.Lights)
	if !ok {
		return
	}

	var params []cloudParams
	var bounds [][2]mgl32.Vec3
	R := Volumetrics.CloudShadowRange
	if deck, ok := lvl.Sky.EffectiveClouds(); ok && deck.CastShadows {
		params = append(params, deckCloudParams(deck, VolumeTime))
		// The deck is infinite; the part that matters is the square above the
		// map, widened by how far a slanting sun carries its shadow sideways.
		reach := R*1.5 + deck.Altitude + deck.Thickness
		bounds = append(bounds, [2]mgl32.Vec3{
			{camPos.X() - reach, deck.Altitude, camPos.Z() - reach},
			{camPos.X() + reach, deck.Altitude + deck.Thickness, camPos.Z() + reach},
		})
	}
	for _, v := range world.CloudVolumes(lvl.Volumes) {
		if len(params) >= MaxShadowClouds {
			break
		}
		if !v.Cloud.CastShadows {
			continue
		}
		lo, hi, ok := v.WorldAABB(lvl)
		if !ok {
			continue
		}
		params = append(params, volumeCloudParams(v, lvl, VolumeTime))
		bounds = append(bounds, [2]mgl32.Vec3{lo, hi})
	}
	if len(params) == 0 {
		return
	}

	size := Volumetrics.CloudShadowSize
	if size < 16 {
		size = 16
	}
	if !ensureCloudShadowTarget(size) {
		return
	}

	lvp, texel := cloudShadowLightViewProj(toSun.Mul(-1), camPos, R, size, bounds)
	bias := mgl32.Translate3D(0.5, 0.5, 0.5).Mul4(mgl32.Scale3D(0.5, 0.5, 0.5))
	cloudShadow.matrix = bias.Mul4(lvp)
	cloudShadow.light = int32(sunIdx)

	vp := boundViewport()
	boundFBO := boundFramebuffer()
	state := currentGLState()
	defer state.restore(boundFBO, vp)

	gl.BindFramebuffer(gl.FRAMEBUFFER, cloudShadow.fbo)
	gl.Viewport(0, 0, size, size)
	gl.Disable(gl.DEPTH_TEST)
	gl.Disable(gl.BLEND)
	gl.Disable(gl.CULL_FACE)
	gl.DepthMask(false)
	gl.BindVertexArray(MainVolumeTargets.ensureVAO())

	b := &volumes.bake
	gl.UseProgram(b.prog)
	bindTex3D(volUnitShape, volumes.shapeTex)
	bindTex3D(volUnitDetail, volumes.detailTex)
	gl.Uniform1i(b.U("volNoiseShape"), volUnitShape)
	gl.Uniform1i(b.U("volNoiseDetail"), volUnitDetail)
	inv := lvp.Inv()
	gl.UniformMatrix4fv(b.U("bakeInvLightViewProj"), 1, false, &inv[0])
	gl.Uniform2f(b.U("bakeTargetSize"), float32(size), float32(size))
	gl.Uniform1f(b.U("bakeTexelWorld"), texel)
	gl.Uniform1i(b.U("volFullscreen"), 1)
	gl.Uniform1i(b.U("numShadowClouds"), int32(len(params)))
	for i := range params {
		uploadCloudParams(b, fmt.Sprintf("shadowClouds[%d]", i), &params[i])
	}
	gl.DrawArrays(gl.TRIANGLES, 0, 3)

	cloudShadow.on = true
}

// cloudShadowLightViewProj builds the bake's orthographic view along the
// light's travel direction d: a 2R square centred on the camera and snapped to
// whole texels, deep enough to hold every cloud's bounds from the sun side, so
// light depth 0 is above all of them.
func cloudShadowLightViewProj(d, camPos mgl32.Vec3, R float32, size int32, bounds [][2]mgl32.Vec3) (mgl32.Mat4, float32) {
	d = d.Normalize()
	up := mgl32.Vec3{0, 1, 0}
	if math.Abs(float64(d.Y())) > 0.99 {
		up = mgl32.Vec3{0, 0, 1}
	}
	right := d.Cross(up).Normalize()
	up = right.Cross(d).Normalize()

	texel := 2 * R / float32(size)
	snap := func(v float32) float32 { return float32(math.Floor(float64(v/texel))) * texel }
	center := right.Mul(snap(camPos.Dot(right))).
		Add(up.Mul(snap(camPos.Dot(up)))).
		Add(d.Mul(camPos.Dot(d)))

	sMin, sMax := float32(math.Inf(1)), float32(math.Inf(-1))
	for _, bb := range bounds {
		for c := 0; c < 8; c++ {
			corner := mgl32.Vec3{bb[c&1][0], bb[(c>>1)&1][1], bb[(c>>2)&1][2]}
			s := corner.Sub(center).Dot(d)
			sMin = float32(math.Min(float64(sMin), float64(s)))
			sMax = float32(math.Max(float64(sMax), float64(s)))
		}
	}
	const margin = 1
	eye := center.Add(d.Mul(sMin - margin))
	far := sMax - sMin + 2*margin
	view := mgl32.LookAtV(eye, eye.Add(d), up)
	proj := mgl32.Ortho(-R, R, -R, R, 0, far)
	return proj.Mul4(view), texel
}

func ensureCloudShadowTarget(size int32) bool {
	if cloudShadow.fbo != 0 && cloudShadow.size == size {
		return true
	}
	if cloudShadow.fbo != 0 {
		gl.DeleteFramebuffers(1, &cloudShadow.fbo)
		gl.DeleteTextures(1, &cloudShadow.tex)
	}
	cloudShadow.size = size
	gl.GenTextures(1, &cloudShadow.tex)
	gl.BindTexture(gl.TEXTURE_2D, cloudShadow.tex)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RG16F, size, size, 0, gl.RG, gl.FLOAT, nil)
	// Linear, so a cloud's shadow is soft-edged between texels rather than a
	// pattern of squares a few metres wide.
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	// Put back whatever was bound: the pass reads the frame's target after
	// this, and on the frame that made the map it used to read this one —
	// and hand the rest of that frame to the cloud shadow.
	prev := boundFramebuffer()
	defer gl.BindFramebuffer(gl.FRAMEBUFFER, prev)
	gl.GenFramebuffers(1, &cloudShadow.fbo)
	gl.BindFramebuffer(gl.FRAMEBUFFER, cloudShadow.fbo)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, cloudShadow.tex, 0)
	gl.DrawBuffer(gl.COLOR_ATTACHMENT0)
	if s := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); s != gl.FRAMEBUFFER_COMPLETE {
		log.Printf("cloud shadows: framebuffer incomplete (0x%x) — cloud shadows disabled", s)
		gl.DeleteFramebuffers(1, &cloudShadow.fbo)
		gl.DeleteTextures(1, &cloudShadow.tex)
		cloudShadow.fbo, cloudShadow.tex, cloudShadow.broken = 0, 0, true
		return false
	}
	return true
}

// ensureVAO makes the target set's vertex array if it has none, and returns it.
func (t *VolumeTargets) ensureVAO() uint32 {
	if t.vao == 0 {
		gl.GenVertexArrays(1, &t.vao)
	}
	return t.vao
}

// uploadCloudShadowTo binds the cloud shadow uniforms on prog, which must be
// bound. A program without the lookup is left alone.
func uploadCloudShadowTo(prog uint32) {
	loc := func(name string) int32 { return world.ProgUniformLoc(prog, name) }
	sampler := loc("cloudShadowMap\x00")
	if sampler < 0 {
		return
	}
	if cloudShadow.dummy == 0 {
		gl.GenTextures(1, &cloudShadow.dummy)
		gl.BindTexture(gl.TEXTURE_2D, cloudShadow.dummy)
		white := []uint8{255, 255, 255, 255}
		gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, 1, 1, 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(white))
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
	}
	gl.ActiveTexture(gl.TEXTURE0 + CloudShadowTexUnit)
	on := int32(0)
	if cloudShadow.on {
		on = 1
		gl.BindTexture(gl.TEXTURE_2D, cloudShadow.tex)
	} else {
		gl.BindTexture(gl.TEXTURE_2D, cloudShadow.dummy)
	}
	gl.Uniform1i(sampler, CloudShadowTexUnit)
	gl.Uniform1i(loc("cloudShadowOn\x00"), on)
	gl.Uniform1i(loc("cloudShadowLight\x00"), cloudShadow.light)
	m := cloudShadow.matrix
	if m == (mgl32.Mat4{}) {
		m = mgl32.Ident4()
	}
	gl.UniformMatrix4fv(loc("cloudShadowMatrix\x00"), 1, false, &m[0])
}
