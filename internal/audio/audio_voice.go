package audio

// audio_voice.go — the voice table, and the two channels the game thread
// talks to it through.
//
// The concurrency rule this file exists to enforce: the mixer goroutine owns
// every voice outright, and the game thread never touches one. It communicates
// two ways, neither of which can block it:
//
//   - Discrete events (start this, stop that) go down a buffered command
//     channel with a non-blocking send. A full channel drops the command and
//     counts it, because stalling the render loop to guarantee a footstep is
//     the wrong trade.
//   - Continuous state (where everything is this frame, where the listener is)
//     is published as one immutable snapshot per frame through an atomic
//     pointer. Latest wins; there is no queue to fall behind.
//
// The consequence worth knowing: a 50 ms frame hitch does not produce a
// dropout, because the mixer keeps running against the last snapshot it saw.
// Positions go a few milliseconds stale, which is inaudible. A gap is not.

import (
	"math"
	"sync/atomic"

	"github.com/go-gl/mathgl/mgl32"
)

// VoiceID is a handle to a playing sound. Zero is "no voice".
//
// Handles are drawn from one monotonic counter and never reused. That matters
// because a world.Sound holds its handle across frames while the mixer is free
// to steal the slot underneath it: with reuse, a stale Stop would silence
// whatever inherited the slot, and a stale gain change would steer it. With a
// counter, a command naming a handle the mixer no longer holds is simply
// dropped — which is exactly the right meaning for "stop a sound that already
// finished".
type VoiceID uint64

var nextVoiceID atomic.Uint64

// newVoiceID mints a handle. Called on the game thread, and the only thing
// Play has to do before it can return one.
func newVoiceID() VoiceID { return VoiceID(nextVoiceID.Add(1)) }

// IsZero reports a handle that names nothing.
func (v VoiceID) IsZero() bool { return v == 0 }

// MaxVoices is the size of the pool. Past this a new sound has to displace an
// existing one; see steal.
const MaxVoices = 32

// Priority orders what survives when the pool is full.
type Priority uint8

const (
	PriorityLow    Priority = 0
	PriorityNormal Priority = 1
	PriorityHigh   Priority = 2
)

// PlayRequest is everything needed to start a voice.
type PlayRequest struct {
	Clip *Clip
	// Gen, when set, is played instead of Clip: the voice pulls each block from
	// it rather than reading a decoded buffer. See Generator.
	Gen      Generator
	Bus      Bus
	Gain     float32
	Pitch    float32
	Loop     bool
	Priority Priority

	// Spatial says whether the sound is placed in the world. Music and UI
	// sounds are not, and go straight to both ears at full gain.
	Spatial  bool
	Position mgl32.Vec3
	Velocity mgl32.Vec3
	Dir      mgl32.Vec3
	Falloff  Falloff
	Cone     Cone

	// FadeIn is a ramp in seconds applied at the start, for music.
	FadeIn float32
}

// VoiceParams is the per-frame state of one live voice, republished every
// frame as part of the snapshot.
type VoiceParams struct {
	ID       VoiceID
	Position mgl32.Vec3
	Velocity mgl32.Vec3
	Dir      mgl32.Vec3
	Gain     float32
	Pitch    float32
	Falloff  Falloff
	Cone     Cone
	Spatial  bool
}

// frameState is one frame's worth of continuous state. Treated as immutable
// once published: the game thread builds a fresh one each frame rather than
// mutating the last, because the mixer may still be reading it.
type frameState struct {
	listener    ListenerBasis
	listenerVel mgl32.Vec3
	voices      []VoiceParams
}

// ---------- commands ----------

type cmdKind uint8

const (
	cmdPlay cmdKind = iota
	cmdStop
	cmdStopAll
	cmdSetLoop
	cmdSetGain
)

type command struct {
	kind cmdKind
	id   VoiceID
	req  PlayRequest
	flag bool
	// fade is the stop ramp in seconds; 0 takes the default declick.
	fade float32
	// gain is cmdSetGain's new level, already clamped away from the zero
	// sentinel by SetGain.
	gain float32
}

// ---------- the voice ----------

type voice struct {
	active bool
	id     VoiceID

	clip *Clip
	gen  Generator
	bus  Bus
	loop bool
	prio Priority

	// cursor is a fractional frame index into clip.Samples. Fractional
	// because the step folds together the clip/device rate difference, pitch
	// and doppler, and forcing it to integers would detune everything.
	cursor float64

	// Target gains for each ear, recomputed per block from the snapshot, and
	// the smoothed values the mixer actually applies per sample. The gap
	// between them is what removes clicks — see mixVoice.
	targetL, targetR float32
	curL, curR       float32
	// primed is false until the first block, when cur jumps to target rather
	// than ramping up from zero. Without it every sound fades in over the
	// declick window, which blunts percussive effects.
	primed bool

	// stopping voices ramp to silence and then free themselves.
	stopping  bool
	stopRate  float32
	fadeIn    float32
	fadeInPos float32

	params VoiceParams
}

// ---------- the mixer ----------

