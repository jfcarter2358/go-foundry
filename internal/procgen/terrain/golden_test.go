package terrain

// golden_test.go — Generate's output pinned to the byte.
//
// The phase 2 split of this package into per-region PROVIDERS and SHARED
// passes (union.go, docs/regions/PHASE_2.md) must leave the classic one-square
// generation exactly as it was: every existing terrain, desert canyon and
// level made from them depends on it. So before the split, Generate was run
// on a few small settings and a hash of everything it returns — heights,
// every mask, the strata index, the albedo hint, the deposit and the carved
// paths — was recorded here. A change that moves any of those by one bit
// fails this test; if a change is MEANT to alter the classic output, it is a
// different change from the regions work and must say so.
//
// FOUNDRY_TERRAIN_GOLDEN_PRINT=1 prints the current hashes (to re-pin after
// a deliberate change).

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"os"
	"testing"
)

// ResultHash is a hash of everything a Result holds that the outputs are
// made from. Exported for the stages package's goldens and the union tests.
func ResultHash(res *Result) string {
	h := sha256.New()
	var b [8]byte
	f32 := func(v []float32) {
		binary.LittleEndian.PutUint64(b[:], uint64(len(v)))
		h.Write(b[:])
		for _, x := range v {
			binary.LittleEndian.PutUint32(b[:4], math.Float32bits(x))
			h.Write(b[:4])
		}
	}
	f32(res.Grid.H)
	binary.LittleEndian.PutUint64(b[:], math.Float64bits(res.Grid.MinX))
	h.Write(b[:])
	binary.LittleEndian.PutUint64(b[:], math.Float64bits(res.Grid.MinZ))
	h.Write(b[:])
	f32(res.Deposit)
	for _, name := range MaskNames {
		h.Write([]byte(name))
		f32(res.Masks[name])
	}
	h.Write(res.StrataIndex)
	h.Write(res.Albedo)
	for _, p := range res.Paths {
		for k := range p.Points {
			for _, v := range []float64{p.Points[k][0], p.Points[k][1], p.Width[k], p.Floor[k]} {
				binary.LittleEndian.PutUint64(b[:], math.Float64bits(v))
				h.Write(b[:])
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// goldenSettings are the pinned generations.
func goldenSettings() map[string]Settings {
	a := small()
	a.Cell = 1 // 256 m at 1 m: canyons, drainage, droplets, thermal, detail all on
	b := small()
	b.Seed = 77
	b.Strata.Dip = [2]float64{3, -2}
	b.Mesa.Joints = 0.4
	b.Canyons[0].Profile = ProfileU
	b.Canyons[0].Tributaries = 2
	return map[string]Settings{"small_1m": a, "small_2m_dip_joints": b}
}

var goldenHashes = map[string]string{
	"small_1m":            "ca64cee9aa093ca4faf944b9973f1bba27c54ec172975829cdd4757b2b7e6c21",
	"small_2m_dip_joints": "8092c0623a531737b653a44b57ec0d0cdd9baada3d06d4fe4112ca3bf3da0d81",
}

func TestGenerateGolden(t *testing.T) {
	for name, s := range goldenSettings() {
		s := s
		res, err := Generate(&s, NewPool(context.Background(), 5), nil)
		if err != nil {
			t.Fatal(err)
		}
		got := ResultHash(res)
		if os.Getenv("FOUNDRY_TERRAIN_GOLDEN_PRINT") != "" {
			t.Logf("%q: %q,", name, got)
			continue
		}
		if want := goldenHashes[name]; got != want {
			t.Errorf("%s: Generate's output hash is %s, pinned %s — the classic terrain changed", name, got, want)
		}
	}
}
