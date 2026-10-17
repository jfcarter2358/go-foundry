package render

// envmap.go — the environment every lit surface reflects (docs/lighting/
// PLAN.md, phase 2; specular.glsl's pbrEnvSpecular).
//
// One RGBA16F cube-map array, envCubes on EnvTexUnit in every world program:
//
//	cube 0      the sky — sky_frag.glsl drawn into a cube from the origin,
//	            again whenever the sky may have changed (a transition, the
//	            frame switching between HDR and LDR, every envSkyPeriod
//	            frames for anything else that moves it)
//	cube 1..8   the reflection volumes nearest the camera (world/
//	            volume_reflection.go), each traced through the GI distance
//	            field from its centre (env_capture_comp.glsl), one volume a
//	            frame round the set while GI runs
//
// Each cube is GGX-prefiltered into its mips (env_prefilter_comp.glsl): mip m
// is roughness m / (envMips - 1), the split sum's first half; the second half
// is specular.glsl's analytic fit, no lookup table.
//
// A capture is drawn into envRaw, a cube with a full mip chain of its own
// that the prefilter samples (filtered importance sampling needs the chain),
// and from there into the array. With the player's reflections off
// (gfx.Reflections) nothing here runs and the scene block's envOn is 0.

import (
	"log"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

const (
	// envSize is each cube's face, in texels.
	envSize = 128
	// envMips is the mips the array keeps: 128 down to 4, roughness 0 to 1.
	envMips = 6
	// envRawMips is the capture's full chain (128 → 1).
	envRawMips = 8
	// envCubes is the array's cubes: the sky and the reflection volumes.
	envCubes = 1 + world.MaxReflectionVolumes
	// envSkyPeriod is how many frames the sky's cube stands before it is
	// drawn again unasked.
	envSkyPeriod = 60
	// envPrefilterSamples is each prefiltered texel's GGX samples.
	envPrefilterSamples = 48
)

// EnvTexUnit is the unit the environment array binds to in a world program:
// one of the seven the point shadows' cubes gave back (shadow.go).
const EnvTexUnit = 3

type envSlot struct {
	vol      *world.Volume
	center   mgl32.Vec3
	captured bool
}

type envMapState struct {
	failed bool
	built  bool

	array, raw, fbo, dummy uint32
	prefilter, capture     progUniforms

	lvl        *world.Level
	skyFrame   int
	skyHDR     int32
	skyValid   bool
	frame      int
	slots      [world.MaxReflectionVolumes]envSlot
	nextVolume int
	active     bool
}

var envMap envMapState

// envBuild makes the textures and programs, once.
func (e *envMapState) build() bool {
	if e.failed {
		return false
	}
	if e.built {
		return true
	}
	var err error
	e.prefilter, err = giComputeProgram(giShaderSource("env_prefilter_comp.glsl", ""))
	if err != nil {
		log.Printf("env: prefilter shader: %v — reflections are off", err)
		e.failed = true
		return false
	}
	e.capture, err = giComputeProgram(giShaderSource("env_capture_comp.glsl", "",
		"gi_sdf_common.glsl", "light_model.glsl"))
	if err != nil {
		log.Printf("env: capture shader: %v — reflection volumes reflect the sky", err)
		e.capture = progUniforms{}
	}

	gl.GenTextures(1, &e.array)
	gl.BindTexture(gl.TEXTURE_CUBE_MAP_ARRAY, e.array)
	gl.TexStorage3D(gl.TEXTURE_CUBE_MAP_ARRAY, envMips, gl.RGBA16F, envSize, envSize, 6*envCubes)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP_ARRAY, gl.TEXTURE_MIN_FILTER, gl.LINEAR_MIPMAP_LINEAR)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP_ARRAY, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP_ARRAY, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP_ARRAY, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP_ARRAY, gl.TEXTURE_WRAP_R, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP_ARRAY, gl.TEXTURE_MAX_LEVEL, envMips-1)
	gl.BindTexture(gl.TEXTURE_CUBE_MAP_ARRAY, 0)

	gl.GenTextures(1, &e.raw)
	gl.BindTexture(gl.TEXTURE_CUBE_MAP, e.raw)
	gl.TexStorage2D(gl.TEXTURE_CUBE_MAP, envRawMips, gl.RGBA16F, envSize, envSize)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_MIN_FILTER, gl.LINEAR_MIPMAP_LINEAR)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_WRAP_R, gl.CLAMP_TO_EDGE)
	gl.BindTexture(gl.TEXTURE_CUBE_MAP, 0)

	gl.GenFramebuffers(1, &e.fbo)
	e.built = true
	return true
}

