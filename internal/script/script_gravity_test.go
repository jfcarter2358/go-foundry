package script

// script_gravity_test.go — the scripting side of momentum and gravity:
// the `impulse` verb, the three gravity knobs as properties, and the player's
// `:landed:` / `:left_ground:` events.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
	"foundry/internal/hooks"
	"foundry/internal/world"
)

// gravityTestLevel is tickTestLevel with one plain crate to push around.
func gravityTestLevel(t *testing.T) *world.Level {
	t.Helper()
	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "crate", Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		BoundHalfX: 0.5, BoundHalfY: 0.5, BoundHalfZ: 0.5,
		Collision: world.DefaultEntityCollision(),
	})
	return lvl
}

// ---------- impulse ----------

// The whole point of the verb: it ADDS. Writing `velocity` would throw away
// whatever the body was already doing, which is why a grenade going off under a
// running player must not use it.
func TestImpulseAddsToWhateverTheBodyWasDoing(t *testing.T) {
	lvl := gravityTestLevel(t)
	e := &lvl.Entities[0]
	e.Velocity = mgl32.Vec3{3, 0, 0}

	runScript(t, lvl, "", "def main() -> null\n    impulse(@crate, <0.0, 8.0, 0.0>)\nend")

	if want := (mgl32.Vec3{3, 8, 0}); e.Velocity != want {
		t.Errorf("after the impulse the crate carries %v, want %v", e.Velocity, want)
	}
}

// Two impulses in a row accumulate, rather than the second replacing the first.
func TestImpulsesAccumulate(t *testing.T) {
	lvl := gravityTestLevel(t)
	runScript(t, lvl, "", `def main() -> null
    impulse(@crate, <1.0, 0.0, 0.0>)
    impulse(@crate, <1.0, 0.0, 0.0>)
end`)
	if got := lvl.Entities[0].Velocity.X(); got != 2 {
		t.Errorf("two impulses of 1 left %v, want 2", got)
	}
}

// It reaches the player on the same terms as anything else — that is what makes
// a blast that throws a crate also throw the person standing next to it.
func TestImpulseReachesThePlayer(t *testing.T) {
	withTestPlayer(t)
	lvl := gravityTestLevel(t)

	runScript(t, lvl, "", "def main() -> null\n    impulse(player(), <0.0, 12.0, 0.0>)\nend")

	if got := world.PlayerEntity.Velocity.Y(); got != 12 {
		t.Errorf("the player carries %v after a 12-unit impulse, want 12", got)
	}
}

func TestImpulseRefusesWhatIsNotAVec(t *testing.T) {
	lvl := gravityTestLevel(t)
	err := runScriptErr(t, lvl, "", "def main() -> null\n    impulse(@crate, 4.0)\nend")
	if err == nil {
		t.Fatal("impulse accepted a number where a vec belongs")
	}
}

// ---------- gravity properties ----------

// The level's own pull, read and written. Reading a level that never set one
// reports the engine's default rather than 0, the way step_height does.
func TestLevelGravityProperty(t *testing.T) {
	lvl := gravityTestLevel(t)

	got := runScript(t, lvl, "", "def main() -> null\n    return @level.prop.gravity\nend")
	if f, ok := toFloat32(got); !ok || f != world.DefaultGravity {
		t.Errorf("a level that says nothing reads gravity %v, want %v", got, world.DefaultGravity)
	}

	runScript(t, lvl, "", "def main() -> null\n    @level.prop.gravity = 6.0\nend")
	if world.LevelGravity(lvl) != 6 {
		t.Errorf("after the write the level falls at %v, want 6", world.LevelGravity(lvl))
	}
	// Zero has to survive the round trip, or a zero-g level would read back as
	// an unset one and snap to 36.
	runScript(t, lvl, "", "def main() -> null\n    @level.prop.gravity = 0.0\nend")
	if world.LevelGravity(lvl) != 0 {
		t.Errorf("zero-g read back as %v", world.LevelGravity(lvl))
	}
}

// A volume's override, including the null that hands the region back.
func TestVolumeGravityProperty(t *testing.T) {
	lvl := gravityTestLevel(t)
	lvl.Volumes = append(lvl.Volumes, world.Volume{
		ID: "chamber", Enabled: true, HalfX: 4, HalfY: 4, HalfZ: 4,
	})

	got := runScript(t, lvl, "", "def main() -> null\n    return @chamber.prop.gravity\nend")
	if _, isNull := got.(flang.NullVal); !isNull {
		t.Errorf("a volume with no override reads %v, want null", got)
	}

	runScript(t, lvl, "", "def main() -> null\n    @chamber.prop.gravity = 0.0\nend")
	if lvl.Volumes[0].Gravity == nil || *lvl.Volumes[0].Gravity != 0 {
		t.Fatalf("the chamber's override is %v, want 0", lvl.Volumes[0].Gravity)
	}
	if got := world.GravityAt(lvl, mgl32.Vec3{}); got != 0 {
		t.Errorf("inside the chamber gravity is %v, want 0", got)
	}

	runScript(t, lvl, "", "def main() -> null\n    @chamber.prop.gravity = null\nend")
	if lvl.Volumes[0].Gravity != nil {
		t.Error("writing null left the override in place")
	}
	if got := world.GravityAt(lvl, mgl32.Vec3{}); got != world.DefaultGravity {
		t.Errorf("after null the chamber gives %v, want the level's %v", got, world.DefaultGravity)
	}
}

