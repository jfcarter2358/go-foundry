package nanitecook

// attrcap_test.go — Options.AttrRadiusCap: hashed only when set, and on a big
// mesh it makes the DAG's errors the surface's rather than its size's, so a
// cut at a given error takes fewer triangles (docs/canyon/PERF.md).

import (
	"testing"

	"foundry/internal/gfx"
)

func TestAttrRadiusCapIsHashedOnlyWhenSet(t *testing.T) {
	dir := t.TempDir()
	src := writeSampleGLB(t, dir, 2)
	plain, _ := HashSource([]string{src}, Options{})
	zero, _ := HashSource([]string{src}, Options{AttrRadiusCap: 0})
	capped, _ := HashSource([]string{src}, Options{AttrRadiusCap: 1})
	capped2, _ := HashSource([]string{src}, Options{AttrRadiusCap: 2})
	if plain != zero {
		t.Error("an unset cap changed the hash — every existing cook would recook")
	}
	if capped == plain || capped == capped2 {
		t.Error("the cap is not in the hash")
	}
}

func TestAttrRadiusCapBoundsTheWeights(t *testing.T) {
	pool := &gfx.IndexedMesh{Vertices: []float32{0, 0, 0, 0, 0, 40, 0, 0, 0, 0}} // a 40 m span: radius 20
	full := attrWeights(pool, Options{})
	capped := attrWeights(pool, Options{AttrRadiusCap: 1})
	for i := range full {
		if full[i] <= 0 || capped[i]*20 != full[i] {
			t.Errorf("weight %d: %g uncapped, %g capped at a metre of a 20 m radius", i, full[i], capped[i])
		}
	}
	small := &gfx.IndexedMesh{Vertices: []float32{0, 0, 0, 0, 0, 1, 0, 0, 0, 0}} // radius 0.5: under the cap
	a, b := attrWeights(small, Options{}), attrWeights(small, Options{AttrRadiusCap: 1})
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("weight %d of a mesh under the cap changed: %g → %g", i, a[i], b[i])
		}
	}
}

func TestAttrRadiusCapCoarsensABigBumpyMeshsCut(t *testing.T) {
	m := bumpyTile(48, 0, 64) // 64 m across, bumpy normals all over
	tris := func(opt Options) [4]int {
		c, err := CookMesh(m, "tile", opt)
		if err != nil {
			t.Fatal(err)
		}
		var n [4]int
		for k, th := range []float32{0.05, 0.1, 0.2, 0.4} {
			for i := range c.Clusters {
				if c.Clusters[i].Selected(th) {
					n[k] += int(c.Clusters[i].TriCount)
				}
			}
		}
		return n
	}
	full, capped := tris(Options{}), tris(Options{AttrRadiusCap: 1})
	t.Logf("cuts at 5/10/20/40 cm: %v triangles uncapped, %v capped", full, capped)
	for k := range full {
		if capped[k] > full[k] {
			t.Errorf("cut %d: capped took %d triangles, more than the uncapped %d", k, capped[k], full[k])
		}
	}
	if capped[3] >= full[3] {
		t.Errorf("capped, the 40 cm cut kept %d triangles of the uncapped %d", capped[3], full[3])
	}
}
