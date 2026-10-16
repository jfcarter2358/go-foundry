package levelio

// fbsp_binary.go — the compiled level's on-disk layout: flat little-endian
// columns behind a directory, rather than one gob stream.
//
// # Why not gob any more
//
// Reading level1 through gob took about two seconds before a single surface
// was built, nearly all of it decoding the surface list: 160,000 faces, each a
// struct of a dozen slices, each slice a separate allocation and each float a
// separate call through gob's reflection. What a load needs is the same
// numbers back in memory, and the cheapest way to get a float array back is to
// have written it as one. So the surfaces are stored as COLUMNS — every face's
// points end to end in one array, every face's light in another, with a count
// per face saying where each face's run starts — and read back as one copy per
// column, with each face's slices cut from the shared arrays rather than
// allocated one by one. The tree, the visibility set and the cluster cut are
// columns too, for the same reason.
//
// # The layout
//
//	header    32 bytes: "FOUNDRYBSP" "\x00BIN", version u32, column count u32,
//	          CRC-32C of everything after the header u32, zero padding
//	directory per column: name length u16, name, kind u8, element count u64,
//	          offset u64 (from the start of the file)
//	data      each column's elements, little-endian, starting 8-byte aligned
//
// Columns are found BY NAME. A reader ignores a column it does not know and
// treats one it cannot find as empty, so a column can be added without
// invalidating every file already compiled — the property gob had and that the
// file comment in compiled_level.go defends. A change that would make old
// columns MEAN something else still bumps CompiledVersion, as it always has.
//
// The irregular parts — the chunks, as runtime values, and the compile's Stats
// — are single byte columns in internal/binenc's encoding, and the authored half is
// a byte column holding the same YAML the gob file carried, for the reason that
// file gives.
//
// # Determinism
//
// The same compiled level always writes the same bytes: columns are written in
// a fixed order, the string table is in order of first use, and nothing is
// written from a map without sorting it. (The gob file was not: it wrote a map
// in Go's iteration order, and a chunk with script attributes or a terrain
// paint layer came out different on every compile.)
//
// # Integrity
//
// The checksum is what turns a truncated or damaged file into a load error
// rather than a level with half its surfaces at the origin. Every count and
// offset is bounds-checked as well, so a file that passes the checksum but was
// written by something else still cannot index out of range.

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"
	"reflect"
	"sync"
	"unsafe"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/bsp"
	"foundry/internal/cluster"
	"foundry/internal/compile"
	"foundry/internal/world"
)

// binaryTag follows compiledMagic in a columnar file. A gob file has the gob
// stream there instead, whose first byte is never zero.
const binaryTag = "\x00BIN"

const binaryHeaderSize = 32

// legacyGobVersion is the last version written as one gob stream. Files of
// that version still load: the columnar layout changed how a level is stored,
// not what it means.
const legacyGobVersion = 9

var crcTable = crc32.MakeTable(crc32.Castagnoli)

type colKind uint8

const (
	colBytes colKind = 1 + iota
	colU32
	colI32
	colF32
	colU64
	colI64
)

func (k colKind) size() int {
	switch k {
	case colBytes:
		return 1
	case colU32, colI32, colF32:
		return 4
	case colU64, colI64:
		return 8
	}
	return 0
}

// littleEndianHost is whether this machine's memory order is the file's, in
// which case a column is copied rather than converted word by word.
var littleEndianHost = func() bool {
	x := uint16(1)
	return *(*byte)(unsafe.Pointer(&x)) == 1
}()

// ---------------------------------------------------------------- writing

type column struct {
	name  string
	kind  colKind
	count int
	data  []byte
}

type colWriter struct {
	cols []column
	strs *stringTable
}

func (w *colWriter) add(name string, kind colKind, count int, data []byte) {
	w.cols = append(w.cols, column{name: name, kind: kind, count: count, data: data})
}

func (w *colWriter) bytesCol(name string, b []byte) { w.add(name, colBytes, len(b), b) }

func (w *colWriter) u32(name string, v []uint32) {
	w.add(name, colU32, len(v), wordsLE(unsafe.Pointer(unsafe.SliceData(v)), len(v)))
}

func (w *colWriter) i32(name string, v []int32) {
	w.add(name, colI32, len(v), wordsLE(unsafe.Pointer(unsafe.SliceData(v)), len(v)))
}

func (w *colWriter) f32(name string, v []float32) {
	w.add(name, colF32, len(v), wordsLE(unsafe.Pointer(unsafe.SliceData(v)), len(v)))
}

func (w *colWriter) u64(name string, v []uint64) {
	b := make([]byte, 8*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint64(b[8*i:], x)
	}
	w.add(name, colU64, len(v), b)
}

func (w *colWriter) i64(name string, v []int64) {
	b := make([]byte, 8*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint64(b[8*i:], uint64(x))
	}
	w.add(name, colI64, len(v), b)
}

// wordsLE is n four-byte words at p as little-endian bytes: a copy of the
// memory on a little-endian host, a word-by-word conversion elsewhere.
func wordsLE(p unsafe.Pointer, n int) []byte {
	out := make([]byte, 4*n)
	if n == 0 {
		return out
	}
	if littleEndianHost {
		copy(out, unsafe.Slice((*byte)(p), 4*n))
		return out
	}
	for i, x := range unsafe.Slice((*uint32)(p), n) {
		binary.LittleEndian.PutUint32(out[4*i:], x)
	}
	return out
}

