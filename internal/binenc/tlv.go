// Package binenc is a deterministic, self-describing binary encoding of Go
// values by reflection: what a compiled level stores its chunks in
// (levelio/fbsp_binary.go), and what the model cache stores a parsed .fmdl in
// (model/model_cache.go).
//
// It began as the encoding for the parts of a compiled level too irregular to
// lay out as columns: the chunks (world.Chunk as runtime values — brushes,
// terrain, script data, tweens) and the compile's Stats.
//
// These used to be gob, along with the rest of the file, and gob had the one
// property worth keeping: it matches fields by NAME, so adding a field to
// world.Chunk does not turn every compiled level on disk into garbage. It also
// had one that disqualifies it for a build artefact: it writes a map in the
// order Go happens to iterate it, which is random. A chunk with script
// attributes or a terrain paint layer encoded differently every time it was
// compiled, so two compiles of one level never produced the same file.
//
// So this is a small self-describing encoding with gob's semantics where they
// matter and none of its nondeterminism:
//
//   - Every value carries a one-byte tag saying what it is, so a reader that
//     meets a field it does not know can skip it — the tolerance gob gave.
//   - A struct is its fields BY NAME (an index into the blob's name table),
//     so fields can be added, removed and reordered across versions.
//   - A field holding its zero value is not written, as gob does not write
//     one, and decodes as zero. An empty slice decodes as nil, as gob's does;
//     a non-nil empty map stays a non-nil empty map, as gob's does. (With
//     Exact, an empty slice stays an empty slice and nil stays nil — for a
//     cache that must hand back exactly what the slow path would have.)
//   - A map's entries are written sorted by their encoded key bytes, which is
//     the whole point.
//   - A type that serialises itself for gob (gob.GobEncoder, then
//     encoding.BinaryMarshaler, then encoding.TextMarshaler — gob's order) is
//     written as that method's bytes and read back through its pair. That is
//     what keeps gfx.Geometry, which encodes as nothing, out of the file.
//   - A run of float32s — []float32, []mgl32.Vec3, a Mat4 — is written packed,
//     four bytes a float, rather than a tag per number.
//
// It is reflection-driven and therefore not fast, which is fine for what it
// carries: a level's chunks are a few hundred kilobytes. The bulk of a
// compiled level — the surfaces, the tree, the visibility set, the clusters —
// is laid out as flat columns instead (fbsp_binary.go).
package binenc

import (
	"bytes"
	"encoding"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"math"
	"reflect"
	"sort"
	"unsafe"
)

const (
	tlvNil    byte = iota // a nil element of a list of pointers
	tlvBool               // one byte
	tlvInt                // zigzag varint
	tlvUint               // varint
	tlvF32                // four bytes
	tlvF64                // eight bytes
	tlvString             // varint length, then the bytes; also a []byte
	tlvList               // varint count, then each element as a value
	tlvMap                // varint count, then key and value of each entry, sorted by key bytes
	tlvStruct             // varint field count, then (varint name index, value) per field
	tlvBlob               // varint length, then what the type's own marshaller wrote
	tlvPacked             // varint count, varint floats per element, then the floats
	tlvEmpty              // an empty, non-nil slice (Exact only)
)

// Options change what is kept.
type Options struct {
	// Exact keeps empty-but-not-nil slices apart from nil ones. Off, an empty
	// slice is left out like a zero value and reads back as nil, which is
	// gob's rule and what a compiled level was always decoded with.
	Exact bool
}

var (
	gobEncoderType    = reflect.TypeOf((*gob.GobEncoder)(nil)).Elem()
	gobDecoderType    = reflect.TypeOf((*gob.GobDecoder)(nil)).Elem()
	binMarshalerType  = reflect.TypeOf((*encoding.BinaryMarshaler)(nil)).Elem()
	binUnmarshalType  = reflect.TypeOf((*encoding.BinaryUnmarshaler)(nil)).Elem()
	textMarshalerType = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
	textUnmarshalType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
)

// Marshal encodes v, which must be a pointer (so that everything under it is
// addressable and a run of floats can be read in place), with gob's rules.
func Marshal(v any) ([]byte, error) { return MarshalOptions(v, Options{}) }

