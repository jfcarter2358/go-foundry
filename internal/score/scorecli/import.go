package scorecli

// import.go — `foundry score import` and `foundry score export-midi`.

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"foundry/internal/score"
	"foundry/internal/score/io/ftrk"
	"foundry/internal/score/io/gm"
	"foundry/internal/score/io/midi"
	"foundry/internal/score/io/musicxml"
	"foundry/internal/score/io/notate"
	"foundry/internal/score/play"
	"foundry/internal/tracker"
)

const importUsage = `foundry score import <file> [flags]

Reads a Standard MIDI File (.mid, .midi, .rmi), a MusicXML score
(.musicxml, .xml, .mxl — what Sibelius, Finale, Dorico and MuseScore
export) or a Tracker song (.ftrk, a file or a song's library name with
the extension) and writes it as sounds/<name>.fscore.

  -o name          the score's library name (default: the file's name)
  -title text      its title (default: the file's)
  -list            show the parts and the guesses and write nothing
  -skip 2,5        leave parts out (numbers as -list shows them)
  -instrument 3=strings/viola_section   use an instrument for a part (repeatable)
  -force           replace a score that is already there

MusicXML only:
  -no-layout       leave the file's page layout (paper, staff size, breaks) out

MIDI and Tracker songs (a MusicXML file is already notation):
  -grid 16,3       quantise to 16ths, letting a beat be triplets instead
                   (8, 16, 32, 64; tuplets 3, 5, 7; "16" alone: no tuplets;
                   with any, a beat whose notes would merge may take 5 or 7)
  -voices 4        most voices a staff gets; 1 writes overlaps as chords
  -no-dynamics     keep velocity out of the notation
  -no-legato       keep every small gap a player left as a rest
  -no-graces       keep flicks before the beat as notes on the grid, not grace notes
  -split C4        lowest note of a piano's upper staff
  -key Eb          the key (Eb, F#m, "C minor", or sharps: -3) instead of the file's
  -meter 6/8       the time signature instead of the file's
`

