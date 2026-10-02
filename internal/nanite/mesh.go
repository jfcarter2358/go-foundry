// Package nanite is the Nanite mesh asset: a dense mesh imported from glTF,
// OBJ or .fmdl and COOKED once into a binary .fnm file the runtime loads as
// is — its vertices welded into a pool, its triangles cut into clusters
// (internal/cluster) and laid out cluster by cluster, its bounds, and a signed
// distance grid GI traces it by (internal/meshsdf). Phase 1 of the Nanite plan
// (docs/nanite/PHASE_1.md); Phase 2 (docs/nanite/PHASE_2.md) made the clusters
// a DAG — every level of detail down to a root, the tree over it the GPU's
// LOD selection walks, and a fixed coarse cut (the FALLBACK mesh) for the
// CPU's uses. Phase 2b (docs/nanite/PHASE_2b.md) added the mesh's COLLISION
// (internal/meshcollide): a fixed cut of the DAG at a few centimetres'
// error, cleaned and put under a tree, and the convex hulls it decomposes
// into. Phase 3 (docs/nanite/PHASE_3.md) QUANTISED it (quant.go) and cut its
// geometry into fixed-size PAGES (pages.go) that the renderer streams from
// the file while a level plays (render/nanite_stream.go); what a load reads
// up front is only the small part: the clusters' bounds and errors, the LOD
// tree, the page table, the fallback mesh, the distance field and the hulls.
//
// # The file (format 4)
//
//	header   "FNM\x00", the format version, the source hash (40 bytes)
//	length   the metadata's length (8 bytes)
//	metadata the Mesh, its geometry left out, gob-encoded and flate-compressed
//	data     every page's packed bytes, one after another (Page.Offset), then
//	         the collision's mesh part (CollisionOffset), flate-compressed
//
// Read loads the header and the metadata only, and keeps the path: a page is
// read when it is asked for (LoadPage), by one ReadAt. Decode, for a mesh in
// memory, keeps the data section with it. Flat decodes every page into the
// old single pool (Vertices, Normals, Indices) for the CPU paths that want
// the whole mesh — the classic draw path, the tools.
//
// A scenery placement whose model is a .fnm is drawn by the GPU-driven
// cluster cull (render/nanite_scenery.go): every placement of every cooked
// mesh in one shared vertex and index pool, one instance record each, culled
// a cluster at a time and drawn with one indirect multi-draw per material.
//
// # Why cook
//
// Everything here is work the runtime would otherwise redo on every load —
// the weld alone is most of a millisecond for a ten-thousand-triangle model,
// the cut a hundred for a sixty-thousand-triangle one, the distance grid more
// than both — for an answer that only changes when the source does. So it is
// done once, and the cooked file carries the SHA-256 of the source bytes (and
// every sidecar the import read) and of the cook's own version: a cook whose
// hash matches the file already on disk is skipped, and a source that changed
// is re-cooked.
//
// # The split
//
// This package is the cooked FILE and nothing else: its format, its reader
// and writer, and the load cache. The cook itself — importing, welding,
// cutting — is internal/nanitecook, because importing a model reaches
// internal/model and the weld internal/gfx, and the cooked file has readers
// that must not: the release packer (internal/pack) follows a cooked mesh's
// textures out of it, and pack sits below gfx (internal/layering_test.go).
package nanite

import (
	"bufio"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"foundry/internal/cluster"
	"foundry/internal/fileutil"
	"foundry/internal/meshcollide"
	"foundry/internal/meshsdf"

	"github.com/go-gl/mathgl/mgl32"
)

// Ext is a cooked Nanite mesh's extension.
const Ext = ".fnm"

// FileVersion is the cooked format's version, in every file's header. A file
// of another version is refused and must be re-cooked. 2: the cluster DAG.
// 3: collision. 4: quantised, paged geometry (Phase 3).
const FileVersion = 4

// CookVersion is folded into every source hash, so a change to how a cook is
// made — not just to what is stored — re-cooks every mesh. Bump it whenever
// the same source would now cook differently.
const CookVersion = 5

const magic = "FNM\x00"

