package render

// nanite_vis_variants_test.go — the material pass's specialised programs
// (nanite_vis_variants.go) are fragment.glsl's main with its surface tests
// fixed. These hold the rewrite to the file in the repository: every test it
// fixes is still there, a key keeps exactly the tests of its bits, and a
// surface's key says what it can use. (That a variant compiles and shades
// as the generic program does is nanite_vis_variants_gl_test.go's.)

import (
	"strings"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/material"
)

func materialSourceForTest(t *testing.T) string {
	t.Helper()
	vert, frag := worldSourcesForTest(t)
	src, err := materialFragmentSource(vert, frag, mustReadShader(repoShader(t, "nanite_material_frag.glsl")))
	if err != nil {
		t.Fatal(err)
	}
	return src
}

func TestMaterialVariantsFindEveryTestTheyFix(t *testing.T) {
	src := materialSourceForTest(t)
	for _, key := range []uint32{0, visKeySplat | visKeyMacro | visKeyStreaks, 0xfff} {
		out, err := specializeMaterialSource(src, key, true)
		if err != nil {
			t.Fatalf("key %#x: %v", key, err)
		}
		for _, s := range visSpecs {
			keep := s.bit != 0 && key&s.bit != 0
			want := "(false && (" + s.test + "))"
			if keep {
				want = "(true && (" + s.test + "))"
			}
			if !strings.Contains(out, want) {
				t.Errorf("key %#x: no %q", key, want)
			}
		}
	}
}

func TestMaterialVariantsTouchOnlyTheShadingMain(t *testing.T) {
	src := materialSourceForTest(t)
	out, err := specializeMaterialSource(src, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(src, "void nvShadeMain()")
	if out[:start] != src[:start] {
		t.Error("the variant changed the source before nvShadeMain")
	}
	tail := "\n// ---- vertex.glsl, as a function"
	if out[strings.Index(out, tail):] != src[strings.Index(src, tail):] {
		t.Error("the variant changed the source after nvShadeMain")
	}
	if _, err := specializeMaterialSource(strings.Replace(src, "hasBlend != 0", "hasBlend > 0", -1), 0, true); err == nil {
		t.Error("a main that no longer tests hasBlend was specialised without complaint")
	}
}

func TestMaterialVariantKeyIsWhatTheSurfaceCanUse(t *testing.T) {
	var plain gfx.SurfaceState
	if k := visShadeKey(&plain, false); k != 0 {
		t.Errorf("a plain surface's key is %#x, want 0", k)
	}
	splat := gfx.SurfaceState{Fx: material.SurfaceFx{HasSplat: true, HasMacro: true, HasStreaks: true}}
	if k := visShadeKey(&splat, true); k != visKeySplat|visKeyMacro|visKeyStreaks|visKeyBaked {
		t.Errorf("a baked splat terrain's key is %#x", k)
	}
	blend := gfx.SurfaceState{BlendTexture: "a", BlendMask: "b", Water: gfx.WaterParams{Enabled: true},
		Blend: gfx.SurfaceBlendModulate}
	if k := visShadeKey(&blend, false); k != visKeyBlend|visKeyWater|visKeyDecal {
		t.Errorf("a blended, watery, modulate surface's key is %#x", k)
	}
	old := gfx.FxDebugMask
	defer func() { gfx.FxDebugMask = old }()
	gfx.FxDebugMask = ^int32(visKeySplat)
	if k := visShadeKey(&splat, false); k&visKeySplat != 0 {
		t.Error("FOUNDRY_FX_MASK's switch did not take the splat out of the key")
	}
}
