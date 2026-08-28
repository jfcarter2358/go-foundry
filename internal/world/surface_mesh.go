package world

// surface_mesh.go — the editable mesh a wall, floor or ceiling wears.
//
// A face of the level used to carry four separate ways of not being flat, and
// Sector.Load picked between them with an if/else chain: a wall was *either*
// terrain (Delaunay over TerrainPoints) *or* extruded (one Outer→Inner prism
// per Extrusion) *or* a plain quad, never two of those at once. Neither of the
// two real mechanisms could express what the other did, and neither could be
// applied twice — an Extrusion is a single prism, so there was no way to put a
// bump on the floor of a recess, or to bevel a hole's rim.
//
// SurfaceMesh replaces both with one representation, the same one the model
// editor already runs on: a shared point pool plus n-gon faces indexing into
// it. Extruding is then a topological operation (SurfaceExtrudeFaces) that can
// be applied to its own output, which is what makes stacked and nested shapes
// fall out for free rather than needing a field each.
//
// # Why the points are parametric
//
// ModelDoc stores free mgl32.Vec3 positions. A level surface cannot: it is not
// an independent object but a *face of a sector*, pinned to the sector's
// outline and to its floor and ceiling heights. Store world positions and the
// mesh is orphaned the moment anybody drags a corner on the 2D map, retypes a
// floor height, or splits a wall in two — and those are ordinary edits, not
// corner cases.
//
// So a SurfacePoint is (U, V) in the face's own parameter space plus a Depth
// along the face's depth axis, and a SurfaceFrame converts that to world space
// using the sector's *current* geometry. The mesh rides the surface: change the
// ceiling height and the wall's mesh stretches with it, because nothing about
// where it is was ever written down.
//
// # Why the outline is not the mesh's to own
//
// Points whose Boundary is >= 0 mirror an outline corner — the sector polygon's
// corners for a floor or ceiling, the four quad corners for a wall — and are
// not independently editable. Sectors share corners with their neighbours and
// have to stay convex for portal culling to work, so if each face's mesh owned
// its own outline the three faces meeting at a corner would drift apart and the
// 2D map would stop being the authority on the level's shape. Interior points
// are free; the boundary is derived, and SyncSurfaceBoundary re-derives it.
// RemapSurfaceMesh (surface_mesh_remap.go) is what carries the interior across
// when the outline itself changes.

import (
	"foundry/internal/geom"

	"github.com/go-gl/mathgl/mgl32"
)

// SurfaceFree is the Boundary value of a point that is nobody's outline corner
// — an interior point, free to be dragged anywhere.
const SurfaceFree = -1

// SurfacePoint is one point of a surface mesh, in the face's own parameter
// space.
//
// U and V follow the convention ExtrusionPoint used before it, so the two are
// directly comparable and the migration needs no coordinate change:
//
//	walls:           U = distance along the wall from its start point,
//	                 V = distance downward from the ceiling.
//	floors/ceilings: U, V = chunk-local X, Z.
//
// Depth is displacement along the frame's depth axis: world +Y for a floor or a
// ceiling, the wall's outward normal for a wall. The floor/ceiling case is a Y
// offset rather than "along the surface normal" on purpose — VertexOffsets and
// TerrainPoint.Depth are both already Y offsets, and those are nearly all the
// authored data there is, so matching them means the migration changes no signs
// and a ceiling's boundary can mirror its VertexOffsets directly.
type SurfacePoint struct {
	U, V, Depth float32

	// Boundary is which outline corner this point mirrors, or SurfaceFree for
	// an interior point. A boundary point's U, V and Depth are overwritten from
	// the sector every time the surface is rebuilt — see SyncSurfaceBoundary.
	Boundary int
}

