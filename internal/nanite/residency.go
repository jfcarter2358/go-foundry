package nanite

// residency.go — which of a mesh's pages are loaded, and the cut that allows.
// Phase 3 of the Nanite plan (docs/nanite/PHASE_3.md), component 9.
//
// The GPU's LOD selection is one test per cluster (internal/cluster's
// dag.go): draw a cluster when its own error is fine enough and its parent
// group's is not. Every cluster of a group reads the same numbers, error only
// grows going up, and so exactly one cluster is drawn along every path from
// the full detail to a root — a complete, crack-free cut, whatever the eye.
//
// Streaming keeps that test and changes the numbers it reads. Call a group
// REFINABLE when it may be drawn finer than itself — the clusters it
// consumed instead of the ones it made — and say it is refinable exactly
// when
//
//   - every page holding a cluster it consumed is resident, and
//   - every group that consumed a cluster it made is refinable too.
//
// The second rule is what keeps the cut whole: refining a group while the
// group above it is not would draw the fine version of a surface under the
// coarse version of the same surface. Then a group that is NOT refinable gets
// error 0 in the cut's eyes: the clusters it made always count as fine enough
// (so they are drawn, when their own parent allows), and the clusters it
// consumed always see a parent fine enough to draw instead (so they are never
// drawn — which is why it does not matter that they are not loaded). A
// refinable group keeps its cooked error.
//
// Those effective errors still grow going up: below a refinable group every
// group is either refinable, with its cooked error no larger than the one
// above, or not, with 0. So the per-cluster test, the tree walk's skips
// (cluster.RefreshErrors recomputes the bounds) and the crack-freedom
// argument all hold unchanged, for any set of resident pages — and the root
// pages, always resident, make the roots alone a whole mesh to fall back on.
// The coarser parent is what is drawn until a page arrives.
//
// What to load is the GPU's to say: a cluster drawn although its own cooked
// error is too coarse for the eye was drawn only because the group that made
// it is not refinable, and it asks for that group's pages (RequestPage); the
// renderer reads the asks back a few frames later (render/nanite_stream.go).

import (
	"slices"
	"sort"

	"foundry/internal/cluster"
)

// Residency tracks a mesh's resident pages and the effective errors they
// allow. Not safe for concurrent use: the renderer keeps one per mesh on its
// main thread.
type Residency struct {
	m *Mesh
	// Resident is per page.
	Resident []bool
	groups   []resGroup
	// order is the groups, coarsest first: consumers before what they
	// consume.
	order []int32
	// made and used are each cluster's group: the one that made it and the
	// one that consumed it (-1: none — level 0, or a root).
	made, used []int32
	refinable  []bool
}

type resGroup struct {
	// pages hold its inputs; consumers took its outputs.
	pages     []int32
	consumers []int32
	level     int32
}

// NewResidency is m's residency with only its root pages resident.
func NewResidency(m *Mesh) *Residency {
	r := &Residency{m: m, Resident: make([]bool, len(m.Pages)),
		made: make([]int32, len(m.Clusters)), used: make([]int32, len(m.Clusters))}
	slot := m.slotOf()
	index := map[groupKey]int32{}
	groupOf := func(c int32, g int32) int32 {
		if g < 0 {
			return -1
		}
		k := groupKey{slot[c], g}
		gi, ok := index[k]
		if !ok {
			gi = int32(len(r.groups))
			index[k] = gi
			r.groups = append(r.groups, resGroup{level: -1})
		}
		return gi
	}
	for ci := range m.Clusters {
		c := &m.Clusters[ci]
		r.made[ci] = groupOf(int32(ci), c.Group)
		r.used[ci] = groupOf(int32(ci), c.Parent)
		if g := r.made[ci]; g >= 0 {
			r.groups[g].level = max(r.groups[g].level, c.Level)
		}
	}
	for ci := range m.Clusters {
		if g := r.used[ci]; g >= 0 {
			p := m.PageOf(int32(ci))
			if !slices.Contains(r.groups[g].pages, p) {
				r.groups[g].pages = append(r.groups[g].pages, p)
			}
			if mg := r.made[ci]; mg >= 0 && !slices.Contains(r.groups[mg].consumers, g) {
				r.groups[mg].consumers = append(r.groups[mg].consumers, g)
			}
		}
	}
	for gi := range r.groups {
		slices.Sort(r.groups[gi].pages)
	}
	r.order = make([]int32, len(r.groups))
	for i := range r.order {
		r.order[i] = int32(i)
	}
	// A consumer's outputs are made at a higher level than anything it
	// consumed, so coarsest-first by level is consumers-first.
	sort.SliceStable(r.order, func(a, b int) bool { return r.groups[r.order[a]].level > r.groups[r.order[b]].level })
	r.refinable = make([]bool, len(r.groups))
	for pi := range m.Pages {
		r.Resident[pi] = m.Pages[pi].Root
	}
	r.Update()
	return r
}

