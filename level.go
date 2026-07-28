package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// ---------- Raw YAML structs ----------

// RawExtrusionPoint is the YAML representation of a point in surface-local space.
type RawExtrusionPoint struct {
	U float32 `yaml:"u"`
	V float32 `yaml:"v"`
}

// RawExtrusion is the YAML representation of a surface extrusion.
type RawExtrusion struct {
	Outer       []RawExtrusionPoint `yaml:"outer"`
	Inner       []RawExtrusionPoint `yaml:"inner"`
	Depth       float32             `yaml:"depth"`
	Texture     string              `yaml:"texture,omitempty"`
	Visible     *bool               `yaml:"visible,omitempty"`      // default true when absent
	BackVisible *bool               `yaml:"back_visible,omitempty"` // default true when absent
	UVOffset    Point               `yaml:"uv_offset,omitempty"`
	UVScale     Point               `yaml:"uv_scale,omitempty"`
	UVRotation  float32             `yaml:"uv_rotation,omitempty"`
	Alpha       *float32            `yaml:"alpha,omitempty"` // default 1.0 when absent
}

// RawTerrainPoint is the YAML representation of a terrain displacement point.
type RawTerrainPoint struct {
	U     float32 `yaml:"u"`
	V     float32 `yaml:"v"`
	Depth float32 `yaml:"depth"`
}

// RawEdge describes one edge (wall or portal) in a sector's edge list.
type RawEdge struct {
	From       string `yaml:"from"`
	To         string `yaml:"to"`
	Kind       string `yaml:"kind"`
	Texture    string `yaml:"texture"`
	Collision  bool   `yaml:"collision"`
	Hidden     bool   `yaml:"hidden"`     // true = wall/portal not rendered (default false = visible)
	Pinned     bool   `yaml:"pinned,omitempty"` // true = wall stays a wall even if shared with another sector
	ToSector   string `yaml:"to_sector"`
	DrawGap     bool   `yaml:"draw_gap"`
	GapTexture  string `yaml:"gap_texture"`
	DoubleSided   *bool             `yaml:"double_sided,omitempty"` // default true when absent
	Alpha         *float32          `yaml:"alpha,omitempty"`        // nil = opaque (1.0)
	Extrusions    []RawExtrusion   `yaml:"extrusions,omitempty"`
	WallTerrain   bool             `yaml:"terrain,omitempty"`
	TerrainPoints []RawTerrainPoint `yaml:"terrain_points,omitempty"`
	UVOffset      Point             `yaml:"uv_offset,omitempty"`
	UVScale       Point             `yaml:"uv_scale,omitempty"`
	UVRotation    float32           `yaml:"uv_rotation,omitempty"`
}

// RawFloor is the YAML representation of a sector floor.
type RawFloor struct {
	Enabled       bool              `yaml:"enabled"`
	Height        float32           `yaml:"height"`
	Texture       string            `yaml:"texture"`
	Terrain       bool              `yaml:"terrain"`
	VertexOffsets []float32         `yaml:"vertex_offsets,omitempty"`
	DoubleSided   *bool             `yaml:"double_sided,omitempty"` // default true when absent
	Alpha         *float32          `yaml:"alpha,omitempty"`        // nil = opaque (1.0)
	Extrusions    []RawExtrusion    `yaml:"extrusions,omitempty"`
	TerrainPoints []RawTerrainPoint `yaml:"terrain_points,omitempty"`
	UVOffset      Point             `yaml:"uv_offset,omitempty"`
	UVScale       Point             `yaml:"uv_scale,omitempty"`
	UVRotation    float32           `yaml:"uv_rotation,omitempty"`
}

// RawCeiling is the YAML representation of a sector ceiling.
type RawCeiling struct {
	Enabled       bool              `yaml:"enabled"`
	Height        float32           `yaml:"height"`
	Texture       string            `yaml:"texture"`
	Terrain       bool              `yaml:"terrain,omitempty"`
	VertexOffsets []float32         `yaml:"vertex_offsets,omitempty"`
	DoubleSided   *bool             `yaml:"double_sided,omitempty"` // default true when absent
	Alpha         *float32          `yaml:"alpha,omitempty"`        // nil = opaque (1.0)
	Extrusions    []RawExtrusion    `yaml:"extrusions,omitempty"`
	TerrainPoints []RawTerrainPoint `yaml:"terrain_points,omitempty"`
	UVOffset      Point             `yaml:"uv_offset,omitempty"`
	UVScale       Point             `yaml:"uv_scale,omitempty"`
	UVRotation    float32           `yaml:"uv_rotation,omitempty"`
}

// RawSector is the YAML representation of one sector.
type RawSector struct {
	ID      string     `yaml:"id"`
	Floor   RawFloor   `yaml:"floor"`
	Ceiling RawCeiling `yaml:"ceiling"`
	Edges   []RawEdge  `yaml:"edges"`
}

