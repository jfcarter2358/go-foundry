package render

// water_w4_gl_test.go — phase W4's water shading through DrawWorld
// (docs/terrain-paint/PHASE_W4.md): the caustics are the water's, so ground
// not under a water surface takes none however far below the water level it
// lies, while the bed under the surface does; and a sheet wearing
// water_falls shades as falling water — white, its streaks moving.
//
//	FOUNDRY_GL_TEST=1 go test -run 'WaterCaustics|WaterFalls' ./internal/render/
//	FOUNDRY_TEST_DUMP=/tmp/dump ... writes the frames as PNGs

import (
	"fmt"
	"testing"
	"time"

	"github.com/go-gl/gl/v4.3-core/gl"

	"foundry/internal/gfx"
	"foundry/internal/material"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// w4Level is one chunk of brushes under waterTestLevel's sky and sun.
func w4Level(t *testing.T, brushes ...world.Brush) *world.Level {
	t.Helper()
	ch := world.Chunk{ID: "w4", Scale: 1, Brushes: brushes}
	for bi := range ch.Brushes {
		if err := ch.Brushes[bi].Load(); err != nil {
			t.Fatal(err)
		}
	}
	ch.RecomputeBounds()
	lvl := &world.Level{Chunks: []world.Chunk{ch}}
	sky := world.DefaultSky()
	lvl.Sky = &sky
	lvl.Lights.Global = world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.35,
		Wrap: world.DefaultLightWrap, Hemisphere: world.DefaultAmbientHemisphere}
	sun := world.DirectionalLight{Color: mgl32.Vec3{1, 0.95, 0.9}, Brightness: 1.2, Yaw: 200, Pitch: -60,
		CastShadows: true, ShadowStrength: 0.8, Dynamic: true}
	sun.SyncDirection()
	lvl.Lights.Directional = []world.DirectionalLight{sun}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	world.UploadLights(lvl.Lights)
	t.Cleanup(func() {
		ReleaseChunkBatches(lvl)
		world.UploadLights(world.LightSet{})
	})
	return lvl
}

// w4Water is a water box whose top face alone wears mat.
func w4Water(id string, center, half mgl32.Vec3, mat string) world.Brush {
	w := world.NewBoxBrush(id, center, half, "water")
	w.Solid = false
	for fi := range w.Faces {
		f := &w.Faces[fi]
		if w.PlacedFaceNormal(f).Y() > 0.9 {
			f.Material = mat
		} else {
			f.NoDraw = true
		}
	}
	return w
}

// pixelDiffAt is the largest channel difference between two frames at (x, y).
func pixelDiffAt(a, b []uint8, size, x, y int) int {
	i := (y*size + x) * 4
	m := 0
	for c := 0; c < 3; c++ {
		d := int(a[i+c]) - int(b[i+c])
		if d < 0 {
			d = -d
		}
		m = max(m, d)
	}
	return m
}