// MarshalOptions is Marshal with options.
func MarshalOptions(v any, opt Options) ([]byte, error) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return nil, fmt.Errorf("binenc.Marshal: want a non-nil pointer, got %T", v)
	}
	e := &tlvEnc{names: map[string]uint64{}, exact: opt.Exact}
	if err := e.value(rv.Elem()); err != nil {
		return nil, err
	}
	// The name table goes first so a reader has it before the fields that
	// index it; it is built while encoding, so the body is assembled first.
	var out []byte
	out = binary.AppendUvarint(out, uint64(len(e.order)))
	for _, n := range e.order {
		out = binary.AppendUvarint(out, uint64(len(n)))
		out = append(out, n...)
	}
	return append(out, e.buf...), nil
}

// Unmarshal decodes data into v, a non-nil pointer to a zero value. It reads
// what either option wrote.
func Unmarshal(data []byte, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return fmt.Errorf("binenc.Unmarshal: want a non-nil pointer, got %T", v)
	}
	d := &tlvDec{data: data, fields: map[reflect.Type]map[string]int{}}
	n, err := d.uvarint()
	if err != nil {
		return err
	}
	if n > uint64(len(data)) {
		return errTLVCorrupt
	}
	d.names = make([]string, n)
	for i := range d.names {
		b, err := d.bytes()
		if err != nil {
			return err
		}
		d.names[i] = string(b)
	}
	if err := d.value(rv.Elem()); err != nil {
		return err
	}
	if d.pos != len(d.data) {
		return fmt.Errorf("binenc: %d bytes left over", len(d.data)-d.pos)
	}
	return nil
}

var errTLVCorrupt = fmt.Errorf("binenc: data is truncated or corrupt")

type tlvEnc struct {
	buf   []byte
	names map[string]uint64
	order []string
	exact bool
}

func (e *tlvEnc) name(n string) uint64 {
	if i, ok := e.names[n]; ok {
		return i
	}
	i := uint64(len(e.order))
	e.names[n] = i
	e.order = append(e.order, n)
	return i
}

// marshaller returns the bytes of a type that serialises itself for gob, and
// whether it is one.
func marshaller(v reflect.Value) ([]byte, bool, error) {
	t := v.Type()
	pv := v
	if !v.CanAddr() {
		// A map value: copy it somewhere addressable so a pointer-receiver
		// method can be found.
		pv = reflect.New(t).Elem()
		pv.Set(v)
	}
	call := func(iface reflect.Type) (reflect.Value, bool) {
		if t.Implements(iface) {
			return pv, true
		}
		if reflect.PointerTo(t).Implements(iface) {
			return pv.Addr(), true
		}
		return reflect.Value{}, false
	}
	if m, ok := call(gobEncoderType); ok {
		b, err := m.Interface().(gob.GobEncoder).GobEncode()
		return b, true, err
	}
	if m, ok := call(binMarshalerType); ok {
		b, err := m.Interface().(encoding.BinaryMarshaler).MarshalBinary()
		return b, true, err
	}
	if m, ok := call(textMarshalerType); ok {
		b, err := m.Interface().(encoding.TextMarshaler).MarshalText()
		return b, true, err
	}
	return nil, false, nil
}

// floatShape is how many float32s a value of t is, when it is nothing but
// float32s laid end to end — a float32, or an array of such — and 0 otherwise.
func floatShape(t reflect.Type) int {
	switch t.Kind() {
	case reflect.Float32:
		return 1
	case reflect.Array:
		if n := floatShape(t.Elem()); n > 0 && t.Len() > 0 {
			return n * t.Len()
		}
	}
	return 0
}

// omits is whether a struct field is left out: its zero value, or — gob's
// rule, unless Exact — a slice with nothing in it.
func (e *tlvEnc) omits(v reflect.Value) bool {
	if v.Kind() == reflect.Slice && !e.exact {
		return v.Len() == 0
	}
	return v.IsZero()
}

