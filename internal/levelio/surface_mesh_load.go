package levelio

// surface_mesh_load.go — filling each surface's mesh as a chunk is loaded.
//
// A SurfaceMesh cannot be built in the struct literals that make a sector,
// because building one needs a SurfaceFrame and a frame needs the sector's
// points, heights and wall list to be final. They are not: vertex offsets get
// reversed if the polygon was wound the wrong way, and InferPortals decides
// after the fact which edges are walls at all. So the meshes are filled in a
// pass of their own, immediately before Sector.Load compiles the geometry.
//
// Two sources, in order of precedence:
//
//   - `mesh:` in the level file, which is what the writer emits now.
//   - the legacy `extrusions:` / `terrain_points:` keys, migrated on the spot
//     (world.MigrateSurfaceMesh). A level written before surface meshes existed
//     loads, edits and saves as one, and the old keys are never written back.
//
// A file carrying both — a level saved by a new build and then hand-merged with
// an old one — takes the mesh and ignores the rest, because the mesh is the
// representation the author has actually been editing.

import (
	"foundry/internal/geom"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// loadSectorSurfaceMeshes fills the floor, ceiling and wall meshes of one
// sector from its raw form.
func loadSectorSurfaceMeshes(ch *world.Chunk, s *world.Sector, rs *world.RawSector, rawPts map[string]geom.Point) {
	if rs == nil {
		return
	}
	pts := world.SectorPoints(ch, s)
	if len(pts) < 3 {
		return
	}

	s.Floor.Mesh = surfaceMeshFor(
		s.FloorFrame(pts),
		rs.Floor.Mesh, rs.Floor.Extrusions, rs.Floor.TerrainPoints, rs.Floor.Terrain)
	s.Ceiling.Mesh = surfaceMeshFor(
		s.CeilingFrame(pts),
		rs.Ceiling.Mesh, rs.Ceiling.Extrusions, rs.Ceiling.TerrainPoints, rs.Ceiling.Terrain)

	for wi := range s.Walls {
		re := rawEdgeForWall(rs, rawPts, &s.Walls[wi])
		if re == nil {
			continue
		}
		frame, ok := s.WallFrameAt(pts, wi)
		if !ok {
			continue
		}
		s.Walls[wi].Mesh = surfaceMeshFor(frame, re.Mesh, re.Extrusions, re.TerrainPoints, re.WallTerrain)
	}
}

// rawEdgeForWall finds the file entry a loaded wall came from.
//
// By ID first: Walls and rs.Edges are not parallel lists — Walls holds only the
// edges that are walls, and InferPortals may have turned some of them into
// portals since the list was built — and an edge's ID survives that conversion
// (sector_edge_id.go), which makes it the durable handle back to the file.
//
// By endpoints when there is no ID. IDs are optional in the format and the
// loader mints one for any edge that arrives without, so by the time a wall
// exists its ID may be an invention that matches nothing on disk. The endpoints
// are the same either way, and an edge is uniquely identified within a sector
// by the pair of points it runs between — in either direction, since the loader
// re-winds a sector whose polygon was authored the wrong way round.
func rawEdgeForWall(rs *world.RawSector, rawPts map[string]geom.Point, w *world.Wall) *world.RawEdge {
	if w.ID != "" {
		for i := range rs.Edges {
			if rs.Edges[i].ID == w.ID {
				return &rs.Edges[i]
			}
		}
	}
	if rawPts == nil {
		return nil
	}
	for i := range rs.Edges {
		from, okF := rawPts[rs.Edges[i].From]
		to, okT := rawPts[rs.Edges[i].To]
		if !okF || !okT {
			continue
		}
		a := mgl32.Vec2{from.X, from.Y}
		b := mgl32.Vec2{to.X, to.Y}
		if (near2(a, w.StartPoint) && near2(b, w.EndPoint)) ||
			(near2(b, w.StartPoint) && near2(a, w.EndPoint)) {
			return &rs.Edges[i]
		}
	}
	return nil
}

func near2(a, b mgl32.Vec2) bool { return a.Sub(b).LenSqr() < 1e-8 }

// surfaceMeshFor resolves one surface's mesh from whichever of the two sources
// the file has.
func surfaceMeshFor(
	frame world.SurfaceFrame,
	raw *world.RawSurfaceMesh,
	rawExt []world.RawExtrusion,
	rawTerrain []world.RawTerrainPoint,
	terrain bool,
) world.SurfaceMesh {
	if raw != nil {
		if m := world.SurfaceMeshFromRaw(raw); !m.IsEmpty() {
			return m
		}
	}
	if len(rawExt) == 0 && len(rawTerrain) == 0 {
		return world.SurfaceMesh{}
	}
	return world.MigrateSurfaceMesh(frame, loadExtrusions(rawExt), loadTerrainPts(rawTerrain), terrain)
}

// loadExtrusions converts the legacy YAML extrusion form. It exists only to
// feed the migration; nothing writes this shape any more.
func loadExtrusions(raws []world.RawExtrusion) []world.Extrusion {
	if len(raws) == 0 {
		return nil
	}
	out := make([]world.Extrusion, 0, len(raws))
	for _, re := range raws {
		ext := world.Extrusion{
			Depth:       re.Depth,
			Texture:     re.Texture,
			Visible:     world.BoolDefault(re.Visible, true),
			BackVisible: world.BoolDefault(re.BackVisible, true),
			UVOffset:    mgl32.Vec2{re.UVOffset.X, re.UVOffset.Y},
			UVScale:     mgl32.Vec2{re.UVScale.X, re.UVScale.Y},
			UVRotation:  re.UVRotation,
			Alpha:       float32Default(re.Alpha, 1.0),
		}
		for _, p := range re.Outer {
			ext.Outer = append(ext.Outer, world.ExtrusionPoint{U: p.U, V: p.V})
		}
		for _, p := range re.Inner {
			ext.Inner = append(ext.Inner, world.ExtrusionPoint{U: p.U, V: p.V})
		}
		// An extrusion with no inner polygon is a straight prism; the old
		// builder treated a length mismatch as a reason to skip the extrusion
		// entirely, which would silently drop a doorway on migration.
		if len(ext.Inner) != len(ext.Outer) {
			ext.Inner = append([]world.ExtrusionPoint(nil), ext.Outer...)
		}
		out = append(out, ext)
	}
	return out
}

// loadTerrainPts converts the legacy YAML terrain-point form.
func loadTerrainPts(raws []world.RawTerrainPoint) []world.TerrainPoint {
	if len(raws) == 0 {
		return nil
	}
	out := make([]world.TerrainPoint, 0, len(raws))
	for _, rt := range raws {
		out = append(out, world.TerrainPoint{U: rt.U, V: rt.V, Depth: rt.Depth})
	}
	return out
}

// rawSurfaceMinDepth and rawSurfaceMaxDepth bound a surface's displacement
// straight from the raw data.
//
// The chunk's vertical bounds are rolled up before the meshes exist — the
// bounds pass runs while sectors are still being appended, and a mesh needs a
// finished sector. Reading the raw form is what lets the bound cover a carved
// surface anyway. RecomputeBounds later rolls up the real sector AABBs, so this
// only has to be no *tighter* than the truth.
func rawSurfaceMinDepth(m *world.RawSurfaceMesh, tp []world.RawTerrainPoint, ext []world.RawExtrusion, floor bool) (float32, bool) {
	return rawSurfaceDepthExtreme(m, tp, ext, floor, true)
}

func rawSurfaceMaxDepth(m *world.RawSurfaceMesh, tp []world.RawTerrainPoint, ext []world.RawExtrusion, floor bool) (float32, bool) {
	return rawSurfaceDepthExtreme(m, tp, ext, floor, false)
}

func rawSurfaceDepthExtreme(
	m *world.RawSurfaceMesh,
	tp []world.RawTerrainPoint,
	ext []world.RawExtrusion,
	floor, min bool,
) (float32, bool) {
	best := float32(0)
	found := false
	take := func(d float32) {
		if !found || (min && d < best) || (!min && d > best) {
			best, found = d, true
		}
	}
	if m != nil {
		for i := 2; i < len(m.Points); i += 3 {
			take(m.Points[i])
		}
	}
	for _, p := range tp {
		take(p.Depth)
	}
	for _, e := range ext {
		// The legacy sign convention, mirrored from migratedExtrusionDepth: a
		// positive Depth sank a floor and raised a ceiling.
		if floor {
			take(-e.Depth)
		} else {
			take(e.Depth)
		}
	}
	return best, found
}

// rawSectorByID finds a sector's raw form by ID. Index would do today — sectors
// are appended in file order — but the mesh pass runs after InferPortals and
// anything that ever filters or reorders the list would silently pair a
// sector's meshes with its neighbour's, which is not a failure that would show
// up as an error.
func rawSectorByID(raws []world.RawSector, id string) *world.RawSector {
	for i := range raws {
		if raws[i].ID == id {
			return &raws[i]
		}
	}
	return nil
}
