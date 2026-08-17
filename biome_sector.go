package main

// biome_sector.go — biome-paint data model.
//
// A BiomeSector is a user-drawn polygon on its own layer, independent of the
// structural sector geometry, painted with exactly one BiomeBrush (kind,
// density, draw range, color, and kind-specific appearance). Because a
// sector owns its brush outright (not a shared/frozen snapshot referenced by
// cells), editing a sector's brush only ever affects that one sector — there
// is no dedup/freeze bookkeeping needed. Sectors may overlap in world space;
// each renders independently, which is how multiple biome looks (e.g. tall +
// short grass) get layered together.

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"

	"github.com/go-gl/mathgl/mgl32"
)

// Biome kinds a brush can paint. Grass/Rock/Wildflower/Cattail are GPU-
// instanced scatter (grass.go); Sand is a displaced ground patch with no
// scatter on top; Water auto-manages a companion water-plane primitive
// (biome_water.go). Every land kind (everything but Water) also gets a
// conforming, textured, lightly-displaced ground patch underneath it so the
// painted area reads as real terrain rather than a flat cutout — see
// biome_ground.go. Moss is a ground patch always, plus an optional dense
// low-clump scatter on top (BiomeBrush.MossScatter) — see its own doc
// comment. Appended after Water (rather than sorted in) so existing saves'
// persisted Kind ints stay valid.
const (
	BiomeGrass int32 = iota
	BiomeRock
	BiomeWildflower
	BiomeCattail
	BiomeSand
	BiomeWater
	BiomeMoss
)

// Default camera-ring draw radii for the GPU-scattered biomes (world units).
const (
	defaultGrassRange      = float32(45)
	defaultRockRange       = float32(90)
	defaultWildflowerRange = float32(40)
	defaultCattailRange    = float32(40)
	defaultMossRange       = float32(35) // small clumps: no need to draw as far as boulders
)

// Default appearance of the GPU-scattered biomes.
const (
	defaultGrassHeight  = float32(0.55)
	defaultGrassWidth   = float32(0.045)
	defaultRockSides    = int32(4) // octahedron (8 faces)
	defaultRockScale    = float32(0.35)
	defaultBiomeDensity = float32(0.8)

	defaultWildflowerHeight = float32(0.4)
	defaultWildflowerWidth  = float32(0.035)
	defaultCattailHeight    = float32(1.1)
	defaultCattailWidth     = float32(0.03)

	// World size of the wildflower bloom / cattail seed head, independent of
	// stem height/width (BiomeBrush.HeadScale; see grass.go's buildBlobMesh
	// callers and biome_vert.glsl's headScale uniform).
	defaultWildflowerHeadScale = float32(0.18)
	defaultCattailHeadScale    = float32(0.16)

	// Multiplier on the wildflower's basal leaves (BiomeBrush.LeafScale),
	// whose geometry is already authored as fractions of the stem's height —
	// so 1 is "as designed", not "one world unit".
	defaultWildflowerLeafScale = float32(1)
)

// defaultCenterColor is the starting color for a wildflower's optional bloom
// center (BiomeBrush.BloomCenter) — a warm pollen yellow, which reads as a
// center against most of the flowerPalette hues.
var defaultCenterColor = mgl32.Vec3{0.95, 0.82, 0.25}

// Default ground-patch and sand-dune/water-depth appearance.
const (
	defaultSandHeight = float32(0.4) // dune displacement amplitude, world units
	defaultWaterDepth = float32(0.6) // how far below the surrounding floor the water surface sits
)

