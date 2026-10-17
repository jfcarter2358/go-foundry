package stages

// rocks_bench_test.go — the numbers in docs/canyon/PHASE_2.md: each preset
// at a few triangle budgets, made, decimated, baked and cooked as the stage
// does, with the cooked file's size. Off unless asked:
//
//	FOUNDRY_ROCK_BENCH=1 go test -run TestRocksBench -timeout 60m -v ./internal/procgen/stages/

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"foundry/internal/procgen/meshout"
	"foundry/internal/procgen/rocks"
)

func TestRocksBench(t *testing.T) {
	if os.Getenv("FOUNDRY_ROCK_BENCH") == "" {
		t.Skip("FOUNDRY_ROCK_BENCH not set")
	}
	cases := []struct {
		preset string
		tris   []int
	}{
		{"pebble", []int{800}},
		{"rubble", []int{3000}},
		{"boulder", []int{5000, 15000, 50000, 150000}},
		{"ledge_slab", []int{60000}},
		{"mesa_cap", []int{300000}},
		{"pillar", []int{300000}},
		{"cliff_chunk", []int{150000, 600000, 1200000}},
	}
	dir := t.TempDir()
	t.Logf("| preset | budget | triangles | meshed | cell | mesh | decimate | bake | cook | .fnm | collision |")
	t.Logf("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |")
	for _, c := range cases {
		for _, n := range c.tris {
			s := rocks.Defaults()
			s.Preset, s.Triangles = c.preset, n
			r, err := rocks.Generate(s, 99)
			if err != nil {
				t.Fatal(err)
			}
			coll := rockCollision("auto", c.preset, r.Meta.Extent, 4)
			out := filepath.Join(dir, fmt.Sprintf("%s_%d.fnm", c.preset, n))
			t0 := time.Now()
			if _, err := meshout.Cook(rockImport(r.Mesh, ""), out, meshout.CookOptions{Force: true,
				CollisionMesh: coll != "hulls", CollisionHulls: coll == "hulls"}); err != nil {
				t.Fatal(err)
			}
			cook := time.Since(t0)
			st, _ := os.Stat(out)
			ms := func(d time.Duration) string { return fmt.Sprintf("%.2f s", d.Seconds()) }
			t.Logf("| %s | %d | %d | %d | %.1f cm | %s | %s | %s | %s | %.1f MB | %s |", c.preset, n, r.Mesh.TriangleCount(),
				r.Timings.MeshTriangles, r.Timings.Cell*100, ms(r.Timings.Mesh), ms(r.Timings.Simplify), ms(r.Timings.Bake),
				ms(cook), float64(st.Size())/(1<<20), coll)
		}
	}
}
