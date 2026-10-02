package meshcollide

// paged.go — the mesh mode's half of the data kept out of memory until a body
// comes near. Phase 3 of the Nanite plan (docs/nanite/PHASE_3.md).
//
// The hulls are small — at most MaxHulls of MaxHullVerts points — and every
// hulls-mode placement needs them whenever anything moves near it, so they
// load with the mesh. The mesh mode's cut is not small: tens of thousands of
// triangles under a tree for a cliff, most of a cooked file once the render
// data is paged. So the cooked file keeps it apart (internal/nanite writes it
// after the pages), the Data a load hands out is STRIPPED — its MeshPart
// fields empty, Paged set, the counts kept — and the part is read back
// through the loader the reader attached (SetMeshLoader) by whoever needs the
// triangles: internal/collision, when a body comes within its prefetch reach
// (on a goroutine, ahead of the first query), and navigation's rasteriser.
// Neither keeps it longer than it is used: what is resident is decided by
// proximity, not by having once been loaded.

import (
	"sync"
)

// MeshPart is the mesh mode's data: the cut's quantised vertices, its
// triangles and their tags, and the tree.
type MeshPart struct {
	Verts []uint16
	Tris  []uint32
	Tags  []uint8
	Nodes []Node
}

// loaders holds each stripped Data's loader. A side table rather than a
// field, so a Data stays a plain gob-able value.
var loaders sync.Map // *Data -> func() (MeshPart, error)

// Strip takes the mesh part out of d, keeping its counts, and returns it.
func (d *Data) Strip() MeshPart {
	part := MeshPart{Verts: d.Verts, Tris: d.Tris, Tags: d.Tags, Nodes: d.Nodes}
	if d.Paged {
		return part
	}
	d.MeshTriangles, d.MeshVertices, d.MeshNodes = len(d.Tris)/3, len(d.Verts)/3, len(d.Nodes)
	d.Verts, d.Tris, d.Tags, d.Nodes = nil, nil, nil, nil
	d.Paged = true
	return part
}

// Restore puts a stripped mesh part back (the cook's own copy, after
// writing).
func (d *Data) Restore(p MeshPart) {
	d.Verts, d.Tris, d.Tags, d.Nodes = p.Verts, p.Tris, p.Tags, p.Nodes
	d.Paged = false
}

// SetMeshLoader says where a stripped d's mesh part is read from.
func (d *Data) SetMeshLoader(f func() (MeshPart, error)) { loaders.Store(d, f) }

// MeshResident reports whether d holds its mesh part (it was never
// stripped).
func (d *Data) MeshResident() bool { return !d.Paged }

// WithMesh is d with its mesh part: d itself when it holds it, otherwise a
// copy with the part read through its loader. The copy is the caller's: it is
// dropped when the caller drops it, which is how the part leaves memory.
func (d *Data) WithMesh() (*Data, error) {
	if !d.Paged || d.MeshTriangles == 0 {
		return d, nil
	}
	f, ok := loaders.Load(d)
	if !ok {
		return nil, errf("the collision mesh was paged out and has no loader")
	}
	part, err := f.(func() (MeshPart, error))()
	if err != nil {
		return nil, err
	}
	full := *d
	full.Restore(part)
	if full.TriangleCount() != d.MeshTriangles || full.VertexCount() != d.MeshVertices || len(full.Nodes) != d.MeshNodes {
		return nil, errf("the collision mesh read back is not the one cooked (%d triangles, want %d)", full.TriangleCount(), d.MeshTriangles)
	}
	if err := full.Validate(); err != nil {
		return nil, err
	}
	return &full, nil
}
