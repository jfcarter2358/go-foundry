package render

// gi_timing_gl_test.go — how long GI's GPU work takes, for before/after
// numbers on a change to its shaders.
//
// A Go benchmark cannot stand up a GL context (gltest.Context wants a
// *testing.T), so these are tests that time a fixed number of updates with
// the GPU drained either side, and log the mean. They measure nothing unless
// asked: FOUNDRY_GL_TEST=1 for the context, FOUNDRY_GL_TIMING=1 to run them.
//
//	FOUNDRY_GL_TEST=1 FOUNDRY_GL_TIMING=1 go test -run GITiming -v ./internal/render/

import (
	"math"
	"os"
	"testing"
	"time"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

func skipUnlessTiming(t *testing.T) {
	t.Helper()
	if os.Getenv("FOUNDRY_GL_TIMING") == "" {
		t.Skip("FOUNDRY_GL_TIMING=1 to time GI's passes")
	}
}

// giLitRoom is giRoom lit the way level1's hangar is: a shadowing sun the
// walls keep out and four shadowing lamps under the ceiling. open takes the
// ceiling off, so the sun comes in and most rays find the sky.
func giLitRoom(open bool) *world.Level {
	lights := world.LightSet{
		Global: world.GlobalLight{Color: mgl32.Vec3{0.3, 0.3, 0.35}, Brightness: 1, Hemisphere: 0.5},
		Directional: []world.DirectionalLight{{
			Color: mgl32.Vec3{1, 0.95, 0.9}, Brightness: 1.2, CastShadows: true,
			Yaw: 30, Pitch: -55,
		}},
	}
	lights.Directional[0].SyncDirection()
	for i, p := range []mgl32.Vec3{{2.5, 3.2, 2.5}, {7.5, 3.2, 2.5}, {2.5, 3.2, 7.5}, {7.5, 3.2, 7.5}} {
		c := mgl32.Vec3{1, 0.8, 0.6}
		if i%2 == 1 {
			c = mgl32.Vec3{0.6, 0.8, 1}
		}
		lights.Points = append(lights.Points, world.PointLight{
			Position: p, Color: c, Brightness: 1.2, Radius: 12, CastShadows: true,
		})
	}
	lvl := giRoom(lights, nil)
	if open {
		for i, b := range lvl.Chunks[0].Brushes {
			if b.ID == "room_ceiling" {
				lvl.Chunks[0].Brushes = append(lvl.Chunks[0].Brushes[:i], lvl.Chunks[0].Brushes[i+1:]...)
				break
			}
		}
	}
	return lvl
}

// timeGIProbes is the milliseconds of one frame's probe update (the
// placement and the trace of every cascade), after warm frames: the best of
// five batches' means, since the GPU may be shared with whatever else is
// running and that only ever makes a batch slower.
func timeGIProbes(t *testing.T, lvl *world.Level, frames int) float64 {
	t.Helper()
	giRunFrames(t, lvl, mgl32.Vec3{5, 2, 5}, 30)
	g := &gi
	best := math.Inf(1)
	for batch := 0; batch < 5; batch++ {
		gl.Finish()
		start := time.Now()
		for i := 0; i < frames; i++ {
			g.placeCascades()
			g.updateProbes(lvl, g.t.hysteresis, false)
			g.frame++
		}
		gl.Finish()
		best = min(best, float64(time.Since(start).Microseconds())/1000/float64(frames))
	}
	return best
}

func TestGITimingProbeUpdate(t *testing.T) {
	skipUnlessTiming(t)
	for _, q := range []gfx.GIQualityLevel{gfx.GIMedium, gfx.GIHigh} {
		for _, open := range []bool{false, true} {
			giTestSetup(t, q, nil)
			ms := timeGIProbes(t, giLitRoom(open), 40)
			t.Logf("GI %v, open %v: %.3f ms a probe update", q, open, ms)
			gi.release()
		}
	}
}

// TestGITimingFrame times whole frames through DrawWorld at 768 square: the
// virtual shadow map's test field (sunlit ground and posts, every pixel
// reading the virtual map) with live GI on, so the material pass samples the
// probes at every pixel. The frame's GPU work, best of five batches' means.
func TestGITimingFrame(t *testing.T) {
	skipUnlessTiming(t)
	w := newVSMWorld(t, true)
	lvl := vsmTestLevel(t)
	lvl.Lights.Global.GI = world.DefaultGISettings()
	vsmLook(mgl32.Vec3{0, 1.7, 6}, mgl32.Vec3{0.1, -0.25, -1})
	prevQ := gfx.GIQuality
	gfx.GIQuality = gfx.GIMedium
	gi.failed = false
	t.Cleanup(func() { gfx.GIQuality = prevQ; gi.release() })
	const size = 768
	fbo := w.target(size)
	draw := func() {
		DrawWorld(lvl, WorldDraw{Now: 1, Target: &FrameTarget{FBO: fbo, Viewport: [4]int32{0, 0, size, size}}})
	}
	for i := 0; i < 20; i++ {
		draw()
	}
	if !gi.active {
		t.Fatal("GI did not run")
	}
	best := math.Inf(1)
	for batch := 0; batch < 5; batch++ {
		gl.Finish()
		start := time.Now()
		for i := 0; i < 20; i++ {
			draw()
		}
		gl.Finish()
		best = min(best, float64(time.Since(start).Microseconds())/1000/20)
	}
	t.Logf("frame at %d square, GI medium, virtual shadow map: %.3f ms a frame", size, best)
}
