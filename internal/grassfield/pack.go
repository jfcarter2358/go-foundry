package grassfield

// pack.go — one instance in twelve bytes, and a tile's instances sorted into
// runs.
//
//	word 0   x (16 bits) | z (16 bits) << 16      over the tile box's X and Z
//	word 1   y (16 bits) | yaw (8) << 16 | scale (8) << 24
//	word 2   r | g << 8 | b << 16 | rank << 24     tint at 1.0 = 128
//	word 3   the up axis (orient.go)                only in an oriented field
//
// Positions quantise to Size/65535 — 2 mm across a 128 m tile — and the
// height to the tile's own height range. Yaw is 1.4° a step, scale 1/255 of
// the field's largest. The tint multiplies the kind's albedo, up to ~2×.
//
// The RANK is a hash of the instance's world XZ (Rank), so an instance
// keeps its rank whatever else is placed round it, and a rerun that places
// the same instance gives it the same rank. BuildField sorts each run by
// rank (ties by the full hash, then position, so the order is total and
// independent of the input order); the renderer draws a prefix of a run and
// fades each instance by its rank (falloff.go).

import (
	"fmt"
	"math"
	"sort"
)

// Instance is one instance, unpacked.
type Instance struct {
	// Kind is its kit kind.
	Kind int
	// Pos is its base, world metres.
	Pos [3]float32
	// Yaw is radians about +Y; Scale its uniform scale.
	Yaw, Scale float32
	// Tint multiplies the albedo (1: as scanned).
	Tint [3]float32
	// Rank is 0..255 (set by BuildField from Pos; read back by Unpack).
	Rank uint8
	// Up is the axis the clump stands on (orient.go), kept only by an
	// oriented field; zero (or +Y) is upright. Unpack fills it from an
	// oriented field and leaves it zero otherwise.
	Up [3]float32
}

// hash64 is a 64-bit mix (splitmix64's finaliser over the words).
func hash64(words ...uint64) uint64 {
	h := uint64(0x9E3779B97F4A7C15)
	for _, w := range words {
		h ^= w + 0x9E3779B97F4A7C15 + (h << 6) + (h >> 2)
		h ^= h >> 30
		h *= 0xBF58476D1CE4E5B9
		h ^= h >> 27
		h *= 0x94D049BB133111EB
		h ^= h >> 31
	}
	return h
}

// positionHash is the hash of a world XZ at millimetres: what ranks an
// instance.
func positionHash(x, z float32) uint64 {
	return hash64(uint64(int64(math.Round(float64(x)*1000))), uint64(int64(math.Round(float64(z)*1000))))
}

// Rank is the rank an instance at world (x, z) takes: 0..255, uniform.
func Rank(x, z float32) uint8 { return uint8(positionHash(x, z) >> 56) }

func quant(v, lo, size float32, max float64) uint32 {
	if size <= 0 {
		return 0
	}
	t := float64((v - lo) / size)
	return uint32(math.Round(math.Min(math.Max(t, 0), 1) * max))
}

// Pack packs in for field f's box and scale (Rank is taken as given): the
// first f.Words() words are the instance (the fourth, its up axis, only
// in an oriented field).
func (f *Field) Pack(in Instance) [OrientedInstanceWords]uint32 {
	x := quant(in.Pos[0], f.Origin[0], f.Size[0], 65535)
	y := quant(in.Pos[1], f.Origin[1], f.Size[1], 65535)
	z := quant(in.Pos[2], f.Origin[2], f.Size[2], 65535)
	yaw := math.Mod(float64(in.Yaw), 2*math.Pi)
	if yaw < 0 {
		yaw += 2 * math.Pi
	}
	yb := uint32(math.Round(yaw/(2*math.Pi)*256)) & 0xff
	sb := quant(in.Scale, 0, f.ScaleMax, 255)
	tint := func(v float32) uint32 {
		return uint32(math.Round(math.Min(math.Max(float64(v)*128, 0), 255)))
	}
	out := [OrientedInstanceWords]uint32{
		x | z<<16,
		y | yb<<16 | sb<<24,
		tint(in.Tint[0]) | tint(in.Tint[1])<<8 | tint(in.Tint[2])<<16 | uint32(in.Rank)<<24,
	}
	if f.Oriented {
		out[3] = PackUp(in.Up)
	}
	return out
}

// Unpack is instance i of the field (Kind is not stored per instance: -1;
// FieldInstances fills it from the runs).
func (f *Field) Unpack(i int) Instance {
	nw := f.Words()
	w := f.Data[i*nw : i*nw+nw]
	u16 := func(v uint32) float32 { return float32(v&0xffff) / 65535 }
	in := Instance{
		Kind: -1,
		Pos: [3]float32{f.Origin[0] + u16(w[0])*f.Size[0], f.Origin[1] + u16(w[1])*f.Size[1],
			f.Origin[2] + u16(w[0]>>16)*f.Size[2]},
		Yaw:   float32((w[1]>>16)&0xff) / 256 * 2 * math.Pi,
		Scale: float32(w[1]>>24) / 255 * f.ScaleMax,
		Tint:  [3]float32{float32(w[2]&0xff) / 128, float32((w[2]>>8)&0xff) / 128, float32((w[2]>>16)&0xff) / 128},
		Rank:  uint8(w[2] >> 24),
	}
	if f.Oriented {
		in.Up = UnpackUp(w[3])
	}
	return in
}

