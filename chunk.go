package main

import (
	"log"

	"github.com/go-gl/mathgl/mgl32"
)

// Chunk is a top-level spatial grouping of sectors.
// MinBound/MaxBound are 3D AABBs (Y-up) used for frustum culling.
// Origin, Rotation, and Scale are editor-set transforms baked into sector
// geometry at load time. PointNames preserves the original YAML point name →
// pre-transform coordinate mapping so SaveLevel can round-trip correctly.
type Chunk struct {
	ID         string
	Points     []mgl32.Vec2            // shared point pool; sectors reference by index
	Sectors    []Sector
	MinBound   mgl32.Vec3
	MaxBound   mgl32.Vec3
	Origin     mgl32.Vec3              // world-space offset applied to all points at load time
	Rotation   float32                 // Y-axis rotation in radians applied at load time
	Scale      float32                 // uniform XZ scale applied at load time (0 = 1.0)
	PointNames map[string]mgl32.Vec2   // original point name → pre-transform XZ coords
	Primitives []Primitive
	WorldGrid  *WorldGrid              // non-nil for procedurally-built world chunks
}

// Clone returns a fully-independent deep copy of the chunk. All slices and
// maps get fresh backing arrays so edits to the copy do not affect the source.
// GPU geometry handles are intentionally zeroed — the caller must re-upload
// sector/primitive/gap geometry before the clone can be rendered. Sector and
// primitive IDs are rewritten with idSuffix so they stay unique within the
// level; in-chunk portal ToSector references are remapped to the new IDs.
func (ch *Chunk) Clone(newID, idSuffix string) Chunk {
	cp := Chunk{
		ID:       newID,
		MinBound: ch.MinBound,
		MaxBound: ch.MaxBound,
		Origin:   ch.Origin,
		Rotation: ch.Rotation,
		Scale:    ch.Scale,
	}

	cp.Points = append([]mgl32.Vec2(nil), ch.Points...)

	if ch.PointNames != nil {
		cp.PointNames = make(map[string]mgl32.Vec2, len(ch.PointNames))
		for k, v := range ch.PointNames {
			cp.PointNames[k] = v
		}
	}

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

	cp.Primitives = make([]Primitive, len(ch.Primitives))
	for pi := range ch.Primitives {
		cp.Primitives[pi] = clonePrimitive(&ch.Primitives[pi], newID, idSuffix, sectorIDRemap)
	}

	if ch.WorldGrid != nil {
		g := *ch.WorldGrid
		g.ChunkID = newID
		g.Heights = append([]float32(nil), ch.WorldGrid.Heights...)
		g.CellType = append([]WorldTerrainType(nil), ch.WorldGrid.CellType...)
		g.CellCity = append([]int32(nil), ch.WorldGrid.CellCity...)
		g.Palette = append([]BuildingTemplate(nil), ch.WorldGrid.Palette...)
		g.Roads = make([]RoadStroke, len(ch.WorldGrid.Roads))
		for i, s := range ch.WorldGrid.Roads {
			g.Roads[i] = RoadStroke{Width: s.Width, Points: append([]mgl32.Vec2(nil), s.Points...)}
		}
		cp.WorldGrid = &g
	}

	return cp
}

func cloneSector(s *Sector, idSuffix string, sectorIDRemap map[string]string) Sector {
	out := Sector{
		ID:       s.ID + idSuffix,
		MinBound: s.MinBound,
		MaxBound: s.MaxBound,
	}
	out.PointIndices = append([]int(nil), s.PointIndices...)
	out.EdgeKinds = append([]string(nil), s.EdgeKinds...)
	out.PrimitiveIndices = append([]int(nil), s.PrimitiveIndices...)

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
	out.Geometry = Geometry{}
	out.Extrusions = cloneExtrusions(w.Extrusions)
	out.TerrainPoints = append([]TerrainPoint(nil), w.TerrainPoints...)
	return out
}

func clonePortal(p *Portal, sectorIDRemap map[string]string) Portal {
	out := *p
	out.FloorGap = Geometry{}
	out.CeilGap = Geometry{}
	if remapped, ok := sectorIDRemap[p.ToSector]; ok {
		out.ToSector = remapped
	}
	return out
}

func cloneFloor(f *Floor) Floor {
	out := *f
	out.Geometry = Geometry{}
	out.VertexOffsets = append([]float32(nil), f.VertexOffsets...)
	out.Extrusions = cloneExtrusions(f.Extrusions)
	out.TerrainPoints = append([]TerrainPoint(nil), f.TerrainPoints...)
	out.CollisionPts = append([]mgl32.Vec2(nil), f.CollisionPts...)
	out.CollisionDepths = append([]float32(nil), f.CollisionDepths...)
	out.CollisionTris = append([]int(nil), f.CollisionTris...)
	out.ExtrusionGeometries = make([]Geometry, len(f.ExtrusionGeometries))
	return out
}

