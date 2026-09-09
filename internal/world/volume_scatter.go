package world

// volume_scatter.go — foliage filling a placed region.
//
// Scatter used to be PAINTED: a polygon drawn on a floor in the top-down map,
// carrying a brush that said what grew there. That model went with the sector.
// A brush face has no floorplan to draw on, and the thing a polygon was really
// expressing — "this patch of ground is grassy" — is better said by placing a
// region and letting whatever geometry is underneath it be the ground.
//
// So scatter is a field on Volume, mirroring Fog: everything a region of
// vegetation needs — a shape, a rotation, half-extents, an ID, an `enabled`
// flag a script can flip, a wireframe cage to author it against, the gizmos to
// move and size it — a volume already has. All scatter adds is what grows and
// how thickly.
//
// # What did NOT come back
//
// The old system did three things. Only one of them is here.
//
//   - **Scatter** — instanced foliage, drawn from a camera-centred lattice.
//     That is this file, and it is the part a volume expresses well.
//   - **Ground patches** — a conforming, displaced, textured mesh under the
//     paint, so a grassy area read as terrain rather than as a flat cutout.
//     Nothing drapes one now. A brush IS the ground, and shape in it comes
//     from subdividing the brush's own faces, so an author who wants rolling
//     terrain builds it rather than switching it on.
//   - **Water** — a companion plane auto-managed by a Water-kind polygon.
//     Nothing replaces it yet; it needs a water *volume*, which is a separate
//     piece of work from this one.
//
// That is why the kinds below are the five that actually scatter something,
// and Sand (a bare ground patch) and Water are not among them.
//
// # Landing on the ground
//
// An instance is generated in the vertex shader from gl_InstanceID — there is
// no CPU-side list of blades — so it cannot raycast to find the floor under
// it. What it samples instead is a small height/coverage texture baked per
// volume by casting rays down through the level's brushes (render/
// scatter_height.go). The polygon version baked the same texture against the
// sector's floor; the difference is only what the ray hits.

import (
	"github.com/go-gl/mathgl/mgl32"
)

// The kinds a scatter volume can grow. Every one of them puts real instanced
// geometry in the world; the old model's Sand and Water were ground-patch and
// water-plane features with no scatter at all, and neither survives as a kind
// here.
const (
	ScatterGrass int32 = iota
	ScatterRock
	ScatterWildflower
	ScatterCattail
	ScatterMoss
)

// ScatterKindNames is the YAML/UI name of each kind, indexed by the kind
// itself — the same arrangement VolumeShapeNames uses, so adding one means
// adding its name here and nowhere else.
var ScatterKindNames = []string{"grass", "rock", "wildflower", "cattail", "moss"}

// ScatterKindName is a kind's name, falling back to grass for a value out of a
// hand-edited file.
func ScatterKindName(k int32) string {
	if k < 0 || int(k) >= len(ScatterKindNames) {
		return ScatterKindNames[ScatterGrass]
	}
	return ScatterKindNames[k]
}

// ScatterKindFromName is the inverse, defaulting to grass for an unknown name
// on the same terms VolumeShapeFromName does.
func ScatterKindFromName(name string) int32 {
	for i, n := range ScatterKindNames {
		if n == name {
			return int32(i)
		}
	}
	return ScatterGrass
}

// Default camera-ring draw radii per kind, in world units.
const (
	defaultGrassRange      = float32(45)
	defaultRockRange       = float32(90)
	defaultWildflowerRange = float32(40)
	defaultCattailRange    = float32(40)
	defaultMossRange       = float32(35) // small clumps: no need to draw as far as boulders
)

// Default appearance.
const (
	DefaultGrassHeight    = float32(0.55)
	DefaultGrassWidth     = float32(0.045)
	DefaultRockSides      = int32(4) // octahedron (8 faces)
	DefaultRockScale      = float32(0.35)
	defaultScatterDensity = float32(0.8)

	DefaultWildflowerHeight = float32(0.4)
	DefaultWildflowerWidth  = float32(0.035)
	DefaultCattailHeight    = float32(1.1)
	DefaultCattailWidth     = float32(0.03)

	// World size of the wildflower bloom / cattail seed head, independent of
	// stem height/width.
	DefaultWildflowerHeadScale = float32(0.18)
	DefaultCattailHeadScale    = float32(0.16)

	// Multiplier on a wildflower's basal leaves, whose geometry is already
	// authored as fractions of the stem's height — so 1 is "as designed", not
	// "one world unit".
	DefaultWildflowerLeafScale = float32(1)
	MaxWildflowerLeafScale     = float32(4)
)

// DefaultCenterColor is the starting colour for a wildflower's optional bloom
// centre — a warm pollen yellow, which reads as a centre against most of the
// shader's flowerPalette hues.
var DefaultCenterColor = mgl32.Vec3{0.95, 0.82, 0.25}

