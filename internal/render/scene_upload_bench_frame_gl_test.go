package render

import "foundry/internal/world"

// benchUploadFrameScene is one frame's scene upload, the way the frame makes
// it: every lit program takes the lights, the fog, the directional shadows and
// the point shadows, each through its own uniforms.
func benchUploadFrameScene(prog uint32, programs int, ls world.LightSet, fog *world.Fog, vols []world.Volume, frame ShadowFrame) {
	for p := 0; p < programs; p++ {
		world.UploadLightsTo(prog, ls, false)
		world.UploadFogTo(prog, fog, vols, nil)
		UploadShadowUniformsTo(prog, ls, frame)
		UploadPointShadowUniformsTo(prog, ls)
	}
}
