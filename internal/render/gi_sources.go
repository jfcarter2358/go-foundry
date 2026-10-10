package render

// gi_sources.go — what the GI distance field is built from, and what changed.
//
// Everything that can block or bounce light is turned into a SOURCE the
// composite shader (gi_sdf_comp.glsl) knows how to measure the distance to:
//
//   - a brush, as the planes of its convex hull (or of each convex piece of a
//     brush that is not convex — Chunk.ConvexBrushPieces, the split collision
//     and CSG already make). The distance is exact inside and never more than
//     the true one outside, which is the side a sphere trace needs it to err
//     on. Detail meshes are traced as their hull.
//   - a chunk's block terrain, as its block grid; the shader searches a few
//     blocks out for the nearest one of the other kind.
//   - an entity's model, as a baked grid of distances (gi_meshsdf.go) placed
//     by the entity's matrix; and a cooked Nanite mesh's placement, as the
//     grid its cook baked (internal/nanite). Skinned models are left out: a
//     bind-pose grid would be traced in a pose the character is not in, and
//     a person blocks too little light for a few capsules to be worth
//     tracking per frame.
//
// Sources come in GROUPS — one per brush, per terrain, per entity model — each
// with a hash of what it holds. A frame re-derives only the groups whose inputs
// could have moved (a chunk whose matrix or mesh generation changed, a dynamic
// brush, an entity), and a group whose hash comes out different, appears or
// disappears hands its old and new bounds to the field as dirty. Nothing else
// is touched, so a level standing still costs a few comparisons a frame.

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"math"
	"strconv"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// The kinds of source, as gi_sdf_comp.glsl numbers them.
const (
	giKindHull    = 0
	giKindCapsule = 1
	giKindMesh    = 2
	giKindTerrain = 3
)

// giMaxAlbedo caps what a surface hands on, as the bake's MaxAlbedo does: a
// white surface returning everything would let a closed white room brighten
// for ever.
const giMaxAlbedo = 0.9

// giUntextured is the albedo of a surface with no texture — the bake's grey
// (compile.untexturedAlbedo), not the editor's normal colours.
const giUntextured = 0.5

// giSource is one thing the field measures distance to.
type giSource struct {
	lo, hi mgl32.Vec3
	kind   int
	color  mgl32.Vec4 // albedo, emissive
	p0, p1 mgl32.Vec4
	// c1..c3 are a terrain's layers 2..4; color is layer 1.
	c1, c2, c3 mgl32.Vec4
	m          mgl32.Mat4
	planes     []mgl32.Vec4
	mesh       *giMeshSDF
	blocks     []uint8
}

type giGroup struct {
	hash   uint64
	lo, hi mgl32.Vec3
	srcs   []giSource
	seen   uint64
	// static is a chunk's brush or terrain group, re-derived only when the
	// chunk's stamp changes.
	static bool
}

// giKey names a group the set keys itself: what kind of thing it is, what it
// belongs to, and its index there. A struct rather than a formatted string so
// that looking a group up every frame allocates nothing. Only the foliage
// units' groups are kept by key now; the chunks', entities' and placements'
// are owned by their stamps and slots (giChunkStamp, giEntitySlot).
type giKey struct {
	kind  byte // 'f' a foliage unit's trees
	owner string
	a, b  int
}

// giChunkStamp is what a chunk's groups were derived from, and the groups:
// the static ones (its brushes at rest and its terrain), re-derived only when
// the stamp changes, and its dynamic brushes', checked every frame.
//
// The chunk OWNS its groups rather than naming them in a map shared with
// everything else, so that a frame in which a chunk has not changed costs one
// comparison for the whole chunk — not a map lookup (and a hash of its ID)
// per brush to say each was seen, and another for each in the sweep.
type giChunkStamp struct {
	gen, terrainGen uint64
	model           mgl32.Mat4
	// valid is false once forgetStamps has said the chunk may not be the one
	// these were derived from: its static groups are derived again, and
	// diffed against these by content.
	valid  bool
	static []giOwned
	dyn    []giOwned
	seen   uint64
	// nDynamic is how many of the chunk's nBrushes brushes were dynamic when
	// the stamp was taken. A brush only turns dynamic (or appears) with the
	// meshes' generation moving on, so a chunk with none skips the per-frame
	// walk for them.
	nDynamic, nBrushes int
}

