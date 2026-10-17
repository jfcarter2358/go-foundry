package paint

// storage.go — a document's paint on disk, and the Set of its layers.
//
//	<doc dir>/<doc>.paint/<layer>/<i>_<j>.png
//
// One PNG per stored tile, N×N texels, row 0 the tile's lowest z:
//
//	density, exclude, material, water   8-bit grey, 0..255 = 0..1
//	height                              16-bit grey, 32768 = 0 m, 1/256 m a step
//	direction                           8-bit RGB, R = x and G = z, 128 = 0,
//	                                    ±127 = ±1; B is 128 and unused
//
// Save writes only what changed since the set was loaded from (or last
// saved to) that folder — a tile whose content hash differs from the one on
// disk, or that is not on disk — and deletes the files of tiles no longer
// stored, then the layer folder when it empties, then the paint folder
// when that does. Layers on disk that the document no longer declares are
// left alone: deleting a layer from the document is not deleting its
// pixels (re-declare it and they are back).
//
// Writes are atomic (a temporary beside the file, renamed over it) and keep
// no .bak: a tile is cheap to repaint and a .bak beside every tile would
// double the folder.

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Dir is a document's paint folder: its path less the extension, plus
// DirSuffix.
func Dir(docPath string) string {
	return strings.TrimSuffix(docPath, filepath.Ext(docPath)) + DirSuffix
}

// Set is a document's layers by name.
type Set struct {
	layers map[string]*Layer
	// saved is each layer's tile hashes as they are in savedDir.
	savedDir string
	saved    map[string]map[TileKey]string
}

// NewSet is an empty set.
func NewSet() *Set { return &Set{layers: map[string]*Layer{}} }

// Layer is the named layer, or nil.
func (s *Set) Layer(name string) *Layer {
	if s == nil {
		return nil
	}
	return s.layers[name]
}

// Names is every layer's name, sorted.
func (s *Set) Names() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.layers))
	for n := range s.layers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Ensure is the layer def declares, created empty when the set has none.
// A layer whose kind or resolution changed is emptied: its texels meant
// something else.
func (s *Set) Ensure(def LayerDef) *Layer {
	l := s.layers[def.Name]
	if l == nil || l.Def.Kind != def.Kind || l.Def.Res() != def.Res() {
		l = NewLayer(def)
		s.layers[def.Name] = l
	}
	l.Def = def
	return l
}

// Clone is a snapshot sharing tiles copy-on-write (layer.go). Nil for nil.
func (s *Set) Clone() *Set {
	if s == nil {
		return nil
	}
	out := &Set{layers: make(map[string]*Layer, len(s.layers)), savedDir: s.savedDir, saved: s.saved}
	for n, l := range s.layers {
		out.layers[n] = l.Clone()
	}
	return out
}

// Fingerprint is a hash of every layer's content, for an editor's save
// chip; 0 for an empty or nil set.
func (s *Set) Fingerprint() uint64 {
	if s == nil {
		return 0
	}
	h := fnv.New64a()
	empty := true
	for _, n := range s.Names() {
		l := s.layers[n]
		if l.Empty() {
			continue
		}
		empty = false
		h.Write([]byte(n + "=" + l.Hash() + ";"))
	}
	if empty {
		return 0
	}
	return h.Sum64()
}

// Load reads the declared layers' tiles from dir (a document's paint
// folder; missing is no paint). A layer with no folder is empty.
func Load(dir string, defs []LayerDef) (*Set, error) {
	s := NewSet()
	s.savedDir, s.saved = dir, map[string]map[TileKey]string{}
	for _, def := range defs {
		if err := def.Validate(); err != nil {
			return nil, err
		}
		l := s.Ensure(def)
		ld := filepath.Join(dir, def.Name)
		ents, err := os.ReadDir(ld)
		if os.IsNotExist(err) {
			continue
		} else if err != nil {
			return nil, err
		}
		hashes := map[TileKey]string{}
		for _, e := range ents {
			k, ok := ParseTileFile(e.Name())
			if !ok || e.IsDir() {
				continue
			}
			data, err := os.ReadFile(filepath.Join(ld, e.Name()))
			if err != nil {
				return nil, err
			}
			t, err := DecodeTile(def, data)
			if err != nil {
				return nil, fmt.Errorf("paint %s/%s: %w", def.Name, e.Name(), err)
			}
			if t.zero() {
				continue
			}
			t.frozen.Store(false)
			l.tiles[k] = t
			hashes[k] = l.TileHash(k)
		}
		s.saved[def.Name] = hashes
	}
	return s, nil
}

