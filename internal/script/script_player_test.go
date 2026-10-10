package script

// script_player_test.go — the player's own script, from the level's player
// block: that it loads, hears the events addressed to @player, and starts every
// session from the attributes the level authored.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/hooks"
	"foundry/internal/testenv"
	"foundry/internal/world"
)

func withTestPlayer(t *testing.T) {
	t.Helper()
	prev := world.PlayerEntity
	world.PlayerEntity = world.NewPlayer(mgl32.Vec3{})
	t.Cleanup(func() { world.PlayerEntity = prev })
}

// The shape a combat level needs: an enemy's `:damage:` reaches the player's
// script, and the player's fire button reaches it too.
func TestPlayerScriptHearsDamageAndFire(t *testing.T) {
	root := withTempScriptDir(t)
	withTestPlayer(t)
	player := writeScript(t, root, "player.flang", `def on_spawn(:spawn:) -> null
    @self.attr.spawned = true
end

def on_damage(:damage:, src ref, amount int) -> null
    @self.attr.health -= amount
end

def on_fire(:primary_fire:) -> null
    @self.attr.shots += 1
end`)
	enemy := writeScript(t, root, "enemy.flang", `def on_spawn(:spawn:) -> null
    @player <- (:damage:, @self, 30)
end`)

	lvl := tickTestLevel(t)
	lvl.Player = world.PlayerSettings{
		Script: player,
		Attributes: world.AttrSet{
			"health":  world.IntAttr(100),
			"shots":   world.IntAttr(0),
			"spawned": world.BoolAttr(false),
		},
	}
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "enemy", Script: enemy, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
	})

	runTicks(t, lvl, 2)
	hooks.ScriptPrimaryFireRequest(true)
	ScriptTick(lvl, 1.0/60.0, true)

	if a, _ := world.PlayerEntity.Attr("spawned"); !a.B {
		t.Error("the player's script never heard :spawn:")
	}
	if a, _ := world.PlayerEntity.Attr("health"); a.I != 70 {
		t.Errorf("player health %d after one 30-point hit, want 70", a.I)
	}
	if a, _ := world.PlayerEntity.Attr("shots"); a.I != 1 {
		t.Errorf("player fired %d times after one press, want 1", a.I)
	}
	if a, _ := lvl.Player.Attributes.Attr("health"); a.I != 100 {
		t.Errorf("the level's authored health became %d; play must not write it", a.I)
	}
}

// A level with no player script leaves @player unscripted, as it always was:
// nothing loads, and nothing is posted to it.
func TestNoPlayerScriptLeavesThePlayerUnscripted(t *testing.T) {
	withTempScriptDir(t)
	withTestPlayer(t)
	world.PlayerEntity.Script = "scripts/left_over.flang"

	lvl := tickTestLevel(t)
	runTicks(t, lvl, 1)

	if world.PlayerEntity.Script != "" {
		t.Errorf("player kept script %q from before the session", world.PlayerEntity.Script)
	}
	for _, r := range scriptedRefs(Scripts, lvl) {
		if r.A == "player" {
			t.Error("an unscripted player was listed as a scripted object")
		}
	}
}

// A type that only ever arrives by spawn — a thrown grenade — has a script
// nothing placed in the level names. It must still run: before spawn loaded
// it, the grenade's fuse was never lit and it never went off.
func TestSpawnedTypeRunsAScriptTheLevelNeverNamed(t *testing.T) {
	root := withTempScriptDir(t)
	fuse := writeScript(t, root, "fuse.flang", `def on_spawn(:spawn:) -> null
    @self.attr.lit = true
end`)
	thrower := writeScript(t, root, "thrower.flang", `def on_spawn(:spawn:) -> null
    spawn("charge", <0.0, 1.0, 0.0>)
end`)
	dir := filepath.Join(fileutil.ResourcesPath, world.EntitiesDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	def := "id: charge\nscript: " + fuse + "\nattributes:\n  lit: {type: bool, value: false}\n"
	if err := os.WriteFile(filepath.Join(dir, "charge.yaml"), []byte(def), 0o644); err != nil {
		t.Fatal(err)
	}
	world.ReloadEntityDefs()
	t.Cleanup(world.ReloadEntityDefs)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "thrower", Script: thrower, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
	})
	runTicks(t, lvl, 3)

	var charge *world.Entity
	for i := range lvl.Entities {
		if lvl.Entities[i].Type == "charge" {
			charge = &lvl.Entities[i]
		}
	}
	if charge == nil {
		t.Fatal("nothing was spawned")
	}
	if a, _ := charge.Attr("lit"); !a.B {
		t.Error("the spawned entity's script never heard :spawn:")
	}
}

