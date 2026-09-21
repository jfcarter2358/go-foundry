// Command levelsim loads a level with no window and runs its scripts for a
// while, reporting what moved.
//
// Linting proves a script's ids resolve (cmd/levelcheck). It does not prove the
// thing actually works — that the gun winds up, fires, cools and traverses, or
// that planting a charge starts a countdown that ends in an explosion. Playing
// the game proves it, at the cost of twenty minutes per attempt. This is the
// middle: the real loader, the real script runtime, the real tick, driven from
// the command line with a watch list of properties printed as they change.
//
//	go run ./cmd/levelsim -resources <install>/resources -level level1 \
//	    -seconds 30 -watch 'aa_turret.rotation.y,aa_turret/beam.scale.x'
//
// WHAT IS AND IS NOT SIMULATED. ScriptTick is the whole script half of a frame:
// timers, message delivery, :tick:, and every verb. What is NOT here is the
// rest of the frame — physics, collision, AI stepping, rendering — so an
// entity's velocity does not move it and an AI agent does not walk. The
// property blends (`move_to`, `rotate_to`, `scale_to`) and brush integration
// ARE stepped, because those belong to the world and not to the frame loop, and
// they are what most script-driven machinery is made of.
//
// THE PLAYER IS REAL, which is what makes the triggers real. -at teleports
// world.PlayerEntity along a route, and the runtime derives `:enter:` and
// `:exit:` from where it now is exactly as it does in a frame — no message is
// faked. -press asks the interact hook for a press on the next tick, which the
// runtime turns into an `:interact:` on the nearest thing the player is
// standing in, again by the engine's own rule.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/hooks"
	"foundry/internal/levelio"
	"foundry/internal/savefile"
	"foundry/internal/savegame"
	"foundry/internal/script"
	"foundry/internal/ui"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func main() {
	res := flag.String("resources", "", "the install's resources/ directory (required)")
	name := flag.String("level", "", "level name (required)")
	seconds := flag.Float64("seconds", 20, "how long to run")
	hz := flag.Float64("hz", 60, "tick rate")
	watch := flag.String("watch", "", "comma-separated things to print when they change")
	at := flag.String("at", "", "teleport the player: 'x,y,z@seconds' separated by ';'")
	press := flag.String("press", "", "seconds at which to press interact, comma separated")
	enable := flag.String("enable", "", "chunks/volumes to switch on before the run, comma separated")
	every := flag.Float64("every", 0, "also print the whole watch list this often, in seconds")
	compiled := flag.Bool("compiled", false, "load the level's .fbsp, as a release build does, instead of its .yaml")
	restoreAt := flag.String("restore-at", "", "seconds at which to save the game, load the level again from its file and restore the save over it, comma separated")
	flag.Parse()
	if *res == "" || *name == "" {
		fmt.Fprintln(os.Stderr, "usage: levelsim -resources <dir> -level <name> [-seconds n] [-watch ...] [-send ...]")
		os.Exit(2)
	}
	fileutil.ResourcesPath = *res

	// LoadLevelHeadless turns the GL uploads off for the load and puts them
	// back. They have to stay off for the RUN too: `spawn` loads the new
	// entity's model, and a model load with no context walks straight into
	// glGetUniformLocation on a nil program and takes the process with it.
	gfx.GeometryUploadsEnabled, gfx.TextureUploadsEnabled = false, false
	if gfx.Textures == nil {
		gfx.Textures = map[string]uint32{}
	}
	gfx.TextureNames = gfx.ScanTextures(fileutil.TexturesDir())

	var lvl world.Level
	// start loads the level and starts play on it, the way installLevel does —
	// once here, and again for every -restore-at.
	start := func() {
		var err error
		if *compiled {
			lvl, err = levelio.LoadCompiledLevel(strings.TrimSuffix(levelio.LevelPath(*name), ".yaml") + ".fbsp")
		} else {
			lvl, err = levelio.LoadLevelHeadless(levelio.LevelPath(*name))
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "load:", err)
			os.Exit(1)
		}
		fmt.Printf("loaded %s: %d chunks, %d entities, %d volumes\n\n",
			*name, len(lvl.Chunks), len(lvl.Entities), len(lvl.Volumes))

		// A real player body, so containment — and therefore :enter:, :exit:
		// and :interact: — is decided the way the engine decides it.
		world.PlayerEntity = world.NewPlayer(mgl32.Vec3{0, 0, 0})
		// The viewmodel too, or the level's own player script fails on its
		// first `@player.model.hands` and is disabled twenty errors later —
		// taking the rest of the run's realism with it.
		world.PlayerEntity.Models = append([]world.EntityModel(nil), lvl.Player.Models...)

		// ScriptRuntimeLoad applies the level's player script as well.
		script.ScriptRuntimeLoad(&lvl)
		script.ScriptEventsReset()
		// The level's own screens, as app/level_load.go puts them up — so a
		// script writing to its HUD finds the widget there, and the watch list
		// can read it.
		ui.Game.Reset()
		for _, s := range lvl.UI {
			if _, err := ui.Game.Show(s); err != nil {
				fmt.Printf("UI %s: %v\n", s, err)
			}
		}
	}
	start()
	restores := parseTimes(*restoreAt)

	// Stand in for the parts of the level this harness does not run. level1's
	// streaming cores, for instance, are disabled until the dropship's descent
	// script arms them twenty minutes in; switching one on here is how the
	// streaming gets exercised without flying the whole mission first.
	for _, id := range strings.Split(*enable, ",") {
		if id = strings.TrimSpace(id); id == "" {
			continue
		}
		found := false
		for i := range lvl.Chunks {
			if lvl.Chunks[i].ID == id {
				lvl.Chunks[i].Disabled, found = false, true
			}
		}
		for i := range lvl.Volumes {
			if lvl.Volumes[i].ID == id {
				lvl.Volumes[i].Enabled, found = true, true
			}
		}
		if !found {
			fmt.Printf("ENABLE %s: no such chunk or volume\n", id)
		} else {
			fmt.Printf("ENABLE %s\n", id)
		}
	}

	watches := parseWatches(*watch)
	moves := parseMoves(*at)
	presses := parseTimes(*press)
	dt := 1.0 / *hz
	last := map[string]string{}
	nextDump := 0.0

	for t := 0.0; t < *seconds; t += dt {
		for _, m := range moves {
			if !m.done && t >= m.at {
				m.done = true
				world.PlayerEntity.Position = m.to
				fmt.Printf("%7.2f  PLAYER -> %s\n", t, vec(m.to))
			}
		}
		for _, pr := range presses {
			if !pr.done && t >= pr.at {
				pr.done = true
				hooks.ScriptInteractRequest()
				fmt.Printf("%7.2f  INTERACT\n", t)
			}
		}
		for _, r := range restores {
			if !r.done && t >= r.at {
				r.done = true
				roundTrip(&lvl, start, t)
			}
		}
		script.ScriptTick(&lvl, float32(dt), true)
		// What a save, a checkpoint, a retry or a restart would have done is
		// the app's, which this harness is not; that one was asked for is
		// what a run can show.
		for _, r := range hooks.TakeSaveRequests() {
			fmt.Printf("%7.2f  %s %s\n", t, saveRequestNames[r.Kind], r.Name)
		}
		// AdvanceBrushes steps both the dynamic brushes' rates AND their
		// tweens, so `move_to` and `scale_to` on a brush are carried here the
		// same way the frame loop carries them.
		world.AdvanceChunks(&lvl, float32(dt))
		world.AdvanceBrushes(&lvl, float32(dt))
		for i := range lvl.Entities {
			lvl.Entities[i].AdvanceTweens(lvl.Frames, float32(dt))
		}

		dump := *every > 0 && t >= nextDump
		if dump {
			nextDump = t + *every
		}
		for _, w := range watches {
			v := read(&lvl, w)
			if v != last[w] || dump {
				if v != last[w] {
					fmt.Printf("%7.2f  %-34s %s\n", t, w, v)
				}
				last[w] = v
			}
		}
		if dump {
			fmt.Printf("%7.2f  ----\n", t)
		}
	}
	fmt.Println("\ndone")
}

