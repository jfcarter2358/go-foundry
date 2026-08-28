package uidraw

import (
	"testing"
	"unsafe"

	"foundry/internal/ui"
	"foundry/internal/uifont"
)

// rect is ui.Rect with positional arguments, so the tables below read as
// geometry rather than as field names. go vet rightly refuses an unkeyed
// literal of another package's struct.
func rect(x, y, w, h float32) ui.Rect { return ui.Rect{X: x, Y: y, W: w, H: h} }

func newList() *List {
	l := &List{}
	l.Reset(ui.Rect{X: 0, Y: 0, W: 1000, H: 800})
	return l
}

// TestVertexIsTwentyBytes guards the layout the attribute pointers in
// uidraw_gl.go hard-code. A field reordered or widened here would upload
// garbage with no compiler complaint at all.
func TestVertexIsTwentyBytes(t *testing.T) {
	if got := unsafe.Sizeof(Vertex{}); got != vertexStride {
		t.Fatalf("Vertex is %d bytes, the attribute pointers assume %d", got, vertexStride)
	}
	v := Vertex{}
	if off := unsafe.Offsetof(v.U); off != 8 {
		t.Errorf("UV is at offset %d, the pointer says 8", off)
	}
	if off := unsafe.Offsetof(v.RGBA); off != 16 {
		t.Errorf("Color is at offset %d, the pointer says 16", off)
	}
}

func TestPackColorIsRGBAInMemoryOrder(t *testing.T) {
	got := PackColor([4]float32{1, 0, 0, 1})
	if got&0xFF != 0xFF {
		t.Errorf("red is not in the low byte: %08x", got)
	}
	if got>>24 != 0xFF {
		t.Errorf("alpha is not in the high byte: %08x", got)
	}
	// Overshoot from a hover tint must saturate, not wrap into its complement.
	if got := PackColor([4]float32{2, -1, 0.5, 1}); got&0xFF != 0xFF || (got>>8)&0xFF != 0 {
		t.Errorf("out-of-range channels did not clamp: %08x", got)
	}
}

func TestBatchMergesOnTextureAndClip(t *testing.T) {
	l := newList()
	l.AddRect(rect(0, 0, 10, 10), 7, 0xFFFFFFFF)
	l.AddRect(rect(20, 0, 10, 10), 7, 0xFFFFFFFF)
	l.AddImage(rect(40, 0, 10, 10), 7, [4]float32{0, 0, 1, 1}, 0xFFFFFFFF)

	if len(l.Cmds) != 1 {
		t.Fatalf("%d commands for three quads on one texture, want 1", len(l.Cmds))
	}
	if l.Cmds[0].IdxCount != 18 {
		t.Errorf("merged command has %d indices, want 18", l.Cmds[0].IdxCount)
	}
}

func TestBatchSplitsWhenTheTextureChanges(t *testing.T) {
	l := newList()
	l.AddRect(rect(0, 0, 10, 10), 1, 0xFFFFFFFF)
	l.AddRect(rect(0, 0, 10, 10), 2, 0xFFFFFFFF)
	l.AddRect(rect(0, 0, 10, 10), 1, 0xFFFFFFFF)

	if len(l.Cmds) != 3 {
		t.Fatalf("%d commands, want 3 — a texture change must break the batch", len(l.Cmds))
	}
	for i, c := range l.Cmds {
		if c.IdxCount != 6 {
			t.Errorf("command %d has %d indices, want 6", i, c.IdxCount)
		}
	}
	if l.Cmds[1].IdxOffset != 6 {
		t.Errorf("command 1 starts at index %d, want 6", l.Cmds[1].IdxOffset)
	}
}

func TestBatchSplitsWhenTheClipChanges(t *testing.T) {
	l := newList()
	l.AddRect(rect(0, 0, 10, 10), 1, 0xFFFFFFFF)
	l.PushClip(rect(0, 0, 50, 50))
	l.AddRect(rect(0, 0, 10, 10), 1, 0xFFFFFFFF)
	l.PopClip()
	l.AddRect(rect(0, 0, 10, 10), 1, 0xFFFFFFFF)

	if len(l.Cmds) != 3 {
		t.Fatalf("%d commands, want 3 — the same texture under a different clip cannot batch", len(l.Cmds))
	}
}

