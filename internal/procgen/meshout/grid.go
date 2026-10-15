package meshout

// grid.go — the one mesh shape every terrain-like generator needs: a
// heightfield over a rectangle, optionally closed into a solid.
//
// HeightGrid is a top surface of (nx+1) x (nz+1) samples. Closed adds skirts
// down to a base height around the four sides and a bottom face, so the tile
// is watertight: a cooked mesh with `collision: mesh` is then one parity solid
// to nav, rather than one solid per triangle (see the spec's engine facts).
// The top's rim stays exactly on the rectangle, so a cook with LockRect set to
// the rectangle pins the seam between neighbouring tiles. Its normals are
// the whole field's when Normal is set (every terrain stage sets it): a
// normal from the tile's own triangles alone differs from its neighbour's
// at the shared rim, which drew a straight shading line along every tile
// edge (docs/regions/PHASE_4.md).

import (
	"github.com/go-gl/mathgl/mgl32"
)

// HeightGrid describes a heightfield tile.
type HeightGrid struct {
	// X0, Z0 is the tile's corner; Size its extent along X and Z.
	X0, Z0, SizeX, SizeZ float32
	// NX, NZ are the cells across (samples are one more each way).
	NX, NZ int
	// Height is the surface height at sample (i, j), 0 <= i <= NX.
	Height func(i, j int) float32
	// UVScale maps world metres to UVs (UV = position·UVScale + UVOffset);
	// 0 is 1/8.
	UVScale float32
	// UVOffset shifts the top's UVs: a tile of a bigger field sets it so
	// its UVs continue its neighbours' (one image across every tile).
	UVOffset mgl32.Vec2
	// Closed adds skirts and a bottom at Base.
	Closed bool
	Base   float32
	// Normal, when set, is the top's normal at sample (i, j) — from the
	// whole field the tile is cut from, so a tile's rim shades exactly as
	// its neighbour's rim does (FieldNormal). Nil: the area-weighted normal
	// of the tile's own triangles, which at the rim sees only one side of
	// the seam and shades every tile edge as a line.
	Normal func(i, j int) mgl32.Vec3
	// Skip, when set, leaves the top's quad (i, j) — between samples (i, j)
	// and (i+1, j+1) — out: a hole, where a cave passes through the ground
	// (stages/cave.go). A tile with holes is open: set Closed only when no
	// quad is skipped.
	Skip func(i, j int) bool
}

// FieldNormal is the unit normal of a heightfield at sample (i, j) by
// central differences over step samples each way (one-sided where the
// field ends): at(i, j) is the height, clamped by the caller's field to
// 0..ni and 0..nj; cell the sample spacing. Two tiles cut from one field
// compute the same normal for a shared rim sample, whatever side of the
// seam they are on.
func FieldNormal(at func(i, j int) float32, i, j, ni, nj, step int, cell float64) mgl32.Vec3 {
	step = max(step, 1)
	il, ir := max(i-step, 0), min(i+step, ni)
	jl, jr := max(j-step, 0), min(j+step, nj)
	var hx, hz float64
	if ir > il {
		hx = float64(at(ir, j)-at(il, j)) / (float64(ir-il) * cell)
	}
	if jr > jl {
		hz = float64(at(i, jr)-at(i, jl)) / (float64(jr-jl) * cell)
	}
	return mgl32.Vec3{float32(-hx), 1, float32(-hz)}.Normalize()
}

// Rect is the tile's XZ rectangle as nanitecook.Options.LockRect wants it.
func (g HeightGrid) Rect() [4]float32 {
	return [4]float32{g.X0, g.Z0, g.X0 + g.SizeX, g.Z0 + g.SizeZ}
}

