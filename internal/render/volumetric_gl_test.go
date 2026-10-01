package render

// volumetric_gl_test.go — the volumetric pass drawn, and its pixels read back.
//
// Everything that decides whether a cloud is where it should be lives in .glsl
// files and in a chain of framebuffers — the depth copy, the downsample, the
// march, the upsample — any link of which can be broken while every program
// still compiles. So each case here draws something with an unmistakable
// answer and reads the pixel: a cloud where the ray hits it and black where it
// misses, black again once a wall stands in front of it, a fireball that glows
// its own colour in a scene with no light, a deck overhead and not underfoot,
// and a shadow under a cloud and none above it.
//
//	FOUNDRY_GL_TEST=1 go test -run 'TestVolumetric|TestCloudShadow' ./internal/render/

import (
	"foundry/internal/gfx"
	"foundry/internal/gltest"
	"foundry/internal/particle"
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// Even, so half resolution divides it exactly.
const volTestSize = 64

const (
	volTestNear = float32(0.1)
	volTestFar  = float32(1000)
)

// volTestTarget makes a colour + depth framebuffer, the way the frame loop's
// window is: the pass reads depth from whatever is bound and draws back into
// it.
func volTestTarget(t *testing.T) uint32 {
	t.Helper()
	var color, depth, fbo uint32
	gl.GenTextures(1, &color)
	gl.BindTexture(gl.TEXTURE_2D, color)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, volTestSize, volTestSize, 0, gl.RGBA, gl.UNSIGNED_BYTE, nil)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
	gl.GenRenderbuffers(1, &depth)
	gl.BindRenderbuffer(gl.RENDERBUFFER, depth)
	gl.RenderbufferStorage(gl.RENDERBUFFER, gl.DEPTH_COMPONENT24, volTestSize, volTestSize)
	gl.GenFramebuffers(1, &fbo)
	gl.BindFramebuffer(gl.FRAMEBUFFER, fbo)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, color, 0)
	gl.FramebufferRenderbuffer(gl.FRAMEBUFFER, gl.DEPTH_ATTACHMENT, gl.RENDERBUFFER, depth)
	if s := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); s != gl.FRAMEBUFFER_COMPLETE {
		t.Fatalf("test framebuffer incomplete: 0x%x", s)
	}
	gl.Viewport(0, 0, volTestSize, volTestSize)
	gl.Enable(gl.DEPTH_TEST)
	gl.DepthFunc(gl.LEQUAL)
	return fbo
}

func volTestSetup(t *testing.T) *VolumeTargets {
	t.Helper()
	gltest.Context(t)
	withRepoResources(t)
	volTestTarget(t)
	VolumetricInit()
	if !VolumetricsReady() {
		t.Fatal("the volumetric programs did not build; the log says why")
	}
	saved := Volumetrics
	gfx.Cam.Position = mgl32.Vec3{}
	targets := &VolumeTargets{}
	t.Cleanup(func() {
		Volumetrics = saved
		targets.Delete()
		cloudShadow.on = false
	})
	return targets
}

// volLook returns projView for an eye at the origin looking along dir.
func volLook(dir, up mgl32.Vec3) mgl32.Mat4 {
	proj := mgl32.Perspective(mgl32.DegToRad(60), 1, volTestNear, volTestFar)
	return proj.Mul4(mgl32.LookAtV(mgl32.Vec3{}, dir, up))
}

// depthAt is the depth-buffer value a surface d units straight ahead writes.
func depthAt(d float32) float64 {
	n, f := volTestNear, volTestFar
	ndc := (f+n)/(f-n) - (2*f*n)/((f-n)*d)
	return float64(ndc*0.5 + 0.5)
}