// giOwned is one group a chunk, an entity or a placement owns: a brush's
// (idx its brush), the terrain's, or a model's (idx its model).
type giOwned struct {
	idx int
	g   *giGroup
}

// giEntitySlot is what entity number ei held at its last collect: its ID,
// which says it is still the same entity, and its models' groups.
type giEntitySlot struct {
	id     string
	models []giModelSlot
}

// giModelSlot is one entity model's group, with the hash of its path kept
// for as long as the path is the one it was taken of.
type giModelSlot struct {
	g        *giGroup
	path     string
	pathHash uint64
	seen     uint64
}

// giScenerySlot is a cooked placement's group, by its place in the level,
// with the hash of its model and texture names kept while they stand.
type giScenerySlot struct {
	id       string
	g        *giGroup
	model    string
	texture  string
	nameHash uint64
	seen     uint64
}

// giSourceSet is the level's sources between frames.
type giSourceSet struct {
	// groups holds the groups nothing else owns: the foliage units' trees.
	groups map[giKey]*giGroup
	chunks map[string]*giChunkStamp
	// entities and scenery hold the entities' and the cooked placements'
	// groups by their place in the level's lists.
	entities []giEntitySlot
	scenery  []giScenerySlot
	meshes   map[string]*giMeshSDF
	frame    uint64
	// count is how many groups there are, as of the last collect.
	count int
	// dirty is every bound that changed this frame, for the field to rebuild.
	dirty [][2]mgl32.Vec3
	// changed is whether flat has to be rebuilt; staticChanged whether its
	// static part (chunks' brushes and terrain) did, which is what the field's
	// index is built over. A moving entity changes the one, not the other.
	changed, staticChanged bool
	// nStatic is how many of flat's sources, from the front, are static.
	nStatic int
	static  []giSource
	// churn is how many groups appeared, changed or went this frame.
	churn int
	// minModel is the smallest entity model traced, in world units.
	minModel float32
	flat     []giSource
	// finishPending is set between collectWorld and collectFinish.
	finishPending bool
}

func newGISourceSet() *giSourceSet {
	return &giSourceSet{
		groups: map[giKey]*giGroup{},
		chunks: map[string]*giChunkStamp{},
		meshes: map[string]*giMeshSDF{},
	}
}

// collect brings the set up to date with lvl, filling dirty and changed.
func (s *giSourceSet) collect(lvl *world.Level) {
	s.collectWorld(lvl)
	s.collectFinish(lvl)
}

// collectWorld is collect's first half: the chunks, the entities and the
// placements — everything that is the level's own, which is all a frame's
// passes only read. It may run on a goroutine while the main thread draws the
// shadows (frame_prep.go); collectFinish, which reads the renderer's foliage,
// sweeps and flattens, runs on the main thread after it.
func (s *giSourceSet) collectWorld(lvl *world.Level) {
	s.frame++
	if !s.finishPending {
		s.dirty = s.dirty[:0]
		s.churn = 0
	}
	// A half collect never finished would have its dirt dropped with the
	// next one's start; it is carried over instead.
	s.finishPending = true
	s.count = 0
	gen := world.BrushMeshGeneration()
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if !ch.IsEnabled() {
			continue
		}
		model := ch.ModelMatrix()
		var tgen uint64
		if ch.Terrain != nil {
			tgen = ch.Terrain.Generation
		}
		key := chunkKey(ch, ci)
		st := s.chunks[key]
		if st == nil {
			st = &giChunkStamp{}
			s.chunks[key] = st
		}
		if !st.valid || st.gen != gen || st.terrainGen != tgen || st.model != model {
			s.deriveChunk(st, ch, model)
			st.gen, st.terrainGen, st.model, st.valid = gen, tgen, model, true
		}
		st.seen = s.frame
		// Dynamic brushes every frame: a script can move one at any time,
		// and nothing about the chunk says so.
		if st.nDynamic > 0 || len(st.dyn) > 0 || len(ch.Brushes) != st.nBrushes {
			s.dynamicBrushes(st, ch, model, gen)
		}
		s.count += len(st.static) + len(st.dyn)
	}

	s.collectEntities(lvl)

	// Cooked Nanite meshes (internal/nanite), as the distance grid their cook
	// baked — sized to the mesh rather than a fixed 32 cells — placed by the
	// placement's matrix. Static props, so re-derived only when one moves or
	// is re-skinned.
	s.collectScenery(lvl)
}

