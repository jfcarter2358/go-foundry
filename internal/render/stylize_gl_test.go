package render

// stylize_gl_test.go — the painted look's screen pass (stylize.go,
// stylize_frag.glsl) on a hand-made normal buffer.
//
// A 32² frame of mid grey, the camera looking down -Z from the origin, and a
// surface 5 units off whose normal turns sharply at the frame's middle
// column. Turning outward (left half facing left, right half facing right) is
// a ridge: the pixels along it must come out brighter — the edge highlight —
// and those away from it untouched. Turning inward is a crease: darker — the
// wash. And a record whose depth the depth buffer no longer holds (something
// drawn over it since) must be left alone.
//
//	FOUNDRY_GL_TEST=1 go test -run StylizePaint ./internal/render/

import (
	"math"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/gltest"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// halfBits is f as an IEEE half, for the normal buffer's packHalf2x16 (no
// NaN, infinity or subnormal: unit-range octahedral coordinates only).
func halfBits(f float32) uint32 {
	b := math.Float32bits(f)
	sign := (b >> 16) & 0x8000
	exp := int32((b>>23)&0xff) - 127 + 15
	mant := b & 0x7fffff
	if exp <= 0 {
		return sign
	}
	return sign | uint32(exp)<<10 | mant>>13
}

// styOctGo is light_model.glsl's styOct.
func styOctGo(n mgl32.Vec3) [2]float32 {
	n = n.Mul(1 / (float32(math.Abs(float64(n[0]))) + float32(math.Abs(float64(n[1]))) + float32(math.Abs(float64(n[2])))))
	if n[2] < 0 {
		sx, sy := float32(1), float32(1)
		if n[0] < 0 {
			sx = -1
		}
		if n[1] < 0 {
			sy = -1
		}
		return [2]float32{(1 - float32(math.Abs(float64(n[1])))) * sx, (1 - float32(math.Abs(float64(n[0])))) * sy}
	}
	return [2]float32{n[0], n[1]}
}

// stylizeRecord is light_model.glsl's stylizePack.
func stylizeRecord(n mgl32.Vec3, depth float32) [4]uint32 {
	o := styOctGo(n.Normalize())
	pk := halfBits(o[0]) | halfBits(o[1])<<16
	return [4]uint32{pk, pk, math.Float32bits(depth), 0x100 | 255}
}

func TestStylizePaintsRidgesAndCreases(t *testing.T) {
	gltest.Context(t)
	withRepoResources(t)
	prevTAA, prevSty := taa, stylizer
	t.Cleanup(func() { taa, stylizer = prevTAA, prevSty })
	taa, stylizer = taaState{}, stylizeState{}
	if !taa.ready() || !stylizer.ready() {
		t.Fatal("the TAA or stylize program did not build")
	}
	gfx.Scene.Block = gfx.SceneBlock{}
	gfx.Scene.Flush()

	const n = 32
	const depth = 5
	proj := mgl32.Perspective(mgl32.DegToRad(60), 1, 0.1, 100)
	view := mgl32.Ident4()
	// The window depth a surface at view depth 5 is drawn at.
	clip := proj.Mul4x1(mgl32.Vec4{0, 0, -depth, 1})
	win := clip[2]/clip[3]*0.5 + 0.5

	grey := make([]float32, n*n*4)
	for i := range grey {
		grey[i] = 0.4
	}
	var colour, gbuf, depthTex uint32
	gl.GenTextures(1, &colour)
	gl.BindTexture(gl.TEXTURE_2D, colour)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA16F, n, n, 0, gl.RGBA, gl.FLOAT, gl.Ptr(grey))
	setNearestClamp()
	gl.GenTextures(1, &gbuf)
	gl.BindTexture(gl.TEXTURE_2D, gbuf)
	setNearestClamp()
	gl.GenTextures(1, &depthTex)
	gl.BindTexture(gl.TEXTURE_2D, depthTex)
	setNearestClamp()
	out, outFBO := colorTarget(gl.RGBA16F, gl.RGBA, n, n)
	t.Cleanup(func() {
		gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
		for _, tex := range []*uint32{&colour, &gbuf, &depthTex, &out} {
			gl.DeleteTextures(1, tex)
		}
		gl.DeleteFramebuffers(1, &outFBO)
	})

	look := world.DefaultStylize()
	look.Edge, look.Wash, look.Drybrush = 1, 1, 0

	// paint runs the pass over a frame whose left half faces left and right
	// half right (outward) or the other way (inward), with the depth buffer
	// holding windowDepth everywhere, and returns the red channel.
	paint := func(outward bool, windowDepth float32) []float32 {
		lx := float32(-0.7)
		if !outward {
			lx = 0.7
		}
		recs := make([]uint32, n*n*4)
		for y := 0; y < n; y++ {
			for x := 0; x < n; x++ {
				nx := lx
				if x >= n/2 {
					nx = -lx
				}
				r := stylizeRecord(mgl32.Vec3{nx, 0, 0.7}, depth)
				copy(recs[(y*n+x)*4:], r[:])
			}
		}
		gl.BindTexture(gl.TEXTURE_2D, gbuf)
		gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA32UI, n, n, 0, gl.RGBA_INTEGER, gl.UNSIGNED_INT, gl.Ptr(recs))
		ds := make([]float32, n*n)
		for i := range ds {
			ds[i] = windowDepth
		}
		gl.BindTexture(gl.TEXTURE_2D, depthTex)
		gl.TexImage2D(gl.TEXTURE_2D, 0, gl.DEPTH_COMPONENT32F, n, n, 0, gl.DEPTH_COMPONENT, gl.FLOAT, gl.Ptr(ds))
		gl.BindFramebuffer(gl.FRAMEBUFFER, outFBO)
		gl.Viewport(0, 0, n, n)
		gl.Disable(gl.DEPTH_TEST)
		gl.Disable(gl.BLEND)
		stylizer.draw(colour, gbuf, depthTex, n, n, view, proj, &look)
		px := make([]float32, n*n*4)
		gl.ReadPixels(0, 0, n, n, gl.RGBA, gl.FLOAT, gl.Ptr(px))
		red := make([]float32, n*n)
		for i := range red {
			red[i] = px[i*4]
		}
		return red
	}
	at := func(red []float32, x, y int) float32 { return red[y*n+x] }

	ridge := paint(true, win)
	if v := at(ridge, n/2, n/2); v < 0.45 {
		t.Errorf("a ridge pixel came out %.3f; want it highlighted above the 0.4 grey", v)
	}
	if v := at(ridge, 4, n/2); math.Abs(float64(v)-0.4) > 0.01 {
		t.Errorf("a flat pixel by a ridge came out %.3f; want the 0.4 grey untouched", v)
	}

	crease := paint(false, win)
	if v := at(crease, n/2, n/2); v > 0.36 {
		t.Errorf("a crease pixel came out %.3f; want it washed below the 0.4 grey", v)
	}

	covered := paint(true, win*0.98)
	for i, v := range covered {
		if math.Abs(float64(v)-0.4) > 0.01 {
			t.Fatalf("pixel %d of a covered frame came out %.3f; want every record refused and the grey kept", i, v)
		}
	}
	gl.Enable(gl.DEPTH_TEST)
}
