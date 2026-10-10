package render

// sky_render_gl_test.go — the sky pass actually drawn, and its pixels read back.
//
// The compile test next door proves the programs link and the uniforms exist.
// That is most of what can go wrong, but not all of it: a sky whose sampling
// direction is built wrong compiles perfectly and draws the ground colour
// overhead, and a fog blend wired to the wrong uniform compiles perfectly and
// does nothing. Both are things you can only see by looking at a pixel.
//
// Rendered into an FBO of its own rather than into the test window's back
// buffer, so the result does not depend on a hidden window having a readable
// drawable.
//
//	FOUNDRY_GL_TEST=1 go test -run TestSkyDraws ./internal/render/

import (
	"fmt"
	"foundry/internal/gfx"
	"foundry/internal/glext"
	"foundry/internal/gltest"
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// Odd on purpose. The gradient eases with pow(t, 0.55), whose slope at the
// horizon is infinite, so a centre pixel half a texel off the view axis reads
// several percent of the way toward the zenith — an artefact of sampling, not of
// the sky. An odd width puts the middle pixel's centre exactly on NDC zero.
const skyTestSize = 65

// skyTestTarget makes a colour+depth FBO and leaves it bound.
func skyTestTarget(t *testing.T) {
	t.Helper()

	var color, depth, fbo uint32
	gl.GenTextures(1, &color)
	gl.BindTexture(gl.TEXTURE_2D, color)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, skyTestSize, skyTestSize, 0, gl.RGBA, gl.UNSIGNED_BYTE, nil)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)

	gl.GenRenderbuffers(1, &depth)
	gl.BindRenderbuffer(gl.RENDERBUFFER, depth)
	gl.RenderbufferStorage(gl.RENDERBUFFER, gl.DEPTH_COMPONENT24, skyTestSize, skyTestSize)

	gl.GenFramebuffers(1, &fbo)
	gl.BindFramebuffer(gl.FRAMEBUFFER, fbo)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, color, 0)
	gl.FramebufferRenderbuffer(gl.FRAMEBUFFER, gl.DEPTH_ATTACHMENT, gl.RENDERBUFFER, depth)
	if s := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); s != gl.FRAMEBUFFER_COMPLETE {
		t.Fatalf("test framebuffer incomplete: 0x%x", s)
	}
	gl.Viewport(0, 0, skyTestSize, skyTestSize)

	// The state the frame loop is in when RenderSky is called.
	gl.Enable(gl.DEPTH_TEST)
	gl.DepthFunc(gl.LEQUAL)
	gl.ClearDepth(1)
}

// drawSkyLooking points the camera along dir, draws the level's sky, and
// returns the centre pixel — which is the colour of exactly that direction.
func drawSkyLooking(t *testing.T, lvl *world.Level, dir, up mgl32.Vec3) [4]uint8 {
	t.Helper()

	gfx.Projection = mgl32.Perspective(mgl32.DegToRad(45), 1, 0.1, 1000)
	gfx.Cam.Position = mgl32.Vec3{10, 3, -7} // deliberately not the origin
	gfx.Cam.Direction = dir
	gfx.Cam.Up = up
	gfx.UpdateCamMatrix()

	// Magenta, so a sky that draws nothing at all is unmistakable rather than
	// looking like a plausible dark colour.
	gl.ClearColor(1, 0, 1, 1)
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)

	RenderSky(lvl)

	var px [4]uint8
	glext.ReadPixels(skyTestSize/2, skyTestSize/2, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
	return px
}

// closeTo compares a read-back pixel against a float colour, with room for the
// 8-bit quantisation and for the interpolated direction at the centre pixel
// being a fraction off dead centre.
func closeTo(t *testing.T, what string, got [4]uint8, want mgl32.Vec3) {
	t.Helper()
	const tol = 8 // of 255
	for i := 0; i < 3; i++ {
		w := int(want[i]*255 + 0.5)
		if d := int(got[i]) - w; d > tol || d < -tol {
			t.Errorf("%s: pixel %v, want ~%v (channel %d off by %d)", what, got[:3], want, i, d)
			return
		}
	}
}

func TestSkyDrawsItsGradientInTheRightDirections(t *testing.T) {
	gltest.Context(t)
	withRepoResources(t)
	skyTestTarget(t)
	SkyInit()

	// Three unmistakable, unmixable colours, so a direction wired to the wrong
	// axis names itself in the failure rather than shifting a shade.
	s := world.DefaultSky()
	s.Zenith = mgl32.Vec3{0, 1, 0}
	s.Horizon = mgl32.Vec3{1, 0, 0}
	s.Ground = mgl32.Vec3{0, 0, 1}
	s.Tint = mgl32.Vec3{1, 1, 1}
	s.Intensity = 1
	lvl := &world.Level{Sky: &s}

	// Straight up and straight down need a non-vertical up vector, or the view
	// matrix is degenerate.
	closeTo(t, "looking up", drawSkyLooking(t, lvl, mgl32.Vec3{0, 1, 0}, mgl32.Vec3{0, 0, 1}), s.Zenith)
	closeTo(t, "looking down", drawSkyLooking(t, lvl, mgl32.Vec3{0, -1, 0}, mgl32.Vec3{0, 0, 1}), s.Ground)

	// Every horizontal heading is the horizon stop exactly, which is also what
	// catches a sky that translates with the camera: the eye is not at the
	// origin, so a cube that failed to have its translation stripped would put
	// a different part of itself in front of each of these.
	for _, d := range []mgl32.Vec3{{0, 0, 1}, {0, 0, -1}, {1, 0, 0}, {-1, 0, 0}} {
		closeTo(t, fmt.Sprintf("looking along %v", d), drawSkyLooking(t, lvl, d, mgl32.Vec3{0, 1, 0}), s.Horizon)
	}
}

