package score

// grammar.go — a voice's music as text.
//
// A .fscore stores each voice as compact text, one bar to a line, the way a
// .ftrk stores patterns as tracker text: it diffs in the Git window, reads
// in the Claude panel, and is what the editor copies to the clipboard.
//
//	| q.Bb4(mp) eC5 hD5~
//	| qD5 3{eEb5 eD5 eC5} hBb4+D5+F5(ferm)
//	| /eA4 qBb4 qr h_
//	| R
//	|
//
// A voice is bars separated by "|". One "|" before the first bar and one
// after the last are optional; whitespace and line breaks between tokens do
// not matter, and "| |" is an empty bar (nothing written: the voice is
// silent there). Each bar is a list of events and tuplet brackets.
//
// An event is, in order:
//
//	grace    optional: "/" an acciaccatura (slashed), "^" an appoggiatura.
//	         Grace notes take no time in the bar.
//	duration a letter — b breve, w whole, h half, q quarter, e eighth,
//	         s 16th, t 32nd, x 64th, y 128th, z 256th — and up to three
//	         dots: "q." is a dotted quarter, "e.." a double-dotted eighth.
//	         Left out, the event takes the duration of the one before it
//	         in the voice (the printer always writes it).
//	body     "r" a rest; "_" a space (takes time, prints nothing); or one
//	         or more pitches joined by "+" — a chord: "qC4+E4+G4".
//	marks    optional: "(" names ")" separated by commas: "(p)",
//	         "(stacc,acc)", "(tech=pizz.)", "(\"dolce\")".
//	hairpin  optional, before or after the marks: "<" opens a crescendo
//	         hairpin, ">" a diminuendo — shorthand for "(cresc)" and
//	         "(dim)", so "h.C5<" is "h.C5(cresc)" (the printer writes the
//	         mark). Inside parentheses ">" is still an accent.
//
// "R" instead of a duration and body is a bar rest: it fills the bar,
// whatever its meter ("R(ferm)" is allowed).
//
// A pitch is a capital letter, accidentals — "#", "##" (or "x"), "b", "bb",
// "n" for a natural — and a scientific octave (C4 is middle C, MIDI 60;
// octaves run -1..9). Pitches are concert pitch and spelled exactly: the
// key signature never changes what a pitch means, only whether its
// accidental is printed. After the octave, per note:
//
//	~  tie into the same pitch in the next event of the voice
//	!  always print the accidental (a courtesy natural in a sharp key)
//	?  print it in parentheses (cautionary)
//
// A tuplet is "n{" … "}": n notes in the time of the usual number (3 in 2,
// 2 in 3, 5, 6 or 7 in 4, 9 in 8 — DefaultTupletDen), or "n:m{" for any
// other: "3{eC4 eD4 eE4}", "5:3{s… }". Tuplets nest and stay inside a bar.
//
// A mark is a name ([A-Za-z0-9_]), "name=value" (value bare when it is
// letters, digits and . _ + # / -, otherwise double-quoted with \" and \\),
// or a quoted string alone, which is the text mark: "(\"dolce\")" is
// (text="dolce"). Inside the parentheses ". ' > _ ^" are staccato,
// staccatissimo, accent, tenuto and marcato. marks.go has the vocabulary;
// unknown names are kept as written.
//
// The printer's output is canonical: explicit durations, one bar per line,
// trailing empty bars dropped. Parse(Format(v)) is v, and Format(Parse(t))
// is the canonical form of t.

import (
	"fmt"
	"strconv"
	"strings"
)

// SyntaxError is a voice that will not parse.
type SyntaxError struct {
	Bar int // 1-based bar in the voice
	Col int // 1-based byte in the bar's text
	Msg string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("bar %d, col %d: %s", e.Bar, e.Col, e.Msg)
}

