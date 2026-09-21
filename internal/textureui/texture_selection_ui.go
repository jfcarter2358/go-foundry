package textureui

// texture_selection_ui.go — the canvas side of the texture editor's selection:
// the Rect Select and Lasso gestures, dragging a selection's contents, the
// scale handles on its bounding box, and the marching-ants outline. The selection itself, and everything it does to the
// document, lives in internal/texture (texture_selection.go).

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"

	"foundry/internal/texture"
)

// Selection-gesture state, alongside texEditorPainting (which is set for the
// whole of any select-tool drag, so the shortcuts' FinishStroke closes it).
var (
	texEditorMoving   bool               // this drag moves the selection's contents
	texEditorScaling  = -1               // handle this drag scales by, or -1
	texEditorSelMode  texture.SelectMode // how this drag's selection combines
	texEditorSelStart [2]int             // texel the drag started on
	texEditorSelLast  [2]int             // texel it is on now
	texEditorMoveLast [2]int             // last offset handed to MoveBy
	texEditorLasso    []mgl32.Vec2       // the lasso so far, in texel units
	texEditorScaleAt  mgl32.Vec2         // cursor (texel units) the scale started at
	texEditorScaleBox [4]float32         // selection box the scale started with
	texEditorScaleOut [4]int             // last edge offsets handed to ScaleEdges
)

// texEditorHandleSelectInput drives the select tools from the cursor's
// position in texel units (not floored: the lasso follows the cursor inside a
// texel) and the left button.
//
// A press on one of the selection box's handles scales its contents (Shift on
// a corner keeps the proportions). A press inside the selection with no
// modifier picks its contents up and drags them; anywhere else it draws a new selection, combined with the old
// one by Shift (add) or Alt/Ctrl (subtract). A click that doesn't drag, with no
// modifier, clears the selection — the usual way out of one.
func texEditorHandleSelectInput(ed *texture.TexEditorState, fx, fy, zoom float32, active bool) {
	leftDown := imgui.IsMouseDown(0) && active
	tx, ty := int(math.Floor(float64(fx))), int(math.Floor(float64(fy)))

	if leftDown && !texEditorPainting {
		texEditorPainting = true
		texEditorSelStart = [2]int{tx, ty}
		texEditorSelLast = texEditorSelStart
		texEditorMoveLast = [2]int{}
		texEditorLasso = nil
		if h := texEditorHandleAt(ed, fx, fy, zoom); h >= 0 && ed.BeginScale() {
			texEditorScaling = h
			texEditorScaleAt = mgl32.Vec2{fx, fy}
			texEditorScaleBox, _ = texEditorSelectionBox(ed)
			texEditorScaleOut = [4]int{}
			return
		}
		mods := sdl.GetModState()
		switch {
		case mods&sdl.KMOD_SHIFT != 0:
			texEditorSelMode = texture.SelectAdd
		case mods&(sdl.KMOD_ALT|sdl.KMOD_CTRL) != 0:
			texEditorSelMode = texture.SelectSubtract
		default:
			texEditorSelMode = texture.SelectReplace
		}
		texEditorMoving = texEditorSelMode == texture.SelectReplace && ed.InSelection(tx, ty) && ed.BeginMove()
		if ed.Tool == texture.ToolSelectLasso && !texEditorMoving {
			texEditorLasso = []mgl32.Vec2{{fx, fy}}
		}
		return
	}

	if leftDown {
		texEditorSelLast = [2]int{tx, ty}
		switch {
		case texEditorScaling >= 0:
			b := texEditorScaleBox
			out := scaleHandleEdges(texEditorScaling,
				int(math.Round(float64(fx-texEditorScaleAt.X()))), int(math.Round(float64(fy-texEditorScaleAt.Y()))),
				int(b[2]-b[0]), int(b[3]-b[1]), sdl.GetModState()&sdl.KMOD_SHIFT != 0)
			if out != texEditorScaleOut {
				ed.ScaleEdges(out[0], out[1], out[2], out[3])
				texEditorScaleOut = out
			}
		case texEditorMoving:
			d := [2]int{tx - texEditorSelStart[0], ty - texEditorSelStart[1]}
			if d != texEditorMoveLast {
				ed.MoveBy(d[0], d[1])
				texEditorMoveLast = d
			}
		case texEditorLasso != nil:
			// A point per quarter texel is as fine as the rasterised mask can
			// tell apart, and keeps a long slow drag from piling up thousands.
			p := mgl32.Vec2{fx, fy}
			if p.Sub(texEditorLasso[len(texEditorLasso)-1]).Len() >= 0.25 {
				texEditorLasso = append(texEditorLasso, p)
			}
		}
		return
	}

	if !texEditorPainting {
		return
	}
	// Released.
	texEditorPainting = false
	switch {
	case texEditorScaling >= 0:
		ed.EndScale()
	case texEditorMoving:
		ed.EndMove()
	case ed.Tool == texture.ToolSelectLasso:
		if len(texEditorLasso) >= 3 {
			ed.Select(ed.LassoMask(texEditorLasso), texEditorSelMode)
		} else if texEditorSelMode == texture.SelectReplace {
			ed.Deselect()
		}
	default:
		if texEditorSelLast == texEditorSelStart && texEditorSelMode == texture.SelectReplace {
			ed.Deselect()
		} else {
			ed.SelectRect(texEditorSelStart[0], texEditorSelStart[1], texEditorSelLast[0], texEditorSelLast[1], texEditorSelMode)
		}
	}
	texEditorMoving = false
	texEditorScaling = -1
	texEditorLasso = nil
}