// A spawned object comes into the world clean, even when it has been handed
// the name of one that was destroyed.
//
// The two are the same case in practice rather than in theory: `spawn` gives a
// new object the first free name of its type, so the grenade thrown after one
// has gone off is called `grenade` again. Everything about an object is keyed
// by that name — its scratch state, whether it has had its `:spawn:`, what it
// was overlapping, its pending timers — and every one of those inherited is a
// bug with its own symptom. The one this test measures is the worst: a second
// grenade that never hears `:spawn:` never lights its fuse and never goes off.
func TestASpawnedObjectDoesNotInheritADeadNamesakesState(t *testing.T) {
	root := withTempScriptDir(t)
	// Each charge counts its own spawn, marks its scratch, and destroys itself
	// on a timer — freeing its name for the next, which it asks for on the way
	// out. A charge that finds the mark already set, or a stray timer from the
	// one before, says so on the thrower.
	fuse := writeScript(t, root, "fuse.flang", `def on_spawn(:spawn:) -> null
    @thrower.attr.spawns += 1
    if @self.state["used"] == true
        @thrower.attr.stale = true
    end
    @self.state["used"] = true
    @self <- after(1, :go:)
    @self <- after(100000, :ghost:)   -- never due; it must not outlive this charge
end
def on_ghost(:ghost:) -> null
    @thrower.attr.haunted = true
end
def on_go(:go:) -> null
    if @thrower.attr.rounds < 2
        @thrower.attr.rounds += 1
        @thrower <- (:throw:,)
    end
    destroy(@self)
end`)
	thrower := writeScript(t, root, "thrower.flang", `def on_spawn(:spawn:) -> null
    throw()
end
def on_throw(:throw:) -> null
    throw()
end
def throw() -> null
    c = spawn("charge", <0.0, 1.0, 0.0>)
    @self.attr.thrown = @self.attr.thrown + c.id + " "
end`)
	dir := filepath.Join(fileutil.ResourcesPath, world.EntitiesDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	def := "id: charge\nscript: " + fuse + "\n"
	if err := os.WriteFile(filepath.Join(dir, "charge.yaml"), []byte(def), 0o644); err != nil {
		t.Fatal(err)
	}
	world.ReloadEntityDefs()
	t.Cleanup(world.ReloadEntityDefs)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "thrower", Script: thrower, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Attributes: world.AttrSet{
			"thrown":  world.StringAttr(""),
			"rounds":  world.IntAttr(0),
			"spawns":  world.IntAttr(0),
			"stale":   world.BoolAttr(false),
			"haunted": world.BoolAttr(false),
		},
	})
	runTicks(t, lvl, 24)

	e := entityByID(lvl, "thrower")
	if e == nil {
		t.Fatal("the thrower went missing")
	}
	thrown, _ := e.Attr("thrown")
	names := strings.Fields(thrown.S)
	if len(names) < 2 {
		t.Fatalf("only %d charges were thrown (%q); the chain did not run", len(names), thrown.S)
	}
	// It only means anything if the name really was reused.
	if names[0] != names[1] {
		t.Skipf("spawn no longer reuses a freed name (%v); this test has nothing to measure", names)
	}
	if n, _ := e.Attr("spawns"); int(n.I) != len(names) {
		t.Errorf("%d charges were thrown but :spawn: reached %d of them: one born under a dead "+
			"namesake's name never woke up", len(names), n.I)
	}
	if a, _ := e.Attr("stale"); a.B {
		t.Error("a spawned charge woke up holding the scratch of the one destroyed before it")
	}
	if a, _ := e.Attr("haunted"); a.B {
		t.Error("a timer left by a destroyed charge was delivered to the one that took its name")
	}
}

func entityByID(lvl *world.Level, id string) *world.Entity {
	for i := range lvl.Entities {
		if lvl.Entities[i].ID == id {
			return &lvl.Entities[i]
		}
	}
	return nil
}

// bone_position finds a socket in the world: through the entity's own matrix
// for anything placed, through the camera for the player's viewmodel.
func TestBonePositionFindsASocketInTheWorld(t *testing.T) {
	root := withTempScriptDir(t)
	testenv.WithoutGeometryUploads(t)
	withTestPlayer(t)
	models := filepath.Join(fileutil.ResourcesPath, "models")
	if err := os.MkdirAll(models, 0o755); err != nil {
		t.Fatal(err)
	}
	gun := `id: gun
vertices:
- [0, 0, 0]
- [0.1, 0, 0]
- [0, 0.1, 0]
- [0, 0, -1]
faces:
- v: [0, 1, 2]
- v: [0, 2, 3]
bones:
- name: muzzle
  head: [0, 0.05, -1]
  tail: [0, 0.05, -1.1]
`
	if err := os.WriteFile(filepath.Join(models, "gun.fmdl"), []byte(gun), 0o644); err != nil {
		t.Fatal(err)
	}
	path := writeScript(t, root, "probe.flang", `def on_spawn(:spawn:) -> null
    a = bone_position(@guard.model.weapon, "muzzle")
    b = bone_position(@player, "weapon", "muzzle")
    @self.attr.a = format("{} {} {}", round(a.x * 100.0), round(a.y * 100.0), round(a.z * 100.0))
    @self.attr.b = format("{} {} {}", round(b.x * 100.0), round(b.y * 100.0), round(b.z * 100.0))
end`)

	guard := world.Entity{ID: "guard", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Position: mgl32.Vec3{10, 0, 0},
		Models:   []world.EntityModel{{Name: "weapon", Path: "models/gun.fmdl", Visible: true}}}
	if err := guard.Load(); err != nil {
		t.Fatal(err)
	}
	world.PlayerEntity.Models = []world.EntityModel{{Name: "weapon", Path: "models/gun.fmdl", Visible: true}}
	if err := world.PlayerEntity.Load(); err != nil {
		t.Fatal(err)
	}
	prevCam := gfx.Cam
	t.Cleanup(func() { gfx.Cam = prevCam })
	gfx.Cam.Position = mgl32.Vec3{0, 2, 5}
	gfx.Cam.Direction = mgl32.Vec3{0, 0, -1}
	gfx.UpdateCamMatrix()

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, guard)
	runTicks(t, lvl, 2)

	g := &lvl.Entities[len(lvl.Entities)-1]
	if a, _ := g.Attr("a"); a.S != "1000 5 -100" {
		t.Errorf("guard's muzzle at %q, want its model point carried to x=10", a.S)
	}
	if b, _ := g.Attr("b"); b.S != "0 205 400" {
		t.Errorf("player's muzzle at %q, want a metre in front of the camera", b.S)
	}
}