// ParseVoice reads a voice's text.
func ParseVoice(text string) (v *Voice, err error) {
	defer RecoverOverflow(&err)
	bars := splitBars(text)
	if len(bars) > MaxBars {
		return nil, &SyntaxError{Bar: MaxBars + 1, Col: 1, Msg: fmt.Sprintf("a voice holds at most %d bars", MaxBars)}
	}
	v = &Voice{Measures: make([]Measure, 0, len(bars))}
	var dur Duration
	haveDur := false
	for i, b := range bars {
		m, err := parseMeasure(b, i+1, &dur, &haveDur)
		if err != nil {
			return nil, err
		}
		v.Measures = append(v.Measures, m)
	}
	v.Trim()
	return v, nil
}

// ParseMeasure reads one bar's text (no "|").
func ParseMeasure(text string) (m Measure, err error) {
	defer RecoverOverflow(&err)
	var dur Duration
	have := false
	return parseMeasure(text, 1, &dur, &have)
}

// Trim drops trailing empty bars, which mean the same as no bar at all.
func (v *Voice) Trim() {
	n := len(v.Measures)
	for n > 0 && len(v.Measures[n-1].Events) == 0 {
		n--
	}
	v.Measures = v.Measures[:n]
}

// Measure returns bar (1-based), or an empty one past the voice's end.
func (v *Voice) Measure(bar int) Measure {
	if bar < 1 || bar > len(v.Measures) {
		return Measure{}
	}
	return v.Measures[bar-1]
}

// splitBars cuts a voice at its top-level "|" (not inside quotes), after
// dropping one leading and one trailing "|".
func splitBars(text string) []string {
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, "|") {
		t = t[1:]
	}
	if strings.HasSuffix(t, "|") && !inQuoteAtEnd(t) {
		t = t[:len(t)-1]
	}
	if strings.TrimSpace(t) == "" {
		return nil
	}
	var out []string
	start, quoted := 0, false
	for i := 0; i < len(t); i++ {
		switch c := t[i]; {
		case quoted && c == '\\':
			i++
		case c == '"':
			quoted = !quoted
		case !quoted && c == '|':
			out = append(out, t[start:i])
			start = i + 1
		}
	}
	return append(out, t[start:])
}

// inQuoteAtEnd reports whether the last byte of t is inside a string.
func inQuoteAtEnd(t string) bool {
	quoted := false
	for i := 0; i < len(t)-1; i++ {
		switch {
		case quoted && t[i] == '\\':
			i++
		case t[i] == '"':
			quoted = !quoted
		}
	}
	return quoted
}

type token struct {
	s   string
	col int
}

// tokenize splits a bar at whitespace, keeping "( … )" and quotes whole,
// and making "n{" and "}" tokens of their own.
func tokenize(s string) ([]token, error) {
	var out []token
	i := 0
	for i < len(s) {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			i++
			continue
		}
		if c == '}' {
			out = append(out, token{"}", i})
			i++
			continue
		}
		start := i
		depth, quoted := 0, false
	scan:
		for i < len(s) {
			c := s[i]
			switch {
			case quoted:
				if c == '\\' {
					i++
				} else if c == '"' {
					quoted = false
				}
			case c == '"':
				quoted = true
			case c == '(':
				depth++
			case c == ')':
				depth--
			case depth == 0 && (c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '}'):
				break scan
			case depth == 0 && c == '{':
				i++
				break scan
			}
			i++
		}
		if quoted {
			return nil, &SyntaxError{Col: start + 1, Msg: "unterminated string"}
		}
		if depth != 0 {
			return nil, &SyntaxError{Col: start + 1, Msg: "unbalanced parentheses in " + strconv.Quote(s[start:i])}
		}
		out = append(out, token{s[start:i], start})
	}
	return out, nil
}

