package texture

// texture_file.go — on-disk format for layered texture documents (.ftex) and
// the global palette (palette.pal). Pure I/O + encoding; no GL calls.
//
// <exe>/textures/<name>.ftex — little-endian, version 3:
//   0    [4]byte  magic "FTEX"
//   4    uint16   version = 3
//   6    uint16   flags — bit 0 (ftexFlagBump) = the document has a bump channel
//   8    uint32   width   (1..2048)
//   12   uint32   height  (1..2048)
//   16   uint16   frameCount (1..32) — the animation stack; 1 means "not animated"
//   18   [2]byte  padding
//   20   [1024]   palette: 256 x RGBA8, the table this document was authored against
//   1044 float32  bumpStrength (version 3 only)
//   1048 ...      frameCount frame blocks, each:
//                   one layer block: the frame's colour layers
//                   one layer block: the frame's height layers — only when
//                                    ftexFlagBump is set
//                 where a layer block is:
//                   uint8   layerCount (1..8)
//                   [3]byte padding
//                   ...     layerCount layers, bottom first:
//                             uint16  nameLen, then name bytes (UTF-8)
//                             uint8   blend
//                             uint8   visible
//                             float32 opacity
//                             uint32  compressedLen
//                             bytes   zlib(one byte per texel, rows top-to-bottom,
//                                          W*H bytes — palette indices on a colour
//                                          layer, heights on a height layer)
//
// Version 2 is version 3 without the bumpStrength word and with only the
// colour layer block per frame (so its frame blocks start at 1044). Version 1
// has no frameCount either — offset 16 is a single implicit frame's
// layerCount directly, with its layer records starting at 1044. Both still
// read: ReadTexDoc parses them into a document with no bump channel. There is
// no "upgrade" step beyond the version number itself — the next save writes
// version 3.
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
	"foundry/internal/gfx"
	"foundry/internal/texdoc"
)

const (
	ftexMagic    = "FTEX"
	ftexVersion1 = uint16(1) // single implicit frame; still readable, no longer written
	ftexVersion2 = uint16(2) // frameCount + repeated frame blocks
	ftexVersion3 = uint16(3) // + bumpStrength and an optional per-frame height block
	fpalMagic    = "FPAL"
	fpalVersion  = uint16(1)

	// ftexFlagBump marks a version-3 file as carrying a bump channel: one
	// extra layer block per frame, holding heights rather than palette
	// indices. Clear on every file the earlier versions wrote, which is what
	// makes the flag safe to read out of their reserved flags word.
	ftexFlagBump = uint16(1 << 0)

	ftexHeaderLen = 4 + 2 + 2 + 4 + 4 + 2 + 2 + texdoc.TexPaletteSz*4 // 1044, every version
	// ftexV3BodyOff is where a version-3 file's first frame block starts: past
	// the header and the bumpStrength word. Versions 1 and 2 have no such word
	// and start their bodies at ftexHeaderLen.
	ftexV3BodyOff = ftexHeaderLen + 4
	fpalFileLen   = 4 + 2 + texdoc.TexPaletteSz*4
)

func palettePath() string           { return filepath.Join(fileutil.TexturesDir(), "palette.pal") }
func FtexPath(name string) string   { return filepath.Join(fileutil.TexturesDir(), name+".ftex") }
func texPNGPath(name string) string { return filepath.Join(fileutil.TexturesDir(), name+".png") }

// texNormalPNGPath is where the normal map derived from a document's bump
// channel lands: beside the albedo, under the name gfx resolves it by. The
// engine never looks at the .ftex — it samples this PNG, exactly as it
// samples <name>.png for the albedo.
func texNormalPNGPath(name string) string {
	return filepath.Join(fileutil.TexturesDir(), gfx.NormalMapName(name)+".png")
}

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

// writeLayer appends one layer record (the format shared by every frame
// block, unchanged since version 1).
func writeLayer(buf *bytes.Buffer, want int, layer texdoc.TexLayer) error {
	if len(layer.Idx) != want {
		return fmt.Errorf("layer %q has %d indices, want %d", layer.Name, len(layer.Idx), want)
	}
	nameBytes := []byte(layer.Name)
	if len(nameBytes) > 0xFFFF {
		return fmt.Errorf("layer name %q too long", layer.Name)
	}
	writeU16(buf, uint16(len(nameBytes)))
	buf.Write(nameBytes)
	buf.WriteByte(uint8(layer.Blend))
	if layer.Visible {
		buf.WriteByte(1)
	} else {
		buf.WriteByte(0)
	}
	writeF32(buf, layer.Opacity)

	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	if _, err := zw.Write(layer.Idx); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	writeU32(buf, uint32(compressed.Len()))
	buf.Write(compressed.Bytes())
	return nil
}

