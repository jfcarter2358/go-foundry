package paint

// flatten_test.go — ModeFlatten brings the ground under a height layer to
// one absolute height.

import (
	"math"
	"testing"
)

func TestFlattenLevelsTheGroundUnderTheLayer(t *testing.T) {
	l := NewLayer(LayerDef{Name: "h", Kind: KindHeight})
	base := func(x, z float64) float64 { return 10 + 0.1*x } // a slope
	s := Stroke{X: 20, Z: 20, Radius: 6, Strength: 1, Mode: ModeFlatten, Value: 12, Base: base}
	l.Apply(s)
	for _, p := range [][2]float64{{20, 20}, {17, 21}, {23, 18}} {
		if got := base(p[0], p[1]) + l.Sample(p[0], p[1]); math.Abs(got-12) > 0.02 {
			t.Errorf("ground at %v: %.3f, want 12", p, got)
		}
	}
	// Outside the brush nothing is painted.
	if v := l.Sample(40, 40); v != 0 {
		t.Errorf("outside: %v", v)
	}
	// A partial strength moves part of the way; without a base, flatten
	// is set.
	m := NewLayer(LayerDef{Name: "m", Kind: KindHeight})
	m.Apply(Stroke{X: 0, Z: 0, Radius: 4, Strength: 0.5, Mode: ModeFlatten, Value: 2})
	if v := m.Sample(0, 0); math.Abs(v-1) > 0.01 {
		t.Errorf("half a flatten toward 2 with no base: %v", v)
	}
	if mm, ok := ParseMode("flatten"); !ok || mm != ModeFlatten || mm.String() != "flatten" {
		t.Errorf("ParseMode(flatten) = %v, %v", mm, ok)
	}
}
