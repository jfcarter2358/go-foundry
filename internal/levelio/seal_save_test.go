package levelio

// seal_save_test.go — the seal box as part of a level file.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/compile"

	"github.com/go-gl/mathgl/mgl32"
)

// A level written before the seal existed opens with one fitted around it,
// compiles sealed however open it is, and from its next save keeps the box it
// was given rather than being fitted afresh.
func TestALevelWithNoSealGetsOneFittedAndKeepsIt(t *testing.T) {
	withResourcesRoot(t)

	lvl := sealedLevel()
	ch := &lvl.Chunks[0]
	ch.Brushes = append(ch.Brushes[:1], ch.Brushes[2:]...) // no roof on the only room

	path := filepath.Join(t.TempDir(), "roofless"+LevelExt)
	if err := SaveLevel(lvl, path); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "seal:") {
		t.Fatal("a level with no seal wrote one")
	}

	loaded, err := LoadLevelHeadless(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Seal == nil {
		t.Fatal("a level with no seal loaded without one")
	}
	for _, p := range []mgl32.Vec3{{-4, 0, -4}, {4, 4, 4}} {
		if !loaded.Seal.Contains(p) {
			t.Errorf("the fitted seal does not hold the room's corner %v", p)
		}
	}
	if loaded.Seal.Contains(mgl32.Vec3{80, 0, 0}) {
		t.Error("the fitted seal stretched over the switched-off chunk")
	}

	c, err := CompileLevelFile(path, compile.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Stats.Leaks) != 0 || c.Stats.InteriorLeaves == 0 {
		t.Errorf("a roofless room inside its seal: %d leaks, %d interior cells", len(c.Stats.Leaks), c.Stats.InteriorLeaves)
	}

	// Resized by the author, saved, and loaded back as they left it.
	loaded.Seal.Half = loaded.Seal.Half.Add(mgl32.Vec3{5, 0, 0})
	loaded.Seal.Disabled = true
	again := filepath.Join(t.TempDir(), "roofless_saved"+LevelExt)
	if err := SaveLevel(&loaded, again); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(again); !strings.Contains(string(data), "seal:") {
		t.Fatal("saving a level did not write its seal")
	}
	back, err := LoadLevelHeadless(again)
	if err != nil {
		t.Fatal(err)
	}
	if back.Seal == nil || !back.Seal.Half.ApproxEqual(loaded.Seal.Half) ||
		!back.Seal.Position.ApproxEqual(loaded.Seal.Position) || !back.Seal.Disabled {
		t.Errorf("seal after a round trip = %+v, want %+v", back.Seal, loaded.Seal)
	}
}
