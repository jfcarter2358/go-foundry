package world

// tree.go — Tree, a plant placed by hand.
//
// A biome grows what it grows wherever it is painted (foliage.go); the author
// chooses the community, never a plant. That is right for undergrowth and
// wrong for the trees a scene is composed around: where each giant stands,
// which way it leans, how big it is, is the author's call. So a tree is a
// level object of its own, placed and moved like scenery, and drawn exactly
// as a grown plant is — the same mesh, Nanite foliage, wind and shadows
// (render/foliage_placed.go) — because it is one: a foliage shape and
// variant, with a transform and colours instead of a biome's ecology.
//
// Any foliage shape can be placed this way; the temperate rainforest's trees
// (FoliageSitkaSpruce through FoliageCottonwood) exist for it, and the
// rainforest biome grows only their undergrowth.
//
// Like scenery, a tree is level-scoped: it stands in world space, and a chunk
// boundary is not something an author aims at.

import (
	"hash/fnv"
	"log"
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// Tree is a placed plant.
type Tree struct {
	// ID names the placement in the Explorer and in status messages, unique
	// within the level like every other object's.
	ID string
	// Species is what it is, and Variant which of that shape's meshes
	// (FoliageVariants); one out of range reads as 0.
	Species FoliageShape
	Variant int
	// Position is the foot of its trunk.
	Position mgl32.Vec3
	// Yaw turns it about its trunk, in radians.
	Yaw float32
	// Scale is its size against the species' own (a Sitka spruce is forty
	// units tall at 1); 0 reads as 1.
	Scale float32
	// Color is its foliage's colour, Accent its moss, berries or flowers
	// (what a biome's colors and accents are). Zero reads as the species'
	// own (TreeColors).
	Color, Accent mgl32.Vec3
	// Solid is whether the player and entities are stopped by its trunk. On
	// by default: a tree you walk through is the exception.
	Solid bool

	// unknown is a species name the file gave that no shape has: kept, so
	// saving the level does not turn a typo into a lost tree, and drawn as
	// nothing meanwhile.
	unknown string
}

// NewTree is a placement of species at pos, at its own size and colours,
// solid — what the editor stamps before the author touches anything.
func NewTree(id string, species FoliageShape, pos mgl32.Vec3) Tree {
	return Tree{ID: id, Species: species, Position: pos, Scale: 1, Solid: true}
}

// EffectiveScale is Scale with its zero read as 1.
func (t *Tree) EffectiveScale() float32 {
	if t.Scale <= 0 {
		return 1
	}
	return t.Scale
}

// EffectiveVariant is Variant clamped to the shape's variants.
func (t *Tree) EffectiveVariant() int {
	if t.Species >= FoliageShapeCount {
		return 0
	}
	if t.Variant < 0 || t.Variant >= FoliageVariants[t.Species] {
		return 0
	}
	return t.Variant
}

// Colors are its foliage and accent colours, the species' own where it
// sets none.
func (t *Tree) Colors() (mgl32.Vec3, mgl32.Vec3) {
	c, a := TreeColors(t.Species)
	if t.Color != (mgl32.Vec3{}) {
		c = t.Color
	}
	if t.Accent != (mgl32.Vec3{}) {
		a = t.Accent
	}
	return c, a
}

// TreeColors is a species' own foliage and accent colour: what a placed
// tree shows until the author picks others.
func TreeColors(s FoliageShape) (mgl32.Vec3, mgl32.Vec3) {
	mid := func(p [2]mgl32.Vec3) mgl32.Vec3 { return p[0].Add(p[1]).Mul(0.5) }
	moss := limbMoss[0]
	switch s {
	case FoliageSitkaSpruce:
		return mid(spruceGreen), moss
	case FoliageHemlock:
		return mid(hemlockGreen), moss
	case FoliageRedcedar:
		return mid(cedarGreen), moss
	case FoliageDouglasFir:
		return mid(firGreen), moss
	case FoliageBigleafMaple:
		return mid(mapleGreen), moss
	case FoliageRedAlder:
		return mid(alderGreen), moss
	case FoliageVineMaple:
		return rgb(0.36, 0.49, 0.15), moss
	case FoliageCottonwood:
		return rgb(0.31, 0.42, 0.15), moss
	case FoliageConifer:
		return rgb(0.16, 0.28, 0.13), moss
	case FoliagePalm:
		return mid(palmGreen), palmTrunks[0]
	case FoliageNurseLog:
		return mid(hemlockGreen), moss
	case FoliageRock:
		return mid(stoneMossy), moss
	}
	return rgb(0.25, 0.40, 0.13), moss
}

// TreeTrunk is how wide (radius) and how tall a species' solid trunk is at
// scale 1: what a placed tree collides as. 0 for a shape with nothing to
// walk into.
func TreeTrunk(s FoliageShape) (radius, height float32) {
	switch s {
	case FoliageSitkaSpruce:
		return 1.4, 40
	case FoliageHemlock:
		return 0.75, 32
	case FoliageRedcedar:
		return 1.5, 30
	case FoliageDouglasFir:
		return 1.25, 44
	case FoliageBigleafMaple:
		return 0.9, 4
	case FoliageRedAlder:
		return 0.35, 16
	case FoliageVineMaple:
		return 0.3, 2
	case FoliageCottonwood:
		return 0.9, 10
	case FoliageBroadleaf:
		return 0.2, 3
	case FoliageConifer:
		return 0.22, 8
	case FoliagePalm, FoliageAcacia:
		return 0.2, 4
	case FoliageCactus:
		return 0.25, 3
	case FoliageRock:
		return 0.45, 0.6
	case FoliageNurseLog:
		return 0.8, 1.5
	}
	return 0, 0
}

// TrunkBox is the box the tree's trunk stands in, in world space, and
// whether it has one (a species with no trunk, or a tree that is not solid,
// has none).
func (t *Tree) TrunkBox() (lo, hi mgl32.Vec3, ok bool) {
	r, h := TreeTrunk(t.Species)
	if !t.Solid || r <= 0 {
		return mgl32.Vec3{}, mgl32.Vec3{}, false
	}
	s := t.EffectiveScale()
	r, h = r*s, h*s
	// A square inside the round trunk, so a body stops at the bark rather
	// than a hand's breadth off it at the diagonals.
	r *= 0.8
	return t.Position.Sub(mgl32.Vec3{r, 0.5, r}), t.Position.Add(mgl32.Vec3{r, h, r}), true
}

// Instance is the tree as the foliage renderer draws it.
func (t *Tree) Instance() FoliageInstance {
	c, a := t.Colors()
	h := fnv.New32a()
	h.Write([]byte(t.ID))
	return FoliageInstance{
		Pos: t.Position, Up: mgl32.Vec3{0, 1, 0}, Yaw: t.Yaw, Scale: t.EffectiveScale(),
		Color: c, Accent: a, Rank: Unit(Hash32(h.Sum32())),
	}
}

// TreeBatches is the level's trees as foliage batches, one per species and
// variant, and a fingerprint of everything in them: when it changes, a tree
// was added, removed, moved, turned, resized or recoloured.
func TreeBatches(lvl *Level) ([]FoliageBatch, uint64) {
	if lvl == nil || len(lvl.Trees) == 0 {
		return nil, 0
	}
	byKey := map[[2]int]int{}
	var out []FoliageBatch
	h := fnv.New64a()
	var b [4]byte
	put := func(f float32) {
		u := math.Float32bits(f)
		b[0], b[1], b[2], b[3] = byte(u), byte(u>>8), byte(u>>16), byte(u>>24)
		h.Write(b[:])
	}
	for i := range lvl.Trees {
		t := &lvl.Trees[i]
		if t.Species >= FoliageShapeCount {
			continue
		}
		in := t.Instance()
		k := [2]int{int(t.Species), t.EffectiveVariant()}
		bi, ok := byKey[k]
		if !ok {
			bi = len(out)
			byKey[k] = bi
			out = append(out, FoliageBatch{Shape: t.Species, Variant: k[1]})
		}
		out[bi].Instances = append(out[bi].Instances, in)
		put(float32(k[0]))
		put(float32(k[1]))
		for _, f := range []float32{in.Pos[0], in.Pos[1], in.Pos[2], in.Yaw, in.Scale,
			in.Color[0], in.Color[1], in.Color[2], in.Accent[0], in.Accent[1], in.Accent[2], in.Rank} {
			put(f)
		}
	}
	return out, h.Sum64() | 1
}

// UniqueTreeID returns base, or base with a number, unused in lvl.
func UniqueTreeID(lvl *Level, base string) string {
	if base == "" {
		base = "tree"
	}
	return uniqueLevelID(lvl, base)
}

// TreeFromRaw converts the YAML form. A species no shape has is logged and
// kept as written.
func TreeFromRaw(r RawTree) Tree {
	t := Tree{
		ID:       r.ID,
		Variant:  r.Variant,
		Position: mgl32.Vec3{r.Position.X, r.Position.Y, r.Position.Z},
		Yaw:      r.Yaw * math.Pi / 180,
		Scale:    r.Scale,
		Solid:    BoolDefault(r.Solid, true),
	}
	if s, ok := FoliageShapeByKey(r.Species); ok {
		t.Species = s
	} else {
		log.Printf("tree %s: no plant shape %q (foundry biomes schema lists them); it draws nothing", r.ID, r.Species)
		t.Species, t.unknown = FoliageShapeCount, r.Species
	}
	if len(r.Color) == 3 {
		t.Color = mgl32.Vec3{r.Color[0], r.Color[1], r.Color[2]}
	}
	if len(r.Accent) == 3 {
		t.Accent = mgl32.Vec3{r.Accent[0], r.Accent[1], r.Accent[2]}
	}
	return t
}

// TreeToRaw is the inverse; defaults are written as absent.
func TreeToRaw(t *Tree) RawTree {
	r := RawTree{
		ID:       t.ID,
		Variant:  t.Variant,
		Position: RawVec3{X: t.Position.X(), Y: t.Position.Y(), Z: t.Position.Z()},
		Yaw:      float32(math.Round(float64(t.Yaw*180/math.Pi)*1000) / 1000),
		Solid:    BoolPtrIfFalse(t.Solid),
	}
	if t.Species < FoliageShapeCount {
		r.Species = foliageShapeKey(t.Species)
	} else {
		r.Species = t.unknown
	}
	if t.Scale > 0 && t.Scale != 1 {
		r.Scale = t.Scale
	}
	if t.Color != (mgl32.Vec3{}) {
		r.Color = []float32{t.Color[0], t.Color[1], t.Color[2]}
	}
	if t.Accent != (mgl32.Vec3{}) {
		r.Accent = []float32{t.Accent[0], t.Accent[1], t.Accent[2]}
	}
	return r
}
