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
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

type staticKey struct {
	chunk       string
	mat         gfx.Material
	doubleSided bool
}

type staticGroup struct {
	key   staticKey
	batch gfx.Batch
	// surfaces lists the group's surface indexes in buffer order; firsts and
	// counts are their ranges, parallel to it.
	surfaces       []int32
	firsts, counts []int32
	// visFirst/visCount are pass scratch, as in chunk_batch.go.
	visFirst, visCount []int32
}

type staticBatches struct {
	// static is what these were built from. Holding the pointer is also what
	// stops a freed StaticGeometry's address being reused by the next one and
	// passing for it.
	static  *world.StaticGeometry
	count   int
	groups  []*staticGroup
	groupOf []int32 // per surface: its group, or -1 for one with nothing to draw
	visible []bool  // per surface: scratch for a pass
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
	if sb != nil && sb.static == lvl.Static && sb.count == len(lvl.Static.Surfaces) {
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
		visible: make([]bool, len(s.Surfaces))}
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
			mat: gfx.Material{Texture: g.Texture, BlendTexture: g.BlendTexture, BlendMask: g.BlendMask,
				Alpha: g.Alpha, Smooth: len(g.Normals) > 0, Baked: len(light) > 0},
			doubleSided: surf.DoubleSided,
		}
		gi, ok := index[key]
		if !ok {
			gi = len(sb.groups)
			index[key] = gi
			grp := &staticGroup{key: key}
			grp.batch.Material = key.mat
			sb.groups = append(sb.groups, grp)
		}
		sb.groupOf[i] = int32(gi)
		sb.groups[gi].surfaces = append(sb.groups[gi].surfaces, int32(i))
	}
	for _, grp := range sb.groups {
		var verts, normals, light []float32
		for _, si := range grp.surfaces {
			g := &s.Surfaces[si].Geometry
			grp.firsts = append(grp.firsts, int32(len(verts)/5))
			grp.counts = append(grp.counts, int32(len(g.Vertices)/5))
			verts = append(verts, g.Vertices...)
			if grp.key.mat.Smooth {
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
	s := lvl.Static
	for _, si := range visible {
		if si < 0 || si >= len(sb.visible) || sb.groupOf[si] < 0 {
			continue
		}
		surf := &s.Surfaces[si]
		if !AABBInFrustum(frustum, surf.Lo, surf.Hi) {
			continue
		}
		sb.visible[si] = true
		drawn++
	}
	identity := mgl32.Ident4()
	for _, grp := range sb.groups {
		transparent := grp.batch.IsTransparent()
		for k, si := range grp.surfaces {
			if !sb.visible[si] {
				continue
			}
			sb.visible[si] = false
			if transparent {
				surf := &s.Surfaces[si]
				c := surf.Lo.Add(surf.Hi).Mul(0.5).Sub(camPos)
				queue(BatchDraw{Batch: &grp.batch, Model: identity, First: grp.firsts[k : k+1], Count: grp.counts[k : k+1],
					DoubleSided: grp.key.doubleSided}, c.Dot(c))
				continue
			}
			grp.addVisible(grp.firsts[k], grp.counts[k])
		}
		if len(grp.visFirst) > 0 {
			grp.batch.Draw(identity, grp.visFirst, grp.visCount)
			calls++
			grp.visFirst, grp.visCount = grp.visFirst[:0], grp.visCount[:0]
		}
	}
	return drawn, calls
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
		if grp.batch.Alpha < 0.05 || s.HiddenChunks[grp.key.chunk] {
			continue
		}
		for k, si := range grp.surfaces {
			surf := &s.Surfaces[si]
			if cull != nil && !AABBInFrustum(*cull, surf.Lo, surf.Hi) {
				continue
			}
			grp.addVisible(grp.firsts[k], grp.counts[k])
		}
		if len(grp.visFirst) > 0 {
			grp.batch.DrawRanges(grp.visFirst, grp.visCount)
			grp.visFirst, grp.visCount = grp.visFirst[:0], grp.visCount[:0]
		}
	}
	gl.BindVertexArray(0)
}
