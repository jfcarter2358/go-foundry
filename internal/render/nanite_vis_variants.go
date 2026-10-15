package render

// nanite_vis_variants.go — the material pass's programs SPECIALISED per kind
// of surface (docs/canyon/PERF.md).
//
// The material pass (nanite_vis.go) runs fragment.glsl's whole main for every
// pixel of every bin, and fragment.glsl is an uber-shader: water, texture
// blending, baked light, decal masks and the seven terrain/rock effects of
// surface_fx.glsl are all there behind uniform branches. A uniform branch
// costs nothing to skip, but the code behind it still has to be compiled into
// the one program, and the GTX 1070 sizes every pixel's registers for the
// worst path through it. On the generated canyon that was most of the frame:
// the material pass cost 15–20 ms at 2560x1080, of which a pixel's actual
// work — measured by stripping the dead paths from the source — was a third.
//
// So each bin is shaded by a program compiled for what its surface CAN use.
// A variant is fragment.glsl's main with each such test rewritten
// `(false && test)` — or `(true && test)`, which leaves the uniform deciding
// — so the compiler drops the paths the bin's surface never takes. Its key
// (visShadeKey) is read off the surface state the bin is shaded with: water,
// a blend texture, baked light, a modulate decal, and each effect the
// material declares. Declared, not applied: an effect whose textures are
// missing still turns its uniform bit off at draw time (applySurfaceFx), and
// the `true &&` form honours that, so a variant can only ever skip work that
// the generic program would have skipped too. Same picture, to the bit.
//
// Variants compile the first time a bin needs one (a level has a handful:
// terrain splat, scan rocks with ground blend and streaks, plain surfaces)
// and are kept for the run; one that fails to compile falls back to the
// generic program, which is what FOUNDRY_NANITE_MAT_VARIANTS=0 uses for every
// bin.

import (
	"fmt"
	"log"
	"strings"

	"foundry/internal/gfx"
)

// NaniteMatVariants switches the specialised material programs; see the file
// comment.
var NaniteMatVariants = gfx.EnvFlag("FOUNDRY_NANITE_MAT_VARIANTS", true)

// The key's bits. The fx bits are surface_fx.glsl's FX_* values, so the key's
// low byte is the matFx a surface can have.
const (
	visKeyTriplanar = 1 << 0
	visKeySplat     = 1 << 1
	visKeyGround    = 1 << 2
	visKeyStreaks   = 1 << 3
	visKeyDetail    = 1 << 4
	visKeyMacro     = 1 << 5
	visKeyGrade     = 1 << 6
	// visKeySplat8 is the splat's eight-layer mode (FX_SPLAT8).
	visKeySplat8 = 1 << 7
	visKeyWater  = 1 << 8
	visKeyBlend  = 1 << 9
	visKeyBaked  = 1 << 10
	visKeyDecal  = 1 << 11
)

// visSpecs are the tests in fragment.glsl's main a variant fixes, each with
// the key bit that keeps it. alphaSharpen has none: the material pass always
// sets it 0 (frameUniforms).
var visSpecs = []struct {
	test string
	bit  uint32
}{
	{"isWater != 0", visKeyWater},
	{"hasBlend != 0", visKeyBlend},
	{"bakedLight != 0", visKeyBaked},
	{"surfaceBlend == 2", visKeyDecal},
	{"alphaSharpen != 0", 0},
	{"(matFx & FX_TRIPLANAR) != 0", visKeyTriplanar},
	{"(matFx & FX_SPLAT) != 0", visKeySplat},
	{"(matFx & FX_GROUND) != 0", visKeyGround},
	{"(matFx & FX_STREAKS) != 0", visKeyStreaks},
	{"(matFx & FX_DETAIL) != 0", visKeyDetail},
	{"(matFx & FX_MACRO) != 0", visKeyMacro},
	{"(matFx & FX_GRADE) != 0", visKeyGrade},
	{"(matFx & FX_SPLAT8) != 0", visKeySplat8},
}

