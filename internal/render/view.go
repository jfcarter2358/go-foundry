package render

// view.go — the frame's debug views, by name.
//
// The views themselves are older than this file: GI's three (gi.go) and the
// 3D editor's wireframe shading. What is here is one name for each, so the
// things that are not a person at the View menu — a play script's `view`, the
// headless `foundry play -view`, the editor's `viewport` tool — can ask for the
// same picture the menu gives, and say back which one they got.

import (
	"fmt"
	"strings"

	"foundry/internal/gfx"
	"foundry/internal/world"
)

// ForceWireframe draws every frame as wireframe whatever the caller's
// WorldDraw says — the wireframe view for a caller that does not own one (a
// headless shot, a play script). The editor's own Z toggle is still
// WorldDraw.Wireframe.
var ForceWireframe bool

// viewNames are the views, in the order they are listed. "normal" is GI's
// "off": a view name, not a GI setting.
var viewNames = []string{"normal", "lighting", "probes", "field", "specular", "mirror", "ao", "wireframe", "clusters", "lod", "overdraw"}

// ViewNames lists the names SetView takes.
func ViewNames() []string { return append([]string(nil), viewNames...) }

// IsView reports whether SetView takes name, without switching to it — for a
// caller that checks a whole script before any of it runs.
func IsView(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "", "normal", "off", "wireframe", "wire":
		return true
	}
	if _, ok := parseNaniteDebug(name); ok {
		return true
	}
	_, ok := ParseGIDebugMode(name)
	return ok
}

// SetView switches the frame to a debug view by name:
//
//	normal     the frame as a player sees it
//	lighting   the light alone, on white surfaces — no textures
//	probes     each live-GI probe as a dot of its own light
//	field      the distance field live GI traces against
//	wireframe  the geometry as lines, the filled passes left out
//	clusters   what the Nanite cull draws, each cluster its own colour
//	lod        the same, coloured by the LOD level each cluster comes from
//	overdraw   the same, every rasterised fragment added up (nanite_debug.go)
//
// probes and field are GI's, and show nothing while live GI is off; the
// answer's note says so rather than leaving a normal-looking picture to
// explain itself. The Nanite views likewise say when the cull is off.
func SetView(name string) (note string, err error) {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "", "normal", "off":
		GIDebug, ForceWireframe, NaniteDebug = GIDebugOff, false, naniteDebugOff
		return "", nil
	case "wireframe", "wire":
		GIDebug, ForceWireframe, NaniteDebug = GIDebugOff, true, naniteDebugOff
		return "", nil
	}
	if m, ok := parseNaniteDebug(name); ok {
		GIDebug, ForceWireframe, NaniteDebug = GIDebugOff, false, m
		if !NaniteClusters && !NaniteChunks && !world.NaniteMeshes {
			return "the Nanite cull is switched off (FOUNDRY_NANITE_*=0), so the " + name + " view colours nothing", nil
		}
		if m == naniteDebugLOD && !NaniteLOD {
			return "LOD selection is off (FOUNDRY_NANITE_LOD=0): everything is level 0", nil
		}
		return "", nil
	}
	m, ok := ParseGIDebugMode(name)
	if !ok {
		return "", fmt.Errorf("%q is not a view: one of %s", name, strings.Join(viewNames, ", "))
	}
	GIDebug, ForceWireframe, NaniteDebug = m, false, naniteDebugOff
	if (m == GIDebugProbes || m == GIDebugField) && gfx.GIQuality == gfx.GIOff {
		return "live GI is off, so the " + name + " view shows nothing of it — turn GI on (low/medium/high) to see it", nil
	}
	return "", nil
}

// ViewName is the view the frame is in now.
func ViewName() string {
	if ForceWireframe {
		return "wireframe"
	}
	if n, ok := naniteDebugNames[NaniteDebug]; ok {
		return n
	}
	if GIDebug == GIDebugOff {
		return "normal"
	}
	return GIDebug.String()
}
