package synth

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"foundry/internal/tracker"
)

// Every sample a starter instrument names is one gen-samples or
// gen-instruments writes: nothing in the library can ship silent.
func TestStarterInstrumentsNameGeneratedSamples(t *testing.T) {
	if bad := checkStarter(); len(bad) > 0 {
		t.Fatal(strings.Join(bad, "\n"))
	}
	all := StarterInstruments()
	if len(all) != len(Recipes)+len(orchestralInstruments()) {
		t.Fatalf("%d instruments", len(all))
	}
	seen := map[string]bool{}
	for _, si := range all {
		if seen[si.Name] {
			t.Errorf("two instruments are called %s", si.Name)
		}
		seen[si.Name] = true
	}
	for _, want := range []string{
		"strings/violin_section", "strings/viola_section", "strings/cello_section", "strings/contrabass_section",
		"woodwinds/flute", "woodwinds/oboe", "woodwinds/clarinet", "woodwinds/bassoon",
		"brass/horn", "brass/trumpet", "brass/trombone", "brass/tuba",
		"keys/grand_piano", "keys/celesta", "keys/harp", "keys/organ", "choir/choir",
		"percussion/timpani", "percussion/orchestral_percussion", "percussion/drum_kit",
		"synth/warm_pad", "synth/supersaw_lead", "drums/kick_808", "fx/fx_vinyl",
	} {
		if !seen[want] {
			t.Errorf("the starter library has no %s", want)
		}
	}
}

// The string sections carry the four articulations a score asks for, and
// the transposing instruments say how they transpose.
func TestOrchestralDefaults(t *testing.T) {
	by := map[string]*tracker.Instrument{}
	for _, si := range StarterInstruments() {
		by[si.Name] = si.Inst
	}
	for _, name := range []string{"strings/violin_section", "strings/viola_section", "strings/cello_section", "strings/contrabass_section"} {
		in := by[name]
		for _, a := range []string{"staccato", "pizzicato", "tremolo", "muted", "pizz.", "con sord."} {
			if in.ArticulationIndex(a) == 0 {
				t.Errorf("%s has no %s", name, a)
			}
		}
		if len(in.Zones) != 2 || len(in.Zones[0].Layers) != 2 {
			t.Errorf("%s should be two registers of two velocity layers: %+v", name, in.Zones)
		}
	}
	for name, want := range map[string]int{"woodwinds/clarinet": -2, "brass/trumpet": -2, "brass/horn": -7, "strings/contrabass_section": -12, "keys/celesta": 12, "percussion/glockenspiel": 24, "woodwinds/flute": 0} {
		if got := by[name].Playback.Transposition; got != want {
			t.Errorf("%s transposes %d, want %d", name, got, want)
		}
	}
	for name, want := range map[string]string{"strings/viola_section": "alto", "strings/cello_section": "bass", "keys/grand_piano": "grand", "percussion/drum_kit": "percussion", "plucked/guitar_nylon": "treble_8vb"} {
		if got := by[name].Playback.Clef; got != want {
			t.Errorf("%s clef %q, want %q", name, got, want)
		}
	}
	if by["keys/harp"].Playback.Gliss != tracker.GlissDiatonic || by["brass/trombone"].Playback.Gliss != tracker.GlissSlide {
		t.Error("harp glissandi run the scale; trombone glissandi slide")
	}
	if z := by["percussion/drum_kit"].Zones[3]; z.Low != z.High || len(z.RoundRobin) != 2 {
		t.Errorf("the kit's snare should be one key in round robin: %+v", z)
	}
}

func TestStarterInstrumentsRoundTrip(t *testing.T) {
	for _, si := range StarterInstruments() {
		b, err := tracker.MarshalInstrument(si.Inst)
		if err != nil {
			t.Fatal(si.Name, err)
		}
		back, err := tracker.ParseInstrument(b)
		if err != nil {
			t.Fatalf("%s: %v\n%s", si.Name, err, b)
		}
		b2, _ := tracker.MarshalInstrument(back)
		if !bytes.Equal(b, b2) {
			t.Fatalf("%s is not stable:\n%s\n---\n%s", si.Name, b, b2)
		}
	}
}

func TestVariantRendersAtItsBase(t *testing.T) {
	v := VariantByName("orchestral/vln_sus_loud_d5")
	if v == nil {
		t.Fatal("no variant")
	}
	s := v.recipe().Generate(Rate, nil)
	if s.Name != "orchestral/vln_sus_loud_d5" || tracker.NoteName(s.BaseNote) != "D-5" || !s.Looped() || s.Frames() == 0 {
		t.Fatalf("variant sample: %s base %s looped %v frames %d", s.Name, tracker.NoteName(s.BaseNote), s.Looped(), s.Frames())
	}
	// Two takes of one recipe with the same knobs differ: round robin.
	a := VariantByName("snare_acoustic_rr2").recipe().Generate(Rate, nil)
	b := ByName("snare_acoustic").Generate(Rate, nil)
	if tracker.Hash(a.Data) == tracker.Hash(b.Data) {
		t.Fatal("a round-robin take is identical to the original")
	}
	if tracker.Hash(a.Data) != tracker.Hash(VariantByName("snare_acoustic_rr2").recipe().Generate(Rate, nil).Data) {
		t.Fatal("a variant does not regenerate identically")
	}
}

func TestInstrumentsMainWritesAndLoads(t *testing.T) {
	dir, smp := t.TempDir(), t.TempDir()
	var out bytes.Buffer
	if code := InstrumentsMain([]string{"-out", dir, "-samples", smp, "-only", "strings/violin_section,kick_808"}, &out); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	in, err := tracker.LoadInstrumentFile(filepath.Join(dir, "strings", "violin_section.finst"))
	if err != nil {
		t.Fatal(err)
	}
	want := orchestralInstruments()[0].Inst
	want = want.Clone()
	in.Category, want.Category = "", ""
	if !reflect.DeepEqual(in.Articulations, want.Articulations) || !reflect.DeepEqual(in.Zones, want.Zones) {
		t.Fatal("the written violin section reads back differently")
	}
	if _, err := os.Stat(filepath.Join(dir, "drums", "kick_808.finst")); err != nil {
		t.Fatal(err)
	}
	for _, s := range in.Samples {
		if v := VariantByName(s); v != nil {
			if _, err := os.Stat(filepath.Join(smp, filepath.FromSlash(s)+".wav")); err != nil {
				t.Errorf("variant %s was not rendered: %v", s, err)
			}
		}
	}
	out.Reset()
	if code := InstrumentsMain([]string{"-list"}, &out); code != 0 || strings.Count(out.String(), "\n") != len(StarterInstruments()) {
		t.Fatalf("-list printed %d lines", strings.Count(out.String(), "\n"))
	}
	if code := InstrumentsMain([]string{"-out", dir, "-no-samples", "-only", "nope"}, &out); code == 0 {
		t.Error("an unknown instrument should fail")
	}
}
