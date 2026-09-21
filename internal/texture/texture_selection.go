package texture

// texture_selection.go — the texture editor's selection: a per-texel mask that
// fences every tool in, plus copy/cut/paste and moving what it holds.
//
// The mask is enforced in exactly one place, setTexel, which every tool (brush,
// spray, line, rect, fill, Fill UV, the eraser, Cut) writes through — so no tool
// has to know a selection exists. The one writer that goes around it is the
// floating content below, which is the thing that *moves* the selection and has
// to write where the mask is going, not where it is.
//
// Moving and pasting work on a "float": content lifted off a layer, kept whole
// beside it, and stamped back onto a copy of what was underneath at the float's
// current offset. Keeping it whole is what lets content dragged off the canvas
// edge come back when it is dragged back, and lets a paste slide over art
// without eating the texels it passes. A float is only good while the layer
// still holds exactly what it last stamped: any other edit — a brush stroke,
// an undo, switching layer — makes that false, and the next move simply lifts
// again from the layer as it now stands. That check is what keeps every other
// operation in the editor from having to know about floats either.

import (
	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/texdoc"
)

// SelectMode says how a newly drawn selection combines with the current one.
type SelectMode int

const (
	SelectReplace SelectMode = iota
	SelectAdd
	SelectSubtract
)

// TexClip is copied layer content: a w x h block, the texels of it that were
// selected, and where on the canvas it came from (so a paste lands back in
// place and can be dragged from there).
type TexClip struct {
	X, Y, W, H int
	Idx        []uint8
	Mask       []bool
}

// texClipboard is shared by every document the editor opens, so art can be
// copied out of one texture and pasted into another.
var texClipboard *TexClip

// ClipboardFull reports whether there is anything to paste.
func ClipboardFull() bool { return texClipboard != nil }

// texFloat is content lifted off one layer (see the file comment).
type texFloat struct {
	frame   int
	channel texdoc.TexChannel
	layer   int

	base    []uint8 // the layer with the lifted content removed
	written []uint8 // the layer as the float last stamped it
	clip    TexClip // the content as placed; clip.X/Y/W/H is where it sits now

	// src is the content as it was lifted or pasted, before any scaling.
	// Every scale resamples from it rather than from clip, so shrinking and
	// then growing back gives the original texels, not a blur of them.
	src          TexClip
	flipX, flipY bool // clip is src mirrored on these axes
}

// HasSelection reports whether a selection is fencing the tools in.
func (e *TexEditorState) HasSelection() bool {
	return e.sel != nil && e.Doc != nil && len(e.sel) == e.Doc.W*e.Doc.H
}

// SelectionMask is the current selection, one bool per texel in row order, or
// nil for none. Read-only: callers must not write through it.
func (e *TexEditorState) SelectionMask() []bool {
	if !e.HasSelection() {
		return nil
	}
	return e.sel
}

// SelectionGen changes whenever the selection does, so the UI can cache
// anything derived from it (its outline) and rebuild only on change.
func (e *TexEditorState) SelectionGen() int { return e.selGen }

// Selected reports whether texel (x, y) may be edited: always true with no
// selection, otherwise only inside it.
func (e *TexEditorState) Selected(x, y int) bool {
	if x < 0 || y < 0 || x >= e.Doc.W || y >= e.Doc.H {
		return false
	}
	if !e.HasSelection() {
		return true
	}
	return e.sel[y*e.Doc.W+x]
}

// InSelection reports whether (x, y) is inside an actual selection — false
// with no selection, unlike Selected. It is what decides whether a drag moves
// the selection's contents or draws a new one.
func (e *TexEditorState) InSelection(x, y int) bool {
	return e.HasSelection() && e.Selected(x, y)
}

// setSelection installs mask (nil = none) as the selection. An all-false mask
// is stored as no selection at all: a selection nothing is inside would fence
// every tool out of the whole canvas, with nothing on screen to say why.
func (e *TexEditorState) setSelection(mask []bool) {
	if mask != nil {
		anySet := false
		for _, v := range mask {
			if v {
				anySet = true
				break
			}
		}
		if !anySet {
			mask = nil
		}
	}
	e.sel = mask
	e.selGen++
}

// Select combines mask with the current selection per mode. Drawing a new
// selection drops any float: its content is already stamped on the layer, and
// the selection that would move it is gone.
func (e *TexEditorState) Select(mask []bool, mode SelectMode) {
	n := e.Doc.W * e.Doc.H
	if len(mask) != n {
		return
	}
	e.float = nil
	cur := e.SelectionMask()
	out := make([]bool, n)
	for i := range out {
		switch mode {
		case SelectAdd:
			out[i] = mask[i] || (cur != nil && cur[i])
		case SelectSubtract:
			out[i] = cur != nil && cur[i] && !mask[i]
		default:
			out[i] = mask[i]
		}
	}
	e.setSelection(out)
}