// headerSize is magic, version and source hash.
const headerSize = 4 + 4 + 32

// prefixSize is the header and the metadata's length.
const prefixSize = headerSize + 8

// Slot is one material slot of a mesh.
type Slot struct {
	Name string
	// Texture is the engine texture name, "" for none (the untextured
	// normal-colour look). A placement may override it by slot name.
	Texture string
	// Smooth asks for the vertex normals regardless of the Smooth Lighting
	// setting (gfx.Material.Smooth). A glTF mesh's are authored, so always.
	Smooth bool
	// DoubleSided is a slot whose back faces are meant to be seen: its
	// clusters carry no normal cone, so the cull never drops them for facing
	// away. Everything but a glTF material saying otherwise is double-sided,
	// because the engine draws models without back-face culling.
	DoubleSided bool
	BaseColor   [4]float32
	// First and Count are the slot's clusters in Mesh.Clusters, every level
	// of them: a slot's clusters are contiguous, so a draw group is one run.
	// The first BaseCount of them are level 0, the full detail.
	First, Count int32
	BaseCount    int32
	// FallbackFirst and FallbackCount are the slot's triangles in
	// Mesh.Fallback.
	FallbackFirst, FallbackCount uint32
}

// Cluster is a cluster of the mesh's DAG: internal/cluster's, with its
// triangles in Mesh.Indices (TriFirst counts triangles from the start of the
// whole list).
type Cluster = cluster.LODCluster

// Mesh is a cooked Nanite mesh.
type Mesh struct {
	// SourceHash is what it was cooked from (see the package comment).
	SourceHash [32]byte
	// Source is the path it was cooked from, relative to resources/ where it
	// could be, for the record.
	Source string

	// Lo/Hi bound the mesh; Center/Radius are a sphere around it.
	Lo, Hi mgl32.Vec3
	Center mgl32.Vec3
	Radius float32

	Slots []Slot
	// Vertices is the pool, 5 floats a vertex (X, Y, Z, U, V) — gfx.Geometry's
	// layout — and Normals 3 a vertex. Indices is the triangle list in
	// cluster order: every level of every slot, so a cluster of any level is
	// one run of it over the same pool. After the cook they are the pages
	// decoded one after another; they are not in the file, and a loaded mesh
	// has them only after Flat (the GPU streams the pages instead).
	Vertices []float32
	Normals  []float32
	Indices  []uint32
	Clusters []Cluster

	// Quant is how the geometry is quantised (quant.go); Pages is the
	// geometry in pages (pages.go), PoolVertices their vertices together.
	Quant        Quant
	Pages        []Page
	PoolVertices int32
	// ClosedSolid is Closed() of the full detail, worked out by the cook so
	// a mesh whose geometry is on disk still knows it.
	ClosedSolid bool
	// CollisionOffset/CollisionSize are the collision's mesh part in the
	// data section (0 size: none).
	CollisionOffset uint64
	CollisionSize   uint32
	// Levels is how many levels the DAG has (1: no simplification).
	Levels int32
	// Nodes is the tree over the clusters (cluster.BuildLODTree), root
	// first; ClusterLeaf is each cluster's leaf number, and Leaves how many
	// leaves there are.
	Nodes       []cluster.LODNode
	ClusterLeaf []int32
	Leaves      int32

	// Fallback is the FALLBACK mesh: one fixed cut of the DAG at
	// FallbackError, a mid level, as a triangle list (each slot's run at its
	// FallbackFirst) over its own small pool, FallbackVertices (5 floats a
	// vertex) and FallbackNormals (3) — so it is there without the pages.
	// It is what the CPU uses the mesh for — the editor's picking, the GI
	// distance field's bake — where the full detail would cost without
	// showing.
	Fallback         []uint32
	FallbackError    float32
	FallbackVertices []float32
	FallbackNormals  []float32

	// SDF is the mesh's signed distance grid, for GI. Nil for a mesh too thin
	// to have one.
	SDF *meshsdf.Grid

	// Collision is what a placement collides with in the hulls and mesh
	// modes (internal/meshcollide): the DAG cut at Collision.Settings.Error,
	// in the mesh's own space, and its convex decomposition. Nil only for a
	// mesh with no triangles.
	Collision *meshcollide.Data

	// Triangles is how many the source had (degenerates and all).
	Triangles int

	// Where the data section is: in memory (data, after BuildPages or
	// Decode) or in the file at path from dataOff (after Read). dataPages
	// is where the pages end in it.
	data      []byte
	path      string
	dataOff   int64
	dataPages uint64
	// flatMu guards Flat's filling of the pool on a shared mesh.
	flatMu sync.Mutex
}

