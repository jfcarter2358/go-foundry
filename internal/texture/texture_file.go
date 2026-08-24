package texture

// texture_file.go — on-disk format for layered texture documents (.ftex) and
// the global palette (palette.pal). Pure I/O + encoding; no GL calls.
//
// <exe>/textures/<name>.ftex — little-endian:
//   0    [4]byte  magic "FTEX"
//   4    uint16   version = 1
//   6    uint16   flags (reserved, 0)
//   8    uint32   width   (1..1024)
//   12   uint32   height  (1..1024)
//   16   uint8    layerCount (1..8)
//   17   [3]byte  padding
//   20   [1024]   palette: 256 x RGBA8, the table this document was authored against
//   1044 ...      layers, bottom first:
//                   uint16  nameLen, then name bytes (UTF-8)
//                   uint8   blend
//                   uint8   visible
//                   float32 opacity
//                   uint32  compressedLen
//                   bytes   zlib(palette indices, rows top-to-bottom, W*H bytes)
//
// <exe>/textures/palette.pal — magic "FPAL", uint16 version, 256 x RGBA8.

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/texdoc"
)

const (
	ftexMagic   = "FTEX"
	ftexVersion = uint16(1)
	fpalMagic   = "FPAL"
	fpalVersion = uint16(1)

	ftexHeaderLen = 4 + 2 + 2 + 4 + 4 + 1 + 3 + texdoc.TexPaletteSz*4 // 1044
	fpalFileLen   = 4 + 2 + texdoc.TexPaletteSz*4
)

func palettePath() string           { return filepath.Join(fileutil.TexturesDir(), "palette.pal") }
func FtexPath(name string) string   { return filepath.Join(fileutil.TexturesDir(), name+".ftex") }
func texPNGPath(name string) string { return filepath.Join(fileutil.TexturesDir(), name+".png") }

// ---------- palette ----------

// WritePalette writes pal to path in the FPAL format.
func WritePalette(path string, pal *texdoc.TexPalette) error {
	buf := make([]byte, fpalFileLen)
	copy(buf[0:4], fpalMagic)
	binary.LittleEndian.PutUint16(buf[4:6], fpalVersion)
	for i, c := range pal {
		off := 6 + i*4
		buf[off], buf[off+1], buf[off+2], buf[off+3] = c.R, c.G, c.B, c.A
	}
	return fileutil.WriteFileAtomic(path, buf)
}

// ReadPalette reads and validates an FPAL file.
func ReadPalette(path string) (*texdoc.TexPalette, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < fpalFileLen {
		return nil, fmt.Errorf("palette %s: truncated (got %d bytes, want %d)", path, len(data), fpalFileLen)
	}
	if string(data[0:4]) != fpalMagic {
		return nil, fmt.Errorf("palette %s: bad magic %q", path, data[0:4])
	}
	if v := binary.LittleEndian.Uint16(data[4:6]); v != fpalVersion {
		return nil, fmt.Errorf("palette %s: unsupported version %d", path, v)
	}
	var pal texdoc.TexPalette
	for i := range pal {
		off := 6 + i*4
		pal[i] = color.RGBA{R: data[off], G: data[off+1], B: data[off+2], A: data[off+3]}
	}
	return &pal, nil
}

// LoadGlobalPalette loads <exe>/textures/palette.pal into texdoc.GlobalPalette. If
// the file is missing, the compiled-in default is used and written out
// immediately, so a fresh checkout has a palette.pal after startup without
// requiring a save first.
func LoadGlobalPalette() {
	pal, err := ReadPalette(palettePath())
	if err != nil {
		texdoc.GlobalPalette = texdoc.DefaultPalette()
		if werr := WritePalette(palettePath(), &texdoc.GlobalPalette); werr != nil {
			log.Printf("WARNING: could not write default palette: %v", werr)
		}
		return
	}
	texdoc.GlobalPalette = *pal
}

// ---------- .ftex ----------

func writeU16(buf *bytes.Buffer, v uint16) {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	buf.Write(b[:])
}

