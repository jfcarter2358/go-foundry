package script

// script_volume_fog_test.go — a script switching a region's fog.
//
// The reason these properties exist at all: a cellar that fills with fog when
// the vent is broken, a boss arena that clears when the fight ends. Every one of
// those is a script writing to a volume that an author placed and tuned, which
// makes the interesting case not "does the write land" but what a write does to
// a volume that has no fog block yet — because most volumes have none, and a
// script that could only ever address the ones that did would be a script an
// author has to remember to prepare for.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
	"foundry/internal/testenv"
	"foundry/internal/world"
)

func fogVolumeLevel(t *testing.T) *world.Level {
	t.Helper()
	testenv.WithoutGeometryUploads(t)

	lvl := &world.Level{}
	v := world.NewVolume("cellar", mgl32.Vec3{0, 0, 0})
	v.HalfX, v.HalfY, v.HalfZ = 4, 2, 4
	lvl.Volumes = append(lvl.Volumes, v)
	return lvl
}

// Reading the fog of a volume that has none is a fair question with a dull
// answer, not an error: it is off, and the rest reads as the defaults.
func TestVolumeWithoutFogReadsAsOff(t *testing.T) {
	lvl := fogVolumeLevel(t)

	if got := runScript(t, lvl, "cellar", `
def main() -> bool
    return @cellar.prop.fog
end
`); got != flang.BoolVal(false) {
		t.Errorf("a volume with no fog block read %v, want false", got)
	}
	if got := runScript(t, lvl, "cellar", `
def main() -> float
    return @cellar.prop.fog_density
end
`); got != flang.FloatVal(world.VolumeFogDefaultDensity) {
		t.Errorf("density read %v, want the default %v", got, world.VolumeFogDefaultDensity)
	}
	if lvl.Volumes[0].Fog != nil {
		t.Error("merely reading gave the volume a fog block")
	}
}

// Switching fog on where there was none creates it at the defaults, so a level
// does not have to pre-author an invisible fog on every volume a script might
// one day want to fill.
func TestScriptCanFillAVolumeWithFog(t *testing.T) {
	lvl := fogVolumeLevel(t)

	runScript(t, lvl, "cellar", `
def main() -> null
    @cellar.prop.fog = true
    @cellar.prop.fog_color = <0.1, 0.2, 0.3>
    @cellar.prop.fog_density = 0.4
    return null
end
`)

	f := lvl.Volumes[0].Fog
	if f == nil {
		t.Fatal("the script's write did not give the volume any fog")
	}
	if !f.Enabled {
		t.Error("the fog was created switched off")
	}
	if f.Color != (mgl32.Vec3{0.1, 0.2, 0.3}) {
		t.Errorf("colour is %v", f.Color)
	}
	if f.Density != 0.4 {
		t.Errorf("density is %v, want 0.4", f.Density)
	}
	if !lvl.Volumes[0].HasFog() {
		t.Error("the volume still would not be sent to the shader")
	}
}

// Turning fog OFF on a volume that never had any must leave it as it found it.
// Creating a switched-off block there would be a volume that acquired a `fog:`
// key by being asked a question, and the next save would write it out.
func TestSwitchingOffFogThatWasNeverThereAddsNothing(t *testing.T) {
	lvl := fogVolumeLevel(t)

	runScript(t, lvl, "cellar", `
def main() -> null
    @cellar.prop.fog = false
    return null
end
`)

	if lvl.Volumes[0].Fog != nil {
		t.Errorf("the volume acquired a fog block from being switched off: %+v", lvl.Volumes[0].Fog)
	}
}

// A write out of range is clamped rather than reaching the shader, which is
// where an unclamped density would show up as a black frame.
func TestScriptFogWritesAreClamped(t *testing.T) {
	lvl := fogVolumeLevel(t)

	runScript(t, lvl, "cellar", `
def main() -> null
    @cellar.prop.fog_density = 1000
    @cellar.prop.fog_max_opacity = 5
    return null
end
`)

	f := lvl.Volumes[0].Fog
	if f == nil {
		t.Fatal("no fog block")
	}
	if f.Density != world.VolumeFogMaxDensity {
		t.Errorf("density is %v, want it clamped to %v", f.Density, world.VolumeFogMaxDensity)
	}
	if f.MaxOpacity != 1 {
		t.Errorf("max opacity is %v, want it clamped to 1", f.MaxOpacity)
	}
}

// Disabling the volume puts the fog out with it. The volume is the region, and
// a disabled one contains nothing — a region that kept fogging after a script
// switched it off would be half there.
func TestDisablingTheVolumePutsItsFogOut(t *testing.T) {
	lvl := fogVolumeLevel(t)
	f := world.DefaultVolumeFog()
	lvl.Volumes[0].Fog = &f

	if !lvl.Volumes[0].HasFog() {
		t.Fatal("the volume was not fogging to begin with")
	}
	runScript(t, lvl, "cellar", `
def main() -> null
    @cellar.prop.enabled = false
    return null
end
`)
	if lvl.Volumes[0].HasFog() {
		t.Error("a disabled volume is still full of fog")
	}
	if lvl.Volumes[0].Fog == nil {
		t.Error("disabling the volume deleted its fog rather than putting it out")
	}
}
