package world

// foliage_file.go — biomes as files: resources/biomes/<name>.fbio.
//
// A biome is data — which plants, under what conditions, on what ground — so
// it belongs in a file an author (or Claude) can change without a rebuild.
// The engine still carries its own table (builtinFoliagePresets) so a project
// with no biomes/ directory paints the same meadows it always did; a file
// replaces the built-in of the same name and a new name adds a biome.
//
//	label: Dense Jungle
//	doc: Shaded, crowded rainforest floor.
//	ground:
//	  texture: ground_jungle   # textures/ground_jungle.png, under the paint
//	  scale: 4                 # units per repeat
//	  tint: [0.20, 0.26, 0.10] # what the ground reads as from far off
//	  cover: 1                 # and how strongly
//	species:
//	  - shape: broadleaf
//	    spacing: 4.5
//	    where: [ground]
//	    slope: [0, 45]
//	    scale: [1.0, 1.7]
//	    colors: [[0.12, 0.30, 0.08], [0.24, 0.44, 0.12]]
//	    light: [0.35, 1]
//	    clearance: 7
//
// Vectors and ranges are sequences, never maps: YAML 1.1 reads a bare `y` key
// as the boolean true (see materials-fmat), and a colour is three numbers
// anyway. The decode is strict — an unknown key is an error that names it —
// because a biome that silently ignores a misspelt `spacng` grows at the
// default density and nobody can tell why.
//
// BiomeSchemaText is the whole format in prose, generated from the tables
// here, and is what the editor's Claude tools hand over.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/biomeground"
	"foundry/internal/fileutil"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// BiomesDirName is where biome files live, beside materials/ and particles/.
const BiomesDirName = "biomes"

// BiomeExt is a biome file's extension.
const BiomeExt = ".fbio"

// BiomesDir is the project's biome directory.
func BiomesDir() string { return filepath.Join(fileutil.ResourcesPath, BiomesDirName) }

// BiomePath is where the biome called name is (or would be) written.
func BiomePath(name string) string { return filepath.Join(BiomesDir(), name+BiomeExt) }

// Limits a file is held to. Spacing has a floor because it sets the number of
// candidates per face as its inverse square: a biome file asking for grass
// every centimetre would ask the placer for ten thousand blades a block.
const (
	biomeMinSpacing = 0.15
	biomeMaxSpecies = 32
	biomeMaxAccents = 16
)

// RawBiome is a .fbio file.
type RawBiome struct {
	// Label is the name the editor shows. Defaults to the file name.
	Label string `yaml:"label,omitempty"`
	// Doc is a line on what the biome is for.
	Doc string `yaml:"doc,omitempty"`
	// Ground is what the painted ground looks like.
	Ground RawBiomeGround `yaml:"ground"`
	// Species is what grows there, in the order it is placed.
	Species []RawBiomeSpecies `yaml:"species"`
}

// RawBiomeGround is a biome file's `ground:` block.
type RawBiomeGround struct {
	// Texture names a texture in textures/ the painted ground shows.
	Texture string `yaml:"texture,omitempty"`
	// Scale is how many units one repeat of it spans. Omitted means 4.
	Scale float32 `yaml:"scale,omitempty"`
	// Walls lets the texture climb walls and cliffs too.
	Walls bool `yaml:"walls,omitempty"`
	// Tint is the colour the ground reads as from a distance, [r, g, b].
	Tint []float32 `yaml:"tint,flow"`
	// Cover is how strongly (0..1) the tint is laid over the ground.
	Cover float32 `yaml:"cover"`
}

