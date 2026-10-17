package scorecli

// export.go — `foundry score export` (every format in one pass, as the
// editor's Export dialog writes them), `export-musicxml`, `export-audio`,
// `export-ftrk` and `codecs`.

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hajimehoshi/go-mp3"
	"github.com/jfreymuth/oggvorbis"

	"foundry/internal/fileutil"
	"foundry/internal/score"
	"foundry/internal/score/interpret"
	"foundry/internal/score/io/audioenc"
	"foundry/internal/score/io/exportset"
	"foundry/internal/score/io/ftrk"
	"foundry/internal/score/io/musicxml"
	"foundry/internal/score/io/pdf"
	"foundry/internal/tracker"
)

const exportUsage = `foundry score export <score> [flags]

Writes the score in several formats at once, as the editor's Export
dialog does, into one folder (default exports/<score>/ beside resources/).

  -formats pdf,musicxml,ogg   any of pdf, musicxml, midi, ogg, wav, mp3,
                   flac, ftrk, or "all" (default pdf,musicxml,ogg)
  -o dir           the folder
PDF:
  -parts=false     the full score only    -score=false   the parts only
  -concert         the score at concert pitch
  -paper a4        letter, a4, tabloid, a3, legal   -staff 6.5  mm
MusicXML:
  -mxl             compressed (.mxl)      -no-layout     no page layout or breaks
MIDI:
  -no-repeats      the score once through as written (MIDI and .ftrk)
Tracker song:
  -rows 4          rows a beat (default: the fewest that hold the rhythms)
Audio:
  -bars 5-8        only these bars (the first time they play)
  -section name    only this section
  -stems           a file per line beside the mix
  -rate 48000      44100 or 48000            -bits 24      16 or 24 (WAV, FLAC)
  -quality 6       OGG Vorbis quality, -1..10
  -mp3-quality 2   MP3 VBR quality, 0 (best)..9    -bitrate 192  MP3 at a constant rate
  -tail 2          bars rendered after the end for releases and reverb
  -fold            fold the tail into the start: the file loops seamlessly
  -tempo 1         the tempo scale, 0.25..2
`

// audioFlags are the flags every audio export shares.
type audioFlags struct {
	bars, section string
	stems, fold   bool
	rate, bits    int
	quality, mp3q float64
	bitrate       int
	tail, tempo   float64
}

func (af *audioFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&af.bars, "bars", "", "")
	fs.StringVar(&af.section, "section", "", "")
	fs.BoolVar(&af.stems, "stems", false, "")
	fs.BoolVar(&af.fold, "fold", false, "")
	fs.IntVar(&af.rate, "rate", 48000, "")
	fs.IntVar(&af.bits, "bits", 24, "")
	fs.Float64Var(&af.quality, "quality", audioenc.DefaultOGGQuality, "")
	fs.Float64Var(&af.mp3q, "mp3-quality", audioenc.DefaultMP3Quality, "")
	fs.IntVar(&af.bitrate, "bitrate", 0, "")
	fs.Float64Var(&af.tail, "tail", 2, "")
	fs.Float64Var(&af.tempo, "tempo", 1, "")
}

// plan is the flags as an audio plan for a score.
func (af *audioFlags) plan(s *score.Score) (exportset.AudioPlan, error) {
	a := exportset.DefaultAudio()
	if af.rate != 44100 && af.rate != 48000 {
		return a, fmt.Errorf("-rate %d: 44100 or 48000", af.rate)
	}
	if af.bits != 16 && af.bits != 24 {
		return a, fmt.Errorf("-bits %d: 16 or 24", af.bits)
	}
	if af.quality < -1 || af.quality > 10 {
		return a, fmt.Errorf("-quality %g: -1 to 10", af.quality)
	}
	if af.mp3q < 0 || af.mp3q > 9 {
		return a, fmt.Errorf("-mp3-quality %g: 0 to 9", af.mp3q)
	}
	if af.bitrate != 0 && (af.bitrate < 32 || af.bitrate > 320) {
		return a, fmt.Errorf("-bitrate %d: 32 to 320", af.bitrate)
	}
	if af.tempo < 0.25 || af.tempo > 2 {
		return a, fmt.Errorf("-tempo %g: 0.25 to 2", af.tempo)
	}
	if af.tail < 0 || af.tail > 64 {
		return a, fmt.Errorf("-tail %g: 0 to 64 bars", af.tail)
	}
	a.Rate, a.Bits, a.Stems, a.FoldTail = af.rate, af.bits, af.stems, af.fold
	a.OGGQuality, a.MP3Quality, a.MP3Bitrate = float32(af.quality), float32(af.mp3q), af.bitrate
	a.TailBars, a.TempoScale = af.tail, af.tempo
	if af.bars == "" && af.section == "" {
		return a, nil
	}
	perf, err := interpret.Interpret(s, interpret.Options{})
	if err != nil {
		return a, err
	}
	if af.bars != "" {
		x, y, ok := strings.Cut(af.bars, "-")
		var from, to int
		if _, err := fmt.Sscanf(x, "%d", &from); err != nil {
			return a, fmt.Errorf("-bars %q: want e.g. 5-8", af.bars)
		}
		to = from
		if ok {
			if _, err := fmt.Sscanf(y, "%d", &to); err != nil {
				return a, fmt.Errorf("-bars %q: want e.g. 5-8", af.bars)
			}
		}
		a.From, a.To, a.RangeName, err = exportset.BarsRange(s, perf, from, to)
		return a, err
	}
	a.From, a.To, a.RangeName, err = exportset.SectionRange(perf, af.section)
	return a, err
}

