package gfx

// sim_gl.go — keeping the simulation half of a frame off the GL.
//
// internal/sim's Step is everything that moves the world, and it ought to need
// no GL context at all: that is what lets it run headlessly (cmd/foundry sim)
// and, later, on a thread of its own. It used to reach the driver in three
// ways — the lights' uniforms whenever a waypoint, an emitter or an effect
// moved one, the view matrix every time the camera was placed, and a texture
// upload whenever something it spawned or retextured named a texture nothing
// had loaded yet. The lights are now the scene block, written by DrawWorld; the
// view matrix is rebuilt on the CPU (RebuildCamMatrix) and uploaded by DrawWorld
// (UploadCamMatrix); and a texture asked for during the step is decoded there
// but handed to the GPU by the next drain (DrainDeferredTextures), which runs
// on the GL thread before the frame draws anything — so it is resident for the
// very frame that asked for it, as before.
//
// The tripwire is the test half: with one installed, the GL entry points in
// this package report and skip their GL instead of making it, which is how
// TestSimStepMakesNoGLCalls proves a frame of the world asks for none.

// inSimStep is set for the length of sim.Step (BeginSimStep/EndSimStep).
var inSimStep bool

// BeginSimStep and EndSimStep bracket sim.Step: between them, GL work that can
// wait for the frame's GL half is queued rather than done.
func BeginSimStep() { inSimStep = true }

// EndSimStep: see BeginSimStep.
func EndSimStep() { inSimStep = false }

// glTripwire, when set, is told about every GL entry point reached in this
// package, and the GL itself is skipped.
var glTripwire func(what string)

// SetGLTripwire installs f (nil removes it). For tests: see the file comment.
func SetGLTripwire(f func(what string)) { glTripwire = f }

// glBlocked reports what to the tripwire and says whether to skip the GL call
// that follows. False whenever no tripwire is installed.
func glBlocked(what string) bool {
	if glTripwire == nil {
		return false
	}
	glTripwire(what)
	return true
}
