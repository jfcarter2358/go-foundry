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
	"foundry/internal/material"
	"foundry/internal/world"
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
	// visKeyTrail is the splat's soft layers reading the ground trails
	// (FX_TRAIL, 256; render/ground_trails.go).
	visKeyTrail = 1 << 12
	// The frame's switches (visFrameKey): set, each says a frame-wide path
	// is OFF this frame, and its tests are folded across the whole material
	// source, not only main — the GI probes inside ambientLight
	// (light_model.glsl) and specular.glsl, the water caustics. Unset (the
	// key 0 the tests use), the uniforms decide as before.
	visKeyNoGI       = 1 << 13
	visKeyNoCaustics = 1 << 14
	// visKeySplatLayout: the key's upper 32 bits are the splat's layer
	// layout (visSplatLayout), folded into fxSplatSurface as literals.
	visKeySplatLayout = 1 << 15
	// visKeyNoCascades: no directional light reads cascades this frame —
	// each shadowed one is the virtual shadow map's — so the cascade path
	// (its slice choice, blend and PCSS) is folded out of
	// calcDirShadowUnfaded.
	visKeyNoCascades = 1 << 16
	// visKeyNoSSR: the environment is not the High tier this frame, so the
	// screen-space reflection march (specular.glsl's ssrTrace) never runs;
	// visKeyNoReflVolumes: no reflection volume is packed, so the
	// environment's box-projected loop never runs.
	visKeyNoSSR         = 1 << 17
	visKeyNoReflVolumes = 1 << 18
	// The lighting settings a player turned off (docs/adventure/PHASE_F.md,
	// Phase F2; NaniteMatSettingFold): each one's code is folded out of the
	// variant, so turning it off gives back its registers as well as its
	// work. visKeyNoSoftShadows: soft_shadows off (vsm.glsl's PCSS);
	// visKeyNoSSAO: nothing makes the occlusion texture (ssaoAt);
	// visKeyNoContact: contact_shadows off; visKeyNoSpecular: specular off;
	// visKeyNoEnv: reflections off (pbrEnvSpecular).
	visKeyNoSoftShadows = 1 << 19
	visKeyNoSSAO        = 1 << 20
	visKeyNoContact     = 1 << 21
	visKeyNoSpecular    = 1 << 22
	visKeyNoEnv         = 1 << 23
	// visKeyFrameMask is every frame-wide bit: a key with any of them
	// expands the includes before folding.
	visKeyFrameMask = visKeyNoGI | visKeyNoCaustics | visKeyNoCascades | visKeyNoSSR | visKeyNoReflVolumes |
		visKeyNoSoftShadows | visKeyNoSSAO | visKeyNoContact | visKeyNoSpecular | visKeyNoEnv
)

// NaniteMatSettingFold switches the setting folds (visKeyNoSoftShadows and
// the rest). FOUNDRY_NANITE_MAT_SETTING_FOLD=0: the uniforms decide, as
// before.
var NaniteMatSettingFold = gfx.EnvFlag("FOUNDRY_NANITE_MAT_SETTING_FOLD", true)

// NaniteMatSplatFold switches the splat layout fold (visKeySplatLayout;
// docs/adventure/PHASE_F.md). Each layer's triplanar, anti-tiling, relief
// and strata switches are uniforms the same for every pixel of a material,
// but the generic splat compiles every combination into each of its eight
// inlined layer samples: the adventure level's rock walls (one triplanar
// layer on nearly every pixel) ran a program of 150 vec4 temporaries, the
// 1070 spilled, and their bins were 22–30 ms of a 2560x1080 frame.
var NaniteMatSplatFold = gfx.EnvFlag("FOUNDRY_NANITE_MAT_SPLAT_FOLD", true)

// levelHasReflectionVolumes reports whether any of lvl's volumes reflects.
func levelHasReflectionVolumes(lvl *world.Level) bool {
	if lvl == nil {
		return false
	}
	for i := range lvl.Volumes {
		if lvl.Volumes[i].HasReflection() {
			return true
		}
	}
	return false
}

