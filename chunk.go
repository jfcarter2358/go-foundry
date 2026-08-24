package main

import (
	"foundry/internal/gfx"
	"log"

	"github.com/go-gl/mathgl/mgl32"
)

// Chunk is a top-level spatial grouping of sectors.
// MinBound/MaxBound are 3D AABBs (Y-up) used for frustum culling.
// Origin, Rotation, and Scale are editor-set transforms baked into sector
// geometry at load time.
//
// PointNames is index-aligned with Points and carries each point's YAML name so
// SaveLevel can round-trip it. It must be maintained by every mutation of the
// point pool — use AddPoint/RemovePoint rather than touching Points directly.
// An empty string means "not yet named"; SaveLevel mints a stable unique name
// for those slots. Binding names to indices rather than to coordinates is what
// lets a point keep its identity when the editor drags it.
type Chunk struct {
	ID           string
	ScriptData                // tags, attributes and the attached script
	Points       []mgl32.Vec2 // shared point pool; sectors reference by index
	Sectors      []Sector
	MinBound     mgl32.Vec3
	MaxBound     mgl32.Vec3
	Origin       mgl32.Vec3    // world-space offset applied to all points at load time
	Rotation     float32       // Y-axis rotation in radians applied at load time
	Scale        float32       // uniform XZ scale applied at load time (0 = 1.0)
	PointNames   []string      // index-aligned with Points; "" = unnamed
	BiomeSectors []BiomeSector // user-drawn biome-paint polygons, owned by this chunk

	// WaterSurfaces is runtime-only (never serialized, never cloned): the
	// companion water planes auto-managed from Water-kind BiomeSectors (see
	// biome_water.go). They are fully derived from BiomeSectors and rebuilt on
	// load and whenever the owning sector changes.
	WaterSurfaces []WaterSurface
}

// AddPoint appends a point to the shared pool and returns its index, keeping
// PointNames index-aligned. The point starts unnamed; SaveLevel assigns it a
// stable name on the next serialise.
func (ch *Chunk) AddPoint(p mgl32.Vec2) int {
	idx := len(ch.Points)
	ch.Points = append(ch.Points, p)
	for len(ch.PointNames) < idx {
		ch.PointNames = append(ch.PointNames, "")
	}
	ch.PointNames = append(ch.PointNames, "")
	return idx
}

// RemovePoint deletes the point at index pi from the shared pool, keeping
// PointNames index-aligned. Callers remain responsible for fixing up sector
// PointIndices and any other index references.
func (ch *Chunk) RemovePoint(pi int) {
	if pi < 0 || pi >= len(ch.Points) {
		return
	}
	ch.Points = append(ch.Points[:pi], ch.Points[pi+1:]...)
	if pi < len(ch.PointNames) {
		ch.PointNames = append(ch.PointNames[:pi], ch.PointNames[pi+1:]...)
	}
}

// Clone returns a fully-independent deep copy of the chunk. All slices and
// maps get fresh backing arrays so edits to the copy do not affect the source.
// GPU geometry handles are intentionally zeroed — the caller must re-upload
// sector/gap geometry before the clone can be rendered. Sector IDs are
// rewritten with idSuffix so they stay unique within the
// level; in-chunk portal ToSector references are remapped to the new IDs.
func (ch *Chunk) Clone(newID, idSuffix string) Chunk {
	cp := Chunk{
		ID:         newID,
		ScriptData: cloneScriptData(ch.ScriptData),
		MinBound:   ch.MinBound,
		MaxBound:   ch.MaxBound,
		Origin:     ch.Origin,
		Rotation:   ch.Rotation,
		Scale:      ch.Scale,
	}

	cp.Points = append([]mgl32.Vec2(nil), ch.Points...)

	cp.PointNames = append([]string(nil), ch.PointNames...)

	// Build old-sector-ID → new-sector-ID map for portal remapping.
	sectorIDRemap := make(map[string]string, len(ch.Sectors))
	for i := range ch.Sectors {
		oldID := ch.Sectors[i].ID
		sectorIDRemap[oldID] = oldID + idSuffix
	}

	cp.Sectors = make([]Sector, len(ch.Sectors))
	for si := range ch.Sectors {
		cp.Sectors[si] = cloneSector(&ch.Sectors[si], idSuffix, sectorIDRemap)
	}

	if ch.BiomeSectors != nil {
		cp.BiomeSectors = make([]BiomeSector, len(ch.BiomeSectors))
		for bi := range ch.BiomeSectors {
			src := &ch.BiomeSectors[bi]
			cp.BiomeSectors[bi] = BiomeSector{
				ID:          src.ID + idSuffix,
				Points:      append([]mgl32.Vec2(nil), src.Points...),
				Brush:       src.Brush,
				HeightDirty: true,
				GroundDirty: true,
			}
		}
	}
	// WaterSurfaces is intentionally left empty — it's fully derived from
	// BiomeSectors (marked GroundDirty above) and gets rebuilt the next time
	// SyncBiomeWaterSurfaces runs.

	return cp
}

