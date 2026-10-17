package render

// nanite_scenery.go — scenery placements of cooked Nanite meshes (.fnm),
// drawn by the GPU-driven cluster cull (nanite_cull.go). Phase 1 of the
// Nanite plan (docs/nanite/PHASE_1.md).
//
// Every cooked mesh any placement uses goes into ONE shared pool: one vertex
// buffer, one normal buffer, one index buffer, one cluster list, each mesh at
// an offset in them (a cluster's draw range carries its mesh's first index
// and base vertex). Every placement is one instance record — its matrix and
// its mesh's bounding sphere — in one buffer that the cull reads and the
// vertex stage reads its matrix from (locations 7-10, the instanced scenery
// path's, picked out per draw by the command's base instance). The groups are
// one per distinct material a placement's slot resolves to (a placement can
// override a slot's texture), so two hundred rocks in three looks are three
// indirect multi-draws whatever the camera sees.
//
// Each mesh brings its whole cluster DAG (Phase 2): every level's clusters
// into the cluster list, every level's triangles into the index buffer (over
// the same vertices — a coarser level only indexes fewer of them), and its
// LOD tree into the node list, each placement getting its own run of leaf
// marks. Every level of a slot is in the slot's tiles — range tiles over its
// leaves (appendLeafTiles) — so the cull chooses the cut each pass
// (nanite_cull.go, "Level of detail").
//
// The pool and the tiles are rebuilt only when the set of placements, the
// meshes they use or their overrides change; a placement that moves (the
// editor's gizmo) only re-sends the instance records.
//
// Since Phase 3 (docs/nanite/PHASE_3.md) the pool is a PAGE pool by default
// (nanite_stream.go): the vertices and indices are the meshes' pages, each in
// a fixed slot, streamed in and out within a memory budget, and a cluster's
// record says where its page sits. The cluster list, the trees and the
// instances are as before; a range tile never spans two pages (rangeWindows),
// since only a page's clusters are one contiguous run of the index buffer.
// FOUNDRY_NANITE_STREAM=0 keeps the Phase 2 pool, every mesh decoded whole
// (nanite.Mesh.Flat) and uploaded with the level.
//
// Wind: a placement with world.Scenery.Wind bends in the vertex stage
// (resources/shaders/wind_common.glsl) — its instance record carries the
// wind, every stage that places a cooked vertex adds the same offset, and the
// cull pads its spheres by how far it can swing. Its normals are the still
// mesh's, and it writes no motion vectors of its own (TAA reprojects its
// leaves with the camera's motion, as the classic foliage path's).
//
// Draw distances and impostors (nanite_draw_distance.go): a member is a
// placement's mesh or, for a placement with an impostor
// (world.Scenery.ImpostorMesh), its impostor's cards — a second member of
// the same placement, a second mesh in the pool, each with the distance
// window it is drawn over in its instance record.
//
// What is not here yet: translucent slots (a cooked mesh draws in the opaque
// pass, alpha-to-coverage cutouts and all — a texture with partial alpha is
// not sorted as a classic model's is), and skinning (a cooked mesh is static,
// bar the wind).

