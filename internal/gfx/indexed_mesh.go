package gfx

// indexed_mesh.go — IndexedMesh, the indexed, cluster-ready mesh that sits
// beside Geometry's de-indexed triangle soup. Phase 0 of the Nanite plan
// (docs/nanite/PHASE_0.md).
//
// Geometry.Load has always de-indexed: every corner of every triangle became a
// record of its own, so a vertex shared by six triangles was uploaded six times
// and the GPU's post-transform cache never saw the same index twice. That was
// the right shape for the things Load was written for — a brush face is a
// handful of triangles, each corner with its own UV, and the chunk batch
// concatenates whole soups into one buffer (render/chunk_batch.go) — and it is
// still the shape those paths and the compiled-surface batch use.
//
// It is the wrong shape for a model. A model is thousands of triangles over a
// connected surface, and everything the Nanite work builds — meshlets of ~128
// triangles over a bounded set of vertices, the cluster DAG's group borders,
// simplification — is phrased over an index buffer and a vertex pool. So a
// model's corners are WELDED back into one: every corner whose full attribute
// record (position, UV, normal, both baked lights, bone indices and weights)
// is bit-for-bit the same as another's becomes one vertex, and the triangle
// list becomes uint32 indices into the pool.
//
// Welding on the WHOLE record is what makes this visually free. Two corners
// that merely share a position — a UV seam, a crease the smoothing split, a
// hard edge between a sharp face and a smooth one — differ somewhere in the
// record and stay two vertices, so every corner reaches the vertex stage with
// exactly the attributes it reached it with before. The rasteriser sees the
// same triangles in the same order with the same inputs; only the fetch is
// different.
//
// The arrays are parallel, one record per vertex, in the same per-attribute
// layouts Geometry uploads — 5-float [X,Y,Z,U,V], 3-float normals, LightStride
// baked light, 4+4 skin — so the upload code is shared between the two shapes.

import (
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// IndexedModels is whether model geometry (Scenery, entity attachments, the
// model editor's mesh) loads and draws indexed. On by default; the
// FOUNDRY_NANITE_INDEXED=0 environment variable turns it off and puts models
// back on the de-indexed path every other Geometry takes, which is the A/B
// switch for captures and benchmarks (see docs/nanite/PHASE_0.md). Read at
// load time, so changing it affects meshes loaded afterwards.
var IndexedModels = envFlag("FOUNDRY_NANITE_INDEXED", true)

// EnvFlag reads a 0/1 (or false/true, off/on) environment switch, falling
// back to def when it is unset or unreadable. The Nanite switches
// (FOUNDRY_NANITE_*) all read through it.
func EnvFlag(name string, def bool) bool { return envFlag(name, def) }

// envFlag is EnvFlag.
func envFlag(name string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "0", "false", "off", "no":
		return false
	case "1", "true", "on", "yes":
		return true
	}
	return def
}

// IndexedMesh is a vertex pool and a uint32 triangle list into it. Every
// per-vertex array is either nil (the mesh has no such attribute) or exactly
// VertexCount records long.
type IndexedMesh struct {
	Vertices    []float32 // 5 per vertex: X, Y, Z, U, V
	Normals     []float32 // 3 per vertex, or nil
	Light       []float32 // LightStride per vertex, or nil
	LightSmooth []float32 // LightStride per vertex, or nil
	SkinIdx     []float32 // 4 per vertex, or nil
	SkinWt      []float32 // 4 per vertex, or nil
	// Indices is the triangle list, three per triangle. Nil on a corner soup
	// — the input WeldCorners takes — where vertex k is simply corner k.
	Indices []uint32
}

// VertexCount is the number of records in the pool.
func (m *IndexedMesh) VertexCount() int { return len(m.Vertices) / 5 }

// CornerCount is the number of triangle corners: the index count, or for a
// soup the vertex count.
func (m *IndexedMesh) CornerCount() int {
	if m.Indices != nil {
		return len(m.Indices)
	}
	return m.VertexCount()
}

