package world

import "foundry/internal/geom"

// level_schema.go — the YAML-facing "Raw*" level format: every type here maps
// directly to a level file's on-disk shape (see resources/levels/*.yaml).
// Conversion to/from the runtime types (Chunk, Sector, Level, ...) lives in
// level_loader.go, split out from what used to be one 1200+-line level.go —
// see RESTRUCTURE_PROPOSAL.md §5 Phase A.

// RawVolume is the YAML representation of a scriptable region (see volume.go).
type RawVolume struct {
	ID            string `yaml:"id"`
	RawScriptData `yaml:",inline"`
	Shape         string `yaml:"shape,omitempty"` // box (default) | sphere | cylinder
	// Attach is what the volume rides — a frame, an entity, or a bone — or
	// absent for the world. With it set, Position and Rotation are in the
	// carrier's coordinates. The same block a sprite's `attach:` is.
	Attach *RawAttach `yaml:"attach,omitempty"`
	// Frame is the older spelling of `attach: {frame: ...}`, read so a level
	// written before volumes took the whole binding still loads. Never written.
	Frame    string  `yaml:"frame,omitempty"`
	Position RawVec3 `yaml:"position"`
	Rotation RawVec3 `yaml:"rotation,omitempty"`
	Half     RawVec3 `yaml:"half"`
	Enabled  *bool   `yaml:"enabled,omitempty"` // default true when absent
	// LoadLevel makes the volume a level exit: the named level is loaded when
	// the player walks in, with no script involved. See Volume.LoadLevel.
	LoadLevel string `yaml:"load_level,omitempty"`
	// Fog fills the region with fog. Absent is the ordinary case — a volume
	// that is only a trigger. See Volume.Fog.
	Fog *RawVolumeFog `yaml:"fog,omitempty"`
	// Cloud fills the region with volumetric cloud. See Volume.Cloud.
	Cloud *RawVolumeCloud `yaml:"cloud,omitempty"`
	// Doorway makes the volume a frame boundary. See Volume.Doorway.
	Doorway *RawFrameDoorway `yaml:"doorway,omitempty"`
	// Scatter fills the region with foliage. See Volume.Scatter.
	Scatter *RawVolumeScatter `yaml:"scatter,omitempty"`
}

// RawVolumeScatter is the foliage filling a volume (volume_scatter.go).
//
// Disabled rather than Enabled, so the zero value is "on": the block only
// exists because someone asked for scatter, and `scatter: {}` meaning no
// scatter would be a trap. The same reasoning RawVolumeFog.Disabled follows,
// which this deliberately mirrors.
//
// The fields that carry a meaningful zero — every distance and fraction whose
// 0 means "use the kind's default", and the booleans — are plain values. The
// ones where 0 is a legitimate authored setting distinct from "absent" are
// pointers, on the rule the rest of this file uses.
type RawVolumeScatter struct {
	Disabled bool `yaml:"disabled,omitempty"`

	Kind    string    `yaml:"kind,omitempty"` // grass (default) | rock | wildflower | cattail | moss
	Density *float32  `yaml:"density,omitempty"`
	Range   float32   `yaml:"range,omitempty"`
	Color   *RawColor `yaml:"color,omitempty"`

	LOD1Distance float32 `yaml:"lod1_distance,omitempty"`
	LOD2Distance float32 `yaml:"lod2_distance,omitempty"`

	GrassHeight *float32 `yaml:"grass_height,omitempty"`
	GrassWidth  *float32 `yaml:"grass_width,omitempty"`
	HeadScale   float32  `yaml:"head_scale,omitempty"`
	GrassClump  float32  `yaml:"grass_clump,omitempty"`

	BloomSpike      bool      `yaml:"bloom_spike,omitempty"`
	BloomCenter     bool      `yaml:"bloom_center,omitempty"`
	BloomSolidColor bool      `yaml:"bloom_solid_color,omitempty"`
	CenterColor     *RawColor `yaml:"center_color,omitempty"`

	StemLeaves bool    `yaml:"stem_leaves,omitempty"`
	LeafScale  float32 `yaml:"leaf_scale,omitempty"`

	RockSides int32    `yaml:"rock_sides,omitempty"`
	RockScale *float32 `yaml:"rock_scale,omitempty"`

	MinSlopeAngle float32 `yaml:"min_slope_angle,omitempty"`
	MaxSlopeAngle float32 `yaml:"max_slope_angle,omitempty"`

	RangeFadeFraction *float32 `yaml:"range_fade_fraction,omitempty"`
	EdgeFadeDistance  float32  `yaml:"edge_fade_distance,omitempty"`
	BoundaryJitter    float32  `yaml:"boundary_jitter,omitempty"`
}

