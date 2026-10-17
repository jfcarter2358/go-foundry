package phimport

// decimate_cards_test.go — thinning a mesh of many small cards keeps whole
// cards, enlarged, deterministically; a slot of big pieces goes through
// edge collapse.

import (
	"testing"

	"foundry/internal/model"

	"github.com/go-gl/mathgl/mgl32"
)

// cardMesh is n separate two-triangle cards (slot 0) beside one 400-quad
// sheet (slot 1).
func cardMesh(n int) *model.ImportMesh {
	m := &model.ImportMesh{Slots: []model.ImportSlot{{Name: "twig"}, {Name: "bark"}}}
	quad := func(x, z, s float32, slot int32) {
		b := uint32(len(m.Positions))
		for _, p := range []mgl32.Vec3{{x, 0, z}, {x + s, 0, z}, {x + s, s, z}, {x, s, z}} {
			m.Positions = append(m.Positions, p)
			m.Normals = append(m.Normals, mgl32.Vec3{0, 0, 1})
			m.UVs = append(m.UVs, mgl32.Vec2{p[0], p[1]})
		}
		m.Indices = append(m.Indices, b, b+1, b+2, b, b+2, b+3)
		m.TriSlot = append(m.TriSlot, slot, slot)
	}
	for i := 0; i < n; i++ {
		quad(float32(i%40), float32(i/40), 0.1, 0)
	}
	// A connected sheet: shared vertices, so one piece.
	b := uint32(len(m.Positions))
	const g = 21
	for j := 0; j < g; j++ {
		for i := 0; i < g; i++ {
			m.Positions = append(m.Positions, mgl32.Vec3{float32(i), 5, float32(j)})
			m.Normals = append(m.Normals, mgl32.Vec3{0, 1, 0})
			m.UVs = append(m.UVs, mgl32.Vec2{float32(i), float32(j)})
		}
	}
	for j := 0; j < g-1; j++ {
		for i := 0; i < g-1; i++ {
			a := b + uint32(j*g+i)
			m.Indices = append(m.Indices, a, a+g, a+g+1, a, a+g+1, a+1)
			m.TriSlot = append(m.TriSlot, 1, 1)
		}
	}
	return m
}

func TestDecimateCardsKeepsWholeCards(t *testing.T) {
	m := cardMesh(4000) // 8000 card triangles, 800 sheet triangles
	// The sheet (opaque) is funded first, with half the target: 600 of its
	// 800; the cards (cutout) get the rest, ~600 of 8,000.
	out := DecimateCards(m, 1200, []string{"twig"})
	if n := out.TriangleCount(); n < 900 || n > 1600 {
		t.Fatalf("%d triangles, want about 1200", n)
	}
	cards, sheet := 0, 0
	for _, s := range out.TriSlot {
		if s == 0 {
			cards++
		} else {
			sheet++
		}
	}
	if cards%2 != 0 || cards < 400 || cards > 800 {
		t.Errorf("%d card triangles: whole cards are two triangles each, about 600 kept", cards)
	}
	if sheet < 400 || sheet >= 800 {
		t.Errorf("the sheet slot has %d triangles: edge collapse should have taken it to about 600", sheet)
	}
	// A kept card is enlarged about its centre: 1/sqrt(0.075) ≈ 3.65×.
	pieces := components(slotMesh(out, func() []int {
		var ts []int
		for t, s := range out.TriSlot {
			if s == 0 {
				ts = append(ts, t)
			}
		}
		return ts
	}(), 0))
	p := pieces[0]
	var lo, hi mgl32.Vec3
	for k, tri := range p {
		for q := 0; q < 3; q++ {
			v := out.Positions[out.Indices[3*tri+q]]
			if k == 0 && q == 0 {
				lo, hi = v, v
			}
			for c := 0; c < 3; c++ {
				lo[c], hi[c] = min(lo[c], v[c]), max(hi[c], v[c])
			}
		}
	}
	if w := hi[0] - lo[0]; w < 0.34 || w > 0.39 {
		t.Errorf("kept card is %.3f wide, want ~0.365 (0.1 enlarged)", w)
	}
	// Deterministic.
	again := DecimateCards(cardMesh(4000), 1200, []string{"twig"})
	if again.TriangleCount() != out.TriangleCount() || again.Positions[7] != out.Positions[7] {
		t.Error("thinning is not deterministic")
	}
	// Under budget: untouched.
	if DecimateCards(m, 1e6, []string{"twig"}) != m {
		t.Error("a mesh within budget was rebuilt")
	}
}

// An opaque slot of many small pieces — a tree's branches, separate tubes —
// is edge-collapsed, never thinned: thinning dropped most limbs and blew the
// rest up about their middles, leaving the needles hanging in the air.
func TestDecimateCardsDoesNotThinOpaquePieces(t *testing.T) {
	m := cardMesh(4000)
	out := DecimateCards(m, 1200, nil) // nothing cutout: the "twig" cards are wood
	var lo, hi mgl32.Vec3
	first := true
	for tri, s := range out.TriSlot {
		if s != 0 {
			continue
		}
		for q := 0; q < 3; q++ {
			v := out.Positions[out.Indices[3*tri+q]]
			if first {
				lo, hi, first = v, v, false
			}
			for c := 0; c < 3; c++ {
				lo[c], hi[c] = min(lo[c], v[c]), max(hi[c], v[c])
			}
		}
	}
	// Every card is 0.1 wide at x = i%40: nothing enlarged reaches past
	// the last card's edge, 39.1.
	if !first && hi[0] > 39.1001 {
		t.Errorf("opaque pieces were enlarged (x reaches %.3f)", hi[0])
	}
	for _, p := range out.Positions {
		if p[1] > 0.1001 && p[1] < 4.9 {
			t.Fatalf("an opaque piece was enlarged: vertex at y %.3f", p[1])
		}
	}
}
