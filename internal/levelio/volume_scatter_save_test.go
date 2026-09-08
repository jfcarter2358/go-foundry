package levelio

// volume_scatter_save_test.go — a scatter volume survives a save and a load.
//
// The round trip is where the "0 means the kind's default" convention either
// holds or quietly loses an authored value: a field whose 0 is a real setting
// has to be a pointer in the raw form, and one whose 0 means "default" must
// not be.

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func TestVolumeScatterRoundTrips(t *testing.T) {
	want := world.VolumeScatter{
		Enabled:           true,
		Kind:              world.ScatterWildflower,
		Density:           0.42,
		Range:             77,
		Color:             mgl32.Vec3{0.2, 0.9, 0.4},
		LOD1Distance:      12,
		LOD2Distance:      34,
		GrassHeight:       0.8,
		GrassWidth:        0.02,
		HeadScale:         0.3,
		GrassClump:        0.6,
		BloomSpike:        true,
		BloomCenter:       true,
		BloomSolidColor:   true,
		CenterColor:       mgl32.Vec3{1, 0.5, 0},
		StemLeaves:        true,
		LeafScale:         2,
		RockSides:         7,
		RockScale:         0.9,
		MinSlopeAngle:     5,
		MaxSlopeAngle:     55,
		RangeFadeFraction: 0.25,
		EdgeFadeDistance:  3,
		BoundaryJitter:    6,
	}
	v := world.Volume{ID: "meadow", Enabled: true, HalfX: 4, HalfY: 2, HalfZ: 4, Scatter: &want}

	got := world.VolumeScatterFromRaw(world.VolumeScatterToRaw(v.Scatter))
	if got == nil {
		t.Fatal("the scatter block did not survive the round trip")
	}
	if *got != want {
		t.Errorf("round trip changed the scatter:\n got %+v\nwant %+v", *got, want)
	}
}

// A volume with no scatter must not acquire one by being saved — the same
// property `fog:` has, and the reason both are pointers.
func TestAVolumeWithoutScatterStaysWithout(t *testing.T) {
	v := world.Volume{ID: "trigger", Enabled: true, HalfX: 1, HalfY: 1, HalfZ: 1}
	raw := world.VolumeToRaw(&v)
	if raw.Scatter != nil {
		t.Errorf("a plain trigger acquired a scatter block: %+v", raw.Scatter)
	}
	back, err := world.VolumeFromRaw(raw)
	if err != nil {
		t.Fatalf("VolumeFromRaw: %v", err)
	}
	if back.Scatter != nil {
		t.Errorf("loading gave it one: %+v", back.Scatter)
	}
}

// `scatter: {}` is scatter, on the same terms `fog: {}` is fog. The block's
// presence is what turns the feature on, so the zero value has to read as
// enabled or writing an empty block would be a trap.
func TestAnEmptyScatterBlockIsEnabled(t *testing.T) {
	got := world.VolumeScatterFromRaw(&world.RawVolumeScatter{})
	if got == nil {
		t.Fatal("an empty scatter block loaded as nothing")
	}
	if !got.Enabled {
		t.Error("an empty scatter block loaded switched off")
	}
	if got.Kind != world.ScatterGrass {
		t.Errorf("kind = %v, want grass", got.Kind)
	}
	if got.Density != 0.8 {
		t.Errorf("density = %v, want the default", got.Density)
	}
}

// A field whose 0 is a REAL authored value has to survive as 0. Density is the
// one that would bite: it is a pointer in the raw form precisely so that
// "grow nothing" is distinguishable from "not stated".
func TestAZeroDensityIsNotTheDefault(t *testing.T) {
	zero := float32(0)
	got := world.VolumeScatterFromRaw(&world.RawVolumeScatter{Density: &zero})
	if got.Density != 0 {
		t.Errorf("an authored density of 0 loaded as %v", got.Density)
	}
}

// And a field whose 0 means "use the kind's default" has to resolve, not be
// taken literally — a range of 0 would draw nothing at all.
func TestAZeroRangeMeansTheKindsDefault(t *testing.T) {
	for _, tc := range []struct {
		kind int32
		want float32
	}{
		{world.ScatterGrass, 45},
		{world.ScatterRock, 90},
		{world.ScatterMoss, 35},
	} {
		s := world.VolumeScatter{Kind: tc.kind}
		if got := world.ScatterRange(&s); got != tc.want {
			t.Errorf("kind %v: range = %v, want %v", tc.kind, got, tc.want)
		}
	}
}

// The LOD boundaries must come out ordered whatever a hand-edited file says —
// the per-tier draw calls assume 0 < LOD1 <= LOD2 <= Range unconditionally.
func TestLODBoundariesStayOrdered(t *testing.T) {
	s := world.VolumeScatter{Kind: world.ScatterGrass, Range: 40}
	// Deliberately backwards, and past the range.
	s.LOD1Distance, s.LOD2Distance = 90, 5

	lod1, lod2 := world.ResolvedLOD1(&s), world.ResolvedLOD2(&s)
	rng := world.ScatterRange(&s)
	if !(lod1 > 0 && lod1 <= lod2 && lod2 <= rng) {
		t.Errorf("boundaries out of order: lod1=%v lod2=%v range=%v", lod1, lod2, rng)
	}
}
