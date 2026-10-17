package render

// chunk_batch.go — a chunk's brush faces, one vertex buffer per material.
//
// A brush face used to be its own gfx.Geometry with a VAO and VBO of its own,
// and a big area is thousands of faces: level1's ground was 6,851 of them,
// about 13,700 GL objects to create when it switched on (2.2 s on a translated
// driver) and one draw call per face in every pass, shadow cascades and cube
// faces included. Now a face keeps only its CPU mesh (gfx.Geometry.CPUOnly) and
// this file owns the GPU side: for each chunk, one gfx.Batch per material —
// texture, blend pair, alpha, smooth or flat, single- or double-sided — holding
// every face of that material, with the range each brush occupies recorded so
// the passes still cull brush by brush and draw what survives with one
// glMultiDrawArrays per material.
//
// Nothing tells a batch its chunk changed; it looks, the way the raycast accel
// does. Every mesh change goes through BrushMesh.Load, Release or Unload, which
// bump world.BrushMeshGeneration, and only when that has moved is each chunk's
// grouping recomputed and fingerprinted — over the faces' vertex slices, which
// Load always replaces — so an edit to one brush re-uploads the materials that
// brush uses and leaves every other chunk and material alone.
//
// Batches are keyed by level and chunk ID, never by *Chunk: a level's chunk
// slice is compacted and re-appended by prefabs, entity geometry and the
// editor, and a pointer into it would describe whichever chunk moved into its
// slot. A chunk whose ID has gone from the level loses its batch at the next
// generation change.
//
// Every chunk is batched whether it is enabled or not, for the reason chunk
// geometry is built at load whether it is enabled or not: switching an area on
// must not stall. Only a chunk that is actually Unloaded has none.

import (
	"unsafe"

	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// batchKey is what faces must share to be drawn together.
type batchKey struct {
	mat         gfx.Material
	doubleSided bool
}

// batchGroup is one material's faces in one chunk.
type batchGroup struct {
	key   batchKey
	batch gfx.Batch
	fp    uint64
	// One range per brush that has faces of this material, in brush order.
	// brushOf[i] is the brush index range i belongs to.
	firsts, counts []int32
	brushOf        []int32
	// visFirst/visCount collect the ranges a pass has found visible. Scratch,
	// reused from pass to pass.
	visFirst, visCount []int32
	// keep holds the face slices the buffer was built from. The fingerprint is
	// over their addresses, and an address is only a trustworthy identity
	// while the array is alive: a slice freed by a reload could otherwise be
	// reallocated at the same address and length, and the stale buffer would
	// pass for current.
	keep [][]float32
}

// rangeRef names one range of one group.
type rangeRef struct{ group, index int32 }

// ChunkBatch is one chunk's batches.
type ChunkBatch struct {
	groups []*batchGroup
	// byBrush[bi] lists every range brush bi has, one per material it uses.
	byBrush [][]rangeRef
	// boxes is each brush's box in chunk space, around exactly the vertices
	// the batches draw for it — authored, before any placement, which a moved
	// brush's box is carried through at draw time. Rebuilt with byBrush, so it
	// can only be as stale as the batches themselves. An empty box (lo > hi)
	// for a brush with nothing drawn.
	boxes [][2]mgl32.Vec3
	// gpu is the GPU-driven cull's half (chunk_nanite.go), built the first
	// time the chunk is drawn that way.
	gpu *chunkGPU
}

// levelBatches is every chunk batch of one level.
type levelBatches struct {
	gen    uint64 // world.BrushMeshGeneration the batches were last checked at
	synced bool
	chunks map[string]*ChunkBatch
}

var batchCache = map[*world.Level]*levelBatches{}

// SyncChunkBatches brings every chunk's batches into line with its brushes,
// if any brush mesh has changed since the last call. Cheap when nothing has.
//
// The draw paths call it themselves, so calling it is never required for
// correctness; the frame loop calls it before the passes, and a level install
// straight after the load, so the upload happens behind the loading screen
// rather than on the first frame the level is seen.
func SyncChunkBatches(lvl *world.Level) {
	if lvl == nil {
		return
	}
	levelBatchesFor(lvl)
	staticBatchesFor(lvl)
}

// ReleaseChunkBatches frees every batch the level has, for a level that is
// going away rather than being replaced in place — the entity editor's preview.
func ReleaseChunkBatches(lvl *world.Level) {
	lb := batchCache[lvl]
	if lb == nil {
		return
	}
	for _, cb := range lb.chunks {
		cb.release()
	}
	delete(batchCache, lvl)
	if sb := staticCache[lvl]; sb != nil {
		sb.release()
		delete(staticCache, lvl)
	}
}

// levelBatchesFor returns the level's batches, resyncing them first if brush
// meshes have changed.
func levelBatchesFor(lvl *world.Level) *levelBatches {
	lb := batchCache[lvl]
	if lb == nil {
		lb = &levelBatches{chunks: map[string]*ChunkBatch{}}
		batchCache[lvl] = lb
	}
	gen := world.BrushMeshGeneration()
	if lb.synced && lb.gen == gen {
		return lb
	}
	lb.gen, lb.synced = gen, true
	seen := make(map[string]bool, len(lvl.Chunks))
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if seen[ch.ID] {
			continue // a transient duplicate ID; the first one owns the batch
		}
		seen[ch.ID] = true
		lb.syncChunk(ch)
	}
	for id, cb := range lb.chunks {
		if !seen[id] {
			cb.release()
			delete(lb.chunks, id)
		}
	}
	return lb
}

