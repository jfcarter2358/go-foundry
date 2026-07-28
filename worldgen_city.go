package main

// worldgen_city.go — procedural city generation for the world builder.
//
// Everything is part of the terrain's cell-sector grid — NO overlay primitives.
// The city (rotated buildings + curved roads) is defined as off-grid 2D regions
// (worldgen_regions.go) and carved into the cells they overlap by constrained
// convex subdivision (world_subdivide.go). The carved sub-polygons ARE the
// terrain sectors:
//   - Roads are terrain triangles re-textured to the road surface, flush with the
//     ground (vertices sit on the heightmap), curved/diagonal because the
//     centerline is smoothed.
//   - Buildings are carved into the grid: an interior floor (flush on terrain) with
//     a raised flat roof, surrounded by a thick wall band (the ring between the
//     outer and inner footprint rectangles). The band rises ground→roof as real
//     sector geometry with solid, collidable wall faces and a parapet top, so the
//     walls have genuine thickness — no overlay boxes, no floating decks. A door
//     is cut into the road-facing band faces as a wall extrusion.
//
// Cells the city does not touch stay single quad sectors (terrain remains a grid).

import (
	"fmt"
	"log"
	"math"
	"math/rand"
	"path/filepath"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
)

const cityGenPrefix = "citygen_"
const roadGenPrefix = "roadgen_"

// BuildingStyle holds every theme-specific knob. Adding a new look is data, not
// new generator code: register another entry in BuildingStyles.
type BuildingStyle struct {
	Name           string
	Textures       []string  // wall/roof palette; one picked per building
	FloorTexture   string    // interior floor texture
	StoryHeight    float32   // world units per story
	StoryWeights   []float32 // weight of 1,2,3,... stories
	MaxLotCells    int       // max grid cells per building lot side
	EnterableFrac  float32   // fraction of buildings with a door
	EmptyLotChance float32   // fraction of eligible lots left empty (plazas)
}

// BuildingStyles is the registry of available styles. v1 ships only "desert".
var BuildingStyles = map[string]BuildingStyle{
	"desert": desertStyle(),
}

func desertStyle() BuildingStyle {
	return BuildingStyle{
		Name:           "desert",
		Textures:       []string{"stucco", "brick", "dirt"},
		FloorTexture:   "stone_floor",
		StoryHeight:    4.0,
		StoryWeights:   []float32{0.45, 0.40, 0.15},
		MaxLotCells:    3,
		EnterableFrac:  0.55,
		EmptyLotChance: 0.18,
	}
}

func resolveBuildingStyle(name string) BuildingStyle {
	if s, ok := BuildingStyles[name]; ok {
		return s
	}
	return BuildingStyles["desert"]
}

// sectorMeta carries the region tagging for one carved sub-polygon.
type sectorMeta struct {
	region   regionClass
	bld      *building
	road     bool
	cellType WorldTerrainType
	cx, cy   int
}

// GenerateCity rebuilds the chunk geometry from the grid: roads and buildings are
// carved into the cell grid as real terrain sectors. Deterministic from
// grid.Seed. Returns the set of textures used (so the caller can upload them).
func GenerateCity(ch *Chunk, grid *WorldGrid, style BuildingStyle) []string {
	clearGenerated(ch) // purge any legacy overlay primitives from older saves
	deleteChunkSectorGeometry(ch)

	used := map[string]bool{}
	road := buildRoadRibbon(grid)
	buildings := generateBuildings(grid, style, road, used)
	cc := &cityCarve{grid: grid, buildings: buildings, road: road}
	carveCity(ch, grid, cc, used)

	names := make([]string, 0, len(used))
	for n := range used {
		names = append(names, n)
	}
	return names
}

