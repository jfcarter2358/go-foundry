package world

// scenery_draw.go — how far from the camera a placement is drawn
// (docs/regions/PRESETS_5.md).
//
// A scanned bush is a few pixels at 300 m and a few dozen clusters of DAG
// roots however far it is: a world of fourteen thousand of them seen across
// five kilometres spent most of its frame culling and rasterising things too
// small to see. So a placement may say how far it is drawn (DrawDistance),
// how far it casts (ShadowDistance), and — a cooked tree — hand over to a far
// stand-in, its IMPOSTOR (a few textured cards baked from the mesh, its own
// cooked .fnm), at ImpostorDistance. All zero, the default, is drawn
// everywhere, exactly as before the fields existed.
//
// # Windows
//
// What is drawn where is a WINDOW of camera distance, [From, To) (zero: open
// at that end), one per thing the placement draws:
//
//	the mesh       [0, ImpostorDistance) with an impostor, else [0, DrawDistance)
//	the impostor   [ImpostorDistance, DrawDistance)
//
// and a shadow window for each, the same clipped to ShadowDistance. The
// distance is from the camera to the placement's ORIGIN (Position), not to
// its bounds' centre: the mesh and its impostor have different bounds, and
// they must agree on the distance to the bit for their fades to fit.
//
// # Fades
//
// A window does not cut at its ends; it fades over the last tenth before each
// (DrawFadeBand) — out over [0.9·To, To), in over [0.9·From, From). The
// renderer does it two ways, both stable and both keyed by the placement's
// own hash of its position (DrawHash), so the same instance always fades the
// same:
//
//   - per pixel, where it can (the Nanite visibility buffer's ID pass and its
//     compute rasteriser): a pixel is kept while a screen-space noise, offset
//     by the hash, is under the fade — a dissolve that TAA resolves into a
//     soft fade. The mesh keeps the noise BELOW the fade at its To and the
//     impostor the noise AT OR ABOVE it at its From — the same number, so
//     every pixel is drawn by exactly one of them (DrawFade);
//   - per instance, everywhere else (shadow passes, the forward and classic
//     paths): the placement is drawn while d < To·(0.9 + 0.1·hash), and from
//     d ≥ From·(0.9 + 0.1·hash) — each instance drops at its own distance in
//     the band, no wave front, and the mesh's cut and its impostor's are one
//     distance, so one casts exactly where the other stops (Drawn).
//
// Shadows always take the per-instance cut: a virtual shadow map page keeps
// what it drew, and a dither there would be frozen into it.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// DrawFadeBand is the share of a window's end it fades over.
const DrawFadeBand = 0.1

// DrawWindow is a stretch of camera distance something is drawn over:
// [From, To), either end 0 for open. None is a window nothing is drawn in
// (an impostor that casts no shadow, say).
type DrawWindow struct {
	From, To float32
	None     bool
}

// Open reports whether the window is the whole of space: drawn everywhere.
func (w DrawWindow) Open() bool { return !w.None && w.From <= 0 && w.To <= 0 }

// Drawn is the per-instance cut: whether something at distance d whose
// placement hashes to hash is drawn (see the file comment).
func (w DrawWindow) Drawn(d, hash float32) bool {
	if w.None {
		return false
	}
	k := 1 - DrawFadeBand + DrawFadeBand*hash
	if w.From > 0 && d < w.From*k {
		return false
	}
	return w.To <= 0 || d < w.To*k
}

// MayDraw is the loosest test either fade can pass: whether anything of it
// can be drawn at distance d. What a CPU look ahead of the GPU's own test
// keeps (a superset of both cuts).
func (w DrawWindow) MayDraw(d float32) bool {
	if w.None {
		return false
	}
	if w.From > 0 && d < w.From*(1-DrawFadeBand) {
		return false
	}
	return w.To <= 0 || d < w.To
}

// DrawFade is the per-pixel fade at distance d: a pixel whose noise n (in
// [0, 1), already offset by the placement's hash) satisfies lo <= n < hi is
// drawn. lo 0 and hi 1 is every pixel; the shaders' drawFadeWindow is this,
// and must stay so.
func (w DrawWindow) DrawFade(d float32) (lo, hi float32) {
	lo, hi = 0, 1
	if w.None {
		return 1, 1
	}
	if w.To > 0 {
		hi = clamp01((w.To - d) / (DrawFadeBand * w.To))
	}
	if w.From > 0 {
		lo = clamp01((w.From - d) / (DrawFadeBand * w.From))
	}
	return lo, hi
}