// exportsDir is exports/<name>/ beside resources/.
func exportsDir(name string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(fileutil.ResourcesPath)), "exports", filepath.Base(name))
}

func exportAll(rest []string, out io.Writer) int {
	fs := flag.NewFlagSet("score export", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() { fmt.Fprint(out, exportUsage) }
	formats := fs.String("formats", "pdf,musicxml,ogg", "")
	dst := fs.String("o", "", "")
	doScore := fs.Bool("score", true, "")
	doParts := fs.Bool("parts", true, "")
	concert := fs.Bool("concert", false, "")
	paper := fs.String("paper", "", "")
	staff := fs.Float64("staff", 0, "")
	mxl := fs.Bool("mxl", false, "")
	noLayout := fs.Bool("no-layout", false, "")
	noRepeats := fs.Bool("no-repeats", false, "")
	rows := fs.Int("rows", 0, "")
	var af audioFlags
	af.register(fs)
	name, flags := splitArgs(rest)
	if err := fs.Parse(flags); err != nil {
		return 2
	}
	if name == "" {
		fmt.Fprintln(out, "score export: name a score")
		return 2
	}
	s, _, err := load(name)
	if err != nil {
		fmt.Fprintf(out, "score export: %v\n", err)
		return 1
	}
	p := exportset.Plan{Dir: *dst, Base: filepath.Base(s.Name), Compressed: *mxl}
	if p.Base == "" || p.Base == "." {
		p.Base = "score"
	}
	if p.Dir == "" {
		p.Dir = exportsDir(p.Base)
	}
	if *formats == "all" {
		p.Formats = exportset.Formats
	} else {
		for _, f := range strings.Split(*formats, ",") {
			ff, err := exportset.ParseFormat(f)
			if err != nil {
				fmt.Fprintf(out, "score export: %v\n", err)
				return 2
			}
			p.Formats = append(p.Formats, ff)
		}
	}
	p.PDF = pdf.ExportOptions{Score: *doScore, Concert: *concert, Paper: strings.ToLower(*paper), Staff: *staff}
	if *doParts {
		for i := range s.Lines {
			p.PDF.Parts = append(p.PDF.Parts, i)
		}
	}
	p.MusicXML.NoLayout = *noLayout
	p.MIDI.NoRepeats = *noRepeats
	p.FTRK = ftrk.ExportOptions{RowsPerBeat: *rows, NoRepeats: *noRepeats}
	if p.Audio, err = af.plan(s); err != nil {
		fmt.Fprintf(out, "score export: %v\n", err)
		return 2
	}
	res, err := exportset.Run(s, p, func(step string, done, total int) {
		fmt.Fprintf(out, "[%d/%d] %s\n", done+1, total, step)
	})
	if err != nil {
		fmt.Fprintf(out, "score export: %v\n", err)
		return 1
	}
	return reportExport(res, out)
}

// reportExport lists what a run wrote and what it could not.
func reportExport(res exportset.Result, out io.Writer) int {
	for _, f := range res.Files {
		size := int64(0)
		if st, err := os.Stat(f); err == nil {
			size = st.Size()
		}
		fmt.Fprintf(out, "%s (%d bytes)\n", f, size)
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(out, "  note: %s\n", w)
	}
	if err := res.Error(); err != nil {
		fmt.Fprintf(out, "score export: %v\n", err)
		return 1
	}
	return 0
}

const exportMusicXMLUsage = `foundry score export-musicxml <score> [flags]

Writes the score as MusicXML 4.0 — what Sibelius, MuseScore, Dorico and
Finale open (Sibelius: File > Import, or open the file).

  -o file          a .musicxml or .xml file, or .mxl for the compressed
                   form (default <score>.musicxml in the working directory)
  -no-layout       leave out the page layout (paper, staff size, breaks)
`

func exportMusicXML(rest []string, out io.Writer) int {
	fs := flag.NewFlagSet("score export-musicxml", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() { fmt.Fprint(out, exportMusicXMLUsage) }
	dst := fs.String("o", "", "")
	noLayout := fs.Bool("no-layout", false, "")
	name, flags := splitArgs(rest)
	if err := fs.Parse(flags); err != nil {
		return 2
	}
	if name == "" {
		fmt.Fprintln(out, "score export-musicxml: name a score")
		return 2
	}
	s, _, err := load(name)
	if err != nil {
		fmt.Fprintf(out, "score export-musicxml: %v\n", err)
		return 1
	}
	if *dst == "" {
		*dst = filepath.Base(s.Name) + ".musicxml"
	}
	warns, err := musicxml.ExportFile(*dst, s, musicxml.ExportOptions{NoLayout: *noLayout})
	if err != nil {
		fmt.Fprintf(out, "score export-musicxml: %v\n", err)
		return 1
	}
	st, _ := os.Stat(*dst)
	fmt.Fprintf(out, "%s: MusicXML 4.0, %d parts, %d bars, %d bytes\n", *dst, len(s.Lines), s.Measures(), st.Size())
	for _, w := range warns {
		fmt.Fprintf(out, "  note: %s\n", w)
	}
	return 0
}

const exportAudioUsage = `foundry score export-audio <score> [flags]

Renders the score and writes it as WAV, FLAC, OGG Vorbis or MP3, as one
mix or a mix and a stem per line.

  -format ogg      wav, flac, ogg or mp3 (default: from -o, else wav)
  -o file          the mix's file (stems go beside it, <name>_<line>.<ext>);
                   default <score>.<format> in the working directory
  -verify          decode every file written and check it
` + `  -bars, -section, -stems, -rate, -bits, -quality, -mp3-quality,
  -bitrate, -tail, -fold, -tempo  as for score export
`

func exportAudio(rest []string, out io.Writer) int {
	fs := flag.NewFlagSet("score export-audio", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() { fmt.Fprint(out, exportAudioUsage) }
	format := fs.String("format", "", "")
	dst := fs.String("o", "", "")
	verify := fs.Bool("verify", false, "")
	var af audioFlags
	af.register(fs)
	name, flags := splitArgs(rest)
	if err := fs.Parse(flags); err != nil {
		return 2
	}
	if name == "" {
		fmt.Fprintln(out, "score export-audio: name a score")
		return 2
	}
	s, _, err := load(name)
	if err != nil {
		fmt.Fprintf(out, "score export-audio: %v\n", err)
		return 1
	}
	f := *format
	if f == "" && *dst != "" {
		f = filepath.Ext(*dst)
	}
	if f == "" {
		f = "wav"
	}
	af2, err := audioenc.ParseFormat(f)
	if err != nil {
		fmt.Fprintf(out, "score export-audio: %v\n", err)
		return 2
	}
	ef := exportset.Format(af2)
	p := exportset.Plan{Formats: []exportset.Format{ef}}
	if *dst == "" {
		p.Dir, p.Base = ".", filepath.Base(s.Name)
	} else {
		p.Dir, p.Base = filepath.Dir(*dst), strings.TrimSuffix(filepath.Base(*dst), filepath.Ext(*dst))
	}
	if p.Audio, err = af.plan(s); err != nil {
		fmt.Fprintf(out, "score export-audio: %v\n", err)
		return 2
	}
	if *dst != "" {
		// The file named is the file written, whatever the range.
		p.Audio.RangeName = ""
	}
	res, err := exportset.Run(s, p, nil)
	if err != nil {
		fmt.Fprintf(out, "score export-audio: %v\n", err)
		return 1
	}
	code := reportExport(res, out)
	if *verify {
		for _, f := range res.Files {
			if msg, err := verifyAudio(f); err != nil {
				fmt.Fprintf(out, "%s: does not decode: %v\n", f, err)
				code = 1
			} else {
				fmt.Fprintf(out, "%s: %s\n", f, msg)
			}
		}
	}
	return code
}

// verifyAudio decodes a written file with a decoder that is not the
// encoder's and says what it holds.
func verifyAudio(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".flac":
		st, err := audioenc.DecodeFLAC(b)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("FLAC, %d Hz, %d-bit, %.2f s, MD5 checked", st.Rate, st.Bits, float64(len(st.Samples)/st.Channels)/float64(st.Rate)), nil
	case ".ogg":
		data, format, err := oggvorbis.ReadAll(bytes.NewReader(b))
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Ogg Vorbis, %d Hz, %d channels, %.2f s", format.SampleRate, format.Channels, float64(len(data)/format.Channels)/float64(format.SampleRate)), nil
	case ".mp3":
		d, err := mp3.NewDecoder(bytes.NewReader(b))
		if err != nil {
			return "", err
		}
		raw, err := io.ReadAll(d)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("MP3, %d Hz, %.2f s", d.SampleRate(), float64(len(raw)/4)/float64(d.SampleRate())), nil
	case ".wav":
		if len(b) < 44 || string(b[:4]) != "RIFF" {
			return "", fmt.Errorf("not a WAV")
		}
		return fmt.Sprintf("WAV, %d bytes of audio", len(b)-44), nil
	}
	return "", fmt.Errorf("not an audio file")
}

const exportFTRKUsage = `foundry score export-ftrk <score> [flags]

Writes the score as a Tracker song: quantised to the Tracker's grid, a
channel for each note a line sounds at once, each line's .finst as the
song's instrument.

  -o file          the .ftrk (default <score>.ftrk in the working directory)
  -rows 4          rows a beat (default: the fewest that hold the rhythms)
  -no-repeats      the score once through as written
`

func exportFTRK(rest []string, out io.Writer) int {
	fs := flag.NewFlagSet("score export-ftrk", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() { fmt.Fprint(out, exportFTRKUsage) }
	dst := fs.String("o", "", "")
	rows := fs.Int("rows", 0, "")
	noRepeats := fs.Bool("no-repeats", false, "")
	name, flags := splitArgs(rest)
	if err := fs.Parse(flags); err != nil {
		return 2
	}
	if name == "" {
		fmt.Fprintln(out, "score export-ftrk: name a score")
		return 2
	}
	s, _, err := load(name)
	if err != nil {
		fmt.Fprintf(out, "score export-ftrk: %v\n", err)
		return 1
	}
	song, warns, err := ftrk.Export(s, ftrk.ExportOptions{RowsPerBeat: *rows, NoRepeats: *noRepeats})
	if err != nil {
		fmt.Fprintf(out, "score export-ftrk: %v\n", err)
		return 1
	}
	if *dst == "" {
		*dst = filepath.Base(s.Name) + tracker.FileExt
	}
	song.Name = strings.TrimSuffix(filepath.Base(*dst), filepath.Ext(*dst))
	if err := tracker.SaveFile(*dst, song); err != nil {
		fmt.Fprintf(out, "score export-ftrk: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "%s: %d channels, %d patterns, %d order positions, %d rows a beat, %d sections\n",
		*dst, len(song.Channels), len(song.Patterns), len(song.Order), song.LPB, len(song.Sections))
	for _, w := range warns {
		fmt.Fprintf(out, "  note: %s\n", w)
	}
	return 0
}

// codecs says where the OGG and MP3 encoders were found.
func codecs(out io.Writer) int {
	libs := audioenc.Libraries()
	var names []string
	for k := range libs {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, n := range names {
		if libs[n] == "" {
			fmt.Fprintf(out, "%-14s not found\n", n)
		} else {
			fmt.Fprintf(out, "%-14s %s\n", n, libs[n])
		}
	}
	fmt.Fprintf(out, "searched first: %s\n", strings.Join(audioenc.CodecDirs(), ", "))
	code := 0
	for _, f := range []audioenc.Format{audioenc.OGG, audioenc.MP3} {
		if err := audioenc.Available(f); err != nil {
			fmt.Fprintf(out, "%s\n", err)
			code = 1
		}
	}
	return code
}
