package musicxml

// import.go — a MusicXML document as a score.
//
// MusicXML is already notation, so this is mostly translation: each part a
// line, each <staff> a staff, each <voice> a voice, notes, chords, rests,
// dots, ties, tuplets (nested, with or without brackets written), grace
// notes, clefs, keys, meters, tempo and metronome marks, transposing
// instruments, part names and MIDI programs. What it has to work out:
//
//   - Time. A measure is a stream with a cursor that <backup> and
//     <forward> move; every note is placed at the cursor in divisions (a
//     part's own unit, which may change) and turned into exact fractions.
//     Divisions that cannot hold a triplet exactly (Sibelius's 256) leave
//     notes a division off; positions within a division snap to where the
//     voice already is.
//   - Voices. Sibelius numbers them 1–4 on the first staff and 5–8 on the
//     second; others restart on each staff or write none at all. A voice
//     belongs to the staff most of its notes are on (a cross-staff note
//     stays in its voice), unless it is really two voices that share a
//     number.
//   - Pitch. MusicXML writes transposing parts as written; a score stores
//     concert pitch and the staff's interval, so pitches are moved and the
//     key the score keeps is the concert one.
//   - Bars. A first measure marked implicit and shorter than its meter is
//     a pickup; a longer measure (a cadenza) gets a meter of its own.
//   - Directions. Dynamics, words, wedges, octave shifts and pedal marks
//     sit between notes in MusicXML and on events in a score: each goes on
//     the event at (or, for an ending, before) its place on its staff.
//
//   - Form. Repeat bar lines are repeats, <ending>s their voltas, segno
//     and coda signs, To Coda, Fine, D.C. and D.S. (from <sound> and from
//     their words) the score's jumps.
//   - Layout. The page in <defaults> (paper, margins, staff size) and
//     <print new-system/new-page> become the score's layout and breaks,
//     unless Options.NoLayout.
//
//   - Words and strings (words.go). Lyrics, verse by verse, onto their
//     notes; chord symbols over the part's line at their places; a TAB
//     staff as tablature with its tuning, and a TAB staff that repeats
//     the notation staff over it as that staff's linked tablature.
//
// A file this package wrote (export.go, Software) is read with what only
// it writes: stem directions (kept as marks where the engraver would not
// draw them so, stems.go), velocities (a note's dynamics attribute),
// grace timing, and the library instrument a part names. What a score
// cannot hold is reported as a warning, never silently dropped.

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"foundry/internal/score"
	"foundry/internal/score/io/gm"
	"foundry/internal/score/io/notate"
	"foundry/internal/tracker"
)

// PartInfo describes a part for an import dialog.
type PartInfo struct {
	ID, Name string
	Staves   int
	Notes    int
	// Program is the part's General MIDI program (0-based), -1 if unsaid.
	Program    int
	Percussion bool
	// Instrument is the guessed library instrument; Clefs the staves'.
	Instrument string
	Clefs      []string
	Transpose  score.Interval
}

// Choice is what to do with one part (in Parts order).
type Choice struct {
	Skip             bool
	Name, Instrument string
}

// Options shape an import.
type Options struct {
	// Name is the score's library name; Title replaces the file's title.
	Name, Title string
	Choices     []Choice
	// NoLayout leaves the file's page layout out (paper, margins, staff
	// size, system and page breaks): the score is laid out its own way.
	NoLayout bool
}

// partHead is a <score-part>.
type partHead struct {
	id, name, abbrev string
	instName, sound  string
	program, channel int
	pan              float32
	unpitched        map[string]int // score-instrument id → MIDI key
	// volume is the part's MIDI volume, 0..1 (-1 unsaid); virtual the
	// library instrument a Foundry export named.
	volume  float32
	virtual string
}

func (d *Document) heads() map[string]*partHead {
	out := map[string]*partHead{}
	for _, sp := range d.root.child("part-list").children("score-part") {
		h := &partHead{id: sp.attr("id"), name: cleanName(sp.str("part-name")), abbrev: cleanName(sp.str("part-abbreviation")),
			program: -1, channel: -1, unpitched: map[string]int{}, volume: -1}
		if dn := sp.child("part-name-display"); dn != nil && h.name == "" {
			h.name = cleanName(displayText(dn))
		}
		if si := sp.child("score-instrument"); si != nil {
			h.instName = cleanName(si.str("instrument-name"))
			h.sound = si.str("instrument-sound")
		}
		for _, si := range sp.children("score-instrument") {
			if vi := si.child("virtual-instrument"); vi != nil && vi.str("virtual-library") == "Foundry" && h.virtual == "" {
				h.virtual = vi.str("virtual-name")
			}
		}
		for _, mi := range sp.children("midi-instrument") {
			if p := mi.integer("midi-program", 0); p > 0 && h.program < 0 {
				h.program = p - 1
			}
			if c := mi.integer("midi-channel", 0); c > 0 && h.channel < 0 {
				h.channel = c - 1
			}
			if u := mi.integer("midi-unpitched", 0); u > 0 {
				h.unpitched[mi.attr("id")] = u - 1
			}
			if mi.has("pan") && h.pan == 0 {
				h.pan = float32(math.Max(-1, math.Min(1, mi.num("pan", 0)/90)))
			}
			if mi.has("volume") && h.volume < 0 {
				h.volume = float32(math.Max(0, math.Min(2, mi.num("volume", 100)/100)))
			}
		}
		out[h.id] = h
	}
	return out
}

// cleanText folds a MusicXML display string: line breaks become spaces
// and runs of spaces one. The music signs (♭ ♮ ♯) stay in text the score
// prints — words, titles, rehearsal marks: every backend draws them
// (engrave/textsign.go).
func cleanText(s string) string {
	s = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// displayText is a <part-name-display>'s name: its display-text runs with
// the signs its accidental-text elements stand for between them (Sibelius
// writes "Clarinet in B" and a flat so).
func displayText(dn *node) string {
	var b strings.Builder
	for _, k := range dn.kids {
		switch k.name {
		case "display-text":
			b.WriteString(k.text)
		case "accidental-text":
			b.WriteString(map[string]string{"flat": "♭", "sharp": "♯", "natural": "♮",
				"double-sharp": "𝄪", "sharp-sharp": "𝄪", "flat-flat": "𝄫"}[strings.TrimSpace(k.text)])
		}
	}
	return b.String()
}

// cleanName is cleanText for a part's name, with the flat and sharp signs
// as letters ("Clarinet in Bb"): the editor's staff headers and menus set
// names in ImGui's font, which has no ♭ or ♯.
func cleanName(s string) string {
	return cleanText(strings.NewReplacer("♭", "b", "♯", "#", "♮", "").Replace(s))
}

// Parts lists the document's parts with the guesses Import would make.
func (d *Document) Parts() []PartInfo {
	heads := d.heads()
	var out []PartInfo
	for _, p := range d.root.children("part") {
		h := heads[p.attr("id")]
		if h == nil {
			h = &partHead{id: p.attr("id"), program: -1, channel: -1}
		}
		info := PartInfo{ID: h.id, Name: partName(h), Program: h.program, Staves: 1}
		clefs := map[int]string{}
		for _, m := range p.children("measure") {
			for _, k := range m.kids {
				switch k.name {
				case "attributes":
					if n := k.integer("staves", 0); n > info.Staves {
						info.Staves = n
					}
					for _, c := range k.children("clef") {
						n := atoiDef(c.attr("number"), 1)
						if _, ok := clefs[n]; !ok {
							clefs[n] = clefName(c)
						}
					}
					if tr := k.child("transpose"); tr != nil && info.Transpose == (score.Interval{}) {
						info.Transpose = transposeOf(tr)
					}
				case "note":
					if !k.has("rest") && !k.has("chord") {
						info.Notes++
					}
					if k.has("unpitched") {
						info.Percussion = true
					}
				}
			}
		}
		for s := 1; s <= info.Staves; s++ {
			c, ok := clefs[s]
			if !ok {
				c = "treble"
				if s == 2 {
					c = "bass"
				}
			}
			if c == "percussion" {
				info.Percussion = true
			}
			info.Clefs = append(info.Clefs, c)
		}
		if h.channel == gm.DrumChannel {
			info.Percussion = true
		}
		info.Instrument = guessInstrument(h, info.Percussion)
		out = append(out, info)
	}
	return out
}

func partName(h *partHead) string {
	switch {
	case h.name != "":
		return h.name
	case h.instName != "":
		return h.instName
	case h.abbrev != "":
		return h.abbrev
	}
	return h.id
}

// guessInstrument: the library instrument a Foundry export named (when
// this library has it), the instrument-sound, then the names, then the
// program.
func guessInstrument(h *partHead, perc bool) string {
	if h.virtual != "" && tracker.InstrumentPath(h.virtual) != "" {
		return h.virtual
	}
	// The names a person typed come before the sound id: a sound id's
	// family ("strings.baryton" from MuseScore's cello) would otherwise
	// match the family's generic instrument.
	for _, n := range []string{h.name, h.instName, h.sound, h.abbrev} {
		if m := gm.Match(n); m != "" && (!perc || gm.Percussive(m) || strings.HasPrefix(m, "percussion/")) {
			return gm.Pick(m)
		}
	}
	if perc {
		return gm.Pick(gm.DrumKit)
	}
	if h.program >= 0 {
		return gm.Pick(gm.Instrument(h.program))
	}
	return gm.Pick(gm.Fallback)
}

func atoiDef(s string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return v
	}
	return def
}

// clefName maps a <clef> to a staff clef.
func clefName(c *node) string {
	sign := strings.ToUpper(c.str("sign"))
	line := c.integer("line", 0)
	oct := c.integer("clef-octave-change", 0)
	switch sign {
	case "G":
		switch {
		case oct < 0:
			return "treble_8vb"
		case oct > 0:
			return "treble_8va"
		}
		return "treble"
	case "F":
		switch {
		case oct < 0:
			return "bass_8vb"
		case oct > 0:
			return "bass_8va"
		}
		return "bass"
	case "C":
		switch {
		case line >= 4:
			return "tenor"
		case line == 3:
			return "alto"
		}
		return "treble"
	case "PERCUSSION":
		return "percussion"
	case "TAB":
		return "treble_8vb"
	}
	return "treble"
}