// SurfaceFace is one CCW polygon of the mesh, wound as seen from its front.
// Verts indexes SurfaceMesh.Points.
type SurfaceFace struct {
	Verts []int

	// UVs, when non-nil, is index-parallel to Verts and overrides the texture
	// coordinates the frame would otherwise derive from (U, V). Extrusion sides
	// need it: a side wall is perpendicular to the surface, so it occupies no
	// area in (U, V) and a derived UV would collapse it to a line.
	UVs []mgl32.Vec2

	// Texture overrides the surface's own; empty means inherit. A face with one
	// compiles into a separate Geometry, which is what an Extrusion's custom
	// back-face texture used to get through its own separate geometry.
	Texture string

	// Alpha applies only to a face carrying its own Texture, for the same
	// reason Extrusion.Alpha did: an inherited face is part of the surface's
	// single draw and shares the surface's alpha.
	Alpha float32

	// Holes are rings cut out of this face, each a list of point indices wound
	// against Verts. They are what lets a floor with a doorway stay *one* face
	// rather than becoming the eight triangles a clipper would leave behind —
	// which matters because a face is the unit the editor selects, textures and
	// extrudes, and "the floor" should not stop being one thing the moment
	// somebody cuts a hole in it.
	//
	// A ring here is the rim of something else: extruding a face inwards leaves
	// a hole in it and grows sides down to a cap, and the hole ring and the cap
	// face share their points, so dragging the cap drags the rim with it.
	Holes [][]int

	// Hole means the face is not drawn. It is how the mesh says "this region is
	// open" — extrude a face and delete its cap and you have a tunnel, which is
	// what Extrusion.Visible=false used to express.
	Hole bool

	// Smooth shades this face from interpolated vertex normals. Only meaningful
	// where the surface as a whole is not already smoothed.
	Smooth bool
}

// Clone deep-copies a face; the slices must not be shared or an edit to one
// would silently rewrite another.
func (f SurfaceFace) Clone() SurfaceFace {
	c := f
	c.Verts = append([]int(nil), f.Verts...)
	c.UVs = append([]mgl32.Vec2(nil), f.UVs...)
	if len(f.Holes) > 0 {
		c.Holes = make([][]int, len(f.Holes))
		for i, h := range f.Holes {
			c.Holes[i] = append([]int(nil), h...)
		}
	}
	return c
}

// SurfaceMesh is the editable mesh of one wall, floor or ceiling.
//
// The zero value is the flat face: no points, no faces, and BuildSurfaceMesh
// falls back to triangulating the frame's outline directly. That matters for
// more than tidiness — nearly every surface in a level is flat, and a mesh that
// costs nothing to *not* have is what lets SurfaceMesh replace the old flat
// path outright rather than sitting beside it.
type SurfaceMesh struct {
	Points []SurfacePoint
	Faces  []SurfaceFace
}

// IsEmpty reports whether the mesh has nothing to draw, in which case the
// surface is its plain outline.
func (m *SurfaceMesh) IsEmpty() bool { return m == nil || len(m.Faces) == 0 }

// Clone deep-copies the mesh.
func (m *SurfaceMesh) Clone() SurfaceMesh {
	if m == nil {
		return SurfaceMesh{}
	}
	out := SurfaceMesh{Points: append([]SurfacePoint(nil), m.Points...)}
	if len(m.Faces) > 0 {
		out.Faces = make([]SurfaceFace, len(m.Faces))
		for i := range m.Faces {
			out.Faces[i] = m.Faces[i].Clone()
		}
	}
	return out
}

// AddPoint appends an interior point and returns its index.
func (m *SurfaceMesh) AddPoint(u, v, depth float32) int {
	m.Points = append(m.Points, SurfacePoint{U: u, V: v, Depth: depth, Boundary: SurfaceFree})
	return len(m.Points) - 1
}

// Vec2 is the point's position in parameter space, which is what every 2D
// routine here — triangulating, point-in-polygon, the editor's picking — wants.
func (p SurfacePoint) Vec2() mgl32.Vec2 { return mgl32.Vec2{p.U, p.V} }

// ---------- frames ----------

