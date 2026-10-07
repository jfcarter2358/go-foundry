package collision

import (
	"math/rand"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// TestEntityGeometryPassMatchesTheFullResolve holds the pass's two shortcuts —
// the geometry list built once, and the reach reject in front of each
// geometry's resolve — to the plain per-call resolve, bit for bit, for bodies
// scattered in, around, against and far from a hut that is both moved and
// turned, so the cached placement inverse is exercised too.
func TestEntityGeometryPassMatchesTheFullResolve(t *testing.T) {
	lvl := hutLevel(t, mgl32.Vec3{3, 0.5, -2})
	lvl.Entities[0].Rotation = mgl32.Vec3{0, 0.7, 0}
	world.RefreshFrames(lvl, lvl.Frames)
	world0 := lvl.Frames.FrameFor(&world.Entity{})
	if world0 == nil {
		t.Fatal("no world frame")
	}

	r := rand.New(rand.NewSource(7))
	type result struct {
		out  mgl32.Vec3
		f, c bool
	}
	run := func(pass, reject bool, p mgl32.Vec3, radius, half float32) result {
		entityReachReject = reject
		defer func() { entityReachReject = true }()
		if pass {
			beginEntityGeometryPass(lvl)
			defer endEntityGeometryPass()
		}
		out, f, c := resolveAgainstEntityBrushes(lvl, nil, world0, p, radius, half)
		return result{out, f, c}
	}
	touched := 0
	for i := 0; i < 20000; i++ {
		p := mgl32.Vec3{r.Float32()*20 - 7, r.Float32()*6 - 1.5, r.Float32()*20 - 12}
		radius := 0.1 + r.Float32()*0.6
		half := 0.2 + r.Float32()*1.0
		want := run(false, false, p, radius, half)
		got := run(true, true, p, radius, half)
		if got != want {
			t.Fatalf("body at %v r=%v h=%v: pass+reject %+v, full resolve %+v", p, radius, half, got, want)
		}
		if want.out != p {
			touched++
		}
	}
	if touched < 1000 {
		t.Fatalf("only %d of the bodies touched the hut; the test is not exercising contact", touched)
	}
}
