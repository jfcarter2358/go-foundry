package editor

// editor_surface_panel.go — surface mode's side panel.
//
// Two front ends onto the same operations, the way the model editor keeps its
// panel and its context menu in step (editor_model_actions.go): every button
// here calls the function the keyboard shortcut calls, so a shortcut that works
// and a button that does not — or one that forgets to push an undo entry — is
// not a thing that can happen.
//
// The panel earns its place mostly by being discoverable. Surface mode is
// almost entirely keyboard-driven once you know it, and nothing in the viewport
// says that E extrudes.

import (
	"fmt"
	"foundry/internal/world"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorSurfacePanel draws the shaping controls for the face named by sel. It
// is the whole panel while surface mode is on, and a single button — the way
// in — while it is not.
func editorSurfacePanel(ch *world.Chunk, s *world.Sector, sel faceSel) {
	imgui.Separator()

	if !surfaceEditActive() || Editor.SurfaceFace != sel {
		if imgui.Button(fmt.Sprintf("Shape Surface##surfenter%s%d", sel.kind, sel.idx)) {
			EnterSurfaceEdit(sel)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Edit this surface as a mesh: place and connect points,\n" +
				"extrude faces into bumps and recesses, open holes.")
		}
		if mesh := s.SurfaceMeshFor(sel.kind, sel.idx); mesh != nil && !mesh.IsEmpty() {
			imgui.SameLine()
			imgui.Text(fmt.Sprintf("(%d faces)", len(mesh.Faces)))
		}
		return
	}

	_, _, mesh, _, ok := surfaceEditTarget()
	if !ok {
		return
	}

	imgui.PushStyleColor(imgui.StyleColorText, imgui.Vec4{X: 0, Y: 1, Z: 0.5, W: 1})
	imgui.Text(fmt.Sprintf("Shaping %s — %d points, %d faces", sel.kind, len(mesh.Points), len(mesh.Faces)))
	imgui.PopStyleColor()
	if Editor.SurfaceStatus != "" {
		imgui.Text(Editor.SurfaceStatus)
	}

	// Selection mode.
	for _, m := range []surfaceSelMode{surfaceSelPoint, surfaceSelEdge, surfaceSelFace} {
		if m != surfaceSelPoint {
			imgui.SameLine()
		}
		label := fmt.Sprintf("%s [%d]##surfmode%d", m, int(m)+1, int(m))
		if imgui.RadioButton(label, Editor.SurfaceMode == m) {
			surfaceSetSelMode(m)
		}
	}

	imgui.Separator()

	if imgui.Button("Extrude [E]##surfext") {
		surfaceExtrude()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Push the selected faces out of the surface, walling in the gap.\n" +
			"Drag the green depth arm to set how far. Press again to stack another step.")
	}
	imgui.SameLine()
	if imgui.Button("Inset [I]##surfinset") {
		surfaceInset()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Shrink the selected faces, leaving a ring around them —\n" +
			"the usual first step in cutting a doorway or a window.")
	}
	imgui.SameLine()
	if imgui.Button("Subdivide [B]##surfsub") {
		surfaceSubdivide()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Split the selected faces at their edge midpoints.\n" +
			"B rather than S, which flies the camera backwards.")
	}

	if imgui.Button("Connect [C]##surfconn") {
		surfaceConnect()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Join two selected points with a new edge, splitting the face between them.")
	}
	imgui.SameLine()
	if imgui.Button("Open / Close [H]##surfhole") {
		surfaceToggleHole()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Mark the selected faces a hole through the surface, or fill them back in.\n" +
			"An open face on a wall is what a body can walk through.")
	}
	imgui.SameLine()
	if imgui.Button("Delete##surfdel") {
		surfaceDeleteFaces()
	}

	imgui.Separator()

	// Per-face properties, for the primary selection.
	if Editor.SurfaceMode == surfaceSelFace && len(Editor.SurfaceFaces) > 0 {
		fi := Editor.SurfaceFaces[0]
		if fi >= 0 && fi < len(mesh.Faces) {
			f := &mesh.Faces[fi]
			changed := false
			imgui.Text(fmt.Sprintf("Face %d (%d corners)", fi, len(f.Verts)))
			changed = textureCombo(fmt.Sprintf("Texture##surfftex%d", fi), &f.Texture, true) || changed
			if imgui.IsItemHovered() {
				imgui.SetTooltip("Empty inherits the surface's own texture.\nA face with its own becomes a separate draw.")
			}
			changed = editorDragFloat(fmt.Sprintf("Alpha##surffa%d", fi), &f.Alpha, 0, 1, "%.2f") || changed
			changed = imgui.Checkbox(fmt.Sprintf("Smooth##surffs%d", fi), &f.Smooth) || changed
			hole := f.Hole
			if imgui.Checkbox(fmt.Sprintf("Open (hole)##surffh%d", fi), &hole) {
				f.Hole = hole
				changed = true
			}
			if changed {
				surfaceRebuild(ch, s)
			}
		}
	}

	// Per-point depth, for the primary selection — the keyboard-free way to set
	// an exact value, which a drag cannot give you.
	if Editor.SurfaceMode == surfaceSelPoint && len(Editor.SurfaceVerts) > 0 {
		pi := Editor.SurfaceVerts[0]
		if pi >= 0 && pi < len(mesh.Points) {
			p := &mesh.Points[pi]
			imgui.Text(fmt.Sprintf("Point %d", pi))
			changed := editorDragFloat(fmt.Sprintf("Depth##surfpd%d", pi), &p.Depth, 0, 0, "%.3f")
			if p.Boundary < 0 {
				changed = editorDragFloat(fmt.Sprintf("U##surfpu%d", pi), &p.U, 0, 0, "%.3f") || changed
				changed = editorDragFloat(fmt.Sprintf("V##surfpv%d", pi), &p.V, 0, 0, "%.3f") || changed
			} else {
				imgui.Text(fmt.Sprintf("Outline corner %d — position follows the sector", p.Boundary))
			}
			if changed {
				surfaceRebuild(ch, s)
			}
		}
	}

	imgui.Separator()
	if imgui.Button("Reset to flat##surfreset") {
		surfaceResetMesh()
	}
	imgui.SameLine()
	if imgui.Button("Done [Esc]##surfdone") {
		ExitSurfaceEdit()
	}
}
