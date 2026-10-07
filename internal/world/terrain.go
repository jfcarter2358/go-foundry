package world

// terrain.go — a chunk's block terrain.
//
// Brushes are convex solids, and building open ground out of them means
// hundreds of hulls: level1's canyon is 734 of them. A terrain is the ground
// built the other way round — a regular 3D grid of blocks in the chunk's own
// space that the author mines out and builds up, one block (or one sphere of
// them) at a time.
//
// # The grid
//
// Origin is the chunk-local position of block (0,0,0)'s minimum corner and
// BlockSize the edge of one cube; there are SizeX*SizeY*SizeZ blocks, stored
// in Blocks (Index). A block is air (0) or solid, and a solid block's value
// is its material: 1..TerrainLayerCount names the texture layer it is made
// of. Everything outside the grid is air, so a terrain is always a closed
// solid.
//
// # Two surfaces, one set of faces
//
// A terrain is drawn, collided with, ray-cast against and navigated over as
// one of two surfaces built from the same blocks (terrain_mesh.go):
//
//   - blocks (Smooth off): every solid block's side that faces air is a flat
//     square — the raw, mineable view;
//   - smooth (Smooth on): a surface net placed by dual contouring over the
//     same grid, one vertex per dual cell, so hills are hills and a
//     one-block step is a slope.
//
// Either way the surface is made of FACES, and a face is named by the solid
// block it belongs to and the direction it looks (FaceKey). In block view a
// face is that square; in smooth view it is the quad of the surface net that
// crosses the same block-to-air edge. That shared name is what lets a biome
// painted on a face in one view stay painted in the other, and what lets a
// ray that hits the smooth surface still say which block it would mine.
//
// # Biomes
//
// Faces are painted with biomes (Paint, keyed by FaceKey), slots into the
// terrain's own palette (Biomes). A biome says what grows on the faces it is
// painted on — see foliage.go.

import (
	"maps"
	"math"
	"slices"

	"github.com/go-gl/mathgl/mgl32"
)

// TerrainLayerCount is how many textures one terrain's blocks can be made
// of. Four is what one RGBA weight per vertex holds.
const TerrainLayerCount = 4

// TerrainMaxSize caps the grid along each axis, and TerrainMaxBlocks its
// volume, so a typo in a level file cannot ask for a grid the size of a
// planet.
const (
	TerrainMaxSize   = 1024
	TerrainMaxBlocks = 32 << 20
)

// TerrainMaxBiomes is how many biome slots one terrain's palette holds; a
// painted face stores its slot in a byte.
const TerrainMaxBiomes = 32

// Face directions, the index a FaceKey carries.
const (
	FaceXPos = iota
	FaceXNeg
	FaceYPos
	FaceYNeg
	FaceZPos
	FaceZNeg
)

// TerrainDirs is each face direction as a block step.
var TerrainDirs = [6][3]int{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}}

// terrainFaceAxes gives, for each direction, the axis it runs along and the
// two in-plane axes (u, v) ordered so that u x v points the way the face
// looks: corners 0, u, u+v, v are then counter-clockwise seen from outside.
var terrainFaceAxes = [6][3]int{
	FaceXPos: {0, 1, 2}, FaceXNeg: {0, 2, 1},
	FaceYPos: {1, 2, 0}, FaceYNeg: {1, 0, 2},
	FaceZPos: {2, 0, 1}, FaceZNeg: {2, 1, 0},
}

