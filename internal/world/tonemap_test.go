package world

import (
	"testing"

	"foundry/internal/gfx"

	"gopkg.in/yaml.v2"
)

// A level without the block packs LDR in any frame; with it, HDR only in a
// scene-target frame; the block round-trips with its defaults left out.
func TestTonemapPacksOnlyInASceneFrame(t *testing.T) {
	defer func() { gfx.HDRFrame = false }()
	var b gfx.SceneBlock
	for _, frame := range []bool{false, true} {
		gfx.HDRFrame = frame
		PackFog(&b, nil, nil, &Level{}, [3]float32{})
		if b.HdrMode != 0 || b.HdrExposure != 1 {
			t.Fatalf("no block, scene frame %v: mode %d exposure %v", frame, b.HdrMode, b.HdrExposure)
		}
	}
	e := float32(1.4)
	lvl := &Level{Tonemap: TonemapFromRaw(&RawTonemap{Exposure: &e})}
	gfx.HDRFrame = false
	PackFog(&b, nil, nil, lvl, [3]float32{})
	if b.HdrMode != 0 {
		t.Fatalf("HDR outside a scene frame")
	}
	gfx.HDRFrame = true
	PackFog(&b, nil, nil, lvl, [3]float32{})
	if b.HdrMode != 1 || b.HdrExposure != 1.4 {
		t.Fatalf("scene frame: mode %d exposure %v", b.HdrMode, b.HdrExposure)
	}
	lvl.Tonemap.Enabled = false
	PackFog(&b, nil, nil, lvl, [3]float32{})
	if b.HdrMode != 0 {
		t.Fatalf("a switched-off block still shades HDR")
	}

	// Round trip.
	if TonemapToRaw(nil) != nil || TonemapFromRaw(nil) != nil {
		t.Fatal("no block must stay no block")
	}
	out, err := yaml.Marshal(TonemapToRaw(TonemapFromRaw(&RawTonemap{})))
	if err != nil || string(out) != "{}\n" {
		t.Fatalf("defaults written: %q %v", out, err)
	}
	out, _ = yaml.Marshal(TonemapToRaw(TonemapFromRaw(&RawTonemap{Exposure: &e, Disabled: true})))
	if string(out) != "disabled: true\nexposure: 1.4\n" {
		t.Fatalf("round trip: %q", out)
	}
}