// SurfaceFrame maps a surface's parameter space to the world, using the
// sector's current geometry. It is the whole reason one mesh editor can serve
// walls, floors and ceilings: everything that differs between them is behind
// these four methods.
type SurfaceFrame interface {
	// ToWorld places a parametric point in chunk-local space.
	ToWorld(u, v, depth float32) mgl32.Vec3

	// Outline is the surface's boundary ring in (U, V), in the order boundary
	// points index it. For a floor or ceiling that is the sector polygon; for a
	// wall, the four corners of its quad.
	Outline() []mgl32.Vec2

	// OutlineDepth is the depth the outline's corner i sits at — a floor or
	// ceiling's per-vertex height offset, and always zero for a wall, whose
	// corners are by definition on the wall plane.
	OutlineDepth(i int) float32

	// TexCoord is the texture coordinate a point gets when its face does not
	// carry explicit UVs.
	TexCoord(u, v float32) mgl32.Vec2

	// Planar reports whether (U, V) is chunk-local XZ. Only a planar surface
	// can have Openings cut in it, and only there does an opening's polygon
	// share a coordinate system with the mesh.
	Planar() bool
}

// WallSurfaceFrame is a wall's parameter space: U along the wall, V down from
// the ceiling, Depth along the outward normal. It is WallLocalToWorld's
// argument list, gathered up so it can be passed as one thing.
type WallSurfaceFrame struct {
	P1, P2                           mgl32.Vec2
	Dir, Norm                        mgl32.Vec2
	WallLen                          float32
	FloorH1, FloorH2, CeilH1, CeilH2 float32
}

// NewWallSurfaceFrame builds the frame for one wall of a sector.
func NewWallSurfaceFrame(p1, p2 mgl32.Vec2, floorH1, floorH2, ceilH1, ceilH2 float32) WallSurfaceFrame {
	dir, norm := WallFrame(p1, p2)
	return WallSurfaceFrame{
		P1: p1, P2: p2, Dir: dir, Norm: norm,
		WallLen: mgl32.Vec2{p2.X() - p1.X(), p2.Y() - p1.Y()}.Len(),
		FloorH1: floorH1, FloorH2: floorH2, CeilH1: ceilH1, CeilH2: ceilH2,
	}
}

func (f WallSurfaceFrame) ToWorld(u, v, depth float32) mgl32.Vec3 {
	x, y, z := WallLocalToWorld(u, v, depth, f.P1, f.P2, f.Dir, f.Norm, f.WallLen,
		f.FloorH1, f.FloorH2, f.CeilH1, f.CeilH2)
	return mgl32.Vec3{x, y, z}
}

// Outline is the wall quad, wound CCW in (U, V) — the same ring the flat path
// emits, and the same one the old extrusion builder fed to its ear clipper, so
// a wall that grows a mesh keeps the facing it had.
//
// The two far corners take their own end's height rather than the average of
// the pair. The old extrusion builder used the average of the two, which
// quietly turned a wall between differing floor heights into a rectangle the
// moment it gained an extrusion — a visible step against the flat wall beside
// it.
func (f WallSurfaceFrame) Outline() []mgl32.Vec2 {
	return []mgl32.Vec2{
		{0, 0},
		{f.WallLen, 0},
		{f.WallLen, f.CeilH2 - f.FloorH2},
		{0, f.CeilH1 - f.FloorH1},
	}
}

func (f WallSurfaceFrame) OutlineDepth(int) float32         { return 0 }
func (f WallSurfaceFrame) TexCoord(u, v float32) mgl32.Vec2 { return mgl32.Vec2{u, v} }
func (f WallSurfaceFrame) Planar() bool                     { return false }

// Normal is the wall's outward normal in 3D — the direction Depth runs in, and
// what the editor points its extrude arm along.
func (f WallSurfaceFrame) Normal() mgl32.Vec3 {
	return mgl32.Vec3{f.Norm.X(), 0, f.Norm.Y()}
}

// PlanarSurfaceFrame is a floor's or a ceiling's parameter space: (U, V) is
// chunk-local XZ and Depth is a world-Y offset from BaseHeight.
type PlanarSurfaceFrame struct {
	Points     []mgl32.Vec2 // sector outline, chunk-local XZ
	Heights    []float32    // absolute Y of each outline corner
	BaseHeight float32
	TileScale  float32
	Up         bool // floor (normal +Y) rather than ceiling
}

