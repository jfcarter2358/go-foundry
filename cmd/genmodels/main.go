// genmodels writes the .fmdl models that ship with the engine.
//
// Run from the repo root: go run ./cmd/genmodels  (or `make models`).
//
// The same argument gentextures makes for textures, made for meshes: a hull
// assembled out of thirty tapered boxes is a thing a program never gets wrong
// and a hand never gets right twice. Everything here goes out through
// model.SaveModel, so a model that would not load is a failed generator run
// rather than a file the engine refuses at startup.
//
// Coordinates are the ones the entity places the model in: +X starboard, +Y up,
// +Z forward, and y = 0 is the deck the interior chunk is authored on. The
// numbers in dropship() are therefore the *same* numbers as in
// resources/chunks/dropship_hold.yaml, and the comments say which are shared.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"foundry/internal/model"

	"github.com/go-gl/mathgl/mgl32"
)

// texelsPerMetre is how the UVs are scaled: one tile of a 64-pixel texture
// every two metres, on every face, whatever its size. Planar per face rather
// than unwrapped, because the hull wears a *tiling* material rather than a
// painted atlas — see ModelDoc.Texture on the difference.
const metresPerTile = 2.0

// section is one end of a part: a trapezoid in the XY plane at a given Z.
//
// Two of them make a part, which is the only shape this file builds. It is
// enough for a hull: a fuselage is a box that narrows, a wing is a box that
// thins, a nacelle is a box. What it cannot do is curve, and nothing here does.
type section struct {
	Z          float32
	HalfBottom float32 // half-width along the section's bottom edge
	HalfTop    float32 // …and along its top edge, for a chined or tapered face
	Bottom     float32 // y of the bottom edge
	Top        float32 // y of the top edge
	XOff       float32 // the whole section, shifted sideways
}

// corners returns the section's four points, bottom-left first and going the
// way part() expects: bottom-left, bottom-right, top-right, top-left.
func (s section) corners() [4]mgl32.Vec3 {
	return [4]mgl32.Vec3{
		{s.XOff - s.HalfBottom, s.Bottom, s.Z},
		{s.XOff + s.HalfBottom, s.Bottom, s.Z},
		{s.XOff + s.HalfTop, s.Top, s.Z},
		{s.XOff - s.HalfTop, s.Top, s.Z},
	}
}

// builder accumulates parts into one document.
type builder struct {
	doc *model.ModelDoc
}

func newBuilder(id, texture string) *builder {
	return &builder{doc: &model.ModelDoc{ID: id, Texture: texture}}
}

// part adds the hexahedron between two sections as six quads.
//
// The winding is fixed here once and never derived per part: with the eight
// vertices laid out bottom-then-top and aft-then-fore, the six faces below are
// the only orderings that wind counter-clockwise seen from outside. Getting one
// backwards produces a shape that looks right until it is lit, which is exactly
// the mistake a generator exists to stop making.
func (b *builder) part(aft, fore section) {
	ac, fc := aft.corners(), fore.corners()
	base := len(b.doc.Verts)
	b.doc.Verts = append(b.doc.Verts,
		ac[0], ac[1], fc[1], fc[0], // 0..3: the bottom ring, CCW seen from above
		ac[3], ac[2], fc[2], fc[3], // 4..7: the top ring, in the same order
	)
	for _, f := range [][4]int{
		{0, 1, 2, 3}, // bottom
		{4, 7, 6, 5}, // top
		{0, 4, 5, 1}, // aft
		{1, 5, 6, 2}, // starboard
		{2, 6, 7, 3}, // fore
		{3, 7, 4, 0}, // port
	} {
		b.quad(base+f[0], base+f[1], base+f[2], base+f[3])
	}
}

