package render

// point_shadow_cache_bench_test.go — what deciding "nothing changed" costs.
//
//	go test -run '^$' -bench PointShadowPlan -benchmem ./internal/render/
//
// Every shadowed point light is signed every frame, whether or not anything
// in its reach moved. The case measured is the common one: three lights over a
// hangar of sixteen animated characters holding still, where the answer is
// "keep" for all three and the whole cost is reaching it.

import (
	"fmt"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func benchShadowCacheLevel() (*world.Level, []world.PointLight) {
	tri := []float32{0, 0, 0, 0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1}
	const bones = 60
	lvl := &world.Level{}
	for i := 0; i < 16; i++ {
		pal := make([]mgl32.Mat4, bones)
		for b := range pal {
			pal[b] = mgl32.Translate3D(float32(b), 0, 0)
		}
		subs := make([]gfx.Geometry, 4)
		for s := range subs {
			subs[s] = gfx.Geometry{Vertices: tri, Alpha: 1, BonePalette: pal}
		}
		lvl.Entities = append(lvl.Entities, world.Entity{
			ID: fmt.Sprintf("crew%d", i), Visible: true,
			Position:   mgl32.Vec3{float32(i%4) * 2, 0, float32(i/4) * 2},
			BoundHalfX: 0.5, BoundHalfY: 1, BoundHalfZ: 0.5,
			Models: []world.EntityModel{{Visible: true, Submeshes: subs, Anim: &world.EntityAnim{Palette: pal}}},
		})
	}
	lights := []world.PointLight{
		{Position: mgl32.Vec3{2, 4, 2}, Radius: 12},
		{Position: mgl32.Vec3{0, 3, 6}, Radius: 10},
		{Position: mgl32.Vec3{6, 3, 0}, Radius: 10},
	}
	return lvl, lights
}

func BenchmarkPointShadowPlanNothingMoved(b *testing.B) {
	lvl, lights := benchShadowCacheLevel()
	pointShadowStates = nil
	b.Cleanup(func() { pointShadowStates = nil })
	// Maps whose static layers already exist, so planning makes no GL calls.
	maps := make([]PointShadowMap, len(lights))
	for i := range maps {
		maps[i] = PointShadowMap{Cubemap: uint32(10 + i), StaticCubemap: uint32(20 + i), StaticFBO: uint32(30 + i)}
	}
	for i := range lights {
		_ = planPointShadow(i, lvl, lights[i], &maps[i])
	}
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		for i := range lights {
			_ = planPointShadow(i, lvl, lights[i], &maps[i])
		}
	}
}
