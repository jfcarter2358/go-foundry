package score

// chords.go — chord symbols: "Cmaj7", "F#m7b5", "Bb7(b9,#11)/D", "N.C.".
//
// A line's chord symbols stand over its top staff at written positions,
// apart from its notes (a lead sheet's changes fall where the harmony
// moves, not where the melody does). Each is a Chord, read from what a
// musician types — every common spelling of a quality, extension,
// alteration, addition and slash bass — and stored in one canonical
// spelling (Chord.String), so a file reads the same whoever typed it:
//
//	chords: [{at: "1", sym: Cmaj7}, {at: "2", sym: Am7}, {at: "3:3", sym: D7b9/F#}, {at: "5", sym: N.C.}]
//
// Spelling, in order after the root (C, F#, Bb):
//
//	quality    "" major, m minor, dim, m7b5 half-diminished, aug, sus2,
//	           sus4, 5 (a power chord, no third)
//	           read also: min mi - (minor), ° o (dim), ø h (half-dim),
//	           + (aug), M maj ma Δ ^ (a major seventh), sus (sus4)
//	seventh    7 (minor seventh), maj7, dim7; mmaj7 minor-major
//	extension  9, 11, 13 over a seventh (C9, Cmaj9, Cm11); 6 and 6/9 (69)
//	altered    b5 #5 b9 #9 #11 b13, and alt (b9 #9 #11 b13 over a 7)
//	added      add2 add4 add9 add11 add13 (with an accidental: add#11)
//	left out   no3 no5 (omit3 reads too)
//	bass       /E, /Bb
//
// Parentheses, commas and spaces between the parts are read and not kept:
// "C7(b9, #11)" is "C7b9#11". A chord symbol plays only when its line asks
// (Line.ChordPlay): a pad or a comping part under the line, voiced from
// Chord.Tones.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ChordQuality is a chord's triad.
type ChordQuality uint8

const (
	ChordMajor ChordQuality = iota
	ChordMinor
	ChordDiminished
	// ChordHalfDiminished is a diminished triad with a minor seventh (m7b5).
	ChordHalfDiminished
	ChordAugmented
	ChordSus2
	ChordSus4
	// ChordPower is a root and a fifth, no third ("5").
	ChordPower
)

// ChordSeventh is a chord's seventh.
type ChordSeventh uint8

const (
	SeventhNone ChordSeventh = iota
	// SeventhMinor is the dominant's (and a minor seventh chord's) seventh.
	SeventhMinor
	SeventhMajor
	// SeventhDiminished is a diminished seventh chord's (a major sixth).
	SeventhDiminished
)

// Degree is a chord degree (5, 9, 11, 13, or an added 2, 4, 6) with an
// alteration in semitones.
type Degree struct {
	N     int
	Alter int
}

func (d Degree) String() string {
	var b strings.Builder
	writeAlter(&b, d.Alter)
	b.WriteString(strconv.Itoa(d.N))
	return b.String()
}

// Chord is a chord symbol's harmony.
type Chord struct {
	// NoChord is "N.C.": nothing plays.
	NoChord bool
	// Root and Bass are pitch names (Octave unused); a slash chord has a
	// bass other than its root.
	Root, Bass Pitch
	HasBass    bool
	Quality    ChordQuality
	Seventh    ChordSeventh
	// Ext is the highest extension over the seventh: 0, 9, 11 or 13 (with
	// Six and no seventh, 9 is a 6/9).
	Ext int
	Six bool
	// Alt is an altered dominant: b9, #9, #11 and b13.
	Alt    bool
	Alters []Degree // b5, #5, b9, #9, #11, b13: replace the natural degree
	Adds   []Degree // add2, add4, add9, add11, add13: over the chord
	Omits  []int    // 3, 5
}

// ChordSymbol is a chord symbol at a written position over a line.
type ChordSymbol struct {
	At    Pos
	Chord Chord
}

// ChordStyle is how a line's chord symbols play.
type ChordStyle uint8

