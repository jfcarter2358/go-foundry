package setup_test

// The stamp has one writer, internal/pack, and this is its only reader that is
// not a shell. Round-tripped through the real writer, so the two cannot drift.

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/pack"
	"foundry/internal/setup"
)

func TestTheInstallerReadsTheStampPackWrites(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "game.yaml"), []byte("title: \"Bob's Quest\"\nversion: 2.1.0\nstart_level: intro\n"), 0644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "foundry-release-windows-amd64.exe")
	if err := os.WriteFile(exe, []byte("MZ"), 0755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "install")
	res, err := pack.Pack(pack.Options{Src: src, Out: out, Exe: exe, GOOS: "windows", Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(out, setup.StampName))
	if err != nil {
		t.Fatal(err)
	}
	got, err := setup.ParseStamp(data)
	if err != nil {
		t.Fatal(err)
	}
	want := setup.Stamp{Title: res.Stamp.Title, Version: res.Stamp.Version, Exe: res.Stamp.Exe, DataDir: res.Stamp.DataDir}
	if got != want || got.Title != "Bob's Quest" {
		t.Errorf("read %+v, pack wrote %+v", got, want)
	}
	if setup.StampName != pack.StampName {
		t.Errorf("the installer looks for %q and pack writes %q", setup.StampName, pack.StampName)
	}
}
