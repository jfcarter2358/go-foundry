package texture

// convert_pngs.go — turning every loose PNG in textures/ into an .ftex at once.
//
// It is File > Import PNG followed by Save, for each PNG that has no .ftex
// yet: the image is quantised to the global palette, and SaveTexDoc writes the
// .ftex and re-flattens the PNG from it. That re-flatten is the point — the
// PNG afterwards is the palette version, the same as a hand conversion leaves.
//
// A <name>_normal.png beside it comes in as the document's bump channel. An
// .ftex keeps height, not normals, so the height is rebuilt from the map
// (texdoc.HeightFromNormalMap) and the save re-derives the map from that. A
// map that is the gradient of some surface comes back all but unchanged; one
// that is not (noise drawn straight into the normals, as rough generated
// ground often is) can only come back as the nearest surface, and a save would
// overwrite the original with it. ConvertOptions.MaxNormalError is where the
// caller says how far off is too far, and a texture over it is left untouched.
//
// Only disk is touched, and nothing here needs the GL context, so it can run
// off the frame loop. The caller reloads textures afterwards, so the rewritten
// PNGs replace the originals on the GPU as well.

import (
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/texdoc"
)

// DefaultMaxNormalError is the average angle, in degrees, a rebuilt normal
// map may differ from the original by before ConvertPNGsToFtex leaves that
// texture alone. A few degrees is below what reads as a change in lighting.
const DefaultMaxNormalError = 4

// ConvertOptions tunes ConvertPNGsToFtex.
type ConvertOptions struct {
	// MaxNormalError skips a texture whose normal map, rebuilt through a
	// height field, would be more than this many degrees off the original on
	// average. 0 converts every one, however far off.
	MaxNormalError float64
	// Progress, when set, is called before each texture with how many are
	// done so far, the total, and the texture about to be converted.
	Progress func(done, total int, name string)
}

// PNGConversion reports what ConvertPNGsToFtex did, by texture name.
type PNGConversion struct {
	Converted []string
	// NormalError is how far off, on average in degrees, each converted
	// texture's rebuilt normal map is from the original it replaced.
	NormalError map[string]float64
	// Skipped names a PNG deliberately left alone, with why.
	Skipped map[string]string
	// Failed names a PNG that could not be converted: undecodable, a size the
	// .ftex format cannot hold, or a write that went wrong.
	Failed map[string]error
}

// PNGsWithoutFtex returns the sorted names of the PNGs in textures/ that have
// no .ftex beside them — what ConvertPNGsToFtex would take on. Normal maps are
// not textures in their own right and are never listed.
func PNGsWithoutFtex() []string {
	entries, err := os.ReadDir(fileutil.TexturesDir())
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".png") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if fileutil.IsNormalMapName(name) {
			continue
		}
		if _, err := os.Stat(FtexPath(name)); err == nil {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ConvertPNGsToFtex converts every PNG PNGsWithoutFtex lists. One failure does
// not stop the rest.
func ConvertPNGsToFtex(opts ConvertOptions) PNGConversion {
	r := PNGConversion{
		NormalError: map[string]float64{},
		Skipped:     map[string]string{},
		Failed:      map[string]error{},
	}
	names := PNGsWithoutFtex()
	for i, name := range names {
		if opts.Progress != nil {
			opts.Progress(i, len(names), name)
		}
		path := texPNGPath(name)
		if _, err := os.Stat(path); err != nil {
			// A .PNG on a case-sensitive disk: SaveTexDoc writes .png, so the
			// original would stay behind as a second copy under the same name.
			r.Skipped[name] = "extension is not lower-case .png"
			continue
		}
		d, err := ImportPNG(path)
		if err != nil {
			r.Failed[name] = err
			continue
		}
		// Without this, a document with no bump channel saves by deleting the
		// normal map beside it — so a map that cannot come in must keep the
		// whole texture out.
		normalErr, skip, err := importNormalMap(d, name, opts.MaxNormalError)
		if err != nil {
			r.Failed[name] = err
			continue
		}
		if skip != "" {
			r.Skipped[name] = skip
			continue
		}
		if err := SaveTexDoc(d, name); err != nil {
			r.Failed[name] = fmt.Errorf("save: %w", err)
			continue
		}
		r.Converted = append(r.Converted, name)
		if normalErr >= 0 {
			r.NormalError[name] = normalErr
		}
	}
	if opts.Progress != nil {
		opts.Progress(len(names), len(names), "")
	}
	return r
}

// importNormalMap gives d a bump channel rebuilt from name's normal map, if
// it has one. It returns the rebuilt map's average error in degrees (-1 when
// there was no map, or a flat one with nothing to keep), or a reason the
// texture should be skipped instead.
func importNormalMap(d *texdoc.TexDoc, name string, maxErr float64) (float64, string, error) {
	path := texNormalPNGPath(name)
	if _, err := os.Stat(path); err != nil {
		return -1, "", nil
	}
	img, err := LoadImageFile(path)
	if err != nil {
		return 0, "", fmt.Errorf("normal map: %w", err)
	}
	if b := img.Bounds(); b.Dx() != d.W || b.Dy() != d.H {
		return 0, fmt.Sprintf("normal map is %dx%d, the texture %dx%d", b.Dx(), b.Dy(), d.W, d.H), nil
	}
	heights, strength, ok := texdoc.HeightFromNormalMap(img)
	if !ok {
		// Flat: the save's removal of the map loses nothing.
		return -1, "", nil
	}
	hf := make([]float32, len(heights))
	for i, v := range heights {
		hf[i] = float32(v) / 255
	}
	rebuilt := texdoc.NormalMapFromHeight(d.W, d.H, hf, strength)
	e := normalMapError(img, rebuilt)
	if maxErr > 0 && e > maxErr {
		return 0, fmt.Sprintf("its normal map can only be rebuilt %.1f° off on average", e), nil
	}

	d.SetChannel(texdoc.ChanBump) // creates the channel with one empty layer
	copy(d.Layers[d.Active].Idx, heights)
	d.SetChannel(texdoc.ChanColor)
	d.SetBumpStrength(strength)
	return e, "", nil
}

// normalMapError is the mean angle, in degrees, between the normals of two
// same-sized maps.
func normalMapError(orig image.Image, rebuilt *image.RGBA) float64 {
	b := orig.Bounds()
	w, h := b.Dx(), b.Dy()
	if w*h == 0 {
		return 0
	}
	var sum float64
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, _ := orig.At(b.Min.X+x, b.Min.Y+y).RGBA()
			ax, ay, az := float64(r)/65535*2-1, float64(g)/65535*2-1, float64(bl)/65535*2-1
			p := (y*w + x) * 4
			cx := float64(rebuilt.Pix[p])/255*2 - 1
			cy := float64(rebuilt.Pix[p+1])/255*2 - 1
			cz := float64(rebuilt.Pix[p+2])/255*2 - 1
			la := math.Sqrt(ax*ax + ay*ay + az*az)
			lc := math.Sqrt(cx*cx + cy*cy + cz*cz)
			if la == 0 || lc == 0 {
				continue
			}
			cos := (ax*cx + ay*cy + az*cz) / (la * lc)
			sum += math.Acos(math.Max(-1, math.Min(1, cos))) * 180 / math.Pi
		}
	}
	return sum / float64(w*h)
}