// NewPlanarSurfaceFrame builds the frame for a floor or ceiling. heightFunc is
// the surface's VertexHeight, which folds in its per-vertex offsets.
func NewPlanarSurfaceFrame(points []mgl32.Vec2, heightFunc func(int) float32, baseHeight, tileScale float32, up bool) PlanarSurfaceFrame {
	heights := make([]float32, len(points))
	for i := range points {
		heights[i] = heightFunc(i)
	}
	if tileScale == 0 {
		tileScale = 1
	}
	return PlanarSurfaceFrame{Points: points, Heights: heights, BaseHeight: baseHeight, TileScale: tileScale, Up: up}
}

func (f PlanarSurfaceFrame) ToWorld(u, v, depth float32) mgl32.Vec3 {
	return mgl32.Vec3{u, f.BaseHeight + depth, v}
}

// Outline is the sector polygon, wound so the surface faces the right way:
// CCW for a floor, CW for a ceiling. That is the same fix-up
// the old floor builder applied to its outer ring, moved to where the ring is
// produced so every consumer sees it already correct.
func (f PlanarSurfaceFrame) Outline() []mgl32.Vec2 {
	out := append([]mgl32.Vec2(nil), f.Points...)
	area := SignedArea2D(out)
	if (f.Up && area < 0) || (!f.Up && area > 0) {
		reverseVec2Slice(out)
	}
	return out
}

// OutlineDepth is the corner's height offset from the base plane. It is read
// through Outline's ordering, so a ceiling's reversal carries the depths with
// it rather than pairing corner 0's height with corner n-1's position.
func (f PlanarSurfaceFrame) OutlineDepth(i int) float32 {
	if len(f.Heights) == 0 {
		return 0
	}
	area := SignedArea2D(f.Points)
	if (f.Up && area < 0) || (!f.Up && area > 0) {
		i = len(f.Heights) - 1 - i
	}
	if i < 0 || i >= len(f.Heights) {
		return 0
	}
	return f.Heights[i] - f.BaseHeight
}

func (f PlanarSurfaceFrame) TexCoord(u, v float32) mgl32.Vec2 {
	return mgl32.Vec2{u / f.TileScale, v / f.TileScale}
}

func (f PlanarSurfaceFrame) Planar() bool { return true }

// ---------- seeding and boundary sync ----------

// SeedSurfaceMesh returns the mesh a flat surface would have if it were written
// out explicitly: the outline ring, and one face covering it.
//
// The editor calls it the first time somebody edits a surface that has been
// getting by on the empty-mesh fallback, so there is something to select.
func SeedSurfaceMesh(frame SurfaceFrame) SurfaceMesh {
	outline := frame.Outline()
	if len(outline) < 3 {
		return SurfaceMesh{}
	}
	m := SurfaceMesh{Points: make([]SurfacePoint, len(outline))}
	verts := make([]int, len(outline))
	for i, p := range outline {
		m.Points[i] = SurfacePoint{U: p.X(), V: p.Y(), Depth: frame.OutlineDepth(i), Boundary: i}
		verts[i] = i
	}
	m.Faces = []SurfaceFace{{Verts: verts, Alpha: 1}}
	return m
}

// SyncSurfaceBoundary rewrites every boundary point from the frame, which is
// what makes the outline the sector's property rather than the mesh's. Called
// on every rebuild, so a mesh cannot hold a stale corner: move a point on the
// 2D map or retype a floor height and the mesh has already followed by the time
// anything draws it.
//
// A boundary point whose corner no longer exists is demoted to a free point
// rather than dropped. The mesh is still referencing it from a face, and
// deleting it here would leave that face indexing past the end of the pool;
// RemapSurfaceMesh is where a shrinking outline is dealt with properly.
func SyncSurfaceBoundary(m *SurfaceMesh, frame SurfaceFrame) {
	if m == nil || len(m.Points) == 0 {
		return
	}
	outline := frame.Outline()
	for i := range m.Points {
		b := m.Points[i].Boundary
		if b < 0 {
			continue
		}
		if b >= len(outline) {
			m.Points[i].Boundary = SurfaceFree
			continue
		}
		m.Points[i].U = outline[b].X()
		m.Points[i].V = outline[b].Y()
		m.Points[i].Depth = frame.OutlineDepth(b)
	}
}

