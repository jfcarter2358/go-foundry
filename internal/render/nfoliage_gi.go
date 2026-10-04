package render

// nfoliage_gi.go — tree crowns in the live GI's distance field. Phase 6 of
// the Nanite plan (docs/nanite/PHASE_6.md), the spec's optional "voxel
// coverage feeds the GI SDF so canopies shade the ground".
//
// The GI traces its probes against a distance field built from sources
// (gi_sources.go): brushes' hulls, terrain blocks, models' baked grids.
// Foliage was none of them, so a forest floor took the open sky's light. A
// tree's voxels say where its crown is dense: each variant's CANOPY is its
// DAG cut at a tenth of the plant's size — the trunk's triangles, and a box
// for every voxel whose coverage, taken over the seven directions it keeps,
// is past nfGICoverage — baked once into a small distance grid
// (internal/meshsdf, as a model's is). Every tall plant within nfGIRange of
// the camera is then a mesh source: that grid, placed by the instance's own
// frame (where it stands, its growth axis, its turn, its size), coloured by
// its leaves. A unit hands its trees in as one group, re-derived only when
// the unit is rebuilt, so a still forest costs the field nothing per frame.
//
// The crown is a porous thing traced as a solid. A voxel a metre across of
// a broadleaf's crown stops a quarter of the rays through it, but a ray
// through the crown crosses half a dozen and is mostly stopped (measured on
// the generator's trees: most crown voxels sit between 0.1 and 0.3), so a
// voxel counts when it holds any real leaf at all: the crown's volume,
// with the air round it and the gaps between sprays left open. Wind is not
// followed (the grid is the rest pose): what a probe sees of a crown is
// blurred over metres anyway.
//
// FOUNDRY_NANITE_FOLIAGE_GI=1 turns it on; it is off by default. It changes
// the picture on purpose — on the forest bench (docs/nanite/PHASE_6.md) a
// view from the forest floor came out 18% darker, the ground under the
// crowns and the crowns' own undersides — which is the opposite of the
// parity a new path is held to before it is on by default. Only with the
// Nanite path on: the classic tiers have no voxels to give.

