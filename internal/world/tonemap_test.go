package world

import (
	"testing"

	"foundry/internal/gfx"

	"gopkg.in/yaml.v2"
)

// A level without the block packs HDR at the metered exposure in a scene
// frame (LDR outside one, or with HDRByDefault off); with a fixed exposure,
// HDR at it only in a scene-target frame; the block round-trips with its
// defaults left out.
func TestTonemapPacksOnlyInASceneFrame(t *testing.T) {
	prevAuto, prevDefault := gfx.AutoExposure, gfx.HDRByDefault
	defer func() { gfx.HDRFrame, gfx.AutoExposure, gfx.HDRByDefault = false, prevAuto, prevDefault }()
	gfx.AutoExposure, gfx.HDRByDefault = 0.6, true
	var b gfx.SceneBlock
	for _, frame := range []bool{false, true} {
		gfx.HDRFrame = frame
		PackFog(&b, nil, nil, &Level{}, [3]float32{})
		wantMode, wantE := int32(0), float32(1)
		if frame {
			wantMode, wantE = 1, 0.6
		}
		if b.HdrMode != wantMode || b.HdrExposure != wantE {
			t.Fatalf("no block, scene frame %v: mode %d exposure %v", frame, b.HdrMode, b.HdrExposure)
		}
	}
	gfx.HDRByDefault = false
	gfx.HDRFrame = true
	PackFog(&b, nil, nil, &Level{}, [3]float32{})
	if b.HdrMode != 0 {
		t.Fatalf("HDRByDefault off: a level without the block still shades HDR")
	}
	gfx.HDRByDefault = true
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

	// An exposure with auto on is a compensation on the metered one.
	on := true
	auto := &Level{Tonemap: TonemapFromRaw(&RawTonemap{Exposure: &e, Auto: &on})}
	PackFog(&b, nil, nil, auto, [3]float32{})
	if d := b.HdrExposure - 1.4*0.6; b.HdrMode != 1 || d > 1e-6 || d < -1e-6 {
		t.Fatalf("auto with compensation: mode %d exposure %v", b.HdrMode, b.HdrExposure)
	}
	out, _ = yaml.Marshal(TonemapToRaw(auto.Tonemap))
	if string(out) != "exposure: 1.4\nauto: true\n" {
		t.Fatalf("auto round trip: %q", out)
	}
}
