package sfz

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/tracker"
)

const testRate = 48000

// sine is a mono sine at hz, frames long, at amplitude amp.
func sine(hz float64, frames int, amp float32) []float32 {
	d := make([]float32, frames)
	for i := range d {
		d[i] = amp * float32(math.Sin(2*math.Pi*hz*float64(i)/testRate))
	}
	return d
}

// writeWAV writes a mono 16-bit WAV, with a smpl chunk when root >= 0
// (and a loop when loopEnd > loopStart, loopEnd exclusive).
func writeWAV(t *testing.T, path string, data []float32, root int, loopStart, loopEnd int) {
	t.Helper()
	var buf bytes.Buffer
	if err := tracker.WriteWAV(&buf, data, 1, testRate, 16); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	if root >= 0 {
		le := binary.LittleEndian
		n := 0
		if loopEnd > loopStart {
			n = 1
		}
		ch := make([]byte, 8+36+24*n)
		copy(ch, "smpl")
		le.PutUint32(ch[4:], uint32(36+24*n))
		le.PutUint32(ch[8+12:], uint32(root))
		le.PutUint32(ch[8+28:], uint32(n))
		if n == 1 {
			l := ch[8+36:]
			le.PutUint32(l[8:], uint32(loopStart))
			le.PutUint32(l[12:], uint32(loopEnd-1))
		}
		b = append(b, ch...)
		le.PutUint32(b[4:], uint32(len(b)-8))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Headers fold into their regions, each level overriding the one above;
// #define, #include, comments, note names, octave_offset, default_path and
// a sample path with spaces all read.
func TestParseFoldsHeaders(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "common.sfzh"), "ampeg_release=0.7 // shared\n")
	text := `
// A comment, then a block one.
/* <region> sample=nope.wav
*/
#define $LO 55
<control> default_path=wavs/ octave_offset=0
<global> #include "common.sfzh"
 volume=-3 lovel=0 hivel=127
<group> lovel=64 seq_length=2
<region> sample=my violin p.wav lokey=$LO hikey=g#3 pitch_keycenter=g3 seq_position=1
<region> sample=b.wav key=c4 volume=-6 tune=12 transpose=-12 seq_position=2
<group> trigger=release
<region> sample=rel.wav
<region> sample=c.wav cutoff=800 ampeg_attack=0.1 ampeg_sustain=50 ampeg_decay=0.2 xfin_locc1=0
`
	f, err := Parse(text, filepath.Join(dir, "inst.sfz"))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Regions) != 4 {
		t.Fatalf("%d regions", len(f.Regions))
	}
	a, b, rel, c := f.Regions[0], f.Regions[1], f.Regions[2], f.Regions[3]
	if want := filepath.Join(dir, "wavs", "my violin p.wav"); a.Sample != want {
		t.Errorf("sample %q, want %q", a.Sample, want)
	}
	if a.LoKey != 55 || a.HiKey != 56 || a.Key != 55 || a.LoVel != 64 || a.HiVel != 127 || a.SeqLength != 2 || a.SeqPosition != 1 {
		t.Errorf("region a: %+v", a)
	}
	if a.Env.Release != 0.7 || a.Volume != -3 {
		t.Errorf("global opcodes: release %v volume %v", a.Env.Release, a.Volume)
	}
	if b.LoKey != 60 || b.HiKey != 60 || b.Key != 60 || b.Volume != -6 || b.Tune != 12 || b.Transpose != -12 || b.SeqPosition != 2 {
		t.Errorf("region b: %+v", b)
	}
	if rel.Trigger != "release" || rel.LoVel != 0 {
		t.Errorf("a new group starts from the global: %+v", rel)
	}
	if c.Cutoff != 800 || c.Env.Attack != 0.1 || c.Env.Sustain != 0.5 || c.Env.Decay != 0.2 {
		t.Errorf("region c: %+v", c)
	}
	if f.Ignored["xfin_locc1"] != 1 {
		t.Errorf("ignored: %v", f.Ignored)
	}

	// octave_offset moves every key.
	f, err = Parse("<control> octave_offset=1\n<region> sample=a.wav key=c3", filepath.Join(dir, "x.sfz"))
	if err != nil || f.Regions[0].LoKey != 60 {
		t.Fatalf("octave_offset: %v %+v", err, f.Regions)
	}
	// A value an opcode cannot take is skipped with a warning (once, for
	// a group's), the region keeping what it had: one typo does not
	// refuse a library.
	f, err = Parse("<group> lokey=h2 tune=nan\n<region> sample=a.wav hikey=70\n<region> sample=b.wav", "x.sfz")
	if err != nil || len(f.Regions) != 2 {
		t.Fatalf("bad values: %v %+v", err, f)
	}
	if r := f.Regions[0]; r.LoKey != 0 || r.HiKey != 70 || r.Tune != 0 {
		t.Errorf("a region under bad values: keys %d..%d tune %g", r.LoKey, r.HiKey, r.Tune)
	}
	if w := strings.Join(f.Warnings, "\n"); len(f.Warnings) != 2 || !strings.Contains(w, `lokey: bad key "h2"`) || !strings.Contains(w, "tune") {
		t.Errorf("warnings: %q", f.Warnings)
	}
}

