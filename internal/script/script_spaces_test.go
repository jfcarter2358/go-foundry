package script

// script_spaces_test.go — every position a script can see or set is world
// space. That is a real contract, not a convenience: `distance` subtracts two
// of them, and a script has no way to know that a sector's stored coordinates
// are chunk-local or that an entity's are relative to the vehicle it is
// standing in.

import (
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
	"foundry/internal/world"
)

// sectorLevel is hostTestLevel with the room's bounds actually computed —
// leveltest.EdgeIDChunk builds the sector by hand and never calls Load, so
// MinBound/MaxBound would otherwise both be the zero vector and every
// position derived from them would be the origin by accident.
func sectorLevel(t *testing.T) *world.Level {
	t.Helper()
	lvl := hostTestLevel(t)
	ch := &lvl.Chunks[0]
	ch.Scale = 1
	if err := world.ReloadSector(ch, &ch.Sectors[0]); err != nil {
		t.Fatalf("ReloadSector: %v", err)
	}
	return lvl
}

// A sector on a moved chunk has to report where it actually is. Its stored
// MinBound/MaxBound/Floor.Height are chunk-local, so without the conversion
// `distance` measures to where the chunk would be if it had never been placed.
func TestSectorPositionIsWorldSpace(t *testing.T) {
	lvl := sectorLevel(t)
	ch := &lvl.Chunks[0]

	// The room spans local [0,10]², floor at 0 → local centre (5, 0, 5).
	// Park an entity exactly on that centre in WORLD terms.
	origin := mgl32.Vec3{100, 0, 200}
	ch.Origin = origin
	lvl.Entities[0].Position = mgl32.Vec3{5, 0, 5}.Add(origin)

	got := runScript(t, lvl, "",
		"def main() -> null\n    return distance(@crate, @room)\nend")
	if !flang.Equal(got, flang.FloatVal(0)) {
		t.Errorf("distance from an entity to the sector it is standing on = %v, want 0 — "+
			"the sector's chunk-local coordinates are being reported as world", got)
	}
}

// The same level with the chunk left at the origin must be unaffected: the
// conversion has to be the identity for content that has never been moved.
func TestSectorPositionUnchangedForAnUnmovedChunk(t *testing.T) {
	lvl := sectorLevel(t)
	lvl.Entities[0].Position = mgl32.Vec3{5, 0, 5}

	got := runScript(t, lvl, "",
		"def main() -> null\n    return distance(@crate, @room)\nend")
	if !flang.Equal(got, flang.FloatVal(0)) {
		t.Errorf("distance on an unmoved chunk = %v, want 0", got)
	}
}

// framedLevel puts the crate inside a vehicle's frame, offset from the world by
// where the vehicle is parked — built through the real BuildFrames path rather
// than by hand, so the frame is the same shape the engine makes.
func framedLevel(t *testing.T, frameOffset mgl32.Vec3) *world.Level {
	t.Helper()
	lvl := hostTestLevel(t)
	lvl.Chunks[0].FrameOwner = "ship"

	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "ship", Position: frameOffset, Scale: mgl32.Vec3{1, 1, 1},
		Vehicle: &world.VehicleSpec{
			Mode:      world.VehicleSpace,
			Interiors: []string{lvl.Chunks[0].ID},
			Drive:     world.DefaultDriveSpec(world.VehicleSpace),
		},
	})
	// The crate rides inside it.
	lvl.Entities[0].Frame = "ship"

	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatalf("RebuildIndexes: %v", err)
	}
	// Sanity: the fixture is only meaningful if the frame really is offset.
	if got := lvl.Frames.Frame("ship").ToWorld(mgl32.Vec3{}); !vecClose(got, frameOffset, 1e-4) {
		t.Fatalf("fixture frame origin = %v, want %v", got, frameOffset)
	}
	return lvl
}

// An entity's stored Position is frame-local; the property is documented world
// space and `distance` subtracts it from other world positions.
func TestEntityPositionPropIsWorldSpace(t *testing.T) {
	offset := mgl32.Vec3{50, 0, 0}
	lvl := framedLevel(t, offset)
	lvl.Entities[0].Position = mgl32.Vec3{1, 2, 3} // inside the ship

	got := runScript(t, lvl, "",
		"def main() -> null\n    return @crate.prop.position\nend")
	want := mgl32.Vec3{1, 2, 3}.Add(offset)
	v, ok := got.(flang.VecVal)
	if !ok {
		t.Fatalf("position prop returned %T, want a vec", got)
	}
	if !vecClose(vec3Of(v), want, 1e-4) {
		t.Errorf("position of a frame-parented entity = %v, want %v (its world position)",
			vec3Of(v), want)
	}
}

// Writing the property back must land the entity where the script asked, in
// world terms — and must round-trip with the getter.
func TestEntityPositionPropRoundTripsThroughItsFrame(t *testing.T) {
	offset := mgl32.Vec3{50, 0, -20}
	lvl := framedLevel(t, offset)

	runScript(t, lvl, "",
		"def main() -> null\n    @crate.prop.position = <7.0,0.0,9.0>\n    return null\nend")

	// Stored frame-local...
	if !vecClose(lvl.Entities[0].Position, mgl32.Vec3{7, 0, 9}.Sub(offset), 1e-4) {
		t.Errorf("stored position = %v, want the world target carried into the frame %v",
			lvl.Entities[0].Position, mgl32.Vec3{7, 0, 9}.Sub(offset))
	}
	// ...and reads back as the world value that was written.
	got := runScript(t, lvl, "",
		"def main() -> null\n    return @crate.prop.position\nend")
	v := got.(flang.VecVal)
	if !vecClose(vec3Of(v), mgl32.Vec3{7, 0, 9}, 1e-4) {
		t.Errorf("read back %v, want the <7,0,9> that was written", vec3Of(v))
	}
}

