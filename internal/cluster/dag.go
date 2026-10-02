package cluster

// dag.go — the cluster DAG: every level of detail of a mesh, cut into
// clusters, from the full mesh down to a few clusters at the root. Phase 2 of
// the Nanite plan (docs/nanite/PHASE_2.md), component 2.
//
// # How it is built
//
// Level 0 is the mesh's own clusters (Build). Then, a level at a time:
//
//  1. GROUP the level's clusters: neighbours on the surface, about GroupSize
//     of them together (partition, below — a greedy graph partition over the
//     clusters' shared corners, seeded along a Morton walk so a group is
//     compact).
//  2. MERGE each group's triangles and SIMPLIFY them to about half
//     (simplify.go), with every position the group shares with another group
//     LOCKED. Two neighbouring groups therefore leave their common border
//     exactly as it was, whatever each does inside — and that is the whole
//     of the crack-freedom argument: any mix of a group's clusters and its
//     simplified replacement meets its neighbours on the same edges.
//  3. RE-SPLIT the simplified triangles into new clusters (Build again).
//     They are the next level; the borders that were locked this time are
//     inside a group the next time round, and get simplified then.
//
// until the level is one cluster, or no group could be simplified any
// further. A group whose simplification fails (less than 15% fewer
// triangles: everything it holds is border) is not consumed; its clusters go
// on to the next level as they are, to be grouped differently there.
//
// # Error, and choosing a cut
//
// A group's ERROR is how far its simplified surface may lie from the full
// detail: the larger of what the simplifier reports, what is measured between
// the two surfaces (deviation, below) and every child's own error — so error
// never falls going up the DAG. Its SPHERE bounds every child's: also never
// shrinking going up. Each cluster it made carries (Error, LODCenter,
// LODRadius) = the group's; each cluster it consumed carries the same as its
// PARENT error and sphere. A cluster with no parent is a root (ParentError <
// 0); level 0's error is 0.
//
// A cut is then chosen per cluster, with no traversal and no neighbour in
// sight: draw a cluster when its own error, seen from the eye, is small
// enough, and its parent's is not —
//
//	projected(Error, LOD sphere) <= threshold < projected(ParentError, parent sphere)
//
// where projected is the error over the distance from the eye to the nearest
// point of the sphere. Because both error and sphere only grow going up, that
// projection only grows going up too; so along any path from a level-0
// cluster to a root exactly one cluster passes, and every cluster of a group
// decides alike (they hold the very same numbers). The result is a complete,
// crack-free cut, whatever the eye — which is what the GPU's per-cluster test
// relies on (nanite_cluster_cull_comp.glsl). Cut, below, is the same choice
// with one world-space error threshold for every cluster, for the CPU's
// fixed cuts (the fallback mesh) and the tests.

import (
	"math"
	"runtime"
	"slices"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/go-gl/mathgl/mgl32"
)

// DefaultGroupSize is how many clusters a group gathers: eight clusters of
// ~128 triangles simplify to about four.
const DefaultGroupSize = 8

// DAGOptions are a DAG build's settings.
type DAGOptions struct {
	Cluster   Options
	GroupSize int
	// AttrWeights weighs the floats after x, y, z in a vertex in the
	// simplification (SimplifyOptions.AttrWeights).
	AttrWeights []float32
	// Locked reports vertices that must never move — say, where this mesh
	// meets another one drawn beside it (a cooked mesh's other material
	// slots). Every vertex at a locked one's position is locked with it.
	Locked func(v uint32) bool
	// MaxLevels caps the levels built; 0 means no cap short of the root.
	MaxLevels int
}

// LODCluster is a cluster of the DAG.
type LODCluster struct {
	Cluster
	// Level is the level it was made at: 0 for the mesh's own clusters.
	Level int32
	// Error is the most its surface lies from the full detail, and
	// LODCenter/LODRadius the sphere that error is projected from.
	Error     float32
	LODCenter mgl32.Vec3
	LODRadius float32
	// ParentError is the error of the group that simplified it, < 0 for a
	// root; ParentCenter/ParentRadius that group's sphere.
	ParentError  float32
	ParentCenter mgl32.Vec3
	ParentRadius float32
	// Group is the group that made it (-1 at level 0), Parent the group that
	// consumed it (-1 for a root).
	Group, Parent int32
}