// chunkBatchFor returns ch's batches, building them if the chunk has none yet
// (a chunk that appeared with no generation change — a rename, say).
func chunkBatchFor(lvl *world.Level, ch *world.Chunk) *ChunkBatch {
	lb := levelBatchesFor(lvl)
	if cb, ok := lb.chunks[ch.ID]; ok {
		return cb
	}
	return lb.syncChunk(ch)
}

// faceRef is one face on its way into a group.
type faceRef struct {
	brush int32
	g     *gfx.Geometry
}

// pendingGroup is a group being gathered.
type pendingGroup struct {
	key   batchKey
	fp    uint64
	faces []faceRef
}

func mix(h, v uint64) uint64 { return (h ^ v) * 1099511628211 }

// syncChunk regroups one chunk's faces, re-uploads the groups whose faces have
// changed, and frees the groups no face uses any more.
func (lb *levelBatches) syncChunk(ch *world.Chunk) *ChunkBatch {
	cb := lb.chunks[ch.ID]
	if ch.Unloaded {
		if cb != nil {
			cb.release()
			delete(lb.chunks, ch.ID)
		}
		return nil
	}
	if cb == nil {
		cb = &ChunkBatch{}
		lb.chunks[ch.ID] = cb
	}

	var order []*pendingGroup
	index := map[batchKey]*pendingGroup{}
	for bi := range ch.Brushes {
		b := &ch.Brushes[bi]
		if !ch.DrawsBrushLive(b) {
			continue // baked into a compiled level's static surfaces
		}
		faces := b.DrawFaces()
		for fi := range faces {
			f := &faces[fi]
			g := &f.Geometry
			if f.NoDraw || len(g.Vertices) == 0 {
				continue
			}
			key := batchKey{
				mat: gfx.Material{
					Texture: g.Texture, MaterialName: g.MaterialName,
					BlendTexture: g.BlendTexture, BlendMask: g.BlendMask,
					Alpha: g.Alpha, Normals: len(g.Normals) > 0, Smooth: g.Smooth,
				},
				doubleSided: f.DoubleSided,
			}
			pg := index[key]
			if pg == nil {
				pg = &pendingGroup{key: key, fp: 14695981039346656037}
				index[key] = pg
				order = append(order, pg)
			}
			pg.faces = append(pg.faces, faceRef{brush: int32(bi), g: g})
			pg.fp = mix(pg.fp, uint64(bi))
			pg.fp = mix(pg.fp, uint64(uintptr(unsafe.Pointer(&g.Vertices[0]))))
			pg.fp = mix(pg.fp, uint64(len(g.Vertices)))
			if len(g.Normals) > 0 {
				pg.fp = mix(pg.fp, uint64(uintptr(unsafe.Pointer(&g.Normals[0]))))
				pg.fp = mix(pg.fp, uint64(len(g.Normals)))
			}
		}
	}

	old := map[batchKey]*batchGroup{}
	for _, g := range cb.groups {
		old[g.key] = g
	}
	groups := make([]*batchGroup, 0, len(order))
	for _, pg := range order {
		g := old[pg.key]
		delete(old, pg.key)
		if g == nil {
			g = &batchGroup{key: pg.key}
			g.batch.Material = pg.key.mat
		} else if g.fp == pg.fp {
			groups = append(groups, g)
			continue
		}
		g.rebuild(pg)
		groups = append(groups, g)
	}
	for _, g := range old {
		g.batch.Delete()
	}
	cb.groups = groups

	cb.byBrush = make([][]rangeRef, len(ch.Brushes))
	for gi, g := range groups {
		for ri, bi := range g.brushOf {
			cb.byBrush[bi] = append(cb.byBrush[bi], rangeRef{group: int32(gi), index: int32(ri)})
		}
	}
	cb.boxes = make([][2]mgl32.Vec3, len(ch.Brushes))
	for bi := range cb.boxes {
		cb.boxes[bi] = emptyBox3()
	}
	for _, pg := range order {
		for _, fr := range pg.faces {
			box := &cb.boxes[fr.brush]
			v := fr.g.Vertices
			for i := 0; i+2 < len(v); i += 5 {
				p := mgl32.Vec3{v[i], v[i+1], v[i+2]}
				box[0], box[1] = geom.VecMin(box[0], p), geom.VecMax(box[1], p)
			}
		}
	}
	return cb
}

