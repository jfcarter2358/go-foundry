package audio

// audio_registry_test.go — the cache, and the part of it that looks like a bug.
//
// Failures are cached. A level with forty torches naming a clip that is not
// there must say so once, not forty times a frame for as long as the level is
// open. That is the same reasoning world.LoadEntityDef's cache is built on,
// and the same test is worth having here.

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
)

// withSoundsDir points the asset path at a temporary directory and restores
// the executable path afterwards. fileutil.ExecutablePath is a package global
// the whole engine resolves assets against, so it gets the with-helper
// treatment every other global in this repo's tests gets.
func withSoundsDir(t *testing.T) string {
	t.Helper()
	prev := fileutil.ExecutablePath
	dir := t.TempDir()
	fileutil.ExecutablePath = dir
	ReloadClips()
	t.Cleanup(func() {
		fileutil.ExecutablePath = prev
		ReloadClips()
	})
	if err := os.MkdirAll(filepath.Join(dir, SoundsDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, SoundsDirName)
}

func writeTestWAV(t *testing.T, dir, name string, frames int) {
	t.Helper()
	s := make([]float32, frames)
	for i := range s {
		s[i] = 0.25
	}
	raw := buildWAV(t, 44100, 1, 16, false, s, false)
	if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAClipIsDecodedOnceAndThenServedFromTheCache(t *testing.T) {
	dir := withSoundsDir(t)
	writeTestWAV(t, dir, "thud.wav", 64)

	a, err := ClipFor("thud")
	if err != nil {
		t.Fatalf("ClipFor: %v", err)
	}
	b, err := ClipFor("thud")
	if err != nil {
		t.Fatalf("ClipFor (second): %v", err)
	}
	if a != b {
		t.Error("the second load returned a different Clip; the cache is not being used")
	}
	if a.Frames() != 64 {
		t.Errorf("decoded %d frames, wrote 64", a.Frames())
	}
}

func TestAMissingSoundIsReportedOnceAndRememberedAsAFailure(t *testing.T) {
	withSoundsDir(t)

	if _, err := ClipFor("not-there"); err == nil {
		t.Fatal("a missing sound loaded")
	}
	before := CachedClipCount()
	for i := 0; i < 40; i++ {
		if _, err := ClipFor("not-there"); err == nil {
			t.Fatal("a missing sound loaded on a later attempt")
		}
	}
	if after := CachedClipCount(); after != before {
		t.Errorf("the cache grew from %d to %d; the failure is not being remembered", before, after)
	}
}

func TestReloadClipsPicksUpAFileWrittenAfterTheFailure(t *testing.T) {
	// The editor's Reload button. An author who fixes a typo'd filename must
	// not have to restart the engine to hear it.
	dir := withSoundsDir(t)
	if _, err := ClipFor("late"); err == nil {
		t.Fatal("it loaded before the file existed")
	}
	writeTestWAV(t, dir, "late.wav", 32)
	if _, err := ClipFor("late"); err == nil {
		t.Error("the cached failure was not consulted; it should still fail before a reload")
	}
	ReloadClips()
	if _, err := ClipFor("late"); err != nil {
		t.Errorf("after ReloadClips the file is there but still fails: %v", err)
	}
}

func TestSoundPathTriesEveryKnownExtension(t *testing.T) {
	dir := withSoundsDir(t)
	writeTestWAV(t, dir, "footstep.wav", 8)

	if p := SoundPath("footstep"); p == "" {
		t.Error("a bare name did not resolve to the .wav beside it")
	}
	if p := SoundPath("footstep.wav"); p == "" {
		t.Error("a name carrying its own extension did not resolve")
	}
	if p := SoundPath("absent"); p != "" {
		t.Errorf("a name with no file resolved to %q", p)
	}
	if p := SoundPath(""); p != "" {
		t.Errorf("an empty name resolved to %q", p)
	}
}

func TestScanSoundNamesDeduplicatesAcrossExtensions(t *testing.T) {
	// Re-encoding an asset commonly leaves both foo.wav and foo.ogg behind,
	// and SoundPath will only ever reach one of them. Offering the author two
	// identical-looking entries in a dropdown would be a puzzle with no
	// answer.
	dir := withSoundsDir(t)
	writeTestWAV(t, dir, "ambient.wav", 8)
	if err := os.WriteFile(filepath.Join(dir, "ambient.ogg"), []byte("not really ogg"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestWAV(t, dir, "click.wav", 8)

	names := ScanSoundNames()
	if len(names) != 2 || names[0] != "ambient" || names[1] != "click" {
		t.Errorf("got %v, want [ambient click] — sorted and deduplicated", names)
	}
}

func TestAnUnreadableFileFailsWithoutTakingTheLevelDown(t *testing.T) {
	// The level loader keeps a placement whose asset will not read, so the
	// author can see it in the editor and fix it. That only works if the
	// error comes back as an error rather than as a panic.
	dir := withSoundsDir(t)
	if err := os.WriteFile(filepath.Join(dir, "broken.wav"), []byte("RIFFnope"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := ClipFor("broken")
	if err == nil {
		t.Error("a malformed file loaded")
	}
	if c != nil {
		t.Error("a failed load returned a Clip")
	}
}
