package sdf

// meshutil.go — measuring and tidying a mesh: area, signed volume, bounds,
// connected pieces (a joint cut right through a rock leaves a fragment
// floating beside it, which a generator usually wants gone), and compaction
// after a simplifier has dropped vertices.

import (
	"math"
	"sort"
)

// Area is the surface area.
func (m *Mesh) Area() float64 {
	a := 0.0
	for t := 0; t+2 < len(m.Indices); t += 3 {
		p0, p1, p2 := m.Positions[m.Indices[t]], m.Positions[m.Indices[t+1]], m.Positions[m.Indices[t+2]]
		a += p1.Sub(p0).Cross(p2.Sub(p0)).Len() / 2
	}
	return a
}

// Volume is the signed volume: positive for a closed mesh wound outward.
func (m *Mesh) Volume() float64 {
	v := 0.0
	for t := 0; t+2 < len(m.Indices); t += 3 {
		p0, p1, p2 := m.Positions[m.Indices[t]], m.Positions[m.Indices[t+1]], m.Positions[m.Indices[t+2]]
		v += p0.Dot(p1.Cross(p2)) / 6
	}
	return v
}

// Bounds is the box the used vertices span.
func (m *Mesh) Bounds() Box {
	b := Box{Lo: Vec3{math.Inf(1), math.Inf(1), math.Inf(1)}, Hi: Vec3{math.Inf(-1), math.Inf(-1), math.Inf(-1)}}
	for _, ix := range m.Indices {
		p := m.Positions[ix]
		b = b.Union(Box{p, p})
	}
	return b
}

// OpenEdges counts the edges used by exactly one triangle (0 for a closed
// mesh), and those used by more than two (non-manifold).
func (m *Mesh) OpenEdges() (open, nonManifold int) {
	count := map[[2]uint32]int{}
	for t := 0; t+2 < len(m.Indices); t += 3 {
		for k := 0; k < 3; k++ {
			a, b := m.Indices[t+k], m.Indices[t+(k+1)%3]
			if a > b {
				a, b = b, a
			}
			count[[2]uint32{a, b}]++
		}
	}
	for _, c := range count {
		switch {
		case c == 1:
			open++
		case c > 2:
			nonManifold++
		}
	}
	return
}

// KeepLargest drops every connected piece (triangles sharing vertices) with
// fewer than minShare of the triangles, and always keeps the largest; then
// compacts. It returns how many pieces it dropped.
func (m *Mesh) KeepLargest(minShare float64) int {
	nv := len(m.Positions)
	parent := make([]int32, nv)
	for i := range parent {
		parent[i] = int32(i)
	}
	var find func(int32) int32
	find = func(x int32) int32 {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	for t := 0; t+2 < len(m.Indices); t += 3 {
		a := find(int32(m.Indices[t]))
		for k := 1; k < 3; k++ {
			b := find(int32(m.Indices[t+k]))
			if a != b {
				if a < b {
					parent[b] = a
				} else {
					parent[a] = b
					a = b
				}
			}
		}
	}
	size := map[int32]int{}
	for t := 0; t+2 < len(m.Indices); t += 3 {
		size[find(int32(m.Indices[t]))]++
	}
	if len(size) <= 1 {
		return 0
	}
	type piece struct {
		root int32
		n    int
	}
	var ps []piece
	for r, n := range size {
		ps = append(ps, piece{r, n})
	}
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].n != ps[j].n {
			return ps[i].n > ps[j].n
		}
		return ps[i].root < ps[j].root
	})
	total := m.TriangleCount()
	keep := map[int32]bool{ps[0].root: true}
	for _, p := range ps[1:] {
		if float64(p.n) >= minShare*float64(total) {
			keep[p.root] = true
		}
	}
	idx := m.Indices[:0]
	for t := 0; t+2 < len(m.Indices); t += 3 {
		if keep[find(int32(m.Indices[t]))] {
			idx = append(idx, m.Indices[t], m.Indices[t+1], m.Indices[t+2])
		}
	}
	m.Indices = idx
	m.Compact()
	return len(ps) - len(keep)
}

// Compact drops the vertices no triangle uses, keeping the rest in order.
func (m *Mesh) Compact() {
	remap := make([]int32, len(m.Positions))
	for i := range remap {
		remap[i] = -1
	}
	n := int32(0)
	for _, ix := range m.Indices {
		if remap[ix] < 0 {
			remap[ix] = 0 // marked; numbered below in vertex order
		}
	}
	for i := range remap {
		if remap[i] == 0 {
			remap[i] = n
			m.Positions[n] = m.Positions[i]
			m.Normals[n] = m.Normals[i]
			n++
		}
	}
	m.Positions, m.Normals = m.Positions[:n], m.Normals[:n]
	for i, ix := range m.Indices {
		m.Indices[i] = uint32(remap[ix])
	}
}

// Transform moves every vertex by p ↦ R·p + t and turns the normals with R
// (a rotation).
func (m *Mesh) Transform(r Mat3, t Vec3) {
	for i := range m.Positions {
		m.Positions[i] = r.Apply(m.Positions[i]).Add(t)
		m.Normals[i] = r.Apply(m.Normals[i])
	}
}

