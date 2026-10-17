package texcompress

// texcompress_test.go — the encoders' quality floors, the block and mip
// bookkeeping, and the cache's round trip.

import (
	"bytes"
	"image"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// smoothImage is a colour gradient with low-frequency waves on it — the kind
// of content BC7 mode 6 is for — opaque.
func smoothImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fx, fy := float64(x)/float64(w), float64(y)/float64(h)
			o := y*img.Stride + x*4
			img.Pix[o+0] = uint8(255 * fx)
			img.Pix[o+1] = uint8(127 + 120*math.Sin(fy*9))
			img.Pix[o+2] = uint8(127 + 120*math.Cos((fx+fy)*7))
			img.Pix[o+3] = 255
		}
	}
	return img
}

// noisyImage is smoothImage with per-texel noise of the given amplitude,
// standing in for photographic grain.
func noisyImage(w, h int, amp float64, seed int64) *image.RGBA {
	img := smoothImage(w, h)
	r := rand.New(rand.NewSource(seed))
	for i := 0; i < len(img.Pix); i += 4 {
		for c := 0; c < 3; c++ {
			v := float64(img.Pix[i+c]) + (r.Float64()*2-1)*amp
			img.Pix[i+c] = uint8(min(max(v, 0), 255))
		}
	}
	return img
}

// psnr compares a and b over the given channels.
func psnr(a, b *image.RGBA, channels int) float64 {
	var se float64
	var n int
	for y := 0; y < a.Rect.Dy(); y++ {
		for x := 0; x < a.Rect.Dx(); x++ {
			oa, ob := y*a.Stride+x*4, y*b.Stride+x*4
			for c := 0; c < channels; c++ {
				d := float64(a.Pix[oa+c]) - float64(b.Pix[ob+c])
				se += d * d
				n++
			}
		}
	}
	if se == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(255*255/(se/float64(n)))
}

func encodeDecode0(t *testing.T, img *image.RGBA, f Format) *image.RGBA {
	t.Helper()
	tex, err := Encode(img, f, 4)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := DecodeLevel(f, tex.Levels[0])
	if err != nil {
		t.Fatal(err)
	}
	return dec
}

func TestBC7SmoothContentIsAbove40dB(t *testing.T) {
	img := smoothImage(256, 256)
	if p := psnr(img, encodeDecode0(t, img, BC7), 4); p < 40 {
		t.Errorf("BC7 PSNR on smooth content = %.2f dB, want >= 40", p)
	} else {
		t.Logf("BC7 smooth PSNR %.2f dB", p)
	}
}

func TestBC7GrainyContentIsAbove32dB(t *testing.T) {
	img := noisyImage(256, 256, 12, 1)
	if p := psnr(img, encodeDecode0(t, img, BC7), 4); p < 32 {
		t.Errorf("BC7 PSNR on grainy content = %.2f dB, want >= 32", p)
	} else {
		t.Logf("BC7 grainy PSNR %.2f dB", p)
	}
}

// A cutout's alpha survives: a hard 0/255 edge stays on the right side of
// one half, and a ramp stays a ramp.
func TestBC7KeepsAlpha(t *testing.T) {
	img := smoothImage(64, 64)
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			a := uint8(0)
			if x >= 32 {
				a = 255
			}
			if y >= 48 {
				a = uint8(x * 4)
			}
			img.Pix[y*img.Stride+x*4+3] = a
		}
	}
	dec := encodeDecode0(t, img, BC7)
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			got := dec.Pix[y*dec.Stride+x*4+3]
			if (x >= 32) != (got >= 128) {
				t.Fatalf("alpha at (%d,%d) = %d, the cutout edge moved", x, y, got)
			}
		}
	}
	if p := psnr(img, dec, 4); p < 35 {
		t.Errorf("BC7 PSNR with alpha = %.2f dB, want >= 35", p)
	}
}

func TestBC7SolidBlockIsWithinOne(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := 0; i < len(img.Pix); i += 4 {
		copy(img.Pix[i:], []byte{37, 100, 201, 255})
	}
	dec := encodeDecode0(t, img, BC7)
	for i := 0; i < len(img.Pix); i++ {
		if d := int(img.Pix[i]) - int(dec.Pix[i]); d < -1 || d > 1 {
			t.Fatalf("solid block channel %d: %d -> %d", i%4, img.Pix[i], dec.Pix[i])
		}
	}
}

// normalField is a bumpy tangent-space normal map, encoded the usual way
// (n * 0.5 + 0.5 in RGB).
func normalField(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fx, fy := float64(x)/16, float64(y)/23
			dx := 0.6 * math.Cos(fx) * math.Cos(fy*0.7)
			dy := 0.5 * math.Sin(fy) * math.Sin(fx*1.3)
			l := math.Sqrt(dx*dx + dy*dy + 1)
			n := [3]float64{-dx / l, -dy / l, 1 / l}
			o := y*img.Stride + x*4
			for c := 0; c < 3; c++ {
				img.Pix[o+c] = uint8(math.Round((n[c]*0.5 + 0.5) * 255))
			}
			img.Pix[o+3] = 255
		}
	}
	return img
}

