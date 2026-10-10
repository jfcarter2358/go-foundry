// Package cluster cuts a triangle mesh into meshlets — the ~128-triangle
// clusters the Nanite work culls, draws and (from Phase 2) simplifies. Phase 1
// of the Nanite plan (docs/nanite/PHASE_1.md).
//
// A cluster is a run of triangles that are next to each other on the surface:
// small enough that culling it whole wastes little (a cluster straddling the
// frustum's edge draws at most a cluster's worth of triangles nobody sees),
// large enough that a level of a few hundred thousand triangles is a few
// thousand clusters, which is what a compute pass tests in well under a
// millisecond. Each one carries what a culling pass needs to throw it away
// without looking at a triangle:
//
//   - a BOX and a SPHERE around its corners, for the frustum and for the
//     occlusion test against the Hi-Z pyramid;
//   - a NORMAL CONE — an axis, a half-angle and an apex — that says from where
//     every triangle in it faces away. A camera inside the cone's back region
//     sees only back faces, and a pass that culls back faces anyway can skip
//     the cluster.
//
// # How the cut is made
//
// Greedy growth, the same shape as meshoptimizer's builder. A cluster starts
// at the first unused triangle in a Morton (Z-order) walk of the triangles'
// centres, and grows by taking, each step, the frontier triangle — one that
// shares a corner POSITION with the cluster — that adds the fewest new
// vertices, ties going to the one nearest the cluster's centre. Adjacency is by
// position rather than by index so that a corner soup (a compiled level's
// surfaces, where no two faces share a vertex record) still grows across the
// edges its faces meet at. When the frontier runs dry the next unused triangle
// in Morton order is taken instead, provided it does not blow the cluster's box
// up: Morton neighbours are spatial neighbours nearly always, which is what
// keeps a cluster of disconnected faces compact.
//
// The limits are on triangles and on DISTINCT vertex indices, so a cluster's
// vertices fit an 8-bit local index list, which is the shape the software
// rasteriser and the page format of later phases want. Phase 1 draws through
// the global index buffer and does not need the vertex limit, but honours it so
// the clusters it caches stay valid when those phases arrive.
//
// Everything is deterministic: the same mesh always cuts the same way, which is
// what lets a compiled level carry its clusters and the runtime trust them.

package cluster