// RawBiomeSpecies is one entry of a biome file's `species:` list.
type RawBiomeSpecies struct {
	Shape       string      `yaml:"shape"`
	Variants    []int       `yaml:"variants,omitempty,flow"`
	Spacing     float32     `yaml:"spacing"`
	Where       []string    `yaml:"where,omitempty,flow"`
	Slope       []float32   `yaml:"slope,omitempty,flow"`
	Scale       []float32   `yaml:"scale,flow"`
	Colors      [][]float32 `yaml:"colors,flow"`
	Accents     [][]float32 `yaml:"accents,omitempty,flow"`
	Upright     float32     `yaml:"upright,omitempty"`
	Sink        float32     `yaml:"sink,omitempty"`
	Light       []float32   `yaml:"light,omitempty,flow"`
	Cluster     float32     `yaml:"cluster,omitempty"`
	ClusterSize float32     `yaml:"cluster_size,omitempty"`
	Edge        float32     `yaml:"edge,omitempty"`
	Moisture    float32     `yaml:"moisture,omitempty"`
	Clearance   int         `yaml:"clearance,omitempty"`
	ColorNoise  float32     `yaml:"color_noise,omitempty"`
	TopEdge     bool        `yaml:"top_edge,omitempty"`
}

// foliageShapeKeys are the shapes' names in a file: the display name with
// underscores.
func foliageShapeKey(s FoliageShape) string { return strings.ReplaceAll(s.String(), " ", "_") }

// foliageShapeAliases are the short names people write for the shapes whose
// file names are two words.
var foliageShapeAliases = map[string]string{
	"broadleaf": "broadleaf_tree", "tree": "broadleaf_tree", "litter": "leaf_litter",
	"leaves": "leaf_litter", "tallgrass": "tall_grass", "jungleplant": "jungle_plant",
	"deadbush": "dead_bush", "pine": "conifer",
	"spruce": "sitka_spruce", "sitka": "sitka_spruce", "western_hemlock": "hemlock",
	"cedar": "redcedar", "western_redcedar": "redcedar", "red_cedar": "redcedar",
	"fir": "douglas_fir", "douglasfir": "douglas_fir",
	"maple": "bigleaf_maple", "alder": "red_alder", "vinemaple": "vine_maple",
	"swordfern": "sword_fern", "fern2": "forest_fern", "sorrel": "oxalis",
	"undershrub": "under_shrub", "huckleberry": "under_shrub", "salal": "under_shrub",
	"nurselog": "nurse_log", "log": "nurse_log", "branches": "branch_litter",
	"sticks": "branch_litter", "floor_litter": "forest_litter",
	"hummock": "moss_mound",
}

// FoliageShapeByKey finds a shape by its file name (or a short alias).
func FoliageShapeByKey(key string) (FoliageShape, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	if full, ok := foliageShapeAliases[key]; ok {
		key = full
	}
	for s := FoliageShape(0); s < FoliageShapeCount; s++ {
		if foliageShapeKey(s) == key {
			return s, true
		}
	}
	return 0, false
}

