package uidraw

// Package uidraw is the screen-space pass: it turns a solved ui.Screen into
// vertices and issues them.
//
// It sits above gfx and below world. Not inside internal/render, because every
// entry point there takes a *world.Level — a UI pass that needed a level could
// not draw a main menu before one has loaded, could not draw the loading screen
// that covers the load, and could not be reused by the UI editor's pop-out
// window. Not inside gfx either: a batcher that knows what a ui.Node is belongs
// above the shared registry rather than in it.
//
// The GL half is deliberately shaped like imguibackend.ImguiRenderDrawData, down
// to the twenty-byte vertex. That renderer has already had every lesson about
// scissor rects and state restoration beaten into it, and matching it means
// those lessons transfer rather than being relearned.
//
// uidraw_list.go holds the geometry: a vertex buffer, an index buffer, a list
// of draw commands, and the primitives that append to them. No GL at all, so
// every batching and nine-slice decision below is unit-testable headlessly.

import (
	"math"

	"foundry/internal/ui"
	"foundry/internal/uifont"
)

// Vertex is twenty bytes: position, texture coordinate, packed colour.
//
// The colour is one uint32 whose bytes are R, G, B, A in memory, uploaded as
// four normalised unsigned bytes. Four floats would be four times the bandwidth
// for a precision no display can show.
type Vertex struct {
	X, Y float32
	U, V float32
	RGBA uint32
}

// Cmd is a run of indices sharing a texture and a scissor rectangle — the two
// things the shader cannot vary per vertex, and therefore the entire batching
// key.
type Cmd struct {
	Tex       uint32
	Clip      ui.Rect
	IdxOffset int
	IdxCount  int
}

// List is one frame's geometry.
//
// Reused across frames: Reset keeps every backing array, so a steady UI
// allocates nothing per frame after the first.
type List struct {
	Verts []Vertex
	Idx   []uint32
	Cmds  []Cmd

	bounds ui.Rect
	clips  []ui.Rect
	lines  []uifont.Line // reused text-layout scratch
}

// Indices are 32-bit. Sixteen would cap a list at 65535 vertices and force the
// glDrawElementsBaseVertex dance around it, which is a whole class of bug in
// exchange for a few hundred kilobytes of buffer.
const bytesPerIndex = 4

// Reset empties the list for a new frame, keeping its buffers. bounds is the
// drawable area in canvas units and becomes the outermost clip.
func (l *List) Reset(bounds ui.Rect) {
	l.Verts = l.Verts[:0]
	l.Idx = l.Idx[:0]
	l.Cmds = l.Cmds[:0]
	l.clips = l.clips[:0]
	l.bounds = bounds
}

// Empty reports whether there is nothing to draw.
func (l *List) Empty() bool { return len(l.Idx) == 0 }

// Clip is the scissor rectangle currently in force.
func (l *List) Clip() ui.Rect {
	if n := len(l.clips); n > 0 {
		return l.clips[n-1]
	}
	return l.bounds
}

// PushClip narrows the clip. It intersects with what is already in force rather
// than replacing it, so a nested panel cannot draw outside its grandparent.
func (l *List) PushClip(r ui.Rect) { l.clips = append(l.clips, l.Clip().Intersect(r)) }

// PopClip restores the previous clip.
func (l *List) PopClip() {
	if n := len(l.clips); n > 0 {
		l.clips = l.clips[:n-1]
	}
}

// AddRect appends a solid rectangle. The caller supplies the white texture, so
// there is one shader with no mode uniform: a flat fill, an image and a glyph
// all come out of the same multiply.
func (l *List) AddRect(r ui.Rect, white uint32, col uint32) {
	l.addQuad(white, r, [4]float32{0, 0, 1, 1}, col)
}

// AddImage appends a textured rectangle sampling a sub-rect given as
// u, v, du, dv — render.SpriteInstance.UV's spelling.
func (l *List) AddImage(r ui.Rect, tex uint32, uv [4]float32, col uint32) {
	l.addQuad(tex, r, uv, col)
}

