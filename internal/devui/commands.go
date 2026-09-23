package devui

// commands.go — what you can type into the console.
//
// Every command that changes the world does it by running flang, not by
// reaching into internal/world itself. `tp` writes `@player.prop.position`,
// `spawn` calls `spawn(...)`, `pause` writes `@level.prop.paused`. That is not
// indirection for its own sake: each of those verbs already carries the detail
// that makes it correct — a rider's position is in its frame's coordinates, a
// spawned entity has to forget its namesake's state, a level's pause has to
// reach the script runtime — and a console that reimplemented any of it would
// be a second, wronger engine that only the author ever runs.
//
// What is written in Go here is the other half: the things that are not world
// state at all. Listings, engine switches, and the two sticky flags (god,
// noclip) that have to be re-applied every tick rather than set once.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"foundry/internal/collision"
	"foundry/internal/fileutil"
	"foundry/internal/flang"
	"foundry/internal/gfx"
	"foundry/internal/hooks"
	"foundry/internal/levelio"
	"foundry/internal/platform"
	"foundry/internal/prefs"
	"foundry/internal/render"
	"foundry/internal/script"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// cmdCtx is one invocation: the level it runs against, and the words after the
// command's own name.
type cmdCtx struct {
	lvl  *world.Level
	args []string
	// rest is everything after the command name with its spacing intact, for
	// the commands whose argument is a line rather than a list of words.
	rest string
}

// arg returns argument i, or "".
func (c *cmdCtx) arg(i int) string {
	if i < len(c.args) {
		return c.args[i]
	}
	return ""
}

// command is one entry in the table below.
type command struct {
	// name is what is typed. usage is the arguments, in the shape help prints
	// them; summary is the one line the list shows.
	name    string
	usage   string
	summary string
	// detail is what `help <name>` adds under the summary. Each string is a
	// line; no line wraps, because the console wraps.
	detail []string
	run    func(c *cmdCtx)
}

// commands is the table, in the order `help` lists them: grouped by what they
// are for rather than alphabetically, because the first thing anyone does with
// a new console is read the list top to bottom.
var commands []command

// byName indexes the table, built once on first use.
var byName map[string]*command

func lookup(name string) *command {
	if byName == nil {
		byName = make(map[string]*command, len(commands))
		for i := range commands {
			byName[commands[i].name] = &commands[i]
		}
	}
	return byName[name]
}

// run dispatches one submitted line.
func run(lvl *world.Level, line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}

	// In the sticky flang mode every line is an expression, and only the one
	// word that leaves it is not.
	if console.repl {
		if line == "exit" || line == "quit" {
			console.repl = false
			printf(kindDim, "left the flang prompt")
			return
		}
		evalFlang(lvl, line)
		return
	}

	// A leading `>` is flang without leaving command mode — the shape you want
	// for one expression in the middle of a session of commands. So is a line
	// that opens with the language's own addressing sigils, because there is
	// no command those could ever be.
	if s, ok := flangLine(line); ok {
		evalFlang(lvl, s)
		return
	}

	name, rest := split1(line)
	cmd := lookup(name)
	if cmd == nil {
		if near := world.NearestName(name, commandNames()); near != "" {
			printf(kindErr, "no command %q — did you mean %q? (`help` lists them)", name, near)
		} else {
			printf(kindErr, "no command %q — `help` lists them, `> ` evaluates flang", name)
		}
		return
	}
	cmd.run(&cmdCtx{lvl: lvl, args: strings.Fields(rest), rest: strings.TrimSpace(rest)})
}

// flangLine reports whether a line is flang rather than a command, and returns
// the flang in it.
func flangLine(line string) (string, bool) {
	switch {
	case strings.HasPrefix(line, ">"):
		return strings.TrimSpace(line[1:]), true
	case strings.HasPrefix(line, "@"), strings.HasPrefix(line, "#"):
		return line, true
	}
	return "", false
}