// Save writes the declared layers' tiles into dir (see the file comment).
func (s *Set) Save(dir string, defs []LayerDef) error {
	if s == nil {
		return nil
	}
	same := s.savedDir == dir
	saved := map[string]map[TileKey]string{}
	for _, def := range defs {
		l := s.layers[def.Name]
		ld := filepath.Join(dir, def.Name)
		var prev map[TileKey]string
		if same {
			prev = s.saved[def.Name]
		}
		now := map[TileKey]string{}
		if l != nil {
			for _, k := range l.Keys() {
				h := l.TileHash(k)
				now[k] = h
				f := filepath.Join(ld, k.String()+".png")
				if prev != nil && prev[k] == h && fileExists(f) {
					continue
				}
				data, err := EncodeTile(def, l.tiles[k])
				if err != nil {
					return err
				}
				if err := writeAtomic(f, data); err != nil {
					return err
				}
			}
		}
		// The files of tiles no longer stored.
		if ents, err := os.ReadDir(ld); err == nil {
			for _, e := range ents {
				k, ok := ParseTileFile(e.Name())
				if !ok {
					continue
				}
				if _, keep := now[k]; !keep {
					if err := os.Remove(filepath.Join(ld, e.Name())); err != nil {
						return err
					}
				}
			}
			os.Remove(ld) // only succeeds when empty
		}
		saved[def.Name] = now
	}
	os.Remove(dir) // likewise
	s.savedDir, s.saved = dir, saved
	return nil
}

// ParseTileFile is the key a tile file's name says ("-1_2.png").
func ParseTileFile(name string) (TileKey, bool) {
	stem, ok := strings.CutSuffix(name, ".png")
	if !ok {
		return TileKey{}, false
	}
	a, b, ok := strings.Cut(stem, "_")
	if !ok {
		return TileKey{}, false
	}
	i, err1 := strconv.Atoi(a)
	j, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil {
		return TileKey{}, false
	}
	return TileKey{i, j}, true
}

// EncodeTile is a tile's PNG.
func EncodeTile(def LayerDef, t *Tile) ([]byte, error) {
	n := t.N
	var img image.Image
	switch def.Kind {
	case KindHeight:
		g := image.NewGray16(image.Rect(0, 0, n, n))
		for k, v := range t.V {
			g.SetGray16(k%n, k/n, color.Gray16{Y: encodeHeight(float64(v))})
		}
		img = g
	case KindDirection:
		g := image.NewNRGBA(image.Rect(0, 0, n, n))
		for k := 0; k < n*n; k++ {
			g.SetNRGBA(k%n, k/n, color.NRGBA{R: encodeDir(float64(t.V[2*k])), G: encodeDir(float64(t.V[2*k+1])), B: 128, A: 255})
		}
		img = g
	default:
		g := image.NewGray(image.Rect(0, 0, n, n))
		for k, v := range t.V {
			g.Pix[k] = encodeUnit(float64(v))
		}
		img = g
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeTile reads a tile's PNG for a layer of def.
func DecodeTile(def LayerDef, data []byte) (*Tile, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	n := def.TexelsPerTile()
	b := img.Bounds()
	if b.Dx() != n || b.Dy() != n {
		return nil, fmt.Errorf("%d×%d texels, a %g m tile at %g m is %d", b.Dx(), b.Dy(), TileSize, def.Res(), n)
	}
	t := newTile(n, def.Channels())
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			c := img.At(b.Min.X+x, b.Min.Y+y)
			k := y*n + x
			switch def.Kind {
			case KindHeight:
				g := color.Gray16Model.Convert(c).(color.Gray16)
				t.V[k] = float32(float64(int(g.Y)-32768) / 256)
			case KindDirection:
				rgba := color.NRGBAModel.Convert(c).(color.NRGBA)
				t.V[2*k] = float32(float64(int(rgba.R)-128) / 127)
				t.V[2*k+1] = float32(float64(int(rgba.G)-128) / 127)
			default:
				g := color.GrayModel.Convert(c).(color.Gray)
				t.V[k] = float32(float64(g.Y) / 255)
			}
		}
	}
	// Exactly what Quantize would have stored, so a loaded tile hashes as
	// the painted one did.
	for i, v := range t.V {
		t.V[i] = float32(def.Quantize(float64(v)))
	}
	return t, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// writeAtomic writes data to path through a temporary beside it.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	return os.Rename(name, path)
}