// BuildBuildingPreview carves a single template building onto a small flat grid
// and returns a standalone chunk for the building editor's 3D preview (geometry
// uploaded). The caller uploads the returned texture names.
func BuildBuildingPreview(t BuildingTemplate) (*Chunk, []string) {
	maxDim := t.Width
	if t.Depth > maxDim {
		maxDim = t.Depth
	}
	cell := float32(6)
	n := int(math.Ceil(float64((maxDim + 10) / cell)))
	if n < 3 {
		n = 3
	}
	origin := mgl32.Vec2{-float32(n) * cell / 2, -float32(n) * cell / 2}
	g := NewWorldGrid(n, n, cell, origin, 1, "__preview__")

	b := instantiateBuilding(t, mgl32.Vec2{0, 0}, mgl32.Vec2{1, 0}, mgl32.Vec2{0, 1}, g)
	setupDoor(b, nil) // no road → door defaults to the +uy (front, +Z) side
	cc := &cityCarve{grid: g, buildings: []*building{b}, road: nil}

	ch := &Chunk{ID: "__preview__", Scale: 1, WorldGrid: g}
	used := map[string]bool{}
	carveCity(ch, g, cc, used)

	names := make([]string, 0, len(used))
	for name := range used {
		names = append(names, name)
	}
	return ch, names
}

// deleteChunkSectorGeometry frees the GPU buffers of every sector in the chunk.
func deleteChunkSectorGeometry(ch *Chunk) {
	for si := range ch.Sectors {
		ch.Sectors[si].Floor.Geometry.Delete()
		ch.Sectors[si].Ceiling.Geometry.Delete()
		for gi := range ch.Sectors[si].Floor.ExtrusionGeometries {
			ch.Sectors[si].Floor.ExtrusionGeometries[gi].Delete()
		}
		for wi := range ch.Sectors[si].Walls {
			ch.Sectors[si].Walls[wi].Geometry.Delete()
			for gi := range ch.Sectors[si].Walls[wi].ExtrusionGeometries {
				ch.Sectors[si].Walls[wi].ExtrusionGeometries[gi].Delete()
			}
		}
	}
}

