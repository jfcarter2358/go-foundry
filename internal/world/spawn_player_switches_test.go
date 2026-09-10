package world

// spawn_player_switches_test.go — the spawn's control and gravity switches, as
// ApplySpawnPoint hands them to the engine.

import (
	"foundry/internal/platform"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// keepPlayerSwitches puts the engine globals back after a test, so one that
// leaves gravity off does not change how every later test in the package runs.
func keepPlayerSwitches(t *testing.T) {
	control, gravity, player := platform.PlayerControlEnabled, platform.GravityEnabled, PlayerEntity
	t.Cleanup(func() {
		platform.PlayerControlEnabled, platform.GravityEnabled, PlayerEntity = control, gravity, player
	})
}

func TestASpawnCanStartThePlayerWithoutControlOrGravity(t *testing.T) {
	keepPlayerSwitches(t)
	PlayerEntity = &Entity{Velocity: mgl32.Vec3{0, -0.4, 0}}

	lvl := &Level{Spawn: &SpawnPoint{DisableControl: true, DisableGravity: true}}
	if !ApplySpawnPoint(lvl) {
		t.Fatal("ApplySpawnPoint found no spawn")
	}
	if platform.PlayerControlEnabled {
		t.Error("player control is on, want off")
	}
	if platform.GravityEnabled {
		t.Error("gravity is on, want off")
	}
	if PlayerEntity.Velocity[1] != 0 {
		t.Errorf("the player is still falling at %v", PlayerEntity.Velocity[1])
	}
}

// The switches are how a level starts, so a spawn that sets neither — and a
// level with no spawn at all — must not inherit the last level's frozen player.
func TestApplyingASpawnResetsTheSwitchesTheLastOneTurnedOff(t *testing.T) {
	keepPlayerSwitches(t)
	PlayerEntity = nil

	for name, lvl := range map[string]*Level{
		"an ordinary spawn": {Spawn: &SpawnPoint{}},
		"no spawn":          {},
	} {
		platform.PlayerControlEnabled, platform.GravityEnabled = false, false
		ApplySpawnPoint(lvl)
		if !platform.PlayerControlEnabled || !platform.GravityEnabled {
			t.Errorf("%s: control %v, gravity %v, want both on",
				name, platform.PlayerControlEnabled, platform.GravityEnabled)
		}
	}
}
