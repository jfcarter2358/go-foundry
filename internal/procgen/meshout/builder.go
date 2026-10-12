// Package meshout is how a procedural generator's meshes leave it: an
// ImportMesh builder (add triangles, weld, derive normals), the GLB write,
// and the Nanite cook of a mesh that exists only in memory.
//
// It is beside internal/procgen rather than in it because the cook reaches
// internal/nanitecook and through it internal/gfx, which the core must not
// (internal/layering_test.go). Stages import it; the core never does.
package meshout

// builder.go — building an ImportMesh.
//
// A generator rarely produces a mesh in the shape a cook wants. A heightfield
// is a grid of shared vertices; a marching surface is a soup of triangles
// whose corners repeat; an SDF mesher knows its normals from the gradient and
// a heightfield does not. The Builder takes any of those — indexed vertices,
// loose triangles, quads — and gives back one welded, indexed ImportMesh with
// a material slot per triangle and a normal per vertex.
//
// Welding is by quantised position (WeldTolerance), and optionally UV: a soup
// built from independent triangles comes out sharing its corners, which is
// what both the cook's simplifier and the collision's watertightness test
// need. Normals are area-weighted vertex normals over the welded mesh unless
// the caller supplied its own.

import (
	"math"

	"foundry/internal/model"

	"github.com/go-gl/mathgl/mgl32"
)

// Builder accumulates a mesh. The zero value is ready to use, with one slot
// named "default".
type Builder struct {
	positions []mgl32.Vec3
	normals   []mgl32.Vec3 // empty, or one per position
	uvs       []mgl32.Vec2 // empty, or one per position
	indices   []uint32
	triSlot   []int32
	slots     []model.ImportSlot
	slot      int32
}

// Slot adds (or selects, by name) a material slot; triangles added after go
// into it. texture is an engine texture name, or "" for the untextured look.
func (b *Builder) Slot(name, texture string) int {
	for i, s := range b.slots {
		if s.Name == name {
			b.slot = int32(i)
			return i
		}
	}
	b.slots = append(b.slots, model.ImportSlot{Name: name, Texture: texture, BaseColor: [4]float32{1, 1, 1, 1}})
	b.slot = int32(len(b.slots) - 1)
	return int(b.slot)
}

// SetSlot selects a slot by index.
func (b *Builder) SetSlot(i int) { b.slot = int32(i) }

// Vertex adds a vertex and returns its index. A zero normal means "derive
// it"; UVs are optional (pass the zero vector).
func (b *Builder) Vertex(p mgl32.Vec3, n mgl32.Vec3, uv mgl32.Vec2) uint32 {
	b.positions = append(b.positions, p)
	b.normals = append(b.normals, n)
	b.uvs = append(b.uvs, uv)
	return uint32(len(b.positions) - 1)
}

// Tri adds a triangle over existing vertices, counter-clockwise seen from
// the front.
func (b *Builder) Tri(a, c, d uint32) {
	b.indices = append(b.indices, a, c, d)
	b.triSlot = append(b.triSlot, b.slot)
}

// Quad adds two triangles over a quad's four vertices in counter-clockwise
// order, split along the shorter diagonal (which keeps a heightfield's
// creases following its ridges rather than one fixed direction).
func (b *Builder) Quad(a, c, d, e uint32) {
	pa, pc, pd, pe := b.positions[a], b.positions[c], b.positions[d], b.positions[e]
	if pa.Sub(pd).LenSqr() <= pc.Sub(pe).LenSqr() {
		b.Tri(a, c, d)
		b.Tri(a, d, e)
		return
	}
	b.Tri(a, c, e)
	b.Tri(c, d, e)
}

// SoupTri adds a triangle from three loose corners. Weld afterwards to share
// them.
func (b *Builder) SoupTri(p0, p1, p2 mgl32.Vec3) {
	i := b.Vertex(p0, mgl32.Vec3{}, mgl32.Vec2{})
	j := b.Vertex(p1, mgl32.Vec3{}, mgl32.Vec2{})
	k := b.Vertex(p2, mgl32.Vec3{}, mgl32.Vec2{})
	b.Tri(i, j, k)
}

// TriangleCount is how many triangles have been added.
func (b *Builder) TriangleCount() int { return len(b.indices) / 3 }

// VertexCount is how many vertices there are now.
func (b *Builder) VertexCount() int { return len(b.positions) }

// WeldTolerance is the default weld distance: a tenth of a millimetre, well
// under any generator's sampling and well over float noise at a kilometre.
const WeldTolerance = 1e-4