// move_to takes a world vec — routinely another object's position — so it has
// to carry it into the target's own frame.
func TestMoveToCarriesIntoTheEntitysFrame(t *testing.T) {
	offset := mgl32.Vec3{0, 30, 0}
	lvl := framedLevel(t, offset)

	runScript(t, lvl, "",
		"def main() -> null\n    move_to(@crate, <0.0,30.0,0.0>)\n    return null\nend")

	if !vecClose(lvl.Entities[0].Position, mgl32.Vec3{}, 1e-4) {
		t.Errorf("move_to stored %v; the world target equals the frame's own origin, "+
			"so the frame-local position should be zero", lvl.Entities[0].Position)
	}
}

// Nothing above may disturb the ordinary no-frame case.
func TestMoveToUnchangedWithoutAFrame(t *testing.T) {
	lvl := hostTestLevel(t)
	runScript(t, lvl, "",
		"def main() -> null\n    move_to(@crate, <1.0,2.0,3.0>)\n    return null\nend")
	if !vecClose(lvl.Entities[0].Position, mgl32.Vec3{1, 2, 3}, 1e-4) {
		t.Errorf("move_to on an unframed entity stored %v, want <1,2,3>", lvl.Entities[0].Position)
	}
}

// ---------- floor/ceiling height ----------
//
// `height` is the authored, chunk-local value; `world_height` is its
// world-space companion, so the natural comparison against a player's or
// entity's Y can be written without mixing spaces.

func TestSurfaceHeightStaysChunkLocal(t *testing.T) {
	lvl := sectorLevel(t)
	lvl.Chunks[0].Origin = mgl32.Vec3{0, 40, 0}
	lvl.Chunks[0].Sectors[0].Floor.Height = 3

	got := runScript(t, lvl, "",
		"def main() -> null\n    return @room.floor.prop.height\nend")
	if !flang.Equal(got, flang.FloatVal(3)) {
		t.Errorf("height = %v, want the authored local 3 — it must not silently "+
			"become a world value, or `height = height + 1` stops round-tripping", got)
	}
}

func TestSurfaceWorldHeightFollowsTheChunk(t *testing.T) {
	lvl := sectorLevel(t)
	lvl.Chunks[0].Sectors[0].Floor.Height = 3

	// Lifted and scaled: world Y is origin.Y + height*scale.
	lvl.Chunks[0].Origin = mgl32.Vec3{0, 40, 0}
	lvl.Chunks[0].Scale = 2

	got := runScript(t, lvl, "",
		"def main() -> null\n    return @room.floor.prop.world_height\nend")
	if !flang.Equal(got, flang.FloatVal(46)) {
		t.Errorf("world_height = %v, want 46 (origin 40 + height 3 * scale 2)", got)
	}
}

// On an unmoved chunk the two must agree, so a level that never places its
// chunks can use either.
func TestSurfaceWorldHeightMatchesHeightWhenUnmoved(t *testing.T) {
	lvl := sectorLevel(t)
	lvl.Chunks[0].Sectors[0].Ceiling.Height = 7

	local := runScript(t, lvl, "",
		"def main() -> null\n    return @room.ceiling.prop.height\nend")
	world := runScript(t, lvl, "",
		"def main() -> null\n    return @room.ceiling.prop.world_height\nend")
	if !flang.Equal(local, world) {
		t.Errorf("on an unmoved chunk height = %v but world_height = %v; they must agree", local, world)
	}
}

// Ceilings get the companion too — surfaceProps builds one table for both.
func TestCeilingHasWorldHeight(t *testing.T) {
	lvl := sectorLevel(t)
	lvl.Chunks[0].Origin = mgl32.Vec3{0, 10, 0}
	lvl.Chunks[0].Sectors[0].Ceiling.Height = 4

	got := runScript(t, lvl, "",
		"def main() -> null\n    return @room.ceiling.prop.world_height\nend")
	if !flang.Equal(got, flang.FloatVal(14)) {
		t.Errorf("ceiling world_height = %v, want 14", got)
	}
}

// Read-only: `height` remains the one writable height, so a script cannot set
// the two against each other.
func TestSurfaceWorldHeightIsReadOnly(t *testing.T) {
	lvl := sectorLevel(t)
	err := runScriptErr(t, lvl, "",
		"def main() -> null\n    @room.floor.prop.world_height = 5.0\n    return null\nend")
	if err == nil {
		t.Fatal("assigning world_height succeeded; it is meant to be read-only")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Errorf("diagnostic does not say why the write was refused: %v", err)
	}
}

// vec3Of narrows a script vec (float64 triple) to the engine's Vec3.
func vec3Of(v flang.VecVal) mgl32.Vec3 {
	return mgl32.Vec3{float32(v[0]), float32(v[1]), float32(v[2])}
}

func vecClose(a, b mgl32.Vec3, tol float32) bool {
	d := a.Sub(b)
	return d.Len() <= tol
}