const (
	// ChordsSilent leaves them printed only.
	ChordsSilent ChordStyle = iota
	// ChordsPad holds each chord until the next: sustained, legato.
	ChordsPad
	// ChordsComp strikes each chord on the beats, short, with the bass on
	// the chord's first beat.
	ChordsComp
)

// ChordStyles are ChordStyle's names in a file.
var ChordStyles = []string{"off", "pad", "comp"}

// ChordPlay is how a line's chord symbols play.
type ChordPlay struct {
	Style ChordStyle
	// Instrument plays them; "" is the line's own.
	Instrument string
	// Volume scales them against the line's notes (1 is as loud).
	Volume float32
	// Octave moves the voicing from where it sits (the bass in octave 2,
	// the chord around middle C).
	Octave int
}

// ---------- reading ----------

// ParseChord reads a chord symbol as a musician types it.
func ParseChord(text string) (Chord, error) {
	var c Chord
	t := strings.TrimSpace(text)
	if t == "" {
		return c, fmt.Errorf("an empty chord symbol")
	}
	switch strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(t, ".", ""), " ", "")) {
	case "NC":
		c.NoChord = true
		return c, nil
	}
	t = normaliseChordText(t)
	root, n, err := scanPitchName(t)
	if err != nil {
		return c, fmt.Errorf("chord %q: %v", text, err)
	}
	c.Root = root
	rest := t[n:]
	// The slash bass: a note name after the last "/" (not 6/9).
	if i := strings.LastIndexByte(rest, '/'); i >= 0 && i+1 < len(rest) && rest[i+1] >= 'A' && rest[i+1] <= 'G' {
		b, n, err := scanPitchName(rest[i+1:])
		if err != nil || i+1+n != len(rest) {
			return c, fmt.Errorf("chord %q: bad bass after /", text)
		}
		c.Bass, c.HasBass = b, true
		rest = rest[:i]
	}
	if err := c.parseBody(rest); err != nil {
		return Chord{}, fmt.Errorf("chord %q: %v", text, err)
	}
	c.normalise()
	return c, nil
}

// normaliseChordText turns the typographic signs into ASCII ones.
func normaliseChordText(t string) string {
	r := strings.NewReplacer("♯", "#", "♭", "b", "Δ", "^", "△", "^", "°", "o", "º", "o", "ø", "h", "Ø", "h",
		"–", "-", "−", "-", " ", "", "(", "", ")", "", ",", "")
	return r.Replace(t)
}

// scanPitchName reads a letter and its accidentals: "C", "F#", "Bb".
func scanPitchName(s string) (Pitch, int, error) {
	if s == "" || s[0] < 'A' || s[0] > 'G' {
		return Pitch{}, 0, fmt.Errorf("no root (a letter A-G)")
	}
	p := Pitch{Step: int8(strings.IndexByte(Steps, s[0]))}
	i := 1
	for i < len(s) && i < 3 {
		switch s[i] {
		case '#':
			p.Alter++
		case 'b':
			// A "b" straight after the letter is a flat (Bb7); an altered
			// degree comes only after a number (C7b9).
			p.Alter--
		default:
			return p, i, nil
		}
		i++
	}
	if p.Alter > 2 || p.Alter < -2 {
		return Pitch{}, 0, fmt.Errorf("too many accidentals")
	}
	return p, i, nil
}

