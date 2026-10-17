package render

// water_gl_test.go — the water pass (water_pass.go, water.glsl) through
// DrawWorld: a pool with a shallow ledge and a deep end under one water
// face, drawn at each quality tier.
//
//	FOUNDRY_GL_TEST=1 go test -run Water ./internal/render/
//	FOUNDRY_TEST_DUMP=/tmp/dump ... writes the frames as PNGs

import (
	"testing"
	"time"

	"github.com/go-gl/gl/v4.3-core/gl"

	"foundry/internal/gfx"
	"foundry/internal/material"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// waterTestLevel is the pool: a ledge 0.3 m under the surface on the left, a
// floor 5 m under it on the right, and a water face at y = 0 over both, the
// rest of the water brush unseen. A gradient sky and a shadowing sun.
func waterTestLevel(t *testing.T, waterMat string) *world.Level {
	t.Helper()
	ch := world.Chunk{ID: "pool", Scale: 1}
	ch.Brushes = append(ch.Brushes,
		world.NewBoxBrush("ledge", mgl32.Vec3{-1.5, -1.3, 0}, mgl32.Vec3{1.5, 1, 3}, "concrete"),
		world.NewBoxBrush("deep", mgl32.Vec3{1.5, -6, 0}, mgl32.Vec3{1.5, 1, 3}, "concrete"))
	if waterMat != "" {
		w := world.NewBoxBrush("water", mgl32.Vec3{0, -0.25, 0}, mgl32.Vec3{3, 0.25, 3}, "water")
		w.Solid = false
		for fi := range w.Faces {
			f := &w.Faces[fi]
			if w.PlacedFaceNormal(f).Y() > 0.9 {
				f.Material = waterMat
			} else {
				f.NoDraw = true
			}
		}
		ch.Brushes = append(ch.Brushes, w)
	}
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
	sun := world.DirectionalLight{Color: mgl32.Vec3{1, 0.95, 0.9}, Brightness: 1, Yaw: 200, Pitch: -45,
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

// waterPixel is the colour at (x, y) of a frame size across, 0..1.
func waterPixel(px []uint8, size, x, y int) mgl32.Vec3 {
	i := (y*size + x) * 4
	return mgl32.Vec3{float32(px[i]) / 255, float32(px[i+1]) / 255, float32(px[i+2]) / 255}
}

func lum(c mgl32.Vec3) float32 { return 0.2126*c.X() + 0.7152*c.Y() + 0.0722*c.Z() }

// Water draws in its own pass at every tier, with the copies the tier asks
// for; the shallow ledge shows through brighter than the deep end, the deep
// end leans to the water's own blue-green, and the water changes the picture.
func TestWaterPassDrawsDepthColourAtEveryTier(t *testing.T) {
	w := newVisWorld(t)
	if n := material.LoadAll(); n == 0 {
		t.Fatal("no materials loaded")
	}
	if !material.Exists("water") {
		t.Fatal("resources/materials/water.fmat did not load")
	}
	prevQ := gfx.WaterQuality
	t.Cleanup(func() { gfx.WaterQuality = prevQ })
	gfx.Cam.Position = mgl32.Vec3{0, 3, 5}
	gfx.Cam.Direction = mgl32.Vec3{0, -0.55, -1}.Normalize()
	gfx.UpdateCamMatrix()

	dry := waterTestLevel(t, "")
	w.frame(dry)
	bare := w.frame(dry)
	dumpPNG("water_dry", bare, int(w.size))

	lvl := waterTestLevel(t, "water")
	size := int(w.size)
	// Left of centre is over the ledge, right over the deep end.
	sx, dx, y := size*3/10, size*7/10, size*2/5
	want := map[gfx.WaterQualityLevel]int32{
		gfx.WaterLow:    waterHaveDepth | waterHaveSky,
		gfx.WaterMedium: waterHaveDepth | waterHaveColour | waterHaveSky,
		gfx.WaterHigh:   waterHaveDepth | waterHaveColour | waterHaveSky | waterHaveSSR | waterHaveCaustics,
	}
	for _, q := range []gfx.WaterQualityLevel{gfx.WaterLow, gfx.WaterMedium, gfx.WaterHigh} {
		gfx.WaterQuality = q
		w.frame(lvl)
		px := w.frame(lvl)
		dumpPNG("water_"+q.String(), px, size)
		if WaterDrawStats.Surfaces != 1 {
			t.Fatalf("%s: the water pass drew %d surfaces, want 1", q, WaterDrawStats.Surfaces)
		}
		if WaterDrawStats.Flags != want[q] {
			t.Errorf("%s: frame flags %b, want %b", q, WaterDrawStats.Flags, want[q])
		}
		shallow, deep := waterPixel(px, size, sx, y), waterPixel(px, size, dx, y)
		bs, bd := waterPixel(bare, size, sx, y), waterPixel(bare, size, dx, y)
		t.Logf("%s: shallow %v (dry %v), deep %v (dry %v)", q, shallow, bs, deep, bd)
		if shallow == bs || deep == bd {
			t.Errorf("%s: the water did not change the picture", q)
		}
		if lum(shallow) <= lum(deep) {
			t.Errorf("%s: the shallow ledge (%v) is not brighter than the deep end (%v)", q, shallow, deep)
		}
		if deep.Z() <= deep.X() || deep.Y() <= deep.X() {
			t.Errorf("%s: the deep end %v does not lean to the water's blue-green", q, deep)
		}
		for i := 0; i+3 < len(px); i += 4 {
			if px[i] == 0 && px[i+1] == 255 && px[i+2] == 0 {
				t.Fatalf("%s: the probe colour came through", q)
			}
		}
	}
	// The swamp preset hides even the ledge 30 cm down: its water is nearly
	// opaque, where the default's ledge reads as the concrete through it.
	clearWater := waterPixel(w.frame(lvl), size, sx, y)
	swamp := waterTestLevel(t, "water_swamp")
	w.frame(swamp)
	spx := w.frame(swamp)
	dumpPNG("water_swamp", spx, size)
	murky := waterPixel(spx, size, sx, y)
	t.Logf("ledge: default water %v, swamp %v; swamp deep end %v", clearWater, murky, waterPixel(spx, size, dx, y))
	if lum(murky) >= lum(clearWater)*0.7 {
		t.Errorf("the swamp's ledge %v is not much darker than the default's %v", murky, clearWater)
	}
}

// The water pass's cost at about 1080p (1440², the same pixel count), the
// pool filling the lower part of the view: the same frame with the pass and
// with it skipped, each finished on the GPU and timed on the wall clock (the
// profiler's GPU timestamps read zero on WSL's d3d12 GL). Logged, not
// asserted: the number is the machine's.
//
//	FOUNDRY_GL_TEST=1 go test -run TestWaterPassCost -v ./internal/render/
func TestWaterPassCost(t *testing.T) {
	w := newVisWorld(t)
	material.LoadAll()
	const size = 1440
	fbo := w.target(size)
	gfx.Cam.Position = mgl32.Vec3{0, 3, 5}
	gfx.Cam.Direction = mgl32.Vec3{0, -0.55, -1}.Normalize()
	gfx.UpdateCamMatrix()
	lvl := waterTestLevel(t, "water")
	prevQ := gfx.WaterQuality
	t.Cleanup(func() { gfx.WaterQuality = prevQ })
	frame := func(skipWater bool) {
		DrawWorld(lvl, WorldDraw{Now: 1, Skip: func(p string) bool { return p == "gi" || (skipWater && p == "water") },
			Target: &FrameTarget{FBO: fbo, Viewport: [4]int32{0, 0, size, size}}})
		gl.Finish()
	}
	timeIt := func(skipWater bool) float64 {
		for i := 0; i < 8; i++ {
			frame(skipWater)
		}
		const n = 40
		start := time.Now()
		for i := 0; i < n; i++ {
			frame(skipWater)
		}
		return float64(time.Since(start).Microseconds()) / 1000 / n
	}
	base := timeIt(true)
	for _, q := range []gfx.WaterQualityLevel{gfx.WaterLow, gfx.WaterMedium, gfx.WaterHigh} {
		gfx.WaterQuality = q
		with := timeIt(false)
		t.Logf("%s: frame %.2f ms with water, %.2f ms without: the pass %.2f ms (%d² target)", q, with, base, with-base, size)
	}
}
