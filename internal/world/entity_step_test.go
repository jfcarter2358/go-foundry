package world

// entity_step_test.go — the drawing side of a step ease. The resolve that feeds
// it is tested in internal/collision (step_ease_test.go).

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// The same half second closes the same gap at any frame rate — a walker on a
// slow machine must not float above its sill for longer.
func TestStepOffsetEaseIsFrameRateIndependent(t *testing.T) {
	remaining := func(hz int, seconds float32) float32 {
		e := Entity{StepOffset: -0.5}
		dt := 1 / float32(hz)
		for i := 0; i < int(seconds*float32(hz)+0.5); i++ {
			e.EaseStepOffset(dt)
		}
		return e.StepOffset
	}
	for _, at := range []float32{0.1, 0.2, 0.3} {
		slow, fast := remaining(30, at), remaining(240, at)
		if d := float32(math.Abs(float64(slow - fast))); d > 0.02 {
			t.Errorf("after %vs: %v at 30 Hz, %v at 240 Hz", at, slow, fast)
		}
	}
	if got := remaining(60, 0.5); got != 0 {
		t.Errorf("half a second at 60 Hz left %v", got)
	}
}

// A lift taller than a stride is capped, so a teleport the resolve happened to
// take part in does not animate a body rising through the floor.
func TestNoteStepLiftCapsTheOffset(t *testing.T) {
	e := Entity{}
	e.NoteStepLift(3, 0.5)
	if e.StepOffset != -0.75 {
		t.Errorf("a 3 m lift under a 0.5 step gave %v, want capped at -0.75", e.StepOffset)
	}
}

// DrawMatrix moves the body straight up by the offset, in metres, whatever the
// entity's facing or scale — and is ModelMatrix exactly with no offset.
func TestDrawMatrixAppliesTheOffsetAlongUp(t *testing.T) {
	e := Entity{Position: mgl32.Vec3{1, 2, 3}, Rotation: mgl32.Vec3{0.3, 1.1, 0}, Scale: mgl32.Vec3{1.5, 1.5, 1.5}}
	if e.DrawMatrix() != e.ModelMatrix() {
		t.Fatal("DrawMatrix differs from ModelMatrix with no offset")
	}
	e.StepOffset = -0.4
	p := e.ModelMatrix().Mul4x1(mgl32.Vec4{0.2, 1, -0.5, 1})
	q := e.DrawMatrix().Mul4x1(mgl32.Vec4{0.2, 1, -0.5, 1})
	if d := q.Sub(p); math.Abs(float64(d.X())) > 1e-5 || math.Abs(float64(d.Y()+0.4)) > 1e-5 || math.Abs(float64(d.Z())) > 1e-5 {
		t.Errorf("a model point moved by %v, want (0, -0.4, 0)", d)
	}
}
