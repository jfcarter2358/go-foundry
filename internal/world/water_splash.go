package world

// water_splash.go — a splash where something meets the water
// (docs/terrain-paint/PHASE_W5.md, item 3).
//
// One call does everything a disturbance of the surface shows and sounds
// like: a one-shot particle effect at the surface (spawn_effect's
// self-removing emitter), the rings it throws (PushWaterRipple) and a
// positional sound. The callers are the simulation's wading watch
// (sim/water_wade.go: a foot in the shallows, a body falling in, anything
// thrown landing in a pond) and the script's hitscan (a shot striking the
// water on its way to the bed).
//
// The shipped effects and clips (resources/particles/water_*.fpar,
// resources/sounds/water_*.wav, phase W5) are looked up by name; a project
// without them gets the rings alone, and a missing name is asked of the disk
// once, not per splash.
//
// Like a footstep's puff the emitter is a one-shot, which the level file
// never writes and a save never captures (level_save.go): a splash is a
// picture, not state. The caller marks the play session dirty (the script
// runtime's rule for anything play adds to the level).

import (
	"math/rand"
	"sync"

	"foundry/internal/audio"
	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// SplashKind is what made the splash.
type SplashKind int

const (
	// SplashStep is a foot coming down in shallow water (wading).
	SplashStep SplashKind = iota
	// SplashEnter is a body dropping into water — a jump into a pond, a fall.
	SplashEnter
	// SplashImpact is something small striking the surface: a shot, a stone.
	SplashImpact
)

// splashLook is each kind's effect, sound and rings.
var splashLook = [...]struct {
	effect, sound string
	gain, reach   float32
}{
	SplashStep:   {"water_step", "water_step", 0.55, 1.2},
	SplashEnter:  {"water_splash", "water_splash", 1, 3.5},
	SplashImpact: {"water_impact", "water_impact", 0.7, 1.6},
}

// splashMissing remembers an effect or clip that is not there, so a
// project without the shipped assets asks the disk once, not per splash.
var splashMissing struct {
	sync.Mutex
	m map[string]bool
}

func splashIsMissing(key string) bool {
	splashMissing.Lock()
	defer splashMissing.Unlock()
	return splashMissing.m[key]
}

func splashSetMissing(key string) {
	splashMissing.Lock()
	if splashMissing.m == nil {
		splashMissing.m = map[string]bool{}
	}
	splashMissing.m[key] = true
	splashMissing.Unlock()
}

// SplashWater makes a splash of kind at p (on the surface) with strength
// 0..1, tinted by the water's own colour; it returns whether it added an
// emitter to the level (the caller then marks the play session dirty).
func SplashWater(lvl *Level, p mgl32.Vec3, kind SplashKind, strength float32, tint mgl32.Vec3) bool {
	if lvl == nil || int(kind) >= len(splashLook) {
		return false
	}
	look := splashLook[kind]
	strength = min(max(strength, 0.05), 1)
	PushWaterRipple(WaterRipple{At: p, Strength: strength, Reach: look.reach * (0.6 + 0.4*strength)})

	if look.sound != "" && !splashIsMissing("s:"+look.sound) {
		if audio.SoundPath(look.sound) == "" {
			splashSetMissing("s:" + look.sound)
		} else {
			audio.Play(audio.PlayOptions{Clip: look.sound, Gain: look.gain * (0.5 + 0.5*strength),
				Pitch: 0.88 + 0.24*rand.Float32(), Spatial: true, Position: p})
		}
	}
	if look.effect == "" || splashIsMissing("e:"+look.effect) {
		return false
	}
	if !spawnSplash(lvl, look.effect, p, strength, tint) {
		splashSetMissing("e:" + look.effect)
		return false
	}
	return true
}

// spawnSplash places the one-shot emitter; false when the effect will not
// load.
func spawnSplash(lvl *Level, effect string, p mgl32.Vec3, strength float32, tint mgl32.Vec3) bool {
	e := NewEmitter(UniqueEmitterID(lvl, "splash_"+effect), p)
	e.Effect = effect
	e.OneShot = true
	e.RateScale = 0.5 + 0.5*strength
	if tint != (mgl32.Vec3{}) {
		e.Tint = tint
	}
	if err := e.Load(); err != nil {
		return false
	}
	for i := range e.Doc.Layers {
		_ = gfx.EnsureTextureLoaded(e.Doc.Layers[i].DrawTexture())
	}
	lvl.Emitters = append(lvl.Emitters, e)
	return true
}

// WaterSplashTint is the colour a splash of water w takes: white water
// lightly tinted by the body's shallow colour (a swamp's spray is browner).
func WaterSplashTint(w WaterSample) mgl32.Vec3 {
	s := w.Water.ShallowColor
	if s == (mgl32.Vec3{}) {
		return mgl32.Vec3{1, 1, 1}
	}
	return mgl32.Vec3{0.75 + 0.25*s.X(), 0.75 + 0.25*s.Y(), 0.75 + 0.25*s.Z()}
}
