package material

// material_shipped_test.go — the materials that ship with the engine.
//
// They are documentation as much as content: each one is the worked example of
// an effect that used to be a field somewhere else. A shipped material that
// does not parse, names a parameter that does not exist, or carries an effect
// its domain cannot use is a broken example, and the person who finds it is
// someone learning the format.
//
// It also pins the ported DEFAULTS. water.fmat spells out the numbers
// gfx.DefaultWaterParams has, so that a water sector converted to a material
// looks the same; if either side is revised without the other, this says so.

import (
	"os"
	"path/filepath"
	"testing"
)

// repoRoot walks up from the test's working directory to the go.mod.
//
// internal/testenv does this already and cannot be used here: it imports
// internal/gfx, which imports THIS package, and a test binary may not close
// that loop. Six lines is a cheaper answer than moving the helper.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
}

func shippedMaterialsDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "resources", "materials")
}

func TestEveryShippedMaterialLoadsAndValidates(t *testing.T) {
	dir := shippedMaterialsDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	found := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != MaterialExt {
			continue
		}
		found++
		path := filepath.Join(dir, e.Name())
		d, err := ReadMaterial(path)
		if err != nil {
			t.Errorf("%s: %v", e.Name(), err)
			continue
		}
		if len(d.Effects) == 0 {
			t.Errorf("%s: no effects — a material that does nothing is not an example", e.Name())
		}
		for _, p := range d.Validate() {
			t.Errorf("%s: %s", e.Name(), p)
		}
		if d.Doc == "" {
			t.Errorf("%s: no doc: line; the picker and the editor both show it", e.Name())
		}
	}
	if found < 10 {
		t.Errorf("only %d shipped materials — the library is meant to cover every ported effect", found)
	}
}

func TestShippedMaterialsSurviveARoundTrip(t *testing.T) {
	// Opening one in the editor and saving it without touching anything must
	// not change it in a way that loses meaning. The bytes may differ — the
	// files carry comments, which YAML does not round-trip — but what they
	// RESOLVE to must not.
	dir := shippedMaterialsDir(t)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != MaterialExt {
			continue
		}
		d, err := ReadMaterial(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // reported by the test above
		}
		data, err := EncodeMaterial(d)
		if err != nil {
			t.Errorf("%s: encode: %v", e.Name(), err)
			continue
		}
		back, err := DecodeMaterial(data)
		if err != nil {
			t.Errorf("%s: re-decode: %v", e.Name(), err)
			continue
		}
		if back.Domain != d.Domain || back.Texture != d.Texture {
			t.Errorf("%s: header changed on round trip", e.Name())
		}
		if len(back.Effects) != len(d.Effects) {
			t.Errorf("%s: %d effects in, %d out", e.Name(), len(d.Effects), len(back.Effects))
			continue
		}
		for i := range d.Effects {
			if !sameEffect(&d.Effects[i], &back.Effects[i]) {
				t.Errorf("%s: effect %q changed on round trip", e.Name(), d.Effects[i].Kind)
			}
		}
	}
}

// sameEffect compares what two effects RESOLVE to, parameter by parameter
// against their kind's schema — not their Params maps, since one may have
// dropped a parameter that equalled its default and the other not.
func sameEffect(a, b *Effect) bool {
	if a.Kind != b.Kind || a.Disabled != b.Disabled {
		return false
	}
	k := KindByName(a.Kind)
	if k == nil {
		return len(a.Params) == len(b.Params)
	}
	for i := range k.Params {
		if !valuesEqual(a.Param(k.Params[i].Name), b.Param(k.Params[i].Name)) {
			return false
		}
	}
	return true
}

func TestWaterMaterialMatchesTheEngineDefaults(t *testing.T) {
	// The port's contract: a water sector turned into a material is the same
	// water. gfx.DefaultWaterParams is the other side of this and cannot be
	// imported here (gfx imports this package), so the numbers are restated —
	// which is the point, since a silent drift is what this catches.
	d, err := ReadMaterial(filepath.Join(shippedMaterialsDir(t), "water"+MaterialExt))
	if err != nil {
		t.Fatal(err)
	}
	s := ResolveSurfaceDoc(d)
	if !s.HasWater {
		t.Fatal("water.fmat does not resolve as water")
	}
	// Since W1 (docs/terrain-paint/PHASE_W1.md) both sides read one value,
	// DefaultWater; water.fmat spells it out.
	if s.Water != DefaultWater() {
		t.Errorf("water.fmat no longer matches DefaultWater: %+v", s.Water)
	}
}

func TestEveryEffectKindHasAShippedExample(t *testing.T) {
	// The library is the documentation. A kind nothing demonstrates is a kind
	// an author has to discover from the Add menu and guess at.
	dir := shippedMaterialsDir(t)
	entries, _ := os.ReadDir(dir)
	used := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != MaterialExt {
			continue
		}
		d, err := ReadMaterial(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		for i := range d.Effects {
			used[d.Effects[i].Kind] = true
		}
	}
	for _, k := range AllKinds() {
		if !used[k.Name] {
			t.Errorf("no shipped material uses the %q effect", k.Name)
		}
	}
}
