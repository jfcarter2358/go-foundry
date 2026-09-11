package levelio

// level_save.go — serialise a runtime Level back to YAML.
//
// SaveLevel reconstructs the RawLevel representation from the live runtime
// state and writes it to disk. Point names come from chunk.PointNames, which is
// index-aligned with chunk.Points; points the editor added are unnamed there and
// get auto-names "E0", "E1", … chosen to avoid names already in use.
//
// Names are bound to point indices, never to coordinates. Matching by
// coordinate meant a point the editor had moved no longer matched its recorded
// name, so it was issued a fresh one — which collided with an existing point and
// silently corrupted the level on the next load.

import (
	"fmt"
	"foundry/internal/collision"
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/render"
	"foundry/internal/script"
	"foundry/internal/world"
	"os"
	"path/filepath"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// ScanLevelFiles returns sorted filenames of level files in dir.
//
// Takes a directory rather than using LevelsDir because the editor's open
// dialog is pointed at wherever it was launched with, which is not always the
// one beside the executable.
func ScanLevelFiles(dir string) []string {
	return fileutil.ScanFilesByExt(dir, LevelExt)
}

// WriteBlankLevel writes a minimal valid level YAML to path.
func WriteBlankLevel(path string) error {
	raw := blankRawLevel()
	data, err := yaml.Marshal(&raw)
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, data)
}

// SaveLevel serialises level to YAML and writes it to path. The level is
// validated before anything touches the filesystem: a level that would not load
// back correctly is never written, so a bad save cannot destroy a good file.
func SaveLevel(level *world.Level, path string) error {
	raw, err := LevelToRaw(level)
	if err != nil {
		return err
	}
	if err := validateRawLevel(&raw); err != nil {
		return fmt.Errorf("refusing to save malformed level: %w", err)
	}
	data, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("yaml marshal: %w", err)
	}
	return fileutil.WriteFileAtomic(path, data)
}

// LevelToRaw converts the runtime Level into its serialisable form, as a level
// file stores it: a prefab is a placement, and what it made is left out.
func LevelToRaw(level *world.Level) (world.RawLevel, error) {
	return levelToRaw(level, false)
}

// LevelToRawExpanded is the same, with every prefab RESOLVED: the content a
// placement made is written out as ordinary level content and the placement
// itself is dropped.
//
// For the level compiler, and only for it. A prefab's link exists so that
// editing one file updates twenty placements, which is an authoring concern —
// by the time a level is compiled the link has done its job, and a build output
// that still had to go and read prefab files would be one that could compile
// differently from what was compiled.
func LevelToRawExpanded(level *world.Level) (world.RawLevel, error) {
	return levelToRaw(level, true)
}