// emptyBox3 is a box that any point grows to fit.
func emptyBox3() [2]mgl32.Vec3 {
	const inf = float32(3.4e38)
	return [2]mgl32.Vec3{{inf, inf, inf}, {-inf, -inf, -inf}}
}

// brushBox is where brush bi of ch is drawn, in chunk space: its cached box,
// carried through the brush's placement when a script has moved it.
func (cb *ChunkBatch) brushBox(ch *world.Chunk, bi int) (mgl32.Vec3, mgl32.Vec3) {
	box := cb.boxes[bi]
	if b := &ch.Brushes[bi]; !b.AtRest() {
		return TransformAABB(b.Placement(), box[0], box[1])
	}
	return box[0], box[1]
}

// rebuild concatenates the group's faces and uploads them.
func (g *batchGroup) rebuild(pg *pendingGroup) {
	g.fp = pg.fp
	g.firsts, g.counts, g.brushOf = g.firsts[:0], g.counts[:0], g.brushOf[:0]
	nv, nn := 0, 0
	for _, fr := range pg.faces {
		nv += len(fr.g.Vertices)
		nn += len(fr.g.Normals)
	}
	verts := make([]float32, 0, nv)
	var normals []float32
	if pg.key.mat.Normals {
		normals = make([]float32, 0, nn)
	}
	g.keep = g.keep[:0]
	for _, fr := range pg.faces {
		g.keep = append(g.keep, fr.g.Vertices, fr.g.Normals)
		first := int32(len(verts) / 5)
		n := int32(len(fr.g.Vertices) / 5)
		verts = append(verts, fr.g.Vertices...)
		if normals != nil {
			normals = append(normals, fr.g.Normals...)
		}
		// Faces arrive in brush order, so a brush's faces of one material are
		// consecutive and fold into one range.
		if k := len(g.brushOf); k > 0 && g.brushOf[k-1] == fr.brush {
			g.counts[k-1] += n
			continue
		}
		g.firsts = append(g.firsts, first)
		g.counts = append(g.counts, n)
		g.brushOf = append(g.brushOf, fr.brush)
	}
	g.batch.Upload(verts, normals, nil)
}

func (cb *ChunkBatch) release() {
	for _, g := range cb.groups {
		g.batch.Delete()
		g.keep = nil
	}
	cb.groups, cb.byBrush = nil, nil
	if cb.gpu != nil {
		cb.gpu.release()
		cb.gpu = nil
	}
}