// envDummy is a 1×1 black cube-map array for envCubes while there is no
// environment, so the sampler never rests on a unit of another type.
func envDummy() uint32 {
	if envMap.dummy == 0 {
		gl.GenTextures(1, &envMap.dummy)
		gl.BindTexture(gl.TEXTURE_CUBE_MAP_ARRAY, envMap.dummy)
		px := [6 * 4]float32{}
		gl.TexImage3D(gl.TEXTURE_CUBE_MAP_ARRAY, 0, gl.RGBA16F, 1, 1, 6, 0, gl.RGBA, gl.FLOAT, gl.Ptr(&px[0]))
		gl.TexParameteri(gl.TEXTURE_CUBE_MAP_ARRAY, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
		gl.TexParameteri(gl.TEXTURE_CUBE_MAP_ARRAY, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
		gl.BindTexture(gl.TEXTURE_CUBE_MAP_ARRAY, 0)
	}
	return envMap.dummy
}

// envWanted is whether this frame reflects an environment.
func envWanted(lvl *world.Level, o WorldDraw) bool {
	return lvl != nil && gfx.Reflections != gfx.ReflectionsOff && !o.Wireframe
}

// UpdateEnvMaps brings the environment up to date for the frame. Called from
// DrawWorld after UpdateGI (a volume's capture traces GI's field and reads its
// probes) and before any program's lighting uniforms go up.
func UpdateEnvMaps(lvl *world.Level, o WorldDraw) {
	e := &envMap
	b := &gfx.Scene.Block
	e.active = false
	if !envWanted(lvl, o) || !e.build() {
		packEnvOff(b)
		return
	}
	if lvl != e.lvl {
		e.lvl = lvl
		e.skyValid = false
		e.slots = [world.MaxReflectionVolumes]envSlot{}
	}
	e.frame++

	// The state a capture changes, read only when one runs: glIsEnabled
	// every frame was a driver round trip the adventure level's frame paid
	// ~1.8 ms of CPU for on the GTX 1070 (docs/adventure/PHASE_F.md), and on
	// most frames nothing is captured.
	var prevFBO uint32
	var prevViewport [4]int32
	var depthWas, cullWas, saved bool
	save := func() {
		if saved {
			return
		}
		saved = true
		prevFBO = boundFramebuffer()
		prevViewport = boundViewport()
		depthWas = gl.IsEnabled(gl.DEPTH_TEST)
		cullWas = gl.IsEnabled(gl.CULL_FACE)
	}

	// The sky.
	hdr := b.HdrMode
	transition := lvl.Sky != nil && lvl.Sky.TransitionFrom != nil
	if !e.skyValid || hdr != e.skyHDR || transition || e.frame-e.skyFrame >= envSkyPeriod {
		save()
		e.captureSky(lvl)
		e.prefilterInto(0)
		e.skyValid, e.skyHDR, e.skyFrame = true, hdr, e.frame
	}

	// The reflection volumes: their slots, kept for as long as each stays
	// among the nearest, and one capture a frame round them while GI's field
	// is there to trace.
	boxes := world.ReflectionVolumes(lvl.Volumes, lvl, gfx.Cam.Position)
	e.assignSlots(boxes)
	if gi.active && e.capture.prog != 0 {
		if s := e.pickCapture(); s >= 0 {
			save()
			e.captureVolume(lvl, s)
			e.prefilterInto(s + 1)
			e.slots[s].captured = true
		}
	}

	if saved {
		gl.BindFramebuffer(gl.FRAMEBUFFER, prevFBO)
		gl.Viewport(prevViewport[0], prevViewport[1], prevViewport[2], prevViewport[3])
		if depthWas {
			gl.Enable(gl.DEPTH_TEST)
		}
		if cullWas {
			gl.Enable(gl.CULL_FACE)
		}
	}
	if gfx.Program != 0 {
		gl.UseProgram(gfx.Program)
	}

	e.active = true
	e.pack(b, lvl)
}

// captureSky draws the sky into envRaw's six faces and fills its mips. A
// level with no sky is its clear colour all round, which is what shows behind
// it.
func (e *envMapState) captureSky(lvl *world.Level) {
	gl.BindFramebuffer(gl.FRAMEBUFFER, e.fbo)
	gl.Viewport(0, 0, envSize, envSize)
	gl.Disable(gl.DEPTH_TEST)
	gl.Disable(gl.CULL_FACE)
	proj := mgl32.Perspective(mgl32.DegToRad(90), 1, 0.1, 10)
	views := cubeFaceViews(mgl32.Vec3{})
	clear := WorldClearColor(lvl)
	for face := 0; face < 6; face++ {
		gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0,
			uint32(gl.TEXTURE_CUBE_MAP_POSITIVE_X+face), e.raw, 0)
		gl.DrawBuffer(gl.COLOR_ATTACHMENT0)
		gl.ClearColor(clear.X(), clear.Y(), clear.Z(), 1)
		gl.Clear(gl.COLOR_BUFFER_BIT)
		if skyRender.initialized && lvl.Sky != nil {
			bindSkyLook(&skyRender.prog, lvl, views[face], proj)
			gl.BindVertexArray(skyRender.vao)
			gl.DrawArrays(gl.TRIANGLES, 0, int32(len(skyCubeVerts)/3))
			gl.BindVertexArray(0)
		}
	}
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_CUBE_MAP_POSITIVE_X, 0, 0)
	ApplyWorldClearColor(lvl)
	gl.BindTexture(gl.TEXTURE_CUBE_MAP, e.raw)
	gl.GenerateMipmap(gl.TEXTURE_CUBE_MAP)
	gl.BindTexture(gl.TEXTURE_CUBE_MAP, 0)
}

