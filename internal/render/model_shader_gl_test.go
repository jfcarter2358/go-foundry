package render

// model_shader_gl_test.go — an opt-in smoke test that the model editor's
// shaders actually compile and link on this machine's driver.
//
// go build cannot see inside a .glsl file, and the model editor compiles its
// program lazily on first open, so a typo in model_vert.glsl would otherwise
// only surface as a black window at the moment someone clicks "Model".
//
// It needs a real GL context, which means a display, so it is opt-in:
//
//	FOUNDRY_GL_TEST=1 go test -run TestModelShadersCompile .
//
// Skipped by default so plain `go test ./...` stays headless-safe.

import (
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/gltest"
	"foundry/internal/testenv"
	"path/filepath"
	"testing"

	"github.com/go-gl/gl/v4.1-core/gl"
)

func TestModelShadersCompile(t *testing.T) {
	gltest.Context(t)

	// The splice below reads skin_common.glsl relative to the install root, so
	// the root has to be the repo's resources/ for the duration.
	prevExe := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join(testenv.RepoRoot(t), "resources")
	t.Cleanup(func() { fileutil.ResourcesPath = prevExe })

	// With skinning spliced in, which is how both editors compile it: the
	// vertex stage calls skinPosition/skinNormal, so the raw file does not
	// compile on its own and never did once the entity editor's rigged preview
	// needed the GPU path.
	prog, err := gfx.CompileProgramWithSkinning(
		repoShader(t, "model_vert.glsl"),
		repoShader(t, "model_frag.glsl"),
	)
	if err != nil {
		t.Fatalf("model editor shaders failed to build: %v", err)
	}
	if prog == 0 {
		t.Fatal("CompileProgramFromFiles returned program 0")
	}

	// Every uniform the renderer sets must exist, or drawModelMesh would be
	// writing to -1 and the mesh would render with a stale matrix.
	for _, name := range []string{"projection", "view", "model", "tex", "camPos", "tint", "smoothShading"} {
		if loc := gl.GetUniformLocation(prog, gl.Str(name+"\x00")); loc < 0 {
			t.Errorf("uniform %q not found in the linked program", name)
		}
	}

	// The skinning uniforms have to be there too, or ApplySkinUniforms writes
	// to -1 and an animated attachment previews in its rest pose.
	for _, name := range []string{"skinned", "bones"} {
		if loc := gl.GetUniformLocation(prog, gl.Str(name+"\x00")); loc < 0 {
			t.Errorf("skinning uniform %q not found in the linked program", name)
		}
	}

	// Attribute locations must match vertex.glsl's, since Geometry.Load builds
	// its VAO against the world program's indices — the bone attributes
	// included, since an entity attachment's VAO carries those two as well.
	for name, want := range map[string]int32{
		"vert": 0, "vertTexCoord": 1, "vertNormal": 2, "boneIdx": 3, "boneWt": 4,
	} {
		if got := gl.GetAttribLocation(prog, gl.Str(name+"\x00")); got != want {
			t.Errorf("attribute %q at location %d, want %d (must match vertex.glsl)", name, got, want)
		}
	}
}

// TestSkinnedShadersCompileAndLink is the check the text-level tests cannot
// make: that skin_common.glsl is valid GLSL, that splicing it into all three
// entity passes still links, and that its uniforms and attributes come out where
// the Go side expects them.
//
// The bone-array uniform is the one worth proving at runtime: MAX_BONES is
// chosen against GL 4.1's *guaranteed* vertex uniform budget, and a driver that
// could not fit it would fail to link here rather than silently at the moment
// someone loads a rigged model.
func TestSkinnedShadersCompileAndLink(t *testing.T) {
	gltest.Context(t)

	prevExe := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join(testenv.RepoRoot(t), "resources")
	t.Cleanup(func() { fileutil.ResourcesPath = prevExe })

	if _, err := gfx.SkinCommonSource(); err != nil {
		t.Fatalf("skin_common.glsl did not load: %v", err)
	}

	// The colour pass, through the real loader.
	if err := gfx.LoadProgram(repoShader(t, "vertex.glsl"), repoShader(t, "fragment.glsl")); err != nil {
		t.Fatalf("world program with skinning failed to build: %v", err)
	}
	for _, name := range []string{"skinned", "bones"} {
		if loc := gl.GetUniformLocation(gfx.Program, gl.Str(name+"\x00")); loc < 0 {
			t.Errorf("uniform %q missing from the world program; ApplySkinUniforms would write to -1", name)
		}
	}
	for name, want := range map[string]int32{"vert": 0, "vertTexCoord": 1, "vertNormal": 2, "boneIdx": 3, "boneWt": 4} {
		if got := gl.GetAttribLocation(gfx.Program, gl.Str(name+"\x00")); got != want {
			t.Errorf("attribute %q at location %d, want %d", name, got, want)
		}
	}

	// Both shadow passes, through the same LinkProgram the engine uses.
	var dirProg, cubeProg uint32
	if err := LinkProgram(repoShader(t, "shadow_vert.glsl"), repoShader(t, "shadow_frag.glsl"), &dirProg); err != nil {
		t.Fatalf("cascade shadow program with skinning failed to build: %v", err)
	}
	if err := LinkProgram(repoShader(t, "shadow_cube_vert.glsl"), repoShader(t, "shadow_cube_frag.glsl"), &cubeProg); err != nil {
		t.Fatalf("point shadow program with skinning failed to build: %v", err)
	}
	for _, p := range []struct {
		name string
		prog uint32
	}{{"cascade", dirProg}, {"point", cubeProg}} {
		for _, name := range []string{"skinned", "bones"} {
			if loc := gl.GetUniformLocation(p.prog, gl.Str(name+"\x00")); loc < 0 {
				t.Errorf("%s shadow program has no %q uniform; an animated entity's shadow would not follow it",
					p.name, name)
			}
		}
	}
}