func TestClipStackIntersects(t *testing.T) {
	l := newList()
	l.PushClip(rect(0, 0, 100, 100))
	l.PushClip(rect(50, 50, 200, 200))
	if got, want := l.Clip(), (rect(50, 50, 50, 50)); got != want {
		t.Errorf("nested clip is %+v, want %+v", got, want)
	}
	l.PopClip()
	if got, want := l.Clip(), (rect(0, 0, 100, 100)); got != want {
		t.Errorf("after a pop the clip is %+v, want %+v", got, want)
	}
	l.PopClip()
	if got, want := l.Clip(), (rect(0, 0, 1000, 800)); got != want {
		t.Errorf("the base clip is %+v, want the drawable area %+v", got, want)
	}
	// Popping an empty stack must not underflow.
	l.PopClip()
	if l.Clip().W != 1000 {
		t.Error("popping past the base corrupted the clip")
	}
}

func TestZeroSizeQuadsAreDropped(t *testing.T) {
	l := newList()
	l.AddRect(rect(0, 0, 0, 10), 1, 0xFFFFFFFF)
	l.AddRect(rect(0, 0, 10, 0), 1, 0xFFFFFFFF)
	l.AddRect(rect(0, 0, -5, 10), 1, 0xFFFFFFFF)
	if !l.Empty() {
		t.Errorf("degenerate rects produced %d indices", len(l.Idx))
	}
}

func TestNineSliceEmitsNineQuadsWithTheRightUVs(t *testing.T) {
	l := newList()
	// A 64x64 texture with 16px insets, drawn at 200x100.
	l.AddNineSlice(rect(10, 20, 200, 100), 1, 64, 64,
		[4]float32{0, 0, 1, 1}, [4]float32{16, 16, 16, 16}, 0xFFFFFFFF)

	if got := len(l.Idx) / 6; got != 9 {
		t.Fatalf("%d quads, want 9", got)
	}

	// The corners keep their texel size; the edges and centre take the rest.
	xs := map[float32]bool{10: true, 26: true, 194: true, 210: true}
	for _, v := range l.Verts {
		if !xs[v.X] {
			t.Errorf("vertex x %v is not on a slice boundary %v", v.X, xs)
			break
		}
	}
	// The inset UVs are the insets as a fraction of the texture.
	us := map[float32]bool{0: true, 0.25: true, 0.75: true, 1: true}
	for _, v := range l.Verts {
		if !us[v.U] {
			t.Errorf("vertex u %v is not a slice boundary %v", v.U, us)
			break
		}
	}
}

func TestNineSliceHonoursAUVSubRect(t *testing.T) {
	l := newList()
	// The right half of a 64x64 atlas: u from 0.5 to 1.0.
	l.AddNineSlice(rect(0, 0, 200, 100), 1, 64, 64,
		[4]float32{0.5, 0, 0.5, 1}, [4]float32{16, 16, 16, 16}, 0xFFFFFFFF)
	for _, v := range l.Verts {
		if v.U < 0.5-1e-5 || v.U > 1+1e-5 {
			t.Fatalf("a nine-slice sampled u %v, outside its own sub-rect", v.U)
		}
	}
}

func TestNineSliceShrinksInsetsRatherThanInvertingTheCentre(t *testing.T) {
	// A button smaller than its own border must shrink, not turn inside out.
	l := newList()
	l.AddNineSlice(rect(0, 0, 20, 10), 1, 64, 64,
		[4]float32{0, 0, 1, 1}, [4]float32{16, 16, 16, 16}, 0xFFFFFFFF)

	for _, v := range l.Verts {
		if v.X < -1e-5 || v.X > 20+1e-5 || v.Y < -1e-5 || v.Y > 10+1e-5 {
			t.Fatalf("a slice escaped its rect at %v,%v", v.X, v.Y)
		}
	}
	if l.Empty() {
		t.Error("a small nine-slice produced nothing at all")
	}
}

