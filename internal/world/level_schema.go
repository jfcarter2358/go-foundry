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
	// Checkpoint makes the volume a checkpoint: the first time the player walks
	// in, the game is autosaved, and dying offers a retry from there. See
	// Volume.Checkpoint.
	Checkpoint bool `yaml:"checkpoint,omitempty"`
	// Fog fills the region with fog. Absent is the ordinary case — a volume
	// that is only a trigger. See Volume.Fog.
	Fog *RawVolumeFog `yaml:"fog,omitempty"`
	// Cloud fills the region with volumetric cloud. See Volume.Cloud.
	Cloud *RawVolumeCloud `yaml:"cloud,omitempty"`
	// Gravity replaces the level's ambient pull inside the region, in units per
	// second squared: 0 for a zero-g chamber, a negative number to throw things
	// at the ceiling. Absent leaves gravity alone, which is every volume
	// authored before this existed. See Volume.Gravity.
	Gravity *float32 `yaml:"gravity,omitempty"`
	// ClearsClouds keeps volumetric cloud out of the region. See
	// Volume.ClearsClouds.
	ClearsClouds bool `yaml:"clears_clouds,omitempty"`
	// Doorway makes the volume a frame boundary. See Volume.Doorway.
	Doorway *RawFrameDoorway `yaml:"doorway,omitempty"`
	// Scatter fills the region with foliage. See Volume.Scatter.
	Scatter *RawVolumeScatter `yaml:"scatter,omitempty"`
	// Objective makes the region a place AI agents are sent to act. See
	// Volume.Objective.
	Objective *RawVolumeObjective `yaml:"objective,omitempty"`
	// Context marks the region as a named place to describe to an assistant,
	// and changes nothing about what the volume does. See Volume.Context.
	Context *RawVolumeContext `yaml:"context,omitempty"`
}

// RawVolumeContext is the context block (volume_context.go).
type RawVolumeContext struct {
	// Note is what this place is, in the author's own words — what the geometry
	// cannot say. Optional.
	Note string `yaml:"note,omitempty"`
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

	Kind    string    `yaml:"kind,omitempty"` // grass (default) | rock | wildflower | cattail | moss | biome
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

	// Biome is what `kind: biome` grows (volume_plants.go).
	Biome *RawVolumeBiome `yaml:"biome,omitempty"`
}

// RawVolumeBiome is the biome a `kind: biome` scatter volume grows: a
// terrain biome slot's fields, less the ground layer a volume has no faces
// to draw with.
type RawVolumeBiome struct {
	// Preset names the biome: a file in biomes/ or a built-in.
	Preset string `yaml:"preset"`
	// Density scales how thickly everything grows. Omitted means 1.
	Density float32 `yaml:"density,omitempty"`
	// Tint multiplies every colour in the biome. Omitted means none.
	Tint *RawColor `yaml:"tint,omitempty"`
	// Amounts scales each kind of thing by shape key — {grass: 0, sitka_spruce:
	// 2}: 0 none, 1 as the biome has it, up to 4.
	Amounts map[string]float32 `yaml:"amounts,omitempty"`
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
	Position      RawVec3  `yaml:"position"`
	Rotation      RawVec3  `yaml:"rotation,omitempty"`
	Width         *float32 `yaml:"width,omitempty"`  // default 1
	Height        *float32 `yaml:"height,omitempty"` // default 1
	Texture       string   `yaml:"texture,omitempty"`
	// Material is the .fmat this sprite wears, by name. Overlaid on the fields
	// around it rather than replacing them; see world/sprite_material.go.
	Material    string     `yaml:"material,omitempty"`
	Billboard   string     `yaml:"billboard,omitempty"` // full (default) | y-axis | fixed
	Blend       string     `yaml:"blend,omitempty"`     // cutout (default) | alpha | additive
	Tint        *RawColor  `yaml:"tint,omitempty"`      // default white
	Alpha       *float32   `yaml:"alpha,omitempty"`     // default 1
	Lit         bool       `yaml:"lit,omitempty"`
	CastShadows bool       `yaml:"cast_shadows,omitempty"`
	Visible     *bool      `yaml:"visible,omitempty"` // default true when absent
	SheetCols   int        `yaml:"sheet_cols,omitempty"`
	SheetRows   int        `yaml:"sheet_rows,omitempty"`
	Frame       int        `yaml:"frame,omitempty"`
	Attach      *RawAttach `yaml:"attach,omitempty"`
}

