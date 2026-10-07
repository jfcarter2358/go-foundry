package world

import (
	"math/rand"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func facesByKey(t *Terrain) map[int32]TerrainFace {
	out := map[int32]TerrainFace{}
	for _, s := range t.Sections() {
		for _, f := range s.Faces {
			out[f.Key] = f
		}
	}
	return out
}

func faceInBox(f TerrainFace, lo, hi mgl32.Vec3) bool {
	for _, p := range f.Corners {
		for a := 0; a < 3; a++ {
			if p[a] < lo[a] || p[a] > hi[a] {
				return false
			}
		}
	}
	return true
}

// Every face an edit adds, removes or changes in any way lies inside the box
// SurfaceChangedSince reports for it, across several edits in a row.
func TestTerrainSurfaceChangedSinceCoversEveryChangedFace(t *testing.T) {
	eachMode(t, func(t *testing.T, smooth bool) {
		tr := hillyTerrain(smooth)
		tr.Biomes = []TerrainBiome{{Preset: "forest", GroundLayer: 4}}
		tr.Touch()
		r := rand.New(rand.NewSource(5))
		for round := 0; round < 12; round++ {
			before := facesByKey(tr)
			gen := tr.Generation
			for k := 0; k <= round%3; k++ {
				x, y, z := r.Intn(tr.SizeX), r.Intn(tr.SizeY), r.Intn(tr.SizeZ)
				switch r.Intn(3) {
				case 0:
					tr.SetBlock(x, y, z, uint8(r.Intn(5)))
				case 1:
					tr.FillBox([3]int{x, y, z}, [3]int{x + 2, y + 1, z + 3}, uint8(r.Intn(2)))
				case 2:
					for key := range before {
						tr.SetPaint(key, 1)
						break
					}
				}
			}
			after := facesByKey(tr)
			lo, hi, changed, ok := tr.SurfaceChangedSince(gen)
			if tr.Generation == gen {
				if changed || !ok {
					t.Fatalf("round %d: nothing edited, but changed=%v ok=%v", round, changed, ok)
				}
				continue
			}
			if !ok || !changed {
				t.Fatalf("round %d: edits through SetBlock/FillBox/SetPaint must be boxed (changed=%v ok=%v)", round, changed, ok)
			}
			for key, f := range after {
				if g, had := before[key]; (!had || g != f) && !faceInBox(f, lo, hi) {
					t.Fatalf("round %d: face %d changed outside %v..%v", round, key, lo, hi)
				}
			}
			for key, f := range before {
				if _, has := after[key]; !has && !faceInBox(f, lo, hi) {
					t.Fatalf("round %d: face %d vanished outside %v..%v", round, key, lo, hi)
				}
			}
			// Nothing since now.
			if _, _, c, ok := tr.SurfaceChangedSince(tr.Generation); c || !ok {
				t.Fatalf("round %d: the current generation reports a change", round)
			}
		}
	})
}

// What the history cannot vouch for, it says so about.
func TestTerrainSurfaceChangedSinceGivesUp(t *testing.T) {
	tr := hillyTerrain(true)
	gen := tr.Generation
	tr.Touch()
	if _, _, _, ok := tr.SurfaceChangedSince(gen); ok {
		t.Error("a whole-terrain Touch was boxed")
	}

	gen = tr.Generation
	tr.Smooth = false
	tr.SetBlock(3, 3, 3, 0)
	if _, _, _, ok := tr.SurfaceChangedSince(gen); ok {
		t.Error("an edit after the smooth view was switched off was boxed")
	}

	gen = tr.Generation
	for i := 0; i < terrainEditHistory+1; i++ {
		x := i % tr.SizeX
		if !tr.SetBlock(x, 10, 4, 1-min(tr.Block(x, 10, 4), 1)) {
			t.Fatal("a toggle changed nothing")
		}
	}
	if _, _, _, ok := tr.SurfaceChangedSince(gen); ok {
		t.Error("an edit dropped from the history was boxed")
	}
	recent := tr.Generation
	tr.SetBlock(7, 11, 7, 2)
	if _, _, c, ok := tr.SurfaceChangedSince(recent); !ok || !c {
		t.Error("the latest edit was not boxed")
	}

	cl := tr.Clone()
	if _, _, _, ok := cl.SurfaceChangedSince(recent); ok {
		t.Error("a clone vouched for its source's history")
	}
}