// carveCity subdivides every city-touched cell against the regions in cc, builds
// the tagged sectors on a shared de-duplicated point pool, uploads their geometry,
// and updates the chunk bounds. Textures used are added to `used`. Shared by
// GenerateCity and the building-editor preview.
func carveCity(ch *Chunk, grid *WorldGrid, cc *cityCarve, used map[string]bool) {
	buildings := cc.buildings
	road := cc.road

	pool := newPointPool()
	var polys []conformPoly
	var meta []sectorMeta

	for cy := 0; cy < grid.Rows; cy++ {
		for cx := 0; cx < grid.Cols; cx++ {
			ci := grid.cIdx(cx, cy)
			cmin, cmax := grid.cellWorldRect(cx, cy)

			var lines []line2
			for _, b := range buildings {
				bmin, bmax := b.aabb()
				if bmax.X() < cmin.X() || bmin.X() > cmax.X() || bmax.Y() < cmin.Y() || bmin.Y() > cmax.Y() {
					continue
				}
				lines = append(lines, b.cutLines()...)
			}
			lines = append(lines, road.cutLinesForCell(cmin, cmax)...)

			cellPoly := []mgl32.Vec2{
				grid.vertexWorld(cx, cy),
				grid.vertexWorld(cx+1, cy),
				grid.vertexWorld(cx+1, cy+1),
				grid.vertexWorld(cx, cy+1),
			}

			var pieces [][]mgl32.Vec2
			if len(lines) == 0 {
				pieces = [][]mgl32.Vec2{cellPoly}
			} else {
				pieces = subdivideConvex(cellPoly, lines)
			}

			for _, piece := range pieces {
				piece = cleanPoly(piece)
				if len(piece) < 3 || absf(polyArea2(piece)) < 1e-5 {
					continue
				}
				if polyArea2(piece) < 0 {
					reverseVec2Slice(piece)
				}
				cen := polyCentroid(piece)
				rc, b := cc.classify(cen)
				m := sectorMeta{region: rc, bld: b, cellType: grid.CellType[ci], cx: cx, cy: cy}
				if rc == regGround {
					m.road = cc.isRoad(cen)
				}
				idxs := make([]int, len(piece))
				for i, p := range piece {
					idxs[i] = pool.add(p)
				}
				polys = append(polys, conformPoly{idxs: idxs})
				meta = append(meta, m)
			}
		}
	}

	// Eliminate T-junctions between subdivided cells and their neighbours.
	conformTJunctions(polys, pool)

	ch.Points = pool.pts

	// Count pieces per cell so a wholly-untouched cell keeps the brush-editable
	// "cell_x_y" sector ID (RebakeCells only touches those).
	pieceCount := map[[2]int]int{}
	for i := range meta {
		pieceCount[[2]int{meta[i].cx, meta[i].cy}]++
	}

	ch.Sectors = make([]Sector, 0, len(polys))
	pieceIdx := map[[2]int]int{}
	for i := range polys {
		m := meta[i]
		key := [2]int{m.cx, m.cy}
		// A wholly-untouched plain-terrain cell keeps the brush-editable
		// "cell_x_y" ID (RebakeCells only refreshes those). A single-piece cell
		// that is interior/band/road must NOT be brush-revertible to terrain.
		var id string
		if pieceCount[key] == 1 && m.region == regGround && !m.road {
			id = fmt.Sprintf("cell_%d_%d", m.cx, m.cy)
		} else {
			id = fmt.Sprintf("%scell_%d_%d_%d", cityGenPrefix, m.cx, m.cy, pieceIdx[key])
		}
		pieceIdx[key]++
		ch.Sectors = append(ch.Sectors, buildCitySector(ch, grid, cc, polys[i].idxs, m, id))
	}

	// Collect textures actually referenced so the caller uploads them.
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		if s.Floor.Texture != "" {
			used[s.Floor.Texture] = true
		}
		if s.Ceiling.Enabled && s.Ceiling.Texture != "" {
			used[s.Ceiling.Texture] = true
		}
		for wi := range s.Walls {
			if s.Walls[wi].Texture != "" {
				used[s.Walls[wi].Texture] = true
			}
		}
	}

	ch.InferPortals() // no-op: every carved edge is a Pinned wall (no portals)
	for si := range ch.Sectors {
		if err := ch.Sectors[si].Load(SectorPoints(ch, &ch.Sectors[si])); err != nil {
			log.Printf("carveCity: sector %q load: %v", ch.Sectors[si].ID, err)
		}
	}
	ch.RebuildGaps()
	grid.updateChunkBounds(ch)
}

// groundTex picks the floor texture for a ground sub-sector: the road surface
// inside the ribbon, a dirt shoulder for the rest of a painted road cell, else
// the cell's terrain texture.
func groundTex(m sectorMeta) string {
	if m.road {
		return "stone_floor"
	}
	if m.cellType == TerrainRoad {
		return "dirt"
	}
	return terrainTexture(m.cellType)
}

