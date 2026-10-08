package biomeground_test

import (
	"bytes"
	"testing"

	"foundry/internal/biomeground"
	"foundry/internal/world"
)

// Every built-in biome's ground has to be paintable: a project without the
// file falls back to this package, and one it could not paint would show
// only the biome's tint.
func TestEveryBuiltinBiomeGroundHasARecipe(t *testing.T) {
	for _, p := range world.FoliagePresets {
		if p.GroundTexture != "" && !biomeground.Has(p.GroundTexture) {
			t.Errorf("biome %s names ground %q, which has no recipe", p.Name, p.GroundTexture)
		}
	}
	if biomeground.Has("meadow") || biomeground.Has("ground_nonesuch") || biomeground.Image("ground_nonesuch") != nil {
		t.Fatal("a name with no recipe was accepted")
	}
}

func TestImageIsDeterministicAndOpaque(t *testing.T) {
	a, b := biomeground.Image("ground_desert"), biomeground.Image("ground_desert")
	if a == nil || a.Rect.Dx() != biomeground.Size || a.Rect.Dy() != biomeground.Size {
		t.Fatalf("got %v", a)
	}
	if !bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("the same ground painted twice came out different")
	}
	for i := 3; i < len(a.Pix); i += 4 {
		if a.Pix[i] != 255 {
			t.Fatalf("pixel %d has alpha %d", i/4, a.Pix[i])
		}
	}
}
