package main

// world_grid.go — data model for the procedural world builder.
//
// A WorldGrid is the editable source-of-truth for a procedurally-built world:
// a regular grid of cells over the XZ plane with a per-vertex heightmap, a
// per-cell terrain type, and per-cell "city" region tags. The grid bakes into a
// Chunk of one quad Sector per cell (see world_bake.go) and drives procedural
// city generation (see worldgen_city.go).
//
// Coordinate conventions match the rest of the engine: world XZ, Y-up. Grid
// vertices are stored row-major; cell (cx,cy) spans vertices (cx,cy)→(cx+1,cy+1).

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// WorldTerrainType is the painted terrain category of a grid cell. It selects
// the cell floor texture (and, for rivers, translucency).
type WorldTerrainType int

const (
	TerrainGeneral WorldTerrainType = iota // default ground
	TerrainRoad                            // flattened road/street
	TerrainRiver                           // lowered water
	TerrainHill                            // raised ground
)

// BuildingTemplate is a user-designed, reusable building the city generator can
// stamp into the world. It is pure design data (footprint, height, textures,
// door) — placement (position/rotation) is decided at generation time. Templates
// live in a WorldGrid's Palette and are edited in the building editor (ModeBuilding).
type BuildingTemplate struct {
	Name        string
	Width       float32 // outer footprint size along the building's local X (world units)
	Depth       float32 // outer footprint size along the building's local Z
	Stories     int     // number of stories (roof height = Stories * StoryHeight)
	StoryHeight float32 // world units per story
	WallThick   float32 // wall band thickness
	Parapet     float32 // height the wall rises above the flat roof
	DoorWidth   float32 // doorway opening width
	Enterable   bool    // whether a door is cut (walk-in) — false = sealed
	WallTex     string  // wall texture
	RoofTex     string  // roof/ceiling texture
	FloorTex    string  // interior floor texture
	Weight      float32 // relative selection weight in the palette (>=0)
}

// defaultBuildingTemplate returns a sensible starting template for the editor.
func defaultBuildingTemplate() BuildingTemplate {
	return BuildingTemplate{
		Name:        "building",
		Width:       8,
		Depth:       8,
		Stories:     2,
		StoryHeight: 4,
		WallThick:   0.5,
		Parapet:     0.7,
		DoorWidth:   1.8,
		Enterable:   true,
		WallTex:     "stucco",
		RoofTex:     "dirt",
		FloorTex:    "stone_floor",
		Weight:      1,
	}
}

// RoadStroke is a free-hand road path painted in world XZ. Unlike the per-cell
// TerrainRoad tag (which is grid-aligned), a stroke follows the exact direction
// the user painted, so diagonal/curved strokes carve genuinely diagonal roads.
type RoadStroke struct {
	Points []mgl32.Vec2 // world XZ polyline, in paint order
	Width  float32      // road width (world units); 0 = grid default
}

// WorldGrid holds the editable state of a procedural world.
type WorldGrid struct {
	Cols, Rows int        // number of cells per axis
	CellSize   float32    // world units per cell edge
	Origin     mgl32.Vec2 // world XZ of vertex (0,0)
	Seed       int64      // master RNG seed for deterministic city generation

	// Heights holds one value per grid vertex, row-major.
	// len == (Cols+1)*(Rows+1); index = vy*(Cols+1)+vx.
	Heights []float32

	// CellType and CellCity hold one value per cell, row-major.
	// len == Cols*Rows; index = cy*Cols+cx.
	CellType []WorldTerrainType
	CellCity []int32 // 0 = none, >0 = city-region id

	// GrassDensity and RockDensity hold the painted coverage [0,1] of each
	// GPU-scattered biome, one value per cell, row-major (len == Cols*Rows).
	// These drive the procedural grass/rock scatter (see grass.go); they never
	// affect the baked terrain geometry or collision.
	GrassDensity []float32
	RockDensity  []float32

	// GrassRange and RockRange are the camera-centered draw radii (world units)
	// for each scattered biome. Blades/rocks only spawn within this radius of the
	// camera, so cost stays bounded regardless of world size. 0 = use default.
	GrassRange float32
	RockRange  float32

	// Appearance controls for the scattered biomes. GrassHeight/GrassWidth are the
	// blade height and base half-width in world units; RockSides is the equatorial
	// vertex count of the rock mesh (a bipyramid, so faces == 2*RockSides).
	// 0 = use default.
	GrassHeight float32
	GrassWidth  float32
	RockSides   int32

	ChunkID   string // terrain chunk this grid bakes into
	StyleName string // building style key (see BuildingStyles); "" = "desert"

	// City generation controls (see the building editor + World panel).
	Palette   []BuildingTemplate // designed buildings the generator picks from; empty = procedural
	Density   float32            // 0..1 target building density in city regions
	FaceRoads bool               // rotate road-adjacent buildings to face the nearest road

	// Free-hand road paths (true diagonals). When present they drive road carving;
	// otherwise the legacy per-cell TerrainRoad tags are used.
	Roads []RoadStroke
}

