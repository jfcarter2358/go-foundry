// Package scorecli is `foundry score`: the Score editor's headless verbs.
//
// Like trackercli it is its own package so the score core stays free of
// flags, files on the command line and the audio device.
package scorecli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"foundry/internal/audio"
	"foundry/internal/fileutil"
	"foundry/internal/score"
	"foundry/internal/score/interpret"
	"foundry/internal/score/play"
	"foundry/internal/tracker"
)

// Command is the verb on the foundry binary.
const Command = "score"

const usage = `foundry score — work on notated scores (sounds/*.fscore).

  foundry score list                         scores and how long each runs
  foundry score inspect <score>              lines, staves, maps, sections, and what is wrong
  foundry score check [score …]              does it parse, validate, find its instruments (all scores with no names)
  foundry score render <score> [flags]       render to a WAV (-o, -section, -from, -seconds, -tempo, -param k=v)
  foundry score render <score> -png out.png  engrave it to an image (-bars 5-8, -scale px/space, -lines 1,3,
                                             -concert, -spacing k, -bar-numbers n)
  foundry score play <score> [flags]         play on the audio device (-section, -from, -tempo, -seconds)
  foundry score fmt [-check] [score …]       rewrite scores in canonical form (all of them with no names)
  foundry score import <file> [-o name]      a MIDI (.mid) or MusicXML (.musicxml, .xml, .mxl) file as a score;
                                             -list shows its parts and writes nothing (see score import -h)
  foundry score export-midi <score> [-o f]   the score as a Standard MIDI File (-no-repeats: as written)

A score is a name under sounds/ without the extension, or a path to a file.
Every verb takes -root <project> before its arguments.
`

// Main runs the command and returns the process exit code.
func Main(root string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet(Command, flag.ContinueOnError)
	fs.SetOutput(out)
	project := fs.String("root", root, "project directory: the one holding resources/")
	fs.Usage = func() { fmt.Fprint(out, usage) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return 2
	}
	abs, err := filepath.Abs(fileutil.ResourcesDir(*project))
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	fileutil.ResourcesPath = abs

	sub, rest := fs.Arg(0), fs.Args()[1:]
	switch sub {
	case "list":
		return list(out)
	case "inspect":
		return inspect(rest, out)
	case "check":
		return check(rest, out)
	case "render":
		return render(rest, out)
	case "play":
		return playCmd(rest, out)
	case "fmt":
		return fmtCmd(rest, out)
	case "import":
		return importCmd(rest, out)
	case "export-midi":
		return exportMIDI(rest, out)
	}
	fmt.Fprintf(out, "score: unknown verb %q\n\n", sub)
	fs.Usage()
	return 2
}

// load reads a score by library name or path.
func load(arg string) (*score.Score, string, error) {
	name := strings.TrimSuffix(arg, score.FileExt)
	if p := score.ScorePath(name); p != "" {
		s, err := score.LoadFile(p)
		return s, p, err
	}
	if st, err := os.Stat(arg); err == nil && !st.IsDir() {
		s, err := score.LoadFile(arg)
		return s, arg, err
	}
	return nil, "", fmt.Errorf("no score %q (a name under %s, or a file)", name, audio.SoundsDir())
}

func instrumentExists(name string) bool { return tracker.InstrumentPath(name) != "" }

func clock(secs float64) string {
	m := int(secs) / 60
	return fmt.Sprintf("%d:%04.1f", m, secs-float64(m*60))
}

func list(out io.Writer) int {
	for _, n := range score.ScanScoreNames() {
		s, err := score.LoadFile(score.ScorePath(n))
		if err != nil {
			fmt.Fprintf(out, "%-32s  error: %v\n", n, err)
			continue
		}
		perf, err := interpret.Interpret(s, interpret.Options{})
		if err != nil {
			fmt.Fprintf(out, "%-32s  error: %v\n", n, err)
			continue
		}
		fmt.Fprintf(out, "%-32s  %s  %3d bars  %2d lines  %d sections  %q\n", n, clock(perf.Seconds), s.Measures(), len(s.Lines), len(s.Sections), s.Title)
	}
	return 0
}

