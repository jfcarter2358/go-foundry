package world

// foliage.go — what grows on a terrain: biomes, the species in them, and the
// ecology that decides where each one takes.
//
// # Painting a biome, not placing plants
//
// An author paints faces of a terrain with a biome — Meadow, Broadleaf
// Forest, Mossy Cliff — and never places a plant. A biome is a community of
// species, and each species carries the conditions it grows in rather than a
// count: how much open sky it wants, whether it crowds the foot of a cliff or
// shies from a drop, whether it likes the low wet ground, how patchily it
// spreads, and which kind of surface it grows out of (ground, a wall, the
// underside of an overhang). Painting a whole hillside Meadow gives grass on
// its slopes, flowers in drifts, stones gathered where the slope meets the
// rock, moss and vines on the cliff above and ferns in the shade under the
// overhang — because that is where each of those grows, read off the blocks.
//
// # Deterministic by position
//
// Every decision is a hash of where it is made (terrain_noise.go) and of the
// terrain's FoliageSeed. Placement runs over a jittered grid aligned to the
// chunk rather than to the face, so candidates continue seamlessly across
// faces and sections, and a section rebuilt after an edit comes back exactly
// as it was except where the edit changed the conditions.
//
// foliage_place.go does the placing; the renderer (render/foliage.go) turns
// the instances into geometry.

