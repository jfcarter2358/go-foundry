package stages

// grass_field_card.go — a card rule's texture: a Poly Haven ground texture
// CUT OUT by its own height map (grass_field_cover.go; docs/terrain-paint/
// PHASE_P3.md).
//
// Poly Haven publishes leaf litter as ground textures (forest_leaves_04,
// dry_decay_leaves, winter_leaves...), not as cut-out leaves: a square of
// soil with leaves on it, no alpha. But each set has a displacement map, and
// on a leaf-litter scan the leaves are what stands up: keeping the texels
// whose height is above a cut, and dropping the rest, leaves the scan's own
// leaves — real shapes, real colour — with the soil between them gone. A
// card laid over the litter splat then adds a layer of leaves that catches
// light and parallax the flat splat cannot.
//
// The cut-out (an RGBA PNG):
//
//   - the albedo, scaled to at most cardTexSize a side, times the set's
//     ambient occlusion when it has one: a card is lit per vertex as one
//     flat face, with none of the normal and AO maps the splat under it
//     wears, so without its own AO it reads as a smooth painted patch
//     beside the crisp scan around it;
//   - alpha = a smoothstep about card_cut of the LOCAL height: the height
//     less a blur of it (a box blur of cardBlur of the side, twice), then
//     normalised to its own 5th–95th percentile (scans differ in range). A
//     scan's displacement is mostly broad lumps — thresholded raw, a card
//     is one smooth blob of litter, a smudge at a grazing angle — and the
//     leaves and twigs are what stands above their own neighbourhood;
//   - the alpha fades to nothing over the outer cardEdge of each atlas tile,
//     the fade bent by a noise so a tile never shows a straight edge.
//
// It is written beside the set's own maps as
// textures/ph_<id>_card<cut>x<tiles>.png, only when it is missing or its
// bytes differ, so a rerun touches nothing and two generators share it, as
// they share the imports.
//
// NOT `_cov`. A grass blade's cutout keeps its coverage down the mips
// (texcompress/mip_coverage.go) so a distant clump stays a clump; a card's
// must not. Kept, the far mips turn each card into an opaque square of its
// averaged colour with a crisp rim — a crazy paving of smudges over the
// litter. With ordinary mips a card's alpha averages away with distance and
// it dissolves into the splat under it, which is the same scan: up close
// the leaves stand off the ground, from afar the ground is just litter.

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"

	"foundry/internal/fileutil"
	"foundry/internal/procgen/noise"
)

// cardTexSize caps the cut-out's side.
const cardTexSize = 1024

// cardEdge is the fraction of a tile's side its alpha fades out over.
const cardEdge = 0.14

// cardBlur is the local-height blur's radius, a fraction of the side.
const cardBlur = 1.0 / 64

// cardBoxBlur blurs v (w × h, wrapping: the scans tile) by a box of radius r,
// rows then columns.
func cardBoxBlur(v []float64, w, h, r int) []float64 {
	tmp := make([]float64, len(v))
	out := make([]float64, len(v))
	n := float64(2*r + 1)
	for y := 0; y < h; y++ {
		sum := 0.0
		for k := -r; k <= r; k++ {
			sum += v[y*w+((k%w)+w)%w]
		}
		for x := 0; x < w; x++ {
			tmp[y*w+x] = sum / n
			sum += v[y*w+(x+r+1)%w] - v[y*w+((x-r)%w+w)%w]
		}
	}
	for x := 0; x < w; x++ {
		sum := 0.0
		for k := -r; k <= r; k++ {
			sum += tmp[(((k%h)+h)%h)*w+x]
		}
		for y := 0; y < h; y++ {
			out[y*w+x] = sum / n
			sum += tmp[((y+r+1)%h)*w+x] - tmp[(((y-r)%h+h)%h)*w+x]
		}
	}
	return out
}

// cardTextureName is the cut-out's engine texture name.
func cardTextureName(id string, cut float64, tiles int) string {
	return fmt.Sprintf("ph_%s_card%02dx%d", id, int(math.Round(cut*100)), tiles)
}

