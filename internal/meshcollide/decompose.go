package meshcollide

// decompose.go — a mesh as a few convex hulls.
//
// GJK and EPA (internal/collision) resolve a body against a convex solid
// exactly and cheaply, and they are what every brush already goes through.
// A rock is not convex, but it is rarely far from a handful of convex pieces,
// and a handful of hulls is a far cheaper thing to push a body out of than
// the triangles themselves. This is the split.
//
// # The method
//
// Recursive plane cuts, scored by hull volume — the shape of V-HACD's
// clipping stage without its voxelisation, and in Go rather than through cgo
// (the project builds for Windows from Linux; a C++ dependency in that build
// is a cost every release would pay).
//
//  1. A piece is a triangle soup — the part of the surface on one side of
//     every cut so far. Its hull is the hull of its corners (and of the
//     points where cuts crossed its triangles, which lie on the cut faces).
//  2. Its CONCAVITY is how far inside its hull the deepest point of its own
//     surface lies: the corners, the triangles' centroids and edge midpoints,
//     each measured to the nearest hull plane. A convex piece's surface lies
//     on its hull and measures zero; an L-shaped one's inner corner measures
//     how far the hull bridges over it. That is the error a body pushed out
//     of the hull rather than the surface would feel.
//  3. The most concave piece is cut, while it is more concave than the
//     tolerance and there is room for another hull. Candidate planes are
//     axis-aligned, at seven fractions of the piece's box on each axis and
//     through the deepest point; the one whose two halves' hulls hold the
//     least volume together wins — the cut that removes the most empty space.
//  4. A hull past MaxHullVerts keeps that many of its vertices, chosen by
//     farthest-point insertion — a bounded cost per GJK support call. The
//     hull kept is inside the one it stands for, by CapError at worst.
//
// An open mesh (a sheet, a cliff face) has no inside, and its hulls fill in
// the space behind it: that is right for a rock with a hole in the bottom
// and wrong for an overhang. `foundry check` warns about hulls of a mesh with
// open edges for that reason; mesh mode is the answer for a sheet.

import (
	"math"
	"runtime"
	"sort"
	"sync"

	"github.com/go-gl/mathgl/mgl32"
)

// Decomposition is what Decompose made.
type Decomposition struct {
	Hulls      []Hull
	CapHit     bool
	VertCapped int
	// CapError is the farthest any vertex a cap dropped lies outside the
	// capped hull: how far inside its piece's hull a capped hull can be.
	CapError  float32
	Concavity float32
}

type dtri [3]v3

type piece struct {
	tris      []dtri
	pts       []v3
	hull      hullResult
	ok        bool
	concavity float64
	deepest   v3
	planes    [][4]float64
}

// maxDecomposeSplits bounds the work on a mesh that will not converge.
const maxDecomposeSplits = 256

// Decompose splits the triangles (3 indices a triangle into pos) into convex
// hulls under s's caps.
func Decompose(pos []mgl32.Vec3, tris []uint32, s Settings) Decomposition {
	s = s.Resolved()
	var out Decomposition
	if len(tris) < 3 {
		return out
	}
	soup := make([]dtri, len(tris)/3)
	var lo, hi v3
	for t := range soup {
		for k := 0; k < 3; k++ {
			p := pos[tris[3*t+k]]
			soup[t][k] = v3{float64(p[0]), float64(p[1]), float64(p[2])}
			if t == 0 && k == 0 {
				lo, hi = soup[t][k], soup[t][k]
			}
			for a := 0; a < 3; a++ {
				lo[a] = math.Min(lo[a], soup[t][k][a])
				hi[a] = math.Max(hi[a], soup[t][k][a])
			}
		}
	}
	diag := hi.sub(lo).len()
	tol := math.Max(float64(s.Concavity), float64(s.ConcavityRel)*diag)
	// A flat piece is thickened to this, so a sheet still has a hull.
	thick := math.Max(diag*1e-3, 0.005)

	root := makePiece(soup, nil, thick)
	root.measure(tol / 4)
	if !root.ok {
		return out
	}
	done := []*piece{}
	open := []*piece{root}
	splits := 0
	for len(open) > 0 {
		// The most concave open piece.
		sort.SliceStable(open, func(i, j int) bool { return open[i].concavity > open[j].concavity })
		p := open[0]
		if p.concavity <= tol {
			done = append(done, open...)
			open = nil
			break
		}
		if len(done)+len(open) >= s.MaxHulls || splits >= maxDecomposeSplits {
			out.CapHit = true
			done = append(done, open...)
			open = nil
			break
		}
		a, b, ok := split(p, thick, tol/4)
		splits++
		open = open[1:]
		if !ok {
			// Nothing divides it: keep it as it is.
			done = append(done, p)
			continue
		}
		open = append(open, a, b)
	}

	for _, p := range done {
		h, capped, capErr := hullOf(p, s.MaxHullVerts)
		if len(h.Pts) < 4 {
			continue
		}
		if capped {
			out.VertCapped++
			out.CapError = max(out.CapError, float32(capErr))
		}
		out.Hulls = append(out.Hulls, h)
		out.Concavity = max(out.Concavity, float32(p.concavity))
	}
	return out
}

