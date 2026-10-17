package gfx

// water_draw.go — a water surface's uniforms and textures on a world-program
// draw (docs/terrain-paint/PHASE_W1.md).
//
// Two kinds of state reach resources/shaders/water.glsl:
//
//   - PER DRAW, the material's water effect (WaterParams): the swell, the
//     colours, roughness, foam and the rest, packed into five vec4s beside the
//     five uniforms the effect has always had. Written by applyWaterUniforms
//     (the global program) and applyWaterLocs (any other copy), from
//     SurfaceState.Apply, on every water draw. A non-water draw writes only
//     isWater = 0, as it always did.
//   - PER FRAME, what render's water pass prepared (WaterFrame): copies of the
//     scene's colour and depth taken after the sky and before any water drew,
//     and the sky rendered into a small octahedral map. render/water_pass.go
//     fills WaterFrame and the frame uniforms (waterFrameFlags & co.) on the
//     global program; a program nothing prepared — the material editor's
//     preview, a test — reads zero flags and takes the shader's fallbacks.
//
// NO NEW SAMPLERS. The world program's fragment stage is at its sampler
// limit (32 on the GTX 1070 and on Mesa's d3d12: render's
// TestMaterialFxSamplersFitTheMaterialPass counts them), so the water path
// borrows samplers a water surface never uses for their own purpose:
//
//	blendTex  (unit 10) — the scene colour copy   (water takes no blend effect)
//	blendMask (unit 11) — the scene depth copy
//	normalMap (unit 12) — the sky's octahedral map (water derives its normal)
//	tex       (unit 0)  — the generated detail map (WaterDetailTexture), which
//	                      a water surface wears in place of its own texture
//	                      (SurfaceState.finishWater)
//
// water.glsl names them through #defines, so the borrowing is visible in one
// place on each side.

import (
	"github.com/go-gl/gl/v4.3-core/gl"
)

// The units the water path's borrowed samplers read; see the file comment.
const (
	WaterSceneColourUnit = BlendTexUnit
	WaterSceneDepthUnit  = BlendMaskUnit
	WaterSkyUnit         = NormalMapUnit
)

// WaterFrame is the water pass's per-frame textures (render/water_pass.go).
// Zero names are fine: the flags uniform says what the shader may read.
var WaterFrame struct {
	SceneColour, SceneDepth, Sky uint32
}

// WaterLocs is one program's water uniform locations.
type WaterLocs struct {
	isWater, waveAmp, waveLen, waveSpeed, reflectivity, reflectColor int32
	absorb, scatter, look, flow, swell                               int32
	flowMap                                                          int32
	blendTex, blendMask, normalMap                                   int32
}

// NewWaterLocs resolves prog's WaterLocs.
func NewWaterLocs(prog uint32) WaterLocs {
	loc := func(name string) int32 { return UniformLoc(prog, name+"\x00") }
	return WaterLocs{
		isWater: loc("isWater"), waveAmp: loc("waveAmplitude"), waveLen: loc("waveLength"),
		waveSpeed: loc("waveSpeed"), reflectivity: loc("reflectivity"), reflectColor: loc("reflectColor"),
		absorb: loc("waterAbsorb"), scatter: loc("waterScatter"), look: loc("waterLook"),
		flow: loc("waterFlow"), swell: loc("waterSwell"), flowMap: loc("waterFlowMap"),
		blendTex: loc("blendTex"), blendMask: loc("blendMask"), normalMap: loc("normalMap"),
	}
}

// waterLocCache is the global program's WaterLocs, dropped on any link.
var (
	waterLocCache    WaterLocs
	waterLocCacheFor uint32
	waterLocCacheGen uint64
)

// applyWaterUniforms pushes a surface's water settings to the global
// program. Every draw call sets isWater, including the disabled case, so a
// water plane never leaks its state onto the geometry drawn after it. Called
// from SurfaceState.Apply, the one place a world-program draw sets its
// per-surface uniforms; bindWaterTextures follows it there, after the blend
// pair and the normal map have been bound.
func applyWaterUniforms(w *WaterParams) {
	if waterLocCacheFor != Program || waterLocCacheGen != linkGeneration {
		waterLocCache, waterLocCacheFor, waterLocCacheGen = NewWaterLocs(Program), Program, linkGeneration
	}
	applyWaterLocs(&waterLocCache, w)
}