// Update works out which groups are refinable from Resident (see the file
// comment). Call it after changing Resident.
func (r *Residency) Update() {
	for _, gi := range r.order {
		g := &r.groups[gi]
		ok := true
		for _, p := range g.pages {
			if !r.Resident[p] {
				ok = false
				break
			}
		}
		for _, c := range g.consumers {
			if !ok {
				break
			}
			ok = r.refinable[c]
		}
		r.refinable[gi] = ok
	}
}

// Effective is cluster c's own and parent error as the cut should see them
// with what is resident: the cooked ones, or 0 where a group may not be
// refined. A root's parent error stays < 0.
func (r *Residency) Effective(c int32) (own, parent float32) {
	cl := &r.m.Clusters[c]
	own, parent = cl.Error, cl.ParentError
	if g := r.made[c]; g >= 0 && !r.refinable[g] {
		own = 0
	}
	if g := r.used[c]; g >= 0 && !r.refinable[g] {
		parent = 0
	}
	return
}

// Drawable reports whether cluster c can ever be in the cut now: its
// consuming group is refinable (or it is a root). Such a cluster's page is
// resident.
func (r *Residency) Drawable(c int32) bool {
	g := r.used[c]
	return g < 0 || r.refinable[g]
}

// RequestPage is the first page cluster c asks for when it is drawn too
// coarse: one holding what its making group consumed; -1 at level 0.
func (r *Residency) RequestPage(c int32) int32 {
	g := r.made[c]
	if g < 0 || len(r.groups[g].pages) == 0 {
		return -1
	}
	return r.groups[g].pages[0]
}

// GroupPages is every page the group that made cluster c needs resident to
// be refined (nil at level 0): what a request for RequestPage(c) loads.
func (r *Residency) GroupPages(c int32) []int32 {
	g := r.made[c]
	if g < 0 {
		return nil
	}
	return r.groups[g].pages
}

// PagesNeededWith is the pages that page p's request should bring in with
// it: every page of every group whose inputs p holds a part of. A group
// split across pages (a unit too big for one) needs all of them at once.
func (r *Residency) PagesNeededWith(p int32) []int32 {
	pg := &r.m.Pages[p]
	var out []int32
	for c := pg.ClusterFirst; c < pg.ClusterFirst+pg.ClusterCount; c++ {
		if g := r.used[c]; g >= 0 {
			for _, q := range r.groups[g].pages {
				if !slices.Contains(out, q) {
					out = append(out, q)
				}
			}
		}
	}
	return out
}

// RefinableGroups counts the groups that may be refined now, of all.
func (r *Residency) RefinableGroups() (refinable, all int) {
	for _, ok := range r.refinable {
		if ok {
			refinable++
		}
	}
	return refinable, len(r.refinable)
}

// EffectiveNodes is the mesh's tree with its errors refreshed for what is
// resident (cluster.RefreshErrors); out is reused when it has room.
func (r *Residency) EffectiveNodes(out []cluster.LODNode) []cluster.LODNode {
	out = append(out[:0], r.m.Nodes...)
	cluster.RefreshErrors(out,
		func(c int32) float32 { o, _ := r.Effective(c); return o },
		func(c int32) float32 { _, p := r.Effective(c); return p })
	return out
}
