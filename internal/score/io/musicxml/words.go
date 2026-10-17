package musicxml

// words.go — lyrics, chord symbols and tablature, both ways.
//
// Lyrics are <lyric> elements on their notes: the verse as its number,
// the syllable's place in its word as <syllabic> (a hyphen after a begin
// or middle syllable), and <extend/> for a syllable held over a melisma.
//
// Chord symbols are <harmony> elements before the note at their place (an
// <offset> where no note starts): the root, a <kind> with our spelling in
// its text attribute (other programs show it; ours reads it back), the
// <degree>s added, altered or left out, and the slash bass. A file from
// another program is read from the kind and degrees.
//
// Tablature: a staff shown as tablature alone is a TAB staff — its clef
// sign TAB, its strings and tuning in <staff-details>, each note's string
// and fret in <technical>. A notation staff with tablature linked under it
// is written as notation, with its tuning in <staff-details> and each
// note's string and fret all the same, so nothing is lost; reading, a TAB
// staff that only repeats the notation staff above it (MuseScore writes
// linked staves so) becomes that staff's linked tablature.

import (
	"strconv"
	"strings"

	"foundry/internal/score"
	"foundry/internal/score/io/notate"
)

// ---------- lyrics out ----------

// lyricsXML writes an event's syllables.
func (p *partWriter) lyricsXML(si, vi int, r vref, e score.Event) {
	if e.Kind != score.EventChord || e.Grace != score.NotGrace {
		return
	}
	w := &p.x.w
	for _, ly := range e.Lyrics() {
		w.open("lyric", attr{"number", itoa(ly.Verse)})
		w.leaf("syllabic", p.syllabic(si, vi, r, ly).String())
		w.leaf("text", ly.Text)
		if ly.Extend && !ly.Hyphen {
			w.empty("extend")
		}
		w.close()
	}
}

// syllabic is a syllable's place in its word: whether the verse's
// syllable before it on the voice joins it with a hyphen.
func (p *partWriter) syllabic(si, vi int, r vref, ly score.Lyric) score.Syllabic {
	before := false
	vs := p.voices[[2]int{si, vi}]
	for k := vs.pos[r] - 1; k >= 0; k-- {
		e := p.event(si, vi, vs.seq[k])
		if prev, ok := e.Lyric(ly.Verse); ok {
			before = prev.Hyphen
			break
		}
	}
	switch {
	case before && ly.Hyphen:
		return score.SyllableMiddle
	case before:
		return score.SyllableEnd
	case ly.Hyphen:
		return score.SyllableBegin
	}
	return score.SyllableSingle
}

// ---------- chord symbols out ----------