import (
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// Default limits: 128 triangles over at most 128 distinct vertices. The usual
// meshlet shape — a closed surface meshes at roughly two triangles per vertex,
// so the two limits bind at about the same time.
const (
	DefaultMaxTriangles = 128
	DefaultMaxVertices  = 128
	// MaxVerticesLimit is the most vertices a cluster may ever be given: its
	// local indices are bytes.
	MaxVerticesLimit = 255
)

// NoCone is the ConeCutoff of a cluster whose triangles face too many ways to
// share a back side. No dot product reaches it, so the cone test never culls.
const NoCone = 2

// Options are the cut's limits. Zero values take the defaults.
type Options struct {
	MaxTriangles int
	MaxVertices  int
}

func (o Options) resolve() Options {
	if o.MaxTriangles <= 0 {
		o.MaxTriangles = DefaultMaxTriangles
	}
	if o.MaxVertices <= 0 {
		o.MaxVertices = DefaultMaxVertices
	}
	if o.MaxVertices > MaxVerticesLimit {
		o.MaxVertices = MaxVerticesLimit
	}
	// A triangle needs three vertices of room, however the limits were set.
	if o.MaxVertices < 3 {
		o.MaxVertices = 3
	}
	return o
}

// Cluster is one meshlet: a run of Result.Order and its bounds.
type Cluster struct {
	// TriFirst and TriCount are the cluster's slice of Result.Order — in
	// triangles, not indices.
	TriFirst, TriCount uint32
	// Vertices is how many distinct vertex indices its triangles use.
	Vertices uint32
	// Lo and Hi bound every corner; Center and Radius are a sphere around
	// them (the box's centre, out to the furthest corner).
	Lo, Hi mgl32.Vec3
	Center mgl32.Vec3
	Radius float32
	// The normal cone. Every triangle's front faces away from an eye for which
	//
	//	dot(normalize(ConeApex - eye), ConeAxis) >= ConeCutoff
	//
	// ConeCutoff is the sine of the cone's half-angle, or NoCone.
	ConeApex   mgl32.Vec3
	ConeAxis   mgl32.Vec3
	ConeCutoff float32
}

// BackfacingFrom reports whether the cone puts every triangle of the cluster
// with its back to eye — what the GPU cull asks (nanite_cull_comp.glsl).
func (c *Cluster) BackfacingFrom(eye mgl32.Vec3) bool {
	if c.ConeCutoff >= 1 {
		return false
	}
	d := c.ConeApex.Sub(eye)
	l := d.Len()
	if l < 1e-12 {
		return false
	}
	return d.Dot(c.ConeAxis) >= c.ConeCutoff*l
}

// Result is a mesh's cut.
type Result struct {
	// Order is the triangles in cluster order: Order[k] is the source triangle
	// (index into the input's triangle list) that goes k-th. Every source
	// triangle appears exactly once.
	Order    []uint32
	Clusters []Cluster
}

// Reorder returns indices laid out in Order, three per triangle, so each
// cluster is a contiguous run [3*TriFirst, 3*(TriFirst+TriCount)) of it.
func (r *Result) Reorder(indices []uint32) []uint32 {
	out := make([]uint32, 0, len(r.Order)*3)
	for _, t := range r.Order {
		if indices == nil {
			out = append(out, 3*t, 3*t+1, 3*t+2)
			continue
		}
		out = append(out, indices[3*t], indices[3*t+1], indices[3*t+2])
	}
	return out
}

// Mesh is the cut's input: positions, stride floats per vertex with x, y, z
// first, and a triangle list into them — or nil Indices for a corner soup,
// where triangle t is vertices 3t, 3t+1 and 3t+2.
type Mesh struct {
	Positions []float32
	Stride    int
	Indices   []uint32
}

func (m *Mesh) stride() int {
	if m.Stride < 3 {
		return 3
	}
	return m.Stride
}

// VertexCount is the vertices Positions holds.
func (m *Mesh) VertexCount() int { return len(m.Positions) / m.stride() }

// TriangleCount is the triangles the mesh holds.
func (m *Mesh) TriangleCount() int {
	if m.Indices != nil {
		return len(m.Indices) / 3
	}
	return m.VertexCount() / 3
}

// Corner is the vertex index of triangle t's k-th corner.
func (m *Mesh) Corner(t, k int) uint32 {
	if m.Indices != nil {
		return m.Indices[3*t+k]
	}
	return uint32(3*t + k)
}

// Position is vertex v's position.
func (m *Mesh) Position(v uint32) mgl32.Vec3 {
	s := int(v) * m.stride()
	return mgl32.Vec3{m.Positions[s], m.Positions[s+1], m.Positions[s+2]}
}

// Build cuts m into clusters.
func Build(m Mesh, opt Options) Result {
	opt = opt.resolve()
	nt := m.TriangleCount()
	if nt == 0 {
		return Result{}
	}
	nv := m.VertexCount()

	// Per triangle: centre and box.
	cent := make([]mgl32.Vec3, nt)
	triBox := make([][2]mgl32.Vec3, nt)
	for t := 0; t < nt; t++ {
		a, b, c := m.Position(m.Corner(t, 0)), m.Position(m.Corner(t, 1)), m.Position(m.Corner(t, 2))
		cent[t] = a.Add(b).Add(c).Mul(1.0 / 3)
		triBox[t] = [2]mgl32.Vec3{vmin(vmin(a, b), c), vmax(vmax(a, b), c)}
	}

	// Position ids: corners at bit-identical positions share one, so the
	// adjacency below crosses between faces of a corner soup.
	posID := make([]int32, nv)
	{
		ids := make(map[[3]uint32]int32, nv)
		for v := 0; v < nv; v++ {
			p := m.Position(uint32(v))
			k := [3]uint32{math.Float32bits(p[0]), math.Float32bits(p[1]), math.Float32bits(p[2])}
			id, ok := ids[k]
			if !ok {
				id = int32(len(ids))
				ids[k] = id
			}
			posID[v] = id
		}
	}
	npos := 0
	for _, id := range posID {
		npos = max(npos, int(id)+1)
	}
	// Triangles around each position, CSR.
	adjStart := make([]int32, npos+1)
	for t := 0; t < nt; t++ {
		for k := 0; k < 3; k++ {
			adjStart[posID[m.Corner(t, k)]+1]++
		}
	}
	for i := 1; i <= npos; i++ {
		adjStart[i] += adjStart[i-1]
	}
	adj := make([]int32, adjStart[npos])
	fill := append([]int32(nil), adjStart[:npos]...)
	for t := 0; t < nt; t++ {
		for k := 0; k < 3; k++ {
			p := posID[m.Corner(t, k)]
			adj[fill[p]] = int32(t)
			fill[p]++
		}
	}

	// The Morton walk.
	morton := mortonOrder(cent)

	// How far from its centre a full cluster's triangles reach, roughly: a
	// patch of MaxTriangles triangles of the mesh's mean edge is about
	// sqrt(MaxTriangles/2) edges across. A frontier triangle much further out
	// than that is the cluster wrapping round one already cut, and it is
	// better closed early than strung round the gap.
	var edgeSum float64
	for t := 0; t < nt; t++ {
		a, b := m.Position(m.Corner(t, 0)), m.Position(m.Corner(t, 1))
		edgeSum += float64(b.Sub(a).Len())
	}
	reach := float32(1.25 * math.Sqrt(float64(opt.MaxTriangles)/2) * edgeSum / float64(nt))
	maxDiag2 := 4 * reach * reach

	used := make([]bool, nt)
	// vertStamp[v] == cluster number + 1 while v is in the cluster being grown.
	vertStamp := make([]int32, nv)
	// candStamp marks triangles already on the current frontier.
	candStamp := make([]int32, nt)

	res := Result{Order: make([]uint32, 0, nt)}
	cursor := 0
	clusterNo := int32(0)
	var frontier []int32
	var tris []int32

	newVerts := func(t int32) int {
		n := 0
		for k := 0; k < 3; k++ {
			v := m.Corner(int(t), k)
			if vertStamp[v] != clusterNo {
				// A triangle repeating a vertex counts it once.
				dup := false
				for j := 0; j < k; j++ {
					if m.Corner(int(t), j) == v {
						dup = true
					}
				}
				if !dup {
					n++
				}
			}
		}
		return n
	}

	for {
		for cursor < nt && used[morton[cursor]] {
			cursor++
		}
		if cursor >= nt {
			break
		}
		clusterNo++
		tris = tris[:0]
		frontier = frontier[:0]
		verts := 0
		var lo, hi mgl32.Vec3
		var sum mgl32.Vec3

		add := func(t int32) {
			used[t] = true
			verts += newVerts(t)
			for k := 0; k < 3; k++ {
				v := m.Corner(int(t), k)
				vertStamp[v] = clusterNo
				p := m.Position(v)
				if len(tris) == 0 && k == 0 {
					lo, hi = p, p
				} else {
					lo, hi = vmin(lo, p), vmax(hi, p)
				}
				for _, nb := range adj[adjStart[posID[v]]:adjStart[posID[v]+1]] {
					if !used[nb] && candStamp[nb] != clusterNo {
						candStamp[nb] = clusterNo
						frontier = append(frontier, nb)
					}
				}
			}
			sum = sum.Add(cent[t])
			tris = append(tris, t)
		}

		add(morton[cursor])
		for len(tris) < opt.MaxTriangles {
			centre := sum.Mul(1 / float32(len(tris)))
			best, bestNew, bestDist := -1, 4, float32(math.MaxFloat32)
			w := 0
			for _, t := range frontier {
				if used[t] {
					continue
				}
				frontier[w] = t
				w++
				d := cent[t].Sub(centre)
				dd := d.Dot(d)
				// A triangle that closes a fan (no new vertex) first;
				// otherwise the nearest, which keeps the cluster round
				// rather than strung out along the walk. The costlier
				// checks only for a triangle that would win.
				nn := newVerts(t)
				closes := min(nn, 1)
				if !(closes < bestNew || (closes == bestNew && dd < bestDist)) {
					continue
				}
				if verts+nn > opt.MaxVertices {
					continue
				}
				if len(tris) >= 8 {
					tb := &triBox[t]
					if d := vmax(hi, tb[1]).Sub(vmin(lo, tb[0])); d.Dot(d) > maxDiag2 {
						continue
					}
				}
				best, bestNew, bestDist = int(t), closes, dd
			}
			frontier = frontier[:w]
			if best >= 0 {
				add(int32(best))
				continue
			}
			// Nothing on the frontier fits: the next triangle along the
			// Morton walk, if it keeps the cluster compact.
			next := -1
			for c := cursor; c < nt; c++ {
				if !used[morton[c]] {
					next = int(morton[c])
					break
				}
			}
			if next < 0 || verts+newVerts(int32(next)) > opt.MaxVertices {
				break
			}
			if len(tris) >= 8 {
				nlo, nhi := lo, hi
				for k := 0; k < 3; k++ {
					p := m.Position(m.Corner(next, k))
					nlo, nhi = vmin(nlo, p), vmax(nhi, p)
				}
				if d := nhi.Sub(nlo); d.Dot(d) > maxDiag2 || d.Len() > 2*hi.Sub(lo).Len()+1e-6 {
					break
				}
			}
			add(int32(next))
		}

		first := uint32(len(res.Order))
		for _, t := range tris {
			res.Order = append(res.Order, uint32(t))
		}
		c := Cluster{TriFirst: first, TriCount: uint32(len(tris)), Vertices: uint32(verts)}
		bound(&m, res.Order[first:], &c)
		res.Clusters = append(res.Clusters, c)
	}
	return res
}

// BuildSequential cuts m into clusters WITHOUT reordering: each cluster is the
// next run of the triangle list, as long as the limits allow. For geometry
// whose draw order is part of its look — a double-sided surface, whose two
// coplanar copies resolve their depth tie by which is drawn last — where the
// greedy cut's reordering would flip the tie. Bounds and cones are Build's.
func BuildSequential(m Mesh, opt Options) Result {
	opt = opt.resolve()
	nt := m.TriangleCount()
	res := Result{Order: make([]uint32, nt)}
	for t := range res.Order {
		res.Order[t] = uint32(t)
	}
	seen := map[uint32]bool{}
	start := 0
	flush := func(end int) {
		if end <= start {
			return
		}
		c := Cluster{TriFirst: uint32(start), TriCount: uint32(end - start), Vertices: uint32(len(seen))}
		bound(&m, res.Order[start:end], &c)
		res.Clusters = append(res.Clusters, c)
		start = end
		clear(seen)
	}
	for t := 0; t < nt; t++ {
		fresh := 0
		for k := 0; k < 3; k++ {
			v := m.Corner(t, k)
			if !seen[v] {
				fresh++
			}
		}
		if t-start >= opt.MaxTriangles || len(seen)+fresh > opt.MaxVertices {
			flush(t)
		}
		for k := 0; k < 3; k++ {
			seen[m.Corner(t, k)] = true
		}
	}
	flush(nt)
	return res
}

// bound fills c's box, sphere and cone from its triangles.
func bound(m *Mesh, tris []uint32, c *Cluster) {
	first := true
	for _, t := range tris {
		for k := 0; k < 3; k++ {
			p := m.Position(m.Corner(int(t), k))
			if first {
				c.Lo, c.Hi, first = p, p, false
			} else {
				c.Lo, c.Hi = vmin(c.Lo, p), vmax(c.Hi, p)
			}
		}
	}
	c.Center = c.Lo.Add(c.Hi).Mul(0.5)
	var r2 float32
	for _, t := range tris {
		for k := 0; k < 3; k++ {
			d := m.Position(m.Corner(int(t), k)).Sub(c.Center)
			r2 = max(r2, d.Dot(d))
		}
	}
	// A hair over, so a corner exactly on the sphere is never judged outside
	// it by a rounding error in the test.
	c.Radius = float32(math.Sqrt(float64(r2)))*1.0001 + 1e-6
	coneOf(m, tris, c)
}

// coneOf fills the normal cone (see Cluster). Worked in float64: the apex is
// a difference of nearly equal terms for a nearly flat cluster.
//
// With axis a and every triangle normal n_i within angle θ of it, choose the
// apex A behind every triangle's plane: dot(n_i, p_i - A) >= 0 for a point p_i
// on triangle i. Then for an eye e,
//
//	dot(n_i, p_i - e) = dot(n_i, p_i - A) + dot(n_i, A - e) >= dot(n_i, A - e)
//
// which is >= 0 — triangle i faces away — for every n_i in the cone as soon
// as A - e is within 90° - θ of a: dot(normalize(A - e), a) >= sin θ.
func coneOf(m *Mesh, tris []uint32, c *Cluster) {
	c.ConeCutoff = NoCone
	type tri struct {
		n [3]float64
		p [3]float64
	}
	list := make([]tri, 0, len(tris))
	var axis [3]float64
	for _, t := range tris {
		a, b, cc := m.Position(m.Corner(int(t), 0)), m.Position(m.Corner(int(t), 1)), m.Position(m.Corner(int(t), 2))
		u := [3]float64{float64(b[0] - a[0]), float64(b[1] - a[1]), float64(b[2] - a[2])}
		v := [3]float64{float64(cc[0] - a[0]), float64(cc[1] - a[1]), float64(cc[2] - a[2])}
		n := [3]float64{u[1]*v[2] - u[2]*v[1], u[2]*v[0] - u[0]*v[2], u[0]*v[1] - u[1]*v[0]}
		l := math.Sqrt(n[0]*n[0] + n[1]*n[1] + n[2]*n[2])
		if l < 1e-20 {
			// Degenerate: it rasterises to nothing, so it constrains nothing.
			continue
		}
		n = [3]float64{n[0] / l, n[1] / l, n[2] / l}
		list = append(list, tri{n: n, p: [3]float64{float64(a[0]), float64(a[1]), float64(a[2])}})
		axis = [3]float64{axis[0] + n[0], axis[1] + n[1], axis[2] + n[2]}
	}
	l := math.Sqrt(axis[0]*axis[0] + axis[1]*axis[1] + axis[2]*axis[2])
	if len(list) == 0 || l < 1e-9 {
		return
	}
	axis = [3]float64{axis[0] / l, axis[1] / l, axis[2] / l}
	minDot := 1.0
	for _, tr := range list {
		minDot = math.Min(minDot, tr.n[0]*axis[0]+tr.n[1]*axis[1]+tr.n[2]*axis[2])
	}
	// Past about 84° there is next to nowhere the whole cluster faces away
	// from, and the apex would sit at a great distance; not worth a test.
	if minDot <= 0.1 {
		return
	}
	centre := [3]float64{float64(c.Center[0]), float64(c.Center[1]), float64(c.Center[2])}
	t := math.Inf(-1)
	for _, tr := range list {
		d := [3]float64{tr.p[0] - centre[0], tr.p[1] - centre[1], tr.p[2] - centre[2]}
		need := -(tr.n[0]*d[0] + tr.n[1]*d[1] + tr.n[2]*d[2]) / (tr.n[0]*axis[0] + tr.n[1]*axis[1] + tr.n[2]*axis[2])
		t = math.Max(t, need)
	}
	// A little further back than the tightest apex, so float32 rounding in
	// the stored apex can only make the test more conservative.
	t += 1e-4 * (float64(c.Radius) + 1)
	apex := [3]float64{centre[0] - axis[0]*t, centre[1] - axis[1]*t, centre[2] - axis[2]*t}
	sin := math.Sqrt(math.Max(0, 1-minDot*minDot))
	c.ConeApex = mgl32.Vec3{float32(apex[0]), float32(apex[1]), float32(apex[2])}
	c.ConeAxis = mgl32.Vec3{float32(axis[0]), float32(axis[1]), float32(axis[2])}
	// And a slightly narrower cone than measured, for the same reason.
	c.ConeCutoff = float32(math.Min(1, sin+1e-4))
}

// mortonOrder is the triangles sorted along a Z-order curve through their
// centres. Ties keep source order, so the walk is deterministic.
func mortonOrder(cent []mgl32.Vec3) []int32 {
	lo, hi := cent[0], cent[0]
	for _, c := range cent[1:] {
		lo, hi = vmin(lo, c), vmax(hi, c)
	}
	ext := hi.Sub(lo)
	codes := make([]uint64, len(cent))
	for i, c := range cent {
		var q [3]uint32
		for a := 0; a < 3; a++ {
			if ext[a] > 0 {
				f := (c[a] - lo[a]) / ext[a]
				q[a] = uint32(min(max(f, 0), 1) * 2097151) // 21 bits
			}
		}
		codes[i] = spreadBits21(q[0]) | spreadBits21(q[1])<<1 | spreadBits21(q[2])<<2
	}
	order := make([]int32, len(cent))
	for i := range order {
		order[i] = int32(i)
	}
	sort.SliceStable(order, func(a, b int) bool { return codes[order[a]] < codes[order[b]] })
	return order
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

func vmin(a, b mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{min(a[0], b[0]), min(a[1], b[1]), min(a[2], b[2])}
}

func vmax(a, b mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{max(a[0], b[0]), max(a[1], b[1]), max(a[2], b[2])}
}

// Stats summarises a cut, for logs and the cook's report.
type Stats struct {
	Clusters              int
	Triangles             int
	MeanTriangles         float64
	MinTriangles, MaxTris int
	MaxVertices           int
	WithCone              int
}

// Summarise measures r.
func (r *Result) Summarise() Stats {
	s := Stats{Clusters: len(r.Clusters), Triangles: len(r.Order), MinTriangles: math.MaxInt}
	for _, c := range r.Clusters {
		s.MinTriangles = min(s.MinTriangles, int(c.TriCount))
		s.MaxTris = max(s.MaxTris, int(c.TriCount))
		s.MaxVertices = max(s.MaxVertices, int(c.Vertices))
		if c.ConeCutoff < 1 {
			s.WithCone++
		}
	}
	if s.Clusters > 0 {
		s.MeanTriangles = float64(s.Triangles) / float64(s.Clusters)
	} else {
		s.MinTriangles = 0
	}
	return s
}