// quad appends one face and gives it planar UVs off its own normal.
func (b *builder) quad(a, bi, c, d int) {
	verts := []int{a, bi, c, d}
	n := faceNormal(b.doc.Verts, verts)
	uvs := make([]mgl32.Vec2, len(verts))
	for i, vi := range verts {
		uvs[i] = planarUV(b.doc.Verts[vi], n)
	}
	b.doc.Faces = append(b.doc.Faces, model.ModelFace{Verts: verts, UVs: uvs})
}

// mirrored adds a part and the one on the other side of the hull.
//
// A section is symmetric about its own XOff, so the far half is the same shape
// at the negated offset — a translation, not a reflection, and so no winding to
// put back. That is the whole reason section carries an offset instead of a
// pair of X bounds: a mirrored part whose faces ended up inside out looks
// correct in wireframe and is lit backwards, and this way it cannot happen.
func (b *builder) mirrored(aft, fore section) {
	b.part(aft, fore)
	aft.XOff, fore.XOff = -aft.XOff, -fore.XOff
	b.part(aft, fore)
}

// faceNormal is Newell's method: it needs no assumption that the four points
// are exactly planar, which a tapered part's side faces are not.
func faceNormal(verts []mgl32.Vec3, idx []int) mgl32.Vec3 {
	var n mgl32.Vec3
	for i := range idx {
		p, q := verts[idx[i]], verts[idx[(i+1)%len(idx)]]
		n = n.Add(mgl32.Vec3{
			(p.Y() - q.Y()) * (p.Z() + q.Z()),
			(p.Z() - q.Z()) * (p.X() + q.X()),
			(p.X() - q.X()) * (p.Y() + q.Y()),
		})
	}
	if n.Len() < 1e-9 {
		return mgl32.Vec3{0, 1, 0}
	}
	return n.Normalize()
}

