package world

// emitter_light.go — the light an effect casts while it goes off.
//
// A muzzle flash is two things at once: the flare on the end of the barrel,
// which is particles, and the wall beside the gun lighting up for a twentieth
// of a second, which is not. The first is the .fpar document's layers; this is
// the other half — one dynamic point light per emitter whose document sets a
// `light:` block (particle.Flash).
//
// It is the emitter's rather than the effect object's (effect.go's
// EffectLights, which is the explosion's own fireball glow) because the thing
// an author reaches for is a document: `spawn_effect("muzzle_flash", at)`
// already places one everywhere a gun goes off, and a light on the document is
// a light everywhere that effect is used — the player's viewmodel, an enemy's
// rifle, a tank's main gun — with nothing to place and nothing to clean up.
//
// TIMING is the first unmuted layer's cycle clock, not a clock of this file's
// own. So the flash goes off when the effect does: at the spawn, at a restart,
// and once per lap of a looping effect. Nothing here is serialized, and a save
// that lands mid-flash simply does not have one.
//
// THE EIGHT SLOTS. The shader has MaxPointLights of them, and a firefight can
// easily light more flashes than a level has left. Who gets one is
// fitPointLights (effect.go), over the level's lights and every flash at once:
// shadow casters keep theirs, and the rest go to what the camera can actually
// see — so the flash beside the player's head is never the one dropped for a
// rifle across the valley.

import (
	"github.com/go-gl/mathgl/mgl32"
)

// FlashLight is the point light this emitter is casting this frame, if any.
//
// Gated on Emitting as well as Enabled: `stop_emit` puts a flashing effect out
// with its particles, and `emit` brings it back. The emitter's Tint multiplies
// the light the way it multiplies the layers, so a placement recoloured green
// lights green.
func (e *Emitter) FlashLight(lvl *Level) (PointLight, bool) {
	if !e.Enabled || !e.Emitting || e.Doc == nil || !e.Doc.Light.On() {
		return PointLight{}, false
	}
	t, ok := e.flashTime()
	if !ok {
		return PointLight{}, false
	}
	f := e.Doc.Light
	b := f.Brightness * f.At(t)
	if b <= 1e-3 {
		return PointLight{}, false
	}
	c, tint := f.EffectiveColor(), e.EffectiveTint()
	return PointLight{
		ID:         e.ID,
		Position:   e.WorldPosition(lvl),
		Color:      mgl32.Vec3{c.X() * tint.X(), c.Y() * tint.Y(), c.Z() * tint.Z()},
		Brightness: b,
		Radius:     f.Radius,
		Dynamic:    true,
	}, true
}

// flashTime is how far into its cycle the effect is, taken from the first
// layer that is actually running. ok is false before the first step, when the
// clock reads 0 because nothing has happened rather than because a cycle has
// just begun.
func (e *Emitter) flashTime() (float32, bool) {
	for i, s := range e.Systems {
		if e.Doc != nil && i < len(e.Doc.Layers) && e.Doc.Layers[i].Muted {
			continue
		}
		if !s.Started() {
			return 0, false
		}
		return s.Clock(), true
	}
	return 0, false
}

// EmitterLights appends the flashes the level's emitters are casting this
// frame to dst and returns it. Which of them actually get one of the shader's
// point-light slots is fitPointLights' decision, made once over the level's
// own lights and every flash together.
func EmitterLights(lvl *Level, dst []PointLight) []PointLight {
	if lvl == nil {
		return dst
	}
	for i := range lvl.Emitters {
		if l, ok := lvl.Emitters[i].FlashLight(lvl); ok {
			dst = append(dst, l)
		}
	}
	return dst
}

// EmitterFlashCount is how many emitter flashes are lighting the level now,
// before the slots are counted.
func EmitterFlashCount(lvl *Level) int {
	if lvl == nil {
		return 0
	}
	n := 0
	for i := range lvl.Emitters {
		if _, ok := lvl.Emitters[i].FlashLight(lvl); ok {
			n++
		}
	}
	return n
}

// AnyEmitterFlash reports whether any emitter is casting a flash — the cheap
// question the frame loop asks to decide whether the lights must go up again.
func AnyEmitterFlash(lvl *Level) bool {
	if lvl == nil {
		return false
	}
	for i := range lvl.Emitters {
		if _, ok := lvl.Emitters[i].FlashLight(lvl); ok {
			return true
		}
	}
	return false
}
