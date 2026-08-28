package editor

// editor_skirt_panel.go — the control for a floor's skirt.
//
// A skirt closes the band between a stacked sector's floor and whatever is
// under it (world/skirt.go). It only means anything for a sector that has
// something below it, so the panel says which mode is chosen and, when one is,
// whether any geometry actually came of it — a skirt asked for but not drawn
// almost always means the surface it was told to meet is not there.

import (
	"foundry/internal/world"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

var skirtModes = []struct {
	label string
	mode  string
	tip   string
}{
	{"None##skirtnone", world.SkirtNone,
		"No skirt. The band under this floor is left open."},
	{"To Ceiling##skirtceil", world.SkirtToCeiling,
		"Down to the ceiling of the room below — fills the slab between two storeys.\n" +
			"Draws nothing where the sector below has no ceiling."},
	{"To Floor##skirtfloor", world.SkirtToFloor,
		"Down to the FLOOR of whatever is below, making the whole height one face.\n" +
			"This is the overhang: a shelf over ground that runs on underneath it."},
}

func editorSkirtPanel(ch *world.Chunk, s *world.Sector) {
	if ch == nil || s == nil {
		return
	}
	if !imgui.TreeNode("Floor Skirt##3dfskirt") {
		return
	}
	defer imgui.TreePop()

	changed := false
	for i, m := range skirtModes {
		if i > 0 {
			imgui.SameLine()
		}
		on := s.Floor.Skirt == m.mode
		if on {
			imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.2, Y: 0.4, Z: 0.9, W: 1})
		}
		if imgui.Button(m.label) {
			if s.Floor.Skirt != m.mode {
				EditorPushUndo()
				s.Floor.Skirt = m.mode
				changed = true
			}
		}
		if on {
			imgui.PopStyleColor()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip(m.tip)
		}
	}

	if s.Floor.Skirt != world.SkirtNone {
		if textureCombo("Skirt Texture##3dfskirttex", &s.Floor.SkirtTexture, true) {
			changed = true
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Left empty, the skirt takes the floor's own texture.")
		}
		if !world.SkirtDrawable(s) {
			imgui.Text("(nothing drawn — no surface below to meet)")
		}
	}

	if changed {
		// The skirt is derived from neighbouring sectors, so it is the
		// chunk-level pass that rebuilds it, not this sector's own reload.
		ch.RebuildGaps()
	}
}