// RawTree is the YAML representation of a hand-placed plant (tree.go):
//
//	trees:
//	- id: spruce_1
//	  species: sitka_spruce
//	  variant: 1
//	  position: {x: 12, "y": 0, z: -40}
//	  yaw: 35
//	  scale: 1.2
//
// Everything after the position is optional: variant 0, no turn, the
// species' own size and colours, solid.
type RawTree struct {
	ID string `yaml:"id"`
	// Species is a foliage shape's name, as a biome file writes it.
	Species string `yaml:"species"`
	Variant int    `yaml:"variant,omitempty"`
	// Position is the foot of the trunk.
	Position RawVec3 `yaml:"position"`
	// Yaw turns it about its trunk, in degrees.
	Yaw float32 `yaml:"yaw,omitempty"`
	// Scale is its size against the species' own; absent is 1.
	Scale float32 `yaml:"scale,omitempty"`
	// Color and Accent are its foliage and moss (or berry, or flower)
	// colours, [r, g, b]; absent is the species' own.
	Color  []float32 `yaml:"color,omitempty,flow"`
	Accent []float32 `yaml:"accent,omitempty,flow"`
	// Solid defaults to true; written only to let bodies through the trunk.
	Solid *bool `yaml:"solid,omitempty"`
	// Chunk is the chunk this tree streams with: not drawn and not solid
	// while it is switched off. Absent: always in.
	Chunk string `yaml:"chunk,omitempty"`
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
	// Materials overrides a cooked Nanite mesh's (.fnm) material slots, slot
	// name to texture: `materials: {stone: granite, moss: snow}`.
	Materials map[string]string `yaml:"materials,omitempty"`
	// Collision is how a cooked Nanite mesh collides: auto (absent), box,
	// hulls, mesh or none. Any other model collides as its box.
	Collision string `yaml:"collision,omitempty"`
	// Wind bends a cooked Nanite mesh in the level's wind: `wind: {bend:
	// 0.02, flutter: 0.08}`. Absent: it stands still.
	Wind *RawSceneryWind `yaml:"wind,omitempty"`
	// Chunk is the chunk this placement streams with: switched off (and
	// unloaded, for an unload_when_disabled chunk) along with it. Absent: always in.
	Chunk string `yaml:"chunk,omitempty"`
}

// RawSceneryWind is a placement's wind (world.SceneryWind).
type RawSceneryWind struct {
	// Bend is how far the top leans in a full gust, as a fraction of the mesh's height (0.02 = 2%).
	Bend float32 `yaml:"bend,omitempty"`
	// Flutter is how far, in metres, branch tips at the crown's edge bob and leaves shake.
	Flutter float32 `yaml:"flutter,omitempty"`
}

