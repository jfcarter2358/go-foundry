package world

import (
	"reflect"
	"testing"

	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

func ambientTestVolume() Volume {
	a := DefaultVolumeAmbient()
	a.Sky, a.Ground, a.Fade = 0.1, 0.4, 2
	return Volume{ID: "cave", Enabled: true, Position: mgl32.Vec3{0, 5, 0}, HalfX: 10, HalfY: 5, HalfZ: 4, Ambient: &a}
}

// A volume without the block writes none; one with it round-trips, ground
// written only where it differs from sky.
func TestVolumeAmbientRoundTrip(t *testing.T) {
	plain := Volume{ID: "trigger", Enabled: true, HalfX: 1, HalfY: 1, HalfZ: 1}
	if r := VolumeToRaw(&plain); r.Ambient != nil {
		t.Fatalf("a plain volume wrote an ambient block: %+v", r.Ambient)
	}
	v := ambientTestVolume()
	v.Ambient.Dust = 0.02
	raw := VolumeToRaw(&v)
	out, err := yaml.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var back RawVolume
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	got, err := VolumeFromRaw(back)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Ambient, v.Ambient) {
		t.Fatalf("ambient changed across a round trip:\n got %+v\nwant %+v\n%s", got.Ambient, v.Ambient, out)
	}
	same := ambientTestVolume()
	same.Ambient.Ground = same.Ambient.Sky
	if r := VolumeAmbientToRaw(same.Ambient); r.Ground != nil {
		t.Fatalf("ground equal to sky was written: %v", *r.Ground)
	}
	// `ambient: {sky: 0.2}` — ground follows sky.
	a := VolumeAmbientFromRaw(&RawVolumeAmbient{Sky: ptrF(0.2)})
	if a.Ground != 0.2 || a.Fade != VolumeAmbientDefaultFade || a.Fog != 1 || !a.Enabled {
		t.Fatalf("defaults: %+v", a)
	}
}

// The factor is 1 outside, the authored cut deep inside (sky facing up,
// ground facing down), eased over the fade near a wall, and the darkest of
// overlapping volumes.
func TestAmbientFactorInsideAndOut(t *testing.T) {
	vols := []Volume{ambientTestVolume()}
	up, down := mgl32.Vec3{0, 1, 0}, mgl32.Vec3{0, -1, 0}
	near := func(a, b float32) bool { return a-b < 1e-4 && b-a < 1e-4 }
	if f := AmbientFactorAt(vols, nil, mgl32.Vec3{20, 5, 0}, up); f != 1 {
		t.Fatalf("outside: %v", f)
	}
	if f := AmbientFactorAt(vols, nil, mgl32.Vec3{0, 5, 0}, up); !near(f, 0.1) {
		t.Fatalf("deep inside facing up: %v, want 0.1", f)
	}
	if f := AmbientFactorAt(vols, nil, mgl32.Vec3{0, 5, 0}, down); !near(f, 0.4) {
		t.Fatalf("deep inside facing down: %v, want 0.4", f)
	}
	// 1 m in from the +X wall with a 2 m fade: half way.
	if f := AmbientFactorAt(vols, nil, mgl32.Vec3{9, 5, 0}, up); !near(f, 0.55) {
		t.Fatalf("1 m inside a 2 m fade: %v, want 0.55", f)
	}
	dark := ambientTestVolume()
	dark.Ambient.Sky = 0.02
	dark.HalfX = 2
	vols = append(vols, dark)
	if f := AmbientFactorAt(vols, nil, mgl32.Vec3{0, 5, 0}, up); !near(f, 0.02) {
		t.Fatalf("two volumes: %v, want the darker 0.02", f)
	}
	// A disabled volume or a switched-off block cuts nothing.
	vols[1].Enabled = false
	vols[0].Ambient.Enabled = false
	if f := AmbientFactorAt(vols, nil, mgl32.Vec3{0, 5, 0}, up); f != 1 {
		t.Fatalf("switched off: %v", f)
	}
}

// The scene block gets the nearest MaxAmbientVolumes, and the slots past the
// count are zeroed.
func TestPackAmbientVolumesNearestFirst(t *testing.T) {
	var vols []Volume
	for i := 0; i < MaxAmbientVolumes+3; i++ {
		v := ambientTestVolume()
		v.ID = "v"
		v.Position = mgl32.Vec3{float32(100 * (MaxAmbientVolumes + 3 - i)), 0, 0}
		v.HalfX, v.HalfY, v.HalfZ = 1, 1, 1
		vols = append(vols, v)
	}
	var b gfx.SceneBlock
	b.AmbVolParams[MaxAmbientVolumes-1] = [4]float32{9, 9, 9, 9}
	PackAmbientVolumes(&b, vols, nil, mgl32.Vec3{})
	if b.NumAmbVolumes != MaxAmbientVolumes {
		t.Fatalf("packed %d, want %d", b.NumAmbVolumes, MaxAmbientVolumes)
	}
	if b.AmbVolCenter[0][0] != 100 {
		t.Fatalf("nearest first: slot 0 at x=%v, want 100", b.AmbVolCenter[0][0])
	}
	PackAmbientVolumes(&b, nil, nil, mgl32.Vec3{})
	if b.NumAmbVolumes != 0 || b.AmbVolParams[0] != ([4]float32{}) {
		t.Fatalf("emptied block kept %d volumes / %v", b.NumAmbVolumes, b.AmbVolParams[0])
	}
}

// Shafts: absent is off and writes nothing; a block round-trips.
func TestFogShaftsRoundTrip(t *testing.T) {
	f := DefaultFog()
	if r := FogToRaw(&f); r.Shafts != nil {
		t.Fatalf("a fog without shafts wrote a shafts block: %+v", r.Shafts)
	}
	if f.ShaftsOn() {
		t.Fatal("shafts on by default")
	}
	f.Shafts = DefaultFogShafts()
	f.Shafts.Density, f.Shafts.Steps, f.Shafts.Falloff, f.Shafts.Base = 0.03, 32, 0.05, 12
	f.Shafts.Color = mgl32.Vec3{1, 0.9, 0.7}
	out, err := yaml.Marshal(FogToRaw(&f))
	if err != nil {
		t.Fatal(err)
	}
	var raw RawFog
	if err := yaml.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	got := FogFromRaw(&raw)
	if !reflect.DeepEqual(got.Shafts, f.Shafts) {
		t.Fatalf("shafts changed across a round trip:\n got %+v\nwant %+v\n%s", got.Shafts, f.Shafts, out)
	}
	if !got.ShaftsOn() {
		t.Fatal("shafts read back off")
	}
	// `shafts: {}` is on at the defaults.
	if g := FogFromRaw(&RawFog{Shafts: &RawFogShafts{}}); !g.ShaftsOn() || g.Shafts.Distance != FogShaftsDefaultDistance {
		t.Fatalf("empty block: %+v", g.Shafts)
	}
}