func (e *tlvEnc) value(v reflect.Value) error {
	if v.Kind() != reflect.Ptr && v.Kind() != reflect.Interface {
		if b, ok, err := marshaller(v); ok {
			if err != nil {
				return err
			}
			e.buf = append(e.buf, tlvBlob)
			e.buf = binary.AppendUvarint(e.buf, uint64(len(b)))
			e.buf = append(e.buf, b...)
			return nil
		}
	}
	switch v.Kind() {
	case reflect.Bool:
		b := byte(0)
		if v.Bool() {
			b = 1
		}
		e.buf = append(e.buf, tlvBool, b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		e.buf = append(e.buf, tlvInt)
		e.buf = binary.AppendVarint(e.buf, v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		e.buf = append(e.buf, tlvUint)
		e.buf = binary.AppendUvarint(e.buf, v.Uint())
	case reflect.Float32:
		e.buf = append(e.buf, tlvF32)
		e.buf = binary.LittleEndian.AppendUint32(e.buf, math.Float32bits(float32(v.Float())))
	case reflect.Float64:
		e.buf = append(e.buf, tlvF64)
		e.buf = binary.LittleEndian.AppendUint64(e.buf, math.Float64bits(v.Float()))
	case reflect.String:
		e.buf = append(e.buf, tlvString)
		e.buf = binary.AppendUvarint(e.buf, uint64(v.Len()))
		e.buf = append(e.buf, v.String()...)
	case reflect.Ptr:
		if v.IsNil() {
			e.buf = append(e.buf, tlvNil)
			return nil
		}
		return e.value(v.Elem())
	case reflect.Slice, reflect.Array:
		return e.list(v)
	case reflect.Map:
		return e.mapValue(v)
	case reflect.Struct:
		return e.structValue(v)
	default:
		return fmt.Errorf("binenc: cannot encode a %s (%s)", v.Kind(), v.Type())
	}
	return nil
}

func (e *tlvEnc) list(v reflect.Value) error {
	n := v.Len()
	if v.Kind() == reflect.Slice {
		if v.IsNil() {
			e.buf = append(e.buf, tlvNil)
			return nil
		}
		if n == 0 && e.exact {
			e.buf = append(e.buf, tlvEmpty)
			return nil
		}
	}
	et := v.Type().Elem()
	if et.Kind() == reflect.Uint8 && v.Kind() == reflect.Slice {
		e.buf = append(e.buf, tlvString)
		e.buf = binary.AppendUvarint(e.buf, uint64(n))
		e.buf = append(e.buf, v.Bytes()...)
		return nil
	}
	if fs := floatShape(et); fs > 0 {
		e.buf = append(e.buf, tlvPacked)
		e.buf = binary.AppendUvarint(e.buf, uint64(n))
		e.buf = binary.AppendUvarint(e.buf, uint64(fs))
		if n == 0 {
			return nil
		}
		if v.Kind() == reflect.Array && !v.CanAddr() {
			c := reflect.New(v.Type()).Elem()
			c.Set(v)
			v = c
		}
		var p unsafe.Pointer
		if v.Kind() == reflect.Slice {
			p = v.UnsafePointer()
		} else {
			p = v.Addr().UnsafePointer()
		}
		for _, f := range unsafe.Slice((*float32)(p), n*fs) {
			e.buf = binary.LittleEndian.AppendUint32(e.buf, math.Float32bits(f))
		}
		return nil
	}
	e.buf = append(e.buf, tlvList)
	e.buf = binary.AppendUvarint(e.buf, uint64(n))
	for i := 0; i < n; i++ {
		if err := e.value(v.Index(i)); err != nil {
			return err
		}
	}
	return nil
}

func (e *tlvEnc) mapValue(v reflect.Value) error {
	if v.IsNil() {
		e.buf = append(e.buf, tlvNil)
		return nil
	}
	// The entries are put in order before ANY of them is written: the
	// iteration order is Go's to randomise, and the file must not follow it —
	// not in the entries' order, and not in the order a struct inside one
	// first uses a field name, which is what numbers the name table. So the
	// sort key is each key encoded on its own, against a table of its own,
	// and only then are the entries encoded for real, in that order.
	type entry struct {
		sortKey []byte
		k, v    reflect.Value
	}
	entries := make([]entry, 0, v.Len())
	it := v.MapRange()
	for it.Next() {
		scratch := &tlvEnc{names: map[string]uint64{}}
		if err := scratch.value(it.Key()); err != nil {
			return err
		}
		entries = append(entries, entry{scratch.buf, it.Key(), it.Value()})
	}
	sort.Slice(entries, func(a, b int) bool { return bytes.Compare(entries[a].sortKey, entries[b].sortKey) < 0 })
	e.buf = append(e.buf, tlvMap)
	e.buf = binary.AppendUvarint(e.buf, uint64(len(entries)))
	for _, en := range entries {
		if err := e.value(en.k); err != nil {
			return err
		}
		if err := e.value(en.v); err != nil {
			return err
		}
	}
	return nil
}

func (e *tlvEnc) structValue(v reflect.Value) error {
	t := v.Type()
	var present []int
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).IsExported() && !e.omits(v.Field(i)) {
			present = append(present, i)
		}
	}
	e.buf = append(e.buf, tlvStruct)
	e.buf = binary.AppendUvarint(e.buf, uint64(len(present)))
	for _, i := range present {
		e.buf = binary.AppendUvarint(e.buf, e.name(t.Field(i).Name))
		if err := e.value(v.Field(i)); err != nil {
			return err
		}
	}
	return nil
}

