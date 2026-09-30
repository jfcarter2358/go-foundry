package nav

// cache.go — building a level's navigation once, and noticing when to again.
//
// Building is the expensive part — every face of every brush clipped against
// every column it covers — and querying is cheap, so the grid is built on first
// use and kept. It has to be thrown away when the level's shape changes, and
// the shape changes in more ways than a hook could reliably catch: the editor
// moves a vertex, a script streams a chunk out, a level load replaces the whole
// thing. So instead of being told, the cache looks: Sync hashes what the grid
// was built from, and a different hash is a different level.
//
// Hashing every brush's verts each frame sounds like a lot and is not — a few
// thousand brushes is well under a millisecond — and it is paid only by a frame
// that has something asking for a path.

import (
	"math"
	"sync"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// MaxSlopeCos is the steepest surface navigation will stand an agent on:
// cos(45°). It is collision.MaxSlopeCos, restated because navigation sits
// below the resolver in the import graph — a test in internal/ai keeps the two
// the same number.
const MaxSlopeCos = float32(0.7071)

type profileKey struct{ radius, height, step int32 }

// cache is the one level's navigation the process holds. Everything in it is
// read and written on the frame loop's thread only; a background build (see
// SyncAsync) works on its own snapshot and hands its result back through
// results, which is the only thing shared with the goroutine.
var cache struct {
	lvl   *world.Level
	sig   uint64 // what hf and grids were built from
	valid bool   // hf and grids describe lvl as it was at sig
	hf    *Heightfield
	grids map[profileKey]*Grid

	// gen numbers the cache's lifetime. Invalidate bumps it, and a background
	// result stamped with an older one is from a level that has since been
	// replaced — possibly in place, at the same *Level — and is thrown away.
	gen uint64
	// profiles is every agent size asked for since the last Invalidate, so a
	// background rebuild produces grids for all of them at once rather than
	// leaving each to be discovered missing a frame later.
	profiles map[profileKey]Params
	// latestSig is the level's signature as SyncAsync last measured it.
	latestSig uint64
	// building is whether a background build for this gen is running.
	building bool
}

// results is where background builds leave what they made. A mutex-guarded
// slot rather than a channel, so a build whose level has gone away never
// blocks on a send nobody will receive.
var results struct {
	sync.Mutex
	done []*buildResult
}

type buildResult struct {
	gen   uint64
	lvl   *world.Level
	sig   uint64
	hf    *Heightfield
	grids map[profileKey]*Grid
}

// pending counts background builds that have not returned, for Wait.
var pending sync.WaitGroup

// Invalidate drops every built grid, so the next request rebuilds from the
// level as it now is. Called when a play session starts or a level loads.
func Invalidate() {
	cache.lvl, cache.valid, cache.hf, cache.grids = nil, false, nil, nil
	cache.profiles, cache.building = nil, false
	cache.gen++
	resetTiles()
	results.Lock()
	results.done = nil
	results.Unlock()
}

// Wait blocks until every background build has finished. The results are not
// installed until the next SyncAsync or Sync — the frame loop's thread is the
// only one that touches the cache. For tests and tools.
func Wait() { pending.Wait() }

// Sync checks the cached grids against the level and drops them if its shape
// has changed since they were built, so the next For rebuilds them on the spot.
// Cheap enough to call once a frame.
//
// The synchronous half: For after Sync always answers for the level as it is
// now. Tests, the editor's overlay and anything else that needs that use it;
// play uses SyncAsync, which would rather serve a stale grid than stall.
func Sync(lvl *world.Level) {
	if lvl == nil {
		Invalidate()
		return
	}
	if cache.lvl != lvl {
		adopt(lvl)
	}
	sig := Signature(lvl)
	cache.latestSig = sig
	install(sig)
	if !cache.valid || cache.sig != sig {
		cache.sig, cache.valid = sig, true
		cache.hf, cache.grids = nil, nil
	}
}

// SyncAsync is Sync for the frame loop in play. A level whose shape has
// changed keeps being served the grids it had while new ones are built on a
// goroutine, and they replace the old ones at a later SyncAsync — so an area
// switching on costs no frame the heightfield rasterisation, which for a big
// area is most of a second.
//
// Only the snapshot (gatherSolids, which copies every solid into world space)
// and the signature run here. Queries still run on this thread only, since a
// Grid's A* scratch is not safe to share, and a finished Grid is never touched
// by the goroutine that built it again.
func SyncAsync(lvl *world.Level) {
	if lvl == nil {
		Invalidate()
		return
	}
	if cache.lvl != lvl {
		adopt(lvl)
	}
	cache.latestSig = Signature(lvl)
	install(0)
	kick(lvl)
}

// adopt points the cache at a different level, dropping what it held for the
// old one.
func adopt(lvl *world.Level) {
	Invalidate()
	cache.lvl = lvl
}

// install takes any finished background builds. A result replaces what is
// served when it is newer than it, and is merged into it when it was built on
// the same heightfield (a profile that was missing). With syncSig non-zero —
// Sync, which needs the answer to be current — only a result built at exactly
// that signature is taken.
func install(syncSig uint64) {
	results.Lock()
	done := results.done
	results.done = nil
	results.Unlock()
	for _, r := range done {
		if r.gen != cache.gen || r.lvl != cache.lvl {
			continue
		}
		cache.building = false
		switch {
		case syncSig != 0 && r.sig != syncSig:
			continue
		case cache.valid && cache.sig == r.sig && cache.hf != nil:
			// Built at the shape already served: keep what is served, and take
			// only the agent sizes it was missing. A grid carries its own
			// heightfield, so the two can sit side by side.
			if cache.grids == nil {
				cache.grids = map[profileKey]*Grid{}
			}
			for k, g := range r.grids {
				if _, ok := cache.grids[k]; !ok {
					cache.grids[k] = g
				}
			}
			continue
		case cache.valid && cache.sig != r.sig && cache.sig == cache.latestSig:
			// Overtaken: For has already built the current shape on the spot.
			continue
		}
		cache.hf, cache.grids, cache.sig, cache.valid = r.hf, r.grids, r.sig, true
	}
}

// kick starts a background build if the level has moved on from what is served
// — or an agent size has been asked for that has no grid yet — and none is
// already running. One build at a time: a level that keeps changing while one
// runs is caught up by the next, started when this one lands.
func kick(lvl *world.Level) {
	if cache.building || len(cache.profiles) == 0 {
		return
	}
	current := cache.valid && cache.sig == cache.latestSig
	var want map[profileKey]Params
	if current {
		for k, p := range cache.profiles {
			if _, ok := cache.grids[k]; !ok {
				if want == nil {
					want = map[profileKey]Params{}
				}
				want[k] = p
			}
		}
		if want == nil {
			return
		}
	} else {
		want = make(map[profileKey]Params, len(cache.profiles))
		for k, p := range cache.profiles {
			want[k] = p
		}
	}

	job := buildResult{gen: cache.gen, lvl: lvl, sig: cache.latestSig}
	var solids []solid
	if current {
		job.hf = cache.hf // immutable once built; buildGrid only reads it
	} else {
		solids = gatherSolids(lvl)
	}
	cache.building = true
	pending.Add(1)
	go func() {
		defer pending.Done()
		r := job
		if r.hf == nil {
			r.hf = buildHeightfield(solids, DefaultCellSize, MaxSlopeCos)
		}
		r.grids = make(map[profileKey]*Grid, len(want))
		for k, p := range want {
			r.grids[k] = buildGrid(r.hf, p)
		}
		results.Lock()
		results.done = append(results.done, &r)
		results.Unlock()
	}()
}

// keyFor quantises a profile and names its cache slot.
func keyFor(p Params) (Params, profileKey) {
	p = Quantize(p)
	return p, profileKey{
		radius: int32(math.Round(float64(p.Radius / quantum))),
		height: int32(math.Round(float64(p.Height / quantum))),
		step:   int32(math.Round(float64(p.StepHeight / quantum))),
	}
}

// For returns the grid for an agent of the given size on lvl, building it if
// need be. A request for a level other than the one last synced syncs first.
//
// It never waits on a background build: with nothing served it builds on the
// spot. After a SyncAsync the grid it returns may describe the level as it was
// before its latest change, exactly as ForReady's does.
func For(lvl *world.Level, p Params) *Grid {
	if lvl == nil {
		return nil
	}
	if cache.lvl != lvl || !cache.valid {
		Sync(lvl)
	}
	p, key := keyFor(p)
	remember(key, p)
	if g, ok := cache.grids[key]; ok {
		return g
	}
	if cache.hf == nil {
		cache.hf = buildHeightfield(gatherSolids(lvl), DefaultCellSize, MaxSlopeCos)
	}
	if cache.grids == nil {
		cache.grids = map[profileKey]*Grid{}
	}
	g := buildGrid(cache.hf, p)
	cache.grids[key] = g
	return g
}

// ForReady is For without the building: the grid served for an agent of this
// size, or nil when none has been built for it yet — in which case one is
// started in the background. fresh reports whether the grid describes the
// level as SyncAsync last measured it, rather than as it was before a change
// whose rebuild has not landed.
//
// nil is not "no navigation": an agent given nil should hold still rather than
// take the straight-line fallback it uses on a level with nothing to navigate,
// or every agent walks at the walls while a build runs.
func ForReady(lvl *world.Level, p Params) (grid *Grid, fresh bool) {
	if lvl == nil {
		return nil, false
	}
	if cache.lvl != lvl {
		SyncAsync(lvl)
	}
	p, key := keyFor(p)
	remember(key, p)
	g, ok := cache.grids[key]
	if !ok {
		kick(lvl)
		return nil, false
	}
	return g, cache.valid && cache.sig == cache.latestSig && !cache.building
}

// remember records an agent size as one the level's grids are wanted for.
func remember(key profileKey, p Params) {
	if cache.profiles == nil {
		cache.profiles = map[profileKey]Params{}
	}
	cache.profiles[key] = p
}

// quantum is the step agent sizes are rounded to, so a level whose guards are
// 0.61 and 0.6 wide shares one grid rather than building two that differ by a
// hundredth of a column.
const quantum = float32(0.05)

// Quantize rounds a profile to the sizes grids are cached at: up, for radius
// and height, since a grid built for a slightly bigger body is still one the
// real body fits.
func Quantize(p Params) Params {
	up := func(v float32) float32 {
		return float32(math.Ceil(float64(v/quantum)-1e-4)) * quantum
	}
	p.Radius = up(p.Radius)
	p.Height = up(p.Height)
	p.StepHeight = float32(math.Round(float64(p.StepHeight/quantum))) * quantum
	return p
}

// navigable reports whether a chunk's solids are part of the navigation: an
// enabled chunk that is standing still. A disabled one is not there; a moving
// one — a velocity or a running move_to — is somewhere else next frame, so
// baking it would both be wrong by then and change the signature every frame,
// throwing away every grid for as long as it moves.
//
// A frame-owned chunk — a ship's hold, a vehicle's deck — counts on the same
// terms, judged by its frame: while the entity carrying it (and whatever that
// rides) is at rest and upright, its floors are baked in world space where the
// frame last published them, so an agent can walk up a parked ship's ramp and
// about its hold. The moment the ship moves they drop out again, and agents
// aboard hold on (internal/ai). A frame that has never been published has no
// placement to bake at yet.
func navigable(lvl *world.Level, ch *world.Chunk) bool {
	if !ch.IsEnabled() || ch.Velocity != (mgl32.Vec3{}) || ch.OriginTween != nil {
		return false
	}
	if !ch.InFrame() {
		return true
	}
	return ch.Xform != (mgl32.Mat4{}) && world.FrameAtRest(lvl, ch.FrameOwner)
}

// Signature hashes everything a heightfield is built from: which world chunks
// are there, where they are placed, and the corners of every static solid in
// them.
//
// It runs every frame, over every corner of every static solid, so it is
// FNV-1a taken a 32-bit word at a time and written out inline. Fed through
// hash.Hash four bytes per call it was most of what the AI cost on a
// three-thousand-brush level with nothing moving; the answer is still a pure
// function of the same data, so an edit anywhere still changes it.
func Signature(lvl *world.Level) uint64 {
	const prime = 1099511628211
	h := uint64(14695981039346656037)
	w := func(v uint32) {
		h ^= uint64(v)
		h *= prime
	}
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if !navigable(lvl, ch) {
			continue
		}
		for i := 0; i < len(ch.ID); i++ {
			h ^= uint64(ch.ID[i])
			h *= prime
		}
		m := ch.ModelMatrix()
		for _, v := range m {
			w(math.Float32bits(v))
		}
		w(uint32(len(ch.Brushes)))
		for bi := range ch.Brushes {
			b := &ch.Brushes[bi]
			if !b.Solid || b.Dynamic {
				w(0xffffffff)
				continue
			}
			w(uint32(len(b.Verts)))
			for _, v := range b.Verts {
				w(math.Float32bits(v.X()))
				w(math.Float32bits(v.Y()))
				w(math.Float32bits(v.Z()))
			}
			w(uint32(len(b.Faces)))
			for fi := range b.Faces {
				w(uint32(len(b.Faces[fi].Indices)))
			}
		}
		// A terrain's generation moves on every edit (world.Terrain.Touch),
		// so it stands in for hashing every height every frame.
		if t := ch.Terrain; t != nil {
			w(uint32(t.Generation))
			w(uint32(t.Generation >> 32))
		}
	}
	return h
}
