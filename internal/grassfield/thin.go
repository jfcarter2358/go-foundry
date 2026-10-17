package grassfield

// thin.go — a kind's meshes: the clump as imported, and the same clump
// thinned for distance.
//
// A Poly Haven grass clump is a handful to a few dozen separate blade cards
// of ~30 triangles each. Edge-collapse decimation cannot shorten a card
// without shredding it (docs/regions/PRESETS_2.md), so the distant LODs here
// THIN instead, as phimport.DecimateCards does for trees: a seeded subset of
// whole pieces (connected components) is kept and each kept piece is WIDENED
// — scaled about its own vertical axis in X and Z by 1/sqrt(fraction),
// capped at MaxWiden — so the clump keeps roughly its cover with fewer
// cards. Unlike DecimateCards, the height is left alone: a blade enlarged
// about its centre would sink into the ground or float, which a tree's
// needle cluster high in a crown never shows and a grass blade at ground
// level always would.
//
// Every vertex carries its wind weights (VertexFloats): up, how far up the
// clump it is (0 at the base, 1 at the LOD 0 mesh's top), and out, how far
// from the clump's axis (0..1 of its radius), then the kind's bend and
// flutter. The thinned LODs keep the LOD 0 mesh's height and radius as the
// reference, so a blade sways the same at every LOD.

import (
	"math"
	"sort"
)

// MaxWiden caps how much a kept piece widens.
const MaxWiden = 2.5

// SourceMesh is a clump as imported: Y up, its base at y = 0, its axis at
// x = z = 0 (phimport.Normalise's frame).
type SourceMesh struct {
	Positions [][3]float32
	Normals   [][3]float32
	UVs       [][2]float32
	Indices   []uint32
}

// Extent is the mesh's height above y = 0 and its reach from the Y axis.
func (s *SourceMesh) Extent() (height, radius float32) {
	for _, p := range s.Positions {
		height = max(height, p[1])
		radius = max(radius, float32(math.Hypot(float64(p[0]), float64(p[2]))))
	}
	return max(height, 1e-3), max(radius, 1e-3)
}

// KindMesh is s as a kit mesh, its wind weights against height and radius,
// with the kind's bend and flutter.
func KindMesh(s *SourceMesh, height, radius, bend, flutter float32) Mesh {
	m := Mesh{Vertices: make([]float32, 0, len(s.Positions)*VertexFloats), Indices: append([]uint32(nil), s.Indices...)}
	for i, p := range s.Positions {
		n := [3]float32{0, 1, 0}
		if i < len(s.Normals) {
			n = s.Normals[i]
		}
		var uv [2]float32
		if i < len(s.UVs) {
			uv = s.UVs[i]
		}
		up := min(max(p[1]/height, 0), 1)
		out := min(float32(math.Hypot(float64(p[0]), float64(p[2])))/radius, 1)
		m.Vertices = append(m.Vertices, p[0], p[1], p[2], n[0], n[1], n[2], uv[0], uv[1], up, out, bend, flutter)
	}
	return m
}

// pieces is s's connected components (triangles joined through shared
// vertices or coincident positions), each its triangle list.
func (s *SourceMesh) pieces() [][]int {
	nv := len(s.Positions)
	parent := make([]int32, nv)
	for i := range parent {
		parent[i] = int32(i)
	}
	find := func(x int32) int32 {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	union := func(a, b int32) {
		if ra, rb := find(a), find(b); ra != rb {
			parent[ra] = rb
		}
	}
	at := map[[3]float32]int32{}
	for i, p := range s.Positions {
		if j, ok := at[p]; ok {
			union(int32(i), j)
		} else {
			at[p] = int32(i)
		}
	}
	nt := len(s.Indices) / 3
	for t := 0; t < nt; t++ {
		a, b, c := int32(s.Indices[3*t]), int32(s.Indices[3*t+1]), int32(s.Indices[3*t+2])
		union(a, b)
		union(b, c)
	}
	index := map[int32]int{}
	var out [][]int
	for t := 0; t < nt; t++ {
		r := find(int32(s.Indices[3*t]))
		k, ok := index[r]
		if !ok {
			k = len(out)
			index[r] = k
			out = append(out, nil)
		}
		out[k] = append(out[k], t)
	}
	return out
}

// Thin is s with about fraction of its triangles: a seeded subset of whole
// pieces, each widened about its own axis (file comment). At least one
// piece is kept — the biggest when the draw keeps none.
func Thin(s *SourceMesh, fraction float64, seed uint64) *SourceMesh {
	if fraction >= 1 || len(s.Indices) == 0 {
		return s
	}
	fraction = math.Max(fraction, 1e-3)
	pcs := s.pieces()
	widen := float32(math.Min(1/math.Sqrt(fraction), MaxWiden))
	keep := make([]int, 0, len(pcs))
	for k := range pcs {
		if float64(hash64(seed, 0x6772_6173, uint64(k))>>11)/float64(1<<53) < fraction {
			keep = append(keep, k)
		}
	}
	if len(keep) == 0 {
		big := 0
		for k := range pcs {
			if len(pcs[k]) > len(pcs[big]) {
				big = k
			}
		}
		keep = []int{big}
	}
	sort.Ints(keep)
	out := &SourceMesh{}
	for _, k := range keep {
		tris := pcs[k]
		remap := map[uint32]uint32{}
		var order []uint32
		var cx, cz float32
		for _, t := range tris {
			for q := 0; q < 3; q++ {
				v := s.Indices[3*t+q]
				if _, ok := remap[v]; !ok {
					remap[v] = 0
					order = append(order, v)
					cx += s.Positions[v][0]
					cz += s.Positions[v][2]
				}
			}
		}
		cx /= float32(len(order))
		cz /= float32(len(order))
		for _, v := range order {
			remap[v] = uint32(len(out.Positions))
			p := s.Positions[v]
			p[0] = cx + (p[0]-cx)*widen
			p[2] = cz + (p[2]-cz)*widen
			out.Positions = append(out.Positions, p)
			if int(v) < len(s.Normals) {
				out.Normals = append(out.Normals, s.Normals[v])
			}
			if int(v) < len(s.UVs) {
				out.UVs = append(out.UVs, s.UVs[v])
			}
		}
		for _, t := range tris {
			for q := 0; q < 3; q++ {
				out.Indices = append(out.Indices, remap[s.Indices[3*t+q]])
			}
		}
	}
	return out
}

// PieceCount is s's number of connected components: a clump of blades has
// many, a pebble or a branch one or two (the stage decimates those instead
// of thinning them).
func (s *SourceMesh) PieceCount() int { return len(s.pieces()) }
