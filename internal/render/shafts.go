package render

// shafts.go — light shafts: a shadowed in-scattering march through dusty air
// near the eye (shafts_frag.glsl; world/fog_shafts.go; docs/adventure/PHASE_B.md).
//
// The volumetric pass (volumetric.go) already has what the march needs: the
// scene's distances at its own resolution (half the frame's, by default), an
// RGBA16F target composited over the frame premultiplied, and a depth-aware
// upsample. The shafts are one more fullscreen draw into that target, first,
// so the clouds and fireballs march over them; the pass runs for a level that
// asks for shafts even when it has no cloud at all.
//
// What the march reads that the cloud march does not is the sun's SHADOW, so
// the program is built on biome_light.glsl (with vsm.glsl, cloud_shadow.glsl,
// light_model.glsl and fog.glsl): biomeDirShadow answers from the virtual
// shadow map where the frame drew one, else from the cascades, and every
// sampler goes up through UploadShadowUniformsTo as for any lit program.
//
// COST. Steps samples a pixel of the volume target (24 by default, half that
// in the cheap march below the High quality tier under TAA, whose history
// averages a jitter that turns every frame), each one shadow-map tap, one
// cloud-shadow tap and a little arithmetic: at 2560x1080 and half resolution
// 1280x540 x 12 = 8.3 M samples, designed to sit under 1.5 ms on a GTX 1070
// (to be measured natively, phase F). FOUNDRY_SHAFTS=0 switches it off.

import (
	"log"
	"math"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// LightShafts switches the pass; a level still has to ask (fog: shafts:).
var LightShafts = gfx.EnvFlag("FOUNDRY_SHAFTS", true)

// shaftsRenderer is the program, built on first use.
type shaftsRenderer struct {
	tried, ok bool
	prog      progUniforms
}

var shafts shaftsRenderer

func (s *shaftsRenderer) ready() bool {
	if s.tried {
		return s.ok
	}
	s.tried = true
	vert := mustReadShader(biomeShaderPath("volume_vert.glsl"))
	frag := gfx.InjectAfterVersion(mustReadShader(biomeShaderPath("shafts_frag.glsl")),
		"#define BIOME_LIGHT_VSM_ONE_TAP 1\n"+biomeLightSnippet(0, false, false))
	vs, err := gfx.CompileShader(vert, gl.VERTEX_SHADER)
	if err != nil {
		log.Printf("shafts: volume_vert.glsl: %v — light shafts off", err)
		return false
	}
	fs, err := gfx.CompileShader(frag, gl.FRAGMENT_SHADER)
	if err != nil {
		log.Printf("shafts: shafts_frag.glsl: %v — light shafts off", err)
		return false
	}
	prog, err := linkStages(vs, fs)
	if err != nil {
		log.Printf("shafts: link: %v — light shafts off", err)
		return false
	}
	s.prog = progUniforms{prog: prog, loc: map[string]int32{}}
	s.ok = true
	return true
}

// shaftsDraw is what a frame's shafts are drawn with: the level's settings,
// its lights and the frame's shadow.
type shaftsDraw struct {
	lvl    *world.Level
	set    world.FogShafts
	shadow ShadowFrame
}

// levelShafts is the frame's shafts, or nil when the level asks for none,
// the switch is off, or the program will not build.
func levelShafts(lvl *world.Level, frame ShadowFrame) *shaftsDraw {
	if !LightShafts || lvl == nil || !lvl.Fog.ShaftsOn() || !shafts.ready() {
		return nil
	}
	return &shaftsDraw{lvl: lvl, set: lvl.Fog.Shafts, shadow: frame}
}

// shaftsSteps is the march's step count this frame: the level's, halved in
// the cheap march (volumeMarchCheap), at least 4.
func shaftsSteps(set world.FogShafts, cheap bool) int32 {
	n := set.Steps
	if cheap {
		n = (n + 1) / 2
	}
	return int32(max(n, world.FogShaftsMinSteps))
}

// draw marches the shafts into the bound volume target (its blend set up by
// the caller), reading the target's scene distances on distTex.
func (d *shaftsDraw) draw(t *VolumeTargets, distTex uint32, invPV mgl32.Mat4, camPos mgl32.Vec3) {
	p := &shafts.prog
	lights := world.SceneLights(d.lvl)
	world.UploadLightsTo(p.prog, lights, false)
	world.UploadFogTo(p.prog, d.lvl.Fog, d.lvl.Volumes, d.lvl)
	UploadShadowUniformsTo(p.prog, d.lvl.Lights, d.shadow)
	gl.UseProgram(p.prog)
	bindTex2D(volUnitScene, distTex)
	gl.Uniform1i(p.U("shSceneDist"), volUnitScene)
	gl.UniformMatrix4fv(p.U("shInvProjView"), 1, false, &invPV[0])
	gl.UniformMatrix4fv(p.U("camera"), 1, false, &gfx.Cam.Matrix[0])
	gl.Uniform3f(p.U("shCamPos"), camPos.X(), camPos.Y(), camPos.Z())
	gl.Uniform2f(p.U("shTargetSize"), float32(t.lowW), float32(t.lowH))
	cheap := volumeMarchCheap()
	turn := float32(0)
	if cheap || taaActive() {
		// A golden-ratio turn of the jitter a frame: TAA's history puts the
		// steps of successive frames together.
		turn = float32(math.Mod(float64(taa.frame)*0.6180339887, 1))
	}
	s := d.set
	gl.Uniform1i(p.U("shSteps"), shaftsSteps(s, cheap))
	gl.Uniform1f(p.U("shJitterTurn"), turn)
	gl.Uniform1f(p.U("shDistance"), s.Distance)
	gl.Uniform1f(p.U("shDensity"), s.Density)
	gl.Uniform1f(p.U("shBase"), s.Base)
	gl.Uniform1f(p.U("shFalloff"), s.Falloff)
	gl.Uniform1f(p.U("shG"), s.Anisotropy)
	gl.Uniform1f(p.U("shStrength"), s.Strength)
	gl.Uniform1f(p.U("shAmbient"), s.Ambient)
	uniformVec3(p.U("shColor"), s.Color)
	sun := int32(-1)
	if i, _, _, ok := volumeSun(d.lvl.Lights); ok {
		sun = int32(i)
	}
	gl.Uniform1i(p.U("shSun"), sun)
	gl.Uniform1i(p.U("volFullscreen"), 1)
	gl.Disable(gl.CULL_FACE)
	gl.DrawArrays(gl.TRIANGLES, 0, 3)
	DebugStats.VolumeDraws++
}