// makePiece hulls a soup and measures its concavity. extra are points that
// belong to the piece's solid without being on its surface (where cuts
// crossed it).
func makePiece(tris []dtri, extra []v3, thick float64) *piece {
	p := &piece{tris: tris}
	// Every corner, repeats and all: quickhull drops a repeat as it drops any
	// point inside, and hashing a hundred thousand corners to dedupe them
	// first cost more than the hull did.
	p.pts = make([]v3, 0, 3*len(tris)+len(extra))
	for _, t := range tris {
		p.pts = append(p.pts, t[0], t[1], t[2])
	}
	p.pts = append(p.pts, extra...)
	p.hull, p.ok = quickHull(p.pts)
	if !p.ok {
		// Flat: thicken along the plane's normal and try again.
		p.pts = thicken(p.pts, thick)
		p.hull, p.ok = quickHull(p.pts)
		if !p.ok {
			return p
		}
	}
	return p
}

// measure finds the piece's concavity: the deepest its own surface lies
// inside its hull.
//
// Depths under floor are not chased: the answer only has to say whether the
// piece is more concave than the tolerance, and where, so a piece that is
// convex to within a quarter of it measures somewhere under that quarter
// without its every sample being scanned against every face.
func (p *piece) measure(floor float64) {
	p.planes = planesOf(p.pts, p.hull)
	p.concavity, p.deepest = 0, v3{}
	// A sample can only raise the maximum if it is deeper than it under every
	// plane, so the plane nearest the last sample (its neighbour on the
	// surface, usually) is tried first: for nearly every sample of a piece
	// that is close to convex, that one plane already rules it out, and the
	// full scan of the hull's faces is skipped.
	hint := 0
	measure := func(q v3) {
		if len(p.planes) == 0 {
			return
		}
		pl := p.planes[hint]
		if pl[3]-(pl[0]*q[0]+pl[1]*q[1]+pl[2]*q[2]) <= max(p.concavity, floor) {
			return
		}
		d := math.Inf(1)
		for i, pl := range p.planes {
			if v := pl[3] - (pl[0]*q[0] + pl[1]*q[1] + pl[2]*q[2]); v < d {
				d, hint = v, i
				if d <= p.concavity {
					return
				}
			}
		}
		if d > p.concavity {
			p.concavity, p.deepest = d, q
		}
	}
	for _, t := range p.tris {
		measure(t[0])
		measure(t[1])
		measure(t[2])
		measure(t[0].add(t[1]).add(t[2]).mul(1.0 / 3))
		measure(t[0].add(t[1]).mul(0.5))
		measure(t[1].add(t[2]).mul(0.5))
		measure(t[2].add(t[0]).mul(0.5))
	}
}

// thicken gives a flat point set a thickness: each point twice, either side
// of the best-fit plane by half of thick.
func thicken(pts []v3, thick float64) []v3 {
	if len(pts) < 3 {
		return pts
	}
	// The plane through the three points spanning the largest triangle found
	// from the first.
	a := pts[0]
	var b v3
	far := -1.0
	for _, q := range pts {
		if d := q.sub(a).len(); d > far {
			far, b = d, q
		}
	}
	var n v3
	best := -1.0
	for _, q := range pts {
		c := b.sub(a).cross(q.sub(a))
		if l := c.len(); l > best {
			best, n = l, c
		}
	}
	if best <= 0 {
		// Collinear: any perpendicular will do, twice.
		dir := b.sub(a)
		n = dir.cross(v3{1, 0, 0})
		if n.len() < 1e-9*dir.len() {
			n = dir.cross(v3{0, 1, 0})
		}
		m := dir.cross(n)
		n, m = n.mul(1/n.len()), m.mul(1/m.len())
		out := make([]v3, 0, 4*len(pts))
		for _, q := range pts {
			for _, s := range [4]v3{n, n.mul(-1), m, m.mul(-1)} {
				out = append(out, q.add(s.mul(thick/2)))
			}
		}
		return out
	}
	n = n.mul(1 / n.len())
	out := make([]v3, 0, 2*len(pts))
	for _, q := range pts {
		out = append(out, q.add(n.mul(thick/2)), q.sub(n.mul(thick/2)))
	}
	return out
}

