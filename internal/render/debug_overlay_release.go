//go:build release

package render

// debug_overlay_release.go — the F3 overlay's absence.
//
// A release build has no imgui, so there is nothing to draw the overlay with.
// The counters in debug.go still run: they cost almost nothing, and the frame
// time and the draw counts are what a crash report needs whether or not anyone
// could see them on screen.

// DebugRenderOverlay does nothing in a release build.
func DebugRenderOverlay() {}