// IsRoot reports whether nothing simplified c further.
func (c *LODCluster) IsRoot() bool { return c.ParentError < 0 }

// LODGroup is one simplification step: the clusters it consumed and made.
type LODGroup struct {
	Level  int32 // the level of the clusters it made
	Error  float32
	Center mgl32.Vec3
	Radius float32
	// Children are the clusters it consumed, Made the clusters it made, as
	// indices into DAG.Clusters.
	Children, Made []int32
	// Triangles in and out.
	TrianglesIn, TrianglesOut int
}

// DAG is a mesh's cluster DAG.
type DAG struct {
	// Indices holds every cluster's triangles, cluster after cluster, in
	// Clusters order; a cluster's TriFirst counts triangles into it.
	Indices []uint32
	// Clusters are in level order, and within a level grouped by the group
	// that consumes them, then by the one that made them — so a group's
	// children are one run, and so are the clusters a group made that
	// another consumed.
	Clusters []LODCluster
	Groups   []LODGroup
	// Levels is how many levels there are (the highest Level + 1).
	Levels int
	// BaseOrder is, for each triangle of level 0 (which is laid out first,
	// so these are Indices' first triangles), the input triangle it is —
	// for a caller that keeps something per input triangle.
	BaseOrder []uint32
}

// BaseTriangles is how many triangles level 0 holds.
func (d *DAG) BaseTriangles() int {
	n := 0
	for i := range d.Clusters {
		if d.Clusters[i].Level == 0 {
			n += int(d.Clusters[i].TriCount)
		}
	}
	return n
}

// Selected reports whether cluster c is in the cut for a world-space error
// threshold: its error within it, its parent's beyond it.
func (c *LODCluster) Selected(threshold float32) bool {
	return c.Error <= threshold && (c.ParentError < 0 || c.ParentError > threshold)
}

// Cut is the clusters in the cut for a world-space error threshold, in
// order: one complete, crack-free version of the mesh whose every part lies
// within threshold of the full detail (as far as its error knows).
func (d *DAG) Cut(threshold float32) []int {
	var out []int
	for i := range d.Clusters {
		if d.Clusters[i].Selected(threshold) {
			out = append(out, i)
		}
	}
	return out
}

// CutIndices is Cut's triangles as one list.
func (d *DAG) CutIndices(threshold float32) []uint32 {
	var out []uint32
	for _, ci := range d.Cut(threshold) {
		c := &d.Clusters[ci]
		out = append(out, d.Indices[3*c.TriFirst:3*(c.TriFirst+c.TriCount)]...)
	}
	return out
}

// LevelError is the largest error any cluster of level l has: a threshold
// that cuts every part of the mesh at level l or coarser where that is
// accurate enough (Cut).
func (d *DAG) LevelError(l int) float32 {
	var e float32
	for i := range d.Clusters {
		if int(d.Clusters[i].Level) == l {
			e = max(e, d.Clusters[i].Error)
		}
	}
	return e
}

// building is a DAG under construction: clusters keep their own triangles
// until the layout at the end.
type building struct {
	m        Mesh
	opt      DAGOptions
	posID    []int32
	clusters []LODCluster
	tris     [][]uint32
	src      [][]uint32 // level 0's input triangles
	groups   []LODGroup
}