// harmonyKinds are MusicXML's chord kinds for our triads and sevenths: by
// quality, then seventh, then extension (0, 9, 11, 13).
func harmonyKind(c score.Chord) (kind string, adds []score.Degree) {
	ext := func(base string, names ...string) string {
		switch c.Ext {
		case 9:
			return names[0]
		case 11:
			return names[1]
		case 13:
			return names[2]
		}
		return base
	}
	extAdds := func() {
		for _, n := range []int{9, 11, 13} {
			if c.Ext >= n && (n != 11 || c.Ext == 11 || c.Quality == score.ChordMinor) {
				adds = append(adds, score.Degree{N: n})
			}
		}
	}
	switch c.Quality {
	case score.ChordPower:
		return "power", nil
	case score.ChordMinor:
		switch c.Seventh {
		case score.SeventhMinor:
			return ext("minor-seventh", "minor-ninth", "minor-11th", "minor-13th"), nil
		case score.SeventhMajor:
			extAdds()
			return "major-minor", adds
		}
		if c.Six {
			if c.Ext == 9 {
				adds = append(adds, score.Degree{N: 9})
			}
			return "minor-sixth", adds
		}
		return "minor", nil
	case score.ChordDiminished:
		extAdds()
		switch c.Seventh {
		case score.SeventhDiminished:
			return "diminished-seventh", adds
		case score.SeventhMajor:
			return "diminished", append([]score.Degree{{N: 7}}, adds...)
		}
		return "diminished", nil
	case score.ChordHalfDiminished:
		extAdds()
		return "half-diminished", adds
	case score.ChordAugmented:
		extAdds()
		switch c.Seventh {
		case score.SeventhMinor:
			return "augmented-seventh", adds
		case score.SeventhMajor:
			return "augmented", append([]score.Degree{{N: 7}}, adds...)
		}
		return "augmented", nil
	case score.ChordSus2, score.ChordSus4:
		kind = "suspended-fourth"
		if c.Quality == score.ChordSus2 {
			kind = "suspended-second"
		}
		switch c.Seventh {
		case score.SeventhMinor:
			adds = append(adds, score.Degree{N: 7, Alter: -1})
		case score.SeventhMajor:
			adds = append(adds, score.Degree{N: 7})
		}
		if c.Six {
			adds = append(adds, score.Degree{N: 6})
		}
		extAdds()
		return kind, adds
	}
	switch c.Seventh {
	case score.SeventhMinor:
		return ext("dominant", "dominant-ninth", "dominant-11th", "dominant-13th"), nil
	case score.SeventhMajor:
		return ext("major-seventh", "major-ninth", "major-11th", "major-13th"), nil
	}
	if c.Six {
		if c.Ext == 9 {
			adds = append(adds, score.Degree{N: 9})
		}
		return "major-sixth", adds
	}
	return "major", nil
}

// harmonyXML writes a chord symbol.
func (p *partWriter) harmonyXML(c score.Chord) {
	w := &p.x.w
	w.open("harmony")
	step := func(el string, pt score.Pitch) {
		w.open(el)
		w.leaf(el+"-step", string(score.Steps[pt.Step]))
		if pt.Alter != 0 {
			w.leaf(el+"-alter", itoa(int(pt.Alter)))
		}
		w.close()
	}
	degree := func(d score.Degree, typ string) {
		w.open("degree")
		w.leaf("degree-value", itoa(d.N))
		w.leaf("degree-alter", itoa(d.Alter))
		w.leaf("degree-type", typ)
		w.close()
	}
	if c.NoChord {
		step("root", score.Pitch{})
		w.leaf("kind", "none", attr{"text", "N.C."})
	} else {
		step("root", c.Root)
		kind, adds := harmonyKind(c)
		w.leaf("kind", kind, attr{"text", chordSuffix(c)})
		if c.HasBass {
			step("bass", c.Bass)
		}
		for _, d := range adds {
			degree(d, "add")
		}
		if c.Alt {
			for _, d := range []score.Degree{{N: 9, Alter: -1}, {N: 9, Alter: 1}, {N: 11, Alter: 1}, {N: 13, Alter: -1}} {
				degree(d, "add")
			}
		}
		for _, a := range c.Alters {
			typ := "add"
			if a.N == 5 || a.N <= c.Ext {
				typ = "alter"
			}
			degree(a, typ)
		}
		for _, a := range c.Adds {
			degree(a, "add")
		}
		for _, o := range c.Omits {
			degree(score.Degree{N: o}, "subtract")
		}
	}
	w.leaf("staff", "1") // (an <offset> goes before it: withOffset)
	w.close()
}

// chordSuffix is a chord's spelling after its root and before its bass.
func chordSuffix(c score.Chord) string {
	t := c.String()
	n := 1
	for n < len(t) && (t[n] == '#' || t[n] == 'b') && n < 3 {
		n++
	}
	t = t[n:]
	if c.HasBass {
		t = t[:strings.LastIndexByte(t, '/')]
	}
	return t
}

