package levelio

import (
	"fmt"
	"foundry/internal/audio"
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/world"
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// level_loader.go — converts the YAML-facing Raw* level format
// (level_schema.go) into runtime state (Level, Chunk, Sector, ...), and
// back. loadRawChunk does the bulk of the work per chunk; LoadLevel and
// NewBlankLevel are the top-level entry points.

// ---------- Loader ----------

// loadRawChunk converts a single RawChunk into a runtime Chunk, uploading all
// geometry to the GPU. It returns the chunk and the set of texture names needed.
func loadRawChunk(rc world.RawChunk) (world.Chunk, map[string]bool, error) {
	texNeeded := map[string]bool{}

	sc := float32(1.0)
	if rc.Scale != 0 {
		sc = rc.Scale
	}
	origin := mgl32.Vec3{rc.Origin.X, rc.Origin.Y, rc.Origin.Z}
	chunkScript, err := world.ScriptDataFromRaw(rc.RawScriptData, "chunk "+rc.ID)
	if err != nil {
		return world.Chunk{}, nil, err
	}
	chunk := world.Chunk{
		ID:         rc.ID,
		ScriptData: chunkScript,
		Origin:     origin,
		Rotation:   rc.Rotation,
		Scale:      sc,
		Velocity:   mgl32.Vec3{rc.Velocity.X, rc.Velocity.Y, rc.Velocity.Z},
		FrameOwner: rc.Frame,
		Disabled:   rc.Disabled,
		// A chunk authored disabled is not built at all — the geometry pass
		// below is skipped and it is marked unloaded, which is exactly the
		// state ReleaseGeometry would have left it in. That is the point of
		// writing `disabled: true` into a level: the area costs nothing until
		// a script switches it on, and world.SetChunkEnabled builds it then.
		Unloaded: rc.Disabled,
	}

	for _, rb := range rc.Brushes {
		b, err := world.BrushFromRaw(rb)
		if err != nil {
			return world.Chunk{}, nil, err
		}
		if len(b.Verts) < 4 {
			return world.Chunk{}, nil, fmt.Errorf(
				"brush %q: %d verts; a solid needs at least four", b.ID, len(b.Verts))
		}
		if err := checkFaceIndices(b.ID, "", &b.BrushMesh); err != nil {
			return world.Chunk{}, nil, err
		}
		if b.Detail != nil {
			if err := checkFaceIndices(b.ID, "detail ", b.Detail); err != nil {
				return world.Chunk{}, nil, err
			}
		}
		// Both meshes' textures: the detail one is what is drawn, and the
		// hull's are what comes back the moment the detail is cleared.
		b.EachFace(func(f *world.BrushFace) {
			for _, tex := range []string{f.Texture, f.BlendTexture, f.BlendMask} {
				if tex != "" {
					texNeeded[tex] = true
				}
			}
		})
		chunk.Brushes = append(chunk.Brushes, b)
	}

	// A disabled chunk is authored, indexed and addressable but never built —
	// see Chunk.Unloaded above.
	if !rc.Disabled {
		for bi := range chunk.Brushes {
			if err := chunk.Brushes[bi].Load(); err != nil {
				return world.Chunk{}, nil, fmt.Errorf("brush %q: %w", chunk.Brushes[bi].ID, err)
			}
		}
	}
	chunk.RecomputeBounds()

	return chunk, texNeeded, nil
}

// LoadLevel reads a YAML level file, converts it to runtime types, and uploads
// all geometry to the GPU. Texture loading is driven from this function via the
// global TextureNames map and ExecutablePath variable.
func LoadLevel(levelPath string) (world.Level, error) {
	raw, err := readRawLevelFile(levelPath)
	if err != nil {
		return world.Level{}, err
	}
	return rawToLevel(raw)
}

// readRawLevelFile parses a level file into its Raw form without touching the
// GPU. Split from LoadLevel so anything that only wants to inspect a level's
// authored shape — validation, migration checks, tooling — can do so headlessly.
func readRawLevelFile(levelPath string) (world.RawLevel, error) {
	data, err := os.ReadFile(levelPath)
	if err != nil {
		return world.RawLevel{}, err
	}
	var raw world.RawLevel
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return world.RawLevel{}, err
	}
	return raw, nil
}

