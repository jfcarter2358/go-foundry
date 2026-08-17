package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"

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

// RawDetailWave is the YAML representation of one displacement wave layer.
type RawDetailWave struct {
	Enabled   bool    `yaml:"enabled"`
	Amplitude float32 `yaml:"amplitude,omitempty"`
	Frequency float32 `yaml:"frequency,omitempty"`
	Rotation  float32 `yaml:"rotation,omitempty"` // degrees within the face plane
	TessLevel float32 `yaml:"tess_level,omitempty"`
}

// RawDetail is the YAML representation of a face's procedural detail.
// Present only when a face has detail enabled (omitempty on the pointer).
type RawDetail struct {
	Preset string `yaml:"preset,omitempty"`
	Seed   int64  `yaml:"seed"`

	Displace bool            `yaml:"displace,omitempty"`
	Waves    []RawDetailWave `yaml:"waves,omitempty"`

	Noise          bool    `yaml:"noise,omitempty"`
	NoiseAmplitude float32 `yaml:"noise_amplitude,omitempty"`
	NoiseFrequency float32 `yaml:"noise_frequency,omitempty"`
	NoiseOctaves   int     `yaml:"noise_octaves,omitempty"`
	NoiseTessLevel float32 `yaml:"noise_tess_level,omitempty"`

	EdgeTaper float32 `yaml:"edge_taper,omitempty"`

	// Legacy single-layer displacement fields (pre-waves levels). Load-only:
	// detailFromRaw folds them into wave 0 + the noise layer, and they are never
	// written back.
	Amplitude float32 `yaml:"amplitude,omitempty"`
	Frequency float32 `yaml:"frequency,omitempty"`
	Octaves   int     `yaml:"octaves,omitempty"`
	TessLevel float32 `yaml:"tess_level,omitempty"`

	Scatter        bool    `yaml:"scatter,omitempty"`
	ScatterMesh    string  `yaml:"scatter_mesh,omitempty"`
	ScatterTex     string  `yaml:"scatter_tex,omitempty"`
	ScatterDensity float32 `yaml:"scatter_density,omitempty"`
	ScatterSpacing float32 `yaml:"scatter_spacing,omitempty"`
	ScatterScale   float32 `yaml:"scatter_scale,omitempty"`
	ScatterJitter  float32 `yaml:"scatter_jitter,omitempty"`
	ScatterRange   float32 `yaml:"scatter_range,omitempty"`
}

// detailFromRaw converts an optional RawDetail into a DetailSpec. A nil pointer
// yields a disabled spec.
func detailFromRaw(r *RawDetail) DetailSpec {
	if r == nil {
		return DetailSpec{}
	}
	d := DetailSpec{
		Enabled:        true,
		Preset:         r.Preset,
		Seed:           r.Seed,
		Displace:       r.Displace,
		Noise:          r.Noise,
		NoiseAmplitude: r.NoiseAmplitude,
		NoiseFrequency: r.NoiseFrequency,
		NoiseOctaves:   r.NoiseOctaves,
		NoiseTessLevel: r.NoiseTessLevel,
		EdgeTaper:      r.EdgeTaper,
		Scatter:        r.Scatter,
		ScatterMesh:    r.ScatterMesh,
		ScatterTex:     r.ScatterTex,
		ScatterDensity: r.ScatterDensity,
		ScatterSpacing: r.ScatterSpacing,
		ScatterScale:   r.ScatterScale,
		ScatterJitter:  r.ScatterJitter,
		ScatterRange:   r.ScatterRange,
	}
	for i, w := range r.Waves {
		if i >= detailMaxWaves {
			break
		}
		d.Waves[i] = DetailWave{
			Enabled:   w.Enabled,
			Amplitude: w.Amplitude,
			Frequency: w.Frequency,
			Rotation:  w.Rotation,
			TessLevel: w.TessLevel,
		}
	}
	// Legacy levels stored one noise layer as amplitude/frequency/octaves/tess.
	// Fold it into the noise layer so old faces keep their look.
	if len(r.Waves) == 0 && !r.Noise && r.Amplitude != 0 {
		d.Noise = true
		d.NoiseAmplitude = r.Amplitude
		d.NoiseFrequency = r.Frequency
		d.NoiseOctaves = r.Octaves
		d.NoiseTessLevel = r.TessLevel
	}
	d.clamp()
	return d
}