// captureVolume traces slot s's volume's surroundings into envRaw.
func (e *envMapState) captureVolume(lvl *world.Level, s int) {
	p := &e.capture
	gi.uploadProbeUniformsTo(p)
	gi.field.uploadSdfUniformsTo(p, giSdfUnitBase)
	lights := world.SceneLights(lvl)
	world.UploadLightsTo(p.prog, lights, false)
	gi.uploadTraceLights(p, lights)
	gl.UseProgram(p.prog)
	c := e.slots[s].center
	gl.Uniform3f(p.U("captureCenter"), c[0], c[1], c[2])
	gl.Uniform1i(p.U("size"), envSize)
	gl.Uniform1i(p.U("frameIndex"), int32(e.frame))
	gl.Uniform1f(p.U("maxDistance"), gi.cascadeSpacing(max(gi.t.cascades-1, 0))*16)
	gl.ActiveTexture(gl.TEXTURE0 + lightingComputeUnit)
	gl.BindTexture(gl.TEXTURE_CUBE_MAP_ARRAY, e.array)
	gl.Uniform1i(p.U("skyEnv"), lightingComputeUnit)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindImageTexture(0, e.raw, 0, true, 0, gl.WRITE_ONLY, gl.RGBA16F)
	gl.DispatchCompute((envSize+7)/8, (envSize+7)/8, 6)
	gl.BindImageTexture(0, 0, 0, false, 0, gl.READ_ONLY, gl.RGBA16F)
	gl.MemoryBarrier(gl.SHADER_IMAGE_ACCESS_BARRIER_BIT | gl.TEXTURE_FETCH_BARRIER_BIT | gl.TEXTURE_UPDATE_BARRIER_BIT)
	gl.BindTexture(gl.TEXTURE_CUBE_MAP, e.raw)
	gl.GenerateMipmap(gl.TEXTURE_CUBE_MAP)
	gl.BindTexture(gl.TEXTURE_CUBE_MAP, 0)
}

// prefilterInto fills cube c of the array, every mip, from envRaw.
func (e *envMapState) prefilterInto(c int) {
	p := &e.prefilter
	gl.UseProgram(p.prog)
	gl.ActiveTexture(gl.TEXTURE0 + lightingComputeUnit + 1)
	gl.BindTexture(gl.TEXTURE_CUBE_MAP, e.raw)
	gl.Uniform1i(p.U("srcEnv"), lightingComputeUnit+1)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.Uniform1i(p.U("cube"), int32(c))
	gl.Uniform1f(p.U("srcSize"), envSize)
	gl.Uniform1i(p.U("sampleCount"), envPrefilterSamples)
	for m := 0; m < envMips; m++ {
		size := int32(envSize >> m)
		gl.BindImageTexture(0, e.array, int32(m), true, 0, gl.WRITE_ONLY, gl.RGBA16F)
		gl.Uniform1i(p.U("size"), size)
		gl.Uniform1f(p.U("roughness"), float32(m)/float32(envMips-1))
		gl.DispatchCompute(uint32((size+7)/8), uint32((size+7)/8), 6)
	}
	gl.BindImageTexture(0, 0, 0, false, 0, gl.READ_ONLY, gl.RGBA16F)
	gl.MemoryBarrier(gl.SHADER_IMAGE_ACCESS_BARRIER_BIT | gl.TEXTURE_FETCH_BARRIER_BIT | gl.TEXTURE_UPDATE_BARRIER_BIT)
}

// assignSlots keeps each volume still among the nearest in the slot it had,
// and gives the newcomers the slots that freed up, uncaptured.
func (e *envMapState) assignSlots(boxes []world.ReflectionBox) {
	keep := map[*world.Volume]bool{}
	for _, b := range boxes {
		keep[b.Volume] = true
	}
	for i := range e.slots {
		if e.slots[i].vol != nil && !keep[e.slots[i].vol] {
			e.slots[i] = envSlot{}
		}
	}
	for _, b := range boxes {
		found := false
		for i := range e.slots {
			if e.slots[i].vol == b.Volume {
				if e.slots[i].center != b.Center {
					// Moved: its old capture is of somewhere else.
					e.slots[i].center, e.slots[i].captured = b.Center, false
				}
				found = true
				break
			}
		}
		if found {
			continue
		}
		for i := range e.slots {
			if e.slots[i].vol == nil {
				e.slots[i] = envSlot{vol: b.Volume, center: b.Center}
				break
			}
		}
	}
}

