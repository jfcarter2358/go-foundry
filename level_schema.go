package main

// level_schema.go — the YAML-facing "Raw*" level format: every type here maps
// directly to a level file's on-disk shape (see resources/levels/*.yaml).
// Conversion to/from the runtime types (Chunk, Sector, Level, ...) lives in
// level_loader.go, split out from what used to be one 1200+-line level.go —
// see RESTRUCTURE_PROPOSAL.md §5 Phase A.

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
