package render

// debug.go — the numbers behind the F3 overlay, and the rows it shows them in.
//
// The numbers and the drawing of them are in separate files because they have
// different fates in a release build: the counters are cheap, harmless and
// still worth having (a script can read them, and a crash log is better with
// them in it), while imgui is not in a release binary at all. See
// debug_overlay.go and debug_overlay_release.go.

import (
	"fmt"
	"os"
	"runtime/metrics"
	"strconv"
	"strings"
	"time"

	"foundry/internal/gfx"
	"foundry/internal/hooks"
	"foundry/internal/platform"
	"foundry/internal/world"
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
	// BrushDrawCalls is how many draw calls the main pass spent on those
	// brushes — one per material per chunk, plus one per moved brush.
	BrushDrawCalls int
	ChunksDrawn    int
	// A compiled level's static surfaces: how many it has, and how many
	// survived the visibility set and the frustum this frame. The ratio is the
	// number to watch — the whole point of compiling is for it to be small in a
	// level built out of rooms, and a ratio near 1 means the level is one open
	// space or is not sealed.
	StaticSurfaces int
	StaticDrawn    int
	// NaniteInstances is the cooked-mesh placements the GPU cull was handed
	// (nanite_scenery.go); how many of their clusters it drew is not read back.
	NaniteInstances int
	// NaniteClusters is the clusters the camera's GPU cull was handed this
	// frame — compiled surfaces' PVS candidates plus every cooked placement's.
	NaniteClusters int
	// NaniteTriangles is the triangles the camera's GPU cull drew, counted
	// only while FOUNDRY_NANITE_STATS=1 (it reads the commands back).
	NaniteTriangles int
	// NaniteVisBins is the material bins the visibility buffer's material
	// pass shaded this frame (nanite_vis.go); 0 off the visibility buffer.
	NaniteVisBins int
	// NaniteSWClusters is the clusters the compute rasteriser drew this
	// frame (nanite_swraster.go), counted only while FOUNDRY_NANITE_STATS=1
	// (it reads the lists' counts back); NaniteSWMode its form: 0 off, 32 or
	// 64 (the atomics it runs on).
	NaniteSWClusters int
	NaniteSWMode     int
	// The sun's virtual shadow map (vsm.go): pages holding a physical page,
	// pages drawn this frame and in how many rectangles, and requests the
	// pool had no page for. VSMOn says the frame's sun used it.
	VSMOn                                    bool
	VSMPages, VSMDrawn, VSMRects, VSMStarved int
	// The Nanite foliage (nfoliage.go): whether the frame's foliage took it,
	// and what its camera pass drew a frame or two ago (read back without
	// waiting): plant clusters, references (a triangle cluster, a part or a
	// brick each), and voxel bricks of those; Overflow its capacity
	// warnings (1 items, 2 references; it grows).
	NaniteFoliage                                              bool
	NaniteFoliageItems, NaniteFoliageRefs, NaniteFoliageBricks int
	NaniteFoliageOverflow                                      uint32
	// The props' collision (collision/scenery_index.go): solid placements by
	// the mode they collide in, and how many cooked meshes have their
	// collision decoded because a body or a ray came near.
	CollisionBox, CollisionHulls, CollisionMesh int
	CollisionResident                           int
	CollisionResidentKB                         float32
	// CollisionLoading is how many paged-out collision meshes are being
	// read ahead of a body now, CollisionSyncLoads how many queries since
	// the level loaded had to wait for one (Phase 3).
	CollisionLoading, CollisionSyncLoads int
	// The sprite pass. Particles is how many quads the emitters put up this
	// frame and SpriteDraws how many draw calls that took — the two numbers an
	// author needs together, because one large batch and a thousand tiny ones
	// cost very differently for the same particle count.
	Particles   int
	Sprites     int
	SpriteDraws int
	// The volumetric pass (volumetric.go): how many marches it ran this frame,
	// and how many particle blobs they carried between them.
	VolumeDraws int
	VolumeBlobs int
	// Scenery is how many placed props survived culling this frame — the one
	// number that says whether a brush-scattered field is costing anything.
	Scenery int
	// Decals is how many projected decals were drawn this frame. Worth its own
	// number because a decal is one draw call per source space it touches, and a
	// firefight adds them faster than an author adds anything.
	Decals int
	// Shadowed point lights, and how many of their cubemaps were actually
	// re-rendered this frame rather than kept (point_shadow_cache.go).
	PointShadows       int
	PointShadowRenders int
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

// debugSampleEvery is how often the process numbers (CPU share, memory) are
// re-read. They are read for a person looking at the overlay, who cannot take
// in more than a couple of updates a second anyway, and neither read is free:
// the CPU figure is a file read and a parse, and the memory one used to be
// runtime.ReadMemStats, which stops the world to take its count.
const debugSampleEvery = 500 * time.Millisecond

// debugLastCPUSample and debugLastMemSample are when DebugUpdateCPU and
// DebugUpdateMemory last did any work.
var (
	debugLastCPUSample time.Time
	debugLastMemSample time.Time
)

// DebugUpdateCPU samples process CPU usage. Call once per frame; it reads the
// process clock at most every debugSampleEvery and keeps the last figure in
// between. The caller only calls it while the overlay is showing.
func DebugUpdateCPU() {
	now := time.Now()
	if now.Sub(debugLastCPUSample) < debugSampleEvery {
		return
	}
	debugLastCPUSample = now
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

// debugMemSamples are the runtime/metrics readings behind the two memory rows:
// live heap objects (what MemStats.Alloc reported) and everything the runtime
// has mapped (MemStats.Sys). Read through runtime/metrics rather than
// ReadMemStats because these do not stop the world.
var debugMemSamples = []metrics.Sample{
	{Name: "/memory/classes/heap/objects:bytes"},
	{Name: "/memory/classes/total:bytes"},
}

// DebugUpdateMemory reads Go runtime memory stats, at most every
// debugSampleEvery.
func DebugUpdateMemory() {
	now := time.Now()
	if now.Sub(debugLastMemSample) < debugSampleEvery {
		return
	}
	debugLastMemSample = now
	metrics.Read(debugMemSamples)
	DebugStats.MemAllocMB = float32(metricBytes(debugMemSamples[0])) / (1024 * 1024)
	DebugStats.MemSysMB = float32(metricBytes(debugMemSamples[1])) / (1024 * 1024)
}

// metricBytes is a runtime/metrics byte count, or 0 if this runtime does not
// know the metric.
func metricBytes(s metrics.Sample) uint64 {
	if s.Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return s.Value.Uint64()
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
	utime, _ := strconv.ParseInt(fields[13], 10, 64)
	stime, _ := strconv.ParseInt(fields[14], 10, 64)
	clkTck := 100.0 // sysconf(_SC_CLK_TCK) is almost always 100 on Linux
	return float64(utime+stime) / clkTck
}

// ---------- what the overlay says ----------
//
// The rows are built here, as text, rather than in the file that draws them.
// There are two drawings of them and only one binary can hold both: a
// development build paints them with imgui (debug_overlay.go), and a release
// build paints them with the runtime's own screen-space pass, which is the only
// text renderer it has (internal/devui). Neither is allowed to be the one that
// knows what "Chunks" means.

// DebugLine is one row of the overlay: a line of text, or a rule between
// groups. The rule is a row rather than a blank string because imgui draws a
// real separator for it and the release overlay draws a real line.
type DebugLine struct {
	Text string
	Rule bool
}

// debugRule is the separator row, spelled once.
var debugRule = DebugLine{Rule: true}

// DebugOverlayLines is the F3 overlay's contents for this frame.
//
// Called once per frame while the overlay is up, and never otherwise: it
// samples live engine state, so the slice it returns is this frame's answer and
// must not be held across one.
func DebugOverlayLines() []DebugLine {
	out := make([]DebugLine, 0, 48)
	line := func(format string, args ...any) {
		out = append(out, DebugLine{Text: fmt.Sprintf(format, args...)})
	}

	line("--- Debug Overlay (F3) ---")
	out = append(out, debugRule)

	line("FPS:          %.1f", DebugStats.FPS)
	line("Frame Time:   %.2f ms", DebugStats.FrameTimeMs)
	line("CPU Usage:    %.1f%%", DebugStats.CPUPercent)
	line("Mem Alloc:    %.1f MB", DebugStats.MemAllocMB)
	line("Mem Sys:      %.1f MB", DebugStats.MemSysMB)
	switch rec, path, left := cpuProfileStatus(); {
	case rec:
		line("CPU profile:  RECORDING %.0fs left (F2 stops) -> %s", left.Seconds(), path)
	case path != "":
		line("CPU profile:  written to %s  (F2 records again)", path)
	default:
		line("CPU profile:  F2 records %.0fs", CPUProfileDefaultSeconds)
	}
	out = append(out, debugRule)

	line("Brushes:      %d loaded / %d drawn", DebugStats.BrushesLoaded, DebugStats.BrushesDrawn)
	line("Brush draws:  %d", DebugStats.BrushDrawCalls)
	if DebugStats.StaticSurfaces > 0 {
		line("Compiled:     %d surfaces / %d drawn", DebugStats.StaticSurfaces, DebugStats.StaticDrawn)
	}
	line("Chunks:       %d loaded / %d drawn", DebugStats.ChunksLoaded, DebugStats.ChunksDrawn)
	line("Sprites:      %d quads / %d particles / %d draws",
		DebugStats.Sprites, DebugStats.Particles, DebugStats.SpriteDraws)
	line("Scenery:      %d drawn", DebugStats.Scenery)
	if DebugStats.CollisionHulls > 0 || DebugStats.CollisionMesh > 0 {
		line("Prop collision: %d box / %d hulls / %d mesh, %d meshes resident (%.0f KB), %d loading, %d waited for",
			DebugStats.CollisionBox, DebugStats.CollisionHulls, DebugStats.CollisionMesh,
			DebugStats.CollisionResident, DebugStats.CollisionResidentKB, DebugStats.CollisionLoading, DebugStats.CollisionSyncLoads)
	}
	if DebugStats.NaniteClusters > 0 || DebugStats.NaniteInstances > 0 {
		line("Nanite:       %d placements, %d clusters to the GPU cull", DebugStats.NaniteInstances, DebugStats.NaniteClusters)
		if NaniteStats {
			line("Nanite drew:  %d triangles", DebugStats.NaniteTriangles)
		}
		if DebugStats.NaniteVisBins > 0 {
			line("Nanite shade: visibility buffer, %d material bins", DebugStats.NaniteVisBins)
		}
		if DebugStats.NaniteSWMode > 0 {
			if NaniteStats {
				line("Nanite raster: compute (%d-bit atomics) for clusters under %.0f px, %d clusters",
					DebugStats.NaniteSWMode, swPixels(), DebugStats.NaniteSWClusters)
			} else {
				line("Nanite raster: compute (%d-bit atomics) for clusters under %.0f px", DebugStats.NaniteSWMode, swPixels())
			}
		}
		// The page pool (nanite_stream.go): what is resident, what moved
		// this frame, and what waits — starved pages mean the view wants
		// more than the pool holds.
		if st := DebugNaniteStream(); st.Slots > 0 {
			line("Nanite pages: %s", st)
		}
	}
	if DebugStats.Decals > 0 {
		// Only when there are any: a level with no decals should not carry a
		// permanently-zero line in an overlay that is already dense.
		line("Decals:       %d drawn", DebugStats.Decals)
	}
	if DebugStats.NaniteFoliage {
		line("Foliage:      Nanite, %d plant clusters, %d references, %d voxel bricks (%s)",
			DebugStats.NaniteFoliageItems, DebugStats.NaniteFoliageRefs, DebugStats.NaniteFoliageBricks, foliage.nan.vox.mode())
	}
	if DebugStats.VSMOn {
		line("Sun shadow:   virtual, %d pages resident, %d drawn in %d rects, %d starved",
			DebugStats.VSMPages, DebugStats.VSMDrawn, DebugStats.VSMRects, DebugStats.VSMStarved)
	}
	line("Point shadows: %d re-rendered of %d", DebugStats.PointShadowRenders, DebugStats.PointShadows)
	if s := GIStatsLast; s.Active {
		line("GI:           %s, %d sources, %d probes traced", s.Tier, s.Sources, s.ProbesTraced)
		line("GI field:     %d bricks built / %d waiting", s.BricksBuilt, s.BricksPending)
	} else {
		line("GI:           off")
	}
	out = append(out, debugRule)

	// The editor flies a free camera and leaves PlayerEntity parked wherever
	// gameplay left it, so reporting that as "Position" there would be a stale
	// number next to a live one.
	switch {
	case hooks.EditorActive():
		c := gfx.Cam.Position
		line("Camera:       %.2f, %.2f, %.2f", c.X(), c.Y(), c.Z())
	case world.PlayerEntity != nil:
		pe := world.PlayerEntity
		p := pe.Position
		line("Position:     %.2f, %.2f, %.2f", p.X(), p.Y(), p.Z())
		// A rider stores its position in its frame's coordinates, so that
		// number is not a place in the world at all — on a ship's deck it
		// reads as a wild coordinate with no way to tell it is not one.
		// The eye is the same point carried out, so report both.
		if pe.Frame != "" {
			c := gfx.Cam.Position
			line("  world (eye):%.2f, %.2f, %.2f", c.X(), c.Y(), c.Z())
		}
		line("Frame:        %s%s", frameLabel(pe.Frame), seatLabel(pe.Seat))
		line("Standing in:  %s", orNone(pe.CurrentChunk))
	default:
		line("Position:     N/A")
	}
	line("Rotation:     yaw=%.2f  pitch=%.2f", gfx.Cam.Yaw, gfx.Cam.Pitch)
	out = append(out, debugRule)

	// Above anti-aliasing because it is the first thing the FPS number at the
	// top has to be read against: 60.0 flat means nothing until you know
	// whether the frame is waiting for the display.
	switch {
	case !platform.VSyncSettable:
		line("VSync:         driver-controlled (not settable, F12)")
	case platform.VSyncInterval == -1:
		line("VSync:         %s  (adaptive, F12)", onOff(platform.VSyncEnabled))
	case platform.VSyncActive() != platform.VSyncEnabled:
		line("VSync:         %s requested, driver says %s  (F12)",
			onOff(platform.VSyncEnabled), onOff(platform.VSyncActive()))
	default:
		line("VSync:         %s  (F12)", onOff(platform.VSyncEnabled))
	}

	if AntiAliasGrantedSamples > 0 {
		line("Anti-aliasing: %s  (MSAA %dx of %dx, F10)",
			onOff(AntiAliasEnabled), AntiAliasGrantedSamples, AntiAliasSamples)
	} else {
		line("Anti-aliasing: unavailable (driver granted 0 samples)")
	}
	// Reported next to MSAA because it is the half of anti-aliasing MSAA can't
	// do (see texture_filter.go), and because a driver silently refusing
	// anisotropy looks exactly like distant ground being blurry for no reason.
	if gfx.TextureAnisotropy > 1 {
		line("Texture filter: mipmaps + %.0fx aniso", gfx.TextureAnisotropy)
	} else {
		line("Texture filter: mipmaps (anisotropic unavailable)")
	}
	// The volumetric pass's cost is almost entirely its resolution and step
	// count, so both are reported next to the frame time they drive.
	if VolumetricsReady() {
		line("Volumetrics:   %s  (F4)", Volumetrics)
		line("               %d marches, %d blobs, cloud shadow map %s",
			DebugStats.VolumeDraws, DebugStats.VolumeBlobs, onOff(CloudShadowActive()))
	} else {
		line("Volumetrics:   unavailable (see the log)")
	}
	out = append(out, debugRule)

	// Where the frame goes, pass by pass (profile.go). GPU is the column that
	// says what the GPU spent; a pass whose CPU number is the larger is the
	// engine or the driver, not the hardware.
	line("%-22s %7s %7s", "Pass", "CPU ms", "GPU ms")
	for _, s := range ProfileSamples() {
		line("%-22s %7.2f %7.2f", strings.Repeat(" ", 2*s.Depth)+s.Name, s.CPUMs, s.GPUMs)
	}

	// TODO(brush migration, Phase 5): the biome lighting cost switches (F5-F9)
	// reported here went with the biome renderer. They come back with scatter
	// volumes.
	return out
}

// onOff renders a bool as an overlay-friendly fixed-width label.
func onOff(b bool) string {
	if b {
		return "ON "
	}
	return "OFF"
}

// frameLabel names the coordinate system the player's position is expressed in.
// The world frame is stored as "", which is not a thing to show anyone.
func frameLabel(id string) string {
	if id == "" {
		return "world"
	}
	return id
}

func seatLabel(seat string) string {
	if seat == "" {
		return ""
	}
	return "  seat=" + seat
}

func orNone(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