// BatchDraw is a deferred draw of some ranges of one chunk batch — what the
// sorted transparent pass queues for a batched brush.
type BatchDraw struct {
	Batch        *gfx.Batch
	Model        mgl32.Mat4
	First, Count []int32
	DoubleSided  bool
}

// Draw issues it through the world program.
func (d *BatchDraw) Draw() { d.Batch.Draw(d.Model, d.First, d.Count) }

// ChunkBrushStats is what one chunk's walk drew, for the debug overlay.
type ChunkBrushStats struct {
	Brushes   int
	DrawCalls int
}

// DrawChunkBrushes draws the brushes of ch through the world program: every
// brush culled against frustum by its world box, the survivors' ranges merged
// per material and drawn with one call each. A brush a script has moved is
// drawn on its own under its placement. Transparent materials are handed to
// queue instead, one entry per brush, with the squared distance from the
// camera to the brush's centre as the sort key — decided here rather than when
// the batch was built, because a texture can gain alpha while the game runs.
func DrawChunkBrushes(lvl *world.Level, ch *world.Chunk, frustum Frustum, chunkModel mgl32.Mat4, camPos mgl32.Vec3,
	queue func(d BatchDraw, distance float32)) ChunkBrushStats {
	var st ChunkBrushStats
	cb := chunkBatchFor(lvl, ch)
	if cb == nil {
		return st
	}
	// A TAA frame's motion vectors need the chunk's matrix (taa.go).
	taaNoteChunk(ch, cb, chunkModel)
	// The frustum is carried into the chunk once, rather than every brush's
	// box out of it.
	local := frustum.InSpaceOf(chunkModel)
	// The GPU-driven cull (chunk_nanite.go) takes the opaque brushes at rest;
	// the walk below still counts them, and does the moved and the
	// transparent ones itself.
	gpu := chunkGPUFor(cb)
	for bi := range ch.Brushes {
		if bi >= len(cb.byBrush) || len(cb.byBrush[bi]) == 0 {
			continue
		}
		brush := &ch.Brushes[bi]
		bMin, bMax := cb.brushBox(ch, bi)
		if !AABBInFrustum(local, bMin, bMax) {
			continue
		}
		st.Brushes++
		model := chunkModel
		moved := !brush.AtRest()
		if moved {
			model = chunkModel.Mul4(brush.Placement())
		}
		// The sort key is the box's centre in the world: a box's centre is
		// carried to the centre of the carried box.
		mid := bMin.Add(bMax).Mul(0.5)
		c := chunkModel.Mul4x1(mid.Vec4(1)).Vec3().Sub(camPos)
		for _, ref := range cb.byBrush[bi] {
			g := cb.groups[ref.group]
			first, count := g.firsts[ref.index:ref.index+1], g.counts[ref.index:ref.index+1]
			// Once a frame per group, not once per brush that has faces in
			// it: the answer is the material library's, behind its lock.
			if frameSurface(&g.batch.Material).transparent {
				queue(BatchDraw{Batch: &g.batch, Model: model, First: first, Count: count,
					DoubleSided: g.key.doubleSided}, c.Dot(c))
				continue
			}
			if moved {
				g.batch.Draw(model, first, count)
				taaNoteBrush(brush, &g.batch, first[0], count[0], model)
				st.DrawCalls++
				continue
			}
			if gpu != nil {
				continue
			}
			g.addVisible(first[0], count[0])
		}
	}
	if gpu != nil {
		st.DrawCalls += gpu.drawCamera(ch, cb, frustum, chunkModel)
		return st
	}
	for _, g := range cb.groups {
		if len(g.visFirst) == 0 {
			continue
		}
		g.batch.Draw(chunkModel, g.visFirst, g.visCount)
		st.DrawCalls++
		g.visFirst, g.visCount = g.visFirst[:0], g.visCount[:0]
	}
	return st
}

