package score

// lyrics.go — the words sung under the notes.
//
// A lyric is a mark on the note it is sung on, one mark a verse: "ly" is
// the first verse, "ly2" … "ly8" the others. The value is the syllable as
// printed, and its last character says how it joins what follows:
//
//	qC4(ly=Hal-) qD4(ly=le-) qE4(ly=lu-) hF4(ly=jah_) qG4 qr
//
// A trailing "-" hyphenates the syllable to the verse's next one (a word
// goes on); a trailing "_" holds it under the notes after it with an
// extender line — a melisma — up to the verse's next syllable or the next
// rest. Neither: the word ends there. A value needing more than letters,
// digits and . _ + # / - is quoted by the voice grammar as any mark's is:
// (ly="don't", ly2="ah,").
//
// Keeping a syllable on its note, not in a separate text the notes are
// counted against, is what an editor's edits need: inserting a note before
// a word leaves every syllable on the note it was on. It is also what
// MusicXML does (a <lyric> belongs to its <note>), so verses go in and out
// of other programs without being re-aligned.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// MaxVerses is how many verses of lyrics a line holds.
const MaxVerses = 8

// Lyric is one syllable of one verse.
type Lyric struct {
	// Verse is 1-based.
	Verse int
	// Text is the syllable as printed.
	Text string
	// Hyphen joins it to the verse's next syllable (the word goes on);
	// Extend holds it under the notes after it with an extender line.
	Hyphen, Extend bool
}

// LyricMark is the mark name of a verse: "ly", "ly2" … "ly8".
func LyricMark(verse int) string {
	if verse <= 1 {
		return "ly"
	}
	return "ly" + strconv.Itoa(verse)
}

// LyricVerse is the verse a mark name holds, or 0 for a name that is not a
// lyric.
func LyricVerse(name string) int {
	if name == "ly" {
		return 1
	}
	if len(name) == 3 && strings.HasPrefix(name, "ly") && name[2] >= '2' && name[2] <= '0'+MaxVerses {
		return int(name[2] - '0')
	}
	return 0
}

// ParseLyric reads a lyric mark's value: the syllable and how it joins on.
func ParseLyric(verse int, value string) Lyric {
	l := Lyric{Verse: verse, Text: value}
	switch {
	case len(value) > 1 && strings.HasSuffix(value, "-"):
		l.Text, l.Hyphen = value[:len(value)-1], true
	case len(value) > 1 && strings.HasSuffix(value, "_"):
		l.Text, l.Extend = value[:len(value)-1], true
	}
	return l
}

// Value is the mark value a lyric is stored as.
func (l Lyric) Value() string {
	switch {
	case l.Hyphen:
		return l.Text + "-"
	case l.Extend:
		return l.Text + "_"
	}
	return l.Text
}

// Mark is the lyric as an event mark.
func (l Lyric) Mark() Mark { return Mark{Name: LyricMark(l.Verse), Value: l.Value()} }

// Lyrics are an event's syllables, by verse.
func (e *Event) Lyrics() []Lyric {
	var out []Lyric
	for _, m := range e.Marks {
		if v := LyricVerse(m.Name); v > 0 && m.Value != "" {
			out = append(out, ParseLyric(v, m.Value))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Verse < out[j].Verse })
	return out
}

// Lyric is an event's syllable in a verse.
func (e *Event) Lyric(verse int) (Lyric, bool) {
	name := LyricMark(verse)
	for _, m := range e.Marks {
		if m.Name == name && m.Value != "" {
			return ParseLyric(verse, m.Value), true
		}
	}
	return Lyric{}, false
}

// SetLyric puts a syllable on an event in its verse, replacing what was
// there; an empty Text takes the verse's syllable off.
func (e *Event) SetLyric(l Lyric) {
	name := LyricMark(l.Verse)
	out := e.Marks[:0:0]
	placed := false
	for _, m := range e.Marks {
		if m.Name != name {
			out = append(out, m)
			continue
		}
		if !placed && l.Text != "" {
			out = append(out, l.Mark())
			placed = true
		}
	}
	if !placed && l.Text != "" {
		out = append(out, l.Mark())
	}
	e.Marks = out
}

