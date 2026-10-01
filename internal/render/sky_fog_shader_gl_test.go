package render

// sky_fog_shader_gl_test.go — opt-in check that the sky program links and that
// fog.glsl actually reached every stage that is supposed to shade with it.
//
// This is the test the feature most needs, and for the reason the sprite shader
// test gives: nothing in `go build` can look inside a .glsl file. Fog is
// declared ONCE and spliced into five separate programs (the world shader, the
// two detail programs that reuse its fragment stage, the biome programs and the
// sprite programs) — and the failure mode when one of them misses the splice is
// not a crash. It is a level where the wall fades into the distance and the
// grass standing in front of it does not, which reads as a bug in the terrain
// rather than as a missing #include, and which only shows up in a level that
// happens to have both.
//
// Needs a display, so it is opt-in like the other GL tests:
//
//	FOUNDRY_GL_TEST=1 go test -run TestSkyAndFogShaders ./internal/render/

import (
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/gltest"
	"foundry/internal/testenv"
	"foundry/internal/world"
	"path/filepath"
	"testing"

	"github.com/go-gl/gl/v4.3-core/gl"
)

func withRepoResources(t *testing.T) {
	t.Helper()
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join(testenv.RepoRoot(t), "resources")
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
}

// fogUniformNames are the ones world.UploadFogTo writes. Every program that
// shades has to expose all of them, or that pass is silently unfogged.
//
// Both halves of the fog model are in the list. The volume ones are the half
// that would go missing quietly: a pass that has the distance fog and not the
// volumes looks completely correct until an author places a bank, and then the
// bank stops at the edge of whatever that pass draws — fog that ends at the
// grass, or that the sprites are standing in front of.
var fogUniformNames = []string{
	"fogMode", "fogColor", "fogStart", "fogEnd",
	"fogDensity", "fogMaxOpacity", "fogCamPos",
	"numFogVolumes", "fogVolBasis[0]", "fogVolCenter[0]", "fogVolShape[0]",
	"fogVolColor[0]", "fogVolDensity[0]", "fogVolMaxOpacity[0]",
}

func TestSkyAndFogShadersCompile(t *testing.T) {
	gltest.Context(t)
	withRepoResources(t)

	SkyInit()
	if !skyRender.initialized {
		t.Fatal("SkyInit did not produce a program; the sky would silently never draw")
	}

	for _, name := range []string{
		"projection", "view", "skyYaw", "skyKind", "skyTex",
		"skyZenith", "skyHorizon", "skyGround", "skyTint", "skyIntensity",
		"skyFogBlend", "skyFogFalloff",
	} {
		if loc := skyRender.prog.U(name); loc < 0 {
			t.Errorf("sky program: uniform %q not found in the linked program", name)
		}
	}

	// The sky blends its horizon into the fog, so it compiles fog.glsl too —
	// fogColor being present is what proves the splice happened rather than
	// the file merely existing.
	for _, name := range []string{"fogMode", "fogColor"} {
		if loc := skyRender.prog.U(name); loc < 0 {
			t.Errorf("sky program: fog uniform %q not found; the horizon blend is dead", name)
		}
	}

	// The sky takes fog VOLUMES at full strength rather than a horizon share of
	// them, because a volume is bounded and the ray leaves it. Standing inside a
	// fog bank under a crisp sky is the most obvious way for one to stop reading
	// as fog, and these uniforms going missing is how that happens.
	for _, name := range []string{"numFogVolumes", "fogVolBasis[0]", "fogVolColor[0]"} {
		if loc := skyRender.prog.U(name); loc < 0 {
			t.Errorf("sky program: fog volume uniform %q not found; the sky would stay clear inside a bank", name)
		}
	}
}

