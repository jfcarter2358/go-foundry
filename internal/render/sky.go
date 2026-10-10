package render

// sky.go — the skybox pass.
//
// One cube, one program, drawn once a frame. What makes it cheap is where it
// sits in the frame rather than anything about the geometry: sky_vert.glsl
// forces every vertex to the far plane (gl_Position = p.xyww), and the pass
// runs AFTER the whole opaque pass, so the engine's global GL_LEQUAL depth test
// discards it everywhere the world already drew. A sky drawn first would shade
// every pixel on screen and then be painted over; this one shades only the
// pixels nothing else reached.
//
// It runs BEFORE the transparent pass, which is the other half of the ordering:
// translucent water and alpha sprites have to blend against the sky behind
// them, and they cannot if the sky has not been drawn yet.
//
// Unlike GrassInit/SpriteInit, SkyInit is called from the engine's
// own startup (app.go) rather than from EditorInit — the sky is level content,
// not an authoring aid, and a release build has no EditorInit to be initialised
// from.

import (
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/world"
	"log"
	"path/filepath"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// skyRenderer holds the sky program and its cube.
type skyRenderer struct {
	prog progUniforms
	vao  uint32
	vbo  uint32

	// warned remembers which face sets have already failed to load, so a sky
	// naming a texture that is not there costs one log line rather than one per
	// frame. Keyed the same way gfx.Cubemaps is.
	warned map[string]bool

	initialized bool
}

var skyRender skyRenderer

// SkyInit compiles the sky program and builds its cube. Must be called once
// with the GL context current.
func SkyInit() {
	shaderDir := fileutil.ShadersDir()
	sp := func(name string) string { return filepath.Join(shaderDir, name) }

	vs, err := gfx.CompileShader(mustReadShader(sp("sky_vert.glsl")), gl.VERTEX_SHADER)
	if err != nil {
		log.Printf("sky vertex shader: %v — this level's sky will not draw", err)
		return
	}
	// The fragment stage reads fogMode/fogColor to blend the horizon into the
	// fog, so it compiles the same fog.glsl every world-shading stage does —
	// see the note on skyFogBlend in sky_frag.glsl.
	fs, err := gfx.CompileShader(gfx.MustInjectFog(mustReadShader(sp("sky_frag.glsl"))), gl.FRAGMENT_SHADER)
	if err != nil {
		log.Printf("sky fragment shader: %v — this level's sky will not draw", err)
		return
	}
	prog, err := linkStages(vs, fs)
	if err != nil {
		log.Printf("sky program: %v — this level's sky will not draw", err)
		return
	}

	skyRender.prog = progUniforms{prog: prog, loc: map[string]int32{}}
	skyRender.warned = map[string]bool{}
	skyRender.vao, skyRender.vbo = uploadSkyCube()

	// Filter cube faces across their shared edges instead of clamping inside
	// each one. Without it, the bilinear tap at a face border reads only that
	// face's own edge texels, and the two faces meeting there disagree by half
	// a texel — which draws as a hairline down all twelve edges of the sky,
	// most visible against a smooth gradient. It is context state rather than
	// texture state, so it is set once here; the point-light shadow cubemaps
	// are the only other cube textures in the engine and they are strictly
	// better off for it too.
	gl.Enable(gl.TEXTURE_CUBE_MAP_SEAMLESS)

	skyRender.initialized = true
	gl.UseProgram(gfx.Program)
}

// skyCubeVerts is a unit cube centred on the origin, position only. Its size is
// irrelevant — the vertex stage pins every vertex to the far plane regardless —
// so it is 1 rather than something tuned to the far clip distance.
var skyCubeVerts = []float32{
	// -X
	-1, -1, -1, -1, -1, 1, -1, 1, 1,
	-1, 1, 1, -1, 1, -1, -1, -1, -1,
	// +X
	1, -1, 1, 1, -1, -1, 1, 1, -1,
	1, 1, -1, 1, 1, 1, 1, -1, 1,
	// -Y
	-1, -1, -1, 1, -1, -1, 1, -1, 1,
	1, -1, 1, -1, -1, 1, -1, -1, -1,
	// +Y
	-1, 1, 1, 1, 1, 1, 1, 1, -1,
	1, 1, -1, -1, 1, -1, -1, 1, 1,
	// -Z
	-1, 1, -1, 1, 1, -1, 1, -1, -1,
	1, -1, -1, -1, -1, -1, -1, 1, -1,
	// +Z
	-1, -1, 1, 1, -1, 1, 1, 1, 1,
	1, 1, 1, -1, 1, 1, -1, -1, 1,
}

func uploadSkyCube() (vao, vbo uint32) {
	gl.GenVertexArrays(1, &vao)
	gl.GenBuffers(1, &vbo)
	gl.BindVertexArray(vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(skyCubeVerts)*4, gl.Ptr(skyCubeVerts), gl.STATIC_DRAW)
	gl.EnableVertexAttribArray(0)
	gl.VertexAttribPointerWithOffset(0, 3, gl.FLOAT, false, 3*4, 0)
	gl.BindVertexArray(0)
	return vao, vbo
}

// SkyTexUnit is the texture image unit the skybox cubemap binds to, and
// SkyFromTexUnit the second one a transition's snapshot binds to while a
// script's ease_sky is blending away from it (see world.Sky.TransitionFrom).
//
// 0 and 1, and safely so: the sky program samples nothing else, and no other
// pass is mid-draw when it runs. Named rather than inlined so the comment in
// gfx/graphics.go about which units are spoken for stays true of every program
// in the engine.
const (
	SkyTexUnit     = 0
	SkyFromTexUnit = 1
)

// resolveSkyCubemap is what a sky state actually draws as: the kind it asks
// for, unless that is cubemap and the cubemap cannot be loaded or is not fully
// named, in which case gradient. warn logs the first time a given face set
// fails for a reason worth reporting — an author's OWN sky, not a transition's
// snapshot of a past one, which failing the same way a second time would just
// be noise about a state the level is already easing away from.
func resolveSkyCubemap(s *world.Sky, warn bool) (world.SkyKind, uint32) {
	if !s.HasCubemap() {
		return world.SkyGradient, 0
	}
	tex, err := gfx.EnsureCubemapLoaded(s.Faces)
	if err != nil {
		if warn {
			key := gfx.CubemapKey(s.Faces)
			if !skyRender.warned[key] {
				skyRender.warned[key] = true
				log.Printf("sky cubemap: %v — falling back to the gradient sky", err)
			}
		}
		return world.SkyGradient, 0
	}
	return world.SkyCubemap, tex
}

// RenderSky draws the level's sky, and restores the main program and the depth
// state on the way out. A level with no sky draws nothing at all, which is what
// leaves every level authored before there were skies looking as it did.
func RenderSky(lvl *world.Level) {
	if !skyRender.initialized || lvl == nil || lvl.Sky == nil {
		return
	}
	s := lvl.Sky
	p := &skyRender.prog

	kind, cube := resolveSkyCubemap(s, true)

	// A transition in flight is a second, frozen sky state to blend away from
	// — see world.Sky.BeginTransition. Outside one, `from` is just `s` again
	// and blend is 1, which uploads a "from" state identical to "to" that
	// sky_frag.glsl never actually samples.
	from := s
	blend := s.TransitionBlend()
	fromKind, fromCube := kind, cube
	if s.TransitionFrom != nil {
		from = s.TransitionFrom
		fromKind, fromCube = resolveSkyCubemap(from, false)
	}

	// The fog uniforms sky_frag.glsl reads for its horizon blend, uploaded
	// through the same function every other program's fog goes through. It
	// leaves the program bound.
	world.UploadFogTo(p.prog, lvl.Fog, lvl.Volumes, lvl)

	// The camera with its translation dropped: the cube is centred on the eye,
	// so the sky has no near side to walk toward.
	view := gfx.Cam.Matrix
	view[12], view[13], view[14] = 0, 0, 0

	proj := gfx.Projection
	gl.UniformMatrix4fv(p.U("projection"), 1, false, &proj[0])
	gl.UniformMatrix4fv(p.U("view"), 1, false, &view[0])

	gl.Uniform1i(p.U("skyKind"), int32(kind))
	gl.Uniform3f(p.U("skyZenith"), s.Zenith.X(), s.Zenith.Y(), s.Zenith.Z())
	gl.Uniform3f(p.U("skyHorizon"), s.Horizon.X(), s.Horizon.Y(), s.Horizon.Z())
	gl.Uniform3f(p.U("skyGround"), s.Ground.X(), s.Ground.Y(), s.Ground.Z())
	gl.Uniform3f(p.U("skyTint"), s.Tint.X(), s.Tint.Y(), s.Tint.Z())
	gl.Uniform1f(p.U("skyIntensity"), s.Intensity)
	gl.Uniform1f(p.U("skyYaw"), mgl32.DegToRad(s.Rotation))

	gl.Uniform1f(p.U("skyBlend"), blend)
	gl.Uniform1i(p.U("skyKindFrom"), int32(fromKind))
	gl.Uniform3f(p.U("skyZenithFrom"), from.Zenith.X(), from.Zenith.Y(), from.Zenith.Z())
	gl.Uniform3f(p.U("skyHorizonFrom"), from.Horizon.X(), from.Horizon.Y(), from.Horizon.Z())
	gl.Uniform3f(p.U("skyGroundFrom"), from.Ground.X(), from.Ground.Y(), from.Ground.Z())
	gl.Uniform3f(p.U("skyTintFrom"), from.Tint.X(), from.Tint.Y(), from.Tint.Z())
	gl.Uniform1f(p.U("skyIntensityFrom"), from.Intensity)
	gl.Uniform1f(p.U("skyYawFrom"), mgl32.DegToRad(from.Rotation))

	fogBlend, falloff := float32(0), world.FogDefaultSkyFalloff
	if lvl.Fog != nil && lvl.Fog.Enabled {
		fogBlend, falloff = lvl.Fog.SkyBlend, lvl.Fog.SkyFalloff
	}
	gl.Uniform1f(p.U("skyFogBlend"), fogBlend)
	gl.Uniform1f(p.U("skyFogFalloff"), falloff)

	gl.Uniform1i(p.U("skyTex"), SkyTexUnit)
	gl.Uniform1i(p.U("skyTexFrom"), SkyFromTexUnit)
	if kind == world.SkyCubemap {
		gl.ActiveTexture(gl.TEXTURE0 + SkyTexUnit)
		gl.BindTexture(gl.TEXTURE_CUBE_MAP, cube)
	}
	if blend < 1 && fromKind == world.SkyCubemap {
		gl.ActiveTexture(gl.TEXTURE0 + SkyFromTexUnit)
		gl.BindTexture(gl.TEXTURE_CUBE_MAP, fromCube)
	}

	// Depth writes off: the sky is behind everything, so nothing gains from it
	// being in the depth buffer, and leaving it out keeps the transparent pass
	// that follows sorting against the world rather than against the far plane.
	// The test itself stays on — it is what makes this pass cost only the
	// pixels the world did not cover.
	cullWas := currentGLState().cull
	gl.Disable(gl.CULL_FACE) // the cube is seen from the inside
	gl.DepthMask(false)

	gl.BindVertexArray(skyRender.vao)
	gl.DrawArrays(gl.TRIANGLES, 0, int32(len(skyCubeVerts)/3))
	gl.BindVertexArray(0)

	gl.DepthMask(true)
	if cullWas {
		gl.Enable(gl.CULL_FACE)
	}
	if gfx.Program != 0 {
		// The pass's contract with the frame loop: leave the world shader
		// bound. Guarded on 0 the way UpdateCamMatrix is, so a test that draws
		// the sky without ever loading the world shader does not bind a program
		// that does not exist.
		gl.UseProgram(gfx.Program)
	}
}

// DefaultClearColor is what the engine cleared to before levels could say
// otherwise, and what a level with neither a sky nor fog still gets.
var DefaultClearColor = mgl32.Vec3{0.2, 0.2, 0.3}

// WorldClearColor is what this level's colour buffer should be cleared to.
//
// With a sky it barely matters — the sky pass fills every pixel the world left
// — but with fog and no sky it matters a great deal: geometry that fades to the
// fog colour against a slate-blue background fades to a silhouette rather than
// to nothing, and the fog's whole job is to make the far distance stop existing.
func WorldClearColor(lvl *world.Level) mgl32.Vec3 {
	if lvl == nil {
		return DefaultClearColor
	}
	if lvl.Sky != nil {
		return lvl.Sky.HorizonColor()
	}
	if lvl.Fog != nil && lvl.Fog.Enabled {
		return lvl.Fog.Color
	}
	return DefaultClearColor
}

// ApplyWorldClearColor sets the GL clear colour for this frame. Called from the
// frame loop immediately before gl.Clear rather than once at startup, because
// what it depends on is level content and the level can change under it.
func ApplyWorldClearColor(lvl *world.Level) {
	c := WorldClearColor(lvl)
	gl.ClearColor(c.X(), c.Y(), c.Z(), 1.0)
}