// Default ground-patch terrain shaping (biome_ground.go's groundDisplacement)
// — GroundAmplitude/GroundFrequency/GroundOctaves are the user-configurable
// generalization of what used to be fixed constants. Sand's amplitude stays
// governed by the dedicated SandHeight field above (much larger range, and
// already shipped/persisted) rather than GroundAmplitude, but Sand's noise
// frequency/octaves are configurable through the shared fields too.
const (
	defaultGroundAmplitude = float32(0.07) // Grass/Rock/Wildflower/Cattail
	defaultGroundFrequency = float32(0.25)
	defaultGroundOctaves   = int32(2)

	defaultWaterGroundAmplitude = float32(0.18) // Water's lakebed
	defaultWaterGroundFrequency = float32(0.09)
	defaultWaterGroundOctaves   = int32(3)

	defaultSandFrequency = float32(0.06) // Sand's dune noise (amplitude is SandHeight)
	defaultSandOctaves   = int32(3)
)

// defaultGroundUVScale is the ground patch's texture tiling when a brush
// doesn't override it (BiomeBrush.GroundUVScale) — texture repeats per world
// unit, the same units a structural floor's Surface.UVScale is in, so a patch
// set to match a floor's UV scale tiles identically to it. 1 means one texture
// repeat per world unit, matching an unscaled floor.
const defaultGroundUVScale = float32(1)

// defaultRangeFadeFraction is the fraction of Range the camera-ring boundary
// fades over on a newly-painted sector — matches the span the fade always used
// before it became configurable (dist in [0.6*range, range]).
const defaultRangeFadeFraction = float32(0.4)

// defaultLOD1Fraction/defaultLOD2Fraction locate the Near->Mid and Mid->Far
// LOD boundaries (grass.go's three-tier scatter) as fractions of a sector's
// own Range when BiomeBrush.LOD1Distance/LOD2Distance are left at 0 — see
// resolvedLOD1/resolvedLOD2 below.
const (
	defaultLOD1Fraction = float32(0.35)
	defaultLOD2Fraction = float32(0.70)
)

// biomeLODFadeFraction is the fraction of a tier's own span its boundary
// dissolves over (the LOD analogue of RangeFadeFraction) — a fixed
// implementation constant rather than a per-brush field, since nobody needs
// to hand-tune where mesh detail swaps the way they tune the sector's own
// painted/camera-ring edges.
const biomeLODFadeFraction = float32(0.15)

// defaultFakeShadowStrength/defaultFakeShadowSpread are the fake ground-contact
// shadow's starting look (see BiomeBrush's own doc comment) on the kinds it
// applies to by default. Strength is deliberately subtle — this is meant to
// read as "something is sitting here", not as a real shadow's full darkening —
// and Spread is short: it only needs to bridge the gap between a sector's own
// ground patch and the surrounding structural floor, not throw a long shadow.
const (
	defaultFakeShadowStrength = float32(0.3)
	defaultFakeShadowSpread   = float32(1.2)
)

// defaultBoundaryJitter is how far (world units) a sector's circular
// range/LOD boundaries bulge by when BoundaryJitter is left unset. Modest
// relative to even the shortest default range (Wildflower/Cattail's 40) —
// enough to visibly break up the circle without the boundary wandering far
// enough to look unintentional.
const defaultBoundaryJitter = float32(4)

// maxStemHeight caps Grass/Wildflower's GrassHeight — enforced both as the
// panel slider's max and defensively at render time (renderBiomeSector), so
// a value from an edited/legacy save can't produce an absurdly tall blade
// either. Cattail is intentionally not capped here (reeds read fine taller).
const maxStemHeight = float32(5)

