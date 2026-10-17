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
//
// A track is either a recording (sounds/<name>.ogg and friends) or a tracker
// song (sounds/<name>.ftrk). A song is the same single voice on the music bus,
// but it is generated live by internal/tracker, so scripts can steer it while
// it plays — switch section on a beat, ramp a param, mute a channel. What they
// asked for is mirrored here (Section, Params, Mutes) so a save game restores
// the music the player was hearing, not the one the level started with.
//
// A score (sounds/<name>.fscore, internal/score) is a third kind, steered
// exactly as a song is: its rehearsal marks are the sections, its lines the
// channels, and its params bind to line volumes, pans, sends and filters.
// Its scheduler (internal/score/play) is the music-bus voice in place of the
// tracker's. When a song and a score share a name, the song wins.

import (
	"foundry/internal/audio"
	"foundry/internal/score"
	"foundry/internal/score/play"
	"foundry/internal/tracker"
)

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

	// Section is the song section (or score rehearsal mark) to start at.
	Section string
	// Params are song params as last set — by the level file at start, then
	// by scripts. Mutes is the channels (a score's lines) a script has muted.
	Params map[string]float32
	Mutes  map[string]bool

	// Voice is the mixer handle. Runtime-only, never serialized.
	Voice audio.VoiceID
	// Song is the tracker player when the track is a song. Runtime-only.
	Song *tracker.Player
	// Score is the score's player when the track is a score. Runtime-only.
	Score *play.Player
	// Resume starts a song at ResumeOrder/ResumeRow (a score at ResumeTick)
	// rather than at Section, for a save game. Runtime-only, consumed by the
	// next start.
	Resume                 bool
	ResumeOrder, ResumeRow int
	ResumeTick             int64
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
	if tracker.IsSong(m.Track) {
		startSong(m)
		return
	}
	if score.IsScore(m.Track) {
		startScore(m)
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

// startSong plays a tracker song. Without a device it does nothing, so the
// once-a-frame retry costs a stat rather than a player per frame.
func startSong(m *Music) {
	if !audio.Available() {
		return
	}
	p, id, err := tracker.Play(m.Track, tracker.PlayOptions{
		Section: m.Section, Gain: m.Gain, FadeIn: m.Fade, Params: m.Params,
		Resume: m.Resume, Order: m.ResumeOrder, Row: m.ResumeRow,
	})
	if err != nil || id.IsZero() {
		return
	}
	for ch, on := range m.Mutes {
		if on {
			p.Mute(ch, true, 0)
		}
	}
	if musicPaused {
		p.Pause(true)
	}
	m.Song, m.Voice = p, id
	m.Resume = false
}

// startScore plays a score, as startSong plays a song.
func startScore(m *Music) {
	if !audio.Available() {
		return
	}
	p, id, err := play.Play(m.Track, play.PlayOptions{
		Section: m.Section, Gain: m.Gain, FadeIn: m.Fade, Params: m.Params,
		Resume: m.Resume, Tick: m.ResumeTick,
	})
	if err != nil || id.IsZero() {
		return
	}
	for line, on := range m.Mutes {
		if on {
			p.Mute(line, true, 0)
		}
	}
	if musicPaused {
		p.Pause(true)
	}
	m.Score, m.Voice = p, id
	m.Resume = false
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
	lvl.Music.Song = nil
	lvl.Music.Score = nil
}

// MusicSection names the section the level's song (or score) is playing,
// or "".
func MusicSection(lvl *Level) string {
	if p := ScorePlayer(lvl); p != nil {
		pos := p.Position()
		perf := p.Performance()
		if pos.Section >= 0 && pos.Section < len(perf.Sections) {
			return perf.Sections[pos.Section].Name
		}
		return ""
	}
	if lvl == nil || lvl.Music == nil || lvl.Music.Song == nil {
		return ""
	}
	pos := lvl.Music.Song.Position()
	s := lvl.Music.Song.Song()
	if pos.Section >= 0 && pos.Section < len(s.Sections) {
		return s.Sections[pos.Section].Name
	}
	return ""
}

// ---------- serialization ----------

// MusicFromRaw reads the optional music block. A nil in is a level with no
// music and stays nil, rather than becoming a Music with an empty track.
func MusicFromRaw(r *RawMusic) *Music {
	if r == nil {
		return nil
	}
	m := &Music{
		Track:   r.Track,
		Gain:    float32Default(r.Gain, 1),
		Loop:    BoolDefault(r.Loop, true),
		Fade:    r.Fade,
		Section: r.Section,
	}
	if len(r.Params) > 0 {
		m.Params = make(map[string]float32, len(r.Params))
		for k, v := range r.Params {
			m.Params[k] = v
		}
	}
	return m
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
		// Scripts write Params during play too, but a play session restores
		// the level before the editor can save it, so this is the authored set.
		Section: m.Section,
		Params:  m.Params,
	}
}