// SelectRect selects the texels from (x0,y0) to (x1,y1) inclusive, clamped to
// the canvas.
func (e *TexEditorState) SelectRect(x0, y0, x1, y1 int, mode SelectMode) {
	e.Select(e.RectMask(x0, y0, x1, y1), mode)
}

// RectMask is the mask of the texels from (x0,y0) to (x1,y1) inclusive.
func (e *TexEditorState) RectMask(x0, y0, x1, y1 int) []bool {
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	w, h := e.Doc.W, e.Doc.H
	mask := make([]bool, w*h)
	for y := max(y0, 0); y <= min(y1, h-1); y++ {
		for x := max(x0, 0); x <= min(x1, w-1); x++ {
			mask[y*w+x] = true
		}
	}
	return mask
}

// LassoMask is the mask of the texels whose centres lie inside the polygon
// pts, given in texel units (so (0,0) is the canvas's top-left corner and
// (W,H) its bottom-right). Even-odd, like Fill UV, so a lasso that crosses
// itself selects what a viewer would expect it to.
func (e *TexEditorState) LassoMask(pts []mgl32.Vec2) []bool {
	w, h := e.Doc.W, e.Doc.H
	mask := make([]bool, w*h)
	if len(pts) < 3 {
		return mask
	}
	lo, hi := pts[0], pts[0]
	for _, p := range pts {
		lo = mgl32.Vec2{min(lo.X(), p.X()), min(lo.Y(), p.Y())}
		hi = mgl32.Vec2{max(hi.X(), p.X()), max(hi.Y(), p.Y())}
	}
	x0, y0 := max(int(lo.X()), 0), max(int(lo.Y()), 0)
	x1, y1 := min(int(hi.X())+1, w-1), min(int(hi.Y())+1, h-1)
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			if pointInLoop(pts, float32(x)+0.5, float32(y)+0.5) {
				mask[y*w+x] = true
			}
		}
	}
	return mask
}

// SelectAll selects the whole canvas.
func (e *TexEditorState) SelectAll() {
	mask := make([]bool, e.Doc.W*e.Doc.H)
	for i := range mask {
		mask[i] = true
	}
	e.Select(mask, SelectReplace)
}

// Deselect drops the selection, and with it any float — its content stays
// where it was last put.
func (e *TexEditorState) Deselect() {
	e.float = nil
	if e.sel != nil {
		e.setSelection(nil)
	}
}

// InvertSelection selects everything that wasn't, and nothing that was.
// Inverting "no selection" (which means everything is editable) gives none.
func (e *TexEditorState) InvertSelection() {
	cur := e.SelectionMask()
	if cur == nil {
		return
	}
	mask := make([]bool, len(cur))
	for i, v := range cur {
		mask[i] = !v
	}
	e.Select(mask, SelectReplace)
}

// selectionBounds is the smallest rectangle holding every selected texel.
func (e *TexEditorState) selectionBounds() (x0, y0, x1, y1 int, ok bool) {
	sel := e.SelectionMask()
	if sel == nil {
		return 0, 0, 0, 0, false
	}
	w := e.Doc.W
	x0, y0, x1, y1 = e.Doc.W, e.Doc.H, -1, -1
	for i, v := range sel {
		if !v {
			continue
		}
		x, y := i%w, i/w
		x0, y0 = min(x0, x), min(y0, y)
		x1, y1 = max(x1, x), max(y1, y)
	}
	return x0, y0, x1, y1, x1 >= 0
}

// ---------- copy / cut / paste ----------

// Copy puts the selected texels of the active layer on the clipboard. With no
// selection it copies the whole layer. Reports whether anything was copied.
func (e *TexEditorState) Copy() bool {
	layer := e.activeLayer()
	if layer == nil {
		return false
	}
	x0, y0, x1, y1, ok := e.selectionBounds()
	if !ok {
		x0, y0, x1, y1 = 0, 0, e.Doc.W-1, e.Doc.H-1
	}
	c := &TexClip{X: x0, Y: y0, W: x1 - x0 + 1, H: y1 - y0 + 1}
	c.Idx = make([]uint8, c.W*c.H)
	c.Mask = make([]bool, c.W*c.H)
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			if !e.Selected(x, y) {
				continue
			}
			j := (y-y0)*c.W + (x - x0)
			c.Idx[j] = layer.Idx[y*e.Doc.W+x]
			c.Mask[j] = true
		}
	}
	texClipboard = c
	return true
}

