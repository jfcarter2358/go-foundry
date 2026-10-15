package gfx

// material_fx.go — the terrain and rock material effects on a world-program
// draw (docs/canyon/PHASE_5.md; the kinds are internal/material/
// material_kinds_terrain.go, the shader resources/shaders/surface_fx.glsl).
//
// A SurfaceState carries them as Fx, the material's resolved material.
// SurfaceFx — a plain comparable value, so the visibility buffer's material
// pass can still tell two bins' states apart with != and skip re-applying an
// identical one. Applying them is two steps:
//
//   - matFx, a bitmask, written on EVERY draw like the rest of a surface's
//     uniforms, so no surface leaks its effects onto the next. Zero — what GL
//     gives the uniform in a program that never writes it, and what every
//     surface without these effects sends — takes none of the shader's
//     branches.
//   - only when it is non-zero, the parameters and the textures the effects
//     named. A surface without them pays one Uniform1i.
//
// An effect whose textures cannot be had (a splat whose layer arrays failed
// to build, a ground blend whose maps are missing) leaves its bit off, so the
// surface draws as if it did not carry it rather than sampling black.
//
// The textures are on units 24-31 (FxUnit*), which nothing else that links
// fragment.glsl uses — but the samplers point there only while an effect
// draws (see BindSurfaceFxSamplers for why, and for where they rest).
//
// EIGHT-LAYER SPLAT (docs/regions/PHASE_3.md). A splat in eight-layer mode
// (material.SurfaceFx.SplatEight) also binds its second weights map on unit
// 31 and writes layers 5–8's uniforms (the *2 vec4s, the tints' second
// half), and sets its own matFx bit, fxSplat8, which is the shader's ONLY
// way into the 5–8 code. A four-layer splat leaves that bit off, so its
// pixels run the four-layer path; the visibility buffer's material
// variants (render/nanite_vis_variants.go) also compile that path out of
// every bin that has no eight-layer surface.
//
// SHARED ATLAS (docs/regions/PHASE_6.md, "Per-tile materials"). A splat
// whose `atlas1..8` name a set of layers (material.SurfaceFx.SplatAtlas) is
// packed into THAT set's atlas, and each of its layers samples the tile of
// the same texture there (splatLayerTiles). A region terrain's four-layer
// tile material names its eight-layer union's layers, so the two share one
// atlas pair — no second copy in video memory — and a tile and its
// eight-layer neighbour read the very same texels at the same mips.