// TriangleCount is CornerCount / 3.
func (m *IndexedMesh) TriangleCount() int { return m.CornerCount() / 3 }

// corner is the vertex the k-th triangle corner reads.
func (m *IndexedMesh) corner(k int) int {
	if m.Indices != nil {
		return int(m.Indices[k])
	}
	return k
}

// Position is vertex v's position.
func (m *IndexedMesh) Position(v int) mgl32.Vec3 {
	return mgl32.Vec3{m.Vertices[v*5], m.Vertices[v*5+1], m.Vertices[v*5+2]}
}

// Validate checks the invariants every consumer relies on: parallel arrays of
// the right length and every index inside the pool.
func (m *IndexedMesh) Validate() error {
	if len(m.Vertices)%5 != 0 {
		return fmt.Errorf("indexed mesh: %d vertex floats is not a whole number of 5-float records", len(m.Vertices))
	}
	n := m.VertexCount()
	for _, a := range []struct {
		name   string
		data   []float32
		stride int
	}{
		{"normals", m.Normals, 3},
		{"light", m.Light, LightStride},
		{"smooth light", m.LightSmooth, LightStride},
		{"bone indices", m.SkinIdx, 4},
		{"bone weights", m.SkinWt, 4},
	} {
		if a.data != nil && len(a.data) != n*a.stride {
			return fmt.Errorf("indexed mesh: %s has %d floats, want %d for %d vertices", a.name, len(a.data), n*a.stride, n)
		}
	}
	if m.CornerCount()%3 != 0 {
		return fmt.Errorf("indexed mesh: %d corners is not a whole number of triangles", m.CornerCount())
	}
	for i, idx := range m.Indices {
		if int(idx) >= n {
			return fmt.Errorf("indexed mesh: index %d at %d is outside the %d-vertex pool", idx, i, n)
		}
	}
	return nil
}

// Bounds is the axis-aligned box of the vertices the triangles use. ok is
// false for an empty mesh.
func (m *IndexedMesh) Bounds() (lo, hi mgl32.Vec3, ok bool) {
	for k := 0; k < m.CornerCount(); k++ {
		p := m.Position(m.corner(k))
		if !ok {
			lo, hi, ok = p, p, true
			continue
		}
		for a := 0; a < 3; a++ {
			lo[a] = min(lo[a], p[a])
			hi[a] = max(hi[a], p[a])
		}
	}
	return lo, hi, ok
}

// ForEachTriangle calls fn with each triangle's three positions, in draw
// order, until fn returns false.
func (m *IndexedMesh) ForEachTriangle(fn func(a, b, c mgl32.Vec3) bool) {
	n := m.CornerCount()
	for k := 0; k+2 < n; k += 3 {
		if !fn(m.Position(m.corner(k)), m.Position(m.corner(k+1)), m.Position(m.corner(k+2))) {
			return
		}
	}
}

// Deindex lays the mesh back out as a corner soup — the shape Geometry has
// always held — with Indices nil. A soup in is a copy out.
func (m *IndexedMesh) Deindex() IndexedMesh {
	n := m.CornerCount()
	out := IndexedMesh{Vertices: make([]float32, 0, n*5)}
	gather := func(src []float32, stride int) []float32 {
		if src == nil {
			return nil
		}
		dst := make([]float32, 0, n*stride)
		for k := 0; k < n; k++ {
			v := m.corner(k)
			dst = append(dst, src[v*stride:(v+1)*stride]...)
		}
		return dst
	}
	out.Vertices = gather(m.Vertices, 5)
	out.Normals = gather(m.Normals, 3)
	out.Light = gather(m.Light, LightStride)
	out.LightSmooth = gather(m.LightSmooth, LightStride)
	out.SkinIdx = gather(m.SkinIdx, 4)
	out.SkinWt = gather(m.SkinWt, 4)
	return out
}

