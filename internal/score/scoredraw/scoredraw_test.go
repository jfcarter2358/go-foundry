package scoredraw

import (
	"image"
	"math"
	"testing"

	"github.com/inkyblackness/imgui-go/v4"

	"foundry/internal/score"
	"foundry/internal/score/engrave"
	"foundry/internal/score/glyph"
)

// imgui's draw list is a Canvas.
var _ Canvas = imgui.DrawList(0)

type rec struct {
	images, lines, tris, texts int
	area                       float32
	last                       [2]imgui.Vec2
	rects                      [][2]imgui.Vec2
	cols                       []imgui.PackedColor
}

func (r *rec) AddImageV(_ imgui.TextureID, a, b, _, _ imgui.Vec2, c imgui.PackedColor) {
	r.images++
	r.last = [2]imgui.Vec2{a, b}
	r.cols = append(r.cols, c)
}
func (r *rec) AddLineV(_, _ imgui.Vec2, c imgui.PackedColor, w float32) {
	r.lines++
	if w < 1 {
		panic("a line thinner than a pixel")
	}
}
func (r *rec) AddRectFilled(a, b imgui.Vec2, c imgui.PackedColor) {
	r.lines++
	r.rects = append(r.rects, [2]imgui.Vec2{a, b})
	if b.X-a.X < 1 || b.Y-a.Y < 1 {
		panic("a rectangle thinner than a pixel")
	}
}
func (r *rec) AddTriangleFilled(a, b, c imgui.Vec2, _ imgui.PackedColor) {
	r.tris++
	r.area += float32(math.Abs(float64((b.X-a.X)*(c.Y-a.Y)-(b.Y-a.Y)*(c.X-a.X)))) / 2
}
func (r *rec) AddText(imgui.Vec2, imgui.PackedColor, string) { r.texts++ }

func testRenderer() (*Renderer, *int) {
	uploads := 0
	r := New()
	r.Upload = func(a *glyph.Atlas, old uint32) uint32 {
		uploads++
		if old != 0 {
			return old
		}
		return 7
	}
	r.UploadText = func(_ *image.RGBA, old uint32) uint32 {
		if old != 0 {
			return old
		}
		return 9
	}
	r.Delete = func(uint32) {}
	return r, &uploads
}

func demo(t *testing.T) *engrave.List {
	s := score.New("t")
	l := score.NewLine("Piano", "keys/grand_piano", "treble")
	v, err := score.ParseVoice("| eC5 eD5~ eD5 eF#5 qG5 q.A5 | R")
	if err != nil {
		t.Fatal(err)
	}
	l.Staves[0].Voices[0] = v
	s.Lines = append(s.Lines, l)
	return engrave.Engrave(s, engrave.Options{BarNumbers: 1})
}

func TestDrawEmitsEveryItem(t *testing.T) {
	r, uploads := testRenderer()
	list := demo(t)
	var want [4]int
	for _, it := range list.Items {
		switch it.Kind {
		case engrave.ItemGlyph:
			want[0]++
		case engrave.ItemLine:
			want[1]++
		case engrave.ItemText:
			want[3]++
		}
	}
	c := &rec{}
	r.Draw(c, list, View{Origin: imgui.Vec2{X: 10, Y: 20}, Scale: 9, Ink: imgui.PackedColor(0xff000000)})
	// Text runs are quads off the text page, like glyphs.
	if c.images != want[0]+want[3] || c.lines != want[1] || c.texts != 0 || want[3] == 0 {
		t.Errorf("drew %d glyph and text quads, %d lines, %d imgui texts, want %v", c.images, c.lines, c.texts, want)
	}
	if c.tris == 0 || c.area <= 0 {
		t.Error("beams and ties drew no triangles")
	}
	if *uploads != 1 || r.Texture(9) != 7 {
		t.Errorf("%d uploads, texture %d", *uploads, r.Texture(9))
	}
	// Again at the same zoom: nothing new to bake, nothing to upload.
	r.Draw(&rec{}, list, View{Scale: 9.2})
	if *uploads != 1 {
		t.Errorf("a second frame uploaded again (%d)", *uploads)
	}
	// Another zoom bucket is another page.
	r.Draw(&rec{}, list, View{Scale: 30})
	if *uploads != 2 {
		t.Errorf("a new zoom made %d uploads", *uploads)
	}
}

func TestDrawPlacesGlyphQuads(t *testing.T) {
	r, _ := testRenderer()
	s := score.New("t")
	l := score.NewLine("L", "keys/grand_piano", "treble")
	l.Staves[0].Voices[0], _ = score.ParseVoice("| wC5")
	s.Lines = append(s.Lines, l)
	list := engrave.Engrave(s, engrave.Options{BarNumbers: -1})
	var head engrave.Item
	for _, it := range list.Items {
		if it.Role == engrave.RoleNotehead {
			head = it
		}
	}
	only := []engrave.Item{head}
	c := &rec{}
	r.Draw(c, &engrave.List{Items: only}, View{Origin: imgui.Vec2{X: 100, Y: 50}, Scale: 20})
	// The quad holds the head's ink at 20 px a space.
	b := engrave.ItemBounds(&head)
	x0, y0 := 100+float32(b.Min.X)*20, 50+float32(b.Min.Y)*20
	x1, y1 := 100+float32(b.Max.X)*20, 50+float32(b.Max.Y)*20
	if !(c.last[0].X <= x0 && c.last[0].Y <= y0 && c.last[1].X >= x1 && c.last[1].Y >= y1) {
		t.Errorf("quad %v does not hold the ink %v..%v", c.last, imgui.Vec2{X: x0, Y: y0}, imgui.Vec2{X: x1, Y: y1})
	}
	if c.last[1].X-c.last[0].X > float32(b.W())*20+6 {
		t.Errorf("quad %v is far wider than the ink", c.last)
	}
}

