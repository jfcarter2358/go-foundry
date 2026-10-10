package raster

// golden_test.go — snapshot tests: every engraving fixture in
// internal/score/engrave/testdata rendered and compared with its reference
// image in testdata/golden, with a tolerance for antialiasing.
//
// Re-capture after an intended change, then look at every image:
//
//	SCORE_ENGRAVE_GOLDEN=update go test ./internal/score/engrave/raster -run Golden
//
// A failing comparison writes what it got beside the reference as
// <name>.got.png (and the difference as <name>.diff.png, red where ink was
// added, blue where it went) in the system temp directory, and says where.

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/score"
	"foundry/internal/score/engrave"
)

// goldenScale is the snapshot resolution: small enough to keep the
// references small, large enough that a moved accidental or a lost dot
// changes hundreds of pixels.
const goldenScale = 8

// goldenCase is one snapshot: a fixture and how to engrave it.
type goldenCase struct {
	name    string // the reference image's name
	fixture string // the .fscore in engrave/testdata
	opt     engrave.Options
}

var goldenCases = []goldenCase{
	{"accidentals", "accidentals", engrave.Options{}},
	{"chords", "chords", engrave.Options{}},
	{"beams", "beams", engrave.Options{}},
	{"meters", "meters", engrave.Options{}},
	{"keys", "keys", engrave.Options{}},
	{"clefs", "clefs", engrave.Options{}},
	{"rests", "rests", engrave.Options{}},
	{"voices", "voices", engrave.Options{}},
	{"dots", "dots", engrave.Options{}},
	{"ties", "ties", engrave.Options{}},
	{"ties_from_bar_2", "ties", engrave.Options{From: 2, To: 3}},
	{"tuplets", "tuplets", engrave.Options{}},
	{"graces", "graces", engrave.Options{}},
	{"grandstaff", "grandstaff", engrave.Options{}},
	{"transposing", "transposing", engrave.Options{}},
	{"transposing_concert", "transposing", engrave.Options{Concert: true}},
	{"demo_bars_9_12", "../../../../resources/sounds/score_demo", engrave.Options{From: 9, To: 12, BarNumbers: 1}},
}

func TestGolden(t *testing.T) {
	update := os.Getenv("SCORE_ENGRAVE_GOLDEN") == "update"
	for _, c := range goldenCases {
		t.Run(c.name, func(t *testing.T) {
			s, err := score.LoadFile(filepath.Join("..", "testdata", c.fixture+score.FileExt))
			if err != nil {
				t.Fatal(err)
			}
			got := Render(engrave.Engrave(s, c.opt), Options{Scale: goldenScale})
			ref := filepath.Join("testdata", "golden", c.name+".png")
			if update {
				if err := writePNG(ref, got); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := readPNG(ref)
			if err != nil {
				t.Fatalf("%v (capture with SCORE_ENGRAVE_GOLDEN=update)", err)
			}
			if msg := compare(want, got); msg != "" {
				dir := os.TempDir()
				_ = writePNG(filepath.Join(dir, c.name+".got.png"), got)
				_ = writePNG(filepath.Join(dir, c.name+".diff.png"), diffImage(want, got))
				t.Errorf("%s: %s (got and diff written to %s)", c.name, msg, dir)
			}
		})
	}
}

// compare reports how two renders differ beyond antialiasing: a size that
// differs by more than a pixel, or more than 0.02% of the pixels (and more
// than 8) changed by more than a quarter of the grey range. The same build
// renders bit for bit the same; the tolerance is for float differences
// between platforms, and a lost dot is 12 pixels at this scale.
func compare(want, got *image.RGBA) string {
	wb, gb := want.Bounds(), got.Bounds()
	if abs(wb.Dx()-gb.Dx()) > 1 || abs(wb.Dy()-gb.Dy()) > 1 {
		return fmt.Sprintf("size %dx%d, want %dx%d", gb.Dx(), gb.Dy(), wb.Dx(), wb.Dy())
	}
	w, h := min(wb.Dx(), gb.Dx()), min(wb.Dy(), gb.Dy())
	bad := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if abs(grey(want.RGBAAt(x, y))-grey(got.RGBAAt(x, y))) > 64 {
				bad++
			}
		}
	}
	if limit := max(8, w*h/5000); bad > limit {
		return fmt.Sprintf("%d pixels differ (limit %d)", bad, limit)
	}
	return ""
}