func TestNineSliceWithNoInsetsIsOneQuad(t *testing.T) {
	l := newList()
	l.AddNineSlice(rect(0, 0, 100, 100), 1, 64, 64,
		[4]float32{0, 0, 1, 1}, [4]float32{}, 0xFFFFFFFF)
	if got := len(l.Idx) / 6; got != 1 {
		t.Errorf("%d quads for a zero-inset slice, want the one-quad fast path", got)
	}
}

func TestNineSliceFallsBackWithoutATextureSize(t *testing.T) {
	l := newList()
	l.AddNineSlice(rect(0, 0, 100, 100), 1, 0, 0,
		[4]float32{0, 0, 1, 1}, [4]float32{8, 8, 8, 8}, 0xFFFFFFFF)
	if got := len(l.Idx) / 6; got != 1 {
		t.Errorf("%d quads with an unknown texture size, want one plain quad", got)
	}
}

func TestTextEmitsOneQuadPerVisibleGlyph(t *testing.T) {
	uifont.Reset()
	defer uifont.Reset()
	a := uifont.Face("", 16)

	l := newList()
	l.AddText(a, 3, rect(0, 0, 500, 40), "AB C", ui.AlignLeft, ui.AlignTop, false, 0xFFFFFFFF)

	// Four runes, but the space has no pixels.
	if got := len(l.Idx) / 6; got != 3 {
		t.Errorf("%d glyph quads for %q, want 3 (the space has no ink)", got, "AB C")
	}
	if len(l.Cmds) != 1 {
		t.Errorf("a text run took %d draw calls, want 1", len(l.Cmds))
	}
}

func TestTextAlignmentMovesTheRun(t *testing.T) {
	uifont.Reset()
	defer uifont.Reset()
	a := uifont.Face("", 16)
	const s = "Hi"
	w, _ := uifont.Measure(a, s)
	box := ui.Rect{X: 0, Y: 0, W: 400, H: 100}

	leftX := firstVertexX(t, a, box, s, ui.AlignLeft)
	centerX := firstVertexX(t, a, box, s, ui.AlignCenter)
	rightX := firstVertexX(t, a, box, s, ui.AlignRight)

	if !(leftX < centerX && centerX < rightX) {
		t.Fatalf("alignment did not move the run: %v %v %v", leftX, centerX, rightX)
	}
	if want := (box.W - w) / 2; centerX < want-2 || centerX > want+2 {
		t.Errorf("centred run starts at %v, want about %v", centerX, want)
	}
}

func TestTextVerticalAlignmentMovesTheRun(t *testing.T) {
	uifont.Reset()
	defer uifont.Reset()
	a := uifont.Face("", 16)
	box := ui.Rect{X: 0, Y: 0, W: 400, H: 200}

	top := firstVertexY(t, a, box, ui.AlignTop)
	middle := firstVertexY(t, a, box, ui.AlignMiddle)
	bottom := firstVertexY(t, a, box, ui.AlignBottom)

	if !(top < middle && middle < bottom) {
		t.Fatalf("vertical alignment did not move the run: %v %v %v", top, middle, bottom)
	}
}

func TestWrappedTextStacksLines(t *testing.T) {
	uifont.Reset()
	defer uifont.Reset()
	a := uifont.Face("", 16)
	limit, _ := uifont.Measure(a, "aaa")

	l := newList()
	l.AddText(a, 3, rect(0, 0, limit, 200), "aaa bbb", ui.AlignLeft, ui.AlignTop, true, 0xFFFFFFFF)

	minY, maxY := l.Verts[0].Y, l.Verts[0].Y
	for _, v := range l.Verts {
		if v.Y < minY {
			minY = v.Y
		}
		if v.Y > maxY {
			maxY = v.Y
		}
	}
	if maxY-minY < a.LineHeight {
		t.Errorf("wrapped text spans %v, want at least one line height (%v)", maxY-minY, a.LineHeight)
	}
}

