package scorecli

import (
	"bytes"
	"encoding/xml"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/score/io/pdf"
)

func TestExportPDFVerb(t *testing.T) {
	useRepoResources(t)
	dir := t.TempDir()
	var out bytes.Buffer
	if code := exportPDF([]string{"score_demo", "-o", dir, "-png", filepath.Join(dir, "png"), "-dpi", "30"}, &out); code != 0 {
		t.Fatalf("export-pdf: %s", out.String())
	}
	want := []string{"score_demo.pdf", "score_demo_flute.pdf", "score_demo_horn.pdf", "score_demo_violins.pdf", "score_demo_cello.pdf", "score_demo_piano.pdf"}
	for _, name := range want {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		f, err := pdf.Parse(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(f.Pages) == 0 {
			t.Errorf("%s has no pages", name)
		}
		if !strings.Contains(out.String(), name) {
			t.Errorf("output does not name %s:\n%s", name, out.String())
		}
	}
	pngs, _ := filepath.Glob(filepath.Join(dir, "png", "*.png"))
	if len(pngs) < len(want) {
		t.Errorf("%d PNGs", len(pngs))
	}
	if f, err := os.Open(pngs[0]); err == nil {
		if img, err := png.Decode(f); err != nil || img.Bounds().Dx() != 255 {
			t.Errorf("png %v %v (want 255 px wide: letter at 30 dpi)", img.Bounds(), err)
		}
		f.Close()
	}

	// A .pdf -o names the score; parts go beside it; -part picks them.
	out.Reset()
	dst := filepath.Join(dir, "out", "demo.pdf")
	if code := exportPDF([]string{"score_demo", "-o", dst, "-part", "2,piano", "-paper", "a4", "-staff", "6", "-concert"}, &out); code != 0 {
		t.Fatalf("export-pdf -o file: %s", out.String())
	}
	got, _ := filepath.Glob(filepath.Join(dir, "out", "*.pdf"))
	if len(got) != 3 {
		t.Errorf("files %v", got)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "out", "demo_horn.pdf"))
	f, err := pdf.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if w, h, _ := f.MediaBox(f.Pages[0]); int(w+0.5) != 595 || int(h+0.5) != 842 {
		t.Errorf("-paper a4: %gx%g", w, h)
	}
	if f.Info()["Subject"] != "Part: Horn" {
		t.Errorf("info %v", f.Info())
	}
	// Refusals.
	for _, bad := range [][]string{{"-paper", "quarto"}, {"-staff", "40"}, {"-part", "Kazoo"}, {"-score=false", "-parts=false"}, {"-dpi", "0"}} {
		out.Reset()
		if code := exportPDF(append([]string{"score_demo", "-o", dir}, bad...), &out); code == 0 {
			t.Errorf("%v accepted: %s", bad, out.String())
		}
	}
	if code := exportPDF(nil, &out); code != 2 {
		t.Errorf("no score accepted")
	}
}

func TestRenderPagesAndSVG(t *testing.T) {
	useRepoResources(t)
	dir := t.TempDir()
	var out bytes.Buffer
	dst := filepath.Join(dir, "p.png")
	if code := render([]string{"score_demo", "-png", dst, "-page", "2", "-scale", "2"}, &out); code != 0 {
		t.Fatalf("render -page: %s", out.String())
	}
	if !strings.Contains(out.String(), "247x320 px") {
		t.Errorf("a letter page at 2 px a space: %s", out.String())
	}
	out.Reset()
	if code := render([]string{"score_demo", "-png", dst, "-page", "1", "-part", "Cello", "-scale", "2"}, &out); code != 0 {
		t.Fatalf("render -part: %s", out.String())
	}
	svgPath := filepath.Join(dir, "sel.svg")
	out.Reset()
	if code := render([]string{"score_demo", "-svg", svgPath, "-bars", "9-12", "-lines", "1,5"}, &out); code != 0 {
		t.Fatalf("render -svg: %s", out.String())
	}
	data, err := os.ReadFile(svgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := xml.Unmarshal(data, new(struct{})); err != nil || !bytes.Contains(data, []byte("<svg ")) {
		t.Errorf("svg: %v", err)
	}
	for _, bad := range [][]string{{"-png", dst, "-page", "9"}, {"-png", dst, "-page", "1", "-bars", "1-2"}, {"-png", dst, "-part", "1"},
		{"-png", dst, "-page", "1", "-part", "Kazoo"}, {"-png", dst, "-svg", svgPath}} {
		out.Reset()
		if code := render(append([]string{"score_demo"}, bad...), &out); code == 0 {
			t.Errorf("%v accepted: %s", bad, out.String())
		}
	}
}

func TestInspectShowsTheLayout(t *testing.T) {
	repo := useRepoResources(t)
	var out bytes.Buffer
	if code := inspect([]string{filepath.Join(repo, "internal", "score", "engrave", "testdata", "pages.fscore")}, &out); code != 0 {
		t.Fatalf("inspect: %s", out.String())
	}
	for _, want := range []string{"layout letter 216x279 mm, staff 7.0 mm", "2 page(s), 4 system(s) (courtesy accidentals)", "breaks before 9, 13, 17 (page)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("inspect lacks %q:\n%s", want, out.String())
		}
	}
}