// Terrain is a chunk's block terrain. See the file comment.
type Terrain struct {
	// Origin is the chunk-local position of block (0,0,0)'s minimum corner.
	Origin mgl32.Vec3
	// BlockSize is the edge of one block, in chunk units.
	BlockSize float32
	// SizeX, SizeY, SizeZ are the grid's size in blocks.
	SizeX, SizeY, SizeZ int
	// Blocks holds one byte per block, indexed by Index: 0 is air, 1..4 a
	// solid block made of that texture layer.
	Blocks []uint8
	// Smooth draws (and collides, casts and navigates) the surface net
	// instead of the blocks.
	Smooth bool
	// Smoothing is how widely the smooth surface blends the blocks, in
	// blocks: the width a floor's height is averaged over (floorSmooth).
	// Higher rolls gentler hills into one slope. 0 reads as
	// DefaultTerrainSmoothing; see EffectiveSmoothing.
	Smoothing float32
	// Layers names the texture each block material shows. An empty name is
	// an unused layer; a terrain whose layers are all empty is drawn in its
	// normal colour, the same as an untextured brush face.
	Layers [TerrainLayerCount]string
	// TextureScale is how many chunk units one repeat of a layer texture
	// spans. 0 reads as DefaultTerrainTextureScale.
	TextureScale float32
	// Biomes is the terrain's palette of biomes; Paint stores a slot into it,
	// counting from 1.
	Biomes []TerrainBiome
	// Paint maps a face (FaceKey) to the biome slot painted on it, counting
	// from 1. Faces that are not in it are bare.
	Paint map[int32]uint8
	// FoliageSeed varies every placement decision foliage makes, so two
	// terrains painted alike do not grow alike.
	FoliageSeed uint32

	// Generation is runtime-only: bumped on every edit, so caches built from
	// the grid (nav tiles, scatter bakes, shadow signatures) can tell they are
	// stale without hashing it.
	Generation uint64

	// edits remembers where recent edits changed the surface
	// (terrain_edits.go), so a cache over a large area can rebuild only
	// round them. Unexported, like mesh.
	edits terrainEditLog

	// mesh is the derived surface, rebuilt section by section as edits dirty
	// it (terrain_mesh.go). Unexported, so gob leaves it out of compiled
	// levels and a clone starts without one.
	mesh *terrainMeshCache
}

// DefaultTerrainTextureScale is TextureScale's zero value: one repeat per two
// units, which puts a 64x64 texture at the texel density brush faces carry.
const DefaultTerrainTextureScale = 2

// terrainGeneration is shared by every terrain so a clone never repeats a
// generation its source already handed out.
var terrainGeneration uint64

// TerrainGeneration moves whenever any terrain anywhere is edited: the
// terrain counterpart of BrushMeshGeneration, for caches that span the level
// (decals) and would otherwise have to walk every terrain to learn nothing
// changed.
func TerrainGeneration() uint64 { return terrainGeneration }

// NewTerrain builds an all-air terrain of sx by sy by sz blocks whose (0,0,0)
// corner sits at origin.
func NewTerrain(origin mgl32.Vec3, blockSize float32, sx, sy, sz int) *Terrain {
	t := &Terrain{Origin: origin, BlockSize: blockSize, SizeX: sx, SizeY: sy, SizeZ: sz}
	t.Normalize()
	return t
}

