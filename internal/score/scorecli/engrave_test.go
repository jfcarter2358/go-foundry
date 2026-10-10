package scorecli

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderPNG(t *testing.T) {
	useRepoResources(t)
	dir := t.TempDir()
	dst := filepath.Join(dir, "demo.png")
	var out bytes.Buffer
	if code := render([]string{"score_demo", "-png", dst, "-bars", "5-8", "-scale", "6", "-lines", "1,5", "-concert"}, &out); code != 0 {
		t.Fatalf("render -png: %s", out.String())
	}
	f, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() < 200 || b.Dy() < 60 {
		t.Errorf("image %v", b)
	}
	if !strings.Contains(out.String(), "4 bars, 3 staves") || !strings.Contains(out.String(), "hash ") {
		t.Errorf("output: %s", out.String())
	}
	// One bar is a range of one; bad ranges and lines are refused.
	out.Reset()
	if code := render([]string{"score_demo", "-png", dst, "-bars", "3"}, &out); code != 0 || !strings.Contains(out.String(), "1 bars") {
		t.Errorf("-bars 3: %d %s", code, out.String())
	}
	for _, bad := range [][]string{{"-bars", "8-5"}, {"-bars", "99"}, {"-bars", "x"}, {"-lines", "9"}, {"-scale", "0"}} {
		out.Reset()
		args := append([]string{"score_demo", "-png", dst}, bad...)
		if code := render(args, &out); code == 0 {
			t.Errorf("%v accepted: %s", bad, out.String())
		}
	}
}