// transposeOf reads a <transpose>: written to sounding.
func transposeOf(tr *node) score.Interval {
	oct := tr.integer("octave-change", 0)
	iv := score.Interval{Diatonic: tr.integer("diatonic", 0) + 7*oct, Chromatic: tr.integer("chromatic", 0) + 12*oct}
	if iv.Diatonic == 0 && iv.Chromatic != 0 || !iv.Valid() {
		// No step count, or one that does not go with the semitones: the
		// usual spelling of the semitones.
		iv.Diatonic, _ = gm.Transposition(iv.Chromatic)
	}
	return iv
}

// fifthsShift is how far an interval moves a key signature: up a major
// second is two sharps more.
func fifthsShift(iv score.Interval) int {
	for _, f := range []int{0, 1, -1, 2, -2, 3, -3, 4, -4, 5, -5, 6, -6, 7, -7} {
		if ((f*7-iv.Chromatic)%12+12)%12 == 0 && ((f*4-iv.Diatonic)%7+7)%7 == 0 {
			return f
		}
	}
	return 0
}

// ---------- reading a part into measures of placed events ----------

// xnote is one <note>.
type xnote struct {
	at      score.Frac // offset in the measure
	dur     score.Frac // its <duration>, actual
	staff   int        // 1-based
	voice   string
	chord   bool
	grace   score.Grace
	rest    bool
	barRest bool // <rest measure="yes"/>
	hidden  bool // an invisible rest or a cue note: time, nothing played
	typed   bool
	written score.Duration
	// timeMod is the <time-modification> (actual in the time of normal);
	// normalUnit the <normal-type> value, if given.
	timeMod    [2]int
	normalUnit score.Frac
	tupStart   []xtuplet
	tupStop    []string
	pitch      score.Pitch // as written
	str        int         // the tablature string it is played on, 0 none
	unpitched  int         // MIDI key, -1 for a pitched note
	inst       string      // <instrument id>: an unpitched note's sound
	acc        score.AccShow
	accWritten bool
	tie        bool
	marks      []score.Mark
	// From a Foundry export: the stem it asked for ("up", "down"), its
	// velocity (1..127, 0 none), its grace timing mark.
	stem  string
	vel   int
	timed string
}

type xtuplet struct {
	number   string
	num, den int // tuplet-actual/normal numbers when written
}

// xdir is a <direction> (or a measure-level <sound>).
type xdir struct {
	at    score.Frac
	staff int
	voice string
	marks []score.Mark
	// stop marks go on the event before the place (a wedge's end).
	stops     []score.Mark
	tempo     *score.TempoMark
	rehearsal string
	// tempoTo ends the dashes of the last gradual tempo change (its To).
	tempoTo bool
	// jumps are the form marks it holds (dacapo, dalsegno, tocoda, fine,
	// segno, coda) and the words that came with them.
	jumps []xjump
}

// xjump is a form mark read from a direction or a barline.
type xjump struct {
	kind  string // dacapo, dalsegno, tocoda, fine, segno, coda
	words string
}

// xclef is a clef change inside a measure.
type xclef struct {
	at    score.Frac
	staff int
	clef  string
}

// xmeasure is a part's measure, read.
type xmeasure struct {
	number   string
	implicit bool
	length   score.Frac // how far the cursor got
	notes    []xnote
	dirs     []xdir
	// From <attributes> (nil/zero when the measure has none).
	meter     *score.Meter
	key       *score.KeyMark // as written
	clefs     map[int]string
	transpose *score.Interval
	// From <barline>.
	repeatStart, repeatEnd bool
	repeatTimes            int
	// endingStart is the passes of an ending starting here ("1, 2"),
	// endingStop whether one ends here.
	endingStart string
	hasEnding   bool
	endingStop  bool
	jumps       []xjump
	clefMid     []xclef
	newSystem   bool
	newPage     bool
	// harmonies are the chord symbols in the measure; tabs the staves'
	// tablature details (by staff number).
	harmonies []xharmony
	tabs      map[int]*xtab
}

// xharmony is a chord symbol read, at its place in the measure.
type xharmony struct {
	at    score.Frac
	staff int
	chord score.Chord
}

type partReader struct {
	divisions int
	warn      func(string, ...any)
	// ours is a file this package wrote; first the score's first part
	// (where tempo text is).
	ours, first bool
	// dashes are the open dashes by number: "tempo" (a rit.'s) or
	// "hairpin" (a text crescendo's).
	dashes map[string]string
}

func (r *partReader) frac(divs float64) score.Frac {
	// Divisions are integers in valid files; a decimal one is rounded to
	// a 1/1024 of a division.
	n := int64(math.Round(divs * 1024))
	return score.F(n, int64(r.divisions)*4*1024)
}

var dynamicNames = map[string]string{
	"ppppp": "pppp", "pppppp": "pppp", "fffff": "ffff", "ffffff": "ffff", "sfzp": "sfp", "pf": "fp",
	"n": "niente",
}

// dynamicMarks reads a <dynamics> element.
func dynamicMarks(dn *node) []score.Mark {
	var out []score.Mark
	for _, k := range dn.kids {
		name := k.name
		if name == "other-dynamics" {
			t := cleanText(k.text)
			if _, ok := score.Dynamics[t]; ok {
				out = append(out, score.Mark{Name: t})
			} else if t != "" {
				out = append(out, score.Mark{Name: "text", Value: t})
			}
			continue
		}
		if alias, ok := dynamicNames[name]; ok {
			name = alias
		}
		if _, ok := score.Dynamics[name]; ok {
			out = append(out, score.Mark{Name: name})
		}
	}
	return out
}

// techniqueRE is playing-technique text: what picks an articulation.
var techniqueRE = regexp.MustCompile(`(?i)^(pizz|arco|con sord|senza sord|col legno|sul pont|sul tasto|ord|norm|nat|trem|mute|muted|open|flz|flutter|harm|spicc|sord|via sord|stopped|\+|o$)`)

// wordMark is the mark a <words> text becomes.
func wordMark(text string) score.Mark {
	if techniqueRE.MatchString(text) {
		return score.Mark{Name: "tech", Value: text}
	}
	return score.Mark{Name: "text", Value: text}
}

var durTypes = map[string]score.DurBase{
	"breve": score.Breve, "whole": score.WholeNote, "half": score.Half, "quarter": score.Quarter,
	"eighth": score.Eighth, "16th": score.D16, "32nd": score.D32, "64th": score.D64,
	"128th": score.D128, "256th": score.D256,
}

func durOf(typ string, dots int) (score.Duration, bool) {
	b, ok := durTypes[strings.TrimSpace(typ)]
	if !ok {
		return score.Duration{}, false
	}
	return score.Duration{Base: b, Dots: uint8(min(dots, 3))}, true
}

// readPart reads every measure of a <part>.
func (r *partReader) readPart(p *node) []xmeasure {
	var out []xmeasure
	r.divisions = 1
	for _, m := range p.children("measure") {
		out = append(out, r.readMeasure(m))
	}
	return out
}

func (r *partReader) readMeasure(m *node) xmeasure {
	xm := xmeasure{number: m.attr("number"), implicit: m.attr("implicit") == "yes"}
	cur := score.Frac{}
	reach := func() {
		if xm.length.Less(cur) {
			xm.length = cur
		}
	}
	var last score.Frac // the start of the last non-chord note (a chord's notes share it)
	for _, k := range m.kids {
		switch k.name {
		case "attributes":
			r.readAttributes(k, &xm, cur)
		case "print":
			xm.newSystem = xm.newSystem || k.attr("new-system") == "yes"
			xm.newPage = xm.newPage || k.attr("new-page") == "yes"
		case "backup":
			cur = cur.Sub(r.frac(k.num("duration", 0)))
			if cur.Sign() < 0 {
				cur = score.Frac{}
			}
		case "forward":
			cur = cur.Add(r.frac(k.num("duration", 0)))
			reach()
		case "note":
			n := r.readNote(k)
			if n.chord {
				n.at = last
			} else {
				n.at = cur
				last = cur
			}
			if !n.chord && n.grace == score.NotGrace {
				cur = cur.Add(n.dur)
				reach()
			}
			xm.notes = append(xm.notes, n)
		case "direction":
			xm.dirs = append(xm.dirs, r.readDirection(k, cur))
		case "sound":
			if t := k.attr("tempo"); t != "" {
				if bpm, err := strconv.ParseFloat(t, 64); err == nil && bpm > 0 {
					xm.dirs = append(xm.dirs, xdir{at: cur, staff: 1, tempo: &score.TempoMark{BPM: bpm, Unit: score.Duration{Base: score.Quarter}}})
				}
			}
			xm.jumps = append(xm.jumps, soundJumps(k, "")...)
		case "barline":
			if rp := k.child("repeat"); rp != nil {
				switch rp.attr("direction") {
				case "forward":
					xm.repeatStart = true
				case "backward":
					xm.repeatEnd = true
					xm.repeatTimes = atoiDef(rp.attr("times"), 0)
				}
			}
			for _, en := range k.children("ending") {
				switch en.attr("type") {
				case "start":
					xm.endingStart, xm.hasEnding = en.attr("number"), true
				case "stop", "discontinue":
					xm.endingStop = true
					if !xm.hasEnding && xm.endingStart == "" {
						// Remember the passes in case no start was written.
						xm.endingStart = en.attr("number")
					}
				}
			}
			if k.has("segno") {
				xm.jumps = append(xm.jumps, xjump{kind: "segno"})
			}
			if k.has("coda") {
				xm.jumps = append(xm.jumps, xjump{kind: "coda"})
			}
		case "harmony":
			if c, ok := readHarmony(k, r.ours); ok {
				xm.harmonies = append(xm.harmonies, xharmony{at: cur.Add(r.frac(k.num("offset", 0))), staff: max(1, k.integer("staff", 1)), chord: c})
			} else {
				r.warn("a chord symbol (%s) could not be read; it is left out", strings.TrimSpace(k.child("kind").text))
			}
		}
	}
	return xm
}

