package levelio

// sound_e2e_test.go — a level with audio in it, all the way through YAML.
//
// The round-trip tests above check one object's fields. This checks the thing
// an author actually does: save a level containing a placed sound and a music
// track, load it back, and get the same level. It is the assertion that the
// two new top-level keys are wired into both directions of the loader, which
// the per-object tests cannot see.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func TestALevelWithSoundsSurvivesSaveAndLoad(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	// Point the asset root at the shipped resources so the clips resolve; the
	// loader reports a missing one and keeps the placement, and asserting on
	// that path separately is what the next test does.
	prev := fileutil.ExecutablePath
	fileutil.ExecutablePath = filepath.Join(testenv.RepoRoot(t), "resources")
	t.Cleanup(func() { fileutil.ExecutablePath = prev })

	lvl, _ := NewBlankLevel()

	s := world.NewSound("waterfall", mgl32.Vec3{4, 1, -2})
	s.Clip = "hum"
	s.Gain = 0.6
	s.RefDistance = 3
	s.MaxDistance = 60
	s.Tags = []string{"ambient"}
	lvl.Sounds = append(lvl.Sounds, s)

	m := world.NewMusic("hum")
	m.Gain = 0.4
	lvl.Music = &m

	path := filepath.Join(t.TempDir(), "audio_level.yaml")
	if err := SaveLevel(&lvl, path); err != nil {
		t.Fatalf("SaveLevel: %v", err)
	}

	// The file has to actually carry the keys; an omitempty mistake would
	// silently drop the whole feature and every in-memory assertion below
	// would still pass on a level that never went to disk.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"sounds:", "music:", "waterfall", "hum"} {
		if !strings.Contains(string(data), key) {
			t.Fatalf("the saved level does not mention %q:\n%s", key, data)
		}
	}

	back, err := LoadLevel(path)
	if err != nil {
		t.Fatalf("LoadLevel: %v", err)
	}

	if len(back.Sounds) != 1 {
		t.Fatalf("%d sounds came back, saved 1", len(back.Sounds))
	}
	got := back.Sounds[0]
	if got.ID != "waterfall" || got.Clip != "hum" {
		t.Errorf("sound came back as %q/%q", got.ID, got.Clip)
	}
	if got.Gain != 0.6 || got.RefDistance != 3 || got.MaxDistance != 60 {
		t.Errorf("gain/distances came back as %v/%v/%v", got.Gain, got.RefDistance, got.MaxDistance)
	}
	if !got.HasTag("ambient") {
		t.Error("the tag did not survive; #ambient would not reach it")
	}
	if back.Music == nil || back.Music.Track != "hum" || back.Music.Gain != 0.4 {
		t.Errorf("music came back as %+v", back.Music)
	}
}

func TestASoundWhoseClipIsMissingKeepsItsPlacement(t *testing.T) {
	// The emitter rule: the placement is real level content and the author
	// needs to see it in the editor to fix it. Failing the load would take the
	// whole level down over one misspelled filename.
	testenv.WithoutGeometryUploads(t)

	lvl, _ := NewBlankLevel()
	s := world.NewSound("broken", mgl32.Vec3{})
	s.Clip = "no-such-clip-anywhere"
	lvl.Sounds = append(lvl.Sounds, s)

	path := filepath.Join(t.TempDir(), "broken.yaml")
	if err := SaveLevel(&lvl, path); err != nil {
		t.Fatalf("SaveLevel: %v", err)
	}
	back, err := LoadLevel(path)
	if err != nil {
		t.Fatalf("a missing clip failed the whole level load: %v", err)
	}
	if len(back.Sounds) != 1 {
		t.Fatalf("the placement was dropped; the author has nothing to click on to fix it")
	}
}

func TestOneShotSoundsAreNotWrittenToTheLevelFile(t *testing.T) {
	// A script firing play_sound_at per impact fills the level with one-shots.
	// Saving mid-session must not bake them in.
	testenv.WithoutGeometryUploads(t)

	lvl, _ := NewBlankLevel()
	keep := world.NewSound("keep", mgl32.Vec3{})
	keep.Clip = "hum"
	spent := world.NewSound("spent", mgl32.Vec3{})
	spent.Clip = "click"
	spent.OneShot = true
	lvl.Sounds = append(lvl.Sounds, keep, spent)

	path := filepath.Join(t.TempDir(), "oneshot.yaml")
	if err := SaveLevel(&lvl, path); err != nil {
		t.Fatalf("SaveLevel: %v", err)
	}
	back, err := LoadLevel(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Sounds) != 1 || back.Sounds[0].ID != "keep" {
		t.Errorf("got %d sounds back; a script-fired one-shot was written to the file", len(back.Sounds))
	}
}
