package texcompress

// encode.go — a whole texture: the mip chain, every level's blocks, in
// parallel.
//
// The work is embarrassingly parallel — every 4x4 block is encoded on its own
// — so each level is cut into rows of blocks and handed to a bounded set of
// goroutines. The bound matters on a shared machine: a 4096² texture is a
// million blocks, and one goroutine per block row is plenty to keep every
// core busy without the scheduler holding thousands of them.
//
// Edge blocks of a level whose size is not a multiple of four (and the 2x2
// and 1x1 levels at the bottom of every chain) are padded by repeating the
// last column and row: the padding is never sampled, and repeating real
// texels keeps it from pulling the block's endpoints toward a colour that is
// not there.

import (
	"fmt"
	"image"
	"runtime"
	"sync"
)

// Encode builds img's mip chain and encodes every level in format. workers
// bounds the goroutines used (0 means GOMAXPROCS).
func Encode(img *image.RGBA, format Format, workers int) (*Texture, error) {
	if img.Rect.Dx() <= 0 || img.Rect.Dy() <= 0 {
		return nil, fmt.Errorf("texcompress: empty image")
	}
	if format != BC7 && format != BC5 {
		return nil, fmt.Errorf("texcompress: unknown format %v", format)
	}
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	t := &Texture{Format: format}
	for _, lvl := range MipChain(img) {
		t.Levels = append(t.Levels, encodeLevel(lvl, format, workers))
	}
	return t, nil
}

// encodeLevel encodes one level.
func encodeLevel(img *image.RGBA, format Format, workers int) Level {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	bw, bh := (w+3)/4, (h+3)/4
	out := Level{W: w, H: h, Data: make([]byte, bw*bh*BlockBytes)}

	rows := make(chan int, bh)
	for by := 0; by < bh; by++ {
		rows <- by
	}
	close(rows)
	n := min(workers, bh)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			var px [16][4]int32
			for by := range rows {
				for bx := 0; bx < bw; bx++ {
					gatherBlock(img, bx*4, by*4, &px)
					dst := out.Data[(by*bw+bx)*BlockBytes:][:BlockBytes]
					if format == BC7 {
						encodeBC7Block(&px, dst)
					} else {
						encodeBC5Block(&px, dst)
					}
				}
			}
		}()
	}
	wg.Wait()
	return out
}

// gatherBlock reads the 4x4 block at (x0, y0), clamping to the image's edge.
func gatherBlock(img *image.RGBA, x0, y0 int, px *[16][4]int32) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	for y := 0; y < 4; y++ {
		row := min(y0+y, h-1) * img.Stride
		for x := 0; x < 4; x++ {
			o := row + min(x0+x, w-1)*4
			p := img.Pix[o : o+4 : o+4]
			px[y*4+x] = [4]int32{int32(p[0]), int32(p[1]), int32(p[2]), int32(p[3])}
		}
	}
}

// DecodeLevel decodes one level back to RGBA, for tests and quality
// measurement. A BC5 level decodes to (x, y, 0, 255), what a GPU samples.
func DecodeLevel(format Format, l Level) (*image.RGBA, error) {
	img := image.NewRGBA(image.Rect(0, 0, l.W, l.H))
	bw, bh := (l.W+3)/4, (l.H+3)/4
	if len(l.Data) != bw*bh*BlockBytes {
		return nil, fmt.Errorf("texcompress: level %dx%d holds %d bytes, want %d", l.W, l.H, len(l.Data), bw*bh*BlockBytes)
	}
	for by := 0; by < bh; by++ {
		for bx := 0; bx < bw; bx++ {
			b := l.Data[(by*bw+bx)*BlockBytes:][:BlockBytes]
			var texels [16][4]uint8
			switch format {
			case BC7:
				if !DecodeBC7Block(b, &texels) {
					return nil, fmt.Errorf("texcompress: block (%d,%d) is not mode 6", bx, by)
				}
			case BC5:
				var rg [16][2]uint8
				DecodeBC5Block(b, &rg)
				for i := range rg {
					texels[i] = [4]uint8{rg[i][0], rg[i][1], 0, 255}
				}
			default:
				return nil, fmt.Errorf("texcompress: unknown format %v", format)
			}
			for y := 0; y < 4; y++ {
				for x := 0; x < 4; x++ {
					px, py := bx*4+x, by*4+y
					if px >= l.W || py >= l.H {
						continue
					}
					copy(img.Pix[py*img.Stride+px*4:][:4], texels[y*4+x][:])
				}
			}
		}
	}
	return img, nil
}