// split1 splits a line into its first word and the rest.
func split1(line string) (head, rest string) {
	if i := strings.IndexAny(line, " \t"); i >= 0 {
		return line[:i], line[i+1:]
	}
	return line, ""
}

func commandNames() []string {
	out := make([]string, 0, len(commands))
	for i := range commands {
		out = append(out, commands[i].name)
	}
	return out
}

// ---------- the table ----------
//
// Built in init rather than as a package-level literal, because `help` is in
// the table and reads the table, and Go refuses an initialisation cycle.

func init() {
	commands = []command{
		// -- the console itself --
		{
			name: "help", usage: "[command]",
			summary: "list the commands, or explain one",
			run:     cmdHelp,
		},
		{
			name: "clear", summary: "empty the scrollback",
			run: func(c *cmdCtx) { clearBack() },
		},
		{
			name: "repl", summary: "type flang instead of commands; `exit` comes back",
			detail: []string{
				"Every line is compiled and run against the level, the way a script's",
				"handler is. `> <line>` does the same for one line without switching.",
				"Locals do not survive a line; @level.state[\"x\"] does.",
			},
			run: func(c *cmdCtx) {
				console.repl = true
				printf(kindDim, "flang prompt — `exit` returns to commands")
			},
		},
		{
			name: "flang", usage: "<line>",
			summary: "evaluate one line of flang",
			run:     func(c *cmdCtx) { evalFlang(c.lvl, c.rest) },
		},

		// -- looking --
		{
			name: "pos", summary: "where the player is, and what it is standing in",
			run: cmdPos,
		},
		{
			name: "find", usage: "<tag>",
			summary: "everything carrying a tag, with its distance",
			run:     cmdFind,
		},
		{
			name: "tags", summary: "every tag anything in this level carries",
			run: cmdTags,
		},
		{
			name: "entities", usage: "[substring]",
			summary: "the level's entities, nearest first",
			run:     cmdEntities,
		},
		{
			name: "types", usage: "[substring]",
			summary: "the entity types `spawn` can make",
			run:     cmdTypes,
		},
		{
			name: "levels", summary: "the levels `level` can load",
			run: cmdLevels,
		},
		{
			name: "stats", summary: "print the F3 overlay into the scrollback",
			detail: []string{
				"The same rows F3 shows, as text — so they can be scrolled back to and",
				"read against what happened next.",
			},
			run: cmdStats,
		},

		// -- doing --
		{
			name: "tp", usage: "<tag|id|x y z>",
			summary: "teleport the player",
			detail: []string{
				"A name is looked up as an object's ID first and as a tag second; a tag",
				"that several things carry teleports to the nearest of them.",
				"Three numbers are a world position.",
			},
			run: cmdTeleport,
		},
		{
			name: "spawn", usage: "<type> [x y z]",
			summary: "make an entity, in front of the camera by default",
			detail: []string{
				"With no position the entity lands where a ray from the camera stops,",
				"which is the surface you are looking at. `types` lists what can be made.",
			},
			run: cmdSpawn,
		},
		{
			name: "destroy", usage: "<id|#tag>",
			summary: "remove an entity or a volume",
			run:     cmdDestroy,
		},
		{
			name: "level", usage: "[name]",
			summary: "say which level is loaded, or load another",
			run:     cmdLevel,
		},
		{
			name: "script", usage: "<name> [entry]",
			summary: "run a .flang file's main() against this level",
			run:     cmdScript,
		},
		{
			name: "reload", summary: "re-read every script from disk",
			run: cmdReload,
		},
		{
			name: "save", usage: "<slot>", summary: "save the game to a slot",
			run: func(c *cmdCtx) { cmdSaveReq(c, hooks.SaveToSlot, "saving to %q") },
		},
		{
			name: "load", usage: "<slot>", summary: "load a slot",
			run: func(c *cmdCtx) { cmdSaveReq(c, hooks.LoadFromSlot, "loading %q") },
		},
		{
			name: "checkpoint", usage: "[name]", summary: "record a checkpoint here",
			run: func(c *cmdCtx) { cmdSaveReq(c, hooks.Checkpoint, "checkpoint %q") },
		},
		{
			name: "retry", summary: "go back to the last checkpoint",
			run: func(c *cmdCtx) {
				hooks.RequestSave(hooks.SaveRequest{Kind: hooks.Retry})
				printf(kindOK, "retrying from the last checkpoint")
			},
		},
		{
			name: "restart", summary: "load this level again from scratch",
			run: func(c *cmdCtx) {
				hooks.RequestSave(hooks.SaveRequest{Kind: hooks.RestartLevel})
				printf(kindOK, "restarting the level")
			},
		},
		{
			name: "quit", summary: "close the game",
			run: func(c *cmdCtx) {
				hooks.RequestQuit()
				printf(kindOK, "quitting")
			},
		},

		// -- switches --
		{
			name: "god", usage: "[on|off]",
			summary: "keep the player's health topped up",
			detail: []string{
				"Re-applied every tick rather than set once, because one rocket is more",
				"than a full bar and a single top-up would not survive it. Does nothing",
				"in a game whose player carries no `health_max` attribute.",
			},
			run: cmdGod,
		},
		{
			name: "noclip", usage: "[on|off]",
			summary: "gravity and world collision off together",
			run:     cmdNoclip,
		},
		{
			name: "gravity", usage: "[on|off]", summary: "whether the player falls",
			run: func(c *cmdCtx) {
				setSwitch(c, "gravity", &platform.GravityEnabled)
				if !platform.GravityEnabled {
					platform.IsOnGround, platform.JumpRequested = false, false
				}
			},
		},
		{
			name: "collision", usage: "[on|off]",
			summary: "whether entities resolve against the world",
			run:     func(c *cmdCtx) { setSwitch(c, "collision", &world.EntityWorldCollisionEnabled) },
		},
		{
			name: "entity_collision", usage: "[on|off]",
			summary: "whether entities resolve against each other",
			run:     func(c *cmdCtx) { setSwitch(c, "entity_collision", &world.EntityEntityCollisionEnabled) },
		},
		{
			name: "pause", usage: "[on|off]",
			summary: "the script pause — the world and the level's scripts stop",
			run:     cmdPause,
		},
		{
			name: "overlay", usage: "[on|off]", summary: "the F3 overlay",
			run: func(c *cmdCtx) { setSwitch(c, "overlay", &render.DebugOverlayVisible) },
		},
		{
			name: "setting", usage: "[name [value]]",
			summary: "read or write one of the game's own saved settings",
			detail: []string{
				"With no arguments, every setting the game has defined. These are the",
				"ones a script reaches with setting() and set_setting(), and they are",
				"written to the player's settings file like any other.",
			},
			run: cmdSetting,
		},
	}
}