// fixture writes a small library: two velocity layers over two key
// ranges, the low range's soft layer a two-turn round robin, a keyswitched
// pizzicato, and a release-trigger region.
func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	w := func(name string, hz float64, amp float32) {
		writeWAV(t, filepath.Join(dir, "samples", name), sine(hz, testRate/2, amp), -1, 0, 0)
	}
	w("low_p1.wav", 220, 0.2)
	w("low_p2.wav", 220, 0.21)
	w("low_f.wav", 220, 0.8)
	w("high_p.wav", 440, 0.2)
	w("high_f.wav", 440, 0.8)
	w("pizz.wav", 330, 0.5)
	w("rel.wav", 100, 0.1)
	// A looped sustain whose loop is in the file.
	writeWAV(t, filepath.Join(dir, "samples", "pad.wav"), sine(261.63, testRate, 0.4), 60, 4800, 4800+1834*10)
	writeFile(t, filepath.Join(dir, "strings.sfz"), `
<control> default_path=samples/
<global> sw_lokey=c1 sw_hikey=d1 sw_default=c1 ampeg_release=0.3
<group> sw_last=c1 sw_label=Sustain
<region> sample=low_p1.wav lokey=a2 hikey=e4 pitch_keycenter=a3 lovel=0 hivel=63 seq_length=2 seq_position=1
<region> sample=low_p2.wav lokey=a2 hikey=e4 pitch_keycenter=a3 lovel=0 hivel=63 seq_length=2 seq_position=2
<region> sample=low_f.wav lokey=a2 hikey=e4 pitch_keycenter=a3 lovel=64 hivel=127
<region> sample=high_p.wav lokey=f4 hikey=c6 pitch_keycenter=a4 lovel=0 hivel=63
<region> sample=high_f.wav lokey=f4 hikey=c6 pitch_keycenter=a4 lovel=64 hivel=127
<group> sw_last=d1 sw_label=Pizz ampeg_release=0.05
<region> sample=pizz.wav lokey=a2 hikey=c6 pitch_keycenter=e4
<group> trigger=release
<region> sample=rel.wav lokey=a2 hikey=c6
`)
	return dir
}

func note(midi int) uint8 { return tkNote(midi) }