// collectFinish is collect's second half: the foliage's tree crowns, then
// what went this frame, then the flattened list.
func (s *giSourceSet) collectFinish(lvl *world.Level) {
	s.finishPending = false
	// Tree crowns, from the Nanite foliage's voxels (nfoliage_gi.go).
	s.foliageCanopySources()

	for k, g := range s.groups {
		if g.seen != s.frame {
			s.drop(g)
			delete(s.groups, k)
		}
	}
	s.count += len(s.groups)
	for k, st := range s.chunks {
		// A chunk that has gone (or been switched off) takes its groups with
		// it, and is derived from scratch if it comes back.
		if st.seen != s.frame {
			for _, o := range st.static {
				s.drop(o.g)
			}
			for _, o := range st.dyn {
				s.drop(o.g)
			}
			delete(s.chunks, k)
		}
	}
	if s.changed {
		if s.staticChanged {
			s.static = s.static[:0]
			s.eachChunk(lvl, func(st *giChunkStamp) {
				for _, o := range st.static {
					s.static = append(s.static, o.g.srcs...)
				}
			})
		}
		s.flat = append(s.flat[:0], s.static...)
		s.nStatic = len(s.static)
		s.eachChunk(lvl, func(st *giChunkStamp) {
			for _, o := range st.dyn {
				s.flat = append(s.flat, o.g.srcs...)
			}
		})
		for ei := range s.entities {
			for _, m := range s.entities[ei].models {
				if m.g != nil {
					s.flat = append(s.flat, m.g.srcs...)
				}
			}
		}
		for i := range s.scenery {
			if g := s.scenery[i].g; g != nil {
				s.flat = append(s.flat, g.srcs...)
			}
		}
		for _, g := range s.groups {
			s.flat = append(s.flat, g.srcs...)
		}
	}
}

// eachChunk calls fn with the stamp of every chunk of lvl collect saw this
// frame, in the level's order, so the flattened list comes out the same for
// the same level.
func (s *giSourceSet) eachChunk(lvl *world.Level, fn func(st *giChunkStamp)) {
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if !ch.IsEnabled() {
			continue
		}
		if st := s.chunks[chunkKey(ch, ci)]; st != nil && st.seen == s.frame {
			fn(st)
		}
	}
}

// deriveChunk re-derives a chunk's static groups — every brush at rest, and
// the terrain — and diffs them by content against what the stamp held: a
// group whose hash comes out the same is no change, one that differs or is
// new dirties its bounds, and one that is gone dirties where it was.
func (s *giSourceSet) deriveChunk(st *giChunkStamp, ch *world.Chunk, model mgl32.Mat4) {
	old := st.static
	st.static = nil
	o := 0
	// take is the old group of the same brush (or the terrain, idx -1), if
	// there was one; old is in the same order the new list is made in.
	take := func(idx int) *giGroup {
		for o < len(old) && old[o].idx < idx && old[o].idx >= 0 {
			s.drop(old[o].g)
			o++
		}
		if o < len(old) && old[o].idx == idx {
			g := old[o].g
			o++
			return g
		}
		return nil
	}
	st.nDynamic, st.nBrushes = 0, len(ch.Brushes)
	for bi := range ch.Brushes {
		if ch.Brushes[bi].Dynamic {
			st.nDynamic++
			continue
		}
		srcs := brushSources(ch, bi, model)
		g := take(bi)
		st.static = append(st.static, giOwned{idx: bi, g: s.update(g, true, srcs, hashSources(srcs))})
	}
	// Whatever brushes are left of the old list went with an edit.
	for o < len(old) && old[o].idx >= 0 {
		s.drop(old[o].g)
		o++
	}
	if ch.Terrain != nil {
		srcs := terrainSources(ch.Terrain, model)
		g := take(-1)
		st.static = append(st.static, giOwned{idx: -1, g: s.update(g, true, srcs, hashSources(srcs))})
	}
	for ; o < len(old); o++ {
		s.drop(old[o].g)
	}
}

