package levelio

// compiled_lod_test.go — Phase 2 of the Nanite plan: the compiler gives a
// level's large opaque draw groups a cluster DAG (world/static_clusters.go),
// it survives the .fbsp, and a load uses it.

import (
	"path/filepath"
	"testing"

	"foundry/internal/compile"
	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func TestTheCompilerGivesLargeGroupsALODDAGThatSurvivesTheFile(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	prev := world.StaticLOD
	world.StaticLOD = true // whatever FOUNDRY_NANITE_LOD_STATIC says
	t.Cleanup(func() { world.StaticLOD = prev })
	// A hall big enough that the bake's grid makes its floor and walls
	// groups of hundreds of faces, lit by a lamp so their light varies.
	lvl := &world.Level{
		Chunks: []world.Chunk{{ID: "hall", Scale: 1,
			Brushes: world.NewHollowRoom("hall", mgl32.Vec3{-20, 0, -20}, mgl32.Vec3{20, 6, 20}, 0.5, "concrete")}},
		Spawn: &world.SpawnPoint{Position: mgl32.Vec3{0, 1, 0}},
		Lights: world.LightSet{Global: world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.3},
			Points: []world.PointLight{{Position: mgl32.Vec3{-8, 4, 5}, Color: mgl32.Vec3{1, 0.9, 0.7}, Brightness: 2, Radius: 14}}},
	}
	c, err := CompileLevel(lvl, compile.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	dags := 0
	for _, cg := range c.Clusters {
		if cg.DAG != nil {
			dags++
		}
	}
	if dags == 0 {
		t.Fatal("no group of the compiled fixture got a DAG")
	}
	path := filepath.Join(t.TempDir(), "test"+CompiledExt)
	if err := SaveCompiledLevel(c, path); err != nil {
		t.Fatal(err)
	}
	back, err := ReadCompiledLevel(path)
	if err != nil {
		t.Fatal(err)
	}
	static, err := StaticGeometryFrom(back)
	if err != nil {
		t.Fatal(err)
	}
	groups, _ := world.StaticGroups(static)
	found := 0
	for gi := range groups {
		cg, stored := static.StaticClustersFor(&groups[gi])
		if !stored {
			t.Fatalf("group %d was recut at load", gi)
		}
		d := cg.DAG
		if d == nil {
			continue
		}
		found++
		corners := 0
		for _, si := range groups[gi].Surfaces {
			corners += len(static.Surfaces[si].Geometry.Vertices) / 5
		}
		if d.Triangles()*3 != corners {
			t.Fatalf("group %d: the DAG's level 0 has %d triangles of the group's %d", gi, d.Triangles(), corners/3)
		}
		seen := make([]bool, d.Triangles())
		for _, tr := range d.BaseOrder {
			if seen[tr] {
				t.Fatalf("group %d: triangle %d placed twice", gi, tr)
			}
			seen[tr] = true
		}
		for _, ix := range d.Indices {
			if int(ix) >= corners {
				t.Fatalf("group %d: index %d past the %d corners", gi, ix, corners)
			}
		}
		if len(d.ClusterLeaf) != len(d.Clusters) || len(d.Nodes) == 0 || d.Levels < 2 {
			t.Fatalf("group %d: DAG without its tree", gi)
		}
	}
	if found != dags {
		t.Fatalf("%d DAGs were compiled, %d came back", dags, found)
	}
	t.Logf("%d of %d groups have a DAG", dags, len(c.Clusters))
}
