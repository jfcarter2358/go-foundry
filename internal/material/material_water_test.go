package material

import (
	"path/filepath"
	"testing"
)

// The water presets (resources/materials/water*.fmat; docs/terrain-paint/
// PHASE_W1.md) all read as water, and they are the looks their names say:
// the swamp far murkier than the alpine lake, the sea with a swell, the
// river drifting fastest.
func TestWaterPresetsAreWaterAndDiffer(t *testing.T) {
	dir := shippedMaterialsDir(t)
	read := func(name string) Water {
		d, err := ReadMaterial(filepath.Join(dir, name+MaterialExt))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		s := ResolveSurfaceDoc(d)
		if !s.HasWater {
			t.Fatalf("%s: no water effect", name)
		}
		return s.Water
	}
	all := map[string]Water{}
	for _, n := range []string{"water", "water_tropical", "water_alpine", "water_lake", "water_river", "water_swamp", "water_sea"} {
		all[n] = read(n)
	}
	if all["water"] != DefaultWater() {
		t.Errorf("water.fmat is meant to spell out the defaults: %+v", all["water"])
	}
	if all["water_swamp"].Absorption < 5*all["water_alpine"].Absorption {
		t.Errorf("swamp absorption %v vs alpine %v", all["water_swamp"].Absorption, all["water_alpine"].Absorption)
	}
	if all["water_sea"].Swell <= 0 {
		t.Error("the sea has no swell")
	}
	for n, w := range all {
		if n != "water_river" && w.FlowSpeed >= all["water_river"].FlowSpeed {
			t.Errorf("%s drifts as fast as the river", n)
		}
	}
}

// water_falls.fmat (phase W4, docs/terrain-paint/PHASE_W4.md) is a falling
// sheet: `falls` on, nothing that would displace or flow-map a vertical
// mesh; every other preset is plain water.
func TestWaterFallsPresetIsAFallingSheet(t *testing.T) {
	dir := shippedMaterialsDir(t)
	for _, n := range []string{"water_falls", "water", "water_river", "water_lake", "water_sea"} {
		d, err := ReadMaterial(filepath.Join(dir, n+MaterialExt))
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		w := ResolveSurfaceDoc(d).Water
		if n != "water_falls" {
			if w.Falls != 0 {
				t.Errorf("%s: falls %v", n, w.Falls)
			}
			continue
		}
		if w.Falls <= 0 || w.FallsSpeed <= 0 || w.WaveAmplitude != 0 || w.Swell != 0 || w.FlowMap != 0 {
			t.Errorf("water_falls %+v", w)
		}
	}
}