// assemble lays the header, directory and columns out as one file.
func (w *colWriter) assemble(version int) []byte {
	dirSize := 0
	for _, c := range w.cols {
		dirSize += 2 + len(c.name) + 1 + 8 + 8
	}
	align := func(n int) int { return (n + 7) &^ 7 }
	off := align(binaryHeaderSize + dirSize)
	offsets := make([]int, len(w.cols))
	for i, c := range w.cols {
		offsets[i] = off
		off = align(off + len(c.data))
	}
	out := make([]byte, off)
	copy(out, compiledMagic)
	copy(out[len(compiledMagic):], binaryTag)
	binary.LittleEndian.PutUint32(out[14:], uint32(version))
	binary.LittleEndian.PutUint32(out[18:], uint32(len(w.cols)))
	p := binaryHeaderSize
	for i, c := range w.cols {
		binary.LittleEndian.PutUint16(out[p:], uint16(len(c.name)))
		p += 2
		p += copy(out[p:], c.name)
		out[p] = byte(c.kind)
		p++
		binary.LittleEndian.PutUint64(out[p:], uint64(c.count))
		p += 8
		binary.LittleEndian.PutUint64(out[p:], uint64(offsets[i]))
		p += 8
		copy(out[offsets[i]:], c.data)
	}
	binary.LittleEndian.PutUint32(out[22:], crc32.Checksum(out[binaryHeaderSize:], crcTable))
	return out
}

// stringTable numbers strings in order of first use; 0 is always "".
type stringTable struct {
	index map[string]uint32
	list  []string
}

func newStringTable() *stringTable {
	return &stringTable{index: map[string]uint32{"": 0}, list: []string{""}}
}

func (t *stringTable) id(s string) uint32 {
	if i, ok := t.index[s]; ok {
		return i
	}
	i := uint32(len(t.list))
	t.index[s] = i
	t.list = append(t.list, s)
	return i
}

func (w *colWriter) writeStrings() {
	var data []byte
	offs := make([]uint32, 0, len(w.strs.list)+1)
	for _, s := range w.strs.list {
		offs = append(offs, uint32(len(data)))
		data = append(data, s...)
	}
	offs = append(offs, uint32(len(data)))
	w.u32("strings.offsets", offs)
	w.bytesCol("strings.data", data)
}

// ---------------------------------------------------------------- reading

type colRef struct {
	kind  colKind
	count int
	data  []byte
}

type colFile struct {
	version int
	cols    map[string]colRef
	strs    []string
}

// isBinaryCompiled is whether data is a columnar compiled level.
func isBinaryCompiled(data []byte) bool {
	n := len(compiledMagic)
	return len(data) >= n+len(binaryTag) && string(data[:n]) == compiledMagic && string(data[n:n+len(binaryTag)]) == binaryTag
}

// binaryVersion reads the version from a columnar file's header without
// checking anything else — for refusing a file from another compiler before
// paying for its checksum.
func binaryVersion(data []byte) (int, bool) {
	if !isBinaryCompiled(data) || len(data) < binaryHeaderSize {
		return 0, false
	}
	return int(binary.LittleEndian.Uint32(data[14:])), true
}

// parseColumns checks the header and checksum and indexes the directory. It
// does not copy any column.
func parseColumns(data []byte) (*colFile, error) {
	if len(data) < binaryHeaderSize || !isBinaryCompiled(data) {
		return nil, errCompiledCorrupt
	}
	f := &colFile{version: int(binary.LittleEndian.Uint32(data[14:])), cols: map[string]colRef{}}
	n := int(binary.LittleEndian.Uint32(data[18:]))
	if crc32.Checksum(data[binaryHeaderSize:], crcTable) != binary.LittleEndian.Uint32(data[22:]) {
		return nil, fmt.Errorf("compiled level is damaged or truncated (checksum mismatch)")
	}
	p := binaryHeaderSize
	for i := 0; i < n; i++ {
		if p+2 > len(data) {
			return nil, errCompiledCorrupt
		}
		nl := int(binary.LittleEndian.Uint16(data[p:]))
		p += 2
		if p+nl+17 > len(data) {
			return nil, errCompiledCorrupt
		}
		name := string(data[p : p+nl])
		p += nl
		kind := colKind(data[p])
		p++
		count := binary.LittleEndian.Uint64(data[p:])
		p += 8
		off := binary.LittleEndian.Uint64(data[p:])
		p += 8
		sz := uint64(kind.size())
		if sz == 0 || off > uint64(len(data)) || count > (uint64(len(data))-off)/sz {
			return nil, errCompiledCorrupt
		}
		f.cols[name] = colRef{kind: kind, count: int(count), data: data[off : off+count*sz]}
	}
	offs, err := f.u32("strings.offsets")
	if err != nil {
		return nil, err
	}
	sdata, err := f.bytes("strings.data")
	if err != nil {
		return nil, err
	}
	if len(offs) > 0 {
		f.strs = make([]string, len(offs)-1)
		for i := range f.strs {
			a, b := offs[i], offs[i+1]
			if a > b || int(b) > len(sdata) {
				return nil, errCompiledCorrupt
			}
			f.strs[i] = string(sdata[a:b])
		}
	}
	return f, nil
}

var errCompiledCorrupt = fmt.Errorf("compiled level is damaged or not a compiled level")

func (f *colFile) col(name string, kind colKind) (colRef, error) {
	c, ok := f.cols[name]
	if !ok {
		return colRef{kind: kind}, nil // absent: empty
	}
	if c.kind != kind {
		return colRef{}, fmt.Errorf("compiled level: column %q has the wrong kind", name)
	}
	return c, nil
}

func (f *colFile) bytes(name string) ([]byte, error) {
	c, err := f.col(name, colBytes)
	return c.data, err
}

// readWords copies a four-byte-word column into p, which has room for n.
func readWords(p unsafe.Pointer, n int, raw []byte) {
	if n == 0 {
		return
	}
	if littleEndianHost {
		copy(unsafe.Slice((*byte)(p), 4*n), raw)
		return
	}
	dst := unsafe.Slice((*uint32)(p), n)
	for i := range dst {
		dst[i] = binary.LittleEndian.Uint32(raw[4*i:])
	}
}

