// Package gm is General MIDI as the Score editor's importers and exporters
// need it: the 128 program names, the starter library instrument each
// program plays as (and back), the channel-10 drum kit, and a guess at an
// instrument from a name — a MIDI track's "Violins", a MusicXML part's
// "Clarinet in B♭" or its instrument-sound "wind.reed.clarinet.bflat".
//
// Every name it returns is a .finst under sounds/instruments/; Pick checks
// the library and falls back along a family when a project has thinned its
// instruments out.
package gm

import (
	"strings"
	"unicode"

	"foundry/internal/tracker"
)

// DrumChannel is channel 10, 0-based: General MIDI's percussion.
const DrumChannel = 9

// DrumKit is the instrument a channel-10 part plays.
const DrumKit = "percussion/drum_kit"

// Fallback is the instrument when nothing else fits.
const Fallback = "keys/grand_piano"

// Names are the General MIDI program names, 0-based.
var Names = [128]string{
	"Acoustic Grand Piano", "Bright Acoustic Piano", "Electric Grand Piano", "Honky-tonk Piano",
	"Electric Piano 1", "Electric Piano 2", "Harpsichord", "Clavinet",
	"Celesta", "Glockenspiel", "Music Box", "Vibraphone", "Marimba", "Xylophone", "Tubular Bells", "Dulcimer",
	"Drawbar Organ", "Percussive Organ", "Rock Organ", "Church Organ", "Reed Organ", "Accordion", "Harmonica", "Tango Accordion",
	"Acoustic Guitar (nylon)", "Acoustic Guitar (steel)", "Electric Guitar (jazz)", "Electric Guitar (clean)",
	"Electric Guitar (muted)", "Overdriven Guitar", "Distortion Guitar", "Guitar Harmonics",
	"Acoustic Bass", "Electric Bass (finger)", "Electric Bass (pick)", "Fretless Bass",
	"Slap Bass 1", "Slap Bass 2", "Synth Bass 1", "Synth Bass 2",
	"Violin", "Viola", "Cello", "Contrabass", "Tremolo Strings", "Pizzicato Strings", "Orchestral Harp", "Timpani",
	"String Ensemble 1", "String Ensemble 2", "Synth Strings 1", "Synth Strings 2",
	"Choir Aahs", "Voice Oohs", "Synth Voice", "Orchestra Hit",
	"Trumpet", "Trombone", "Tuba", "Muted Trumpet", "French Horn", "Brass Section", "Synth Brass 1", "Synth Brass 2",
	"Soprano Sax", "Alto Sax", "Tenor Sax", "Baritone Sax", "Oboe", "English Horn", "Bassoon", "Clarinet",
	"Piccolo", "Flute", "Recorder", "Pan Flute", "Blown Bottle", "Shakuhachi", "Whistle", "Ocarina",
	"Lead 1 (square)", "Lead 2 (sawtooth)", "Lead 3 (calliope)", "Lead 4 (chiff)",
	"Lead 5 (charang)", "Lead 6 (voice)", "Lead 7 (fifths)", "Lead 8 (bass + lead)",
	"Pad 1 (new age)", "Pad 2 (warm)", "Pad 3 (polysynth)", "Pad 4 (choir)",
	"Pad 5 (bowed)", "Pad 6 (metallic)", "Pad 7 (halo)", "Pad 8 (sweep)",
	"FX 1 (rain)", "FX 2 (soundtrack)", "FX 3 (crystal)", "FX 4 (atmosphere)",
	"FX 5 (brightness)", "FX 6 (goblins)", "FX 7 (echoes)", "FX 8 (sci-fi)",
	"Sitar", "Banjo", "Shamisen", "Koto", "Kalimba", "Bagpipe", "Fiddle", "Shanai",
	"Tinkle Bell", "Agogo", "Steel Drums", "Woodblock", "Taiko Drum", "Melodic Tom", "Synth Drum", "Reverse Cymbal",
	"Guitar Fret Noise", "Breath Noise", "Seashore", "Bird Tweet", "Telephone Ring", "Helicopter", "Applause", "Gunshot",
}

