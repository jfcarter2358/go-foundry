package ruins

import (
	"context"
	"math"
	"reflect"
	"testing"
	"time"

	"foundry/internal/procgen/sdf"
)

// TestVariantsMesh meshes every variant coarsely: each is a closed, finite
// solid of about its stated size, standing on its base.
func TestVariantsMesh(t *testing.T) {
	lib := Library(7, 1)
	if len(lib) < 10 {
		t.Fatalf("%d variants", len(lib))
	}
	for _, v := range lib {
		v := v
		t.Run(v.Name, func(t *testing.T) {
			if testing.Short() && v.Kind != KindWall {
				t.Skip("short")
			}
			t0 := time.Now()
			s := v.Build()
			cell := 0.2
			m, st, err := sdf.Polygonize(s, sdf.MeshOptions{Cell: cell, Ctx: context.Background(), Sharpness: 0.8})
			if err != nil {
				t.Fatal(err)
			}
			if m.TriangleCount() < 200 {
				t.Fatalf("%d triangles", m.TriangleCount())
			}
			b := m.Bounds()
			vb := v.Box()
			for k := 0; k < 3; k++ {
				if b.Lo[k] < vb.Lo[k]-0.3 || b.Hi[k] > vb.Hi[k]+0.3 {
					t.Errorf("mesh bounds %v outside the variant's box %v", b, vb)
					break
				}
			}
			if b.Hi[1] < v.Height*0.7 {
				t.Errorf("mesh top %.1f, the variant says %.1f", b.Hi[1], v.Height)
			}
			t.Logf("%s: %d tris at %.2f m (%v cells, %d evals) in %s; bounds %.1f..%.1f × %.1f..%.1f × %.1f..%.1f",
				v.Name, m.TriangleCount(), cell, st.Cells, st.Evaluations, time.Since(t0).Round(time.Millisecond),
				b.Lo[0], b.Hi[0], b.Lo[1], b.Hi[1], b.Lo[2], b.Hi[2])
		})
	}
}

// slope is a test mountainside: falling 0.45 m a metre to +z, with a bulge
// and a cliff band, rising across x a little.
func slope(x, z float64) float64 {
	h := 100 - 0.45*z + 0.05*x + 3*math.Sin(z/17)*math.Cos(x/23)
	if z > 120 && z < 135 {
		h -= (z - 120) * 1.5 // a cliff band: 22 m in 15 m
	} else if z >= 135 {
		h -= 22.5
	}
	return h
}

func testSettings() Settings {
	s := Defaults()
	s.Seed = 99
	s.Path = []PathPoint{{0, 0, 26}, {10, 60, 28}, {-5, 130, 28}, {5, 190, 26}}
	return s
}

// TestLayoutStandsOnTheGround: every floor is over the ground under it,
// every body reaches under the ground (no terrace, stair or plinth
// floats), and every building stands on a floor.
func TestLayoutStandsOnTheGround(t *testing.T) {
	lib := Library(7, 1)
	l := Lay(testSettings(), slope, lib)
	if len(l.Terraces) < 4 || len(l.Stairs) < 3 {
		t.Fatalf("%d terraces, %d stairs", len(l.Terraces), len(l.Stairs))
	}
	for i, tr := range l.Terraces {
		lo, hi := groundRange(slope, tr.Rect)
		if tr.Floor < hi {
			t.Errorf("terrace %d: floor %.2f under the ground's %.2f", i, tr.Floor, hi)
		}
		if tr.Bottom > lo-0.5 {
			t.Errorf("terrace %d: body bottom %.2f does not reach under the ground's lowest %.2f", i, tr.Bottom, lo)
		}
	}
	for i, s := range l.Stairs {
		lo, _ := groundRange(slope, s.Rect)
		if s.Base > lo-0.5 {
			t.Errorf("stair %d: base %.2f over the ground's lowest %.2f", i, s.Base, lo)
		}
	}
	for i, p := range l.Plinths {
		lo, hi := groundRange(slope, p.Rect)
		if p.Top < hi || p.Bottom > lo-0.5 {
			t.Errorf("plinth %d: %.2f..%.2f against the ground's %.2f..%.2f", i, p.Bottom, p.Top, lo, hi)
		}
	}
	// Each building's base is on a terrace floor or a plinth top (or, for
	// debris, sunk into the ground).
	for _, in := range l.Instances {
		on := false
		for _, tr := range l.Terraces {
			if a, b := tr.Local(in.X, in.Z); math.Abs(a) <= tr.HA+0.01 && math.Abs(b) <= tr.HB+0.01 && math.Abs(in.Y-tr.Floor) < 1e-6 {
				on = true
			}
		}
		for _, p := range l.Plinths {
			if a, b := p.Local(in.X, in.Z); math.Abs(a) <= p.HA && math.Abs(b) <= p.HB && math.Abs(in.Y-p.Top) < 1e-6 {
				on = true
			}
		}
		if !on && in.On == -2 && in.Y <= slope(in.X, in.Z) {
			on = true
		}
		if !on {
			t.Errorf("%s at %.1f,%.1f,%.1f stands on nothing (ground %.1f)", in.Variant, in.X, in.Y, in.Z, slope(in.X, in.Z))
		}
	}
	t.Logf("%d terraces, %d stairs, %d plinths, %d instances, %d spots", len(l.Terraces), len(l.Stairs), len(l.Plinths), len(l.Instances), len(l.Spots))
}

