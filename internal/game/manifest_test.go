package game

import "testing"

// A manifest is the one file between a built engine and a shipped game, and it
// is read exactly once, at boot, on a machine with no author sitting at it. So
// what is guarded here is that a partial manifest still boots and a broken one
// says why, rather than the round-trip.

func TestOmittedFieldsKeepTheirDefaults(t *testing.T) {
	m, err := Parse([]byte("title: Test\nstart_level: intro\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Version != Default().Version {
		t.Errorf("version = %q, want the default %q", m.Version, Default().Version)
	}
	// A window block nobody wrote must not open a 0x0 window; the engine's own
	// defaults survive because the zero here means "unset".
	if m.Window.Width != 0 || m.Window.Height != 0 {
		t.Errorf("window = %dx%d, want 0x0 (meaning: leave the engine's default)", m.Window.Width, m.Window.Height)
	}
}

func TestFullManifestParses(t *testing.T) {
	m, err := Parse([]byte(`
title: My Game
version: 1.2.3
data_dir: MyGame
start_level: intro
window:
  width: 1280
  height: 720
  fullscreen: true
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.StartLevel != "intro" {
		t.Errorf("start level = %q", m.StartLevel)
	}
	if m.Window.Width != 1280 || m.Window.Height != 720 || !m.Window.Fullscreen {
		t.Errorf("window = %+v", m.Window)
	}
	if got := m.WindowTitle(); got != "My Game 1.2.3" {
		t.Errorf("WindowTitle = %q", got)
	}
	if got := m.DataDirName(); got != "MyGame" {
		t.Errorf("DataDirName = %q", got)
	}
}

func TestRejectsWhatCannotBeDefaulted(t *testing.T) {
	for _, tc := range []struct{ name, yaml string }{
		{"no start level", "title: Test\n"},
		{"no title", "start_level: intro\n"},
		{"start level as a path", "title: Test\nstart_level: levels/intro.yaml\n"},
		{"unknown key", "title: Test\nstart_level: intro\nstart_lvel: intro\n"},
	} {
		if _, err := Parse([]byte(tc.yaml)); err == nil {
			t.Errorf("%s: parsed without error", tc.name)
		}
	}
}

// The title goes straight into a directory name, so the characters that are
// illegal in one must not reach the filesystem.
func TestDataDirNameIsPathSafe(t *testing.T) {
	m, err := Parse([]byte("title: 'Bob''s: Quest/2 '\nstart_level: intro\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := m.DataDirName(); got != "Bob's- Quest-2" {
		t.Errorf("DataDirName = %q", got)
	}
}