// programs is the starter-library instrument each program plays as.
var programs = [128]string{
	// piano
	"keys/grand_piano", "keys/piano", "keys/piano", "keys/piano",
	"keys/epiano_tine", "keys/epiano_reed", "keys/harpsichord", "keys/clavinet",
	// chromatic percussion
	"keys/celesta", "percussion/glockenspiel", "keys/music_box", "percussion/vibraphone",
	"percussion/marimba", "percussion/xylophone", "percussion/glockenspiel", "plucked/koto",
	// organ
	"keys/organ_drawbar", "keys/organ_combo", "keys/organ_combo", "keys/organ_pipe",
	"keys/organ", "keys/organ_combo", "leads/lead_vox", "keys/organ_combo",
	// guitar
	"plucked/guitar_nylon", "plucked/guitar_steel", "plucked/guitar_steel", "plucked/guitar_steel",
	"plucked/guitar_muted", "plucked/guitar_power", "plucked/guitar_power", "plucked/guitar_nylon",
	// bass
	"bass/bass_upright", "bass/bass_finger", "bass/bass_pick", "bass/bass_rubber",
	"bass/bass_slap", "bass/bass_slap", "bass/bass_moog", "bass/bass_fm",
	// strings
	"strings/violin_section", "strings/viola_section", "strings/cello_section", "strings/contrabass_section",
	"strings/violin_section", "pads/strings_pizz", "keys/harp", "percussion/timpani",
	// ensemble
	"strings/violin_section", "pads/strings_section", "synth/string_machine", "pads/pad_string_machine",
	"choir/choir", "vocal/vox_ooh", "pads/pad_choir", "fx/fx_impact",
	// brass
	"brass/trumpet", "brass/trombone", "brass/tuba", "brass/trumpet",
	"brass/horn", "winds/brass_section", "winds/brass_section", "leads/lead_pwm",
	// reed
	"woodwinds/clarinet", "woodwinds/clarinet", "woodwinds/clarinet", "woodwinds/bassoon",
	"woodwinds/oboe", "woodwinds/oboe", "woodwinds/bassoon", "woodwinds/clarinet",
	// pipe
	"woodwinds/flute", "woodwinds/flute", "winds/flute", "winds/pan_flute",
	"winds/pan_flute", "winds/pan_flute", "leads/lead_whistle", "leads/lead_ocarina",
	// synth lead
	"leads/lead_square", "leads/lead_supersaw", "leads/lead_whistle", "leads/lead_pluck",
	"leads/lead_overdrive", "leads/lead_vox", "leads/lead_sync", "leads/lead_acid",
	// synth pad
	"pads/pad_shimmer", "pads/pad_warm", "pads/pad_pwm", "pads/pad_choir",
	"pads/pad_bowed_metal", "pads/pad_glass", "pads/pad_air", "pads/pad_sweep",
	// synth effects
	"pads/pad_glass", "pads/pad_dark", "keys/music_box", "pads/pad_air",
	"pads/pad_shimmer", "pads/drone_scifi", "pads/pad_sweep", "pads/drone_scifi",
	// ethnic
	"plucked/koto", "plucked/banjo", "plucked/koto", "plucked/koto",
	"plucked/kalimba", "woodwinds/oboe", "strings/violin_section", "woodwinds/oboe",
	// percussive
	"percussion/glockenspiel", "drums/cowbell_808", "percussion/marimba", "drums/woodblock",
	"drums/taiko", "drums/tom_mid", "drums/tom_808", "drums/cymbal_reverse",
	// sound effects
	"plucked/guitar_muted", "fx/fx_whoosh", "fx/fx_whoosh", "leads/lead_whistle",
	"fx/fx_radio", "fx/fx_riser", "fx/fx_vinyl", "fx/fx_impact",
}