// BC5 keeps x and y; the rebuilt z (as fragment.glsl does it) lands the
// normal within a degree on average and a few at worst.
func TestBC5NormalAngularError(t *testing.T) {
	img := normalField(128, 128)
	dec := encodeDecode0(t, img, BC5)
	var sum, worst float64
	n := 0
	for i := 0; i < len(img.Pix); i += 4 {
		src := unpackNormal(img.Pix[i], img.Pix[i+1], img.Pix[i+2], false)
		got := unpackNormal(dec.Pix[i], dec.Pix[i+1], 0, true)
		ang := math.Acos(min(1, src[0]*got[0]+src[1]*got[1]+src[2]*got[2])) * 180 / math.Pi
		sum += ang
		worst = max(worst, ang)
		n++
	}
	mean := sum / float64(n)
	t.Logf("BC5 normal error: mean %.3f°, worst %.3f°", mean, worst)
	if mean > 1 || worst > 5 {
		t.Errorf("BC5 normal error mean %.3f° worst %.3f°, want <= 1° and <= 5°", mean, worst)
	}
}

func unpackNormal(r, g, b uint8, rebuildZ bool) [3]float64 {
	x := float64(r)/255*2 - 1
	y := float64(g)/255*2 - 1
	z := float64(b)/255*2 - 1
	if rebuildZ {
		z = math.Sqrt(max(0, 1-x*x-y*y))
	}
	l := math.Sqrt(x*x + y*y + z*z)
	return [3]float64{x / l, y / l, z / l}
}

// An odd-sized texture's chain halves with the floor down to 1x1, every level
// holds exactly ceil(w/4)*ceil(h/4) blocks, and every level decodes.
func TestOddSizesAndMipsDownTo1x1(t *testing.T) {
	for _, f := range []Format{BC7, BC5} {
		img := noisyImage(37, 19, 20, 2)
		tex, err := Encode(img, f, 3)
		if err != nil {
			t.Fatal(err)
		}
		if len(tex.Levels) != MipCount(37, 19) || MipCount(37, 19) != 6 {
			t.Fatalf("%v: %d levels, want %d (6)", f, len(tex.Levels), MipCount(37, 19))
		}
		w, h := 37, 19
		for i, l := range tex.Levels {
			if l.W != w || l.H != h {
				t.Errorf("%v level %d is %dx%d, want %dx%d", f, i, l.W, l.H, w, h)
			}
			if len(l.Data) != ((w+3)/4)*((h+3)/4)*16 {
				t.Errorf("%v level %d holds %d bytes", f, i, len(l.Data))
			}
			if _, err := DecodeLevel(f, l); err != nil {
				t.Errorf("%v level %d: %v", f, i, err)
			}
			w, h = max(1, w/2), max(1, h/2)
		}
		if last := tex.Levels[len(tex.Levels)-1]; last.W != 1 || last.H != 1 {
			t.Errorf("%v chain ends at %dx%d", f, last.W, last.H)
		}
	}
}

func TestDownsampleIsABoxFilter(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	copy(img.Pix, []byte{0, 10, 100, 255, 4, 20, 100, 255, 8, 30, 100, 255, 12, 40, 101, 255})
	d := Downsample(img)
	if d.Rect.Dx() != 1 || d.Rect.Dy() != 1 {
		t.Fatalf("2x2 halved to %v", d.Rect)
	}
	if want := []byte{6, 25, 100, 255}; !bytes.Equal(d.Pix, want) {
		t.Errorf("box of 2x2 = %v, want %v", d.Pix, want)
	}
}

// The second load of the same bytes comes off disk with no encode; a changed
// source misses; a damaged file misses rather than failing.
func TestCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(CacheDirEnv, dir)
	img := smoothImage(64, 32)
	src := []byte("pretend png bytes")

	first, hit, serr, err := LoadOrEncode(src, img, BC7, 2)
	if err != nil || serr != nil {
		t.Fatal(err, serr)
	}
	if hit {
		t.Fatal("an empty cache hit")
	}
	second, hit, _, err := LoadOrEncode(src, img, BC7, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !hit {
		t.Fatal("the second load encoded again")
	}
	if len(first.Levels) != len(second.Levels) {
		t.Fatal("level count changed through the cache")
	}
	for i := range first.Levels {
		if !bytes.Equal(first.Levels[i].Data, second.Levels[i].Data) {
			t.Fatalf("level %d changed through the cache", i)
		}
	}

	// Another format is another entry.
	if _, hit, _, _ := LoadOrEncode(src, img, BC5, 2); hit {
		t.Error("a BC5 load was served the BC7 entry")
	}
	// The source changed: stale, so a miss.
	if _, hit, _, _ := LoadOrEncode([]byte("edited png bytes"), img, BC7, 2); hit {
		t.Error("an edited source was served the old entry")
	}
	// A damaged entry is a miss, and is rewritten whole.
	path := CachePath(dir, CacheKey(src, BC7))
	if err := os.WriteFile(path, []byte("FTXC garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, hit, _, err := LoadOrEncode(src, img, BC7, 2); hit || err != nil {
		t.Errorf("a damaged entry: hit=%v err=%v", hit, err)
	}
	if _, err := LoadCached(dir, CacheKey(src, BC7), BC7); err != nil {
		t.Errorf("the damaged entry was not rewritten: %v", err)
	}
	// No temp files left behind.
	tmps, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(tmps) != 0 {
		t.Errorf("temp files left: %v", tmps)
	}
}