// levelToRaw converts the runtime Level into its serialisable form.
//
// keepPrefabContent inverts how prefabs are written: false stores the
// placements and skips what they made (a level file), true stores what they
// made and skips the placements (a compiled level).
func levelToRaw(level *world.Level, keepPrefabContent bool) (world.RawLevel, error) {
	var raw world.RawLevel

	for ci := range level.Chunks {
		ch := &level.Chunks[ci]
		if ch.FromEntity != "" || (ch.FromPrefab != "" && !keepPrefabContent) {
			// Content materialised into this level from somewhere else: an
			// entity definition's own geometry (Chunk.FromEntity), or a prefab
			// file's (Chunk.FromPrefab). It belongs to that file and is rebuilt
			// from it on every load; writing it out would freeze a copy and
			// stop the level tracking the original.
			continue
		}
		rc, err := chunkToRaw(ch)
		if err != nil {
			return world.RawLevel{}, fmt.Errorf("chunk %q: %w", ch.ID, err)
		}
		raw.Chunks = append(raw.Chunks, rc)
	}

	if level.Spawn != nil {
		raw.Spawn = spawnToRaw(level.Spawn)
	}
	raw.Seal = world.SealToRaw(level.Seal)

	// The prefab placements themselves. What they MADE is skipped throughout
	// this function, on FromPrefab — see the chunk loop above. Both halves
	// swap over under keepPrefabContent.
	if !keepPrefabContent {
		for i := range level.Prefabs {
			raw.Prefabs = append(raw.Prefabs, world.PrefabInstanceToRaw(&level.Prefabs[i]))
		}
	}

	for i := range level.Entities {
		if level.Entities[i].FromPrefab != "" && !keepPrefabContent {
			continue
		}
		// A param bound on a carried volume lives on the level volume, which
		// the loop below skips; read it back onto the entity so it is written
		// as this placement's override (world.CarriedVolumeTemplates).
		e := level.Entities[i]
		e.Volumes = world.CarriedVolumeTemplates(level, &level.Entities[i])
		raw.Entities = append(raw.Entities, world.EntityToRaw(&e, entityDefFor(&e)))
	}

	for i := range level.Volumes {
		// A volume an entity's type hung on it is the type's content, on the
		// rule the emitters below follow — a level that wrote a copy would have
		// two of it on the next load.
		if level.Volumes[i].FromEntity != "" || (level.Volumes[i].FromPrefab != "" && !keepPrefabContent) {
			continue
		}
		raw.Volumes = append(raw.Volumes, world.VolumeToRaw(&level.Volumes[i]))
	}

	for i := range level.Sprites {
		raw.Sprites = append(raw.Sprites, world.SpriteToRaw(&level.Sprites[i]))
	}

	// An emitter an entity's type hung on it is that type's content, not the
	// level's — the rule a materialised chunk follows (Chunk.FromEntity), for
	// the same reason: a level that wrote a copy would freeze the placement and
	// stop tracking the definition. Prefab content is excluded through the same
	// flag, since a prefab's entity carries its own.
	for i := range level.Emitters {
		if level.Emitters[i].FromEntity != "" {
			continue
		}
		raw.Emitters = append(raw.Emitters, world.EmitterToRaw(&level.Emitters[i]))
	}

	for i := range level.Scenery {
		raw.Scenery = append(raw.Scenery, world.SceneryToRaw(&level.Scenery[i]))
	}

	// One-shot sounds are skipped for the reason Emitter.OneShot is never
	// serialized: a level file places lasting sounds, and the ones a script
	// fired per impact are not level content.
	for i := range level.Sounds {
		if level.Sounds[i].OneShot {
			continue
		}
		raw.Sounds = append(raw.Sounds, world.SoundToRaw(&level.Sounds[i]))
	}
	for i := range level.WaypointAnimations {
		// A path an entity's type carries is the type's, on the emitters' rule
		// above — and its points are in the entity's space, not the world's.
		if level.WaypointAnimations[i].FromEntity != "" {
			continue
		}
		raw.WaypointAnimations = append(raw.WaypointAnimations, world.WaypointAnimationToRaw(&level.WaypointAnimations[i]))
	}
	raw.Music = world.MusicToRaw(level.Music)
	raw.Sky = world.SkyToRaw(level.Sky)
	raw.Fog = world.FogToRaw(level.Fog)
	raw.Player = world.PlayerSettingsToRaw(level.Player)

	raw.Scripts = append([]string(nil), level.Scripts...)
	raw.UI = append([]string(nil), level.UI...)
	raw.Lights = lightsToRaw(level.Lights, keepPrefabContent)
	return raw, nil
}

// entityDefFor returns the definition an entity was instanced from, or nil when
// it cannot be read. Saving falls back to writing the entity in full in that
// case (see EntityToRaw), so a definition that has been deleted or renamed out
// from under a level costs a verbose entry rather than a lost one.
func entityDefFor(e *world.Entity) *world.EntityDef {
	if e.Type == "" {
		return nil
	}
	def, err := world.LoadEntityDef(e.Type)
	if err != nil {
		return nil
	}
	return def
}

