package ai

// ai_nanite_test.go — an agent across a cooked Nanite cliff
// (docs/nanite/PHASE_2b.md). Navigation rasterises the mesh's collision cut
// and the walker's resolve stands on it, so an agent sent to the top of a
// mesa takes its ramp, never its cliff, and arrives standing on the surface
// the mesh draws.

import (
	"math"
	"testing"

	"foundry/internal/collision"
	"foundry/internal/hooks"
	"foundry/internal/meshcollide"
	"foundry/internal/nanite"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// mesaHeight is a mesa's surface: a 2 cm apron west of x 6, a plateau 1.6 m
// up east of x 12 (under aiLevel's 4 m ceiling with room to stand), joined by a ramp only where z is in [8, 12]; elsewhere
// the plateau ends in a cliff at x 12.
func mesaHeight(x, z float32) float32 {
	const top = 1.6
	switch {
	case x >= 12:
		return top
	case z >= 8 && z <= 12 && x >= 6:
		return max(0.02, top*(x-6)/6)
	default:
		return 0.02
	}
}

// mesa cooks the mesa as a closed solid over x, z in [2, 18]: its surface a
// quarter metre a quad, a flat bottom at 0 and skirts round the sides. Big,
// so it collides in the mesh mode; watertight, so navigation reads its
// inside as solid.
func mesa(t *testing.T) world.Scenery {
	t.Helper()
	// Pinned on: FOUNDRY_NANITE_COLLISION=0 in the environment would make
	// this a test of the box path.
	prevNC := world.NaniteCollision
	world.NaniteCollision = true
	t.Cleanup(func() { world.NaniteCollision = prevNC })
	const n = 64
	var pos []float32
	vert := func(i, j int, y func(x, z float32) float32) {
		x := 2 + 16*float32(i)/n
		z := 2 + 16*float32(j)/n
		pos = append(pos, x, y(x, z), z)
	}
	for j := 0; j <= n; j++ {
		for i := 0; i <= n; i++ {
			vert(i, j, mesaHeight)
		}
	}
	for j := 0; j <= n; j++ {
		for i := 0; i <= n; i++ {
			vert(i, j, func(x, z float32) float32 { return 0 })
		}
	}
	w := uint32(n + 1)
	top := func(i, j uint32) uint32 { return j*w + i }
	bot := func(i, j uint32) uint32 { return w*w + j*w + i }
	var idx []uint32
	for j := uint32(0); j < n; j++ {
		for i := uint32(0); i < n; i++ {
			a, b, c, d := top(i, j), top(i+1, j), top(i, j+1), top(i+1, j+1)
			idx = append(idx, a, c, b, b, c, d) // up
			a, b, c, d = bot(i, j), bot(i+1, j), bot(i, j+1), bot(i+1, j+1)
			idx = append(idx, a, b, c, b, d, c) // down
		}
	}
	// Skirts: each edge of the boundary, top to bottom, wound outward.
	for k := uint32(0); k < n; k++ {
		quad := func(t0, t1, b0, b1 uint32) { idx = append(idx, t0, b0, t1, t1, b0, b1) }
		quad(top(k+1, 0), top(k, 0), bot(k+1, 0), bot(k, 0)) // -z
		quad(top(k, n), top(k+1, n), bot(k, n), bot(k+1, n)) // +z
		quad(top(0, k), top(0, k+1), bot(0, k), bot(0, k+1)) // -x
		quad(top(n, k+1), top(n, k), bot(n, k+1), bot(n, k)) // +x
	}
	d, err := meshcollide.Build(meshcollide.Input{Positions: pos, Stride: 3, Indices: idx}, meshcollide.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if d.OpenEdges != 0 || d.NonManifold != 0 {
		t.Fatalf("the mesa is not watertight: %d open, %d non-manifold edges", d.OpenEdges, d.NonManifold)
	}
	s := world.NewScenery("mesa", "models/mesa.fnm", mgl32.Vec3{})
	lo, hi := mgl32.Vec3{2, 0, 2}, mgl32.Vec3{18, 1.6, 18}
	s.Nanite = &nanite.Mesh{Lo: lo, Hi: hi, Collision: d}
	s.LocalMin, s.LocalMax = lo, hi
	return s
}

func TestAnAgentTakesTheRampUpACookedMesaNotTheCliff(t *testing.T) {
	lvl := aiLevel(t)
	lvl.Scenery = []world.Scenery{mesa(t)}
	if m := lvl.Scenery[0].CollisionMode(); m != meshcollide.ModeMesh {
		t.Fatalf("the mesa resolves to %v, want mesh", m)
	}
	collision.InvalidateScenery()
	lvl.Entities = []world.Entity{agent("guard", mgl32.Vec3{4, 0.02, 4})}
	goal := mgl32.Vec3{15, 1.6, 4}
	if !MoveTo(lvl, &lvl.Entities[0], goal, false) {
		t.Fatal("MoveTo called the top of the mesa unreachable")
	}
	var events []hooks.AIEvent
	const dt = float32(1.0 / 60.0)
	usedRamp := false
	for i := 0; i < 60*25; i++ {
		Update(lvl, dt, nil)
		collision.EntityUpdate(lvl, dt, true, nil)
		events = append(events, hooks.TakeAIEvents()...)
		f := Feet(lvl, &lvl.Entities[0])
		surface := mesaHeight(f.X(), f.Z())
		if f.X() < 11.9 && f.Y() > 0.3 {
			// Off the ground west of the cliff, the only floor is the ramp.
			if f.Z() < 7.6 || f.Z() > 12.4 {
				t.Fatalf("frame %d: the guard is at %v, off the ground beside the ramp — up the cliff", i, f)
			}
			usedRamp = true
		}
		if f.X() > 2.5 && f.X() < 17.5 && f.Z() > 2.5 && f.Z() < 17.5 && f.Y() < surface-0.15 {
			t.Fatalf("frame %d: the guard's feet are at %v, under the mesa's surface at %v", i, f, surface)
		}
		if count(events, "ai_arrived") > 0 {
			break
		}
	}
	f := Feet(lvl, &lvl.Entities[0])
	if count(events, "ai_arrived") != 1 {
		t.Fatalf("the guard never arrived; it is at %v, events %v", f, events)
	}
	if !usedRamp {
		t.Error("the guard arrived without walking the ramp")
	}
	if math.Abs(float64(f.Y()-1.6)) > 0.15 {
		t.Errorf("the guard arrived with its feet at %v, not on the plateau at 1.6", f.Y())
	}
}
