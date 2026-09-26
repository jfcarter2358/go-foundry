package collision

// brush_boxes.go — per-chunk brush boxes, cached against the mesh generation.

import (
	"slices"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// brushBoxes caches a chunk's brush boxes for the two callers that ask about
// every brush of a chunk many times a frame: the particle probe, once per
// colliding particle, and the entity resolver, once per body per tick —
// thousands of re-boxings of the same unmoved brushes a second, which was the
// whole of what particle collision used to cost.
//
// Invalidated the way ConvexBrushPieces is (world/brush_decompose.go): by
// BrushMeshGeneration, which every mesh change moves on, and by the brush
// slice itself being a different one. Which brushes may move follows the
// raycast trees' rule (raycast_accel.go): a Dynamic brush, or one not at rest
// when the cache was built, is "loose" — boxed live on every use and in no
// cell — and flipping Dynamic moves the generation on.
type brushBoxes struct {
	gen     uint64
	brushes *world.Brush
	count   int

	// Per brush, its box and, for a fixed one, a box per face; both in chunk
	// space, padded by the millimetre bodyBoxTouchesBrush pads by. A vertical
	// line meets a solid's top and bottom and nothing else, so the face boxes
	// turn a brush of hundreds of faces into two ray-triangle tests.
	boxes     []aabb
	faceBoxes [][]aabb
	loose     []int32

	// A grid over the chunk's XZ footprint, each cell listing the fixed
	// brushes whose box covers it, so a probe visits the few brushes under its
	// column rather than every brush of a ground chunk that has thousands.
	lo     mgl32.Vec3
	cell   float32
	nx, nz int
	cells  [][]int32

	// Scratch for near: the candidate list it returns, and a per-brush stamp
	// so a brush listed in several of the cells a body covers comes back once.
	cand  []int32
	seen  []uint32
	stamp uint32
}

// brushGridCells caps the grid's size a side: a chunk's footprint is cut into
// at most this many cells along its longer axis, and never cells smaller than
// brushGridMinCell, so a small chunk is a handful of cells.
const (
	brushGridCells   = 64
	brushGridMinCell = float32(2)
	brushBoxPad      = float32(1e-3)
)

var brushBoxCache = map[*world.Chunk]*brushBoxes{}

// brushBoxesFor returns ch's box cache, building it if the chunk's meshes
// have changed since it was built.
func brushBoxesFor(ch *world.Chunk) *brushBoxes {
	if len(ch.Brushes) == 0 {
		return nil
	}
	gen := world.BrushMeshGeneration()
	pc := brushBoxCache[ch]
	if pc != nil && pc.gen == gen && pc.brushes == &ch.Brushes[0] && pc.count == len(ch.Brushes) {
		return pc
	}
	if pc == nil {
		// Chunk pointers die with their level; a cache that outlived one is
		// dropped when it is noticed rather than tracked.
		if len(brushBoxCache) > 256 {
			clear(brushBoxCache)
		}
		pc = &brushBoxes{}
		brushBoxCache[ch] = pc
	}
	pc.gen, pc.brushes, pc.count = gen, &ch.Brushes[0], len(ch.Brushes)
	pc.boxes = append(pc.boxes[:0], make([]aabb, len(ch.Brushes))...)
	pc.faceBoxes = append(pc.faceBoxes[:0], make([][]aabb, len(ch.Brushes))...)
	pc.loose = pc.loose[:0]
	all := emptyBox()
	for bi := range ch.Brushes {
		b := &ch.Brushes[bi]
		if b.Dynamic || !b.AtRest() {
			pc.boxes[bi] = emptyBox()
			pc.loose = append(pc.loose, int32(bi))
			continue
		}
		box := emptyBox()
		faces := make([]aabb, len(b.Faces))
		for fi := range b.Faces {
			fb := emptyBox()
			for _, vi := range b.Faces[fi].Indices {
				if vi >= 0 && vi < len(b.Verts) {
					fb.addPoint(b.Verts[vi])
				}
			}
			faces[fi] = padBox(fb)
		}
		for _, v := range b.Verts {
			box.addPoint(v)
		}
		pc.boxes[bi], pc.faceBoxes[bi] = padBox(box), faces
		if box.valid() {
			all.addBox(box)
		}
	}
	pc.buildGrid(all)
	return pc
}

func padBox(b aabb) aabb {
	if !b.valid() {
		return b
	}
	return aabb{b.lo.Sub(mgl32.Vec3{brushBoxPad, brushBoxPad, brushBoxPad}), b.hi.Add(mgl32.Vec3{brushBoxPad, brushBoxPad, brushBoxPad})}
}

// touches is bodyBoxTouchesBrush against a cached box, which already carries
// its millimetre of padding: whether the box at c with half-extents half
// overlaps it.
func (b aabb) touches(c, half mgl32.Vec3) bool {
	for i := 0; i < 3; i++ {
		if c[i]+half[i] < b.lo[i] || c[i]-half[i] > b.hi[i] {
			return false
		}
	}
	return true
}

// inColumn reports whether a vertical line at p's XZ passes through b.
func (b aabb) inColumn(p mgl32.Vec3) bool {
	return b.valid() && p.X() >= b.lo.X() && p.X() <= b.hi.X() && p.Z() >= b.lo.Z() && p.Z() <= b.hi.Z()
}

func (pc *brushBoxes) buildGrid(all aabb) {
	pc.cells, pc.nx, pc.nz = pc.cells[:0], 0, 0
	if !all.valid() {
		return
	}
	pc.lo = all.lo
	ext := max(all.hi.X()-all.lo.X(), all.hi.Z()-all.lo.Z())
	pc.cell = max(ext/brushGridCells, brushGridMinCell)
	pc.nx = int((all.hi.X()-all.lo.X())/pc.cell) + 1
	pc.nz = int((all.hi.Z()-all.lo.Z())/pc.cell) + 1
	pc.cells = make([][]int32, pc.nx*pc.nz)
	for bi, box := range pc.boxes {
		if !box.valid() {
			continue
		}
		x0, z0 := pc.cellOf(box.lo)
		x1, z1 := pc.cellOf(box.hi)
		for z := z0; z <= z1; z++ {
			for x := x0; x <= x1; x++ {
				pc.cells[z*pc.nx+x] = append(pc.cells[z*pc.nx+x], int32(bi))
			}
		}
	}
}

// cellOf is the grid cell a chunk-space point falls in, clamped to the grid.
func (pc *brushBoxes) cellOf(p mgl32.Vec3) (int, int) {
	x := int((p.X() - pc.lo.X()) / pc.cell)
	z := int((p.Z() - pc.lo.Z()) / pc.cell)
	return min(max(x, 0), pc.nx-1), min(max(z, 0), pc.nz-1)
}

// fixedUnder is the fixed brushes whose cell a vertical probe at chunk-space p
// falls in. A point off the grid is off every fixed brush.
func (pc *brushBoxes) fixedUnder(p mgl32.Vec3) []int32 {
	if pc.nx == 0 {
		return nil
	}
	x := (p.X() - pc.lo.X()) / pc.cell
	z := (p.Z() - pc.lo.Z()) / pc.cell
	if x < 0 || z < 0 || int(x) >= pc.nx || int(z) >= pc.nz {
		return nil
	}
	return pc.cells[int(z)*pc.nx+int(x)]
}

// near is every brush a body box at c with half-extents half might touch, in
// ascending brush order: the fixed brushes listed in the grid cells the box's
// XZ footprint covers, those after brush index `after` only, plus every loose
// brush after it (which have no cell, and are boxed live by the caller).
//
// Ascending order is what lets a caller walk these instead of the whole
// brush list and get exactly the answer the whole walk gives: the skipped
// brushes are ones whose box the body is nowhere near, which the whole walk
// would have rejected on the box test anyway. The slice is scratch, valid until
// the next call on this chunk.
func (pc *brushBoxes) near(c, half mgl32.Vec3, after int) []int32 {
	pc.cand = pc.cand[:0]
	if len(pc.seen) != pc.count {
		pc.seen = make([]uint32, pc.count)
		pc.stamp = 0
	}
	pc.stamp++
	if pc.stamp == 0 { // wrapped: every old stamp is now ambiguous
		clear(pc.seen)
		pc.stamp = 1
	}
	if pc.nx > 0 {
		x0, z0 := pc.cellOf(c.Sub(half))
		x1, z1 := pc.cellOf(c.Add(half))
		for z := z0; z <= z1; z++ {
			for x := x0; x <= x1; x++ {
				for _, bi := range pc.cells[z*pc.nx+x] {
					if int(bi) > after && pc.seen[bi] != pc.stamp {
						pc.seen[bi] = pc.stamp
						pc.cand = append(pc.cand, bi)
					}
				}
			}
		}
	}
	for _, bi := range pc.loose {
		if int(bi) > after {
			pc.cand = append(pc.cand, bi)
		}
	}
	slices.Sort(pc.cand)
	return pc.cand
}
