package tracker

// engine_golden_test.go — the Engine refactor's proof that nothing changed.
//
// The hashes below were captured from the Player BEFORE its voices, strips
// and buses moved into Engine. Each song is built to reach a different part
// of the old renderBlock: every effect command, every envelope, the strip
// inserts and filters, both send buses and the compressor, preview notes,
// mutes, solos and stingers driven through the command channel mid-render.
// If one of these moves, the Tracker sounds different.

import (
	"math"
	"os"
	"testing"
)

// stereoSample is a ping-pong looped stereo saw-ish wave, base note C-4.
func stereoSample(secs float64) *Sample {
	n := int(secs * testRate)
	d := make([]float32, n*2)
	for i := 0; i < n; i++ {
		ph := math.Mod(float64(i)*261.6256/testRate, 1)
		d[i*2] = float32(2*ph - 1)
		d[i*2+1] = float32(math.Sin(2 * math.Pi * ph))
	}
	return &Sample{Name: "saw", Data: d, Channels: 2, Rate: testRate, BaseNote: NoteC4, Volume: 0.8,
		Loop: LoopPingPong, LoopStart: n / 4, LoopEnd: n - n/4}
}

// oneShot is an unlooped decaying noise burst — a drum.
func oneShot(secs float64) *Sample {
	n := int(secs * testRate)
	d := make([]float32, n)
	x := uint32(12345)
	for i := range d {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		d[i] = (float32(x)/float32(math.MaxUint32)*2 - 1) * float32(math.Exp(-float64(i)/float64(n)*6))
	}
	return &Sample{Name: "hit", Data: d, Channels: 1, Rate: testRate, BaseNote: NoteC4, Volume: 1}
}

