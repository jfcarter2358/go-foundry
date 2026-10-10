package trackercli

// golden_test.go — the shipped songs sound exactly as they did.
//
// The hashes were captured from the Player before its voices, strips and
// buses moved into tracker.Engine, so this is the proof that the refactor
// left every song the game ships bit-identical: a whole pass of each song
// and a stretch of each of its sections.

import (
	"os"
	"testing"

	"foundry/internal/tracker"
)

var shippedGolden = map[string]string{
	"covenant_assault":        "b0ded30bf5c7a5b5",
	"covenant_assault/combat": "bc727d7f8a0c5aa7",
	"covenant_assault/intro":  "c4e1b657107c14bd",
	"covenant_assault/outro":  "aba8064dbcf93eec",
	"demo_theme":              "c507867b8106c125",
	"demo_theme/combat":       "28a579149b29beb8",
	"demo_theme/intro":        "a3a7c9f70e7f5f0f",
	"demo_theme/outro":        "c1bde906d9eb06ff",
	"relay_combat":            "32a3c99e5a57230f",
	"relay_combat/combat":     "1befbfe0069fe09b",
	"relay_combat/intro":      "49ffbd100e520c58",
	"relay_combat/outro":      "a1cafcb0a5028ca4",
	"ring_chant":              "dce6ce126eda504a",
	"ring_chant/chant":        "d30ec997a0ba865d",
	"ring_chant/march":        "098d32dcc957d40b",
	"ring_chant/outro":        "a8a82a47e9a404ff",
	"ring_chant/reprise":      "612afa01508bb2e1",
}

func TestShippedSongsGolden(t *testing.T) {
	if testing.Short() {
		t.Skip("renders every shipped song")
	}
	useRepoResources(t)
	print := os.Getenv("TRACKER_GOLDEN_PRINT") != ""
	for _, name := range tracker.ScanSongNames() {
		s, err := tracker.Load(name)
		if err != nil {
			t.Fatal(err)
		}
		cases := map[string]tracker.RenderOptions{name: {Tail: 2}}
		for _, sec := range s.Sections {
			cases[name+"/"+sec.Name] = tracker.RenderOptions{Section: sec.Name, Seconds: 6}
		}
		for key, o := range cases {
			got := tracker.Hash(tracker.Render(s.Clone(), o))
			if print {
				t.Logf("%q: %q,", key, got)
				continue
			}
			want, ok := shippedGolden[key]
			if !ok {
				t.Errorf("%s: no golden hash (a new song or section? capture it with TRACKER_GOLDEN_PRINT=1)", key)
			} else if got != want {
				t.Errorf("%s: hash %s, want %s — the song sounds different", key, got, want)
			}
		}
	}
}
