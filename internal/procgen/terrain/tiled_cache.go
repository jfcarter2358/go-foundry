package terrain

// tiled_cache.go — keeping tiled-erosion windows between runs
// (docs/regions/SPEC.md, "Incremental regeneration").
//
// Tiled erosion (tiled.go) erodes each window from nothing but its own
// inputs: the pre-erosion heights and deposits over its square, the material
// model there (which regions own each sample, their shares and strata
// offsets), the materials those regions compile to, the window's knobs and
// its fixed seed. So a window whose inputs are bit-identical to last run's
// erodes to the bit-identical result, and a caller that kept that result can
// hand it back instead of eroding again — the expensive half of a terrain
// run, after the cook.
//
// The KEY is a SHA-256 of exactly those inputs (windowKey): the square's
// place in the world and its size, the knobs, every sample's heights,
// deposits, owner byte, offset and band entries, and for each region present
// the provider's Key — a fingerprint of the settings its material compiles
// from, which the caller supplies (Provider.Key; region_terrain uses its
// preset terrain stage's hash, seed, anchor and measure). A provider with no
// Key makes the windows it reaches uncacheable. Content addressing means
// there is no notion of which edits "touch" a window: a far region's edit
// that does not change a sample of this square cannot change its key, and
// one that does cannot be missed — including the nearest-region fill that
// can carry a region's influence past its band (docs/regions/PHASE_2.md,
// "Gap fill reaches far").
//
// The cached VALUE is the eroded heights and deposits over the part of the
// square the cross-fade reads (fade > 0), and the window's droplet count, so
// a cached run's Result matches an eroding one's. The cache itself is the
// caller's (WindowCache): region_terrain stores each window as
// erosion/w_<i>_<j>.bin in its stage folder, the key in the header, so a
// file is self-verifying and a cancelled run can never leave one that lies.

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
)

// WindowCache keeps eroded windows between runs. Load returns the payload
// Stored under name if its key matches. Called from the generating
// goroutine, one window at a time.
type WindowCache interface {
	Load(name string, key [32]byte) ([]byte, bool)
	Store(name string, key [32]byte, payload []byte) error
}

// windowKeyVersion changes when the erosion's arithmetic or the payload's
// layout does, so every cached window misses.
const windowKeyVersion = "terrain-window-1"

// windowKey is the window's inputs' hash, and whether it can be cached (every
// provider present has a Key).
func windowKey(u *Union, wf *field, sx, sz, ss, wi, wj int) ([32]byte, bool) {
	m := wf.m
	present := map[int]bool{}
	for k, r := range m.reg {
		if r < mixed {
			present[int(r)] = true
			continue
		}
		for _, e := range m.entries(k) {
			present[int(e.r)] = true
		}
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%g|%g|%g|%d|%d|%d|%d|%d|%d|%d|%#v|%#v|%#v|%d|%v\n", windowKeyVersion, u.N, u.Cell, u.MinX, u.MinZ,
		sx, sz, ss, wi, wj, u.Margin, u.Window, wf.k.drainage, wf.k.droplets, wf.k.thermal, wf.k.seed, wf.k.edgeAware)
	for r := 0; r < len(u.Providers); r++ {
		if !present[r] {
			continue
		}
		if u.Providers[r].Key == "" {
			return [32]byte{}, false
		}
		fmt.Fprintf(h, "p%d=%s\n", r, u.Providers[r].Key)
	}
	var b [12]byte
	put := func(f float32) {
		binary.LittleEndian.PutUint32(b[:4], math.Float32bits(f))
		h.Write(b[:4])
	}
	for k := range m.reg {
		h.Write([]byte{m.reg[k]})
		put(m.off[k])
		if m.reg[k] >= mixed {
			for _, e := range m.entries(k) {
				h.Write([]byte{e.r})
				put(e.w)
				put(e.off)
			}
		}
	}
	for _, v := range wf.g.H {
		put(v)
	}
	for _, v := range wf.deposit {
		put(v)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, true
}

// fadeSpan is the sub-range [a, b) of a window square's ss samples along one
// axis (starting at union sample s0) where the window's fade is above zero:
// all the cross-fade reads.
func fadeSpan(s0, ss int, core [2]int, first, last bool, h float64) (int, int) {
	a, b := ss, 0
	for i := 0; i < ss; i++ {
		if fade(s0+i, core, first, last, h) > 0 {
			a = min(a, i)
			b = i + 1
		}
	}
	if b <= a {
		return 0, 0
	}
	return a, b
}

// encodeWindow is the cached value: droplets, the span, then the heights and
// deposits over it, row by row.
func encodeWindow(hs, dep []float32, ss, x0, x1, z0, z1, droplets int) []byte {
	n := (x1 - x0) * (z1 - z0)
	out := make([]byte, 24+8*n)
	binary.LittleEndian.PutUint64(out[0:], uint64(droplets))
	for k, v := range []int{x0, x1, z0, z1} {
		binary.LittleEndian.PutUint32(out[8+4*k:], uint32(v))
	}
	p := 24
	for _, src := range [][]float32{hs, dep} {
		for j := z0; j < z1; j++ {
			for i := x0; i < x1; i++ {
				binary.LittleEndian.PutUint32(out[p:], math.Float32bits(src[j*ss+i]))
				p += 4
			}
		}
	}
	return out
}

// decodeWindow fills the span of hs and dep from a cached value; false when
// it does not fit (a different span: treat as a miss).
func decodeWindow(data []byte, hs, dep []float32, ss, x0, x1, z0, z1 int) (droplets int, ok bool) {
	n := (x1 - x0) * (z1 - z0)
	if len(data) != 24+8*n {
		return 0, false
	}
	for k, v := range []int{x0, x1, z0, z1} {
		if int(binary.LittleEndian.Uint32(data[8+4*k:])) != v {
			return 0, false
		}
	}
	p := 24
	for _, dst := range [][]float32{hs, dep} {
		for j := z0; j < z1; j++ {
			for i := x0; i < x1; i++ {
				dst[j*ss+i] = math.Float32frombits(binary.LittleEndian.Uint32(data[p:]))
				p += 4
			}
		}
	}
	return int(binary.LittleEndian.Uint64(data[0:])), true
}
