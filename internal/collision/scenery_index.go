package collision

// scenery_index.go — finding the props near a body without visiting them all.
//
// Scenery used to be a linear scan: every body against every solid prop,
// every tick, and every ray against every prop's box. A field of a thousand
// cooked rocks made that the frame. So a level's solid placements now sit
// under a bounding-volume hierarchy of their world boxes, the PLACEMENT
// BROADPHASE, and every query — a body's resolve, a ray, a particle's probe —
// asks it for the few placements its own box or segment reaches.
//
// # One entry a placement
//
// Each entry holds what every query would otherwise rebuild per call: the
// placement's resolved collision mode (world.Scenery.CollisionMode), its
// matrix and inverse, its world box, the world box of each of its hulls, and
// for the box mode the immovable proxy entity scenery_collision.go resolves
// against. Scenery is level-scoped and placed in world space, so the matrix
// is the placement's own; a parent transform, should one ever carry a prop,
// composes into it here and nowhere else.
//
// # Knowing when it is stale
//
// Scenery is not addressable by scripts (world/scenery.go), so between
// frames only the editor or a level load changes it. The index is keyed on
// the level and the scenery slice itself (a load, or an append that moved
// it, rebuilds at once), and checked against a per-placement fingerprint —
// transform, bounds, solidity, mode, cooked mesh — once per physics tick
// (Epoch): the first query of a tick pays a compare per placement, the rest
// trust it. A level with only a few props is checked on every query, which
// costs less than a microsecond and keeps an edit and a query in the same
// tick honest; a big one that is edited between two queries of one tick
// wants InvalidateScenery.
//
// # Resident collision
//
// A mesh-mode placement's triangles are decoded (meshcollide.Resident) only
// while bodies are near it, and dropped after residentTTL ticks with no body
// near and no query, so the decoded form of a far field of rocks is never
// held. Hulls need no decoding; their points are read where the cooked file
// holds them.
//
// Since Phase 3 (docs/nanite/PHASE_3.md) a loaded mesh's triangles are not in
// memory at all until wanted: the cooked file keeps them apart from the
// metadata (meshcollide's paged.go), so becoming resident means reading them
// from disk. That is done AHEAD of the first query, by proximity: every tick
// (prefetchNear, from EntityUpdate) each world-space body's box, grown by
// prefetchReach and by where its speed takes it in prefetchAhead seconds, is
// run through the index, and every mesh-mode placement it reaches that is
// not resident starts loading on a goroutine — reading and decoding off the
// physics thread — and is kept resident while a body stays that near. A
// query that reaches a mesh still not resident (a ray from afar, a body that
// teleported) waits for it, or reads it there and then; those are counted as
// SyncLoads, so a level that stalls on them shows it in F3.

