package world

// terrain_mesh.go — the surface a terrain's blocks make.
//
// # Sections
//
// The surface is built in sections of TerrainSectionSize^3 blocks. An edit
// dirties the sections around the blocks it changed (TouchBlocks) and only
// those are rebuilt, the next time anything asks for the surface
// (Terrain.Sections) — which is always the main thread: the renderer, a
// collision tick, the nav gather, the editor's picker. A section is rebuilt
// whole, so there is no seam bookkeeping: every quantity a face needs is a
// pure function of the blocks within a few of it, and a face near a section
// border comes out identically whichever side builds it.
//
// # Faces
//
// Both surfaces are lists of TerrainFace: four corners, counter-clockwise
// seen from the air side, each with a normal, layer weights and a biome
// cover colour. A face is owned by the section of the solid block it belongs
// to.
//
// Block view: the corners are the block's side.
//
// Smooth view: a surface net placed by dual contouring. The grid's samples
// are block centres (air outside the grid); every dual cell — the cube
// between eight neighbouring centres — that has both solid and air corners
// gets one vertex, and every solid-to-air edge between two centres becomes
// the quad joining the four dual cells around it. That edge is exactly a
// block face, which is why the two views share FaceKeys.
//
// Where a dual cell's vertex goes is decided in two steps.
//
// First, dual contouring: the blocks are blurred into a smooth field (held to
// the blocks' own side of one half, so a tunnel narrower than the blur stays
// open), each of the cell's solid-to-air edges contributes a plane where
// that field crosses one half, and the vertex is the point closest to all of
// them (the QEF minimiser), clamped into its cell.
//
// Then relaxation (placeVertices): the blocks only say solid or air, so a
// gentle hill is steps several blocks apart, and anything read off them
// locally leaves a shelf between steps — a smoothed hill is still terraced.
// Each vertex is moved, round after round, toward the average of its
// neighbours but never out of its own cell, and settles into the ramp
// through the steps' corners (a constrained elastic surface net).
// Terrain.Smoothing sets how many rounds. The normals are the relaxed
// surface's own.

import (
	"math"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/go-gl/mathgl/mgl32"
)

// TerrainSectionSize is a section's edge, in blocks: the unit of rebuild,
// culling and foliage generation.
const TerrainSectionSize = 16

// terrainBrick is the ray-query bin's edge, in blocks. A section's bins
// reach one brick past its own blocks each way, because a smooth face's
// vertices sit up to half a block outside the blocks that own it.
const (
	terrainBrick     = 2
	terrainBricksSec = TerrainSectionSize / terrainBrick
	terrainBrickSpan = terrainBricksSec + 2
)

// terrainQEFBias pulls a dual-contoured vertex toward the centroid of its
// crossings. Higher is smoother and rounder; lower keeps ridges and cliff
// lips sharper.
const terrainQEFBias = 0.12

// TerrainFace is one face of a terrain's surface. See the file comment.
type TerrainFace struct {
	// Key names the face (Terrain.FaceKey).
	Key int32
	// Corners are counter-clockwise seen from the air side, in chunk space.
	Corners [4]mgl32.Vec3
	// Normals are each corner's shading normal, pointing into the air.
	Normals [4]mgl32.Vec3
	// Weights are each corner's four layer weights, one byte each (layer 0
	// in the low byte), summing to 255.
	Weights [4]uint32
	// Cover is each corner's biome ground tint, RGBA bytes (R in the low
	// byte); A is how much of the tint the ground takes.
	Cover [4]uint32
	// Ground is each corner's weight on the terrain's ground channels
	// (Terrain.GroundTextures), one byte per channel with channel 0 in the
	// low byte: how much of the ground round the corner is painted with the
	// biome whose texture that channel carries. Zero is the block's own
	// layers; the edge of a painted patch is where it falls off.
	Ground [4]uint64
	// AltSplit splits the quad along corners 1-3 instead of 0-2.
	AltSplit bool
}

// Tri is the corner indices of the face's triangle k (0 or 1), wound like
// the face.
func (f *TerrainFace) Tri(k int) [3]int {
	if f.AltSplit {
		if k == 0 {
			return [3]int{0, 1, 3}
		}
		return [3]int{1, 2, 3}
	}
	if k == 0 {
		return [3]int{0, 1, 2}
	}
	return [3]int{0, 2, 3}
}

// Triangle is the face's triangle k in chunk space.
func (f *TerrainFace) Triangle(k int) [3]mgl32.Vec3 {
	c := f.Tri(k)
	return [3]mgl32.Vec3{f.Corners[c[0]], f.Corners[c[1]], f.Corners[c[2]]}
}

// PointAt is the point at (u, v) in [0,1]^2 across the face, bilinear over
// its corners, and the shading normal there.
func (f *TerrainFace) PointAt(u, v float32) (p, n mgl32.Vec3) {
	w := [4]float32{(1 - u) * (1 - v), u * (1 - v), u * v, (1 - u) * v}
	for i := 0; i < 4; i++ {
		p = p.Add(f.Corners[i].Mul(w[i]))
		n = n.Add(f.Normals[i].Mul(w[i]))
	}
	if l := n.Len(); l > 1e-9 {
		n = n.Mul(1 / l)
	}
	return p, n
}

// TerrainSection is one section of a terrain's surface.
type TerrainSection struct {
	// X, Y, Z are the section's coordinates; it holds the faces of blocks
	// [X*16, X*16+16) and so on.
	X, Y, Z int
	Faces   []TerrainFace
	// Lo, Hi are the chunk-space box around every face; Lo > Hi when there
	// are none.
	Lo, Hi mgl32.Vec3
	// Gen changes every time the faces are rebuilt, and FoliageGen every time
	// anything the section's foliage depends on changes (which reaches
	// further than the faces do: a block placed overhead shades the grass).
	// Both are unique across every terrain, so a cache keyed on them never
	// mistakes one section for another.
	Gen, FoliageGen uint64

	// Ray-query bins: brickStart[b]..brickStart[b+1] index brickTris, each a
	// face index*2 + triangle. Bricks are terrainBrick blocks on a side,
	// terrainBrickSpan of them each way starting one brick below the
	// section's own.
	brickStart []int32
	brickTris  []int32
	faceIndex  map[int32]int32
}

// FaceByKey returns the section's face named key, if it owns one.
func (s *TerrainSection) FaceByKey(key int32) (*TerrainFace, bool) {
	i, ok := s.faceIndex[key]
	if !ok {
		return nil, false
	}
	return &s.Faces[i], true
}

// Empty reports whether the section has no surface.
func (s *TerrainSection) Empty() bool { return len(s.Faces) == 0 }