// planarUV projects a point onto the two axes the face is most nearly parallel
// to. Metre-scaled, so plating is the same size everywhere on the hull.
func planarUV(p mgl32.Vec3, n mgl32.Vec3) mgl32.Vec2 {
	ax, ay, az := abs32(n.X()), abs32(n.Y()), abs32(n.Z())
	switch {
	case ax >= ay && ax >= az:
		return mgl32.Vec2{p.Z() / metresPerTile, -p.Y() / metresPerTile}
	case ay >= az:
		return mgl32.Vec2{p.X() / metresPerTile, p.Z() / metresPerTile}
	default:
		return mgl32.Vec2{p.X() / metresPerTile, -p.Y() / metresPerTile}
	}
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func main() {
	out := filepath.Join("resources", "models")
	if err := os.MkdirAll(out, 0o755); err != nil {
		panic(err)
	}
	for _, m := range []*model.ModelDoc{dropshipHull(), dropshipGlass()} {
		path := filepath.Join(out, m.ID+model.ModelExt)
		if err := model.SaveModel(m, path); err != nil {
			panic(err)
		}
		fmt.Printf("%s: %d vertices, %d faces\n", path, len(m.Verts), len(m.Faces))
	}
}

// ---------------------------------------------------------------------------
// The dropship.
//
// A blunt, boxy assault shuttle: slab fuselage, twin engine pods on stub
// pylons, a stepped nose over a chin turret, and a boarding tunnel projecting
// aft. The hold inside it is not modelled here at all — it is world geometry
// (resources/chunks/dropship_hold.yaml), because a room the player walks around
// in has to be sectors and not triangles. This is only the skin over it, which
// is why the constants it shares with that file are named and not inlined.
// ---------------------------------------------------------------------------

const (
	// Shared with dropship_hold.yaml — the hold's inside, which this is the
	// skin over. Get one of them wrong and the deck pokes through the belly.
	bayHalf  = 2.2  // the hold's inside half-width
	bayFore  = 7.2  // its forward bulkhead
	bayCeil  = 2.6  // its ceiling
	vestHalf = 1.3  // the aft vestibule's inside half-width
	vestAft  = -3.6 // where the vestibule ends, and the frame portal is
	vestCeil = 2.25
	vestDeck = 0.03 // the vestibule's floor, a lip above the ground outside

	shell = 0.4 // hull thickness: the gap between those and the outside

	hullHalf = bayHalf + shell // 2.6
	hullTop  = bayCeil + shell // 3.0
	hullAft  = -0.35           // just aft of the hatch, where the tunnel starts
	hullFore = bayFore + shell

	// The ship parks with its deck level with the ground: the hold is authored
	// flat, the ground outside is at y = 0 too, and one walks straight in over
	// the vestibule's three-centimetre lip. The hull carries no landing gear
	// and stands on nothing.
	//
	// The alternative — skids and a ramp down to the ground — was tried and
	// abandoned. A frame portal hands the walker over while they are still some
	// way back from the doorway (collision.portalCrossingBand), so a ramp means
	// crossing between two floors at different heights and being snapped up the
	// difference. Flat is the shape with no seam in it.
	//
	// hullBottom is therefore under the ground rather than on it: the structure
	// below the deck is real, and simply buried by the floor the ship stands
	// on. Nothing below y = 0 is ever seen.
	hullBottom = -0.62
	groundLine = 0.0
	// noseLift is how far the underside has risen by the nose. It has to clear
	// the ground by more than hullBottom is under it, or the chin turret hangs
	// in the dirt.
	noseLift = 1.30

	// bellyTop and spineBase are where the fuselage's three bands meet, and
	// they are just OUTSIDE the hold rather than anywhere convenient. A band
	// boundary is a pair of faces, and a face inside the hold is a plate
	// hanging in the middle of the room: the deck and ceiling are the chunk's,
	// drawn at 0 and bayCeil, and they only hide the hull's structure if none
	// of it is in between. Everything else here is checked against the same
	// rule by the generator's own test.
	// bellyTop clears the doorway's own floor (the six-centimetre track the
	// blast door seats in) and not just the deck, because the fuselage runs aft
	// past the doorway: a face at deck level would cross the threshold five
	// centimetres up, in the one place the player walks through slowly.
	bellyTop  = -0.08
	spineBase = bayCeil + 0.01
)

func dropshipHull() *model.ModelDoc {
	b := newBuilder("dropship", "hull_plate")

	// --- fuselage ------------------------------------------------------------
	//
	// A SHELL, not a stack of solid boxes, and that is the whole difficulty of
	// modelling a hull around a room. The underfloor and the spine can be solid
	// slabs because they sit entirely below the deck and above the ceiling. The
	// body between them cannot: an end cap anywhere along its length is a plate
	// standing across the hold or across the doorway, and there is nowhere to
	// put one that is not in one or the other. So the sides run the full length
	// with no caps, and the two bulkheads are built as rings around the
	// openings they have to leave — the doorway aft, and nothing forward.
	//
	// The bulkheads also sit a centimetre outside the chunk's own walls rather
	// than flush with them. Two coplanar surfaces are a stripe of flicker; one
	// behind the other is hidden by it.
	const skin = 0.39 // side thickness: hullHalf back to just outside bayHalf
	const jamb = 1.4  // the doorway's half-width, from dropship_hold.yaml
	const lintel = 2.41

	b.part( // underfloor, chined
		section{Z: hullAft, HalfBottom: 2.05, HalfTop: hullHalf, Bottom: hullBottom, Top: bellyTop},
		section{Z: hullFore, HalfBottom: 2.05, HalfTop: hullHalf, Bottom: hullBottom, Top: bellyTop},
	)
	b.part( // spine, tapered
		section{Z: hullAft, HalfBottom: hullHalf, HalfTop: 2.10, Bottom: spineBase, Top: hullTop},
		section{Z: hullFore, HalfBottom: hullHalf, HalfTop: 2.10, Bottom: spineBase, Top: hullTop},
	)
	b.mirrored( // flanks, end to end
		section{Z: hullAft, HalfBottom: skin / 2, HalfTop: skin / 2, Bottom: bellyTop, Top: spineBase, XOff: hullHalf - skin/2},
		section{Z: hullFore, HalfBottom: skin / 2, HalfTop: skin / 2, Bottom: bellyTop, Top: spineBase, XOff: hullHalf - skin/2},
	)
	b.mirrored( // aft bulkhead, either side of the doorway
		section{Z: hullAft, HalfBottom: (hullHalf - skin - jamb) / 2, HalfTop: (hullHalf - skin - jamb) / 2,
			Bottom: bellyTop, Top: spineBase, XOff: jamb + (hullHalf-skin-jamb)/2},
		section{Z: -0.01, HalfBottom: (hullHalf - skin - jamb) / 2, HalfTop: (hullHalf - skin - jamb) / 2,
			Bottom: bellyTop, Top: spineBase, XOff: jamb + (hullHalf-skin-jamb)/2},
	)
	b.part( // …and over it
		section{Z: hullAft, HalfBottom: jamb, HalfTop: jamb, Bottom: lintel, Top: spineBase},
		section{Z: -0.01, HalfBottom: jamb, HalfTop: jamb, Bottom: lintel, Top: spineBase},
	)
	b.part( // forward bulkhead, behind the hold's own
		section{Z: bayFore + 0.01, HalfBottom: hullHalf - skin, HalfTop: hullHalf - skin, Bottom: bellyTop, Top: spineBase},
		section{Z: hullFore, HalfBottom: hullHalf - skin, HalfTop: hullHalf - skin, Bottom: bellyTop, Top: spineBase},
	)

	// --- nose: narrows forward of the hold's bulkhead, and lifts clear ---
	b.part(
		section{Z: hullFore, HalfBottom: 2.30, HalfTop: 2.55, Bottom: hullBottom, Top: spineBase},
		section{Z: 10.2, HalfBottom: 1.20, HalfTop: 1.55, Bottom: hullBottom + noseLift, Top: 1.60},
	)
	b.part(
		section{Z: hullFore, HalfBottom: 2.55, HalfTop: 2.10, Bottom: spineBase, Top: hullTop},
		section{Z: 10.2, HalfBottom: 1.55, HalfTop: 1.25, Bottom: 1.60, Top: 1.95},
	)

	// --- cockpit, with the canopy glass carrying on forward as its own model ---
	b.part(
		section{Z: 8.4, HalfBottom: 1.50, HalfTop: 1.35, Bottom: 1.90, Top: 3.00},
		section{Z: 10.0, HalfBottom: 1.25, HalfTop: 1.15, Bottom: 1.62, Top: 2.55},
	)

	// --- chin turret and its two barrels, in the space the nose lifted open ---
	b.part(
		section{Z: 8.5, HalfBottom: 0.58, HalfTop: 0.62, Bottom: 0.10, Top: 0.95},
		section{Z: 10.3, HalfBottom: 0.48, HalfTop: 0.52, Bottom: 0.18, Top: 0.90},
	)
	b.mirrored(
		section{Z: 10.2, HalfBottom: 0.10, HalfTop: 0.10, Bottom: 0.36, Top: 0.58, XOff: 0.26},
		section{Z: 11.4, HalfBottom: 0.08, HalfTop: 0.08, Bottom: 0.38, Top: 0.56, XOff: 0.26},
	)

	// --- stub pylons out to the engine pods ---
	b.mirrored(
		section{Z: 1.0, HalfBottom: 1.05, HalfTop: 1.05, Bottom: 1.05, Top: 1.75, XOff: 3.60},
		section{Z: 4.8, HalfBottom: 1.05, HalfTop: 1.05, Bottom: 1.15, Top: 1.70, XOff: 3.60},
	)

	// --- engine nacelles: the pod, its intake lip, and its nozzle ---
	b.mirrored(
		section{Z: -0.80, HalfBottom: 1.05, HalfTop: 1.20, Bottom: 0.55, Top: 2.25, XOff: 5.15},
		section{Z: 5.20, HalfBottom: 1.10, HalfTop: 1.20, Bottom: 0.40, Top: 2.30, XOff: 5.15},
	)
	b.mirrored(
		section{Z: 5.20, HalfBottom: 1.20, HalfTop: 1.20, Bottom: 0.40, Top: 2.30, XOff: 5.15},
		section{Z: 5.85, HalfBottom: 1.02, HalfTop: 1.02, Bottom: 0.60, Top: 2.10, XOff: 5.15},
	)
	b.mirrored(
		section{Z: -1.55, HalfBottom: 0.98, HalfTop: 0.98, Bottom: 0.72, Top: 2.05, XOff: 5.15},
		section{Z: -0.80, HalfBottom: 1.18, HalfTop: 1.18, Bottom: 0.58, Top: 2.20, XOff: 5.15},
	)

	// --- raked twin fins ---
	b.mirrored(
		section{Z: -0.20, HalfBottom: 0.22, HalfTop: 0.18, Bottom: hullTop, Top: 4.55, XOff: 1.75},
		section{Z: 1.50, HalfBottom: 0.22, HalfTop: 0.18, Bottom: hullTop, Top: 3.90, XOff: 1.75},
	)

	// --- dorsal sensor pod ---
	b.part(
		section{Z: 4.20, HalfBottom: 0.55, HalfTop: 0.50, Bottom: hullTop, Top: 3.45},
		section{Z: 6.00, HalfBottom: 0.55, HalfTop: 0.35, Bottom: hullTop, Top: 3.35},
	)

	// --- the boarding tunnel around the vestibule -----------------------------
	//
	// Two walls and a roof, open at the aft end and with no floor of its own:
	// the way in is a hole, what closes it is the blast door two metres further
	// forward (sectors, not mesh), and what one walks on is the vestibule
	// sector's own deck. The inner faces sit clear of that sector's walls so
	// the two surfaces never fight over the same pixels.
	const tunnelIn, tunnelOut = vestHalf + 0.06, vestHalf + 0.42
	b.mirrored(
		section{Z: vestAft - 0.1, HalfBottom: 0.18, HalfTop: 0.18, Bottom: hullBottom, Top: vestCeil + 0.37, XOff: tunnelIn + 0.18},
		section{Z: hullAft, HalfBottom: 0.18, HalfTop: 0.18, Bottom: hullBottom, Top: vestCeil + 0.37, XOff: tunnelIn + 0.18},
	)
	b.part(
		section{Z: vestAft - 0.1, HalfBottom: tunnelOut, HalfTop: tunnelOut, Bottom: vestCeil + 0.01, Top: vestCeil + 0.37},
		section{Z: hullAft, HalfBottom: tunnelOut, HalfTop: tunnelOut, Bottom: vestCeil + 0.01, Top: vestCeil + 0.37},
	)

	// --- rubbing strakes down each flank, proud of the hull and outboard of
	// the hold: the one thing that reads as landing gear on a ship with none.
	b.mirrored(
		section{Z: 0.60, HalfBottom: 0.24, HalfTop: 0.20, Bottom: hullBottom, Top: 0.34, XOff: 2.46},
		section{Z: 5.20, HalfBottom: 0.24, HalfTop: 0.20, Bottom: hullBottom, Top: 0.34, XOff: 2.46},
	)

	return b.doc
}

// dropshipGlass is the canopy, a second attachment for one reason: a model
// wears one texture, and glass is not plating. The entity draws it at low alpha
// over the cockpit the hull leaves open in front.
func dropshipGlass() *model.ModelDoc {
	b := newBuilder("dropship_glass", "hull_glass")
	b.part(
		section{Z: 10.0, HalfBottom: 1.25, HalfTop: 1.15, Bottom: 1.62, Top: 2.55},
		section{Z: 11.15, HalfBottom: 0.80, HalfTop: 0.72, Bottom: 1.35, Top: 1.95},
	)
	return b.doc
}