// ---------- the console itself ----------

func cmdHelp(c *cmdCtx) {
	if name := c.arg(0); name != "" {
		cmd := lookup(name)
		if cmd == nil {
			printf(kindErr, "no command %q", name)
			return
		}
		printf(kindOut, "%s %s", cmd.name, cmd.usage)
		printf(kindOut, "    %s", cmd.summary)
		for _, d := range cmd.detail {
			printf(kindDim, "    %s", d)
		}
		return
	}

	printf(kindOut, "Commands — `help <name>` for more on one.")
	w := 0
	for i := range commands {
		if n := len(commands[i].name) + len(commands[i].usage) + 1; n > w {
			w = n
		}
	}
	for i := range commands {
		sig := commands[i].name
		if commands[i].usage != "" {
			sig += " " + commands[i].usage
		}
		printf(kindOut, "  %-*s  %s", w, sig, commands[i].summary)
	}
	printf(kindDim, "A line starting with `>`, `@` or `#` is flang, whatever mode you are in.")
}

// ---------- looking ----------

func cmdPos(c *cmdCtx) {
	pe := world.PlayerEntity
	if pe == nil {
		printf(kindErr, "there is no player")
		return
	}
	p := pe.Position
	printf(kindOut, "position  %.2f %.2f %.2f", p.X(), p.Y(), p.Z())
	if pe.Frame != "" {
		// The stored position is in the frame's coordinates, so on a moving
		// ship it is not a place in the world at all. Say both.
		w := gfx.Cam.Position
		printf(kindOut, "frame     %s (eye in world: %.2f %.2f %.2f)", pe.Frame, w.X(), w.Y(), w.Z())
	}
	if pe.Seat != "" {
		printf(kindOut, "seat      %s", pe.Seat)
	}
	printf(kindOut, "chunk     %s", orDash(pe.CurrentChunk))
	printf(kindOut, "facing    yaw %.1f  pitch %.1f", gfx.Cam.Yaw, gfx.Cam.Pitch)
	printf(kindDim, "tp %.2f %.2f %.2f", p.X(), p.Y(), p.Z())
}

