package world

// sound.go — a sound placed in the level.
//
// The split is emitter.go's, for emitter.go's reason: what a sound *is* lives
// in a file under sounds/, and where it is lives in the level that places it.
// The level entry carries only the handful of knobs worth varying between two
// placements of the same asset — how loud, how far it carries, whether it
// loops — because everything else is a property of the recording and changing
// it here would be changing it for every other placement.
//
// internal/audio owns the mixing and knows nothing about levels; this file is
// the seam, exactly as world/emitter.go is the seam onto internal/particle.
//
// Note the field is Gain, not Volume. `Volume` is already a level object type
// in this engine — a scriptable region — and two unrelated meanings of the
// word inside one package would be a genuinely confusing collision.

import (
	"foundry/internal/audio"

	"github.com/go-gl/mathgl/mgl32"
)

// Sound is one placed sound source.
type Sound struct {
	// ID is unique within a level; a script addresses a sound by it (`@id`).
	ID string
	// ScriptData carries the tags, attributes and attached script, so `#tag`
	// reaches sounds on the same terms it reaches entities and emitters.
	ScriptData

	// Clip names a file under sounds/, without the extension. The extension
	// is left off so that re-encoding an asset from WAV to OGG does not mean
	// editing every level that plays it.
	Clip string

	// Position is world-space unless Attach names a carrier, in which case it
	// is that carrier's local space. Rotation is Euler XYZ in radians and aims
	// the cone; it means nothing for an omnidirectional sound.
	Position mgl32.Vec3
	Rotation mgl32.Vec3

	// Enabled is the master switch: a disabled sound holds no voice at all.
	// Playing is the softer one — it stops the sound without giving up the
	// placement, which is what a script toggling a machine on and off wants.
	Enabled bool
	Playing bool
	// Loop is true for ambience, false for a one-shot that a script re-fires.
	Loop bool

	// Gain multiplies the clip's own level; 1 is the recording as authored.
	// Pitch multiplies its playback rate.
	Gain  float32
	Pitch float32

	// Bus selects which of the player's sliders this answers to.
	Bus string

	// RefDistance is the radius within which the sound plays at full gain,
	// MaxDistance the radius past which it is silent, and Rolloff how sharply
	// it falls between them.
	RefDistance float32
	MaxDistance float32
	Rolloff     float32
	// Atten names the falloff curve. A name rather than an enum int, so a
	// reordered enum cannot silently reinterpret an old level file.
	Atten string

	// Cone makes the sound directional. Angles are total cone widths in
	// radians; a zero cone is omnidirectional, which is what a sound that
	// never sets one gets.
	ConeInner     float32
	ConeOuter     float32
	ConeOuterGain float32

	// Attach names the frame, entity or bone this sound rides.
	Attach Attach

	// OneShot marks a sound that removes itself once it has finished. Set only
	// by the `play_sound_at` verb and never serialized: a level file places
	// lasting sounds, and a script firing one per impact would otherwise fill
	// the level with spent ones.
	OneShot bool

	// Voice is the handle of the mixer voice this placement currently holds,
	// or zero when it is not sounding. Runtime-only, like Emitter.Doc — it
	// must never reach a level file.
	Voice audio.VoiceID
}

// NewSound returns a sound with the defaults a freshly placed one should have:
// enabled, sounding, looping, at the clip's own level.
//
// Looping by default because the overwhelmingly common thing to place by hand
// is ambience — a torch, a machine, a stream. A one-shot is almost always
// fired by a script rather than placed.
func NewSound(id string, pos mgl32.Vec3) Sound {
	f := audio.DefaultFalloff()
	return Sound{
		ID:          id,
		Position:    pos,
		Enabled:     true,
		Playing:     true,
		Loop:        true,
		Gain:        1,
		Pitch:       1,
		Bus:         string(audio.BusEffects),
		RefDistance: f.Ref,
		MaxDistance: f.Max,
		Rolloff:     f.Rolloff,
		Atten:       string(f.Model),
	}
}

// Falloff assembles the distance response the mixer wants.
func (s *Sound) Falloff() audio.Falloff {
	return audio.Falloff{
		Ref:     s.RefDistance,
		Max:     s.MaxDistance,
		Rolloff: s.Rolloff,
		Model:   audio.AttenModel(s.Atten),
	}
}

// Cone assembles the directional response.
func (s *Sound) Cone() audio.Cone {
	return audio.Cone{Inner: s.ConeInner, Outer: s.ConeOuter, OuterGain: s.ConeOuterGain}
}

// AudioBus maps the stored name onto a bus, defaulting to effects so a
// hand-edited level with a typo is audible and therefore fixable.
func (s *Sound) AudioBus() audio.Bus {
	if audio.Bus(s.Bus) == audio.BusMusic {
		return audio.BusMusic
	}
	return audio.BusEffects
}

