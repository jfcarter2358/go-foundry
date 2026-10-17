// Package exportset writes a score in every format the Export dialog
// offers, in one pass: the PDF score and parts, MusicXML ("for
// Sibelius"), MIDI, a Tracker song, and audio — OGG for the game, WAV,
// MP3 and FLAC — as one mix or a mix and a stem per line, of the whole
// score, the loop region or a selection, with a tail rung out after the
// end or folded back into the start so the file loops without a seam.
//
// The editor's Export dialog (mockup screen 6) and `foundry score export`
// both build a Plan and Run it; Files says beforehand what it will write.
package exportset

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/score"
	"foundry/internal/score/interpret"
	"foundry/internal/score/io/audioenc"
	"foundry/internal/score/io/ftrk"
	"foundry/internal/score/io/midi"
	"foundry/internal/score/io/musicxml"
	"foundry/internal/score/io/pdf"
	"foundry/internal/score/play"
	"foundry/internal/tracker"
)

// Format is one of the dialog's formats.
type Format string

const (
	PDF      Format = "pdf"
	MusicXML Format = "musicxml"
	MIDI     Format = "midi"
	OGG      Format = "ogg"
	WAV      Format = "wav"
	MP3      Format = "mp3"
	FLAC     Format = "flac"
	FTRK     Format = "ftrk"
)

// Formats are every format in the dialog's order.
var Formats = []Format{PDF, MusicXML, MIDI, OGG, WAV, MP3, FLAC, FTRK}

// Label is the format as the dialog names it; Note the gloss beside it.
func (f Format) Label() string {
	switch f {
	case PDF:
		return "PDF"
	case MusicXML:
		return "MusicXML"
	case MIDI:
		return "MIDI"
	case FTRK:
		return "Tracker song (.ftrk)"
	}
	return strings.ToUpper(string(f))
}

// Note is the gloss beside a format's name.
func (f Format) Note() string {
	switch f {
	case PDF:
		return "score + parts"
	case MusicXML:
		return "for Sibelius"
	case OGG:
		return "game audio"
	}
	return ""
}

// Audio reports an audio format; Encoding is the encoder's name for it.
func (f Format) Audio() bool { return f == OGG || f == WAV || f == MP3 || f == FLAC }

func (f Format) encoding() audioenc.Format { return audioenc.Format(f) }

// ParseFormat reads a format's name ("sibelius" and "xml" are MusicXML,
// "mid" MIDI, "tracker" the song).
func ParseFormat(s string) (Format, error) {
	t := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "."))
	switch t {
	case "xml", "mxl", "sibelius", "musicxml":
		return MusicXML, nil
	case "mid", "midi":
		return MIDI, nil
	case "tracker", "song", "ftrk":
		return FTRK, nil
	case "vorbis":
		return OGG, nil
	}
	for _, f := range Formats {
		if string(f) == t {
			return f, nil
		}
	}
	return "", fmt.Errorf("%q is not an export format (pdf, musicxml, midi, ogg, wav, mp3, flac, ftrk)", s)
}

// AudioPlan is what the audio formats render.
type AudioPlan struct {
	// From and To are the range in performance ticks (To 0: the end);
	// RangeName goes in the file names ("loop", "bars5-8"), "" for the
	// whole score.
	From, To  int64
	RangeName string
	// Rate is 44100 or 48000 (0 is 48000); Bits WAV's and FLAC's sample
	// size, 16 or 24 (0 is 24).
	Rate, Bits int
	// OGGQuality is -1..10; MP3Quality the VBR quality 0..9, or
	// MP3Bitrate kb/s constant.
	OGGQuality float32
	MP3Quality float32
	MP3Bitrate int
	// Stems writes a file per line beside the mix: the line alone,
	// through its own effects, the buses and the master.
	Stems bool
	// TailBars is how long renders after the end, in bars at the end's
	// tempo and meter; FoldTail mixes that tail back into the start and
	// cuts the file at the end, so it loops seamlessly (the reverb of the
	// last bars rings over the first).
	TailBars float64
	FoldTail bool
	// TempoScale plays faster or slower (the transport's slider), 0 is 1.
	TempoScale float64
}

// DefaultAudio is the dialog's starting audio: 48 kHz, 24-bit lossless,
// OGG quality 6, MP3 VBR V2, two bars of tail.
func DefaultAudio() AudioPlan {
	return AudioPlan{Rate: 48000, Bits: 24, OGGQuality: audioenc.DefaultOGGQuality, MP3Quality: audioenc.DefaultMP3Quality, TailBars: 2}
}

