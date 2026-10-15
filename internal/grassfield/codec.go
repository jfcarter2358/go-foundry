package grassfield

// codec.go — the .fgk and .fgrass files, little-endian.
//
// KIT
//
//	"FGKT" u32 version  u32 kinds
//	per kind: str name, str texture, f32 height radius bend flutter range,
//	          u32 flags, u32 lods,
//	          per LOD: u32 vertices, u32 indices, f32×VertexFloats each, u32 each
//
// FIELD
//
//	"FGRS" u32 version  str kit
//	f32×3 origin  f32×3 size  f32 scale_max  f32 cell
//	u32 cells_x  u32 cells_z  u32 kinds  u32 instances
//	(u32 offset, u32 count) × cells·kinds
//	(f32 min_y, f32 max_y) × cells
//	u32×3 × instances
//
// A str is a u32 length and its bytes. Decoders check every count against
// what is left of the file before allocating, so a truncated or corrupt
// file is an error, never a panic or a giant allocation.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

type writer struct{ b bytes.Buffer }

func (w *writer) u32(v uint32)  { _ = binary.Write(&w.b, binary.LittleEndian, v) }
func (w *writer) f32(v float32) { w.u32(math.Float32bits(v)) }
func (w *writer) str(s string) {
	w.u32(uint32(len(s)))
	w.b.WriteString(s)
}

type reader struct {
	b   []byte
	err error
}

var errShort = errors.New("truncated")

func (r *reader) need(n int) bool {
	if r.err != nil {
		return false
	}
	if n < 0 || len(r.b) < n {
		r.err = errShort
		return false
	}
	return true
}

func (r *reader) u32() uint32 {
	if !r.need(4) {
		return 0
	}
	v := binary.LittleEndian.Uint32(r.b)
	r.b = r.b[4:]
	return v
}

func (r *reader) f32() float32 { return math.Float32frombits(r.u32()) }

func (r *reader) str() string {
	n := int(r.u32())
	if !r.need(n) {
		return ""
	}
	s := string(r.b[:n])
	r.b = r.b[n:]
	return s
}

// count reads a count of items of size bytes each and checks they fit.
func (r *reader) count(size int) int {
	n := int(r.u32())
	if r.err == nil && (n < 0 || size > 0 && n > len(r.b)/size) {
		r.err = errShort
		return 0
	}
	return n
}

func (r *reader) floats(n int) []float32 {
	if !r.need(4 * n) {
		return nil
	}
	out := make([]float32, n)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(r.b[4*i:]))
	}
	r.b = r.b[4*n:]
	return out
}

func (r *reader) words(n int) []uint32 {
	if !r.need(4 * n) {
		return nil
	}
	out := make([]uint32, n)
	for i := range out {
		out[i] = binary.LittleEndian.Uint32(r.b[4*i:])
	}
	r.b = r.b[4*n:]
	return out
}

func (r *reader) magic(m string) error {
	if !r.need(4) || string(r.b[:4]) != m {
		return fmt.Errorf("not a %s file", m)
	}
	r.b = r.b[4:]
	return nil
}

// EncodeKit is k's file.
func EncodeKit(k *Kit) ([]byte, error) {
	if len(k.Kinds) == 0 || len(k.Kinds) > MaxKinds {
		return nil, fmt.Errorf("grass kit: %d kinds", len(k.Kinds))
	}
	var w writer
	w.b.WriteString(KitMagic)
	w.u32(KitVersion)
	w.u32(uint32(len(k.Kinds)))
	for i := range k.Kinds {
		kd := &k.Kinds[i]
		if len(kd.LODs) == 0 || len(kd.LODs) > MaxLODs {
			return nil, fmt.Errorf("grass kit: kind %s has %d LODs", kd.Name, len(kd.LODs))
		}
		w.str(kd.Name)
		w.str(kd.Texture)
		for _, v := range []float32{kd.Height, kd.Radius, kd.Bend, kd.Flutter, kd.Range} {
			w.f32(v)
		}
		w.u32(kd.Flags)
		w.u32(uint32(len(kd.LODs)))
		for _, m := range kd.LODs {
			if len(m.Vertices)%VertexFloats != 0 || len(m.Indices)%3 != 0 {
				return nil, fmt.Errorf("grass kit: kind %s has a ragged mesh", kd.Name)
			}
			nv := uint32(m.VertexCount())
			for _, ix := range m.Indices {
				if ix >= nv {
					return nil, fmt.Errorf("grass kit: kind %s index %d of %d vertices", kd.Name, ix, nv)
				}
			}
			w.u32(nv)
			w.u32(uint32(len(m.Indices)))
			for _, v := range m.Vertices {
				w.f32(v)
			}
			for _, ix := range m.Indices {
				w.u32(ix)
			}
		}
	}
	return w.b.Bytes(), nil
}

