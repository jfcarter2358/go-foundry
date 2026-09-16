package script

// script_chunk_move_test.go — moving a chunk from a script.
//
// `@chunk.prop.origin` is a placement, not baked geometry: a chunk's points,
// sector heights and biome paint are all chunk-local, so writing the origin
// moves everything authored in it at once and rebuilds nothing. The one thing
// that does not follow on its own is the spatial hash, whose keys are world
// cells — so these assert the write re-buckets it immediately, rather than
// deferring the way `enabled` has to.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
	"foundry/internal/world"
)

// boundedTestLevel is hostTestLevel with the bounds the spatial hash reads. The
// fixture chunk is a 10x10 room at the origin, built without them because most
// property tests never look one up by position.
func boundedTestLevel(t *testing.T) *world.Level {
	t.Helper()
	lvl := hostTestLevel(t)
	ch := &lvl.Chunks[0]
	ch.Scale = 1
	ch.MinBound, ch.MaxBound = mgl32.Vec3{0, 0, 0}, mgl32.Vec3{10, 4, 10}
	world.RebuildSpatialHash(lvl)
	return lvl
}

func TestChunkOriginPropertyReadsAndWrites(t *testing.T) {
	lvl := boundedTestLevel(t)

	got := runScript(t, lvl, "", "def main() -> null\n    return @chunk.prop.origin\nend")
	if !flang.Equal(got, flang.VecVal{0, 0, 0}) {
		t.Errorf("an unmoved chunk reads back %v, want <0,0,0>", got)
	}

	runScript(t, lvl, "", script("    @chunk.prop.origin = <20.0, 3.0, -5.0>"))
	if lvl.Chunks[0].Origin != (mgl32.Vec3{20, 3, -5}) {
		t.Fatalf("origin is %v, want the written vec", lvl.Chunks[0].Origin)
	}

	got = runScript(t, lvl, "", "def main() -> null\n    return @chunk.prop.origin\nend")
	if !flang.Equal(got, flang.VecVal{20, 3, -5}) {
		t.Errorf("a moved chunk reads back %v, want <20,3,-5>", got)
	}
}

func TestMovingAChunkCarriesItThroughTheSpatialHash(t *testing.T) {
	// The point of the property: after the write, world-space lookups — which
	// is collision, entity location and the script location verbs — find the
	// chunk where it now is and not where it was. No drain step in between, on
	// purpose: re-bucketing is CPU work and the setter does it itself.
	lvl := boundedTestLevel(t)

	inside := mgl32.Vec3{5, 1, 5}
	if ch := world.FindChunkSH(lvl.Spatial, lvl.Chunks, inside); ch == nil {
		t.Fatal("fixture: the chunk is not findable at its own position to begin with")
	}

	runScript(t, lvl, "", script("    @chunk.prop.origin = <200.0, 0.0, -100.0>"))

	moved := inside.Add(mgl32.Vec3{200, 0, -100})
	if ch := world.FindChunkSH(lvl.Spatial, lvl.Chunks, moved); ch == nil {
		t.Error("the moved chunk is not findable at its new position; the hash was not re-bucketed")
	}
	if ch := world.FindChunkSH(lvl.Spatial, lvl.Chunks, inside); ch != nil {
		t.Error("the chunk is still findable at the footprint it moved off")
	}
}

func TestMovingAChunkRewritesNoLocalGeometry(t *testing.T) {
	// Nothing is re-baked, which is what makes a move free and what makes the
	// authored numbers a script reads back afterwards still the authored ones.
	lvl := boundedTestLevel(t)
	vertsBefore := append([]mgl32.Vec3(nil), lvl.Chunks[0].Brushes[0].Verts...)
	minBefore, maxBefore := lvl.Chunks[0].MinBound, lvl.Chunks[0].MaxBound

	runScript(t, lvl, "", script("    @chunk.prop.origin = <60.0, 0.0, 60.0>"))

	ch := &lvl.Chunks[0]
	for i := range vertsBefore {
		if ch.Brushes[0].Verts[i] != vertsBefore[i] {
			t.Fatalf("vert %d moved to %v; chunk-local coordinates must not be rewritten", i, ch.Brushes[0].Verts[i])
		}
	}
	if ch.MinBound != minBefore || ch.MaxBound != maxBefore {
		t.Errorf("chunk-local bounds became %v..%v; they are local and do not move", ch.MinBound, ch.MaxBound)
	}
	// The world-space companion is where the move shows up.
	if got := ch.LocalToWorld(mgl32.Vec3{5, 0, 5}); got != (mgl32.Vec3{65, 0, 65}) {
		t.Errorf("local (5,0,5) carries out to %v, want (65,0,65)", got)
	}
}