// ModelMatrix is the sound's own transform, before whatever it rides.
func (s *Sound) ModelMatrix() mgl32.Mat4 {
	T := mgl32.Translate3D(s.Position.X(), s.Position.Y(), s.Position.Z())
	return T.Mul4(EulerMatrixYXZ(s.Rotation))
}

// OriginMatrix is the sound's transform carried by whatever it rides.
func (s *Sound) OriginMatrix(lvl *Level) mgl32.Mat4 {
	if s.Attach.IsZero() {
		return s.ModelMatrix()
	}
	return s.Attach.Matrix(lvl).Mul4(s.ModelMatrix())
}

// WorldPosition is where the sound currently is, for the mixer and for the
// editor's marker.
func (s *Sound) WorldPosition(lvl *Level) mgl32.Vec3 {
	m := s.OriginMatrix(lvl)
	return mgl32.Vec3{m[12], m[13], m[14]}
}

// WorldDirection is where a cone points, in world space. Local +X, matching
// how the spawn point derives its facing.
func (s *Sound) WorldDirection(lvl *Level) mgl32.Vec3 {
	m := s.OriginMatrix(lvl)
	d := mgl32.Vec3{m[0], m[1], m[2]}
	if d.Len() < 1e-6 {
		return mgl32.Vec3{1, 0, 0}
	}
	return d.Normalize()
}

// Sounding reports whether this placement should hold a voice right now.
func (s *Sound) Sounding() bool { return s.Enabled && s.Playing && s.Clip != "" }

// FindSound returns the sound with the given ID, or nil.
func FindSound(lvl *Level, id string) *Sound {
	if lvl == nil || id == "" {
		return nil
	}
	for i := range lvl.Sounds {
		if lvl.Sounds[i].ID == id {
			return &lvl.Sounds[i]
		}
	}
	return nil
}

// FindSoundsByTag gathers every sound carrying a tag, for `#tag` addressing.
func FindSoundsByTag(lvl *Level, tag string) []*Sound {
	if lvl == nil || tag == "" {
		return nil
	}
	var out []*Sound
	for i := range lvl.Sounds {
		if lvl.Sounds[i].HasTag(tag) {
			out = append(out, &lvl.Sounds[i])
		}
	}
	return out
}

// UniqueSoundID mints an ID free across the whole level.
func UniqueSoundID(lvl *Level, base string) string {
	if base == "" {
		base = "sound"
	}
	return uniqueLevelID(lvl, base)
}

// ---------- the per-frame pass ----------

// soundFrames double-buffers the slice handed to audio.Publish. The mixer
// goroutine may still be reading last frame's while this frame's is built, so
// they cannot be the same slice — and allocating a fresh one every frame to
// avoid that would be a per-frame allocation in the render loop.
var soundFrames [2][]audio.VoiceParams
var soundFrameIdx int

// SoundUpdate starts, stops and repositions every placed sound, then publishes
// this frame's state to the mixer.
//
// It sits in the same slot as EmitterUpdate and for the same reasons: after
// RefreshFrames, so a sound riding a vehicle is read where it now actually is,
// and before the script tick, so a sound a script starts is audible in the
// block the mixer builds next.
//
// Like EmitterUpdate it is deliberately not gated on the editor being closed.
// A sound that went silent the moment the editor opened could not be placed by
// ear, which is most of how a positional sound gets placed at all. The
// `playing` flag is handed over anyway so the pass can tell play from
// authoring.
func SoundUpdate(lvl *Level, dt float32, playing bool, listenerPos, listenerFwd, listenerUp mgl32.Vec3) {
	_ = dt
	_ = playing

	soundFrameIdx ^= 1
	buf := soundFrames[soundFrameIdx][:0]

	if lvl != nil {
		reap := false
		for i := range lvl.Sounds {
			s := &lvl.Sounds[i]

			if !s.Sounding() {
				if !s.Voice.IsZero() {
					audio.Stop(s.Voice)
					s.Voice = 0
				}
				// A one-shot whose voice has ended is spent, and removing it
				// is what stops a script firing one per impact from filling
				// the level with silent placements.
				if s.OneShot {
					reap = true
				}
				continue
			}

			if s.Voice.IsZero() {
				s.Voice = audio.Play(audio.PlayOptions{
					Clip:     s.Clip,
					Bus:      s.AudioBus(),
					Gain:     s.Gain,
					Pitch:    s.Pitch,
					Loop:     s.Loop,
					Spatial:  true,
					Position: s.WorldPosition(lvl),
					Dir:      s.WorldDirection(lvl),
					Falloff:  s.Falloff(),
					Cone:     s.Cone(),
				})
				if s.Voice.IsZero() {
					// No device, a missing asset, or a full pool. All three
					// are reported elsewhere; retrying every frame would be a
					// decode attempt per frame, which the clip cache's
					// remembered failures already prevent.
					if s.OneShot {
						reap = true
					}
					continue
				}
			}

			buf = append(buf, audio.VoiceParams{
				ID:       s.Voice,
				Position: s.WorldPosition(lvl),
				Dir:      s.WorldDirection(lvl),
				Gain:     s.Gain,
				Pitch:    s.Pitch,
				Falloff:  s.Falloff(),
				Cone:     s.Cone(),
				Spatial:  true,
			})
		}
		if reap {
			reapSpentSounds(lvl)
		}
	}

	soundFrames[soundFrameIdx] = buf
	audio.Publish(audio.Frame{
		ListenerPos:     listenerPos,
		ListenerForward: listenerFwd,
		ListenerUp:      listenerUp,
		Voices:          buf,
	})
}