// FoliageShapeDocs describes each shape for an author choosing one: what it
// is, how big it grows at scale 1, and what its variants are.
var FoliageShapeDocs = [FoliageShapeCount]string{
	FoliageGrass:        "a clump of curved grass blades, ~0.5 tall; the dense ground cover",
	FoliageTallGrass:    "a sparse clump of long blades with seed heads, ~1.2 tall",
	FoliageFlower:       "a flowering plant; variant 0 a daisy head, 1 a spike (lupine, foxglove), 2 nodding bells; flower colour comes from accents",
	FoliageFern:         "a rosette of arching fronds, ~0.7 tall",
	FoliageShrub:        "a low leafy bush, ~1.2 tall and wide",
	FoliageBroadleaf:    "a deciduous tree, ~6-8 tall with a round crown; needs clearance 5+",
	FoliageConifer:      "a pine or spruce, ~8-10 tall; needs clearance 7+",
	FoliageRock:         "a part-buried boulder, ~1 across; four shapes; use sink 0.3-0.4",
	FoliagePebbles:      "a scatter of small stones, ~1 across",
	FoliageMoss:         "flat patches of moss (or lichen) hugging any surface",
	FoliageVine:         "a hanging strand of leaves; on walls use top_edge to hang it from a cliff's lip, on ceilings it hangs down",
	FoliageReed:         "a clump of reeds with cattail heads (accents colour the heads), ~1.6 tall",
	FoliageMushroom:     "a small cluster of mushrooms; accents colour the caps",
	FoliageLitter:       "fallen leaves (variant 0, accents colour them) or needles (variant 1) lying flat",
	FoliageDeadwood:     "a fallen branch or driftwood log, ~2 long",
	FoliagePalm:         "a palm, ~6-7 tall, trunk leaning more in later variants; accents colour the trunk (coconuts on 0 and 1 are a darker tone of it); needs clearance 6+",
	FoliageCactus:       "variant 0 a saguaro column with arms (~3.5 tall), 1 a ribbed barrel with a crown of flowers, 2 a prickly pear of flat paddles with fruit; accents colour flowers and fruit",
	FoliageAcacia:       "a savannah tree, ~4.5 tall with a wide flat-topped canopy; variant 2 has a lower tier; needs clearance 5+",
	FoliageJunglePlant:  "variant 0 elephant ears on long stalks (~1 tall), 1 a banana with long arching leaves (~2 tall)",
	FoliageDeadBush:     "a leafless twiggy bush ~0.9 tall; variant 1 keeps tufts of leaves at its tips, coloured by accents",
	FoliageIce:          "glacier ice: variant 0 a faceted block ~1 across, 1 a tall leaning serac ~3 tall, 2 a cluster of broken shards; colour it pale blue-white; use sink 0.2-0.3",
	FoliageSitkaSpruce:  "a Sitka spruce, ~40 tall with a buttressed foot and scaly bark; 0 mature, 1 an old moss-hung giant (~48), 2 a colonnade tree on stilt roots; accents colour the hanging moss; needs clearance 8+",
	FoliageHemlock:      "a western hemlock, ~32 tall, slender and lacy with a nodding leader; 2 straddles an old stump on its roots; accents colour the moss; needs clearance 8+",
	FoliageRedcedar:     "a western redcedar, ~30 tall, fluted foot and stringy red bark, J-shaped limbs hung with flat sprays; 1 an old candelabra with a dead spike top; accents colour the moss; needs clearance 8+",
	FoliageDouglasFir:   "a Douglas-fir, ~44 tall, a long clean bole of deeply furrowed bark under a high crown; 1 old and broken-topped, 2 younger and fuller; accents colour the moss; needs clearance 8+",
	FoliageBigleafMaple: "a bigleaf maple, ~20 tall and as wide, great ascending limbs sleeved in moss with licorice ferns and hanging club moss; accents colour the moss; needs clearance 6+",
	FoliageRedAlder:     "a red alder, ~18 tall, straight pale lichen-blotched trunk, narrow crown; 1 leaning, 2 twin-stemmed; accents colour the moss at its foot; needs clearance 6+",
	FoliageVineMaple:    "a vine maple, a sprawling clump of arching mossy stems ~7 tall and wide under flat layers of round leaves; accents colour the moss",
	FoliageCottonwood:   "a black cottonwood, ~28 tall, deeply furrowed grey bark, ascending limbs, open crown of heart-shaped leaves; 2 old with dead limbs; needs clearance 8+",
	FoliageSwordFern:    "a sword fern, a fountain of long leathery fronds ~1.2 tall; 0 large, 1 medium, 2 skirted with dead fronds (accents colour the dead ones brown)",
	FoliageForestFern:   "variant 0 a lady fern (lacy vase, ~1 tall), 1 a deer fern (flat rosette with a few upright fertile fronds)",
	FoliageOxalis:       "a patch of redwood sorrel: trefoil leaves on thin stalks ~0.15 tall; accents colour its few flowers (white or pink)",
	FoliageUnderShrub:   "variant 0 red huckleberry (airy, ~1.8 tall, red berries), 1 salal (dense leathery thicket ~0.9), 2 devil's club (spiny stems ~2 tall, huge leaves, red berry spikes); accents colour the berries",
	FoliageNurseLog:     "variant 0 a fallen moss-covered log ~12 long with young hemlocks on its back, 1 a mossy broken snag ~4 tall, 2 an upturned root wad with its log; colours are the moss and saplings, accents the moss; use upright 0 and sink 0.1-0.2",
	FoliageBranchLitter: "variant 0 a fallen conifer branch ~3 long with dead needles (accents colour them), 1 a scatter of sticks, 2 a mossy fallen limb with ferns",
	FoliageForestLitter: "fallen leaves lying curled on the ground; 0 big maple leaves, 1 conifer needles, cones and sprigs, 2 oval leaves; accents colour the leaves",
	FoliageMossMound:    "a hummock of deep moss ~0.5 tall (0), a moss-capped stone (1) or a moss-buried stub of wood (2); colours are the moss",
}

