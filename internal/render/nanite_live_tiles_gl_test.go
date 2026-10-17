package render

// nanite_live_tiles_gl_test.go — the live-tile list (nanite_live_tiles.go)
// writes the same commands as dispatching every range tile: on a field of
// rocks seen from poses that cull some of them, every slot's live command,
// every tile's survivor count and the packed list match the full dispatch,
// pass for pass, in plain and occlusion modes.
//
//	FOUNDRY_GL_TEST=1 go test -timeout 300s -run 'LiveTiles' ./internal/render/

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func TestNaniteLiveTilesWriteWhatEveryTileWould(t *testing.T) {
	newVisWorld(t)
	if !naniteLive.ok {
		t.Fatal("the live-tile programs did not build")
	}
	NaniteLOD = true
	saved := NaniteLiveTiles
	t.Cleanup(func() { NaniteLiveTiles = saved })
	rock := visTestRock(t)
	lvl := &world.Level{}
	for i := 0; i < 64; i++ {
		s := world.NewScenery("rock", rock, mgl32.Vec3{float32(i%8)*3 - 12, 0, -float32(i/8) * 3})
		if err := s.Load(); err != nil {
			t.Fatal(err)
		}
		lvl.Scenery = append(lvl.Scenery, s)
	}
	t.Cleanup(func() { world.DeleteScenery(lvl) })
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	r := &naniteScenery
	if !r.prepare(lvl) {
		t.Fatal("the rocks did not take the GPU path")
	}
	b := &r.batch
	if b.rangeTile == 0 || b.live2.nRuns < 64 {
		t.Fatalf("%d range tiles in %d runs", b.rangeTile, b.live2.nRuns)
	}
	type out struct {
		cmds, counts []uint32
	}
	run := func(live bool, v cullView) out {
		NaniteLiveTiles = live
		if !b.cull(v) {
			t.Fatal("the pass did not run")
		}
		return out{readUints(b.cmdBuf, b.slots*5), readUints(b.tileCountBuf, len(b.tiles))}
	}
	culledSome := false
	poses := []struct{ pos, dir mgl32.Vec3 }{
		{mgl32.Vec3{0, 2, 8}, mgl32.Vec3{0, 0, -1}},
		{mgl32.Vec3{-14, 1, -4}, mgl32.Vec3{1, 0, -0.3}.Normalize()},
		{mgl32.Vec3{0, 2, 8}, mgl32.Vec3{0, 0, 1}},
		{mgl32.Vec3{0, 2, 8}, mgl32.Vec3{0, 0, -1}},
	}
	view := func(i int) cullView {
		gfx.Cam.Position, gfx.Cam.Direction = poses[i].pos, poses[i].dir
		gfx.UpdateCamMatrix()
		projView := gfx.Projection.Mul4(gfx.Cam.Matrix)
		return cameraCullView(ExtractFrustum(projView), projView, gfx.Cam.Position, cullPlain)
	}
	for pi := range poses {
		// The live list's state from a live pass of the pose before (runs
		// going out of view clear their slots), then this pose's live pass
		// against the full dispatch of it.
		if pi > 0 {
			run(true, view(pi-1))
		}
		got := run(true, view(pi))
		full := run(false, view(pi))
		vis := readUints(b.visBuf, len(b.insts))
		in := 0
		for _, x := range vis {
			if x != 0 {
				in++
			}
		}
		if in < len(vis) {
			culledSome = true
		}
		for s := 0; s < b.slots; s++ {
			f, g := full.cmds[s*5:s*5+5], got.cmds[s*5:s*5+5]
			if f[1] != g[1] || (f[1] != 0 && [5]uint32(f) != [5]uint32(g)) {
				t.Fatalf("pose %d: slot %d is %v, the full dispatch wrote %v", pi, s, g, f)
			}
		}
		for ti := range full.counts {
			if full.counts[ti] != got.counts[ti] {
				t.Fatalf("pose %d: tile %d counts %d survivors, the full dispatch %d", pi, ti, got.counts[ti], full.counts[ti])
			}
		}
		t.Logf("pose %d: %d of %d instances in view", pi, in, len(vis))
	}
	if !culledSome {
		t.Error("no pose culled an instance: the list was never tested leaving tiles out")
	}
}