// chordItems are a part's chord symbols in a bar, for its first voice.
func (p *partWriter) chordItems(bar int, barLen score.Frac) []item {
	var out []item
	for _, ch := range p.l.Chords {
		if ch.At.Bar != bar || !ch.At.Offset.Less(barLen) {
			continue
		}
		c := ch.Chord
		if len(p.l.Staves) > 0 {
			// Written as the part reads: transposed with its first staff.
			st := p.l.Staves[0]
			c = c.Transpose(score.Interval{Diatonic: -st.Transpose.Diatonic, Chromatic: -st.Transpose.Chromatic})
		}
		out = append(out, item{at: ch.At.Offset, write: func() { p.harmonyXML(c) }})
	}
	return out
}

// ---------- tablature out ----------

// staffDetailsXML writes a staff's tablature: a TAB staff's lines, the
// tuning lowest string first (MusicXML counts lines from the bottom), the
// capo.
func (x *exporter) staffDetailsXML(st *score.Staff, number int) {
	t := st.Tab
	w := &x.w
	if t == nil {
		if st.Percussion() && st.StaffLines() != 5 {
			w.open("staff-details", attr{"number", itoa(number)})
			w.leaf("staff-lines", itoa(st.StaffLines()))
			w.close()
		}
		return
	}
	w.open("staff-details", attr{"number", itoa(number)})
	if t.Only {
		w.leaf("staff-lines", itoa(t.Strings()))
	}
	for i, p := range t.Tuning {
		w.open("staff-tuning", attr{"line", itoa(i + 1)})
		w.leaf("tuning-step", string(score.Steps[p.Step]))
		if p.Alter != 0 {
			w.leaf("tuning-alter", itoa(int(p.Alter)))
		}
		w.leaf("tuning-octave", itoa(int(p.Octave)))
		w.close()
	}
	if t.Capo > 0 {
		w.leaf("capo", itoa(t.Capo))
	}
	w.close()
}

// tabPlaces are an event's notes on its staff's strings, as the
// tablature shows them (nil without one).
func (p *partWriter) tabPlaces(si, vi int, r vref) []score.TabNote {
	st := p.l.Staves[si]
	if st.Tab == nil {
		return nil
	}
	if p.places == nil {
		p.places = map[[2]int][][][]score.TabNote{}
	}
	k := [2]int{si, vi}
	pl, ok := p.places[k]
	if !ok {
		pl = st.Tab.VoicePlaces(st.Voices[vi])
		p.places[k] = pl
	}
	if r.bar-1 < len(pl) && r.idx < len(pl[r.bar-1]) {
		return pl[r.bar-1][r.idx]
	}
	return nil
}

// linkedTabsAsStaves is the score as MusicXML holds a linked tablature:
// the notation staff, and after it a TAB staff of the same notes (with
// only their strings marked, the rest is the notation's). The score given
// is not changed; one with no linked tablature is returned as it is.
func linkedTabsAsStaves(s *score.Score) *score.Score {
	linked := false
	for _, l := range s.Lines {
		for _, st := range l.Staves {
			linked = linked || st.Tab != nil && !st.Tab.Only
		}
	}
	if !linked {
		return s
	}
	c := s.Clone()
	for _, l := range c.Lines {
		var staves []*score.Staff
		for _, st := range l.Staves {
			staves = append(staves, st)
			if st.Tab == nil || st.Tab.Only {
				continue
			}
			tab := st.Clone()
			tab.Tab.Only = true
			tab.Clefs = nil
			for _, v := range tab.Voices {
				for bi := range v.Measures {
					for ei := range v.Measures[bi].Events {
						e := &v.Measures[bi].Events[ei]
						var keep []score.Mark
						for _, mk := range e.Marks {
							if mk.Name == "str" {
								keep = append(keep, mk)
							}
						}
						e.Marks = keep
					}
				}
			}
			st.Tab = nil
			staves = append(staves, tab)
		}
		l.Staves = staves
	}
	return c
}

// ---------- reading ----------

