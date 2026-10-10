package world

import (
	"slices"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// The borrowed answer is the copied one, from every kind of answer — a cell,
// the same cell again, another cell, the void, and with a chunk hidden — and
// moves the serial exactly when the copied one would.
func TestTheBorrowedVisibleListIsTheCopiedOne(t *testing.T) {
	a, b := twoCellTree(), twoCellTree()
	for i := range a.Surfaces {
		a.Surfaces[i].Chunk = []string{"x", "x", "y"}[i]
		b.Surfaces[i].Chunk = a.Surfaces[i].Chunk
	}
	eyes := []mgl32.Vec3{{1, 0, 0}, {1, 0, 0}, {-1, 0, 0}, {50, 0, 0}, {1, 0, 0}}
	check := func(when string) {
		for _, eye := range eyes {
			want := a.VisibleSurfaces(eye, nil)
			got := b.VisibleSurfacesView(eye)
			if !slices.Equal(got, want) {
				t.Errorf("%s, from %v: borrowed %v, copied %v", when, eye, got, want)
			}
			if a.VisibleSerial() != b.VisibleSerial() {
				t.Errorf("%s, from %v: serials %d and %d", when, eye, b.VisibleSerial(), a.VisibleSerial())
			}
		}
	}
	check("nothing hidden")
	a.SetChunkHidden("x", true)
	b.SetChunkHidden("x", true)
	check("a chunk hidden")
	a.SetChunkHidden("x", false)
	b.SetChunkHidden("x", false)
	check("shown again")
}
