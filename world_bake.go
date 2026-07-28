package main

// world_bake.go — turns a WorldGrid into renderable/collidable Chunk geometry.
//
// BakeChunk builds the full chunk once (on "New World" and is also the model for
// regeneration). RebakeCells refreshes only the sectors touched by a brush
// stroke, so painting stays cheap on large grids.

import (
	"fmt"
	"log"

	"github.com/go-gl/mathgl/mgl32"
)

// worldCeilHeadroom is added above a cell's tallest corner when setting the
// (disabled) ceiling height. The cull AABB uses Ceiling.Height for its top, so
// this keeps raised terrain from being wrongly frustum-culled (risk fix #1).
const worldCeilHeadroom = float32(3.0)

// BakeChunk builds a Chunk from the grid: one quad Sector per cell, sharing a
// point pool of grid vertices so adjacent floors are seamless.
func (g *WorldGrid) BakeChunk() Chunk {
	chunk := Chunk{
		ID:        g.ChunkID,
		Scale:     1.0,
		WorldGrid: g,
	}

	// Point pool: one entry per grid vertex, in vIdx order.
	nV := (g.Cols + 1) * (g.Rows + 1)
	chunk.Points = make([]mgl32.Vec2, nV)
	for vy := 0; vy <= g.Rows; vy++ {
		for vx := 0; vx <= g.Cols; vx++ {
			chunk.Points[g.vIdx(vx, vy)] = g.vertexWorld(vx, vy)
		}
	}

	// One sector per cell, in row-major (cIdx) order so chunk.Sectors[cIdx]
	// stays 1:1 with cells for incremental RebakeCells.
	chunk.Sectors = make([]Sector, 0, g.Cols*g.Rows)
	for cy := 0; cy < g.Rows; cy++ {
		for cx := 0; cx < g.Cols; cx++ {
			chunk.Sectors = append(chunk.Sectors, g.makeCellSector(cx, cy))
		}
	}

	// Winding sanity check (risk fix #2): cell rings must be CCW or floors face
	// the wrong way and InferPortals' opposite-winding test fails.
	if len(chunk.Sectors) > 0 {
		if signedArea2D(SectorPoints(&chunk, &chunk.Sectors[0])) <= 0 {
			log.Printf("BakeChunk: cell winding is not CCW (signedArea<=0); check cellCorners order")
		}
	}

	// InferPortals runs for parity with the load path, but interior walls are
	// Pinned so nothing is promoted to a portal — terrain stays a flat grid of
	// wall-bounded floor sectors that frustum-cull independently. (Using portals
	// here would trigger indoor-style portal culling and blank out the world.)
	chunk.InferPortals()

	for si := range chunk.Sectors {
		s := &chunk.Sectors[si]
		if err := s.Load(SectorPoints(&chunk, s)); err != nil {
			log.Printf("BakeChunk: sector %q load: %v", s.ID, err)
		}
	}

	g.updateChunkBounds(&chunk)
	return chunk
}

// makeCellSector builds the Sector for cell (cx,cy) with floor height taken from
// the four corner heights. Height is stored as Floor.Height (min corner) plus
// per-vertex offsets, so the rendered/collided surface is continuous and the
// cull AABB (Floor.Height..Ceiling.Height) spans the real vertical range.
func (g *WorldGrid) makeCellSector(cx, cy int) Sector {
	corners := g.cellCorners(cx, cy)
	h := g.cellCornerHeights(cx, cy)
	minH, maxH := h[0], h[0]
	for _, v := range h[1:] {
		if v < minH {
			minH = v
		}
		if v > maxH {
			maxH = v
		}
	}
	ct := g.CellType[g.cIdx(cx, cy)]

	offsets := make([]float32, 4)
	for i := range offsets {
		offsets[i] = h[i] - minH
	}

	s := Sector{
		ID:           fmt.Sprintf("cell_%d_%d", cx, cy),
		PointIndices: []int{corners[0], corners[1], corners[2], corners[3]},
		EdgeKinds:    []string{"wall", "wall", "wall", "wall"},
		Floor: Floor{
			Enabled:       true,
			Height:        minH,
			VertexOffsets: offsets,
			Texture:       terrainTexture(ct),
			DoubleSided:   true,
			Alpha:         terrainAlpha(ct),
		},
		Ceiling: Ceiling{
			Enabled: false,
			Height:  maxH + worldCeilHeadroom,
		},
	}

	pts := [4]mgl32.Vec2{
		g.vertexWorld(cx, cy),
		g.vertexWorld(cx+1, cy),
		g.vertexWorld(cx+1, cy+1),
		g.vertexWorld(cx, cy+1),
	}
	// Perimeter edges (no neighbor cell) are solid visible barriers so the
	// player can't walk off the world (risk fix #6). Interior edges are
	// invisible, non-colliding, and Pinned so InferPortals never promotes them
	// to portals — terrain must use frustum-only culling, not portal culling.
	for i := 0; i < 4; i++ {
		boundary := false
		switch i {
		case 0:
			boundary = cy == 0
		case 1:
			boundary = cx == g.Cols-1
		case 2:
			boundary = cy == g.Rows-1
		case 3:
			boundary = cx == 0
		}
		s.Walls = append(s.Walls, Wall{
			StartPoint:  pts[i],
			EndPoint:    pts[(i+1)%4],
			Texture:     terrainTexture(ct),
			Collision:   boundary,
			Visible:     boundary,
			Pinned:      !boundary,
			DoubleSided: true,
			Alpha:       1.0,
		})
	}
	return s
}

