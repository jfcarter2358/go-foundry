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
// What is not here yet: translucent slots (a cooked mesh draws in the opaque
// pass, alpha-to-coverage cutouts and all — a texture with partial alpha is
// not sorted as a classic model's is), and skinning (a cooked mesh is static).

import (
	"encoding/binary"
	"hash/fnv"
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
	clusters uint32
	nodes    uint32

	// members are the level's placements drawn here, by index; sig what the
	// groups and tiles were built from.
	lvl     *world.Level
	members []int
	sig     uint64

	batch  cullBatch
	groups []gfx.Material
	insts  []gpuInstance
	// spheres are the placements' world spheres, for the CPU's look ahead of
	// a pass (inView).
	spheres []mgl32.Vec4
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
	r.members = r.members[:0]
	for i := range lvl.Scenery {
		if s := &lvl.Scenery[i]; s.NaniteGPU && s.Nanite != nil {
			r.members = append(r.members, i)
		}
	}
	if len(r.members) == 0 {
		return false
	}
	sig := r.signature(lvl)
	if lvl != r.lvl || sig != r.sig {
		r.lvl, r.sig = lvl, sig
		r.rebuild(lvl)
	}
	r.updateInstances(lvl)
	if r.stream != nil {
		r.stream.update(r.clusters, r.nodes)
	}
	return true
}

// signature hashes what the pool, groups and tiles depend on: which
// placements, which mesh each uses, and each one's resolved slot textures.
func (r *naniteSceneryRenderer) signature(lvl *world.Level) uint64 {
	h := fnv.New64a()
	var b [8]byte
	for _, i := range r.members {
		s := &lvl.Scenery[i]
		binary.LittleEndian.PutUint64(b[:], uint64(i))
		h.Write(b[:])
		binary.LittleEndian.PutUint64(b[:], uint64(uintptr(unsafe.Pointer(s.Nanite))))
		h.Write(b[:])
		for si := range s.Nanite.Slots {
			h.Write([]byte(s.SlotTexture(si)))
			h.Write([]byte{0})
		}
	}
	// A debug view tiles a cluster a command (appendLeafTiles).
	if naniteDebugActive() {
		h.Write([]byte{1})
	}
	// The pool's kind.
	if naniteStreaming() {
		h.Write([]byte{2})
	}
	return h.Sum64()
}

// rebuild remakes the pool (if a mesh came or went) and the groups and tiles.
func (r *naniteSceneryRenderer) rebuild(lvl *world.Level) {
	used := map[*nanite.Mesh]bool{}
	var list []*nanite.Mesh
	for _, i := range r.members {
		m := lvl.Scenery[i].Nanite
		if !used[m] {
			used[m] = true
			list = append(list, m)
		}
	}
	samePool := len(list) == len(r.poolList) && (r.stream != nil) == naniteStreaming()
	for _, m := range list {
		if r.meshes[m] == nil {
			samePool = false
		}
	}
	if !samePool {
		r.buildPool(list)
	}

	// Groups: one per resolved material, in order of first use.
	type key struct {
		tex    string
		smooth bool
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
	for k, i := range r.members {
		s := &lvl.Scenery[i]
		m := s.Nanite
		pm := r.meshes[m]
		for si, slot := range m.Slots {
			if slot.Count == 0 {
				continue
			}
			kk := key{s.SlotTexture(si), slot.Smooth}
			g, ok := groupOf[kk]
			if !ok {
				g = len(r.groups)
				groupOf[kk] = g
				r.groups = append(r.groups, gfx.Material{Texture: kk.tex, Alpha: 1, Normals: true, Smooth: kk.smooth})
			}
			runs = append(runs, run{group: g, inst: k, first: pm.clusterBase + uint32(slot.First), count: uint32(slot.Count),
				hist: hist + uint32(slot.First), mesh: pm})
		}
		hist += uint32(len(m.Clusters))
	}
	// Each placement's leaf marks.
	r.leafBase = r.leafBase[:0]
	leaves := uint32(0)
	for _, i := range r.members {
		r.leafBase = append(r.leafBase, leaves)
		leaves += r.meshes[lvl.Scenery[i].Nanite].leaves
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
	r.batch.setTiles(tiles, groups, slot)
	r.batch.ensureHistory(int(hist))
	r.insts = r.insts[:0]
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
		r.vbo, r.nbo, r.ebo = 0, 0, 0 // the page pool's own
	} else if r.vbo != 0 {
		gl.DeleteBuffers(1, &r.vbo)
		gl.DeleteBuffers(1, &r.nbo)
		gl.DeleteBuffers(1, &r.ebo)
		r.vbo, r.nbo, r.ebo = 0, 0, 0
	}
	r.meshes = map[*nanite.Mesh]*naniteMeshSlot{}
	r.poolList = list
	if naniteStreaming() && r.buildPagePool(list) {
		return
	}
	var verts, normals []float32
	var indices []uint32
	var clusters []gpuCluster
	var nodes []gpuNode
	for _, m := range list {
		if err := m.Flat(); err != nil {
			log.Printf("nanite: %s: %v", m.Source, err)
		}
		pm := &naniteMeshSlot{baseVertex: int32(len(verts) / 5), firstIndex: uint32(len(indices)), clusterBase: uint32(len(clusters)),
			nodeBase: uint32(len(nodes)), leaves: uint32(m.Leaves), leafRanges: cluster.LeafRanges(m.Nodes), closed: m.ClosedSolid}
		r.meshes[m] = pm
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
	gl.BindVertexArray(0)
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)
	uploadClusters(&r.clusters, clusters)
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
		nodes = append(nodes, nbuf...)
		s.meshes[i].gpuClusters = append([]gpuCluster(nil), cbuf...)
		s.meshes[i].gpuNodes = append([]gpuNode(nil), nbuf...)
		s.meshes[i].dirty = false
	}
	r.stream = s
	r.batch.stream = s
	r.vbo, r.nbo, r.ebo = s.vbo, s.nbo, s.ebo
	if r.vao == 0 {
		gl.GenVertexArrays(1, &r.vao)
	}
	gl.BindVertexArray(r.vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, r.vbo)
	gfx.BindVertexLayout()
	gl.BindBuffer(gl.ARRAY_BUFFER, r.nbo)
	gl.EnableVertexAttribArray(gfx.AttribNormal)
	gl.VertexAttribPointerWithOffset(gfx.AttribNormal, 3, gl.FLOAT, false, 3*4, 0)
	gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, r.ebo)
	gl.BindVertexArray(0)
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)
	uploadClusters(&r.clusters, clusters)
	uploadNodes(&r.nodes, nodes)
	return true
}