// writeLayerBlock appends one layer block: the count byte, its padding, and
// every layer record. Shared by a frame's colour and height stacks, which are
// the same shape on disk and differ only in what the bytes mean.
func writeLayerBlock(buf *bytes.Buffer, want int, layers []texdoc.TexLayer) error {
	buf.WriteByte(uint8(len(layers)))
	buf.Write([]byte{0, 0, 0}) // padding
	for _, layer := range layers {
		if err := writeLayer(buf, want, layer); err != nil {
			return err
		}
	}
	return nil
}

// WriteTexDoc encodes d (every frame in its animation stack, both channels)
// in the .ftex format and writes it to path. Always writes version 3.
func WriteTexDoc(path string, d *texdoc.TexDoc) error {
	if err := texdoc.ValidTexSize(d.W, d.H); err != nil {
		return err
	}
	frames := d.Frames()
	if len(frames) < 1 || len(frames) > texdoc.TexMaxFrames {
		return fmt.Errorf("frame count %d out of range [1,%d]", len(frames), texdoc.TexMaxFrames)
	}
	hasBump := d.HasBump()
	for _, f := range frames {
		if len(f.Layers) < 1 || len(f.Layers) > texdoc.TexMaxLayers {
			return fmt.Errorf("layer count %d out of range [1,%d]", len(f.Layers), texdoc.TexMaxLayers)
		}
		if hasBump && (len(f.Bump) < 1 || len(f.Bump) > texdoc.TexMaxLayers) {
			return fmt.Errorf("height layer count %d out of range [1,%d]", len(f.Bump), texdoc.TexMaxLayers)
		}
	}

	flags := uint16(0)
	if hasBump {
		flags |= ftexFlagBump
	}

	var buf bytes.Buffer
	buf.WriteString(ftexMagic)
	writeU16(&buf, ftexVersion3)
	writeU16(&buf, flags)
	writeU32(&buf, uint32(d.W))
	writeU32(&buf, uint32(d.H))
	writeU16(&buf, uint16(len(frames)))
	buf.Write([]byte{0, 0}) // padding
	for _, c := range d.Pal {
		buf.WriteByte(c.R)
		buf.WriteByte(c.G)
		buf.WriteByte(c.B)
		buf.WriteByte(c.A)
	}
	writeF32(&buf, d.BumpStrength())

	want := d.W * d.H
	for _, f := range frames {
		if err := writeLayerBlock(&buf, want, f.Layers); err != nil {
			return err
		}
		if !hasBump {
			continue
		}
		if err := writeLayerBlock(&buf, want, f.Bump); err != nil {
			return err
		}
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

// readLayer parses one layer record starting at pos, returning the layer and
// the position just past it. frameIdx/li name the layer in error messages —
// version 1 has an implicit single frame (0).
func readLayer(data []byte, pos, want, frameIdx, li int) (texdoc.TexLayer, int, error) {
	if pos+2 > len(data) {
		return texdoc.TexLayer{}, 0, fmt.Errorf(".ftex: truncated at frame %d layer %d name length", frameIdx, li)
	}
	nameLen := int(binary.LittleEndian.Uint16(data[pos : pos+2]))
	pos += 2
	if pos+nameLen > len(data) {
		return texdoc.TexLayer{}, 0, fmt.Errorf(".ftex: truncated at frame %d layer %d name", frameIdx, li)
	}
	name := string(data[pos : pos+nameLen])
	pos += nameLen

	if pos+1+1+4+4 > len(data) {
		return texdoc.TexLayer{}, 0, fmt.Errorf(".ftex: truncated at frame %d layer %d header", frameIdx, li)
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
		return texdoc.TexLayer{}, 0, fmt.Errorf(".ftex: truncated at frame %d layer %d payload", frameIdx, li)
	}

	zr, err := zlib.NewReader(bytes.NewReader(data[pos : pos+compLen]))
	if err != nil {
		return texdoc.TexLayer{}, 0, fmt.Errorf(".ftex: frame %d layer %d: %w", frameIdx, li, err)
	}
	idx := make([]uint8, want)
	if _, err := io.ReadFull(zr, idx); err != nil {
		zr.Close()
		return texdoc.TexLayer{}, 0, fmt.Errorf(".ftex: frame %d layer %d: decompressed length != %d: %w", frameIdx, li, want, err)
	}
	var extra [1]byte
	if n, _ := zr.Read(extra[:]); n != 0 {
		zr.Close()
		return texdoc.TexLayer{}, 0, fmt.Errorf(".ftex: frame %d layer %d decompresses to more than %d bytes", frameIdx, li, want)
	}
	zr.Close()
	pos += compLen

	return texdoc.TexLayer{Name: name, Visible: visible, Opacity: opacity, Blend: blend, Idx: idx}, pos, nil
}

// readLayerBlock parses one layer block — a layerCount byte, 3 bytes padding,
// then that many layer records — starting at pos.
func readLayerBlock(data []byte, pos, want, frameIdx int) ([]texdoc.TexLayer, int, error) {
	if pos+4 > len(data) {
		return nil, 0, fmt.Errorf(".ftex: truncated at frame %d header", frameIdx)
	}
	layerCount := data[pos]
	if layerCount == 0 || layerCount > texdoc.TexMaxLayers {
		return nil, 0, fmt.Errorf(".ftex: frame %d layer count %d out of range [1,%d]", frameIdx, layerCount, texdoc.TexMaxLayers)
	}
	pos += 4 // layerCount + 3 bytes padding

	layers := make([]texdoc.TexLayer, 0, layerCount)
	for li := 0; li < int(layerCount); li++ {
		layer, next, err := readLayer(data, pos, want, frameIdx, li)
		if err != nil {
			return nil, 0, err
		}
		layers = append(layers, layer)
		pos = next
	}
	return layers, pos, nil
}

// readFrame parses one frame block: the colour layer block, then the height
// layer block when the file's flags say the document carries one.
func readFrame(data []byte, pos, want, frameIdx int, hasBump bool) (texdoc.TexFrame, int, error) {
	layers, pos, err := readLayerBlock(data, pos, want, frameIdx)
	if err != nil {
		return texdoc.TexFrame{}, 0, err
	}
	// The active layer is not saved, so reopen on the top one: where the tools
	// paint over everything already visible. Layer 0 is under every other
	// layer (an unwrapped texture's "UV Guide" sits under its "Paint"), and
	// paint put there shows only where the layers above are transparent.
	frame := texdoc.TexFrame{Layers: layers, Active: len(layers) - 1}
	if hasBump {
		bump, next, err := readLayerBlock(data, pos, want, frameIdx)
		if err != nil {
			return texdoc.TexFrame{}, 0, err
		}
		frame.Bump = bump
		frame.BumpActive = len(bump) - 1
		pos = next
	}
	return frame, pos, nil
}

func parseTexDoc(data []byte) (*texdoc.TexDoc, error) {
	if len(data) < ftexHeaderLen {
		return nil, fmt.Errorf(".ftex: truncated header (got %d bytes, want at least %d)", len(data), ftexHeaderLen)
	}
	if string(data[0:4]) != ftexMagic {
		return nil, fmt.Errorf(".ftex: bad magic %q", data[0:4])
	}
	version := binary.LittleEndian.Uint16(data[4:6])
	if version != ftexVersion1 && version != ftexVersion2 && version != ftexVersion3 {
		return nil, fmt.Errorf(".ftex: unsupported version %d", version)
	}
	flags := binary.LittleEndian.Uint16(data[6:8])
	hasBump := version == ftexVersion3 && flags&ftexFlagBump != 0
	width := binary.LittleEndian.Uint32(data[8:12])
	height := binary.LittleEndian.Uint32(data[12:16])
	if width == 0 || height == 0 {
		return nil, fmt.Errorf(".ftex: zero dimension %dx%d", width, height)
	}
	if err := texdoc.ValidTexSize(int(width), int(height)); err != nil {
		return nil, fmt.Errorf(".ftex: %w", err)
	}

	var pal texdoc.TexPalette
	const palOff = 20
	for i := range pal {
		off := palOff + i*4
		pal[i] = color.RGBA{R: data[off], G: data[off+1], B: data[off+2], A: data[off+3]}
	}

	want := int(width) * int(height)
	var frames []texdoc.TexFrame
	var bumpStrength float32

	if version == ftexVersion1 {
		// No frameCount: offset 16 is the single implicit frame's layerCount
		// directly (1 byte + 3 padding), and its layer records start right
		// after the header, same as they always did.
		layerCount := data[16]
		if layerCount == 0 || layerCount > texdoc.TexMaxLayers {
			return nil, fmt.Errorf(".ftex: layer count %d out of range [1,%d]", layerCount, texdoc.TexMaxLayers)
		}
		pos := ftexHeaderLen
		layers := make([]texdoc.TexLayer, 0, layerCount)
		for li := 0; li < int(layerCount); li++ {
			layer, next, err := readLayer(data, pos, want, 0, li)
			if err != nil {
				return nil, err
			}
			layers = append(layers, layer)
			pos = next
		}
		frames = []texdoc.TexFrame{{Layers: layers, Active: len(layers) - 1}}
	} else {
		frameCount := binary.LittleEndian.Uint16(data[16:18])
		if frameCount == 0 || int(frameCount) > texdoc.TexMaxFrames {
			return nil, fmt.Errorf(".ftex: frame count %d out of range [1,%d]", frameCount, texdoc.TexMaxFrames)
		}
		pos := ftexHeaderLen
		if version == ftexVersion3 {
			if ftexV3BodyOff > len(data) {
				return nil, fmt.Errorf(".ftex: truncated at bump strength")
			}
			bumpStrength = math.Float32frombits(binary.LittleEndian.Uint32(data[pos:ftexV3BodyOff]))
			pos = ftexV3BodyOff
		}
		frames = make([]texdoc.TexFrame, 0, frameCount)
		for fi := 0; fi < int(frameCount); fi++ {
			frame, next, err := readFrame(data, pos, want, fi, hasBump)
			if err != nil {
				return nil, err
			}
			frames = append(frames, frame)
			pos = next
		}
	}

	return texdoc.NewTexDocFromFrames(int(width), int(height), pal, frames, bumpStrength)
}

// SaveTexDoc writes the .ftex source, the flattened PNG the engine samples as
// the albedo, and — when the document carries a bump channel — the normal map
// derived from its height field, to
// <exe>/textures/<name>.{ftex,png} and <exe>/textures/<name>_normal.png. It
// does not touch the GPU or the texture registry — see
// RegisterTexture/QueueTextureUpload in gfx, called by the editor immediately
// after a successful save.
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

	if err := writeNormalMapPNG(d, name); err != nil {
		return err
	}

	d.ClearDirty()
	return nil
}

// writeNormalMapPNG writes (or removes) <name>_normal.png to match the
// document's bump channel.
//
// The removal half matters as much as the writing half: the renderer decides
// a texture is normal-mapped purely by whether that file loaded, so a
// leftover map from a bump channel the artist has since deleted would keep
// lighting the surface as if the relief were still there. Only the frame
// being edited is written for an animated document — the whole stack goes to
// the GPU through SyncAnimatedFrames, and the PNG is what a cold start loads
// before any .ftex has been read.
func writeNormalMapPNG(d *texdoc.TexDoc, name string) error {
	path := texNormalPNGPath(name)
	if !d.HasBump() {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stale normal map: %w", err)
		}
		return nil
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, d.NormalMap()); err != nil {
		return fmt.Errorf("encode normal map png: %w", err)
	}
	return fileutil.WriteFileAtomic(path, buf.Bytes())
}

// ImportPNG loads an existing PNG (or other image.Decode-able file) as a
// new single-layer texdoc.TexDoc, quantised against the current global palette.
// Images larger than texdoc.TexMaxDim in either dimension are rejected rather than
// silently downscaled — a rescale changes the art and should be explicit.
func ImportPNG(path string) (*texdoc.TexDoc, error) {
	img, err := LoadImageFile(path)
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

	// Built through the constructor rather than as a struct literal: a TexDoc
	// carries unexported frame/channel bookkeeping, and one assembled by hand
	// has an empty animation stack that every whole-document operation
	// (Flatten included) indexes into.
	d, err := texdoc.NewTexDocFromFrames(w, h, texdoc.GlobalPalette, []texdoc.TexFrame{{
		Layers: []texdoc.TexLayer{{Name: "Layer 1", Visible: true, Opacity: 1, Blend: texdoc.BlendNormal, Idx: idx}},
	}}, 0)
	if err != nil {
		return nil, err
	}
	d.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return d, nil
}

// LoadImageFile decodes a PNG (or other image.Decode-able file) for import,
// refusing anything larger than texdoc.TexMaxDim before it is resampled or
// quantised.
func LoadImageFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	return img, nil
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
