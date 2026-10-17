package render

// water_w5_gl_test.go — phase W5's interaction through DrawWorld
// (docs/terrain-paint/PHASE_W5.md): a ring pushed by the simulation shows
// on the surface where it was pushed, rain rings the whole surface, and a
// camera below the surface draws the underwater view — the water's murk as
// a fog volume, caustics from below at the high tier, the world's sound
// muffled — and hands it all back once the camera is out again.
//
//	FOUNDRY_GL_TEST=1 go test -run 'WaterRipples|WaterUnderwater' ./internal/render/
//	FOUNDRY_TEST_DUMP=/tmp/dump ... writes the frames as PNGs

import (
	"testing"

	"foundry/internal/audio"
	"foundry/internal/gfx"
	"foundry/internal/material"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// w5Water registers a still, mirror-calm water material for the test.
func w5Water(t *testing.T) string {
	t.Helper()
	material.LoadAll()
	d := &material.Doc{Name: "w5_water", Domain: material.DomainSurface}
	e := d.AddEffect(material.KindWater)
	e.Set("detail_strength", material.FloatValue(0))
	e.Set("wave_amplitude", material.FloatValue(0))
	e.Set("roughness", material.FloatValue(0.02))
	e.Set("ssr", material.FloatValue(0))
	e.Set("caustics", material.FloatValue(2))
	material.Put(d)
	t.Cleanup(func() { material.Clear(); material.LoadAll() })
	return d.Name
}

func TestWaterRipplesRingTheSurface(t *testing.T) {
	w := newVisWorld(t)
	mat := w5Water(t)
	gfx.Cam.Position = mgl32.Vec3{0, 5, 3}
	gfx.Cam.Direction = mgl32.Vec3{0, -1, -0.6}.Normalize()
	gfx.UpdateCamMatrix()
	floor := world.NewBoxBrush("floor", mgl32.Vec3{0, -2.5, 0}, mgl32.Vec3{8, 0.5, 8}, "concrete")
	lvl := w4Level(t, floor, w4Water("pool", mgl32.Vec3{0, -1, 0}, mgl32.Vec3{8, 1, 8}, mat))
	world.InvalidateWaterQuery()
	world.DrainWaterRipples(nil)
	size := int(w.size)
	skip := func(p string) bool { return p == "gi" }
	frame := func(now float64) []uint8 {
		w.frameIntoWith(lvl, WorldDraw{Now: now, Skip: skip})
		return w.frameIntoWith(lvl, WorldDraw{Now: now, Skip: skip})
	}
	calm := frame(2)
	// A body dropping in at the middle of the view, 0.7 s before.
	world.PushWaterRipple(world.WaterRipple{At: mgl32.Vec3{0, 0, 0}, Strength: 1, Reach: 3})
	frame(1.3)
	ringed := frame(2)
	dumpPNG("w5_calm", calm, size)
	dumpPNG("w5_ringed", ringed, size)
	changed := 0
	for y := size / 4; y < size*3/4; y += 4 {
		for x := size / 4; x < size*3/4; x += 4 {
			if pixelDiffAt(calm, ringed, size, x, y) > 6 {
				changed++
			}
		}
	}
	if changed < 10 {
		t.Errorf("a ring changed %d samples of the surface, want it seen", changed)
	}
	// Long after, gone.
	if late := frame(9); pixelDiffAt(calm, late, size, size/2, size/2) > 3 {
		t.Errorf("the ring was still there 7 s on")
	}

	// Rain rings the calm surface everywhere.
	lvl.RainRipples = 1
	rain := frame(2)
	lvl.RainRipples = 0
	dumpPNG("w5_rain", rain, size)
	rained := 0
	for y := size / 4; y < size*3/4; y += 4 {
		for x := size / 8; x < size*7/8; x += 4 {
			if pixelDiffAt(calm, rain, size, x, y) > 4 {
				rained++
			}
		}
	}
	if rained < 20 {
		t.Errorf("rain changed %d samples of the surface, want it all over", rained)
	}
}

func TestWaterUnderwaterView(t *testing.T) {
	w := newVisWorld(t)
	mat := w5Water(t)
	prevQ := gfx.WaterQuality
	t.Cleanup(func() { gfx.WaterQuality = prevQ; audio.SetMuffle(0) })
	gfx.WaterQuality = gfx.WaterHigh
	floor := world.NewBoxBrush("floor", mgl32.Vec3{0, -4.5, 0}, mgl32.Vec3{12, 0.5, 12}, "concrete")
	post := world.NewBoxBrush("post", mgl32.Vec3{0, -2, -6}, mgl32.Vec3{0.5, 3, 0.5}, "concrete")
	lvl := w4Level(t, floor, post, w4Water("pool", mgl32.Vec3{0, -2, 0}, mgl32.Vec3{12, 2, 12}, mat))
	world.InvalidateWaterQuery()
	size := int(w.size)
	skip := func(p string) bool { return p == "gi" }
	frame := func() []uint8 {
		w.frameIntoWith(lvl, WorldDraw{Now: 1, Skip: skip})
		return w.frameIntoWith(lvl, WorldDraw{Now: 1, Skip: skip})
	}

	// Above, looking across at the post: dry.
	gfx.Cam.Position = mgl32.Vec3{0, 1.5, 2}
	gfx.Cam.Direction = mgl32.Vec3{0, -0.3, -1}.Normalize()
	gfx.UpdateCamMatrix()
	above := frame()
	if WaterUnderwater() || audio.Muffle() != 0 || waterBodies.count != 0 {
		t.Fatalf("above water: under %v muffle %v caustic bodies %d", WaterUnderwater(), audio.Muffle(), waterBodies.count)
	}
	if n := len(frameVolumes(lvl)); n != 0 {
		t.Fatalf("above water: %d frame fog volumes", n)
	}

	// Under, at the same spot 2.5 m down.
	gfx.Cam.Position = mgl32.Vec3{0, -2.5, 2}
	gfx.UpdateCamMatrix()
	under := frame()
	if !WaterUnderwater() || audio.Muffle() != 1 {
		t.Fatalf("under water: under %v muffle %v", WaterUnderwater(), audio.Muffle())
	}
	if waterBodies.count != 1 {
		t.Errorf("under water at the high tier: %d caustic bodies, want the pool", waterBodies.count)
	}
	vols := frameVolumes(lvl)
	if len(vols) != 1 || !vols[0].HasFog() || vols[0].Position.Y() >= 0 {
		t.Fatalf("under water: frame fog volumes %+v", vols)
	}
	// Looking up: the surface from below.
	gfx.Cam.Direction = mgl32.Vec3{0, 1, -0.4}.Normalize()
	gfx.UpdateCamMatrix()
	up := frame()
	dumpPNG("w5_above", above, size)
	dumpPNG("w5_under", under, size)
	dumpPNG("w5_under_up", up, size)
	// The top of the frame: the sky above, the surface's underside below.
	if d := pixelDiffAt(above, under, size, size/2, size/8); d < 10 {
		t.Errorf("the view under water looks like the view above it (%d)", d)
	}
	// The post, 8 m off through clear water, is still made out but tinted:
	// the frame is not one flat colour.
	lo, hi := 255, 0
	for x := 0; x < size; x += 8 {
		v := int(under[(size/2*size+x)*4+1])
		lo, hi = min(lo, v), max(hi, v)
	}
	if hi-lo < 4 {
		t.Errorf("under water the middle row is flat (%d..%d): the murk swallowed everything", lo, hi)
	}

	// Out again (with the hysteresis band crossed): everything handed back.
	gfx.Cam.Position = mgl32.Vec3{0, 1.5, 2}
	gfx.Cam.Direction = mgl32.Vec3{0, -0.3, -1}.Normalize()
	gfx.UpdateCamMatrix()
	again := frame()
	if WaterUnderwater() || audio.Muffle() != 0 || waterBodies.count != 0 {
		t.Fatalf("back above: under %v muffle %v bodies %d", WaterUnderwater(), audio.Muffle(), waterBodies.count)
	}
	if d := pixelDiffAt(above, again, size, size/2, size/2); d > 3 {
		t.Errorf("back above the frame differs from before by %d", d)
	}
}
