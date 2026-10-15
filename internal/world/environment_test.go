package world

import (
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// The absent-block contract both types share with Music: a level that has never
// had a sky or fog must round-trip to a file with no such block in it, or every
// level in the project grows one on its next save.
func TestAnAbsentEnvironmentBlockStaysAbsent(t *testing.T) {
	if got := SkyFromRaw(nil); got != nil {
		t.Errorf("a level with no sky block produced %+v; nil must stay nil", got)
	}
	if got := SkyToRaw(nil); got != nil {
		t.Errorf("nil sky wrote %+v", got)
	}
	if got := FogFromRaw(nil); got != nil {
		t.Errorf("a level with no fog block produced %+v; nil must stay nil", got)
	}
	if got := FogToRaw(nil); got != nil {
		t.Errorf("nil fog wrote %+v", got)
	}
}

func TestFogRoundTripsThroughItsRawForm(t *testing.T) {
	want := Fog{
		Enabled:    true,
		Mode:       FogExp2,
		Color:      mgl32.Vec3{0.1, 0.2, 0.3},
		Start:      12,
		End:        340,
		Density:    0.02,
		MaxOpacity: 0.85,
		SkyBlend:   0.4,
		SkyFalloff: 6,
	}
	// The atmosphere (fog_atmosphere.go) at its switched-off defaults, which
	// is what a block without one reads back as.
	d := DefaultFog()
	want.Height, want.Aerial, want.Sun = d.Height, d.Aerial, d.Sun
	want.Height.Color, want.Aerial.Color = want.Color, want.Color
	want.SkyHaze, want.SkyDistance = d.SkyHaze, d.SkyDistance
	want.Shafts = d.Shafts // off, at its defaults (fog_shafts.go)
	got := FogFromRaw(FogToRaw(&want))
	if got == nil {
		t.Fatal("a full fog block round-tripped to nil")
	}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("fog changed across a round trip:\n got %+v\nwant %+v", *got, want)
	}
}

// A switched-off fog keeps its numbers — that is the whole difference between
// the checkbox and deleting the block, and it is the same contract
// GlobalLight.Disabled has.
func TestASwitchedOffFogKeepsItsSettings(t *testing.T) {
	f := DefaultFog()
	f.Enabled = false
	f.Start = 7
	f.Color = mgl32.Vec3{1, 0, 0}

	got := FogFromRaw(FogToRaw(&f))
	if got == nil {
		t.Fatal("a switched-off fog round-tripped to nil; it must still write a block")
	}
	if got.Enabled {
		t.Error("a switched-off fog came back switched on")
	}
	if got.Start != 7 || got.Color != (mgl32.Vec3{1, 0, 0}) {
		t.Errorf("a switched-off fog lost its settings: %+v", *got)
	}
	if got.EffectiveMode() != fogModeOff {
		t.Errorf("EffectiveMode = %d for a switched-off fog; the shader reads 0 as off", got.EffectiveMode())
	}
}

// The mode is written as a name so reordering the constants cannot silently
// reinterpret an existing level file.
func TestFogModeSurvivesItsNameRatherThanItsNumber(t *testing.T) {
	for _, m := range []FogMode{FogLinear, FogExp, FogExp2} {
		if got := FogModeFromName(FogModeName(m)); got != m {
			t.Errorf("mode %d round-tripped through %q as %d", m, FogModeName(m), got)
		}
	}
	// Anything unrecognised — including the empty string a hand-written file
	// leaves — has to land on a mode that draws, not on the off value.
	for _, name := range []string{"", "nonsense", "LINEAR"} {
		if got := FogModeFromName(name); got == fogModeOff {
			t.Errorf("mode %q read back as off; an unknown mode must fall back to a real one", name)
		}
	}
}

// Clamp exists so nothing the editor can produce reaches the shader as a
// divide-by-zero or an inverted ramp.
func TestFogClampKeepsEndPastStart(t *testing.T) {
	f := Fog{Enabled: true, Mode: FogLinear, Start: 100, End: 100}
	f.Clamp()
	if f.End <= f.Start {
		t.Errorf("Clamp left End (%v) at or before Start (%v); the linear ramp divides by their difference", f.End, f.Start)
	}

	// Dragging Start up past End must extend the fog, not invert it: the two
	// are not swapped, End is pushed.
	g := Fog{Enabled: true, Mode: FogLinear, Start: 500, End: 100}
	g.Clamp()
	if g.Start != 500 {
		t.Errorf("Clamp moved Start to %v; the author's Start is the one they dragged", g.Start)
	}
	if g.End <= 500 {
		t.Errorf("Clamp left End at %v, before Start", g.End)
	}
}

func TestSkyRoundTripsThroughItsRawForm(t *testing.T) {
	want := DefaultSky()
	want.Kind = SkyCubemap
	want.SetFacesFromBase("dusk")
	want.Rotation = 90
	want.Intensity = 1.5
	want.Tint = mgl32.Vec3{0.9, 0.95, 1}

	got := SkyFromRaw(SkyToRaw(&want))
	if got == nil {
		t.Fatal("a full sky block round-tripped to nil")
	}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("sky changed across a round trip:\n got %+v\nwant %+v", *got, want)
	}
	if got.Faces[0] != "dusk_px" || got.Faces[5] != "dusk_nz" {
		t.Errorf("faces came back as %v; the order is GL's own", got.Faces)
	}
}

// A gradient sky must not carry six empty strings into the file — nothing reads
// them and every level would grow a `faces:` list it never asked for.
func TestAGradientSkyWritesNoFaceList(t *testing.T) {
	s := DefaultSky()
	raw := SkyToRaw(&s)
	if len(raw.Faces) != 0 {
		t.Errorf("a gradient sky wrote faces %v", raw.Faces)
	}
}

// The faces are kept when the kind is switched, so flipping between the two
// modes to compare them does not cost the art.
func TestSwitchingToGradientKeepsTheCubemapFaces(t *testing.T) {
	s := DefaultSky()
	s.Kind = SkyCubemap
	s.SetFacesFromBase("dusk")
	s.Kind = SkyGradient

	got := SkyFromRaw(SkyToRaw(&s))
	if got.Faces[0] != "dusk_px" {
		t.Errorf("switching to gradient lost the faces: %v", got.Faces)
	}
	if got.HasCubemap() {
		t.Error("a gradient sky reported itself as a cubemap")
	}
}

// Five faces is not a cubemap: the renderer falls back to the gradient rather
// than binding an incomplete texture, and this is the predicate it asks.
func TestAPartlyFilledCubemapIsNotOne(t *testing.T) {
	s := DefaultSky()
	s.Kind = SkyCubemap
	s.SetFacesFromBase("dusk")
	if !s.HasCubemap() {
		t.Fatal("six named faces did not read as a cubemap")
	}
	s.Faces[3] = ""
	if s.HasCubemap() {
		t.Error("five named faces read as a cubemap")
	}
}

// The fog panel offers this as the colour to match, and the clear colour is
// built from it, so it has to fold in the tint and intensity rather than
// reporting the raw stop.
func TestSkyHorizonColorTakesTintAndIntensity(t *testing.T) {
	s := DefaultSky()
	s.Horizon = mgl32.Vec3{0.5, 0.5, 0.5}
	s.Tint = mgl32.Vec3{1, 0, 0}
	s.Intensity = 2

	got := s.HorizonColor()
	want := mgl32.Vec3{1, 0, 0}
	if got != want {
		t.Errorf("HorizonColor = %v, want %v", got, want)
	}
}
