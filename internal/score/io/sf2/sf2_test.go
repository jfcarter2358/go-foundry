package sf2

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

const testRate = 32000

// bank builds a small SoundFont in memory, the way a sound designer's tool
// would write one.
type bank struct {
	name    string
	data    []int16
	samples []sampleHdr
	insts   []testInst
	presets []testPreset
}

type testGen struct {
	oper   uint16
	amount uint16
}

func g(oper uint16, v int) testGen        { return testGen{oper, uint16(int16(v))} }
func rng(oper uint16, lo, hi int) testGen { return testGen{oper, uint16(lo) | uint16(hi)<<8} }

type testInst struct {
	name  string
	zones [][]testGen // the first may be global (no sampleID)
}

type testPreset struct {
	name          string
	bank, program int
	zones         [][]testGen
}

// addSine appends a sine sample (with the 46 zero points the format
// wants after each) and returns its index.
func (b *bank) addSine(name string, hz float64, frames int, root int, loop [2]int, kind, link uint16) int {
	start := len(b.data)
	for i := 0; i < frames; i++ {
		b.data = append(b.data, int16(12000*math.Sin(2*math.Pi*hz*float64(i)/testRate)))
	}
	end := len(b.data)
	b.data = append(b.data, make([]int16, 46)...)
	b.samples = append(b.samples, sampleHdr{name: name, start: uint32(start), end: uint32(end),
		startLoop: uint32(start + loop[0]), endLoop: uint32(start + loop[1]), rate: testRate,
		pitch: uint8(root), link: link, kind: kind})
	return len(b.samples) - 1
}

func name20(s string) []byte {
	b := make([]byte, 20)
	copy(b, s)
	return b
}

func chunk(id string, data []byte) []byte {
	var out bytes.Buffer
	out.WriteString(id)
	binary.Write(&out, binary.LittleEndian, uint32(len(data)))
	out.Write(data)
	if len(data)%2 == 1 {
		out.WriteByte(0)
	}
	return out.Bytes()
}

func list(kind string, parts ...[]byte) []byte {
	return chunk("LIST", append([]byte(kind), bytes.Join(parts, nil)...))
}

// bytes writes the bank as an .sf2.
func (b *bank) bytes() []byte {
	le := binary.LittleEndian
	var smpl bytes.Buffer
	binary.Write(&smpl, le, b.data)
	info := list("INFO", chunk("ifil", []byte{2, 0, 4, 0}), chunk("INAM", append([]byte(b.name), 0)))
	sdta := list("sdta", chunk("smpl", smpl.Bytes()))

	var phdr, pbag, pgen, inst, ibag, igen, shdr bytes.Buffer
	gens := func(bagBuf, genBuf *bytes.Buffer, zones [][]testGen, ngen *int) {
		for _, z := range zones {
			binary.Write(bagBuf, le, [2]uint16{uint16(*ngen), 0})
			for _, gg := range z {
				binary.Write(genBuf, le, [2]uint16{gg.oper, gg.amount})
				*ngen++
			}
		}
	}
	npg, nbag := 0, 0
	for _, p := range b.presets {
		phdr.Write(name20(p.name))
		binary.Write(&phdr, le, [3]uint16{uint16(p.program), uint16(p.bank), uint16(nbag)})
		binary.Write(&phdr, le, [3]uint32{})
		gens(&pbag, &pgen, p.zones, &npg)
		nbag += len(p.zones)
	}
	phdr.Write(name20("EOP"))
	binary.Write(&phdr, le, [3]uint16{0, 0, uint16(nbag)})
	binary.Write(&phdr, le, [3]uint32{})
	binary.Write(&pbag, le, [2]uint16{uint16(npg), 0})
	binary.Write(&pgen, le, [2]uint16{0, 0})

	nig, nibag := 0, 0
	for _, in := range b.insts {
		inst.Write(name20(in.name))
		binary.Write(&inst, le, uint16(nibag))
		gens(&ibag, &igen, in.zones, &nig)
		nibag += len(in.zones)
	}
	inst.Write(name20("EOI"))
	binary.Write(&inst, le, uint16(nibag))
	binary.Write(&ibag, le, [2]uint16{uint16(nig), 0})
	binary.Write(&igen, le, [2]uint16{0, 0})

	for _, s := range append(b.samples, sampleHdr{name: "EOS"}) {
		shdr.Write(name20(s.name))
		binary.Write(&shdr, le, [5]uint32{s.start, s.end, s.startLoop, s.endLoop, s.rate})
		shdr.Write([]byte{s.pitch, byte(s.correction)})
		binary.Write(&shdr, le, [2]uint16{s.link, s.kind})
	}
	mod := make([]byte, 10)
	pdta := list("pdta", chunk("phdr", phdr.Bytes()), chunk("pbag", pbag.Bytes()), chunk("pmod", mod),
		chunk("pgen", pgen.Bytes()), chunk("inst", inst.Bytes()), chunk("ibag", ibag.Bytes()), chunk("imod", mod),
		chunk("igen", igen.Bytes()), chunk("shdr", shdr.Bytes()))
	body := append([]byte("sfbk"), append(append(info, sdta...), pdta...)...)
	return chunk("RIFF", body)
}

