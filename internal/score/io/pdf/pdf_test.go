package pdf

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"foundry/internal/score"
	"foundry/internal/score/engrave"
	"foundry/internal/score/engrave/raster"
)

func demo(t *testing.T) *score.Score {
	t.Helper()
	s, err := score.LoadFile(filepath.Join("..", "..", "..", "..", "resources", "sounds", "score_demo.fscore"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var fixed = time.Date(2026, 10, 16, 9, 30, 0, 0, time.FixedZone("x", -5*3600))

func writeBook(t *testing.T, b *engrave.Book, o Options) (*File, []byte) {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteBook(&buf, b, o); err != nil {
		t.Fatal(err)
	}
	f, err := Parse(buf.Bytes())
	if err != nil {
		t.Fatalf("the written PDF does not parse: %v", err)
	}
	return f, buf.Bytes()
}

func TestAScoreWritesAndParsesBack(t *testing.T) {
	s := demo(t)
	b := engrave.ScoreBook(s, false)
	f, data := writeBook(t, b, Options{Info: Info{Created: fixed, Keywords: "test"}})
	if len(f.Pages) != len(b.Pages) || len(b.Pages) < 2 {
		t.Fatalf("%d pages in the PDF, %d laid out", len(f.Pages), len(b.Pages))
	}
	if f.Version != "1.4" {
		t.Errorf("version %q", f.Version)
	}
	for i, pg := range f.Pages {
		w, h, err := f.MediaBox(pg)
		if err != nil || math.Abs(w-612) > 0.01 || math.Abs(h-792) > 0.01 {
			t.Errorf("page %d MediaBox %gx%g (%v), want letter 612x792", i+1, w, h, err)
		}
		c, err := f.Contents(pg)
		if err != nil || !bytes.Contains(c, []byte(" Do Q")) {
			t.Errorf("page %d contents: %v", i+1, err)
		}
	}
	in := f.Info()
	if in["Title"] != "Score Demo" || in["Author"] != "Foundry" || in["Subject"] != "Full score" || in["Keywords"] != "test" ||
		in["Creator"] == "" || in["Producer"] == "" || in["CreationDate"] != "D:20261016093000-05'00'" {
		t.Errorf("info %v", in)
	}
	// Every glyph is one form, used by reference.
	res := f.Dict(f.Pages[0]["Resources"])
	xo := f.Dict(res["XObject"])
	if len(xo) < 20 {
		t.Errorf("only %d forms", len(xo))
	}
	notehead := 0
	for i := range f.Pages {
		c, _ := f.Contents(f.Pages[i])
		notehead += bytes.Count(c, []byte("/GnoteheadBlack Do"))
	}
	if notehead < 50 || bytes.Count(data, []byte("/Subtype /Form")) != len(xo) {
		t.Errorf("%d black noteheads drawn; %d forms written, %d named", notehead, bytes.Count(data, []byte("/Subtype /Form")), len(xo))
	}
	// No fonts in the file: text is outlines.
	if bytes.Contains(data, []byte("/Font")) {
		t.Errorf("a font in the PDF")
	}
	// The same pages write the same file (the ID too).
	_, again := writeBook(t, b, Options{Info: Info{Created: fixed, Keywords: "test"}})
	if !bytes.Equal(data, again) {
		t.Errorf("two writes of the same book differ")
	}
}

func TestPartsMetadataAndUnicode(t *testing.T) {
	s := demo(t)
	s.Title = "Hangar — Thème ♯"
	b := engrave.PartBook(s, 2, false)
	f, _ := writeBook(t, b, Options{Info: Info{Created: fixed}})
	in := f.Info()
	if in["Title"] != "Hangar — Thème ♯ — Violins" || in["Subject"] != "Part: Violins" {
		t.Errorf("info %q / %q", in["Title"], in["Subject"])
	}
	if pdfString("a(b)\\c") != `(a\(b\)\\c)` {
		t.Errorf("escapes: %s", pdfString("a(b)\\c"))
	}
}

// TestThePDFDrawsWhatTheListDraws rasterises every page of the PDF with
// the package's own interpreter and compares it with the same page drawn
// straight from the display list by the PNG backend.
func TestThePDFDrawsWhatTheListDraws(t *testing.T) {
	s := demo(t)
	for _, which := range []string{"score", "part"} {
		b := engrave.ScoreBook(s, false)
		if which == "part" {
			b = engrave.PartBook(s, 4, false)
		}
		f, _ := writeBook(t, b, Options{Info: Info{Created: fixed}})
		const pxPerPt = 1.4
		for i, pg := range b.Pages {
			got, err := Rasterize(f, i, pxPerPt)
			if err != nil {
				t.Fatalf("%s page %d: %v", which, i+1, err)
			}
			want := raster.Render(b.List(pg), raster.Options{Scale: pxPerPt * b.Space * PointsPerMM})
			if got.Rect.Dx() != want.Rect.Dx() || got.Rect.Dy() != want.Rect.Dy() {
				// Ceil of the same product two ways: at most a pixel.
				if abs(got.Rect.Dx()-want.Rect.Dx()) > 1 || abs(got.Rect.Dy()-want.Rect.Dy()) > 1 {
					t.Fatalf("%s page %d: %v vs %v", which, i+1, got.Rect, want.Rect)
				}
			}
			off, ink := diff(got, want)
			if ink < 2000 {
				t.Errorf("%s page %d: only %d inked pixels", which, i+1, ink)
			}
			if float64(off) > 0.01*float64(ink) {
				t.Errorf("%s page %d: %d of %d inked pixels differ", which, i+1, off, ink)
				if dir := os.Getenv("SCORE_PDF_DIFF"); dir != "" {
					savePNG(t, filepath.Join(dir, fmt.Sprintf("%s_%d_pdf.png", which, i+1)), got)
					savePNG(t, filepath.Join(dir, fmt.Sprintf("%s_%d_list.png", which, i+1)), want)
				}
			}
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// diff counts pixels more than a quarter of the grey range apart, against
// the pixels either image inks.
func diff(a, b *image.RGBA) (off, ink int) {
	w, h := min(a.Rect.Dx(), b.Rect.Dx()), min(a.Rect.Dy(), b.Rect.Dy())
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			ga := int(a.Pix[a.PixOffset(x, y)])
			gb := int(b.Pix[b.PixOffset(x, y)])
			if ga < 200 || gb < 200 {
				ink++
			}
			if d := ga - gb; d > 64 || d < -64 {
				off++
			}
		}
	}
	return
}

func TestDamagedFilesAreRefused(t *testing.T) {
	s := demo(t)
	var buf bytes.Buffer
	if err := WriteBook(&buf, engrave.PartBook(s, 0, false), Options{Info: Info{Created: fixed}}); err != nil {
		t.Fatal(err)
	}
	good := buf.Bytes()
	cases := map[string]func([]byte) []byte{
		"no header": func(b []byte) []byte { return b[1:] },
		"truncated": func(b []byte) []byte { return b[:len(b)/2] },
		"no eof":    func(b []byte) []byte { return bytes.TrimSuffix(b, []byte("%%EOF\n")) },
		"shifted": func(b []byte) []byte {
			// One byte more before every object: every offset is wrong.
			i := bytes.Index(b, []byte("1 0 obj"))
			return append(append(append([]byte(nil), b[:i]...), ' '), b[i:]...)
		},
		"length": func(b []byte) []byte {
			i := bytes.Index(b, []byte("/Length "))
			c := append([]byte(nil), b...)
			c[i+8] = '9'
			if b[i+8] == '9' {
				c[i+8] = '1'
			}
			return c
		},
		"size": func(b []byte) []byte { return bytes.Replace(b, []byte("/Size "), []byte("/Size 1"), 1) },
	}
	for name, damage := range cases {
		if _, err := Parse(damage(good)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
	// A content stream with an operator the writer never uses is refused
	// when drawn.
	f, err := Parse(good)
	if err != nil {
		t.Fatal(err)
	}
	st := f.Resolve(f.Pages[0]["Contents"]).(*Stream)
	st.Data = append([]byte("BT "), st.Data...)
	if _, err := Rasterize(f, 0, 1); err == nil || !strings.Contains(err.Error(), "BT") {
		t.Errorf("an unknown operator drew: %v", err)
	}
}

func TestUncompressedStreamsAreReadable(t *testing.T) {
	s := demo(t)
	var buf bytes.Buffer
	if err := WriteBook(&buf, engrave.PartBook(s, 0, false), Options{NoCompress: true, Info: Info{Created: fixed}}); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(buf.Bytes(), []byte("FlateDecode")) || !bytes.Contains(buf.Bytes(), []byte(" cm /G")) {
		t.Errorf("streams are not plain")
	}
	if _, err := Parse(buf.Bytes()); err != nil {
		t.Fatal(err)
	}
}

func TestWriteRefusesNoPages(t *testing.T) {
	if err := Write(&bytes.Buffer{}, nil, Options{}); err == nil {
		t.Errorf("no pages written")
	}
	if err := Write(&bytes.Buffer{}, []Page{{List: &engrave.List{}}}, Options{}); err == nil {
		t.Errorf("a page with no size written")
	}
}

func TestNumbers(t *testing.T) {
	for v, want := range map[float64]string{0: "0", -0.00001: "0", 1.5: "1.5", 2: "2", -3.25: "-3.25", 1.23456789: "1.2346", math.NaN(): "0"} {
		if got := num(v); got != want {
			t.Errorf("num(%v) = %q, want %q", v, got, want)
		}
	}
}

func savePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := pngEncode(f, img); err != nil {
		t.Fatal(err)
	}
}

func pngEncode(w io.Writer, img image.Image) error { return png.Encode(w, img) }