type tlvDec struct {
	data   []byte
	pos    int
	names  []string
	fields map[reflect.Type]map[string]int
}

func (d *tlvDec) byte() (byte, error) {
	if d.pos >= len(d.data) {
		return 0, errTLVCorrupt
	}
	b := d.data[d.pos]
	d.pos++
	return b, nil
}

func (d *tlvDec) uvarint() (uint64, error) {
	x, n := binary.Uvarint(d.data[d.pos:])
	if n <= 0 {
		return 0, errTLVCorrupt
	}
	d.pos += n
	return x, nil
}

func (d *tlvDec) varint() (int64, error) {
	x, n := binary.Varint(d.data[d.pos:])
	if n <= 0 {
		return 0, errTLVCorrupt
	}
	d.pos += n
	return x, nil
}

func (d *tlvDec) take(n uint64) ([]byte, error) {
	if n > uint64(len(d.data)-d.pos) {
		return nil, errTLVCorrupt
	}
	b := d.data[d.pos : d.pos+int(n)]
	d.pos += int(n)
	return b, nil
}

func (d *tlvDec) bytes() ([]byte, error) {
	n, err := d.uvarint()
	if err != nil {
		return nil, err
	}
	return d.take(n)
}

// count reads an element count and refuses one the remaining bytes could not
// possibly hold (every element is at least a byte), so a corrupt count is an
// error rather than a giant allocation.
func (d *tlvDec) count() (int, error) {
	n, err := d.uvarint()
	if err != nil {
		return 0, err
	}
	if n > uint64(len(d.data)-d.pos) {
		return 0, errTLVCorrupt
	}
	return int(n), nil
}

func (d *tlvDec) mismatch(tag byte, t reflect.Type) error {
	return fmt.Errorf("binenc: data holds a value of kind %d where this build has a %s", tag, t)
}

// unmarshaller finds the decoding half of a self-serialising type on the
// addressable v.
func unmarshaller(v reflect.Value) (func([]byte) error, bool) {
	pt := reflect.PointerTo(v.Type())
	if !pt.Implements(gobEncoderType) && !v.Type().Implements(gobEncoderType) &&
		!pt.Implements(binMarshalerType) && !v.Type().Implements(binMarshalerType) &&
		!pt.Implements(textMarshalerType) && !v.Type().Implements(textMarshalerType) {
		return nil, false
	}
	a := v.Addr().Interface()
	switch {
	case pt.Implements(gobEncoderType) || v.Type().Implements(gobEncoderType):
		if u, ok := a.(gob.GobDecoder); ok {
			return u.GobDecode, true
		}
	case pt.Implements(binMarshalerType) || v.Type().Implements(binMarshalerType):
		if u, ok := a.(encoding.BinaryUnmarshaler); ok {
			return u.UnmarshalBinary, true
		}
	default:
		if u, ok := a.(encoding.TextUnmarshaler); ok {
			return u.UnmarshalText, true
		}
	}
	// It marshals but cannot unmarshal; nothing can be done with the bytes.
	return func([]byte) error { return nil }, true
}