// RebakeCells refreshes geometry for the given cell indices (cIdx values) after
// a brush edit. It re-derives floor height/texture from the current grid state
// and re-uploads only those sectors, freeing their old GPU buffers first
// (risk fix #3). Topology is unchanged, so InferPortals is not re-run.
func (g *WorldGrid) RebakeCells(chunk *Chunk, cells []int) {
	// Cell sectors may be interleaved with free-placed building sectors, so look
	// them up by ID rather than by slice index.
	idToSec := make(map[string]int, len(chunk.Sectors))
	for i := range chunk.Sectors {
		idToSec[chunk.Sectors[i].ID] = i
	}
	maxCell := g.Cols * g.Rows
	for _, ci := range cells {
		if ci < 0 || ci >= maxCell {
			continue
		}
		cx := ci % g.Cols
		cy := ci / g.Cols
		si, ok := idToSec[fmt.Sprintf("cell_%d_%d", cx, cy)]
		if !ok {
			continue
		}
		s := &chunk.Sectors[si]

		h := g.cellCornerHeights(cx, cy)
		minH, maxH := h[0], h[0]
		for _, v := range h[1:] {
			if v < minH {
				minH = v
			}
			if v > maxH {
				maxH = v
			}
		}
		ct := g.CellType[ci]

		s.Floor.Height = minH
		// Derive per-vertex offsets from the bilinear heightmap at each resolved
		// sector point. Bilinear (not nearest-vertex) so that a cell which gained
		// extra boundary vertices from the T-junction conforming pass (when a
		// neighbouring cell was subdivided for the city) still rebakes seamlessly.
		pts := SectorPoints(chunk, s)
		if len(s.Floor.VertexOffsets) < len(pts) {
			s.Floor.VertexOffsets = make([]float32, len(pts))
		}
		for i, p := range pts {
			s.Floor.VertexOffsets[i] = g.HeightAt(p.X(), p.Y()) - minH
		}
		s.Floor.Texture = terrainTexture(ct)
		s.Floor.Alpha = terrainAlpha(ct)
		s.Ceiling.Height = maxH + worldCeilHeadroom
		for wi := range s.Walls {
			s.Walls[wi].Texture = terrainTexture(ct)
		}

		s.Floor.Geometry.Delete()
		s.Ceiling.Geometry.Delete()
		for wi := range s.Walls {
			s.Walls[wi].Geometry.Delete()
		}

		if err := s.Load(SectorPoints(chunk, s)); err != nil {
			log.Printf("RebakeCells: sector %q load: %v", s.ID, err)
		}
	}
	g.updateChunkBounds(chunk)
}

// updateChunkBounds recomputes the chunk AABB from the point pool (XZ) and the
// heightmap (Y), with ceiling headroom on top so tall terrain isn't culled.
func (g *WorldGrid) updateChunkBounds(chunk *Chunk) {
	if len(chunk.Points) == 0 {
		return
	}
	minX, maxX := chunk.Points[0].X(), chunk.Points[0].X()
	minZ, maxZ := chunk.Points[0].Y(), chunk.Points[0].Y()
	for _, p := range chunk.Points {
		if p.X() < minX {
			minX = p.X()
		}
		if p.X() > maxX {
			maxX = p.X()
		}
		if p.Y() < minZ {
			minZ = p.Y()
		}
		if p.Y() > maxZ {
			maxZ = p.Y()
		}
	}
	minY, maxY := float32(0), float32(0)
	for i, hgt := range g.Heights {
		if i == 0 || hgt < minY {
			minY = hgt
		}
		if i == 0 || hgt > maxY {
			maxY = hgt
		}
	}
	chunk.MinBound = mgl32.Vec3{minX, minY, minZ}
	chunk.MaxBound = mgl32.Vec3{maxX, maxY + worldCeilHeadroom, maxZ}

	// Expand the Y extent to cover carved building roofs/parapets (their sectors
	// have an enabled ceiling well above the heightmap), so the chunk AABB is not
	// frustum-culled when only the rooftops are on screen.
	for si := range chunk.Sectors {
		s := &chunk.Sectors[si]
		if s.Ceiling.Enabled && s.Ceiling.Height > chunk.MaxBound[1] {
			chunk.MaxBound[1] = s.Ceiling.Height
		}
		if s.Floor.Height < chunk.MinBound[1] {
			chunk.MinBound[1] = s.Floor.Height
		}
	}

	// Expand for generated building primitives so tall buildings are not
	// frustum-culled after a later brush stroke recomputes bounds.
	for pi := range chunk.Primitives {
		p := &chunk.Primitives[pi]
		half := mgl32.Vec3{p.BoundHalfX, p.BoundHalfY, p.BoundHalfZ}
		lo := p.Position.Sub(half)
		hi := p.Position.Add(half)
		for i := 0; i < 3; i++ {
			if lo[i] < chunk.MinBound[i] {
				chunk.MinBound[i] = lo[i]
			}
			if hi[i] > chunk.MaxBound[i] {
				chunk.MaxBound[i] = hi[i]
			}
		}
	}
}
