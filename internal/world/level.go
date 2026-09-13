package world

// level.go — Level, the runtime world.
//
// The type lives apart from the loader that builds it (level_loader.go) because
// nearly everything in the engine takes a *Level and almost none of it loads
// one: the renderer, the collision resolver, the script runtime, the biome
// meshers and the editor all name this type, and none of them should have to
// depend on YAML deserialisation to do so.

// Level holds all loaded chunks, the player spawn point, and the lights.
//
// Spawn is a pointer so a level with no spawn authored stays distinguishable
// from one whose spawn happens to sit at the origin; nil means "start the
// player wherever the caller already had them".
type Level struct {
	Chunks []Chunk
	Spawn  *SpawnPoint
	// Seal is the box the level compiler treats as the inside of the level
	// (seal.go). Every loaded level has one; nil only for a level built in
	// memory, which the compiler then seals the old way — with its own walls,
	// or not at all.
	Seal *Seal
	// Scripts are the level's own .flang handlers — see RawLevel.Scripts.
	Scripts []string
	// UI are the .fui screens this level puts up when play starts — see
	// RawLevel.UI.
	UI []string
	// Volumes are the level's scriptable regions (volume.go).
	Volumes []Volume
	// Entities are the level's placed dynamic objects (entity.go). They live on
	// the level rather than in a chunk because they move — see RawLevel's note.
	Entities []Entity
	// Prefabs are the level's placements of reusable content (prefab.go). What
	// each one MAKES — a chunk, entities, volumes, lights — is materialised
	// into the lists around this one and tagged with the instance it came
	// from; this list is only the placements themselves, which is what the
	// level file stores.
	Prefabs []PrefabInstance
	// Sprites are the level's camera-facing quads (sprite.go). Level-scoped for
	// the same reason entities are — see RawLevel's note.
	Sprites []Sprite
	// Emitters are the level's placed particle effects (emitter.go).
	Emitters []Emitter
	// Scenery are the level's placed static models (scenery.go) — one mesh, a
	// transform and a collision flag, and nothing a script can address.
	Scenery []Scenery
	// Sounds are the level's placed sound sources (sound.go).
	Sounds []Sound
	// Effects are the level's placed procedural effects (effect.go).
	Effects []Effect
	// WaypointAnimations are the level's paths an object can be sent along
	// (waypoint.go, waypoint_follow.go). Drawn only in the editor.
	WaypointAnimations []WaypointAnimation
	// Music is the level's global track, or nil when it has none.
	Music *Music
	// Sky and Fog are the level's environment (sky.go, fog.go), or nil when it
	// has neither — which is what a level authored before they existed has, and
	// is why they are pointers rather than zero-valued structs.
	Sky *Sky
	Fog *Fog
	// Player is the level's tuning for the engine-owned player it starts
	// (player_settings.go). A value rather than a pointer, because there is
	// always a player and its zero value is the engine's own defaults.
	Player PlayerSettings
	Lights LightSet
	// Static is a compiled level's drawable geometry (static.go), or nil for an
	// authored one. When it is set, every chunk marked Compiled is drawn from
	// here instead of from its own brushes.
	Static  *StaticGeometry
	Spatial *SpatialHash // accelerated chunk/sector lookup; rebuilt after level load
	// Frames are the level's moving coordinate systems (frame.go). Built with
	// Spatial, from the same structural information, and refreshed every tick.
	// Nil is the world-only case and reads as "no frames", not as an error.
	Frames *FrameSet
}
