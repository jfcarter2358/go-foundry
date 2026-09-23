package render

import (
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/world"
)

// The overlay's rows are built without imgui and without a GL context, because
// two different painters draw them — imgui in a development build and the
// runtime's own screen-space pass in a release one (internal/devui). A test
// that needed either would not be testing the thing they share.

func TestOverlayLinesReportTheCounters(t *testing.T) {
	prev := DebugStats
	t.Cleanup(func() { DebugStats = prev })

	DebugStats.FPS = 59.5
	DebugStats.ChunksLoaded, DebugStats.ChunksDrawn = 12, 3

	var b strings.Builder
	rules := 0
	for _, l := range DebugOverlayLines() {
		if l.Rule {
			rules++
			if l.Text != "" {
				t.Errorf("a rule carried text: %q", l.Text)
			}
			continue
		}
		b.WriteString(l.Text)
		b.WriteByte('\n')
	}
	out := b.String()

	for _, want := range []string{"59.5", "12 loaded / 3 drawn", "VSync:", "Pass"} {
		if !strings.Contains(out, want) {
			t.Errorf("the overlay never said %q:\n%s", want, out)
		}
	}
	if rules == 0 {
		t.Error("the overlay has no separators, so every group runs together")
	}
}

func TestOverlayLinesReportThePlayersFrameAsWellAsItsPosition(t *testing.T) {
	prev := world.PlayerEntity
	t.Cleanup(func() { world.PlayerEntity = prev })

	world.PlayerEntity = &world.Entity{ID: "player", Position: mgl32.Vec3{1, 2, 3}}
	if out := linesText(); !strings.Contains(out, "Position:") || strings.Contains(out, "N/A") {
		t.Errorf("a placed player was not reported:\n%s", out)
	}

	// A rider's stored position is in its frame's coordinates, which is not a
	// place in the world — so the eye has to be reported beside it.
	world.PlayerEntity.Frame = "dropship"
	out := linesText()
	if !strings.Contains(out, "world (eye)") {
		t.Errorf("a rider's world position was not reported:\n%s", out)
	}
	if !strings.Contains(out, "dropship") {
		t.Errorf("the frame was not named:\n%s", out)
	}

	world.PlayerEntity = nil
	if out := linesText(); !strings.Contains(out, "N/A") {
		t.Errorf("with no player the overlay said:\n%s", out)
	}
}

func linesText() string {
	var b strings.Builder
	for _, l := range DebugOverlayLines() {
		b.WriteString(l.Text)
		b.WriteByte('\n')
	}
	return b.String()
}