// ---------------------------------------------------------------- watches
//
// A watch is `<id>.<field>`, `<entity>/<brush>.<field>` for a brush an
// entity carries, or `<screen>:<widget>.<field>` for a widget on a screen that
// is up (visible, alpha, text, world_anchor). Fields are the ones that matter for machinery: position,
// rotation, scale, velocity, angular_velocity, brightness, enabled, visible —
// and, on an entity or a volume, `attr.<name>` (the state a level's scripts
// keep on their objects), plus an agent's `ai_enabled` and `ai_waypoints`.

var saveRequestNames = map[hooks.SaveRequestKind]string{
	hooks.SaveToSlot: "SAVE_GAME", hooks.LoadFromSlot: "LOAD_GAME", hooks.Checkpoint: "CHECKPOINT",
	hooks.Retry: "RETRY", hooks.RestartLevel: "RESTART_LEVEL",
}

// roundTrip saves the game, loads the level again from its file and lays the
// save over it — a checkpoint and a retry, back to back, with nothing between
// them — so a watch list shows whether anything the level's scripts were
// doing failed to come back.
func roundTrip(lvl *world.Level, start func(), t float64) {
	t0 := time.Now()
	data, err := savefile.Encode(savegame.Capture(lvl, savefile.KindCheckpoint, "levelsim"))
	if err != nil {
		fmt.Printf("%7.2f  SAVE FAILED: %v\n", t, err)
		return
	}
	saved := time.Since(t0)
	start()
	t1 := time.Now()
	s, err := savefile.Decode(data)
	if err == nil {
		err = savegame.Apply(lvl, s)
	}
	if err != nil {
		fmt.Printf("%7.2f  RESTORE FAILED: %v\n", t, err)
		return
	}
	fmt.Printf("%7.2f  SAVED %d bytes in %v, RESTORED in %v (load not counted)\n",
		t, len(data), saved.Round(time.Microsecond), time.Since(t1).Round(time.Microsecond))

	// Saved again at once, the restored game should be the save it came from.
	// Both go through the file format, which is what a save and a retry see.
	again, err := savefile.Encode(savegame.Capture(lvl, savefile.KindCheckpoint, "levelsim"))
	if err != nil {
		fmt.Printf("%7.2f  RE-SAVE FAILED: %v\n", t, err)
		return
	}
	s1, _ := savefile.Decode(data)
	s2, _ := savefile.Decode(again)
	for _, d := range savegame.Diff(s1, s2) {
		fmt.Printf("%7.2f  DIFFERS AFTER RESTORE: %s\n", t, d)
	}
}

