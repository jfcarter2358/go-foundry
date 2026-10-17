package tracker

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"foundry/internal/fileutil"
)

// dcSample is a constant level, so where a note starts and how loud it is
// can be read straight off the output.
func dcSample(name string, level float32, secs float64) *Sample {
	d := make([]float32, int(secs*testRate))
	for i := range d {
		d[i] = level
	}
	return &Sample{Name: name, Data: d, Channels: 1, Rate: testRate, BaseNote: NoteC4, Volume: 1,
		Loop: LoopForward, LoopStart: 0, LoopEnd: len(d)}
}

// quietEngine has one strip at unity, dry, with nothing on the buses.
func quietEngine() *Engine {
	e := NewEngine(1)
	e.SetBuses(ReverbSpec{}, DelaySpec{}, CompressorSpec{}, 1)
	e.SetStrip(0, StripControls{Volume: 1})
	e.SetRate(testRate)
	return e
}

func firstSound(out []float32) int {
	for i := 0; i < len(out)/2; i++ {
		if out[i*2] != 0 || out[i*2+1] != 0 {
			return i
		}
	}
	return -1
}

// A cue lands on its frame, inside a block and across renders.
func TestEngineCuesAreSampleAccurate(t *testing.T) {
	in := NewInstrument("dc", "dc")
	in.SetLoaded(0, dcSample("dc", 0.5, 1))
	for _, at := range []int{0, 1, 63, 100, 333, 479, 700, 1500} {
		e := quietEngine()
		if !e.Cue(Cue{At: at, Kind: CueNoteOn, Strip: 0, Inst: in, Pitch: float32(NoteC4)}) {
			t.Fatal("cue refused")
		}
		var out []float32
		buf := make([]float32, 480*2)
		for r := 0; r < 4; r++ {
			e.Render(buf, 480, testRate)
			out = append(out, buf...)
		}
		if got := firstSound(out); got != at {
			t.Errorf("note cued at frame %d first sounds at %d", at, got)
		}
	}
}

// Notes on, off, controlled and killed through cues, in time order even when
// queued out of order.
func TestEngineCueOrderAndNoteControl(t *testing.T) {
	in := NewInstrument("dc", "dc")
	in.SetLoaded(0, dcSample("dc", 0.5, 1))
	in.FadeOut = 0.001
	e := quietEngine()
	id := e.NewNoteID()
	e.Cue(Cue{At: 400, Kind: CueNoteOff, Note: id})
	e.Cue(Cue{At: 100, Kind: CueNoteOn, Note: id, Strip: 0, Inst: in, Pitch: float32(NoteC4), Opts: NoteOptions{Volume: 0.5}})
	e.Cue(Cue{At: 200, Kind: CueNoteControl, Note: id, Volume: 1})
	buf := make([]float32, 960*2)
	e.Render(buf, 960, testRate)
	if firstSound(buf) != 100 {
		t.Fatalf("first sound at %d", firstSound(buf))
	}
	// Level 0.5 * sample 0.5 at centre pan (equal power, cos 45°).
	c := float32(math.Cos(math.Pi / 4))
	// (The first block after a start or a change ramps; read past it.)
	if v := buf[180*2]; math.Abs(float64(v-0.25*c)) > 1e-4 {
		t.Errorf("volume 0.5 plays at %v, want %v", v, 0.25*c)
	}
	if v := buf[300*2]; math.Abs(float64(v-0.5*c)) > 1e-4 {
		t.Errorf("after the control cue: %v, want %v", v, 0.5*c)
	}
	if v := buf[900*2]; v != 0 {
		t.Errorf("note-off at 400 still sounding at 900: %v", v)
	}
	if e.Playing(id) || e.PendingCues() != 0 {
		t.Error("the note should have ended and the queue drained")
	}
}