// RawFrameDoorway is the boundary between two frames (volume_doorway.go).
//
// One key, and it is optional: `doorway: {}` is a ramp onto the world, which is
// what nearly every doorway is. That is deliberate — the block's presence is
// the whole of what makes a volume a doorway, and requiring a target would mean
// writing `to: world` on every ship in the game.
type RawFrameDoorway struct {
	To string `yaml:"to,omitempty"` // entity id, or absent/"world" for the world
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

// RawCloudLook is what a cloud looks like (cloud.go), shared by a volume's
// `cloud:` block and the sky's `clouds:` block, which inline it.
//
// Every number is a pointer, because every one of them has a meaningful zero
// that is not its default — a zero erosion is soft cotton, a zero wind is a
// still day. NoShadows is inverted for the reason RawVolumeFog.Disabled is: the
// ordinary cloud casts, and the key only needs writing for the one that does
// not.
type RawCloudLook struct {
	Color      *RawColor `yaml:"color,omitempty"`
	Coverage   *float32  `yaml:"coverage,omitempty"`
	Density    *float32  `yaml:"density,omitempty"`
	Scale      *float32  `yaml:"scale,omitempty"`
	Detail     *float32  `yaml:"detail,omitempty"`
	Erosion    *float32  `yaml:"erosion,omitempty"`
	Wind       *RawVec3  `yaml:"wind,omitempty"`
	Absorption *float32  `yaml:"absorption,omitempty"`
	Ambient    *float32  `yaml:"ambient,omitempty"`
	NoShadows  bool      `yaml:"no_shadows,omitempty"`
}

// RawVolumeCloud is the cloud filling a volume (cloud.go). Disabled rather than
// Enabled, on RawVolumeFog's reasoning.
type RawVolumeCloud struct {
	Disabled     bool `yaml:"disabled,omitempty"`
	RawCloudLook `yaml:",inline"`
	Softness     *float32 `yaml:"softness,omitempty"`
}

// RawSkyClouds is the sky's cloud deck (cloud.go).
type RawSkyClouds struct {
	Disabled     bool `yaml:"disabled,omitempty"`
	RawCloudLook `yaml:",inline"`
	Altitude     *float32 `yaml:"altitude,omitempty"`
	Thickness    *float32 `yaml:"thickness,omitempty"`
	Range        *float32 `yaml:"range,omitempty"`
}

// RawPrefabInstance is one placement of a prefab file (prefab.go).
//
// Scale is a plain float rather than a pointer, because 0 has a defined reading
// here — EffectiveScale treats it as 1, the same way Chunk.Scale and
// EntityChunk.Scale do — so absent and "1" are the same value and there is
// nothing for a pointer to distinguish.
type RawPrefabInstance struct {
	ID string `yaml:"id"`
	// File is the prefab under prefabs/, without the extension.
	File     string  `yaml:"file"`
	Position RawVec3 `yaml:"position"`
	Rotation float32 `yaml:"rotation,omitempty"` // radians about Y
	Scale    float32 `yaml:"scale,omitempty"`    // 0 = 1
	// Frame is the vehicle the instance rides, absent for the world.
	Frame string `yaml:"frame,omitempty"`
}

// RawPrefab is a prefab file's own contents (prefab.go).
//
// The brushes are a flat list rather than a chunk, because a prefab has no
// placement of its own — the instance supplies that. What a prefab holds is
// content authored around its origin; where that content goes is the level's
// business, and writing a `chunks:` block here would mean every prefab file
// carrying an Origin nothing reads.
type RawPrefab struct {
	Brushes  []RawBrush      `yaml:"brushes,omitempty"`
	Entities []RawEntity     `yaml:"entities,omitempty"`
	Volumes  []RawVolume     `yaml:"volumes,omitempty"`
	Lights   []RawPointLight `yaml:"lights,omitempty"`
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

// RawBrush is the YAML representation of one brush: the level's unit of solid
// geometry (see brush.go).
//
// Verts are flat [x, y, z] triples rather than a mapping per vertex because a
// brush has a lot of them and one line each is unreadable; faces name vertex
// indices for the same reason. Both are written in the order the in-memory
// brush holds them, so a load/save round trip is byte-identical and a diff of
// a level file shows only what actually changed.
type RawBrush struct {
	ID            string `yaml:"id"`
	RawScriptData `yaml:",inline"`
	// Verts is [x, y, z] per vertex, flattened. Chunk-local.
	Verts []float32      `yaml:"verts"`
	Faces []RawBrushFace `yaml:"faces"`
	// Solid is whether bodies collide with this brush. A pointer so the
	// common case — a solid one — writes nothing and an explicitly
	// non-solid one is distinguishable from a file written before the field
	// existed.
	Solid *bool `yaml:"solid,omitempty"`
	// Dynamic marks a brush a script moves at run time. A plain bool rather
	// than a pointer, unlike Solid: the default is false, so an absent key and
	// an explicit `dynamic: false` mean the same thing.
	Dynamic bool `yaml:"dynamic,omitempty"`
	// Detail is the brush's free-form surface geometry, drawn instead of the
	// faces above and ignored by collision entirely (brush_detail.go). Nested
	// rather than a second pair of top-level keys so that the two meshes read
	// as the two meshes they are, and absent for the ordinary brush that is
	// drawn as the solid it is.
	Detail *RawBrushMesh `yaml:"detail,omitempty"`
}

// RawBrushMesh is the YAML representation of a brush's detail mesh: the same
// flattened vertex list and index-into-it face list a brush itself is written
// as, since it is the same kind of mesh.
type RawBrushMesh struct {
	Verts []float32      `yaml:"verts"`
	Faces []RawBrushFace `yaml:"faces"`
}

// RawBrushFace is the YAML representation of one face of a brush.
type RawBrushFace struct {
	// Indices lists the face's corners as indices into the owning brush's
	// Verts, in winding order (CCW seen from outside the solid).
	Indices []int `yaml:"indices"`
	// Texture is omitted when the face wears none, which is how a face that has
	// not been painted saves: the renderer draws it from its own normal.
	Texture      string     `yaml:"texture,omitempty"`
	BlendTexture string     `yaml:"blend_texture,omitempty"`
	BlendMask    string     `yaml:"blend_mask,omitempty"`
	UVOffset     geom.Point `yaml:"uv_offset,omitempty"`
	UVScale      geom.Point `yaml:"uv_scale,omitempty"`
	UVRotation   float32    `yaml:"uv_rotation,omitempty"`
	// UVs are the face's per-corner texture coordinates from an atlas unwrap,
	// flattened two floats per corner in Indices' order — the same flattening
	// Verts uses. Absent on every face whose coordinates are projected from its
	// own plane, which is every face that has not been unwrapped.
	UVs         []float32 `yaml:"uvs,omitempty"`
	Smooth      bool      `yaml:"smooth,omitempty"`
	SmoothAngle float32   `yaml:"smooth_angle,omitempty"`
	Alpha       *float32  `yaml:"alpha,omitempty"` // nil = opaque (1.0)
	DoubleSided bool      `yaml:"double_sided,omitempty"`
	// NoDraw is a face with no geometry built at all — a brush side buried
	// against a neighbour's. NoCollide is one that draws but does not stop
	// anything, independent of the brush's own Solid flag.
	NoDraw    bool `yaml:"nodraw,omitempty"`
	NoCollide bool `yaml:"nocollide,omitempty"`
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
// Origin, Rotation, and Scale are this chunk's live placement transform (see
// Chunk.ModelMatrix); brush verts stay chunk-local both on disk and at
// runtime.
type RawChunk struct {
	ID            string `yaml:"id"`
	RawScriptData `yaml:",inline"`
	Brushes       []RawBrush     `yaml:"brushes"`
	Origin        RawChunkOrigin `yaml:"origin"`
	Rotation      float32        `yaml:"rotation"`
	Scale         float32        `yaml:"scale"`
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
	Disabled bool `yaml:"disabled,omitempty"`
}

// TODO(brush migration, Phase 5): RawBiomeSector and RawBiomeBrush lived
// here — the painted polygon and its kind/appearance. Biome scatter is being
// rebuilt as a field on RawVolume (mirroring RawVolumeFog), so the schema for
// it comes back there rather than under a chunk. RawChunkLayer went with
// sector authoring layers, which brushes have no equivalent of: a brush sits
// where its verts are, in three dimensions, rather than on a 2D band.

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
	// DisableControl and DisableGravity start the player without their keys or
	// without falling. Opt-outs, so every spawn written before them loads as the
	// ordinary walking start and saves without gaining either key.
	DisableControl bool `yaml:"disable_control,omitempty"`
	DisableGravity bool `yaml:"disable_gravity,omitempty"`
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

// RawWaypointAnimation is a path a dynamic brush can be sent along
// (waypoint.go): an ID, how many seconds it takes, and its points in order.
type RawWaypointAnimation struct {
	ID     string        `yaml:"id"`
	Length float32       `yaml:"length,omitempty"`
	Points []RawWaypoint `yaml:"points"`
}

// RawWaypoint is one point of a waypoint animation.
//
// Rotation and Time are plain values because absent and zero mean the same: no
// turn, and reached the moment the brush is sent. Scale is a pointer, on this
// file's rule: an absent scale is {1,1,1} and not a brush collapsed to nothing.
//
// Events are atom names without their colons, posted to the brush on arrival.
type RawWaypoint struct {
	Position RawVec3  `yaml:"position"`
	Rotation RawVec3  `yaml:"rotation,omitempty"`
	Scale    *RawVec3 `yaml:"scale,omitempty"`
	Time     float32  `yaml:"time,omitempty"`
	Events   []string `yaml:"events,omitempty"`
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
	// Seal is the level's sealing box (seal.go). Absent from a level written
	// before it existed, which gets one fitted when it loads.
	Seal *RawSeal `yaml:"seal,omitempty"`
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
	// Prefabs are the level's placements of reusable content (prefab.go). Only
	// the placements are stored: what each one MAKES is read from its file
	// every load, which is the whole point of the link.
	Prefabs []RawPrefabInstance `yaml:"prefabs,omitempty"`
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
	// WaypointAnimations are the level's paths a dynamic brush can be sent
	// along (waypoint.go). Level-scoped and world-space for the volumes'
	// reason: a path crosses whatever chunks it likes.
	WaypointAnimations []RawWaypointAnimation `yaml:"waypoint_animations,omitempty"`
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

	// Clouds is the volumetric cloud deck. Absent is a clear sky.
	Clouds *RawSkyClouds `yaml:"clouds,omitempty"`
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