// RawEdge describes one edge (wall or portal) in a sector's edge list.
type RawEdge struct {
	From          string            `yaml:"from"`
	To            string            `yaml:"to"`
	Kind          string            `yaml:"kind"`
	Texture       string            `yaml:"texture"`
	Collision     bool              `yaml:"collision"`
	Hidden        bool              `yaml:"hidden"`           // true = wall/portal not rendered (default false = visible)
	Pinned        bool              `yaml:"pinned,omitempty"` // true = wall stays a wall even if shared with another sector
	ToSector      string            `yaml:"to_sector"`
	DrawGap       bool              `yaml:"draw_gap"`
	GapTexture    string            `yaml:"gap_texture"`
	DoubleSided   *bool             `yaml:"double_sided,omitempty"` // default true when absent
	Alpha         *float32          `yaml:"alpha,omitempty"`        // nil = opaque (1.0)
	Extrusions    []RawExtrusion    `yaml:"extrusions,omitempty"`
	WallTerrain   bool              `yaml:"terrain,omitempty"`
	TerrainPoints []RawTerrainPoint `yaml:"terrain_points,omitempty"`
	UVOffset      Point             `yaml:"uv_offset,omitempty"`
	UVScale       Point             `yaml:"uv_scale,omitempty"`
	UVRotation    float32           `yaml:"uv_rotation,omitempty"`
	Detail        *RawDetail        `yaml:"detail,omitempty"`
	Smooth        bool              `yaml:"smooth,omitempty"`       // interpolate vertex normals instead of flat facets
	SmoothAngle   float32           `yaml:"smooth_angle,omitempty"` // crease threshold in degrees; 0 = default
}

// RawFloor is the YAML representation of a sector floor.
type RawFloor struct {
	Enabled       bool              `yaml:"enabled"`
	Hidden        bool              `yaml:"hidden,omitempty"` // texture/geometry hidden; Enabled/collision unaffected
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
	Detail        *RawDetail        `yaml:"detail,omitempty"`
	Smooth        bool              `yaml:"smooth,omitempty"`       // interpolate vertex normals instead of flat facets
	SmoothAngle   float32           `yaml:"smooth_angle,omitempty"` // crease threshold in degrees; 0 = default
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
	Detail        *RawDetail        `yaml:"detail,omitempty"`
	Smooth        bool              `yaml:"smooth,omitempty"`       // interpolate vertex normals instead of flat facets
	SmoothAngle   float32           `yaml:"smooth_angle,omitempty"` // crease threshold in degrees; 0 = default
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
	ID          string            `yaml:"id"`
	Type        string            `yaml:"type"` // "rect_prism", "sphere", "cylinder", "plane", "water"
	Position    RawEntityPosition `yaml:"position"`
	Rotation    RawEntityPosition `yaml:"rotation"`
	Scale       RawEntityPosition `yaml:"scale"`
	Texture     string            `yaml:"texture"`
	Sides       int               `yaml:"sides,omitempty"` // cylinder only; default 16
	Collision   bool              `yaml:"collision,omitempty"`
	Alpha       *float32          `yaml:"alpha,omitempty"` // nil = opaque (1.0)
	UVOffset    Point             `yaml:"uv_offset,omitempty"`
	UVScale     Point             `yaml:"uv_scale,omitempty"`     // zero = {1,1}
	UVRotation  float32           `yaml:"uv_rotation,omitempty"`  // radians
	Smooth      bool              `yaml:"smooth,omitempty"`       // interpolate vertex normals instead of flat facets
	SmoothAngle float32           `yaml:"smooth_angle,omitempty"` // crease threshold in degrees; 0 = default
	Water       *RawWater         `yaml:"water,omitempty"`        // water primitives only
}

// RawWater is the YAML representation of a water plane's wave and reflection
// settings. Absent means the defaults from DefaultWaterParams.
type RawWater struct {
	WaveAmplitude float32   `yaml:"wave_amplitude"`
	WaveLength    float32   `yaml:"wave_length"`
	WaveSpeed     float32   `yaml:"wave_speed"`
	Reflectivity  float32   `yaml:"reflectivity"`
	ReflectColor  RawColor3 `yaml:"reflect_color"`
}

