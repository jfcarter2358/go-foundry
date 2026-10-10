package render

import (
	"testing"

	"foundry/internal/gfx"
)

// A frame's cached answers are the ones asked of the material directly, in a
// frame and out of one, and a new frame asks again.
func TestFrameSurfaceAnswersAsTheMaterialDoes(t *testing.T) {
	mats := []gfx.Material{
		{Texture: "stone", Alpha: 1},
		{Texture: "glass", Alpha: 0.4},
		{Texture: "stone", Alpha: 1, BlendTexture: "moss", BlendMask: "mask"},
		{Alpha: 1, Normals: true, Smooth: true},
	}
	check := func(when string) {
		for i := range mats {
			m := &mats[i]
			fs := frameSurface(m)
			if fs.st != m.SurfaceState() || fs.prepass != prepassable(m) || fs.transparent != m.IsTransparent() {
				t.Errorf("%s: material %d answered %+v (prepass %v, transparent %v)", when, i, fs.st, fs.prepass, fs.transparent)
			}
		}
	}
	check("outside a frame")
	drawFrameNo++
	drawFrameOpen = true
	defer func() { drawFrameOpen = false }()
	check("in a frame")
	check("asked again in the frame")
	// Changed between frames: the next frame sees it.
	mats[0].Alpha = 0.5
	drawFrameNo++
	check("the next frame")
}
