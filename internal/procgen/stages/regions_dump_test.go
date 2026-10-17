package stages

import (
	"image"
	"image/png"
	"os"
	"testing"

	"foundry/internal/procgen"
)

// dumpOverlay writes an overlay layer as a PNG, for looking at
// (FOUNDRY_REGIONS_DUMP=<path> go test -run TestGate).
func dumpOverlay(t *testing.T, im *procgen.ImageLayer, path string) {
	img := image.NewNRGBA(image.Rect(0, 0, im.W, im.H))
	copy(img.Pix, im.RGBA)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s (%dx%d)", path, im.W, im.H)
}
