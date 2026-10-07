package world

// terrain_edits.go — where a terrain's surface has changed since a
// generation.
//
// Generation says THAT a terrain changed; caches built from its surface over
// a large area (a scatter volume's height bake, every decal lying on it)
// would otherwise have to rebuild all of it for one mined block. So each
// edit made through TouchBlocks / TouchPaint is also remembered as a box,
// and SurfaceChangedSince answers with the box round every edit after a
// given generation — or says it cannot, after which the caller rebuilds the
// way it always did.
//
// The box is conservative: an edit's blocks grown by how far the surface can
// move around them (smoothReach for a change of shape, as markDirty uses;
// two blocks for paint), plus one more block because a smooth face's corners
// sit up to a block and a half past the block that owns it. Anything outside
// it is, face for face, what it was — the section rebuild that markDirty
// drives depends on exactly that.

import "github.com/go-gl/mathgl/mgl32"

// terrainEditHistory is how many edits a terrain remembers. A pick held down
// edits a few times a second and a cache catches up within a frame, so the
// window is generous; past it the oldest edit is dropped and a cache older
// than that rebuilds whole.
const terrainEditHistory = 64

// terrainEdit is one remembered edit: the generation it produced and the
// block box whose surface it can have changed, inclusive.
type terrainEdit struct {
	gen    uint64
	lo, hi [3]int
}

// terrainEditLog is a terrain's edit history. Its zero value remembers
// nothing before the first edit it sees.
type terrainEditLog struct {
	edits []terrainEdit
	// floor is the generation the history starts at: every edit producing a
	// generation above it is in edits. A whole-terrain Touch moves it up to
	// the Touch's own generation, since that edit has no box.
	floor uint64
	// shape is what the surface was built with when the history began; an
	// edit that finds it changed (Smooth toggled, a resize that did not go
	// through Touch) starts the history over.
	shape terrainShapeKey
}

// terrainShapeKey is everything besides the blocks and paint that decides
// where a terrain's faces are.
type terrainShapeKey struct {
	smooth     bool
	smoothing  float32
	blockSize  float32
	origin     mgl32.Vec3
	sx, sy, sz int
}

func (t *Terrain) shapeKey() terrainShapeKey {
	return terrainShapeKey{t.Smooth, t.EffectiveSmoothing(), t.BlockSize, t.Origin, t.SizeX, t.SizeY, t.SizeZ}
}

// forgetEdits starts the history over at the current generation: a change
// with no box, or a terrain the history no longer describes.
func (t *Terrain) forgetEdits() {
	t.edits = terrainEditLog{floor: t.Generation, shape: t.shapeKey()}
}

// recordEdit remembers that the edit which produced t.Generation touched
// blocks [x0,x1] x [y0,y1] x [z0,z1], reaching reach blocks round them.
func (t *Terrain) recordEdit(x0, y0, z0, x1, y1, z1, reach int) {
	if t.edits.shape != t.shapeKey() {
		t.forgetEdits()
		return
	}
	r := reach + 1
	e := terrainEdit{gen: t.Generation, lo: [3]int{x0 - r, y0 - r, z0 - r}, hi: [3]int{x1 + r, y1 + r, z1 + r}}
	if len(t.edits.edits) == terrainEditHistory {
		t.edits.floor = t.edits.edits[0].gen
		copy(t.edits.edits, t.edits.edits[1:])
		t.edits.edits = t.edits.edits[:terrainEditHistory-1]
	}
	t.edits.edits = append(t.edits.edits, e)
}

// SurfaceChangedSince reports where the terrain's surface (its faces: their
// corners, normals and looks) can differ from what it was when the
// generation counter stood at gen — the terrain's own Generation then, or
// TerrainGeneration() then, since every terrain draws from that one counter —
// as a chunk-space box. changed is false when nothing has; ok is false when
// the terrain cannot say — gen predates what it remembers, the whole terrain
// was touched, or its shape settings moved — and the caller must treat all of
// it as changed.
func (t *Terrain) SurfaceChangedSince(gen uint64) (lo, hi mgl32.Vec3, changed, ok bool) {
	if gen >= t.Generation {
		return lo, hi, false, true
	}
	if gen < t.edits.floor || t.edits.shape != t.shapeKey() {
		return lo, hi, true, false
	}
	var blo, bhi [3]int
	for _, e := range t.edits.edits {
		if e.gen <= gen {
			continue
		}
		if !changed {
			blo, bhi = e.lo, e.hi
		} else {
			for a := 0; a < 3; a++ {
				blo[a], bhi[a] = min(blo[a], e.lo[a]), max(bhi[a], e.hi[a])
			}
		}
		changed = true
	}
	if !changed {
		// The generation moved for something with no surface — cannot
		// happen through touch, but a caller is better served by "all" than
		// by a wrong "nothing".
		return lo, hi, true, false
	}
	// Block b spans [b, b+1); the box's far corner is the far side of bhi.
	lo = t.BlockMin(blo[0], blo[1], blo[2])
	hi = t.BlockMin(bhi[0]+1, bhi[1]+1, bhi[2]+1)
	return lo, hi, true, true
}