import (
	"slices"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// FoliageShape is a kind of plant or object the renderer knows how to draw.
type FoliageShape uint8

const (
	// FoliageGrass is a clump of curved blades.
	FoliageGrass FoliageShape = iota
	// FoliageTallGrass is a sparse clump of long blades with seed heads.
	FoliageTallGrass
	// FoliageFlower is a flowering plant: variant 0 a daisy-like head, 1 a
	// spike of small flowers, 2 a cluster of bells.
	FoliageFlower
	// FoliageFern is a rosette of arching fronds.
	FoliageFern
	// FoliageShrub is a low woody bush.
	FoliageShrub
	// FoliageBroadleaf is a deciduous tree.
	FoliageBroadleaf
	// FoliageConifer is a pine or spruce.
	FoliageConifer
	// FoliageRock is a boulder or stone, part-buried.
	FoliageRock
	// FoliagePebbles is a scatter of small stones.
	FoliagePebbles
	// FoliageMoss is a cushion of moss hugging its surface.
	FoliageMoss
	// FoliageVine is a hanging strand of leaves.
	FoliageVine
	// FoliageReed is a clump of reeds or cattails.
	FoliageReed
	// FoliageMushroom is a small cluster of mushrooms.
	FoliageMushroom
	// FoliageLitter is fallen leaves or needles lying on the ground.
	FoliageLitter
	// FoliageDeadwood is a fallen branch.
	FoliageDeadwood
	// FoliagePalm is a palm: a slender, leaning, ringed trunk under a crown
	// of long arching fronds.
	FoliagePalm
	// FoliageCactus is a cactus: variant 0 a saguaro column with arms, 1 a
	// ribbed barrel crowned with flowers, 2 a prickly pear of flat paddles.
	FoliageCactus
	// FoliageAcacia is a savannah tree: a trunk forking low into spreading
	// limbs under a wide, flat-topped canopy.
	FoliageAcacia
	// FoliageJunglePlant is a big-leaved understory plant — elephant ears
	// and banana leaves on long stalks.
	FoliageJunglePlant
	// FoliageDeadBush is a leafless, twiggy desert bush: sagebrush gone
	// grey, a creosote skeleton.
	FoliageDeadBush
	// FoliageIce is glacier ice: variant 0 a faceted ice block, 1 a tall
	// leaning serac, 2 a cluster of broken shards.
	FoliageIce

	FoliageShapeCount
)

// FoliageVariants is how many distinct meshes the renderer builds for each
// shape. Placement picks among them; the renderer must build exactly this
// many.
var FoliageVariants = [FoliageShapeCount]int{
	FoliageGrass: 3, FoliageTallGrass: 2, FoliageFlower: 3, FoliageFern: 2,
	FoliageShrub: 3, FoliageBroadleaf: 3, FoliageConifer: 3, FoliageRock: 4,
	FoliagePebbles: 2, FoliageMoss: 2, FoliageVine: 3, FoliageReed: 2,
	FoliageMushroom: 2, FoliageLitter: 2, FoliageDeadwood: 2, FoliagePalm: 3,
	FoliageCactus: 3, FoliageAcacia: 3, FoliageJunglePlant: 2, FoliageDeadBush: 2,
	FoliageIce: 3,
}

var foliageShapeNames = [FoliageShapeCount]string{
	"grass", "tall grass", "flower", "fern", "shrub", "broadleaf tree", "conifer",
	"rock", "pebbles", "moss", "vine", "reed", "mushroom", "leaf litter", "deadwood",
	"palm", "cactus", "acacia", "jungle plant", "dead bush", "ice",
}

func (s FoliageShape) String() string {
	if s < FoliageShapeCount {
		return foliageShapeNames[s]
	}
	return "?"
}

// FoliageSurface is which way a surface faces, as a set of bits.
type FoliageSurface uint8

const (
	// OnGround is a surface facing mostly up.
	OnGround FoliageSurface = 1 << iota
	// OnWall is a surface facing mostly sideways.
	OnWall
	// OnCeiling is a surface facing mostly down: the underside of an
	// overhang, a cave roof.
	OnCeiling
)

// FoliageSpecies is one species in a biome: what it looks like and where it
// grows. Ranges are [min, max].
type FoliageSpecies struct {
	Shape FoliageShape
	// Variants restricts which of the shape's meshes this species uses, as a
	// bit set; 0 is all of them.
	Variants uint8
	// Spacing is the typical distance between neighbours where the species
	// grows at its thickest, in units.
	Spacing float32
	// Where is the kinds of surface it grows out of.
	Where FoliageSurface
	// Slope is the range of surface angles from horizontal it takes, in
	// degrees (ground species; walls and ceilings ignore it).
	Slope [2]float32
	// Scale is the range of sizes it grows to.
	Scale [2]float32
	// Colors is the range of its colour; Accents, when non-empty, is a
	// palette its flowers (or caps, or berries) are picked from — patch by
	// patch, so a drift of flowers is one colour with the odd stray.
	Colors  [2]mgl32.Vec3
	Accents []mgl32.Vec3
	// Upright is how much it grows straight up rather than out of its
	// surface: 1 for a tree on a slope, 0 for moss.
	Upright float32
	// Sink is how far into the surface it is set, as a fraction of its size.
	Sink float32
	// Light is the range of sky exposure it thrives in (0 = a cave, 1 = open
	// sky). It fades out over a soft margin either side.
	Light [2]float32
	// Cluster is how patchy it grows: 0 evenly, 1 in tight drifts with bare
	// ground between. ClusterSize is how wide a drift is, in units.
	Cluster, ClusterSize float32
	// Edge is its taste for the foot of walls and cliffs: positive gathers
	// there (fallen stones, ferns), negative avoids the lip of a drop.
	Edge float32
	// Moisture is its taste for low, sheltered ground: positive wants it
	// (reeds), negative avoids it (dry grass).
	Moisture float32
	// Clearance is how many blocks of open air it needs above it.
	Clearance int
	// ColorNoise is how strongly its colour drifts across the landscape — a
	// field is lusher in one place and drier in another.
	ColorNoise float32
	// TopEdge places it along the top edge of a wall face and hangs it down,
	// like a vine over a cliff lip; the wall must be the top of its cliff.
	TopEdge bool
}

// FoliagePreset is a biome's definition: a community of species, the
// texture the ground under them shows and the colour it reads as from afar.
// Built-in presets are the table below; a project's biomes/<name>.fbio files
// (foliage_file.go) replace them by name and add new ones.
type FoliagePreset struct {
	Name  string
	Label string
	// Doc is a line on what the biome is for, shown to the author.
	Doc string
	// GroundTint is the colour the ground under it reads as from a distance,
	// and Cover how much — the density of the grass the eye stops resolving
	// into blades.
	GroundTint mgl32.Vec3
	Cover      float32
	// GroundTexture names a texture (textures/) the painted ground shows over
	// its block's own layer — sand under a desert, leaf mould under a
	// jungle. Empty leaves the block's layer. A texture that does not exist
	// is skipped, not drawn black. GroundScale is how many units one repeat
	// spans (0 reads as DefaultGroundScale), and GroundWalls lets it climb
	// walls and cliffs rather than only the ground that faces up.
	GroundTexture string
	GroundScale   float32
	GroundWalls   bool
	Species       []FoliageSpecies
}

// DefaultGroundScale is how many units one repeat of a biome's ground
// texture spans when its file does not say.
const DefaultGroundScale = 4

// EffectiveGroundScale is GroundScale with its zero read as the default.
func (p *FoliagePreset) EffectiveGroundScale() float32 {
	if p.GroundScale <= 0 {
		return DefaultGroundScale
	}
	return p.GroundScale
}

// TerrainBiome is one slot of a terrain's biome palette.
type TerrainBiome struct {
	// Preset names the biome's definition (FoliagePresets).
	Preset string
	// Density scales how thickly everything in it grows. 0 reads as 1.
	Density float32
	// Tint multiplies every colour in it. Zero reads as white.
	Tint mgl32.Vec3
	// GroundLayer, when 1..4, draws the faces it is painted on with that
	// texture layer rather than their block's.
	GroundLayer int
	// Amounts scales how much of each kind of thing grows, by shape key
	// ("grass", "rock", "broadleaf"): 0 none, 1 as the biome has it, up to
	// MaxFoliageAmount. A shape not in it grows as the biome has it. Keyed
	// by shape rather than by species so a biome file whose species are
	// reordered or split keeps the author's choices.
	Amounts map[string]float32
}

// MaxFoliageAmount is the most one kind of thing can be turned up: four
// times as many as the biome grows.
const MaxFoliageAmount = 4

// Amount is how much of shape s grows in this biome, relative to its
// definition.
func (b *TerrainBiome) Amount(s FoliageShape) float32 {
	if v, ok := b.Amounts[foliageShapeKey(s)]; ok {
		return min(max(v, 0), MaxFoliageAmount)
	}
	return 1
}

// SetAmount sets how much of shape s grows; 1 removes the override.
func (b *TerrainBiome) SetAmount(s FoliageShape, v float32) {
	v = min(max(v, 0), MaxFoliageAmount)
	if v == 1 {
		delete(b.Amounts, foliageShapeKey(s))
		if len(b.Amounts) == 0 {
			b.Amounts = nil
		}
		return
	}
	if b.Amounts == nil {
		b.Amounts = map[string]float32{}
	}
	b.Amounts[foliageShapeKey(s)] = v
}

// Shapes is every kind of thing the biome grows, in the order its species
// first name them.
func (p *FoliagePreset) Shapes() []FoliageShape {
	var out []FoliageShape
	for _, sp := range p.Species {
		if !slices.Contains(out, sp.Shape) {
			out = append(out, sp.Shape)
		}
	}
	return out
}

// Def is the biome's preset, or the first preset if it names none known.
func (b *TerrainBiome) Def() *FoliagePreset {
	if p := FoliagePresetByName(b.Preset); p != nil {
		return p
	}
	return &FoliagePresets[0]
}

// EffectiveDensity is Density with its zero value read as 1.
func (b *TerrainBiome) EffectiveDensity() float32 {
	if b.Density <= 0 {
		return 1
	}
	return b.Density
}

// EffectiveTint is Tint with its zero value read as white.
func (b *TerrainBiome) EffectiveTint() mgl32.Vec3 {
	if b.Tint == (mgl32.Vec3{}) {
		return mgl32.Vec3{1, 1, 1}
	}
	return b.Tint
}

// GroundCover is the tint the ground painted with this biome takes, and how
// much of it.
func (b *TerrainBiome) GroundCover() (mgl32.Vec3, float32) {
	d := b.Def()
	t := b.EffectiveTint()
	c := mgl32.Vec3{d.GroundTint[0] * t[0], d.GroundTint[1] * t[1], d.GroundTint[2] * t[2]}
	return c, min(d.Cover*min(b.EffectiveDensity(), 1.5), 1)
}

// FoliagePresetByName finds a preset, or nil.
func FoliagePresetByName(name string) *FoliagePreset {
	for i := range FoliagePresets {
		if FoliagePresets[i].Name == name {
			return &FoliagePresets[i]
		}
	}
	return nil
}

// FoliagePresetNames lists every preset's name, sorted.
func FoliagePresetNames() []string {
	out := make([]string, len(FoliagePresets))
	for i := range FoliagePresets {
		out[i] = FoliagePresets[i].Name
	}
	sort.Strings(out)
	return out
}

func rgb(r, g, b float32) mgl32.Vec3 { return mgl32.Vec3{r, g, b} }

// Palettes shared between presets.
var (
	meadowFlowers = []mgl32.Vec3{rgb(0.95, 0.93, 0.85), rgb(0.95, 0.80, 0.18), rgb(0.55, 0.35, 0.80), rgb(0.35, 0.45, 0.90), rgb(0.90, 0.35, 0.40), rgb(0.97, 0.60, 0.20)}
	wetFlowers    = []mgl32.Vec3{rgb(0.95, 0.85, 0.20), rgb(0.55, 0.40, 0.85), rgb(0.92, 0.92, 0.95)}
	mushroomCaps  = []mgl32.Vec3{rgb(0.60, 0.40, 0.25), rgb(0.85, 0.80, 0.70), rgb(0.75, 0.20, 0.15)}
	stoneGrey     = [2]mgl32.Vec3{rgb(0.42, 0.41, 0.39), rgb(0.60, 0.58, 0.54)}
	stoneMossy    = [2]mgl32.Vec3{rgb(0.38, 0.40, 0.33), rgb(0.52, 0.53, 0.46)}
	mossGreen     = [2]mgl32.Vec3{rgb(0.22, 0.34, 0.10), rgb(0.36, 0.46, 0.14)}
	ivyGreen      = [2]mgl32.Vec3{rgb(0.16, 0.30, 0.10), rgb(0.28, 0.42, 0.14)}
	jungleGreen   = [2]mgl32.Vec3{rgb(0.12, 0.30, 0.08), rgb(0.24, 0.44, 0.12)}
	palmGreen     = [2]mgl32.Vec3{rgb(0.26, 0.42, 0.14), rgb(0.40, 0.52, 0.20)}
	cactusGreen   = [2]mgl32.Vec3{rgb(0.42, 0.54, 0.34), rgb(0.54, 0.64, 0.42)}
	desertFlowers = []mgl32.Vec3{rgb(0.95, 0.35, 0.45), rgb(0.97, 0.80, 0.25), rgb(0.92, 0.55, 0.20)}
	jungleFlowers = []mgl32.Vec3{rgb(0.92, 0.20, 0.25), rgb(0.97, 0.55, 0.12), rgb(0.85, 0.30, 0.65), rgb(0.98, 0.85, 0.25)}
	palmTrunks    = []mgl32.Vec3{rgb(0.56, 0.49, 0.40), rgb(0.62, 0.56, 0.46), rgb(0.50, 0.44, 0.36)}
	sandStone     = [2]mgl32.Vec3{rgb(0.66, 0.54, 0.40), rgb(0.80, 0.68, 0.52)}
	redRock       = [2]mgl32.Vec3{rgb(0.55, 0.28, 0.17), rgb(0.72, 0.42, 0.26)}
	basalt        = [2]mgl32.Vec3{rgb(0.16, 0.16, 0.16), rgb(0.26, 0.25, 0.24)}
	lichenGrey    = [2]mgl32.Vec3{rgb(0.48, 0.50, 0.44), rgb(0.62, 0.62, 0.54)}
	woollyMoss    = [2]mgl32.Vec3{rgb(0.42, 0.48, 0.36), rgb(0.54, 0.60, 0.44)}
	dryGold       = [2]mgl32.Vec3{rgb(0.66, 0.56, 0.30), rgb(0.80, 0.68, 0.40)}
	bleachedWood  = [2]mgl32.Vec3{rgb(0.60, 0.56, 0.50), rgb(0.76, 0.72, 0.66)}
	greyTwigs     = [2]mgl32.Vec3{rgb(0.40, 0.36, 0.32), rgb(0.54, 0.50, 0.44)}
	iceBlue       = [2]mgl32.Vec3{rgb(0.70, 0.84, 0.92), rgb(0.86, 0.94, 0.98)}
	morainGrey    = [2]mgl32.Vec3{rgb(0.36, 0.36, 0.36), rgb(0.50, 0.49, 0.47)}
	frostedStone  = [2]mgl32.Vec3{rgb(0.56, 0.58, 0.60), rgb(0.72, 0.74, 0.76)}
	mesaRock      = [2]mgl32.Vec3{rgb(0.66, 0.38, 0.22), rgb(0.82, 0.56, 0.36)}
	juniperGreen  = [2]mgl32.Vec3{rgb(0.22, 0.30, 0.18), rgb(0.32, 0.38, 0.24)}
	graniteGrey   = [2]mgl32.Vec3{rgb(0.46, 0.45, 0.43), rgb(0.62, 0.60, 0.57)}
)

// allSurfaces is every kind of surface.
const allSurfaces = OnGround | OnWall | OnCeiling

// FoliagePresets are the biomes an author can paint: the built-ins with a
// project's biome files laid over them (LoadBiomes). Main thread only.
var FoliagePresets = append([]FoliagePreset(nil), builtinFoliagePresets...)

// builtinFoliagePresets are the biomes the engine knows without any file —
// what a project with no biomes/ directory paints with, and what
// `foundry biomes export` writes out as a starting point.
var builtinFoliagePresets = []FoliagePreset{
	{
		Name: "meadow", Label: "Meadow",
		Doc:           "Open grassland: grass, drifts of flowers, shrubs and stones at the foot of slopes, moss and vines on cliffs.",
		GroundTexture: "ground_meadow",
		GroundTint:    rgb(0.34, 0.46, 0.16), Cover: 0.8,
		Species: []FoliageSpecies{
			{Shape: FoliageGrass, Spacing: 0.3, Where: OnGround, Slope: [2]float32{0, 55}, Scale: [2]float32{0.75, 1.2},
				Colors: [2]mgl32.Vec3{rgb(0.28, 0.44, 0.12), rgb(0.42, 0.54, 0.17)}, Upright: 0.6,
				Light: [2]float32{0.25, 1}, Cluster: 0.12, ClusterSize: 5, Edge: -0.3, ColorNoise: 0.35},
			{Shape: FoliageTallGrass, Spacing: 0.75, Where: OnGround, Slope: [2]float32{0, 45}, Scale: [2]float32{0.8, 1.35},
				Colors: [2]mgl32.Vec3{rgb(0.42, 0.50, 0.20), rgb(0.60, 0.58, 0.30)}, Upright: 0.8,
				Light: [2]float32{0.3, 1}, Cluster: 0.65, ClusterSize: 7, Edge: 0.8, ColorNoise: 0.3},
			{Shape: FoliageFlower, Spacing: 0.9, Where: OnGround, Slope: [2]float32{0, 35}, Scale: [2]float32{0.8, 1.25},
				Colors: [2]mgl32.Vec3{rgb(0.26, 0.42, 0.14), rgb(0.36, 0.50, 0.18)}, Accents: meadowFlowers, Upright: 0.85,
				Light: [2]float32{0.5, 1}, Cluster: 0.8, ClusterSize: 6, ColorNoise: 0.15},
			{Shape: FoliageShrub, Spacing: 7, Where: OnGround, Slope: [2]float32{0, 40}, Scale: [2]float32{0.55, 1.0},
				Colors: [2]mgl32.Vec3{rgb(0.20, 0.34, 0.12), rgb(0.32, 0.42, 0.16)}, Upright: 0.9,
				Light: [2]float32{0.35, 1}, Cluster: 0.5, ClusterSize: 10, Edge: 1.2, Clearance: 2},
			{Shape: FoliageRock, Spacing: 6, Where: OnGround, Slope: [2]float32{0, 70}, Scale: [2]float32{0.2, 0.65},
				Colors: stoneGrey, Sink: 0.35, Cluster: 0.45, ClusterSize: 8, Edge: 1.5},
			{Shape: FoliagePebbles, Spacing: 3, Where: OnGround, Slope: [2]float32{0, 50}, Scale: [2]float32{0.7, 1.2},
				Colors: stoneGrey, Sink: 0.2, Cluster: 0.6, ClusterSize: 5, Edge: 1},
			{Shape: FoliageMoss, Spacing: 0.9, Where: OnWall | OnCeiling, Scale: [2]float32{0.6, 1.3},
				Colors: mossGreen, Light: [2]float32{0, 0.85}, Cluster: 0.55, ClusterSize: 3, Moisture: 0.4},
			{Shape: FoliageVine, Spacing: 2.2, Where: OnWall, TopEdge: true, Scale: [2]float32{0.6, 1.3},
				Colors: ivyGreen, Cluster: 0.6, ClusterSize: 4},
			{Shape: FoliageVine, Spacing: 1.6, Where: OnCeiling, Scale: [2]float32{0.5, 1.1},
				Colors: ivyGreen, Cluster: 0.5, ClusterSize: 3},
		},
	},
	{
		Name: "forest", Label: "Broadleaf Forest",
		Doc:           "Temperate broadleaf woodland: trees with shrubs, ferns, mushrooms and leaf litter beneath.",
		GroundTexture: "ground_forest",
		GroundTint:    rgb(0.27, 0.24, 0.13), Cover: 0.9,
		Species: []FoliageSpecies{
			{Shape: FoliageBroadleaf, Spacing: 6.5, Where: OnGround, Slope: [2]float32{0, 40}, Scale: [2]float32{0.75, 1.3},
				Colors: [2]mgl32.Vec3{rgb(0.20, 0.34, 0.10), rgb(0.34, 0.46, 0.14)}, Upright: 1,
				Light: [2]float32{0.45, 1}, Cluster: 0.15, ClusterSize: 20, Edge: -0.6, Clearance: 6, ColorNoise: 0.4},
			{Shape: FoliageShrub, Spacing: 3.2, Where: OnGround, Slope: [2]float32{0, 45}, Scale: [2]float32{0.5, 1.0},
				Colors: [2]mgl32.Vec3{rgb(0.18, 0.32, 0.10), rgb(0.30, 0.40, 0.14)}, Upright: 0.9,
				Light: [2]float32{0.15, 0.9}, Cluster: 0.55, ClusterSize: 8, Edge: 0.6, Clearance: 2},
			{Shape: FoliageFern, Spacing: 0.9, Where: OnGround, Slope: [2]float32{0, 55}, Scale: [2]float32{0.7, 1.3},
				Colors: [2]mgl32.Vec3{rgb(0.20, 0.38, 0.10), rgb(0.32, 0.48, 0.14)}, Upright: 0.6,
				Light: [2]float32{0, 0.75}, Cluster: 0.6, ClusterSize: 5, Edge: 0.8, Moisture: 0.5, ColorNoise: 0.2},
			{Shape: FoliageGrass, Spacing: 0.45, Where: OnGround, Slope: [2]float32{0, 50}, Scale: [2]float32{0.6, 1.0},
				Colors: [2]mgl32.Vec3{rgb(0.24, 0.40, 0.11), rgb(0.34, 0.48, 0.15)}, Upright: 0.6,
				Light: [2]float32{0.55, 1}, Cluster: 0.45, ClusterSize: 4, ColorNoise: 0.3},
			{Shape: FoliageLitter, Spacing: 0.4, Where: OnGround, Slope: [2]float32{0, 45}, Scale: [2]float32{0.7, 1.3},
				Colors: [2]mgl32.Vec3{rgb(0.45, 0.30, 0.14), rgb(0.62, 0.42, 0.18)}, Accents: []mgl32.Vec3{rgb(0.70, 0.38, 0.12), rgb(0.55, 0.45, 0.20), rgb(0.40, 0.26, 0.12)},
				Light: [2]float32{0, 0.8}, Cluster: 0.3, ClusterSize: 3},
			{Shape: FoliageMushroom, Spacing: 4, Where: OnGround, Slope: [2]float32{0, 35}, Scale: [2]float32{0.7, 1.3},
				Colors: [2]mgl32.Vec3{rgb(0.85, 0.82, 0.74), rgb(0.92, 0.88, 0.80)}, Accents: mushroomCaps,
				Light: [2]float32{0, 0.55}, Cluster: 0.85, ClusterSize: 3, Moisture: 0.6},
			{Shape: FoliageDeadwood, Spacing: 9, Where: OnGround, Slope: [2]float32{0, 30}, Scale: [2]float32{0.7, 1.3},
				Colors: [2]mgl32.Vec3{rgb(0.30, 0.24, 0.17), rgb(0.42, 0.35, 0.26)}, Sink: 0.1, Cluster: 0.3, ClusterSize: 10},
			{Shape: FoliageRock, Spacing: 8, Where: OnGround, Slope: [2]float32{0, 70}, Scale: [2]float32{0.3, 0.9},
				Colors: stoneMossy, Sink: 0.4, Cluster: 0.5, ClusterSize: 10, Edge: 1.2},
			{Shape: FoliageMoss, Spacing: 0.6, Where: OnWall | OnCeiling, Scale: [2]float32{0.7, 1.4},
				Colors: mossGreen, Cluster: 0.4, ClusterSize: 3, Moisture: 0.3},
			{Shape: FoliageFern, Spacing: 2.5, Where: OnWall, Scale: [2]float32{0.5, 0.8},
				Colors: [2]mgl32.Vec3{rgb(0.20, 0.38, 0.10), rgb(0.30, 0.46, 0.14)}, Upright: 0.3, Cluster: 0.6, ClusterSize: 4},
			{Shape: FoliageVine, Spacing: 1.4, Where: OnWall, TopEdge: true, Scale: [2]float32{0.8, 1.6},
				Colors: ivyGreen, Cluster: 0.4, ClusterSize: 4},
			{Shape: FoliageVine, Spacing: 1.0, Where: OnCeiling, Scale: [2]float32{0.6, 1.4},
				Colors: ivyGreen, Cluster: 0.4, ClusterSize: 3},
		},
	},
	{
		Name: "pine", Label: "Pine Forest",
		Doc:           "Conifer forest on a floor of fallen needles, with ferns and mushrooms in the shade.",
		GroundTexture: "ground_pine",
		GroundTint:    rgb(0.33, 0.26, 0.15), Cover: 0.9,
		Species: []FoliageSpecies{
			{Shape: FoliageConifer, Spacing: 5, Where: OnGround, Slope: [2]float32{0, 45}, Scale: [2]float32{0.7, 1.35},
				Colors: [2]mgl32.Vec3{rgb(0.12, 0.24, 0.12), rgb(0.20, 0.32, 0.15)}, Upright: 1,
				Light: [2]float32{0.4, 1}, Cluster: 0.2, ClusterSize: 18, Edge: -0.4, Clearance: 8, ColorNoise: 0.25},
			{Shape: FoliageLitter, Spacing: 0.35, Where: OnGround, Variants: 0b10, Slope: [2]float32{0, 45}, Scale: [2]float32{0.8, 1.4},
				Colors: [2]mgl32.Vec3{rgb(0.48, 0.32, 0.16), rgb(0.62, 0.44, 0.22)}, Light: [2]float32{0, 0.85}, Cluster: 0.2, ClusterSize: 3},
			{Shape: FoliageFern, Spacing: 1.8, Where: OnGround, Slope: [2]float32{0, 50}, Scale: [2]float32{0.6, 1.0},
				Colors: [2]mgl32.Vec3{rgb(0.22, 0.38, 0.12), rgb(0.34, 0.46, 0.15)}, Upright: 0.6,
				Light: [2]float32{0, 0.7}, Cluster: 0.7, ClusterSize: 5, Edge: 0.8, Moisture: 0.4},
			{Shape: FoliageGrass, Spacing: 0.6, Where: OnGround, Slope: [2]float32{0, 45}, Scale: [2]float32{0.6, 0.95},
				Colors: [2]mgl32.Vec3{rgb(0.40, 0.44, 0.20), rgb(0.52, 0.50, 0.26)}, Upright: 0.6,
				Light: [2]float32{0.6, 1}, Cluster: 0.6, ClusterSize: 4, ColorNoise: 0.3},
			{Shape: FoliageRock, Spacing: 5, Where: OnGround, Slope: [2]float32{0, 80}, Scale: [2]float32{0.3, 1.0},
				Colors: stoneMossy, Sink: 0.4, Cluster: 0.55, ClusterSize: 9, Edge: 1.5},
			{Shape: FoliageShrub, Spacing: 6, Where: OnGround, Variants: 0b011, Slope: [2]float32{0, 40}, Scale: [2]float32{0.4, 0.75},
				Colors: [2]mgl32.Vec3{rgb(0.18, 0.30, 0.12), rgb(0.26, 0.36, 0.15)}, Upright: 0.9,
				Light: [2]float32{0.3, 1}, Cluster: 0.5, ClusterSize: 6, Edge: 0.6},
			{Shape: FoliageMushroom, Spacing: 5, Where: OnGround, Slope: [2]float32{0, 35}, Scale: [2]float32{0.6, 1.1},
				Colors: [2]mgl32.Vec3{rgb(0.85, 0.82, 0.74), rgb(0.92, 0.88, 0.80)}, Accents: mushroomCaps,
				Light: [2]float32{0, 0.6}, Cluster: 0.85, ClusterSize: 3},
			{Shape: FoliageDeadwood, Spacing: 10, Where: OnGround, Slope: [2]float32{0, 30}, Scale: [2]float32{0.8, 1.4},
				Colors: [2]mgl32.Vec3{rgb(0.36, 0.28, 0.20), rgb(0.48, 0.40, 0.30)}, Sink: 0.1, Cluster: 0.3, ClusterSize: 10},
			{Shape: FoliageMoss, Spacing: 0.6, Where: OnWall | OnCeiling, Scale: [2]float32{0.6, 1.2},
				Colors: mossGreen, Cluster: 0.5, ClusterSize: 3, Moisture: 0.3},
		},
	},
	{
		Name: "wetland", Label: "Wetland",
		Doc:           "Marsh and lake margins: reeds in the low ground, lush grass, irises.",
		GroundTexture: "ground_wetland",
		GroundTint:    rgb(0.23, 0.33, 0.13), Cover: 0.85,
		Species: []FoliageSpecies{
			{Shape: FoliageReed, Spacing: 0.55, Where: OnGround, Slope: [2]float32{0, 25}, Scale: [2]float32{0.8, 1.35},
				Colors: [2]mgl32.Vec3{rgb(0.32, 0.44, 0.16), rgb(0.48, 0.52, 0.22)}, Accents: []mgl32.Vec3{rgb(0.36, 0.22, 0.12), rgb(0.30, 0.19, 0.11)}, Upright: 1,
				Light: [2]float32{0.4, 1}, Cluster: 0.7, ClusterSize: 6, Moisture: 1, ColorNoise: 0.25},
			{Shape: FoliageGrass, Spacing: 0.28, Where: OnGround, Slope: [2]float32{0, 50}, Scale: [2]float32{0.9, 1.35},
				Colors: [2]mgl32.Vec3{rgb(0.22, 0.40, 0.11), rgb(0.32, 0.48, 0.14)}, Upright: 0.6,
				Light: [2]float32{0.25, 1}, Cluster: 0.1, ClusterSize: 5, ColorNoise: 0.25},
			{Shape: FoliageTallGrass, Spacing: 0.6, Where: OnGround, Slope: [2]float32{0, 40}, Scale: [2]float32{1.0, 1.5},
				Colors: [2]mgl32.Vec3{rgb(0.32, 0.46, 0.16), rgb(0.44, 0.52, 0.20)}, Upright: 0.8,
				Light: [2]float32{0.3, 1}, Cluster: 0.5, ClusterSize: 5, Moisture: 0.5, ColorNoise: 0.25},
			{Shape: FoliageFlower, Spacing: 1.6, Where: OnGround, Variants: 0b110, Slope: [2]float32{0, 25}, Scale: [2]float32{0.9, 1.4},
				Colors: [2]mgl32.Vec3{rgb(0.24, 0.42, 0.14), rgb(0.34, 0.50, 0.18)}, Accents: wetFlowers, Upright: 0.9,
				Light: [2]float32{0.5, 1}, Cluster: 0.8, ClusterSize: 5, Moisture: 0.6},
			{Shape: FoliageMoss, Spacing: 0.5, Where: allSurfaces, Slope: [2]float32{0, 90}, Scale: [2]float32{0.7, 1.3},
				Colors: [2]mgl32.Vec3{rgb(0.20, 0.32, 0.10), rgb(0.30, 0.42, 0.13)}, Light: [2]float32{0, 0.8}, Cluster: 0.6, ClusterSize: 3, Moisture: 0.6},
			{Shape: FoliageVine, Spacing: 1.2, Where: OnWall, TopEdge: true, Scale: [2]float32{0.7, 1.4},
				Colors: ivyGreen, Cluster: 0.4, ClusterSize: 4},
		},
	},
	{
		Name: "scrub", Label: "Dry Scrub",
		Doc:           "Dry, patchy grassland with shrubs, stones and bare dirt between the tufts.",
		GroundTexture: "ground_scrub",
		GroundTint:    rgb(0.54, 0.47, 0.31), Cover: 0.45,
		Species: []FoliageSpecies{
			{Shape: FoliageGrass, Spacing: 0.5, Where: OnGround, Slope: [2]float32{0, 50}, Scale: [2]float32{0.6, 1.05},
				Colors: [2]mgl32.Vec3{rgb(0.55, 0.50, 0.28), rgb(0.70, 0.62, 0.38)}, Upright: 0.6,
				Light: [2]float32{0.3, 1}, Cluster: 0.55, ClusterSize: 3, Moisture: -0.4, ColorNoise: 0.35},
			{Shape: FoliageTallGrass, Spacing: 1.3, Where: OnGround, Slope: [2]float32{0, 45}, Scale: [2]float32{0.7, 1.2},
				Colors: [2]mgl32.Vec3{rgb(0.62, 0.55, 0.32), rgb(0.75, 0.66, 0.42)}, Upright: 0.8,
				Light: [2]float32{0.3, 1}, Cluster: 0.6, ClusterSize: 4, Edge: 0.6, ColorNoise: 0.3},
			{Shape: FoliageShrub, Spacing: 3.5, Where: OnGround, Slope: [2]float32{0, 45}, Scale: [2]float32{0.45, 0.9},
				Colors: [2]mgl32.Vec3{rgb(0.34, 0.38, 0.22), rgb(0.46, 0.46, 0.28)}, Upright: 0.9,
				Light: [2]float32{0.3, 1}, Cluster: 0.4, ClusterSize: 6, Edge: 0.7, ColorNoise: 0.25},
			{Shape: FoliageRock, Spacing: 2.8, Where: OnGround, Slope: [2]float32{0, 80}, Scale: [2]float32{0.2, 0.8},
				Colors: [2]mgl32.Vec3{rgb(0.55, 0.48, 0.40), rgb(0.70, 0.62, 0.52)}, Sink: 0.35, Cluster: 0.5, ClusterSize: 6, Edge: 1.5},
			{Shape: FoliagePebbles, Spacing: 1.3, Where: OnGround, Slope: [2]float32{0, 55}, Scale: [2]float32{0.7, 1.3},
				Colors: [2]mgl32.Vec3{rgb(0.55, 0.50, 0.42), rgb(0.72, 0.64, 0.54)}, Sink: 0.2, Cluster: 0.45, ClusterSize: 4, Edge: 0.8},
			{Shape: FoliageDeadwood, Spacing: 12, Where: OnGround, Slope: [2]float32{0, 30}, Scale: [2]float32{0.5, 1.0},
				Colors: [2]mgl32.Vec3{rgb(0.52, 0.46, 0.38), rgb(0.64, 0.58, 0.48)}, Sink: 0.1, Cluster: 0.3, ClusterSize: 10},
		},
	},
	{
		Name: "cliff", Label: "Mossy Cliff",
		Doc:           "Rock faces and ledges carpeted in moss, ferns in the cracks, vines hanging from the lip.",
		GroundTexture: "ground_cliff", GroundWalls: true,
		GroundTint: rgb(0.30, 0.38, 0.17), Cover: 0.5,
		Species: []FoliageSpecies{
			{Shape: FoliageMoss, Spacing: 0.5, Where: allSurfaces, Slope: [2]float32{0, 90}, Scale: [2]float32{0.7, 1.4},
				Colors: mossGreen, Cluster: 0.5, ClusterSize: 2.5, Moisture: 0.3, ColorNoise: 0.25},
			{Shape: FoliageFern, Spacing: 1.6, Where: OnWall | OnGround, Slope: [2]float32{0, 90}, Scale: [2]float32{0.45, 0.85},
				Colors: [2]mgl32.Vec3{rgb(0.20, 0.38, 0.10), rgb(0.32, 0.46, 0.14)}, Upright: 0.35,
				Light: [2]float32{0, 0.8}, Cluster: 0.65, ClusterSize: 3, Edge: 0.5, Moisture: 0.4},
			{Shape: FoliageVine, Spacing: 0.9, Where: OnWall, TopEdge: true, Scale: [2]float32{0.8, 1.8},
				Colors: ivyGreen, Cluster: 0.35, ClusterSize: 3},
			{Shape: FoliageVine, Spacing: 0.8, Where: OnCeiling, Scale: [2]float32{0.6, 1.5},
				Colors: ivyGreen, Cluster: 0.35, ClusterSize: 3},
			{Shape: FoliageGrass, Spacing: 0.45, Where: OnGround, Slope: [2]float32{0, 60}, Scale: [2]float32{0.6, 1.0},
				Colors: [2]mgl32.Vec3{rgb(0.28, 0.44, 0.13), rgb(0.40, 0.52, 0.17)}, Upright: 0.5,
				Light: [2]float32{0.3, 1}, Cluster: 0.5, ClusterSize: 3, ColorNoise: 0.3},
			{Shape: FoliageRock, Spacing: 4, Where: OnGround, Slope: [2]float32{0, 80}, Scale: [2]float32{0.25, 0.8},
				Colors: stoneMossy, Sink: 0.4, Cluster: 0.5, ClusterSize: 6, Edge: 1.5},
		},
	},
	{
		Name: "scree", Label: "Rocky Scree",
		Doc:           "Broken rock and gravel below cliffs with a little hardy grass.",
		GroundTexture: "ground_scree",
		GroundTint:    rgb(0.46, 0.45, 0.42), Cover: 0.35,
		Species: []FoliageSpecies{
			{Shape: FoliageRock, Spacing: 1.4, Where: OnGround, Slope: [2]float32{0, 85}, Scale: [2]float32{0.25, 1.1},
				Colors: stoneGrey, Sink: 0.35, Cluster: 0.4, ClusterSize: 6, Edge: 1.5, ColorNoise: 0.2},
			{Shape: FoliagePebbles, Spacing: 0.55, Where: OnGround, Slope: [2]float32{0, 60}, Scale: [2]float32{0.8, 1.4},
				Colors: stoneGrey, Sink: 0.2, Cluster: 0.3, ClusterSize: 4, Edge: 0.8},
			{Shape: FoliageGrass, Spacing: 0.9, Where: OnGround, Slope: [2]float32{0, 45}, Scale: [2]float32{0.5, 0.85},
				Colors: [2]mgl32.Vec3{rgb(0.40, 0.46, 0.20), rgb(0.54, 0.54, 0.28)}, Upright: 0.6,
				Light: [2]float32{0.4, 1}, Cluster: 0.8, ClusterSize: 3, Edge: 0.4, ColorNoise: 0.3},
			{Shape: FoliageMoss, Spacing: 1.2, Where: OnWall | OnCeiling, Scale: [2]float32{0.5, 1.0},
				Colors: [2]mgl32.Vec3{rgb(0.40, 0.44, 0.30), rgb(0.55, 0.56, 0.40)}, Cluster: 0.6, ClusterSize: 3},
		},
	},
	{
		Name: "jungle", Label: "Dense Jungle",
		Doc:           "Shaded, crowded rainforest: tall trees, palms, big-leaved understory, ferns, flowers and thick vines.",
		GroundTexture: "ground_jungle",
		GroundTint:    rgb(0.20, 0.26, 0.10), Cover: 1,
		Species: []FoliageSpecies{
			// Emergent trees over a closed canopy; palms where a gap lets
			// light down.
			// Trees shade nothing as far as Light is concerned (it is read off
			// the blocks), and a jungle floor is in shade everywhere, so the
			// understory takes any light rather than waiting for an overhang.
			{Shape: FoliageBroadleaf, Spacing: 4.5, Where: OnGround, Slope: [2]float32{0, 45}, Scale: [2]float32{1.0, 1.7},
				Colors: jungleGreen, Upright: 1,
				Light: [2]float32{0.35, 1}, Cluster: 0.1, ClusterSize: 16, Edge: -0.3, Clearance: 7, ColorNoise: 0.3},
			{Shape: FoliagePalm, Spacing: 7, Where: OnGround, Variants: 0b011, Slope: [2]float32{0, 40}, Scale: [2]float32{0.8, 1.2},
				Colors: palmGreen, Accents: palmTrunks, Upright: 0.9,
				Light: [2]float32{0.45, 1}, Cluster: 0.5, ClusterSize: 10, Clearance: 7, ColorNoise: 0.2},
			{Shape: FoliageJunglePlant, Spacing: 1.5, Where: OnGround, Slope: [2]float32{0, 55}, Scale: [2]float32{0.8, 1.5},
				Colors: jungleGreen, Upright: 0.7,
				Light: [2]float32{0, 1}, Cluster: 0.35, ClusterSize: 5, Edge: 0.6, Moisture: 0.4, Clearance: 2, ColorNoise: 0.2},
			{Shape: FoliageFern, Spacing: 0.7, Where: OnGround, Slope: [2]float32{0, 60}, Scale: [2]float32{0.9, 1.6},
				Colors: jungleGreen, Upright: 0.6,
				Light: [2]float32{0, 1}, Cluster: 0.3, ClusterSize: 4, Edge: 0.6, Moisture: 0.5, ColorNoise: 0.2},
			{Shape: FoliageShrub, Spacing: 2.2, Where: OnGround, Slope: [2]float32{0, 50}, Scale: [2]float32{0.6, 1.2},
				Colors: jungleGreen, Upright: 0.9,
				Light: [2]float32{0.1, 1}, Cluster: 0.4, ClusterSize: 6, Edge: 0.8, Clearance: 2},
			{Shape: FoliageFlower, Spacing: 2.5, Where: OnGround, Variants: 0b101, Slope: [2]float32{0, 40}, Scale: [2]float32{1.0, 1.5},
				Colors: jungleGreen, Accents: jungleFlowers, Upright: 0.8,
				Light: [2]float32{0.2, 1}, Cluster: 0.85, ClusterSize: 4, Moisture: 0.3},
			{Shape: FoliageLitter, Spacing: 0.5, Where: OnGround, Slope: [2]float32{0, 45}, Scale: [2]float32{0.8, 1.4},
				Colors: [2]mgl32.Vec3{rgb(0.36, 0.28, 0.14), rgb(0.50, 0.38, 0.18)}, Accents: []mgl32.Vec3{rgb(0.46, 0.32, 0.14), rgb(0.36, 0.34, 0.16), rgb(0.30, 0.22, 0.10)},
				Light: [2]float32{0, 1}, Cluster: 0.3, ClusterSize: 3},
			{Shape: FoliageMushroom, Spacing: 4, Where: OnGround, Slope: [2]float32{0, 35}, Scale: [2]float32{0.7, 1.3},
				Colors: [2]mgl32.Vec3{rgb(0.85, 0.82, 0.74), rgb(0.92, 0.88, 0.80)}, Accents: mushroomCaps,
				Light: [2]float32{0, 1}, Cluster: 0.85, ClusterSize: 3, Moisture: 0.6},
			{Shape: FoliageDeadwood, Spacing: 8, Where: OnGround, Slope: [2]float32{0, 30}, Scale: [2]float32{0.8, 1.4},
				Colors: [2]mgl32.Vec3{rgb(0.26, 0.22, 0.14), rgb(0.36, 0.32, 0.20)}, Sink: 0.15, Cluster: 0.3, ClusterSize: 10},
			{Shape: FoliageMoss, Spacing: 0.45, Where: allSurfaces, Slope: [2]float32{25, 90}, Scale: [2]float32{0.7, 1.4},
				Colors: mossGreen, Cluster: 0.35, ClusterSize: 3, Moisture: 0.4, ColorNoise: 0.2},
			{Shape: FoliageFern, Spacing: 1.6, Where: OnWall, Scale: [2]float32{0.5, 0.9},
				Colors: jungleGreen, Upright: 0.3, Cluster: 0.5, ClusterSize: 4},
			{Shape: FoliageVine, Spacing: 0.7, Where: OnWall, TopEdge: true, Scale: [2]float32{1.0, 2.0},
				Colors: ivyGreen, Cluster: 0.25, ClusterSize: 3},
			{Shape: FoliageVine, Spacing: 0.6, Where: OnCeiling, Scale: [2]float32{0.8, 1.8},
				Colors: ivyGreen, Cluster: 0.25, ClusterSize: 3},
		},
	},
	{
		Name: "desert", Label: "Desert",
		Doc:           "Open sand with saguaros standing apart, barrel cacti and prickly pears in flower, dead bushes.",
		GroundTexture: "ground_desert",
		GroundTint:    rgb(0.78, 0.64, 0.44), Cover: 0.2,
		Species: []FoliageSpecies{
			// Mostly open sand: saguaros standing apart, barrels and dead
			// bushes between, stones gathered under the rock.
			{Shape: FoliageCactus, Spacing: 11, Where: OnGround, Variants: 0b001, Slope: [2]float32{0, 30}, Scale: [2]float32{0.7, 1.3},
				Colors: cactusGreen, Upright: 1,
				Light: [2]float32{0.6, 1}, Cluster: 0.25, ClusterSize: 20, Edge: -0.5, Moisture: -0.3, Clearance: 4, ColorNoise: 0.15},
			{Shape: FoliageCactus, Spacing: 5, Where: OnGround, Variants: 0b110, Slope: [2]float32{0, 40}, Scale: [2]float32{0.7, 1.3},
				Colors: cactusGreen, Accents: desertFlowers, Upright: 1,
				Light: [2]float32{0.5, 1}, Cluster: 0.6, ClusterSize: 7, Edge: 0.5, Clearance: 1, ColorNoise: 0.15},
			{Shape: FoliageDeadBush, Spacing: 3.2, Where: OnGround, Slope: [2]float32{0, 45}, Scale: [2]float32{0.6, 1.2},
				Colors: greyTwigs, Accents: []mgl32.Vec3{rgb(0.52, 0.56, 0.46), rgb(0.60, 0.62, 0.52)}, Upright: 0.9,
				Light: [2]float32{0.4, 1}, Cluster: 0.4, ClusterSize: 6, Edge: 0.6, ColorNoise: 0.2},
			{Shape: FoliageGrass, Spacing: 1.3, Where: OnGround, Slope: [2]float32{0, 40}, Scale: [2]float32{0.5, 0.9},
				Colors: dryGold, Upright: 0.6,
				Light: [2]float32{0.4, 1}, Cluster: 0.8, ClusterSize: 3, Edge: 0.8, Moisture: 0.4, ColorNoise: 0.3},
			{Shape: FoliageRock, Spacing: 5, Where: OnGround, Slope: [2]float32{0, 80}, Scale: [2]float32{0.25, 1.0},
				Colors: sandStone, Sink: 0.4, Cluster: 0.55, ClusterSize: 8, Edge: 1.6, ColorNoise: 0.15},
			{Shape: FoliagePebbles, Spacing: 2.2, Where: OnGround, Slope: [2]float32{0, 55}, Scale: [2]float32{0.7, 1.3},
				Colors: sandStone, Sink: 0.25, Cluster: 0.5, ClusterSize: 4, Edge: 1},
			{Shape: FoliageDeadwood, Spacing: 16, Where: OnGround, Slope: [2]float32{0, 30}, Scale: [2]float32{0.5, 0.9},
				Colors: bleachedWood, Sink: 0.1, Cluster: 0.3, ClusterSize: 10},
		},
	},
	{
		Name: "beach", Label: "Beach",
		Doc:           "Sand behind the shoreline: leaning palms, dune grass, shells and driftwood.",
		GroundTexture: "ground_beach",
		GroundTint:    rgb(0.86, 0.78, 0.60), Cover: 0.25,
		Species: []FoliageSpecies{
			// Palms lean out over the sand; marram grass holds the dunes;
			// the tideline is shells, pebbles and bleached driftwood.
			{Shape: FoliagePalm, Spacing: 9, Where: OnGround, Slope: [2]float32{0, 30}, Scale: [2]float32{0.8, 1.2},
				Colors: palmGreen, Accents: palmTrunks, Upright: 0.85,
				Light: [2]float32{0.5, 1}, Cluster: 0.55, ClusterSize: 14, Clearance: 7, ColorNoise: 0.25},
			{Shape: FoliageTallGrass, Spacing: 0.9, Where: OnGround, Slope: [2]float32{0, 45}, Scale: [2]float32{0.6, 1.1},
				Colors: [2]mgl32.Vec3{rgb(0.56, 0.58, 0.36), rgb(0.72, 0.68, 0.46)}, Upright: 0.8,
				Light: [2]float32{0.4, 1}, Cluster: 0.7, ClusterSize: 5, Edge: 0.4, Moisture: -0.3, ColorNoise: 0.3},
			{Shape: FoliageGrass, Spacing: 0.9, Where: OnGround, Slope: [2]float32{0, 45}, Scale: [2]float32{0.6, 1.0},
				Colors: [2]mgl32.Vec3{rgb(0.50, 0.56, 0.30), rgb(0.66, 0.66, 0.40)}, Upright: 0.6,
				Light: [2]float32{0.4, 1}, Cluster: 0.75, ClusterSize: 4, Moisture: -0.3, ColorNoise: 0.3},
			{Shape: FoliagePebbles, Spacing: 1.6, Where: OnGround, Slope: [2]float32{0, 40}, Scale: [2]float32{0.6, 1.1},
				Colors: [2]mgl32.Vec3{rgb(0.80, 0.76, 0.70), rgb(0.95, 0.90, 0.84)}, Sink: 0.25, Cluster: 0.6, ClusterSize: 4, Moisture: 0.5},
			{Shape: FoliageRock, Spacing: 8, Where: OnGround, Slope: [2]float32{0, 80}, Scale: [2]float32{0.3, 1.0},
				Colors: stoneGrey, Sink: 0.4, Cluster: 0.6, ClusterSize: 8, Edge: 1.5},
			{Shape: FoliageDeadwood, Spacing: 10, Where: OnGround, Slope: [2]float32{0, 20}, Scale: [2]float32{0.7, 1.4},
				Colors: bleachedWood, Sink: 0.15, Cluster: 0.4, ClusterSize: 12, Moisture: 0.6},
		},
	},
	{
		Name: "tundra", Label: "Tundra",
		Doc:           "Cold, knee-high country: sedge, lichen, dwarf shrubs in autumn red, the odd stunted spruce.",
		GroundTexture: "ground_tundra",
		GroundTint:    rgb(0.50, 0.46, 0.34), Cover: 0.6,
		Species: []FoliageSpecies{
			// Nothing stands higher than the knee: sedge and lichen, dwarf
			// willow and birch in the lee, a stunted spruce or two, stones
			// heaved up by the frost.
			{Shape: FoliageGrass, Spacing: 0.45, Where: OnGround, Slope: [2]float32{0, 55}, Scale: [2]float32{0.45, 0.8},
				Colors: [2]mgl32.Vec3{rgb(0.52, 0.50, 0.30), rgb(0.64, 0.58, 0.36)}, Upright: 0.5,
				Light: [2]float32{0.3, 1}, Cluster: 0.4, ClusterSize: 4, ColorNoise: 0.4},
			{Shape: FoliageMoss, Spacing: 0.55, Where: OnGround, Slope: [2]float32{0, 50}, Scale: [2]float32{0.7, 1.3},
				Colors: lichenGrey, Cluster: 0.5, ClusterSize: 4, Moisture: -0.2, ColorNoise: 0.3},
			{Shape: FoliageShrub, Spacing: 2.4, Where: OnGround, Slope: [2]float32{0, 40}, Scale: [2]float32{0.22, 0.45},
				Colors: [2]mgl32.Vec3{rgb(0.40, 0.30, 0.16), rgb(0.58, 0.28, 0.14)}, Upright: 0.8,
				Light: [2]float32{0.3, 1}, Cluster: 0.6, ClusterSize: 6, Edge: 0.8, Moisture: 0.3, ColorNoise: 0.35},
			{Shape: FoliageTallGrass, Spacing: 1.4, Where: OnGround, Slope: [2]float32{0, 30}, Scale: [2]float32{0.5, 0.75},
				Colors: [2]mgl32.Vec3{rgb(0.60, 0.58, 0.40), rgb(0.72, 0.70, 0.52)}, Upright: 0.9,
				Light: [2]float32{0.4, 1}, Cluster: 0.8, ClusterSize: 5, Moisture: 0.8},
			{Shape: FoliageFlower, Spacing: 2.4, Where: OnGround, Variants: 0b001, Slope: [2]float32{0, 30}, Scale: [2]float32{0.45, 0.7},
				Colors: [2]mgl32.Vec3{rgb(0.34, 0.40, 0.20), rgb(0.44, 0.48, 0.26)}, Accents: []mgl32.Vec3{rgb(0.95, 0.94, 0.88), rgb(0.95, 0.85, 0.30), rgb(0.80, 0.55, 0.85)}, Upright: 0.85,
				Light: [2]float32{0.5, 1}, Cluster: 0.85, ClusterSize: 5},
			{Shape: FoliageConifer, Spacing: 16, Where: OnGround, Slope: [2]float32{0, 30}, Scale: [2]float32{0.25, 0.5},
				Colors: [2]mgl32.Vec3{rgb(0.14, 0.22, 0.14), rgb(0.22, 0.28, 0.17)}, Upright: 1,
				Light: [2]float32{0.4, 1}, Cluster: 0.7, ClusterSize: 20, Edge: 0.4, Clearance: 4},
			{Shape: FoliageRock, Spacing: 4.5, Where: OnGround, Slope: [2]float32{0, 80}, Scale: [2]float32{0.25, 0.9},
				Colors: lichenGrey, Sink: 0.4, Cluster: 0.45, ClusterSize: 8, Edge: 1.3, ColorNoise: 0.2},
			{Shape: FoliagePebbles, Spacing: 1.8, Where: OnGround, Slope: [2]float32{0, 55}, Scale: [2]float32{0.7, 1.2},
				Colors: stoneGrey, Sink: 0.2, Cluster: 0.5, ClusterSize: 4, Edge: 0.8},
			{Shape: FoliageMoss, Spacing: 1.0, Where: OnWall | OnCeiling, Scale: [2]float32{0.5, 1.0},
				Colors: lichenGrey, Cluster: 0.55, ClusterSize: 3},
		},
	},
	{
		Name: "badlands", Label: "Badlands",
		Doc:           "Eroded red earth: boulders piled below the banks, gravel, dead brush, dry grass in the gullies.",
		GroundTexture: "ground_badlands",
		GroundTint:    rgb(0.66, 0.40, 0.26), Cover: 0.25,
		Species: []FoliageSpecies{
			// Red rock and dry wash: boulders fallen under every bank, dead
			// brush, sparse grass in the gullies, a prickly pear here and
			// there.
			{Shape: FoliageRock, Spacing: 2.4, Where: OnGround, Slope: [2]float32{0, 85}, Scale: [2]float32{0.25, 1.2},
				Colors: redRock, Sink: 0.35, Cluster: 0.5, ClusterSize: 7, Edge: 1.8, ColorNoise: 0.25},
			{Shape: FoliagePebbles, Spacing: 1.0, Where: OnGround, Slope: [2]float32{0, 60}, Scale: [2]float32{0.7, 1.4},
				Colors: redRock, Sink: 0.2, Cluster: 0.4, ClusterSize: 4, Edge: 1},
			{Shape: FoliageDeadBush, Spacing: 3.5, Where: OnGround, Slope: [2]float32{0, 45}, Scale: [2]float32{0.6, 1.1},
				Colors: greyTwigs, Accents: []mgl32.Vec3{rgb(0.50, 0.52, 0.42)}, Upright: 0.9,
				Light: [2]float32{0.4, 1}, Cluster: 0.45, ClusterSize: 6, Edge: 0.7, Moisture: 0.2},
			{Shape: FoliageGrass, Spacing: 1.0, Where: OnGround, Slope: [2]float32{0, 35}, Scale: [2]float32{0.5, 0.9},
				Colors: dryGold, Upright: 0.6,
				Light: [2]float32{0.4, 1}, Cluster: 0.75, ClusterSize: 3, Edge: 0.5, Moisture: 0.7, ColorNoise: 0.3},
			{Shape: FoliageCactus, Spacing: 9, Where: OnGround, Variants: 0b100, Slope: [2]float32{0, 30}, Scale: [2]float32{0.6, 1.1},
				Colors: cactusGreen, Accents: desertFlowers, Upright: 1,
				Light: [2]float32{0.5, 1}, Cluster: 0.7, ClusterSize: 8, Clearance: 1},
			{Shape: FoliageDeadwood, Spacing: 14, Where: OnGround, Slope: [2]float32{0, 30}, Scale: [2]float32{0.5, 1.0},
				Colors: bleachedWood, Sink: 0.1, Cluster: 0.3, ClusterSize: 10, Moisture: 0.4},
		},
	},
	{
		Name: "volcanic", Label: "Volcanic Plains",
		Doc:           "An Icelandic lava field: black rubble under woolly grey-green moss, with purple lupine drifts.",
		GroundTexture: "ground_volcanic",
		GroundTint:    rgb(0.30, 0.34, 0.24), Cover: 0.7,
		Species: []FoliageSpecies{
			// An Icelandic lava field: black rubble under a thick, grey-green
			// blanket of woolly moss, lupines in purple drifts, thin grass,
			// and bare basalt wherever it is too steep for the moss to hold.
			{Shape: FoliageMoss, Spacing: 0.35, Where: OnGround, Slope: [2]float32{0, 60}, Scale: [2]float32{0.9, 1.5},
				Colors: woollyMoss, Cluster: 0.2, ClusterSize: 6, Moisture: 0.2, ColorNoise: 0.35},
			{Shape: FoliageRock, Spacing: 2.0, Where: OnGround, Slope: [2]float32{0, 85}, Scale: [2]float32{0.25, 1.0},
				Colors: basalt, Sink: 0.3, Cluster: 0.35, ClusterSize: 6, Edge: 1.3, ColorNoise: 0.15},
			{Shape: FoliagePebbles, Spacing: 0.9, Where: OnGround, Slope: [2]float32{0, 60}, Scale: [2]float32{0.8, 1.4},
				Colors: basalt, Sink: 0.2, Cluster: 0.4, ClusterSize: 4, Edge: 0.8},
			{Shape: FoliageFlower, Spacing: 0.9, Where: OnGround, Variants: 0b010, Slope: [2]float32{0, 35}, Scale: [2]float32{1.0, 1.5},
				Colors: [2]mgl32.Vec3{rgb(0.26, 0.40, 0.18), rgb(0.34, 0.46, 0.22)}, Accents: []mgl32.Vec3{rgb(0.45, 0.32, 0.85), rgb(0.55, 0.36, 0.88), rgb(0.40, 0.40, 0.90), rgb(0.85, 0.55, 0.85)}, Upright: 0.9,
				Light: [2]float32{0.5, 1}, Cluster: 0.7, ClusterSize: 9, Moisture: 0.2},
			{Shape: FoliageGrass, Spacing: 0.8, Where: OnGround, Slope: [2]float32{0, 45}, Scale: [2]float32{0.5, 0.85},
				Colors: [2]mgl32.Vec3{rgb(0.40, 0.46, 0.22), rgb(0.54, 0.54, 0.30)}, Upright: 0.6,
				Light: [2]float32{0.3, 1}, Cluster: 0.7, ClusterSize: 4, Moisture: 0.4, ColorNoise: 0.3},
			{Shape: FoliageMoss, Spacing: 0.8, Where: OnWall | OnCeiling, Scale: [2]float32{0.6, 1.2},
				Colors: woollyMoss, Cluster: 0.6, ClusterSize: 3, Moisture: 0.3},
		},
	},
	{
		Name: "savannah", Label: "Savannah",
		Doc:           "Golden grassland with acacias standing far apart and shrubs in the gullies.",
		GroundTexture: "ground_savannah",
		GroundTint:    rgb(0.68, 0.58, 0.32), Cover: 0.8,
		Species: []FoliageSpecies{
			// A sea of golden grass, acacias standing alone with the whole
			// plain between them, thornbush in the gullies.
			{Shape: FoliageTallGrass, Spacing: 0.7, Where: OnGround, Slope: [2]float32{0, 45}, Scale: [2]float32{0.65, 1.05},
				Colors: dryGold, Upright: 0.8,
				Light: [2]float32{0.3, 1}, Cluster: 0.3, ClusterSize: 8, ColorNoise: 0.4},
			{Shape: FoliageGrass, Spacing: 0.4, Where: OnGround, Slope: [2]float32{0, 55}, Scale: [2]float32{0.7, 1.1},
				Colors: [2]mgl32.Vec3{rgb(0.60, 0.54, 0.28), rgb(0.74, 0.64, 0.36)}, Upright: 0.6,
				Light: [2]float32{0.25, 1}, Cluster: 0.1, ClusterSize: 5, ColorNoise: 0.4},
			{Shape: FoliageAcacia, Spacing: 15, Where: OnGround, Slope: [2]float32{0, 30}, Scale: [2]float32{0.8, 1.3},
				Colors: [2]mgl32.Vec3{rgb(0.26, 0.36, 0.14), rgb(0.38, 0.44, 0.18)}, Upright: 1,
				Light: [2]float32{0.6, 1}, Cluster: 0.1, ClusterSize: 40, Edge: -0.5, Clearance: 6, ColorNoise: 0.2},
			{Shape: FoliageShrub, Spacing: 7, Where: OnGround, Slope: [2]float32{0, 40}, Scale: [2]float32{0.45, 0.9},
				Colors: [2]mgl32.Vec3{rgb(0.30, 0.36, 0.16), rgb(0.42, 0.44, 0.22)}, Upright: 0.9,
				Light: [2]float32{0.3, 1}, Cluster: 0.6, ClusterSize: 8, Edge: 0.8, Moisture: 0.5, Clearance: 2},
			{Shape: FoliageDeadBush, Spacing: 9, Where: OnGround, Variants: 0b01, Slope: [2]float32{0, 40}, Scale: [2]float32{0.6, 1.0},
				Colors: greyTwigs, Upright: 0.9, Cluster: 0.4, ClusterSize: 8},
			{Shape: FoliageRock, Spacing: 10, Where: OnGround, Slope: [2]float32{0, 80}, Scale: [2]float32{0.3, 1.1},
				Colors: [2]mgl32.Vec3{rgb(0.52, 0.44, 0.36), rgb(0.66, 0.58, 0.48)}, Sink: 0.4, Cluster: 0.7, ClusterSize: 10, Edge: 1.5},
		},
	},
	{
		Name: "mesa", Label: "Mesa",
		Doc:           "Banded red tablelands: sediment cliffs, boulders fallen from them, juniper and dry brush on the flats.",
		GroundTexture: "ground_mesa", GroundScale: 10, GroundWalls: true,
		GroundTint: rgb(0.74, 0.46, 0.30), Cover: 0.3,
		Species: []FoliageSpecies{
			// The cliffs are the point; what grows is sparse and keeps to the
			// flats and the foot of the walls.
			{Shape: FoliageRock, Spacing: 3, Where: OnGround, Slope: [2]float32{0, 85}, Scale: [2]float32{0.3, 1.4},
				Colors: mesaRock, Sink: 0.35, Cluster: 0.45, ClusterSize: 8, Edge: 2, ColorNoise: 0.25},
			{Shape: FoliagePebbles, Spacing: 1.2, Where: OnGround, Slope: [2]float32{0, 60}, Scale: [2]float32{0.7, 1.4},
				Colors: mesaRock, Sink: 0.2, Cluster: 0.4, ClusterSize: 5, Edge: 1.2},
			{Shape: FoliageShrub, Spacing: 6, Where: OnGround, Slope: [2]float32{0, 30}, Scale: [2]float32{0.45, 0.9},
				Colors: juniperGreen, Upright: 0.9,
				Light: [2]float32{0.4, 1}, Cluster: 0.45, ClusterSize: 10, Edge: 0.5, Clearance: 2, ColorNoise: 0.2},
			{Shape: FoliageDeadBush, Spacing: 4, Where: OnGround, Slope: [2]float32{0, 40}, Scale: [2]float32{0.6, 1.1},
				Colors: greyTwigs, Accents: []mgl32.Vec3{rgb(0.52, 0.56, 0.46)}, Upright: 0.9,
				Light: [2]float32{0.4, 1}, Cluster: 0.4, ClusterSize: 6, Edge: 0.6},
			{Shape: FoliageGrass, Spacing: 1.1, Where: OnGround, Slope: [2]float32{0, 35}, Scale: [2]float32{0.5, 0.9},
				Colors: dryGold, Upright: 0.6,
				Light: [2]float32{0.4, 1}, Cluster: 0.7, ClusterSize: 4, Moisture: 0.5, ColorNoise: 0.3},
			{Shape: FoliageCactus, Spacing: 12, Where: OnGround, Variants: 0b100, Slope: [2]float32{0, 25}, Scale: [2]float32{0.6, 1.0},
				Colors: cactusGreen, Accents: desertFlowers, Upright: 1,
				Light: [2]float32{0.5, 1}, Cluster: 0.7, ClusterSize: 8, Clearance: 1},
		},
	},
	{
		Name: "glacier", Label: "Glacier",
		Doc:           "A tongue of blue ice: seracs and broken blocks, dark moraine gravel along its edges, nothing growing.",
		GroundTexture: "ground_glacier", GroundScale: 8, GroundWalls: true,
		GroundTint: rgb(0.80, 0.88, 0.94), Cover: 0.2,
		Species: []FoliageSpecies{
			// Seracs stand where the ice breaks over a step — the lip of a
			// drop — and blocks tumble to the foot of it.
			{Shape: FoliageIce, Spacing: 7, Where: OnGround, Variants: 0b010, Slope: [2]float32{0, 50}, Scale: [2]float32{0.7, 1.4},
				Colors: iceBlue, Upright: 0.8, Sink: 0.15,
				Cluster: 0.5, ClusterSize: 12, Edge: 0.6, ColorNoise: 0.15},
			{Shape: FoliageIce, Spacing: 3.5, Where: OnGround, Variants: 0b101, Slope: [2]float32{0, 60}, Scale: [2]float32{0.5, 1.3},
				Colors: iceBlue, Upright: 0.5, Sink: 0.25,
				Cluster: 0.45, ClusterSize: 8, Edge: 1.5, ColorNoise: 0.15},
			{Shape: FoliageRock, Spacing: 6, Where: OnGround, Slope: [2]float32{0, 70}, Scale: [2]float32{0.3, 1.0},
				Colors: morainGrey, Sink: 0.35, Cluster: 0.7, ClusterSize: 10, Edge: 1.8},
			{Shape: FoliagePebbles, Spacing: 1.4, Where: OnGround, Slope: [2]float32{0, 50}, Scale: [2]float32{0.8, 1.4},
				Colors: morainGrey, Sink: 0.2, Cluster: 0.75, ClusterSize: 7, Edge: 1.4},
		},
	},
	{
		Name: "ice_plains", Label: "Ice Plains",
		Doc:           "Flat, wind-scoured snowfields: frozen tufts, ice blocks, frosted stones, lichen on the rock.",
		GroundTexture: "ground_ice_plains", GroundScale: 6,
		GroundTint: rgb(0.88, 0.91, 0.94), Cover: 0.35,
		Species: []FoliageSpecies{
			{Shape: FoliageGrass, Spacing: 1.2, Where: OnGround, Slope: [2]float32{0, 35}, Scale: [2]float32{0.4, 0.7},
				Colors: [2]mgl32.Vec3{rgb(0.62, 0.60, 0.48), rgb(0.76, 0.74, 0.62)}, Upright: 0.6,
				Light: [2]float32{0.3, 1}, Cluster: 0.8, ClusterSize: 4, Edge: 0.6, ColorNoise: 0.2},
			{Shape: FoliageIce, Spacing: 9, Where: OnGround, Variants: 0b101, Slope: [2]float32{0, 30}, Scale: [2]float32{0.4, 1.0},
				Colors: iceBlue, Upright: 0.5, Sink: 0.3, Cluster: 0.55, ClusterSize: 14, ColorNoise: 0.15},
			{Shape: FoliageRock, Spacing: 7, Where: OnGround, Slope: [2]float32{0, 80}, Scale: [2]float32{0.3, 1.0},
				Colors: frostedStone, Sink: 0.4, Cluster: 0.5, ClusterSize: 10, Edge: 1.4},
			{Shape: FoliagePebbles, Spacing: 3, Where: OnGround, Slope: [2]float32{0, 50}, Scale: [2]float32{0.7, 1.2},
				Colors: frostedStone, Sink: 0.25, Cluster: 0.6, ClusterSize: 5, Edge: 0.8},
			{Shape: FoliageDeadBush, Spacing: 14, Where: OnGround, Variants: 0b01, Slope: [2]float32{0, 30}, Scale: [2]float32{0.4, 0.7},
				Colors: greyTwigs, Upright: 0.9, Cluster: 0.6, ClusterSize: 10, Edge: 0.6},
			{Shape: FoliageMoss, Spacing: 1.1, Where: OnWall | OnCeiling, Scale: [2]float32{0.5, 1.0},
				Colors: lichenGrey, Cluster: 0.6, ClusterSize: 3},
		},
	},
	{
		Name: "rocky_cliff", Label: "Rocky Cliff",
		Doc:           "Bare stone faces and ledges: talus piled at the foot, lichen on the rock, a tuft of grass or a stunted pine where soil holds.",
		GroundTexture: "ground_rocky_cliff", GroundScale: 6, GroundWalls: true,
		GroundTint: rgb(0.50, 0.49, 0.46), Cover: 0.3,
		Species: []FoliageSpecies{
			{Shape: FoliageRock, Spacing: 2.2, Where: OnGround, Slope: [2]float32{0, 85}, Scale: [2]float32{0.3, 1.3},
				Colors: graniteGrey, Sink: 0.3, Cluster: 0.4, ClusterSize: 6, Edge: 2, ColorNoise: 0.2},
			{Shape: FoliagePebbles, Spacing: 0.8, Where: OnGround, Slope: [2]float32{0, 65}, Scale: [2]float32{0.8, 1.4},
				Colors: graniteGrey, Sink: 0.2, Cluster: 0.35, ClusterSize: 4, Edge: 1.4},
			{Shape: FoliageMoss, Spacing: 0.9, Where: allSurfaces, Slope: [2]float32{30, 90}, Scale: [2]float32{0.5, 1.1},
				Colors: lichenGrey, Cluster: 0.6, ClusterSize: 3, ColorNoise: 0.3},
			{Shape: FoliageGrass, Spacing: 1.2, Where: OnGround, Slope: [2]float32{0, 40}, Scale: [2]float32{0.5, 0.85},
				Colors: [2]mgl32.Vec3{rgb(0.40, 0.46, 0.22), rgb(0.54, 0.54, 0.30)}, Upright: 0.6,
				Light: [2]float32{0.3, 1}, Cluster: 0.8, ClusterSize: 3, Edge: 0.6, Moisture: 0.4, ColorNoise: 0.3},
			{Shape: FoliageConifer, Spacing: 18, Where: OnGround, Slope: [2]float32{0, 35}, Scale: [2]float32{0.3, 0.55},
				Colors: [2]mgl32.Vec3{rgb(0.14, 0.22, 0.14), rgb(0.22, 0.28, 0.17)}, Upright: 1,
				Light: [2]float32{0.4, 1}, Cluster: 0.6, ClusterSize: 12, Edge: -0.3, Clearance: 4},
			{Shape: FoliageShrub, Spacing: 9, Where: OnGround, Slope: [2]float32{0, 45}, Scale: [2]float32{0.3, 0.6},
				Colors: juniperGreen, Upright: 0.9, Cluster: 0.6, ClusterSize: 6, Edge: 1, Clearance: 1},
		},
	},
}
