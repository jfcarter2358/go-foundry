package particle

// particle_waterfall_test.go — waterfall_mist.fpar, the spray ground_edit
// places at the foot of every waterfall (docs/terrain-paint/PHASE_W4.md),
// loads and runs: both layers make particles within their caps, the spray
// is prewarmed (a fall seen on arrival is already misting) and it names the
// shipped puff texture.

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/testenv"

	"github.com/go-gl/mathgl/mgl32"
)

func TestShippedWaterfallMistLoadsAndRuns(t *testing.T) {
	root := testenv.RepoRoot(t)
	d, err := LoadParticle(filepath.Join(root, "resources", "particles", "waterfall_mist.fpar"))
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != "waterfall_mist" || len(d.Layers) != 2 {
		t.Fatalf("id %q, %d layers", d.ID, len(d.Layers))
	}
	if !d.Layers[0].Prewarm {
		t.Error("the spray is not prewarmed")
	}
	for i := range d.Layers {
		l := d.Layers[i]
		if l.Texture != "particle_mist" {
			t.Errorf("layer %s texture %q", l.Name, l.Texture)
		}
		s := NewSystem(l, d.Gravity, d.Seed+uint32(i))
		for f := 0; f < 120; f++ {
			s.Step(1.0/60.0, mgl32.Ident4())
		}
		if s.Count() == 0 || s.Count() > l.EffectiveMax() {
			t.Errorf("layer %s: %d particles (cap %d)", l.Name, s.Count(), l.EffectiveMax())
		}
	}
	if _, err := os.Stat(filepath.Join(root, "resources", "textures", "particle_mist.png")); err != nil {
		t.Error(err)
	}
}