// Plan is one export.
type Plan struct {
	// Dir is where the files go; Base their name before the extension
	// (the score's name).
	Dir, Base string
	Formats   []Format
	PDF       pdf.ExportOptions
	MusicXML  musicxml.ExportOptions
	// Compressed writes MusicXML as .mxl.
	Compressed bool
	MIDI       midi.ExportOptions
	FTRK       ftrk.ExportOptions
	Audio      AudioPlan
}

// Has reports whether the plan writes a format.
func (p Plan) Has(f Format) bool {
	for _, g := range p.Formats {
		if g == f {
			return true
		}
	}
	return false
}

// HasAudio reports whether the plan writes any audio.
func (p Plan) HasAudio() bool {
	for _, f := range p.Formats {
		if f.Audio() {
			return true
		}
	}
	return false
}

// FileSafe keeps a name's letters and digits, the rest as underscores
// (the PDF parts' rule).
func FileSafe(s string) string {
	var b strings.Builder
	under := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			under = false
		case !under && b.Len() > 0:
			b.WriteByte('_')
			under = true
		}
	}
	out := strings.TrimRight(b.String(), "_")
	if out == "" {
		return "line"
	}
	return out
}

// stems are the lines with a stem: those the mix plays (not muted, or
// soloed when any is), with their file names made unique.
func stems(s *score.Score) (lines []int, names []string) {
	used := map[string]bool{}
	for i, l := range s.Lines {
		if play.LineSilenced(s, i) {
			continue
		}
		n := FileSafe(l.Name)
		for k := 2; used[n]; k++ {
			n = fmt.Sprintf("%s_%d", FileSafe(l.Name), k)
		}
		used[n] = true
		lines, names = append(lines, i), append(names, n)
	}
	return lines, names
}

func (p Plan) audioBase() string {
	if p.Audio.RangeName != "" {
		return p.Base + "_" + p.Audio.RangeName
	}
	return p.Base
}

// Files are the paths the plan writes, in the order Run writes them.
func (p Plan) Files(s *score.Score) []string {
	var out []string
	add := func(name string) { out = append(out, filepath.Join(p.Dir, name)) }
	for _, f := range Formats {
		if !p.Has(f) {
			continue
		}
		switch f {
		case PDF:
			if p.PDF.Score {
				add(p.Base + ".pdf")
			}
			used := map[string]bool{p.Base + ".pdf": p.PDF.Score}
			for _, li := range p.PDF.Parts {
				if li < 0 || li >= len(s.Lines) {
					continue
				}
				file := pdf.PartFileName(p.Base, s.Lines[li].Name)
				for k := 2; used[file]; k++ {
					file = pdf.PartFileName(p.Base, fmt.Sprintf("%s %d", s.Lines[li].Name, k))
				}
				used[file] = true
				add(file)
			}
		case MusicXML:
			if p.Compressed {
				add(p.Base + ".mxl")
			} else {
				add(p.Base + ".musicxml")
			}
		case MIDI:
			add(p.Base + ".mid")
		case FTRK:
			add(p.Base + tracker.FileExt)
		default:
			ext := f.encoding().Ext()
			add(p.audioBase() + ext)
			if p.Audio.Stems {
				_, names := stems(s)
				for _, n := range names {
					add(p.audioBase() + "_" + n + ext)
				}
			}
		}
	}
	return out
}

// Result is what a Run wrote.
type Result struct {
	Files    []string
	Warnings []string
	// Failed are the formats that could not be written, and why (an OGG
	// with no libvorbisenc); the rest are written all the same.
	Failed map[Format]error
}

// Error is the failures as one error, nil with none.
func (r Result) Error() error {
	if len(r.Failed) == 0 {
		return nil
	}
	var fs []string
	for f, err := range r.Failed {
		fs = append(fs, fmt.Sprintf("%s: %v", f.Label(), err))
	}
	sort.Strings(fs)
	return fmt.Errorf("%s", strings.Join(fs, "; "))
}

// Progress hears what Run is doing: a step's description and how many of
// the steps are done.
type Progress func(step string, done, total int)

