package render

// nanite_vis_dump_test.go — the pictures nanite_vis_gl_test.go compares, kept
// for a person to look at: set FOUNDRY_TEST_DUMP to a directory and each
// comparison's frames are written there as PNGs. Unset, nothing is written.

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
)

// dumpPNG writes an RGBA readback (bottom row first) to
// $FOUNDRY_TEST_DUMP/name.png, opaque.
func dumpPNG(name string, px []uint8, size int) {
	dir := os.Getenv("FOUNDRY_TEST_DUMP")
	if dir == "" {
		return
	}
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		copy(img.Pix[y*size*4:(y+1)*size*4], px[(size-1-y)*size*4:(size-y)*size*4])
	}
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		return
	}
	png.Encode(f, img)
	f.Close()
}