// families are the fallbacks, in order, for an instrument a project does
// not have: the first of the same category that it does, then these.
var families = map[string][]string{
	"strings":    {"strings/violin_section", "pads/strings_section"},
	"brass":      {"brass/horn", "winds/brass_section"},
	"woodwinds":  {"woodwinds/flute", "winds/flute"},
	"winds":      {"winds/flute", "woodwinds/flute"},
	"keys":       {"keys/grand_piano", "keys/piano"},
	"percussion": {"percussion/drum_kit", "drums/snare_acoustic"},
	"drums":      {"percussion/drum_kit"},
	"choir":      {"choir/choir", "vocal/vox_ah"},
	"vocal":      {"choir/choir", "vocal/vox_ah"},
	"plucked":    {"plucked/guitar_nylon", "keys/harp"},
	"bass":       {"bass/bass_finger", "bass/bass_upright"},
	"pads":       {"pads/pad_warm"},
	"leads":      {"leads/lead_square"},
	"synth":      {"synth/warm_pad", "pads/pad_warm"},
	"fx":         {"fx/fx_impact"},
	"chip":       {"chip/chip_pulse_50"},
}

// Instrument is the starter instrument for a 0-based program (out of
// range is the piano). It does not look in the library: Pick does.
func Instrument(program int) string {
	if program < 0 || program > 127 {
		return Fallback
	}
	return programs[program]
}

// categoryPrograms are the program an instrument of a category exports
// as when the table above never names it.
var categoryPrograms = map[string]int{
	"strings": 48, "brass": 61, "woodwinds": 73, "winds": 73, "keys": 0,
	"choir": 52, "vocal": 52, "percussion": 0, "drums": 0, "plucked": 24,
	"bass": 33, "pads": 89, "leads": 80, "fx": 55, "chip": 80,
}

// preferred are the program for instruments the table names more than
// once, or not at all, where the first mention is not the best.
var preferred = map[string]int{
	"strings/violin_section": 40, "woodwinds/flute": 73, "woodwinds/clarinet": 71,
	"woodwinds/oboe": 68, "woodwinds/bassoon": 70, "keys/piano": 1,
	"percussion/glockenspiel": 9, "brass/trumpet": 56, "plucked/guitar_steel": 25,
	"plucked/harp": 46, "winds/clarinet": 71, "winds/oboe": 68, "winds/trumpet": 56,
	"winds/trombone": 57, "winds/french_horn": 60, "pads/cello": 42,
	"synth/acid_bass": 38, "synth/analog_bass": 39, "synth/glass_pad": 93, "synth/shimmer_pad": 88,
	"synth/square_lead": 80, "synth/string_machine": 50, "synth/supersaw_lead": 81,
	"synth/sync_lead": 86, "synth/warm_pad": 89, "vocal/vox_ah": 52, "vocal/vox_chant": 52,
	"keys/organ": 19, "keys/glockenspiel": 9, "keys/marimba": 12, "keys/vibraphone": 11,
	"leads/lead_whistle": 78, "plucked/koto": 107, "pads/strings_section": 49,
}

// Program is the 0-based program an instrument exports as: where the
// table plays it, or its category's usual program.
func Program(instrument string) int {
	if p, ok := preferred[instrument]; ok {
		return p
	}
	for i, n := range programs {
		if n == instrument {
			return i
		}
	}
	cat, name, _ := strings.Cut(instrument, "/")
	switch {
	case strings.Contains(name, "bass"):
		return 38
	case strings.Contains(name, "pad"):
		return 89
	case strings.Contains(name, "lead"):
		return 81
	}
	if p, ok := categoryPrograms[cat]; ok {
		return p
	}
	return 0
}

// Percussive reports an instrument that plays as a kit: channel 10, each
// key its own sound.
func Percussive(instrument string) bool {
	cat, _, _ := strings.Cut(instrument, "/")
	return cat == "drums" || instrument == DrumKit || instrument == "percussion/orchestral_percussion"
}

// Pick returns the first candidate the library has, then the first of a
// candidate's family, then the piano; with no library at all it returns
// the first candidate.
func Pick(candidates ...string) string {
	for _, c := range candidates {
		if c != "" && tracker.InstrumentPath(c) != "" {
			return c
		}
	}
	for _, c := range candidates {
		cat, _, _ := strings.Cut(c, "/")
		for _, f := range families[cat] {
			if tracker.InstrumentPath(f) != "" {
				return f
			}
		}
	}
	if tracker.InstrumentPath(Fallback) != "" || len(candidates) == 0 || candidates[0] == "" {
		return Fallback
	}
	return candidates[0]
}

// rule is a set of words that names an instrument: any of the phrases, as
// whole words, and none of the exclusions.
type rule struct {
	phrases    []string
	not        []string
	instrument string
}