// Normalize makes a terrain read from a file safe to use: sizes clamped into
// range, Blocks exactly the length the grid needs, materials in range, and
// paint only on faces that exist and slots that do.
func (t *Terrain) Normalize() {
	if !(t.BlockSize > 0) {
		t.BlockSize = 1
	}
	t.SizeX = clampInt(t.SizeX, 1, TerrainMaxSize)
	t.SizeY = clampInt(t.SizeY, 1, TerrainMaxSize)
	t.SizeZ = clampInt(t.SizeZ, 1, TerrainMaxSize)
	for t.SizeX*t.SizeY*t.SizeZ > TerrainMaxBlocks {
		t.SizeY = max(1, t.SizeY/2)
		if t.SizeX*t.SizeY*t.SizeZ > TerrainMaxBlocks && t.SizeY == 1 {
			t.SizeX, t.SizeZ = max(1, t.SizeX/2), max(1, t.SizeZ/2)
		}
	}
	if len(t.Blocks) != t.BlockCount() {
		b := make([]uint8, t.BlockCount())
		copy(b, t.Blocks)
		t.Blocks = b
	}
	for i, v := range t.Blocks {
		if int(v) > TerrainLayerCount {
			t.Blocks[i] = TerrainLayerCount
		}
	}
	if len(t.Biomes) > TerrainMaxBiomes {
		t.Biomes = t.Biomes[:TerrainMaxBiomes]
	}
	for k, slot := range t.Paint {
		if slot == 0 || int(slot) > len(t.Biomes) || !t.FaceExists(k) {
			delete(t.Paint, k)
		}
	}
	t.Touch()
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Touch marks the whole terrain edited. Anything that writes the exported
// fields directly (rather than through SetBlock, SetPaint and the like) must
// call it, or the surface keeps showing the old grid.
func (t *Terrain) Touch() {
	terrainGeneration++
	t.Generation = terrainGeneration
	if t.mesh != nil {
		t.mesh.dirtyAll = true
	}
	t.forgetEdits()
}

// TouchBlocks marks blocks [x0,x1] x [y0,y1] x [z0,z1] edited — cheaper than
// Touch, since only the sections of surface around them are rebuilt.
func (t *Terrain) TouchBlocks(x0, y0, z0, x1, y1, z1 int) {
	t.touch(x0, y0, z0, x1, y1, z1, true)
}

// TouchPaint marks the paint on blocks [x0,x1]... edited: their faces' looks
// and what grows on them, not their shape, so less of the surface rebuilds.
func (t *Terrain) TouchPaint(x0, y0, z0, x1, y1, z1 int) {
	t.touch(x0, y0, z0, x1, y1, z1, false)
}

func (t *Terrain) touch(x0, y0, z0, x1, y1, z1 int, shape bool) {
	terrainGeneration++
	t.Generation = terrainGeneration
	if t.mesh != nil {
		t.mesh.markDirty(t, x0, y0, z0, x1, y1, z1, shape)
	}
	// The same reach markDirty rebuilds for (SurfaceChangedSince).
	reach := 2
	if shape {
		reach = t.smoothReach()
	}
	t.recordEdit(x0, y0, z0, x1, y1, z1, reach)
}

// Clone returns an independent deep copy.
func (t *Terrain) Clone() *Terrain {
	if t == nil {
		return nil
	}
	cp := *t
	cp.mesh = nil
	cp.Blocks = append([]uint8(nil), t.Blocks...)
	cp.Biomes = append([]TerrainBiome(nil), t.Biomes...)
	for i := range cp.Biomes {
		cp.Biomes[i].Amounts = maps.Clone(cp.Biomes[i].Amounts)
	}
	if t.Paint != nil {
		cp.Paint = make(map[int32]uint8, len(t.Paint))
		for k, v := range t.Paint {
			cp.Paint[k] = v
		}
	}
	cp.Touch()
	return &cp
}

// BlockCount is Blocks' length.
func (t *Terrain) BlockCount() int { return t.SizeX * t.SizeY * t.SizeZ }

// In reports whether block (x, y, z) is inside the grid.
func (t *Terrain) In(x, y, z int) bool {
	return x >= 0 && y >= 0 && z >= 0 && x < t.SizeX && y < t.SizeY && z < t.SizeZ
}

// Index is block (x, y, z)'s position in Blocks. The block must be In.
func (t *Terrain) Index(x, y, z int) int { return (z*t.SizeY+y)*t.SizeX + x }

// BlockCoords is Index's inverse.
func (t *Terrain) BlockCoords(i int) (x, y, z int) {
	x = i % t.SizeX
	i /= t.SizeX
	return x, i % t.SizeY, i / t.SizeY
}

// Block is block (x, y, z)'s value: 0 for air (and for anywhere outside the
// grid), else its material.
func (t *Terrain) Block(x, y, z int) uint8 {
	if !t.In(x, y, z) {
		return 0
	}
	return t.Blocks[t.Index(x, y, z)]
}

// Solid reports whether block (x, y, z) is solid.
func (t *Terrain) Solid(x, y, z int) bool { return t.Block(x, y, z) != 0 }

// SetBlock sets block (x, y, z) to v (0 = air, else a material), and reports
// whether anything changed. Faces that stop existing lose their paint: a
// block mined away takes the grass on it with it, and a block placed against
// a painted wall covers that wall's moss.
func (t *Terrain) SetBlock(x, y, z int, v uint8) bool {
	if !t.In(x, y, z) {
		return false
	}
	if int(v) > TerrainLayerCount {
		v = TerrainLayerCount
	}
	i := t.Index(x, y, z)
	old := t.Blocks[i]
	if old == v {
		return false
	}
	t.Blocks[i] = v
	if len(t.Paint) > 0 {
		if v == 0 {
			for d := 0; d < 6; d++ {
				delete(t.Paint, int32(i*6+d))
			}
		} else if old == 0 {
			for d := 0; d < 6; d++ {
				s := TerrainDirs[d]
				nx, ny, nz := x+s[0], y+s[1], z+s[2]
				if t.In(nx, ny, nz) {
					delete(t.Paint, t.FaceKey(nx, ny, nz, d^1))
				}
			}
			// The new block's own faces start bare.
			for d := 0; d < 6; d++ {
				delete(t.Paint, int32(i*6+d))
			}
		}
	}
	t.TouchBlocks(x, y, z, x, y, z)
	return true
}

// FaceKey names the face of block (x, y, z) looking along direction dir.
// The block must be In. d^1 is always the opposite direction.
func (t *Terrain) FaceKey(x, y, z, dir int) int32 {
	return int32(t.Index(x, y, z)*6 + dir)
}

// FaceBlock is FaceKey's inverse.
func (t *Terrain) FaceBlock(key int32) (x, y, z, dir int) {
	x, y, z = t.BlockCoords(int(key) / 6)
	return x, y, z, int(key) % 6
}

// FaceExists reports whether key is a face of the surface: its block is
// solid and the block it looks at is air.
func (t *Terrain) FaceExists(key int32) bool {
	if key < 0 || int(key)/6 >= t.BlockCount() {
		return false
	}
	x, y, z, d := t.FaceBlock(key)
	if !t.Solid(x, y, z) {
		return false
	}
	s := TerrainDirs[d]
	return !t.Solid(x+s[0], y+s[1], z+s[2])
}

// PaintOf is the biome slot painted on face key, counting from 1, or 0.
func (t *Terrain) PaintOf(key int32) uint8 { return t.Paint[key] }

// SetPaint paints biome slot (counting from 1; 0 clears) onto face key, and
// reports whether anything changed. Faces that do not exist are left alone.
func (t *Terrain) SetPaint(key int32, slot uint8) bool {
	if int(slot) > len(t.Biomes) || (slot != 0 && !t.FaceExists(key)) {
		return false
	}
	if t.Paint[key] == slot {
		return false
	}
	if slot == 0 {
		delete(t.Paint, key)
	} else {
		if t.Paint == nil {
			t.Paint = map[int32]uint8{}
		}
		t.Paint[key] = slot
	}
	x, y, z, _ := t.FaceBlock(key)
	t.TouchPaint(x, y, z, x, y, z)
	return true
}

// TerrainGroundChannels is how many distinct biome ground textures one
// terrain can show; a face corner carries one weight byte for each.
const TerrainGroundChannels = 8

// GroundTextures is the distinct ground textures of the biomes in the
// palette, in palette order: channel i of a face's Ground weights shows
// GroundTextures()[i]. A slot with its own GroundLayer shows that layer
// instead and takes no channel; past TerrainGroundChannels textures, the
// rest are left to their tint.
func (t *Terrain) GroundTextures() []string {
	var out []string
	for i := range t.Biomes {
		b := &t.Biomes[i]
		if b.GroundLayer > 0 {
			continue
		}
		tex := b.Def().GroundTexture
		if tex == "" || slices.Contains(out, tex) {
			continue
		}
		if len(out) == TerrainGroundChannels {
			break
		}
		out = append(out, tex)
	}
	return out
}

// groundChannels is each palette slot's ground channel, counting slots from
// 1 as Paint does (index 0 is bare), -1 for none.
func (t *Terrain) groundChannels() []int8 {
	tex := t.GroundTextures()
	out := make([]int8, len(t.Biomes)+1)
	out[0] = -1
	for i := range t.Biomes {
		out[i+1] = -1
		b := &t.Biomes[i]
		if b.GroundLayer > 0 {
			continue
		}
		if c := slices.Index(tex, b.Def().GroundTexture); c >= 0 && b.Def().GroundTexture != "" {
			out[i+1] = int8(c)
		}
	}
	return out
}

// BiomeAt is the biome painted on face key, or nil.
func (t *Terrain) BiomeAt(key int32) *TerrainBiome {
	s := t.Paint[key]
	if s == 0 || int(s) > len(t.Biomes) {
		return nil
	}
	return &t.Biomes[s-1]
}

// RemoveBiome deletes palette slot (counting from 1) and every face painted
// with it; later slots move down one.
func (t *Terrain) RemoveBiome(slot int) {
	if slot < 1 || slot > len(t.Biomes) {
		return
	}
	t.Biomes = append(t.Biomes[:slot-1], t.Biomes[slot:]...)
	for k, s := range t.Paint {
		switch {
		case int(s) == slot:
			delete(t.Paint, k)
		case int(s) > slot:
			t.Paint[k] = s - 1
		}
	}
	t.Touch()
}

// EffectiveTextureScale is TextureScale with its zero value read as the
// default.
func (t *Terrain) EffectiveTextureScale() float32 {
	if t.TextureScale <= 0 {
		return DefaultTerrainTextureScale
	}
	return t.TextureScale
}

// HasTexture reports whether any layer names a texture.
func (t *Terrain) HasTexture() bool {
	for _, l := range t.Layers {
		if l != "" {
			return true
		}
	}
	return false
}

// BlockMin is block (x, y, z)'s minimum corner in chunk space.
func (t *Terrain) BlockMin(x, y, z int) mgl32.Vec3 {
	s := t.BlockSize
	return t.Origin.Add(mgl32.Vec3{float32(x) * s, float32(y) * s, float32(z) * s})
}

// BlockAt is the block containing chunk-space point p, which may be outside
// the grid.
func (t *Terrain) BlockAt(p mgl32.Vec3) (x, y, z int) {
	g := p.Sub(t.Origin).Mul(1 / t.BlockSize)
	return int(math.Floor(float64(g[0]))), int(math.Floor(float64(g[1]))), int(math.Floor(float64(g[2])))
}

// LocalBounds is the chunk-space box every surface the terrain can build
// fits inside: the grid, grown by half a block because the smooth surface's
// outermost vertices sit that far out.
func (t *Terrain) LocalBounds() (lo, hi mgl32.Vec3) {
	h := t.BlockSize / 2
	lo = t.Origin.Sub(mgl32.Vec3{h, h, h})
	hi = t.Origin.Add(mgl32.Vec3{float32(t.SizeX)*t.BlockSize + h, float32(t.SizeY)*t.BlockSize + h, float32(t.SizeZ)*t.BlockSize + h})
	return lo, hi
}

// Resize changes the grid to sx by sy by sz blocks, keeping its (0,0,0)
// corner, block size and every block (and its paint) still inside it.
func (t *Terrain) Resize(sx, sy, sz int) {
	sx = clampInt(sx, 1, TerrainMaxSize)
	sy = clampInt(sy, 1, TerrainMaxSize)
	sz = clampInt(sz, 1, TerrainMaxSize)
	if sx == t.SizeX && sy == t.SizeY && sz == t.SizeZ {
		return
	}
	old := *t
	t.SizeX, t.SizeY, t.SizeZ = sx, sy, sz
	t.Blocks = make([]uint8, t.BlockCount())
	for z := 0; z < min(sz, old.SizeZ); z++ {
		for y := 0; y < min(sy, old.SizeY); y++ {
			for x := 0; x < min(sx, old.SizeX); x++ {
				t.Blocks[t.Index(x, y, z)] = old.Blocks[old.Index(x, y, z)]
			}
		}
	}
	if old.Paint != nil {
		t.Paint = map[int32]uint8{}
		for k, s := range old.Paint {
			x, y, z, d := old.FaceBlock(k)
			if t.In(x, y, z) {
				if nk := t.FaceKey(x, y, z, d); t.FaceExists(nk) {
					t.Paint[nk] = s
				}
			}
		}
	}
	t.mesh = nil
	t.Touch()
}

// GrowToInclude widens the grid so blocks lo..hi (inclusive, in the current
// grid's coordinates, which may be negative or past the far sides) lie inside
// it. Every block and its paint keeps its place in the chunk: growing towards
// -X, -Y or -Z moves Origin back by as many blocks, and shift reports how far
// block coordinates moved (the block that was (x, y, z) is now (x+shift[0],
// ...)). ok is false, and nothing changes, when the grown grid would pass
// TerrainMaxSize or TerrainMaxBlocks.
func (t *Terrain) GrowToInclude(lo, hi [3]int) (shift [3]int, ok bool) {
	size := [3]int{t.SizeX, t.SizeY, t.SizeZ}
	var ns [3]int
	for a := 0; a < 3; a++ {
		nlo, nhi := min(0, lo[a]), max(size[a]-1, hi[a])
		shift[a] = -nlo
		ns[a] = nhi - nlo + 1
		if ns[a] > TerrainMaxSize {
			return [3]int{}, false
		}
	}
	if ns[0]*ns[1]*ns[2] > TerrainMaxBlocks {
		return [3]int{}, false
	}
	if ns == size {
		return shift, true
	}
	old := *t
	t.SizeX, t.SizeY, t.SizeZ = ns[0], ns[1], ns[2]
	t.Blocks = make([]uint8, t.BlockCount())
	for z := 0; z < old.SizeZ; z++ {
		for y := 0; y < old.SizeY; y++ {
			src := old.Index(0, y, z)
			dst := t.Index(shift[0], y+shift[1], z+shift[2])
			copy(t.Blocks[dst:dst+old.SizeX], old.Blocks[src:src+old.SizeX])
		}
	}
	if old.Paint != nil {
		t.Paint = make(map[int32]uint8, len(old.Paint))
		for k, s := range old.Paint {
			x, y, z, d := old.FaceBlock(k)
			t.Paint[t.FaceKey(x+shift[0], y+shift[1], z+shift[2], d)] = s
		}
	}
	t.Origin = t.Origin.Sub(mgl32.Vec3{float32(shift[0]), float32(shift[1]), float32(shift[2])}.Mul(t.BlockSize))
	t.mesh = nil
	t.Touch()
	return shift, true
}

// FillBox sets every block in lo..hi (inclusive, clipped to the grid) to v
// (0 = air) and reports whether anything changed. The paint follows
// SetBlock's rules — faces that stop existing lose theirs — but the surface
// around the box is marked for rebuilding once rather than once per block.
func (t *Terrain) FillBox(lo, hi [3]int, v uint8) bool {
	if int(v) > TerrainLayerCount {
		v = TerrainLayerCount
	}
	x0, y0, z0 := max(lo[0], 0), max(lo[1], 0), max(lo[2], 0)
	x1, y1, z1 := min(hi[0], t.SizeX-1), min(hi[1], t.SizeY-1), min(hi[2], t.SizeZ-1)
	if x0 > x1 || y0 > y1 || z0 > z1 {
		return false
	}
	changed := false
	for z := z0; z <= z1; z++ {
		for y := y0; y <= y1; y++ {
			for x := x0; x <= x1; x++ {
				i := t.Index(x, y, z)
				old := t.Blocks[i]
				if old == v {
					continue
				}
				t.Blocks[i] = v
				changed = true
				if old == 0 && len(t.Paint) > 0 {
					// A block that was air starts bare, as SetBlock's does.
					for d := 0; d < 6; d++ {
						delete(t.Paint, int32(i*6+d))
					}
				}
			}
		}
	}
	if !changed {
		return false
	}
	// Faces in the box, or looking into it from a neighbour, may have been
	// covered or mined away.
	for k := range t.Paint {
		x, y, z, _ := t.FaceBlock(k)
		if x >= x0-1 && x <= x1+1 && y >= y0-1 && y <= y1+1 && z >= z0-1 && z <= z1+1 && !t.FaceExists(k) {
			delete(t.Paint, k)
		}
	}
	t.TouchBlocks(x0, y0, z0, x1, y1, z1)
	return true
}

// TerrainFill describes how Generate fills a grid.
type TerrainFill struct {
	// Height is the ground's mean height, in blocks.
	Height int
	// Hills is how far the ground rises and falls around Height, in blocks.
	// 0 is flat.
	Hills float32
	// HillSize is the typical width of a hill, in blocks.
	HillSize float32
	// Seed picks the hills.
	Seed uint32
	// Top, Under and Deep are the materials (1..4) of the top block, the few
	// blocks under it and everything below those.
	Top, Under, Deep uint8
	// UnderDepth is how many blocks of Under lie beneath the top.
	UnderDepth int
	// Biome, when non-zero, is painted on every top face (a biome slot,
	// counting from 1).
	Biome uint8
}

// Generate replaces every block with ground shaped by f: a heightmap of
// layered value noise, with a top material, a shallow under-layer and deep
// rock, the way ground actually is. Steep hillsides show their under-layer
// because the top material only covers the column's top block.
func (t *Terrain) Generate(f TerrainFill) {
	for i := range t.Blocks {
		t.Blocks[i] = 0
	}
	t.Paint = nil
	if f.HillSize <= 0 {
		f.HillSize = 24
	}
	mat := func(m uint8) uint8 {
		if m == 0 || int(m) > TerrainLayerCount {
			return 1
		}
		return m
	}
	top, under, deep := mat(f.Top), mat(f.Under), mat(f.Deep)
	for z := 0; z < t.SizeZ; z++ {
		for x := 0; x < t.SizeX; x++ {
			h := float32(f.Height)
			if f.Hills > 0 {
				p := mgl32.Vec2{float32(x), float32(z)}.Mul(1 / f.HillSize)
				// Ridged-ish: sharpen the noise's upper half so hills have
				// crests and the valleys between them stay broad.
				n := FBM2(p, f.Seed, 4)*2 - 1
				if n > 0 {
					n = n * (1 + 0.5*n)
				}
				h += n * f.Hills
			}
			ground := clampInt(int(math.Round(float64(h))), 1, t.SizeY)
			for y := 0; y < ground; y++ {
				m := deep
				switch {
				case y == ground-1:
					m = top
				case y >= ground-1-f.UnderDepth:
					m = under
				}
				t.Blocks[t.Index(x, y, z)] = m
			}
			if f.Biome != 0 && int(f.Biome) <= len(t.Biomes) {
				if t.Paint == nil {
					t.Paint = map[int32]uint8{}
				}
				t.Paint[t.FaceKey(x, ground-1, z, FaceYPos)] = f.Biome
			}
		}
	}
	t.Touch()
}

// SolidRange reports the lowest and highest solid block Y over the whole
// grid, and whether there is any solid block at all.
func (t *Terrain) SolidRange() (lo, hi int, ok bool) {
	lo, hi = t.SizeY, -1
	for i, v := range t.Blocks {
		if v == 0 {
			continue
		}
		_, y, _ := t.BlockCoords(i)
		lo, hi = min(lo, y), max(hi, y)
	}
	return lo, hi, hi >= 0
}
