package world

// soft_ground.go — ground that gives underfoot (docs/trails/SPEC.md).
//
// A generated terrain's splat material marks its soft layers — sand, snow —
// with each layer's `_soft` param (material_kinds_terrain.go). This is the
// CPU's half of that: what kind of ground, and how much of it, lies under a
// point, read from the very weights texture the shader mixes the layers by.
// The walkers' steps ask it (sim/footsteps.go) for three things the picture
// cannot give them: the drag on their stride, the sound and puff of a step,
// and whether to press a footprint into the trail map at all (the trail map
// itself is render's, ground_trails.go; its stamps queue here).
//
// THE TILE UNDER A POINT. A terrain is many scenery tiles, each wearing a
// splat material whose weights cover the whole terrain's rectangle (a region
// terrain's per-tile materials each their own), and a cave's floor and rock
// wear copies of it over the same ground. So the tile is best named by what
// is actually under the feet: SoftGroundOn takes the scenery a downward ray
// hit (the sim casts it). SoftGroundAt, with no ray, takes the smallest tile
// whose box holds the point — right on open ground, ambiguous where tiles
// stack (a cave under the terrain). Either way the material's weights are
// bilinearly read there, as the shader's linear filter reads them. The height blend that
// picks which layer SHOWS at a pixel is left out: a step's drag and sound
// want the share of sand around the foot, not which grain is on top.
//
// Only procgen terrain: brush ground and block terrain are going away (the
// user's direction, 2026-10-15) and never carry a splat.

import (
	"image"
	"image/draw"
	_ "image/png"
	"math"
	"os"
	"sync"

	"foundry/internal/gfx"
	"foundry/internal/material"

	"github.com/go-gl/mathgl/mgl32"
)

// SoftGroundKind is the engine's profile of one soft-ground kind.
type SoftGroundKind struct {
	// Depth is how deep a footprint presses, in metres (a layer's
	// `_soft_depth` overrides it), and Fill the share of a footprint that
	// fills back in quickly rather than lasting the session: material.
	// SoftKinds' own, which the shader reads too.
	Depth, Fill float32
	// Drag is the share of a walker's stride the ground takes from it.
	Drag float32
	// Sink is how far the eye drops standing in it, metres: a boot sunk in.
	Sink float32
	// Sound is a step's sound (resources/sounds), Effect its puff
	// (resources/particles, a .fpar); either empty or missing is none.
	Sound, Effect string
	// Color is the puff's colour until the layer's own texture has been
	// averaged (softColors).
	Color mgl32.Vec3
}

// SoftGroundKinds are the profiles by kind name (material.SoftSand, …).
var SoftGroundKinds = map[string]SoftGroundKind{
	material.SoftSand: {Depth: material.SoftKinds[material.SoftSand].Depth, Fill: material.SoftKinds[material.SoftSand].Fill,
		Drag: 0.12, Sink: 0.03, Sound: material.SoftStepAsset(material.SoftSand), Effect: material.SoftStepAsset(material.SoftSand), Color: mgl32.Vec3{0.72, 0.6, 0.46}},
	material.SoftSnow: {Depth: material.SoftKinds[material.SoftSnow].Depth, Fill: material.SoftKinds[material.SoftSnow].Fill,
		Drag: 0.25, Sink: 0.07, Sound: material.SoftStepAsset(material.SoftSnow), Effect: material.SoftStepAsset(material.SoftSnow), Color: mgl32.Vec3{0.9, 0.92, 0.95}},
}

// SoftGround is the ground under a point: the soft layers' share of it and
// their profile, weighted by that share.
type SoftGround struct {
	// Share is how much of the ground is soft, 0..1.
	Share float32
	// Kind is the soft kind with the most weight ("" none).
	Kind string
	// Depth, Drag and Sink are the soft layers' own, weighted by share (so
	// already scaled down where the ground is half rock); Fill is the
	// soft layers' average (not scaled by share: it is a ratio).
	Depth, Fill, Drag, Sink float32
	// Color is the soft layers' colour (each texture's average, tinted),
	// weighted by share: what a puff of it is tinted.
	Color mgl32.Vec3
}

// Soft reports whether there is enough soft ground here to step into.
func (g SoftGround) Soft() bool { return g.Share >= 0.05 && g.Kind != "" }

// Profile is the dominant kind's profile.
func (g SoftGround) Profile() SoftGroundKind { return SoftGroundKinds[g.Kind] }

// softTile is one scenery tile wearing a splat with a soft layer.
type softTile struct {
	id     string
	lo, hi mgl32.Vec3
	area   float32
	fx     *material.SurfaceFx
}