// RawPrimitive is the YAML representation of a procedural 3D primitive.
type RawPrimitive struct {
	ID        string            `yaml:"id"`
	Type      string            `yaml:"type"`                // "rect_prism", "sphere", "cylinder", "plane"
	Position  RawEntityPosition `yaml:"position"`
	Rotation  RawEntityPosition `yaml:"rotation"`
	Scale     RawEntityPosition `yaml:"scale"`
	Texture   string            `yaml:"texture"`
	Sides     int               `yaml:"sides,omitempty"`     // cylinder only; default 16
	Collision bool              `yaml:"collision,omitempty"`
	Alpha      *float32          `yaml:"alpha,omitempty"`        // nil = opaque (1.0)
	UVOffset   Point             `yaml:"uv_offset,omitempty"`
	UVScale    Point             `yaml:"uv_scale,omitempty"`  // zero = {1,1}
	UVRotation float32           `yaml:"uv_rotation,omitempty"` // radians
}

// RawChunkOrigin is the YAML representation of a chunk's world-space origin offset.
type RawChunkOrigin struct {
	X float32 `yaml:"x"`
	Y float32 `yaml:"y"`
	Z float32 `yaml:"z"`
}

// RawChunk is the YAML representation of one chunk.
// Points use {x, y} which map to world (X, Z) — Y-up convention.
// Origin, Rotation, and Scale are optional editor-set transforms baked in at load time.
type RawChunk struct {
	ID       string            `yaml:"id"`
	Points   map[string]Point  `yaml:"points"`
	Sectors  []RawSector       `yaml:"sectors"`
	Origin   RawChunkOrigin    `yaml:"origin"`
	Rotation float32           `yaml:"rotation"`
	Scale      float32           `yaml:"scale"`
	Primitives []RawPrimitive    `yaml:"primitives,omitempty"`
	WorldGrid  *RawWorldGrid     `yaml:"world_grid,omitempty"`
}

// RawWorldGrid is the YAML representation of a procedural world grid. When
// present on a chunk it makes the chunk re-editable in the world builder; the
// chunk's sectors/primitives are still stored normally so manual tweaks persist.
type RawWorldGrid struct {
	Cols      int       `yaml:"cols"`
	Rows      int       `yaml:"rows"`
	CellSize  float32   `yaml:"cell_size"`
	Origin    Point     `yaml:"origin"`
	Seed      int64     `yaml:"seed"`
	StyleName string    `yaml:"style_name,omitempty"`
	Heights   []float32 `yaml:"heights"`
	CellType  []int     `yaml:"cell_type"`
	CellCity  []int32   `yaml:"cell_city"`

	// GPU-scattered biome coverage (grass / rock), one value per cell, and the
	// per-biome camera-ring draw radii. Omitted on worlds with no painted biomes.
	GrassDensity []float32 `yaml:"grass_density,omitempty"`
	RockDensity  []float32 `yaml:"rock_density,omitempty"`
	GrassRange   float32   `yaml:"grass_range,omitempty"`
	RockRange    float32   `yaml:"rock_range,omitempty"`
	GrassHeight  float32   `yaml:"grass_height,omitempty"`
	GrassWidth   float32   `yaml:"grass_width,omitempty"`
	RockSides    int32     `yaml:"rock_sides,omitempty"`

	// City generation controls.
	Palette   []RawBuildingTemplate `yaml:"palette,omitempty"`
	Density   float32               `yaml:"density,omitempty"`
	FaceRoads *bool                 `yaml:"face_roads,omitempty"` // pointer so absent == default(true)
	Roads     []RawRoadStroke       `yaml:"roads,omitempty"`      // free-hand road paths
}

// RawRoadStroke is the YAML form of a RoadStroke (world-XZ polyline + width).
type RawRoadStroke struct {
	Points []Point `yaml:"points"`
	Width  float32 `yaml:"width,omitempty"`
}

// RawBuildingTemplate is the YAML form of a BuildingTemplate.
type RawBuildingTemplate struct {
	Name        string  `yaml:"name"`
	Width       float32 `yaml:"width"`
	Depth       float32 `yaml:"depth"`
	Stories     int     `yaml:"stories"`
	StoryHeight float32 `yaml:"story_height"`
	WallThick   float32 `yaml:"wall_thick"`
	Parapet     float32 `yaml:"parapet"`
	DoorWidth   float32 `yaml:"door_width"`
	Enterable   bool    `yaml:"enterable"`
	WallTex     string  `yaml:"wall_tex"`
	RoofTex     string  `yaml:"roof_tex"`
	FloorTex    string  `yaml:"floor_tex"`
	Weight      float32 `yaml:"weight"`
}

// RawEntityPosition holds the 3D spawn position of an entity.
type RawEntityPosition struct {
	X float32 `yaml:"x"`
	Y float32 `yaml:"y"`
	Z float32 `yaml:"z"`
}

// RawEntity is the YAML representation of an entity.
type RawEntity struct {
	ID         string            `yaml:"id"`
	Position   RawEntityPosition `yaml:"position"`
	Rotation   RawEntityPosition `yaml:"rotation"` // Euler XYZ in radians
	Scale      RawEntityPosition `yaml:"scale"`    // per-axis scale; zero = {1,1,1}
	Model      string            `yaml:"model"`
	Texture    string            `yaml:"texture"`
	Bounding   string            `yaml:"bounding"`
	BoundHalfX float32           `yaml:"bound_half_x"`
	BoundHalfY float32           `yaml:"bound_half_y"`
	BoundHalfZ float32           `yaml:"bound_half_z"`
	Alpha      *float32          `yaml:"alpha,omitempty"` // nil = opaque (1.0)
}

