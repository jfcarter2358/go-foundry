package script

// script_decal_test.go — decals reached from a script, end to end through the
// real event loop: the ref kind, the cache, the property table, the `decal` verb
// and the cap all have to agree.
//
// The verb's interesting behaviour is the one it has that no other spawning verb
// has: it takes a POINT and works out the plane itself, so most of what is
// checked here is what happens when there is a surface at that point and when
// there is not.

import (
	"strings"
	"testing"

	"foundry/internal/flang"
	"foundry/internal/leveltest"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// decalLevel is a hollow room with its geometry built, so there is a real surface
// for a decal to find. It spans (0,0,0) to (10,4,10), so the middle of its floor
// — the surface these tests aim at — is (5, 0, 5), facing up. Aiming at a CORNER
// instead would legitimately find a wall, which is not what is being tested.
func decalLevel(t *testing.T) *world.Level {
	t.Helper()
	ch := leveltest.RoomChunk(t)
	ch.ID = "chunk"
	leveltest.LoadGeometry(t, ch)
	return &world.Level{Chunks: []world.Chunk{*ch}}
}

func TestDecalVerbFindsTheSurfaceUnderThePoint(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "s.flang", `def on_spawn(:spawn:) -> null
    decal("decal_scorch", <5.0, 0.01, 5.0>, {"size": 1.0, "blend": "multiply"})
end`)

	lvl := decalLevel(t)
	lvl.Scripts = []string{path}
	runTicks(t, lvl, 2)

	if len(lvl.Decals) != 1 {
		t.Fatalf("decals placed: got %d, want 1", len(lvl.Decals))
	}
	d := &lvl.Decals[0]
	if !d.Transient {
		t.Error("a script-placed decal should be transient, so the cap can recycle it")
	}
	if got, want := d.Facing(), (mgl32.Vec3{0, 1, 0}); !vecNearDecal(got, want) {
		t.Errorf("facing: got %v, want the floor's %v — the surface under the point was not found", got, want)
	}
	if d.Width != 1 || d.Height != 1 {
		t.Errorf("size: got %vx%v, want 1x1", d.Width, d.Height)
	}
}

// Nothing there is an ANSWER, not an error: a script shooting at the sky should
// leave no hole and need no check to avoid one.
func TestDecalVerbPlacesNothingInOpenAir(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "s.flang", `def on_spawn(:spawn:) -> null
    decal("decal_scorch", <5.0, 500.0, 5.0>)
end`)

	lvl := decalLevel(t)
	lvl.Scripts = []string{path}
	runTicks(t, lvl, 2)

	if len(lvl.Decals) != 0 {
		t.Fatalf("a decal was placed in open air: %d", len(lvl.Decals))
	}
}

// An explicit normal skips the search entirely, which is what a script placing a
// mark on a surface it chose rather than one it hit needs.
func TestDecalVerbHonoursAnExplicitNormal(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "s.flang", `def on_spawn(:spawn:) -> null
    decal("decal_scorch", <5.0, 400.0, 5.0>, {"normal": <1.0, 0.0, 0.0>})
end`)

	lvl := decalLevel(t)
	lvl.Scripts = []string{path}
	runTicks(t, lvl, 2)

	if len(lvl.Decals) != 1 {
		t.Fatalf("decals placed: got %d, want 1 — an explicit normal should not need a surface", len(lvl.Decals))
	}
	if got, want := lvl.Decals[0].Facing(), (mgl32.Vec3{1, 0, 0}); !vecNearDecal(got, want) {
		t.Errorf("facing: got %v, want the given %v", got, want)
	}
}

