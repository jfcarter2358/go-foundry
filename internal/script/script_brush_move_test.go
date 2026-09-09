package script

// script_brush_move_test.go — moving a brush from a script.
//
// The chunk counterpart of this is script_chunk_move_test.go and the two are
// deliberately similar: a placement written by a script, applied to authored
// geometry that is never rewritten, with the spatial hash re-bucketed by the
// setter rather than deferred.
//
// The one thing a chunk has no counterpart for is the GATE. A brush's corners
// are baked into the compiled level's BSP and clipped against its neighbours,
// so moving one that was not marked dynamic would move a shape the compiled
// world no longer agrees exists — which works in the editor and silently does
// nothing in a shipped build. So the write is refused outright, and that
// refusal is most of what is tested here.

import (
	"math"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
	"foundry/internal/world"
)

// dynamicBrushLevel is the shared fixture with its "room" brush marked dynamic.
func dynamicBrushLevel(t *testing.T) *world.Level {
	t.Helper()
	lvl := boundedTestLevel(t)
	lvl.Chunks[0].Brushes[0].Dynamic = true
	return lvl
}

func TestBrushPositionReadsItsCentreAndWritesItsPlacement(t *testing.T) {
	lvl := dynamicBrushLevel(t)
	b := &lvl.Chunks[0].Brushes[0]
	authored := append([]mgl32.Vec3(nil), b.Verts...)
	rest := b.Position()

	got := runScript(t, lvl, "", "def main() -> null\n    return @room.prop.position\nend")
	if !flang.Equal(got, flang.VecVal{float64(rest.X()), float64(rest.Y()), float64(rest.Z())}) {
		t.Errorf("an unmoved brush reads back %v, want its centre %v", got, rest)
	}

	runScript(t, lvl, "", script("    @room.prop.position = <20.0, 3.0, -5.0>"))
	if pos := b.Position(); !vecNear(pos, mgl32.Vec3{20, 3, -5}, 1e-4) {
		t.Fatalf("position is %v, want the written vec", pos)
	}
	// The authored corners are the level file's contents and must be untouched:
	// the move is a placement on top of them.
	for i := range b.Verts {
		if b.Verts[i] != authored[i] {
			t.Fatalf("vert %d was rewritten to %v; the write should have gone to the placement",
				i, b.Verts[i])
		}
	}

	got = runScript(t, lvl, "", "def main() -> null\n    return @room.prop.position\nend")
	if !flang.Equal(got, flang.VecVal{20, 3, -5}) {
		t.Errorf("a moved brush reads back %v, want <20,3,-5>", got)
	}
}

// The gate: the same write on a brush that was not marked dynamic is an error
// the author can see, naming the toggle that would allow it.
func TestWritingAPlacementOnAStaticBrushIsRefused(t *testing.T) {
	for _, prop := range []string{"position", "rotation", "velocity", "angular_velocity"} {
		lvl := boundedTestLevel(t) // "room" is NOT dynamic
		err := runScriptErr(t, lvl, "", script("    @room.prop."+prop+" = <1.0, 0.0, 0.0>"))
		if err == nil {
			t.Fatalf("writing %s on a static brush was allowed", prop)
		}
		if !strings.Contains(err.Error(), "not dynamic") {
			t.Errorf("writing %s failed with %q, want it to name the dynamic toggle", prop, err)
		}
		if !lvl.Chunks[0].Brushes[0].AtRest() {
			t.Errorf("writing %s moved the brush anyway", prop)
		}
	}
}

// Reading is not gated — a script may perfectly well ask where a wall is.
func TestReadingAPlacementIsAllowedOnAnyBrush(t *testing.T) {
	lvl := boundedTestLevel(t)
	got := runScript(t, lvl, "", "def main() -> null\n    return @room.prop.dynamic\nend")
	if !flang.Equal(got, flang.BoolVal(false)) {
		t.Errorf("dynamic reads back %v on a static brush, want false", got)
	}
	if runScript(t, lvl, "", "def main() -> null\n    return @room.prop.position\nend") == nil {
		t.Error("reading position on a static brush returned nothing")
	}
}

