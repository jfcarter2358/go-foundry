package render

// vsm_lookup_gl_test.go — vsm.glsl's lookup, alone (docs/adventure/PHASE_B.md,
// "the ghost shadow").
//
// A fully lit receiver's nine bilinear taps sum to 9 — or, rounded, to a hair
// over it, and 1 − 9.0000005/9 is a hair under zero, which vsmShadow used to
// take for its "page not resident" answer and walk on to the coarser levels.
// Nobody asks for those pages (the marks ask for a pixel's own level and the
// next), so they keep whatever they held when last drawn: a character's shadow
// from where it stood a while ago, stamped on lit ground. Here the receiver's
// own level is lit everywhere and every coarser level is solid shadow, so a
// lookup that falls through is caught at once.
//
//	FOUNDRY_GL_TEST=1 GODEBUG=cgocheck=0 go test -run TestVSMLitLookupNeverFallsThrough ./internal/render/

import (
	"math/rand"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/gltest"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

const vsmLookupKernel = `
layout(local_size_x = 64) in;
layout(std430, binding = 0) buffer Points { vec4 pts[]; };
layout(std430, binding = 1) buffer Out { vec2 outv[]; };
uniform int count;
void main() {
    uint i = gl_GlobalInvocationID.x;
    if (i >= uint(count)) {
        return;
    }
    vec3 p = pts[i].xyz;
    vec3 n = vec3(0.0, 1.0, 0.0);
    vec3 l = vec3(0.0, -1.0, 0.0);
    outv[i] = vec2(vsmShadow(p, n, l), vsmShadow1(p, n, l));
}
`

func TestVSMLitLookupNeverFallsThrough(t *testing.T) {
	gltest.Context(t)
	withRepoResources(t)

	src := "#version 430\n" + mustReadShader(biomeShaderPath("vsm.glsl")) + "\n" + vsmLookupKernel
	cs, err := gfx.CompileShader(src, gl.COMPUTE_SHADER)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	prog, err := linkStages(cs)
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	defer gl.DeleteProgram(prog)

	// Two pages: layer 0 all far (lit), layer 1 all at the light (shadow).
	var atlas uint32
	gl.GenTextures(1, &atlas)
	defer gl.DeleteTextures(1, &atlas)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, atlas)
	page := vsmPageTexels * vsmPageTexels
	depths := make([]float32, 2*page)
	for i := 0; i < page; i++ {
		depths[i] = 1
	}
	gl.TexImage3D(gl.TEXTURE_2D_ARRAY, 0, gl.DEPTH_COMPONENT32F, vsmPageTexels, vsmPageTexels, 2, 0,
		gl.DEPTH_COMPONENT, gl.FLOAT, gl.Ptr(depths))
	vsmShadowSampling(gl.TEXTURE_2D_ARRAY)

	// The table: level 0 is every page lit; every coarser level is every
	// page shadow.
	var table uint32
	gl.GenTextures(1, &table)
	defer gl.DeleteTextures(1, &table)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, table)
	cells := make([]uint32, vsmLevels*vsmWindowPages*vsmWindowPages)
	for i := range cells {
		cells[i] = 2 // layer 1, + 1
		if i < vsmWindowPages*vsmWindowPages {
			cells[i] = 1 // layer 0, + 1
		}
	}
	gl.TexImage3D(gl.TEXTURE_2D_ARRAY, 0, gl.R32UI, vsmWindowPages, vsmWindowPages, vsmLevels, 0,
		gl.RED_INTEGER, gl.UNSIGNED_INT, gl.Ptr(cells))
	setIntegerNearest(gl.TEXTURE_2D_ARRAY)

	// Receivers on a floor round the camera, at random places within a
	// texel, all reading level 0 (a large negative bias), the light straight
	// down: light space x, y = world x, z.
	const n = 64 * 256
	rng := rand.New(rand.NewSource(7))
	pts := make([]float32, 4*n)
	half := float32(vsmTexel(0) * vsmLevelTexels * 0.4)
	for i := 0; i < n; i++ {
		pts[4*i] = (rng.Float32()*2 - 1) * half
		pts[4*i+2] = (rng.Float32()*2 - 1) * half
	}
	var bufs [2]uint32
	gl.GenBuffers(2, &bufs[0])
	defer gl.DeleteBuffers(2, &bufs[0])
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, bufs[0])
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, len(pts)*4, gl.Ptr(pts), gl.STATIC_DRAW)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, bufs[1])
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, n*2*4, nil, gl.DYNAMIC_READ)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 0, bufs[0])
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 1, bufs[1])

	gl.UseProgram(prog)
	loc := func(name string) int32 { return gl.GetUniformLocation(prog, gl.Str(name+"\x00")) }
	gl.ActiveTexture(gl.TEXTURE0 + VSMAtlasUnit)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, atlas)
	gl.ActiveTexture(gl.TEXTURE0 + VSMTableUnit)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, table)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.Uniform1i(loc("vsmAtlas"), VSMAtlasUnit)
	gl.Uniform1i(loc("vsmPageTable"), VSMTableUnit)
	f := VSMFrame{
		rot:         mgl32.Mat3FromRows(mgl32.Vec3{1, 0, 0}, mgl32.Vec3{0, 0, 1}, mgl32.Vec3{0, -1, 0}),
		depthOffset: 0.5,
		depthScale:  1,
		levelBias:   -40,
	}
	for l := range f.camTexel {
		f.camTexel[l] = [2]float32{vsmLevelTexels / 2, vsmLevelTexels / 2}
	}
	uploadVSMUniforms(prog, &f, loc)
	gl.Uniform1i(loc("count"), n)
	gl.DispatchCompute(n/64, 1, 1)
	gl.MemoryBarrier(gl.BUFFER_UPDATE_BARRIER_BIT)

	out := make([]float32, 2*n)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, bufs[1])
	gl.GetBufferSubData(gl.SHADER_STORAGE_BUFFER, 0, len(out)*4, gl.Ptr(out))
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	gl.UseProgram(0)

	bad9, bad1 := 0, 0
	for i := 0; i < n; i++ {
		if out[2*i] != 0 {
			bad9++
		}
		if out[2*i+1] != 0 {
			bad1++
		}
	}
	if bad9 > 0 || bad1 > 0 {
		t.Fatalf("lit receivers read as shadowed — fell through to a coarser level: %d of %d (nine taps), %d (one tap)", bad9, n, bad1)
	}
}
