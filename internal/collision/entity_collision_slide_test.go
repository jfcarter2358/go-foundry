package collision

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// A push stops only the part of a body's motion that goes into what it hit:
// two walkers brushing past each other at an angle keep sliding by rather
// than wedging, which cancelling whole world axes made them do.
func TestAPushKeepsTheSlideAlongWhatWasHit(t *testing.T) {
	e := &world.Entity{Velocity: mgl32.Vec3{3, 0, 1}}
	// Pushed back along a diagonal: the velocity heads into it on both axes.
	n := mgl32.Vec3{-1, 0, -1}.Normalize()
	entityPushOut(e, n.Mul(0.1))
	if into := e.Velocity.Dot(n); into < -1e-5 {
		t.Errorf("still moving %.3f into the body it was pushed out of", -into)
	}
	slide := mgl32.Vec3{1, 0, -1}.Normalize()
	if got, want := e.Velocity.Dot(slide), (mgl32.Vec3{3, 0, 1}).Dot(slide); absf32(got-want) > 1e-5 {
		t.Errorf("slide along the contact = %.3f, want %.3f kept", got, want)
	}
}

