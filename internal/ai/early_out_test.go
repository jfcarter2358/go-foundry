package ai

import (
	"fmt"
	"math/rand"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// TestPickCoverMatchesTheWalk holds the score-ordered cover pick to the
// sampled-order walk it replaced — the same spot, ties included — over many
// random sample sets around a low wall, ducked and standing, peeking and not.
// Samples are snapped to a coarse grid so equal scores actually occur.
func TestPickCoverMatchesTheWalk(t *testing.T) {
	lvl := lowWallLevel(t)
	lvl.Entities = []world.Entity{agent("soldier", mgl32.Vec3{10, 0, 6})}
	e := &lvl.Entities[0]
	p := player(t, mgl32.Vec3{10, 0, 17})
	threat := eyeOf(lvl, p)
	feet := feetOf(lvl, e)
	standEye := ProfileFor(lvl, e).Height * 0.9
	r := rand.New(rand.NewSource(5))
	found := 0
	for trial := 0; trial < 400; trial++ {
		centre := mgl32.Vec3{8 + r.Float32()*4, 0, 7 + r.Float32()*4}
		var samples []mgl32.Vec3
		for i := 0; i < 48; i++ {
			x := float32(int(r.Float32()*16)) * 0.5
			z := float32(int(r.Float32()*16)) * 0.5
			samples = append(samples, mgl32.Vec3{6 + x, 0, 6 + z})
		}
		opts := CoverOpts{Duck: []float32{0, 0.6}[trial%2], Peek: trial%3 == 0}
		duck := opts.Duck
		if duck <= 0 {
			duck = 1
		}
		wp, wok := findCoverWalk(lvl, e, samples, feet, centre, threat, p.ID, duck, standEye, opts)
		gp, gok := pickCover(lvl, e, samples, feet, centre, threat, p.ID, duck, standEye, opts)
		if wp != gp || wok != gok {
			t.Fatalf("trial %d: pick %v,%v, walk %v,%v", trial, gp, gok, wp, wok)
		}
		if wok {
			found++
		}
	}
	if found < 50 {
		t.Fatalf("only %d trials found cover; the comparison is not exercising the pick", found)
	}
}

// TestNearestPerceivedMatchesTheWalk does the same for target choice: a ring
// of candidate targets at shared distances, some behind the wall.
func TestNearestPerceivedMatchesTheWalk(t *testing.T) {
	lvl := lowWallLevel(t)
	a := agent("soldier", mgl32.Vec3{10, 0, 6})
	a.AI.Targets = []string{"#foe"}
	a.AI.HearingRange = 3
	lvl.Entities = []world.Entity{a}
	r := rand.New(rand.NewSource(9))
	for i := 0; i < 24; i++ {
		f := agent(fmt.Sprintf("foe%d", i), mgl32.Vec3{10 + float32(int(r.Float32()*8)-4), 0, 6 + float32(int(r.Float32()*12)-2)})
		f.Tags = []string{"foe"}
		lvl.Entities = append(lvl.Entities, f)
	}
	e := &lvl.Entities[0]
	picked := 0
	for trial := 0; trial < 200; trial++ {
		e.Rotation[1] = r.Float32() * 6.28
		var except *world.Entity
		if trial%4 == 0 {
			except = &lvl.Entities[1+r.Intn(24)]
		}
		cands := Candidates(lvl, e, nil)
		want := nearestPerceivedWalk(lvl, e, cands, eyeOf(lvl, e), except)
		got := nearestPerceived(lvl, e, nil, except)
		if got != want {
			t.Fatalf("trial %d: nearestPerceived %v, walk %v", trial, idOf(got), idOf(want))
		}
		if got != nil {
			picked++
		}
	}
	if picked < 50 {
		t.Fatalf("only %d trials perceived anyone; the comparison is not exercising the choice", picked)
	}
}

func idOf(e *world.Entity) string {
	if e == nil {
		return "<nil>"
	}
	return e.ID
}