// chordWords are the spellings read after the root, longest first within
// each meaning so "maj7" is not read as "m" then "aj7".
var chordWords = []struct {
	word string
	do   func(c *Chord, st *chordReadState)
}{
	{"minmaj", func(c *Chord, st *chordReadState) { c.Quality, st.majSev = ChordMinor, true }},
	{"mmaj", func(c *Chord, st *chordReadState) { c.Quality, st.majSev = ChordMinor, true }},
	{"mM", func(c *Chord, st *chordReadState) { c.Quality, st.majSev = ChordMinor, true }},
	{"m^", func(c *Chord, st *chordReadState) { c.Quality, st.majSev, st.bareMaj = ChordMinor, true, true }},
	{"-^", func(c *Chord, st *chordReadState) { c.Quality, st.majSev, st.bareMaj = ChordMinor, true, true }},
	{"maj", func(c *Chord, st *chordReadState) { st.majSev = true }},
	{"Maj", func(c *Chord, st *chordReadState) { st.majSev = true }},
	{"MAJ", func(c *Chord, st *chordReadState) { st.majSev = true }},
	{"ma", func(c *Chord, st *chordReadState) { st.majSev = true }},
	{"M", func(c *Chord, st *chordReadState) { st.majSev = true }},
	{"^", func(c *Chord, st *chordReadState) { st.majSev, st.bareMaj = true, true }},
	{"min", func(c *Chord, st *chordReadState) { c.Quality = ChordMinor }},
	{"mi", func(c *Chord, st *chordReadState) { c.Quality = ChordMinor }},
	{"m", func(c *Chord, st *chordReadState) { c.Quality = ChordMinor }},
	{"-", func(c *Chord, st *chordReadState) { c.Quality = ChordMinor }},
	{"dim", func(c *Chord, st *chordReadState) { c.Quality = ChordDiminished }},
	{"o", func(c *Chord, st *chordReadState) { c.Quality = ChordDiminished }},
	{"h", func(c *Chord, st *chordReadState) { c.Quality, c.Seventh = ChordHalfDiminished, SeventhMinor }},
	{"aug", func(c *Chord, st *chordReadState) { c.Quality = ChordAugmented }},
	{"+", func(c *Chord, st *chordReadState) { c.Quality = ChordAugmented }},
}

type chordReadState struct {
	// majSev: a "maj" was read, so the seventh to come is major (alone,
	// with bareMaj — Δ — it is a major seventh chord; "Cmaj" is a triad).
	majSev, bareMaj bool
}

