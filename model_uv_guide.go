package main

// model_uv_guide.go — the checker pattern a freshly unwrapped atlas starts as.
//
// A new atlas used to be filled flat grey, which told you nothing: with every
// texel the same colour, a face on the model gave no clue which part of the
// image it was showing, so painting meant daubing a test colour, looking at the
// model, and guessing again.
//
// The fix is the standard UV test pattern, doing two jobs at once:
//
//   - The checker squares are a fixed size in *texels*, so they show texel
//     density directly. Squares that come out rectangular on the model mean the
//     unwrap stretched that island; squares of visibly different sizes on two
//     faces mean those faces got different shares of the texture.
//   - The light squares are tinted by a colour field that sweeps corner to
//     corner, so a square's colour says where on the image it lives. Look at
//     the model, see teal, and you know to paint in the middle of the atlas.
//
// The two are deliberately independent. Density wants small squares; colour
// identification wants a slow sweep with well-separated colours. Tying the
// colour to the square index would force one resolution to serve both.
//
// Everything here is quantised to the global 256-entry palette
// (texdoc.GlobalPalette), which is derived from the game's own art and is
// therefore mostly earth tones — it has nowhere near 256 colours' worth of
// saturated hue. That is why the sweep below is measured rather than assumed:
// see model_uv_guide_test.go, which asserts that distant squares actually come
// out as different palette entries.

import (
	"image/color"
	"math"

	"foundry/internal/texdoc"
)

const (
	// atlasCheckerTexels is the side of one checker square, in texels. Fixed in
	// texels rather than as a fraction of the image so a square means the same
	// amount of texture at every atlas size — which is the whole point of
	// judging stretch by it.
	atlasCheckerTexels = 16

	// atlasGuideHueSpan is how far round the colour wheel the sweep travels, in
	// degrees. 240 runs red -> yellow -> green -> blue.
	//
	// It stops there for two separate reasons. A full 360 arrives back at red,
	// so the two ends of the gradient would be the same colour and the corners
	// it exists to tell apart would not be. And the stretch beyond blue is
	// magenta and violet, which the game's palette essentially does not contain
	// — those hues quantise to greys, so a longer sweep does not buy more
	// colours, it buys a washed-out corner. Rendering the 300-degree version
	// makes this obvious: the right-hand quarter of the atlas comes out grey.
	atlasGuideHueSpan = 240.0

	// The light squares' saturation, and the range their brightness sweeps.
	//
	// These three are measured, not chosen by eye. Against the game's palette a
	// more saturated sweep resolves *worse*: at s=0.9 the quantiser collapses
	// the sampled squares onto far fewer slots than at s=0.6, because there
	// simply are not many saturated entries to land on. Pushing the other way
	// is no better — below about s=0.5 the whole image drifts towards grey.
	//
	// The brightness floor is what makes the vertical ramp survive quantisation.
	// It also has to keep the dimmest tint clearly above atlasGuideDark, or the
	// checker stops reading as a checker in that corner.
	atlasGuideSat    = 0.60
	atlasGuideValMin = 0.40
	atlasGuideValMax = 1.0
)

// atlasGuideDark is the constant colour of the checker's dark squares. Neutral,
// so it does not compete with the hue sweep, and near-black so that even the
// dimmest tint separates from it.
//
// It is this dark specifically because of blue. Luminance weights green far
// above blue, so the dim blue corner of the sweep is the faintest square on the
// image by some margin; a lighter dark square would leave that corner reading
// as navy-on-slate. Deepening this was the cheaper fix — the alternative was
// raising atlasGuideValMin, which would have flattened the vertical brightness
// ramp everywhere to rescue one corner.
var atlasGuideDark = color.RGBA{R: 18, G: 18, B: 24, A: 255}