func read(lvl *world.Level, w string) string {
	id, field, ok := strings.Cut(w, ".")
	if !ok {
		return "?"
	}
	if screen, widget, isUI := strings.Cut(id, ":"); isUI {
		return widgetField(screen, widget, field)
	}
	if ent, brush, isBrush := strings.Cut(id, "/"); isBrush {
		e := world.FindEntity(lvl, ent)
		if e == nil {
			return "no entity " + ent
		}
		for ci := range lvl.Chunks {
			ch := &lvl.Chunks[ci]
			for bi := range ch.Brushes {
				b := &ch.Brushes[bi]
				if b.ID == ent+"."+brush || b.ID == brush {
					return brushField(ch, b, field)
				}
			}
		}
		return "no brush " + brush
	}
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if ch.ID == id {
			switch field {
			case "enabled":
				return fmt.Sprint(!ch.Disabled)
			case "origin":
				return vec(ch.Origin)
			}
		}
		for bi := range ch.Brushes {
			if ch.Brushes[bi].ID == id {
				return brushField(ch, &ch.Brushes[bi], field)
			}
		}
	}
	// `<entity>#<attachment>.<field>` — a model attachment, which is where a
	// beam that has to be re-skinned has to live (a brush has no texture).
	if ent, att, isModel := strings.Cut(id, "#"); isModel {
		e := world.FindEntity(lvl, ent)
		if e == nil {
			return "no entity " + ent
		}
		m := e.Model(att)
		if m == nil {
			return "no attachment " + att
		}
		switch field {
		case "texture":
			return m.Texture
		case "visible":
			return fmt.Sprint(m.Visible)
		case "scale":
			return vec(m.Scale)
		case "scale.x":
			return num(m.Scale.X())
		case "alpha":
			return num(m.Alpha)
		}
		return "?"
	}
	if e := world.FindEntity(lvl, id); e != nil {
		switch field {
		case "position":
			return vec(e.Position)
		case "rotation":
			return vec(e.Rotation)
		case "scale":
			return vec(e.Scale)
		case "visible":
			return fmt.Sprint(e.Visible)
		case "rotation.y":
			return num(e.Rotation.Y())
		case "ai_enabled":
			return fmt.Sprint(e.AI != nil && e.AI.Enabled)
		case "ai_waypoints":
			if e.AIState == nil || e.AIState.Route == "" {
				return "none"
			}
			return e.AIState.Route
		}
		// `attr.<name>`: the state a level's scripts keep on their objects.
		if name, isAttr := strings.CutPrefix(field, "attr."); isAttr {
			return attrField(e.Attributes, name)
		}
	}
	if v := world.FindVolume(lvl, id); v != nil {
		switch field {
		case "enabled":
			return fmt.Sprint(v.Enabled)
		}
		if name, isAttr := strings.CutPrefix(field, "attr."); isAttr {
			return attrField(v.Attributes, name)
		}
	}
	for i := range lvl.Lights.Points {
		p := &lvl.Lights.Points[i]
		if p.ID != id {
			continue
		}
		switch field {
		case "brightness":
			return num(p.Brightness)
		case "enabled":
			return fmt.Sprint(!p.Disabled)
		case "position":
			return vec(p.Position)
		case "color":
			return vec(p.Color)
		}
	}
	return "?"
}

