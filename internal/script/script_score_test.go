package script

import (
	"testing"

	"foundry/internal/flang"
	"foundry/internal/score"
	"foundry/internal/score/play"
	"foundry/internal/tracker"
	"foundry/internal/world"
)

// testScoreSrc is two bars of 4/4 at the default 120: a bar is 2 s.
const testScoreSrc = `bars: 2
sections: [{name: a, bar: 1}, {name: b, bar: 2}]
markers: [{name: drop, bar: 1, beat: "3"}]
params: [{name: intensity, default: 0}]
bindings: [{param: intensity, line: Horn, target: volume, from: 0, to: 1}]
lines:
  - name: Strings
    instrument: test/strings
    staves: [{clef: treble, voices: ["| wC4 | wD4 |"]}]
  - name: Horn
    instrument: test/horn
    staves: [{clef: treble, voices: ["| wE4 | wF4 |"]}]
`

// scoreOnLevel puts a score's player on the level as if MusicUpdate had
// started it, with no audio device: the test renders it in place of the
// mixer. Its instruments are a quiet constant, loaded from nowhere.
func scoreOnLevel(t *testing.T, lvl *world.Level) *play.Player {
	t.Helper()
	prev := play.LoadInstrument
	play.LoadInstrument = func(name string) (*tracker.Instrument, error) {
		d := make([]float32, 4800)
		for i := range d {
			d[i] = 0.1
		}
		in := tracker.NewInstrument(name, name)
		in.SetLoaded(0, &tracker.Sample{Name: name, Data: d, Channels: 1, Rate: 48000, BaseNote: tracker.NoteC4, Volume: 1,
			Loop: tracker.LoopForward, LoopStart: 0, LoopEnd: len(d)})
		return in, nil
	}
	t.Cleanup(func() { play.LoadInstrument = prev; score.Reload() })
	s, err := score.Parse([]byte(testScoreSrc))
	if err != nil {
		t.Fatal(err)
	}
	p, errs := play.New(s)
	if p == nil {
		t.Fatal(errs)
	}
	p.Start("a")
	lvl.Music = &world.Music{Track: "theme", Gain: 1, Loop: true, Score: p, Voice: 1}
	return p
}

func renderScore(p *play.Player, secs float64) {
	buf := make([]float32, 480*2)
	for i := 0; i < int(secs*100); i++ {
		p.Render(buf, 480, 48000)
	}
}

// The song verbs steer a score, and its events reach the scripts as a
// song's do: its markers, its rehearsal marks as sections, every beat (from
// 0 in the bar) with the bar after its first beat, and the end.
func TestScriptsSteerTheScoreAndHearItsEvents(t *testing.T) {
	lvl := pointerLevel(t, "", `
def on_tick(:tick:, dt float) -> null
    if @crate.attr.n == 0
        @crate.attr.n = 1
        song_param("intensity", 0.5)
        song_mute("Horn", true)
        @crate.attr.x = song_position()["bar"]
    end
end

def on_marker(:song_marker:, name str, value int) -> null
    @crate.attr.hit = format("{}@{}", name, value)
end

def on_section(:song_section:, name str) -> null
    @crate.attr.seen = name
end

def on_beat(:song_beat:, beat int, section str) -> null
    @crate.attr.level = format("{}{}{}", @crate.attr.level, section, beat)
end

def on_bar(:song_bar:, section str) -> null
    @crate.attr.level = format("{}|", @crate.attr.level)
end

def on_end(:song_end:, song str) -> null
    @crate.attr.seen = format("end of {}", song)
end

def on_go(:go:) -> null
    song_section("b", "now")
end`)
	p := scoreOnLevel(t, lvl)
	ptick(lvl, 1)
	if lvl.Music.Params["intensity"] != 0.5 || !lvl.Music.Mutes["Horn"] {
		t.Fatalf("song_param/song_mute were not remembered for a save: %+v %+v", lvl.Music.Params, lvl.Music.Mutes)
	}
	if got := crateAttr(t, lvl, "x"); got.I != 1 && got.F != 1 {
		t.Errorf("song_position()[bar] = %+v, want 1", got)
	}
	if got := crateAttr(t, lvl, "seen").S; got != "a" {
		t.Errorf(":song_section: at the start carried %q, want a", got)
	}

	renderScore(p, 1.2) // beats at 0, 0.5 and 1 s; the marker on beat 3
	ptick(lvl, 1)
	if got := crateAttr(t, lvl, "hit").S; got != "drop@1" {
		t.Errorf(":song_marker: carried %q, want drop@1", got)
	}
	if got := crateAttr(t, lvl, "level").S; got != "a0|a1a2" {
		t.Errorf("beats and bars heard as %q, want a0|a1a2", got)
	}

	_ = Scripts.post(flang.RefVal{Kind: flang.RefLevel}, "go", nil, flang.Pos{})
	ptick(lvl, 1)
	renderScore(p, 0.02)
	ptick(lvl, 1)
	if got := crateAttr(t, lvl, "seen").S; got != "b" {
		t.Errorf(":song_section: carried %q, want b", got)
	}
	if lvl.Music.Section != "b" {
		t.Errorf("music section remembered as %q, want b", lvl.Music.Section)
	}
	if got := crateAttr(t, lvl, "level").S; got != "a0|a1a2b0|" {
		t.Errorf("after the jump, beats heard as %q, want a0|a1a2b0|", got)
	}

	renderScore(p, 2.1) // bar 2 runs out: the score does not loop
	ptick(lvl, 1)
	if got := crateAttr(t, lvl, "seen").S; got != "end of theme" {
		t.Errorf(":song_end: carried %q, want end of theme", got)
	}
}