// volDraw clears to black with the scene's depth at depth, draws the pass, and
// returns the pixel at (x, y).
func volDraw(t *testing.T, targets *VolumeTargets, lvl *world.Level, projView mgl32.Mat4, depth float64, x, y int32) [4]uint8 {
	t.Helper()
	gl.ClearColor(0, 0, 0, 1)
	gl.ClearDepth(depth)
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	gl.ClearDepth(1)
	RenderVolumetrics(lvl, projView, mgl32.Vec3{}, nil, targets)
	var px [4]uint8
	gl.ReadPixels(x, y, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
	return px
}

// ambientLevel is a level lit only by a white global light: no sun, so a cloud
// is exactly its ambient colour and nothing about the phase function or the
// light march can make a case pass by accident.
func ambientLevel() *world.Level {
	lvl := &world.Level{}
	lvl.Lights.Global = world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 1}
	return lvl
}

// denseCloud is a sphere of cloud with every knob set to "solid white".
func denseCloud(center mgl32.Vec3, radius float32) world.Volume {
	v := world.NewVolume("cloud", center)
	v.Shape = world.VolumeSphere
	v.HalfX, v.HalfY, v.HalfZ = radius, radius, radius
	c := world.DefaultVolumeCloud()
	c.Coverage = 1
	c.Density = 4
	c.Erosion = 0
	c.Softness = 0.05
	c.Wind = mgl32.Vec3{}
	c.Scale = 200 // one smooth lump, so the noise cannot punch a hole at the centre
	v.Cloud = &c
	return v
}

func TestVolumetricProgramsBuild(t *testing.T) {
	volTestSetup(t)
	if volumes.shapeTex == 0 || volumes.detailTex == 0 {
		t.Error("the noise volumes were not uploaded")
	}
}

func TestVolumetricCloudDrawsWhereItIsAndNowhereElse(t *testing.T) {
	targets := volTestSetup(t)
	lvl := ambientLevel()
	lvl.Volumes = []world.Volume{denseCloud(mgl32.Vec3{0, 0, 30}, 10)}
	pv := volLook(mgl32.Vec3{0, 0, 1}, mgl32.Vec3{0, 1, 0})

	for _, half := range []bool{true, false} {
		Volumetrics.HalfRes = half
		centre := volDraw(t, targets, lvl, pv, 1, volTestSize/2, volTestSize/2)
		if centre[0] < 90 {
			t.Errorf("half=%v: looking straight through the cloud read %v; it should be lit white", half, centre)
		}
		corner := volDraw(t, targets, lvl, pv, 1, 1, 1)
		if corner[0] > 4 {
			t.Errorf("half=%v: a ray that misses the cloud read %v; it should be the black behind it", half, corner)
		}
	}
	if DebugStats.VolumeDraws != 1 {
		t.Errorf("counted %d marches for one cloud", DebugStats.VolumeDraws)
	}
}

// The depth copy, the downsample and the clip, together: a wall ten units out
// hides a cloud thirty units out, at both resolutions. A pass whose copy failed
// silently draws the cloud straight through the wall, which is exactly what a
// driver refusing the blit looks like.
func TestVolumetricCloudIsClippedByTheScene(t *testing.T) {
	targets := volTestSetup(t)
	lvl := ambientLevel()
	lvl.Volumes = []world.Volume{denseCloud(mgl32.Vec3{0, 0, 30}, 10)}
	pv := volLook(mgl32.Vec3{0, 0, 1}, mgl32.Vec3{0, 1, 0})

	for _, half := range []bool{true, false} {
		Volumetrics.HalfRes = half
		if px := volDraw(t, targets, lvl, pv, depthAt(10), volTestSize/2, volTestSize/2); px[0] > 4 {
			t.Errorf("half=%v: a cloud behind a wall drew over it: %v", half, px)
		}
		// A wall INSIDE the cloud cuts it short but leaves what is in front.
		if px := volDraw(t, targets, lvl, pv, depthAt(25), volTestSize/2, volTestSize/2); px[0] < 60 {
			t.Errorf("half=%v: the five units of cloud in front of a wall drew nothing: %v", half, px)
		}
	}
	if targets.copyFailed {
		t.Error("the driver refused the depth copy; volumes would draw through walls")
	}
}

