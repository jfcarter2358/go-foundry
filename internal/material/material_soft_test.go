package material

import "testing"

// TestSoftLayersRoundTripAndAreFoundByThePacker — a splat layer's soft kind
// survives a write and a read, and the written text is what the release
// packer finds it by (SoftKindsIn), so the kind's step sound and puff ship.
func TestSoftLayersRoundTripAndAreFoundByThePacker(t *testing.T) {
	d := &Doc{Name: "soft_rt", Domain: DomainSurface}
	e := d.AddEffect(KindSplat)
	e.Set("weights", TextureValue("w"))
	e.Set(SplatLayerParam(0, ""), TextureValue("red_sand"))
	e.Set(SplatLayerParam(0, "soft"), EnumValue(SoftSand))
	e.Set(SplatLayerParam(5, ""), TextureValue("snow_02"))
	e.Set(SplatLayerParam(5, "soft"), EnumValue(SoftSnow))
	e.Set(SplatLayerParam(5, "soft_depth"), FloatValue(0.15))
	data, err := EncodeMaterial(d)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, k := range SoftKindsIn(string(data)) {
		kinds[k] = true
	}
	if !kinds[SoftSand] || !kinds[SoftSnow] || len(kinds) != 2 {
		t.Errorf("the packer finds %v in\n%s", kinds, data)
	}
	back, err := DecodeMaterial(data)
	if err != nil {
		t.Fatal(err)
	}
	s := ResolveSurfaceDoc(back)
	if s.Fx.SplatLayers[0].Soft != SoftSand {
		t.Errorf("layer 1's soft kind after a round trip: %q", s.Fx.SplatLayers[0].Soft)
	}
	if !s.Fx.HasSoft() {
		t.Errorf("a splat with a sand layer has no soft layer")
	}
	// Layer 6 is not drawn without weights2 (not eight-layer mode), so its
	// snow is not the splat's.
	if s.Fx.SplatLayers[5].Soft != "" {
		t.Errorf("a layer outside eight-layer mode kept its soft kind")
	}
	if d := SoftLayerDepth(&s.Fx.SplatLayers[0]); d != SoftKinds[SoftSand].Depth {
		t.Errorf("sand's depth %v, want the kind's %v", d, SoftKinds[SoftSand].Depth)
	}
}
