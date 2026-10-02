package render

// static_nanite.go — a compiled level's opaque surfaces through the GPU-driven
// cull (nanite_cull.go). Phase 1 of the Nanite plan (docs/nanite/PHASE_1.md).
//
// Each draw group (static_batch.go) already holds its surfaces' corners in one
// vertex buffer. Here it is given an index list in CLUSTER order — the cut the
// compiler stored (world/static_clusters.go), or one made now for a group the
// stored cut does not match — so each cluster is a contiguous run of indices,
// and the level's clusters go into one SSBO for the compute cull to read.
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

import (
	"log"
	"sort"

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

	main       cullBatch
	mainGroups []int // the static group each of main's cull groups draws
	mainSerial uint64
	mainTrans  uint64
	mainValid  bool
	// mainSurfaces is how many opaque surfaces the PVS handed the tiles, for
	// the overlay — what the GPU then culls is not read back.
	mainSurfaces int
	// pass2 is set by a camera pass in two-pass mode, for the occlusion pass
	// to finish.
	pass2      bool
	pass2View  cullView
	shadow     cullBatch
	shadowGrps []int
	shadowHid  uint64
	shadowHidN int
	shadowOK   bool

	// stored and recut count the groups whose cut came from the compiled
	// file and those cut at load.
	stored, recut int
	// spheres are the clusters' spheres, CPU-side, for markLive.
	spheres [][4]float32
}

// buildStaticGPU gives each group its cluster-order index list and builds the
// level's cluster list. Nil when the cull cannot run.
func buildStaticGPU(s *world.StaticGeometry, sb *staticBatches, planned []world.StaticGroup) *staticGPU {
	if !naniteCull.ready() {
		return nil
	}
	g := &staticGPU{}
	var all []gpuCluster
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
		idx := make([]uint32, 0, 3*len(cg.Order))
		for _, t := range cg.Order {
			idx = append(idx, 3*t, 3*t+1, 3*t+2)
		}
		grp.batch.UploadIndices(idx)
		grp.clusterFirst, grp.clusterCount = int32(len(all)), int32(len(cg.Clusters))

		// Which surface each triangle of the group's stream is.
		triStart := make([]int32, len(grp.surfaces)+1)
		for k, si := range grp.surfaces {
			triStart[k+1] = triStart[k] + int32(len(s.Surfaces[si].Geometry.Vertices)/15)
		}
		for ci := range cg.Clusters {
			c := &cg.Clusters[ci]
			all = append(all, toGPUCluster(c, 0, 0))
			g.spheres = append(g.spheres, all[len(all)-1].Sphere)
			gc := grp.clusterFirst + int32(ci)
			for _, t := range cg.Order[c.TriFirst : c.TriFirst+c.TriCount] {
				k := sort.Search(len(grp.surfaces), func(i int) bool { return triStart[i+1] > int32(t) })
				pairs = append(pairs, pair{grp.surfaces[k], gc})
			}
		}
	}
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
	g.main.setInstances([]gpuInstance{identityInstance()})
	g.shadow.setInstances([]gpuInstance{identityInstance()})
	g.main.ensureHistory(len(all))
	if g.recut > 0 {
		log.Printf("nanite: compiled level: %d clusters in %d groups; %d groups had no stored cut and were cut at load (recompile to store it)",
			len(all), len(planned), g.recut)
	} else {
		log.Printf("nanite: compiled level: %d clusters in %d groups", len(all), len(planned))
	}
	return g
}

