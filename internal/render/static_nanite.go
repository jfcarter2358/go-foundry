package render

// static_nanite.go — a compiled level's opaque surfaces through the GPU-driven
// cull (nanite_cull.go). Phase 1 of the Nanite plan (docs/nanite/PHASE_1.md).
//
// Each draw group (static_batch.go) already holds its surfaces' corners in its
// pool's vertex buffer. Here it is given an index list in CLUSTER order — the
// cut the compiler stored (world/static_clusters.go), or one made now for a
// group the stored cut does not match — so each cluster is a contiguous run of
// indices, and the level's clusters go into one SSBO for the compute cull to
// read. The index lists of a pool's groups are one index buffer, and a
// cluster's draw names where in the pool its indices and its group's corners
// start, so every command of every group of a pool draws from one vertex
// array.
//
// The cull's GROUPS — a run of command slots drawn with one draw state — are
// MATERIALS, not draw groups: every draw group of one material in one pool,
// whichever chunk it came from, is one cull group, one indirect draw and one
// material bin in the visibility buffer, where a level of a thousand
// chunk-and-material groups made a thousand of each. A shadow pass, which
// draws depth alone, has one cull group a pool.
//
// The camera's view keeps the PVS as the first, cheapest filter: the
// surfaces VisibleSurfaces names mark the clusters they are cut into, and only
// those become tiles. The tiles are rebuilt only when that answer changes —
// the camera crossing into another cell, a chunk streaming in or out, a
// texture edit moving a group between the opaque and transparent passes — so
// a camera moving within a cell costs nothing on the CPU at all. The compute
// pass then does the frustum, the normal cone and the two-pass occlusion, per
// cluster.
//
// The shadow passes cull every cluster of every group that casts (not hidden,
// not near-invisible) against the light's frustum — deliberately not the PVS,
// for the reason shadowStaticSurfaces gives.
//
// A group the compiler gave a cluster DAG (Phase 2; world/static_clusters.go)
// brings every level: its index list is every level's triangles over the
// same corner buffer, its clusters every level's, and it is one INSTANCE of
// its own (the identity, always visible) carrying its LOD tree's root and its
// leaf marks, so the cull chooses its cut each pass like a cooked mesh's. The
// PVS marks a group's level-0 clusters by surface as before; a coarser
// cluster is marked when any cluster its group simplified is (propagate), so
// it is a candidate exactly when some of the surface it stands for is.

