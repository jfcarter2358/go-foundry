package harness

import (
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/score"
	"foundry/internal/world"
)

// A headless run has no audio device, so a score never gets a player; its
// track and section are still the level's music, and a watch reads them.
func TestAWatchReadsAScoresTrackAndSection(t *testing.T) {
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = t.TempDir()
	t.Cleanup(func() { fileutil.ResourcesPath = prev; score.Reload() })
	s, err := score.Parse([]byte("bars: 2\nsections: [{name: a, bar: 1}, {name: b, bar: 2}]\nlines: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := score.Save("theme", s); err != nil {
		t.Fatal(err)
	}
	lvl := &world.Level{}
	if got := read(lvl, "music.track"); got != "none" {
		t.Errorf("music.track with no music = %q, want none", got)
	}
	if !world.PlaySong(lvl, "theme", "b", 0) {
		t.Fatal("play_song of a score did not switch the track")
	}
	if got := read(lvl, "music.track"); got != "theme" {
		t.Errorf("music.track = %q, want theme", got)
	}
	if got := read(lvl, "music.section"); got != "b" {
		t.Errorf("music.section = %q, want b", got)
	}
	if world.ScorePlayer(lvl) != nil {
		t.Error("a score got a player with no audio device")
	}
}
