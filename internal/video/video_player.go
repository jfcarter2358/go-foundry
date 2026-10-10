package video

// video_player.go — one clip, one clock.
//
// A player is the whole of "this video is playing": where it has got to, which
// frame that means, whether the soundtrack is running, and what happens when it
// reaches the end. It holds no GL object and no file handle it did not open
// itself, so a test can run a two-hour clip through it in a millisecond by
// calling Advance with a large dt.
//
// The clock is accumulated wall-clock time, not the audio device's cursor.
// Making the soundtrack the master would be more correct in principle, and it
// is what a media player does; it needs the mixer to publish a live sample
// position, and it buys accuracy that nothing here can perceive. Advance is fed
// real elapsed seconds, so a hitch moves picture and sound by the same amount
// and the two stay together for as long as a cutscene lasts.

import (
	"image"

	"foundry/internal/audio"
)

// Player walks a decoded track against a clock.
//
// Not safe for concurrent use, and deliberately not made so: a player belongs
// to the frame loop that ticks it, in the way a ui.Screen belongs to the stack
// holding it.
type Player struct {
	// Source is the asset name this was opened from, for logs and for the
	// registry's "has the widget been repointed" check.
	Source string

	dec decoder
	trk Track
	// err is the reason this player has no picture. Kept rather than returned
	// so that a source that will not open fails once and then draws nothing,
	// instead of hitting the disk every frame for as long as the screen is up.
	err error

	playing bool
	loop    bool
	muted   bool
	volume  float32

	time float64
	// at is the frame index in `frame`, or -1 before the first decode.
	at    int
	frame *image.RGBA
	rev   uint64
	ended bool

	voice audio.VoiceID
}

// Open decodes a video's headers and returns a player parked on its first
// frame, paused.
//
// It never returns nil. A source that cannot be read comes back as a player
// with Err set: every method on it is safe, it draws nothing, and it reports
// zero duration — which is what lets a caller treat "no such file" and "not
// started yet" the same way, because a screen author cannot tell them apart
// either.
func Open(source string) *Player {
	p := &Player{Source: source, volume: 1, at: -1}

	path := VideoPath(source)
	if path == "" {
		p.err = &missingError{source}
		logf("%s: no file under %s/ (looked for %v)", source, VideoDirName, VideoExts)
		return p
	}
	dec, err := open(source, path)
	if err != nil {
		p.err = err
		logf("%v", err)
		return p
	}
	p.dec = dec
	p.trk = *dec.Track()
	p.decodeTo(0)
	return p
}

// missingError is "there is no such video", separated from a decode failure so
// that a caller can tell an unset source from a broken one.
type missingError struct{ name string }

func (e *missingError) Error() string { return "no video named " + e.name }

// Err is why this player has no picture, or nil.
func (p *Player) Err() error { return p.err }

// Size is the picture's dimensions in pixels, zero when there is none.
func (p *Player) Size() (w, h int) { return p.trk.Width, p.trk.Height }

// Duration is how long the clip runs, in seconds.
func (p *Player) Duration() float32 { return float32(p.trk.Duration) }

// Time is where playback has got to, in seconds.
func (p *Player) Time() float32 { return float32(p.time) }

// Playing reports whether the clock is running.
func (p *Player) Playing() bool { return p.playing }

// Ended reports whether a non-looping clip has run out. Cleared by a seek or by
// starting it again, so it means "is at the end" rather than "has ever been".
func (p *Player) Ended() bool { return p.ended }

// Frame is the picture that should be on screen, or nil.
//
// The image is owned by the decoder and is overwritten by the next Advance, so
// a caller keeping it across frames must copy or upload it first. Rev is how a
// caller knows an upload is even needed.
func (p *Player) Frame() *image.RGBA { return p.frame }

// Rev counts how many times Frame has changed. A texture whose uploaded Rev
// matches this one is already showing the right picture, which is what keeps a
// paused video — or a twelve-frames-a-second one on a 144 Hz display — from
// re-uploading a megabyte every frame.
func (p *Player) Rev() uint64 { return p.rev }

// Loop reports and SetLoop sets whether the clip restarts at the end.
func (p *Player) Loop() bool { return p.loop }

// SetLoop switches looping.
//
// It is a flag and nothing more: switching it on does not start a clip that has
// already stopped, because "loop this" is a statement about what happens at the
// end rather than an instruction to play. Play does that, and on a finished
// clip it rewinds first.
func (p *Player) SetLoop(on bool) { p.loop = on }

// SetVolume sets the soundtrack's level, 0 to 1.
func (p *Player) SetVolume(v float32) {
	v = clamp01(v)
	if p.volume == v {
		return
	}
	p.volume = v
	p.applyGain()
}

