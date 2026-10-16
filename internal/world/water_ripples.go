package world

// water_ripples.go — the impacts that ring a water surface
// (docs/terrain-paint/PHASE_W5.md, item 2).
//
// Whatever disturbs water — a foot coming down in the shallows, a body
// falling in, a shot striking the surface — pushes a WaterRipple here. The
// renderer drains the queue each frame into its own short list of live
// rings near the camera (render/water_ripples.go) and hands the water shader
// their centres, ages and strengths as a uniform array: the rings are drawn
// analytically, a damped wave packet spreading out from each centre, so
// there is no ripple texture and no sampler (the water draws through the
// world program, which has none to spare).
//
// The queue is the trail stamps' pattern (soft_ground.go): the simulation
// pushes, the draw drains, a mutex between them, a cap so a frame that is
// never drawn (a headless run) cannot grow it without bound. None of it is
// saved: ripples are a picture, not state.
//
// Rain on the water is not a list of impacts: Level.RainRipples (0..1, the
// level's `rain_ripples:` key and `@level.prop.rain_ripples`) asks the
// shader for a procedural field of drops over every water surface.

import (
	"sync"

	"github.com/go-gl/mathgl/mgl32"
)

// WaterRipple is one disturbance of a water surface: where (world X/Z and
// the surface height), how hard (0..1: a footstep ~0.35, a body falling in
// 1) and how big its rings run (metres the packet spreads to; 0 is the
// default for its strength).
type WaterRipple struct {
	At       mgl32.Vec3
	Strength float32
	Reach    float32
}

var waterRipples struct {
	sync.Mutex
	list []WaterRipple
}

// maxQueuedRipples caps the queue between draws.
const maxQueuedRipples = 64

// PushWaterRipple queues a ripple for the next draw.
func PushWaterRipple(r WaterRipple) {
	waterRipples.Lock()
	if len(waterRipples.list) >= maxQueuedRipples {
		copy(waterRipples.list, waterRipples.list[1:])
		waterRipples.list = waterRipples.list[:len(waterRipples.list)-1]
	}
	waterRipples.list = append(waterRipples.list, r)
	waterRipples.Unlock()
}

// DrainWaterRipples appends the queued ripples to dst and empties the queue.
func DrainWaterRipples(dst []WaterRipple) []WaterRipple {
	waterRipples.Lock()
	dst = append(dst, waterRipples.list...)
	waterRipples.list = waterRipples.list[:0]
	waterRipples.Unlock()
	return dst
}

// ClampRainRipples keeps a rain_ripples value in its 0..1 range.
func ClampRainRipples(v float32) float32 {
	return min(max(v, 0), 1)
}