// AddNineSlice appends a rectangle whose corners keep their texel size while
// its edges and centre stretch — the only way a rounded panel or a bordered
// button survives being resized.
//
// texW and texH are the source texture's pixel size, which is what the insets
// are measured in. Insets that do not fit are scaled down proportionally rather
// than allowed to overlap, so a button smaller than its own border shrinks
// instead of turning inside out.
func (l *List) AddNineSlice(r ui.Rect, tex uint32, texW, texH float32, uv [4]float32, slice [4]float32, col uint32) {
	left, top, right, bottom := slice[0], slice[1], slice[2], slice[3]
	if left+top+right+bottom == 0 || texW <= 0 || texH <= 0 {
		l.addQuad(tex, r, uv, col)
		return
	}

	if h := left + right; h > r.W && h > 0 {
		k := r.W / h
		left, right = left*k, right*k
	}
	if v := top + bottom; v > r.H && v > 0 {
		k := r.H / v
		top, bottom = top*k, bottom*k
	}

	u0, v0, du, dv := uv[0], uv[1], uv[2], uv[3]
	xs := [4]float32{r.X, r.X + left, r.X + r.W - right, r.X + r.W}
	ys := [4]float32{r.Y, r.Y + top, r.Y + r.H - bottom, r.Y + r.H}
	us := [4]float32{u0, u0 + slice[0]/texW*du, u0 + du - slice[2]/texW*du, u0 + du}
	vs := [4]float32{v0, v0 + slice[1]/texH*dv, v0 + dv - slice[3]/texH*dv, v0 + dv}

	for row := 0; row < 3; row++ {
		for col2 := 0; col2 < 3; col2++ {
			w, h := xs[col2+1]-xs[col2], ys[row+1]-ys[row]
			if w <= 0 || h <= 0 {
				continue
			}
			l.addQuad(tex,
				ui.Rect{X: xs[col2], Y: ys[row], W: w, H: h},
				[4]float32{us[col2], vs[row], us[col2+1] - us[col2], vs[row+1] - vs[row]},
				col)
		}
	}
}

// AddText lays a string out inside a rectangle and appends a quad per visible
// glyph. The atlas is measured with the same code the layout solve used, so
// what is drawn is the size that was reserved for it.
func (l *List) AddText(a *uifont.Atlas, tex uint32, r ui.Rect, s string, h ui.HAlign, v ui.VAlign, wrap bool, col uint32) {
	if a == nil || s == "" {
		return
	}
	maxW := float32(0)
	if wrap {
		maxW = r.W
	}
	l.lines = uifont.Layout(a, s, maxW, l.lines)

	total := float32(len(l.lines)) * a.LineHeight
	var y float32
	switch v {
	case ui.AlignMiddle:
		y = r.Y + (r.H-total)/2
	case ui.AlignBottom:
		y = r.Y + r.H - total
	default:
		y = r.Y
	}

	for i := range l.lines {
		ln := &l.lines[i]
		var x float32
		switch h {
		case ui.AlignCenter:
			x = r.X + (r.W-ln.Width)/2
		case ui.AlignRight:
			x = r.X + r.W - ln.Width
		default:
			x = r.X
		}
		// The baseline, not the top: glyph quads are measured from it, which is
		// what makes a mixed-size line sit on one line rather than one box.
		baseline := y + float32(i)*a.LineHeight + a.Ascent
		for _, q := range ln.Quads {
			l.addQuad(tex,
				ui.Rect{X: x + q.X0, Y: baseline + q.Y0, W: q.X1 - q.X0, H: q.Y1 - q.Y0},
				[4]float32{q.U0, q.V0, q.U1 - q.U0, q.V1 - q.V0},
				col)
		}
	}
}

