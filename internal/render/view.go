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
)

// ForceWireframe draws every frame as wireframe whatever the caller's
// WorldDraw says — the wireframe view for a caller that does not own one (a
// headless shot, a play script). The editor's own Z toggle is still
// WorldDraw.Wireframe.
var ForceWireframe bool

// viewNames are the views, in the order they are listed. "normal" is GI's
// "off": a view name, not a GI setting.
var viewNames = []string{"normal", "lighting", "probes", "field", "wireframe"}

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
//
// probes and field are GI's, and show nothing while live GI is off; the
// answer's note says so rather than leaving a normal-looking picture to
// explain itself.
func SetView(name string) (note string, err error) {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "", "normal", "off":
		GIDebug, ForceWireframe = GIDebugOff, false
		return "", nil
	case "wireframe", "wire":
		GIDebug, ForceWireframe = GIDebugOff, true
		return "", nil
	}
	m, ok := ParseGIDebugMode(name)
	if !ok {
		return "", fmt.Errorf("%q is not a view: one of %s", name, strings.Join(viewNames, ", "))
	}
	GIDebug, ForceWireframe = m, false
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
	if GIDebug == GIDebugOff {
		return "normal"
	}
	return GIDebug.String()
}
