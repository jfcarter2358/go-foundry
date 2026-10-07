package render

// frame_prep_gl_test.go — the frame's preparation on goroutines draws what it
// drew on the main thread, frame for frame and byte for byte: GI on (its
// source diff is one of the two), and particle effects of every blend mode in
// view (the camera's gather is the other).
//
//	FOUNDRY_GL_TEST=1 go test -run TestParallelFramePrepDrawsTheSame ./internal/render/
//
// Run it under -race too: the point of the comparison is that the goroutines
// read nothing the main thread writes meanwhile.

import (
	"bytes"
	"fmt"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/particle"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// prepTestEffects is three emitters in front of the camera, one per blend
// mode, their particles placed by hand (nothing simulates them here).
func prepTestEffects() []world.Emitter {
	var out []world.Emitter
	for i, blend := range []gfx.SpriteBlend{gfx.SpriteBlendAlpha, gfx.SpriteBlendAdditive, gfx.SpriteBlendCutout} {
		l := particle.NewLayer(fmt.Sprintf("l%d", i))
		l.Blend = blend
		l.Texture = "concrete"
		l.CastShadows = true
		s := particle.NewSystem(l, 0, uint32(i+1))
		centre := mgl32.Vec3{float32(i)*0.8 - 0.8, 0.4, 2.5}
		for k := 0; k < 60; k++ {
			f := float32(k)
			s.Live = append(s.Live, particle.Particle{
				Pos:  centre.Add(mgl32.Vec3{0.3 * float32(k%7) / 7, 0.05 * float32(k%11), 0.02 * f}),
				Size: 0.15, Life: 4, Age: 0.03 * f,
			})
		}
		e := world.NewEmitter(fmt.Sprintf("fx%d", i), centre)
		e.Doc = &particle.Doc{Layers: []particle.Layer{l}}
		e.Systems = []*particle.System{s}
		out = append(out, e)
	}
	return out
}

func TestParallelFramePrepDrawsTheSame(t *testing.T) {
	w := newVisWorld(t)
	// A DrawWorld frame of this scene leaves a GL error behind it (it did
	// before this test existed: TestVisBufferShadesTheForwardPicture then
	// TestUntexturedTerrainIsPaintedWithItsNormal fails the same way), which
	// the next test to ask would take for its own.
	t.Cleanup(func() {
		for gl.GetError() != gl.NO_ERROR {
		}
	})
	rock := visTestRock(t)
	prevGI, prevPar := gfx.GIQuality, ParallelFramePrep
	t.Cleanup(func() {
		gfx.GIQuality, ParallelFramePrep = prevGI, prevPar
		gi.release()
	})
	gfx.GIQuality = gfx.GIMedium
	run := func(parallel bool) [][]uint8 {
		ParallelFramePrep = parallel
		gi.release()
		ForgetTAAHistory()
		taa.prevValid = false
		lvl := visTestLevel(t, rock)
		lvl.Emitters = prepTestEffects()
		var frames [][]uint8
		for f := 0; f < 4; f++ {
			// GI on: no Skip.
			px := w.frameIntoWith(lvl, WorldDraw{Now: 1 + float64(f)*0.016})
			frames = append(frames, px)
			// Something for the source diff to find: a placement moves.
			lvl.Scenery[0].Position = lvl.Scenery[0].Position.Add(mgl32.Vec3{0.05, 0, 0})
		}
		return frames
	}
	// The first frames a context draws are not the ones after (the world's
	// first-use state), so one sequence is drawn and dropped before the two
	// that are compared.
	run(false)
	serial := run(false)
	if !gi.active {
		t.Fatal("GI did not run; the comparison would not cover its source diff")
	}
	if DebugStats.Particles == 0 {
		t.Fatal("no particles were gathered; the comparison would not cover the sprites")
	}
	before := prepStarted
	parallel := run(true)
	if prepStarted.gi == before.gi || prepStarted.sprites == before.sprites {
		t.Fatalf("the parallel run started %d GI and %d sprite jobs; both should have run",
			prepStarted.gi-before.gi, prepStarted.sprites-before.sprites)
	}
	for f := range serial {
		if !bytes.Equal(serial[f], parallel[f]) {
			over8, over32, worst := pixelDiff(serial[f], parallel[f])
			t.Errorf("frame %d differs with the preparation on goroutines: %d pixels over 8, %d over 32, worst %d",
				f, over8, over32, worst)
		}
	}
}