func TestMovingAChunkCarriesAnAttachedEntity(t *testing.T) {
	// world.CarryAttachedEntities is expected to be called from every writer
	// of Chunk.Origin, and this is the script-property one.
	lvl := boundedTestLevel(t)
	crate := world.FindEntity(lvl, "crate")
	crate.AttachedChunk = "chunk"
	crate.Position = mgl32.Vec3{1, 2, 3}

	runScript(t, lvl, "", script("    @chunk.prop.origin = <20.0, 3.0, -5.0>"))

	got := world.FindEntity(lvl, "crate").Position
	want := mgl32.Vec3{21, 5, -2}
	if got != want {
		t.Errorf("attached crate is at %v, want %v", got, want)
	}
}

func TestChunkOriginRefusesANonVector(t *testing.T) {
	lvl := boundedTestLevel(t)
	err := runScriptErr(t, lvl, "", script("    @chunk.prop.origin = 4.0"))
	if err == nil {
		t.Fatal("writing a float to origin was accepted")
	}
	if lvl.Chunks[0].Origin != (mgl32.Vec3{}) {
		t.Errorf("the refused write moved the chunk anyway, to %v", lvl.Chunks[0].Origin)
	}
}

func TestRewritingTheSameOriginKeepsTheHashUsable(t *testing.T) {
	// The setter skips the rehash when the value has not changed — a script
	// holding a chunk still writes this every frame. The skip must not be
	// reachable in a state where the hash still needed rebuilding.
	lvl := boundedTestLevel(t)
	runScript(t, lvl, "", script("    @chunk.prop.origin = <40.0, 0.0, 0.0>"))
	runScript(t, lvl, "", script("    @chunk.prop.origin = <40.0, 0.0, 0.0>"))

	if ch := world.FindChunkSH(lvl.Spatial, lvl.Chunks, mgl32.Vec3{45, 1, 5}); ch == nil {
		t.Error("the chunk is not findable after a second, identical write")
	}
}

func TestChunkVelocityPropertyReadsAndWrites(t *testing.T) {
	// The other half of moving a chunk: a rate the engine keeps applying,
	// rather than an origin a handler has to rewrite every frame.
	lvl := boundedTestLevel(t)

	got := runScript(t, lvl, "", "def main() -> null\n    return @chunk.prop.velocity\nend")
	if !flang.Equal(got, flang.VecVal{0, 0, 0}) {
		t.Errorf("a still chunk reads back %v, want <0,0,0>", got)
	}

	runScript(t, lvl, "", script("    @chunk.prop.velocity = <0.0, 2.0, 0.0>"))
	if lvl.Chunks[0].Velocity != (mgl32.Vec3{0, 2, 0}) {
		t.Fatalf("velocity is %v, want the written vec", lvl.Chunks[0].Velocity)
	}

	got = runScript(t, lvl, "", "def main() -> null\n    return @chunk.prop.velocity\nend")
	if !flang.Equal(got, flang.VecVal{0, 2, 0}) {
		t.Errorf("a moving chunk reads back %v, want <0,2,0>", got)
	}
}

