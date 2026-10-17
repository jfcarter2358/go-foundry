package render

// nanite_draw_distance_test.go — the draw-distance bookkeeping that needs no
// GL (nanite_draw_distance.go): what an instance record carries, the CPU's
// look ahead passing everything the GPU keeps, and the virtual shadow map's
// tracker redrawing a page when a caster crosses its shadow distance.

import (
	"testing"

	"foundry/internal/nanite"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func TestInstanceDrawRecordsTheWindow(t *testing.T) {
	if d := instanceDraw(world.DrawWindow{}, 0.4); d != ([4]float32{}) {
		t.Errorf("an open window wrote %v, want zero (drawn everywhere, as before)", d)
	}
	if d := instanceDraw(world.DrawWindow{From: 100, To: 900}, 0.4); d != ([4]float32{100, 900, 0.4, 0}) {
		t.Errorf("window [100, 900): %v", d)
	}
	// None: a window nothing is inside, whatever the distance.
	d := instanceDraw(world.DrawWindow{None: true}, 0.4)
	if !(d[0] > d[1] && d[1] > 0) {
		t.Errorf("an empty window wrote %v: some distance passes it", d)
	}
}

// gpuKeeps is nanite_instance_cull_comp.glsl's drawKeeps, for the test.
func gpuKeeps(dr [4]float32, d float32, mode int32) bool {
	if dr[0] <= 0 && dr[1] <= 0 {
		return true
	}
	k, kFrom := float32(1), float32(0.9)
	if mode == drawHashed {
		k = 0.9 + 0.1*dr[2]
		kFrom = k
	}
	if dr[0] > 0 && d < dr[0]*kFrom {
		return false
	}
	return dr[1] <= 0 || d < dr[1]*k
}

func TestCPULookAheadKeepsWhatTheGPUKeeps(t *testing.T) {
	s := world.NewScenery("fir", "fir.fnm", mgl32.Vec3{0, 0, 0})
	s.DrawDistance, s.Impostor, s.ImpostorDistance = 1000, "x", 200
	s.ImpostorMesh, s.NaniteGPU = &nanite.Mesh{}, true
	for _, imp := range []bool{false, true} {
		for d := float32(0); d < 1100; d += 0.37 {
			eye := mgl32.Vec3{d, 0, 0}
			dr, _ := placementDraw(&s, imp, eye)
			for _, mode := range []int32{drawDither, drawHashed} {
				if gpuKeeps(dr, d, mode) && !placementMayDraw(&s, imp, eye) {
					t.Fatalf("impostor %v at %g m: the GPU keeps it (mode %d), the CPU's look ahead does not", imp, d, mode)
				}
			}
		}
	}
}

func TestVSMTrackerRedrawsACasterCrossingItsShadowDistance(t *testing.T) {
	prev := DrawDistances
	t.Cleanup(func() { DrawDistances = prev })
	DrawDistances = true
	m := &nanite.Mesh{Lo: mgl32.Vec3{-1, 0, -1}, Hi: mgl32.Vec3{1, 2, 1}}
	s := world.NewScenery("rock", "rock.fnm", mgl32.Vec3{0, 0, 0})
	s.Nanite, s.NaniteGPU = m, true
	s.LocalMin, s.LocalMax = m.Lo, m.Hi
	s.DrawDistance, s.ShadowDistance = 400, 100
	lvl := &world.Level{Scenery: []world.Scenery{s}}
	lo, hi, _ := lvl.Scenery[0].WorldAABBFrom(sceneryModel(lvl, &lvl.Scenery[0]))
	box := vsmBox{lo, hi, true}
	var tr vsmTracker
	near, far := mgl32.Vec3{50, 0, 0}, mgl32.Vec3{150, 0, 0}
	tr.collect(lvl, near, 100)
	if _, boxes := tr.collect(lvl, near, 100); boxIn(boxes, box) {
		t.Fatal("a still caster inside its shadow distance was handed in")
	}
	if _, boxes := tr.collect(lvl, far, 100); !boxIn(boxes, box) {
		t.Fatal("the camera moved past the caster's shadow distance and its pages were not redrawn: its shadow stays")
	}
	if _, boxes := tr.collect(lvl, far, 100); boxIn(boxes, box) {
		t.Error("handed in again with nothing changed")
	}
	if _, boxes := tr.collect(lvl, near, 100); !boxIn(boxes, box) {
		t.Error("back inside its shadow distance and its pages were not redrawn: its shadow does not come back")
	}
}
