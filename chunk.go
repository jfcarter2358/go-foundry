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