// softIndex is the level's soft tiles, rebuilt when the scenery list or
// the material library changes.
var softIndex struct {
	sync.Mutex
	lvl   *Level
	n     int
	gen   int
	tiles []softTile
	// byID is the tiles by scenery ID.
	byID map[string]int
	// maps are the decoded weights textures by reference.
	maps map[string]*softMap
	// colors are the soft layers' textures' average colours, filled in by
	// a goroutine the rebuild starts (a scan's first decode is a hitch the
	// first step must not take); a texture not there yet reads as its
	// kind's Color.
	colors map[string]mgl32.Vec3
	// loading is the weights textures a goroutine is decoding
	// (SoftMapPrefetch).
	loading map[string]chan struct{}
}

// SoftMapPrefetch decodes a level's soft weights textures on a goroutine when
// the tile index is rebuilt, rather than on the main thread at a body's first
// step onto each (docs/adventure/PHASE_F.md, Phase F2: the adventure level's
// ~90 ms frame at the ruins' first footstep was the decode of a weights map).
// The index is built at the first body's first step, so by the time one
// reaches another area its maps are long decoded; a step that does come
// first waits for the decode, as it would have done it.
// FOUNDRY_SOFT_MAP_PREFETCH=0: each map decoded on first use, as before.
var SoftMapPrefetch = gfx.EnvFlag("FOUNDRY_SOFT_MAP_PREFETCH", true)

// softMap is a weights texture's texels, RGBA as stored.
type softMap struct {
	w, h int
	pix  []uint8
	ok   bool
}

// SoftGroundAt is the ground under (x, z) for a body whose feet are at y.
// Nothing soft there (no tile, a firm layer, the scenery not loaded) is the
// zero SoftGround.
func SoftGroundAt(lvl *Level, x, y, z float32) SoftGround {
	if lvl == nil {
		return SoftGround{}
	}
	softIndex.Lock()
	defer softIndex.Unlock()
	softRefresh(lvl)
	var best *softTile
	for i := range softIndex.tiles {
		t := &softIndex.tiles[i]
		if x < t.lo.X() || x > t.hi.X() || z < t.lo.Z() || z > t.hi.Z() {
			continue
		}
		// A body standing on the tile has its feet within the tile's height
		// span, give or take a step; one in a cave under it or flying over
		// it is not on it.
		if y < t.lo.Y()-1.5 || y > t.hi.Y()+1.5 {
			continue
		}
		if best == nil || t.area < best.area {
			best = t
		}
	}
	if best == nil {
		return SoftGround{}
	}
	return softSample(best.fx, x, z)
}

// SoftGroundOn is the ground at (x, z) of the scenery placement sceneryID —
// the one a ray down from a body's feet hit. Firm (the zero SoftGround) when
// that placement wears no soft splat.
func SoftGroundOn(lvl *Level, sceneryID string, x, z float32) SoftGround {
	if lvl == nil || sceneryID == "" {
		return SoftGround{}
	}
	softIndex.Lock()
	defer softIndex.Unlock()
	softRefresh(lvl)
	i, ok := softIndex.byID[sceneryID]
	if !ok {
		return SoftGround{}
	}
	return softSample(softIndex.tiles[i].fx, x, z)
}

// WarmSoftGround builds lvl's soft tile index now — and so, under
// SoftMapPrefetch, starts decoding its weights maps — rather than at a body's
// first step onto soft ground.
func WarmSoftGround(lvl *Level) {
	if lvl == nil || !SoftMapPrefetch {
		return
	}
	softIndex.Lock()
	defer softIndex.Unlock()
	softRefresh(lvl)
}