// AddLine appends a thick line as a quad. Used by the editor's overlays, which
// go through this list rather than a separate one so that they are clipped and
// scaled by exactly the transform the content is.
func (l *List) AddLine(x0, y0, x1, y1, thick float32, white uint32, col uint32) {
	if thick <= 0 {
		thick = 1
	}
	dx, dy := x1-x0, y1-y0
	length := float32(math.Hypot(float64(dx), float64(dy)))
	if length == 0 {
		return
	}
	// The normal, scaled to half the thickness.
	nx, ny := -dy/length*thick/2, dx/length*thick/2

	l.ensureCmd(white)
	base := uint32(len(l.Verts))
	l.Verts = append(l.Verts,
		Vertex{X: x0 + nx, Y: y0 + ny, U: 0, V: 0, RGBA: col},
		Vertex{X: x1 + nx, Y: y1 + ny, U: 1, V: 0, RGBA: col},
		Vertex{X: x1 - nx, Y: y1 - ny, U: 1, V: 1, RGBA: col},
		Vertex{X: x0 - nx, Y: y0 - ny, U: 0, V: 1, RGBA: col},
	)
	l.addIndices(base)
}

// AddRectOutline appends a hollow rectangle, drawn inward so the outline of a
// selected widget covers the widget rather than its neighbours.
func (l *List) AddRectOutline(r ui.Rect, thick float32, white uint32, col uint32) {
	if thick <= 0 {
		thick = 1
	}
	if r.W <= 0 || r.H <= 0 {
		return
	}
	t := thick
	if t > r.W/2 {
		t = r.W / 2
	}
	if t > r.H/2 {
		t = r.H / 2
	}
	l.AddRect(ui.Rect{X: r.X, Y: r.Y, W: r.W, H: t}, white, col)
	l.AddRect(ui.Rect{X: r.X, Y: r.Y + r.H - t, W: r.W, H: t}, white, col)
	l.AddRect(ui.Rect{X: r.X, Y: r.Y + t, W: t, H: r.H - 2*t}, white, col)
	l.AddRect(ui.Rect{X: r.X + r.W - t, Y: r.Y + t, W: t, H: r.H - 2*t}, white, col)
}

// addQuad is the one place a rectangle becomes four vertices and six indices.
func (l *List) addQuad(tex uint32, r ui.Rect, uv [4]float32, col uint32) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	l.ensureCmd(tex)
	base := uint32(len(l.Verts))
	u0, v0, u1, v1 := uv[0], uv[1], uv[0]+uv[2], uv[1]+uv[3]
	l.Verts = append(l.Verts,
		Vertex{X: r.X, Y: r.Y, U: u0, V: v0, RGBA: col},
		Vertex{X: r.X + r.W, Y: r.Y, U: u1, V: v0, RGBA: col},
		Vertex{X: r.X + r.W, Y: r.Y + r.H, U: u1, V: v1, RGBA: col},
		Vertex{X: r.X, Y: r.Y + r.H, U: u0, V: v1, RGBA: col},
	)
	l.addIndices(base)
}

func (l *List) addIndices(base uint32) {
	l.Idx = append(l.Idx, base, base+1, base+2, base, base+2, base+3)
	l.Cmds[len(l.Cmds)-1].IdxCount += 6
}

// ensureCmd opens a new draw command when the texture or the clip has changed,
// and reuses the current one when neither has. A HUD comes out at three to
// eight draw calls; a menu with one atlas and one font comes out at two.
func (l *List) ensureCmd(tex uint32) {
	clip := l.Clip()
	if n := len(l.Cmds); n > 0 {
		cur := &l.Cmds[n-1]
		if cur.Tex == tex && cur.Clip == clip {
			return
		}
		if cur.IdxCount == 0 {
			// Nothing was ever appended to it — retarget rather than leaving an
			// empty command for the renderer to skip.
			cur.Tex, cur.Clip = tex, clip
			return
		}
	}
	l.Cmds = append(l.Cmds, Cmd{Tex: tex, Clip: clip, IdxOffset: len(l.Idx)})
}

// PackColor turns a colour into the uploaded byte order, clamping rather than
// wrapping: a tint that overshot in a hover calculation should saturate, not
// come out as its own complement.
func PackColor(c [4]float32) uint32 {
	return uint32(to255(c[0])) |
		uint32(to255(c[1]))<<8 |
		uint32(to255(c[2]))<<16 |
		uint32(to255(c[3]))<<24
}

func to255(v float32) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return uint8(v*255 + 0.5)
}
