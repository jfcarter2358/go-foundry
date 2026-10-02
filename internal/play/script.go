package play

// script.go — the command language, and the runner that spends it over ticks.
//
// A script is one line per command, or several separated by semicolons, which
// is what makes it typeable in one breath:
//
//	look 90 -5; forward 3; use; wait 1; shot door.png   // a comment
//
// It is small on purpose. Every verb is something a player does with a key, a
// button or the mouse, plus a handful that stand in for the parts of a level a
// two-minute run cannot reach (enable, wake, kill, teleport). Anything that
// would need a loop, a condition or a variable belongs in a .flang script or a
// scenario file — this is the hands, not the brain.
//
// The runner is a stepper rather than a loop: Tick is called once per frame and
// returns that frame's input. That is what lets the same script play in a
// headless session, which ticks as fast as it can, and in the editor's window,
// which ticks at the refresh rate and has an author watching.

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"foundry/internal/hooks"
	"foundry/internal/platform"
	"foundry/internal/render"
	"foundry/internal/sim"

	"github.com/go-gl/mathgl/mgl32"
)

// Cmd is one command, parsed but not resolved: the level a name points at may
// not exist until the tick the command runs on.
type Cmd struct {
	Verb string
	Args []string
	Line int
	Text string
}

// verb describes one command for the parser and for the help text. min and max
// are how many arguments it takes, max -1 meaning "the rest of the line".
type verb struct {
	min, max int
	usage    string
	help     string
}

var verbs = map[string]verb{
	"wait":        {1, 1, "wait <seconds>", "let the world run"},
	"look":        {1, 2, "look <yaw> [pitch]", "point the view, in degrees (yaw 0 is +X, 90 is +Z; pitch up is positive)"},
	"turn":        {1, 2, "turn <dyaw> [dpitch]", "turn the view by so many degrees"},
	"look_at":     {1, 3, "look_at <x,y,z|entity|#tag>", "point the view at a place, an entity, or the nearest entity with a tag"},
	"forward":     {0, 1, "forward [seconds]", "walk where the view points (default 1 s)"},
	"back":        {0, 1, "back [seconds]", "walk backwards"},
	"left":        {0, 1, "left [seconds]", "strafe left"},
	"right":       {0, 1, "right [seconds]", "strafe right"},
	"stop":        {0, 0, "stop", "let go of the movement keys"},
	"to":          {1, 4, "to <x,y,z|entity|#tag> [seconds]", "face a place and walk to it, giving up after so long (default 20 s) or when the way is blocked"},
	"crouch":      {1, 1, "crouch on|off", "hold or release crouch"},
	"sprint":      {1, 1, "sprint on|off", "hold or release sprint"},
	"jump":        {0, 0, "jump", "one jump"},
	"fire":        {0, 1, "fire [seconds]", "hold the trigger (default 0.1 s, a tap)"},
	"aim":         {1, 1, "aim on|off", "hold or release the aim button (iron sights)"},
	"reload":      {0, 0, "reload", "reload"},
	"throw":       {0, 0, "throw", "throw a grenade"},
	"weapon":      {1, 1, "weapon <slot>", "select a weapon slot"},
	"next_weapon": {0, 0, "next_weapon", "cycle weapons forward"},
	"prev_weapon": {0, 0, "prev_weapon", "cycle weapons back"},
	"use":         {0, 0, "use", "press interact"},
	"key":         {2, -1, "key down|up|tap <name>", "press, release, or press and release any key, mouse button or pad input by name (Space, F, Left Shift, Mouse Left, Pad A, Pad RT), for key_down and :key_down:"},
	"click":       {1, 1, "click <widget>|<screen>:<widget>", "click a widget on a screen that is up"},
	"cursor":      {1, 1, "cursor <x,y>|center|off", "free the pointer and hold it at a window pixel, as a mouse would; off captures it again"},
	"tap":         {0, 1, "tap [left|right|middle]", "press and release a mouse button on the world where the cursor is"},
	"drag":        {1, 3, "drag <x,y> [seconds] [left|right|middle]", "hold a mouse button and move the cursor to a window pixel over so long (default 0.3 s)"},
	"teleport":    {1, 3, "teleport <x,y,z>", "put the player somewhere, skipping the walk"},
	"enable":      {1, 1, "enable <chunk|volume id>", "switch a chunk or volume on"},
	"wake":        {1, 1, "wake <#tag>", "switch on the AI of every entity with a tag"},
	"kill":        {1, 1, "kill <#tag>", "damage every scripted entity with a tag for 9999"},
	"read":        {1, 1, "read <path>", "report one value: player.position, <id>.attr.health, hud:ammo.text …"},
	"say":         {1, -1, "say <text>", "put a note in the log, to mark what a run was testing"},
	"shot":        {0, 1, "shot [name.png]", "take a screenshot"},
	"view":        {1, 1, "view <normal|lighting|probes|field|wireframe|clusters|lod|overdraw>", "draw the shots that follow in a debug view: lighting is the light alone on white surfaces; clusters, lod and overdraw show what the Nanite cull draws"},
}