func importCmd(rest []string, out io.Writer) int {
	fs := flag.NewFlagSet("score import", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() { fmt.Fprint(out, importUsage) }
	name := fs.String("o", "", "")
	title := fs.String("title", "", "")
	list := fs.Bool("list", false, "")
	skip := fs.String("skip", "", "")
	force := fs.Bool("force", false, "")
	grid := fs.String("grid", notate.DefaultGrid().String(), "")
	voices := fs.Int("voices", score.MaxVoices, "")
	noDyn := fs.Bool("no-dynamics", false, "")
	noLegato := fs.Bool("no-legato", false, "")
	noGraces := fs.Bool("no-graces", false, "")
	split := fs.String("split", "C4", "")
	key := fs.String("key", "", "")
	meter := fs.String("meter", "", "")
	noLayout := fs.Bool("no-layout", false, "")
	insts := map[int]string{}
	fs.Func("instrument", "", func(v string) error {
		n, inst, ok := strings.Cut(v, "=")
		i, err := strconv.Atoi(n)
		if !ok || err != nil || i < 1 {
			return fmt.Errorf("want part=instrument, e.g. 3=strings/viola_section")
		}
		insts[i] = inst
		return nil
	})
	file, flags := splitArgs(rest)
	if err := fs.Parse(flags); err != nil {
		return 2
	}
	if file == "" {
		fmt.Fprintln(out, "score import: name a .mid, .musicxml or .mxl file")
		return 2
	}
	skipped := map[int]bool{}
	for _, f := range strings.Split(*skip, ",") {
		if f = strings.TrimSpace(f); f == "" {
			continue
		}
		i, err := strconv.Atoi(f)
		if err != nil || i < 1 {
			fmt.Fprintf(out, "score import: -skip %q: want part numbers, e.g. 2,5\n", *skip)
			return 2
		}
		skipped[i] = true
	}
	for i, inst := range insts {
		if !instrumentExists(inst) {
			fmt.Fprintf(out, "score import: part %d: no instrument %q in the library\n", i, inst)
			return 1
		}
	}
	if *name == "" {
		*name = libraryName(file)
	}

	var s *score.Score
	var warns []string
	switch ext := strings.ToLower(filepath.Ext(file)); ext {
	case ".mid", ".midi", ".rmi", ".smf", tracker.FileExt:
		var a *midi.Analysis
		var f *midi.File
		var song *tracker.Song
		var guesses []midi.Choice
		if strings.EqualFold(filepath.Ext(file), tracker.FileExt) {
			path := file
			if _, err := os.Stat(path); err != nil {
				path = tracker.SongPath(strings.TrimSuffix(file, tracker.FileExt))
			}
			var err error
			if song, err = tracker.LoadFile(path); err != nil {
				fmt.Fprintf(out, "score import: %v\n", err)
				return 1
			}
			var ws []string
			if a, f, guesses, ws, err = ftrk.Analyze(song); err != nil {
				fmt.Fprintf(out, "score import: %v\n", err)
				return 1
			}
			warns = append(warns, ws...)
		} else {
			var err error
			if f, err = midi.ReadFile(file); err != nil {
				fmt.Fprintf(out, "score import: %v\n", err)
				return 1
			}
			a = midi.Analyze(f)
		}
		if *list {
			for i := range a.Parts {
				if i < len(guesses) && guesses[i].Instrument != "" {
					a.Parts[i].Instrument = guesses[i].Instrument
				}
			}
			listMIDI(a, out)
			return 0
		}
		o := midi.Options{Name: *name, Title: *title, Voices: *voices, NoDynamics: *noDyn, NoLegato: *noLegato, NoGraces: *noGraces}
		var err error
		if o.Grid, err = notate.ParseGrid(*grid); err != nil {
			fmt.Fprintf(out, "score import: %v\n", err)
			return 2
		}
		if o.Split, err = parseSplit(*split); err != nil {
			fmt.Fprintf(out, "score import: %v\n", err)
			return 2
		}
		if *key != "" {
			k, err := parseKey(*key)
			if err != nil {
				fmt.Fprintf(out, "score import: %v\n", err)
				return 2
			}
			o.Key = &k
		}
		if *meter != "" {
			m, err := score.ParseMeter(*meter)
			if err != nil {
				fmt.Fprintf(out, "score import: %v\n", err)
				return 2
			}
			o.Meter = &m
		}
		o.Choices = make([]midi.Choice, len(a.Parts))
		for i := range o.Choices {
			o.Choices[i] = midi.Choice{Skip: skipped[i+1], Instrument: insts[i+1]}
		}
		var iw []string
		if song != nil {
			// Import reads the song again: its warnings are Analyze's
			// above, so only the importer's own are new.
			warns = nil
			s, iw, err = ftrk.Import(song, o)
		} else {
			s, iw, err = midi.Import(f, o)
		}
		warns = append(warns, iw...)
		if err != nil {
			fmt.Fprintf(out, "score import: %v\n", err)
			return 1
		}
	case ".musicxml", ".xml", ".mxl":
		d, err := musicxml.ReadFile(file)
		if err != nil {
			fmt.Fprintf(out, "score import: %v\n", err)
			return 1
		}
		parts := d.Parts()
		if *list {
			listMusicXML(d, parts, out)
			return 0
		}
		o := musicxml.Options{Name: *name, Title: *title, Choices: make([]musicxml.Choice, len(parts)), NoLayout: *noLayout}
		for i := range o.Choices {
			o.Choices[i] = musicxml.Choice{Skip: skipped[i+1], Instrument: insts[i+1]}
		}
		if s, warns, err = musicxml.Import(d, o); err != nil {
			fmt.Fprintf(out, "score import: %v\n", err)
			return 1
		}
	default:
		fmt.Fprintf(out, "score import: %q is not a MIDI, MusicXML or Tracker file (.mid, .musicxml, .xml, .mxl, .ftrk)\n", file)
		return 2
	}

	// Lines arrive at full volume: lower the master until it does not clip.
	if peak, err := play.FitGain(s, 0.9); err == nil && peak > 0.9 {
		warns = append(warns, fmt.Sprintf("the mix peaked at %.2f; master gain set to %.2f", peak, s.Gain))
	}
	if p := score.ScorePath(*name); p != "" && !*force {
		fmt.Fprintf(out, "score import: %s already exists (use -force to replace it, or -o another name)\n", p)
		return 1
	}
	if err := score.Save(*name, s); err != nil {
		fmt.Fprintf(out, "score import: %v\n", err)
		return 1
	}
	notes := 0
	for _, l := range s.Lines {
		for _, st := range l.Staves {
			for _, v := range st.Voices {
				for _, m := range v.Measures {
					for _, e := range m.Events {
						if e.Kind == score.EventChord && e.Grace == score.NotGrace {
							notes += len(e.Notes)
						}
					}
				}
			}
		}
	}
	fmt.Fprintf(out, "%s: %q, %d bars, %d lines, %d notes\n", score.ScorePath(*name), s.Title, s.Measures(), len(s.Lines), notes)
	for _, l := range s.Lines {
		var staves []string
		for _, st := range l.Staves {
			d := fmt.Sprintf("%s×%d", st.Clef, len(st.Voices))
			if st.Transpose != (score.Interval{}) {
				d += " " + st.Transpose.String()
			}
			staves = append(staves, d)
		}
		fmt.Fprintf(out, "  %-24s %-30s %s\n", l.Name, l.Instrument, strings.Join(staves, ", "))
	}
	for _, w := range warns {
		fmt.Fprintf(out, "  note: %s\n", w)
	}
	for _, is := range s.Validate(instrumentExists) {
		fmt.Fprintf(out, "  %s\n", is)
	}
	return 0
}

// libraryName makes a file name a score name: "Hangar Sketch (v2).mid" is
// hangar_sketch_v2.
func libraryName(file string) string {
	base := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	var b strings.Builder
	under := false
	for _, r := range strings.ToLower(base) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			under = false
		} else if !under && b.Len() > 0 {
			b.WriteByte('_')
			under = true
		}
	}
	n := strings.TrimSuffix(b.String(), "_")
	if n == "" {
		n = "imported"
	}
	return n
}