func widgetField(screen, widget, field string) string {
	sc := ui.Game.Find(screen)
	if sc == nil {
		return "screen " + screen + " not up"
	}
	n := sc.Root.Find(widget)
	if n == nil {
		return "no widget " + widget
	}
	switch field {
	case "visible":
		return fmt.Sprint(n.Visible)
	case "alpha":
		return num(n.Alpha)
	case "text":
		return fmt.Sprintf("%q", n.Text.Value)
	case "world_anchor":
		if p, ok := n.WorldAnchor(); ok {
			return vec(p)
		}
		return "none"
	}
	return "?"
}

// `ch` is needed for one thing: Brush.Position() answers in CHUNK coordinates,
// and a chunk whose origin is at z -399 reports a brush sitting there as z 0 —
// which reads as a bug in whatever script put it there, and is not one. World
// space is what a script writes and what the watch list should show.
func brushField(ch *world.Chunk, b *world.Brush, field string) string {
	switch field {
	case "position":
		return vec(b.Position().Add(ch.Origin))
	case "offset":
		return vec(b.Offset)
	case "rotation":
		return vec(b.Rotation)
	case "scale":
		return vec(b.EffectiveScale())
	case "scale.x":
		return num(b.EffectiveScale().X())
	case "angular_velocity":
		return vec(b.AngularVelocity)
	case "angular_velocity.z":
		return num(b.AngularVelocity.Z())
	case "velocity":
		return vec(b.Velocity)
	case "solid":
		return fmt.Sprint(b.Solid)
	case "texture":
		// The first face's texture. A script re-skinning a brush writes every
		// face, so one is enough to watch a cycle.
		if len(b.Faces) > 0 {
			return b.Faces[0].Texture
		}
		return ""
	}
	return "?"
}

// Rounded hard, so a value that is merely jittering in the last decimal does
// not print a line every tick.
func num(f float32) string { return strconv.FormatFloat(float64(f), 'f', 2, 32) }

func vec(v mgl32.Vec3) string {
	return fmt.Sprintf("<%s, %s, %s>", num(v.X()), num(v.Y()), num(v.Z()))
}

func parseWatches(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

type moveSpec struct {
	to   mgl32.Vec3
	at   float64
	done bool
}

func parseMoves(s string) []*moveSpec {
	var out []*moveSpec
	for _, p := range strings.Split(s, ";") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		body, atS, _ := strings.Cut(p, "@")
		xyz := strings.Split(body, ",")
		if len(xyz) != 3 {
			continue
		}
		var v mgl32.Vec3
		for i := 0; i < 3; i++ {
			f, _ := strconv.ParseFloat(strings.TrimSpace(xyz[i]), 32)
			v[i] = float32(f)
		}
		at, _ := strconv.ParseFloat(atS, 64)
		out = append(out, &moveSpec{to: v, at: at})
	}
	return out
}

type timeSpec struct {
	at   float64
	done bool
}

func parseTimes(s string) []*timeSpec {
	var out []*timeSpec
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		at, _ := strconv.ParseFloat(p, 64)
		out = append(out, &timeSpec{at: at})
	}
	return out
}

// attrField prints one attribute of a set, whatever its type.
func attrField(s world.AttrSet, name string) string {
	a, ok := s[name]
	if !ok {
		return "unset"
	}
	if i, ok := a.Int(); ok {
		return fmt.Sprint(i)
	}
	if f, ok := a.Float(); ok {
		return num(float32(f))
	}
	if b, ok := a.Bool(); ok {
		return fmt.Sprint(b)
	}
	return a.S
}
