package world

// static_clusters.go — a compiled level's surfaces, grouped for drawing and cut
// into clusters. Phase 1 of the Nanite plan (docs/nanite/PHASE_1.md).
//
// The renderer draws a compiled level out of one vertex buffer per GROUP —
// the surfaces sharing a source chunk, cover set, material and sidedness
// (render/static_batch.go). The GPU-driven path (render/nanite_cull.go) culls
// those groups a CLUSTER at a time: ~128 neighbouring triangles with a box, a
// sphere and a normal cone (internal/cluster). Cutting a level into clusters
// is a few hundred milliseconds of work for a big one, so it is done once, by
// the compiler, and the cut is carried in the .fbsp beside the surfaces
// (levelio.CompiledLevel.Clusters).
//
// The grouping is written HERE, once, so the compiler that cuts the clusters
// and the renderer that draws them cannot disagree about which triangles a
// group's buffer holds or in what order: both call StaticGroups. Even so, the
// runtime never trusts a cut blindly — each group's StaticClusterGroup carries
// a signature over its member surfaces and their triangle counts, and a group
// whose signature does not match (a .fbsp written by an older build, or a
// grouping the Smooth Lighting setting changed) is simply cut again at load.
//
// # Levels of detail
//
// Since Phase 2 (docs/nanite/PHASE_2.md) the compiler gives each large opaque
// group a cluster DAG (internal/cluster/dag.go) instead of a flat cut: every
// level of detail of the group, down to a root, which the GPU cull chooses
// from per cluster. A compiled surface is cut on a world grid for the light
// bake (compile.SubdivideFaces), so a floor is a dense grid of faces whose
// baked light varies smoothly over most of it and sharply at a shadow's edge:
// the simplification weighs the seven-float light record (gfx.LightStride)
// with the normals and UVs, so the even stretches collapse to a few
// triangles and the shadow edges stay (StaticLODWeights). The group's corner
// stream is a soup — every face its own corners — so corners identical in
// every float (position, UV, normal, light) are welded first, and the DAG
// indexes the first of each such set: coarse triangles draw from the same
// buffer as the full detail. A DAG is built only by the compiler; a group
// cut again at load gets the flat cut, as in Phase 1.

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"sort"
	"strings"

	"foundry/internal/cluster"
	"foundry/internal/geom"
	"foundry/internal/gfx"
)

// StaticGroupKey is what compiled surfaces must share to be drawn together.
type StaticGroupKey struct {
	Chunk string
	// Cover is the surface's CoveredBy, joined: a cover surface shows and
	// hides on other chunks than its own, so it cannot share a group that is
	// skipped or kept whole by chunk.
	Cover       string
	Mat         gfx.Material
	DoubleSided bool
}

// StaticSurfaceKey is surface surf's group key.
func StaticSurfaceKey(surf *StaticSurface) StaticGroupKey {
	g := &surf.Geometry
	light := g.BakedLightData()
	return StaticGroupKey{
		Chunk: surf.Chunk,
		Cover: strings.Join(surf.CoveredBy, "\x00"),
		Mat: gfx.Material{Texture: g.Texture, MaterialName: g.MaterialName,
			BlendTexture: g.BlendTexture, BlendMask: g.BlendMask,
			Alpha: g.Alpha, Normals: len(g.Normals) > 0, Smooth: g.Smooth, Baked: len(light) > 0},
		DoubleSided: surf.DoubleSided,
	}
}

// ID is the key as a stable string, which is how a stored cut names its group.
func (k StaticGroupKey) ID() string {
	var b strings.Builder
	b.WriteString(k.Chunk)
	b.WriteByte(0x1f)
	b.WriteString(k.Cover)
	b.WriteByte(0x1f)
	m := &k.Mat
	for _, s := range []string{m.Texture, m.MaterialName, m.BlendTexture, m.BlendMask} {
		b.WriteString(s)
		b.WriteByte(0x1f)
	}
	var f [4]byte
	binary.LittleEndian.PutUint32(f[:], math.Float32bits(m.Alpha))
	b.Write(f[:])
	for _, on := range []bool{m.Normals, m.Smooth, m.Baked, k.DoubleSided} {
		if on {
			b.WriteByte('1')
		} else {
			b.WriteByte('0')
		}
	}
	return b.String()
}

// StaticGroup is one group: its key and its surfaces, in buffer order.
type StaticGroup struct {
	Key      StaticGroupKey
	Surfaces []int32
}