// rules are tried in order, so the specific come before the general:
// "bass clarinet" before "clarinet" before "bass", "english horn" before
// "horn", "timpani" before "percussion".
var rules = []rule{
	{phrases: []string{"pizz", "pizzicato"}, instrument: "pads/strings_pizz"},
	{phrases: []string{"contrabass", "double bass", "string bass", "basses", "cb", "kb", "kontrabass"}, not: []string{"clarinet", "bassoon", "trombone", "tuba", "guitar"}, instrument: "strings/contrabass_section"},
	{phrases: []string{"violoncello", "violoncelli", "cello", "cellos", "celli", "vc", "vlc"}, instrument: "strings/cello_section"},
	{phrases: []string{"viola", "violas", "vla", "vle", "va"}, not: []string{"gamba"}, instrument: "strings/viola_section"},
	{phrases: []string{"violin", "violins", "vln", "vn", "vl", "fiddle", "violino", "violini"}, instrument: "strings/violin_section"},
	{phrases: []string{"string machine", "synth strings"}, instrument: "synth/string_machine"},
	{phrases: []string{"strings", "string ensemble", "string orchestra"}, instrument: "strings/violin_section"},
	{phrases: []string{"english horn", "cor anglais", "oboe", "oboes", "ob", "shanai"}, instrument: "woodwinds/oboe"},
	{phrases: []string{"contrabassoon", "bassoon", "bassoons", "bsn", "fagott", "fagotto"}, instrument: "woodwinds/bassoon"},
	{phrases: []string{"clarinet", "clarinets", "cl", "clar", "klarinette"}, instrument: "woodwinds/clarinet"},
	{phrases: []string{"sax", "saxophone", "soprano sax", "alto sax", "tenor sax", "baritone sax", "reed"}, instrument: "woodwinds/clarinet"},
	{phrases: []string{"piccolo", "flute", "flutes", "fl", "flauto", "recorder", "fife"}, instrument: "woodwinds/flute"},
	{phrases: []string{"pan flute", "panpipes", "shakuhachi"}, instrument: "winds/pan_flute"},
	{phrases: []string{"horn", "horns", "french horn", "hn", "hrn", "corno", "corni"}, not: []string{"english"}, instrument: "brass/horn"},
	{phrases: []string{"trumpet", "trumpets", "tpt", "trp", "cornet", "flugelhorn", "tromba"}, instrument: "brass/trumpet"},
	{phrases: []string{"trombone", "trombones", "tbn", "trb", "bass trombone"}, instrument: "brass/trombone"},
	{phrases: []string{"tuba", "tubas", "euphonium", "sousaphone", "tba"}, instrument: "brass/tuba"},
	{phrases: []string{"brass", "brass section"}, instrument: "winds/brass_section"},
	{phrases: []string{"timpani", "timp", "timps", "kettle drums", "kettledrums", "pauken"}, instrument: "percussion/timpani"},
	{phrases: []string{"glockenspiel", "glock", "orchestral bells", "tubular bells", "chimes", "bells"}, instrument: "percussion/glockenspiel"},
	{phrases: []string{"xylophone", "xylo", "xyl"}, instrument: "percussion/xylophone"},
	{phrases: []string{"marimba", "steel drums"}, instrument: "percussion/marimba"},
	{phrases: []string{"vibraphone", "vibes", "vib"}, instrument: "percussion/vibraphone"},
	{phrases: []string{"celesta", "celeste"}, instrument: "keys/celesta"},
	{phrases: []string{"harpsichord", "cembalo"}, instrument: "keys/harpsichord"},
	{phrases: []string{"harp", "harps", "hp", "arpa"}, instrument: "keys/harp"},
	{phrases: []string{"church organ", "pipe organ"}, instrument: "keys/organ_pipe"},
	{phrases: []string{"organ", "org"}, instrument: "keys/organ"},
	{phrases: []string{"electric piano", "rhodes", "epiano", "e piano"}, instrument: "keys/epiano_tine"},
	{phrases: []string{"piano", "pno", "pf", "grand", "keyboard", "pianoforte"}, instrument: "keys/grand_piano"},
	{phrases: []string{"drum set", "drum kit", "drumset", "drums", "kit", "drum", "snare", "kick", "hi hat", "hihat", "cymbal", "cymbals"}, instrument: DrumKit},
	{phrases: []string{"percussion", "perc"}, instrument: "percussion/orchestral_percussion"},
	{phrases: []string{"choir", "chorus", "voice", "voices", "vocals", "vocal", "soprano", "alto", "tenor", "baritone", "aah", "aahs", "ooh", "satb"}, instrument: "choir/choir"},
	{phrases: []string{"electric guitar", "distortion guitar", "overdriven guitar"}, instrument: "plucked/guitar_power"},
	{phrases: []string{"steel guitar", "acoustic guitar"}, instrument: "plucked/guitar_steel"},
	{phrases: []string{"guitar", "guitars", "gtr", "classical guitar", "nylon"}, instrument: "plucked/guitar_nylon"},
	{phrases: []string{"banjo"}, instrument: "plucked/banjo"},
	{phrases: []string{"koto", "sitar", "shamisen", "dulcimer"}, instrument: "plucked/koto"},
	{phrases: []string{"kalimba", "mbira"}, instrument: "plucked/kalimba"},
	{phrases: []string{"synth bass"}, instrument: "bass/bass_moog"},
	{phrases: []string{"upright bass", "acoustic bass"}, instrument: "bass/bass_upright"},
	{phrases: []string{"bass guitar", "electric bass", "bass", "e bass"}, instrument: "bass/bass_finger"},
	{phrases: []string{"pad", "synth pad"}, instrument: "pads/pad_warm"},
	{phrases: []string{"lead", "synth lead", "synth"}, instrument: "leads/lead_square"},
}

