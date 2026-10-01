package render

// material_preview.go — standing the world program up for a preview window.
//
// The material editor shows a surface shaded exactly as the game shades it,
// and it does that by drawing through a SECOND copy of the world program
// (gfx.CompileWorldProgram) rather than through gfx.Program itself. Programs
// are shared between the editor's GL contexts, so the level window's program
// would draw the preview perfectly well — and then the preview's uniforms would
// be sitting in it: a light rig, a camera, a fog that the level window sets
// again next frame but not before its own passes have started. A copy costs one
// program object and removes the whole question.
//
// What the copy needs is everything fragment.glsl reads that is NOT per-draw:
// the lights, the shadow samplers, the fog and the clouds. The samplers are the
// part that is easy to skip and fatal to skip — a sampler2DArrayShadow or a
// samplerCube left unassigned defaults to texture unit 0, which holds the
// albedo sampler2D, and a type collision on one unit makes every draw through
// the program GL_INVALID_OPERATION. Nothing rasterises and nothing says why.
// UploadShadowUniformsTo and UploadPointShadowUniformsTo bind them all,
// including the unused ones, which is exactly why they are called here with an
// empty light set rather than skipped.

import (
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// previewSkinUniforms caches the preview program's skinning locations. One
// cache per program, as the note on gfx.SkinUniforms says — the same name
// lands at a different location in each.
var previewSkinUniforms gfx.SkinUniforms

// PreviewLights is the rig a preview window shades with: a key light over the
// viewer's shoulder, a fill from the other side to keep the dark side readable,
// and a generous ambient. Not a level's lighting — a lookdev rig, which is what
// judging a material wants.
//
// It is built fresh each call rather than kept, because DirectionalLight
// carries derived state (SyncDirection) and a shared copy would be one more
// thing to keep in step.
func PreviewLights() world.LightSet {
	key := world.DirectionalLight{
		Color: mgl32.Vec3{1, 0.97, 0.92}, Brightness: 1.1,
		Yaw: 135, Pitch: -40,
	}
	key.SyncDirection()
	fill := world.DirectionalLight{
		Color: mgl32.Vec3{0.72, 0.80, 0.95}, Brightness: 0.35,
		Yaw: -60, Pitch: -10,
	}
	fill.SyncDirection()
	return world.LightSet{
		Global: world.GlobalLight{
			Color: mgl32.Vec3{0.55, 0.58, 0.65}, Brightness: 0.45,
			Wrap: world.DefaultLightWrap, Hemisphere: world.DefaultAmbientHemisphere,
		},
		Directional: []world.DirectionalLight{key, fill},
	}
}

// PrepareWorldProgramForPreview uploads everything a preview needs into prog
// and leaves it bound: the camera and projection, the lookdev rig, the shadow
// samplers (all of them — see the file comment), no fog and no cloud shadow.
//
// Called once per frame from the preview window's Draw, before any geometry is
// drawn through prog. camPos is where the eye is; the water fresnel and the
// specular glint are measured against it, so a preview with a stale one has
// highlights that do not move.
func PrepareWorldProgramForPreview(prog uint32, view, proj mgl32.Mat4, camPos mgl32.Vec3, time float32) {
	if prog == 0 {
		return
	}
	gl.UseProgram(prog)
	loc := func(name string) int32 { return world.ProgUniformLoc(prog, name) }

	gl.UniformMatrix4fv(loc("projection\x00"), 1, false, &proj[0])
	gl.UniformMatrix4fv(loc("camera\x00"), 1, false, &view[0])
	gl.Uniform3f(loc("camPos\x00"), camPos.X(), camPos.Y(), camPos.Z())
	gl.Uniform1f(loc("time\x00"), time)

	lights := PreviewLights()
	world.UploadLightsTo(prog, lights, false)

	// The shadow uniforms with a frame that casts nothing. This is the call
	// that binds the cascade arrays and the eight cubemaps; without it the
	// program is unusable, not merely unshadowed.
	frame := ShadowFrame{Lights: make([]DirShadow, len(lights.Directional))}
	for i := range frame.Lights {
		frame.Lights[i].BaseLayer = -1
		for c := range frame.Lights[i].Cascades {
			frame.Lights[i].Cascades[c].Matrix = mgl32.Ident4()
		}
	}
	UploadShadowUniformsTo(prog, lights, frame)
	UploadPointShadowUniformsTo(prog, lights)
	uploadCloudShadowTo(prog)

	// No fog: a preview is a lookdev booth, and distance fog would wash the
	// far side of the sphere toward a colour the material has nothing to do
	// with. Passing a nil Fog is how world.UploadFogTo spells "off".
	world.UploadFogTo(prog, nil, nil, nil)

	// A preview surface is lit live and never baked, has no skin, and its
	// silhouette is judged on screen rather than fed to alpha-to-coverage.
	gl.Uniform1i(loc("bakedLight\x00"), 0)
	gl.Uniform1i(loc("bakedSunLight\x00"), -1)
	// Nor bounced: the sphere is in a booth, not in the level GI traced.
	disableGITo(prog)
	gl.UseProgram(prog)
	gl.Uniform1i(loc("alphaSharpen\x00"), 0)
	gfx.ApplySkinUniforms(&previewSkinUniforms, prog, nil)
}