// What a score answers the rest of the verbs with: play_note on a line,
// song_tempo as the tempo slider, song_stinger of a score or a song, and
// song_position's score fields. Names it does not have are refused.
func TestScoreVerbs(t *testing.T) {
	lvl := pointerLevel(t, "", `
def on_tick(:tick:, dt float) -> null
    if @crate.attr.n == 0
        @crate.attr.n = 1
        @crate.attr.hit = format("{} {} {} {}", play_note("Strings", "C4"), play_note(2, "C-4", 0.5, 0.2), play_note("Tuba", "C4"), play_note(1, "H9"))
        @crate.attr.seen = format("{} {} {}", song_stinger("sting"), song_stinger("sting", "bar"), song_stinger("nothing"))
        @crate.attr.level = format("{} {}", song_tempo(60, 0), song_section("coda"))
        @crate.attr.x = song_position()["tick"]
    end
end`)
	p := scoreOnLevel(t, lvl)
	s, err := score.Parse([]byte(testScoreSrc))
	if err != nil {
		t.Fatal(err)
	}
	if err := score.Save("sting", s); err != nil {
		t.Fatal(err)
	}
	ptick(lvl, 1)
	if got := crateAttr(t, lvl, "hit").S; got != "true true false false" {
		t.Errorf("play_note: %q, want true true false false", got)
	}
	if got := crateAttr(t, lvl, "seen").S; got != "true true false" {
		t.Errorf("song_stinger: %q, want true true false", got)
	}
	if got := crateAttr(t, lvl, "level").S; got != "true false" {
		t.Errorf("song_tempo, song_section(coda): %q, want true false", got)
	}
	// song_tempo(60) of a score written at 120 is the slider at half: a
	// second of the mixer is half a second of the score.
	renderScore(p, 1)
	if got := p.Position().Seconds; got < 0.49 || got > 0.51 {
		t.Errorf("at song_tempo(60) a second plays %.3f s of the score, want 0.5", got)
	}
}

// With a score file and no audio device, play_song still makes it the
// level's track at the asked-for section — what a headless run reads as
// music.track and music.section.
func TestPlaySongTakesAScore(t *testing.T) {
	lvl := pointerLevel(t, "", `
def on_tick(:tick:, dt float) -> null
    if @crate.attr.n == 0
        @crate.attr.n = 1
        @crate.attr.seen = format("{} {}", play_song("theme", "b"), play_song("missing"))
    end
end`)
	s, err := score.Parse([]byte(testScoreSrc))
	if err != nil {
		t.Fatal(err)
	}
	if err := score.Save("theme", s); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(score.Reload)
	ptick(lvl, 1)
	if got := crateAttr(t, lvl, "seen").S; got != "true false" {
		t.Errorf("play_song(theme, b), play_song(missing): %q, want true false", got)
	}
	if lvl.Music == nil || lvl.Music.Track != "theme" || lvl.Music.Section != "b" {
		t.Errorf("music is %+v, want theme at b", lvl.Music)
	}
}

// The scheduler posts a downbeat's bar and then its beat from the mixer
// goroutine, so a frame's drain can take the bar and leave the beat for the
// next frame: the bar is still heard, after its beat.
func TestScoreBarSurvivesASplitDrain(t *testing.T) {
	lvl := pointerLevel(t, "", `
def on_beat(:song_beat:, beat int, section str) -> null
    @crate.attr.level = format("{}{}{}", @crate.attr.level, section, beat)
end

def on_bar(:song_bar:, section str) -> null
    @crate.attr.level = format("{}|", @crate.attr.level)
end`)
	p := scoreOnLevel(t, lvl)
	renderScore(p, 0.01)
	evs := p.Events(nil)
	split := -1
	for i, e := range evs {
		if e.Kind == play.EventBar {
			split = i + 1
		}
	}
	if split < 0 || split >= len(evs) || evs[split].Kind != play.EventBeat {
		t.Fatalf("the downbeat's events: %+v", evs)
	}
	postScoreEventList(Scripts, lvl, p.Performance(), evs[:split])
	ptick(lvl, 1)
	postScoreEventList(Scripts, lvl, p.Performance(), evs[split:])
	ptick(lvl, 1)
	if got := crateAttr(t, lvl, "level").S; got != "a0|" {
		t.Errorf("a downbeat drained over two frames was heard as %q, want a0|", got)
	}
}