// DecodeKit reads a kit file.
func DecodeKit(data []byte) (*Kit, error) {
	r := &reader{b: data}
	if err := r.magic(KitMagic); err != nil {
		return nil, err
	}
	if v := r.u32(); r.err == nil && v != KitVersion {
		return nil, fmt.Errorf("grass kit version %d, want %d", v, KitVersion)
	}
	n := r.count(16)
	if n > MaxKinds {
		return nil, fmt.Errorf("grass kit: %d kinds", n)
	}
	k := &Kit{Kinds: make([]Kind, 0, n)}
	for i := 0; i < n && r.err == nil; i++ {
		var kd Kind
		kd.Name, kd.Texture = r.str(), r.str()
		kd.Height, kd.Radius, kd.Bend, kd.Flutter, kd.Range = r.f32(), r.f32(), r.f32(), r.f32(), r.f32()
		kd.Flags = r.u32()
		nl := int(r.u32())
		if r.err == nil && (nl < 1 || nl > MaxLODs) {
			return nil, fmt.Errorf("grass kit: kind %s has %d LODs", kd.Name, nl)
		}
		for l := 0; l < nl && r.err == nil; l++ {
			nv := r.count(4 * VertexFloats)
			ni := int(r.u32())
			if r.err == nil && ni%3 != 0 {
				return nil, fmt.Errorf("grass kit: kind %s: %d indices", kd.Name, ni)
			}
			m := Mesh{Vertices: r.floats(nv * VertexFloats), Indices: r.words(ni)}
			for _, ix := range m.Indices {
				if ix >= uint32(nv) {
					return nil, fmt.Errorf("grass kit: kind %s index %d of %d vertices", kd.Name, ix, nv)
				}
			}
			kd.LODs = append(kd.LODs, m)
		}
		k.Kinds = append(k.Kinds, kd)
	}
	if r.err != nil {
		return nil, fmt.Errorf("grass kit: %w", r.err)
	}
	return k, nil
}

// EncodeField is f's file.
func EncodeField(f *Field) ([]byte, error) {
	nc := f.CellsX * f.CellsZ
	if nc <= 0 || f.Kinds <= 0 || len(f.Runs) != nc*f.Kinds || len(f.CellY) != nc || len(f.Data)%InstanceWords != 0 {
		return nil, fmt.Errorf("grass field: inconsistent (%d×%d cells, %d kinds, %d runs)", f.CellsX, f.CellsZ, f.Kinds, len(f.Runs))
	}
	if err := f.check(); err != nil {
		return nil, err
	}
	var w writer
	w.b.WriteString(FieldMagic)
	w.u32(FieldVersion)
	w.str(f.Kit)
	for _, v := range f.Origin {
		w.f32(v)
	}
	for _, v := range f.Size {
		w.f32(v)
	}
	w.f32(f.ScaleMax)
	w.f32(f.Cell)
	w.u32(uint32(f.CellsX))
	w.u32(uint32(f.CellsZ))
	w.u32(uint32(f.Kinds))
	w.u32(uint32(f.Count()))
	for _, r := range f.Runs {
		w.u32(r.Offset)
		w.u32(r.Count)
	}
	for _, y := range f.CellY {
		w.f32(y[0])
		w.f32(y[1])
	}
	for _, d := range f.Data {
		w.u32(d)
	}
	return w.b.Bytes(), nil
}

// check verifies every run lies inside the data.
func (f *Field) check() error {
	n := uint64(f.Count())
	for i, r := range f.Runs {
		if uint64(r.Offset)+uint64(r.Count) > n {
			return fmt.Errorf("grass field: run %d (%d+%d) past %d instances", i, r.Offset, r.Count, n)
		}
	}
	return nil
}

// DecodeField reads a field file.
func DecodeField(data []byte) (*Field, error) {
	r := &reader{b: data}
	if err := r.magic(FieldMagic); err != nil {
		return nil, err
	}
	if v := r.u32(); r.err == nil && v != FieldVersion {
		return nil, fmt.Errorf("grass field version %d, want %d", v, FieldVersion)
	}
	f := &Field{Kit: r.str()}
	for i := range f.Origin {
		f.Origin[i] = r.f32()
	}
	for i := range f.Size {
		f.Size[i] = r.f32()
	}
	f.ScaleMax, f.Cell = r.f32(), r.f32()
	f.CellsX, f.CellsZ, f.Kinds = int(r.u32()), int(r.u32()), int(r.u32())
	n := int(r.u32())
	if r.err != nil {
		return nil, fmt.Errorf("grass field: %w", r.err)
	}
	nc := f.CellsX * f.CellsZ
	if f.CellsX <= 0 || f.CellsZ <= 0 || f.Kinds <= 0 || f.Kinds > MaxKinds || nc > len(r.b) ||
		nc*f.Kinds > len(r.b)/8 || n > len(r.b)/(4*InstanceWords) {
		return nil, fmt.Errorf("grass field: bad header (%d×%d cells, %d kinds, %d instances)", f.CellsX, f.CellsZ, f.Kinds, n)
	}
	runs := r.words(2 * nc * f.Kinds)
	f.Runs = make([]Run, nc*f.Kinds)
	for i := range f.Runs {
		if runs != nil {
			f.Runs[i] = Run{Offset: runs[2*i], Count: runs[2*i+1]}
		}
	}
	ys := r.floats(2 * nc)
	f.CellY = make([][2]float32, nc)
	for c := range f.CellY {
		if ys != nil {
			f.CellY[c] = [2]float32{ys[2*c], ys[2*c+1]}
		}
	}
	f.Data = r.words(n * InstanceWords)
	if r.err != nil {
		return nil, fmt.Errorf("grass field: %w", r.err)
	}
	if err := f.check(); err != nil {
		return nil, err
	}
	return f, nil
}
