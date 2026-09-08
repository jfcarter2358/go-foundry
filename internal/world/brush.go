package world

// brush.go — Brush, the level's fundamental solid.
//
// Where the level used to be built from Sectors — 2D floorplan polygons with
// a floor/ceiling height field — it is now built from Brushes: convex
// polyhedra, authored as an explicit vertex + face list (the same shape as a
// model's mesh) rather than as planes to be intersected. That is what makes
// "edit it like the model editor" literal: a brush's faces are dragged,
// extruded and inset the same way a mesh's are, not derived from clipping
// half-spaces the way a classic Quake .map brush is.
//
// Brushes are required to be CONVEX. That is what keeps point/shape
// collision cheap (GJK against the vertex list directly — every vertex of a
// convex solid sits on its own hull, so no separate hull computation is
// needed) and keeps compile-time CSG face-clipping a half-space problem
// instead of a general polygon-boolean one. The editor is responsible for
// keeping an authored brush convex; IsConvex below is the check it uses.
//
// A Brush's Verts are chunk-local, the same convention every other chunk
// coordinate follows (see chunk.go) — Chunk.ModelMatrix is the one place
// that carries them into world space (or a frame's space, for a frame-owned
// chunk).

import (
	"math"

	"foundry/internal/geom"
	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// Brush is a solid, convex polyhedron: a level's fundamental piece of
// geometry, replacing the Sector.
type Brush struct {
	ID         string
	ScriptData // tags, attributes and the attached script
	// Verts are the brush's corners, chunk-local. Every vertex is expected to
	// sit on the convex hull — see the file comment — so nothing here holds a
	// separate hull; ConvexHull collision (internal/collision) reads these
	// directly.
	Verts []mgl32.Vec3
	Faces []BrushFace

	// Solid is whether the player and entities collide with this brush. On by
	// default; a false brush is decorative geometry a body passes through —
	// the brush counterpart of a wall with Collision off.
	Solid bool
}

// BrushFace is one planar face of a brush: a convex n-gon (in practice
// usually a triangle or a quad) wound CCW when viewed from outside the
// solid, with its own texture and shading.
type BrushFace struct {
	// Indices lists this face's corners into the owning Brush's Verts, in
	// winding order. A planar loop of at least 3.
	Indices []int

	Texture      string
	BlendTexture string // optional second texture, mixed in by BlendMask's luminance
	BlendMask    string // grayscale mask texture; 0 = Texture, 1 = BlendTexture
	UVOffset     mgl32.Vec2
	UVScale      mgl32.Vec2 // zero = {1,1}
	UVRotation   float32    // radians
	Smooth       bool       // shade from interpolated vertex normals across the brush
	SmoothAngle  float32    // crease threshold in degrees; 0 = DefaultSmoothAngle
	Detail       DetailSpec // per-face procedural rock displacement; disabled by default
	Alpha        float32    // 0 = fully transparent, 1 = fully opaque
	DoubleSided  bool       // render from both sides

	// NoDraw makes the face invisible — no geometry is built or uploaded —
	// while it still collides if the brush is Solid. For a brush face buried
	// against a neighbour's, or a clip-only volume.
	NoDraw bool
	// NoCollide makes the face visible but non-solid, independent of the
	// brush's own Solid flag. (Solid=false already makes every face
	// non-solid; this is for the one-face exception — a window opening in an
	// otherwise solid wall brush.)
	NoCollide bool

	// ---------- runtime, never authored and never saved ----------
	Geometry          gfx.Geometry
	SubmeshGeometries []gfx.Geometry // Detail's texture-override regions, same as Wall/Floor/Ceiling
}

// NewBrushFace returns a face with the defaults an authored one should have.
func NewBrushFace(indices []int, texture string) BrushFace {
	return BrushFace{
		Indices:     indices,
		Texture:     texture,
		UVScale:     mgl32.Vec2{1, 1},
		Alpha:       1,
		DoubleSided: false,
	}
}

// LocalAABB returns the brush's chunk-local bounds.
func (b *Brush) LocalAABB() (min, max mgl32.Vec3) {
	if len(b.Verts) == 0 {
		return mgl32.Vec3{}, mgl32.Vec3{}
	}
	min, max = b.Verts[0], b.Verts[0]
	for _, p := range b.Verts[1:] {
		min, max = geom.VecMin(min, p), geom.VecMax(max, p)
	}
	return min, max
}

// WorldAABB is LocalAABB carried through ch's ModelMatrix.
func (b *Brush) WorldAABB(ch *Chunk) (min, max mgl32.Vec3) {
	lo, hi := b.LocalAABB()
	return transformAABB(ch.ModelMatrix(), lo, hi)
}

// FacePoints resolves a face's corners to their brush-local positions.
func (b *Brush) FacePoints(f *BrushFace) []mgl32.Vec3 {
	pts := make([]mgl32.Vec3, len(f.Indices))
	for i, vi := range f.Indices {
		if vi >= 0 && vi < len(b.Verts) {
			pts[i] = b.Verts[vi]
		}
	}
	return pts
}

// FaceNormal is a face's outward normal, from its first three corners.
func (b *Brush) FaceNormal(f *BrushFace) mgl32.Vec3 {
	pts := b.FacePoints(f)
	if len(pts) < 3 {
		return mgl32.Vec3{0, 1, 0}
	}
	return geom.TriangleNormal(pts[0], pts[1], pts[2])
}

// IsConvex reports whether every vertex of the brush lies on or behind every
// face's plane (within a small tolerance) — the property collision and
// compile-time CSG both depend on. The editor calls this after any edit that
// could break it (a free vertex drag; extrude/inset on a box start already
// preserve it).
func (b *Brush) IsConvex() bool {
	const eps = 1e-4
	for fi := range b.Faces {
		f := &b.Faces[fi]
		pts := b.FacePoints(f)
		if len(pts) < 3 {
			continue
		}
		n := geom.TriangleNormal(pts[0], pts[1], pts[2])
		d := n.Dot(pts[0])
		for _, v := range b.Verts {
			if n.Dot(v)-d > eps {
				return false
			}
		}
	}
	return true
}

// ApplyUVTransform transforms the UV coordinates of a vertex slice in-place.
// Vertices are [X,Y,Z,U,V]. Applies scale around (0,0), then rotation, then
// offset.
func ApplyUVTransform(vertices [][]float32, offset mgl32.Vec2, scale mgl32.Vec2, rotation float32) {
	if scale == (mgl32.Vec2{}) {
		scale = mgl32.Vec2{1, 1}
	}
	if offset == (mgl32.Vec2{}) && scale == (mgl32.Vec2{1, 1}) && rotation == 0 {
		return
	}
	cos := float32(math.Cos(float64(rotation)))
	sin := float32(math.Sin(float64(rotation)))
	for i := range vertices {
		u := vertices[i][3] * scale.X()
		v := vertices[i][4] * scale.Y()
		ru := u*cos - v*sin
		rv := u*sin + v*cos
		vertices[i][3] = ru + offset.X()
		vertices[i][4] = rv + offset.Y()
	}
}

// planarBasis returns two orthonormal vectors spanning the plane
// perpendicular to n, used to flatten a face's 3D corners into UV space.
func planarBasis(n mgl32.Vec3) (u, v mgl32.Vec3) {
	n = n.Normalize()
	ref := mgl32.Vec3{0, 1, 0}
	if math.Abs(float64(n.Dot(ref))) > 0.99 {
		ref = mgl32.Vec3{1, 0, 0}
	}
	u = n.Cross(ref).Normalize()
	v = n.Cross(u).Normalize()
	return u, v
}

// BuildBrushFaceMesh triangulates a planar convex face — a fan from corner 0,
// valid because a brush face is required convex — and derives UVs by
// projecting each corner onto the face's own plane. doubleSided appends a
// second, reverse-wound copy so the face draws from both sides.
//
// This is the brush counterpart of BuildSurfaceMesh: much simpler, because a
// face has no openings and no per-vertex displacement mesh of its own —
// organic shape comes from subdividing the brush's own vertex/face graph
// (adding real geometry), not from a parametric layer riding on top of a
// flat base the way a sector surface's SurfaceMesh did.
func BuildBrushFaceMesh(pts []mgl32.Vec3, doubleSided bool) (verts [][]float32, indices []int) {
	n := len(pts)
	if n < 3 {
		return nil, nil
	}
	normal := geom.TriangleNormal(pts[0], pts[1], pts[2])
	u, v := planarBasis(normal)
	origin := pts[0]

	verts = make([][]float32, n)
	for i, p := range pts {
		rel := p.Sub(origin)
		verts[i] = []float32{p.X(), p.Y(), p.Z(), rel.Dot(u), rel.Dot(v)}
	}
	for i := 1; i < n-1; i++ {
		indices = append(indices, 0, i, i+1)
	}

	if doubleSided {
		base := len(verts)
		verts = append(verts, append([][]float32(nil), verts[:n]...)...)
		for i := 1; i < n-1; i++ {
			indices = append(indices, base, base+i+1, base+i)
		}
	}
	return verts, indices
}

// Load builds and uploads every visible face's render geometry. Call after
// any edit to the brush's verts or faces.
func (b *Brush) Load() error {
	for fi := range b.Faces {
		f := &b.Faces[fi]
		f.Geometry.Delete()
		for i := range f.SubmeshGeometries {
			f.SubmeshGeometries[i].Delete()
		}
		f.SubmeshGeometries = nil

		if f.NoDraw {
			continue
		}
		pts := b.FacePoints(f)
		verts, indices := BuildBrushFaceMesh(pts, f.DoubleSided)
		if len(verts) == 0 {
			continue
		}
		ApplyUVTransform(verts, f.UVOffset, f.UVScale, f.UVRotation)
		f.Geometry.Smooth, f.Geometry.SmoothAngle = f.Smooth, f.SmoothAngle
		if err := f.Geometry.Load(verts, indices, f.Texture, mgl32.Vec3{}); err != nil {
			return err
		}
		f.Geometry.Alpha = f.Alpha
		f.Geometry.BlendTexture = f.BlendTexture
		f.Geometry.BlendMask = f.BlendMask
	}
	return nil
}

// NewHollowRoom returns the six brushes bounding an axis-aligned walkable
// room: a floor, a ceiling, and four walls, each wallThickness deep,
// enclosing the space between min and max. This is the editor's "hollow"
// primitive — the fast way to block out a room, rather than hand-placing six
// brushes — and is also what a test fixture wanting somewhere to walk around
// reaches for, since a single NewBoxBrush is a solid block with no interior.
func NewHollowRoom(idPrefix string, min, max mgl32.Vec3, wallThickness float32, texture string) []Brush {
	if wallThickness <= 0 {
		wallThickness = 0.5
	}
	cx, cz := (min.X()+max.X())/2, (min.Z()+max.Z())/2
	hx, hz := (max.X()-min.X())/2, (max.Z()-min.Z())/2
	floorY, ceilY := min.Y(), max.Y()
	midY := (floorY + ceilY) / 2
	halfWallY := (ceilY - floorY) / 2
	ht := wallThickness / 2

	return []Brush{
		NewBoxBrush(idPrefix+"_floor",
			mgl32.Vec3{cx, floorY - ht, cz}, mgl32.Vec3{hx + wallThickness, ht, hz + wallThickness}, texture),
		NewBoxBrush(idPrefix+"_ceiling",
			mgl32.Vec3{cx, ceilY + ht, cz}, mgl32.Vec3{hx + wallThickness, ht, hz + wallThickness}, texture),
		NewBoxBrush(idPrefix+"_wall_negx",
			mgl32.Vec3{min.X() - ht, midY, cz}, mgl32.Vec3{ht, halfWallY, hz}, texture),
		NewBoxBrush(idPrefix+"_wall_posx",
			mgl32.Vec3{max.X() + ht, midY, cz}, mgl32.Vec3{ht, halfWallY, hz}, texture),
		NewBoxBrush(idPrefix+"_wall_negz",
			mgl32.Vec3{cx, midY, min.Z() - ht}, mgl32.Vec3{hx, halfWallY, ht}, texture),
		NewBoxBrush(idPrefix+"_wall_posz",
			mgl32.Vec3{cx, midY, max.Z() + ht}, mgl32.Vec3{hx, halfWallY, ht}, texture),
	}
}

// CloneBrush returns a deep, independent copy. GPU geometry handles are
// zeroed — the caller must Load the clone before it can be rendered, exactly
// as Chunk.Clone's sector counterpart used to require.
func CloneBrush(b *Brush, idSuffix string) Brush {
	out := Brush{
		ID:         b.ID + idSuffix,
		ScriptData: CloneScriptData(b.ScriptData),
		Solid:      b.Solid,
		Verts:      append([]mgl32.Vec3(nil), b.Verts...),
	}
	out.Faces = make([]BrushFace, len(b.Faces))
	for i := range b.Faces {
		out.Faces[i] = b.Faces[i]
		out.Faces[i].Indices = append([]int(nil), b.Faces[i].Indices...)
		out.Faces[i].Geometry = gfx.Geometry{}
		out.Faces[i].SubmeshGeometries = nil
	}
	return out
}

// NewBoxBrush returns an axis-aligned box brush centred at center with the
// given half-extents, one face per side, each defaulted with texture and
// facing outward (CCW viewed from outside). The stamp the editor's brush tool
// starts from, and a convenient way to hand-author a test level.
func NewBoxBrush(id string, center mgl32.Vec3, half mgl32.Vec3, texture string) Brush {
	hx, hy, hz := half.X(), half.Y(), half.Z()
	cx, cy, cz := center.X(), center.Y(), center.Z()
	// Corner i's bits are (x, y, z) signs, matching CornerSign's convention.
	verts := make([]mgl32.Vec3, 8)
	for i := 0; i < 8; i++ {
		verts[i] = mgl32.Vec3{
			cx + CornerSign(i, 0)*hx,
			cy + CornerSign(i, 1)*hy,
			cz + CornerSign(i, 2)*hz,
		}
	}
	// Corner indices: 0=(---) 1=(+--) 2=(-+-) 3=(++-) 4=(--+) 5=(+-+) 6=(-++) 7=(+++)
	faces := []BrushFace{
		NewBrushFace([]int{0, 4, 6, 2}, texture), // -X
		NewBrushFace([]int{5, 1, 3, 7}, texture), // +X
		NewBrushFace([]int{0, 1, 5, 4}, texture), // -Y (floor)
		NewBrushFace([]int{2, 6, 7, 3}, texture), // +Y (ceiling)
		NewBrushFace([]int{1, 0, 2, 3}, texture), // -Z
		NewBrushFace([]int{4, 5, 7, 6}, texture), // +Z
	}
	return Brush{ID: id, Verts: verts, Faces: faces, Solid: true}
}

// ---------- serialization ----------

// BrushFromRaw converts the YAML form. Follows the same conventions every
// other FromRaw here does: an absent `solid:` is solid, an absent alpha is
// opaque, and a zero UV scale reads as {1,1}.
func BrushFromRaw(r RawBrush) (Brush, error) {
	data, err := ScriptDataFromRaw(r.RawScriptData, "brush "+r.ID)
	if err != nil {
		return Brush{}, err
	}
	b := Brush{
		ID:         r.ID,
		ScriptData: data,
		Solid:      BoolDefault(r.Solid, true),
	}
	// Verts arrive flattened, three floats per vertex; a trailing partial
	// triple is a malformed file rather than a vertex, and is dropped.
	b.Verts = make([]mgl32.Vec3, 0, len(r.Verts)/3)
	for i := 0; i+2 < len(r.Verts); i += 3 {
		b.Verts = append(b.Verts, mgl32.Vec3{r.Verts[i], r.Verts[i+1], r.Verts[i+2]})
	}
	b.Faces = make([]BrushFace, 0, len(r.Faces))
	for _, rf := range r.Faces {
		b.Faces = append(b.Faces, BrushFace{
			Indices:      append([]int(nil), rf.Indices...),
			Texture:      rf.Texture,
			BlendTexture: rf.BlendTexture,
			BlendMask:    rf.BlendMask,
			UVOffset:     mgl32.Vec2{rf.UVOffset.X, rf.UVOffset.Y},
			UVScale:      mgl32.Vec2{rf.UVScale.X, rf.UVScale.Y},
			UVRotation:   rf.UVRotation,
			Smooth:       rf.Smooth,
			SmoothAngle:  rf.SmoothAngle,
			Detail:       DetailFromRaw(rf.Detail),
			Alpha:        float32Default(rf.Alpha, 1.0),
			DoubleSided:  rf.DoubleSided,
			NoDraw:       rf.NoDraw,
			NoCollide:    rf.NoCollide,
		})
	}
	return b, nil
}

// BrushToRaw is the inverse.
func BrushToRaw(b *Brush) RawBrush {
	out := RawBrush{
		ID:            b.ID,
		RawScriptData: ScriptDataToRaw(b.ScriptData),
		Solid:         BoolPtrIfFalse(b.Solid),
	}
	out.Verts = make([]float32, 0, len(b.Verts)*3)
	for _, v := range b.Verts {
		out.Verts = append(out.Verts, v.X(), v.Y(), v.Z())
	}
	out.Faces = make([]RawBrushFace, 0, len(b.Faces))
	for i := range b.Faces {
		f := &b.Faces[i]
		alpha := f.Alpha
		var alphaPtr *float32
		// Opaque is the default, so an opaque face writes no key at all and a
		// level that never touched alpha round-trips unchanged.
		if alpha != 1 {
			alphaPtr = &alpha
		}
		out.Faces = append(out.Faces, RawBrushFace{
			Indices:      append([]int(nil), f.Indices...),
			Texture:      f.Texture,
			BlendTexture: f.BlendTexture,
			BlendMask:    f.BlendMask,
			UVOffset:     geom.Point{X: f.UVOffset.X(), Y: f.UVOffset.Y()},
			UVScale:      geom.Point{X: f.UVScale.X(), Y: f.UVScale.Y()},
			UVRotation:   f.UVRotation,
			Smooth:       f.Smooth,
			SmoothAngle:  f.SmoothAngle,
			Detail:       DetailToRaw(f.Detail),
			Alpha:        alphaPtr,
			DoubleSided:  f.DoubleSided,
			NoDraw:       f.NoDraw,
			NoCollide:    f.NoCollide,
		})
	}
	return out
}
