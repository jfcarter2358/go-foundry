package nanitecook

// sample.go — a procedural dense mesh, for the tests, the GL captures and
// `foundry nanite sample`: a lumpy rock made by subdividing an icosahedron
// and pushing each vertex out by layered noise. At level 7 it is 327,680
// triangles, the kind of asset Phase 1's cull exists for, and nothing about it
// has to be checked into the repository.

import (
	"math"

	"foundry/internal/model"

	"github.com/go-gl/mathgl/mgl32"
)

// SampleRock is a rock of 20*4^level triangles and radius about 1, with
// smooth normals and a spherical UV mapping. Two material slots, "stone" over
// most of it and "moss" (double-sided) on the upward-facing cap, so a sample
// exercises a multi-slot draw.
func SampleRock(level int, seed float32) *model.ImportMesh {
	t := float32((1 + math.Sqrt(5)) / 2)
	verts := []mgl32.Vec3{
		{-1, t, 0}, {1, t, 0}, {-1, -t, 0}, {1, -t, 0},
		{0, -1, t}, {0, 1, t}, {0, -1, -t}, {0, 1, -t},
		{t, 0, -1}, {t, 0, 1}, {-t, 0, -1}, {-t, 0, 1},
	}
	for i := range verts {
		verts[i] = verts[i].Normalize()
	}
	faces := [][3]uint32{
		{0, 11, 5}, {0, 5, 1}, {0, 1, 7}, {0, 7, 10}, {0, 10, 11},
		{1, 5, 9}, {5, 11, 4}, {11, 10, 2}, {10, 7, 6}, {7, 1, 8},
		{3, 9, 4}, {3, 4, 2}, {3, 2, 6}, {3, 6, 8}, {3, 8, 9},
		{4, 9, 5}, {2, 4, 11}, {6, 2, 10}, {8, 6, 7}, {9, 8, 1},
	}
	for l := 0; l < level; l++ {
		mid := map[[2]uint32]uint32{}
		midpoint := func(a, b uint32) uint32 {
			k := [2]uint32{min(a, b), max(a, b)}
			if v, ok := mid[k]; ok {
				return v
			}
			verts = append(verts, verts[a].Add(verts[b]).Normalize())
			mid[k] = uint32(len(verts) - 1)
			return mid[k]
		}
		next := make([][3]uint32, 0, len(faces)*4)
		for _, f := range faces {
			a, b, c := midpoint(f[0], f[1]), midpoint(f[1], f[2]), midpoint(f[2], f[0])
			next = append(next, [3]uint32{f[0], a, c}, [3]uint32{f[1], b, a}, [3]uint32{f[2], c, b}, [3]uint32{a, b, c})
		}
		faces = next
	}
	// Wound counter-clockwise seen from outside, whichever way the list
	// above happens to run.
	f0 := faces[0]
	if verts[f0[1]].Sub(verts[f0[0]]).Cross(verts[f0[2]].Sub(verts[f0[0]])).Dot(verts[f0[0]]) < 0 {
		for i := range faces {
			faces[i][1], faces[i][2] = faces[i][2], faces[i][1]
		}
	}
	// Displace: a few octaves of a smooth, seeded function of direction.
	height := func(d mgl32.Vec3) float32 {
		h := float32(0)
		amp, freq := float32(0.18), float32(1.7)
		for o := 0; o < 5; o++ {
			x, y, z := d[0]*freq+seed, d[1]*freq-seed*0.7, d[2]*freq+seed*1.3
			h += amp * float32(math.Sin(float64(x*1.3+y*0.7))*math.Cos(float64(y*1.1-z*0.9))*math.Sin(float64(z*1.7+x*0.3)))
			amp *= 0.5
			freq *= 2.1
		}
		return h
	}
	m := &model.ImportMesh{
		Slots: []model.ImportSlot{
			{Name: "stone", BaseColor: [4]float32{0.5, 0.48, 0.45, 1}},
			{Name: "moss", BaseColor: [4]float32{0.3, 0.45, 0.2, 1}, DoubleSided: true},
		},
	}
	for _, d := range verts {
		p := d.Mul(1 + height(d))
		p[1] *= 0.7 // a rock sits lower than it is wide
		m.Positions = append(m.Positions, p)
		u := float32(math.Atan2(float64(d[2]), float64(d[0]))/(2*math.Pi)) + 0.5
		v := float32(math.Acos(float64(max(-1, min(1, d[1])))) / math.Pi)
		m.UVs = append(m.UVs, mgl32.Vec2{u * 4, v * 2})
	}
	// Smooth normals from the displaced faces.
	acc := make([]mgl32.Vec3, len(m.Positions))
	for _, f := range faces {
		a, b, c := m.Positions[f[0]], m.Positions[f[1]], m.Positions[f[2]]
		n := b.Sub(a).Cross(c.Sub(a))
		for _, v := range f {
			acc[v] = acc[v].Add(n)
		}
	}
	for _, n := range acc {
		if l := n.Len(); l > 0 {
			n = n.Mul(1 / l)
		}
		m.Normals = append(m.Normals, n)
	}
	for _, f := range faces {
		m.Indices = append(m.Indices, f[0], f[1], f[2])
		up := verts[f[0]].Add(verts[f[1]]).Add(verts[f[2]]).Normalize()[1] > 0.8
		if up {
			m.TriSlot = append(m.TriSlot, 1)
		} else {
			m.TriSlot = append(m.TriSlot, 0)
		}
	}
	return m
}