// reapSpentSounds drops finished one-shots. Compacts in place rather than
// rebuilding, so the common case of nothing to reap costs one pass.
func reapSpentSounds(lvl *Level) {
	out := lvl.Sounds[:0]
	for i := range lvl.Sounds {
		s := &lvl.Sounds[i]
		if s.OneShot && s.Voice.IsZero() {
			continue
		}
		out = append(out, *s)
	}
	lvl.Sounds = out
}

// StopAllSounds silences every voice and forgets every handle.
//
// Called wherever the level is rebuilt wholesale — undo, redo, the end of a
// play session. Without it, undoing the placement of a sound leaves it playing
// forever, attached to an object that no longer exists.
func StopAllSounds(lvl *Level) {
	audio.StopAll()
	if lvl == nil {
		return
	}
	for i := range lvl.Sounds {
		lvl.Sounds[i].Voice = 0
	}
}

// ---------- serialization ----------

// SoundFromRaw builds a sound from its level-file form.
func SoundFromRaw(r RawSound) (Sound, error) {
	data, err := ScriptDataFromRaw(r.RawScriptData, "sound "+r.ID)
	if err != nil {
		return Sound{}, err
	}
	def := audio.DefaultFalloff()
	s := Sound{
		ID:            r.ID,
		ScriptData:    data,
		Clip:          r.Clip,
		Position:      mgl32.Vec3{r.Position.X, r.Position.Y, r.Position.Z},
		Rotation:      mgl32.Vec3{r.Rotation.X, r.Rotation.Y, r.Rotation.Z},
		Enabled:       BoolDefault(r.Enabled, true),
		Playing:       BoolDefault(r.Playing, true),
		Loop:          BoolDefault(r.Loop, true),
		Gain:          float32Default(r.Gain, 1),
		Pitch:         float32Default(r.Pitch, 1),
		Bus:           r.Bus,
		RefDistance:   float32Default(r.RefDistance, def.Ref),
		MaxDistance:   float32Default(r.MaxDistance, def.Max),
		Rolloff:       float32Default(r.Rolloff, def.Rolloff),
		Atten:         r.Atten,
		ConeInner:     r.ConeInner,
		ConeOuter:     r.ConeOuter,
		ConeOuterGain: r.ConeOuterGain,
		Attach:        AttachFromRaw(r.Attach),
	}
	if s.Bus == "" {
		s.Bus = string(audio.BusEffects)
	}
	if s.Atten == "" {
		s.Atten = string(def.Model)
	}
	return s, nil
}

// SoundToRaw writes a sound back out. Every optional key is omitted at its
// default, so a level authored before a field existed round-trips unchanged.
func SoundToRaw(s *Sound) RawSound {
	def := audio.DefaultFalloff()
	return RawSound{
		ID:            s.ID,
		RawScriptData: ScriptDataToRaw(s.ScriptData),
		Clip:          s.Clip,
		Position:      RawVec3{X: s.Position.X(), Y: s.Position.Y(), Z: s.Position.Z()},
		Rotation:      RawVec3{X: s.Rotation.X(), Y: s.Rotation.Y(), Z: s.Rotation.Z()},
		Enabled:       BoolPtrIfFalse(s.Enabled),
		Playing:       BoolPtrIfFalse(s.Playing),
		Loop:          BoolPtrIfFalse(s.Loop),
		Gain:          float32PtrIfNot(s.Gain, 1),
		Pitch:         float32PtrIfNot(s.Pitch, 1),
		Bus:           stringIfNot(s.Bus, string(audio.BusEffects)),
		RefDistance:   float32PtrIfNot(s.RefDistance, def.Ref),
		MaxDistance:   float32PtrIfNot(s.MaxDistance, def.Max),
		Rolloff:       float32PtrIfNot(s.Rolloff, def.Rolloff),
		Atten:         stringIfNot(s.Atten, string(def.Model)),
		ConeInner:     s.ConeInner,
		ConeOuter:     s.ConeOuter,
		ConeOuterGain: s.ConeOuterGain,
		Attach:        AttachToRaw(s.Attach),
	}
}

// stringIfNot blanks a name that matches the default, so it is omitted.
func stringIfNot(v, def string) string {
	if v == def {
		return ""
	}
	return v
}
