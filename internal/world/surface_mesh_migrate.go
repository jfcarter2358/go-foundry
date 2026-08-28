package world

// surface_mesh_migrate.go — turning the old displacement fields into a mesh.
//
// Extrusions and TerrainPoints were how a surface stopped being flat before
// SurfaceMesh existed, and every level on disk is written in them. Rather than
// keep both representations alive and make each consumer handle two, the loader
// converts once: a level read from YAML arrives in memory as a mesh, is edited
// as a mesh, and is written back out as one. The old fields survive only as
// input.
//
// The conversion is exact for extrusions — an Extrusion *is* a prism, and a
// prism is a rim, a set of sides and a cap, which is precisely what the mesh
// spells out. It is exact for terrain too: Delaunay already produced triangles
// and the mesh simply keeps them as faces instead of throwing the topology away
// and re-deriving it on every load.
//
// What the conversion buys, beyond one representation instead of four, is that
// the result is *editable*. A migrated doorway is not a special object with a
// Depth field any more; it is a hole ring, four side quads and a cap face, and
// every one of those can be selected, dragged, extruded again or deleted.

import (
	"foundry/internal/geom"

	"github.com/go-gl/mathgl/mgl32"
)

// MigrateSurfaceMesh builds the mesh equivalent of a surface's legacy
// displacement fields. It returns the zero mesh when there is nothing to
// convert, which is the overwhelming majority of surfaces and stays flat.
//
// terrain selects the terrain branch, mirroring the if/else Sector.Load used to
// run: a surface was either a Delaunay terrain or an extruded plane, never
// both, so the migration has the same two cases and no question about how they
// would have combined.
func MigrateSurfaceMesh(
	frame SurfaceFrame,
	extrusions []Extrusion,
	terrainPts []TerrainPoint,
	terrain bool,
) SurfaceMesh {
	if terrain && len(terrainPts) > 0 {
		return migrateTerrainMesh(frame, terrainPts)
	}
	if len(extrusions) > 0 {
		return migrateExtrusionMesh(frame, extrusions)
	}
	return SurfaceMesh{}
}

// migrateTerrainMesh keeps the Delaunay triangulation the terrain surface was
// already drawn from, as one face per triangle.
//
// Storing the triangles rather than re-triangulating on load is the point:
// Delaunay is unconstrained, so it answers a slightly different question every
// time the point set changes, and an author who nudged one point would find
// edges flipping elsewhere on the surface. Once the triangles are faces they
// are the author's, and only the edits they make change them.
func migrateTerrainMesh(frame SurfaceFrame, terrainPts []TerrainPoint) SurfaceMesh {
	outline := frame.Outline()
	if len(outline) < 3 {
		return SurfaceMesh{}
	}
	m := SurfaceMesh{Points: make([]SurfacePoint, 0, len(outline)+len(terrainPts))}
	pts := make([]mgl32.Vec2, 0, len(outline)+len(terrainPts))
	for i, p := range outline {
		m.Points = append(m.Points, SurfacePoint{U: p.X(), V: p.Y(), Depth: frame.OutlineDepth(i), Boundary: i})
		pts = append(pts, p)
	}
	for _, tp := range terrainPts {
		m.Points = append(m.Points, SurfacePoint{U: tp.U, V: tp.V, Depth: tp.Depth, Boundary: SurfaceFree})
		pts = append(pts, mgl32.Vec2{tp.U, tp.V})
	}

	// DelaunayTriangulate clips to the boundary it is given, and wants that
	// boundary CCW. Outline already hands a ceiling's back CW so the surface
	// faces down, so the ring is re-wound just for the triangulator and the
	// resulting faces are flipped back afterwards.
	boundary := append([]mgl32.Vec2(nil), outline...)
	flipped := false
	if SignedArea2D(boundary) < 0 {
		reverseVec2Slice(boundary)
		flipped = true
	}
	tris := geom.DelaunayTriangulate(pts, boundary)
	for i := 0; i+2 < len(tris); i += 3 {
		a, b, c := tris[i], tris[i+1], tris[i+2]
		if flipped {
			a, c = c, a
		}
		m.Faces = append(m.Faces, SurfaceFace{Verts: []int{a, b, c}, Alpha: 1})
	}
	if len(m.Faces) == 0 {
		return SurfaceMesh{}
	}
	return m
}