// RawColor is the YAML representation of an RGB color.
type RawColor struct {
	R float32 `yaml:"r"`
	G float32 `yaml:"g"`
	B float32 `yaml:"b"`
}

// RawGlobalLight is the YAML representation of a global ambient light.
type RawGlobalLight struct {
	Color      RawColor `yaml:"color"`
	Brightness float32  `yaml:"brightness"`
}

// RawDirLight is the YAML representation of a directional light.
type RawDirLight struct {
	Direction struct {
		X float32 `yaml:"x"`
		Y float32 `yaml:"y"`
		Z float32 `yaml:"z"`
	} `yaml:"direction"`
	Color          RawColor `yaml:"color"`
	Brightness     float32  `yaml:"brightness"`
	CastShadows    bool     `yaml:"cast_shadows"`
	ShadowStrength float32  `yaml:"shadow_strength"`
}

// RawPointLight is the YAML representation of a point light.
type RawPointLight struct {
	Position struct {
		X float32 `yaml:"x"`
		Y float32 `yaml:"y"`
		Z float32 `yaml:"z"`
	} `yaml:"position"`
	Color          RawColor `yaml:"color"`
	Brightness     float32  `yaml:"brightness"`
	Radius         float32  `yaml:"radius"`
	CastShadows    bool     `yaml:"cast_shadows"`
	ShadowStrength float32  `yaml:"shadow_strength"`
}

// RawLights holds all light definitions from YAML.
type RawLights struct {
	Global      RawGlobalLight  `yaml:"global"`
	Directional []RawDirLight   `yaml:"directional"`
	Point       []RawPointLight `yaml:"point"`
}

// RawLevel is the top-level YAML document.
type RawLevel struct {
	Chunks   []RawChunk  `yaml:"chunks"`
	Entities []RawEntity `yaml:"entities"`
	Lights   RawLights   `yaml:"lights"`
}

// ---------- Runtime types ----------

// Level holds all loaded chunks, entities, and lights.
type Level struct {
	Chunks   []Chunk
	Entities []Entity
	Lights   LightSet
	Spatial  *SpatialHash // accelerated chunk/sector lookup; rebuilt after level load
}

// ---------- Loader ----------

