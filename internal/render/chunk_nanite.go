package render

// chunk_nanite.go — a chunk's live brushes through the GPU-driven cull
// (nanite_cull.go). Phase 1 of the Nanite plan (docs/nanite/PHASE_1.md).
//
// A compiled level bakes most of its brushes into static surfaces
// (static_nanite.go), but not all of them: a frame-owned chunk — the inside
// of a ship that flies — a chunk that moves, and an authored level played
// uncompiled are drawn live out of their chunk batches (chunk_batch.go). On
// level1 that is the whole opaque world the player starts in. So the chunk
// batches get the cull too: each group's per-brush ranges are cut into
// clusters (never across brushes, so a brush is still the unit that moves and
// is culled alone), the group's buffer is given an index list in cluster
// order, and the clusters of every brush at rest are culled on the GPU — the
// frustum, the normal cone, the two-pass occlusion — under the chunk's own
// matrix, which is the one instance of a chunk's batch.
//
// What stays on the CPU path: a brush a script has moved (it draws under its
// own placement), a transparent material (sorted), and the editor's view,
// where a brush edit rebuilds a group every frame of a drag and a cut per
// rebuild would be a hitch. The cut is made lazily, the first time a group is
// drawn this way after it was rebuilt.

import (
	"encoding/binary"
	"hash/fnv"

	"foundry/internal/cluster"
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// NaniteChunks sends live chunk brushes through the GPU cull. On by default;
// FOUNDRY_NANITE_CHUNKS=0 keeps them on the CPU walk (chunk_batch.go) while
// compiled surfaces and cooked meshes still take the GPU path.
var NaniteChunks = gfx.EnvFlag("FOUNDRY_NANITE_CHUNKS", true)

// naniteChunksActive is whether the frame being drawn sends chunk brushes
// through the GPU cull: off in the editor's view. Set by DrawWorld.
var naniteChunksActive bool

// chunkGPU is one chunk batch's GPU-cull half.
type chunkGPU struct {
	clusters uint32
	n        int
	// sig is the groups (by fingerprint) it was built for.
	sig uint64
	// groupFirst[gi] is group gi's first cluster in the chunk's list;
	// clusterBrush the brush each cluster is of.
	groupFirst   []int32
	groupCount   []int32
	clusterBrush []int32
	// spheres are the clusters' spheres in chunk space, for markLive.
	spheres [][4]float32

	main, shadow cullBatch
	mainGroups   []int
	shadowGroups []int
	mainKey      uint64
	shadowKey    uint64
	model        mgl32.Mat4
	modelSet     bool
	pass2        bool
	pass2View    cullView
	pass2Model   mgl32.Mat4
}

// naniteChunkPending lists the chunks whose first pass ran this frame, for
// naniteOcclusionPass.
var naniteChunkPending []chunkPass2

type chunkPass2 struct {
	cb *ChunkBatch
	g  *chunkGPU
}

// groupsSig fingerprints a chunk batch's groups.
func (cb *ChunkBatch) groupsSig() uint64 {
	h := uint64(14695981039346656037)
	for _, g := range cb.groups {
		h = mix(h, g.fp)
		h = mix(h, uint64(g.batch.Vao))
	}
	return mix(h, uint64(len(cb.groups)))
}

// chunkGPUMinTriangles is the smallest chunk the GPU cull takes: below it the
// cull's fixed cost (its dispatches, a prepass) is more than the CPU walk it
// replaces — a handful of small moving chunks, as level3's vehicles are.
// Kept low on purpose: what the cull buys a chunk is mostly the prepass, and
// that pays by the pixel, not the triangle — level1's hangar is a few hundred
// triangles that fill the screen (docs/nanite/PHASE_1.md).
var chunkGPUMinTriangles = 200

// chunkGPUFor returns cb's GPU half, cutting the groups (again) if they have
// changed since. Nil when the cull cannot run, or the chunk is too small to
// be worth it.
func chunkGPUFor(cb *ChunkBatch) *chunkGPU {
	if !NaniteClusters || !NaniteChunks || !naniteChunksActive || !gfx.GeometryUploadsEnabled || !naniteCull.ready() {
		return nil
	}
	tris := 0
	for _, g := range cb.groups {
		tris += int(g.batch.Verts) / 3
	}
	if tris < chunkGPUMinTriangles {
		return nil
	}
	sig := cb.groupsSig()
	if cb.gpu != nil && cb.gpu.sig == sig {
		return cb.gpu
	}
	g := cb.gpu
	if g == nil {
		g = &chunkGPU{}
		cb.gpu = g
	}
	g.sig = sig
	g.groupFirst = g.groupFirst[:0]
	g.groupCount = g.groupCount[:0]
	g.clusterBrush = g.clusterBrush[:0]
	g.spheres = g.spheres[:0]
	var all []gpuCluster
	for _, grp := range cb.groups {
		first := int32(len(all))
		var idx []uint32
		tri := uint32(0)
		// The group's corner soup, face by face, as rebuild laid it out.
		var soup []float32
		for k := 0; k < len(grp.keep); k += 2 {
			soup = append(soup, grp.keep[k]...)
		}
		for ri := range grp.firsts {
			f, n := int(grp.firsts[ri]), int(grp.counts[ri])
			if (f+n)*5 > len(soup) {
				continue
			}
			// In order, never reordered: a brush is a few dozen triangles,
			// already one compact cluster or two, and its faces' draw order
			// is what settles a depth tie with whatever lies on them (trim,
			// a panel on a wall) — reordered, a tie flips and a capture
			// changes along every such seam.
			r := cluster.BuildSequential(cluster.Mesh{Positions: soup[f*5 : (f+n)*5], Stride: 5}, world.StaticClusterOptions)
			for _, t := range r.Order {
				v := uint32(f) + 3*t
				idx = append(idx, v, v+1, v+2)
			}
			for _, c := range r.Clusters {
				c.TriFirst += tri
				all = append(all, toGPUCluster(&c, 0, 0))
				g.spheres = append(g.spheres, all[len(all)-1].Sphere)
				g.clusterBrush = append(g.clusterBrush, grp.brushOf[ri])
			}
			tri += uint32(len(r.Order))
		}
		grp.batch.UploadIndices(idx)
		g.groupFirst = append(g.groupFirst, first)
		g.groupCount = append(g.groupCount, int32(len(all))-first)
	}
	g.n = len(all)
	uploadClusters(&g.clusters, all)
	g.main.clusters, g.shadow.clusters = g.clusters, g.clusters
	g.main.ensureHistory(len(all))
	g.main.forgetHistory()
	g.mainKey, g.shadowKey = 0, 0
	g.modelSet = false
	return g
}

func (g *chunkGPU) release() {
	g.main.release()
	g.shadow.release()
	if g.clusters != 0 {
		gl.DeleteBuffers(1, &g.clusters)
		g.clusters = 0
	}
}

// restKey hashes which brushes of ch are not at rest, and which groups are
// in the pass, so tiles are rebuilt only when either changes.
func restKey(ch *world.Chunk, include func(gi int) bool, groups int) uint64 {
	h := fnv.New64a()
	var b [4]byte
	for bi := range ch.Brushes {
		if !ch.Brushes[bi].AtRest() {
			binary.LittleEndian.PutUint32(b[:], uint32(bi))
			h.Write(b[:])
		}
	}
	h.Write([]byte{0xff})
	for gi := 0; gi < groups; gi++ {
		if include(gi) {
			binary.LittleEndian.PutUint32(b[:], uint32(gi))
			h.Write(b[:])
		}
	}
	return h.Sum64() | 1
}

// tilesFor builds tiles over the clusters of the included groups whose
// brushes are at rest.
func (g *chunkGPU) tilesFor(ch *world.Chunk, cb *ChunkBatch, include func(gi int) bool) ([]gpuTile, []cullGroup, []int, int) {
	var tiles []gpuTile
	var groups []cullGroup
	var which []int
	slot := 0
	for gi := range cb.groups {
		if gi >= len(g.groupFirst) || !include(gi) {
			continue
		}
		start := slot
		lo, hi := g.groupFirst[gi], g.groupFirst[gi]+g.groupCount[gi]
		for c := lo; c < hi; {
			if bi := g.clusterBrush[c]; bi >= int32(len(ch.Brushes)) || !ch.Brushes[bi].AtRest() {
				c++
				continue
			}
			end := c + 1
			for end < hi && g.clusterBrush[end] < int32(len(ch.Brushes)) && ch.Brushes[g.clusterBrush[end]].AtRest() {
				end++
			}
			tiles = appendTiles(tiles, uint32(c), uint32(end-c), 0, uint32(c), &slot)
			c = end
		}
		if slot > start {
			groups = append(groups, cullGroup{first: int32(start), count: int32(slot - start)})
			which = append(which, gi)
		}
	}
	return tiles, groups, which, slot
}

// setModel makes the batch's one instance the chunk's matrix.
func (b *cullBatch) setModel(m mgl32.Mat4) {
	in := identityInstance()
	in.Model = m
	if !coneSafe(m) {
		in.Flags[0] |= instNoCone
	}
	if len(b.insts) == 1 && b.insts[0] == in {
		return
	}
	b.setInstances([]gpuInstance{in})
}

// drawCamera is the camera's first (or only) pass over the chunk's opaque,
// unmoved brushes. Returns the draw calls made.
func (g *chunkGPU) drawCamera(ch *world.Chunk, cb *ChunkBatch, frustum Frustum, chunkModel mgl32.Mat4) int {
	opaque := func(gi int) bool { return !cb.groups[gi].batch.IsTransparent() }
	if k := restKey(ch, opaque, len(cb.groups)); k != g.mainKey {
		tiles, groups, which, slots := g.tilesFor(ch, cb, opaque)
		g.main.setTiles(tiles, groups, slots)
		g.mainGroups = which
		g.mainKey = k
	}
	g.main.setModel(chunkModel)
	g.main.markLive(g.spheres, chunkModel, &frustum)
	DebugStats.NaniteClusters += g.main.slots
	projView := cameraProjView()
	eye := gfx.Cam.Position
	mode := int32(cullPlain)
	g.pass2 = false
	if NaniteOcclusionBrushes && naniteTwoPass && g.main.slots > 0 {
		mode = cullPass1
		g.pass2 = true
		g.pass2View = cameraCullView(frustum, projView, eye, cullPass2)
		g.pass2Model = chunkModel
		naniteChunkPending = append(naniteChunkPending, chunkPass2{cb: cb, g: g})
	}
	if !g.main.cull(cameraCullView(frustum, projView, eye, mode)) {
		gl.UseProgram(gfx.Program)
		return 0
	}
	return g.drawMain(cb, chunkModel)
}

func (g *chunkGPU) drawMain(cb *ChunkBatch, model mgl32.Mat4) int {
	prepassBatch(&g.main, len(g.mainGroups),
		func(i int) *gfx.Material { return &cb.groups[g.mainGroups[i]].batch.Material },
		func(int) mgl32.Mat4 { return model },
		func(i int) uint32 { return cb.groups[g.mainGroups[i]].batch.Vao }, false)
	ProfileBegin("nanite: draw")
	defer ProfileEnd()
	calls := 0
	for i, gi := range g.mainGroups {
		if !g.main.groupLive(i) {
			continue
		}
		cb.groups[gi].batch.Bind(model)
		g.main.drawGroup(i)
		calls++
	}
	gl.BindVertexArray(0)
	return calls
}

// drawOcclusion is the second pass.
func (g *chunkGPU) drawOcclusion(cb *ChunkBatch) {
	if !g.pass2 {
		return
	}
	g.pass2 = false
	if g.main.cull(g.pass2View) {
		g.drawMain(cb, g.pass2Model)
	}
}

// drawShadow casts the chunk's unmoved brushes' clusters that pass cull.
// prog is bound again after the cull; *current is the model matrix bound to
// modelUniform, which this sets to the chunk's.
func (g *chunkGPU) drawShadow(ch *world.Chunk, cb *ChunkBatch, chunkModel mgl32.Mat4, cull *Frustum, prog uint32,
	modelUniform int32, current *mgl32.Mat4) {
	casts := func(gi int) bool { return cb.groups[gi].batch.Alpha >= 0.05 }
	if k := restKey(ch, casts, len(cb.groups)); k != g.shadowKey {
		tiles, groups, which, slots := g.tilesFor(ch, cb, casts)
		g.shadow.setTiles(tiles, groups, slots)
		g.shadowGroups = which
		g.shadowKey = k
	}
	g.shadow.setModel(chunkModel)
	g.shadow.markLive(g.spheres, chunkModel, cull)
	ok := g.shadow.cull(shadowCullView(cull))
	gl.UseProgram(prog)
	if !ok {
		return
	}
	if chunkModel != *current {
		*current = chunkModel
		gl.UniformMatrix4fv(modelUniform, 1, false, &current[0])
	}
	for i, gi := range g.shadowGroups {
		if !g.shadow.groupLive(i) {
			continue
		}
		gl.BindVertexArray(cb.groups[gi].batch.Vao)
		g.shadow.drawGroup(i)
	}
	gl.BindVertexArray(0)
}
