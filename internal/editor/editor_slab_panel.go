package editor

// editor_slab_panel.go — the controls for a floor's or a ceiling's thickness.
//
// A slab (world/slab.go) is the far side of a surface: the underside of a
// floor, the top of a ceiling, and the rim joining either to the surface it
// hangs off. It is a group inside its surface's group, beside Shape, Texture UV
// and Skirt, because everything about a floor belongs under Floor — including
// the part of it you only ever see from the room below.
//
// The group is drawn whether or not the surface has any thickness yet. A
// control that only appears once the feature is switched on is a feature nobody
// finds, and Thickness is the switch: at zero there is nothing to texture, so
// the rest of the block is replaced by the one line that says so.

import (
	"foundry/internal/world"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// slabPanelLabel is what a surface's slab is called in the panel and in the
// viewport's status line. A floor's far face is underneath it and a ceiling's
// is on top of it, and calling both of them "slab" would leave the author
// working out which side of what they were about to retexture.
func slabPanelLabel(kind string) string {
	if kind == "ceiling" {
		return "Top Side"
	}
	return "Underside"
}

// editorSlabPanel draws one surface's slab group. kind is the *surface's*
// ("floor" or "ceiling"), not the slab face's.
func editorSlabPanel(ch *world.Chunk, s *world.Sector, sl *world.Slab, kind, idSuffix string) {
	id := kind + "slab" + idSuffix
	focus := slabPanelFocus(kind)

	editorFocusOpen(focus)
	editorPropGroup(slabPanelLabel(kind)+"##grp"+id, 0, func() {
		editorFocusScroll(focus)

		changed := false

		if editorDragFloat("Thickness##t"+id, &sl.Thickness, 0, 0, "%.3f") {
			// A negative thickness would put the far face on the wrong side of
			// the surface — a solid turned inside out — so the control stops at
			// zero rather than letting the level describe one.
			if sl.Thickness < 0 {
				sl.Thickness = 0
			}
			// Thinned away to nothing while its far face was being shaped:
			// there is no face left in the viewport to shape, so the mode goes
			// with it. The mesh is kept, so dragging the thickness back brings
			// the shape back with it.
			if !sl.HasThickness() && surfaceEditActive() &&
				Editor.SurfaceFace == surfaceFaceSel(Editor.ActiveChunkIdx, Editor.ActiveSectorIdx, slabFaceKind(kind)) {
				ExitSurfaceEdit()
			}
			changed = true
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("How far the " + slabPanelLabel(kind) + " sits from the " + kind + ".\n" +
				"0 leaves it the bare plane it has always been.")
		}

		if !sl.HasThickness() {
			editorTextMutedWrapped("No thickness: this " + kind + " is a single plane. " +
				"Give it one and it grows a face on the far side and a rim round the edge, " +
				"both of them clickable in the 3D view.")
			return
		}

		changed = imgui.Checkbox("Hide Face##hid"+id, &sl.Hidden) || changed
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Stop drawing the far face, keeping the rim — for a slab whose\n" +
				"underside is already covered by the storey below.")
		}
		changed = textureCombo("Texture##tex"+id, &sl.Texture, true) || changed
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Left empty, the " + slabPanelLabel(kind) + " takes the " + kind + "'s own texture.")
		}
		changed = textureCombo("Blend Texture##btex"+id, &sl.BlendTexture, true) || changed
		changed = textureCombo("Blend Mask##bmask"+id, &sl.BlendMask, true) || changed
		changed = textureCombo("Rim Texture##rtex"+id, &sl.RimTexture, true) || changed
		if imgui.IsItemHovered() {
			imgui.SetTooltip("The band round the edge of the slab.\n" +
				"Left empty, it takes the texture above.")
		}
		changed = imgui.Checkbox("Rim Across Portals##rap"+id, &sl.RimAcrossPortals) || changed
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Off by default: a portal edge gets no rim, because the slab\n" +
				"continues into the sector next door and its own rim would sit\n" +
				"in the same place. Switch this on to seal the edge anyway.")
		}
		changed = imgui.Checkbox("Double Sided##ds"+id, &sl.DoubleSided) || changed
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Off by default: the far face is the back of a solid, and the\n" +
				"side of it facing into the slab is never seen.")
		}

		if editorUVControls(slabPanelLabel(kind)+idSuffix, &sl.UVOffset, &sl.UVScale, &sl.UVRotation) {
			changed = true
		}

		editorSlabShapeControls(ch, s, sl, kind)

		editorTextMuted("Alpha and smoothing follow the " + kind + "'s.")
		if imgui.IsItemHovered() {
			imgui.SetTooltip("A slab is the same piece of material as the surface it hangs\n" +
				"off, so a half-transparent floor over an opaque underside is\n" +
				"not something a level can say.")
		}

		if changed {
			// The slab is built by the sector's own reload rather than by the
			// chunk-level pass a skirt needs: nothing about it depends on a
			// neighbouring sector.
			_ = world.ReloadSector(ch, s)
		}
	})
}

// editorSlabShapeControls is the far face's shape: whether it follows the
// surface it hangs off, the way into shaping it, and the way back.
//
// The way back matters more than it looks. Surface mode's own "Reset to flat"
// means a flat plane, and for a slab that is a third state rather than the
// original one — an underside that is flat *and* detached, which is not what
// somebody who wanted to undo their shaping is asking for. Following again is
// emptying the mesh, and only this button says so.
func editorSlabShapeControls(ch *world.Chunk, s *world.Sector, sl *world.Slab, kind string) {
	slabSel := surfaceFaceSel(Editor.ActiveChunkIdx, Editor.ActiveSectorIdx, slabFaceKind(kind))

	if !sl.Shaped() {
		editorTextMuted("Shape: follows the " + kind + ".")
		if imgui.IsItemHovered() {
			imgui.SetTooltip("The " + slabPanelLabel(kind) + " is the " + kind + "'s own shape\n" +
				"offset by the thickness, so carving one carves both.\n" +
				"Shaping it below detaches the two.")
		}
	} else {
		editorTextMuted("Shape: its own.")
		if imgui.Button("Follow the " + editor3DKindLabel(kind) + "##slabfollow" + kind) {
			EditorPushUndo()
			// Shaping mode indexes into the mesh about to be emptied, so it
			// cannot survive this — and leaving it on would put the editor in
			// the one state EnterSurfaceEdit exists to prevent: shaping a mesh
			// with nothing in it.
			if surfaceEditActive() && Editor.SurfaceFace == slabSel {
				ExitSurfaceEdit()
			}
			sl.Mesh = world.SurfaceMesh{}
			_ = world.ReloadSector(ch, s)
			editorSetStatus("%s follows the %s again", slabPanelLabel(kind), kind)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Throw away the shape given to the " + slabPanelLabel(kind) + "\n" +
				"and go back to mirroring the " + kind + ".")
		}
	}

	editorSurfacePanel(ch, s, slabSel)
}