func cmdFind(c *cmdCtx) {
	tag := c.arg(0)
	if tag == "" {
		printf(kindErr, "find takes a tag")
		return
	}
	tag = strings.TrimPrefix(tag, "#")
	refs := script.TaggedRefs(c.lvl, tag)
	if len(refs) == 0 {
		printf(kindErr, "nothing carries #%s%s", tag, didYouMean(tag, script.LevelTags(c.lvl)))
		return
	}
	from, haveFrom := playerPosition(c.lvl)
	for _, r := range refs {
		line := fmt.Sprintf("  %-10s %s", r.Kind.String(), r.A)
		if p, ok := script.RefPosition(c.lvl, r); ok {
			line += fmt.Sprintf("   %8.2f %8.2f %8.2f", p.X(), p.Y(), p.Z())
			if haveFrom {
				line += fmt.Sprintf("   %7.1f away", p.Sub(from).Len())
			}
		}
		printf(kindOut, "%s", line)
	}
	printf(kindDim, "%d carrying #%s", len(refs), tag)
}

func cmdTags(c *cmdCtx) {
	tags := script.LevelTags(c.lvl)
	if len(tags) == 0 {
		printf(kindDim, "nothing in this level carries a tag")
		return
	}
	printf(kindOut, "%s", strings.Join(tags, "  "))
	printf(kindDim, "%d tags", len(tags))
}

func cmdEntities(c *cmdCtx) {
	if c.lvl == nil {
		printf(kindErr, "no level is loaded")
		return
	}
	filter := strings.ToLower(c.arg(0))
	from, haveFrom := playerPosition(c.lvl)

	type row struct {
		id, typ string
		dist    float32
		pos     mgl32.Vec3
	}
	var rows []row
	for i := range c.lvl.Entities {
		e := &c.lvl.Entities[i]
		if filter != "" &&
			!strings.Contains(strings.ToLower(e.ID), filter) &&
			!strings.Contains(strings.ToLower(e.Type), filter) {
			continue
		}
		p := c.lvl.Frames.WorldPosition(e)
		r := row{id: e.ID, typ: e.Type, pos: p}
		if haveFrom {
			r.dist = p.Sub(from).Len()
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		printf(kindDim, "no entities match %q", c.arg(0))
		return
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].dist != rows[j].dist {
			return rows[i].dist < rows[j].dist
		}
		return rows[i].id < rows[j].id
	})
	// A level with two thousand entities in it would fill the scrollback and
	// push everything else out of the ring. The filter is the way to see more.
	const cap = 60
	shown := rows
	if len(shown) > cap {
		shown = shown[:cap]
	}
	for _, r := range shown {
		printf(kindOut, "  %-24s %-20s %8.2f %8.2f %8.2f  %7.1f away",
			r.id, orDash(r.typ), r.pos.X(), r.pos.Y(), r.pos.Z(), r.dist)
	}
	if len(rows) > len(shown) {
		printf(kindDim, "%d entities, %d shown — pass a substring to narrow it", len(rows), len(shown))
	} else {
		printf(kindDim, "%d entities", len(rows))
	}
}

