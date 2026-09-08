package world

// volume_fog_test.go — the parts of a fog volume that are answerable in Go.
//
// What the fog LOOKS like is fog.glsl's, and is proved on a GPU by
// render/fog_volume_gl_test.go. What is left here is everything that decides
// whether the shader ever hears about a volume at all — which volumes are sent,
// what basis they are sent in, and whether a level file survives the trip.

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func foggyVolume(id string) Volume {
	v := NewVolume(id, mgl32.Vec3{0, 0, 0})
	f := DefaultVolumeFog()
	v.Fog = &f
	return v
}

func TestVolumeFogRoundTrips(t *testing.T) {
	v := testVolume(VolumeSphere)
	f := VolumeFog{Enabled: true, Color: mgl32.Vec3{0.2, 0.3, 0.4}, Density: 0.42, MaxOpacity: 0.8}
	v.Fog = &f

	back, err := VolumeFromRaw(VolumeToRaw(&v))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if back.Fog == nil {
		t.Fatal("the fog block did not survive the round trip")
	}
	if *back.Fog != f {
		t.Errorf("fog came back %+v, want %+v", *back.Fog, f)
	}
}

// A volume that never had fog must not acquire an empty block by being loaded
// and saved — the guarantee that adding this feature changed no existing level
// file, and the same one load_level makes.
func TestAVolumeWithoutFogWritesNoFogBlock(t *testing.T) {
	plain := testVolume(VolumeBox)
	if raw := VolumeToRaw(&plain); raw.Fog != nil {
		t.Errorf("a plain volume wrote a fog block: %+v", raw.Fog)
	}
	back, err := VolumeFromRaw(VolumeToRaw(&plain))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if back.Fog != nil {
		t.Errorf("a plain volume came back with fog: %+v", back.Fog)
	}
}

// Switching the fog off is a different gesture from deleting it, so the numbers
// have to still be there when it comes back — the same promise Fog.Enabled and
// GlobalLight.Disabled make.
func TestASwitchedOffVolumeFogKeepsItsSettings(t *testing.T) {
	v := testVolume(VolumeBox)
	f := DefaultVolumeFog()
	f.Enabled = false
	f.Density = 0.9
	f.Color = mgl32.Vec3{1, 0, 0}
	v.Fog = &f

	back, err := VolumeFromRaw(VolumeToRaw(&v))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if back.Fog == nil {
		t.Fatal("a switched-off fog was deleted rather than kept")
	}
	if back.Fog.Enabled {
		t.Error("the fog came back switched on")
	}
	if back.Fog.Density != 0.9 || back.Fog.Color != (mgl32.Vec3{1, 0, 0}) {
		t.Errorf("a switched-off fog lost its settings: %+v", *back.Fog)
	}
}

func TestVolumeFogClampsToWhatTheShaderAssumes(t *testing.T) {
	f := VolumeFog{Density: 1e6, MaxOpacity: 4, Color: mgl32.Vec3{-1, 2, 0.5}}
	f.Clamp()

	if f.Density != VolumeFogMaxDensity {
		t.Errorf("density clamped to %v, want %v", f.Density, VolumeFogMaxDensity)
	}
	if f.MaxOpacity != 1 {
		t.Errorf("max opacity clamped to %v, want 1", f.MaxOpacity)
	}
	if f.Color != (mgl32.Vec3{0, 1, 0.5}) {
		t.Errorf("colour clamped to %v", f.Color)
	}
}

// Every way of switching a volume's fog off has to keep it out of the upload:
// the shader has eight slots, and one spent on fog that draws nothing is one a
// visible bank does not get.
func TestOnlyVisibleFogVolumesAreSent(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Volume)
	}{
		{"no fog block", func(v *Volume) { v.Fog = nil }},
		{"fog switched off", func(v *Volume) { v.Fog.Enabled = false }},
		{"volume disabled", func(v *Volume) { v.Enabled = false }},
		{"no extents", func(v *Volume) { v.HalfX = 0 }},
		{"zero density", func(v *Volume) { v.Fog.Density = 0 }},
		{"zero max opacity", func(v *Volume) { v.Fog.MaxOpacity = 0 }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := foggyVolume("bank")
			if !v.HasFog() {
				t.Fatal("the unedited volume was not going to be sent in the first place")
			}
			c.edit(&v)
			if v.HasFog() {
				t.Error("still sent to the shader")
			}
			if got := FogVolumes([]Volume{v}); len(got) != 0 {
				t.Errorf("FogVolumes returned %d volumes", len(got))
			}
		})
	}
}

// The shader carries a fixed array, so the list has to stop at its length — and
// stop in level order, so which banks survive is a property of the file rather
// than of where the camera happens to be standing.
func TestFogVolumesStopAtTheShadersLimit(t *testing.T) {
	var vols []Volume
	for i := 0; i < MaxFogVolumes+3; i++ {
		vols = append(vols, foggyVolume(string(rune('a'+i))))
	}
	// One in the middle that draws nothing must not spend a slot.
	vols[2].Fog.Enabled = false

	got := FogVolumes(vols)
	if len(got) != MaxFogVolumes {
		t.Fatalf("sent %d volumes, want %d", len(got), MaxFogVolumes)
	}
	for _, v := range got {
		if v.ID == "c" {
			t.Error("a volume whose fog is switched off spent one of the slots")
		}
	}
	if got[0].ID != "a" {
		t.Errorf("the list is not in level order: first is %q", got[0].ID)
	}
}

// FogBasis is what fog.glsl intersects the view ray in, so what it has to
// guarantee is that a point on the volume's surface lands on the unit shape:
// each half-extent maps to 1 along its own axis, whatever the volume's size and
// whatever it has been rotated to.
func TestFogBasisMapsTheVolumeOntoTheUnitShape(t *testing.T) {
	v := NewVolume("bank", mgl32.Vec3{3, -4, 5})
	v.HalfX, v.HalfY, v.HalfZ = 2, 8, 0.5
	v.Rotation = mgl32.Vec3{0.3, math.Pi / 2, -0.7}

	rot := EulerMatrixYXZ(v.Rotation).Mat3()
	basis := v.FogBasis()

	// The tip of each local axis, taken out to world space, has to come back as
	// that axis' unit vector.
	for i, want := range []mgl32.Vec3{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}} {
		local := mgl32.Vec3{}
		local[i] = v.HalfExtents()[i]
		world := v.Position.Add(rot.Mul3x1(local))

		got := basis.Mul3x1(world.Sub(v.Position))
		if got.Sub(want).Len() > 1e-4 {
			t.Errorf("axis %d: half-extent mapped to %v, want %v", i, got, want)
		}
	}

	// And the centre stays the centre.
	if got := basis.Mul3x1(v.Position.Sub(v.Position)); got.Len() > 1e-6 {
		t.Errorf("the centre mapped to %v, want the origin", got)
	}
}