func writeU32(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

func writeF32(buf *bytes.Buffer, v float32) {
	writeU32(buf, math.Float32bits(v))
}

// WriteTexDoc encodes d in the .ftex format and writes it to path.
func WriteTexDoc(path string, d *texdoc.TexDoc) error {
	if err := texdoc.ValidTexSize(d.W, d.H); err != nil {
		return err
	}
	if len(d.Layers) < 1 || len(d.Layers) > texdoc.TexMaxLayers {
		return fmt.Errorf("layer count %d out of range [1,%d]", len(d.Layers), texdoc.TexMaxLayers)
	}

	var buf bytes.Buffer
	buf.WriteString(ftexMagic)
	writeU16(&buf, ftexVersion)
	writeU16(&buf, 0) // flags
	writeU32(&buf, uint32(d.W))
	writeU32(&buf, uint32(d.H))
	buf.WriteByte(uint8(len(d.Layers)))
	buf.Write([]byte{0, 0, 0}) // padding
	for _, c := range d.Pal {
		buf.WriteByte(c.R)
		buf.WriteByte(c.G)
		buf.WriteByte(c.B)
		buf.WriteByte(c.A)
	}

	want := d.W * d.H
	for _, layer := range d.Layers {
		if len(layer.Idx) != want {
			return fmt.Errorf("layer %q has %d indices, want %d", layer.Name, len(layer.Idx), want)
		}
		nameBytes := []byte(layer.Name)
		if len(nameBytes) > 0xFFFF {
			return fmt.Errorf("layer name %q too long", layer.Name)
		}
		writeU16(&buf, uint16(len(nameBytes)))
		buf.Write(nameBytes)
		buf.WriteByte(uint8(layer.Blend))
		if layer.Visible {
			buf.WriteByte(1)
		} else {
			buf.WriteByte(0)
		}
		writeF32(&buf, layer.Opacity)

		var compressed bytes.Buffer
		zw := zlib.NewWriter(&compressed)
		if _, err := zw.Write(layer.Idx); err != nil {
			return err
		}
		if err := zw.Close(); err != nil {
			return err
		}
		writeU32(&buf, uint32(compressed.Len()))
		buf.Write(compressed.Bytes())
	}

	return fileutil.WriteFileAtomic(path, buf.Bytes())
}

// ReadTexDoc reads and validates a .ftex file from path.
func ReadTexDoc(path string) (*texdoc.TexDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := parseTexDoc(data)
	if err != nil {
		return nil, err
	}
	doc.Path = path
	doc.Name = strings.TrimSuffix(filepath.Base(path), ".ftex")
	return doc, nil
}

func parseTexDoc(data []byte) (*texdoc.TexDoc, error) {
	if len(data) < ftexHeaderLen {
		return nil, fmt.Errorf(".ftex: truncated header (got %d bytes, want at least %d)", len(data), ftexHeaderLen)
	}
	if string(data[0:4]) != ftexMagic {
		return nil, fmt.Errorf(".ftex: bad magic %q", data[0:4])
	}
	if v := binary.LittleEndian.Uint16(data[4:6]); v != ftexVersion {
		return nil, fmt.Errorf(".ftex: unsupported version %d", v)
	}
	width := binary.LittleEndian.Uint32(data[8:12])
	height := binary.LittleEndian.Uint32(data[12:16])
	if width == 0 || height == 0 {
		return nil, fmt.Errorf(".ftex: zero dimension %dx%d", width, height)
	}
	if err := texdoc.ValidTexSize(int(width), int(height)); err != nil {
		return nil, fmt.Errorf(".ftex: %w", err)
	}
	layerCount := data[16]
	if layerCount == 0 || layerCount > texdoc.TexMaxLayers {
		return nil, fmt.Errorf(".ftex: layer count %d out of range [1,%d]", layerCount, texdoc.TexMaxLayers)
	}

	var pal texdoc.TexPalette
	const palOff = 20
	for i := range pal {
		off := palOff + i*4
		pal[i] = color.RGBA{R: data[off], G: data[off+1], B: data[off+2], A: data[off+3]}
	}

	want := int(width) * int(height)
	pos := ftexHeaderLen
	layers := make([]texdoc.TexLayer, 0, layerCount)
	for li := 0; li < int(layerCount); li++ {
		if pos+2 > len(data) {
			return nil, fmt.Errorf(".ftex: truncated at layer %d name length", li)
		}
		nameLen := int(binary.LittleEndian.Uint16(data[pos : pos+2]))
		pos += 2
		if pos+nameLen > len(data) {
			return nil, fmt.Errorf(".ftex: truncated at layer %d name", li)
		}
		name := string(data[pos : pos+nameLen])
		pos += nameLen

		if pos+1+1+4+4 > len(data) {
			return nil, fmt.Errorf(".ftex: truncated at layer %d header", li)
		}
		blend := texdoc.TexBlend(data[pos])
		pos++
		visible := data[pos] != 0
		pos++
		opacity := math.Float32frombits(binary.LittleEndian.Uint32(data[pos : pos+4]))
		pos += 4
		compLen := int(binary.LittleEndian.Uint32(data[pos : pos+4]))
		pos += 4
		if compLen < 0 || pos+compLen > len(data) {
			return nil, fmt.Errorf(".ftex: truncated at layer %d payload", li)
		}

		zr, err := zlib.NewReader(bytes.NewReader(data[pos : pos+compLen]))
		if err != nil {
			return nil, fmt.Errorf(".ftex: layer %d: %w", li, err)
		}
		idx := make([]uint8, want)
		if _, err := io.ReadFull(zr, idx); err != nil {
			zr.Close()
			return nil, fmt.Errorf(".ftex: layer %d: decompressed length != %d: %w", li, want, err)
		}
		var extra [1]byte
		if n, _ := zr.Read(extra[:]); n != 0 {
			zr.Close()
			return nil, fmt.Errorf(".ftex: layer %d decompresses to more than %d bytes", li, want)
		}
		zr.Close()
		pos += compLen

		layers = append(layers, texdoc.TexLayer{Name: name, Visible: visible, Opacity: opacity, Blend: blend, Idx: idx})
	}

	return &texdoc.TexDoc{W: int(width), H: int(height), Pal: pal, Layers: layers, Active: 0}, nil
}

// SaveTexDoc writes both the .ftex source and the flattened PNG the engine
// uses to <exe>/textures/<name>.{ftex,png}. It does not touch the GPU or the
// texture registry — see RegisterTexture/QueueTextureUpload in go,
// called by the editor immediately after a successful save.
func SaveTexDoc(d *texdoc.TexDoc, name string) error {
	if err := texdoc.ValidTexSize(d.W, d.H); err != nil {
		return err
	}
	dir := fileutil.TexturesDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	d.Name = name
	d.Path = FtexPath(name)
	if err := WriteTexDoc(d.Path, d); err != nil {
		return err
	}

	flat := d.Flatten()
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, flat); err != nil {
		return fmt.Errorf("encode png: %w", err)
	}
	if err := fileutil.WriteFileAtomic(texPNGPath(name), pngBuf.Bytes()); err != nil {
		return err
	}

	d.ClearDirty()
	return nil
}