// Nothing on the render path allocates: the Tracker's Player and the
// cue-driven Engine both.
func TestRenderAllocationFree(t *testing.T) {
	s := goldenEffectsSong(t)
	p := NewPlayer(s)
	p.Start("")
	buf := make([]float32, 480*2)
	p.Render(buf, 480, testRate) // first render sizes everything
	if n := testing.AllocsPerRun(200, func() { p.Render(buf, 480, testRate) }); n != 0 {
		t.Errorf("Player.Render allocates %v times a block", n)
	}

	in := layeredInstrument()
	e := quietEngine()
	e.Render(buf, 480, testRate)
	k := 0
	if n := testing.AllocsPerRun(200, func() {
		k++
		id := e.NewNoteID()
		e.Cue(Cue{At: k % 480, Kind: CueNoteOn, Note: id, Strip: 0, Inst: in, Pitch: float32(int(NoteC4) + k%12), Opts: NoteOptions{Velocity: 0.55}})
		e.Cue(Cue{At: 470, Kind: CueNoteOff, Note: id})
		e.Render(buf, 480, testRate)
	}); n != 0 {
		t.Errorf("Engine.Render with cues allocates %v times a block", n)
	}
}

// layeredInstrument has a soft and a loud layer that overlap between
// velocity 60 and 80, the loud one in two round-robin takes.
func layeredInstrument() *Instrument {
	in := NewInstrument("layered", "")
	in.Samples = []string{"soft", "loud", "loud2", "pizz"}
	in.SetLoaded(0, dcSample("soft", 0.2, 1))
	in.SetLoaded(1, dcSample("loud", 0.8, 1))
	in.SetLoaded(2, dcSample("loud2", 0.7, 1))
	in.SetLoaded(3, dcSample("pizz", 0.4, 0.2))
	in.Zones = []Zone{{Sample: 0, Low: NoteMin, High: NoteMax, Layers: []Layer{
		{Sample: 0, VelLow: 0, VelHigh: 80},
		{Sample: 1, RoundRobin: []int{2}, VelLow: 60, VelHigh: 127, Gain: -6},
	}}}
	in.Articulations = []Articulation{
		{Name: "pizzicato", Triggers: []string{"pizz.", "pizz"}, Zones: []Zone{{Sample: 3, Low: NoteMin, High: NoteMax}}, VolEnv: EnvelopeOff()},
		{Name: "staccato", VolEnv: Envelope{Points: []EnvPoint{{0, 1}, {0.05, 0}}, Sustain: -1, LoopStart: -1, LoopEnd: -1}, Gain: -3},
	}
	return in
}

func TestVelocityLayersCrossfade(t *testing.T) {
	in := layeredInstrument()
	var picks [2]voicePick
	if n := in.pick(NoteC4, 0.3, 0, nil, &picks); n != 1 || picks[0].smp.Name != "soft" || picks[0].level != 1 {
		t.Fatalf("velocity 0.3: %d %+v", n, picks[0])
	}
	if n := in.pick(NoteC4, 0.95, 0, nil, &picks); n != 1 || picks[0].smp.Name != "loud" {
		t.Fatalf("velocity 0.95: %d %+v", n, picks[0])
	}
	if g := picks[0].level; math.Abs(float64(g)-math.Pow(10, -6.0/20)) > 1e-5 {
		t.Fatalf("the loud layer's -6 dB trim reads %v", g)
	}
	// Half-way through the overlap: both, at equal power.
	n := in.pick(NoteC4, 70.0/127, 0, nil, &picks)
	if n != 2 || picks[0].smp.Name != "loud" || picks[1].smp.Name != "soft" {
		t.Fatalf("overlap: %d %v %v", n, picks[0].smp.Name, picks[1].smp.Name)
	}
	hi := float64(picks[0].level) / math.Pow(10, -6.0/20)
	lo := float64(picks[1].level)
	if math.Abs(hi*hi+lo*lo-1) > 1e-4 || math.Abs(hi-lo) > 1e-4 {
		t.Fatalf("crossfade weights %v and %v are not equal power at the midpoint", hi, lo)
	}
	// The engine starts both as one note.
	e := quietEngine()
	id := e.NoteOn(0, in, float32(NoteC4), NoteOptions{Velocity: 70.0 / 127})
	voices := 0
	for i := range e.voices {
		if e.voices[i].active && e.voices[i].group == id {
			voices++
		}
	}
	if voices != 2 {
		t.Fatalf("a crossfaded note started %d voices", voices)
	}
	e.Kill(id)
	buf := make([]float32, 960*2)
	e.Render(buf, 960, testRate)
	if e.Playing(id) {
		t.Fatal("kill left a layer sounding")
	}
}