// clampGrassClump resolves a brush's GrassClump to the 0..1 the shader's tuft
// math is parameterized over. Unlike most brush fields, 0 is a MEANINGFUL
// value here (no clumping — the original even scatter) rather than the usual
// "0 means the kind's default", so an out-of-range value is clamped rather
// than replaced with a default. Written as `!(v > 0)` so a NaN out of a
// hand-edited save falls to 0 instead of propagating into the uniform.
func clampGrassClump(v float32) float32 {
	if !(v > 0) {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// resolvedLeafScale applies the usual "0 means the kind's default" convention
// to BiomeBrush.LeafScale, and caps it so a hand-edited save can't grow leaves
// large enough to swallow the flower they belong to.
func resolvedLeafScale(v float32) float32 {
	if !(v > 0) { // also catches NaN out of a hand-edited save
		return defaultWildflowerLeafScale
	}
	if v > maxWildflowerLeafScale {
		return maxWildflowerLeafScale
	}
	return v
}

// maxWildflowerLeafScale is the panel slider's top end and the clamp
// resolvedLeafScale enforces.
const maxWildflowerLeafScale = float32(4)

// biomeLODTierCount reports how many LOD tiers (grass.go's Near/Mid/Far
// scatter — see its doc comment) kind renders with. Only the kinds that
// scatter as a dense "field" of small foliage get all three; Rock (sparse,
// individually-read instances, no group-clump mesh worth building) and every
// non-scattering kind render as a single tier, unchanged from before LOD
// existed.
func biomeLODTierCount(kind int32) int {
	switch kind {
	case BiomeGrass, BiomeWildflower, BiomeCattail, BiomeMoss:
		return 3
	default:
		return 1
	}
}

// resolvedLOD1/resolvedLOD2 resolve a brush's LOD1Distance/LOD2Distance to
// the world-unit Near->Mid and Mid->Far boundaries actually used at render
// time: the usual "0 means the kind's default" convention
// (defaultLOD1Fraction/defaultLOD2Fraction of the sector's own Range), then
// clamped so 0 < LOD1 <= LOD2 <= Range holds even for a hand-edited save —
// grass.go's per-tier draw calls assume that ordering unconditionally.
func resolvedLOD1(b *BiomeBrush) float32 {
	rng := biomeSectorRange(b)
	v := b.LOD1Distance
	if !(v > 0) { // also catches NaN out of a hand-edited save
		v = rng * defaultLOD1Fraction
	}
	return clampLODDistance(v, rng)
}

func resolvedLOD2(b *BiomeBrush) float32 {
	rng := biomeSectorRange(b)
	lod1 := resolvedLOD1(b)
	v := b.LOD2Distance
	if !(v > 0) {
		v = rng * defaultLOD2Fraction
	}
	v = clampLODDistance(v, rng)
	if v < lod1 {
		v = lod1
	}
	return v
}

// clampLODDistance keeps a resolved LOD boundary inside (0, rng] — also
// falling back to a tiny sliver of rng for a NaN/non-positive value out of a
// hand-edited save, the same defensiveness clampGrassClump/resolvedLeafScale
// apply to their own fields.
func clampLODDistance(v, rng float32) float32 {
	if !(v > 0) {
		v = rng * 0.01
	}
	if v > rng {
		v = rng
	}
	return v
}

// biomeMaxPolyVerts caps how many points a single BiomeSector polygon can
// have — generous for a hand-drawn shape, and mirrored as MAX_POLY in
// biome_vert.glsl (the uniform array the polygon is uploaded into).
const biomeMaxPolyVerts = 64

// BiomeBrush is the paint configuration assigned to a BiomeSector. Unlike the
// old frozen-layer model, this lives directly on the sector it paints, so
// editing it only ever affects that sector.
type BiomeBrush struct {
	Kind    int32      // BiomeGrass / BiomeRock / BiomeWildflower / BiomeCattail / BiomeSand / BiomeWater
	Density float32    // 0..1 survival probability, uniform across the polygon (scatter kinds only)
	Range   float32    // camera-ring draw distance (world units, scatter kinds only)
	Color   mgl32.Vec3 // paint color: tints the rendered blade/rock/flower/reed directly

	// LOD1Distance/LOD2Distance (world units from the camera, kinds with
	// biomeLODTierCount==3 only — see grass.go's three-tier scatter doc
	// comment) are the Near->Mid and Mid->Far mesh-detail boundaries. 0 uses
	// the kind's default (resolvedLOD1/resolvedLOD2: defaultLOD1Fraction/
	// defaultLOD2Fraction of Range). The Far tier always runs from LOD2Distance
	// out to Range.
	LOD1Distance float32
	LOD2Distance float32

	// Grass/Wildflower/Cattail stem sizing (GrassHeight/GrassWidth are reused
	// across all three blade-shaped kinds rather than duplicating a field per
	// kind — they all scatter from the same tapered-blade template).
	GrassHeight float32
	GrassWidth  float32

	// HeadScale is the world size of a Wildflower's bloom or a Cattail's seed
	// head — independent of GrassHeight/GrassWidth (the stem). Unused by
	// Grass/Rock. 0 uses the kind's default (defaultWildflowerHeadScale /
	// defaultCattailHeadScale).
	HeadScale float32

	// GrassClump (Grass only, 0..1) gathers the otherwise evenly-scattered
	// blades into tufts that read as individual grass plants: blades pull in
	// toward a shared root point, fan out radially, arc outward along their
	// length, and grade so the middle of a tuft stands tall and upright while
	// the rim splays short and low. 0 — the zero value, so every level
	// authored before this existed loads unchanged — is exactly the old
	// one-blade-per-lattice-slot scatter. See biome_vert.glsl's clump uniform
	// for the geometry; nothing about it is baked into the mesh or the
	// height/coverage texture, so changing it needs no rebuild.
	//
	// Note a clumped tuft's middle blade grows past GrassHeight (by the
	// shader's CLUMP_TALL), the same way the existing per-instance size
	// variation already overshoots it — GrassHeight is the authored average,
	// not a hard ceiling on any one blade.
	GrassClump float32

	// Wildflower bloom options. Every zero value is the pre-existing look, so
	// blooms authored before these existed load unchanged and no save
	// migration is needed:
	//   BloomSpike      false = squat petal disc, true = elongated spike
	//                   (the cattail-like "corn dog" silhouette).
	//   BloomCenter     false = petals only, true = a small separate center
	//                   blob on top of them, colored by CenterColor.
	//   BloomSolidColor false = per-instance random hue from the shader's
	//                   flowerPalette (tinted by Color), true = Color used
	//                   directly, so a sector is one deliberate flower color.
	BloomSpike      bool
	BloomCenter     bool
	BloomSolidColor bool
	CenterColor     mgl32.Vec3

	// StemLeaves (Wildflower only) rings the base of the stem with triangular
	// leaves that arc outward and nod over — see buildWildflowerLeaves. Off
	// (the zero value, so pre-existing flowers load unchanged) is a bare stem.
	//
	// LeafScale sizes them. Unlike HeadScale, which is an absolute world size,
	// this is a MULTIPLIER on leaves already authored as fractions of the
	// stem's own height — so the default keeps leaves in proportion on a short
	// flower and a tall one alike, and the knob is for making them lusher or
	// sparser rather than for re-deriving their size from scratch. 0 uses
	// defaultWildflowerLeafScale.
	StemLeaves bool
	LeafScale  float32

	// Rock appearance.
	RockSides int32   // rock equatorial vertex count (faces == 2*RockSides)
	RockScale float32 // rock world scale (unit blob size)

	// Sand: ground patch only, no scatter. SandHeight is the dune
	// displacement amplitude (world units); 0 uses defaultSandHeight.
	SandHeight float32

	// Water: auto-manages a companion water-plane primitive filling the
	// painted polygon (see biome_water.go). WaterDepth is how far below the
	// surrounding floor the water surface sits; 0 uses defaultWaterDepth.
	// Wave/reflection look reuses the existing water-primitive params.
	WaterDepth float32
	Water      WaterParams

	// Moss: always a ground patch (moss texture); MossScatter additionally
	// turns on a dense low-clump scatter on top, reusing Density/Range/Color
	// (draw radius, coverage, tint) and RockScale (clump size) rather than
	// duplicating a field per kind — Moss and Rock never share a brush, so
	// there's nothing to collide with.
	MossScatter bool

	// MinSlopeAngle/MaxSlopeAngle (degrees from world-up) restrict where
	// Grass/Sand/Rock/Moss appear when painted over a floor with active
	// procedural displacement detail (detail.go) — e.g. Grass only on the
	// flatter mesa tops of a rock outcrop, Rock only on its steeper faces.
	// Ignored on an ordinary flat floor (nothing to gate — its slope is
	// always ~0) and by Wildflower/Cattail/Water, which never follow
	// displacement (biomeFollowsDisplacement, biome_displace.go). 0 uses the
	// kind's default (resolvedMinSlope/resolvedMaxSlope).
	MinSlopeAngle float32
	MaxSlopeAngle float32

	// Ground patch (biome_ground.go) — the conforming, textured, displaced
	// mesh every land kind (and Water's lakebed) renders underneath it.
	// GroundTexture overrides the kind's default texture (groundTextureFor)
	// when non-empty. GroundAmplitude/GroundFrequency/GroundOctaves shape its
	// procedural noise displacement; 0 uses the kind's default. Sand's
	// amplitude is SandHeight instead (see its own doc comment), but Sand's
	// dune frequency/octaves still come from these shared fields.
	GroundTexture   string
	GroundAmplitude float32
	GroundFrequency float32
	GroundOctaves   int32

	// GroundUVScale tiles the ground patch's texture: UV = world XZ * this,
	// exactly how a structural floor's Surface.UVScale is applied to its own
	// world-XZ UVs (sector.go / applyUVTransform), so dialing a patch to the
	// same numbers as the floor it's painted over makes the two textures tile
	// at the same size. Either component 0 uses defaultGroundUVScale (1).
	GroundUVScale mgl32.Vec2

	// Soft edges (scatter kinds only — see biome_scatter_common.glsl's
	// rangeFadeFraction/edgeFadeDistance uniforms, which these feed directly).
	// Both shrink instances toward their root rather than fading their alpha
	// (the scatter pass draws opaque), so the transition reads as vegetation
	// thinning out rather than a hard line even up close.
	//
	// RangeFadeFraction is the fraction of Range (measured in from the outer
	// edge) the camera-ring boundary fades over. 0 is the ORIGINAL behavior —
	// a hard cutoff exactly at Range — kept as the zero value so every sector
	// painted before this existed loads unchanged; defaultBiomeBrush sets it
	// to defaultRangeFadeFraction for every newly-painted one.
	RangeFadeFraction float32

	// EdgeFadeDistance (world units) is how far in from the sector's own
	// painted polygon edge an instance takes to reach full size — the boundary
	// the user actually drew, distinct from the camera-ring fade above. 0
	// (the zero value, so existing sectors are unaffected) disables it: unlike
	// RangeFadeFraction, this isn't given a nonzero default automatically,
	// since it costs a real per-instance distance-to-polygon query and is
	// only worth paying for on sectors where the drawn edge is actually going
	// to be seen up close.
	EdgeFadeDistance float32

	// FakeShadowStrength/FakeShadowSpread fake the ground-contact shadow of
	// Grass/Wildflower/Cattail scatter (biome_shadow_decal.go), which real
	// per-instance shadow casting is deliberately kept off for by default
	// (BiomeShadows.CastFoliage) — a blade thinner than a shadow texel aliases
	// more than it convinces, and this reads as reasonable at a fraction of
	// the cost: a darkening multiplier baked into the ground patch's own
	// shading (biome_ground.go) plus a small halo mesh that extends the
	// darkening out onto the STRUCTURAL floor surrounding the sector — the gap
	// real per-instance casting's short range (BiomeShadows.CastRange) leaves.
	//
	// FakeShadowStrength 0..1, 0 = off (the zero value: existing sectors are
	// unaffected; defaultBiomeBrush sets defaultFakeShadowStrength on the
	// kinds it applies to). FakeShadowSpread is the halo's reach beyond the
	// polygon edge in world units; 0 uses defaultFakeShadowSpread. Ignored for
	// Rock (its real per-fragment bump/AO already reads as grounded) and for
	// kinds that don't scatter at all.
	FakeShadowStrength float32
	FakeShadowSpread   float32

	// BoundaryJitter (world units) bulges the scatter's circular boundaries —
	// the outer camera-ring edge (Range) AND, for the kinds with LOD tiers
	// (biomeLODTierCount==3: Grass/Wildflower/Cattail/Moss), the Near->Mid and
	// Mid->Far tier boundaries too — out and in by a smooth, low-frequency
	// world-space noise, so the transition reads as an organic patch of
	// thinning vegetation instead of a perfect circle centered on the player.
	// One field covers every ring a sector has: it perturbs the DISTANCE ITSELF
	// (see biome_scatter_common.glsl's distJ) rather than each boundary
	// separately, so all of a sector's rings bulge and recede together at the
	// same world positions instead of wobbling independently and incoherently.
	//
	// 0 uses defaultBoundaryJitter (the usual "0 means the kind's default"
	// convention — see GroundAmplitude etc.) rather than meaning "off": unlike
	// EdgeFadeDistance, this costs almost nothing (the same per-instance hash
	// math the clump jitter already pays unconditionally) and a perfectly
	// circular pop-in line is not a look worth defaulting to.
	BoundaryJitter float32
}

// fakesGroundShadow reports whether kind is one of the ones
// FakeShadowStrength/FakeShadowSpread apply to — thin foliage without a
// strong real silhouette, the same kind-set BiomeShadows.CastFoliage's real
// casting covers. Rock/Moss are excluded: real casting is on for them by
// default (BiomeShadows.CastRockMoss) and their per-fragment bump/AO already
// reads as grounded, so a second darkening term would just double up.
func fakesGroundShadow(kind int32) bool {
	switch kind {
	case BiomeGrass, BiomeWildflower, BiomeCattail:
		return true
	}
	return false
}

// BiomeSector is a user-drawn polygon painted with one brush. Multiple
// sectors may overlap; each is rendered as its own instanced pass.
type BiomeSector struct {
	ID     string
	Points []mgl32.Vec2 // world-space polygon (CCW), independent of structural geometry
	Brush  BiomeBrush

	// HeightDirty is runtime-only (never serialized): true whenever Points
	// has changed since the last render, so the renderer knows to rebuild
	// this sector's cached height texture from current floor geometry. Also
	// force-set true to manually resync after unrelated structural edits.
	HeightDirty bool

	// GroundDirty is runtime-only, mirroring HeightDirty for the two other
	// sector-derived meshes: the ground patch (biome_ground.go, every land
	// kind) and the companion water-plane primitive (biome_water.go, Water
	// kind only). Kept separate from HeightDirty so a param change that only
	// affects one of those (e.g. WaterDepth) doesn't force a height-texture
	// rebuild, and vice versa — but every call site that sets one currently
	// sets both, since points/kind changes affect all three.
	GroundDirty bool
}

// isScatterKind reports whether kind is *structurally* capable of spawning
// GPU-instanced blade/rock/clump instances (grass.go) — i.e. whether
// Density/Range are meaningful for it at all. Sand and Water never scatter —
// Sand is a ground patch only, Water is a companion primitive. Moss is
// structurally capable but only actually scatters when its own
// BiomeBrush.MossScatter toggle is on — see (*BiomeBrush).Scatters, which
// callers issuing the actual draw call should use instead of this.
func isScatterKind(kind int32) bool {
	switch kind {
	case BiomeGrass, BiomeRock, BiomeWildflower, BiomeCattail, BiomeMoss:
		return true
	default:
		return false
	}
}

// Scatters reports whether b should actually issue a GPU-instanced scatter
// draw call: isScatterKind, further gated by MossScatter for Moss (every
// other scatter kind always scatters — Moss is the only one with an
// off-by-default ground-patch-only mode).
func (b *BiomeBrush) Scatters() bool {
	if !isScatterKind(b.Kind) {
		return false
	}
	if b.Kind == BiomeMoss {
		return b.MossScatter
	}
	return true
}

// defaultBiomeBrush returns a fresh, fully-populated brush for kind — used
// both when a new sector is drawn and when an existing sector's kind is
// switched in the panel, so every kind starts from a sensible look rather
// than carrying over another kind's field values (a Rock's RockScale left
// over on a sector switched to Grass, etc. — harmless since unused fields are
// ignored, but confusing to edit).
func defaultBiomeBrush(kind int32) BiomeBrush {
	b := BiomeBrush{
		Kind:            kind,
		Density:         defaultBiomeDensity,
		GrassHeight:     defaultGrassHeight,
		GrassWidth:      defaultGrassWidth,
		RockSides:       defaultRockSides,
		RockScale:       defaultRockScale,
		SandHeight:      defaultSandHeight,
		WaterDepth:      defaultWaterDepth,
		Water:           DefaultWaterParams(),
		GroundAmplitude: defaultGroundAmplitude,
		GroundFrequency: defaultGroundFrequency,
		GroundOctaves:   defaultGroundOctaves,
		GroundUVScale:   mgl32.Vec2{defaultGroundUVScale, defaultGroundUVScale},
		BoundaryJitter:  defaultBoundaryJitter,
		// Harmless (ignored) on kinds that don't scatter at all; scoped to the
		// kinds that actually read it below where a kind-specific default
		// would differ, same pattern as GroundAmplitude etc. above.
		RangeFadeFraction: defaultRangeFadeFraction,
	}
	if fakesGroundShadow(kind) {
		b.FakeShadowStrength = defaultFakeShadowStrength
		b.FakeShadowSpread = defaultFakeShadowSpread
	}
	switch kind {
	case BiomeRock:
		b.Range = defaultRockRange
		b.Color = mgl32.Vec3{0.55, 0.55, 0.58}
	case BiomeWildflower:
		b.Range = defaultWildflowerRange
		b.GrassHeight = defaultWildflowerHeight
		b.GrassWidth = defaultWildflowerWidth
		b.HeadScale = defaultWildflowerHeadScale
		b.LeafScale = defaultWildflowerLeafScale
		b.Color = mgl32.Vec3{1, 1, 1} // neutral: lets the per-instance hue palette show through
		b.CenterColor = defaultCenterColor
	case BiomeCattail:
		b.Range = defaultCattailRange
		b.GrassHeight = defaultCattailHeight
		b.GrassWidth = defaultCattailWidth
		b.HeadScale = defaultCattailHeadScale
		b.Color = mgl32.Vec3{1, 1, 1} // neutral: fixed reed/brown base colors show through
	case BiomeSand:
		b.Color = mgl32.Vec3{0.82, 0.72, 0.48}
		b.GroundFrequency = defaultSandFrequency
		b.GroundOctaves = defaultSandOctaves
	case BiomeWater:
		b.Color = mgl32.Vec3{0.3, 0.5, 0.55}
		b.GroundAmplitude = defaultWaterGroundAmplitude
		b.GroundFrequency = defaultWaterGroundFrequency
		b.GroundOctaves = defaultWaterGroundOctaves
	case BiomeMoss:
		b.Range = defaultMossRange
		b.Color = mgl32.Vec3{0.28, 0.42, 0.22}
	default: // BiomeGrass
		b.Range = defaultGrassRange
		b.Color = mgl32.Vec3{0.4, 0.7, 0.3}
	}
	return b
}

// groundTextureFor returns the texture painted on the conforming ground
// patch underneath a sector of the given kind — "a relevant texture for
// underneath them", per kind. Water's patch is its lakebed, seen through the
// translucent surface above it.
func groundTextureFor(kind int32) string {
	switch kind {
	case BiomeRock:
		return "rock"
	case BiomeSand:
		return "sand"
	case BiomeWater:
		return "sand" // lakebed
	case BiomeMoss:
		return "moss"
	default: // Grass, Wildflower, Cattail
		return "dirt"
	}
}

// Bounds returns the world-XZ AABB of the sector's polygon.
func (s *BiomeSector) Bounds() (min, max mgl32.Vec2) {
	if len(s.Points) == 0 {
		return
	}
	min, max = s.Points[0], s.Points[0]
	for _, p := range s.Points[1:] {
		if p.X() < min.X() {
			min[0] = p.X()
		}
		if p.Y() < min.Y() {
			min[1] = p.Y()
		}
		if p.X() > max.X() {
			max[0] = p.X()
		}
		if p.Y() > max.Y() {
			max[1] = p.Y()
		}
	}
	return
}

// biomeSeed derives a deterministic per-sector jitter/survival-pick seed from
// the sector's ID and its brush settings. Two sectors with different brush
// configs (or just different IDs) decorrelate their scatter lattices, so
// e.g. tall grass and short grass painted over the same area don't land on
// identical spots.
//
// GrassClump, StemLeaves, LeafScale and GroundUVScale are deliberately NOT
// hashed in. Folding any of them in would reshuffle the scatter of every
// already-authored sector the moment the field existed — and for the slider-
// driven ones it would additionally teleport the whole lattice while you drag,
// hiding the very effect you're tuning. None of them changes WHERE anything
// scatters, only
// what the scattered thing (or, for GroundUVScale, the patch underneath it)
// looks like, and sector IDs already decorrelate overlapping sectors on their
// own.
func biomeSeed(s *BiomeSector) mgl32.Vec2 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s.ID))
	// GroundTexture is the one variable-length (string) field on BiomeBrush —
	// binary.Write only supports fixed-size data, and silently writes
	// nothing at all for a struct containing a string, which used to make
	// every field below it (and the whole rest of the brush) invisible to
	// the hash. Hash it separately, then the rest of the struct via a
	// same-layout-minus-the-string local type.
	_, _ = h.Write([]byte(s.Brush.GroundTexture))
	fixed := struct {
		Kind                               int32
		Density, Range                     float32
		Color                              mgl32.Vec3
		GrassHeight, GrassWidth, HeadScale float32
		RockSides                          int32
		RockScale                          float32
		SandHeight                         float32
		WaterDepth                         float32
		Water                              WaterParams
		MossScatter                        bool
		BloomSpike                         bool
		BloomCenter                        bool
		BloomSolidColor                    bool
		CenterColor                        mgl32.Vec3
		MinSlopeAngle, MaxSlopeAngle       float32
		GroundAmplitude, GroundFrequency   float32
		GroundOctaves                      int32
	}{
		s.Brush.Kind, s.Brush.Density, s.Brush.Range, s.Brush.Color,
		s.Brush.GrassHeight, s.Brush.GrassWidth, s.Brush.HeadScale,
		s.Brush.RockSides, s.Brush.RockScale, s.Brush.SandHeight, s.Brush.WaterDepth, s.Brush.Water,
		s.Brush.MossScatter,
		s.Brush.BloomSpike, s.Brush.BloomCenter, s.Brush.BloomSolidColor, s.Brush.CenterColor,
		s.Brush.MinSlopeAngle, s.Brush.MaxSlopeAngle,
		s.Brush.GroundAmplitude, s.Brush.GroundFrequency, s.Brush.GroundOctaves,
	}
	_ = binary.Write(h, binary.LittleEndian, fixed)
	v := h.Sum64()
	a := float32(v%1_000_003) / 1_000_003.0
	b := float32((v/1_000_003)%1_000_003) / 1_000_003.0
	return mgl32.Vec2{a * 1000, b * 1000}
}

// nextBiomeSectorID returns a default ID unique within ch's existing biome
// sectors (IDs only need to be unique per chunk, not level-wide).
func nextBiomeSectorID(ch *Chunk) string {
	for n := len(ch.BiomeSectors); ; n++ {
		id := fmt.Sprintf("biome_%d", n)
		if !biomeSectorIDTaken(ch, id) {
			return id
		}
	}
}

func biomeSectorIDTaken(ch *Chunk, id string) bool {
	for i := range ch.BiomeSectors {
		if ch.BiomeSectors[i].ID == id {
			return true
		}
	}
	return false
}
