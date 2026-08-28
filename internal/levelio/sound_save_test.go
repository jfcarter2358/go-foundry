package levelio

// sound_save_test.go — sounds and music through the YAML boundary.
//
// The same two assertions the emitter and sprite tests make, for the same
// reasons: a fully-specified sound must survive a round trip, and a level that
// has no sounds must not grow a key. The second is the one that silently
// breaks when someone adds a field without a pointer, which is why it
// enumerates the keys by name rather than counting them.
//
// The third assertion here is specific to audio: the mixer handle must never
// reach a file. A voice id written into a level and read back a week later
// would name a voice that has never existed.

import (
	"reflect"
	"strings"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

func TestSoundRoundTripsEveryField(t *testing.T) {
	want := world.Sound{
		ID:            "waterfall",
		Clip:          "water_loop",
		Position:      mgl32.Vec3{1.5, 2.2, -3},
		Rotation:      mgl32.Vec3{0, 0.5, 0},
		Enabled:       false,
		Playing:       false,
		Loop:          false,
		Gain:          0.4,
		Pitch:         1.25,
		Bus:           "music",
		RefDistance:   3,
		MaxDistance:   75,
		Rolloff:       2,
		Atten:         "linear",
		ConeInner:     0.5,
		ConeOuter:     1.5,
		ConeOuterGain: 0.25,
		Attach:        world.Attach{Frame: "ship", Entity: "npc", Model: "body", Bone: "head"},
	}

	got, err := world.SoundFromRaw(world.SoundToRaw(&want))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	// ScriptData holds maps and slices; Voice is runtime-only.
	got.ScriptData = world.ScriptData{}
	want.ScriptData = world.ScriptData{}
	if !got.Voice.IsZero() {
		t.Error("a live mixer voice came back out of a level file; it must not be written")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sound changed across a round trip:\n got %+v\nwant %+v", got, want)
	}
}

func TestDefaultSoundWritesNoOptionalKeys(t *testing.T) {
	s := world.NewSound("sound", mgl32.Vec3{1, 2, 3})
	s.Clip = "torch"
	out, err := yaml.Marshal(world.SoundToRaw(&s))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text := string(out)
	for _, key := range []string{
		"enabled:", "playing:", "loop:", "gain:", "pitch:", "bus:",
		"ref_distance:", "max_distance:", "rolloff:", "atten:",
		"cone_inner:", "cone_outer:", "cone_outer_gain:",
		"attach:", "rotation:", "tags:", "attributes:", "script:",
	} {
		if strings.Contains(text, key) {
			t.Errorf("a default sound wrote %q; it is the same as absent and must be omitted:\n%s", key, text)
		}
	}
}

func TestSoundlessLevelGainsNoSoundsKey(t *testing.T) {
	// An existing level must round-trip byte-identical after this feature
	// lands. There is no version number in this format; that guarantee is
	// entirely carried by omitempty.
	raw := world.RawLevel{}
	out, err := yaml.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{"sounds:", "music:"} {
		if strings.Contains(string(out), key) {
			t.Errorf("a level with no audio wrote %q:\n%s", key, out)
		}
	}
}

func TestMusicRoundTripsAndAnAbsentBlockStaysAbsent(t *testing.T) {
	if got := world.MusicFromRaw(nil); got != nil {
		t.Errorf("a level with no music block produced %+v; nil must stay nil", got)
	}
	if got := world.MusicToRaw(nil); got != nil {
		t.Errorf("nil music wrote %+v", got)
	}
	// A track-less Music is the same as none, and must not write a block.
	empty := world.Music{Gain: 1, Loop: true}
	if got := world.MusicToRaw(&empty); got != nil {
		t.Errorf("music with no track wrote %+v", got)
	}

	want := world.Music{Track: "cavern", Gain: 0.6, Loop: false, Fade: 2.5}
	got := world.MusicFromRaw(world.MusicToRaw(&want))
	if got == nil {
		t.Fatal("a full music block round-tripped to nil")
	}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("music changed across a round trip:\n got %+v\nwant %+v", *got, want)
	}
}

func TestASoundIDCollidesAcrossTheWholeLevel(t *testing.T) {
	// SCRIPTING.md promises that `@id` never needs qualifying, which depends
	// entirely on LevelIDTaken knowing about every kind. It is the easiest
	// step to miss when adding one.
	lvl := world.Level{
		Sounds: []world.Sound{{ID: "hum"}},
	}
	if !world.LevelIDTaken(&lvl, "hum") {
		t.Error("a sound's ID is not reserved; another object could take it")
	}
	if id := world.UniqueSoundID(&lvl, "hum"); id == "hum" {
		t.Error("UniqueSoundID handed back an ID that is already in use")
	}
}