// Zones, velocity layers, round robin and keyswitched articulations.
func TestBuildMapsTheRegions(t *testing.T) {
	dir := fixture(t)
	f, err := ParseFile(filepath.Join(dir, "strings.sfz"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(f, Options{Name: "imported/strings", Source: "strings.sfz"})
	if err != nil {
		t.Fatal(err)
	}
	in := b.Instrument
	if in.Name != "Strings" || !strings.Contains(in.Description, "strings.sfz") {
		t.Errorf("name %q description %q", in.Name, in.Description)
	}
	if len(in.Zones) != 2 {
		t.Fatalf("zones: %+v", in.Zones)
	}
	lo, hi := in.Zones[0], in.Zones[1]
	if lo.Low != note(45) || lo.High != note(64) || hi.Low != note(65) || hi.High != note(84) {
		t.Errorf("zone ranges %s..%s, %s..%s", tracker.NoteName(lo.Low), tracker.NoteName(lo.High), tracker.NoteName(hi.Low), tracker.NoteName(hi.High))
	}
	if len(lo.Layers) != 2 || lo.Layers[0].VelHigh != 63 || lo.Layers[1].VelLow != 64 {
		t.Fatalf("low layers: %+v", lo.Layers)
	}
	if len(lo.Layers[0].RoundRobin) != 1 || len(lo.Layers[1].RoundRobin) != 0 {
		t.Errorf("round robin: %+v", lo.Layers)
	}
	name := func(i int) string { return in.Samples[i] }
	if got := name(lo.Layers[0].Sample) + " " + name(lo.Layers[0].RoundRobin[0]); got != "imported/strings/low_p1 imported/strings/low_p2" {
		t.Errorf("round-robin turns: %s", got)
	}
	// The pitch: a3 is where low_p1 plays as recorded.
	if s := in.Loaded(lo.Layers[0].Sample); s.BaseNote != note(57) {
		t.Errorf("base note %s", tracker.NoteName(s.BaseNote))
	}
	// One articulation: pizzicato, over the whole range, its release short.
	if len(in.Articulations) != 1 {
		t.Fatalf("articulations: %+v", in.Articulations)
	}
	pz := in.Articulations[0]
	if pz.Name != "pizzicato" || in.ArticulationIndex("pizz") != 1 || len(pz.Zones) != 1 || pz.FadeOut != 0.05 {
		t.Errorf("pizzicato: %+v", pz)
	}
	if in.FadeOut != 0.3 || in.VolEnv.On() {
		t.Errorf("release %v, env %+v", in.FadeOut, in.VolEnv)
	}
	// The keyswitch range is not the playable range.
	if in.Playback.Low != note(45) || in.Playback.High != note(84) || in.Playback.Clef != "treble" {
		t.Errorf("playback %+v", in.Playback)
	}
	joined := strings.Join(b.Warnings, "\n")
	if !strings.Contains(joined, "1 release-trigger") {
		t.Errorf("warnings:\n%s", joined)
	}

	// It plays: a note in each zone and the pizzicato, at the right pitch.
	for _, c := range []struct {
		midi int
		art  int
		vel  float32
		hz   float64
	}{{57, 0, 1, 220}, {57, 0, 0.3, 220}, {69, 0, 1, 440}, {64, 1, 1, 330}, {69, 1, 1, 440}} {
		got := pitchOf(t, in, c.midi, c.art, c.vel)
		if math.Abs(got-c.hz)/c.hz > 0.02 {
			t.Errorf("MIDI %d articulation %d: %.1f Hz, want %.0f", c.midi, c.art, got, c.hz)
		}
	}
}

// pitchOf renders a note and counts its rising zero crossings.
func pitchOf(t *testing.T, in *tracker.Instrument, midi, art int, vel float32) float64 {
	t.Helper()
	e := tracker.NewEngine(1)
	e.SetStrip(0, tracker.StripControls{Volume: 1})
	if e.NoteOn(0, in, float32(note(midi)), tracker.NoteOptions{Articulation: art, Velocity: vel}) == 0 {
		t.Fatalf("MIDI %d plays nothing", midi)
	}
	const frames = 9600
	buf := make([]float32, frames*2)
	e.Render(buf, frames, testRate)
	// Skip the first 10 ms (the attack), count over the rest.
	n := 0
	for i := 481; i < frames; i++ {
		if buf[(i-1)*2] < 0 && buf[i*2] >= 0 {
			n++
		}
	}
	return float64(n) / (float64(frames-481) / testRate)
}

// Loops (the file's own when the region gives none), offset and end,
// tune and transpose, and a shaped envelope.
func TestBuildLoopsTuningAndEnvelope(t *testing.T) {
	dir := fixture(t)
	writeFile(t, filepath.Join(dir, "pad.sfz"), `
<region> sample=samples/pad.wav lokey=48 pitch_keycenter=sample ampeg_attack=0.2 ampeg_decay=0.3 ampeg_sustain=60 ampeg_release=1.5
<region> sample=samples/low_f.wav key=40 offset=100 end=1099 loop_mode=loop_continuous loop_start=200 loop_end=599 tune=-30 transpose=12 volume=-6
`)
	f, err := ParseFile(filepath.Join(dir, "pad.sfz"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(f, Options{Name: "imported/pad"})
	if err != nil {
		t.Fatal(err)
	}
	in := b.Instrument
	var pad, cut *tracker.Sample
	for _, s := range b.Samples {
		switch {
		case strings.HasSuffix(s.Name, "/pad"):
			pad = s
		case strings.HasSuffix(s.Name, "/low_f"):
			cut = s
		}
	}
	if pad == nil || cut == nil {
		t.Fatalf("samples: %v", in.Samples)
	}
	if pad.Loop != tracker.LoopForward || pad.LoopStart != 4800 || pad.LoopEnd != 4800+18340 || pad.BaseNote != tracker.NoteC4 {
		t.Errorf("pad: loop %v %d..%d base %s", pad.Loop, pad.LoopStart, pad.LoopEnd, tracker.NoteName(pad.BaseNote))
	}
	if cut.Frames() != 1000 || cut.LoopStart != 100 || cut.LoopEnd != 500 || cut.FineTune != -30 || cut.BaseNote != note(28) {
		t.Errorf("cut: %d frames, loop %d..%d, tune %v, base %s", cut.Frames(), cut.LoopStart, cut.LoopEnd, cut.FineTune, tracker.NoteName(cut.BaseNote))
	}
	if math.Abs(float64(cut.Volume)-0.501) > 0.002 {
		t.Errorf("volume -6 dB as %v", cut.Volume)
	}
	env := in.VolEnv
	if !env.On() || env.Sustain < 0 {
		t.Fatalf("envelope %+v", env)
	}
	if p := env.Points[env.Sustain]; math.Abs(float64(p.T)-0.5) > 1e-6 || math.Abs(float64(p.V)-0.6) > 1e-6 {
		t.Errorf("sustain point %+v", p)
	}
	if last := env.Points[len(env.Points)-1]; last.V != 0 || math.Abs(float64(last.T)-2.0) > 1e-6 {
		t.Errorf("release ends at %+v", last)
	}
	if in.Playback.Low != note(40) || in.Playback.High != note(127) || in.Playback.Clef != "grand" {
		t.Errorf("range %+v", in.Playback)
	}
	// Over the whole keyboard a region says nothing about a range.
	f, _ = Parse("<region> sample=samples/pad.wav", filepath.Join(dir, "all.sfz"))
	if b, err := Build(f, Options{Name: "imported/all"}); err != nil || b.Instrument.Playback.Low != 0 || b.Instrument.Playback.High != 0 {
		t.Errorf("whole keyboard: %v %+v", err, b.Instrument.Playback)
	}
}

// Import writes the library, and the library instrument plays.
func TestImportWritesAPlayableInstrument(t *testing.T) {
	dir := fixture(t)
	res := t.TempDir()
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = res
	t.Cleanup(func() {
		fileutil.ResourcesPath = prev
		tracker.ReloadInstruments()
		tracker.ReloadSamples()
	})
	b, err := Import(filepath.Join(dir, "strings.sfz"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if b.Name != "imported/strings" {
		t.Errorf("name %q", b.Name)
	}
	if got := tracker.ScanInstrumentNames(); len(got) != 1 || got[0] != "imported/strings" {
		t.Fatalf("library: %v", got)
	}
	in, err := tracker.LoadInstrument("imported/strings")
	if err != nil {
		t.Fatal(err)
	}
	if in.Category != ImportedCategory || len(in.Samples) != 6 {
		t.Errorf("category %q, %d samples", in.Category, len(in.Samples))
	}
	for i := range in.Samples {
		if in.Loaded(i) == nil {
			t.Errorf("sample %s did not load", in.Samples[i])
		}
	}
	if hz := pitchOf(t, in, 69, 0, 1); math.Abs(hz-440) > 9 {
		t.Errorf("A4 from the library at %.1f Hz", hz)
	}
	if hz := pitchOf(t, in, 76, 1, 1); math.Abs(hz-660) > 13 {
		t.Errorf("pizz E5 from the library at %.1f Hz", hz)
	}
}

// A sample that will not load is a warning, not a failure; none loading
// is an error.
func TestMissingSamples(t *testing.T) {
	dir := fixture(t)
	writeFile(t, filepath.Join(dir, "half.sfz"), "<region> sample=samples/pizz.wav\n<region> sample=gone.wav lokey=80\n<region> sample=x.flac lokey=90")
	f, err := ParseFile(filepath.Join(dir, "half.sfz"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(f, Options{Name: "imported/half"})
	if err != nil {
		t.Fatal(err)
	}
	if w := strings.Join(b.Warnings, "\n"); !strings.Contains(w, "gone.wav: no such file") || !strings.Contains(w, "x.flac") {
		t.Errorf("warnings:\n%s", w)
	}
	f, _ = Parse("<region> sample=gone.wav", filepath.Join(dir, "none.sfz"))
	if _, err := Build(f, Options{Name: "imported/none"}); err == nil {
		t.Error("an instrument with no sample built")
	}
}

func TestArticulationNames(t *testing.T) {
	for label, want := range map[string]string{
		"Pizz": "pizzicato", "Spiccato": "staccato", "Trem.": "tremolo", "Legato": "legato",
		"Con sord.": "muted", "Arco": "sustain", "Flautando Soft": "flautando_soft",
	} {
		if got, _ := ArticulationName(label, 24); got != want {
			t.Errorf("%q: %q, want %q", label, got, want)
		}
	}
	if got, _ := ArticulationName("", 24); got != "keyswitch C1" {
		t.Errorf("no label: %q", got)
	}
}