func TestWritingChunkVelocityMovesNothingYet(t *testing.T) {
	// Writing a rate is not a move: the chunk stays exactly where it is until
	// the next tick integrates it (world.AdvanceChunks), which is also what
	// re-buckets the hash. A setter that moved the chunk on the write would
	// double-count the first frame.
	lvl := boundedTestLevel(t)
	runScript(t, lvl, "", script("    @chunk.prop.velocity = <10.0, 0.0, 0.0>"))

	if lvl.Chunks[0].Origin != (mgl32.Vec3{}) {
		t.Errorf("the chunk moved to %v on the write", lvl.Chunks[0].Origin)
	}
	if ch := world.FindChunkSH(lvl.Spatial, lvl.Chunks, mgl32.Vec3{5, 1, 5}); ch == nil {
		t.Error("the chunk stopped being findable where it still is")
	}

	// One second of play later, it has moved and the hash has followed.
	world.AdvanceChunks(lvl, 1)
	if ch := world.FindChunkSH(lvl.Spatial, lvl.Chunks, mgl32.Vec3{15, 1, 5}); ch == nil {
		t.Error("the chunk is not findable where a second of its velocity put it")
	}
}

func TestChunkVelocityRefusesANonVector(t *testing.T) {
	lvl := boundedTestLevel(t)
	err := runScriptErr(t, lvl, "", script("    @chunk.prop.velocity = 4.0"))
	if err == nil {
		t.Fatal("writing a float to velocity was accepted")
	}
	if lvl.Chunks[0].Velocity != (mgl32.Vec3{}) {
		t.Errorf("the refused write set a velocity anyway, to %v", lvl.Chunks[0].Velocity)
	}
}

func TestChunkRotationPropertyReadsAndWrites(t *testing.T) {
	lvl := boundedTestLevel(t)

	got := runScript(t, lvl, "", "def main() -> null\n    return @chunk.prop.rotation\nend")
	if !flang.Equal(got, flang.FloatVal(0)) {
		t.Errorf("an unturned chunk reads back %v, want 0", got)
	}

	runScript(t, lvl, "", script("    @chunk.prop.rotation = 1.5"))
	if lvl.Chunks[0].Rotation != 1.5 {
		t.Fatalf("rotation is %v, want the written value", lvl.Chunks[0].Rotation)
	}

	got = runScript(t, lvl, "", "def main() -> null\n    return @chunk.prop.rotation\nend")
	if !flang.Equal(got, flang.FloatVal(1.5)) {
		t.Errorf("a turned chunk reads back %v, want 1.5", got)
	}
}

// Turning a chunk moves what a world-space lookup finds there, the same way
// moving one does — see TestMovingAChunkCarriesItThroughTheSpatialHash.
func TestRotatingAChunkCarriesItThroughTheSpatialHash(t *testing.T) {
	lvl := boundedTestLevel(t)

	inside := mgl32.Vec3{5, 1, 5}
	if ch := world.FindChunkSH(lvl.Spatial, lvl.Chunks, inside); ch == nil {
		t.Fatal("fixture: the chunk is not findable at its own position to begin with")
	}

	// A quarter turn carries the 10x10 room's footprint from the +X+Z
	// quadrant into the +X-Z one: mgl32.HomogRotate3DY's +90 degrees maps
	// local (x,z) to world (z,-x), so (5,1,5) lands at (5,1,-5).
	runScript(t, lvl, "", script("    @chunk.prop.rotation = 1.5707964"))

	turned := mgl32.Vec3{5, 1, -5}
	if ch := world.FindChunkSH(lvl.Spatial, lvl.Chunks, turned); ch == nil {
		t.Error("the turned chunk is not findable at its new footprint; the hash was not re-bucketed")
	}
	if ch := world.FindChunkSH(lvl.Spatial, lvl.Chunks, inside); ch != nil {
		t.Error("the chunk is still findable at the footprint it turned off")
	}
}

func TestChunkRotationRefusesANonNumber(t *testing.T) {
	lvl := boundedTestLevel(t)
	err := runScriptErr(t, lvl, "", script("    @chunk.prop.rotation = <1.0, 0.0, 0.0>"))
	if err == nil {
		t.Fatal("writing a vec to rotation was accepted")
	}
	if lvl.Chunks[0].Rotation != 0 {
		t.Errorf("the refused write turned the chunk anyway, to %v", lvl.Chunks[0].Rotation)
	}
}

