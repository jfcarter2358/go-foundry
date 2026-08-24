package main

// biome_light.go — lighting and shadow plumbing shared by the biome passes
// (the scatter in grass.go and the ground patch in biome_ground.go).
//
// Both passes own their GL programs and never touch the main shader's state,
// which used to mean they also had their own hand-rolled two-term lighting: a
// global ambient plus directional light 0, no point lights, no shadows. That
// read as a painted layer floating free of the scene — a building's shadow
// swept across the terrain and stopped dead at the sector boundary, and a torch
// lit the floor but not the grass standing on it.
//
// The fix is deliberately not a second lighting model. resources/shaders/
// biome_light.glsl declares uniforms with the SAME NAMES fragment.glsl uses, so
// UploadLightsTo (light.go), UploadShadowUniformsTo and
// UploadPointShadowUniformsTo (shadow.go) drive the biome programs unchanged.
// Adding a light type to the scene shader reaches the biome passes for free.
//
// What is tuned per call site is the COST, via the three defines below — see the
// header of biome_light.glsl for what each one buys.

import (
	"fmt"
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"path/filepath"

	"github.com/go-gl/gl/v4.1-core/gl"
)

// BiomeShadowSettings gates how much of the shadow pipeline the biome passes
// pay for. Receiving is cheap enough to leave on; casting is the expensive
// half, because a caster has to be re-instanced into every shadow map it
// appears in, on top of the color pass it was already drawn in.
//
// Toggled live from the F3 debug overlay and the F5/F6/F7 keys (see debug.go
// and input.go) so each one's cost can be measured against the frame time
// sitting next to it rather than estimated.
type BiomeShadowSettings struct {
	// Receive: scatter and ground patches take the scene's cascade + cubemap
	// shadows. Foliage resolves this once per vertex, so the cost is bounded by
	// instance count rather than by how much of the screen the grass covers.
	Receive bool

	// CastRockMoss: rock and moss instances render into the shadow maps. These
	// are the scatter kinds worth casting — chunky silhouettes, and capped at
	// maxRockGridN/maxMossGridN (130/260 per side) rather than grass's 320.
	CastRockMoss bool

	// CastFoliage: grass, wildflowers and cattails render into the shadow maps
	// too. Off by default, and the reason the whole struct is runtime-toggleable:
	// at maxGrassGridN this re-instances up to 102,400 blades per sector per
	// cascade, and a blade thinner than a shadow texel tends to alias into
	// flickering speckle rather than a shadow. Worth trying at low density or
	// with a short CastRange; worth measuring before shipping on.
	CastFoliage bool

	// CastCascades caps how many of ShadowCascades the scatter is drawn into,
	// nearest first. Rock/moss silhouettes read as real shadow-casters well
	// past the first cascade (unlike a single grass blade), so the default is
	// 2 rather than 1 — see cascadeSplit in RenderBiomeScatterDirShadow for how
	// this and CastRange combine to bound each cascade's own instance count.
	CastCascades int

	// CastRange is an EXTRA user cap on the scatter ring's radius for the
	// shadow passes, in world units — on top of, not instead of, the natural
	// cap each shadow map already provides (a cascade's own split distance, a
	// point light's own far plane; see castRangeFor). 0 means no extra cap:
	// casting reaches exactly as far as the shadow map itself does, which is
	// usually already the right answer. Set it lower only to trade a still
	// more distant rock's shadow for frame time. Uploaded as the `castRange`
	// uniform, which rejects instances AFTER placement — deliberately not a
	// smaller `range`, which would move every instance and slide the shadows
	// off the blades that cast them.
	CastRange float32

	// CastLODTiers caps how many of a scatter kind's LOD tiers (Near=1,
	// Mid=2, Far=3 — see grass.go's three-tier scatter doc comment and
	// biomeLODTierCount) render into shadow maps at all, nearest first. This
	// is a SEPARATE gate from CastRange/CastCascades above: a tier that
	// doesn't make this cut is skipped structurally, not range-clipped, and
	// when it DOES make the cut it casts using its own (cheaper, for Mid/Far)
	// mesh, never the Near tier's full one. Default 1 (Near only) is what
	// delivers the big win LOD promises for shadows — every tier beyond that
	// trades some of that saving back for reach, same as the switches above.
	// Kinds with only one tier (Rock) ignore this entirely.
	CastLODTiers int
}

// BiomeShadows is the live settings block. Defaults are the configuration the
// costing in biome_light.glsl assumes: receive everywhere, cast from the chunky
// kinds only, into the nearest two cascades, with no extra user range cap
// beyond what each shadow map naturally reaches.
var BiomeShadows = BiomeShadowSettings{
	Receive:      true,
	CastRockMoss: true,
	CastFoliage:  false,
	CastCascades: 2,
	CastRange:    0,
	CastLODTiers: 1,
}

// BiomeWindTime is the wind clock, in seconds, that every biome pass animates
// from. A single global rather than a per-call parameter because the shadow
// passes run earlier in the frame than the color pass and MUST use the same
// value: a blade that sways in the color pass and stands still in the depth
// pass sheds its shadow every time the wind moves it. Set once per frame in
// main.go, before RenderShadowPass.
var BiomeWindTime float32

// AnyCast reports whether any scatter kind is currently configured to render
// into shadow maps — the cheap early-out for the shadow passes. CastRange==0
// is NOT "disabled" (see its doc comment) — only the kind switches and
// CastCascades gate casting outright.
func (b *BiomeShadowSettings) AnyCast() bool {
	return (b.CastRockMoss || b.CastFoliage) && b.CastCascades > 0
}

