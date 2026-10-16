package render

// exposure.go — automatic exposure (docs/lighting/PLAN.md, phase 4): the
// eye's adaptation, for every level that shades in HDR without fixing its
// exposure (world/tonemap.go).
//
// Each scene-target frame the resolved picture is metered and the exposure
// adapted on the GPU (exposure_comp.glsl), in a small buffer the frame's
// output (taa_output_frag.glsl) reads it from. The CPU never reads it back:
// a synchronous readback every frame is what Mesa's D3D12 driver — the GL a
// WSL editor draws with — deadlocks on (glext/readback.go).
//
// So the CPU's exposure (gfx.AutoExposure, in the scene block as hdrExposure)
// stays at the reference, gfx.DefaultAutoExposure: what the display-referred
// colours (the sky, unlit surfaces) are carried into the scene's units by.
// At a daylit level's equilibrium the adapted exposure is about that, and
// they show as authored; in a cave, where the eye has opened up, the sky
// through its mouth shows brighter, as it does to an eye.

import (
	"log"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
)

const (
	// exposureKey is the scene luminance the metered geometric mean is
	// brought to: under the ACES curve, about where an ordinary daylit
	// level's LDR shading put its mean on screen.
	exposureKey = 0.13
	// The adapted exposure's range: a cave is brightened, a snowfield
	// darkened, but neither all the way.
	exposureMin = 0.4
	exposureMax = 1.8
	// exposureAdapt is how much of the way the eye adapts: the exposure
	// follows (key / mean) to this power, so a dark room is brightened
	// rather than brought up to daylight — its author lit it dark — and a
	// daylit level's mean lands where its LDR shading had it.
	exposureAdapt = 0.6
	// How fast the exposure follows the meter, per second, in each direction.
	exposureSpeedUp   = 1.2 // brightening, coming in from the light
	exposureSpeedDown = 3.0 // darkening, walking out into it

	// ExposureBinding is the shader-storage binding the adapted exposure is
	// read from by the frame's output.
	ExposureBinding = 0
)

type exposureState struct {
	failed, built bool
	prog          progUniforms
	buf           uint32

	lvlName string
	lastNow float64
	reset   bool
	// metered: the buffer holds an adapted exposure the output may use.
	metered bool
}

var exposure exposureState

func (e *exposureState) build() bool {
	if e.failed {
		return false
	}
	if e.built {
		return true
	}
	var err error
	if e.prog, err = giComputeProgram(giShaderSource("exposure_comp.glsl", "")); err != nil {
		log.Printf("exposure: %v — the exposure stays where it is", err)
		e.failed = true
		return false
	}
	gl.GenBuffers(1, &e.buf)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, e.buf)
	init := [4]float32{gfx.DefaultAutoExposure, 0, 0, 0}
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, 16, gl.Ptr(&init[0]), gl.DYNAMIC_COPY)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	e.built, e.reset = true, true
	return true
}

// meterExposure meters src (the resolved picture) and adapts the exposure in
// the GPU's buffer. Called from sceneFrameEnd in an HDR frame.
func meterExposure(src uint32, lvl *world.Level, now float64) {
	e := &exposure
	if !e.build() {
		return
	}
	name := ""
	if lvl != nil {
		name = lvl.Name
	}
	if name != e.lvlName {
		// A new level: its first reading taken outright, not eased into from
		// the last level's adaptation.
		e.lvlName, e.reset = name, true
	}
	dt := float32(now - e.lastNow)
	e.lastNow = now
	if dt < 0 || dt > 0.25 {
		dt = 0.25
	}
	p := &e.prog
	gl.UseProgram(p.prog)
	gl.ActiveTexture(gl.TEXTURE0 + lightingComputeUnit)
	gl.BindTexture(gl.TEXTURE_2D, src)
	gl.Uniform1i(p.U("meterSrc"), lightingComputeUnit)
	dist := aoDummy()
	if aoPass.valid {
		dist = aoPass.final
	}
	gl.ActiveTexture(gl.TEXTURE0 + lightingComputeUnit + 1)
	gl.BindTexture(gl.TEXTURE_2D, dist)
	gl.Uniform1i(p.U("meterDist"), lightingComputeUnit+1)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.Uniform1i(p.U("meterUseDist"), boolI32(aoPass.valid))
	gl.Uniform1f(p.U("expDt"), dt)
	gl.Uniform1f(p.U("expKey"), exposureKey)
	gl.Uniform1f(p.U("expAdapt"), exposureAdapt)
	gl.Uniform1f(p.U("expMin"), exposureMin)
	gl.Uniform1f(p.U("expMax"), exposureMax)
	gl.Uniform1f(p.U("expSpeedUp"), exposureSpeedUp)
	gl.Uniform1f(p.U("expSpeedDown"), exposureSpeedDown)
	gl.Uniform1i(p.U("expReset"), boolI32(e.reset))
	gl.Uniform1f(p.U("expStart"), gfx.DefaultAutoExposure)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 0, e.buf)
	gl.DispatchCompute(1, 1, 1)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 0, 0)
	gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT)
	e.reset, e.metered = false, true
}

// bindExposureTo hands the frame's output the adapted exposure: the buffer
// on ExposureBinding, and whether to use it (auto) — and by how much over the
// reference the scene block's hdrExposure carries (the level's compensation).
func bindExposureTo(p *progUniforms, auto bool) {
	use := auto && exposure.metered && exposure.buf != 0
	gl.Uniform1i(p.U("autoExposure"), boolI32(use))
	if use {
		gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, ExposureBinding, exposure.buf)
		gl.Uniform1f(p.U("exposureReference"), gfx.DefaultAutoExposure)
	}
}