func cmdTypes(c *cmdCtx) {
	names := world.ScanEntityDefs()
	if filter := strings.ToLower(c.arg(0)); filter != "" {
		var keep []string
		for _, n := range names {
			if strings.Contains(strings.ToLower(n), filter) {
				keep = append(keep, n)
			}
		}
		names = keep
	}
	if len(names) == 0 {
		printf(kindDim, "no entity types match %q", c.arg(0))
		return
	}
	printf(kindOut, "%s", strings.Join(names, "  "))
	printf(kindDim, "%d types", len(names))
}

func cmdLevels(c *cmdCtx) {
	names := levelio.ScanLevelFiles(levelio.LevelsDir())
	for i, n := range names {
		names[i] = strings.TrimSuffix(filepath.Base(n), filepath.Ext(n))
	}
	if len(names) == 0 {
		printf(kindDim, "no levels found in %s", levelio.LevelsDir())
		return
	}
	sort.Strings(names)
	printf(kindOut, "%s", strings.Join(names, "  "))
	printf(kindDim, "%d levels", len(names))
}

func cmdStats(c *cmdCtx) {
	for _, l := range render.DebugOverlayLines() {
		if l.Rule {
			printf(kindDim, "  ----")
			continue
		}
		printf(kindOut, "  %s", l.Text)
	}
}

// ---------- doing ----------

func cmdTeleport(c *cmdCtx) {
	p, ok := teleportTarget(c)
	if !ok {
		return
	}
	// Through the property, not through Entity.Position: a player riding a
	// ship stores its position in the ship's coordinates, and the property
	// setter is what carries a world point into them.
	if err := evalStatement(c.lvl, fmt.Sprintf("@player.prop.position = %s", vecLit(p))); err != nil {
		printf(kindErr, "tp: %v", err)
		return
	}
	printf(kindOK, "teleported to %.2f %.2f %.2f", p.X(), p.Y(), p.Z())
}

// teleportTarget reads tp's argument: three numbers, or a name.
func teleportTarget(c *cmdCtx) (mgl32.Vec3, bool) {
	switch len(c.args) {
	case 3:
		p, err := vec3Args(c.args)
		if err != nil {
			printf(kindErr, "tp: %v", err)
			return mgl32.Vec3{}, false
		}
		return p, true
	case 1:
		return namedPosition(c.lvl, c.args[0])
	}
	printf(kindErr, "tp takes a tag, an ID, or three numbers")
	return mgl32.Vec3{}, false
}

// namedPosition resolves a name typed at the console to a place in the world.
//
// An ID first and a tag second, because an ID is unique and a tag is not: a
// name that is both should mean the one thing it can only mean.
func namedPosition(lvl *world.Level, name string) (mgl32.Vec3, bool) {
	bare := strings.TrimPrefix(strings.TrimPrefix(name, "#"), "@")
	if !strings.HasPrefix(name, "#") {
		if r, ok := script.ResolveRef(lvl, bare); ok {
			if p, ok := script.RefPosition(lvl, r); ok {
				return p, true
			}
			printf(kindErr, "%s is a %s, which is not anywhere in particular", bare, r.Kind.String())
			return mgl32.Vec3{}, false
		}
	}

	refs := script.TaggedRefs(lvl, bare)
	from, haveFrom := playerPosition(lvl)
	best, found := mgl32.Vec3{}, false
	for _, r := range refs {
		p, ok := script.RefPosition(lvl, r)
		if !ok {
			continue
		}
		if !found || (haveFrom && p.Sub(from).Len() < best.Sub(from).Len()) {
			best, found = p, true
		}
	}
	if !found {
		printf(kindErr, "nothing called %q%s", bare, didYouMean(bare, script.LevelTags(lvl)))
		return mgl32.Vec3{}, false
	}
	return best, true
}

