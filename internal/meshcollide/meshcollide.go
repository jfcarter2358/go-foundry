// Package meshcollide is the collision a cooked Nanite mesh carries: a fixed
// coarse cut of its cluster DAG, cleaned, with a bounding-volume hierarchy
// over it for rays and probes (the MESH mode), and that cut split into a
// handful of convex hulls (the HULLS mode). Phase 2b of the Nanite plan
// (docs/nanite/PHASE_2b.md).
//
// # Why a cut, and why cooked
//
// The full detail a cooked rock draws is tens of thousands of triangles, and
// collision has no use for the ones a body could never feel: a bump a few
// centimetres tall is under any foot. The DAG (internal/cluster) already
// knows every coarser version of the mesh and how far each strays from the
// full detail, so the collision mesh is simply the cut at a fixed
// world-space error — Settings.Error, a few centimetres — rather than a
// second simplification with its own idea of what matters.
//
// Everything here is work done once per source: the cut is cleaned (welded
// by position, degenerates dropped, its open edges counted), quantised, put
// under a tree, and decomposed into hulls. The cook (internal/nanitecook)
// stores the result in the .fnm beside the render data, keyed by the same
// source hash with these settings folded in, so a change of settings
// re-cooks and nothing else does.
//
// # The modes
//
//	box    the placement's bounding box, as scenery has always collided
//	hulls  a few convex hulls, resolved exactly as brushes are (GJK/EPA)
//	mesh   the triangles themselves, asked the questions a sheet answers
//	       (rays from inside the body, as terrain is)
//	none   nothing
//
// A mesh asks for one (Data.Mode); auto picks hulls for something a body
// could walk around and mesh for something it would walk on — a placement
// under Settings.AutoSize across is hulls, anything bigger is mesh. A
// placement may override either (world.Scenery.Collision).
//
// # The split
//
// This package is pure CPU and imports nothing of the engine's but mgl32: the
// cooked file (internal/nanite) carries a Data, and internal/nanite sits
// below gfx. The runtime that resolves bodies against it is
// internal/collision; navigation (internal/nav) rasterises it too.
package meshcollide

import (
	"strings"

	"github.com/go-gl/mathgl/mgl32"
)

// Mode is how a placement of a mesh collides.
type Mode uint8

const (
	// ModeAuto is hulls under Settings.AutoSize, mesh above (Resolve).
	ModeAuto Mode = iota
	ModeBox
	ModeHulls
	ModeMesh
	ModeNone
)

var modeNames = [...]string{"auto", "box", "hulls", "mesh", "none"}

func (m Mode) String() string {
	if int(m) < len(modeNames) {
		return modeNames[m]
	}
	return "auto"
}

// ModeNames is every mode by name, in order, for a picker.
func ModeNames() []string { return append([]string(nil), modeNames[:]...) }

// ParseMode reads a mode's name; "" is auto.
func ParseMode(s string) (Mode, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ModeAuto, true
	}
	for i, n := range modeNames {
		if s == n {
			return Mode(i), true
		}
	}
	return ModeAuto, false
}

// Settings are a collision cook's knobs. The zero value takes every default.
type Settings struct {
	// Error is the collision cut's world-space error in metres (in the mesh's
	// own units: a placement scaled up scales it too). The default 3 cm is
	// under any foot and well over the bumps a dense scan is made of.
	Error float32
	// MaxHulls caps the decomposition; a mesh that wanted more is cut short
	// and says so (Data.HullCapHit). MaxHullVerts caps each hull, which is
	// what a support-function query costs per call: 128 points hold a
	// sphere to within 3.5% of its radius (inside it), 64 to within 6.5%.
	MaxHulls     int
	MaxHullVerts int
	// Concavity is how far, in metres, a hull may stand proud of the surface
	// it stands for before the piece is split again; ConcavityRel is the same
	// as a fraction of the mesh's diagonal. The larger of the two is used, so
	// a cliff is not asked for centimetre hulls.
	Concavity    float32
	ConcavityRel float32
	// AutoSize is the largest extent, in metres, at which auto picks hulls.
	AutoSize float32
	// Mode is the mesh's own choice; auto unless the cook was told otherwise.
	Mode Mode
}