type terrainMeshCache struct {
	sizeX, sizeY, sizeZ int
	blockSize           float32
	origin              mgl32.Vec3
	smooth              bool
	smoothing           float32
	layers              [TerrainLayerCount]string

	nx, ny, nz int
	sections   []*TerrainSection
	dirty      []bool
	anyDirty   bool
	dirtyAll   bool

	// surfaceRange's cache.
	topGen         uint64
	topLo, topSpan float32
}

// terrainUID hands out the section generations.
var terrainUID uint64

func nextTerrainUID() uint64 {
	terrainUID++
	return terrainUID
}

func (c *terrainMeshCache) matches(t *Terrain) bool {
	return c.sizeX == t.SizeX && c.sizeY == t.SizeY && c.sizeZ == t.SizeZ &&
		c.blockSize == t.BlockSize && c.origin == t.Origin && c.smooth == t.Smooth &&
		c.smoothing == t.EffectiveSmoothing()
}

func newTerrainMeshCache(t *Terrain) *terrainMeshCache {
	c := &terrainMeshCache{
		sizeX: t.SizeX, sizeY: t.SizeY, sizeZ: t.SizeZ,
		blockSize: t.BlockSize, origin: t.Origin, smooth: t.Smooth,
		smoothing: t.EffectiveSmoothing(),
	}
	c.nx = (t.SizeX + TerrainSectionSize - 1) / TerrainSectionSize
	c.ny = (t.SizeY + TerrainSectionSize - 1) / TerrainSectionSize
	c.nz = (t.SizeZ + TerrainSectionSize - 1) / TerrainSectionSize
	c.sections = make([]*TerrainSection, c.nx*c.ny*c.nz)
	c.dirty = make([]bool, len(c.sections))
	for z := 0; z < c.nz; z++ {
		for y := 0; y < c.ny; y++ {
			for x := 0; x < c.nx; x++ {
				c.sections[c.secIndex(x, y, z)] = &TerrainSection{X: x, Y: y, Z: z}
			}
		}
	}
	c.dirtyAll = true
	return c
}

func (c *terrainMeshCache) secIndex(x, y, z int) int { return (z*c.ny+y)*c.nx + x }

// markDirty marks the sections whose faces the blocks [x0..x1]... can
// change, and the wider set whose foliage they can.
func (c *terrainMeshCache) markDirty(t *Terrain, x0, y0, z0, x1, y1, z1 int, shape bool) {
	if c.dirtyAll {
		return
	}
	// A change of shape reaches as far as the smoothing does; a change of
	// paint only the faces painted and those sharing their vertices — a
	// vertex blends the faces of its dual cell, and a face's corners are a
	// cell further out.
	reach := 2
	if shape {
		reach = t.smoothReach()
	}
	sec := func(v, n int) int { return clampInt(v/TerrainSectionSize, 0, n-1) }
	sx0, sx1 := sec(max(x0-reach, 0), c.nx), sec(x1+reach, c.nx)
	sy0, sy1 := sec(max(y0-reach, 0), c.ny), sec(y1+reach, c.ny)
	sz0, sz1 := sec(max(z0-reach, 0), c.nz), sec(z1+reach, c.nz)
	for z := sz0; z <= sz1; z++ {
		for y := sy0; y <= sy1; y++ {
			for x := sx0; x <= sx1; x++ {
				c.dirty[c.secIndex(x, y, z)] = true
			}
		}
	}
	c.anyDirty = true
	// Foliage reads further: how much sky a face sees (anything above it)
	// and how its neighbours are painted and shaped (a few blocks around).
	freach := max(terrainFoliageReach, reach)
	fx0, fx1 := sec(max(x0-freach, 0), c.nx), sec(x1+freach, c.nx)
	fz0, fz1 := sec(max(z0-freach, 0), c.nz), sec(z1+freach, c.nz)
	fy1 := sec(y1+freach, c.ny)
	for z := fz0; z <= fz1; z++ {
		for y := 0; y <= fy1; y++ {
			for x := fx0; x <= fx1; x++ {
				c.sections[c.secIndex(x, y, z)].FoliageGen = nextTerrainUID()
			}
		}
	}
}

// Sections returns the terrain's surface, rebuilding whatever edits have
// dirtied since it was last asked for. Main thread only. The returned
// sections are the terrain's own; callers must not change them.
func (t *Terrain) Sections() []*TerrainSection {
	return t.ensureMesh().sections
}

func (t *Terrain) ensureMesh() *terrainMeshCache {
	c := t.mesh
	if c == nil || !c.matches(t) {
		c = newTerrainMeshCache(t)
		t.mesh = c
	}
	if c.layers != t.Layers {
		c.layers = t.Layers
	}
	if !c.dirtyAll && !c.anyDirty {
		return c
	}
	var todo []*TerrainSection
	for i, s := range c.sections {
		if c.dirtyAll || c.dirty[i] {
			todo = append(todo, s)
			c.dirty[i] = false
		}
	}
	// Sections are independent of each other — each reads only the blocks,
	// which cannot change while this (main-thread) call is running — so a
	// rebuild of several fans out, a builder and its scratch per worker.
	// A smooth rebuild shares its field and dual cells between neighbouring
	// sections instead (terrain_mesh_union.go).
	workers := min(len(todo), runtime.NumCPU())
	if t.Smooth && terrainUnionBuild {
		buildSmoothUnion(t, todo)
	} else if workers <= 1 {
		b := newTerrainBuilder(t)
		for _, s := range todo {
			b.build(s)
		}
	} else {
		var next atomic.Int64
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				b := newTerrainBuilder(t)
				for {
					i := int(next.Add(1)) - 1
					if i >= len(todo) {
						return
					}
					b.build(todo[i])
				}
			}()
		}
		wg.Wait()
	}
	for _, s := range todo {
		s.Gen = nextTerrainUID()
		if c.dirtyAll {
			s.FoliageGen = nextTerrainUID()
		}
	}
	c.dirtyAll, c.anyDirty = false, false
	return c
}

// SectionAt is the section holding block (x, y, z)'s faces, or nil outside
// the grid.
func (t *Terrain) SectionAt(x, y, z int) *TerrainSection {
	if !t.In(x, y, z) {
		return nil
	}
	c := t.ensureMesh()
	return c.sections[c.secIndex(x/TerrainSectionSize, y/TerrainSectionSize, z/TerrainSectionSize)]
}

// Face returns the surface's face named key, if the surface has one.
func (t *Terrain) Face(key int32) (*TerrainFace, bool) {
	if key < 0 || int(key)/6 >= t.BlockCount() {
		return nil, false
	}
	x, y, z, _ := t.FaceBlock(key)
	s := t.SectionAt(x, y, z)
	if s == nil {
		return nil, false
	}
	return s.FaceByKey(key)
}

// ---------- building ----------