// testBank is a piano split at E4 with a velocity layer and a looped high
// sample, its release in a global zone, and a stereo pad from a linked
// left/right pair.
func testBank() *bank {
	b := &bank{name: "Test Bank"}
	low := b.addSine("Piano A3", 220, testRate/2, 57, [2]int{0, 0}, monoSample, 0)
	lowSoft := b.addSine("Piano A3 soft", 220, testRate/2, 57, [2]int{0, 0}, monoSample, 0)
	high := b.addSine("Piano A4", 440, testRate/2, 69, [2]int{1000, 1000 + 727*4}, monoSample, 0)
	left := b.addSine("Pad L", 330, testRate/2, 64, [2]int{0, 0}, leftSample, 4)
	right := b.addSine("Pad R", 330, testRate/2, 64, [2]int{0, 0}, rightSample, 3)
	_ = right
	b.insts = []testInst{
		{name: "Piano", zones: [][]testGen{
			{g(genReleaseVolEnv, int(1200*math.Log2(0.5)))}, // global: 0.5 s release
			{rng(genKeyRange, 0, 64), rng(genVelRange, 0, 63), g(genSampleID, lowSoft)},
			{rng(genKeyRange, 0, 64), rng(genVelRange, 64, 127), g(genAttenuation, 60), g(genSampleID, low)},
			// A filter at 440 Hz the modulation envelope opens three
			// octaves, decaying over 28 s to nothing.
			{rng(genKeyRange, 65, 127), g(genSampleModes, 1), g(genFineTune, -20), g(genFilterFc, 6900), g(genModEnvToFilterFc, 3600),
				g(genDecayModEnv, int(math.Round(1200*math.Log2(28)))), g(genSustainModEnv, 1000), g(genSampleID, high)},
		}},
		{name: "Pad", zones: [][]testGen{
			{rng(genKeyRange, 0, 127), g(genPan, -500), g(genAttackVolEnv, int(1200*math.Log2(0.25))), g(genSustainVolEnv, 60), g(genDecayVolEnv, 0), g(genSampleID, left)},
			{rng(genKeyRange, 0, 127), g(genPan, 500), g(genAttackVolEnv, int(1200*math.Log2(0.25))), g(genSustainVolEnv, 60), g(genDecayVolEnv, 0), g(genSampleID, left+1)},
		}},
	}
	b.presets = []testPreset{
		{name: "Grand Piano", program: 0, zones: [][]testGen{{g(genCoarseTune, 0), g(genInstrument, 0)}}},
		// The preset narrows the pad to C3..C6 and adds 2 dB of attenuation.
		{name: "Warm Pad", program: 89, zones: [][]testGen{{rng(genKeyRange, 48, 84), g(genAttenuation, 20), g(genInstrument, 1)}}},
	}
	return b
}

