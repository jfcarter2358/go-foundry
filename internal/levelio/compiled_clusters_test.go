package levelio

// compiled_clusters_test.go — the compiler's cut of a level's draw groups into
// clusters (world/static_clusters.go), Phase 1 of the Nanite plan: it is made,
// it survives the .fbsp round trip, a load installs it, and every group of the
// loaded surfaces finds its stored cut — so a compiled level is never cut again
// at load — while a cut that no longer matches is recut rather than trusted.

import (
	"path/filepath"
	"testing"

	"foundry/internal/testenv"
	"foundry/internal/world"
)

func TestTheCompilerCutsEveryGroupAndALoadUsesIt(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	c := compileFixture(t)
	if len(c.Clusters) == 0 {
		t.Fatal("the compile stored no clusters")
	}
	path := filepath.Join(t.TempDir(), "test"+CompiledExt)
	if err := SaveCompiledLevel(c, path); err != nil {
		t.Fatal(err)
	}
	back, err := ReadCompiledLevel(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Clusters) != len(c.Clusters) {
		t.Fatalf("%d of %d cut groups came back", len(back.Clusters), len(c.Clusters))
	}
	static, err := StaticGeometryFrom(back)
	if err != nil {
		t.Fatal(err)
	}
	groups, _ := world.StaticGroups(static)
	if len(groups) != len(back.Clusters) {
		t.Fatalf("%d groups load, %d were cut", len(groups), len(back.Clusters))
	}
	tris := 0
	for gi := range groups {
		cg, stored := static.StaticClustersFor(&groups[gi])
		if !stored {
			t.Fatalf("group %d (%s) was recut at load", gi, groups[gi].Key.Chunk)
		}
		// The cut covers the group's triangles, each once.
		n := 0
		for _, si := range groups[gi].Surfaces {
			n += len(static.Surfaces[si].Geometry.Vertices) / 15
		}
		if len(cg.Order) != n {
			t.Fatalf("group %d: cut of %d triangles, group has %d", gi, len(cg.Order), n)
		}
		seen := make([]bool, n)
		for _, tr := range cg.Order {
			if seen[tr] {
				t.Fatalf("group %d: triangle %d placed twice", gi, tr)
			}
			seen[tr] = true
		}
		tris += n
	}
	if tris == 0 {
		t.Fatal("the fixture has no drawable triangles")
	}

	// A cut that no longer matches its group is recut, not trusted.
	static.Clusters[0].Sig ^= 1
	static.SetClusters(static.Clusters)
	g0 := world.StaticGroupKey{}
	for gi := range groups {
		if groups[gi].Key.ID() == static.Clusters[0].Key {
			g0 = groups[gi].Key
			if _, stored := static.StaticClustersFor(&groups[gi]); stored {
				t.Fatal("a cut with the wrong signature was trusted")
			}
		}
	}
	if g0 == (world.StaticGroupKey{}) {
		t.Fatal("the first stored cut names no group")
	}
}