type mixer struct {
	rate     int
	channels int

	voices [MaxVoices]voice

	// state is the last snapshot the game thread published.
	state atomic.Pointer[frameState]

	// cmds carries discrete events. Buffered deep enough that a burst of
	// impacts in one frame does not drop any.
	cmds chan command

	// dropped counts commands lost to a full channel, surfaced in Stats so a
	// dropped footstep is diagnosable rather than mysterious.
	dropped   atomic.Uint64
	underruns atomic.Uint64
	// liveCount and lastPeak are published by the mixer for the game thread's
	// debug overlay. Read without a lock and therefore possibly a block
	// stale, which is the right trade for a number that is only ever drawn.
	liveCount atomic.Int64
	lastPeak  atomic.Uint32
	// pending maps a handle the game thread already minted to its slot.
	byID map[VoiceID]int

	scratch []float32

	// limiterGain and peak are the master limiter's state and its last
	// reading. Peak is surfaced in Stats as a free level meter, which is what
	// tells an author their clip is 12 dB hotter than everything else.
	limiterGain float32
	peak        float32
}

func newMixer(rate, channels int) *mixer {
	return &mixer{
		rate:        rate,
		channels:    channels,
		cmds:        make(chan command, 512),
		byID:        map[VoiceID]int{},
		limiterGain: 1,
	}
}

// allocate picks a slot for a new voice, stealing if the pool is full.
//
// Stealing takes the quietest voice of equal or lower priority. A request that
// cannot find one is refused rather than served: cutting off the sound the
// player is currently listening to in order to start a quieter one is a worse
// outcome than dropping the new sound, and it is the failure mode that makes
// a busy scene sound broken rather than merely busy.
func (m *mixer) allocate(prio Priority) int {
	for i := range m.voices {
		if !m.voices[i].active {
			return i
		}
	}
	best, bestGain := -1, float32(1e30)
	for i := range m.voices {
		v := &m.voices[i]
		if v.prio > prio {
			continue
		}
		// Looping ambience is a better candidate than a one-shot mid-transient
		// at the same level, because it will simply come back next frame.
		g := v.curL + v.curR
		if v.loop {
			g *= 0.5
		}
		if g < bestGain {
			best, bestGain = i, g
		}
	}
	return best
}

func (m *mixer) freeVoice(i int) {
	delete(m.byID, m.voices[i].id)
	m.voices[i] = voice{}
}

// drainCommands applies everything queued since the last block.
func (m *mixer) drainCommands() {
	for {
		select {
		case c := <-m.cmds:
			m.apply(c)
		default:
			return
		}
	}
}

func (m *mixer) apply(c command) {
	switch c.kind {
	case cmdPlay:
		if c.req.Gen == nil && (c.req.Clip == nil || c.req.Clip.Frames() == 0) {
			return
		}
		// A cap on sustained voices, so a script bug that starts a loop every
		// frame cannot eat the pool and silence everything else. One-shots are
		// unaffected, which is the common case.
		if c.req.Loop && m.loopingVoices() >= maxLoopingVoices {
			m.dropped.Add(1)
			return
		}
		slot := m.allocate(c.req.Priority)
		if slot < 0 {
			m.dropped.Add(1)
			return
		}
		if m.voices[slot].active {
			m.freeVoice(slot)
		}
		m.voices[slot] = voice{
			active: true,
			id:     c.id,
			clip:   c.req.Clip,
			gen:    c.req.Gen,
			bus:    c.req.Bus,
			loop:   c.req.Loop,
			prio:   c.req.Priority,
			fadeIn: c.req.FadeIn,
			params: VoiceParams{
				ID:       c.id,
				Position: c.req.Position,
				Velocity: c.req.Velocity,
				Dir:      c.req.Dir,
				Gain:     c.req.Gain,
				Pitch:    c.req.Pitch,
				Falloff:  c.req.Falloff,
				Cone:     c.req.Cone,
				Spatial:  c.req.Spatial,
			},
		}
		m.byID[c.id] = slot

	case cmdStop:
		if i, ok := m.byID[c.id]; ok {
			m.beginStop(i, c.fade)
		}

	case cmdStopAll:
		for i := range m.voices {
			if m.voices[i].active {
				m.beginStop(i, c.fade)
			}
		}

	case cmdSetLoop:
		if i, ok := m.byID[c.id]; ok {
			m.voices[i].loop = c.flag
		}

	case cmdSetGain:
		// Written into params rather than into a field of its own, so that a
		// voice which is *also* in the per-frame snapshot — a placed sound —
		// has its level overwritten by the next publish. Only a voice nobody
		// republishes, which is what a video's soundtrack is, keeps it.
		if i, ok := m.byID[c.id]; ok {
			m.voices[i].params.Gain = c.gain
		}
	}
}

// declickSeconds is the shortest ramp allowed on a stop. Cutting a waveform
// dead at a non-zero sample is a step discontinuity, and a step is a click —
// which is much more noticeable than the 5 ms of tail this costs.
const declickSeconds = 0.005

func (m *mixer) beginStop(i int, fade float32) {
	v := &m.voices[i]
	if !v.active {
		return
	}
	if fade < declickSeconds {
		fade = declickSeconds
	}
	v.stopping = true
	v.stopRate = 1 / (fade * float32(m.rate))
}

// liveApprox and peakApprox are the game thread's view of the two numbers the
// debug overlay wants. Published by the mixer once a block.
func (m *mixer) liveApprox() int { return int(m.liveCount.Load()) }
func (m *mixer) peakApprox() float32 {
	return math.Float32frombits(m.lastPeak.Load())
}

// loopingVoices counts sustained voices, for the cap. Mixer-goroutine only.
func (m *mixer) loopingVoices() int {
	n := 0
	for i := range m.voices {
		if m.voices[i].active && m.voices[i].loop {
			n++
		}
	}
	return n
}

// live reports how many voices are sounding. Mixer-goroutine only.
func (m *mixer) live() int {
	n := 0
	for i := range m.voices {
		if m.voices[i].active {
			n++
		}
	}
	return n
}