// goldenEffectsSong exercises every effect command, envelope and insert.
func goldenEffectsSong(t testing.TB) *Song {
	s := NewSong("golden")
	s.SetChannelCount(6)
	s.FXColumns = 3
	s.Patterns[0].Resize(32, 6)
	pat := "" +
		"C-4 01 30 V48 P40 ... | E-3 02 .. A47 ... ... | C-4 03 .. ... ... ... | G-2 04 .. U08 F60 R80 | C-5 01 20 T48 ... ... | C-4 03 .. X80 Z80 ...\n" +
		"... .. .. V00 S04 ... | ... .. .. A00 ... ... | ... .. .. ... ... ... | ... .. .. W04 ... ... | ... .. .. T00 ... ... | ... .. .. ... ... ...\n" +
		"D-4 01 .. G20 ... ... | G-3 02 .. ... ... ... | C-4 03 .. Q02 ... ... | ... .. .. D04 ... ... | E-5 01 .. Y20 ... ... | === .. .. ... ... ...\n" +
		"... .. .. G00 ... ... | ... .. .. ... ... ... | ... .. .. ... ... ... | ... .. .. W40 ... ... | ... .. .. Y02 ... ... | ... .. .. ... ... ...\n" +
		"F-4 01 .. J90 ... ... | C-4 02 28 O40 ... ... | C-4 03 .. N03 ... ... | A-2 04 .. I01 ... ... | G-5 01 .. C03 ... ... | C-4 03 .. H1F ... ...\n" +
		"... .. .. J70 ... ... | E-4 02 .. ... ... ... | ... .. .. ... ... ... | ... .. .. I00 ... ... | ... .. .. ... ... ... | ... .. .. ... ... ...\n" +
		"=== .. .. ... ... ... | G-4 02 .. S20 ... ... | ^^^ .. .. ... ... ... | ... .. .. U10 ... ... | A-5 01 10 ... ... ... | C-4 03 .. H10 ... ...\n" +
		"... .. .. ... ... ... | ... .. .. ... ... ... | ... .. .. ... ... ... | ... .. .. ... ... ... | ... .. .. ... ... ... | ... .. .. ... ... ...\n" +
		"C-4 01 .. L00 ... ... | C-5 02 .. ... ... ... | C-4 03 .. ... ... ... | C-3 04 .. F20 ... ... | ... .. .. ... ... ... | ... .. .. ... ... ...\n" +
		"... .. .. ... ... ... | ... .. .. ... ... ... | C-4 03 .. ... ... ... | ... .. .. W40 ... ... | ... .. .. ... ... ... | ... .. .. ... ... ...\n" +
		"... .. .. L01 ... ... | ... .. .. ... ... ... | ... .. .. ... ... ... | ... .. .. ... ... ... | ... .. .. ... ... ... | ... .. .. ... ... ...\n" +
		"G-4 01 .. M60 ... ... | ... .. .. ... ... ... | C-4 03 .. ... ... ... | ... .. .. ... ... ... | C-6 01 .. E01 ... ... | ... .. .. ... ... ...\n"
	if err := ParsePatternText(s.Patterns[0], pat); err != nil {
		t.Fatal(err)
	}
	p2 := NewPattern("b", 16, 6)
	if err := ParsePatternText(p2, ""+
		"C-4 01 .. M90 ... ... | C-3 02 .. ... ... ... | C-4 03 .. ... ... ... | C-2 04 .. ... ... ... | C-5 01 .. ... ... ... | ... .. .. ... ... ...\n"+
		"\n\n\n"+
		"=== .. .. K02 ... ... | === .. .. ... ... ... | ... .. .. ... ... ... | === .. .. ... ... ... | === .. .. ... ... ... | ... .. .. ... ... ...\n"); err != nil {
		t.Fatal(err)
	}
	s.Patterns = append(s.Patterns, p2)
	s.Order = []int{0, 1, 0}

	lead := NewInstrument("lead", "sine")
	lead.SetLoaded(0, sineSample(261.6256, 1))
	lead.VolEnv = Envelope{Points: []EnvPoint{{0, 0}, {0.02, 1}, {0.2, 0.6}, {0.5, 0}}, Sustain: 2, LoopStart: -1, LoopEnd: -1}
	lead.PitchEnv = Envelope{Points: []EnvPoint{{0, 1}, {0.05, 0}}, Sustain: -1, LoopStart: -1, LoopEnd: -1}
	lead.VibRate, lead.VibDepth, lead.VibDelay = 5, 0.2, 0.1
	lead.Interp = InterpLinear

	pad := NewInstrument("pad", "saw")
	pad.SetLoaded(0, stereoSample(0.8))
	pad.Polyphony = 3
	pad.FadeOut = 0.3
	pad.PanEnv = Envelope{Points: []EnvPoint{{0, -1}, {0.3, 1}, {0.6, -1}}, Sustain: -1, LoopStart: 0, LoopEnd: 2}
	pad.Filter, pad.Cutoff, pad.Resonance = FilterBandPass, 1200, 0.4
	pad.FilterEnv = Envelope{Points: []EnvPoint{{0, 2}, {0.4, -1}}, Sustain: -1, LoopStart: -1, LoopEnd: -1}

	hit := NewInstrument("hit", "hit")
	hit.SetLoaded(0, oneShot(0.3))
	hit.Interp = InterpNearest

	bass := NewInstrument("bass", "sine")
	bass.SetLoaded(0, sineSample(261.6256, 1))
	bass.SetLoaded(1, stereoSample(0.5))
	bass.Samples = []string{"sine", "saw"}
	bass.Zones = []Zone{{Sample: 0, Low: NoteMin, High: 30}, {Sample: 1, Low: 31, High: NoteMax, BaseNote: NoteC4 - 12}}
	bass.Filter, bass.Cutoff, bass.Resonance = FilterHighPass, 300, 0.7
	bass.Transpose, bass.FineTune = 12, -20

	s.Instruments = []*Instrument{lead, pad, hit, bass}
	s.Channels[1].Pan, s.Channels[1].SendA = -0.5, 0.4
	s.Channels[2].Drive = 0.7
	s.Channels[3].Crush, s.Channels[3].SendB = 6, 0.5
	s.Channels[4].Volume, s.Channels[4].Pan, s.Channels[4].SendA = 0.7, 0.6, 0.2
	s.Gain = 0.8
	s.Compressor = CompressorSpec{Enabled: true, Threshold: -18, Ratio: 4, Attack: 0.003, Release: 0.1, Makeup: 3}
	s.Delay = DelaySpec{Beats: 0.5, Feedback: 0.5, Tone: 0.4, PingPong: true, Return: 0.7}
	s.Params = []Param{{Name: "bright", Default: 0.3}, {Name: "level", Default: 1}}
	s.Bindings = []Binding{
		{Param: "bright", Channel: "Ch 2", Target: BindCutoff, From: 300, To: 12000},
		{Param: "level", Channel: "Ch 5", Target: BindVolume, From: 0.2, To: 1},
		{Param: "level", Channel: "Ch 1", Target: BindPan, From: -0.5, To: 0.5},
		{Param: "bright", Channel: "Ch 6", Target: BindSendA, From: 0, To: 1},
		{Param: "bright", Channel: "Ch 6", Target: BindSendB, From: 1, To: 0},
	}
	s.Markers = map[uint8]string{1: "m"}
	s.Interp = InterpCubic
	return s
}

