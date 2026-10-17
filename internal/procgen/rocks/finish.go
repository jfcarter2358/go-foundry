package rocks

// finish.go — from the mesher's output to a placeable mesh: the decimation,
// the placement frame and the box-projected UVs.

import (
	"context"
	"math"

	"foundry/internal/cluster"
	"foundry/internal/procgen/sdf"
)

// simplify decimates m to about target triangles with the cluster
// simplifier (quadric edge collapse onto existing vertices) — the normals
// weighed in at 2% of the rock's extent per radian, and the colours (nil for
// none) at 3% per unit — then compacts it, colours with it.
func simplify(m *sdf.Mesh, colors [][4]uint8, target int, ext float64) [][4]uint8 {
	stride := 6
	if colors != nil {
		stride = 10
	}
	verts := make([]float32, 0, len(m.Positions)*stride)
	for i, p := range m.Positions {
		n := m.Normals[i]
		verts = append(verts, float32(p[0]), float32(p[1]), float32(p[2]), float32(n[0]), float32(n[1]), float32(n[2]))
		if colors != nil {
			c := colors[i]
			verts = append(verts, float32(c[0])/255, float32(c[1])/255, float32(c[2])/255, float32(c[3])/255)
		}
	}
	nw, cw := float32(0.02*ext), float32(0.03*ext)
	res := cluster.Simplify(verts, stride, m.Indices, cluster.SimplifyOptions{TargetTriangles: target,
		AttrWeights: []float32{nw, nw, nw, cw, cw, cw, cw}})
	m.Indices = res.Indices
	// Compact, the colours in step.
	remap := make([]int32, len(m.Positions))
	for i := range remap {
		remap[i] = -1
	}
	for _, ix := range m.Indices {
		remap[ix] = 0
	}
	n := int32(0)
	var out [][4]uint8
	for i := range remap {
		if remap[i] < 0 {
			continue
		}
		remap[i] = n
		m.Positions[n], m.Normals[n] = m.Positions[i], m.Normals[i]
		if colors != nil {
			out = append(out, colors[i])
		}
		n++
	}
	m.Positions, m.Normals = m.Positions[:n], m.Normals[:n]
	for i, ix := range m.Indices {
		m.Indices[i] = uint32(remap[ix])
	}
	return out
}

// meshNormals sets every vertex normal to the angle-weighted mean of its
// triangles' normals.
func meshNormals(m *sdf.Mesh) {
	acc := make([]sdf.Vec3, len(m.Positions))
	for t := 0; t+2 < len(m.Indices); t += 3 {
		ix := [3]uint32{m.Indices[t], m.Indices[t+1], m.Indices[t+2]}
		p := [3]sdf.Vec3{m.Positions[ix[0]], m.Positions[ix[1]], m.Positions[ix[2]]}
		n := p[1].Sub(p[0]).Cross(p[2].Sub(p[0]))
		if n.Len() == 0 {
			continue
		}
		n = n.Normalize()
		for k := 0; k < 3; k++ {
			e1 := p[(k+1)%3].Sub(p[k]).Normalize()
			e2 := p[(k+2)%3].Sub(p[k]).Normalize()
			a := math.Acos(sdf.Clamp(e1.Dot(e2), -1, 1))
			acc[ix[k]] = acc[ix[k]].Add(n.Mul(a))
		}
	}
	for i := range acc {
		if acc[i].Len() > 0 {
			m.Normals[i] = acc[i].Normalize()
		}
	}
}

// placementOffset is the translation that puts the rock in its placement
// frame: its lowest point on y = 0 and its footprint centred on the origin,
// or, for a rock with a flat back, the back on z = 0 (the rock in front of
// it, along +Z).
func placementOffset(m *sdf.Mesh, pr *preset) sdf.Vec3 {
	b := m.Bounds()
	c := b.Center()
	off := sdf.Vec3{-c[0], -b.Lo[1], -c[2]}
	if pr.back {
		off[2] = -b.Lo[2]
	}
	return off
}

// boxUVs is m as a finished Mesh: every triangle projected onto the plane
// across its normal's main axis (mirrored on the negative sides so the
// texture never reads backwards), the UVs scale per metre, a vertex split
// wherever triangles of two projections meet so each keeps its own. colors
// (nil for none) are carried to the copies.
func boxUVs(m *sdf.Mesh, colors [][4]uint8, scale float64) *Mesh {
	out := &Mesh{}
	type key struct {
		v    uint32
		proj int8
	}
	index := make(map[key]uint32, len(m.Positions)*5/4)
	uv := func(p sdf.Vec3, proj int8) [2]float32 {
		axis, sign := int(proj/2), 1.0
		if proj%2 == 1 {
			sign = -1
		}
		var u, v float64
		switch axis {
		case 0:
			u, v = -sign*p[2], p[1]
		case 1:
			u, v = p[0], -sign*p[2]
		default:
			u, v = sign*p[0], p[1]
		}
		return [2]float32{float32(u * scale), float32(v * scale)}
	}
	out.Indices = make([]uint32, len(m.Indices))
	for t := 0; t+2 < len(m.Indices); t += 3 {
		a, b, c := m.Indices[t], m.Indices[t+1], m.Indices[t+2]
		n := m.Positions[b].Sub(m.Positions[a]).Cross(m.Positions[c].Sub(m.Positions[a]))
		axis := 0
		an := n.Abs()
		if an[1] > an[axis] {
			axis = 1
		}
		if an[2] > an[axis] {
			axis = 2
		}
		proj := int8(2 * axis)
		if n[axis] < 0 {
			proj++
		}
		for k, v := range [3]uint32{a, b, c} {
			kk := key{v, proj}
			ix, ok := index[kk]
			if !ok {
				ix = uint32(len(out.Positions))
				index[kk] = ix
				p, nn := m.Positions[v], m.Normals[v]
				out.Positions = append(out.Positions, [3]float32{float32(p[0]), float32(p[1]), float32(p[2])})
				out.Normals = append(out.Normals, [3]float32{float32(nn[0]), float32(nn[1]), float32(nn[2])})
				out.UVs = append(out.UVs, uv(p, proj))
				if colors != nil {
					out.Colors = append(out.Colors, colors[v])
				}
			}
			out.Indices[t+k] = ix
		}
	}
	return out
}

// parallelFor is sdf.Parallel.
func parallelFor(ctx context.Context, workers, n int, fn func(i int)) error {
	return sdf.Parallel(ctx, workers, n, fn)
}

// radiusXZ is the largest horizontal distance of a position from the
// origin, over the positions with y in lo..hi.
func radiusXZ(m *Mesh, lo, hi float32) float64 {
	r := 0.0
	for _, p := range m.Positions {
		if p[1] < lo || p[1] > hi {
			continue
		}
		r = math.Max(r, math.Hypot(float64(p[0]), float64(p[2])))
	}
	return r
}
