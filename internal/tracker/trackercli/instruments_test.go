package trackercli

import (
	"bytes"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/tracker"
)

// Every shipped library instrument sounds, in every articulation, at a
// note in its range — through the Engine, as a score will play it.
func TestShippedInstrumentsSound(t *testing.T) {
	useRepoResources(t)
	names := tracker.ScanInstrumentNames()
	if len(names) < 150 {
		t.Fatalf("only %d library instruments", len(names))
	}
	buf := make([]float32, 480*2)
	for _, name := range names {
		in, err := tracker.LoadInstrument(name)
		if err != nil {
			t.Fatal(err)
		}
		pitch := float32(tracker.NoteC4)
		if pb := in.Playback; pb.Low != 0 && pb.High != 0 {
			pitch = float32((int(pb.Low) + int(pb.High)) / 2)
		}
		for art := 0; art <= len(in.Articulations); art++ {
			for _, vel := range []float32{0.3, 0.75, 1} {
				e := tracker.NewEngine(1)
				e.SetStrip(0, tracker.StripControls{Volume: 1})
				id := e.NoteOn(0, in, pitch, tracker.NoteOptions{Articulation: art, Velocity: vel})
				if id == 0 {
					t.Fatalf("%s articulation %d plays nothing", name, art)
				}
				// Long enough for a riser or a reversed cymbal to arrive.
				var pk float32
				for i := 0; i < 1000 && pk < 0.05; i++ {
					e.Render(buf, 480, 48000)
					if p := tracker.Peak(buf); p > pk {
						pk = p
					}
				}
				if pk < 0.02 || pk != pk || pk > 3 {
					t.Errorf("%s articulation %d velocity %.2f peaks at %v", name, art, vel, pk)
				}
			}
		}
	}
}

// pitchHz finds the left channel's period by autocorrelation, searching
// lags within a fifth of the expected frequency either way — near enough to
// tell a note from its neighbours, far enough to catch a wrong sample.
func pitchHz(st []float32, rate int, near float64) float64 {
	n := len(st) / 2
	x := make([]float64, n)
	for i := range x {
		x[i] = float64(st[i*2])
	}
	lo, hi := int(float64(rate)/(near*1.5)), int(float64(rate)/(near/1.5))
	best, bestR := 0, math.Inf(-1)
	corr := func(lag int) float64 {
		var r, ea, eb float64
		for i := 0; i+lag < n; i++ {
			r += x[i] * x[i+lag]
			ea += x[i] * x[i]
			eb += x[i+lag] * x[i+lag]
		}
		return r / math.Sqrt(ea*eb+1e-12)
	}
	for lag := lo; lag <= hi; lag++ {
		if r := corr(lag); r > bestR {
			best, bestR = lag, r
		}
	}
	// Parabolic refinement between the neighbouring lags.
	a, b, c := corr(best-1), bestR, corr(best+1)
	d := 0.0
	if den := a - 2*b + c; den != 0 {
		d = 0.5 * (a - c) / den
	}
	return float64(rate) / (float64(best) + d)
}

// The multi-sampled sections play in tune across their registers, and a
// transposing instrument's sound is where its Playback says it is.
func TestOrchestralInstrumentsInTune(t *testing.T) {
	useRepoResources(t)
	for _, c := range []struct {
		inst string
		note string
	}{
		{"woodwinds/flute", "A-5"},
		{"woodwinds/clarinet", "A-4"},
		{"brass/trumpet", "A-4"},
		{"keys/organ", "A-3"},
		{"strings/violin_section", "A-4"},
		{"strings/violin_section", "A-5"},
		{"strings/cello_section", "A-2"},
		{"keys/grand_piano", "A-2"},
		{"keys/grand_piano", "A-6"},
		{"strings/viola_section", "A-3"},
		{"strings/contrabass_section", "A-1"},
		{"woodwinds/bassoon", "A-2"},
		{"brass/tuba", "A-1"},
		{"brass/horn", "A-3"},
		{"brass/trombone", "A-2"},
		{"keys/harp", "A-2"},
		{"choir/choir", "A-3"},
	} {
		in, err := tracker.LoadInstrument(c.inst)
		if err != nil {
			t.Fatal(err)
		}
		n, _ := tracker.ParseNote(c.note)
		e := tracker.NewEngine(1)
		e.SetBuses(tracker.ReverbSpec{}, tracker.DelaySpec{}, tracker.CompressorSpec{}, 1)
		e.SetStrip(0, tracker.StripControls{Volume: 1})
		hz0 := 440 * math.Pow(2, float64(int(n)-58)/12)
		e.NoteOn(0, in, float32(n), tracker.NoteOptions{})
		out := make([]float32, 36000*2)
		e.Render(out, 36000, 48000)
		got := pitchHz(out[24000*2:], 48000, hz0)
		if math.Abs(got-hz0)/hz0 > 0.015 {
			t.Errorf("%s %s plays at %.1f Hz, want %.1f", c.inst, c.note, got, hz0)
		}
	}
}

