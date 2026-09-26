package editor

// editor_environment_panel.go — the level's sky and fog, in the properties
// panel.
//
// Level-scoped settings with nowhere else to live, so they sit beside the
// level's UI screen list (editor_ui_level_panel.go) rather than being reachable
// only by hand-editing the level file, which is where the level's music still
// is. Neither is a property of a chunk, a sector or any placed object: they are
// properties of the air, and there is exactly one of each.
//
// Both are optional, and the checkbox at the top of each group is what creates
// and destroys them — a level with no sky writes no `sky:` block, which is what
// keeps every level authored before this one looking exactly as it did. Turning
// one OFF is not the same gesture: Fog.Enabled keeps the numbers while hiding
// their effect, the way a switched-off light keeps its colour.

import (
	"fmt"
	"foundry/internal/gfx"
	"foundry/internal/material"
	"foundry/internal/world"

	"foundry/internal/imguibackend"
	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorEnvironmentSection draws the Sky and Fog blocks.
func editorEnvironmentSection(lvl *world.Level) {
	if lvl == nil {
		return
	}
	imgui.Separator()
	if !imgui.CollapsingHeader("Environment##env") {
		return
	}
	editorSkyGroup(lvl)
	editorFogGroup(lvl)
	editorViewDistanceGroup(lvl)
}

// ---------- View distance ----------

// editorViewDistanceGroup is the level's far plane. Zero is "the engine's",
// so the field needs no checkbox: at rest it already says "not set".
func editorViewDistanceGroup(lvl *world.Level) {
	editorPropGroup("View distance##viewdistgrp", 0, func() {
		d := world.LevelViewDistance(lvl)
		if editorDragFloatStep("View distance##viewdist", &d, world.MinViewDistance, world.MaxViewDistance, "%.0f", 50) {
			EditorPushUndo()
			lvl.ViewDistance = world.ClampViewDistance(d)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("How far the camera sees, in world units — the far clip\n" +
				"plane. Raise it for a large outdoor level. Longer costs\n" +
				"depth precision far away, so go only as far as needed.")
		}
		if lvl.ViewDistance > 0 {
			if imgui.Button("Use default##viewdistdef") {
				EditorPushUndo()
				lvl.ViewDistance = 0
				editorSetStatus("View distance back to the default (%.0f)", world.DefaultViewDistance)
			}
		} else {
			editorTextMutedWrapped(fmt.Sprintf("Following the default: %.0f units.", world.DefaultViewDistance))
		}
	})
}

// ---------- Sky ----------

func editorSkyGroup(lvl *world.Level) {
	editorPropGroup("Sky##skygrp", 0, func() {
		has := lvl.Sky != nil
		if imgui.Checkbox("This level has a sky##skyon", &has) {
			EditorPushUndo()
			if has {
				s := world.DefaultSky()
				lvl.Sky = &s
				editorSetStatus("Added a sky to the level")
			} else {
				lvl.Sky = nil
				editorSetStatus("Removed the level's sky")
			}
		}
		if lvl.Sky == nil {
			editorTextMutedWrapped(
				"Without one the view clears to a flat colour behind the geometry, which is what every level did before skies existed.")
			return
		}
		editorSkyBody(lvl.Sky)
	})
}

func editorSkyBody(s *world.Sky) {
	// A sky material, if any. Unlike every other domain it is FOLDED INTO the
	// fields below when the level loads rather than overlaid at draw time (see
	// world/sky_material.go), so what the rows underneath show once one is
	// named is what the material set — editing them by hand works, and the
	// next reload puts the material's values back.
	if materialCombo("Material##skymat", &s.Material, material.DomainSky) {
		EditorPushUndo()
		s.ApplyMaterial()
	}
	if s.Material != "" {
		editorTextMutedWrapped(
			"The rows below show what the material set. Changing one here holds until the level is reloaded; " +
				"to make it stick, change it in the material.")
	}

	// Kind first: it decides which half of this block is even relevant, and the
	// gradient stops and the six faces are both kept across a switch so
	// flipping between them to compare does not cost either one.
	kind := world.SkyKindName(s.Kind)
	if imguibackend.BeginCombo("Kind##skykind", kind) {
		for _, name := range world.SkyKindNames {
			if imguibackend.SelectableV(name, name == kind, 0, imgui.Vec2{}) && name != kind {
				EditorPushUndo()
				s.Kind = world.SkyKindFromName(name)
			}
		}
		imguibackend.EndCombo()
	}

	if s.Kind == world.SkyCubemap {
		editorSkyFaces(s)
	} else {
		editorSkyColor("Zenith##skyzen", &s.Zenith)
		editorSkyColor("Horizon##skyhor", &s.Horizon)
		editorSkyColor("Ground##skygnd", &s.Ground)
		editorTextMutedWrapped(
			"Ground is what is seen looking down where the level has no floor — over a cliff edge, or off a bridge.")
	}

	editorSkyColor("Tint##skytint", &s.Tint)
	editorDragFloatStep("Intensity##skyint", &s.Intensity, 0, 16, "%.2f", 0.05)
	editorDragFloatStep("Rotation##skyrot", &s.Rotation, -360, 360, "%.0f deg", 1)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Turns the sky about the vertical axis.\nHow a cubemap is lined up with the level's own sun\nwithout repainting it.")
	}
	editorSkyClouds(s)
	s.Clamp()
}

