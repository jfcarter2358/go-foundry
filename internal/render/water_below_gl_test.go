package render

// water_below_gl_test.go — water seen from below its own height is never
// drawn black (fixes after W5, docs/terrain-paint/PHASE_W5.md): a river
// sloping up away from an eye standing in the air by its lower end (the
// river above a fall, seen from the plunge pool) is shaded as water seen
// from above, and the surface seen from truly under water shows its lit
// murk and the window, not black.
//
//	FOUNDRY_GL_TEST=1 go test -run WaterBelow ./internal/render/
//	FOUNDRY_TEST_DUMP=/tmp/dump ... writes the frames as PNGs

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// rampBrush is a box whose top rises by rise from its +Z end to its −Z end.
func rampBrush(b world.Brush, rise float32) world.Brush {
	for i := range b.Verts {
		if world.CornerSign(i, 1) > 0 && world.CornerSign(i, 2) < 0 {
			b.Verts[i][1] += rise
		}
	}
	return b
}

func TestWaterBelowTheSurfaceHeightIsNotBlack(t *testing.T) {
	w := newVisWorld(t)
	mat := w5Water(t)
	prevQ := gfx.WaterQuality
	t.Cleanup(func() { gfx.WaterQuality = prevQ })
	gfx.WaterQuality = gfx.WaterHigh
	// A sloping river 20 m long rising 4 m away from the eye, a metre of
	// water over its bed; the eye in the air 1 m over its lower end.
	bed := rampBrush(world.NewBoxBrush("bed", mgl32.Vec3{0, -2, 0}, mgl32.Vec3{6, 0.5, 10}, "concrete"), 4)
	water := rampBrush(world.NewBoxBrush("river", mgl32.Vec3{0, -0.75, 0}, mgl32.Vec3{6, 0.75, 10}, "water"), 4)
	water.Solid = false
	for fi := range water.Faces {
		f := &water.Faces[fi]
		if water.PlacedFaceNormal(f).Y() > 0.9 {
			f.Material = mat
		} else {
			f.NoDraw = true
		}
	}
	bare := w4Level(t, bed)
	lvl := w4Level(t, bed, water)
	world.InvalidateWaterQuery()
	size := int(w.size)
	skip := func(p string) bool { return p == "gi" }
	frame := func(l *world.Level) []uint8 {
		w.frameIntoWith(l, WorldDraw{Now: 1, Skip: skip})
		return w.frameIntoWith(l, WorldDraw{Now: 1, Skip: skip})
	}

	gfx.Cam.Position = mgl32.Vec3{0, 1, 12}
	gfx.Cam.Direction = mgl32.Vec3{0, 0.05, -1}.Normalize()
	gfx.UpdateCamMatrix()
	b := frame(bare)
	a := frame(lvl)
	if WaterUnderwater() {
		t.Fatalf("the eye 1 m over the river's lower end counted as under water")
	}
	dumpPNG("below_bare", b, size)
	dumpPNG("below_river", a, size)
	// The water's pixels (where the river changed the picture) up the
	// middle column: those above the eye's height (rows above the
	// horizon, the upper half of the frame) against those below it.
	var up, down float32
	nu, nd := 0, 0
	for y := 0; y < size; y++ {
		for x := size/2 - 8; x <= size/2+8; x += 4 {
			if pixelDiffAt(a, b, size, x, y) < 6 {
				continue
			}
			l := lum(waterPixel(a, size, x, y))
			if y > size/2+2 {
				up += l
				nu++
			} else if y < size/2-2 {
				down += l
				nd++
			}
		}
	}
	if nu < 6 || nd < 6 {
		t.Fatalf("the river covered %d pixels above the eye's height and %d below", nu, nd)
	}
	up, down = up/float32(nu), down/float32(nd)
	t.Logf("water above the eye's height %.3f, below %.3f", up, down)
	if up < 0.08 || up < 0.4*down {
		t.Errorf("the river above the eye's height is drawn dark (%.3f, the water nearer %.3f): seen as from under water", up, down)
	}

	// Truly under the water (in its lower end), looking up through the
	// surface: the window and the murk, not black.
	gfx.Cam.Position = mgl32.Vec3{0, -0.6, 8}
	gfx.Cam.Direction = mgl32.Vec3{0, 1, -0.5}.Normalize()
	gfx.UpdateCamMatrix()
	u := frame(lvl)
	if !WaterUnderwater() {
		t.Fatalf("0.6 m into the river did not count as under water")
	}
	dumpPNG("below_under_up", u, size)
	var sum float32
	n := 0
	for y := size / 4; y < size*3/4; y += 4 {
		for x := size / 4; x < size*3/4; x += 4 {
			sum += lum(waterPixel(u, size, x, y))
			n++
		}
	}
	if m := sum / float32(n); m < 0.05 {
		t.Errorf("the surface seen from under the water is near black (mean %.3f)", m)
	}
}