func cmdSpawn(c *cmdCtx) {
	typeName := c.arg(0)
	if typeName == "" {
		printf(kindErr, "spawn takes an entity type — `types` lists them")
		return
	}
	if _, err := world.LoadEntityDef(typeName); err != nil {
		printf(kindErr, "spawn: %v%s", err, didYouMean(typeName, world.ScanEntityDefs()))
		return
	}

	var p mgl32.Vec3
	switch len(c.args) {
	case 1:
		p = aimPoint(c.lvl)
	case 4:
		var err error
		if p, err = vec3Args(c.args[1:]); err != nil {
			printf(kindErr, "spawn: %v", err)
			return
		}
	default:
		printf(kindErr, "spawn takes a type and, optionally, three numbers")
		return
	}

	// The verb, not world.Instantiate: it is what names the entity, gives it
	// its script, materialises the emitters, volumes and paths its type
	// carries, and makes sure it does not inherit a destroyed namesake's state.
	v, err := evalValue(c.lvl, fmt.Sprintf("spawn(%s, %s)", strLit(typeName), vecLit(p)))
	if err != nil {
		printf(kindErr, "spawn: %v", err)
		return
	}
	printf(kindOK, "spawned %s at %.2f %.2f %.2f", valueName(v), p.X(), p.Y(), p.Z())
}

func cmdDestroy(c *cmdCtx) {
	name := c.arg(0)
	if name == "" {
		printf(kindErr, "destroy takes an ID or a #tag")
		return
	}
	if strings.HasPrefix(name, "#") {
		tag := name[1:]
		refs := script.TaggedRefs(c.lvl, tag)
		if len(refs) == 0 {
			printf(kindErr, "nothing carries #%s", tag)
			return
		}
		// One statement per ref rather than `destroy(#tag)`: destroy takes one
		// object, and doing it here means a ref that cannot be destroyed
		// reports itself without stopping the others.
		n := 0
		for _, r := range refs {
			if err := evalStatement(c.lvl, fmt.Sprintf("destroy(@%s)", r.A)); err != nil {
				printf(kindErr, "destroy %s: %v", r.A, err)
				continue
			}
			n++
		}
		printf(kindOK, "destroyed %d of %d carrying #%s", n, len(refs), tag)
		return
	}
	bare := strings.TrimPrefix(name, "@")
	if err := evalStatement(c.lvl, fmt.Sprintf("destroy(@%s)", bare)); err != nil {
		printf(kindErr, "destroy: %v", err)
		return
	}
	printf(kindOK, "destroyed %s", bare)
}

func cmdLevel(c *cmdCtx) {
	if c.arg(0) == "" {
		if c.lvl == nil || c.lvl.Name == "" {
			printf(kindDim, "no level, or one built in memory")
			return
		}
		printf(kindOut, "%s", c.lvl.Name)
		return
	}
	// A request, not a load: the frame loop owns level changes, and loading
	// from here would free the console's own caller.
	hooks.RequestLevelLoad(c.arg(0))
	printf(kindOK, "loading %s", c.arg(0))
}

func cmdScript(c *cmdCtx) {
	name := c.arg(0)
	if name == "" {
		printf(kindErr, "script takes the name of a .flang file under %s", fileutil.ScriptsDir())
		return
	}
	entry := c.arg(1)
	if entry == "" {
		entry = "main"
	}
	if !strings.HasSuffix(name, fileutil.ScriptExt) {
		name += fileutil.ScriptExt
	}
	path := filepath.Join(fileutil.ScriptsDir(), filepath.FromSlash(name))
	src, err := script.ReadScriptFile(path)
	if err != nil {
		printf(kindErr, "%v", err)
		return
	}
	v, err := script.RunScriptValue(c.lvl, path, string(src), entry,
		func(s string) { printf(kindOut, "%s", s) })
	if err != nil {
		printf(kindErr, "%v", err)
		return
	}
	if v != nil && v.Type() != flang.TypeNull {
		printf(kindOK, "%s", v.String())
	} else {
		printf(kindOK, "ran %s:%s()", name, entry)
	}
}

