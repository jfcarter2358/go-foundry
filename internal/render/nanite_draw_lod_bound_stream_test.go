package render

// nanite_draw_lod_bound_stream_test.go — the draw bound's LOD keys must come
// from a mesh's COOKED errors (staticClusterRecords), not from the records as
// the residency at load left them. Built from the load-time records, a page
// not resident at load counted none of its tiles; once it streamed in and its
// clusters entered the cut, the commands they packed were cut off past the
// bound and whole terrain tiles went missing (docs/regions/PRESETS_3.md).

import (
	"testing"

	"foundry/internal/nanitecook"

	"github.com/go-gl/mathgl/mgl32"
)

func TestDrawLODBoundKeysAreTheCookedErrors(t *testing.T) {
	m, err := nanitecook.CookMesh(nanitecook.SampleRock(4, 0.4), "rock.glb", nanitecook.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Pages) < 2 {
		t.Fatalf("the sample rock cooked to %d pages; the test wants a streamed mesh", len(m.Pages))
	}
	static := staticClusterRecords(m, nil)
	if len(static) != len(m.Clusters) {
		t.Fatalf("%d records for %d clusters", len(static), len(m.Clusters))
	}
	roots := 0
	for i := range static {
		// clusterRecords' order: page by page, each page's run of clusters.
		c := &m.Clusters[i]
		want := c.ParentError
		if want < 0 {
			want, roots = -1, roots+1
		}
		if static[i].Errs[0] != c.Error || static[i].Errs[1] != want {
			t.Fatalf("cluster %d: errors %v, cooked %g / %g", i, static[i].Errs, c.Error, c.ParentError)
		}
	}
	if roots == 0 {
		t.Fatal("no roots")
	}

	// One tile a cluster, one instance at the origin.
	var tiles []gpuTile
	slot := 0
	for ci := range static {
		tiles = appendRangeTile(tiles, uint32(ci), 1, 0, 0, &slot)
	}
	for i := range tiles {
		tiles[i].B[3] = 0
	}
	bound := func(records []gpuCluster, eye mgl32.Vec3, k float32) int32 {
		b := &cullBatch{cpuClusters: records, leaves: 1}
		b.tiles, b.groups, b.slots = tiles, []cullGroup{{0, int32(slot)}}, slot
		b.instSlotsDirty = true
		b.insts = []gpuInstance{placedInstance(mgl32.Ident4(), mgl32.Vec3{}, m.Radius)}
		b.setBoundLOD(naniteLODView{eye: eye, k: k}, true)
		b.boundDraws([]bool{true})
		return b.drawMax[0]
	}
	// What the cull packs once every page has streamed in: the clusters the
	// shaders' test takes with the cooked errors.
	need := func(eye mgl32.Vec3, k float32) int32 {
		n := int32(0)
		for ci := range static {
			if shaderSelects(&static[ci], mgl32.Ident4(), 1, eye, k) {
				n += rangeSlots
			}
		}
		return n
	}
	// The records as a load that had only the roots' pages resident leaves
	// them: every other cluster's parent error 0, out of any cut.
	loadTime := append([]gpuCluster(nil), static...)
	for i := range loadTime {
		if loadTime[i].Errs[1] >= 0 {
			loadTime[i].Errs[1] = 0
		}
	}
	under := 0
	for _, d := range []float32{0.5, 1, 2, 4, 8, 16, 32, 64, 128} {
		for _, k := range []float32{200, 1000, 4000} {
			eye := mgl32.Vec3{0, 0, d + m.Radius}
			n := need(eye, k)
			if got := bound(static, eye, k); got < n {
				t.Errorf("eye %g m, k %g: the bound from the cooked errors is %d, below the %d slots the cut draws", d, k, got, n)
			}
			if bound(loadTime, eye, k) < n {
				under++
			}
		}
	}
	if under == 0 {
		t.Error("the load-time records never under-counted: the test no longer shows the case it guards")
	}
}