// A fireball in a scene with no light at all: every bit of colour on screen is
// its own emission, so the pixel is the colour ramp's hue.
func TestVolumetricBlobsGlowTheirOwnColour(t *testing.T) {
	targets := volTestSetup(t)

	l := particle.NewLayer("fireball")
	l.Render = particle.RenderVolume
	l.SizeRamp, l.AlphaRamp = nil, nil
	l.ColorRamp = particle.Gradient{{T: 0, C: mgl32.Vec3{1, 0.45, 0.1}}}
	l.Volume.Density = 6
	l.Volume.Displacement = 0
	l.Volume.Erosion = 0
	l.Volume.Emission = 1
	s := particle.NewSystem(l, 0, 1)
	s.Live = append(s.Live, particle.Particle{Pos: mgl32.Vec3{0, 0, 10}, Size: 6, Life: 10, Age: 0.1})

	e := world.NewEmitter("boom", mgl32.Vec3{0, 0, 10})
	e.Doc = &particle.Doc{Layers: []particle.Layer{l}}
	e.Systems = []*particle.System{s}
	lvl := &world.Level{Emitters: []world.Emitter{e}}

	px := volDraw(t, targets, lvl, volLook(mgl32.Vec3{0, 0, 1}, mgl32.Vec3{0, 1, 0}), 1, volTestSize/2, volTestSize/2)
	// Emission 1 is the colour ramp at between half (the rim) and full (the
	// core) brightness, and a dense ball is seen at its rim: red at least half
	// of 255.
	if px[0] < 120 || !(px[0] > px[1] && px[1] > px[2]) {
		t.Errorf("the fireball's core read %v; it should glow orange (red > green > blue) with no light in the scene", px)
	}
	if DebugStats.VolumeBlobs != 1 {
		t.Errorf("counted %d blobs, want 1", DebugStats.VolumeBlobs)
	}
}

// The deck is a slab overhead: looking up passes through it, looking down
// never can.
func TestVolumetricDeckCoversTheSkyAboveNotBelow(t *testing.T) {
	targets := volTestSetup(t)
	lvl := ambientLevel()
	sky := world.DefaultSky()
	deck := world.DefaultSkyClouds()
	deck.Coverage = 1
	deck.Density = 1
	deck.Erosion = 0
	deck.Altitude = 40
	deck.Thickness = 30
	deck.Wind = mgl32.Vec3{}
	sky.Clouds = &deck
	lvl.Sky = &sky

	up := volDraw(t, targets, lvl, volLook(mgl32.Vec3{0, 1, 0}, mgl32.Vec3{0, 0, 1}), 1, volTestSize/2, volTestSize/2)
	if up[0] < 60 {
		t.Errorf("looking up into the deck read %v", up)
	}
	down := volDraw(t, targets, lvl, volLook(mgl32.Vec3{0, -1, 0}, mgl32.Vec3{0, 0, 1}), 1, volTestSize/2, volTestSize/2)
	if down[0] > 4 {
		t.Errorf("looking down, away from the deck, read %v", down)
	}
}