func TestRewritingTheSameRotationKeepsTheHashUsable(t *testing.T) {
	// Same short-circuit as origin's own, and the same reason it matters: the
	// skip must not be reachable in a state where the hash still needed
	// rebuilding.
	lvl := boundedTestLevel(t)
	runScript(t, lvl, "", script("    @chunk.prop.rotation = 1.5707964"))
	runScript(t, lvl, "", script("    @chunk.prop.rotation = 1.5707964"))

	if ch := world.FindChunkSH(lvl.Spatial, lvl.Chunks, mgl32.Vec3{5, 1, -5}); ch == nil {
		t.Error("the chunk is not findable after a second, identical write")
	}
}

func TestChunkAngularVelocityPropertyReadsAndWrites(t *testing.T) {
	lvl := boundedTestLevel(t)

	got := runScript(t, lvl, "", "def main() -> null\n    return @chunk.prop.angular_velocity\nend")
	if !flang.Equal(got, flang.FloatVal(0)) {
		t.Errorf("a still chunk reads back %v, want 0", got)
	}

	runScript(t, lvl, "", script("    @chunk.prop.angular_velocity = 0.5"))
	if lvl.Chunks[0].AngularVelocity != 0.5 {
		t.Fatalf("angular_velocity is %v, want the written value", lvl.Chunks[0].AngularVelocity)
	}

	got = runScript(t, lvl, "", "def main() -> null\n    return @chunk.prop.angular_velocity\nend")
	if !flang.Equal(got, flang.FloatVal(0.5)) {
		t.Errorf("a turning chunk reads back %v, want 0.5", got)
	}
}

func TestWritingChunkAngularVelocityTurnsNothingYet(t *testing.T) {
	// Velocity's own contract, one level up: writing a rate is not a turn —
	// the chunk stays exactly as it is oriented until the next tick
	// integrates it (world.AdvanceChunks).
	lvl := boundedTestLevel(t)
	runScript(t, lvl, "", script("    @chunk.prop.angular_velocity = 1.5707964"))

	if lvl.Chunks[0].Rotation != 0 {
		t.Errorf("the chunk turned to %v on the write", lvl.Chunks[0].Rotation)
	}

	world.AdvanceChunks(lvl, 1)
	if lvl.Chunks[0].Rotation == 0 {
		t.Error("a second of play did not integrate the angular velocity")
	}
}

func TestChunkAngularVelocityRefusesANonNumber(t *testing.T) {
	lvl := boundedTestLevel(t)
	err := runScriptErr(t, lvl, "", script("    @chunk.prop.angular_velocity = <1.0, 0.0, 0.0>"))
	if err == nil {
		t.Fatal("writing a vec to angular_velocity was accepted")
	}
	if lvl.Chunks[0].AngularVelocity != 0 {
		t.Errorf("the refused write set an angular velocity anyway, to %v", lvl.Chunks[0].AngularVelocity)
	}
}

func TestRotateToEasesAChunkAndRebucketsOnArrival(t *testing.T) {
	lvl := boundedTestLevel(t)
	runScript(t, lvl, "", script("    rotate_to(@chunk, <0.0, 1.5707964, 0.0>, 1.0)"))

	if lvl.Chunks[0].RotationTween == nil {
		t.Fatal("rotate_to did not start a tween")
	}
	world.AdvanceChunks(lvl, 1)
	if got := lvl.Chunks[0].Rotation; got < 1.57 || got > 1.58 {
		t.Errorf("rotation after the blend is %v, want ~1.5707964", got)
	}
	if lvl.Chunks[0].RotationTween != nil {
		t.Error("the tween did not clear on arrival")
	}
	if ch := world.FindChunkSH(lvl.Spatial, lvl.Chunks, mgl32.Vec3{5, 1, -5}); ch == nil {
		t.Error("the turned chunk is not findable at its new footprint once the tween lands")
	}
}