func TestDrawClipsAndColours(t *testing.T) {
	r, _ := testRenderer()
	list := demo(t)
	all := &rec{}
	r.Draw(all, list, View{Scale: 9})
	bar2 := list.Bar(2)
	some := &rec{}
	r.Draw(some, list, View{Scale: 9, Clip: engrave.Rect{Min: engrave.Point{X: bar2.X0, Y: -100}, Max: engrave.Point{X: bar2.X1, Y: 100}},
		Colour: func(it *engrave.Item) imgui.PackedColor {
			if it.Role == engrave.RoleRest {
				return 0xff0000ff
			}
			return 0xff000000
		}})
	if some.images >= all.images || some.images == 0 {
		t.Errorf("clipped to bar 2: %d glyphs of %d", some.images, all.images)
	}
	red := 0
	for _, c := range some.cols {
		if c == 0xff0000ff {
			red++
		}
	}
	if red != 1 {
		t.Errorf("%d red glyphs, want the bar rest", red)
	}
	if withAlpha(0xff112233, 0.5)>>24 != 0x80 {
		t.Error("withAlpha")
	}
}

// Horizontal and vertical lines land on whole pixels, at least one thick;
// a slanted one stays a line.
func TestDrawSnapsStraightLines(t *testing.T) {
	r, _ := testRenderer()
	list := &engrave.List{Items: []engrave.Item{
		{Kind: engrave.ItemLine, P: engrave.Point{X: 1, Y: 2.03}, Q: engrave.Point{X: 9, Y: 2.03}, Width: 0.13},
		{Kind: engrave.ItemLine, P: engrave.Point{X: 3.07, Y: 1}, Q: engrave.Point{X: 3.07, Y: 4.5}, Width: 0.12},
		{Kind: engrave.ItemLine, P: engrave.Point{X: 1, Y: 1}, Q: engrave.Point{X: 5, Y: 3}, Width: 0.12},
	}}
	c := &rec{}
	r.Draw(c, list, View{Origin: imgui.Vec2{X: 10.3, Y: 20.6}, Scale: 9})
	if c.lines != 3 || len(c.rects) != 2 {
		t.Fatalf("%d lines, %d of them rectangles", c.lines, len(c.rects))
	}
	for _, rc := range c.rects {
		for _, v := range []float32{rc[0].X, rc[0].Y, rc[1].X, rc[1].Y} {
			if v != float32(math.Round(float64(v))) {
				t.Errorf("rectangle %v is off the pixel grid", rc)
			}
		}
	}
	h := c.rects[0]
	if h[1].Y-h[0].Y != 1 || math.Abs(float64(h[0].Y+0.5-(20.6+2.03*9))) > 0.5 {
		t.Errorf("staff line %v: want one row at y %.2f", h, 20.6+2.03*9)
	}
	if v := c.rects[1]; v[1].X-v[0].X != 1 || v[1].Y-v[0].Y < 31 {
		t.Errorf("stem %v", v)
	}
	// A thick line keeps its width.
	if a, b, ok := pixelRect(imgui.Vec2{X: 0, Y: 10}, imgui.Vec2{X: 20, Y: 10}, 3.4); !ok || b.Y-a.Y != 3 {
		t.Errorf("3.4 px line = %v..%v", a, b)
	}
}

// A text run is set in the layout's face at the view's size: its quad is
// as wide as the face measures it, it scales with the zoom, a centred or
// right-aligned run hangs from its anchor, and the page holds its ink in
// white with coverage in alpha.
func TestDrawSetsTextAtItsSize(t *testing.T) {
	r, _ := testRenderer()
	it := engrave.Item{Kind: engrave.ItemText, Text: "Allegro", Font: engrave.TextBold, Size: 2, P: engrave.Point{X: 10, Y: 5}}
	draw := func(scale float32, align engrave.Align) [2]imgui.Vec2 {
		it.Align = align
		c := &rec{}
		r.Draw(c, &engrave.List{Items: []engrave.Item{it}}, View{Scale: scale})
		if c.images != 1 {
			t.Fatalf("%d quads for one run", c.images)
		}
		return c.last
	}
	q := draw(10, engrave.AlignLeft)
	want := float32(engrave.TextWidthFont("Allegro", engrave.TextBold, 2) * 10)
	if w := q[1].X - q[0].X; w < want-2 || w > want+6 {
		t.Errorf("quad %.1f px wide, the face measures %.1f", w, want)
	}
	if q[0].X > 100+1 || q[1].Y < 50 || q[0].Y > 50-10 {
		t.Errorf("quad %v does not hang from the baseline at (100, 50)", q)
	}
	big := draw(20, engrave.AlignLeft)
	if w, w2 := q[1].X-q[0].X, big[1].X-big[0].X; w2 < 1.8*w {
		t.Errorf("at twice the zoom the run is %.1f px, was %.1f", w2, w)
	}
	right := draw(10, engrave.AlignRight)
	if right[1].X > 100+2 || right[1].X < 100-4 {
		t.Errorf("a right-aligned run ends at %.1f, want about 100", right[1].X)
	}
	cen := draw(10, engrave.AlignCenter)
	if mid := (cen[0].X + cen[1].X) / 2; mid < 97 || mid > 103 {
		t.Errorf("a centred run's middle is %.1f", mid)
	}
	ink := 0
	for i := 3; i < len(r.text.img.Pix); i += 4 {
		if a := r.text.img.Pix[i]; a > 0 {
			ink++
			if r.text.img.Pix[i-1] != 255 {
				t.Fatal("text page pixels are premultiplied, want white with coverage")
			}
		}
	}
	if ink < 50 {
		t.Errorf("the page holds only %d inked pixels", ink)
	}
}