import (
	"encoding/binary"
	"hash/maphash"
	"log"
	"math"
	"sort"
	"unsafe"

	"foundry/internal/cluster"
	"foundry/internal/gfx"
	"foundry/internal/nanite"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

func init() {
	world.NaniteGPUAvailable = func() bool { return world.NaniteMeshes && naniteCull.ready() }
}

// naniteSceneryRenderer is the level's cooked placements on the GPU.
type naniteSceneryRenderer struct {
	// The pool.
	meshes   map[*nanite.Mesh]*naniteMeshSlot
	poolList []*nanite.Mesh
	vao      uint32
	vbo, nbo uint32
	ebo      uint32
	// cbo is the pool's vertex colours (RGBA8 a vertex), when any mesh in
	// it has them (docs/canyon/PHASE_2.md); 0 otherwise.
	cbo      uint32
	clusters uint32
	nodes    uint32
	// cpuClusters is the cluster records for the draw bound's LOD keys
	// (nanite_draw_lod_bound.go): as uploaded for a flat pool; for a page
	// pool with their cooked errors, never the residency's
	// (staticClusterRecords).
	cpuClusters []gpuCluster

	// preparedFrame is the DrawWorld frame (naniteStreamFrame) prepare last
	// brought everything up to date in, and prepared what it answered: a
	// frame's passes — a camera pass or two, and a shadow view per cascade or
	// virtual-map rectangle — share one look at the level, which nothing in
	// DrawWorld moves.
	preparedFrame uint32
	preparedLvl   *world.Level
	prepared      bool

	// members are the level's placements drawn here, by index, and
	// memberImp whether each is the placement's impostor rather than its
	// mesh (nanite_draw_distance.go); sig what the groups and tiles were
	// built from.
	lvl       *world.Level
	members   []int
	memberImp []bool
	sig       uint64

	batch  cullBatch
	groups []gfx.Material
	insts  []gpuInstance
	// spheres are the placements' world spheres, for the CPU's look ahead of
	// a pass (inView), and reach how far each one's wind can swing it in
	// air of strength 1 (0: still).
	spheres []mgl32.Vec4
	reach   []float32
	// inMask is which placements the last inView found in its frustum.
	inMask []bool
	// camMay is, per member, whether its draw window can hold the camera's
	// distance this frame (placementMayDraw): what a camera pass's inView
	// keeps of the frustum's.
	camMay []bool
	// placed is each member's placedInstance as last worked out and placedAt
	// the matrix it was worked out under: a member that has not moved keeps
	// it, rather than a coneSafe test (a 3x3 determinant and three dot
	// products) a member a frame. Cleared by rebuild.
	placed   []gpuInstance
	placedAt []mgl32.Mat4
	// leafBase is each placement's first leaf mark.
	leafBase []uint32

	pass2     bool
	pass2View cullView

	// stream is the page pool, when the meshes stream (nil: the whole
	// meshes are in vbo/nbo/ebo).
	stream *naniteStreamer
	// attachedVAO is the VAO the instance attributes were last pointed in.
	attachedVAO uint32
}

// naniteMeshSlot is one mesh's place in the pool.
type naniteMeshSlot struct {
	baseVertex  int32
	firstIndex  uint32
	clusterBase uint32
	// nodeBase is its tree's root in the node list, leaves how many leaves
	// the tree has (0: no tree), and leafRanges their runs of clusters —
	// the range tiles.
	nodeBase   uint32
	leaves     uint32
	leafRanges [][2]int32
	// closed: the mesh bounds a solid (nanite.Mesh.Closed), so its
	// placements are flagged for the shadow passes' cone test.
	closed bool
	// leafPages is each leaf's page, when the pool streams: a range tile
	// takes leaves of one page only.
	leafPages []int32
}

var naniteScenery naniteSceneryRenderer

// pending reports a first pass waiting on the occlusion pass.
func (r *naniteSceneryRenderer) pending() bool { return r.pass2 }

// prepare brings the pool, groups, tiles and instances up to date with lvl.
// False when the level has nothing for this path to draw.
func (r *naniteSceneryRenderer) prepare(lvl *world.Level) bool {
	if lvl == nil || !naniteCull.ok {
		return false
	}
	// Once a frame inside DrawWorld: chicago's 1,624 placements re-signed and
	// their matrices rebuilt for every shadow rectangle were a fifth of the
	// frame's CPU. Outside it (a test, a preview) every call looks again.
	if naniteLODFrame.set && lvl == r.preparedLvl && r.preparedFrame == naniteStreamFrame {
		return r.prepared
	}
	r.preparedFrame = 0
	r.members = r.members[:0]
	r.memberImp = r.memberImp[:0]
	for i := range lvl.Scenery {
		// A placement whose chunk is off is not a member: the signature
		// below then rebuilds the tiles without it, and a mesh none of the
		// members uses leaves the pool.
		if s := &lvl.Scenery[i]; s.NaniteGPU && s.Nanite != nil && s.InWorld() {
			r.members = append(r.members, i)
			r.memberImp = append(r.memberImp, false)
			// And its impostor's cards, a member of their own.
			if DrawDistances && s.HasImpostor() {
				r.members = append(r.members, i)
				r.memberImp = append(r.memberImp, true)
			}
		}
	}
	if len(r.members) == 0 {
		r.markPrepared(lvl, false)
		return false
	}
	sig := r.signature(lvl)
	if lvl != r.lvl || sig != r.sig {
		sameLevel := lvl == r.lvl
		r.lvl, r.sig = lvl, sig
		r.rebuild(lvl, sameLevel)
	}
	r.updateInstances(lvl)
	if r.stream != nil {
		r.stream.update(r.clusters, r.nodes)
	}
	r.markPrepared(lvl, true)
	return true
}

// markPrepared records prepare's answer for the rest of a DrawWorld frame.
func (r *naniteSceneryRenderer) markPrepared(lvl *world.Level, ok bool) {
	if naniteLODFrame.set {
		r.preparedLvl, r.preparedFrame, r.prepared = lvl, naniteStreamFrame, ok
	}
}

// signature hashes what the pool, groups and tiles depend on: which
// placements, which mesh each uses, and each one's resolved slot textures and
// materials.
func (r *naniteSceneryRenderer) signature(lvl *world.Level) uint64 {
	// maphash, not fnv: this runs every frame over every member's every
	// slot name, and fnv's byte at a time (with a []byte copy of each name)
	// was 2.6 ms a frame over the biome showcase's 14,800 members
	// (docs/regions/PRESETS_5.md). The seed is the process's, and a
	// signature is only ever compared within one.
	var h maphash.Hash
	h.SetSeed(naniteSigSeed)
	var b [16]byte
	for k, i := range r.members {
		m := r.memberMesh(lvl, k)
		binary.LittleEndian.PutUint64(b[:8], uint64(i))
		binary.LittleEndian.PutUint64(b[8:], uint64(uintptr(unsafe.Pointer(m))))
		h.Write(b[:])
		for si := range m.Slots {
			h.WriteString(r.slotTexture(lvl, k, si))
			h.WriteByte(0)
			h.WriteString(r.slotMaterial(lvl, k, si))
			h.WriteByte(0)
		}
	}
	// A debug view tiles a cluster a command (appendLeafTiles).
	if naniteDebugActive() {
		h.WriteByte(1)
	}
	// The pool's kind.
	if naniteStreaming() {
		h.WriteByte(2)
	}
	return h.Sum64()
}

// NanitePoolWholeLevel builds the scenery page pool from every Nanite mesh
// the level places, not only those whose chunks are on (rebuild);
// FOUNDRY_NANITE_POOL_WHOLE_LEVEL=0 is the old pool of the members alone.
var NanitePoolWholeLevel = gfx.EnvFlag("FOUNDRY_NANITE_POOL_WHOLE_LEVEL", true)

// naniteSigSeed is signature's hash seed, fixed for the process.
var naniteSigSeed = maphash.MakeSeed()

// rebuild remakes the pool (if a mesh came that it lacks) and the groups and
// tiles.
//
// Within one level the pool only grows. A mesh no member uses any more —
// every placement of it in a chunk that streamed out (world.Scenery.Chunk) —
// keeps its place: rebuilding the pool releases every resident page, so the
// whole level would stream in again, and its shadows redraw, each time an
// area switched off. Nothing draws the idle mesh, so nothing requests its
// pages and the page pool evicts them as it would any unseen page. When the
// pool has to be rebuilt for a new mesh it keeps the idle ones too, so the
// area coming back on does not rebuild it again. A new level starts afresh.
func (r *naniteSceneryRenderer) rebuild(lvl *world.Level, sameLevel bool) {
	used := map[*nanite.Mesh]bool{}
	var list []*nanite.Mesh
	for k := range r.members {
		m := r.memberMesh(lvl, k)
		if !used[m] {
			used[m] = true
			list = append(list, m)
		}
	}
	samePool := (r.stream != nil) == naniteStreaming()
	for _, m := range list {
		if r.meshes[m] == nil {
			samePool = false
		}
	}
	// The pool holds every mesh the level places, its chunk on or off
	// (NanitePoolWholeLevel): built once, a script switching an area on in
	// play finds its meshes there and nothing is released and streamed in
	// again — on the adventure level the ruins coming on had rebuilt it,
	// a 2 s frame (docs/adventure/PHASE_F.md).
	if !samePool && NanitePoolWholeLevel {
		for i := range lvl.Scenery {
			s := &lvl.Scenery[i]
			if !s.NaniteGPU || s.Nanite == nil {
				continue
			}
			for _, m := range []*nanite.Mesh{s.Nanite, s.ImpostorMesh} {
				if m != nil && (m == s.Nanite || DrawDistances && s.HasImpostor()) && !used[m] {
					used[m] = true
					list = append(list, m)
				}
			}
		}
	}
	if !samePool && sameLevel {
		for _, m := range r.poolList {
			if !used[m] {
				used[m] = true
				list = append(list, m)
			}
		}
	}
	if !sameLevel && len(list) != len(r.poolList) {
		samePool = false // a new level's pool holds that level's meshes only
	}
	if !samePool {
		r.buildPool(list)
	}

	// Groups: one per resolved material, in order of first use — the slot's
	// texture, and the .fmat it wears (Scenery.SlotMaterial), which sets the
	// uniforms the group's one draw applies for all of its placements, so two
	// placements of a mesh in different materials draw apart.
	type key struct {
		tex    string
		mat    string
		smooth bool
		colors bool
	}
	groupOf := map[key]int{}
	r.groups = r.groups[:0]
	type run struct {
		group, inst  int
		first, count uint32
		hist         uint32
		mesh         *naniteMeshSlot
	}
	var runs []run
	hist := uint32(0)
	for k := range r.members {
		m := r.memberMesh(lvl, k)
		pm := r.meshes[m]
		for si, slot := range m.Slots {
			if slot.Count == 0 {
				continue
			}
			kk := key{r.slotTexture(lvl, k, si), r.slotMaterial(lvl, k, si), slot.Smooth, m.HasColors && r.cbo != 0}
			g, ok := groupOf[kk]
			if !ok {
				g = len(r.groups)
				groupOf[kk] = g
				r.groups = append(r.groups, gfx.Material{Texture: kk.tex, MaterialName: kk.mat, Alpha: 1, Normals: true,
					Smooth: kk.smooth, VertexColors: kk.colors})
			}
			runs = append(runs, run{group: g, inst: k, first: pm.clusterBase + uint32(slot.First), count: uint32(slot.Count),
				hist: hist + uint32(slot.First), mesh: pm})
		}
		hist += uint32(len(m.Clusters))
	}
	// Each placement's leaf marks.
	r.leafBase = r.leafBase[:0]
	leaves := uint32(0)
	for k := range r.members {
		r.leafBase = append(r.leafBase, leaves)
		leaves += r.meshes[r.memberMesh(lvl, k)].leaves
	}
	r.batch.leaves = int(leaves)
	r.batch.nodes = r.nodes
	sort.SliceStable(runs, func(a, b int) bool { return runs[a].group < runs[b].group })
	var tiles []gpuTile
	groups := make([]cullGroup, len(r.groups))
	slot := 0
	for gi := range r.groups {
		groups[gi].first = int32(slot)
		for _, rn := range runs {
			if rn.group != gi {
				continue
			}
			if len(rn.mesh.leafRanges) > 0 {
				// A tile a leaf of the DAG, drawn as one range.
				tiles = appendLeafTiles(tiles, rn.mesh.leafRanges, rn.mesh.leafPages, rn.mesh.clusterBase, rn.first, rn.count, uint32(rn.inst), rn.hist, &slot)
			} else {
				tiles = appendTiles(tiles, rn.first, rn.count, uint32(rn.inst), rn.hist, &slot)
			}
		}
		groups[gi].count = int32(slot) - groups[gi].first
	}
	r.batch.clusters = r.clusters
	r.batch.cpuClusters = r.cpuClusters
	r.batch.setTiles(tiles, groups, slot)
	// The groups the compute rasteriser may take: every group shares the
	// pool's buffers, so only the material decides (nanite_swraster.go).
	sw := make([]bool, len(r.groups))
	for gi := range r.groups {
		sw[gi] = swEligible(&r.groups[gi])
	}
	r.batch.setSWGroups(sw)
	r.batch.ensureHistory(int(hist))
	r.insts = r.insts[:0]
	r.placed, r.placedAt = nil, nil
}

// naniteStreaming is whether cooked meshes go through the page pool: the
// switch, and LOD selection on (the pool may hold less than the full
// detail, which only the LOD test knows to step round).
func naniteStreaming() bool { return NaniteStream && NaniteLOD }

// buildPool uploads list's meshes into one set of buffers: their pages into
// the page pool, or (FOUNDRY_NANITE_STREAM=0) the whole of each.
func (r *naniteSceneryRenderer) buildPool(list []*nanite.Mesh) {
	// Whatever pool there was goes; the VAO stays (its instance attributes
	// point at the batch's instances, whichever pool it reads).
	if r.stream != nil {
		r.stream.release()
		r.stream = nil
		r.batch.stream = nil
		r.vbo, r.nbo, r.ebo, r.cbo = 0, 0, 0, 0 // the page pool's own
	} else if r.vbo != 0 {
		gl.DeleteBuffers(1, &r.vbo)
		gl.DeleteBuffers(1, &r.nbo)
		gl.DeleteBuffers(1, &r.ebo)
		if r.cbo != 0 {
			gl.DeleteBuffers(1, &r.cbo)
		}
		r.vbo, r.nbo, r.ebo, r.cbo = 0, 0, 0, 0
	}
	r.meshes = map[*nanite.Mesh]*naniteMeshSlot{}
	r.poolList = list
	if naniteStreaming() && r.buildPagePool(list) {
		return
	}
	var verts, normals []float32
	var indices, colors []uint32
	var clusters []gpuCluster
	var nodes []gpuNode
	anyColors := false
	for _, m := range list {
		anyColors = anyColors || m.HasColors
	}
	for _, m := range list {
		if err := m.Flat(); err != nil {
			log.Printf("nanite: %s: %v", m.Source, err)
		}
		pm := &naniteMeshSlot{baseVertex: int32(len(verts) / 5), firstIndex: uint32(len(indices)), clusterBase: uint32(len(clusters)),
			nodeBase: uint32(len(nodes)), leaves: uint32(m.Leaves), leafRanges: cluster.LeafRanges(m.Nodes), closed: m.ClosedSolid}
		r.meshes[m] = pm
		if anyColors {
			if m.HasColors && len(m.Colors) == len(m.Vertices)/5 {
				colors = append(colors, m.Colors...)
			} else {
				// Never read (its groups do not set vertexColors); white
				// all the same.
				for range len(m.Vertices) / 5 {
					colors = append(colors, 0xffffffff)
				}
			}
		}
		verts = append(verts, m.Vertices...)
		normals = append(normals, m.Normals...)
		indices = append(indices, m.Indices...)
		for ci := range m.Clusters {
			clusters = append(clusters, toGPULODCluster(&m.Clusters[ci], m.ClusterLeaf[ci], pm.firstIndex, pm.baseVertex))
		}
		for ni := range m.Nodes {
			nodes = append(nodes, toGPUNode(&m.Nodes[ni], pm.nodeBase))
		}
	}
	if r.vao == 0 {
		gl.GenVertexArrays(1, &r.vao)
	}
	gl.GenBuffers(1, &r.vbo)
	gl.GenBuffers(1, &r.nbo)
	gl.GenBuffers(1, &r.ebo)
	gl.BindVertexArray(r.vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, r.vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(verts)*4, gl.Ptr(verts), gl.STATIC_DRAW)
	gfx.BindVertexLayout()
	gl.BindBuffer(gl.ARRAY_BUFFER, r.nbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(normals)*4, gl.Ptr(normals), gl.STATIC_DRAW)
	gl.EnableVertexAttribArray(gfx.AttribNormal)
	gl.VertexAttribPointerWithOffset(gfx.AttribNormal, 3, gl.FLOAT, false, 3*4, 0)
	gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, r.ebo)
	gl.BufferData(gl.ELEMENT_ARRAY_BUFFER, len(indices)*4, gl.Ptr(indices), gl.STATIC_DRAW)
	if len(colors) > 0 {
		gl.GenBuffers(1, &r.cbo)
		gl.BindBuffer(gl.ARRAY_BUFFER, r.cbo)
		gl.BufferData(gl.ARRAY_BUFFER, len(colors)*4, gl.Ptr(colors), gl.STATIC_DRAW)
		gfx.BindPackedColorLayout()
	} else {
		gl.DisableVertexAttribArray(gfx.AttribColor)
	}
	gl.BindVertexArray(0)
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)
	uploadClusters(&r.clusters, clusters)
	r.cpuClusters = clusters
	uploadNodes(&r.nodes, nodes)
}

