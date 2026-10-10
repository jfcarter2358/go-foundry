package editor

// editor_material_picker.go — the Material row, wherever something can wear one.
//
// One widget, used by the face panel, the sprite panel, the entity
// attachment panels, the particle editor's layer panel and the sky panel. It
// is deliberately the same row everywhere, because a material means the same
// thing everywhere: naming one overlays its effects on whatever the object
// itself is set to, and clearing it takes them back off.
//
// The list is filtered by DOMAIN. A wall is not offered a sky and a sprite is
// not offered a wall's water, because an effect in the wrong domain resolves to
// nothing and a picker that offers it is a picker that lies. A name the level
// carries that no longer resolves is still shown, marked, rather than silently
// dropped — the courtesy textureCombo pays a texture that has gone.

import (
	"foundry/internal/imguibackend"
	"foundry/internal/material"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// materialCombo draws a material picker for one domain and reports whether the
// selection changed. An "Edit" button beside it opens the material editor on
// whatever is named, which is what makes the row a way in rather than a
// dead end.
func materialCombo(label string, name *string, domain material.Domain) bool {
	// Not cached. The list is a walk of a map with tens of entries in it,
	// filtered by domain — cheaper than the bookkeeping a cache would need to
	// be invalidated correctly from the material editor, a Claude reload, and
	// a project switch, and there is one of these rows on screen at a time.
	names := material.NamesFor(domain)

	preview := *name
	if preview == "" {
		preview = "(none)"
	}

	changed := false
	imgui.SetNextItemWidth(170)
	if imguibackend.BeginCombo(label, preview) {
		if imguibackend.SelectableV("(none)", *name == "", 0, imgui.Vec2{}) && *name != "" {
			*name = ""
			changed = true
		}
		known := false
		for _, n := range names {
			if n == *name {
				known = true
			}
			if imguibackend.SelectableV(n, n == *name, 0, imgui.Vec2{}) && n != *name {
				*name = n
				changed = true
			}
		}
		if !known && *name != "" {
			// Either a material of another domain, or one whose file has gone.
			// Saying which is more use than marking both "missing".
			note := " (missing)"
			if material.Exists(*name) {
				note = " (not a " + material.DomainName(domain) + " material)"
			}
			imguibackend.SelectableV(*name+note, true, 0, imgui.Vec2{})
		}
		imguibackend.EndCombo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A .fmat this wears. Its effects are overlaid on the settings\n" +
			"here rather than replacing them, so naming one does not lose\n" +
			"the texture, alpha or tint set below.")
	}
	if *name != "" {
		imgui.SameLine()
		if imgui.Button("Edit##" + label) {
			OpenMaterialEditorOn(*name)
		}
	}
	return changed
}
