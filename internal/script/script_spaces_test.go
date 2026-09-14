package script

// script_spaces_test.go — every position a script can see or set is world
// space. That is a real contract, not a convenience: `distance` subtracts two
// of them, and a script has no way to know that a sector's stored coordinates
// are chunk-local or that an entity's are relative to the vehicle it is
// standing in.

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
	"foundry/internal/world"
)

// brushLevel is hostTestLevel with its chunk bounds computed. A brush's own
// bounds come straight off its verts, so unlike the sector fixture this needs
// no geometry load to have a position at all.
func brushLevel(t *testing.T) *world.Level {
	t.Helper()
	lvl := hostTestLevel(t)
	ch := &lvl.Chunks[0]
	ch.Scale = 1
	ch.RecomputeBounds()
	return lvl
}

// roomCentre is the local centre of the brush @room names — what a position
// verb reports for it, carried through the chunk's placement.
func roomCentre(lvl *world.Level) mgl32.Vec3 {
	lo, hi := lvl.Chunks[0].Brushes[0].LocalAABB()
	return lo.Add(hi).Mul(0.5)
}

// A sector on a moved chunk has to report where it actually is. Its stored
// MinBound/MaxBound/Floor.Height are chunk-local, so without the conversion
// `distance` measures to where the chunk would be if it had never been placed.
func TestBrushPositionIsWorldSpace(t *testing.T) {
	lvl := brushLevel(t)
	ch := &lvl.Chunks[0]

	// Park an entity exactly on the brush's own centre, in WORLD terms.
	origin := mgl32.Vec3{100, 0, 200}
	ch.Origin = origin
	lvl.Entities[0].Position = roomCentre(lvl).Add(origin)

	got := runScript(t, lvl, "",
		"def main() -> null\n    return distance(@crate, @room)\nend")
	if !flang.Equal(got, flang.FloatVal(0)) {
		t.Errorf("distance from an entity to the brush it is standing on = %v, want 0 — "+
			"the brush's chunk-local coordinates are being reported as world", got)
	}
}

