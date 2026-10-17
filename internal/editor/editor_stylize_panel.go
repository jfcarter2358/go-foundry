package editor

// editor_stylize_panel.go — the level's painted-miniature look
// (world.Stylize; experimental): a switch in the 3D view's View menu and its
// numbers in the Environment section.
//
// The look is the level's, saved in its `stylize:` block, so switching it is
// an edit (undoable, marks the level changed) rather than a view setting —
// play draws what the editor showed. The first switch-on writes a block with
// the defaults; switching off after that keeps the numbers (Enabled), the way
// the fog's switch does.

import (
	"foundry/internal/gfx"
	"foundry/internal/render"
	"foundry/internal/world"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

const editorStylizeTooltip = "Experimental. A painted-miniature look for this level: shadows\n" +
	"turn cool instead of black, bright light warms, convex edges are\n" +
	"highlighted, creases washed and raised detail dry-brushed.\n" +
	"Saved with the level. Settings: Properties > Environment > Stylized lighting."

// editorStylizeOn reports whether lvl is drawn with its painted look.
func editorStylizeOn(lvl *world.Level) bool { return world.LevelStylize(lvl) != nil }

// editorSetStylize switches lvl's look, writing a default block the first
// time.
func editorSetStylize(lvl *world.Level, on bool) {
	if lvl == nil || editorStylizeOn(lvl) == on {
		return
	}
	EditorPushUndo()
	if lvl.Stylize == nil {
		s := world.DefaultStylize()
		lvl.Stylize = &s
	}
	lvl.Stylize.Enabled = on
	if on {
		editorSetStatus("Stylized lighting on")
	} else {
		editorSetStatus("Stylized lighting off (its settings are kept)")
	}
}

// editorStylizeMenuItem is the View menu's switch.
func editorStylizeMenuItem(lvl *world.Level) {
	on := editorStylizeOn(lvl)
	if imgui.MenuItemV("Stylized Lighting (Experimental)", "", on, lvl != nil) {
		editorSetStylize(lvl, !on)
	}
	editorMenuTooltip(editorStylizeTooltip)
}

// editorStylizeGroup is the Environment section's group: the switch and,
// once the level has a block, its numbers.
func editorStylizeGroup(lvl *world.Level) {
	editorPropGroup("Stylized lighting (experimental)##stygrp", 0, func() {
		on := editorStylizeOn(lvl)
		if imgui.Checkbox("Painted look##styon", &on) {
			editorSetStylize(lvl, on)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip(editorStylizeTooltip)
		}
		if lvl.Stylize == nil {
			return
		}
		s := lvl.Stylize
		if !s.Enabled {
			editorTextMutedWrapped("Switched off — everything below is kept.")
		}
		if gfx.AntiAliasMode != gfx.AAModeTAA || !render.EditorSceneFrame {
			editorTextMutedWrapped("The edge, wash and dry-brush need TAA anti-aliasing; " +
				"in this mode only the light ramp shows.")
		}

		imgui.Text("Light")
		editorSkyColor("Shadow colour##styshc", &s.ShadowColor)
		editorDragFloatStep("Shadow tint##stysht", &s.ShadowTint, 0, 1, "%.2f", 0.05)
		if imgui.IsItemHovered() {
			imgui.SetTooltip("How far the dark side turns toward the shadow colour.")
		}
		editorDragFloatStep("Shadow fill##styshf", &s.ShadowFill, 0, 1, "%.3f", 0.01)
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Shadow-coloured light lifted into the dark, so nothing goes black.")
		}
		editorSkyColor("Highlight colour##styhic", &s.HighlightColor)
		editorDragFloatStep("Highlight tint##styhit", &s.HighlightTint, 0, 1, "%.2f", 0.05)
		editorDragFloatStep("Shadow point##styshp", &s.ShadowPoint, 0, 4, "%.2f", 0.01)
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Light (as shown on screen, 0..1) at and below which a\nsurface counts as fully in shadow.")
		}
		editorDragFloatStep("Light point##stylip", &s.LightPoint, 0, 8, "%.2f", 0.01)
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Light at and above which a surface takes the full highlight tint.")
		}

		imgui.Text("Paint")
		editorSkyColor("Edge colour##styedc", &s.EdgeColor)
		editorDragFloatStep("Edge highlight##styed", &s.Edge, 0, 2, "%.2f", 0.05)
		editorDragFloatStep("Edge width##styedw", &s.EdgeWidth, 0.5, 8, "%.1f px", 0.1)
		if imgui.IsItemHovered() {
			imgui.SetTooltip("In pixels of a 1080-line picture; wider screens scale it.")
		}
		editorSkyColor("Wash colour##stywac", &s.WashColor)
		editorDragFloatStep("Recess wash##stywa", &s.Wash, 0, 1, "%.2f", 0.05)
		editorSkyColor("Dry-brush colour##stydrc", &s.DrybrushColor)
		editorDragFloatStep("Dry-brush##stydr", &s.Drybrush, 0, 2, "%.2f", 0.05)
		editorDragFloatStep("Dry-brush scale##stydrs", &s.DrybrushScale, 0.01, 200, "%.2f", 0.1)
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Brush strokes per world unit.")
		}
		editorDragFloatStep("Dry-brush threshold##stydrt", &s.DrybrushThreshold, 0, 1, "%.2f", 0.02)
		if imgui.IsItemHovered() {
			imgui.SetTooltip("How raised detail must be before the brush catches it.")
		}
		editorDragFloatStep("Fade distance##styfd", &s.FadeDistance, 1, 100000, "%.0f", 1)
		if imgui.IsItemHovered() {
			imgui.SetTooltip("World units from the camera at which the edge, wash and\ndry-brush have faded out. The light ramp does not fade.")
		}
		if imgui.Button("Reset to defaults##styreset") {
			EditorPushUndo()
			d := world.DefaultStylize()
			d.Enabled = s.Enabled
			*s = d
			editorSetStatus("Stylized lighting back to its defaults")
		}
		s.Clamp()
	})
}
