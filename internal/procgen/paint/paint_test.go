package paint

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func density(name string) LayerDef { return LayerDef{Name: name, Kind: KindDensity} }

// TestStoredTilesRoundTripExactly paints every kind, saves, loads, and gets
// back the same samples and the same hashes.
func TestStoredTilesRoundTripExactly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "doc.paint")
	defs := []LayerDef{density("grass"), {Name: "lift", Kind: KindHeight, Resolution: 1},
		{Name: "flow", Kind: KindDirection}, {Name: "trail", Kind: KindMaterial, Resolution: 0.25}}
	s := NewSet()
	for _, d := range defs {
		s.Ensure(d)
	}
	s.Layer("grass").Apply(Stroke{X: 10, Z: -5, Radius: 12, Strength: 0.7, Falloff: 0.5})
	s.Layer("lift").Apply(Stroke{X: 70, Z: 70, Radius: 9, Strength: -2.3, Falloff: 0.3})
	s.Layer("flow").Apply(Stroke{X: -30, Z: 3, Radius: 6, Strength: 1, Dir: [2]float64{0.6, -0.8}})
	s.Layer("trail").ApplyLine(Stroke{Radius: 2, Strength: 1}, -20, -20, 20, 20)
	if err := s.Save(dir, defs); err != nil {
		t.Fatal(err)
	}
	back, err := Load(dir, defs)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		a, b := s.Layer(d.Name), back.Layer(d.Name)
		if a.Hash() != b.Hash() {
			t.Errorf("%s: hash changed through save and load", d.Name)
		}
		for _, k := range a.Keys() {
			ta, tb := a.Tile(k), b.Tile(k)
			if tb == nil {
				t.Fatalf("%s: tile %s missing after load", d.Name, k)
			}
			for i := range ta.V {
				if ta.V[i] != tb.V[i] {
					t.Fatalf("%s tile %s sample %d: %v became %v", d.Name, k, i, ta.V[i], tb.V[i])
				}
			}
		}
	}
	if v := back.Layer("lift").Sample(70, 70); math.Abs(v+2.3) > 0.01 {
		t.Errorf("height at the stamp's centre %.3f, want -2.3", v)
	}
	if d := back.Layer("flow").Direction(-30, 3); math.Abs(d[0]-0.6) > 0.02 || math.Abs(d[1]+0.8) > 0.02 {
		t.Errorf("direction %v, want (0.6, -0.8)", d)
	}
}

// TestUntouchedTilesStoreNothingAndErasedOnesAreDeleted: a stroke near the
// origin writes the four tiles round it, an erase takes them all away, and
// a save leaves no folder.
func TestUntouchedTilesStoreNothingAndErasedOnesAreDeleted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "doc.paint")
	defs := []LayerDef{density("grass")}
	s := NewSet()
	l := s.Ensure(defs[0])
	l.Apply(Stroke{X: 0, Z: 0, Radius: 5, Strength: 1})
	if got := l.Keys(); len(got) != 4 {
		t.Fatalf("a disc on the origin stored %d tiles (%v), want the 4 round it", len(got), got)
	}
	if err := s.Save(dir, defs); err != nil {
		t.Fatal(err)
	}
	files, _ := os.ReadDir(filepath.Join(dir, "grass"))
	if len(files) != 4 {
		t.Fatalf("%d files, want 4", len(files))
	}
	for i := 0; i < 40 && !l.Empty(); i++ {
		l.Apply(Stroke{X: 0, Z: 0, Radius: 8, Strength: 0.5, Mode: ModeErase})
	}
	if !l.Empty() {
		t.Fatalf("erasing left %v", l.Keys())
	}
	if err := s.Save(dir, defs); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the paint folder is still there after erasing everything (%v)", err)
	}
}

// TestSamplingIsBilinearAcrossTileEdges: a set ramp sampled between texel
// centres, on both sides of a tile edge.
func TestSamplingIsBilinearAcrossTileEdges(t *testing.T) {
	l := NewLayer(LayerDef{Name: "h", Kind: KindHeight, Resolution: 1})
	// Two texels either side of x = 64 (the edge of tiles 0 and 1).
	for _, p := range []struct{ x, v float64 }{{63.5, 2}, {64.5, 4}} {
		l.Apply(Stroke{X: p.x, Z: 10.5, Radius: 0.4, Strength: 1, Mode: ModeSet, Value: p.v})
	}
	if len(l.Keys()) != 2 {
		t.Fatalf("tiles %v, want two", l.Keys())
	}
	if v := l.Sample(64, 10.5); math.Abs(v-3) > 1e-6 {
		t.Errorf("midway across the edge %.4f, want 3", v)
	}
	if v := l.Sample(63.5, 10.5); math.Abs(v-2) > 1e-6 {
		t.Errorf("at a texel centre %.4f, want 2", v)
	}
	if v := l.Sample(63.5, 11); math.Abs(v-1) > 1e-6 {
		t.Errorf("half way to an unpainted row %.4f, want 1", v)
	}
	if v := l.Sample(-500, 3); v != 0 {
		t.Errorf("far from any paint %.4f, want 0", v)
	}
	var nilLayer *Layer
	if nilLayer.Sample(0, 0) != 0 {
		t.Error("a nil layer samples non-zero")
	}
}

