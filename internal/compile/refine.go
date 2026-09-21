package compile

// refine.go — more samples where a shadow edge crosses.
//
// The bake stores light per vertex, and SubdivideFaces puts the vertices on a
// GridStep grid. Out on open ground that is plenty: the light there changes
// slowly, if at all. Across a shadow edge it is a step from lit to dark, and a
// four-unit grid can only draw that as a four-unit sawtooth — the long shadow
// of a canyon wall laid across the floor as a row of triangles.
//
// So the edges are found and the grid is made finer there alone. A face whose
// vertices disagree about which lights they can see has an edge crossing it;
// every face in its grid cell is cut again at half the step, and the same
// question asked of the new pieces, down to MinGridStep. Cells are world cells,
// not faces, for the reason the grid is world-aligned in the first place (see
// the file comment in light.go): faces meeting in a cell are cut at the same
// places.
//
// Where a fine face meets a coarse one, the coarse side has no vertex to match
// the fine side's, and the two could show a seam. It needs no ring of extra
// cells to hide it, which is the obvious move and nearly doubled level1: a
// shadow edge crossing the boundary between two cells leaves that boundary's
// ends disagreeing, and both cells hold those ends, so both are cut. A coarse
// face is only left beside a fine one where no edge crosses between them.
//
// A corner buried against a flush neighbour (airStart) takes no part in the
// question. It is dark for want of air rather than because of an edge, and
// counting it would cut every hidden flush face for nothing.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// refineShadowEdges re-cuts, finer, the faces a shadow edge crosses — see the
// file comment. step is the grid the faces were cut on. b.bvh stays the one
// built before the cuts — a face and its pieces are the same surface — and the
// caller rebuilds it afterwards.
func (b *baker) refineShadowEdges(step, minStep float32) {
	t := b.tree
	// candidate[fi]: face fi was cut at the current step, so it is a piece the
	// next level may cut again. Every face is, to begin with.
	// src[fi]: face fi's index in the list b.bvh was built over, which its
	// shadow rays skip.
	candidate := make([]bool, len(t.Faces))
	src := make([]int, len(t.Faces))
	for i := range candidate {
		candidate[i], src[i] = true, i
	}
	for ; step/2 >= minStep-1e-4; step /= 2 {
		edge := make([]bool, len(t.Faces))
		parallelFor(len(t.Faces), func(fi int) {
			if candidate[fi] {
				edge[fi] = b.shadowEdgeCrosses(&t.Faces[fi], src[fi])
			}
		})
		marked := map[[3]int32]bool{}
		for fi, e := range edge {
			if !e {
				continue
			}
			marked[gridCell(centroidOf(t.Faces[fi].Points), step)] = true
		}
		if len(marked) == 0 {
			return
		}

		half := step / 2
		faces := make([]Face, 0, len(t.Faces))
		next := make([]bool, 0, len(t.Faces))
		nextSrc := make([]int, 0, len(t.Faces))
		remap := make([][]int, len(t.Faces))
		for fi := range t.Faces {
			f := &t.Faces[fi]
			if !candidate[fi] || !marked[gridCell(centroidOf(f.Points), step)] {
				faces = append(faces, *f)
				next = append(next, false)
				nextSrc = append(nextSrc, src[fi])
				remap[fi] = []int{len(faces) - 1}
				continue
			}
			for _, piece := range gridSubdivide(f.Points, half) {
				g := *f
				g.Points = piece
				g.dropLight()
				faces = append(faces, g)
				next = append(next, true)
				nextSrc = append(nextSrc, src[fi])
				remap[fi] = append(remap[fi], len(faces)-1)
			}
		}
		t.Faces = faces
		candidate, src = next, nextSrc
		for ni := range t.Nodes {
			t.Nodes[ni].Faces = remapIndices(t.Nodes[ni].Faces, remap)
		}
		for li := range t.Leaves {
			t.Leaves[li].Faces = remapIndices(t.Leaves[li].Faces, remap)
		}
	}
}

// shadowEdgeCrosses is whether f's corners disagree about which shadowing
// lights reach them, on either side the face is lit from. self is the face's
// index in b.bvh's list.
func (b *baker) shadowEdgeCrosses(f *Face, self int) bool {
	sides := []float32{1}
	if f.DoubleSided {
		sides = append(sides, -1)
	}
	for _, side := range sides {
		n := f.Plane.N.Mul(side)
		var first uint64
		seen := false
		for i, p := range f.Points {
			start, buried := b.airStart(f, i, n)
			if buried {
				continue // see the file comment
			}
			sig := b.shadowSignature(p, n, start, false, self)
			if !seen {
				first, seen = sig, true
			} else if sig != first {
				return true
			}
		}
	}
	return false
}

// shadowSignature is one bit per shadow-casting baked light (the first 64):
// set when the light would reach the point but something is in the way. The
// shadow half of sample, with the light's amount left out, because an edge is
// where that bit changes and nowhere else needs the samples.
func (b *baker) shadowSignature(p, n, start mgl32.Vec3, buried bool, self int) uint64 {
	var sig uint64
	bit := 0
	g := b.lights.Global
	for i := range b.lights.Directional {
		dl := &b.lights.Directional[i]
		if bit >= 64 || dl.EffectiveBrightness() <= 0 || dl.LitLive() || !dl.CastsShadows() || dl.Direction.Len() < 1e-6 {
			continue
		}
		toLight := dl.Direction.Normalize().Mul(-1)
		if g.LightWrap(n.Dot(toLight)) > 0 {
			reach := b.reach
			if e, ok := sealExit(b.seal, start, toLight); ok {
				reach = e.Sub(start).Len()
			}
			if b.bvh.occluded(start, toLight, reach, self, buried) {
				sig |= 1 << bit
			}
		}
		bit++
	}
	for i := range b.lights.Points {
		pl := &b.lights.Points[i]
		if bit >= 64 || pl.EffectiveBrightness() <= 0 || pl.Radius <= 0 || pl.LitLive() || !pl.CastsShadows() {
			continue
		}
		if d := pl.Position.Sub(p).Len(); d > 1e-6 && d < pl.Radius && g.LightWrap(n.Dot(pl.Position.Sub(p).Mul(1/d))) > 0 {
			ray := pl.Position.Sub(start)
			if l := ray.Len(); l > 1e-6 && b.bvh.occluded(start, ray.Mul(1/l), l, self, buried) {
				sig |= 1 << bit
			}
		}
		bit++
	}
	return sig
}

// gridCell is the world grid cell of size step holding p.
func gridCell(p mgl32.Vec3, step float32) [3]int32 {
	var k [3]int32
	for a := 0; a < 3; a++ {
		k[a] = int32(math.Floor(float64(p[a] / step)))
	}
	return k
}

func centroidOf(pts []mgl32.Vec3) mgl32.Vec3 {
	var c mgl32.Vec3
	for _, p := range pts {
		c = c.Add(p)
	}
	return c.Mul(1 / float32(len(pts)))
}