// soundJumps are the form marks a <sound> plays: D.C., D.S., To Coda,
// Fine, and the segno and coda it names.
func soundJumps(s *node, words string) []xjump {
	var out []xjump
	for _, a := range []string{"dacapo", "dalsegno", "tocoda", "fine", "segno", "coda"} {
		if v := s.attr(a); v != "" && v != "no" {
			out = append(out, xjump{kind: a, words: words})
		}
	}
	return out
}

// wordJump is the form mark a text names on its own ("D.S. al Coda",
// "To Coda", "Fine"), for files that write the words without a <sound>.
func wordJump(t string) string {
	l := strings.ToLower(strings.TrimSpace(t))
	switch {
	case strings.HasPrefix(l, "d.c.") || strings.HasPrefix(l, "da capo"):
		return "dacapo"
	case strings.HasPrefix(l, "d.s.") || strings.HasPrefix(l, "dal segno"):
		return "dalsegno"
	case l == "to coda" || l == "to coda ø" || l == "al coda":
		return "tocoda"
	case l == "fine":
		return "fine"
	}
	return ""
}

func (r *partReader) readAttributes(a *node, xm *xmeasure, cur score.Frac) {
	if d := a.integer("divisions", 0); d > 0 {
		r.divisions = d
	}
	if k := a.child("key"); k != nil && k.has("fifths") {
		mode := k.str("mode")
		if mode == "" || mode == "none" {
			mode = "major"
		}
		xm.key = &score.KeyMark{Fifths: max(-7, min(7, k.integer("fifths", 0))), Mode: mode}
	}
	if t := a.child("time"); t != nil {
		if mt, ok := meterOf(t); ok {
			xm.meter = &mt
		} else if !t.has("senza-misura") {
			r.warn("time signature %q not understood; the meter before it carries on", t.str("beats")+"/"+t.str("beat-type"))
		}
	}
	for _, d := range a.children("staff-details") {
		if xm.tabs == nil {
			xm.tabs = map[int]*xtab{}
		}
		readStaffDetails(d, xm.tabs)
	}
	for _, c := range a.children("clef") {
		if strings.TrimSpace(c.str("sign")) == "TAB" && cur.IsZero() {
			if xm.tabs == nil {
				xm.tabs = map[int]*xtab{}
			}
			n := atoiDef(c.attr("number"), 1)
			if xm.tabs[n] == nil {
				xm.tabs[n] = &xtab{tuning: map[int]score.Pitch{}}
			}
			xm.tabs[n].clef = true
		}
		if cur.Sign() > 0 {
			xm.clefMid = append(xm.clefMid, xclef{at: cur, staff: atoiDef(c.attr("number"), 1), clef: clefName(c)})
			continue
		}
		if xm.clefs == nil {
			xm.clefs = map[int]string{}
		}
		xm.clefs[atoiDef(c.attr("number"), 1)] = clefName(c)
	}
	if tr := a.child("transpose"); tr != nil {
		iv := transposeOf(tr)
		xm.transpose = &iv
	}
}

// meterOf reads a <time>: "3+2+2"/8, a composite 2/4+3/8 written as two
// pairs (made one additive meter over the smaller unit), C and C|.
func meterOf(t *node) (score.Meter, bool) {
	beats, types := t.children("beats"), t.children("beat-type")
	if len(beats) == 0 || len(beats) != len(types) {
		return score.Meter{}, false
	}
	den := 0
	for _, bt := range types {
		d := atoiDef(bt.text, 0)
		if d <= 0 || d&(d-1) != 0 {
			return score.Meter{}, false
		}
		den = max(den, d)
	}
	var m score.Meter
	m.Den = den
	for i, b := range beats {
		scale := den / atoiDef(types[i].text, 1)
		for _, g := range strings.Split(strings.TrimSpace(b.text), "+") {
			n := atoiDef(g, 0)
			if n <= 0 {
				return score.Meter{}, false
			}
			m.Groups = append(m.Groups, n*scale)
		}
	}
	switch t.attr("symbol") {
	case "common":
		if m.Num() == 4 && m.Den == 4 {
			m.Symbol = "common"
		}
	case "cut":
		if m.Num() == 2 && m.Den == 2 {
			m.Symbol = "cut"
		}
	}
	return m, true
}

func (r *partReader) readNote(k *node) xnote {
	n := xnote{staff: max(1, k.integer("staff", 1)), voice: k.str("voice"), chord: k.has("chord"), unpitched: -1}
	if n.voice == "" {
		n.voice = "1"
	}
	if g := k.child("grace"); g != nil {
		n.grace = score.Appoggiatura
		if g.attr("slash") == "yes" {
			n.grace = score.Acciaccatura
		}
		switch {
		case g.attr("steal-time-previous") != "":
			n.timed = "before_beat"
		case g.attr("steal-time-following") != "":
			n.timed = "on_beat"
		}
	}
	if r.ours {
		switch k.str("stem") {
		case "up", "down":
			n.stem = k.str("stem")
		}
		if d := k.attr("dynamics"); d != "" {
			if v, err := strconv.ParseFloat(d, 64); err == nil && v > 0 {
				n.vel = max(1, min(127, int(math.Round(v*90/100))))
			}
		}
	}
	n.dur = r.frac(k.num("duration", 0))
	if rest := k.child("rest"); rest != nil {
		n.rest = true
		n.barRest = rest.attr("measure") == "yes"
		if k.attr("print-object") == "no" {
			n.hidden = true
		}
		if rest.has("display-step") {
			n.pitch = pitchOf(rest.str("display-step"), 0, rest.integer("display-octave", 4))
		}
	}
	if k.has("cue") {
		n.hidden = true
	}
	if p := k.child("pitch"); p != nil {
		alter := p.num("alter", 0)
		if alter != math.Round(alter) {
			r.warn("microtonal alteration %g rounded", alter)
		}
		n.pitch = pitchOf(p.str("step"), int(math.Round(alter)), p.integer("octave", 4))
	}
	if u := k.child("unpitched"); u != nil {
		n.pitch = pitchOf(u.str("display-step"), 0, u.integer("display-octave", 4))
		n.unpitched = n.pitch.MIDI()
		n.inst = k.child("instrument").attr("id")
	}
	dots := len(k.children("dot"))
	if d, ok := durOf(k.str("type"), dots); ok {
		n.written, n.typed = d, true
	}
	if tm := k.child("time-modification"); tm != nil {
		n.timeMod = [2]int{tm.integer("actual-notes", 1), tm.integer("normal-notes", 1)}
		if nt := tm.str("normal-type"); nt != "" {
			if d, ok := durOf(nt, len(tm.children("normal-dot"))); ok {
				n.normalUnit = d.Value()
			}
		}
	}
	if acc := k.child("accidental"); acc != nil {
		n.accWritten = true
		if acc.attr("cautionary") == "yes" || acc.attr("parentheses") == "yes" || acc.attr("editorial") == "yes" || acc.attr("bracket") == "yes" {
			n.acc = score.AccCautionary
		}
	}
	for _, t := range k.children("tie") {
		if t.attr("type") == "start" {
			n.tie = true
		}
	}
	for _, nt := range k.children("notations") {
		r.readNotations(nt, &n)
	}
	if !n.rest {
		for i, ly := range k.children("lyric") {
			if m, ok := readLyric(ly, i); ok {
				n.marks = append(n.marks, m)
			}
		}
	}
	if n.stem != "" {
		n.marks = append(n.marks, score.Mark{Name: n.stem})
	}
	if n.timed != "" && n.grace != score.NotGrace {
		n.marks = append(n.marks, score.Mark{Name: n.timed})
	}
	if n.vel > 0 && !n.rest {
		n.marks = append(n.marks, score.Mark{Name: "vel", Value: strconv.Itoa(n.vel)})
	}
	return n
}

func pitchOf(step string, alter, octave int) score.Pitch {
	s := 0
	if t := strings.ToUpper(strings.TrimSpace(step)); t != "" {
		s = max(0, strings.IndexByte(score.Steps, t[0]))
	}
	return score.Pitch{Step: int8(s), Alter: int8(max(-2, min(2, alter))), Octave: int8(max(-1, min(9, octave)))}
}

var articulationMarks = map[string]string{
	"staccato": "stacc", "staccatissimo": "staccmo", "spiccato": "staccmo", "accent": "acc",
	"strong-accent": "marc", "tenuto": "ten", "detached-legato": "portato",
}

// jazzMarks read the jazz slides.
var jazzMarks = map[string]string{"scoop": "scoop", "plop": "plop", "doit": "doit", "falloff": "fall"}

var ornamentMarks = map[string]string{
	"trill-mark": "tr", "mordent": "mord", "inverted-mordent": "imord", "turn": "turn",
	"inverted-turn": "iturn", "delayed-turn": "turn", "shake": "tr",
}

// fermataValues are a fermata's shapes as the lengths a score gives them.
var fermataValues = map[string]string{
	"": "", "normal": "", "angled": "short", "double-angled": "short", "half-curve": "short",
	"square": "long", "double-square": "verylong", "curlew": "verylong", "double-dot": "long",
}

// accAlter is an accidental-mark's (or accidental's) name as semitones.
var accAlter = map[string]int{
	"sharp": 1, "double-sharp": 2, "sharp-sharp": 2, "flat": -1, "flat-flat": -2, "natural": 0,
}

func accValue(a int) string {
	switch a {
	case 1:
		return "#"
	case 2:
		return "x"
	case -1:
		return "b"
	case -2:
		return "bb"
	}
	return "n"
}