// buildPagePool is buildPool's page pool (nanite_stream.go): false when the
// meshes cannot stream, for the flat pool to take over.
func (r *naniteSceneryRenderer) buildPagePool(list []*nanite.Mesh) bool {
	clusterBase := make([]uint32, len(list))
	nodeBase := make([]uint32, len(list))
	nc, nn := uint32(0), uint32(0)
	for i, m := range list {
		clusterBase[i], nodeBase[i] = nc, nn
		nc += uint32(len(m.Clusters))
		nn += uint32(len(m.Nodes))
	}
	s := newNaniteStreamer(list, clusterBase, nodeBase)
	if s == nil {
		return false
	}
	var clusters []gpuCluster
	// static: the same records with their cooked errors, for the draw
	// bound (staticClusterRecords) — never the residency's.
	var static []gpuCluster
	var nodes []gpuNode
	var cbuf []gpuCluster
	var nbuf []gpuNode
	for i, m := range list {
		pm := &naniteMeshSlot{clusterBase: clusterBase[i], nodeBase: nodeBase[i], leaves: uint32(m.Leaves),
			leafRanges: cluster.LeafRanges(m.Nodes), closed: m.ClosedSolid}
		for _, l := range pm.leafRanges {
			pm.leafPages = append(pm.leafPages, m.PageOf(l[0]))
		}
		r.meshes[m] = pm
		s.meshes[i].res.Update()
		cbuf = s.clusterRecords(i, cbuf)
		nbuf = s.nodeRecords(i, nbuf)
		clusters = append(clusters, cbuf...)
		static = staticClusterRecords(m, static)
		nodes = append(nodes, nbuf...)
		s.meshes[i].gpuClusters = append([]gpuCluster(nil), cbuf...)
		s.meshes[i].gpuNodes = append([]gpuNode(nil), nbuf...)
		s.meshes[i].dirty = false
	}
	r.stream = s
	r.batch.stream = s
	r.vbo, r.nbo, r.ebo, r.cbo = s.vbo, s.nbo, s.ebo, s.cbo
	if r.vao == 0 {
		gl.GenVertexArrays(1, &r.vao)
	}
	gl.BindVertexArray(r.vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, r.vbo)
	gfx.BindVertexLayout()
	gl.BindBuffer(gl.ARRAY_BUFFER, r.nbo)
	gl.EnableVertexAttribArray(gfx.AttribNormal)
	gl.VertexAttribPointerWithOffset(gfx.AttribNormal, 3, gl.FLOAT, false, 3*4, 0)
	if r.cbo != 0 {
		gl.BindBuffer(gl.ARRAY_BUFFER, r.cbo)
		gfx.BindPackedColorLayout()
	} else {
		gl.DisableVertexAttribArray(gfx.AttribColor)
	}
	gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, r.ebo)
	gl.BindVertexArray(0)
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)
	uploadClusters(&r.clusters, clusters)
	r.cpuClusters = static
	uploadNodes(&r.nodes, nodes)
	return true
}

