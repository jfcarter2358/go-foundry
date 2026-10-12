package world

import (
	"math"
	"testing"

	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// A fog block written before the atmosphere existed reads back with every part
// of it off, writes back without a single new key, and packs fogAtmos 0: an
// existing level renders exactly as it did.
func TestFogWithoutAnAtmosphereIsUnchanged(t *testing.T) {
	in := `color: {r: 0.86, g: 0.78, b: 0.68}
start: 400
end: 3000
max_opacity: 0.6
`
	var r RawFog
	if err := yaml.Unmarshal([]byte(in), &r); err != nil {
		t.Fatal(err)
	}
	f := FogFromRaw(&r)
	if f.HasAtmosphere() || f.Height.Enabled || f.Aerial.Enabled || f.Sun.Enabled {
		t.Fatalf("an old fog block has an atmosphere: %+v", f)
	}
	out := FogToRaw(f)
	if out.Height != nil || out.Aerial != nil || out.Sun != nil || out.SkyHaze != nil || out.SkyDistance != nil {
		t.Errorf("an old fog block writes atmosphere keys back: %+v", out)
	}
	var b gfx.SceneBlock
	PackFog(&b, f, nil, nil, mgl32.Vec3{})
	if b.FogAtmos != 0 || b.FogHeightDensity != 0 || b.FogAerialDensity != 0 || b.FogSunColor != [3]float32{} {
		t.Errorf("an old fog block packs an atmosphere: atmos %d height %v aerial %v sun %v",
			b.FogAtmos, b.FogHeightDensity, b.FogAerialDensity, b.FogSunColor)
	}
	// And a fresh one from the editor is the plain distance fog too.
	d := DefaultFog()
	if d.HasAtmosphere() {
		t.Error("DefaultFog has an atmosphere switched on")
	}
}

func TestFogAtmosphereRoundTrips(t *testing.T) {
	in := `mode: none
color: {r: 0.9, g: 0.8, b: 0.7}
height: {density: 0.05, base: -20, falloff: 0.08}
aerial: {distance: 1800, tint: {r: 0.7, g: 1, b: 1.4}, scale_height: 900, color: {r: 0.8, g: 0.82, b: 0.85}}
sun: {strength: 0.3, anisotropy: 0.8}
sky_haze: 0.6
`
	var r RawFog
	if err := yaml.Unmarshal([]byte(in), &r); err != nil {
		t.Fatal(err)
	}
	f := FogFromRaw(&r)
	if f.Mode != FogNone || f.EffectiveMode() != FogNone {
		t.Errorf("mode none read as %v", f.Mode)
	}
	if !f.Height.Enabled || f.Height.Density != 0.05 || f.Height.Base != -20 || f.Height.Falloff != 0.08 {
		t.Errorf("height: %+v", f.Height)
	}
	if f.Height.Color != f.Color {
		t.Errorf("height fog with no colour of its own is %v, want the fog's %v", f.Height.Color, f.Color)
	}
	if !f.Aerial.Enabled || f.Aerial.Distance != 1800 || f.Aerial.ScaleHeight != 900 || f.Aerial.Tint != (mgl32.Vec3{0.7, 1, 1.4}) {
		t.Errorf("aerial: %+v", f.Aerial)
	}
	if !f.Sun.Enabled || f.Sun.Strength != 0.3 || f.Sun.Anisotropy != 0.8 || f.Sun.Color != (mgl32.Vec3{1, 1, 1}) {
		t.Errorf("sun: %+v", f.Sun)
	}
	if f.SkyHaze != 0.6 || f.SkyDistance != FogDefaultSkyDistance {
		t.Errorf("sky haze %v distance %v", f.SkyHaze, f.SkyDistance)
	}
	back := FogFromRaw(FogToRaw(f))
	if *back != *f {
		t.Errorf("round trip changed the fog:\n got %+v\nwant %+v", *back, *f)
	}

	// A part switched off keeps its numbers.
	f.Height.Enabled = false
	back = FogFromRaw(FogToRaw(f))
	if back.Height.Enabled || back.Height.Density != 0.05 {
		t.Errorf("a switched-off height fog came back as %+v", back.Height)
	}
}

// What PackFog sends: the aerial distance as a density, the scale height as a
// falloff, the sun toward the level's first lit directional light, coloured
// by it.
func TestFogAtmospherePacksTheSun(t *testing.T) {
	f := DefaultFog()
	f.Aerial.Enabled, f.Sun.Enabled = true, true
	f.Aerial.Distance, f.Aerial.ScaleHeight = 2000, 500
	lvl := &Level{}
	lvl.Lights.Directional = []DirectionalLight{
		{ID: "off", Direction: mgl32.Vec3{0, -1, 0}, Color: mgl32.Vec3{1, 0, 0}, Brightness: 1, Disabled: true},
		{ID: "sun", Direction: mgl32.Vec3{0.6, -0.8, 0}, Color: mgl32.Vec3{1, 0.5, 0.25}, Brightness: 2},
	}
	var b gfx.SceneBlock
	PackFog(&b, &f, nil, lvl, mgl32.Vec3{})
	if b.FogAtmos != 1 {
		t.Fatal("atmosphere not on")
	}
	if math.Abs(float64(b.FogAerialDensity-1.0/2000)) > 1e-9 || math.Abs(float64(b.FogAerialFalloff-1.0/500)) > 1e-9 {
		t.Errorf("aerial density %v falloff %v", b.FogAerialDensity, b.FogAerialFalloff)
	}
	if b.FogHeightDensity != 0 {
		t.Errorf("height fog is off but packs density %v", b.FogHeightDensity)
	}
	want := mgl32.Vec3{-0.6, 0.8, 0}
	if mgl32.Vec3(b.FogSunDir).Sub(want).Len() > 1e-5 {
		t.Errorf("sun dir %v, want %v (toward the lit sun)", b.FogSunDir, want)
	}
	s := f.Sun.Strength
	if mgl32.Vec3(b.FogSunColor).Sub(mgl32.Vec3{2 * s, s, 0.5 * s}).Len() > 1e-5 {
		t.Errorf("sun colour %v", b.FogSunColor)
	}
	// No sun in the level: no lobe.
	PackFog(&b, &f, nil, &Level{}, mgl32.Vec3{})
	if b.FogSunColor != [3]float32{} {
		t.Errorf("no sun, but the lobe is %v", b.FogSunColor)
	}
	// Switched off: nothing.
	f.Enabled = false
	PackFog(&b, &f, nil, lvl, mgl32.Vec3{})
	if b.FogAtmos != 0 {
		t.Error("a switched-off fog packs its atmosphere")
	}
}

// The aerial tint makes the blue channel go first, as Rayleigh's does.
func TestFogAerialTintTakesBlueFirst(t *testing.T) {
	f := DefaultFog()
	f.Aerial.Enabled = true
	T := f.FogTransmittance(0, 1500)
	if !(T[2] < T[1] && T[1] < T[0]) {
		t.Errorf("transmittance %v: want blue < green < red", T)
	}
	if near := f.FogTransmittance(0, 10); near[0] < 0.99 {
		t.Errorf("10 m of haze takes %.3f", 1-near[0])
	}
}

// The dust-haze preset is a look a cloud block starts from by name, and the
// keys beside it still win.
func TestCloudDustHazePreset(t *testing.T) {
	var r RawVolumeCloud
	if err := yaml.Unmarshal([]byte("preset: dust_haze\ndensity: 0.01\n"), &r); err != nil {
		t.Fatal(err)
	}
	c := VolumeCloudFromRaw(&r)
	p, _ := CloudLookPreset("dust_haze")
	if c.Density != 0.01 || c.Color != p.Color || c.Coverage != p.Coverage || c.CastShadows {
		t.Errorf("dust haze cloud: %+v", c.CloudLook)
	}
	if _, ok := CloudLookPreset("nope"); ok {
		t.Error("an unknown preset was found")
	}
	for _, n := range CloudPresetNames {
		if _, ok := CloudLookPreset(n); !ok {
			t.Errorf("listed preset %q is not a preset", n)
		}
	}
}