// The point of the whole arrangement: one fog model, compiled into every pass
// that shades, so nothing can fade at a different rate from the thing next to
// it. Each of these programs is built by a different helper, and each of those
// is a separate opportunity to forget the splice.
func TestEveryShadingProgramGotTheFog(t *testing.T) {
	gltest.Context(t)
	withRepoResources(t)
	t.Cleanup(DeleteSprites)

	GrassInit()
	SpriteInit()
	SkyInit()

	for _, p := range []struct {
		name string
		prog uint32
	}{
		{"scatter colour", scatters.color.prog},
		{"sprite colour", sprites.color.prog},
	} {
		if p.prog == 0 {
			t.Errorf("%s program did not link", p.name)
			continue
		}
		for _, name := range fogUniformNames {
			if world.ProgUniformLoc(p.prog, name+"\x00") < 0 {
				t.Errorf("%s program: fog uniform %q missing — this pass would not fade with the rest of the scene", p.name, name)
			}
		}
	}
}

// The world shader is the one every other pass is fogged in order to AGREE
// with, so it gets its own check: it is loaded by gfx.LoadProgram rather than
// by any helper in this package, which is a sixth and separate place the splice
// has to happen.
func TestTheWorldShaderGotTheFog(t *testing.T) {
	gltest.Context(t)
	withRepoResources(t)

	if err := gfx.LoadProgram(repoShader(t, "vertex.glsl"), repoShader(t, "fragment.glsl")); err != nil {
		t.Fatalf("LoadProgram: %v", err)
	}
	for _, name := range fogUniformNames {
		if world.ProgUniformLoc(gfx.Program, name+"\x00") < 0 {
			t.Errorf("world shader: fog uniform %q missing — nothing in the level would fade", name)
		}
	}
}

// The depth-only programs are the ones that must NOT have picked fog up: they
// write a depth and nothing else, and splicing a shading concern into them
// would be a lie about what they do. This is the assertion that keeps the
// `fogged` flag on the program helpers from quietly becoming decoration.
func TestDepthOnlyProgramsHaveNoFog(t *testing.T) {
	gltest.Context(t)
	withRepoResources(t)

	GrassInit()
	SpriteInit()

	for _, p := range []struct {
		name string
		prog uint32
	}{
		{"scatter cascade shadow", scatters.shadow.prog},
		{"scatter cube shadow", scatters.cube.prog},
	} {
		if loc := world.ProgUniformLoc(p.prog, "fogColor\x00"); loc >= 0 {
			t.Errorf("%s program exposes fogColor; a depth pass has no colour to fog", p.name)
		}
	}
}

// The other half of internal/app's world_passes_test.go. That one proves
// InitWorldPasses is what the engine calls and that the editor does not; this
// one proves the call actually leaves every pass able to draw.
//
// Worth having as a pair because the bug it descends from was silent in exactly
// this gap: each pass early-outs on its own readiness flag, so a pass that was
// never initialised does not fail, it simply draws nothing.
func TestInitWorldPassesStandsUpEveryPass(t *testing.T) {
	gltest.Context(t)
	withRepoResources(t)
	t.Cleanup(DeleteSprites)

	InitWorldPasses()

	for _, p := range []struct {
		name  string
		ready bool
	}{
		{"scatter volumes", scatters.initialized},
		{"sprites and particles", sprites.ready},
		{"sky", skyRender.initialized},
	} {
		if !p.ready {
			t.Errorf("InitWorldPasses left the %s pass uninitialised; it would draw nothing, silently", p.name)
		}
	}

}

// A cubemap the level names but that is not on disk must not take the sky down
// with it — RenderSky falls back to the gradient, and the loader path this
// checks is the one that decides.
func TestAnUnknownCubemapFaceIsAnErrorNotACrash(t *testing.T) {
	gltest.Context(t)
	withRepoResources(t)

	s := world.DefaultSky()
	s.Kind = world.SkyCubemap
	s.SetFacesFromBase("no_such_sky_texture")
	if !s.HasCubemap() {
		t.Fatal("six named faces did not read as a cubemap")
	}
	if _, err := gfx.EnsureCubemapLoaded(s.Faces); err == nil {
		t.Error("a cubemap naming textures that do not exist loaded successfully")
	}
	// And the GL state is unpolluted: a half-built cubemap must not be left
	// bound or leaked.
	if e := gl.GetError(); e != gl.NO_ERROR {
		t.Errorf("failed cubemap load left GL error 0x%x", e)
	}
}