// terrainBuilder holds the scratch one rebuild pass shares between
// sections.
type terrainBuilder struct {
	t *Terrain
	// workers is how many goroutines each stage of a smooth build splits
	// across (par); 1 builds serially, as a builder per section does.
	workers int
	// Smooth scratch, for the section being built: occupancy and its blur
	// over the section grown by pad blocks each way.
	pad    int
	halo   int
	iters  int
	kernel []float32
	// relaxation scratch: positions, double-buffered; the surface cells;
	// each one's six neighbours it relaxes toward (-1 for none).
	pa, pb []mgl32.Vec3
	surf   []int32
	nbr    []int32
	// floorSmooth's per-column floor lists, and its weight and height-reach
	// tables by column offset.
	floors [][]int16
	fw     []float32
	fd     []int
	r0     [3]int
	rn     [3]int
	occ    []float32
	blur   []float32
	tmp    []float32
	// fld is the blur held to the blocks' side of one half (prepareSmooth):
	// the field the surface is placed on and shaded by.
	fld []float32
	cells  []terrainDualVertex
	c0, cn [3]int
	// groundOf is each palette slot's ground channel (index into
	// Terrain.GroundTextures), -1 for none; groundOf[0] is bare ground.
	groundOf []int8
}

// DefaultTerrainSmoothing is Smoothing's zero value, in blocks: the width of
// the Gaussian a floor's height is averaged over (floorSmooth). Wide enough
// that a hill stepping up one block every eight reads as one slope.
const DefaultTerrainSmoothing = 3

// TerrainSmoothingMin and TerrainSmoothingMax bound Smoothing.
const (
	TerrainSmoothingMin = 0.5
	TerrainSmoothingMax = 8
)

// EffectiveSmoothing is Smoothing with its zero value read as the default,
// clamped into range.
func (t *Terrain) EffectiveSmoothing() float32 {
	if t.Smoothing <= 0 {
		return DefaultTerrainSmoothing
	}
	return min(max(t.Smoothing, TerrainSmoothingMin), TerrainSmoothingMax)
}

// terrainBlurSigma is the width of the Gaussian the smooth field is blurred
// with, in blocks, and terrainBlurRadius how far its kernel reaches. The blur
// only has to give the vertices a sensible start and the edges sensible
// crossings; taking the terraces out is the relaxation's job.
const (
	terrainBlurSigma  = 1.0
	terrainBlurRadius = 2
)

// terrainRelaxIterations is how many rounds of constrained relaxation the
// smooth surface gets after its floors are levelled: enough to blend a
// floor into the wall rising from it, not the tool for long slopes (that is
// floorSmooth).
const terrainRelaxIterations = 4

// smoothHalo is how many dual cells past a section's own the relaxation
// runs over, so that the section's vertices come out exactly as they would
// relaxed over the whole terrain: each round reaches one cell further.
func (t *Terrain) smoothHalo() int { return terrainRelaxIterations + 1 }

// floorRadius is how many columns the floor averaging reaches.
func (t *Terrain) floorRadius() int {
	return int(math.Ceil(float64(2 * t.EffectiveSmoothing())))
}

// smoothReach is how far around an edited block the surface can change.
func (t *Terrain) smoothReach() int {
	if !t.Smooth {
		return 1
	}
	return max(terrainBlurRadius+4, t.floorRadius()+2) + t.smoothHalo()
}

type terrainDualVertex struct {
	done bool
	// surf is whether the cell has both solid and air corners, and so a
	// vertex at all.
	surf    bool
	pos     mgl32.Vec3
	nrm     mgl32.Vec3
	weights uint32
	cover   uint32
	ground  uint64
}

func newTerrainBuilder(t *Terrain) *terrainBuilder {
	b := &terrainBuilder{t: t, groundOf: t.groundChannels()}
	if t.Smooth {
		r := terrainBlurRadius
		sigma := float64(terrainBlurSigma)
		sum := float32(0)
		for k := -r; k <= r; k++ {
			w := float32(math.Exp(-float64(k*k) / (2 * sigma * sigma)))
			b.kernel = append(b.kernel, w)
			sum += w
		}
		for i := range b.kernel {
			b.kernel[i] /= sum
		}
		// Everything read is within 3 blocks of the cells relaxed (dual cells,
		// the gradient, the Newton steps), which reach the halo past the
		// section; the blur needs its radius past that.
		b.halo = t.smoothHalo()
		b.iters = terrainRelaxIterations
		b.pad = r + 4 + b.halo
	}
	return b
}

func (b *terrainBuilder) build(s *TerrainSection) {
	t := b.t
	x0, y0, z0, x1, y1, z1 := t.sectionBlocks(s)
	if t.Smooth {
		// The field, the vertices and their relaxation are most of a smooth
		// build, and most sections of a terrain — solid rock under the
		// ground, open air over it — have no surface to place them for.
		if !b.hasFace(x0, y0, z0, x1, y1, z1) {
			s.Faces = s.Faces[:0]
			b.finish(s)
			return
		}
		b.prepareSmooth(x0, y0, z0, x1, y1, z1)
	}
	b.emit(s)
}

// sectionBlocks is the block box [x0,x1) x [y0,y1) x [z0,z1) section s owns.
func (t *Terrain) sectionBlocks(s *TerrainSection) (x0, y0, z0, x1, y1, z1 int) {
	x0, y0, z0 = s.X*TerrainSectionSize, s.Y*TerrainSectionSize, s.Z*TerrainSectionSize
	return x0, y0, z0, min(x0+TerrainSectionSize, t.SizeX), min(y0+TerrainSectionSize, t.SizeY), min(z0+TerrainSectionSize, t.SizeZ)
}

// emit writes section s's faces — read off the dual cells prepareSmooth
// placed, for a smooth surface, which must cover the section — and its box,
// index and ray bins. It writes only s, so several sections sharing one
// prepared builder can be emitted at once.
func (b *terrainBuilder) emit(s *TerrainSection) {
	t := b.t
	s.Faces = s.Faces[:0]
	x0, y0, z0, x1, y1, z1 := t.sectionBlocks(s)
	for z := z0; z < z1; z++ {
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				m := t.Blocks[t.Index(x, y, z)]
				if m == 0 {
					continue
				}
				for d := 0; d < 6; d++ {
					st := TerrainDirs[d]
					if t.Solid(x+st[0], y+st[1], z+st[2]) {
						continue
					}
					if t.Smooth {
						b.smoothFace(s, x, y, z, d)
					} else {
						b.blockFace(s, x, y, z, d, m)
					}
				}
			}
		}
	}
	b.finish(s)
}

// hasFace reports whether any block in [x0,x1) x [y0,y1) x [z0,z1) has a
// face: is solid, beside air.
func (b *terrainBuilder) hasFace(x0, y0, z0, x1, y1, z1 int) bool {
	t := b.t
	for z := z0; z < z1; z++ {
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				if t.Blocks[t.Index(x, y, z)] == 0 {
					continue
				}
				for d := 0; d < 6; d++ {
					st := TerrainDirs[d]
					if !t.Solid(x+st[0], y+st[1], z+st[2]) {
						return true
					}
				}
			}
		}
	}
	return false
}

