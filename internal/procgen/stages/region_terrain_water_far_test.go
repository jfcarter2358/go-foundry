package stages

// region_terrain_water_far_test.go — a sea reaching the union's edge is
// carried over the backdrop by sheets that do not overlap; a pond is not.

import (
	"math"
	"testing"

	"foundry/internal/procgen/place"
	"foundry/internal/procgen/terrain"
)

func TestFarWaterRectsCarryTheSeaOut(t *testing.T) {
	g := terrain.NewGrid(64, 2) // −64..64
	far := &terrain.FarMeta{Bounds: terrain.Bounds{MinX: -500, MinZ: -500, MaxX: 500, MaxZ: 500}}
	w := &terrain.WaterResult{Bodies: []terrain.WaterBody{
		// A sea over the east half, touching +x, +z and −z.
		{Level: -1.5, Bounds: [4]float64{10, -64, 64, 64}, Area: 1},
		// A pond in the middle: never carried out.
		{Pond: true, Level: 2, Bounds: [4]float64{-10, -10, 0, 0}, Area: 1},
	}}
	rs := farWaterRects(g, far, w, func(int) bool { return true })
	if len(farWaterRects(g, far, w, func(int) bool { return false })) != 0 {
		t.Error("a region without a coastline carried its water out")
	}
	sides := map[string]farWaterRect{}
	for _, r := range rs {
		sides[r.side] = r
		if r.level != -1.5 {
			t.Errorf("%s at %.2f", r.side, r.level)
		}
	}
	if len(rs) != 3 || sides["nx"].side != "" {
		t.Fatalf("sheets %+v, want px, pz, nz", rs)
	}
	px := sides["px"]
	if px.minX != 64 || px.maxX != 500 || px.minZ != -500 || px.maxZ != 500 {
		t.Errorf("+x sheet %+v: from the union edge to the backdrop's, corner to corner", px)
	}
	pz := sides["pz"]
	if pz.minX != 10 || pz.maxX != 64 || pz.minZ != 64 || pz.maxZ != 500 {
		t.Errorf("+z sheet %+v: the body's x extent within the union", pz)
	}
	// No overlap between sheets.
	for i := range rs {
		for j := i + 1; j < len(rs); j++ {
			a, b := rs[i], rs[j]
			ox := math.Min(a.maxX, b.maxX) - math.Max(a.minX, b.minX)
			oz := math.Min(a.maxZ, b.maxZ) - math.Max(a.minZ, b.minZ)
			if ox > 0 && oz > 0 {
				t.Errorf("%s and %s overlap", a.side, b.side)
			}
		}
	}
	m := farWaterMesh(px, 0.1).Mesh()
	if m.TriangleCount() == 0 {
		t.Fatal("empty sheet")
	}
	for _, p := range m.Positions {
		if p[1] != -1.5 {
			t.Fatalf("vertex at %.2f", p[1])
		}
	}
}

// sizedVariants drops the small pieces, keeping the largest when none is
// big enough.
func TestSizedVariantsDropsSmallPieces(t *testing.T) {
	vs := placeVariantsOfSizes(0.05, 0.2, 0.3)
	if got := sizedVariants(vs, 0); len(got) != 3 {
		t.Errorf("min 0 kept %d", len(got))
	}
	if got := sizedVariants(vs, 0.15); len(got) != 2 {
		t.Errorf("min 0.15 kept %d", len(got))
	}
	if got := sizedVariants(vs, 5); len(got) != 1 || got[0].Extent[0] != 0.3 {
		t.Errorf("min 5 kept %+v", got)
	}
}

// placeVariantsOfSizes is one variant per size (a cube that big).
func placeVariantsOfSizes(sizes ...float64) []place.Variant {
	var out []place.Variant
	for _, s := range sizes {
		out = append(out, place.Variant{Extent: [3]float64{s, s, s}})
	}
	return out
}