func cloneSector(s *Sector, idSuffix string, sectorIDRemap map[string]string) Sector {
	out := Sector{
		ID:         s.ID + idSuffix,
		ScriptData: cloneScriptData(s.ScriptData),
		MinBound:   s.MinBound,
		MaxBound:   s.MaxBound,
	}
	out.PointIndices = append([]int(nil), s.PointIndices...)
	out.EdgeKinds = append([]string(nil), s.EdgeKinds...)
	// Edge IDs are scoped to their sector, so a clone keeps them as they are —
	// unlike the sector ID itself, which is level-scoped and takes the suffix.
	out.EdgeIDs = append([]string(nil), s.EdgeIDs...)

	out.Walls = make([]Wall, len(s.Walls))
	for i := range s.Walls {
		out.Walls[i] = cloneWall(&s.Walls[i])
	}

	out.Portals = make([]Portal, len(s.Portals))
	for i := range s.Portals {
		out.Portals[i] = clonePortal(&s.Portals[i], sectorIDRemap)
	}

	out.Floor = cloneFloor(&s.Floor)
	out.Ceiling = cloneCeiling(&s.Ceiling)
	return out
}

func cloneWall(w *Wall) Wall {
	out := *w
	out.ScriptData = cloneScriptData(w.ScriptData)
	out.Geometry = gfx.Geometry{}
	out.Extrusions = cloneExtrusions(w.Extrusions)
	out.TerrainPoints = append([]TerrainPoint(nil), w.TerrainPoints...)
	return out
}

func clonePortal(p *Portal, sectorIDRemap map[string]string) Portal {
	out := *p
	out.ScriptData = cloneScriptData(p.ScriptData)
	out.FloorGap = gfx.Geometry{}
	out.CeilGap = gfx.Geometry{}
	if remapped, ok := sectorIDRemap[p.ToSector]; ok {
		out.ToSector = remapped
	}
	return out
}

func cloneFloor(f *Floor) Floor {
	out := *f
	out.ScriptData = cloneScriptData(f.ScriptData)
	out.Geometry = gfx.Geometry{}
	out.VertexOffsets = append([]float32(nil), f.VertexOffsets...)
	out.Extrusions = cloneExtrusions(f.Extrusions)
	out.TerrainPoints = append([]TerrainPoint(nil), f.TerrainPoints...)
	out.CollisionPts = append([]mgl32.Vec2(nil), f.CollisionPts...)
	out.CollisionDepths = append([]float32(nil), f.CollisionDepths...)
	out.CollisionTris = append([]int(nil), f.CollisionTris...)
	out.ExtrusionGeometries = make([]gfx.Geometry, len(f.ExtrusionGeometries))
	return out
}

func cloneCeiling(c *Ceiling) Ceiling {
	out := *c
	out.ScriptData = cloneScriptData(c.ScriptData)
	out.Geometry = gfx.Geometry{}
	out.VertexOffsets = append([]float32(nil), c.VertexOffsets...)
	out.Extrusions = cloneExtrusions(c.Extrusions)
	out.TerrainPoints = append([]TerrainPoint(nil), c.TerrainPoints...)
	out.CollisionPts = append([]mgl32.Vec2(nil), c.CollisionPts...)
	out.CollisionDepths = append([]float32(nil), c.CollisionDepths...)
	out.CollisionTris = append([]int(nil), c.CollisionTris...)
	out.ExtrusionGeometries = make([]gfx.Geometry, len(c.ExtrusionGeometries))
	return out
}

func cloneExtrusions(src []Extrusion) []Extrusion {
	if src == nil {
		return nil
	}
	out := make([]Extrusion, len(src))
	for i := range src {
		out[i] = src[i]
		out[i].Outer = append([]ExtrusionPoint(nil), src[i].Outer...)
		out[i].Inner = append([]ExtrusionPoint(nil), src[i].Inner...)
	}
	return out
}

