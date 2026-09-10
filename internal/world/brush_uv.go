package world

// brush_uv.go — a brush face's UVs stated per corner, and the atlas unwrap that
// states them.
//
// A brush face has always derived its texture coordinates: project the corners
// onto the face's own plane, then offset, scale and turn the result
// (BuildBrushFaceMesh, ApplyUVTransform). That is exactly right for a tiling
// material — a brick repeats at the same size whatever shape the wall is — and
// exactly wrong for PAINTING a surface, because a projection gives every face
// the whole image. Two walls projected onto one texture share every texel, so a
// stroke on one lands on the other.
//
// Painting needs what a model already has: each face owning its own region of
// one image, stated per corner. So a face may carry UVs, and when it does they
// replace the projection. Nothing but an unwrap writes them.
//
// Three rules keep them honest:
//
//   - They count only while there is one per corner (HasUVs). An edit that
//     rewires a face's loop without knowing about them leaves them the wrong
//     length, and the face falls back to its projection rather than drawing
//     its corners with each other's coordinates.
//   - They ride the corners. An authored brush has no transform — moving one
//     moves its verts (see Brush) — so per-corner UVs go wherever the brush
//     goes. A world-locked mapping would slide the painting off a brush the
//     moment the gizmo touched it.
//   - The offset, scale and rotation still apply on top, in the same order, so
//     the panel's fields mean the same thing on a painted face as on a tiled
//     one. An unwrap zeroes them, because a layout nudged off its island is a
//     layout sampling its neighbour's texels.

import (
	"math"

	"foundry/internal/geom"

	"github.com/go-gl/mathgl/mgl32"
)

// HasUVs reports whether the face's stated UVs are the ones it is drawn with:
// a real face, and exactly one coordinate per corner.
func (f *BrushFace) HasUVs() bool {
	return len(f.Indices) >= 3 && len(f.UVs) == len(f.Indices)
}

// applyCornerUVs writes a face's stated UVs over the projected ones
// BuildBrushFaceMesh put in verts. The corners repeat past the first loop for a
// double-sided face's reverse copy, which draws the same texels from behind.
func applyCornerUVs(verts [][]float32, uvs []mgl32.Vec2) {
	n := len(uvs)
	if n == 0 {
		return
	}
	for i := range verts {
		uv := uvs[i%n]
		verts[i][3], verts[i][4] = uv.X(), uv.Y()
	}
}

// FaceUVs returns the texture coordinate each corner of f is drawn with —
// stated or projected, with the offset, scale and rotation applied — built the
// way Load builds the face, so what it reports is what the sampler sees. The
// texture editor's layout overlay is drawn from it.
func (m *BrushMesh) FaceUVs(f *BrushFace) []mgl32.Vec2 {
	verts, _ := BuildBrushFaceMesh(m.FacePoints(f), false)
	if len(verts) == 0 {
		return nil
	}
	if f.HasUVs() {
		applyCornerUVs(verts, f.UVs)
	}
	ApplyUVTransform(verts, f.UVOffset, f.UVScale, f.UVRotation)
	out := make([]mgl32.Vec2, len(verts))
	for i, v := range verts {
		out[i] = mgl32.Vec2{v[3], v[4]}
	}
	return out
}

// ---------- as a function of position, for the compiler ----------

// UVMap is a face's texture coordinates as a function of where on the face a
// point is: u = U.xyz·p + U.w, and likewise v.
//
// The compiler needs this and the editor does not. A compiled level is made of
// the PIECES a face is cut into — by CSG, by the partition, by the light grid —
// and a piece has none of its source face's corners, so per-corner UVs have
// nothing to hang on. A projection has the same problem and solves it by
// carrying its origin into every piece (see BuildBrushFaceMeshFrom); stated UVs
// solve it by being turned into a map once, on the whole face, which every
// piece then evaluates at its own points.
type UVMap struct {
	U, V mgl32.Vec4
}