// Run writes the plan. A format that fails is in Result.Failed and the
// others are written; the error is for a plan that cannot start.
func Run(s *score.Score, p Plan, progress Progress) (Result, error) {
	res := Result{Failed: map[Format]error{}}
	if len(p.Formats) == 0 {
		return res, fmt.Errorf("export: no formats chosen")
	}
	if p.Dir == "" || p.Base == "" {
		return res, fmt.Errorf("export: no folder or name")
	}
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return res, err
	}
	if progress == nil {
		progress = func(string, int, int) {}
	}
	// Audio first checks its encoders, so a missing library is said
	// before minutes of rendering.
	var audioFormats []Format
	for _, f := range Formats {
		if p.Has(f) && f.Audio() {
			if err := audioenc.Available(f.encoding()); err != nil {
				res.Failed[f] = err
				continue
			}
			audioFormats = append(audioFormats, f)
		}
	}
	steps := 0
	for _, f := range p.Formats {
		if !f.Audio() {
			steps++
		}
	}
	nStems := 0
	if p.Audio.Stems {
		ls, _ := stems(s)
		nStems = len(ls)
	}
	if len(audioFormats) > 0 {
		steps += 1 + nStems
	}
	done := 0
	step := func(what string) {
		progress(what, done, steps)
		done++
	}
	warn := func(prefix string, ws []string) {
		for _, w := range ws {
			res.Warnings = append(res.Warnings, prefix+": "+w)
		}
	}
	for _, f := range Formats {
		if !p.Has(f) || f.Audio() {
			continue
		}
		switch f {
		case PDF:
			step("Engraving the PDFs")
			files, err := pdf.Export(s, p.Dir, p.Base, p.PDF)
			for _, x := range files {
				res.Files = append(res.Files, x.Path)
			}
			if err != nil {
				res.Failed[f] = err
			}
		case MusicXML:
			step("Writing MusicXML")
			ext := ".musicxml"
			if p.Compressed {
				ext = ".mxl"
			}
			path := filepath.Join(p.Dir, p.Base+ext)
			ws, err := musicxml.ExportFile(path, s, p.MusicXML)
			warn("MusicXML", ws)
			if err != nil {
				res.Failed[f] = err
			} else {
				res.Files = append(res.Files, path)
			}
		case MIDI:
			step("Writing MIDI")
			path := filepath.Join(p.Dir, p.Base+".mid")
			if err := midi.ExportFile(path, s, p.MIDI); err != nil {
				res.Failed[f] = err
			} else {
				res.Files = append(res.Files, path)
			}
		case FTRK:
			step("Writing the Tracker song")
			path := filepath.Join(p.Dir, p.Base+tracker.FileExt)
			song, ws, err := ftrk.Export(s, p.FTRK)
			warn("Tracker song", ws)
			if err == nil {
				song.Name = p.Base
				err = tracker.SaveFile(path, song)
			}
			if err != nil {
				res.Failed[f] = err
			} else {
				res.Files = append(res.Files, path)
			}
		}
	}
	if len(audioFormats) == 0 {
		return res, nil
	}
	a := p.Audio
	if a.Rate == 0 {
		a.Rate = 48000
	}
	tracks, ws, err := renderAudio(s, a, func(what string) { step(what) })
	warn("Audio", ws)
	if err != nil {
		for _, f := range audioFormats {
			res.Failed[f] = err
		}
		return res, nil
	}
	_, names := stems(s)
	for _, f := range audioFormats {
		o := audioenc.DefaultOptions(f.encoding())
		o.Bits = a.Bits
		switch f {
		case OGG:
			o.Quality = a.OGGQuality
		case MP3:
			o.Quality, o.Bitrate = a.MP3Quality, a.MP3Bitrate
		}
		for ti, tr := range tracks {
			name := p.audioBase()
			o.Tags = audioenc.Tags{Title: s.Title, Artist: s.Composer}
			if ti > 0 {
				name += "_" + names[ti-1]
				o.Tags.Title = s.Title + " (" + tr.line + ")"
			}
			path := filepath.Join(p.Dir, name+f.encoding().Ext())
			if err := audioenc.WriteFile(path, tr.data, 2, a.Rate, o); err != nil {
				res.Failed[f] = err
				break
			}
			res.Files = append(res.Files, path)
		}
	}
	return res, nil
}

// audioTrack is a rendered mix or stem.
type audioTrack struct {
	line string // "" for the mix
	data []float32
}

// renderAudio renders the mix and (Stems) each line alone.
func renderAudio(s *score.Score, a AudioPlan, step func(string)) ([]audioTrack, []string, error) {
	var warns []string
	perf, err := interpret.Interpret(s, interpret.Options{})
	if err != nil {
		return nil, nil, err
	}
	o, foldFrames, err := renderOptions(perf, a)
	if err != nil {
		return nil, nil, err
	}
	render := func(sc *score.Score, what string) ([]float32, error) {
		data, errs, err := play.RenderScore(sc, o)
		if err != nil {
			return nil, err
		}
		for _, e := range errs {
			warns = append(warns, e.Error())
		}
		if foldFrames > 0 {
			data = Fold(data, foldFrames)
		}
		// A file clips where the render passes full scale: say so, as
		// score render does, with the gain that would fit.
		if peak := Peak(data); peak > 1 {
			warns = append(warns, fmt.Sprintf("%s clips (peak %.2f); a master gain of %.2f or less would fit", what, peak, float64(sc.Gain)*0.99/peak))
		}
		return data, nil
	}
	step("Rendering the mix")
	mix, err := render(s, "the mix")
	if err != nil {
		return nil, warns, err
	}
	out := []audioTrack{{data: mix}}
	if !a.Stems {
		return out, warns, nil
	}
	lines, _ := stems(s)
	for _, li := range lines {
		step("Rendering " + s.Lines[li].Name)
		alone := s.Clone()
		for i, l := range alone.Lines {
			l.Solo = i == li
			if i == li {
				l.Mute = false
			}
		}
		data, err := render(alone, "the "+s.Lines[li].Name+" stem")
		if err != nil {
			return nil, warns, err
		}
		out = append(out, audioTrack{line: s.Lines[li].Name, data: data})
	}
	return out, warns, nil
}

