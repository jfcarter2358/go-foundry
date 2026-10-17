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
		out, err := specializeMaterialSource(src, uint64(key), true)
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

// TestMaterialVariantsFoldTheFrameSwitches: a key saying GI and the caustics
// are off this frame takes every test of them out of the whole source — the
// probe lookups behind ambientLight's giEnabled were the point
// (docs/adventure/PHASE_F.md) — and a key without those bits leaves every one.
func TestMaterialVariantsFoldTheFrameSwitches(t *testing.T) {
	src := materialSourceForTest(t)
	full, err := gfx.ExpandShaderIncludes(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range visFrameSpecs {
		if !strings.Contains(full, s.test) {
			t.Fatalf("the material source no longer tests %q; visFrameSpecs needs updating", s.test)
		}
	}
	out, err := specializeMaterialSource(src, visKeyFrameMask, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range visFrameSpecs {
		if strings.Contains(out, s.test) {
			t.Errorf("folded variant still tests %q", s.test)
		}
	}
	kept, err := specializeMaterialSource(src, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range visFrameSpecs {
		if strings.Count(kept, s.test) != strings.Count(src, s.test) && strings.Count(kept, s.test) != strings.Count(full, s.test) {
			t.Errorf("key 0 folded %q", s.test)
		}
	}
}

// TestMaterialVariantsFoldTheSplatLayout: a splat's layout key replaces each
// layer switch fxSplatSurface reads with the literal applySplat would have
// written — relief keeping its uniform's depth where the layer has one — and
// a key without the layout bit leaves the uniforms (docs/adventure/PHASE_F.md).
func TestMaterialVariantsFoldTheSplatLayout(t *testing.T) {
	src := materialSourceForTest(t)
	var st gfx.SurfaceState
	st.Fx.HasSplat = true
	st.Fx.SplatLayers[0] = material.SplatLayer{Texture: "sand", AntiTile: true}
	st.Fx.SplatLayers[1] = material.SplatLayer{Texture: "gravel", Relief: 0.07, NoStrata: true}
	st.Fx.SplatLayers[3] = material.SplatLayer{Texture: "rock", Triplanar: true, AntiTile: true, Relief: 0.5}
	lay := visSplatLayout(&st)
	if lay&visKeySplatLayout == 0 {
		t.Fatal("a splat surface has no layout key")
	}
	out, err := specializeMaterialSource(src, visKeySplat|lay, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"T = vec4(0.0, 0.0, 0.0, 1.0),",
		"A = vec4(1.0, 0.0, 0.0, 1.0),",
		// Layer 4 is triplanar: applySplat writes it no relief.
		"R = vec4(0.0, fxSplatRelief.y, 0.0, 0.0);",
		// Absent layer 3 has NoStrata false, as applySplat's b2f(!NoStrata).
		"vec4 Q = vec4(1.0, 0.0, 1.0, 1.0);",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("folded source has no %q", want)
		}
	}
	plain, err := specializeMaterialSource(src, visKeySplat, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range visSplatSpecs {
		if strings.Count(plain, s.text) != strings.Count(src, s.text) {
			t.Errorf("no layout bit, yet %q was folded", s.text)
		}
	}
	var none gfx.SurfaceState
	if visSplatLayout(&none) != 0 {
		t.Error("a surface with no splat has a layout key")
	}
}