func TestRoundRobinCycles(t *testing.T) {
	in := layeredInstrument()
	e := quietEngine()
	var got []string
	for i := 0; i < 5; i++ {
		var picks [2]voicePick
		in.pick(NoteC4, 1, 0, e, &picks)
		got = append(got, picks[0].smp.Name)
	}
	if want := []string{"loud", "loud2", "loud", "loud2", "loud"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("round robin played %v, want %v", got, want)
	}
	// Without an engine every pick is the first take.
	var picks [2]voicePick
	in.pick(NoteC4, 1, 0, nil, &picks)
	in.pick(NoteC4, 1, 0, nil, &picks)
	if picks[0].smp.Name != "loud" {
		t.Fatalf("an engine-less pick advanced the round robin: %s", picks[0].smp.Name)
	}
}

func TestArticulationSelection(t *testing.T) {
	in := layeredInstrument()
	for word, want := range map[string]int{"pizzicato": 1, "Pizz.": 1, "staccato": 2, "arco": 0, "": 0} {
		if got := in.ArticulationIndex(word); got != want {
			t.Errorf("ArticulationIndex(%q) = %d, want %d", word, got, want)
		}
	}
	e := quietEngine()
	id := e.NoteOn(0, in, float32(NoteC4), NoteOptions{Articulation: 1})
	var v *pvoice
	for i := range e.voices {
		if e.voices[i].active && e.voices[i].group == id {
			v = &e.voices[i]
		}
	}
	if v == nil || v.smp.Name != "pizz" {
		t.Fatalf("pizzicato played %+v", v)
	}
	// Staccato keeps the zones and swaps the envelope: gone in 50 ms.
	e = quietEngine()
	id = e.NoteOn(0, in, float32(NoteC4), NoteOptions{Articulation: 2})
	buf := make([]float32, 4800*2)
	e.Render(buf, 4800, testRate)
	if pk := Peak(buf[:480*2]); pk < 0.1 {
		t.Fatalf("staccato silent at its start: %v", pk)
	}
	if pk := Peak(buf[3000*2:]); pk > 1e-4 {
		t.Fatalf("staccato still sounding after its envelope: %v", pk)
	}
}

// The Tracker's @ command picks an articulation; its volume column picks the
// velocity layer.
func TestTrackerArticulationAndVelocity(t *testing.T) {
	s := testSong(t, "C-4 01 .. @01 ...\n\n\n\nC-4 01 08 @00 ...\n\n\n\nC-4 01 40 ... ...\n")
	s.Instruments = []*Instrument{layeredInstrument()}
	p := NewPlayer(s)
	p.Start("")
	buf := make([]float32, 480*2)
	sampleOf := func() string {
		for i := range p.eng.voices {
			if v := &p.eng.voices[i]; v.active && v.attached {
				return v.smp.Name
			}
		}
		return ""
	}
	p.Render(buf, 480, testRate)
	if got := sampleOf(); got != "pizz" {
		t.Fatalf("@01 played %q, want pizz", got)
	}
	// Row 4 is 0.5 s in at 120 BPM, 4 rows a beat.
	for i := 0; i < 50; i++ {
		p.Render(buf, 480, testRate)
	}
	if got := sampleOf(); got != "soft" {
		t.Fatalf("volume 08 (velocity 0.125) played %q, want soft", got)
	}
	for i := 0; i < 50; i++ {
		p.Render(buf, 480, testRate)
	}
	if got := sampleOf(); got != "loud" && got != "loud2" {
		t.Fatalf("volume 40 played %q, want a loud take", got)
	}
}

