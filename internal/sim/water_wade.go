package sim

// water_wade.go — bodies in water (docs/terrain-paint/PHASE_W5.md, items 2
// and 3).
//
// Once every body has moved, each one that can be in water — the player on
// foot, and every entity that falls (walkers, thrown things, bodies) — is
// asked of world.WaterAt at its feet, and what it is doing there shows on
// the surface:
//
//   - DROPPING IN: its feet were above the surface last tick and are under
//     it now, falling faster than a step down a bank (enterSpeed). A splash
//     (world.SplashEnter; a small body — a thrown stone, a can — makes
//     SplashImpact instead) at the point it went in, its strength from how
//     fast it was falling.
//   - WADING: standing in water shallower than most of its height. Every
//     stride a foot comes down with a small splash and its rings
//     (world.SplashStep), and while it moves it trails a ring every
//     wakeEvery seconds — the wake round its legs; standing still, a slow
//     ring now and then.
//   - Deeper — swimming, or walking the bed of a pond with the water over
//     its head — no splashes, but a ring above it now and then while it
//     moves near the surface.
//
// Everything goes through world.SplashWater (the effect, the rings, the
// sound), so the shapes of a splash live in one place. None of it is saved:
// rings and spray are a picture, not state. The emitters it adds are
// one-shots, and the play session is marked dirty for them as the footsteps'
// puffs are, so leaving play puts the level back.

import (
	"math"

	"foundry/internal/platform"
	"foundry/internal/script"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// wader is one body's water state.
type wader struct {
	in   bool    // feet under a surface last tick
	dist float32 // ground covered in water since the last step splash
	wake float32 // seconds since the last wake ring
	seen uint64
}

var wading struct {
	lvl   *world.Level
	m     map[string]*wader
	frame uint64
}

const (
	// enterSpeed is the downward speed (m/s) past which crossing the surface
	// is dropping in, not stepping down a bank.
	enterSpeed = 2.2
	// wakeEvery is the wake's ring interval while moving, seconds;
	// stillEvery while standing.
	wakeEvery  = 0.28
	stillEvery = 1.6
	// smallBody is the half-height below which a body is a small thing
	// (a stone, a can): it lands with an impact, not a body's splash.
	smallBody = 0.3
)

// stepWater advances every body's water state by dt (the file comment).
func stepWater(lvl *world.Level, dt float32) {
	if lvl == nil || dt <= 0 {
		return
	}
	if wading.lvl != lvl || wading.m == nil {
		wading.lvl = lvl
		wading.m = map[string]*wader{}
	}
	if !world.LevelHasWater(lvl) {
		return
	}
	wading.frame++
	dirty := false
	if p := world.PlayerEntity; p != nil && p.Seat == "" && !platform.FlyMode {
		dirty = waterBody(lvl, p, playerKey, dt) || dirty
	}
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if e.IsVehicle() || e.Frame != "" || !e.HasGravity() {
			continue
		}
		dirty = waterBody(lvl, e, e.ID, dt) || dirty
	}
	if dirty {
		script.PlaySessionMarkDirty()
	}
	if wading.frame%300 == 0 {
		for id, w := range wading.m {
			if wading.frame-w.seen > 300 {
				delete(wading.m, id)
			}
		}
	}
}

// waterBody is one body's tick; true when it added an emitter.
func waterBody(lvl *world.Level, e *world.Entity, key string, dt float32) bool {
	half := e.BoundHalfExtents().Y()
	if half <= 0 {
		half = e.BoundHalfY
	}
	feet := e.Position.Sub(mgl32.Vec3{0, half, 0})
	ws := world.WaterAt(lvl, feet)
	w := wading.m[key]
	if w == nil {
		w = &wader{in: ws.In}
		wading.m[key] = w
	}
	w.seen = wading.frame
	wasIn := w.in
	w.in = ws.In
	if !ws.In {
		w.dist, w.wake = 0, 0
		return false
	}
	tint := world.WaterSplashTint(ws)
	at := mgl32.Vec3{feet.X(), ws.Surface, feet.Z()}
	vy := e.Velocity.Y()
	h := mgl32.Vec2{e.Velocity.X(), e.Velocity.Z()}
	speed := h.Len()
	added := false

	if !wasIn && vy < -enterSpeed {
		// Dropping in: a body's splash, or a small thing's plop.
		kind := world.SplashEnter
		if half < smallBody {
			kind = world.SplashImpact
		}
		strength := float32(math.Min(float64(-vy)/9, 1))
		added = world.SplashWater(lvl, at, kind, strength, tint)
		w.dist, w.wake = 0, 0
		return added
	}

	height := 2 * half
	switch {
	case ws.Depth < height*0.75:
		// Wading: the legs in the water, the body out of it.
		if speed > 0.4 {
			w.dist += speed * dt
			if s := stride(speed); w.dist >= s {
				w.dist -= s
				if w.dist > s {
					w.dist = 0
				}
				// Deeper water throws more; a run more than a walk.
				strength := min(0.25+0.35*min(ws.Depth/0.6, 1)+0.06*speed, 1)
				// Ahead of the body: the foot that is coming down.
				fwd := h.Mul(1 / speed)
				foot := at.Add(mgl32.Vec3{fwd.X() * 0.25, 0, fwd.Y() * 0.25})
				added = world.SplashWater(lvl, foot, world.SplashStep, strength, tint) || added
			}
			w.wake += dt
			if w.wake >= wakeEvery {
				w.wake = 0
				world.PushWaterRipple(world.WaterRipple{At: at, Strength: min(0.18+0.08*speed, 0.45), Reach: 1.4})
			}
		} else {
			w.wake += dt
			if w.wake >= stillEvery {
				w.wake = 0
				world.PushWaterRipple(world.WaterRipple{At: at, Strength: 0.12, Reach: 0.9})
			}
		}
	default:
		// In over its waist: rings above it while it moves near the top.
		if speed > 0.3 && ws.Depth < height+0.3 {
			w.wake += dt
			if w.wake >= wakeEvery*1.5 {
				w.wake = 0
				world.PushWaterRipple(world.WaterRipple{At: at, Strength: 0.3, Reach: 1.8})
			}
		}
	}
	return added
}
