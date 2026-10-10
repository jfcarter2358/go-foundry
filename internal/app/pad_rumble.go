package app

// pad_rumble.go — the rumble the engine sets off by itself.
//
// Most rumble is a game's to decide — a shot, a hit, a landing — and scripts
// ask for it (`rumble`). An explosion is the engine's own: it already knows
// where every one goes off, so each kicks the pad as it detonates, harder the
// closer it is, the way its flash and sound already scale.

import (
	"foundry/internal/gfx"
	"foundry/internal/platform"
	"foundry/internal/world"
)

// explosionRumbleReach is how many fireball radii away an explosion is still
// felt.
const explosionRumbleReach = 8

// explosionsLive is which explosions were going off last frame, so each is
// felt once, as it starts. Swapped with explosionsSpare every frame rather than
// cleared, which also drops effects that have left the level.
var explosionsLive, explosionsSpare = map[string]bool{}, map[string]bool{}

// rumbleForExplosions kicks the pad for every explosion that went off since
// last frame.
func rumbleForExplosions(lvl *world.Level) {
	if lvl == nil {
		return
	}
	clear(explosionsSpare)
	for i := range lvl.Effects {
		e := &lvl.Effects[i]
		if e.Kind != world.EffectExplosion {
			continue
		}
		s, ok := e.ExplosionState(lvl)
		if !ok {
			continue
		}
		explosionsSpare[e.ID] = true
		if explosionsLive[e.ID] {
			continue
		}
		reach := e.Explosion.Radius * explosionRumbleReach
		if reach <= 0 {
			continue
		}
		k := 1 - s.Center.Sub(gfx.Cam.Position).Len()/reach
		if k <= 0 {
			continue
		}
		platform.Rumble(k, k*0.6, 0.2+0.5*k)
	}
	explosionsLive, explosionsSpare = explosionsSpare, explosionsLive
}