// readLyric is a <lyric> as a mark on its note.
func readLyric(k *node, index int) (score.Mark, bool) {
	verse := atoiDef(k.attr("number"), index+1)
	if verse < 1 || verse > score.MaxVerses {
		verse = min(max(index+1, 1), score.MaxVerses)
	}
	var parts []string
	for _, t := range k.children("text") {
		parts = append(parts, t.text)
	}
	text := strings.TrimSpace(strings.Join(parts, "‿"))
	if text == "" {
		return score.Mark{}, false
	}
	ly := score.Lyric{Verse: verse, Text: text}
	switch k.str("syllabic") {
	case "begin", "middle":
		ly.Hyphen = true
	}
	if ex := k.child("extend"); ex != nil && ex.attr("type") != "stop" && !ly.Hyphen {
		ly.Extend = true
	}
	return ly.Mark(), true
}

// harmonyKindText is how each MusicXML kind is spelled after the root,
// for reading a file that does not give its own text.
var harmonyKindText = map[string]string{
	"major": "", "minor": "m", "augmented": "aug", "diminished": "dim",
	"dominant": "7", "major-seventh": "maj7", "minor-seventh": "m7", "diminished-seventh": "dim7",
	"augmented-seventh": "aug7", "half-diminished": "m7b5", "major-minor": "mmaj7",
	"major-sixth": "6", "minor-sixth": "m6", "dominant-ninth": "9", "major-ninth": "maj9",
	"minor-ninth": "m9", "dominant-11th": "11", "major-11th": "maj11", "minor-11th": "m11",
	"dominant-13th": "13", "major-13th": "maj13", "minor-13th": "m13", "suspended-second": "sus2",
	"suspended-fourth": "sus4", "power": "5", "Neapolitan": "", "Italian": "7", "French": "7b5",
	"German": "7", "pedal": "5", "Tristan": "7b5",
}

// readHarmony is a <harmony> as a chord, ok false for one it cannot read.
func readHarmony(k *node, ours bool) (score.Chord, bool) {
	kind := k.child("kind")
	kindName := strings.TrimSpace(kind.text)
	if kindName == "none" {
		return score.Chord{NoChord: true}, true
	}
	rt := k.child("root")
	if rt == nil {
		return score.Chord{}, false
	}
	root := pitchName(rt.str("root-step"), rt.integer("root-alter", 0))
	bass := ""
	if b := k.child("bass"); b != nil {
		bass = "/" + pitchName(b.str("bass-step"), b.integer("bass-alter", 0))
	}
	// Our own text, or one another program wrote, if it reads.
	if t := kind.attr("text"); t != "" && (ours || kind.attr("use-symbols") != "yes") {
		if c, err := score.ParseChord(root + t + bass); err == nil && (ours || sameKind(c, kindName)) {
			return c, true
		}
	}
	sym, ok := harmonyKindText[kindName]
	if !ok {
		return score.Chord{}, false
	}
	var b strings.Builder
	b.WriteString(root + sym)
	for _, d := range k.children("degree") {
		v := d.integer("degree-value", 0)
		al := d.integer("degree-alter", 0)
		acc := ""
		switch {
		case al < 0:
			acc = "b"
		case al > 0:
			acc = "#"
		}
		switch d.str("degree-type") {
		case "subtract":
			b.WriteString("no" + strconv.Itoa(v))
		case "alter":
			b.WriteString(acc + strconv.Itoa(v))
		default:
			if v == 7 && !strings.Contains(sym, "7") {
				// An added seventh (C7sus4 as suspended-fourth + b7).
				if al < 0 {
					b.Reset()
					b.WriteString(root + "7" + sym)
				} else {
					b.WriteString("maj7")
				}
				continue
			}
			if acc != "" && (v == 5 || v == 9 || v == 11 || v == 13) {
				b.WriteString(acc + strconv.Itoa(v))
				continue
			}
			b.WriteString("add" + acc + strconv.Itoa(v))
		}
	}
	b.WriteString(bass)
	c, err := score.ParseChord(b.String())
	return c, err == nil
}