// ---------- building ----------

// SurfaceSubmesh is a run of geometry that has to be drawn separately from the
// surface's own: a face carrying its own texture. This is what an Extrusion's
// custom back-face texture used to compile to, under a name that no longer says
// "extrusion", since a mesh face is not one.
type SurfaceSubmesh struct {
	Texture  string
	Vertices [][]float32
	Indices  []int
	Alpha    float32
}

// BuildSurfaceMesh compiles a surface mesh into GPU-ready geometry.
//
// Returns the main buffer (everything inheriting the surface's own texture),
// and one submesh per distinct overriding texture. An empty mesh compiles to
// the frame's bare outline, which is the flat surface every unedited face in
// the level still is.
//
// openings are cut out of planar surfaces only — a hole through a *wall* is a
// mesh face marked Hole, not an Opening, and Opening's own machinery (pairing,
// slab-face cutting, the culling BFS) is about connecting one storey to
// another, which is not what a wall does.
func BuildSurfaceMesh(
	frame SurfaceFrame,
	mesh *SurfaceMesh,
	doubleSided bool,
	openings []Opening,
) ([][]float32, []int, []SurfaceSubmesh) {
	faces, points := surfaceFacesFor(frame, mesh)
	if len(faces) == 0 {
		return nil, nil, nil
	}

	var mainVerts [][]float32
	var mainIdx []int
	subs := map[string]*SurfaceSubmesh{}
	var subOrder []string

	for fi := range faces {
		f := &faces[fi]
		g := surfaceFaceGeometry(frame, points, f, openings)
		if len(g.Tris) < 3 {
			continue
		}

		verts := make([][]float32, len(g.Pts))
		for i, pt := range g.Pts {
			w := frame.ToWorld(pt.X(), pt.Y(), g.Depths[i])
			verts[i] = []float32{w.X(), w.Y(), w.Z(), g.UVs[i].X(), g.UVs[i].Y()}
		}

		dst, dstIdx := &mainVerts, &mainIdx
		if f.Texture != "" {
			sub, ok := subs[f.Texture]
			if !ok {
				sub = &SurfaceSubmesh{Texture: f.Texture, Alpha: f.Alpha}
				subs[f.Texture] = sub
				subOrder = append(subOrder, f.Texture)
			}
			dst, dstIdx = &sub.Vertices, &sub.Indices
		}

		base := len(*dst)
		*dst = append(*dst, verts...)
		for _, t := range g.Tris {
			*dstIdx = append(*dstIdx, base+t)
		}
		// A double-sided surface gets the reverse winding too, so the mesh
		// reads from behind. Emitted as its own copy of the vertices rather
		// than reusing them, because Geometry.Load derives a flat normal per
		// triangle and one shared vertex would have to carry both.
		if doubleSided {
			rev := len(*dst)
			for _, v := range verts {
				c := make([]float32, len(v))
				copy(c, v)
				*dst = append(*dst, c)
			}
			for i := 0; i+2 < len(g.Tris); i += 3 {
				*dstIdx = append(*dstIdx, rev+g.Tris[i+2], rev+g.Tris[i+1], rev+g.Tris[i])
			}
		}
	}

	out := make([]SurfaceSubmesh, 0, len(subOrder))
	for _, tex := range subOrder {
		out = append(out, *subs[tex])
	}
	return mainVerts, mainIdx, out
}

// surfaceFacesFor returns the faces and points to compile: the mesh's own once
// its boundary has been synced, or a single outline face when it is empty.
func surfaceFacesFor(frame SurfaceFrame, mesh *SurfaceMesh) ([]SurfaceFace, []SurfacePoint) {
	if mesh.IsEmpty() {
		seeded := SeedSurfaceMesh(frame)
		return seeded.Faces, seeded.Points
	}
	SyncSurfaceBoundary(mesh, frame)
	return mesh.Faces, mesh.Points
}