// The defaults Settings' zero values take.
const (
	DefaultError        = 0.03
	DefaultMaxHulls     = 16
	DefaultMaxHullVerts = 128
	DefaultConcavity    = 0.05
	DefaultConcavityRel = 0.02
	DefaultAutoSize     = 4
)

// Resolved fills in the defaults.
func (s Settings) Resolved() Settings {
	if s.Error <= 0 {
		s.Error = DefaultError
	}
	if s.MaxHulls <= 0 {
		s.MaxHulls = DefaultMaxHulls
	}
	if s.MaxHullVerts < 8 {
		if s.MaxHullVerts <= 0 {
			s.MaxHullVerts = DefaultMaxHullVerts
		} else {
			s.MaxHullVerts = 8
		}
	}
	if s.Concavity <= 0 {
		s.Concavity = DefaultConcavity
	}
	if s.ConcavityRel <= 0 {
		s.ConcavityRel = DefaultConcavityRel
	}
	if s.AutoSize <= 0 {
		s.AutoSize = DefaultAutoSize
	}
	return s
}

// Data is a mesh's cooked collision: the cut, quantised, under its tree, and
// the hulls.
type Data struct {
	// Settings is what it was cooked with, resolved.
	Settings Settings

	// Lo and Hi are the box the vertices are quantised over.
	Lo, Hi mgl32.Vec3
	// Verts is three uint16 a vertex, each the fraction of Lo..Hi on its
	// axis (Dequantize). 16 bits over a 100 m cliff is 1.5 mm.
	Verts []uint16
	// Tris is the cut's triangles, in the tree's leaf order; Tags is each
	// triangle's surface tag (the material slot it came from), one a
	// triangle.
	Tris []uint32
	Tags []uint8
	// TagNames names the tags: TagNames[tag] is the slot's name.
	TagNames []string
	// Nodes is the bounding-volume hierarchy over Tris, root first.
	Nodes []Node

	// Paged says Verts, Tris, Tags and Nodes were taken out (Strip) to be
	// read back when wanted (WithMesh, paged.go); MeshTriangles,
	// MeshVertices and MeshNodes are then their counts.
	Paged                                  bool
	MeshTriangles, MeshVertices, MeshNodes int

	// What the clean found: corners welded into one position, triangles
	// dropped for having no area, edges used by one triangle only (an open
	// border), and edges shared by more than two (non-manifold).
	Welded, Degenerate, OpenEdges, NonManifold int
	// SourceTriangles is the cut before cleaning.
	SourceTriangles int

	// Hulls is the convex decomposition of the cut.
	Hulls []Hull
	// HullCapHit says the decomposition stopped at MaxHulls with pieces still
	// more concave than the tolerance; VertCapped is how many hulls lost
	// vertices to MaxHullVerts. Concavity is the worst piece's, in metres.
	HullCapHit bool
	VertCapped int
	Concavity  float32
	// VertCapError is how far, in metres, a capped hull lies inside the
	// uncapped one at worst.
	VertCapError float32
}

// Node is one node of the tree: a box, and either two children (Count 0: the
// left child is the next node, the right is Right) or a run of triangles
// (Count > 0, from First).
type Node struct {
	Lo, Hi [3]float32
	// Right is the right child of an inner node, or the first triangle of a
	// leaf.
	Right int32
	Count int32
}

// IsLeaf reports whether n holds triangles.
func (n *Node) IsLeaf() bool { return n.Count > 0 }

// Hull is one convex piece: its vertices, its outward triangles over them,
// and the planes it is the intersection of (xyz outward unit normal, w the
// offset: a point p is inside when n·p <= w for every plane).
type Hull struct {
	Pts    []mgl32.Vec3
	Tris   []uint16
	Planes []mgl32.Vec4
	Lo, Hi mgl32.Vec3
}

