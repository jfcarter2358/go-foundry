// Package grassfield is the Poly Haven grass instancer's data: the GRASS KIT
// (the instanceable meshes a field draws, .fgk) and the GRASS FIELD (one
// terrain tile's instances, .fgrass), their binary formats, how instances
// are packed, sorted and ranked, and the distance rules every consumer of
// them shares (falloff.go). docs/regions/PRESETS_2B.md is the design.
//
// # Why a separate path
//
// A Poly Haven grass set is a few dozen clumps of 0.1–0.3 m, each a few
// hundred to a few thousand triangles of blade cards. A prairie needs a
// continuous 1–2 m sward: hundreds of thousands of clumps per square
// kilometre. As scenery placements (one world.Scenery each, Nanite cull,
// per-placement CPU cost) that was ~1 ms of CPU frame per thousand in view
// (docs/regions/PHASE_6.md), so step 2 of the presets could only afford
// sparse patches over a grass texture. The engine's own foliage biomes
// (world/foliage.go, .fbio) draw dense procedural grass, but the user's
// direction for the presets is Poly Haven plants only. So this is a third
// path: the generator (procgen/stages grass_field) imports Poly Haven grass,
// flower and reed clumps into a kit of small LOD meshes and writes each
// terrain tile's instances as a compact binary file; the level holds ONE
// volume per tile naming its file (world.VolumeGrass); render/grass*.go
// draws them GPU-instanced, culled per 16 m cell on the CPU, thinned by
// distance in a fixed hashed order.
//
// # The kit (.fgk)
//
// Kinds — one per Poly Haven clump (grass_medium_01's tall_a, mid_b, ...)
// — each with its albedo texture (a `_cov` cutout with coverage-preserving
// mips, docs/regions/PRESETS_2.md), its wind and up to MaxLODs meshes:
// LOD 0 the clump as imported (capped), LOD 1 and 2 the same clump THINNED
// (thin.go: a seeded subset of whole blades, each widened to keep the
// clump's cover). Vertices are VertexFloats floats: position, normal, UV
// and the wind weights (how far up the clump and how far out from its axis
// the vertex is), so the vertex stage needs nothing per kind.
//
// # The field (.fgrass)
//
// One terrain tile's instances, 12 bytes each (pack.go): position
// quantised to 16 bits per axis over the tile's box, yaw and scale in a
// byte each, a tint (three bytes, 1.0 at 128) and a RANK byte. The tile is
// cut into square cells (Cell metres); instances are stored grouped by
// (cell, kind) — a RUN — and within a run in increasing rank, so the first
// n of a run are the n lowest ranks. The rank is a hash of the instance's
// world position: fixed, independent of the camera and of anything else in
// the tile. Drawing "the first n" of every run is an even, stable thinning
// (falloff.go), which is what lets the renderer drop distant instances
// without a popping wave: the same instances drop out at the same distance
// every time, each fading over a narrow band of rank.
//
// Pure Go: no GL, no world. The stage writes these files; render reads them.
package grassfield

// Format magics and versions.
const (
	KitMagic     = "FGKT"
	KitVersion   = 1
	FieldMagic   = "FGRS"
	FieldVersion = 1
)

// File extensions.
const (
	KitExt   = ".fgk"
	FieldExt = ".fgrass"
)

// MaxLODs is the most meshes a kind carries.
const MaxLODs = 3

// MaxKinds is the most kinds a kit may hold (a run index is cell·kinds+kind
// in 32 bits, and the vertex stage needs no per-kind table, so this is only
// a sanity bound).
const MaxKinds = 1024

// VertexFloats is one kit vertex: position (3), normal (3), UV (2) and the
// wind weights (up: 0 at the clump's base to 1 at its top; out: 0 on its
// axis to 1 at its widest; then the kind's bend and flutter).
const VertexFloats = 12

// InstanceWords is one packed instance, in uint32s.
const InstanceWords = 3

// Kit is the meshes a set of fields draws.
type Kit struct {
	Kinds []Kind
}

// KindCasts marks a kind that casts shadows (within the shadow range).
const KindCasts = 1

// Kind is one instanceable clump.
type Kind struct {
	// Name is where it came from (the Poly Haven piece, e.g.
	// grass_medium_01_tall_a_lod0).
	Name string
	// Texture is its albedo, an engine texture name.
	Texture string
	// Height and Radius are its LOD 0 mesh's height above its base and its
	// reach from its vertical axis, metres at scale 1.
	Height, Radius float32
	// Bend and Flutter are its wind (wind_common.glsl's windOffset): how far
	// its top leans in a full gust, as a fraction of its height, and how far
	// a blade tip moves, metres.
	Bend, Flutter float32
	// Range is the fraction of the field's draw range this kind is drawn to
	// (0 or 1: all of it) — short flowers can stop before the grass.
	Range float32
	// Flags are KindCasts and future bits.
	Flags uint32
	// LODs are its meshes, LOD 0 first; at least one.
	LODs []Mesh
}

// Casts reports whether the kind casts shadows.
func (k *Kind) Casts() bool { return k.Flags&KindCasts != 0 }

// Mesh is one LOD: VertexFloats floats per vertex, three indices a triangle.
type Mesh struct {
	Vertices []float32
	Indices  []uint32
}

// Triangles is the mesh's triangle count.
func (m *Mesh) Triangles() int { return len(m.Indices) / 3 }

// VertexCount is the mesh's vertex count.
func (m *Mesh) VertexCount() int { return len(m.Vertices) / VertexFloats }

// Field is one tile's instances.
type Field struct {
	// Kit is the kit's path, resources-relative and slash-separated.
	Kit string
	// Origin is the tile's box's low corner (world metres) and Size its
	// extent: positions are quantised over it.
	Origin, Size [3]float32
	// ScaleMax is the largest scale: a scale byte is s/255·ScaleMax.
	ScaleMax float32
	// Cell is the cells' side (metres); CellsX × CellsZ of them from
	// Origin, row-major in z (cell (cx, cz) is index cz·CellsX + cx).
	Cell           float32
	CellsX, CellsZ int
	// Kinds is the kit's kind count the runs are laid out for.
	Kinds int
	// Runs is (offset, count) per cell per kind: run cell·Kinds + kind.
	Runs []Run
	// CellY is each cell's ground height range (min, max), world metres:
	// the cell's box bottom and, with the tallest kind, its top.
	CellY [][2]float32
	// Data is the packed instances, InstanceWords each, in run order.
	Data []uint32
}

// Run is one (cell, kind)'s instances: Data[Offset·3 : (Offset+Count)·3].
type Run struct {
	Offset, Count uint32
}

// Count is the field's instance count.
func (f *Field) Count() int { return len(f.Data) / InstanceWords }

// Run is cell c's run of kind k.
func (f *Field) Run(c, k int) Run { return f.Runs[c*f.Kinds+k] }

// CellBox is cell c's ground box in XZ (min x, min z, max x, max z).
func (f *Field) CellBox(c int) [4]float32 {
	cx, cz := c%f.CellsX, c/f.CellsX
	x0 := f.Origin[0] + float32(cx)*f.Cell
	z0 := f.Origin[2] + float32(cz)*f.Cell
	return [4]float32{x0, z0, x0 + f.Cell, z0 + f.Cell}
}