// updateInstances re-sends the instance records if any placement moved.
func (r *naniteSceneryRenderer) updateInstances(lvl *world.Level) {
	changed := len(r.insts) != len(r.members)
	if changed {
		r.insts = make([]gpuInstance, len(r.members))
		r.spheres = make([]mgl32.Vec4, len(r.members))
		r.reach = make([]float32, len(r.members))
	}
	if len(r.camMay) != len(r.members) {
		r.camMay = make([]bool, len(r.members))
	}
	if len(r.placed) != len(r.members) {
		r.placed = make([]gpuInstance, len(r.members))
		r.placedAt = make([]mgl32.Mat4, len(r.members))
		for k := range r.placedAt {
			r.placedAt[k][15] = float32(math.NaN()) // never matches
		}
	}
	eye := gfx.Cam.Position
	for k, i := range r.members {
		s := &lvl.Scenery[i]
		imp := r.memberImp[k]
		mesh := r.memberMesh(lvl, k)
		model := sceneryModel(lvl, s)
		if r.placedAt[k] != model {
			r.placedAt[k], r.placed[k] = model, placedInstance(model, mesh.Center, mesh.Radius)
		}
		in := r.placed[k]
		// Its distance window, and whether it casts this frame
		// (nanite_draw_distance.go).
		var casts bool
		in.Draw, casts = placementDraw(s, imp, eye)
		if !casts {
			in.Flags[0] |= instShadowHidden
		}
		r.camMay[k] = placementMayDraw(s, imp, eye)
		if pm := r.meshes[mesh]; pm != nil {
			// An impostor's cards are cone-tested from the light too: only
			// the cards facing the sun cast (impostor/cones.go) — the top
			// card under a high sun, the side card turned to it under a low
			// one. All nine cast a crown far denser than the tree's, and
			// shadowed the cards themselves into dark cones.
			if pm.closed || imp {
				in.Flags[0] |= instShadowCone
			}
			if pm.leaves > 0 {
				in.Flags[0] |= instLOD
				in.Flags[1], in.Flags[2] = pm.nodeBase, r.leafBase[k]
			}
		}
		// An impostor stands still: its sway is lost in the distance, and
		// its cards' cones stay true.
		if w, ok := sceneryWindRecord(s); ok && !imp {
			in.Wind = w
			// A bent cluster's triangles no longer face where its cone
			// says.
			in.Flags[0] |= instNoCone
			in.Flags[0] &^= instShadowCone
		}
		if in != r.insts[k] {
			r.insts[k] = in
			c, rad := naniteMeshBounds(s, mesh)
			r.spheres[k] = c.Vec4(rad)
			r.reach[k] = 0
			if !imp {
				r.reach[k] = sceneryWindReach(s, 1)
			}
			changed = true
		}
	}
	if !changed {
		return
	}
	hadBuf := r.batch.instBuf
	r.batch.setInstances(r.insts)
	r.batch.upload()
	if hadBuf != r.batch.instBuf || hadBuf == 0 || r.attachedVAO != r.vao {
		r.attachInstances()
	}
}