func parseMeasure(text string, bar int, dur *Duration, haveDur *bool) (Measure, error) {
	fail := func(col int, format string, a ...any) error {
		return &SyntaxError{Bar: bar, Col: col + 1, Msg: fmt.Sprintf(format, a...)}
	}
	toks, err := tokenize(text)
	if err != nil {
		se := err.(*SyntaxError)
		se.Bar = bar
		return Measure{}, se
	}
	var m Measure
	var stack []int // open tuplets, 1-based
	var used []bool // tuplet has an event in it (or in a child)
	for _, tk := range toks {
		switch {
		case tk.s == "}":
			if len(stack) == 0 {
				return Measure{}, fail(tk.col, "\"}\" closes no tuplet")
			}
			if !used[stack[len(stack)-1]-1] {
				return Measure{}, fail(tk.col, "empty tuplet")
			}
			stack = stack[:len(stack)-1]
		case strings.HasSuffix(tk.s, "{"):
			t, err := parseTupletOpen(tk.s[:len(tk.s)-1])
			if err != nil {
				return Measure{}, fail(tk.col, "%v", err)
			}
			if len(stack) > 0 {
				t.Parent = stack[len(stack)-1]
			}
			m.Tuplets = append(m.Tuplets, t)
			used = append(used, false)
			stack = append(stack, len(m.Tuplets))
		default:
			e, col, err := parseEvent(tk.s, dur, haveDur)
			if err != nil {
				return Measure{}, fail(tk.col+col, "%v", err)
			}
			if len(stack) > 0 {
				e.Tuplet = stack[len(stack)-1]
				for t := e.Tuplet; t != 0; t = m.Tuplets[t-1].Parent {
					used[t-1] = true
				}
			}
			m.Events = append(m.Events, e)
		}
	}
	if len(stack) > 0 {
		return Measure{}, fail(len(text), "tuplet not closed by the end of the bar")
	}
	return m, nil
}

func parseTupletOpen(s string) (Tuplet, error) {
	ns, ds, hasDen := strings.Cut(s, ":")
	n, err := strconv.Atoi(ns)
	if err != nil || n < 1 || n > 64 {
		return Tuplet{}, fmt.Errorf("bad tuplet %q (want e.g. 3{ or 5:4{)", s+"{")
	}
	d := DefaultTupletDen(n)
	if hasDen {
		d, err = strconv.Atoi(ds)
		if err != nil || d < 1 || d > 64 {
			return Tuplet{}, fmt.Errorf("bad tuplet %q", s+"{")
		}
	}
	return Tuplet{Num: n, Den: d}, nil
}

// parseEvent reads one event token. On an error it says where in the token.
func parseEvent(tok string, dur *Duration, haveDur *bool) (Event, int, error) {
	var e Event
	i := 0
	switch tok[0] {
	case '/':
		e.Grace, i = Acciaccatura, 1
	case '^':
		e.Grace, i = Appoggiatura, 1
	}
	if i >= len(tok) {
		return e, i, fmt.Errorf("grace mark with no note")
	}
	if tok[i] == 'R' {
		if e.Grace != NotGrace {
			return e, i, fmt.Errorf("a bar rest cannot be a grace note")
		}
		e.Kind = EventBarRest
		i++
	} else {
		if k := strings.IndexByte(durLetters, tok[i]); k >= 0 {
			d := Duration{Base: DurBase(k - 1)}
			i++
			for i < len(tok) && tok[i] == '.' {
				d.Dots++
				i++
			}
			if d.Dots > 3 {
				return e, i, fmt.Errorf("at most three dots")
			}
			*dur, *haveDur = d, true
		} else if !*haveDur {
			return e, i, fmt.Errorf("%q has no duration (start with b w h q e s t x y z)", tok)
		}
		e.Dur = *dur
		if i >= len(tok) {
			return e, i, fmt.Errorf("%q: a duration needs a pitch, r or _ after it", tok)
		}
		switch tok[i] {
		case 'r':
			e.Kind = EventRest
			i++
		case '_':
			e.Kind = EventSpace
			i++
		default:
			for {
				p, n, err := scanPitch(tok[i:])
				if err != nil {
					return e, i, err
				}
				i += n
				note := Note{Pitch: p}
			flags:
				for i < len(tok) {
					switch tok[i] {
					case '~':
						note.Tie = true
					case '!':
						note.Acc = AccForce
					case '?':
						note.Acc = AccCautionary
					default:
						break flags
					}
					i++
				}
				e.Notes = append(e.Notes, note)
				if i < len(tok) && tok[i] == '+' {
					i++
					continue
				}
				break
			}
		}
		if e.Grace != NotGrace && e.Kind != EventChord {
			return e, 0, fmt.Errorf("a grace note must be a note")
		}
	}
	// A hairpin shorthand: "<" opens a crescendo hairpin and ">" a
	// diminuendo, before the marks or after them: "h.C5<" is h.C5(cresc).
	// Outside the parentheses neither means anything else (an accent is
	// "(>)"). The printer writes the marks themselves.
	pin := ""
	hairpin := func(c byte) bool {
		switch c {
		case '<':
			pin = "cresc"
		case '>':
			pin = "dim"
		default:
			return false
		}
		return true
	}
	end := len(tok)
	if i < end && hairpin(tok[i]) {
		i++
	} else if end > i+1 && tok[i] == '(' && hairpin(tok[end-1]) {
		end--
	}
	tok = tok[:end]
	if i < len(tok) && tok[i] == '(' {
		if tok[len(tok)-1] != ')' {
			return e, len(tok) - 1, fmt.Errorf("text after the marks' \")\"")
		}
		marks, err := parseMarks(tok[i+1 : len(tok)-1])
		if err != nil {
			return e, i + 1, err
		}
		e.Marks = marks
		i = len(tok)
	}
	if i != len(tok) {
		if pin != "" && (tok[i] == '<' || tok[i] == '>') {
			return e, i, fmt.Errorf("one hairpin (\"<\" or \">\") to an event")
		}
		return e, i, fmt.Errorf("unexpected %q in %q", tok[i:], tok)
	}
	if pin != "" {
		for _, m := range e.Marks {
			if m.Name == "cresc" || m.Name == "dim" {
				return e, 0, fmt.Errorf("%q opens two hairpins", tok)
			}
		}
		e.Marks = append(e.Marks, Mark{Name: pin})
	}
	return e, 0, nil
}