// envRecapturePeriod is how many frames apart the volumes already captured
// are traced again, one a time round the set: often enough that a light
// switching on shows in a reflection within a few frames, rarely enough that
// the trace is not paid every frame.
const envRecapturePeriod = 4

// pickCapture is the slot to capture this frame: one never captured first,
// else, every envRecapturePeriod frames, the next round the set. -1 for none.
func (e *envMapState) pickCapture() int {
	for i := range e.slots {
		if e.slots[i].vol != nil && !e.slots[i].captured {
			return i
		}
	}
	if e.frame%envRecapturePeriod != 0 {
		return -1
	}
	for k := 0; k < len(e.slots); k++ {
		i := (e.nextVolume + k) % len(e.slots)
		if e.slots[i].vol != nil {
			e.nextVolume = i + 1
			return i
		}
	}
	return -1
}

// SSRTexUnit is the unit the last frame's picture binds to for the
// screen-space reflections (specular.glsl's ssrTrace).
const SSRTexUnit = 5

// ssrReady is whether this frame can trace the screen: the high tier, and a
// last frame with a picture and the occlusion pass's distances to march.
func ssrReady() bool {
	return gfx.Reflections == gfx.ReflectionsHigh && taa.active && taa.on && taa.historyValid && aoPass.valid
}

// pack writes the environment's members of the scene block.
func (e *envMapState) pack(b *gfx.SceneBlock, lvl *world.Level) {
	b.EnvOn = 1
	if ssrReady() {
		b.EnvOn = 2
	}
	b.EnvMaxMip = envMips - 1
	b.EnvIntensity = 1
	b.EnvGINormalize = 0
	if gi.active {
		b.EnvGINormalize = 1
	}
	n := 0
	for i := range e.slots {
		s := &e.slots[i]
		if s.vol == nil || !s.captured {
			continue
		}
		lo, hi, ok := s.vol.WorldAABB(lvl)
		if !ok {
			continue
		}
		r := s.vol.Reflection
		b.ReflVolCenter[n] = [4]float32{s.center[0], s.center[1], s.center[2], float32(i + 1)}
		b.ReflVolMin[n] = [4]float32{lo[0], lo[1], lo[2], r.Fade}
		b.ReflVolMax[n] = [4]float32{hi[0], hi[1], hi[2], r.Intensity}
		n++
	}
	b.NumReflVolumes = int32(n)
	for i := n; i < world.MaxReflectionVolumes; i++ {
		b.ReflVolCenter[i], b.ReflVolMin[i], b.ReflVolMax[i] = [4]float32{}, [4]float32{}, [4]float32{}
	}
	// Not flushed here: UploadShadowUniforms flushes the block before anything
	// draws with it, and a flush between GI's dispatches and the frame's draws
	// is one more write into a buffer the GPU may be reading.
}

// packEnvOff writes "no environment" into the scene block.
func packEnvOff(b *gfx.SceneBlock) {
	b.EnvOn, b.NumReflVolumes, b.EnvGINormalize = 0, 0, 0
}

// uploadEnvTo binds the environment array (or the dummy) to prog's envCubes,
// and the occlusion texture (ao.go) to its ssaoTex. Called from
// UploadShadowUniformsTo beside uploadGITo, which every program that lights a
// surface already calls.
func uploadEnvTo(prog uint32) {
	uploadAOTo(prog)
	uploadLightmapsTo(prog)
	// The last frame's picture for the screen-space reflections: TAA's
	// history, which this frame's resolve does not write (it writes the
	// other one).
	hist := aoDummy()
	if ssrReady() {
		hist = taa.history[taa.cur]
	}
	gl.UseProgram(prog)
	gl.ActiveTexture(gl.TEXTURE0 + SSRTexUnit)
	gl.BindTexture(gl.TEXTURE_2D, hist)
	gl.Uniform1i(gfx.UniformLocName(prog, "ssrColor"), SSRTexUnit)
	gl.ActiveTexture(gl.TEXTURE0)
	tex := envMap.array
	if !envMap.active || tex == 0 {
		tex = envDummy()
	}
	gl.UseProgram(prog)
	gl.ActiveTexture(gl.TEXTURE0 + EnvTexUnit)
	gl.BindTexture(gl.TEXTURE_CUBE_MAP_ARRAY, tex)
	gl.Uniform1i(gfx.UniformLocName(prog, "envCubes"), EnvTexUnit)
	gl.ActiveTexture(gl.TEXTURE0)
}