func TestResetKeepsTheBackingArrays(t *testing.T) {
	// The whole point of reusing a list: a steady UI must not allocate per
	// frame.
	l := newList()
	for i := 0; i < 50; i++ {
		l.AddRect(rect(float32(i), 0, 5, 5), uint32(i%3), 0xFFFFFFFF)
	}
	vc, ic, cc := cap(l.Verts), cap(l.Idx), cap(l.Cmds)

	l.Reset(rect(0, 0, 100, 100))
	if !l.Empty() {
		t.Error("Reset left geometry behind")
	}
	if cap(l.Verts) != vc || cap(l.Idx) != ic || cap(l.Cmds) != cc {
		t.Errorf("Reset dropped the buffers: %d/%d/%d -> %d/%d/%d",
			vc, ic, cc, cap(l.Verts), cap(l.Idx), cap(l.Cmds))
	}

	l.AddRect(rect(0, 0, 5, 5), 1, 0xFFFFFFFF)
	if cap(l.Verts) != vc {
		t.Error("the second frame reallocated")
	}
}

func TestAddLineIsPerpendicularlyThick(t *testing.T) {
	l := newList()
	l.AddLine(0, 50, 100, 50, 4, 1, 0xFFFFFFFF)
	if got := len(l.Idx) / 6; got != 1 {
		t.Fatalf("%d quads for a line, want 1", got)
	}
	minY, maxY := l.Verts[0].Y, l.Verts[0].Y
	for _, v := range l.Verts {
		if v.Y < minY {
			minY = v.Y
		}
		if v.Y > maxY {
			maxY = v.Y
		}
	}
	if maxY-minY < 3.9 || maxY-minY > 4.1 {
		t.Errorf("a 4-thick horizontal line spans %v vertically", maxY-minY)
	}
}

func TestAddLineIgnoresAZeroLengthLine(t *testing.T) {
	l := newList()
	l.AddLine(10, 10, 10, 10, 4, 1, 0xFFFFFFFF)
	if !l.Empty() {
		t.Error("a zero-length line produced geometry with an undefined normal")
	}
}

func TestRectOutlineIsFourSidesDrawnInward(t *testing.T) {
	l := newList()
	r := ui.Rect{X: 10, Y: 20, W: 100, H: 50}
	l.AddRectOutline(r, 3, 1, 0xFFFFFFFF)

	if got := len(l.Idx) / 6; got != 4 {
		t.Fatalf("%d quads for an outline, want 4", got)
	}
	for _, v := range l.Verts {
		if v.X < r.X-1e-5 || v.X > r.X+r.W+1e-5 || v.Y < r.Y-1e-5 || v.Y > r.Y+r.H+1e-5 {
			t.Fatalf("the outline escaped its rect at %v,%v — it must be drawn inward", v.X, v.Y)
		}
	}
}

func TestRectOutlineClampsThicknessOnATinyRect(t *testing.T) {
	l := newList()
	l.AddRectOutline(rect(0, 0, 4, 4), 20, 1, 0xFFFFFFFF)
	for _, v := range l.Verts {
		if v.X < -1e-5 || v.X > 4+1e-5 {
			t.Fatalf("a too-thick outline escaped at x %v", v.X)
		}
	}
}

func firstVertexX(t *testing.T, a *uifont.Atlas, box ui.Rect, s string, h ui.HAlign) float32 {
	t.Helper()
	l := newList()
	l.AddText(a, 3, box, s, h, ui.AlignTop, false, 0xFFFFFFFF)
	if len(l.Verts) == 0 {
		t.Fatal("no glyphs")
	}
	return l.Verts[0].X
}

func firstVertexY(t *testing.T, a *uifont.Atlas, box ui.Rect, v ui.VAlign) float32 {
	t.Helper()
	l := newList()
	l.AddText(a, 3, box, "Hi", ui.AlignLeft, v, false, 0xFFFFFFFF)
	if len(l.Verts) == 0 {
		t.Fatal("no glyphs")
	}
	return l.Verts[0].Y
}
