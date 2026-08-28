package editor

// editor_ui_canvas.go — the live preview and the overlays on it.
//
// The preview is drawn by internal/uidraw, the same pass the game uses, at the
// design resolution the toolbar names rather than at this window's size. That
// is the whole reason a screen looks the same here as it does in the level: one
// solver, one batcher, one set of numbers.
//
// The overlays — grid, safe area, selection outline, handles, anchors — go into
// the *same draw list* as the content, rather than into an imgui draw list over
// the top. Two reasons: they are then clipped and scaled by exactly the
// transform the content is, so a handle cannot drift away from the edge it
// belongs to; and it means every frame of the editor is exercising the pass
// that ships.

import (
	"foundry/internal/ui"
	"foundry/internal/uidraw"
	"foundry/internal/uiwin"

	"github.com/go-gl/mathgl/mgl32"
)

// Overlay colours, in the editor's own palette rather than the theme's: these
// are chrome, and they have to stay legible over whatever the document is.
var (
	uiGridColor      = [4]float32{1, 1, 1, 0.05}
	uiGridMajorColor = [4]float32{1, 1, 1, 0.12}
	uiBoundsColor    = [4]float32{1, 1, 1, 0.35}
	uiSafeAreaColor  = [4]float32{1, 0.8, 0.2, 0.3}
	uiOutlineColor   = [4]float32{0.35, 0.65, 0.95, 0.55}
	uiSelectColor    = [4]float32{1, 0.62, 0.16, 1}
	uiHandleColor    = [4]float32{1, 0.62, 0.16, 1}
	uiAnchorColor    = [4]float32{0.45, 0.85, 0.55, 1}
	uiPivotColor     = [4]float32{1, 1, 1, 0.9}
)

const (
	uiGridStep       = float32(20)
	uiGridMajorEvery = 5

	// Handle and hit sizes are in *window pixels*, converted to document units
	// per frame. A handle that scaled with the zoom would be unusable at either
	// end of the range.
	uiHandlePx       = float32(9)
	uiAnchorPx       = float32(7)
	uiPivotPx        = float32(5)
	uiOutlineThickPx = float32(1)
	uiSelectThickPx  = float32(2)

	// uiSafeAreaInset is the fraction of each edge a television might crop.
	uiSafeAreaInset = float32(0.05)
)

// uiEditorDrawCanvas draws the checkerboard, the document and the overlays.
func uiEditorDrawCanvas(w *uiwin.UIWindow, area ui.Rect) {
	canvas := uiCanvasRect(w)
	if canvas.W <= 0 || canvas.H <= 0 {
		return
	}
	scale := uiView.scale(canvas, area)
	ox, oy := uiView.origin(canvas, area)

	// Everything is built in window pixels and drawn with a scale of 1, so the
	// overlays can be specified in pixels where that is what they mean and in
	// document units where that is.
	uiList.Reset(ui.Rect{W: float32(w.W), H: float32(w.H)})
	uiList.PushClip(canvas)

	doc := ui.Rect{X: ox, Y: oy, W: area.W * scale, H: area.H * scale}
	uiList.AddRect(doc, uidraw.White(), uidraw.PackColor([4]float32{0.06, 0.07, 0.08, 1}))
	if uiShowGrid {
		uiDrawGrid(doc, scale)
	}

	uiDrawDocument(w, area, canvas, scale)

	uiList.AddRectOutline(doc, 1, uidraw.White(), uidraw.PackColor(uiBoundsColor))
	if uiShowSafeArea {
		inset := mgl32.Vec2{doc.W * uiSafeAreaInset, doc.H * uiSafeAreaInset}
		uiList.AddRectOutline(
			ui.Rect{X: doc.X + inset[0], Y: doc.Y + inset[1],
				W: doc.W - 2*inset[0], H: doc.H - 2*inset[1]},
			1, uidraw.White(), uidraw.PackColor(uiSafeAreaColor))
	}

	uiDrawOverlays(canvas, area)
	uiList.PopClip()

	uidraw.Render(&uiList, w.W, w.H, 1)
}

// uiDrawDocument draws the screen itself through the shipping pass, into its own
// list, then transfers the result into the canvas list at the view transform.
//
// A second list rather than building straight into uiList, because the document
// is laid out in its own units and the transform between those and this window's
// pixels is the view — folding that into the projection would also transform the
// overlays, which are specified in pixels.
func uiDrawDocument(w *uiwin.UIWindow, area, canvas ui.Rect, scale float32) {
	var doc uidraw.List
	uidraw.BuildInto(&doc, uiDoc, uiPreview.Interaction(), area, 1)

	ox, oy := uiView.origin(canvas, area)
	uiList.AppendTransformed(&doc, ox, oy, scale)
}

