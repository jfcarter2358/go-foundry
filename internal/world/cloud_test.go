package world

// cloud_test.go — the two cloud blocks through YAML, their gates, and the deck
// through an ease_sky.

import (
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

func tunedCloudLook() CloudLook {
	return CloudLook{
		Color:       mgl32.Vec3{0.8, 0.7, 0.6},
		Coverage:    0.25,
		Density:     1.5,
		Scale:       120,
		Detail:      7,
		Erosion:     0,
		Wind:        mgl32.Vec3{0, 0, 0},
		Absorption:  2.5,
		Ambient:     0.5,
		CastShadows: false,
	}
}

// Every field, including the zeros that are not defaults — a zero erosion and a
// still wind are both things an author means.
func TestVolumeCloudRoundTripsEveryField(t *testing.T) {
	v := NewVolume("thunderhead", mgl32.Vec3{1, 2, 3})
	v.Cloud = &VolumeCloud{Enabled: false, CloudLook: tunedCloudLook(), Softness: 0.8}

	data, err := yaml.Marshal(VolumeToRaw(&v))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw RawVolume
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	back, err := VolumeFromRaw(raw)
	if err != nil {
		t.Fatalf("from raw: %v", err)
	}
	if back.Cloud == nil || *back.Cloud != *v.Cloud {
		t.Fatalf("cloud changed across a round trip:\n got %+v\nwant %+v\n%s", back.Cloud, v.Cloud, data)
	}
}

func TestVolumeWithoutCloudWritesNoCloudKey(t *testing.T) {
	v := NewVolume("trigger", mgl32.Vec3{})
	data, err := yaml.Marshal(VolumeToRaw(&v))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "cloud") {
		t.Errorf("a plain trigger wrote a cloud key:\n%s", data)
	}
}

// `cloud: {}` is a cloud at the defaults, not a switched-off one.
func TestEmptyCloudBlockIsADefaultCloud(t *testing.T) {
	var raw RawVolume
	if err := yaml.Unmarshal([]byte("id: c\nhalf: {x: 1, y: 1, z: 1}\ncloud: {}\n"), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	v, err := VolumeFromRaw(raw)
	if err != nil {
		t.Fatalf("from raw: %v", err)
	}
	if v.Cloud == nil || *v.Cloud != DefaultVolumeCloud() {
		t.Errorf("empty block loaded as %+v, want the defaults", v.Cloud)
	}
	if !v.HasCloud() {
		t.Error("a default cloud in a bounded, enabled volume draws nothing")
	}
}

func TestCloudClampKeepsTheShaderInRange(t *testing.T) {
	c := VolumeCloud{CloudLook: CloudLook{
		Color:    mgl32.Vec3{2, -1, 0.5},
		Coverage: 3, Density: -1, Scale: 0, Detail: 1e9, Erosion: 2,
		Wind: mgl32.Vec3{1e6, 0, -1e6}, Absorption: 99, Ambient: -3,
	}, Softness: 0}
	c.Clamp()
	if c.Color != (mgl32.Vec3{1, 0, 0.5}) || c.Coverage != 1 || c.Density != 0 ||
		c.Scale != CloudMinScale || c.Detail != CloudMaxScale || c.Erosion != 1 ||
		c.Wind != (mgl32.Vec3{CloudMaxWind, 0, -CloudMaxWind}) ||
		c.Absorption != CloudMaxAbsorption || c.Ambient != 0 || c.Softness != 0.01 {
		t.Errorf("clamped to %+v", c)
	}
}

// Every way of turning a cloud off has to turn it off.
func TestHasCloudGates(t *testing.T) {
	base := NewVolume("c", mgl32.Vec3{})
	cloud := DefaultVolumeCloud()
	base.Cloud = &cloud
	if !base.HasCloud() {
		t.Fatal("the reference cloud does not draw")
	}

	cases := map[string]func(v *Volume){
		"no block":        func(v *Volume) { v.Cloud = nil },
		"cloud off":       func(v *Volume) { c := *v.Cloud; c.Enabled = false; v.Cloud = &c },
		"volume disabled": func(v *Volume) { v.Enabled = false },
		"zero density":    func(v *Volume) { c := *v.Cloud; c.Density = 0; v.Cloud = &c },
		"zero coverage":   func(v *Volume) { c := *v.Cloud; c.Coverage = 0; v.Cloud = &c },
		"no extents":      func(v *Volume) { v.HalfY = 0 },
	}
	for name, mutate := range cases {
		v := base
		mutate(&v)
		if v.HasCloud() {
			t.Errorf("%s: still draws", name)
		}
	}
	if got := CloudVolumes([]Volume{base, NewVolume("plain", mgl32.Vec3{})}); len(got) != 1 || got[0].ID != "c" {
		t.Errorf("CloudVolumes returned %d volumes", len(got))
	}
}

func TestSkyCloudsRoundTrip(t *testing.T) {
	s := DefaultSky()
	s.Clouds = &SkyClouds{Enabled: true, CloudLook: tunedCloudLook(), Altitude: -40, Thickness: 12, Range: 900}

	data, err := yaml.Marshal(SkyToRaw(&s))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw RawSky
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	back := SkyFromRaw(&raw)
	if back.Clouds == nil || *back.Clouds != *s.Clouds {
		t.Fatalf("deck changed across a round trip:\n got %+v\nwant %+v", back.Clouds, s.Clouds)
	}

	clear := DefaultSky()
	data, _ = yaml.Marshal(SkyToRaw(&clear))
	if strings.Contains(string(data), "clouds") {
		t.Errorf("a clear sky wrote a clouds key:\n%s", data)
	}
}

// ease_sky snapshots the sky and the writes after it change the live one in
// place. If the snapshot shared the deck, there would be nothing to ease from.
func TestBeginTransitionSnapshotsTheDeck(t *testing.T) {
	s := DefaultSky()
	deck := DefaultSkyClouds()
	s.Clouds = &deck
	s.BeginTransition(2)
	s.Clouds.Coverage = 1

	if s.TransitionFrom.Clouds.Coverage == 1 {
		t.Fatal("the transition's snapshot shares its deck with the live sky")
	}
}

func TestEffectiveCloudsEasesBetweenDecks(t *testing.T) {
	near := func(what string, got, want float32) {
		t.Helper()
		if d := got - want; d > 1e-4 || d < -1e-4 {
			t.Errorf("%s: got %v, want %v", what, got, want)
		}
	}

	// Settled.
	s := DefaultSky()
	if _, ok := s.EffectiveClouds(); ok {
		t.Error("a clear sky has a deck")
	}
	deck := DefaultSkyClouds()
	s.Clouds = &deck
	if c, ok := s.EffectiveClouds(); !ok || c != deck {
		t.Error("a settled sky's deck is not its own")
	}

	// Clearing: halfway from a deck to none, the deck is half covered.
	s.BeginTransition(2)
	s.Clouds = nil
	s.TransitionElapsed = 1
	c, ok := s.EffectiveClouds()
	if !ok {
		t.Fatal("halfway through clearing, the deck is already gone")
	}
	near("clearing coverage", c.Coverage, deck.Coverage*0.5)

	// Between two decks, every number is halfway.
	s = DefaultSky()
	base := DefaultSkyClouds()
	live := base
	s.Clouds = &live
	s.BeginTransition(4)
	s.Clouds.Altitude = base.Altitude + 100
	s.Clouds.Density = base.Density * 3
	s.TransitionElapsed = 1
	c, _ = s.EffectiveClouds()
	near("altitude a quarter of the way", c.Altitude, base.Altitude+25)
	near("density a quarter of the way", c.Density, base.Density*1.5)
}
