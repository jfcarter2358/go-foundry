package terrain

// basin_test.go — CarveBasins' profile, levels and keys on hand-made
// grids, and the distance transform under it.

import (
	"math"
	"testing"
)

// flatGrid is an n-cell square of cell metres from (x0, z0) at height h,
// tilted by slope metres per metre along x.
func flatGrid(n int, cell, x0, z0 float64, h, slope float32) *Grid {
	g := &Grid{N: n, Cell: cell, MinX: x0, MinZ: z0, H: make([]float32, (n+1)*(n+1))}
	for j := 0; j <= n; j++ {
		for i := 0; i <= n; i++ {
			g.H[j*(n+1)+i] = h + slope*float32(g.X(i))
		}
	}
	return g
}

// disc is a painted disc of radius r at (cx, cz), 1 inside with a 1 m
// linear edge (as a soft brush paints it).
func disc(cx, cz, r float64) func(x, z float64) float64 {
	return func(x, z float64) float64 {
		d := math.Hypot(x-cx, z-cz)
		return math.Max(0, math.Min(1, r+0.5-d))
	}
}

func TestEDTIsExact(t *testing.T) {
	w, h := 9, 7
	in := make([]bool, w*h)
	in[3*w+4] = true
	d := edt(in, w, h, true)
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			want := float32((i-4)*(i-4) + (j-3)*(j-3))
			if d[j*w+i] != want {
				t.Fatalf("(%d, %d): %v, want %v", i, j, d[j*w+i], want)
			}
		}
	}
	// Inside a filled window, distance to the border (one beyond it).
	for k := range in {
		in[k] = true
	}
	d = edt(in, w, h, false)
	if d[3*w+4] != 16 || d[0] != 1 {
		t.Fatalf("centre %v (want 16), corner %v (want 1)", d[3*w+4], d[0])
	}
}

func TestBasinProfileShelvesFromTheShore(t *testing.T) {
	g := flatGrid(100, 1, -50, -50, 10, 0)
	pr := DefaultBasinProfile()
	pr.Noise = 0
	r := CarveBasins(g, disc(0, 0, 20), [4]float64{-25, -25, 25, 25}, pr, nil)
	if len(r.Bodies) != 1 {
		t.Fatalf("%d bodies", len(r.Bodies))
	}
	b := r.Bodies[0]
	if math.Abs(b.Level-(10-pr.Freeboard)) > 1e-4 || math.Abs(b.Spill-10) > 1e-4 || b.Locked {
		t.Fatalf("level %.4f spill %.4f locked %v", b.Level, b.Spill, b.Locked)
	}
	if b.Anchor != [2]float64{0, 0} {
		t.Errorf("anchor %v, want the centre", b.Anchor)
	}
	lv := float32(b.Level)
	depthAt := func(d float64) float64 { return float64(lv - g.Sample(20-d, 0)) }
	// The shore is at the outline: the bed is at the water within a few
	// cm there, at the shelf a bank in, and deepest in the middle.
	if v := depthAt(0); math.Abs(v) > 0.06 {
		t.Errorf("depth at the outline %.3f", v)
	}
	if v := depthAt(pr.Bank + 0.5); math.Abs(v-pr.ShelfDepth) > 0.08 {
		t.Errorf("depth on the shelf %.3f, want %.2f", v, pr.ShelfDepth)
	}
	if v := depthAt(20); math.Abs(v-pr.Depth) > 0.05 {
		t.Errorf("depth in the middle %.3f, want %.2f", v, pr.Depth)
	}
	// Deeper inland, never a step: the bed falls monotonically and no
	// faster than 0.6 m per metre.
	prev := depthAt(0)
	for d := 0.5; d <= 20; d += 0.5 {
		v := depthAt(d)
		if v < prev-1e-3 || v-prev > 0.3 {
			t.Fatalf("at %.1f m in: depth %.3f after %.3f", d, v, prev)
		}
		prev = v
	}
	// The ground outside is untouched (flat ground needs no bank cut).
	if h := g.Sample(30, 0); h != 10 {
		t.Errorf("outside: %v", h)
	}
	// The water covers the inside, at the level, and stops at the rim.
	k := 50*101 + 50
	if r.Water.Body[k] != 0 || r.Water.Level[k] != lv {
		t.Errorf("centre water body %d level %v", r.Water.Body[k], r.Water.Level[k])
	}
	if r.Water.Body[50*101+50+22] >= 0 {
		t.Error("water outside the rim")
	}
	if b.Area < math.Pi*18*18 || b.Area > math.Pi*21*21 {
		t.Errorf("area %.0f", b.Area)
	}
}

