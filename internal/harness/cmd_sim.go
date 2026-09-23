package harness

// cmd_sim.go — `foundry sim`, the harness on the command line.
//
// Linting proves a script's ids resolve (`foundry check`). It does not prove the
// thing works — that the gun winds up, fires, cools and traverses, or that
// planting a charge starts a countdown that ends in an explosion. Playing the
// game proves it, at the cost of twenty minutes per attempt. This is the middle:
// the real loader, the real frame, the real script runtime, driven from the
// command line with a watch list printed as it changes.
//
//	foundry sim -level level1 -seconds 30 -watch 'aa_turret.rotation.y'
//	foundry sim -scenario scenarios/breach.yaml
//
// The frame is the whole frame — physics, collision, AI and scripts — because
// the alternative was a harness that quietly skipped the half of the engine the
// question was usually about. `-scripts-only` asks for the cheap subset back.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
)

// SimCommand is the verb: `foundry sim`.
const SimCommand = "sim"

// SimMain runs one simulation from the command line. root is the project to work
// on unless -root or -resources says otherwise.
func SimMain(root string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet(SimCommand, flag.ContinueOnError)
	fs.SetOutput(out)
	project := fs.String("root", root, "project directory: the one holding resources/")
	res := fs.String("resources", "", "the resources/ directory itself, if it is not <root>/resources")
	level := fs.String("level", "", "level name (required, unless -scenario names one)")
	scenario := fs.String("scenario", "", "run a scenario file instead of the flags below")
	seconds := fs.Float64("seconds", 20, "how long to run")
	hz := fs.Float64("hz", 60, "tick rate")
	watch := fs.String("watch", "", "comma-separated things to print when they change")
	at := fs.String("at", "", "teleport the player: 'x,y,z@seconds' separated by ';'")
	walkTo := fs.String("walk-to", "", "walk the player there on foot: 'x,y,z@seconds' separated by ';'")
	press := fs.String("press", "", "seconds at which to press interact, comma separated")
	enable := fs.String("enable", "", "chunks/volumes to switch on before the run, comma separated")
	every := fs.Float64("every", 0, "also print the whole watch list this often, in seconds")
	compiled := fs.Bool("compiled", false, "load the level's .fbsp, as a release build does, instead of its .yaml")
	restoreAt := fs.String("restore-at", "", "seconds at which to save the game, load the level again from its file and restore the save over it, comma separated")
	scriptsOnly := fs.Bool("scripts-only", false, "run only the script half of the frame: no AI, no physics, nothing that moves a body")
	god := fs.Bool("god", false, "keep the player alive: health held far over anything one tick can take")
	kill := fs.String("kill", "", "send (:damage:, @player, 9999) to every entity with a tag: 'tag@seconds' separated by ';'")
	wake := fs.String("wake", "", "switch on the AI controller of, and show, every entity with a tag: 'tag@seconds' separated by ';' — what a level's zone scripts do when the player comes near")
	menus := fs.String("menus", "answer", "what to do about a modal screen that comes up: `answer` clicks the widget its initial_focus names, once per screen, so a run does not stop at a menu; `hold` leaves it up, as a player who has not touched it")
	cpuProfile := fs.String("cpuprofile", "", "write a CPU profile of the run to this file (go tool pprof)")
	fs.Usage = func() {
		fmt.Fprintf(out, "foundry %s — run a level headlessly and report what moved.\n\nFlags:\n", SimCommand)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *level == "" && *scenario == "" {
		fs.Usage()
		return 2
	}

	opts := Options{
		Resources:   *res,
		Project:     *project,
		Level:       *level,
		Compiled:    *compiled,
		Hz:          *hz,
		ScriptsOnly: *scriptsOnly,
		God:         *god,
		Menus:       *menus,
		OnEvent:     printer(out),
	}

	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			fmt.Fprintln(out, "cpuprofile:", err)
			return 1
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Fprintln(out, "cpuprofile:", err)
			return 1
		}
		defer pprof.StopCPUProfile()
	}

	if *scenario != "" {
		sc, err := LoadScenario(*scenario)
		if err != nil {
			fmt.Fprintln(out, "scenario:", err)
			return 1
		}
		fails, err := sc.Run(opts)
		if err != nil {
			fmt.Fprintln(out, err)
			return 1
		}
		if len(fails) > 0 {
			fmt.Fprintf(out, "\n%d expectations failed\n", len(fails))
			return 1
		}
		fmt.Fprintln(out, "\nall expectations held")
		return 0
	}

	s, err := Open(opts)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	s.Watch(parseWatches(*watch))
	for _, id := range splitList(*enable) {
		s.Enable(id)
	}

	moves, walks := parseMoves(*at), parseMoves(*walkTo)
	presses, restores := parseTimes(*press), parseTimes(*restoreAt)
	kills, wakes := parseTags(*kill), parseTags(*wake)
	nextDump := 0.0
	var walkTarget *moveSpec

	for s.T < *seconds {
		for _, m := range moves {
			if m.fire(s.T) {
				s.Teleport(m.to)
			}
		}
		for _, m := range walks {
			if m.fire(s.T) {
				walkTarget = m
			}
		}
		for _, p := range presses {
			if p.fire(s.T) {
				s.Press()
			}
		}
		for _, k := range kills {
			if k.fire(s.T) {
				s.Kill(k.tag)
			}
		}
		for _, w := range wakes {
			if w.fire(s.T) {
				s.Wake(w.tag)
			}
		}
		for _, r := range restores {
			if r.fire(s.T) {
				s.RoundTrip()
			}
		}
		if walkTarget != nil && !s.WalkToward(walkTarget.to, WalkSpeed()) {
			walkTarget = nil
			s.Walk(mgl32.Vec3{})
		}
		s.Step()
		if *every > 0 && s.T >= nextDump {
			nextDump = s.T + *every
			s.Dump()
			fmt.Fprintf(out, "%7.2f  ----\n", s.T)
		}
	}
	fmt.Fprintln(out, "\ndone")
	return 0
}