// applyWaterLocs is applyWaterUniforms through l (the program in use).
func applyWaterLocs(l *WaterLocs, w *WaterParams) {
	if !w.Enabled {
		gl.Uniform1i(l.isWater, 0)
		return
	}
	gl.Uniform1i(l.isWater, 1)
	gl.Uniform1f(l.waveAmp, w.WaveAmplitude)
	gl.Uniform1f(l.waveLen, w.WaveLength)
	gl.Uniform1f(l.waveSpeed, w.WaveSpeed)
	gl.Uniform1f(l.reflectivity, w.Reflectivity)
	gl.Uniform3f(l.reflectColor, w.ReflectColor.X(), w.ReflectColor.Y(), w.ReflectColor.Z())
	gl.Uniform4f(l.absorb, w.ShallowColor.X(), w.ShallowColor.Y(), w.ShallowColor.Z(), w.Absorption)
	gl.Uniform4f(l.scatter, w.ScatterColor.X(), w.ScatterColor.Y(), w.ScatterColor.Z(), w.Subsurface)
	gl.Uniform4f(l.look, w.Roughness, w.Refraction, w.Foam, w.ShoreFade)
	fx, fz := waterFlowVector(w)
	gl.Uniform4f(l.flow, fx, fz, w.DetailScale, w.DetailStrength)
	gl.Uniform4f(l.swell, w.Swell, w.SwellLength, w.Caustics, w.SSR)
	// The vertex-colour flow map (phase W3): its strength and the
	// two-phase advection's cycle, seconds; then (phase W4) the falling
	// sheet's strength and its streaks' speed, m/s.
	gl.Uniform4f(l.flowMap, w.FlowMap, WaterFlowCycle, w.Falls, w.FallsSpeed)
}

// WaterFlowCycle is the flow map's two-phase cycle (seconds): each copy of
// the ripples is carried this long before it fades out and restarts
// (resources/shaders/water.glsl, waterFlowMap.y).
const WaterFlowCycle = 1.6

// waterFlowVector is the ripples' drift in world X/Z, metres a second: the
// heading normalised, times the speed. A zero heading drifts along +X.
func waterFlowVector(w *WaterParams) (float32, float32) {
	d := w.FlowDirection
	if l := d.Len(); l > 1e-6 {
		d = d.Mul(1 / l)
	} else {
		d[0], d[1] = 1, 0
	}
	return d.X() * w.FlowSpeed, d.Y() * w.FlowSpeed
}

// bindWaterTextures binds the water pass's frame textures to the borrowed
// samplers' units and points those samplers there, for a water draw through
// l's program. A non-water draw leaves everything as the blend pair and the
// normal map set it.
func bindWaterTextures(l *WaterLocs, w *WaterParams) {
	if !w.Enabled {
		return
	}
	EnsureWaterDetailTexture()
	for _, b := range [3]struct {
		unit uint32
		tex  uint32
		loc  int32
	}{
		{WaterSceneColourUnit, WaterFrame.SceneColour, l.blendTex},
		{WaterSceneDepthUnit, WaterFrame.SceneDepth, l.blendMask},
		{WaterSkyUnit, WaterFrame.Sky, l.normalMap},
	} {
		gl.ActiveTexture(gl.TEXTURE0 + b.unit)
		gl.BindTexture(gl.TEXTURE_2D, b.tex)
		gl.Uniform1i(b.loc, int32(b.unit))
	}
	gl.ActiveTexture(gl.TEXTURE0)
}

// bindWaterTexturesGlobal is bindWaterTextures through the global program.
func bindWaterTexturesGlobal(w *WaterParams) {
	if !w.Enabled {
		return
	}
	if waterLocCacheFor != Program || waterLocCacheGen != linkGeneration {
		waterLocCache, waterLocCacheFor, waterLocCacheGen = NewWaterLocs(Program), Program, linkGeneration
	}
	bindWaterTextures(&waterLocCache, w)
}

// finishWater is the last step of a surface state's assembly: a water
// surface wears the generated detail map as its albedo (the water path reads
// its ripples and foam from `tex`; see the file comment) and is drawn in the
// water pass, never the opaque one (IsTransparent).
func (s *SurfaceState) finishWater() {
	if s.Water.Enabled {
		s.Texture = WaterDetailTexture
		s.NormalMap = WaterDetailTexture
	}
}

// CastsShadow reports whether g goes into the shadow maps: not when it is too
// faint to see (alpha under 0.05, the rule the shadow passes always had), and
// never when it is water — a water surface lets the sun through to the bed,
// where its caustics play, rather than shading the whole lake floor.
func (g *Geometry) CastsShadow() bool {
	if g.Alpha < 0.05 {
		return false
	}
	return !g.SurfaceState().Water.Enabled
}