// VertexCount is the vertices in the pool (decoded or not).
func (m *Mesh) VertexCount() int {
	if m.Vertices == nil && m.PoolVertices > 0 {
		return int(m.PoolVertices)
	}
	return len(m.Vertices) / 5
}

// HasFlat reports whether the pool is in memory (Flat has run, or the mesh
// was cooked here).
func (m *Mesh) HasFlat() bool { return m.Indices != nil }

// TriangleCount is the triangles of the full detail (level 0).
func (m *Mesh) TriangleCount() int {
	n := 0
	for i := range m.Clusters {
		if m.Clusters[i].Level == 0 {
			n += int(m.Clusters[i].TriCount)
		}
	}
	return n
}

// StoredTriangles is the triangles of every level together.
func (m *Mesh) StoredTriangles() int {
	n := 0
	for i := range m.Clusters {
		n += int(m.Clusters[i].TriCount)
	}
	return n
}

// Validate checks the mesh is internally consistent: what Read does before
// handing a file to anything that would index with it. The pool's indices
// are checked when it is in memory (HasFlat); a page's when it is decoded.
func (m *Mesh) Validate() error {
	if m.HasFlat() {
		nv := len(m.Vertices) / 5
		if len(m.Vertices)%5 != 0 || len(m.Normals) != nv*3 {
			return fmt.Errorf("vertex pool ragged: %d floats, %d normal floats", len(m.Vertices), len(m.Normals))
		}
		if len(m.Indices)%3 != 0 || len(m.Indices)/3 != m.StoredTriangles() {
			return fmt.Errorf("pool holds %d indices for clusters of %d triangles", len(m.Indices), m.StoredTriangles())
		}
		for i, ix := range m.Indices {
			if int(ix) >= nv {
				return fmt.Errorf("index %d is %d, past the %d-vertex pool", i, ix, nv)
			}
		}
	}
	if err := m.validatePages(); err != nil {
		return err
	}
	next := uint32(0)
	for ci := range m.Clusters {
		c := &m.Clusters[ci]
		if c.TriFirst != next || c.TriCount == 0 {
			return fmt.Errorf("cluster %d does not follow on (first %d, want %d)", ci, c.TriFirst, next)
		}
		next += c.TriCount
	}
	at := int32(0)
	fb := uint32(0)
	for si, s := range m.Slots {
		if s.First != at || s.Count < 0 || s.BaseCount < 0 || s.BaseCount > s.Count {
			return fmt.Errorf("slot %d's clusters do not follow on", si)
		}
		for ci := s.First; ci < s.First+s.Count; ci++ {
			if (ci < s.First+s.BaseCount) != (m.Clusters[ci].Level == 0) {
				return fmt.Errorf("slot %d's level-0 clusters are not its first %d", si, s.BaseCount)
			}
		}
		if s.FallbackFirst != fb {
			return fmt.Errorf("slot %d's fallback triangles do not follow on", si)
		}
		fb += s.FallbackCount
		at += s.Count
	}
	if int(at) != len(m.Clusters) {
		return fmt.Errorf("slots cover %d of %d clusters", at, len(m.Clusters))
	}
	if int(fb)*3 != len(m.Fallback) {
		return fmt.Errorf("slots cover %d of %d fallback triangles", fb, len(m.Fallback)/3)
	}
	fnv := len(m.FallbackVertices) / 5
	if len(m.FallbackVertices)%5 != 0 || len(m.FallbackNormals) != fnv*3 {
		return fmt.Errorf("fallback pool ragged")
	}
	for i, ix := range m.Fallback {
		if int(ix) >= fnv {
			return fmt.Errorf("fallback index %d is %d, past the %d-vertex fallback pool", i, ix, fnv)
		}
	}
	if len(m.ClusterLeaf) != len(m.Clusters) {
		return fmt.Errorf("%d cluster leaves for %d clusters", len(m.ClusterLeaf), len(m.Clusters))
	}
	for ci, l := range m.ClusterLeaf {
		if l < 0 || l >= m.Leaves {
			return fmt.Errorf("cluster %d is in leaf %d of %d", ci, l, m.Leaves)
		}
	}
	for ni := range m.Nodes {
		n := &m.Nodes[ni]
		if n.ChildCount > 0 && (n.FirstChild <= int32(ni) || int(n.FirstChild+n.ChildCount) > len(m.Nodes)) {
			return fmt.Errorf("node %d's children %d+%d are out of order", ni, n.FirstChild, n.ChildCount)
		}
		if n.IsLeaf() && (n.ClusterFirst < 0 || int(n.ClusterFirst+n.ClusterCount) > len(m.Clusters) || n.Leaf >= m.Leaves) {
			return fmt.Errorf("leaf node %d is out of range", ni)
		}
		if n.LeafFirst < 0 || n.LeafFirst+n.LeafCount > m.Leaves {
			return fmt.Errorf("node %d's leaves are out of range", ni)
		}
	}
	if len(m.Clusters) > 0 && len(m.Nodes) == 0 {
		return fmt.Errorf("clusters but no tree over them")
	}
	if g := m.SDF; g != nil && len(g.Data) != g.N*g.N*g.N {
		return fmt.Errorf("SDF holds %d of %d voxels", len(g.Data), g.N*g.N*g.N)
	}
	if c := m.Collision; c != nil {
		if err := c.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// SlotIndex is the slot named name, or -1.
func (m *Mesh) SlotIndex(name string) int {
	for i := range m.Slots {
		if m.Slots[i].Name == name {
			return i
		}
	}
	return -1
}

// SlotTriangles is slot si's full-detail (level 0) triangle range in
// Indices: what a placement draws when the GPU's LOD selection is not
// drawing it.
func (m *Mesh) SlotTriangles(si int) (first, count uint32) {
	s := &m.Slots[si]
	if s.BaseCount == 0 {
		return 0, 0
	}
	a := &m.Clusters[s.First]
	b := &m.Clusters[s.First+s.BaseCount-1]
	return a.TriFirst, b.TriFirst + b.TriCount - a.TriFirst
}

// BaseIndices is the full detail, every slot's level 0 one after another
// (the whole of Indices for a mesh with no slots yet).
func (m *Mesh) BaseIndices() []uint32 {
	if len(m.Slots) == 0 {
		return m.Indices
	}
	var out []uint32
	for si := range m.Slots {
		first, count := m.SlotTriangles(si)
		out = append(out, m.Indices[3*first:3*(first+count)]...)
	}
	return out
}

// SlotFallback is slot si's triangles in the fallback mesh.
func (m *Mesh) SlotFallback(si int) []uint32 {
	s := &m.Slots[si]
	return m.Fallback[3*s.FallbackFirst : 3*(s.FallbackFirst+s.FallbackCount)]
}

// ---------- the file ----------

// Encode writes the mesh as a .fnm (see the package comment): the header,
// the metadata, then the pages and the collision's mesh part. The mesh must
// have its pages (BuildPages, or a Decode/Read of a format-4 file).
func (m *Mesh) Encode(w io.Writer) error {
	data, err := m.dataSection()
	if err != nil {
		return err
	}
	// The collision's mesh part goes after the pages, out of the metadata.
	var part meshcollide.MeshPart
	stripped := false
	if c := m.Collision; c != nil && !c.Paged && c.TriangleCount() > 0 {
		part = c.Strip()
		stripped = true
		var pb bytes.Buffer
		zw, _ := flate.NewWriter(&pb, flate.BestSpeed)
		if err := gob.NewEncoder(zw).Encode(part); err != nil {
			c.Restore(part)
			return err
		}
		zw.Close()
		m.CollisionOffset, m.CollisionSize = uint64(len(data)), uint32(pb.Len())
		data = append(data[:len(data):len(data)], pb.Bytes()...)
	} else if c == nil || c.TriangleCount() == 0 {
		m.CollisionOffset, m.CollisionSize = 0, 0
	} else {
		// Already paged out (a loaded mesh written again): carry its bytes.
		blob, err := m.readData(m.CollisionOffset, m.CollisionSize)
		if err != nil {
			return err
		}
		m.CollisionOffset = uint64(len(data))
		data = append(data[:len(data):len(data)], blob...)
	}
	var meta bytes.Buffer
	zw, err := flate.NewWriter(&meta, flate.BestSpeed)
	if err != nil {
		return err
	}
	// The geometry is in the pages, never in the metadata.
	m.flatMu.Lock()
	v, n, ix := m.Vertices, m.Normals, m.Indices
	m.Vertices, m.Normals, m.Indices = nil, nil, nil
	err = gob.NewEncoder(zw).Encode(m)
	m.Vertices, m.Normals, m.Indices = v, n, ix
	m.flatMu.Unlock()
	if stripped {
		m.Collision.Restore(part)
	}
	if err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	var hdr [prefixSize]byte
	copy(hdr[:4], magic)
	binary.LittleEndian.PutUint32(hdr[4:], FileVersion)
	copy(hdr[8:], m.SourceHash[:])
	binary.LittleEndian.PutUint64(hdr[headerSize:], uint64(meta.Len()))
	for _, b := range [][]byte{hdr[:], meta.Bytes(), data} {
		if _, err := w.Write(b); err != nil {
			return err
		}
	}
	return nil
}

// dataSection is the pages' bytes.
func (m *Mesh) dataSection() ([]byte, error) {
	if len(m.Pages) == 0 && len(m.Clusters) > 0 {
		return nil, fmt.Errorf("the mesh has no pages (BuildPages)")
	}
	if m.data != nil {
		return m.data[:m.dataPages:m.dataPages], nil
	}
	return m.readData(0, uint32(m.dataPages))
}

// Write saves the mesh to path, atomically.
func (m *Mesh) Write(path string) error {
	var buf bytes.Buffer
	if err := m.Encode(&buf); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, buf.Bytes())
}

// readPrefix reads and checks a file's header and metadata.
func readPrefix(r io.Reader) (*Mesh, int64, error) {
	var hdr [prefixSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, 0, fmt.Errorf("not a cooked Nanite mesh: %w", err)
	}
	if string(hdr[:4]) != magic {
		return nil, 0, fmt.Errorf("not a cooked Nanite mesh")
	}
	if v := binary.LittleEndian.Uint32(hdr[4:]); v != FileVersion {
		return nil, 0, fmt.Errorf("cooked by format version %d; this build reads %d — re-cook it (foundry nanite cook)", v, FileVersion)
	}
	metaLen := binary.LittleEndian.Uint64(hdr[headerSize:])
	if metaLen > 1<<31 {
		return nil, 0, fmt.Errorf("cooked mesh is damaged: metadata of %d bytes", metaLen)
	}
	var m Mesh
	if err := gob.NewDecoder(flate.NewReader(io.LimitReader(r, int64(metaLen)))).Decode(&m); err != nil {
		return nil, 0, fmt.Errorf("decoding cooked mesh: %w", err)
	}
	if !bytes.Equal(m.SourceHash[:], hdr[8:headerSize]) {
		return nil, 0, fmt.Errorf("cooked mesh header and body disagree")
	}
	m.dataPages = m.PageBytes()
	return &m, prefixSize + int64(metaLen), nil
}

// Decode reads a whole .fnm from r, keeping its data section in memory.
func Decode(r io.Reader) (*Mesh, error) {
	all, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	m, off, err := readPrefix(bytes.NewReader(all))
	if err != nil {
		return nil, err
	}
	if off > int64(len(all)) {
		return nil, fmt.Errorf("cooked mesh is truncated")
	}
	m.data = all[off:]
	if err := m.finishLoad(); err != nil {
		return nil, err
	}
	return m, nil
}

// Read loads a .fnm's header and metadata from disk: its pages stay in the
// file until LoadPage (or Flat) reads them.
func Read(path string) (*Mesh, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m, off, err := readPrefix(bufio.NewReader(f))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	m.path, m.dataOff = path, off
	if info, err := f.Stat(); err == nil {
		end := uint64(off) + m.dataPages
		if c := m.CollisionOffset + uint64(m.CollisionSize); m.CollisionSize > 0 && uint64(off)+c > end {
			end = uint64(off) + c
		}
		if uint64(info.Size()) < end {
			return nil, fmt.Errorf("%s: cooked mesh is truncated", filepath.Base(path))
		}
	}
	if err := m.finishLoad(); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return m, nil
}

// finishLoad checks a mesh just read and attaches its collision's loader.
func (m *Mesh) finishLoad() error {
	if err := m.Validate(); err != nil {
		return fmt.Errorf("cooked mesh is damaged: %w", err)
	}
	if c := m.Collision; c != nil && c.Paged {
		off, size := m.CollisionOffset, m.CollisionSize
		c.SetMeshLoader(func() (meshcollide.MeshPart, error) {
			blob, err := m.readData(off, size)
			if err != nil {
				return meshcollide.MeshPart{}, err
			}
			var part meshcollide.MeshPart
			if err := gob.NewDecoder(flate.NewReader(bytes.NewReader(blob))).Decode(&part); err != nil {
				return meshcollide.MeshPart{}, fmt.Errorf("collision mesh: %w", err)
			}
			return part, nil
		})
	}
	return nil
}

// readData reads size bytes at off in the data section: from memory, or
// from the file — checking first that the file is still the one this mesh
// was read from (a re-cook under a running game is an error, not garbage).
func (m *Mesh) readData(off uint64, size uint32) ([]byte, error) {
	if m.data != nil {
		if off+uint64(size) > uint64(len(m.data)) {
			return nil, fmt.Errorf("cooked mesh data out of range")
		}
		return m.data[off : off+uint64(size)], nil
	}
	if m.path == "" {
		return nil, fmt.Errorf("cooked mesh has no data")
	}
	f, err := os.Open(m.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var hdr [headerSize]byte
	if _, err := f.ReadAt(hdr[:], 0); err != nil {
		return nil, err
	}
	if !bytes.Equal(hdr[8:], m.SourceHash[:]) || binary.LittleEndian.Uint32(hdr[4:]) != FileVersion {
		return nil, fmt.Errorf("%s was re-cooked since it was loaded", filepath.Base(m.path))
	}
	out := make([]byte, size)
	if _, err := f.ReadAt(out, m.dataOff+int64(off)); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(m.path), err)
	}
	return out, nil
}