// warmMaterialVariants builds the program of every material key lvl's
// Nanite scenery can be shaded with — every placement, its chunk on or off —
// the first frame a level (or a frame switch) is shaded. The driver takes
// seconds to build one of these programs; built lazily, a chunk the level's
// script switched on in play stalled the frame that first drew it (the
// adventure level's cave flight: 1–2 s; docs/adventure/PHASE_F.md).
// FOUNDRY_NANITE_MAT_WARM=0 leaves them to first use.
func (v *naniteVisPass) warmMaterialVariants(lvl *world.Level, frameKey uint32) {
	if !NaniteMatWarm || !NaniteMatVariants || v.matSrc == "" || lvl == nil {
		return
	}
	// Near water (phase W5): the caustic variants too, once per level.
	wantWater := frameKey&visKeyNoCaustics != 0 && waterInteract.near && visWarmedWater != lvl
	if v.warmed == lvl && v.warmedKey == frameKey && v.warmedN == len(lvl.Scenery) && !wantWater {
		return
	}
	v.warmed, v.warmedKey, v.warmedN = lvl, frameKey, len(lvl.Scenery)
	if wantWater {
		visWarmedWater = lvl
	}
	type slotKey struct {
		tex, mat string
		smooth   bool
	}
	seen := map[slotKey]bool{}
	built := map[uint64]bool{}
	// Water is drawn both ways: with the bed-side caustics folded out
	// (above water) and in (the camera under a surface, phase W5's
	// water_interact.go). The caustic variants are built once the camera
	// comes within reach of a surface (waterInteract.near), so diving in
	// does not stall on a build and a level is not made to build twice
	// its programs at load for water nobody goes near.
	keys := []uint32{frameKey}
	if wantWater || (visWarmedWater == lvl && frameKey&visKeyNoCaustics != 0) {
		keys = append(keys, frameKey&^visKeyNoCaustics)
	}
	add := func(tex, mat string, smooth bool) {
		sk := slotKey{tex, mat, smooth}
		if seen[sk] {
			return
		}
		seen[sk] = true
		m := gfx.Material{Texture: tex, MaterialName: mat, Alpha: 1, Normals: true, Smooth: smooth}
		st := &frameSurface(&m).st
		// Unbaked: a placement's bin never carries baked light (that is
		// the compiled level's static surfaces').
		for _, fk := range keys {
			k := uint64(visShadeKey(st, false)|fk) | visSplatLayout(st)
			if !built[k] {
				built[k] = true
				v.materialProgram(k)
			}
		}
	}
	for i := range lvl.Scenery {
		s := &lvl.Scenery[i]
		if !s.NaniteGPU || s.Nanite == nil {
			continue
		}
		for si, slot := range s.Nanite.Slots {
			if slot.Count > 0 {
				add(s.SlotTexture(si), s.SlotMaterial(si), slot.Smooth)
			}
		}
		if s.HasImpostor() && s.ImpostorMesh != nil {
			for _, slot := range s.ImpostorMesh.Slots {
				if slot.Count > 0 {
					add(slot.Texture, "", slot.Smooth)
				}
			}
		}
	}
}

// visWarmedWater is the level whose caustic variants warmMaterialVariants
// built (phase W5).
var visWarmedWater *world.Level

// NaniteMatWarm switches warmMaterialVariants.
var NaniteMatWarm = gfx.EnvFlag("FOUNDRY_NANITE_MAT_WARM", true)

// visSplatLayout is st's splat layout for the key's upper word, 4 bits a
// layer (triplanar, anti-tiled, relief, strata) — exactly what applySplat
// writes into fxSplatTri/Anti/Relief/StrataOn (and their *2 in eight-layer
// mode) — with visKeySplatLayout set; 0 when there is no splat to fold.
func visSplatLayout(st *gfx.SurfaceState) uint64 {
	f := &st.Fx
	if !NaniteMatSplatFold || !f.HasSplat || gfx.FxDebugMask&visKeySplat == 0 {
		return 0
	}
	n := material.SplatLayers
	if f.SplatEight {
		n = material.SplatLayersMax
	}
	var lay uint64
	for i := 0; i < n; i++ {
		sl := &f.SplatLayers[i]
		var b uint64
		if sl.Triplanar {
			b |= 1
		}
		if sl.AntiTile {
			b |= 2
		}
		if !sl.Triplanar && sl.Relief > 0 {
			b |= 4
		}
		if !sl.NoStrata {
			b |= 8
		}
		lay |= b << (4 * i)
	}
	return lay<<32 | visKeySplatLayout
}

// splatLayoutVec is the literal vec4 for layers [first, first+4) of the
// layout: bit's 1.0/0.0, or for relief the uniform's own component where
// the layer has relief (its depth is not folded, only whether it has one).
func splatLayoutVec(lay uint64, first, bit int, keep string) string {
	parts := make([]string, 4)
	for i := 0; i < 4; i++ {
		on := lay>>(4*(first+i)+bit)&1 != 0
		switch {
		case keep != "" && on:
			parts[i] = keep + "." + string("xyzw"[i])
		case on:
			parts[i] = "1.0"
		default:
			parts[i] = "0.0"
		}
	}
	return "vec4(" + strings.Join(parts, ", ") + ")"
}

