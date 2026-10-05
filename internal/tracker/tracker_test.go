package tracker

import (
	"math"
	"strings"
	"testing"
)

const testRate = 48000

// sineSample is a looping sine at freq Hz, base note C-4.
func sineSample(freq float64, secs float64) *Sample {
	n := int(secs * testRate)
	d := make([]float32, n)
	for i := range d {
		d[i] = float32(math.Sin(2 * math.Pi * freq * float64(i) / testRate))
	}
	period := testRate / freq
	end := int(math.Floor(float64(n)/period) * period)
	return &Sample{Name: "sine", Data: d, Channels: 1, Rate: testRate, BaseNote: NoteC4, Volume: 1,
		Loop: LoopForward, LoopStart: 0, LoopEnd: end}
}

// testSong has one sine instrument and a 16-row pattern.
func testSong(t *testing.T, data string) *Song {
	t.Helper()
	s := NewSong("test")
	s.SetChannelCount(2)
	s.Patterns[0].Resize(16, 2)
	if err := ParsePatternText(s.Patterns[0], data); err != nil {
		t.Fatal(err)
	}
	in := NewInstrument("sine", "sine")
	in.SetLoaded(0, sineSample(261.6256, 1))
	s.Instruments = []*Instrument{in}
	return s
}

func TestNoteNames(t *testing.T) {
	for _, n := range []uint8{1, 13, NoteC4, 58, NoteMax, NoteOff, NoteCut, NoteNone} {
		got, err := ParseNote(NoteName(n))
		if err != nil || got != n {
			t.Fatalf("note %d: %q -> %d, %v", n, NoteName(n), got, err)
		}
	}
	if NoteName(58) != "A-4" {
		t.Fatalf("58 is %s, want A-4", NoteName(58))
	}
	if n, _ := ParseNote("Db4"); NoteName(n) != "C#4" {
		t.Fatalf("Db4 read as %s", NoteName(n))
	}
}

func TestCellRoundTrip(t *testing.T) {
	c := Cell{Note: 58, Instr: 3, Vol: 0x20, FX: [MaxFXColumns]Effect{{FxVibrato, 0x48}, {FxPan, 0xC0}}}
	txt := FormatCell(c, 2)
	if txt != "A-4 03 20 V48 PC0" {
		t.Fatalf("formatted %q", txt)
	}
	back, err := ParseCell(txt)
	if err != nil || back != c {
		t.Fatalf("parsed %+v, %v", back, err)
	}
	if _, err := ParseCell("C-4 01 .. Q?? ..."); err == nil {
		t.Fatal("bad effect accepted")
	}
}

func TestFileRoundTrip(t *testing.T) {
	s := testSong(t, "C-4 01 .. ... ... | ... .. .. ... ...\n\nE-4 01 30 V48 ... | G-4 01 .. ... ...\n")
	s.Sections = []Section{{Name: "a", Start: 0, End: 1, Loop: true}}
	s.Params = []Param{{Name: "intensity", Default: 0.25}}
	s.Bindings = []Binding{{Param: "intensity", Channel: "Ch 1", Target: BindCutoff, From: 400, To: 20000}}
	s.Markers = map[uint8]string{1: "drop"}
	s.Instruments[0].VolEnv = Envelope{Points: []EnvPoint{{0, 0}, {0.01, 1}, {0.5, 0.6}, {1, 0}}, Sustain: 2, LoopStart: -1, LoopEnd: -1}
	b, err := Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(b)
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	b2, _ := Marshal(back)
	if string(b) != string(b2) {
		t.Fatalf("not stable:\n%s\n---\n%s", b, b2)
	}
	if back.Patterns[0].Cells[2][0].FX[0] != (Effect{FxVibrato, 0x48}) {
		t.Fatalf("cell lost: %+v", back.Patterns[0].Cells[2][0])
	}
	if !strings.Contains(string(b), "data: |") {
		t.Fatalf("pattern data should be a text block:\n%s", b)
	}
}

func TestRenderIsDeterministicAndAudible(t *testing.T) {
	s := testSong(t, "C-4 01 .. ... ... | E-4 01 20 ... ...\n\nG-4 01 .. V44 ... | === .. .. ... ...\n")
	a := Render(s, RenderOptions{Rate: testRate, Seconds: 1})
	b := Render(s, RenderOptions{Rate: testRate, Seconds: 1})
	if Hash(a) != Hash(b) {
		t.Fatal("two renders differ")
	}
	if pk := Peak(a); pk < 0.1 || pk > 2 {
		t.Fatalf("peak %v", pk)
	}
}

// zeroCrossFreq estimates the frequency of a mono-ish signal.
func zeroCrossFreq(st []float32, from, to int) float64 {
	n := 0
	first, last := -1, -1
	for i := from + 1; i < to; i++ {
		if st[(i-1)*2] < 0 && st[i*2] >= 0 {
			if first < 0 {
				first = i
			}
			last = i
			n++
		}
	}
	if n < 2 {
		return 0
	}
	return float64(n-1) * testRate / float64(last-first)
}

