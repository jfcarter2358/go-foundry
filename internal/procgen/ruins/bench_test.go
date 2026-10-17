package ruins

import (
	"testing"

	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/sdf"
)

// BenchmarkSiteField is the site field's cost a sample, near the floors.
func BenchmarkSiteField(b *testing.B) {
	st := testSettings()
	l := Lay(st, slope, Library(7, 1))
	f := NewSite(l, 3, st.Walkway, 1).Shape().F
	rng := noise.NewRNG(1)
	pts := make([]sdf.Vec3, 4096)
	for i := range pts {
		t := l.Terraces[rng.Intn(len(l.Terraces))]
		x, z := t.World(rng.Range(-t.HA, t.HA), rng.Range(-t.HB-2, t.HB+2))
		pts[i] = sdf.Vec3{x, t.Floor + rng.Range(-6, 2), z}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f(pts[i%len(pts)])
	}
}

// BenchmarkTowerField is a tower's cost a sample, near its surface.
func BenchmarkTowerField(b *testing.B) {
	f := Library(7, 1)[0].Build().F
	rng := noise.NewRNG(1)
	pts := make([]sdf.Vec3, 4096)
	for i := range pts {
		pts[i] = sdf.Vec3{rng.Range(-5, 5), rng.Range(0, 20), rng.Range(-5, 5)}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f(pts[i%len(pts)])
	}
}