// TestStairsAreWalkable: the way down is unbroken — each stair starts where
// a terrace ends, at its floor, and ends where the next begins, at its floor
// — and every riser is within the rise limit (the player's step is 0.5) on
// a tread a foot can take.
func TestStairsAreWalkable(t *testing.T) {
	st := testSettings()
	l := Lay(st, slope, Library(7, 1))
	end := func(r Rect, sign float64) (float64, float64) { return r.World(sign*r.HA, 0) }
	tend := func(t Terrace, sign float64) (float64, float64) { return t.World(sign*t.HA, t.WalkB) }
	si := 0
	for i := 0; i+1 < len(l.Terraces); i++ {
		a, b := l.Terraces[i], l.Terraces[i+1]
		ax, az := tend(a, 1)
		bx, bz := tend(b, -1)
		if math.Hypot(ax-bx, az-bz) < 0.01 {
			if math.Abs(a.Floor-b.Floor) > 0.06 {
				t.Errorf("terraces %d and %d meet with a %.2f m step and no stair", i, i+1, a.Floor-b.Floor)
			}
			continue
		}
		if si >= len(l.Stairs) {
			t.Fatalf("terraces %d and %d are apart and no stair is left", i, i+1)
		}
		s := l.Stairs[si]
		si++
		sx, sz := end(s.Rect, -1)
		fx, fz := end(s.Rect, 1)
		if math.Hypot(sx-ax, sz-az) > 0.01 || math.Hypot(fx-bx, fz-bz) > 0.01 {
			t.Errorf("stair %d does not join terraces %d and %d", si-1, i, i+1)
		}
		if math.Abs(s.Top-a.Floor) > 1e-9 || math.Abs(s.Bottom-b.Floor) > 1e-9 {
			t.Errorf("stair %d runs %.2f → %.2f, the floors are %.2f → %.2f", si-1, s.Top, s.Bottom, a.Floor, b.Floor)
		}
		if math.Abs(s.Rise) > st.Rise+1e-9 || math.Abs(s.Rise) > 0.5 {
			t.Errorf("stair %d: riser %.3f over the limit %.2f", si-1, s.Rise, st.Rise)
		}
		if s.Tread < 0.25 {
			t.Errorf("stair %d: tread %.3f", si-1, s.Tread)
		}
		// Walking it: the walking height never jumps by more than a riser.
		prev := s.Top
		for x := -s.HA - 0.5; x <= s.HA+0.5; x += 0.05 {
			h := s.StepTop(x)
			if math.Abs(h-prev) > math.Abs(s.Rise)+1e-9 {
				t.Fatalf("stair %d: a %.2f m step at %.2f", si-1, h-prev, x)
			}
			prev = h
		}
		if math.Abs(prev-s.Bottom) > 1e-9 {
			t.Errorf("stair %d ends at %.2f, not its foot %.2f", si-1, prev, s.Bottom)
		}
	}
	// The cliff band forced at least one terrace onto a foundation.
	tall := 0.0
	for _, tr := range l.Terraces {
		tall = math.Max(tall, tr.Floor-tr.GroundLo)
	}
	if tall < 4 {
		t.Errorf("the tallest retaining wall is %.1f m: the cliff band should have needed a tall one", tall)
	}
}

// TestLayoutIsDeterministic: the same settings make the same layout, and
// the seed changes it.
func TestLayoutIsDeterministic(t *testing.T) {
	lib := Library(7, 1)
	a := Lay(testSettings(), slope, lib)
	b := Lay(testSettings(), slope, lib)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("two layouts of the same settings differ")
	}
	s := testSettings()
	s.Seed = 100
	if c := Lay(s, slope, lib); reflect.DeepEqual(a.Instances, c.Instances) {
		t.Fatal("another seed made the same buildings")
	}
	// The variants' fields are deterministic too.
	v1, v2 := Library(7, 1)[0].Build(), Library(7, 1)[0].Build()
	for _, q := range []sdf.Vec3{{1, 3, 4.6}, {0, 12, 2}, {3.9, 0.6, 0.2}} {
		if v1.F(q) != v2.F(q) {
			t.Fatalf("variant field differs at %v", q)
		}
	}
}

// TestSiteFieldMatchesTheLayout: the site's field is solid just under each
// floor and each tread, open just over them, and solid under the ground's
// lowest point under a terrace (its foundation).
func TestSiteFieldMatchesTheLayout(t *testing.T) {
	st := testSettings()
	l := Lay(st, slope, Library(7, 1))
	site := NewSite(l, 3, st.Walkway, 0)
	f := site.Shape().F
	for i, tr := range l.Terraces {
		x, z := tr.World(0, tr.WalkB) // the walkway's middle
		if f(sdf.Vec3{x, tr.Floor - 0.05, z}) >= 0 || f(sdf.Vec3{x, tr.Floor + 0.08, z}) <= 0 {
			t.Errorf("terrace %d: the floor is not at %.2f (%.3f under, %.3f over)", i, tr.Floor,
				f(sdf.Vec3{x, tr.Floor - 0.05, z}), f(sdf.Vec3{x, tr.Floor + 0.08, z}))
		}
		if f(sdf.Vec3{x, tr.GroundLo - 0.3, z}) >= 0 {
			t.Errorf("terrace %d: no foundation under the ground", i)
		}
	}
	for i, s := range l.Stairs {
		for k := 0; k < s.Steps; k++ {
			a := -s.HA + (float64(k)+0.5)*s.Tread
			x, z := s.World(a, 0)
			h := s.StepTop(a)
			if f(sdf.Vec3{x, h - 0.04, z}) >= 0 || f(sdf.Vec3{x, h + 0.06, z}) <= 0 {
				t.Errorf("stair %d step %d: the tread is not at %.2f", i, k, h)
				break
			}
		}
	}
}