// migrateExtrusionMesh converts a plane carrying extrusions.
//
// Each Extrusion becomes three things: a hole ring in the base face at its
// Outer polygon, a side quad per rim edge running out to its Inner polygon at
// Depth, and a cap face on the Inner ring. Visible and BackVisible decide which
// of those are drawn, exactly as they decided which the old builder emitted.
//
// The base stays a single face with holes rather than being clipped into
// triangles here, because a face is what the editor selects and textures — a
// wall that grew a doorway should not thereby become eight separate things to
// click.
func migrateExtrusionMesh(frame SurfaceFrame, extrusions []Extrusion) SurfaceMesh {
	outline := frame.Outline()
	if len(outline) < 3 {
		return SurfaceMesh{}
	}
	m := SurfaceMesh{Points: make([]SurfacePoint, 0, len(outline))}
	base := SurfaceFace{Verts: make([]int, 0, len(outline)), Alpha: 1}
	for i, p := range outline {
		m.Points = append(m.Points, SurfacePoint{U: p.X(), V: p.Y(), Depth: frame.OutlineDepth(i), Boundary: i})
		base.Verts = append(base.Verts, i)
	}
	baseRing := append([]mgl32.Vec2(nil), outline...)
	baseCCW := SignedArea2D(baseRing) > 0
	baseDepths := migrateOutlineDepths(frame, len(outline))
	baseUVs := migrateOutlineUVs(frame, outline)

	// Reserve slot 0 for the base so cap and side faces come after it, which
	// keeps a migrated mesh's face order stable and readable.
	m.Faces = append(m.Faces, base)

	for ei := range extrusions {
		ext := &extrusions[ei]
		n := len(ext.Outer)
		if n < 3 || len(ext.Inner) != n {
			continue
		}
		depth := migratedExtrusionDepth(frame, ext.Depth)

		// The rim sits on the surface, at whatever depth the base already has
		// there — the old builder put it on the flat base plane, which stepped
		// visibly wherever the surface sloped.
		rim := make([]int, n)
		for i, p := range ext.Outer {
			d, _ := surfaceFanSample(baseRing, baseDepths, baseUVs, mgl32.Vec2{p.U, p.V})
			rim[i] = m.AddPoint(p.U, p.V, d)
		}
		lid := make([]int, n)
		for i, p := range ext.Inner {
			lid[i] = m.AddPoint(p.U, p.V, depth)
		}

		// A hole ring must wind against the face it is cut in.
		holeRing := append([]int(nil), rim...)
		if migrateRingCCW(m.Points, holeRing) == baseCCW {
			reverseIntSlice(holeRing)
		}
		m.Faces[0].Holes = append(m.Faces[0].Holes, holeRing)

		// Sides. [rim i, rim j, cap j, cap i] is the corner order
		// RefreshSurfaceSideUVs reads, and the winding the old side quads had.
		if ext.Visible {
			for i := 0; i < n; i++ {
				j := (i + 1) % n
				m.Faces = append(m.Faces, SurfaceFace{
					Verts:   []int{rim[i], rim[j], lid[j], lid[i]},
					Texture: ext.Texture,
					Alpha:   migrateAlpha(ext.Alpha),
				})
			}
		}
		// The cap is the inner polygon at depth. An extrusion that was neither
		// Visible nor BackVisible cut a hole and drew nothing, so its cap is
		// marked Hole and the ring survives only as something to drag.
		m.Faces = append(m.Faces, SurfaceFace{
			Verts:   lid,
			Texture: ext.Texture,
			Alpha:   migrateAlpha(ext.Alpha),
			Hole:    !ext.Visible && !ext.BackVisible,
		})
	}

	if len(m.Faces) == 1 && len(m.Faces[0].Holes) == 0 {
		return SurfaceMesh{}
	}
	m.RefreshSurfaceSideUVs(migrateSideFaceIndices(&m))
	return m
}

// migratedExtrusionDepth converts an Extrusion's Depth into a SurfacePoint one.
//
// The old field meant different things on different surfaces, which is part of
// why it is being retired. On a wall it was already displacement along the
// outward normal and carries over unchanged. On a floor a positive Depth went
// *down* and on a ceiling it went *up* — both "into the solid" — while
// TerrainPoint.Depth and VertexOffsets on the very same surfaces were plain +Y
// offsets. SurfacePoint.Depth is the +Y one, so the plane cases flip sign.
func migratedExtrusionDepth(frame SurfaceFrame, depth float32) float32 {
	pf, ok := frame.(PlanarSurfaceFrame)
	if !ok {
		return depth
	}
	if pf.Up {
		return -depth // floor: old positive Depth sank a pit
	}
	return depth // ceiling: old positive Depth already rose
}

// migrateAlpha maps an unset Extrusion.Alpha to opaque. Zero meant "never
// touched" far more often than it meant "invisible", and a level that had
// simply never opened the alpha control should not load with holes in it.
func migrateAlpha(a float32) float32 {
	if a <= 0 {
		return 1
	}
	return a
}

func migrateOutlineDepths(frame SurfaceFrame, n int) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = frame.OutlineDepth(i)
	}
	return out
}

func migrateOutlineUVs(frame SurfaceFrame, outline []mgl32.Vec2) []mgl32.Vec2 {
	out := make([]mgl32.Vec2, len(outline))
	for i, p := range outline {
		out[i] = frame.TexCoord(p.X(), p.Y())
	}
	return out
}

func migrateRingCCW(points []SurfacePoint, ring []int) bool {
	poly := make([]mgl32.Vec2, len(ring))
	for i, vi := range ring {
		if vi < 0 || vi >= len(points) {
			return true
		}
		poly[i] = points[vi].Vec2()
	}
	return SignedArea2D(poly) > 0
}

// migrateSideFaceIndices is every four-cornered face that is not the base —
// the sides, which need their UVs derived from the depth they ended up at.
func migrateSideFaceIndices(m *SurfaceMesh) []int {
	var out []int
	for i := 1; i < len(m.Faces); i++ {
		if len(m.Faces[i].Verts) == 4 {
			out = append(out, i)
		}
	}
	return out
}

func reverseIntSlice(s []int) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
