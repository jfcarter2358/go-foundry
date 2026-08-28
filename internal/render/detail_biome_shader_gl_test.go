package render

// detail_biome_shader_gl_test.go — opt-in check that the detail (tessellation
// + scatter) and biome (scatter + ground patch) programs still compile and
// link, and that the `model` uniform every one of them gained for Phase 4
// (see HANDOFF.md) — carrying a chunk's live Origin/Rotation/Scale into what
// used to be an implicitly-identity pass — is actually present in the linked
// program rather than optimized away by a typo'd name.
//
// go build cannot see inside a .glsl file, and DetailInit/GrassInit compile
// their programs lazily on first use, so a mistake here would otherwise
// surface as a panic (or a silently-stale -1 uniform location) the moment a
// level with detail or biome paint was opened. Needs a display, so it is
// opt-in like model_shader_gl_test.go/shadow_shader_gl_test.go:
//
//	FOUNDRY_GL_TEST=1 go test -run TestDetailBiomeGroundShadersCompile .

import (
	"foundry/internal/fileutil"
	"foundry/internal/gltest"
	"foundry/internal/testenv"
	"path/filepath"
	"testing"
)

func TestDetailBiomeGroundShadersCompile(t *testing.T) {
	gltest.Context(t)
	prevExe := fileutil.ExecutablePath
	fileutil.ExecutablePath = filepath.Join(testenv.RepoRoot(t), "resources")
	t.Cleanup(func() { fileutil.ExecutablePath = prevExe })

	// GrassInit also compiles the ground-patch program (GroundInit); DetailInit
	// compiles the tessellation (displace) and instanced (scatter) programs,
	// each in its color/directional-shadow/point-shadow variant.
	DetailInit()
	GrassInit()

	checkModel := func(name string, loc int32) {
		t.Helper()
		if loc < 0 {
			t.Errorf("%s: `model` uniform not found in the linked program", name)
		}
	}
	checkModel("detail displace color", detailRender.dispColor.U("model"))
	checkModel("detail displace dir shadow", detailRender.dispShadow.U("model"))
	checkModel("detail displace cube shadow", detailRender.dispCube.U("model"))
	checkModel("detail scatter color", detailRender.scatColor.U("model"))
	checkModel("detail scatter dir shadow", detailRender.scatShadow.U("model"))
	checkModel("detail scatter cube shadow", detailRender.scatCube.U("model"))
	checkModel("biome scatter color", biomes.color.U("model"))
	checkModel("biome scatter dir shadow", biomes.shadow.U("model"))
	checkModel("biome scatter cube shadow", biomes.cube.U("model"))
	checkModel("ground patch", groundPatches.uModel)
}
