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

import (
	stdbits "math/bits"
	"sort"
	"strings"

	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

type staticKey struct {
	chunk string
	// cover is the surface's CoveredBy, joined: a cover surface shows and
	// hides on other chunks than its own, so it cannot share a batch the
	// shadow pass skips or keeps whole by chunk.
	cover       string
	mat         gfx.Material
	doubleSided bool
}

type staticGroup struct {
	key       staticKey
	coveredBy []string // the surfaces' CoveredBy, which key.cover joins
	batch     gfx.Batch
	// surfaces lists the group's surface indexes in buffer order; firsts and
	// counts are their ranges, parallel to it. The order is spatial (see
	// sortSurfacesSpatially), which is what lets clusters below be both
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
}

// staticCluster is a run of a group's surfaces, [start, end) in the group's
// surfaces list, and the box around all of them.
type staticCluster struct {
	start, end int32
	lo, hi     mgl32.Vec3
}

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
}

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
	sb := &staticBatches{static: s, count: len(s.Surfaces), groupOf: make([]int32, len(s.Surfaces)),
		smoothLighting: gfx.SmoothLighting}
	index := map[staticKey]int{}
	for i := range s.Surfaces {
		surf := &s.Surfaces[i]
		g := &surf.Geometry
		if len(g.Vertices) == 0 {
			sb.groupOf[i] = -1
			continue
		}
		light := g.BakedLightData()
		key := staticKey{
			chunk: surf.Chunk,
			cover: strings.Join(surf.CoveredBy, "\x00"),
			mat: gfx.Material{Texture: g.Texture, MaterialName: g.MaterialName,
				BlendTexture: g.BlendTexture, BlendMask: g.BlendMask,
				Alpha: g.Alpha, Normals: len(g.Normals) > 0, Smooth: g.Smooth, Baked: len(light) > 0},
			doubleSided: surf.DoubleSided,
		}
		gi, ok := index[key]
		if !ok {
			gi = len(sb.groups)
			index[key] = gi
			grp := &staticGroup{key: key, coveredBy: surf.CoveredBy}
			grp.batch.Material = key.mat
			sb.groups = append(sb.groups, grp)
		}
		sb.groupOf[i] = int32(gi)
		sb.groups[gi].surfaces = append(sb.groups[gi].surfaces, int32(i))
	}
	sb.posInGroup = make([]int32, len(s.Surfaces))
	sb.clusterOf = make([]int32, len(s.Surfaces))
	for _, grp := range sb.groups {
		sortSurfacesSpatially(s, grp.surfaces)
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
		var verts, normals, light []float32
		for _, si := range grp.surfaces {
			g := &s.Surfaces[si].Geometry
			grp.firsts = append(grp.firsts, int32(len(verts)/5))
			grp.counts = append(grp.counts, int32(len(g.Vertices)/5))
			verts = append(verts, g.Vertices...)
			if grp.key.mat.Normals {
				normals = append(normals, g.Normals...)
			}
			if grp.key.mat.Baked {
				light = append(light, g.BakedLightData()...)
			}
		}
		grp.batch.Upload(verts, normals, light)
	}
	return sb
}

// sortSurfacesSpatially orders a group's surfaces along a Morton (Z-order)
// curve through their centres, so that surfaces next to each other in the list
// are next to each other in space and any run of them has a tight box.
func sortSurfacesSpatially(s *world.StaticGeometry, surfaces []int32) {
	if len(surfaces) <= staticClusterSize {
		return
	}
	lo, hi := s.Surfaces[surfaces[0]].Lo, s.Surfaces[surfaces[0]].Hi
	for _, si := range surfaces {
		lo, hi = geom.VecMin(lo, s.Surfaces[si].Lo), geom.VecMax(hi, s.Surfaces[si].Hi)
	}
	ext := hi.Sub(lo)
	codes := make(map[int32]uint64, len(surfaces))
	for _, si := range surfaces {
		c := s.Surfaces[si].Lo.Add(s.Surfaces[si].Hi).Mul(0.5)
		var q [3]uint32
		for a := 0; a < 3; a++ {
			if ext[a] > 0 {
				q[a] = uint32((c[a] - lo[a]) / ext[a] * 2097151) // 21 bits
			}
		}
		codes[si] = spreadBits21(q[0]) | spreadBits21(q[1])<<1 | spreadBits21(q[2])<<2
	}
	sort.SliceStable(surfaces, func(a, b int) bool { return codes[surfaces[a]] < codes[surfaces[b]] })
}

// spreadBits21 puts the low 21 bits of v two zero bits apart, for a 63-bit
// Morton code.
func spreadBits21(v uint32) uint64 {
	x := uint64(v) & 0x1fffff
	x = (x | x<<32) & 0x1f00000000ffff
	x = (x | x<<16) & 0x1f0000ff0000ff
	x = (x | x<<8) & 0x100f00f00f00f00f
	x = (x | x<<4) & 0x10c30c30c30c30c3
	x = (x | x<<2) & 0x1249249249249249
	return x
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
	for _, g := range sb.groups {
		g.batch.Delete()
	}
	sb.groups = nil
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
	if len(sb.clusterAt) != len(sb.clusters) {
		sb.clusterAt = make([]uint64, len(sb.clusters))
		sb.clusterIs = make([]FrustumClass, len(sb.clusters))
	}
	sb.passNumber++
	for _, si := range visible {
		if si < 0 || si >= len(sb.groupOf) || sb.groupOf[si] < 0 {
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
			if surf := &s.Surfaces[si]; !AABBInFrustum(frustum, surf.Lo, surf.Hi) {
				continue
			}
		}
		k := sb.posInGroup[si]
		sb.groups[sb.groupOf[si]].visBits[k>>6] |= 1 << (k & 63)
		drawn++
	}
	identity := mgl32.Ident4()
	for _, grp := range sb.groups {
		transparent := grp.batch.IsTransparent()
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
					surf := &s.Surfaces[grp.surfaces[k]]
					c := surf.Lo.Add(surf.Hi).Mul(0.5).Sub(camPos)
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

// shadowStaticSurfaces casts every compiled surface that is not hidden and
// passes cull, one call per batch, under the identity model the caller has
// bound. Deliberately not the visibility set: a wall the camera cannot see still
// casts the shadow that falls where it can.
func shadowStaticSurfaces(lvl *world.Level, cull *Frustum) {
	sb := staticBatchesFor(lvl)
	if sb == nil {
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
