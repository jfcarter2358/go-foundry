package meshcollide

// build.go — from a collision cut to cooked collision data.
//
// The cut arrives as the DAG gave it: a vertex pool shared with the render
// data, where one position is several records (a UV seam, a crease in the
// normals), and triangles that a coarse level may have squashed flat. None
// of that means anything to a body, so the clean that comes first:
//
//   - welds corners by POSITION alone, within a hair of the mesh's size, so
//     a seam the renderer needs two records for is one vertex here and the
//     triangles either side of it share an edge;
//   - drops every triangle that has no area once welded (two corners the
//     same, or all three in a line), which a ray would only ever graze;
//   - counts the edges used by one triangle (open borders: the mesh is a
//     sheet there, and a hull of it fills in behind) and by more than two
//     (non-manifold), for the editor and `foundry check` to report.
//
// Then the vertices are quantised to 16 bits over the cut's box, and
// everything after — the tree, the hulls — is built from the DEQUANTISED
// positions, so the tree's boxes bound exactly the triangles the runtime
// will test, not the slightly different ones the cook started from.

import (
	"fmt"
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

func errf(format string, args ...any) error { return fmt.Errorf(format, args...) }

// Input is a collision cut before cleaning: a vertex pool (Stride floats a
// vertex, x, y, z first), a triangle list over it, and each triangle's tag.
type Input struct {
	Positions []float32
	Stride    int
	Indices   []uint32
	// TriTags is a tag per triangle (nil: every triangle 0), and TagNames
	// what each tag is called.
	TriTags  []uint8
	TagNames []string
}

// Build cleans the cut, quantises it, builds its tree and decomposes it.
func Build(in Input, s Settings) (*Data, error) {
	s = s.Resolved()
	stride := in.Stride
	if stride < 3 {
		stride = 3
	}
	nt := len(in.Indices) / 3
	d := &Data{Settings: s, TagNames: append([]string(nil), in.TagNames...), SourceTriangles: nt}
	if nt == 0 {
		return d, nil
	}
	nv := len(in.Positions) / stride
	pos := func(v uint32) mgl32.Vec3 {
		return mgl32.Vec3{in.Positions[stride*int(v)], in.Positions[stride*int(v)+1], in.Positions[stride*int(v)+2]}
	}

	// The weld tolerance, from the extent of what the triangles use.
	lo, hi := mgl32.Vec3{}, mgl32.Vec3{}
	for i, ix := range in.Indices {
		if int(ix) >= nv {
			return nil, errf("collision cut index %d past %d vertices", ix, nv)
		}
		p := pos(ix)
		if i == 0 {
			lo, hi = p, p
			continue
		}
		lo = vmin(lo, p)
		hi = vmax(hi, p)
	}
	ext := hi.Sub(lo).Len()
	cell := float64(max(ext*1e-6, 1e-6))

	// Weld by position: corners in one cell of a fine grid are one vertex.
	type key [3]int64
	at := map[key]uint32{}
	remap := make([]uint32, nv)
	used := make([]bool, nv)
	for _, ix := range in.Indices {
		used[ix] = true
	}
	var welded []mgl32.Vec3
	for v := 0; v < nv; v++ {
		if !used[v] {
			continue
		}
		p := pos(uint32(v))
		k := key{int64(math.Floor(float64(p[0]) / cell)), int64(math.Floor(float64(p[1]) / cell)), int64(math.Floor(float64(p[2]) / cell))}
		if w, ok := at[k]; ok {
			remap[v] = w
			d.Welded++
			continue
		}
		remap[v] = uint32(len(welded))
		at[k] = remap[v]
		welded = append(welded, p)
	}

	// Quantise over the welded box, flat axes widened a hair so every axis
	// has a range to divide.
	d.Lo, d.Hi = welded[0], welded[0]
	for _, p := range welded[1:] {
		d.Lo, d.Hi = vmin(d.Lo, p), vmax(d.Hi, p)
	}
	for k := 0; k < 3; k++ {
		if d.Hi[k]-d.Lo[k] < 1e-6 {
			d.Lo[k] -= 1e-3
			d.Hi[k] += 1e-3
		}
	}
	d.Verts = make([]uint16, 0, 3*len(welded))
	for _, p := range welded {
		for k := 0; k < 3; k++ {
			q := (p[k] - d.Lo[k]) / (d.Hi[k] - d.Lo[k])
			d.Verts = append(d.Verts, uint16(math.Round(float64(min(max(q, 0), 1))*65535)))
		}
	}
	deq := make([]mgl32.Vec3, len(welded))
	for v := range deq {
		deq[v] = d.Dequantize(uint32(v))
	}

	// Triangles over the welded, quantised vertices; degenerates dropped.
	minArea := float64(ext) * float64(ext) * 1e-12
	var tris []uint32
	var tags []uint8
	for t := 0; t < nt; t++ {
		a, b, c := remap[in.Indices[3*t]], remap[in.Indices[3*t+1]], remap[in.Indices[3*t+2]]
		if a == b || b == c || c == a {
			d.Degenerate++
			continue
		}
		if float64(deq[b].Sub(deq[a]).Cross(deq[c].Sub(deq[a])).Len()) <= minArea {
			d.Degenerate++
			continue
		}
		tris = append(tris, a, b, c)
		var tag uint8
		if t < len(in.TriTags) {
			tag = in.TriTags[t]
		}
		tags = append(tags, tag)
	}

	// Edge use, undirected.
	edges := map[[2]uint32]int{}
	for t := 0; t < len(tris)/3; t++ {
		for k := 0; k < 3; k++ {
			a, b := tris[3*t+k], tris[3*t+(k+1)%3]
			if a > b {
				a, b = b, a
			}
			edges[[2]uint32{a, b}]++
		}
	}
	for _, n := range edges {
		switch {
		case n == 1:
			d.OpenEdges++
		case n > 2:
			d.NonManifold++
		}
	}

	// The tree, and the triangles in its leaf order.
	nodes, order := buildBVH(deq, tris)
	d.Nodes = nodes
	d.Tris = make([]uint32, 0, len(tris))
	d.Tags = make([]uint8, 0, len(tags))
	for _, t := range order {
		d.Tris = append(d.Tris, tris[3*t:3*t+3]...)
		d.Tags = append(d.Tags, tags[t])
	}

	// The hulls.
	dec := Decompose(deq, tris, s)
	d.Hulls, d.HullCapHit, d.VertCapped, d.VertCapError, d.Concavity = dec.Hulls, dec.CapHit, dec.VertCapped, dec.CapError, dec.Concavity
	return d, d.Validate()
}

func vmin(a, b mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{min(a[0], b[0]), min(a[1], b[1]), min(a[2], b[2])}
}

func vmax(a, b mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{max(a[0], b[0]), max(a[1], b[1]), max(a[2], b[2])}
}
