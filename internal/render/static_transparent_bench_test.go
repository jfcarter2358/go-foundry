package render

// static_transparent_bench_test.go — what a frame of a compiled level costs
// the CPU in finding its transparent surfaces, which the GPU cull leaves to it:
// the PVS answer, and the walk for the sorted pass.

import (
	"math/rand"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// glassyStatic is scatteredStatic with one surface in twenty made glass.
func glassyStatic(n int) *world.StaticGeometry {
	s := scatteredStatic(n, rand.New(rand.NewSource(3)))
	for i := range s.Surfaces {
		if i%20 == 0 {
			s.Surfaces[i].Geometry.Texture = "glass"
			s.Surfaces[i].Geometry.Alpha = 0.5
		}
	}
	return s
}

// benchStaticTransparentFrame is the frame loop's half: the PVS answer, then
// the transparent surfaces in view queued for the sorted pass.
func benchStaticTransparentFrame(s *world.StaticGeometry, sb *staticBatches, f Frustum, eye mgl32.Vec3) int {
	visible := s.VisibleSurfacesView(eye)
	n := 0
	sb.gatherVisibleMode(s, visible, f, eye, func(BatchDraw, float32) { n++ }, true)
	return n
}

func BenchmarkStaticTransparentGather(b *testing.B) {
	prev := gfx.GeometryUploadsEnabled
	gfx.GeometryUploadsEnabled = false
	defer func() { gfx.GeometryUploadsEnabled = prev }()
	s := glassyStatic(60000)
	sb := buildStaticBatches(s)
	eye := mgl32.Vec3{0, 0, -120}
	f := ExtractFrustum(mgl32.Perspective(mgl32.DegToRad(70), 16.0/9, 0.1, 500).Mul4(
		mgl32.LookAtV(eye, mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 1, 0})))
	if benchStaticTransparentFrame(s, sb, f, eye) == 0 {
		b.Fatal("no glass in view")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchStaticTransparentFrame(s, sb, f, eye)
	}
}
