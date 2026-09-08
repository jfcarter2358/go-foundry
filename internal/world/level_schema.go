package world

import "foundry/internal/geom"

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
	UVOffset    geom.Point          `yaml:"uv_offset,omitempty"`
	UVScale     geom.Point          `yaml:"uv_scale,omitempty"`
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
	// DetailFromRaw folds them into wave 0 + the noise layer, and they are never
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

// DetailFromRaw converts an optional RawDetail into a DetailSpec. A nil pointer
// yields a disabled spec.
func DetailFromRaw(r *RawDetail) DetailSpec {
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
		if i >= DetailMaxWaves {
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
	d.Clamp()
	return d
}

// RawVolume is the YAML representation of a scriptable region (see volume.go).
type RawVolume struct {
	ID            string `yaml:"id"`
	RawScriptData `yaml:",inline"`
	Shape         string  `yaml:"shape,omitempty"` // box (default) | sphere | cylinder
	Position      RawVec3 `yaml:"position"`
	Rotation      RawVec3 `yaml:"rotation,omitempty"`
	Half          RawVec3 `yaml:"half"`
	Enabled       *bool   `yaml:"enabled,omitempty"` // default true when absent
	// LoadLevel makes the volume a level exit: the named level is loaded when
	// the player walks in, with no script involved. See Volume.LoadLevel.
	LoadLevel string `yaml:"load_level,omitempty"`
	// Fog fills the region with fog. Absent is the ordinary case — a volume
	// that is only a trigger. See Volume.Fog.
	Fog *RawVolumeFog `yaml:"fog,omitempty"`
}

// RawVolumeFog is the fog filling a volume (volume_fog.go).
//
// Disabled rather than Enabled, so the zero value is "on": the block only
// exists because someone added fog, and `fog: {}` meaning no fog would be a
// trap. Same reasoning as RawFog.Disabled, which this deliberately mirrors — an
// author who has written one should be able to guess the other.
type RawVolumeFog struct {
	Disabled bool `yaml:"disabled,omitempty"`

	Color      *RawColor `yaml:"color,omitempty"`
	Density    *float32  `yaml:"density,omitempty"`     // per world unit inside
	MaxOpacity *float32  `yaml:"max_opacity,omitempty"` // default 1
}

// RawSprite is the YAML representation of a level sprite (sprite.go) — a
// camera-facing textured quad.
//
// Nearly every field is optional, and the defaults are chosen so that the
// smallest useful entry is an id, a position and a texture. Width/Height/Alpha
// are pointers because 0 is never a meaningful authored value for them and
// "absent" has to mean 1, not 0; Visible is a pointer for the usual reason
// (absent means on, and a level that never mentioned it meant on).
type RawSprite struct {
	ID            string `yaml:"id"`
	RawScriptData `yaml:",inline"`
	Position      RawVec3    `yaml:"position"`
	Rotation      RawVec3    `yaml:"rotation,omitempty"`
	Width         *float32   `yaml:"width,omitempty"`  // default 1
	Height        *float32   `yaml:"height,omitempty"` // default 1
	Texture       string     `yaml:"texture,omitempty"`
	Billboard     string     `yaml:"billboard,omitempty"` // full (default) | y-axis | fixed
	Blend         string     `yaml:"blend,omitempty"`     // cutout (default) | alpha | additive
	Tint          *RawColor  `yaml:"tint,omitempty"`      // default white
	Alpha         *float32   `yaml:"alpha,omitempty"`     // default 1
	Lit           bool       `yaml:"lit,omitempty"`
	CastShadows   bool       `yaml:"cast_shadows,omitempty"`
	Visible       *bool      `yaml:"visible,omitempty"` // default true when absent
	SheetCols     int        `yaml:"sheet_cols,omitempty"`
	SheetRows     int        `yaml:"sheet_rows,omitempty"`
	Frame         int        `yaml:"frame,omitempty"`
	Attach        *RawAttach `yaml:"attach,omitempty"`
}

// RawScenery is the YAML representation of a placed static model
// (scenery.go).
//
// Deliberately the shortest entry in the format. Bounds are not here because
// they are read off the model, and neither tags, attributes nor a script are,
// because a prop that needs any of those is an entity — see scenery.go.
type RawScenery struct {
	ID string `yaml:"id"`
	// Model is the mesh file, install-relative: "models/barrel.fmdl".
	Model    string  `yaml:"model"`
	Position RawVec3 `yaml:"position"`
	Rotation RawVec3 `yaml:"rotation,omitempty"`
	// Scale is a pointer so an unwritten key means {1,1,1} rather than
	// collapsing the model to nothing.
	Scale *RawVec3 `yaml:"scale,omitempty"`
	// Solid defaults to true when absent — scenery exists mostly to be walked
	// around, so the flag is written only to switch collision off.
	Solid *bool `yaml:"solid,omitempty"`
}

// RawEmitter is the YAML representation of a placed particle effect
// (emitter.go).
//
// Almost nothing is here, on purpose: what an effect IS lives in its .fpar
// document, and this is only where one is placed and the handful of knobs worth
// varying between two placements of the same effect. Nothing about the live
// simulation is written — a level file says an explosion goes here, not where
// its sparks currently are.
type RawEmitter struct {
	ID            string `yaml:"id"`
	RawScriptData `yaml:",inline"`
	Effect        string     `yaml:"effect"`
	Position      RawVec3    `yaml:"position"`
	Rotation      RawVec3    `yaml:"rotation,omitempty"`
	Enabled       *bool      `yaml:"enabled,omitempty"`  // default true when absent
	Emitting      *bool      `yaml:"emitting,omitempty"` // default true when absent
	RateScale     *float32   `yaml:"rate_scale,omitempty"`
	Tint          *RawColor  `yaml:"tint,omitempty"`
	Attach        *RawAttach `yaml:"attach,omitempty"`
}

// RawEdge describes one edge (wall or portal) in a sector's edge list.
type RawEdge struct {
	// ID is the edge's stable identity, how a script addresses it. Absent in
	// files written before scripting existed — the loader then derives one from
	// the point-name pair, so an older level migrates silently and gains real
	// IDs on its next save. See sector_edge_id.go.
	ID            string `yaml:"id,omitempty"`
	RawScriptData `yaml:",inline"`
	From          string          `yaml:"from"`
	To            string          `yaml:"to"`
	Kind          string          `yaml:"kind"`
	Texture       string          `yaml:"texture"`
	BlendTexture  string          `yaml:"blend_texture,omitempty"`
	BlendMask     string          `yaml:"blend_mask,omitempty"`
	Collision     bool            `yaml:"collision"`
	Hidden        bool            `yaml:"hidden"`           // true = wall/portal not rendered (default false = visible)
	Pinned        bool            `yaml:"pinned,omitempty"` // true = wall stays a wall even if shared with another sector
	ToSector      string          `yaml:"to_sector"`
	ToFrame       string          `yaml:"to_frame,omitempty"`
	DrawGap       bool            `yaml:"draw_gap"`
	GapTexture    string          `yaml:"gap_texture"`
	DoubleSided   *bool           `yaml:"double_sided,omitempty"` // default true when absent
	Alpha         *float32        `yaml:"alpha,omitempty"`        // nil = opaque (1.0)
	Mesh          *RawSurfaceMesh `yaml:"mesh,omitempty"`
	// Extrusions, WallTerrain and TerrainPoints are read-only legacy input: the
	// loader migrates them into Mesh and the writer never emits them again.
	Extrusions    []RawExtrusion    `yaml:"extrusions,omitempty"`
	WallTerrain   bool              `yaml:"terrain,omitempty"`
	TerrainPoints []RawTerrainPoint `yaml:"terrain_points,omitempty"`
	UVOffset      geom.Point        `yaml:"uv_offset,omitempty"`
	UVScale       geom.Point        `yaml:"uv_scale,omitempty"`
	UVRotation    float32           `yaml:"uv_rotation,omitempty"`
	Detail        *RawDetail        `yaml:"detail,omitempty"`
	Smooth        bool              `yaml:"smooth,omitempty"`       // interpolate vertex normals instead of flat facets
	SmoothAngle   float32           `yaml:"smooth_angle,omitempty"` // crease threshold in degrees; 0 = default
}

// RawFloor is the YAML representation of a sector floor.
type RawFloor struct {
	RawScriptData `yaml:",inline"`
	Enabled       bool            `yaml:"enabled"`
	Hidden        bool            `yaml:"hidden,omitempty"` // texture/geometry hidden; Enabled/collision unaffected
	Height        float32         `yaml:"height"`
	Texture       string          `yaml:"texture"`
	BlendTexture  string          `yaml:"blend_texture,omitempty"`
	BlendMask     string          `yaml:"blend_mask,omitempty"`
	Terrain       bool            `yaml:"terrain"`
	VertexOffsets []float32       `yaml:"vertex_offsets,omitempty"`
	DoubleSided   *bool           `yaml:"double_sided,omitempty"` // default true when absent
	Alpha         *float32        `yaml:"alpha,omitempty"`        // nil = opaque (1.0)
	Mesh          *RawSurfaceMesh `yaml:"mesh,omitempty"`
	// Extrusions and TerrainPoints are read-only legacy input: the loader
	// migrates them into Mesh and the writer never emits them again.
	Extrusions    []RawExtrusion    `yaml:"extrusions,omitempty"`
	Openings      []RawOpening      `yaml:"openings,omitempty"` // holes cut through the surface; see Opening
	TerrainPoints []RawTerrainPoint `yaml:"terrain_points,omitempty"`
	// Skirt closes the band between this floor and whatever is under it — a
	// storey's slab edge, or the face under an overhang. "" (the default) draws
	// nothing, so a level that never asked for one round-trips unchanged.
	Skirt        string `yaml:"skirt,omitempty"`
	SkirtTexture string `yaml:"skirt_texture,omitempty"`
	// Slab is the floor's thickness — the underside it grows and the rim round
	// it. Absent is the plane; see slab.go.
	Slab        *RawSlab   `yaml:"slab,omitempty"`
	UVOffset    geom.Point `yaml:"uv_offset,omitempty"`
	UVScale     geom.Point `yaml:"uv_scale,omitempty"`
	UVRotation  float32    `yaml:"uv_rotation,omitempty"`
	Detail      *RawDetail `yaml:"detail,omitempty"`
	Smooth      bool       `yaml:"smooth,omitempty"`       // interpolate vertex normals instead of flat facets
	SmoothAngle float32    `yaml:"smooth_angle,omitempty"` // crease threshold in degrees; 0 = default
}

// RawCeiling is the YAML representation of a sector ceiling.
type RawCeiling struct {
	RawScriptData `yaml:",inline"`
	Enabled       bool            `yaml:"enabled"`
	Height        float32         `yaml:"height"`
	Texture       string          `yaml:"texture"`
	BlendTexture  string          `yaml:"blend_texture,omitempty"`
	BlendMask     string          `yaml:"blend_mask,omitempty"`
	Terrain       bool            `yaml:"terrain,omitempty"`
	VertexOffsets []float32       `yaml:"vertex_offsets,omitempty"`
	DoubleSided   *bool           `yaml:"double_sided,omitempty"` // default true when absent
	Alpha         *float32        `yaml:"alpha,omitempty"`        // nil = opaque (1.0)
	Mesh          *RawSurfaceMesh `yaml:"mesh,omitempty"`
	// Extrusions and TerrainPoints are read-only legacy input: the loader
	// migrates them into Mesh and the writer never emits them again.
	Extrusions    []RawExtrusion    `yaml:"extrusions,omitempty"`
	Openings      []RawOpening      `yaml:"openings,omitempty"` // holes cut through the surface; see Opening
	TerrainPoints []RawTerrainPoint `yaml:"terrain_points,omitempty"`
	// Slab is the ceiling's thickness — the top side it grows and the rim
	// round it. Absent is the plane; see slab.go.
	Slab        *RawSlab   `yaml:"slab,omitempty"`
	UVOffset    geom.Point `yaml:"uv_offset,omitempty"`
	UVScale     geom.Point `yaml:"uv_scale,omitempty"`
	UVRotation  float32    `yaml:"uv_rotation,omitempty"`
	Detail      *RawDetail `yaml:"detail,omitempty"`
	Smooth      bool       `yaml:"smooth,omitempty"`       // interpolate vertex normals instead of flat facets
	SmoothAngle float32    `yaml:"smooth_angle,omitempty"` // crease threshold in degrees; 0 = default
}

// RawSlab is the YAML representation of a floor's or a ceiling's thickness (see
// slab.go). Absent means the surface is the bare plane it has always been, so a
// level that never asked for a slab round-trips byte-identically.
type RawSlab struct {
	Thickness    float32 `yaml:"thickness"`
	Hidden       bool    `yaml:"hidden,omitempty"`
	Texture      string  `yaml:"texture,omitempty"` // "" = the surface's own
	BlendTexture string  `yaml:"blend_texture,omitempty"`
	BlendMask    string  `yaml:"blend_mask,omitempty"`
	RimTexture   string  `yaml:"rim_texture,omitempty"` // "" = texture
	// RimAcrossPortals builds the rim over portal edges as well as ordinary
	// ones — off by default because a portal's neighbour draws the same band
	// in the same place (see slab.go). On is for an author who wants a slab's
	// edge sealed even where it would normally continue into another sector.
	RimAcrossPortals bool       `yaml:"rim_across_portals,omitempty"`
	DoubleSided      bool       `yaml:"double_sided,omitempty"`
	UVOffset         geom.Point `yaml:"uv_offset,omitempty"`
	UVScale          geom.Point `yaml:"uv_scale,omitempty"`
	UVRotation       float32    `yaml:"uv_rotation,omitempty"`
	// Mesh is the far face's own shape. Absent means it mirrors the surface's,
	// which is what nearly every slab does — so shaping one costs a level file
	// nothing until somebody actually does it.
	Mesh *RawSurfaceMesh `yaml:"mesh,omitempty"`
}

// RawSurfaceMesh is the YAML representation of a surface's editable mesh (see
// surface_mesh.go). Absent means the surface is flat, which is what nearly all
// of them are — so the common case costs a level file nothing.
//
// Points are flat triples rather than a mapping per point because a shaped
// surface has a lot of them and one line each is unreadable; faces name point
// indices for the same reason. Both are written in the order the in-memory mesh
// holds them, so a load/save round trip is byte-identical and a diff of a level
// file shows only what actually changed.
type RawSurfaceMesh struct {
	// Points is [u, v, depth] per point, flattened.
	Points []float32 `yaml:"points,flow"`
	// Boundary is the outline corner each point mirrors, -1 for a free one.
	// Index-parallel to the point triples.
	Boundary []int            `yaml:"boundary,flow"`
	Faces    []RawSurfaceFace `yaml:"faces"`
}

// RawSurfaceFace is one polygon of a surface mesh.
type RawSurfaceFace struct {
	Verts []int `yaml:"verts,flow"`
	// UVs is [u, v] per corner, flattened; absent means the face derives its
	// texture coordinates from the surface, which most faces do.
	UVs []float32 `yaml:"uvs,flow,omitempty"`
	// Holes are rings of point indices cut out of this face.
	Holes   [][]int `yaml:"holes,omitempty"`
	Texture string  `yaml:"texture,omitempty"`
	Alpha   float32 `yaml:"alpha,omitempty"`
	Hole    bool    `yaml:"hole,omitempty"`
	Smooth  bool    `yaml:"smooth,omitempty"`
}

// RawOpening is the YAML representation of a hole cut through a floor or
// ceiling (see Opening). Points are chunk-local {x, y} mapping to (X, Z), the
// same convention RawChunk.Points uses, so a hole moves with its chunk.
//
// No far-side sector is recorded: what an opening leads to is derived from the
// geometry every time it is asked (OpeningTarget), so there is no reference
// here that could go stale.
type RawOpening struct {
	ID     string       `yaml:"id"`
	Points []geom.Point `yaml:"points"`
	// Link names the opening on the slab's other face — see Opening.Link.
	// Omitted for a standalone hole.
	Link string `yaml:"link,omitempty"`
}

// RawSector is the YAML representation of one sector.
type RawSector struct {
	ID            string `yaml:"id"`
	RawScriptData `yaml:",inline"`
	Floor         RawFloor   `yaml:"floor"`
	Ceiling       RawCeiling `yaml:"ceiling"`
	Edges         []RawEdge  `yaml:"edges"`
	// Layer is the chunk authoring band this sector was drawn on (see
	// Sector.Layer and layer.go). Omitted for the ground layer, which is both
	// the default and what every sector authored before layers existed is on —
	// so adding this changed no existing save.
	Layer int `yaml:"layer,omitempty"`
}

// RawWater is the YAML representation of a water surface's wave and reflection
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
// Points use {x, y} which map to chunk-local (X, Z) — Y-up convention.
// Origin, Rotation, and Scale are this chunk's live placement transform (see
// Chunk.ModelMatrix); Points and everything derived from them stay chunk-local
// both on disk and at runtime.
type RawChunk struct {
	ID            string `yaml:"id"`
	RawScriptData `yaml:",inline"`
	Points        map[string]geom.Point `yaml:"points"`
	Sectors       []RawSector           `yaml:"sectors"`
	Origin        RawChunkOrigin        `yaml:"origin"`
	Rotation      float32               `yaml:"rotation"`
	Scale         float32               `yaml:"scale"`
	// Velocity is world units per second added to Origin during play, for a
	// chunk that moves on its own — a lift, a barge, a closing wall. Omitted
	// for the still chunk every level is mostly made of, so a level written
	// before it existed round-trips byte-identical. See Chunk.Velocity.
	Velocity RawVec3 `yaml:"velocity,omitempty"`
	// Frame names the entity whose local space this chunk's geometry is
	// authored in — a vehicle interior, carried around the world by whatever
	// owns it. Omitted is the ordinary case: a chunk placed directly in world
	// space by its own Origin/Rotation/Scale, with no carrier.
	Frame string `yaml:"frame,omitempty"`
	// Disabled starts the chunk switched off: not drawn, not collided with, and
	// never uploaded in the first place — which is what makes it worth writing
	// for a level that streams its areas in. A script turns it back on with
	// `@id.prop.enabled = true`. See Chunk.Disabled for why it is spelled this
	// way round.
	Disabled     bool             `yaml:"disabled,omitempty"`
	BiomeSectors []RawBiomeSector `yaml:"biome_sectors,omitempty"`
	// Layers names this chunk's authoring bands and gives each the heights a
	// sector drawn on it starts at. Purely editor metadata — a sector's own
	// `layer:` is the durable fact — so it is omitted entirely for a chunk that
	// only ever used the ground layer, and a chunk missing entries for bands
	// its sectors do claim is valid (see Chunk.LayerAt).
	Layers []RawChunkLayer `yaml:"layers,omitempty"`
}

// RawChunkLayer is the YAML representation of one ChunkLayer.
type RawChunkLayer struct {
	Name  string  `yaml:"name,omitempty"`
	BaseY float32 `yaml:"base_y"`
	CeilY float32 `yaml:"ceil_y"`
}

// RawBiomeSector is the YAML representation of a user-drawn biome-paint
// polygon (see BiomeSector). Points are chunk-local, like structural sector
// points, so a sector moves with its chunk automatically — through the same
// Chunk.ModelMatrix, with no separate handling of its own.
type RawBiomeSector struct {
	ID     string        `yaml:"id"`
	Points []geom.Point  `yaml:"points"`
	Brush  RawBiomeBrush `yaml:"brush"`
	// DrapeSector records which structural surface this paint was drawn over,
	// for the one case derivation cannot recover: two sectors stacked at the
	// same XZ. Omitted when unbound, so a level written before it existed
	// round-trips byte-identical. See BiomeSector.DrapeSector.
	DrapeSector string `yaml:"drape_sector,omitempty"`
	// Layer is the chunk authoring band this paint was drawn on; see
	// RawSector.Layer, which it mirrors exactly.
	Layer int `yaml:"layer,omitempty"`
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

	// Water kind only: drop the lakebed patch under the surface. Plain
	// omitempty bool for the same reason as MossScatter below — the zero value
	// (lakebed shown) is the pre-existing look, so no migration is needed.
	HideLakebed bool `yaml:"hide_lakebed,omitempty"`

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
	LeafScale       float32  `yaml:"leaf_scale,omitempty"`  // multiplier on those leaves; 0 = DefaultWildflowerLeafScale

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
	// a surface's uv_scale; zero/absent = DefaultGroundUVScale.
	GroundUVScale geom.Point `yaml:"ground_uv_scale,omitempty"`

	// Soft edges (see BiomeBrush's own doc comments). Both zero-value-safe: a
	// sector saved before these existed loads back with the exact old
	// hard-edged look.
	RangeFadeFraction float32 `yaml:"range_fade_fraction,omitempty"`
	EdgeFadeDistance  float32 `yaml:"edge_fade_distance,omitempty"`

	// BoundaryJitter (see BiomeBrush's own doc comment) — 0 means "use
	// DefaultBoundaryJitter", not "off", so a sector saved before this existed
	// picks it up automatically rather than needing a resave to look right.
	BoundaryJitter float32 `yaml:"boundary_jitter,omitempty"`
}

// RawVec3 is a bare {x, y, z} triple in YAML.
type RawVec3 struct {
	X float32 `yaml:"x"`
	Y float32 `yaml:"y"`
	Z float32 `yaml:"z"`
}

// RawSpawn is the YAML representation of the level's player spawn point
// (spawn_point.go). Rotation is Euler XYZ in radians; the facing it produces is
// derived from it rather than stored, so the arrow in the editor and the camera
// gameplay starts with can never disagree.
type RawSpawn struct {
	Position RawVec3 `yaml:"position"`
	Rotation RawVec3 `yaml:"rotation"`
	// Frame names a vehicle to start aboard. With it set, Position and Rotation
	// are that vehicle's local coordinates rather than the world's — a spawn
	// inside a ship has to travel with the ship, or moving the ship moves the
	// player out through its hull.
	Frame string `yaml:"frame,omitempty"`
	// Seat names which seat to start in. Only meaningful with Frame set; empty
	// is standing on the deck.
	Seat string `yaml:"seat,omitempty"`
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
	// Disabled, rather than Enabled, so the zero value is "on" — every level
	// authored before the editor grew a switch for this has one, and none of
	// them meant it to be dark. Same reasoning as RawFloor.Hidden.
	Disabled bool `yaml:"disabled,omitempty"`
}

// RawDirLight is the YAML representation of a directional light.
// Direction is written out for readability and for levels read by older builds;
// Yaw/Pitch (degrees) are the authored values and win when present. They are
// pointers so an absent pair is distinguishable from a deliberate 0/0, which
// would otherwise be indistinguishable from "derive from direction".
type RawDirLight struct {
	ID            string `yaml:"id,omitempty"` // stable identity for scripts; minted on load when absent
	RawScriptData `yaml:",inline"`
	Direction     struct {
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
	Disabled       bool     `yaml:"disabled,omitempty"` // see RawGlobalLight.Disabled
}

// RawPointLight is the YAML representation of a point light.
type RawPointLight struct {
	ID            string `yaml:"id,omitempty"` // stable identity for scripts; minted on load when absent
	RawScriptData `yaml:",inline"`
	Position      struct {
		X float32 `yaml:"x"`
		Y float32 `yaml:"y"`
		Z float32 `yaml:"z"`
	} `yaml:"position"`
	Color          RawColor `yaml:"color"`
	Brightness     float32  `yaml:"brightness"`
	Radius         float32  `yaml:"radius"`
	CastShadows    bool     `yaml:"cast_shadows"`
	ShadowStrength float32  `yaml:"shadow_strength"`
	Disabled       bool     `yaml:"disabled,omitempty"` // see RawGlobalLight.Disabled
}

// RawLights holds all light definitions from YAML.
type RawLights struct {
	Global      RawGlobalLight  `yaml:"global"`
	Directional []RawDirLight   `yaml:"directional"`
	Point       []RawPointLight `yaml:"point"`
}

// RawLevel is the top-level YAML document.
//
// Spawn is a pointer so "no spawn point authored" stays distinguishable from
// one deliberately placed at the origin facing +X, which a zero value would
// otherwise look exactly like.
//
// Entities are instances of the definitions under resources/entities/ — their
// YAML shape is RawEntity, which lives in entity_def.go beside the definition
// format it references. They sit at the level rather than inside a chunk
// because they move: an entity that walked out of the chunk it was authored in
// would otherwise have to be re-parented mid-frame, and its saved position is
// world-space for exactly the same reason.
// RawSound is a placed sound source (sound.go).
//
// Every optional key follows level_schema.go's two conventions: a pointer
// where "absent" and "zero" must stay distinguishable — a Gain of 0 is a muted
// sound and is not the same as an unwritten gain — and a name string rather
// than an enum int for Bus and Atten, so a reordered enum cannot silently
// reinterpret an old file.
type RawSound struct {
	ID            string `yaml:"id"`
	RawScriptData `yaml:",inline"`
	// Clip names a file under sounds/, without the extension.
	Clip     string  `yaml:"clip"`
	Position RawVec3 `yaml:"position"`
	Rotation RawVec3 `yaml:"rotation,omitempty"`

	Enabled *bool `yaml:"enabled,omitempty"` // default true when absent
	Playing *bool `yaml:"playing,omitempty"` // default true when absent
	Loop    *bool `yaml:"loop,omitempty"`    // default true when absent

	Gain  *float32 `yaml:"gain,omitempty"`  // default 1
	Pitch *float32 `yaml:"pitch,omitempty"` // default 1
	Bus   string   `yaml:"bus,omitempty"`   // default "effects"

	RefDistance *float32 `yaml:"ref_distance,omitempty"`
	MaxDistance *float32 `yaml:"max_distance,omitempty"`
	Rolloff     *float32 `yaml:"rolloff,omitempty"`
	Atten       string   `yaml:"atten,omitempty"` // default "inverse"

	// The cone angles are total widths in radians. Plain floats with a plain
	// omitempty: absent means 0, and 0 genuinely means omnidirectional.
	ConeInner     float32 `yaml:"cone_inner,omitempty"`
	ConeOuter     float32 `yaml:"cone_outer,omitempty"`
	ConeOuterGain float32 `yaml:"cone_outer_gain,omitempty"`

	Attach *RawAttach `yaml:"attach,omitempty"`
}

// RawMusic is the level's global track.
//
// It follows RawSpawn's shape rather than the list shape the other placed
// objects take: there is at most one, it has no ID, and it is written under a
// top-level key. A level with no music writes no `music:` block at all, which
// is why Level.Music is a pointer.
type RawMusic struct {
	// Track names a file under sounds/, without the extension.
	Track string   `yaml:"track"`
	Gain  *float32 `yaml:"gain,omitempty"` // default 1
	Loop  *bool    `yaml:"loop,omitempty"` // default true
	// Fade is the crossfade in seconds when a script switches track.
	Fade float32 `yaml:"fade,omitempty"`
}

type RawLevel struct {
	Chunks []RawChunk `yaml:"chunks"`
	Spawn  *RawSpawn  `yaml:"spawn,omitempty"`
	// Scripts are the level's own handlers, where `@self` is the level. Game
	// rules that belong to no single object live here — a win condition, a
	// spawn director — and it is a list because rules split by topic across
	// files far more naturally than one object's behaviour does.
	Scripts []string `yaml:"scripts,omitempty"`
	// UI are the .fui screens this level puts up when play starts, named
	// without their extension. A list for the same reason Scripts is: a HUD and
	// a compass and a subtitle bar are three documents, not one, and which of
	// them a level wants is a per-level decision.
	//
	// Menus a script opens later are not listed here — show_screen names them
	// directly. This is only what comes up with the level.
	UI []string `yaml:"ui,omitempty"`
	// Volumes are the level's scriptable regions (volume.go). They sit at the
	// level rather than in a chunk because a trigger can span a doorway between
	// two of them, and its authored position is world-space.
	Volumes  []RawVolume `yaml:"volumes,omitempty"`
	Entities []RawEntity `yaml:"entities,omitempty"`
	// Sprites are the level's camera-facing quads (sprite.go). They sit beside
	// volumes and entities rather than in a chunk because one attached to a
	// moving entity crosses chunks, and its authored position is world-space
	// unless its `attach:` says otherwise.
	Sprites []RawSprite `yaml:"sprites,omitempty"`
	// Emitters are the level's placed particle effects (emitter.go), beside
	// sprites for the same reasons.
	Emitters []RawEmitter `yaml:"emitters,omitempty"`
	// Scenery are the level's placed static models (scenery.go). Level-scoped
	// rather than chunk-scoped for the volumes' reason: a prop is placed in
	// world space, and a chunk boundary is not something an author aims at
	// while scattering rocks along a path.
	Scenery []RawScenery `yaml:"scenery,omitempty"`
	// Sounds are the level's placed sound sources (sound.go), beside emitters
	// for the same reasons — one attached to a moving entity crosses chunks.
	Sounds []RawSound `yaml:"sounds,omitempty"`
	// Music is the level's global track, if it has one.
	Music *RawMusic `yaml:"music,omitempty"`
	// Sky and Fog are the level's environment (sky.go, fog.go). Pointers, and
	// beside Music rather than inside Lights, for the same two reasons: there
	// is at most one of each and it belongs to no object, and a level that has
	// never had one must write no block at all — an absent `sky:` is what keeps
	// every level authored before this looking exactly as it did.
	Sky *RawSky `yaml:"sky,omitempty"`
	Fog *RawFog `yaml:"fog,omitempty"`
	// Player is this level's tuning for the body it starts (player_settings.go).
	// A pointer for the reason Sky and Fog are: the player is engine-owned, so
	// a level that never wanted to say anything about it writes no block at all
	// and gets exactly the movement every level had before the block existed.
	Player *RawPlayer `yaml:"player,omitempty"`
	Lights RawLights  `yaml:"lights"`
}

// RawPlayer is the level's `player:` block — see PlayerSettings.
//
// Every key is a pointer so that absent and zero stay distinct, the way
// RawEntity's bounds overrides are: the block exists the moment an author sets
// one number in it, and the keys they did not touch must keep following the
// engine's default rather than being frozen at zero by having been written out.
type RawPlayer struct {
	// StepHeight is the tallest riser the player walks up rather than being
	// stopped by, in world units. Absent or non-positive means the body's own
	// height.
	StepHeight *float32 `yaml:"step_height,omitempty"`
}

// RawSky is the level's sky (sky.go).
//
// Faces is a flat six-element list in GL face order rather than six named keys,
// because that is the order the upload wants and a named-key block invites
// exactly one of them to be misspelled and silently absent. Writing the names
// out at all — rather than a base name the engine expands — is what lets
// cmd/foundrypack see which textures the level reaches; a base name indexes
// nothing.
type RawSky struct {
	Kind  string   `yaml:"kind,omitempty"`  // gradient (default) | cubemap
	Faces []string `yaml:"faces,omitempty"` // +X, -X, +Y, -Y, +Z, -Z

	Zenith  *RawColor `yaml:"zenith,omitempty"`
	Horizon *RawColor `yaml:"horizon,omitempty"`
	Ground  *RawColor `yaml:"ground,omitempty"`

	Tint      *RawColor `yaml:"tint,omitempty"`
	Intensity *float32  `yaml:"intensity,omitempty"` // default 1
	Rotation  float32   `yaml:"rotation,omitempty"`  // degrees about +Y
}

// RawFog is the level's distance fog (fog.go).
//
// Disabled, rather than Enabled, so the zero value is "on" — the block only
// exists because someone added fog, and `fog: {}` meaning no fog would be a
// trap. Same reasoning as RawGlobalLight.Disabled.
type RawFog struct {
	Mode     string `yaml:"mode,omitempty"` // linear (default) | exp | exp2
	Disabled bool   `yaml:"disabled,omitempty"`

	Color   *RawColor `yaml:"color,omitempty"`
	Start   float32   `yaml:"start,omitempty"`
	End     *float32  `yaml:"end,omitempty"`     // linear only
	Density *float32  `yaml:"density,omitempty"` // exp/exp2 only

	MaxOpacity *float32 `yaml:"max_opacity,omitempty"` // default 1
	SkyBlend   *float32 `yaml:"sky_blend,omitempty"`
	SkyFalloff *float32 `yaml:"sky_falloff,omitempty"`
}
