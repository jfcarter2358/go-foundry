package impostor

import (
	"image"
	"math"
	"testing"

	"foundry/internal/cluster"
	"foundry/internal/nanite"

	"github.com/go-gl/mathgl/mgl32"
)

// coneMesh is a "tree": a closed cone, base radius r at y 0 and apex at y h,
// with outward smooth normals, as one cooked slot of one level-0 cluster.
func coneMesh(r, h float32, seg int) *nanite.Mesh {
	m := &nanite.Mesh{}
	slope := r / h
	add := func(p, n mgl32.Vec3) uint32 {
		m.Vertices = append(m.Vertices, p[0], p[1], p[2], 0.5, 0.5)
		m.Normals = append(m.Normals, n[0], n[1], n[2])
		return uint32(len(m.Vertices)/5 - 1)
	}
	for i := 0; i < seg; i++ {
		a0 := 2 * math.Pi * float64(i) / float64(seg)
		a1 := 2 * math.Pi * float64(i+1) / float64(seg)
		p0 := mgl32.Vec3{r * float32(math.Cos(a0)), 0, r * float32(math.Sin(a0))}
		p1 := mgl32.Vec3{r * float32(math.Cos(a1)), 0, r * float32(math.Sin(a1))}
		n0 := mgl32.Vec3{float32(math.Cos(a0)), slope, float32(math.Sin(a0))}.Normalize()
		n1 := mgl32.Vec3{float32(math.Cos(a1)), slope, float32(math.Sin(a1))}.Normalize()
		apex := mgl32.Vec3{0, h, 0}
		// Outward: (p1 - p0) x (apex - p0) points out for this order.
		ia, ib, ic := add(p0, n0), add(apex, n0.Add(n1).Normalize()), add(p1, n1)
		m.Indices = append(m.Indices, ia, ib, ic)
	}
	nt := uint32(len(m.Indices) / 3)
	m.Clusters = []nanite.Cluster{{Cluster: cluster.Cluster{TriFirst: 0, TriCount: nt}}}
	m.Slots = []nanite.Slot{{Name: "bark", Smooth: true, First: 0, Count: 1, BaseCount: 1}}
	m.Lo, m.Hi = mgl32.Vec3{-r, 0, -r}, mgl32.Vec3{r, h, r}
	return m
}

func coverageIn(img *image.NRGBA, x0, y0, x1, y1 int) int {
	n := 0
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			if img.Pix[img.PixOffset(x, y)+3] >= 128 {
				n++
			}
		}
	}
	return n
}

func TestBakeMakesNineCardsAndTheirAtlas(t *testing.T) {
	res, err := Bake(coneMesh(1, 4, 48), nil, Options{Cell: 64})
	if err != nil {
		t.Fatal(err)
	}
	if res.Cards != 9 || res.Grid != 3 {
		t.Fatalf("%d cards in a %dx%d grid, want 9 in 3x3", res.Cards, res.Grid, res.Grid)
	}
	if b := res.Albedo.Bounds(); b.Dx() != 192 || b.Dy() != 192 {
		t.Fatalf("atlas %v, want 192x192", b)
	}
	if len(res.Mesh.Slots) != 9 || len(res.Mesh.TriSlot) != 18 || len(res.Mesh.Indices) != 54 {
		t.Fatalf("%d slots, %d triangles: want one two-triangle slot a card", len(res.Mesh.Slots), len(res.Mesh.TriSlot))
	}
	// Every card is wound to face the way its normal says (the cull's cone
	// test reads the winding).
	m := res.Mesh
	for ti := 0; ti < len(m.Indices)/3; ti++ {
		a, b, c := m.Positions[m.Indices[3*ti]], m.Positions[m.Indices[3*ti+1]], m.Positions[m.Indices[3*ti+2]]
		if b.Sub(a).Cross(c.Sub(a)).Dot(m.Normals[m.Indices[3*ti]]) <= 0 {
			t.Errorf("triangle %d faces away from its card's normal", ti)
		}
	}
	// The pairs face opposite ways; the last card faces up.
	if n0, n1 := m.Normals[0], m.Normals[4]; n0.Dot(n1) > -0.99 {
		t.Errorf("a plane's two cards face %v and %v, not back to back", n0, n1)
	}
	if up := m.Normals[len(m.Normals)-1]; up.Y() < 0.99 {
		t.Errorf("the top card faces %v, not up", up)
	}
}

