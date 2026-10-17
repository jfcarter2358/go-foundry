package sim

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/material"
	"foundry/internal/platform"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// sandLevel is one 100 m tile over [0, 100]² at height 0 whose splat puts
// sand on the west half and rock on the east, and the player standing at
// (x, z) on it.
func sandLevel(t *testing.T, x, z float32) *world.Level {
	t.Helper()
	dir := t.TempDir()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255})
	img.SetNRGBA(1, 0, color.NRGBA{A: 255})
	f, err := os.Create(filepath.Join(dir, "w.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	d := &material.Doc{Name: "footsteps_test_ground", Domain: material.DomainSurface}
	e := d.AddEffect(material.KindSplat)
	e.Set("weights", material.TextureValue(filepath.Join(dir, "w")))
	e.Set("bounds_min", material.Vec2Value(mgl32.Vec2{0, 0}))
	e.Set("bounds_max", material.Vec2Value(mgl32.Vec2{100, 100}))
	e.Set(material.SplatLayerParam(0, ""), material.TextureValue("sand_tex"))
	e.Set(material.SplatLayerParam(0, "soft"), material.EnumValue(material.SoftSand))
	e.Set(material.SplatLayerParam(3, ""), material.TextureValue("rock_tex"))
	material.Put(d)
	t.Cleanup(func() { material.Remove(d.Name) })

	lvl := &world.Level{}
	lvl.Scenery = append(lvl.Scenery, world.Scenery{ID: "tile", Material: d.Name,
		LocalMin: mgl32.Vec3{0, -1, 0}, LocalMax: mgl32.Vec3{100, 0, 100}})
	prev, prevGround, prevFly := world.PlayerEntity, platform.IsOnGround, platform.FlyMode
	t.Cleanup(func() {
		world.PlayerEntity, platform.IsOnGround, platform.FlyMode = prev, prevGround, prevFly
		world.DrainTrailStamps(nil)
	})
	p := world.NewPlayer(mgl32.Vec3{x, 0, z})
	p.Position[1] = p.BoundHalfY // feet on the ground
	world.PlayerEntity = p
	platform.IsOnGround, platform.FlyMode = true, false
	world.DrainTrailStamps(nil)
	return lvl
}

// walk moves the player along +Z at speed for seconds, stepping the
// walkers each 60th of a second as the frame does.
func walk(lvl *world.Level, speed, seconds float32) {
	p := world.PlayerEntity
	p.Velocity = mgl32.Vec3{0, 0, speed}
	const dt = 1.0 / 60
	for t := float32(0); t < seconds; t += dt {
		p.Position = p.Position.Add(p.Velocity.Mul(dt))
		stepWalkers(lvl, dt, false)
	}
}

func TestWalkingOnSandLeavesAlternatingFootprints(t *testing.T) {
	lvl := sandLevel(t, 20, 10)
	walk(lvl, 1.5, 4) // six metres at a walk

	stamps := world.DrainTrailStamps(nil)
	// A walk's stride is ~0.7 m: some eight steps in six metres.
	if len(stamps) < 6 || len(stamps) > 11 {
		t.Fatalf("%d footprints in six metres of walking, want about eight", len(stamps))
	}
	for i, s := range stamps {
		c := s.A.Add(s.B).Mul(0.5)
		// Each foot is put down beside the line walked (x = 20), the left
		// and right alternately, along the way it is going (+Z).
		if d := c.X() - 20; d > -0.05 && d < 0.05 {
			t.Errorf("footprint %d is on the line itself (x %v), not beside it", i, c.X())
		}
		if i > 0 {
			prev := stamps[i-1].A.Add(stamps[i-1].B).Mul(0.5)
			if (c.X() > 20) == (prev.X() > 20) {
				t.Errorf("footprints %d and %d are both on the same side", i-1, i)
			}
			if c.Y() <= prev.Y() {
				t.Errorf("footprint %d (z %v) is not ahead of %d (z %v)", i, c.Y(), i-1, prev.Y())
			}
		}
		if along := s.B.Sub(s.A); along.Y() <= 0 || along.X() != 0 {
			t.Errorf("footprint %d does not point the way walked: %v", i, along)
		}
	}

	// On sand the stride drags and the eye sinks.
	if d := playerSoftDrag(); d <= 0 {
		t.Errorf("no drag on sand")
	}
	if world.PlayerGroundSink <= 0.01 {
		t.Errorf("the eye has not sunk into the sand: %v", world.PlayerGroundSink)
	}
}

func TestWalkingOnRockLeavesNothing(t *testing.T) {
	lvl := sandLevel(t, 80, 10)
	walk(lvl, 1.5, 4)
	if stamps := world.DrainTrailStamps(nil); len(stamps) != 0 {
		t.Errorf("%d footprints pressed into rock", len(stamps))
	}
	if d := playerSoftDrag(); d != 0 {
		t.Errorf("rock drags at the stride: %v", d)
	}
}

func TestStandingAndFlyingLeaveNothing(t *testing.T) {
	lvl := sandLevel(t, 20, 10)
	walk(lvl, 0, 2)
	if stamps := world.DrainTrailStamps(nil); len(stamps) != 0 {
		t.Errorf("standing still pressed %d footprints", len(stamps))
	}
	platform.FlyMode = true
	walk(lvl, 3, 2)
	if stamps := world.DrainTrailStamps(nil); len(stamps) != 0 {
		t.Errorf("flying pressed %d footprints", len(stamps))
	}
}

func TestLandingPressesBothFeet(t *testing.T) {
	lvl := sandLevel(t, 20, 10)
	stepWalkers(lvl, 1.0/60, true)
	if stamps := world.DrainTrailStamps(nil); len(stamps) != 2 {
		t.Errorf("a landing pressed %d footprints, want both feet", len(stamps))
	}
}

func TestAIWalkersOnSandStepAndAreDragged(t *testing.T) {
	lvl := sandLevel(t, 80, 80) // the player on the rock, out of it
	lvl.Entities = append(lvl.Entities, world.Entity{ID: "grunt", Position: mgl32.Vec3{20, 0.9, 10},
		Velocity: mgl32.Vec3{0, 0, 2}, AI: &world.AISpec{Speed: 2}})
	g := &lvl.Entities[0]
	g.BoundHalfY = 0.9
	g.Collision.Gravity, g.Collision.Movable, g.Collision.OnGround = true, true, true
	const dt = 1.0 / 60
	for i := 0; i < 180; i++ {
		g.Position = g.Position.Add(g.Velocity.Mul(dt))
		stepWalkers(lvl, dt, false)
	}
	if stamps := world.DrainTrailStamps(nil); len(stamps) < 4 {
		t.Errorf("an AI walker crossing sand pressed %d footprints", len(stamps))
	}
	if d := world.WalkerDrag("grunt"); d <= 0 {
		t.Errorf("an AI walker on sand has no drag for its steering")
	}
}