// addVisible appends a range to the pass's list, extending the last one when
// the two are contiguous in the buffer.
func (g *batchGroup) addVisible(first, count int32) {
	if k := len(g.visFirst); k > 0 && g.visFirst[k-1]+g.visCount[k-1] == first {
		g.visCount[k-1] += count
		return
	}
	g.visFirst = append(g.visFirst, first)
	g.visCount = append(g.visCount, count)
}

// shadowChunkBrushes is DrawChunkBrushes for a depth pass: the program and its
// skinning state are the caller's, only position is read, and nothing is
// transparent — but a material too faint to see (alpha under 0.05) casts
// nothing, as a single surface's shadow never has. *current tracks the model
// matrix bound to modelUniform, so an unmoved chunk uploads none.
func shadowChunkBrushes(lvl *world.Level, ch *world.Chunk, chunkModel mgl32.Mat4, cull *Frustum,
	prog uint32, modelUniform int32, current *mgl32.Mat4) {
	cb := chunkBatchFor(lvl, ch)
	if cb == nil {
		return
	}
	// The unmoved brushes per cluster on the GPU (chunk_nanite.go) when it
	// can; the walk below then casts only the moved ones.
	var gpu *chunkGPU
	if NaniteShadows {
		gpu = chunkGPUFor(cb)
	}
	setModel := func(m mgl32.Mat4) {
		if m != *current {
			*current = m
			gl.UniformMatrix4fv(modelUniform, 1, false, &current[0])
		}
	}
	var localCull Frustum
	if cull != nil {
		localCull = cull.InSpaceOf(chunkModel)
	}
	for bi := range ch.Brushes {
		if bi >= len(cb.byBrush) || len(cb.byBrush[bi]) == 0 {
			continue
		}
		brush := &ch.Brushes[bi]
		// Culled in the chunk's own space, against where the brush is drawn
		// (brushBox carries a moved one to where it IS).
		if cull != nil {
			bMin, bMax := cb.brushBox(ch, bi)
			if !AABBInFrustum(localCull, bMin, bMax) {
				continue
			}
		}
		moved := !brush.AtRest()
		for _, ref := range cb.byBrush[bi] {
			g := cb.groups[ref.group]
			if !batchCastsShadow(&g.batch.Material) {
				continue
			}
			if moved {
				setModel(chunkModel.Mul4(brush.Placement()))
				g.batch.DrawRanges(g.firsts[ref.index:ref.index+1], g.counts[ref.index:ref.index+1])
				continue
			}
			if gpu != nil {
				continue
			}
			g.addVisible(g.firsts[ref.index], g.counts[ref.index])
		}
	}
	if gpu != nil {
		gpu.drawShadow(ch, cb, chunkModel, cull, prog, modelUniform, current)
		return
	}
	for _, g := range cb.groups {
		if len(g.visFirst) == 0 {
			continue
		}
		setModel(chunkModel)
		g.batch.DrawRanges(g.visFirst, g.visCount)
		g.visFirst, g.visCount = g.visFirst[:0], g.visCount[:0]
	}
}

// EachChunkBatch calls fn with every batch of ch, for a caller drawing the
// chunk whole under a program of its own — the entity editor's preview, which
// neither culls nor moves brushes. fn sets its uniforms from the batch's
// material and draws with b.DrawRanges(AllOf(b)).
func EachChunkBatch(lvl *world.Level, ch *world.Chunk, fn func(b *gfx.Batch)) {
	cb := chunkBatchFor(lvl, ch)
	if cb == nil {
		return
	}
	for _, g := range cb.groups {
		fn(&g.batch)
	}
}

// ChunkBatchObjectCount reports how many GL buffer objects the level's chunk
// batches hold, for measuring and the tests.
func ChunkBatchObjectCount(lvl *world.Level) (batches, buffers int) {
	lb := batchCache[lvl]
	if lb == nil {
		return 0, 0
	}
	for _, cb := range lb.chunks {
		for _, g := range cb.groups {
			batches++
			if g.batch.Vbo != 0 {
				buffers++
			}
			if g.batch.Nbo != 0 {
				buffers++
			}
		}
	}
	return batches, buffers
}
