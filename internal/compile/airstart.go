package compile

// airstart.go — where a baked vertex's rays set out from.
//
// A baked vertex is a corner of a face, and CSG puts a great many of those
// exactly on the line where the face disappears into a neighbouring brush: the
// foot of a canyon wall, where the ground runs under it. Nudged off the surface
// by the shadow bias, such a vertex is a hair INSIDE the neighbour. Neither way
// of treating a ray from there is right. Counted as blocked by the neighbour's
// faces, every one of them bakes black, and each black vertex is a dark
// triangle fan along the seam. Let out through the neighbour's back faces, a
// vertex on the shaded side of a wall sees the sun straight through it.
//
// What the vertex actually stands for is the visible surface beside it, so
// that is where its rays start: slid along its own face toward the face's
// centre, which is over the part of the face CSG kept, until the point is in
// open air. From there a ray meets any solid it crosses front first.

import (
	"math"
	"slices"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/bsp"
	"foundry/internal/world"
)

// airSteps are how far along its face a vertex's start is slid, in world
// units, tried in order until one is out of every solid. Each is capped at
// airMaxFraction of the way to the face's centre, so a small face never sends
// its corners' rays out from over one another.
var airSteps = [...]float32{0, 0.05, 0.15, 0.4, 1}

const airMaxFraction = 0.5

// solidIndex answers "is this point inside a brush" over a uniform grid of
// the solids' boxes.
type solidIndex struct {
	solids []solid
	cell   float32
	cells  map[[3]int32][]int32
}

const solidIndexCell = 8

func newSolidIndex(lvl *world.Level) *solidIndex {
	if lvl == nil {
		return nil
	}
	all, _ := gatherSolids(lvl)
	ix := &solidIndex{cell: solidIndexCell, cells: map[[3]int32][]int32{}}
	for _, s := range all {
		if s.chunk == "" {
			// The seal's slabs: outside the level, where no vertex is.
			continue
		}
		si := int32(len(ix.solids))
		ix.solids = append(ix.solids, s)
		lo, hi := ix.key(s.lo), ix.key(s.hi)
		for x := lo[0]; x <= hi[0]; x++ {
			for y := lo[1]; y <= hi[1]; y++ {
				for z := lo[2]; z <= hi[2]; z++ {
					k := [3]int32{x, y, z}
					ix.cells[k] = append(ix.cells[k], si)
				}
			}
		}
	}
	return ix
}

func (ix *solidIndex) key(p mgl32.Vec3) [3]int32 {
	var k [3]int32
	for a := 0; a < 3; a++ {
		k[a] = int32(math.Floor(float64(p[a] / ix.cell)))
	}
	return k
}

// inside reports whether p is inside any solid not from one of the chunks
// ignored.
func (ix *solidIndex) inside(p mgl32.Vec3, ignore []string) bool {
	if ix == nil {
		return false
	}
	for _, si := range ix.cells[ix.key(p)] {
		s := &ix.solids[si]
		if p[0] < s.lo[0] || p[1] < s.lo[1] || p[2] < s.lo[2] ||
			p[0] > s.hi[0] || p[1] > s.hi[1] || p[2] > s.hi[2] {
			continue
		}
		if s.containsPoint(p) && !slices.Contains(ignore, s.chunk) {
			return true
		}
	}
	return false
}

// airStart is where point i of face f sends its rays from on the side facing
// side (the plane's normal, or its reverse for a double-sided face's back):
// off the surface by the shadow bias and, if that is inside a brush, slid
// toward the face's centre until it is not. buried is true when no step got
// clear, and then the caller has to count a ray leaving a solid as blocked,
// since it cannot tell leaving the solid it is in from crossing a wall.
//
// A cover face (Face.CoveredBy) is lit for the moment its covering chunks are
// switched off, so their solids are not in its way.
func (b *baker) airStart(f *Face, i int, side mgl32.Vec3) (start mgl32.Vec3, buried bool) {
	return b.airStartAt(f, f.Points[i], side)
}

// airStartAt is airStart for any point p of face f — a lightmap texel's
// (lightmap.go) as well as a corner.
func (b *baker) airStartAt(f *Face, p, side mgl32.Vec3) (start mgl32.Vec3, buried bool) {
	off := side.Mul(b.opt.ShadowBias)
	toCentre := bsp.Centroid(f.Points).Sub(p)
	dist := toCentre.Len()
	if dist > 1e-6 {
		toCentre = toCentre.Mul(1 / dist)
	}
	limit := dist * airMaxFraction
	for _, d := range airSteps {
		start = p.Add(off).Add(toCentre.Mul(min(d, limit)))
		if !b.solids.inside(start, f.CoveredBy) {
			return start, false
		}
		if d >= limit {
			break
		}
	}
	return start, true
}
