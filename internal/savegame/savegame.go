// Package savegame takes a running game apart into a savefile.Snapshot and
// puts one back together.
//
// Capture reads the level, the player, the script runtime and the screen
// stack as the last tick left them. Apply lays a snapshot over a level that has
// just been loaded fresh from its file — by the same path load_level takes —
// so everything a save does not mention is simply as the level file says. That
// is the whole of the design, and what makes a save survive the level being
// edited after it was made: objects are matched by ID, a saved one the level
// no longer has is skipped, and a new one stays where the author put it.
//
// Three kinds of object are not a matter of overwriting fields:
//
//   - An entity in the save but not in the file was spawned by a script, and
//     is spawned again from its type under the same ID (script.SpawnForRestore).
//   - An entity or volume in the file but not in the save was destroyed, and is
//     taken out again (script.RemoveForRestore).
//   - One-shot emitters, sounds and effects — a muzzle flash, a gunshot, an
//     explosion — are left out altogether. They are over in a moment, and
//     putting a moment back is replaying it.
//
// What is not restored, deliberately: live particles, how far into its clip a
// sound was, and a crossfade in progress. All three restart, which reads as a
// cut rather than as a fault.
package savegame

import (
	"fmt"
	"log"
	"reflect"
	"time"

	"foundry/internal/ai"
	"foundry/internal/flang"
	"foundry/internal/gfx"
	"foundry/internal/platform"
	"foundry/internal/savefile"
	"foundry/internal/script"
	"foundry/internal/ui"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// loadingScreen is the screen a level load puts up, which a save must not
// record as one the game had up.
const loadingScreen = "loading"

// Capture takes the running game apart.
func Capture(lvl *world.Level, kind savefile.Kind, checkpoint string) *savefile.Snapshot {
	s := &savefile.Snapshot{
		Header: savefile.Header{
			Level:      lvl.Name,
			Kind:       kind,
			Checkpoint: checkpoint,
			Saved:      time.Now(),
			PlayTime:   script.PlayTime(),
		},
	}
	s.World = captureWorld(lvl)
	s.Player = capturePlayer()
	s.UI = captureScreens()
	keep := map[string]bool{}
	for _, sc := range s.UI {
		keep[sc.ID] = true
	}
	s.Script = script.CaptureScriptState(keep)
	return s
}

// Fresh is a save of the start of a level: nothing but its name.
func Fresh(level string, kind savefile.Kind) *savefile.Snapshot {
	return &savefile.Snapshot{
		Header: savefile.Header{Level: level, Kind: kind, Saved: time.Now()},
		Fresh:  true,
	}
}

// ---------- capture ----------

func captureWorld(lvl *world.Level) savefile.WorldState {
	ws := savefile.WorldState{
		GlobalLight:  lvl.Lights.Global,
		StepHeight:   lvl.Player.StepHeight,
		Camera:       lvl.Player.Camera,
		Gravity:      world.CopyFloatPtr(lvl.Gravity),
		ViewDistance: lvl.ViewDistance,
	}
	for i := range lvl.Entities {
		ws.Entities = append(ws.Entities, captureEntity(&lvl.Entities[i]))
	}
	for i := range lvl.Chunks {
		ws.Chunks = append(ws.Chunks, captureChunk(&lvl.Chunks[i]))
	}
	for i := range lvl.Volumes {
		ws.Volumes = append(ws.Volumes, captureVolume(&lvl.Volumes[i]))
	}
	ws.PointLights = append(ws.PointLights, lvl.Lights.Points...)
	ws.DirLights = append(ws.DirLights, lvl.Lights.Directional...)
	ws.Sprites = append(ws.Sprites, lvl.Sprites...)
	for i := range lvl.Emitters {
		e := &lvl.Emitters[i]
		if e.OneShot {
			continue
		}
		ws.Emitters = append(ws.Emitters, savefile.EmitterState{
			ID: e.ID, Tags: e.Tags, Attributes: e.Attributes, Effect: e.Effect,
			Position: e.Position, Rotation: e.Rotation, Enabled: e.Enabled, Emitting: e.Emitting,
			RateScale: e.RateScale, Tint: e.Tint, Attach: e.Attach, Waypoints: e.Waypoints,
		})
	}
	for i := range lvl.Sounds {
		s := &lvl.Sounds[i]
		if s.OneShot {
			continue
		}
		ws.Sounds = append(ws.Sounds, savefile.SoundState{
			ID: s.ID, Tags: s.Tags, Attributes: s.Attributes, Clip: s.Clip,
			Position: s.Position, Rotation: s.Rotation, Enabled: s.Enabled, Playing: s.Playing,
			Loop: s.Loop, Gain: s.Gain, Pitch: s.Pitch, Attach: s.Attach, Waypoints: s.Waypoints,
		})
	}
	for i := range lvl.Effects {
		if !lvl.Effects[i].OneShot {
			ws.Effects = append(ws.Effects, lvl.Effects[i])
		}
	}
	// Decals, less the transient ones, on the one-shot effects' rule above: a
	// mark a script left per impact is not state a save should carry, and there
	// are hundreds of them.
	for i := range lvl.Decals {
		if !lvl.Decals[i].Transient {
			ws.Decals = append(ws.Decals, bareDecal(&lvl.Decals[i]))
		}
	}
	if lvl.Sky != nil {
		sk := *lvl.Sky
		ws.Sky = &sk
	}
	if m := lvl.Music; m != nil {
		ms := &savefile.MusicState{Track: m.Track, Gain: m.Gain, Loop: m.Loop, Fade: m.Fade,
			Section: m.Section, Params: m.Params, Mutes: m.Mutes}
		if m.Song != nil {
			pos := m.Song.Position()
			ms.Order, ms.Row, ms.Playing = pos.Order, pos.Row, pos.Playing
			if s := world.MusicSection(lvl); s != "" {
				ms.Section = s
			}
		}
		if m.Score != nil {
			pos := m.Score.Position()
			ms.Tick, ms.Playing = pos.Tick, pos.Playing
			if s := world.MusicSection(lvl); s != "" {
				ms.Section = s
			}
		}
		ws.Music = ms
	}
	return ws
}

// bareDecal is a decal less its built mesh, which holds GPU buffers and is
// clipped out of the level's own geometry — so it is rebuilt on load (the first
// SyncDecals sees an unbuilt decal) rather than written into the save.
func bareDecal(d *world.Decal) world.Decal {
	b := *d
	b.Pieces = nil
	return b
}

// bareEntity is an entity less everything that is its type's rather than its
// own: the models (saved as ModelStates), and the brush, emitter, volume and
// path templates that loading and spawning materialise into the level.
func bareEntity(e *world.Entity) world.Entity {
	b := *e
	b.Models = nil
	b.Brushes = nil
	b.Emitters = nil
	b.Volumes = nil
	b.WaypointAnimations = nil
	return b
}

func captureEntity(e *world.Entity) savefile.EntityState {
	return savefile.EntityState{
		Entity:     bareEntity(e),
		Models:     captureModels(e.Models),
		HasAI:      e.AI != nil,
		HasVehicle: e.Vehicle != nil,
	}
}

func captureModels(ms []world.EntityModel) []savefile.ModelState {
	out := make([]savefile.ModelState, 0, len(ms))
	for i := range ms {
		m := &ms[i]
		st := savefile.ModelState{
			Name: m.Name, Path: m.Path, Texture: m.Texture,
			Offset: m.Offset, Rotation: m.Rotation, Scale: m.Scale,
			Alpha: m.Alpha, Visible: m.Visible,
			UVScroll: m.UVScroll, UVOffset: m.UVOffset,
		}
		if a := m.Anim; a != nil && a.Doc != nil && a.Clip >= 0 && a.Clip < len(a.Doc.Animations) {
			st.Clip = a.Doc.Animations[a.Clip].Name
			st.Time, st.Speed, st.Playing, st.Loop = a.Time, a.Speed, a.Playing, a.Loop
		}
		out = append(out, st)
	}
	return out
}

func captureChunk(ch *world.Chunk) savefile.ChunkState {
	c := *ch
	c.Brushes = nil
	// Authored ground nothing in play writes: the level it is laid over
	// already has it, so a save need not carry every height again.
	c.Terrain = nil
	// Derived every tick from what rides it, and never read back.
	c.Xform, c.FrameScale, c.Unloaded = mgl32.Mat4{}, 0, false
	cs := savefile.ChunkState{Chunk: c}
	for bi := range ch.Brushes {
		b := &ch.Brushes[bi]
		cs.Brushes = append(cs.Brushes, savefile.BrushState{
			Index: bi, ID: b.ID, Tags: b.Tags, Attributes: b.Attributes,
			Solid: b.Solid, Dynamic: b.Dynamic,
			Offset: b.Offset, Rotation: b.Rotation, Scale: b.Scale,
			Velocity: b.Velocity, AngularVelocity: b.AngularVelocity,
			Waypoints: b.Waypoints, Tweens: b.Tweens,
		})
	}
	return cs
}

func captureVolume(v *world.Volume) savefile.VolumeState {
	return savefile.VolumeState{
		ID: v.ID, FromEntity: v.FromEntity, Tags: v.Tags, Attributes: v.Attributes, Script: v.Script,
		Attach: v.Attach, Position: v.Position, Rotation: v.Rotation,
		HalfX: v.HalfX, HalfY: v.HalfY, HalfZ: v.HalfZ,
		Enabled: v.Enabled, LoadLevel: v.LoadLevel, Checkpoint: v.Checkpoint,
		Fog: v.Fog, Cloud: v.Cloud, ClearsClouds: v.ClearsClouds,
		Gravity: world.CopyFloatPtr(v.Gravity), Waypoints: v.Waypoints,
	}
}

func capturePlayer() savefile.PlayerState {
	ps := savefile.PlayerState{
		Yaw: gfx.Cam.Yaw, Pitch: gfx.Cam.Pitch,
		Control: platform.PlayerControlEnabled, Gravity: platform.GravityEnabled,
		Fly: platform.FlyMode,
	}
	if p := world.PlayerEntity; p != nil {
		ps.Entity = bareEntity(p)
		ps.Models = captureModels(p.Models)
	}
	return ps
}

// captureScreens records the screens up that belong to the game rather than to
// the moment of saving. A modal screen is a menu — the pause menu the player
// saved from, the save screen itself — and restoring one would load the game
// straight back into a menu.
func captureScreens() []savefile.ScreenState {
	var out []savefile.ScreenState
	for _, sc := range ui.Game.Screens() {
		if sc.Modal || sc.ID == loadingScreen {
			continue
		}
		st := savefile.ScreenState{ID: sc.ID}
		sc.Root.Walk(func(_, n *ui.Node) bool {
			if n.ID != "" {
				st.Nodes = append(st.Nodes, savefile.NodeState{
					ID: n.ID, Visible: n.Visible, Enabled: n.Enabled, Alpha: n.Alpha,
					Text: n.Text.Value, TextColor: n.Text.Color,
					Color: n.Style.Color, Texture: n.Style.Texture,
					Value: n.Value.Value, Min: n.Value.Min, Max: n.Value.Max, On: n.Value.On,
					FillColor: n.Value.FillColor, Attributes: n.Attributes,
				})
			}
			return true
		})
		out = append(out, st)
	}
	return out
}

// ---------- apply ----------

// Apply lays a snapshot over lvl, which must be the snapshot's level freshly
// loaded and installed — the script runtime loaded on it, the events reset, the
// level's own screens up — and not yet ticked.
//
// Problems with single objects are logged and skipped: a save that restores
// all but one crate is worth more than an error.
func Apply(lvl *world.Level, s *savefile.Snapshot) error {
	if lvl == nil || s == nil {
		return fmt.Errorf("nothing to restore")
	}
	if s.Level != lvl.Name {
		return fmt.Errorf("save is of level %q, not %q", s.Level, lvl.Name)
	}
	if s.Fresh {
		return nil
	}
	applyWorld(lvl, &s.World)
	applyPlayer(lvl, &s.Player)
	applyScreens(s.UI)
	script.RestoreScriptState(s.Script)

	// The level's indexes and frames describe where things were loaded, not
	// where the save put them.
	world.SyncChunkGeometry(lvl)
	if err := world.RebuildIndexes(lvl); err != nil {
		log.Printf("restoring %s: %v", lvl.Name, err)
	}
	world.MarkBrushMeshesChanged()
	return nil
}

func applyWorld(lvl *world.Level, ws *savefile.WorldState) {
	applyEntities(lvl, ws.Entities)

	chunks := map[string]*world.Chunk{}
	for i := range lvl.Chunks {
		chunks[lvl.Chunks[i].ID] = &lvl.Chunks[i]
	}
	for i := range ws.Chunks {
		if ch := chunks[ws.Chunks[i].Chunk.ID]; ch != nil {
			applyChunk(ch, &ws.Chunks[i])
		}
	}

	applyVolumes(lvl, ws.Volumes)

	for _, saved := range ws.PointLights {
		for i := range lvl.Lights.Points {
			if lvl.Lights.Points[i].ID == saved.ID {
				lvl.Lights.Points[i] = saved
			}
		}
	}
	for _, saved := range ws.DirLights {
		for i := range lvl.Lights.Directional {
			if lvl.Lights.Directional[i].ID == saved.ID {
				lvl.Lights.Directional[i] = saved
			}
		}
	}
	lvl.Lights.Global = ws.GlobalLight

	for _, saved := range ws.Sprites {
		if sp := findSprite(lvl, saved.ID); sp != nil {
			*sp = saved
			script.QueueTextureLoad(saved.Texture)
		}
	}
	for i := range ws.Emitters {
		if e := world.FindEmitter(lvl, ws.Emitters[i].ID); e != nil {
			applyEmitter(e, &ws.Emitters[i])
		}
	}
	for i := range ws.Sounds {
		if s := findSound(lvl, ws.Sounds[i].ID); s != nil {
			applySound(s, &ws.Sounds[i])
		}
	}
	for _, saved := range ws.Effects {
		for i := range lvl.Effects {
			if lvl.Effects[i].ID == saved.ID {
				lvl.Effects[i] = saved
			}
		}
	}
	// Decals are applied onto the fresh load OR appended, unlike everything
	// above, which only overwrites what it finds. A `decal` with `permanent` is a
	// mark a script put there deliberately — a stain that stays through the level
	// — and it exists in no level file, so a save that only updated the authored
	// ones would lose it. Its mesh is rebuilt on the next frame either way.
	for _, saved := range ws.Decals {
		if d := world.FindDecal(lvl, saved.ID); d != nil {
			*d = saved
		} else {
			lvl.Decals = append(lvl.Decals, saved)
		}
		script.QueueTextureLoad(saved.Texture)
	}

	// The sky and the fog blocks are never deleted by a script, so a nil in the
	// save is a block gob could not tell from an empty one, and the level's is
	// kept.
	if ws.Sky != nil {
		sk := *ws.Sky
		if sk.Clouds == nil && lvl.Sky != nil {
			sk.Clouds = lvl.Sky.Clouds
		}
		lvl.Sky = &sk
	}
	world.StopMusic(lvl, 0)
	lvl.Music = nil
	if m := ws.Music; m != nil {
		lvl.Music = &world.Music{Track: m.Track, Gain: m.Gain, Loop: m.Loop, Fade: m.Fade,
			Section: m.Section, Params: m.Params, Mutes: m.Mutes,
			Resume: m.Playing, ResumeOrder: m.Order, ResumeRow: m.Row, ResumeTick: m.Tick}
	}
	lvl.Player.StepHeight = ws.StepHeight
	// A save from before the camera was saved has none; the level's own
	// stands rather than being reset to first person.
	if ws.Camera != (world.PlayerCamera{}) {
		lvl.Player.Camera = ws.Camera
	}
	lvl.Gravity = world.CopyFloatPtr(ws.Gravity)
	lvl.ViewDistance = ws.ViewDistance
}

// applyEntities takes out what was destroyed, makes again what was spawned,
// and then writes every saved entity's state onto its namesake.
func applyEntities(lvl *world.Level, saved []savefile.EntityState) {
	inSave := make(map[string]bool, len(saved))
	for i := range saved {
		inSave[saved[i].Entity.ID] = true
	}
	var gone []string
	for i := range lvl.Entities {
		if !inSave[lvl.Entities[i].ID] {
			gone = append(gone, lvl.Entities[i].ID)
		}
	}
	for _, id := range gone {
		script.RemoveForRestore(lvl, flang.RefEntity, id)
	}

	for i := range saved {
		st := &saved[i]
		e := world.FindEntity(lvl, st.Entity.ID)
		if e == nil {
			if st.Entity.Type == "" {
				log.Printf("restoring: entity %q is not in the level and has no type to spawn it from", st.Entity.ID)
				continue
			}
			var err error
			if e, err = script.SpawnForRestore(lvl, st.Entity.Type, st.Entity.ID); err != nil {
				log.Printf("restoring: spawning %q as a %s: %v", st.Entity.ID, st.Entity.Type, err)
				continue
			}
		}
		applyEntity(e, st)
	}
}

// applyEntity writes an entity's saved state onto it. Only what play changes
// is written: the rest came from the entity's type and the level file, and is
// the same object it was when the save was made.
func applyEntity(e *world.Entity, st *savefile.EntityState) {
	s := &st.Entity
	e.Tags = s.Tags
	e.Position, e.Rotation, e.Scale = s.Position, s.Rotation, s.Scale
	e.Velocity, e.AngularVelocity = s.Velocity, s.AngularVelocity
	e.Tweens, e.Waypoints = s.Tweens, s.Waypoints
	e.BoundingKind = s.BoundingKind
	e.BoundHalfX, e.BoundHalfY, e.BoundHalfZ = s.BoundHalfX, s.BoundHalfY, s.BoundHalfZ
	e.BoundOffset = s.BoundOffset
	e.Script = s.Script
	e.Attributes, e.ScriptParams = s.Attributes, s.ScriptParams
	e.Collision = s.Collision
	e.Visible = s.Visible
	e.Seat, e.Frame, e.AttachedChunk = s.Seat, s.Frame, s.AttachedChunk
	e.StepOffset = 0
	// A crouch (hit_height) is play state like any other: without it a soldier
	// saved ducked behind a sandbag comes back presenting its whole body.
	e.HitHeight = s.HitHeight

	// gob writes a pointer to a zero struct as nil, so presence is saved beside
	// each of these and a present one comes back as at least a zero value.
	switch {
	case st.HasVehicle && s.Vehicle != nil:
		e.Vehicle = s.Vehicle
	case st.HasVehicle && e.Vehicle != nil:
		v := *e.Vehicle
		e.Vehicle = &v
	case !st.HasVehicle:
		e.Vehicle = nil
	}
	e.VState = s.VState
	switch {
	case st.HasAI && s.AI != nil:
		e.AI = s.AI
	case st.HasAI && e.AI == nil:
		e.AI = &world.AISpec{}
	case !st.HasAI:
		e.AI = nil
	}
	e.AIState = s.AIState
	if e.AIState != nil {
		// The load that came before this began a new session, and a state from
		// any other is thrown away on the controller's next tick.
		e.AIState.Session = ai.CurrentSession()
	}
	applyModels(e.Models, st.Models)
}

func applyModels(ms []world.EntityModel, saved []savefile.ModelState) {
	for _, st := range saved {
		for i := range ms {
			m := &ms[i]
			if m.Name != st.Name {
				continue
			}
			m.Offset, m.Rotation, m.Scale = st.Offset, st.Rotation, st.Scale
			m.Alpha, m.Visible = st.Alpha, st.Visible
			m.UVScroll, m.UVOffset = st.UVScroll, st.UVOffset
			if st.Texture != m.Texture {
				// SetTexture, not the field: an entity spawned for the restore
				// is already uploaded wearing its type's texture, and the
				// submeshes are what draw — a reskinned reinforcement came
				// back from a checkpoint in the type's colours.
				// An empty name means "the mesh's own", which only a reload
				// can put back, so that one stays a field write.
				if st.Texture != "" {
					m.SetTexture(st.Texture)
				} else {
					m.Texture = ""
				}
				script.QueueTextureLoad(st.Texture)
			}
			if st.Path != m.Path {
				log.Printf("restoring: model %q was swapped for %s; its mesh is the level's", m.Name, st.Path)
			}
			if a := m.Anim; a != nil {
				if st.Clip == "" {
					a.SetClip("", 0)
				} else if a.SetClip(st.Clip, 0) {
					a.Time, a.Speed, a.Playing, a.Loop = st.Time, st.Speed, st.Playing, st.Loop
				}
			}
		}
	}
}

func applyChunk(ch *world.Chunk, st *savefile.ChunkState) {
	s := &st.Chunk
	ch.Tags, ch.Attributes = s.Tags, s.Attributes
	ch.Origin, ch.Rotation, ch.Scale = s.Origin, s.Rotation, s.Scale
	// The bounds are the brushes' as they stood, moved ones included — the
	// cull box a script's move_to grew, which nothing re-derives on its own.
	ch.MinBound, ch.MaxBound = s.MinBound, s.MaxBound
	ch.Velocity, ch.AngularVelocity = s.Velocity, s.AngularVelocity
	ch.OriginTween, ch.RotationTween, ch.Waypoints = s.OriginTween, s.RotationTween, s.Waypoints
	// Written straight to the flag: SyncChunkGeometry, once everything is in,
	// is what notices it moved and releases or rebuilds the geometry.
	ch.Disabled = s.Disabled
	for _, b := range st.Brushes {
		if b.Index < 0 || b.Index >= len(ch.Brushes) || ch.Brushes[b.Index].ID != b.ID {
			continue
		}
		br := &ch.Brushes[b.Index]
		br.Tags, br.Attributes = b.Tags, b.Attributes
		br.Solid, br.Dynamic = b.Solid, b.Dynamic
		br.Offset, br.Rotation, br.Scale = b.Offset, b.Rotation, b.Scale
		br.Velocity, br.AngularVelocity = b.Velocity, b.AngularVelocity
		br.Waypoints, br.Tweens = b.Waypoints, b.Tweens
	}
}

func applyVolumes(lvl *world.Level, saved []savefile.VolumeState) {
	inSave := make(map[string]bool, len(saved))
	for i := range saved {
		inSave[saved[i].ID] = true
	}
	// A level volume missing from the save was destroyed. One an entity carries
	// went with its entity, or came back with it.
	var gone []string
	for i := range lvl.Volumes {
		if v := &lvl.Volumes[i]; !inSave[v.ID] && v.FromEntity == "" {
			gone = append(gone, v.ID)
		}
	}
	for _, id := range gone {
		script.RemoveForRestore(lvl, flang.RefVolume, id)
	}
	for i := range saved {
		st := &saved[i]
		for vi := range lvl.Volumes {
			v := &lvl.Volumes[vi]
			if v.ID != st.ID {
				continue
			}
			v.Tags, v.Attributes, v.Script = st.Tags, st.Attributes, st.Script
			v.Attach, v.Position, v.Rotation = st.Attach, st.Position, st.Rotation
			v.HalfX, v.HalfY, v.HalfZ = st.HalfX, st.HalfY, st.HalfZ
			v.Enabled, v.LoadLevel, v.Checkpoint = st.Enabled, st.LoadLevel, st.Checkpoint
			if st.Fog != nil {
				v.Fog = st.Fog
			}
			if st.Cloud != nil {
				v.Cloud = st.Cloud
			}
			v.ClearsClouds, v.Waypoints = st.ClearsClouds, st.Waypoints
			// Written unconditionally, unlike Fog and Cloud above: nil is a
			// meaningful value here — "this region no longer overrides gravity"
			// — where a nil fog block only ever means the save predates it.
			v.Gravity = world.CopyFloatPtr(st.Gravity)
		}
	}
}

func applyEmitter(e *world.Emitter, st *savefile.EmitterState) {
	reload := e.Effect != st.Effect || e.Tint != st.Tint || e.RateScale != st.RateScale
	e.Tags, e.Attributes = st.Tags, st.Attributes
	e.Effect, e.Position, e.Rotation = st.Effect, st.Position, st.Rotation
	e.Enabled, e.Emitting, e.RateScale, e.Tint = st.Enabled, st.Emitting, st.RateScale, st.Tint
	e.Attach, e.Waypoints = st.Attach, st.Waypoints
	if reload {
		if err := e.Load(); err != nil {
			log.Printf("restoring emitter %q: %v", e.ID, err)
		}
		return
	}
	for _, sys := range e.Systems {
		if sys != nil {
			sys.Emitting = e.Emitting
		}
	}
}

func applySound(s *world.Sound, st *savefile.SoundState) {
	s.Tags, s.Attributes = st.Tags, st.Attributes
	s.Clip, s.Position, s.Rotation = st.Clip, st.Position, st.Rotation
	s.Enabled, s.Playing, s.Loop = st.Enabled, st.Playing, st.Loop
	s.Gain, s.Pitch = st.Gain, st.Pitch
	s.Attach, s.Waypoints = st.Attach, st.Waypoints
}

func applyPlayer(lvl *world.Level, ps *savefile.PlayerState) {
	p := world.PlayerEntity
	if p == nil {
		return
	}
	s := &ps.Entity
	p.Position, p.Rotation = s.Position, s.Rotation
	p.Velocity = s.Velocity
	p.BoundHalfY = s.BoundHalfY
	p.Script = s.Script
	p.Attributes, p.ScriptParams = s.Attributes, s.ScriptParams
	p.Collision = s.Collision
	p.Visible = s.Visible
	p.Tags = s.Tags
	// Aboard: the vehicle's own occupant list came back with the vehicle's
	// state, so only the player's half of the seat is written here.
	p.Frame, p.Seat = s.Frame, s.Seat
	applyModels(p.Models, ps.Models)

	world.SetPlayerControl(ps.Control)
	world.SetPlayerFly(ps.Fly)
	world.SetPlayerGravity(ps.Gravity)
	// Both switches clear the momentum — one stops a fall, the other drops
	// everything — and the save may have been taken mid-flight.
	p.Velocity = s.Velocity
	gfx.Cam.Yaw, gfx.Cam.Pitch = ps.Yaw, ps.Pitch
	gfx.Cam.Direction = gfx.DirectionFromAngles(gfx.Cam.Yaw, gfx.Cam.Pitch)
	gfx.UpdateCamMatrix()
	platform.IsOnGround = false
	platform.JumpRequested = false
}

// applyScreens puts back the screens the game had up, and what scripts had
// written to them. A screen the level put up itself is already there and is
// only written to.
func applyScreens(saved []savefile.ScreenState) {
	for _, st := range saved {
		sc, err := ui.Game.Show(st.ID)
		if err != nil {
			log.Printf("restoring screen %q: %v", st.ID, err)
			continue
		}
		for _, ns := range st.Nodes {
			n := sc.Find(ns.ID)
			if n == nil {
				continue
			}
			n.Visible, n.Enabled, n.Alpha = ns.Visible, ns.Enabled, ns.Alpha
			// A bound label or bar is the binding's to write, and it will on the
			// next tick; writing it here would only be overwritten.
			if n.Text.Bind == "" {
				n.Text.Value = ns.Text
			}
			n.Text.Color = ns.TextColor
			n.Style.Color = ns.Color
			if n.Style.Texture != ns.Texture {
				n.Style.Texture = ns.Texture
				script.QueueTextureLoad(ns.Texture)
			}
			if n.Value.Bind == "" {
				n.Value.Value, n.Value.Min, n.Value.Max = ns.Value, ns.Min, ns.Max
			}
			n.Value.On = ns.On
			n.Value.FillColor = ns.FillColor
			n.Attributes = ns.Attributes
		}
	}
}

func findSprite(lvl *world.Level, id string) *world.Sprite {
	for i := range lvl.Sprites {
		if lvl.Sprites[i].ID == id {
			return &lvl.Sprites[i]
		}
	}
	return nil
}

func findSound(lvl *world.Level, id string) *world.Sound {
	for i := range lvl.Sounds {
		if lvl.Sounds[i].ID == id {
			return &lvl.Sounds[i]
		}
	}
	return nil
}

// ---------- checking ----------

// Diff lists where two snapshots of the same level disagree, object by object
// — what a save put back and what came back differently. For tools and tests:
// a snapshot taken straight after restoring another should have none.
func Diff(a, b *savefile.Snapshot) []string {
	var out []string
	note := func(what string, x, y any) {
		if !reflect.DeepEqual(x, y) {
			out = append(out, fmt.Sprintf("%s:\n    saved    %+v\n    restored %+v", what, x, y))
		}
	}
	entities := map[string]savefile.EntityState{}
	for _, e := range b.World.Entities {
		entities[e.Entity.ID] = e
	}
	for _, e := range a.World.Entities {
		r, ok := entities[e.Entity.ID]
		if !ok {
			out = append(out, "entity "+e.Entity.ID+" did not come back")
			continue
		}
		delete(entities, e.Entity.ID)
		note("entity "+e.Entity.ID, e, r)
	}
	for id := range entities {
		out = append(out, "entity "+id+" came back but was not saved")
	}
	chunks := map[string]savefile.ChunkState{}
	for _, c := range b.World.Chunks {
		chunks[c.Chunk.ID] = c
	}
	for _, c := range a.World.Chunks {
		note("chunk "+c.Chunk.ID, c, chunks[c.Chunk.ID])
	}
	volumes := map[string]savefile.VolumeState{}
	for _, v := range b.World.Volumes {
		volumes[v.ID] = v
	}
	for _, v := range a.World.Volumes {
		note("volume "+v.ID, v, volumes[v.ID])
	}
	note("point lights", a.World.PointLights, b.World.PointLights)
	note("directional lights", a.World.DirLights, b.World.DirLights)
	note("sprites", a.World.Sprites, b.World.Sprites)
	note("emitters", a.World.Emitters, b.World.Emitters)
	note("sounds", a.World.Sounds, b.World.Sounds)
	note("effects", a.World.Effects, b.World.Effects)
	note("decals", a.World.Decals, b.World.Decals)
	note("sky", a.World.Sky, b.World.Sky)
	note("music", a.World.Music, b.World.Music)
	note("player", a.Player, b.Player)
	note("script state", a.Script, b.Script)
	note("screens", a.UI, b.UI)
	return out
}