// ---------- scale handles ----------

// texEditorHandles are the eight handles on the selection box, as which side
// each moves on x and y: -1 the left/top edge, +1 the right/bottom, 0 neither.
// Corners come first so that on a box too small to hold them apart, the
// corner — which scales both ways — wins.
var texEditorHandles = [8][2]int{
	{-1, -1}, {1, -1}, {1, 1}, {-1, 1},
	{0, -1}, {1, 0}, {0, 1}, {-1, 0},
}

const (
	texEditorHandleHalf = 4  // half a handle's side, in screen pixels
	texEditorHandleGrab = 7  // how near the cursor must be to grab one
	texEditorHandleRoom = 24 // shortest side, on screen, that shows edge handles
)

// texEditorHandlePos is where handle h sits on box, in texel units, and
// whether it is shown at this zoom: a side too short on screen to hold its
// middle handle apart from the corners goes without one.
func texEditorHandlePos(box [4]float32, h int, zoom float32) (mgl32.Vec2, bool) {
	pick := func(side int, lo, hi float32) float32 {
		switch side {
		case -1:
			return lo
		case 1:
			return hi
		}
		return (lo + hi) / 2
	}
	s := texEditorHandles[h]
	if s[0] == 0 && (box[2]-box[0])*zoom < texEditorHandleRoom ||
		s[1] == 0 && (box[3]-box[1])*zoom < texEditorHandleRoom {
		return mgl32.Vec2{}, false
	}
	return mgl32.Vec2{pick(s[0], box[0], box[2]), pick(s[1], box[1], box[3])}, true
}

// texEditorHandleAt is the handle under the cursor (texel units), or -1.
func texEditorHandleAt(ed *texture.TexEditorState, fx, fy, zoom float32) int {
	box, ok := texEditorSelectionBox(ed)
	if !ok {
		return -1
	}
	for h := range texEditorHandles {
		p, shown := texEditorHandlePos(box, h, zoom)
		if !shown {
			continue
		}
		if abs32(fx-p.X())*zoom <= texEditorHandleGrab && abs32(fy-p.Y())*zoom <= texEditorHandleGrab {
			return h
		}
	}
	return -1
}

// scaleHandleEdges turns dragging handle h by dx, dy texels into how far each
// edge of a w x h box moves (left, top, right, bottom). keepAspect holds a
// corner drag to the box's proportions, following whichever axis was dragged
// further; it has no say over an edge handle, which scales one axis only.
func scaleHandleEdges(h, dx, dy, w, hgt int, keepAspect bool) [4]int {
	s := texEditorHandles[h]
	if keepAspect && s[0] != 0 && s[1] != 0 && w > 0 && hgt > 0 {
		// The box's new size along each axis, as a multiple of its old one;
		// negative once the handle has crossed the opposite edge.
		fx := float64(w+s[0]*dx) / float64(w)
		fy := float64(hgt+s[1]*dy) / float64(hgt)
		f := fx
		if math.Abs(fy) > math.Abs(fx) {
			f = fy
		}
		dx = s[0] * (int(math.Round(f*float64(w))) - w)
		dy = s[1] * (int(math.Round(f*float64(hgt))) - hgt)
	}
	var out [4]int
	switch s[0] {
	case -1:
		out[0] = dx
	case 1:
		out[2] = dx
	}
	switch s[1] {
	case -1:
		out[1] = dy
	case 1:
		out[3] = dy
	}
	return out
}

func abs32(v float32) float32 { return float32(math.Abs(float64(v))) }

// ---------- outline ----------

// selSegment is one straight run of the selection's boundary, in texel units.
type selSegment struct{ a, b mgl32.Vec2 }