func inspect(rest []string, out io.Writer) int {
	if len(rest) == 0 {
		fmt.Fprintln(out, "score inspect: name a score")
		return 2
	}
	s, path, err := load(rest[0])
	if err != nil {
		fmt.Fprintf(out, "score inspect: %v\n", err)
		return 1
	}
	perf, err := interpret.Interpret(s, interpret.Options{})
	if err != nil {
		fmt.Fprintf(out, "score inspect: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "%s — %q", s.Name, s.Title)
	if s.Composer != "" {
		fmt.Fprintf(out, " by %s", s.Composer)
	}
	fmt.Fprintf(out, "\n  %s\n", path)
	loop := ""
	if s.Loop {
		loop = ", loops"
	}
	fmt.Fprintf(out, "  %d bars, plays %d (repeats unrolled), %s, %d notes%s\n", s.Measures(), len(perf.Bars), clock(perf.Seconds), len(perf.Notes), loop)
	if s.Pickup.Sign() > 0 {
		fmt.Fprintf(out, "  pickup of %s\n", s.Pickup)
	}
	for _, t := range s.Tempo {
		u := t.Unit
		if u == (score.Duration{}) {
			u = score.Duration{Base: score.Quarter}
		}
		ramp := ""
		if t.Ramp {
			ramp = " (ramps to the next)"
		}
		text := ""
		if t.Text != "" {
			text = " " + t.Text
		}
		fmt.Fprintf(out, "  tempo  %-8s %s = %g%s%s\n", s.FormatPos(t.At), u, t.BPM, text, ramp)
	}
	for _, m := range s.Meter {
		fmt.Fprintf(out, "  meter  bar %-4d %s\n", m.Bar, m.Meter)
	}
	for _, k := range s.Key {
		fmt.Fprintf(out, "  key    bar %-4d %s %s\n", k.Bar, keyName(k.Fifths, k.Mode), k.Mode)
	}
	for _, r := range s.Repeats {
		fmt.Fprintf(out, "  repeat bars %d..%d x%d\n", r.Start, r.End, r.Plays())
	}
	for _, sp := range perf.Sections {
		next := ""
		switch {
		case sp.Loop:
			next = ", loops"
		case sp.Next >= 0:
			next = ", then " + perf.Sections[sp.Next].Name
		}
		fmt.Fprintf(out, "  section %-14s %s..%s%s\n", sp.Name, clock(sp.Start), clock(sp.EndSec), next)
	}
	for _, m := range s.Markers {
		fmt.Fprintf(out, "  marker %-15s %s\n", m.Name, s.FormatPos(m.At))
	}
	for _, p := range s.Params {
		fmt.Fprintf(out, "  param  %-15s default %.2f\n", p.Name, p.Default)
	}
	counts := make([]int, len(s.Lines))
	lo, hi := make([]int, len(s.Lines)), make([]int, len(s.Lines))
	for i := range lo {
		lo[i], hi[i] = 999, -1
	}
	for _, n := range perf.Notes {
		counts[n.Line]++
		lo[n.Line], hi[n.Line] = min(lo[n.Line], n.MIDI), max(hi[n.Line], n.MIDI)
	}
	var rangeIssues []score.Issue
	for i, l := range s.Lines {
		state := "missing"
		var in *tracker.Instrument
		if p := tracker.InstrumentPath(l.Instrument); p != "" {
			state = "ok"
			if lib, err := tracker.LoadInstrumentFile(p); err == nil {
				in = lib
			} else {
				state = "error: " + err.Error()
			}
		}
		fmt.Fprintf(out, "  line %d %-16s %-28s %s  vol %.2f pan %+.2f", i+1, l.Name, l.Instrument, state, l.Volume, l.Pan)
		if l.Mute {
			fmt.Fprint(out, " muted")
		}
		if l.Solo {
			fmt.Fprint(out, " solo")
		}
		fmt.Fprintln(out)
		for si, st := range l.Staves {
			tr := ""
			if st.Transpose != (score.Interval{}) {
				tr = " transposed " + st.Transpose.String()
			}
			fmt.Fprintf(out, "         staff %d %s%s, %d voice(s)\n", si+1, st.Clef, tr, len(st.Voices))
		}
		if counts[i] > 0 {
			fmt.Fprintf(out, "         %d notes, %s..%s\n", counts[i], score.SpellMIDI(lo[i], 0, 1), score.SpellMIDI(hi[i], 0, 1))
		}
		if in != nil {
			pb := in.Playback
			low, high := 0, 0
			for _, n := range perf.Notes {
				if n.Line != i {
					continue
				}
				// Playback ranges are tracker note numbers: MIDI - 11.
				tn := n.MIDI - 60 + int(tracker.NoteC4)
				if tn < 0 || tn > 255 || !pb.InRange(uint8(tn)) {
					if pb.Low != 0 && tn < int(pb.Low) {
						low++
					} else {
						high++
					}
				}
			}
			if low+high > 0 {
				rangeIssues = append(rangeIssues, score.Issue{Severity: score.Warning, Line: i + 1,
					Msg: fmt.Sprintf("%d note(s) below and %d above %s's range", low, high, l.Instrument)})
			}
		}
	}
	for _, u := range play.Unplayed(s) {
		fmt.Fprintf(out, "  not played yet (effects rack is a later phase): %s\n", u)
	}
	issues := append(s.Validate(instrumentExists), rangeIssues...)
	for _, is := range issues {
		fmt.Fprintf(out, "  %s\n", is)
	}
	if score.HasErrors(issues) {
		return 1
	}
	return 0
}

func keyName(fifths int, mode string) string {
	major := []string{"Cb", "Gb", "Db", "Ab", "Eb", "Bb", "F", "C", "G", "D", "A", "E", "B", "F#", "C#"}
	minor := []string{"Ab", "Eb", "Bb", "F", "C", "G", "D", "A", "E", "B", "F#", "C#", "G#", "D#", "A#"}
	if fifths < -7 || fifths > 7 {
		return "?"
	}
	if mode == "minor" {
		return minor[fifths+7]
	}
	return major[fifths+7]
}

// check validates the named scores (names or paths), or every score in
// the project when none is named.
func check(rest []string, out io.Writer) int {
	bad := 0
	names := rest
	if len(names) == 0 {
		names = score.ScanScoreNames()
	}
	for _, n := range names {
		s, path, err := load(n)
		if err != nil {
			if path != "" && strings.HasPrefix(err.Error(), path) {
				fmt.Fprintln(out, err)
			} else {
				fmt.Fprintf(out, "%s: %v\n", n, err)
			}
			bad++
			continue
		}
		issues := s.Validate(instrumentExists)
		if _, err := interpret.Interpret(s, interpret.Options{}); err != nil {
			issues = append(issues, score.Issue{Severity: score.Error, Msg: err.Error()})
		}
		for _, is := range issues {
			if is.Severity == score.Error {
				fmt.Fprintf(out, "%s: %s\n", n, is)
			}
		}
		if score.HasErrors(issues) {
			bad++
		}
	}
	if bad > 0 {
		fmt.Fprintf(out, "%d score(s) with problems\n", bad)
		return 1
	}
	fmt.Fprintf(out, "all %d score(s) ok\n", len(names))
	return 0
}

// playFlags are the flags render and play share.
type playFlags struct {
	section, from string
	seconds       float64
	tempo         float64
	params        map[string]float32
}

func (pf *playFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&pf.section, "section", "", "start at this section (rehearsal mark)")
	fs.StringVar(&pf.from, "from", "", "start at a bar or bar:beat (e.g. 9, 9:3)")
	fs.Float64Var(&pf.seconds, "seconds", 0, "length; 0 is to the end")
	fs.Float64Var(&pf.tempo, "tempo", 1, "tempo scale, 0.25..2 (the score is not changed)")
	pf.params = map[string]float32{}
	fs.Func("param", "set a score param before playing: name=value (repeatable)", func(v string) error {
		k, val, ok := strings.Cut(v, "=")
		f, err := strconv.ParseFloat(val, 32)
		if !ok || err != nil {
			return fmt.Errorf("want name=value, got %q", v)
		}
		pf.params[k] = float32(f)
		return nil
	})
}

// start resolves -section and -from against a prepared score.
func (pf *playFlags) start(s *score.Score, perf *interpret.Performance) (string, int64, error) {
	if pf.tempo < 0.25 || pf.tempo > 2 {
		return "", 0, fmt.Errorf("-tempo %g: the tempo scale runs from 0.25 to 2", pf.tempo)
	}
	if pf.section != "" {
		if perf.SectionIndex(pf.section) < 0 {
			return "", 0, fmt.Errorf("no section %q", pf.section)
		}
		return pf.section, 0, nil
	}
	if pf.from == "" {
		return "", 0, nil
	}
	pos, err := s.ParsePos(pf.from)
	if err != nil {
		return "", 0, err
	}
	tick := perf.BarTick(pos.Bar)
	if tick < 0 {
		return "", 0, fmt.Errorf("bar %d is not in the score", pos.Bar)
	}
	return "", tick + pos.Offset.Ticks(), nil
}

// splitArgs separates the score name from the flags around it.
func splitArgs(rest []string) (string, []string) {
	for i, a := range rest {
		if !strings.HasPrefix(a, "-") && (i == 0 || !strings.HasPrefix(rest[i-1], "-") || !needsValue(rest[i-1])) {
			return a, append(append([]string(nil), rest[:i]...), rest[i+1:]...)
		}
	}
	return "", rest
}

func needsValue(flag string) bool {
	if strings.Contains(flag, "=") {
		return false
	}
	switch strings.TrimLeft(flag, "-") {
	case "check", "list", "force", "no-dynamics", "no-legato", "no-graces", "no-repeats", "concert":
		return false
	}
	return true
}

func render(rest []string, out io.Writer) int {
	fs := flag.NewFlagSet("score render", flag.ContinueOnError)
	fs.SetOutput(out)
	dst := fs.String("o", "", "output .wav (default <score>.wav in the working directory)")
	tail := fs.Float64("tail", 2, "seconds of reverb and release after the end")
	rate := fs.Int("rate", 48000, "sample rate")
	var pf playFlags
	pf.register(fs)
	var ef engraveFlags
	ef.register(fs)
	name, flags := splitArgs(rest)
	if err := fs.Parse(flags); err != nil {
		return 2
	}
	if name == "" {
		fmt.Fprintln(out, "score render: name a score")
		return 2
	}
	s, _, err := load(name)
	if err != nil {
		fmt.Fprintf(out, "score render: %v\n", err)
		return 1
	}
	if ef.png != "" {
		return renderPNG(s, ef, out)
	}
	prog, errs, err := play.Prepare(s)
	if err != nil {
		fmt.Fprintf(out, "score render: %v\n", err)
		return 1
	}
	for _, e := range errs {
		fmt.Fprintf(out, "warning: %v\n", e)
	}
	// A score with errors still renders (that is how to hear what is
	// wrong), but says so.
	for _, is := range s.Validate(nil) {
		if is.Severity == score.Error {
			fmt.Fprintln(out, is)
		}
	}
	section, from, err := pf.start(s, prog.Perf)
	if err != nil {
		fmt.Fprintf(out, "score render: %v\n", err)
		return 1
	}
	if *dst == "" {
		*dst = filepath.Base(s.Name) + ".wav"
	}
	data := play.Render(prog, play.RenderOptions{Rate: *rate, Section: section, From: from, Seconds: pf.seconds, Tail: *tail, TempoScale: pf.tempo, Params: pf.params})
	if err := play.WriteWAV(*dst, data, *rate); err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	pk := tracker.Peak(data)
	fmt.Fprintf(out, "%s: %.1f s, peak %.2f, hash %s\n", *dst, float64(len(data)/2)/float64(*rate), pk, tracker.Hash(data))
	if pk > 1 {
		fmt.Fprintf(out, "warning: the mix clips (peak %.2f); lower the score's gain to %.2f or less\n", pk, s.Gain/pk)
	}
	return 0
}

func playCmd(rest []string, out io.Writer) int {
	fs := flag.NewFlagSet("score play", flag.ContinueOnError)
	fs.SetOutput(out)
	var pf playFlags
	pf.register(fs)
	name, flags := splitArgs(rest)
	if err := fs.Parse(flags); err != nil {
		return 2
	}
	if name == "" {
		fmt.Fprintln(out, "score play: name a score")
		return 2
	}
	s, _, err := load(name)
	if err != nil {
		fmt.Fprintf(out, "score play: %v\n", err)
		return 1
	}
	p, errs := play.New(s)
	if p == nil {
		fmt.Fprintf(out, "score play: %v\n", errs[len(errs)-1])
		return 1
	}
	for _, e := range errs {
		fmt.Fprintf(out, "warning: %v\n", e)
	}
	section, from, err := pf.start(s, p.Performance())
	if err != nil {
		fmt.Fprintf(out, "score play: %v\n", err)
		return 1
	}
	if err := sdl.Init(sdl.INIT_AUDIO); err != nil {
		fmt.Fprintf(out, "score play: no audio (%v)\n", err)
		return 1
	}
	defer sdl.Quit()
	if err := audio.Init(audio.Config{}); err != nil || !audio.Available() {
		fmt.Fprintf(out, "score play: no audio device (%v); use `foundry score render` instead\n", err)
		return 1
	}
	defer audio.Shutdown()
	for k, v := range pf.params {
		p.SetParamNow(k, v)
	}
	if section != "" {
		p.Start(section)
	} else {
		p.StartAt(from)
	}
	p.TempoScale(pf.tempo, 0)
	id := audio.PlayGenerator(p, audio.PlayOptions{Bus: audio.BusMusic, Priority: audio.PriorityHigh})
	if id.IsZero() {
		fmt.Fprintln(out, "score play: the mixer would not take the voice")
		return 1
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	defer signal.Stop(stop)
	started := time.Now()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	fmt.Fprintf(out, "playing %s (ctrl-C stops)\n", s.Name)
	var evs []play.Event
	for {
		select {
		case <-stop:
			audio.FadeOut(id, 0.3)
			time.Sleep(350 * time.Millisecond)
			fmt.Fprintln(out, "\nstopped")
			return 0
		case <-tick.C:
		}
		evs = p.Events(evs[:0])
		ended := false
		for _, e := range evs {
			switch e.Kind {
			case play.EventSection:
				fmt.Fprintf(out, "\n§ %s\n", e.Name)
			case play.EventMarker:
				fmt.Fprintf(out, "\nmarker %s\n", e.Name)
			case play.EventEnd:
				ended = true
			}
		}
		pos := p.Position()
		fmt.Fprintf(out, "\r  bar %3d beat %d  %s ", pos.Bar, pos.Beat, clock(pos.Seconds))
		if ended || (pf.seconds > 0 && time.Since(started).Seconds() >= pf.seconds) {
			if !ended {
				audio.FadeOut(id, 0.3)
			}
			// Let the tail ring out.
			time.Sleep(1500 * time.Millisecond)
			fmt.Fprintln(out)
			return 0
		}
	}
}

func fmtCmd(rest []string, out io.Writer) int {
	fs := flag.NewFlagSet("score fmt", flag.ContinueOnError)
	fs.SetOutput(out)
	only := fs.Bool("check", false, "report scores that are not canonical; write nothing")
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	names := fs.Args()
	if len(names) == 0 {
		names = score.ScanScoreNames()
	}
	sort.Strings(names)
	code := 0
	for _, n := range names {
		s, path, err := load(n)
		if err != nil {
			fmt.Fprintf(out, "%s: %v\n", n, err)
			code = 1
			continue
		}
		was, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(out, err)
			code = 1
			continue
		}
		b, err := score.Marshal(s)
		if err != nil {
			fmt.Fprintf(out, "%s: %v\n", n, err)
			code = 1
			continue
		}
		if string(b) == string(was) {
			continue
		}
		if *only {
			fmt.Fprintf(out, "%s: not in canonical form\n", path)
			code = 1
			continue
		}
		if err := os.WriteFile(path, b, 0o644); err != nil {
			fmt.Fprintln(out, err)
			code = 1
			continue
		}
		score.Reload()
		fmt.Fprintf(out, "%s: rewritten\n", path)
	}
	return code
}
