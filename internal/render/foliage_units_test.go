package render

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// The pool hands out spans that never overlap and takes them back whole:
// freed neighbours merge, so the free list after everything is returned is
// one span as big as the pool.
func TestFoliagePoolSpansMergeBack(t *testing.T) {
	p := foliagePool{cap: 100, spans: []poolSpan{{0, 100}}}
	a, b, c := p.alloc(10), p.alloc(20), p.alloc(30)
	if a != 0 || b != 10 || c != 30 {
		t.Fatalf("spans at %d, %d, %d", a, b, c)
	}
	if p.used() != 60 {
		t.Fatalf("used %d", p.used())
	}
	p.free(b, 20)
	if d := p.alloc(15); d != 10 {
		t.Fatalf("a freed gap was not reused: %d", d)
	}
	p.free(10, 15)
	p.free(a, 10)
	p.free(c, 30)
	if len(p.spans) != 1 || p.spans[0] != (poolSpan{0, 100}) {
		t.Fatalf("free list did not merge back: %v", p.spans)
	}
}

// The unit tree covers every section exactly once at every level, and a
// cluster's box holds its members'.
func TestFoliageUnitTreeCoversEverySection(t *testing.T) {
	tr := world.NewTerrain(mgl32.Vec3{}, 1, 150, 20, 90)
	tr.Generate(world.TerrainFill{Height: 6, Hills: 3, Top: 1, Under: 1, Deep: 1})
	secs := tr.Sections()
	var g foliageTerrainGPU
	g.rebuildTree(secs)
	for k := 1; k <= foliageClusterLevels; k++ {
		seen := make([]int, len(secs))
		for _, u := range g.level(k) {
			for _, s := range u.secs {
				seen[s]++
				if !secs[s].Empty() {
					for a := 0; a < 3; a++ {
						if secs[s].Lo[a] < u.meshLo[a] || secs[s].Hi[a] > u.meshHi[a] {
							t.Fatalf("level %d cluster box misses section %d", k, s)
						}
					}
				}
			}
		}
		for s, n := range seen {
			if n != 1 {
				t.Fatalf("level %d holds section %d %d times", k, s, n)
			}
		}
	}
	if len(g.tops) != len(g.level(foliageClusterLevels)) {
		t.Fatal("tops are not the top level")
	}
}

// What a unit is built with at distance b still holds every instance any
// distance past b can draw: the rank cut is never below what the thinning
// keeps further out, and a shape out of range there is not built at all.
func TestFoliageCutKeepsEverythingDrawnFurtherOut(t *testing.T) {
	foliage.updateParams()
	for s := world.FoliageShape(0); s < world.FoliageShapeCount; s++ {
		p := foliage.params[s]
		for _, b := range []float32{10, 40, 150, 500} {
			cut := cutFor(b)(s)
			if b >= p.rng {
				if cut != 0 {
					t.Fatalf("%v: built at %v, past its range %v, with cut %v", s, b, p.rng, cut)
				}
				continue
			}
			for _, d := range []float32{b, b * 1.5, b * 3} {
				if k := foliageKeep(p, d); k > cut {
					t.Fatalf("%v built at %v: keeps %v at %v but was cut at %v", s, b, k, d, cut)
				}
			}
		}
	}
}

// Every shape's far tier is past its far mesh, which is past nothing, so the
// tier windows are in order and each cross-fade band has room.
func TestFoliageTiersAreInOrder(t *testing.T) {
	for s, in := range foliageShapes {
		if in.lod <= 0 || in.cpuLod <= 0 {
			t.Errorf("%v has no far-mesh distance", world.FoliageShape(s))
		}
		if in.far > 0 && (in.far*0.88 <= in.lod || in.far*0.88 <= in.cpuLod) {
			t.Errorf("%v: far tier at %v is inside its mesh band (lod %v, cpu %v)", world.FoliageShape(s), in.far, in.lod, in.cpuLod)
		}
		if in.rng <= in.far || in.rng <= in.lod {
			t.Errorf("%v: range %v ends before its tiers", world.FoliageShape(s), in.rng)
		}
	}
}