// WeldCorners turns a corner soup (Indices nil: vertex k is corner k) into an
// indexed mesh, merging every set of corners whose whole attribute record is
// bit-for-bit identical. First occurrence wins the slot, so vertices keep the
// order the soup first used them in — which keeps the indices of neighbouring
// triangles close together, the locality the post-transform cache and the
// meshlet builder both want.
//
// Bitwise rather than approximate comparison is deliberate: it is what makes
// the weld exactly invisible. Two corners a rounding error apart differ in the
// picture by that rounding error, and a weld that merged them would be a
// (tiny) visual change; keeping them apart costs one vertex.
//
// An already-indexed mesh is de-indexed first, so this is also a re-weld.
func WeldCorners(soup IndexedMesh) IndexedMesh {
	if soup.Indices != nil {
		soup = soup.Deindex()
	}
	n := soup.VertexCount()
	attrs := []struct {
		src    []float32
		stride int
	}{
		{soup.Vertices, 5},
		{soup.Normals, 3},
		{soup.Light, LightStride},
		{soup.LightSmooth, LightStride},
		{soup.SkinIdx, 4},
		{soup.SkinWt, 4},
	}
	// A ragged soup — an attribute array shorter than the positions — would
	// read out of range below. Geometry.Load never builds one (deindexLight and
	// friends pad), but an attribute that does not cover every corner cannot be
	// welded on, so it is dropped rather than trusted.
	for i := range attrs {
		if attrs[i].src != nil && len(attrs[i].src) != n*attrs[i].stride {
			attrs[i].src = nil
		}
	}

	hashCorner := func(k int) uint64 {
		h := uint64(14695981039346656037)
		for _, a := range attrs {
			if a.src == nil {
				continue
			}
			for _, f := range a.src[k*a.stride : (k+1)*a.stride] {
				h ^= uint64(math.Float32bits(f))
				h *= 1099511628211
			}
		}
		return h
	}
	sameCorner := func(k, j int) bool {
		for _, a := range attrs {
			if a.src == nil {
				continue
			}
			x, y := a.src[k*a.stride:(k+1)*a.stride], a.src[j*a.stride:(j+1)*a.stride]
			for i := range x {
				if math.Float32bits(x[i]) != math.Float32bits(y[i]) {
					return false
				}
			}
		}
		return true
	}

	// Open hashing: head[hash] is the newest soup corner kept with that hash,
	// next[] chains back through older ones. Corners, not output slots, are
	// chained, so the comparison reads the soup directly.
	head := make(map[uint64]int32, n)
	next := make([]int32, n)
	slot := make([]uint32, n) // output vertex of each KEPT soup corner
	out := IndexedMesh{Indices: make([]uint32, n)}
	kept := make([]int32, 0, n)
	for k := 0; k < n; k++ {
		h := hashCorner(k)
		found := int32(-1)
		chain, ok := head[h]
		if ok {
			for j := chain; j >= 0; j = next[j] {
				if sameCorner(k, int(j)) {
					found = j
					break
				}
			}
		}
		if found >= 0 {
			out.Indices[k] = slot[found]
			continue
		}
		if ok {
			next[k] = chain
		} else {
			next[k] = -1
		}
		head[h] = int32(k)
		slot[k] = uint32(len(kept))
		out.Indices[k] = slot[k]
		kept = append(kept, int32(k))
	}

	gather := func(src []float32, stride int) []float32 {
		if src == nil {
			return nil
		}
		dst := make([]float32, 0, len(kept)*stride)
		for _, k := range kept {
			dst = append(dst, src[int(k)*stride:(int(k)+1)*stride]...)
		}
		return dst
	}
	out.Vertices = gather(attrs[0].src, 5)
	out.Normals = gather(attrs[1].src, 3)
	out.Light = gather(attrs[2].src, LightStride)
	out.LightSmooth = gather(attrs[3].src, LightStride)
	out.SkinIdx = gather(attrs[4].src, 4)
	out.SkinWt = gather(attrs[5].src, 4)
	return out
}

// ---------- Geometry's indexed half ----------