// parseBody reads what follows the root.
func (c *Chord) parseBody(s string) error {
	var st chordReadState
	if strings.HasPrefix(s, "madd") {
		// Cmadd9: minor, not "ma" then "dd9".
		c.Quality, s = ChordMinor, s[1:]
	}
	// The quality, at most once ("omit" is not a diminished "o").
	for _, w := range chordWords {
		if strings.HasPrefix(s, w.word) && !strings.HasPrefix(s, "omit") {
			w.do(c, &st)
			s = s[len(w.word):]
			break
		}
	}
	// "Cm" then "maj7" (Cm(maj7)).
	if c.Quality == ChordMinor && !st.majSev {
		for _, w := range []string{"maj", "Maj", "ma", "M", "^"} {
			if strings.HasPrefix(s, w) {
				st.majSev = true
				st.bareMaj = w == "^"
				s = s[len(w):]
				break
			}
		}
	}
	// The number: 5 (power), 6, 6/9, 69, 7, 9, 11, 13.
	switch {
	case strings.HasPrefix(s, "6/9"), strings.HasPrefix(s, "6-9"):
		c.Six, c.Ext, s = true, 9, s[3:]
	case strings.HasPrefix(s, "69"):
		c.Six, c.Ext, s = true, 9, s[2:]
	case strings.HasPrefix(s, "13"), strings.HasPrefix(s, "11"):
		c.Ext, _ = strconv.Atoi(s[:2])
		s = s[2:]
		c.setSeventh(st)
	case strings.HasPrefix(s, "9"):
		c.Ext, s = 9, s[1:]
		c.setSeventh(st)
	case strings.HasPrefix(s, "7"):
		s = s[1:]
		c.setSeventh(st)
	case strings.HasPrefix(s, "6"):
		c.Six, s = true, s[1:]
	case strings.HasPrefix(s, "5") && c.Quality == ChordMajor && !st.majSev:
		c.Quality, s = ChordPower, s[1:]
	case strings.HasPrefix(s, "2") && !st.majSev:
		// A pop chart's "C2": the chord with its 2nd added (Cadd2).
		c.Adds, s = append(c.Adds, Degree{N: 2}), s[1:]
	default:
		if st.bareMaj {
			c.Seventh = SeventhMajor // Δ alone
		}
	}
	// Then the alterations, additions, omissions and sus, in any order.
	for s != "" {
		switch {
		case strings.HasPrefix(s, "alt"):
			c.Alt, s = true, s[3:]
			if c.Seventh == SeventhNone {
				c.Seventh = SeventhMinor
			}
		case strings.HasPrefix(s, "sus2"):
			if c.Quality == ChordSus4 {
				c.Adds = append(c.Adds, Degree{N: 2}) // Csus4sus2: both
			} else {
				c.Quality = ChordSus2
			}
			s = s[4:]
		case strings.HasPrefix(s, "sus"):
			if c.Quality == ChordSus2 {
				// Csus2sus4: the fourth over the sus2's 2nd.
				c.Adds = append(c.Adds, Degree{N: 2})
			}
			c.Quality, s = ChordSus4, s[3:]
			s = strings.TrimPrefix(s, "4")
		case strings.HasPrefix(s, "add"):
			d, n, err := scanDegree(s[3:])
			if err != nil {
				return fmt.Errorf("add: %v", err)
			}
			switch d.N {
			case 2, 4, 6, 9, 11, 13:
			default:
				return fmt.Errorf("add%s: an added note is a 2, 4, 6, 9, 11 or 13", d)
			}
			c.Adds = append(c.Adds, d)
			s = s[3+n:]
		case strings.HasPrefix(s, "no"), strings.HasPrefix(s, "omit"):
			k := 2
			if s[0] == 'o' {
				k = 4
			}
			switch {
			case strings.HasPrefix(s[k:], "3"):
				c.Omits = append(c.Omits, 3)
			case strings.HasPrefix(s[k:], "5"):
				c.Omits = append(c.Omits, 5)
			default:
				return fmt.Errorf("only a 3rd or a 5th can be left out")
			}
			s = s[k+1:]
		case strings.HasPrefix(s, "maj") && c.Seventh == SeventhNone:
			// A major seventh after the quality (Cdim(maj7), Caugmaj9).
			s = s[3:]
			c.Seventh = SeventhMajor
			for _, n := range []string{"13", "11", "9", "7"} {
				if strings.HasPrefix(s, n) {
					if n != "7" {
						c.Ext, _ = strconv.Atoi(n)
					}
					s = s[len(n):]
					break
				}
			}
		case s[0] == '+' && (len(s) == 1 || s[1] < '0' || s[1] > '9'):
			// A "+" with no number after the seventh (C7+, C9+) is a
			// sharpened fifth, as a lone "+" after the root is aug.
			if c.Quality == ChordMajor && c.Seventh == SeventhNone && !c.Six {
				c.Quality = ChordAugmented
			} else {
				c.Alters = append(c.Alters, Degree{N: 5, Alter: 1})
			}
			s = s[1:]
		case s[0] == 'b' || s[0] == '#' || s[0] == '+' || s[0] == '-':
			d, n, err := scanDegree(s)
			if err != nil {
				return err
			}
			if d.Alter == 0 {
				return fmt.Errorf("%q: an alteration is flat or sharp", s[:n])
			}
			switch d.N {
			case 5, 9, 11, 13:
			default:
				return fmt.Errorf("%s: only the 5th, 9th, 11th and 13th are altered", d)
			}
			c.Alters = append(c.Alters, d)
			s = s[n:]
		case strings.HasPrefix(s, "5") || strings.HasPrefix(s, "9") || strings.HasPrefix(s, "11") || strings.HasPrefix(s, "13"):
			// A natural degree after an alteration ("7b9 13"): an added one.
			d, n, _ := scanDegree(s)
			if c.Seventh != SeventhNone && d.N > c.Ext {
				c.Ext = d.N
			} else if d.N != 5 {
				c.Adds = append(c.Adds, d)
			}
			s = s[n:]
		default:
			return fmt.Errorf("cannot read %q", s)
		}
	}
	return nil
}