var foliageSurfaceKeys = []struct {
	key string
	s   FoliageSurface
}{{"ground", OnGround}, {"wall", OnWall}, {"ceiling", OnCeiling}}

// ParseBiome reads a biome file's bytes into a preset named name. Every
// problem is reported, one per line, rather than the first.
func ParseBiome(name string, data []byte) (*FoliagePreset, error) {
	var raw RawBiome
	if err := yaml.UnmarshalStrict(data, &raw); err != nil {
		return nil, fmt.Errorf("%s%s: %w", name, BiomeExt, err)
	}
	return raw.toPreset(name)
}

func (raw *RawBiome) toPreset(name string) (*FoliagePreset, error) {
	var errs []string
	bad := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }
	vec3 := func(where string, v []float32) mgl32.Vec3 {
		if len(v) != 3 {
			bad("%s: want [r, g, b], got %d numbers", where, len(v))
			return mgl32.Vec3{}
		}
		for _, c := range v {
			if c < 0 || c > 2 {
				bad("%s: colour components run 0..1 (up to 2 to overbright), got %g", where, c)
				break
			}
		}
		return mgl32.Vec3{v[0], v[1], v[2]}
	}
	pair := func(where string, v []float32, lo, hi float32, required bool) [2]float32 {
		if len(v) == 0 && !required {
			return [2]float32{}
		}
		if len(v) != 2 {
			bad("%s: want [min, max], got %d numbers", where, len(v))
			return [2]float32{}
		}
		if v[0] > v[1] {
			bad("%s: min %g is above max %g", where, v[0], v[1])
		}
		if v[0] < lo || v[1] > hi {
			bad("%s: must lie within [%g, %g]", where, lo, hi)
		}
		return [2]float32{v[0], v[1]}
	}
	unit := func(where string, v float32) {
		if v < 0 || v > 1 {
			bad("%s: must be 0..1, got %g", where, v)
		}
	}

	p := &FoliagePreset{Name: name, Label: raw.Label, Doc: raw.Doc}
	if p.Label == "" {
		p.Label = name
	}
	g := raw.Ground
	p.GroundTexture = strings.TrimSpace(g.Texture)
	p.GroundScale = g.Scale
	p.GroundWalls = g.Walls
	if g.Scale < 0 || g.Scale > 256 {
		bad("ground.scale: units per repeat, 0 (default) to 256, got %g", g.Scale)
	}
	if len(g.Tint) == 0 {
		bad("ground.tint: required, [r, g, b] — the colour the ground reads as from far off")
	} else {
		p.GroundTint = vec3("ground.tint", g.Tint)
	}
	unit("ground.cover", g.Cover)
	p.Cover = g.Cover

	if len(raw.Species) > biomeMaxSpecies {
		bad("species: at most %d, got %d", biomeMaxSpecies, len(raw.Species))
	}
	for i, rs := range raw.Species {
		at := fmt.Sprintf("species[%d]", i)
		shape, ok := FoliageShapeByKey(rs.Shape)
		if !ok {
			bad("%s.shape: unknown shape %q (one of %s)", at, rs.Shape, strings.Join(FoliageShapeKeys(), ", "))
			continue
		}
		at = fmt.Sprintf("species[%d] (%s)", i, foliageShapeKey(shape))
		sp := FoliageSpecies{Shape: shape}
		for _, v := range rs.Variants {
			if v < 0 || v >= FoliageVariants[shape] {
				bad("%s.variants: %s has variants 0..%d, got %d", at, foliageShapeKey(shape), FoliageVariants[shape]-1, v)
				continue
			}
			sp.Variants |= 1 << uint(v)
		}
		if rs.Spacing < biomeMinSpacing || rs.Spacing > 200 {
			bad("%s.spacing: distance between neighbours in units, %g..200, got %g", at, biomeMinSpacing, rs.Spacing)
		}
		sp.Spacing = rs.Spacing
		if len(rs.Where) == 0 {
			sp.Where = OnGround
		}
		for _, w := range rs.Where {
			found := false
			for _, k := range foliageSurfaceKeys {
				if strings.EqualFold(w, k.key) {
					sp.Where |= k.s
					found = true
				}
			}
			if !found {
				bad("%s.where: %q is not ground, wall or ceiling", at, w)
			}
		}
		sp.Slope = pair(at+".slope", rs.Slope, 0, 90, false)
		sp.Scale = pair(at+".scale", rs.Scale, 0.01, 20, true)
		if len(rs.Colors) != 2 {
			bad("%s.colors: want [[r, g, b], [r, g, b]] — the range its colour is picked from", at)
		} else {
			sp.Colors = [2]mgl32.Vec3{vec3(at+".colors[0]", rs.Colors[0]), vec3(at+".colors[1]", rs.Colors[1])}
		}
		if len(rs.Accents) > biomeMaxAccents {
			bad("%s.accents: at most %d", at, biomeMaxAccents)
		}
		for k, a := range rs.Accents {
			sp.Accents = append(sp.Accents, vec3(fmt.Sprintf("%s.accents[%d]", at, k), a))
		}
		unit(at+".upright", rs.Upright)
		unit(at+".sink", rs.Sink)
		unit(at+".cluster", rs.Cluster)
		unit(at+".color_noise", rs.ColorNoise)
		sp.Upright, sp.Sink, sp.Cluster, sp.ColorNoise = rs.Upright, rs.Sink, rs.Cluster, rs.ColorNoise
		sp.Light = pair(at+".light", rs.Light, 0, 1, false)
		if rs.ClusterSize < 0 || rs.ClusterSize > 500 {
			bad("%s.cluster_size: units, 0..500, got %g", at, rs.ClusterSize)
		}
		sp.ClusterSize = rs.ClusterSize
		if rs.Edge < -3 || rs.Edge > 3 {
			bad("%s.edge: -3..3, got %g", at, rs.Edge)
		}
		if rs.Moisture < -3 || rs.Moisture > 3 {
			bad("%s.moisture: -3..3, got %g", at, rs.Moisture)
		}
		sp.Edge, sp.Moisture = rs.Edge, rs.Moisture
		if rs.Clearance < 0 || rs.Clearance > 32 {
			bad("%s.clearance: blocks of open air, 0..32, got %d", at, rs.Clearance)
		}
		sp.Clearance = rs.Clearance
		if rs.TopEdge && sp.Where&OnWall == 0 {
			bad("%s.top_edge: only means something for a species that grows on walls", at)
		}
		sp.TopEdge = rs.TopEdge
		p.Species = append(p.Species, sp)
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("%s%s:\n  %s", name, BiomeExt, strings.Join(errs, "\n  "))
	}
	return p, nil
}