func uiDrawGrid(doc ui.Rect, scale float32) {
	step := uiGridStep * scale
	if step < 4 {
		// Below this the grid is a solid wash rather than a grid.
		return
	}
	minor := uidraw.PackColor(uiGridColor)
	major := uidraw.PackColor(uiGridMajorColor)

	for i, x := 0, doc.X; x <= doc.X+doc.W+0.5; i, x = i+1, x+step {
		col := minor
		if i%uiGridMajorEvery == 0 {
			col = major
		}
		uiList.AddRect(ui.Rect{X: x, Y: doc.Y, W: 1, H: doc.H}, uidraw.White(), col)
	}
	for i, y := 0, doc.Y; y <= doc.Y+doc.H+0.5; i, y = i+1, y+step {
		col := minor
		if i%uiGridMajorEvery == 0 {
			col = major
		}
		uiList.AddRect(ui.Rect{X: doc.X, Y: y, W: doc.W, H: 1}, uidraw.White(), col)
	}
}

// uiDrawOverlays draws every node's outline and the selection's handles.
func uiDrawOverlays(canvas, area ui.Rect) {
	if uiDoc == nil {
		return
	}
	sel := uiSelected()

	if uiShowOutlines {
		outline := uidraw.PackColor(uiOutlineColor)
		uiDoc.Root.Walk(func(parent, n *ui.Node) bool {
			if parent != nil && !n.Culled() {
				uiList.AddRectOutline(uiToScreen(n.Solved(), canvas, area),
					uiOutlineThickPx, uidraw.White(), outline)
			}
			return true
		})
	}

	if sel == nil {
		return
	}
	r := uiToScreen(sel.Solved(), canvas, area)
	uiList.AddRectOutline(r, uiSelectThickPx, uidraw.White(), uidraw.PackColor(uiSelectColor))

	handle := uidraw.PackColor(uiHandleColor)
	for _, h := range uiHandleRects(r) {
		uiList.AddRect(h, uidraw.White(), handle)
	}

	// The anchors, on the parent's rect: that is what they are fractions of,
	// and drawing them on the node's own rect would say something false.
	if parent := uiParentOf(sel); parent != nil {
		pr := uiToScreen(parent.Solved(), canvas, area)
		anchor := uidraw.PackColor(uiAnchorColor)
		for _, a := range uiAnchorRects(sel, pr) {
			uiList.AddRect(a, uidraw.White(), anchor)
		}
	}

	// The pivot, which is where rotation and scale happen from.
	px := r.X + sel.Rect.Pivot[0]*r.W
	py := r.Y + sel.Rect.Pivot[1]*r.H
	pivot := uidraw.PackColor(uiPivotColor)
	uiList.AddRect(ui.Rect{X: px - uiPivotPx, Y: py - 1, W: uiPivotPx * 2, H: 2}, uidraw.White(), pivot)
	uiList.AddRect(ui.Rect{X: px - 1, Y: py - uiPivotPx, W: 2, H: uiPivotPx * 2}, uidraw.White(), pivot)
}

// uiToScreen maps a solved document rect into this window's pixels.
func uiToScreen(r ui.Rect, canvas, area ui.Rect) ui.Rect {
	s := uiView.scale(canvas, area)
	ox, oy := uiView.origin(canvas, area)
	return ui.Rect{X: ox + r.X*s, Y: oy + r.Y*s, W: r.W * s, H: r.H * s}
}

// uiHandleRects is the eight resize grips, in the order uiHandle names them.
func uiHandleRects(r ui.Rect) [8]ui.Rect {
	h := uiHandlePx
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	pts := [8]mgl32.Vec2{
		{r.X, r.Y}, {cx, r.Y}, {r.X + r.W, r.Y},
		{r.X, cy}, {r.X + r.W, cy},
		{r.X, r.Y + r.H}, {cx, r.Y + r.H}, {r.X + r.W, r.Y + r.H},
	}
	var out [8]ui.Rect
	for i, p := range pts {
		out[i] = ui.Rect{X: p[0] - h/2, Y: p[1] - h/2, W: h, H: h}
	}
	return out
}

// uiAnchorRects is the four anchor markers on the parent's rect.
func uiAnchorRects(n *ui.Node, parent ui.Rect) [4]ui.Rect {
	a := uiAnchorPx
	xs := [2]float32{
		parent.X + n.Rect.AnchorMin[0]*parent.W,
		parent.X + n.Rect.AnchorMax[0]*parent.W,
	}
	ys := [2]float32{
		parent.Y + n.Rect.AnchorMin[1]*parent.H,
		parent.Y + n.Rect.AnchorMax[1]*parent.H,
	}
	return [4]ui.Rect{
		{X: xs[0] - a/2, Y: ys[0] - a/2, W: a, H: a},
		{X: xs[1] - a/2, Y: ys[0] - a/2, W: a, H: a},
		{X: xs[0] - a/2, Y: ys[1] - a/2, W: a, H: a},
		{X: xs[1] - a/2, Y: ys[1] - a/2, W: a, H: a},
	}
}

// uiParentOf finds a node's parent in the document, or nil for the root.
func uiParentOf(n *ui.Node) *ui.Node {
	if uiDoc == nil || n == nil {
		return nil
	}
	var found *ui.Node
	uiDoc.Root.Walk(func(parent, cur *ui.Node) bool {
		if cur == n {
			found = parent
		}
		return found == nil
	})
	return found
}
