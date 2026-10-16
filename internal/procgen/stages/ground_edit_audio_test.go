package stages

import (
	"math"
	"testing"

	"foundry/internal/procgen/terrain"
)

// A river 120 m along X with a riffle stretch and a fall, beside a pond:
// the plan roars at the fall, uses rapids at the riffles, laps the pond's
// shore, and never stacks two sounds.
func TestWaterSoundsFollowTheWaterWithoutStacking(t *testing.T) {
	g := terrain.NewGrid(64, 2) // 128 m, centred
	var st []terrain.RiverStation
	for s := 0.0; s <= 120; s += 2 {
		riffle := 0.0
		if s > 70 && s < 95 {
			riffle = 1
		}
		st = append(st, terrain.RiverStation{X: -60 + s, Z: -40, S: s, TX: 1, Surface: 5 - s*0.02, Width: 4, Speed: 0.8 + riffle, Riffle: riffle})
	}
	rv := &groundRivers{res: &terrain.RiverResult{
		Rivers: []terrain.River{{Name: "brook", Stations: st}},
		Falls:  []terrain.Waterfall{{River: "brook", X: 0, Z: -40, Lip: 4, Foot: 1, DirX: 1, Width: 4}},
	}}
	// A round pond of radius 14 m at (20, 30).
	S := g.S()
	w := &terrain.WaterResult{Level: make([]float32, S*S), Body: make([]int32, S*S),
		Bodies: []terrain.WaterBody{{Pond: true, Level: 2}}}
	for j := 0; j < S; j++ {
		for i := 0; i < S; i++ {
			k := j*S + i
			w.Body[k] = -1
			x, z := g.MinX+float64(i)*g.Cell, g.MinZ+float64(j)*g.Cell
			if math.Hypot(x-20, z-30) < 14 {
				w.Body[k] = 0
				w.Level[k] = 2
			}
		}
	}
	plan := planWaterSounds(g, rv, &groundWater{water: w}, 24)
	n := map[string]int{}
	for i, s := range plan {
		n[s.Clip]++
		if s.MaxDistance <= s.RefDistance || s.Gain <= 0 || s.Pitch < 0.8 || s.Pitch > 1.2 {
			t.Errorf("%s: gain %v pitch %v radii %v..%v", s.ID, s.Gain, s.Pitch, s.RefDistance, s.MaxDistance)
		}
		for _, o := range plan[i+1:] {
			if d := math.Hypot(float64(s.Position.X-o.Position.X), float64(s.Position.Z-o.Position.Z)); d < 24*0.7-0.01 {
				t.Errorf("%s and %s stacked %.1f m apart", s.ID, o.ID, d)
			}
		}
	}
	if n[waterClipFalls] != 1 {
		t.Errorf("%d roars, want the fall's one", n[waterClipFalls])
	}
	if n[waterClipRapids] == 0 || n[waterClipRiver] == 0 {
		t.Errorf("flow %d rapids %d: want both along a river with a riffle stretch", n[waterClipRiver], n[waterClipRapids])
	}
	if n[waterClipLap] == 0 || n[waterClipLap] > waterSoundsMaxPerBody {
		t.Errorf("%d lapping sounds on the pond", n[waterClipLap])
	}
	for _, s := range plan {
		if s.Clip == waterClipLap && math.Abs(math.Hypot(float64(s.Position.X)-20, float64(s.Position.Z)-30)-14) > 3 {
			t.Errorf("%s at %v is not on the shore", s.ID, s.Position)
		}
	}
	// Off by default: nothing for a document that does not ask.
	if len(planWaterSounds(g, nil, nil, 24)) != 0 {
		t.Error("sounds with no water")
	}
}