// blankRawLevel returns a minimal valid RawLevel: a single four-walled room
// lit by soft ambient light. Used to write a starter file from the editor's
// "New Level" dialog.
func blankRawLevel() world.RawLevel {
	return world.RawLevel{
		Chunks: []world.RawChunk{{
			ID: "default",
			// A hollow 8x8x4 room: six brushes, each a thin slab, enclosing
			// somewhere to stand. A single solid box would be a block, not a
			// room — see world.NewHollowRoom, which this mirrors.
			Brushes: blankRoomBrushes(),
		}},
		Lights: world.RawLights{
			Global: world.RawGlobalLight{Color: world.RawColor{R: 1, G: 1, B: 1}, Brightness: 0.3},
		},
	}
}

// NewBlankLevel builds a completely empty in-memory level (no chunks, and not
// backed by any file on disk) suitable for opening the
// editor to a truly blank scene. It still carries the same default ambient
// light as blankRawLevel — a level with zero lights renders everything pure
// black (see fragment.glsl: lighting starts at globalLightColor *
// globalLightBrightness and only grows from there), so geometry added via
// "New Chunk" would otherwise be invisible until a light is added by hand.
// The caller is responsible for saving it if they want to keep it.
func NewBlankLevel() (world.Level, error) {
	return rawToLevel(world.RawLevel{
		Lights: world.RawLights{
			Global: world.RawGlobalLight{Color: world.RawColor{R: 1, G: 1, B: 1}, Brightness: 0.3},
		},
	})
}

// rawToLightSet converts parsed YAML lights into runtime lights. Split out of
// rawToLevel because it touches no GPU state.
func rawToLightSet(rl world.RawLights) world.LightSet {
	var ls world.LightSet
	ls.Global = world.GlobalLight{
		Color:      mgl32.Vec3{rl.Global.Color.R, rl.Global.Color.G, rl.Global.Color.B},
		Brightness: rl.Global.Brightness,
		Disabled:   rl.Global.Disabled,
	}
	for _, rd := range rl.Directional {
		ss := rd.ShadowStrength
		if ss == 0 && rd.CastShadows {
			ss = 0.5 // default to half-strength shadows
		}
		dlScript, _ := world.ScriptDataFromRaw(rd.RawScriptData, "directional light "+rd.ID)
		dl := world.DirectionalLight{
			ID:             rd.ID,
			ScriptData:     dlScript,
			Color:          mgl32.Vec3{rd.Color.R, rd.Color.G, rd.Color.B},
			Brightness:     rd.Brightness,
			CastShadows:    rd.CastShadows,
			ShadowStrength: ss,
			Disabled:       rd.Disabled,
		}
		// Angles are authoritative when present; otherwise derive them from the
		// direction vector, which is all pre-rotation levels stored.
		if rd.Yaw != nil && rd.Pitch != nil {
			dl.Yaw, dl.Pitch = *rd.Yaw, *rd.Pitch
		} else {
			dl.Yaw, dl.Pitch = world.DirLightYawPitch(mgl32.Vec3{rd.Direction.X, rd.Direction.Y, rd.Direction.Z})
		}
		dl.SyncDirection()
		ls.Directional = append(ls.Directional, dl)
	}
	for _, rp := range rl.Point {
		ls.Points = append(ls.Points, PointLightFromRaw(rp))
	}
	// Lights predate having identity, so most levels arrive with none. Mint them
	// here rather than at every use site, so a light is addressable the moment it
	// exists.
	world.AssignLightIDs(&ls)
	return ls
}

// PointLightFromRaw converts one point light's YAML form.
//
// Split out of the loop above because a prefab stamps lights too (prefabs.go),
// and two conversions of the same eight fields would drift — the shadow-
// strength default in particular is the kind of thing that gets fixed in one
// copy.
func PointLightFromRaw(rp world.RawPointLight) world.PointLight {
	// A light that casts but says nothing about how dark gets a middling
	// default rather than none, which would read as "cast_shadows did nothing".
	ss := rp.ShadowStrength
	if ss == 0 && rp.CastShadows {
		ss = 0.5
	}
	data, _ := world.ScriptDataFromRaw(rp.RawScriptData, "point light "+rp.ID)
	return world.PointLight{
		ID:             rp.ID,
		ScriptData:     data,
		Position:       mgl32.Vec3{rp.Position.X, rp.Position.Y, rp.Position.Z},
		Color:          mgl32.Vec3{rp.Color.R, rp.Color.G, rp.Color.B},
		Brightness:     rp.Brightness,
		Radius:         rp.Radius,
		CastShadows:    rp.CastShadows,
		ShadowStrength: ss,
		Disabled:       rp.Disabled,
	}
}