// BuildDAG builds m's cluster DAG. m must be indexed or a soup (nil
// Indices); positions are matched bit for bit, so a welded pool simplifies
// across its seams and a soup only within each face.
func BuildDAG(m Mesh, opt DAGOptions) DAG {
	if opt.GroupSize <= 1 {
		opt.GroupSize = DefaultGroupSize
	}
	if m.Indices == nil {
		m.Indices = make([]uint32, m.TriangleCount()*3)
		for i := range m.Indices {
			m.Indices[i] = uint32(i)
		}
	}
	b := &building{m: m, opt: opt}
	b.posID = positionIDs(&m)

	base := Build(m, opt.Cluster)
	current := make([]int32, 0, len(base.Clusters))
	for _, c := range base.Clusters {
		tris := make([]uint32, 0, 3*c.TriCount)
		for _, t := range base.Order[c.TriFirst : c.TriFirst+c.TriCount] {
			tris = append(tris, m.Indices[3*t:3*t+3]...)
		}
		id := b.add(c, 0, 0, c.Center, c.Radius, -1, tris)
		b.src[id] = base.Order[c.TriFirst : c.TriFirst+c.TriCount]
		current = append(current, id)
	}

	maxLevels := opt.MaxLevels
	if maxLevels <= 0 {
		maxLevels = 64
	}
	for level := int32(0); len(current) > 1 && int(level)+1 < maxLevels; level++ {
		groups := b.partition(current)
		// A position two groups share is locked for this level.
		owner := make(map[int32]int32, len(current)*64)
		shared := make(map[int32]bool)
		for gi, g := range groups {
			for _, ci := range g {
				for _, v := range b.tris[ci] {
					p := b.posID[v]
					if o, ok := owner[p]; !ok {
						owner[p] = int32(gi)
					} else if o != int32(gi) {
						shared[p] = true
					}
				}
			}
		}
		locked := func(v uint32) bool {
			return shared[b.posID[v]] || (opt.Locked != nil && opt.Locked(v))
		}
		// The groups simplify independently — each reads the shared vertex
		// pool and the locks, nothing else — so they run in parallel; the
		// results are committed in group order, so the DAG is the same
		// whatever the scheduling.
		results := make([]groupResult, len(groups))
		parallelFor(len(groups), func(gi int) {
			results[gi] = b.simplifyGroup(groups[gi], locked)
		})
		var next []int32
		progress := false
		for gi, g := range groups {
			if !results[gi].ok {
				next = append(next, g...)
				continue
			}
			progress = true
			next = append(next, b.commitGroup(g, level+1, &results[gi])...)
		}
		if !progress {
			break
		}
		current = next
	}
	return b.layout()
}

// add records a cluster and its triangles.
func (b *building) add(c Cluster, level int32, errv float32, centre mgl32.Vec3, radius float32, group int32, tris []uint32) int32 {
	id := int32(len(b.clusters))
	b.clusters = append(b.clusters, LODCluster{Cluster: c, Level: level, Error: errv, LODCenter: centre, LODRadius: radius,
		ParentError: -1, Group: group, Parent: -1})
	b.tris = append(b.tris, tris)
	b.src = append(b.src, nil)
	return id
}

// groupResult is one group's simplification, before it is committed.
type groupResult struct {
	ok       bool
	idx      []uint32 // the simplified triangles
	err      float32
	trisIn   int
	clusters Result
}

// simplifyGroup merges, simplifies and re-splits one group — or reports it
// could not simplify enough to be worth a level. It only reads b, so groups
// run in parallel.
func (b *building) simplifyGroup(g []int32, locked func(uint32) bool) groupResult {
	var idx []uint32
	childErr := float32(0)
	for _, ci := range g {
		idx = append(idx, b.tris[ci]...)
		childErr = max(childErr, b.clusters[ci].Error)
	}
	nt := len(idx) / 3
	res := Simplify(b.m.Positions, b.m.stride(), idx, SimplifyOptions{
		TargetTriangles: nt / 2,
		AttrWeights:     b.opt.AttrWeights,
		Locked:          locked,
	})
	out := len(res.Indices) / 3
	if out == 0 || float64(out) > 0.85*float64(nt) {
		return groupResult{}
	}
	// Never below a child's: error only grows going up.
	errv := max(res.Error, deviation(&b.m, idx, res.Indices), childErr)
	sub := Mesh{Positions: b.m.Positions, Stride: b.m.Stride, Indices: res.Indices}
	return groupResult{ok: true, idx: res.Indices, err: errv, trisIn: nt, clusters: Build(sub, b.opt.Cluster)}
}

