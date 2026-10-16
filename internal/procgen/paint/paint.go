// Package paint is the generators' paint layers: named masks an author
// paints onto the world in the Generator editor, which stages read as one
// more input (docs/terrain-paint/SPEC.md, docs/guide/36-terrain-paint.md).
//
// Paint is an INPUT to a generator, never a placement. A stroke writes a
// layer; a `scatter`, `grass_field`, `terrain_material` or `region_material`
// stage that names the layer reads it as one more band, so a rerun never
// loses paint and painted cover still obeys the land (no grass on a cliff
// because the slope band still says no).
//
// A layer is declared in the .fgen (`paint: [{name, kind, resolution}]`)
// and its pixels live beside it, sparse and tiled:
//
//	generators/<doc>.fgen
//	generators/<doc>.paint/<layer>/<i>_<j>.png
//
// TILES ARE FIXED IN WORLD SPACE: TileSize (64 m) squares counted from the
// world origin, tile (i, j) covering x in [64 i, 64 i + 64) and z in
// [64 j, 64 j + 64). A layer does not need to know which terrain it lies
// on, so it is world-level as the spec asks (one layer serves every region
// of a level generation doc), and survives a terrain being resized or
// re-tiled. 64 m divides the terrain stages' tile sizes that matter (128 m,
// the default and the maximum, and 64 m): a world-aligned 128 m terrain
// tile is exactly 2×2 paint tiles, and a centred terrain with an odd tile
// count still has its edges on multiples of 64 m. Where a terrain tile does
// not line up, TilesOverlapping still answers which paint tiles feed it,
// which is all a per-tile hash (region.go's RectHash) needs.
//
// A tile is TileSize/Resolution texels a side (128 at the default 0.5 m).
// A texel's value sits at its centre; Sample interpolates bilinearly between
// centres, across tile edges, with a missing tile reading 0. UNTOUCHED TILES
// STORE NOTHING, and a stroke that leaves a tile all zero deletes it — so a
// painted-and-erased layer is again no files at all.
//
// The kinds and what one texel holds:
//
//	density    0..1, 8-bit grey    — how much: a band on a rule
//	exclude    0..1, 8-bit grey    — keep clear: a multiplier on every rule
//	material   0..1, 8-bit grey    — a splat override's weight
//	water      0..1, 8-bit grey    — pond / lake / marsh area (ground_edit
//	                                 carves it; a body's level is a spill
//	                                 height or pinned in the stage's
//	                                 `bodies` table, keyed by a point)
//	height     metres, 16-bit grey — a signed offset, 1/256 m steps, ±128 m
//	direction  (x, z), RGB         — a flow vector, -1..1 per axis, in R and G
//
// Values are QUANTISED to the file's precision as they are written, so what
// is in memory is exactly what a save and a load give back, and a tile's
// content hash is the hash of its file's samples.
//
// Pure Go: no world, gfx or procgen imports (internal/layering_test.go
// holds it below procgen, which declares the layers and hands them to the
// stages).
package paint

// paint.go — the kinds, a layer's declaration, and quantisation.

import (
	"fmt"
	"math"
	"regexp"
)

// TileSize is a paint tile's side in metres, counted from the world origin.
const TileSize = 64.0

// DefaultResolution is a layer's metres per texel when its declaration
// does not say.
const DefaultResolution = 0.5

// Resolution bounds: a tile is between 8 and 512 texels a side.
const (
	MinResolution = 0.125
	MaxResolution = 8.0
)

// DirSuffix is what a document's paint folder adds to its path less the
// .fgen: generators/<doc>.paint.
const DirSuffix = ".paint"

// The kinds.
const (
	KindDensity   = "density"
	KindExclude   = "exclude"
	KindDirection = "direction"
	KindHeight    = "height"
	KindWater     = "water"
	KindMaterial  = "material"
)

// Kinds is every kind, in the order the editor offers them.
var Kinds = []string{KindDensity, KindExclude, KindMaterial, KindWater, KindHeight, KindDirection}