// softRefresh rebuilds the tile index when it is stale.
func softRefresh(lvl *Level) {
	gen := material.Generation()
	if softIndex.lvl == lvl && softIndex.n == len(lvl.Scenery) && softIndex.gen == gen {
		return
	}
	if softIndex.gen != gen || softIndex.maps == nil {
		softIndex.maps = map[string]*softMap{}
	}
	softIndex.lvl, softIndex.n, softIndex.gen = lvl, len(lvl.Scenery), gen
	softIndex.tiles = softIndex.tiles[:0]
	softIndex.byID = map[string]int{}
	if softIndex.colors == nil {
		softIndex.colors = map[string]mgl32.Vec3{}
	}
	resolved := map[string]*material.SurfaceFx{}
	for i := range lvl.Scenery {
		s := &lvl.Scenery[i]
		if s.Material == "" {
			continue
		}
		fx, seen := resolved[s.Material]
		if !seen {
			if m, ok := material.ResolveSurface(s.Material); ok && m.Fx.HasSoft() {
				f := m.Fx
				fx = &f
			}
			resolved[s.Material] = fx
		}
		if fx == nil {
			continue
		}
		lo, hi, ok := s.WorldAABB()
		if !ok {
			continue
		}
		softIndex.byID[s.ID] = len(softIndex.tiles)
		softIndex.tiles = append(softIndex.tiles, softTile{id: s.ID, lo: lo, hi: hi,
			area: (hi.X() - lo.X()) * (hi.Z() - lo.Z()), fx: fx})
	}
	var want []string
	var maps []string
	for _, fx := range resolved {
		if fx == nil {
			continue
		}
		if SoftMapPrefetch {
			for _, ref := range []string{fx.SplatWeights, fx.SplatWeights2} {
				if _, have := softIndex.maps[ref]; ref != "" && !have && softIndex.loading[ref] == nil {
					maps = append(maps, ref)
				}
			}
		}
		for _, l := range fx.SplatLayers {
			if _, have := softIndex.colors[l.Texture]; l.Soft != "" && l.Texture != "" && !have {
				want = append(want, l.Texture)
			}
		}
	}
	if len(maps) > 0 {
		if softIndex.loading == nil {
			softIndex.loading = map[string]chan struct{}{}
		}
		done := make(chan struct{})
		for _, ref := range maps {
			softIndex.loading[ref] = done
		}
		gen := softIndex.gen
		go func() {
			defer close(done)
			for _, ref := range maps {
				m := loadSoftMap(ref)
				softIndex.Lock()
				delete(softIndex.loading, ref)
				if softIndex.gen == gen {
					softIndex.maps[ref] = m
				}
				softIndex.Unlock()
			}
		}()
	}
	if len(want) > 0 {
		go func() {
			for _, tex := range want {
				c, ok := gfx.TextureMeanColor(tex)
				if !ok {
					continue
				}
				softIndex.Lock()
				softIndex.colors[tex] = c
				softIndex.Unlock()
			}
		}()
	}
}

// softSample is the soft ground of splat fx at (x, z).
func softSample(fx *material.SurfaceFx, x, z float32) SoftGround {
	sx, sz := fx.SplatMax.X()-fx.SplatMin.X(), fx.SplatMax.Y()-fx.SplatMin.Y()
	if sx <= 0 || sz <= 0 {
		return SoftGround{}
	}
	u, v := (x-fx.SplatMin.X())/sx, (z-fx.SplatMin.Y())/sz
	var w [material.SplatLayersMax]float32
	a := softWeights(fx.SplatWeights, u, v)
	copy(w[:4], a[:])
	if fx.SplatEight {
		b := softWeights(fx.SplatWeights2, u, v)
		copy(w[4:], b[:])
	}
	var g SoftGround
	var kinds = map[string]float32{}
	total := float32(0)
	for i := range w {
		total += w[i]
	}
	if total <= 1e-4 {
		return g
	}
	for i, l := range fx.SplatLayers {
		if l.Soft == "" || l.Texture == "" || w[i] <= 0 {
			continue
		}
		k, ok := SoftGroundKinds[l.Soft]
		if !ok {
			continue
		}
		share := w[i] / total
		depth := k.Depth
		if l.SoftDepth > 0 {
			depth = l.SoftDepth
		}
		g.Share += share
		g.Depth += share * depth
		g.Fill += share * k.Fill
		g.Drag += share * k.Drag
		g.Sink += share * k.Sink * depth / k.Depth
		col, ok := softIndex.colors[l.Texture]
		if ok {
			t := l.Tint
			if t == (mgl32.Vec3{}) {
				t = mgl32.Vec3{1, 1, 1}
			}
			col = mgl32.Vec3{col.X() * t.X(), col.Y() * t.Y(), col.Z() * t.Z()}
		} else {
			col = k.Color
		}
		g.Color = g.Color.Add(col.Mul(share))
		kinds[l.Soft] += share
	}
	if g.Share <= 0 {
		return SoftGround{}
	}
	g.Fill /= g.Share
	g.Color = g.Color.Mul(1 / g.Share)
	best := float32(0)
	for k, s := range kinds {
		if s > best || (s == best && k < g.Kind) {
			g.Kind, best = k, s
		}
	}
	return g
}

// softWeights is a weights texture's four channels at (u, v), bilinear and
// clamped to the edge, 0..1. A texture that cannot be read weighs nothing.
func softWeights(ref string, u, v float32) [4]float32 {
	m := softIndex.maps[ref]
	if done := softIndex.loading[ref]; m == nil && done != nil {
		// On its way (SoftMapPrefetch): wait for it, the index unlocked
		// so the decode can store it. The caller holds the lock and reads
		// nothing of the index after this but the map.
		softIndex.Unlock()
		<-done
		softIndex.Lock()
		m = softIndex.maps[ref]
	}
	if m == nil {
		m = loadSoftMap(ref)
		softIndex.maps[ref] = m
	}
	if !m.ok {
		return [4]float32{}
	}
	fx := u*float32(m.w) - 0.5
	fz := v*float32(m.h) - 0.5
	x0, z0 := int(math.Floor(float64(fx))), int(math.Floor(float64(fz)))
	tx, tz := fx-float32(x0), fz-float32(z0)
	var out [4]float32
	for j := 0; j < 2; j++ {
		for i := 0; i < 2; i++ {
			px := min(max(x0+i, 0), m.w-1)
			pz := min(max(z0+j, 0), m.h-1)
			wt := (1 - tx + float32(i)*(2*tx-1)) * (1 - tz + float32(j)*(2*tz-1))
			o := 4 * (pz*m.w + px)
			for c := 0; c < 4; c++ {
				out[c] += wt * float32(m.pix[o+c]) / 255
			}
		}
	}
	return out
}