// selectionOutline traces the boundary between selected and unselected texels
// (the canvas edge counting as unselected), merging each straight run into one
// segment so a big rectangle is four lines rather than thousands.
func selectionOutline(mask []bool, w, h int) []selSegment {
	at := func(x, y int) bool {
		return x >= 0 && y >= 0 && x < w && y < h && mask[y*w+x]
	}
	var segs []selSegment
	// Horizontal edges: the line y separates row y-1 from row y.
	for y := 0; y <= h; y++ {
		start := -1
		for x := 0; x <= w; x++ {
			edge := x < w && at(x, y-1) != at(x, y)
			if edge && start < 0 {
				start = x
			} else if !edge && start >= 0 {
				segs = append(segs, selSegment{mgl32.Vec2{float32(start), float32(y)}, mgl32.Vec2{float32(x), float32(y)}})
				start = -1
			}
		}
	}
	// Vertical edges: the line x separates column x-1 from column x.
	for x := 0; x <= w; x++ {
		start := -1
		for y := 0; y <= h; y++ {
			edge := y < h && at(x-1, y) != at(x, y)
			if edge && start < 0 {
				start = y
			} else if !edge && start >= 0 {
				segs = append(segs, selSegment{mgl32.Vec2{float32(x), float32(start)}, mgl32.Vec2{float32(x), float32(y)}})
				start = -1
			}
		}
	}
	return segs
}

// The outline is rebuilt only when the selection changes: tracing a 2048²
// mask is a full pass over it, and the ants are drawn every frame.
var (
	texEditorOutlineEd   *texture.TexEditorState
	texEditorOutlineGen  = -1
	texEditorOutlineSegs []selSegment
	texEditorOutlineBox  [4]float32 // the outline's extent: left, top, right, bottom
)

func texEditorSelectionOutline(ed *texture.TexEditorState) []selSegment {
	if ed != texEditorOutlineEd || ed.SelectionGen() != texEditorOutlineGen {
		texEditorOutlineEd, texEditorOutlineGen = ed, ed.SelectionGen()
		texEditorOutlineSegs = nil
		if mask := ed.SelectionMask(); mask != nil {
			texEditorOutlineSegs = selectionOutline(mask, ed.Doc.W, ed.Doc.H)
		}
		// The box falls out of the outline for free, where finding it from
		// the mask would be another full pass.
		if len(texEditorOutlineSegs) > 0 {
			a := texEditorOutlineSegs[0].a
			b := [4]float32{a.X(), a.Y(), a.X(), a.Y()}
			for _, s := range texEditorOutlineSegs {
				for _, p := range [2]mgl32.Vec2{s.a, s.b} {
					b = [4]float32{min(b[0], p.X()), min(b[1], p.Y()), max(b[2], p.X()), max(b[3], p.Y())}
				}
			}
			texEditorOutlineBox = b
		}
	}
	return texEditorOutlineSegs
}

// texEditorSelectionBox is the bounding box of the selection, in texel units.
func texEditorSelectionBox(ed *texture.TexEditorState) ([4]float32, bool) {
	if len(texEditorSelectionOutline(ed)) == 0 {
		return [4]float32{}, false
	}
	return texEditorOutlineBox, true
}

var (
	texEditorAntsDark  = imgui.PackedColorFromVec4(imgui.Vec4{X: 0, Y: 0, Z: 0, W: 1})
	texEditorAntsLight = imgui.PackedColorFromVec4(imgui.Vec4{X: 1, Y: 1, Z: 1, W: 1})
	texEditorHandleHot = imgui.PackedColorFromVec4(imgui.Vec4{X: 1, Y: 0.8, Z: 0.2, W: 1})
)