// visShadeKey is the variant a bin of surface st is shaded with.
func visShadeKey(st *gfx.SurfaceState, baked bool) uint32 {
	var k uint32
	f := &st.Fx
	for _, b := range []struct {
		on  bool
		bit uint32
	}{
		{f.HasTriplanar, visKeyTriplanar}, {f.HasSplat, visKeySplat}, {f.HasGroundBlend, visKeyGround},
		{f.HasStreaks, visKeyStreaks}, {f.HasDetailNormal, visKeyDetail}, {f.HasMacro, visKeyMacro},
		{f.HasGrade, visKeyGrade}, {f.HasSplat && f.SplatEight, visKeySplat8},
	} {
		if b.on {
			k |= b.bit
		}
	}
	// The A/B mask (gfx.FxDebugMask) takes the effects out of the variant too.
	k &= uint32(gfx.FxDebugMask) & 0xff
	if st.Water.Enabled {
		k |= visKeyWater
	}
	if st.BlendTexture != "" && st.BlendMask != "" {
		k |= visKeyBlend
	}
	if baked {
		k |= visKeyBaked
	}
	if st.Blend == gfx.SurfaceBlendModulate {
		k |= visKeyDecal
	}
	return k
}

// specializeMaterialSource is the assembled material-pass source src with
// fragment.glsl's main (nvShadeMain, up to the vertex part) specialised to
// key. strict reports a test the main no longer contains as an error (the
// tests); otherwise that test is left to its uniform.
func specializeMaterialSource(src string, key uint32, strict bool) (string, error) {
	start := strings.Index(src, "void nvShadeMain()")
	end := strings.Index(src, "\n// ---- vertex.glsl, as a function")
	if start < 0 || end < start {
		return "", fmt.Errorf("material pass: no nvShadeMain to specialise")
	}
	body := src[start:end]
	for _, s := range visSpecs {
		if !strings.Contains(body, s.test) {
			if strict {
				return "", fmt.Errorf("fragment.glsl's main no longer tests %q; the material variants need updating", s.test)
			}
			continue
		}
		keep := "false"
		if s.bit != 0 && key&s.bit != 0 {
			keep = "true"
		}
		body = strings.ReplaceAll(body, s.test, "("+keep+" && ("+s.test+"))")
	}
	return src[:start] + body + src[end:], nil
}

// visMatProg is one material program and its per-bin locations.
type visMatProg struct {
	progUniforms
	locs                                gfx.SurfaceLocs
	baked, hasNormals, hasLight, binLoc int32
	failed                              bool
	// frame is the last DrawWorld frame its frame uniforms were sent in.
	frame uint64
}

func newVisMatProg(prog uint32) *visMatProg {
	p := &visMatProg{progUniforms: progUniforms{prog: prog, loc: map[string]int32{}}}
	p.locs = gfx.NewSurfaceLocs(prog)
	p.baked, p.hasNormals = p.U("bakedLight"), p.U("hasNormals")
	p.hasLight, p.binLoc = p.U("hasLight"), p.U("bin")
	return p
}

// materialProgram is the program bin key k is shaded with: its variant, or
// the generic program when variants are off or this one would not build.
func (v *naniteVisPass) materialProgram(k uint32) *visMatProg {
	if v.generic == nil {
		v.generic = &visMatProg{progUniforms: v.material, locs: v.matLocs,
			baked: v.matBaked, hasNormals: v.matHasNormals, hasLight: v.matHasLight, binLoc: v.matBin}
	}
	if !NaniteMatVariants || v.matSrc == "" {
		return v.generic
	}
	if p, ok := v.variants[k]; ok {
		if p.failed {
			return v.generic
		}
		return p
	}
	if v.variants == nil {
		v.variants = map[uint32]*visMatProg{}
	}
	src, err := specializeMaterialSource(v.matSrc, k, false)
	var prog uint32
	if err == nil {
		prog, err = gfx.CompileProgramFromSources(mustReadShader(biomeShaderPath("nanite_material_vert.glsl")), src)
	}
	if err != nil {
		log.Printf("nanite: material variant %#x: %v — shaded by the generic program", k, err)
		v.variants[k] = &visMatProg{failed: true}
		return v.generic
	}
	p := newVisMatProg(prog)
	v.variants[k] = p
	return p
}