// MaxStemHeight caps Grass/Wildflower's GrassHeight — enforced as the panel
// slider's max and again at render time, so a hand-edited value cannot produce
// an absurd blade. Cattail is deliberately not capped (reeds read fine taller).
const MaxStemHeight = float32(5)

// defaultRangeFadeFraction is the fraction of Range the camera-ring boundary
// fades over on a freshly placed volume.
const defaultRangeFadeFraction = float32(0.4)

// DefaultLOD1Fraction/DefaultLOD2Fraction locate the Near→Mid and Mid→Far LOD
// boundaries as fractions of the volume's own Range when the explicit
// distances are left at 0.
const (
	DefaultLOD1Fraction = float32(0.35)
	DefaultLOD2Fraction = float32(0.70)
)

// ScatterLODFadeFraction is the fraction of a tier's own span its boundary
// dissolves over — a fixed implementation constant rather than a per-volume
// field, since nobody needs to hand-tune where mesh detail swaps the way they
// tune the visible outer edge.
const ScatterLODFadeFraction = float32(0.15)

// DefaultBoundaryJitter is how far (world units) the circular range and LOD
// boundaries bulge by when BoundaryJitter is left unset. Modest against even
// the shortest default range — enough to break up the circle without the
// boundary wandering far enough to look unintentional.
const DefaultBoundaryJitter = float32(4)

// VolumeScatter is what grows inside a scatter volume.
type VolumeScatter struct {
	// Enabled switches the scatter off while keeping the settings, exactly as
	// VolumeFog.Enabled does. Distinct from the volume's own Enabled, which
	// also disarms it as a trigger.
	Enabled bool

	Kind    int32      // ScatterGrass / ScatterRock / ...
	Density float32    // 0..1 survival probability, uniform across the region
	Range   float32    // camera-ring draw distance, world units
	Color   mgl32.Vec3 // tints the rendered blade/rock/flower/reed directly

	// LOD1Distance/LOD2Distance (world units from the camera, kinds with
	// ScatterLODTierCount == 3 only) are the Near→Mid and Mid→Far mesh-detail
	// boundaries. 0 uses the kind's default fraction of Range. The Far tier
	// always runs from LOD2Distance out to Range.
	LOD1Distance float32
	LOD2Distance float32

	// Grass/Wildflower/Cattail stem sizing. Reused across all three
	// blade-shaped kinds rather than duplicating a field per kind — they all
	// scatter from the same tapered-blade template.
	GrassHeight float32
	GrassWidth  float32

	// HeadScale is the world size of a Wildflower's bloom or a Cattail's seed
	// head, independent of the stem. 0 uses the kind's default.
	HeadScale float32

	// GrassClump (Grass only, 0..1) gathers otherwise evenly-scattered blades
	// into tufts that read as individual plants: blades pull in toward a shared
	// root, fan out radially, arc outward along their length, and grade so the
	// middle of a tuft stands tall while the rim splays short.
	//
	// 0 is a MEANINGFUL value here — the even one-blade-per-slot scatter — not
	// the usual "0 means the default", which is why ClampGrassClump clamps an
	// out-of-range value rather than replacing it.
	GrassClump float32

	// Wildflower bloom options. Every zero value is the plainest look, so a
	// volume that never touched them reads as a simple petal disc:
	//   BloomSpike      false = squat petal disc, true = an elongated spike.
	//   BloomCenter     false = petals only, true = a small separate centre
	//                   blob on top of them, coloured by CenterColor.
	//   BloomSolidColor false = per-instance random hue from the shader's
	//                   palette (tinted by Color), true = Color used directly,
	//                   so a volume reads as one deliberate flower colour.
	BloomSpike      bool
	BloomCenter     bool
	BloomSolidColor bool
	CenterColor     mgl32.Vec3

	// StemLeaves (Wildflower only) rings the base of the stem with triangular
	// leaves that arc outward and nod over. LeafScale is a MULTIPLIER on
	// leaves already authored as fractions of the stem's height, so the default
	// keeps them in proportion on a short flower and a tall one alike.
	StemLeaves bool
	LeafScale  float32

	// Rock appearance. RockScale doubles as the clump size for Moss, which
	// never shares a volume with Rock, so there is nothing to collide with.
	RockSides int32
	RockScale float32

	// MinSlopeAngle/MaxSlopeAngle (degrees from world-up) restrict which parts
	// of the surface underneath the volume grow anything — grass on the
	// flatter tops of a displaced rock face, say, and nothing on its cliffs.
	// 0 uses the kind's default (ResolvedMinSlope/ResolvedMaxSlope).
	MinSlopeAngle float32
	MaxSlopeAngle float32

	// RangeFadeFraction is the fraction of Range (measured in from the outer
	// edge) the camera-ring boundary fades over. EdgeFadeDistance is how far in
	// from the VOLUME'S OWN boundary an instance takes to reach full size — the
	// edge the author placed, distinct from the camera ring.
	//
	// Both shrink instances toward their root rather than fading alpha (the
	// pass draws opaque), so a transition reads as vegetation thinning out
	// rather than as a hard line even up close.
	RangeFadeFraction float32
	EdgeFadeDistance  float32

	// BoundaryJitter (world units) bulges the circular camera-ring and LOD
	// boundaries in and out by a smooth low-frequency world-space noise, so the
	// transition reads as an organic patch rather than a circle centred on the
	// player. It perturbs the DISTANCE itself, so every ring bulges together at
	// the same world positions instead of wobbling independently.
	//
	// 0 uses DefaultBoundaryJitter rather than meaning "off": it costs almost
	// nothing, and a perfectly circular pop-in line is not a look worth
	// defaulting to.
	BoundaryJitter float32
}

