package cave

import (
	"context"
	"math"
	"testing"
	"time"

	"foundry/internal/procgen/sdf"
	"foundry/internal/procgen/terrain"
)

// hill is a test ground: flat at 0 for x < 0, a 20 m high plateau from
// x = 10 on, a cliff between.
func hill() *terrain.Grid {
	g := terrain.NewGrid(128, 0.5)
	sz := g.S()
	for j := 0; j < sz; j++ {
		for i := 0; i < sz; i++ {
			x := g.X(i)
			h := 20 * math.Min(math.Max((x-6)/4, 0), 1)
			g.H[j*sz+i] = float32(h)
		}
	}
	return g
}

// tunnel runs from the flat ground at x = -10 into the plateau.
func tunnel() Settings {
	s := Defaults()
	s.Seed = 7
	s.Path = []Point{{X: -12, Y: 0, Z: 0, Width: 7, Height: 6}, {X: 8, Y: 0, Z: 0, Width: 7, Height: 6},
		{X: 25, Y: 1, Z: 5, Width: 10, Height: 8}}
	return s
}

func TestTheVoidIsOpenInsideAndRockOutside(t *testing.T) {
	v := New(tunnel())
	if v.Empty() {
		t.Fatal("empty")
	}
	// The middle of the passage, a metre and a half up, is void.
	l, ok := v.Local(sdf.Vec3{2, 1.5, 0})
	if !ok {
		t.Fatal("no local frame at the passage")
	}
	if d := v.At(sdf.Vec3{2, 2.5, l.A * 0}); d >= 0 {
		t.Errorf("mid-passage is not void: %.2f", d)
	}
	// Under the floor and well past the walls: rock.
	if d := v.At(sdf.Vec3{2, -0.6, 0}); d <= 0 {
		t.Errorf("under the floor is void: %.2f", d)
	}
	if d := v.At(sdf.Vec3{2, 2, 9}); d <= 0 {
		t.Errorf("beside the wall is void: %.2f", d)
	}
	if d := v.At(sdf.Vec3{2, 12, 0}); d <= 0 {
		t.Errorf("over the roof is void: %.2f", d)
	}
	// The floor is flat-ish: the void reaches down to about the path's
	// floor across the middle lane.
	for _, z := range []float64{-1, 0, 1} {
		if fl, _, ok := v.FloorAt(4, 2, z); !ok || math.Abs(fl) > 0.5 {
			t.Errorf("floor at z=%g: %.2f (ok %v)", z, fl, ok)
		}
	}
}

func TestTheCutOpensTheMouthAndOnlyTheMouth(t *testing.T) {
	g := hill()
	v := New(tunnel())
	pool := terrain.NewPool(context.Background(), 4)
	cut, n, err := Cut(v, g, 0.3, pool)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("nothing cut: the mouth is sealed")
	}
	// The flat ground in front of the mouth is the cave's floor carried
	// out: never cut. The cliff quads the passage goes through are.
	N := g.N
	cliffCut, flatCut := 0, 0
	for j := 0; j < N; j++ {
		for i := 0; i < N; i++ {
			if !cut[j*N+i] {
				continue
			}
			x := g.X(i) + g.Cell/2
			switch {
			case x < 5.5:
				flatCut++
			case x < 11:
				cliffCut++
			}
		}
	}
	if cliffCut == 0 {
		t.Errorf("the cliff was not cut where the passage enters it")
	}
	if flatCut > 0 {
		t.Errorf("%d quads of the flat ground in front of the mouth were cut", flatCut)
	}
	// No kept quad's surface is inside the void (more than the margin
	// above its floor).
	for j := 0; j < N; j++ {
		for i := 0; i < N; i++ {
			if cut[j*N+i] {
				continue
			}
			x, z := g.X(i)+g.Cell/2, g.Z(j)+g.Cell/2
			lo := math.Min(math.Min(float64(g.At(i, j)), float64(g.At(i+1, j))), math.Min(float64(g.At(i, j+1)), float64(g.At(i+1, j+1))))
			hi := math.Max(math.Max(float64(g.At(i, j)), float64(g.At(i+1, j))), math.Max(float64(g.At(i, j+1)), float64(g.At(i+1, j+1))))
			for y := lo; y <= hi; y += 0.1 {
				p := sdf.Vec3{x, y, z}
				if l, ok := v.Local(p); ok && l.B > 0.5 && v.At(p) < -0.05 {
					t.Fatalf("kept quad (%d,%d) passes through the void at %v (%.2f)", i, j, p, v.At(p))
				}
			}
		}
	}
}