// RecomputeBounds re-derives the chunk's world-space AABB (used for
// frustum-culling the whole chunk before descending into per-sector culling)
// from its current sectors and water surfaces. Sector.Load keeps each sector's own
// bounds accurate, but nothing else rolls that up to the chunk level, so any
// editor operation that adds/removes/moves a sector without going through
// InferPortalsAndReload leaves MinBound/MaxBound stale — geometry added
// outside the original bounds then gets frustum-culled along with the rest of
// the chunk even when it's on screen.
func (ch *Chunk) RecomputeBounds() {
	if len(ch.Sectors) == 0 && len(ch.WaterSurfaces) == 0 {
		return
	}
	first := true
	extend := func(min, max mgl32.Vec3) {
		if first {
			ch.MinBound = min
			ch.MaxBound = max
			first = false
			return
		}
		for i := 0; i < 3; i++ {
			if min[i] < ch.MinBound[i] {
				ch.MinBound[i] = min[i]
			}
			if max[i] > ch.MaxBound[i] {
				ch.MaxBound[i] = max[i]
			}
		}
	}

	for si := range ch.Sectors {
		extend(ch.Sectors[si].WorldAABB())
	}

	for wi := range ch.WaterSurfaces {
		extend(ch.WaterSurfaces[wi].WorldAABB())
	}
}

// InferPortals detects edges shared between two sectors in this chunk and
// converts wall edges into portal connections. An edge is "shared" when sector
// A has edge (P→Q) and sector B has edge (Q→P) — i.e. the same two chunk
// points in opposite winding order. Existing portals are preserved and have
// their ToSector updated; wall edges that match a reverse edge in another
// sector are promoted to portals with DrawGap enabled.
func (ch *Chunk) InferPortals() {
	type edgeKey struct{ a, b int }

	// Map each directed edge to its owning sector index.
	edgeOwner := map[edgeKey]int{}
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		n := len(s.PointIndices)
		for ei := 0; ei < n; ei++ {
			a := s.PointIndices[ei]
			b := s.PointIndices[(ei+1)%n]
			edgeOwner[edgeKey{a, b}] = si
		}
	}

	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		n := len(s.PointIndices)
		ensureEdgeIDs(s)

		var newWalls []Wall
		var newPortals []Portal
		newEdgeKinds := make([]string, n)
		newEdgeIDs := make([]string, n)

		wallIdx, portalIdx := 0, 0
		for ei := 0; ei < n; ei++ {
			a := s.PointIndices[ei]
			b := s.PointIndices[(ei+1)%n]

			kind := "wall"
			if ei < len(s.EdgeKinds) {
				kind = s.EdgeKinds[ei]
			}
			// The edge's ID is the one thing that survives whatever this loop
			// decides the edge should become.
			edgeID := s.EdgeIDs[ei]
			newEdgeIDs[ei] = edgeID

			revOwner, hasReverse := edgeOwner[edgeKey{b, a}]
			isShared := hasReverse && revOwner != si

			if kind == "wall" && wallIdx < len(s.Walls) {
				if isShared && !s.Walls[wallIdx].Pinned {
					// Promote wall → portal. The ID carries across: this is the
					// same edge, and a script addressing it must not lose it
					// because the engine changed the edge's kind underneath.
					w := s.Walls[wallIdx]
					newEdgeKinds[ei] = "portal"
					newPortals = append(newPortals, Portal{
						ID:          edgeID,
						StartPoint:  w.StartPoint,
						EndPoint:    w.EndPoint,
						ToSector:    ch.Sectors[revOwner].ID,
						DrawGap:     true,
						Visible:     w.Visible,
						DoubleSided: w.DoubleSided,
					})
				} else {
					w := s.Walls[wallIdx]
					w.ID = edgeID
					newEdgeKinds[ei] = "wall"
					newWalls = append(newWalls, w)
				}
				wallIdx++
			} else if kind == "portal" && portalIdx < len(s.Portals) {
				p := s.Portals[portalIdx]
				p.ID = edgeID
				if isShared {
					// Update target in case sector IDs shifted
					p.ToSector = ch.Sectors[revOwner].ID
				}
				newEdgeKinds[ei] = "portal"
				newPortals = append(newPortals, p)
				portalIdx++
			} else {
				// Kind/record mismatch. Emit a blank wall rather than nothing:
				// dropping the edge would leave Walls one short of the ring and
				// silently shift every record after it, which is far worse than
				// an untextured wall the author can see and fix.
				log.Printf("InferPortals: sector %q edge %d (%q): kind %q but no matching wall/portal entry; substituting a blank wall",
					s.ID, ei, edgeID, kind)
				newEdgeKinds[ei] = "wall"
				newWalls = append(newWalls, Wall{
					ID:          edgeID,
					StartPoint:  ch.Points[a],
					EndPoint:    ch.Points[b],
					Collision:   true,
					Visible:     true,
					DoubleSided: true,
					Alpha:       1.0,
					UVScale:     mgl32.Vec2{1, 1},
				})
			}
		}

		s.EdgeKinds = newEdgeKinds
		s.EdgeIDs = newEdgeIDs
		s.Walls = newWalls
		s.Portals = newPortals
	}
}