func TestPitch(t *testing.T) {
	s := testSong(t, "A-4 01 .. ... ...\n")
	s.Reverb.Return, s.Delay.Return = 0, 0
	out := Render(s, RenderOptions{Rate: testRate, Seconds: 0.5})
	f := zeroCrossFreq(out, 2000, 20000)
	if math.Abs(f-440) > 1 {
		t.Fatalf("A-4 played at %.2f Hz", f)
	}
}

func TestArpeggioAndSlideChangePitch(t *testing.T) {
	plain := testSong(t, "C-4 01 .. ... ...\n")
	slid := testSong(t, "C-4 01 .. U40 ...\n... .. .. U40 ...\n... .. .. U40 ...\n")
	for _, s := range []*Song{plain, slid} {
		s.Reverb.Return, s.Delay.Return = 0, 0
	}
	a := Render(plain, RenderOptions{Rate: testRate, Seconds: 0.5})
	b := Render(slid, RenderOptions{Rate: testRate, Seconds: 0.5})
	// Row 3 starts at 3 rows * 125 ms = 0.375 s.
	fa := zeroCrossFreq(a, 18500, 23000)
	fb := zeroCrossFreq(b, 18500, 23000)
	if fb < fa*1.3 {
		t.Fatalf("slide up did not raise the pitch: %.1f -> %.1f", fa, fb)
	}
}

func TestSectionsQuantised(t *testing.T) {
	s := testSong(t, "C-4 01 .. ... ...\n")
	s.Patterns = append(s.Patterns, NewPattern("b", 16, 2))
	s.Order = []int{0, 1}
	s.Sections = []Section{{Name: "a", Start: 0, End: 1, Loop: true}, {Name: "b", Start: 1, End: 2, Loop: true}}
	p := NewPlayer(s)
	p.Start("a")
	buf := make([]float32, 480*2)
	// 2 rows in (row = 125 ms at 120 BPM, 4 LPB), ask for b on the next bar.
	for i := 0; i < 25; i++ {
		p.Render(buf, 480, testRate)
	}
	p.Section("b", QuantBar)
	for i := 0; i < 300; i++ {
		p.Render(buf, 480, testRate)
		pos := p.Position()
		if pos.Order == 1 {
			if pos.Section != 1 {
				t.Fatalf("in order 1 but section %d", pos.Section)
			}
			return
		}
		if pos.Row != 0 && pos.Row%16 == 0 {
			t.Fatalf("passed a bar without switching: %+v", pos)
		}
	}
	t.Fatal("section b never started")
}

func TestSectionLoopsAndNext(t *testing.T) {
	s := testSong(t, "")
	for i := range s.Patterns[0].Cells {
		s.Patterns[0].Cells[i] = s.Patterns[0].Cells[i][:2]
	}
	s.Patterns[0].Resize(4, 2)
	s.Patterns = append(s.Patterns, NewPattern("b", 4, 2))
	s.Order = []int{0, 1}
	s.Sections = []Section{{Name: "intro", Start: 0, End: 1, Next: "loop"}, {Name: "loop", Start: 1, End: 2, Loop: true}}
	p := NewPlayer(s)
	p.Start("intro")
	buf := make([]float32, 480*2)
	var evs []Event
	for i := 0; i < 400; i++ {
		p.Render(buf, 480, testRate)
		evs = p.Events(evs)
	}
	if p.Position().Order != 1 {
		t.Fatalf("expected to be looping in order 1, at %+v", p.Position())
	}
	found := false
	for _, e := range evs {
		if e.Kind == EventSection && e.Name == "loop" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no section event for loop: %+v", evs)
	}
}

func TestMarkerEvent(t *testing.T) {
	s := testSong(t, "... .. .. ... ...\nC-4 01 .. E07 ...\n")
	s.Markers = map[uint8]string{7: "drop"}
	p := NewPlayer(s)
	p.Start("")
	buf := make([]float32, 480*2)
	var evs []Event
	for i := 0; i < 40; i++ {
		p.Render(buf, 480, testRate)
		evs = p.Events(evs)
	}
	for _, e := range evs {
		if e.Kind == EventMarker && e.Name == "drop" && e.Value == 7 && e.Row == 1 {
			return
		}
	}
	t.Fatalf("no marker event: %+v", evs)
}