// surfaceFaceGeom is one face flattened into its own parameter space and cut
// into triangles: positions, depths and UVs that are index-parallel, plus
// triangle indices into them.
//
// It is deliberately *not* world-space. Rendering and collision both need this
// same cut — one to place it with the frame, the other to query heights from it
// — and computing it once here is what stops the two disagreeing about where a
// hole's rim is. A disagreement there is a hole you can see but stand on, or
// one you fall through without seeing.
//
// The corner count is not len(f.Verts): clipping holes introduces points nobody
// authored — the rims themselves, and the duplicated corners bridgeHole nudges
// aside to join a ring to the outline.
type surfaceFaceGeom struct {
	Pts    []mgl32.Vec2
	Depths []float32
	UVs    []mgl32.Vec2
	Tris   []int
}

// surfaceFaceGeometry cuts one face up, holes and all.
//
// Two paths, chosen by whether there is anything to cut out:
//
//   - nothing: the face is triangulated in *3D*, on its own best-fit plane.
//     That is essential rather than merely better — an extrusion's side wall
//     stands perpendicular to the surface, so it has zero area in (U, V) and a
//     2D clipper would refuse it.
//   - something: the face is clipped in (U, V), where its own rings and any
//     Opening polygon share a coordinate system. A face carrying holes is a
//     base-plane face by construction, so flattening it loses nothing, and the
//     new points' depth and UV are interpolated across the face rather than
//     dropped onto the base plane — over the same triangle fan
//     InterpolateSurfaceHeight answers a collision query from. Flattening them
//     was the old behaviour and is indistinguishable on a level floor; it
//     showed up once the surface sloped, as a step around the hole and a crack
//     at the bridge where the two sides disagreed.
func surfaceFaceGeometry(
	frame SurfaceFrame,
	points []SurfacePoint,
	f *SurfaceFace,
	openings []Opening,
) surfaceFaceGeom {
	var g surfaceFaceGeom
	if f.Hole || len(f.Verts) < 3 {
		return g
	}
	ring := make([]mgl32.Vec2, 0, len(f.Verts))
	for i, vi := range f.Verts {
		if vi < 0 || vi >= len(points) {
			return surfaceFaceGeom{}
		}
		p := points[vi]
		ring = append(ring, p.Vec2())
		g.Depths = append(g.Depths, p.Depth)
		if i < len(f.UVs) {
			g.UVs = append(g.UVs, f.UVs[i])
		} else {
			g.UVs = append(g.UVs, frame.TexCoord(p.U, p.V))
		}
	}
	g.Pts = ring

	holes := surfaceFaceHoleRings(points, f, ring)
	if frame.Planar() {
		holes = append(holes, surfaceOpeningRings(ring, openings)...)
	}

	if len(holes) == 0 {
		world := make([]mgl32.Vec3, len(ring))
		for i := range ring {
			world[i] = frame.ToWorld(ring[i].X(), ring[i].Y(), g.Depths[i])
		}
		for _, t := range geom.TriangulatePolygon3D(world) {
			g.Tris = append(g.Tris, t[0], t[1], t[2])
		}
		// An Opening that only partly overlaps the face has no single ring to
		// clip against, so it is cut by dropping the triangles whose centroid
		// falls in it. That is what a terrain floor already did, and it is as
		// precise as the triangulation near the rim.
		if frame.Planar() && len(openings) > 0 {
			g.Tris = flattenTris(dropSurfaceTrisInOpenings(ring, g.Tris, openings))
		}
		return g
	}

	merged, tris := geom.EarClipWithHoles(ring, holes)
	if len(tris) < 3 {
		return surfaceFaceGeom{}
	}
	out := surfaceFaceGeom{Pts: merged, Tris: tris}
	for _, pt := range merged {
		depth, uv := surfaceFanSample(ring, g.Depths, g.UVs, pt)
		out.Depths = append(out.Depths, depth)
		out.UVs = append(out.UVs, uv)
	}
	if frame.Planar() && len(openings) > 0 {
		out.Tris = flattenTris(dropSurfaceTrisInOpenings(merged, out.Tris, openings))
	}
	return out
}