// atlasGuideColorAt returns the light-square tint at (u, v) in 0..1.
//
// The sweep runs corner to corner: (0,0) is a deep red, and the opposite corner
// (1,1) is a bright blue, having travelled the colour wheel and the brightness
// ramp on the way. Hue moves with u and brightness with v.
//
// Those two axes are deliberately *not* rotated onto the diagonal, which is the
// obvious way to write "a gradient from one corner to the other" and is the
// version this started as. Rotating makes hue a function of u+v, so (0.2, 0.7)
// and (0.7, 0.2) get the same hue and the two off-corners become hard to tell
// apart — and the perpendicular brightness ramp that is supposed to separate
// them is pinned at its midpoint all along the main diagonal, exactly where the
// hue repeats are worst. Measured over a checker grid, the diagonal form gave
// 19 distinguishable squares out of 64, with 135 far-apart colour collisions
// and mirrored points landing on the same palette slot; the axis-aligned form
// used here has no mirroring at all and comfortably more distinct colours.
//
// It still reads as one corner-to-corner gradient — it just also tells the
// other two corners apart. See TestAtlasGuideDistantSquaresDiffer and
// TestAtlasGuideOffDiagonalPointsDiffer, which pin both properties.
func atlasGuideColorAt(u, v float32) color.RGBA {
	hue := float64(clamp01(u)) * atlasGuideHueSpan
	val := atlasGuideValMin + (atlasGuideValMax-atlasGuideValMin)*float64(clamp01(v))
	return hsvToRGBA(hue, atlasGuideSat, val)
}

// AtlasGuidePattern renders the checker guide at w x h as straight RGBA, before
// palette quantisation. Pure and allocation-only, so the pattern can be
// asserted on without a texture document or a GL context.
//
// Row 0 is the top of the image, matching how the flattened texture is uploaded
// and therefore how V is sampled — so "the red corner" is the same corner in
// the texture editor, on the model, and in these coordinates.
func AtlasGuidePattern(w, h int) []color.RGBA {
	out := make([]color.RGBA, w*h)
	if w <= 0 || h <= 0 {
		return out
	}
	// A square is at least one texel even on a tiny atlas, or the modulus below
	// divides by zero and the whole image comes out one colour.
	square := atlasCheckerTexels
	if square > w {
		square = w
	}
	if square > h {
		square = h
	}
	if square < 1 {
		square = 1
	}

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := atlasGuideDark
			if ((x/square)+(y/square))%2 == 0 {
				// The colour is sampled at the texel, not at the square's
				// centre, so the sweep stays smooth across the image rather
				// than stepping with the checker — the two resolutions are
				// independent on purpose.
				u := (float32(x) + 0.5) / float32(w)
				v := (float32(y) + 0.5) / float32(h)
				c = atlasGuideColorAt(u, v)
			}
			out[y*w+x] = c
		}
	}
	return out
}

// fillAtlasGuide writes the checker guide into layer li of d.
//
// One QuantCache for the whole fill: the pattern is a few dozen distinct
// colours repeated over up to a million texels, so memoising the nearest-slot
// search turns a per-texel 256-entry scan into a map hit.
func fillAtlasGuide(d *texdoc.TexDoc, li int) {
	if d == nil || li < 0 || li >= len(d.Layers) {
		return
	}
	layer := &d.Layers[li]
	pattern := AtlasGuidePattern(d.W, d.H)
	q := texdoc.NewQuantCache(&d.Pal)
	for i := range layer.Idx {
		if i >= len(pattern) {
			break
		}
		layer.Idx[i] = q.Quantize(pattern[i])
	}
}

// hsvToRGBA converts HSV (hue in degrees, s and v in 0..1) to straight-alpha
// RGBA. Hue is the natural axis for "walk through visibly different colours",
// which is what the sweep needs and what an RGB lerp between two endpoints
// cannot give — that would pass through the muddy middle rather than round the
// wheel.
func hsvToRGBA(h, s, v float64) color.RGBA {
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	to8 := func(f float64) uint8 {
		n := int(math.Round((f + m) * 255))
		if n < 0 {
			n = 0
		}
		if n > 255 {
			n = 255
		}
		return uint8(n)
	}
	return color.RGBA{R: to8(r), G: to8(g), B: to8(b), A: 255}
}