// RawColor3 is an RGB triple in YAML.
type RawColor3 struct {
	R float32 `yaml:"r"`
	G float32 `yaml:"g"`
	B float32 `yaml:"b"`
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
	ID           string           `yaml:"id"`
	Points       map[string]Point `yaml:"points"`
	Sectors      []RawSector      `yaml:"sectors"`
	Origin       RawChunkOrigin   `yaml:"origin"`
	Rotation     float32          `yaml:"rotation"`
	Scale        float32          `yaml:"scale"`
	Primitives   []RawPrimitive   `yaml:"primitives,omitempty"`
	BiomeSectors []RawBiomeSector `yaml:"biome_sectors,omitempty"`
}

// RawBiomeSector is the YAML representation of a user-drawn biome-paint
// polygon (see BiomeSector). Points are stored in chunk-local coordinates
// (like structural sector points) and re-derived to world space at load time
// using the chunk's Origin/Rotation/Scale, so a sector moves with its chunk.
type RawBiomeSector struct {
	ID     string        `yaml:"id"`
	Points []Point       `yaml:"points"`
	Brush  RawBiomeBrush `yaml:"brush"`
}

// RawBiomeBrush is the YAML form of a BiomeBrush.
type RawBiomeBrush struct {
	Kind        int32    `yaml:"kind"`
	Density     float32  `yaml:"density"`
	Range       float32  `yaml:"range,omitempty"`
	Color       RawColor `yaml:"color,omitempty"`
	GrassHeight float32  `yaml:"grass_height,omitempty"`
	GrassWidth  float32  `yaml:"grass_width,omitempty"`
	HeadScale   float32  `yaml:"head_scale,omitempty"`  // wildflower bloom / cattail seed-head size
	GrassClump  float32  `yaml:"grass_clump,omitempty"` // Grass only: 0 = even scatter, 1 = tight plant-like tufts

	// LOD1Distance/LOD2Distance are the Near->Mid and Mid->Far mesh-detail
	// boundaries (grass.go's three-tier scatter); 0 = the kind's default
	// fraction of Range. See BiomeBrush's own doc comment.
	LOD1Distance float32 `yaml:"lod1_distance,omitempty"`
	LOD2Distance float32 `yaml:"lod2_distance,omitempty"`

	RockSides  int32     `yaml:"rock_sides,omitempty"`
	RockScale  float32   `yaml:"rock_scale,omitempty"`
	SandHeight float32   `yaml:"sand_height,omitempty"`
	WaterDepth float32   `yaml:"water_depth,omitempty"`
	Water      *RawWater `yaml:"water,omitempty"` // Water kind only; absent = DefaultWaterParams

	// Moss kind only: whether the optional clump scatter rides on top of the
	// ground patch. Plain omitempty bool — the meaningful default (patch
	// only) matches Go's zero value, same reasoning as RawFloor.Hidden.
	MossScatter bool `yaml:"moss_scatter,omitempty"`

	// Wildflower bloom options (see BiomeBrush's Bloom* fields). All plain
	// omitempty bools for the same reason as MossScatter: every zero value
	// is the pre-existing look, so blooms saved before these existed load
	// back unchanged with no migration.
	BloomSpike      bool     `yaml:"bloom_spike,omitempty"`
	BloomCenter     bool     `yaml:"bloom_center,omitempty"`
	BloomSolidColor bool     `yaml:"bloom_solid_color,omitempty"`
	CenterColor     RawColor `yaml:"center_color,omitempty"`
	StemLeaves      bool     `yaml:"stem_leaves,omitempty"` // Wildflower only: basal leaves around the stem
	LeafScale       float32  `yaml:"leaf_scale,omitempty"`  // multiplier on those leaves; 0 = defaultWildflowerLeafScale

	// Slope gating over displaced faces (Grass/Sand/Rock/Moss); 0 = the
	// kind's default (resolvedMinSlope/resolvedMaxSlope).
	MinSlopeAngle float32 `yaml:"min_slope_angle,omitempty"`
	MaxSlopeAngle float32 `yaml:"max_slope_angle,omitempty"`

	// Ground patch (see BiomeBrush's own doc comment).
	GroundTexture   string  `yaml:"ground_texture,omitempty"`
	GroundAmplitude float32 `yaml:"ground_amplitude,omitempty"`
	GroundFrequency float32 `yaml:"ground_frequency,omitempty"`
	GroundOctaves   int32   `yaml:"ground_octaves,omitempty"`
	// GroundUVScale tiles the patch texture (world XZ * scale), same units as
	// a surface's uv_scale; zero/absent = defaultGroundUVScale.
	GroundUVScale Point `yaml:"ground_uv_scale,omitempty"`

	// Soft edges and fake ground-contact shadow (see BiomeBrush's own doc
	// comments). All zero-value-safe: a sector saved before these existed
	// loads back with the exact old hard-edged, unshadowed look.
	RangeFadeFraction  float32 `yaml:"range_fade_fraction,omitempty"`
	EdgeFadeDistance   float32 `yaml:"edge_fade_distance,omitempty"`
	FakeShadowStrength float32 `yaml:"fake_shadow_strength,omitempty"`
	FakeShadowSpread   float32 `yaml:"fake_shadow_spread,omitempty"`

	// BoundaryJitter (see BiomeBrush's own doc comment) — 0 means "use
	// defaultBoundaryJitter", not "off", so a sector saved before this existed
	// picks it up automatically rather than needing a resave to look right.
	BoundaryJitter float32 `yaml:"boundary_jitter,omitempty"`
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
// Direction is written out for readability and for levels read by older builds;
// Yaw/Pitch (degrees) are the authored values and win when present. They are
// pointers so an absent pair is distinguishable from a deliberate 0/0, which
// would otherwise be indistinguishable from "derive from direction".
type RawDirLight struct {
	Direction struct {
		X float32 `yaml:"x"`
		Y float32 `yaml:"y"`
		Z float32 `yaml:"z"`
	} `yaml:"direction"`
	Yaw            *float32 `yaml:"yaw,omitempty"`
	Pitch          *float32 `yaml:"pitch,omitempty"`
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

	cos32 := float32(math.Cos(float64(rc.Rotation)))
	sin32 := float32(math.Sin(float64(rc.Rotation)))

	// Sort so the point pool is built in a deterministic order — Go map
	// iteration is randomised, which would otherwise shuffle point indices on
	// every load and make saved files churn between runs.
	names := make([]string, 0, len(rc.Points))
	for name := range rc.Points {
		names = append(names, name)
	}
	sort.Strings(names)

	chunk.PointNames = make([]string, 0, len(names))
	pointNameToIdx := make(map[string]int, len(names))
	for _, name := range names {
		rp := rc.Points[name]
		x := rp.X * sc
		y := rp.Y * sc
		tx := cos32*x - sin32*y + origin.X()
		tz := sin32*x + cos32*y + origin.Z()
		pointNameToIdx[name] = len(chunk.Points)
		chunk.Points = append(chunk.Points, mgl32.Vec2{tx, tz})
		chunk.PointNames = append(chunk.PointNames, name)
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
				Hidden:        rs.Floor.Hidden,
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
				Detail:        detailFromRaw(rs.Floor.Detail),
				Smooth:        rs.Floor.Smooth,
				SmoothAngle:   rs.Floor.SmoothAngle,
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
				Detail:        detailFromRaw(rs.Ceiling.Detail),
				Smooth:        rs.Ceiling.Smooth,
				SmoothAngle:   rs.Ceiling.SmoothAngle,
			},
		}

		if rs.Floor.Texture != "" {
			texNeeded[rs.Floor.Texture] = true
		}
		if rs.Ceiling.Texture != "" {
			texNeeded[rs.Ceiling.Texture] = true
		}

		// A sector's edges must form a closed ring visiting each point exactly
		// once. Silently de-duplicating a repeated name (as this used to do)
		// produces a sector with fewer vertices than edges, which desynchronises
		// Walls/Portals from EdgeKinds and yields degenerate, overlapping
		// geometry. Reject the file instead so the damage is visible.
		pointSeen := make(map[string]bool, len(rs.Edges))
		for ei, e := range rs.Edges {
			if pointSeen[e.From] {
				return Chunk{}, nil, fmt.Errorf(
					"sector %q: point %q appears more than once in the edge ring", rs.ID, e.From)
			}
			pointSeen[e.From] = true

			next := rs.Edges[(ei+1)%len(rs.Edges)]
			if e.To != next.From {
				return Chunk{}, nil, fmt.Errorf(
					"sector %q: edge %d ends at %q but edge %d starts at %q — edges must form a closed ring",
					rs.ID, ei, e.To, (ei+1)%len(rs.Edges), next.From)
			}

			idx, ok := pointNameToIdx[e.From]
			if !ok {
				return Chunk{}, nil, fmt.Errorf(
					"sector %q: edge references unknown point %q", rs.ID, e.From)
			}
			sector.PointIndices = append(sector.PointIndices, idx)
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
					Detail:        detailFromRaw(edge.Detail),
					Smooth:        edge.Smooth,
					SmoothAngle:   edge.SmoothAngle,
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
			// Reversing vertex order shifts which polygon edge is which: new
			// edge i is the reverse of old edge (n-2-i) mod n, not old edge
			// (n-1-i) — a plain array-reverse of EdgeKinds/Walls/Portals
			// misaligns edge kinds against the reversed points. Rebuild the
			// per-edge kind/wall/portal records in the correct new order.
			n := len(sector.PointIndices)
			type edgeRec struct {
				kind   string
				wall   Wall
				portal Portal
			}
			recs := make([]edgeRec, n)
			wi, pi := 0, 0
			for i := 0; i < n; i++ {
				if i < len(sector.EdgeKinds) && sector.EdgeKinds[i] == "portal" {
					recs[i] = edgeRec{kind: "portal", portal: sector.Portals[pi]}
					pi++
				} else {
					recs[i] = edgeRec{kind: "wall", wall: sector.Walls[wi]}
					wi++
				}
			}

			newPointIndices := make([]int, n)
			for i := 0; i < n; i++ {
				newPointIndices[i] = sector.PointIndices[n-1-i]
			}
			sector.PointIndices = newPointIndices

			newEdgeKinds := make([]string, n)
			newWalls := make([]Wall, 0, len(sector.Walls))
			newPortals := make([]Portal, 0, len(sector.Portals))
			for i := 0; i < n; i++ {
				rec := recs[((n-2-i)%n+n)%n]
				newEdgeKinds[i] = rec.kind
				if rec.kind == "portal" {
					rec.portal.StartPoint, rec.portal.EndPoint = rec.portal.EndPoint, rec.portal.StartPoint
					newPortals = append(newPortals, rec.portal)
				} else {
					rec.wall.StartPoint, rec.wall.EndPoint = rec.wall.EndPoint, rec.wall.StartPoint
					newWalls = append(newWalls, rec.wall)
				}
			}
			sector.EdgeKinds = newEdgeKinds
			sector.Walls = newWalls
			sector.Portals = newPortals
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
				lo1, lo2 := edgeHeights(&chunk, s, s.Floor.VertexHeight, start, end)
				hi1, hi2 := edgeHeights(&chunk, adj, adj.Floor.VertexHeight, start, end)
				verts, indices := buildGapQuad(start, end, lo1, lo2, hi1, hi2, segLen)
				if err := p.FloorGap.Load(verts, indices, tex, mgl32.Vec3{}); err != nil {
					return Chunk{}, nil, fmt.Errorf("portal floor gap %q→%q: %w", s.ID, p.ToSector, err)
				}
			}

			if s.Ceiling.Enabled && adj.Ceiling.Enabled && s.Ceiling.Height > adj.Ceiling.Height {
				lo1, lo2 := edgeHeights(&chunk, adj, adj.Ceiling.VertexHeight, start, end)
				hi1, hi2 := edgeHeights(&chunk, s, s.Ceiling.VertexHeight, start, end)
				verts, indices := buildGapQuad(start, end, lo1, lo2, hi1, hi2, segLen)
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
		case "water":
			kind = PrimitiveWater
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
			ID:          rp.ID,
			Kind:        kind,
			Position:    mgl32.Vec3{worldX, worldY, worldZ},
			Rotation:    mgl32.Vec3{rp.Rotation.X, rp.Rotation.Y, rp.Rotation.Z},
			Scale:       scale,
			Texture:     rp.Texture,
			Sides:       sides,
			Collision:   rp.Collision,
			Alpha:       float32Default(rp.Alpha, 1.0),
			UVOffset:    mgl32.Vec2{rp.UVOffset.X, rp.UVOffset.Y},
			UVScale:     uvScale,
			UVRotation:  rp.UVRotation,
			Smooth:      rp.Smooth,
			SmoothAngle: rp.SmoothAngle,
			Water:       waterParamsFromRaw(rp.Water, kind),
			ChunkID:     chunk.ID,
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
		case PrimitivePlane, PrimitiveWater:
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

	// Bounds so far come only from the chunk's points; fold in primitives so a
	// plane extending past the sector polygons isn't frustum-culled with them.
	chunk.RecomputeBounds()

	// Biome sector polygons — points are chunk-local, re-derived to world
	// space using the same transform already applied to structural points.
	for _, rb := range rc.BiomeSectors {
		bs := BiomeSector{
			ID:          rb.ID,
			Points:      make([]mgl32.Vec2, len(rb.Points)),
			HeightDirty: true,
			GroundDirty: true,
			Brush: BiomeBrush{
				Kind:         rb.Brush.Kind,
				Density:      rb.Brush.Density,
				Range:        rb.Brush.Range,
				Color:        mgl32.Vec3{rb.Brush.Color.R, rb.Brush.Color.G, rb.Brush.Color.B},
				GrassHeight:  rb.Brush.GrassHeight,
				GrassWidth:   rb.Brush.GrassWidth,
				HeadScale:    rb.Brush.HeadScale,
				GrassClump:   rb.Brush.GrassClump,
				LOD1Distance: rb.Brush.LOD1Distance,
				LOD2Distance: rb.Brush.LOD2Distance,
				RockSides:    rb.Brush.RockSides,
				RockScale:    rb.Brush.RockScale,
				SandHeight:   rb.Brush.SandHeight,
				WaterDepth:   rb.Brush.WaterDepth,
				Water:        waterParamsFromRaw(rb.Brush.Water, PrimitiveWater),

				MossScatter:   rb.Brush.MossScatter,
				MinSlopeAngle: rb.Brush.MinSlopeAngle,
				MaxSlopeAngle: rb.Brush.MaxSlopeAngle,

				BloomSpike:      rb.Brush.BloomSpike,
				BloomCenter:     rb.Brush.BloomCenter,
				BloomSolidColor: rb.Brush.BloomSolidColor,
				CenterColor: mgl32.Vec3{
					rb.Brush.CenterColor.R, rb.Brush.CenterColor.G, rb.Brush.CenterColor.B,
				},
				StemLeaves: rb.Brush.StemLeaves,
				LeafScale:  rb.Brush.LeafScale,

				GroundTexture:   rb.Brush.GroundTexture,
				GroundAmplitude: rb.Brush.GroundAmplitude,
				GroundFrequency: rb.Brush.GroundFrequency,
				GroundOctaves:   rb.Brush.GroundOctaves,
				GroundUVScale:   mgl32.Vec2{rb.Brush.GroundUVScale.X, rb.Brush.GroundUVScale.Y},

				RangeFadeFraction:  rb.Brush.RangeFadeFraction,
				EdgeFadeDistance:   rb.Brush.EdgeFadeDistance,
				FakeShadowStrength: rb.Brush.FakeShadowStrength,
				FakeShadowSpread:   rb.Brush.FakeShadowSpread,
				BoundaryJitter:     rb.Brush.BoundaryJitter,
			},
		}
		for i, rp := range rb.Points {
			bs.Points[i] = transformPoint2D(mgl32.Vec2{rp.X, rp.Y}, origin, rc.Rotation, sc)
		}
		chunk.BiomeSectors = append(chunk.BiomeSectors, bs)
	}

	return chunk, texNeeded, nil
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

	return rawToLevel(raw)
}

// blankRawLevel returns a minimal valid RawLevel: a single four-walled room
// lit by soft ambient light. Used to write a starter file from the editor's
// "New Level" dialog.
func blankRawLevel() RawLevel {
	return RawLevel{
		Chunks: []RawChunk{{
			ID: "default",
			Points: map[string]Point{
				"A": {X: -4, Y: -4},
				"B": {X: 4, Y: -4},
				"C": {X: 4, Y: 4},
				"D": {X: -4, Y: 4},
			},
			Sectors: []RawSector{{
				ID: "room",
				Floor: RawFloor{
					Enabled: true,
					Height:  0,
					Texture: "default",
				},
				Ceiling: RawCeiling{
					Enabled: true,
					Height:  4,
					Texture: "default",
				},
				Edges: []RawEdge{
					{From: "A", To: "B", Kind: "wall", Texture: "default", Collision: true},
					{From: "B", To: "C", Kind: "wall", Texture: "default", Collision: true},
					{From: "C", To: "D", Kind: "wall", Texture: "default", Collision: true},
					{From: "D", To: "A", Kind: "wall", Texture: "default", Collision: true},
				},
			}},
		}},
		Lights: RawLights{
			Global: RawGlobalLight{Color: RawColor{R: 1, G: 1, B: 1}, Brightness: 0.3},
		},
	}
}

// NewBlankLevel builds a completely empty in-memory level (no chunks or
// entities, and not backed by any file on disk) suitable for opening the
// editor to a truly blank scene. It still carries the same default ambient
// light as blankRawLevel — a level with zero lights renders everything pure
// black (see fragment.glsl: lighting starts at globalLightColor *
// globalLightBrightness and only grows from there), so geometry added via
// "New Chunk" would otherwise be invisible until a light is added by hand.
// The caller is responsible for saving it if they want to keep it.
func NewBlankLevel() (Level, error) {
	return rawToLevel(RawLevel{
		Lights: RawLights{
			Global: RawGlobalLight{Color: RawColor{R: 1, G: 1, B: 1}, Brightness: 0.3},
		},
	})
}

// rawToLightSet converts parsed YAML lights into runtime lights. Split out of
// rawToLevel because it touches no GPU state.
func rawToLightSet(rl RawLights) LightSet {
	var ls LightSet
	ls.Global = GlobalLight{
		Color:      mgl32.Vec3{rl.Global.Color.R, rl.Global.Color.G, rl.Global.Color.B},
		Brightness: rl.Global.Brightness,
	}
	for _, rd := range rl.Directional {
		ss := rd.ShadowStrength
		if ss == 0 && rd.CastShadows {
			ss = 0.5 // default to half-strength shadows
		}
		dl := DirectionalLight{
			Color:          mgl32.Vec3{rd.Color.R, rd.Color.G, rd.Color.B},
			Brightness:     rd.Brightness,
			CastShadows:    rd.CastShadows,
			ShadowStrength: ss,
		}
		// Angles are authoritative when present; otherwise derive them from the
		// direction vector, which is all pre-rotation levels stored.
		if rd.Yaw != nil && rd.Pitch != nil {
			dl.Yaw, dl.Pitch = *rd.Yaw, *rd.Pitch
		} else {
			dl.Yaw, dl.Pitch = DirLightYawPitch(mgl32.Vec3{rd.Direction.X, rd.Direction.Y, rd.Direction.Z})
		}
		dl.SyncDirection()
		ls.Directional = append(ls.Directional, dl)
	}
	for _, rp := range rl.Point {
		ss := rp.ShadowStrength
		if ss == 0 && rp.CastShadows {
			ss = 0.5
		}
		ls.Points = append(ls.Points, PointLight{
			Position:       mgl32.Vec3{rp.Position.X, rp.Position.Y, rp.Position.Z},
			Color:          mgl32.Vec3{rp.Color.R, rp.Color.G, rp.Color.B},
			Brightness:     rp.Brightness,
			Radius:         rp.Radius,
			CastShadows:    rp.CastShadows,
			ShadowStrength: ss,
		})
	}
	return ls
}

// rawToLevel converts a parsed RawLevel into runtime types and uploads all
// referenced geometry/textures to the GPU.
func rawToLevel(raw RawLevel) (Level, error) {
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
	level.Lights = rawToLightSet(raw.Lights)

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
