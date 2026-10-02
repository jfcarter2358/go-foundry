package meshcollide

// quickhull.go — the convex hull of a point set.
//
// The decomposition (decompose.go) asks for a great many hulls: one per piece
// it keeps, and two per candidate split plane it scores. So this is Barber,
// Dobkin and Huhdanpaa's quickhull with outside sets — every point is filed
// against one face it lies in front of, and only a face's own points are ever
// asked about again — rather than the simpler "test every point against every
// face" incremental hull, which is quadratic and was the first thing a
// profile of a cliff's decomposition pointed at.
//
// Robustness is bought the usual two ways. Everything runs in float64, so a
// hull of float32 inputs has thirty bits of slack before rounding can flip a
// side test. And a point counts as in front of a face only when it is more
// than eps in front, with eps scaled to the point set's extent: a point that
// close to a face is inside the hull for every purpose collision has, and
// refusing it is what keeps near-coplanar faces from fighting over it and
// leaving a hole in the horizon.
//
// The visible region of a new point is found by walking across shared edges
// from the face it was filed against, never by testing every live face. A
// region found that way is connected by construction, and a connected region
// has a single horizon loop — which is the invariant the new faces are fanned
// from. A visible face the walk cannot reach is left alone; it costs a
// sliver of hull, never a broken one.

import (
	"math"
)

type v3 [3]float64