// VertexCount is the cut's vertices.
func (d *Data) VertexCount() int {
	if d.Paged {
		return d.MeshVertices
	}
	return len(d.Verts) / 3
}

// TriangleCount is the cut's triangles.
func (d *Data) TriangleCount() int {
	if d.Paged {
		return d.MeshTriangles
	}
	return len(d.Tris) / 3
}

// Dequantize is vertex v's position.
func (d *Data) Dequantize(v uint32) mgl32.Vec3 {
	var p mgl32.Vec3
	for k := 0; k < 3; k++ {
		q := float32(d.Verts[3*v+uint32(k)]) / 65535
		p[k] = d.Lo[k] + q*(d.Hi[k]-d.Lo[k])
	}
	return p
}

// Resolve is the mode a placement whose largest extent is size (metres,
// scaled) collides with, given an override (auto for none).
func (d *Data) Resolve(override Mode, size float32) Mode {
	m := override
	if m == ModeAuto && d != nil {
		m = d.Settings.Mode
	}
	if m == ModeAuto {
		auto := float32(DefaultAutoSize)
		if d != nil {
			auto = d.Settings.Resolved().AutoSize
		}
		if size <= auto {
			m = ModeHulls
		} else {
			m = ModeMesh
		}
	}
	// A mode the data cannot serve falls back to the box.
	switch m {
	case ModeHulls:
		if d == nil || len(d.Hulls) == 0 {
			return ModeBox
		}
	case ModeMesh:
		if d == nil || d.TriangleCount() == 0 {
			return ModeBox
		}
	}
	return m
}

// Validate checks the data is internally consistent. A stripped mesh part is
// checked when it is read back (WithMesh).
func (d *Data) Validate() error {
	if d.Paged {
		return d.validateHulls()
	}
	nv := uint32(d.VertexCount())
	if len(d.Verts)%3 != 0 || len(d.Tris)%3 != 0 {
		return errf("collision mesh is ragged")
	}
	if len(d.Tags) != d.TriangleCount() {
		return errf("%d tags for %d collision triangles", len(d.Tags), d.TriangleCount())
	}
	for _, ix := range d.Tris {
		if ix >= nv {
			return errf("collision index %d past %d vertices", ix, nv)
		}
	}
	for _, t := range d.Tags {
		if int(t) >= len(d.TagNames) && len(d.TagNames) > 0 {
			return errf("collision tag %d of %d", t, len(d.TagNames))
		}
	}
	nt := int32(d.TriangleCount())
	for i := range d.Nodes {
		n := &d.Nodes[i]
		if n.IsLeaf() {
			if n.Right < 0 || n.Right+n.Count > nt {
				return errf("collision leaf %d out of range", i)
			}
		} else if int(n.Right) <= i+1 || int(n.Right) >= len(d.Nodes) || i+1 >= len(d.Nodes) {
			return errf("collision node %d's children are out of order", i)
		}
	}
	if nt > 0 && len(d.Nodes) == 0 {
		return errf("collision triangles but no tree")
	}
	return d.validateHulls()
}

func (d *Data) validateHulls() error {
	for hi := range d.Hulls {
		h := &d.Hulls[hi]
		for _, ix := range h.Tris {
			if int(ix) >= len(h.Pts) {
				return errf("hull %d's triangles index past its %d points", hi, len(h.Pts))
			}
		}
		if len(h.Pts) < 4 || len(h.Planes) < 4 {
			return errf("hull %d is not a solid", hi)
		}
	}
	return nil
}

// Bytes is roughly what the data holds in memory (a stripped mesh part
// counts nothing).
func (d *Data) Bytes() int {
	n := len(d.Verts)*2 + len(d.Tris)*4 + len(d.Tags) + len(d.Nodes)*32
	for i := range d.Hulls {
		n += len(d.Hulls[i].Pts)*12 + len(d.Hulls[i].Tris)*2 + len(d.Hulls[i].Planes)*16
	}
	return n
}
