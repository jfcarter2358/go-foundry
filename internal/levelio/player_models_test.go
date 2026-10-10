package levelio

// player_models_test.go — a level's player block reaches the player whichever
// exists first.

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// The app loads its startup level before it creates the player, so the load's
// own install finds nobody to give the viewmodel to. A release build booting
// straight into level1 had a player with no `hands`, and every script line
// addressing @player.model.hands failed. The startup calls InstallPlayerModels
// again once the player exists; this is that call.
func TestThePlayerGetsItsViewmodelWhenCreatedAfterTheLoad(t *testing.T) {
	prev := world.PlayerEntity
	t.Cleanup(func() { world.PlayerEntity = prev })

	world.PlayerEntity = nil
	lvl := &world.Level{}
	lvl.Player.Models = []world.EntityModel{{Name: "hands"}}
	if err := InstallPlayerModels(lvl); err != nil {
		t.Fatalf("with no player yet: %v", err)
	}

	world.PlayerEntity = world.NewPlayer(mgl32.Vec3{})
	if err := InstallPlayerModels(lvl); err != nil {
		t.Fatal(err)
	}
	if len(world.PlayerEntity.Models) != 1 || world.PlayerEntity.Models[0].Name != "hands" {
		t.Fatalf("player models %+v, want the level's hands", world.PlayerEntity.Models)
	}

	// A later level with no viewmodel takes it away again.
	if err := InstallPlayerModels(&world.Level{}); err != nil {
		t.Fatal(err)
	}
	if len(world.PlayerEntity.Models) != 0 {
		t.Errorf("a level with no player models left %d on the player", len(world.PlayerEntity.Models))
	}
}