// StaticGroups groups s's drawable surfaces (those with vertices), in order of
// first appearance, each group's surfaces sorted along a Morton curve through
// their centres — the order the renderer lays them out in its buffer, so that
// any run of them has a tight box. groupOf is each surface's group, or -1.
func StaticGroups(s *StaticGeometry) (groups []StaticGroup, groupOf []int32) {
	groupOf = make([]int32, len(s.Surfaces))
	index := map[StaticGroupKey]int{}
	for i := range s.Surfaces {
		surf := &s.Surfaces[i]
		if len(surf.Geometry.Vertices) == 0 {
			groupOf[i] = -1
			continue
		}
		key := StaticSurfaceKey(surf)
		gi, ok := index[key]
		if !ok {
			gi = len(groups)
			index[key] = gi
			groups = append(groups, StaticGroup{Key: key})
		}
		groupOf[i] = int32(gi)
		groups[gi].Surfaces = append(groups[gi].Surfaces, int32(i))
	}
	for gi := range groups {
		SortSurfacesSpatially(s, groups[gi].Surfaces, StaticSortMin)
	}
	return groups, groupOf
}

// StaticSortMin is the group size below which StaticGroups leaves surfaces in
// file order: too few to be worth sorting (and the renderer's 32-surface
// culling runs are one run anyway).
const StaticSortMin = 32

// SortSurfacesSpatially orders surfaces along a Morton (Z-order) curve through
// their centres, so that surfaces next to each other in the list are next to
// each other in space. Lists no longer than minLen are left alone.
func SortSurfacesSpatially(s *StaticGeometry, surfaces []int32, minLen int) {
	if len(surfaces) <= minLen {
		return
	}
	lo, hi := s.Surfaces[surfaces[0]].Lo, s.Surfaces[surfaces[0]].Hi
	for _, si := range surfaces {
		lo, hi = geom.VecMin(lo, s.Surfaces[si].Lo), geom.VecMax(hi, s.Surfaces[si].Hi)
	}
	ext := hi.Sub(lo)
	codes := make(map[int32]uint64, len(surfaces))
	for _, si := range surfaces {
		c := s.Surfaces[si].Lo.Add(s.Surfaces[si].Hi).Mul(0.5)
		var q [3]uint32
		for a := 0; a < 3; a++ {
			if ext[a] > 0 {
				q[a] = uint32((c[a] - lo[a]) / ext[a] * 2097151) // 21 bits
			}
		}
		codes[si] = spreadBits21(q[0]) | spreadBits21(q[1])<<1 | spreadBits21(q[2])<<2
	}
	sort.SliceStable(surfaces, func(a, b int) bool { return codes[surfaces[a]] < codes[surfaces[b]] })
}

// spreadBits21 puts the low 21 bits of v two zero bits apart, for a 63-bit
// Morton code.
func spreadBits21(v uint32) uint64 {
	x := uint64(v) & 0x1fffff
	x = (x | x<<32) & 0x1f00000000ffff
	x = (x | x<<16) & 0x1f0000ff0000ff
	x = (x | x<<8) & 0x100f00f00f00f00f
	x = (x | x<<4) & 0x10c30c30c30c30c3
	x = (x | x<<2) & 0x1249249249249249
	return x
}

// StaticClusterGroup is one group's cut, as the compiled file carries it.
type StaticClusterGroup struct {
	// Key is the group's StaticGroupKey.ID().
	Key string
	// Sig is StaticGroupSignature of the group the cut was made from.
	Sig uint64
	// Order and Clusters are the cut (cluster.Result) over the group's
	// triangle stream: its surfaces' triangles, surface by surface in buffer
	// order, each surface's in its own vertex order. Empty when DAG is set.
	Order    []uint32
	Clusters []cluster.Cluster
	// DAG is the group's cluster DAG, when the compiler built one.
	DAG *StaticGroupDAG
}

// StaticGroupDAG is a group's cluster DAG (see the file comment).
type StaticGroupDAG struct {
	// Indices are every level's triangles, cluster by cluster, as corner
	// numbers in the group's corner stream (its surfaces' corners, surface
	// by surface in buffer order).
	Indices  []uint32
	Clusters []cluster.LODCluster
	// BaseOrder is, per level-0 triangle as laid out, the triangle of the
	// stream it is: how the PVS's visible surfaces find their clusters.
	BaseOrder []uint32
	Levels    int32
	// The tree over it (cluster.BuildLODTree).
	Nodes       []cluster.LODNode
	ClusterLeaf []int32
	Leaves      int32
}

// Triangles is how many triangles the stream has (level 0's).
func (d *StaticGroupDAG) Triangles() int { return len(d.BaseOrder) }

// StaticDAGMin is the fewest triangles a group needs to be given a DAG: below
// it there is too little to simplify for a level to be worth its clusters.
const StaticDAGMin = 256