func listMIDI(a *midi.Analysis, out io.Writer) {
	fmt.Fprintf(out, "Standard MIDI file, format %d, %d tracks, %d notes, %d ticks a quarter\n", a.Format, a.Tracks, a.Notes, a.Division)
	how := func(guessed bool) string {
		if guessed {
			return "guessed"
		}
		return "from the file"
	}
	mode := "major"
	if a.Minor {
		mode = "minor"
	}
	fmt.Fprintf(out, "  time %s (%s), key %s %s (%s), tempo %g with %d change(s)\n", a.Meter, how(a.MeterGuessed), keyName(a.Fifths, mode), mode, how(a.KeyGuessed), roundTo(a.Tempo, 2), a.TempoChanges)
	fmt.Fprintf(out, "  %-3s %-26s %5s  %-26s %-30s %s\n", "#", "part", "notes", "program", "instrument", "clef")
	for i, p := range a.Parts {
		prog := "—"
		switch {
		case p.Percussion:
			prog = "channel 10 (drums)"
		case p.Program >= 0:
			prog = fmt.Sprintf("%d %s", p.Program+1, gm.Names[p.Program])
		}
		fmt.Fprintf(out, "  %-3d %-26s %5d  %-26s %-30s %s\n", i+1, p.Name, p.Notes, prog, p.Instrument, p.Clef)
	}
}

func listMusicXML(d *musicxml.Document, parts []musicxml.PartInfo, out io.Writer) {
	sw := d.Software
	if sw == "" {
		sw = "unknown software"
	}
	plural := "s"
	if len(parts) == 1 {
		plural = ""
	}
	fmt.Fprintf(out, "MusicXML %s from %s, %d part%s\n", d.Version, sw, len(parts), plural)
	fmt.Fprintf(out, "  %-3s %-26s %5s  %-26s %-30s %s\n", "#", "part", "notes", "program", "instrument", "staves")
	for i, p := range parts {
		prog := "—"
		if p.Program >= 0 {
			prog = fmt.Sprintf("%d %s", p.Program+1, gm.Names[p.Program])
		}
		staves := strings.Join(p.Clefs, " + ")
		if p.Transpose != (score.Interval{}) {
			staves += " (transposed " + p.Transpose.String() + ")"
		}
		fmt.Fprintf(out, "  %-3d %-26s %5d  %-26s %-30s %s\n", i+1, p.Name, p.Notes, prog, p.Instrument, staves)
	}
}

func roundTo(v float64, places int) float64 {
	p := 1.0
	for i := 0; i < places; i++ {
		p *= 10
	}
	return float64(int64(v*p+0.5)) / p
}