// SplitNonManifold makes the mesh a manifold: dual contouring puts one vertex
// in a cell even where two sheets of surface pass through it (two rock faces
// a crack's width apart, two bumps kissing), and then an edge is shared by
// four triangles, or a vertex by two fans of them. Each such vertex is split,
// one copy per fan of triangles joined by manifold edges, and every copy but
// the first is nudged a hair toward its own fan, so the two sheets are apart
// by position as well as by index — what a closed-solid test counts by
// (nanite.Mesh.Closed). It returns how many vertices it added.
func (m *Mesh) SplitNonManifold() int {
	added := 0
	for pass := 0; pass < 4; pass++ {
		n := m.splitPass()
		added += n
		if n == 0 {
			break
		}
	}
	return added
}

func (m *Mesh) splitPass() int {
	nt := m.TriangleCount()
	nv := len(m.Positions)
	ekey := func(a, b uint32) uint64 {
		if a > b {
			a, b = b, a
		}
		return uint64(a)<<32 | uint64(b)
	}
	uses := make(map[uint64]int32, nt*3/2)
	for t := 0; t < nt; t++ {
		for k := 0; k < 3; k++ {
			uses[ekey(m.Indices[3*t+k], m.Indices[3*t+(k+1)%3])]++
		}
	}
	// Each vertex's triangles (CSR).
	start := make([]int32, nv+1)
	for _, ix := range m.Indices {
		start[ix+1]++
	}
	for i := 0; i < nv; i++ {
		start[i+1] += start[i]
	}
	fill := append([]int32(nil), start[:nv]...)
	tris := make([]int32, len(m.Indices))
	for t := 0; t < nt; t++ {
		for k := 0; k < 3; k++ {
			v := m.Indices[3*t+k]
			tris[fill[v]] = int32(t)
			fill[v]++
		}
	}
	added := 0
	var comp []int32
	for v := uint32(0); int(v) < nv; v++ {
		ts := tris[start[v]:start[v+1]]
		if len(ts) < 2 {
			continue
		}
		comp = comp[:0]
		for i := range ts {
			comp = append(comp, int32(i))
		}
		var find func(int32) int32
		find = func(x int32) int32 {
			for comp[x] != x {
				comp[x] = comp[comp[x]]
				x = comp[x]
			}
			return x
		}
		// The other two corners of each triangle at v.
		others := func(t int32) (uint32, uint32) {
			a, b, c := m.Indices[3*t], m.Indices[3*t+1], m.Indices[3*t+2]
			switch v {
			case a:
				return b, c
			case b:
				return c, a
			}
			return a, b
		}
		for i := range ts {
			ai, bi := others(ts[i])
			for j := i + 1; j < len(ts); j++ {
				aj, bj := others(ts[j])
				for _, w := range [2]uint32{ai, bi} {
					if (w == aj || w == bj) && uses[ekey(v, w)] == 2 {
						ri, rj := find(int32(i)), find(int32(j))
						if ri != rj {
							comp[max(ri, rj)] = min(ri, rj)
						}
					}
				}
			}
		}
		roots := map[int32][]int32{}
		var order []int32
		for i := range ts {
			r := find(int32(i))
			if _, ok := roots[r]; !ok {
				order = append(order, r)
			}
			roots[r] = append(roots[r], ts[i])
		}
		if len(order) < 2 {
			continue
		}
		p := m.Positions[v]
		for _, r := range order[1:] {
			fan := roots[r]
			// Toward the fan's centroid, a percent of the way.
			var c Vec3
			for _, t := range fan {
				for k := 0; k < 3; k++ {
					c = c.Add(m.Positions[m.Indices[3*int(t)+k]])
				}
			}
			c = c.Mul(1 / float64(3*len(fan)))
			nv2 := uint32(len(m.Positions))
			m.Positions = append(m.Positions, p.Add(c.Sub(p).Mul(0.01)))
			m.Normals = append(m.Normals, m.Normals[v])
			for _, t := range fan {
				for k := uint32(0); k < 3; k++ {
					if m.Indices[3*uint32(t)+k] == v {
						m.Indices[3*uint32(t)+k] = nv2
					}
				}
			}
			added++
		}
	}
	return added
}

// ClosedByPosition reports whether every edge, matching vertices by
// position, is used by exactly two triangles running along it in opposite
// directions (nanite.Mesh.Closed's test).
func (m *Mesh) ClosedByPosition() bool {
	ids := map[Vec3]uint32{}
	id := make([]uint32, len(m.Positions))
	for i, p := range m.Positions {
		x, ok := ids[p]
		if !ok {
			x = uint32(len(ids))
			ids[p] = x
		}
		id[i] = x
	}
	type e struct{ uses, dir int32 }
	edges := map[uint64]e{}
	for t := 0; t+2 < len(m.Indices); t += 3 {
		a, b, c := id[m.Indices[t]], id[m.Indices[t+1]], id[m.Indices[t+2]]
		if a == b || b == c || a == c {
			continue
		}
		for _, ed := range [3][2]uint32{{a, b}, {b, c}, {c, a}} {
			lo, hi, d := ed[0], ed[1], int32(1)
			if lo > hi {
				lo, hi, d = hi, lo, -1
			}
			k := uint64(lo)<<32 | uint64(hi)
			x := edges[k]
			x.uses++
			x.dir += d
			edges[k] = x
		}
	}
	for _, x := range edges {
		if x.uses != 2 || x.dir != 0 {
			return false
		}
	}
	return len(edges) > 0
}
