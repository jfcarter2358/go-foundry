package gfx

import "testing"

// A glow's soft edge lives in its texture's alpha, which only the blended pass
// honours — and a PNG's alpha is never recorded — so unlit means blended.
func TestUnlitGeometryDrawsInTheBlendedPass(t *testing.T) {
	g := Geometry{Texture: "unlit_test_png", Alpha: 1}
	if g.IsTransparent() {
		t.Fatal("an opaque lit surface went to the blended pass")
	}
	g.Unlit = true
	if !g.IsTransparent() {
		t.Fatal("an unlit surface stayed in the opaque pass")
	}
	if g.UnlitFlag() != 1 {
		t.Fatal("unlit flag not raised for the shader")
	}
}