// readOrnaments reads <ornaments>: the ornaments with the accidentals
// marked on their auxiliary notes (a trill's "tr=#", a turn's "turn=b/#"),
// tremolos (single, and the start of a two-note one). Three strokes are
// the plain mark: an unmeasured tremolo either way.
func (r *partReader) readOrnaments(k *node, add func(name, value string)) {
	var last string // the ornament the accidental marks follow
	var above, below string
	flush := func() {
		if last == "" {
			return
		}
		v := ""
		switch {
		case last == "mord" && below != "":
			v = below
		case last == "turn" || last == "iturn":
			if above != "" || below != "" {
				v = above
				if below != "" {
					v += "/" + below
				}
			}
		default:
			v = above
		}
		add(last, v)
		last, above, below = "", "", ""
	}
	for _, o := range k.kids {
		if m, ok := ornamentMarks[o.name]; ok {
			flush()
			last = m
			continue
		}
		switch o.name {
		case "accidental-mark":
			a, ok := accAlter[strings.TrimSpace(o.text)]
			if !ok || last == "" {
				continue
			}
			if o.attr("placement") == "below" {
				below = accValue(a)
			} else {
				above = accValue(a)
			}
		case "tremolo":
			strokes := strings.TrimSpace(o.text)
			if strokes == "3" {
				strokes = ""
			}
			switch o.attr("type") {
			case "start":
				add("trem2", strokes)
			case "stop":
			default:
				add("trem", strokes)
			}
		}
	}
	flush()
}

func (r *partReader) readNotations(nt *node, n *xnote) {
	add := func(name, value string) {
		for _, m := range n.marks {
			if m.Name == name && m.Value == value {
				return
			}
		}
		n.marks = append(n.marks, score.Mark{Name: name, Value: value})
	}
	for _, k := range nt.kids {
		switch k.name {
		case "tied":
			if k.attr("type") == "start" {
				n.tie = true
			}
		case "tuplet":
			switch k.attr("type") {
			case "start":
				t := xtuplet{number: k.attr("number")}
				if ta := k.child("tuplet-actual"); ta != nil {
					t.num = ta.integer("tuplet-number", 0)
				}
				if tn := k.child("tuplet-normal"); tn != nil {
					t.den = tn.integer("tuplet-number", 0)
				}
				n.tupStart = append(n.tupStart, t)
			case "stop":
				n.tupStop = append(n.tupStop, k.attr("number"))
			}
		case "articulations":
			for _, a := range k.kids {
				if m, ok := articulationMarks[a.name]; ok {
					add(m, "")
				}
				if a.name == "other-articulation" && strings.EqualFold(strings.TrimSpace(a.text), "legato") {
					add("legato", "")
				}
				if m, ok := jazzMarks[a.name]; ok {
					v := ""
					if (m == "doit" || m == "fall") && a.attr("line-length") == "long" {
						v = "long"
					}
					add(m, v)
				}
			}
		case "ornaments":
			r.readOrnaments(k, add)
		case "fermata":
			add("ferm", fermataValues[strings.TrimSpace(k.text)])
		case "arpeggiate":
			v := ""
			switch k.attr("direction") {
			case "down":
				v = "down"
			case "up":
				if r.ours {
					v = "up"
				}
			}
			add("arp", v)
		case "slur":
			switch k.attr("type") {
			case "start":
				add("slur", "")
			case "stop":
				add("slur_end", "")
			}
		case "glissando", "slide":
			if k.attr("type") == "start" {
				v := ""
				if k.attr("line-type") == "wavy" {
					v = "wavy"
				}
				add("gliss", v)
			}
		case "dynamics":
			for _, m := range dynamicMarks(k) {
				add(m.Name, m.Value)
			}
		case "technical":
			for _, t := range k.kids {
				switch t.name {
				case "stopped":
					add("stopped", "")
				case "open":
					add("open", "")
				case "brass-bend":
					add("bend", "")
				case "string":
					if v := atoiDef(strings.TrimSpace(t.text), 0); v >= 1 && v <= score.MaxStrings {
						n.str = v
					}
				}
			}
		}
	}
}

func (r *partReader) readDirection(d *node, cur score.Frac) xdir {
	x := xdir{at: cur.Add(r.frac(d.num("offset", 0))), staff: max(1, d.integer("staff", 1)), voice: d.str("voice")}
	if x.at.Sign() < 0 {
		x.at = score.Frac{}
	}
	var words []string
	italic, bold, others := false, false, false
	var metro *score.TempoMark
	var dashStart, dashStop []string // dashes' numbers
	for _, dt := range d.children("direction-type") {
		for _, k := range dt.kids {
			switch k.name {
			case "words":
				if t := cleanText(k.text); t != "" {
					words = append(words, t)
					italic = italic || k.attr("font-style") == "italic"
					bold = bold || k.attr("font-weight") == "bold"
				}
			case "dynamics":
				x.marks = append(x.marks, dynamicMarks(k)...)
				others = true
			case "metronome":
				units := k.children("beat-unit")
				pm := k.str("per-minute")
				if len(units) != 1 || pm == "" {
					continue
				}
				bpm := leadingNumber(pm)
				u, ok := durOf(units[0].text, len(k.children("beat-unit-dot")))
				if bpm > 0 && ok {
					metro = &score.TempoMark{BPM: bpm, Unit: u}
				}
			case "rehearsal":
				x.rehearsal = cleanText(k.text)
			case "segno":
				x.jumps = append(x.jumps, xjump{kind: "segno"})
			case "coda":
				x.jumps = append(x.jumps, xjump{kind: "coda"})
			case "dashes":
				switch k.attr("type") {
				case "start":
					dashStart = append(dashStart, k.attr("number"))
				case "stop":
					dashStop = append(dashStop, k.attr("number"))
				}
			case "wedge":
				others = true
				switch k.attr("type") {
				case "crescendo":
					x.marks = append(x.marks, score.Mark{Name: "cresc"})
				case "diminuendo":
					x.marks = append(x.marks, score.Mark{Name: "dim"})
				case "stop":
					x.stops = append(x.stops, score.Mark{Name: "hairpin_end"})
				}
			case "octave-shift":
				others = true
				size := atoiDef(k.attr("size"), 8)
				switch k.attr("type") {
				case "down": // the notes are shown lower: 8va
					x.marks = append(x.marks, score.Mark{Name: map[bool]string{true: "15ma", false: "8va"}[size >= 15]})
				case "up":
					x.marks = append(x.marks, score.Mark{Name: map[bool]string{true: "15mb", false: "8vb"}[size >= 15]})
				case "stop":
					x.stops = append(x.stops, score.Mark{Name: "ottava_end"})
				}
			case "pedal":
				others = true
				switch k.attr("type") {
				case "start", "change", "resume":
					// A change is a re-pedal: the line goes up and down
					// again there, which a pedal mark on a held pedal is.
					x.marks = append(x.marks, score.Mark{Name: "ped"})
				case "stop", "discontinue":
					x.stops = append(x.stops, score.Mark{Name: "ped_end"})
				}
			}
		}
	}
	text := strings.Join(words, " ")
	snd := d.child("sound")
	if snd != nil {
		x.jumps = append(x.jumps, soundJumps(snd, text)...)
	}
	if len(x.jumps) == 0 && text != "" {
		if k := wordJump(text); k != "" {
			x.jumps = append(x.jumps, xjump{kind: k, words: text})
		}
	}
	if metro == nil && snd != nil && snd.attr("tempo") != "" {
		if bpm, err := strconv.ParseFloat(snd.attr("tempo"), 64); err == nil && bpm > 0 {
			metro = &score.TempoMark{BPM: bpm, Unit: score.Duration{Base: score.Quarter}}
		}
	}
	if r.dashes == nil {
		r.dashes = map[string]string{}
	}
	// Tempo text with no number: a change (rit., a tempo) wherever it is,
	// or bold words above the first part, as notation programs write
	// tempo text.
	if metro == nil && text != "" && len(x.jumps) == 0 && !others {
		if (score.TempoMark{Text: text}).Change() != score.TempoSet ||
			r.first && bold && !italic && d.attr("placement") != "below" {
			metro = &score.TempoMark{}
		}
	}
	// A Foundry export says which of its three kinds of text a direction
	// is in its id.
	kind := ""
	if r.ours {
		id := d.attr("id")
		for _, k := range []string{"text", "tech", "expr"} {
			if strings.HasSuffix(id, "-"+k) {
				kind = k
			}
		}
	}
	switch {
	case kind != "" && metro == nil && len(dashStart) == 0:
		for _, w := range words {
			x.marks = append(x.marks, score.Mark{Name: kind, Value: w})
		}
	case metro != nil:
		// Words with a tempo are the tempo's text ("Allegro").
		metro.Text = text
		metro.BPM = math.Round(metro.BPM*1000) / 1000
		if len(dashStart) > 0 && metro.Change().Gradual() {
			metro.HasTo = true
			r.dashes[dashStart[0]] = "tempo"
		}
		x.tempo = metro
	case len(x.jumps) > 0:
		// The words are the jump's.
	case len(dashStart) > 0:
		// Words with dashes: a crescendo or diminuendo written out.
		x.marks = append(x.marks, textHairpin(text))
		r.dashes[dashStart[0]] = "hairpin"
	default:
		for _, w := range words {
			switch {
			case italic && d.attr("placement") == "below":
				x.marks = append(x.marks, score.Mark{Name: "expr", Value: w})
			default:
				x.marks = append(x.marks, wordMark(w))
			}
		}
	}
	for _, n := range dashStop {
		switch r.dashes[n] {
		case "tempo":
			x.tempoTo = true
		default:
			x.stops = append(x.stops, score.Mark{Name: "hairpin_end"})
		}
		delete(r.dashes, n)
	}
	return x
}

// textHairpin is a crescendo or diminuendo written as words with dashes:
// "cresc." and "dim." are the plain text marks, other words the mark's
// text ("poco a poco cresc.").
func textHairpin(text string) score.Mark {
	l := strings.ToLower(strings.TrimSpace(text))
	switch l {
	case "cresc.", "cresc", "crescendo":
		return score.Mark{Name: "cresc", Value: "text"}
	case "dim.", "dim", "diminuendo", "decresc.", "decresc", "decrescendo":
		return score.Mark{Name: "dim", Value: "text"}
	}
	if strings.Contains(l, "dim") || strings.Contains(l, "decresc") {
		return score.Mark{Name: "dim", Value: text}
	}
	if text == "" {
		text = "text"
	}
	return score.Mark{Name: "cresc", Value: text}
}