// surfaceFaceHoleRings resolves a face's Holes into parameter-space rings,
// wound against the face's own so the ear clipper takes them as holes.
func surfaceFaceHoleRings(points []SurfacePoint, f *SurfaceFace, ring []mgl32.Vec2) [][]mgl32.Vec2 {
	if len(f.Holes) == 0 {
		return nil
	}
	ringCCW := SignedArea2D(ring) > 0
	var out [][]mgl32.Vec2
	for _, h := range f.Holes {
		if len(h) < 3 {
			continue
		}
		poly := make([]mgl32.Vec2, 0, len(h))
		ok := true
		for _, vi := range h {
			if vi < 0 || vi >= len(points) {
				ok = false
				break
			}
			poly = append(poly, points[vi].Vec2())
		}
		if !ok || len(poly) < 3 {
			continue
		}
		if (SignedArea2D(poly) > 0) == ringCCW {
			reverseVec2Slice(poly)
		}
		out = append(out, poly)
	}
	return out
}

// surfaceOpeningRings returns the openings this face wholly contains, wound
// against it.
//
// "Wholly contains" is every corner of the hole inside the ring. A partial
// overlap is deliberately excluded: bridgeHole assumes the hole is interior,
// and one that crosses the outline produces a bridge leaving the polygon and
// triangles spilling outside the face. Those fall to the centroid cut instead.
func surfaceOpeningRings(ring []mgl32.Vec2, openings []Opening) [][]mgl32.Vec2 {
	if len(openings) == 0 || len(ring) < 3 {
		return nil
	}
	ringCCW := SignedArea2D(ring) > 0
	var holes [][]mgl32.Vec2
	for i := range openings {
		op := &openings[i]
		if len(op.Points) < 3 {
			continue
		}
		inside := true
		for _, p := range op.Points {
			if !geom.PointInPolygon(p, ring) {
				inside = false
				break
			}
		}
		if !inside {
			continue
		}
		hole := append([]mgl32.Vec2(nil), op.Points...)
		if (SignedArea2D(hole) > 0) == ringCCW {
			reverseVec2Slice(hole)
		}
		holes = append(holes, hole)
	}
	return holes
}

// surfaceFanSample interpolates a face's depth and UV at an arbitrary point in
// its parameter space, over a fan from corner 0.
func surfaceFanSample(ring []mgl32.Vec2, depths []float32, uvs []mgl32.Vec2, pt mgl32.Vec2) (float32, mgl32.Vec2) {
	// An exact corner match is both cheaper and more precise than interpolating
	// a corner from its own neighbours.
	for i, rp := range ring {
		if rp.Sub(pt).LenSqr() < 1e-6 {
			return depths[i], uvs[i]
		}
	}
	for i := 1; i < len(ring)-1; i++ {
		inside, wA, wB, wC := geom.PointInTriangle2D(pt, ring[0], ring[i], ring[i+1])
		if !inside {
			continue
		}
		depth := wA*depths[0] + wB*depths[i] + wC*depths[i+1]
		uv := uvs[0].Mul(wA).Add(uvs[i].Mul(wB)).Add(uvs[i+1].Mul(wC))
		return depth, uv
	}
	if len(uvs) > 0 {
		return 0, uvs[0]
	}
	return 0, mgl32.Vec2{}
}

// dropSurfaceTrisInOpenings removes the triangles whose centroid falls in a
// hole — the approximate cut, for a face an opening only partly covers.
func dropSurfaceTrisInOpenings(ring []mgl32.Vec2, tris []int, openings []Opening) [][3]int {
	var out [][3]int
	for i := 0; i+2 < len(tris); i += 3 {
		a, b, c := tris[i], tris[i+1], tris[i+2]
		if a >= len(ring) || b >= len(ring) || c >= len(ring) {
			continue
		}
		mid := ring[a].Add(ring[b]).Add(ring[c]).Mul(1.0 / 3.0)
		if SurfaceIsOpenAt(openings, mid) {
			continue
		}
		out = append(out, [3]int{a, b, c})
	}
	return out
}

