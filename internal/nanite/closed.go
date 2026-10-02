package nanite

// closed.go — whether a cooked mesh is a closed solid, which is what lets a
// shadow pass drop the clusters that face away from the light.
//
// The camera's cull already drops a cluster whose every triangle faces away
// from the eye (the normal cone, internal/cluster): the colour pass culls
// back faces, so those triangles would never have shown. A shadow pass draws
// back faces too, and there the reason has to be different. A ray from the
// light that meets a CLOSED surface meets it first on a face turned toward
// the light — it has to enter the solid before it can leave it — so the
// nearest depth along every ray, which is all a shadow map keeps, comes from
// the light-facing triangles alone, and the clusters wholly turned away can
// be skipped without changing one texel. On a dense rock that is close to
// half of what a cascade draws.
//
// For an OPEN mesh — a cliff facade, a sheet of rock face with nothing
// behind it — the back faces are the only thing between the light and what
// lies behind, and dropping them would lose the shadow. So the shadow cone is
// allowed only on meshes that pass this test, which the renderer asks once
// per mesh when it builds its pool (render/nanite_scenery.go).

import "math"

// Closed reports whether the mesh's triangles, all slots together, bound a
// solid: every edge is shared by exactly two triangles that run along it in
// opposite directions. Vertices are matched by position, bit for bit — the
// pool splits a vertex at every UV seam and crease, and those copies still
// sit at the same point. Degenerate triangles (two corners at one point) are
// ignored, as they bound nothing. A mesh with no triangles is not closed.
func (m *Mesh) Closed() bool {
	nt := len(m.Indices) / 3
	if nt == 0 || len(m.Vertices) < 5 {
		return false
	}
	// One id per distinct position.
	type pos [3]uint32
	ids := make(map[pos]uint32, len(m.Vertices)/5)
	posID := make([]uint32, len(m.Vertices)/5)
	for v := range posID {
		p := pos{
			math.Float32bits(m.Vertices[5*v]),
			math.Float32bits(m.Vertices[5*v+1]),
			math.Float32bits(m.Vertices[5*v+2]),
		}
		id, ok := ids[p]
		if !ok {
			id = uint32(len(ids))
			ids[p] = id
		}
		posID[v] = id
	}
	// Per undirected edge: how many triangles use it, and the sum of their
	// directions along it (+1 low to high id, -1 high to low). Closed means
	// two uses and a sum of zero, everywhere.
	type edge struct {
		uses int32
		dir  int32
	}
	edges := make(map[uint64]edge, nt*3/2)
	for t := 0; t < nt; t++ {
		a, b, c := posID[m.Indices[3*t]], posID[m.Indices[3*t+1]], posID[m.Indices[3*t+2]]
		if a == b || b == c || a == c {
			continue
		}
		for _, e := range [3][2]uint32{{a, b}, {b, c}, {c, a}} {
			lo, hi, d := e[0], e[1], int32(1)
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
	if len(edges) == 0 {
		return false
	}
	for _, e := range edges {
		if e.uses != 2 || e.dir != 0 {
			return false
		}
	}
	return true
}