// ImportPNG loads an existing PNG (or other image.Decode-able file) as a
// new single-layer texdoc.TexDoc, quantised against the current global palette.
// Images larger than texdoc.TexMaxDim in either dimension are rejected rather than
// silently downscaled — a rescale changes the art and should be explicit.
func ImportPNG(path string) (*texdoc.TexDoc, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if err := texdoc.ValidTexSize(w, h); err != nil {
		return nil, fmt.Errorf("import %s: %w", filepath.Base(path), err)
	}

	idx := make([]uint8, w*h)
	q := texdoc.NewQuantCache(&texdoc.GlobalPalette)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			idx[y*w+x] = q.Quantize(color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(bl >> 8), A: uint8(a >> 8)})
		}
	}

	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return &texdoc.TexDoc{
		Name: name, W: w, H: h, Pal: texdoc.GlobalPalette,
		Layers: []texdoc.TexLayer{{Name: "Layer 1", Visible: true, Opacity: 1, Blend: texdoc.BlendNormal, Idx: idx}},
		Active: 0,
	}, nil
}

// ScanFtexFiles returns sorted logical names (without extension) of .ftex
// files in <exe>/textures.
func ScanFtexFiles() []string {
	files := fileutil.ScanFilesByExt(fileutil.TexturesDir(), ".ftex")
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = strings.TrimSuffix(f, ".ftex")
	}
	return names
}
