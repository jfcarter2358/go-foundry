package render

import (
	"testing"

	"foundry/internal/gfx"
)

// Every listed view is one SetView takes and ViewName names back, so a tool's
// enum and what it answers cannot drift apart.
func TestEveryViewRoundTrips(t *testing.T) {
	t.Cleanup(func() { GIDebug, ForceWireframe = GIDebugOff, false })
	for _, name := range ViewNames() {
		if !IsView(name) {
			t.Errorf("IsView(%q) is false", name)
		}
		if _, err := SetView(name); err != nil {
			t.Fatalf("SetView(%q): %v", name, err)
		}
		if got := ViewName(); got != name {
			t.Errorf("SetView(%q) reads back as %q", name, got)
		}
	}
	if _, err := SetView("sepia"); err == nil || IsView("sepia") {
		t.Error("an unknown view was taken")
	}
}

// probes and field are GI's own and say so when GI is off; lighting is the
// shader's and works without it.
func TestGIViewsNoteThatGIIsOff(t *testing.T) {
	prev := gfx.GIQuality
	t.Cleanup(func() { gfx.GIQuality = prev; GIDebug, ForceWireframe = GIDebugOff, false })
	gfx.GIQuality = gfx.GIOff
	if note, _ := SetView("probes"); note == "" {
		t.Error("probes with GI off gave no note")
	}
	if note, _ := SetView("lighting"); note != "" {
		t.Errorf("lighting with GI off noted %q — it does not need GI", note)
	}
}