// attachInstances points the pool VAO's instance attributes at the batch's
// instance buffer: the matrix (the first 64 bytes of each record), the
// sphere after it and the wind at its end (vertex.glsl's instanceSphere and
// instanceWind).
func (r *naniteSceneryRenderer) attachInstances() {
	r.attachedVAO = r.vao
	gl.BindVertexArray(r.vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, r.batch.instBuf)
	for i := uint32(0); i < 4; i++ {
		loc := gfx.AttribInstanceModel + i
		gl.EnableVertexAttribArray(loc)
		gl.VertexAttribPointerWithOffset(loc, 4, gl.FLOAT, false, gpuInstanceStride, uintptr(i*16))
		gl.VertexAttribDivisor(loc, 1)
	}
	for _, a := range []struct {
		loc uint32
		off uintptr
	}{{gfx.AttribInstanceSphere, unsafe.Offsetof(gpuInstance{}.Sphere)}, {gfx.AttribInstanceWind, unsafe.Offsetof(gpuInstance{}.Wind)}} {
		gl.EnableVertexAttribArray(a.loc)
		gl.VertexAttribPointerWithOffset(a.loc, 4, gl.FLOAT, false, gpuInstanceStride, a.off)
		gl.VertexAttribDivisor(a.loc, 1)
	}
	gl.BindVertexArray(0)
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)
}