// The caustics are drawn by the water onto the bed it shows: a floor 1.2 m
// under the water's level beside the pool (no water over it: a dry hollow
// below the level, which the old box test lit) is pixel for pixel the
// dry level's at the high tier, at two moments; the same floor under the
// water changes when the caustics are on (high) against off (medium).
func TestWaterCausticsOnlyUnderTheSurface(t *testing.T) {
	w := newVisWorld(t)
	material.LoadAll()
	d := &material.Doc{Name: "w4_caustic_water", Domain: material.DomainSurface}
	e := d.AddEffect(material.KindWater)
	e.Set("ssr", material.FloatValue(0))
	e.Set("caustics", material.FloatValue(2.5))
	e.Set("absorption", material.FloatValue(0.15))
	material.Put(d)
	t.Cleanup(func() { material.Clear(); material.LoadAll() })
	prevQ := gfx.WaterQuality
	t.Cleanup(func() { gfx.WaterQuality = prevQ })
	gfx.Cam.Position = mgl32.Vec3{0, 4, 4}
	gfx.Cam.Direction = mgl32.Vec3{0, -1, -0.9}.Normalize()
	gfx.UpdateCamMatrix()

	floor := world.NewBoxBrush("floor", mgl32.Vec3{0, -1.7, 0}, mgl32.Vec3{5, 0.5, 5}, "concrete")
	dry := w4Level(t, floor)
	wet := w4Level(t, floor, w4Water("pool", mgl32.Vec3{2.5, -0.3, 0}, mgl32.Vec3{2.5, 0.3, 5}, "w4_caustic_water"))
	size := int(w.size)
	// Left of centre: the dry floor; right: the floor under the water.
	lx, rx, y := size/5, size*4/5, size/2
	at := func(lvl *world.Level, now float64) []uint8 {
		w.frameIntoWith(lvl, WorldDraw{Now: now, Skip: func(p string) bool { return p == "gi" }})
		return w.frameIntoWith(lvl, WorldDraw{Now: now, Skip: func(p string) bool { return p == "gi" }})
	}
	gfx.WaterQuality = gfx.WaterHigh
	bare := at(dry, 1)
	h1 := at(wet, 1)
	h2 := at(wet, 1.9)
	dumpPNG("w4_caustics_dry", bare, size)
	dumpPNG("w4_caustics_high", h1, size)
	if WaterDrawStats.Flags&waterHaveCaustics == 0 {
		t.Fatalf("high tier frame flags %b: no caustics", WaterDrawStats.Flags)
	}
	if d1, d2 := pixelDiffAt(bare, h1, size, lx, y), pixelDiffAt(bare, h2, size, lx, y); d1 > 2 || d2 > 2 {
		t.Errorf("the dry floor beside the pool changed with the water: %d / %d", d1, d2)
	}
	gfx.WaterQuality = gfx.WaterMedium
	m1 := at(wet, 1)
	dumpPNG("w4_caustics_medium", m1, size)
	// Over a patch of the wet floor, the caustics move the picture.
	moved := 0
	for dy := -6; dy <= 6; dy += 3 {
		for dx := -6; dx <= 6; dx += 3 {
			if pixelDiffAt(h1, m1, size, rx+dx, y+dy) > 6 {
				moved++
			}
		}
	}
	if moved < 3 {
		t.Errorf("the bed under the water took no caustics (%d of 25 pixels changed)", moved)
	}
}

// A sheet wearing water_falls is falling water: brighter than the dark wall
// it hangs in front of (white water), and its streaks move.
func TestWaterFallsSheetShadesAsFallingWater(t *testing.T) {
	w := newVisWorld(t)
	material.LoadAll()
	if !material.Exists("water_falls") {
		t.Fatal("resources/materials/water_falls.fmat did not load")
	}
	prevQ := gfx.WaterQuality
	t.Cleanup(func() { gfx.WaterQuality = prevQ })
	gfx.WaterQuality = gfx.WaterHigh
	gfx.Cam.Position = mgl32.Vec3{0, 1.5, 6}
	gfx.Cam.Direction = mgl32.Vec3{0, 0, -1}
	gfx.UpdateCamMatrix()

	wall := world.NewBoxBrush("wall", mgl32.Vec3{0, 1.5, -1}, mgl32.Vec3{4, 3, 0.5}, "brick")
	floor := world.NewBoxBrush("floor", mgl32.Vec3{0, -0.5, 1}, mgl32.Vec3{4, 0.5, 4}, "concrete")
	sheet := world.NewBoxBrush("sheet", mgl32.Vec3{0, 1.5, 0}, mgl32.Vec3{1, 1.5, 0.02}, "water")
	sheet.Solid = false
	for fi := range sheet.Faces {
		f := &sheet.Faces[fi]
		if sheet.PlacedFaceNormal(f).Z() > 0.9 {
			f.Material = "water_falls"
		} else {
			f.NoDraw = true
		}
	}
	bare := w4Level(t, wall, floor)
	lvl := w4Level(t, wall, floor, sheet)
	size := int(w.size)
	cx, cy := size/2, size/2
	frame := func(l *world.Level, now float64) []uint8 {
		w.frameIntoWith(l, WorldDraw{Now: now, Skip: func(p string) bool { return p == "gi" }})
		return w.frameIntoWith(l, WorldDraw{Now: now, Skip: func(p string) bool { return p == "gi" }})
	}
	b := frame(bare, 1)
	a1 := frame(lvl, 1)
	a2 := frame(lvl, 1.23)
	dumpPNG("w4_falls_bare", b, size)
	dumpPNG("w4_falls", a1, size)
	if WaterDrawStats.Surfaces != 1 {
		t.Fatalf("the water pass drew %d surfaces", WaterDrawStats.Surfaces)
	}
	// Mean over a patch of the sheet: whiter than the wall behind it.
	var ls, lb float32
	n := 0
	for dy := -10; dy <= 10; dy += 2 {
		for dx := -10; dx <= 10; dx += 2 {
			ls += lum(waterPixel(a1, size, cx+dx, cy+dy))
			lb += lum(waterPixel(b, size, cx+dx, cy+dy))
			n++
		}
	}
	ls, lb = ls/float32(n), lb/float32(n)
	t.Logf("sheet %.3f, wall behind %.3f", ls, lb)
	if ls < lb+0.15 {
		t.Errorf("the falling sheet (%.3f) is not white water over the wall (%.3f)", ls, lb)
	}
	moved := 0
	for dy := -10; dy <= 10; dy += 2 {
		for dx := -10; dx <= 10; dx += 2 {
			if pixelDiffAt(a1, a2, size, cx+dx, cy+dy) > 4 {
				moved++
			}
		}
	}
	if moved < 10 {
		t.Errorf("the streaks did not move: %d of %d pixels changed", moved, n)
	}
}

