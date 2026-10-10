package compile

// shadow_test.go — where a baked shadow ray starts (airstart.go) and where the
// grid is made finer for one (refine.go).

import (
	"slices"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/bsp"
	"foundry/internal/world"
)

// sunFrom is a shadowing sun shining along dir, over a faint ambient.
func sunFrom(dir mgl32.Vec3) world.LightSet {
	return world.LightSet{
		Global: world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.05},
		Directional: []world.DirectionalLight{{
			Direction: dir.Normalize(), Color: mgl32.Vec3{1, 1, 1}, Brightness: 1,
			CastShadows: true, ShadowStrength: 1,
		}},
	}
}

// overhangWall is a wall standing in the floor whose west face leans out over
// it: 2 wide at its buried foot, 4 wide at its top.
func overhangWall() world.Brush {
	verts := []mgl32.Vec3{
		{-1, -0.5, -4}, {1, -0.5, -4}, {1, -0.5, 4}, {-1, -0.5, 4},
		{-3, 4, -4}, {1, 4, -4}, {1, 4, 4}, {-3, 4, 4},
	}
	centre := mgl32.Vec3{-0.5, 1.75, 0}
	outward := func(idx []int) []int {
		p := func(k int) mgl32.Vec3 { return verts[idx[k]] }
		if p(1).Sub(p(0)).Cross(p(2).Sub(p(0))).Dot(p(0).Sub(centre)) < 0 {
			slices.Reverse(idx)
		}
		return idx
	}
	var faces []world.BrushFace
	for _, q := range [][]int{{0, 1, 2, 3}, {4, 5, 6, 7}, {0, 3, 7, 4}, {1, 2, 6, 5}, {0, 1, 5, 4}, {3, 2, 6, 7}} {
		faces = append(faces, world.NewBrushFace(outward(q), ""))
	}
	return world.Brush{ID: "wall", BrushMesh: world.BrushMesh{Verts: verts, Faces: faces}, Solid: true}
}

// The foot of a canyon wall on its shaded side. The floor's vertex where it
// runs under the wall is, a bias above the floor, inside the overhang. Let out
// through the wall's far side, that ray baked the foot in full sun with the
// wall between it and the sun; started from the air beside it, it is shadowed.
func TestTheShadedFootOfAnOverhangingWallStaysDark(t *testing.T) {
	lvl := levelOf(boxFromTo("floor", mgl32.Vec3{-10, -1, -6}, mgl32.Vec3{10, 0, 6}), overhangWall())
	lvl.Lights = sunFrom(mgl32.Vec3{-1, -1, 0}) // from high in the east
	tr, _, _ := compiled(t, lvl)
	BakeLight(tr, lvl, DefaultLightOptions())

	// Where the leaning face meets the floor: x = -1 - 2*(0.5/4.5).
	footX := float32(-1 - 2*0.5/4.5)
	var foot, open []float32
	for fi := range tr.Faces {
		f := &tr.Faces[fi]
		if f.Brush != "floor" || f.Plane.N.Y() < 0.99 {
			continue
		}
		for i, p := range f.Points {
			switch {
			case bsp.Abs(p.X()-footX) < 0.01 && bsp.Abs(p.Z()) < 3:
				foot = append(foot, f.Light[i].X())
			case p.X() < -8:
				open = append(open, f.Light[i].X())
			}
		}
	}
	if len(foot) == 0 || len(open) == 0 {
		t.Fatalf("found %d foot and %d open-floor vertices; the fixture has moved", len(foot), len(open))
	}
	lit := slices.Min(open)
	for _, l := range foot {
		if l > lit/2 {
			t.Fatalf("the foot of the wall's shaded side baked %v, open floor %v: the sun came through the wall", l, lit)
		}
	}
}