// The point of writing a position at all: after it, world-space lookups find
// the brush where it now is. The setter re-buckets the hash itself rather than
// deferring, so there is no drain step in between.
func TestMovingABrushCarriesItThroughTheSpatialHash(t *testing.T) {
	lvl := dynamicBrushLevel(t)
	b := &lvl.Chunks[0].Brushes[0]

	before := lvl.Chunks[0].MaxBound
	runScript(t, lvl, "", script("    @room.prop.position = <0.0, 60.0, 0.0>"))
	after := lvl.Chunks[0].MaxBound

	if after.Y() <= before.Y() {
		t.Errorf("chunk bounds top %v -> %v; a brush lifted 60 units should have grown them",
			before.Y(), after.Y())
	}
	lo, hi := b.LocalAABB()
	if lo.Y() > 60 || hi.Y() < 60 {
		t.Errorf("the brush's own bounds are %v..%v, which do not contain where it was sent", lo, hi)
	}
}

// Velocity is a rate: writing it moves nothing this instant, and the engine's
// own tick is what carries the brush along afterwards.
func TestBrushVelocityIsIntegratedByTheEngine(t *testing.T) {
	lvl := dynamicBrushLevel(t)
	b := &lvl.Chunks[0].Brushes[0]
	rest := b.Position()

	runScript(t, lvl, "", script("    @room.prop.velocity = <0.0, 2.0, 0.0>"))
	if !vecNear(b.Position(), rest, 1e-5) {
		t.Fatalf("writing a velocity moved the brush immediately, to %v", b.Position())
	}

	world.AdvanceBrushes(lvl, 0.5)
	if got := b.Position().Y() - rest.Y(); got < 0.99 || got > 1.01 {
		t.Errorf("after half a second at 2/s the brush rose %v, want 1", got)
	}
}

// Angular velocity is the same bargain one level round: a rate that turns
// nothing at the instant it is written, integrated onto `rotation` by the
// engine's own tick. Unlike `velocity` it is in the brush's own Euler basis,
// so what a script writes is what `rotation` grows by.
func TestBrushAngularVelocityIsIntegratedByTheEngine(t *testing.T) {
	lvl := dynamicBrushLevel(t)
	b := &lvl.Chunks[0].Brushes[0]

	runScript(t, lvl, "", script("    @room.prop.angular_velocity = <0.0, 2.0, 0.0>"))
	if b.Rotation != (mgl32.Vec3{}) {
		t.Fatalf("writing an angular velocity turned the brush immediately, to %v", b.Rotation)
	}
	got := runScript(t, lvl, "", "def main() -> null\n    return @room.prop.angular_velocity\nend")
	if !flang.Equal(got, flang.VecVal{0, 2, 0}) {
		t.Errorf("angular_velocity reads back %v, want <0,2,0>", got)
	}

	world.AdvanceBrushes(lvl, 0.5)
	if y := b.Rotation.Y(); y < 0.99 || y > 1.01 {
		t.Errorf("after half a second at 2 rad/s the brush turned %v, want 1", y)
	}
	// A turn moves the pivot nowhere: rotation is about the brush's own centre.
	if !vecNear(b.Position(), b.Pivot(), 1e-5) {
		t.Errorf("turning the brush moved its centre to %v, want its pivot %v", b.Position(), b.Pivot())
	}
}

// A brush left spinning must not accumulate an angle whose float32 resolution
// has gone coarse — the wrap Entity.Integrate does, done here for the same
// reason.
func TestBrushAngularVelocityWrapsRatherThanGrowing(t *testing.T) {
	lvl := dynamicBrushLevel(t)
	b := &lvl.Chunks[0].Brushes[0]

	runScript(t, lvl, "", script("    @room.prop.angular_velocity = <0.0, 3.0, 0.0>"))
	for i := 0; i < 20; i++ {
		world.AdvanceBrushes(lvl, 1)
	}
	if y := b.Rotation.Y(); y < -math.Pi || y > math.Pi {
		t.Errorf("after 60 radians of spin the rotation is %v, want it wrapped into [-pi, pi]", y)
	}
}

// vecNear reports whether two vectors agree within tol on every axis.
func vecNear(a, b mgl32.Vec3, tol float32) bool {
	d := a.Sub(b)
	return mgl32.Abs(d.X()) <= tol && mgl32.Abs(d.Y()) <= tol && mgl32.Abs(d.Z()) <= tol
}
