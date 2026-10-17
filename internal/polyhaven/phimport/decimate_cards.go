package phimport

// decimate_cards.go — a plant simplified by THINNING its leaves, not by
// collapsing their edges.
//
// Poly Haven's trees and grasses carry their foliage as thousands of tiny
// separate pieces: fir_tree_01's needles are ~2.6 M triangles per tree in
// cards of ~10 triangles each (71,000 cards on one fir_sapling_medium),
// grass_medium_01's blades ~30 triangles each. Decimate's edge collapse
// (decimate.go) cannot shorten a ten-triangle card without destroying it,
// so at a tenth of the budget the cards collapse to slivers and a fir comes
// out as a bare skeleton of branches (docs/regions/PRESETS_2.md has the
// render). The trunk and limbs are a few large connected pieces, which edge
// collapse simplifies well.
//
// So for a CUTOUT slot made of many small pieces — an AGGREGATE: at least
// 64 connected components averaging at most 48 triangles — this keeps a
// deterministic random subset of WHOLE pieces, the fraction its share of
// the budget allows, and enlarges each kept piece about its own centre by
// 1/sqrt(fraction) (capped at 4×), so the foliage keeps roughly its area
// and silhouette with fewer, larger cards — stochastic simplification of
// aggregate detail (Cook, Halstead, Planck & Ryu, 2007). Other slots go
// through Decimate's edge collapse.
//
// Only cutout slots are thinned: a tree's branches are many small pieces
// too (fir_tree_01's bark is 665 separate tubes of ~32 triangles), and
// thinned like needles they came out as a handful of limbs blown up 4×
// about their middles, the needles hanging in the air where the rest had
// been. Foliage is alpha-cut; wood is opaque. And the wood is funded first
// — up to half the target, never less than its proportional share — and
// the foliage takes the rest: proportionally, the fir's branches got 1.5 k
// of a 300 k budget, which edge collapse cannot spread over 665 limbs
// either.
//
// It is opt-in (ModelOptions.ThinFoliage, the polyhaven_plants stage's
// `thin_foliage`) and names its files `_c<tris>` rather than `_t<tris>`, so
// every existing import's meshes and file names are unchanged.

import (
	"math"

	"foundry/internal/model"
	"foundry/internal/procgen/noise"

	"github.com/go-gl/mathgl/mgl32"
)

// Aggregate slot thresholds.
const (
	cardMinPieces      = 64
	cardMaxPieceTris   = 48
	cardMaxEnlargement = 4
)

// DecimateCards is m simplified to about target triangles, aggregate cutout
// slots thinned piece by piece and the rest edge-collapsed (file comment).
// cutout names the slots drawn alpha-tested (ModelImport.Cutout).
func DecimateCards(m *model.ImportMesh, target int, cutout []string) *model.ImportMesh {
	n := m.TriangleCount()
	if target <= 0 || n <= target {
		return m
	}
	slots := max(len(m.Slots), 1)
	slotOf := func(t int) int {
		if t < len(m.TriSlot) && m.TriSlot[t] >= 0 && int(m.TriSlot[t]) < slots {
			return int(m.TriSlot[t])
		}
		return 0
	}
	per := make([][]int, slots)
	for t := 0; t < n; t++ {
		s := slotOf(t)
		per[s] = append(per[s], t)
	}
	cut := map[string]bool{}
	for _, c := range cutout {
		cut[c] = true
	}
	// Each slot's triangles as a mesh of its own, and which are thinned.
	subs := make([]*model.ImportMesh, slots)
	pieces := make([][][]int, slots)
	woodTris, leafTris := 0, 0
	for s, tris := range per {
		if len(tris) == 0 {
			continue
		}
		subs[s] = slotMesh(m, tris, int32(s))
		if s < len(m.Slots) && cut[m.Slots[s].Name] {
			if pc := components(subs[s]); len(pc) >= cardMinPieces && len(tris) <= cardMaxPieceTris*len(pc) {
				pieces[s] = pc
			}
		}
		if pieces[s] != nil {
			leafTris += len(tris)
		} else {
			woodTris += len(tris)
		}
	}
	share := func(budget, tris, of int) int {
		return int(math.Max(4, math.Round(float64(budget)*float64(tris)/float64(of))))
	}
	// The wood first: half the target at most, its proportional share at
	// least (all of it when nothing is thinned).
	woodBudget := min(woodTris, max(target*woodTris/n, target/2))
	if leafTris == 0 {
		woodBudget = target
	}
	res := make([]*model.ImportMesh, slots)
	woodOut := 0
	for s, sub := range subs {
		if sub != nil && pieces[s] == nil {
			res[s] = Decimate(sub, share(woodBudget, sub.TriangleCount(), woodTris))
			woodOut += res[s].TriangleCount()
		}
	}
	// The foliage takes the rest, and what the wood left of its own when
	// edge collapse came in under it.
	if leafTris > 0 {
		leafBudget := target - min(woodOut, woodBudget)
		for s, sub := range subs {
			if sub != nil && pieces[s] != nil {
				res[s] = thinPieces(sub, pieces[s], share(leafBudget, sub.TriangleCount(), leafTris), uint64(s))
			}
		}
	}
	out := &model.ImportMesh{Slots: m.Slots, Files: m.Files}
	for _, r := range res {
		if r != nil {
			appendMesh(out, r)
		}
	}
	return out
}