func TestBakeSeesTheShapeAndItsNormals(t *testing.T) {
	const cell = 64
	res, err := Bake(coneMesh(1, 4, 64), nil, Options{Cell: cell})
	if err != nil {
		t.Fatal(err)
	}
	// Card 0, seen from the side: a cone narrow at the top (v = 0), wide at
	// the base.
	top := coverageIn(res.Albedo, 0, 0, cell, cell/4)
	bottom := coverageIn(res.Albedo, 0, 3*cell/4, cell, cell)
	if bottom < 3*top || bottom == 0 {
		t.Errorf("side view: %d covered texels in its top quarter, %d in its bottom: not a cone", top, bottom)
	}
	// The top card (cell 8, at 2,2), from above: a disc — its centre covered,
	// its corner not.
	cx, cy := 2*cell+cell/2, 2*cell+cell/2
	if res.Albedo.Pix[res.Albedo.PixOffset(cx, cy)+3] < 128 || res.Albedo.Pix[res.Albedo.PixOffset(2*cell+1, 2*cell+1)+3] > 0 {
		t.Errorf("top view: centre alpha %d, corner %d: want a disc", res.Albedo.Pix[res.Albedo.PixOffset(cx, cy)+3],
			res.Albedo.Pix[res.Albedo.PixOffset(2*cell+1, 2*cell+1)+3])
	}
	// Card 0's normals in its tangent space: right of the axis they lean
	// toward +u, left toward -u; up the slope (against +v, which runs down);
	// out of the card always.
	ts := func(x, y int) mgl32.Vec3 {
		o := res.Normal.PixOffset(x, y)
		return mgl32.Vec3{float32(res.Normal.Pix[o])/127.5 - 1, float32(res.Normal.Pix[o+1])/127.5 - 1, float32(res.Normal.Pix[o+2])/127.5 - 1}
	}
	right, left := ts(cell/2+cell/6, 3*cell/4), ts(cell/2-cell/6, 3*cell/4)
	if right.X() < 0.2 || left.X() > -0.2 {
		t.Errorf("tangent normals right %v, left %v: want +x on the right, -x on the left", right, left)
	}
	if right.Y() > -0.05 || right.Z() < 0.3 {
		t.Errorf("tangent normal %v: want up the slope (-y) and out of the card (+z)", right)
	}
}

func TestBakeCutsTheTexturesClearTexels(t *testing.T) {
	// A texture clear on its left half: a cone whose UVs all sit there is
	// not there at all.
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			o := img.PixOffset(x, y)
			img.Pix[o], img.Pix[o+1], img.Pix[o+2] = 200, 40, 40
			if x >= 4 {
				img.Pix[o+3] = 255
			}
		}
	}
	m := coneMesh(1, 4, 32)
	m.Slots[0].Texture = "leaf"
	for v := 0; v < len(m.Vertices)/5; v++ {
		m.Vertices[5*v+3] = 0.2 // the clear half
	}
	res, err := Bake(m, func(string) (image.Image, error) { return img, nil }, Options{Cell: 32})
	if err != nil {
		t.Fatal(err)
	}
	if n := coverageIn(res.Albedo, 0, 0, 96, 96); n != 0 {
		t.Errorf("%d texels covered by triangles whose every texel is clear", n)
	}
	for v := 0; v < len(m.Vertices)/5; v++ {
		m.Vertices[5*v+3] = 0.8 // the opaque half
	}
	res, err = Bake(m, func(string) (image.Image, error) { return img, nil }, Options{Cell: 32})
	if err != nil {
		t.Fatal(err)
	}
	o := res.Albedo.PixOffset(16, 28)
	if a, r := res.Albedo.Pix[o+3], res.Albedo.Pix[o]; a < 128 || r < 150 {
		t.Errorf("the opaque half baked alpha %d, red %d: want the texture's red", a, r)
	}
}

func TestPathsSitBesideTheMesh(t *testing.T) {
	p := PathsFor("models/polyhaven/fir_tree_01/fir_tree_01_a_lod0_c50000.fnm")
	if p.Model != "models/polyhaven/fir_tree_01/fir_tree_01_a_lod0_c50000_imp.fnm" ||
		p.Provenance != "models/polyhaven/fir_tree_01/fir_tree_01_a_lod0_c50000_imp.yaml" ||
		p.Texture != "ph_imp_fir_tree_01_a_lod0_c50000_cov" {
		t.Errorf("paths %+v", p)
	}
}

func TestViewConesDrawOneCardFromTheSideAndTheTopFromAbove(t *testing.T) {
	res, err := Bake(coneMesh(1, 4, 24), nil, Options{Cell: 16})
	if err != nil {
		t.Fatal(err)
	}
	// The cooked cards stand in for here: a cluster a card, as the cook
	// makes them (a slot's two triangles are one cluster).
	m := &nanite.Mesh{}
	for si := range res.Mesh.Slots {
		m.Slots = append(m.Slots, nanite.Slot{First: int32(si), Count: 1})
		m.Clusters = append(m.Clusters, nanite.Cluster{})
	}
	SetViewCones(m, res)
	drawn := func(eye mgl32.Vec3) (side, top int) {
		for ci := range m.Clusters {
			if m.Clusters[ci].BackfacingFrom(eye) {
				continue
			}
			if ci == len(m.Clusters)-1 {
				top++
			} else {
				side++
			}
		}
		return
	}
	for az := 0.0; az < 360; az += 5 {
		a := az * math.Pi / 180
		eye := mgl32.Vec3{float32(1000 * math.Sin(a)), 2, float32(1000 * math.Cos(a))}
		if side, top := drawn(eye); side < 1 || side > 2 || top != 0 {
			t.Errorf("from the side at %g°: %d side cards, %d top: want one or two side cards and no top", az, side, top)
		}
	}
	if side, top := drawn(mgl32.Vec3{3, 1000, 2}); side != 0 || top != 1 {
		t.Errorf("from above: %d side cards, %d top; want the top alone", side, top)
	}
	// Every elevation is covered by something.
	for el := 0.0; el <= 90; el += 2 {
		for az := 0.0; az < 360; az += 7 {
			e, a := el*math.Pi/180, az*math.Pi/180
			eye := mgl32.Vec3{float32(1000 * math.Cos(e) * math.Sin(a)), 2.4 + float32(1000*math.Sin(e)), float32(1000 * math.Cos(e) * math.Cos(a))}
			if side, top := drawn(eye); side+top == 0 {
				t.Fatalf("at elevation %g°, azimuth %g°: no card is drawn", el, az)
			}
		}
	}
}