// buildCitySector turns one carved convex polygon (pool indices, CCW) into a
// terrain sector: floor flush on the heightmap, region-appropriate ceiling, and
// per-edge walls classified by sampling the neighbouring region.
func buildCitySector(ch *Chunk, grid *WorldGrid, cc *cityCarve, idxs []int, m sectorMeta, id string) Sector {
	n := len(idxs)
	pts := make([]mgl32.Vec2, n)
	heights := make([]float32, n)
	minH := float32(math.MaxFloat32)
	maxH := -float32(math.MaxFloat32)
	for i, idx := range idxs {
		p := ch.Points[idx]
		pts[i] = p
		h := grid.HeightAt(p.X(), p.Y())
		heights[i] = h
		if h < minH {
			minH = h
		}
		if h > maxH {
			maxH = h
		}
	}
	offsets := make([]float32, n)
	for i := range offsets {
		offsets[i] = heights[i] - minH
	}

	s := Sector{
		ID:           id,
		PointIndices: append([]int(nil), idxs...),
		EdgeKinds:    make([]string, n),
	}
	for i := range s.EdgeKinds {
		s.EdgeKinds[i] = "wall"
	}

	var ceilY float32
	switch m.region {
	case regInterior:
		s.Floor = Floor{Enabled: true, Height: minH, VertexOffsets: offsets, Texture: m.bld.floorTex, DoubleSided: true, Alpha: 1}
		ceilY = m.bld.roofY
		s.Ceiling = Ceiling{Enabled: true, Height: ceilY, Texture: m.bld.roofTex, DoubleSided: true, Alpha: 1}
	case regBand:
		s.Floor = Floor{Enabled: true, Height: minH, VertexOffsets: offsets, Texture: m.bld.wallTex, DoubleSided: true, Alpha: 1}
		ceilY = m.bld.roofY + m.bld.parapet
		s.Ceiling = Ceiling{Enabled: true, Height: ceilY, Texture: m.bld.roofTex, DoubleSided: true, Alpha: 1}
	default: // ground
		ceilY = maxH + worldCeilHeadroom
		s.Floor = Floor{Enabled: true, Height: minH, VertexOffsets: offsets, Texture: groundTex(m), DoubleSided: true, Alpha: terrainAlpha(m.cellType)}
		s.Ceiling = Ceiling{Enabled: false, Height: ceilY}
	}

	const sampleEps = float32(0.04)
	gridMin := grid.Origin
	gridMax := mgl32.Vec2{grid.Origin.X() + float32(grid.Cols)*grid.CellSize, grid.Origin.Y() + float32(grid.Rows)*grid.CellSize}

	for e := 0; e < n; e++ {
		A := pts[e]
		B := pts[(e+1)%n]
		w := Wall{StartPoint: A, EndPoint: B, DoubleSided: true, Pinned: true, Alpha: 1, Texture: s.Floor.Texture}

		dir := B.Sub(A)
		dl := dir.Len()
		if dl < 1e-6 {
			s.Walls = append(s.Walls, w) // degenerate edge: hidden non-colliding
			continue
		}
		udir := dir.Mul(1 / dl)
		mid := A.Add(B).Mul(0.5)
		outN := mgl32.Vec2{udir.Y(), -udir.X()} // outward normal for CCW polygon
		sample := mid.Add(outN.Mul(sampleEps))

		if sample.X() < gridMin.X() || sample.X() > gridMax.X() || sample.Y() < gridMin.Y() || sample.Y() > gridMax.Y() {
			w.Visible = true // world perimeter barrier
			w.Collision = true
			s.Walls = append(s.Walls, w)
			continue
		}

		rcN, bN := cc.classify(sample)
		var dr doorRegion
		switch {
		case m.bld != nil && m.bld.door.open(mid):
			dr = m.bld.door
		case bN != nil && bN.door.open(mid):
			dr = bN.door
		}
		doorOpen := dr.valid

		switch m.region {
		case regBand:
			if rcN == regBand && bN == m.bld {
				// internal band edge — hidden, non-colliding
			} else {
				w.Visible = true
				w.Collision = true
				w.Texture = m.bld.wallTex
				if doorOpen {
					if u0, u1, ok := doorEdgeURange(dr, A, udir, dl); ok {
						openDoorHoleRange(&w, ceilY-(heights[e]+heights[(e+1)%n])*0.5, u0, u1)
					}
				}
			}
		case regInterior:
			if rcN == regInterior && bN == m.bld {
				// internal room edge — hidden, non-colliding
			} else if !doorOpen {
				w.Collision = true // invisible backup collider toward the band
			}
		default: // ground
			if (rcN == regBand || rcN == regInterior) && !doorOpen {
				w.Collision = true // invisible backup collider at a building exterior
			}
			// ground↔ground/road: hidden, non-colliding (free terrain travel)
		}
		s.Walls = append(s.Walls, w)
	}

	return s
}