// faceLook is the layer weights, cover colour and ground channel weights of
// face key on a block of material m: the biome painted on it may give the
// ground its own layer or its own texture, and gives it its tint.
func (b *terrainBuilder) faceLook(key int32, m uint8) (weights, cover uint32, ground uint64) {
	layer := int(m) - 1
	if bi := b.t.BiomeAt(key); bi != nil {
		if bi.GroundLayer > 0 && bi.GroundLayer <= TerrainLayerCount {
			layer = bi.GroundLayer - 1
		}
		tint, amount := bi.GroundCover()
		cover = packRGBA(tint, amount)
		if slot := int(b.t.Paint[key]); slot < len(b.groundOf) && b.groundOf[slot] >= 0 {
			ground = 255 << (8 * uint(b.groundOf[slot]))
		}
	}
	layer = clampInt(layer, 0, TerrainLayerCount-1)
	return 255 << (8 * uint(layer)), cover, ground
}

func packRGBA(c mgl32.Vec3, a float32) uint32 {
	q := func(v float32) uint32 { return uint32(clampInt(int(v*255+0.5), 0, 255)) }
	return q(c[0]) | q(c[1])<<8 | q(c[2])<<16 | q(a)<<24
}

func (b *terrainBuilder) blockFace(s *TerrainSection, x, y, z, d int, m uint8) {
	t := b.t
	ax := terrainFaceAxes[d]
	bs := t.BlockSize
	base := t.BlockMin(x, y, z)
	if d%2 == 0 {
		base[ax[0]] += bs
	}
	var u, v mgl32.Vec3
	u[ax[1]] = bs
	v[ax[2]] = bs
	var n mgl32.Vec3
	n[ax[0]] = 1
	if d%2 == 1 {
		n[ax[0]] = -1
	}
	key := t.FaceKey(x, y, z, d)
	w, cv, gw := b.faceLook(key, m)
	s.Faces = append(s.Faces, TerrainFace{
		Key:     key,
		Corners: [4]mgl32.Vec3{base, base.Add(u), base.Add(u).Add(v), base.Add(v)},
		Normals: [4]mgl32.Vec3{n, n, n, n},
		Weights: [4]uint32{w, w, w, w},
		Cover:   [4]uint32{cv, cv, cv, cv},
		Ground:  [4]uint64{gw, gw, gw, gw},
	})
}

// prepareSmooth fills the occupancy and blur for the section's
// neighbourhood, and clears the dual-cell cache.
func (b *terrainBuilder) prepareSmooth(x0, y0, z0, x1, y1, z1 int) {
	t := b.t
	p := b.pad
	b.r0 = [3]int{x0 - p, y0 - p, z0 - p}
	b.rn = [3]int{x1 - x0 + 2*p, y1 - y0 + 2*p, z1 - z0 + 2*p}
	n := b.rn[0] * b.rn[1] * b.rn[2]
	if cap(b.occ) < n {
		b.occ = make([]float32, n)
		b.blur = make([]float32, n)
		b.tmp = make([]float32, n)
		b.fld = make([]float32, n)
	}
	b.occ, b.blur, b.tmp, b.fld = b.occ[:n], b.blur[:n], b.tmp[:n], b.fld[:n]
	// Every stage below writes each sample from the stage before alone, so
	// it splits into slabs of Z across b.workers with nothing to merge: the
	// result is the same bytes however it is split (terrain_mesh_union.go).
	plane := b.rn[0] * b.rn[1]
	b.par(b.rn[2], func(zlo, zhi int) {
		// Row by row: a row outside the grid is all air, and inside it the
		// blocks are read straight, clipped to the grid along X.
		xlo, xhi := max(-b.r0[0], 0), min(t.SizeX-b.r0[0], b.rn[0])
		for z := zlo; z < zhi; z++ {
			for y := 0; y < b.rn[1]; y++ {
				row := b.occ[z*plane+y*b.rn[0] : z*plane+(y+1)*b.rn[0]]
				clear(row)
				gy, gz := b.r0[1]+y, b.r0[2]+z
				if gy < 0 || gz < 0 || gy >= t.SizeY || gz >= t.SizeZ || xlo >= xhi {
					continue
				}
				blocks := t.Blocks[t.Index(b.r0[0]+xlo, gy, gz):]
				for x := xlo; x < xhi; x++ {
					if blocks[x-xlo] != 0 {
						row[x] = 1
					}
				}
			}
		}
	})
	// A separable Gaussian along each axis. Outside the scratch region the
	// first pass reads the grid itself; later passes clamp, which only
	// disturbs samples within the kernel's radius of the region's edge — pad
	// keeps those further out than anything the faces read.
	stride := [3]int{1, b.rn[0], b.rn[0] * b.rn[1]}
	src, dst := b.occ, b.tmp
	r := len(b.kernel) / 2
	for axis := 0; axis < 3; axis++ {
		if axis == 1 {
			src, dst = b.tmp, b.blur
		} else if axis == 2 {
			src, dst = b.blur, b.tmp
		}
		st := stride[axis]
		na := b.rn[axis]
		b.par(b.rn[2], func(zlo, zhi int) {
			for z := zlo; z < zhi; z++ {
				for y := 0; y < b.rn[1]; y++ {
					row := y*stride[1] + z*stride[2]
					// Whether the whole row is the kernel's radius inside the
					// region along the pass's axis (X is decided per sample).
					rowIn := true
					switch axis {
					case 1:
						rowIn = y >= r && y < na-r
					case 2:
						rowIn = z >= r && z < na-r
					}
					for x := 0; x < b.rn[0]; x++ {
						i := row + x
						if !rowIn || (axis == 0 && (x < r || x >= na-r)) {
							dst[i] = b.blurEdge(axis, [3]int{x, y, z}, i, src)
							continue
						}
						sum := float32(0)
						j := i - r*st
						for _, w := range b.kernel {
							sum += w * src[j]
							j += st
						}
						dst[i] = sum
					}
				}
			}
		})
	}
	// The field the surface is placed on keeps every block on its own side
	// of one half: a solid block never reads less than terrainFieldHold
	// above it, an air block never more than that below. The blur alone
	// fills in anything narrower than itself — a mined tunnel two blocks
	// high reads as solid in its middle and seals — and thins a one-block
	// ledge away. Held like this, every block-to-air edge still crosses one
	// half, so the surface keeps every feature the blocks have, while on open
	// ground (where a slope's blur already agrees with the blocks) nothing
	// changes.
	b.par(n, func(lo, hi int) {
		copy(b.blur[lo:hi], b.tmp[lo:hi])
		for i := lo; i < hi; i++ {
			if b.occ[i] > 0 {
				b.fld[i] = max(b.blur[i], 0.5+terrainFieldHold)
			} else {
				b.fld[i] = min(b.blur[i], 0.5-terrainFieldHold)
			}
		}
	})

	h := b.halo
	b.c0 = [3]int{x0 - 1 - h, y0 - 1 - h, z0 - 1 - h}
	b.cn = [3]int{x1 - x0 + 2 + 2*h, y1 - y0 + 2 + 2*h, z1 - z0 + 2 + 2*h}
	nc := b.cn[0] * b.cn[1] * b.cn[2]
	if cap(b.cells) < nc {
		b.cells = make([]terrainDualVertex, nc)
		b.pa = make([]mgl32.Vec3, nc)
		b.pb = make([]mgl32.Vec3, nc)
	}
	b.cells, b.pa, b.pb = b.cells[:nc], b.pa[:nc], b.pb[:nc]
	b.par(nc, func(lo, hi int) {
		clear(b.cells[lo:hi])
	})
	b.placeVertices()
}