func (f *colFile) u32(name string) ([]uint32, error) {
	c, err := f.col(name, colU32)
	if err != nil || c.count == 0 {
		return nil, err
	}
	out := make([]uint32, c.count)
	readWords(unsafe.Pointer(&out[0]), c.count, c.data)
	return out, nil
}

func (f *colFile) i32(name string) ([]int32, error) {
	c, err := f.col(name, colI32)
	if err != nil || c.count == 0 {
		return nil, err
	}
	out := make([]int32, c.count)
	readWords(unsafe.Pointer(&out[0]), c.count, c.data)
	return out, nil
}

func (f *colFile) f32(name string) ([]float32, error) {
	c, err := f.col(name, colF32)
	if err != nil || c.count == 0 {
		return nil, err
	}
	out := make([]float32, c.count)
	readWords(unsafe.Pointer(&out[0]), c.count, c.data)
	return out, nil
}

func (f *colFile) u64(name string) ([]uint64, error) {
	c, err := f.col(name, colU64)
	if err != nil || c.count == 0 {
		return nil, err
	}
	out := make([]uint64, c.count)
	for i := range out {
		out[i] = binary.LittleEndian.Uint64(c.data[8*i:])
	}
	return out, nil
}

func (f *colFile) i64(name string) ([]int64, error) {
	c, err := f.col(name, colI64)
	if err != nil || c.count == 0 {
		return nil, err
	}
	out := make([]int64, c.count)
	for i := range out {
		out[i] = int64(binary.LittleEndian.Uint64(c.data[8*i:]))
	}
	return out, nil
}

func (f *colFile) str(id uint32) (string, error) {
	if int(id) >= len(f.strs) {
		if id == 0 {
			return "", nil
		}
		return "", errCompiledCorrupt
	}
	return f.strs[id], nil
}

// ---------------------------------------------------------------- runs
//
// A run column is one variable-length list per record laid end to end, with a
// count column beside it. Writing appends; reading hands each record its
// slice of one shared array, capped so an append to one can never write into
// the next.

// splitRuns cuts flat into len(counts) runs of stride elements each. A record
// with no elements gets nil, as gob gave it.
func splitRuns[T any](counts []uint32, flat []T, stride int, n int, name string) ([][]T, error) {
	if len(counts) != n && !(len(counts) == 0 && len(flat) == 0) {
		return nil, fmt.Errorf("compiled level: %s has %d counts for %d records", name, len(counts), n)
	}
	out := make([][]T, n)
	at := 0
	for i, c := range counts {
		k := int(c) * stride
		if k > len(flat)-at {
			return nil, fmt.Errorf("compiled level: %s overruns its data", name)
		}
		if k > 0 {
			out[i] = flat[at : at+k : at+k]
		}
		at += k
	}
	if at != len(flat) {
		return nil, fmt.Errorf("compiled level: %s has %d elements left over", name, len(flat)-at)
	}
	return out, nil
}

// eachRun is splitRuns without the slice of runs: it hands each record its
// run as it goes. For the surface lists, where a [][]T per list per level was
// a hundred megabytes of garbage on a large one.
func eachRun[T any](counts []uint32, flat []T, n int, name string, set func(i int, run []T)) error {
	if len(counts) != n && !(len(counts) == 0 && len(flat) == 0) {
		return fmt.Errorf("compiled level: %s has %d counts for %d records", name, len(counts), n)
	}
	at := 0
	for i, c := range counts {
		k := int(c)
		if k > len(flat)-at {
			return fmt.Errorf("compiled level: %s overruns its data", name)
		}
		if k > 0 {
			set(i, flat[at:at+k:at+k])
		}
		at += k
	}
	if at != len(flat) {
		return fmt.Errorf("compiled level: %s has %d elements left over", name, len(flat)-at)
	}
	return nil
}

func vec3sAsFloats(v []mgl32.Vec3) []float32 {
	if len(v) == 0 {
		return nil
	}
	return unsafe.Slice(&v[0][0], 3*len(v))
}

func floatsAsVec3s(f []float32) []mgl32.Vec3 {
	if len(f) == 0 {
		return nil
	}
	return unsafe.Slice((*mgl32.Vec3)(unsafe.Pointer(&f[0])), len(f)/3)
}

// ---------------------------------------------------------------- faces

// faceVec3Lists are compile.Face's per-point lists, by column name. Every
// one is written and read the same way; TestTheColumnarFormatCoversEveryField
// fails when Face grows a field none of these tables names.
var faceVec3Lists = []struct {
	name string
	get  func(*compile.Face) *[]mgl32.Vec3
}{
	{"face.points", func(f *compile.Face) *[]mgl32.Vec3 { return &f.Points }},
	{"face.smoothPoly", func(f *compile.Face) *[]mgl32.Vec3 { return &f.SmoothPoly }},
	{"face.smoothCorner", func(f *compile.Face) *[]mgl32.Vec3 { return &f.SmoothCorner }},
	{"face.light", func(f *compile.Face) *[]mgl32.Vec3 { return &f.Light }},
	{"face.smoothLight", func(f *compile.Face) *[]mgl32.Vec3 { return &f.SmoothLight }},
	{"face.backLight", func(f *compile.Face) *[]mgl32.Vec3 { return &f.BackLight }},
	{"face.smoothBackLight", func(f *compile.Face) *[]mgl32.Vec3 { return &f.SmoothBackLight }},
	{"face.indirect", func(f *compile.Face) *[]mgl32.Vec3 { return &f.Indirect }},
	{"face.smoothIndirect", func(f *compile.Face) *[]mgl32.Vec3 { return &f.SmoothIndirect }},
	{"face.backIndirect", func(f *compile.Face) *[]mgl32.Vec3 { return &f.BackIndirect }},
	{"face.smoothBackIndirect", func(f *compile.Face) *[]mgl32.Vec3 { return &f.SmoothBackIndirect }},
}

