package audio

// audio_api.go — the front door.
//
// This is the only file the rest of the engine needs to read to learn the
// package. Everything here is safe to call from the game thread, safe to call
// with no device open, and none of it blocks: a Play is one atomic increment
// and one non-blocking channel send, and the per-frame publish is one atomic
// pointer store.
//
// Nothing here decodes. ClipFor does disk I/O and can take tens of
// milliseconds, so a Play on a name that has never been heard before hitches
// one frame — which is why Preload exists and why the level loader calls it.

import (
	"github.com/go-gl/mathgl/mgl32"
)

// PlayOptions is everything about starting one sound. The zero value is
// usable: gain and pitch of 0 read as 1, so the common case is two fields.
type PlayOptions struct {
	// Clip names an asset under sounds/, without the extension.
	Clip string

	Bus      Bus
	Gain     float32
	Pitch    float32
	Loop     bool
	Priority Priority
	// FadeIn ramps the start over this many seconds. For music.
	FadeIn float32

	// Spatial places the sound in the world. Left false, it plays at full
	// gain in both ears — which is what music and UI sounds want.
	Spatial  bool
	Position mgl32.Vec3
	Velocity mgl32.Vec3
	// Dir is the direction a cone points. Zero is omnidirectional.
	Dir     mgl32.Vec3
	Falloff Falloff
	Cone    Cone
}

// Play starts a sound and returns a handle to it.
//
// It returns a zero handle when there is no device, when the clip will not
// load, or when the pool is full of higher-priority sounds. A zero handle is
// safe to pass to every other function here, so callers do not need to check.
func Play(o PlayOptions) VoiceID {
	d := active.Load()
	if d == nil {
		return 0
	}
	clip, err := ClipFor(o.Clip)
	if err != nil || clip == nil {
		return 0
	}

	if o.Falloff == (Falloff{}) {
		o.Falloff = DefaultFalloff()
	}
	if o.Gain == 0 {
		o.Gain = 1
	}
	if o.Pitch == 0 {
		o.Pitch = 1
	}
	if o.Bus == "" {
		o.Bus = BusEffects
	}

	id := newVoiceID()
	if !d.send(command{kind: cmdPlay, id: id, req: PlayRequest{
		Clip:     clip,
		Bus:      o.Bus,
		Gain:     o.Gain,
		Pitch:    o.Pitch,
		Loop:     o.Loop,
		Priority: o.Priority,
		FadeIn:   o.FadeIn,
		Spatial:  o.Spatial,
		Position: o.Position,
		Velocity: o.Velocity,
		Dir:      o.Dir,
		Falloff:  o.Falloff,
		Cone:     o.Cone,
	}}) {
		return 0
	}
	return id
}

// PlaySound is the short form for a one-shot in the world.
func PlaySound(clip string, pos mgl32.Vec3, gain float32) VoiceID {
	return Play(PlayOptions{Clip: clip, Gain: gain, Spatial: true, Position: pos})
}

// PlayGlobal is the short form for a sound with no position — music, UI.
func PlayGlobal(clip string, bus Bus, gain float32, loop bool) VoiceID {
	return Play(PlayOptions{Clip: clip, Bus: bus, Gain: gain, Loop: loop})
}

// Stop ends a voice over a short ramp, so it does not click.
func Stop(id VoiceID) { stopWithFade(id, 0) }

// FadeOut ends a voice over the given number of seconds.
func FadeOut(id VoiceID, seconds float32) { stopWithFade(id, seconds) }

func stopWithFade(id VoiceID, seconds float32) {
	if id == 0 {
		return
	}
	if d := active.Load(); d != nil {
		d.send(command{kind: cmdStop, id: id, fade: seconds})
	}
}

// StopAll silences everything. Used when a level is swapped out — see
// levelio.ApplyRawLevel, where an undo that deletes a sound would otherwise
// leave it playing forever, attached to an object that no longer exists.
func StopAll() {
	if d := active.Load(); d != nil {
		d.send(command{kind: cmdStopAll})
	}
}

// SetLoop switches a live voice between one-shot and sustained.
func SetLoop(id VoiceID, loop bool) {
	if id == 0 {
		return
	}
	if d := active.Load(); d != nil {
		d.send(command{kind: cmdSetLoop, id: id, flag: loop})
	}
}

// send is the non-blocking hand-off. A full channel drops the command and
// counts it rather than parking the render loop: 512 sounds requested inside
// one frame is already a bug, and a dropped footstep is a better symptom than
// a stalled frame.
func (d *device) send(c command) bool {
	select {
	case d.mix.cmds <- c:
		return true
	default:
		d.mix.dropped.Add(1)
		return false
	}
}

// ---------- the per-frame publish ----------

// Frame is one frame's worth of continuously-changing state, handed over in
// one piece.
//
// It is published through an atomic pointer rather than sent as a command, so
// the newest always wins and a burst of Plays cannot leave a stale listener
// queued behind them. The mixer keeps rendering against the last one it saw,
// which is why a long frame produces slightly stale positions rather than a
// dropout.
type Frame struct {
	ListenerPos      mgl32.Vec3
	ListenerForward  mgl32.Vec3
	ListenerUp       mgl32.Vec3
	ListenerVelocity mgl32.Vec3
	// Voices is every live positional voice's state this frame. The caller
	// owns the slice until Publish; after that it must not touch it again.
	Voices []VoiceParams
}

// Publish hands this frame's state to the mixer. It never blocks and never
// allocates.
//
// The slice must not be reused by the caller afterwards — the mixer may still
// be reading it. world.SoundUpdate double-buffers for this reason.
func Publish(f Frame) {
	d := active.Load()
	if d == nil {
		return
	}
	d.mix.state.Store(&frameState{
		listener:    BasisFrom(f.ListenerPos, f.ListenerForward, f.ListenerUp),
		listenerVel: f.ListenerVelocity,
		voices:      f.Voices,
	})
}

// ---------- assets ----------

// Preload decodes clips ahead of time, so the first Play does not hitch.
// Called by the level loader for every sound a level places.
func Preload(names ...string) {
	for _, n := range names {
		if n != "" {
			ClipFor(n)
		}
	}
}