// rawToLevel converts a parsed RawLevel into runtime types and uploads all
// referenced geometry/textures to the GPU.
// rawToLevel builds a runtime level from its authored form.
//
// The three expensive parts — the chunk loop, the level-scoped objects and the
// texture loads — are separate functions rather than inline, because a level
// load has two drivers: this one, which runs the whole thing in a single call,
// and levelio_load_job.go, which runs it a slice at a time so a loading screen
// can animate over it. Both call exactly these functions in exactly this order,
// which is what makes the stepped load provably the same load.
func rawToLevel(raw world.RawLevel) (world.Level, error) {
	var level world.Level
	texNeeded := map[string]bool{}

	if _, err := loadChunkRange(raw, &level, texNeeded, 0, len(raw.Chunks)); err != nil {
		return world.Level{}, err
	}
	if err := loadLevelObjects(raw, &level, texNeeded); err != nil {
		return world.Level{}, err
	}
	names := textureLoadOrder(texNeeded)
	if _, err := loadTextureRange(names, 0, len(names)); err != nil {
		return world.Level{}, err
	}
	if err := finishLevel(&level); err != nil {
		return world.Level{}, err
	}
	return level, nil
}

// loadChunkRange builds chunks [from, from+count) onto level, and reports where
// the next call should resume.
func loadChunkRange(raw world.RawLevel, level *world.Level, texNeeded map[string]bool, from, count int) (int, error) {
	to := from + count
	if to > len(raw.Chunks) {
		to = len(raw.Chunks)
	}
	for i := from; i < to; i++ {
		rc := raw.Chunks[i]
		chunk, chunkTex, err := loadRawChunk(rc)
		if err != nil {
			return i, err
		}
		for t := range chunkTex {
			texNeeded[t] = true
		}
		level.Chunks = append(level.Chunks, chunk)
	}
	return to, nil
}

