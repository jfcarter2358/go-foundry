package render

// nanite_compact_gl_test.go — the command packing's prefix sum (Phase 5
// rewrote it as a scan over every tile in blocks of 256; see
// nanite_compact_comp.glsl), checked against the fixed list it packs, on a
// batch of thousands of tiles in more than one group.
//
//	FOUNDRY_GL_TEST=1 go test -timeout 300s -run 'Packing' ./internal/render/

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/glext"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

func readUints(buf uint32, n int) []uint32 {
	out := make([]uint32, n)
	if n == 0 {
		return out
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, buf)
	glext.GetBufferSubData(gl.SHADER_STORAGE_BUFFER, 0, n*4, gl.Ptr(&out[0]))
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	return out
}

// The packed list is, group by group, exactly the fixed list's live
// commands in their order, and each group's count is how many there are.
func TestNanitePackingKeepsEveryLiveCommandInOrder(t *testing.T) {
	newVisWorld(t)
	if !naniteCull.canCompact {
		t.Skip("this context has no indirect-count draw; nothing is packed")
	}
	NaniteLOD = true
	rock := visTestRock(t)
	lvl := &world.Level{}
	for i := 0; i < 48; i++ {
		s := world.NewScenery("rock", rock, mgl32.Vec3{float32(i%8)*1.2 - 4, float32(i/8)*0.8 - 2, -float32(i%5) * 0.7})
		tex := []string{"concrete", "brick", ""}[i%3]
		s.Materials = map[string]string{"stone": tex, "moss": tex, "": tex}
		if err := s.Load(); err != nil {
			t.Fatal(err)
		}
		s.Scale = mgl32.Vec3{0.5, 0.5, 0.5}
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
	if len(b.tiles) <= 512 || len(b.groups) < 2 {
		t.Fatalf("%d tiles in %d groups: not enough to cross the scan's blocks", len(b.tiles), len(b.groups))
	}
	for pi, pos := range []mgl32.Vec3{{0, 0, 6}, {3, 1, 3}} {
		gfx.Cam.Position, gfx.Cam.Direction = pos, mgl32.Vec3{0, 0, -1}
		gfx.UpdateCamMatrix()
		projView := gfx.Projection.Mul4(gfx.Cam.Matrix)
		v := cameraCullView(ExtractFrustum(projView), projView, gfx.Cam.Position, cullPlain)
		v.compact = true
		if !b.cull(v) || !b.compacted {
			t.Fatalf("pose %d: the pass was not packed", pi)
		}
		fixed := readUints(b.cmdBuf, b.slots*5)
		packed := readUints(b.packedBuf, b.slots*5)
		counts := readUints(b.countBuf, len(b.groups))
		live := 0
		for gi, g := range b.groups {
			var want [][5]uint32
			for s := g.first; s < g.first+g.count; s++ {
				if fixed[s*5+1] != 0 {
					var c [5]uint32
					copy(c[:], fixed[s*5:s*5+5])
					want = append(want, c)
				}
			}
			if int(counts[gi]) != len(want) {
				t.Fatalf("pose %d, group %d: count %d, want %d live commands", pi, gi, counts[gi], len(want))
			}
			for k, c := range want {
				var got [5]uint32
				copy(got[:], packed[(int(g.first)+k)*5:(int(g.first)+k)*5+5])
				if got != c {
					t.Fatalf("pose %d, group %d: packed command %d is %v, want %v", pi, gi, k, got, c)
				}
			}
			live += len(want)
		}
		t.Logf("pose %d: %d tiles in %d groups, %d slots, %d live commands packed", pi, len(b.tiles), len(b.groups), b.slots, live)
		if live == 0 {
			t.Fatalf("pose %d: nothing live", pi)
		}
	}
}