// Cut copies the selection, then clears it on the active layer to the
// "nothing here" value (0 on both channels) as one undo step. The selection
// stays, so the hole can be painted straight back into.
func (e *TexEditorState) Cut() bool {
	if !e.Copy() {
		return false
	}
	e.EraseSelection()
	return true
}

// EraseSelection clears every selected texel of the active layer — the
// Delete key. Goes through setTexel like any tool, so the mask is the fence.
func (e *TexEditorState) EraseSelection() {
	if e.activeLayer() == nil {
		return
	}
	e.BeginStroke()
	for y := 0; y < e.Doc.H; y++ {
		for x := 0; x < e.Doc.W; x++ {
			e.setTexel(x, y, 0)
		}
	}
	e.EndStroke()
}

// Paste drops the clipboard onto the active layer as a float, back where it
// was copied from (pulled onto the canvas if that is now off it — a paste
// into a smaller texture), and selects it so it can be dragged into place.
// It is one undo step, which takes the selection back with it.
func (e *TexEditorState) Paste() bool {
	c := texClipboard
	layer := e.activeLayer()
	if c == nil || layer == nil {
		return false
	}
	clip := *c
	clip.X = max(min(clip.X, e.Doc.W-clip.W), 0)
	clip.Y = max(min(clip.Y, e.Doc.H-clip.H), 0)

	e.beginSelStroke()
	e.float = &texFloat{
		frame:   e.Doc.ActiveFrameIndex(),
		channel: e.Doc.Channel(),
		layer:   e.Doc.Active,
		base:    append([]uint8(nil), layer.Idx...),
		clip:    clip,
		src:     clip,
	}
	e.stampFloat()
	e.EndStroke()
	return true
}

// ---------- moving ----------

// floatValid reports whether the float can still be moved: it belongs to the
// checked-out layer, and that layer holds exactly what it last stamped.
func (e *TexEditorState) floatValid() bool {
	f := e.float
	if f == nil || f.frame != e.Doc.ActiveFrameIndex() || f.channel != e.Doc.Channel() || f.layer != e.Doc.Active {
		return false
	}
	layer := e.activeLayer()
	if layer == nil || len(layer.Idx) != len(f.written) {
		return false
	}
	for i, v := range layer.Idx {
		if f.written[i] != v {
			return false
		}
	}
	return true
}

// lift makes a float out of the selected texels of the active layer, leaving
// 0 ("nothing here") behind them.
func (e *TexEditorState) lift() bool {
	layer := e.activeLayer()
	x0, y0, x1, y1, ok := e.selectionBounds()
	if layer == nil || !ok {
		return false
	}
	w := e.Doc.W
	f := &texFloat{
		frame:   e.Doc.ActiveFrameIndex(),
		channel: e.Doc.Channel(),
		layer:   e.Doc.Active,
		base:    append([]uint8(nil), layer.Idx...),
		clip:    TexClip{X: x0, Y: y0, W: x1 - x0 + 1, H: y1 - y0 + 1},
	}
	f.clip.Idx = make([]uint8, f.clip.W*f.clip.H)
	f.clip.Mask = make([]bool, f.clip.W*f.clip.H)
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			i := y*w + x
			if !e.sel[i] {
				continue
			}
			j := (y-y0)*f.clip.W + (x - x0)
			f.clip.Idx[j] = layer.Idx[i]
			f.clip.Mask[j] = true
			f.base[i] = 0
		}
	}
	f.src = f.clip
	// Nothing has moved yet, and the layer as it stands is exactly the base
	// with this content stamped back where it came from.
	f.written = append([]uint8(nil), layer.Idx...)
	e.float = f
	return true
}

// stampFloat rewrites the active layer as the float's base with the float on
// top at its current offset, and moves the selection to cover it. Texels of
// the float that hold 0 are left out, so a lifted shape carries no box of
// transparency with it to punch through what it is dragged across.
func (e *TexEditorState) stampFloat() {
	f := e.float
	layer := e.activeLayer()
	w, h := e.Doc.W, e.Doc.H
	copy(layer.Idx, f.base)
	sel := make([]bool, w*h)
	c := &f.clip
	for cy := 0; cy < c.H; cy++ {
		y := c.Y + cy
		if y < 0 || y >= h {
			continue
		}
		for cx := 0; cx < c.W; cx++ {
			x := c.X + cx
			j := cy*c.W + cx
			if x < 0 || x >= w || !c.Mask[j] {
				continue
			}
			sel[y*w+x] = true
			if c.Idx[j] != 0 {
				layer.Idx[y*w+x] = c.Idx[j]
			}
		}
	}
	f.written = append(f.written[:0], layer.Idx...)
	e.setSelection(sel)
	e.PreviewDirty = true
	if e.Doc.Channel() == texdoc.ChanBump {
		e.BumpDirty = true
	}
}

