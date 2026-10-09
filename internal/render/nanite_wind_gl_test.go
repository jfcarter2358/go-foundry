package render

// nanite_wind_gl_test.go — cooked placements bending in the wind
// (world.Scenery.Wind, resources/shaders/wind_common.glsl), through DrawWorld
// on the visibility buffer's parity scene:
//
//   - In still air, or without wind of their own, the placements are where
//     they were whatever the clock says.
//   - In wind they move as the clock runs.
//   - Bent, the visibility buffer's picture is still the forward path's: the
//     ID pass and the material pass bend each corner as vertex.glsl does.
//
//	FOUNDRY_GL_TEST=1 go test -timeout 300s -run 'Wind' ./internal/render/

import (
	"testing"

	"foundry/internal/world"
)

// windFrame draws lvl twice at clock now (the occlusion history's frame,
// then the one read) and returns the second.
func (w *visWorld) windFrame(lvl *world.Level, now float64) []uint8 {
	o := WorldDraw{Now: now, Skip: func(p string) bool { return p == "gi" }}
	w.frameIntoWith(lvl, o)
	return w.frameIntoWith(lvl, o)
}

func setSceneryWind(lvl *world.Level, wind world.SceneryWind) {
	for i := range lvl.Scenery {
		if lvl.Scenery[i].NaniteGPU {
			lvl.Scenery[i].Wind = wind
		}
	}
}

func TestWindBendsCookedPlacements(t *testing.T) {
	w := newVisWorld(t)
	lvl := visTestLevel(t, visTestRock(t))
	prev := foliageWind
	t.Cleanup(func() { foliageWind = prev })
	total := int(w.size * w.size)
	// Exaggerated, so a 0.4-scale rock's sway is many pixels.
	strong := world.SceneryWind{Bend: 0.3, Flutter: 0.2}

	moved := func(label string) int {
		a := w.windFrame(lvl, 1)
		b := w.windFrame(lvl, 1.7)
		over8, _, worst := pixelDiff(a, b)
		t.Logf("%s: %d pixels moved past 8 levels between clocks 1 and 1.7 (worst %d)", label, over8, worst)
		return over8
	}

	foliageWind.strength = 1
	setSceneryWind(lvl, world.SceneryWind{})
	if n := moved("no wind of their own"); n != 0 {
		t.Errorf("placements without wind moved: %d pixels", n)
	}

	setSceneryWind(lvl, strong)
	foliageWind.strength = 0
	if n := moved("still air"); n != 0 {
		t.Errorf("placements moved in still air: %d pixels", n)
	}

	foliageWind.strength = 1
	if n := moved("wind"); n < total/200 {
		t.Errorf("placements in wind moved only %d pixels (want at least %d)", n, total/200)
	}

	// Bent, the visibility buffer still draws the forward path's picture.
	shots := map[bool][]uint8{}
	for _, vis := range []bool{false, true} {
		NaniteVisBuffer = vis
		shots[vis] = w.windFrame(lvl, 1.7)
	}
	over8, over32, worst := pixelDiff(shots[false], shots[true])
	t.Logf("bent: forward against visibility buffer, %d pixels past 8 levels, %d past 32, worst %d", over8, over32, worst)
	if over8 > total/500 || over32 > 0 {
		t.Errorf("bent placements: %d pixels differ by more than 8 levels (allowed %d), %d by more than 32 (worst %d)",
			over8, total/500, over32, worst)
	}
}
