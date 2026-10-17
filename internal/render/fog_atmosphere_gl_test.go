package render

// fog_atmosphere_gl_test.go — fog.glsl's atmosphere (height fog, aerial
// perspective, the sun's in-scattering), on a GPU, against its closed forms.
//
// The same throwaway probe fog_math_gl_test.go uses (a flat colour at a world
// position given as a uniform), read back in all three channels: the aerial
// tint is a per-channel extinction, and a test that read red alone would pass
// with it ignored.
//
//	FOUNDRY_GL_TEST=1 go test -run TestFogAtmos ./internal/render/

import (
	"math"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/glext"
	"foundry/internal/gltest"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// shadeRGB is fogProbe.shade in all three channels, through lvl's lights (for
// the sun) — a white surface at pos seen from the eye at gfx.Cam.Position.
func (p *fogProbe) shadeRGB(f *world.Fog, lvl *world.Level, pos mgl32.Vec3, additive bool) mgl32.Vec3 {
	world.UploadFogTo(p.prog, f, nil, lvl)
	gl.Uniform3f(world.ProgUniformLoc(p.prog, "probePos\x00"), pos.X(), pos.Y(), pos.Z())
	a := int32(0)
	if additive {
		a = 1
	}
	gl.Uniform1i(world.ProgUniformLoc(p.prog, "probeAdditive\x00"), a)
	gl.Disable(gl.DEPTH_TEST)
	gl.ClearColor(0, 1, 0, 1)
	gl.Clear(gl.COLOR_BUFFER_BIT)
	gl.BindVertexArray(p.vao)
	gl.DrawArrays(gl.TRIANGLES, 0, 3)
	gl.BindVertexArray(0)
	var px [4]uint8
	glext.ReadPixels(skyTestSize/2, skyTestSize/2, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
	return mgl32.Vec3{float32(px[0]) / 255, float32(px[1]) / 255, float32(px[2]) / 255}
}

func nearlyRGB(t *testing.T, what string, got, want mgl32.Vec3) {
	t.Helper()
	for i := 0; i < 3; i++ {
		if math.Abs(float64(got[i]-want[i])) > 0.02 {
			t.Errorf("%s: got %v, want %v", what, got, want)
			return
		}
	}
}

// atmosOnly is a fog with no distance fade and black air, so what survives of
// the white probe IS the transmittance.
func atmosOnly() world.Fog {
	f := world.DefaultFog()
	f.Mode = world.FogNone
	f.Color = mgl32.Vec3{}
	f.MaxOpacity = 1
	f.Height.Color, f.Aerial.Color = mgl32.Vec3{}, mgl32.Vec3{}
	return f
}

func TestFogAtmosHeightFogIntegratesTheExponential(t *testing.T) {
	p := fogProbeSetup(t)
	f := atmosOnly()
	f.Height = world.FogHeight{Enabled: true, Density: 0.02, Base: 5, Falloff: 0.2}

	// Level rays: the density at the eye's height times the length.
	for _, y := range []float32{5, 10, 0} {
		gfx.Cam.Position = mgl32.Vec3{0, y, 0}
		for _, d := range []float32{20, 60} {
			want := float32(math.Exp(-0.02 * float64(d) * math.Exp(-0.2*float64(y-5))))
			nearly(t, "level ray", p.shadeRGB(&f, nil, mgl32.Vec3{0, y, d}, false)[0], want)
		}
	}
	// Straight up out of the fog: σ/k·e^(−k(y0−base))·(1 − e^(−k·h)), and the
	// same depth looking back down the same segment.
	gfx.Cam.Position = mgl32.Vec3{0, 5, 0}
	h := 30.0
	depth := 0.02 / 0.2 * (1 - math.Exp(-0.2*h))
	nearly(t, "looking up", p.shadeRGB(&f, nil, mgl32.Vec3{0, 5 + float32(h), 0}, false)[0], float32(math.Exp(-depth)))
	gfx.Cam.Position = mgl32.Vec3{0, 5 + float32(h), 0}
	nearly(t, "looking down", p.shadeRGB(&f, nil, mgl32.Vec3{0, 5, 0}, false)[0], float32(math.Exp(-depth)))
	gfx.Cam.Position = mgl32.Vec3{}

	// Off, it is nothing at all.
	f.Height.Enabled = false
	nearlyRGB(t, "off", p.shadeRGB(&f, nil, mgl32.Vec3{0, 0, 500}, false), mgl32.Vec3{1, 1, 1})
}

func TestFogAtmosAerialTintsTowardBlueWithRange(t *testing.T) {
	p := fogProbeSetup(t)
	f := atmosOnly()
	f.Aerial = world.DefaultFogAerial(mgl32.Vec3{})
	f.Aerial.Distance = 1000
	f.Aerial.Tint = mgl32.Vec3{0.5, 1, 2}
	f.Aerial.ScaleHeight = 1e6 // level and uniform

	got := p.shadeRGB(&f, nil, mgl32.Vec3{0, 0, 500}, false)
	nearlyRGB(t, "500 m of tinted haze", got, mgl32.Vec3{
		float32(math.Exp(-0.25)), float32(math.Exp(-0.5)), float32(math.Exp(-1))})

	// A grey haze colour: the far surface goes toward it fastest in blue,
	// which on a red rock is the bluish shift at range.
	f.Aerial.Color = mgl32.Vec3{0.5, 0.5, 0.5}
	got = p.shadeRGB(&f, nil, mgl32.Vec3{0, 0, 500}, false)
	for i, tr := range []float64{math.Exp(-0.25), math.Exp(-0.5), math.Exp(-1)} {
		want := float32(tr + 0.5*(1-tr))
		if math.Abs(float64(got[i]-want)) > 0.02 {
			t.Errorf("channel %d with grey haze: got %.3f, want %.3f", i, got[i], want)
		}
	}

	// The cap holds: with MaxOpacity 0.5 nothing loses more than half.
	f.Aerial.Color = mgl32.Vec3{}
	f.MaxOpacity = 0.5
	nearlyRGB(t, "capped", p.shadeRGB(&f, nil, mgl32.Vec3{0, 0, 50000}, false), mgl32.Vec3{0.5, 0.5, 0.5})

	// An additive surface is swallowed by the same transmittance, not tinted.
	f.MaxOpacity = 1
	f.Aerial.Color = mgl32.Vec3{1, 1, 1}
	got = p.shadeRGB(&f, nil, mgl32.Vec3{0, 0, 500}, true)
	nearlyRGB(t, "additive", got, mgl32.Vec3{
		float32(math.Exp(-0.25)), float32(math.Exp(-0.5)), float32(math.Exp(-1))})
}

func TestFogAtmosSunGlowsTowardTheSun(t *testing.T) {
	p := fogProbeSetup(t)
	f := atmosOnly()
	f.Aerial = world.DefaultFogAerial(mgl32.Vec3{})
	f.Aerial.Distance, f.Aerial.Tint, f.Aerial.ScaleHeight = 1000, mgl32.Vec3{1, 1, 1}, 1e6
	f.Sun = world.FogSun{Enabled: true, Strength: 0.1, Anisotropy: 0.6, Color: mgl32.Vec3{1, 1, 1}}
	lvl := &world.Level{}
	// The sun low in +Z: light travels toward -Z.
	lvl.Lights.Directional = []world.DirectionalLight{{ID: "sun",
		Direction: mgl32.Vec3{0, -0.2, -1}.Normalize(), Color: mgl32.Vec3{1, 0.8, 0.6}, Brightness: 1}}

	// Black air: what it adds is (sun colour · (lobe − 1) · strength) · (1 − T).
	toward := mgl32.Vec3{0, 0.2, 1}.Normalize().Mul(500)
	away := toward.Mul(-1)
	T := math.Exp(-0.5)
	hg := func(mu float64) float64 { g := 0.6; d := 1 + g*g - 2*g*mu; return (1 - g*g) / (d * math.Sqrt(d)) }
	white := p.shadeRGB(&f, lvl, toward, false)
	wantT := (hg(1) - 1) * 0.1 * (1 - T)
	// The probe is white: T of it survives, then the air adds its glow.
	if math.Abs(float64(white[0])-(T+wantT)) > 0.03 {
		t.Errorf("looking into the sun: red %.3f, want %.3f", white[0], T+wantT)
	}
	if b := float64(white[2]); math.Abs(b-(T+wantT*0.6)) > 0.03 {
		t.Errorf("looking into the sun: blue %.3f, want %.3f (the sun's colour)", b, T+wantT*0.6)
	}
	back := p.shadeRGB(&f, lvl, away, false)
	// Away from the sun the lobe is under its average, and black air
	// cannot go below black.
	if want := T + max((hg(-1)-1)*0.1, 0)*(1-T); math.Abs(float64(back[0])-want) > 0.03 {
		t.Errorf("looking away from the sun: red %.3f, want %.3f", back[0], want)
	}
	if !(white[0] > back[0]+0.2) {
		t.Errorf("the haze toward the sun (%v) is not brighter than away from it (%v)", white, back)
	}

	// No sun in the level: no glow.
	nearly(t, "no sun", p.shadeRGB(&f, &world.Level{}, toward, false)[0], float32(T))
}

// The sky takes the same integral as the ground: a level sky ray through a
// thick haze is the haze's colour, straight up keeps the sky, and a sky with
// fog but no atmosphere draws as it always did.
func TestFogAtmosHazesTheHorizonNotTheZenith(t *testing.T) {
	gltest.Context(t)
	withRepoResources(t)
	skyTestTarget(t)
	SkyInit()

	s := world.DefaultSky()
	s.Zenith = mgl32.Vec3{0, 0, 1}
	s.Horizon = mgl32.Vec3{0, 0, 1}
	s.Ground = mgl32.Vec3{0, 0, 1}
	s.Tint = mgl32.Vec3{1, 1, 1}
	s.Intensity = 1

	f := world.DefaultFog()
	f.Mode = world.FogNone
	f.SkyBlend = 0
	lvl := &world.Level{Sky: &s, Fog: &f}
	closeTo(t, "fog, no atmosphere", drawSkyLooking(t, lvl, mgl32.Vec3{0, 0, 1}, mgl32.Vec3{0, 1, 0}), s.Horizon)

	f.Aerial = world.DefaultFogAerial(mgl32.Vec3{1, 0, 0})
	f.Aerial.Distance, f.Aerial.ScaleHeight, f.Aerial.Tint = 500, 1000, mgl32.Vec3{1, 1, 1}
	closeTo(t, "level through the haze", drawSkyLooking(t, lvl, mgl32.Vec3{0, 0, 1}, mgl32.Vec3{0, 1, 0}),
		mgl32.Vec3{1, 0, 0})
	// Straight up: σ·H = 1000/500 = 2 of depth.
	T := float32(math.Exp(-2))
	closeTo(t, "straight up", drawSkyLooking(t, lvl, mgl32.Vec3{0, 1, 0}, mgl32.Vec3{0, 0, 1}),
		mgl32.Vec3{1 - T, 0, T})
	// sky_haze 0: the sky is left alone.
	f.SkyHaze = 0
	closeTo(t, "sky haze 0", drawSkyLooking(t, lvl, mgl32.Vec3{0, 0, 1}, mgl32.Vec3{0, 1, 0}), s.Horizon)
}
