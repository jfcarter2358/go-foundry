package main

import (
	"os"
	"path/filepath"
	"testing"
)

// withTempPresetDir points executablePath at a scratch directory and clears the
// user-preset registry, so preset tests never touch the real dist/ tree or leak
// state into each other.
func withTempPresetDir(t *testing.T) {
	t.Helper()
	prevPath := executablePath
	prevSet := detailUserPresets
	prevNames := detailUserPresetNames
	prevPresets := snapshotDetailPresets()

	executablePath = t.TempDir()
	detailUserPresets = map[string]bool{}
	detailUserPresetNames = nil

	t.Cleanup(func() {
		executablePath = prevPath
		detailUserPresets = prevSet
		detailUserPresetNames = prevNames
		detailPresets = prevPresets
	})
}

// TestDetailPresetSaveLoad verifies a saved preset comes back with its wave and
// noise settings intact, and carries no seed of its own.
func TestDetailPresetSaveLoad(t *testing.T) {
	withTempPresetDir(t)

	in := DetailSpec{
		Enabled: true, Seed: 99, Displace: true,
		Waves: [detailMaxWaves]DetailWave{
			{Enabled: true, Amplitude: 0.42, Frequency: 0.7, Rotation: 15, TessLevel: 28},
			{Enabled: false},
			{Enabled: true, Amplitude: 0.11, Frequency: 2.5, Rotation: -80, TessLevel: 12},
		},
		Noise: true, NoiseAmplitude: 0.2, NoiseFrequency: 1.75, NoiseOctaves: 5, NoiseTessLevel: 30,
		EdgeTaper: 0.35,
		Scatter:   true, ScatterMesh: "rock", ScatterTex: "rock",
		ScatterDensity: 0.6, ScatterSpacing: 1.1, ScatterScale: 0.45,
		ScatterJitter: 0.7, ScatterRange: 55,
	}
	if err := SaveDetailPreset("my ridges", in); err != nil {
		t.Fatalf("SaveDetailPreset: %v", err)
	}
	if _, err := os.Stat(filepath.Join(detailPresetsDir(), "my ridges.yaml")); err != nil {
		t.Fatalf("preset file not written: %v", err)
	}

	// Forget everything and reload from disk.
	detailUserPresets = map[string]bool{}
	detailUserPresetNames = nil
	delete(detailPresets, "my ridges")
	LoadDetailPresets()

	got, ok := detailPresets["my ridges"]
	if !ok {
		t.Fatal("preset not registered after LoadDetailPresets")
	}
	if !detailUserPresets["my ridges"] {
		t.Error("reloaded preset not marked as a user preset")
	}
	if got.Waves != in.Waves {
		t.Errorf("waves changed: got %+v want %+v", got.Waves, in.Waves)
	}
	if got.Noise != in.Noise || got.NoiseAmplitude != in.NoiseAmplitude ||
		got.NoiseFrequency != in.NoiseFrequency || got.NoiseOctaves != in.NoiseOctaves ||
		got.NoiseTessLevel != in.NoiseTessLevel || got.EdgeTaper != in.EdgeTaper {
		t.Errorf("noise/taper changed: got %+v want %+v", got, in)
	}
	if got.ScatterDensity != in.ScatterDensity || got.ScatterScale != in.ScatterScale ||
		got.ScatterTex != in.ScatterTex {
		t.Errorf("scatter changed: got %+v want %+v", got, in)
	}
	if got.Seed != 0 {
		t.Errorf("preset carries a seed (%d); seeds are per-face", got.Seed)
	}

	// Applying it must keep the target face's own seed.
	face := DetailSpec{Seed: 1234}
	applyDetailPreset(&face, "my ridges")
	if face.Seed != 1234 {
		t.Errorf("applying preset clobbered the face seed: got %d want 1234", face.Seed)
	}
	if face.Waves != in.Waves {
		t.Errorf("applied waves = %+v, want %+v", face.Waves, in.Waves)
	}
}

// TestDetailPresetUpdate verifies saving over an existing name replaces it
// rather than adding a second entry.
func TestDetailPresetUpdate(t *testing.T) {
	withTempPresetDir(t)

	first := DetailSpec{Enabled: true, Displace: true,
		Waves: [detailMaxWaves]DetailWave{{Enabled: true, Amplitude: 0.1, Frequency: 1, TessLevel: 8}}}
	if err := SaveDetailPreset("ridges", first); err != nil {
		t.Fatalf("SaveDetailPreset: %v", err)
	}
	second := first
	second.Waves[0].Amplitude = 0.9
	if err := SaveDetailPreset("ridges", second); err != nil {
		t.Fatalf("SaveDetailPreset (update): %v", err)
	}

	if n := len(detailUserPresetNames); n != 1 {
		t.Fatalf("user preset count = %d, want 1 (%v)", n, detailUserPresetNames)
	}
	if got := detailPresets["ridges"].Waves[0].Amplitude; got != 0.9 {
		t.Errorf("amplitude after update = %v, want 0.9", got)
	}
}

// TestDetailPresetDeleteRestoresBuiltin verifies a user preset may shadow a
// built-in and that deleting it brings the built-in back.
func TestDetailPresetDeleteRestoresBuiltin(t *testing.T) {
	withTempPresetDir(t)

	builtin, ok := detailBuiltinPresets["rock"]
	if !ok {
		t.Fatal(`built-in preset "rock" missing`)
	}
	shadow := builtin
	shadow.Waves[0].Amplitude = builtin.Waves[0].Amplitude + 1
	if err := SaveDetailPreset("rock", shadow); err != nil {
		t.Fatalf("SaveDetailPreset: %v", err)
	}
	if got := detailPresets["rock"].Waves[0].Amplitude; got != shadow.Waves[0].Amplitude {
		t.Fatalf("shadowing preset not applied: amplitude %v", got)
	}

	if err := DeleteDetailPreset("rock"); err != nil {
		t.Fatalf("DeleteDetailPreset: %v", err)
	}
	if detailPresets["rock"].Waves[0].Amplitude != builtin.Waves[0].Amplitude {
		t.Error("built-in preset not restored after deleting its shadow")
	}
	if detailUserPresets["rock"] {
		t.Error("deleted preset still marked as a user preset")
	}
	if _, err := os.Stat(filepath.Join(detailPresetsDir(), "rock.yaml")); !os.IsNotExist(err) {
		t.Errorf("preset file still on disk: %v", err)
	}

	// Built-ins are not deletable.
	if err := DeleteDetailPreset("cliff"); err == nil {
		t.Error("deleting a built-in preset should fail")
	}
}

// TestDetailPresetNameSanitising verifies typed names can never escape the
// presets directory.
func TestDetailPresetNameSanitising(t *testing.T) {
	cases := map[string]string{
		"  ridges  ":       "ridges",
		"ridges.yaml":      "ridges",
		"../../etc/passwd": "etcpasswd",
		"a/b":              "ab",
		"":                 "",
		"///":              "",
		"Rock_2-b":         "Rock_2-b",
	}
	for in, want := range cases {
		if got := detailPresetName(in); got != want {
			t.Errorf("detailPresetName(%q) = %q, want %q", in, got, want)
		}
	}
}
