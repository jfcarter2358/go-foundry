package paint

// region_test.go — the per-piece keys a per-tile rerun is decided by
// (RectHash), the changed-tile answers, and the falloff curves.

import (
	"math"
	"testing"
)

func TestRectHashFollowsOnlyTheTexelsInReach(t *testing.T) {
	l := NewLayer(LayerDef{Name: "m", Kind: KindDensity})
	// Nothing painted: every key is empty.
	if h := l.RectHash(0, 0, 64, 64); h != "" {
		t.Fatalf("empty layer key %q", h)
	}
	// A stroke in the middle of tile (1, 1).
	l.Apply(Stroke{X: 96, Z: 96, Radius: 6, Strength: 1})
	inTile := l.RectHash(64, 64, 128, 128)
	if inTile == "" {
		t.Fatal("the painted tile's own rectangle has no key")
	}
	// Its neighbours' rectangles read the tile's edge texels, which are
	// still zero: their keys stay empty.
	for _, r := range [][4]float64{{0, 64, 64, 128}, {128, 64, 192, 128}, {64, 0, 128, 64}, {0, 0, 64, 64}} {
		if h := l.RectHash(r[0], r[1], r[2], r[3]); h != "" {
			t.Errorf("rect %v sees a stroke 26 m past its edge (key %q)", r, h)
		}
	}
	// A stroke on the edge reaches the neighbour.
	before := l.RectHash(0, 64, 64, 128)
	l.Apply(Stroke{X: 64.2, Z: 90, Radius: 1, Strength: 1})
	if l.RectHash(0, 64, 64, 128) == before {
		t.Error("a stroke on the shared edge left the neighbour's key alone")
	}
	if l.RectHash(64, 64, 128, 128) == inTile {
		t.Error("a second stroke left the painted tile's key alone")
	}
	// A far stroke leaves this rectangle's key alone; erasing restores it.
	k0 := l.RectHash(64, 64, 128, 128)
	snap := l.Snapshot(l.TouchedBy(Stroke{X: 400, Z: 400, Radius: 5}))
	l.Apply(Stroke{X: 400, Z: 400, Radius: 5, Strength: 1})
	if l.RectHash(64, 64, 128, 128) != k0 {
		t.Error("a stroke 300 m away changed the key")
	}
	l.Restore(snap)
	if got := ChangedTiles(l, l.Clone()); len(got) != 0 {
		t.Errorf("a clone differs in %v", got)
	}
}

func TestChangedTilesAndSnapshots(t *testing.T) {
	def := LayerDef{Name: "m", Kind: KindDensity}
	a := NewLayer(def)
	a.Apply(Stroke{X: 10, Z: 10, Radius: 4, Strength: 1})
	b := a.Clone()
	s := Stroke{X: 70, Z: 10, Radius: 4, Strength: 1}
	keys := b.TouchedBy(s)
	before := b.Snapshot(keys)
	b.Apply(s)
	after := b.Snapshot(keys)
	got := ChangedTiles(a, b)
	if len(got) != 1 || got[0] != (TileKey{1, 0}) {
		t.Fatalf("changed %v, want [1_0]", got)
	}
	if sc := SnapshotChanged(def, before, after); len(sc) != 1 || sc[0] != (TileKey{1, 0}) {
		t.Fatalf("snapshot changed %v, want [1_0]", sc)
	}
	if got := ChangedTiles(nil, b); len(got) != 2 {
		t.Errorf("against nothing: %v", got)
	}
}

func TestFalloffCurves(t *testing.T) {
	for _, c := range []struct {
		curve Curve
		at    float64 // weight at the middle of the eased ring
	}{{CurveSmooth, 0.5}, {CurveLinear, 0.5}, {CurveSharp, 0.25}, {CurveDome, 0.75}} {
		s := Stroke{Radius: 10, Falloff: 1, Curve: c.curve}
		if w := s.weight(5); math.Abs(w-c.at) > 1e-9 {
			t.Errorf("%s: weight at half the radius %g, want %g", c.curve, w, c.at)
		}
		if w := s.weight(0); w != 1 {
			t.Errorf("%s: centre weight %g", c.curve, w)
		}
		if w := s.weight(10); w != 0 {
			t.Errorf("%s: rim weight %g", c.curve, w)
		}
		if got, ok := ParseCurve(c.curve.String()); !ok || got != c.curve {
			t.Errorf("curve %s does not round-trip", c.curve)
		}
	}
}
