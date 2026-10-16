package render

// ao.go — screen-space ambient occlusion and the sun's contact shadow
// (docs/lighting/PLAN.md, phases 3 and 6).
//
// The world is forward-shaded with no full depth prepass, so there is no depth
// to occlude against before the surfaces are lit. The occlusion is worked out
// AFTER the opaque world has drawn, from the depth it left (ao_comp.glsl:
// GTAO, the GI field's occlusion, the contact shadow's march; then
// ao_blur_comp.glsl), and the NEXT frame's shading reads it: a fragment
// carries its world position through the view-projection the texture was made
// with (aoPrevViewProj in the scene block) to find its texel, and takes it
// only if the distance stored there is its own — a texel that described
// something else last frame (a disocclusion, a moving thing's edge) reads as
// unoccluded. One frame late, which TAA's history hides; in exchange the
// occlusion lands only where it belongs, on the ambient light and the
// environment's reflection (specular.glsl's ssaoAt), and the contact shadow
// on the sun's direct light.
//
// Only in a scene-target frame (TAA, the default): the depth has to be a
// texture of one sample a pixel. An MSAA or "off" frame goes without.

import (
	"log"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

const (
	// AOTexUnit is the unit the occlusion texture binds to in a world
	// program — the second of the units the point shadows gave back.
	AOTexUnit = 4

	// lightingComputeUnit is where the lighting passes' compute programs take
	// their inputs (the scene's depth, a capture): a unit no world program
	// reads, so binding one there never puts a texture under a world
	// program's sampler — the depth, still the frame's attachment, would be a
	// feedback loop.
	lightingComputeUnit = 60

	aoRadius        = 1.0 // metres
	aoSlices        = 2
	aoSteps         = 6
	aoContactLength = 0.5 // metres
	aoContactSteps  = 12
)

type aoState struct {
	failed, built bool
	prog, progSDF progUniforms
	blur          progUniforms
	raw, final    uint32
	w, h          int32
	dummy         uint32

	frame int
	// What the texture in hand was made with, for the next frame to read it
	// by: valid only once a frame made one, and only for as long as frames
	// keep making them.
	valid        bool
	viewProj     mgl32.Mat4
	contactLight int
	hasAO        bool
	sdf          bool
}

var aoPass aoState

func (a *aoState) build() bool {
	if a.failed {
		return false
	}
	if a.built {
		return true
	}
	var err error
	if a.prog, err = giComputeProgram(giShaderSource("ao_comp.glsl", "")); err != nil {
		log.Printf("ao: %v — ambient occlusion is off", err)
		a.failed = true
		return false
	}
	if a.blur, err = giComputeProgram(giShaderSource("ao_blur_comp.glsl", "")); err != nil {
		log.Printf("ao: blur: %v — ambient occlusion is off", err)
		a.failed = true
		return false
	}
	if a.progSDF, err = giComputeProgram(giShaderSource("ao_comp.glsl", "#define AO_SDF 1\n", "gi_sdf_common.glsl")); err != nil {
		log.Printf("ao: the field's variant: %v — screen-space only", err)
		a.progSDF = progUniforms{}
	}
	a.built = true
	return true
}

// ensure sizes the textures to half of w × h.
func (a *aoState) ensure(w, h int32) {
	hw, hh := max((w+1)/2, 1), max((h+1)/2, 1)
	if a.raw != 0 && a.w == hw && a.h == hh {
		return
	}
	for _, t := range []*uint32{&a.raw, &a.final} {
		if *t != 0 {
			gl.DeleteTextures(1, t)
		}
		gl.GenTextures(1, t)
		gl.BindTexture(gl.TEXTURE_2D, *t)
		gl.TexStorage2D(gl.TEXTURE_2D, 1, gl.RGBA16F, hw, hh)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	}
	gl.BindTexture(gl.TEXTURE_2D, 0)
	a.w, a.h = hw, hh
	a.valid = false
}

// aoWanted is whether this frame makes the texture: a scene-target frame
// with AO or contact shadows asked for.
func aoWanted(o WorldDraw) bool {
	// The high reflection tier marches the screen-space reflections through
	// the distances this texture carries, so it wants the pass too.
	want := gfx.AmbientOcclusion || gfx.ContactShadows || gfx.Reflections == gfx.ReflectionsHigh
	return want && sceneFrameActive() && !o.Wireframe
}

// beginAOFrame tells the frame's shading about the texture the last frame
// made, or that there is none. Before anything is lit.
func beginAOFrame(o WorldDraw) {
	a := &aoPass
	b := &gfx.Scene.Block
	if !aoWanted(o) || !a.valid {
		a.valid = a.valid && aoWanted(o)
		b.SsaoOn, b.ContactShadowsOn, b.SsaoStrength = 0, 0, 0
		return
	}
	b.SsaoOn = 1
	b.SsaoPrevViewProj = a.viewProj
	b.SsaoStrength = 0
	if a.hasAO {
		b.SsaoStrength = 1
	}
	b.ContactShadowsOn = 0
	if gfx.ContactShadows && a.contactLight >= 0 {
		b.ContactShadowsOn = int32(a.contactLight + 1)
	}
}

// contactLightOf is the directional light the contact shadow follows: the
// first that casts a shadow and is lit. -1 for none.
func contactLightOf(ls world.LightSet) int {
	for i := range ls.Directional {
		if i >= world.MaxDirLights {
			break
		}
		d := &ls.Directional[i]
		if d.EffectiveBrightness() > 0 && d.CastsShadows() && d.Direction.Len() > 1e-6 {
			return i
		}
	}
	return -1
}

// runAOPass makes the texture from the depth the opaque world left. Called
// from DrawWorld after the transparent pass (which writes no depth) and before
// the viewmodel clears it.
func runAOPass(lvl *world.Level, o WorldDraw) {
	a := &aoPass
	if !aoWanted(o) || lvl == nil || !a.build() {
		a.valid = false
		return
	}
	ProfileBegin("ao")
	defer ProfileEnd()
	a.ensure(scene.w, scene.h)
	a.frame++

	proj := gfx.Projection
	view := gfx.Cam.Matrix
	inv := proj.Inv()
	viewRot := view.Mat3()
	viewToWorld := viewRot.Transpose()

	p := &a.prog
	sdf := false
	if gfx.AmbientOcclusion && gi.active && a.progSDF.prog != 0 {
		p, sdf = &a.progSDF, true
		gi.field.uploadSdfUniformsTo(p, giSdfUnitBase)
	}
	gl.UseProgram(p.prog)
	gl.ActiveTexture(gl.TEXTURE0 + lightingComputeUnit)
	gl.BindTexture(gl.TEXTURE_2D, scene.depth)
	gl.Uniform1i(p.U("sceneDepth"), lightingComputeUnit)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.UniformMatrix4fv(p.U("aoProj"), 1, false, &proj[0])
	gl.UniformMatrix4fv(p.U("aoInvProj"), 1, false, &inv[0])
	gl.UniformMatrix3fv(p.U("aoViewToWorld"), 1, false, &viewToWorld[0])
	cam := gfx.Cam.Position
	gl.Uniform3f(p.U("aoCamPos"), cam[0], cam[1], cam[2])
	gl.Uniform2i(p.U("fullSize"), scene.w, scene.h)
	gl.Uniform2i(p.U("halfSize"), a.w, a.h)
	radius, slices := float32(aoRadius), int32(aoSlices)
	if !gfx.AmbientOcclusion {
		// Contact shadows or the reflections' distances alone: no horizons to find.
		radius, slices = 0, 0
	}
	gl.Uniform1f(p.U("aoRadius"), radius)
	gl.Uniform1i(p.U("aoSlices"), max(slices, 1))
	gl.Uniform1i(p.U("aoSteps"), aoSteps)
	gl.Uniform1i(p.U("aoFrame"), int32(a.frame))
	gl.Uniform1i(p.U("sdfAO"), boolI32(sdf))

	a.contactLight = -1
	steps := int32(0)
	dir := mgl32.Vec3{}
	if gfx.ContactShadows {
		ls := world.SceneLights(lvl)
		if i := contactLightOf(ls); i >= 0 {
			a.contactLight = i
			steps = aoContactSteps
			dir = viewRot.Mul3x1(ls.Directional[i].Direction.Normalize().Mul(-1))
		}
	}
	gl.Uniform3f(p.U("contactDir"), dir[0], dir[1], dir[2])
	gl.Uniform1f(p.U("contactLength"), aoContactLength)
	gl.Uniform1i(p.U("contactSteps"), steps)

	gl.BindImageTexture(0, a.raw, 0, false, 0, gl.WRITE_ONLY, gl.RGBA16F)
	gl.DispatchCompute(uint32((a.w+7)/8), uint32((a.h+7)/8), 1)
	gl.MemoryBarrier(gl.SHADER_IMAGE_ACCESS_BARRIER_BIT | gl.TEXTURE_FETCH_BARRIER_BIT)

	bl := &a.blur
	gl.UseProgram(bl.prog)
	gl.ActiveTexture(gl.TEXTURE0 + lightingComputeUnit)
	gl.BindTexture(gl.TEXTURE_2D, a.raw)
	gl.Uniform1i(bl.U("aoRaw"), lightingComputeUnit)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.Uniform2i(bl.U("halfSize"), a.w, a.h)
	gl.BindImageTexture(0, a.final, 0, false, 0, gl.WRITE_ONLY, gl.RGBA16F)
	gl.DispatchCompute(uint32((a.w+7)/8), uint32((a.h+7)/8), 1)
	gl.BindImageTexture(0, 0, 0, false, 0, gl.READ_ONLY, gl.RGBA16F)
	gl.MemoryBarrier(gl.SHADER_IMAGE_ACCESS_BARRIER_BIT | gl.TEXTURE_FETCH_BARRIER_BIT)
	gl.ActiveTexture(gl.TEXTURE0 + lightingComputeUnit)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	gl.ActiveTexture(gl.TEXTURE0)

	a.viewProj = proj.Mul4(view)
	a.hasAO, a.sdf = gfx.AmbientOcclusion, sdf
	a.valid = true
	if gfx.Program != 0 {
		gl.UseProgram(gfx.Program)
	}
}

func boolI32(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// aoDummy is a 1×1 texture for ssaoTex while there is no occlusion.
func aoDummy() uint32 {
	if aoPass.dummy == 0 {
		gl.GenTextures(1, &aoPass.dummy)
		gl.BindTexture(gl.TEXTURE_2D, aoPass.dummy)
		px := [4]float32{1, 1, 0, 1}
		gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA16F, 1, 1, 0, gl.RGBA, gl.FLOAT, gl.Ptr(&px[0]))
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
		gl.BindTexture(gl.TEXTURE_2D, 0)
	}
	return aoPass.dummy
}

// uploadAOTo binds the occlusion texture (or the stand-in) to prog's ssaoTex.
func uploadAOTo(prog uint32) {
	tex := aoPass.final
	if !aoPass.valid || tex == 0 {
		tex = aoDummy()
	}
	gl.UseProgram(prog)
	gl.ActiveTexture(gl.TEXTURE0 + AOTexUnit)
	gl.BindTexture(gl.TEXTURE_2D, tex)
	gl.Uniform1i(gfx.UniformLocName(prog, "ssaoTex"), AOTexUnit)
	gl.ActiveTexture(gl.TEXTURE0)
}