// printer formats the run's events, one per line, the way levelsim always
// printed them: the second it happened, then what happened.
func printer(out io.Writer) func(Event) {
	return func(e Event) {
		switch e.Kind {
		case "NOTE":
			if e.Name == "loaded" {
				fmt.Fprintf(out, "loaded %s\n\n", e.Value)
				return
			}
			fmt.Fprintf(out, "%7.2f  %s %s\n", e.T, strings.ToUpper(e.Name), e.Value)
		case "WATCH":
			fmt.Fprintf(out, "%7.2f  %-34s %s\n", e.T, e.Name, e.Value)
		default:
			line := e.Kind
			if e.Name != "" {
				line += " " + e.Name
			}
			if e.Value != "" {
				line += " " + e.Value
			}
			fmt.Fprintf(out, "%7.2f  %s\n", e.T, line)
		}
	}
}

// ---------------------------------------------------------------- flag parsing

func parseWatches(s string) []string {
	out := splitList(s)
	sort.Strings(out)
	return out
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// spec is one thing that happens once, at a time. fire reports the tick it is
// due on and never again.
type spec struct {
	at   float64
	done bool
}

func (s *spec) fire(now float64) bool {
	if s.done || now < s.at {
		return false
	}
	s.done = true
	return true
}

type moveSpec struct {
	spec
	to mgl32.Vec3
}

// parseMoves reads 'x,y,z@seconds;...'.
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
		out = append(out, &moveSpec{spec: spec{at: at}, to: v})
	}
	return out
}

func parseTimes(s string) []*spec {
	var out []*spec
	for _, p := range splitList(s) {
		at, _ := strconv.ParseFloat(p, 64)
		out = append(out, &spec{at: at})
	}
	return out
}

type tagSpec struct {
	spec
	tag string
}

// parseTags reads 'tag@seconds;...'.
func parseTags(s string) []*tagSpec {
	var out []*tagSpec
	for _, p := range strings.Split(s, ";") {
		tag, at, ok := strings.Cut(strings.TrimSpace(p), "@")
		if !ok || tag == "" {
			continue
		}
		sec, _ := strconv.ParseFloat(at, 64)
		out = append(out, &tagSpec{spec: spec{at: sec}, tag: tag})
	}
	return out
}
