package world

// volume_fog.go — fog that fills a placed region.
//
// The level's `fog:` block (fog.go) is the air of the whole level: it has no
// position because there is nowhere it is not. A fog VOLUME is the other half
// of that idea — fog with an edge, so a cellar can be damp while the courtyard
// outside it is clear, a ravine can hold a bank the road above it does not, and
// a boss arena can fill with something the corridor into it did not have.
//
// It is a field on Volume rather than a fourth kind of placed object, for the
// same reason LoadLevel is: everything a region of fog needs — a shape, a
// rotation, half-extents, an ID, an `enabled` flag a script can flip, a
// wireframe cage to author it against, the gizmos to move and size it — a
// volume already has, and all a fog volume adds is what colour and how thick.
// A separate type would have been those six things copied so that the seventh
// could be new.
//
// One consequence worth stating plainly: a fog volume is still a volume, so it
// also fires `enter` and `exit`, and disabling it puts out the fog AND disarms
// the trigger. That is the intended reading rather than a leak — "the player is
// in the fog" is exactly the event a level wants when there is fog to be in.
//
// Nothing here decides how the fog LOOKS; resources/shaders/fog.glsl does, once,
// for every pass that shades. This file holds what the author set, keeps it in
// the range that shader assumes, and gets it there.

import (
	"fmt"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// MaxFogVolumes is how many the shader carries, and so how many of a level's
// fog volumes are drawn at once. Past it, the extras are dropped in level order
// — the same truncation MaxPointLights does, and for the same reason: a fixed
// array is what keeps the term a handful of uniforms instead of a buffer bind
// in five programs. Eight is far past the number of rooms visible at once.
//
// MUST match MAX_FOG_VOLUMES in resources/shaders/fog.glsl.
const MaxFogVolumes = 8

// VolumeFog is the fog filling a volume.
//
// There is no mode: the level fog offers linear/exp/exp2 because it is choosing
// a curve against DISTANCE, where "fade out by 400 units" and "haze that never
// finishes" are genuinely different intents. A volume's thickness is a physical
// depth through a body of fog, and the exponential extinction is what that
// actually is — linear fog inside a box would make the fog change with how the
// box happens to be sized rather than with how much of it you are looking
// through.
type VolumeFog struct {
	// Enabled switches the fog off while keeping the numbers, exactly as
	// Fog.Enabled and GlobalLight.Disabled do. Distinct from the volume's own
	// Enabled, which also disarms it as a trigger.
	Enabled bool
	Color   mgl32.Vec3
	// Density is the extinction per world unit travelled INSIDE the region, so
	// it means the same thing whatever size the volume is: 0.15 puts a 10-unit
	// crossing at about three-quarters fog and a 2-unit one at a quarter.
	Density float32
	// MaxOpacity caps the blend the way Fog.MaxOpacity does, so a volume can be
	// authored as a haze that never entirely hides what is standing in it.
	MaxOpacity float32
}

// VolumeFog defaults and limits.
const (
	VolumeFogDefaultDensity float32 = 0.15
	// VolumeFogMaxDensity is well above the level fog's ceiling of 1, because a
	// volume is authored at room scale rather than draw-distance scale: filling
	// a two-unit doorway needs a density a 400-unit view never would.
	VolumeFogMaxDensity float32 = 10
)

// DefaultVolumeFog is the fog a volume gets when an author first ticks the box:
// visible immediately at the sizes volumes are actually placed at, in the same
// pale blue-grey the level fog starts as.
func DefaultVolumeFog() VolumeFog {
	return VolumeFog{
		Enabled:    true,
		Color:      mgl32.Vec3{0.62, 0.68, 0.76},
		Density:    VolumeFogDefaultDensity,
		MaxOpacity: 1,
	}
}

// Clamp puts every field back inside the range fog.glsl assumes.
func (f *VolumeFog) Clamp() {
	if f == nil {
		return
	}
	f.Density = clampF(f.Density, 0, VolumeFogMaxDensity)
	f.MaxOpacity = clampF(f.MaxOpacity, 0, 1)
	for i := 0; i < 3; i++ {
		f.Color[i] = clampF(f.Color[i], 0, 1)
	}
}

// HasFog reports whether the volume puts any fog on screen.
//
// The volume's own Enabled is part of the test, not just the fog's: a disabled
// volume contains nothing, and a region that still fogged after a script
// switched it off would be a volume that was half there. A zero density or a
// zero opacity is fog that would draw nothing, and is dropped here rather than
// spending one of the eight slots.
func (v *Volume) HasFog() bool {
	return v != nil && v.Fog != nil && v.Fog.Enabled && v.Enabled && v.HasBounds() &&
		v.Fog.Density > 0 && v.Fog.MaxOpacity > 0
}

// FogVolumes returns the volumes whose fog should be drawn, in level order, at
// most MaxFogVolumes of them.
func FogVolumes(vols []Volume) []*Volume {
	var out []*Volume
	for i := range vols {
		if !vols[i].HasFog() {
			continue
		}
		out = append(out, &vols[i])
		if len(out) == MaxFogVolumes {
			break
		}
	}
	return out
}

// FogBasis is the matrix fog.glsl intersects the view ray in: it takes a
// world-space offset from the volume's centre, undoes the volume's rotation,
// and divides by the half-extents, so every shape becomes the unit box, unit
// sphere or unit cylinder and one intersection routine per shape covers all
// sizes and orientations.
//
// The rotation is orthonormal, so its inverse is its transpose — which is worth
// using rather than inverting, because a general inverse of a near-singular
// matrix is exactly the sort of thing that silently produces NaNs in a shader.
//
// The volume must have extents: this divides by all three. Every caller comes
// through FogVolumes, which only returns volumes that pass HasFog, and HasFog
// requires HasBounds.
func (v *Volume) FogBasis() mgl32.Mat3 {
	rot := EulerMatrixYXZ(v.Rotation).Mat3().Transpose()
	// Scale row i by 1/half_i. Mat3 is column-major, so row r of column c is
	// element c*3+r.
	inv := mgl32.Vec3{1 / v.HalfX, 1 / v.HalfY, 1 / v.HalfZ}
	var m mgl32.Mat3
	for c := 0; c < 3; c++ {
		for r := 0; r < 3; r++ {
			m[c*3+r] = rot[c*3+r] * inv[r]
		}
	}
	return m
}

// UploadFogVolumesTo sends the fog volume uniforms to prog, which must already
// be bound — it is called from UploadFogTo, immediately after that binds.
//
// numFogVolumes is written even when there are none, for the reason UploadFogTo
// writes mode 0: the program may still be holding the previous level's volumes,
// and a zero count is what makes the shader's loop cost nothing.
func UploadFogVolumesTo(prog uint32, vols []Volume) {
	if prog == 0 {
		return
	}
	active := FogVolumes(vols)
	gl.Uniform1i(ProgUniformLoc(prog, "numFogVolumes\x00"), int32(len(active)))

	for i, v := range active {
		f := v.Fog
		// Each Sprintf result is kept in a named variable so the string's
		// backing array stays alive for the duration of the gl call, the same
		// way UploadLightsTo does it.
		basis := v.FogBasis()
		basisName := fmt.Sprintf("fogVolBasis[%d]\x00", i)
		gl.UniformMatrix3fv(ProgUniformLoc(prog, basisName), 1, false, &basis[0])

		centerName := fmt.Sprintf("fogVolCenter[%d]\x00", i)
		gl.Uniform3f(ProgUniformLoc(prog, centerName),
			v.Position.X(), v.Position.Y(), v.Position.Z())

		shapeName := fmt.Sprintf("fogVolShape[%d]\x00", i)
		gl.Uniform1i(ProgUniformLoc(prog, shapeName), int32(v.Shape))

		colorName := fmt.Sprintf("fogVolColor[%d]\x00", i)
		gl.Uniform3f(ProgUniformLoc(prog, colorName),
			f.Color.X(), f.Color.Y(), f.Color.Z())

		densityName := fmt.Sprintf("fogVolDensity[%d]\x00", i)
		gl.Uniform1f(ProgUniformLoc(prog, densityName), f.Density)

		maxOpName := fmt.Sprintf("fogVolMaxOpacity[%d]\x00", i)
		gl.Uniform1f(ProgUniformLoc(prog, maxOpName), f.MaxOpacity)
	}
}

// ---------- YAML ----------

// VolumeFogFromRaw reads the optional per-volume fog block. A nil in is a
// volume that is only a trigger and stays one, rather than becoming a
// switched-off fog that the next save would then write back out.
func VolumeFogFromRaw(r *RawVolumeFog) *VolumeFog {
	if r == nil {
		return nil
	}
	f := DefaultVolumeFog()
	f.Enabled = !r.Disabled
	if r.Color != nil {
		f.Color = mgl32.Vec3{r.Color.R, r.Color.G, r.Color.B}
	}
	if r.Density != nil {
		f.Density = *r.Density
	}
	if r.MaxOpacity != nil {
		f.MaxOpacity = *r.MaxOpacity
	}
	f.Clamp()
	return &f
}

// VolumeFogToRaw is the inverse. Every key is written once there is a fog at
// all, for the reason FogToRaw writes all of its: a fog that omitted the
// density it was tuned with would come back as a different fog.
func VolumeFogToRaw(f *VolumeFog) *RawVolumeFog {
	if f == nil {
		return nil
	}
	c := *f
	c.Clamp()
	density, maxOp := c.Density, c.MaxOpacity
	return &RawVolumeFog{
		Disabled:   !c.Enabled,
		Color:      &RawColor{R: c.Color.X(), G: c.Color.Y(), B: c.Color.Z()},
		Density:    &density,
		MaxOpacity: &maxOp,
	}
}
