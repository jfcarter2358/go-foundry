package render

// fog_volume_gl_test.go — the fog volume arithmetic in fog.glsl, on a GPU.
//
// A fog volume's whole claim is that its thickness is the LENGTH of the view
// ray inside the region, not a distance to its centre and not a flat fill of
// its silhouette. Everything that makes it read as a body of fog rather than as
// a sticker follows from that: looking along a bank is thicker than looking
// across it, walking in fills the screen, a wall past its far face is fogged by
// exactly its depth, and a ray that clips a sphere's corner is barely touched
// where one through the middle is not.
//
// None of that is checkable in Go — it lives entirely inside a .glsl file — and
// every one of those is the kind of thing that looks completely right in a
// screenshot of a box seen head-on, which is the shot anyone takes first. The
// rotated and off-axis cases are where a plausible-but-wrong implementation
// gives itself away.
//
// The probe is the one from fog_math_gl_test.go: a white surface at a world
// position given as a uniform, with the LEVEL fog switched off entirely, so the
// value read back is exactly the volume term's 1 - opacity.
//
//	FOUNDRY_GL_TEST=1 go test -run TestFogVolume ./internal/render/

import (
	"foundry/internal/gfx"
	"foundry/internal/world"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// blackFogVolume is a volume full of black fog, so the surviving surface
// brightness IS its transmittance and nothing else is in the way.
func blackFogVolume(shape world.VolumeShape, center, half mgl32.Vec3, density float32) world.Volume {
	v := world.NewVolume("probe", center)
	v.Shape = shape
	v.HalfX, v.HalfY, v.HalfZ = half.X(), half.Y(), half.Z()
	f := world.DefaultVolumeFog()
	f.Color = mgl32.Vec3{0, 0, 0}
	f.Density = density
	f.MaxOpacity = 1
	v.Fog = &f
	return v
}

// transmittance is what the shader should return for a span: the fraction of
// the surface that survives 1 - exp(-density * span).
func transmittance(density, span float32) float32 {
	return float32(math.Exp(float64(-density * span)))
}

// The headline claim. A box twice as deep fogs by twice the exponent at the
// same density — because the term is a path length, and the only thing that
// changed is how much of it there was.
func TestFogVolumeThicknessIsThePathThroughIt(t *testing.T) {
	p := fogProbeSetup(t)
	behind := mgl32.Vec3{0, 0, 100}
	const density = 0.25

	thin := blackFogVolume(world.VolumeBox, mgl32.Vec3{0, 0, 10}, mgl32.Vec3{5, 5, 2}, density)
	nearly(t, "4 units of fog",
		p.shadeThrough(nil, []world.Volume{thin}, behind, false), transmittance(density, 4))

	deep := blackFogVolume(world.VolumeBox, mgl32.Vec3{0, 0, 10}, mgl32.Vec3{5, 5, 4}, density)
	nearly(t, "8 units of fog",
		p.shadeThrough(nil, []world.Volume{deep}, behind, false), transmittance(density, 8))
}

// Where the segment starts and stops matters as much as the shape does: a
// surface in FRONT of the volume has no fog in front of it at all, one inside
// it has only the part it has already been carried through, and an eye inside
// it starts fogging from the eye. Get the clamp wrong in either direction and a
// fog bank tints the room you are standing in before it.
func TestFogVolumeOnlyFogsWhatIsBehindIt(t *testing.T) {
	p := fogProbeSetup(t)
	const density = 0.25
	// z from 8 to 12.
	vol := []world.Volume{blackFogVolume(world.VolumeBox, mgl32.Vec3{0, 0, 10}, mgl32.Vec3{5, 5, 2}, density)}

	nearly(t, "surface in front of the volume",
		p.shadeThrough(nil, vol, mgl32.Vec3{0, 0, 5}, false), 1)
	nearly(t, "surface halfway into the volume",
		p.shadeThrough(nil, vol, mgl32.Vec3{0, 0, 10}, false), transmittance(density, 2))
	nearly(t, "surface past the far face",
		p.shadeThrough(nil, vol, mgl32.Vec3{0, 0, 100}, false), transmittance(density, 4))

	// The eye inside the bank: the fog now starts at the eye, so only the half
	// of the volume still in front of it counts.
	gfx.Cam.Position = mgl32.Vec3{0, 0, 10}
	t.Cleanup(func() { gfx.Cam.Position = mgl32.Vec3{} })
	nearly(t, "standing in the middle of it",
		p.shadeThrough(nil, vol, mgl32.Vec3{0, 0, 100}, false), transmittance(density, 2))
}

// A volume is authored with a rotation, and the ray has to be carried into its
// space before the shape is solved. A slab that is thin on X and turned to face
// the camera must fog by its THICKNESS, not by the twenty units it is wide.
// Skip the inverse rotation and this case is off by a factor of twenty while
// the axis-aligned test above still passes.
func TestFogVolumeRespectsRotation(t *testing.T) {
	p := fogProbeSetup(t)
	behind := mgl32.Vec3{0, 0, 100}
	const density = 0.05

	// A slab 1 unit thick on X, 20 wide on Y and Z, standing 20 away.
	flat := blackFogVolume(world.VolumeBox, mgl32.Vec3{0, 0, 20}, mgl32.Vec3{0.5, 10, 10}, density)
	nearly(t, "looking along the slab",
		p.shadeThrough(nil, []world.Volume{flat}, behind, false), transmittance(density, 20))

	// Turned a quarter turn about Y, the thin axis now faces the camera.
	turned := flat
	turned.Rotation = mgl32.Vec3{0, float32(math.Pi / 2), 0}
	nearly(t, "looking through the turned slab",
		p.shadeThrough(nil, []world.Volume{turned}, behind, false), transmittance(density, 1))
}

// The three shapes have to be three shapes. This ray passes through the corner
// of the box the sphere is inscribed in and MISSES the sphere itself, so a
// sphere volume solved as its bounding box reads as fog where there is none —
// which is the failure that makes a spherical bank look like a crate.
func TestFogVolumeSphereIsNotItsBox(t *testing.T) {
	p := fogProbeSetup(t)
	const density = 0.2
	center, half := mgl32.Vec3{0, 0, 10}, mgl32.Vec3{5, 5, 5}
	corner := mgl32.Vec3{600, 600, 1000}

	box := p.shadeThrough(nil, []world.Volume{
		blackFogVolume(world.VolumeBox, center, half, density)}, corner, false)
	if box > 0.9 {
		t.Fatalf("the box read %.3f through its own corner; the ray was supposed to cross it", box)
	}

	nearly(t, "sphere through the corner the box fills",
		p.shadeThrough(nil, []world.Volume{
			blackFogVolume(world.VolumeSphere, center, half, density)}, corner, false), 1)
}

// The same argument for the cylinder, in both of the ways it differs from a
// box: round in XZ, and capped in Y. The cap is the half an infinite-cylinder
// implementation silently drops, and dropping it produces a column of fog
// running the height of the level from a volume the author placed at ankle
// height.
func TestFogVolumeCylinderIsRoundAndCapped(t *testing.T) {
	p := fogProbeSetup(t)
	const density = 1
	center, half := mgl32.Vec3{0, 0, 10}, mgl32.Vec3{5, 10, 5}
	corner := mgl32.Vec3{900, 0, 1000}

	box := p.shadeThrough(nil, []world.Volume{
		blackFogVolume(world.VolumeBox, center, half, density)}, corner, false)
	if box > 0.9 {
		t.Fatalf("the box read %.3f through its own corner; the ray was supposed to cross it", box)
	}
	nearly(t, "cylinder through the corner the box fills",
		p.shadeThrough(nil, []world.Volume{
			blackFogVolume(world.VolumeCylinder, center, half, density)}, corner, false), 1)

	// Sitting 20 above the eye line, 4 units tall: a ray along the ground must
	// pass under it entirely.
	overhead := blackFogVolume(world.VolumeCylinder,
		mgl32.Vec3{0, 20, 10}, mgl32.Vec3{5, 2, 5}, density)
	nearly(t, "under a cylinder that stops short",
		p.shadeThrough(nil, []world.Volume{overhead}, mgl32.Vec3{0, 0, 100}, false), 1)
}

// Two banks in a row are two banks: each composites over what is already there,
// so their transmittances multiply. Half and half leaves a quarter, not a half
// and not nothing.
func TestFogVolumesCompose(t *testing.T) {
	p := fogProbeSetup(t)
	// density * span = ln 2, so each volume alone leaves exactly half.
	const density = 0.17328680
	near := blackFogVolume(world.VolumeBox, mgl32.Vec3{0, 0, 10}, mgl32.Vec3{5, 5, 2}, density)
	far := blackFogVolume(world.VolumeBox, mgl32.Vec3{0, 0, 30}, mgl32.Vec3{5, 5, 2}, density)
	far.ID = "probe2"
	behind := mgl32.Vec3{0, 0, 100}

	nearly(t, "one bank", p.shadeThrough(nil, []world.Volume{near}, behind, false), 0.5)
	nearly(t, "two banks", p.shadeThrough(nil, []world.Volume{near, far}, behind, false), 0.25)
}

// MaxOpacity does for a volume what it does for the level fog: puts a floor
// under what survives, so a bank can be thick without erasing what is standing
// in it.
func TestFogVolumeMaxOpacityLeavesASilhouette(t *testing.T) {
	p := fogProbeSetup(t)
	v := blackFogVolume(world.VolumeBox, mgl32.Vec3{0, 0, 10}, mgl32.Vec3{5, 5, 5}, 5)
	v.Fog.MaxOpacity = 0.75
	nearly(t, "deep fog under a 0.75 cap",
		p.shadeThrough(nil, []world.Volume{v}, mgl32.Vec3{0, 0, 100}, false), 0.25)
}

// An additive surface draws over a framebuffer that already holds the volume,
// so the volume swallows it rather than tinting it — the same rule
// applyFogAdditive follows for the distance fog, and the same test that
// separates the two: against a WHITE fog the mixing answer goes to full white
// and the attenuating answer goes to nothing.
func TestFogVolumeSwallowsAdditiveSurfaces(t *testing.T) {
	p := fogProbeSetup(t)
	v := blackFogVolume(world.VolumeBox, mgl32.Vec3{0, 0, 10}, mgl32.Vec3{5, 5, 5}, 5)
	v.Fog.Color = mgl32.Vec3{1, 1, 1}
	vols := []world.Volume{v}
	pos := mgl32.Vec3{0, 0, 100}

	if got := p.shadeThrough(nil, vols, pos, false); got < 0.98 {
		t.Errorf("ordinary surface read %.3f inside a white bank; it should be the fog colour", got)
	}
	if got := p.shadeThrough(nil, vols, pos, true); got > 0.02 {
		t.Errorf("additive surface read %.3f; a spark deep in fog must be swallowed, not brightened", got)
	}
}

// Every way of switching a volume's fog off has to actually switch it off, and
// a volume with no fog at all — which is nearly all of them — must leave the
// frame exactly as it found it.
func TestFogVolumeOffContributesNothing(t *testing.T) {
	p := fogProbeSetup(t)
	behind := mgl32.Vec3{0, 0, 100}
	base := blackFogVolume(world.VolumeBox, mgl32.Vec3{0, 0, 10}, mgl32.Vec3{5, 5, 5}, 1)

	// Sanity: it does something while it is on.
	if got := p.shadeThrough(nil, []world.Volume{base}, behind, false); got > 0.5 {
		t.Fatalf("the reference volume read %.3f; it was supposed to be fogging", got)
	}

	plain := base
	plain.Fog = nil
	nearly(t, "a volume with no fog block",
		p.shadeThrough(nil, []world.Volume{plain}, behind, false), 1)

	fogOff := base
	f := *base.Fog
	f.Enabled = false
	fogOff.Fog = &f
	nearly(t, "fog switched off",
		p.shadeThrough(nil, []world.Volume{fogOff}, behind, false), 1)

	volOff := base
	volOff.Enabled = false
	nearly(t, "the volume itself disabled",
		p.shadeThrough(nil, []world.Volume{volOff}, behind, false), 1)

	nearly(t, "no volumes at all", p.shadeThrough(nil, nil, behind, false), 1)
}

// The two halves of the fog model have to survive each other: a level with fog
// banks and no distance fog must show the banks, and one with both must show
// both. The early-out for `fogMode == 0` sits between them, and putting it in
// the wrong place would make every fog volume in the project vanish from any
// level that had not also switched on distance fog.
func TestFogVolumeWorksWithoutDistanceFog(t *testing.T) {
	p := fogProbeSetup(t)
	const density = 0.25
	vols := []world.Volume{
		blackFogVolume(world.VolumeBox, mgl32.Vec3{0, 0, 10}, mgl32.Vec3{5, 5, 2}, density)}
	behind := mgl32.Vec3{0, 0, 100}

	nearly(t, "banks with no distance fog block",
		p.shadeThrough(nil, vols, behind, false), transmittance(density, 4))

	off := blackFog()
	off.Enabled = false
	nearly(t, "banks with the distance fog switched off",
		p.shadeThrough(&off, vols, behind, false), transmittance(density, 4))

	// With both, the distance fog goes over the top: at 100 units of a 0-to-100
	// linear ramp it is at full strength and takes the frame on its own.
	both := blackFog()
	both.Mode = world.FogLinear
	both.Start = 0
	both.End = 100
	nearly(t, "banks under full distance fog", p.shadeThrough(&both, vols, behind, false), 0)
}