// KindDocs say what each kind is for, for the editor's tooltip and the docs.
var KindDocs = map[string]string{
	KindDensity:   "0..1: how much — a `paint` band on scatter and grass rules, or on a material role",
	KindExclude:   "0..1: keep clear — a multiplier on every rule of a stage that lists it under `exclude`",
	KindMaterial:  "0..1: a splat override — painted weight forces a named ground layer (a trail of gravel)",
	KindWater:     "0..1: pond, lake and marsh area — a ground_edit stage's water list carves each painted area as a body",
	KindHeight:    "metres: a signed height offset — a ground_edit stage's height list adds it (flatten paints toward a height)",
	KindDirection: "(x, z) -1..1: a flow vector (oriented placements, phase P4)",
}

// ValidKind reports whether k is a kind.
func ValidKind(k string) bool {
	for _, x := range Kinds {
		if x == k {
			return true
		}
	}
	return false
}

// LayerDef declares a layer: what the document's `paint:` list holds.
type LayerDef struct {
	// Name is the layer's folder and what a stage's mask parameter says:
	// lower-case letters, digits and underscores.
	Name string
	// Kind is one of Kinds.
	Kind string
	// Resolution is metres per texel; 0 reads DefaultResolution.
	Resolution float64
}

// Res is the resolution, DefaultResolution when unset.
func (d LayerDef) Res() float64 {
	if d.Resolution <= 0 {
		return DefaultResolution
	}
	return d.Resolution
}

// TexelsPerTile is a tile's side in texels.
func (d LayerDef) TexelsPerTile() int { return int(math.Round(TileSize / d.Res())) }

// Channels is how many values a texel holds: 2 for direction, else 1.
func (d LayerDef) Channels() int {
	if d.Kind == KindDirection {
		return 2
	}
	return 1
}

// Signed reports whether a texel's values run below zero (height,
// direction) rather than 0..1.
func (d LayerDef) Signed() bool { return d.Kind == KindHeight || d.Kind == KindDirection }

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// ValidName reports whether name is a legal layer name.
func ValidName(name string) bool { return namePattern.MatchString(name) }

// Validate says what is wrong with the declaration, or nil.
func (d LayerDef) Validate() error {
	if !ValidName(d.Name) {
		return fmt.Errorf("paint layer %q: a name is lower-case letters, digits and underscores, starting with a letter", d.Name)
	}
	if !ValidKind(d.Kind) {
		return fmt.Errorf("paint layer %s: kind %q is not one of %v", d.Name, d.Kind, Kinds)
	}
	r := d.Res()
	if r < MinResolution || r > MaxResolution {
		return fmt.Errorf("paint layer %s: resolution %g m is outside %g..%g", d.Name, r, MinResolution, MaxResolution)
	}
	if n := TileSize / r; math.Abs(n-math.Round(n)) > 1e-9 {
		return fmt.Errorf("paint layer %s: resolution %g m must divide the %g m paint tile", d.Name, r, TileSize)
	}
	return nil
}

// Quantize is v at the precision the kind's file stores: 1/255 steps in
// 0..1 for the grey kinds, 1/256 m in ±128 m for height, 1/127 steps in
// -1..1 (zero exact) for direction.
func (d LayerDef) Quantize(v float64) float64 {
	switch d.Kind {
	case KindHeight:
		return float64(int(encodeHeight(v))-32768) / 256
	case KindDirection:
		return float64(int(encodeDir(v))-128) / 127
	}
	return float64(encodeUnit(v)) / 255
}

// encodeUnit is a 0..1 value as a byte.
func encodeUnit(v float64) uint8 {
	return uint8(math.Max(0, math.Min(1, v))*255 + 0.5)
}

// encodeHeight is metres as a 16-bit sample, 32768 = 0.
func encodeHeight(v float64) uint16 {
	s := math.Round(v*256) + 32768
	return uint16(math.Max(0, math.Min(65535, s)))
}

// encodeDir is a -1..1 component as a byte, 128 = 0.
func encodeDir(v float64) uint8 {
	s := math.Round(math.Max(-1, math.Min(1, v))*127) + 128
	return uint8(s)
}