var numberRE = regexp.MustCompile(`[0-9]+(\.[0-9]+)?`)

func leadingNumber(s string) float64 {
	v, _ := strconv.ParseFloat(numberRE.FindString(s), 64)
	return v
}

// ---------- the score ----------

// Import makes a score of a document. The warnings are what it could not
// carry over.
func Import(d *Document, o Options) (s *score.Score, warnings []string, err error) {
	defer score.RecoverOverflow(&err)
	seen := map[string]bool{}
	warn := func(format string, a ...any) {
		msg := fmt.Sprintf(format, a...)
		if !seen[msg] {
			seen[msg] = true
			warnings = append(warnings, msg)
		}
	}
	parts := d.root.children("part")
	if len(parts) == 0 {
		return nil, nil, fmt.Errorf("musicxml: the score has no parts")
	}
	heads := d.heads()
	infos := d.Parts()
	read := make([][]xmeasure, len(parts))
	nbars := 0
	ours := strings.HasPrefix(d.Software, Software)
	for i, p := range parts {
		pr := &partReader{warn: warn, ours: ours, first: i == 0}
		read[i] = pr.readPart(p)
		nbars = max(nbars, len(read[i]))
	}
	if nbars == 0 {
		return nil, nil, fmt.Errorf("musicxml: the score has no measures")
	}
	if nbars > score.MaxBars {
		return nil, nil, fmt.Errorf("musicxml: %d measures (a score holds at most %d)", nbars, score.MaxBars)
	}
	for i, m := range read {
		for b, xm := range m {
			if score.MaxBarLength.Less(xm.length) {
				return nil, nil, fmt.Errorf("musicxml: part %s, measure %d is %s whole notes long (at most %s)", infos[i].Name, b+1, xm.length, score.MaxBarLength)
			}
		}
	}

	name := o.Name
	if name == "" {
		name = "imported"
	}
	s = score.New(name)
	s.Title = o.Title
	if s.Title == "" {
		s.Title = cleanText(d.root.child("work").str("work-title"))
	}
	if s.Title == "" {
		s.Title = cleanText(d.root.str("movement-title"))
	}
	if s.Title == "" {
		s.Title = name
	}
	for _, c := range d.root.child("identification").children("creator") {
		if c.attr("type") == "composer" || s.Composer == "" && c.attr("type") == "" {
			s.Composer = cleanText(c.text)
		}
	}
	for _, cr := range d.root.children("credit") {
		text := cleanText(cr.str("credit-words"))
		switch cr.str("credit-type") {
		case "subtitle":
			s.Subtitle = text
		case "composer":
			if s.Composer == "" {
				s.Composer = text
			}
		case "title":
			if s.Title == "" || s.Title == name {
				s.Title = text
			}
		}
	}
	if s.Subtitle == "" && d.root.child("work").str("work-title") != "" {
		// A movement title beside a work title is the subtitle.
		if mt := cleanText(d.root.str("movement-title")); mt != s.Title {
			s.Subtitle = mt
		}
	}
	s.Bars = nbars
	if !o.NoLayout {
		readLayout(d.root.child("defaults"), &s.Layout)
	}

	ref := referencePart(read, infos)
	buildMaps(s, read, ref, infos, warn)
	if !o.NoLayout {
		for b, xm := range read[ref] {
			if b > 0 && b < s.Bars && (xm.newSystem || xm.newPage) {
				s.Layout.Breaks = append(s.Layout.Breaks, score.Break{Bar: b + 1, Page: xm.newPage})
			}
		}
	}
	lay := notate.LayoutOf(s)

	for pi, p := range parts {
		var ch Choice
		if pi < len(o.Choices) {
			ch = o.Choices[pi]
		}
		if ch.Skip {
			continue
		}
		h := heads[p.attr("id")]
		if h == nil {
			h = &partHead{id: p.attr("id"), program: -1, channel: -1, unpitched: map[string]int{}}
		}
		l, err := buildLine(read[pi], infos[pi], h, ch, s, lay, ours, warn)
		if err != nil {
			return nil, warnings, fmt.Errorf("part %s: %w", infos[pi].Name, err)
		}
		s.Lines = append(s.Lines, l)
	}
	if len(s.Lines) == 0 {
		return nil, warnings, fmt.Errorf("musicxml: every part was skipped")
	}
	uniqueLineNames(s)
	if ours {
		looseStems(s)
	}
	for _, is := range s.Validate(nil) {
		if is.Severity == score.Error {
			warn("%s", is)
		}
	}
	return s, warnings, nil
}

func uniqueLineNames(s *score.Score) {
	count := map[string]int{}
	for _, l := range s.Lines {
		count[strings.ToLower(l.Name)]++
	}
	n := map[string]int{}
	for _, l := range s.Lines {
		k := strings.ToLower(l.Name)
		if count[k] > 1 {
			n[k]++
			l.Name = fmt.Sprintf("%s %d", l.Name, n[k])
		}
	}
}

// referencePart is the part whose key and meter the score takes: the first
// that is not transposed (its key is the concert key), else the first.
func referencePart(read [][]xmeasure, infos []PartInfo) int {
	for i, info := range infos {
		if info.Transpose == (score.Interval{}) && !info.Percussion && len(read[i]) > 0 {
			return i
		}
	}
	return 0
}

// buildMaps sets the bars, meters, keys, tempo marks, rehearsal marks and
// repeats.
func buildMaps(s *score.Score, read [][]xmeasure, ref int, infos []PartInfo, warn func(string, ...any)) {
	n := s.Bars
	meters := make([]score.Meter, n)
	cur := score.FourFour()
	for b := 0; b < n; b++ {
		if b < len(read[ref]) && read[ref][b].meter != nil {
			cur = *read[ref][b].meter
		} else {
			for _, m := range read {
				if b < len(m) && m[b].meter != nil {
					cur = *m[b].meter
					break
				}
			}
		}
		meters[b] = cur
	}
	// Bar lengths: how far the fullest part got.
	for b := 0; b < n; b++ {
		var length score.Frac
		implicit := false
		for _, m := range read {
			if b < len(m) {
				length = score.MaxFrac(length, m[b].length)
				implicit = implicit || m[b].implicit || m[b].number == "0"
			}
		}
		full := meters[b].Length()
		switch {
		case length.IsZero() || length.Eq(full):
		case b == 0 && implicit && length.Less(full):
			s.Pickup = length
		case full.Less(length) || implicit:
			if m, ok := meterForLength(length, meters[b]); ok {
				meters[b] = m
				warn("bar %d is %s long, not %s: given a meter of its own (%s)", b+1, length, full, m)
			}
		}
	}
	s.Meter = nil
	for b, m := range meters {
		if len(s.Meter) == 0 || !s.Meter[len(s.Meter)-1].Meter.Eq(m) {
			s.Meter = append(s.Meter, score.MeterMark{Bar: b + 1, Meter: m})
		}
	}
	// Keys: the reference part's, moved to concert pitch.
	shift := fifthsShift(infos[ref].Transpose)
	s.Key = []score.KeyMark{{Bar: 1, Mode: "major"}}
	for b := 0; b < len(read[ref]) && b < n; b++ {
		if k := read[ref][b].key; k != nil {
			km := score.KeyMark{Bar: b + 1, Fifths: k.Fifths + shift, Mode: k.Mode}
			for km.Fifths > 7 {
				km.Fifths -= 12
			}
			for km.Fifths < -7 {
				km.Fifths += 12
			}
			last := &s.Key[len(s.Key)-1]
			switch {
			case last.Bar == km.Bar:
				*last = km
			case last.Fifths != km.Fifths || last.Mode != km.Mode:
				s.Key = append(s.Key, km)
			}
		}
	}
	// Tempo marks and rehearsal marks from every part, the first at a
	// place winning.
	lay := notate.LayoutOf(s)
	s.Tempo = nil
	tempoAt := map[string]bool{}
	sections := map[int]bool{}
	names := map[string]int{}
	for _, m := range read {
		gradual := -1 // this part's open rit. or accel., into s.Tempo
		for b := 0; b < len(m) && b < n; b++ {
			for _, dr := range m[b].dirs {
				pos := score.Pos{Bar: b + 1, Offset: dr.at}
				if bl := lay.BarLen(b); !pos.Offset.Less(bl) {
					pos.Offset = score.Frac{}
					if b+1 < n {
						pos.Bar++
					}
				}
				if dr.tempoTo && gradual >= 0 {
					s.Tempo[gradual].To = score.Pos{Bar: b + 1, Offset: dr.at}
					if bl := lay.BarLen(b); !dr.at.Less(bl) {
						s.Tempo[gradual].To = score.Pos{Bar: b + 2}
					}
					gradual = -1
				}
				if key := s.FormatPos(pos); dr.tempo != nil && !tempoAt[key] {
					tempoAt[key] = true
					t := *dr.tempo
					t.At = pos
					s.Tempo = append(s.Tempo, t)
					if t.HasTo {
						gradual = len(s.Tempo) - 1
					}
				}
				if dr.rehearsal != "" && !sections[pos.Bar] {
					sections[pos.Bar] = true
					name := dr.rehearsal
					names[name]++
					if names[name] > 1 {
						name = fmt.Sprintf("%s %d", name, names[name])
					}
					s.Sections = append(s.Sections, score.Section{Name: name, Bar: pos.Bar})
				}
			}
		}
	}
	for i := range s.Tempo {
		// Dashes that never stopped: the change runs to the next mark.
		if t := &s.Tempo[i]; t.HasTo && t.To == (score.Pos{}) {
			t.HasTo = false
		}
	}
	sort.SliceStable(s.Tempo, func(i, j int) bool { return score.PosLess(s.Tempo[i].At, s.Tempo[j].At) })
	if len(s.Tempo) == 0 || s.Tempo[0].At.Bar != 1 || !s.Tempo[0].At.Offset.IsZero() {
		// A score starts at a tempo: the first mark's, or 120.
		first := score.TempoMark{At: score.Pos{Bar: 1}, BPM: 120, Unit: score.Duration{Base: score.Quarter}}
		if len(s.Tempo) > 0 {
			first.BPM, first.Unit = s.Tempo[0].BPM, s.Tempo[0].Unit
		}
		s.Tempo = append([]score.TempoMark{first}, s.Tempo...)
	}
	// Repeats, endings and jumps from the reference part's bar lines and
	// directions (and the first part's, where the jumps usually are).
	open := 0
	openEnding := -1
	for b, xm := range read[ref] {
		if b >= n {
			break
		}
		if xm.repeatStart {
			open = b + 1
		}
		if xm.hasEnding {
			s.Voltas = append(s.Voltas, score.Volta{Start: b + 1, End: b + 1, Passes: passesOf(xm.endingStart, len(s.Voltas)+1)})
			openEnding = len(s.Voltas) - 1
		}
		if xm.endingStop {
			if openEnding >= 0 {
				s.Voltas[openEnding].End = b + 1
			} else {
				s.Voltas = append(s.Voltas, score.Volta{Start: b + 1, End: b + 1, Passes: passesOf(xm.endingStart, len(s.Voltas)+1)})
			}
			openEnding = -1
		} else if openEnding >= 0 {
			s.Voltas[openEnding].End = b + 1
		}
		if xm.repeatEnd {
			start := open
			if start == 0 {
				start = 1
			}
			if k := len(s.Repeats); k > 0 && start <= s.Repeats[k-1].End {
				warn("repeat ending at bar %d overlaps the one before; it is left out", b+1)
			} else {
				s.Repeats = append(s.Repeats, score.Repeat{Start: start, End: b + 1, Times: min(xm.repeatTimes, 16)})
			}
			open = 0
		}
	}
	jumpParts := []int{ref}
	if ref != 0 {
		jumpParts = append(jumpParts, 0)
	}
	seenJump := map[[2]int]bool{}
	for _, pi := range jumpParts {
		for b, xm := range read[pi] {
			if b >= n {
				break
			}
			js := append([]xjump(nil), xm.jumps...)
			for _, dr := range xm.dirs {
				js = append(js, dr.jumps...)
			}
			for _, j := range js {
				jm := jumpOf(j, b+1)
				if jm.Kind == 0 || seenJump[[2]int{int(jm.Kind), jm.Bar}] {
					continue
				}
				seenJump[[2]int{int(jm.Kind), jm.Bar}] = true
				s.Jumps = append(s.Jumps, jm)
			}
		}
	}
	s.SortMaps()
}