// TestStrokeModes: add saturates, set reaches its value, smooth spreads a
// spike, falloff fades the rim, and erase reaches zero.
func TestStrokeModes(t *testing.T) {
	l := NewLayer(density("d"))
	for i := 0; i < 5; i++ {
		l.Apply(Stroke{X: 5, Z: 5, Radius: 3, Strength: 0.4})
	}
	if v := l.Sample(5.25, 5.25); v != 1 {
		t.Errorf("five adds of 0.4: %.3f, want saturated 1", v)
	}
	soft := NewLayer(density("s"))
	soft.Apply(Stroke{X: 5, Z: 5, Radius: 4, Strength: 1, Falloff: 1})
	if c, r := soft.Sample(5.25, 5.25), soft.Sample(8.25, 5.25); !(c > 0.9 && r < c && r > 0) {
		t.Errorf("full falloff: centre %.3f, near the rim %.3f — want a fade", c, r)
	}
	set := NewLayer(density("v"))
	set.Apply(Stroke{X: 5, Z: 5, Radius: 3, Strength: 1, Mode: ModeSet, Value: 0.4})
	if v := set.Sample(5.25, 5.25); math.Abs(v-0.4) > 0.003 {
		t.Errorf("set 0.4: %.3f", v)
	}
	sm := NewLayer(density("m"))
	sm.Apply(Stroke{X: 5.25, Z: 5.25, Radius: 0.3, Strength: 1})
	sm.Apply(Stroke{X: 5.25, Z: 5.25, Radius: 2, Strength: 1, Mode: ModeSmooth})
	if c, n := sm.Sample(5.25, 5.25), sm.Sample(5.75, 5.25); !(c < 0.5 && n > 0) {
		t.Errorf("smooth: spike %.3f, neighbour %.3f — want it spread", c, n)
	}
}

// TestHashesFollowContent: painting changes the hash, painting another
// layer does not, and undoing a stroke through Snapshot/Restore brings the
// old hash back.
func TestHashesFollowContent(t *testing.T) {
	s := NewSet()
	a, b := s.Ensure(density("a")), s.Ensure(density("b"))
	ha0, hb0 := a.Hash(), b.Hash()
	st := Stroke{X: 100, Z: 30, Radius: 4, Strength: 0.5}
	snap := a.Snapshot(a.TouchedBy(st))
	a.Apply(st)
	if a.Hash() == ha0 {
		t.Error("painting a left its hash alone")
	}
	if b.Hash() != hb0 {
		t.Error("painting a changed b's hash")
	}
	a.Restore(snap)
	if a.Hash() != ha0 || !a.Empty() {
		t.Error("restoring the snapshot did not bring a back")
	}
}

// TestCloneIsCopyOnWrite: a clone keeps what it saw while the original
// goes on painting, and the reverse.
func TestCloneIsCopyOnWrite(t *testing.T) {
	s := NewSet()
	l := s.Ensure(density("g"))
	l.Apply(Stroke{X: 10, Z: 10, Radius: 3, Strength: 0.5})
	snap := s.Clone()
	h := snap.Layer("g").Hash()
	l.Apply(Stroke{X: 10, Z: 10, Radius: 3, Strength: 0.5})
	if snap.Layer("g").Hash() != h {
		t.Error("painting the original changed the clone")
	}
	snap.Layer("g").Apply(Stroke{X: 40, Z: 40, Radius: 3, Strength: 1, Mode: ModeErase})
	if l.Sample(10.25, 10.25) != 1 {
		t.Errorf("original %.3f after two 0.5 adds", l.Sample(10.25, 10.25))
	}
}

func TestLayerDefValidate(t *testing.T) {
	for _, d := range []LayerDef{{Name: "Bad", Kind: KindDensity}, {Name: "x", Kind: "paint"},
		{Name: "x", Kind: KindDensity, Resolution: 0.3}, {Name: "x", Kind: KindDensity, Resolution: 16}} {
		if d.Validate() == nil {
			t.Errorf("%+v validated", d)
		}
	}
	if err := (LayerDef{Name: "ok_1", Kind: KindWater, Resolution: 2}).Validate(); err != nil {
		t.Error(err)
	}
}

func TestTilesOverlapping(t *testing.T) {
	got := TilesOverlapping(0, 0, 128, 64)
	if len(got) != 2 || got[0] != (TileKey{0, 0}) || got[1] != (TileKey{1, 0}) {
		t.Errorf("a 128×64 m terrain tile at the origin: %v, want tiles 0_0 and 1_0", got)
	}
	if k := TileAt(-0.1, 64); k != (TileKey{-1, 1}) {
		t.Errorf("TileAt %v", k)
	}
}
