package devui

import (
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/world"
)

// levelWithPlayer builds the smallest level a world-changing command can be
// tried against: a player, and one entity with a tag on it to aim at.
func levelWithPlayer(t *testing.T) *world.Level {
	t.Helper()
	prev := world.PlayerEntity
	world.PlayerEntity = world.NewPlayer(mgl32.Vec3{})
	t.Cleanup(func() { world.PlayerEntity = prev })

	lvl := &world.Level{}
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID:       "beacon",
		Tags:     []string{"marker"},
		Position: mgl32.Vec3{12, 3, -4},
	})
	return lvl
}

func TestTeleportToCoordinatesMovesThePlayer(t *testing.T) {
	reset(t)
	lvl := levelWithPlayer(t)
	run(lvl, "tp 1 2 3")
	if got := world.PlayerEntity.Position; got != (mgl32.Vec3{1, 2, 3}) {
		t.Fatalf("player is at %v, want {1 2 3}\n%s", got, output())
	}
}

func TestTeleportToAnIDMovesThePlayerThere(t *testing.T) {
	reset(t)
	lvl := levelWithPlayer(t)
	run(lvl, "tp beacon")
	if got := world.PlayerEntity.Position; got != (mgl32.Vec3{12, 3, -4}) {
		t.Fatalf("player is at %v, want the beacon's place\n%s", got, output())
	}
}

func TestTeleportToATagMovesThePlayerThere(t *testing.T) {
	reset(t)
	lvl := levelWithPlayer(t)
	run(lvl, "tp #marker")
	if got := world.PlayerEntity.Position; got != (mgl32.Vec3{12, 3, -4}) {
		t.Fatalf("player is at %v, want the tagged entity's place\n%s", got, output())
	}
}

func TestTeleportToNothingSaysSoAndDoesNotMove(t *testing.T) {
	reset(t)
	lvl := levelWithPlayer(t)
	run(lvl, "tp nowhere")
	if got := world.PlayerEntity.Position; got != (mgl32.Vec3{}) {
		t.Fatalf("the player moved to %v on a failed teleport", got)
	}
	if lastKind() != kindErr {
		t.Fatalf("a failed teleport was not reported as one:\n%s", output())
	}
}

func TestFindListsWhatCarriesATag(t *testing.T) {
	reset(t)
	lvl := levelWithPlayer(t)
	run(lvl, "find marker")
	out := output()
	if !strings.Contains(out, "beacon") {
		t.Fatalf("find did not list the tagged entity:\n%s", out)
	}
	if !strings.Contains(out, "12.00") {
		t.Fatalf("find did not print its position:\n%s", out)
	}
}

func TestFindOnAnUnknownTagSuggestsOne(t *testing.T) {
	reset(t)
	lvl := levelWithPlayer(t)
	run(lvl, "find marekr")
	if !strings.Contains(output(), "marker") {
		t.Fatalf("no suggestion for a near-miss tag:\n%s", output())
	}
}

func TestEntitiesListsTheLevelNearestFirst(t *testing.T) {
	reset(t)
	lvl := levelWithPlayer(t)
	lvl.Entities = append(lvl.Entities, world.Entity{ID: "far", Position: mgl32.Vec3{500, 0, 0}})
	run(lvl, "entities")
	out := output()
	if strings.Index(out, "far") < strings.Index(out, "beacon") {
		t.Fatalf("entities were not nearest first:\n%s", out)
	}
	run(lvl, "entities far")
	if strings.Contains(strings.SplitN(output(), "entities\n", 2)[1], "beacon") {
		t.Fatalf("the substring filter did not narrow the list:\n%s", output())
	}
}

func TestDestroyRemovesTheEntity(t *testing.T) {
	reset(t)
	lvl := levelWithPlayer(t)
	run(lvl, "destroy beacon")
	for i := range lvl.Entities {
		if lvl.Entities[i].ID == "beacon" {
			t.Fatalf("the entity is still there:\n%s", output())
		}
	}
}

func TestGodStaysOnUntilItIsTurnedOff(t *testing.T) {
	reset(t)
	lvl := levelWithPlayer(t)
	world.PlayerEntity.Attributes = map[string]world.AttrValue{
		"health_max": {I: 100, F: 100},
		"health":     {I: 100, F: 100},
	}
	run(lvl, "god on")

	world.PlayerEntity.Attributes["health"] = world.AttrValue{I: 1, F: 1}
	applyStickies(lvl)
	if got := world.PlayerEntity.Attributes["health"].I; got <= 100 {
		t.Fatalf("health is %d — god did not top it back up", got)
	}

	run(lvl, "god off")
	world.PlayerEntity.Attributes["health"] = world.AttrValue{I: 1, F: 1}
	applyStickies(lvl)
	if got := world.PlayerEntity.Attributes["health"].I; got != 1 {
		t.Fatalf("health is %d — god is still on", got)
	}
}