func parseMarks(s string) ([]Mark, error) {
	var parts []string
	start, quoted := 0, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case quoted && c == '\\':
			i++
		case c == '"':
			quoted = !quoted
		case !quoted && c == ',':
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	var out []Mark
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, fmt.Errorf("empty mark")
		}
		if a, ok := Aliases[p]; ok {
			out = append(out, Mark{Name: a})
			continue
		}
		if p[0] == '"' {
			v, err := unquote(p)
			if err != nil {
				return nil, err
			}
			out = append(out, Mark{Name: "text", Value: v})
			continue
		}
		name, val, hasVal := strings.Cut(p, "=")
		if !validMarkName(name) {
			return nil, fmt.Errorf("bad mark %q", p)
		}
		m := Mark{Name: name}
		if hasVal {
			if strings.HasPrefix(val, "\"") {
				v, err := unquote(val)
				if err != nil {
					return nil, err
				}
				m.Value = v
			} else if bareValue(val) {
				m.Value = val
			} else {
				return nil, fmt.Errorf("mark %q: quote a value like that", p)
			}
			if m.Value == "" {
				return nil, fmt.Errorf("mark %q has an empty value", p)
			}
		}
		out = append(out, m)
	}
	return out, nil
}

func validMarkName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

func bareValue(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("._+#/-", c) >= 0) {
			return false
		}
	}
	return true
}

func unquote(s string) (string, error) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", fmt.Errorf("bad string %s", s)
	}
	var b strings.Builder
	for i := 1; i < len(s)-1; i++ {
		c := s[i]
		if c == '\\' {
			i++
			if i >= len(s)-1 {
				return "", fmt.Errorf("bad escape in %s", s)
			}
			c = s[i]
			if c != '"' && c != '\\' {
				return "", fmt.Errorf("bad escape \\%c in %s (only \\\" and \\\\)", c, s)
			}
		} else if c == '"' {
			return "", fmt.Errorf("stray quote in %s", s)
		}
		b.WriteByte(c)
	}
	return b.String(), nil
}

func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		if s[i] == '"' || s[i] == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	b.WriteByte('"')
	return b.String()
}

// ---------- printing ----------

// FormatVoice writes a voice canonically: one bar to a line, each opened
// by "|", and a closing "|". An empty voice is "".
func FormatVoice(v *Voice) string {
	n := len(v.Measures)
	for n > 0 && len(v.Measures[n-1].Events) == 0 {
		n--
	}
	if n == 0 {
		return ""
	}
	var b strings.Builder
	for _, m := range v.Measures[:n] {
		b.WriteString("|")
		if t := FormatMeasure(m); t != "" {
			b.WriteByte(' ')
			b.WriteString(t)
		}
		b.WriteByte('\n')
	}
	b.WriteString("|\n")
	return b.String()
}

