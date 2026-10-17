// Package impostor bakes a cooked mesh's far stand-in: a few textured cards
// that look like it from a distance (docs/regions/PRESETS_5.md).
//
// A scanned tree is a few hundred clusters at its full detail and a few
// dozen DAG roots at its coarsest — and the roots are as coarse as it gets,
// however far away the tree stands: from 600 m up over a forest the biome
// showcase asked the visibility buffer for 1.19 M command records, most of
// them the roots of fourteen thousand scans (docs/regions/PRESETS_4.md). An
// impostor is what replaces them past a distance: eight vertical cards in a
// star round the trunk and one horizontal card through the crown, each a
// single quad, one cluster each, drawn by the same GPU cull, the same
// visibility buffer and the same material pass as the mesh — so it is lit,
// shadowed, fogged and TAA'd by exactly the code that lights the mesh
// (world/scenery_draw.go, render/nanite_draw_distance.go).
//
// # The cards
//
// Views vertical planes through the mesh's vertical axis, at angles kπ/Views,
// each TWO one-sided quads back to back: the one facing +n shows the mesh as
// seen from far out along +n, the one facing −n as seen from −n. Each quad is
// its own slot, so its own cluster with its own normal cone, and the cull's
// cone test drops whichever of the pair faces away from the camera — a quad
// is never seen from behind, where its baked normals would be mirrored
// wrong. They stand a hair (Offset) off the plane, so near edge-on, where
// the cone keeps both, the nearer wins the depth test cleanly. With Top, one
// more quad faces up through the crown (TopHeight up the mesh) for views from
// above.
//
// # The bake
//
// Each card is an orthographic view of the mesh's full detail (level 0),
// rasterised here on the CPU — no GL, so a generator can run it headless —
// Super times finer than kept: a pixel is the nearest triangle whose texel
// passes the alpha cutout (0.5, the shader's), its colour that texel times
// the vertex colour (and its cavity), its normal the interpolated vertex
// normal, turned toward the viewer as the shader turns a back face's. The
// kept texel averages its Super² samples weighted by coverage, its alpha the
// covered share; covered texels' colours and normals are bled into the clear
// ones round them so filtering a silhouette does not pull in black.
//
// The normal is stored in the CARD's tangent space — x along the card's +u,
// y along its +v (down the image, the engine's V), z out of the card — which
// is what fragment.glsl's cotangentFrame rebuilds from the card's UV
// derivatives, so the material pass lights the card per texel as the mesh's
// own normals would have been lit.
//
// The albedo is written as a coverage-preserving texture (a name ending
// _cov: texcompress/mip_coverage.go), so a distant card's mips keep the
// crown as dense as up close instead of thinning it away.
package impostor

import (
	"fmt"
	"image"
	"math"

	"foundry/internal/model"
	"foundry/internal/nanite"

	"github.com/go-gl/mathgl/mgl32"
)

// Version is the bake's own version, in every result's provenance: bump it
// when Bake makes different pixels or cards from the same mesh.
const Version = 3

// Options are the bake's settings; zero fields take the defaults.
type Options struct {
	// Views is how many vertical planes there are (two cards each); 4.
	Views int
	// Top adds the horizontal card; NoTop leaves it out.
	NoTop bool
	// TopHeight is where the horizontal card is, as a share of the mesh's
	// height from its base; 0.6.
	TopHeight float32
	// Cell is a card's texels on a side; 256.
	Cell int
	// Super is the bake's supersampling on a side; 2.
	Super int
	// Offset is how far each card stands off its plane, as a share of the
	// mesh's horizontal radius; 0.004.
	Offset float32
	// SideAngle and TopAngle are the half-angles, in degrees, of the view
	// directions a vertical card and the top card are drawn from
	// (SetViewCones); 35 and 65.
	SideAngle, TopAngle float32
}

func (o Options) withDefaults() Options {
	if o.Views <= 0 {
		o.Views = 4
	}
	if o.TopHeight <= 0 {
		o.TopHeight = 0.6
	}
	if o.Cell <= 0 {
		o.Cell = 256
	}
	if o.Super <= 0 {
		o.Super = 2
	}
	if o.Offset <= 0 {
		o.Offset = 0.004
	}
	if o.SideAngle <= 0 {
		o.SideAngle = 35
	}
	if o.TopAngle <= 0 {
		o.TopAngle = 65
	}
	return o
}

// Textures gives the bake a slot's texture by its engine name; nil (or an
// error) bakes the slot a flat grey, as an untextured slot.
type Textures func(name string) (image.Image, error)

// Result is a bake: the cards to cook, and the atlas their one slot texture
// is (Albedo) with its tangent-space normal map (Normal).
type Result struct {
	Mesh   *model.ImportMesh
	Albedo *image.NRGBA
	Normal *image.NRGBA
	// Cards is how many cards there are; Grid the atlas's cells on a side.
	Cards, Grid int
	// cards are the views, in slot order (SetViewCones).
	cards []card
	opt   Options
}

// card is one view: where it stands and what it looks along.
type card struct {
	// origin is the card's top-left corner in the mesh's frame; right and
	// down its edges (u and v run along them, 0 to 1); n the way it faces
	// (the view is from far out along n, looking back along -n).
	origin, right, down, n mgl32.Vec3
}

