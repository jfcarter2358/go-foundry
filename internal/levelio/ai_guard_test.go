package levelio

// ai_guard_test.go — the shipped sample agent, loaded the way a level loads it,
// doing what its type file says it does with no script involved: walking the
// volumes tagged #patrol in order, and noticing a player who walks into view.

import (
	"math"
	"path/filepath"
	"testing"

	"foundry/internal/ai"
	"foundry/internal/collision"
	"foundry/internal/fileutil"
	"foundry/internal/hooks"
	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func TestTheShippedGuardPatrolsAndNotices(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join(testenv.RepoRoot(t), "resources")
	world.ReloadEntityDefs()
	t.Cleanup(func() {
		fileutil.ResourcesPath = prev
		world.ReloadEntityDefs()
		ai.ResetSession()
		hooks.TakeAIEvents()
	})

	room := world.RawChunk{ID: "room"}
	for _, b := range world.NewHollowRoom("room", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{24, 4, 24}, 0.5, "") {
		room.Brushes = append(room.Brushes, world.BrushToRaw(&b))
	}
	raw := world.RawLevel{
		Chunks:   []world.RawChunk{room},
		Entities: []world.RawEntity{{Type: "guard", ID: "guard", Position: world.RawVec3{X: 12, Y: 0, Z: 12}}},
	}
	for _, p := range []struct {
		id   string
		x, z float32
	}{{"post_a", 4, 4}, {"post_b", 20, 4}} {
		v := world.NewVolume(p.id, mgl32.Vec3{p.x, 1, p.z})
		v.Tags = []string{"patrol"}
		o := world.DefaultVolumeObjective()
		o.Duration = 0.5
		v.Objective = &o
		raw.Volumes = append(raw.Volumes, world.VolumeToRaw(&v))
	}

	lvl, err := rawToLevel(raw)
	if err != nil {
		t.Fatalf("rawToLevel: %v", err)
	}
	g := world.FindEntity(&lvl, "guard")
	if g == nil || g.AI == nil {
		t.Fatalf("the guard type loaded with no controller: %+v", g)
	}
	ai.ResetSession()
	hooks.TakeAIEvents()

	var done []string
	var events []hooks.AIEvent
	step := func(seconds float32, player *world.Entity) {
		const dt = float32(1.0 / 60.0)
		for s := float32(0); s < seconds; s += dt {
			ai.Update(&lvl, dt, player)
			collision.EntityUpdate(&lvl, dt, true, player)
			world.RefreshFrames(&lvl, lvl.Frames)
			for _, ev := range hooks.TakeAIEvents() {
				events = append(events, ev)
				if ev.Atom == "objective_complete" {
					done = append(done, ev.Objective)
				}
			}
		}
	}
	step(25, nil)
	if len(done) < 3 || done[0] != "post_a" || done[1] != "post_b" || done[2] != "post_a" {
		t.Fatalf("the guard's patrol went %v, want post_a, post_b, post_a…", done)
	}

	// A player straight in front of it, well inside its sight range — and
	// inside the room, which a guard walking at a wall has only just in front.
	g = world.FindEntity(&lvl, "guard")
	feet := ai.Feet(&lvl, g)
	yaw := float64(g.Rotation.Y())
	ahead := mgl32.Vec3{float32(-math.Sin(yaw)), 0, float32(-math.Cos(yaw))}
	at := feet.Add(ahead.Mul(3))
	at[0] = float32(math.Max(1.5, math.Min(22.5, float64(at[0]))))
	at[2] = float32(math.Max(1.5, math.Min(22.5, float64(at[2]))))
	p := world.NewPlayer(at.Add(mgl32.Vec3{0, 0.9, 0}))
	events = nil
	step(1, p)
	seen := false
	for _, ev := range events {
		if ev.Atom == "target_acquired" && ev.Target == "player" {
			seen = true
		}
	}
	if !seen {
		t.Errorf("a player three metres in front of the guard was not noticed: %v", events)
	}
}