// The body's scale and cap, and the read-only `gravity` that reports the answer
// all three produce.
func TestEntityGravityScaleAndAnswer(t *testing.T) {
	lvl := gravityTestLevel(t)
	moon := float32(6)
	lvl.Gravity = &moon

	runScript(t, lvl, "", `def main() -> null
    @crate.prop.gravity_scale = 0.5
    @crate.prop.max_fall_speed = 4.0
end`)
	c := lvl.Entities[0].Collision
	if c.EffectiveGravityScale() != 0.5 || c.EffectiveMaxFallSpeed() != 4 {
		t.Fatalf("the crate reads scale %v cap %v, want 0.5 and 4",
			c.EffectiveGravityScale(), c.EffectiveMaxFallSpeed())
	}

	got := runScript(t, lvl, "", "def main() -> null\n    return @crate.prop.gravity\nend")
	if f, ok := toFloat32(got); !ok || f != moon*0.5 {
		t.Errorf("the crate reports falling at %v, want %v", got, moon*0.5)
	}
}

func TestEntityGravityAnswerIsReadOnly(t *testing.T) {
	lvl := gravityTestLevel(t)
	if err := runScriptErr(t, lvl, "", "def main() -> null\n    @crate.prop.gravity = 10.0\nend"); err == nil {
		t.Fatal("the derived gravity accepted a write")
	}
}

// ---------- :landed: and :left_ground: ----------

// Both edges reach the player's script, and neither fires while the footing is
// unchanged — walking along a floor must not post `:landed:` every frame.
func TestLandedAndLeftGroundFireOnTheEdges(t *testing.T) {
	root := withTempScriptDir(t)
	withTestPlayer(t)
	path := writeScript(t, root, "player.flang", `def on_landed(:landed:, speed float) -> null
    @self.attr.landings += 1
    @self.attr.impact = speed
end

def on_left(:left_ground:) -> null
    @self.attr.takeoffs += 1
end`)

	lvl := tickTestLevel(t)
	lvl.Player = world.PlayerSettings{
		Script: path,
		Attributes: world.AttrSet{
			"landings": world.IntAttr(0),
			"takeoffs": world.IntAttr(0),
			"impact":   world.FloatAttr(0),
		},
	}

	// The footing is engine state rather than session state, so it is recorded
	// AFTER the load — ScriptEventsReset clears it, which is exactly what stops
	// a fall in one level landing in the next.
	t.Cleanup(hooks.ResetPlayerGround)
	runTicks(t, lvl, 0)

	// Standing still on the ground, for several frames: no edges at all. The
	// first of them is the baseline — there is nothing before it to be an edge
	// against.
	hooks.RecordPlayerGround(true, false, 0)
	for i := 0; i < 4; i++ {
		ScriptTick(lvl, 1.0/60.0, true)
	}
	if a, _ := world.PlayerEntity.Attr("landings"); a.I != 0 {
		t.Errorf("standing on the floor posted %d landings", a.I)
	}
	if a, _ := world.PlayerEntity.Attr("takeoffs"); a.I != 0 {
		t.Errorf("standing on the floor posted %d takeoffs", a.I)
	}

	// Off the ground — jumped, or walked off a ledge; the engine reports the
	// same thing either way.
	hooks.RecordPlayerGround(false, false, 0)
	ScriptTick(lvl, 1.0/60.0, true)
	if a, _ := world.PlayerEntity.Attr("takeoffs"); a.I != 1 {
		t.Errorf("leaving the ground posted %d takeoffs, want 1", a.I)
	}

	// …and back down, hard.
	hooks.RecordPlayerGround(true, true, 21.5)
	ScriptTick(lvl, 1.0/60.0, true)
	if a, _ := world.PlayerEntity.Attr("landings"); a.I != 1 {
		t.Fatalf("landing posted %d landings, want 1", a.I)
	}
	if a, _ := world.PlayerEntity.Attr("impact"); a.F < 21 || a.F > 22 {
		t.Errorf(":landed: carried an impact of %v, want 21.5", a.F)
	}

	// Still standing: no repeat.
	ScriptTick(lvl, 1.0/60.0, true)
	if a, _ := world.PlayerEntity.Attr("landings"); a.I != 1 {
		t.Errorf("a second frame on the floor posted another landing (%d total)", a.I)
	}
}

// The first tick of a session has nothing to compare against, so it must post
// nothing — otherwise every player who spawned standing up would land at their
// own feet on frame one.
func TestTheFirstTickPostsNoFootingEvent(t *testing.T) {
	root := withTempScriptDir(t)
	withTestPlayer(t)
	path := writeScript(t, root, "player.flang", `def on_landed(:landed:, speed float) -> null
    @self.attr.landings += 1
end`)

	lvl := tickTestLevel(t)
	lvl.Player = world.PlayerSettings{
		Script:     path,
		Attributes: world.AttrSet{"landings": world.IntAttr(0)},
	}

	t.Cleanup(hooks.ResetPlayerGround)
	runTicks(t, lvl, 0)
	hooks.RecordPlayerGround(true, true, 12)
	ScriptTick(lvl, 1.0/60.0, true)
	if a, _ := world.PlayerEntity.Attr("landings"); a.I != 0 {
		t.Errorf("the first tick of the session posted %d landings, want 0", a.I)
	}
}