func TestTheRockIsTheGroundLessTheVoid(t *testing.T) {
	g := hill()
	v := New(tunnel())
	pool := terrain.NewPool(context.Background(), 4)
	cut, _, _ := Cut(v, g, 0.3, pool)
	r := NewRock(v, g, HoleDistance(g, cut, 3))
	for _, c := range []struct {
		p    sdf.Vec3
		rock bool
	}{
		{sdf.Vec3{20, 3, 2}, false},   // in the passage
		{sdf.Vec3{20, 14, 30}, true},  // deep in the plateau, away from it
		{sdf.Vec3{20, 25, 30}, false}, // over the plateau
		{sdf.Vec3{-20, 1, 30}, false}, // over the flat
	} {
		if got := r.F(c.p) < 0; got != c.rock {
			t.Errorf("%v: rock %v, want %v (F %.2f)", c.p, got, c.rock, r.F(c.p))
		}
	}
	// Round the hole the rock stands a lip over the ground; away from it
	// it lies under the ground.
	if off := r.Offset(30, 30); off >= 0 {
		t.Errorf("away from the hole the rock is not hidden: %.2f", off)
	}
}

func TestTheSkyDarkensAwayFromTheMouth(t *testing.T) {
	g := hill()
	s := tunnel()
	s.Path = append(s.Path, Point{X: 45, Y: 1, Z: 0, Width: 7, Height: 6})
	v := New(s)
	pool := terrain.NewPool(context.Background(), 4)
	t0 := time.Now()
	sky, err := BakeSky(v, g, 1, 16, pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("sky %d×%d×%d in %s", sky.NX, sky.NY, sky.NZ, time.Since(t0))
	open := sky.At(sdf.Vec3{-8, 2, 0})
	mouth := sky.At(sdf.Vec3{9, 2, 0})
	deep := sky.At(sdf.Vec3{38, 3, 0})
	t.Logf("open %.3f, mouth %.3f, deep %.3f", open, mouth, deep)
	if !(open > mouth && mouth > deep) || deep > 0.05 || open < 0.5 {
		t.Errorf("sky open %.3f, mouth %.3f, deep %.3f: want falling, deep near 0", open, mouth, deep)
	}
}

func TestTheRockMeshes(t *testing.T) {
	g := hill()
	v := New(tunnel())
	pool := terrain.NewPool(context.Background(), 4)
	cut, _, _ := Cut(v, g, 0.3, pool)
	r := NewRock(v, g, HoleDistance(g, cut, 3))
	box := sdf.Box{Lo: sdf.Vec3{0, -3, -16}, Hi: sdf.Vec3{32, 24, 16}}
	shape := sdf.Shape{F: func(p sdf.Vec3) float64 {
		b := math.Max(math.Max(box.Lo[0]-p[0], p[0]-box.Hi[0]), math.Max(math.Max(box.Lo[1]-p[1], p[1]-box.Hi[1]), math.Max(box.Lo[2]-p[2], p[2]-box.Hi[2])))
		return math.Max(r.F(p), b)
	}, Box: box, Lip: r.LipBound()}
	t0 := time.Now()
	m, st, err := sdf.Polygonize(shape, sdf.MeshOptions{Cell: 0.3, Weathering: 1, Sharpness: 0.35})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d triangles, %d evaluations, in %s", m.TriangleCount(), st.Evaluations, time.Since(t0))
	if m.TriangleCount() < 1000 {
		t.Errorf("only %d triangles", m.TriangleCount())
	}
}