// loadRawChunk converts a single RawChunk into a runtime Chunk, uploading all
// geometry to the GPU. It returns the chunk and the set of texture names needed.
func loadRawChunk(rc RawChunk) (Chunk, map[string]bool, error) {
	texNeeded := map[string]bool{}

	sc := float32(1.0)
	if rc.Scale != 0 {
		sc = rc.Scale
	}
	origin := mgl32.Vec3{rc.Origin.X, rc.Origin.Y, rc.Origin.Z}
	chunk := Chunk{
		ID:       rc.ID,
		Origin:   origin,
		Rotation: rc.Rotation,
		Scale:    sc,
	}

	chunk.PointNames = make(map[string]mgl32.Vec2, len(rc.Points))
	for name, rp := range rc.Points {
		chunk.PointNames[name] = mgl32.Vec2{rp.X, rp.Y}
	}

	cos32 := float32(math.Cos(float64(rc.Rotation)))
	sin32 := float32(math.Sin(float64(rc.Rotation)))
	transformedPoints := make(map[string]Point, len(rc.Points))
	for name, rp := range rc.Points {
		x := rp.X * sc
		y := rp.Y * sc
		tx := cos32*x - sin32*y + origin.X()
		tz := sin32*x + cos32*y + origin.Z()
		transformedPoints[name] = Point{X: tx, Y: tz}
	}

	pointNameToIdx := make(map[string]int, len(transformedPoints))
	for name, rp := range transformedPoints {
		pointNameToIdx[name] = len(chunk.Points)
		chunk.Points = append(chunk.Points, mgl32.Vec2{rp.X, rp.Y})
	}

	for i, pt := range chunk.Points {
		if i == 0 {
			chunk.MinBound = mgl32.Vec3{pt.X(), float32(math.MaxFloat32), pt.Y()}
			chunk.MaxBound = mgl32.Vec3{pt.X(), -float32(math.MaxFloat32), pt.Y()}
		} else {
			if pt.X() < chunk.MinBound.X() {
				chunk.MinBound[0] = pt.X()
			}
			if pt.Y() < chunk.MinBound.Z() {
				chunk.MinBound[2] = pt.Y()
			}
			if pt.X() > chunk.MaxBound.X() {
				chunk.MaxBound[0] = pt.X()
			}
			if pt.Y() > chunk.MaxBound.Z() {
				chunk.MaxBound[2] = pt.Y()
			}
		}
	}

	for _, rs := range rc.Sectors {
		loadExtrusions := func(raws []RawExtrusion, transformXZ bool) []Extrusion {
			var out []Extrusion
			for _, re := range raws {
				extUVScale := mgl32.Vec2{re.UVScale.X, re.UVScale.Y}
				if extUVScale == (mgl32.Vec2{}) {
					extUVScale = mgl32.Vec2{1, 1}
				}
				ext := Extrusion{
					Depth:       re.Depth,
					Texture:     re.Texture,
					Visible:     boolDefault(re.Visible, true),
					BackVisible: boolDefault(re.BackVisible, true),
					UVOffset:    mgl32.Vec2{re.UVOffset.X, re.UVOffset.Y},
					UVScale:     extUVScale,
					UVRotation:  re.UVRotation,
					Alpha:       float32Default(re.Alpha, 1.0),
				}
				if re.Texture != "" {
					texNeeded[re.Texture] = true
				}
				for _, rp := range re.Outer {
					if transformXZ {
						tp := transformPoint2D(mgl32.Vec2{rp.U, rp.V}, origin, rc.Rotation, sc)
						ext.Outer = append(ext.Outer, ExtrusionPoint{U: tp.X(), V: tp.Y()})
					} else {
						ext.Outer = append(ext.Outer, ExtrusionPoint{U: rp.U, V: rp.V})
					}
				}
				for _, rp := range re.Inner {
					if transformXZ {
						tp := transformPoint2D(mgl32.Vec2{rp.U, rp.V}, origin, rc.Rotation, sc)
						ext.Inner = append(ext.Inner, ExtrusionPoint{U: tp.X(), V: tp.Y()})
					} else {
						ext.Inner = append(ext.Inner, ExtrusionPoint{U: rp.U, V: rp.V})
					}
				}
				out = append(out, ext)
			}
			return out
		}
		loadTerrainPts := func(raws []RawTerrainPoint, transformXZ bool) []TerrainPoint {
			var out []TerrainPoint
			for _, rtp := range raws {
				if transformXZ {
					tp := transformPoint2D(mgl32.Vec2{rtp.U, rtp.V}, origin, rc.Rotation, sc)
					out = append(out, TerrainPoint{U: tp.X(), V: tp.Y(), Depth: rtp.Depth})
				} else {
					out = append(out, TerrainPoint{U: rtp.U, V: rtp.V, Depth: rtp.Depth})
				}
			}
			return out
		}

		floorUVScale := mgl32.Vec2{rs.Floor.UVScale.X, rs.Floor.UVScale.Y}
		if floorUVScale == (mgl32.Vec2{}) {
			floorUVScale = mgl32.Vec2{1, 1}
		}
		ceilUVScale := mgl32.Vec2{rs.Ceiling.UVScale.X, rs.Ceiling.UVScale.Y}
		if ceilUVScale == (mgl32.Vec2{}) {
			ceilUVScale = mgl32.Vec2{1, 1}
		}
		sector := Sector{
			ID: rs.ID,
			Floor: Floor{
				Enabled:       rs.Floor.Enabled,
				Height:        rs.Floor.Height,
				Texture:       rs.Floor.Texture,
				Terrain:       rs.Floor.Terrain,
				DoubleSided:   boolDefault(rs.Floor.DoubleSided, true),
				Alpha:         float32Default(rs.Floor.Alpha, 1.0),
				Extrusions:    loadExtrusions(rs.Floor.Extrusions, true),
				TerrainPoints: loadTerrainPts(rs.Floor.TerrainPoints, true),
				UVOffset:      mgl32.Vec2{rs.Floor.UVOffset.X, rs.Floor.UVOffset.Y},
				UVScale:       floorUVScale,
				UVRotation:    rs.Floor.UVRotation,
			},
			Ceiling: Ceiling{
				Enabled:       rs.Ceiling.Enabled,
				Height:        rs.Ceiling.Height,
				Texture:       rs.Ceiling.Texture,
				Terrain:       rs.Ceiling.Terrain,
				DoubleSided:   boolDefault(rs.Ceiling.DoubleSided, true),
				Alpha:         float32Default(rs.Ceiling.Alpha, 1.0),
				Extrusions:    loadExtrusions(rs.Ceiling.Extrusions, true),
				TerrainPoints: loadTerrainPts(rs.Ceiling.TerrainPoints, true),
				UVOffset:      mgl32.Vec2{rs.Ceiling.UVOffset.X, rs.Ceiling.UVOffset.Y},
				UVScale:       ceilUVScale,
				UVRotation:    rs.Ceiling.UVRotation,
			},
		}

		if rs.Floor.Texture != "" {
			texNeeded[rs.Floor.Texture] = true
		}
		if rs.Ceiling.Texture != "" {
			texNeeded[rs.Ceiling.Texture] = true
		}

		pointSeen := map[string]bool{}
		var pointOrder []string
		for _, e := range rs.Edges {
			if !pointSeen[e.From] {
				pointSeen[e.From] = true
				pointOrder = append(pointOrder, e.From)
			}
			if !pointSeen[e.To] {
				pointSeen[e.To] = true
				pointOrder = append(pointOrder, e.To)
			}
		}
		for _, name := range pointOrder {
			if idx, ok := pointNameToIdx[name]; ok {
				sector.PointIndices = append(sector.PointIndices, idx)
			}
		}

		for _, edge := range rs.Edges {
			fromIdx, okFrom := pointNameToIdx[edge.From]
			toIdx, okTo := pointNameToIdx[edge.To]
			if !okFrom || !okTo {
				return Chunk{}, nil, fmt.Errorf("sector %q: edge references unknown point: %s->%s",
					rs.ID, edge.From, edge.To)
			}
			start := chunk.Points[fromIdx]
			end := chunk.Points[toIdx]

			switch edge.Kind {
			case "wall":
				if edge.Texture != "" {
					texNeeded[edge.Texture] = true
				}
				wallUVScale := mgl32.Vec2{edge.UVScale.X, edge.UVScale.Y}
				if wallUVScale == (mgl32.Vec2{}) {
					wallUVScale = mgl32.Vec2{1, 1}
				}
				sector.Walls = append(sector.Walls, Wall{
					StartPoint:    start,
					EndPoint:      end,
					Texture:       edge.Texture,
					Collision:     edge.Collision,
					Visible:       !edge.Hidden,
					Pinned:        edge.Pinned,
					DoubleSided:   boolDefault(edge.DoubleSided, true),
					Terrain:       edge.WallTerrain,
					Alpha:         float32Default(edge.Alpha, 1.0),
					Extrusions:    loadExtrusions(edge.Extrusions, false),
					TerrainPoints: loadTerrainPts(edge.TerrainPoints, false),
					UVOffset:      mgl32.Vec2{edge.UVOffset.X, edge.UVOffset.Y},
					UVScale:       wallUVScale,
					UVRotation:    edge.UVRotation,
				})
				sector.EdgeKinds = append(sector.EdgeKinds, "wall")
			case "portal":
				sector.Portals = append(sector.Portals, Portal{
					StartPoint:  start,
					EndPoint:    end,
					ToSector:    edge.ToSector,
					DrawGap:     edge.DrawGap,
					GapTexture:  edge.GapTexture,
					Visible:     !edge.Hidden,
					DoubleSided: boolDefault(edge.DoubleSided, true),
					Alpha:       float32Default(edge.Alpha, 1.0),
				})
				sector.EdgeKinds = append(sector.EdgeKinds, "portal")
			}
		}

		sectorPts := SectorPoints(&chunk, &sector)

		reversed := signedArea2D(sectorPts) < 0
		if reversed {
			for i, j := 0, len(sector.PointIndices)-1; i < j; i, j = i+1, j-1 {
				sector.PointIndices[i], sector.PointIndices[j] = sector.PointIndices[j], sector.PointIndices[i]
			}
			for i := range sector.Walls {
				sector.Walls[i].StartPoint, sector.Walls[i].EndPoint =
					sector.Walls[i].EndPoint, sector.Walls[i].StartPoint
			}
			for i := range sector.Portals {
				sector.Portals[i].StartPoint, sector.Portals[i].EndPoint =
					sector.Portals[i].EndPoint, sector.Portals[i].StartPoint
			}
			for i, j := 0, len(sector.EdgeKinds)-1; i < j; i, j = i+1, j-1 {
				sector.EdgeKinds[i], sector.EdgeKinds[j] = sector.EdgeKinds[j], sector.EdgeKinds[i]
			}
			sectorPts = SectorPoints(&chunk, &sector)
		}

		nPts := len(sector.PointIndices)
		sector.Floor.VertexOffsets = make([]float32, nPts)
		for i := 0; i < nPts && i < len(rs.Floor.VertexOffsets); i++ {
			sector.Floor.VertexOffsets[i] = rs.Floor.VertexOffsets[i]
		}
		sector.Ceiling.VertexOffsets = make([]float32, nPts)
		for i := 0; i < nPts && i < len(rs.Ceiling.VertexOffsets); i++ {
			sector.Ceiling.VertexOffsets[i] = rs.Ceiling.VertexOffsets[i]
		}
		if reversed {
			for i, j := 0, len(sector.Floor.VertexOffsets)-1; i < j; i, j = i+1, j-1 {
				sector.Floor.VertexOffsets[i], sector.Floor.VertexOffsets[j] = sector.Floor.VertexOffsets[j], sector.Floor.VertexOffsets[i]
			}
			for i, j := 0, len(sector.Ceiling.VertexOffsets)-1; i < j; i, j = i+1, j-1 {
				sector.Ceiling.VertexOffsets[i], sector.Ceiling.VertexOffsets[j] = sector.Ceiling.VertexOffsets[j], sector.Ceiling.VertexOffsets[i]
			}
		}

		minFloor := sector.Floor.Height
		for _, off := range rs.Floor.VertexOffsets {
			if h := sector.Floor.Height + off; h < minFloor {
				minFloor = h
			}
		}
		for _, tp := range sector.Floor.TerrainPoints {
			if h := sector.Floor.Height + tp.Depth; h < minFloor {
				minFloor = h
			}
		}
		for _, ext := range sector.Floor.Extrusions {
			if h := sector.Floor.Height - ext.Depth; h < minFloor {
				minFloor = h
			}
		}
		maxCeil := sector.Ceiling.Height
		for _, off := range rs.Ceiling.VertexOffsets {
			if h := sector.Ceiling.Height + off; h > maxCeil {
				maxCeil = h
			}
		}
		for _, tp := range sector.Ceiling.TerrainPoints {
			if h := sector.Ceiling.Height + tp.Depth; h > maxCeil {
				maxCeil = h
			}
		}
		for _, ext := range sector.Ceiling.Extrusions {
			if h := sector.Ceiling.Height + ext.Depth; h > maxCeil {
				maxCeil = h
			}
		}
		if minFloor < chunk.MinBound.Y() {
			chunk.MinBound[1] = minFloor
		}
		if maxCeil > chunk.MaxBound.Y() {
			chunk.MaxBound[1] = maxCeil
		}

		chunk.Sectors = append(chunk.Sectors, sector)
	}

	chunk.InferPortals()

	for si := range chunk.Sectors {
		s := &chunk.Sectors[si]
		sectorPts := SectorPoints(&chunk, s)
		if err := s.Load(sectorPts); err != nil {
			return Chunk{}, nil, fmt.Errorf("sector %q load: %w", s.ID, err)
		}
	}

	sectorByID := map[string]*Sector{}
	for i := range chunk.Sectors {
		sectorByID[chunk.Sectors[i].ID] = &chunk.Sectors[i]
	}
	for si := range chunk.Sectors {
		s := &chunk.Sectors[si]
		for pi := range s.Portals {
			p := &s.Portals[pi]
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
			if tex != "" {
				texNeeded[tex] = true
			}
			start := p.StartPoint
			end := p.EndPoint
			edgeVec := mgl32.Vec2{end.X() - start.X(), end.Y() - start.Y()}
			segLen := edgeVec.Len()
			if segLen < 1e-6 {
				continue
			}

			if s.Floor.Height < adj.Floor.Height {
				verts, indices := buildGapQuad(start, end, s.Floor.Height, adj.Floor.Height, segLen)
				if err := p.FloorGap.Load(verts, indices, tex, mgl32.Vec3{}); err != nil {
					return Chunk{}, nil, fmt.Errorf("portal floor gap %q→%q: %w", s.ID, p.ToSector, err)
				}
			}

			if s.Ceiling.Enabled && adj.Ceiling.Enabled && s.Ceiling.Height > adj.Ceiling.Height {
				verts, indices := buildGapQuad(start, end, adj.Ceiling.Height, s.Ceiling.Height, segLen)
				if err := p.CeilGap.Load(verts, indices, tex, mgl32.Vec3{}); err != nil {
					return Chunk{}, nil, fmt.Errorf("portal ceil gap %q→%q: %w", s.ID, p.ToSector, err)
				}
			}
		}
	}

	for _, rp := range rc.Primitives {
		scale := mgl32.Vec3{rp.Scale.X, rp.Scale.Y, rp.Scale.Z}
		if scale == (mgl32.Vec3{}) {
			scale = mgl32.Vec3{1, 1, 1}
		}

		px := rp.Position.X * sc
		pz := rp.Position.Z * sc
		worldX := cos32*px - sin32*pz + origin.X()
		worldZ := sin32*px + cos32*pz + origin.Z()
		worldY := rp.Position.Y + origin.Y()

		var kind PrimitiveType
		switch rp.Type {
		case "sphere":
			kind = PrimitiveSphere
		case "cylinder":
			kind = PrimitiveCylinder
		case "plane":
			kind = PrimitivePlane
		default:
			kind = PrimitiveRectPrism
		}

		sides := rp.Sides
		if sides < 3 {
			sides = 16
		}

		uvScale := mgl32.Vec2{rp.UVScale.X, rp.UVScale.Y}
		if uvScale == (mgl32.Vec2{}) {
			uvScale = mgl32.Vec2{1, 1}
		}

		prim := Primitive{
			ID:         rp.ID,
			Kind:       kind,
			Position:   mgl32.Vec3{worldX, worldY, worldZ},
			Rotation:   mgl32.Vec3{rp.Rotation.X, rp.Rotation.Y, rp.Rotation.Z},
			Scale:      scale,
			Texture:    rp.Texture,
			Sides:      sides,
			Collision:  rp.Collision,
			Alpha:      float32Default(rp.Alpha, 1.0),
			UVOffset:   mgl32.Vec2{rp.UVOffset.X, rp.UVOffset.Y},
			UVScale:    uvScale,
			UVRotation: rp.UVRotation,
			ChunkID:    chunk.ID,
		}

		switch kind {
		case PrimitiveRectPrism:
			prim.BoundingKind = BoundingAABB
			prim.BoundHalfX = scale.X() / 2
			prim.BoundHalfY = scale.Y() / 2
			prim.BoundHalfZ = scale.Z() / 2
		case PrimitiveSphere:
			prim.BoundingKind = BoundingSphere
			maxS := scale.X()
			if scale.Y() > maxS {
				maxS = scale.Y()
			}
			if scale.Z() > maxS {
				maxS = scale.Z()
			}
			prim.BoundHalfX = maxS / 2
			prim.BoundHalfY = maxS / 2
			prim.BoundHalfZ = maxS / 2
		case PrimitiveCylinder:
			prim.BoundingKind = BoundingCylinder
			maxXZ := scale.X()
			if scale.Z() > maxXZ {
				maxXZ = scale.Z()
			}
			prim.BoundHalfX = maxXZ / 2
			prim.BoundHalfY = scale.Y() / 2
			prim.BoundHalfZ = maxXZ / 2
		case PrimitivePlane:
			prim.BoundingKind = BoundingAABB
			prim.BoundHalfX = scale.X() / 2
			prim.BoundHalfY = 0.01
			prim.BoundHalfZ = scale.Z() / 2
		}

		if err := prim.Load(); err != nil {
			return Chunk{}, nil, fmt.Errorf("primitive %q load: %w", rp.ID, err)
		}

		if rp.Texture != "" {
			texNeeded[rp.Texture] = true
		}

		chunk.Primitives = append(chunk.Primitives, prim)
	}

	for pi := range chunk.Primitives {
		p := &chunk.Primitives[pi]
		xz := mgl32.Vec2{p.Position.X(), p.Position.Z()}
		for si := range chunk.Sectors {
			s := &chunk.Sectors[si]
			pts := SectorPoints(&chunk, s)
			if pointInPolygon(xz, pts) {
				p.SectorID = s.ID
				s.PrimitiveIndices = append(s.PrimitiveIndices, pi)
				break
			}
		}
	}

	// Attach the editable world grid (if any) for continued brush editing. The
	// full geometry was loaded above, so manual edits persist verbatim.
	if rc.WorldGrid != nil {
		chunk.WorldGrid = worldGridFromRaw(rc.WorldGrid, chunk.ID)
	}

	return chunk, texNeeded, nil
}

