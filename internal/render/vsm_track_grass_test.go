package render

// vsm_track_grass_test.go — the virtual shadow map's tracker and the grass
// instancer's casting box (grass_field_shadow.go, vsm_track.go;
// docs/regions/PRESETS_4.md, bug 2b).
//
// Grass casts only within the shadow reach ROUND THE CAMERA, so its box
// moves with the camera. The tracker used to hand in only this frame's box:
// the pages under where the camera had been kept the clumps cast from there
// — dark blobs over the ground where no grass casts any more (from 40 m up,
// after a walk or a viewpoint jump). These pin that last frame's box is
// handed in once the box moves or goes, and that a field of kinds that do
// not cast hands in no box at all.

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/grassfield"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// withGrassCasting stands up grassFields with one visible field over
// 0..100 on XZ whose kit casts as asked, the shadow reach at 14 m, and puts
// it all back after.
func withGrassCasting(t *testing.T, casts bool) {
	t.Helper()
	prev, prevDist, prevCam := grassFields, gfx.GrassShadowDistance, gfx.Cam.Position
	t.Cleanup(func() {
		grassFields, gfx.GrassShadowDistance, gfx.Cam.Position = prev, prevDist, prevCam
	})
	grassFields = grassFieldRenderer{initialized: true}
	gfx.GrassShadowDistance = 14
	kit := &grassKitGPU{groups: []grassGroup{{casts: casts}}}
	grassFields.visible = []*grassFieldGPU{{
		field: &grassfield.Field{Origin: [3]float32{0, 0, 0}, Size: [3]float32{100, 2, 100}},
		kit:   kit,
		top:   1,
	}}
}

// boxIn reports whether boxes holds a mover box equal to b.
func boxIn(boxes []vsmBox, b vsmBox) bool {
	for _, x := range boxes {
		if x == b {
			return true
		}
	}
	return false
}

func TestVSMTrackerHandsInTheGrassBoxTheCameraLeft(t *testing.T) {
	withGrassCasting(t, true)
	lvl := &world.Level{}
	var tr vsmTracker

	gfx.Cam.Position = mgl32.Vec3{20, 1.7, 20}
	tr.collect(lvl, gfx.Cam.Position, 100)
	lo, hi, ok := grassShadowBox()
	if !ok {
		t.Fatal("no grass box with a casting field in view")
	}
	first := mover(lo, hi)

	// Still camera: the box is handed in (the grass sways), once.
	_, boxes := tr.collect(lvl, gfx.Cam.Position, 100)
	if !boxIn(boxes, first) {
		t.Fatal("this frame's grass box was not handed in")
	}

	// The camera moves on: where it was must be handed in as well as where
	// it is, or those pages keep the old clumps' shadows.
	gfx.Cam.Position = mgl32.Vec3{60, 1.7, 60}
	_, boxes = tr.collect(lvl, gfx.Cam.Position, 100)
	lo, hi, _ = grassShadowBox()
	second := mover(lo, hi)
	if !boxIn(boxes, second) {
		t.Error("the new grass box was not handed in")
	}
	if !boxIn(boxes, first) {
		t.Error("the grass box the camera left was not handed in: its pages keep stale clump shadows")
	}

	// Grass goes out of reach altogether: the last box still goes in, once.
	grassFields.visible = nil
	_, boxes = tr.collect(lvl, gfx.Cam.Position, 100)
	if !boxIn(boxes, second) {
		t.Error("the last grass box was not handed in when the grass stopped casting")
	}
	_, boxes = tr.collect(lvl, gfx.Cam.Position, 100)
	if boxIn(boxes, second) {
		t.Error("a grass box that is long gone is still handed in every frame")
	}
}

func TestGrassThatDoesNotCastHasNoShadowBox(t *testing.T) {
	withGrassCasting(t, false)
	gfx.Cam.Position = mgl32.Vec3{20, 1.7, 20}
	if _, _, ok := grassShadowBox(); ok {
		t.Error("a field of non-casting kinds gave the shadow map a box to redraw")
	}
}
