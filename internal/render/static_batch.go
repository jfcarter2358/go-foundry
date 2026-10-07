package render

// static_batch.go — a compiled level's static surfaces, one vertex buffer per
// source chunk and material.
//
// The release-build counterpart of chunk_batch.go. A compiled level's surfaces
// were one gfx.Geometry each, and a streamed level compiles thousands of them,
// so it had the draw-call problem batching solved for authored chunks all over
// again. Here each surface keeps its CPU mesh (raycasts read it) and its GPU
// half goes into a gfx.Batch with its baked light: one per source chunk,
// material and sidedness. The chunk is part of the key so that switching a
// streamed chunk off skips its batches whole.
//
// Compiled surfaces never change after load, so a level's batches are built
// once for the StaticGeometry they came from and rebuilt only when the level
// is handed a different one — a level load, or the editor's compiled preview
// going up or down.
//
// The groups' vertices are not in a buffer each but in POOLS: every group's
// corners, normals and baked light back to back in one set of buffers (or a
// few, where one would pass the driver's largest storage block), under one
// vertex array, each group's ranges addressing its own stretch. A group is
// still its own draw state and its own ranges; what it no longer has is a
// vertex array of its own, so the GPU-driven passes below draw every group of
// a pool without binding another, and a material's groups in every chunk are
// one draw (static_nanite.go).
//
// Since Phase 1 of the Nanite plan each group's buffer also carries an index
// list in CLUSTER order (world/static_clusters.go — the compiler's cut), and
// the opaque groups are culled and drawn by the GPU-driven cull
// (nanite_cull.go, static_nanite.go): the PVS still picks the candidate
// clusters on the CPU, and a compute pass does the frustum, the normal cone
// and the two-pass Hi-Z occlusion. The CPU path below stays, for transparent
// groups (which must be sorted) and as the fallback when the cull is switched
// off (FOUNDRY_NANITE_CLUSTERS=0) or cannot run.

