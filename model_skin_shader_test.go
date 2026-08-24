package main

// model_skin_shader_test.go — headless checks that the GPU skinning path is
// wired up consistently across the four places it has to agree.
//
// The GL smoke test (model_shader_gl_test.go) proves the shaders compile; these
// prove they say the same thing as the Go side, which is the failure that would
// otherwise show up as a silently unbent or a wildly exploded mesh rather than
// as a compile error.

import (
	"fmt"
	"foundry/internal/gfx"
	"foundry/internal/leveltest"
	"foundry/internal/model"
	"foundry/internal/modeltest"
	"strings"
	"testing"
)

// TestSkinShaderMatchesGoBoneLimit pins MAX_BONES against MaxSkinBones. They are
// duplicated by necessity — one in GLSL, one in Go — and the Go side is what
// refuses an oversized armature at load, so a shader with the smaller number
// would let a model through and then bind past the end of its palette.
func TestSkinShaderMatchesGoBoneLimit(t *testing.T) {
	src := leveltest.RepoFile(t, "resources", "shaders", "skin_common.glsl")
	want := fmt.Sprintf("#define MAX_BONES %d", gfx.MaxSkinBones)
	if !strings.Contains(src, want) {
		t.Errorf("skin_common.glsl is out of step with MaxSkinBones=%d (looking for %q)", gfx.MaxSkinBones, want)
	}
}

// TestSkinShaderCarriesTheInfluenceBudget checks the vec4 pair matches
// ModelMaxInfluences, which is what normalizeWeights prunes to.
func TestSkinShaderCarriesTheInfluenceBudget(t *testing.T) {
	if model.ModelMaxInfluences != 4 {
		t.Fatalf("ModelMaxInfluences is %d; skin_common.glsl carries influences in a vec4", model.ModelMaxInfluences)
	}
	src := leveltest.RepoFile(t, "resources", "shaders", "skin_common.glsl")
	for _, decl := range []string{"in vec4 boneIdx", "in vec4 boneWt"} {
		if !strings.Contains(src, decl) {
			t.Errorf("skin_common.glsl no longer declares %q", decl)
		}
	}
}

// TestEveryEntityPassSkins is the one that stops a shadow desyncing from its
// mesh: an animated entity is drawn by the colour pass and both shadow passes,
// and a pass that forgot to skin would draw it in its rest pose.
func TestEveryEntityPassSkins(t *testing.T) {
	for _, name := range []string{"vertex.glsl", "shadow_vert.glsl", "shadow_cube_vert.glsl"} {
		src := leveltest.RepoFile(t, "resources", "shaders", name)
		if !strings.Contains(src, "skinPosition(vert)") {
			t.Errorf("%s does not skin its position — an animated entity would draw at rest in this pass", name)
		}
	}
}

// TestSkinIsGatedOnTheUniformNotTheWeights guards the trap the shader's own
// header describes: an unbound VAO hands a vec4 attribute the default
// (0, 0, 0, 1), so a weight sum of zero is not what "unskinned" looks like.
func TestSkinIsGatedOnTheUniformNotTheWeights(t *testing.T) {
	src := leveltest.RepoFile(t, "resources", "shaders", "skin_common.glsl")
	if !strings.Contains(src, "uniform int  skinned") && !strings.Contains(src, "uniform int skinned") {
		t.Fatal("skin_common.glsl no longer declares the `skinned` uniform")
	}
	for _, fn := range []string{"skinPosition", "skinNormal"} {
		i := strings.Index(src, "vec"+"4 "+fn)
		if i < 0 {
			i = strings.Index(src, fn+"(")
		}
		if i < 0 {
			t.Errorf("skin_common.glsl no longer defines %s", fn)
			continue
		}
		body := src[i:]
		if end := strings.Index(body, "\n}"); end > 0 {
			body = body[:end]
		}
		if !strings.Contains(body, "skinned == 0") {
			t.Errorf("%s does not early-out on the `skinned` uniform; unskinned geometry would deform", fn)
		}
	}
}

// TestModelEditorShaderDoesNotSkin pins the CPU/GPU split: the editor deforms on
// the CPU into the array its wireframe and picking read, so a second
// implementation in model_vert.glsl would be a second thing to keep in step.
func TestModelEditorShaderDoesNotSkin(t *testing.T) {
	src := leveltest.RepoFile(t, "resources", "shaders", "model_vert.glsl")
	if strings.Contains(src, "skinPosition") {
		t.Error("model_vert.glsl skins; the model editor is supposed to deform on the CPU")
	}
}

// TestSkinAttrsForPacksInfluences covers the CPU side of the same contract.
func TestSkinAttrsForPacksInfluences(t *testing.T) {
	idx, wt := model.SkinAttrsFor([]model.ModelVertexWeight{{Bone: 3, Weight: 0.25}, {Bone: 7, Weight: 0.75}})
	if len(idx) != model.ModelMaxInfluences || len(wt) != model.ModelMaxInfluences {
		t.Fatalf("packed %d/%d values, want %d each", len(idx), len(wt), model.ModelMaxInfluences)
	}
	if idx[0] != 3 || wt[0] != 0.25 || idx[1] != 7 || wt[1] != 0.75 {
		t.Errorf("influences did not survive packing: %v / %v", idx, wt)
	}
	// The tail must be zero *weight*, which is what the shader treats as absent.
	for i := 2; i < model.ModelMaxInfluences; i++ {
		if wt[i] != 0 {
			t.Errorf("slot %d has weight %v, want 0", i, wt[i])
		}
	}

	// An unbound vertex must pack to all-zero weights, not to bone 0 at 1.
	idx, wt = model.SkinAttrsFor(nil)
	for i := range wt {
		if wt[i] != 0 || idx[i] != 0 {
			t.Errorf("an unbound vertex packed to %v / %v, want zeroes", idx, wt)
			break
		}
	}
}

// TestSubmeshesCarrySkinAttributesOnlyWhenRigged keeps the extra buffers off
// every wall in the level.
func TestSubmeshesCarrySkinAttributesOnlyWhenRigged(t *testing.T) {
	plain := model.NewCubeDoc()
	for _, sub := range plain.ModelSubmeshes() {
		if sub.SkinIdx != nil || sub.SkinWt != nil {
			t.Error("an unrigged model produced skinning attributes")
		}
		if len(sub.SrcVerts) != len(sub.Vertices) {
			t.Errorf("SrcVerts has %d entries for %d vertices", len(sub.SrcVerts), len(sub.Vertices))
		}
	}

	rigged, idx := modeltest.BoneChain(t)
	rigged.AssignVertsToBone(modeltest.AllVerts(rigged), idx[0], 1)
	for _, sub := range rigged.ModelSubmeshes() {
		if len(sub.SkinIdx) != len(sub.Vertices) || len(sub.SkinWt) != len(sub.Vertices) {
			t.Fatalf("rigged submesh has %d/%d skin records for %d vertices",
				len(sub.SkinIdx), len(sub.SkinWt), len(sub.Vertices))
		}
		// Every record must describe the vertex it sits beside.
		for i, src := range sub.SrcVerts {
			want, _ := model.SkinAttrsFor(rigged.WeightsFor(src))
			for j := range want {
				if sub.SkinIdx[i][j] != want[j] {
					t.Fatalf("record %d (vertex %d) bound to the wrong bone: %v, want %v",
						i, src, sub.SkinIdx[i], want)
				}
			}
		}
	}
}
