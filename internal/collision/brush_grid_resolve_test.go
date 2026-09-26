package collision

import (
	"fmt"
	"math/rand"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// pushOutWholeList is pushOutOfChunkBrushes as it was before it asked the
// grid: every brush of the chunk, in order, each box-tested against the body
// where it is by then. The grid walk must give exactly this.
func pushOutWholeList(chunk *world.Chunk, pos mgl32.Vec3, radius, halfHeight float32) (out mgl32.Vec3, hitFloor, hitCeiling bool) {
	out = pos
	body := Cylinder{C: out, Half: mgl32.Vec3{radius, halfHeight, radius}, Rot: mgl32.Ident4()}
	bestFloorCos := float32(0)
	for bi := range chunk.Brushes {
		b := &chunk.Brushes[bi]
		if !b.Solid || len(b.Verts) < 4 || !bodyBoxTouchesBrush(body.C, body.Half, b) {
			continue
		}
		m := b.Placement()
		for _, piece := range chunk.ConvexBrushPieces(bi) {
			n, depth, ok := Penetration(body, ConvexHull{Pts: piece.Verts, Xform: m})
			if !ok || depth <= 0 {
				continue
			}
			push := n.Mul(-(depth + 0.001))
			out = out.Add(push)
			body = Translate(body, push).(Cylinder)
			mag := push.Len()
			if mag < 1e-9 {
				continue
			}
			cos := push.Y() / mag
			switch {
			case cos > 0:
				if cos >= MaxSlopeCos && cos > bestFloorCos {
					bestFloorCos, hitFloor = cos, true
				}
			case cos < 0:
				if -cos >= MaxSlopeCos {
					hitCeiling = true
				}
			}
		}
	}
	return
}

// A chunk of a few thousand scattered, overlapping boxes — some dynamic, so
// the loose list is exercised too — resolves every body exactly as the
// whole-list walk does. Bodies are dropped into the clutter, where a push
// out of one brush lands them in the next, which is the case the re-query
// after a push exists for.
func TestGridResolveMatchesTheWholeListWalk(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	ch := &world.Chunk{ID: "c", Scale: 1}
	for i := 0; i < 3000; i++ {
		c := mgl32.Vec3{rng.Float32()*400 - 200, rng.Float32() * 6, rng.Float32()*400 - 200}
		h := mgl32.Vec3{0.2 + rng.Float32()*3, 0.2 + rng.Float32()*2, 0.2 + rng.Float32()*3}
		if i%500 == 0 {
			h = mgl32.Vec3{60, 0.5, 60} // a few floors that span many cells
		}
		b := world.NewBoxBrush(fmt.Sprint("b", i), c, h, "")
		b.Dynamic = i%97 == 0
		ch.Brushes = append(ch.Brushes, b)
	}
	ch.RecomputeBounds()

	pushed := 0
	for i := 0; i < 4000; i++ {
		p := mgl32.Vec3{rng.Float32()*420 - 210, rng.Float32() * 8, rng.Float32()*420 - 210}
		r, hh := 0.2+rng.Float32()*0.6, 0.4+rng.Float32()*0.8
		got, gf, gc := pushOutOfChunkBrushes(ch, p, r, hh)
		want, wf, wc := pushOutWholeList(ch, p, r, hh)
		if got != want || gf != wf || gc != wc {
			t.Fatalf("body %d at %v: grid walk gave %v floor=%v ceiling=%v, whole list %v floor=%v ceiling=%v",
				i, p, got, gf, gc, want, wf, wc)
		}
		if got != p {
			pushed++
		}
	}
	// Most bodies land in open space; the test is only worth anything if a
	// good number are pushed, several times over.
	if pushed < 200 {
		t.Fatalf("only %d of 4000 bodies were pushed at all — the scene is too sparse to test anything", pushed)
	}
	t.Logf("%d of 4000 bodies pushed", pushed)
}