// A clearing volume empties the cloud and the deck inside it and nothing
// outside it: the ray through a cleared sphere of cloud reads black, and one
// through a box cleared somewhere else is still lit white. The camera sits in
// a clearing box of its own for the deck case — the ship's hold, looking up out
// of it into a deck that is still there past its roof.
func TestVolumetricClearsCloudsInsideTheRegionOnly(t *testing.T) {
	targets := volTestSetup(t)
	lvl := ambientLevel()
	clear := func(id string, shape world.VolumeShape, at mgl32.Vec3, half float32) world.Volume {
		v := world.NewVolume(id, at)
		v.Shape = shape
		v.HalfX, v.HalfY, v.HalfZ = half, half, half
		v.ClearsClouds = true
		return v
	}
	pv := volLook(mgl32.Vec3{0, 0, 1}, mgl32.Vec3{0, 1, 0})

	for _, half := range []bool{true, false} {
		Volumetrics.HalfRes = half

		lvl.Volumes = []world.Volume{
			denseCloud(mgl32.Vec3{0, 0, 30}, 10),
			clear("elsewhere", world.VolumeBox, mgl32.Vec3{0, 0, -50}, 5),
		}
		if px := volDraw(t, targets, lvl, pv, 1, volTestSize/2, volTestSize/2); px[0] < 90 {
			t.Errorf("half=%v: a clear region behind the camera emptied the cloud ahead: %v", half, px)
		}

		for _, shape := range []world.VolumeShape{world.VolumeSphere, world.VolumeBox, world.VolumeCylinder} {
			lvl.Volumes = []world.Volume{
				denseCloud(mgl32.Vec3{0, 0, 30}, 10),
				clear("hold", shape, mgl32.Vec3{0, 0, 30}, 12),
			}
			if px := volDraw(t, targets, lvl, pv, 1, volTestSize/2, volTestSize/2); px[0] > 4 {
				t.Errorf("half=%v %s: cloud drew inside the region clearing it: %v", half, world.VolumeShapeName(shape), px)
			}
			// Disabled, it contains nothing — including the absence of cloud.
			lvl.Volumes[1].Enabled = false
			if px := volDraw(t, targets, lvl, pv, 1, volTestSize/2, volTestSize/2); px[0] < 90 {
				t.Errorf("half=%v %s: a disabled clear region still emptied the cloud: %v", half, world.VolumeShapeName(shape), px)
			}
		}
	}

	sky := world.DefaultSky()
	deck := world.DefaultSkyClouds()
	deck.Coverage, deck.Density, deck.Erosion = 1, 1, 0
	deck.Altitude, deck.Thickness = 40, 30
	deck.Wind = mgl32.Vec3{}
	sky.Clouds = &deck
	lvl.Sky = &sky
	up := volLook(mgl32.Vec3{0, 1, 0}, mgl32.Vec3{0, 0, 1})
	lvl.Volumes = []world.Volume{clear("hold", world.VolumeBox, mgl32.Vec3{}, 5)}
	if px := volDraw(t, targets, lvl, up, 1, volTestSize/2, volTestSize/2); px[0] < 60 {
		t.Errorf("a small clear region round the camera hid the deck far above it: %v", px)
	}
	lvl.Volumes = []world.Volume{clear("column", world.VolumeCylinder, mgl32.Vec3{0, 50, 0}, 60)}
	if px := volDraw(t, targets, lvl, up, 1, volTestSize/2, volTestSize/2); px[0] > 4 {
		t.Errorf("a clear region through the deck left cloud overhead: %v", px)
	}
}

// The pass hands the frame back exactly as it found it. The frame loop and
// ImGui both draw straight after it on the assumption that nothing moved.
func TestVolumetricLeavesGLStateAlone(t *testing.T) {
	targets := volTestSetup(t)
	lvl := ambientLevel()
	lvl.Volumes = []world.Volume{denseCloud(mgl32.Vec3{0, 0, 30}, 10)}

	var fboBefore int32
	gl.GetIntegerv(gl.DRAW_FRAMEBUFFER_BINDING, &fboBefore)
	gl.Enable(gl.DEPTH_TEST)
	gl.Disable(gl.BLEND)
	gl.Enable(gl.CULL_FACE)
	gl.CullFace(gl.BACK)
	gl.DepthMask(true)

	volDraw(t, targets, lvl, volLook(mgl32.Vec3{0, 0, 1}, mgl32.Vec3{0, 1, 0}), 1, 0, 0)

	var fbo, cull int32
	var vp [4]int32
	var mask bool
	gl.GetIntegerv(gl.DRAW_FRAMEBUFFER_BINDING, &fbo)
	gl.GetIntegerv(gl.CULL_FACE_MODE, &cull)
	gl.GetIntegerv(gl.VIEWPORT, &vp[0])
	gl.GetBooleanv(gl.DEPTH_WRITEMASK, &mask)
	if fbo != fboBefore {
		t.Errorf("framebuffer %d bound after the pass, was %d", fbo, fboBefore)
	}
	if !gl.IsEnabled(gl.DEPTH_TEST) || gl.IsEnabled(gl.BLEND) || !gl.IsEnabled(gl.CULL_FACE) ||
		gl.IsEnabled(gl.DEPTH_CLAMP) || cull != gl.BACK || !mask {
		t.Error("the pass left depth test, blending, culling, depth clamp or the depth mask changed")
	}
	if vp != [4]int32{0, 0, volTestSize, volTestSize} {
		t.Errorf("viewport %v after the pass", vp)
	}
	gl.Disable(gl.CULL_FACE)
}