// LoadPage reads page pi and decodes it: what the renderer's loaders do, off
// the main thread. Safe to call from any goroutine.
func (m *Mesh) LoadPage(pi int) (PageData, error) {
	if pi < 0 || pi >= len(m.Pages) {
		return PageData{}, fmt.Errorf("no page %d", pi)
	}
	p := &m.Pages[pi]
	b, err := m.readData(p.Offset, p.Size)
	if err != nil {
		return PageData{}, err
	}
	return m.decodePage(pi, b)
}

// Flat makes sure the whole pool is in memory — Vertices, Normals, Indices,
// every page decoded in order — for the CPU paths that draw or read the
// full mesh. Once done it stays; safe on a shared mesh.
func (m *Mesh) Flat() error {
	m.flatMu.Lock()
	defer m.flatMu.Unlock()
	if m.Indices != nil {
		return nil
	}
	var verts, normals []float32
	var idx []uint32
	for pi := range m.Pages {
		d, err := m.LoadPage(pi)
		if err != nil {
			return err
		}
		base := uint32(len(verts) / 5)
		for _, ix := range d.Indices {
			idx = append(idx, base+ix)
		}
		verts = append(verts, d.Vertices...)
		normals = append(normals, d.Normals...)
	}
	if idx == nil {
		idx = []uint32{}
	}
	m.Vertices, m.Normals, m.Indices = verts, normals, idx
	return nil
}

