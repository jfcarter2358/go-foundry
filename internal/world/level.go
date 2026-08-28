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
	// Scripts are the level's own .flang handlers — see RawLevel.Scripts.
	Scripts []string
	// Volumes are the level's scriptable regions (volume.go).
	Volumes []Volume
	// Entities are the level's placed dynamic objects (entity.go). They live on
	// the level rather than in a chunk because they move — see RawLevel's note.
	Entities []Entity
	// Sprites are the level's camera-facing quads (sprite.go). Level-scoped for
	// the same reason entities are — see RawLevel's note.
	Sprites []Sprite
	// Emitters are the level's placed particle effects (emitter.go).
	Emitters []Emitter
	// Sounds are the level's placed sound sources (sound.go).
	Sounds []Sound
	// Music is the level's global track, or nil when it has none.
	Music   *Music
	Lights  LightSet
	Spatial *SpatialHash // accelerated chunk/sector lookup; rebuilt after level load
	// Frames are the level's moving coordinate systems (frame.go). Built with
	// Spatial, from the same structural information, and refreshed every tick.
	// Nil is the world-only case and reads as "no frames", not as an error.
	Frames *FrameSet
}