// commitGroup records a simplified group and the clusters it made.
func (b *building) commitGroup(g []int32, level int32, r *groupResult) []int32 {
	centre, radius := b.mergeSpheres(g)
	gid := int32(len(b.groups))
	grp := LODGroup{Level: level, Error: r.err, Center: centre, Radius: radius,
		Children: append([]int32(nil), g...), TrianglesIn: r.trisIn, TrianglesOut: len(r.idx) / 3}
	for _, ci := range g {
		c := &b.clusters[ci]
		c.ParentError, c.ParentCenter, c.ParentRadius, c.Parent = r.err, centre, radius, gid
	}
	for _, c := range r.clusters.Clusters {
		tris := make([]uint32, 0, 3*c.TriCount)
		for _, t := range r.clusters.Order[c.TriFirst : c.TriFirst+c.TriCount] {
			tris = append(tris, r.idx[3*t:3*t+3]...)
		}
		grp.Made = append(grp.Made, b.add(c, level, r.err, centre, radius, gid, tris))
	}
	b.groups = append(b.groups, grp)
	return grp.Made
}

// parallelFor runs f(0..n-1) over the machine's cores.
func parallelFor(n int, f func(i int)) {
	workers := min(runtime.GOMAXPROCS(0), n)
	if workers <= 1 {
		for i := 0; i < n; i++ {
			f(i)
		}
		return
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				if i >= n {
					return
				}
				f(i)
			}
		}()
	}
	wg.Wait()
}

// mergeSpheres is a sphere around every LOD sphere of the clusters g.
func (b *building) mergeSpheres(g []int32) (mgl32.Vec3, float32) {
	lo, hi := b.clusters[g[0]].LODCenter, b.clusters[g[0]].LODCenter
	for _, ci := range g {
		c := &b.clusters[ci]
		r := mgl32.Vec3{c.LODRadius, c.LODRadius, c.LODRadius}
		lo = vmin(lo, c.LODCenter.Sub(r))
		hi = vmax(hi, c.LODCenter.Add(r))
	}
	centre := lo.Add(hi).Mul(0.5)
	var radius float32
	for _, ci := range g {
		c := &b.clusters[ci]
		radius = max(radius, c.LODCenter.Sub(centre).Len()+c.LODRadius)
	}
	return centre, radius*1.0001 + 1e-6
}

// partition groups the clusters of a level: neighbours (sharing corner
// positions) together, about GroupSize to a group. Greedy: a group starts at
// the first free cluster along a Morton walk of their centres, and takes,
// each step, the free cluster sharing the most corners with it (ties to the
// nearest), until it is full or has no free neighbour. A group left small
// (under a third full) joins the neighbouring group it shares most with, if
// that one has room for it.
func (b *building) partition(clusters []int32) [][]int32 {
	n := len(clusters)
	// Which clusters touch each position.
	touch := map[int32][]int32{}
	for k, ci := range clusters {
		seen := map[int32]bool{}
		for _, v := range b.tris[ci] {
			p := b.posID[v]
			if !seen[p] {
				seen[p] = true
				touch[p] = append(touch[p], int32(k))
			}
		}
	}
	// Shared-corner counts between clusters (local indices).
	adj := make([]map[int32]int32, n)
	for i := range adj {
		adj[i] = map[int32]int32{}
	}
	for _, ks := range touch {
		for i := 0; i < len(ks); i++ {
			for j := i + 1; j < len(ks); j++ {
				adj[ks[i]][ks[j]]++
				adj[ks[j]][ks[i]]++
			}
		}
	}
	cent := make([]mgl32.Vec3, n)
	for k, ci := range clusters {
		cent[k] = b.clusters[ci].Center
	}
	order := mortonOrder(cent)

	groupOf := make([]int32, n)
	for i := range groupOf {
		groupOf[i] = -1
	}
	var groups [][]int32
	size := b.opt.GroupSize
	for _, seed := range order {
		if groupOf[seed] >= 0 {
			continue
		}
		gid := int32(len(groups))
		members := []int32{seed}
		groupOf[seed] = gid
		score := map[int32]int32{}
		for nb, w := range adj[seed] {
			if groupOf[nb] < 0 {
				score[nb] += w
			}
		}
		for len(members) < size {
			centre := mgl32.Vec3{}
			for _, m := range members {
				centre = centre.Add(cent[m])
			}
			centre = centre.Mul(1 / float32(len(members)))
			best, bestW, bestD := int32(-1), int32(0), float32(math.MaxFloat32)
			for nb, w := range score {
				if groupOf[nb] >= 0 {
					continue
				}
				d := cent[nb].Sub(centre).Len()
				if w > bestW || (w == bestW && (d < bestD || (d == bestD && nb < best))) {
					best, bestW, bestD = nb, w, d
				}
			}
			if best < 0 {
				break
			}
			members = append(members, best)
			groupOf[best] = gid
			delete(score, best)
			for nb, w := range adj[best] {
				if groupOf[nb] < 0 {
					score[nb] += w
				}
			}
		}
		groups = append(groups, members)
	}
	// Small groups into a neighbour with room.
	for gi := range groups {
		g := groups[gi]
		if len(g) == 0 || len(g)*3 >= size {
			continue
		}
		links := map[int32]int32{}
		for _, m := range g {
			for nb, w := range adj[m] {
				if og := groupOf[nb]; og != int32(gi) {
					links[og] += w
				}
			}
		}
		best, bestW := int32(-1), int32(0)
		for og, w := range links {
			if len(groups[og])+len(g) > size+size/2 {
				continue
			}
			if w > bestW || (w == bestW && og < best) {
				best, bestW = og, w
			}
		}
		if best < 0 {
			continue
		}
		for _, m := range g {
			groupOf[m] = best
		}
		groups[best] = append(groups[best], g...)
		groups[gi] = nil
	}
	out := make([][]int32, 0, len(groups))
	for _, g := range groups {
		if len(g) == 0 {
			continue
		}
		ids := make([]int32, len(g))
		for i, k := range g {
			ids[i] = clusters[k]
		}
		out = append(out, ids)
	}
	return out
}