func (a v3) sub(b v3) v3      { return v3{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func (a v3) add(b v3) v3      { return v3{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }
func (a v3) mul(s float64) v3 { return v3{a[0] * s, a[1] * s, a[2] * s} }
func (a v3) dot(b v3) float64 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
func (a v3) cross(b v3) v3 {
	return v3{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}
func (a v3) len() float64 { return math.Sqrt(a.dot(a)) }

// qhFace is one triangle of the hull under construction, wound so its normal
// points out.
type qhFace struct {
	v       [3]int
	n       v3
	d       float64 // n·p = d on the face
	outside []int   // points filed against this face, all in front of it
	dead    bool
}

func (f *qhFace) dist(p v3) float64 { return f.n.dot(p) - f.d }

// hullResult is a hull as indices into the input: the points that are its
// vertices, and its outward-wound triangles over them.
type hullResult struct {
	verts []int
	tris  [][3]int
}

// quickHull is the convex hull of pts. ok is false when the points span no
// volume — fewer than four, or all on one plane — which the caller must
// handle (a flat piece has no hull to collide with).
func quickHull(pts []v3) (hullResult, bool) {
	if len(pts) < 4 {
		return hullResult{}, false
	}
	// eps from the extent: a millionth of the set's size, floored so a tiny
	// set near the origin does not ask for exact arithmetic.
	lo, hi := pts[0], pts[0]
	for _, p := range pts[1:] {
		for k := 0; k < 3; k++ {
			lo[k] = math.Min(lo[k], p[k])
			hi[k] = math.Max(hi[k], p[k])
		}
	}
	ext := hi.sub(lo).len()
	if ext <= 0 {
		return hullResult{}, false
	}
	eps := math.Max(ext*1e-7, 1e-9)

	// The initial simplex: the two points farthest apart along an axis, the
	// point farthest from their line, the point farthest from that plane.
	var i0, i1 int
	best := -1.0
	for k := 0; k < 3; k++ {
		mn, mx := 0, 0
		for i, p := range pts {
			if p[k] < pts[mn][k] {
				mn = i
			}
			if p[k] > pts[mx][k] {
				mx = i
			}
		}
		if d := pts[mx][k] - pts[mn][k]; d > best {
			best, i0, i1 = d, mn, mx
		}
	}
	if best <= eps {
		return hullResult{}, false
	}
	line := pts[i1].sub(pts[i0])
	i2, best := -1, 0.0
	for i, p := range pts {
		if d := line.cross(p.sub(pts[i0])).len(); d > best {
			best, i2 = d, i
		}
	}
	if i2 < 0 || best/line.len() <= eps {
		return hullResult{}, false
	}
	pn := line.cross(pts[i2].sub(pts[i0]))
	pn = pn.mul(1 / pn.len())
	i3, best := -1, 0.0
	for i, p := range pts {
		if d := math.Abs(pn.dot(p.sub(pts[i0]))); d > best {
			best, i3 = d, i
		}
	}
	if i3 < 0 || best <= eps {
		return hullResult{}, false
	}

	h := qhBuilder{pts: pts, eps: eps, edge: map[[2]int]int{}}
	simplex := [4]int{i0, i1, i2, i3}
	centre := pts[i0].add(pts[i1]).add(pts[i2]).add(pts[i3]).mul(0.25)
	for _, f := range [4][3]int{{0, 1, 2}, {0, 3, 1}, {1, 3, 2}, {0, 2, 3}} {
		a, b, c := simplex[f[0]], simplex[f[1]], simplex[f[2]]
		// Wind each face away from the simplex's centre.
		n := pts[b].sub(pts[a]).cross(pts[c].sub(pts[a]))
		if n.dot(pts[a].sub(centre)) < 0 {
			b, c = c, b
		}
		h.addFace(a, b, c)
	}
	// File every other point against the first face it is in front of.
	for i := range pts {
		if i == i0 || i == i1 || i == i2 || i == i3 {
			continue
		}
		h.file(i, []int{0, 1, 2, 3})
	}

	for {
		fi := -1
		for i := range h.faces {
			if !h.faces[i].dead && len(h.faces[i].outside) > 0 {
				fi = i
				break
			}
		}
		if fi < 0 {
			break
		}
		h.expand(fi)
	}
	return h.result(), true
}

type qhBuilder struct {
	pts   []v3
	eps   float64
	faces []qhFace
	// edge maps a directed edge to the live face that owns it. Every edge of
	// a closed hull appears once each way, so a face's neighbour across
	// (a, b) is the owner of (b, a).
	edge map[[2]int]int
}

func (h *qhBuilder) addFace(a, b, c int) int {
	pa, pb, pc := h.pts[a], h.pts[b], h.pts[c]
	n := pb.sub(pa).cross(pc.sub(pa))
	if l := n.len(); l > 0 {
		n = n.mul(1 / l)
	}
	f := qhFace{v: [3]int{a, b, c}, n: n, d: n.dot(pa)}
	h.faces = append(h.faces, f)
	idx := len(h.faces) - 1
	h.edge[[2]int{a, b}] = idx
	h.edge[[2]int{b, c}] = idx
	h.edge[[2]int{c, a}] = idx
	return idx
}

// file puts point i in the outside set of the first of faces it is in front
// of; a point in front of none is inside the hull and dropped for good.
func (h *qhBuilder) file(i int, faces []int) {
	p := h.pts[i]
	for _, fi := range faces {
		f := &h.faces[fi]
		if f.dead {
			continue
		}
		if f.dist(p) > h.eps {
			f.outside = append(f.outside, i)
			return
		}
	}
}

// expand adds the farthest point of face fi's outside set to the hull.
func (h *qhBuilder) expand(fi int) {
	f := &h.faces[fi]
	apex, far := -1, -1.0
	for _, i := range f.outside {
		if d := f.dist(h.pts[i]); d > far {
			far, apex = d, i
		}
	}
	p := h.pts[apex]

	// The visible region, walked across edges from fi.
	visible := map[int]bool{fi: true}
	stack := []int{fi}
	var order []int
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		order = append(order, cur)
		v := h.faces[cur].v
		for k := 0; k < 3; k++ {
			a, b := v[k], v[(k+1)%3]
			nb, ok := h.edge[[2]int{b, a}]
			if !ok || visible[nb] || h.faces[nb].dead {
				continue
			}
			if h.faces[nb].dist(p) > h.eps {
				visible[nb] = true
				stack = append(stack, nb)
			}
		}
	}

	// The horizon: edges of visible faces whose neighbour is not visible.
	// Gathered before anything is removed, then fanned to the apex in the
	// visible face's own winding, which is what keeps the new faces outward.
	type hedge struct{ a, b int }
	var horizon []hedge
	var orphans []int
	for _, vi := range order {
		v := h.faces[vi].v
		for k := 0; k < 3; k++ {
			a, b := v[k], v[(k+1)%3]
			nb, ok := h.edge[[2]int{b, a}]
			if !ok || !visible[nb] {
				horizon = append(horizon, hedge{a, b})
			}
		}
		orphans = append(orphans, h.faces[vi].outside...)
	}
	for _, vi := range order {
		fv := &h.faces[vi]
		fv.dead = true
		fv.outside = nil
		for k := 0; k < 3; k++ {
			e := [2]int{fv.v[k], fv.v[(k+1)%3]}
			if h.edge[e] == vi {
				delete(h.edge, e)
			}
		}
	}
	made := make([]int, 0, len(horizon))
	for _, e := range horizon {
		made = append(made, h.addFace(e.a, e.b, apex))
	}
	for _, i := range orphans {
		if i != apex {
			h.file(i, made)
		}
	}
}

// result compacts the live faces and the points they use.
func (h *qhBuilder) result() hullResult {
	remap := map[int]int{}
	var r hullResult
	for i := range h.faces {
		f := &h.faces[i]
		if f.dead {
			continue
		}
		var t [3]int
		for k, v := range f.v {
			j, ok := remap[v]
			if !ok {
				j = len(r.verts)
				remap[v] = j
				r.verts = append(r.verts, v)
			}
			t[k] = j
		}
		r.tris = append(r.tris, t)
	}
	return r
}

// hullVolume is the volume a hull encloses: the sum of the tetrahedra its
// outward faces make with any interior point.
func hullVolume(pts []v3, r hullResult) float64 {
	if len(r.verts) == 0 {
		return 0
	}
	var c v3
	for _, v := range r.verts {
		c = c.add(pts[v])
	}
	c = c.mul(1 / float64(len(r.verts)))
	vol := 0.0
	for _, t := range r.tris {
		a, b, d := pts[r.verts[t[0]]].sub(c), pts[r.verts[t[1]]].sub(c), pts[r.verts[t[2]]].sub(c)
		vol += a.dot(b.cross(d)) / 6
	}
	return math.Abs(vol)
}
