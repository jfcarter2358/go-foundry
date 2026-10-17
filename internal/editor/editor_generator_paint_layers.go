package editor

// editor_generator_paint_layers.go — the paint bar's Layer menu: rename or
// delete the active layer (docs/guide/36-terrain-paint.md).
//
// Both are document edits (one undo step each, on the document's stack, as
// New layer… is) and both go through procgen's paint_edit.go, which keeps
// the stages' mask params in step: a rename rewrites every param that named
// the layer, a delete clears them, so the document still validates. The
// pixels follow a rename as a copy-on-write clone; a deleted layer's pixels
// stay in memory undeclared, so undoing the delete brings them back. Its
// folder beside the .fgen is left on disk.

import (
	"strings"

	"foundry/internal/procgen/paint"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// genPaintLayerMenu is the Layer button and its popup.
func genPaintLayerMenu() {
	if imgui.Button("Layer…##genpaintlayermenu") {
		genPaint.renameName = genPaint.layer
		imgui.OpenPopup("##genpaintlayerpop")
	}
	editorMenuTooltip("Rename or delete the active layer.")
	if !imgui.BeginPopup("##genpaintlayerpop") {
		return
	}
	defer imgui.EndPopup()
	if genDoc == nil || genDoc.PaintDef(genPaint.layer) == nil {
		imgui.Text("No layer selected")
		return
	}
	users := genDoc.PaintLayerUsers(genPaint.layer)
	if len(users) > 0 {
		imgui.Text("Read by: " + strings.Join(users, ", "))
	} else {
		imgui.Text("No stage reads this layer yet")
	}
	imgui.Separator()
	imgui.SetNextItemWidth(160)
	imgui.InputTextV("##genpaintrename", &genPaint.renameName, 0, nil)
	imgui.SameLine()
	valid := paint.ValidName(genPaint.renameName) && genPaint.renameName != genPaint.layer &&
		genDoc.PaintDef(genPaint.renameName) == nil
	if imgui.Button("Rename##genpaintrenameok") && valid {
		genPaintRenameLayer(genPaint.layer, genPaint.renameName)
		imgui.CloseCurrentPopup()
	}
	editorMenuTooltip("Rename the layer, and every stage param that reads it.")
	imgui.Separator()
	if imgui.Button("Delete layer##genpaintdelete") {
		genPaintDeleteLayer(genPaint.layer)
		imgui.CloseCurrentPopup()
	}
	editorMenuTooltip("Drop the layer's declaration and clear the params that read it (Ctrl+Z on the document brings it back). Its folder stays on disk.")
}

// genPaintRenameLayer renames a layer as an undoable document edit.
func genPaintRenameLayer(from, to string) {
	GeneratorPushUndo()
	n, err := genDoc.RenamePaintLayer(from, to)
	if err != nil {
		generatorUndoStack.pop()
		genStatusf("Rename failed: %v", err)
		return
	}
	genPaint.layer = to
	genPaintFreeOverlay()
	genAfterEdit()
	genStatusf("Paint layer %s renamed %s (%d params updated)", from, to, n)
}

// genPaintDeleteLayer deletes a layer as an undoable document edit.
func genPaintDeleteLayer(name string) {
	GeneratorPushUndo()
	n, err := genDoc.DeletePaintLayer(name)
	if err != nil {
		generatorUndoStack.pop()
		genStatusf("Delete failed: %v", err)
		return
	}
	genPaint.layer = ""
	if len(genDoc.Paint) > 0 {
		genPaint.layer = genDoc.Paint[0].Name
	}
	genPaintFreeOverlay()
	genAfterEdit()
	genStatusf("Paint layer %s deleted (%d params cleared)", name, n)
}
