package uidraw

// uidraw_build.go — walking a solved screen into a draw list.
//
// One pre-order walk in tree order, which is draw order: a node paints after
// its parent and before its next sibling. There is no z field and no sort,
// because reordering the children array *is* reordering the draw — which is
// what makes the editor's Move Up and Move Down do the obvious thing.
//
// Everything here works in canvas units. The scale into device pixels lives in
// the projection matrix, so this code is identical whether it is building the
// game's HUD at 4K or the editor's preview in a 700-pixel-wide panel. That is
// the whole reason a screen looks the same in both.

import (
	"foundry/internal/gfx"
	"foundry/internal/ui"
	"foundry/internal/uifont"
)

// caretWidth is how thick a text field's cursor is drawn, in canvas units.
const caretWidth = 2

// focusRing is the outline drawn round the navigation-focused widget, in canvas
// units. It exists because a gamepad or keyboard user has no pointer to tell
// them where they are.
const focusRing = 2

// sliderHandleW is the grab handle's width in canvas units.
const sliderHandleW = 14

// Build appends a solved screen's geometry to a list.
//
// scale is canvas units per device pixel, used only for pixel snapping — the
// vertices themselves stay in canvas units.
func Build(l *List, s *ui.Screen, in *ui.Interaction, scale float32) {
	if s == nil {
		return
	}
	var noInteraction ui.Interaction
	if in == nil {
		in = &noInteraction
	}
	buildNode(l, s, &s.Root, in, 1, scale, s.Canvas.PixelSnap)
}

func buildNode(l *List, s *ui.Screen, n *ui.Node, in *ui.Interaction, alpha, scale float32, snap bool) {
	if !n.Visible || n.Culled() {
		return
	}
	a := alpha * n.Alpha
	if a <= 0 {
		// A fully transparent subtree still lays out and still takes input; it
		// just has nothing to say to the rasteriser.
		return
	}

	if n.Clip {
		l.PushClip(snapRect(n.SolvedClip(), scale, snap))
	}

	r := snapRect(n.Solved(), scale, snap)
	state := in.StateOf(s.ID, n)
	col, tex, uv := n.Style.Resolve(state, a)

	drawBackground(l, n, r, col, tex, uv)

	if n.Kind == ui.KindVideo {
		drawVideoFrame(l, n, r, a)
	}

	switch n.Kind {
	case ui.KindBar:
		drawFill(l, n, r, a)
	case ui.KindSlider:
		drawFill(l, n, r, a)
		drawSliderHandle(l, n, r, a)
	case ui.KindToggle:
		drawToggleMark(l, n, r, a)
	}

	switch n.Kind {
	case ui.KindText, ui.KindButton, ui.KindInput, ui.KindToggle:
		drawText(l, s, n, in, r, a, state)
	}

	if in.NavDriven && in.Focus.Is(s.ID, n.ID) {
		ring := ui.ActiveTheme.Accent
		ring[3] *= a
		l.AddRectOutline(r, focusRing, White(), PackColor(ring))
	}

	for i := range n.Children {
		buildNode(l, s, &n.Children[i], in, a, scale, snap)
	}

	if n.Clip {
		l.PopClip()
	}
}

// drawBackground paints a node's own rect: nothing at all when it is fully
// transparent, a nine-slice when it has insets and a known texture size, an
// image when it has a texture, a flat fill otherwise.
func drawBackground(l *List, n *ui.Node, r ui.Rect, col [4]float32, tex string, uv [4]float32) {
	if col[3] <= 0 {
		return
	}
	packed := PackColor(col)

	if tex == "" {
		l.AddRect(r, White(), packed)
		return
	}
	id, ok := gfx.Textures[tex]
	if !ok {
		// A texture that has not been loaded would bind as texture 0 and sample
		// black, which reads as a broken widget rather than a missing asset.
		// The flat fill at least shows the authored colour.
		l.AddRect(r, White(), packed)
		return
	}

	if n.Style.Slice != ([4]float32{}) {
		if tw, th, known := gfx.TextureSize(tex); known {
			l.AddNineSlice(r, id, tw, th, uv, n.Style.Slice, packed)
			return
		}
	}
	l.AddImage(fitRect(r, n.Style.Fill, tex, &uv), id, uv, packed)
}

