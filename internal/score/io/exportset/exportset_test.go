package exportset

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/hajimehoshi/go-mp3"
	"github.com/jfreymuth/oggvorbis"

	"foundry/internal/fileutil"
	"foundry/internal/score"
	"foundry/internal/score/interpret"
	"foundry/internal/score/io/audioenc"
	"foundry/internal/score/io/midi"
	"foundry/internal/score/io/musicxml"
	"foundry/internal/score/io/pdf"
	"foundry/internal/tracker"
)

func useRepoResources(t *testing.T) {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join(filepath.Dir(here), "..", "..", "..", "..", "resources")
	t.Cleanup(func() {
		fileutil.ResourcesPath = prev
		tracker.ReloadInstruments()
	})
	tracker.ReloadInstruments()
}

// little is a short score of two lines, one muted.
func little(t *testing.T) *score.Score {
	t.Helper()
	s, err := score.Parse([]byte(`name: little
title: Little Piece
composer: Somebody
bars: 2
tempo: [{bar: 1, bpm: 120}]
lines:
  - name: Flute
    instrument: woodwinds/flute
    staves: [{clef: treble, voices: ["| qC5(mf) qD5 qE5 qF5 | wG5 |"]}]
  - name: Cello I
    instrument: strings/cello_section
    staves: [{clef: bass, voices: ["| wC3 | wG2 |"]}]
  - name: Horn
    instrument: brass/horn
    mute: true
    staves: [{clef: treble, voices: ["| wC4 | wC4 |"]}]
`))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Every format at once: the files Files promised, each one readable as
// what it is.
func TestEveryFormat(t *testing.T) {
	useRepoResources(t)
	s := little(t)
	dir := t.TempDir()
	p := Plan{Dir: dir, Base: "little", Formats: Formats, Audio: DefaultAudio(),
		PDF: pdf.ExportOptions{Score: true, Parts: []int{0, 1, 2}}}
	p.Audio.Stems = true
	var steps []string
	res, err := Run(s, p, func(what string, done, total int) { steps = append(steps, what) })
	if err != nil {
		t.Fatal(err)
	}
	if res.Error() != nil {
		if os.Getenv("FOUNDRY_REQUIRE_CODECS") != "" || (!strings.Contains(res.Error().Error(), "libvorbis") && !strings.Contains(res.Error().Error(), "libmp3lame")) {
			t.Fatal(res.Error())
		}
		t.Logf("without codecs: %v", res.Error())
	}
	want := p.Files(s)
	got := append([]string(nil), res.Files...)
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(got, "\n") != strings.Join(want, "\n") && res.Error() == nil {
		t.Errorf("wrote\n%s\npromised\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// Muted lines have no stem: the mix and two stems a format.
	if n := len(want); n != 4+1+1+1+4*3 {
		t.Errorf("%d files promised: %v", n, want)
	}
	if len(steps) == 0 {
		t.Error("no progress")
	}
	for _, f := range res.Files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		switch filepath.Ext(f) {
		case ".pdf":
			if _, err := pdf.Parse(b); err != nil {
				t.Errorf("%s: %v", f, err)
			}
		case ".musicxml":
			d, err := musicxml.Read(b)
			if err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			back, _, err := musicxml.Import(d, musicxml.Options{})
			if err != nil || len(back.Lines) != 3 {
				t.Errorf("%s: %v", f, err)
			}
		case ".mid":
			if _, err := midi.Read(bytes.NewReader(b)); err != nil {
				t.Errorf("%s: %v", f, err)
			}
		case ".ftrk":
			song, err := tracker.Parse(b)
			if err != nil || song.Name != "little" || len(song.Instruments) != 3 {
				t.Errorf("%s: %v", f, err)
			}
		case ".wav":
			if string(b[:4]) != "RIFF" || binary.LittleEndian.Uint16(b[34:]) != 24 || binary.LittleEndian.Uint32(b[24:]) != 48000 {
				t.Errorf("%s: header", f)
			}
		case ".flac":
			st, err := audioenc.DecodeFLAC(b)
			if err != nil || st.Rate != 48000 || st.Bits != 24 {
				t.Errorf("%s: %v", f, err)
			}
			if !strings.Contains(strings.Join(st.Tags, "|"), "TITLE=Little Piece") {
				t.Errorf("%s: tags %v", f, st.Tags)
			}
		case ".ogg":
			if _, _, err := oggvorbis.ReadAll(bytes.NewReader(b)); err != nil {
				t.Errorf("%s: %v", f, err)
			}
		case ".mp3":
			d, err := mp3.NewDecoder(bytes.NewReader(b))
			if err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			if _, err := io.ReadAll(d); err != nil {
				t.Errorf("%s: %v", f, err)
			}
		default:
			t.Errorf("an unexpected file %s", f)
		}
	}
}

// decodeWAV reads a 16-bit WAV this package wrote.
func decodeWAV(t *testing.T, path string) []float32 {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(b[34:]) != 16 {
		t.Fatalf("%s is not 16-bit", path)
	}
	out := make([]float32, (len(b)-44)/2)
	for i := range out {
		out[i] = float32(int16(binary.LittleEndian.Uint16(b[44+2*i:]))) / 32767
	}
	return out
}

// A region with its tail folded in is exactly the region long, and its
// start holds the tail of its end (what looping it plays); rung out, the
// tail follows. The stems of the lines the mix plays add up to about the
// mix.
func TestRangesTailsAndStems(t *testing.T) {
	useRepoResources(t)
	s := little(t)
	perf, err := interpret.Interpret(s, interpret.Options{})
	if err != nil {
		t.Fatal(err)
	}
	bar := perf.Bars[1]
	dir := t.TempDir()
	base := Plan{Dir: dir, Base: "little", Formats: []Format{WAV}, Audio: DefaultAudio()}
	base.Audio.Bits, base.Audio.Rate = 16, 24000
	base.Audio.From, base.Audio.To, base.Audio.RangeName = bar.Tick, bar.Tick+bar.Ticks, "bar2"

	ring := base
	ring.Audio.TailBars = 1
	res, err := Run(s, ring, nil)
	if err != nil || res.Error() != nil {
		t.Fatal(err, res.Error())
	}
	if filepath.Base(res.Files[0]) != "little_bar2.wav" {
		t.Errorf("file %s", res.Files[0])
	}
	rung := decodeWAV(t, res.Files[0])
	region := int(math.Round((bar.End - bar.Start) * 24000))
	if got := len(rung) / 2; got < region+region*9/10 || got > region*2+2 {
		t.Errorf("rung out: %d frames for a region of %d and a bar of tail", got, region)
	}

	fold := base
	fold.Audio.TailBars, fold.Audio.FoldTail, fold.Audio.RangeName = 1, true, "loop"
	res, err = Run(s, fold, nil)
	if err != nil || res.Error() != nil {
		t.Fatal(err, res.Error())
	}
	looped := decodeWAV(t, res.Files[0])
	if len(looped)/2 != region {
		t.Errorf("folded: %d frames, the region is %d", len(looped)/2, region)
	}
	// The start is the start plus the tail.
	var diff, energy float64
	for i := 0; i < region*2; i++ {
		want := rung[i]
		if region*2+i < len(rung) {
			want += rung[region*2+i]
		}
		diff += math.Abs(float64(looped[i] - want))
		energy += math.Abs(float64(want))
	}
	if diff > energy*0.01 {
		t.Errorf("the folded start is not the start and the tail: %.3g off of %.3g", diff, energy)
	}

	stems := base
	stems.Audio.Stems, stems.Audio.TailBars, stems.Audio.RangeName = true, 0, ""
	stems.Audio.From, stems.Audio.To = 0, 0
	s.Master = nil // no compressor, so stems sum
	res, err = Run(s, stems, nil)
	if err != nil || res.Error() != nil {
		t.Fatal(err, res.Error())
	}
	names := []string{}
	for _, f := range res.Files {
		names = append(names, filepath.Base(f))
	}
	if strings.Join(names, " ") != "little.wav little_flute.wav little_cello_i.wav" {
		t.Fatalf("files %v", names)
	}
	mix, a, b := decodeWAV(t, res.Files[0]), decodeWAV(t, res.Files[1]), decodeWAV(t, res.Files[2])
	var d2, e2 float64
	for i := range mix {
		var x float32
		if i < len(a) {
			x += a[i]
		}
		if i < len(b) {
			x += b[i]
		}
		d2 += float64((mix[i] - x) * (mix[i] - x))
		e2 += float64(mix[i] * mix[i])
	}
	if d2 > e2*0.01 {
		t.Errorf("the stems do not add up to the mix: %.3g off of %.3g", d2, e2)
	}
}

func TestFoldAndTail(t *testing.T) {
	data := []float32{1, 1, 2, 2, 3, 3, 4, 4, 5, 5}
	if got := Fold(data, 2); len(got) != 4 || got[0] != 1+3+5 || got[2] != 2+4 {
		t.Errorf("fold %v", got)
	}
	if got := Fold(data[:2], 3); len(got) != 6 || got[0] != 1 || got[5] != 0 {
		t.Errorf("fold of a short file %v", got)
	}
	s := score.New("x")
	s.Lines = append(s.Lines, score.NewLine("A", "keys/grand_piano", "treble"))
	perf, _ := interpret.Interpret(s, interpret.Options{})
	// 4/4 at 120: a bar is two seconds.
	if sec := TailSeconds(perf, perf.Length, 2); math.Abs(sec-4) > 1e-9 {
		t.Errorf("two bars of tail are %g s", sec)
	}
}

func TestPlansAreChecked(t *testing.T) {
	s := score.New("x")
	if _, err := Run(s, Plan{Dir: t.TempDir(), Base: "x"}, nil); err == nil {
		t.Error("a plan with no formats ran")
	}
	if _, err := Run(s, Plan{Formats: []Format{MIDI}}, nil); err == nil {
		t.Error("a plan with no folder ran")
	}
	for in, want := range map[string]Format{"sibelius": MusicXML, "mid": MIDI, "OGG": OGG, "tracker": FTRK, ".flac": FLAC} {
		if f, err := ParseFormat(in); err != nil || f != want {
			t.Errorf("%s: %v %v", in, f, err)
		}
	}
	if _, err := ParseFormat("aiff"); err == nil {
		t.Error("aiff parsed")
	}
}

// A render past full scale says so, with the gain that would fit; one
// that fits says nothing.
func TestClippingIsSaid(t *testing.T) {
	useRepoResources(t)
	for _, gain := range []float32{1, 40} {
		s := little(t)
		s.Gain = gain
		p := Plan{Dir: t.TempDir(), Base: "little", Formats: []Format{WAV}, Audio: DefaultAudio()}
		res, err := Run(s, p, nil)
		if err != nil || res.Error() != nil {
			t.Fatal(err, res.Error())
		}
		clips := false
		for _, w := range res.Warnings {
			clips = clips || strings.Contains(w, "the mix clips")
		}
		if clips != (gain > 1) {
			t.Errorf("gain %g: warnings %q", gain, res.Warnings)
		}
	}
	if got := Peak([]float32{0.2, -1.5, 0.9}); got != 1.5 {
		t.Errorf("peak %g", got)
	}
}
