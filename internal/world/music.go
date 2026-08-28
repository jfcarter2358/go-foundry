package world

// music.go — the level's global track.
//
// Music is the one sound with no position, so it is not a Sound with a flag:
// nearly every field on Sound is about where it is and how it carries, and
// none of that means anything here. It follows SpawnPoint's shape instead —
// at most one per level, no ID, written under a top-level key, and a pointer
// so that "this level has no music" and "this level has music at defaults"
// stay distinguishable.
//
// It is played through the same mixer as everything else, on its own bus, so
// the player's music slider reaches it and nothing else.

import "foundry/internal/audio"

// Music is a level's global track.
type Music struct {
	// Track names a file under sounds/, without the extension.
	Track string
	// Gain multiplies the recording's own level; 1 is as authored.
	Gain float32
	// Loop is almost always true — a track that stops and leaves silence is
	// rarely what is wanted — but a one-shot sting is a legitimate thing to
	// place.
	Loop bool
	// Fade is the crossfade in seconds when a script switches track, and the
	// ramp in when this one starts.
	Fade float32

	// Voice is the mixer handle. Runtime-only, never serialized.
	Voice audio.VoiceID
}

// NewMusic returns a track at its own level, looping.
func NewMusic(track string) Music {
	return Music{Track: track, Gain: 1, Loop: true, Fade: 1}
}

// MusicUpdate starts the level's track if it is not already playing.
//
// Called once a frame beside SoundUpdate. Starting here rather than at level
// load means a track whose file appears later — an author dropping one in
// while the editor is open — begins on its own, and it costs one comparison a
// frame when there is nothing to do.
func MusicUpdate(lvl *Level) {
	if lvl == nil || lvl.Music == nil {
		return
	}
	m := lvl.Music
	if m.Track == "" || !m.Voice.IsZero() {
		return
	}
	m.Voice = audio.Play(audio.PlayOptions{
		Clip:   m.Track,
		Bus:    audio.BusMusic,
		Gain:   m.Gain,
		Loop:   m.Loop,
		FadeIn: m.Fade,
		// Deliberately not spatial: music has no position, and giving it one
		// would make it pan as the player turns their head.
		Spatial: false,
	})
}

// SetMusic switches the level's track, crossfading out whatever was playing.
//
// Returns false when there is nothing to do, so a script setting the track it
// is already playing does not restart it — which would be an audible stutter
// for what reads as a no-op.
func SetMusic(lvl *Level, track string, fade float32) bool {
	if lvl == nil {
		return false
	}
	if lvl.Music != nil && lvl.Music.Track == track && !lvl.Music.Voice.IsZero() {
		return false
	}
	StopMusic(lvl, fade)
	if track == "" {
		return true
	}
	m := NewMusic(track)
	if fade > 0 {
		m.Fade = fade
	}
	lvl.Music = &m
	MusicUpdate(lvl)
	return true
}

// StopMusic fades the current track out and forgets it.
func StopMusic(lvl *Level, fade float32) {
	if lvl == nil || lvl.Music == nil {
		return
	}
	if !lvl.Music.Voice.IsZero() {
		if fade > 0 {
			audio.FadeOut(lvl.Music.Voice, fade)
		} else {
			audio.Stop(lvl.Music.Voice)
		}
		lvl.Music.Voice = 0
	}
}

// ---------- serialization ----------

// MusicFromRaw reads the optional music block. A nil in is a level with no
// music and stays nil, rather than becoming a Music with an empty track.
func MusicFromRaw(r *RawMusic) *Music {
	if r == nil {
		return nil
	}
	return &Music{
		Track: r.Track,
		Gain:  float32Default(r.Gain, 1),
		Loop:  BoolDefault(r.Loop, true),
		Fade:  r.Fade,
	}
}

// MusicToRaw writes it back, omitting every key at its default.
func MusicToRaw(m *Music) *RawMusic {
	if m == nil || m.Track == "" {
		return nil
	}
	return &RawMusic{
		Track: m.Track,
		Gain:  float32PtrIfNot(m.Gain, 1),
		Loop:  BoolPtrIfFalse(m.Loop),
		Fade:  m.Fade,
	}
}