// CastsKind reports whether brush kind should be drawn into shadow maps under
// the current settings. Moss is grouped with Rock, matching the way it shares
// Rock's shader path everywhere else (see renderBiomeSector).
func (b *BiomeShadowSettings) CastsKind(kind int32) bool {
	switch kind {
	case BiomeRock, BiomeMoss:
		return b.CastRockMoss
	case BiomeGrass, BiomeWildflower, BiomeCattail:
		return b.CastFoliage
	}
	return false
}

// castRangeFor clamps a sector's own draw range for a shadow pass, in two
// steps: first to mapReach — the shadow map actually being rendered into can't
// usefully receive an instance beyond its own extent (a cascade's split
// distance, a point light's far plane; 0 means no such limit, e.g. an unset
// cascadeSplit) — then to the user's own CastRange, if set. Without the first
// step, an instance placed and lit past a cascade's own light box just gets
// silently clipped away by the GPU: correct, but wasted vertex work, and the
// entire reason CastCascades defaults to 2 instead of "however far CastRange
// says" was to have the map's own reach be the thing that actually bounds
// this, not a hand-tuned number that has to be kept in sync with it.
func (b *BiomeShadowSettings) castRangeFor(sectorRange, mapReach float32) float32 {
	rng := sectorRange
	if mapReach > 0 && mapReach < rng {
		rng = mapReach
	}
	if b.CastRange > 0 && b.CastRange < rng {
		rng = b.CastRange
	}
	return rng
}

// ---------- shader assembly ----------

// biomeShaderPath resolves a shader file inside the running executable's
// shaders directory.
func biomeShaderPath(name string) string {
	return filepath.Join(fileutil.ExecutablePath, "shaders", name)
}

// biomeLightSnippet returns biome_light.glsl prefixed with the defines that fix
// its cost for one shader stage.
//
// pcf is the filter radius: 0 = a single tap, 1 = 3x3 (foliage's own vertex
// stage — one EVALUATION POINT per blade, but averaged over its neighbouring
// texels rather than a hard single tap; see the color pass's own doc comment
// in GrassInit for why a hard tap there flickers — and rock/moss fragments),
// 2 = 5x5, matching fragment.glsl's own filter (the ground patch, whose
// shadow edges have to agree with the structural floor's).
//
// pointShadows gates the 20-tap cubemap lookup, which foliage skips: it still
// takes point-light diffuse, just not point-light shadows.
//
// cascadeBlend gates fragment.glsl's cascade cross-fade band, which costs a
// second cascade lookup in the tail of each split. Only the ground patch buys
// it, and it buys it for the same reason it buys the 5x5 filter: it is a
// floor-sized surface that intersects the structural floor, so anywhere the
// two are visible together a shadow edge has to land in the same place on
// both. A blade or a pebble is too small to show the seam.
func biomeLightSnippet(pcf int, pointShadows, cascadeBlend bool) string {
	src := fmt.Sprintf("#define BIOME_LIGHT_PCF %d\n", pcf)
	if pointShadows {
		src += "#define BIOME_LIGHT_POINT_SHADOWS 1\n"
	}
	if cascadeBlend {
		src += "#define BIOME_LIGHT_CASCADE_BLEND 1\n"
	}
	return src + mustReadShader(biomeShaderPath("biome_light.glsl"))
}

// biomeScatterSnippet returns the shared instance placement/shaping code, which
// all three scatter vertex stages (color, cascade depth, cubemap depth) compile
// from the same source so a blade casts the shadow of the blade you can see.
func biomeScatterSnippet() string {
	return mustReadShader(biomeShaderPath("biome_scatter_common.glsl"))
}

// mustBiomeProgram links a vertex + fragment program, injecting a (possibly
// different) shared snippet into each stage after its #version line. Panics on
// failure, like the detail programs it mirrors — a shader that won't compile is
// a build error, not a runtime condition worth limping through.
func mustBiomeProgram(vertPath, fragPath, vertSnippet, fragSnippet string) uint32 {
	vs, err := gfx.CompileShader(gfx.InjectAfterVersion(mustReadShader(vertPath), vertSnippet), gl.VERTEX_SHADER)
	if err != nil {
		panic(fmt.Sprintf("biome vert %s: %v", vertPath, err))
	}
	fs, err := gfx.CompileShader(gfx.InjectAfterVersion(mustReadShader(fragPath), fragSnippet), gl.FRAGMENT_SHADER)
	if err != nil {
		panic(fmt.Sprintf("biome frag %s: %v", fragPath, err))
	}
	prog, err := linkStages(vs, fs)
	if err != nil {
		panic(fmt.Sprintf("biome program (%s + %s): %v", vertPath, fragPath, err))
	}
	return prog
}

// ---------- per-frame uniform upload ----------

// uploadBiomeLighting sends the whole scene lighting + shadow state to a biome
// program and leaves it bound. frame carries the directional cascade setup from
// RenderShadowPass.
//
// The three Upload*To calls are the same ones RenderDetailColor makes for the
// detail programs; nothing here is biome-specific except the view matrix (for
// cascade selection, which fragment.glsl gets as a varying from vertex.glsl)
// and the Receive toggle.
func uploadBiomeLighting(prog uint32, level *Level, frame ShadowFrame) {
	UploadLightsTo(prog, level.Lights, false)
	UploadShadowUniformsTo(prog, level.Lights, frame)
	UploadPointShadowUniformsTo(prog, level.Lights)

	gl.UseProgram(prog)
	cam := gfx.Cam.Matrix
	gl.UniformMatrix4fv(progUniformLoc(prog, "camera\x00"), 1, false, &cam[0])
	gl.Uniform1i(progUniformLoc(prog, "receiveShadows\x00"), boolToInt32(BiomeShadows.Receive))
}