// SyncNaniteScenery builds the level's pool of cooked meshes now — when the
// level is installed, behind its loading screen — rather than in its first
// frame, and waits for the GPU to have taken it: the page pool's preload is
// hundreds of megabytes on a big level, and left to the first frames the
// driver's upload of it surfaced as a 30-100 ms wait in each of the first
// several (docs/nanite/PHASE_3.md).
func SyncNaniteScenery(lvl *world.Level) {
	if lvl == nil || !naniteCull.ready() {
		return
	}
	if naniteScenery.prepare(lvl) {
		gl.Finish()
	}
}

// RenderNaniteScenery draws the level's cooked placements for the camera:
// the first pass, when the frame runs two (naniteTwoPass), else the only one.
func RenderNaniteScenery(lvl *world.Level, frustum Frustum, projView mgl32.Mat4) {
	r := &naniteScenery
	r.pass2 = false
	DebugStats.NaniteInstances = 0
	if !r.prepare(lvl) {
		return
	}
	DebugStats.NaniteInstances = len(r.members)
	DebugStats.NaniteClusters += r.batch.slots
	in := r.inView(&frustum, true)
	if in == 0 {
		// Every placement behind the camera: nothing to cull, and nothing
		// for the occlusion pass to finish. Their history goes stale, which
		// costs one frame of drawing them in the second pass when they come
		// back — never a frame of them missing.
		return
	}
	eye := gfx.Cam.Position
	mode := int32(cullPlain)
	// The camera's passes are packed (cameraCullView's default), however
	// many placements are in view. Phase 2 packed them only with most
	// placements out of view, measuring the packing at a millisecond on the
	// rock field; that was its prefix sum, one workgroup walking the whole
	// group's tiles, and since Phase 5's scan (nanite_compact_comp.glsl) it
	// costs a tenth of a millisecond and saves the draw walking tens of
	// thousands of empty commands: the rock field behind its wall drew a
	// millisecond faster (docs/nanite/PHASE_5.md). It matters the more since
	// the compute rasteriser empties the commands of every small cluster.
	if naniteTwoPass && r.batch.slots > 0 {
		mode = cullPass1
		r.pass2 = true
		r.pass2View = cameraCullView(frustum, projView, eye, cullPass2)
	}
	v := cameraCullView(frustum, projView, eye, mode)
	// Bounded before admitSW: the records the visibility buffer keeps for
	// the pass are the bound's (visSpan), not the batch's every slot. By the
	// pass's LOD too (nanite_draw_lod_bound.go).
	r.batch.setBoundLOD(v.lod, true)
	r.batch.boundDraws(r.inMask)
	r.batch.admitSW(r.visGroups())
	if r.batch.cull(v) {
		r.drawColour()
	}
	gl.UseProgram(gfx.Program)
}