// layout orders the clusters — by level, then by the group that consumes
// them (roots last), then by the group that made them, then as made — and
// lays their triangles out in that order. A run of clusters made and consumed
// by the same groups shares every number the LOD test reads, so it is drawn
// or not as one (BuildLODTree's leaves, the renderer's LOD tiles).
func (b *building) layout() DAG {
	n := len(b.clusters)
	order := make([]int32, n)
	for i := range order {
		order[i] = int32(i)
	}
	parentKey := func(c *LODCluster) int64 {
		if c.Parent < 0 {
			return math.MaxInt64
		}
		return int64(c.Parent)
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, c := &b.clusters[order[i]], &b.clusters[order[j]]
		if a.Level != c.Level {
			return a.Level < c.Level
		}
		if parentKey(a) != parentKey(c) {
			return parentKey(a) < parentKey(c)
		}
		return a.Group < c.Group
	})
	newID := make([]int32, n)
	for k, old := range order {
		newID[old] = int32(k)
	}
	d := DAG{Clusters: make([]LODCluster, n), Groups: b.groups}
	for k, old := range order {
		c := b.clusters[old]
		c.TriFirst = uint32(len(d.Indices) / 3)
		d.Indices = append(d.Indices, b.tris[old]...)
		if c.Level == 0 {
			d.BaseOrder = append(d.BaseOrder, b.src[old]...)
		}
		d.Clusters[k] = c
		d.Levels = max(d.Levels, int(c.Level)+1)
	}
	for gi := range d.Groups {
		g := &d.Groups[gi]
		for i, c := range g.Children {
			g.Children[i] = newID[c]
		}
		for i, c := range g.Made {
			g.Made[i] = newID[c]
		}
		slices.Sort(g.Children)
		slices.Sort(g.Made)
	}
	return d
}

// positionIDs numbers m's distinct positions, bit for bit.
func positionIDs(m *Mesh) []int32 {
	nv := m.VertexCount()
	ids := make(map[[3]uint32]int32, nv)
	out := make([]int32, nv)
	for v := 0; v < nv; v++ {
		p := m.Position(uint32(v))
		k := [3]uint32{math.Float32bits(p[0]), math.Float32bits(p[1]), math.Float32bits(p[2])}
		id, ok := ids[k]
		if !ok {
			id = int32(len(ids))
			ids[k] = id
		}
		out[v] = id
	}
	return out
}