// loadLevelObjects builds everything that sits at the level rather than in a
// chunk: the spawn, entities, volumes, sprites, emitters, sounds, music and
// lights. One step rather than several — together they are a fraction of a
// chunk's cost, and splitting them would buy nothing but seams.
func loadLevelObjects(raw world.RawLevel, level *world.Level, texNeeded map[string]bool) error {
	// Chunk and sector IDs are how a script addresses geometry, so they have to
	// name exactly one thing. Nothing enforced that before scripting existed, and
	// the editor could mint duplicates, so resolve them here — renaming with a
	// warning rather than rejecting, since a level authored under the old rules
	// should still open.
	level.Scripts = append([]string(nil), raw.Scripts...)
	level.UI = append([]string(nil), raw.UI...)

	world.ResolveLevelIDCollisions(level.Chunks)

	// The player spawn: one optional marker per level, not a placed object.
	if raw.Spawn != nil {
		spawn := &world.SpawnPoint{
			Position: mgl32.Vec3{raw.Spawn.Position.X, raw.Spawn.Position.Y, raw.Spawn.Position.Z},
			Rotation: mgl32.Vec3{raw.Spawn.Rotation.X, raw.Spawn.Rotation.Y, raw.Spawn.Rotation.Z},
			Frame:    raw.Spawn.Frame,
			Seat:     raw.Spawn.Seat,
		}
		if err := spawn.Load(); err != nil {
			return fmt.Errorf("spawn point load: %w", err)
		}
		level.Spawn = spawn
	}

	// Entities: blank ones authored in the level, and instances of the
	// definitions under resources/entities/.
	//
	// A bad entity fails the load rather than being skipped. An entity that
	// quietly vanished would take its attributes and its ID with it, and the
	// first sign of trouble would be a script failing to find something the
	// level plainly says is there.
	for _, re := range raw.Entities {
		e, err := world.EntityFromRaw(re, world.LoadEntityDef)
		if err != nil {
			return err
		}
		// IDs address entities from scripts, so a level with two of the same
		// gets the duplicate renamed rather than made unreachable.
		e.ID = world.UniqueEntityID(level, e.ID)
		if err := e.Load(); err != nil {
			return fmt.Errorf("entity %q load: %w", e.ID, err)
		}
		level.Entities = append(level.Entities, e)
	}

	// The geometry those entities carry (world/entity_chunk.go). Materialised
	// after every entity exists, not inside the loop above, because a chunk's
	// ID is uniqued against the level and an entity placed later could
	// otherwise take a name one placed earlier had already been given.
	//
	// Geometry that will not build is reported and skipped, unlike a bad
	// entity: the entity is what a script addresses and what carries the
	// attributes, so losing it silently would be invisible, while losing its
	// solid is a hole you can see.
	for i := range level.Entities {
		if err := MaterializeEntityGeometry(level, &level.Entities[i], texNeeded); err != nil {
			log.Printf("%v", err)
		}
	}

	// The effects those entities carry (world/entity_emitter.go), materialised
	// here for the same reason and under the same rule: after every entity
	// exists, since a materialised emitter's ID is uniqued against the level,
	// and reported rather than fatal, since a plume whose .fpar has been
	// deleted is a marker to fix and not a level that will not open.
	for i := range level.Entities {
		if err := world.MaterializeEntityEmitters(level, &level.Entities[i], texNeeded); err != nil {
			log.Printf("%v", err)
		}
	}

	// The prefab PLACEMENTS. What they make is materialised at the very end of
	// this function, once everything the level itself authored exists — see
	// there for why the order matters.
	for _, rp := range raw.Prefabs {
		inst := world.PrefabInstanceFromRaw(rp)
		inst.ID = world.UniquePrefabInstanceID(level, inst.ID)
		level.Prefabs = append(level.Prefabs, inst)
	}

	// Volumes: scriptable regions, with the same ID guarantee entities get.
	for _, rv := range raw.Volumes {
		v, err := world.VolumeFromRaw(rv)
		if err != nil {
			return err
		}
		v.ID = world.UniqueVolumeID(level, v.ID)
		if err := v.Load(); err != nil {
			// A cage that would not build is a missing authoring aid, not a
			// broken level — the volume still works.
			log.Printf("volume %q: marker load: %v", v.ID, err)
		}
		level.Volumes = append(level.Volumes, v)
	}

	// Sprites: camera-facing quads, with the same ID guarantee.
	//
	// A sprite whose texture is missing falls back to the marker rather than
	// failing the load — the same call the editor's texture picker makes when
	// it shows a name as "(missing)". A quad in the wrong texture is a visible
	// problem an author can fix; a level that will not open is not.
	for _, rs := range raw.Sprites {
		sp, err := world.SpriteFromRaw(rs)
		if err != nil {
			return err
		}
		sp.ID = world.UniqueSpriteID(level, sp.ID)
		if err := sp.EnsureSpriteTexture(); err != nil {
			log.Printf("sprite %q: texture %q: %v", sp.ID, sp.Texture, err)
		}
		level.Sprites = append(level.Sprites, sp)
	}

	// Emitters: placed particle effects, with the same ID guarantee.
	//
	// A missing or malformed .fpar is reported and the emitter kept, for the
	// reason a volume with an unbuildable cage is kept: the placement is real
	// level content and the author needs to see it to fix it. What they get is
	// a marker in the editor and an emitter that emits nothing.
	for _, rem := range raw.Emitters {
		em, err := world.EmitterFromRaw(rem)
		if err != nil {
			return err
		}
		em.ID = world.UniqueEmitterID(level, em.ID)
		if err := em.Load(); err != nil {
			log.Printf("emitter %q: effect %q: %v", em.ID, em.Effect, err)
		} else {
			for i := range em.Doc.Layers {
				texNeeded[em.Doc.Layers[i].Texture] = true
			}
		}
		level.Emitters = append(level.Emitters, em)
	}

	// Scenery: static props, with the same ID guarantee.
	//
	// A model that will not load costs the placement its mesh, not its
	// existence — the same rule an entity attachment follows. What the author
	// gets is a reported error and a prop that draws nothing, which is fixable;
	// a level that refuses to open over one missing rock is not.
	for _, rsc := range raw.Scenery {
		sc := world.SceneryFromRaw(rsc)
		sc.ID = world.UniqueSceneryID(level, sc.ID)
		if err := sc.Load(); err != nil {
			// Load's own error already names the placement and the model.
			log.Printf("%v", err)
		}
		level.Scenery = append(level.Scenery, sc)
	}

	// Sounds: placed sound sources, with the same ID guarantee.
	//
	// A missing or unreadable clip is reported and the sound kept, on exactly
	// the emitter rule above: the placement is real level content and the
	// author needs to see it in order to fix it. What they get is a marker in
	// the editor and a sound that makes none.
	//
	// Preloading here rather than on first play matters because decoding is
	// disk I/O measured in tens of milliseconds, and doing it inside the frame
	// that first triggers the sound is a visible hitch.
	for _, rs := range raw.Sounds {
		sd, err := world.SoundFromRaw(rs)
		if err != nil {
			return err
		}
		sd.ID = world.UniqueSoundID(level, sd.ID)
		if sd.Clip != "" {
			if _, err := audio.ClipFor(sd.Clip); err != nil {
				log.Printf("sound %q: clip %q: %v", sd.ID, sd.Clip, err)
			}
		}
		level.Sounds = append(level.Sounds, sd)
	}

	level.Music = world.MusicFromRaw(raw.Music)
	if level.Music != nil && level.Music.Track != "" {
		if _, err := audio.ClipFor(level.Music.Track); err != nil {
			log.Printf("music %q: %v", level.Music.Track, err)
		}
	}

	// The environment (sky.go, fog.go). No preload: the sky's six faces are
	// uploaded lazily by the pass that binds them (RenderSky), because unlike a
	// sound clip there is no first-use hitch to hide — the sky is drawn on the
	// very first frame of the level or never.
	level.Sky = world.SkyFromRaw(raw.Sky)
	level.Fog = world.FogFromRaw(raw.Fog)

	// The player's own tuning (player_settings.go). Assigned unconditionally,
	// including for a level with no `player:` block: this loader fills a Level
	// that may be carrying the previous level's settings, and a level that says
	// nothing must get the engine's defaults rather than whatever the level
	// before it happened to ask for.
	level.Player = world.PlayerSettingsFromRaw(raw.Player)

	// Convert lights
	level.Lights = rawToLightSet(raw.Lights)

	// And last of all, what the prefab instances make (world/prefab.go).
	//
	// Last because every ID a prefab stamps is uniqued against the level, and
	// the level's own names have to win: a level naming a volume `door` keeps
	// it, and the prefab's own `door` becomes `lamp3_door` and then, if it must,
	// `lamp3_door_2`. Materialising earlier would have renamed the level's own
	// object out from under the script that addresses it.
	//
	// An instance that will not load is reported and skipped, on the chunk
	// attachment's rule: a level that refuses to open over one missing prefab
	// file is worse than one that opens with a hole in it and says so.
	for i := range level.Prefabs {
		if err := MaterializePrefab(level, &level.Prefabs[i], texNeeded); err != nil {
			log.Printf("%v", err)
		}
	}
	return nil
}