// editorSkyClouds draws the sky's volumetric cloud deck. A block of its own
// with its own checkbox, the same create-and-destroy gesture as the sky's.
func editorSkyClouds(s *world.Sky) {
	imgui.Separator()
	has := s.Clouds != nil
	if imgui.Checkbox("Volumetric cloud deck##skycloudson", &has) {
		EditorPushUndo()
		if has {
			d := world.DefaultSkyClouds()
			s.Clouds = &d
			editorSetStatus("Added a cloud deck to the sky")
		} else {
			s.Clouds = nil
			editorSetStatus("Removed the sky's cloud deck")
		}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A layer of ray-marched cloud between two heights, running out to the horizon.\n" +
			"Unlike the dome it has an altitude, so terrain can rise into it.")
	}
	if s.Clouds == nil {
		return
	}
	d := s.Clouds
	on := d.Enabled
	if imgui.Checkbox("Enabled##skycloud_en", &on) {
		EditorPushUndo()
		d.Enabled = on
	}
	editorDragFloatStep("Altitude##skycloudalt", &d.Altitude, -world.SkyCloudMaxAltitude, world.SkyCloudMaxAltitude, "%.0f", 1)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The height of the deck's base, in world units.")
	}
	editorDragFloatStep("Thickness##skycloudthick", &d.Thickness, world.SkyCloudMinThickness, 5000, "%.0f", 1)
	editorDragFloatStep("Range##skycloudrange", &d.Range, world.SkyCloudMinRange, 50000, "%.0f", 10)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How far from the eye the deck is drawn before it fades into the horizon.\nIt bounds the cost of looking along the deck, not only its look.")
	}
	editorCloudLook("sky", &d.CloudLook)
	editorTextMutedWrapped("ease_sky blends the deck too: a script can clear or thicken the clouds along with the dome.")
}

// editorSkyFaces draws the six texture pickers and the shortcut that fills them.
func editorSkyFaces(s *world.Sky) {
	editorTextMutedWrapped(
		"Six textures, in GL face order. All six must be set — a partly filled cubemap falls back to the gradient rather than drawing five faces and a hole.")

	if imgui.Button("Fill from base name##skybase") {
		imgui.OpenPopup("##skybasepop")
	}
	imgui.SameLine()
	editorTextMuted("e.g. dusk -> dusk_px, dusk_nx, ...")
	if imgui.BeginPopup("##skybasepop") {
		imgui.Text("Base name")
		if imgui.InputTextV("##skybasein", &Editor.SkyBaseName, imgui.InputTextFlagsEnterReturnsTrue, nil) {
			EditorPushUndo()
			releaseSkyCubemap(s)
			s.SetFacesFromBase(Editor.SkyBaseName)
			for _, f := range s.Faces {
				_ = gfx.EnsureTextureLoaded(f)
			}
			editorSetStatus("Sky faces set from %q", Editor.SkyBaseName)
			imgui.CloseCurrentPopup()
		}
		imgui.EndPopup()
	}

	for i := range s.Faces {
		label := fmt.Sprintf("%s##skyface%d", world.SkyFaceOrder[i], i)
		before := s.Faces
		if textureCombo(label, &s.Faces[i], true) {
			// Keyed on the face set it had BEFORE the edit — that is the
			// cubemap now orphaned, and releasing it is what keeps a session
			// spent trying skies from accumulating one uploaded texture set per
			// attempt.
			releaseFaces(before)
		}
	}
}

