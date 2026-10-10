package gfx

// geometry_normal_color_test.go — which surfaces the shader paints from their
// own normal. The rule is "names no texture", not "names one that failed to
// load": a missing image is a content error worth seeing, while an unpainted
// brush is the ordinary state of a blockout.

import "testing"

func TestNormalColorFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		tex  string
		want int32
	}{
		{"unpainted", "", 1},
		{"painted", "concrete", 0},
		{"painted with a texture that is not loaded", "not_a_real_texture", 0},
	} {
		g := Geometry{Texture: tc.tex}
		if got := g.NormalColorFlag(); got != tc.want {
			t.Errorf("%s: NormalColorFlag() = %d, want %d", tc.name, got, tc.want)
		}
	}
}