// Default camera-ring draw radii for the GPU-scattered biomes (world units).
const (
	defaultGrassRange = float32(45)
	defaultRockRange  = float32(90)
)

// Default appearance of the GPU-scattered biomes.
const (
	defaultGrassHeight = float32(0.55)
	defaultGrassWidth  = float32(0.045)
	defaultRockSides   = int32(4) // octahedron (8 faces)
)

// biomeDensityAt returns the grass and rock coverage of cell (cx,cy), or (0,0)
// if the cell is out of range or the arrays are unallocated (old saves).
func (g *WorldGrid) biomeDensityAt(cx, cy int) (grass, rock float32) {
	ci := g.cIdx(cx, cy)
	if ci < 0 {
		return 0, 0
	}
	if ci < len(g.GrassDensity) {
		grass = g.GrassDensity[ci]
	}
	if ci < len(g.RockDensity) {
		rock = g.RockDensity[ci]
	}
	return grass, rock
}

// NewWorldGrid allocates a flat grid (all heights 0, all cells TerrainGeneral).
func NewWorldGrid(cols, rows int, cellSize float32, origin mgl32.Vec2, seed int64, chunkID string) *WorldGrid {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	if cellSize <= 0 {
		cellSize = 4.0
	}
	g := &WorldGrid{
		Cols:      cols,
		Rows:      rows,
		CellSize:  cellSize,
		Origin:    origin,
		Seed:      seed,
		Heights:      make([]float32, (cols+1)*(rows+1)),
		CellType:     make([]WorldTerrainType, cols*rows),
		CellCity:     make([]int32, cols*rows),
		GrassDensity: make([]float32, cols*rows),
		RockDensity:  make([]float32, cols*rows),
		GrassRange:   defaultGrassRange,
		RockRange:    defaultRockRange,
		GrassHeight:  defaultGrassHeight,
		GrassWidth:   defaultGrassWidth,
		RockSides:    defaultRockSides,
		ChunkID:      chunkID,
		StyleName:    "desert",
		Density:      0.5,
		FaceRoads:    true,
	}
	return g
}

// vIdx returns the Heights/vertex index for grid vertex (vx,vy), or -1 if out of range.
func (g *WorldGrid) vIdx(vx, vy int) int {
	if vx < 0 || vy < 0 || vx > g.Cols || vy > g.Rows {
		return -1
	}
	return vy*(g.Cols+1) + vx
}

// cIdx returns the cell index for cell (cx,cy), or -1 if out of range.
func (g *WorldGrid) cIdx(cx, cy int) int {
	if cx < 0 || cy < 0 || cx >= g.Cols || cy >= g.Rows {
		return -1
	}
	return cy*g.Cols + cx
}

// vertexWorld returns the world XZ position of grid vertex (vx,vy).
func (g *WorldGrid) vertexWorld(vx, vy int) mgl32.Vec2 {
	return mgl32.Vec2{
		g.Origin.X() + float32(vx)*g.CellSize,
		g.Origin.Y() + float32(vy)*g.CellSize,
	}
}

// cellCorners returns the four vertex indices of cell (cx,cy) in CCW winding
// (matching createChunkAtCamera / the floor builder's expected order):
//
//	0:(cx,cy) 1:(cx+1,cy) 2:(cx+1,cy+1) 3:(cx,cy+1)
func (g *WorldGrid) cellCorners(cx, cy int) [4]int {
	return [4]int{
		g.vIdx(cx, cy),
		g.vIdx(cx+1, cy),
		g.vIdx(cx+1, cy+1),
		g.vIdx(cx, cy+1),
	}
}

