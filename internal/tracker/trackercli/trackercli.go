// Package trackercli is `foundry tracker`: the music tracker's headless verbs.
//
// Its own package rather than part of internal/tracker because it reaches both
// the tracker core and the sample synthesizer (internal/tracker/synth), which
// itself builds on the core — a command in the core would be an import cycle.
package trackercli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"foundry/internal/audio"
	"foundry/internal/fileutil"
	"foundry/internal/tracker"
)

// Command is the verb on the foundry binary.
const Command = "tracker"

const usage = `foundry tracker — work on tracker songs (sounds/*.ftrk) and the sample library.

  foundry tracker list                       songs and how long each runs
  foundry tracker inspect <song>             channels, sections, instruments, samples
  foundry tracker check                      every song and instrument: does it parse, do its samples load
  foundry tracker render <song> [flags]      render to a WAV (-o, -section, -seconds, -param k=v)
  foundry tracker samples                    the sample library under sounds/samples/
  foundry tracker instruments                the instrument library under sounds/instruments/
  foundry tracker new <song>                 write an empty song
  foundry tracker gen-samples [-only a,b]    (re)generate the built-in sample library
  foundry tracker gen-instruments [-only a]  (re)generate the starter instruments (.finst) and their samples

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
		return check(out)
	case "render":
		return render(rest, out)
	case "samples":
		for _, n := range tracker.ScanSampleNames() {
			fmt.Fprintln(out, n)
		}
		return 0
	case "instruments":
		return instruments(out)
	case "new":
		return newSong(rest, out)
	case "gen-samples":
		if GenSamples == nil {
			fmt.Fprintln(out, "tracker gen-samples: the sample synthesizer is not built into this binary")
			return 1
		}
		return GenSamples(rest, out)
	case "gen-instruments":
		if GenInstruments == nil {
			fmt.Fprintln(out, "tracker gen-instruments: the sample synthesizer is not built into this binary")
			return 1
		}
		return GenInstruments(rest, out)
	}
	fmt.Fprintf(out, "tracker: unknown verb %q\n\n", sub)
	fs.Usage()
	return 2
}

// GenSamples and GenInstruments are the synthesizer's entry points,
// installed by gen.go.
var GenSamples, GenInstruments func(args []string, out io.Writer) int

// instruments lists the instrument library: name, articulations, range.
func instruments(out io.Writer) int {
	for _, n := range tracker.ScanInstrumentNames() {
		in, err := tracker.LoadInstrumentFile(tracker.InstrumentPath(n))
		if err != nil {
			fmt.Fprintf(out, "%-36s  error: %v\n", n, err)
			continue
		}
		var arts []string
		for _, a := range in.Articulations {
			arts = append(arts, a.Name)
		}
		rng := ""
		if pb := in.Playback; pb.Low != 0 || pb.High != 0 {
			rng = tracker.NoteName(max(pb.Low, tracker.NoteMin)) + ".." + tracker.NoteName(max(pb.High, pb.Low))
		}
		fmt.Fprintf(out, "%-36s  %-24s %-10s %-11s %s\n", n, in.Name, in.Playback.Clef, rng, strings.Join(arts, ","))
	}
	return 0
}

func list(out io.Writer) int {
	for _, n := range tracker.ScanSongNames() {
		s, err := tracker.LoadFile(tracker.SongPath(n))
		if err != nil {
			fmt.Fprintf(out, "%-32s  error: %v\n", n, err)
			continue
		}
		d := s.Duration()
		fmt.Fprintf(out, "%-32s  %d:%04.1f  %3.0f BPM  %2d ch  %d sections\n", n, int(d)/60, d-float64(int(d)/60*60), s.BPM, len(s.Channels), len(s.Sections))
	}
	return 0
}

func loadArg(rest []string, out io.Writer, verb string) (*tracker.Song, string, bool) {
	if len(rest) == 0 {
		fmt.Fprintf(out, "tracker %s: name a song (a path under sounds/, no extension)\n", verb)
		return nil, "", false
	}
	name := strings.TrimSuffix(rest[0], tracker.FileExt)
	path := tracker.SongPath(name)
	if path == "" {
		if st, err := os.Stat(rest[0]); err == nil && !st.IsDir() {
			path = rest[0]
		} else {
			fmt.Fprintf(out, "tracker %s: no song %q\n", verb, name)
			return nil, "", false
		}
	}
	s, err := tracker.LoadFile(path)
	if err != nil {
		fmt.Fprintf(out, "tracker %s: %v\n", verb, err)
		return nil, "", false
	}
	return s, name, true
}

func inspect(rest []string, out io.Writer) int {
	s, name, ok := loadArg(rest, out, "inspect")
	if !ok {
		return 1
	}
	errs := tracker.ResolveSamples(s)
	d := s.Duration()
	fmt.Fprintf(out, "%s — %q\n", name, s.Name)
	fmt.Fprintf(out, "  %.0f BPM, %d rows/beat, %d ticks/row, %d/4, one pass %d:%04.1f\n", s.BPM, s.LPB, s.TicksPerRow, s.BeatsPerBar, int(d)/60, d-float64(int(d)/60*60))
	fmt.Fprintf(out, "  %d patterns, order %v\n", len(s.Patterns), s.Order)
	fmt.Fprintf(out, "  channels:")
	for i, c := range s.Channels {
		fmt.Fprintf(out, " %d:%s", i+1, c.Name)
	}
	fmt.Fprintln(out)
	for _, sec := range s.Sections {
		next := ""
		switch {
		case sec.Loop:
			next = ", loops"
		case sec.Next != "":
			next = ", then " + sec.Next
		}
		fmt.Fprintf(out, "  section %-16s order %d..%d%s\n", sec.Name, sec.Start, sec.End-1, next)
	}
	for _, p := range s.Params {
		fmt.Fprintf(out, "  param %-18s default %.2f\n", p.Name, p.Default)
	}
	for i, in := range s.Instruments {
		if in.Ref != "" {
			fmt.Fprintf(out, "  instrument %02X %-18s library %s %v\n", i+1, in.Name, in.Ref, in.Samples)
			continue
		}
		fmt.Fprintf(out, "  instrument %02X %-18s %v\n", i+1, in.Name, in.Samples)
	}
	for _, err := range errs {
		fmt.Fprintf(out, "  missing: %v\n", err)
	}
	if len(errs) > 0 {
		return 1
	}
	return 0
}

func check(out io.Writer) int {
	bad := 0
	for _, n := range tracker.ScanSongNames() {
		s, err := tracker.LoadFile(tracker.SongPath(n))
		if err != nil {
			fmt.Fprintf(out, "%s: %v\n", n, err)
			bad++
			continue
		}
		probs := s.Validate()
		for _, e := range tracker.ResolveSamples(s) {
			probs = append(probs, e.Error())
		}
		for _, p := range probs {
			fmt.Fprintf(out, "%s: %s\n", n, p)
		}
		if len(probs) > 0 {
			bad++
		}
	}
	badInst := 0
	for _, n := range tracker.ScanInstrumentNames() {
		in, err := tracker.LoadInstrumentFile(tracker.InstrumentPath(n))
		if err != nil {
			fmt.Fprintf(out, "%s: %v\n", n, err)
			badInst++
			continue
		}
		errs := in.Resolve(tracker.SampleFor)
		for _, e := range errs {
			fmt.Fprintf(out, "instrument %s: %v\n", n, e)
		}
		if len(errs) > 0 {
			badInst++
		}
	}
	if bad > 0 || badInst > 0 {
		fmt.Fprintf(out, "%d song(s) and %d instrument(s) with problems\n", bad, badInst)
		return 1
	}
	fmt.Fprintln(out, "all songs and instruments ok")
	return 0
}

func render(rest []string, out io.Writer) int {
	fs := flag.NewFlagSet("tracker render", flag.ContinueOnError)
	fs.SetOutput(out)
	dst := fs.String("o", "", "output .wav (default <song>.wav in the working directory)")
	section := fs.String("section", "", "start at this section")
	secs := fs.Float64("seconds", 0, "length; 0 is one pass of the order list (or the section) plus -tail")
	tail := fs.Float64("tail", 2, "seconds of reverb and release after the end")
	rate := fs.Int("rate", 48000, "sample rate")
	params := map[string]float32{}
	fs.Func("param", "set a song param before playing: name=value (repeatable)", func(v string) error {
		k, val, ok := strings.Cut(v, "=")
		f, err := strconv.ParseFloat(val, 32)
		if !ok || err != nil {
			return fmt.Errorf("want name=value, got %q", v)
		}
		params[k] = float32(f)
		return nil
	})
	if len(rest) == 0 {
		fmt.Fprintln(out, "tracker render: name a song")
		return 2
	}
	name := rest[0]
	if err := fs.Parse(rest[1:]); err != nil {
		return 2
	}
	s, name, ok := loadArg([]string{name}, out, "render")
	if !ok {
		return 1
	}
	for _, err := range tracker.ResolveSamples(s) {
		fmt.Fprintf(out, "warning: %v\n", err)
	}
	if *dst == "" {
		*dst = filepath.Base(name) + ".wav"
	}
	o := tracker.RenderOptions{Rate: *rate, Section: *section, Seconds: *secs, Tail: *tail, Params: params}
	data := tracker.Render(s, o)
	f, err := os.Create(*dst)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	if err := tracker.WriteWAV(f, data, 2, *rate, 24); err != nil {
		f.Close()
		fmt.Fprintln(out, err)
		return 1
	}
	if err := f.Close(); err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	fmt.Fprintf(out, "%s: %.1f s, peak %.2f, hash %s\n", *dst, float64(len(data)/2)/float64(*rate), tracker.Peak(data), tracker.Hash(data))
	return 0
}

func newSong(rest []string, out io.Writer) int {
	if len(rest) == 0 {
		fmt.Fprintln(out, "tracker new: name the song (a path under sounds/, no extension)")
		return 2
	}
	name := strings.TrimSuffix(rest[0], tracker.FileExt)
	path := filepath.Join(audio.SoundsDir(), filepath.FromSlash(name)+tracker.FileExt)
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(out, "tracker new: %s already exists\n", path)
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	if err := tracker.SaveFile(path, tracker.NewSong(filepath.Base(name))); err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	fmt.Fprintln(out, path)
	return 0
}
