package main

// editor_context_menu.go — the machinery both viewport context menus are built
// on: the model editor's (editor_model_context_menu.go) and the 3D level
// editor's (editor_3d_context_menu.go).
//
// Two things are shared, and they are the two things that would otherwise drift:
//
//   - the item tree. A menu is built as data and rendered separately, which is
//     what makes "what does this selection offer?" a question a test can ask
//     without a GL context or an ImGui frame — and what keeps the two menus
//     looking and behaving alike, since they render through the same code.
//
//   - the gesture. The right button was already spoken for in both viewports:
//     it is mouse-look. So the two meanings are told apart the same way in
//     both — a drag looks around, a press and release without travel opens the
//     menu — because a gesture that meant different things in the two windows
//     would be worse than no menu at all.
//
// What each editor supplies for itself is the interesting half: which rows
// exist for which selection, and what they do.

import (
	"fmt"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// ctxMenuItem is one row. The zero value is an enabled, unticked, plain row;
// the fields that are set decide what it renders as:
//
//	Separator            a divider
//	Sub non-empty        a submenu
//	Custom non-nil       a widget drawn inline (a slider, say)
//	Do nil, Label set    a caption — the menu's own heading
//	otherwise            a clickable item
type ctxMenuItem struct {
	Label     string
	Shortcut  string
	Tooltip   string
	Disabled  bool
	Checked   bool
	Separator bool
	Sub       []ctxMenuItem
	Do        func()
	Custom    func()
}

// ctxDragSlopPx is how far the cursor may travel between a right-button press
// and its release and still count as a click rather than a look-around.
const ctxDragSlopPx = 4

// ctxGesture is the right-button gesture in progress for one viewport.
//
// down is only set for presses the viewport actually received — a press ImGui
// captured never arms it, so releasing over the viewport after clicking a panel
// cannot open a menu.
type ctxGesture struct {
	down   bool
	moved  bool
	pressX int32
	pressY int32

	pending bool // open the popup on the next frame
	menuX   int32
	menuY   int32
}

// press arms the gesture at a right-button press on the viewport.
func (g *ctxGesture) press(mx, my int32) {
	g.down, g.moved = true, false
	g.pressX, g.pressY = mx, my
}

// motion is fed every mouse-motion event, and disarms the menu once the cursor
// has travelled far enough for the gesture to be a look.
func (g *ctxGesture) motion(mx, my int32) {
	if !g.down || g.moved {
		return
	}
	dx, dy := mx-g.pressX, my-g.pressY
	if dx*dx+dy*dy > ctxDragSlopPx*ctxDragSlopPx {
		g.moved = true
	}
}

// release ends the gesture and reports whether it was a click rather than a
// drag. The caller decides what a click means — usually: select what is under
// the cursor if nothing is, then open.
func (g *ctxGesture) release() bool {
	armed := g.down && !g.moved
	g.down = false
	return armed
}

// open asks for the menu at (mx, my) on the next frame. It cannot be opened
// from the event path, which runs outside the ImGui frame the popup belongs to.
func (g *ctxGesture) open(mx, my int32) {
	g.pending = true
	g.menuX, g.menuY = mx, my
}

// ctxMenuFrame draws one viewport's popup, building its rows only on the frames
// it is actually open — building walks armatures and texture lists, and there
// is no reason to pay for that while nobody is looking at it.
//
// Call it once per frame, after the panels, so the rows are built from the same
// state they show.
func ctxMenuFrame(id string, g *ctxGesture, build func() []ctxMenuItem) {
	if g.pending {
		imgui.OpenPopup(id)
		g.pending = false
	}
	if !imgui.IsPopupOpen(id) {
		return
	}
	imgui.SetNextWindowPos(imgui.Vec2{X: float32(g.menuX), Y: float32(g.menuY)})
	if imgui.BeginPopup(id) {
		ctxDrawMenuItems(build())
		imgui.EndPopup()
	}
}

// ctxDrawMenuItems renders one level of the tree. The index is folded into each
// row's ImGui id so two rows that read the same — two bones with the same name
// in a half-renamed armature, say — stay distinct widgets.
func ctxDrawMenuItems(items []ctxMenuItem) {
	for i := range items {
		it := &items[i]
		label := fmt.Sprintf("%s##ctxrow%d", it.Label, i)
		switch {
		case it.Separator:
			imgui.Separator()
		case it.Custom != nil:
			it.Custom()
		case len(it.Sub) > 0:
			if imgui.BeginMenuV(label, !it.Disabled) {
				ctxDrawMenuItems(it.Sub)
				imgui.EndMenu()
			}
		case it.Do == nil:
			imgui.Text(it.Label)
		default:
			if imgui.MenuItemV(label, it.Shortcut, it.Checked, !it.Disabled) {
				it.Do()
			}
		}
		if it.Tooltip != "" && imgui.IsItemHovered() {
			imgui.SetTooltip(it.Tooltip)
		}
	}
}

// ctxTextureMenuItems is the texture list as menu rows, ticked at current.
// Both menus assign textures, and both want the same list in the same order.
func ctxTextureMenuItems(current string, set func(string)) []ctxMenuItem {
	names := sortedTextureNames()
	items := make([]ctxMenuItem, 0, len(names))
	for _, n := range names {
		name := n
		items = append(items, ctxMenuItem{
			Label:   name,
			Checked: name == current,
			Do:      func() { set(name) },
		})
	}
	if len(items) == 0 {
		items = append(items, ctxMenuItem{Label: "No textures found"})
	}
	return items
}