// setSeventh sets the seventh a number over 6 implies: major after a
// "maj", diminished on a diminished triad, otherwise minor.
func (c *Chord) setSeventh(st chordReadState) {
	switch {
	case st.majSev:
		c.Seventh = SeventhMajor
	case c.Quality == ChordDiminished:
		c.Seventh = SeventhDiminished
	default:
		c.Seventh = SeventhMinor
	}
}

// scanDegree reads an accidental (b # + -) and a number.
func scanDegree(s string) (Degree, int, error) {
	d := Degree{}
	i := 0
	for i < len(s) {
		switch s[i] {
		case 'b', '-':
			d.Alter--
		case '#', '+':
			d.Alter++
		default:
			goto num
		}
		i++
	}
num:
	j := i
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j == i {
		return d, 0, fmt.Errorf("%q: want a number", s)
	}
	// "b913": two degrees run together reads the first one only.
	if j-i > 2 {
		j = i + 1
		if s[i] == '1' {
			j = i + 2
		}
	}
	d.N, _ = strconv.Atoi(s[i:j])
	if d.Alter < -1 || d.Alter > 1 {
		return d, 0, fmt.Errorf("%q: one flat or sharp", s[:j])
	}
	return d, j, nil
}

// normalise puts a chord in its one spelling: a minor triad with a
// flattened fifth and a minor seventh is half-diminished; lists sorted,
// without repeats.
func (c *Chord) normalise() {
	if c.Quality == ChordMinor && c.Seventh == SeventhMinor {
		for i, a := range c.Alters {
			if a == (Degree{5, -1}) {
				c.Quality = ChordHalfDiminished
				c.Alters = append(c.Alters[:i:i], c.Alters[i+1:]...)
				break
			}
		}
	}
	if c.Quality == ChordDiminished && c.Seventh == SeventhMinor {
		c.Quality = ChordHalfDiminished
	}
	if c.Quality == ChordHalfDiminished && c.Seventh == SeventhNone {
		c.Seventh = SeventhMinor
	}
	if c.Seventh == SeventhNone && !c.Six && c.Ext > 0 {
		c.Seventh = SeventhMinor
	}
	if c.Alt {
		var keep []Degree
		for _, a := range c.Alters {
			if a.N == 5 || a.N == 9 || a.N == 11 || a.N == 13 {
				continue // alt names them all
			}
			keep = append(keep, a)
		}
		c.Alters = keep
	}
	sortDegrees := func(ds []Degree) []Degree {
		sort.Slice(ds, func(i, j int) bool {
			if ds[i].N != ds[j].N {
				return ds[i].N < ds[j].N
			}
			return ds[i].Alter < ds[j].Alter
		})
		var out []Degree
		for _, d := range ds {
			if len(out) == 0 || out[len(out)-1] != d {
				out = append(out, d)
			}
		}
		return out
	}
	c.Alters, c.Adds = sortDegrees(c.Alters), sortDegrees(c.Adds)
	sort.Ints(c.Omits)
	var om []int
	for _, o := range c.Omits {
		if len(om) == 0 || om[len(om)-1] != o {
			om = append(om, o)
		}
	}
	c.Omits = om
}

// ---------- writing ----------

// String is the chord's canonical spelling, which ParseChord reads back
// as the same chord.
func (c Chord) String() string {
	if c.NoChord {
		return "N.C."
	}
	var b strings.Builder
	b.WriteString(pitchName(c.Root))
	b.WriteString(c.suffix(false))
	if c.HasBass {
		b.WriteByte('/')
		b.WriteString(pitchName(c.Bass))
	}
	return b.String()
}

