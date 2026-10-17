package sfz

import (
	"fmt"
	"math"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/tracker"
)

// TestRandomFilesDoNotPanic builds thousands of SFZ files from the
// opcodes the parser reads, each given a sensible, an extreme or a
// nonsense value, under headers in any order (with #define, #include of
// itself, comments left open), and asks only that each is read or refused
// with an error — never a panic in the editor's process — and that what
// is built keeps its zones inside the keyboard.
func TestRandomFilesDoNotPanic(t *testing.T) {
	dir := fixture(t)
	ops := []string{"sample", "lokey", "hikey", "key", "lovel", "hivel", "pitch_keycenter", "transpose", "tune",
		"volume", "amplitude", "pan", "offset", "end", "loop_mode", "loop_start", "loop_end", "loopstart", "loopend",
		"ampeg_attack", "ampeg_hold", "ampeg_decay", "ampeg_sustain", "ampeg_release", "ampeg_delay",
		"seq_length", "seq_position", "lorand", "hirand", "sw_last", "sw_down", "sw_label", "sw_default", "sw_lokey", "sw_hikey",
		"trigger", "fil_type", "cutoff", "resonance", "octave_offset", "note_offset", "default_path", "bogus_op"}
	vals := []string{"0", "1", "-1", "127", "128", "-400", "99999999999", "1e308", "nan", "inf", "c4", "c#-2", "zz", "",
		"0.5", "-0.5", "low_p1.wav", "pad.wav", "missing.wav", "../samples/pad.wav", "loop_continuous", "one_shot",
		"no_loop", "release", "lpf_2p", "hpf_1p", "=sample", "$X", "60.7", "g9", "a-1"}
	heads := []string{"<control>", "<global>", "<master>", "<group>", "<region>", "<region>", "<region>", "<curve>", "<effect>", "<bogus>"}
	r := rand.New(rand.NewSource(5))
	built := 0
	for i := 0; i < 3000; i++ {
		var b strings.Builder
		b.WriteString("<control> default_path=samples/\n")
		if r.Intn(4) == 0 {
			b.WriteString("#define $X 64\n")
		}
		if r.Intn(20) == 0 {
			b.WriteString("#include \"self.sfz\"\n")
		}
		for h := 0; h < 1+r.Intn(8); h++ {
			b.WriteString(heads[r.Intn(len(heads))])
			if r.Intn(3) > 0 {
				b.WriteString(" sample=" + vals[16+r.Intn(4)])
			}
			for k := 0; k < r.Intn(8); k++ {
				v := vals[r.Intn(len(vals))]
				if r.Intn(10) < 7 {
					v = []string{"0", "1", "60", "100", "c4", "e5", "0.5"}[r.Intn(7)]
				}
				fmt.Fprintf(&b, " %s=%s", ops[r.Intn(len(ops))], v)
			}
			if r.Intn(30) == 0 {
				b.WriteString(" /* never closed")
			}
			if r.Intn(30) == 0 {
				b.WriteString(" =oops ==")
			}
			b.WriteString("\n")
		}
		text := b.String()
		path := filepath.Join(dir, "self.sfz")
		writeFile(t, path, text)
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("panic on\n%s\n%v", text, p)
				}
			}()
			f, err := Parse(text, path)
			if err != nil {
				return
			}
			bt, err := Build(f, Options{Name: "imported/x"})
			if err != nil {
				return
			}
			built++
			zs := bt.Instrument.Zones
			for _, a := range bt.Instrument.Articulations {
				zs = append(zs, a.Zones...)
			}
			for _, z := range zs {
				if z.Low > z.High || z.High > 127 || z.Sample < 0 || z.Sample >= len(bt.Instrument.Samples) && len(bt.Instrument.Samples) > 0 {
					t.Fatalf("zone %+v (%d samples) from\n%s", z, len(bt.Instrument.Samples), text)
				}
			}
		}()
	}
	if built < 300 {
		t.Errorf("only %d of the files built: the test is not reaching the builder", built)
	}
}

// An instrument name is a library name: one that would climb out of the
// library is refused before anything is written.
func TestNamesStayInTheLibrary(t *testing.T) {
	dir := fixture(t)
	f, err := ParseFile(filepath.Join(dir, "strings.sfz"))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../evil", "imported/../../evil", "imported//x", "./x", `imported\x`, "c:/x"} {
		if _, err := Build(f, Options{Name: bad}); err == nil {
			t.Errorf("%q was taken as a name", bad)
		}
	}
	if _, err := Build(f, Options{Name: "imported/strings"}); err != nil {
		t.Errorf("a good name: %v", err)
	}
}

// Regions whose cutoffs are a little apart share one filter at their
// geometric mean; far apart, the instrument plays unfiltered, and either
// way the import says so.
func TestNearlyEqualFiltersAreOne(t *testing.T) {
	dir := fixture(t)
	build := func(c1, c2 string) *Built {
		t.Helper()
		f, err := Parse("<region> sample=samples/low_f.wav hikey=59 fil_type=lpf_2p cutoff="+c1+
			"\n<region> sample=samples/high_f.wav lokey=60 fil_type=lpf_2p cutoff="+c2, filepath.Join(dir, "f.sfz"))
		if err != nil {
			t.Fatal(err)
		}
		b, err := Build(f, Options{Name: "imported/f"})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	b := build("1000", "1200")
	if want := math.Sqrt(1000 * 1200); math.Abs(float64(b.Instrument.Cutoff)-want) > 0.5 || b.Instrument.Filter != tracker.FilterLowPass {
		t.Errorf("cutoffs 1000 and 1200: %v at %g Hz", b.Instrument.Filter, b.Instrument.Cutoff)
	}
	if !strings.Contains(strings.Join(b.Warnings, "\n"), "1000..1200 Hz") {
		t.Errorf("warnings %q", b.Warnings)
	}
	b = build("1000", "3000")
	if b.Instrument.Cutoff < 19999 || !strings.Contains(strings.Join(b.Warnings, "\n"), "filter differently") {
		t.Errorf("cutoffs 1000 and 3000: %g Hz, warnings %q", b.Instrument.Cutoff, b.Warnings)
	}
	b = build("1500", "1500")
	if b.Instrument.Cutoff != 1500 || strings.Contains(strings.Join(b.Warnings, "\n"), "Hz") {
		t.Errorf("one cutoff: %g Hz, warnings %q", b.Instrument.Cutoff, b.Warnings)
	}
}