// The frame loop draws into the window's MULTISAMPLED back buffer, so in the
// running engine the depth copy is a multisample resolve, not the plain copy
// the cases above make. A framebuffer of multisampled renderbuffers stands in
// for the window: the cloud has to draw, and a wall in front of it has to hide
// it, exactly as it does single-sampled.
func TestVolumetricDrawsIntoAMultisampledFrame(t *testing.T) {
	targets := volTestSetup(t)

	const samples = 4
	var color, depth, msaa uint32
	gl.GenRenderbuffers(1, &color)
	gl.BindRenderbuffer(gl.RENDERBUFFER, color)
	gl.RenderbufferStorageMultisample(gl.RENDERBUFFER, samples, gl.RGBA8, volTestSize, volTestSize)
	gl.GenRenderbuffers(1, &depth)
	gl.BindRenderbuffer(gl.RENDERBUFFER, depth)
	gl.RenderbufferStorageMultisample(gl.RENDERBUFFER, samples, gl.DEPTH24_STENCIL8, volTestSize, volTestSize)
	gl.GenFramebuffers(1, &msaa)
	gl.BindFramebuffer(gl.FRAMEBUFFER, msaa)
	gl.FramebufferRenderbuffer(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.RENDERBUFFER, color)
	gl.FramebufferRenderbuffer(gl.FRAMEBUFFER, gl.DEPTH_STENCIL_ATTACHMENT, gl.RENDERBUFFER, depth)
	if s := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); s != gl.FRAMEBUFFER_COMPLETE {
		t.Skipf("multisampled framebuffer unsupported here: 0x%x", s)
	}
	resolve := volTestTarget(t) // single-sampled, to read pixels back from
	gl.BindFramebuffer(gl.FRAMEBUFFER, msaa)

	lvl := ambientLevel()
	lvl.Volumes = []world.Volume{denseCloud(mgl32.Vec3{0, 0, 30}, 10)}
	pv := volLook(mgl32.Vec3{0, 0, 1}, mgl32.Vec3{0, 1, 0})

	centre := func(sceneDepth float64) [4]uint8 {
		gl.BindFramebuffer(gl.FRAMEBUFFER, msaa)
		gl.ClearColor(0, 0, 0, 1)
		gl.ClearDepth(sceneDepth)
		gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
		gl.ClearDepth(1)
		RenderVolumetrics(lvl, pv, mgl32.Vec3{}, nil, targets)
		gl.BindFramebuffer(gl.READ_FRAMEBUFFER, msaa)
		gl.BindFramebuffer(gl.DRAW_FRAMEBUFFER, resolve)
		gl.BlitFramebuffer(0, 0, volTestSize, volTestSize, 0, 0, volTestSize, volTestSize, gl.COLOR_BUFFER_BIT, gl.NEAREST)
		gl.BindFramebuffer(gl.FRAMEBUFFER, resolve)
		var px [4]uint8
		gl.ReadPixels(volTestSize/2, volTestSize/2, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
		return px
	}

	if px := centre(1); px[0] < 90 {
		t.Errorf("the cloud drew %v into a multisampled frame", px)
	}
	if px := centre(depthAt(10)); px[0] > 4 {
		t.Errorf("a cloud behind a wall drew over it in a multisampled frame: %v", px)
	}
	if targets.copyFailed {
		t.Error("the driver refused the multisample depth resolve")
	}
	if targets.srcInternal != gl.DEPTH24_STENCIL8 {
		t.Errorf("read the source's depth format as 0x%x, want DEPTH24_STENCIL8", targets.srcInternal)
	}
	gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
}