// releaseSkyCubemap and releaseFaces drop a cubemap the level no longer names.
func releaseSkyCubemap(s *world.Sky) { releaseFaces(s.Faces) }

func releaseFaces(faces [world.SkyFaces]string) {
	for _, f := range faces {
		if f == "" {
			return // never was a complete cubemap, so nothing was uploaded
		}
	}
	gfx.ReleaseCubemap(faces)
}

func editorSkyColor(label string, v *mgl32.Vec3) {
	col := [3]float32{v.X(), v.Y(), v.Z()}
	if imgui.ColorEdit3(label, &col) {
		*v = mgl32.Vec3{col[0], col[1], col[2]}
	}
}

// ---------- Fog ----------

func editorFogGroup(lvl *world.Level) {
	editorPropGroup("Fog##foggrp", 0, func() {
		has := lvl.Fog != nil
		if imgui.Checkbox("This level has fog##fogon", &has) {
			EditorPushUndo()
			if has {
				f := world.DefaultFog()
				// Start it agreeing with the sky it will be seen against. Fog
				// whose colour fights the horizon is the single most common way
				// for a first attempt at it to look wrong, and matching is the
				// answer often enough to be the default rather than a button.
				if lvl.Sky != nil {
					f.Color = lvl.Sky.HorizonColor()
				}
				lvl.Fog = &f
				editorSetStatus("Added fog to the level")
			} else {
				lvl.Fog = nil
				editorSetStatus("Removed the level's fog")
			}
		}
		if lvl.Fog == nil {
			return
		}
		editorFogBody(lvl)
	})
}

func editorFogBody(lvl *world.Level) {
	f := lvl.Fog

	on := f.Enabled
	if imgui.Checkbox("Enabled##fogen", &on) {
		EditorPushUndo()
		f.Enabled = on
	}
	if !f.Enabled {
		editorTextMutedWrapped("Switched off — everything below is kept.")
	}

	mode := world.FogModeName(f.Mode)
	if imguibackend.BeginCombo("Mode##fogmode", mode) {
		for _, name := range world.FogModeNames {
			if imguibackend.SelectableV(name, name == mode, 0, imgui.Vec2{}) && name != mode {
				EditorPushUndo()
				f.Mode = world.FogModeFromName(name)
			}
		}
		imguibackend.EndCombo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("linear: nothing until Start, full at End — the mode to hide a draw distance with.\n" +
			"exp: haze with no far limit.\nexp2: clear up close, then thickening fast — a fog bank.")
	}

	editorSkyColor("Colour##fogcol", &f.Color)
	if lvl.Sky != nil {
		imgui.SameLine()
		if imgui.Button("Match sky##fogmatch") {
			f.Color = lvl.Sky.HorizonColor()
			editorSetStatus("Fog colour matched to the sky's horizon")
		}
	}

	editorDragFloatStep("Start##fogstart", &f.Start, 0, world.FogMaxDistance, "%.1f", 1)
	if f.Mode == world.FogLinear {
		editorDragFloatStep("End##fogend", &f.End, 0, world.FogMaxDistance, "%.1f", 1)
	} else {
		editorDragFloatStep("Density##fogdens", &f.Density, 0, 1, "%.4f", 0.0005)
	}

	editorDragFloatStep("Max opacity##fogmax", &f.MaxOpacity, 0, 1, "%.2f", 0.05)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Caps the blend. Below 1 a landmark stays a silhouette\nat the far end of heavy fog instead of disappearing.")
	}

	if lvl.Sky != nil {
		editorDragFloatStep("Sky blend##fogsky", &f.SkyBlend, 0, 1, "%.2f", 0.05)
		editorDragFloatStep("Sky falloff##fogskyf", &f.SkyFalloff, 0.01, 32, "%.1f", 0.25)
		editorTextMutedWrapped(
			"How much of the fog colour the sky takes at the horizon. Without it the ground fades into the fog while the sky behind it stays crisp, and the join becomes the most visible edge on screen.")
	} else {
		editorTextMutedWrapped("Add a sky to blend the fog into the horizon.")
	}

	f.Clamp()
}
