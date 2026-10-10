package synth

// instruments.go — the starter instrument library, and `gen-instruments`.
//
// Two sets, both built from the recipes. One .finst per recipe, in the
// recipe's own folder, so anything the sample library can play a song or a
// score can name as an instrument with sensible defaults: a range and clef
// for its kind, a release that suits it, a gliss style. And an
// orchestral-ish set — string sections with sustain, staccato, pizzicato,
// tremolo and muted articulations, woodwinds, brass, keys, choir, timpani,
// orchestral percussion, a drum kit and a few synths — built from the
// recipes and the variants (variants.go) in instruments/strings/,
// woodwinds/, brass/, keys/, choir/, percussion/ and synth/.
//
// Like the samples, the library is checked in and regenerates
// identically; this runs when an instrument here changes.

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/tracker"
)

// StarterInstrument is one library instrument and the name it is saved as.
type StarterInstrument struct {
	Name string // "strings/violin_section"
	Inst *tracker.Instrument
}

// StarterInstruments is the whole starter library, sorted by name.
func StarterInstruments() []StarterInstrument {
	var out []StarterInstrument
	for i := range Recipes {
		r := &Recipes[i]
		out = append(out, StarterInstrument{Name: r.LibraryName(), Inst: recipeInstrument(r)})
	}
	out = append(out, orchestralInstruments()...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// note reads a note name the tables below are written in; a typo is a
// programming error.
func note(s string) uint8 {
	n, err := tracker.ParseNote(s)
	if err != nil {
		panic("synth: " + err.Error())
	}
	return n
}

// ---------- one per recipe ----------

// recipeInstrument wraps a recipe's sample with what its kind of sound wants.
func recipeInstrument(r *Recipe) *tracker.Instrument {
	in := tracker.NewInstrument(title(r.Name), r.LibraryName())
	in.Description = r.Desc
	pb := &in.Playback
	pb.Clef = "treble"
	switch r.Category {
	case "drums":
		// One-shots end by themselves: in a score they ring past a short
		// written note rather than being choked by its end.
		pb.Clef, pb.Release = "percussion", 3
		in.Polyphony = 4
		switch r.Name {
		case "timpani", "tom_808", "tom_mid", "tom_floor", "taiko":
			pb.Clef = "bass"
		}
	case "bass":
		pb.Clef, pb.Low, pb.High = "bass", note("C-1"), note("G-4")
		in.FadeOut = 0.08
	case "leads":
		pb.Low, pb.High = note("C-3"), note("C-7")
		in.FadeOut = 0.12
		in.VibRate, in.VibDepth, in.VibDelay = 5.5, 0.12, 0.35
	case "pads":
		pb.Low, pb.High = note("C-2"), note("C-7")
		in.FadeOut = 0.9
		switch r.Name {
		case "cello":
			pb.Clef, pb.Low, pb.High = "bass", note("C-2"), note("A-5")
			in.FadeOut = 0.3
		case "strings_pizz":
			pb.Low, pb.High = note("G-3"), note("E-7")
			in.FadeOut, pb.Release = 0.3, 0.6
		case "drone_scifi", "pad_dark":
			pb.Clef, pb.Low, pb.High = "bass", note("C-1"), note("C-5")
		}
	case "keys":
		pb.Clef, pb.Low, pb.High = "grand", note("A-0"), note("C-8")
		in.FadeOut, in.Polyphony = 0.3, 4
		switch r.Name {
		case "organ_drawbar", "organ_pipe", "organ_combo":
			in.FadeOut = 0.12
		case "marimba":
			pb.Clef, pb.Low, pb.High = "grand", note("C-2"), note("C-7")
		case "vibraphone":
			pb.Clef, pb.Low, pb.High = "treble", note("F-3"), note("F-6")
		case "glockenspiel":
			// Written two octaves below where it sounds.
			pb.Clef, pb.Low, pb.High, pb.Transposition = "treble", note("G-5"), note("C-8"), 24
		case "music_box":
			pb.Clef, pb.Low, pb.High = "treble", note("C-4"), note("C-8")
		}
	case "plucked":
		pb.Low, pb.High = note("E-2"), note("E-6")
		in.FadeOut, in.Polyphony = 0.25, 4
		switch r.Name {
		case "guitar_nylon", "guitar_steel", "guitar_muted", "guitar_power":
			pb.Clef = "treble_8vb"
		case "harp":
			pb.Clef, pb.Low, pb.High, pb.Gliss = "grand", note("B-0"), note("G#7"), tracker.GlissDiatonic
			in.FadeOut = 1.5
		}
	case "winds":
		in.FadeOut = 0.12
		pb.Low, pb.High = note("C-3"), note("C-7")
		switch r.Name {
		case "trombone", "french_horn":
			pb.Clef, pb.Low, pb.High = "bass", note("E-2"), note("F-5")
		}
		if r.Name == "trombone" || r.Name == "brass_section" {
			pb.Gliss = tracker.GlissSlide
		} else {
			pb.Gliss = tracker.GlissChromatic
		}
	case "chip":
		pb.Low, pb.High = note("C-2"), note("C-8")
		in.FadeOut = 0.02
		in.Interp = tracker.InterpNearest
		if r.Name == "chip_noise" || r.Name == "chip_noise_metal" {
			pb.Clef = "percussion"
		}
	case "vocal":
		pb.Low, pb.High = note("C-3"), note("C-6")
		in.FadeOut = 0.4
		if r.Name == "vox_chant" {
			pb.Clef, pb.Low, pb.High = "bass", note("C-1"), note("C-4")
		}
	case "fx":
		pb.Clef, pb.Release = "percussion", 6
		in.FadeOut = 0.3
	}
	return in
}

// title makes a display name: "lead_supersaw" is "Lead Supersaw".
func title(s string) string {
	words := strings.Split(s, "_")
	for i, w := range words {
		if w != "" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// ---------- the orchestral set ----------

// builder assembles an instrument whose samples are named, not numbered.
type builder struct {
	in *tracker.Instrument
}

func newBuilder(name, desc string) *builder {
	in := tracker.NewInstrument(name, "")
	in.Description = desc
	return &builder{in: in}
}

// s is a sample's slot, added the first time it is named.
func (b *builder) s(sample string) int {
	for i, n := range b.in.Samples {
		if n == sample {
			return i
		}
	}
	b.in.Samples = append(b.in.Samples, sample)
	return len(b.in.Samples) - 1
}

// zone is a note range playing one sample, with round-robin alternates.
func (b *builder) zone(lo, hi, sample string, rr ...string) tracker.Zone {
	z := tracker.Zone{Sample: b.s(sample), Low: note(lo), High: note(hi)}
	for _, r := range rr {
		z.RoundRobin = append(z.RoundRobin, b.s(r))
	}
	return z
}

// hit is a single-key zone that plays its sample at the sample's own pitch
// — a drum map entry — or, with base, pitched relative to it.
func (b *builder) hit(key, sample, base string, rr ...string) tracker.Zone {
	z := b.zone(key, key, sample, rr...)
	if base == "" {
		base = key
	}
	z.BaseNote = note(base)
	return z
}

// layered is a note range with a soft and a loud velocity layer that
// crossfade between velocities 64 and 96.
func (b *builder) layered(lo, hi, soft, loud string) tracker.Zone {
	return tracker.Zone{Sample: b.s(loud), Low: note(lo), High: note(hi), Layers: []tracker.Layer{
		{Sample: b.s(soft), VelLow: 0, VelHigh: 96},
		{Sample: b.s(loud), VelLow: 64, VelHigh: 127},
	}}
}

func (b *builder) playback(lo, hi, clef string, transposition int, gliss tracker.GlissStyle) {
	b.in.Playback = tracker.Playback{Low: note(lo), High: note(hi), Clef: clef, Transposition: transposition, Gliss: gliss}
}

func (b *builder) art(a tracker.Articulation) {
	if !a.VolEnv.On() && a.VolEnv.Sustain == 0 && a.VolEnv.LoopStart == 0 {
		a.VolEnv = tracker.EnvelopeOff()
	}
	b.in.Articulations = append(b.in.Articulations, a)
}

// env is an envelope from (time, value) pairs, with a sustain point and a
// loop (point indices; -1 off).
func env(sustain, loopStart, loopEnd int, pts ...float32) tracker.Envelope {
	e := tracker.Envelope{Sustain: sustain, LoopStart: loopStart, LoopEnd: loopEnd}
	for i := 0; i+1 < len(pts); i += 2 {
		e.Points = append(e.Points, tracker.EnvPoint{T: pts[i], V: pts[i+1]})
	}
	return e
}

// tremoloEnv is a fast unmeasured tremolo: the level dips about 15 times a
// second while the note is held, and fades out once it is released.
func tremoloEnv() tracker.Envelope {
	return env(-1, 1, 3, 0, 0, 0.012, 1, 0.042, 0.38, 0.072, 1, 0.3, 0)
}

const orch = VariantCategory + "/"

// stringRegister is the samples one register of a string section plays.
type stringRegister struct {
	soft, loud, short, pizz string
}

var (
	upperLow  = stringRegister{orch + "vln_sus_soft_g3", orch + "vln_sus_loud_g3", orch + "vln_short_g3", "pads/strings_pizz"}
	upperHigh = stringRegister{orch + "vln_sus_soft_d5", orch + "vln_sus_loud_d5", orch + "vln_short_d5", "pads/strings_pizz"}
	lowerLow  = stringRegister{orch + "vc_sus_soft_c2", orch + "vc_sus_loud_c2", orch + "vc_short_c2", orch + "pizz_c2"}
	lowerHigh = stringRegister{orch + "vc_sus_soft_g3", orch + "vc_sus_loud_g3", orch + "vc_short_g3", "pads/strings_pizz"}
)

// stringSection is a section in two registers split at split, with the
// four articulations every section has.
func stringSection(name, desc, split string, low, high stringRegister) *builder {
	b := newBuilder(name, desc)
	below := tracker.NoteName(note(split) - 1)
	b.in.Zones = []tracker.Zone{
		b.layered("C-0", below, low.soft, low.loud),
		b.layered(split, "B-9", high.soft, high.loud),
	}
	b.in.FadeOut = 0.28
	b.in.Polyphony = 2
	b.art(tracker.Articulation{Name: "staccato", Triggers: []string{"stacc.", "spiccato", "spicc.", "staccatissimo"},
		Zones: []tracker.Zone{b.zone("C-0", below, low.short), b.zone(split, "B-9", high.short)}, FadeOut: 0.07})
	b.art(tracker.Articulation{Name: "pizzicato", Triggers: []string{"pizz.", "pizz"},
		Zones: []tracker.Zone{b.zone("C-0", below, low.pizz), b.zone(split, "B-9", high.pizz)}, FadeOut: 0.5})
	b.art(tracker.Articulation{Name: "tremolo", Triggers: []string{"trem.", "trem"},
		Zones: []tracker.Zone{b.zone("C-0", below, low.short), b.zone(split, "B-9", high.short)}, VolEnv: tremoloEnv(), Gain: -1})
	b.art(tracker.Articulation{Name: "muted", Triggers: []string{"con sord.", "con sordino", "sord."},
		Zones: []tracker.Zone{b.zone("C-0", below, low.soft), b.zone(split, "B-9", high.soft)}, Gain: -5, FadeOut: 0.35})
	b.in.Playback.Staccato = 0.4
	return b
}

// windArticulations are what every wind and brass instrument here has: a
// tongued staccato and an accented marcato over the same samples, a
// flutter-tongue tremolo, and (brass) a muted soft layer.
func windArticulations(b *builder, soft string, zones []tracker.Zone) {
	b.art(tracker.Articulation{Name: "staccato", Triggers: []string{"stacc.", "staccatissimo"}, FadeOut: 0.05})
	b.art(tracker.Articulation{Name: "marcato", Triggers: []string{"accent", "sfz", "marc."},
		VolEnv: env(2, -1, -1, 0, 0, 0.01, 1.25, 0.12, 0.85, 0.3, 0), Gain: 1})
	b.art(tracker.Articulation{Name: "tremolo", Triggers: []string{"flz.", "flutter", "trem."}, VolEnv: tremoloEnv()})
	if soft != "" {
		var mz []tracker.Zone
		for _, z := range zones {
			mz = append(mz, tracker.Zone{Sample: b.s(soft), Low: z.Low, High: z.High})
		}
		b.art(tracker.Articulation{Name: "muted", Triggers: []string{"con sord.", "mute", "muted", "sord."}, Zones: mz, Gain: -6})
	}
}

func orchestralInstruments() []StarterInstrument {
	var out []StarterInstrument
	add := func(name string, b *builder) {
		out = append(out, StarterInstrument{Name: name, Inst: b.in})
	}

	// ---- strings ----
	b := stringSection("Violin Section", "A violin section: sustain, staccato, pizzicato, tremolo, muted", "C-5", upperLow, upperHigh)
	b.playback("G-3", "E-7", "treble", 0, tracker.GlissSlide)
	b.in.Playback.Staccato = 0.4
	add("strings/violin_section", b)

	b = stringSection("Viola Section", "A viola section: darker than the violins, read in the alto clef", "A-4", upperLow, upperHigh)
	b.in.Filter, b.in.Cutoff, b.in.Resonance = tracker.FilterLowPass, 4800, 0.1
	b.playback("C-3", "E-6", "alto", 0, tracker.GlissSlide)
	b.in.Playback.Staccato = 0.4
	add("strings/viola_section", b)

	b = stringSection("Cello Section", "A cello section: sustain, staccato, pizzicato, tremolo, muted", "D#3", lowerLow, lowerHigh)
	b.playback("C-2", "A-5", "bass", 0, tracker.GlissSlide)
	b.in.Playback.Staccato = 0.4
	add("strings/cello_section", b)

	b = stringSection("Contrabass Section", "Double basses: written an octave above where they sound", "G-3", lowerLow, lowerHigh)
	b.in.Filter, b.in.Cutoff, b.in.Resonance = tracker.FilterLowPass, 2600, 0.1
	b.playback("E-1", "G-4", "bass", -12, tracker.GlissSlide)
	b.in.Playback.Staccato = 0.4
	add("strings/contrabass_section", b)

	// ---- woodwinds ----
	b = newBuilder("Flute", "Flute: pure tone, breath and chiff")
	b.in.Zones = []tracker.Zone{b.zone("C-0", "B-9", "winds/flute")}
	b.in.FadeOut, b.in.VibRate, b.in.VibDepth, b.in.VibDelay = 0.12, 5, 0.1, 0.3
	windArticulations(b, "", nil)
	b.playback("C-4", "C-7", "treble", 0, tracker.GlissChromatic)
	add("woodwinds/flute", b)

	b = newBuilder("Oboe", "Oboe: narrow, nasal double reed")
	b.in.Zones = []tracker.Zone{b.zone("C-0", "B-9", "winds/oboe")}
	b.in.FadeOut, b.in.VibRate, b.in.VibDepth, b.in.VibDelay = 0.1, 5.2, 0.08, 0.35
	windArticulations(b, "", nil)
	b.playback("A#3", "A-6", "treble", 0, tracker.GlissChromatic)
	add("woodwinds/oboe", b)

	b = newBuilder("Clarinet in B♭", "Clarinet in B-flat: sounds a tone below where it is written")
	b.in.Zones = []tracker.Zone{b.zone("C-0", "B-9", "winds/clarinet")}
	b.in.FadeOut = 0.1
	windArticulations(b, "", nil)
	b.playback("D-3", "A#6", "treble", -2, tracker.GlissChromatic)
	add("woodwinds/clarinet", b)

	b = newBuilder("Bassoon", "Bassoon: a wide double reed in the bass")
	b.in.Zones = []tracker.Zone{b.zone("C-0", "B-9", orch+"bassoon_c2")}
	b.in.FadeOut = 0.1
	windArticulations(b, "", nil)
	b.playback("A#1", "E-5", "bass", 0, tracker.GlissChromatic)
	add("woodwinds/bassoon", b)

	// ---- brass ----
	b = newBuilder("Horn in F", "French horn in F: sounds a fifth below where it is written")
	b.in.Zones = []tracker.Zone{b.layered("C-0", "B-9", orch+"horn_soft", "winds/french_horn")}
	b.in.FadeOut = 0.15
	windArticulations(b, orch+"horn_soft", b.in.Zones)
	b.playback("B-1", "F-5", "treble", -7, tracker.GlissChromatic)
	add("brass/horn", b)

	b = newBuilder("Trumpet in B♭", "Trumpet in B-flat: brightness follows how hard it is blown")
	b.in.Zones = []tracker.Zone{b.layered("C-0", "B-9", orch+"trumpet_soft", "winds/trumpet")}
	b.in.FadeOut = 0.1
	windArticulations(b, orch+"trumpet_soft", b.in.Zones)
	b.playback("E-3", "C-6", "treble", -2, tracker.GlissChromatic)
	add("brass/trumpet", b)

	b = newBuilder("Trombone", "Trombone: brassy, with a slide for glissandi")
	b.in.Zones = []tracker.Zone{b.layered("C-0", "B-9", orch+"trombone_soft", "winds/trombone")}
	b.in.FadeOut = 0.12
	windArticulations(b, orch+"trombone_soft", b.in.Zones)
	b.playback("E-2", "F-5", "bass", 0, tracker.GlissSlide)
	add("brass/trombone", b)

	b = newBuilder("Tuba", "Tuba: the bass of the brass")
	b.in.Zones = []tracker.Zone{b.zone("C-0", "B-9", orch+"tuba_c2")}
	b.in.FadeOut = 0.15
	windArticulations(b, "", nil)
	b.playback("D-1", "F-4", "bass", 0, tracker.GlissChromatic)
	add("brass/tuba", b)

	// ---- keys ----
	b = newBuilder("Grand Piano", "Grand piano in three registers, each with a soft and a hard hammer")
	b.in.Zones = []tracker.Zone{
		b.layered("C-0", "F#2", orch+"piano_soft_c2", orch+"piano_loud_c2"),
		b.layered("G-2", "F#5", orch+"piano_soft_c4", "keys/piano"),
		b.layered("G-5", "B-9", orch+"piano_soft_c6", orch+"piano_loud_c6"),
	}
	b.in.FadeOut, b.in.Polyphony = 0.35, 8
	b.art(tracker.Articulation{Name: "staccato", Triggers: []string{"stacc.", "staccatissimo"}, FadeOut: 0.08})
	b.playback("A-0", "C-8", "grand", 0, tracker.GlissDiatonic)
	add("keys/grand_piano", b)

	b = newBuilder("Celesta", "Celesta: sounds an octave above where it is written")
	b.in.Zones = []tracker.Zone{b.zone("C-0", "B-9", orch+"celesta_c5")}
	b.in.FadeOut, b.in.Polyphony = 0.6, 6
	b.playback("C-4", "C-8", "grand", 12, tracker.GlissDiatonic)
	add("keys/celesta", b)

	b = newBuilder("Harp", "Concert harp: rings until damped; glissandi run the scale")
	b.in.Zones = []tracker.Zone{b.zone("C-0", "B-2", orch+"harp_c2"), b.zone("C-3", "B-9", "plucked/harp")}
	b.in.FadeOut, b.in.Polyphony = 1.5, 8
	b.art(tracker.Articulation{Name: "muted", Triggers: []string{"étouffé", "etouffe", "damped"}, FadeOut: 0.12})
	b.playback("B-0", "G#7", "grand", 0, tracker.GlissDiatonic)
	b.in.Playback.Release = 2.5
	add("keys/harp", b)

	b = newBuilder("Church Organ", "Pipe organ: principal and mixture, with chiff")
	b.in.Zones = []tracker.Zone{b.zone("C-0", "B-9", "keys/organ_pipe")}
	b.in.FadeOut, b.in.Polyphony = 0.18, 6
	b.art(tracker.Articulation{Name: "staccato", Triggers: []string{"stacc."}, FadeOut: 0.06})
	b.playback("C-1", "C-8", "grand", 0, tracker.GlissChromatic)
	add("keys/organ", b)

	// ---- choir ----
	b = newBuilder("Choir", "Mixed choir on 'ah', with 'ooh' as an articulation")
	b.in.Zones = []tracker.Zone{b.zone("C-0", "B-2", "vocal/vox_chant"), b.zone("C-3", "B-9", "vocal/vox_ah")}
	b.in.FadeOut, b.in.Polyphony = 0.45, 4
	b.art(tracker.Articulation{Name: "ooh", Triggers: []string{"oo", "ooh", "bocca chiusa", "humming"},
		Zones: []tracker.Zone{b.zone("C-0", "B-9", "pads/pad_choir")}, FadeOut: 0.6})
	b.art(tracker.Articulation{Name: "staccato", Triggers: []string{"stacc."}, FadeOut: 0.08})
	b.playback("E-2", "C-6", "grand", 0, tracker.GlissSlide)
	add("choir/choir", b)

	// ---- percussion ----
	b = newBuilder("Timpani", "Timpani: tuned kettle drums, soft and hard mallets")
	b.in.Zones = []tracker.Zone{b.layered("C-0", "B-9", orch+"timpani_soft", "drums/timpani")}
	b.in.FadeOut, b.in.Polyphony = 1.2, 4
	b.art(tracker.Articulation{Name: "muted", Triggers: []string{"secco", "muffled", "damped", "étouffé"}, FadeOut: 0.12})
	b.playback("D-2", "C-4", "bass", 0, tracker.GlissSlide)
	b.in.Playback.Release = 3
	add("percussion/timpani", b)

	// General MIDI's percussion keys, so a MIDI file's drums land right.
	b = newBuilder("Orchestral Percussion", "Bass drum, snare, cymbals, triangle, tambourine, woodblock, claves, tam-tam on the General MIDI keys")
	b.in.Zones = []tracker.Zone{
		b.hit("B-1", "drums/taiko", "C-2"),
		b.hit("C-2", "drums/taiko", ""),
		b.hit("D-2", "drums/snare_acoustic", "", orch+"snare_acoustic_rr2", orch+"snare_acoustic_rr3"),
		b.hit("C#3", "drums/crash", ""),
		b.hit("D#3", "drums/ride", ""),
		b.hit("E-3", "fx/fx_impact", ""),
		b.hit("F#3", "drums/tambourine", ""),
		b.hit("A-3", "drums/crash", "G-3"),
		b.hit("D#5", "drums/clave", ""),
		b.hit("E-5", "drums/woodblock", "D-5"),
		b.hit("F-5", "drums/woodblock", ""),
		b.hit("G#5", "drums/triangle", "A-5"),
		b.hit("A-5", "drums/triangle", ""),
	}
	b.in.FadeOut, b.in.Polyphony = 0.08, 8
	b.art(tracker.Articulation{Name: "choked", Triggers: []string{"choke", "l.v. off", "secco"}, FadeOut: 0.03})
	b.in.Playback = tracker.Playback{Clef: "percussion", Release: 4}
	add("percussion/orchestral_percussion", b)

	b = newBuilder("Xylophone", "Xylophone: sounds an octave above where it is written")
	b.in.Zones = []tracker.Zone{b.zone("C-0", "B-9", orch+"xylophone_c5")}
	b.in.FadeOut, b.in.Polyphony = 0.2, 6
	b.playback("F-4", "C-8", "treble", 12, tracker.GlissDiatonic)
	b.in.Playback.Release = 0.8
	add("percussion/xylophone", b)

	b = newBuilder("Glockenspiel", "Glockenspiel: sounds two octaves above where it is written")
	b.in.Zones = []tracker.Zone{b.zone("C-0", "B-9", "keys/glockenspiel")}
	b.in.FadeOut, b.in.Polyphony = 0.6, 6
	b.playback("G-5", "C-8", "treble", 24, tracker.GlissDiatonic)
	b.in.Playback.Release = 2
	add("percussion/glockenspiel", b)

	b = newBuilder("Marimba", "Marimba: tuned wooden bars over resonators")
	b.in.Zones = []tracker.Zone{b.zone("C-0", "B-9", "keys/marimba")}
	b.in.FadeOut, b.in.Polyphony = 0.3, 6
	b.playback("C-2", "C-7", "grand", 0, tracker.GlissDiatonic)
	b.in.Playback.Release = 1
	add("percussion/marimba", b)

	b = newBuilder("Vibraphone", "Vibraphone: long-ringing metal bars")
	b.in.Zones = []tracker.Zone{b.zone("C-0", "B-9", "keys/vibraphone")}
	b.in.FadeOut, b.in.Polyphony = 0.8, 6
	b.art(tracker.Articulation{Name: "muted", Triggers: []string{"dampened", "damped", "secco"}, FadeOut: 0.1})
	b.playback("F-3", "F-6", "treble", 0, tracker.GlissDiatonic)
	add("percussion/vibraphone", b)

	b = newBuilder("Drum Kit", "An acoustic kit on the General MIDI keys, snare and hat in round robin")
	b.in.Zones = []tracker.Zone{
		b.hit("B-1", "drums/kick_acoustic", "C-2"),
		b.hit("C-2", "drums/kick_acoustic", ""),
		b.hit("C#2", "drums/snare_rim", ""),
		b.hit("D-2", "drums/snare_acoustic", "", orch+"snare_acoustic_rr2", orch+"snare_acoustic_rr3"),
		b.hit("D#2", "drums/clap_808", ""),
		b.hit("E-2", "drums/snare_909", ""),
		b.hit("F-2", "drums/tom_floor", ""),
		b.hit("F#2", "drums/hat_closed_808", "", orch+"hat_closed_rr2"),
		b.hit("G-2", "drums/tom_floor", "F-2"),
		b.hit("G#2", "drums/hat_pedal", ""),
		b.hit("A-2", "drums/tom_mid", "B-2"),
		b.hit("A#2", "drums/hat_open_808", ""),
		b.hit("B-2", "drums/tom_mid", ""),
		b.hit("C-3", "drums/tom_mid", "B-2"),
		b.hit("C#3", "drums/crash", ""),
		b.hit("D-3", "drums/tom_mid", "B-2"),
		b.hit("D#3", "drums/ride", ""),
		b.hit("F-3", "drums/ride_bell", ""),
		b.hit("F#3", "drums/tambourine", ""),
		b.hit("G-3", "drums/crash", "D-3"),
		b.hit("G#3", "drums/cowbell_808", ""),
		b.hit("A-3", "drums/crash", "A#3"),
	}
	b.in.FadeOut, b.in.Polyphony = 0.05, 8
	b.in.Playback = tracker.Playback{Clef: "percussion", Release: 3}
	add("percussion/drum_kit", b)

	// ---- synth ----
	synthPad := func(name, title, desc, sample string) {
		b := newBuilder(title, desc)
		b.in.Zones = []tracker.Zone{b.zone("C-0", "B-9", sample)}
		b.in.FadeOut, b.in.Polyphony = 1.1, 6
		b.art(tracker.Articulation{Name: "staccato", Triggers: []string{"stacc."}, FadeOut: 0.12})
		b.playback("C-2", "C-7", "grand", 0, tracker.GlissSlide)
		add("synth/"+name, b)
	}
	synthPad("warm_pad", "Warm Pad", "Warm detuned saws with a slow attack", "pads/pad_warm")
	synthPad("glass_pad", "Glass Pad", "Glassy bell partials, slowly beating", "pads/pad_glass")
	synthPad("string_machine", "String Machine", "A 70s ensemble string machine", "pads/pad_string_machine")
	synthPad("shimmer_pad", "Shimmer Pad", "Octaves that sparkle as they move", "pads/pad_shimmer")

	synthLead := func(name, title, desc, sample string) {
		b := newBuilder(title, desc)
		b.in.Zones = []tracker.Zone{b.zone("C-0", "B-9", sample)}
		b.in.FadeOut = 0.12
		b.in.VibRate, b.in.VibDepth, b.in.VibDelay = 5.5, 0.15, 0.3
		b.art(tracker.Articulation{Name: "staccato", Triggers: []string{"stacc."}, FadeOut: 0.04})
		b.playback("C-3", "C-7", "treble", 0, tracker.GlissSlide)
		add("synth/"+name, b)
	}
	synthLead("supersaw_lead", "Supersaw Lead", "Seven detuned saws across the stereo field", "leads/lead_supersaw")
	synthLead("square_lead", "Square Lead", "A gently filtered square", "leads/lead_square")
	synthLead("sync_lead", "Sync Lead", "Hard-sync saw with a sweeping slave", "leads/lead_sync")

	b = newBuilder("Acid Bass", "A raw saw through a resonant low-pass that snaps shut on every note")
	b.in.Zones = []tracker.Zone{b.zone("C-0", "B-9", "bass/bass_acid")}
	b.in.Filter, b.in.Cutoff, b.in.Resonance = tracker.FilterLowPass, 500, 0.75
	b.in.FilterEnv = env(-1, -1, -1, 0, 3, 0.25, 0.5, 0.6, 0)
	b.in.FadeOut = 0.06
	b.art(tracker.Articulation{Name: "staccato", Triggers: []string{"stacc."}, FadeOut: 0.03})
	b.playback("C-1", "G-4", "bass", 0, tracker.GlissSlide)
	add("synth/acid_bass", b)

	b = newBuilder("Analog Bass", "Two detuned saws into a ladder filter")
	b.in.Zones = []tracker.Zone{b.zone("C-0", "B-9", "bass/bass_moog")}
	b.in.FadeOut = 0.08
	b.art(tracker.Articulation{Name: "staccato", Triggers: []string{"stacc."}, FadeOut: 0.03})
	b.playback("C-1", "G-4", "bass", 0, tracker.GlissSlide)
	add("synth/analog_bass", b)

	return out
}

// ---------- gen-instruments ----------

// InstrumentsMain runs `gen-instruments [-out dir] [-samples dir]
// [-no-samples] [-only name,...] [-list]`: it renders the variant samples
// the orchestral set plays, then writes every starter instrument.
func InstrumentsMain(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("gen-instruments", flag.ContinueOnError)
	fs.SetOutput(out)
	dir := fs.String("out", "", "instrument directory (default: the project's instrument library)")
	smpDir := fs.String("samples", "", "sample directory for the variants (default: the project's sample library)")
	noSamples := fs.Bool("no-samples", false, "write the instruments only; do not render the variant samples")
	only := fs.String("only", "", "comma-separated instrument names to write (default: all)")
	list := fs.Bool("list", false, "list the instruments and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	all := StarterInstruments()
	if *list {
		for _, si := range all {
			fmt.Fprintf(out, "%-36s %s\n", si.Name, si.Inst.Description)
		}
		return 0
	}
	if *dir == "" {
		*dir = tracker.InstrumentsDir()
	}
	if *smpDir == "" {
		*smpDir = tracker.SamplesDir()
	}
	todo := all
	if *only != "" {
		todo = nil
		for _, name := range strings.Split(*only, ",") {
			name = strings.TrimSpace(name)
			found := false
			for _, si := range all {
				if si.Name == name || filepath.Base(si.Name) == name {
					todo, found = append(todo, si), true
				}
			}
			if name != "" && !found {
				fmt.Fprintf(out, "gen-instruments: no instrument %q (try -list)\n", name)
				return 1
			}
		}
	}
	if !*noSamples {
		need := map[string]bool{}
		for _, si := range todo {
			for _, s := range si.Inst.Samples {
				need[s] = true
			}
		}
		n := 0
		for i := range Variants {
			v := &Variants[i]
			if !need[v.LibraryName()] {
				continue
			}
			if err := writeRecipe(*smpDir, Rate, v.recipe(), nil); err != nil {
				fmt.Fprintf(out, "gen-instruments: %v\n", err)
				return 1
			}
			n++
			fmt.Fprintf(out, "sample %s\n", v.LibraryName())
		}
		fmt.Fprintf(out, "gen-instruments: %d variant samples -> %s\n", n, *smpDir)
	}
	for _, si := range todo {
		path := filepath.Join(*dir, filepath.FromSlash(si.Name)+tracker.InstrumentExt)
		if err := tracker.SaveInstrumentFile(path, si.Inst); err != nil {
			fmt.Fprintf(out, "gen-instruments: %s: %v\n", si.Name, err)
			return 1
		}
	}
	tracker.ReloadInstruments()
	fmt.Fprintf(out, "gen-instruments: %d instruments -> %s\n", len(todo), *dir)
	return 0
}

// knownSample reports whether a library name is something gen-samples or
// gen-instruments writes.
func knownSample(name string) bool {
	return ByName(name) != nil || VariantByName(name) != nil
}

// checkStarter reports instruments that name a sample nothing generates.
func checkStarter() []string {
	var bad []string
	for _, si := range StarterInstruments() {
		for _, s := range si.Inst.Samples {
			if !knownSample(s) {
				bad = append(bad, fmt.Sprintf("%s names sample %q, which nothing generates", si.Name, s))
			}
		}
	}
	return bad
}