// sameKind reports a chord read from a file's text that agrees with the
// kind it names (a text that reads as another chord is a display string,
// not the chord).
func sameKind(c score.Chord, kind string) bool {
	k, _ := harmonyKind(c)
	if k == kind {
		return true
	}
	// A spelling with extra degrees still has its triad's kind.
	return strings.HasPrefix(kind, strings.SplitN(k, "-", 2)[0])
}

func pitchName(step string, alter int) string {
	s := strings.ToUpper(strings.TrimSpace(step))
	if s == "" {
		s = "C"
	}
	s = s[:1]
	for ; alter > 0; alter-- {
		s += "#"
	}
	for ; alter < 0; alter++ {
		s += "b"
	}
	return s
}

// xtab is a staff's tablature as read: its strings, tuning and capo, and
// whether its clef is TAB.
type xtab struct {
	lines  int
	tuning map[int]score.Pitch // by MusicXML line, 1 the lowest
	capo   int
	clef   bool
}

// readStaffDetails reads a <staff-details> into the staff's tablature.
func readStaffDetails(d *node, tabs map[int]*xtab) {
	n := atoiDef(d.attr("number"), 1)
	t := tabs[n]
	if t == nil {
		t = &xtab{tuning: map[int]score.Pitch{}}
		tabs[n] = t
	}
	if l := d.integer("staff-lines", 0); l > 0 {
		t.lines = l
	}
	for _, st := range d.children("staff-tuning") {
		line := atoiDef(st.attr("line"), 0)
		if line < 1 {
			continue
		}
		t.tuning[line] = pitchOf(st.str("tuning-step"), st.integer("tuning-alter", 0), st.integer("tuning-octave", 3))
	}
	if c := d.integer("capo", 0); c > 0 {
		t.capo = c
	}
}

// tab makes the score's tablature of what a staff's details said: its
// tuning, or for a TAB staff with none the usual one for its strings.
func (t *xtab) tab() *score.Tab {
	n := t.lines
	for line := range t.tuning {
		n = max(n, line)
	}
	var tuning []score.Pitch
	if len(t.tuning) >= n && n > 0 {
		for line := 1; line <= n; line++ {
			tuning = append(tuning, t.tuning[line])
		}
	} else {
		name := "guitar"
		switch n {
		case 4:
			name = "bass"
		case 5:
			name = "bass_5"
		case 7:
			name = "guitar_7"
		}
		tuning = score.NewTab(name).Tuning
	}
	if len(tuning) == 0 || len(tuning) > score.MaxStrings {
		return nil
	}
	return &score.Tab{Tuning: tuning, Capo: min(t.capo, 12), Only: t.clef}
}

// readWords gives a built line what its measures said about words and
// strings: the chord symbols, each staff's tablature, and a TAB staff that
// only repeats the notation over it folded into that staff.
func readWords(l *score.Line, ms []xmeasure, s *score.Score, lay notate.Layout, warn func(string, ...any)) {
	for b, xm := range ms {
		if b >= s.Bars {
			break
		}
		for _, h := range xm.harmonies {
			at := score.Pos{Bar: b + 1, Offset: h.at}
			if !h.at.Less(lay.BarLen(b)) {
				if b+1 >= s.Bars {
					continue
				}
				at = score.Pos{Bar: b + 2}
			}
			dup := false
			for _, c := range l.Chords {
				dup = dup || c.At.Bar == at.Bar && c.At.Offset.Eq(at.Offset)
			}
			if !dup {
				l.Chords = append(l.Chords, score.ChordSymbol{At: at, Chord: h.chord})
			}
		}
		for n, t := range xm.tabs {
			if n < 1 || n > len(l.Staves) || l.Staves[n-1].Tab != nil {
				continue
			}
			if st := l.Staves[n-1]; st.Percussion() {
				if t.lines >= 1 && t.lines < 5 && b == 0 {
					st.Lines = t.lines // a single drum's one line
				}
				continue
			}
			if tab := t.tab(); tab != nil && (t.clef || len(t.tuning) > 0) {
				st := l.Staves[n-1]
				st.Tab = tab
				if tab.Only && st.Clef == "treble_8vb" && tab.Tuning[0].MIDI() < 36 {
					st.Clef = "bass" // a bass's tablature
				}
			}
		}
	}
	l.SortChords()
	foldLinkedTab(l, warn)
	for _, st := range l.Staves {
		looseStrings(st)
	}
}

