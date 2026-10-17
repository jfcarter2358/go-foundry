package render

// water_sky.go — the sky the water reflects (docs/terrain-paint/PHASE_W1.md,
// "Reflection").
//
// A level's sky is many things — a cubemap or a gradient, a transition between
// two of them, a yaw, a tint, the horizon's share of the fog and the
// atmosphere's haze — and all of it is in sky_frag.glsl. Rather than teach the
// water shader each of them, the sky shader ITSELF is drawn into a small
// octahedral map (water_oct.glsl's mapping, Y up) once a frame that has water
// in it, and the water reads that: a plain sampler2D, which the fragment
// stage can take where a samplerCube would collide with the albedo on unit 0
// in every program that never set it, and whose mips (generated after each
// draw) blur the reflection by roughness. A level with no sky clears the map
// to its clear colour and the water's flags say "no sky", so the reflection
// falls back to the ambient light along the reflected ray.
//
// The program is sky_frag.glsl with its varying made a global and its main
// renamed, followed by a main that sets the direction from the pixel's
// octahedral position — the same text as the sky pass, so the two cannot
// disagree about what the sky looks like.

import (
	"fmt"
	"log"
	"path/filepath"
	"regexp"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// waterSkySize is the map's edge, texels: a reflection is blurred by the
// ripples anyway, and 128² keeps the per-frame draw negligible.
const waterSkySize = 128

// waterSkyMap is the program, its target and whether it is usable.
type waterSkyMap struct {
	prog        progUniforms
	vao         uint32
	tex, fbo    uint32
	mips        int32
	initialised bool
	failed      bool
}

var waterSky waterSkyMap

var (
	reSkyVarying = regexp.MustCompile(`(?m)^\s*in\s+vec3\s+vDir\s*;`)
	reSkyMain    = regexp.MustCompile(`\bvoid\s+main\s*\(\s*\)`)
)

// waterSkySource is the map's fragment stage: sky_frag.glsl (fog already
// injected) with vDir a global and main renamed, and a main of its own.
func waterSkySource(skySrc string) (string, error) {
	if !reSkyVarying.MatchString(skySrc) {
		return "", fmt.Errorf("sky_frag.glsl: no `in vec3 vDir;` to make a global")
	}
	if !reSkyMain.MatchString(skySrc) {
		return "", fmt.Errorf("sky_frag.glsl: no main")
	}
	src := reSkyVarying.ReplaceAllString(skySrc, "vec3 vDir;")
	src = reSkyMain.ReplaceAllString(src, "void skyMain()")
	return src + `

// ---- the water's sky map (render/water_sky.go) ----
#include "water_oct.glsl"
uniform float waterSkySize;
void main() {
    vDir = waterOctDecode(gl_FragCoord.xy / waterSkySize);
    skyMain();
}
`, nil
}

// waterSkyVertex is a full-screen triangle.
const waterSkyVertex = `#version 430
void main() {
    vec2 p = vec2(float((gl_VertexID << 1) & 2), float(gl_VertexID & 2));
    gl_Position = vec4(p * 2.0 - 1.0, 0.0, 1.0);
}
`

func (m *waterSkyMap) init() bool {
	if m.initialised {
		return !m.failed
	}
	m.initialised = true
	fail := func(err error) bool {
		log.Printf("water: sky map: %v — the water reflects the ambient light instead", err)
		m.failed = true
		return false
	}
	skySrc := mustReadShader(filepath.Join(fileutil.ShadersDir(), "sky_frag.glsl"))
	src, err := waterSkySource(skySrc)
	if err != nil {
		return fail(err)
	}
	vs, err := gfx.CompileShader(waterSkyVertex, gl.VERTEX_SHADER)
	if err != nil {
		return fail(err)
	}
	fs, err := gfx.CompileShader(gfx.MustInjectFog(src), gl.FRAGMENT_SHADER)
	if err != nil {
		return fail(err)
	}
	prog, err := linkStages(vs, fs)
	if err != nil {
		return fail(err)
	}
	m.prog = progUniforms{prog: prog, loc: map[string]int32{}}
	gl.GenVertexArrays(1, &m.vao)
	gl.GenTextures(1, &m.tex)
	gl.BindTexture(gl.TEXTURE_2D, m.tex)
	m.mips = 1
	for s := waterSkySize; s > 1; s /= 2 {
		m.mips++
	}
	gl.TexStorage2D(gl.TEXTURE_2D, m.mips, gl.RGBA16F, waterSkySize, waterSkySize)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR_MIPMAP_LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	gl.GenFramebuffers(1, &m.fbo)
	gl.BindFramebuffer(gl.FRAMEBUFFER, m.fbo)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, m.tex, 0)
	gl.DrawBuffer(gl.COLOR_ATTACHMENT0)
	if st := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); st != gl.FRAMEBUFFER_COMPLETE {
		return fail(fmt.Errorf("framebuffer incomplete (0x%x)", st))
	}
	return true
}

// draw renders lvl's sky into the map and reports whether the water may read
// it as a sky (false: no sky, or no map). The caller restores the frame's
// framebuffer, viewport and state.
func (m *waterSkyMap) draw(lvl *world.Level) bool {
	if !m.init() {
		return false
	}
	gl.BindFramebuffer(gl.FRAMEBUFFER, m.fbo)
	gl.Viewport(0, 0, waterSkySize, waterSkySize)
	if lvl == nil || lvl.Sky == nil || !skyRender.initialized {
		c := WorldClearColor(lvl)
		gl.ClearBufferfv(gl.COLOR, 0, &[]float32{c.X(), c.Y(), c.Z(), 1}[0])
		return false
	}
	gl.Disable(gl.DEPTH_TEST)
	gl.DepthMask(false)
	gl.Disable(gl.BLEND)
	gl.Disable(gl.CULL_FACE)
	gl.UseProgram(m.prog.prog)
	// The view and projection are unused here (the direction comes from
	// the pixel); bindSkyLook sends them anyway.
	ident := mgl32.Ident4()
	bindSkyLook(&m.prog, lvl, ident, ident)
	gl.UseProgram(m.prog.prog)
	gl.Uniform1f(m.prog.U("waterSkySize"), waterSkySize)
	gl.BindVertexArray(m.vao)
	gl.DrawArrays(gl.TRIANGLES, 0, 3)
	gl.BindVertexArray(0)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, m.tex)
	gl.GenerateMipmap(gl.TEXTURE_2D)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	return true
}