// loadSoftMap decodes a weights texture as stored: its channels are four
// layers' weights, not a colour, so an NRGBA's alpha must not be folded
// into the others (gfx's decodeExact does the same for the GPU's copy).
func loadSoftMap(ref string) *softMap {
	path, ok := gfx.FxTexturePath(ref)
	if !ok {
		return &softMap{}
	}
	f, err := os.Open(path)
	if err != nil {
		return &softMap{}
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return &softMap{}
	}
	b := img.Bounds()
	m := &softMap{w: b.Dx(), h: b.Dy(), ok: b.Dx() > 0 && b.Dy() > 0}
	switch t := img.(type) {
	case *image.NRGBA:
		m.pix = make([]uint8, 4*m.w*m.h)
		for y := 0; y < m.h; y++ {
			copy(m.pix[4*y*m.w:4*(y+1)*m.w], t.Pix[y*t.Stride:y*t.Stride+4*m.w])
		}
	case *image.RGBA:
		m.pix = make([]uint8, 4*m.w*m.h)
		for y := 0; y < m.h; y++ {
			copy(m.pix[4*y*m.w:4*(y+1)*m.w], t.Pix[y*t.Stride:y*t.Stride+4*m.w])
		}
	default:
		rgba := image.NewNRGBA(image.Rect(0, 0, m.w, m.h))
		draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Src)
		m.pix = rgba.Pix
	}
	return m
}

// ---------- the trail stamps ----------

// TrailStamp is one press into the ground trails (render/ground_trails.go):
// a capsule from A to B (world X, Z) of the given radius, pressed Strength
// deep (0..1 of the ground's own depth), with a raised rim of Rim around it.
// A footprint is a short capsule along the foot.
type TrailStamp struct {
	A, B     mgl32.Vec2
	Radius   float32
	Strength float32
	Rim      float32
}

var trailStamps struct {
	sync.Mutex
	list []TrailStamp
}

// maxQueuedStamps caps the queue between draws: a frame that is not drawn
// (a headless run, a minimised window) must not grow it without bound.
const maxQueuedStamps = 256

// PushTrailStamp queues a stamp for the next draw's trail update.
func PushTrailStamp(s TrailStamp) {
	trailStamps.Lock()
	if len(trailStamps.list) >= maxQueuedStamps {
		copy(trailStamps.list, trailStamps.list[1:])
		trailStamps.list = trailStamps.list[:len(trailStamps.list)-1]
	}
	trailStamps.list = append(trailStamps.list, s)
	trailStamps.Unlock()
}

// DrainTrailStamps appends the queued stamps to dst and empties the queue.
func DrainTrailStamps(dst []TrailStamp) []TrailStamp {
	trailStamps.Lock()
	dst = append(dst, trailStamps.list...)
	trailStamps.list = trailStamps.list[:0]
	trailStamps.Unlock()
	return dst
}

// walkerDrag is each AI walker's soft-ground drag by entity ID, written by
// the sim's steps (sim/footsteps.go) and read by the AI's steering
// (ai.steer), which shortens the speed it asks for by it — a drag on the
// TARGET speed, so it is the same at any frame rate and any acceleration.
var walkerDrag = map[string]float32{}

// SetWalkerDrag records entity id's drag (0 forgets it). Main thread only,
// as the sim and the AI both are.
func SetWalkerDrag(id string, d float32) {
	if d <= 0 {
		delete(walkerDrag, id)
		return
	}
	walkerDrag[id] = d
}

// WalkerDrag is entity id's soft-ground drag, 0 on firm ground.
func WalkerDrag(id string) float32 { return walkerDrag[id] }

// ClearWalkerDrag forgets every walker's drag (a new level).
func ClearWalkerDrag() { clear(walkerDrag) }

// TrailFocus is where the trail map is centred: the player's feet, written
// by the sim each frame (the camera's position when there is no player).
var TrailFocus mgl32.Vec3

// PlayerGroundSink is how far the first-person eye is lowered by the soft
// ground the player stands in, eased by the sim (sim/footsteps.go) and
// subtracted by PlacePlayerCamera.
var PlayerGroundSink float32