// At evaluates the map at p.
func (m UVMap) At(p mgl32.Vec3) mgl32.Vec2 {
	return mgl32.Vec2{m.U.Vec3().Dot(p) + m.U.W(), m.V.Vec3().Dot(p) + m.V.W()}
}

// FitUVMap finds the map that best reproduces uvs at pts — a planar face's
// corners and the coordinates stated for them. ok is false for a face with no
// plane, or with coordinates that are not numbers.
//
// For what an unwrap writes the fit is exact, not approximate: an island is a
// flat projection of a flat face, so its coordinates are already an affine
// function of position. It only approximates once someone has dragged a corner
// of an unwrapped quad out of that relationship — the texture stretches with
// the corner in the editor, piecewise across the face's two triangles, and the
// compiled face takes the nearest single map to that.
//
// Least squares, in float64, over the corners' coordinates in the face's own
// plane: two unknowns and a mean per axis, so a 2x2 solve.
func FitUVMap(pts []mgl32.Vec3, uvs []mgl32.Vec2) (UVMap, bool) {
	if len(pts) < 3 || len(uvs) != len(pts) {
		return UVMap{}, false
	}
	n := geom.PolygonNormal3D(pts)
	if n.Len() < 0.5 {
		return UVMap{}, false
	}
	e1, e2 := planarBasis(n)

	k := float64(len(pts))
	var cx, cy, cz, mu, mv float64
	for i, p := range pts {
		cx, cy, cz = cx+float64(p.X()), cy+float64(p.Y()), cz+float64(p.Z())
		mu, mv = mu+float64(uvs[i].X()), mv+float64(uvs[i].Y())
	}
	cx, cy, cz, mu, mv = cx/k, cy/k, cz/k, mu/k, mv/k
	if math.IsNaN(mu) || math.IsNaN(mv) || math.IsInf(mu, 0) || math.IsInf(mv, 0) {
		return UVMap{}, false
	}

	dot := func(e mgl32.Vec3, x, y, z float64) float64 {
		return float64(e.X())*x + float64(e.Y())*y + float64(e.Z())*z
	}
	var sss, sst, stt, ssu, stu, ssv, stv float64
	for i, p := range pts {
		dx, dy, dz := float64(p.X())-cx, float64(p.Y())-cy, float64(p.Z())-cz
		s, t := dot(e1, dx, dy, dz), dot(e2, dx, dy, dz)
		du, dv := float64(uvs[i].X())-mu, float64(uvs[i].Y())-mv
		sss, sst, stt = sss+s*s, sst+s*t, stt+t*t
		ssu, stu = ssu+s*du, stu+t*du
		ssv, stv = ssv+s*dv, stv+t*dv
	}
	det := sss*stt - sst*sst
	spread := sss + stt
	if spread <= 0 || det <= 1e-9*spread*spread {
		// The corners are (nearly) collinear in the plane: there is no second
		// direction to fit against.
		return UVMap{}, false
	}

	axis := func(su, tu, mean float64) mgl32.Vec4 {
		a := (stt*su - sst*tu) / det
		b := (sss*tu - sst*su) / det
		x := a*float64(e1.X()) + b*float64(e2.X())
		y := a*float64(e1.Y()) + b*float64(e2.Y())
		z := a*float64(e1.Z()) + b*float64(e2.Z())
		return mgl32.Vec4{float32(x), float32(y), float32(z), float32(mean - (x*cx + y*cy + z*cz))}
	}
	return UVMap{U: axis(ssu, stu, mu), V: axis(ssv, stv, mv)}, true
}

// ApplyUVMap sets each vertex's UV from its own position. Vertices are
// [X,Y,Z,U,V], as BuildBrushFaceMesh makes them.
func ApplyUVMap(verts [][]float32, m UVMap) {
	for i := range verts {
		uv := m.At(mgl32.Vec3{verts[i][0], verts[i][1], verts[i][2]})
		verts[i][3], verts[i][4] = uv.X(), uv.Y()
	}
}

// ---------- the atlas unwrap ----------

