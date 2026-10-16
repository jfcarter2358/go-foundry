package grassfield

// card.go — a CARD kind's mesh: leaf litter as a flat alpha-cut patch lying
// on the ground (KindCard; docs/terrain-paint/PHASE_P3.md).
//
// A card is one square of side 1 m at scale 1, centred on the clump's root,
// in the clump's own frame (Y up, so an oriented card lies along the
// ground's normal). It is a 3 × 3 grid of vertices rather than one quad so
// it can CUP: the rim is lifted by Cup × r² (r the distance from the
// centre, 0..1 at a corner), which keeps the edges of a card on a hollow
// above the ground the tangent plane would put them under. Its texture is
// one tile of an atlas: tile (i, j) of an n × n grid of the source image,
// so four kinds made from the same cut-out share one texture — one draw
// group — and differ in which part of the scan they show.
//
// Every vertex is lit as the ground (normal +Y) and carries wind weights
// up = 1 (no self-shading toward the base: a card IS the base), out = 0,
// bend and flutter 0 (a card never sways).

// CardCup is the rim lift of a card, as a fraction of its side.
const CardCup = 0.03

// CardMesh is the mesh of atlas tile (i, j) of an n × n atlas.
func CardMesh(i, j, n int) Mesh {
	if n < 1 {
		n = 1
	}
	u0, v0 := float32(i)/float32(n), float32(j)/float32(n)
	du := 1 / float32(n)
	var m Mesh
	for gz := 0; gz < 3; gz++ {
		for gx := 0; gx < 3; gx++ {
			fx, fz := float32(gx)/2, float32(gz)/2
			x, z := fx-0.5, fz-0.5
			r2 := (x*x + z*z) * 2 // 1 at a corner
			m.Vertices = append(m.Vertices,
				x, CardCup*r2, z, // position
				0, 1, 0, // normal
				u0+fx*du, v0+fz*du, // UV
				1, 0, 0, 0) // up, out, bend, flutter
		}
	}
	for gz := 0; gz < 2; gz++ {
		for gx := 0; gx < 2; gx++ {
			a := uint32(gz*3 + gx)
			b, c, d := a+1, a+3, a+4
			// Counter-clockwise seen from above (+Y).
			m.Indices = append(m.Indices, a, c, b, b, c, d)
		}
	}
	return m
}
