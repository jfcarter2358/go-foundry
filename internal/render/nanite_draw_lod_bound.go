package render

// nanite_draw_lod_bound.go — the draw bound (cullBatch.drawMax) tightened by
// level of detail (docs/canyon/PERF.md).
//
// The GTX 1070's driver walks an indirect-count draw to its maximum, ~5.6 ns
// a command slot, whatever the count (nanite-draw-bound; nanite_cull.go
// boundDraws). The bound was every slot of every instance in view. A cooked
// mesh's DAG holds about twice its full detail's clusters, and most of its
// tiles are its finest levels — which a placement at a distance can never
// draw: the LOD test takes a cluster only while its PARENT is too coarse,
// and far enough away every fine cluster's parent is fine enough. On the
// generated canyon's overview every one of 6,607 placements was in view, so
// both camera passes walked all 817k slots for the 64k commands they packed.
//
// So an instance in view contributes only the slots of the tiles that could
// draw at its distance. The test, from the shaders' own (lodSelects in
// nanite_cluster_common.glsl): a cluster draws only if its parent is NOT
// fine, i.e. not parentError·s·k <= max(|eye − P| − pr·s, LOD_NEAR), with P
// and pr its parent's LOD sphere, s the instance's largest scale and k the
// pass's LOD factor. Every parent sphere of the instance lies within
// instReach (in the mesh's own units) of its bounding sphere's centre C, and
// the instance's matrix stretches no distance by more than s, so
// |eye − P| − pr·s >= |eye − M·C| − reach·s =: D for all of them — whatever
// the placement has been moved or scaled to since; a tile whose largest parent error
// e has e·s·k <= max(D, LOD_NEAR) therefore draws nothing, and its slots are
// left out. Roots (no parent) always count. Per instance and group the tiles
// are sorted by e with their slots summed before each, so the bound is a
// binary search.
//
// It is an upper bound on what the cull can pack, never below it: the test
// above is the shader's, loosened; streaming only ever draws COARSER than
// the cut (an ancestor of what is not resident), whose tiles have larger
// parent errors and are counted. FOUNDRY_NANITE_DRAW_LOD_BOUND=0 counts
// every slot in view, as before.

import (
	"math"
	"sort"

	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// NaniteDrawLODBound switches the LOD tightening of the draw bound.
var NaniteDrawLODBound = gfx.EnvFlag("FOUNDRY_NANITE_DRAW_LOD_BOUND", true)

// lodNear is the shaders' LOD_NEAR (nanite_cull_common.glsl).
const lodNear = 1e-3

// setBoundLOD gives the next boundDraws the pass's LOD parameters; on false
// it counts every slot in view.
func (b *cullBatch) setBoundLOD(lod naniteLODView, on bool) {
	b.boundLOD, b.boundLODOn = lod, on
}

// maxScaleOf is the shaders' maxScale: the longest of the matrix's axes.
func maxScaleOf(m *[16]float32) float32 {
	ax := func(i int) float32 { return m[i]*m[i] + m[i+1]*m[i+1] + m[i+2]*m[i+2] }
	return float32(math.Sqrt(float64(max(ax(0), ax(4), ax(8)))))
}

// buildLODBound fills the instances' LOD keys from the tiles: called with
// instSlots rebuilt.
func (b *cullBatch) buildLODBound() {
	type tileKey struct {
		key   float32
		slots int32
	}
	// Per instance, per entry of its instSlots list: the tiles' keys.
	keys := make([][][]tileKey, len(b.insts))
	b.instReach = make([]float32, len(b.insts))
	for i := range b.insts {
		keys[i] = make([][]tileKey, len(b.instSlots[i]))
	}
	for ti := range b.tiles {
		t := &b.tiles[ti]
		inst, group := int(t.A[2]), int32(t.B[3])
		if inst >= len(b.insts) {
			continue
		}
		n := int32(rangeSlots)
		if t.B[2] != tileRange {
			n = int32(t.A[1])
		}
		// The instance's sphere is its mesh's, in the mesh's units, as are
		// the clusters' parent spheres.
		c := mgl32.Vec3{b.insts[inst].Sphere[0], b.insts[inst].Sphere[1], b.insts[inst].Sphere[2]}
		key := float32(0)
		for ci := t.A[0]; ci < t.A[0]+t.A[1]; ci++ {
			if int(ci) >= len(b.cpuClusters) {
				key = float32(math.Inf(1)) // not known: counted
				break
			}
			cl := &b.cpuClusters[ci]
			pe := cl.Errs[1]
			if pe < 0 {
				key = float32(math.Inf(1)) // a root: always in a cut
				continue
			}
			key = max(key, pe)
			p := mgl32.Vec3{cl.Parent[0], cl.Parent[1], cl.Parent[2]}
			b.instReach[inst] = max(b.instReach[inst], p.Sub(c).Len()+cl.Parent[3])
		}
		// The entry of its group in the instance's list (built in the same
		// tile order, so it is the last one with that group so far).
		list := b.instSlots[inst]
		for k := len(list) - 1; k >= 0; k-- {
			if list[k].group == group {
				keys[inst][k] = append(keys[inst][k], tileKey{key, n})
				break
			}
		}
	}
	for i := range b.instSlots {
		for k := range b.instSlots[i] {
			ks := keys[i][k]
			sort.Slice(ks, func(a, c int) bool { return ks[a].key < ks[c].key })
			gs := &b.instSlots[i][k]
			gs.lodKey = make([]float32, len(ks))
			gs.lodCum = make([]int32, len(ks)+1)
			for j, tk := range ks {
				gs.lodKey[j] = tk.key
				gs.lodCum[j+1] = gs.lodCum[j] + tk.slots
			}
		}
	}
}

// addLODBound adds instance i's slots that could draw under lod to drawMax.
func (b *cullBatch) addLODBound(i int, lod naniteLODView) {
	in := &b.insts[i]
	m := mgl32.Mat4(in.Model)
	c := m.Mul4x1(mgl32.Vec4{in.Sphere[0], in.Sphere[1], in.Sphere[2], 1}).Vec3()
	s := maxScaleOf(&in.Model)
	d := max(c.Sub(lod.eye).Len()-b.instReach[i]*s, lodNear)
	// A tile draws nothing when key·s·k <= d: count those above.
	limit := d / max(s*lod.k, 1e-30)
	for _, gs := range b.instSlots[i] {
		if len(gs.lodCum) != len(gs.lodKey)+1 {
			b.drawMax[gs.group] += gs.slots
			continue
		}
		j := sort.Search(len(gs.lodKey), func(j int) bool { return gs.lodKey[j] > limit })
		b.drawMax[gs.group] += gs.lodCum[len(gs.lodKey)] - gs.lodCum[j]
	}
}
