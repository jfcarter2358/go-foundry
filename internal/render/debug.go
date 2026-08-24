package render

import (
	"fmt"
	"foundry/internal/gfx"
	"foundry/internal/hooks"
	"foundry/internal/world"
	"os"
	"runtime"
	"strings"
	"time"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// DebugOverlayVisible controls whether the F3 debug overlay is drawn.
var DebugOverlayVisible = false

// DebugStats holds the values displayed in the debug overlay.
var DebugStats struct {
	FPS                 float32
	FrameTimeMs         float32
	CPUPercent          float32
	MemAllocMB          float32
	MemSysMB            float32
	SectorsLoaded       int
	ChunksLoaded        int
	SectorsDrawn        int
	SectorsPortalCulled int
	ChunksDrawn         int
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

// DebugRenderOverlay draws the debug HUD using ImGui.
// Must be called between ImGuiNewFrame and ImGuiRender.
func DebugRenderOverlay() {
	if !DebugOverlayVisible {
		return
	}

	// Position at top-left with some padding. In the editor the full-width
	// toolbar owns the first 28px, and an overlay parked on top of it would
	// eat the clicks meant for the mode buttons — drop below it instead.
	top := float32(10)
	if hooks.EditorActive() {
		top = 38
	}
	imgui.SetNextWindowPosV(imgui.Vec2{X: 10, Y: top}, imgui.ConditionAlways, imgui.Vec2{})
	imgui.SetNextWindowBgAlpha(0.6)

	flags := imgui.WindowFlagsNoDecoration |
		imgui.WindowFlagsAlwaysAutoResize |
		imgui.WindowFlagsNoSavedSettings |
		imgui.WindowFlagsNoFocusOnAppearing |
		imgui.WindowFlagsNoNav |
		imgui.WindowFlagsNoMove

	if imgui.BeginV("##debug_overlay", nil, flags) {
		imgui.Text("--- Debug Overlay (F3) ---")
		imgui.Separator()

		imgui.Text(fmt.Sprintf("FPS:          %.1f", DebugStats.FPS))
		imgui.Text(fmt.Sprintf("Frame Time:   %.2f ms", DebugStats.FrameTimeMs))
		imgui.Text(fmt.Sprintf("CPU Usage:    %.1f%%", DebugStats.CPUPercent))
		imgui.Text(fmt.Sprintf("Mem Alloc:    %.1f MB", DebugStats.MemAllocMB))
		imgui.Text(fmt.Sprintf("Mem Sys:      %.1f MB", DebugStats.MemSysMB))
		imgui.Separator()

		imgui.Text(fmt.Sprintf("Sectors:      %d loaded / %d drawn / %d portal-culled", DebugStats.SectorsLoaded, DebugStats.SectorsDrawn, DebugStats.SectorsPortalCulled))
		imgui.Text(fmt.Sprintf("Chunks:       %d loaded / %d drawn", DebugStats.ChunksLoaded, DebugStats.ChunksDrawn))
		imgui.Separator()

		// The editor flies a free camera and leaves PlayerEntity parked
		// wherever gameplay left it, so reporting that as "Position" there
		// would be a stale number next to a live one.
		if hooks.EditorActive() {
			c := gfx.Cam.Position
			imgui.Text(fmt.Sprintf("Camera:       %.2f, %.2f, %.2f", c.X(), c.Y(), c.Z()))
		} else if world.PlayerEntity != nil {
			p := world.PlayerEntity.Position
			imgui.Text(fmt.Sprintf("Position:     %.2f, %.2f, %.2f", p.X(), p.Y(), p.Z()))
		} else {
			imgui.Text("Position:     N/A")
		}
		imgui.Text(fmt.Sprintf("Rotation:     yaw=%.2f  pitch=%.2f", gfx.Cam.Yaw, gfx.Cam.Pitch))
		imgui.Separator()

		if AntiAliasGrantedSamples > 0 {
			imgui.Text(fmt.Sprintf("Anti-aliasing: %s  (MSAA %dx of %dx, F10)",
				onOff(AntiAliasEnabled), AntiAliasGrantedSamples, AntiAliasSamples))
		} else {
			imgui.Text("Anti-aliasing: unavailable (driver granted 0 samples)")
		}
		// Reported next to MSAA because it is the half of anti-aliasing MSAA
		// can't do (see texture_filter.go), and because a driver silently
		// refusing anisotropy looks exactly like distant ground being blurry
		// for no reason.
		if gfx.TextureAnisotropy > 1 {
			imgui.Text(fmt.Sprintf("Texture filter: mipmaps + %.0fx aniso", gfx.TextureAnisotropy))
		} else {
			imgui.Text("Texture filter: mipmaps (anisotropic unavailable)")
		}
		imgui.Separator()

		// Biome lighting cost switches. Reported here, next to the frame time,
		// because that is the whole point of their being runtime-toggleable:
		// flip one and read what it cost off the line above rather than
		// guessing. Driven by keys (see input.go) rather than by checkboxes so
		// they work with the mouse captured for camera look.
		imgui.Text("--- Biome lighting ---")
		imgui.Text(fmt.Sprintf("Receive shadows:  %s  (F5)", onOff(BiomeShadows.Receive)))
		imgui.Text(fmt.Sprintf("Cast rock/moss:   %s  (F6)", onOff(BiomeShadows.CastRockMoss)))
		imgui.Text(fmt.Sprintf("Cast foliage:     %s  (F7)", onOff(BiomeShadows.CastFoliage)))
		imgui.Text(fmt.Sprintf("Cast cascades:    %d of %d  (F8)", BiomeShadows.CastCascades, ShadowCascades))
		imgui.Text(fmt.Sprintf("Cast range:       %.0f", BiomeShadows.CastRange))
		imgui.Text(fmt.Sprintf("Cast LOD tiers:   %d of 3  (F9)", BiomeShadows.CastLODTiers))
	}
	imgui.End()
}

// onOff renders a bool as an overlay-friendly fixed-width label.
func onOff(b bool) string {
	if b {
		return "ON "
	}
	return "OFF"
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
