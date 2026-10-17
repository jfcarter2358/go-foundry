package render

// water_caustics.go — where the water's caustics come from
// (docs/terrain-paint/PHASE_W1.md, "Caustics"; reworked in PHASE_W4.md).
//
// PHASE W1 drew the caustics from the surfaces UNDER the water: fragment.glsl
// multiplied the light on any fragment below a listed water body's surface
// and inside its box by water.glsl's waterCaustics, the bodies (up to eight,
// the nearest the camera) gathered here from the level — cooked placements
// wearing water and brush faces wearing it — each a box in X/Z and one
// surface height. That had two faults the later phases ran into:
//
//   - a body is a box under one level, so a DRY hollow below the water level
//     inside a pond tile's box took caustics as if it were under water;
//   - a generated river's ribbon SLOPES (phase W3): as one box under its
//     highest point it lit every dry bank below that height, so W3 dropped
//     rivers from the list and their beds had none.
//
// PHASE W4 moved the caustics onto the WATER: the water pass's surfaces draw
// them on the bed they show (water.glsl's waterBedCaustics, behind the
// waterHaveCaustics frame flag at the high tier). The bed's position comes
// back out of the depth copy along the view ray, so a caustic lands only
// where a water surface actually covers the ground — any pond, any river at
// any slope, and never a dry hollow beside it — faded by the depth under
// that very surface and its own absorption, carried down a river's current,
// and only where the sun reaches the surface. It needs no list, no box and
// no sampler, and it reaches every kind of bed (block terrain, biome ground,
// grass and scatter too, which the bed-side path never did).
//
// So the bed-side path is retired: the frame's body count is always zero.
// The uniforms stay declared in water.glsl, the test in fragment.glsl stays
// one uniform compare, and the visibility buffer's material variants fold
// it out entirely (nanite_vis_variants.go's visKeyNoCaustics reads the count
// here) — the shared files are untouched. What the bed-side path drew that
// the water's cannot: caustics seen from UNDER the surface (the camera
// below the water), which phase W5's underwater view brings back: with the
// camera under a surface the list holds that one body (prepareWaterCaustics,
// water_interact.go), and only then.

import (
	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/gfx"
	"foundry/internal/world"
)

// waterMaxCaustics is WATER_MAX_CAUSTICS in water.glsl.
const waterMaxCaustics = 8

// waterBodies is the bed-side caustics' frame list: empty above water since
// phase W4 (the file comment); under water (phase W5, water_interact.go)
// the one body the camera is in, so the bed and everything else below the
// surface around the camera is lit by its caustics from below.
var waterBodies struct {
	count int32
	box   [waterMaxCaustics * 4]float32
	info  [waterMaxCaustics * 4]float32
}

// waterUnderCausticReach is how far round the camera the under-water body's
// box runs, metres: past the murk any further caustic is fogged out.
const waterUnderCausticReach = 80

// prepareWaterCaustics fills the bed-side list for the frame: the camera's
// own body when it is under water (beginWaterFrame decided), else none.
func prepareWaterCaustics(_ *world.Level, eye mgl32.Vec3) {
	waterBodies.count = 0
	wi := &waterInteract
	if !wi.under || gfx.WaterQuality < gfx.WaterHigh {
		return
	}
	w := wi.sample.Water
	strength := w.Caustics
	if strength <= 0 {
		return
	}
	scale := w.DetailScale * 0.9
	if scale < 0.5 {
		scale = 0.5
	}
	r := float32(waterUnderCausticReach)
	waterBodies.box = [waterMaxCaustics * 4]float32{eye.X() - r, eye.Z() - r, eye.X() + r, eye.Z() + r}
	waterBodies.info = [waterMaxCaustics * 4]float32{wi.sample.Surface, strength, scale, max(w.Absorption, 0.05)}
	waterBodies.count = 1
}

// uploadWaterCausticsTo sends prog the bed-side list.
func uploadWaterCausticsTo(prog uint32) {
	gl.UseProgram(prog)
	gl.Uniform1i(gfx.UniformLoc(prog, "waterCausticCount\x00"), waterBodies.count)
	if waterBodies.count > 0 {
		if l := gfx.UniformLoc(prog, "waterCausticBox\x00"); l >= 0 {
			gl.Uniform4fv(l, waterBodies.count, &waterBodies.box[0])
		}
		if l := gfx.UniformLoc(prog, "waterCausticInfo\x00"); l >= 0 {
			gl.Uniform4fv(l, waterBodies.count, &waterBodies.info[0])
		}
	}
}