func cmdReload(c *cmdCtx) {
	script.ScriptReload()
	printf(kindOK, "scripts re-read from disk")
}

func cmdSaveReq(c *cmdCtx, kind hooks.SaveRequestKind, format string) {
	name := c.rest
	if name == "" && kind != hooks.Checkpoint {
		printf(kindErr, "this takes the name of a slot")
		return
	}
	hooks.RequestSave(hooks.SaveRequest{Kind: kind, Name: name})
	printf(kindOK, format, name)
}

// ---------- switches ----------

func cmdGod(c *cmdCtx) {
	setSwitch(c, "god", &console.god)
	if console.god {
		applyStickies(c.lvl)
	}
}

func cmdNoclip(c *cmdCtx) {
	// Read the current state as "both off", so that a bare `noclip` flips the
	// pair together however they were left by the two commands underneath it.
	on := !(platform.GravityEnabled || world.EntityWorldCollisionEnabled)
	want, err := switchArg(c.args, on)
	if err != nil {
		printf(kindErr, "noclip: %v", err)
		return
	}
	platform.GravityEnabled = !want
	world.EntityWorldCollisionEnabled = !want
	if want {
		platform.IsOnGround, platform.JumpRequested = false, false
	}
	printf(kindOK, "noclip %s", onOffWord(want))
}

func cmdPause(c *cmdCtx) {
	v, err := evalValue(c.lvl, "@level.prop.paused")
	if err != nil {
		printf(kindErr, "pause: %v", err)
		return
	}
	want, err := switchArg(c.args, isTrue(v))
	if err != nil {
		printf(kindErr, "pause: %v", err)
		return
	}
	if err := evalStatement(c.lvl, fmt.Sprintf("@level.prop.paused = %t", want)); err != nil {
		printf(kindErr, "pause: %v", err)
		return
	}
	printf(kindOK, "pause %s", onOffWord(want))
}

func cmdSetting(c *cmdCtx) {
	switch len(c.args) {
	case 0:
		data, err := prefs.Marshal()
		if err != nil {
			printf(kindErr, "settings: %v", err)
			return
		}
		s := strings.TrimSpace(string(data))
		if s == "" || s == "{}" {
			printf(kindDim, "this game has defined no settings")
			return
		}
		for _, line := range strings.Split(s, "\n") {
			printf(kindOut, "  %s", line)
		}
	case 1:
		v, ok := prefs.Get(c.args[0])
		if !ok {
			printf(kindErr, "no setting %q", c.args[0])
			return
		}
		printf(kindOut, "%s = %v", c.args[0], v)
	default:
		if err := prefs.Set(c.args[0], settingValue(c.args[1])); err != nil {
			printf(kindErr, "setting: %v", err)
			return
		}
		printf(kindOK, "%s = %s", c.args[0], c.args[1])
	}
}

// setSwitch is the body every plain on/off command shares.
func setSwitch(c *cmdCtx, name string, flag *bool) {
	want, err := switchArg(c.args, *flag)
	if err != nil {
		printf(kindErr, "%s: %v", name, err)
		return
	}
	*flag = want
	printf(kindOK, "%s %s", name, onOffWord(want))
}

// switchArg reads an on/off argument. With none, the switch flips, which is
// what a command typed twice in a row is nearly always meant to do.
func switchArg(args []string, current bool) (bool, error) {
	if len(args) == 0 {
		return !current, nil
	}
	switch strings.ToLower(args[0]) {
	case "on", "true", "1", "yes":
		return true, nil
	case "off", "false", "0", "no":
		return false, nil
	}
	return false, fmt.Errorf("%q is not on or off", args[0])
}