func cloneCeiling(c *Ceiling) Ceiling {
	out := *c
	out.Geometry = Geometry{}
	out.VertexOffsets = append([]float32(nil), c.VertexOffsets...)
	out.Extrusions = cloneExtrusions(c.Extrusions)
	out.TerrainPoints = append([]TerrainPoint(nil), c.TerrainPoints...)
	out.CollisionPts = append([]mgl32.Vec2(nil), c.CollisionPts...)
	out.CollisionDepths = append([]float32(nil), c.CollisionDepths...)
	out.CollisionTris = append([]int(nil), c.CollisionTris...)
	out.ExtrusionGeometries = make([]Geometry, len(c.ExtrusionGeometries))
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

func clonePrimitive(p *Primitive, newChunkID, idSuffix string, sectorIDRemap map[string]string) Primitive {
	out := *p
	out.Geometry = Geometry{}
	out.ID = p.ID + idSuffix
	out.ChunkID = newChunkID
	if remapped, ok := sectorIDRemap[p.SectorID]; ok {
		out.SectorID = remapped
	}
	return out
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

		var newWalls []Wall
		var newPortals []Portal
		newEdgeKinds := make([]string, n)

		wallIdx, portalIdx := 0, 0
		for ei := 0; ei < n; ei++ {
			a := s.PointIndices[ei]
			b := s.PointIndices[(ei+1)%n]

			kind := "wall"
			if ei < len(s.EdgeKinds) {
				kind = s.EdgeKinds[ei]
			}

			revOwner, hasReverse := edgeOwner[edgeKey{b, a}]
			isShared := hasReverse && revOwner != si

			if kind == "wall" && wallIdx < len(s.Walls) {
				if isShared && !s.Walls[wallIdx].Pinned {
					// Promote wall → portal
					w := s.Walls[wallIdx]
					newEdgeKinds[ei] = "portal"
					newPortals = append(newPortals, Portal{
						StartPoint:  w.StartPoint,
						EndPoint:    w.EndPoint,
						ToSector:    ch.Sectors[revOwner].ID,
						DrawGap:     true,
						Visible:     w.Visible,
						DoubleSided: w.DoubleSided,
					})
				} else {
					newEdgeKinds[ei] = "wall"
					newWalls = append(newWalls, s.Walls[wallIdx])
				}
				wallIdx++
			} else if kind == "portal" && portalIdx < len(s.Portals) {
				p := s.Portals[portalIdx]
				if isShared {
					// Update target in case sector IDs shifted
					p.ToSector = ch.Sectors[revOwner].ID
				}
				newEdgeKinds[ei] = "portal"
				newPortals = append(newPortals, p)
				portalIdx++
			} else {
				// Fallback: edge kind/index mismatch — default to wall
				log.Printf("InferPortals: sector %q edge %d: kind %q but no matching wall/portal entry; defaulting to wall", s.ID, ei, kind)
				newEdgeKinds[ei] = "wall"
			}
		}

		s.EdgeKinds = newEdgeKinds
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
		_ = s.Load(SectorPoints(ch, s))
	}
	ch.RebuildGaps()
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
				lo, hi := s.Floor.Height, adj.Floor.Height
				gapH := hi - lo
				a := []float32{start.X(), hi, start.Y(), 0, 0}
				b := []float32{end.X(), hi, end.Y(), segLen, 0}
				c := []float32{end.X(), lo, end.Y(), segLen, gapH}
				d := []float32{start.X(), lo, start.Y(), 0, gapH}
				_ = p.FloorGap.Load(
					[][]float32{a, b, c, d}, []int{0, 1, 2, 2, 3, 0},
					tex, mgl32.Vec3{},
				)
			}

			// Ceiling filler: only drawn from the higher-ceiling side.
			if s.Ceiling.Enabled && adj.Ceiling.Enabled && s.Ceiling.Height > adj.Ceiling.Height {
				lo, hi := adj.Ceiling.Height, s.Ceiling.Height
				gapH := hi - lo
				a := []float32{start.X(), hi, start.Y(), 0, 0}
				b := []float32{end.X(), hi, end.Y(), segLen, 0}
				c := []float32{end.X(), lo, end.Y(), segLen, gapH}
				d := []float32{start.X(), lo, start.Y(), 0, gapH}
				_ = p.CeilGap.Load(
					[][]float32{a, b, c, d}, []int{0, 1, 2, 2, 3, 0},
					tex, mgl32.Vec3{},
				)
			}
		}
	}
}