// A shadow edge gets the fine grid and open ground keeps the coarse one.
func TestTheGridIsFinerOnlyWhereAShadowEdgeCrosses(t *testing.T) {
	lvl := levelOf(
		boxFromTo("floor", mgl32.Vec3{-16, -1, -16}, mgl32.Vec3{16, 0, 16}),
		boxFromTo("slab", mgl32.Vec3{-3, 6, -3}, mgl32.Vec3{3, 7, 3}),
	)
	lvl.Lights = sunFrom(mgl32.Vec3{0, -1, 0})
	tr, _, _ := compiled(t, lvl)
	opt := DefaultLightOptions()
	BakeLight(tr, lvl, opt)

	span := func(p mgl32.Vec3) float32 {
		f := faceAt(t, tr, p, mgl32.Vec3{0, 1, 0})
		lo, hi := bsp.Bounds(f.Points)
		return max(hi.X()-lo.X(), hi.Z()-lo.Z())
	}
	// The slab's shadow edge is at x = 3, inside the grid cell 0..4.
	if s := span(mgl32.Vec3{3.1, 0, 1.4}); s > opt.MinGridStep+1e-3 {
		t.Errorf("floor under the shadow edge was cut %v wide, want at most %v", s, opt.MinGridStep)
	}
	if s := span(mgl32.Vec3{-12, 0, -12}); s < opt.GridStep-1e-3 {
		t.Errorf("open floor was cut %v wide, want the coarse %v", s, opt.GridStep)
	}
}

// turnedSlab is a 6 x 6 slab 6 units up, turned 30 degrees about Y so its
// shadow's edges run diagonally across the world-aligned sample grid.
func turnedSlab() world.Brush {
	b := boxFromTo("slab", mgl32.Vec3{-3, 6, -3}, mgl32.Vec3{3, 7, 3})
	rot := mgl32.HomogRotate3DY(mgl32.DegToRad(30))
	for i, v := range b.Verts {
		b.Verts[i] = rot.Mul4x1(v.Vec4(1)).Vec3()
	}
	return b
}

// A shadow edge diagonal to the grid bakes as coverage: vertices near it hold
// fractions of the sun, which interpolate to a straight edge, rather than a
// staircase of all-or-nothing vertices.
func TestADiagonalShadowEdgeBakesPartialCoverage(t *testing.T) {
	lvl := levelOf(boxFromTo("floor", mgl32.Vec3{-16, -1, -16}, mgl32.Vec3{16, 0, 16}), turnedSlab())
	lvl.Lights = sunFrom(mgl32.Vec3{0, -1, 0})
	tr, _, _ := compiled(t, lvl)
	BakeLight(tr, lvl, DefaultLightOptions())

	partial := 0
	for fi := range tr.Faces {
		f := &tr.Faces[fi]
		if f.Brush != "floor" || f.Plane.N.Y() < 0.99 {
			continue
		}
		for _, s := range f.SunShare {
			if s > 0.05 && s < 0.95 {
				partial++
			}
		}
	}
	if partial < 8 {
		t.Fatalf("%d floor vertices along the diagonal edge hold a partial share of the sun, want a band of them", partial)
	}
}

// Face.SunShare is the fraction of the baked sun each point kept: all of it in
// the open, none of it in the slab's shadow.
func TestSunShareIsWhatTheBakeKept(t *testing.T) {
	lvl := levelOf(
		boxFromTo("floor", mgl32.Vec3{-16, -1, -16}, mgl32.Vec3{16, 0, 16}),
		boxFromTo("slab", mgl32.Vec3{-4, 6, -4}, mgl32.Vec3{4, 7, 4}),
	)
	lvl.Lights = sunFrom(mgl32.Vec3{0, -1, 0})
	tr, _, _ := compiled(t, lvl)
	BakeLight(tr, lvl, DefaultLightOptions())

	// At the floor vertex at p — well clear of the slab's edge, whose own
	// vertices hold the fraction of their patch that is lit.
	share := func(p mgl32.Vec3) float32 {
		f := faceAt(t, tr, p, mgl32.Vec3{0, 1, 0})
		if len(f.SunShare) != len(f.Points) {
			t.Fatalf("face at %v has %d shares for %d points", p, len(f.SunShare), len(f.Points))
		}
		for i, q := range f.Points {
			if q.Sub(p).Len() < 1e-3 {
				return f.SunShare[i]
			}
		}
		t.Fatalf("no vertex at %v", p)
		return 0
	}
	if s := share(mgl32.Vec3{-12, 0, -12}); s != 1 {
		t.Errorf("open floor kept %v of the sun, want 1", s)
	}
	if s := share(mgl32.Vec3{0, 0, 0}); s != 0 {
		t.Errorf("floor under the slab kept %v of the sun, want 0", s)
	}
}