import (
	stdbits "math/bits"

	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// staticKey is world.StaticGroupKey under the names this file has always
// used; the grouping itself is written in world/static_clusters.go, so the
// compiler that cuts a group into clusters and this file that draws it agree.
type staticKey struct {
	chunk       string
	cover       string
	mat         gfx.Material
	doubleSided bool
}

func staticKeyOf(k world.StaticGroupKey) staticKey {
	return staticKey{chunk: k.Chunk, cover: k.Cover, mat: k.Mat, doubleSided: k.DoubleSided}
}

type staticGroup struct {
	key       staticKey
	coveredBy []string // the surfaces' CoveredBy, which key.cover joins
	// batch is the group's draw state, its buffers its pool's.
	batch gfx.Batch
	// pool is the pool the group's vertices are in, from vertBase on.
	pool     int
	vertBase int32
	// surfaces lists the group's surface indexes in buffer order; firsts and
	// counts are their ranges in the pool's buffers, parallel to it. The order is spatial (see
	// world.SortSurfacesSpatially), which is what lets clusters below be both
	// compact boxes and contiguous runs of the buffer.
	surfaces       []int32
	firsts, counts []int32
	// clusters partition surfaces into runs of up to staticClusterSize
	// neighbours, each with the box around them; lo/hi bound the group.
	clusters []staticCluster
	lo, hi   mgl32.Vec3
	// visFirst/visCount are pass scratch, as in chunk_batch.go.
	visFirst, visCount []int32
	// visBits is the camera pass's scratch: one bit per surface, in buffer
	// order, for the ones found visible — read back in order to make ranges
	// without walking every surface of the level.
	visBits []uint64
	// clusterFirst/clusterCount are the group's clusters in the level-wide
	// GPU cluster list (static_nanite.go); zero count without one.
	clusterFirst, clusterCount int32
}

// staticCluster is a run of a group's surfaces, [start, end) in the group's
// surfaces list, and the box around all of them.
type staticCluster struct {
	start, end int32
	lo, hi     mgl32.Vec3
}

// staticBox is a surface's bounds.
type staticBox struct{ lo, hi mgl32.Vec3 }

// staticClusterSize is how many surfaces a culling cluster holds. A shadow
// pass tests every cluster box and then, for a cluster straddling a plane,
// every surface in it: small enough that a straddling cluster wastes little,
// large enough that a level of seventy thousand surfaces is a couple of
// thousand boxes. Thirty-two splits the difference and was not sensitive.
const staticClusterSize = 32

type staticBatches struct {
	// static is what these were built from. Holding the pointer is also what
	// stops a freed StaticGeometry's address being reused by the next one and
	// passing for it.
	static  *world.StaticGeometry
	count   int
	groups  []*staticGroup
	groupOf []int32 // per surface: its group, or -1 for one with nothing to draw
	// Per surface: its position in its group's surfaces (and so its bit in
	// visBits) and the cluster it is in.
	posInGroup []int32
	clusterOf  []int32
	// boxes is each surface's bounds, packed: the walks below read a box
	// per visible surface, and a StaticSurface (its CPU mesh and all) is
	// several cache lines wide.
	boxes []staticBox
	// Per cluster, level-wide: the camera pass's verdict on it, stamped with
	// the pass it was made in so nothing is cleared between frames. clusters
	// indexes the groups' clusters by that level-wide number.
	clusters   []*staticCluster
	clusterAt  []uint64
	clusterIs  []FrustumClass
	passNumber uint64
	// smoothLighting is gfx.SmoothLighting as it was at the build. Each
	// surface's baked light was picked by it (Geometry.BakedLightData), so a
	// change of setting means a rebuild.
	smoothLighting bool
	// transparent is each group's IsTransparent, as noteTransparency last
	// found it; transparentSig hashes it, so the GPU path can tell when a
	// texture edit moved a group between the passes.
	transparent    []bool
	transparentSig uint64
	anyTransparent bool
	// transVisible is the visible surfaces in transparent groups, out of the
	// PVS answer it was filtered from (transSerial, and the list's own
	// identity) under the transparency transSig hashes: a level of tens of
	// thousands of visible surfaces and a few dozen panes of glass walks the
	// dozens, and the thousands only when the answer changes.
	transVisible []int
	transSerial  uint64
	transSig     uint64
	transFrom    *int
	transLen     int
	transValid   bool
	// gpu is the GPU-driven cull's half (static_nanite.go), nil while it is
	// off or cannot run.
	gpu *staticGPU
	// pools are the buffers the groups' vertices share.
	pools []*staticPool
}

// staticPool is one set of buffers several groups' vertices share: positions
// and texture coordinates, normals and baked light where any group of the pool
// has them (a group without is padded with what its own vertex array would
// have read: nothing), and — once the GPU path has made them — the groups'
// cluster-order index lists, back to back.
type staticPool struct {
	batch gfx.Batch
	verts int32
	// idx is buildStaticGPU's index list while it is being made.
	idx []uint32
}

// staticPoolBytes caps a pool's largest buffer: the driver's largest storage
// block (the visibility buffer's material pass reads every buffer as one), and
// no more than this whatever the driver allows, so a level's buffers are never
// one allocation of half a gigabyte.
const staticPoolBytes = 256 << 20

// StaticPools switches the pooling: off (FOUNDRY_STATIC_POOLS=0), every group
// is a pool of its own, as every group had buffers of its own before.
var StaticPools = gfx.EnvFlag("FOUNDRY_STATIC_POOLS", true)

// staticLightPad is a baked-light record for a corner with none: what a
// vertex array with the attribute switched off reads.
var staticLightPad = [gfx.LightStride]float32{0, 0, 0, 1, 0, 0, 0}

var staticCache = map[*world.Level]*staticBatches{}

// staticBatchesFor returns the level's static batches, building them for the
// level's current StaticGeometry if they were built for another, and releasing
// them when it has none.
func staticBatchesFor(lvl *world.Level) *staticBatches {
	sb := staticCache[lvl]
	if lvl == nil || lvl.Static == nil {
		if sb != nil {
			sb.release()
			delete(staticCache, lvl)
		}
		return nil
	}
	if sb != nil && sb.static == lvl.Static && sb.count == len(lvl.Static.Surfaces) &&
		sb.smoothLighting == gfx.SmoothLighting {
		return sb
	}
	if sb != nil {
		sb.release()
	}
	sb = buildStaticBatches(lvl.Static)
	staticCache[lvl] = sb
	return sb
}

func buildStaticBatches(s *world.StaticGeometry) *staticBatches {
	sb := &staticBatches{static: s, count: len(s.Surfaces), smoothLighting: gfx.SmoothLighting}
	planned, groupOf := world.StaticGroups(s)
	sb.groupOf = groupOf
	for gi := range planned {
		key := staticKeyOf(planned[gi].Key)
		grp := &staticGroup{key: key, surfaces: planned[gi].Surfaces,
			coveredBy: s.Surfaces[planned[gi].Surfaces[0]].CoveredBy}
		grp.batch.Material = key.mat
		sb.groups = append(sb.groups, grp)
	}
	sb.posInGroup = make([]int32, len(s.Surfaces))
	sb.clusterOf = make([]int32, len(s.Surfaces))
	sb.boxes = make([]staticBox, len(s.Surfaces))
	for i := range s.Surfaces {
		sb.boxes[i] = staticBox{s.Surfaces[i].Lo, s.Surfaces[i].Hi}
	}
	for _, grp := range sb.groups {
		grp.buildClusters(s)
		grp.visBits = make([]uint64, (len(grp.surfaces)+63)/64)
		for ci := range grp.clusters {
			c := &grp.clusters[ci]
			for k := c.start; k < c.end; k++ {
				si := grp.surfaces[k]
				sb.posInGroup[si] = k
				sb.clusterOf[si] = int32(len(sb.clusters))
			}
			sb.clusters = append(sb.clusters, c)
		}
	}
	sb.buildPools(s)
	if NaniteClusters && gfx.GeometryUploadsEnabled {
		sb.gpu = buildStaticGPU(s, sb, planned)
	}
	return sb
}

// buildPools lays the groups' vertices out in pools, in group order, a new
// pool started wherever the next group would take the current one past
// staticPoolBytes or the driver's largest storage block, and uploads them.
func (sb *staticBatches) buildPools(s *world.StaticGeometry) {
	limit := int64(staticPoolBytes)
	if gfx.GeometryUploadsEnabled {
		var block int64
		gl.GetInteger64v(gl.MAX_SHADER_STORAGE_BLOCK_SIZE, &block)
		if block > 0 {
			limit = min(limit, block)
		}
	}
	// The baked light is the widest record a corner has.
	maxVerts := int32(limit / (gfx.LightStride * 4))
	start := 0
	var count int32
	flush := func(end int) {
		if end > start {
			sb.uploadPool(s, sb.groups[start:end])
		}
		start, count = end, 0
	}
	for gi, grp := range sb.groups {
		n := int32(0)
		for _, si := range grp.surfaces {
			n += int32(len(s.Surfaces[si].Geometry.Vertices) / 5)
		}
		if count > 0 && (count+n > maxVerts || !StaticPools) {
			flush(gi)
		}
		count += n
	}
	flush(len(sb.groups))
}

// uploadPool makes one pool of groups.
func (sb *staticBatches) uploadPool(s *world.StaticGeometry, groups []*staticGroup) {
	pool := &staticPool{}
	pi := len(sb.pools)
	sb.pools = append(sb.pools, pool)
	anyNormals, anyLight := false, false
	for _, grp := range groups {
		anyNormals = anyNormals || grp.key.mat.Normals
		anyLight = anyLight || grp.key.mat.Baked
	}
	var verts, normals, light []float32
	for _, grp := range groups {
		grp.pool, grp.vertBase = pi, int32(len(verts)/5)
		for _, si := range grp.surfaces {
			g := &s.Surfaces[si].Geometry
			grp.firsts = append(grp.firsts, int32(len(verts)/5))
			grp.counts = append(grp.counts, int32(len(g.Vertices)/5))
			verts = append(verts, g.Vertices...)
			// The normals and the light line up with the corners whatever a
			// surface carries: a group without them (or a surface short of
			// them) reads what its own vertex array would have, nothing.
			want := len(verts) / 5
			if anyNormals {
				if grp.key.mat.Normals {
					normals = append(normals, g.Normals[:min(len(g.Normals), (want-len(normals)/3)*3)]...)
				}
				for len(normals) < want*3 {
					normals = append(normals, 0)
				}
			}
			if anyLight {
				if grp.key.mat.Baked {
					l := g.BakedLightData()
					light = append(light, l[:min(len(l), (want-len(light)/gfx.LightStride)*gfx.LightStride)]...)
				}
				for len(light) < want*gfx.LightStride {
					light = append(light, staticLightPad[len(light)%gfx.LightStride])
				}
			}
		}
	}
	pool.verts = int32(len(verts) / 5)
	pool.batch.Upload(verts, normals, light)
	for _, grp := range groups {
		grp.batch.Vao, grp.batch.Vbo, grp.batch.Nbo, grp.batch.Lbo = pool.batch.Vao, pool.batch.Vbo, pool.batch.Nbo, pool.batch.Lbo
		if !grp.key.mat.Normals {
			// The group's own normals flag, as its own buffers would have
			// said: the pool's normals are padding for it.
			grp.batch.Nbo = 0
		}
		if !grp.key.mat.Baked {
			grp.batch.Lbo = 0
		}
		n := int32(0)
		for _, c := range grp.counts {
			n += c
		}
		grp.batch.Verts = n
	}
}

// noteTransparency records each group's IsTransparent for this frame.
// Decided per frame rather than at the build for the reason Material says:
// the texture editor can give a texture alpha while the game runs.
func (sb *staticBatches) noteTransparency() {
	if len(sb.transparent) != len(sb.groups) {
		sb.transparent = make([]bool, len(sb.groups))
	}
	h := uint64(14695981039346656037)
	sb.anyTransparent = false
	for gi, grp := range sb.groups {
		t := frameSurface(&grp.batch.Material).transparent
		sb.transparent[gi] = t
		if t {
			h ^= uint64(gi) + 1
			h *= 1099511628211
			sb.anyTransparent = true
		}
	}
	sb.transparentSig = h
}

// buildClusters cuts the group's (spatially sorted) surfaces into runs and
// boxes each run, and the group.
func (g *staticGroup) buildClusters(s *world.StaticGeometry) {
	g.clusters = g.clusters[:0]
	for start := 0; start < len(g.surfaces); start += staticClusterSize {
		end := min(start+staticClusterSize, len(g.surfaces))
		c := staticCluster{start: int32(start), end: int32(end),
			lo: s.Surfaces[g.surfaces[start]].Lo, hi: s.Surfaces[g.surfaces[start]].Hi}
		for _, si := range g.surfaces[start+1 : end] {
			c.lo, c.hi = geom.VecMin(c.lo, s.Surfaces[si].Lo), geom.VecMax(c.hi, s.Surfaces[si].Hi)
		}
		if start == 0 {
			g.lo, g.hi = c.lo, c.hi
		} else {
			g.lo, g.hi = geom.VecMin(g.lo, c.lo), geom.VecMax(g.hi, c.hi)
		}
		g.clusters = append(g.clusters, c)
	}
}

func (sb *staticBatches) release() {
	// The groups' buffers are their pools'.
	for _, p := range sb.pools {
		p.batch.Delete()
	}
	sb.pools = nil
	sb.groups = nil
	if sb.gpu != nil {
		sb.gpu.release()
		sb.gpu = nil
	}
}

// addVisible is batchGroup.addVisible for a static group.
func (g *staticGroup) addVisible(first, count int32) {
	if k := len(g.visFirst); k > 0 && g.visFirst[k-1]+g.visCount[k-1] == first {
		g.visCount[k-1] += count
		return
	}
	g.visFirst = append(g.visFirst, first)
	g.visCount = append(g.visCount, count)
}

// addCulled adds the group's surfaces that pass cull, a cluster at a time: a
// cluster wholly outside is skipped without looking at its surfaces, one wholly
// inside goes in as a single range, and only one straddling a plane has its
// surfaces tested one by one.
func (g *staticGroup) addCulled(s *world.StaticGeometry, cull *Frustum) {
	switch ClassifyAABB(*cull, g.lo, g.hi) {
	case FrustumOutside:
		return
	case FrustumInside:
		g.addVisible(g.firsts[0], g.firsts[len(g.firsts)-1]+g.counts[len(g.counts)-1]-g.firsts[0])
		return
	}
	for _, c := range g.clusters {
		switch ClassifyAABB(*cull, c.lo, c.hi) {
		case FrustumOutside:
			continue
		case FrustumInside:
			first := g.firsts[c.start]
			g.addVisible(first, g.firsts[c.end-1]+g.counts[c.end-1]-first)
			continue
		}
		for k := c.start; k < c.end; k++ {
			surf := &s.Surfaces[g.surfaces[k]]
			if AABBInFrustum(*cull, surf.Lo, surf.Hi) {
				g.addVisible(g.firsts[k], g.counts[k])
			}
		}
	}
}

// DrawStaticSurfaces draws the compiled surfaces listed in visible (what
// StaticGeometry.VisibleSurfaces returned) that pass the frustum, one call per
// batch, and hands the transparent ones to queue with the squared distance from
// the camera to the surface's centre. Returns how many surfaces were drawn or
// queued, and the draw calls it took.
func DrawStaticSurfaces(lvl *world.Level, visible []int, frustum Frustum, camPos mgl32.Vec3,
	queue func(d BatchDraw, distance float32)) (drawn, calls int) {
	sb := staticBatchesFor(lvl)
	if sb == nil {
		return 0, 0
	}
	if sb.gpu != nil && naniteCull.ready() {
		// The GPU-driven cull draws the opaque groups (static_nanite.go);
		// only the transparent ones are gathered here, for the sorted pass.
		drawn = sb.gatherVisibleMode(lvl.Static, visible, frustum, camPos, queue, true)
		n, c := sb.gpu.drawCamera(lvl.Static, sb, visible, frustum, camPos)
		return drawn + n, c
	}
	drawn = sb.gatherVisible(lvl.Static, visible, frustum, camPos, queue)
	identity := mgl32.Ident4()
	for _, grp := range sb.groups {
		if len(grp.visFirst) > 0 {
			grp.batch.Draw(identity, grp.visFirst, grp.visCount)
			calls++
			grp.visFirst, grp.visCount = grp.visFirst[:0], grp.visCount[:0]
		}
	}
	return drawn, calls
}

// gatherVisible is DrawStaticSurfaces short of drawing: it leaves each group's
// opaque ranges in visFirst/visCount, in buffer order, and hands the
// transparent surfaces to queue.
func (sb *staticBatches) gatherVisible(s *world.StaticGeometry, visible []int, frustum Frustum, camPos mgl32.Vec3,
	queue func(d BatchDraw, distance float32)) (drawn int) {
	return sb.gatherVisibleMode(s, visible, frustum, camPos, queue, false)
}

// gatherVisibleMode is gatherVisible, leaving out the opaque groups when
// transparentOnly — which the GPU-driven cull draws instead.
func (sb *staticBatches) gatherVisibleMode(s *world.StaticGeometry, visible []int, frustum Frustum, camPos mgl32.Vec3,
	queue func(d BatchDraw, distance float32), transparentOnly bool) (drawn int) {
	if transparentOnly {
		sb.noteTransparency()
		if !sb.anyTransparent {
			// Nothing for this walk to find: the opaque surfaces are the GPU
			// cull's, and a level of tens of thousands of visible surfaces
			// is not walked to learn that.
			return 0
		}
	}
	if transparentOnly {
		visible = sb.transparentVisible(s, visible)
	}
	if len(sb.clusterAt) != len(sb.clusters) {
		sb.clusterAt = make([]uint64, len(sb.clusters))
		sb.clusterIs = make([]FrustumClass, len(sb.clusters))
	}
	sb.passNumber++
	for _, si := range visible {
		if si < 0 || si >= len(sb.groupOf) || sb.groupOf[si] < 0 {
			continue
		}
		if transparentOnly && !sb.transparent[sb.groupOf[si]] {
			continue
		}
		// The surface's cluster is judged once a pass, and only a surface in
		// a cluster straddling the frustum is tested on its own.
		ci := sb.clusterOf[si]
		if sb.clusterAt[ci] != sb.passNumber {
			c := sb.clusters[ci]
			sb.clusterAt[ci], sb.clusterIs[ci] = sb.passNumber, ClassifyAABB(frustum, c.lo, c.hi)
		}
		switch sb.clusterIs[ci] {
		case FrustumOutside:
			continue
		case FrustumIntersects:
			if box := &sb.boxes[si]; !AABBInFrustum(frustum, box.lo, box.hi) {
				continue
			}
		}
		k := sb.posInGroup[si]
		sb.groups[sb.groupOf[si]].visBits[k>>6] |= 1 << (k & 63)
		drawn++
	}
	identity := mgl32.Ident4()
	for gi, grp := range sb.groups {
		var transparent bool
		if transparentOnly {
			// noteTransparency's answer for this frame; nothing in an
			// opaque group was marked.
			if transparent = sb.transparent[gi]; !transparent {
				continue
			}
		} else {
			transparent = frameSurface(&grp.batch.Material).transparent
		}
		// The visible surfaces in buffer order, a word of the bitset at a
		// time; a group nothing of which is visible is a scan of zeros.
		for w, bits := range grp.visBits {
			if bits == 0 {
				continue
			}
			grp.visBits[w] = 0
			for ; bits != 0; bits &= bits - 1 {
				k := w<<6 + stdbits.TrailingZeros64(bits)
				if transparent {
					box := &sb.boxes[grp.surfaces[k]]
					c := box.lo.Add(box.hi).Mul(0.5).Sub(camPos)
					queue(BatchDraw{Batch: &grp.batch, Model: identity, First: grp.firsts[k : k+1], Count: grp.counts[k : k+1],
						DoubleSided: grp.key.doubleSided}, c.Dot(c))
					continue
				}
				grp.addVisible(grp.firsts[k], grp.counts[k])
			}
		}
	}
	return drawn
}

// transparentVisible is the surfaces of visible in transparent groups, in
// visible's order: what gatherVisibleMode's walk would have kept of it.
// Rebuilt only when the PVS answer (its serial, and the list itself) or the
// groups' transparency changed; the frustum is the walk's, every frame.
func (sb *staticBatches) transparentVisible(s *world.StaticGeometry, visible []int) []int {
	var from *int
	if len(visible) > 0 {
		from = &visible[0]
	}
	if sb.transValid && sb.transSerial == s.VisibleSerial() && sb.transSig == sb.transparentSig &&
		sb.transFrom == from && sb.transLen == len(visible) {
		return sb.transVisible
	}
	sb.transVisible = sb.transVisible[:0]
	for _, si := range visible {
		if si < 0 || si >= len(sb.groupOf) || sb.groupOf[si] < 0 || !sb.transparent[sb.groupOf[si]] {
			continue
		}
		sb.transVisible = append(sb.transVisible, si)
	}
	sb.transSerial, sb.transSig, sb.transFrom, sb.transLen, sb.transValid =
		s.VisibleSerial(), sb.transparentSig, from, len(visible), true
	return sb.transVisible
}

// shadowStaticSurfaces casts every compiled surface that is not hidden and
// passes cull, one call per batch, under the identity model the caller has
// bound. Deliberately not the visibility set: a wall the camera cannot see still
// casts the shadow that falls where it can.
func shadowStaticSurfaces(lvl *world.Level, prog uint32, cull *Frustum) {
	sb := staticBatchesFor(lvl)
	if sb == nil {
		return
	}
	if sb.gpu != nil && NaniteShadows && naniteCull.ready() {
		sb.gpu.drawShadow(lvl.Static, sb, prog, cull)
		return
	}
	s := lvl.Static
	for _, grp := range sb.groups {
		if grp.batch.Alpha < 0.05 || s.HiddenFor(grp.key.chunk, grp.coveredBy) || len(grp.surfaces) == 0 {
			continue
		}
		if cull == nil {
			grp.addVisible(grp.firsts[0], grp.firsts[len(grp.firsts)-1]+grp.counts[len(grp.counts)-1]-grp.firsts[0])
		} else {
			grp.addCulled(s, cull)
		}
		if len(grp.visFirst) > 0 {
			grp.batch.DrawRanges(grp.visFirst, grp.visCount)
			grp.visFirst, grp.visCount = grp.visFirst[:0], grp.visCount[:0]
		}
	}
	gl.BindVertexArray(0)
}
