package render

// static_pool_gl_test.go — a compiled level's groups pooled draw what they
// drew each in buffers of its own: frames of the visibility-buffer parity
// scene (compiled surfaces with baked light, a chunk, cooked and classic
// rocks, a shadowing sun and point light) compared byte for byte, forward and
// visibility-buffer both.
//
//	FOUNDRY_GL_TEST=1 go test -run TestStaticPoolsDrawTheSame ./internal/render/

import (
	"bytes"
	"testing"

	"github.com/go-gl/gl/v4.3-core/gl"
)

func TestStaticPoolsDrawTheSame(t *testing.T) {
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
	prev, prevVis := StaticPools, NaniteVisBuffer
	t.Cleanup(func() { StaticPools, NaniteVisBuffer = prev, prevVis })
	for _, vis := range []bool{true, false} {
		NaniteVisBuffer = vis
		run := func(pooled bool) [][]uint8 {
			StaticPools = pooled
			ForgetTAAHistory()
			taa.prevValid = false
			lvl := visTestLevel(t, rock)
			sb := staticBatchesFor(lvl)
			if sb == nil || sb.gpu == nil {
				t.Fatal("the GPU path did not build")
			}
			if pooled && len(sb.pools) != 1 || !pooled && len(sb.pools) != len(sb.groups) {
				t.Fatalf("pooled=%v: %d pools for %d groups", pooled, len(sb.pools), len(sb.groups))
			}
			var frames [][]uint8
			for f := 0; f < 3; f++ {
				frames = append(frames, w.frame(lvl))
			}
			return frames
		}
		run(false)
		alone, pooled := run(false), run(true)
		for f := range alone {
			if !bytes.Equal(alone[f], pooled[f]) {
				over8, over32, worst := pixelDiff(alone[f], pooled[f])
				t.Errorf("vis=%v frame %d: pooled differs from a pool a group: %d pixels over 8, %d over 32, worst %d",
					vis, f, over8, over32, worst)
			}
		}
	}
}
