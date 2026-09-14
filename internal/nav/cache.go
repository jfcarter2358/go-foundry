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
	"hash/fnv"
	"math"

	"foundry/internal/world"
)

// MaxSlopeCos is the steepest surface navigation will stand an agent on:
// cos(45°). It is collision.MaxSlopeCos, restated because navigation sits
// below the resolver in the import graph — a test in internal/ai keeps the two
// the same number.
const MaxSlopeCos = float32(0.7071)

type profileKey struct{ radius, height, step int32 }

var cache struct {
	lvl   *world.Level
	sig   uint64
	valid bool
	hf    *Heightfield
	grids map[profileKey]*Grid
}

// Invalidate drops every built grid, so the next request rebuilds from the
// level as it now is. Called when a play session starts or a level loads.
func Invalidate() {
	cache.lvl, cache.valid, cache.hf, cache.grids = nil, false, nil, nil
}

// Sync checks the cached grids against the level and drops them if its shape
// has changed since they were built. Cheap enough to call once a frame.
func Sync(lvl *world.Level) {
	if lvl == nil {
		Invalidate()
		return
	}
	sig := Signature(lvl)
	if cache.lvl != lvl || !cache.valid || cache.sig != sig {
		cache.lvl, cache.sig, cache.valid = lvl, sig, true
		cache.hf, cache.grids = nil, nil
	}
}

// For returns the grid for an agent of the given size on lvl, building it if
// need be. A request for a level other than the one last synced syncs first.
func For(lvl *world.Level, p Params) *Grid {
	if lvl == nil {
		return nil
	}
	if cache.lvl != lvl || !cache.valid {
		Sync(lvl)
	}
	p = Quantize(p)
	key := profileKey{
		radius: int32(math.Round(float64(p.Radius / quantum))),
		height: int32(math.Round(float64(p.Height / quantum))),
		step:   int32(math.Round(float64(p.StepHeight / quantum))),
	}
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

// Signature hashes everything a heightfield is built from: which world chunks
// are there, where they are placed, and the corners of every static solid in
// them.
func Signature(lvl *world.Level) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	f32 := func(v float32) {
		b := math.Float32bits(v)
		buf[0], buf[1], buf[2], buf[3] = byte(b), byte(b>>8), byte(b>>16), byte(b>>24)
		_, _ = h.Write(buf[:4])
	}
	u32 := func(v uint32) {
		buf[0], buf[1], buf[2], buf[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
		_, _ = h.Write(buf[:4])
	}
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if !ch.IsEnabled() || ch.InFrame() {
			continue
		}
		_, _ = h.Write([]byte(ch.ID))
		m := ch.ModelMatrix()
		for _, v := range m {
			f32(v)
		}
		u32(uint32(len(ch.Brushes)))
		for bi := range ch.Brushes {
			b := &ch.Brushes[bi]
			if !b.Solid || b.Dynamic {
				u32(0xffffffff)
				continue
			}
			u32(uint32(len(b.Verts)))
			for _, v := range b.Verts {
				f32(v.X())
				f32(v.Y())
				f32(v.Z())
			}
			u32(uint32(len(b.Faces)))
			for fi := range b.Faces {
				u32(uint32(len(b.Faces[fi].Indices)))
			}
		}
	}
	return h.Sum64()
}