// Build adds the tile to b (into its current slot) and welds it.
func (g HeightGrid) Build(b *Builder) {
	uvs := g.UVScale
	if uvs == 0 {
		uvs = 1.0 / 8
	}
	at := func(i, j int) mgl32.Vec3 {
		x := g.X0 + g.SizeX*float32(i)/float32(g.NX)
		z := g.Z0 + g.SizeZ*float32(j)/float32(g.NZ)
		return mgl32.Vec3{x, g.Height(i, j), z}
	}
	base := uint32(b.VertexCount())
	row := uint32(g.NX + 1)
	for j := 0; j <= g.NZ; j++ {
		for i := 0; i <= g.NX; i++ {
			p := at(i, j)
			var n mgl32.Vec3
			if g.Normal != nil {
				n = g.Normal(i, j)
			}
			b.Vertex(p, n, mgl32.Vec2{p[0]*uvs + g.UVOffset[0], p[2]*uvs + g.UVOffset[1]})
		}
	}
	for j := 0; j < g.NZ; j++ {
		for i := 0; i < g.NX; i++ {
			if g.Skip != nil && g.Skip(i, j) {
				continue
			}
			a := base + uint32(j)*row + uint32(i)
			// (i,j) -> (i,j+1) -> (i+1,j+1) -> (i+1,j) is counter-clockwise
			// seen from above (+Y) with +Z toward the viewer's bottom.
			b.Quad(a, a+row, a+row+1, a+1)
		}
	}
	if g.Closed {
		g.closeTile(b, at, uvs)
	}
	// Welded keeping UVs: the skirts' UVs differ from the top's, so the rim
	// stays split and the top's normals are the top's alone — a rim normal
	// averaged with its skirt would tilt outward and show every tile seam.
	// The mesh is still closed by position, which is what nav and the cook's
	// Closed test read.
	b.Weld(0, true)
}

// closeTile adds the four skirts and the bottom.
func (g HeightGrid) closeTile(b *Builder, at func(i, j int) mgl32.Vec3, uvs float32) {
	// The rim: along z = Z0 (j = 0) with x rising, then along x = X1 with z
	// rising, back along z = Z1, back along x = X0 — clockwise seen from
	// above (-Y is the normal of that turn).
	var rim []mgl32.Vec3
	for i := 0; i < g.NX; i++ {
		rim = append(rim, at(i, 0))
	}
	for j := 0; j < g.NZ; j++ {
		rim = append(rim, at(g.NX, j))
	}
	for i := g.NX; i > 0; i-- {
		rim = append(rim, at(i, g.NZ))
	}
	for j := g.NZ; j > 0; j-- {
		rim = append(rim, at(0, j))
	}
	n := len(rim)
	for k := 0; k < n; k++ {
		p, q := rim[k], rim[(k+1)%n]
		pb, qb := mgl32.Vec3{p[0], g.Base, p[2]}, mgl32.Vec3{q[0], g.Base, q[2]}
		// Wound so the skirt faces OUT of the tile.
		a := b.Vertex(p, mgl32.Vec3{}, mgl32.Vec2{(p[0] + p[2]) * uvs, p[1] * uvs})
		c := b.Vertex(pb, mgl32.Vec3{}, mgl32.Vec2{(p[0] + p[2]) * uvs, g.Base * uvs})
		d := b.Vertex(qb, mgl32.Vec3{}, mgl32.Vec2{(q[0] + q[2]) * uvs, g.Base * uvs})
		e := b.Vertex(q, mgl32.Vec3{}, mgl32.Vec2{(q[0] + q[2]) * uvs, q[1] * uvs})
		b.Tri(a, d, c)
		b.Tri(a, e, d)
	}
	// The bottom: a fan from the rectangle's centre over the rim's base,
	// facing down. From the centre, so no triangle is a sliver along a side
	// (a fan from a corner would lay every point of its own two sides in a
	// line with it).
	down := mgl32.Vec3{0, -1, 0}
	cx, cz := g.X0+g.SizeX/2, g.Z0+g.SizeZ/2
	centre := b.Vertex(mgl32.Vec3{cx, g.Base, cz}, down, mgl32.Vec2{cx * uvs, cz * uvs})
	ring := make([]uint32, n)
	for k := 0; k < n; k++ {
		x, z := rim[k][0], rim[k][2]
		ring[k] = b.Vertex(mgl32.Vec3{x, g.Base, z}, down, mgl32.Vec2{x * uvs, z * uvs})
	}
	for k := 0; k < n; k++ {
		b.Tri(centre, ring[k], ring[(k+1)%n]) // the rim runs clockwise from above, so this faces down
	}
}