// DefaultVolumeScatter is what a freshly ticked scatter volume starts as: a
// grass field, on the same terms DefaultVolumeFog gives a fog volume its
// numbers.
func DefaultVolumeScatter() VolumeScatter {
	return VolumeScatter{
		Enabled:           true,
		Kind:              ScatterGrass,
		Density:           defaultScatterDensity,
		Range:             defaultGrassRange,
		Color:             mgl32.Vec3{1, 1, 1},
		GrassHeight:       DefaultGrassHeight,
		GrassWidth:        DefaultGrassWidth,
		RockSides:         DefaultRockSides,
		RockScale:         DefaultRockScale,
		CenterColor:       DefaultCenterColor,
		LeafScale:         DefaultWildflowerLeafScale,
		RangeFadeFraction: defaultRangeFadeFraction,
	}
}

// ---------- resolved values ----------

// ScatterRange is the camera-ring draw radius actually used, applying the
// "0 means the kind's default" convention every resolver here follows.
func ScatterRange(s *VolumeScatter) float32 {
	if s.Range > 0 {
		return s.Range
	}
	switch s.Kind {
	case ScatterRock:
		return defaultRockRange
	case ScatterWildflower:
		return defaultWildflowerRange
	case ScatterCattail:
		return defaultCattailRange
	case ScatterMoss:
		return defaultMossRange
	default:
		return defaultGrassRange
	}
}

// ScatterLODTierCount reports how many LOD tiers a kind renders with. Only the
// kinds that scatter as a dense field of small foliage get all three; Rock is
// sparse and individually read, with no group-clump mesh worth building.
func ScatterLODTierCount(kind int32) int {
	switch kind {
	case ScatterGrass, ScatterWildflower, ScatterCattail, ScatterMoss:
		return 3
	default:
		return 1
	}
}

// ResolvedLOD1 and ResolvedLOD2 resolve the Near→Mid and Mid→Far boundaries to
// world units, then clamp so 0 < LOD1 <= LOD2 <= Range holds even for a
// hand-edited file — the per-tier draw calls assume that ordering
// unconditionally.
func ResolvedLOD1(s *VolumeScatter) float32 {
	rng := ScatterRange(s)
	v := s.LOD1Distance
	if !(v > 0) { // also catches NaN out of a hand-edited file
		v = rng * DefaultLOD1Fraction
	}
	return clampLODDistance(v, rng)
}

func ResolvedLOD2(s *VolumeScatter) float32 {
	rng := ScatterRange(s)
	lod1 := ResolvedLOD1(s)
	v := s.LOD2Distance
	if !(v > 0) {
		v = rng * DefaultLOD2Fraction
	}
	v = clampLODDistance(v, rng)
	if v < lod1 {
		v = lod1
	}
	return v
}

func clampLODDistance(v, rng float32) float32 {
	if !(v > 0) {
		v = rng * 0.01
	}
	if v > rng {
		v = rng
	}
	return v
}