// Usage is the command list, for a tool's help and for an error message.
func Usage() string {
	var b strings.Builder
	b.WriteString("play commands (one per line, or separated by ';'):\n")
	for _, name := range verbOrder {
		v := verbs[name]
		fmt.Fprintf(&b, "  %-34s %s\n", v.usage, v.help)
	}
	return b.String()
}

// verbOrder is the order Usage lists them in: the order a run uses them, not
// the alphabet.
var verbOrder = []string{
	"look", "turn", "look_at", "forward", "back", "left", "right", "to", "stop",
	"crouch", "sprint", "jump", "use", "key", "fire", "aim", "reload", "throw",
	"weapon", "next_weapon", "prev_weapon", "click", "cursor", "tap", "drag",
	"wait", "shot", "view", "read", "say", "teleport", "enable", "wake", "kill",
}

// Parse reads a script and checks every command before any of it runs: a typo
// in the last line should not be found out three minutes into a walk.
func Parse(text string) ([]Cmd, error) {
	var cmds []Cmd
	for ln, line := range strings.Split(text, "\n") {
		// A comment is `//`, not `#`: a tag is spelled #enemy and is an
		// argument to half the verbs here, so the usual marker would eat one.
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		for _, part := range strings.Split(line, ";") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			fields := strings.Fields(part)
			c := Cmd{Verb: strings.ToLower(fields[0]), Args: fields[1:], Line: ln + 1, Text: part}
			v, ok := verbs[c.Verb]
			if !ok {
				return nil, fmt.Errorf("line %d: no play command %q\n\n%s", c.Line, c.Verb, Usage())
			}
			if len(c.Args) < v.min || (v.max >= 0 && len(c.Args) > v.max) {
				return nil, fmt.Errorf("line %d: %s", c.Line, v.usage)
			}
			if c.Verb == "view" && !render.IsView(c.Args[0]) {
				return nil, fmt.Errorf("line %d: %q is not a view: one of %s", c.Line, c.Args[0], strings.Join(render.ViewNames(), ", "))
			}
			// `say` keeps its whole line, spaces and all.
			if c.Verb == "say" {
				c.Args = []string{strings.TrimSpace(strings.TrimPrefix(part, fields[0]))}
			}
			cmds = append(cmds, c)
		}
	}
	return cmds, nil
}

// Runner plays a parsed script against a target, one tick at a time.
type Runner struct {
	t     Target
	cmds  []Cmd
	i     int
	ctl   Controls
	shots []string

	// hold is what a timed command has left to run, and release is what it
	// does when that runs out — letting go of the trigger, or of the movement.
	hold    float64
	release func()
	// during runs every tick a timed command is holding — a drag moving the
	// cursor along its way.
	during func(dt float64)

	// goal is a `to` in flight: where it is walking, when it gives up, and
	// where it was a second ago, which is how it notices a wall.
	goal *goal

	done bool
}