// drawTexEditorSelection draws the selection as marching ants, its scale
// handles while a select tool is out, and the rect or lasso being dragged out. Like the UV overlays it is screen-only and never
// touches the document.
func drawTexEditorSelection(dl imgui.DrawList, ed *texture.TexEditorState, topLeft imgui.Vec2, zoom float32) {
	toScreen := func(p mgl32.Vec2) imgui.Vec2 {
		return imgui.Vec2{X: topLeft.X + p.X()*zoom, Y: topLeft.Y + p.Y()*zoom}
	}
	// Only what is inside the canvas panel is worth drawing; at high zoom
	// most of a big selection's outline is off it.
	clipMin, clipMax := imgui.WindowPos(), imgui.WindowPos().Plus(imgui.WindowSize())
	phase := float32(sdl.GetTicks() / 80 % 8) // ants crawl one pixel per 80 ms
	for _, s := range texEditorSelectionOutline(ed) {
		a, b := toScreen(s.a), toScreen(s.b)
		if max(a.X, b.X) < clipMin.X || min(a.X, b.X) > clipMax.X ||
			max(a.Y, b.Y) < clipMin.Y || min(a.Y, b.Y) > clipMax.Y {
			continue
		}
		texEditorDrawAnts(dl, a, b, phase, clipMin, clipMax)
	}

	drawing := texEditorPainting && !texEditorMoving && texEditorScaling < 0
	if box, ok := texEditorSelectionBox(ed); ok && ed.Tool.IsSelectTool() && !drawing {
		mp := imgui.MousePos()
		hot := texEditorScaling
		if !texEditorPainting {
			hot = texEditorHandleAt(ed, (mp.X-topLeft.X)/zoom, (mp.Y-topLeft.Y)/zoom, zoom)
		}
		for h := range texEditorHandles {
			p, shown := texEditorHandlePos(box, h, zoom)
			if !shown {
				continue
			}
			c := toScreen(p)
			half := imgui.Vec2{X: texEditorHandleHalf, Y: texEditorHandleHalf}
			fill := texEditorAntsLight
			if h == hot {
				fill = texEditorHandleHot
			}
			dl.AddRectFilled(c.Minus(half), c.Plus(half), fill)
			dl.AddRect(c.Minus(half), c.Plus(half), texEditorAntsDark)
		}
	}

	if !drawing {
		return
	}
	switch {
	case texEditorLasso != nil:
		for i := 1; i < len(texEditorLasso); i++ {
			texEditorDrawAnts(dl, toScreen(texEditorLasso[i-1]), toScreen(texEditorLasso[i]), phase, clipMin, clipMax)
		}
		if n := len(texEditorLasso); n > 2 {
			// The closing edge the release will add, so the user sees the
			// shape they will actually get.
			dl.AddLineV(toScreen(texEditorLasso[n-1]), toScreen(texEditorLasso[0]), texEditorAntsLight, 1)
		}
	case ed.Tool == texture.ToolSelectRect:
		x0, x1 := min(texEditorSelStart[0], texEditorSelLast[0]), max(texEditorSelStart[0], texEditorSelLast[0])+1
		y0, y1 := min(texEditorSelStart[1], texEditorSelLast[1]), max(texEditorSelStart[1], texEditorSelLast[1])+1
		c := [4]mgl32.Vec2{{float32(x0), float32(y0)}, {float32(x1), float32(y0)}, {float32(x1), float32(y1)}, {float32(x0), float32(y1)}}
		for i := range c {
			texEditorDrawAnts(dl, toScreen(c[i]), toScreen(c[(i+1)%4]), phase, clipMin, clipMax)
		}
	}
}

// texEditorDrawAnts draws one marching-ants line: solid dark underneath, light
// dashes on top offset by phase, so it reads on any colour.
func texEditorDrawAnts(dl imgui.DrawList, a, b imgui.Vec2, phase float32, clipMin, clipMax imgui.Vec2) {
	dl.AddLineV(a, b, texEditorAntsDark, 1)
	d := b.Minus(a)
	length := float32(math.Hypot(float64(d.X), float64(d.Y)))
	if length == 0 {
		return
	}
	dir := d.Times(1 / length)
	// Walk only the part of the line inside the panel: zoomed in, one edge of
	// a big selection can run to a hundred thousand pixels.
	lo, hi, ok := clipSpan(a, dir, length, clipMin, clipMax)
	if !ok {
		return
	}
	const dash = 4
	start := phase - 2*dash + float32(math.Floor(float64(lo/(2*dash))))*2*dash
	for t := start; t < hi; t += 2 * dash {
		t0, t1 := max(t, 0), min(t+dash, length)
		if t1 > t0 {
			dl.AddLineV(a.Plus(dir.Times(t0)), a.Plus(dir.Times(t1)), texEditorAntsLight, 1)
		}
	}
}

// clipSpan is the range of t in [0, length] for which a + dir*t lies inside
// the rectangle — a Liang-Barsky slab clip.
func clipSpan(a, dir imgui.Vec2, length float32, lo, hi imgui.Vec2) (float32, float32, bool) {
	t0, t1 := float32(0), length
	for _, ax := range [2][4]float32{{a.X, dir.X, lo.X, hi.X}, {a.Y, dir.Y, lo.Y, hi.Y}} {
		p, d, mn, mx := ax[0], ax[1], ax[2], ax[3]
		if d == 0 {
			if p < mn || p > mx {
				return 0, 0, false
			}
			continue
		}
		ta, tb := (mn-p)/d, (mx-p)/d
		if ta > tb {
			ta, tb = tb, ta
		}
		t0, t1 = max(t0, ta), min(t1, tb)
	}
	return t0, t1, t1 > t0
}
