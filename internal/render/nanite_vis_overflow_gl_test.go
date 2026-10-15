package render

// nanite_vis_overflow_gl_test.go — a frame that asks the visibility buffer
// for more records than an ID can name still draws everything
// (nanite_vis_overflow.go; docs/regions/PRESETS_4.md, bug 1).
//
// The limit is 2^20 commands; the biome showcase's overview asked for 1.19 M
// and the commands past it were dropped — white holes over its scans. Here a
// grid of sixteen cards is drawn with the record limit cut to three
// (naniteVisRecordLimit), so most of its commands overflow:
//
//   - without the overflow pass, the picture loses most of the cards (the
//     control: the test can see the holes);
//   - with it, the picture matches the one drawn with no limit at all.
//
//	FOUNDRY_GL_TEST=1 go test -timeout 600s -run NaniteVisOverflow ./internal/render/

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func TestNaniteVisOverflowDrawsWhatTheRecordsCannotName(t *testing.T) {
	w := newVisWorld(t)
	card := cutoutCard(t, "overflow_card", "brick", 0.9, 0.9, 0)

	// The state holds GL objects and fences: never save and restore a copy of
	// it (the copy's fences are deleted by the live state's first reads — the
	// dead fence a later test tripped over); release it, and leave a fresh
	// one to build lazily.
	prevLimit := naniteVisRecordLimit
	t.Cleanup(func() {
		naniteVisRecordLimit = prevLimit
		naniteVisOverflow.release()
	})

	prevDense := NaniteVisDense
	t.Cleanup(func() { NaniteVisDense = prevDense })
	lvl := &world.Level{}
	for i := 0; i < 16; i++ {
		x, y := float32(i%4)-1.5, float32(i/4)*1.0-1.0
		s := world.NewScenery("card", card, mgl32.Vec3{x, y, 0})
		if err := s.Load(); err != nil {
			t.Fatal(err)
		}
		lvl.Scenery = append(lvl.Scenery, s)
	}
	lvl.Lights.Global = world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.8,
		Wrap: world.DefaultLightWrap, Hemisphere: world.DefaultAmbientHemisphere}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	world.UploadLights(lvl.Lights)
	t.Cleanup(func() {
		world.DeleteScenery(lvl)
		naniteScenery.lvl = nil
		world.UploadLights(world.LightSet{})
	})
	draw := func() []uint8 {
		w.frame(lvl)
		return w.frame(lvl)
	}
	setCam := func() {
		NaniteVisBuffer, NaniteVisDense = true, true
		// Square on to the grid, close enough that no card is the compute
		// rasteriser's.
		gfx.Cam.Position, gfx.Cam.Direction = mgl32.Vec3{0, 0.5, 3.2}, mgl32.Vec3{0, 0, -1}
		gfx.UpdateCamMatrix()
		ForgetTAAHistory()
	}

	setCam()
	naniteVisRecordLimit = naniteVisMaxRecords
	full := draw()

	// The control: three records, nothing to catch the rest.
	naniteVisRecordLimit = 3
	naniteVisOverflow.release()
	naniteVisOverflow = visOverflowState{tried: true, ctx: currentGLContext()} // no overflow pass
	setCam()
	holes := draw()
	if over8, _, _ := pixelDiff(full, holes); over8 < 200 {
		t.Fatalf("with three records and no overflow pass only %d pixels changed: the test does not make the frame overflow", over8)
	}

	// The overflow pass, its risk raised as a readback over the limit does.
	naniteVisOverflow.release()
	if !naniteVisOverflow.ready() {
		t.Fatal("the overflow pass did not build")
	}
	naniteVisOverflow.take(1000)
	if !naniteVisOverflow.overflowRisk() {
		t.Fatal("a demand over the limit did not raise the risk")
	}
	setCam()
	caught := draw()
	dumpPNG("vis_overflow_full", full, int(w.size))
	dumpPNG("vis_overflow_holes", holes, int(w.size))
	dumpPNG("vis_overflow_caught", caught, int(w.size))
	over8, over32, worst := pixelDiff(full, caught)
	t.Logf("overflowed, caught: %d pixels over 8 levels, %d over 32, worst %d", over8, over32, worst)
	if over32 > int(w.size)*int(w.size)/200 {
		t.Errorf("the overflow drawn the forward way differs from the full frame in %d pixels (over 32 levels)", over32)
	}
}