func TestMuteAndParamBinding(t *testing.T) {
	s := testSong(t, "C-4 01 .. ... ...\n")
	s.Reverb.Return, s.Delay.Return = 0, 0
	s.Params = []Param{{Name: "level", Default: 1}}
	s.Bindings = []Binding{{Param: "level", Channel: "Ch 1", Target: BindVolume, From: 0, To: 1}}
	loud := Render(s, RenderOptions{Rate: testRate, Seconds: 0.3})
	quiet := Render(s, RenderOptions{Rate: testRate, Seconds: 0.3, Params: map[string]float32{"level": 0}})
	if Peak(loud) < 0.1 || Peak(quiet) > 0.001 {
		t.Fatalf("binding: loud %v quiet %v", Peak(loud), Peak(quiet))
	}

	p := NewPlayer(s)
	p.Start("")
	buf := make([]float32, 4800*2)
	p.Render(buf, 4800, testRate)
	p.Mute("Ch 1", true, 0.01)
	p.Render(buf, 4800, testRate)
	p.Render(buf, 4800, testRate)
	if pk := Peak(buf); pk > 0.001 {
		t.Fatalf("muted channel still at %v", pk)
	}
}

func TestEnvelope(t *testing.T) {
	e := Envelope{Points: []EnvPoint{{0, 0}, {0.1, 1}, {0.2, 0.5}, {0.4, 0}}, Sustain: 2, LoopStart: -1, LoopEnd: -1}
	tt := float32(0)
	for i := 0; i < 100; i++ {
		tt = e.Advance(tt, 0.01, false)
	}
	if v := e.Value(tt); math.Abs(float64(v-0.5)) > 1e-6 {
		t.Fatalf("held at %v, want sustain 0.5", v)
	}
	for i := 0; i < 30; i++ {
		tt = e.Advance(tt, 0.01, true)
	}
	if !e.Done(tt) || e.Value(tt) != 0 {
		t.Fatalf("released envelope at %v = %v", tt, e.Value(tt))
	}
}

func TestNoteOffReleasesAndSongEnds(t *testing.T) {
	s := testSong(t, "C-4 01 .. ... ...\n=== .. .. ... ...\n")
	s.Loop = false
	s.Patterns[0].Resize(4, 2)
	p := NewPlayer(s)
	p.Start("")
	buf := make([]float32, 480*2)
	ended := false
	for i := 0; i < 2000; i++ {
		if !p.Render(buf, 480, testRate) {
			ended = true
			break
		}
	}
	if !ended {
		t.Fatal("non-looping song never ended")
	}
}

func TestPreviewNote(t *testing.T) {
	s := testSong(t, "")
	p := NewPlayer(s)
	p.Note(1, 1, NoteC4, 1)
	buf := make([]float32, 4800*2)
	p.Render(buf, 4800, testRate)
	if Peak(buf) < 0.1 {
		t.Fatal("preview note silent")
	}
	p.NoteOff(1)
	p.Render(buf, 4800, testRate)
	p.Render(buf, 4800, testRate)
	if Peak(buf) > 0.01 {
		t.Fatalf("preview note did not release: %v", Peak(buf))
	}
}

func BenchmarkPlayer32Channels(b *testing.B) {
	s := NewSong("bench")
	s.SetChannelCount(32)
	s.Patterns[0].Resize(16, 32)
	in := NewInstrument("sine", "sine")
	in.SetLoaded(0, sineSample(261.6256, 1))
	in.Filter, in.Cutoff, in.Resonance = FilterLowPass, 2000, 0.3
	s.Instruments = []*Instrument{in}
	for r := 0; r < 16; r += 4 {
		for c := 0; c < 32; c++ {
			s.Patterns[0].Cells[r][c] = Cell{Note: uint8(37 + c), Instr: 1, Vol: VolNone, FX: [MaxFXColumns]Effect{{FxVibrato, 0x44}}}
		}
	}
	for c := range s.Channels {
		s.Channels[c].SendA = 0.3
	}
	p := NewPlayer(s)
	p.Start("")
	buf := make([]float32, 480*2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Render(buf, 480, testRate)
	}
	// 480 frames = 10 ms; the design budget is 0.5 ms per 10 ms block.
	b.ReportMetric(float64(b.Elapsed().Microseconds())/float64(b.N), "us/10ms")
}

func TestChannelInserts(t *testing.T) {
	s := testSong(t, "C-4 01 .. ... ...\n")
	s.Reverb.Return, s.Delay.Return = 0, 0
	clean := Render(s, RenderOptions{Rate: testRate, Seconds: 0.2})
	s.Channels[0].Crush = 3
	crushed := Render(s, RenderOptions{Rate: testRate, Seconds: 0.2})
	levels := map[float32]bool{}
	for i := 4800; i < len(crushed); i += 2 {
		levels[crushed[i]] = true
	}
	if len(levels) > 12 || Hash(clean) == Hash(crushed) {
		t.Fatalf("3-bit crush left %d distinct levels", len(levels))
	}
	s.Channels[0].Crush, s.Channels[0].Drive = 0, 1
	if Hash(Render(s, RenderOptions{Rate: testRate, Seconds: 0.2})) == Hash(clean) {
		t.Fatal("drive changed nothing")
	}
}