// A song slot can play a library instrument by name.
func TestSongPlaysLibraryInstrument(t *testing.T) {
	useRepoResources(t)
	src := "version: 1\nbpm: 100\nchannels: [{name: strings}, {name: kit}]\norder: [0]\n" +
		"instruments:\n  - name: Violins\n    ref: strings/violin_section\n  - name: Kit\n    ref: percussion/drum_kit\n" +
		"patterns:\n  - rows: 16\n    data: |\n" +
		"      A-4 01 30 ... ... | C-2 02 .. ... ...\n      ... .. .. ... ... | ... .. .. ... ...\n" +
		"      ... .. .. ... ... | D-2 02 .. ... ...\n      ... .. .. ... ... | ... .. .. ... ...\n" +
		"      E-5 01 .. @02 ... | F#2 02 .. ... ...\n"
	s, err := tracker.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if errs := tracker.ResolveSamples(s); len(errs) > 0 {
		t.Fatal(errs)
	}
	data := tracker.Render(s, tracker.RenderOptions{Seconds: 3})
	if pk := tracker.Peak(data); pk < 0.1 || pk > 1.5 {
		t.Fatalf("peak %v", pk)
	}
	var out bytes.Buffer
	root := filepath.Dir(fileutil.ResourcesPath)
	if code := Main(root, []string{"instruments"}, &out); code != 0 || !strings.Contains(out.String(), "strings/violin_section") ||
		!strings.Contains(out.String(), "staccato,pizzicato,tremolo,muted") {
		t.Fatalf("instruments: %d\n%s", code, out.String())
	}
}

// The string tremolo articulation pulses about fifteen times a second; the
// sustain does not.
func TestStringTremoloPulses(t *testing.T) {
	useRepoResources(t)
	in, err := tracker.LoadInstrument("strings/violin_section")
	if err != nil {
		t.Fatal(err)
	}
	depth := func(art int) (float64, int) {
		e := tracker.NewEngine(1)
		e.SetBuses(tracker.ReverbSpec{}, tracker.DelaySpec{}, tracker.CompressorSpec{}, 1)
		e.SetStrip(0, tracker.StripControls{Volume: 1})
		e.NoteOn(0, in, 58, tracker.NoteOptions{Articulation: art})
		out := make([]float32, 96000*2)
		e.Render(out, 96000, 48000)
		// 10 ms RMS windows of both channels over 1.0..1.6 s, past the attack.
		var lv []float64
		for w := 0; w < 60; w++ {
			var s float64
			for i := 0; i < 960; i++ {
				v := float64(out[48000*2+w*960+i])
				s += v * v
			}
			lv = append(lv, math.Sqrt(s/960))
		}
		lo, hi, peaks := math.Inf(1), 0.0, 0
		for i, v := range lv {
			lo, hi = math.Min(lo, v), math.Max(hi, v)
			if i > 0 && i+1 < len(lv) && v > lv[i-1] && v >= lv[i+1] && v > 0 {
				peaks++
			}
		}
		return hi / math.Max(lo, 1e-9), peaks
	}
	trem := in.ArticulationIndex("tremolo")
	td, peaks := depth(trem)
	sd, _ := depth(0)
	t.Logf("tremolo depth %.2f with %d pulses; sustain depth %.2f", td, peaks, sd)
	if td < 2 || td < 1.5*sd || peaks < 6 || peaks > 14 {
		t.Errorf("tremolo: depth %.2f (sustain %.2f), %d pulses in 0.6 s", td, sd, peaks)
	}
}
