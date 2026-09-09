package world

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// A level with no `player:` block gets the engine's default.
func TestAbsentPlayerBlockFallsBackToTheDefault(t *testing.T) {
	p := PlayerSettingsFromRaw(nil)
	if p != (PlayerSettings{}) {
		t.Fatalf("absent block gave %+v, want the zero value", p)
	}
	if got, want := p.EffectiveStepHeight(), DefaultStepHeight(); got != want {
		t.Errorf("step height %v, want the default %v", got, want)
	}
}

// The default is a stair riser, not a body height. It was the latter, which on
// solid brush geometry meant the player could walk up onto anything shorter
// than themselves — and gave StepUp a lift tall enough to cross a ceiling with.
func TestTheDefaultStepHeightIsACurb(t *testing.T) {
	if got := DefaultStepHeight(); got != 0.5 {
		t.Errorf("DefaultStepHeight() = %v, want 0.5", got)
	}
	if body := NewPlayer(mgl32.Vec3{}); DefaultStepHeight() >= body.BoundHalfY*2 {
		t.Errorf("the default is still the body's own height (%v)", body.BoundHalfY*2)
	}
}

// A non-positive step height is the default rather than "no stepping at all":
// zero clearance would be refused by the seam between two floors that differ by
// a rounding error, so it is never what an author meant.
func TestNonPositiveStepHeightIsTheDefault(t *testing.T) {
	for _, v := range []float32{0, -1} {
		p := PlayerSettings{StepHeight: v}
		if got, want := p.EffectiveStepHeight(), DefaultStepHeight(); got != want {
			t.Errorf("step height %v gave %v, want the default %v", v, got, want)
		}
	}
}

func TestAuthoredStepHeightIsUsedAsGiven(t *testing.T) {
	v := float32(0.75)
	p := PlayerSettingsFromRaw(&RawPlayer{StepHeight: &v})
	if got := p.EffectiveStepHeight(); got != v {
		t.Errorf("step height %v, want %v", got, v)
	}
}

// Saving a level that never touched the block must write no block, or every
// level authored before this one would gain one the first time it was opened.
func TestDefaultPlayerSettingsWriteNoBlock(t *testing.T) {
	if r := PlayerSettingsToRaw(PlayerSettings{}); r != nil {
		t.Errorf("default settings wrote %+v, want no block", r)
	}
}

func TestPlayerSettingsRoundTrip(t *testing.T) {
	in := PlayerSettings{StepHeight: 0.75}
	raw := PlayerSettingsToRaw(in)
	if raw == nil || raw.StepHeight == nil {
		t.Fatalf("set settings wrote %+v, want a step height", raw)
	}
	if out := PlayerSettingsFromRaw(raw); out != in {
		t.Errorf("round trip gave %+v, want %+v", out, in)
	}
}
