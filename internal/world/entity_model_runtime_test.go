package world

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func TestUVScrollSlidesAndWrapsTheOffset(t *testing.T) {
	m := EntityModel{UVScroll: mgl32.Vec2{0.5, -0.25}}
	m.Update(3, "tank")
	want := mgl32.Vec2{0.5, 0.25}
	if d := m.UVOffset.Sub(want).Len(); d > 1e-5 {
		t.Errorf("after 3 s the offset is %v, want %v (1.5 and -0.75 wrapped into one tile)", m.UVOffset, want)
	}
	m.UVScroll = mgl32.Vec2{}
	m.Update(10, "tank")
	if d := m.UVOffset.Sub(want).Len(); d > 1e-5 {
		t.Errorf("a stopped scroll moved the offset to %v", m.UVOffset)
	}
}

func TestAnimPoseDueThinsWithDistanceAndSpreadsOverFrames(t *testing.T) {
	e := &Entity{Visible: true}
	count := func(pos mgl32.Vec3, index int) int {
		e.Position = pos
		n := 0
		for f := uint64(0); f < 64; f++ {
			if AnimPoseDue(e, index, f, mgl32.Vec3{}) {
				n++
			}
		}
		return n
	}
	for _, c := range []struct {
		d    float32
		want int
	}{{10, 64}, {100, 32}, {200, 16}, {400, 8}} {
		if got := count(mgl32.Vec3{c.d, 0, 0}, 0); got != c.want {
			t.Errorf("at %v m posed %d of 64 frames, want %d", c.d, got, c.want)
		}
	}
	e.Position = mgl32.Vec3{400, 0, 0}
	if AnimPoseDue(e, 0, 0, mgl32.Vec3{}) == AnimPoseDue(e, 1, 0, mgl32.Vec3{}) {
		t.Error("two far entities are posed on the same frame; the work should be spread")
	}
}
