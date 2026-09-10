package script

// script_entity_emitter_test.go — addressing the particle effects an entity
// carries, from a script.
//
// The rule is the one the brushes beside them follow, and these assert it is
// literally the same rule: a script writes `@ship.thruster` — the placement and
// the definition-local name — and the level-unique "ship.thruster" the
// materialiser gave it is the engine's business.

import (
	"testing"

	"foundry/internal/flang"
	"foundry/internal/world"
)

// shipWithEffects is shipLevel plus the emitters an entity's type would have
// materialised: attached to the entity, IDs qualified by it.
func shipWithEffects(t *testing.T) *world.Level {
	t.Helper()
	lvl := shipLevel(t)

	e := world.FindEntity(lvl, "ship")
	e.Emitters = []world.EntityEmitter{
		func() world.EntityEmitter {
			m := world.NewEntityEmitter("thruster")
			m.Effect = "plume"
			m.Tags = []string{"engine"}
			return m
		}(),
	}
	// Materialised the way world.MaterializeEntityEmitters does. The .fpar does
	// not exist under a test's working directory, which is fine and is the case
	// a level with a deleted effect is in: the emitter is real and addressable
	// and simply produces nothing.
	_ = world.MaterializeEntityEmitters(lvl, e, nil)
	return lvl
}

func TestAnEntityEmitterIsAddressedByItsDefinitionName(t *testing.T) {
	lvl := shipWithEffects(t)

	got := runScript(t, lvl, "", "def main() -> null\n    return @ship.thruster.id\nend")
	if !flang.Equal(got, flang.StrVal("ship.thruster")) {
		t.Errorf("@ship.thruster.id = %v, want the qualified level ID %q", got, "ship.thruster")
	}
	got = runScript(t, lvl, "", "def main() -> null\n    return @ship.thruster.kind\nend")
	if !flang.Equal(got, flang.AtomVal("emitter")) {
		t.Errorf("@ship.thruster.kind = %v, want emitter", got)
	}
}

func TestEntityEmittersListsThemAll(t *testing.T) {
	lvl := shipWithEffects(t)

	got := runScript(t, lvl, "", "def main() -> null\n    return len(@ship.emitters)\nend")
	if !flang.Equal(got, flang.IntVal(1)) {
		t.Errorf("len(@ship.emitters) = %v, want 1", got)
	}
	got = runScript(t, lvl, "", "def main() -> null\n    return @ship.emitters[0].id\nend")
	if !flang.Equal(got, flang.StrVal("ship.thruster")) {
		t.Errorf("@ship.emitters[0].id = %v, want %q", got, "ship.thruster")
	}
}

func TestAnEntityEmitterTakesTheParticleVerbs(t *testing.T) {
	lvl := shipWithEffects(t)

	runScript(t, lvl, "", script("    stop_emit(@ship.thruster)"))
	em := world.FindEmitter(lvl, "ship.thruster")
	if em == nil {
		t.Fatal("the materialised emitter went missing")
	}
	if em.Emitting {
		t.Error("stop_emit did not reach an emitter addressed through its entity")
	}
}

// The linter has to follow the same step, or `@ship.thruster.prop.emitting` is
// checked against the entity property table and flagged as a typo.
func TestLintFollowsAnEntityEmitterStep(t *testing.T) {
	lvl := shipWithEffects(t)

	if got := lintSource(t, lvl, script("    x = @ship.thruster.prop.emitting")); len(got) != 0 {
		t.Errorf("reading an emitter property through an entity was flagged: %v", got)
	}
	got := lintSource(t, lvl, script("    x = @ship.thruster.prop.emittingg"))
	wantOneIssue(t, got, "emitter", "emittingg")
}

// Destroying the entity takes its effects with it, or the level goes on
// emitting from where the thing used to be — an Attach naming a gone entity
// resolves to the world origin rather than to nowhere.
func TestDestroyingAnEntityTakesItsEmitters(t *testing.T) {
	lvl := shipWithEffects(t)

	runScript(t, lvl, "", script("    destroy(@ship)"))
	if world.FindEmitter(lvl, "ship.thruster") != nil {
		t.Error("the emitter outlived the entity that carried it")
	}
}
