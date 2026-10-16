package sf2

import (
	"encoding/binary"
	"math/rand"
	"testing"

	"foundry/internal/score/io/sfz"
)

// TestCorruptBanksDoNotPanic feeds the parser and the region builder
// thousands of damaged copies of a good bank — bytes flipped, the file cut
// short, chunk sizes and every index field set to nonsense — and asks only
// that each is either read or refused with an error. An import runs in the
// editor's process: a panic there would take the editor down.
func TestCorruptBanksDoNotPanic(t *testing.T) {
	good := testBank().bytes()
	r := rand.New(rand.NewSource(3))
	try := func(b []byte) {
		defer func() {
			if p := recover(); p != nil {
				t.Fatalf("panic on a damaged bank: %v", p)
			}
		}()
		f, err := Parse(b)
		if err != nil {
			return
		}
		for _, p := range f.Presets() {
			rs, _, err := f.Regions(p.Index)
			if err != nil {
				continue
			}
			_, _ = sfz.Build(&sfz.File{Regions: rs, SwLoKey: -1, SwHiKey: -1, SwDefault: -1}, sfz.Options{Name: "imported/x"})
		}
	}
	for i := 0; i < 4000; i++ {
		b := append([]byte(nil), good...)
		switch i % 4 {
		case 0: // random bytes
			for k := 0; k < 1+r.Intn(12); k++ {
				b[r.Intn(len(b))] = byte(r.Intn(256))
			}
		case 1: // cut short
			b = b[:r.Intn(len(b))]
		case 2: // a little-endian 16- or 32-bit field set to an extreme
			at := r.Intn(len(b) - 4)
			switch r.Intn(4) {
			case 0:
				binary.LittleEndian.PutUint32(b[at:], 0xFFFFFFFF)
			case 1:
				binary.LittleEndian.PutUint32(b[at:], uint32(r.Intn(1<<20)))
			case 2:
				binary.LittleEndian.PutUint16(b[at:], 0xFFFF)
			default:
				binary.LittleEndian.PutUint16(b[at:], uint16(r.Intn(64)))
			}
		default: // 16-bit words aligned on the hydra's records
			for k := 0; k < 1+r.Intn(6); k++ {
				at := (len(b)/2 - 1 - r.Intn(len(b)/8)) * 2
				binary.LittleEndian.PutUint16(b[at:], uint16(r.Intn(1<<16)))
			}
		}
		try(b)
	}
}

// A preset's notes count the modulators its own zones and instruments
// carry, not every one in the bank.
func TestModulatorsCountedPerPreset(t *testing.T) {
	f, err := Parse(testBank().bytes())
	if err != nil {
		t.Fatal(err)
	}
	// The pad (instrument 1, bags 4..6) carries three; the piano none.
	if len(f.ibags) != 7 {
		t.Fatalf("%d instrument bags", len(f.ibags))
	}
	for k := range f.ibags {
		f.ibags[k].mod = 0
	}
	f.ibags[5].mod, f.ibags[6].mod, f.imods = 2, 3, 3
	_, piano, err := f.Regions(0)
	if err != nil {
		t.Fatal(err)
	}
	_, pad, err := f.Regions(1)
	if err != nil {
		t.Fatal(err)
	}
	if piano["modulators"] != 0 || pad["modulators"] != 3 {
		t.Errorf("modulators: piano %d, pad %d (want 0, 3)", piano["modulators"], pad["modulators"])
	}
}