// visSplatSpecs are the splat's uniform reads the layout fold replaces in
// surface_fx.glsl's fxSplatSurface: the text, and what it becomes for a
// layout (lay: the key's upper word).
var visSplatSpecs = []struct {
	text string
	with func(lay uint64) string
}{
	{"T = fxSplatTri,", func(l uint64) string { return "T = " + splatLayoutVec(l, 0, 0, "") + "," }},
	{"A = fxSplatAnti,", func(l uint64) string { return "A = " + splatLayoutVec(l, 0, 1, "") + "," }},
	{"R = fxSplatRelief;", func(l uint64) string { return "R = " + splatLayoutVec(l, 0, 2, "fxSplatRelief") + ";" }},
	{"vec4 Q = fxSplatStrataOn;", func(l uint64) string { return "vec4 Q = " + splatLayoutVec(l, 0, 3, "") + ";" }},
	{"T2 = fxSplatTri2;", func(l uint64) string { return "T2 = " + splatLayoutVec(l, 4, 0, "") + ";" }},
	{"A2 = fxSplatAnti2;", func(l uint64) string { return "A2 = " + splatLayoutVec(l, 4, 1, "") + ";" }},
	{"R2 = fxSplatRelief2;", func(l uint64) string { return "R2 = " + splatLayoutVec(l, 4, 2, "fxSplatRelief2") + ";" }},
	{"Q2 = fxSplatStrataOn2;", func(l uint64) string { return "Q2 = " + splatLayoutVec(l, 4, 3, "") + ";" }},
}

// NaniteMatFrameFold switches the frame-wide folds (visKeyNoGI,
// visKeyNoCaustics; docs/adventure/PHASE_F.md). With GI off the probe
// lookups behind `giEnabled` were still compiled into every material
// program, and the 1070 sized every pixel's registers for them: 4–6 ms of
// the adventure level's material pass at 2560x1080.
var NaniteMatFrameFold = gfx.EnvFlag("FOUNDRY_NANITE_MAT_FRAME_FOLD", true)

// visFrameSpecs are the frame-wide tests a variant folds when its key says
// the path is off, each with what it then reads as. Each must be exactly
// what the uniform holds whenever the bit is set: giEnabled is 0 whenever
// GI did not run this frame (uploadGITo), waterCausticCount 0 with no
// bodies (uploadWaterCausticsTo).
var visFrameSpecs = []struct {
	test, off string
	bit       uint32
}{
	{"giEnabled == 0", "true", visKeyNoGI},
	{"giEnabled != 0", "false", visKeyNoGI},
	{"waterCausticCount > 0", "false", visKeyNoCaustics},
	// Exact when every light either is the VSM's (vsmShadows answers first)
	// or has no cascade layer (UploadShadowUniformsTo writes -1).
	{"if (layerBase < 0) {", "if (true) {", visKeyNoCascades},
	// The scene block's envOn (render/envmap.go) is 2 only for High.
	{"envOn != 2", "true", visKeyNoSSR},
	{"i >= numReflVolumes", "true", visKeyNoReflVolumes},
	// The player's lighting settings (Phase F2): softShadowsOn is
	// boolInt(gfx.SoftShadows) (world/light.go); ssaoOn is 0 unless
	// aoWanted (ao.go); contactShadowsOn 0 without gfx.ContactShadows;
	// specularOn is boolInt(gfx.SpecularLighting); envOn is 0 whenever
	// reflections are off (envWanted, packEnvOff).
	{"softShadowsOn != 0", "false", visKeyNoSoftShadows},
	{"ssaoOn == 0", "true", visKeyNoSSAO},
	{"i + 1 == contactShadowsOn", "false", visKeyNoContact},
	{"specularOn != 0", "false", visKeyNoSpecular},
	{"envOn == 0", "true", visKeyNoEnv},
}

