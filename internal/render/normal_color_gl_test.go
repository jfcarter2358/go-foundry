package render

// normal_color_gl_test.go — what an untextured surface actually comes out as,
// on a GPU.
//
// The Go side of this is one boolean (Geometry.NormalColorFlag) and the shader
// side is one branch, and either can be right on its own while the pair does
// nothing: the uniform is looked up by name, so a rename gives -1 and silence.
// What the pass has to produce is not "some colour" but a specific one — the
// surface normal encoded as n * 0.5 + 0.5 — because that is what makes each
// plane of a blocked-out solid a different, predictable colour instead of a
// flat fill that reads as a missing texture.
//
//	FOUNDRY_GL_TEST=1 go test -run TestUntextured ./internal/render/

import (
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/gltest"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// normalProbe draws one quad through the real world program and reads the
// centre pixel back.
type normalProbe struct{ t *testing.T }

func newNormalProbe(t *testing.T) *normalProbe {
	t.Helper()
	gltest.Context(t)
	withRepoResources(t)
	skyTestTarget(t)

	if err := gfx.LoadProgram(repoShader(t, "vertex.glsl"), repoShader(t, "fragment.glsl")); err != nil {
		t.Fatalf("world program: %v", err)
	}
	gl.UseProgram(gfx.Program)

	// The uniforms Geometry.Draw writes through, resolved the way app.go does.
	// Every one of them is program state, so a location left over from another
	// test's program would put this test's alpha into someone else's slot.
	u := func(name string) int32 { return world.ProgUniformLoc(gfx.Program, name+"\x00") }
	gfx.ProjectionUniform = u("projection")
	gfx.Cam.Uniform = u("camera")
	gfx.AlphaUniform = u("alpha")
	gfx.SmoothUniform = u("smoothShading")
	gfx.BakedLightUniform = u("bakedLight")
	gfx.NormalColorUniform = u("normalColor")
	gfx.UnlitUniform = u("unlit")
	gfx.AlphaSharpenUniform = u("alphaSharpen")
	gfx.IsWaterUniform = u("isWater")
	gfx.WaveAmplitudeUniform = u("waveAmplitude")
	gfx.WaveLengthUniform = u("waveLength")
	gfx.WaveSpeedUniform = u("waveSpeed")
	gfx.ReflectivityUniform = u("reflectivity")
	gfx.ReflectColorUniform = u("reflectColor")
	gfx.HasBlendUniform = u("hasBlend")
	gfx.BlendTexUniform = u("blendTex")
	gfx.BlendMaskUniform = u("blendMask")
	gfx.HasNormalMapUniform = u("hasNormalMap")
	gfx.NormalMapUniform = u("normalMap")
	gl.Uniform1i(gfx.AlphaSharpenUniform, 0)

	// Flat white ambient and no other light, so the shaded result IS the
	// albedo and nothing has to be divided back out of the readback.
	var ls world.LightSet
	ls.Global.Color = mgl32.Vec3{1, 1, 1}
	ls.Global.Brightness = 1
	world.UploadLightsTo(gfx.Program, ls, false)
	world.UploadFogTo(gfx.Program, nil, nil, nil)

	// Not optional even with no shadow-casting light in the scene: an
	// unassigned sampler defaults to unit 0, where the albedo sampler2D lives,
	// and a cube or array sampler colliding with it there makes every draw
	// through this program GL_INVALID_OPERATION — nothing rasterises at all.
	if err := InitShadows(repoShader(t, "shadow_vert.glsl"), repoShader(t, "shadow_frag.glsl"),
		repoShader(t, "shadow_cube_vert.glsl"), repoShader(t, "shadow_cube_frag.glsl")); err != nil {
		t.Fatalf("shadow programs: %v", err)
	}
	UploadShadowUniformsTo(gfx.Program, ls, ShadowFrame{})
	UploadPointShadowUniformsTo(gfx.Program, ls)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.UseProgram(gfx.Program)

	// Looking down -Z at the origin from a little way back.
	gfx.Projection = mgl32.Perspective(mgl32.DegToRad(45), 1, 0.1, 100)
	gl.UniformMatrix4fv(gfx.ProjectionUniform, 1, false, &gfx.Projection[0])
	gfx.Cam.Position = mgl32.Vec3{0, 0, 4}
	gfx.Cam.Direction = mgl32.Vec3{0, 0, -1}
	gfx.UpdateCamMatrix()

	return &normalProbe{t: t}
}

// shadeQuad draws a unit quad in the XY plane — so its outward normal is +Z
// before model is applied — and returns the centre pixel.
func (p *normalProbe) shadeQuad(texture string, model mgl32.Mat4) [4]uint8 {
	p.t.Helper()
	return p.shadeQuadWith(gfx.Geometry{Alpha: 1}, texture, model)
}

// shadeQuadWith is shadeQuad starting from g, for the per-surface flags.
func (p *normalProbe) shadeQuadWith(g gfx.Geometry, texture string, model mgl32.Mat4) [4]uint8 {
	p.t.Helper()

	const h = 1.5 // wide enough to cover the centre pixel even edge-on-ish
	verts := [][]float32{
		{-h, -h, 0, 0, 0},
		{h, -h, 0, 1, 0},
		{h, h, 0, 1, 1},
		{-h, h, 0, 0, 1},
	}
	if err := g.Load(verts, []int{0, 1, 2, 0, 2, 3}, texture, mgl32.Vec3{}); err != nil {
		p.t.Fatalf("upload probe quad: %v", err)
	}
	defer g.Delete()

	gl.Disable(gl.CULL_FACE)
	gl.ClearColor(0, 1, 0, 1) // a colour the shader cannot produce
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	g.DrawWithTransform(model)

	var px [4]uint8
	gl.ReadPixels(skyTestSize/2, skyTestSize/2, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
	return px
}

// nearlyByte allows for the 8-bit readback and the shader's own rounding.
func nearlyByte(t *testing.T, what string, got uint8, want float32) {
	t.Helper()
	const tol = 6
	w := int(want*255 + 0.5)
	if d := int(got) - w; d > tol || d < -tol {
		t.Errorf("%s: got %d, want %d", what, got, w)
	}
}

// A face with no texture is painted with its own normal, and turning the face
// turns the colour with it. The second case is what separates this from a
// constant: a quad rolled 45 degrees about Y has an X component it did not
// have before, and the red channel has to pick it up.
func TestUntexturedSurfaceIsPaintedWithItsNormal(t *testing.T) {
	p := newNormalProbe(t)

	facing := p.shadeQuad("", mgl32.Ident4())
	nearlyByte(t, "facing the camera, red", facing[0], 0.5)
	nearlyByte(t, "facing the camera, green", facing[1], 0.5)
	nearlyByte(t, "facing the camera, blue", facing[2], 1.0)
	if facing[3] != 255 {
		t.Errorf("an untextured face came out at alpha %d, want opaque", facing[3])
	}

	// Rolled 45 degrees about Y: the normal is now (sin45, 0, cos45).
	const sin45 = 0.70710678
	turned := p.shadeQuad("", mgl32.HomogRotate3DY(mgl32.DegToRad(45)))
	nearlyByte(t, "turned 45 degrees, red", turned[0], 0.5+0.5*sin45)
	nearlyByte(t, "turned 45 degrees, green", turned[1], 0.5)
	nearlyByte(t, "turned 45 degrees, blue", turned[2], 0.5+0.5*sin45)
}

// The other half of the branch: naming a texture still samples it. Without
// this the flag could be stuck on and every wall in the game would be shaded
// in normals with the untextured test still passing.
func TestTexturedSurfaceStillSamplesItsTexture(t *testing.T) {
	p := newNormalProbe(t)

	gfx.TextureNames = gfx.ScanTextures(fileutil.TexturesDir())
	if err := gfx.EnsureTextureLoaded("concrete"); err != nil {
		t.Fatalf("load concrete: %v", err)
	}
	px := p.shadeQuad("concrete", mgl32.Ident4())

	// concrete.png is grey, so its channels sit close together; the normal
	// colour of this quad is (128, 128, 255), which does not.
	if int(px[2])-int(px[0]) > 40 {
		t.Errorf("a concrete face came back at %v — blue that far ahead of red is the normal colour, not the texture", px)
	}
}
