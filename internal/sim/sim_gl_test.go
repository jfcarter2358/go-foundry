package sim

// sim_gl_test.go — a frame of the world makes no GL call.
//
// Step is meant to run with no GL context at all: headlessly today, on a
// thread of its own later. It used to make three kinds of call — the lights'
// uniforms on a waypoint, an emitter or an effect, the view matrix whenever
// the camera was placed, and a texture upload when something asked for an
// unloaded texture — each guarded by "no program, nothing to do", which kept a
// headless run alive without making the step GL-free. gfx's tripwire catches
// any of its GL entry points being reached, and skips the GL, so this runs
// without a context and fails with a list rather than a segfault.

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func TestSimStepMakesNoGLCalls(t *testing.T) {
	lvl := &world.Level{
		Entities: []world.Entity{{ID: "crate", Position: mgl32.Vec3{3, 0, 0}, Scale: mgl32.Vec3{1, 1, 1}, Visible: true}},
	}
	// A point light and a sun, so a frame that still uploaded lights on a
	// change would have something to upload.
	lvl.Lights.Points = []world.PointLight{{ID: "lamp", Position: mgl32.Vec3{0, 3, 0}, Color: mgl32.Vec3{1, 1, 1}, Brightness: 1, Radius: 10}}
	sun := world.DirectionalLight{ID: "sun", Color: mgl32.Vec3{1, 1, 1}, Brightness: 1, Pitch: -45}
	sun.SyncDirection()
	lvl.Lights.Directional = []world.DirectionalLight{sun}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatalf("RebuildIndexes: %v", err)
	}
	prev := world.PlayerEntity
	t.Cleanup(func() { world.PlayerEntity = prev })
	// A player on foot, so the camera is placed on it every frame.
	world.PlayerEntity = world.NewPlayer(mgl32.Vec3{0, 0, 5})

	// The uploads on, as in the running engine, so a texture asked for takes
	// the path that would reach the GPU.
	prevTex := gfx.TextureUploadsEnabled
	gfx.TextureUploadsEnabled = true
	t.Cleanup(func() { gfx.TextureUploadsEnabled = prevTex })

	var hits []string
	gfx.SetGLTripwire(func(what string) { hits = append(hits, what) })
	t.Cleanup(func() { gfx.SetGLTripwire(nil) })

	// The tripwire has teeth: the GL halves it guards do report.
	gfx.UpdateCamMatrix()
	if len(hits) == 0 {
		t.Fatal("UpdateCamMatrix did not trip the GL tripwire; the test below would prove nothing")
	}
	hits = hits[:0]

	const hz = 60
	for i := 0; i < hz/2; i++ {
		Step(lvl, 1.0/hz, Gates{ViewW: 1920, ViewH: 1080}, Input{Intent: mgl32.Vec3{0.05, 0, 0}})
	}
	if len(hits) > 0 {
		t.Errorf("sim.Step reached the GL %d times; first few: %v", len(hits), hits[:min(len(hits), 5)])
	}

	// And the camera it placed is the CPU's to read straight away: the
	// matrix is rebuilt in the step even though it is uploaded by the draw.
	want := mgl32.LookAtV(gfx.Cam.Position, gfx.Cam.Position.Add(gfx.Cam.Direction), gfx.CamUp())
	if gfx.Cam.Matrix != want {
		t.Errorf("after a step the view matrix is %v, want the one the camera's pose gives, %v", gfx.Cam.Matrix, want)
	}
}

// The source half: nothing in sim.go asks for the GL-uploading spellings.
func TestSimSourceUsesTheCPUSpellings(t *testing.T) {
	src := readSimSource(t)
	for _, call := range []string{"UploadLightsTo(", "UpdateCamMatrix(", "UploadCamMatrix("} {
		if n := countCalls(src, call); n > 0 {
			t.Errorf("sim.go calls %s %d times; the step must leave GL to the draw", call, n)
		}
	}
}