// BeginMove starts dragging the selection's contents: it opens one undo step
// for the whole drag and lifts the content, unless an earlier move or paste
// already has it floating. Reports whether there is anything to move.
func (e *TexEditorState) BeginMove() bool {
	if !e.HasSelection() || e.activeLayer() == nil {
		return false
	}
	if !e.floatValid() {
		e.float = nil
	}
	e.beginSelStroke()
	if e.float == nil && !e.lift() {
		e.strokeActive = false
		return false
	}
	e.moveFromX, e.moveFromY = e.float.clip.X, e.float.clip.Y
	return true
}

// MoveBy places the moving content dx, dy texels from where BeginMove found it.
func (e *TexEditorState) MoveBy(dx, dy int) {
	if e.float == nil || !e.strokeActive {
		return
	}
	x, y := e.moveFromX+dx, e.moveFromY+dy
	if x == e.float.clip.X && y == e.float.clip.Y {
		return
	}
	e.float.clip.X, e.float.clip.Y = x, y
	e.stampFloat()
}

// EndMove commits the drag as one undo step. The content stays floating, so
// the next drag picks it up again without disturbing what it now covers.
func (e *TexEditorState) EndMove() {
	e.EndStroke()
}

// Nudge moves the selection's contents by dx, dy as one undo step — the
// arrow keys.
func (e *TexEditorState) Nudge(dx, dy int) {
	if e.BeginMove() {
		e.MoveBy(dx, dy)
		e.EndMove()
	}
}

// ---------- scaling ----------

// BeginScale starts resizing the selection's contents, lifting them just as
// BeginMove does; the whole drag is one undo step. Reports whether there is
// anything to scale.
func (e *TexEditorState) BeginScale() bool {
	if !e.BeginMove() {
		return false
	}
	c := e.float.clip
	e.scaleFrom = [4]int{c.X, c.Y, c.X + c.W, c.Y + c.H}
	return true
}

// ScaleEdges resizes the scaling content so its left, top, right and bottom
// edges sit dl, dt, dr, db texels from where BeginScale found them. An edge
// dragged past its opposite mirrors the content on that axis; the content
// never gets thinner than one texel.
func (e *TexEditorState) ScaleEdges(dl, dt, dr, db int) {
	f := e.float
	if f == nil || !e.strokeActive {
		return
	}
	x, w, flipX := scaleSpan(e.scaleFrom[0]+dl, e.scaleFrom[2]+dr)
	y, h, flipY := scaleSpan(e.scaleFrom[1]+dt, e.scaleFrom[3]+db)
	if x == f.clip.X && y == f.clip.Y && w == f.clip.W && h == f.clip.H && flipX == f.flipX && flipY == f.flipY {
		return
	}
	f.clip = resampleClip(f.src, w, h, flipX, flipY)
	f.clip.X, f.clip.Y = x, y
	f.flipX, f.flipY = flipX, flipY
	e.stampFloat()
}

// EndScale commits the resize as one undo step. The content stays floating
// with its original texels kept, so a later resize starts from those.
func (e *TexEditorState) EndScale() {
	e.EndStroke()
}

// scaleSpan turns two edge positions into a start, a width of at least one
// texel, and whether they crossed.
func scaleSpan(a, b int) (start, size int, flip bool) {
	if b < a {
		a, b, flip = b, a, true
	}
	return a, max(b-a, 1), flip
}

// resampleClip scales c to w x h texels by nearest neighbour — the only
// resampling that keeps palette indices meaningful — mirrored as asked.
func resampleClip(c TexClip, w, h int, flipX, flipY bool) TexClip {
	out := TexClip{W: w, H: h, Idx: make([]uint8, w*h), Mask: make([]bool, w*h)}
	for y := 0; y < h; y++ {
		sy := min((2*y+1)*c.H/(2*h), c.H-1)
		if flipY {
			sy = c.H - 1 - sy
		}
		for x := 0; x < w; x++ {
			sx := min((2*x+1)*c.W/(2*w), c.W-1)
			if flipX {
				sx = c.W - 1 - sx
			}
			out.Idx[y*w+x] = c.Idx[sy*c.W+sx]
			out.Mask[y*w+x] = c.Mask[sy*c.W+sx]
		}
	}
	return out
}

// beginSelStroke opens a stroke that also records the selection, for the
// operations that move it (move, paste): undoing one of those has to put the
// selection back where the content was, or the undone art would be sitting
// outside the fence the user is looking at.
func (e *TexEditorState) beginSelStroke() {
	e.BeginStroke()
	e.strokeSelBefore = e.SelectionMask()
	e.strokeSelRecorded = true
}
