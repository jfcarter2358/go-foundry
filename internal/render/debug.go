package render

// debug.go — the numbers behind the F3 overlay.
//
// The numbers and the drawing of them are in separate files because they have
// different fates in a release build: the counters are cheap, harmless and
// still worth having (a script can read them, and a crash log is better with
// them in it), while the overlay itself is imgui and imgui is not in a release
// binary at all. See debug_overlay.go and debug_overlay_release.go.

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
)

// DebugOverlayVisible controls whether the F3 debug overlay is drawn.
var DebugOverlayVisible = false

// DebugStats holds the values displayed in the debug overlay.
var DebugStats struct {
	FPS           float32
	FrameTimeMs   float32
	CPUPercent    float32
	MemAllocMB    float32
	MemSysMB      float32
	BrushesLoaded int
	ChunksLoaded  int
	BrushesDrawn  int
	ChunksDrawn   int
	// A compiled level's static surfaces: how many it has, and how many
	// survived the visibility set and the frustum this frame. The ratio is the
	// number to watch — the whole point of compiling is for it to be small in a
	// level built out of rooms, and a ratio near 1 means the level is one open
	// space or is not sealed.
	StaticSurfaces int
	StaticDrawn    int
	// The sprite pass. Particles is how many quads the emitters put up this
	// frame and SpriteDraws how many draw calls that took — the two numbers an
	// author needs together, because one large batch and a thousand tiny ones
	// cost very differently for the same particle count.
	Particles   int
	Sprites     int
	SpriteDraws int
	// Scenery is how many placed props survived culling this frame — the one
	// number that says whether a brush-scattered field is costing anything.
	Scenery int
}

// cpuTracker holds state for per-frame CPU usage estimation.
var cpuTracker struct {
	lastTime    time.Time
	lastCPU     float64 // cumulative CPU seconds at last sample
	initialized bool
}

// DebugToggle flips the debug overlay on/off.
func DebugToggle() {
	DebugOverlayVisible = !DebugOverlayVisible
}

// DebugUpdateCPU samples process CPU usage. Call once per frame.
func DebugUpdateCPU() {
	now := time.Now()
	cpuSec := processCPUSeconds()

	if !cpuTracker.initialized {
		cpuTracker.lastTime = now
		cpuTracker.lastCPU = cpuSec
		cpuTracker.initialized = true
		return
	}

	wall := now.Sub(cpuTracker.lastTime).Seconds()
	if wall > 0 {
		DebugStats.CPUPercent = float32((cpuSec - cpuTracker.lastCPU) / wall * 100.0)
	}
	cpuTracker.lastTime = now
	cpuTracker.lastCPU = cpuSec
}

// DebugUpdateMemory reads Go runtime memory stats.
func DebugUpdateMemory() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	DebugStats.MemAllocMB = float32(m.Alloc) / (1024 * 1024)
	DebugStats.MemSysMB = float32(m.Sys) / (1024 * 1024)
}

// processCPUSeconds returns the cumulative CPU time used by this process.
// On Linux it reads /proc/self/stat; on other platforms it returns 0.
func processCPUSeconds() float64 {
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) < 15 {
		return 0
	}
	var utime, stime int64
	fmt.Sscanf(fields[13], "%d", &utime)
	fmt.Sscanf(fields[14], "%d", &stime)
	clkTck := 100.0 // sysconf(_SC_CLK_TCK) is almost always 100 on Linux
	return float64(utime+stime) / clkTck
}
