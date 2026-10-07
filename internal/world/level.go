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
	// Name is the level's name as load_level takes it — its path under
	// levels/ without the extension — or "" for one built in memory. Set by
	// the loaders, not authored: it is what a save game records to find the
	// level again, and what restarting reloads.
	Name   string
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
	// EscapeLevel replaces the pause menu with a level change — see
	// RawLevel.EscapeLevel.
	EscapeLevel string
	// SaveMode is what saving the game in this level writes — see
	// RawLevel.SaveMode and savefile.
	SaveMode SaveMode
	// LightGrid and LightMinGrid are this level's own light-bake spacing, in
	// world units, where it asks for one; 0 leaves the compiler's default.
	// See RawCompile and compile.Compile.
	LightGrid, LightMinGrid float32
	// NoBake ships the level compiled but unlit by the baker: CSG, BSP,
	// visibility and sealing still run, the light stage does not, and every
	// static surface is lit live — with GI where the player has it on (render/
	// gi.go), with the flat ambient where not. See RawCompile.Bake.
	NoBake bool
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
	// Decals are the level's projected textures (decal.go). Level-scoped for the
	// volumes' reason and one more of its own: a decal's mesh is clipped out of
	// whatever chunks its box reaches, so it belongs to none of them.
	Decals []Decal
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
	// Gravity is this level's ambient downward pull, in units per second
	// squared, or nil for the engine's DefaultGravity — see gravity.go.
	//
	// A pointer rather than a 0-means-default float because zero gravity is a
	// thing a level may legitimately want, and a Level is routinely built as a
	// struct literal by tests and by the editor: those must keep falling at the
	// engine's rate, and `gravity: 0` must not be the same value as saying
	// nothing. Read through LevelGravity, never directly.
	Gravity *float32
	// ViewDistance is the camera's far plane in this level, or 0 for the
	// engine's default — see RawLevel.ViewDistance. Read through
	// LevelViewDistance.
	ViewDistance float32
	Lights       LightSet
	// Static is a compiled level's drawable geometry (static.go), or nil for an
	// authored one. When it is set, every chunk marked Compiled is drawn from
	// here instead of from its own brushes.
	Static  *StaticGeometry
	Spatial *SpatialHash // accelerated chunk/sector lookup; rebuilt after level load
	// Frames are the level's moving coordinate systems (frame.go). Built with
	// Spatial, from the same structural information, and refreshed every tick.
	// Nil is the world-only case and reads as "no frames", not as an error.
	Frames *FrameSet

	// entityIndex is FindEntity's ID lookup (entity_index.go): derived, and
	// self-validating, so nothing that edits Entities has to maintain it.
	entityIndex entityIndexSlot
}

// SaveMode is what a save made in a level holds.
type SaveMode uint8

const (
	// SaveExact saves the game as it stands, down to where the player is.
	SaveExact SaveMode = iota
	// SaveCheckpoint saves the last checkpoint the player reached instead — or
	// the start of the level, before any — so a save can never be used to
	// skip the stretch between two checkpoints a level's design rests on.
	SaveCheckpoint
)

// SaveModeNames are the YAML spellings, indexed by SaveMode.
var SaveModeNames = []string{"exact", "checkpoint"}

// SaveModeFromName reads the YAML spelling; anything unrecognised, including
// "", is SaveExact.
func SaveModeFromName(s string) SaveMode {
	for i, n := range SaveModeNames {
		if n == s {
			return SaveMode(i)
		}
	}
	return SaveExact
}

// SaveModeName is the inverse, and "" for the default so a level that never
// chose one writes nothing.
func SaveModeName(m SaveMode) string {
	if m == SaveExact || int(m) >= len(SaveModeNames) {
		return ""
	}
	return SaveModeNames[m]
}

// View distance bounds. Below MinViewDistance a level would clip its own rooms;
// past MaxViewDistance a 24-bit depth buffer has too little precision left at
// range to tell surfaces apart.
const (
	DefaultViewDistance float32 = 1000
	MinViewDistance     float32 = 50
	MaxViewDistance     float32 = 50000
)

// LevelViewDistance is how far the camera sees in lvl: its own
// `view_distance:`, clamped, or DefaultViewDistance when it names none.
func LevelViewDistance(lvl *Level) float32 {
	if lvl == nil || lvl.ViewDistance <= 0 {
		return DefaultViewDistance
	}
	return ClampViewDistance(lvl.ViewDistance)
}

// ClampViewDistance keeps a view distance inside [MinViewDistance, MaxViewDistance].
func ClampViewDistance(d float32) float32 {
	if d < MinViewDistance {
		return MinViewDistance
	}
	if d > MaxViewDistance {
		return MaxViewDistance
	}
	return d
}