// value decodes the next value into the addressable v.
func (d *tlvDec) value(v reflect.Value) error {
	tag, err := d.byte()
	if err != nil {
		return err
	}
	if tag == tlvNil {
		v.Set(reflect.Zero(v.Type()))
		return nil
	}
	if tag == tlvEmpty {
		if v.Kind() != reflect.Slice {
			return d.mismatch(tag, v.Type())
		}
		v.Set(reflect.MakeSlice(v.Type(), 0, 0))
		return nil
	}
	if v.Kind() == reflect.Ptr {
		d.pos-- // the pointee has the tag
		p := reflect.New(v.Type().Elem())
		if err := d.value(p.Elem()); err != nil {
			return err
		}
		v.Set(p)
		return nil
	}
	if tag == tlvBlob {
		b, err := d.bytes()
		if err != nil {
			return err
		}
		if u, ok := unmarshaller(v); ok {
			// The decoder may keep the slice; it must not alias the file.
			return u(append([]byte(nil), b...))
		}
		return d.mismatch(tag, v.Type())
	}
	switch v.Kind() {
	case reflect.Bool:
		if tag != tlvBool {
			return d.mismatch(tag, v.Type())
		}
		b, err := d.byte()
		if err != nil {
			return err
		}
		v.SetBool(b != 0)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var x int64
		switch tag {
		case tlvInt:
			if x, err = d.varint(); err != nil {
				return err
			}
		case tlvUint:
			u, err := d.uvarint()
			if err != nil {
				return err
			}
			x = int64(u)
		default:
			return d.mismatch(tag, v.Type())
		}
		if v.OverflowInt(x) {
			return fmt.Errorf("binenc: %d overflows a %s", x, v.Type())
		}
		v.SetInt(x)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		var x uint64
		switch tag {
		case tlvUint:
			if x, err = d.uvarint(); err != nil {
				return err
			}
		case tlvInt:
			i, err := d.varint()
			if err != nil {
				return err
			}
			if i < 0 {
				return fmt.Errorf("binenc: %d does not fit a %s", i, v.Type())
			}
			x = uint64(i)
		default:
			return d.mismatch(tag, v.Type())
		}
		if v.OverflowUint(x) {
			return fmt.Errorf("binenc: %d overflows a %s", x, v.Type())
		}
		v.SetUint(x)
	case reflect.Float32, reflect.Float64:
		switch tag {
		case tlvF32:
			b, err := d.take(4)
			if err != nil {
				return err
			}
			v.SetFloat(float64(math.Float32frombits(binary.LittleEndian.Uint32(b))))
		case tlvF64:
			b, err := d.take(8)
			if err != nil {
				return err
			}
			v.SetFloat(math.Float64frombits(binary.LittleEndian.Uint64(b)))
		default:
			return d.mismatch(tag, v.Type())
		}
	case reflect.String:
		if tag != tlvString {
			return d.mismatch(tag, v.Type())
		}
		b, err := d.bytes()
		if err != nil {
			return err
		}
		v.SetString(string(b))
	case reflect.Slice, reflect.Array:
		return d.list(tag, v)
	case reflect.Map:
		return d.mapValue(tag, v)
	case reflect.Struct:
		return d.structValue(tag, v)
	default:
		return d.mismatch(tag, v.Type())
	}
	return nil
}