// FoliageShapeKeys lists every shape's file name, in enum order.
func FoliageShapeKeys() []string {
	out := make([]string, FoliageShapeCount)
	for s := FoliageShape(0); s < FoliageShapeCount; s++ {
		out[s] = foliageShapeKey(s)
	}
	return out
}

// RawBiomeOf is a preset as its file would hold it.
func RawBiomeOf(p *FoliagePreset) RawBiome {
	v3 := func(v mgl32.Vec3) []float32 { return []float32{round3(v[0]), round3(v[1]), round3(v[2])} }
	pr := func(v [2]float32) []float32 {
		if v == ([2]float32{}) {
			return nil
		}
		return []float32{round3(v[0]), round3(v[1])}
	}
	raw := RawBiome{Label: p.Label, Doc: p.Doc, Ground: RawBiomeGround{
		Texture: p.GroundTexture, Scale: p.GroundScale, Walls: p.GroundWalls,
		Tint: v3(p.GroundTint), Cover: p.Cover,
	}}
	for _, sp := range p.Species {
		rs := RawBiomeSpecies{
			Shape: foliageShapeKey(sp.Shape), Spacing: sp.Spacing,
			Slope: pr(sp.Slope), Scale: []float32{round3(sp.Scale[0]), round3(sp.Scale[1])},
			Colors:  [][]float32{v3(sp.Colors[0]), v3(sp.Colors[1])},
			Upright: sp.Upright, Sink: sp.Sink, Light: pr(sp.Light),
			Cluster: sp.Cluster, ClusterSize: sp.ClusterSize, Edge: sp.Edge, Moisture: sp.Moisture,
			Clearance: sp.Clearance, ColorNoise: sp.ColorNoise, TopEdge: sp.TopEdge,
		}
		for k := 0; k < 8; k++ {
			if sp.Variants&(1<<uint(k)) != 0 {
				rs.Variants = append(rs.Variants, k)
			}
		}
		for _, k := range foliageSurfaceKeys {
			if sp.Where&k.s != 0 {
				rs.Where = append(rs.Where, k.key)
			}
		}
		for _, a := range sp.Accents {
			rs.Accents = append(rs.Accents, v3(a))
		}
		raw.Species = append(raw.Species, rs)
	}
	return raw
}