// planesOf is a hull's face planes, outward, with the planes of coplanar
// faces (a flat side fanned into many triangles) merged into one.
func planesOf(pts []v3, h hullResult) [][4]float64 {
	var out [][4]float64
	type key [4]int64
	seen := map[key]bool{}
	q := func(x float64) int64 { return int64(math.Round(x * 1e7)) }
	for _, t := range h.tris {
		a, b, c := pts[h.verts[t[0]]], pts[h.verts[t[1]]], pts[h.verts[t[2]]]
		n := b.sub(a).cross(c.sub(a))
		l := n.len()
		if l <= 1e-18 {
			continue
		}
		n = n.mul(1 / l)
		pl := [4]float64{n[0], n[1], n[2], n.dot(a)}
		k := key{q(pl[0]), q(pl[1]), q(pl[2]), q(pl[3])}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, pl)
	}
	return out
}

// split cuts p by the best of its candidate planes.
func split(p *piece, thick, floor float64) (*piece, *piece, bool) {
	lo, hi := p.pts[0], p.pts[0]
	for _, q := range p.pts[1:] {
		for a := 0; a < 3; a++ {
			lo[a] = math.Min(lo[a], q[a])
			hi[a] = math.Max(hi[a], q[a])
		}
	}
	type cand struct {
		axis int
		at   float64
	}
	var cands []cand
	for axis := 0; axis < 3; axis++ {
		if hi[axis]-lo[axis] <= thick {
			continue
		}
		for k := 1; k <= 7; k++ {
			cands = append(cands, cand{axis, lo[axis] + (hi[axis]-lo[axis])*float64(k)/8})
		}
		if d := p.deepest[axis]; d > lo[axis]+thick && d < hi[axis]-thick {
			cands = append(cands, cand{axis, d})
		}
	}
	// The candidates are independent, so they are scored side by side; the
	// winner is chosen in candidate order afterwards, so a decomposition does
	// not depend on which finished first.
	type scored struct {
		a, b *piece
		vol  float64
	}
	res := make([]scored, len(cands))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	for i, c := range cands {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, c cand) {
			defer func() { <-sem; wg.Done() }()
			l, r, cut := clipSoup(p.tris, c.axis, c.at)
			if len(l) == 0 || len(r) == 0 {
				return
			}
			a := makePiece(l, cut, thick)
			b := makePiece(r, cut, thick)
			if !a.ok || !b.ok {
				return
			}
			res[i] = scored{a, b, hullVolume(a.pts, a.hull) + hullVolume(b.pts, b.hull)}
		}(i, c)
	}
	wg.Wait()
	var bestA, bestB *piece
	best := math.Inf(1)
	for _, r := range res {
		if r.a != nil && r.vol < best {
			best, bestA, bestB = r.vol, r.a, r.b
		}
	}
	if bestA == nil {
		return nil, nil, false
	}
	// Only the winner is measured: the score needs volumes, not depths.
	bestA.measure(floor)
	bestB.measure(floor)
	return bestA, bestB, true
}

