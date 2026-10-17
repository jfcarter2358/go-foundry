package render

// bloom.go — the glow round bright light (docs/lighting/PLAN.md, phase 4;
// bloom_comp.glsl): a mip chain of the resolved HDR picture at half
// resolution and down, filtered down and added back up, mixed into the frame
// by taa_output_frag.glsl before the tone curve. Only in an HDR frame (there
// is no light above white to glow otherwise) with the player's bloom on
// (gfx.Bloom).

import (
	"log"

	"github.com/go-gl/gl/v4.3-core/gl"
)

const (
	bloomMips = 6
	// BloomStrength is how much of the glow the frame takes: a mix toward it,
	// so it brightens round a light and costs the rest of the picture a
	// little.
	BloomStrength = 0.05
)

type bloomState struct {
	failed, built bool
	prog          progUniforms
	tex           uint32
	w, h          int32
	levels        int32
}

var bloom bloomState

func (b *bloomState) build() bool {
	if b.failed {
		return false
	}
	if b.built {
		return true
	}
	var err error
	if b.prog, err = giComputeProgram(giShaderSource("bloom_comp.glsl", "")); err != nil {
		log.Printf("bloom: %v — bloom is off", err)
		b.failed = true
		return false
	}
	b.built = true
	return true
}

func (b *bloomState) ensure(w, h int32) {
	hw, hh := max(w/2, 1), max(h/2, 1)
	if b.tex != 0 && b.w == hw && b.h == hh {
		return
	}
	if b.tex != 0 {
		gl.DeleteTextures(1, &b.tex)
	}
	b.levels = bloomMips
	for b.levels > 1 && (hw>>(b.levels-1) < 2 || hh>>(b.levels-1) < 2) {
		b.levels--
	}
	gl.GenTextures(1, &b.tex)
	gl.BindTexture(gl.TEXTURE_2D, b.tex)
	gl.TexStorage2D(gl.TEXTURE_2D, b.levels, gl.RGBA16F, hw, hh)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR_MIPMAP_NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	b.w, b.h = hw, hh
}

// runBloom builds the chain from src (w × h) and returns the texture whose
// mip 0 is the glow, or 0 when there is none.
func runBloom(src uint32, w, h int32) uint32 {
	b := &bloom
	if !b.build() {
		return 0
	}
	ProfileBegin("bloom")
	defer ProfileEnd()
	b.ensure(w, h)
	p := &b.prog
	gl.UseProgram(p.prog)
	gl.Uniform1i(p.U("bloomSrc"), lightingComputeUnit)
	size := func(l int32) (int32, int32) { return max(b.w>>l, 1), max(b.h>>l, 1) }

	// Down: the frame into mip 0, each mip into the next.
	gl.Uniform1i(p.U("upPass"), 0)
	for l := int32(0); l < b.levels; l++ {
		gl.ActiveTexture(gl.TEXTURE0 + lightingComputeUnit)
		if l == 0 {
			gl.BindTexture(gl.TEXTURE_2D, src)
			gl.Uniform1f(p.U("srcLod"), 0)
			gl.Uniform2f(p.U("srcTexel"), 1/float32(w), 1/float32(h))
		} else {
			gl.BindTexture(gl.TEXTURE_2D, b.tex)
			sw, sh := size(l - 1)
			gl.Uniform1f(p.U("srcLod"), float32(l-1))
			gl.Uniform2f(p.U("srcTexel"), 1/float32(sw), 1/float32(sh))
		}
		gl.Uniform1i(p.U("karis"), boolI32(l == 0))
		dw, dh := size(l)
		gl.Uniform2i(p.U("dstSize"), dw, dh)
		gl.BindImageTexture(0, b.tex, l, false, 0, gl.READ_WRITE, gl.RGBA16F)
		gl.DispatchCompute(uint32((dw+7)/8), uint32((dh+7)/8), 1)
		gl.MemoryBarrier(gl.SHADER_IMAGE_ACCESS_BARRIER_BIT | gl.TEXTURE_FETCH_BARRIER_BIT)
	}

	// Up: each mip adds the one below it, smallest first.
	gl.Uniform1i(p.U("upPass"), 1)
	gl.Uniform1i(p.U("karis"), 0)
	gl.ActiveTexture(gl.TEXTURE0 + lightingComputeUnit)
	gl.BindTexture(gl.TEXTURE_2D, b.tex)
	for l := b.levels - 1; l >= 1; l-- {
		sw, sh := size(l)
		gl.Uniform1f(p.U("srcLod"), float32(l))
		gl.Uniform2f(p.U("srcTexel"), 1/float32(sw), 1/float32(sh))
		dw, dh := size(l - 1)
		gl.Uniform2i(p.U("dstSize"), dw, dh)
		gl.BindImageTexture(0, b.tex, l-1, false, 0, gl.READ_WRITE, gl.RGBA16F)
		gl.DispatchCompute(uint32((dw+7)/8), uint32((dh+7)/8), 1)
		gl.MemoryBarrier(gl.SHADER_IMAGE_ACCESS_BARRIER_BIT | gl.TEXTURE_FETCH_BARRIER_BIT)
	}
	gl.BindImageTexture(0, 0, 0, false, 0, gl.READ_ONLY, gl.RGBA16F)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	gl.ActiveTexture(gl.TEXTURE0)
	return b.tex
}
