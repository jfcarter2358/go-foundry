package levelio

import (
	"fmt"
	"foundry/internal/audio"
	"foundry/internal/compile"
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/score"
	"foundry/internal/score/play"
	"foundry/internal/tracker"
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
		ID:              rc.ID,
		ScriptData:      chunkScript,
		Origin:          origin,
		Rotation:        rc.Rotation,
		Scale:           sc,
		Velocity:        mgl32.Vec3{rc.Velocity.X, rc.Velocity.Y, rc.Velocity.Z},
		AngularVelocity: rc.AngularVelocity,
		FrameOwner:      rc.Frame,
		Disabled:        rc.Disabled,

		UnloadWhenDisabled: rc.UnloadWhenDisabled,
		Streamed:           rc.Streamed,
		Moves:              rc.Moves,
	}
	// Every chunk is built here, behind the loading screen, whether it starts
	// switched on or not: a script switching an area on mid-play then costs an
	// index rebuild rather than a stall. The exception is a chunk that opted
	// into unloading while disabled — it is left in exactly the state
	// ReleaseGeometry would have, and SyncChunkGeometry builds it on enable.
	chunk.Unloaded = !chunk.WantsGeometry()

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

	terrain, err := world.TerrainFromRaw(rc.Terrain)
	if err != nil {
		return world.Chunk{}, nil, fmt.Errorf("chunk %q: %w", rc.ID, err)
	}
	chunk.Terrain = terrain
	if terrain != nil {
		for _, tex := range terrain.Layers {
			if tex != "" {
				texNeeded[tex] = true
			}
		}
	}

	// See Chunk.Unloaded above.
	if !chunk.Unloaded {
		for bi := range chunk.Brushes {
			if err := chunk.Brushes[bi].Load(); err != nil {
				return world.Chunk{}, nil, fmt.Errorf("brush %q: %w", chunk.Brushes[bi].ID, err)
			}
		}
	}
	chunk.RecomputeBounds()

	return chunk, texNeeded, nil
}

// InstallPlayerModels puts a level's player block Models — the first-person
// viewmodel — onto world.PlayerEntity, replacing whatever it carried, nil
// included, and uploads them.
//
// world.PlayerEntity is engine-owned rather than part of a Level (see
// entity.go's header), so this is the one place anything copies a level's
// player block onto it. The loader calls it; so does the app's startup, which
// loads its first level BEFORE the player exists — without that second call a
// release build booting straight into a level had a player with no `hands`,
// and every script addressing `@player.model.hands` failed. A nil PlayerEntity
// (tests, tools) is left alone.
func InstallPlayerModels(level *world.Level) error {
	if world.PlayerEntity == nil || level == nil {
		return nil
	}
	world.PlayerEntity.Delete()
	world.PlayerEntity.Models = append([]world.EntityModel(nil), level.Player.Models...)
	if err := world.PlayerEntity.Load(); err != nil {
		return fmt.Errorf("player viewmodel load: %w", err)
	}
	return InstallPlayerBody(level)
}

// InstallPlayerBody puts a level's `player: body:` models onto the engine-owned
// third-person body (world.PlayerBodyEntity), the same unconditional way
// InstallPlayerModels does the viewmodel: a level with no body gets none.
// Hidden until the frame loop's camera shows it.
func InstallPlayerBody(level *world.Level) error {
	if level == nil {
		return nil
	}
	if world.PlayerBodyEntity == nil {
		if len(level.Player.Body.Models) == 0 {
			return nil
		}
		world.PlayerBodyEntity = world.NewPlayerBody()
	}
	b := world.PlayerBodyEntity
	b.Delete()
	b.Visible = false
	b.Models = append([]world.EntityModel(nil), level.Player.Body.Models...)
	if err := b.Load(); err != nil {
		return fmt.Errorf("player body load: %w", err)
	}
	return nil
}