// fullInstrument sets every field a .finst carries.
func fullInstrument() *Instrument {
	in := layeredInstrument()
	in.Name, in.Description = "Layered", "A test of everything"
	in.Volume, in.Pan, in.Transpose, in.FineTune = 0.8, -0.2, -12, 5
	in.VolEnv = Envelope{Points: []EnvPoint{{0, 0}, {0.1, 1}, {0.4, 0.7}, {1, 0}}, Sustain: 2, LoopStart: -1, LoopEnd: -1}
	in.FilterEnv = Envelope{Points: []EnvPoint{{0, 1}, {0.3, 0}}, Sustain: -1, LoopStart: -1, LoopEnd: -1}
	in.Filter, in.Cutoff, in.Resonance = FilterLowPass, 3000, 0.3
	in.FadeOut, in.VibRate, in.VibDepth, in.VibDelay = 0.4, 5, 0.1, 0.3
	in.Polyphony, in.Interp = 4, InterpLinear
	in.Zones = append(in.Zones, Zone{Sample: 3, Low: 100, High: NoteMax, BaseNote: 100, RoundRobin: []int{1, 2}})
	in.Zones[0].High = 99
	in.Playback = Playback{Low: 44, High: 92, Transposition: -2, Clef: "treble", Gliss: GlissDiatonic, Staccato: 0.4, Release: 0.25,
		StaffLines: 1, Tuning: "guitar", Drums: []DrumNote{{Key: 36, Name: "Kick", Line: 1, Head: "normal", Shortcut: "B"},
			{Key: 46, Name: "Hat open", Line: 9, Head: "x", Articulation: "open"}}}
	return in
}

func TestInstrumentFileRoundTrip(t *testing.T) {
	in := fullInstrument()
	b, err := MarshalInstrument(in)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseInstrument(b)
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	b2, _ := MarshalInstrument(back)
	if string(b) != string(b2) {
		t.Fatalf("not stable:\n%s\n---\n%s", b, b2)
	}
	want := in.Clone()
	want.loaded, back.loaded = nil, nil
	if !reflect.DeepEqual(want, back) {
		t.Fatalf("round trip changed the instrument:\n%+v\n%+v", want, back)
	}
	for _, key := range []string{"layers:", "velocity: [60, 127]", "round_robin: [2]", "articulations:", "triggers: [pizz., pizz]", "playback:", "range: [G-3, G-7]", "gliss: diatonic", "transposition: -2", "staff_lines: 1", "articulation: open", "shortcut: B"} {
		if !strings.Contains(string(b), key) {
			t.Errorf("the file has no %q:\n%s", key, b)
		}
	}
	if _, err := ParseInstrument([]byte("version: 1\nname: x\nsamples: [a]\nplayback: {clef: soprano}\n")); err == nil {
		t.Error("an unknown clef was accepted")
	}
	for _, bad := range []string{"{staff_lines: 7}", "{drums: [{key: 200, name: x, line: 1}]}", "{drums: [{key: 36, name: x, line: 1}, {key: 36, name: y, line: 2}]}",
		"{drums: [{key: 36, line: 1}]}", "{drums: [{key: 36, name: x, line: 1, head: star}]}", "{drums: [{key: 36, name: x, line: 1, shortcut: Q}]}",
		"{drums: [{key: 36, name: x, line: 40}]}"} {
		if _, err := ParseInstrument([]byte("version: 1\nname: x\nsamples: [a]\nplayback: " + bad + "\n")); err == nil {
			t.Errorf("playback %s was accepted", bad)
		}
	}
	if _, err := ParseInstrument([]byte("version: 1\nname: x\nsamples: [a]\nzones: [{sample: 0, low: C-0, high: B-9, layers: [{sample: 3, velocity: [0, 127]}]}]\n")); err == nil {
		t.Error("a layer naming a missing sample was accepted")
	}
}

// useTempResources points the library at a scratch resources/ tree.
func useTempResources(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = dir
	ReloadSongs()
	t.Cleanup(func() { fileutil.ResourcesPath = prev; ReloadSongs() })
	return dir
}

