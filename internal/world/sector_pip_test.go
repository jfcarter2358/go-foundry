package world

// sector_pip_test.go — PointInSector is an allocation-free restatement of
// SectorPoints + geom.PointInPolygon, so the only thing worth testing about it
// is that it never disagrees with the pair it replaced. Every collision lookup
// and every biome floor sample goes through it.

import (
	"math/rand"
	"testing"

	"foundry/internal/geom"

	"github.com/go-gl/mathgl/mgl32"
)

func pipTestChunk() (*Chunk, *Sector) {
	ch := &Chunk{
		Scale: 1,
		// A deliberately non-convex (L-shaped) ring, so the ray-cast has
		// somewhere to disagree if the two implementations ever drift.
		Points: []mgl32.Vec2{
			{0, 0}, {10, 0}, {10, 4}, {4, 4}, {4, 10}, {0, 10},
		},
	}
	s := &Sector{PointIndices: []int{0, 1, 2, 3, 4, 5}}
	ch.Sectors = []Sector{*s}
	return ch, &ch.Sectors[0]
}

func TestPointInSectorMatchesPointInPolygon(t *testing.T) {
	ch, s := pipTestChunk()
	poly := SectorPoints(ch, s)

	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		// Spread beyond the polygon so outside, inside and the concave notch
		// are all covered.
		p := mgl32.Vec2{rng.Float32()*16 - 3, rng.Float32()*16 - 3}
		want := geom.PointInPolygon(p, poly)
		if got := PointInSector(ch, s, p); got != want {
			t.Fatalf("PointInSector%v = %v, geom.PointInPolygon = %v", p, got, want)
		}
	}
}

// An out-of-range point index reads as the zero vector in SectorPoints; the
// allocation-free form has to see the same thing or the two would disagree on
// a half-built sector.
func TestPointInSectorMatchesOnOutOfRangeIndices(t *testing.T) {
	ch, _ := pipTestChunk()
	s := &Sector{PointIndices: []int{0, 1, 99, -4, 4, 5}}
	poly := SectorPoints(ch, s)

	rng := rand.New(rand.NewSource(2))
	for i := 0; i < 5000; i++ {
		p := mgl32.Vec2{rng.Float32()*16 - 3, rng.Float32()*16 - 3}
		want := geom.PointInPolygon(p, poly)
		if got := PointInSector(ch, s, p); got != want {
			t.Fatalf("PointInSector%v = %v, geom.PointInPolygon = %v (out-of-range indices)", p, got, want)
		}
	}
}

func TestPointInSectorIsAllocationFree(t *testing.T) {
	ch, s := pipTestChunk()
	got := testing.AllocsPerRun(200, func() {
		PointInSector(ch, s, mgl32.Vec2{2, 2})
	})
	if got != 0 {
		t.Errorf("PointInSector allocated %v times per call, want 0 — the whole "+
			"point of it is that FindSector runs per texel of a biome height texture", got)
	}
}

func TestPointInSectorHandlesNils(t *testing.T) {
	ch, s := pipTestChunk()
	if PointInSector(nil, s, mgl32.Vec2{2, 2}) {
		t.Error("nil chunk should not contain anything")
	}
	if PointInSector(ch, nil, mgl32.Vec2{2, 2}) {
		t.Error("nil sector should not contain anything")
	}
}