type goal struct {
	at       mgl32.Vec3
	left     float64 // seconds before giving up
	lastAt   mgl32.Vec3
	sinceFix float64
}

// New starts a run. Nothing happens until the first Tick.
func New(t Target, cmds []Cmd) *Runner { return &Runner{t: t, cmds: cmds} }

// Done reports whether the script has finished.
func (r *Runner) Done() bool { return r.done }

// Shots are the files the script's `shot` commands wrote, in order.
func (r *Runner) Shots() []string { return r.shots }

// Controls are what the script is holding, for a caller that drives the frame
// itself rather than taking Tick's input.
func (r *Runner) Controls() *Controls { return &r.ctl }

// Tick advances the script by one frame of dt and answers that frame's input.
//
// It is called BEFORE the frame's own step, which is what a key being down
// means: the press has already happened when the tick that spends it runs.
func (r *Runner) Tick(dt float64) sim.Input {
	if r.done {
		return sim.Input{}
	}
	// What is already running gets this tick first.
	if r.hold > 0 {
		r.hold -= dt
		if r.during != nil {
			r.during(dt)
		}
		if r.hold <= 0 {
			r.hold = 0
			r.during = nil
			if r.release != nil {
				r.release()
				r.release = nil
			}
		}
	}
	if r.goal != nil {
		r.stepGoal(dt)
	}

	// Then as many instantaneous commands as will go, stopping at the first
	// one that takes time.
	for r.hold <= 0 && r.goal == nil && r.i < len(r.cmds) {
		c := r.cmds[r.i]
		r.i++
		if err := r.begin(c); err != nil {
			// A command that cannot run is a note, not the end of the run: a
			// script that fails to find a door should still show what it is
			// looking at when it fails.
			r.t.Emit("ERROR", c.Text, err.Error())
		}
	}
	if r.hold <= 0 && r.goal == nil && r.i >= len(r.cmds) {
		r.finish()
		return sim.Input{}
	}
	return r.ctl.Input(dt)
}

// finish lets go of everything a script was holding. A run that ended with the
// trigger down would leave the next one firing.
func (r *Runner) finish() {
	if r.release != nil {
		r.release()
		r.release = nil
	}
	r.ctl.Stop()
	PrimaryFire(false)
	r.during = nil
	// The pointer goes back to the mouse, or the author's own could not point
	// at anything after a run in the editor's window.
	ReleasePointer()
	r.done = true
}

// Stop ends a run early, releasing what it held — a caller giving up on a
// script that is walking into a wall.
func (r *Runner) Stop() {
	if !r.done {
		r.t.Emit("STOP", "", fmt.Sprintf("%d of %d commands run", r.i, len(r.cmds)))
		r.finish()
	}
}

// stepGoal walks one tick towards a `to` target.
func (r *Runner) stepGoal(dt float64) {
	lvl := r.t.Level()
	if lvl == nil {
		r.t.Emit("WALK", "stopped", "the level went away")
		r.goal, r.ctl.Forward, r.ctl.Strafe = nil, 0, 0
		return
	}
	at := Eye(lvl)
	g := r.goal
	d := g.at.Sub(at)
	d[1] = 0
	if d.Len() < 0.5 {
		r.t.Emit("WALK", "arrived", vec(at))
		r.goal, r.ctl.Forward, r.ctl.Strafe = nil, 0, 0
		return
	}
	g.left -= dt
	if g.left <= 0 {
		r.t.Emit("WALK", "gave up", fmt.Sprintf("%s, still %.1fm short", vec(at), d.Len()))
		r.goal, r.ctl.Forward, r.ctl.Strafe = nil, 0, 0
		return
	}
	// Nowhere in a second means something is in the way. Worth saying: a run
	// that silently walked into a wall for twenty seconds looks, in the log,
	// exactly like one that walked somewhere.
	g.sinceFix += dt
	if g.sinceFix >= 1 {
		if at.Sub(g.lastAt).Len() < 0.15 {
			r.t.Emit("WALK", "blocked", fmt.Sprintf("%s, %.1fm short of %s", vec(at), d.Len(), vec(g.at)))
			r.goal, r.ctl.Forward, r.ctl.Strafe = nil, 0, 0
			return
		}
		g.lastAt, g.sinceFix = at, 0
	}
	// Steered every tick rather than aimed once: the walk is a direction, and
	// a body pushed sideways by a step or a slope has to be re-aimed or it
	// arrives somewhere else. Aimed from a point at the target's own height, so
	// walking somewhere below or above does not leave the view pitched at the
	// floor — which is what the picture at the end of the walk would be of.
	LookAt(mgl32.Vec3{at.X(), g.at.Y(), at.Z()}, g.at)
	r.ctl.Forward, r.ctl.Strafe = 1, 0
}

