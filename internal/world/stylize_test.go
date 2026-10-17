package world

import (
	"testing"

	"foundry/internal/gfx"

	"gopkg.in/yaml.v2"
)

// A fresh block writes nothing but itself; a changed number and a deliberate
// zero survive the round trip; switching off keeps the numbers; out-of-range
// numbers come back clamped.
func TestStylizeRoundTrip(t *testing.T) {
	d := DefaultStylize()
	if r := StylizeToRaw(&d); *r != (RawStylize{}) {
		t.Fatalf("defaults should write an empty block, got %+v", r)
	}
	s := DefaultStylize()
	s.Enabled = false
	s.Wash = 0
	s.EdgeWidth = 3
	s.ShadowColor[2] = 0.9
	out, err := yaml.Marshal(StylizeToRaw(&s))
	if err != nil {
		t.Fatal(err)
	}
	var r RawStylize
	if err := yaml.Unmarshal(out, &r); err != nil {
		t.Fatal(err)
	}
	got := StylizeFromRaw(&r)
	if *got != s {
		t.Fatalf("round trip: got %+v\nwant %+v\nyaml:\n%s", *got, s, out)
	}
	if StylizeFromRaw(nil) != nil || StylizeToRaw(nil) != nil {
		t.Fatal("no block must stay no block")
	}
	big := float32(50)
	neg := float32(-1)
	c := StylizeFromRaw(&RawStylize{Edge: &big, Wash: &neg, LightPoint: &neg})
	if c.Edge != 2 || c.Wash != 0 || c.LightPoint <= c.ShadowPoint {
		t.Fatalf("clamp: edge %v wash %v points %v/%v", c.Edge, c.Wash, c.ShadowPoint, c.LightPoint)
	}
}

// The ramp is packed for a level whose look is on, and zero — off — for one
// without a block or with it switched off.
func TestStylizePacksTheRamp(t *testing.T) {
	var b gfx.SceneBlock
	lvl := &Level{}
	PackFog(&b, nil, nil, lvl, [3]float32{})
	if b.StyRamp[3] != 0 {
		t.Fatal("no block: ramp should be off")
	}
	s := DefaultStylize()
	lvl.Stylize = &s
	PackFog(&b, nil, nil, lvl, [3]float32{})
	if b.StyRamp != [4]float32{s.ShadowPoint, s.LightPoint, s.ShadowFill, 1} ||
		b.StyShadow[3] != s.ShadowTint || b.StyHighlight[0] != s.HighlightColor[0] {
		t.Fatalf("on: ramp %v shadow %v highlight %v", b.StyRamp, b.StyShadow, b.StyHighlight)
	}
	s.Enabled = false
	PackFog(&b, nil, nil, lvl, [3]float32{})
	if b.StyRamp != ([4]float32{}) || b.StyShadow != ([4]float32{}) {
		t.Fatal("switched off: ramp should be zero")
	}
}