// words splits a name into lower-case words: punctuation, the dots and
// dashes of a MusicXML instrument-sound, and digits all break words; the
// music symbols ♭ ♯ go.
func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r)
	})
}

// sameWord reports a name's word w as a phrase's word p, or its plural
// ("Violoncellos", "Contrabasses", "Piccolos"): an orchestra's parts are
// named for their sections.
func sameWord(w, p string) bool {
	if w == p {
		return true
	}
	if len(p) < 3 || !strings.HasPrefix(w, p) {
		return false
	}
	switch w[len(p):] {
	case "s", "es":
		return true
	}
	return false
}

// containsPhrase reports whether ws holds the words of phrase in a row.
func containsPhrase(ws []string, phrase string) bool {
	p := strings.Fields(phrase)
	for i := 0; i+len(p) <= len(ws); i++ {
		ok := true
		for j := range p {
			if !sameWord(ws[i+j], p[j]) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// Match guesses an instrument from a name ("Violin I", "Clarinet in B♭",
// "strings.violin", "Pno."), or "" when nothing matches.
func Match(name string) string {
	ws := words(name)
	if len(ws) == 0 {
		return ""
	}
	for _, r := range rules {
		hit := false
		for _, p := range r.phrases {
			if containsPhrase(ws, p) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		excluded := false
		for _, n := range r.not {
			if containsPhrase(ws, n) {
				excluded = true
				break
			}
		}
		if !excluded {
			return r.instrument
		}
	}
	return ""
}

// Transposition is a standard written-to-sounding interval for a number of
// semitones (an instrument's Playback.Transposition): -2 is a major second
// down, -7 a perfect fifth down, +12 an octave up.
func Transposition(semis int) (diatonic, chromatic int) {
	// The usual spelling of each interval inside an octave, by semitones.
	steps := [12]int{0, 1, 1, 2, 2, 3, 3, 4, 5, 5, 6, 6}
	s, sign := semis, 1
	if s < 0 {
		s, sign = -s, -1
	}
	return sign * (steps[s%12] + 7*(s/12)), semis
}

// Playback reads a library instrument's playback defaults (clef, range,
// transposition) from its .finst without loading its samples.
func Playback(instrument string) (tracker.Playback, bool) {
	p := tracker.InstrumentPath(instrument)
	if p == "" {
		return tracker.Playback{}, false
	}
	in, err := tracker.LoadInstrumentFile(p)
	if err != nil {
		return tracker.Playback{}, false
	}
	return in.Playback, true
}