// StaticLODWeights weigh a compiled surface's attributes in the DAG's
// simplification, in metres per unit of the attribute: a corner whose baked
// light is off by 0.05 costs as much as a surface moved 5 cm, so a shadow
// edge on a floor keeps its triangles until it is a few hundred metres off,
// while an evenly lit stretch, or one lit by a smooth gradient, costs nothing
// to collapse.
var StaticLODWeights = struct{ UV, Normal, Light float32 }{UV: 0.25, Normal: 0.25, Light: 1}

// StaticLOD switches the compiler's DAGs (FOUNDRY_NANITE_LOD_STATIC=0: the
// flat cut of Phase 1 for every group).
var StaticLOD = gfx.EnvFlag("FOUNDRY_NANITE_LOD_STATIC", true)

// StaticClusterOptions are the limits a compiled level is cut with. The
// vertex limit binds first on a corner soup, which is what a compiled
// surface's buffer is: 255 corners is 85 triangles.
var StaticClusterOptions = cluster.Options{MaxTriangles: cluster.DefaultMaxTriangles, MaxVertices: cluster.MaxVerticesLimit}

// StaticGroupSignature hashes what a cut of the group depends on: the key, and
// each member surface's index and triangle count, in order.
func StaticGroupSignature(s *StaticGeometry, g *StaticGroup) uint64 {
	h := fnv.New64a()
	h.Write([]byte(g.Key.ID()))
	var b [8]byte
	for _, si := range g.Surfaces {
		binary.LittleEndian.PutUint32(b[:4], uint32(si))
		binary.LittleEndian.PutUint32(b[4:], uint32(len(s.Surfaces[si].Geometry.Vertices)/15))
		h.Write(b[:])
	}
	return h.Sum64()
}

// StaticGroupPositions is the group's triangle stream as positions, three
// corners a triangle — what the cut is made over.
func StaticGroupPositions(s *StaticGeometry, g *StaticGroup) []float32 {
	n := 0
	for _, si := range g.Surfaces {
		n += len(s.Surfaces[si].Geometry.Vertices)
	}
	out := make([]float32, 0, n)
	for _, si := range g.Surfaces {
		out = append(out, s.Surfaces[si].Geometry.Vertices...)
	}
	return out
}

// CutStaticGroup cuts one group into clusters: a flat cut, as at load.
func CutStaticGroup(s *StaticGeometry, g *StaticGroup) StaticClusterGroup {
	pos := StaticGroupPositions(s, g)
	cut := cluster.Build
	if g.Key.DoubleSided {
		// Two coplanar copies of every face, whose depth tie is resolved by
		// draw order: cut in order, so the order is the one it always was.
		cut = cluster.BuildSequential
	}
	r := cut(cluster.Mesh{Positions: pos, Stride: 5}, StaticClusterOptions)
	return StaticClusterGroup{Key: g.Key.ID(), Sig: StaticGroupSignature(s, g), Order: r.Order, Clusters: r.Clusters}
}

// BuildStaticGroupDAG builds one group's cluster DAG, or nil when it gets the
// flat cut: a double-sided group (whose coplanar copies settle their depth
// tie by draw order, which only the flat in-order cut keeps), one not opaque
// (drawn sorted, not culled), or one too small to simplify.
//
// seams, when set, are the positions another group also has (StaticSeams):
// they are locked, so where this group meets a neighbour — another chunk's
// floor, another material's wall — every level keeps the corners the
// neighbour draws there, and no cut of either can open a crack between them.
func BuildStaticGroupDAG(s *StaticGeometry, g *StaticGroup, seams map[[3]uint32]bool) *StaticGroupDAG {
	if !StaticLOD || g.Key.DoubleSided || g.Key.Mat.Alpha < 1 || triangleCount(s, g) < StaticDAGMin {
		return nil
	}
	// The corner stream, a corner a row of 15 floats: position, UV, normal,
	// baked light — zeros for what the group does not carry.
	const stride = 3 + 2 + 3 + gfx.LightStride
	var rows []float32
	for _, si := range g.Surfaces {
		geo := &s.Surfaces[si].Geometry
		light := geo.BakedLightData()
		n := len(geo.Vertices) / 5
		for c := 0; c < n; c++ {
			rows = append(rows, geo.Vertices[5*c:5*c+5]...)
			if len(geo.Normals) >= 3*(c+1) {
				rows = append(rows, geo.Normals[3*c:3*c+3]...)
			} else {
				rows = append(rows, 0, 0, 0)
			}
			if len(light) >= gfx.LightStride*(c+1) {
				rows = append(rows, light[gfx.LightStride*c:gfx.LightStride*(c+1)]...)
			} else {
				rows = append(rows, make([]float32, gfx.LightStride)...)
			}
		}
	}
	corners := len(rows) / stride
	// Corners equal in every float are one vertex: the first of them.
	first := make(map[[stride]uint32]uint32, corners)
	idx := make([]uint32, corners)
	for c := 0; c < corners; c++ {
		var k [stride]uint32
		for f := 0; f < stride; f++ {
			k[f] = math.Float32bits(rows[c*stride+f])
		}
		v, ok := first[k]
		if !ok {
			v = uint32(c)
			first[k] = v
		}
		idx[c] = v
	}
	w := StaticLODWeights
	weights := []float32{w.UV, w.UV, w.Normal, w.Normal, w.Normal}
	for i := 0; i < gfx.LightStride; i++ {
		weights = append(weights, w.Light)
	}
	var locked func(uint32) bool
	if len(seams) > 0 {
		locked = func(v uint32) bool {
			o := int(v) * stride
			return seams[[3]uint32{math.Float32bits(rows[o]), math.Float32bits(rows[o+1]), math.Float32bits(rows[o+2])}]
		}
	}
	d := cluster.BuildDAG(cluster.Mesh{Positions: rows, Stride: stride, Indices: idx},
		cluster.DAGOptions{Cluster: StaticClusterOptions, AttrWeights: weights, Locked: locked})
	if d.Levels < 2 {
		return nil
	}
	nodes, leafOf := cluster.BuildLODTree(d.Clusters, [][2]int32{{0, int32(len(d.Clusters))}})
	out := &StaticGroupDAG{Indices: d.Indices, Clusters: d.Clusters, BaseOrder: d.BaseOrder, Levels: int32(d.Levels),
		Nodes: nodes, ClusterLeaf: leafOf}
	for _, n := range nodes {
		out.Leaves = max(out.Leaves, n.Leaf+1)
	}
	return out
}