func round3(v float32) float32 {
	return float32(int(v*1000+sign32(v)*0.5)) / 1000
}

func sign32(v float32) float32 {
	if v < 0 {
		return -1
	}
	return 1
}

// MarshalBiome is a preset as a file, with a header that says what it is.
func MarshalBiome(p *FoliagePreset) ([]byte, error) {
	raw := RawBiomeOf(p)
	body, err := yaml.Marshal(&raw)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "# %s%s — a terrain biome: what grows where it is painted, and the ground under it.\n", p.Name, BiomeExt)
	b.WriteString("# The format is docs/guide/30-terrain.md's \"Biome files\"; the editor reloads this on save.\n")
	b.Write(body)
	return b.Bytes(), nil
}

// ---------- the registry ----------

// foliagePresetGeneration moves whenever FoliagePresets changes, so caches
// built from a preset (the ground texture array) can tell.
var foliagePresetGeneration uint64 = 1

// FoliagePresetGeneration is the preset table's change counter.
func FoliagePresetGeneration() uint64 { return foliagePresetGeneration }

// biomeFileSource records which presets came from a file, and which.
var biomeFileSource = map[string]string{}

// BiomeSource is where preset name comes from: a file's path, "built-in", or
// "" if there is no such biome.
func BiomeSource(name string) string {
	if f, ok := biomeFileSource[name]; ok {
		return f
	}
	if FoliagePresetByName(name) != nil {
		return "built-in"
	}
	return ""
}

// BuiltinFoliagePreset is the engine's own definition of name, or nil.
func BuiltinFoliagePreset(name string) *FoliagePreset {
	for i := range builtinFoliagePresets {
		if builtinFoliagePresets[i].Name == name {
			return &builtinFoliagePresets[i]
		}
	}
	return nil
}

// PutFoliagePreset installs p, replacing any preset of its name.
func PutFoliagePreset(p FoliagePreset) {
	foliagePresetGeneration++
	for i := range FoliagePresets {
		if FoliagePresets[i].Name == p.Name {
			FoliagePresets[i] = p
			return
		}
	}
	FoliagePresets = append(FoliagePresets, p)
}