// Instances is every instance with its kind, in run order.
func (f *Field) Instances() []Instance {
	out := make([]Instance, 0, f.Count())
	for c := 0; c < f.CellsX*f.CellsZ; c++ {
		for k := 0; k < f.Kinds; k++ {
			r := f.Run(c, k)
			for i := r.Offset; i < r.Offset+r.Count; i++ {
				in := f.Unpack(int(i))
				in.Kind = k
				out = append(out, in)
			}
		}
	}
	return out
}

// FieldSpec is what BuildField lays instances out over.
type FieldSpec struct {
	Kit string
	// Min and Max are the tile's XZ rectangle (world metres).
	Min, Max [2]float32
	// Cell is the cells' side (metres; the last row and column may be
	// short).
	Cell  float32
	Kinds int
	// Oriented makes an oriented field (Field.Oriented): set when the kit
	// has an oriented kind. A field with an instance whose Up is set is
	// oriented regardless.
	Oriented bool
}

// BuildField packs instances (each Kind < spec.Kinds, inside the tile's
// rectangle) into a field: the box from their heights, the scale range from
// their scales, each one's rank from its world position, the runs by cell
// and kind, each run in rank order.
func BuildField(spec FieldSpec, ins []Instance) (*Field, error) {
	if spec.Cell <= 0 || spec.Max[0] <= spec.Min[0] || spec.Max[1] <= spec.Min[1] {
		return nil, fmt.Errorf("grass field: bad tile %v..%v cell %g", spec.Min, spec.Max, spec.Cell)
	}
	if spec.Kinds <= 0 || spec.Kinds > MaxKinds {
		return nil, fmt.Errorf("grass field: %d kinds", spec.Kinds)
	}
	f := &Field{Kit: spec.Kit, Cell: spec.Cell, Kinds: spec.Kinds, Oriented: spec.Oriented}
	for i := 0; i < len(ins) && !f.Oriented; i++ {
		f.Oriented = ins[i].Up != ([3]float32{})
	}
	f.CellsX = int(math.Ceil(float64((spec.Max[0] - spec.Min[0]) / spec.Cell)))
	f.CellsZ = int(math.Ceil(float64((spec.Max[1] - spec.Min[1]) / spec.Cell)))
	nc := f.CellsX * f.CellsZ
	lo, hi := float32(math.Inf(1)), float32(math.Inf(-1))
	smax := float32(0)
	for i := range ins {
		in := &ins[i]
		if in.Kind < 0 || in.Kind >= spec.Kinds {
			return nil, fmt.Errorf("grass field: instance %d has kind %d of %d", i, in.Kind, spec.Kinds)
		}
		lo, hi = min(lo, in.Pos[1]), max(hi, in.Pos[1])
		smax = max(smax, in.Scale)
	}
	if len(ins) == 0 {
		lo, hi = 0, 0
	}
	f.Origin = [3]float32{spec.Min[0], lo, spec.Min[1]}
	f.Size = [3]float32{spec.Max[0] - spec.Min[0], max(hi-lo, 0.01), spec.Max[1] - spec.Min[1]}
	f.ScaleMax = max(smax, 1e-3)
	type keyed struct {
		run  int
		hash uint64
		in   Instance
	}
	ks := make([]keyed, len(ins))
	f.CellY = make([][2]float32, nc)
	for c := range f.CellY {
		f.CellY[c] = [2]float32{float32(math.Inf(1)), float32(math.Inf(-1))}
	}
	for i, in := range ins {
		cx := min(max(int((in.Pos[0]-spec.Min[0])/spec.Cell), 0), f.CellsX-1)
		cz := min(max(int((in.Pos[2]-spec.Min[1])/spec.Cell), 0), f.CellsZ-1)
		c := cz*f.CellsX + cx
		h := positionHash(in.Pos[0], in.Pos[2])
		in.Rank = uint8(h >> 56)
		ks[i] = keyed{run: c*spec.Kinds + in.Kind, hash: h, in: in}
		cy := &f.CellY[c]
		cy[0], cy[1] = min(cy[0], in.Pos[1]), max(cy[1], in.Pos[1])
	}
	for c := range f.CellY {
		if f.CellY[c][0] > f.CellY[c][1] {
			f.CellY[c] = [2]float32{lo, lo}
		}
	}
	sort.Slice(ks, func(a, b int) bool {
		x, y := &ks[a], &ks[b]
		if x.run != y.run {
			return x.run < y.run
		}
		if x.hash != y.hash {
			return x.hash < y.hash
		}
		if x.in.Pos[0] != y.in.Pos[0] {
			return x.in.Pos[0] < y.in.Pos[0]
		}
		return x.in.Pos[2] < y.in.Pos[2]
	})
	f.Runs = make([]Run, nc*spec.Kinds)
	nw := f.Words()
	f.Data = make([]uint32, 0, len(ks)*nw)
	for i := range ks {
		r := &f.Runs[ks[i].run]
		if r.Count == 0 {
			r.Offset = uint32(i)
		}
		r.Count++
		w := f.Pack(ks[i].in)
		f.Data = append(f.Data, w[:nw]...)
	}
	// Empty runs point where their neighbours end, so an offset is always
	// in range.
	next := uint32(len(ks))
	for i := len(f.Runs) - 1; i >= 0; i-- {
		if f.Runs[i].Count == 0 {
			f.Runs[i].Offset = next
		} else {
			next = f.Runs[i].Offset
		}
	}
	return f, nil
}
