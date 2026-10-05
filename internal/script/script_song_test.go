package script

import (
	"testing"

	"foundry/internal/flang"
	"foundry/internal/tracker"
	"foundry/internal/world"
)

// songOnLevel puts a tracker player on the level as if MusicUpdate had started
// it, with no audio device: the test renders it in place of the mixer.
func songOnLevel(lvl *world.Level) *tracker.Player {
	s := tracker.NewSong("t")
	s.Patterns[0].Resize(8, 4)
	s.Patterns[0].Cells[1][0].FX[0] = tracker.Effect{Cmd: tracker.FxMarker, Param: 3}
	s.Patterns = append(s.Patterns, tracker.NewPattern("b", 8, 4))
	s.Order = []int{0, 1}
	s.Sections = []tracker.Section{{Name: "a", Start: 0, End: 1, Loop: true}, {Name: "b", Start: 1, End: 2, Loop: true}}
	s.Markers = map[uint8]string{3: "drop"}
	s.Params = []tracker.Param{{Name: "intensity"}}
	p := tracker.NewPlayer(s)
	p.Start("a")
	lvl.Music = &world.Music{Track: "t", Gain: 1, Loop: true, Song: p}
	return p
}

func renderSong(p *tracker.Player, secs float64) {
	buf := make([]float32, 480*2)
	for i := 0; i < int(secs*100); i++ {
		p.Render(buf, 480, 48000)
	}
}

func TestScriptsSteerTheSongAndHearItsEvents(t *testing.T) {
	lvl := pointerLevel(t, "", `
def on_tick(:tick:, dt float) -> null
    if @crate.attr.n == 0
        @crate.attr.n = 1
        song_param("intensity", 0.5)
        song_mute("Ch 2", true)
        @crate.attr.x = song_position()["row"]
    end
end

def on_marker(:song_marker:, name str, value int) -> null
    @crate.attr.hit = name
end

def on_section(:song_section:, name str) -> null
    @crate.attr.seen = name
end

def on_go(:go:) -> null
    song_section("b", "now")
end`)
	p := songOnLevel(lvl)
	ptick(lvl, 1)
	if lvl.Music.Params["intensity"] != 0.5 || !lvl.Music.Mutes["Ch 2"] {
		t.Fatalf("song_param/song_mute were not remembered for a save: %+v %+v", lvl.Music.Params, lvl.Music.Mutes)
	}

	renderSong(p, 0.3) // past row 1's marker
	ptick(lvl, 1)
	if got := crateAttr(t, lvl, "hit").S; got != "drop" {
		t.Errorf(":song_marker: carried %q, want drop", got)
	}

	_ = Scripts.post(flang.RefVal{Kind: flang.RefLevel}, "go", nil, flang.Pos{})
	ptick(lvl, 1)
	renderSong(p, 0.02)
	ptick(lvl, 1)
	if got := crateAttr(t, lvl, "seen").S; got != "b" {
		t.Errorf(":song_section: carried %q, want b", got)
	}
	if lvl.Music.Section != "b" {
		t.Errorf("music section remembered as %q, want b", lvl.Music.Section)
	}
}

func TestSongVerbsWithNoSongAreQuiet(t *testing.T) {
	lvl := pointerLevel(t, "", `
def on_tick(:tick:, dt float) -> null
    @crate.attr.seen = format("{}", song_section("x"))
    @crate.attr.level = format("{}", song_position()["playing"])
end`)
	ptick(lvl, 1)
	if got := crateAttr(t, lvl, "seen").S; got != "false" {
		t.Errorf("song_section with no song = %q, want false", got)
	}
	if got := crateAttr(t, lvl, "level").S; got != "false" {
		t.Errorf("song_position().playing with no song = %q, want false", got)
	}
}
