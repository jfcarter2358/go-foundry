package terrain

// landform_lobes_test.go — the exported lava lobes (Lobes, LobeMeta.Cover)
// outline exactly the tongues the heightfield raised (lavaLobe.at), wherever
// the provider is anchored — what the ground material's lava rule relies on
// (stages/terrain_material_cover.go, docs/regions/PRESETS_6.md).

import (
	"math"
	"testing"
)

func TestLobeMetaCoverIsTheRaisedLobesOutline(t *testing.T) {
	s := &Settings{Seed: 77}
	s.Landform.Volcano = Volcano{Count: 2, Height: 150, Radius: 220, Crater: 0.2, CraterDepth: 28,
		Lobes: 4, LobeLength: 260, LobeWidth: 45, LobeThickness: 5}
	over := Rect{MinX: 300, MinZ: -200, MaxX: 1300, MaxZ: 800}
	anchor := [2]float64{800, 300}
	lobes := Lobes(s, over, anchor)
	if len(lobes) != 8 {
		t.Fatalf("%d lobes, want 2 cones x 4", len(lobes))
	}
	// The shaper's own lobes, in provider coordinates.
	local := Rect{MinX: over.MinX - anchor[0], MinZ: over.MinZ - anchor[1], MaxX: over.MaxX - anchor[0], MaxZ: over.MaxZ - anchor[1]}
	raised := lavaLobes(s, placeVents(s, local))
	on := 0
	for k, l := range lobes {
		r := raised[k]
		// Walk a lattice round the lobe in world coordinates.
		for a := -60.0; a <= r.length+60; a += 7 {
			for b := -120.0; b <= 120; b += 6 {
				wx := l.X + a*l.DX - b*l.DZ
				wz := l.Z + a*l.DZ + b*l.DX
				got := l.Cover(wx, wz)
				want := r.at(wx-anchor[0], wz-anchor[1]) / r.thickness
				if math.Abs(got-want) > 1e-9 {
					t.Fatalf("lobe %d at (%.1f, %.1f): cover %.6f, raised %.6f", k, wx, wz, got, want)
				}
				if got > 0.9 {
					on++
				}
			}
		}
	}
	if on == 0 {
		t.Fatal("no lattice point lies on any lobe's top")
	}
	// No volcano, no lobes.
	if Lobes(&Settings{}, over, anchor) != nil {
		t.Error("lobes without a volcano")
	}
}
