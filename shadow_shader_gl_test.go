package main

// shadow_shader_gl_test.go — opt-in check that every stage which samples the
// cascade array still compiles against the driver.
//
// The cascade array is a comparison sampler (EnsureCascadeArray), which makes
// its GLSL type sampler2DArrayShadow and constrains how it can be read: the
// biome block compiles into vertex stages, where the only explicit-lod form
// available for that sampler type is textureGrad. A mistake there is not a
// compile error Go can see — it surfaces as a panic out of GrassInit at the
// moment a level with foliage is opened.
//
//	FOUNDRY_GL_TEST=1 go test -run TestShadowSamplingShadersCompile .

import (
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"testing"

	"github.com/go-gl/gl/v4.1-core/gl"
)

// compileBiomeStages mirrors mustBiomeProgram without its panic, so a failure
// is a test message rather than a stack trace.
func compileBiomeStages(vertPath, fragPath, vertSnippet, fragSnippet string) error {
	vs, err := gfx.CompileShader(gfx.InjectAfterVersion(mustReadShader(vertPath), vertSnippet), gl.VERTEX_SHADER)
	if err != nil {
		return err
	}
	fs, err := gfx.CompileShader(gfx.InjectAfterVersion(mustReadShader(fragPath), fragSnippet), gl.FRAGMENT_SHADER)
	if err != nil {
		return err
	}
	_, err = linkStages(vs, fs)
	return err
}

func TestShadowSamplingShadersCompile(t *testing.T) {
	glTestContext(t)

	prevExe := fileutil.ExecutablePath
	fileutil.ExecutablePath = "resources"
	t.Cleanup(func() { fileutil.ExecutablePath = prevExe })

	// The scene shader: 5x5 PCF in a fragment stage.
	if err := gfx.LoadProgram("resources/shaders/vertex.glsl", "resources/shaders/fragment.glsl"); err != nil {
		t.Fatalf("world program: %v", err)
	}

	// The ground patch: the single-tap variant in a VERTEX stage, the full 5x5
	// with cascade blending in the fragment stage.
	if err := compileBiomeStages(
		biomeShaderPath("ground_vert.glsl"), biomeShaderPath("ground_frag.glsl"),
		biomeLightSnippet(0, false, false), biomeLightSnippet(2, true, true)); err != nil {
		t.Errorf("ground patch program: %v", err)
	}

	// The scatter: 3x3 in both stages, the vertex one sampling once per blade.
	if err := compileBiomeStages(
		biomeShaderPath("biome_vert.glsl"), biomeShaderPath("biome_frag.glsl"),
		biomeScatterSnippet()+"\n"+biomeLightSnippet(1, false, false),
		biomeLightSnippet(1, true, false)); err != nil {
		t.Errorf("biome scatter program: %v", err)
	}
}
