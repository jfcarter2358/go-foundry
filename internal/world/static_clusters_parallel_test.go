package world

// static_clusters_parallel_test.go — BuildStaticClusters over a level of many
// draw groups: how long it takes, and (with the groups built side by side)
// that it builds exactly what one group at a time does.

import (
	"bytes"
	"encoding/gob"
	"math"
	"testing"

	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// manyGroupFloor is a row of chunks, each a bumpy grid floor of its own
// size, laid edge to edge so neighbours share a seam — a level's worth of
// draw groups, big and small. Every chunk also carries a strip of
// double-sided and of see-through quads, which take the flat cut, so both
// kinds of group are in the mix.
func manyGroupFloor(tb testing.TB, sizes []int) *StaticGeometry {
	tb.Helper()
	height := func(x, z float32) float32 {
		return 0.4*float32(math.Sin(float64(x)*0.7)*math.Cos(float64(z)*0.5)) + 0.15*float32(math.Sin(float64(x*z)*0.3))
	}
	s := &StaticGeometry{}
	add := func(chunk string, x0, z0, x1, z1 float32, double bool, alpha float32) {
		verts := [][]float32{
			{x0, height(x0, z0), z0, x0, z0}, {x0, height(x0, z1), z1, x0, z1},
			{x1, height(x1, z1), z1, x1, z1}, {x1, height(x1, z0), z0, x1, z0},
		}
		var g gfx.Geometry
		g.CPUOnly = true
		if err := g.Load(verts, []int{0, 1, 2, 0, 2, 3}, "concrete", mgl32.Vec3{}); err != nil {
			tb.Fatal(err)
		}
		g.Alpha = alpha
		s.Surfaces = append(s.Surfaces, StaticSurface{Geometry: g, Chunk: chunk, DoubleSided: double, Normal: mgl32.Vec3{0, 1, 0},
			Lo: mgl32.Vec3{x0, -1, z0}, Hi: mgl32.Vec3{x1, 1, z1}})
	}
	const step = 0.5
	x := float32(0)
	for ci, n := range sizes {
		chunk := string(rune('a' + ci))
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				add(chunk, x+float32(i)*step, float32(j)*step, x+float32(i+1)*step, float32(j+1)*step, false, 1)
			}
		}
		for i := 0; i < n; i++ {
			add(chunk, x+float32(i)*step, -step, x+float32(i+1)*step, 0, true, 1)
			add(chunk, x+float32(i)*step, -2*step, x+float32(i+1)*step, -step, false, 0.5)
		}
		x += float32(n) * step
	}
	return s
}

// benchFloorSizes is one big chunk and a spread of smaller ones, as a real
// level has: its terrain and its rooms.
var benchFloorSizes = []int{120, 70, 60, 50, 40, 40, 30, 30, 30, 24, 24, 20, 20, 16, 16, 12}

func BenchmarkBuildStaticClusters(b *testing.B) {
	s := manyGroupFloor(b, benchFloorSizes)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BuildStaticClusters(s)
	}
}

// encodeClusters is the cut as the compiled file stores it: gob, which
// compares floats bit for bit where DeepEqual would trip over a NaN.
func encodeClusters(t *testing.T, c []StaticClusterGroup) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(c); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestBuildStaticClustersSideBySideMatchesOneAtATime(t *testing.T) {
	prev, prevLOD := parallelStaticClusters, StaticLOD
	StaticLOD = true
	t.Cleanup(func() { parallelStaticClusters, StaticLOD = prev, prevLOD })

	// One group well past a worker's share (it fans out on its own), and
	// many that share the workers.
	s := manyGroupFloor(t, []int{60, 24, 20, 20, 16, 16, 12, 12, 10, 10, 8, 8, 6})
	parallelStaticClusters = false
	serial := BuildStaticClusters(s)
	parallelStaticClusters = true
	side := BuildStaticClusters(s)
	if len(serial) < 30 {
		t.Fatalf("only %d groups: the floor should give dozens", len(serial))
	}
	dags := 0
	for _, g := range serial {
		if g.DAG != nil {
			dags++
		}
	}
	if dags < 6 {
		t.Fatalf("only %d of %d groups took a DAG", dags, len(serial))
	}
	if !bytes.Equal(encodeClusters(t, serial), encodeClusters(t, side)) {
		t.Fatal("cutting the groups side by side stored a different cut from cutting them one at a time")
	}
}