// The cap is what keeps a firefight finite, and it has to hold within a single
// tick: a script placing a thousand marks must not build a thousand meshes and
// then throw them away.
func TestDecalVerbCapsWhatItPlaces(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "s.flang", `def on_spawn(:spawn:) -> null
    i = 0
    while i < 250
        decal("decal_scorch", <5.0, 0.01, 5.0>)
        i = i + 1
    end
end`)

	lvl := decalLevel(t)
	lvl.Scripts = []string{path}
	runTicks(t, lvl, 2)

	if len(lvl.Decals) > world.MaxTransientDecals {
		t.Errorf("decals after 250 calls: got %d, want at most %d", len(lvl.Decals), world.MaxTransientDecals)
	}
	if len(lvl.Decals) != world.MaxTransientDecals {
		t.Errorf("decals after 250 calls: got %d, want exactly the cap %d", len(lvl.Decals), world.MaxTransientDecals)
	}
}

// `permanent` opts a mark out of the cap and into the save game.
func TestDecalVerbPermanentIsNotTransient(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "s.flang", `def on_spawn(:spawn:) -> null
    decal("decal_scorch", <5.0, 0.01, 5.0>, {"permanent": true})
end`)

	lvl := decalLevel(t)
	lvl.Scripts = []string{path}
	runTicks(t, lvl, 2)

	if len(lvl.Decals) != 1 {
		t.Fatalf("decals placed: got %d, want 1", len(lvl.Decals))
	}
	if lvl.Decals[0].Transient {
		t.Error("a permanent decal is still marked transient, so the cap can drop it")
	}
}

// A misspelled option must be named rather than silently ignored — the hardest
// kind of script bug to see is the one that does nothing.
func TestDecalVerbRejectsAnUnknownOption(t *testing.T) {
	o := decalOpts{}
	if err := applyDecalOptions(&o, flang.NewDict()); err != nil {
		t.Fatalf("an empty options dict was rejected: %v", err)
	}

	d := flang.NewDict()
	d.M["sizee"] = flang.FloatVal(1)
	err := applyDecalOptions(&o, d)
	if err == nil {
		t.Fatal("a misspelled option was accepted")
	}
	// And the message points at the real one, which is the whole value of the
	// option table being a table.
	if !strings.Contains(err.Error(), "size") {
		t.Errorf("the error does not suggest the real option: %v", err)
	}
}

// A script must be able to address a decal it placed, and to reach the
// properties that need no rebuild.
func TestScriptCanReachADecalItPlaced(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "s.flang", `def on_spawn(:spawn:) -> null
    d = decal("decal_scorch", <5.0, 0.01, 5.0>)
    d.prop.alpha = 0.25
    d.prop.tint = <1.0, 0.0, 0.0>
    d.prop.lifetime = 5.0
    d.prop.visible = false
end`)

	lvl := decalLevel(t)
	lvl.Scripts = []string{path}
	runTicks(t, lvl, 2)

	if len(lvl.Decals) != 1 {
		t.Fatalf("decals placed: got %d, want 1", len(lvl.Decals))
	}
	d := &lvl.Decals[0]
	if d.Alpha != 0.25 || d.Tint != (mgl32.Vec3{1, 0, 0}) || d.Lifetime != 5 || d.Visible {
		t.Errorf("the script's writes did not reach the decal: alpha %v, tint %v, life %v, visible %v",
			d.Alpha, d.Tint, d.Lifetime, d.Visible)
	}
}

// And to find one by tag, which is what `#tag` addressing rests on.
func TestDecalsAreFoundByTag(t *testing.T) {
	lvl := decalLevel(t)
	d := world.NewDecal("sign", mgl32.Vec3{}, mgl32.Vec3{0, 0, 1})
	d.Tags = []string{"signage"}
	lvl.Decals = append(lvl.Decals, d)

	if got := world.FindDecalsByTag(lvl, "signage"); len(got) != 1 || got[0].ID != "sign" {
		t.Errorf("FindDecalsByTag: got %v", got)
	}
}

// ---------- helpers ----------

func vecNearDecal(a, b mgl32.Vec3) bool { return a.Sub(b).Len() < 1e-3 }
