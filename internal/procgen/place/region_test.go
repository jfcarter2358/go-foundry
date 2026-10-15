package place

// region_test.go — placement for one environment region (region.go): its
// rocks thin with its weight, are keyed by the world and not by the ground
// they were placed over, and the patch grid and wall columns follow it.

import (
	"fmt"
	"math"
	"testing"
)

// westWeight is a region owning x < −20, sharing −20..20, none past 20.
func westWeight(x, z float64) float64 { return 1 - smooth(-20, 20, x) }

func gentle(x, z float64) float64 { return 2*math.Sin(x*0.03) + math.Cos(z*0.02) }

func instanceKeys(res *ScatterResult, keep func(x, z float64) bool) map[string]bool {
	out := map[string]bool{}
	for _, in := range res.Instances {
		if keep(in.Pos[0], in.Pos[2]) {
			out[fmt.Sprintf("%.6f,%.6f,%.6f,%d", in.Pos[0], in.Pos[2], in.Scale, in.Variant)] = true
		}
	}
	return out
}

func TestRegionScatterIsKeyedByTheWorld(t *testing.T) {
	r := DefaultRule()
	r.Variants, r.Density, r.Cluster = boulders(3), 3, 0.5
	reg := &Region{Weight: westWeight}
	small := groundOf(t, 256, 1, gentle) // −128..128
	big := groundOf(t, 384, 1, gentle)   // −192..192: the union grew
	a, err := Scatter(small, ScatterSettings{Rules: []Rule{r}, Seed: 7, Margin: 1, Region: reg}, pool(4))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Scatter(big, ScatterSettings{Rules: []Rule{r}, Seed: 7, Margin: 1, Region: reg}, pool(3))
	if err != nil {
		t.Fatal(err)
	}
	inner := func(x, z float64) bool { return math.Abs(x) < 90 && math.Abs(z) < 90 }
	ka, kb := instanceKeys(a, inner), instanceKeys(b, inner)
	if len(ka) < 100 {
		t.Fatalf("only %d rocks in the inner square", len(ka))
	}
	diff := 0
	for k := range ka {
		if !kb[k] {
			diff++
		}
	}
	for k := range kb {
		if !ka[k] {
			diff++
		}
	}
	if diff != 0 {
		t.Errorf("%d of %d rocks in the shared square moved when the ground grew", diff, len(ka))
	}
	// Nothing where the region has no share, and fewer across the band.
	var west, band, east int
	for _, in := range a.Instances {
		switch x := in.Pos[0]; {
		case x > 20:
			east++
		case x > -20:
			band++
		case x > -60:
			west++
		}
	}
	if east != 0 {
		t.Errorf("%d rocks where the region has no share", east)
	}
	if band == 0 || band >= west {
		t.Errorf("the band (40 m) holds %d rocks, the 40 m inside it %d: want fewer, not none", band, west)
	}
	// The classic scatter (no region) is untouched by any of this.
	c1 := scatterOnce(t, small, 2, r)
	c2, err := Scatter(small, ScatterSettings{Rules: []Rule{r}, Seed: 99, Margin: 1}, pool(5))
	if err != nil {
		t.Fatal(err)
	}
	if len(c1.Instances) != len(c2.Instances) {
		t.Errorf("classic scatters differ")
	}
}

func TestRegionPatchesFollowTheWeight(t *testing.T) {
	g := groundOf(t, 256, 1, gentle)
	r := DefaultRule()
	r.Slope, r.Cluster = [2]float64{0, 40}, 0
	ps, err := Patches(g, &r, PatchSettings{Cell: 16, Threshold: 0.4, MaxSize: 64, Region: &Region{Weight: westWeight}}, pool(4))
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) == 0 {
		t.Fatal("no patches")
	}
	for _, p := range ps {
		if p.MaxX > 16+1e-9 {
			t.Errorf("a patch reaches x %.0f, where the weight is under the threshold", p.MaxX)
		}
		if math.Mod(p.MinX, 16) != 0 || math.Mod(p.MinZ, 16) != 0 {
			t.Errorf("a patch at %.1f,%.1f is off the world's 16 m grid", p.MinX, p.MinZ)
		}
	}
}

func TestWallKeepDropsColumns(t *testing.T) {
	g := stepGround(t, 128, 0.5, 25)
	lines := TraceCliffFeet(g, TraceSettings{Threshold: 0.5, Cell: 2, MinLength: 20, Smooth: 2})
	s := DefaultWall()
	s.Chunks, s.Seed = chunks(3), 5
	all, err := Walls(g, lines, s, pool(1))
	if err != nil {
		t.Fatal(err)
	}
	s.Keep = func(x, z float64) bool { return z < 0 }
	half, err := Walls(g, lines, s, pool(1))
	if err != nil {
		t.Fatal(err)
	}
	if len(half.Instances) == 0 || len(half.Instances) >= len(all.Instances) {
		t.Fatalf("keeping half the wall placed %d chunks of %d", len(half.Instances), len(all.Instances))
	}
	for _, in := range half.Instances {
		if in.Pos[2] > 6 {
			t.Errorf("a chunk at z %.1f, outside the kept half", in.Pos[2])
		}
	}
}