// deviation measures how far apart two versions of one surface lie: the
// furthest any corner of the original is from the simplified triangles, and
// any simplified triangle's centre from the original ones. Brute force, but
// a group is a thousand triangles.
func deviation(m *Mesh, original, simplified []uint32) float32 {
	type tri struct{ a, b, c [3]float64 }
	load := func(idx []uint32) []tri {
		out := make([]tri, 0, len(idx)/3)
		for t := 0; t+2 < len(idx); t += 3 {
			var tr tri
			for k, dst := range []*[3]float64{&tr.a, &tr.b, &tr.c} {
				p := m.Position(idx[t+k])
				*dst = [3]float64{float64(p[0]), float64(p[1]), float64(p[2])}
			}
			out = append(out, tr)
		}
		return out
	}
	orig, simp := load(original), load(simplified)
	if len(simp) == 0 || len(orig) == 0 {
		return 0
	}
	// Each triangle's box, so most are dismissed by a box test.
	boxes := func(set []tri) [][2][3]float64 {
		out := make([][2][3]float64, len(set))
		for i, t := range set {
			for a := 0; a < 3; a++ {
				out[i][0][a] = min(t.a[a], t.b[a], t.c[a])
				out[i][1][a] = max(t.a[a], t.b[a], t.c[a])
			}
		}
		return out
	}
	origBox, simpBox := boxes(orig), boxes(simp)
	nearest := func(p [3]float64, set []tri, box [][2][3]float64) float64 {
		best := math.Inf(1)
		for i := range set {
			var d2 float64
			for a := 0; a < 3; a++ {
				if p[a] < box[i][0][a] {
					d := box[i][0][a] - p[a]
					d2 += d * d
				} else if p[a] > box[i][1][a] {
					d := p[a] - box[i][1][a]
					d2 += d * d
				}
			}
			if d2 >= best {
				continue
			}
			best = min(best, pointTriDist2(p, set[i].a, set[i].b, set[i].c))
			if best == 0 {
				break
			}
		}
		return best
	}
	worst := 0.0
	seen := map[[3]float64]bool{}
	for _, t := range orig {
		for _, p := range [][3]float64{t.a, t.b, t.c} {
			if seen[p] {
				continue
			}
			seen[p] = true
			worst = max(worst, nearest(p, simp, simpBox))
		}
	}
	for _, t := range simp {
		c := scale3(add3(add3(t.a, t.b), t.c), 1.0/3)
		worst = max(worst, nearest(c, orig, origBox))
	}
	return float32(math.Sqrt(worst))
}

// pointTriDist2 is the squared distance from p to the triangle abc
// (Ericson, Real-Time Collision Detection, 5.1.5).
func pointTriDist2(p, a, b, c [3]float64) float64 {
	ab, ac, ap := sub3(b, a), sub3(c, a), sub3(p, a)
	d1, d2 := dot3(ab, ap), dot3(ac, ap)
	if d1 <= 0 && d2 <= 0 {
		return dot3(ap, ap)
	}
	bp := sub3(p, b)
	d3, d4 := dot3(ab, bp), dot3(ac, bp)
	if d3 >= 0 && d4 <= d3 {
		return dot3(bp, bp)
	}
	vc := d1*d4 - d3*d2
	if vc <= 0 && d1 >= 0 && d3 <= 0 {
		v := d1 / (d1 - d3)
		q := sub3(p, add3(a, scale3(ab, v)))
		return dot3(q, q)
	}
	cp := sub3(p, c)
	d5, d6 := dot3(ab, cp), dot3(ac, cp)
	if d6 >= 0 && d5 <= d6 {
		return dot3(cp, cp)
	}
	vb := d5*d2 - d1*d6
	if vb <= 0 && d2 >= 0 && d6 <= 0 {
		w := d2 / (d2 - d6)
		q := sub3(p, add3(a, scale3(ac, w)))
		return dot3(q, q)
	}
	va := d3*d6 - d5*d4
	if va <= 0 && (d4-d3) >= 0 && (d5-d6) >= 0 {
		w := (d4 - d3) / ((d4 - d3) + (d5 - d6))
		q := sub3(p, add3(b, scale3(sub3(c, b), w)))
		return dot3(q, q)
	}
	denom := 1 / (va + vb + vc)
	v, w := vb*denom, vc*denom
	q := sub3(p, add3(a, add3(scale3(ab, v), scale3(ac, w))))
	return dot3(q, q)
}
