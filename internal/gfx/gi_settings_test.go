package gfx

import "testing"

func TestGIQualityNamesParseBack(t *testing.T) {
	for _, n := range GIQualityNames() {
		q, ok := ParseGIQuality(n)
		if !ok || q.String() != n {
			t.Errorf("%q parsed to %v (ok %v)", n, q, ok)
		}
	}
	if _, ok := ParseGIQuality("ultra"); ok {
		t.Error("an unknown tier parsed")
	}
}

func TestTheDefaultGIQualityFollowsTheGPU(t *testing.T) {
	prevQ, prevChosen := GIQuality, GIQualityChosen
	t.Cleanup(func() { GIQuality, GIQualityChosen = prevQ, prevChosen })
	for _, c := range []struct {
		renderer string
		want     GIQualityLevel
	}{
		{"NVIDIA GeForce GTX 1080/PCIe/SSE2", GIMedium},
		{"D3D12 (NVIDIA GeForce GTX 1080)", GIMedium},
		{"AMD Radeon RX 6800 XT", GIMedium},
		{"Mesa Intel(R) UHD Graphics 620 (KBL GT2)", GIOff},
		{"llvmpipe (LLVM 21.1.8, 256 bits)", GIOff},
		{"AMD Radeon(TM) Graphics", GIOff},
	} {
		GIQualityChosen = false
		PickDefaultGIQuality(c.renderer)
		if GIQuality != c.want {
			t.Errorf("%q picked %v, want %v", c.renderer, GIQuality, c.want)
		}
	}
}

func TestShortBakedLightRowsArePaddedToTheStride(t *testing.T) {
	out := deindexLight([][]float32{{1, 2, 3}, {1, 2, 3, 0.5}, {1, 2, 3, 0.5, 0.1, 0.2, 0.3}}, []int{0, 1, 2})
	if len(out) != 3*LightStride {
		t.Fatalf("%d floats for three corners, want %d", len(out), 3*LightStride)
	}
	if out[LightStride+3] != 0.5 || out[2*LightStride+6] != 0.3 || out[4] != 0 {
		t.Errorf("rows laid out as %v", out)
	}
}