// cards lays the views out round the mesh's bounds.
func cards(lo, hi mgl32.Vec3, radius float32, o Options) []card {
	c := lo.Add(hi).Mul(0.5)
	var out []card
	off := radius * o.Offset
	for k := 0; k < o.Views; k++ {
		th := float64(k) * math.Pi / float64(o.Views)
		n := mgl32.Vec3{float32(-math.Sin(th)), 0, float32(math.Cos(th))}
		for _, side := range []float32{1, -1} {
			nn := n.Mul(side)
			// Seen from +nn, right is (-nn) x Y.
			right := nn.Mul(-1).Cross(mgl32.Vec3{0, 1, 0})
			base := mgl32.Vec3{c[0], hi[1], c[2]}.Add(nn.Mul(off))
			out = append(out, card{
				origin: base.Sub(right.Mul(radius)),
				right:  right.Mul(2 * radius),
				down:   mgl32.Vec3{0, lo[1] - hi[1], 0},
				n:      nn,
			})
		}
	}
	if !o.NoTop {
		y := lo[1] + (hi[1]-lo[1])*o.TopHeight
		// Seen from above, +x to the right and +z down the image.
		out = append(out, card{
			origin: mgl32.Vec3{c[0] - radius, y, c[2] - radius},
			right:  mgl32.Vec3{2 * radius, 0, 0},
			down:   mgl32.Vec3{0, 0, 2 * radius},
			n:      mgl32.Vec3{0, 1, 0},
		})
	}
	return out
}

// Bake bakes m's impostor. m must be a cooked mesh with its pages decoded
// (nanite.Mesh.Flat) or able to decode them.
func Bake(m *nanite.Mesh, tex Textures, opt Options) (*Result, error) {
	o := opt.withDefaults()
	if err := m.Flat(); err != nil {
		return nil, err
	}
	src, err := sourceTriangles(m, tex)
	if err != nil {
		return nil, err
	}
	if len(src.tris) == 0 {
		return nil, fmt.Errorf("impostor: the mesh has no triangles")
	}
	lo, hi := src.lo, src.hi
	cx, cz := (lo[0]+hi[0])/2, (lo[2]+hi[2])/2
	var radius float32
	for _, p := range src.pos {
		radius = max(radius, float32(math.Hypot(float64(p[0]-cx), float64(p[2]-cz))))
	}
	radius = max(radius*1.01, 1e-3)
	cs := cards(lo, hi, radius, o)
	grid := int(math.Ceil(math.Sqrt(float64(len(cs)))))
	side := grid * o.Cell
	res := &Result{Cards: len(cs), Grid: grid, cards: cs, opt: o,
		Albedo: image.NewNRGBA(image.Rect(0, 0, side, side)), Normal: image.NewNRGBA(image.Rect(0, 0, side, side))}
	// The clear texels of the atlas: no colour, a flat normal.
	for i := 0; i < len(res.Normal.Pix); i += 4 {
		res.Normal.Pix[i], res.Normal.Pix[i+1], res.Normal.Pix[i+2], res.Normal.Pix[i+3] = 128, 128, 255, 255
	}
	mesh := &model.ImportMesh{}
	for ci, cd := range cs {
		gx, gy := ci%grid, ci/grid
		view := src.render(cd, o.Cell*o.Super)
		alb, nor := resolve(view, cd, o.Cell, o.Super)
		blit(res.Albedo, alb, gx*o.Cell, gy*o.Cell, o.Cell)
		blit(res.Normal, nor, gx*o.Cell, gy*o.Cell, o.Cell)
		addQuad(mesh, cd, ci, gx, gy, grid, o.Cell)
	}
	res.Mesh = mesh
	return res, nil
}

// addQuad appends card ci as its own slot: two triangles wound to face n,
// its UVs its atlas cell (inset half a texel so filtering stays inside it).
func addQuad(m *model.ImportMesh, cd card, ci, gx, gy, grid, cell int) {
	base := uint32(len(m.Positions))
	g := float32(grid)
	inset := float32(0.5) / float32(grid*cell)
	u0, v0 := float32(gx)/g+inset, float32(gy)/g+inset
	u1, v1 := float32(gx+1)/g-inset, float32(gy+1)/g-inset
	corners := []mgl32.Vec3{cd.origin, cd.origin.Add(cd.right), cd.origin.Add(cd.right).Add(cd.down), cd.origin.Add(cd.down)}
	uvs := []mgl32.Vec2{{u0, v0}, {u1, v0}, {u1, v1}, {u0, v1}}
	for k := range corners {
		m.Positions = append(m.Positions, corners[k])
		m.Normals = append(m.Normals, cd.n)
		m.UVs = append(m.UVs, uvs[k])
	}
	tri := [6]uint32{0, 1, 2, 0, 2, 3}
	// Wound so the face normal is n.
	if corners[1].Sub(corners[0]).Cross(corners[2].Sub(corners[0])).Dot(cd.n) < 0 {
		tri = [6]uint32{0, 2, 1, 0, 3, 2}
	}
	for _, i := range tri {
		m.Indices = append(m.Indices, base+i)
	}
	m.TriSlot = append(m.TriSlot, int32(ci), int32(ci))
	m.Slots = append(m.Slots, model.ImportSlot{Name: fmt.Sprintf("card%d", ci), BaseColor: [4]float32{1, 1, 1, 1}})
}

// SetTexture names the texture every card wears.
func (r *Result) SetTexture(name string) {
	for i := range r.Mesh.Slots {
		r.Mesh.Slots[i].Texture = name
	}
}

// blit copies a cell x cell image into dst at (x, y).
func blit(dst, src *image.NRGBA, x, y, cell int) {
	for row := 0; row < cell; row++ {
		copy(dst.Pix[dst.PixOffset(x, y+row):dst.PixOffset(x+cell, y+row)], src.Pix[src.PixOffset(0, row):src.PixOffset(cell, row)])
	}
}