// fitRect applies the fill mode, adjusting either the destination rect
// (letterbox) or the source UVs (crop). Stretch and tile leave both alone —
// tile because repeating without relying on a texture wrap mode would mean
// emitting one quad per repetition, which is a real feature rather than a
// branch, and is not in this version.
func fitRect(r ui.Rect, mode ui.FillMode, tex string, uv *[4]float32) ui.Rect {
	if mode == ui.FillStretch || mode == ui.FillTile {
		return r
	}
	tw, th, ok := gfx.TextureSize(tex)
	if !ok || tw <= 0 || th <= 0 || r.W <= 0 || r.H <= 0 {
		return r
	}
	srcW, srcH := tw*uv[2], th*uv[3]
	if srcW <= 0 || srcH <= 0 {
		return r
	}

	rectAspect := r.W / r.H
	srcAspect := srcW / srcH

	if mode == ui.FillFit {
		out := r
		if srcAspect > rectAspect {
			out.H = r.W / srcAspect
			out.Y = r.Y + (r.H-out.H)/2
		} else {
			out.W = r.H * srcAspect
			out.X = r.X + (r.W-out.W)/2
		}
		return out
	}

	// FillFill: keep the rect, crop the source.
	if srcAspect > rectAspect {
		keep := rectAspect / srcAspect
		uv[0] += uv[2] * (1 - keep) / 2
		uv[2] *= keep
	} else {
		keep := srcAspect / rectAspect
		uv[1] += uv[3] * (1 - keep) / 2
		uv[3] *= keep
	}
	return r
}

// drawVideoFrame paints the picture over the node's background.
//
// Over, not instead of. The background is what shows before the first frame has
// decoded and, under the default fit mode, what the letterbox bars are made of
// — so a video widget's Style.Color is its bars, and it defaults to black for
// that reason.
//
// Which is also why the frame itself is drawn white rather than tinted by
// Style.Color the way an image is: one colour cannot be both the bars and a
// tint on the picture between them, and of the two, bars is the one every clip
// needs. The node's alpha still applies, so fading a video in and out works.
//
// A frame that has not reached the GPU yet leaves the background alone rather
// than drawing an untextured quad, which would be a white rectangle flashing
// for one frame at the start of every clip.
func drawVideoFrame(l *List, n *ui.Node, r ui.Rect, alpha float32) {
	name := n.VideoTexture()
	if alpha <= 0 || name == "" {
		return
	}
	id, ok := gfx.Textures[name]
	if !ok {
		return
	}
	uv := [4]float32{0, 0, 1, 1}
	l.AddImage(fitRect(r, n.Style.Fill, name, &uv), id, uv,
		PackColor([4]float32{1, 1, 1, alpha}))
}

// drawFill paints the filled portion of a bar or slider, growing from the edge
// its direction names.
func drawFill(l *List, n *ui.Node, r ui.Rect, alpha float32) {
	f := n.Value.Fraction()
	if f <= 0 {
		return
	}
	col := n.Value.FillColor
	col[3] *= alpha
	if col[3] <= 0 {
		return
	}

	fill := r
	switch n.Value.Dir {
	case ui.FillRightLeft:
		fill.W = r.W * f
		fill.X = r.X + r.W - fill.W
	case ui.FillBottomTop:
		fill.H = r.H * f
		fill.Y = r.Y + r.H - fill.H
	case ui.FillTopBottom:
		fill.H = r.H * f
	default:
		fill.W = r.W * f
	}

	packed := PackColor(col)
	if n.Value.FillTexture != "" {
		if id, ok := gfx.Textures[n.Value.FillTexture]; ok {
			l.AddImage(fill, id, [4]float32{0, 0, 1, 1}, packed)
			return
		}
	}
	l.AddRect(fill, White(), packed)
}