// The screen-space reflection's share of the high tier, at about 1080p
// (1440²): the pool under a material with ssr 1 and the same with ssr 0, each
// frame finished on the GPU and timed on the wall clock (WSL's GPU
// timestamps read zero). Logged, not asserted (phase W4's SSR numbers).
//
//	FOUNDRY_GL_TEST=1 go test -run TestWaterSSRCost -v ./internal/render/
func TestWaterSSRCost(t *testing.T) {
	w := newVisWorld(t)
	material.LoadAll()
	for _, v := range []float32{0, 1} {
		d := &material.Doc{Name: fmt.Sprintf("w4_ssr_%g", v), Domain: material.DomainSurface}
		d.AddEffect(material.KindWater).Set("ssr", material.FloatValue(v))
		material.Put(d)
	}
	t.Cleanup(func() { material.Clear(); material.LoadAll() })
	const size = 1440
	fbo := w.target(size)
	gfx.Cam.Position = mgl32.Vec3{0, 3, 5}
	gfx.Cam.Direction = mgl32.Vec3{0, -0.35, -1}.Normalize()
	gfx.UpdateCamMatrix()
	prevQ := gfx.WaterQuality
	t.Cleanup(func() { gfx.WaterQuality = prevQ })
	gfx.WaterQuality = gfx.WaterHigh
	timeIt := func(lvl *world.Level) float64 {
		frame := func() {
			DrawWorld(lvl, WorldDraw{Now: 1, Skip: func(p string) bool { return p == "gi" },
				Target: &FrameTarget{FBO: fbo, Viewport: [4]int32{0, 0, size, size}}})
			gl.Finish()
		}
		for i := 0; i < 8; i++ {
			frame()
		}
		const n = 40
		start := time.Now()
		for i := 0; i < n; i++ {
			frame()
		}
		return float64(time.Since(start).Microseconds()) / 1000 / n
	}
	off := timeIt(waterTestLevel(t, "w4_ssr_0"))
	on := timeIt(waterTestLevel(t, "w4_ssr_1"))
	off2 := timeIt(waterTestLevel(t, "w4_ssr_0"))
	t.Logf("high tier, %d²: frame %.2f ms with SSR, %.2f / %.2f ms without: SSR %.2f ms", size, on, off, off2, on-(off+off2)/2)
}
