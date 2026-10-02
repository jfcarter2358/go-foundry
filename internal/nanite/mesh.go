// Package nanite is the Nanite mesh asset: a dense mesh imported from glTF,
// OBJ or .fmdl and COOKED once into a binary .fnm file the runtime loads as
// is — its vertices welded into a pool, its triangles cut into clusters
// (internal/cluster) and laid out cluster by cluster, its bounds, and a signed
// distance grid GI traces it by (internal/meshsdf). Phase 1 of the Nanite plan
// (docs/nanite/PHASE_1.md); later phases add the cluster DAG, collision data
// and paging to the same file, behind its version number.
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
	"foundry/internal/meshsdf"

	"github.com/go-gl/mathgl/mgl32"
)

// Ext is a cooked Nanite mesh's extension.
const Ext = ".fnm"

// FileVersion is the cooked format's version, in every file's header. A file
// of another version is refused and must be re-cooked.
const FileVersion = 1

// CookVersion is folded into every source hash, so a change to how a cook is
// made — not just to what is stored — re-cooks every mesh. Bump it whenever
// the same source would now cook differently.
const CookVersion = 1

const magic = "FNM\x00"

// headerSize is magic, version and source hash.
const headerSize = 4 + 4 + 32

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
	// First and Count are the slot's clusters in Mesh.Clusters: a slot's
	// clusters are contiguous, so a draw group is one run of them.
	First, Count int32
}

// Cluster is a cluster of the mesh: internal/cluster's, with its triangles
// in Mesh.Indices (TriFirst counts triangles from the start of the whole list).
type Cluster = cluster.Cluster

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
	// layout — and Normals 3 a vertex.
	Vertices []float32
	Normals  []float32
	// Indices is the triangle list in cluster order.
	Indices  []uint32
	Clusters []Cluster

	// SDF is the mesh's signed distance grid, for GI. Nil for a mesh too thin
	// to have one.
	SDF *meshsdf.Grid

	// Triangles is how many the source had (degenerates and all).
	Triangles int
}

// VertexCount is the vertices in the pool.
func (m *Mesh) VertexCount() int { return len(m.Vertices) / 5 }

// TriangleCount is the triangles drawn.
func (m *Mesh) TriangleCount() int { return len(m.Indices) / 3 }

// Validate checks the mesh is internally consistent: what Read does before
// handing a file to anything that would index with it.
func (m *Mesh) Validate() error {
	nv := m.VertexCount()
	if len(m.Vertices)%5 != 0 || len(m.Normals) != nv*3 {
		return fmt.Errorf("vertex pool ragged: %d floats, %d normal floats", len(m.Vertices), len(m.Normals))
	}
	if len(m.Indices)%3 != 0 {
		return fmt.Errorf("%d indices is not whole triangles", len(m.Indices))
	}
	for i, ix := range m.Indices {
		if int(ix) >= nv {
			return fmt.Errorf("index %d is %d, past the %d-vertex pool", i, ix, nv)
		}
	}
	next := uint32(0)
	for ci := range m.Clusters {
		c := &m.Clusters[ci]
		if c.TriFirst != next || c.TriCount == 0 {
			return fmt.Errorf("cluster %d does not follow on (first %d, want %d)", ci, c.TriFirst, next)
		}
		next += c.TriCount
	}
	if int(next) != m.TriangleCount() {
		return fmt.Errorf("clusters cover %d of %d triangles", next, m.TriangleCount())
	}
	at := int32(0)
	for si, s := range m.Slots {
		if s.First != at || s.Count < 0 {
			return fmt.Errorf("slot %d's clusters do not follow on", si)
		}
		at += s.Count
	}
	if int(at) != len(m.Clusters) {
		return fmt.Errorf("slots cover %d of %d clusters", at, len(m.Clusters))
	}
	if g := m.SDF; g != nil && len(g.Data) != g.N*g.N*g.N {
		return fmt.Errorf("SDF holds %d of %d voxels", len(g.Data), g.N*g.N*g.N)
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

// SlotTriangles is slot si's triangle range in Indices.
func (m *Mesh) SlotTriangles(si int) (first, count uint32) {
	s := &m.Slots[si]
	if s.Count == 0 {
		return 0, 0
	}
	a := &m.Clusters[s.First]
	b := &m.Clusters[s.First+s.Count-1]
	return a.TriFirst, b.TriFirst + b.TriCount - a.TriFirst
}

// ---------- the file ----------

// Encode writes the mesh as a .fnm: the header, then the rest flate-compressed.
func (m *Mesh) Encode(w io.Writer) error {
	var hdr [headerSize]byte
	copy(hdr[:4], magic)
	binary.LittleEndian.PutUint32(hdr[4:], FileVersion)
	copy(hdr[8:], m.SourceHash[:])
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	zw, err := flate.NewWriter(w, flate.BestSpeed)
	if err != nil {
		return err
	}
	if err := gob.NewEncoder(zw).Encode(m); err != nil {
		return err
	}
	return zw.Close()
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

// Decode reads a .fnm.
func Decode(r io.Reader) (*Mesh, error) {
	var hdr [headerSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, fmt.Errorf("not a cooked Nanite mesh: %w", err)
	}
	if string(hdr[:4]) != magic {
		return nil, fmt.Errorf("not a cooked Nanite mesh")
	}
	if v := binary.LittleEndian.Uint32(hdr[4:]); v != FileVersion {
		return nil, fmt.Errorf("cooked by format version %d; this build reads %d — re-cook it (foundry nanite cook)", v, FileVersion)
	}
	var m Mesh
	if err := gob.NewDecoder(flate.NewReader(r)).Decode(&m); err != nil {
		return nil, fmt.Errorf("decoding cooked mesh: %w", err)
	}
	if !bytes.Equal(m.SourceHash[:], hdr[8:]) {
		return nil, fmt.Errorf("cooked mesh header and body disagree")
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("cooked mesh is damaged: %w", err)
	}
	return &m, nil
}

// Read loads a .fnm from disk.
func Read(path string) (*Mesh, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m, err := Decode(bufio.NewReader(f))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return m, nil
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