// dynamicBrushes brings a chunk's dynamic brushes' groups up to date: each
// re-derived when its matrix (or the meshes' generation) is not the one its
// hash was taken of.
func (s *giSourceSet) dynamicBrushes(st *giChunkStamp, ch *world.Chunk, model mgl32.Mat4, gen uint64) {
	k := 0
	for bi := range ch.Brushes {
		b := &ch.Brushes[bi]
		if !b.Dynamic {
			continue
		}
		// The list is in brush order: a group for a brush before this one
		// that is no longer dynamic (or no longer there) goes.
		for k < len(st.dyn) && st.dyn[k].idx < bi {
			s.drop(st.dyn[k].g)
			st.dyn = append(st.dyn[:k], st.dyn[k+1:]...)
		}
		var g *giGroup
		if k < len(st.dyn) && st.dyn[k].idx == bi {
			g = st.dyn[k].g
		} else {
			st.dyn = append(st.dyn, giOwned{})
			copy(st.dyn[k+1:], st.dyn[k:])
			st.dyn[k] = giOwned{idx: bi}
		}
		m := model.Mul4(b.Placement())
		if h := hashMat(m, gen); g != nil && g.hash == h {
			g.seen = s.frame
		} else {
			st.dyn[k].g = s.update(g, false, brushSources(ch, bi, model), h)
		}
		k++
	}
	for _, o := range st.dyn[k:] {
		s.drop(o.g)
	}
	st.dyn = st.dyn[:k]
}

// collectEntities brings the entities' model groups up to date, by the
// entities' places in the level: the slot of an entity that is still the
// same one (its ID) is checked by its matrix's coarse hash and its model's
// path, kept hashed for as long as the path is the same string.
func (s *giSourceSet) collectEntities(lvl *world.Level) {
	n := len(lvl.Entities)
	for ei := n; ei < len(s.entities); ei++ {
		s.dropEntity(&s.entities[ei])
	}
	if len(s.entities) > n {
		clear(s.entities[n:])
		s.entities = s.entities[:n]
	}
	for len(s.entities) < n {
		s.entities = append(s.entities, giEntitySlot{})
	}
	for ei := range lvl.Entities {
		e := &lvl.Entities[ei]
		slot := &s.entities[ei]
		if slot.id != e.ID {
			// Another entity has this place now: the one that had it is
			// gone, as far as its groups are concerned.
			s.dropEntity(slot)
			slot.id = e.ID
		}
		// The player's own body sits on the camera: traced, it would only
		// shade the probes round the eye, and it moves every frame.
		if !e.Visible || e == world.PlayerEntity || e.ID == "player" {
			s.sweepEntity(slot)
			continue
		}
		var base mgl32.Mat4
		based := false
		if len(slot.models) < len(e.Models) {
			slot.models = append(slot.models, make([]giModelSlot, len(e.Models)-len(slot.models))...)
		}
		for mi := range e.Models {
			m := &e.Models[mi]
			if !giModelTraced(e, m) {
				continue
			}
			if !based {
				frame := lvl.Frames.FrameFor(e)
				base = e.DrawMatrix()
				if !frame.IsWorld() {
					base = frame.World.Mul4(base)
				}
				based = true
			}
			ms := &slot.models[mi]
			if ms.path != m.Path || ms.pathHash == 0 {
				ms.path, ms.pathHash = m.Path, hashString(m.Path)
			}
			wm := base.Mul4(e.AttachmentMatrix(m))
			h := hashMatCoarse(wm) ^ ms.pathHash
			ms.seen = s.frame
			if ms.g != nil && ms.g.hash == h {
				ms.g.seen = s.frame
				continue
			}
			ms.g = s.update(ms.g, false, s.modelSources(m, wm, s.minModel), h)
		}
		s.sweepEntity(slot)
	}
}