// slotMesh is triangles tris of m as a mesh of their own (vertices
// compacted), every triangle in slot s.
func slotMesh(m *model.ImportMesh, tris []int, s int32) *model.ImportMesh {
	out := &model.ImportMesh{Slots: m.Slots, Files: m.Files}
	remap := map[uint32]uint32{}
	for _, t := range tris {
		for k := 0; k < 3; k++ {
			v := m.Indices[3*t+k]
			nv, ok := remap[v]
			if !ok {
				nv = uint32(len(out.Positions))
				remap[v] = nv
				copyVertex(out, m, v)
			}
			out.Indices = append(out.Indices, nv)
		}
		out.TriSlot = append(out.TriSlot, s)
	}
	return out
}

// copyVertex appends vertex v of m to out.
func copyVertex(out, m *model.ImportMesh, v uint32) {
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

// appendMesh appends b's geometry to a.
func appendMesh(a, b *model.ImportMesh) {
	base := uint32(len(a.Positions))
	a.Positions = append(a.Positions, b.Positions...)
	if b.Normals != nil {
		a.Normals = append(a.Normals, b.Normals...)
	}
	if b.UVs != nil {
		a.UVs = append(a.UVs, b.UVs...)
	}
	if b.Colors != nil {
		a.Colors = append(a.Colors, b.Colors...)
	}
	for _, i := range b.Indices {
		a.Indices = append(a.Indices, base+i)
	}
	a.TriSlot = append(a.TriSlot, b.TriSlot...)
}

// components is m's connected pieces (triangles sharing a vertex record or
// a position), each its triangle list, in order of first triangle.
func components(m *model.ImportMesh) [][]int {
	nv := len(m.Positions)
	parent := make([]int32, nv)
	for i := range parent {
		parent[i] = int32(i)
	}
	var find func(x int32) int32
	find = func(x int32) int32 {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	union := func(a, b int32) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}
	// Vertex records split at UV and normal seams: join records at one
	// position, so a card is one piece however its seams fall.
	at := map[mgl32.Vec3]int32{}
	for i, p := range m.Positions {
		if j, ok := at[p]; ok {
			union(int32(i), j)
		} else {
			at[p] = int32(i)
		}
	}
	n := m.TriangleCount()
	for t := 0; t < n; t++ {
		a, b, c := int32(m.Indices[3*t]), int32(m.Indices[3*t+1]), int32(m.Indices[3*t+2])
		union(a, b)
		union(b, c)
	}
	index := map[int32]int{}
	var out [][]int
	for t := 0; t < n; t++ {
		r := find(int32(m.Indices[3*t]))
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

// thinPieces keeps whole pieces of m — a seeded random subset holding about
// target triangles — each enlarged about its centre to make up the area of
// the ones dropped.
func thinPieces(m *model.ImportMesh, pieces [][]int, target int, seed uint64) *model.ImportMesh {
	n := m.TriangleCount()
	f := math.Min(1, float64(target)/float64(n))
	grow := float32(math.Min(1/math.Sqrt(f), cardMaxEnlargement))
	out := &model.ImportMesh{Slots: m.Slots, Files: m.Files}
	for k, tris := range pieces {
		// A deterministic draw per piece: the same mesh thins the same way.
		if float64(noise.Hash64(seed, 0x4341_5244, uint64(k))>>11)/float64(1<<53) >= f {
			continue
		}
		var c mgl32.Vec3
		remap := map[uint32]uint32{}
		var order []uint32
		for _, t := range tris {
			for q := 0; q < 3; q++ {
				v := m.Indices[3*t+q]
				if _, ok := remap[v]; !ok {
					remap[v] = 0
					order = append(order, v)
					c = c.Add(m.Positions[v])
				}
			}
		}
		c = c.Mul(1 / float32(len(order)))
		for _, v := range order {
			remap[v] = uint32(len(out.Positions))
			copyVertex(out, m, v)
			p := &out.Positions[len(out.Positions)-1]
			*p = c.Add(p.Sub(c).Mul(grow))
		}
		for _, t := range tris {
			for q := 0; q < 3; q++ {
				out.Indices = append(out.Indices, remap[m.Indices[3*t+q]])
			}
			out.TriSlot = append(out.TriSlot, m.TriSlot[t])
		}
	}
	return out
}