// pitchName is a letter and its accidentals.
func pitchName(p Pitch) string {
	var b strings.Builder
	b.WriteByte(Steps[p.Step])
	writeAlter(&b, int(p.Alter))
	return b.String()
}

// suffix is everything after the root but the bass; print spells it as
// a lead sheet does (° ø Δ-free, flats and sharps as ♭ ♯, alterations
// bracketed when there are several).
func (c Chord) suffix(print bool) string {
	var b strings.Builder
	num := func() string {
		if c.Ext > 0 {
			return strconv.Itoa(c.Ext)
		}
		return "7"
	}
	switch c.Quality {
	case ChordPower:
		b.WriteString("5")
	case ChordDiminished:
		if print {
			b.WriteString("°")
		} else {
			b.WriteString("dim")
		}
		switch c.Seventh {
		case SeventhDiminished:
			b.WriteString(num())
		case SeventhMajor:
			b.WriteString("maj" + num())
		}
	case ChordHalfDiminished:
		if print {
			b.WriteString("ø" + num())
		} else {
			b.WriteString("m" + num() + "b5")
		}
	case ChordAugmented:
		if print {
			b.WriteString("+")
		} else {
			b.WriteString("aug")
		}
		c.writeSeventh(&b, num)
	default:
		if c.Quality == ChordMinor && c.Seventh == SeventhMajor && !c.Six && print {
			// A lead sheet brackets the minor-major seventh: Cm(maj7),
			// never the "Cmmaj7" the file spells.
			b.WriteString("m(maj" + num() + ")")
			break
		}
		if c.Quality == ChordMinor {
			b.WriteString("m")
		}
		c.writeSeventh(&b, num)
	}
	if c.Alt {
		b.WriteString("alt")
	}
	var alts []string
	for _, a := range c.Alters {
		alts = append(alts, a.String())
	}
	if print && len(alts) > 1 {
		b.WriteString("(" + strings.Join(alts, ",") + ")")
	} else {
		b.WriteString(strings.Join(alts, ""))
	}
	switch c.Quality {
	case ChordSus2:
		b.WriteString("sus2")
	case ChordSus4:
		b.WriteString("sus4")
	}
	for _, a := range c.Adds {
		b.WriteString("add" + a.String())
	}
	for _, o := range c.Omits {
		if print {
			b.WriteString("(no" + strconv.Itoa(o) + ")")
		} else {
			b.WriteString("no" + strconv.Itoa(o))
		}
	}
	s := b.String()
	if print {
		s = strings.NewReplacer("b", "♭", "#", "♯").Replace(s)
	}
	return s
}

// writeSeventh writes a major, minor, sus or augmented chord's sixth,
// seventh and extension.
func (c Chord) writeSeventh(b *strings.Builder, num func() string) {
	switch {
	case c.Six && c.Ext == 9:
		b.WriteString("6/9")
	case c.Six:
		b.WriteString("6")
	}
	switch c.Seventh {
	case SeventhMinor:
		b.WriteString(num())
	case SeventhMajor:
		b.WriteString("maj" + num())
	case SeventhDiminished:
		b.WriteString("dim" + num())
	}
}

// Printed is the chord symbol as a page shows it: Root, its accidental
// (-2..2), the rest of the symbol with ♭ and ♯ in it, and the slash bass
// the same way (Bass empty for none).
func (c Chord) Printed() (root string, rootAlter int, rest string, bass string, bassAlter int) {
	if c.NoChord {
		return "N.C.", 0, "", "", 0
	}
	root, rootAlter = string(Steps[c.Root.Step]), int(c.Root.Alter)
	rest = c.suffix(true)
	if c.HasBass {
		bass, bassAlter = string(Steps[c.Bass.Step]), int(c.Bass.Alter)
	}
	return
}

// ---------- sound ----------