// sweepEntity drops the groups of an entity's models that this frame did
// not trace, and counts the rest.
func (s *giSourceSet) sweepEntity(slot *giEntitySlot) {
	for mi := range slot.models {
		ms := &slot.models[mi]
		if ms.g == nil {
			continue
		}
		if ms.seen != s.frame {
			s.drop(ms.g)
			ms.g = nil
			continue
		}
		s.count++
	}
}

// dropEntity drops every group of a slot's entity.
func (s *giSourceSet) dropEntity(slot *giEntitySlot) {
	for mi := range slot.models {
		if g := slot.models[mi].g; g != nil {
			s.drop(g)
		}
	}
	slot.models = slot.models[:0]
	slot.id = ""
}

// collectScenery is collectEntities for the cooked placements.
func (s *giSourceSet) collectScenery(lvl *world.Level) {
	n := len(lvl.Scenery)
	for i := n; i < len(s.scenery); i++ {
		if g := s.scenery[i].g; g != nil {
			s.drop(g)
		}
	}
	if len(s.scenery) > n {
		clear(s.scenery[n:])
		s.scenery = s.scenery[:n]
	}
	for len(s.scenery) < n {
		s.scenery = append(s.scenery, giScenerySlot{})
	}
	for i := range lvl.Scenery {
		sc := &lvl.Scenery[i]
		slot := &s.scenery[i]
		if slot.id != sc.ID && slot.g != nil {
			s.drop(slot.g)
			slot.g = nil
		}
		slot.id = sc.ID
		// Its chunk off: not seen, so the drop below takes it out of the field.
		if sc.Nanite != nil && sc.Nanite.SDF != nil && sc.InWorld() {
			tex := sc.SlotTexture(0)
			if slot.nameHash == 0 || slot.model != sc.Model || slot.texture != tex {
				slot.model, slot.texture = sc.Model, tex
				slot.nameHash = hashString(sc.Model) ^ hashString(tex)
			}
			wm := sc.ModelMatrix()
			h := hashMatCoarse(wm) ^ slot.nameHash
			slot.seen = s.frame
			if slot.g != nil && slot.g.hash == h {
				slot.g.seen = s.frame
			} else {
				slot.g = s.update(slot.g, false, naniteSceneryGISources(sc, wm, s.minModel), h)
			}
		}
		if slot.g != nil {
			if slot.seen != s.frame {
				s.drop(slot.g)
				slot.g = nil
				continue
			}
			s.count++
		}
	}
}

// forgetStamps makes the next collect re-derive every chunk's groups, for a
// level whose chunks may be new structs with the same IDs. What they held is
// kept, for the new derivation to be diffed against.
func (s *giSourceSet) forgetStamps() {
	for _, st := range s.chunks {
		st.valid = false
	}
}

// update is old (nil for none) brought to hold srcs under hash h: the same
// group when the hash says nothing changed, a new one — its old and new
// bounds dirtied — when it did.
func (s *giSourceSet) update(old *giGroup, static bool, srcs []giSource, h uint64) *giGroup {
	lo, hi := sourcesBounds(srcs)
	if len(srcs) == 0 && (old == nil || len(old.srcs) == 0) {
		// Nothing in the field before or after — a model too small to
		// trace, moving: remembered, so it is not re-derived next frame,
		// but not a change.
		return &giGroup{hash: h, seen: s.frame, static: static}
	}
	if old != nil && old.hash == h {
		old.seen = s.frame
		old.srcs, old.lo, old.hi = srcs, lo, hi
		return old
	}
	s.staticChanged = s.staticChanged || static || (old != nil && old.static)
	if old != nil {
		s.dirty = append(s.dirty, [2]mgl32.Vec3{old.lo, old.hi})
	}
	if len(srcs) > 0 {
		s.dirty = append(s.dirty, [2]mgl32.Vec3{lo, hi})
	}
	s.changed = true
	s.churn++
	return &giGroup{hash: h, lo: lo, hi: hi, srcs: srcs, seen: s.frame, static: static}
}

// drop is a group going: where it was is dirty.
func (s *giSourceSet) drop(g *giGroup) {
	if g == nil {
		return
	}
	s.dirty = append(s.dirty, [2]mgl32.Vec3{g.lo, g.hi})
	s.changed = true
	s.staticChanged = s.staticChanged || g.static
	s.churn++
}

