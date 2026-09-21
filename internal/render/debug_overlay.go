//go:build !release

package render

// debug_overlay.go — the F3 overlay, drawn with imgui.
//
// Split from debug.go so that a release build, which has no imgui in it, can
// answer DebugRenderOverlay with a stub and still keep every counter debug.go
// collects. See debug_overlay_release.go.

import (
	"fmt"
	"strings"

	"foundry/internal/gfx"
	"foundry/internal/hooks"
	"foundry/internal/platform"
	"foundry/internal/world"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

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

		imgui.Text(fmt.Sprintf("Brushes:      %d loaded / %d drawn", DebugStats.BrushesLoaded, DebugStats.BrushesDrawn))
		imgui.Text(fmt.Sprintf("Brush draws:  %d", DebugStats.BrushDrawCalls))
		if DebugStats.StaticSurfaces > 0 {
			imgui.Text(fmt.Sprintf("Compiled:     %d surfaces / %d drawn", DebugStats.StaticSurfaces, DebugStats.StaticDrawn))
		}
		imgui.Text(fmt.Sprintf("Chunks:       %d loaded / %d drawn", DebugStats.ChunksLoaded, DebugStats.ChunksDrawn))
		imgui.Text(fmt.Sprintf("Sprites:      %d quads / %d particles / %d draws",
			DebugStats.Sprites, DebugStats.Particles, DebugStats.SpriteDraws))
		imgui.Text(fmt.Sprintf("Scenery:      %d drawn", DebugStats.Scenery))
		imgui.Text(fmt.Sprintf("Point shadows: %d re-rendered of %d", DebugStats.PointShadowRenders, DebugStats.PointShadows))
		imgui.Separator()

		// The editor flies a free camera and leaves PlayerEntity parked
		// wherever gameplay left it, so reporting that as "Position" there
		// would be a stale number next to a live one.
		if hooks.EditorActive() {
			c := gfx.Cam.Position
			imgui.Text(fmt.Sprintf("Camera:       %.2f, %.2f, %.2f", c.X(), c.Y(), c.Z()))
		} else if world.PlayerEntity != nil {
			pe := world.PlayerEntity
			p := pe.Position
			imgui.Text(fmt.Sprintf("Position:     %.2f, %.2f, %.2f", p.X(), p.Y(), p.Z()))
			// A rider stores its position in its frame's coordinates, so that
			// number is not a place in the world at all — on a ship's deck it
			// reads as a wild coordinate with no way to tell it is not one.
			// The eye is the same point carried out, so report both.
			if pe.Frame != "" {
				c := gfx.Cam.Position
				imgui.Text(fmt.Sprintf("  world (eye):%.2f, %.2f, %.2f", c.X(), c.Y(), c.Z()))
			}
			imgui.Text(fmt.Sprintf("Frame:        %s%s", frameLabel(pe.Frame), seatLabel(pe.Seat)))
			imgui.Text(fmt.Sprintf("Standing in:  %s", orNone(pe.CurrentChunk)))
		} else {
			imgui.Text("Position:     N/A")
		}
		imgui.Text(fmt.Sprintf("Rotation:     yaw=%.2f  pitch=%.2f", gfx.Cam.Yaw, gfx.Cam.Pitch))
		imgui.Separator()

		// Above anti-aliasing because it is the first thing the FPS number at
		// the top has to be read against: 60.0 flat means nothing until you
		// know whether the frame is waiting for the display.
		switch {
		case !platform.VSyncSettable:
			imgui.Text("VSync:         driver-controlled (not settable, F12)")
		case platform.VSyncInterval == -1:
			imgui.Text(fmt.Sprintf("VSync:         %s  (adaptive, F12)", onOff(platform.VSyncEnabled)))
		case platform.VSyncActive() != platform.VSyncEnabled:
			imgui.Text(fmt.Sprintf("VSync:         %s requested, driver says %s  (F12)",
				onOff(platform.VSyncEnabled), onOff(platform.VSyncActive())))
		default:
			imgui.Text(fmt.Sprintf("VSync:         %s  (F12)", onOff(platform.VSyncEnabled)))
		}

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
		// The volumetric pass's cost is almost entirely its resolution and step
		// count, so both are reported next to the frame time they drive.
		if VolumetricsReady() {
			imgui.Text(fmt.Sprintf("Volumetrics:   %s  (F4)", Volumetrics))
			imgui.Text(fmt.Sprintf("               %d marches, %d blobs, cloud shadow map %s",
				DebugStats.VolumeDraws, DebugStats.VolumeBlobs, onOff(CloudShadowActive())))
		} else {
			imgui.Text("Volumetrics:   unavailable (see the log)")
		}
		imgui.Separator()

		// Where the frame goes, pass by pass (profile.go). GPU is the column
		// that says what the GPU spent; a pass whose CPU number is the larger
		// is the engine or the driver, not the hardware.
		imgui.Text(fmt.Sprintf("%-22s %7s %7s", "Pass", "CPU ms", "GPU ms"))
		for _, s := range ProfileSamples() {
			imgui.Text(fmt.Sprintf("%-22s %7.2f %7.2f", strings.Repeat(" ", 2*s.Depth)+s.Name, s.CPUMs, s.GPUMs))
		}
		imgui.Separator()

		// TODO(brush migration, Phase 5): the biome lighting cost switches
		// (F5-F9) reported here went with the biome renderer. They come back
		// with scatter volumes.
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