// A song slot can name a library instrument; old songs with embedded ones
// still load; the slot saves as its ref and plays what the .finst says.
func TestSongReferencesLibraryInstrument(t *testing.T) {
	useTempResources(t)
	smp := dcSample("keys/dc", 0.5, 0.5)
	if err := SaveSample(smp); err != nil {
		t.Fatal(err)
	}
	lib := NewInstrument("Library DC", "keys/dc")
	lib.Volume = 0.5
	lib.Articulations = []Articulation{{Name: "staccato", FadeOut: 0.02}}
	lib.Playback.Clef = "bass"
	if err := SaveInstrument("keys/dc_piano", lib); err != nil {
		t.Fatal(err)
	}
	if got := ScanInstrumentNames(); !reflect.DeepEqual(got, []string{"keys/dc_piano"}) {
		t.Fatalf("library lists %v", got)
	}
	loaded, err := LoadInstrument("keys/dc_piano")
	if err != nil || loaded.Category != "keys" || loaded.Loaded(0) == nil || loaded.Playback.Clef != "bass" {
		t.Fatalf("library instrument: %+v %v", loaded, err)
	}

	// A version-1 song from before .finst, plus one slot that refs it.
	src := "version: 1\nbpm: 120\nchannels: [{name: a}]\norder: [0]\n" +
		"instruments:\n  - name: Embedded\n    samples: [keys/dc]\n  - name: Piano\n    ref: keys/dc_piano\n" +
		"patterns:\n  - rows: 4\n    data: |\n      C-4 02 .. ...\n"
	s, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if errs := ResolveSamples(s); len(errs) != 0 {
		t.Fatal(errs)
	}
	slot := s.Instruments[1]
	if slot.Name != "Piano" || slot.Ref != "keys/dc_piano" || slot.Volume != 0.5 || slot.Loaded(0) == nil || len(slot.Articulations) != 1 {
		t.Fatalf("the ref slot resolved to %+v", slot)
	}
	b, err := Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "ref: keys/dc_piano") || strings.Count(string(b), "samples:") != 1 {
		t.Fatalf("a ref slot should save as its name and ref only:\n%s", b)
	}
	s.Reverb.Return, s.Delay.Return = 0, 0
	if pk := Peak(Render(s, RenderOptions{Rate: testRate, Seconds: 0.3})); pk < 0.1 {
		t.Fatalf("the library instrument is silent: %v", pk)
	}
	// Embedding makes it the song's own.
	slot.Embed()
	b, _ = Marshal(s)
	if strings.Contains(string(b), "ref:") || strings.Count(string(b), "samples:") != 2 {
		t.Fatalf("an embedded slot should save in full:\n%s", b)
	}
	// A missing library instrument is reported, and the slot is silent.
	s2, _ := Parse([]byte(strings.Replace(src, "keys/dc_piano", "keys/none", 1)))
	if errs := ResolveSamples(s2); len(errs) != 1 {
		t.Fatalf("a missing .finst reported %v", errs)
	}
	path := filepath.Join(InstrumentsDir(), "keys", "dc_piano.finst")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveSampleRenumbers(t *testing.T) {
	in := layeredInstrument()
	in.RemoveSample(1) // "loud": the loud layer's own sample
	if len(in.Samples) != 3 || in.Samples[1] != "loud2" {
		t.Fatalf("samples %v", in.Samples)
	}
	l := in.Zones[0].Layers[1]
	if l.Sample != 1 || len(l.RoundRobin) != 1 || l.RoundRobin[0] != 1 {
		t.Fatalf("loud layer after removing its sample: %+v", l)
	}
	if in.Articulations[0].Zones[0].Sample != 2 || in.Loaded(2).Name != "pizz" {
		t.Fatalf("pizzicato zone did not follow its sample down: %+v", in.Articulations[0].Zones[0])
	}
}

// BenchmarkEngine32Notes is the cue-driven path a score drives: 32 layered
// notes held through a strip each, re-cued every block.
func BenchmarkEngine32Notes(b *testing.B) {
	in := layeredInstrument()
	in.Filter, in.Cutoff, in.Resonance = FilterLowPass, 2000, 0.3
	e := NewEngine(32)
	for i := 0; i < 32; i++ {
		e.SetStrip(i, StripControls{Volume: 0.5, SendA: 0.3})
	}
	e.SetRate(testRate)
	ids := make([]NoteID, 32)
	for i := range ids {
		ids[i] = e.NoteOn(i, in, float32(37+i), NoteOptions{Velocity: 0.9})
	}
	buf := make([]float32, 480*2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for k, id := range ids {
			e.Cue(Cue{At: (k * 13) % 480, Kind: CueNoteControl, Note: id, Volume: 0.8, Bend: float32(k%3) * 0.1})
		}
		e.Render(buf, 480, testRate)
	}
	b.ReportMetric(float64(b.Elapsed().Microseconds())/float64(b.N), "us/10ms")
}