// weld replaces the corner soup Load just built with its welded pool and
// index list. Every per-vertex array Load keeps is welded in step, so record
// k of each still describes the same vertex.
func (g *Geometry) weld() {
	m := WeldCorners(IndexedMesh{
		Vertices:    g.Vertices,
		Normals:     g.Normals,
		Light:       g.vertexLight,
		LightSmooth: g.vertexLightSmooth,
		SkinIdx:     g.skinIdx,
		SkinWt:      g.skinWt,
	})
	g.Vertices, g.Normals = m.Vertices, m.Normals
	g.vertexLight, g.vertexLightSmooth = m.Light, m.LightSmooth
	g.skinIdx, g.skinWt = m.SkinIdx, m.SkinWt
	g.Indices = m.Indices
}

// Mesh views the geometry's CPU half as an IndexedMesh — the pool and index
// list for an indexed geometry, the corner soup (Indices nil) otherwise. The
// slices are shared, not copied.
func (g *Geometry) Mesh() IndexedMesh {
	return IndexedMesh{
		Vertices:    g.Vertices,
		Normals:     g.Normals,
		Light:       g.vertexLight,
		LightSmooth: g.vertexLightSmooth,
		SkinIdx:     g.skinIdx,
		SkinWt:      g.skinWt,
		Indices:     g.Indices,
	}
}

// IsIndexed reports whether the geometry holds a pool and an index list
// rather than a corner soup.
func (g *Geometry) IsIndexed() bool { return g.Indices != nil }

// CornerCount is the number of triangle corners the geometry draws: what a
// draw call's count is.
func (g *Geometry) CornerCount() int {
	if g.Indices != nil {
		return len(g.Indices)
	}
	return len(g.Vertices) / 5
}

// ForEachTriangle calls fn with each triangle's three positions, in the
// geometry's own space, until fn returns false. It reads both shapes, which
// is what every CPU walk over a model's triangles — picking, the GI mesh SDF
// — must use instead of striding Vertices by 15.
func (g *Geometry) ForEachTriangle(fn func(a, b, c mgl32.Vec3) bool) {
	m := g.Mesh()
	m.ForEachTriangle(fn)
}

// TriangleVertices is the geometry as a de-indexed [X,Y,Z,U,V] corner list:
// Vertices itself for a soup, a fresh copy laid out per corner for an indexed
// geometry. For the callers that hand a triangle list on to code which
// strides it by 15.
func (g *Geometry) TriangleVertices() []float32 {
	if g.Indices == nil {
		return g.Vertices
	}
	out := make([]float32, 0, len(g.Indices)*5)
	for _, i := range g.Indices {
		out = append(out, g.Vertices[i*5:i*5+5]...)
	}
	return out
}

// DrawTriangles issues the geometry's draw call with its VAO already bound and
// its uniforms already set: glDrawElements for an indexed geometry, the old
// glDrawArrays for a soup. Every draw of a Geometry's own VAO goes through
// here (or DrawTrianglesInstanced) so the two shapes cannot drift apart.
func (g *Geometry) DrawTriangles() {
	if g.Indices != nil && g.Ebo != 0 {
		gl.DrawElements(gl.TRIANGLES, int32(len(g.Indices)), gl.UNSIGNED_INT, nil)
		return
	}
	gl.DrawArrays(gl.TRIANGLES, 0, int32(len(g.Vertices)/5))
}

// DrawTrianglesInstanced is DrawTriangles for count instances starting at
// instance first of whatever per-instance buffers the VAO carries.
func (g *Geometry) DrawTrianglesInstanced(first, count int) {
	if g.Indices != nil && g.Ebo != 0 {
		gl.DrawElementsInstancedBaseInstance(gl.TRIANGLES, int32(len(g.Indices)), gl.UNSIGNED_INT, nil, int32(count), uint32(first))
		return
	}
	gl.DrawArraysInstancedBaseInstance(gl.TRIANGLES, 0, int32(len(g.Vertices)/5), int32(count), uint32(first))
}