// buildCardTexture writes (when needed) the cut-out of layer l and returns
// its texture name.
func buildCardTexture(resources string, l *TextureLayer, cut float64, tiles int) (string, error) {
	if l.Albedo == "" || l.Height == "" {
		return "", fmt.Errorf("texture %s has no albedo or no height map to cut a card from", l.ID)
	}
	dir := filepath.Join(resources, fileutil.TexturesDirName)
	alb, err := decodeImageFile(filepath.Join(dir, l.Albedo+".png"))
	if err != nil {
		return "", err
	}
	hgt, err := decodeImageFile(filepath.Join(dir, l.Height+".png"))
	if err != nil {
		return "", err
	}
	var ao image.Image
	if l.AO != "" {
		if ao, err = decodeImageFile(filepath.Join(dir, l.AO+".png")); err != nil {
			return "", err
		}
	}
	img := cutCard(alb, hgt, ao, cut, tiles)
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img); err != nil {
		return "", err
	}
	name := cardTextureName(l.ID, cut, tiles)
	path := filepath.Join(dir, name+".png")
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, buf.Bytes()) {
		return name, nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return "", err
	}
	return name, os.Rename(tmp, path)
}

func decodeImageFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return img, nil
}

// grey is image im's luminance at (x, y) of a w × h grid laid over it.
func greyAt(im image.Image, x, y, w, h int) float64 {
	b := im.Bounds()
	sx := b.Min.X + x*b.Dx()/w
	sy := b.Min.Y + y*b.Dy()/h
	r, g, bb, _ := im.At(sx, sy).RGBA()
	return (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bb)) / 65535
}

// cutCard is the cut-out: alb's colour (times ao, when not nil), alpha from
// hgt (file comment).
func cutCard(alb, hgt, ao image.Image, cut float64, tiles int) *image.NRGBA {
	b := alb.Bounds()
	w, h := b.Dx(), b.Dy()
	for w > cardTexSize || h > cardTexSize {
		w, h = (w+1)/2, (h+1)/2
	}
	// The height, sampled onto the output grid, and its percentiles.
	hs := make([]float64, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			hs[y*w+x] = greyAt(hgt, x, y, w, h)
		}
	}
	// The local height: what stands above its neighbourhood.
	r := max(int(float64(w)*cardBlur), 1)
	blur := cardBoxBlur(cardBoxBlur(hs, w, h, r), w, h, r)
	for i := range hs {
		hs[i] -= blur[i]
	}
	sorted := append([]float64(nil), hs...)
	sort.Float64s(sorted)
	lo, hi := sorted[len(sorted)*5/100], sorted[len(sorted)*95/100]
	if hi <= lo {
		hi = lo + 1e-3
	}
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	tw, th := float64(w)/float64(tiles), float64(h)/float64(tiles)
	smooth := func(e0, e1, x float64) float64 {
		t := math.Max(0, math.Min(1, (x-e0)/(e1-e0)))
		return t * t * (3 - 2*t)
	}
	bs := alb.Bounds()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := (hs[y*w+x] - lo) / (hi - lo)
			a := smooth(cut-0.07, cut+0.07, v)
			// The tile's edge fade, its line bent by a noise.
			fx := math.Mod(float64(x), tw) / tw
			fy := math.Mod(float64(y), th) / th
			e := math.Min(math.Min(fx, 1-fx), math.Min(fy, 1-fy))
			wob := noise.FBM2(0xca2d, float64(x)/37, float64(y)/37, 3, 0.5) * 0.06
			a *= smooth(0.01, cardEdge, e+wob)
			r, g, bb, _ := alb.At(bs.Min.X+x*bs.Dx()/w, bs.Min.Y+y*bs.Dy()/h).RGBA()
			occ := 1.0
			if ao != nil {
				occ = greyAt(ao, x, y, w, h)
			}
			c := func(v uint32) uint8 { return uint8(math.Round(float64(v>>8) * occ)) }
			out.SetNRGBA(x, y, color.NRGBA{R: c(r), G: c(g), B: c(bb), A: uint8(math.Round(a * 255))})
		}
	}
	return out
}