func TestParseAndRegions(t *testing.T) {
	f, err := Parse(testBank().bytes())
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "Test Bank" {
		t.Errorf("name %q", f.Name)
	}
	ps := f.Presets()
	if len(ps) != 2 || ps[0].Name != "Grand Piano" || ps[1].Program != 89 {
		t.Fatalf("presets %+v", ps)
	}
	rs, _, err := f.Regions(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 3 {
		t.Fatalf("%d piano regions", len(rs))
	}
	soft, loud, high := rs[0], rs[1], rs[2]
	if soft.HiKey != 64 || soft.HiVel != 63 || soft.Key != 57 || soft.Env.Release < 0.49 || soft.Env.Release > 0.51 {
		t.Errorf("soft: %+v", soft)
	}
	// initialAttenuation plays at the EMU's 0.4: 6 dB written, 2.4 heard.
	if loud.LoVel != 64 || math.Abs(loud.Volume+2.4) > 1e-9 {
		t.Errorf("loud: %+v", loud)
	}
	// Its filter sits where the envelope holds it over the first second.
	if want := 8.176 * math.Exp2((6900+3600*(1-0.5/28))/1200); high.Filter != "lpf_2p" || math.Abs(high.Cutoff/want-1) > 1e-3 {
		t.Errorf("high's filter: %s at %g Hz, want %g", high.Filter, high.Cutoff, want)
	}
	if high.LoKey != 65 || high.Tune != -20 || high.LoopStart != 1000 || high.LoopEnd != 1000+727*4-1 || high.Audio.Frames() != testRate/2 {
		t.Errorf("high: %+v", high)
	}
	// The pad: one stereo region, narrowed by the preset, attenuated by both.
	rs, _, err = f.Regions(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 {
		t.Fatalf("%d pad regions", len(rs))
	}
	pad := rs[0]
	if pad.Audio.Channels != 2 || pad.LoKey != 48 || pad.HiKey != 84 || math.Abs(pad.Volume+0.8) > 1e-9 || pad.Pan != 0 {
		t.Errorf("pad: %d channels, keys %d..%d, volume %v, pan %v", pad.Audio.Channels, pad.LoKey, pad.HiKey, pad.Volume, pad.Pan)
	}
	if math.Abs(pad.Env.Attack-0.25) > 1e-3 || math.Abs(pad.Env.Sustain-math.Pow(10, -0.3)) > 1e-6 {
		t.Errorf("pad envelope %+v", pad.Env)
	}

	if _, err := Parse([]byte("RIFF\x04\x00\x00\x00WAVE")); err == nil {
		t.Error("a WAV parsed as a SoundFont")
	}
}

func TestImportWritesEveryPreset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Test Bank.sf2")
	if err := os.WriteFile(path, testBank().bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	res := t.TempDir()
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = res
	t.Cleanup(func() {
		fileutil.ResourcesPath = prev
		tracker.ReloadInstruments()
		tracker.ReloadSamples()
	})
	built, err := Import(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(built) != 2 {
		t.Fatalf("%d instruments", len(built))
	}
	names := tracker.ScanInstrumentNames()
	if strings.Join(names, " ") != "imported/test_bank/grand_piano imported/test_bank/warm_pad" {
		t.Fatalf("library: %v", names)
	}
	piano, err := tracker.LoadInstrument("imported/test_bank/grand_piano")
	if err != nil {
		t.Fatal(err)
	}
	if piano.Name != "Grand Piano" || piano.Category != "imported" || len(piano.Zones) != 2 || len(piano.Zones[0].Layers) != 2 {
		t.Errorf("piano: %q %q %+v", piano.Name, piano.Category, piano.Zones)
	}
	if piano.FadeOut < 0.49 || piano.FadeOut > 0.51 {
		t.Errorf("release %v", piano.FadeOut)
	}
	hi := piano.Loaded(piano.Zones[1].Sample)
	if hi == nil || hi.Loop != tracker.LoopForward || hi.LoopStart != 1000 || hi.LoopEnd != 1000+727*4 || hi.FineTune != -20 {
		t.Errorf("high sample %+v", hi)
	}
	for _, c := range []struct {
		midi int
		hz   float64
	}{{57, 220}, {69, 440 * math.Exp2(-20.0/1200)}, {76, 659.26 * math.Exp2(-20.0/1200)}} {
		if got := pitchOf(t, piano, c.midi); math.Abs(got-c.hz)/c.hz > 0.02 {
			t.Errorf("piano MIDI %d at %.1f Hz, want %.1f", c.midi, got, c.hz)
		}
	}
	pad, err := tracker.LoadInstrument("imported/test_bank/warm_pad")
	if err != nil {
		t.Fatal(err)
	}
	if s := pad.Loaded(0); s == nil || s.Channels != 2 {
		t.Fatalf("pad sample %+v", s)
	}
	if !pad.VolEnv.On() || pad.Playback.Low != 37 || pad.Playback.High != 73 {
		t.Errorf("pad envelope %+v, range %+v", pad.VolEnv, pad.Playback)
	}
	// One preset of several still goes into the bank's folder.
	built, err = Import(path, Options{Presets: []int{0}, Name: "imported/tb"})
	if err != nil || len(built) != 1 || built[0].Name != "imported/tb/grand_piano" {
		t.Fatalf("one preset: %v %+v", err, built)
	}
}

// pitchOf renders a note at full velocity and counts rising zero
// crossings past the attack.
func pitchOf(t *testing.T, in *tracker.Instrument, midi int) float64 {
	t.Helper()
	e := tracker.NewEngine(1)
	e.SetStrip(0, tracker.StripControls{Volume: 1})
	if e.NoteOn(0, in, float32(midi-60+int(tracker.NoteC4)), tracker.NoteOptions{Velocity: 1}) == 0 {
		t.Fatalf("MIDI %d plays nothing", midi)
	}
	const frames, rate = 9600, 48000
	buf := make([]float32, frames*2)
	e.Render(buf, frames, rate)
	n := 0
	for i := 481; i < frames; i++ {
		if buf[(i-1)*2] < 0 && buf[i*2] >= 0 {
			n++
		}
	}
	return float64(n) / (float64(frames-481) / rate)
}