func grey(c color.RGBA) int { return (int(c.R)*3 + int(c.G)*6 + int(c.B)) / 10 }

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// diffImage shows what moved: red where got has ink want lacks, blue the
// other way, the shared ink grey.
func diffImage(want, got *image.RGBA) *image.RGBA {
	b := want.Bounds().Union(got.Bounds())
	out := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			w, g := 255, 255
			if (image.Point{x, y}).In(want.Bounds()) {
				w = grey(want.RGBAAt(x, y))
			}
			if (image.Point{x, y}).In(got.Bounds()) {
				g = grey(got.RGBAAt(x, y))
			}
			switch {
			case g < w-64:
				out.SetRGBA(x, y, color.RGBA{220, 0, 0, 255})
			case w < g-64:
				out.SetRGBA(x, y, color.RGBA{0, 0, 220, 255})
			default:
				v := uint8(255 - (255-min(w, g))/3)
				out.SetRGBA(x, y, color.RGBA{v, v, v, 255})
			}
		}
	}
	return out
}

func writePNG(path string, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func readPNG(path string) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	out := image.NewRGBA(img.Bounds())
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			out.Set(x, y, img.At(x, y))
		}
	}
	return out, nil
}

// TestGoldenCatchesAChange makes sure the comparison is not so tolerant it
// passes anything: the accidentals fixture with one accidental forced
// differs from its reference.
func TestGoldenCatchesAChange(t *testing.T) {
	s, err := score.LoadFile(filepath.Join("..", "testdata", "accidentals"+score.FileExt))
	if err != nil {
		t.Fatal(err)
	}
	want := Render(engrave.Engrave(s, engrave.Options{}), Options{Scale: goldenScale})
	v := s.Lines[0].Staves[0].Voices[0]
	v.Measures[0].Events[0].Notes[0].Pitch.Alter = 1 // F#4 in D: its natural goes
	got := Render(engrave.Engrave(s, engrave.Options{}), Options{Scale: goldenScale})
	if compare(want, got) == "" {
		t.Error("a lost natural went unnoticed")
	}
	if compare(want, want) != "" {
		t.Error("an image differs from itself")
	}
}

func TestRenderDrawsInkAndText(t *testing.T) {
	s := score.New("t")
	l := score.NewLine("Flute", "woodwinds/flute", "treble")
	v, err := score.ParseVoice("| qC5 qD5 qE5 qF5 | wG5")
	if err != nil {
		t.Fatal(err)
	}
	l.Staves[0].Voices[0] = v
	s.Lines = append(s.Lines, l)
	list := engrave.Engrave(s, engrave.Options{From: 2, To: 2})
	img := Render(list, Options{Scale: 10})
	ink := 0
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i-3] < 128 {
			ink++
		}
	}
	if ink < 500 {
		t.Errorf("only %d inked pixels", ink)
	}
	// The bar number "2" is the list's one text item; it draws.
	var text *engrave.Item
	for i := range list.Items {
		if list.Items[i].Kind == engrave.ItemText {
			text = &list.Items[i]
		}
	}
	if text == nil || !strings.Contains(text.Text, "2") {
		t.Fatalf("no bar number: %+v", text)
	}
	clip := Render(list, Options{Scale: 10, Clip: engrave.Rect{Min: engrave.Point{X: text.P.X - 0.5, Y: text.P.Y - 1.5}, Max: engrave.Point{X: text.P.X + 1.5, Y: text.P.Y + 0.5}}})
	dark := 0
	for i := 0; i < len(clip.Pix); i += 4 {
		if clip.Pix[i] < 128 {
			dark++
		}
	}
	if dark < 5 {
		t.Error("the bar number did not draw")
	}
}
