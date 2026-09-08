package editor

// editor_ui_ops.go — the structural edits: add, delete, duplicate, reorder,
// reparent.
//
// Every one of these takes an undo snapshot first and marks the document dirty
// afterwards. They work on IDs rather than pointers wherever they can, because
// a Clone taken for undo invalidates every pointer into the tree.

import (
	"foundry/internal/ui"
)

// uiAddNode inserts a new node of a kind as a child of the selection, or of the
// root when nothing is selected, and selects it.
//
// A new widget is created at a modest pinned size rather than at the format's
// stretch-to-fill default: dropping a button into a menu and having it cover the
// whole menu is technically correct and practically useless.
func uiAddNode(kind ui.Kind) {
	if uiDoc == nil {
		return
	}
	UIPushUndo()

	parent := uiSelected()
	if parent == nil || parent.Kind == ui.KindInclude {
		parent = &uiDoc.Root
	}

	n := ui.NewNode(uiDoc.UniqueNodeID(ui.KindName(kind)), kind)
	n.Style = ui.ActiveTheme.StyleFor(kind)
	n.Text.Color = ui.ActiveTheme.TextColor
	uiDefaultRectFor(&n, kind)
	if kind == ui.KindText || kind == ui.KindButton {
		n.Text.Value = ui.KindName(kind)
		n.Text.HAlign = ui.AlignCenter
	}

	parent.Children = append(parent.Children, n)
	uiSel = n.ID
	uiEdited()
	uiStatus = "Added " + ui.KindName(kind)
}

// uiDefaultRectFor gives a new node a size worth seeing.
func uiDefaultRectFor(n *ui.Node, kind ui.Kind) {
	w, h := float32(220), float32(56)
	switch kind {
	case ui.KindText:
		w, h = 240, 40
	case ui.KindToggle:
		w, h = 40, 40
	case ui.KindBar, ui.KindSlider:
		w, h = 260, 24
	case ui.KindPanel, ui.KindImage, ui.KindInclude:
		w, h = 320, 200
	case ui.KindVideo:
		// Sixteen by nine, because that is what a video is. A square default
		// would letterbox every clip anyone dropped into it and read as the
		// fit mode being wrong.
		w, h = 356, 200
	}
	n.Rect.AnchorMin = [2]float32{0, 0}
	n.Rect.AnchorMax = [2]float32{0, 0}
	n.Rect.Offset = [4]float32{40, 40, -(40 + w), -(40 + h)}
}

// uiDeleteSelection removes the selected node and everything under it.
func uiDeleteSelection() {
	if uiDoc == nil || uiSel == "" || uiSel == uiDoc.Root.ID {
		return
	}
	sel := uiSelected()
	if sel == nil || sel.FromInclude() {
		return
	}
	UIPushUndo()

	parent, idx := uiLocate(uiSel)
	if parent == nil {
		UIPopUndo()
		return
	}
	parent.Children = append(parent.Children[:idx], parent.Children[idx+1:]...)

	// Select what took its place, or the parent when it was the last child, so
	// the panel is never left pointing at nothing after a delete.
	switch {
	case idx < len(parent.Children):
		uiSel = parent.Children[idx].ID
	case len(parent.Children) > 0:
		uiSel = parent.Children[len(parent.Children)-1].ID
	default:
		uiSel = parent.ID
	}
	uiEdited()
	uiStatus = "Deleted"
}

// uiDuplicateSelection copies the selected subtree in beside itself.
func uiDuplicateSelection() {
	if uiDoc == nil || uiSel == "" || uiSel == uiDoc.Root.ID {
		return
	}
	sel := uiSelected()
	if sel == nil || sel.FromInclude() {
		return
	}
	UIPushUndo()

	parent, idx := uiLocate(uiSel)
	if parent == nil {
		UIPopUndo()
		return
	}
	clone := sel.Clone()
	uiRenameSubtree(&clone)

	parent.Children = append(parent.Children, ui.Node{})
	copy(parent.Children[idx+2:], parent.Children[idx+1:])
	parent.Children[idx+1] = clone

	uiSel = clone.ID
	uiEdited()
	uiStatus = "Duplicated"
}