// begin runs one command, and sets up whatever it leaves running.
func (r *Runner) begin(c Cmd) error {
	lvl := r.t.Level()
	if lvl == nil {
		// A level can go out from under a run — a script's `load_level`, or an
		// editor closing one — and half these verbs would read it.
		return fmt.Errorf("no level is loaded")
	}
	switch c.Verb {
	case "wait":
		s, err := seconds(c.Args[0])
		if err != nil {
			return err
		}
		r.hold = s

	case "look", "turn":
		yaw, err := number(c.Args[0])
		if err != nil {
			return err
		}
		pitch := 0.0
		if len(c.Args) > 1 {
			if pitch, err = number(c.Args[1]); err != nil {
				return err
			}
		}
		if c.Verb == "turn" {
			y, p := Look()
			yaw, pitch = y+yaw, p+pitch
		} else if len(c.Args) == 1 {
			// `look 90` turns without changing the pitch, which is what
			// someone typing one number means.
			_, pitch = Look()
		}
		SetLook(yaw, pitch)
		r.t.Emit("LOOK", "", fmt.Sprintf("yaw %.0f pitch %.0f", yaw, pitch))

	case "look_at":
		at, err := Point(lvl, strings.Join(c.Args, ","), Eye(lvl))
		if err != nil {
			return err
		}
		LookAt(Eye(lvl), at)
		y, p := Look()
		r.t.Emit("LOOK", strings.Join(c.Args, " "), fmt.Sprintf("yaw %.0f pitch %.0f", y, p))

	case "forward", "back", "left", "right":
		s := 1.0
		if len(c.Args) > 0 {
			var err error
			if s, err = seconds(c.Args[0]); err != nil {
				return err
			}
		}
		switch c.Verb {
		case "forward":
			r.ctl.Forward, r.ctl.Strafe = 1, 0
		case "back":
			r.ctl.Forward, r.ctl.Strafe = -1, 0
		case "left":
			r.ctl.Forward, r.ctl.Strafe = 0, 1
		case "right":
			r.ctl.Forward, r.ctl.Strafe = 0, -1
		}
		r.hold, r.release = s, r.ctl.Stop

	case "stop":
		r.ctl.Stop()

	case "to":
		// The place is one argument (x,y,z, an id or a #tag) or three (x y z);
		// one more than that is the timeout.
		args, limit := c.Args, 20.0
		if len(args) == 2 || len(args) == 4 {
			v, err := seconds(args[len(args)-1])
			if err != nil {
				return err
			}
			limit, args = v, args[:len(args)-1]
		}
		at, err := Point(lvl, strings.Join(args, ","), Eye(lvl))
		if err != nil {
			return err
		}
		here := Eye(lvl)
		r.goal = &goal{at: at, left: limit, lastAt: here}
		r.t.Emit("WALK", "to", fmt.Sprintf("%s (%.1fm)", vec(at), at.Sub(here).Len()))

	case "crouch":
		on, err := onOff(c.Args[0])
		if err != nil {
			return err
		}
		r.ctl.Crouch = on
	case "sprint":
		on, err := onOff(c.Args[0])
		if err != nil {
			return err
		}
		r.ctl.Sprint = on
	case "jump":
		r.ctl.Jump = true

	case "fire":
		s := 0.1
		if len(c.Args) > 0 {
			var err error
			if s, err = seconds(c.Args[0]); err != nil {
				return err
			}
		}
		PrimaryFire(true)
		r.hold, r.release = s, func() { PrimaryFire(false) }
		r.t.Emit("FIRE", "", fmt.Sprintf("%.2fs", s))
	case "aim":
		on, err := onOff(c.Args[0])
		if err != nil {
			return err
		}
		SecondaryFire(on)
	case "reload":
		Reload()
	case "throw":
		Throw()
		r.t.Emit("THROW", "", "")
	case "weapon":
		n, err := strconv.Atoi(c.Args[0])
		if err != nil {
			return fmt.Errorf("weapon: %q is not a slot number", c.Args[0])
		}
		WeaponSlot(n)
	case "next_weapon":
		CycleWeapon(1)
	case "prev_weapon":
		CycleWeapon(-1)

	case "use":
		Interact()
		r.t.Emit("INTERACT", "", "")

	case "key":
		name := strings.Join(c.Args[1:], " ")
		in, ok := platform.ParseInput(name)
		if !ok || !in.Bound() {
			return fmt.Errorf("%q is no key, mouse button or pad input", name)
		}
		switch strings.ToLower(c.Args[0]) {
		case "down":
			platform.PressKey(in)
		case "up":
			platform.ReleaseKey(in)
		case "tap":
			platform.PressKey(in)
			platform.ReleaseKey(in)
		default:
			return fmt.Errorf("key takes down, up or tap, not %q", c.Args[0])
		}
		r.t.Emit("KEY", strings.ToLower(c.Args[0]), in.Name())

	case "cursor":
		if c.Args[0] == "off" {
			ReleasePointer()
			r.t.Emit("CURSOR", "off", "")
			break
		}
		x, y, err := pixel(c.Args[0])
		if err != nil {
			return err
		}
		PointAt(x, y)
		r.t.Emit("CURSOR", "", fmt.Sprintf("%.0f,%.0f", x, y))

	case "tap":
		b, err := mouseButton(c.Args, 0)
		if err != nil {
			return err
		}
		if err := MouseButton(b, true); err != nil {
			return err
		}
		if err := MouseButton(b, false); err != nil {
			return err
		}
		r.t.Emit("TAP", b.Atom(), "")

	case "drag":
		tx, ty, err := pixel(c.Args[0])
		if err != nil {
			return err
		}
		s := 0.3
		if len(c.Args) > 1 {
			if s, err = seconds(c.Args[1]); err != nil {
				return err
			}
		}
		b, err := mouseButton(c.Args, 2)
		if err != nil {
			return err
		}
		from := hooks.Cursor()
		if err := MouseButton(b, true); err != nil {
			return err
		}
		fx, fy, elapsed := from.X, from.Y, 0.0
		r.hold = s
		r.during = func(dt float64) {
			elapsed += dt
			f := float32(math.Min(elapsed/s, 1))
			PointAt(fx+(tx-fx)*f, fy+(ty-fy)*f)
		}
		r.release = func() {
			PointAt(tx, ty)
			_ = MouseButton(b, false)
		}
		r.t.Emit("DRAG", b.Atom(), fmt.Sprintf("%.0f,%.0f -> %.0f,%.0f", fx, fy, tx, ty))

	case "click":
		screen, widget, _ := cutLast(c.Args[0], ":")
		if err := Click(screen, widget); err != nil {
			return err
		}
		r.t.Emit("CLICK", c.Args[0], "")

	case "teleport":
		p, err := Point(lvl, strings.Join(c.Args, ","), Eye(lvl))
		if err != nil {
			return err
		}
		Teleport(p)
		r.t.Emit("PLAYER", "teleport", vec(p))

	case "enable":
		if !Enable(lvl, c.Args[0]) {
			return fmt.Errorf("no chunk or volume %q", c.Args[0])
		}
		r.t.Emit("ENABLE", c.Args[0], "")
	case "wake":
		n := Wake(lvl, strings.TrimPrefix(c.Args[0], "#"))
		r.t.Emit("WAKE", c.Args[0], fmt.Sprint(n))
	case "kill":
		n := Kill(lvl, strings.TrimPrefix(c.Args[0], "#"))
		r.t.Emit("KILL", c.Args[0], fmt.Sprint(n))

	case "read":
		r.t.Emit("READ", c.Args[0], r.t.Read(c.Args[0]))
	case "say":
		r.t.Emit("SAY", "", c.Args[0])

	case "view":
		note, err := render.SetView(c.Args[0])
		if err != nil {
			return err
		}
		r.t.Emit("VIEW", render.ViewName(), note)

	case "shot":
		name := ""
		if len(c.Args) > 0 {
			name = c.Args[0]
		}
		path, err := r.t.Shot(name)
		if err != nil {
			return err
		}
		r.shots = append(r.shots, path)
		r.t.Emit("SHOT", path, "")
	}
	return nil
}