// LoadBiomes resets the presets to the built-ins and lays every biome file in
// dir over them. A file that does not parse is reported and leaves its
// built-in (if any) in place. Returns how many files loaded.
func LoadBiomes(dir string) (int, []error) {
	FoliagePresets = append([]FoliagePreset(nil), builtinFoliagePresets...)
	biomeFileSource = map[string]string{}
	foliagePresetGeneration++
	var errs []error
	n := 0
	for _, file := range fileutil.ScanFilesByExt(dir, BiomeExt) {
		name := strings.TrimSuffix(file, BiomeExt)
		if err := loadBiomeFile(name, filepath.Join(dir, file)); err != nil {
			errs = append(errs, err)
			continue
		}
		n++
	}
	return n, errs
}

func loadBiomeFile(name, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	p, err := ParseBiome(name, data)
	if err != nil {
		return err
	}
	PutFoliagePreset(*p)
	biomeFileSource[name] = path
	return nil
}

// ReloadBiome re-reads one biome file. A file that is gone puts the built-in
// back, or removes the biome if there is none — faces painted with it then
// grow the first preset's plants (TerrainBiome.Def).
func ReloadBiome(dir, name string) error {
	path := filepath.Join(dir, name+BiomeExt)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		delete(biomeFileSource, name)
		if b := BuiltinFoliagePreset(name); b != nil {
			PutFoliagePreset(*b)
			return nil
		}
		for i := range FoliagePresets {
			if FoliagePresets[i].Name == name {
				FoliagePresets = append(FoliagePresets[:i], FoliagePresets[i+1:]...)
				foliagePresetGeneration++
				break
			}
		}
		return nil
	}
	return loadBiomeFile(name, path)
}

// BiomeFileStamps is each biome file's modification stamp, for a poll that
// wants to know whether any changed on disk.
func BiomeFileStamps(dir string) map[string]string {
	out := map[string]string{}
	for _, file := range fileutil.ScanFilesByExt(dir, BiomeExt) {
		if st, err := os.Stat(filepath.Join(dir, file)); err == nil {
			out[strings.TrimSuffix(file, BiomeExt)] = fmt.Sprintf("%d:%d", st.ModTime().UnixNano(), st.Size())
		}
	}
	return out
}