// passesOf reads an ending's numbers ("1", "1, 2", "1-3", "1. 2."); with
// none it is the k'th ending.
func passesOf(t string, k int) []int {
	var out []int
	for _, f := range strings.FieldsFunc(t, func(r rune) bool { return r == ',' || r == ' ' || r == '.' }) {
		if a, b, ok := strings.Cut(f, "-"); ok {
			x, y := atoiDef(a, 0), atoiDef(b, 0)
			for p := x; p >= 1 && p <= y && p <= 16; p++ {
				out = append(out, p)
			}
			continue
		}
		if p := atoiDef(f, 0); p >= 1 && p <= 16 {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		out = []int{max(1, min(k, 16))}
	}
	return out
}

// jumpOf is a form mark as a score's jump at a bar.
func jumpOf(j xjump, bar int) score.Jump {
	jm := score.Jump{Bar: bar}
	switch j.kind {
	case "segno":
		jm.Kind = score.JumpSegno
	case "coda":
		jm.Kind = score.JumpCoda
	case "tocoda":
		jm.Kind = score.JumpToCoda
	case "fine":
		jm.Kind = score.JumpFine
	case "dacapo":
		jm.Kind = score.JumpDC
	case "dalsegno":
		jm.Kind = score.JumpDS
	}
	if jm.Kind == score.JumpDC || jm.Kind == score.JumpDS {
		l := strings.ToLower(j.words)
		switch {
		case strings.Contains(l, "al fine"):
			jm.Al = score.JumpFine
		case strings.Contains(l, "al coda"):
			jm.Al = score.JumpCoda
		}
		jm.Repeats = strings.Contains(l, "con rip")
	}
	return jm
}

// readLayout reads <defaults>: the staff size from the scaling, the paper
// and its margins. Values that are the score's defaults are left at zero,
// as a score with no layout has them.
func readLayout(df *node, lay *score.Layout) {
	sc := df.child("scaling")
	if sc == nil {
		return
	}
	mm, tenths := sc.num("millimeters", 0), sc.num("tenths", 0)
	if mm <= 0 || tenths <= 0 {
		return
	}
	staff := math.Round(mm*40/tenths*100) / 100
	if staff >= score.MinStaff && staff <= score.MaxStaff && math.Abs(staff-score.DefaultStaff) > 0.005 {
		lay.Staff = staff
	}
	toMM := func(t float64) float64 { return math.Round(t*mm/tenths*100) / 100 }
	pl := df.child("page-layout")
	if pl == nil {
		return
	}
	w, h := toMM(pl.num("page-width", 0)), toMM(pl.num("page-height", 0))
	if w > 50 && h > 50 && w < 2000 && h < 2000 {
		lay.Paper = score.Paper{Landscape: w > h}
		pw, ph := w, h
		if pw > ph {
			pw, ph = ph, pw
		}
		for _, name := range score.PaperNames {
			if sz, ok := score.PaperSizes[name]; ok && math.Abs(sz[0]-pw) < 0.6 && math.Abs(sz[1]-ph) < 0.6 {
				lay.Paper.Name = name
			}
		}
		if lay.Paper.Name == "" {
			lay.Paper = score.Paper{Name: "custom", Width: pw, Height: ph, Landscape: w > h}
		}
		if lay.Paper.Name == score.DefaultPaper && !lay.Paper.Landscape {
			lay.Paper.Name = ""
		}
	}
	for _, pm := range pl.children("page-margins") {
		if t := pm.attr("type"); t != "" && t != "both" && t != "odd" {
			continue
		}
		m := score.Margins{Top: toMM(pm.num("top-margin", 0)), Bottom: toMM(pm.num("bottom-margin", 0)),
			Left: toMM(pm.num("left-margin", 0)), Right: toMM(pm.num("right-margin", 0))}
		def := score.DefaultMargin
		if math.Abs(m.Top-def) < 0.02 && math.Abs(m.Bottom-def) < 0.02 && math.Abs(m.Left-def) < 0.02 && math.Abs(m.Right-def) < 0.02 {
			m = score.Margins{}
		}
		if m.Top >= 0 && m.Bottom >= 0 && m.Left >= 0 && m.Right >= 0 && m.Left+m.Right < w && m.Top+m.Bottom < h {
			lay.Paper.Margins = m
		}
		break
	}
}

// meterForLength is a meter that is a bar of a length, in the denominator
// of the meter it replaces (or finer, or coarser past a numerator of 64).
func meterForLength(length score.Frac, like score.Meter) (score.Meter, bool) {
	den := length.Den
	if den&(den-1) != 0 || den > 128 {
		return score.Meter{}, false
	}
	num := length.Num
	for den < int64(like.Den) {
		num, den = num*2, den*2
	}
	// A meter's numerator runs to 64: a longer bar counts in longer
	// notes, and failing that in even additive groups (129/128 as
	// 43+43+43/128).
	for num > 64 && num%2 == 0 && den > 1 {
		num, den = num/2, den/2
	}
	if num > 64*64 {
		return score.Meter{}, false
	}
	k := (num + 63) / 64
	groups := make([]int, k)
	for i := range groups {
		groups[i] = int(num / k)
		if int64(i) < num%k {
			groups[i]++
		}
	}
	return score.Meter{Groups: groups, Den: int(den)}, true
}

// voiceKey is a voice as the score has it: a staff and an index.
type voiceKey struct{ staff, voice int }

// voiceMap assigns each MusicXML (staff, voice) a score staff and voice:
// a voice number lives on the staff most of its notes are on, unless a
// quarter or more of them are on another, which then has its own voice of
// that number. Voices on a staff are numbered in order (lowest first);
// past four are dropped. In a file of ours a voice is always its staff's:
// its notes on another staff are cross-staff notes.
func voiceMap(ms []xmeasure, staves int, ours bool, warn func(string, ...any)) map[[2]string]voiceKey {
	counts := map[string]map[int]int{}
	for _, xm := range ms {
		for _, n := range xm.notes {
			if counts[n.voice] == nil {
				counts[n.voice] = map[int]int{}
			}
			counts[n.voice][min(n.staff, staves)]++
		}
	}
	home := map[[2]string]int{} // (voice, staff) → staff it lives on
	perStaff := map[int][]string{}
	for v, byStaff := range counts {
		total, best, bestN := 0, 1, -1
		for st, c := range byStaff {
			total += c
			if c > bestN || c == bestN && st < best {
				best, bestN = st, c
			}
		}
		split := func(c int) bool { return !ours && c*4 >= total }
		for st, c := range byStaff {
			target := best
			if split(c) {
				target = st
			}
			home[[2]string{v, strconv.Itoa(st)}] = target
		}
		for st, c := range byStaff {
			if st == best || split(c) {
				perStaff[st] = appendUnique(perStaff[st], v)
			}
		}
	}
	out := map[[2]string]voiceKey{}
	for st, vs := range perStaff {
		sort.Slice(vs, func(i, j int) bool {
			a, ea := strconv.Atoi(vs[i])
			b, eb := strconv.Atoi(vs[j])
			if ea == nil && eb == nil {
				return a < b
			}
			return vs[i] < vs[j]
		})
		for i, v := range vs {
			if i >= score.MaxVoices {
				warn("staff %d has more than %d voices; voice %s is left out", st, score.MaxVoices, v)
				continue
			}
			for k, target := range home {
				if k[0] == v && target == st {
					out[k] = voiceKey{st, i}
				}
			}
		}
	}
	return out
}

func appendUnique(xs []string, x string) []string {
	for _, v := range xs {
		if v == x {
			return xs
		}
	}
	return append(xs, x)
}

// placed is an event of a voice's bar with where it starts.
type placed struct {
	at score.Frac
	ev score.Event
}

// voiceBar collects one voice's bar as it is read.
type voiceBar struct {
	events  []placed
	tuplets []score.Tuplet
	open    []openTuplet // innermost last
	// implicitWritten sums an implicit tuplet's written values.
	implicit bool
}

type openTuplet struct {
	index  int // 1-based into tuplets
	number string
	ratio  score.Frac // Den/Num of this level
}

func buildLine(ms []xmeasure, info PartInfo, h *partHead, ch Choice, s *score.Score, lay notate.Layout, ours bool, warn func(string, ...any)) (*score.Line, error) {
	name := info.Name
	if ch.Name != "" {
		name = ch.Name
	}
	inst := info.Instrument
	if ch.Instrument != "" {
		inst = ch.Instrument
	}
	l := score.NewLine(name, inst, "treble")
	l.Pan = h.pan
	if h.volume >= 0 {
		l.Volume = h.volume
	}
	if h.abbrev != "" && h.abbrev != score.AbbreviateName(name) && ch.Name == "" {
		l.Short = h.abbrev
	}
	staves := max(1, info.Staves)
	l.Staves = make([]*score.Staff, staves)
	for i := range l.Staves {
		l.Staves[i] = &score.Staff{Clef: info.Clefs[i], Transpose: info.Transpose}
	}
	vmap := voiceMap(ms, staves, ours, warn)
	nv := make([]int, staves)
	for _, k := range vmap {
		nv[k.staff-1] = max(nv[k.staff-1], k.voice+1)
	}
	for i, st := range l.Staves {
		st.Voices = make([]*score.Voice, max(1, nv[i]))
		for v := range st.Voices {
			st.Voices[v] = &score.Voice{Measures: make([]score.Measure, s.Bars)}
		}
	}
	clefNow := map[int]string{}
	for i, st := range l.Staves {
		clefNow[i+1] = st.Clef
	}
	transpose := info.Transpose
	var pending map[int][]score.Mark // marks carried to the next bar, by staff
	for b := 0; b < s.Bars; b++ {
		var xm xmeasure
		if b < len(ms) {
			xm = ms[b]
		}
		// Clef changes: at the bar's start, then inside it in order.
		for st := 1; st <= staves; st++ {
			if c, ok := xm.clefs[st]; ok && b > 0 && c != clefNow[st] {
				l.Staves[st-1].Clefs = append(l.Staves[st-1].Clefs, score.ClefChange{At: score.Pos{Bar: b + 1}, Clef: c})
				clefNow[st] = c
			}
		}
		mids := append([]xclef(nil), xm.clefMid...)
		sort.SliceStable(mids, func(i, j int) bool { return mids[i].at.Less(mids[j].at) })
		for _, c := range mids {
			st := min(max(c.staff, 1), staves)
			if c.clef == clefNow[st] {
				continue
			}
			at := score.Pos{Bar: b + 1, Offset: c.at}
			if !c.at.Less(lay.BarLen(b)) {
				if b+1 >= s.Bars {
					continue
				}
				at = score.Pos{Bar: b + 2}
			}
			l.Staves[st-1].Clefs = append(l.Staves[st-1].Clefs, score.ClefChange{At: at, Clef: c.clef})
			clefNow[st] = c.clef
		}
		if xm.transpose != nil && *xm.transpose != transpose {
			if b > 0 {
				warn("%s: a change of transposition at bar %d is not kept (the staff keeps %s)", name, b+1, transpose)
			}
		}
		barLen := lay.BarLen(b)
		bars := map[voiceKey]*voiceBar{}
		get := func(k voiceKey) *voiceBar {
			vb := bars[k]
			if vb == nil {
				vb = &voiceBar{}
				bars[k] = vb
			}
			return vb
		}
		accState := map[int]map[int]int{} // staff → diatonic → alteration in force
		keyWritten := s.KeyAt(b+1).Fifths - fifthsShift(transpose)
		var lastKey voiceKey
		haveLast := false
		for _, xn := range xm.notes {
			k, ok := vmap[[2]string{xn.voice, strconv.Itoa(min(xn.staff, staves))}]
			if !ok {
				continue
			}
			// A note of no length that is not a grace (some exporters
			// write one for an invisible item) takes no part.
			if !xn.chord && xn.grace == score.NotGrace && xn.dur.IsZero() {
				continue
			}
			if xn.chord && haveLast {
				k = lastKey
			}
			vb := get(k)
			// The note's sounding pitch and how its accidental shows.
			var note score.Note
			if !xn.rest {
				note = noteOf(xn, h, transpose, accState, keyWritten)
			}
			if xn.chord && haveLast && len(vb.events) > 0 {
				ev := &vb.events[len(vb.events)-1].ev
				if ev.Kind == score.EventChord && !xn.rest && !xn.hidden {
					strs := ev.TabStrings()
					ev.Notes = append(ev.Notes, note)
					ev.Marks = mergeMarks(ev.Marks, xn.marks)
					ev.SetTabStrings(append(strs, xn.str))
				}
				closeTuplets(vb, xn.tupStop)
				continue
			}
			lastKey, haveLast = k, true
			ev := score.Event{Kind: score.EventChord, Grace: xn.grace}
			switch {
			case xn.hidden:
				ev.Kind = score.EventSpace
			case xn.rest:
				ev.Kind = score.EventRest
				// A rest keeps what notation marks it carries: a fermata.
				for _, mk := range xn.marks {
					if mk.Name == "ferm" {
						ev.Marks = append(ev.Marks, mk)
					}
				}
			default:
				ev.Notes = []score.Note{note}
				ev.Marks = mergeMarks(nil, xn.marks)
				ev.SetTabStrings([]int{xn.str})
				// A note of the voice on the staff next to the voice's own:
				// a cross-staff note.
				if d := min(xn.staff, staves) - k.staff; d == 1 || d == -1 {
					ev.SetCrossStaff(d)
				}
			}
			if ev.Grace != score.NotGrace && ev.Kind != score.EventChord {
				continue // a grace rest is nothing
			}
			openTuplets(vb, xn)
			if len(vb.open) > 0 {
				ev.Tuplet = vb.open[len(vb.open)-1].index
			}
			ev.Dur = writtenDur(xn, vb, warn)
			vb.events = append(vb.events, placed{at: xn.at, ev: ev})
			if xn.barRest {
				vb.events[len(vb.events)-1].ev.Kind = score.EventBarRest
			}
			closeTuplets(vb, xn.tupStop)
			closeImplicit(vb, xn)
		}
		// Write each voice's bar: gaps as spaces, a lone whole-bar rest as
		// a bar rest, the first voice filled to the bar line.
		offsets := map[voiceKey][]score.Frac{}
		for k, vb := range bars {
			m, offs := settleBar(vb, barLen, s.MeterAt(b+1), k.voice == 0, name, b+1, warn)
			l.Staves[k.staff-1].Voices[k.voice].Measures[b] = m
			offsets[k] = offs
		}
		// Directions onto events, by staff.
		for st, marks := range pending {
			if attachAt(l, b, st, -1, score.Frac{}, marks, false, offsets) {
				delete(pending, st)
			}
		}
		for _, dr := range xm.dirs {
			st := min(dr.staff, staves)
			v := -1
			if dr.voice != "" {
				if k, ok := vmap[[2]string{dr.voice, strconv.Itoa(st)}]; ok && k.staff == st {
					v = k.voice
				}
			}
			if len(dr.stops) > 0 {
				attachAt(l, b, st, v, dr.at, dr.stops, true, offsets)
			}
			if len(dr.marks) > 0 && !attachAt(l, b, st, v, dr.at, dr.marks, false, offsets) {
				if pending == nil {
					pending = map[int][]score.Mark{}
				}
				pending[st] = append(pending[st], dr.marks...)
			}
		}
	}
	for _, st := range l.Staves {
		for _, v := range st.Voices {
			v.Trim()
		}
	}
	dropDanglingTies(l)
	readWords(l, ms, s, lay, warn)
	return l, nil
}

// noteOf is a <note>'s sounding pitch and accidental display. Written
// pitches are moved by the staff's interval to concert; an unpitched
// percussion note plays its instrument's MIDI key. An accidental the file
// prints where the key and the bar so far would not is kept as forced.
func noteOf(xn xnote, h *partHead, transpose score.Interval, acc map[int]map[int]int, keyWritten int) score.Note {
	n := score.Note{Tie: xn.tie, Acc: xn.acc}
	w := xn.pitch
	if acc[xn.staff] == nil {
		acc[xn.staff] = map[int]int{}
	}
	state := acc[xn.staff]
	implied, ok := state[w.Diatonic()]
	if !ok {
		implied = score.KeyAlter(keyWritten, int(w.Step))
	}
	if xn.accWritten && n.Acc == score.AccAuto && int(w.Alter) == implied {
		n.Acc = score.AccForce
	}
	state[w.Diatonic()] = int(w.Alter)
	if xn.unpitched != -1 {
		key := xn.unpitched
		if k, ok := h.unpitched[xn.inst]; ok {
			key = k
		}
		n.Pitch = score.SpellMIDI(key, 0, 1)
		n.Acc = score.AccAuto
		return n
	}
	n.Pitch = w.Transpose(transpose)
	return n
}

func mergeMarks(have, add []score.Mark) []score.Mark {
	for _, m := range add {
		dup := false
		for _, h := range have {
			if h == m {
				dup = true
				break
			}
		}
		if !dup {
			have = append(have, m)
		}
	}
	return have
}

// openTuplets opens the brackets a note starts, and an implicit one when
// it is in a time modification no bracket covers.
func openTuplets(vb *voiceBar, xn xnote) {
	product := func() score.Frac {
		p := score.Whole(1)
		for _, o := range vb.open {
			p = p.Mul(o.ratio)
		}
		return p
	}
	for _, ts := range xn.tupStart {
		if vb.implicit {
			vb.open, vb.implicit = vb.open[:len(vb.open)-1], false
		}
		num, den := ts.num, ts.den
		if num == 0 || den == 0 {
			// The level's ratio is the note's whole time modification over
			// the brackets already open.
			total := score.F(int64(max(1, xn.timeMod[1])), int64(max(1, xn.timeMod[0])))
			r := total.Div(product())
			num, den = int(r.Den), int(r.Num)
		}
		if num <= 1 && den <= 1 {
			num, den = 3, 2
		}
		parent := 0
		if len(vb.open) > 0 {
			parent = vb.open[len(vb.open)-1].index
		}
		vb.tuplets = append(vb.tuplets, score.Tuplet{Num: num, Den: den, Parent: parent})
		vb.open = append(vb.open, openTuplet{index: len(vb.tuplets), number: ts.number, ratio: score.F(int64(den), int64(num))})
	}
	if xn.timeMod[0] > 0 && xn.timeMod[0] != xn.timeMod[1] && len(vb.open) == 0 && xn.grace == score.NotGrace {
		vb.tuplets = append(vb.tuplets, score.Tuplet{Num: xn.timeMod[0], Den: xn.timeMod[1]})
		vb.open = append(vb.open, openTuplet{index: len(vb.tuplets), ratio: score.F(int64(xn.timeMod[1]), int64(xn.timeMod[0]))})
		vb.implicit = true
	}
}

// closeTuplets closes the brackets a note stops (by number; unnumbered,
// the innermost).
func closeTuplets(vb *voiceBar, stops []string) {
	for _, num := range stops {
		for i := len(vb.open) - 1; i >= 0; i-- {
			if vb.open[i].number == num || num == "" || i == 0 {
				vb.open = vb.open[:i]
				break
			}
		}
		vb.implicit = false
	}
}

// closeImplicit ends an unbracketed tuplet once its notes come to a plain
// value — three triplet eighths are a quarter — or a note outside it comes.
func closeImplicit(vb *voiceBar, xn xnote) {
	if !vb.implicit || len(vb.open) == 0 {
		return
	}
	idx := vb.open[len(vb.open)-1].index
	var sum score.Frac
	for _, p := range vb.events {
		if p.ev.Tuplet == idx && p.ev.Grace == score.NotGrace {
			sum = sum.Add(p.ev.Dur.Value())
		}
	}
	actual := sum.Mul(vb.open[len(vb.open)-1].ratio)
	if oddFree(actual.Den) {
		vb.open, vb.implicit = vb.open[:len(vb.open)-1], false
	}
}

func oddFree(d int64) bool { return d > 0 && d&(d-1) == 0 }

// writtenDur is a note's written value: its <type> and dots, or failing
// that its duration undone by the tuplets it is in.
func writtenDur(xn xnote, vb *voiceBar, warn func(string, ...any)) score.Duration {
	if xn.typed {
		return xn.written
	}
	w := xn.dur
	for _, o := range vb.open {
		w = w.Div(o.ratio)
	}
	if xn.grace != score.NotGrace {
		return score.Duration{Base: score.Eighth}
	}
	if d, ok := score.DurationFor(w); ok {
		return d
	}
	for b := score.Breve; b <= score.D256; b++ {
		if d := (score.Duration{Base: b}); !w.Less(d.Value()) {
			warn("a note of %s of a whole note has no single value; written as %s", w, d)
			return d
		}
	}
	return score.Duration{Base: score.D256}
}

// settleBar turns a voice's placed events into its measure: events in
// order, gaps (a <forward>, a voice that starts late) as spaces, the first
// voice filled to the bar line, a lone whole-bar rest as a bar rest, and
// anything past the bar line cut. It returns each event's offset.
func settleBar(vb *voiceBar, barLen score.Frac, meter score.Meter, first bool, line string, bar int, warn func(string, ...any)) (score.Measure, []score.Frac) {
	evs := vb.events
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].at.Less(evs[j].at) })
	m := score.Measure{Tuplets: vb.tuplets}
	// One division of slack: positions that close are where the voice is.
	slack := score.F(1, 1024)
	var offs []score.Frac
	at := score.Frac{}
	for _, p := range evs {
		gap := p.at.Sub(at)
		if slack.Less(gap) {
			if err := notate.Fill(&m, score.EventSpace, at, gap, meter, barLen); err != nil {
				warn("%s bar %d: %v", line, bar, err)
			}
			for len(offs) < len(m.Events) {
				offs = append(offs, at)
			}
			at = p.at
		} else if gap.Neg().Sign() > 0 && slack.Less(gap.Neg()) && p.ev.Grace == score.NotGrace {
			warn("%s bar %d: notes of one voice overlap; the later is left out", line, bar)
			continue
		}
		ev := p.ev
		if ev.Kind == score.EventBarRest {
			ev.Dur = score.Duration{}
		}
		length := m.Actual(ev, barLen)
		// A whole rest that fills the bar alone is the bar rest, whatever
		// the meter (what Sibelius writes for an empty bar).
		if ev.Kind == score.EventRest && len(evs) == 1 && length.Eq(barLen) && ev.Dur == (score.Duration{Base: score.WholeNote}) {
			ev = score.Event{Kind: score.EventBarRest, Marks: ev.Marks}
			length = barLen
		}
		if barLen.Less(at.Add(length)) && ev.Kind != score.EventBarRest {
			warn("%s bar %d: the bar holds more than its meter; what is past the bar line is left out", line, bar)
			break
		}
		m.Events = append(m.Events, ev)
		offs = append(offs, at)
		at = at.Add(length)
	}
	if first && len(m.Events) > 0 && at.Less(barLen) && m.Events[0].Kind != score.EventBarRest {
		if err := notate.Fill(&m, score.EventSpace, at, barLen.Sub(at), meter, barLen); err == nil {
			for len(offs) < len(m.Events) {
				offs = append(offs, at)
			}
		}
	}
	// A bar rest stands alone.
	for _, e := range m.Events {
		if e.Kind == score.EventBarRest && len(m.Events) > 1 {
			kept := m.Events[:0]
			for _, e := range m.Events {
				if e.Kind != score.EventBarRest {
					kept = append(kept, e)
				}
			}
			m.Events = kept
			offs, _ = m.Offsets(barLen)
			break
		}
	}
	// Tuplets nothing is in are dropped (an implicit one that never got
	// a note, a bracket around a grace).
	return pruneTuplets(m), offs
}