// chunkToRaw converts a runtime Chunk back into its YAML form.
func chunkToRaw(ch *world.Chunk) (world.RawChunk, error) {
	rc := world.RawChunk{
		ID:            ch.ID,
		RawScriptData: world.ScriptDataToRaw(ch.ScriptData),
		Origin:        world.RawChunkOrigin{X: ch.Origin.X(), Y: ch.Origin.Y(), Z: ch.Origin.Z()},
		Rotation:      ch.Rotation,
		Scale:         ch.Scale,
		Velocity:      world.RawVec3{X: ch.Velocity.X(), Y: ch.Velocity.Y(), Z: ch.Velocity.Z()},
		Frame:         ch.FrameOwner,
		Disabled:      ch.Disabled,
	}
	rc.Brushes = make([]world.RawBrush, 0, len(ch.Brushes))
	for bi := range ch.Brushes {
		rc.Brushes = append(rc.Brushes, world.BrushToRaw(&ch.Brushes[bi]))
	}
	return rc, nil
}

// validateRawChunk checks a chunk's brushes are well-formed before it is
// written: a face may only name verts the brush actually has, and a solid
// needs at least four of them. The loader checks the same two things on the
// way in — this is the writer's half, so a level is never saved in a shape it
// could not load back.
func validateRawChunk(rc *world.RawChunk) error {
	for bi := range rc.Brushes {
		b := &rc.Brushes[bi]
		nVerts := len(b.Verts) / 3
		if nVerts < 4 {
			return fmt.Errorf("chunk %q brush %q: %d verts; a solid needs at least four",
				rc.ID, b.ID, nVerts)
		}
		if err := checkRawFaces(rc.ID, b.ID, "", nVerts, b.Faces); err != nil {
			return err
		}
		if b.Detail != nil {
			// The detail mesh indexes its OWN vertex pool, so it is checked
			// against its own count rather than the hull's.
			if err := checkRawFaces(rc.ID, b.ID, "detail ",
				len(b.Detail.Verts)/3, b.Detail.Faces); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkRawFaces(chunkID, brushID, what string, nVerts int, faces []world.RawBrushFace) error {
	for fi := range faces {
		for _, vi := range faces[fi].Indices {
			if vi < 0 || vi >= nVerts {
				return fmt.Errorf("chunk %q brush %q: %sface %d names vertex %d, which it does not have",
					chunkID, brushID, what, fi, vi)
			}
		}
	}
	return nil
}

func validateRawLevel(raw *world.RawLevel) error {
	for ci := range raw.Chunks {
		if err := validateRawChunk(&raw.Chunks[ci]); err != nil {
			return err
		}
	}
	return nil
}

// spawnToRaw converts the level's spawn point to its raw YAML form. Position
// stays in world space: the spawn belongs to the level, not to a chunk, so
// there is no chunk transform to be local to.
func spawnToRaw(s *world.SpawnPoint) *world.RawSpawn {
	return &world.RawSpawn{
		Position: world.RawVec3{X: s.Position.X(), Y: s.Position.Y(), Z: s.Position.Z()},
		Rotation: world.RawVec3{X: s.Rotation.X(), Y: s.Rotation.Y(), Z: s.Rotation.Z()},
		Frame:    s.Frame,
		Seat:     s.Seat,

		DisableControl: s.DisableControl,
		DisableGravity: s.DisableGravity,
	}
}

// lightsToRaw converts the LightSet to its raw YAML form.
func lightsToRaw(ls world.LightSet, keepPrefabContent bool) world.RawLights {
	rl := world.RawLights{
		Global: world.RawGlobalLight{
			Color:      world.RawColor{R: ls.Global.Color.X(), G: ls.Global.Color.Y(), B: ls.Global.Color.Z()},
			Brightness: ls.Global.Brightness,
			Disabled:   ls.Global.Disabled,
		},
	}
	for _, dl := range ls.Directional {
		// Re-derive so a stale/NaN Direction from an older in-memory level can
		// never be written back out.
		dl.SyncDirection()
		yaw, pitch := dl.Yaw, dl.Pitch
		rl.Directional = append(rl.Directional, world.RawDirLight{
			ID:            dl.ID,
			RawScriptData: world.ScriptDataToRaw(dl.ScriptData),
			Direction: struct {
				X float32 `yaml:"x"`
				Y float32 `yaml:"y"`
				Z float32 `yaml:"z"`
			}{dl.Direction.X(), dl.Direction.Y(), dl.Direction.Z()},
			Yaw:            &yaw,
			Pitch:          &pitch,
			Color:          world.RawColor{R: dl.Color.X(), G: dl.Color.Y(), B: dl.Color.Z()},
			Brightness:     dl.Brightness,
			CastShadows:    dl.CastShadows,
			ShadowStrength: dl.ShadowStrength,
			Disabled:       dl.Disabled,
		})
	}
	for _, pl := range ls.Points {
		if pl.FromPrefab != "" && !keepPrefabContent {
			continue // a prefab file's light; see the chunk loop in levelToRaw
		}
		rl.Point = append(rl.Point, world.RawPointLight{
			ID:            pl.ID,
			RawScriptData: world.ScriptDataToRaw(pl.ScriptData),
			Position: struct {
				X float32 `yaml:"x"`
				Y float32 `yaml:"y"`
				Z float32 `yaml:"z"`
			}{pl.Position.X(), pl.Position.Y(), pl.Position.Z()},
			Color:          world.RawColor{R: pl.Color.X(), G: pl.Color.Y(), B: pl.Color.Z()},
			Brightness:     pl.Brightness,
			Radius:         pl.Radius,
			CastShadows:    pl.CastShadows,
			ShadowStrength: pl.ShadowStrength,
			Disabled:       pl.Disabled,
		})
	}
	return rl
}
func float32PtrIfNotOne(v float32) *float32 {
	if v != 1.0 {
		return &v
	}
	return nil
}

// trimTrailingZeros returns a copy of s with trailing zero values removed.
// Returns nil if all values are zero (avoids writing empty arrays to YAML).
func trimTrailingZeros(s []float32) []float32 {
	last := -1
	for i, v := range s {
		if v != 0 {
			last = i
		}
	}
	if last < 0 {
		return nil
	}
	out := make([]float32, last+1)
	copy(out, s[:last+1])
	return out
}

// SaveChunk serialises a single chunk to a standalone YAML file.
func SaveChunk(ch *world.Chunk, path string) error {
	rc, err := chunkToRaw(ch)
	if err != nil {
		return err
	}
	if err := validateRawChunk(&rc); err != nil {
		return fmt.Errorf("refusing to save malformed chunk: %w", err)
	}
	data, err := yaml.Marshal(rc)
	if err != nil {
		return fmt.Errorf("yaml marshal: %w", err)
	}
	return fileutil.WriteFileAtomic(path, data)
}

// LoadChunkFromFile reads a standalone chunk YAML file.
func LoadChunkFromFile(path string) (world.RawChunk, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return world.RawChunk{}, err
	}
	var rc world.RawChunk
	if err := yaml.Unmarshal(data, &rc); err != nil {
		return world.RawChunk{}, err
	}
	return rc, nil
}

// ImportChunkIntoLevel loads a chunk from a YAML file and appends it to the
// level at target, in world space. It resolves ID conflicts by appending
// numeric suffixes.
//
// The imported file's own Origin is discarded in favor of target: it was
// authored relative to whatever level it came from, which has no meaning in
// this one, and keeping it would as likely as not land the import on top of
// existing geometry.
func ImportChunkIntoLevel(lvl *world.Level, path string, target mgl32.Vec3) (string, error) {
	rc, err := LoadChunkFromFile(path)
	if err != nil {
		return "", err
	}

	existingIDs := map[string]bool{}
	existingBrushIDs := map[string]bool{}
	for i := range lvl.Chunks {
		existingIDs[lvl.Chunks[i].ID] = true
		for j := range lvl.Chunks[i].Brushes {
			existingBrushIDs[lvl.Chunks[i].Brushes[j].ID] = true
		}
	}

	suffix := ""
	if existingIDs[rc.ID] {
		for n := 1; ; n++ {
			suffix = fmt.Sprintf("_%d", n)
			if !existingIDs[rc.ID+suffix] {
				break
			}
		}
		rc.ID = rc.ID + suffix
	}

	// Brush IDs are level-unique, so an imported chunk's brushes are suffixed
	// alongside the chunk itself. Nothing inside a chunk references a brush by
	// name, so unlike the sector version this replaces there is nothing to
	// remap afterwards — a brush that collides with one already in the level
	// is renamed and that is the whole of it.
	for bi := range rc.Brushes {
		id := rc.Brushes[bi].ID + suffix
		if suffix == "" && existingBrushIDs[id] {
			for n := 1; ; n++ {
				candidate := fmt.Sprintf("%s_%d", rc.Brushes[bi].ID, n)
				if !existingBrushIDs[candidate] {
					id = candidate
					break
				}
			}
		}
		rc.Brushes[bi].ID = id
		existingBrushIDs[id] = true
	}

	chunk, texNeeded, err := loadRawChunk(rc)
	if err != nil {
		return "", fmt.Errorf("load chunk %q: %w", rc.ID, err)
	}
	chunk.Origin = target

	for name := range texNeeded {
		if _, already := gfx.Textures[name]; already {
			continue
		}
		tpath, ok := gfx.TextureNames[name]
		if !ok {
			return "", fmt.Errorf("unknown texture %q — ensure a matching .png exists in textures/", name)
		}
		if err := gfx.LoadTexture(name, filepath.Join(fileutil.ResourcesPath, tpath)); err != nil {
			return "", err
		}
	}

	lvl.Chunks = append(lvl.Chunks, chunk)
	if err := world.RebuildIndexes(lvl); err != nil {
		return "", err
	}

	return rc.ID, nil
}

// applyRawLevel rebuilds lvl from a raw snapshot, in place.
//
// Split out of restoreLevelSnapshot because three different things need exactly
// this and nothing else: undo, redo, and the end of a play session
// (script_play.go). The parts they do *not* share — resetting the editor's
// selection, and moving the player to the spawn — are the caller's to decide.
func ApplyRawLevel(raw world.RawLevel, lvl *world.Level) error {
	newLevel, err := rawToLevel(raw)
	if err != nil {
		return err
	}
	// Built before the swap so a failed rebuild leaves the current level intact,
	// released after it so the outgoing entities' GPU buffers do not leak — see
	// EditorLoadLevel's note.
	collision.DeleteEntities(lvl)
	render.DeleteVolumes(lvl)
	// And the chunks' own buffers. An undo, a redo and the restore at the end
	// of a play session all come through here, so leaking here leaks the whole
	// level's geometry once per Ctrl+Z.
	world.ReleaseLevelGeometry(lvl)
	// Every voice the outgoing level held names a placement that is about to
	// stop existing. Without this, undoing the placement of a sound leaves it
	// playing forever with nothing left to switch it off — the audio version
	// of the leaked GPU buffers the two calls above exist to prevent.
	world.StopAllSounds(lvl)
	world.StopMusic(lvl, 0)
	*lvl = newLevel

	world.UploadLights(lvl.Lights)
	render.LoadVolumes(lvl)

	// Every ref a script holds now points at an object that has been
	// re-instantiated, so anything the runtime remembered about them is stale.
	script.ScriptRuntimeLoad(lvl)
	return nil
}
