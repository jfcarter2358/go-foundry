package gfx

import (
	"image"
	"testing"
)

func TestDefaultPoolIsAnEighthOfTheCardWithinBounds(t *testing.T) {
	old := NaniteStreamPoolMB
	defer func() { NaniteStreamPoolMB = old }()
	NaniteStreamPoolMB = 256
	for _, c := range []struct{ vram, want int }{{0, 256}, {1024, 256}, {4096, 512}, {8192, 1024}, {24576, 1024}} {
		if got := defaultPoolMB(c.vram); got != c.want {
			t.Errorf("%d MB card: pool %d MB, want %d", c.vram, got, c.want)
		}
	}
}

func TestTextureOpacityIsReadFromTheDecodedImage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	noteTextureOpaque("zz_opaque", img)
	img.Pix[7] = 254
	noteTextureOpaque("zz_alpha", img)
	if o, ok := TextureFullyOpaque("zz_opaque"); !ok || !o {
		t.Errorf("opaque image: %v %v", o, ok)
	}
	if o, ok := TextureFullyOpaque("zz_alpha"); !ok || o {
		t.Errorf("image with one alpha texel: %v %v", o, ok)
	}
	if _, ok := TextureFullyOpaque("zz_never"); ok {
		t.Error("a texture never loaded reports an answer")
	}
}
