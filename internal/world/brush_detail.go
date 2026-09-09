package world

// brush_detail.go — a brush's detail mesh: what it LOOKS like, as opposed to
// what it IS.
//
// A brush is a convex polyhedron, and convexity is not a stylistic preference:
// collision reads its verts as a hull directly, the compiler clips its faces as
// half-spaces, and both are wrong rather than merely slow on a concave one
// (brush.go). That rules out most of what makes a surface interesting. A
// window reveal, a ledge running along a wall, a lip around a doorway, a panel
// pressed into a bulkhead — every one of them is concave, and every one of them
// is a detail nobody should ever collide with differently from the flat wall it
// is cut into.
//
// So a brush gets a second mesh. Detail is the same BrushMesh the hull is, with
// none of the hull's rules:
//
//   - It is DRAWN INSTEAD of the hull. Both are never on screen at once, which
//     is what keeps a detailed brush from z-fighting with itself.
//   - It is not consulted by collision, ever. A body meets the hull, and the
//     hull is whatever the author shaped in edit mode — moving a detail vertex
//     cannot change where the player can walk, which is the entire point.
//   - It need not be convex, closed, or even connected.
//   - It seals nothing. The compiler builds its partition and its visibility
//     from the hull faces, and inserts the detail into the cells they made.
//     This is Quake's detail brush, one level down: the same trade, applied to
//     the faces of a solid rather than to a solid in a level.
//
// A detail mesh starts as a copy of the hull (SeedDetail) so entering detail
// mode changes nothing visible, and diverges from there. It is NOT kept in step
// with later hull edits — reshaping the hull under a detail mesh leaves the two
// disagreeing, which the editor warns about and offers to re-seed. Anything
// automatic here would either silently discard detail work or silently move it,
// and both are worse than saying so.

import (
	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// Clone returns a deep, independent copy of the mesh. GPU handles are zeroed —
// the copy owns no buffers until it is Loaded — exactly as CloneBrush requires.
func (m *BrushMesh) Clone() BrushMesh {
	out := BrushMesh{Verts: append([]mgl32.Vec3(nil), m.Verts...)}
	out.Faces = make([]BrushFace, len(m.Faces))
	for i := range m.Faces {
		out.Faces[i] = m.Faces[i]
		out.Faces[i].Indices = append([]int(nil), m.Faces[i].Indices...)
		out.Faces[i].Geometry = gfx.Geometry{}
	}
	return out
}

// HasDetail reports whether this brush is drawn from a detail mesh rather than
// from its own faces. An empty detail mesh does not count: it would draw
// nothing at all, and a brush that has been detailed down to no faces is
// indistinguishable from a bug.
func (b *Brush) HasDetail() bool {
	return b.Detail != nil && len(b.Detail.Faces) > 0
}

// DrawMesh is the mesh a brush is DRAWN from — the detail mesh when it has one,
// its own hull otherwise.
//
// The pointer is into the brush, not a copy, so a caller may take the address
// of a face's Geometry through it. Every draw, shadow, pick and upload path
// goes through here; every collision path reads b.Verts and b.Faces directly.
// That is the whole of the distinction, and it is meant to be readable at the
// call site: DrawFaces is what you see, Faces is what you hit.
func (b *Brush) DrawMesh() *BrushMesh {
	if b.HasDetail() {
		return b.Detail
	}
	return &b.BrushMesh
}

// DrawFaces is DrawMesh's face list, which is what most callers actually want.
func (b *Brush) DrawFaces() []BrushFace { return b.DrawMesh().Faces }

// Load uploads the geometry the brush is drawn from, and frees whatever the
// other mesh was holding.
//
// Releasing the other one is not tidiness. A brush that has just been given a
// detail mesh was drawing its hull a moment ago, and leaving those buffers
// uploaded would draw the solid straight through the detail carved into it.
func (b *Brush) Load() error {
	if b.HasDetail() {
		b.BrushMesh.Release()
		return b.Detail.Load()
	}
	if b.Detail != nil {
		b.Detail.Release()
	}
	return b.BrushMesh.Load()
}

// Release frees every GPU buffer the brush owns, on both meshes. For a brush
// being removed from the level.
func (b *Brush) Release() {
	b.BrushMesh.Release()
	if b.Detail != nil {
		b.Detail.Release()
	}
}

// Unload is Release for a brush that is coming back — a streamed-out chunk.
func (b *Brush) Unload() {
	b.BrushMesh.Unload()
	if b.Detail != nil {
		b.Detail.Unload()
	}
}

// SeedDetail gives the brush a detail mesh copied from its hull, if it has not
// got one already, and reports whether it made one.
//
// A copy rather than an empty mesh: detail is something you carve INTO a
// surface, so the surface has to be there to carve. Entering detail mode on a
// plain brush therefore changes nothing on screen until the first edit, which
// is what makes the mode safe to look around in.
func (b *Brush) SeedDetail() bool {
	if b.Detail != nil {
		return false
	}
	d := b.BrushMesh.Clone()
	b.Detail = &d
	return true
}

// ClearDetail throws the detail mesh away and puts the brush back to being
// drawn as the solid it is.
func (b *Brush) ClearDetail() bool {
	if b.Detail == nil {
		return false
	}
	b.Detail.Release()
	b.Detail = nil
	return true
}

// ResetDetail re-seeds the detail mesh from the hull as it is now, discarding
// whatever was carved into it. What the editor offers when a hull edit has left
// the two disagreeing.
func (b *Brush) ResetDetail() {
	b.ClearDetail()
	b.SeedDetail()
}

// EachFace calls fn for every face of BOTH meshes — the hull's and the detail
// one's.
//
// For the passes that are about what a brush REFERENCES rather than about what
// it draws: which textures have to be loaded before it can be shown. The hull's
// faces keep their textures while a detail mesh is drawn over them, because
// clearing the detail puts them straight back on screen, and a texture the
// loader skipped would come back as white.
func (b *Brush) EachFace(fn func(*BrushFace)) {
	for fi := range b.Faces {
		fn(&b.Faces[fi])
	}
	if b.Detail == nil {
		return
	}
	for fi := range b.Detail.Faces {
		fn(&b.Detail.Faces[fi])
	}
}