// Weld merges vertices whose positions are within tol (WeldTolerance when
// <= 0) of each other — and, with keepUVs, whose UVs are too (so a UV seam
// stays split). Degenerate triangles left behind are dropped. A supplied
// normal is kept from the first vertex of each merged group; Normals then
// rederives any that are zero.
func (b *Builder) Weld(tol float32, keepUVs bool) {
	if tol <= 0 {
		tol = WeldTolerance
	}
	inv := 1 / float64(tol)
	type key struct {
		x, y, z int64
		u, v    int64
	}
	q := func(f float32) int64 { return int64(math.Round(float64(f) * inv)) }
	index := map[key]uint32{}
	remap := make([]uint32, len(b.positions))
	var pos []mgl32.Vec3
	var nrm []mgl32.Vec3
	var uvs []mgl32.Vec2
	for i, p := range b.positions {
		k := key{x: q(p[0]), y: q(p[1]), z: q(p[2])}
		if keepUVs {
			k.u, k.v = q(b.uvs[i][0]), q(b.uvs[i][1])
		}
		if j, ok := index[k]; ok {
			remap[i] = j
			continue
		}
		j := uint32(len(pos))
		index[k] = j
		remap[i] = j
		pos = append(pos, p)
		nrm = append(nrm, b.normals[i])
		uvs = append(uvs, b.uvs[i])
	}
	var idx []uint32
	var slots []int32
	for t := 0; t+2 < len(b.indices); t += 3 {
		a, c, d := remap[b.indices[t]], remap[b.indices[t+1]], remap[b.indices[t+2]]
		if a == c || c == d || a == d {
			continue
		}
		idx = append(idx, a, c, d)
		slots = append(slots, b.triSlot[t/3])
	}
	b.positions, b.normals, b.uvs, b.indices, b.triSlot = pos, nrm, uvs, idx, slots
}

// Normals fills every zero normal with the area-weighted average of its
// triangles' facet normals (the cross product's length is twice the area, so
// summing unnormalised crosses weights by area for free).
func (b *Builder) Normals() {
	acc := make([]mgl32.Vec3, len(b.positions))
	for t := 0; t+2 < len(b.indices); t += 3 {
		a, c, d := b.indices[t], b.indices[t+1], b.indices[t+2]
		n := b.positions[c].Sub(b.positions[a]).Cross(b.positions[d].Sub(b.positions[a]))
		acc[a] = acc[a].Add(n)
		acc[c] = acc[c].Add(n)
		acc[d] = acc[d].Add(n)
	}
	for i := range b.normals {
		if b.normals[i] != (mgl32.Vec3{}) {
			continue
		}
		if l := acc[i].Len(); l > 0 {
			b.normals[i] = acc[i].Mul(1 / l)
		} else {
			b.normals[i] = mgl32.Vec3{0, 1, 0}
		}
	}
}

// Bounds is the box the vertices span.
func (b *Builder) Bounds() (lo, hi mgl32.Vec3) {
	for i, p := range b.positions {
		if i == 0 {
			lo, hi = p, p
			continue
		}
		for k := 0; k < 3; k++ {
			lo[k] = min(lo[k], p[k])
			hi[k] = max(hi[k], p[k])
		}
	}
	return
}

// Mesh is the result: normals derived where missing, a default slot when
// none was named. The builder's slices are copied, so it can go on building.
func (b *Builder) Mesh() *model.ImportMesh {
	b.Normals()
	slots := b.slots
	if len(slots) == 0 {
		slots = []model.ImportSlot{{Name: "default", BaseColor: [4]float32{1, 1, 1, 1}}}
	}
	m := &model.ImportMesh{
		Positions: append([]mgl32.Vec3(nil), b.positions...),
		Normals:   append([]mgl32.Vec3(nil), b.normals...),
		UVs:       append([]mgl32.Vec2(nil), b.uvs...),
		Indices:   append([]uint32(nil), b.indices...),
		TriSlot:   append([]int32(nil), b.triSlot...),
		Slots:     append([]model.ImportSlot(nil), slots...),
	}
	return m
}

// OpenEdges counts edges used by exactly one triangle, by POSITION (within
// WeldTolerance) rather than by vertex, so a UV seam does not count — zero
// for a closed, watertight mesh, which is what a terrain tile must be for nav
// to treat it as one solid.
func (b *Builder) OpenEdges() int {
	inv := 1 / float64(WeldTolerance)
	type pk struct{ x, y, z int64 }
	ids := map[pk]int{}
	id := func(v uint32) int {
		p := b.positions[v]
		k := pk{int64(math.Round(float64(p[0]) * inv)), int64(math.Round(float64(p[1]) * inv)), int64(math.Round(float64(p[2]) * inv))}
		n, ok := ids[k]
		if !ok {
			n = len(ids)
			ids[k] = n
		}
		return n
	}
	type edge struct{ a, c int }
	count := map[edge]int{}
	for t := 0; t+2 < len(b.indices); t += 3 {
		for k := 0; k < 3; k++ {
			a, c := id(b.indices[t+k]), id(b.indices[t+(k+1)%3])
			if a > c {
				a, c = c, a
			}
			count[edge{a, c}]++
		}
	}
	n := 0
	for _, c := range count {
		if c == 1 {
			n++
		}
	}
	return n
}