// uiRenameSubtree gives every node in a copied subtree a fresh ID.
//
// Every one, not just the root: two nodes sharing an ID make every script
// reference to it a coin flip, and the format refuses to save it at all.
func uiRenameSubtree(n *ui.Node) {
	n.Walk(func(_, c *ui.Node) bool {
		c.ID = uiDoc.UniqueNodeID(uiStripSuffix(c.ID))
		return true
	})
}

// uiStripSuffix removes a trailing _<digits>, so duplicating "btn_2" gives
// "btn_3" rather than "btn_2_2".
func uiStripSuffix(id string) string {
	i := len(id)
	for i > 0 && id[i-1] >= '0' && id[i-1] <= '9' {
		i--
	}
	if i > 1 && i < len(id) && id[i-1] == '_' {
		return id[:i-1]
	}
	return id
}

// uiMoveSelection reorders the selection among its siblings, which is
// reordering the draw: tree order is paint order, and there is no z field to
// mean anything else.
func uiMoveSelection(delta int) {
	if uiDoc == nil || uiSel == "" {
		return
	}
	parent, idx := uiLocate(uiSel)
	if parent == nil {
		return
	}
	dst := idx + delta
	if dst < 0 || dst >= len(parent.Children) {
		return
	}
	UIPushUndo()
	parent.Children[idx], parent.Children[dst] = parent.Children[dst], parent.Children[idx]
	uiEdited()
}

// uiReparent moves a node under a new parent, at the end of its children.
//
// Refuses to move a node into its own subtree, which would detach the whole
// branch from the document and lose it.
func uiReparent(childID, newParentID string) bool {
	if uiDoc == nil || childID == "" || childID == uiDoc.Root.ID || childID == newParentID {
		return false
	}
	child := uiDoc.Find(childID)
	target := uiDoc.Find(newParentID)
	if child == nil || target == nil || target.FromInclude() || child.FromInclude() {
		return false
	}
	if target.Kind == ui.KindInclude {
		return false
	}
	if child.Find(newParentID) != nil {
		return false
	}

	UIPushUndo()
	moved := child.Clone()
	parent, idx := uiLocate(childID)
	if parent == nil {
		UIPopUndo()
		return false
	}
	parent.Children = append(parent.Children[:idx], parent.Children[idx+1:]...)

	// Find the target again: removing the child may have moved it in memory.
	target = uiDoc.Find(newParentID)
	if target == nil {
		UIPopUndo()
		return false
	}
	target.Children = append(target.Children, moved)
	uiSel = moved.ID
	uiEdited()
	return true
}

// uiWrapInPanel puts a new panel between the selection and its parent, taking
// the selection's rect so nothing visibly moves.
func uiWrapInPanel() {
	if uiDoc == nil || uiSel == "" || uiSel == uiDoc.Root.ID {
		return
	}
	sel := uiSelected()
	if sel == nil || sel.FromInclude() {
		return
	}
	UIPushUndo()

	parent, idx := uiLocate(uiSel)
	if parent == nil {
		UIPopUndo()
		return
	}

	wrapper := ui.NewNode(uiDoc.UniqueNodeID("panel"), ui.KindPanel)
	wrapper.Rect = sel.Rect
	wrapper.Style.Color = [4]float32{1, 1, 1, 0}

	inner := sel.Clone()
	// The child now fills the wrapper, which is exactly where it already was.
	inner.Rect = ui.DefaultRectXf()
	wrapper.Children = []ui.Node{inner}

	parent.Children[idx] = wrapper
	uiSel = wrapper.ID
	uiEdited()
	uiStatus = "Wrapped in a panel"
}

// uiLocate finds a node's parent and its index among that parent's children.
func uiLocate(id string) (*ui.Node, int) {
	if uiDoc == nil {
		return nil, -1
	}
	var parent *ui.Node
	idx := -1
	uiDoc.Root.Walk(func(p, n *ui.Node) bool {
		if idx >= 0 {
			return false
		}
		if n.ID == id && p != nil {
			for i := range p.Children {
				if p.Children[i].ID == id {
					parent, idx = p, i
					break
				}
			}
		}
		return idx < 0
	})
	return parent, idx
}