import (
	"math"
	"os"
	"strconv"

	"foundry/internal/material"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// The effects' texture units.
const (
	FxUnitSplatWeights = 24
	FxUnitSplatAlbedo  = 25 // a 2×2 atlas of the layers
	FxUnitSplatNormal  = 26 // likewise
	FxUnitStrata       = 27
	FxUnitGroundColor  = 28
	FxUnitGroundHeight = 29
	FxUnitDetail       = 30
	// FxUnitSplatWeights2 is an eight-layer splat's second weights map
	// (layers 5–8). 31: free in every program that links fragment.glsl
	// (render's TestFxUnitsAreFree).
	FxUnitSplatWeights2 = 31
)

// The matFx bits: surface_fx.glsl's FX_*.
const (
	fxTriplanar = 1 << iota
	fxSplat
	fxGround
	fxStreaks
	fxDetail
	fxMacro
	fxGrade
	// fxSplat8 is the splat's eight-layer mode, set beside fxSplat.
	fxSplat8
)

// FxLocs is one program's locations for the effects' uniforms.
type FxLocs struct {
	mask, triplanar                                 int32
	splatWeights, splatAlbedo, splatNormal          int32
	splatRect, splatInvScale, splatTri, splatHeight int32
	splatLayer, splatTint, splatParams, splatAtlas  int32
	strata, strataParams, splatBedding              int32
	splatAnti, splatRelief, splatRipple             int32
	// Eight-layer mode: the second weights map, layers 5–8's vec4s, the
	// per-layer strata switches and the atlas's columns.
	splatWeights2, splatInvScale2, splatTri2, splatHeight2 int32
	splatLayer2, splatAnti2, splatRelief2                  int32
	splatStrataOn, splatStrataOn2, splatCols               int32
	// splatRows is a palette atlas's rows (fxSplatRows; 2 otherwise).
	splatRows                                          int32
	groundColor, groundHeight, groundRect, groundRange int32
	groundParams                                       int32
	streaks, streakColor                               int32
	detail, detailParams                               int32
	macro, gradeMatch, gradeHS, gradeTint              int32
	// prog is the program they are of; bound is whether
	// BindSurfaceFxSamplers has pointed its samplers at the rest units
	// (fxLocsFor's cached copy only).
	prog  uint32
	bound bool
}

// NewFxLocs resolves prog's FxLocs.
func NewFxLocs(prog uint32) FxLocs {
	loc := func(name string) int32 { return UniformLoc(prog, name+"\x00") }
	return FxLocs{
		prog: prog,
		mask: loc("matFx"), triplanar: loc("fxTriplanar"),
		splatWeights: loc("fxSplatWeights"), splatAlbedo: loc("fxSplatAlbedo"), splatNormal: loc("fxSplatNormal"),
		splatRect: loc("fxSplatRect"), splatInvScale: loc("fxSplatInvScale"), splatTri: loc("fxSplatTri"),
		splatHeight: loc("fxSplatHeight"), splatLayer: loc("fxSplatLayer"), splatTint: loc("fxSplatTint"),
		splatParams: loc("fxSplatParams"), splatAtlas: loc("fxSplatAtlas"), strata: loc("fxStrata"), strataParams: loc("fxStrataParams"),
		groundColor: loc("fxGroundColor"), groundHeight: loc("fxGroundHeight"), groundRect: loc("fxGroundRect"),
		groundRange: loc("fxGroundRange"), groundParams: loc("fxGroundParams"),
		streaks: loc("fxStreaks"), streakColor: loc("fxStreakColor"),
		detail: loc("fxDetail"), detailParams: loc("fxDetailParams"),
		macro: loc("fxMacro"), gradeMatch: loc("fxGradeMatch"), gradeHS: loc("fxGradeHS"), gradeTint: loc("fxGradeTint"),
		splatBedding: loc("fxSplatBedding"), splatAnti: loc("fxSplatAnti"), splatRelief: loc("fxSplatRelief"),
		splatRipple:   loc("fxSplatRipple"),
		splatWeights2: loc("fxSplatWeights2"), splatInvScale2: loc("fxSplatInvScale2"), splatTri2: loc("fxSplatTri2"),
		splatHeight2: loc("fxSplatHeight2"), splatLayer2: loc("fxSplatLayer2"), splatAnti2: loc("fxSplatAnti2"),
		splatRelief2: loc("fxSplatRelief2"), splatStrataOn: loc("fxSplatStrataOn"), splatStrataOn2: loc("fxSplatStrataOn2"),
		splatCols: loc("fxSplatCols"), splatRows: loc("fxSplatRows"),
	}
}

// fxLocCache is FxLocs per program, for Apply's draws through the global
// program and anything else that names a program per draw. Dropped whenever
// any program is linked (linkGeneration), since a relinked program's
// locations may differ.
var (
	fxLocCache = map[uint32]*FxLocs{}
	fxLocGen   uint64
)

// fxLocsFor is prog's FxLocs, cached.
func fxLocsFor(prog uint32) *FxLocs {
	if fxLocGen != linkGeneration {
		fxLocCache = map[uint32]*FxLocs{}
		fxLocGen = linkGeneration
	}
	l := fxLocCache[prog]
	if l == nil {
		v := NewFxLocs(prog)
		l = &v
		fxLocCache[prog] = l
	}
	return l
}

// programFx is fxLocsFor(Program), held for Apply: every draw through the
// world program reads it, and a map lookup a draw is what SurfaceLocs exists
// to avoid elsewhere.
var programFx struct {
	prog uint32
	gen  uint64
	l    *FxLocs
}

func programFxLocs() *FxLocs {
	if programFx.l == nil || programFx.prog != Program || programFx.gen != linkGeneration {
		programFx.l = fxLocsFor(Program)
		programFx.prog, programFx.gen = Program, linkGeneration
	}
	return programFx.l
}

// resolveSurfaceFx is the named material's effects.
func resolveSurfaceFx(name string) (material.SurfaceFx, bool) {
	m, ok := material.ResolveSurface(name)
	return m.Fx, ok
}

// fxLive is, per program, whether its effect samplers currently point at the
// effects' own units (FxUnit*) rather than resting on unit 0. Dropped with
// fxLocCache on any link.
var (
	fxLive    = map[uint32]bool{}
	fxLiveGen uint64
)

func fxLiveFor(prog uint32) bool {
	if fxLiveGen != linkGeneration {
		fxLive = map[uint32]bool{}
		fxLiveGen = linkGeneration
	}
	return fxLive[prog]
}

// BindSurfaceFxSamplers points prog's effect samplers at their REST unit, 0
// — the albedo's: every one of them is a sampler2D like it, so sharing its
// unit is legal and adds nothing. Once a program and link. prog must be in
// use. Called with the shadow samplers, from every program that links
// fragment.glsl (render.UploadShadowUniformsTo).
//
// Why rest rather than sit on their own units: on Mesa's d3d12 driver
// (WSLg), a program whose samplers name units of their own pays for them on
// every draw whether or not the branch reading them runs — the phase-4
// gallery level, which wears no effect, went from 21.0 to 23-27 ms p50 with
// a second-long stall, and the splat's layers as 2D ARRAYS cost that even
// parked on another array unit. So the layers are 2×2 atlases (sampler2D,
// material_fx_textures.go), everything rests on unit 0, and an effect draw
// points its samplers at their own units (fxSamplersLive) and the next draw
// without effects points them back.
func BindSurfaceFxSamplers(prog uint32) { bindSurfaceFxSamplers(prog, false) }

// BindSurfaceFxSamplersNow is BindSurfaceFxSamplers without the once: for a
// program in another window's context (the material editor's preview),
// whose ID may be one the main context's cache already holds.
func BindSurfaceFxSamplersNow(prog uint32) { bindSurfaceFxSamplers(prog, true) }

func bindSurfaceFxSamplers(prog uint32, force bool) {
	l := fxLocsFor(prog)
	if l.mask < 0 || (l.bound && !force) {
		return // a program without the effects, or one already pointed
	}
	l.bound = true
	fxSamplersRest(l)
}

// fxSamplerLocs is l's eight sampler locations, in FxUnit order.
func fxSamplerLocs(l *FxLocs) [8]int32 {
	return [8]int32{l.splatWeights, l.splatAlbedo, l.splatNormal, l.strata, l.groundColor, l.groundHeight, l.detail,
		l.splatWeights2}
}

// fxUnits is the eight samplers' own units, in the same order.
var fxUnits = [8]int32{FxUnitSplatWeights, FxUnitSplatAlbedo, FxUnitSplatNormal, FxUnitStrata, FxUnitGroundColor,
	FxUnitGroundHeight, FxUnitDetail, FxUnitSplatWeights2}

// fxSamplersRest points l's samplers at unit 0.
func fxSamplersRest(l *FxLocs) {
	for _, loc := range fxSamplerLocs(l) {
		gl.Uniform1i(loc, 0)
	}
	fxLiveFor(l.prog)
	fxLive[l.prog] = false
}

// fxSamplersLive points l's samplers at the effects' own units.
func fxSamplersLive(l *FxLocs) {
	for i, loc := range fxSamplerLocs(l) {
		gl.Uniform1i(loc, fxUnits[i])
	}
	fxLive[l.prog] = true
}

// applySurfaceFx writes a state's effects through l (the program in use).
func applySurfaceFx(f *material.SurfaceFx, texture string, l *FxLocs) {
	if l.mask < 0 {
		return
	}
	live := fxLiveFor(l.prog)
	if !f.Any() {
		gl.Uniform1i(l.mask, 0)
		if live {
			fxSamplersRest(l)
		}
		return
	}
	if !live {
		fxSamplersLive(l)
	}
	mask := int32(0)
	if f.HasTriplanar {
		mask |= fxTriplanar
		gl.Uniform3f(l.triplanar, 1/max(f.TriplanarScale, 1e-3), max(f.TriplanarSharpness, 1), b2f(f.TriplanarUpright))
	}
	if f.HasSplat {
		if ok, eight := applySplat(f, l); ok {
			mask |= fxSplat
			if eight {
				mask |= fxSplat8
			}
		}
	}
	if f.HasGroundBlend && applyGround(f, l) {
		mask |= fxGround
	}
	if f.HasStreaks {
		mask |= fxStreaks
		gl.Uniform4f(l.streaks, f.StreakStrength, 1/max(f.StreakScale, 1e-3), 1/max(f.StreakLength, 1e-3), f.StreakOverhang)
		gl.Uniform3f(l.streakColor, f.StreakColor.X(), f.StreakColor.Y(), f.StreakColor.Z())
	}
	if f.HasDetailNormal {
		if tex, bc5, ok := fxDetailTexture(f.DetailMap); ok {
			mask |= fxDetail
			gl.ActiveTexture(gl.TEXTURE0 + FxUnitDetail)
			gl.BindTexture(gl.TEXTURE_2D, tex)
			gl.ActiveTexture(gl.TEXTURE0)
			gl.Uniform4f(l.detailParams, 1/max(f.DetailScale, 1e-3), f.DetailStrength, 1/max(f.DetailDistance, 1e-3), b2f(bc5))
		}
	}
	if f.HasMacro {
		mask |= fxMacro
		gl.Uniform4f(l.macro, 1/max(f.MacroScale, 1e-3), f.MacroAmount, f.MacroWarmth, f.MacroVertical)
	}
	if f.HasGrade {
		mask |= fxGrade
		g := GradeUniforms(f, texture)
		gl.Uniform4f(l.gradeMatch, g.Match[0], g.Match[1], g.Match[2], g.Match[3])
		gl.Uniform4f(l.gradeHS, g.HS[0], g.HS[1], g.HS[2], g.HS[3])
		gl.Uniform3f(l.gradeTint, g.Tint[0], g.Tint[1], g.Tint[2])
	}
	gl.Uniform1i(l.mask, mask&FxDebugMask)
}

// FxDebugMask is ANDed into every surface's matFx: FOUNDRY_FX_MASK (an
// integer of the FX_* bits, default all) switches effects off everywhere for
// an A/B of their cost (docs/canyon/PERF.md). Not a quality setting.
var FxDebugMask = func() int32 {
	if v, err := strconv.Atoi(os.Getenv("FOUNDRY_FX_MASK")); err == nil {
		return int32(v)
	}
	return -1
}()

// applySplat binds the splat's textures and writes its uniforms; ok false
// when its arrays or weights cannot be had, eight whether it drew in
// eight-layer mode (an eight-layer splat whose second weights map is
// missing draws its first four layers).
func applySplat(f *material.SurfaceFx, l *FxLocs) (ok, eight bool) {
	arr := SplatArraysForAtlas(splatAtlasNames(f))
	if arr == nil {
		return false, false
	}
	tiles := splatLayerTiles(f, arr)
	w, ok := fxDataTexture(f.SplatWeights, false)
	if !ok {
		return false, false
	}
	if f.SplatEight {
		if w2, ok2 := fxDataTexture(f.SplatWeights2, false); ok2 {
			eight = true
			gl.ActiveTexture(gl.TEXTURE0 + FxUnitSplatWeights2)
			gl.BindTexture(gl.TEXTURE_2D, w2)
		}
	}
	gl.ActiveTexture(gl.TEXTURE0 + FxUnitSplatWeights)
	gl.BindTexture(gl.TEXTURE_2D, w)
	gl.ActiveTexture(gl.TEXTURE0 + FxUnitSplatAlbedo)
	gl.BindTexture(gl.TEXTURE_2D, arr.Albedo)
	gl.ActiveTexture(gl.TEXTURE0 + FxUnitSplatNormal)
	gl.BindTexture(gl.TEXTURE_2D, arr.Normal)
	strata := float32(0)
	if f.SplatStrata != "" && f.SplatStrataAmt > 0 {
		if t, ok := fxDataTexture(f.SplatStrata, false); ok {
			gl.ActiveTexture(gl.TEXTURE0 + FxUnitStrata)
			gl.BindTexture(gl.TEXTURE_2D, t)
			strata = f.SplatStrataAmt
		}
	}
	gl.ActiveTexture(gl.TEXTURE0)
	rect := fxRect(f.SplatMin, f.SplatMax)
	gl.Uniform4f(l.splatRect, rect[0], rect[1], rect[2], rect[3])
	n := material.SplatLayers
	if eight {
		n = material.SplatLayersMax
	}
	var inv, tri, height, layer, anti, relief, strataOn [material.SplatLayersMax]float32
	var tint [3 * material.SplatLayersMax]float32
	for i := 0; i < n; i++ {
		sl := &f.SplatLayers[i]
		inv[i] = 1 / max(sl.Scale, 1e-3)
		tri[i] = b2f(sl.Triplanar)
		anti[i] = b2f(sl.AntiTile)
		strataOn[i] = b2f(!sl.NoStrata)
		if !sl.Triplanar {
			relief[i] = max(sl.Relief, 0)
		}
		height[i] = sl.Height
		layer[i] = float32(tiles[i])
		t := sl.Tint
		if t == (mgl32.Vec3{}) {
			t = mgl32.Vec3{1, 1, 1}
		}
		tint[3*i], tint[3*i+1], tint[3*i+2] = t.X(), t.Y(), t.Z()
	}
	gl.Uniform4fv(l.splatInvScale, 1, &inv[0])
	gl.Uniform4fv(l.splatTri, 1, &tri[0])
	gl.Uniform4fv(l.splatAnti, 1, &anti[0])
	gl.Uniform4fv(l.splatRelief, 1, &relief[0])
	gl.Uniform4fv(l.splatHeight, 1, &height[0])
	gl.Uniform4fv(l.splatLayer, 1, &layer[0])
	gl.Uniform4fv(l.splatStrataOn, 1, &strataOn[0])
	gl.Uniform3fv(l.splatTint, int32(n), &tint[0])
	if eight {
		gl.Uniform4fv(l.splatInvScale2, 1, &inv[4])
		gl.Uniform4fv(l.splatTri2, 1, &tri[4])
		gl.Uniform4fv(l.splatAnti2, 1, &anti[4])
		gl.Uniform4fv(l.splatRelief2, 1, &relief[4])
		gl.Uniform4fv(l.splatHeight2, 1, &height[4])
		gl.Uniform4fv(l.splatLayer2, 1, &layer[4])
		gl.Uniform4fv(l.splatStrataOn2, 1, &strataOn[4])
	}
	gl.Uniform3f(l.splatParams, max(f.SplatBlend, 0.005), f.SplatNormal, f.SplatAO)
	// The atlas: a tile's texels and its gutter as fractions of the atlas,
	// across (u: Cols tiles) and down (v: Rows, two but for a palette), and
	// the columns and rows.
	tile := float32(arr.Size) + 2*float32(arr.Gutter)
	cols := float32(max(arr.Cols, 2))
	rows := float32(max(arr.Rows, 2))
	gl.Uniform4f(l.splatAtlas, float32(arr.Size)/(cols*tile), float32(arr.Size)/(rows*tile),
		float32(arr.Gutter)/(cols*tile), float32(arr.Gutter)/(rows*tile))
	gl.Uniform1f(l.splatCols, cols)
	gl.Uniform1f(l.splatRows, rows)
	span := f.SplatStrataHi - f.SplatStrataLo
	if span == 0 {
		span = 1
	}
	gl.Uniform4f(l.strataParams, f.SplatStrataLo, 1/span, strata, f.SplatStrataWarp)
	// The bedding relief and the walls' normal strength; a material from
	// before they existed resolves to 0 and 0, which is no bedding and (the
	// multiplier's 0 read as unset) the side projections as the others.
	wallN := f.SplatWallNormal
	if wallN <= 0 {
		wallN = 1
	}
	gl.Uniform4f(l.splatBedding, max(f.SplatBedding, 0), 1/max(f.SplatBeddingSpacing, 0.05), wallN, 0)
	// The sand ripples: strength (0: off), 1/wavelength, the wind's bearing
	// in radians, the layer.
	gl.Uniform4f(l.splatRipple, max(f.SplatRipple, 0), 1/max(f.SplatRippleWave, 0.02),
		f.SplatRippleAngle*math.Pi/180, f.SplatRippleLayer)
	return true, eight
}

// applyGround binds the ground blend's two maps and writes its uniforms.
func applyGround(f *material.SurfaceFx, l *FxLocs) bool {
	c, ok := fxDataTexture(f.GroundColor, false)
	if !ok {
		return false
	}
	h, ok := fxDataTexture(f.GroundHeight, true)
	if !ok {
		return false
	}
	gl.ActiveTexture(gl.TEXTURE0 + FxUnitGroundColor)
	gl.BindTexture(gl.TEXTURE_2D, c)
	gl.ActiveTexture(gl.TEXTURE0 + FxUnitGroundHeight)
	gl.BindTexture(gl.TEXTURE_2D, h)
	gl.ActiveTexture(gl.TEXTURE0)
	rect := fxRect(f.GroundMin, f.GroundMax)
	gl.Uniform4f(l.groundRect, rect[0], rect[1], rect[2], rect[3])
	gl.Uniform2f(l.groundRange, f.GroundLo, f.GroundHi-f.GroundLo)
	gl.Uniform4f(l.groundParams, max(f.GroundBand, 0.01), f.GroundBreakup, f.GroundStrength, f.GroundContact)
	return true
}

// fxRect is a world rectangle as the shader takes it: its corner and the
// reciprocal of its size.
func fxRect(lo, hi mgl32.Vec2) [4]float32 {
	sx, sz := hi.X()-lo.X(), hi.Y()-lo.Y()
	if sx == 0 {
		sx = 1
	}
	if sz == 0 {
		sz = 1
	}
	return [4]float32{lo.X(), lo.Y(), 1 / sx, 1 / sz}
}

func splatLayerNames(f *material.SurfaceFx) [material.SplatLayersMax]string {
	var n [material.SplatLayersMax]string
	for i := range n {
		n[i] = f.SplatLayers[i].Texture
	}
	return n
}

// splatAtlasNames is the names a splat's atlas is built from: its shared
// atlas's (SurfaceFx.SplatAtlas, a region terrain's per-tile material
// borrowing its union's) when it names one, else its own layers.
func splatAtlasNames(f *material.SurfaceFx) [material.SplatAtlasMax]string {
	if f.SplatAtlas != ([material.SplatAtlasMax]string{}) {
		return f.SplatAtlas
	}
	var out [material.SplatAtlasMax]string
	own := splatLayerNames(f)
	copy(out[:], own[:])
	return out
}

// splatLayerTiles is each of the splat's layers' tile in arr (the atlas
// splatAtlasNames built), -1 none: its own index's for its own atlas, and
// for a shared one the tile of the first atlas layer of the same texture —
// the tile the union material's layer of that texture samples.
func splatLayerTiles(f *material.SurfaceFx, arr *SplatArrays) [material.SplatLayersMax]int {
	if f.SplatAtlas == ([material.SplatAtlasMax]string{}) {
		var own [material.SplatLayersMax]int
		copy(own[:], arr.Layer[:])
		return own
	}
	var out [material.SplatLayersMax]int
	for i := range out {
		out[i] = -1
		tex := f.SplatLayers[i].Texture
		if tex == "" {
			continue
		}
		for j, n := range f.SplatAtlas {
			if n == tex {
				out[i] = arr.Layer[j]
				break
			}
		}
	}
	return out
}

// GradeValues is the colour grade as the shader takes it.
type GradeValues struct {
	Match [4]float32 // the per-channel ratio, and the amount
	HS    [4]float32 // cos and sin of the hue turn, saturation, brightness
	Tint  [3]float32 // the tint with its brightness divided out
}

// GradeUniforms works out the grade's uniforms for a surface wearing
// texture: the match ratio carries the source colour (the texture's own mean
// when the material gave none) to the target with both colours' brightness
// divided out, so matching changes hue and saturation and leaves how light
// the rock is to the scan. Exported for the tests that check the shader
// against it.
func GradeUniforms(f *material.SurfaceFx, texture string) GradeValues {
	var g GradeValues
	src := f.GradeSource
	if src == (mgl32.Vec3{}) {
		if m, ok := TextureMeanColor(texture); ok {
			src = m
		}
	}
	ratio := mgl32.Vec3{1, 1, 1}
	if src != (mgl32.Vec3{}) && f.GradeTarget != (mgl32.Vec3{}) {
		s := chroma(src)
		t := chroma(f.GradeTarget)
		for i := 0; i < 3; i++ {
			ratio[i] = t[i] / max(s[i], 1e-3)
		}
	}
	g.Match = [4]float32{ratio[0], ratio[1], ratio[2], f.GradeMatch}
	h := float64(mgl32.DegToRad(f.GradeHue))
	g.HS = [4]float32{float32(math.Cos(h)), float32(math.Sin(h)), f.GradeSaturation, f.GradeBrightness}
	tint := f.GradeTint
	if tint == (mgl32.Vec3{}) {
		tint = mgl32.Vec3{1, 1, 1}
	}
	tn := chroma(tint)
	g.Tint = [3]float32{tn[0], tn[1], tn[2]}
	return g
}

// chroma is c with its luma divided out (Rec. 601 weights, the shader's).
func chroma(c mgl32.Vec3) mgl32.Vec3 {
	y := 0.299*c.X() + 0.587*c.Y() + 0.114*c.Z()
	if y <= 1e-4 {
		return mgl32.Vec3{1, 1, 1}
	}
	return c.Mul(1 / y)
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}
