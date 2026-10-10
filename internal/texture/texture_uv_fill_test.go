package texture

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// A square island from texel 2 to texel 6 on a 16² canvas fills exactly those
// texels — interior and rim — leaves the rest alone, and undoes in one step.
func TestFillUVIslandsCoversIslandOnly(t *testing.T) {
	ed := newTestEditor(t, 16, 16)
	square := []mgl32.Vec2{{2.0 / 16, 2.0 / 16}, {6.0 / 16, 2.0 / 16}, {6.0 / 16, 6.0 / 16}, {2.0 / 16, 6.0 / 16}}
	if !ed.FillUVIslands([][]mgl32.Vec2{square}, 9) {
		t.Fatal("FillUVIslands reported no change")
	}
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			want := uint8(0)
			if x >= 2 && x <= 6 && y >= 2 && y <= 6 {
				want = 9
			}
			if got := ed.Doc.Layers[0].Idx[y*16+x]; got != want {
				t.Errorf("(%d,%d) = %d, want %d", x, y, got, want)
			}
		}
	}

	if ed.FillUVIslands([][]mgl32.Vec2{square}, 9) {
		t.Error("refilling with the same colour reported a change")
	}
	ed.Undo()
	for i, v := range ed.Doc.Layers[0].Idx {
		if v != 0 {
			t.Fatalf("texel %d = %d after one undo, want 0", i, v)
		}
	}
}

// A thin diagonal triangle has almost no texel centres inside it; its rim must
// still be painted so filtering along the seam samples the new colour.
func TestFillUVIslandsPaintsThinRim(t *testing.T) {
	ed := newTestEditor(t, 32, 32)
	tri := []mgl32.Vec2{{0.1, 0.1}, {0.9, 0.9}, {0.9, 0.91}}
	ed.FillUVIslands([][]mgl32.Vec2{tri}, 3)
	for _, p := range [][2]int{{3, 3}, {16, 16}, {28, 28}} {
		if got := ed.Doc.Layers[0].Idx[p[1]*32+p[0]]; got != 3 {
			t.Errorf("texel %v on the edge = %d, want 3", p, got)
		}
	}
	if got := ed.Doc.Layers[0].Idx[3*32+28]; got != 0 {
		t.Errorf("texel far from the triangle = %d, want 0", got)
	}
}