// worldGridFromRaw reconstructs a WorldGrid from its serialised form.
func worldGridFromRaw(rw *RawWorldGrid, chunkID string) *WorldGrid {
	g := &WorldGrid{
		Cols:      rw.Cols,
		Rows:      rw.Rows,
		CellSize:  rw.CellSize,
		Origin:    mgl32.Vec2{rw.Origin.X, rw.Origin.Y},
		Seed:      rw.Seed,
		Heights:   append([]float32(nil), rw.Heights...),
		CellCity:  append([]int32(nil), rw.CellCity...),
		ChunkID:   chunkID,
		StyleName: rw.StyleName,
	}
	g.CellType = make([]WorldTerrainType, len(rw.CellType))
	for i, v := range rw.CellType {
		g.CellType[i] = WorldTerrainType(v)
	}
	if g.StyleName == "" {
		g.StyleName = "desert"
	}

	g.Density = rw.Density
	g.FaceRoads = true
	if rw.FaceRoads != nil {
		g.FaceRoads = *rw.FaceRoads
	}

	// GPU-scattered biomes. Always allocate the per-cell arrays so the brush and
	// mask upload can index them even on older saves that predate biomes.
	nCells := rw.Cols * rw.Rows
	g.GrassDensity = make([]float32, nCells)
	g.RockDensity = make([]float32, nCells)
	copy(g.GrassDensity, rw.GrassDensity)
	copy(g.RockDensity, rw.RockDensity)
	g.GrassRange = rw.GrassRange
	if g.GrassRange <= 0 {
		g.GrassRange = defaultGrassRange
	}
	g.RockRange = rw.RockRange
	if g.RockRange <= 0 {
		g.RockRange = defaultRockRange
	}
	g.GrassHeight = rw.GrassHeight
	if g.GrassHeight <= 0 {
		g.GrassHeight = defaultGrassHeight
	}
	g.GrassWidth = rw.GrassWidth
	if g.GrassWidth <= 0 {
		g.GrassWidth = defaultGrassWidth
	}
	g.RockSides = rw.RockSides
	if g.RockSides < 3 {
		g.RockSides = defaultRockSides
	}
	for _, rt := range rw.Palette {
		g.Palette = append(g.Palette, BuildingTemplate{
			Name:        rt.Name,
			Width:       rt.Width,
			Depth:       rt.Depth,
			Stories:     rt.Stories,
			StoryHeight: rt.StoryHeight,
			WallThick:   rt.WallThick,
			Parapet:     rt.Parapet,
			DoorWidth:   rt.DoorWidth,
			Enterable:   rt.Enterable,
			WallTex:     rt.WallTex,
			RoofTex:     rt.RoofTex,
			FloorTex:    rt.FloorTex,
			Weight:      rt.Weight,
		})
	}
	for _, rs := range rw.Roads {
		stroke := RoadStroke{Width: rs.Width}
		for _, p := range rs.Points {
			stroke.Points = append(stroke.Points, mgl32.Vec2{p.X, p.Y})
		}
		if len(stroke.Points) >= 2 {
			g.Roads = append(g.Roads, stroke)
		}
	}
	return g
}

