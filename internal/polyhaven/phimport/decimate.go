package phimport

// decimate.go — a scan simplified to a triangle budget before the cook.
//
// Nanite handles a dense mesh's screen cost, but not its per-placement cull
// cost (a placement's tiles scale with its leaves — docs/canyon/SPEC.md,
// "Instancing") nor its disk size, so a scan scattered by the hundred wants
// a light copy. The cluster simplifier does it (quadric edge collapse onto
// existing vertices), with the normals and the UVs weighed in so a UV island
// is not smeared across its seam, slot by slot (each material's triangles
// alone, its outline kept: glTF primitives share no vertex records, so a
// slot's edge is a border to it). Each slot gets its share of the target by
// its triangle count. Unused vertices are dropped afterwards.

import (
	"math"

	"foundry/internal/cluster"
	"foundry/internal/model"

	"github.com/go-gl/mathgl/mgl32"
)

// Decimate is m simplified to about target triangles (m itself when it is
// already within target).
func Decimate(m *model.ImportMesh, target int) *model.ImportMesh {
	n := m.TriangleCount()
	if target <= 0 || n <= target {
		return m
	}
	lo, hi := bounds(m)
	ext := float64(hi.Sub(lo).Len())
	nw, uw := float32(0.02*ext), float32(0.15*ext)
	const stride = 8
	verts := make([]float32, 0, len(m.Positions)*stride)
	for i, p := range m.Positions {
		var nn mgl32.Vec3
		if i < len(m.Normals) {
			nn = m.Normals[i]
		}
		var uv mgl32.Vec2
		if i < len(m.UVs) {
			uv = m.UVs[i]
		}
		verts = append(verts, p[0], p[1], p[2], nn[0], nn[1], nn[2], uv[0], uv[1])
	}
	weights := []float32{nw, nw, nw, uw, uw}
	slots := len(m.Slots)
	if slots == 0 {
		slots = 1
	}
	per := make([][]uint32, slots)
	for t := 0; t < n; t++ {
		s := 0
		if t < len(m.TriSlot) && m.TriSlot[t] >= 0 && int(m.TriSlot[t]) < slots {
			s = int(m.TriSlot[t])
		}
		per[s] = append(per[s], m.Indices[3*t], m.Indices[3*t+1], m.Indices[3*t+2])
	}
	out := &model.ImportMesh{Slots: m.Slots, Files: m.Files}
	remap := map[uint32]uint32{}
	for s, idx := range per {
		if len(idx) == 0 {
			continue
		}
		share := int(math.Max(4, math.Round(float64(target)*float64(len(idx)/3)/float64(n))))
		res := cluster.Simplify(verts, stride, idx, cluster.SimplifyOptions{TargetTriangles: share, AttrWeights: weights})
		for k, v := range res.Indices {
			nv, ok := remap[v]
			if !ok {
				nv = uint32(len(out.Positions))
				remap[v] = nv
				out.Positions = append(out.Positions, m.Positions[v])
				if m.Normals != nil {
					out.Normals = append(out.Normals, m.Normals[v])
				}
				if m.UVs != nil {
					out.UVs = append(out.UVs, m.UVs[v])
				}
				if m.Colors != nil {
					out.Colors = append(out.Colors, m.Colors[v])
				}
			}
			out.Indices = append(out.Indices, nv)
			if k%3 == 2 {
				out.TriSlot = append(out.TriSlot, int32(s))
			}
		}
	}
	return out
}

// cloneMesh is a deep copy of the geometry (slots and files shared).
func cloneMesh(m *model.ImportMesh) *model.ImportMesh {
	c := *m
	c.Positions = append([]mgl32.Vec3(nil), m.Positions...)
	c.Normals = append([]mgl32.Vec3(nil), m.Normals...)
	c.UVs = append([]mgl32.Vec2(nil), m.UVs...)
	c.Indices = append([]uint32(nil), m.Indices...)
	c.TriSlot = append([]int32(nil), m.TriSlot...)
	if m.Colors != nil {
		c.Colors = append([][4]uint8(nil), m.Colors...)
	}
	if m.Normals == nil {
		c.Normals = nil
	}
	if m.UVs == nil {
		c.UVs = nil
	}
	return &c
}