// clipSoup splits triangles by the plane x[axis] = at, and returns both sides
// and the points where triangles crossed it.
func clipSoup(tris []dtri, axis int, at float64) (left, right []dtri, cut []v3) {
	for _, t := range tris {
		var s [3]float64
		neg, pos := 0, 0
		for k := 0; k < 3; k++ {
			s[k] = t[k][axis] - at
			if s[k] < 0 {
				neg++
			} else if s[k] > 0 {
				pos++
			}
		}
		if pos == 0 {
			left = append(left, t)
			continue
		}
		if neg == 0 {
			right = append(right, t)
			continue
		}
		// Straddles: each side's polygon, fanned.
		var lp, rp []v3
		for k := 0; k < 3; k++ {
			a, b := t[k], t[(k+1)%3]
			sa, sb := s[k], s[(k+1)%3]
			if sa <= 0 {
				lp = append(lp, a)
			}
			if sa >= 0 {
				rp = append(rp, a)
			}
			if (sa < 0 && sb > 0) || (sa > 0 && sb < 0) {
				f := sa / (sa - sb)
				x := a.add(b.sub(a).mul(f))
				x[axis] = at
				lp = append(lp, x)
				rp = append(rp, x)
				cut = append(cut, x)
			}
		}
		for i := 1; i+1 < len(lp); i++ {
			left = append(left, dtri{lp[0], lp[i], lp[i+1]})
		}
		for i := 1; i+1 < len(rp); i++ {
			right = append(right, dtri{rp[0], rp[i], rp[i+1]})
		}
	}
	return left, right, cut
}

// hullOf is a piece's hull as a Hull, capped to maxVerts vertices, and how
// far the cap moved its surface.
func hullOf(p *piece, maxVerts int) (Hull, bool, float64) {
	pts, hr := p.pts, p.hull
	capped := false
	capErr := 0.0
	if len(hr.verts) > maxVerts {
		capped = true
		hv := make([]v3, len(hr.verts))
		for i, v := range hr.verts {
			hv[i] = pts[v]
		}
		// Half the budget on support points along directions spread evenly
		// over the sphere — near the best there is for a round piece — then
		// greedy farthest-point insertion for the rest: whichever of the full
		// hull's vertices lies farthest outside the hull so far goes in next.
		// The directions alone crowd the ends of a long, thin piece; the
		// insertion puts the rest of the points where the error is.
		keep := supportSet(hv, maxVerts/2)
		for len(keep) < maxVerts {
			r, ok := quickHull(keep)
			if !ok {
				break
			}
			planes := planesOf(keep, r)
			far, worst := -1, 1e-9
			for i, q := range hv {
				d := math.Inf(-1)
				for _, pl := range planes {
					d = math.Max(d, pl[0]*q[0]+pl[1]*q[1]+pl[2]*q[2]-pl[3])
				}
				if d > worst {
					far, worst = i, d
				}
			}
			if far < 0 {
				break
			}
			keep = append(keep, hv[far])
		}
		if r, ok := quickHull(keep); ok {
			pts, hr = keep, r
			for _, pl := range planesOf(keep, r) {
				for _, q := range hv {
					capErr = math.Max(capErr, pl[0]*q[0]+pl[1]*q[1]+pl[2]*q[2]-pl[3])
				}
			}
		}
	}
	h := Hull{}
	for i, v := range hr.verts {
		q := pts[v]
		h.Pts = append(h.Pts, mgl32.Vec3{float32(q[0]), float32(q[1]), float32(q[2])})
		if i == 0 {
			h.Lo, h.Hi = h.Pts[0], h.Pts[0]
		}
		h.Lo, h.Hi = vmin(h.Lo, h.Pts[i]), vmax(h.Hi, h.Pts[i])
	}
	for _, t := range hr.tris {
		h.Tris = append(h.Tris, uint16(t[0]), uint16(t[1]), uint16(t[2]))
	}
	for _, pl := range planesOf(pts, hr) {
		h.Planes = append(h.Planes, mgl32.Vec4{float32(pl[0]), float32(pl[1]), float32(pl[2]), float32(pl[3])})
	}
	return h, capped, capErr
}

// supportSet is the distinct farthest points of pts along n directions spread
// evenly over the sphere (a Fibonacci lattice).
func supportSet(pts []v3, n int) []v3 {
	seen := map[int]bool{}
	var out []v3
	golden := math.Pi * (3 - math.Sqrt(5))
	for i := 0; i < n; i++ {
		y := 1 - 2*(float64(i)+0.5)/float64(n)
		r := math.Sqrt(math.Max(0, 1-y*y))
		th := golden * float64(i)
		d := v3{r * math.Cos(th), y, r * math.Sin(th)}
		bi, bd := 0, math.Inf(-1)
		for j, q := range pts {
			if v := q.dot(d); v > bd {
				bd, bi = v, j
			}
		}
		if !seen[bi] {
			seen[bi] = true
			out = append(out, pts[bi])
		}
	}
	return out
}