// LoadLevel reads a YAML level file, converts it to runtime types, and uploads
// all geometry to the GPU. Texture loading is driven from this function via the
// global textureNames map and executablePath variable.
func LoadLevel(levelPath string) (Level, error) {
	data, err := os.ReadFile(levelPath)
	if err != nil {
		return Level{}, err
	}

	var raw RawLevel
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Level{}, err
	}

	var level Level
	texNeeded := map[string]bool{}

	for _, rc := range raw.Chunks {
		chunk, chunkTex, err := loadRawChunk(rc)
		if err != nil {
			return Level{}, err
		}
		for t := range chunkTex {
			texNeeded[t] = true
		}
		level.Chunks = append(level.Chunks, chunk)
	}

	// Convert entities
	for _, re := range raw.Entities {
		scale := mgl32.Vec3{re.Scale.X, re.Scale.Y, re.Scale.Z}
		if scale == (mgl32.Vec3{}) {
			scale = mgl32.Vec3{1, 1, 1}
		}
		entity := Entity{
			ID:          re.ID,
			Position:    mgl32.Vec3{re.Position.X, re.Position.Y, re.Position.Z},
			Rotation:    mgl32.Vec3{re.Rotation.X, re.Rotation.Y, re.Rotation.Z},
			Scale:       scale,
			ModelPath:   re.Model,
			TexturePath: re.Texture,
			BoundHalfX:  re.BoundHalfX,
			BoundHalfY:  re.BoundHalfY,
			BoundHalfZ:  re.BoundHalfZ,
			Alpha:       float32Default(re.Alpha, 1.0),
			Visible:     re.Model != "",
		}
		switch re.Bounding {
		case "sphere":
			entity.BoundingKind = BoundingSphere
		case "cylinder":
			entity.BoundingKind = BoundingCylinder
		default:
			entity.BoundingKind = BoundingAABB
		}
		if err := entity.Load(); err != nil {
			return Level{}, fmt.Errorf("entity %q load: %w", re.ID, err)
		}
		level.Entities = append(level.Entities, entity)
	}

	// Convert lights
	rl := raw.Lights
	level.Lights.Global = GlobalLight{
		Color:      mgl32.Vec3{rl.Global.Color.R, rl.Global.Color.G, rl.Global.Color.B},
		Brightness: rl.Global.Brightness,
	}
	for _, rd := range rl.Directional {
		ss := rd.ShadowStrength
		if ss == 0 && rd.CastShadows {
			ss = 0.5 // default to half-strength shadows
		}
		level.Lights.Directional = append(level.Lights.Directional, DirectionalLight{
			Direction:      mgl32.Vec3{rd.Direction.X, rd.Direction.Y, rd.Direction.Z},
			Color:          mgl32.Vec3{rd.Color.R, rd.Color.G, rd.Color.B},
			Brightness:     rd.Brightness,
			CastShadows:    rd.CastShadows,
			ShadowStrength: ss,
		})
	}
	for _, rp := range rl.Point {
		ss := rp.ShadowStrength
		if ss == 0 && rp.CastShadows {
			ss = 0.5
		}
		level.Lights.Points = append(level.Lights.Points, PointLight{
			Position:       mgl32.Vec3{rp.Position.X, rp.Position.Y, rp.Position.Z},
			Color:          mgl32.Vec3{rp.Color.R, rp.Color.G, rp.Color.B},
			Brightness:     rp.Brightness,
			Radius:         rp.Radius,
			CastShadows:    rp.CastShadows,
			ShadowStrength: ss,
		})
	}

	// Load all textures referenced by this level
	for name := range texNeeded {
		if _, already := Textures[name]; already {
			continue
		}
		path, ok := textureNames[name]
		if !ok {
			return Level{}, fmt.Errorf("unknown texture %q — ensure a matching .png exists in textures/", name)
		}
		if err := LoadTexture(name, filepath.Join(executablePath, path)); err != nil {
			return Level{}, err
		}
	}

	level.Spatial = BuildSpatialHash(level.Chunks, 8.0)

	return level, nil
}

// boolDefault returns the value of a *bool, defaulting to def when nil.
func boolDefault(p *bool, def bool) bool {
	if p != nil {
		return *p
	}
	return def
}

// float32Default returns the value of a *float32, defaulting to def when nil.
func float32Default(p *float32, def float32) float32 {
	if p != nil {
		return *p
	}
	return def
}

// buildGapQuad returns the vertices and indices for a vertical quad spanning
// from lo to hi between start and end (in XZ), with the given segment length.
func buildGapQuad(start, end mgl32.Vec2, lo, hi, segLen float32) ([][]float32, []int) {
	gapH := hi - lo
	a := []float32{start.X(), hi, start.Y(), 0, 0}
	b := []float32{end.X(), hi, end.Y(), segLen, 0}
	c := []float32{end.X(), lo, end.Y(), segLen, gapH}
	d := []float32{start.X(), lo, start.Y(), 0, gapH}
	return [][]float32{a, b, c, d}, []int{0, 1, 2, 2, 3, 0}
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
