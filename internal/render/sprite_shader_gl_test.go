package render

// sprite_shader_gl_test.go — opt-in check that the three sprite programs
// compile and link, and that every uniform the Go side drives actually exists
// in the linked program.
//
// This matters more here than it does for most passes. sprite_common.glsl is
// injected into all three vertex stages so a quad casts the shadow of the quad
// you can see; if one of the depth stages failed to compile against it, the
// only symptom in the running engine would be a panic at editor startup, and if
// a uniform name were merely misspelled the symptom would be a sprite whose
// shadow silently detaches as the camera turns. Neither is something go build
// can see, because it cannot look inside a .glsl file.
//
// Needs a display, so it is opt-in like the other GL tests:
//
//	FOUNDRY_GL_TEST=1 go test -run TestSpriteShadersCompile ./internal/render/

import (
	"foundry/internal/fileutil"
	"foundry/internal/gltest"
	"foundry/internal/testenv"
	"path/filepath"
	"testing"
)

func TestSpriteShadersCompile(t *testing.T) {
	gltest.Context(t)
	// Not the bare "resources" the two older GL tests use: `go test` runs each
	// package in its own directory, so that path only resolves with a symlink
	// dropped inside internal/render (see HANDOFF.md's note). testenv.RepoRoot
	// walks up to the go.mod instead, which is what it exists for and what lets
	// this test pass from anywhere.
	prevExe := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join(testenv.RepoRoot(t), "resources")
	t.Cleanup(func() { fileutil.ResourcesPath = prevExe })
	t.Cleanup(DeleteSprites)

	SpriteInit()

	// The placement uniforms, which all three stages read out of
	// sprite_common.glsl. A -1 here means the shared snippet did not reach that
	// program, which is exactly the failure the injection exists to prevent.
	for _, p := range []struct {
		name string
		prog *spriteProgram
	}{
		{"colour", &sprites.color},
		{"dir shadow", &sprites.dir},
		{"cube shadow", &sprites.cube},
	} {
		for _, u := range []struct {
			name string
			loc  int32
		}{
			{"projView", p.prog.projView},
			{"viewPos", p.prog.viewPos},
			{"viewDir", p.prog.viewDir},
			{"directional", p.prog.directional},
			{"billboardMode", p.prog.billboardMode},
			{"fixedRight", p.prog.fixedRight},
			{"fixedUp", p.prog.fixedUp},
		} {
			if u.loc < 0 {
				t.Errorf("%s program: uniform %q not found in the linked program", p.name, u.name)
			}
		}
	}

	// The colour pass's own, including the lighting block's `receiveShadows` —
	// its presence is what proves biome_light.glsl was injected and that a lit
	// sprite will actually take the scene's shadows.
	for _, u := range []struct {
		name string
		loc  int32
	}{
		{"tex", sprites.color.tex},
		{"blendMode", sprites.color.blendMode},
		{"alphaCutoff", sprites.color.alphaCutoff},
		{"lit", sprites.color.lit},
		{"receiveShadows", sprites.color.receiveShadows},
	} {
		if u.loc < 0 {
			t.Errorf("colour program: uniform %q not found in the linked program", u.name)
		}
	}
}

// repoShader resolves a shader by name against the repository's resources/
// directory, for the GL tests in this package.
//
// `go test` runs each package in its own directory, so a bare
// "resources/shaders/x.glsl" only resolved with a symlink dropped inside
// internal/render — the workaround HANDOFF.md used to document. Walking up to
// the go.mod instead is what testenv.RepoRoot is for.
func repoShader(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(testenv.RepoRoot(t), "resources", "shaders", name)
}