var faceF32Lists = []struct {
	name string
	get  func(*compile.Face) *[]float32
}{
	{"face.sunShare", func(f *compile.Face) *[]float32 { return &f.SunShare }},
	{"face.backSunShare", func(f *compile.Face) *[]float32 { return &f.BackSunShare }},
}

// faceScalars is how many float32s faceScalarsOf writes per face.
const faceScalars = 22

func faceScalarsOf(f *compile.Face, out []float32) {
	copy(out, []float32{
		f.Plane.N[0], f.Plane.N[1], f.Plane.N[2], f.Plane.D,
		f.UVOffset[0], f.UVOffset[1], f.UVScale[0], f.UVScale[1],
		f.UVRotation, f.SmoothAngle, f.Alpha,
		f.UVOrigin[0], f.UVOrigin[1], f.UVOrigin[2],
		f.UVMap.U[0], f.UVMap.U[1], f.UVMap.U[2], f.UVMap.U[3],
		f.UVMap.V[0], f.UVMap.V[1], f.UVMap.V[2], f.UVMap.V[3],
	})
}

func setFaceScalars(f *compile.Face, s []float32) {
	f.Plane.N = mgl32.Vec3{s[0], s[1], s[2]}
	f.Plane.D = s[3]
	f.UVOffset = mgl32.Vec2{s[4], s[5]}
	f.UVScale = mgl32.Vec2{s[6], s[7]}
	f.UVRotation, f.SmoothAngle, f.Alpha = s[8], s[9], s[10]
	f.UVOrigin = mgl32.Vec3{s[11], s[12], s[13]}
	f.UVMap.U = mgl32.Vec4{s[14], s[15], s[16], s[17]}
	f.UVMap.V = mgl32.Vec4{s[18], s[19], s[20], s[21]}
}

const (
	faceDetail uint32 = 1 << iota
	faceNoDraw
	faceSmooth
	faceDoubleSided
	faceUVMapped
)

// faceStrings is how many string ids writeFaces writes per face.
const faceStrings = 5

func writeFaces(w *colWriter, faces []compile.Face) {
	n := len(faces)
	strs := make([]uint32, 0, faceStrings*n)
	idx := make([]int64, n)
	flags := make([]uint32, n)
	scalars := make([]float32, faceScalars*n)
	coveredN := make([]uint32, n)
	var covered []uint32
	for i := range faces {
		f := &faces[i]
		strs = append(strs, w.strs.id(f.Chunk), w.strs.id(f.Brush), w.strs.id(f.Texture),
			w.strs.id(f.BlendTexture), w.strs.id(f.BlendMask))
		idx[i] = int64(f.FaceIdx)
		var fl uint32
		for _, b := range []struct {
			on  bool
			bit uint32
		}{{f.Detail, faceDetail}, {f.NoDraw, faceNoDraw}, {f.Smooth, faceSmooth},
			{f.DoubleSided, faceDoubleSided}, {f.UVMapped, faceUVMapped}} {
			if b.on {
				fl |= b.bit
			}
		}
		flags[i] = fl
		faceScalarsOf(f, scalars[faceScalars*i:])
		coveredN[i] = uint32(len(f.CoveredBy))
		for _, s := range f.CoveredBy {
			covered = append(covered, w.strs.id(s))
		}
	}
	w.i64("face.count", []int64{int64(n)})
	w.u32("face.strings", strs)
	w.i64("face.faceIdx", idx)
	w.u32("face.flags", flags)
	w.f32("face.scalars", scalars)
	w.u32("face.coveredBy.n", coveredN)
	w.u32("face.coveredBy", covered)
	for _, l := range faceVec3Lists {
		counts := make([]uint32, n)
		total := 0
		for i := range faces {
			counts[i] = uint32(len(*l.get(&faces[i])))
			total += len(*l.get(&faces[i]))
		}
		flat := make([]float32, 0, 3*total)
		for i := range faces {
			flat = append(flat, vec3sAsFloats(*l.get(&faces[i]))...)
		}
		w.u32(l.name+".n", counts)
		w.f32(l.name, flat)
	}
	for _, l := range faceF32Lists {
		counts := make([]uint32, n)
		var flat []float32
		for i := range faces {
			counts[i] = uint32(len(*l.get(&faces[i])))
			flat = append(flat, *l.get(&faces[i])...)
		}
		w.u32(l.name+".n", counts)
		w.f32(l.name, flat)
	}
	lm := make([]float32, faceLightmapFloats*n)
	for i := range faces {
		faceLightmapOf(&faces[i].Lightmap, lm[faceLightmapFloats*i:])
	}
	w.f32("face.lightmap", lm)
}

// faceLightmapFloats is how many float32s a face's lightmap chart takes
// (compile.LightmapChart): page, the rectangle, origin, U, V, texel.
const faceLightmapFloats = 15

func faceLightmapOf(c *compile.LightmapChart, out []float32) {
	copy(out, []float32{float32(c.Page), float32(c.X), float32(c.Y), float32(c.W), float32(c.H),
		c.Origin[0], c.Origin[1], c.Origin[2], c.U[0], c.U[1], c.U[2], c.V[0], c.V[1], c.V[2], c.Texel})
}

func setFaceLightmap(c *compile.LightmapChart, s []float32) {
	c.Page, c.X, c.Y, c.W, c.H = int32(s[0]), int32(s[1]), int32(s[2]), int32(s[3]), int32(s[4])
	c.Origin = mgl32.Vec3{s[5], s[6], s[7]}
	c.U = mgl32.Vec3{s[8], s[9], s[10]}
	c.V = mgl32.Vec3{s[11], s[12], s[13]}
	c.Texel = s[14]
}

// writeLightmaps writes the lightmap pages (compile/lightmap.go): their count
// and size, then every page's RGBA half floats end to end.
func writeLightmaps(w *colWriter, pages []compile.LightmapPage) {
	size := int64(0)
	if len(pages) > 0 {
		size = int64(pages[0].Size)
	}
	w.i64("lightmap.shape", []int64{int64(len(pages)), size})
	var raw []byte
	for _, p := range pages {
		b := make([]byte, 2*len(p.Texels))
		for i, v := range p.Texels {
			b[2*i], b[2*i+1] = byte(v), byte(v>>8)
		}
		raw = append(raw, b...)
	}
	w.bytesCol("lightmap.texels", raw)
}