// InferPortalsAndReload runs InferPortals, then reloads geometry for every
// sector and rebuilds gap walls. Use this after editor operations that add
// or modify sectors.
func (ch *Chunk) InferPortalsAndReload() {
	ch.InferPortals()
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		_ = reloadSector(ch, s)
	}
	ch.RebuildGaps()
	ch.RecomputeBounds()
}

// RebuildGaps regenerates portal gap geometry for all sectors in this chunk.
// Call after changing DrawGap, GapTexture, or floor/ceiling heights.
func (ch *Chunk) RebuildGaps() {
	sectorByID := map[string]*Sector{}
	for i := range ch.Sectors {
		sectorByID[ch.Sectors[i].ID] = &ch.Sectors[i]
	}
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		for pi := range s.Portals {
			p := &s.Portals[pi]
			// Clear existing gap geometry
			p.FloorGap.Vertices = nil
			p.CeilGap.Vertices = nil

			if !p.DrawGap {
				continue
			}
			adj, ok := sectorByID[p.ToSector]
			if !ok {
				continue
			}
			tex := p.GapTexture
			if tex == "" {
				tex = s.Floor.Texture
			}
			start := p.StartPoint
			end := p.EndPoint
			segLen := mgl32.Vec2{end.X() - start.X(), end.Y() - start.Y()}.Len()
			if segLen < 1e-6 {
				continue // degenerate zero-length edge
			}

			// Floor riser: only drawn from the lower-floor side.
			if s.Floor.Height < adj.Floor.Height {
				lo1, lo2 := edgeHeights(ch, s, s.Floor.VertexHeight, start, end)
				hi1, hi2 := edgeHeights(ch, adj, adj.Floor.VertexHeight, start, end)
				verts, indices := buildGapQuad(start, end, lo1, lo2, hi1, hi2, segLen)
				_ = p.FloorGap.Load(verts, indices, tex, mgl32.Vec3{})
			}

			// Ceiling filler: only drawn from the higher-ceiling side.
			if s.Ceiling.Enabled && adj.Ceiling.Enabled && s.Ceiling.Height > adj.Ceiling.Height {
				lo1, lo2 := edgeHeights(ch, adj, adj.Ceiling.VertexHeight, start, end)
				hi1, hi2 := edgeHeights(ch, s, s.Ceiling.VertexHeight, start, end)
				verts, indices := buildGapQuad(start, end, lo1, lo2, hi1, hi2, segLen)
				_ = p.CeilGap.Load(verts, indices, tex, mgl32.Vec3{})
			}
		}
	}
}

// buildGapQuad returns the vertices and indices for a vertical quad spanning
// from lo to hi between start and end (in XZ), with the given segment length.
// Pure sector/polygon geometry — moved here (from level.go) to sit beside its
// only caller, RebuildGaps, per RESTRUCTURE_PROPOSAL.md §5 Phase A.
func buildGapQuad(start, end mgl32.Vec2, lo1, lo2, hi1, hi2, segLen float32) ([][]float32, []int) {
	// Sloped surfaces can cross within the edge; collapse rather than invert
	// the quad so the face never winds backwards.
	if hi1 < lo1 {
		hi1 = lo1
	}
	if hi2 < lo2 {
		hi2 = lo2
	}
	a := []float32{start.X(), hi1, start.Y(), 0, 0}
	b := []float32{end.X(), hi2, end.Y(), segLen, 0}
	c := []float32{end.X(), lo2, end.Y(), segLen, hi2 - lo2}
	d := []float32{start.X(), lo1, start.Y(), 0, hi1 - lo1}
	return [][]float32{a, b, c, d}, []int{0, 1, 2, 2, 3, 0}
}

// edgeHeights returns a sector surface's height at the two endpoints of an edge,
// honoring per-vertex offsets. Endpoints that aren't sector vertices fall back
// to the surface's flat height.
func edgeHeights(ch *Chunk, s *Sector, vertexHeight func(int) float32, start, end mgl32.Vec2) (float32, float32) {
	pts := SectorPoints(ch, s)
	return vertexHeight(findVertexIdx(pts, start)), vertexHeight(findVertexIdx(pts, end))
}

// signedArea2D computes the signed area of a 2D polygon (shoelace formula).
// Positive = CCW winding (interior to the left); negative = CW.
func signedArea2D(pts []mgl32.Vec2) float32 {
	n := len(pts)
	area := float32(0)
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		area += pts[i].X()*pts[j].Y() - pts[j].X()*pts[i].Y()
	}
	return area / 2
}
