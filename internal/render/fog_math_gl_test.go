package render

// fog_math_gl_test.go — fog.glsl's actual arithmetic, on a GPU.
//
// The other GL tests prove the fog uniforms reach every program. This proves
// what those uniforms then DO: that distance is radial rather than planar, that
// each of the three modes has the curve its name promises, that MaxOpacity caps
// the blend, and that an additive surface is attenuated rather than tinted.
//
// None of that is checkable in Go — it lives entirely inside a .glsl file — and
// all of it is the kind of thing that stays plausible while being wrong. A
// planar distance, in particular, looks completely correct in a screenshot and
// only reveals itself when the camera turns.
//
// The shader here is a throwaway that shades one flat colour at a world position
// given as a uniform, so the fog term is isolated from lighting, geometry and
// the depth buffer. It compiles the SAME fog.glsl the engine does.
//
//	FOUNDRY_GL_TEST=1 go test -run TestFog ./internal/render/

import (
	"foundry/internal/gfx"
	"foundry/internal/gltest"
	"foundry/internal/world"
	"math"
	"testing"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

const fogProbeVert = `#version 430
layout(location = 0) in vec2 aPos;
void main() { gl_Position = vec4(aPos, 0.0, 1.0); }
`

// A white surface, so the value read back IS 1 - fogFactor when the fog colour
// is black. Nothing else is in the way.
const fogProbeFrag = `#version 430
uniform vec3 probePos;
uniform int  probeAdditive;
out vec4 outColor;
void main() {
    vec3 c = vec3(1.0);
    outColor = vec4(probeAdditive != 0 ? applyFogAdditive(c, probePos)
                                       : applyFog(c, probePos), 1.0);
}
`

type fogProbe struct {
	prog uint32
	vao  uint32
}

func newFogProbe(t *testing.T) *fogProbe {
	t.Helper()

	vs, err := gfx.CompileShader(fogProbeVert, gl.VERTEX_SHADER)
	if err != nil {
		t.Fatalf("probe vertex: %v", err)
	}
	fs, err := gfx.CompileShader(gfx.MustInjectFog(fogProbeFrag), gl.FRAGMENT_SHADER)
	if err != nil {
		t.Fatalf("probe fragment: %v", err)
	}
	prog, err := linkStages(vs, fs)
	if err != nil {
		t.Fatalf("probe program: %v", err)
	}

	// One triangle covering the viewport.
	tri := []float32{-1, -1, 3, -1, -1, 3}
	var vao, vbo uint32
	gl.GenVertexArrays(1, &vao)
	gl.GenBuffers(1, &vbo)
	gl.BindVertexArray(vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(tri)*4, gl.Ptr(tri), gl.STATIC_DRAW)
	gl.EnableVertexAttribArray(0)
	gl.VertexAttribPointerWithOffset(0, 2, gl.FLOAT, false, 2*4, 0)
	gl.BindVertexArray(0)

	return &fogProbe{prog: prog, vao: vao}
}

// shade returns the red channel of the fogged white surface at pos, as 0..1.
// With a black fog colour that is exactly 1 - fogFactor(pos).
func (p *fogProbe) shade(f *world.Fog, pos mgl32.Vec3, additive bool) float32 {
	return p.shadeThrough(f, nil, pos, additive)
}

// shadeThrough is shade with fog volumes in the way as well — the same probe,
// since applyFog is the one entry point both halves of the fog model come out
// of. See fog_volume_gl_test.go.
func (p *fogProbe) shadeThrough(f *world.Fog, vols []world.Volume, pos mgl32.Vec3, additive bool) float32 {
	world.UploadFogTo(p.prog, f, vols, nil) // binds the program and sets fogCamPos
	gl.Uniform3f(world.ProgUniformLoc(p.prog, "probePos\x00"), pos.X(), pos.Y(), pos.Z())
	a := int32(0)
	if additive {
		a = 1
	}
	gl.Uniform1i(world.ProgUniformLoc(p.prog, "probeAdditive\x00"), a)

	gl.Disable(gl.DEPTH_TEST)
	gl.ClearColor(0, 1, 0, 1)
	gl.Clear(gl.COLOR_BUFFER_BIT)
	gl.BindVertexArray(p.vao)
	gl.DrawArrays(gl.TRIANGLES, 0, 3)
	gl.BindVertexArray(0)

	var px [4]uint8
	gl.ReadPixels(skyTestSize/2, skyTestSize/2, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
	return float32(px[0]) / 255
}

func nearly(t *testing.T, what string, got, want float32) {
	t.Helper()
	const tol = 0.02 // 8-bit readback plus the shader's own float rounding
	if float32(math.Abs(float64(got-want))) > tol {
		t.Errorf("%s: got %.3f, want %.3f", what, got, want)
	}
}

// blackFog is the setup every case here starts from: black, so the surviving
// surface brightness IS the un-fogged fraction.
func blackFog() world.Fog {
	f := world.DefaultFog()
	f.Color = mgl32.Vec3{0, 0, 0}
	f.MaxOpacity = 1
	return f
}

func fogProbeSetup(t *testing.T) *fogProbe {
	t.Helper()
	gltest.Context(t)
	withRepoResources(t)
	skyTestTarget(t)
	gfx.Cam.Position = mgl32.Vec3{} // the eye fog is measured from
	return newFogProbe(t)
}

func TestFogLinearRampsBetweenStartAndEnd(t *testing.T) {
	p := fogProbeSetup(t)

	f := blackFog()
	f.Mode = world.FogLinear
	f.Start = 100
	f.End = 300

	nearly(t, "inside Start", p.shade(&f, mgl32.Vec3{0, 0, 50}, false), 1)
	nearly(t, "at Start", p.shade(&f, mgl32.Vec3{0, 0, 100}, false), 1)
	nearly(t, "halfway", p.shade(&f, mgl32.Vec3{0, 0, 200}, false), 0.5)
	nearly(t, "at End", p.shade(&f, mgl32.Vec3{0, 0, 300}, false), 0)
	nearly(t, "beyond End", p.shade(&f, mgl32.Vec3{0, 0, 900}, false), 0)
}

// The one that a screenshot cannot catch. Every point on a sphere around the
// eye is the same distance away, whatever axis it is on — with a view-axis
// depth instead, the fog on a fixed object would change as the camera turned.
func TestFogDistanceIsRadialNotPlanar(t *testing.T) {
	p := fogProbeSetup(t)

	f := blackFog()
	f.Mode = world.FogLinear
	f.Start = 0
	f.End = 200

	const d = 100
	want := p.shade(&f, mgl32.Vec3{0, 0, d}, false)
	nearly(t, "same distance on +Z", want, 0.5)

	for _, pos := range []mgl32.Vec3{
		{d, 0, 0}, {-d, 0, 0}, {0, d, 0}, {0, 0, -d},
		{d / float32(math.Sqrt2), 0, d / float32(math.Sqrt2)},
	} {
		nearly(t, "same distance, different heading", p.shade(&f, pos, false), want)
	}
}

func TestFogExponentialModesFollowTheirCurves(t *testing.T) {
	p := fogProbeSetup(t)

	f := blackFog()
	f.Start = 0
	f.Density = 0.01

	// exp: exp(-density*d) of the surface survives.
	f.Mode = world.FogExp
	for _, d := range []float32{50, 100, 250} {
		nearly(t, "exp", p.shade(&f, mgl32.Vec3{0, 0, d}, false),
			float32(math.Exp(-float64(f.Density*d))))
	}

	// exp2: exp(-(density*d)^2) — clearer up close, thickening much faster
	// once it starts, which is the whole reason both modes exist.
	f.Mode = world.FogExp2
	for _, d := range []float32{50, 100, 250} {
		td := float64(f.Density * d)
		nearly(t, "exp2", p.shade(&f, mgl32.Vec3{0, 0, d}, false),
			float32(math.Exp(-td*td)))
	}

	f.Mode = world.FogExp
	near := p.shade(&f, mgl32.Vec3{0, 0, 60}, false)
	f.Mode = world.FogExp2
	if p.shade(&f, mgl32.Vec3{0, 0, 60}, false) <= near {
		t.Error("exp2 is not clearer than exp at short range; the two modes are indistinguishable")
	}
}

// MaxOpacity is what keeps a landmark readable at the far end of heavy fog, so
// what it has to guarantee is a floor on the surface that survives — at any
// distance, in any mode.
func TestFogMaxOpacityLeavesASilhouette(t *testing.T) {
	p := fogProbeSetup(t)

	f := blackFog()
	f.Mode = world.FogLinear
	f.Start = 0
	f.End = 100
	f.MaxOpacity = 0.75

	nearly(t, "far beyond End", p.shade(&f, mgl32.Vec3{0, 0, 5000}, false), 0.25)

	f.Mode = world.FogExp
	f.Density = 0.5
	nearly(t, "deep exp fog", p.shade(&f, mgl32.Vec3{0, 0, 5000}, false), 0.25)
}

// An additive surface draws over a framebuffer that already holds the fog, so
// fog swallows it rather than tinting it. With a WHITE fog colour the two rules
// disagree completely: mixing would brighten a distant surface to full white,
// attenuating darkens it to nothing.
func TestAdditiveFogAttenuatesRatherThanTints(t *testing.T) {
	p := fogProbeSetup(t)

	f := blackFog()
	f.Color = mgl32.Vec3{1, 1, 1}
	f.Mode = world.FogLinear
	f.Start = 0
	f.End = 200

	pos := mgl32.Vec3{0, 0, 200} // fully fogged

	if got := p.shade(&f, pos, false); got < 0.98 {
		t.Errorf("ordinary fog: fully fogged surface read %.3f against a white fog; it should be the fog colour", got)
	}
	if got := p.shade(&f, pos, true); got > 0.02 {
		t.Errorf("additive fog: fully fogged surface read %.3f; it must be swallowed, not brightened", got)
	}
}

// A switched-off fog, and a level with none at all, must both leave the surface
// exactly as it was — the shader's `fogMode == 0` early-out is what every
// unfogged level in the project depends on.
func TestNoFogChangesNothing(t *testing.T) {
	p := fogProbeSetup(t)

	f := blackFog()
	f.Mode = world.FogLinear
	f.Start = 0
	f.End = 100
	f.Enabled = false

	nearly(t, "switched-off fog", p.shade(&f, mgl32.Vec3{0, 0, 5000}, false), 1)
	nearly(t, "no fog block at all", p.shade(nil, mgl32.Vec3{0, 0, 5000}, false), 1)
}
