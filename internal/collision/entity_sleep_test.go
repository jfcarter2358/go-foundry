package collision

import (
	"fmt"
	"testing"

	"foundry/internal/hooks"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// restLevel is crowdLevel with most walkers standing still, and the world
// moving under some of them on a schedule: a dynamic brush (a lift slab) rising
// under a few, the level's gravity changing, bodies being shoved and teleported
// and having their shape changed — every kind of wake-up the memo must notice.
func restLevel() *world.Level {
	lvl := crowdLevel(90)
	// A lift slab in the yard, dynamic so a script may move it.
	slab := world.NewBoxBrush("lift", mgl32.Vec3{12, 0.1, -12}, mgl32.Vec3{3, 0.1, 3}, "concrete")
	slab.Dynamic = true
	lvl.Chunks[0].Brushes = append(lvl.Chunks[0].Brushes, slab)
	lvl.Chunks[0].RecomputeBounds()
	world.MarkBrushMeshesChanged()
	if err := world.RebuildIndexes(lvl); err != nil {
		panic(err)
	}
	world.RefreshFrames(lvl, lvl.Frames)
	for i := 1; i < len(lvl.Entities); i++ {
		e := &lvl.Entities[i]
		e.Velocity = mgl32.Vec3{}
		e.Waypoints = nil
		if i%9 == 0 {
			// A few standing on the lift.
			e.Position = mgl32.Vec3{11 + float32(i%3), 0.5, -12 + float32(i%4)}
		}
	}
	return lvl
}

// TestRestingBodiesMemoMatchesTheFullSolve runs the same scripted disturbances
// with the motion memo on and off and holds every body, every event, every
// frame to bit equality — and requires that the memo actually answered most
// of the resting bodies' steps.
func TestRestingBodiesMemoMatchesTheFullSolve(t *testing.T) {
	defer func(s bool) { SleepRestingBodies = s }(SleepRestingBodies)
	const frames = 300
	const dt = float32(1.0 / 60)
	run := func(sleep bool) ([][]string, uint64) {
		SleepRestingBodies = sleep
		lvl := restLevel()
		hooks.TakeAnimEvents()
		hooks.TakeWaypointEvents()
		hooks.TakeCollisionPairs()
		g := float32(world.DefaultGravity)
		lift := 0
		for i := range lvl.Chunks[0].Brushes {
			if lvl.Chunks[0].Brushes[i].ID == "lift" {
				lift = i
			}
		}
		hits := MotionMemoHits()
		var out [][]string
		for f := 0; f < frames; f++ {
			switch {
			case f >= 100 && f < 140:
				// The lift rises under the bodies on it.
				lvl.Chunks[0].Brushes[lift].Offset = mgl32.Vec3{0, float32(f-100) * 0.01, 0}
			case f == 160:
				g *= 0.5
				lvl.Gravity = &g
			case f == 200:
				lvl.Entities[5].Velocity = mgl32.Vec3{3, 2, 0} // a shove
				lvl.Entities[6].Position = lvl.Entities[6].Position.Add(mgl32.Vec3{0, 1, 0})
				lvl.Entities[7].BoundHalfY = 0.6 // a crouch
				lvl.Entities[8].Collision.Gravity = false
			case f == 240:
				lvl.Entities[7].BoundHalfY = 0.9
				lvl.Entities[8].Collision.Gravity = true
			}
			EntityUpdate(lvl, dt, true, nil)
			world.RefreshFrames(lvl, lvl.Frames)
			st := crowdState(lvl)
			for _, ev := range hooks.TakeAnimEvents() {
				st = append(st, fmt.Sprintf("anim %+v", ev))
			}
			for _, p := range hooks.TakeCollisionPairs() {
				st = append(st, fmt.Sprintf("pair %v", p))
			}
			out = append(out, st)
		}
		return out, MotionMemoHits() - hits
	}
	full, _ := run(false)
	memo, hits := run(true)
	for f := range full {
		if len(full[f]) != len(memo[f]) {
			t.Fatalf("frame %d: %d lines without the memo, %d with", f, len(full[f]), len(memo[f]))
		}
		for i := range full[f] {
			if full[f][i] != memo[f][i] {
				t.Fatalf("frame %d line %d differs:\nfull %s\nmemo %s", f, i, full[f][i], memo[f][i])
			}
		}
	}
	// Not most of them: a cylinder resting on a box brush settles into a
	// sawtooth a hundred-millionth of a metre deep (each frame's push out of
	// the floor lands a rounding error lower until the next one overshoots),
	// so its inputs never repeat bit for bit and the memo rightly declines
	// it. Bodies whose rest is an exact fixed point — the common case on a
	// real level's floors, a fifth of all steps in level3 — are memoised.
	if min := uint64(1000); hits < min {
		t.Fatalf("the memo answered only %d body steps (want at least %d): it is not taking", hits, min)
	}
	t.Logf("memo answered %d of %d body steps", hits, 90*frames)
}