// drawSliderHandle puts a grab handle at the value's position along the track.
func drawSliderHandle(l *List, n *ui.Node, r ui.Rect, alpha float32) {
	f := n.Value.Fraction()
	col := ui.ActiveTheme.Accent
	col[3] *= alpha

	h := ui.Rect{X: r.X + (r.W-sliderHandleW)*f, Y: r.Y, W: sliderHandleW, H: r.H}
	if n.Value.Dir == ui.FillBottomTop || n.Value.Dir == ui.FillTopBottom {
		h = ui.Rect{X: r.X, Y: r.Y + (r.H-sliderHandleW)*f, W: r.W, H: sliderHandleW}
	}
	l.AddRect(h, White(), PackColor(col))
}

// drawToggleMark fills the inset of a switched-on toggle.
func drawToggleMark(l *List, n *ui.Node, r ui.Rect, alpha float32) {
	if !n.Value.On {
		return
	}
	col := ui.ActiveTheme.Accent
	col[3] *= alpha
	inset := minF(r.W, r.H) * 0.25
	l.AddRect(ui.Rect{X: r.X + inset, Y: r.Y + inset, W: r.W - 2*inset, H: r.H - 2*inset},
		White(), PackColor(col))
}

// drawText paints a node's string, plus the caret when it is a focused input.
func drawText(l *List, s *ui.Screen, n *ui.Node, in *ui.Interaction, r ui.Rect, alpha float32, state ui.VisualState) {
	str := n.Text.Shown()
	focused := n.Kind == ui.KindInput && in.Focus.Is(s.ID, n.ID)
	if str == "" && !focused {
		return
	}

	font, size := ui.ActiveTheme.FontFor(n.Text)
	atlas := uifont.Face(font, size)
	id := gfx.Textures[atlas.TexName]

	col := n.Text.Color
	col[3] *= alpha
	if state == ui.StateDisabled {
		col[3] *= 0.5
	}
	packed := PackColor(col)

	if str != "" {
		l.AddText(atlas, id, r, str, n.Text.HAlign, n.Text.VAlign, n.Text.Wrap, packed)
	}

	if focused {
		drawCaret(l, n, in, atlas, r, col)
	}
}

// drawCaret puts the text cursor where the next character will go. It is
// measured with the same atlas the string was drawn with, so it lands between
// the glyphs rather than near them.
func drawCaret(l *List, n *ui.Node, in *ui.Interaction, a *uifont.Atlas, r ui.Rect, col [4]float32) {
	str := n.Text.Shown()
	caret := in.Caret
	if caret > len(str) {
		caret = len(str)
	}

	w, _ := uifont.Measure(a, str)
	var x float32
	switch n.Text.HAlign {
	case ui.AlignCenter:
		x = r.X + (r.W-w)/2
	case ui.AlignRight:
		x = r.X + r.W - w
	default:
		x = r.X
	}
	x += uifont.CaretX(a, str, caret)

	var y float32
	switch n.Text.VAlign {
	case ui.AlignMiddle:
		y = r.Y + (r.H-a.LineHeight)/2
	case ui.AlignBottom:
		y = r.Y + r.H - a.LineHeight
	default:
		y = r.Y
	}
	l.AddRect(ui.Rect{X: x, Y: y, W: caretWidth, H: a.LineHeight}, White(), PackColor(col))
}

// snapRect rounds a rect to whole device pixels.
//
// Edges are snapped rather than origin-and-size, so two abutting elements stay
// abutting: rounding a position and a width independently can leave a one-pixel
// seam between them that appears and disappears as a window is resized.
//
// Snapping happens here rather than in the layout solve on purpose. Hit
// testing, anchor drags and the editor's numeric readouts all want stable
// continuous values; only the rasterisation wants whole pixels.
func snapRect(r ui.Rect, scale float32, snap bool) ui.Rect {
	if !snap || scale <= 0 {
		return r
	}
	x0 := roundToPixel(r.X, scale)
	y0 := roundToPixel(r.Y, scale)
	x1 := roundToPixel(r.X+r.W, scale)
	y1 := roundToPixel(r.Y+r.H, scale)
	return ui.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

func roundToPixel(v, scale float32) float32 {
	px := v * scale
	if px < 0 {
		return float32(int(px-0.5)) / scale
	}
	return float32(int(px+0.5)) / scale
}

func minF(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}
