package sim

import (
	"testing"

	"foundry/internal/material"
	"foundry/internal/platform"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// poolLevel is a 20 m brush pool whose surface is at y = 0, and the player.
func poolLevel(t *testing.T) (*world.Level, *world.Entity) {
	t.Helper()
	d := &material.Doc{Name: "wade_test_water", Domain: material.DomainSurface}
	d.AddEffect(material.KindWater)
	material.Put(d)
	t.Cleanup(func() { material.Remove(d.Name) })
	pool := world.NewBoxBrush("pool", mgl32.Vec3{0, -2, 0}, mgl32.Vec3{10, 2, 10}, "")
	for i := range pool.Faces {
		pool.Faces[i].Material = d.Name
	}
	lvl := &world.Level{Chunks: []world.Chunk{{ID: "c", Brushes: []world.Brush{pool}}}}
	world.InvalidateWaterQuery()
	prev, prevFly := world.PlayerEntity, platform.FlyMode
	t.Cleanup(func() {
		world.PlayerEntity, platform.FlyMode = prev, prevFly
		world.DrainWaterRipples(nil)
	})
	p := world.NewPlayer(mgl32.Vec3{0, 0, 0})
	world.PlayerEntity = p
	platform.FlyMode = false
	world.DrainWaterRipples(nil)
	return lvl, p
}

func TestWadingRingsTheWaterAndAJumpSplashes(t *testing.T) {
	lvl, p := poolLevel(t)
	// Knee deep, walking: rings every stride and the wake between.
	p.Position = mgl32.Vec3{0, -0.4 + p.BoundHalfY, 0}
	p.Velocity = mgl32.Vec3{1.5, 0, 0}
	for i := 0; i < 120; i++ {
		p.Position = p.Position.Add(p.Velocity.Mul(1.0 / 60))
		stepWater(lvl, 1.0/60)
	}
	rings := world.DrainWaterRipples(nil)
	if len(rings) < 4 {
		t.Fatalf("%d rings in two seconds of wading, want a wake and steps", len(rings))
	}
	for _, r := range rings {
		if r.At.Y() != 0 {
			t.Fatalf("a ring at height %v, want on the surface", r.At.Y())
		}
	}

	// Out of the water, then dropping in fast: a splash, with its rings.
	p.Position = mgl32.Vec3{3, 2 + p.BoundHalfY, 3}
	p.Velocity = mgl32.Vec3{0, -6, 0}
	stepWater(lvl, 1.0/60)
	p.Position = mgl32.Vec3{3, -0.3 + p.BoundHalfY, 3}
	stepWater(lvl, 1.0/60)
	rings = world.DrainWaterRipples(nil)
	if len(rings) == 0 || rings[0].Strength < 0.5 {
		t.Fatalf("dropping in made rings %+v, want a strong one", rings)
	}
	// Dry ground: nothing.
	p.Position = mgl32.Vec3{30, p.BoundHalfY, 30}
	p.Velocity = mgl32.Vec3{2, 0, 0}
	for i := 0; i < 60; i++ {
		stepWater(lvl, 1.0/60)
	}
	if n := len(world.DrainWaterRipples(nil)); n != 0 {
		t.Errorf("%d rings walking on dry ground", n)
	}
}