// readLightmaps reads them back; a file with none (a per-vertex bake) has
// none.
func readLightmaps(f *colFile) ([]compile.LightmapPage, error) {
	shape, err := f.i64("lightmap.shape")
	if err != nil {
		return nil, nil // written before lightmaps: there are none
	}
	if len(shape) != 2 || shape[0] < 0 || shape[1] < 0 {
		return nil, errCompiledCorrupt
	}
	count, size := int(shape[0]), int(shape[1])
	if count == 0 {
		return nil, nil
	}
	raw, err := f.bytes("lightmap.texels")
	if err != nil {
		return nil, err
	}
	per := size * size * 4
	if len(raw) != 2*per*count {
		return nil, errCompiledCorrupt
	}
	pages := make([]compile.LightmapPage, count)
	for p := range pages {
		tex := make([]uint16, per)
		b := raw[2*per*p:]
		for i := range tex {
			tex[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
		}
		pages[p] = compile.LightmapPage{Size: int32(size), Texels: tex}
	}
	return pages, nil
}

func readFaces(f *colFile) ([]compile.Face, error) {
	cnt, err := f.i64("face.count")
	if err != nil {
		return nil, err
	}
	if len(cnt) != 1 || cnt[0] < 0 {
		return nil, errCompiledCorrupt
	}
	n := int(cnt[0])
	if n == 0 {
		return nil, nil
	}
	strs, err := f.u32("face.strings")
	if err != nil {
		return nil, err
	}
	idx, err := f.i64("face.faceIdx")
	if err != nil {
		return nil, err
	}
	flags, err := f.u32("face.flags")
	if err != nil {
		return nil, err
	}
	scalars, err := f.f32("face.scalars")
	if err != nil {
		return nil, err
	}
	if len(strs) != faceStrings*n || len(idx) != n || len(flags) != n || len(scalars) != faceScalars*n {
		return nil, fmt.Errorf("compiled level: the surface columns disagree on how many surfaces there are")
	}
	faces := make([]compile.Face, n)
	for i := range faces {
		fc := &faces[i]
		s := strs[faceStrings*i:]
		for k, dst := range []*string{&fc.Chunk, &fc.Brush, &fc.Texture, &fc.BlendTexture, &fc.BlendMask} {
			if *dst, err = f.str(s[k]); err != nil {
				return nil, err
			}
		}
		fc.FaceIdx = int(idx[i])
		fl := flags[i]
		fc.Detail = fl&faceDetail != 0
		fc.NoDraw = fl&faceNoDraw != 0
		fc.Smooth = fl&faceSmooth != 0
		fc.DoubleSided = fl&faceDoubleSided != 0
		fc.UVMapped = fl&faceUVMapped != 0
		setFaceScalars(fc, scalars[faceScalars*i:])
	}

	coveredN, err := f.u32("face.coveredBy.n")
	if err != nil {
		return nil, err
	}
	coveredIDs, err := f.u32("face.coveredBy")
	if err != nil {
		return nil, err
	}
	covered := make([]string, len(coveredIDs))
	for i, id := range coveredIDs {
		if covered[i], err = f.str(id); err != nil {
			return nil, err
		}
	}
	runs, err := splitRuns(coveredN, covered, 1, n, "face.coveredBy")
	if err != nil {
		return nil, err
	}
	for i := range faces {
		faces[i].CoveredBy = runs[i]
	}

	// Each list is its own columns and its own field of every face, so the
	// lists are read at once: no two of them write the same memory.
	var jobs []func() error
	for _, l := range faceVec3Lists {
		jobs = append(jobs, func() error {
			counts, err := f.u32(l.name + ".n")
			if err != nil {
				return err
			}
			flat, err := f.f32(l.name)
			if err != nil {
				return err
			}
			if len(flat)%3 != 0 {
				return errCompiledCorrupt
			}
			return eachRun(counts, floatsAsVec3s(flat), n, l.name, func(i int, run []mgl32.Vec3) {
				*l.get(&faces[i]) = run
			})
		})
	}
	for _, l := range faceF32Lists {
		jobs = append(jobs, func() error {
			counts, err := f.u32(l.name + ".n")
			if err != nil {
				return err
			}
			flat, err := f.f32(l.name)
			if err != nil {
				return err
			}
			return eachRun(counts, flat, n, l.name, func(i int, run []float32) {
				*l.get(&faces[i]) = run
			})
		})
	}
	jobs = append(jobs, func() error {
		lm, err := f.f32("face.lightmap")
		if err != nil {
			// Written before lightmaps: every face lit per vertex.
			for i := range faces {
				faces[i].Lightmap.Page = -1
			}
			return nil
		}
		if len(lm) != faceLightmapFloats*n {
			return errCompiledCorrupt
		}
		for i := range faces {
			setFaceLightmap(&faces[i].Lightmap, lm[faceLightmapFloats*i:])
		}
		return nil
	})
	if err := runAll(jobs); err != nil {
		return nil, err
	}
	return faces, nil
}

// runAll runs every job at once and returns the first error in job order.
func runAll(jobs []func() error) error {
	errs := make([]error, len(jobs))
	var wg sync.WaitGroup
	for i, job := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = job()
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------- tree

func planesAsFloats(p []bsp.Plane) []float32 {
	if len(p) == 0 {
		return nil
	}
	return unsafe.Slice(&p[0].N[0], 4*len(p))
}

func floatsAsPlanes(f []float32) []bsp.Plane {
	if len(f) == 0 {
		return nil
	}
	return unsafe.Slice((*bsp.Plane)(unsafe.Pointer(&f[0])), len(f)/4)
}

func intsToI32(v []int) ([]int32, error) {
	out := make([]int32, len(v))
	for i, x := range v {
		if x < math.MinInt32 || x > math.MaxInt32 {
			return nil, fmt.Errorf("compiled level: index %d does not fit the file", x)
		}
		out[i] = int32(x)
	}
	return out, nil
}

func i32sToInts(v []int32) []int {
	if len(v) == 0 {
		return nil
	}
	out := make([]int, len(v))
	for i, x := range v {
		out[i] = int(x)
	}
	return out
}

func writeTree(w *colWriter, t *bsp.Tree) error {
	if t == nil {
		return nil
	}
	w.u32("tree.present", []uint32{1})
	w.i32("tree.root", []int32{int32(t.Root)})
	w.f32("tree.box", []float32{t.Lo[0], t.Lo[1], t.Lo[2], t.Hi[0], t.Hi[1], t.Hi[2]})

	nn := len(t.Nodes)
	planes := make([]bsp.Plane, nn)
	children := make([]int32, 0, 2*nn)
	boxes := make([]float32, 0, 6*nn)
	facesN := make([]uint32, nn)
	var faces []int
	for i := range t.Nodes {
		n := &t.Nodes[i]
		planes[i] = n.Plane
		children = append(children, int32(n.Front), int32(n.Back))
		boxes = append(boxes, n.Lo[0], n.Lo[1], n.Lo[2], n.Hi[0], n.Hi[1], n.Hi[2])
		facesN[i] = uint32(len(n.Faces))
		faces = append(faces, n.Faces...)
	}
	nf, err := intsToI32(faces)
	if err != nil {
		return err
	}
	w.i64("tree.node.count", []int64{int64(nn)})
	w.f32("tree.node.plane", planesAsFloats(planes))
	w.i32("tree.node.children", children)
	w.f32("tree.node.box", boxes)
	w.u32("tree.node.faces.n", facesN)
	w.i32("tree.node.faces", nf)

	nl := len(t.Leaves)
	flags := make([]uint32, nl)
	lboxes := make([]float32, 0, 6*nl)
	planesN := make([]uint32, nl)
	var lplanes []bsp.Plane
	lfacesN := make([]uint32, nl)
	var lfaces []int
	for i := range t.Leaves {
		l := &t.Leaves[i]
		if l.Solid {
			flags[i] |= 1
		}
		if l.Outside {
			flags[i] |= 2
		}
		lboxes = append(lboxes, l.Lo[0], l.Lo[1], l.Lo[2], l.Hi[0], l.Hi[1], l.Hi[2])
		planesN[i] = uint32(len(l.Planes))
		lplanes = append(lplanes, l.Planes...)
		lfacesN[i] = uint32(len(l.Faces))
		lfaces = append(lfaces, l.Faces...)
	}
	lf, err := intsToI32(lfaces)
	if err != nil {
		return err
	}
	w.i64("tree.leaf.count", []int64{int64(nl)})
	w.u32("tree.leaf.flags", flags)
	w.f32("tree.leaf.box", lboxes)
	w.u32("tree.leaf.planes.n", planesN)
	w.f32("tree.leaf.planes", planesAsFloats(lplanes))
	w.u32("tree.leaf.faces.n", lfacesN)
	w.i32("tree.leaf.faces", lf)
	return nil
}

func one64(f *colFile, name string) (int, error) {
	v, err := f.i64(name)
	if err != nil {
		return 0, err
	}
	if len(v) == 0 {
		return 0, nil
	}
	if len(v) != 1 || v[0] < 0 || v[0] > math.MaxInt32 {
		return 0, errCompiledCorrupt
	}
	return int(v[0]), nil
}

func readTree(f *colFile) (*bsp.Tree, error) {
	present, err := f.u32("tree.present")
	if err != nil || len(present) == 0 {
		return nil, err
	}
	t := &bsp.Tree{}
	root, err := f.i32("tree.root")
	if err != nil {
		return nil, err
	}
	box, err := f.f32("tree.box")
	if err != nil {
		return nil, err
	}
	if len(root) != 1 || len(box) != 6 {
		return nil, errCompiledCorrupt
	}
	t.Root = bsp.ChildRef(root[0])
	t.Lo, t.Hi = mgl32.Vec3{box[0], box[1], box[2]}, mgl32.Vec3{box[3], box[4], box[5]}

	nn, err := one64(f, "tree.node.count")
	if err != nil {
		return nil, err
	}
	planes, err := f.f32("tree.node.plane")
	if err != nil {
		return nil, err
	}
	children, err := f.i32("tree.node.children")
	if err != nil {
		return nil, err
	}
	boxes, err := f.f32("tree.node.box")
	if err != nil {
		return nil, err
	}
	if len(planes) != 4*nn || len(children) != 2*nn || len(boxes) != 6*nn {
		return nil, fmt.Errorf("compiled level: the tree's node columns disagree")
	}
	facesN, err := f.u32("tree.node.faces.n")
	if err != nil {
		return nil, err
	}
	faces, err := f.i32("tree.node.faces")
	if err != nil {
		return nil, err
	}
	faceRuns, err := splitRuns(facesN, i32sToInts(faces), 1, nn, "tree.node.faces")
	if err != nil {
		return nil, err
	}
	if nn > 0 {
		t.Nodes = make([]bsp.Node, nn)
	}
	pl := floatsAsPlanes(planes)
	for i := range t.Nodes {
		n := &t.Nodes[i]
		n.Plane = pl[i]
		n.Front, n.Back = bsp.ChildRef(children[2*i]), bsp.ChildRef(children[2*i+1])
		b := boxes[6*i:]
		n.Lo, n.Hi = mgl32.Vec3{b[0], b[1], b[2]}, mgl32.Vec3{b[3], b[4], b[5]}
		n.Faces = faceRuns[i]
	}

	nl, err := one64(f, "tree.leaf.count")
	if err != nil {
		return nil, err
	}
	flags, err := f.u32("tree.leaf.flags")
	if err != nil {
		return nil, err
	}
	lboxes, err := f.f32("tree.leaf.box")
	if err != nil {
		return nil, err
	}
	if len(flags) != nl || len(lboxes) != 6*nl {
		return nil, fmt.Errorf("compiled level: the tree's leaf columns disagree")
	}
	planesN, err := f.u32("tree.leaf.planes.n")
	if err != nil {
		return nil, err
	}
	lplanes, err := f.f32("tree.leaf.planes")
	if err != nil {
		return nil, err
	}
	if len(lplanes)%4 != 0 {
		return nil, errCompiledCorrupt
	}
	planeRuns, err := splitRuns(planesN, floatsAsPlanes(lplanes), 1, nl, "tree.leaf.planes")
	if err != nil {
		return nil, err
	}
	lfacesN, err := f.u32("tree.leaf.faces.n")
	if err != nil {
		return nil, err
	}
	lfaces, err := f.i32("tree.leaf.faces")
	if err != nil {
		return nil, err
	}
	lfaceRuns, err := splitRuns(lfacesN, i32sToInts(lfaces), 1, nl, "tree.leaf.faces")
	if err != nil {
		return nil, err
	}
	if nl > 0 {
		t.Leaves = make([]bsp.Leaf, nl)
	}
	for i := range t.Leaves {
		l := &t.Leaves[i]
		l.Solid = flags[i]&1 != 0
		l.Outside = flags[i]&2 != 0
		b := lboxes[6*i:]
		l.Lo, l.Hi = mgl32.Vec3{b[0], b[1], b[2]}, mgl32.Vec3{b[3], b[4], b[5]}
		l.Planes = planeRuns[i]
		l.Faces = lfaceRuns[i]
	}
	return t, nil
}

// ---------------------------------------------------------------- PVS

func writePVS(w *colWriter, v *bsp.PVS) {
	if v == nil {
		return
	}
	w.i64("pvs.shape", []int64{int64(v.Leaves), int64(v.RowBytes)})
	w.bytesCol("pvs.bits", v.Bits)
}

func readPVS(f *colFile) (*bsp.PVS, error) {
	shape, err := f.i64("pvs.shape")
	if err != nil || len(shape) == 0 {
		return nil, err
	}
	if len(shape) != 2 {
		return nil, errCompiledCorrupt
	}
	bits, err := f.bytes("pvs.bits")
	if err != nil {
		return nil, err
	}
	v := &bsp.PVS{Leaves: int(shape[0]), RowBytes: int(shape[1])}
	if len(bits) > 0 {
		v.Bits = append([]byte(nil), bits...)
	}
	return v, nil
}

// ---------------------------------------------------------------- clusters
//
// The cluster records are plain runs of four-byte numbers (cluster.Cluster,
// LODCluster, LODNode), so they are written as their memory, word for word.
// Each column carries the record's width in words beside it: a record that
// grows a field reads as a width mismatch and asks for a recompile, rather
// than decoding every record shifted by one.

// podWords is how many four-byte words a value of t is, when t is nothing but
// four-byte numbers, and 0 otherwise.
func podWords(t reflect.Type) int {
	switch t.Kind() {
	case reflect.Float32, reflect.Int32, reflect.Uint32:
		return 1
	case reflect.Array:
		if n := podWords(t.Elem()); n > 0 {
			return n * t.Len()
		}
		return 0
	case reflect.Struct:
		total := 0
		for i := 0; i < t.NumField(); i++ {
			n := podWords(t.Field(i).Type)
			if n == 0 {
				return 0
			}
			total += n
		}
		if uintptr(total)*4 != t.Size() {
			return 0
		}
		return total
	}
	return 0
}

func writePOD[T any](w *colWriter, name string, v []T) error {
	words := podWords(reflect.TypeOf((*T)(nil)).Elem())
	if words == 0 {
		return fmt.Errorf("compiled level: %T is not a run of four-byte numbers", *new(T))
	}
	w.u32(name+".words", []uint32{uint32(words)})
	w.add(name, colU32, words*len(v), wordsLE(unsafe.Pointer(unsafe.SliceData(v)), words*len(v)))
	return nil
}

// readPOD reads a run written by writePOD. Empty reads as nil.
func readPOD[T any](f *colFile, name string) ([]T, error) {
	c, err := f.col(name, colU32)
	if err != nil || c.count == 0 {
		return nil, err
	}
	words := podWords(reflect.TypeOf((*T)(nil)).Elem())
	stored, err := f.u32(name + ".words")
	if err != nil {
		return nil, err
	}
	if len(stored) != 1 || int(stored[0]) != words || words == 0 {
		return nil, fmt.Errorf("compiled level: %s records were written %v words wide and this build's are %d — recompile the level", name, stored, words)
	}
	if c.count%words != 0 {
		return nil, errCompiledCorrupt
	}
	out := make([]T, c.count/words)
	readWords(unsafe.Pointer(&out[0]), c.count, c.data)
	return out, nil
}

// readPODRuns reads a run column of POD records with its count column, split
// per record.
func readPODRuns[T any](f *colFile, name string, n int) ([][]T, error) {
	flat, err := readPOD[T](f, name)
	if err != nil {
		return nil, err
	}
	counts, err := f.u32(name + ".n")
	if err != nil {
		return nil, err
	}
	return splitRuns(counts, flat, 1, n, name)
}

func writeClusters(w *colWriter, groups []world.StaticClusterGroup) error {
	g := len(groups)
	keys := make([]uint32, g)
	sigs := make([]uint64, g)
	orderN := make([]uint32, g)
	var order []uint32
	clN := make([]uint32, g)
	var cls []cluster.Cluster
	hasDAG := make([]uint32, g)
	var dags []*world.StaticGroupDAG
	for i := range groups {
		gr := &groups[i]
		keys[i] = w.strs.id(gr.Key)
		sigs[i] = gr.Sig
		orderN[i] = uint32(len(gr.Order))
		order = append(order, gr.Order...)
		clN[i] = uint32(len(gr.Clusters))
		cls = append(cls, gr.Clusters...)
		if gr.DAG != nil {
			hasDAG[i] = 1
			dags = append(dags, gr.DAG)
		}
	}
	w.i64("cg.count", []int64{int64(g)})
	w.u32("cg.key", keys)
	w.u64("cg.sig", sigs)
	w.u32("cg.order.n", orderN)
	w.u32("cg.order", order)
	w.u32("cg.clusters.n", clN)
	if err := writePOD(w, "cg.clusters", cls); err != nil {
		return err
	}
	w.u32("cg.dag", hasDAG)

	d := len(dags)
	idxN, baseN, cluN, nodeN, leafN := make([]uint32, d), make([]uint32, d), make([]uint32, d), make([]uint32, d), make([]uint32, d)
	levels, leaves := make([]int32, d), make([]int32, d)
	var idx, base []uint32
	var lcl []cluster.LODCluster
	var nodes []cluster.LODNode
	var cleaf []int32
	for i, dg := range dags {
		idxN[i] = uint32(len(dg.Indices))
		idx = append(idx, dg.Indices...)
		cluN[i] = uint32(len(dg.Clusters))
		lcl = append(lcl, dg.Clusters...)
		baseN[i] = uint32(len(dg.BaseOrder))
		base = append(base, dg.BaseOrder...)
		levels[i] = dg.Levels
		nodeN[i] = uint32(len(dg.Nodes))
		nodes = append(nodes, dg.Nodes...)
		leafN[i] = uint32(len(dg.ClusterLeaf))
		cleaf = append(cleaf, dg.ClusterLeaf...)
		leaves[i] = dg.Leaves
	}
	w.u32("dag.indices.n", idxN)
	w.u32("dag.indices", idx)
	w.u32("dag.clusters.n", cluN)
	if err := writePOD(w, "dag.clusters", lcl); err != nil {
		return err
	}
	w.u32("dag.baseOrder.n", baseN)
	w.u32("dag.baseOrder", base)
	w.i32("dag.levels", levels)
	w.u32("dag.nodes.n", nodeN)
	if err := writePOD(w, "dag.nodes", nodes); err != nil {
		return err
	}
	w.u32("dag.clusterLeaf.n", leafN)
	w.i32("dag.clusterLeaf", cleaf)
	w.i32("dag.leaves", leaves)
	return nil
}

func readClusters(f *colFile) ([]world.StaticClusterGroup, error) {
	g, err := one64(f, "cg.count")
	if err != nil || g == 0 {
		return nil, err
	}
	keys, err := f.u32("cg.key")
	if err != nil {
		return nil, err
	}
	sigs, err := f.u64("cg.sig")
	if err != nil {
		return nil, err
	}
	hasDAG, err := f.u32("cg.dag")
	if err != nil {
		return nil, err
	}
	if len(keys) != g || len(sigs) != g || len(hasDAG) != g {
		return nil, fmt.Errorf("compiled level: the cluster columns disagree")
	}
	orderN, err := f.u32("cg.order.n")
	if err != nil {
		return nil, err
	}
	order, err := f.u32("cg.order")
	if err != nil {
		return nil, err
	}
	orders, err := splitRuns(orderN, order, 1, g, "cg.order")
	if err != nil {
		return nil, err
	}
	cls, err := readPODRuns[cluster.Cluster](f, "cg.clusters", g)
	if err != nil {
		return nil, err
	}

	d := 0
	for _, h := range hasDAG {
		if h != 0 {
			d++
		}
	}
	u32Runs := func(name string) ([][]uint32, error) {
		counts, err := f.u32(name + ".n")
		if err != nil {
			return nil, err
		}
		flat, err := f.u32(name)
		if err != nil {
			return nil, err
		}
		return splitRuns(counts, flat, 1, d, name)
	}
	idx, err := u32Runs("dag.indices")
	if err != nil {
		return nil, err
	}
	base, err := u32Runs("dag.baseOrder")
	if err != nil {
		return nil, err
	}
	lcl, err := readPODRuns[cluster.LODCluster](f, "dag.clusters", d)
	if err != nil {
		return nil, err
	}
	nodes, err := readPODRuns[cluster.LODNode](f, "dag.nodes", d)
	if err != nil {
		return nil, err
	}
	leafN, err := f.u32("dag.clusterLeaf.n")
	if err != nil {
		return nil, err
	}
	leafFlat, err := f.i32("dag.clusterLeaf")
	if err != nil {
		return nil, err
	}
	cleaf, err := splitRuns(leafN, leafFlat, 1, d, "dag.clusterLeaf")
	if err != nil {
		return nil, err
	}
	levels, err := f.i32("dag.levels")
	if err != nil {
		return nil, err
	}
	leaves, err := f.i32("dag.leaves")
	if err != nil {
		return nil, err
	}
	if len(levels) != d || len(leaves) != d {
		return nil, fmt.Errorf("compiled level: the cluster DAG columns disagree")
	}

	out := make([]world.StaticClusterGroup, g)
	di := 0
	for i := range out {
		gr := &out[i]
		if gr.Key, err = f.str(keys[i]); err != nil {
			return nil, err
		}
		gr.Sig = sigs[i]
		gr.Order = orders[i]
		gr.Clusters = cls[i]
		if hasDAG[i] != 0 {
			gr.DAG = &world.StaticGroupDAG{
				Indices:     idx[di],
				Clusters:    lcl[di],
				BaseOrder:   base[di],
				Levels:      levels[di],
				Nodes:       nodes[di],
				ClusterLeaf: cleaf[di],
				Leaves:      leaves[di],
			}
			di++
		}
	}
	return out, nil
}
