package particle

// particle_shipped_test.go — the effect that ships with the engine, actually
// run.
//
// A worked example is documentation, and documentation rots. This asserts that
// torch.fpar still parses, that every layer still produces particles, and that
// none of them exceeds the cap it declares — which between them catch a schema
// change that silently stopped reading a key.

import (
	"foundry/internal/testenv"
	"path/filepath"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func TestShippedTorchEffectLoadsAndRuns(t *testing.T) {
	path := filepath.Join(testenv.RepoRoot(t), "resources", "particles", "torch.fpar")
	d, err := LoadParticle(path)
	if err != nil {
		t.Fatalf("load torch.fpar: %v", err)
	}
	if len(d.Layers) != 3 {
		t.Fatalf("got %d layers, want 3 (flame, smoke, sparks)", len(d.Layers))
	}
	for i := range d.Layers {
		l := d.Layers[i]
		s := NewSystem(l, d.Gravity, d.Seed+uint32(i))
		for f := 0; f < 120; f++ { // two seconds
			s.Step(1.0/60.0, mgl32.Ident4())
		}
		if s.Count() == 0 {
			t.Errorf("layer %q produced no particles after two seconds", l.Name)
		}
		if s.Count() > l.EffectiveMax() {
			t.Errorf("layer %q exceeded its cap: %d > %d", l.Name, s.Count(), l.EffectiveMax())
		}
	}
}

// TestShippedTorchSurvivesASaveReload — the effect is also the round-trip
// fixture, so a schema change that drops a key shows up here as a difference
// rather than in a level nobody reopens for a month.
func TestShippedTorchSurvivesASaveReload(t *testing.T) {
	path := filepath.Join(testenv.RepoRoot(t), "resources", "particles", "torch.fpar")
	d, err := LoadParticle(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	back := rawToDoc(docToRaw(d))
	if len(back.Layers) != len(d.Layers) {
		t.Fatalf("layer count changed: %d -> %d", len(d.Layers), len(back.Layers))
	}
	for i := range d.Layers {
		a, b := d.Layers[i], back.Layers[i]
		if a.Name != b.Name || a.Rate != b.Rate || a.Blend != b.Blend ||
			a.Collision != b.Collision || len(a.ColorRamp) != len(b.ColorRamp) ||
			len(a.AlphaRamp) != len(b.AlphaRamp) || a.Gravity != b.Gravity {
			t.Errorf("layer %q changed across a round trip:\n got %+v\nwant %+v", a.Name, b, a)
		}
	}
}