// updateInstances re-sends the instance records if any placement moved.
func (r *naniteSceneryRenderer) updateInstances(lvl *world.Level) {
	changed := len(r.insts) != len(r.members)
	if changed {
		r.insts = make([]gpuInstance, len(r.members))
		r.spheres = make([]mgl32.Vec4, len(r.members))
	}
	for k, i := range r.members {
		s := &lvl.Scenery[i]
		in := placedInstance(s.ModelMatrix(), s.Nanite.Center, s.Nanite.Radius)
		if pm := r.meshes[s.Nanite]; pm != nil {
			if pm.closed {
				in.Flags[0] |= instShadowCone
			}
			if pm.leaves > 0 {
				in.Flags[0] |= instLOD
				in.Flags[1], in.Flags[2] = pm.nodeBase, r.leafBase[k]
			}
		}
		if in != r.insts[k] {
			r.insts[k] = in
			c, rad := naniteSceneryBounds(s)
			r.spheres[k] = c.Vec4(rad)
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

// attachInstances points the pool VAO's instance-matrix attributes at the
// batch's instance buffer: the first 64 bytes of each 96-byte record.
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
	in := r.inView(&frustum)
	if in == 0 {
		// Every placement behind the camera: nothing to cull, and nothing
		// for the occlusion pass to finish. Their history goes stale, which
		// costs one frame of drawing them in the second pass when they come
		// back — never a frame of them missing.
		return
	}
	eye := gfx.Cam.Position
	mode := int32(cullPlain)
	// Packed by the shadows' rule (renderNaniteSceneryShadow): only with most
	// placements out of view. The cooked meshes' DAGs are range tiles, whose
	// list is short already, and on the rock field the packing cost the
	// camera's two passes a millisecond (docs/nanite/PHASE_2.md).
	compact := in*2 < len(r.spheres)
	if naniteTwoPass && r.batch.slots > 0 {
		mode = cullPass1
		r.pass2 = true
		r.pass2View = cameraCullView(frustum, projView, eye, cullPass2)
		r.pass2View.compact = compact
	}
	v := cameraCullView(frustum, projView, eye, mode)
	v.compact = compact
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
	if r.batch.cull(r.pass2View) {
		r.drawColour()
	}
}

func (r *naniteSceneryRenderer) drawColour() {
	if naniteDebugActive() {
		r.batch.drawDebug(len(r.groups), func(int) uint32 { return r.vao }, true)
		return
	}
	identity := mgl32.Ident4()
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
	gl.BindVertexArray(0)
}

// renderNaniteSceneryShadow casts the cooked placements into the bound shadow
// map, culled cluster by cluster against cull, under prog.
func renderNaniteSceneryShadow(lvl *world.Level, prog uint32, cull *Frustum) {
	r := &naniteScenery
	if !r.prepare(lvl) {
		return
	}
	in := r.inView(cull)
	if in == 0 {
		// Nothing this light can see: no cull at all. A point light's six
		// faces mostly look at none of a level's dense props.
		return
	}
	v := shadowCullView(cull)
	// Packed when most placements are out of the frustum, where the fixed
	// list would be mostly empty commands; drawn as it stands when most are
	// in, where the packing is all cost. That holds with LOD selection too:
	// a DAG's leaves are range tiles of rangeSlots commands each, so the
	// list is no longer than the full detail's cluster list was, and on the
	// rock field packing it cost 2 ms a frame more than it saved
	// (docs/nanite/PHASE_2.md).
	v.compact = in*2 < len(r.spheres)
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
// none).
func (r *naniteSceneryRenderer) inView(cull *Frustum) int {
	if cull == nil {
		return len(r.spheres)
	}
	n := 0
	for _, sp := range r.spheres {
		if sphereInFrustum(*cull, sp.Vec3(), sp[3]) {
			n++
		}
	}
	return n
}

// naniteSceneryBounds is a placement's world sphere, for tests and the
// point-shadow cache.
func naniteSceneryBounds(s *world.Scenery) (mgl32.Vec3, float32) {
	m := s.ModelMatrix()
	c := m.Mul4x1(s.Nanite.Center.Vec4(1))
	sc := float32(math.Sqrt(float64(max(m.Col(0).Vec3().LenSqr(), m.Col(1).Vec3().LenSqr(), m.Col(2).Vec3().LenSqr()))))
	return c.Vec3(), s.Nanite.Radius * sc
}