// put stores a group the set itself keys (a foliage unit's) under its
// content's hash.
func (s *giSourceSet) put(k giKey, static bool, srcs []giSource) {
	s.putHashed(k, static, srcs, hashSources(srcs))
}

func (s *giSourceSet) putHashed(k giKey, static bool, srcs []giSource, h uint64) {
	s.groups[k] = s.update(s.groups[k], static, srcs, h)
}
func chunkKey(ch *world.Chunk, ci int) string {
	if ch.ID != "" {
		return ch.ID
	}
	return "#" + strconv.Itoa(ci)
}

// brushSources is one brush's convex pieces, each a hull in world space. A
// brush with no opaque drawn face is left out: glass, water and clip volumes
// neither block light nor bounce it.
func brushSources(ch *world.Chunk, bi int, chunkModel mgl32.Mat4) []giSource {
	b := &ch.Brushes[bi]
	albedo, ok := brushAlbedo(b)
	if !ok {
		return nil
	}
	m := chunkModel.Mul4(b.Placement())
	normalM := m.Mat3().Inv().Transpose()
	var out []giSource
	for _, piece := range ch.ConvexBrushPieces(bi) {
		if len(piece.Verts) < 4 {
			continue
		}
		src := giSource{kind: giKindHull, color: albedo.Vec4(0)}
		first := true
		for _, v := range piece.Verts {
			p := mulPoint(m, v)
			if first {
				src.lo, src.hi, first = p, p, false
			} else {
				src.lo, src.hi = vmin(src.lo, p), vmax(src.hi, p)
			}
		}
		for fi := range piece.Faces {
			f := &piece.Faces[fi]
			pts := piece.FacePoints(f)
			if len(pts) < 3 {
				continue
			}
			n := normalM.Mul3x1(piece.FaceNormal(f))
			if n.Len() < 1e-6 {
				continue
			}
			n = n.Normalize()
			p := mulPoint(m, pts[0])
			src.planes = append(src.planes, n.Vec4(n.Dot(p)))
		}
		if len(src.planes) >= 4 {
			out = append(out, src)
		}
	}
	return out
}

// brushAlbedo is the mean albedo over a brush's opaque drawn faces, and
// whether it has any.
func brushAlbedo(b *world.Brush) (mgl32.Vec3, bool) {
	var sum mgl32.Vec3
	n := 0
	for fi := range b.DrawFaces() {
		f := &b.DrawFaces()[fi]
		if f.NoDraw || f.Alpha < 0.99 {
			continue
		}
		sum = sum.Add(textureAlbedo(f.DrawTexture()))
		n++
	}
	if n == 0 {
		return mgl32.Vec3{}, false
	}
	return capAlbedo(sum.Mul(1 / float32(n))), true
}

func textureAlbedo(name string) mgl32.Vec3 {
	if c, ok := giTextureMean(name); ok {
		return c
	}
	return mgl32.Vec3{giUntextured, giUntextured, giUntextured}
}

// giTextureMean is a texture's average colour — a variable so the tests can
// colour surfaces without a texture on disk, as the bake's do.
var giTextureMean = gfx.TextureMeanColor

func capAlbedo(c mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{min(c[0], giMaxAlbedo), min(c[1], giMaxAlbedo), min(c[2], giMaxAlbedo)}
}

// terrainSources is a chunk's block grid, as one source.
func terrainSources(t *world.Terrain, chunkModel mgl32.Mat4) []giSource {
	if t == nil || len(t.Blocks) == 0 || t.BlockSize <= 0 {
		return nil
	}
	if _, _, ok := t.SolidRange(); !ok {
		return nil
	}
	size := mgl32.Vec3{float32(t.SizeX), float32(t.SizeY), float32(t.SizeZ)}.Mul(t.BlockSize)
	llo, lhi := t.Origin, t.Origin.Add(size)
	lo, hi := transformBox(chunkModel, llo, lhi)
	// World -> block coordinates: undo the chunk, then the grid's origin and
	// block size.
	grid := mgl32.Scale3D(1/t.BlockSize, 1/t.BlockSize, 1/t.BlockSize).
		Mul4(mgl32.Translate3D(-t.Origin[0], -t.Origin[1], -t.Origin[2])).
		Mul4(chunkModel.Inv())
	scale := mgl32.Vec3{chunkModel[0], chunkModel[1], chunkModel[2]}.Len()
	var layers [world.TerrainLayerCount]mgl32.Vec4
	for i := range layers {
		layers[i] = capAlbedo(textureAlbedo(t.Layers[i])).Vec4(0)
	}
	src := giSource{
		kind: giKindTerrain, lo: lo, hi: hi, m: grid,
		color: layers[0], c1: layers[1], c2: layers[2], c3: layers[3],
		p0:     mgl32.Vec4{0, float32(t.SizeX), float32(t.SizeY), float32(t.SizeZ)},
		p1:     mgl32.Vec4{t.BlockSize * scale, 0, 0, 0},
		blocks: t.Blocks,
	}
	return []giSource{src}
}

