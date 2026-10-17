package place

import (
	"math"
	"testing"
)

// A row of thin round pillars (7 m across), each narrower than a chunk at its scale,
// stands on a plain beside a long mesa wall. With the phase-7 tests on, the
// pillars' flanks get no chunks (a chunk there hangs off the sides) while the
// mesa wall is still lined, and every chunk kept touches its face over at
// least MinContact of its back; with them off the pillars are stacked too —
// the "rocks stuck onto a pillar" the renders showed.
func TestWallsLeavePillarsAndHangingSeats(t *testing.T) {
	pillar := func(x, z float64) float64 {
		h := 0.0
		for k := -2; k <= 2; k++ {
			d := math.Hypot(x+30, z-float64(k)*24)
			h = math.Max(h, 30*smooth(3.5, 2, d))
		}
		return h
	}
	g := groundOf(t, 128, 0.5, func(x, z float64) float64 {
		return math.Max(pillar(x, z), 25*smooth(17, 23, x))
	})
	lines := TraceCliffFeet(g, TraceSettings{Threshold: 0.5, Cell: 2, MinLength: 10, Smooth: 2})
	onPillar := func(in Instance) bool { return in.Pos[0] < 0 }

	s := DefaultWall()
	s.Chunks = chunks(3)
	s.Seed = 3
	s.MinWall = 4
	off, err := Walls(g, lines, s, pool(1))
	if err != nil {
		t.Fatal(err)
	}
	ph7 := DefaultWallPhase7
	s.MinContact, s.ContactGap, s.Backing, s.TierJitter = ph7.MinContact, ph7.ContactGap, ph7.Backing, ph7.TierJitter
	on, err := Walls(g, lines, s, pool(1))
	if err != nil {
		t.Fatal(err)
	}
	count := func(r *WallResult) (pillars, mesa int) {
		for _, in := range r.Instances {
			if in.Kind != "chunk" {
				continue
			}
			if onPillar(in) {
				pillars++
			} else {
				mesa++
			}
		}
		return
	}
	for _, in := range on.Instances {
		if in.Kind == "chunk" && onPillar(in) {
			t.Logf("pillar chunk at %.1f %.1f %.1f scale %.2f", in.Pos[0], in.Pos[1], in.Pos[2], in.Scale)
		}
	}
	pOff, _ := count(off)
	pOn, mOn := count(on)
	if pOff == 0 {
		t.Fatal("the pillars got no chunks even with the tests off: the fixture does not test anything")
	}
	if pOn != 0 {
		t.Errorf("%d chunks on the pillars with the tests on (%d off); pillars %d, contact drops %d", pOn, pOff, on.Pillars, on.ContactDropped)
	}
	if mOn < 10 {
		t.Errorf("only %d chunks on the mesa wall with the tests on", mOn)
	}
	if on.Pillars == 0 && on.ContactDropped == 0 {
		t.Error("nothing was counted as a pillar or a hanging seat")
	}
}

// The tier pitch follows the chunks placed and the jitter: the bottoms up a
// column are not one fixed step apart.
func TestTiersAreNotEvenlyPitched(t *testing.T) {
	g := stepGround(t, 128, 0.5, 40)
	lines := TraceCliffFeet(g, TraceSettings{Threshold: 0.5, Cell: 2, MinLength: 20, Smooth: 2})
	s := DefaultWall()
	s.Chunks = chunks(3)
	s.Seed = 9
	s.TierJitter = 0.35
	s.DropJitter = 0
	res, err := Walls(g, lines, s, pool(1))
	if err != nil {
		t.Fatal(err)
	}
	steps := map[int64]bool{}
	var prev float64
	have := false
	for _, in := range res.Instances {
		if in.Kind != "chunk" {
			continue
		}
		if have && in.Pos[1] > prev {
			steps[int64(math.Round((in.Pos[1]-prev)*10))] = true
		}
		prev, have = in.Pos[1], true
	}
	if len(steps) < 6 {
		t.Errorf("tier steps take only %d distinct values: %v", len(steps), steps)
	}
}