func TestBasinOnASlopeCutsTheBankAndHoldsTheWater(t *testing.T) {
	g := flatGrid(120, 1, -60, -60, 10, 0.08) // 4.6° along x
	pr := DefaultBasinProfile()
	r := CarveBasins(g, disc(0, 0, 15), [4]float64{-20, -20, 20, 20}, pr, nil)
	b := r.Bodies[0]
	// The level is the rim's low (downhill, -x) side.
	if want := 10 + 0.08*(-16.5); math.Abs(b.Spill-want) > 0.15 {
		t.Errorf("spill %.3f, want about %.3f", b.Spill, want)
	}
	// Uphill, the outer bank rises no steeper than bank_slope: no cliff.
	for x := 15.0; x < 15+pr.Reach; x += 0.5 {
		s := (g.Sample(x+0.5, 0) - g.Sample(x-0.5, 0))
		if float64(s) > pr.BankSlope+0.12 {
			t.Fatalf("bank at x=%.1f rises %.2f per metre", x, s)
		}
	}
	// Every rim sample stands above the water.
	for a := 0.0; a < 2*math.Pi; a += 0.05 {
		if h := g.Sample(16.2*math.Cos(a), 16.2*math.Sin(a)); float64(h) < b.Level {
			t.Fatalf("rim at %.2f rad: %.3f under the water %.3f", a, h, b.Level)
		}
	}
}

func TestBasinPinsSurviveRepaintingAndSetTheLevel(t *testing.T) {
	area := disc(0, 0, 12)
	g := flatGrid(80, 1, -40, -40, 5, 0)
	pins := []BasinLevel{{At: [2]float64{3, -2}, Level: 5.3}}
	r := CarveBasins(g, area, [4]float64{-15, -15, 15, 15}, DefaultBasinProfile(), pins)
	if b := r.Bodies[0]; !b.Locked || b.Level != float64(float32(5.3)) || b.Pin != 0 {
		t.Fatalf("pinned body %+v", b)
	}
	// A berm holds water above the old ground: the rim reaches the level.
	if h := g.Sample(13, 0); float64(h) < 5.3 {
		t.Errorf("rim %.3f under the pinned level", h)
	}
	// Repainted bigger and lopsided, the pin still finds it.
	g2 := flatGrid(80, 1, -40, -40, 5, 0)
	area2 := func(x, z float64) float64 { return math.Max(area(x, z), disc(10, 0, 9)(x, z)) }
	r2 := CarveBasins(g2, area2, [4]float64{-15, -15, 22, 15}, DefaultBasinProfile(), pins)
	if len(r2.Bodies) != 1 || !r2.Bodies[0].Locked || len(r2.Orphans) != 0 {
		t.Fatalf("after repainting: %+v orphans %v", r2.Bodies, r2.Orphans)
	}
	// A pin on dry ground is an orphan.
	r3 := CarveBasins(flatGrid(80, 1, -40, -40, 5, 0), disc(-20, -20, 6), [4]float64{-30, -30, -10, -10}, DefaultBasinProfile(), pins)
	if len(r3.Orphans) != 1 || r3.Bodies[0].Locked {
		t.Fatalf("orphans %v, body %+v", r3.Orphans, r3.Bodies)
	}
}

func TestMarshHummocksBreakTheSurface(t *testing.T) {
	g := flatGrid(120, 1, -60, -60, 3, 0)
	pr := DefaultBasinProfile()
	pr.ShelfDepth, pr.Depth, pr.Bank, pr.Shelf = 0.15, 0.4, 2, 2
	pr.Hummocks, pr.HummockSize = 0.45, 4
	r := CarveBasins(g, disc(0, 0, 40), [4]float64{-45, -45, 45, 45}, pr, nil)
	b := r.Bodies[0]
	// Islands: samples well inside the outline that stay dry.
	dry, wet := 0, 0
	s := g.S()
	for j := 0; j < s; j++ {
		for i := 0; i < s; i++ {
			if math.Hypot(g.X(i), g.Z(j)) > 30 {
				continue
			}
			if r.Water.Body[j*s+i] >= 0 {
				wet++
			} else {
				dry++
			}
		}
	}
	if wet == 0 || dry < wet/20 || dry > wet {
		t.Errorf("inside the marsh: %d wet, %d dry samples", wet, dry)
	}
	if b.MaxDepth > 0.8 {
		t.Errorf("a marsh %.2f m deep", b.MaxDepth)
	}
}