func pruneTuplets(m score.Measure) score.Measure {
	used := make([]bool, len(m.Tuplets)+1)
	for _, e := range m.Events {
		for t := e.Tuplet; t > 0 && t <= len(m.Tuplets); t = m.Tuplets[t-1].Parent {
			used[t] = true
		}
	}
	newIdx := make([]int, len(m.Tuplets)+1)
	var kept []score.Tuplet
	for i, t := range m.Tuplets {
		if used[i+1] {
			if t.Parent > 0 {
				t.Parent = newIdx[t.Parent]
			}
			kept = append(kept, t)
			newIdx[i+1] = len(kept)
		}
	}
	m.Tuplets = kept
	for i := range m.Events {
		m.Events[i].Tuplet = newIdx[m.Events[i].Tuplet]
	}
	return m
}

// attachAt puts marks on the event of a staff's bar at a place: for a
// start, the first note or rest at or after it (voice v first, then any);
// for an ending (stop), the last one before it. It reports whether there
// was one.
func attachAt(l *score.Line, b, staff, v int, at score.Frac, marks []score.Mark, stop bool, offsets map[voiceKey][]score.Frac) bool {
	st := l.Staves[staff-1]
	order := []int{}
	if v >= 0 && v < len(st.Voices) {
		order = append(order, v)
	}
	for i := range st.Voices {
		if i != v {
			order = append(order, i)
		}
	}
	type cand struct {
		vi, ei int
		at     score.Frac
	}
	var best *cand
	for _, vi := range order {
		m := &st.Voices[vi].Measures[b]
		offs := offsets[voiceKey{staff, vi}]
		for ei, e := range m.Events {
			if ei >= len(offs) || e.Grace != score.NotGrace || e.Kind == score.EventSpace {
				continue
			}
			o := offs[ei]
			if stop {
				if (o.Less(at) || at.IsZero() && ei == 0) && (best == nil || best.at.Less(o)) {
					best = &cand{vi, ei, o}
				}
			} else if !o.Less(at) && (best == nil || o.Less(best.at)) {
				best = &cand{vi, ei, o}
			}
		}
		if best != nil && vi == v {
			break
		}
	}
	if best == nil && !stop {
		// Nothing after it in the bar: the last thing in it.
		for _, vi := range order {
			m := &st.Voices[vi].Measures[b]
			for ei := len(m.Events) - 1; ei >= 0; ei-- {
				if m.Events[ei].Kind != score.EventSpace && m.Events[ei].Grace == score.NotGrace {
					if at.Sign() == 0 {
						best = &cand{vi, ei, score.Frac{}}
					}
					break
				}
			}
			if best != nil {
				break
			}
		}
	}
	if best == nil {
		return false
	}
	e := &st.Voices[best.vi].Measures[b].Events[best.ei]
	e.Marks = mergeMarks(e.Marks, marks)
	return true
}

// dropDanglingTies clears a tie that does not reach the same pitch in its
// voice's next event (a tie into another voice, or a broken file).
func dropDanglingTies(l *score.Line) {
	for _, st := range l.Staves {
		for _, v := range st.Voices {
			var prev *score.Event
			for bi := range v.Measures {
				m := &v.Measures[bi]
				if len(m.Events) == 0 && prev != nil {
					clearTies(prev)
					prev = nil
				}
				for ei := range m.Events {
					e := &m.Events[ei]
					if e.Grace != score.NotGrace {
						continue
					}
					if prev != nil {
						for ni := range prev.Notes {
							if prev.Notes[ni].Tie && !hasPitch(e, prev.Notes[ni].Pitch.MIDI()) {
								prev.Notes[ni].Tie = false
							}
						}
					}
					prev = e
				}
			}
			if prev != nil {
				clearTies(prev)
			}
		}
	}
}

func clearTies(e *score.Event) {
	for i := range e.Notes {
		e.Notes[i].Tie = false
	}
}

func hasPitch(e *score.Event, midi int) bool {
	for _, n := range e.Notes {
		if n.Pitch.MIDI() == midi {
			return true
		}
	}
	return false
}
