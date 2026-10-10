package world

// fog.go — the level's distance fog.
//
// Level-scoped, like Music and unlike anything placed: fog is a property of the
// air, so there is at most one, it has no ID and no position, and a level with
// none writes no `fog:` block at all — which is why Level.Fog is a pointer.
//
// Fog that DOES have a position is a fog volume, in volume_fog.go. The two are
// separate types because they answer separate questions, but they share one
// shader file and one upload — see UploadFogTo at the bottom.
//
// Nothing here decides how fog LOOKS; resources/shaders/fog.glsl does, once,
// and every program that shades world geometry compiles that same file (see its
// header). This file's whole job is to hold what the author set, keep it inside
// the range the shader assumes, and get it to whichever program is about to
// draw.

import (
	"foundry/internal/gfx"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// FogMode is how fog thickens with distance. The values are the fogMode uniform
// fog.glsl switches on and MUST match it; 0 is reserved there for "off", which
// on this side is Fog.Enabled being false (or Level.Fog being nil).
type FogMode int32

const (
	fogModeOff FogMode = 0
	// FogLinear ramps from nothing at Start to full at End. The one mode with a
	// distance you can point at, and the one to reach for when fog is being
	// used to hide a draw distance rather than to be weather.
	FogLinear FogMode = 1
	// FogExp is 1-exp(-density*d): no far limit, so nothing ever quite finishes
	// fading. Reads as haze.
	FogExp FogMode = 2
	// FogExp2 is the same with the exponent squared — clearer up close and
	// thickening faster once it starts. Reads as a fog bank.
	FogExp2 FogMode = 3
)

// FogModeName and FogModeFromName are the YAML spelling of a mode.
//
// A name rather than the integer, for the reason RawSound spells its bus out:
// the file has to keep meaning what it meant if the constants above are ever
// reordered.
func FogModeName(m FogMode) string {
	switch m {
	case FogExp:
		return "exp"
	case FogExp2:
		return "exp2"
	default:
		return "linear"
	}
}

// FogModeFromName reads a mode back, falling back to linear for anything it
// does not recognise — including the empty string an older or hand-written
// file leaves.
func FogModeFromName(s string) FogMode {
	switch s {
	case "exp":
		return FogExp
	case "exp2":
		return FogExp2
	default:
		return FogLinear
	}
}

// FogModeNames is every mode in the order the editor offers them.
var FogModeNames = []string{"linear", "exp", "exp2"}

// Fog is a level's distance fog.
type Fog struct {
	// Enabled switches the fog off without losing what it was set to, exactly
	// as GlobalLight.Disabled does — a level keeps its fog numbers while an
	// author looks at the level without them.
	Enabled bool
	Mode    FogMode
	Color   mgl32.Vec3
	// Start is where fog begins, in world units, in every mode.
	Start float32
	// End is where linear fog reaches full strength. Unused by the exponential
	// modes, and kept rather than cleared when one of them is selected.
	End float32
	// Density is the exponential modes' rate. Unused by linear, and kept for
	// the same reason.
	Density float32
	// MaxOpacity caps the blend. 1 lets distant geometry disappear completely;
	// lower leaves a silhouette showing through, which is what keeps a landmark
	// readable at the far end of heavy fog.
	MaxOpacity float32
	// SkyBlend is how much of Color the sky takes at the horizon, and
	// SkyFalloff how tightly that is confined to it. Without it the ground
	// fades to the fog while the sky behind it stays crisp, and the join
	// becomes the most visible edge on screen — at exactly the distance the fog
	// was added to hide. See sky_frag.glsl.
	SkyBlend   float32
	SkyFalloff float32
}

// Fog defaults, and the range each field is clamped to. FogDefaultEnd is well
// inside the default 1000-unit far plane (world.DefaultViewDistance), so the
// default fog finishes before the geometry does rather than after it.
const (
	FogDefaultStart      float32 = 40
	FogDefaultEnd        float32 = 400
	FogDefaultDensity    float32 = 0.006
	FogDefaultSkyBlend   float32 = 0.7
	FogDefaultSkyFalloff float32 = 4
	// FogMaxDistance bounds Start/End in the editor. Past the far plane fog is
	// a number with no pixels under it.
	FogMaxDistance float32 = 5000
)

// DefaultFog is a switched-on fog an author can see immediately: a pale
// blue-grey haze that thins the far half of the default view distance.
func DefaultFog() Fog {
	return Fog{
		Enabled:    true,
		Mode:       FogLinear,
		Color:      mgl32.Vec3{0.62, 0.68, 0.76},
		Start:      FogDefaultStart,
		End:        FogDefaultEnd,
		Density:    FogDefaultDensity,
		MaxOpacity: 1,
		SkyBlend:   FogDefaultSkyBlend,
		SkyFalloff: FogDefaultSkyFalloff,
	}
}

// Clamp puts every field back inside the range fog.glsl assumes.
//
// End is pushed past Start rather than Start pulled back, because an author
// dragging Start upward means "the clear air reaches further", and swapping the
// two under them would instead invert the fog.
func (f *Fog) Clamp() {
	if f == nil {
		return
	}
	if f.Mode < FogLinear || f.Mode > FogExp2 {
		f.Mode = FogLinear
	}
	f.Start = clampF(f.Start, 0, FogMaxDistance)
	f.End = clampF(f.End, 0, FogMaxDistance)
	if f.End <= f.Start {
		f.End = f.Start + 1
	}
	f.Density = clampF(f.Density, 0, 1)
	f.MaxOpacity = clampF(f.MaxOpacity, 0, 1)
	f.SkyBlend = clampF(f.SkyBlend, 0, 1)
	f.SkyFalloff = clampF(f.SkyFalloff, 0.01, 32)
	for i := 0; i < 3; i++ {
		f.Color[i] = clampF(f.Color[i], 0, 1)
	}
}

// EffectiveMode is the mode actually uploaded — the off value while the fog is
// switched off, so the shader's single `fogMode == 0` early-out covers both
// "this level has no fog" and "the author turned it off".
func (f *Fog) EffectiveMode() FogMode {
	if f == nil || !f.Enabled {
		return fogModeOff
	}
	return f.Mode
}

func clampF(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ---------- YAML ----------

// FogFromRaw reads the optional fog block. A nil in is a level with no fog and
// stays nil, rather than becoming a switched-off Fog that would then be written
// back out on the next save.
func FogFromRaw(r *RawFog) *Fog {
	if r == nil {
		return nil
	}
	f := DefaultFog()
	f.Enabled = !r.Disabled
	f.Mode = FogModeFromName(r.Mode)
	if r.Color != nil {
		f.Color = mgl32.Vec3{r.Color.R, r.Color.G, r.Color.B}
	}
	f.Start = r.Start
	if r.End != nil {
		f.End = *r.End
	}
	if r.Density != nil {
		f.Density = *r.Density
	}
	if r.MaxOpacity != nil {
		f.MaxOpacity = *r.MaxOpacity
	}
	if r.SkyBlend != nil {
		f.SkyBlend = *r.SkyBlend
	}
	if r.SkyFalloff != nil {
		f.SkyFalloff = *r.SkyFalloff
	}
	f.Clamp()
	return &f
}

// FogToRaw writes the block back. Every optional key is written unconditionally
// once there is a fog at all: these are not "absent means 1" defaults an author
// would recognise, and a fog file that omitted the density it was tuned with
// would come back as a different fog.
func FogToRaw(f *Fog) *RawFog {
	if f == nil {
		return nil
	}
	c := *f
	c.Clamp()
	end, density, maxOp := c.End, c.Density, c.MaxOpacity
	skyBlend, skyFalloff := c.SkyBlend, c.SkyFalloff
	return &RawFog{
		Mode:       FogModeName(c.Mode),
		Disabled:   !c.Enabled,
		Color:      &RawColor{R: c.Color.X(), G: c.Color.Y(), B: c.Color.Z()},
		Start:      c.Start,
		End:        &end,
		Density:    &density,
		MaxOpacity: &maxOp,
		SkyBlend:   &skyBlend,
		SkyFalloff: &skyFalloff,
	}
}

// ---------- GPU ----------

// UploadFog sends the fog uniforms to the main world shader.
func UploadFog(f *Fog, vols []Volume, lvl *Level) { UploadFogTo(gfx.Program, f, vols, lvl) }

// UploadFogTo hands the fog to the scene block prog reads (gfx/scene_block.go),
// and leaves prog bound.
//
// It takes the level's volumes as well as its fog because both halves of the
// fog model live in one shader file and one call has to drive all of it: a pass
// that uploaded the distance fog and forgot the volumes would be a pass where a
// fog bank stops at the grass. Making it one argument list rather than two
// functions is what turns "remember to call the other one in five places" into
// a compile error.
//
// Called once per frame rather than on change, because fogCamPos moves every
// frame anyway — the fog is measured from the eye, so there is no "unchanged"
// state to skip. A nil or switched-off fog is written as mode 0 rather than
// skipped: the block may be holding last level's fog, and a shader-side
// early-out is what makes that cost nothing. Every program reads the one
// block, so after the frame's first call the others find it unchanged.
//
// lvl resolves what each volume rides (Volume.Attach), so a fog bank bolted to
// a ship moves with it; nil reads them all as world-space.
func UploadFogTo(prog uint32, f *Fog, vols []Volume, lvl *Level) {
	if prog == 0 {
		// No program, so nowhere to upload to — a headless test, where a GL
		// call with no context current segfaults in the driver rather than
		// failing. Same guard as UploadLightsTo.
		return
	}
	gl.UseProgram(prog)
	s := gfx.SceneFor(prog)
	PackFog(&s.Block, f, vols, lvl, gfx.Cam.Position)
	s.Flush()
}

// PackFog writes the distance fog f, the fog volumes among vols and the eye
// camPos into the scene block b. No GL.
//
// Volumes first, and unconditionally: they are placed rather than
// distance-based, so a level whose distance fog is off or absent can still be
// full of fog banks.
func PackFog(b *gfx.SceneBlock, f *Fog, vols []Volume, lvl *Level, camPos mgl32.Vec3) {
	PackFogVolumes(b, vols, lvl)

	b.FogMode = int32(f.EffectiveMode())
	b.FogCamPos = camPos
	if f == nil {
		b.FogColor, b.FogStart, b.FogEnd, b.FogDensity, b.FogMaxOpacity = [3]float32{}, 0, 0, 0, 0
		return
	}
	b.FogColor = f.Color
	b.FogStart, b.FogEnd = f.Start, f.End
	b.FogDensity, b.FogMaxOpacity = f.Density, f.MaxOpacity
}