// giModelTraced is whether an entity model takes part in GI: drawn, opaque,
// not giving off light of its own, and not skinned.
func giModelTraced(e *world.Entity, m *world.EntityModel) bool {
	if !entityDrawable(e, m) || m.Unlit || (m.Alpha > 0 && m.Alpha < 0.99) {
		return false
	}
	for si := range m.Submeshes {
		if len(m.Submeshes[si].SkinIdx) > 0 {
			return false
		}
	}
	return true
}

// modelSources is one entity model as a placed distance grid.
func (s *giSourceSet) modelSources(m *world.EntityModel, worldM mgl32.Mat4, minSize float32) []giSource {
	key := m.Path
	if key == "" && len(m.Submeshes) > 0 && len(m.Submeshes[0].Vertices) > 0 {
		key = fmt.Sprintf("%p", &m.Submeshes[0].Vertices[0])
	}
	sdf, ok := s.meshes[key]
	if !ok {
		// Walked through ForEachTriangle: a model's submeshes are indexed
		// (gfx.IndexedModels), so Vertices is a pool, not a triangle list.
		var tris []mgl32.Vec3
		for si := range m.Submeshes {
			m.Submeshes[si].ForEachTriangle(func(a, b, c mgl32.Vec3) bool {
				tris = append(tris, a, b, c)
				return true
			})
		}
		sdf = bakeMeshSDF(tris, giMeshRes)
		s.meshes[key] = sdf
	}
	if sdf == nil {
		return nil
	}
	tex := m.Texture
	if tex == "" && len(m.Submeshes) > 0 {
		tex = m.Submeshes[0].Texture
	}
	lo, hi := transformBox(worldM, sdf.Lo, sdf.Hi)
	// Smaller than half a probe cell, a model changes nothing GI can show,
	// and tracking it would rebuild the field round it every time it moves.
	if ext := hi.Sub(lo); max(ext[0], ext[1], ext[2]) < minSize {
		return nil
	}
	scale := (mgl32.Vec3{worldM[0], worldM[1], worldM[2]}.Len() +
		mgl32.Vec3{worldM[4], worldM[5], worldM[6]}.Len() +
		mgl32.Vec3{worldM[8], worldM[9], worldM[10]}.Len()) / 3
	// World -> grid coordinates, voxel centres at integers.
	grid := mgl32.Scale3D(1/sdf.Cell, 1/sdf.Cell, 1/sdf.Cell).
		Mul4(mgl32.Translate3D(-sdf.Lo[0], -sdf.Lo[1], -sdf.Lo[2])).
		Mul4(worldM.Inv())
	return []giSource{{
		kind: giKindMesh, lo: lo, hi: hi, m: grid, mesh: sdf,
		color: capAlbedo(textureAlbedo(tex)).Vec4(0),
		p0:    mgl32.Vec4{0, float32(sdf.N), sdf.Cell * scale, 0},
	}}
}