// StaticSeams is the positions, bit for bit, that more than one of groups
// has a corner at: where a group meets a neighbour it is drawn beside but
// simplified apart from. Every group counts — a small, double-sided or
// see-through one is drawn at full detail, so a neighbour must keep its
// corners for it too.
func StaticSeams(s *StaticGeometry, groups []StaticGroup) map[[3]uint32]bool {
	owner := map[[3]uint32]int32{}
	seams := map[[3]uint32]bool{}
	for gi := range groups {
		for _, si := range groups[gi].Surfaces {
			v := s.Surfaces[si].Geometry.Vertices
			for c := 0; c+5 <= len(v); c += 5 {
				k := [3]uint32{math.Float32bits(v[c]), math.Float32bits(v[c+1]), math.Float32bits(v[c+2])}
				if o, ok := owner[k]; !ok {
					owner[k] = int32(gi)
				} else if o != int32(gi) {
					seams[k] = true
				}
			}
		}
	}
	return seams
}

// BuildStaticClusters cuts every group of s — what the compiler stores: a
// DAG for each group that takes one (BuildStaticGroupDAG), the flat cut for
// the rest.
func BuildStaticClusters(s *StaticGeometry) []StaticClusterGroup {
	if s == nil {
		return nil
	}
	groups, _ := StaticGroups(s)
	seams := StaticSeams(s, groups)
	out := make([]StaticClusterGroup, len(groups))
	for gi := range groups {
		if d := BuildStaticGroupDAG(s, &groups[gi], seams); d != nil {
			out[gi] = StaticClusterGroup{Key: groups[gi].Key.ID(), Sig: StaticGroupSignature(s, &groups[gi]), DAG: d}
			continue
		}
		out[gi] = CutStaticGroup(s, &groups[gi])
	}
	return out
}

// StaticClustersFor returns the stored cut for group g if there is one whose
// signature matches, or cuts it afresh. stored is reported so a caller can say
// how much of the level's cut it had to redo.
func (s *StaticGeometry) StaticClustersFor(g *StaticGroup) (c StaticClusterGroup, stored bool) {
	id := g.Key.ID()
	if s.clusterIndex == nil && len(s.Clusters) > 0 {
		s.clusterIndex = make(map[string]int, len(s.Clusters))
		for i := range s.Clusters {
			s.clusterIndex[s.Clusters[i].Key] = i
		}
	}
	if i, ok := s.clusterIndex[id]; ok {
		cg := s.Clusters[i]
		n := len(cg.Order)
		if cg.DAG != nil {
			n = cg.DAG.Triangles()
		}
		if cg.Sig == StaticGroupSignature(s, g) && n == triangleCount(s, g) {
			return cg, true
		}
	}
	return CutStaticGroup(s, g), false
}

func triangleCount(s *StaticGeometry, g *StaticGroup) int {
	n := 0
	for _, si := range g.Surfaces {
		n += len(s.Surfaces[si].Geometry.Vertices) / 15
	}
	return n
}

// SetClusters installs a stored cut (the compiled file's), replacing any
// before it.
func (s *StaticGeometry) SetClusters(c []StaticClusterGroup) {
	s.Clusters = c
	s.clusterIndex = nil
}