import (
	"log"
	"sort"
	"sync"
	"time"

	"foundry/internal/meshcollide"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// checkEveryQueryBelow is the placement count under which every query, not
// just a tick's first, checks the fingerprint.
const checkEveryQueryBelow = 64

// sceneryEntry is one solid placement in the index: a scenery's, or a
// placed tree's trunk (world.Tree.TrunkBox), which is a box-mode entry.
type sceneryEntry struct {
	idx  int // into lvl.Scenery; -1 for a tree
	id   string
	mode meshcollide.Mode
	box  aabb // world space, padded
	m    mgl32.Mat4
	inv  mgl32.Mat4
	data *meshcollide.Data
	// hullBoxes are each hull's world box, for the hulls mode.
	hullBoxes []aabb
	// proxy is the box mode's immovable body (sceneryProxy).
	proxy world.Entity
}

// sceneryKey is what a placement's entry depends on.
type sceneryKey struct {
	pos, rot, scale mgl32.Vec3
	lo, hi          mgl32.Vec3
	solid           bool
	mode            meshcollide.Mode
	data            *meshcollide.Data
	// off: its chunk is switched off (world.Scenery.ChunkOff).
	off bool
}

func keyOf(s *world.Scenery) sceneryKey {
	return sceneryKey{pos: s.Position, rot: s.Rotation, scale: s.Scale, lo: s.LocalMin, hi: s.LocalMax,
		solid: s.Solid, mode: s.Collision, data: s.CollisionData(), off: s.ChunkOff}
}

// treeKey is what a placed tree's entry depends on.
type treeKey struct {
	lo, hi mgl32.Vec3
	ok     bool
	off    bool // its chunk is switched off (world.Tree.ChunkOff)
}

func treeKeyOf(t *world.Tree) treeKey {
	lo, hi, ok := t.TrunkBox()
	return treeKey{lo, hi, ok, t.ChunkOff}
}

// sceneryIndex is a level's solid placements under a tree of their boxes.
type sceneryIndex struct {
	lvl   *world.Level
	base  *world.Scenery
	n     int
	keys  []sceneryKey
	trees []treeKey
	tbase *world.Tree
	epoch uint64

	entries []sceneryEntry
	nodes   []placementNode
	// counts by mode, for the overlay.
	byMode [5]int
}

// placementNode is one node of the broadphase tree: a box and either two
// children (left is the next node, right is Right) or a run of entries.
type placementNode struct {
	box          aabb
	right        int32
	first, count int32
}

var sceneryState struct {
	sync.Mutex
	idx   *sceneryIndex
	epoch uint64
}

// Epoch starts a physics tick for the placement index: the next query
// checks the scenery against its fingerprint once more. EntityUpdate and
// MovePlayer call it; so does anything else that steps the world.
func Epoch() {
	sceneryState.Lock()
	sceneryState.epoch++
	sceneryState.Unlock()
}

// InvalidateScenery drops the placement index, so the next query rebuilds
// it — for an edit between two queries of one tick.
func InvalidateScenery() {
	sceneryState.Lock()
	sceneryState.idx = nil
	sceneryState.Unlock()
}

// frozenScenery is the index a parallel phase reads (freezeScenery): checked
// and published once before the workers start, so a query from any of them is
// one pointer compare instead of a lock — and, for a level of a few props, a
// fingerprint pass — every probe. Scenery is not addressable by scripts and
// nothing edits it inside a physics pass, so the index the phase starts with
// is the one every query in it would have found.
var frozenScenery *sceneryIndex

// freezeScenery brings lvl's index up to date and publishes it for the
// parallel phase; thawScenery ends that. Both on the frame loop's goroutine,
// before the workers start and after they have finished.
func freezeScenery(lvl *world.Level) { frozenScenery = sceneryIndexFor(lvl) }
func thawScenery()                    { frozenScenery = nil }

// sceneryIndexFor is lvl's index, rebuilt if it is stale. Never nil for a
// level with scenery; nil otherwise.
func sceneryIndexFor(lvl *world.Level) *sceneryIndex {
	if lvl == nil || len(lvl.Scenery)+len(lvl.Trees) == 0 {
		return nil
	}
	if ix := frozenScenery; ix != nil && ix.lvl == lvl {
		return ix
	}
	sceneryState.Lock()
	defer sceneryState.Unlock()
	ix := sceneryState.idx
	if ix != nil && ix.lvl == lvl && ix.base == firstScenery(lvl) && ix.n == len(lvl.Scenery) &&
		ix.tbase == firstTree(lvl) && len(ix.trees) == len(lvl.Trees) {
		if ix.epoch == sceneryState.epoch && ix.n+len(ix.trees) > checkEveryQueryBelow {
			return ix
		}
		same := true
		for i := range lvl.Scenery {
			if keyOf(&lvl.Scenery[i]) != ix.keys[i] {
				same = false
				break
			}
		}
		for i := 0; same && i < len(lvl.Trees); i++ {
			if treeKeyOf(&lvl.Trees[i]) != ix.trees[i] {
				same = false
			}
		}
		if same {
			ix.epoch = sceneryState.epoch
			return ix
		}
	}
	start := time.Now()
	ix = buildSceneryIndex(lvl)
	ix.epoch = sceneryState.epoch
	sceneryState.idx = ix
	statsMu.Lock()
	sceneryStats.rebuilds++
	sceneryStats.buildTime += time.Since(start)
	statsMu.Unlock()
	return ix
}

// firstScenery and firstTree are where the level's slices start (nil when
// empty): an append that moved one rebuilds the index.
func firstScenery(lvl *world.Level) *world.Scenery {
	if len(lvl.Scenery) == 0 {
		return nil
	}
	return &lvl.Scenery[0]
}

func firstTree(lvl *world.Level) *world.Tree {
	if len(lvl.Trees) == 0 {
		return nil
	}
	return &lvl.Trees[0]
}

func buildSceneryIndex(lvl *world.Level) *sceneryIndex {
	ix := &sceneryIndex{lvl: lvl, base: firstScenery(lvl), n: len(lvl.Scenery), keys: make([]sceneryKey, len(lvl.Scenery)),
		tbase: firstTree(lvl), trees: make([]treeKey, len(lvl.Trees))}
	for i := range lvl.Scenery {
		s := &lvl.Scenery[i]
		ix.keys[i] = keyOf(s)
		if !s.InWorld() {
			continue // its chunk is off: walked through, like the chunk
		}
		mode := s.CollisionMode()
		ix.byMode[mode]++
		if mode == meshcollide.ModeNone {
			continue
		}
		e := sceneryEntry{idx: i, id: s.ID, mode: mode, m: s.ModelMatrix(), data: s.CollisionData()}
		e.inv = e.m.Inv()
		switch mode {
		case meshcollide.ModeBox:
			lo, hi, ok := s.CollisionAABB()
			if !ok {
				continue
			}
			e.box = aabb{lo: lo, hi: hi}.padded()
		default:
			// The cooked collision's own box, which a sheet flat on one axis
			// still has (meshcollide widens a flat axis a hair).
			e.box = aabb{lo: e.data.Lo, hi: e.data.Hi}.transformed(e.m).padded()
		}
		switch mode {
		case meshcollide.ModeBox:
			e.proxy, _ = sceneryProxy(s)
		case meshcollide.ModeHulls:
			for hi := range e.data.Hulls {
				h := &e.data.Hulls[hi]
				e.hullBoxes = append(e.hullBoxes, aabb{lo: h.Lo, hi: h.Hi}.transformed(e.m).padded())
			}
		}
		ix.entries = append(ix.entries, e)
	}
	// A placed tree is solid as the box round its trunk: its crown is not
	// something a body is stopped by.
	for i := range lvl.Trees {
		t := &lvl.Trees[i]
		ix.trees[i] = treeKeyOf(t)
		if !t.InWorld() {
			continue // its chunk is off: walked through, like the chunk
		}
		lo, hi, ok := t.TrunkBox()
		if !ok {
			ix.byMode[meshcollide.ModeNone]++
			continue
		}
		ix.byMode[meshcollide.ModeBox]++
		c, h := lo.Add(hi).Mul(0.5), hi.Sub(lo).Mul(0.5)
		e := sceneryEntry{idx: -1, id: t.ID, mode: meshcollide.ModeBox, m: mgl32.Translate3D(c[0], c[1], c[2]),
			box: aabb{lo: lo, hi: hi}.padded()}
		e.inv = e.m.Inv()
		e.proxy = world.Entity{ID: t.ID, Position: c, Scale: mgl32.Vec3{1, 1, 1}, BoundingKind: world.BoundingAABB,
			BoundHalfX: h[0], BoundHalfY: h[1], BoundHalfZ: h[2]}
		ix.entries = append(ix.entries, e)
	}
	ix.buildTree()
	return ix
}

// buildTree is a median-split tree over the entries' boxes.
func (ix *sceneryIndex) buildTree() {
	ix.nodes = ix.nodes[:0]
	if len(ix.entries) == 0 {
		return
	}
	var build func(first, count int) int
	build = func(first, count int) int {
		box := emptyBox()
		for i := first; i < first+count; i++ {
			box.addBox(ix.entries[i].box)
		}
		at := len(ix.nodes)
		ix.nodes = append(ix.nodes, placementNode{box: box})
		if count <= 4 {
			ix.nodes[at].first, ix.nodes[at].count = int32(first), int32(count)
			return at
		}
		ext := box.hi.Sub(box.lo)
		axis := 0
		if ext[1] > ext[axis] {
			axis = 1
		}
		if ext[2] > ext[axis] {
			axis = 2
		}
		part := ix.entries[first : first+count]
		sort.SliceStable(part, func(a, b int) bool {
			return part[a].box.lo[axis]+part[a].box.hi[axis] < part[b].box.lo[axis]+part[b].box.hi[axis]
		})
		half := count / 2
		build(first, half)
		r := build(first+half, count-half)
		ix.nodes[at].right = int32(r)
		return at
	}
	build(0, len(ix.entries))
}

// overlapping calls fn with each entry whose box touches [lo, hi], until fn
// returns false.
func (ix *sceneryIndex) overlapping(lo, hi mgl32.Vec3, fn func(e *sceneryEntry) bool) {
	if ix == nil || len(ix.nodes) == 0 {
		return
	}
	var stack [64]int32
	stack[0] = 0
	sp := 1
	for sp > 0 {
		sp--
		ni := stack[sp]
		n := &ix.nodes[ni]
		if !boxTouches(n.box, lo, hi) {
			continue
		}
		if n.count > 0 {
			for i := n.first; i < n.first+n.count; i++ {
				e := &ix.entries[i]
				if boxTouches(e.box, lo, hi) && !fn(e) {
					return
				}
			}
			continue
		}
		if sp+2 <= len(stack) {
			stack[sp], stack[sp+1] = n.right, ni+1
			sp += 2
		}
	}
}

// along calls fn with each entry whose box the segment origin + t·dir,
// t in [0, tMax], passes through, until fn returns false. fn may lower
// *tMax to cull what lies beyond a hit.
func (ix *sceneryIndex) along(origin, dir mgl32.Vec3, tMax *float32, fn func(e *sceneryEntry) bool) {
	if ix == nil || len(ix.nodes) == 0 {
		return
	}
	r := newRayDir(origin, dir)
	var stack [64]int32
	stack[0] = 0
	sp := 1
	for sp > 0 {
		sp--
		ni := stack[sp]
		n := &ix.nodes[ni]
		if _, ok := r.enterBox(n.box, *tMax); !ok {
			continue
		}
		if n.count > 0 {
			for i := n.first; i < n.first+n.count; i++ {
				e := &ix.entries[i]
				if _, ok := r.enterBox(e.box, *tMax); ok && !fn(e) {
					return
				}
			}
			continue
		}
		if sp+2 <= len(stack) {
			stack[sp], stack[sp+1] = n.right, ni+1
			sp += 2
		}
	}
}

func boxTouches(b aabb, lo, hi mgl32.Vec3) bool {
	return b.lo[0] <= hi[0] && b.hi[0] >= lo[0] && b.lo[1] <= hi[1] && b.hi[1] >= lo[1] && b.lo[2] <= hi[2] && b.hi[2] >= lo[2]
}

// ---------- resident collision ----------

// residentTTL is how many ticks a decoded mesh stays with no query reaching
// it: ten seconds at the shipped rate, so a body walking past a rock does not
// decode it twice.
const residentTTL = 600

// prefetchReach is how far, in metres, beyond a body's own box a mesh-mode
// placement is made resident; prefetchAhead how many seconds of the body's
// velocity are added to it. A walker covers a few metres a second, so a
// mesh is read well before anything could touch it.
const (
	prefetchReach = 16
	prefetchAhead = 1.0
)

type residentEntry struct {
	r    *meshcollide.Resident
	last uint64
}

// pendingLoad is a mesh being read on a goroutine.
type pendingLoad struct {
	done chan struct{}
	r    *meshcollide.Resident
	err  error
}

var residents struct {
	sync.Mutex
	m       map[*meshcollide.Data]*residentEntry
	loading map[*meshcollide.Data]*pendingLoad
	tick    uint64
}

// decodeResident reads d's mesh part if it was paged out, and decodes it.
func decodeResident(d *meshcollide.Data) (*meshcollide.Resident, error) {
	full, err := d.WithMesh()
	if err != nil {
		return nil, err
	}
	return meshcollide.NewResident(full), nil
}

// residentFor is d decoded: resident already (made so by prefetchNear, as a
// rule), or waited for, or read now.
func residentFor(d *meshcollide.Data) *meshcollide.Resident {
	residents.Lock()
	if residents.m == nil {
		residents.m = map[*meshcollide.Data]*residentEntry{}
	}
	if e := residents.m[d]; e != nil {
		e.last = residents.tick
		residents.Unlock()
		return e.r
	}
	pl := residents.loading[d]
	residents.Unlock()

	// Not resident: the query has to wait for it.
	var r *meshcollide.Resident
	var err error
	if pl != nil {
		<-pl.done
		r, err = pl.r, pl.err
	} else {
		r, err = decodeResident(d)
	}
	statsMu.Lock()
	sceneryStats.decodes++
	if !d.MeshResident() {
		sceneryStats.syncLoads++
	}
	statsMu.Unlock()
	if err != nil {
		// A mesh whose triangles cannot be read collides as nothing rather
		// than taking the game down; said once per load.
		logResidentError(d, err)
		r = meshcollide.NewResident(&meshcollide.Data{})
	}
	residents.Lock()
	if e := residents.m[d]; e != nil {
		r = e.r
	} else {
		residents.m[d] = &residentEntry{r: r, last: residents.tick}
	}
	residents.Unlock()
	return r
}

// residentTick advances the residency clock, takes in what finished loading
// and drops what has gone unused. EntityUpdate calls it once a frame.
func residentTick() {
	residents.Lock()
	defer residents.Unlock()
	residents.tick++
	for d, pl := range residents.loading {
		select {
		case <-pl.done:
			delete(residents.loading, d)
			if pl.err != nil {
				logResidentError(d, pl.err)
				continue
			}
			if residents.m[d] == nil {
				residents.m[d] = &residentEntry{r: pl.r, last: residents.tick}
			}
		default:
		}
	}
	for d, e := range residents.m {
		if residents.tick-e.last > residentTTL {
			delete(residents.m, d)
			statsMu.Lock()
			sceneryStats.evictions++
			statsMu.Unlock()
		}
	}
}

// prefetchNear makes every mesh-mode placement near a world-space body
// resident ahead of its first query: loading it on a goroutine if it is not,
// and keeping it so while the body is near (see the file comment).
func prefetchNear(lvl *world.Level, player *world.Entity) {
	prefetchBodies(lvl, player, true)
}

// prefetchBodies is prefetchNear for player and, with all, every entity.
func prefetchBodies(lvl *world.Level, player *world.Entity, all bool) {
	ix := sceneryIndexFor(lvl)
	if ix == nil || ix.byMode[meshcollide.ModeMesh] == 0 {
		return
	}
	near := func(e *world.Entity) {
		if e == nil || e.Frame != "" {
			return
		}
		half := e.BoundHalfExtents()
		v := e.Velocity.Mul(prefetchAhead)
		reach := mgl32.Vec3{prefetchReach, prefetchReach, prefetchReach}.Add(half)
		lo := e.Position.Sub(reach).Add(mgl32.Vec3{min(v[0], 0), min(v[1], 0), min(v[2], 0)})
		hi := e.Position.Add(reach).Add(mgl32.Vec3{max(v[0], 0), max(v[1], 0), max(v[2], 0)})
		ix.overlapping(lo, hi, func(se *sceneryEntry) bool {
			if se.mode == meshcollide.ModeMesh {
				keepResident(se.data)
			}
			return true
		})
	}
	near(player)
	for i := 0; all && i < len(lvl.Entities); i++ {
		if e := &lvl.Entities[i]; e.CollidesWithWorld() {
			near(e)
		}
	}
}

// keepResident marks d wanted this tick, starting its load if it is neither
// resident nor loading.
func keepResident(d *meshcollide.Data) {
	residents.Lock()
	defer residents.Unlock()
	if residents.m == nil {
		residents.m = map[*meshcollide.Data]*residentEntry{}
	}
	if e := residents.m[d]; e != nil {
		e.last = residents.tick
		return
	}
	if residents.loading == nil {
		residents.loading = map[*meshcollide.Data]*pendingLoad{}
	}
	if residents.loading[d] != nil {
		return
	}
	pl := &pendingLoad{done: make(chan struct{})}
	residents.loading[d] = pl
	statsMu.Lock()
	sceneryStats.prefetches++
	statsMu.Unlock()
	go func() {
		pl.r, pl.err = decodeResident(d)
		close(pl.done)
	}()
}

var residentErrors sync.Map

func logResidentError(d *meshcollide.Data, err error) {
	if _, seen := residentErrors.LoadOrStore(d, true); !seen {
		log.Printf("collision: a cooked mesh's collision could not be read: %v — it collides as nothing", err)
	}
}

// WaitForCollisionLoads blocks until every collision mesh being read on a
// goroutine has arrived, and takes them in: for tests and tools that step
// the world headlessly and want what a real frame rate would have had.
func WaitForCollisionLoads() {
	residents.Lock()
	var waits []*pendingLoad
	for _, pl := range residents.loading {
		waits = append(waits, pl)
	}
	residents.Unlock()
	for _, pl := range waits {
		<-pl.done
	}
	residents.Lock()
	residents.tick--
	residents.Unlock()
	residentTick()
}

// ---------- the overlay's numbers ----------

// SceneryStats is what the placement collision did, for F3 and the bench.
type SceneryStats struct {
	// Placements by resolved mode, in the current index.
	Box, Hulls, Mesh, None int
	// Resident meshes and the bytes their decoded form holds.
	Resident      int
	ResidentBytes int
	// Since the last TakeSceneryStats: index rebuilds, decodes and
	// evictions, and the CPU time spent in each kind of query. Prefetches
	// are meshes a body's nearness started reading ahead of any query;
	// SyncLoads, queries that had to wait for a paged-out mesh's triangles
	// (each one a stall of the physics tick: Phase 3).
	Rebuilds, Decodes, Evictions int
	Prefetches, SyncLoads        int
	// Loading is how many are being read right now.
	Loading                int
	BuildTime, ResolveTime time.Duration
	RayTime, ParticleTime  time.Duration
}

// statsMu guards sceneryStats. Taken after residents' or sceneryState's
// lock, never before either.
var statsMu sync.Mutex

var sceneryStats struct {
	rebuilds, decodes, evictions int
	prefetches, syncLoads        int
	buildTime                    time.Duration
	resolveTime, rayTime         time.Duration
	particleTime                 time.Duration
}

// ProfileScenery turns on the timing of scenery queries. The F3 overlay
// and the bench turn it on with the profiler; off, a query reads no clock.
var ProfileScenery bool

// TakeSceneryStats returns the stats since the last call and starts them
// over.
func TakeSceneryStats() SceneryStats {
	sceneryState.Lock()
	var out SceneryStats
	if ix := sceneryState.idx; ix != nil {
		out.Box = ix.byMode[meshcollide.ModeBox]
		out.Hulls = ix.byMode[meshcollide.ModeHulls]
		out.Mesh = ix.byMode[meshcollide.ModeMesh]
		out.None = ix.byMode[meshcollide.ModeNone]
	}
	sceneryState.Unlock()
	residents.Lock()
	out.Resident = len(residents.m)
	out.Loading = len(residents.loading)
	for _, e := range residents.m {
		out.ResidentBytes += e.r.Bytes()
	}
	residents.Unlock()
	statsMu.Lock()
	out.Rebuilds, out.Decodes, out.Evictions = sceneryStats.rebuilds, sceneryStats.decodes, sceneryStats.evictions
	out.Prefetches, out.SyncLoads = sceneryStats.prefetches, sceneryStats.syncLoads
	sceneryStats.prefetches, sceneryStats.syncLoads = 0, 0
	out.BuildTime, out.ResolveTime = sceneryStats.buildTime, sceneryStats.resolveTime
	out.RayTime, out.ParticleTime = sceneryStats.rayTime, sceneryStats.particleTime
	sceneryStats.rebuilds, sceneryStats.decodes, sceneryStats.evictions = 0, 0, 0
	sceneryStats.buildTime, sceneryStats.resolveTime, sceneryStats.rayTime, sceneryStats.particleTime = 0, 0, 0, 0
	statsMu.Unlock()
	return out
}

// profStart and profStop time a query when ProfileScenery is on.
func profStart() time.Time {
	if !ProfileScenery {
		return time.Time{}
	}
	return time.Now()
}

func profStop(start time.Time, into *time.Duration) {
	if !start.IsZero() {
		statsMu.Lock()
		*into += time.Since(start)
		statsMu.Unlock()
	}
}
