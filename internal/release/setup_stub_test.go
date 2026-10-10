package release

// The Windows installer program, as the release build finds and checks it.

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/setup"
	"foundry/internal/testenv"
)

// A release.yaml written when installers were a checkbox still loads.
func TestAnOldReleaseYamlWithInstallersStillLoads(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{ConfigFileName: "platforms: [windows]\noutput: build\nprune: true\ninstallers: false\nallow_leaks: false\n"})
	if _, err := LoadConfig(root); err != nil {
		t.Errorf("an old release.yaml no longer loads: %v", err)
	}
}

// The installer is checked for before anything is compiled, like the game
// binary it installs.
func TestBuildStopsBeforeCompilingWithoutTheInstaller(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"resources/game.yaml": "title: Test Game\nstart_level: intro\n",
		filepath.Join(RuntimesDirName, PlayerFileName(Platforms[1])): "MZ",
	})
	cfg := DefaultConfig()
	cfg.Platforms = []string{"windows"}
	var out strings.Builder
	_, err := Build(context.Background(), Options{Root: root, Config: cfg, Out: &out})
	if err == nil || !strings.Contains(err.Error(), "installer") {
		t.Fatalf("err = %v, want the missing installer", err)
	}
	if strings.Contains(out.String(), "Compiling levels") {
		t.Error("the build compiled levels before finding it had no installer")
	}
}

// Built from the real source, it is accepted; any other program is not.
func TestOnlyTheInstallerProgramIsAppendedTo(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a program")
	}
	stub := filepath.Join(t.TempDir(), SetupStubFileName)
	if err := BuildSetupStub(context.Background(), testenv.RepoRoot(t), stub, io.Discard); err != nil {
		t.Fatalf("the installer does not build for Windows: %v", err)
	}
	if err := CheckSetupStub(stub); err != nil {
		t.Errorf("the real installer was refused: %v", err)
	}
	if _, err := setup.Open(stub); err != setup.ErrNoPayload {
		t.Errorf("a bare installer program opened as an installer: %v", err)
	}

	other := buildProbe(t, "")
	if err := CheckSetupStub(other); err == nil {
		t.Error("a program that is not the installer was accepted as one")
	}
	if _, err := os.Stat(stub); err != nil {
		t.Fatal(err)
	}
}