// AtlasFace names one face for an atlas unwrap: a face of a mesh, and the
// transform carrying that mesh's corners out to where they are measured.
//
// The transform is the chunk's ModelMatrix, and it is there for SIZE. Texel
// density has to come out uniform across everything one atlas covers, and a
// chunk can be scaled — a face in a chunk at twice the scale is twice the wall,
// and owes twice the texels. The zero value means identity.
type AtlasFace struct {
	Mesh  *BrushMesh
	Face  int
	Xform mgl32.Mat4
}

// LayoutBrushAtlas works out an atlas layout for the faces without writing
// anything; the layout's UVs are index-parallel to faces.
//
// Separate from ApplyBrushAtlas so a caller with an undo stack can learn whether
// the unwrap will succeed before it takes a snapshot, rather than taking one and
// then having to take it back.
//
// Every mesh's vertices go into one pool, each mesh at its own offset, so faces
// of one mesh that share corners share an island and faces of two different
// brushes never do (geom.AtlasUnwrap joins through shared indices only). The
// islands are laid out V-down, so a wall reads the right way up on the canvas.
func LayoutBrushAtlas(faces []AtlasFace, opts geom.AtlasOptions) (geom.AtlasLayout, error) {
	opts.VDown = true

	var pool []mgl32.Vec3
	base := map[*BrushMesh]int{}
	loops := make([][]int, len(faces))
	for i, af := range faces {
		m := af.Mesh
		if m == nil || af.Face < 0 || af.Face >= len(m.Faces) {
			continue
		}
		off, seen := base[m]
		if !seen {
			off = len(pool)
			base[m] = off
			xf := af.Xform
			if xf == (mgl32.Mat4{}) {
				xf = mgl32.Ident4()
			}
			for _, v := range m.Verts {
				pool = append(pool, mulPoint(xf, v))
			}
		}
		src := m.Faces[af.Face].Indices
		loop := make([]int, len(src))
		for c, vi := range src {
			if vi < 0 || vi >= len(m.Verts) {
				// Left nil, which the unwrap skips. Offsetting a bad index would
				// quietly point it into the NEXT mesh's corners instead.
				loop = nil
				break
			}
			loop[c] = off + vi
		}
		loops[i] = loop
	}
	return geom.AtlasUnwrap(pool, loops, opts)
}

// ApplyBrushAtlas writes a layout onto the faces it was made for and reports how
// many it wrote. Each written face's offset, scale and rotation go back to
// identity — see the file comment. Nothing is reloaded; that is the caller's.
func ApplyBrushAtlas(faces []AtlasFace, layout geom.AtlasLayout) int {
	n := 0
	for i, af := range faces {
		if i >= len(layout.UVs) || layout.UVs[i] == nil ||
			af.Mesh == nil || af.Face < 0 || af.Face >= len(af.Mesh.Faces) {
			continue
		}
		f := &af.Mesh.Faces[af.Face]
		if len(layout.UVs[i]) != len(f.Indices) {
			continue
		}
		f.UVs = append([]mgl32.Vec2(nil), layout.UVs[i]...)
		f.UVOffset = mgl32.Vec2{}
		f.UVScale = mgl32.Vec2{1, 1}
		f.UVRotation = 0
		n++
	}
	return n
}

// ---------- serialization ----------

// uvsFromRaw unflattens a face's saved UVs, two floats per corner. A trailing
// odd float is a malformed file and is dropped, as a partial vertex is.
func uvsFromRaw(raw []float32) []mgl32.Vec2 {
	if len(raw) < 2 {
		return nil
	}
	out := make([]mgl32.Vec2, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		out = append(out, mgl32.Vec2{raw[i], raw[i+1]})
	}
	return out
}

// uvsToRaw flattens the UVs a face is drawn with. A face whose stated UVs no
// longer match its corners saves none: they were already being ignored, and
// writing them would carry the mismatch into every later load.
func uvsToRaw(f *BrushFace) []float32 {
	if !f.HasUVs() {
		return nil
	}
	out := make([]float32, 0, len(f.UVs)*2)
	for _, uv := range f.UVs {
		out = append(out, uv.X(), uv.Y())
	}
	return out
}