// parseSplit reads a note: "C4", "F#3" or a MIDI number.
func parseSplit(s string) (int, error) {
	if n, err := strconv.Atoi(s); err == nil && n >= 0 && n <= 127 {
		return n, nil
	}
	p, err := score.ParsePitch(s)
	if err != nil {
		return 0, fmt.Errorf("-split %q: want a note (C4) or a MIDI number (60)", s)
	}
	return p.MIDI(), nil
}

// parseKey reads "Eb", "F#m", "C minor", "D major" or a signature: "-3".
func parseKey(s string) (score.KeyMark, error) {
	t := strings.TrimSpace(s)
	if n, err := strconv.Atoi(t); err == nil {
		if n < -7 || n > 7 {
			return score.KeyMark{}, fmt.Errorf("-key %d: a signature has -7..7", n)
		}
		return score.KeyMark{Bar: 1, Fifths: n, Mode: "major"}, nil
	}
	mode := "major"
	lower := strings.ToLower(t)
	switch {
	case strings.HasSuffix(lower, " minor"):
		mode, t = "minor", strings.TrimSpace(t[:len(t)-6])
	case strings.HasSuffix(lower, " major"):
		t = strings.TrimSpace(t[:len(t)-6])
	case strings.HasSuffix(t, "m") && len(t) > 1:
		mode, t = "minor", t[:len(t)-1]
	}
	if t == "" {
		return score.KeyMark{}, fmt.Errorf("-key %q", s)
	}
	t = strings.ToUpper(t[:1]) + t[1:]
	p, err := score.ParsePitch(t + "4")
	if err != nil {
		return score.KeyMark{}, fmt.Errorf("-key %q: want e.g. Eb, F#m, \"C minor\" or -3", s)
	}
	// The key whose tonic is this spelled note: count fifths from C (or
	// from A for a minor key).
	tonic := p.Transpose(score.Interval{})
	ref := score.Pitch{Step: 0, Octave: 4} // C
	if mode == "minor" {
		ref = score.Pitch{Step: 5, Octave: 4} // A
	}
	for f := -7; f <= 7; f++ {
		c := ref
		for i := 0; i < abs(f); i++ {
			if f > 0 {
				c = c.Transpose(score.Interval{Diatonic: 4, Chromatic: 7})
			} else {
				c = c.Transpose(score.Interval{Diatonic: -4, Chromatic: -7})
			}
		}
		if c.Step == tonic.Step && c.Alter == tonic.Alter {
			return score.KeyMark{Bar: 1, Fifths: f, Mode: mode}, nil
		}
	}
	return score.KeyMark{}, fmt.Errorf("-key %q: no key signature has that tonic", s)
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func exportMIDI(rest []string, out io.Writer) int {
	fs := flag.NewFlagSet("score export-midi", flag.ContinueOnError)
	fs.SetOutput(out)
	dst := fs.String("o", "", "output .mid (default <score>.mid in the working directory)")
	noRepeats := fs.Bool("no-repeats", false, "write the score once through as written, repeats not played out")
	name, flags := splitArgs(rest)
	if err := fs.Parse(flags); err != nil {
		return 2
	}
	if name == "" {
		fmt.Fprintln(out, "score export-midi: name a score")
		return 2
	}
	s, _, err := load(name)
	if err != nil {
		fmt.Fprintf(out, "score export-midi: %v\n", err)
		return 1
	}
	if *dst == "" {
		*dst = filepath.Base(s.Name) + ".mid"
	}
	f, err := midi.Export(s, midi.ExportOptions{NoRepeats: *noRepeats})
	if err != nil {
		fmt.Fprintf(out, "score export-midi: %v\n", err)
		return 1
	}
	if err := f.WriteFile(*dst); err != nil {
		fmt.Fprintf(out, "score export-midi: %v\n", err)
		return 1
	}
	notes := 0
	for _, t := range f.Tracks {
		for _, e := range t.Events {
			if e.Kind == midi.NoteOn {
				notes++
			}
		}
	}
	fmt.Fprintf(out, "%s: format 1, %d tracks (a conductor and %d lines), %d notes\n", *dst, len(f.Tracks), len(f.Tracks)-1, notes)
	return 0
}