func clamp01(x float32) float32 { return min(max(x, 0), 1) }

// impostorDrawn reports whether the placement draws an impostor: one is
// loaded (the GPU cull draws the placement) and its window is not empty.
func (s *Scenery) impostorDrawn() bool {
	if s.ImpostorMesh == nil || !s.NaniteGPU {
		return false
	}
	d := s.EffectiveImpostorDistance()
	return d > 0 && (s.DrawDistance <= 0 || d < s.DrawDistance)
}

// HasImpostor reports whether the placement draws an impostor tier.
func (s *Scenery) HasImpostor() bool { return s.impostorDrawn() }

// DefaultImpostorDistance is where a placement with an impostor but no
// ImpostorDistance hands over: a third of its draw distance, or this with
// none.
const DefaultImpostorDistance = 300

// EffectiveImpostorDistance is ImpostorDistance, or its default.
func (s *Scenery) EffectiveImpostorDistance() float32 {
	if s.ImpostorDistance > 0 {
		return s.ImpostorDistance
	}
	if s.DrawDistance > 0 {
		return s.DrawDistance / 3
	}
	return DefaultImpostorDistance
}

// MeshWindow is where the placement's own mesh is drawn.
func (s *Scenery) MeshWindow() DrawWindow {
	if s.impostorDrawn() {
		return DrawWindow{To: s.EffectiveImpostorDistance()}
	}
	return DrawWindow{To: s.DrawDistance}
}

// ImpostorWindow is where its impostor is drawn (None without one).
func (s *Scenery) ImpostorWindow() DrawWindow {
	if !s.impostorDrawn() {
		return DrawWindow{None: true}
	}
	return DrawWindow{From: s.EffectiveImpostorDistance(), To: s.DrawDistance}
}

// MeshShadowWindow and ImpostorShadowWindow are where each casts: its draw
// window clipped to ShadowDistance.
func (s *Scenery) MeshShadowWindow() DrawWindow { return s.clipShadow(s.MeshWindow()) }

func (s *Scenery) ImpostorShadowWindow() DrawWindow { return s.clipShadow(s.ImpostorWindow()) }

func (s *Scenery) clipShadow(w DrawWindow) DrawWindow {
	sd := s.ShadowDistance
	if w.None || sd <= 0 {
		return w
	}
	if w.From > 0 && sd <= w.From {
		return DrawWindow{None: true}
	}
	if w.To <= 0 || sd < w.To {
		w.To = sd
	}
	return w
}

// DrawDistanceFrom is the distance the windows are measured in: from eye to
// the placement's origin (see the file comment).
func (s *Scenery) DrawDistanceFrom(eye mgl32.Vec3) float32 { return s.Position.Sub(eye).Len() }

// DrawHash is the placement's own number in [0, 1) the fades are keyed by:
// a hash of its position, so a placement and its impostor share it, and it
// does not change when the level's list is reordered.
func (s *Scenery) DrawHash() float32 { return DrawHashAt(s.Position) }

// DrawHashAt is DrawHash for a placement at p.
func DrawHashAt(p mgl32.Vec3) float32 {
	h := uint32(2166136261)
	for _, f := range p {
		h ^= math.Float32bits(f)
		h *= 16777619
		h ^= h >> 15
	}
	h *= 0x2c1b3c6d
	h ^= h >> 12
	return float32(h>>8) / (1 << 24)
}

// DrawnAt is the per-instance cut of the placement's mesh at eye: drawn
// (shadow false) or casting (true). What the classic path and the editor's
// own counts use.
func (s *Scenery) DrawnAt(eye mgl32.Vec3, shadow bool) bool {
	w := s.MeshWindow()
	if shadow {
		w = s.MeshShadowWindow()
	}
	if w.Open() {
		return true
	}
	return w.Drawn(s.DrawDistanceFrom(eye), s.DrawHash())
}
