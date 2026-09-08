package world

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// A level with no `player:` block behaves exactly as every level did before the
// block existed: the allowance is the body's own height.
func TestAbsentPlayerBlockFallsBackToTheBodysHeight(t *testing.T) {
	p := PlayerSettingsFromRaw(nil)
	if p != (PlayerSettings{}) {
		t.Fatalf("absent block gave %+v, want the zero value", p)
	}
	body := NewPlayer(mgl32.Vec3{})
	if got, want := p.EffectiveStepHeight(body), body.BoundHalfY*2; got != want {
		t.Errorf("step height %v, want the body's height %v", got, want)
	}
	if got, want := DefaultStepHeight(), body.BoundHalfY*2; got != want {
		t.Errorf("DefaultStepHeight() = %v, want %v — the two must agree", got, want)
	}
}

// A non-positive step height is the default rather than "no stepping at all":
// zero clearance would be refused by the seam between two floors that differ by
// a rounding error, so it is never what an author meant.
func TestNonPositiveStepHeightIsTheDefault(t *testing.T) {
	body := NewPlayer(mgl32.Vec3{})
	for _, v := range []float32{0, -1} {
		p := PlayerSettings{StepHeight: v}
		if got, want := p.EffectiveStepHeight(body), body.BoundHalfY*2; got != want {
			t.Errorf("step height %v gave %v, want the default %v", v, got, want)
		}
	}
}

func TestAuthoredStepHeightIsUsedAsGiven(t *testing.T) {
	v := float32(0.5)
	p := PlayerSettingsFromRaw(&RawPlayer{StepHeight: &v})
	if got := p.EffectiveStepHeight(NewPlayer(mgl32.Vec3{})); got != v {
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