// cellCornerHeights returns the heights of the four corners of cell (cx,cy)
// in the same order as cellCorners.
func (g *WorldGrid) cellCornerHeights(cx, cy int) [4]float32 {
	c := g.cellCorners(cx, cy)
	var h [4]float32
	for i, idx := range c {
		if idx >= 0 && idx < len(g.Heights) {
			h[i] = g.Heights[idx]
		}
	}
	return h
}

// worldToVertex returns the nearest grid vertex (vx,vy) to a world XZ point,
// clamped to the grid. Used to map a sector's resolved points back to grid
// vertices regardless of point-pool ordering (which a YAML round-trip changes).
func (g *WorldGrid) worldToVertex(p mgl32.Vec2) (int, int) {
	vx := int(math.Round(float64((p.X() - g.Origin.X()) / g.CellSize)))
	vy := int(math.Round(float64((p.Y() - g.Origin.Y()) / g.CellSize)))
	if vx < 0 {
		vx = 0
	}
	if vx > g.Cols {
		vx = g.Cols
	}
	if vy < 0 {
		vy = 0
	}
	if vy > g.Rows {
		vy = g.Rows
	}
	return vx, vy
}

// cellCenterWorld returns the world XZ position of the center of cell (cx,cy).
func (g *WorldGrid) cellCenterWorld(cx, cy int) mgl32.Vec2 {
	return mgl32.Vec2{
		g.Origin.X() + (float32(cx)+0.5)*g.CellSize,
		g.Origin.Y() + (float32(cy)+0.5)*g.CellSize,
	}
}

// cellWorldRect returns the world XZ min/max corners of cell (cx,cy).
func (g *WorldGrid) cellWorldRect(cx, cy int) (min, max mgl32.Vec2) {
	min = mgl32.Vec2{
		g.Origin.X() + float32(cx)*g.CellSize,
		g.Origin.Y() + float32(cy)*g.CellSize,
	}
	max = mgl32.Vec2{min.X() + g.CellSize, min.Y() + g.CellSize}
	return
}

// HeightAt returns the bilinearly-interpolated terrain height at world (wx,wz),
// clamped to the grid extent. Used to sit buildings on the terrain surface.
func (g *WorldGrid) HeightAt(wx, wz float32) float32 {
	gx := (wx - g.Origin.X()) / g.CellSize
	gz := (wz - g.Origin.Y()) / g.CellSize
	if gx < 0 {
		gx = 0
	}
	if gz < 0 {
		gz = 0
	}
	maxX := float32(g.Cols)
	maxZ := float32(g.Rows)
	if gx > maxX {
		gx = maxX
	}
	if gz > maxZ {
		gz = maxZ
	}
	cx := int(gx)
	cz := int(gz)
	if cx >= g.Cols {
		cx = g.Cols - 1
	}
	if cz >= g.Rows {
		cz = g.Rows - 1
	}
	fx := gx - float32(cx)
	fz := gz - float32(cz)

	h00 := g.Heights[g.vIdx(cx, cz)]
	h10 := g.Heights[g.vIdx(cx+1, cz)]
	h01 := g.Heights[g.vIdx(cx, cz+1)]
	h11 := g.Heights[g.vIdx(cx+1, cz+1)]

	top := h00*(1-fx) + h10*fx
	bot := h01*(1-fx) + h11*fx
	return top*(1-fz) + bot*fz
}

// terrainTexture maps a terrain type to a logical texture name. These fall back
// to textures that already ship with the project; drop dedicated desert PNGs
// into resources/textures (e.g. sand/road/water) and remap here to upgrade.
func terrainTexture(t WorldTerrainType) string {
	switch t {
	case TerrainRoad:
		return "stone_floor"
	case TerrainRiver:
		return "concrete"
	case TerrainHill:
		return "dirt"
	default:
		return "dirt"
	}
}

// terrainAlpha returns the floor alpha for a terrain type (rivers are translucent).
func terrainAlpha(t WorldTerrainType) float32 {
	if t == TerrainRiver {
		return 0.6
	}
	return 1.0
}