// validatePages checks the page table against the clusters.
func (m *Mesh) validatePages() error {
	next := int32(0)
	var end uint64
	var verts int64
	for pi := range m.Pages {
		p := &m.Pages[pi]
		if p.ClusterFirst != next || p.ClusterCount <= 0 {
			return fmt.Errorf("page %d's clusters do not follow on", pi)
		}
		if p.Vertices > PageMaxVertices || p.Triangles > PageMaxTriangles || p.Vertices < 0 || p.Triangles < 0 {
			return fmt.Errorf("page %d holds %d vertices and %d triangles, past a page", pi, p.Vertices, p.Triangles)
		}
		tris := int32(0)
		for c := p.ClusterFirst; c < p.ClusterFirst+p.ClusterCount; c++ {
			if int(c) >= len(m.Clusters) {
				return fmt.Errorf("page %d runs past the clusters", pi)
			}
			tris += int32(m.Clusters[c].TriCount)
			if p.Root && !m.Clusters[c].IsRoot() {
				return fmt.Errorf("root page %d holds a cluster with a parent", pi)
			}
		}
		if tris != p.Triangles {
			return fmt.Errorf("page %d says %d triangles, its clusters hold %d", pi, p.Triangles, tris)
		}
		if p.Offset != end {
			return fmt.Errorf("page %d's bytes do not follow on", pi)
		}
		end += uint64(p.Size)
		for _, pp := range p.Parents {
			if pp < 0 || int(pp) >= len(m.Pages) || int(pp) == pi {
				return fmt.Errorf("page %d's parent %d is out of range", pi, pp)
			}
		}
		next += p.ClusterCount
		verts += int64(p.Vertices)
	}
	if len(m.Pages) > 0 && int(next) != len(m.Clusters) {
		return fmt.Errorf("pages cover %d of %d clusters", next, len(m.Clusters))
	}
	if len(m.Pages) > 0 && verts != int64(m.PoolVertices) {
		return fmt.Errorf("pages hold %d vertices, the mesh says %d", verts, m.PoolVertices)
	}
	for i := range m.Clusters {
		if m.Clusters[i].IsRoot() && len(m.Pages) > 0 && !m.Pages[m.PageOf(int32(i))].Root {
			return fmt.Errorf("root cluster %d is not on a root page", i)
		}
	}
	return nil
}