// placeVertices places every dual cell's vertex in the section and its
// halo: dual contouring for a start, then constrained relaxation, then the
// normals of the relaxed surface.
//
// The relaxation is what takes the terraces out. Blocks only say solid or
// air, so a gentle hill is steps a few blocks apart, and anything read off
// them locally — however blurred — leaves a shelf between steps. Here each
// vertex is moved, round after round, toward the average of its neighbours,
// but never out of its own cell. Held in their cells like that the vertices
// cannot lose any feature the blocks have (a vertex always sits between the
// solid and the air it separates), and on a staircase they settle into the
// straight ramp through its corners however far apart the steps are — the
// "constrained elastic surface net". Cliffs stay cliffs: their vertices
// are held in a vertical stack of cells.
func (b *terrainBuilder) placeVertices() {
	t := b.t
	n := b.cn
	idx := func(cx, cy, cz int) int {
		return (cx - b.c0[0]) + (cy-b.c0[1])*n[0] + (cz-b.c0[2])*n[0]*n[1]
	}
	// Each cell's vertex reads only the field and the blocks and is written
	// by its own cell alone, so slabs of cells place in parallel; so does
	// every per-cell stage after it, each reading only what the stage before
	// finished writing.
	b.par(n[2], func(zlo, zhi int) {
		for cz := b.c0[2] + zlo; cz < b.c0[2]+zhi; cz++ {
			for cy := b.c0[1]; cy < b.c0[1]+n[1]; cy++ {
				for cx := b.c0[0]; cx < b.c0[0]+n[0]; cx++ {
					s0 := b.solid(cx, cy, cz)
					mixed := false
					for c := 1; c < 8 && !mixed; c++ {
						mixed = b.solid(cx+c&1, cy+(c>>1)&1, cz+(c>>2)&1) != s0
					}
					if !mixed {
						continue
					}
					dv := b.dualVertex(cx, cy, cz)
					dv.surf = true
					b.pa[idx(cx, cy, cz)] = dv.pos
				}
			}
		}
	})

	b.surf = b.surf[:0]
	for i := range b.cells {
		if b.cells[i].surf {
			b.surf = append(b.surf, int32(i))
		}
	}
	b.floorSmooth()

	// Relax, over the surface cells only. A cell pulls only toward
	// neighbours whose surface faces roughly its own way: across a tunnel
	// one block high the floor's and the roof's cells are neighbours, and
	// averaging them together would close it.
	bs := t.BlockSize
	const inset = 0.001
	const pull = 0.85
	strides := [3]int{1, n[0], n[0] * n[1]}
	if cap(b.nbr) < len(b.surf)*6 {
		b.nbr = make([]int32, len(b.surf)*6)
	}
	b.nbr = b.nbr[:len(b.surf)*6]
	b.par(len(b.surf), func(lo, hi int) {
		for si := lo; si < hi; si++ {
			i := int(b.surf[si])
			c := [3]int{i % n[0], (i / n[0]) % n[1], i / (n[0] * n[1])}
			ni := b.cells[i].nrm
			for a := 0; a < 3; a++ {
				for k, st := range [2]int{-1, 1} {
					slot := si*6 + a*2 + k
					b.nbr[slot] = -1
					if c[a]+st < 0 || c[a]+st >= n[a] {
						continue
					}
					j := i + st*strides[a]
					if b.cells[j].surf && b.cells[j].nrm.Dot(ni) > 0 {
						b.nbr[slot] = int32(j)
					}
				}
			}
		}
	})
	// Jacobi rounds: each reads the last round's positions only, so a round
	// splits across workers and the rounds are the barriers.
	src, dst := b.pa, b.pb
	for it := 0; it < b.iters; it++ {
		b.par(len(b.surf), func(lo, hi int) {
			for si := lo; si < hi; si++ {
				i := int(b.surf[si])
				var sum mgl32.Vec3
				k := 0
				for _, j := range b.nbr[si*6 : si*6+6] {
					if j >= 0 {
						sum = sum.Add(src[j])
						k++
					}
				}
				p := src[i]
				if k > 0 {
					p = p.Add(sum.Mul(1 / float32(k)).Sub(p).Mul(pull))
				}
				c := [3]int{i % n[0], (i / n[0]) % n[1], i / (n[0] * n[1])}
				for a := 0; a < 3; a++ {
					lo := t.Origin[a] + (float32(b.c0[a]+c[a])+0.5)*bs
					p[a] = min(max(p[a], lo+inset*bs), lo+bs-inset*bs)
				}
				dst[i] = p
			}
		})
		src, dst = dst, src
	}
	for _, i := range b.surf {
		b.cells[i].pos = src[i]
	}

	// Normals of the relaxed surface: the quads round each vertex, summed.
	// Every position is final by now, and each cell writes only its own
	// normal.
	b.par(n[2], func(zlo, zhi int) {
		for cz := b.c0[2] + zlo; cz < b.c0[2]+zhi; cz++ {
			for cy := b.c0[1]; cy < b.c0[1]+n[1]; cy++ {
				for cx := b.c0[0]; cx < b.c0[0]+n[0]; cx++ {
					dv := &b.cells[idx(cx, cy, cz)]
					if !dv.surf {
						continue
					}
					var sum mgl32.Vec3
					for axis := 0; axis < 3; axis++ {
						for k := 0; k < 4; k++ {
							a := [3]int{cx, cy, cz}
							a[(axis+1)%3] += k & 1
							a[(axis+2)%3] += k >> 1
							bb := a
							bb[axis]++
							sa, sb := b.solid(a[0], a[1], a[2]), b.solid(bb[0], bb[1], bb[2])
							if sa == sb {
								continue
							}
							solid, d := a, axis*2
							if sb {
								solid, d = bb, axis*2+1
							}
							q, ok := b.quadCells(solid, d)
							if !ok {
								continue
							}
							p0, p1, p2, p3 := b.cells[q[0]].pos, b.cells[q[1]].pos, b.cells[q[2]].pos, b.cells[q[3]].pos
							sum = sum.Add(p2.Sub(p0).Cross(p3.Sub(p1)))
						}
					}
					if sum.Len() > 1e-9 {
						dv.nrm = sum.Normalize()
					}
				}
			}
		}
	})
}