func onOffWord(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// applyStickies re-applies the switches that do not stay applied.
//
// Health is the only one so far. A single top-up lasts until the next thing
// that hurts the player, so god has to be a thing the frame does rather than a
// thing a command did — the same shape the headless simulator's -god uses, for
// the same reason.
func applyStickies(lvl *world.Level) {
	if !console.god || world.PlayerEntity == nil {
		return
	}
	// Far more than one tick can take away: a rocket does more than a full
	// bar, and a player who dies between two top-ups has already put the
	// game-over screen up.
	hm, ok := world.PlayerEntity.Attributes["health_max"]
	if !ok {
		return
	}
	hm.I, hm.F = 100000, 100000
	world.PlayerEntity.Attributes["health"] = hm
}

// ---------- shared ----------

// aimPoint is where a ray from the camera stops: the surface being looked at,
// or a point out in front when there is nothing there.
func aimPoint(lvl *world.Level) mgl32.Vec3 {
	const reach = 200
	res := collision.Raycast(lvl, gfx.Cam.Position, gfx.Cam.Direction, collision.RayFilter{
		Range:    reach,
		Geometry: true,
	})
	if res.Blocked {
		// Back off the surface, or the entity is spawned inside it and the
		// first thing collision does is push it out somewhere unhelpful.
		return res.Stop.Sub(gfx.Cam.Direction.Normalize().Mul(1))
	}
	return gfx.Cam.Position.Add(gfx.Cam.Direction.Normalize().Mul(5))
}

// playerPosition is where the player is, in world coordinates.
func playerPosition(lvl *world.Level) (mgl32.Vec3, bool) {
	if world.PlayerEntity == nil {
		return mgl32.Vec3{}, false
	}
	if lvl != nil {
		return lvl.Frames.WorldPosition(world.PlayerEntity), true
	}
	return world.PlayerEntity.Position, true
}

// vec3Args reads three numbers typed as separate words.
func vec3Args(args []string) (mgl32.Vec3, error) {
	var v mgl32.Vec3
	for i := 0; i < 3; i++ {
		f, err := strconv.ParseFloat(args[i], 32)
		if err != nil {
			return v, fmt.Errorf("%q is not a number", args[i])
		}
		v[i] = float32(f)
	}
	return v, nil
}

// vecLit renders a vec the way flang spells one. The components are forced to
// carry a decimal point, because `1` is an int in flang and `<1, 0, 0>` is not
// a vec.
func vecLit(p mgl32.Vec3) string {
	return fmt.Sprintf("<%s, %s, %s>", floatLit(p.X()), floatLit(p.Y()), floatLit(p.Z()))
}

func floatLit(f float32) string {
	s := strconv.FormatFloat(float64(f), 'f', -1, 32)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

// strLit renders a string the way flang spells one, escaped — a type name with
// a quote in it is not going to happen, and building source without escaping is
// how it happens anyway.
func strLit(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString("\\\"")
		case '\\':
			b.WriteString("\\\\")
		case '\n':
			b.WriteString("\\n")
		case '\t':
			b.WriteString("\\t")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// settingValue turns a typed word into the kind of value it looks like, since a
// setting holds one of the four scalars and the console only ever has text.
func settingValue(s string) any {
	switch strings.ToLower(s) {
	case "true":
		return true
	case "false":
		return false
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

func isTrue(v flang.Value) bool {
	b, ok := v.(flang.BoolVal)
	return ok && bool(b)
}

// valueName is what a spawned ref is called, for the line that reports it.
func valueName(v flang.Value) string {
	if r, ok := v.(flang.RefVal); ok {
		return r.A
	}
	if v == nil {
		return "?"
	}
	return v.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// didYouMean is the suggestion appended to a name that matched nothing.
func didYouMean(name string, candidates []string) string {
	if near := world.NearestName(name, candidates); near != "" {
		return fmt.Sprintf(" — did you mean %q?", near)
	}
	return ""
}
