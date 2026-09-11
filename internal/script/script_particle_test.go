package script

// script_particle_test.go — the particle verbs and properties, end to end
// through the real event loop.
//
// Headless: the effect is written to a temp particles/ directory, and
// world.Emitter.Load reads it with no GL involved. What is being asserted is
// that a script can reach an emitter at all — the five binding tables have to
// agree for that to work, and each of them is easy to forget.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/fileutil"
	"foundry/internal/particle"
	"foundry/internal/world"
)

// writeEffect puts a minimal .fpar in the temp particles/ directory and returns
// the name an emitter would use.
func writeEffect(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(fileutil.ResourcesPath, particle.ParticlesDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `id: ` + name + `
layers:
  - name: main
    rate: 60
    life_min: 5
    life_max: 5
    max: 200
`
	if err := os.WriteFile(filepath.Join(dir, name+particle.ParticleExt), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return name
}

// emitterLevel builds a level holding one scripted emitter.
func emitterLevel(t *testing.T, script, effect string) *world.Level {
	t.Helper()
	lvl := tickTestLevel(t)
	e := world.NewEmitter("fountain", mgl32.Vec3{0, 1, 0})
	e.Effect = effect
	e.Script = script
	if err := e.Load(); err != nil {
		t.Fatalf("load effect %q: %v", effect, err)
	}
	lvl.Emitters = append(lvl.Emitters, e)
	return lvl
}

// TestScriptCanReachAnEmitter is the whole binding chain in one assertion: the
// ref kind, the cache, refFor, the property table and the runtime.
func TestScriptCanReachAnEmitter(t *testing.T) {
	root := withTempScriptDir(t)
	fx := writeEffect(t, "spray")
	path := writeScript(t, root, "e.flang", `def on_spawn(:spawn:) -> null
    @self.prop.emitting = false
end`)

	lvl := emitterLevel(t, path, fx)
	runTicks(t, lvl, 3)

	if lvl.Emitters[0].Emitting {
		t.Error("the script's write to .prop.emitting did not reach the emitter")
	}
}

// TestStopEmitLeavesLiveParticlesAlone — the distinction between stop_emit and
// switching the emitter off, and the reason both exist.
func TestStopEmitLeavesLiveParticlesAlone(t *testing.T) {
	root := withTempScriptDir(t)
	fx := writeEffect(t, "spray")
	path := writeScript(t, root, "e.flang", `def on_spawn(:spawn:) -> null
    stop_emit(@self)
end`)

	lvl := emitterLevel(t, path, fx)

	// Build up a plume first, so there is something in flight for stop_emit to
	// leave alone. The script has not run yet — runTicks loads it below.
	for i := 0; i < 30; i++ {
		world.EmitterUpdate(lvl, 1.0/60.0, true)
	}
	if lvl.Emitters[0].LiveCount() == 0 {
		t.Fatal("the emitter produced nothing to begin with")
	}

	runTicks(t, lvl, 2)

	if lvl.Emitters[0].Emitting {
		t.Error("stop_emit did not clear Emitting")
	}
	if !lvl.Emitters[0].Enabled {
		t.Error("stop_emit switched the whole emitter off; that is what .prop.enabled is for")
	}
	// The particles already in flight have a five-second life, so they survive.
	world.EmitterUpdate(lvl, 1.0/60.0, true)
	if lvl.Emitters[0].LiveCount() == 0 {
		t.Error("stop_emit killed the particles already in flight")
	}
}

// TestBurstReleasesParticles.
func TestBurstReleasesParticles(t *testing.T) {
	root := withTempScriptDir(t)
	fx := writeEffect(t, "spray")
	path := writeScript(t, root, "e.flang", `def on_spawn(:spawn:) -> null
    burst(@self, 25)
end`)

	lvl := emitterLevel(t, path, fx)
	lvl.Emitters[0].Emitting = false // isolate the burst from the rate
	lvl.Emitters[0].SyncSystems()

	runTicks(t, lvl, 2)

	if got := lvl.Emitters[0].LiveCount(); got != 25 {
		t.Errorf("burst produced %d particles, want 25", got)
	}
}

// TestBurstRejectsAnUnknownLayer — a typo'd layer name is a script error a
// author can fix, not a silent no-op.
func TestBurstRejectsAnUnknownLayer(t *testing.T) {
	root := withTempScriptDir(t)
	fx := writeEffect(t, "spray")
	path := writeScript(t, root, "e.flang", `def on_spawn(:spawn:) -> null
    burst(@self, 5, "nope")
end`)

	lvl := emitterLevel(t, path, fx)
	runTicks(t, lvl, 2)

	if lvl.Emitters[0].LiveCount() != 0 {
		t.Error("a burst naming a layer that does not exist released particles")
	}
}

// TestSpawnEffectPlacesAndReapsAnEmitter — the verb gameplay actually uses.
func TestSpawnEffectPlacesAndReaps(t *testing.T) {
	root := withTempScriptDir(t)
	// A short, non-looping, burst-only effect, so it finishes on its own.
	dir := filepath.Join(fileutil.ResourcesPath, particle.ParticlesDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `id: boom
layers:
  - name: main
    rate: 0
    duration: 0.1
    loop: false
    life_min: 0.2
    life_max: 0.2
    max: 50
    bursts:
      - {t: 0, n: 10}
`
	if err := os.WriteFile(filepath.Join(dir, "boom"+particle.ParticleExt), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	path := writeScript(t, root, "e.flang", `def on_spawn(:spawn:) -> null
    spawn_effect("boom", <1.0, 2.0, 3.0>)
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "trigger", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
	})

	runTicks(t, lvl, 2)

	if len(lvl.Emitters) != 1 {
		t.Fatalf("spawn_effect placed %d emitters, want 1", len(lvl.Emitters))
	}
	e := &lvl.Emitters[0]
	if e.Effect != "boom" {
		t.Errorf("emitter names effect %q, want boom", e.Effect)
	}
	if e.Position != (mgl32.Vec3{1, 2, 3}) {
		t.Errorf("emitter landed at %v, want {1 2 3}", e.Position)
	}
	if !e.OneShot {
		t.Error("spawn_effect's emitter is not marked one-shot; it would never be cleaned up")
	}

	// It must not be reaped on the frame it was created, before it has emitted.
	world.EmitterUpdate(lvl, 1.0/60.0, true)
	if len(lvl.Emitters) != 1 {
		t.Fatal("the one-shot was reaped before it had emitted anything")
	}
	if lvl.Emitters[0].LiveCount() == 0 {
		t.Fatal("the one-shot's burst did not fire")
	}

	// And it must be gone once its last particle has died.
	for i := 0; i < 60 && len(lvl.Emitters) > 0; i++ {
		world.EmitterUpdate(lvl, 1.0/60.0, true)
	}
	if len(lvl.Emitters) != 0 {
		t.Errorf("the one-shot was not reaped: %d emitters, %d live",
			len(lvl.Emitters), lvl.Emitters[0].LiveCount())
	}
}

// TestSpriteProperties — the other half of the binding work.
func TestSpriteProperties(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "s.flang", `def on_spawn(:spawn:) -> null
    @self.prop.visible = false
    @self.prop.alpha = 0.25
end`)

	lvl := tickTestLevel(t)
	sp := world.NewSprite("bush", mgl32.Vec3{})
	sp.Script = path
	lvl.Sprites = append(lvl.Sprites, sp)

	runTicks(t, lvl, 2)

	if lvl.Sprites[0].Visible {
		t.Error("the script's write to .prop.visible did not reach the sprite")
	}
	if got := lvl.Sprites[0].Alpha; got < 0.24 || got > 0.26 {
		t.Errorf("alpha is %v, want 0.25", got)
	}
}

// TestTagsReachSpritesAndEmitters — `#tag` has to see them, or the whole
// addressing story is inconsistent between kinds.
func TestTagsReachSpritesAndEmitters(t *testing.T) {
	testenv := tickTestLevel(t)
	sp := world.NewSprite("bush", mgl32.Vec3{})
	sp.Tags = []string{"foliage"}
	em := world.NewEmitter("smoke", mgl32.Vec3{})
	em.Tags = []string{"foliage"}
	testenv.Sprites = append(testenv.Sprites, sp)
	testenv.Emitters = append(testenv.Emitters, em)

	c := buildRefCache(testenv)
	refs := c.refsWithTag("foliage")
	if len(refs) != 2 {
		t.Fatalf("#foliage matched %d objects, want 2: %v", len(refs), refs)
	}

	// And each resolves back by name.
	for _, id := range []string{"bush", "smoke"} {
		r, ok := c.refFor(id)
		if !ok {
			t.Errorf("@%s did not resolve", id)
			continue
		}
		if !c.alive(r) {
			t.Errorf("@%s resolved but reads as dead", id)
		}
		if c.scriptDataFor(r) == nil {
			t.Errorf("@%s has no script data; tags and attributes would be unreachable", id)
		}
	}
}