// drawOcclusion is the second pass, after naniteOcclusionPass built the
// pyramid.
func (r *naniteSceneryRenderer) drawOcclusion() {
	if !r.pass2 {
		return
	}
	r.pass2 = false
	// The first pass's frustum, asked again: a shadow pass between the two
	// would have left its own marks.
	r.inView(&Frustum{Planes: r.pass2View.planes}, true)
	r.batch.setBoundLOD(r.pass2View.lod, true)
	r.batch.boundDraws(r.inMask)
	r.batch.admitSW(r.visGroups())
	if r.batch.cull(r.pass2View) {
		r.drawColour()
	}
}

// visGroups is the batch's groups as the visibility buffer draws them.
func (r *naniteSceneryRenderer) visGroups() []visGroup {
	identity := mgl32.Ident4()
	groups := make([]visGroup, len(r.groups))
	for gi := range r.groups {
		gi := gi
		groups[gi] = visGroup{mat: &r.groups[gi], vao: r.vao, vbo: r.vbo, nbo: r.nbo, ebo: r.ebo, instanced: true,
			forward: func() {
				r.groups[gi].BindWorld(identity, 0, true)
				gl.BindVertexArray(r.vao)
				r.batch.drawGroup(gi)
				gl.Uniform1i(gfx.InstancedUniform, 0)
				gl.Uniform1i(gfx.VertexColorsUniform, 0)
			}}
		if r.groups[gi].VertexColors {
			groups[gi].cbo = r.cbo
		}
	}
	return groups
}

func (r *naniteSceneryRenderer) drawColour() {
	if naniteDebugActive() {
		r.batch.drawDebug(len(r.groups), func(int) uint32 { return r.vao }, true)
		return
	}
	identity := mgl32.Ident4()
	// A visibility-buffer frame draws the placements' IDs instead, shaded
	// later in one pass (nanite_vis.go).
	if naniteVisActive() {
		if naniteVis.drawBatch(&r.batch, r.visGroups()) {
			return
		}
	}
	prepassBatch(&r.batch, len(r.groups),
		func(i int) *gfx.Material { return &r.groups[i] },
		func(int) mgl32.Mat4 { return identity },
		func(int) uint32 { return r.vao }, true)
	gl.BindVertexArray(r.vao)
	for gi := range r.groups {
		r.groups[gi].BindWorld(identity, 0, true)
		gl.BindVertexArray(r.vao)
		r.batch.drawGroup(gi)
	}
	gl.Uniform1i(gfx.InstancedUniform, 0)
	// A coloured group's flag is not left for a draw that does not set it.
	gl.Uniform1i(gfx.VertexColorsUniform, 0)
	gl.BindVertexArray(0)
}

// renderNaniteSceneryShadow casts the cooked placements into the bound shadow
// map, culled cluster by cluster against cull, under prog.
func renderNaniteSceneryShadow(lvl *world.Level, prog uint32, cull *Frustum) {
	r := &naniteScenery
	if !r.prepare(lvl) {
		return
	}
	in := r.inView(cull, false)
	if in == 0 {
		// Nothing this light can see: no cull at all. A point light's six
		// faces mostly look at none of a level's dense props.
		return
	}
	v := shadowCullView(cull)
	// Packed, as the camera's passes are, and drawn no further than what is
	// in view can have written (cullBatch.drawMax). Phase 2 packed a shadow
	// pass only with most placements out of its frustum, measuring the
	// packing at 2 ms over three cascades on the rock field; that was the
	// one-workgroup prefix sum Phase 5's scan replaced. Unpacked, a pass
	// walks every slot of the batch — chicago's 856,184, ~4.8 ms a shadow
	// view however little it drew.
	v.compact = true
	r.batch.setBoundLOD(v.lod, true)
	r.batch.boundDraws(r.inMask)
	ok := r.batch.cull(v)
	gl.UseProgram(prog)
	if !ok {
		return
	}
	loc := gfx.UniformLoc(prog, "instanced\x00")
	gl.Uniform1i(loc, 1)
	gl.BindVertexArray(r.vao)
	for gi := range r.groups {
		r.batch.drawGroup(gi)
	}
	gl.BindVertexArray(0)
	gl.Uniform1i(loc, 0)
}