// ClampGrassClump resolves GrassClump to the 0..1 the shader's tuft math is
// parameterised over. Written as `!(v > 0)` so a NaN out of a hand-edited file
// falls to 0 instead of propagating into the uniform.
func ClampGrassClump(v float32) float32 {
	if !(v > 0) {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// ResolvedLeafScale applies the default convention to LeafScale and caps it, so
// a hand-edited file cannot grow leaves large enough to swallow the flower they
// belong to.
func ResolvedLeafScale(v float32) float32 {
	if !(v > 0) {
		return DefaultWildflowerLeafScale
	}
	if v > MaxWildflowerLeafScale {
		return MaxWildflowerLeafScale
	}
	return v
}

// ResolvedBoundaryJitter applies the default convention to BoundaryJitter.
func ResolvedBoundaryJitter(s *VolumeScatter) float32 {
	if s.BoundaryJitter > 0 {
		return s.BoundaryJitter
	}
	return DefaultBoundaryJitter
}

// Default slope gates per kind, in degrees from world-up. Foliage wants ground
// it could actually root in; rock is the one kind that reads correctly on a
// steep face, so it gets the whole range.
const (
	DefaultFoliageMaxSlope = float32(40)
	DefaultRockMaxSlope    = float32(90)
)

// ResolvedMinSlope and ResolvedMaxSlope are the slope gate actually applied,
// in degrees from world-up. A max below the min is a hand-edited file asking
// for nothing at all, and is left as it is rather than silently swapped —
// "nothing grows here" is a legible result, and swapping would hide the typo.
func ResolvedMinSlope(s *VolumeScatter) float32 {
	if s.MinSlopeAngle > 0 {
		return s.MinSlopeAngle
	}
	return 0
}

func ResolvedMaxSlope(s *VolumeScatter) float32 {
	if s.MaxSlopeAngle > 0 {
		return s.MaxSlopeAngle
	}
	if s.Kind == ScatterRock {
		return DefaultRockMaxSlope
	}
	return DefaultFoliageMaxSlope
}

// ---------- serialization ----------

// VolumeScatterFromRaw converts the YAML form. nil in, nil out: a volume with
// no `scatter:` block grows nothing.
func VolumeScatterFromRaw(r *RawVolumeScatter) *VolumeScatter {
	if r == nil {
		return nil
	}
	s := DefaultVolumeScatter()
	// Disabled rather than Enabled, so `scatter: {}` is scatter — see
	// RawVolumeScatter's own note.
	s.Enabled = !r.Disabled
	s.Kind = ScatterKindFromName(r.Kind)
	s.Density = float32Default(r.Density, defaultScatterDensity)
	s.Range = r.Range
	if r.Color != nil {
		s.Color = mgl32.Vec3{r.Color.R, r.Color.G, r.Color.B}
	}
	s.LOD1Distance, s.LOD2Distance = r.LOD1Distance, r.LOD2Distance
	s.GrassHeight = float32Default(r.GrassHeight, DefaultGrassHeight)
	s.GrassWidth = float32Default(r.GrassWidth, DefaultGrassWidth)
	s.HeadScale = r.HeadScale
	s.GrassClump = r.GrassClump
	s.BloomSpike, s.BloomCenter, s.BloomSolidColor = r.BloomSpike, r.BloomCenter, r.BloomSolidColor
	if r.CenterColor != nil {
		s.CenterColor = mgl32.Vec3{r.CenterColor.R, r.CenterColor.G, r.CenterColor.B}
	}
	s.StemLeaves, s.LeafScale = r.StemLeaves, r.LeafScale
	if r.RockSides != 0 {
		s.RockSides = r.RockSides
	}
	s.RockScale = float32Default(r.RockScale, DefaultRockScale)
	s.MinSlopeAngle, s.MaxSlopeAngle = r.MinSlopeAngle, r.MaxSlopeAngle
	s.RangeFadeFraction = float32Default(r.RangeFadeFraction, defaultRangeFadeFraction)
	s.EdgeFadeDistance = r.EdgeFadeDistance
	s.BoundaryJitter = r.BoundaryJitter
	return &s
}

// VolumeScatterToRaw is the inverse.
func VolumeScatterToRaw(s *VolumeScatter) *RawVolumeScatter {
	if s == nil {
		return nil
	}
	color := RawColor{R: s.Color.X(), G: s.Color.Y(), B: s.Color.Z()}
	center := RawColor{R: s.CenterColor.X(), G: s.CenterColor.Y(), B: s.CenterColor.Z()}
	density, gh, gw := s.Density, s.GrassHeight, s.GrassWidth
	rock, fade := s.RockScale, s.RangeFadeFraction
	return &RawVolumeScatter{
		Disabled:          !s.Enabled,
		Kind:              ScatterKindName(s.Kind),
		Density:           &density,
		Range:             s.Range,
		Color:             &color,
		LOD1Distance:      s.LOD1Distance,
		LOD2Distance:      s.LOD2Distance,
		GrassHeight:       &gh,
		GrassWidth:        &gw,
		HeadScale:         s.HeadScale,
		GrassClump:        s.GrassClump,
		BloomSpike:        s.BloomSpike,
		BloomCenter:       s.BloomCenter,
		BloomSolidColor:   s.BloomSolidColor,
		CenterColor:       &center,
		StemLeaves:        s.StemLeaves,
		LeafScale:         s.LeafScale,
		RockSides:         s.RockSides,
		RockScale:         &rock,
		MinSlopeAngle:     s.MinSlopeAngle,
		MaxSlopeAngle:     s.MaxSlopeAngle,
		RangeFadeFraction: &fade,
		EdgeFadeDistance:  s.EdgeFadeDistance,
		BoundaryJitter:    s.BoundaryJitter,
	}
}