// visFrameKey is this frame's switches for visShadeKey's key. Each comes
// from a setting or from the level as a whole wherever it can, so that it
// does not flip in play: a new key is a new program, and the driver takes
// seconds to build one (warmMaterialVariants builds a level's at load).
func visFrameKey(lvl *world.Level, frame ShadowFrame) uint32 {
	if !NaniteMatFrameFold {
		return 0
	}
	var k uint32
	cascades := false
	for i := range frame.Lights {
		if frame.Lights[i].BaseLayer >= 0 && (frame.VSM == nil || frame.VSM.Light != i) {
			cascades = true
		}
	}
	if !cascades {
		k |= visKeyNoCascades
	}
	// envOn is 2 only for the High tier (envmap.go); a level with no
	// reflection volume never packs one.
	if gfx.Reflections != gfx.ReflectionsHigh {
		k |= visKeyNoSSR
	}
	if !levelHasReflectionVolumes(lvl) {
		k |= visKeyNoReflVolumes
	}
	// GI off by the player's setting never runs (giEnabled stays 0); on, the
	// uniform decides, ready or not.
	if gfx.GIQuality == gfx.GIOff {
		k |= visKeyNoGI
	}
	if waterBodies.count == 0 {
		k |= visKeyNoCaustics
	}
	if NaniteMatSettingFold {
		if !gfx.SoftShadows {
			k |= visKeyNoSoftShadows
		}
		if !gfx.AmbientOcclusion && !gfx.ContactShadows && gfx.Reflections != gfx.ReflectionsHigh {
			k |= visKeyNoSSAO
		}
		if !gfx.ContactShadows {
			k |= visKeyNoContact
		}
		if !gfx.SpecularLighting {
			k |= visKeyNoSpecular
		}
		if gfx.Reflections == gfx.ReflectionsOff {
			k |= visKeyNoEnv
		}
	}
	return k
}

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
	{"(matFx & FX_TRAIL) != 0", visKeyTrail},
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
	// The ground trails: declared by a soft layer, whether or not this
	// frame has a trail map (the uniform bit says that); FX_TRAIL is the
	// mask's 256.
	if f.HasSoft() && gfx.FxDebugMask&256 != 0 {
		k |= visKeyTrail
	}
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
func specializeMaterialSource(src string, key uint64, strict bool) (string, error) {
	start := strings.Index(src, "void nvShadeMain()")
	end := strings.Index(src, "\n// ---- vertex.glsl, as a function")
	if start < 0 || end < start {
		return "", fmt.Errorf("material pass: no nvShadeMain to specialise")
	}
	// The frame's switches and the splat layout fold text in files the
	// source only #includes (light_model.glsl, specular.glsl,
	// surface_fx.glsl): expanded here, they are in reach, and CompileShader
	// finds nothing left to expand.
	if key&(uint64(visKeyFrameMask)|visKeySplatLayout) != 0 {
		x, err := gfx.ExpandShaderIncludes(src)
		if err != nil {
			return "", fmt.Errorf("material pass: %v", err)
		}
		src = x
		start = strings.Index(src, "void nvShadeMain()")
		end = strings.Index(src, "\n// ---- vertex.glsl, as a function")
		if start < 0 || end < start {
			return "", fmt.Errorf("material pass: no nvShadeMain to specialise")
		}
	}
	body := src[start:end]
	// The frame's switches, over the whole source (the functions main calls
	// as well): the unfolded form keeps the uniform test, so a test nothing
	// ever folded compiles as it always did.
	for _, s := range visFrameSpecs {
		if key&uint64(s.bit) == 0 {
			continue
		}
		with := "(" + s.off + ")"
		if strings.HasPrefix(s.test, "if (") {
			with = s.off // a whole statement head, not an expression
		}
		src = strings.ReplaceAll(src, s.test, with)
		body = strings.ReplaceAll(body, s.test, with)
	}
	// The splat's layout, as literals (surface_fx.glsl, outside main).
	if key&visKeySplatLayout != 0 {
		lay := key >> 32
		for _, s := range visSplatSpecs {
			if !strings.Contains(src, s.text) {
				if strict {
					return "", fmt.Errorf("surface_fx.glsl no longer reads %q; visSplatSpecs needs updating", s.text)
				}
				continue
			}
			src = strings.ReplaceAll(src, s.text, s.with(lay))
		}
	}
	start = strings.Index(src, "void nvShadeMain()")
	end = strings.Index(src, "\n// ---- vertex.glsl, as a function")
	for _, s := range visSpecs {
		if !strings.Contains(body, s.test) {
			if strict {
				return "", fmt.Errorf("fragment.glsl's main no longer tests %q; the material variants need updating", s.test)
			}
			continue
		}
		keep := "false"
		if s.bit != 0 && key&uint64(s.bit) != 0 {
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
func (v *naniteVisPass) materialProgram(k uint64) *visMatProg {
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
		v.variants = map[uint64]*visMatProg{}
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
	dumpMaterialProgram(k, prog, src)
	p := newVisMatProg(prog)
	v.variants[k] = p
	return p
}
