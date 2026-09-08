package world

// spatial_boundary_test.go — standing on the floor must count as being in the
// chunk.
//
// A chunk's vertical bounds are the extent of its geometry, so its lowest floor
// *is* its MinBound.Y. A body resting on that floor therefore sits exactly on
// the boundary of every lookup's Y test, and whether it is found comes down to
// which side the last float rounded to. That is not a hypothetical: the resolver
// arrives at a resting position by adding a clamped centre and subtracting an
// offset, and the sign of the leftover is a property of the level's coordinates
// rather than of anything an author controls.
//
// What it breaks is quiet. CurrentChunk goes empty, so anything that reads a
// body's location misfires for anything standing still — which is most
// things, most of the time.

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// boundaryChunk is one 10×10 box brush with its floor at y=floorY and ceiling
// at y=ceilY.
func boundaryChunk(floorY, ceilY float32) *Chunk {
	half := (ceilY - floorY) / 2
	center := mgl32.Vec3{5, floorY + half, 5}
	ch := &Chunk{
		ID:      "room",
		Brushes: []Brush{NewBoxBrush("A", center, mgl32.Vec3{5, half, 5}, "")},
	}
	ch.RecomputeBounds()
	return ch
}

func TestAPointOnTheFloorIsInsideTheChunk(t *testing.T) {
	ch := boundaryChunk(0, 8)
	lvl := &Level{Chunks: []Chunk{*ch}}
	if err := RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	frame := lvl.Frames.World()

	// A hair either side of the floor, and a hair either side of the ceiling.
	// Everything within a rounding error of the surface has to be found.
	for _, dy := range []float32{1e-3, 1e-5, 1e-7, 1e-9, 0, -1e-9, -1e-7, -1e-5, -1e-4} {
		lo := mgl32.Vec3{5, lvl.Chunks[0].MinBound.Y() + dy, 5}
		hi := mgl32.Vec3{5, lvl.Chunks[0].MaxBound.Y() - dy, 5}

		if c := FindChunkSH(lvl.Spatial, lvl.Chunks, lo); c == nil {
			t.Errorf("floor%+.0e: the spatial hash found nothing", dy)
		}
		if c := FindChunk(lvl.Chunks, lo); c == nil {
			t.Errorf("floor%+.0e: the linear scan found nothing", dy)
		}
		if c := FindChunk(lvl.Chunks, lo); c == nil {
			t.Errorf("floor%+.0e: FindChunk found nothing", dy)
		}
		if c := frame.ChunkAt(lvl, lo); c == nil {
			t.Errorf("floor%+.0e: the frame search found nothing", dy)
		}
		if c := FindChunkSH(lvl.Spatial, lvl.Chunks, hi); c == nil {
			t.Errorf("ceiling%+.0e: found nothing", dy)
		}
	}
}

func TestTheFloorToleranceScalesWithTheCoordinate(t *testing.T) {
	// float32 has about a millimetre of resolution at ten thousand units, so a
	// fixed tolerance that works at the origin is below the noise floor out
	// there. A body resting on a floor at y=10000 has to be found too.
	ch := boundaryChunk(10000, 10008)
	lvl := &Level{Chunks: []Chunk{*ch}}
	if err := RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	base := lvl.Chunks[0].MinBound.Y()
	for _, ulps := range []int{1, 2, 4} {
		y := base
		for i := 0; i < ulps; i++ {
			y = nextDownFloat32(y)
		}
		if c := FindChunkSH(lvl.Spatial, lvl.Chunks, y3(y)); c == nil {
			t.Errorf("%d ulp below a floor at %v: found nothing", ulps, base)
		}
	}
}

func y3(y float32) mgl32.Vec3 { return mgl32.Vec3{5, y, 5} }

// nextDownFloat32 is the next representable float32 below v — one unit in the
// last place, which is the smallest error a rounding can produce.
func nextDownFloat32(v float32) float32 {
	if v == 0 {
		return -math.SmallestNonzeroFloat32
	}
	bits := math.Float32bits(v)
	if v > 0 {
		return math.Float32frombits(bits - 1)
	}
	return math.Float32frombits(bits + 1)
}

func TestStackedChunksStayDistinct(t *testing.T) {
	// The Y test exists to tell vertically stacked chunks apart, so widening it
	// must not start answering with the wrong floor. Half a unit apart is far
	// more than any tolerance and far less than a room.
	lower := boundaryChunk(0, 4)
	lower.ID = "lower"
	upper := boundaryChunk(4.5, 8.5)
	upper.ID = "upper"
	upper.Brushes[0].ID = "B"

	lvl := &Level{Chunks: []Chunk{*lower, *upper}}
	if err := RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		y    float32
		want string
	}{
		{0, "lower"}, {2, "lower"}, {3.9, "lower"},
		{4.6, "upper"}, {6, "upper"}, {8.5, "upper"},
	}
	for _, tc := range cases {
		c := FindChunkSH(lvl.Spatial, lvl.Chunks, mgl32.Vec3{5, tc.y, 5})
		if c == nil {
			t.Errorf("y=%v: found no chunk", tc.y)
			continue
		}
		if c.ID != tc.want {
			t.Errorf("y=%v: found %q, want %q", tc.y, c.ID, tc.want)
		}
	}
}