func (d *tlvDec) list(tag byte, v reflect.Value) error {
	et := v.Type().Elem()
	switch tag {
	case tlvString:
		if et.Kind() != reflect.Uint8 {
			return d.mismatch(tag, v.Type())
		}
		b, err := d.bytes()
		if err != nil {
			return err
		}
		if v.Kind() == reflect.Slice {
			if len(b) > 0 {
				v.SetBytes(append([]byte(nil), b...))
			}
			return nil
		}
		if len(b) != v.Len() {
			return d.mismatch(tag, v.Type())
		}
		reflect.Copy(v, reflect.ValueOf(b))
		return nil

	case tlvPacked:
		n, err := d.count()
		if err != nil {
			return err
		}
		fs, err := d.uvarint()
		if err != nil {
			return err
		}
		if int(fs) != floatShape(et) {
			return d.mismatch(tag, v.Type())
		}
		raw, err := d.take(uint64(n) * fs * 4)
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		var p unsafe.Pointer
		if v.Kind() == reflect.Slice {
			s := reflect.MakeSlice(v.Type(), n, n)
			v.Set(s)
			p = s.UnsafePointer()
		} else {
			if n != v.Len() {
				return d.mismatch(tag, v.Type())
			}
			p = v.Addr().UnsafePointer()
		}
		dst := unsafe.Slice((*float32)(p), n*int(fs))
		for i := range dst {
			dst[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
		}
		return nil

	case tlvList:
		n, err := d.count()
		if err != nil {
			return err
		}
		if v.Kind() == reflect.Slice {
			if n == 0 {
				return nil
			}
			v.Set(reflect.MakeSlice(v.Type(), n, n))
		} else if n != v.Len() {
			return d.mismatch(tag, v.Type())
		}
		for i := 0; i < n; i++ {
			if err := d.value(v.Index(i)); err != nil {
				return err
			}
		}
		return nil
	}
	return d.mismatch(tag, v.Type())
}

func (d *tlvDec) mapValue(tag byte, v reflect.Value) error {
	if tag != tlvMap {
		return d.mismatch(tag, v.Type())
	}
	n, err := d.count()
	if err != nil {
		return err
	}
	t := v.Type()
	m := reflect.MakeMapWithSize(t, n)
	for i := 0; i < n; i++ {
		k := reflect.New(t.Key()).Elem()
		if err := d.value(k); err != nil {
			return err
		}
		e := reflect.New(t.Elem()).Elem()
		if err := d.value(e); err != nil {
			return err
		}
		m.SetMapIndex(k, e)
	}
	v.Set(m)
	return nil
}

func (d *tlvDec) structValue(tag byte, v reflect.Value) error {
	if tag != tlvStruct {
		return d.mismatch(tag, v.Type())
	}
	t := v.Type()
	byName := d.fields[t]
	if byName == nil {
		byName = map[string]int{}
		for i := 0; i < t.NumField(); i++ {
			if t.Field(i).IsExported() {
				byName[t.Field(i).Name] = i
			}
		}
		d.fields[t] = byName
	}
	n, err := d.count()
	if err != nil {
		return err
	}
	for i := 0; i < n; i++ {
		ni, err := d.uvarint()
		if err != nil {
			return err
		}
		if ni >= uint64(len(d.names)) {
			return errTLVCorrupt
		}
		fi, ok := byName[d.names[ni]]
		if !ok {
			// A field this build no longer has: skipped, as gob skips one.
			if err := d.skip(); err != nil {
				return err
			}
			continue
		}
		if err := d.value(v.Field(fi)); err != nil {
			return fmt.Errorf("%s.%s: %w", t.Name(), d.names[ni], err)
		}
	}
	return nil
}

// skip steps over one value without decoding it.
func (d *tlvDec) skip() error {
	tag, err := d.byte()
	if err != nil {
		return err
	}
	switch tag {
	case tlvNil, tlvEmpty:
	case tlvBool:
		_, err = d.take(1)
	case tlvInt:
		_, err = d.varint()
	case tlvUint:
		_, err = d.uvarint()
	case tlvF32:
		_, err = d.take(4)
	case tlvF64:
		_, err = d.take(8)
	case tlvString, tlvBlob:
		_, err = d.bytes()
	case tlvPacked:
		var n, fs uint64
		if n, err = d.uvarint(); err == nil {
			if fs, err = d.uvarint(); err == nil {
				if fs != 0 && n > uint64(len(d.data))/fs {
					return errTLVCorrupt
				}
				_, err = d.take(n * fs * 4)
			}
		}
	case tlvList:
		var n int
		if n, err = d.count(); err == nil {
			for i := 0; i < n && err == nil; i++ {
				err = d.skip()
			}
		}
	case tlvMap:
		var n int
		if n, err = d.count(); err == nil {
			for i := 0; i < 2*n && err == nil; i++ {
				err = d.skip()
			}
		}
	case tlvStruct:
		var n int
		if n, err = d.count(); err == nil {
			for i := 0; i < n && err == nil; i++ {
				if _, err = d.uvarint(); err == nil {
					err = d.skip()
				}
			}
		}
	default:
		return errTLVCorrupt
	}
	return err
}