// looseStrings keeps only the string choices a tablature would not make
// by itself: a file names every note's string, and a note the file puts
// where Tab.Place would (from where the hand is, as the engraver walks a
// voice) is left to the tablature, so editing it later can move it.
func looseStrings(st *score.Staff) {
	if st.Tab == nil {
		for _, v := range st.Voices {
			for bi := range v.Measures {
				for ei := range v.Measures[bi].Events {
					v.Measures[bi].Events[ei].SetTabStrings(nil)
				}
			}
		}
		return
	}
	for _, v := range st.Voices {
		hand := 0.0
		for bi := range v.Measures {
			for ei := range v.Measures[bi].Events {
				e := &v.Measures[bi].Events[ei]
				if e.Kind != score.EventChord {
					continue
				}
				ps := make([]score.Pitch, len(e.Notes))
				for i, n := range e.Notes {
					ps[i] = n.Pitch
				}
				fixed := e.TabStrings()
				auto := st.Tab.Place(ps, nil, hand)
				for i := range fixed {
					if auto[i].OK && auto[i].String == fixed[i] {
						fixed[i] = 0
					}
				}
				e.SetTabStrings(fixed)
				if h := score.HandAt(st.Tab.Place(ps, fixed, hand)); h > 0 {
					hand = h
				}
			}
		}
	}
}

// foldLinkedTab makes a TAB staff that repeats the notation staff over it
// (the same notes at the same times) that staff's linked tablature: the
// staff goes, its string choices onto the notation's notes.
func foldLinkedTab(l *score.Line, warn func(string, ...any)) {
	for i := 1; i < len(l.Staves); i++ {
		tab, above := l.Staves[i], l.Staves[i-1]
		if tab.Tab == nil || !tab.Tab.Only || above.Tab != nil || !sameMusic(above, tab) {
			continue
		}
		above.Tab = tab.Tab
		above.Tab.Only = false
		for vi, v := range tab.Voices {
			for bi, m := range v.Measures {
				for ei := range m.Events {
					strs := m.Events[ei].TabStrings()
					above.Voices[vi].Measures[bi].Events[ei].SetTabStrings(strs)
				}
			}
		}
		l.Staves = append(l.Staves[:i], l.Staves[i+1:]...)
		return
	}
}

// sameMusic reports two staves with the same events: their voices' rhythm
// and pitches alike, bar by bar.
func sameMusic(a, b *score.Staff) bool {
	if len(a.Voices) != len(b.Voices) {
		return false
	}
	for vi := range a.Voices {
		va, vb := a.Voices[vi], b.Voices[vi]
		if len(va.Measures) != len(vb.Measures) {
			return false
		}
		for bi := range va.Measures {
			ma, mb := va.Measures[bi], vb.Measures[bi]
			if len(ma.Events) != len(mb.Events) {
				return false
			}
			for ei := range ma.Events {
				ea, eb := ma.Events[ei], mb.Events[ei]
				if ea.Kind != eb.Kind || ea.Dur != eb.Dur || len(ea.Notes) != len(eb.Notes) {
					return false
				}
				for k := range ea.Notes {
					if ea.Notes[k].Pitch.MIDI() != eb.Notes[k].Pitch.MIDI() {
						return false
					}
				}
			}
		}
	}
	return true
}
