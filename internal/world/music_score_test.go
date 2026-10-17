package world

import (
	"testing"

	"foundry/internal/score"
	"foundry/internal/score/play"
	"foundry/internal/tracker"
)

// scoreOnLevel puts a score's player on a level as if MusicUpdate had
// started it, with no audio device: the test renders it in place of the
// mixer. Every line plays a constant level, so silence is easy to see.
func scoreOnLevel(t *testing.T) (*Level, *play.Player) {
	t.Helper()
	prev := play.LoadInstrument
	play.LoadInstrument = func(name string) (*tracker.Instrument, error) {
		d := make([]float32, 48000)
		for i := range d {
			d[i] = 0.5
		}
		in := tracker.NewInstrument(name, name)
		in.SetLoaded(0, &tracker.Sample{Name: name, Data: d, Channels: 1, Rate: 48000, BaseNote: tracker.NoteC4, Volume: 1,
			Loop: tracker.LoopForward, LoopStart: 0, LoopEnd: len(d)})
		return in, nil
	}
	t.Cleanup(func() { play.LoadInstrument = prev; musicPaused = false })
	s, err := score.Parse([]byte(`bars: 2
sections: [{name: a, bar: 1, loop: true}, {name: b, bar: 2, loop: true}]
params: [{name: intensity, default: 0}]
bindings: [{param: intensity, line: Horn, target: volume, from: 0, to: 1}]
lines:
  - name: Strings
    instrument: test/strings
    staves: [{clef: treble, voices: ["| wC4 | wD4 |"]}]
  - name: Horn
    instrument: test/horn
    staves: [{clef: treble, voices: ["| wE4 | wF4 |"]}]
`))
	if err != nil {
		t.Fatal(err)
	}
	p, errs := play.New(s)
	if p == nil {
		t.Fatal(errs)
	}
	p.Start("a")
	lvl := &Level{}
	lvl.Music = &Music{Track: "t", Gain: 1, Loop: true, Score: p, Voice: 1}
	return lvl, p
}

func renderScore(p *play.Player, secs float64) []float32 {
	out := make([]float32, int(secs*48000)*2)
	for i := 0; i+480 <= len(out)/2; i += 480 {
		p.Render(out[i*2:(i+480)*2], 480, 48000)
	}
	return out
}

func peak(b []float32) float32 {
	var m float32
	for _, v := range b {
		m = max(m, v, -v)
	}
	return m
}

// The song verbs' world half steers a score: sections are its rehearsal
// marks, channels its lines, params its own; every change is remembered for
// a save, and a name the score does not have is refused.
func TestMusicSteersAScore(t *testing.T) {
	lvl, p := scoreOnLevel(t)
	if !MusicSteerable(lvl) || ScorePlayer(lvl) != p || SongPlayer(lvl) != nil {
		t.Fatal("the score is not the level's steerable music")
	}
	renderScore(p, 0.05)
	if got := MusicSection(lvl); got != "a" {
		t.Errorf("section %q, want a", got)
	}
	if !SetSongSection(lvl, "b", tracker.QuantNow) || lvl.Music.Section != "b" {
		t.Errorf("song_section(b) was not taken or not remembered (%q)", lvl.Music.Section)
	}
	if SetSongSection(lvl, "coda", tracker.QuantBar) {
		t.Error("a section the score does not have was taken")
	}
	renderScore(p, 0.05)
	if got := MusicSection(lvl); got != "b" {
		t.Errorf("section %q after song_section(b), want b", got)
	}
	if pos := p.Position(); pos.Bar != 2 {
		t.Errorf("play head at bar %d after song_section(b), want 2", pos.Bar)
	}

	if !SetSongParam(lvl, "intensity", 0.5, 0) || lvl.Music.Params["intensity"] != 0.5 {
		t.Errorf("song_param was not taken or not remembered: %v", lvl.Music.Params)
	}
	if SetSongParam(lvl, "nope", 1, 0) {
		t.Error("a param the score does not have was taken")
	}
	if !SetSongMute(lvl, "Horn", true, 0) || !SetSongMute(lvl, "1", true, 0) || !lvl.Music.Mutes["Horn"] || !lvl.Music.Mutes["1"] {
		t.Errorf("song_mute by name and number: %v", lvl.Music.Mutes)
	}
	if SetSongMute(lvl, "Tuba", true, 0) || SetSongMute(lvl, "3", true, 0) {
		t.Error("a line the score does not have was muted")
	}
	renderScore(p, 0.1)
	if v := peak(renderScore(p, 0.1)); v > 1e-4 {
		t.Errorf("both lines muted, the score still plays at %v", v)
	}

	// Asking for the playing score again changes section, not the track.
	if PlaySong(lvl, "t", "a", 0) || lvl.Music.Score != p || lvl.Music.Section != "a" {
		t.Error("play_song of the playing score restarted it")
	}
	renderScore(p, 2.1)
	if got := MusicSection(lvl); got != "a" {
		t.Errorf("section %q after play_song(t, a), want a", got)
	}

	StopMusic(lvl, 0)
	if MusicSteerable(lvl) || lvl.Music.Score != nil || MusicSection(lvl) != "" {
		t.Error("stop_music left the score steerable")
	}
}

// The hard pause freezes a score where it is, silent, and lets it go on.
func TestMusicPauseFreezesAScore(t *testing.T) {
	lvl, p := scoreOnLevel(t)
	renderScore(p, 0.5)
	if peak(renderScore(p, 0.1)) == 0 {
		t.Fatal("the score is silent before the pause")
	}
	MusicPause(lvl, true)
	at := p.Position().Seconds
	if v := peak(renderScore(p, 0.5)); v != 0 {
		t.Errorf("paused, the score plays at %v", v)
	}
	if got := p.Position(); !got.Paused || got.Seconds != at {
		t.Errorf("paused, the score moved from %v to %+v", at, got)
	}
	MusicPause(lvl, false)
	if peak(renderScore(p, 0.1)) == 0 || p.Position().Seconds <= at {
		t.Error("unpaused, the score did not go on")
	}
}

// A level's music block names a score the way it names a song: track,
// section and params read and write back unchanged.
func TestMusicBlockNamesAScore(t *testing.T) {
	raw := &RawMusic{Track: "hangar_theme", Section: "theme", Params: map[string]float32{"intensity": 0.25}}
	m := MusicFromRaw(raw)
	if m.Track != "hangar_theme" || m.Section != "theme" || m.Params["intensity"] != 0.25 || !m.Loop || m.Gain != 1 {
		t.Fatalf("read %+v", m)
	}
	back := MusicToRaw(m)
	if back.Track != raw.Track || back.Section != raw.Section || back.Params["intensity"] != 0.25 || back.Gain != nil || back.Loop != nil {
		t.Errorf("wrote %+v", back)
	}
}