// ---------- steering a song ----------

// PlaySong switches the level's track to a tracker song (or a score)
// starting at section. When that one is already playing it changes section
// on the next beat instead of restarting, and reports false.
func PlaySong(lvl *Level, name, section string, fade float32) bool {
	if lvl == nil {
		return false
	}
	if m := lvl.Music; m != nil && m.Track == name && !m.Voice.IsZero() && (m.Song != nil || m.Score != nil) {
		if section != "" {
			m.Section = section
			if m.Song != nil {
				m.Song.Section(section, tracker.QuantBeat)
			} else {
				m.Score.Section(section, tracker.QuantBeat)
			}
		}
		return false
	}
	StopMusic(lvl, fade)
	m := NewMusic(name)
	if fade > 0 {
		m.Fade = fade
	}
	m.Section = section
	lvl.Music = &m
	MusicUpdate(lvl)
	return true
}

// SongPlayer is the level's tracker player, or nil when no song is playing.
func SongPlayer(lvl *Level) *tracker.Player {
	if lvl == nil || lvl.Music == nil {
		return nil
	}
	return lvl.Music.Song
}

// ScorePlayer is the level's score player, or nil when no score is playing.
func ScorePlayer(lvl *Level) *play.Player {
	if lvl == nil || lvl.Music == nil {
		return nil
	}
	return lvl.Music.Score
}

// MusicSteerable reports whether the level's track is a song or a score
// that is playing — what the song_ verbs can steer.
func MusicSteerable(lvl *Level) bool {
	return SongPlayer(lvl) != nil || ScorePlayer(lvl) != nil
}

// SetSongSection queues a section change and remembers it for a save.
func SetSongSection(lvl *Level, name string, q tracker.Quantize) bool {
	if sp := ScorePlayer(lvl); sp != nil {
		if sp.Performance().SectionIndex(name) < 0 {
			return false
		}
		lvl.Music.Section = name
		return sp.Section(name, q)
	}
	p := SongPlayer(lvl)
	if p == nil || p.Song().SectionIndex(name) < 0 {
		return false
	}
	lvl.Music.Section = name
	return p.Section(name, q)
}

// SetSongParam ramps a song param and remembers it for a save.
func SetSongParam(lvl *Level, name string, v, secs float32) bool {
	sp, p := ScorePlayer(lvl), SongPlayer(lvl)
	switch {
	case sp != nil:
		if sp.Score().ParamIndex(name) < 0 {
			return false
		}
	case p == nil || p.Song().ParamIndex(name) < 0:
		return false
	}
	if lvl.Music.Params == nil {
		lvl.Music.Params = map[string]float32{}
	}
	lvl.Music.Params[name] = v
	if sp != nil {
		return sp.SetParam(name, v, secs)
	}
	return p.SetParam(name, v, secs)
}

// SetSongMute fades a channel out or in and remembers it for a save.
//
// For a score, channel is a line, by name or 1-based number.
func SetSongMute(lvl *Level, channel string, on bool, secs float32) bool {
	sp, p := ScorePlayer(lvl), SongPlayer(lvl)
	switch {
	case sp != nil:
		if sp.Score().LineIndex(channel) < 0 {
			return false
		}
	case p == nil || p.Song().ChannelIndex(channel) < 0:
		return false
	}
	if lvl.Music.Mutes == nil {
		lvl.Music.Mutes = map[string]bool{}
	}
	lvl.Music.Mutes[channel] = on
	if sp != nil {
		return sp.Mute(channel, on, secs)
	}
	return p.Mute(channel, on, secs)
}

// musicPaused is the pause state last sent to the song, so a paused game does
// not send a command a frame.
var musicPaused bool

// MusicPause freezes or resumes the level's song (or score) with the level's
// hard pause.
func MusicPause(lvl *Level, paused bool) {
	if paused == musicPaused {
		return
	}
	if p := SongPlayer(lvl); p != nil {
		p.Pause(paused)
	}
	if p := ScorePlayer(lvl); p != nil {
		p.Pause(paused)
	}
	musicPaused = paused
}