func flattenTris(tris [][3]int) []int {
	out := make([]int, 0, len(tris)*3)
	for _, t := range tris {
		out = append(out, t[0], t[1], t[2])
	}
	return out
}

// ---------- collision data ----------

// SurfaceCollisionMesh flattens a planar surface's mesh into the triangle soup
// InterpolateSurfaceHeight queries: XZ positions, depths relative to the base
// plane, and index triples.
//
// This is the same cache buildFloorCollisionData produced from a Delaunay
// terrain, filled from the mesh instead — which is what makes a floor's bumps
// something a body actually walks on rather than something it walks through.
//
// Faces marked Hole contribute nothing, so standing over one finds no triangle
// and the caller falls through, exactly as an Opening already behaves.
func SurfaceCollisionMesh(frame SurfaceFrame, mesh *SurfaceMesh, openings []Opening) ([]mgl32.Vec2, []float32, []int) {
	if !frame.Planar() {
		return nil, nil, nil
	}
	faces, points := surfaceFacesFor(frame, mesh)
	if len(faces) == 0 {
		return nil, nil, nil
	}

	var pts []mgl32.Vec2
	var depths []float32
	var tris []int
	for fi := range faces {
		g := surfaceFaceGeometry(frame, points, &faces[fi], openings)
		if len(g.Tris) < 3 {
			continue
		}
		base := len(pts)
		pts = append(pts, g.Pts...)
		depths = append(depths, g.Depths...)
		for _, t := range g.Tris {
			tris = append(tris, base+t)
		}
	}
	if len(tris) == 0 {
		return nil, nil, nil
	}
	return pts, depths, tris
}

// SurfaceWallWindows returns the (U, V) rings of a wall mesh's Hole faces —
// the regions a body may pass through.
//
// The wall's collision plane stays flat: a mesh can push a wall's surface in
// and out, but what stops a body is still the wall's straight segment on the
// 2D map, with these windows punched through it. That is the same contract
// DetailSpec already ships with — its displacement is not collided with either
// — and it is what keeps wall collision a segment test rather than a swept
// mesh query. Bumps you can walk into; holes you can walk through.
func SurfaceWallWindows(mesh *SurfaceMesh) [][]mgl32.Vec2 {
	if mesh.IsEmpty() {
		return nil
	}
	var out [][]mgl32.Vec2
	for fi := range mesh.Faces {
		f := &mesh.Faces[fi]
		if !f.Hole || len(f.Verts) < 3 {
			continue
		}
		ring := make([]mgl32.Vec2, 0, len(f.Verts))
		for _, vi := range f.Verts {
			if vi < 0 || vi >= len(mesh.Points) {
				ring = nil
				break
			}
			ring = append(ring, mesh.Points[vi].Vec2())
		}
		if len(ring) >= 3 {
			out = append(out, ring)
		}
	}
	return out
}

// MinDepth and MaxDepth are the extremes a mesh reaches along its depth axis,
// and false when the mesh is empty and so reaches nothing.
//
// They exist for LocalAABB, which has to cover whatever an author carved before
// it can decide the sector is off screen. A box drawn to the flat plane would
// cull a sector while the bottom of its pit was still visible.
func (m *SurfaceMesh) MinDepth() (float32, bool) { return m.depthExtreme(true) }

// MaxDepth is MinDepth's mirror.
func (m *SurfaceMesh) MaxDepth() (float32, bool) { return m.depthExtreme(false) }

func (m *SurfaceMesh) depthExtreme(min bool) (float32, bool) {
	if m == nil || len(m.Points) == 0 {
		return 0, false
	}
	best := m.Points[0].Depth
	for _, p := range m.Points[1:] {
		if (min && p.Depth < best) || (!min && p.Depth > best) {
			best = p.Depth
		}
	}
	return best, true
}

// reverseVec2Slice reverses a slice of Vec2 in place. Winding fix-ups are the
// only thing that needs it, and every one of them is in this package.
func reverseVec2Slice(s []mgl32.Vec2) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