// Verses is how many verses a line's lyrics run to: the highest verse any
// of its notes has a syllable in.
func (l *Line) Verses() int {
	n := 0
	for _, st := range l.Staves {
		for _, v := range st.Voices {
			for _, m := range v.Measures {
				for i := range m.Events {
					for _, mk := range m.Events[i].Marks {
						n = max(n, LyricVerse(mk.Name))
					}
				}
			}
		}
	}
	return n
}

// Syllabic is where a syllable stands in its word, as MusicXML names it.
type Syllabic uint8

const (
	SyllableSingle Syllabic = iota // a word of one syllable
	SyllableBegin                  // the first of a word's syllables
	SyllableMiddle
	SyllableEnd
)

func (s Syllabic) String() string {
	return [...]string{"single", "begin", "middle", "end"}[s]
}

// LyricAt is one syllable of a voice, where it is sung.
type LyricAt struct {
	Lyric
	// Bar is 1-based, Event an index into its measure.
	Bar, Event int
	// Syllabic is the syllable's place in its word: a hyphen before it or
	// after it.
	Syllabic Syllabic
	// Until is the last event a syllable is sung over: the event itself,
	// or with an extender the last note before the verse's next syllable,
	// a rest or the voice's end. UntilBar is its bar.
	UntilBar, Until int
}

// VoiceLyrics are a voice's syllables in one verse, in order, each with its
// place in its word and the notes an extender holds it over. Grace notes
// carry none.
func VoiceLyrics(v *Voice, verse int) []LyricAt {
	var out []LyricAt
	hyphened := false
	open := -1 // the syllable an extender is still running from
	for bi, m := range v.Measures {
		for ei := range m.Events {
			e := &m.Events[ei]
			if e.Grace != NotGrace {
				continue
			}
			if e.Kind != EventChord {
				open = -1
				continue
			}
			l, ok := e.Lyric(verse)
			if !ok {
				if open >= 0 {
					out[open].UntilBar, out[open].Until = bi+1, ei
				}
				continue
			}
			at := LyricAt{Lyric: l, Bar: bi + 1, Event: ei, UntilBar: bi + 1, Until: ei}
			switch {
			case hyphened && l.Hyphen:
				at.Syllabic = SyllableMiddle
			case hyphened:
				at.Syllabic = SyllableEnd
			case l.Hyphen:
				at.Syllabic = SyllableBegin
			}
			hyphened = l.Hyphen
			out = append(out, at)
			open = -1
			if l.Extend {
				open = len(out) - 1
			}
		}
		if len(m.Events) == 0 {
			open = -1
		}
	}
	return out
}

// LyricText is a verse of a voice as words: syllables joined by hyphens,
// words by spaces ("Hal-le-lu-jah, a-men") — what the CLI's inspect prints.
func LyricText(v *Voice, verse int) string {
	var b strings.Builder
	for i, l := range VoiceLyrics(v, verse) {
		if i > 0 && (l.Syllabic == SyllableSingle || l.Syllabic == SyllableBegin) {
			b.WriteByte(' ')
		}
		b.WriteString(l.Text)
		if l.Hyphen {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// lyricProblem says what is wrong with a lyric on an event, or "".
func lyricProblem(e *Event, mk Mark) string {
	switch {
	case mk.Value == "":
		return fmt.Sprintf("lyric %s has no syllable", mk.Name)
	case e.Kind != EventChord:
		return fmt.Sprintf("lyric %q on a rest (only notes are sung)", mk.Value)
	case e.Grace != NotGrace:
		return fmt.Sprintf("lyric %q on a grace note", mk.Value)
	}
	return ""
}