// A level with no sky must leave the background exactly as it found it — this
// is the whole backward-compatibility promise, and it is one `if` away from
// being broken.
func TestNoSkyDrawsNothing(t *testing.T) {
	gltest.Context(t)
	withRepoResources(t)
	skyTestTarget(t)
	SkyInit()

	got := drawSkyLooking(t, &world.Level{}, mgl32.Vec3{0, 0, 1}, mgl32.Vec3{0, 1, 0})
	closeTo(t, "a level with no sky", got, mgl32.Vec3{1, 0, 1}) // the magenta clear
}

// The horizon blend: with sky_blend at 1 and a falloff of 1, a horizontal view
// is entirely the fog colour and straight up is untouched. This is the join
// between the two features, and the uniform it rides on is set nowhere else.
func TestFogPullsTheSkyDownToTheHorizon(t *testing.T) {
	gltest.Context(t)
	withRepoResources(t)
	skyTestTarget(t)
	SkyInit()

	s := world.DefaultSky()
	s.Zenith = mgl32.Vec3{0, 1, 0}
	s.Horizon = mgl32.Vec3{1, 0, 0}
	s.Tint = mgl32.Vec3{1, 1, 1}
	s.Intensity = 1

	f := world.DefaultFog()
	f.Color = mgl32.Vec3{0, 0, 1}
	f.SkyBlend = 1
	f.SkyFalloff = 1
	lvl := &world.Level{Sky: &s, Fog: &f}

	closeTo(t, "horizon under full fog blend",
		drawSkyLooking(t, lvl, mgl32.Vec3{0, 0, 1}, mgl32.Vec3{0, 1, 0}), f.Color)
	closeTo(t, "zenith under full fog blend",
		drawSkyLooking(t, lvl, mgl32.Vec3{0, 1, 0}, mgl32.Vec3{0, 0, 1}), s.Zenith)

	// And a switched-off fog leaves the sky alone, which is what proves the
	// blend is gated on Enabled rather than merely on the block existing.
	f.Enabled = false
	closeTo(t, "horizon with the fog switched off",
		drawSkyLooking(t, lvl, mgl32.Vec3{0, 0, 1}, mgl32.Vec3{0, 1, 0}), s.Horizon)
}

// A sky mid-transition draws as a blend of the state it is easing away from and
// the one it is easing toward, not merely one or the other — which is the one
// thing the compile test and the pure-Go TransitionBlend tests beside sky.go
// cannot see for themselves: they prove the fraction is right and the uniforms
// exist, not that sky_frag.glsl actually mixes two colours rather than picking
// one.
func TestSkyEasesBetweenTwoGradients(t *testing.T) {
	gltest.Context(t)
	withRepoResources(t)
	skyTestTarget(t)
	SkyInit()

	s := world.DefaultSky()
	s.Zenith = mgl32.Vec3{1, 1, 1}
	s.Tint = mgl32.Vec3{1, 1, 1}
	s.Intensity = 1

	from := s
	from.Zenith = mgl32.Vec3{0, 0, 0}
	s.TransitionFrom = &from
	s.TransitionSeconds = 2

	up, upVec := mgl32.Vec3{0, 1, 0}, mgl32.Vec3{0, 0, 1}
	lvl := &world.Level{Sky: &s}

	s.TransitionElapsed = 0
	closeTo(t, "blend 0 reads as the from-state", drawSkyLooking(t, lvl, up, upVec), from.Zenith)

	s.TransitionElapsed = 2 // fully eased in
	closeTo(t, "blend 1 reads as the to-state", drawSkyLooking(t, lvl, up, upVec), s.Zenith)

	s.TransitionElapsed = 1 // halfway
	closeTo(t, "blend 0.5 reads as the midpoint",
		drawSkyLooking(t, lvl, up, upVec), mgl32.Vec3{0.5, 0.5, 0.5})
}

// Rotation turns the image on the sky, not the cube. With the gradient there is
// nothing to turn — which is itself the assertion: a rotation that leaked into
// the geometry would tilt the horizon.
func TestSkyRotationDoesNotTiltTheGradient(t *testing.T) {
	gltest.Context(t)
	withRepoResources(t)
	skyTestTarget(t)
	SkyInit()

	s := world.DefaultSky()
	s.Zenith = mgl32.Vec3{0, 1, 0}
	s.Horizon = mgl32.Vec3{1, 0, 0}
	s.Rotation = 90
	lvl := &world.Level{Sky: &s}

	closeTo(t, "rotated sky, looking up",
		drawSkyLooking(t, lvl, mgl32.Vec3{0, 1, 0}, mgl32.Vec3{0, 0, 1}), s.Zenith)
	closeTo(t, "rotated sky, looking level",
		drawSkyLooking(t, lvl, mgl32.Vec3{0, 0, 1}, mgl32.Vec3{0, 1, 0}), s.Horizon)
}