// textureLoadOrder is the textures a level needs and does not already have, in
// a stable order so a stepped load makes the same progress twice running.
func textureLoadOrder(texNeeded map[string]bool) []string {
	out := make([]string, 0, len(texNeeded))
	for name := range texNeeded {
		if _, already := gfx.Textures[name]; already {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// loadTextureRange uploads names [from, from+count), and reports where to
// resume.
func loadTextureRange(names []string, from, count int) (int, error) {
	to := from + count
	if to > len(names) {
		to = len(names)
	}
	for i := from; i < to; i++ {
		name := names[i]
		if _, already := gfx.Textures[name]; already {
			continue
		}
		path, ok := gfx.TextureNames[name]
		if !ok {
			return i, fmt.Errorf("unknown texture %q — ensure a matching .png exists in textures/", name)
		}
		if err := gfx.LoadTexture(name, filepath.Join(fileutil.ExecutablePath, path)); err != nil {
			return i, err
		}
	}
	return to, nil
}

// finishLevel builds the spatial hash and the frame graph, which need every
// object in place.
func finishLevel(level *world.Level) error {
	if err := world.RebuildIndexes(level); err != nil {
		// A malformed frame graph is an authoring mistake with no sensible
		// fallback: the geometry would be drawn somewhere the collision cannot
		// find it. Refuse the level and say why.
		return err
	}
	return nil
}
func float32Default(p *float32, def float32) float32 {
	if p != nil {
		return *p
	}
	return def
}

// blankRoomBrushes is the starter level's room, in its YAML form: the six
// brushes world.NewHollowRoom would build, converted straight back to Raw so
// the file written matches what the loader would produce.
func blankRoomBrushes() []world.RawBrush {
	room := world.NewHollowRoom("room", mgl32.Vec3{-4, 0, -4}, mgl32.Vec3{4, 4, 4}, 0.5, "default")
	out := make([]world.RawBrush, 0, len(room))
	for i := range room {
		out = append(out, world.BrushToRaw(&room[i]))
	}
	return out
}

// checkFaceIndices refuses a mesh whose faces name corners it has not got —
// which would index out of range the first time anything triangulated it. Run
// over a brush's hull and over its detail mesh separately, since each indexes
// its own vertex pool.
func checkFaceIndices(brushID, what string, m *world.BrushMesh) error {
	for fi := range m.Faces {
		for _, vi := range m.Faces[fi].Indices {
			if vi < 0 || vi >= len(m.Verts) {
				return fmt.Errorf("brush %q: %sface names vertex %d, which it does not have",
					brushID, what, vi)
			}
		}
	}
	return nil
}