func (g *staticGPU) release() {
	g.main.release()
	g.shadow.release()
	if g.clusters != 0 {
		gl.DeleteBuffers(1, &g.clusters)
		g.clusters = 0
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
	var tiles []gpuTile
	var groups []cullGroup
	g.mainGroups = g.mainGroups[:0]
	slot := 0
	for gi, grp := range sb.groups {
		if sb.transparent[gi] || grp.clusterCount == 0 {
			continue
		}
		start := slot
		lo, hi := grp.clusterFirst, grp.clusterFirst+grp.clusterCount
		for c := lo; c < hi; {
			if g.stamp[c] != g.stampNo {
				c++
				continue
			}
			end := c + 1
			for end < hi && g.stamp[end] == g.stampNo {
				end++
			}
			tiles = appendTiles(tiles, uint32(c), uint32(end-c), 0, uint32(c), &slot)
			c = end
		}
		if slot > start {
			groups = append(groups, cullGroup{first: int32(start), count: int32(slot - start)})
			g.mainGroups = append(g.mainGroups, gi)
		}
	}
	g.main.setTiles(tiles, groups, slot)
}

// drawCamera is the camera's first (or only) pass over the opaque groups:
// rebuild the candidate tiles if the PVS answer changed, cull, draw. Returns
// the opaque surfaces the PVS handed it and the draw calls made.
func (g *staticGPU) drawCamera(s *world.StaticGeometry, sb *staticBatches, visible []int, frustum Frustum, eye mgl32.Vec3) (int, int) {
	g.pass2 = false
	if serial := s.VisibleSerial(); !g.mainValid || serial != g.mainSerial || sb.transparentSig != g.mainTrans {
		g.rebuildMain(sb, visible)
		g.mainSerial, g.mainTrans, g.mainValid = serial, sb.transparentSig, true
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

func (g *staticGPU) drawMain(sb *staticBatches) int {
	identity := mgl32.Ident4()
	prepassBatch(&g.main, len(g.mainGroups),
		func(i int) *gfx.Material { return &sb.groups[g.mainGroups[i]].batch.Material },
		func(int) mgl32.Mat4 { return identity },
		func(i int) uint32 { return sb.groups[g.mainGroups[i]].batch.Vao }, false)
	calls := 0
	ProfileBegin("nanite: draw")
	for i, gi := range g.mainGroups {
		if !g.main.groupLive(i) {
			continue
		}
		sb.groups[gi].batch.Bind(identity)
		g.main.drawGroup(i)
		calls++
	}
	ProfileEnd()
	gl.BindVertexArray(0)
	return calls
}

// drawShadow casts every casting group's clusters that pass cull, under prog
// (which the cull's compute passes unbind, and this binds back).
func (g *staticGPU) drawShadow(s *world.StaticGeometry, sb *staticBatches, prog uint32, cull *Frustum) {
	if !g.shadowOK || s.HiddenVersion() != g.shadowHid || len(s.HiddenChunks) != g.shadowHidN {
		var tiles []gpuTile
		var groups []cullGroup
		g.shadowGrps = g.shadowGrps[:0]
		slot := 0
		for gi, grp := range sb.groups {
			if grp.batch.Alpha < 0.05 || grp.clusterCount == 0 || s.HiddenFor(grp.key.chunk, grp.coveredBy) {
				continue
			}
			start := slot
			tiles = appendTiles(tiles, uint32(grp.clusterFirst), uint32(grp.clusterCount), 0, 0, &slot)
			groups = append(groups, cullGroup{first: int32(start), count: int32(slot - start)})
			g.shadowGrps = append(g.shadowGrps, gi)
		}
		g.shadow.setTiles(tiles, groups, slot)
		g.shadowHid, g.shadowHidN, g.shadowOK = s.HiddenVersion(), len(s.HiddenChunks), true
	}
	g.shadow.markLive(g.spheres, mgl32.Ident4(), cull)
	ok := g.shadow.cull(shadowCullView(cull))
	gl.UseProgram(prog)
	if !ok {
		return
	}
	for i, gi := range g.shadowGrps {
		if !g.shadow.groupLive(i) {
			continue
		}
		gl.BindVertexArray(sb.groups[gi].batch.Vao)
		g.shadow.drawGroup(i)
	}
	gl.BindVertexArray(0)
}

// cameraProjView is the camera's projection × view, as the frame set it up.
func cameraProjView() mgl32.Mat4 {
	return gfx.Projection.Mul4(gfx.Cam.Matrix)
}
