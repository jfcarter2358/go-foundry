package harness

// Package harness is the engine with no window: a level loaded, a real player
// body in it, and the frame ticking.
//
// It exists because every tool and test that wanted a headless level used to
// stand one up for itself — cmd/levelsim, cmd/levelcheck, a scratch test per
// investigation — and each one got some part of the setup wrong in a way that
// took a while to notice. The load has to leave the GL uploads off for the
// whole run, not just the load; the player needs the level's viewmodel or its
// own script dies on the first `@player.model.hands`; the level's screens have
// to be up before a script writes to its HUD. All of that is Open's business
// now, and the tick is internal/sim's, which is the same one app.Run runs.
//
// The CLI on top of it is cmd_sim.go (`foundry sim`). A Go test can use the
// same Session directly, which is the point of it being a package: a repro
// that used to be a scratch main can be a test that stays.

import (
	"fmt"
	"log"
	"strings"
	"time"

	"foundry/internal/ai"
	"foundry/internal/fileutil"
	"foundry/internal/flang"
	"foundry/internal/gfx"
	"foundry/internal/glctx"
	"foundry/internal/hooks"
	"foundry/internal/levelio"
	"foundry/internal/platform"
	"foundry/internal/savefile"
	"foundry/internal/savegame"
	"foundry/internal/script"
	"foundry/internal/sim"
	"foundry/internal/ui"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// Options is how a run is set up. Everything has a working default except the
// level and where the resources are.
type Options struct {
	// Resources is the install's resources/ directory. Project is the
	// directory holding it, for a caller that has the project rather than the
	// content root; Resources wins if both are set.
	Resources string
	Project   string
	// Level is the level's name, without the extension.
	Level string
	// Compiled loads the level's .fbsp, as a release build does, instead of its
	// .yaml.
	Compiled bool
	// Hz is the tick rate, 60 by default. A run is deterministic in it: two
	// runs at the same Hz do the same thing.
	Hz float64
	// ScriptsOnly runs the script half of the frame and nothing that moves a
	// body — no AI, no physics. It is much cheaper (no navigation build) and it
	// is NOT a frame; see Step.
	ScriptsOnly bool
	// God holds the player's health far over anything one tick can take.
	God bool
	// Menus says what to do about a modal screen that comes up: "answer" (the
	// default) clicks the widget its initial_focus names, once per screen, so a
	// run does not stop at the difficulty menu every level opens on; "hold"
	// leaves it up, as a player who has not touched it.
	Menus string
	// ViewW and ViewH are the framebuffer the UI solves against. A HUD widget's
	// solved position depends on it, so a run that watches one should say what
	// resolution it means; 1920x1080 by default.
	ViewW, ViewH float32
	// Draw turns the pictures on. A drawing session makes an offscreen GL
	// context, loads the level with its meshes and textures on the GPU rather
	// than headlessly, and answers Shot with a .png of what the player can
	// see. It needs a display and a GL 4.1 driver; without Draw a session is
	// blind and needs neither.
	//
	// It also pins the goroutine that opened it: see shot.go.
	Draw bool
	// ShotW and ShotH are the size of those pictures, 1280x720 by default.
	// They are also the window the projection and the UI layout are measured
	// against, so they override ViewW and ViewH.
	ShotW, ShotH int
	// ShotDir is where a picture named without a directory goes. The default
	// is a foundry-play folder in the system's temporary directory.
	ShotDir string
	// OnEvent receives everything the run has to say. The CLI prints it; a test
	// can assert on it. nil discards.
	OnEvent func(Event)
}

// Event is one thing that happened, at the second it happened.
//
// Kind is a short verb the CLI prints as a column: WATCH for a watched value
// that changed, PLAYER, INTERACT, KILL, WAKE, SCREEN, ENABLE for an action the
// run took, SAVE_GAME and friends for a request a script made, NOTE for
// anything else.
type Event struct {
	T     float64
	Kind  string
	Name  string
	Value string
}

// Session is one loaded level, ticking.
type Session struct {
	// Level is the loaded level. A caller may read it directly — that is what
	// the engine's own functions take — but should go through the Session for
	// anything that moves time.
	Level *world.Level
	// T is how far into the run we are, in seconds.
	T float64

	opts     Options
	dt       float64
	intent   mgl32.Vec3
	jump     bool
	crouch   bool
	sprint   bool
	answered map[string]bool // modal screens already clicked
	watches  []string
	last     map[string]string

	// The drawing half, nil unless Options.Draw: the offscreen target every
	// Shot is drawn into, and how many have been taken (which is what numbers
	// the unnamed ones).
	target *glctx.Target
	shots  int
}

// Open loads the level and starts play on it.
//
// The GL uploads go off and STAY off: LoadLevelHeadless turns them off for the
// load and puts them back, which is not enough here. `spawn` loads the new
// entity's model, and a model load with no context walks straight into
// glGetUniformLocation on a nil program and takes the process with it.
func Open(opts Options) (*Session, error) {
	if opts.Level == "" {
		return nil, fmt.Errorf("harness: no level named")
	}
	res := opts.Resources
	if res == "" {
		if opts.Project == "" {
			return nil, fmt.Errorf("harness: no resources directory")
		}
		res = fileutil.ResourcesDir(opts.Project)
	}
	if opts.Hz == 0 {
		opts.Hz = 60
	}
	if opts.Menus == "" {
		opts.Menus = "answer"
	}
	if opts.ViewW == 0 || opts.ViewH == 0 {
		opts.ViewW, opts.ViewH = 1920, 1080
	}
	if opts.ShotW == 0 || opts.ShotH == 0 {
		opts.ShotW, opts.ShotH = 1280, 720
	}
	fileutil.ResourcesPath = res

	// A drawing session wants the uploads ON — the whole point of it is that
	// the meshes and the textures reach the GPU — so the two kinds of session
	// set up the graphics in opposite ways. Everything else about them is the
	// same.
	gfx.GeometryUploadsEnabled, gfx.TextureUploadsEnabled = opts.Draw, opts.Draw
	if gfx.Textures == nil {
		gfx.Textures = map[string]uint32{}
	}
	gfx.TextureNames = gfx.ScanTextures(fileutil.TexturesDir())
	// The project's biome files, as app.Run loads them: a terrain painted
	// with one grows and looks as its file says, not as the built-in does.
	if _, errs := world.LoadBiomes(world.BiomesDir()); len(errs) > 0 {
		for _, err := range errs {
			log.Printf("WARNING: biome: %v", err)
		}
	}

	s := &Session{
		opts:     opts,
		dt:       1 / opts.Hz,
		answered: map[string]bool{},
		last:     map[string]string{},
	}
	if opts.Draw {
		// Before the load: the level's meshes and textures are uploaded as it
		// is read, and they need somewhere to go.
		if err := s.standUpDrawing(); err != nil {
			return nil, err
		}
	}
	if err := s.load(); err != nil {
		s.closeDrawing()
		return nil, err
	}
	return s, nil
}

// Close releases what the session holds. A blind session holds nothing and
// need not be closed; a drawing one owns a GL target and the thread its
// context is current on.
func (s *Session) Close() { s.closeDrawing() }

// Dt is the length of one tick, in seconds.
func (s *Session) Dt() float64 { return s.dt }

// load loads the level and starts play on it, the way app's installLevel does —
// once from Open, and again for every RoundTrip.
func (s *Session) load() error {
	path := levelio.LevelPath(s.opts.Level)
	var err error
	var lvl world.Level
	switch {
	case s.opts.Compiled:
		lvl, err = levelio.LoadCompiledLevel(strings.TrimSuffix(path, ".yaml") + ".fbsp")
	case s.opts.Draw:
		// The ordinary load, uploads and all: this session is going to draw
		// what it reads. LoadLevelHeadless would turn them off for the load
		// and leave every mesh on the CPU.
		lvl, err = levelio.LoadLevel(path)
	default:
		lvl, err = levelio.LoadLevelHeadless(path)
	}
	if err != nil {
		return fmt.Errorf("load %s: %w", s.opts.Level, err)
	}
	s.Level = &lvl
	s.note("loaded", fmt.Sprintf("%s: %d chunks, %d entities, %d volumes",
		s.opts.Level, len(lvl.Chunks), len(lvl.Entities), len(lvl.Volumes)))

	// A real player body, so containment — and therefore :enter:, :exit: and
	// :interact: — is decided the way the engine decides it.
	world.PlayerEntity = world.NewPlayer(mgl32.Vec3{0, 0, 0})
	// The viewmodel too, or the level's own player script fails on its first
	// `@player.model.hands` and is disabled twenty errors later — taking the
	// rest of the run's realism with it. A drawing session loads the models
	// properly a few lines down, since it has to draw them.
	world.PlayerEntity.Models = append([]world.EntityModel(nil), lvl.Player.Models...)
	if s.opts.Draw {
		if err := s.installDrawnLevel(); err != nil {
			return err
		}
	}
	// Where the level starts the player, as app/level_load.go puts them —
	// aboard a vehicle when the spawn names one. Teleport moves them on.
	world.ApplySpawnPoint(s.Level)

	// A fresh AI session per load, as a level load starts one.
	ai.ResetSession()
	// ScriptRuntimeLoad applies the level's player script as well.
	script.ScriptRuntimeLoad(s.Level)
	script.ScriptEventsReset()
	// The level's own screens, as app/level_load.go puts them up — so a script
	// writing to its HUD finds the widget there, and a watch can read it.
	ui.Game.Reset()
	for _, name := range lvl.UI {
		if _, err := ui.Game.Show(name); err != nil {
			s.note("ui", fmt.Sprintf("%s: %v", name, err))
		}
	}
	return nil
}

// Step advances the run by one tick.
//
// The tick is sim.Step — the same frame app.Run runs, in the same order, minus
// only the drawing. What that buys is that an agent walks, a body falls, a
// vehicle drives and a trigger fires because the engine decided it, not because
// the harness reimplemented the decision.
//
// With ScriptsOnly it is the script half alone: the tick, the timers, the
// messages, every verb, and the world's own easing (the tweens and the brush
// rates, which belong to the world rather than to the frame loop). Nothing
// moves a body, so an entity's velocity does not carry it and an agent does not
// walk. It is a cheaper answer to a narrower question, and it is not a frame.
func (s *Session) Step() {
	s.beforeTick()
	s.tick(sim.Input{
		Intent: s.intent,
		Jump:   s.jump,
		Crouch: s.crouch,
		Sprint: s.sprint,
	})
	s.jump = false
	s.afterTick()
}

// beforeTick is what every tick does whatever drives it: keep a god-mode
// player alive, and answer a modal screen that has come up since the last one.
func (s *Session) beforeTick() {
	if s.opts.God {
		// Far over anything one tick can take: a rocket does more than a full
		// bar, and a player who dies between two top-ups puts the game-over
		// screen up, which pauses the level and ends the run's realism as
		// surely as a crash.
		if hm, ok := world.PlayerEntity.Attributes["health_max"]; ok {
			hm.I, hm.F = 100000, 100000
			world.PlayerEntity.Attributes["health"] = hm
		}
	}
	// A modal screen that has come up since the last tick: take the default and
	// move on, unless the run asked to hold. Before the tick, so the click is
	// delivered with everything else this frame; after the screen's own :shown:
	// has had a tick to run.
	if s.opts.Menus != "hold" {
		for _, sc := range ui.Game.Screens() {
			if !sc.Modal || s.answered[sc.ID] || sc.InitialFocus == "" {
				continue
			}
			s.answered[sc.ID] = true
			hooks.RecordUIEvent(hooks.UIEvent{Atom: "click", Screen: sc.ID, Widget: sc.InitialFocus})
			s.emit(Event{Kind: "SCREEN", Name: sc.ID, Value: sc.InitialFocus})
		}
	}

}

// afterTick is the other half: the requests the tick raised, the clock, and
// the watch list.
func (s *Session) afterTick() {
	// What a save, a checkpoint, a retry or a restart would have done is the
	// app's, which this is not; that one was asked for is what a run can show.
	for _, r := range hooks.TakeSaveRequests() {
		s.emit(Event{Kind: saveRequestNames[r.Kind], Name: r.Name})
	}
	s.T += s.dt
	s.reportWatches(false)
}

// StepWith advances the run by one tick, driven by an input built elsewhere —
// a play script's, whose movement is a vector and a duration rather than the
// held keys Walk and Crouch stand for. Everything else about the tick is
// Step's: the same god-mode top-up, the same menu answering, the same watches.
func (s *Session) StepWith(in sim.Input) {
	s.beforeTick()
	s.tick(in)
	s.afterTick()
}

// tick is the frame itself, with whatever input drove it.
func (s *Session) tick(in sim.Input) {
	dt := float32(s.dt)
	if s.opts.ScriptsOnly {
		s.stepScriptsOnly(dt)
		return
	}
	sim.Step(s.Level, dt, sim.Gates{
		ViewW: s.opts.ViewW,
		ViewH: s.opts.ViewH,
	}, in)
}

// stepScriptsOnly is ScriptsOnly's tick: the script half of a frame and the
// world's own easing, with nothing that moves a body.
//
// The passes are in the frame's order, which is the only order the script tick
// is correct in: the frames a script reads are republished first, and the
// particles it fires are stepped before the tick that fires them so a burst
// lands at age 0.
func (s *Session) stepScriptsOnly(dt float32) {
	world.RefreshFrames(s.Level, s.Level.Frames)
	world.EmitterUpdate(s.Level, dt, true)
	world.EffectUpdate(s.Level, dt, true)
	ui.Game.Tick(dt, s.opts.ViewW, s.opts.ViewH)
	script.ScriptTick(s.Level, dt, true)
	// AdvanceBrushes steps both the dynamic brushes' rates AND their tweens.
	world.AdvanceChunks(s.Level, dt)
	world.AdvanceBrushes(s.Level, dt)
	// The entities' own tweens, which a frame steps inside EntityUpdate — so
	// this stands in for that pass, and is exactly why it must not run when the
	// full frame does.
	for i := range s.Level.Entities {
		s.Level.Entities[i].AdvanceTweens(s.Level.Frames, dt)
	}
}

// RunFor ticks until the run is this many seconds long.
func (s *Session) RunFor(seconds float64) {
	for s.T < seconds {
		s.Step()
	}
}

// ---------------------------------------------------------------- driving

// Teleport puts the player at a place in the world.
//
// It takes them off whatever they were riding first, or the point would be read
// in that frame's coordinates. The runtime derives :enter: and :exit: from where
// the player now is, exactly as it does in a frame — no message is faked.
func (s *Session) Teleport(p mgl32.Vec3) {
	world.PlayerEntity.Frame, world.PlayerEntity.Seat = "", ""
	world.PlayerEntity.Position = p
	s.emit(Event{Kind: "PLAYER", Value: vec(p)})
}

// Walk sets the movement the player asks for, in the player's own frame, and
// leaves it set until it is changed. This is the real move: gravity, walls,
// steps and frame portals all apply, because it goes through the same
// collision.MovePlayer a keyboard does.
//
// The vector is a displacement for ONE TICK, which is what the engine's move
// intent is (gfx.MoveIntent builds the keyboard's the same way). WalkSpeed is
// what a key would ask for, so WalkSpeed()/Hz is an ordinary walk.
func (s *Session) Walk(intent mgl32.Vec3) {
	s.intent = intent
	s.emit(Event{Kind: "PLAYER", Name: "walk", Value: vec(intent)})
}

// WalkToward points the player at a place and walks them at `speed` (metres per
// second) towards it, reporting whether they are still short of it. Call it
// each tick: it is a direction, not a path, and nothing about it avoids a wall.
func (s *Session) WalkToward(p mgl32.Vec3, speed float32) bool {
	at := s.Level.Frames.WorldPosition(world.PlayerEntity)
	d := p.Sub(at)
	d[1] = 0
	if d.Len() < 0.25 {
		s.intent = mgl32.Vec3{}
		return false
	}
	// The intent is per tick, as collision.PlayerMove reads it: DTScale is for
	// gravity and velocity, not for this.
	s.intent = d.Normalize().Mul(speed * float32(s.dt))
	return true
}

// WalkSpeed is how fast a held movement key walks, in metres per second: the
// same number gfx.MoveIntent builds the keyboard's intent from, so a scripted
// walk and a played one cover ground at the same rate.
func WalkSpeed() float32 { return platform.MovementSpeed * 60 }

// Crouch and Sprint hold the two held actions. Jump is a press, spent by the
// next tick.
func (s *Session) Crouch(held bool) { s.crouch = held }
func (s *Session) Sprint(held bool) { s.sprint = held }
func (s *Session) Jump()            { s.jump = true }

// Press asks the interact hook for a press on the next tick, which the runtime
// turns into an :interact: on the nearest thing the player is standing in — by
// the engine's own rule, not the harness's.
func (s *Session) Press() {
	hooks.ScriptInteractRequest()
	s.emit(Event{Kind: "INTERACT"})
}

// Enable switches a chunk or a volume on, standing in for the parts of the
// level the run does not play: level1's streaming cores are disabled until the
// dropship's descent script arms them twenty minutes in.
func (s *Session) Enable(id string) bool {
	found := false
	for i := range s.Level.Chunks {
		if s.Level.Chunks[i].ID == id {
			s.Level.Chunks[i].Disabled, found = false, true
		}
	}
	for i := range s.Level.Volumes {
		if s.Level.Volumes[i].ID == id {
			s.Level.Volumes[i].Enabled, found = true, true
		}
	}
	if !found {
		s.emit(Event{Kind: "ENABLE", Name: id, Value: "no such chunk or volume"})
		return false
	}
	s.emit(Event{Kind: "ENABLE", Name: id})
	return true
}

// Kill sends (:damage:, @player, 9999) to every scripted entity with a tag —
// what the player's gun would have done, without the aiming.
func (s *Session) Kill(tag string) int {
	n := 0
	for i := range s.Level.Entities {
		e := &s.Level.Entities[i]
		if !e.HasTag(tag) || e.Script == "" {
			continue
		}
		player := flang.RefVal{Kind: flang.RefEntity, A: "player"}
		if err := script.PostEntityEvent(e.ID, "damage", player, flang.IntVal(9999)); err != nil {
			s.emit(Event{Kind: "KILL", Name: e.ID, Value: err.Error()})
			continue
		}
		n++
	}
	s.emit(Event{Kind: "KILL", Name: "#" + tag, Value: fmt.Sprint(n)})
	return n
}

// Wake switches on the AI controller of, and shows, every entity with a tag —
// what a level's zone scripts do when the player comes near.
func (s *Session) Wake(tag string) int {
	n := 0
	for i := range s.Level.Entities {
		e := &s.Level.Entities[i]
		if e.HasTag(tag) && e.AI != nil {
			e.AI.Enabled, e.Visible = true, true
			n++
		}
	}
	s.emit(Event{Kind: "WAKE", Name: "#" + tag, Value: fmt.Sprint(n)})
	return n
}

// ---------------------------------------------------------------- watching

// Watch sets the list of things to report whenever they change. The syntax is
// read.go's.
func (s *Session) Watch(paths []string) {
	s.watches = paths
	s.last = map[string]string{}
	s.reportWatches(false)
}

// Read answers one watch path now. "?" means the path named nothing.
func (s *Session) Read(path string) string { return read(s.Level, path) }

// Dump reports every watched value whether it changed or not.
func (s *Session) Dump() { s.reportWatches(true) }

func (s *Session) reportWatches(all bool) {
	for _, w := range s.watches {
		v := read(s.Level, w)
		if v == s.last[w] {
			if all {
				s.emit(Event{Kind: "WATCH", Name: w, Value: v})
			}
			continue
		}
		s.last[w] = v
		s.emit(Event{Kind: "WATCH", Name: w, Value: v})
	}
}

// ---------------------------------------------------------------- saves

var saveRequestNames = map[hooks.SaveRequestKind]string{
	hooks.SaveToSlot: "SAVE_GAME", hooks.LoadFromSlot: "LOAD_GAME", hooks.Checkpoint: "CHECKPOINT",
	hooks.Retry: "RETRY", hooks.RestartLevel: "RESTART_LEVEL",
}

// RoundTrip saves the game, loads the level again from its file and lays the
// save over it — a checkpoint and a retry, back to back, with nothing between
// them — so a watch list shows whether anything the level's scripts were doing
// failed to come back.
func (s *Session) RoundTrip() {
	t0 := time.Now()
	data, err := savefile.Encode(savegame.Capture(s.Level, savefile.KindCheckpoint, "harness"))
	if err != nil {
		s.emit(Event{Kind: "SAVE", Value: "FAILED: " + err.Error()})
		return
	}
	saved := time.Since(t0)
	if err := s.load(); err != nil {
		s.emit(Event{Kind: "RESTORE", Value: "RELOAD FAILED: " + err.Error()})
		return
	}
	t1 := time.Now()
	st, err := savefile.Decode(data)
	if err == nil {
		err = savegame.Apply(s.Level, st)
	}
	if err != nil {
		s.emit(Event{Kind: "RESTORE", Value: "FAILED: " + err.Error()})
		return
	}
	s.emit(Event{Kind: "RESTORE", Value: fmt.Sprintf("SAVED %d bytes in %v, RESTORED in %v (load not counted)",
		len(data), saved.Round(time.Microsecond), time.Since(t1).Round(time.Microsecond))})

	// Saved again at once, the restored game should be the save it came from.
	// Both go through the file format, which is what a save and a retry see.
	again, err := savefile.Encode(savegame.Capture(s.Level, savefile.KindCheckpoint, "harness"))
	if err != nil {
		s.emit(Event{Kind: "RESTORE", Value: "RE-SAVE FAILED: " + err.Error()})
		return
	}
	s1, _ := savefile.Decode(data)
	s2, _ := savefile.Decode(again)
	for _, d := range savegame.Diff(s1, s2) {
		s.emit(Event{Kind: "DIFFERS", Value: d})
	}
}

// ---------------------------------------------------------------- events

func (s *Session) emit(e Event) {
	if s.opts.OnEvent == nil {
		return
	}
	e.T = s.T
	s.opts.OnEvent(e)
}

func (s *Session) note(name, text string) { s.emit(Event{Kind: "NOTE", Name: name, Value: text}) }
