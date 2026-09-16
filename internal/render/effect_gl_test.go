package render

// effect_gl_test.go — explosion effects through the volumetric pass, on a GPU.
//
// The explosion's density and colour live in volume_common.glsl behind a draw
// kind and a dozen uniforms looked up by name, any of which could be missing
// with every program still linking. So each case draws one with an answer that
// cannot be reached by accident and reads the pixel: fire glowing its own
// colour in a scene with no light, smoke once it has cooled, and nothing at all
// outside its run.
//
//	FOUNDRY_GL_TEST=1 go test -run TestVolumetricExplosion ./internal/render/

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// explosionLevel holds one explosion ten units ahead of the eye, age of the
// way through its run.
func explosionLevel(age float32) *world.Level {
	fx := world.NewEffect("boom", world.EffectExplosion, mgl32.Vec3{0, 0, 10})
	fx.Explosion.Rise = 0
	fx.Explosion.Density = 6
	fx.Time = age * fx.Duration()
	return &world.Level{Effects: []world.Effect{fx}}
}

func TestVolumetricExplosionGlowsFireColoured(t *testing.T) {
	targets := volTestSetup(t)
	lvl := explosionLevel(0.08)
	px := volDraw(t, targets, lvl, volLook(mgl32.Vec3{0, 0, 1}, mgl32.Vec3{0, 1, 0}), 1, volTestSize/2, volTestSize/2)
	// No light in the scene at all, so every bit of colour is the fire's own:
	// hot, and hued like fire.
	if px[0] < 180 || !(px[0] >= px[1] && px[1] > px[2]) {
		t.Errorf("a fresh explosion's centre read %v; it should glow like fire (red >= green > blue) with no light in the scene", px)
	}
	if DebugStats.VolumeDraws != 1 {
		t.Errorf("counted %d volume draws, want 1", DebugStats.VolumeDraws)
	}

	// Well to the side of it, nothing.
	side := volDraw(t, targets, lvl, volLook(mgl32.Vec3{1, 0, 0}, mgl32.Vec3{0, 1, 0}), 1, volTestSize/2, volTestSize/2)
	if side[0] > 10 || side[1] > 10 || side[2] > 10 {
		t.Errorf("looking away from the explosion read %v; want black", side)
	}
}

func TestVolumetricExplosionCoolsToSmoke(t *testing.T) {
	targets := volTestSetup(t)
	lvl := explosionLevel(0.8)
	lvl.Lights.Global = world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 1}
	px := volDraw(t, targets, lvl, volLook(mgl32.Vec3{0, 0, 1}, mgl32.Vec3{0, 1, 0}), 1, volTestSize/2, volTestSize/2)
	// Lit by ambient only, a cooled explosion is its smoke colour: dim and
	// near grey, with none of the fire's red left in it.
	if px[0] > 120 || int(px[0])-int(px[2]) > 40 {
		t.Errorf("a late explosion's centre read %v; it should have cooled to grey smoke", px)
	}
	if px[0] == 0 && px[1] == 0 && px[2] == 0 {
		t.Error("a late explosion drew nothing; the smoke should still be there")
	}
}

func TestVolumetricExplosionDrawsNothingOutsideItsRun(t *testing.T) {
	targets := volTestSetup(t)
	lvl := explosionLevel(0.2)
	lvl.Effects[0].Playing = false
	px := volDraw(t, targets, lvl, volLook(mgl32.Vec3{0, 0, 1}, mgl32.Vec3{0, 1, 0}), 1, volTestSize/2, volTestSize/2)
	if px[0] > 0 || px[1] > 0 || px[2] > 0 || DebugStats.VolumeDraws != 0 {
		t.Errorf("a stopped explosion read %v with %d draws; want nothing", px, DebugStats.VolumeDraws)
	}
}

// Late in the run, the space around a thinning explosion — outside its radius
// but inside the reach its noise could push it to — must stay clear. The term
// that keeps a young fireball's heart filled went negative as the smoke thinned,
// and multiplied by the (also negative) distance past the heart it filled the
// entire reach solid: a grey ball that popped in for the run's last half second.
func TestVolumetricExplosionDoesNotFillItsReachAsItThins(t *testing.T) {
	targets := volTestSetup(t)
	lvl := explosionLevel(0.9)
	lvl.Lights.Global = world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 1}
	fx := &lvl.Effects[0]
	st, _ := fx.ExplosionState(lvl)
	// A ray passing the centre between the radius and the reach.
	miss := (st.Radius + fx.Explosion.Reach(st)) / 2
	fx.Position = mgl32.Vec3{miss, 0, 10}
	px := volDraw(t, targets, lvl, volLook(mgl32.Vec3{0, 0, 1}, mgl32.Vec3{0, 1, 0}), 1, volTestSize/2, volTestSize/2)
	if px[0] > 40 || px[1] > 40 || px[2] > 40 {
		t.Errorf("a ray %v from a thinning explosion's centre (radius %v) read %v; want clear air", miss, st.Radius, px)
	}
}