// TouchTerrainsUsing rebuilds every terrain in lvl whose palette paints with
// one of names (all of them when names is empty): its faces take the new
// ground and everything on them regrows. Returns how many it touched.
func TouchTerrainsUsing(lvl *Level, names ...string) int {
	if lvl == nil {
		return 0
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	n := 0
	for ci := range lvl.Chunks {
		t := lvl.Chunks[ci].Terrain
		if t == nil {
			continue
		}
		uses := len(want) == 0
		for _, b := range t.Biomes {
			if want[b.Preset] {
				uses = true
			}
		}
		if uses && len(t.Biomes) > 0 {
			t.Touch()
			n++
		}
	}
	return n
}

// ---------- the format, in words ----------

// BiomeSchemaText describes the biome file format, the shapes and how the
// ecology reads the terrain — everything needed to write one, from the tables
// the parser checks against.
func BiomeSchemaText() string {
	var b strings.Builder
	b.WriteString(`# Biome files: resources/biomes/<name>.fbio (YAML)

A biome is painted onto terrain faces; it decides what grows on them and what
the ground under them looks like. A file replaces the built-in biome of the same
name, or adds a new one. The file name is the biome's name, which a terrain's
palette refers to as "preset: <name>". Decoding is strict: unknown keys are errors.

label: Display name                # optional, defaults to the file name
doc: One line on what it is for    # optional
ground:
  texture: ground_desert   # a texture name in textures/ (no extension) the painted
                           # ground shows over its block's own texture; blends out at
                           # the paint's edge. Omit to keep the block's texture.
  scale: 4                 # units per repeat of that texture (default 4)
  walls: false             # true lets the texture climb walls/cliffs; by default only
                           # ground facing up takes it
  tint: [r, g, b]          # REQUIRED: the colour the ground reads as from far away,
                           # where the plants have thinned out
  cover: 0.8               # 0..1: how strongly the tint is laid over the ground
species:                   # up to 32, placed in order
  - shape: grass           # REQUIRED, see the shapes below
    variants: [0, 2]       # optional: which of the shape's meshes; omit for all
    spacing: 0.3           # REQUIRED: distance between neighbours where it grows
                           # thickest, in units (>= 0.15). Instances per square unit
                           # ~ 1/spacing^2, so this is the performance knob.
    where: [ground]        # ground | wall | ceiling (surface facing up / sideways /
                           # down, from the surface normal). Default [ground].
    slope: [0, 55]         # ground only: surface angle from horizontal it takes, deg
    scale: [0.75, 1.2]     # REQUIRED: size range (1 = the shape's natural size)
    colors: [[r,g,b], [r,g,b]]   # REQUIRED: colour range
    accents: [[r,g,b], ...]      # optional palette for flowers/caps/fruit/trunks,
                                 # picked patch by patch
    upright: 0.6           # 0..1: 1 grows straight up (trees), 0 out of the surface (moss)
    sink: 0.35             # 0..1: how far into the surface, as a fraction of its size
    light: [0.25, 1]       # sky exposure it thrives in, 0 = cave, 1 = open sky. Omit
                           # for any. NOTE: only blocks shade — trees do not — so a
                           # shade lover ([0, 0.7]) never grows on open ground.
    cluster: 0.12          # 0..1: 0 grows evenly, 1 in tight drifts with bare between
    cluster_size: 5        # how wide a drift is, in units
    edge: -0.3             # -3..3: positive gathers at the foot of walls/cliffs,
                           # negative keeps back from the lip of a drop
    moisture: 0            # -3..3: positive wants low sheltered ground, negative avoids it
    clearance: 2           # blocks of open air it needs above (trees: 5-8)
    color_noise: 0.35      # 0..1: how much its colour drifts across the landscape
    top_edge: false        # wall species only: hang from the top edge of a cliff (vines)

Colours are linear 0..1 and read darker than you expect on solid shapes (rocks,
trunks, cacti), which get no light through them: keep their darker colour above
~0.35. Leaves and blades are lit through and can go darker.

## Shapes
`)
	for s := FoliageShape(0); s < FoliageShapeCount; s++ {
		fmt.Fprintf(&b, "- %s (variants 0..%d): %s\n", foliageShapeKey(s), FoliageVariants[s]-1, FoliageShapeDocs[s])
	}
	b.WriteString("Short names also work: broadleaf, litter, dead_bush -> deadbush, and so on.\n\n" +
		"A terrain's palette entry can scale each shape on that terrain only, without touching the file:\n" +
		"  biomes: [{preset: forest, amounts: {broadleaf: 0.3, fern: 2}}]   # 0 none, 1 as the file says, up to 4\n")
	b.WriteString(`
## Ground textures
Any texture in textures/ can be a biome's ground. It should tile seamlessly, and
it is shown triplanar, so it has no "up". The ones the built-in biomes use are
made by ` + "`go run ./cmd/genbiomeground`" + ` (resources/textures/ground_<biome>.png);
add a recipe there to make a new one, or write any tiling PNG.
`)
	return b.String()
}

// FoliagePresetsSorted is every preset, by name.
func FoliagePresetsSorted() []*FoliagePreset {
	out := make([]*FoliagePreset, len(FoliagePresets))
	for i := range FoliagePresets {
		out[i] = &FoliagePresets[i]
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// BiomeTextureProblem is what is wrong with p's ground texture, or "": a
// named texture with no file under textures/ is skipped when drawn, so the
// author would never see why the ground did not change. A built-in ground
// (internal/biomeground) needs no file: the engine paints it.
func BiomeTextureProblem(p *FoliagePreset) string {
	if p.GroundTexture == "" || biomeground.Has(p.GroundTexture) {
		return ""
	}
	if _, err := os.Stat(filepath.Join(fileutil.TexturesDir(), p.GroundTexture+".png")); err != nil {
		return fmt.Sprintf("ground.texture %q has no textures/%s.png", p.GroundTexture, p.GroundTexture)
	}
	return ""
}
