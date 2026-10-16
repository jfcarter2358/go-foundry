package particle

// particle_wind.go — the wind a layer with `wind:` drifts in
// (docs/terrain-paint/PHASE_W5.md).
//
// The level's wind is the renderer's (the grass, the trees and the cooked
// meshes all bend in it); the renderer hands it here once a frame with
// SetWind, as a velocity in m/s, and a layer whose Wind is set pulls its
// particles' horizontal velocity toward it at that rate. A waterfall's mist
// drifts downwind instead of standing in a column. The simulation may run
// on another goroutine than the renderer, so the value sits behind an
// atomic pointer.

import (
	"sync/atomic"

	"github.com/go-gl/mathgl/mgl32"
)

var wind atomic.Pointer[mgl32.Vec3]

// SetWind sets the wind velocity layers drift in, m/s.
func SetWind(v mgl32.Vec3) { wind.Store(&v) }

// windVelocity is the wind velocity, zero before any SetWind.
func windVelocity() mgl32.Vec3 {
	if w := wind.Load(); w != nil {
		return *w
	}
	return mgl32.Vec3{}
}