// doorEdgeURange returns the [u0,u1] span along an edge (start A, unit dir, length
// wallLen) that lies within the doorway opening, or ok=false if the edge does not
// run along the doorway.
func doorEdgeURange(dr doorRegion, A, dir mgl32.Vec2, wallLen float32) (float32, float32, bool) {
	slope := dir.Dot(dr.tangent)
	if absf(slope) < 1e-3 {
		return 0, 0, false // edge perpendicular to the doorway
	}
	base := A.Sub(dr.center).Dot(dr.tangent)
	uA := (-dr.halfLen - base) / slope
	uB := (dr.halfLen - base) / slope
	if uA > uB {
		uA, uB = uB, uA
	}
	if uA < 0 {
		uA = 0
	}
	if uB > wallLen {
		uB = wallLen
	}
	if uB-uA < 0.1 {
		return 0, 0, false
	}
	return uA, uB, true
}

// openDoorHoleRange cuts a door-sized hole into a wall between u0 and u1, reaching
// from the floor up to ~2.4 of clearance (V is measured down from the ceiling).
func openDoorHoleRange(w *Wall, wallH, u0, u1 float32) {
	if wallH < 0.4 || u1-u0 < 0.1 {
		return
	}
	clear := float32(2.4)
	if clear > wallH-0.1 {
		clear = wallH - 0.1
	}
	vTop := wallH - clear
	if vTop < 0.05 {
		vTop = 0.05
	}
	vBot := wallH - 0.02
	pts := []ExtrusionPoint{{U: u0, V: vTop}, {U: u1, V: vTop}, {U: u1, V: vBot}, {U: u0, V: vBot}}
	w.Extrusions = []Extrusion{{Outer: pts, Inner: pts, Depth: 0, Visible: false, BackVisible: false, Alpha: 1.0}}
}

// clearGenerated removes any legacy overlay primitives (from older versions).
func clearGenerated(ch *Chunk) {
	kept := ch.Primitives[:0]
	for i := range ch.Primitives {
		id := ch.Primitives[i].ID
		if strings.HasPrefix(id, cityGenPrefix) || strings.HasPrefix(id, roadGenPrefix) {
			ch.Primitives[i].Geometry.Delete()
		} else {
			kept = append(kept, ch.Primitives[i])
		}
	}
	ch.Primitives = kept
}

// cityEligible reports whether a terrain type can host buildings.
func cityEligible(t WorldTerrainType) bool {
	return t == TerrainGeneral || t == TerrainHill
}

// cellIsRoad reports whether cell (cx,cy) exists and is a road.
func cellIsRoad(grid *WorldGrid, cx, cy int) bool {
	ci := grid.cIdx(cx, cy)
	return ci >= 0 && grid.CellType[ci] == TerrainRoad
}

// weightedStories draws a story count from the style's weights.
func weightedStories(r *rand.Rand, weights []float32) int {
	if len(weights) == 0 {
		return 1
	}
	total := float32(0)
	for _, w := range weights {
		total += w
	}
	x := r.Float32() * total
	for i, w := range weights {
		if x < w {
			return i + 1
		}
		x -= w
	}
	return len(weights)
}

// pickTexture chooses a palette entry at random.
func pickTexture(palette []string, r *rand.Rand) string {
	if len(palette) == 0 {
		return "stucco"
	}
	return palette[r.Intn(len(palette))]
}

// ensureTexturesLoaded uploads any named textures not already on the GPU.
func ensureTexturesLoaded(names []string) {
	for _, name := range names {
		if name == "" {
			continue
		}
		if _, ok := Textures[name]; ok {
			continue
		}
		path, ok := textureNames[name]
		if !ok {
			log.Printf("worldgen: no texture file for %q (drop a matching .png in textures/)", name)
			continue
		}
		if err := LoadTexture(name, filepath.Join(executablePath, path)); err != nil {
			log.Printf("worldgen: load texture %q: %v", name, err)
		}
	}
}
