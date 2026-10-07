package render

// gi_sources_bench_test.go — what live GI's source diff costs a frame on a
// level standing still: every chunk unchanged, every entity where it was.

import (
	"fmt"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// giBenchLevel is chunks chunks of brushes boxes each, and entities entities
// each carrying one small model.
func giBenchLevel(chunks, brushes, entities int) *world.Level {
	lvl := &world.Level{}
	for c := 0; c < chunks; c++ {
		ch := world.Chunk{ID: fmt.Sprintf("chunk_%d", c)}
		for i := 0; i < brushes; i++ {
			at := mgl32.Vec3{float32(c*40 + i%20*2), float32(i / 20), 0}
			ch.Brushes = append(ch.Brushes, world.NewBoxBrush(fmt.Sprintf("b%d", i), at, mgl32.Vec3{0.9, 0.5, 0.9}, ""))
		}
		lvl.Chunks = append(lvl.Chunks, ch)
	}
	tri := cubeTris(mgl32.Vec3{})
	var verts []float32
	for _, p := range tri {
		verts = append(verts, p[0], p[1], p[2], 0, 0)
	}
	for i := 0; i < entities; i++ {
		e := world.Entity{ID: fmt.Sprintf("crate_%d", i), Visible: true, Position: mgl32.Vec3{float32(i), 0, 5}, Scale: mgl32.Vec3{2, 2, 2}}
		m := world.EntityModel{Path: "models/crate.fmdl", Visible: true}
		m.Submeshes = []gfx.Geometry{{Vertices: verts}}
		e.Models = []world.EntityModel{m}
		lvl.Entities = append(lvl.Entities, e)
	}
	return lvl
}

func BenchmarkGISourceCollectStill(b *testing.B) {
	lvl := giBenchLevel(20, 300, 200)
	s := newGISourceSet()
	s.minModel = 0.1
	s.collect(lvl)
	s.changed, s.staticChanged = false, false
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.collect(lvl)
	}
	if s.changed {
		b.Fatal("a still level changed")
	}
}