// goldenDriven renders the effects song while sending commands on a fixed
// schedule, the way the editor and scripts do.
func goldenDriven(t testing.TB) []float32 {
	s := goldenEffectsSong(t)
	stg := goldenEffectsSong(t)
	stg.Order = []int{1}
	p := NewPlayer(s)
	p.Start("")
	const block = 480
	out := make([]float32, 0, 600*block*2)
	buf := make([]float32, block*2)
	for i := 0; i < 600; i++ {
		switch i {
		case 10:
			p.Note(1, 2, NoteC4+7, 0.8)
		case 40:
			p.NoteOff(1)
			p.SetParam("bright", 1, 1.5)
		case 60:
			p.Mute("Ch 3", true, 0.2)
			p.NoteFor(1, NoteC4+12, 1, 0.3)
		case 90:
			p.Stinger(stg, QuantBar)
			p.Tempo(150, 2)
		case 130:
			p.Mute("Ch 3", false, 0.1)
			p.Solo(1)
		case 170:
			p.Solo(-1)
			p.SetParam("level", 0, 0)
		case 220:
			p.PlayFrom(1, 0)
		case 300:
			p.Pause(true)
		case 310:
			p.Pause(false)
		case 400:
			p.StopTransport()
			p.Note(2, 4, NoteC4-12, 1)
		case 450:
			p.NoteOff(2)
		}
		p.Render(buf, block, testRate)
		out = append(out, buf...)
	}
	return out
}

// goldenCases is every render the refactor must leave untouched.
func goldenCases(t *testing.T) map[string]func() []float32 {
	return map[string]func() []float32{
		"effects": func() []float32 {
			return Render(goldenEffectsSong(t), RenderOptions{Rate: testRate, Tail: 2})
		},
		"effects_44k": func() []float32 {
			return Render(goldenEffectsSong(t), RenderOptions{Rate: 44100, Seconds: 4})
		},
		"effects_params": func() []float32 {
			return Render(goldenEffectsSong(t), RenderOptions{Rate: testRate, Seconds: 5, Params: map[string]float32{"bright": 0.9, "level": 0.1}})
		},
		"driven": func() []float32 { return goldenDriven(t) },
		"simple": func() []float32 {
			return Render(testSong(t, "C-4 01 .. ... ... | E-4 01 20 ... ...\n\nG-4 01 .. V44 ... | === .. .. ... ...\n"), RenderOptions{Rate: testRate, Seconds: 1})
		},
		"crushed": func() []float32 {
			s := testSong(t, "C-4 01 .. ... ...\n")
			s.Channels[0].Crush = 3
			return Render(s, RenderOptions{Rate: testRate, Seconds: 0.5})
		},
	}
}

// goldenHashes were captured from the pre-Engine Player.
var goldenHashes = map[string]string{
	"effects":        "eaa7881e725dbb31",
	"effects_44k":    "1d7299ba72f6e829",
	"effects_params": "b19867cd81b7ac19",
	"driven":         "88c4da2dc4abf473",
	"simple":         "432a9daa9655d652",
	"crushed":        "6363c47b3cd8d187",
}

func TestEngineGolden(t *testing.T) {
	for name, f := range goldenCases(t) {
		data := f()
		if pk := Peak(data); pk < 0.05 || pk != pk || pk > 4 {
			t.Errorf("%s: peak %v — the case should be audible and finite", name, pk)
		}
		got := Hash(data)
		if os.Getenv("TRACKER_GOLDEN_PRINT") != "" {
			t.Logf("%q: %q,", name, got)
			continue
		}
		if want := goldenHashes[name]; got != want {
			t.Errorf("%s: hash %s, want %s — the Tracker's sound changed", name, got, want)
		}
	}
}