// LoadLevel reads a YAML level file, converts it to runtime types, and uploads
// all geometry to the GPU. Texture loading is driven from this function via the
// global TextureNames map and ResourcesPath variable.
func LoadLevel(levelPath string) (world.Level, error) {
	raw, err := readRawLevelFile(levelPath)
	if err != nil {
		return world.Level{}, err
	}
	lvl, err := rawToLevel(raw)
	lvl.Name = LevelNameFromPath(levelPath)
	return lvl, err
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
		Color:       mgl32.Vec3{rl.Global.Color.R, rl.Global.Color.G, rl.Global.Color.B},
		Brightness:  rl.Global.Brightness,
		Disabled:    rl.Global.Disabled,
		Wrap:        world.DefaultLightWrap,
		Hemisphere:  world.DefaultAmbientHemisphere,
		Bounces:     world.DefaultBounces,
		BounceScale: world.DefaultBounceScale,
		GI:          world.DefaultGISettings(),
	}
	if g := rl.Global.GI; g != nil {
		ls.Global.GI.Disabled = g.Disabled
		if g.Intensity != nil {
			ls.Global.GI.Intensity = max(*g.Intensity, 0)
		}
		if g.ProbeSpacing != nil {
			ls.Global.GI.ProbeSpacing = min(max(*g.ProbeSpacing, world.GIProbeSpacingMin), world.GIProbeSpacingMax)
		}
		if g.AmbientFloor != nil {
			ls.Global.GI.AmbientFloor = min(max(*g.AmbientFloor, 0), 1)
		}
	}
	if rl.Global.Wrap != nil {
		ls.Global.Wrap = *rl.Global.Wrap
	}
	if rl.Global.Hemisphere != nil {
		ls.Global.Hemisphere = *rl.Global.Hemisphere
	}
	if b := rl.Global.Bounce; b != nil {
		if b.Count != nil {
			ls.Global.Bounces = min(max(*b.Count, 0), world.MaxBounces)
		}
		if b.Scale != nil {
			ls.Global.BounceScale = max(*b.Scale, 0)
		}
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
			Dynamic:        rd.Dynamic,
			NoBake:         rd.NoBake,
			NoGI:           rd.NoGI,
			BakeExclude:    rd.BakeExclude,
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
	attach := world.AttachFromRaw(rp.Attach)
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
		// An attached light moves with its carrier, and a light that moves
		// cannot be baked — so attached is dynamic, whatever the file says.
		Dynamic:     rp.Dynamic || !attach.IsZero(),
		NoBake:      rp.NoBake,
		NoGI:        rp.NoGI,
		BakeExclude: rp.BakeExclude,
		Attach:      attach,
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
// chunk: the spawn, entities, volumes, sprites, emitters, scenery, sounds,
// music and lights.
//
// It is three pieces run back to back — the objects before the scenery, the
// scenery's own loads, the objects after — because a stepped load (LoadJob)
// runs the middle one a slice at a time: a generated level holds tens of
// thousands of placements, and loading them as one step froze the window for
// as long as that took with the bar standing still. The synchronous loader
// runs the same three in the same order, so there is still one loader.
func loadLevelObjects(raw world.RawLevel, level *world.Level, texNeeded map[string]bool) error {
	if err := loadLevelObjectsBeforeScenery(raw, level, texNeeded); err != nil {
		return err
	}
	world.LoadSceneryRange(level, world.ChunksByID(level), 0, len(level.Scenery))
	return loadLevelObjectsAfterScenery(raw, level, texNeeded)
}

// loadLevelObjectsBeforeScenery is loadLevelObjects up to the scenery: every
// placement is in the level, with its ID, and marked unloaded, and nothing has
// loaded a model yet. world.LoadSceneryRange is what loads them.
func loadLevelObjectsBeforeScenery(raw world.RawLevel, level *world.Level, texNeeded map[string]bool) error {
	// Chunk and sector IDs are how a script addresses geometry, so they have to
	// name exactly one thing. Nothing enforced that before scripting existed, and
	// the editor could mint duplicates, so resolve them here — renaming with a
	// warning rather than rejecting, since a level authored under the old rules
	// should still open.
	level.Scripts = append([]string(nil), raw.Scripts...)
	level.UI = append([]string(nil), raw.UI...)
	level.EscapeLevel = raw.EscapeLevel
	level.SaveMode = world.SaveModeFromName(raw.SaveMode)
	level.Gravity = world.CopyFloatPtr(raw.Gravity)
	level.ViewDistance = raw.ViewDistance
	if raw.Compile != nil {
		level.LightGrid, level.LightMinGrid = raw.Compile.LightGrid, raw.Compile.LightMinGrid
		level.NoBake = raw.Compile.Bake == world.BakeNone
	}

	world.ResolveLevelIDCollisions(level.Chunks)

	// The player spawn: one optional marker per level, not a placed object.
	if raw.Spawn != nil {
		spawn := &world.SpawnPoint{
			Position: mgl32.Vec3{raw.Spawn.Position.X, raw.Spawn.Position.Y, raw.Spawn.Position.Z},
			Rotation: mgl32.Vec3{raw.Spawn.Rotation.X, raw.Spawn.Rotation.Y, raw.Spawn.Rotation.Z},
			Frame:    raw.Spawn.Frame,
			Seat:     raw.Spawn.Seat,

			DisableControl: raw.Spawn.DisableControl,
			DisableGravity: raw.Spawn.DisableGravity,
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
	//
	// Every entity is resolved from its definition first, so that the models
	// they all wear — with the props' and the player's — can be parsed on
	// every core at once before the loop below loads them one by one
	// (model_prefetch.go). A definition that will not resolve still fails the
	// load at the same point it did: after the entities before it have
	// loaded, and only if none of them failed first.
	world.ForgetUnusedModelMeshes()
	ents := make([]world.Entity, 0, len(raw.Entities))
	var resolveErr error
	for _, re := range raw.Entities {
		e, err := world.EntityFromRaw(re, world.LoadEntityDef)
		if err != nil {
			resolveErr = err
			break
		}
		ents = append(ents, e)
	}
	prefetchLevelModels(ents, raw.Scenery, raw.Player, raw.Sprites)
	for _, e := range ents {
		// IDs address entities from scripts, so a level with two of the same
		// gets the duplicate renamed rather than made unreachable.
		e.ID = world.UniqueEntityID(level, e.ID)
		if err := e.Load(); err != nil {
			return fmt.Errorf("entity %q load: %w", e.ID, err)
		}
		level.Entities = append(level.Entities, e)
	}
	if resolveErr != nil {
		return resolveErr
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

	// The volumes the level's entities carry (world/entity_volume.go). After
	// the level's own rather than beside the emitters above: IDs are uniqued
	// against everything in the level, and it is the level-authored volume a
	// script already names that should keep its ID when the two collide.
	for i := range level.Entities {
		if world.MaterializeEntityVolumes(level, &level.Entities[i]) == 0 {
			continue
		}
		if err := world.LoadEntityVolumeCages(level, level.Entities[i].ID); err != nil {
			log.Printf("%v", err)
		}
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

	// Decals: projected textures, with the same ID guarantee and the sprites'
	// missing-texture rule.
	//
	// The MESH is not built here. It is clipped out of the level's own geometry,
	// which does not exist yet — the chunks above have their brushes but a
	// compiled level's static surfaces are attached later — so the first
	// SyncDecals of the first frame builds them all, spread over a few frames by
	// its budget (world/decal_sync.go).
	for _, rd := range raw.Decals {
		d, err := world.DecalFromRaw(rd)
		if err != nil {
			return err
		}
		d.ID = world.UniqueDecalID(level, d.ID)
		if err := d.EnsureDecalTexture(); err != nil {
			log.Printf("decal %q: texture %q: %v", d.ID, d.Texture, err)
		}
		level.Decals = append(level.Decals, d)
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
	//
	// A placement streaming with a chunk that starts switched off and
	// unload_when_disabled is left unloaded, as its chunk is: the chunks are
	// already built, so SyncPlacementChunks can say which those are, and builds
	// them when the chunk comes on.
	//
	// The IDs are minted against one snapshot of the level's namespace
	// (world.LevelIDIndex) rather than by UniqueSceneryID per placement,
	// which rescans every list each time: a generated level holds tens of
	// thousands of placements, and that was quadratic. The trees below share
	// the snapshot, so a tree cannot take a placement's ID.
	ids := world.NewLevelIDIndex(level)
	for _, rsc := range raw.Scenery {
		sc := world.SceneryFromRaw(rsc)
		sc.ID = ids.Unique(sc.ID, "scenery")
		level.Scenery = append(level.Scenery, sc)
	}
	sceneryFrom := len(level.Scenery) - len(raw.Scenery)
	for i := sceneryFrom; i < len(level.Scenery); i++ {
		level.Scenery[i].Unloaded = true
	}

	// Trees: hand-placed plants, with the same ID guarantee. Nothing to
	// load: the foliage renderer draws them from the shared templates.
	for _, rt := range raw.Trees {
		t := world.TreeFromRaw(rt)
		t.ID = ids.Unique(t.ID, "tree")
		level.Trees = append(level.Trees, t)
	}
	// Which props and trees start out with their chunk. The props that do
	// are loaded by world.LoadSceneryRange, between this and the rest.
	world.SyncPlacementChunks(level, false)
	return nil
}

// loadLevelObjectsAfterScenery is the rest of loadLevelObjects, once the
// scenery has loaded.
func loadLevelObjectsAfterScenery(raw world.RawLevel, level *world.Level, texNeeded map[string]bool) error {
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

	// Effects: placed procedural effects, with the same ID guarantee. Nothing
	// to preload — an explosion is drawn from its parameters and the noise
	// volumes the volumetric pass already owns.
	for _, re := range raw.Effects {
		fx, err := world.EffectFromRaw(re)
		if err != nil {
			return err
		}
		fx.ID = world.UniqueEffectID(level, fx.ID)
		level.Effects = append(level.Effects, fx)
	}

	// Waypoint animations: paths a dynamic brush can be sent along, with the
	// same ID guarantee. Nothing to preload — they are points, drawn only by
	// the editor.
	for _, rw := range raw.WaypointAnimations {
		wa := world.WaypointAnimationFromRaw(rw)
		wa.ID = world.UniqueWaypointAnimationID(level, wa.ID)
		level.WaypointAnimations = append(level.WaypointAnimations, wa)
	}

	// The paths the level's entities carry (world/entity_waypoints.go). After
	// the level's own, on the carried volumes' rule: it is the level-authored
	// animation a script already names that keeps its ID when the two collide.
	for i := range level.Entities {
		world.MaterializeEntityWaypointAnimations(level, &level.Entities[i])
	}

	level.Music = world.MusicFromRaw(raw.Music)
	if level.Music != nil && level.Music.Track != "" {
		// A song preloads its samples; a score, its instruments; a
		// recording, its clip.
		if tracker.IsSong(level.Music.Track) {
			tracker.Load(level.Music.Track)
		} else if score.IsScore(level.Music.Track) {
			play.Preload(level.Music.Track)
		} else if _, err := audio.ClipFor(level.Music.Track); err != nil {
			log.Printf("music %q: %v", level.Music.Track, err)
		}
	}

	// The environment (sky.go, fog.go). No preload: the sky's six faces are
	// uploaded lazily by the pass that binds them (RenderSky), because unlike a
	// sound clip there is no first-use hitch to hide — the sky is drawn on the
	// very first frame of the level or never.
	level.Sky = world.SkyFromRaw(raw.Sky)
	level.Fog = world.FogFromRaw(raw.Fog)
	level.Tonemap = world.TonemapFromRaw(raw.Tonemap)

	// The player's own tuning (player_settings.go). Assigned unconditionally,
	// including for a level with no `player:` block: this loader fills a Level
	// that may be carrying the previous level's settings, and a level that says
	// nothing must get the engine's defaults rather than whatever the level
	// before it happened to ask for.
	player, err := world.PlayerSettingsFromRaw(raw.Player)
	if err != nil {
		return err
	}
	level.Player = player

	// The player's own first-person viewmodel, the same unconditional way. See
	// InstallPlayerModels.
	if err := InstallPlayerModels(level); err != nil {
		return err
	}

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

	// The sealing box (world/seal.go). A level that has never had one gets one
	// fitted around what it holds — here, after every chunk, entity and prefab
	// is in, so the fit sees all of it — and the next save writes it into the
	// file, from when on it is the author's to size.
	if raw.Seal != nil {
		level.Seal = world.SealFromRaw(raw.Seal)
	} else {
		level.Seal = compile.FitSeal(level)
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
			// Remembered, so that rescanning textures/ later can load it
			// without the level having to be reopened — see
			// gfx.NoteMissingTexture.
			gfx.NoteMissingTexture(name)
			return i, fmt.Errorf("unknown texture %q — ensure a matching .png exists in textures/", name)
		}
		if err := gfx.LoadTexture(name, filepath.Join(fileutil.ResourcesPath, path)); err != nil {
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
