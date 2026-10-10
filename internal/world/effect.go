package world

// effect.go — Effect, a placed procedural effect.
//
// An emitter is a placement of a .fpar document: what it looks like is
// particles, and what the particles do is authored elsewhere. An effect is the
// other kind of spectacle — one the engine knows how to draw by itself, from a
// handful of numbers, because what it is cannot be built out of sprites and
// soft balls without looking like it was. The first one is an explosion: a
// ray-marched fireball that radiates outward, cools from white through orange
// to smoke, and lights the level around it while it burns.
//
// So the split is by Kind rather than by document. The level entry carries the
// kind and that kind's parameters directly — there is no second file, because
// the parameters ARE the effect, the way a sound's attenuation is the sound.
// A new kind is a new EffectKind, a parameter block beside Explosion, and a
// branch where the explosion's density and light are evaluated
// (render/volumetric.go, volume_common.glsl, EffectLights below).
//
// Time is runtime-only, like Emitter.Systems: a level file says an explosion
// goes here, not how far through going off it currently is.

import (
	"math"
	"sort"

	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// EffectKind names what an effect is. A string rather than an enum int, on
// level_schema.go's rule: a reordered enum cannot reinterpret an old file.
type EffectKind string

const (
	// EffectExplosion is a volumetric fireball that expands, cools into smoke
	// and flashes light over the level while it burns.
	EffectExplosion EffectKind = "explosion"
)

// EffectKinds is every kind, in the order the editor offers them.
var EffectKinds = []EffectKind{EffectExplosion}

// Explosion is the explosion kind's parameters.
//
// Every one of them is a property of this placement, not of an asset shared
// with other placements — see the file comment for why there is no document.
type Explosion struct {
	// Radius is how far the fireball reaches once fully grown, in world units.
	Radius float32
	// Duration is the seconds from detonation to the last of the smoke.
	Duration float32
	// Brightness multiplies the fire's own glow. The colour comes from how
	// hot each part of it is; this is how much of it there is.
	Brightness float32
	// FireTint multiplies the fire's colour ramp. White is a natural fire.
	FireTint mgl32.Vec3
	// SmokeColor is the smoke's albedo — what the scene's light reflects off
	// once the fire in it has cooled.
	SmokeColor mgl32.Vec3
	// Density is the extinction per world unit at the fireball's thickest.
	Density float32
	// Turbulence is how far the noise pushes the surface in and out, as a
	// fraction of the radius. 0 is a sphere.
	Turbulence float32
	// Rise is how far the ball climbs over its life, in world units: hot gas
	// lifting the smoke off the ground it went off on.
	Rise float32
	// LightColor, LightBrightness and LightRadius are the flash it lights the
	// level with. LightBrightness 0 is an explosion that lights nothing.
	LightColor      mgl32.Vec3
	LightBrightness float32
	LightRadius     float32
}

// DefaultExplosion is a man-sized fireball, the parameters a freshly placed
// explosion starts with.
func DefaultExplosion() Explosion {
	return Explosion{
		Radius:          4,
		Duration:        3,
		Brightness:      1.6,
		FireTint:        mgl32.Vec3{1, 1, 1},
		SmokeColor:      mgl32.Vec3{0.3, 0.28, 0.26},
		Density:         1.6,
		Turbulence:      0.6,
		Rise:            2,
		LightColor:      mgl32.Vec3{1, 0.62, 0.3},
		LightBrightness: 6,
		LightRadius:     24,
	}
}

// Effect is one placed procedural effect.
type Effect struct {
	// ID is unique within a level; a script addresses an effect by it (`@id`).
	ID string
	// ScriptData carries the tags, attributes and attached script.
	ScriptData

	// Kind is what the effect is. The parameter block for it is the one read.
	Kind EffectKind

	// Position is world-space unless Attach names a carrier, in which case it
	// is that carrier's local space. Rotation is Euler XYZ in radians; it turns
	// the effect's noise, so two explosions side by side do not look alike.
	Position mgl32.Vec3
	Rotation mgl32.Vec3

	// Enabled is the master switch: a disabled effect draws and lights nothing.
	// Playing is whether its timeline is running.
	Enabled bool
	Playing bool
	// Loop sets it off again LoopDelay seconds after it finishes. Off, it goes
	// off once and stops until a script plays it again.
	Loop      bool
	LoopDelay float32

	// Explosion is the EffectExplosion kind's parameters.
	Explosion Explosion

	// Attach names the frame, entity or bone this effect rides.
	Attach Attach

	// OneShot marks an effect that removes itself once it has finished. Set
	// only by the `explode` verb and never serialized, for Emitter.OneShot's
	// reason.
	OneShot bool

	// Time is the seconds since this run went off. Negative while a looping
	// effect waits out LoopDelay. Runtime-only, never serialized.
	Time float32
}

// NewEffect returns an effect of kind with the defaults a freshly placed one
// should have: running and looping, so it can be placed by eye rather than by
// pressing Detonate after every nudge.
func NewEffect(id string, kind EffectKind, pos mgl32.Vec3) Effect {
	if kind == "" {
		kind = EffectExplosion
	}
	return Effect{
		ID:        id,
		Kind:      kind,
		Position:  pos,
		Enabled:   true,
		Playing:   true,
		Loop:      true,
		LoopDelay: 1,
		Explosion: DefaultExplosion(),
	}
}

// Duration is how long one run lasts, in seconds, never zero.
func (e *Effect) Duration() float32 {
	d := e.Explosion.Duration
	if d < 0.05 {
		d = 0.05
	}
	return d
}

// Progress is how far through its run the effect is, 0 to 1. Outside that
// range — waiting to loop, or finished — it is not live.
func (e *Effect) Progress() float32 {
	return e.Time / e.Duration()
}

// Live reports whether the effect is drawing and lighting this frame.
func (e *Effect) Live() bool {
	if !e.Enabled || !e.Playing {
		return false
	}
	a := e.Progress()
	return a >= 0 && a < 1
}

// Detonate sets the effect off from the start — the editor's button, and the
// `play` verb.
func (e *Effect) Detonate() {
	e.Enabled = true
	e.Playing = true
	e.Time = 0
}

// Step advances the timeline. Reports whether the run just ended on an effect
// that does not loop.
func (e *Effect) Step(dt float32) (finished bool) {
	if !e.Enabled || !e.Playing || dt <= 0 {
		return false
	}
	e.Time += dt
	d := e.Duration()
	if e.Time < d {
		return false
	}
	if e.Loop {
		delay := e.LoopDelay
		if delay < 0 {
			delay = 0
		}
		// Wrapped rather than reset, so a long hitch does not lose the delay;
		// clamped so a hitch longer than a whole cycle does not leave it
		// several cycles behind and firing every frame to catch up.
		e.Time -= d + delay
		if e.Time > 0 {
			e.Time = 0
		}
		return false
	}
	e.Playing = false
	e.Time = d
	return true
}

// ModelMatrix is the effect's authored transform, before attachment.
func (e *Effect) ModelMatrix() mgl32.Mat4 {
	T := mgl32.Translate3D(e.Position.X(), e.Position.Y(), e.Position.Z())
	return T.Mul4(EulerMatrixYXZ(e.Rotation))
}

// OriginMatrix is the effect's transform, carried by whatever it rides.
func (e *Effect) OriginMatrix(lvl *Level) mgl32.Mat4 {
	if e.Attach.IsZero() {
		return e.ModelMatrix()
	}
	return e.Attach.Matrix(lvl).Mul4(e.ModelMatrix())
}

// WorldPosition is where the effect currently is.
func (e *Effect) WorldPosition(lvl *Level) mgl32.Vec3 {
	m := e.OriginMatrix(lvl)
	return mgl32.Vec3{m[12], m[13], m[14]}
}

// WorldRotation is the effect's orientation in world space, as a pure rotation
// with any carrier's scale divided out — what turns its noise.
func (e *Effect) WorldRotation(lvl *Level) mgl32.Mat3 {
	m := e.OriginMatrix(lvl).Mat3()
	for c := 0; c < 3; c++ {
		col := m.Col(c)
		if l := col.Len(); l > 1e-6 {
			m.SetCol(c, col.Mul(1/l))
		}
	}
	return m
}

// ---------- the explosion's timeline ----------

// ExplosionState is an explosion at one instant: what the renderer marches and
// what its light is. Solved on the CPU so the shader, the light and a test all
// read one answer.
type ExplosionState struct {
	// Center is the fireball's centre in world space, lifted by Rise.
	Center mgl32.Vec3
	// Radius is its current size — the surface before noise displacement.
	Radius float32
	// Age is the run's progress, 0 to 1.
	Age float32
	// Heat is how hot the fire still is, 1 at detonation, 0 once it is smoke.
	Heat float32
	// Fade scales the density, falling to 0 as the last smoke thins out.
	Fade float32
	// Flash is the light's envelope, 0 to 1.
	Flash float32
	// Jets is how hard the blast is still throwing fire outward in streaks,
	// 1 at detonation, gone well before the fire cools. The shader pushes the
	// surface out along the radius by it, and Reach makes room for them.
	Jets float32
}

// The fireball's growth is two fronts added together: the blast, which takes
// it to most of its size in the first tenth of a second or so, and the slow
// billowing spread of what is left burning after it. A single curve either
// balloons (too slow at the start) or stops dead (too fast at the end).
const (
	explosionBlastGrowth = 30
	explosionBlastShare  = 0.72
	explosionDriftGrowth = 3
)

// explosionJetsReach is how much further than the billows the jets can push
// the surface, as a share of Turbulence. MUST match the jets term in
// volume_common.glsl's explosionDensity.
const explosionJetsReach = 0.7

// ExplosionStateAt solves the explosion kind at run progress age, for an
// effect whose origin is at. ok is false outside the run.
func (x Explosion) StateAt(age float32, at mgl32.Vec3) (s ExplosionState, ok bool) {
	if age < 0 || age >= 1 {
		return ExplosionState{}, false
	}
	a := float64(age)
	blast := (1 - math.Exp(-explosionBlastGrowth*a)) / (1 - math.Exp(-explosionBlastGrowth))
	drift := (1 - math.Exp(-explosionDriftGrowth*a)) / (1 - math.Exp(-explosionDriftGrowth))
	grow := explosionBlastShare*blast + (1-explosionBlastShare)*drift
	radius := float64(x.Radius) * (0.1 + 0.9*grow)
	jets := smoothstep64(0, 0.01, a) * (1 - smoothstep64(0.04, 0.3, a))

	heat := math.Exp(-3.2*a) * (1 - smoothstep64(0.45, 0.85, a))
	fade := 1 - smoothstep64(0.5, 1, a)
	// A near-instant attack, then a decay well ahead of the fire's: the flash
	// is the detonation, and a light that lingered with the smoke would make
	// the smoke look like it was burning.
	flash := math.Min(a/0.015, 1) * math.Exp(-7*a)

	rise := float32(math.Pow(a, 1.5)) * x.Rise
	return ExplosionState{
		Center: at.Add(mgl32.Vec3{0, rise, 0}),
		Radius: float32(radius),
		Age:    age,
		Heat:   float32(heat),
		Fade:   float32(fade),
		Flash:  float32(flash),
		Jets:   float32(jets),
	}, true
}

// Reach is how far from the centre the fireball can have density at all: its
// radius pushed out by the noise at its strongest, and by the jets while they
// last.
func (x Explosion) Reach(s ExplosionState) float32 {
	t := x.Turbulence
	if t < 0 {
		t = 0
	}
	return s.Radius * (1 + t*(1+explosionJetsReach*s.Jets))
}

// ExplosionState is the effect's explosion as it is this frame, if it is live.
func (e *Effect) ExplosionState(lvl *Level) (ExplosionState, bool) {
	if e.Kind != EffectExplosion || !e.Live() {
		return ExplosionState{}, false
	}
	return e.Explosion.StateAt(e.Progress(), e.WorldPosition(lvl))
}

// WorldAABB bounds what the effect draws this frame. ok is false when it draws
// nothing.
func (e *Effect) WorldAABB(lvl *Level) (min, max mgl32.Vec3, ok bool) {
	s, live := e.ExplosionState(lvl)
	if !live {
		return mgl32.Vec3{}, mgl32.Vec3{}, false
	}
	r := e.Explosion.Reach(s)
	ext := mgl32.Vec3{r, r, r}
	return s.Center.Sub(ext), s.Center.Add(ext), true
}

func smoothstep64(lo, hi, x float64) float64 {
	t := (x - lo) / (hi - lo)
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return t * t * (3 - 2*t)
}

// ---------- light ----------

// EffectLights appends the point lights the level's live effects are casting
// this frame to dst and returns it.
//
// Dynamic, because a flash is the definition of a light that changes in game —
// it must be summed live on baked surfaces too. Never shadow-casting: a
// cubemap per explosion is six scene passes for a light that is gone in a
// second, and the fireball itself is where the light is coming from.
func EffectLights(lvl *Level, dst []PointLight) []PointLight {
	if lvl == nil {
		return dst
	}
	for i := range lvl.Effects {
		e := &lvl.Effects[i]
		s, ok := e.ExplosionState(lvl)
		if !ok {
			continue
		}
		x := &e.Explosion
		b := x.LightBrightness * s.Flash
		if b <= 1e-3 || x.LightRadius <= 0 {
			continue
		}
		// Warmed toward the fire's colour while it is hot, and reaching
		// further as the fireball grows: the light comes off the whole ball,
		// not off the point it went off at.
		reach := x.LightRadius * (0.55 + 0.45*s.Radius/maxF32(x.Radius, 1e-3))
		dst = append(dst, PointLight{
			ID:         e.ID,
			Position:   s.Center,
			Color:      x.LightColor,
			Brightness: b,
			Radius:     reach,
			Dynamic:    true,
		})
	}
	return dst
}

// sceneLightPoints is SceneLights' reused buffer. Everything that calls it is
// on the render thread and uploads the answer before asking again.
var sceneLightPoints []PointLight

// SceneLights is the level's lights with attached ones resolved to where they
// now are and its effects' and emitters' flashes added — what every lighting
// upload that runs per frame should hand the shader.
//
// The shader carries MaxPointLights of them. While the level and its flashes
// together fit, everything is here, the level's own lights first and in the
// order the file gave them. When they do not, fitPointLights below decides who
// gets a slot.
func SceneLights(lvl *Level) LightSet {
	if lvl == nil {
		return LightSet{}
	}
	ls := lvl.Lights
	attached := HasAttachedLights(lvl)
	flashes := len(lvl.Effects) > 0 || len(lvl.Emitters) > 0
	if !attached && !flashes && len(ls.Points) <= MaxPointLights {
		return ls
	}
	buf := append(sceneLightPoints[:0], ls.Points...)
	// Attached lights are carried out to where their carriers now are. A copy,
	// so the level keeps the authored offset the editor and the file own.
	for i := range buf {
		if !buf[i].Attach.IsZero() {
			buf[i].Position = buf[i].WorldPosition(lvl)
		}
	}
	if flashes {
		buf = EffectLights(lvl, buf)
		buf = EmitterLights(lvl, buf)
	}
	sceneLightPoints = buf
	ls.Points = fitPointLights(buf)
	return ls
}

// sceneLightFit is fitPointLights' reused answer, on sceneLightPoints' terms.
// A second buffer rather than a cut of that one, because the fit reads the
// whole list while it is building its answer.
var sceneLightFit []PointLight

// fitPointLights cuts a scene's point lights down to the MaxPointLights the
// shader carries, keeping the ones the viewer is actually going to see.
//
// Under the limit it is the identity, which is every level the editor will let
// you author: it refuses a ninth point light. Over it — a level a tool
// generated, or an ordinary one in a firefight full of muzzle flashes — the
// rule is:
//
//   - every SHADOW-CASTING light keeps its slot, in the order it came in. Its
//     cubemap is found by its index (RenderPointShadowPass walks this same
//     list), so shuffling one would hand it another light's shadow and throw
//     away the point-shadow cache every time the camera moved.
//   - the rest, flashes included, compete by what they contribute to what the
//     camera can see: brightness and reach for them, distance against them.
//     So the flash on the end of the player's own gun beats a lamp across the
//     courtyard, and a rifle going off two hundred metres away beats nothing.
//
// Before this, the first eight in the file won and everything after them lit
// nothing at all, wherever it stood and however bright it was.
func fitPointLights(buf []PointLight) []PointLight {
	if len(buf) <= MaxPointLights {
		return buf
	}
	kept := sceneLightFit[:0]
	for i := range buf {
		if buf[i].CastsShadows() && len(kept) < MaxPointLights {
			kept = append(kept, buf[i])
		}
	}
	if len(kept) < MaxPointLights {
		rest := sceneLightRest[:0]
		for i := range buf {
			if !buf[i].CastsShadows() {
				rest = append(rest, buf[i])
			}
		}
		// Stable, so two lights of equal worth keep level order and neither
		// flickers in and out of the last slot from frame to frame.
		sort.SliceStable(rest, func(a, b int) bool {
			return pointLightWorth(&rest[a]) > pointLightWorth(&rest[b])
		})
		if n := MaxPointLights - len(kept); n < len(rest) {
			rest = rest[:n]
		}
		kept = append(kept, rest...)
		sceneLightRest = rest[:0]
	}
	sceneLightFit = kept
	return kept
}

// sceneLightRest is fitPointLights' scratch list of the lights competing for
// what the shadow casters left.
var sceneLightRest []PointLight

// pointLightWorth ranks one point light against another for a contested slot:
// how much of it the viewer is going to see. A light that is off, or that
// reaches nowhere, is worth nothing.
func pointLightWorth(l *PointLight) float32 {
	b := l.EffectiveBrightness()
	if b <= 0 || l.Radius <= 0 {
		return 0
	}
	return b * l.Radius / (1 + l.Position.Sub(gfx.Cam.Position).LenSqr())
}

// EffectLightCount is how many effect flashes are lighting the level now.
func EffectLightCount(lvl *Level) int {
	n := 0
	if lvl == nil {
		return 0
	}
	for i := range lvl.Effects {
		e := &lvl.Effects[i]
		if s, ok := e.ExplosionState(lvl); ok && e.Explosion.LightBrightness*s.Flash > 1e-3 {
			n++
		}
	}
	return n
}

// ---------- level lookup ----------

// FindEffect returns the effect with the given ID, or nil.
func FindEffect(lvl *Level, id string) *Effect {
	if lvl == nil || id == "" {
		return nil
	}
	for i := range lvl.Effects {
		if lvl.Effects[i].ID == id {
			return &lvl.Effects[i]
		}
	}
	return nil
}

// UniqueEffectID mints an ID free across the whole level.
func UniqueEffectID(lvl *Level, base string) string {
	if base == "" {
		base = "effect"
	}
	return uniqueLevelID(lvl, base)
}

// ---------- the per-frame pass ----------

// effectLightsWereLit remembers whether last frame's upload carried any effect
// light, so the frame after the last flash dies uploads once more to clear it.
var effectLightsWereLit bool

// EffectUpdate advances every effect and reports whether the scene's lights
// need uploading again this frame — while any flash is lit, and once more
// after the last one goes out.
//
// Called beside EmitterUpdate and not gated on the editor being open, for the
// emitter's reason: an explosion that froze while placing it could not be
// placed by eye. running is false only under a pause, which does stop it.
func EffectUpdate(lvl *Level, dt float32, running bool) (lightsChanged bool) {
	if lvl == nil {
		return false
	}
	if running {
		reap := false
		for i := range lvl.Effects {
			if lvl.Effects[i].Step(dt) && lvl.Effects[i].OneShot {
				reap = true
			}
		}
		if reap {
			out := lvl.Effects[:0]
			for i := range lvl.Effects {
				e := &lvl.Effects[i]
				if e.OneShot && !e.Playing {
					continue
				}
				out = append(out, *e)
			}
			lvl.Effects = out
		}
	}
	lit := EffectLightCount(lvl) > 0
	changed := lit || effectLightsWereLit
	effectLightsWereLit = lit
	return changed
}

// ---------- serialization ----------

// EffectFromRaw builds an effect from its level-file form.
func EffectFromRaw(r RawEffect) (Effect, error) {
	data, err := ScriptDataFromRaw(r.RawScriptData, "effect "+r.ID)
	if err != nil {
		return Effect{}, err
	}
	def := DefaultExplosion()
	e := Effect{
		ID:         r.ID,
		ScriptData: data,
		Kind:       EffectKind(r.Kind),
		Position:   mgl32.Vec3{r.Position.X, r.Position.Y, r.Position.Z},
		Rotation:   mgl32.Vec3{r.Rotation.X, r.Rotation.Y, r.Rotation.Z},
		Enabled:    BoolDefault(r.Enabled, true),
		Playing:    BoolDefault(r.Playing, true),
		Loop:       BoolDefault(r.Loop, true),
		LoopDelay:  float32Default(r.LoopDelay, 1),
		Attach:     AttachFromRaw(r.Attach),
		Explosion:  def,
	}
	if e.Kind == "" {
		e.Kind = EffectExplosion
	}
	if x := r.Explosion; x != nil {
		e.Explosion = Explosion{
			Radius:          float32Default(x.Radius, def.Radius),
			Duration:        float32Default(x.Duration, def.Duration),
			Brightness:      float32Default(x.Brightness, def.Brightness),
			FireTint:        color3Default(x.FireTint, def.FireTint),
			SmokeColor:      color3Default(x.SmokeColor, def.SmokeColor),
			Density:         float32Default(x.Density, def.Density),
			Turbulence:      float32Default(x.Turbulence, def.Turbulence),
			Rise:            float32Default(x.Rise, def.Rise),
			LightColor:      color3Default(x.LightColor, def.LightColor),
			LightBrightness: float32Default(x.LightBrightness, def.LightBrightness),
			LightRadius:     float32Default(x.LightRadius, def.LightRadius),
		}
	}
	return e, nil
}

// EffectToRaw writes an effect back out. Every key is omitted at its default,
// and the explosion block is left out entirely when nothing in it was changed.
func EffectToRaw(e *Effect) RawEffect {
	r := RawEffect{
		ID:            e.ID,
		RawScriptData: ScriptDataToRaw(e.ScriptData),
		Kind:          string(e.Kind),
		Position:      RawVec3{X: e.Position.X(), Y: e.Position.Y(), Z: e.Position.Z()},
		Rotation:      RawVec3{X: e.Rotation.X(), Y: e.Rotation.Y(), Z: e.Rotation.Z()},
		Enabled:       BoolPtrIfFalse(e.Enabled),
		Playing:       BoolPtrIfFalse(e.Playing),
		Loop:          BoolPtrIfFalse(e.Loop),
		LoopDelay:     float32PtrIfNot(e.LoopDelay, 1),
		Attach:        AttachToRaw(e.Attach),
	}
	if e.Kind == EffectExplosion {
		def := DefaultExplosion()
		x := &e.Explosion
		rx := RawExplosion{
			Radius:          float32PtrIfNot(x.Radius, def.Radius),
			Duration:        float32PtrIfNot(x.Duration, def.Duration),
			Brightness:      float32PtrIfNot(x.Brightness, def.Brightness),
			FireTint:        color3PtrIfNot(x.FireTint, def.FireTint),
			SmokeColor:      color3PtrIfNot(x.SmokeColor, def.SmokeColor),
			Density:         float32PtrIfNot(x.Density, def.Density),
			Turbulence:      float32PtrIfNot(x.Turbulence, def.Turbulence),
			Rise:            float32PtrIfNot(x.Rise, def.Rise),
			LightColor:      color3PtrIfNot(x.LightColor, def.LightColor),
			LightBrightness: float32PtrIfNot(x.LightBrightness, def.LightBrightness),
			LightRadius:     float32PtrIfNot(x.LightRadius, def.LightRadius),
		}
		if rx != (RawExplosion{}) {
			r.Explosion = &rx
		}
	}
	return r
}

// color3Default reads an optional colour against a default other than white.
func color3Default(c *RawColor, def mgl32.Vec3) mgl32.Vec3 {
	if c == nil {
		return def
	}
	return mgl32.Vec3{c.R, c.G, c.B}
}

// color3PtrIfNot writes a colour only when it differs from def.
func color3PtrIfNot(v, def mgl32.Vec3) *RawColor {
	if v == def {
		return nil
	}
	return &RawColor{R: v.X(), G: v.Y(), B: v.Z()}
}
