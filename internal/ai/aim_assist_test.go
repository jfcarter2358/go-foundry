package ai

import (
	"testing"

	"foundry/internal/world"
)

// Aim assist helps onto what hunts the player and nothing else: not a friend,
// not a corpse (its controller switched off), not something hidden.
func TestHostileToPlayer(t *testing.T) {
	player := &world.Entity{ID: "player"}
	player.Tags = []string{"squad"}
	agent := func(targets ...string) *world.Entity {
		return &world.Entity{ID: "e", Visible: true, AI: &world.AISpec{Enabled: true, Targets: targets}}
	}
	for _, c := range []struct {
		name string
		e    *world.Entity
		want bool
	}{
		{"hunts the player", agent("player"), true},
		{"hunts a tag the player carries", agent("#squad"), true},
		{"hunts the player by ID", agent("@player"), true},
		{"hunts someone else", agent("#enemy"), false},
		{"hunts nothing", agent(), false},
		{"no controller", &world.Entity{ID: "crate", Visible: true}, false},
	} {
		if got := HostileToPlayer(c.e, player); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}

	dead := agent("player")
	dead.AI.Enabled = false
	if HostileToPlayer(dead, player) {
		t.Error("an agent with its controller off still drew aim assist")
	}
	hidden := agent("player")
	hidden.Visible = false
	if HostileToPlayer(hidden, player) {
		t.Error("a hidden agent drew aim assist")
	}
	locked := agent()
	locked.AIState = &world.AIState{Target: "player"}
	if !HostileToPlayer(locked, player) {
		t.Error("an agent a script pointed at the player is not hostile")
	}
}