// floorSmooth levels the floors. Ground built from blocks is a staircase:
// flat for as many columns as a hill takes to climb one block, then a step.
// Read off the blocks, however blurred or relaxed, the flats stay flat. So
// every vertex on a floor — a dual cell with solid below and air above along
// one of its vertical edges, and no roof in it — takes its height from the
// floors around it: a Gaussian average, Terrain.Smoothing columns wide, of
// the top of each surrounding column's floor. Averaging a staircase that way
// gives the ramp through it, however far apart the steps.
//
// Each column contributes the floor nearest this one in height, and only if
// it is within reach of it — a step of about one block per two columns of
// distance. So a cliff's top and its foot never average together and the
// cliff stays a cliff, and a cave's floor averages with the cave's floor, not
// with the ground above it. The height is clamped into the vertex's cell, as
// every vertex is, so no feature the blocks have is lost.
func (b *terrainBuilder) floorSmooth() {
	t := b.t
	r := t.floorRadius()
	sigma := t.EffectiveSmoothing()
	// Columns' floors: the Y of every solid block with air above it, in the
	// columns the averaging can reach and the heights it can look at.
	x0, z0 := b.c0[0]-r, b.c0[2]-r
	nx, nz := b.cn[0]+2*r+1, b.cn[2]+2*r+1
	ylo, yhi := b.c0[1]-r-2, b.c0[1]+b.cn[1]+r+2
	if cap(b.floors) < nx*nz {
		b.floors = make([][]int16, nx*nz)
	}
	b.floors = b.floors[:nx*nz]
	b.par(nz, func(zlo, zhi int) {
		for cz := zlo; cz < zhi; cz++ {
			for cx := 0; cx < nx; cx++ {
				f := b.floors[cz*nx+cx][:0]
				x, z := x0+cx, z0+cz
				if x >= 0 && z >= 0 && x < t.SizeX && z < t.SizeZ {
					for y := max(ylo, 0); y <= min(yhi, t.SizeY-1); y++ {
						if t.Blocks[t.Index(x, y, z)] != 0 && !t.Solid(x, y+1, z) {
							f = append(f, int16(y))
						}
					}
				}
				b.floors[cz*nx+cx] = f
			}
		}
	})
	// Each column's weight depends only on its offset from the cell: a
	// table, not an exp per column per vertex. Columns out of reach weigh 0.
	side := 2*r + 2
	if cap(b.fw) < side*side {
		b.fw = make([]float32, side*side)
		b.fd = make([]int, side*side)
	}
	b.fw, b.fd = b.fw[:side*side], b.fd[:side*side]
	for dz := -r; dz <= r+1; dz++ {
		for dx := -r; dx <= r+1; dx++ {
			fx, fz := float32(dx)-0.5, float32(dz)-0.5
			d2 := fx*fx + fz*fz
			dist := float32(math.Sqrt(float64(d2)))
			k := (dz+r)*side + dx + r
			b.fw[k], b.fd[k] = 0, 0
			if dist <= float32(r)+0.75 {
				b.fw[k] = float32(math.Exp(-float64(d2) / float64(2*sigma*sigma)))
				// How far from this floor's height a column's floor may be and
				// still count: about a block per two columns of distance.
				b.fd[k] = 1 + int(dist/2)
			}
		}
	}
	n := b.cn
	bs := t.BlockSize
	const inset = 0.001
	// Each floor vertex writes only its own start position.
	b.par(len(b.surf), func(slo, shi int) {
		for _, i32 := range b.surf[slo:shi] {
			i := int(i32)
			c := [3]int{b.c0[0] + i%n[0], b.c0[1] + (i/n[0])%n[1], b.c0[2] + i/(n[0]*n[1])}
			// A floor edge, and no roof, among the cell's four vertical edges.
			floor, roof := false, false
			for k := 0; k < 4; k++ {
				x, z := c[0]+k&1, c[2]+k>>1
				lo, hi := b.solid(x, c[1], z), b.solid(x, c[1]+1, z)
				floor = floor || (lo && !hi)
				roof = roof || (!lo && hi)
			}
			if !floor || roof {
				continue
			}
			// The cell is centred between four columns; weights by distance
			// from there, in half-block units squared.
			var sum, wsum float32
			for dz := -r; dz <= r+1; dz++ {
				for dx := -r; dx <= r+1; dx++ {
					k := (dz+r)*side + dx + r
					wt := b.fw[k]
					if wt == 0 {
						continue
					}
					col := b.floors[(c[2]+dz-z0)*nx+(c[0]+dx-x0)]
					best, bestD := int16(0), int(1<<30)
					for _, fy := range col {
						if dd := absInt(int(fy) - c[1]); dd < bestD {
							best, bestD = fy, dd
						}
					}
					if bestD > b.fd[k] {
						continue
					}
					sum += wt * float32(best+1)
					wsum += wt
				}
			}
			if wsum <= 0 {
				continue
			}
			top := sum / wsum
			lo := float32(c[1]) + 0.5
			top = min(max(top, lo+inset), lo+1-inset)
			p := b.pa[i]
			p[1] = t.Origin[1] + top*bs
			b.pa[i] = p
		}
	})
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// quadCells is the indices, in b.cells, of the four dual cells whose
// vertices make the smooth quad of solid block solid's face looking along d,
// in the face's corner order (0, u, u+v, v); ok is false when one is outside
// the scratch region.
func (b *terrainBuilder) quadCells(solid [3]int, d int) (q [4]int, ok bool) {
	ax := terrainFaceAxes[d]
	cell := solid
	if d%2 == 1 {
		cell[ax[0]]--
	}
	offs := [4][2]int{{-1, -1}, {0, -1}, {0, 0}, {-1, 0}}
	for i, o := range offs {
		c := cell
		c[ax[1]] += o[0]
		c[ax[2]] += o[1]
		for a := 0; a < 3; a++ {
			if c[a] < b.c0[a] || c[a] >= b.c0[a]+b.cn[a] {
				return q, false
			}
		}
		q[i] = (c[0] - b.c0[0]) + (c[1]-b.c0[1])*b.cn[0] + (c[2]-b.c0[2])*b.cn[0]*b.cn[1]
	}
	return q, true
}

// solid is t.Solid(x, y, z), read from the occupancy prepareSmooth filled
// when the block is in the scratch region — everything the dual cells and
// their normals ask about is — and from the grid otherwise.
func (b *terrainBuilder) solid(x, y, z int) bool {
	lx, ly, lz := x-b.r0[0], y-b.r0[1], z-b.r0[2]
	if lx < 0 || ly < 0 || lz < 0 || lx >= b.rn[0] || ly >= b.rn[1] || lz >= b.rn[2] {
		return b.t.Solid(x, y, z)
	}
	return b.occ[lx+ly*b.rn[0]+lz*b.rn[0]*b.rn[1]] > 0
}

// blurEdge is the blur pass along axis at sample c (index i) of the scratch
// region when the kernel reaches past the region's edge. It sums in the same
// order as the interior's loop, so a sample comes out the same bits either
// way.
func (b *terrainBuilder) blurEdge(axis int, c [3]int, i int, src []float32) float32 {
	r := len(b.kernel) / 2
	st := [3]int{1, b.rn[0], b.rn[0] * b.rn[1]}[axis]
	na := b.rn[axis]
	sum := float32(0)
	for k := -r; k <= r; k++ {
		cc := c
		cc[axis] += k
		v := float32(0)
		if cc[axis] < 0 || cc[axis] >= na {
			v = b.edgeSample(axis, cc, src)
		} else {
			v = src[i+k*st]
		}
		sum += b.kernel[k+r] * v
	}
	return sum
}

// edgeSample is the partially-blurred value just outside the scratch region
// along axis. Only the first pass (raw occupancy) can read the grid
// directly; later passes clamp (see prepareSmooth).
func (b *terrainBuilder) edgeSample(axis int, c [3]int, src []float32) float32 {
	if axis == 0 {
		if b.t.Solid(b.r0[0]+c[0], b.r0[1]+c[1], b.r0[2]+c[2]) {
			return 1
		}
		return 0
	}
	c[axis] = clampInt(c[axis], 0, b.rn[axis]-1)
	return src[c[0]+c[1]*b.rn[0]+c[2]*b.rn[0]*b.rn[1]]
}

// terrainFieldHold is how far past one half the held field keeps each block
// on its own side.
const terrainFieldHold = 0.05

func (b *terrainBuilder) localIndex(x, y, z int) int {
	lx, ly, lz := x-b.r0[0], y-b.r0[1], z-b.r0[2]
	lx = clampInt(lx, 0, b.rn[0]-1)
	ly = clampInt(ly, 0, b.rn[1]-1)
	lz = clampInt(lz, 0, b.rn[2]-1)
	return lx + ly*b.rn[0] + lz*b.rn[0]*b.rn[1]
}

// sample reads the held field at block (x, y, z).
func (b *terrainBuilder) sample(x, y, z int) float32 { return b.fld[b.localIndex(x, y, z)] }

// gradient is the held field's gradient at block (x, y, z) — it points into
// the solid. On open ground the held field is the blur itself; in a tunnel
// narrower than the blur it is what still has the tunnel in it.
func (b *terrainBuilder) gradient(x, y, z int) mgl32.Vec3 {
	return mgl32.Vec3{
		b.sample(x+1, y, z) - b.sample(x-1, y, z),
		b.sample(x, y+1, z) - b.sample(x, y-1, z),
		b.sample(x, y, z+1) - b.sample(x, y, z-1),
	}.Mul(0.5)
}

// dualVertex is dual cell (cx, cy, cz)'s vertex — the cube between block
// centres (cx..cx+1, cy..cy+1, cz..cz+1).
func (b *terrainBuilder) dualVertex(cx, cy, cz int) *terrainDualVertex {
	li := (cx - b.c0[0]) + (cy-b.c0[1])*b.cn[0] + (cz-b.c0[2])*b.cn[0]*b.cn[1]
	dv := &b.cells[li]
	if dv.done {
		return dv
	}
	dv.done = true
	t := b.t
	var ata mgl32.Mat3
	var atb, mass, nsum mgl32.Vec3
	var wsum [TerrainLayerCount]float32
	var gsum [TerrainGroundChannels]float32
	var csum mgl32.Vec4
	count := 0
	// paintCount is the crossings that say anything about the paint: painted
	// ones, and bare ones facing up. An unpainted riser in a painted field
	// (a fill paints only the tops of the columns, and a smooth hill's
	// terraces have sides) is neither bare ground nor painted ground, and
	// must not thin the paint out across every step of a slope.
	paintCount := 0
	for axis := 0; axis < 3; axis++ {
		for k := 0; k < 4; k++ {
			a := [3]int{cx, cy, cz}
			a[(axis+1)%3] += k & 1
			a[(axis+2)%3] += k >> 1
			bb := a
			bb[axis]++
			sa, sb := b.solid(a[0], a[1], a[2]), b.solid(bb[0], bb[1], bb[2])
			if sa == sb {
				continue
			}
			solid, air := a, bb
			if sb {
				solid, air = bb, a
			}
			// Where the held field crosses one half along the edge — always
			// inside it, since the field keeps solid above and air below.
			ds, da := b.sample(solid[0], solid[1], solid[2]), b.sample(air[0], air[1], air[2])
			f := min(max((ds-0.5)/(ds-da), 0.02), 0.98)
			ps := mgl32.Vec3{float32(solid[0]) + 0.5, float32(solid[1]) + 0.5, float32(solid[2]) + 0.5}
			pa := mgl32.Vec3{float32(air[0]) + 0.5, float32(air[1]) + 0.5, float32(air[2]) + 0.5}
			p := ps.Add(pa.Sub(ps).Mul(f))
			g := b.gradient(solid[0], solid[1], solid[2]).Mul(1 - f).Add(b.gradient(air[0], air[1], air[2]).Mul(f))
			n := g.Mul(-1)
			if n.Len() < 1e-4 {
				n = pa.Sub(ps)
			}
			n = n.Normalize()
			ata = ata.Add(outer3(n, n))
			atb = atb.Add(n.Mul(n.Dot(p)))
			mass = mass.Add(p)
			nsum = nsum.Add(n)
			count++

			dir := 0
			for d := 0; d < 6; d++ {
				st := TerrainDirs[d]
				if solid[0]+st[0] == air[0] && solid[1]+st[1] == air[1] && solid[2]+st[2] == air[2] {
					dir = d
					break
				}
			}
			fkey := t.FaceKey(solid[0], solid[1], solid[2], dir)
			w, cv, gw := b.faceLook(fkey, t.Blocks[t.Index(solid[0], solid[1], solid[2])])
			if dir == FaceYPos || t.Paint[fkey] != 0 {
				paintCount++
			}
			for l := 0; l < TerrainLayerCount; l++ {
				wsum[l] += float32((w >> (8 * uint(l))) & 0xff)
			}
			for c := 0; c < TerrainGroundChannels; c++ {
				gsum[c] += float32((gw >> (8 * uint(c))) & 0xff)
			}
			ca := float32(cv>>24) / 255
			csum = csum.Add(mgl32.Vec4{float32(cv&0xff) / 255 * ca, float32((cv>>8)&0xff) / 255 * ca, float32((cv>>16)&0xff) / 255 * ca, ca})
		}
	}
	lo := mgl32.Vec3{float32(cx) + 0.5, float32(cy) + 0.5, float32(cz) + 0.5}
	if count == 0 {
		dv.pos = t.Origin.Add(lo.Add(mgl32.Vec3{0.5, 0.5, 0.5}).Mul(t.BlockSize))
		dv.nrm = mgl32.Vec3{0, 1, 0}
		dv.weights = 255
		return dv
	}
	mass = mass.Mul(1 / float32(count))
	// (AtA + bias I) x = Atb + bias * mass.
	sys := ata.Add(mgl32.Ident3().Mul(terrainQEFBias))
	rhs := atb.Add(mass.Mul(terrainQEFBias))
	x := mass
	if det := sys.Det(); math.Abs(float64(det)) > 1e-9 {
		x = sys.Inv().Mul3x1(rhs)
	}
	const inset = 0.001
	clampCell := func(x mgl32.Vec3) mgl32.Vec3 {
		for i := 0; i < 3; i++ {
			x[i] = min(max(x[i], lo[i]+inset), lo[i]+1-inset)
		}
		return x
	}
	x = clampCell(x)
	dv.pos = t.Origin.Add(x.Mul(t.BlockSize))
	// A first normal, from the Hermite data: what the relaxation uses to tell
	// a floor from the roof above it. The surface's own replaces it once the
	// vertices are final.
	if nsum.Len() > 1e-6 {
		dv.nrm = nsum.Normalize()
	} else {
		dv.nrm = mgl32.Vec3{0, 1, 0}
	}
	dv.weights = packWeights(wsum)
	// A fraction of the faces round the vertex, not a share of 255: a
	// corner half on bare ground is half textured, which is what fades a
	// painted patch out at its edge instead of stopping on a block line.
	pc := float32(max(paintCount, 1))
	for c, v := range gsum {
		dv.ground |= uint64(clampInt(int(v/pc+0.5), 0, 255)) << (8 * uint(c))
	}
	if csum[3] > 0 {
		inv := 1 / csum[3]
		dv.cover = packRGBA(mgl32.Vec3{csum[0] * inv, csum[1] * inv, csum[2] * inv}, min(csum[3]/pc, 1))
	}
	return dv
}

func outer3(a, b mgl32.Vec3) mgl32.Mat3 {
	return mgl32.Mat3{
		a[0] * b[0], a[1] * b[0], a[2] * b[0],
		a[0] * b[1], a[1] * b[1], a[2] * b[1],
		a[0] * b[2], a[1] * b[2], a[2] * b[2],
	}
}

// packWeights turns summed layer weights into bytes summing to exactly 255,
// the remainder going to the heaviest layer.
func packWeights(w [TerrainLayerCount]float32) uint32 {
	sum := float32(0)
	heaviest := 0
	for i, v := range w {
		sum += v
		if v > w[heaviest] {
			heaviest = i
		}
	}
	if sum <= 0 {
		return 255
	}
	var bytes [TerrainLayerCount]int
	total := 0
	for i, v := range w {
		bytes[i] = int(v / sum * 255)
		total += bytes[i]
	}
	bytes[heaviest] += 255 - total
	out := uint32(0)
	for i, v := range bytes {
		out |= uint32(clampInt(v, 0, 255)) << (8 * uint(i))
	}
	return out
}

func (b *terrainBuilder) smoothFace(s *TerrainSection, x, y, z, d int) {
	var f TerrainFace
	f.Key = b.t.FaceKey(x, y, z, d)
	// Corners 0, u, u+v, v: the dual cells round the block-to-air edge,
	// placed and relaxed by placeVertices.
	q, _ := b.quadCells([3]int{x, y, z}, d)
	for i, ci := range q {
		dv := &b.cells[ci]
		f.Corners[i] = dv.pos
		f.Normals[i] = dv.nrm
		f.Weights[i] = dv.weights
		f.Cover[i] = dv.cover
		f.Ground[i] = dv.ground
	}
	// Fold along the shorter diagonal.
	f.AltSplit = f.Corners[1].Sub(f.Corners[3]).LenSqr() < f.Corners[0].Sub(f.Corners[2]).LenSqr()
	s.Faces = append(s.Faces, f)
}

// finish computes the section's box, face index and ray bins.
func (b *terrainBuilder) finish(s *TerrainSection) {
	t := b.t
	inf := float32(math.Inf(1))
	s.Lo, s.Hi = mgl32.Vec3{inf, inf, inf}, mgl32.Vec3{-inf, -inf, -inf}
	if s.faceIndex == nil {
		s.faceIndex = map[int32]int32{}
	} else {
		clear(s.faceIndex)
	}
	nb := terrainBrickSpan * terrainBrickSpan * terrainBrickSpan
	counts := make([]int32, nb+1)
	type ref struct{ b, tri int32 }
	refs := make([]ref, 0, len(s.Faces)*4)
	base := [3]int{s.X*terrainBricksSec - 1, s.Y*terrainBricksSec - 1, s.Z*terrainBricksSec - 1}
	inv := 1 / (t.BlockSize * terrainBrick)
	for fi := range s.Faces {
		f := &s.Faces[fi]
		s.faceIndex[f.Key] = int32(fi)
		for _, p := range f.Corners {
			for k := 0; k < 3; k++ {
				s.Lo[k] = min(s.Lo[k], p[k])
				s.Hi[k] = max(s.Hi[k], p[k])
			}
		}
		for k := 0; k < 2; k++ {
			tri := f.Triangle(k)
			var lo, hi [3]int
			for a := 0; a < 3; a++ {
				mn := min(tri[0][a], tri[1][a], tri[2][a])
				mx := max(tri[0][a], tri[1][a], tri[2][a])
				lo[a] = clampInt(int(math.Floor(float64((mn-t.Origin[a])*inv)))-base[a], 0, terrainBrickSpan-1)
				hi[a] = clampInt(int(math.Floor(float64((mx-t.Origin[a])*inv)))-base[a], 0, terrainBrickSpan-1)
			}
			for bz := lo[2]; bz <= hi[2]; bz++ {
				for by := lo[1]; by <= hi[1]; by++ {
					for bx := lo[0]; bx <= hi[0]; bx++ {
						bi := int32((bz*terrainBrickSpan+by)*terrainBrickSpan + bx)
						refs = append(refs, ref{bi, int32(fi*2 + k)})
						counts[bi+1]++
					}
				}
			}
		}
	}
	for i := 1; i <= nb; i++ {
		counts[i] += counts[i-1]
	}
	s.brickStart = counts
	s.brickTris = make([]int32, len(refs))
	fill := make([]int32, nb)
	for _, r := range refs {
		s.brickTris[counts[r.b]+fill[r.b]] = r.tri
		fill[r.b]++
	}
}