// FormatMeasure writes one bar's events, tuplets bracketed.
func FormatMeasure(m Measure) string {
	var b strings.Builder
	var open []int
	sep := func() {
		if b.Len() > 0 {
			last := b.String()[b.Len()-1]
			if last != '{' {
				b.WriteByte(' ')
			}
		}
	}
	for _, e := range m.Events {
		chain := tupletChain(m, e.Tuplet)
		k := 0
		for k < len(open) && k < len(chain) && open[k] == chain[k] {
			k++
		}
		for len(open) > k {
			b.WriteByte('}')
			open = open[:len(open)-1]
		}
		for _, t := range chain[k:] {
			sep()
			tp := m.Tuplets[t-1]
			b.WriteString(strconv.Itoa(tp.Num))
			if tp.Den != DefaultTupletDen(tp.Num) {
				b.WriteByte(':')
				b.WriteString(strconv.Itoa(tp.Den))
			}
			b.WriteByte('{')
			open = append(open, t)
		}
		sep()
		b.WriteString(FormatEvent(e))
	}
	for range open {
		b.WriteByte('}')
	}
	return b.String()
}

// tupletChain is the tuplets an event is in, outermost first.
func tupletChain(m Measure, t int) []int {
	var out []int
	for t > 0 && t <= len(m.Tuplets) && len(out) <= len(m.Tuplets) {
		out = append(out, t)
		t = m.Tuplets[t-1].Parent
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// FormatEvent writes one event as the grammar spells it.
func FormatEvent(e Event) string {
	var b strings.Builder
	switch e.Grace {
	case Acciaccatura:
		b.WriteByte('/')
	case Appoggiatura:
		b.WriteByte('^')
	}
	switch e.Kind {
	case EventBarRest:
		b.WriteByte('R')
	default:
		b.WriteString(e.Dur.String())
		switch e.Kind {
		case EventRest:
			b.WriteByte('r')
		case EventSpace:
			b.WriteByte('_')
		default:
			for i, n := range e.Notes {
				if i > 0 {
					b.WriteByte('+')
				}
				b.WriteString(n.Pitch.String())
				switch n.Acc {
				case AccForce:
					b.WriteByte('!')
				case AccCautionary:
					b.WriteByte('?')
				}
				if n.Tie {
					b.WriteByte('~')
				}
			}
		}
	}
	if len(e.Marks) > 0 {
		b.WriteByte('(')
		for i, m := range e.Marks {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(formatMark(m))
		}
		b.WriteByte(')')
	}
	return b.String()
}

func formatMark(m Mark) string {
	if m.Name == "text" && m.Value != "" {
		return quote(m.Value)
	}
	if m.Value == "" {
		return m.Name
	}
	if bareValue(m.Value) {
		return m.Name + "=" + m.Value
	}
	return m.Name + "=" + quote(m.Value)
}

// ---------- timing within a bar ----------

// Actual is an event's length in the bar: its written value scaled by
// every tuplet it is in. A grace note is zero; a bar rest is barLen.
func (m Measure) Actual(e Event, barLen Frac) Frac {
	switch {
	case e.Grace != NotGrace:
		return Frac{0, 1}
	case e.Kind == EventBarRest:
		return barLen
	}
	v := e.Dur.Value()
	for t := e.Tuplet; t > 0 && t <= len(m.Tuplets); t = m.Tuplets[t-1].Parent {
		v = v.Mul(m.Tuplets[t-1].Ratio())
	}
	return v
}

// Offsets are each event's start in the bar, and the bar's filled length.
func (m Measure) Offsets(barLen Frac) ([]Frac, Frac) {
	out := make([]Frac, len(m.Events))
	at := Frac{0, 1}
	for i, e := range m.Events {
		out[i] = at
		at = at.Add(m.Actual(e, barLen))
	}
	return out, at
}