import (
	"math"
	"unsafe"

	"foundry/internal/cluster"
	"foundry/internal/gfx"
	"foundry/internal/meshsdf"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// NaniteFoliageGI puts tree crowns into the GI field (see the file comment).
var NaniteFoliageGI = gfx.EnvFlag("FOUNDRY_NANITE_FOLIAGE_GI", false)

// How far from the camera trees go into the field, the coverage a voxel
// must have to block it, and the cells a side of a canopy's grid.
const (
	nfGIRange    = 120
	nfGICoverage = 0.12
	nfGIGridRes  = 24
)

// nfGITree is one tall plant's placement, kept on its unit for the field.
type nfGITree struct {
	pos, up    mgl32.Vec3
	yaw, scale float32
	color      mgl32.Vec3
	bucket     int
}

// nfKeepTrees appends batches' tall plants (the shapes the Nanite path
// draws by default: those that had an impostor) to out.
func nfKeepTrees(out []nfGITree, batches []world.FoliageBatch) []nfGITree {
	for bi := range batches {
		b := &batches[bi]
		if !foliageShapes[b.Shape].impostor {
			continue
		}
		bucket := nfBucketOf(b.Shape, b.Variant)
		for _, in := range b.Instances {
			out = append(out, nfGITree{pos: in.Pos, up: in.Up, yaw: in.Yaw, scale: in.Scale, color: in.Color, bucket: bucket})
		}
	}
	return out
}

// canopy is bucket's canopy grid, baked the first time it is asked for
// (nil: the variant has nothing dense enough to block light).
func (r *nfRenderer) canopy(bucket int) *giMeshSDF {
	if r.canopies == nil {
		r.canopies = map[int]*giMeshSDF{}
	}
	if g, ok := r.canopies[bucket]; ok {
		return g
	}
	g := r.bakeCanopy(bucket)
	r.canopies[bucket] = g
	return g
}

// bakeCanopy builds a variant's canopy (see the file comment).
func (r *nfRenderer) bakeCanopy(bucket int) *giMeshSDF {
	d := &r.data
	b := &d.buckets[bucket]
	cut := b.radius / 10
	var tris []mgl32.Vec3
	// The dense voxels, by their cell on their size's lattice.
	dense := map[[4]int32]float32{}
	vert := func(i int) mgl32.Vec3 {
		v := d.verts[i*nfVertexFloats:]
		return mgl32.Vec3{v[0], v[1], v[2]}
	}
	for ci := b.firstCluster; ci < b.firstCluster+b.clusters; ci++ {
		c := &d.clusters[ci]
		if c.kind == nfKindProxy || c.err > cut || (c.perr >= 0 && c.perr <= cut) {
			continue
		}
		switch c.kind {
		case nfKindTriangles:
			for _, i := range d.idx[c.firstIndex : c.firstIndex+c.indexCount] {
				tris = append(tris, vert(c.baseVertex+int(i)))
			}
		case nfKindVoxels:
			for k := c.firstBrick; k < c.firstBrick+c.bricks; k++ {
				br := &d.bricks[k]
				corner := mgl32.Vec3{br.Corner[0], br.Corner[1], br.Corner[2]}
				size := br.Corner[3]
				mask := uint64(br.Mask[0]) | uint64(br.Mask[1])<<32
				vi := int(br.First)
				for l := 0; l < cluster.BrickVoxels; l++ {
					if mask&(1<<uint(l)) == 0 {
						continue
					}
					v := d.voxels[vi]
					vi++
					if nfVoxelMeanCoverage(v) < nfGICoverage {
						continue
					}
					lo := corner.Add(mgl32.Vec3{float32(l&3) * size, float32((l>>2)&3) * size, float32(l>>4) * size})
					dense[nfCell(lo, size)] = size
				}
			}
		}
	}
	// The dense voxels' outer faces only: a face two of them share is
	// inside the crown, and the bake's sign comes from a flood that a face
	// stops (a crown of boxes would be all shell, nothing inside).
	for c, size := range dense {
		lo := mgl32.Vec3{float32(c[0]) * size, float32(c[1]) * size, float32(c[2]) * size}
		hi := lo.Add(mgl32.Vec3{size, size, size})
		for f, n := range [6][3]int32{{-1, 0, 0}, {1, 0, 0}, {0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}} {
			if _, ok := dense[[4]int32{c[0] + n[0], c[1] + n[1], c[2] + n[2], c[3]}]; ok {
				continue
			}
			tris = appendBoxFace(tris, lo, hi, f)
		}
	}
	return meshsdf.Bake(tris, nfGIGridRes)
}

// nfVoxelMeanCoverage is a packed voxel's coverage over its seven directions.
func nfVoxelMeanCoverage(v nfVoxel) float32 {
	sum := float32(0)
	for k := 0; k < 7; k++ {
		w := v[k/4]
		sum += float32((w >> (8 * uint(k%4))) & 0xff)
	}
	return sum / 7 / 255
}

// nfCell is the voxel at lo of edge size, as a cell of that size's lattice.
func nfCell(lo mgl32.Vec3, size float32) [4]int32 {
	q := func(x float32) int32 { return int32(math.Floor(float64(x/size) + 0.5)) }
	return [4]int32{q(lo[0]), q(lo[1]), q(lo[2]), int32(math.Float32bits(size))}
}

// appendBoxFace appends face f (-x, +x, -y, +y, -z, +z) of the box [lo, hi]
// as two triangles, wound outward.
func appendBoxFace(tris []mgl32.Vec3, lo, hi mgl32.Vec3, f int) []mgl32.Vec3 {
	faces := [6][4]int{{0, 4, 6, 2}, {1, 3, 7, 5}, {0, 1, 5, 4}, {2, 6, 7, 3}, {0, 2, 3, 1}, {4, 5, 7, 6}}
	c := func(i int) mgl32.Vec3 {
		p := lo
		if i&1 != 0 {
			p[0] = hi[0]
		}
		if i&2 != 0 {
			p[1] = hi[1]
		}
		if i&4 != 0 {
			p[2] = hi[2]
		}
		return p
	}
	q := faces[f]
	return append(tris, c(q[0]), c(q[1]), c(q[2]), c(q[0]), c(q[2]), c(q[3]))
}

// nfPlantMatrix is a plant's frame — foliage_common.glsl's foliageBegin —
// carried into the world by the terrain's model: unit-scale plant space to
// world.
func nfPlantMatrix(model mgl32.Mat4, t *nfGITree) mgl32.Mat4 {
	up := t.up.Normalize()
	ref := mgl32.Vec3{0, 1, 0}
	if math.Abs(float64(up.Y())) >= 0.95 {
		ref = mgl32.Vec3{1, 0, 0}
	}
	t0 := ref.Cross(up).Normalize()
	b0 := up.Cross(t0)
	c, s := float32(math.Cos(float64(t.yaw))), float32(math.Sin(float64(t.yaw)))
	x := t0.Mul(c).Add(b0.Mul(s))
	z := x.Cross(up)
	local := mgl32.Mat4{
		x[0] * t.scale, x[1] * t.scale, x[2] * t.scale, 0,
		up[0] * t.scale, up[1] * t.scale, up[2] * t.scale, 0,
		z[0] * t.scale, z[1] * t.scale, z[2] * t.scale, 0,
		t.pos[0], t.pos[1], t.pos[2], 1,
	}
	return model.Mul4(local)
}

// foliageCanopySources adds this frame's tree crowns to the GI's sources:
// a group a unit within nfGIRange of the camera, all its trees.
func (s *giSourceSet) foliageCanopySources() {
	r := &foliage.nan
	if !r.active() || !NaniteFoliageGI || !foliage.initialized {
		return
	}
	for di := range foliage.draws {
		dr := &foliage.draws[di]
		u := dr.u
		if len(u.trees) == 0 || dr.d > nfGIRange {
			continue
		}
		// A unit lives at one address as long as it is built.
		k := giKey{'f', "", int(uintptr(unsafe.Pointer(u))), 0}
		h := hashMatCoarse(*dr.model) ^ u.treesGen*0x9e3779b97f4a7c15
		if g := s.groups[k]; g != nil && g.hash == h {
			g.seen = s.frame
			continue
		}
		var srcs []giSource
		for ti := range u.trees {
			t := &u.trees[ti]
			sdf := r.canopy(t.bucket)
			if sdf == nil {
				continue
			}
			wm := nfPlantMatrix(*dr.model, t)
			lo, hi := transformBox(wm, sdf.Lo, sdf.Hi)
			if ext := hi.Sub(lo); max(ext[0], ext[1], ext[2]) < s.minModel {
				continue
			}
			// The grid's cell in world units: the plant's size, times the
			// terrain's.
			scale := mgl32.Vec3{wm[4], wm[5], wm[6]}.Len()
			grid := mgl32.Scale3D(1/sdf.Cell, 1/sdf.Cell, 1/sdf.Cell).
				Mul4(mgl32.Translate3D(-sdf.Lo[0], -sdf.Lo[1], -sdf.Lo[2])).
				Mul4(wm.Inv())
			srcs = append(srcs, giSource{
				kind: giKindMesh, lo: lo, hi: hi, m: grid, mesh: sdf,
				color: capAlbedo(t.color).Vec4(0),
				p0:    mgl32.Vec4{0, float32(sdf.N), sdf.Cell * scale, 0},
			})
		}
		s.putHashed(k, false, srcs, h)
	}
}