// The running engine's source is the DEFAULT framebuffer, whose attachments are
// named GL_DEPTH and GL_STENCIL rather than the FBO names. Asking it the wrong
// way is a GL error on every frame; this asks it the way the pass does.
func TestVolumetricReadsTheDefaultFramebuffersDepthFormat(t *testing.T) {
	volTestSetup(t)
	gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
	for i := 0; i < 16 && gl.GetError() != gl.NO_ERROR; i++ {
	}
	internal, _ := sourceDepthFormat(0)
	if err := gl.GetError(); err != gl.NO_ERROR {
		t.Errorf("querying the default framebuffer's depth raised GL error 0x%x", err)
	}
	if internal == 0 {
		t.Error("no depth format came back for the default framebuffer")
	}
}

const cloudShadowProbeVert = `#version 430
void main() {
    vec2 p = vec2(gl_VertexID == 1 ? 3.0 : -1.0, gl_VertexID == 2 ? 3.0 : -1.0);
    gl_Position = vec4(p, 0.0, 1.0);
}
`

const cloudShadowProbeFrag = `#version 430
uniform vec3 probePos;
out vec4 outColor;
void main() {
    outColor = vec4(vec3(cloudShadowFactor(0, probePos)), 1.0);
}
`

// A cloud's shadow falls under it and nowhere else: not on a point above the
// cloud, and not on a point off the side of it, with the sun straight down.
func TestCloudShadowDarkensOnlyWhatIsUnderTheCloud(t *testing.T) {
	volTestSetup(t)

	lvl := &world.Level{}
	lvl.Lights.Directional = []world.DirectionalLight{{
		Direction: mgl32.Vec3{0, -1, 0}, Color: mgl32.Vec3{1, 1, 1}, Brightness: 1,
	}}
	slab := world.NewVolume("deck", mgl32.Vec3{0, 50, 0})
	slab.HalfX, slab.HalfY, slab.HalfZ = 60, 5, 60
	c := world.DefaultVolumeCloud()
	c.Coverage, c.Density, c.Erosion, c.Scale = 1, 3, 0, 400
	c.Wind = mgl32.Vec3{}
	slab.Cloud = &c
	lvl.Volumes = []world.Volume{slab}

	RenderCloudShadowMap(lvl, mgl32.Vec3{})
	if !CloudShadowActive() {
		t.Fatal("no cloud shadow map was baked for a casting cloud under a sun")
	}

	fs, err := gfx.InjectCloudShadow(cloudShadowProbeFrag)
	if err != nil {
		t.Fatal(err)
	}
	prog, err := gfx.CompileProgramFromSources(cloudShadowProbeVert, fs)
	if err != nil {
		t.Fatalf("probe program: %v", err)
	}
	var vao uint32
	gl.GenVertexArrays(1, &vao)

	shade := func(pos mgl32.Vec3) float32 {
		gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
		volTestTarget(t)
		gl.UseProgram(prog)
		uploadCloudShadowTo(prog)
		gl.Uniform3f(world.ProgUniformLoc(prog, "probePos\x00"), pos.X(), pos.Y(), pos.Z())
		gl.Disable(gl.DEPTH_TEST)
		gl.BindVertexArray(vao)
		gl.DrawArrays(gl.TRIANGLES, 0, 3)
		gl.BindVertexArray(0)
		var px [4]uint8
		gl.ReadPixels(volTestSize/2, volTestSize/2, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
		return float32(px[0]) / 255
	}

	if got := shade(mgl32.Vec3{0, 0, 0}); got > 0.5 {
		t.Errorf("the ground under the cloud reads %.2f lit; it should be in shadow", got)
	}
	if got := shade(mgl32.Vec3{0, 80, 0}); got < 0.95 {
		t.Errorf("a point above the cloud reads %.2f lit; the cloud is below it", got)
	}
	if got := shade(mgl32.Vec3{200, 0, 0}); got < 0.95 {
		t.Errorf("the ground off the side of the cloud reads %.2f lit", got)
	}

	// And with the clouds gone, the dummy is bound and nothing is shadowed.
	cloudShadow.on = false
	if got := shade(mgl32.Vec3{0, 0, 0}); got < 0.99 {
		t.Errorf("with no cloud shadow map the ground reads %.2f lit", got)
	}
}
