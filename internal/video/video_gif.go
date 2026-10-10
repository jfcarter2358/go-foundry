package video

// video_gif.go — animated GIF, composed.
//
// `image/gif` hands back what the file literally contains, which is not a list
// of pictures: it is a list of *patches*, each with a disposal method saying
// what to do with the canvas before the next one lands. A player that drew the
// patches directly would show a logo animation as a scatter of rectangles on
// black, which is the classic way of getting this wrong.
//
// So this keeps the canvas and replays. Playing forwards costs one patch per
// frame; seeking backwards replays from the start, because a GIF has no
// keyframes to restart from and the alternative — holding every composed frame
// as RGBA — is a hundred megabytes for a five-second animation.

import (
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	"os"
)

type gifDecoder struct {
	name string
	g    *gif.GIF
	trk  Track

	// canvas is the composed picture as of frame `at`; -1 means "nothing drawn
	// yet", which is the state a rewind returns it to.
	canvas *image.RGBA
	at     int

	// prev holds the canvas as it was before the frame at `at` was drawn, for
	// DisposalPrevious. One frame deep is all the format can ask for.
	prev *image.RGBA
}

func openGIF(name, path string) (decoder, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	g, err := gif.DecodeAll(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if len(g.Image) == 0 {
		return nil, fmt.Errorf("%s: no frames", name)
	}

	w, h := g.Config.Width, g.Config.Height
	if w <= 0 || h <= 0 {
		// A GIF written without a logical screen descriptor worth trusting.
		// The first frame's bounds are the next best answer.
		b := g.Image[0].Bounds()
		w, h = b.Dx(), b.Dy()
	}

	d := &gifDecoder{
		name:   name,
		g:      g,
		canvas: image.NewRGBA(image.Rect(0, 0, w, h)),
		prev:   image.NewRGBA(image.Rect(0, 0, w, h)),
		at:     -1,
	}
	d.trk.Width, d.trk.Height = w, h
	d.trk.Starts = make([]float64, len(g.Image))
	t := 0.0
	for i := range g.Image {
		d.trk.Starts[i] = t
		t += delaySeconds(g, i)
	}
	d.trk.Duration = t
	return d, nil
}

// delaySeconds is a frame's hold time. GIF counts in hundredths of a second,
// and zero is what an encoder writes when it means "as fast as possible" —
// which every viewer since Netscape has read as a tenth of a second, because
// treating it literally makes the whole animation happen in one frame.
func delaySeconds(g *gif.GIF, i int) float64 {
	if i >= len(g.Delay) || g.Delay[i] <= 0 {
		return 0.1
	}
	return float64(g.Delay[i]) / 100
}

func (d *gifDecoder) Track() *Track { return &d.trk }
func (d *gifDecoder) Close() error  { return nil }

func (d *gifDecoder) Frame(i int) (*image.RGBA, error) {
	if i < 0 || i >= len(d.g.Image) {
		return nil, fmt.Errorf("frame %d of %d", i, len(d.g.Image))
	}
	if i < d.at {
		d.rewind()
	}
	for d.at < i {
		d.step(d.at + 1)
	}
	return d.canvas, nil
}

func (d *gifDecoder) rewind() {
	draw.Draw(d.canvas, d.canvas.Bounds(), image.Transparent, image.Point{}, draw.Src)
	d.at = -1
}

// step composes one frame onto the canvas, having first applied the *previous*
// frame's disposal. Disposal is stated on the frame being left, not the frame
// being drawn, which is the detail that makes a naive implementation flicker.
func (d *gifDecoder) step(i int) {
	if d.at >= 0 && d.at < len(d.g.Disposal) {
		switch d.g.Disposal[d.at] {
		case gif.DisposalBackground:
			// The area the outgoing frame covered goes transparent. Not the
			// whole canvas: a frame that patched one corner disposes of one
			// corner.
			r := d.g.Image[d.at].Bounds()
			draw.Draw(d.canvas, r, image.Transparent, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			draw.Draw(d.canvas, d.canvas.Bounds(), d.prev, d.prev.Bounds().Min, draw.Src)
		}
	}

	if i < len(d.g.Disposal) && d.g.Disposal[i] == gif.DisposalPrevious {
		draw.Draw(d.prev, d.prev.Bounds(), d.canvas, d.canvas.Bounds().Min, draw.Src)
	}

	src := d.g.Image[i]
	// Over, not Src: a GIF frame's transparent index means "leave what is
	// underneath", which is the entire mechanism patches work by.
	draw.Draw(d.canvas, src.Bounds(), src, src.Bounds().Min, draw.Over)
	d.at = i
}