// ReadHeader reads only a .fnm's version and source hash — what deciding
// whether to re-cook needs.
func ReadHeader(path string) (version uint32, hash [32]byte, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, hash, err
	}
	defer f.Close()
	var hdr [headerSize]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return 0, hash, err
	}
	if string(hdr[:4]) != magic {
		return 0, hash, fmt.Errorf("%s is not a cooked Nanite mesh", path)
	}
	copy(hash[:], hdr[8:])
	return binary.LittleEndian.Uint32(hdr[4:]), hash, nil
}

// IsCookedPath reports whether path names a cooked mesh.
func IsCookedPath(path string) bool { return strings.EqualFold(filepath.Ext(path), Ext) }

// ---------- the runtime's cache ----------

// cache holds every cooked mesh loaded this run, by path, with the file's
// size and time: a level of a hundred placements of one rock reads it once,
// and a re-cook while the editor runs is seen at the next load.
var cache = struct {
	sync.Mutex
	m map[string]cacheEntry
}{m: map[string]cacheEntry{}}

type cacheEntry struct {
	mesh *Mesh
	size int64
	mod  int64
}

// Load is Read through the cache. The mesh is shared: callers must not
// modify it.
func Load(path string) (*Mesh, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	cache.Lock()
	e, ok := cache.m[path]
	cache.Unlock()
	if ok && e.size == info.Size() && e.mod == info.ModTime().UnixNano() {
		return e.mesh, nil
	}
	m, err := Read(path)
	if err != nil {
		return nil, err
	}
	cache.Lock()
	cache.m[path] = cacheEntry{mesh: m, size: info.Size(), mod: info.ModTime().UnixNano()}
	cache.Unlock()
	return m, nil
}