// The same level with the chunk left at the origin must be unaffected: the
// conversion has to be the identity for content that has never been moved.
func TestBrushPositionUnchangedForAnUnmovedChunk(t *testing.T) {
	lvl := brushLevel(t)
	lvl.Entities[0].Position = roomCentre(lvl)

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

// local_position is the deliberate exception: a rider's own movement has to be
// measurable without the ship's. Moving the ship must move `position` and leave
// `local_position` exactly where it was — the player script's walk/idle choice
// depends on it.
func TestEntityLocalPositionIgnoresTheFrameMoving(t *testing.T) {
	lvl := framedLevel(t, mgl32.Vec3{50, 0, 0})
	lvl.Entities[0].Position = mgl32.Vec3{1, 2, 3}

	read := func(prop string) mgl32.Vec3 {
		got := runScript(t, lvl, "",
			"def main() -> null\n    return @crate.prop."+prop+"\nend")
		v, ok := got.(flang.VecVal)
		if !ok {
			t.Fatalf("%s prop returned %T, want a vec", prop, got)
		}
		return vec3Of(v)
	}

	if got := read("local_position"); !vecClose(got, mgl32.Vec3{1, 2, 3}, 1e-4) {
		t.Errorf("local_position = %v, want the stored frame-local <1,2,3>", got)
	}

	// The ship moves; the crate does not move within it.
	world.FindEntity(lvl, "ship").Position = mgl32.Vec3{80, -10, 5}
	world.RefreshFrames(lvl, lvl.Frames)
	if got := read("local_position"); !vecClose(got, mgl32.Vec3{1, 2, 3}, 1e-4) {
		t.Errorf("local_position after the ship moved = %v, want <1,2,3> unchanged", got)
	}
	if got := read("position"); vecClose(got, mgl32.Vec3{51, 2, 3}, 1e-4) {
		t.Errorf("position after the ship moved = %v, still the old world value — "+
			"the test is not moving the frame", got)
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

// Nothing above may disturb the ordinary no-frame case: the conversion has to
// be the identity for a level with no vehicles in it, which is most of them.
func TestPositionPropUnchangedWithoutAFrame(t *testing.T) {
	lvl := hostTestLevel(t)
	runScript(t, lvl, "",
		"def main() -> null\n    @crate.prop.position = <1.0,2.0,3.0>\n    return null\nend")
	if !vecClose(lvl.Entities[0].Position, mgl32.Vec3{1, 2, 3}, 1e-4) {
		t.Errorf("position written on an unframed entity stored %v, want <1,2,3>",
			lvl.Entities[0].Position)
	}
}

// Velocity is stored in the entity's frame, because it is integrated onto a
// position that is. The property is world space, so a script can aim one
// entity at another without knowing either is aboard a ship.
func TestEntityVelocityPropIsWorldSpace(t *testing.T) {
	lvl := framedLevel(t, mgl32.Vec3{50, 0, 0})
	// Yaw the ship a quarter turn: its local +X points along world -Z.
	lvl.Frames.Frame("ship").Rot = mgl32.QuatRotate(math.Pi/2, mgl32.Vec3{0, 1, 0})
	lvl.Entities[0].Velocity = mgl32.Vec3{2, 0, 0} // 2 u/s down the ship's own +X

	got := runScript(t, lvl, "",
		"def main() -> null\n    return @crate.prop.velocity\nend")
	v, ok := got.(flang.VecVal)
	if !ok {
		t.Fatalf("velocity prop returned %T, want a vec", got)
	}
	if !vecClose(vec3Of(v), mgl32.Vec3{0, 0, -2}, 1e-4) {
		t.Errorf("velocity of a frame-parented entity = %v, want <0,0,-2> (its world velocity)",
			vec3Of(v))
	}
}

// Writing it back stores the frame-local value, and reads back as what was
// written — a rider pushed along world +X keeps going that way whatever the
// ship's heading.
func TestEntityVelocityPropRoundTripsThroughItsFrame(t *testing.T) {
	lvl := framedLevel(t, mgl32.Vec3{50, 0, -20})
	lvl.Frames.Frame("ship").Rot = mgl32.QuatRotate(math.Pi/2, mgl32.Vec3{0, 1, 0})

	runScript(t, lvl, "",
		"def main() -> null\n    @crate.prop.velocity = <3.0,0.0,0.0>\n    return null\nend")

	if !vecClose(lvl.Entities[0].Velocity, mgl32.Vec3{0, 0, 3}, 1e-4) {
		t.Errorf("stored velocity = %v, want the world <3,0,0> carried into the yawed frame <0,0,3>",
			lvl.Entities[0].Velocity)
	}
	got := runScript(t, lvl, "",
		"def main() -> null\n    return @crate.prop.velocity\nend")
	if v := got.(flang.VecVal); !vecClose(vec3Of(v), mgl32.Vec3{3, 0, 0}, 1e-4) {
		t.Errorf("read back %v, want the <3,0,0> that was written", vec3Of(v))
	}
}

// Nothing above may disturb the ordinary no-frame case: the conversion has to
// be the identity for a level with no vehicles in it, which is most of them.
func TestVelocityPropUnchangedWithoutAFrame(t *testing.T) {
	lvl := hostTestLevel(t)
	runScript(t, lvl, "",
		"def main() -> null\n    @crate.prop.velocity = <1.0,2.0,3.0>\n    return null\nend")
	if !vecClose(lvl.Entities[0].Velocity, mgl32.Vec3{1, 2, 3}, 1e-4) {
		t.Errorf("velocity written on an unframed entity stored %v, want <1,2,3>", lvl.Entities[0].Velocity)
	}
}

// The floor/ceiling `height`/`world_height` property pair is gone with
// sectors: a brush's shape is its verts, not a height field, so there is no
// single authored number for a script to read or write. See
// script_props_geom.go's brushProps.

func vec3Of(v flang.VecVal) mgl32.Vec3 {
	return mgl32.Vec3{float32(v[0]), float32(v[1]), float32(v[2])}
}

func vecClose(a, b mgl32.Vec3, tol float32) bool {
	d := a.Sub(b)
	return d.Len() <= tol
}