// Tones are the chord's notes as semitones above its root, lowest first,
// without repeats: the triad (or its sus or power form), sixth, seventh,
// extensions (a 13th over a major or dominant chord leaves its 11th out,
// as players voice it), alterations in place of the degrees they alter,
// additions, less what is left out.
func (c Chord) Tones() []int {
	if c.NoChord {
		return nil
	}
	third, fifth := 4, 7
	switch c.Quality {
	case ChordMinor:
		third = 3
	case ChordDiminished, ChordHalfDiminished:
		third, fifth = 3, 6
	case ChordAugmented:
		fifth = 8
	case ChordSus2:
		third = 2
	case ChordSus4:
		third = 5
	case ChordPower:
		third = -1
	}
	deg := map[int]int{1: 0, 3: third, 5: fifth}
	if third < 0 {
		delete(deg, 3)
	}
	if c.Six {
		deg[6] = 9
	}
	switch c.Seventh {
	case SeventhMinor:
		deg[7] = 10
	case SeventhMajor:
		deg[7] = 11
	case SeventhDiminished:
		deg[7] = 9
	}
	if c.Ext >= 9 {
		deg[9] = 14
	}
	if c.Ext >= 11 && (c.Ext == 11 || c.Quality == ChordMinor || c.Quality == ChordSus4) {
		deg[11] = 17
	}
	if c.Ext >= 13 {
		deg[13] = 21
	}
	if c.Alt {
		delete(deg, 9)
		delete(deg, 13)
		deg[5] = 6 // the #11 / b5
		deg[90] = 13
		deg[91] = 15
		deg[92] = 20
	}
	natural := map[int]int{5: 7, 9: 14, 11: 17, 13: 21}
	extra := 100
	for _, a := range c.Alters {
		v := natural[a.N] + a.Alter
		if a.N == 5 || a.N == 9 || a.N == 13 {
			// A b9 replaces the 9; a #9 beside a b9 is its own.
			if old, ok := deg[a.N]; ok && old != natural[a.N] {
				deg[extra] = v
				extra++
				continue
			}
			deg[a.N] = v
			continue
		}
		deg[a.N] = v // #11
	}
	addSemis := map[int]int{2: 2, 4: 5, 6: 9, 9: 14, 11: 17, 13: 21}
	for _, a := range c.Adds {
		deg[extra] = addSemis[a.N] + a.Alter
		extra++
	}
	for _, o := range c.Omits {
		delete(deg, o)
	}
	seen := map[int]bool{}
	var out []int
	for _, v := range deg {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Ints(out)
	return out
}

// BassPitch is the chord's lowest note's name: the slash bass, or the root.
func (c Chord) BassPitch() Pitch {
	if c.HasBass {
		return c.Bass
	}
	return c.Root
}

// Transpose moves the chord by an interval, spelled as the interval
// implies (a B-flat clarinet's written chord over a concert C7 is D7).
func (c Chord) Transpose(iv Interval) Chord {
	if c.NoChord || iv == (Interval{}) {
		return c
	}
	mv := func(p Pitch) Pitch {
		p.Octave = 4
		q := p.Transpose(iv)
		q.Octave = 0
		return q
	}
	c = c.Clone()
	c.Root = mv(c.Root)
	if c.HasBass {
		c.Bass = mv(c.Bass)
	}
	return c
}

// Clone copies the chord's lists.
func (c Chord) Clone() Chord {
	c.Alters = append([]Degree(nil), c.Alters...)
	c.Adds = append([]Degree(nil), c.Adds...)
	c.Omits = append([]int(nil), c.Omits...)
	return c
}

// ChordAt is the chord symbol of a line in force at a written position:
// the last at or before it, nil before the first.
func (l *Line) ChordAt(p Pos) *ChordSymbol {
	var out *ChordSymbol
	for i := range l.Chords {
		if !posLess(p, l.Chords[i].At) {
			out = &l.Chords[i]
		}
	}
	return out
}

// SortChords puts a line's chord symbols in order.
func (l *Line) SortChords() {
	sort.SliceStable(l.Chords, func(i, j int) bool { return posLess(l.Chords[i].At, l.Chords[j].At) })
}