// inView counts the placements whose sphere touches cull (all of them for
// none), marking them in r.inMask (nil for none) for the pass's draw bound
// (cullBatch.boundDraws). The sphere is grown a little past the GPU's: a
// placement its instance test passes must pass here too, or what it packs
// past the bound is never drawn. A camera pass keeps only members whose draw
// window can hold the camera (camMay), a shadow pass only those casting this
// frame (instShadowHidden), as the instance stage does
// (nanite_draw_distance.go).
func (r *naniteSceneryRenderer) inView(cull *Frustum, camera bool) int {
	if cull == nil {
		r.inMask = nil
		return len(r.spheres)
	}
	if len(r.inMask) != len(r.spheres) {
		r.inMask = make([]bool, len(r.spheres))
	}
	n := 0
	k := gfx.Wind.Strength()
	drawOn := DrawDistances && !naniteDebugActive()
	for i, sp := range r.spheres {
		if drawOn && i < len(r.insts) && i < len(r.camMay) {
			if camera && !r.camMay[i] || !camera && r.insts[i].Flags[0]&instShadowHidden != 0 {
				r.inMask[i] = false
				continue
			}
		}
		rad := sp[3] + r.reach[i]*k
		in := sphereInFrustum(*cull, sp.Vec3(), rad*1.01+0.05)
		r.inMask[i] = in
		if in {
			n++
		}
	}
	return n
}

// sceneryWindRecord is s's instance wind (wind_common.glsl's windOffset):
// bend, flutter, its mesh's base height and one over its height. False for
// a placement without wind, or a mesh with no height to bend over.
func sceneryWindRecord(s *world.Scenery) ([4]float32, bool) {
	if !s.Wind.Active() {
		return [4]float32{}, false
	}
	lo, hi := s.LocalMin.Y(), s.LocalMax.Y()
	if s.Nanite != nil && hi <= lo {
		lo, hi = s.Nanite.Center.Y()-s.Nanite.Radius, s.Nanite.Center.Y()+s.Nanite.Radius
	}
	if hi-lo < 1e-4 {
		return [4]float32{}, false
	}
	return [4]float32{s.Wind.Bend, s.Wind.Flutter, lo, 1 / (hi - lo)}, true
}

// sceneryWindMoving reports whether s is drawn swaying this frame: a cooked
// placement the GPU cull draws, with wind, in moving air. What the shadow
// caches redraw every frame.
func sceneryWindMoving(s *world.Scenery) bool {
	return s.NaniteGPU && s.Nanite != nil && s.Wind.Active() && gfx.Wind.Strength() > 0
}

// sceneryWindReach is how far s's wind can carry any vertex, in air of
// strength k: wind_common.glsl's windReach, for the CPU's tests.
func sceneryWindReach(s *world.Scenery, k float32) float32 {
	w, ok := sceneryWindRecord(s)
	if !ok || k <= 0 {
		return 0
	}
	m := s.ModelMatrix()
	height := m.Col(1).Vec3().Len() / w[3]
	bend := k * w[0] * height * 1.15
	return bend + bend*bend*0.5/max(height, 0.05) + 2.5*k*w[1]
}

// naniteSceneryBounds is a placement's world sphere, for tests and the
// point-shadow cache.
func naniteSceneryBounds(s *world.Scenery) (mgl32.Vec3, float32) {
	return naniteMeshBounds(s, s.Nanite)
}

// naniteMeshBounds is the world sphere of mesh (the placement's own, or its
// impostor's) under s's placement.
func naniteMeshBounds(s *world.Scenery, mesh *nanite.Mesh) (mgl32.Vec3, float32) {
	m := s.ModelMatrix()
	c := m.Mul4x1(mesh.Center.Vec4(1))
	sc := float32(math.Sqrt(float64(max(m.Col(0).Vec3().LenSqr(), m.Col(1).Vec3().LenSqr(), m.Col(2).Vec3().LenSqr()))))
	return c.Vec3(), mesh.Radius * sc
}

// memberMesh is member k's mesh: its placement's, or its impostor's cards.
func (r *naniteSceneryRenderer) memberMesh(lvl *world.Level, k int) *nanite.Mesh {
	s := &lvl.Scenery[r.members[k]]
	if k < len(r.memberImp) && r.memberImp[k] {
		return s.ImpostorMesh
	}
	return s.Nanite
}

// slotTexture and slotMaterial are what member k's slot si draws with: the
// placement's (its overrides and its .fmat) for its mesh; an impostor's own
// baked texture, and no material, for its cards.
func (r *naniteSceneryRenderer) slotTexture(lvl *world.Level, k, si int) string {
	if r.memberImp[k] {
		return lvl.Scenery[r.members[k]].ImpostorMesh.Slots[si].Texture
	}
	return lvl.Scenery[r.members[k]].SlotTexture(si)
}

func (r *naniteSceneryRenderer) slotMaterial(lvl *world.Level, k, si int) string {
	if r.memberImp[k] {
		return ""
	}
	return lvl.Scenery[r.members[k]].SlotMaterial(si)
}