// RawDecal is the YAML representation of a projected decal (decal.go) — a
// texture laid onto the level's own surfaces.
//
// Position and rotation are the two keys that always appear, because a decal is
// an orientation as much as a place: its local +Z is the direction it faces out
// of the surface it lies on. Everything else has a default chosen so that the
// smallest useful entry is an id, a position, a rotation and a texture.
//
// Width/Height/Depth/Alpha/MaxAngle/Push are pointers because zero is never a
// meaningful authored value for any of them and "absent" has to mean the default
// rather than nothing; Visible is a pointer for the usual reason.
type RawDecal struct {
	ID            string `yaml:"id"`
	RawScriptData `yaml:",inline"`
	Position      RawVec3 `yaml:"position"`
	// Rotation aims the projection: the decal's local +Z is the direction it
	// faces OUT of the surface, so a decal on a floor is pitched to look up. The
	// editor writes this from the face that was clicked, which is why an author
	// rarely types one.
	Rotation RawVec3 `yaml:"rotation,omitempty"`
	// Width and Height are the size the texture is fitted to, across the surface.
	Width  *float32 `yaml:"width,omitempty"`  // default 0.25
	Height *float32 `yaml:"height,omitempty"` // default 0.25
	// Depth is how far along the projection axis a surface may lie and still be
	// painted, measured either side of Position. Small on purpose: a deep decal
	// aimed at a wall also prints itself on whatever stands behind the wall.
	Depth   *float32 `yaml:"depth,omitempty"` // default 0.25
	Texture string   `yaml:"texture,omitempty"`
	// Material is the .fmat this decal wears, by name. Overlaid on the fields
	// here rather than replacing them; a decal's mesh is ordinary world geometry,
	// so every surface effect applies to one.
	Material string `yaml:"material,omitempty"`
	// Blend is how the decal combines with the surface under it: alpha (the
	// default) lays it over, multiply darkens by it — which is what scorch, dirt
	// and soot want, since one such texture then reads correctly on every colour
	// it lands on — and additive adds light.
	Blend string    `yaml:"blend,omitempty"`
	Tint  *RawColor `yaml:"tint,omitempty"` // default white
	Alpha *float32  `yaml:"alpha,omitempty"`
	// Unlit ignores the scene's lights. Off by default, which is the opposite of
	// a sprite's default: a decal is part of the surface it lies on, and one that
	// stayed bright in a dark room would read as a light source.
	Unlit bool `yaml:"unlit,omitempty"`
	// MaxAngle is how far, in degrees, a surface's normal may lean from the
	// decal's facing and still be painted — what stops a decal sprayed at a wall
	// from smearing onto the floor as well.
	MaxAngle *float32 `yaml:"max_angle,omitempty"` // default 75
	// Push lifts the built mesh off the surface along its normal, in world units,
	// on top of the depth offset the renderer already applies. Worth raising only
	// where a decal on a surface seen almost edge-on still shimmers.
	Push *float32 `yaml:"push,omitempty"` // default 0.002
	// Order breaks the tie between overlapping decals: higher draws later, so on
	// top. Equal orders draw in the order they appear here.
	Order   int   `yaml:"order,omitempty"`
	Visible *bool `yaml:"visible,omitempty"` // default true when absent
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
	Texture string `yaml:"texture,omitempty"`
	// Material is the .fmat this face wears, by name, or empty for none. What
	// the material says is overlaid on the fields here rather than replacing
	// them, so a face can carry a material for its scroll and still name its
	// own texture — see internal/material and gfx.SurfaceState.
	Material     string     `yaml:"material,omitempty"`
	BlendTexture string     `yaml:"blend_texture,omitempty"`
	BlendMask    string     `yaml:"blend_mask,omitempty"`
	UVOffset     geom.Point `yaml:"uv_offset,omitempty"`
	UVScale      geom.Point `yaml:"uv_scale,omitempty"`
	UVRotation   float32    `yaml:"uv_rotation,omitempty"`
	// UVs are the face's per-corner texture coordinates from an atlas unwrap,
	// flattened two floats per corner in Indices' order — the same flattening
	// Verts uses. Absent on every face whose coordinates are projected from its
	// own plane, which is every face that has not been unwrapped.
	UVs []float32 `yaml:"uvs,omitempty"`
	// The override's texture and transform are saved even while it is off, so
	// toggling it back on returns the placement it had.
	TextureOverride    bool       `yaml:"texture_override,omitempty"`
	OverrideTexture    string     `yaml:"override_texture,omitempty"`
	OverrideUVOffset   geom.Point `yaml:"override_uv_offset,omitempty"`
	OverrideUVScale    geom.Point `yaml:"override_uv_scale,omitempty"`
	OverrideUVRotation float32    `yaml:"override_uv_rotation,omitempty"`
	Smooth             bool       `yaml:"smooth,omitempty"`
	SmoothAngle        float32    `yaml:"smooth_angle,omitempty"`
	Alpha              *float32   `yaml:"alpha,omitempty"` // nil = opaque (1.0)
	DoubleSided        bool       `yaml:"double_sided,omitempty"`
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
	// AngularVelocity is radians per second added to Rotation during play —
	// Velocity's rotational counterpart, and omitted on the same terms. See
	// Chunk.AngularVelocity.
	AngularVelocity float32 `yaml:"angular_velocity,omitempty"`
	// Frame names the entity whose local space this chunk's geometry is
	// authored in — a vehicle interior, carried around the world by whatever
	// owns it. Omitted is the ordinary case: a chunk placed directly in world
	// space by its own Origin/Rotation/Scale, with no carrier.
	Frame string `yaml:"frame,omitempty"`
	// Disabled starts the chunk switched off: not drawn and not collided with,
	// though still built at load, so a script turning it back on with
	// `@id.prop.enabled = true` costs no stall. See Chunk.Disabled for why it
	// is spelled this way round.
	Disabled bool `yaml:"disabled,omitempty"`
	// UnloadWhenDisabled frees the chunk's geometry while it is switched off —
	// and never builds it at load if it starts off — trading a rebuild stall
	// on enable for the memory. See Chunk.UnloadWhenDisabled.
	UnloadWhenDisabled bool `yaml:"unload_when_disabled,omitempty"`
	// Streamed bakes the chunk into a compiled level even though it starts
	// disabled, hidden until a script switches it on. See Chunk.Streamed.
	Streamed bool `yaml:"streamed,omitempty"`
	// Moves keeps the chunk out of the compile, drawn live, because a script
	// moves it. See Chunk.Moves.
	Moves bool `yaml:"moves,omitempty"`
	// Terrain is the chunk's block terrain, if it has one: ground built from
	// blocks that the editor mines, builds on and paints biomes onto, drawn
	// as blocks or as a smooth surface. See RawTerrain.
	Terrain *RawTerrain `yaml:"terrain,omitempty"`
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

// RawVec2 is a bare {x, y} pair in YAML.
type RawVec2 struct {
	X float32 `yaml:"x"`
	Y float32 `yaml:"y"`
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
	// Wrap and Hemisphere are GlobalLight's lighting-model knobs. Pointers so
	// a level written before they existed gets the defaults
	// (DefaultLightWrap, DefaultAmbientHemisphere) and a deliberate 0 — the
	// old hard look — survives a round trip.
	Wrap       *float32 `yaml:"wrap,omitempty"`
	Hemisphere *float32 `yaml:"hemisphere,omitempty"`
	// Bounce is the light bake's inter-reflection — see GlobalLight.Bounces.
	// Absent gets the defaults; `count: 0` switches it off.
	Bounce *RawBounce `yaml:"bounce,omitempty"`
	// GI is the level's live global illumination — see GlobalLight.GI.
	// Absent gets the defaults.
	GI *RawGI `yaml:"gi,omitempty"`
}

// RawGI is GlobalLight.GI. Pointers so a deliberate 0 survives a round trip.
type RawGI struct {
	Disabled     bool     `yaml:"disabled,omitempty"`      // GI off for this level, whatever the player's setting
	Intensity    *float32 `yaml:"intensity,omitempty"`     // multiplies the bounce light; default 1
	ProbeSpacing *float32 `yaml:"probe_spacing,omitempty"` // finest probe spacing in world units, 0.5 to 16; default 2
	AmbientFloor *float32 `yaml:"ambient_floor,omitempty"` // share of the flat ambient kept under GI, 0 to 1; default 0
}

// RawBounce is GlobalLight.Bounces/BounceScale. Pointers for the same reason
// as Wrap: a deliberate 0 has to survive a round trip.
type RawBounce struct {
	Count *int     `yaml:"count,omitempty"` // bounces baked, 0 to 4; default 1
	Scale *float32 `yaml:"scale,omitempty"` // strength of each bounce; default 1
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
	Dynamic        bool     `yaml:"dynamic,omitempty"`  // lit live, not baked; see PointLight.Dynamic
	NoBake         bool     `yaml:"no_bake,omitempty"`  // static, not baked; see PointLight.NoBake
	NoGI           bool     `yaml:"no_gi,omitempty"`    // lights directly but not bounced by live GI; see PointLight.NoGI
	// BakeExclude is chunk IDs this light's bake skips, while it still lights
	// them live. See DirectionalLight.BakeExclude.
	BakeExclude []string `yaml:"bake_exclude,omitempty"`
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
	Dynamic        bool     `yaml:"dynamic,omitempty"`  // lit live, not baked; see PointLight.Dynamic
	NoBake         bool     `yaml:"no_bake,omitempty"`  // static, not baked; see PointLight.NoBake
	NoGI           bool     `yaml:"no_gi,omitempty"`    // lights directly but not bounced by live GI; see PointLight.NoGI
	// BakeExclude is chunk IDs this light's bake skips, while it still lights
	// them live. See DirectionalLight.BakeExclude.
	BakeExclude []string   `yaml:"bake_exclude,omitempty"`
	Attach      *RawAttach `yaml:"attach,omitempty"` // what it rides; see PointLight.Attach
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

// RawEffect is a placed procedural effect (effect.go).
//
// Kind is a name, on this file's rule. The kind's parameters sit in a block
// named for it, so a second kind adds a sibling block rather than
// reinterpreting the first's keys, and a level that never changed a parameter
// writes no block at all.
type RawEffect struct {
	ID            string `yaml:"id"`
	RawScriptData `yaml:",inline"`
	Kind          string  `yaml:"kind"`
	Position      RawVec3 `yaml:"position"`
	Rotation      RawVec3 `yaml:"rotation,omitempty"`

	Enabled   *bool    `yaml:"enabled,omitempty"`    // default true when absent
	Playing   *bool    `yaml:"playing,omitempty"`    // default true when absent
	Loop      *bool    `yaml:"loop,omitempty"`       // default true when absent
	LoopDelay *float32 `yaml:"loop_delay,omitempty"` // default 1

	Explosion *RawExplosion `yaml:"explosion,omitempty"`

	Attach *RawAttach `yaml:"attach,omitempty"`
}

// RawExplosion is the explosion kind's parameters. Every key is a pointer, so
// an absent one takes world.DefaultExplosion's value rather than zero.
type RawExplosion struct {
	Radius          *float32  `yaml:"radius,omitempty"`
	Duration        *float32  `yaml:"duration,omitempty"`
	Brightness      *float32  `yaml:"brightness,omitempty"`
	FireTint        *RawColor `yaml:"fire_tint,omitempty"`
	SmokeColor      *RawColor `yaml:"smoke_color,omitempty"`
	Density         *float32  `yaml:"density,omitempty"`
	Turbulence      *float32  `yaml:"turbulence,omitempty"`
	Rise            *float32  `yaml:"rise,omitempty"`
	LightColor      *RawColor `yaml:"light_color,omitempty"`
	LightBrightness *float32  `yaml:"light_brightness,omitempty"`
	LightRadius     *float32  `yaml:"light_radius,omitempty"`
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
	// AI is what an AI agent following the path does here. See WaypointAI.
	AI *RawWaypointAI `yaml:"ai,omitempty"`
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
	// Section is where a tracker song (sounds/<track>.ftrk) or a score
	// (sounds/<track>.fscore, by rehearsal mark) starts. Ignored for a
	// recording.
	Section string `yaml:"section,omitempty"`
	// Params sets a tracker song's or a score's params (0..1) before it
	// starts.
	Params map[string]float32 `yaml:"params,omitempty"`
}

// RawCompile is a level's own compile settings (Level.LightGrid). A zero
// field is the compiler's default.
type RawCompile struct {
	// LightGrid is how far apart baked light samples are, in world units:
	// faces are cut on a grid this size before lighting. A level of open
	// ground a kilometre long wants a coarser grid than a courtyard does —
	// the finer one costs every frame, in surfaces drawn and shadowed.
	LightGrid float32 `yaml:"light_grid,omitempty"`
	// LightMinGrid is the finest spacing the bake refines to where a shadow
	// edge crosses a face.
	LightMinGrid float32 `yaml:"light_min_grid,omitempty"`
	// Bake is what the light stage does: "" or "full" bakes every static
	// light, its ambient and its bounce into the surfaces; "none" skips the
	// bake, so the level is lit live (GI and every light) in a release build
	// as it is in the editor. Every light then counts against the live
	// lighting limits (4 directional, 8 point).
	Bake string `yaml:"bake,omitempty"`
}

// The values RawCompile.Bake takes.
const (
	BakeFull = "full"
	BakeNone = "none"
)

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
	// EscapeLevel, when set, is the level Escape loads during play instead of
	// putting up the pause menu — an intro or cutscene the player can skip but
	// has nothing to pause. Named without its extension, as load_level takes it.
	EscapeLevel string `yaml:"escape_level,omitempty"`
	// Compile is how this level asks to be compiled, when it wants something
	// other than the defaults. Absent for every level that does not.
	Compile *RawCompile `yaml:"compile,omitempty"`
	// SaveMode is what saving the game in this level writes: `exact` (the
	// default) saves the game as it stands, `checkpoint` saves the last
	// checkpoint the player reached — or the start of the level before any —
	// so a save cannot skip the stretch between two checkpoints.
	SaveMode string `yaml:"save_mode,omitempty"`
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
	// Trees are the level's hand-placed plants (tree.go), drawn as foliage.
	Trees []RawTree `yaml:"trees,omitempty"`
	// Decals are the level's projected textures (decal.go) — posters, stencils,
	// scorch marks. Level-scoped for one more reason than the rest: a decal's
	// mesh is clipped out of whatever chunks its box reaches, so it belongs to
	// none of them.
	Decals []RawDecal `yaml:"decals,omitempty"`
	// Sounds are the level's placed sound sources (sound.go), beside emitters
	// for the same reasons — one attached to a moving entity crosses chunks.
	Sounds []RawSound `yaml:"sounds,omitempty"`
	// Effects are the level's placed procedural effects (effect.go) —
	// explosions — beside sounds and emitters for the same reasons.
	Effects []RawEffect `yaml:"effects,omitempty"`
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
	// Gravity is the level's ambient downward pull, in world units per second
	// squared. Absent is the engine's 36, which is what every level built so
	// far falls at; 6 is roughly a moon, 0 is a station with the plating off.
	// A volume may override it in one region and a body may scale it — see
	// world/gravity.go.
	Gravity *float32 `yaml:"gravity,omitempty"`
	// ViewDistance is how far the camera sees in this level, in world units —
	// the projection's far plane. Absent (or 0) is the engine's 1000; a large
	// outdoor level raises it so distant terrain is not clipped away. Longer
	// costs depth precision at range, so raise it only as far as needed.
	ViewDistance float32   `yaml:"view_distance,omitempty"`
	Lights       RawLights `yaml:"lights"`
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
	// Models is the first-person viewmodel: hands and whatever they hold,
	// drawn in front of the camera by render.RenderViewmodel rather than
	// anywhere in the world (see player_settings.go's header). Same shape as
	// an entity type's own `models:` — the same Parent/Bone socket mounting,
	// the same Animations list — because it is the same EntityModel, just
	// read back with the camera in place of the entity's own transform.
	//
	// Unlike StepHeight this is not merged key-by-key: a level that says
	// nothing gets no viewmodel at all, and one that does replaces the whole
	// list, the same way an entity's own `models:` is never merged with
	// whatever the previous level happened to leave behind.
	Models []RawEntityModel `yaml:"models,omitempty"`
	// Script is the .flang file driving the player: what the fire buttons do,
	// what `:damage:` does to it, what dying means in this level. The same
	// three keys an entity type uses, for the same reason Models is: the
	// player is an entity, just not an authored one.
	Script     string                  `yaml:"script,omitempty"`
	Attributes map[string]RawAttribute `yaml:"attributes,omitempty"`
	Params     map[string]RawAttribute `yaml:"params,omitempty"`
	// GravityScale multiplies whatever gravity is in force where the player is
	// standing — the level's, or a volume's where one overrides it. Absent (or
	// 0) is 1. This is the same knob an entity type's `collision:` block has,
	// spelled here because the player has no entity file to put it in.
	GravityScale *float32 `yaml:"gravity_scale,omitempty"`
	// MaxFallSpeed is the terminal speed the player falls at, in units per
	// second. Absent (or 0) is the engine's 30.
	MaxFallSpeed *float32 `yaml:"max_fall_speed,omitempty"`
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
	// Material is a sky .fmat, by name. What it carries is folded into the
	// fields here when the level loads and whenever the material changes; see
	// world/sky_material.go.
	Material string `yaml:"material,omitempty"`

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