// naniteSceneryGISources is a cooked placement as a placed distance grid —
// modelSources, with the grid its cook baked.
func naniteSceneryGISources(sc *world.Scenery, worldM mgl32.Mat4, minSize float32) []giSource {
	sdf := sc.Nanite.SDF
	lo, hi := transformBox(worldM, sdf.Lo, sdf.Hi)
	if ext := hi.Sub(lo); max(ext[0], ext[1], ext[2]) < minSize {
		return nil
	}
	scale := (mgl32.Vec3{worldM[0], worldM[1], worldM[2]}.Len() +
		mgl32.Vec3{worldM[4], worldM[5], worldM[6]}.Len() +
		mgl32.Vec3{worldM[8], worldM[9], worldM[10]}.Len()) / 3
	grid := mgl32.Scale3D(1/sdf.Cell, 1/sdf.Cell, 1/sdf.Cell).
		Mul4(mgl32.Translate3D(-sdf.Lo[0], -sdf.Lo[1], -sdf.Lo[2])).
		Mul4(worldM.Inv())
	albedo := textureAlbedo(sc.SlotTexture(0))
	if sc.SlotTexture(0) == "" && len(sc.Nanite.Slots) > 0 {
		// An untextured slot draws in its normal's colour; GI takes its
		// authored base colour, which is closer to what it is meant to be.
		bc := sc.Nanite.Slots[0].BaseColor
		albedo = mgl32.Vec3{bc[0], bc[1], bc[2]}
	}
	return []giSource{{
		kind: giKindMesh, lo: lo, hi: hi, m: grid, mesh: sdf,
		color: capAlbedo(albedo).Vec4(0),
		p0:    mgl32.Vec4{0, float32(sdf.N), sdf.Cell * scale, 0},
	}}
}

func sourcesBounds(srcs []giSource) (lo, hi mgl32.Vec3) {
	for i, s := range srcs {
		if i == 0 {
			lo, hi = s.lo, s.hi
			continue
		}
		lo, hi = vmin(lo, s.lo), vmax(hi, s.hi)
	}
	return lo, hi
}

func mulPoint(m mgl32.Mat4, p mgl32.Vec3) mgl32.Vec3 {
	r := m.Mul4x1(p.Vec4(1))
	return mgl32.Vec3{r[0], r[1], r[2]}
}

func vmin(a, b mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{min(a[0], b[0]), min(a[1], b[1]), min(a[2], b[2])}
}

func vmax(a, b mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{max(a[0], b[0]), max(a[1], b[1]), max(a[2], b[2])}
}

// hashSources hashes what a group's sources would write into the field.
func hashSources(srcs []giSource) uint64 {
	h := fnv.New64a()
	var buf [4]byte
	f := func(v float32) {
		binary.LittleEndian.PutUint32(buf[:], math.Float32bits(v))
		h.Write(buf[:])
	}
	for _, s := range srcs {
		f(float32(s.kind))
		for _, v := range [][]float32{s.color[:], s.p0[:], s.p1[:], s.c1[:], s.c2[:], s.c3[:], s.m[:]} {
			for _, x := range v {
				f(x)
			}
		}
		for _, p := range s.planes {
			f(p[0])
			f(p[1])
			f(p[2])
			f(p[3])
		}
		if s.blocks != nil {
			h.Write(s.blocks)
		}
		if s.mesh != nil {
			fmt.Fprintf(h, "%p", s.mesh)
		}
	}
	return h.Sum64()
}

// hashMat is a matrix's exact hash, with gen folded in so a mesh edit on a
// dynamic brush that has not moved still re-derives it.
func hashMat(m mgl32.Mat4, gen uint64) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	for _, x := range m {
		binary.LittleEndian.PutUint32(buf[:4], math.Float32bits(x))
		h.Write(buf[:4])
	}
	binary.LittleEndian.PutUint64(buf[:], gen)
	h.Write(buf[:])
	return h.Sum64()
}

// hashMatCoarse hashes a matrix rounded to a fraction of the finest voxel, so
// an entity that is idling in place — breathing, a float's worth of drift —
// does not dirty the field every frame.
func hashMatCoarse(m mgl32.Mat4) uint64 {
	h := fnv.New64a()
	var buf [4]byte
	for i, x := range m {
		q := float64(x) * 256 // rotation and scale terms
		if i >= 12 {
			q = float64(x) * 32 // translation, to 1/32 of a unit
		}
		binary.LittleEndian.PutUint32(buf[:], uint32(int32(math.Round(q))))
		h.Write(buf[:])
	}
	return h.Sum64()
}

func hashString(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}