import (
	"log"
	"sort"

	"foundry/internal/cluster"
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// staticGPU is a static batch set's GPU-cull half.
type staticGPU struct {
	clusters  uint32
	nClusters int
	// surfStart/surfClusters list, per surface, the level-wide clusters it
	// has triangles in (CSR).
	surfStart    []int32
	surfClusters []int32
	// stamp marks the clusters a camera pass's visible surfaces name.
	stamp   []uint32
	stampNo uint32

	main cullBatch
	// mats are the material groups: every draw group of one material in one
	// pool, in the order the level's groups first name them. mainGroups is
	// the material group each of main's cull groups draws.
	mats       []staticMatGroup
	mainGroups []int
	mainSerial uint64
	mainTrans  uint64
	mainValid  bool
	// mainSurfaces is how many opaque surfaces the PVS handed the tiles, for
	// the overlay — what the GPU then culls is not read back.
	mainSurfaces int
	// pass2 is set by a camera pass in two-pass mode, for the occlusion pass
	// to finish.
	pass2     bool
	pass2View cullView
	shadow    cullBatch
	// shadowGrps is the pool each of the shadow pass's cull groups draws.
	shadowGrps []int
	shadowHid  uint64
	shadowHidN int
	shadowOK   bool

	// stored and recut count the groups whose cut came from the compiled
	// file and those cut at load; dags how many had a DAG.
	stored, recut, dags int
	// spheres are the clusters' spheres, CPU-side, for markLive.
	spheres [][4]float32

	// The DAGs: each group's instance (0, the shared identity, for one
	// without), the level's LOD tree nodes, and per cluster the DAG group
	// that made it and the one that consumed it (level-wide numbers, -1
	// for none), for propagate. groupStamp is propagate's scratch.
	groupInst []uint32
	// leafRanges is each DAG group's leaves (level-wide cluster numbers):
	// its range tiles.
	leafRanges [][][2]int32
	// mainDebug is whether the camera's tiles were built for a debug view.
	mainDebug bool
	// visGroups is drawMain's list for the visibility buffer, kept.
	visGroups  []visGroup
	nodes      uint32
	madeBy     []int32
	consumedBy []int32
	groupStamp []uint32
}

// staticMatGroup is one material's draw groups within one pool.
type staticMatGroup struct {
	pool   int
	groups []int
}

// buildStaticGPU gives each group its cluster-order index list and builds the
// level's cluster list. Nil when the cull cannot run.
func buildStaticGPU(s *world.StaticGeometry, sb *staticBatches, planned []world.StaticGroup) *staticGPU {
	if !naniteCull.ready() {
		return nil
	}
	g := &staticGPU{}
	var all []gpuCluster
	var nodes []gpuNode
	insts := []gpuInstance{identityInstance()}
	leaves, dagGroups := uint32(0), int32(0)
	g.groupInst = make([]uint32, len(planned))
	g.leafRanges = make([][][2]int32, len(planned))
	type pair struct{ surf, cl int32 }
	var pairs []pair
	for gi := range planned {
		grp := sb.groups[gi]
		cg, stored := s.StaticClustersFor(&planned[gi])
		if stored {
			g.stored++
		} else {
			g.recut++
		}
		// Which surface each triangle of the group's stream is.
		triStart := make([]int32, len(grp.surfaces)+1)
		for k, si := range grp.surfaces {
			triStart[k+1] = triStart[k] + int32(len(s.Surfaces[si].Geometry.Vertices)/15)
		}
		surfaceOf := func(t uint32) int32 {
			k := sort.Search(len(grp.surfaces), func(i int) bool { return triStart[i+1] > int32(t) })
			return grp.surfaces[k]
		}
		pool := sb.pools[grp.pool]
		indexBase := uint32(len(pool.idx))
		if d := cg.DAG; d != nil {
			g.dags++
			pool.idx = append(pool.idx, d.Indices...)
			grp.clusterFirst, grp.clusterCount = int32(len(all)), int32(len(d.Clusters))
			nodeBase := uint32(len(nodes))
			for ni := range d.Nodes {
				nodes = append(nodes, toGPUNode(&d.Nodes[ni], nodeBase))
			}
			in := identityInstance()
			in.Flags[0] |= instLOD
			in.Flags[1], in.Flags[2] = nodeBase, leaves
			g.groupInst[gi] = uint32(len(insts))
			insts = append(insts, in)
			leaves += uint32(d.Leaves)
			for _, l := range cluster.LeafRanges(d.Nodes) {
				g.leafRanges[gi] = append(g.leafRanges[gi], [2]int32{grp.clusterFirst + l[0], l[1]})
			}
			for ci := range d.Clusters {
				c := &d.Clusters[ci]
				all = append(all, toGPULODCluster(c, d.ClusterLeaf[ci], indexBase, grp.vertBase))
				g.spheres = append(g.spheres, all[len(all)-1].Sphere)
				made, consumed := int32(-1), int32(-1)
				if c.Group >= 0 {
					made = dagGroups + c.Group
				}
				if c.Parent >= 0 {
					consumed = dagGroups + c.Parent
				}
				g.madeBy = append(g.madeBy, made)
				g.consumedBy = append(g.consumedBy, consumed)
				if c.Level != 0 {
					continue
				}
				gc := grp.clusterFirst + int32(ci)
				for _, t := range d.BaseOrder[c.TriFirst : c.TriFirst+c.TriCount] {
					pairs = append(pairs, pair{surfaceOf(t), gc})
				}
			}
			for ci := range d.Clusters {
				dagGroups = max(dagGroups, g.madeBy[int(grp.clusterFirst)+ci]+1, g.consumedBy[int(grp.clusterFirst)+ci]+1)
			}
			continue
		}
		for _, t := range cg.Order {
			pool.idx = append(pool.idx, 3*t, 3*t+1, 3*t+2)
		}
		grp.clusterFirst, grp.clusterCount = int32(len(all)), int32(len(cg.Clusters))
		for ci := range cg.Clusters {
			c := &cg.Clusters[ci]
			all = append(all, toGPUCluster(c, indexBase, grp.vertBase))
			g.spheres = append(g.spheres, all[len(all)-1].Sphere)
			g.madeBy = append(g.madeBy, -1)
			g.consumedBy = append(g.consumedBy, -1)
			gc := grp.clusterFirst + int32(ci)
			for _, t := range cg.Order[c.TriFirst : c.TriFirst+c.TriCount] {
				pairs = append(pairs, pair{surfaceOf(t), gc})
			}
		}
	}
	// Each pool's index lists, as one index buffer its vertex array holds.
	for _, pool := range sb.pools {
		pool.batch.UploadIndices(pool.idx)
		pool.idx = nil
	}
	for _, grp := range sb.groups {
		grp.batch.Ebo = sb.pools[grp.pool].batch.Ebo
	}
	g.mats = staticMatGroups(sb)
	sort.Slice(pairs, func(a, b int) bool {
		if pairs[a].surf != pairs[b].surf {
			return pairs[a].surf < pairs[b].surf
		}
		return pairs[a].cl < pairs[b].cl
	})
	g.surfStart = make([]int32, len(s.Surfaces)+1)
	for i, p := range pairs {
		if i > 0 && p == pairs[i-1] {
			continue
		}
		g.surfClusters = append(g.surfClusters, p.cl)
		g.surfStart[p.surf+1]++
	}
	for i := 1; i < len(g.surfStart); i++ {
		g.surfStart[i] += g.surfStart[i-1]
	}
	g.nClusters = len(all)
	g.stamp = make([]uint32, len(all))
	uploadClusters(&g.clusters, all)
	g.main.clusters, g.shadow.clusters = g.clusters, g.clusters
	g.groupStamp = make([]uint32, dagGroups)
	if leaves > 0 {
		uploadNodes(&g.nodes, nodes)
		g.main.nodes, g.shadow.nodes = g.nodes, g.nodes
		g.main.leaves, g.shadow.leaves = int(leaves), int(leaves)
	}
	g.main.setInstances(insts)
	g.shadow.setInstances(append([]gpuInstance(nil), insts...))
	g.main.ensureHistory(len(all))
	if g.recut > 0 {
		log.Printf("nanite: compiled level: %d clusters in %d groups (%d with LOD, %d materials, %d pools); %d groups had no stored cut and were cut at load (recompile to store it)",
			len(all), len(planned), g.dags, len(g.mats), len(sb.pools), g.recut)
	} else {
		log.Printf("nanite: compiled level: %d clusters in %d groups (%d with LOD, %d materials, %d pools)",
			len(all), len(planned), g.dags, len(g.mats), len(sb.pools))
	}
	return g
}

// staticMatGroups gathers the draw groups by pool and material, in the order
// the groups first name each.
func staticMatGroups(sb *staticBatches) []staticMatGroup {
	type key struct {
		pool int
		mat  gfx.Material
	}
	index := map[key]int{}
	var out []staticMatGroup
	for gi, grp := range sb.groups {
		k := key{grp.pool, grp.key.mat}
		mi, ok := index[k]
		if !ok {
			mi = len(out)
			index[k] = mi
			out = append(out, staticMatGroup{pool: grp.pool})
		}
		out[mi].groups = append(out[mi].groups, gi)
	}
	return out
}

func (g *staticGPU) release() {
	g.main.release()
	g.shadow.release()
	if g.clusters != 0 {
		gl.DeleteBuffers(1, &g.clusters)
		g.clusters = 0
	}
	if g.nodes != 0 {
		gl.DeleteBuffers(1, &g.nodes)
		g.nodes = 0
	}
}

// clustersOf is the clusters surface si has triangles in.
func (g *staticGPU) clustersOf(si int) []int32 {
	if si < 0 || si+1 >= len(g.surfStart) {
		return nil
	}
	return g.surfClusters[g.surfStart[si]:g.surfStart[si+1]]
}

// rebuildMain turns the visible surfaces into the camera pass's tiles: per
// opaque group, the runs of its clusters some visible surface is in.
func (g *staticGPU) rebuildMain(sb *staticBatches, visible []int) {
	g.stampNo++
	if g.stampNo == 0 {
		clear(g.stamp)
		clear(g.groupStamp)
		g.stampNo = 1
	}
	g.mainSurfaces = 0
	for _, si := range visible {
		if si < 0 || si >= len(sb.groupOf) {
			continue
		}
		gi := sb.groupOf[si]
		if gi < 0 || sb.transparent[gi] {
			continue
		}
		g.mainSurfaces++
		for _, c := range g.clustersOf(si) {
			g.stamp[c] = g.stampNo
		}
	}
	g.propagate()
	var tiles []gpuTile
	var groups []cullGroup
	g.mainGroups = g.mainGroups[:0]
	slot := 0
	for mi, mg := range g.mats {
		// One material, so one answer for all its groups.
		if sb.transparent[mg.groups[0]] {
			continue
		}
		start := slot
		for _, gi := range mg.groups {
			tiles = g.mainTiles(sb, gi, tiles, &slot)
		}
		if slot > start {
			groups = append(groups, cullGroup{first: int32(start), count: int32(slot - start)})
			g.mainGroups = append(g.mainGroups, mi)
		}
	}
	g.main.setTiles(tiles, groups, slot)
}

// mainTiles appends draw group gi's candidate tiles: the runs of its clusters
// some visible surface is in.
func (g *staticGPU) mainTiles(sb *staticBatches, gi int, tiles []gpuTile, slot *int) []gpuTile {
	grp := sb.groups[gi]
	if grp.clusterCount == 0 {
		return tiles
	}
	lo, hi := grp.clusterFirst, grp.clusterFirst+grp.clusterCount
	if ranges := g.leafRanges[gi]; len(ranges) > 0 && !naniteDebugActive() {
		// A DAG: range tiles over the leaves any marked cluster is in.
		var marked [][2]int32
		for _, l := range ranges {
			for c := l[0]; c < l[0]+l[1]; c++ {
				if g.stamp[c] == g.stampNo {
					marked = append(marked, l)
					break
				}
			}
		}
		for _, w := range rangeWindows(marked, nil) {
			tiles = appendRangeTile(tiles, uint32(w[0]), uint32(w[1]), g.groupInst[gi], uint32(w[0]), slot)
		}
		lo = hi
	}
	for c := lo; c < hi; {
		if g.stamp[c] != g.stampNo {
			c++
			continue
		}
		end := c + 1
		for end < hi && g.stamp[end] == g.stampNo {
			end++
		}
		tiles = appendTiles(tiles, uint32(c), uint32(end-c), g.groupInst[gi], uint32(c), slot)
		c = end
	}
	return tiles
}

// propagate marks the coarser clusters of every DAG: a cluster made by a
// group whose consumed clusters include a marked one is marked. The clusters
// are in level order within each group's run, so one walk sees every child
// before the clusters its group made.
func (g *staticGPU) propagate() {
	if len(g.groupStamp) == 0 {
		return
	}
	for c := range g.stamp {
		if m := g.madeBy[c]; m >= 0 && g.groupStamp[m] == g.stampNo {
			g.stamp[c] = g.stampNo
		}
		if p := g.consumedBy[c]; p >= 0 && g.stamp[c] == g.stampNo {
			g.groupStamp[p] = g.stampNo
		}
	}
}

// drawCamera is the camera's first (or only) pass over the opaque groups:
// rebuild the candidate tiles if the PVS answer changed, cull, draw. Returns
// the opaque surfaces the PVS handed it and the draw calls made.
func (g *staticGPU) drawCamera(s *world.StaticGeometry, sb *staticBatches, visible []int, frustum Frustum, eye mgl32.Vec3) (int, int) {
	g.pass2 = false
	if serial := s.VisibleSerial(); !g.mainValid || serial != g.mainSerial || sb.transparentSig != g.mainTrans ||
		naniteDebugActive() != g.mainDebug {
		g.rebuildMain(sb, visible)
		g.mainSerial, g.mainTrans, g.mainValid, g.mainDebug = serial, sb.transparentSig, true, naniteDebugActive()
	}
	DebugStats.NaniteClusters += g.main.slots
	g.main.markLive(g.spheres, mgl32.Ident4(), &frustum)
	projView := cameraProjView()
	mode := int32(cullPlain)
	if NaniteOcclusion && NaniteOcclusionBrushes && naniteTwoPass && g.main.slots > 0 {
		mode = cullPass1
		g.pass2 = true
		g.pass2View = cameraCullView(frustum, projView, eye, cullPass2)
	}
	if !g.main.cull(cameraCullView(frustum, projView, eye, mode)) {
		return g.mainSurfaces, 0
	}
	return g.mainSurfaces, g.drawMain(sb)
}

// drawOcclusion is the camera's second pass, after the Hi-Z pyramid is built
// (naniteOcclusionPass). Returns the draw calls made.
func (g *staticGPU) drawOcclusion(sb *staticBatches) int {
	if !g.pass2 {
		return 0
	}
	g.pass2 = false
	if !g.main.cull(g.pass2View) {
		return 0
	}
	return g.drawMain(sb)
}

// mainBatch is the draw group whose batch — draw state and pool — cull group
// i of the camera's passes draws with: its material's first.
func (g *staticGPU) mainBatch(sb *staticBatches, i int) *gfx.Batch {
	return &sb.groups[g.mats[g.mainGroups[i]].groups[0]].batch
}

func (g *staticGPU) drawMain(sb *staticBatches) int {
	if naniteDebugActive() {
		g.main.drawDebug(len(g.mainGroups), func(i int) uint32 { return g.mainBatch(sb, i).Vao }, false)
		return len(g.mainGroups)
	}
	identity := mgl32.Ident4()
	// A visibility-buffer frame draws the groups' IDs instead, shaded later
	// in one pass (nanite_vis.go).
	if naniteVisActive() {
		groups := g.visGroups[:0]
		for i := range g.mainGroups {
			b := g.mainBatch(sb, i)
			i := i
			groups = append(groups, visGroup{mat: &b.Material, vao: b.Vao, vbo: b.Vbo, nbo: b.Nbo, lbo: b.Lbo, ebo: b.Ebo,
				baked:   b.Baked && b.Lbo != 0,
				forward: func() { b.Bind(identity); g.main.drawGroup(i) }})
		}
		g.visGroups = groups
		if naniteVis.drawBatch(&g.main, groups) {
			return len(g.mainGroups)
		}
	}
	prepassBatch(&g.main, len(g.mainGroups),
		func(i int) *gfx.Material { return &g.mainBatch(sb, i).Material },
		func(int) mgl32.Mat4 { return identity },
		func(i int) uint32 { return g.mainBatch(sb, i).Vao }, false)
	calls := 0
	ProfileBegin("nanite: draw")
	g.main.bindIndirect()
	for i := range g.mainGroups {
		if !g.main.groupLive(i) {
			continue
		}
		g.mainBatch(sb, i).Bind(identity)
		g.main.drawGroupBound(i)
		calls++
	}
	g.main.unbindIndirect()
	ProfileEnd()
	gl.BindVertexArray(0)
	return calls
}

// drawShadow casts every casting group's clusters that pass cull, under prog
// (which the cull's compute passes unbind, and this binds back).
func (g *staticGPU) drawShadow(s *world.StaticGeometry, sb *staticBatches, prog uint32, cull *Frustum) {
	if !g.shadowOK || s.HiddenVersion() != g.shadowHid || len(s.HiddenChunks) != g.shadowHidN {
		// Depth alone, so no draw state but the vertex array: a pool's
		// casters are one cull group and one draw.
		var tiles []gpuTile
		var groups []cullGroup
		g.shadowGrps = g.shadowGrps[:0]
		slot := 0
		start, pool := 0, -1
		close := func() {
			if pool >= 0 && slot > start {
				groups = append(groups, cullGroup{first: int32(start), count: int32(slot - start)})
				g.shadowGrps = append(g.shadowGrps, pool)
			}
			start = slot
		}
		for gi, grp := range sb.groups {
			if grp.batch.Alpha < 0.05 || grp.clusterCount == 0 || s.HiddenFor(grp.key.chunk, grp.coveredBy) {
				continue
			}
			if grp.pool != pool {
				close()
				pool = grp.pool
			}
			if ranges := g.leafRanges[gi]; len(ranges) > 0 {
				for _, w := range rangeWindows(ranges, nil) {
					tiles = appendRangeTile(tiles, uint32(w[0]), uint32(w[1]), g.groupInst[gi], 0, &slot)
				}
			} else {
				tiles = appendTiles(tiles, uint32(grp.clusterFirst), uint32(grp.clusterCount), g.groupInst[gi], 0, &slot)
			}
		}
		close()
		g.shadow.setTiles(tiles, groups, slot)
		g.shadowHid, g.shadowHidN, g.shadowOK = s.HiddenVersion(), len(s.HiddenChunks), true
	}
	g.shadow.markLive(g.spheres, mgl32.Ident4(), cull)
	ok := g.shadow.cull(shadowCullView(cull))
	gl.UseProgram(prog)
	if !ok {
		return
	}
	g.shadow.bindIndirect()
	for i, pi := range g.shadowGrps {
		if !g.shadow.groupLive(i) {
			continue
		}
		gl.BindVertexArray(sb.pools[pi].batch.Vao)
		g.shadow.drawGroupBound(i)
	}
	g.shadow.unbindIndirect()
	gl.BindVertexArray(0)
}

// cameraProjView is the camera's projection × view, as the frame set it up.
func cameraProjView() mgl32.Mat4 {
	return gfx.Projection.Mul4(gfx.Cam.Matrix)
}