// renderOptions are the renderer's options for a plan: the range, the
// tail in seconds, and (FoldTail) how many frames the folded file is.
func renderOptions(perf *interpret.Performance, a AudioPlan) (play.RenderOptions, int, error) {
	scale := a.TempoScale
	if scale <= 0 {
		scale = 1
	}
	scale = math.Max(0.25, math.Min(2, scale))
	from, to := a.From, a.To
	if to <= 0 || to > perf.Length {
		to = perf.Length
	}
	if from < 0 || from >= to {
		return play.RenderOptions{}, 0, fmt.Errorf("export: the range %d..%d ticks is empty", from, to)
	}
	tail := TailSeconds(perf, to, a.TailBars) / scale
	o := play.RenderOptions{Rate: a.Rate, From: from, To: to, Tail: tail, TempoScale: scale}
	fold := 0
	if a.FoldTail {
		secs := (perf.Tempo.Seconds(float64(to)) - perf.Tempo.Seconds(float64(from))) / scale
		fold = int(math.Round(secs * float64(a.Rate)))
		if fold <= 0 {
			return o, 0, fmt.Errorf("export: the range is too short to loop")
		}
	}
	return o, fold, nil
}

// TailSeconds is bars of tail as seconds: the bar the range ends in, at
// the tempo there, that many times.
func TailSeconds(perf *interpret.Performance, end int64, bars float64) float64 {
	if bars <= 0 {
		return 0
	}
	barTicks := int64(score.TicksPerWhole)
	for _, b := range perf.Bars {
		if b.Tick < end {
			barTicks = b.Ticks
		}
	}
	at := float64(end)
	return bars * (perf.Tempo.Seconds(at) - perf.Tempo.Seconds(at-float64(barTicks)))
}

// Peak is the largest magnitude of a render's samples.
func Peak(data []float32) float64 {
	var p float32
	for _, v := range data {
		if v < 0 {
			v = -v
		}
		if v > p {
			p = v
		}
	}
	return float64(p)
}

// Fold makes interleaved stereo loop: what is past frames (the tail) is
// mixed back over the start, as many times round as it is long, and the
// result is exactly frames long. A file shorter than frames is padded.
func Fold(data []float32, frames int) []float32 {
	out := make([]float32, frames*2)
	copy(out, data)
	for i := frames * 2; i < len(data); i++ {
		out[i%(frames*2)] += data[i]
	}
	return out
}

// BarsRange is the performance's span of written bars a..z (z's end): from
// where a first plays to where z ends after it, and a name for files.
func BarsRange(s *score.Score, perf *interpret.Performance, a, z int) (from, to int64, name string, err error) {
	if z < a {
		a, z = z, a
	}
	if a < 1 || z > s.Measures() {
		return 0, 0, "", fmt.Errorf("bars %d-%d: the score has %d", a, z, s.Measures())
	}
	from = perf.BarTick(a)
	if from < 0 {
		return 0, 0, "", fmt.Errorf("bar %d does not play", a)
	}
	to = -1
	for _, b := range perf.Bars {
		if b.Bar == z && b.Tick >= from {
			to = b.Tick + b.Ticks
			break
		}
	}
	if to <= from {
		return 0, 0, "", fmt.Errorf("bar %d does not play after bar %d", z, a)
	}
	name = fmt.Sprintf("bars%d-%d", a, z)
	if a == z {
		name = fmt.Sprintf("bar%d", a)
	}
	return from, to, name, nil
}

// SectionRange is a section's span, named after it.
func SectionRange(perf *interpret.Performance, section string) (from, to int64, name string, err error) {
	for _, sec := range perf.Sections {
		if strings.EqualFold(sec.Name, section) {
			return sec.Tick, sec.End, FileSafe(sec.Name), nil
		}
	}
	return 0, 0, "", fmt.Errorf("no section %q", section)
}