// ---------------------------------------------------------------- arguments

// seconds reads a duration. A bare number is seconds; "250ms" and "2s" are
// allowed because they are what anyone types for a short one.
func seconds(s string) (float64, error) {
	switch {
	case strings.HasSuffix(s, "ms"):
		v, err := strconv.ParseFloat(strings.TrimSuffix(s, "ms"), 64)
		return v / 1000, err
	case strings.HasSuffix(s, "s"):
		v, err := strconv.ParseFloat(strings.TrimSuffix(s, "s"), 64)
		return v, err
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number of seconds", s)
	}
	return v, nil
}

func number(s string) (float64, error) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", s)
	}
	return v, nil
}

func onOff(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "on", "true", "yes", "1", "down", "hold":
		return true, nil
	case "off", "false", "no", "0", "up", "release":
		return false, nil
	}
	return false, fmt.Errorf("%q is not on or off", s)
}

// parseVec reads x,y,z — or "x y z", which Point hands in joined by commas, so
// both spellings arrive here the same.
func parseVec(s string) (mgl32.Vec3, bool) {
	parts := strings.Split(s, ",")
	if len(parts) != 3 {
		return mgl32.Vec3{}, false
	}
	var v mgl32.Vec3
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 32)
		if err != nil {
			return mgl32.Vec3{}, false
		}
		v[i] = float32(f)
	}
	return v, true
}