// SetMuted silences the soundtrack without stopping it, so unmuting picks up
// where the picture is rather than restarting the audio.
func (p *Player) SetMuted(m bool) {
	if p.muted == m {
		return
	}
	p.muted = m
	p.applyGain()
}

// Play starts, or resumes, the clock. Starting a clip that has run out rewinds
// it: "play" on a finished video means play it, not sit at the end.
func (p *Player) Play() {
	if p.ended {
		p.Seek(0)
	}
	if p.playing {
		return
	}
	p.playing = true
	p.startSound()
}

// Pause stops the clock and the soundtrack, leaving the picture where it is.
func (p *Player) Pause() {
	if !p.playing {
		return
	}
	p.playing = false
	p.stopSound()
}

// Seek moves to a time in seconds, clamped into the clip, and decodes the frame
// there. It keeps playing if it was playing, restarting the soundtrack from the
// new position.
func (p *Player) Seek(sec float32) {
	t := float64(sec)
	if t < 0 {
		t = 0
	}
	if t > p.trk.Duration {
		t = p.trk.Duration
	}
	p.stopSound()
	p.time = t
	p.ended = false
	p.decodeTo(p.trk.FrameAt(t))
	if p.playing {
		p.startSound()
	}
}

// Advance moves the clock on by dt seconds and decodes whatever frame that
// lands on.
//
// A dt long enough to cross the end of a looping clip wraps once and lands
// where it should, rather than clamping — a tab-out that produces a two-second
// frame should not desynchronise a looping background.
func (p *Player) Advance(dt float32) {
	if !p.playing || p.err != nil || p.trk.Duration <= 0 {
		return
	}
	p.time += float64(dt)

	if p.time >= p.trk.Duration {
		if !p.loop {
			p.time = p.trk.Duration
			p.decodeTo(p.trk.Count() - 1)
			p.playing = false
			p.ended = true
			p.stopSound()
			return
		}
		p.time -= p.trk.Duration * float64(int(p.time/p.trk.Duration))
		// The soundtrack is one clip playing once, so a wrap has to start it
		// again. Restarting on the wrap rather than looping the voice keeps
		// picture and sound wrapping at the same instant even when the audio
		// is fractionally shorter than the video, which a muxer's padding
		// routinely makes it.
		p.stopSound()
		p.startSound()
	}
	p.decodeTo(p.trk.FrameAt(p.time))
}

// Close releases the file and silences the soundtrack.
func (p *Player) Close() {
	p.stopSound()
	if p.dec != nil {
		p.dec.Close()
		p.dec = nil
	}
	p.frame = nil
	p.playing = false
}

// decodeTo decodes a frame index if it is not the one already in hand.
func (p *Player) decodeTo(i int) {
	if p.dec == nil || i == p.at || i < 0 || i >= p.trk.Count() {
		return
	}
	img, err := p.dec.Frame(i)
	if err != nil {
		// One bad frame is not a broken video: hold the last good picture and
		// carry on, so a single corrupt chunk is a stutter rather than a black
		// screen for the rest of the clip.
		logf("%s: %v", p.Source, err)
		p.at = i
		return
	}
	p.at = i
	p.frame = img
	p.rev++
}

// ---------- the soundtrack ----------

// startSound plays the clip from the current time.
//
// The offset is a sub-slice of the decoded samples rather than a start position
// the mixer would have to understand: a Clip is immutable and its Samples are
// never written, so a second Clip sharing the tail of the first is free and
// carries no risk. That is the whole reason seeking a video's audio needs no
// change to the mixer at all.
func (p *Player) startSound() {
	clip := p.trk.Sound
	if clip == nil || clip.Channels == 0 {
		return
	}
	off := int(p.time * float64(clip.Rate))
	if off < 0 {
		off = 0
	}
	if off >= clip.Frames() {
		return
	}
	from := &audio.Clip{
		Name:     clip.Name,
		Samples:  clip.Samples[off*clip.Channels:],
		Rate:     clip.Rate,
		Channels: clip.Channels,
	}
	// The music bus, because a soundtrack is what the music slider is for, and
	// high priority because a cutscene losing its dialogue to a footstep would
	// be the pool working exactly as intended and still be wrong.
	p.voice = audio.PlayClip(from, audio.PlayOptions{
		Bus:      audio.BusMusic,
		Gain:     p.gain(),
		Priority: audio.PriorityHigh,
	})
}

func (p *Player) stopSound() {
	if p.voice != 0 {
		audio.Stop(p.voice)
		p.voice = 0
	}
}

func (p *Player) applyGain() {
	if p.voice != 0 {
		audio.SetGain(p.voice, p.gain())
	}
}

func (p *Player) gain() float32 {
	if p.muted {
		return audio.SilentGain
	}
	if p.volume <= 0 {
		return audio.SilentGain
	}
	return p.volume
}

func clamp01(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