// cutLast splits on the LAST separator, so a screen named with a colon in it
// still finds its widget.
func cutLast(s, sep string) (before, after string, found bool) {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return "", s, false
}

func vec(v mgl32.Vec3) string {
	return fmt.Sprintf("%.1f,%.1f,%.1f", v[0], v[1], v[2])
}

// pixel reads a window position, "x,y" in pixels from the top-left, or
// "center" for the middle of the window.
func pixel(s string) (float32, float32, error) {
	if s == "center" || s == "centre" || s == "middle" {
		return float32(platform.WinWidth) / 2, float32(platform.WinHeight) / 2, nil
	}
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("%q is not a window position; write x,y in pixels, or center", s)
	}
	x, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 32)
	y, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 32)
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("%q is not a window position; write x,y in pixels, or center", s)
	}
	return float32(x), float32(y), nil
}

// mouseButton reads an optional button name at args[i]; left when absent.
func mouseButton(args []string, i int) (hooks.PointerButton, error) {
	if i >= len(args) {
		return hooks.PointerLeft, nil
	}
	switch args[i] {
	case "left":
		return hooks.PointerLeft, nil
	case "right":
		return hooks.PointerRight, nil
	case "middle":
		return hooks.PointerMiddle, nil
	}
	return 0, fmt.Errorf("%q is not a mouse button; left, right or middle", args[i])
}
