package musicxml

// export.go — a score as MusicXML 4.0, partwise: what "Export for
// Sibelius" writes, and what MuseScore, Dorico and Finale open too.
//
// Each line is a part, its staves the part's staves (a piano's two in one
// part, braced), each staff's voices numbered 1–4 and 5–8 as Sibelius
// numbers them. Notes are written as they read on the page: a transposing
// staff at its written pitch with a <transpose> saying how it sounds, the
// key a part's own, an ottava's notes at their true pitch under an
// octave-shift (MusicXML's convention). What the page shows is written
// out — accidentals the bar needs, beams by the meter (the engraver's
// groups), tuplet brackets nested as written, ties, slurs, dynamics,
// hairpins and text crescendos, articulations, ornaments with their
// accidentals, tremolos, fermatas, arpeggios, glissandi, grace notes, clef
// changes, ottava and pedal lines, technique and expression text; and over
// the parts the score's maps: key and time signatures, tempo marks with
// their metronome marks (and rit./accel. with their dashes), rehearsal
// marks, repeats, endings, segno, coda, To Coda, Fine, D.C. and D.S. The
// page layout goes in as hints — paper, margins and staff size in
// <defaults>, the writer's system and page breaks as <print> — and the
// title, subtitle and composer as credits.
//
// Playback is in it too where MusicXML has a place for it: <sound tempo>,
// <sound dynamics>, a part's MIDI program, volume and pan, and a note's
// velocity (vel=) as its dynamics attribute. The library instrument a line
// plays is named in its <virtual-instrument>, so a score exported and
// imported again plays what it played.
//
// What MusicXML cannot carry is the game's and the mixer's: params,
// bindings, markers, a section's loop and next, effects, buses, lanes,
// swing and humanise. A score is the place for those; this is notation.

import (
	"archive/zip"
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/score"
	"foundry/internal/score/io/gm"
)

// Software is what an export names in its <encoding>. A file that says it
// was written by this is read back with the parts only Foundry writes:
// stem directions, velocities, grace timing.
const Software = "Foundry Score"

// ExportOptions shape an export.
type ExportOptions struct {
	// NoLayout leaves the page layout out: no paper, margins, staff size
	// or breaks, so the program opening it lays the score out its own way.
	NoLayout bool
	// Kits, when set, is the drum map an instrument's percussion staves
	// are written by (nil from it: the General MIDI kit's); unset, the
	// library's (score.KitFor).
	Kits func(instrument string) *score.Kit
}

// ExportFile writes a score to a .musicxml (or .xml) file, or a
// compressed .mxl. The warnings are what the file could not hold.
func ExportFile(path string, s *score.Score, o ExportOptions) ([]string, error) {
	b, warns, err := Export(s, o)
	if err != nil {
		return warns, err
	}
	if strings.EqualFold(filepath.Ext(path), ".mxl") {
		if b, err = Compress(b, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))+".musicxml"); err != nil {
			return warns, err
		}
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return warns, err
		}
	}
	return warns, os.WriteFile(path, b, 0o644)
}

// Compress packs a MusicXML document as an .mxl: a zip holding the score
// and a META-INF/container.xml naming it (the mimetype entry first and
// stored, as the format asks).
func Compress(doc []byte, name string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	put := func(n string, b []byte, method uint16) error {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: n, Method: method})
		if err != nil {
			return err
		}
		_, err = f.Write(b)
		return err
	}
	container := `<?xml version="1.0" encoding="UTF-8"?>
<container>
  <rootfiles>
    <rootfile full-path="` + name + `" media-type="application/vnd.recordare.musicxml+xml"/>
  </rootfiles>
</container>
`
	if err := put("mimetype", []byte("application/vnd.recordare.musicxml"), zip.Store); err != nil {
		return nil, err
	}
	if err := put("META-INF/container.xml", []byte(container), zip.Deflate); err != nil {
		return nil, err
	}
	if err := put(name, doc, zip.Deflate); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Export writes a score as a MusicXML document. The warnings are what the
// file could not hold.
func Export(s *score.Score, o ExportOptions) (b []byte, warnings []string, err error) {
	defer score.RecoverOverflow(&err)
	if len(s.Lines) == 0 {
		return nil, nil, fmt.Errorf("musicxml: the score has no lines to write")
	}
	s = linkedTabsAsStaves(s)
	x := &exporter{s: s, o: o, starts: s.BarStarts(), bars: s.Measures()}
	seen := map[string]bool{}
	x.warn = func(format string, a ...any) {
		msg := fmt.Sprintf(format, a...)
		if !seen[msg] {
			seen[msg] = true
			warnings = append(warnings, msg)
		}
	}
	if x.div, err = divisions(s); err != nil {
		return nil, nil, err
	}
	x.stems = stemDirs(s)
	x.document()
	return x.w.b.Bytes(), warnings, nil
}

type exporter struct {
	s      *score.Score
	o      ExportOptions
	w      xw
	div    int64 // divisions a quarter
	starts []score.Frac
	bars   int
	warn   func(string, ...any)
	ids    int // directions given an id so far
	// stems is every stem's direction as the engraver draws it (stems.go).
	stems map[stemKey]bool
}

// divisions is the divisions a quarter every length and place in the
// score is a whole number of: the least common multiple of their
// denominators in quarters.
func divisions(s *score.Score) (int64, error) {
	l := int64(1)
	var bad error
	add := func(f score.Frac) {
		q := f.MulInt(4)
		if q.Sign() < 0 {
			q = q.Neg()
		}
		d := q.Den
		if d <= 0 {
			return
		}
		g := gcd64(l, d)
		if l/g > (1<<31)/d {
			bad = fmt.Errorf("musicxml: the score's rhythms need more than 2^31 divisions a quarter")
			return
		}
		l = l / g * d
	}
	for b := 1; b <= s.Measures(); b++ {
		add(s.BarLength(b))
	}
	for _, l := range s.Lines {
		for _, st := range l.Staves {
			for _, c := range st.Clefs {
				add(c.At.Offset)
			}
			for _, v := range st.Voices {
				for bi, m := range v.Measures {
					bl := s.BarLength(bi + 1)
					for _, e := range m.Events {
						add(m.Actual(e, bl))
					}
				}
			}
		}
	}
	for _, t := range s.Tempo {
		add(t.At.Offset)
		if t.HasTo {
			add(t.To.Offset)
		}
	}
	return l, bad
}

func gcd64(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	if a < 0 {
		return -a
	}
	return a
}

// dur is a length in divisions.
func (x *exporter) dur(f score.Frac) int64 {
	q := f.MulInt(4 * x.div)
	return q.Round()
}

// ---------- the document ----------

func (x *exporter) document() {
	s, w := x.s, &x.w
	w.b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="no"?>` + "\n")
	w.b.WriteString(`<!DOCTYPE score-partwise PUBLIC "-//Recordare//DTD MusicXML 4.0 Partwise//EN" "http://www.musicxml.org/dtds/partwise.dtd">` + "\n")
	w.open("score-partwise", attr{"version", "4.0"})
	title := s.Title
	if title == "" {
		title = s.Name
	}
	w.open("work")
	w.leaf("work-title", title)
	w.close()
	if s.Subtitle != "" {
		w.leaf("movement-title", s.Subtitle)
	}
	w.open("identification")
	if s.Composer != "" {
		w.leaf("creator", s.Composer, attr{"type", "composer"})
	}
	w.open("encoding")
	w.leaf("software", Software)
	for _, el := range []string{"accidental", "beam", "stem"} {
		w.empty("supports", attr{"element", el}, attr{"type", "yes"})
	}
	if !x.o.NoLayout {
		w.empty("supports", attr{"attribute", "new-system"}, attr{"element", "print"}, attr{"type", "yes"}, attr{"value", "yes"})
		w.empty("supports", attr{"attribute", "new-page"}, attr{"element", "print"}, attr{"type", "yes"}, attr{"value", "yes"})
	}
	w.close()
	w.close()
	pageW, pageH := x.defaults()
	x.credits(title, pageW, pageH)
	x.partList()
	for li, l := range s.Lines {
		x.part(li, l)
	}
	w.close()
}

// tenths is millimetres in the file's unit: a staff is 40 tenths high.
func (x *exporter) tenths(mm float64) float64 {
	return mm / x.s.Layout.StaffOrDefault() * 40
}

// defaults writes the scaling and the page; it returns the page's size in
// tenths for the credits.
func (x *exporter) defaults() (float64, float64) {
	lay, w := x.s.Layout, &x.w
	pw, ph := lay.Paper.PaperSize()
	if x.o.NoLayout {
		return x.tenths(pw), x.tenths(ph)
	}
	m := lay.Paper.MarginsOrDefault()
	w.open("defaults")
	w.open("scaling")
	w.leaf("millimeters", num(lay.StaffOrDefault()))
	w.leaf("tenths", "40")
	w.close()
	w.open("page-layout")
	w.leaf("page-height", num(x.tenths(ph)))
	w.leaf("page-width", num(x.tenths(pw)))
	w.open("page-margins", attr{"type", "both"})
	w.leaf("left-margin", num(x.tenths(m.Left)))
	w.leaf("right-margin", num(x.tenths(m.Right)))
	w.leaf("top-margin", num(x.tenths(m.Top)))
	w.leaf("bottom-margin", num(x.tenths(m.Bottom)))
	w.close()
	w.close()
	w.close()
	return x.tenths(pw), x.tenths(ph)
}

// credits are the title, subtitle and composer at the top of page one.
func (x *exporter) credits(title string, pw, ph float64) {
	s, w := x.s, &x.w
	m := s.Layout.Paper.MarginsOrDefault()
	top := ph - x.tenths(m.Top)
	credit := func(kind, text string, dx, dy float64, justify string, size int) {
		w.open("credit", attr{"page", "1"})
		w.leaf("credit-type", kind)
		w.leaf("credit-words", text, attr{"default-x", num(dx)}, attr{"default-y", num(dy)},
			attr{"font-size", itoa(size)}, attr{"justify", justify}, attr{"valign", "top"})
		w.close()
	}
	credit("title", title, pw/2, top, "center", 22)
	if s.Subtitle != "" {
		credit("subtitle", s.Subtitle, pw/2, top-45, "center", 14)
	}
	if s.Composer != "" {
		credit("composer", s.Composer, pw-x.tenths(m.Right), top-80, "right", 11)
	}
}

// kitFor is the drum map a line's percussion is written by.
func (x *exporter) kitFor(l *score.Line) *score.Kit {
	if x.o.Kits != nil {
		if k := x.o.Kits(l.Instrument); k != nil {
			return k
		}
		return score.GMKit
	}
	return l.Kit()
}

// percussive is a staff written as unpitched notes.
func percussive(st *score.Staff) bool { return st.Clef == "percussion" }

// drumKeys are the MIDI keys a line's percussion staves play, in order.
func drumKeys(l *score.Line) []int {
	set := map[int]bool{}
	for _, st := range l.Staves {
		if !percussive(st) {
			continue
		}
		for _, v := range st.Voices {
			for _, m := range v.Measures {
				for _, e := range m.Events {
					for _, n := range e.Notes {
						set[n.Pitch.MIDI()] = true
					}
				}
			}
		}
	}
	var out []int
	for k := range set {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func partID(li int) string { return fmt.Sprintf("P%d", li+1) }

// instID is a part's instrument: one for a pitched part, one per drum
// (by its MIDI key) on a kit.
func instID(li, key int) string {
	if key < 0 {
		return fmt.Sprintf("P%d-I1", li+1)
	}
	return fmt.Sprintf("P%d-I%d", li+1, key+1)
}

// lineChannels are the lines' MIDI channels (1-based): 10 for a kit, the
// others in turn.
func lineChannels(s *score.Score) []int {
	out := make([]int, len(s.Lines))
	next := 0
	for i, l := range s.Lines {
		kit := gm.Percussive(l.Instrument)
		for _, st := range l.Staves {
			kit = kit || percussive(st)
		}
		if kit {
			out[i] = gm.DrumChannel + 1
			continue
		}
		out[i] = next + 1
		next = (next + 1) % 16
		if next == gm.DrumChannel {
			next++
		}
	}
	return out
}

func (x *exporter) partList() {
	s, w := x.s, &x.w
	channels := lineChannels(s)
	w.open("part-list")
	// The family brackets, as bracketed part groups whose barlines join.
	groups := score.BracketGroups(s, nil)
	for li, l := range s.Lines {
		for gi, g := range groups {
			if g.First == li {
				w.open("part-group", attr{"type", "start"}, attr{"number", itoa(gi + 1)})
				w.leaf("group-symbol", "bracket")
				w.leaf("group-barline", "yes")
				w.close()
			}
		}
		w.open("score-part", attr{"id", partID(li)})
		w.leaf("part-name", l.Name)
		w.leaf("part-abbreviation", l.ShortName())
		virtual := func() {
			if l.Instrument == "" {
				return
			}
			w.open("virtual-instrument")
			w.leaf("virtual-library", "Foundry")
			w.leaf("virtual-name", l.Instrument)
			w.close()
		}
		keys := drumKeys(l)
		if len(keys) == 0 {
			w.open("score-instrument", attr{"id", instID(li, -1)})
			w.leaf("instrument-name", l.Name)
			virtual()
			w.close()
		} else {
			for i, k := range keys {
				w.open("score-instrument", attr{"id", instID(li, k)})
				w.leaf("instrument-name", x.kitFor(l).NameOf(score.SpellMIDI(k, 0, 1)))
				if i == 0 {
					virtual()
				}
				w.close()
			}
		}
		midiInst := func(id string, unpitched int) {
			w.open("midi-instrument", attr{"id", id})
			w.leaf("midi-channel", itoa(channels[li]))
			if channels[li] != gm.DrumChannel+1 {
				w.leaf("midi-program", itoa(gm.Program(l.Instrument)+1))
			}
			if unpitched >= 0 {
				w.leaf("midi-unpitched", itoa(unpitched+1))
			}
			w.leaf("volume", num(math.Round(float64(l.Volume)*10000)/100))
			w.leaf("pan", num(math.Round(float64(l.Pan)*90*100)/100))
			w.close()
		}
		if len(keys) == 0 {
			midiInst(instID(li, -1), -1)
		} else {
			for _, k := range keys {
				midiInst(instID(li, k), k)
			}
		}
		w.close()
		for gi, g := range groups {
			if g.Last == li {
				w.empty("part-group", attr{"type", "stop"}, attr{"number", itoa(gi + 1)})
			}
		}
	}
	w.close()
}

// ---------- a part ----------

// vref is an event of a voice: its bar (1-based) and index.
type vref struct{ bar, idx int }

// voiceState is what a voice carries from one event to the next, across
// bar lines.
type voiceState struct {
	seq []vref
	pos map[vref]int
	// open spanners: slurs (a count, numbered by depth), a hairpin
	// ("wedge" or "dashes"), a two-note tremolo's strokes.
	slurs   int
	hairpin string
	last    vref // the voice's last event
}

// staffState is a staff's lines: ottava (its size, 0 none) and pedal.
type staffState struct {
	ottava int
	pedal  bool
}

type partWriter struct {
	x      *exporter
	li     int
	l      *score.Line
	voices map[[2]int]*voiceState
	staves []*staffState
	// shift is how far the part's written key is from the concert key.
	shift int
	keys  []int // drum keys
	// kit is the line's drum map (its instrument's); places its staves'
	// voices on their tablature's strings.
	kit    *score.Kit
	places map[[2]int][][][]score.TabNote
}

func (x *exporter) part(li int, l *score.Line) {
	w := &x.w
	p := &partWriter{x: x, li: li, l: l, voices: map[[2]int]*voiceState{}, keys: drumKeys(l), kit: x.kitFor(l)}
	if len(l.Staves) > 0 {
		p.shift = fifthsShift(score.Interval{Diatonic: -l.Staves[0].Transpose.Diatonic, Chromatic: -l.Staves[0].Transpose.Chromatic})
	}
	for si, st := range l.Staves {
		p.staves = append(p.staves, &staffState{})
		for vi, v := range st.Voices {
			vs := &voiceState{pos: map[vref]int{}}
			for bi, m := range v.Measures {
				for ei := range m.Events {
					r := vref{bi + 1, ei}
					vs.pos[r] = len(vs.seq)
					vs.seq = append(vs.seq, r)
				}
			}
			if len(vs.seq) > 0 {
				vs.last = vs.seq[len(vs.seq)-1]
			}
			p.voices[[2]int{si, vi}] = vs
		}
	}
	w.open("part", attr{"id", partID(li)})
	for b := 1; b <= x.bars; b++ {
		p.measure(b)
	}
	w.close()
}

// event is the event a reference names.
func (p *partWriter) event(si, vi int, r vref) *score.Event {
	m := &p.l.Staves[si].Voices[vi].Measures[r.bar-1]
	return &m.Events[r.idx]
}

// neighbour is the voice's previous (dir -1) or next (dir 1) event that
// is not a grace note, if any.
func (p *partWriter) neighbour(si, vi int, r vref, dir int) (vref, *score.Event, bool) {
	vs := p.voices[[2]int{si, vi}]
	for k := vs.pos[r] + dir; k >= 0 && k < len(vs.seq); k += dir {
		e := p.event(si, vi, vs.seq[k])
		if e.Grace == score.NotGrace {
			return vs.seq[k], e, true
		}
	}
	return vref{}, nil, false
}

// item is a direction placed in a measure's first voice at an offset (the
// part-wide marks: tempo, rehearsal marks, jumps).
type item struct {
	at    score.Frac
	write func()
}

// writtenFifths is the part's key at a bar, as its first staff writes it.
func (p *partWriter) writtenFifths(bar int) int {
	f := p.x.s.KeyAt(bar).Fifths + p.shift
	for f > 7 {
		f -= 12
	}
	for f < -7 {
		f += 12
	}
	return f
}

func (p *partWriter) measure(bar int) {
	x, s, w := p.x, p.x.s, &p.x.w
	barLen := s.BarLength(bar)
	as := []attr{{"number", itoa(bar)}}
	if bar == 1 && s.Pickup.Sign() > 0 {
		as = []attr{{"number", "0"}, {"implicit", "yes"}}
	} else if s.Pickup.Sign() > 0 {
		as = []attr{{"number", itoa(bar - 1)}}
	}
	w.open("measure", as...)
	if !x.o.NoLayout && bar > 1 {
		if br, ok := s.Layout.BreakAt(bar); ok {
			if br.Page {
				w.empty("print", attr{"new-page", "yes"})
			} else {
				w.empty("print", attr{"new-system", "yes"})
			}
		}
	}
	p.leftBarline(bar)
	p.attributes(bar)

	var items []item
	if p.li == 0 {
		items = p.partItems(bar, barLen)
	}
	if chords := p.chordItems(bar, barLen); len(chords) > 0 {
		items = append(items, chords...)
		sort.SliceStable(items, func(i, j int) bool { return items[i].at.Less(items[j].at) })
	}
	first := true
	cursor := score.Frac{}
	for si, st := range p.l.Staves {
		clefs := p.clefsIn(si, bar)
		for vi := range st.Voices {
			m := st.Voices[vi].Measures
			var meas score.Measure
			if bar <= len(m) {
				meas = m[bar-1]
			}
			isFirst := si == 0 && vi == 0
			if len(meas.Events) == 0 && !isFirst {
				continue
			}
			if !first && cursor.Sign() > 0 {
				// Back to the bar's start for the next voice.
				w.open("backup")
				w.leaf("duration", fmt.Sprint(x.dur(cursor)))
				w.close()
			}
			first = false
			end := p.voice(si, vi, bar, meas, barLen, items, clefs, isFirst)
			if end.Less(barLen) {
				w.open("forward")
				w.leaf("duration", fmt.Sprint(x.dur(barLen.Sub(end))))
				w.leaf("voice", itoa(si*4+vi+1))
				w.leaf("staff", itoa(si+1))
				w.close()
				end = barLen
			}
			if isFirst {
				// Marks at the end of the bar (To Coda, Fine, D.S.).
				p.endItems(bar)
			}
			cursor = end
			clefs = nil
		}
		for _, c := range clefs {
			x.warn("%s: a clef change at %s is in a bar the staff has no notes in; it is left out", p.l.Name, s.FormatPos(c.At))
		}
	}
	p.rightBarline(bar)
	w.close()
}

// clefsIn are a staff's clef changes inside a bar (after its start).
func (p *partWriter) clefsIn(si, bar int) []score.ClefChange {
	var out []score.ClefChange
	if t := p.l.Staves[si].Tab; t != nil && t.Only {
		return nil // a TAB staff has no clef changes
	}
	for _, c := range p.l.Staves[si].Clefs {
		if c.At.Bar == bar && c.At.Offset.Sign() > 0 {
			out = append(out, c)
		}
	}
	return out
}

// clefAtStart is a clef change at a bar's start (bar > 1), if any.
func clefAtStart(st *score.Staff, bar int) (string, bool) {
	for _, c := range st.Clefs {
		if c.At.Bar == bar && c.At.Offset.IsZero() {
			return c.Clef, true
		}
	}
	if bar == 1 {
		return st.Clef, true
	}
	return "", false
}

// clefXML writes a clef.
func (x *exporter) clefXML(name string, number int) {
	w := &x.w
	sign, line, oct := "G", 2, 0
	switch name {
	case "bass":
		sign, line = "F", 4
	case "bass_8vb":
		sign, line, oct = "F", 4, -1
	case "bass_8va":
		sign, line, oct = "F", 4, 1
	case "alto":
		sign, line = "C", 3
	case "tenor":
		sign, line = "C", 4
	case "treble_8vb":
		oct = -1
	case "treble_8va":
		oct = 1
	case "percussion":
		sign, line = "percussion", 0
	case "tab":
		sign, line = "TAB", 5
	}
	w.open("clef", attr{"number", itoa(number)})
	w.leaf("sign", sign)
	if line > 0 {
		w.leaf("line", itoa(line))
	}
	if oct != 0 {
		w.leaf("clef-octave-change", itoa(oct))
	}
	w.close()
}

// attributes writes what changes at the bar's start: divisions (bar 1),
// key, time, staves, clefs, transposition.
func (p *partWriter) attributes(bar int) {
	x, s, w := p.x, p.x.s, &p.x.w
	key := bar == 1
	time := bar == 1
	if bar > 1 {
		for _, k := range s.Key {
			if k.Bar == bar {
				prev := s.KeyAt(bar - 1)
				key = key || prev.Fifths != k.Fifths || prev.Mode != k.Mode
			}
		}
		for _, m := range s.Meter {
			if m.Bar == bar && !s.MeterAt(bar-1).Eq(m.Meter) {
				time = true
			}
		}
		// The bar after a pickup states the meter again only if it changed.
	}
	type sc struct {
		staff int
		clef  string
	}
	var clefs []sc
	for si, st := range p.l.Staves {
		if c, ok := clefAtStart(st, bar); ok {
			if st.Tab != nil && st.Tab.Only {
				if bar > 1 {
					continue
				}
				c = "tab"
			}
			clefs = append(clefs, sc{si + 1, c})
		}
	}
	if !key && !time && len(clefs) == 0 {
		return
	}
	w.open("attributes")
	if bar == 1 {
		w.leaf("divisions", fmt.Sprint(x.div))
	}
	if key {
		k := s.KeyAt(bar)
		w.open("key")
		w.leaf("fifths", itoa(p.writtenFifths(bar)))
		mode := k.Mode
		if mode == "" {
			mode = "major"
		}
		w.leaf("mode", mode)
		w.close()
	}
	if time {
		m := s.MeterAt(bar)
		var sym string
		switch m.Symbol {
		case "common":
			sym = "common"
		case "cut":
			sym = "cut"
		}
		w.open("time", attr{"symbol", sym})
		groups := make([]string, len(m.Groups))
		for i, g := range m.Groups {
			groups[i] = itoa(g)
		}
		w.leaf("beats", strings.Join(groups, "+"))
		w.leaf("beat-type", itoa(m.Den))
		w.close()
	}
	if bar == 1 && len(p.l.Staves) > 1 {
		w.leaf("staves", itoa(len(p.l.Staves)))
	}
	for _, c := range clefs {
		x.clefXML(c.clef, c.staff)
	}
	if bar == 1 {
		for si, st := range p.l.Staves {
			x.staffDetailsXML(st, si+1)
		}
	}
	if bar == 1 {
		same := true
		for _, st := range p.l.Staves {
			same = same && st.Transpose == p.l.Staves[0].Transpose
		}
		for si, st := range p.l.Staves {
			if st.Transpose == (score.Interval{}) || (same && si > 0) {
				continue
			}
			as := []attr{}
			if !same {
				as = append(as, attr{"number", itoa(si + 1)})
			}
			x.transposeXML(st.Transpose, as)
		}
	}
	w.close()
}

// transposeXML writes a written-to-sounding interval: steps and semitones
// within an octave, and the octaves.
func (x *exporter) transposeXML(iv score.Interval, as []attr) {
	w := &x.w
	oct := 0
	d, c := iv.Diatonic, iv.Chromatic
	for d >= 7 || (d > 0 && c >= 12) {
		d, c, oct = d-7, c-12, oct+1
	}
	for d <= -7 || (d < 0 && c <= -12) {
		d, c, oct = d+7, c+12, oct-1
	}
	w.open("transpose", as...)
	w.leaf("diatonic", itoa(d))
	w.leaf("chromatic", itoa(c))
	if oct != 0 {
		w.leaf("octave-change", itoa(oct))
	}
	w.close()
}

// leftBarline is a repeat's start and an ending's.
func (p *partWriter) leftBarline(bar int) {
	s, w := p.x.s, &p.x.w
	var start bool
	for _, r := range s.Repeats {
		start = start || r.Start == bar
	}
	var volta *score.Volta
	for i := range s.Voltas {
		if s.Voltas[i].Start == bar {
			volta = &s.Voltas[i]
		}
	}
	if !start && volta == nil {
		return
	}
	w.open("barline", attr{"location", "left"})
	if start {
		w.leaf("bar-style", "heavy-light")
	}
	if volta != nil {
		w.leaf("ending", volta.Label(), attr{"number", passesText(volta.Passes)}, attr{"type", "start"})
	}
	if start {
		w.empty("repeat", attr{"direction", "forward"})
	}
	w.close()
}

func passesText(ps []int) string {
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = itoa(p)
	}
	return strings.Join(parts, ", ")
}

// rightBarline is a repeat's end, an ending's, and the final bar line.
func (p *partWriter) rightBarline(bar int) {
	x, s, w := p.x, p.x.s, &p.x.w
	var rep *score.Repeat
	for i := range s.Repeats {
		if s.Repeats[i].End == bar {
			rep = &s.Repeats[i]
		}
	}
	var volta *score.Volta
	for i := range s.Voltas {
		if s.Voltas[i].End == bar {
			volta = &s.Voltas[i]
		}
	}
	final := bar == x.bars
	if rep == nil && volta == nil && !final {
		return
	}
	w.open("barline", attr{"location", "right"})
	switch {
	case rep != nil || final:
		w.leaf("bar-style", "light-heavy")
	}
	if volta != nil {
		kind := "discontinue"
		if rep != nil {
			kind = "stop"
		}
		w.empty("ending", attr{"number", passesText(volta.Passes)}, attr{"type", kind})
	}
	if rep != nil {
		times := ""
		if rep.Plays() != 2 {
			times = itoa(rep.Plays())
		}
		w.empty("repeat", attr{"direction", "backward"}, attr{"times", times})
	}
	w.close()
}

// partItems are the score-wide marks of a bar, written with the first
// part: rehearsal marks, segno and coda at the start, tempo marks where
// they are (and the end of a gradual change's dashes).
func (p *partWriter) partItems(bar int, barLen score.Frac) []item {
	x, s, w := p.x, p.x.s, &p.x.w
	var out []item
	for _, sec := range s.Sections {
		if sec.Bar == bar {
			name := sec.Name
			out = append(out, item{write: func() {
				w.open("direction", attr{"placement", "above"})
				w.open("direction-type")
				w.leaf("rehearsal", name, attr{"enclosure", "square"})
				w.close()
				w.leaf("staff", "1")
				w.close()
			}})
		}
	}
	for _, j := range s.Jumps {
		if j.Bar != bar || (j.Kind != score.JumpSegno && j.Kind != score.JumpCoda) {
			continue
		}
		kind := j.Kind
		out = append(out, item{write: func() {
			w.open("direction", attr{"placement", "above"})
			w.open("direction-type")
			if kind == score.JumpSegno {
				w.empty("segno")
			} else {
				w.empty("coda")
			}
			w.close()
			w.leaf("staff", "1")
			if kind == score.JumpSegno {
				w.empty("sound", attr{"segno", "segno"})
			} else {
				w.empty("sound", attr{"coda", "coda"})
			}
			w.close()
		}})
	}
	for _, t := range s.Tempo {
		t := t
		if t.At.Bar == bar && t.At.Offset.Less(barLen) {
			out = append(out, item{at: t.At.Offset, write: func() { x.tempoXML(t) }})
		}
		if t.HasTo && t.To.Bar == bar {
			out = append(out, item{at: t.To.Offset, write: func() {
				w.open("direction")
				w.open("direction-type")
				w.empty("dashes", attr{"type", "stop"}, attr{"number", tempoDashes})
				w.close()
				w.leaf("staff", "1")
				w.close()
			}})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].at.Less(out[j].at) })
	return out
}

// endItems are the jumps at the end of a bar: To Coda, Fine, D.C., D.S.
func (p *partWriter) endItems(bar int) {
	s, w := p.x.s, &p.x.w
	if p.li != 0 {
		return
	}
	for _, j := range s.Jumps {
		if j.Bar != bar {
			continue
		}
		var snd attr
		switch j.Kind {
		case score.JumpToCoda:
			snd = attr{"tocoda", "coda"}
		case score.JumpFine:
			snd = attr{"fine", "yes"}
		case score.JumpDC:
			snd = attr{"dacapo", "yes"}
		case score.JumpDS:
			snd = attr{"dalsegno", "segno"}
		default:
			continue
		}
		text := j.Text()
		if j.Repeats {
			text += " con rip."
		}
		w.open("direction", attr{"placement", "above"})
		w.open("direction-type")
		w.leaf("words", text, attr{"font-style", "italic"}, attr{"justify", "right"})
		w.close()
		w.leaf("staff", "1")
		w.empty("sound", snd)
		w.close()
	}
}

// tempoDashes numbers a tempo change's dashes apart from any crescendo's
// (those are numbered by voice).
const tempoDashes = "16"

// tempoXML writes a tempo mark: its text, its metronome mark, its dashes,
// and the tempo it sets for playback.
func (x *exporter) tempoXML(t score.TempoMark) {
	w := &x.w
	w.open("direction", attr{"placement", "above"})
	if t.Text != "" {
		w.open("direction-type")
		w.leaf("words", t.Text, attr{"font-weight", "bold"})
		w.close()
	}
	if t.BPM > 0 {
		u := t.Unit
		if u == (score.Duration{}) {
			u = score.Duration{Base: score.Quarter}
		}
		w.open("direction-type")
		w.open("metronome", attr{"parentheses", "no"})
		w.leaf("beat-unit", durTypeName(u.Base))
		for i := 0; i < int(u.Dots); i++ {
			w.empty("beat-unit-dot")
		}
		w.leaf("per-minute", num(t.BPM))
		w.close()
		w.close()
	}
	if t.HasTo {
		w.open("direction-type")
		w.empty("dashes", attr{"type", "start"}, attr{"number", tempoDashes})
		w.close()
	}
	w.leaf("staff", "1")
	if t.BPM > 0 {
		w.empty("sound", attr{"tempo", num(t.QuarterBPM())})
	}
	w.close()
}

var durTypeNames = map[score.DurBase]string{
	score.Breve: "breve", score.WholeNote: "whole", score.Half: "half", score.Quarter: "quarter",
	score.Eighth: "eighth", score.D16: "16th", score.D32: "32nd", score.D64: "64th",
	score.D128: "128th", score.D256: "256th",
}

func durTypeName(b score.DurBase) string { return durTypeNames[b] }

// ---------- a voice's bar ----------

// voice writes one voice's events in a bar; it returns how far it got.
// The first voice of the part also carries the part-wide items and the
// staff's clef changes are placed in it (or failing an event at their
// place in it, the first voice with one).
func (p *partWriter) voice(si, vi, bar int, m score.Measure, barLen score.Frac, items []item, clefs []score.ClefChange, first bool) score.Frac {
	x, w := p.x, &p.x.w
	offs, _ := m.Offsets(barLen)
	beams := beamsFor(m, offs, p.x.s.MeterAt(bar), barLen, bar == 1 && p.x.s.Pickup.Sign() > 0)
	tuplets := tupletEnds(m)
	vs := p.voices[[2]int{si, vi}]
	itemAt := 0
	flush := func(upto score.Frac, cursor score.Frac, inclusive bool) {
		for first && itemAt < len(items) && (items[itemAt].at.Less(upto) || inclusive && items[itemAt].at.Eq(upto)) {
			it := items[itemAt]
			itemAt++
			p.withOffset(it.at.Sub(cursor), it.write)
		}
	}
	at := score.Frac{}
	for ei, e := range m.Events {
		o := offs[ei]
		flush(o, o, true)
		for len(clefs) > 0 && !o.Less(clefs[0].At.Offset) {
			if !o.Eq(clefs[0].At.Offset) && first {
				x.warn("%s: a clef change at %s falls inside a note; it is written at the next one", p.l.Name, x.s.FormatPos(clefs[0].At))
			}
			w.open("attributes")
			x.clefXML(clefs[0].Clef, si+1)
			w.close()
			clefs = clefs[1:]
		}
		r := vref{bar, ei}
		length := m.Actual(e, barLen)
		p.writeEvent(si, vi, r, m, e, length, beams[ei], tuplets[ei], vs)
		at = o.Add(length)
	}
	if first {
		flush(barLen, at, false)
		// Anything at the bar's end goes on the end of the bar.
		for itemAt < len(items) {
			it := items[itemAt]
			itemAt++
			p.withOffset(it.at.Sub(at), it.write)
		}
	}
	for _, c := range clefs {
		// No event at or after the change in this voice: at the end.
		w.open("attributes")
		x.clefXML(c.Clef, si+1)
		w.close()
	}
	return at
}

// withOffset writes a direction a distance (in whole notes) from the
// cursor: MusicXML's <offset> moves it there without moving the cursor.
func (p *partWriter) withOffset(d score.Frac, write func()) {
	if d.IsZero() {
		write()
		return
	}
	// The direction is written by write; its <offset> goes after its
	// direction-types, so it is spliced in before the staff element.
	w := &p.x.w
	start := w.b.Len()
	write()
	out := w.b.Bytes()[start:]
	idx := bytes.Index(out, []byte("<staff>"))
	if idx < 0 {
		return
	}
	line := bytes.LastIndexByte(out[:idx], '\n') + 1
	ind := string(out[line:idx])
	ins := []byte(ind + "<offset>" + fmt.Sprint(p.x.dur(d)) + "</offset>\n")
	tail := append([]byte(nil), out[line:]...)
	w.b.Truncate(start + line)
	w.b.Write(ins)
	w.b.Write(tail)
}

// tupletMark is where an event opens and closes tuplet brackets: the
// levels (1-based depth) opening, outermost first, and those closing,
// innermost first, each with its tuplet.
type tupletMark struct {
	starts, stops []int // tuplet indexes (1-based into Tuplets)
	depth         map[int]int
}

// tupletEnds finds each event's tuplet starts and stops: a group starts
// on its first event that is not a grace note and stops on its last.
func tupletEnds(m score.Measure) []tupletMark {
	out := make([]tupletMark, len(m.Events))
	first, last := map[int]int{}, map[int]int{}
	depth := map[int]int{}
	for ei, e := range m.Events {
		if e.Grace != score.NotGrace {
			continue
		}
		ch := chainOf(m, e.Tuplet)
		for d, t := range ch {
			depth[t] = d + 1
			if _, ok := first[t]; !ok {
				first[t] = ei
			}
			last[t] = ei
		}
	}
	for ei, e := range m.Events {
		if e.Grace != score.NotGrace {
			continue
		}
		ch := chainOf(m, e.Tuplet)
		out[ei].depth = depth
		for _, t := range ch {
			if first[t] == ei {
				out[ei].starts = append(out[ei].starts, t)
			}
		}
		for i := len(ch) - 1; i >= 0; i-- {
			if last[ch[i]] == ei {
				out[ei].stops = append(out[ei].stops, ch[i])
			}
		}
	}
	return out
}

// chainOf is the tuplets an event is in, outermost first.
func chainOf(m score.Measure, t int) []int {
	var out []int
	for t > 0 && t <= len(m.Tuplets) && len(out) <= len(m.Tuplets) {
		out = append([]int{t}, out...)
		t = m.Tuplets[t-1].Parent
	}
	return out
}

// beamTypes are one event's beam elements, level 1 first.
type beamTypes []string

// beamsFor groups a voice's bar into beams as the engraver does (the
// meter's beam units; a tuplet's notes on their own; rests, quarters and
// longer, and "nobeam" break a group; 4/4's two plain eighth pairs in a
// half join) and gives each note its beam types at every level.
func beamsFor(m score.Measure, offs []score.Frac, meter score.Meter, barLen score.Frac, pickup bool) []beamTypes {
	out := make([]beamTypes, len(m.Events))
	units := meter.BeamUnits()
	shift := score.Frac{}
	if pickup {
		if sh := meter.Length().Sub(barLen); sh.Sign() > 0 {
			shift = sh
		}
	}
	unitOf := func(t score.Frac) int {
		t = t.Add(shift)
		at := score.Frac{}
		for i, u := range units {
			at = at.Add(u)
			if t.Less(at) {
				return i
			}
		}
		return len(units)
	}
	beamable := func(e score.Event) bool {
		return e.Kind == score.EventChord && len(e.Notes) > 0 && e.Dur.Base >= score.Eighth && !e.Has("nobeam")
	}
	type key struct{ unit, tuplet int }
	var groups [][]int
	var cur []int
	var ck key
	flush := func() {
		if len(cur) > 1 {
			groups = append(groups, cur)
		}
		cur = nil
	}
	var graces []int
	flushGraces := func() {
		if len(graces) > 1 {
			groups = append(groups, graces)
		}
		graces = nil
	}
	for ei, e := range m.Events {
		if e.Grace != score.NotGrace {
			if beamable(e) {
				graces = append(graces, ei)
			} else {
				flushGraces()
			}
			continue
		}
		flushGraces()
		if !beamable(e) {
			flush()
			continue
		}
		k := key{unit: unitOf(offs[ei])}
		if e.Tuplet > 0 {
			k = key{unit: -1, tuplet: e.Tuplet}
		}
		if len(cur) > 0 && k != ck {
			flush()
		}
		cur, ck = append(cur, ei), k
	}
	flush()
	flushGraces()
	if meter.Den == 4 && len(meter.Groups) == 1 && meter.Groups[0] == 4 && shift.IsZero() {
		plain := func(g []int) bool {
			if len(g) != 2 {
				return false
			}
			for _, ei := range g {
				e := m.Events[ei]
				if e.Dur.Base != score.Eighth || e.Dur.Dots != 0 || e.Tuplet != 0 || e.Grace != score.NotGrace {
					return false
				}
			}
			return offs[g[1]].Sub(offs[g[0]]).Eq(score.F(1, 8))
		}
		var merged [][]int
		for i := 0; i < len(groups); i++ {
			g := groups[i]
			if i+1 < len(groups) && plain(g) && plain(groups[i+1]) {
				u, v := unitOf(offs[g[0]]), unitOf(offs[groups[i+1][0]])
				if (u == 0 || u == 2) && v == u+1 && offs[g[0]].Eq(score.F(int64(u), 4)) {
					merged = append(merged, append(append([]int(nil), g...), groups[i+1]...))
					i++
					continue
				}
			}
			merged = append(merged, g)
		}
		groups = merged
	}
	for _, g := range groups {
		levels := 0
		for _, ei := range g {
			levels = max(levels, int(m.Events[ei].Dur.Base-score.Quarter))
		}
		for _, ei := range g {
			out[ei] = make(beamTypes, int(m.Events[ei].Dur.Base-score.Quarter))
		}
		for lv := 1; lv <= levels; lv++ {
			for i := 0; i < len(g); {
				if int(m.Events[g[i]].Dur.Base-score.Quarter) < lv {
					i++
					continue
				}
				j := i
				for j+1 < len(g) && int(m.Events[g[j+1]].Dur.Base-score.Quarter) >= lv {
					j++
				}
				switch {
				case lv == 1 || j > i:
					out[g[i]][lv-1] = "begin"
					for k := i + 1; k < j; k++ {
						out[g[k]][lv-1] = "continue"
					}
					out[g[j]][lv-1] = "end"
				case i == 0:
					out[g[i]][lv-1] = "forward hook"
				default:
					out[g[i]][lv-1] = "backward hook"
				}
				i = j + 1
			}
		}
	}
	return out
}

// ---------- one event ----------

var accidentalNames = map[int]string{-2: "flat-flat", -1: "flat", 0: "natural", 1: "sharp", 2: "double-sharp"}

var articulationXML = map[string]string{
	"stacc": "staccato", "staccmo": "staccatissimo", "acc": "accent", "ten": "tenuto",
	"marc": "strong-accent", "portato": "detached-legato",
}

var ornamentXML = map[string]string{
	"tr": "trill-mark", "mord": "mordent", "imord": "inverted-mordent", "turn": "turn", "iturn": "inverted-turn",
}

var fermataShapes = map[string]string{"": "normal", "short": "angled", "long": "square", "verylong": "double-square"}

var ottavaSizes = map[string][2]int{"8va": {8, -1}, "8vb": {8, 1}, "15ma": {15, -1}, "15mb": {15, 1}}

// direction writes a direction on a staff and voice.
func (p *partWriter) direction(si, vi int, placement string, body func(), sound ...attr) {
	w := &p.x.w
	w.open("direction", attr{"placement", placement})
	body()
	w.leaf("voice", itoa(si*4+vi+1))
	w.leaf("staff", itoa(si+1))
	if len(sound) > 0 {
		w.empty("sound", sound...)
	}
	w.close()
}

// textDirection writes a staff text, a technique or an expression. Their
// words look alike to another program; the id (unique, and ignored by
// everything but this package's importer) says which it is.
func (p *partWriter) textDirection(si, vi int, kind, placement string, words func()) {
	w := &p.x.w
	p.x.ids++
	w.open("direction", attr{"placement", placement}, attr{"id", fmt.Sprintf("f%d-%s", p.x.ids, kind)})
	p.dirType(words)
	w.leaf("voice", itoa(si*4+vi+1))
	w.leaf("staff", itoa(si+1))
	w.close()
}

func (p *partWriter) dirType(body func()) {
	w := &p.x.w
	w.open("direction-type")
	body()
	w.close()
}

// before writes the directions an event's marks start: dynamics, text,
// hairpins, ottava and pedal lines.
func (p *partWriter) before(si, vi int, e *score.Event, vs *voiceState) {
	w := &p.x.w
	ss := p.staves[si]
	for _, mk := range e.Marks {
		mk := mk
		switch {
		case mk.Kind() == score.MarkDynamic:
			name := mk.Name
			if name == "niente" {
				name = "n"
			}
			lvl := score.Dynamics[mk.Name].Level
			p.direction(si, vi, "below", func() {
				p.dirType(func() {
					w.open("dynamics")
					w.empty(name)
					w.close()
				})
			}, attr{"dynamics", num(math.Round(float64(lvl)*127/90*10000) / 100)})
		case mk.Name == "cresc" || mk.Name == "dim":
			p.closeHairpin(si, vi, vs)
			if mk.Value == "" {
				kind := map[string]string{"cresc": "crescendo", "dim": "diminuendo"}[mk.Name]
				p.direction(si, vi, "below", func() {
					p.dirType(func() { w.empty("wedge", attr{"type", kind}, attr{"number", itoa(vi + 1)}) })
				})
				vs.hairpin = "wedge"
			} else {
				text := mk.Value
				if text == "text" {
					text = map[string]string{"cresc": "cresc.", "dim": "dim."}[mk.Name]
				}
				p.direction(si, vi, "below", func() {
					p.dirType(func() { w.leaf("words", text, attr{"font-style", "italic"}) })
					p.dirType(func() { w.empty("dashes", attr{"type", "start"}, attr{"number", itoa(vi + 1)}) })
				})
				vs.hairpin = "dashes"
			}
		case mk.Name == "text" || mk.Name == "tech":
			if mk.Value != "" {
				p.textDirection(si, vi, mk.Name, "above", func() { w.leaf("words", mk.Value) })
			}
		case mk.Name == "expr":
			if mk.Value != "" {
				p.textDirection(si, vi, mk.Name, "below", func() { w.leaf("words", mk.Value, attr{"font-style", "italic"}) })
			}
		case ottavaSizes[mk.Name][0] != 0:
			if ss.ottava != 0 {
				p.closeOttava(si, vi)
			}
			sz := ottavaSizes[mk.Name]
			kind := "down"
			if sz[1] > 0 {
				kind = "up"
			}
			p.direction(si, vi, map[bool]string{true: "above", false: "below"}[sz[1] < 0], func() {
				p.dirType(func() { w.empty("octave-shift", attr{"type", kind}, attr{"size", itoa(sz[0])}, attr{"number", "1"}) })
			})
			ss.ottava = sz[0]
		case mk.Name == "ped":
			kind := "start"
			if ss.pedal {
				kind = "change"
			}
			p.direction(si, vi, "below", func() {
				p.dirType(func() { w.empty("pedal", attr{"type", kind}, attr{"line", "yes"}) })
			})
			ss.pedal = true
		}
	}
}

func (p *partWriter) closeHairpin(si, vi int, vs *voiceState) {
	w := &p.x.w
	switch vs.hairpin {
	case "wedge":
		p.direction(si, vi, "below", func() {
			p.dirType(func() { w.empty("wedge", attr{"type", "stop"}, attr{"number", itoa(vi + 1)}) })
		})
	case "dashes":
		p.direction(si, vi, "below", func() {
			p.dirType(func() { w.empty("dashes", attr{"type", "stop"}, attr{"number", itoa(vi + 1)}) })
		})
	}
	vs.hairpin = ""
}

func (p *partWriter) closeOttava(si, vi int) {
	w := &p.x.w
	ss := p.staves[si]
	size := ss.ottava
	p.direction(si, vi, "above", func() {
		p.dirType(func() { w.empty("octave-shift", attr{"type", "stop"}, attr{"size", itoa(size)}, attr{"number", "1"}) })
	})
	ss.ottava = 0
}

func (p *partWriter) closePedal(si, vi int) {
	w := &p.x.w
	p.direction(si, vi, "below", func() {
		p.dirType(func() { w.empty("pedal", attr{"type", "stop"}, attr{"line", "yes"}) })
	})
	p.staves[si].pedal = false
}

// after writes the directions an event's marks end — a hairpin's stop
// after its last note, an ottava's, a pedal's — and closes what is still
// open when the voice ends.
func (p *partWriter) after(si, vi int, r vref, e *score.Event, vs *voiceState) {
	ss := p.staves[si]
	end := r == vs.last
	// A hairpin or an ottava the next event starts another of ends here.
	var next *score.Event
	if e.Grace == score.NotGrace {
		_, next, _ = p.neighbour(si, vi, r, 1)
	}
	starts := func(names ...string) bool {
		if next == nil {
			return false
		}
		for _, n := range names {
			if next.Has(n) {
				return true
			}
		}
		return false
	}
	if e.Has("hairpin_end") || end && vs.hairpin != "" || vs.hairpin != "" && starts("cresc", "dim") {
		p.closeHairpin(si, vi, vs)
	}
	if ss.ottava != 0 && (e.Has("ottava_end") || end && p.lastOfStaff(si, r) || starts("8va", "8vb", "15ma", "15mb")) {
		p.closeOttava(si, vi)
	}
	if ss.pedal && (e.Has("ped_end") || end && p.lastOfStaff(si, r)) {
		p.closePedal(si, vi)
	}
}

// lastOfStaff reports whether no voice of a staff has an event after a
// place (the staff's lines close there).
func (p *partWriter) lastOfStaff(si int, r vref) bool {
	for vi := range p.l.Staves[si].Voices {
		vs := p.voices[[2]int{si, vi}]
		if len(vs.seq) > 0 && (vs.last.bar > r.bar) {
			return false
		}
	}
	return true
}

// writeEvent writes an event: its directions, its notes (or rest, or
// space), and what it ends.
func (p *partWriter) writeEvent(si, vi int, r vref, m score.Measure, e score.Event, length score.Frac, beams beamTypes, tm tupletMark, vs *voiceState) {
	x, s, w := p.x, p.x.s, &p.x.w
	st := p.l.Staves[si]
	voice := itoa(si*4 + vi + 1)
	if e.Kind == score.EventSpace {
		w.open("forward")
		w.leaf("duration", fmt.Sprint(x.dur(length)))
		w.leaf("voice", voice)
		w.leaf("staff", itoa(si+1))
		w.close()
		return
	}
	p.before(si, vi, &e, vs)

	// What the event is tied from, glides from, and the tremolo it ends.
	tiedIn := map[int]bool{}
	glissIn, tremIn := "", ""
	if e.Grace == score.NotGrace {
		if _, prev, ok := p.neighbour(si, vi, r, -1); ok {
			for _, n := range prev.Notes {
				if n.Tie {
					tiedIn[n.Pitch.MIDI()] = true
				}
			}
			for _, mk := range prev.Marks {
				switch mk.Name {
				case "gliss":
					if e.Kind == score.EventChord {
						glissIn = "solid"
						if mk.Value == "wavy" {
							glissIn = "wavy"
						}
					}
				case "trem2":
					tremIn = "stop"
				}
			}
		}
	}
	offs, _ := m.Offsets(s.BarLength(r.bar))
	at := offs[r.idx]
	ratio := score.Whole(1)
	actual, normal := 1, 1
	for _, t := range chainOf(m, e.Tuplet) {
		tp := m.Tuplets[t-1]
		actual *= tp.Num
		normal *= tp.Den
		ratio = ratio.Mul(tp.Ratio())
	}
	notes := e.Notes
	rest := e.Kind != score.EventChord || len(notes) == 0
	if rest {
		notes = []score.Note{{}}
	}
	vel := e.VelocityMark()
	tabs := p.tabPlaces(si, vi, r)
	for ni, n := range notes {
		as := []attr{}
		if vel > 0 && !rest {
			as = append(as, attr{"dynamics", num(math.Round(float64(vel)/90*1000000) / 10000)})
		}
		w.open("note", as...)
		if e.Grace != score.NotGrace {
			ga := []attr{}
			if e.Grace == score.Acciaccatura {
				ga = append(ga, attr{"slash", "yes"})
			}
			if e.Has("before_beat") {
				ga = append(ga, attr{"steal-time-previous", "50"})
			}
			if e.Has("on_beat") {
				ga = append(ga, attr{"steal-time-following", "50"})
			}
			w.empty("grace", ga...)
		}
		if ni > 0 {
			w.empty("chord")
		}
		written := st.Written(n.Pitch)
		switch {
		case rest && e.Kind == score.EventBarRest:
			w.empty("rest", attr{"measure", "yes"})
		case rest:
			w.empty("rest")
		case percussive(st):
			pos, _, _ := p.kit.Display(n.Pitch)
			dp := score.FromDiatonic(score.PercussionBottom + pos)
			w.open("unpitched")
			w.leaf("display-step", string(score.Steps[dp.Step]))
			w.leaf("display-octave", itoa(int(dp.Octave)))
			w.close()
		default:
			w.open("pitch")
			w.leaf("step", string(score.Steps[written.Step]))
			if written.Alter != 0 {
				w.leaf("alter", itoa(int(written.Alter)))
			}
			w.leaf("octave", itoa(int(written.Octave)))
			w.close()
		}
		if e.Grace == score.NotGrace {
			w.leaf("duration", fmt.Sprint(x.dur(length)))
		}
		if !rest {
			if tiedIn[n.Pitch.MIDI()] {
				w.empty("tie", attr{"type", "stop"})
			}
			if n.Tie {
				w.empty("tie", attr{"type", "start"})
			}
		}
		if !rest && percussive(st) {
			w.empty("instrument", attr{"id", instID(p.li, n.Pitch.MIDI())})
		}
		w.leaf("voice", voice)
		if e.Kind != score.EventBarRest {
			w.leaf("type", durTypeName(e.Dur.Base))
			for i := 0; i < int(e.Dur.Dots); i++ {
				w.empty("dot")
			}
		}
		if !rest && !percussive(st) {
			if p.showsAccidental(si, r, at, n, tiedIn[n.Pitch.MIDI()] && e.Grace == score.NotGrace) {
				as := []attr{}
				if n.Acc == score.AccCautionary {
					as = append(as, attr{"parentheses", "yes"})
				}
				w.leaf("accidental", accidentalNames[int(written.Alter)], as...)
			}
		}
		if actual != normal && e.Grace == score.NotGrace {
			w.open("time-modification")
			w.leaf("actual-notes", itoa(actual))
			w.leaf("normal-notes", itoa(normal))
			w.close()
		}
		if !rest {
			up, drawn := p.x.stems[stemKey{p.li, si, vi, r.bar, r.idx}]
			switch {
			case e.Has("up"):
				w.leaf("stem", "up")
			case e.Has("down"):
				w.leaf("stem", "down")
			case drawn && up:
				w.leaf("stem", "up")
			case drawn:
				w.leaf("stem", "down")
			}
			if percussive(st) {
				_, head, _ := p.kit.Display(n.Pitch)
				switch head {
				case score.DrumHeadX:
					w.leaf("notehead", "x")
				case score.DrumHeadCircleX:
					w.leaf("notehead", "circle-x")
				case score.DrumHeadDiamond:
					w.leaf("notehead", "diamond")
				case score.DrumHeadTriangle:
					w.leaf("notehead", "triangle")
				}
			}
		}
		// A cross-staff note is on the staff it is written on (its voice
		// still its own staff's).
		staffNo := si + 1
		if d := e.CrossStaff(); !rest && d != 0 && si+d >= 0 && si+d < len(p.l.Staves) && !p.l.Staves[si+d].Percussion() {
			staffNo += d
		}
		w.leaf("staff", itoa(staffNo))
		if ni == 0 {
			for lv, b := range beams {
				if b != "" {
					w.leaf("beam", b, attr{"number", itoa(lv + 1)})
				}
			}
		}
		var place *score.TabNote
		if !rest && ni < len(tabs) && tabs[ni].OK {
			place = &tabs[ni]
		}
		p.notations(si, vi, e, n, ni, rest, tiedIn, glissIn, tremIn, tm, m, vs, place)
		if ni == 0 && !rest {
			p.lyricsXML(si, vi, r, e)
		}
		w.close()
	}
	p.after(si, vi, r, &e, vs)
}

// showsAccidental reports whether a note shows its accidental: forced or
// cautionary, or the bar and key so far say otherwise; a note tied into
// shows none.
func (p *partWriter) showsAccidental(si int, r vref, at score.Frac, n score.Note, tiedIn bool) bool {
	if tiedIn {
		return false
	}
	if n.Acc == score.AccForce || n.Acc == score.AccCautionary {
		return true
	}
	return p.x.s.AlterInForce(p.l.Staves[si], r.bar, at, n.Pitch.Diatonic()) != int(n.Pitch.Alter)
}

// accMark is an ornament accidental as an accidental-mark's name.
func accMark(a int) string { return accidentalNames[a] }

// notations writes a note's <notations>: ties, tuplets, slurs, glissandi,
// ornaments, articulations, fermata, arpeggio.
func (p *partWriter) notations(si, vi int, e score.Event, n score.Note, ni int, rest bool, tiedIn map[int]bool, glissIn, tremIn string, tm tupletMark, m score.Measure, vs *voiceState, place *score.TabNote) {
	w := &p.x.w
	var body []func()
	add := func(f func()) { body = append(body, f) }
	if !rest {
		if tiedIn[n.Pitch.MIDI()] {
			add(func() { w.empty("tied", attr{"type", "stop"}) })
		}
		if n.Tie {
			add(func() { w.empty("tied", attr{"type", "start"}) })
		}
	}
	if ni == 0 {
		for _, t := range tm.starts {
			tp := m.Tuplets[t-1]
			depth := tm.depth[t]
			add(func() {
				w.open("tuplet", attr{"type", "start"}, attr{"number", itoa(depth)}, attr{"bracket", "yes"})
				w.open("tuplet-actual")
				w.leaf("tuplet-number", itoa(tp.Num))
				w.close()
				w.open("tuplet-normal")
				w.leaf("tuplet-number", itoa(tp.Den))
				w.close()
				w.close()
			})
		}
		for _, t := range tm.stops {
			depth := tm.depth[t]
			add(func() { w.empty("tuplet", attr{"type", "stop"}, attr{"number", itoa(depth)}) })
		}
		for _, mk := range e.Marks {
			switch mk.Name {
			case "slur_end":
				if vs.slurs > 0 {
					num := vs.slurs
					vs.slurs--
					add(func() { w.empty("slur", attr{"type", "stop"}, attr{"number", itoa(num)}) })
				}
			}
		}
		for _, mk := range e.Marks {
			if mk.Name == "slur" && vs.slurs < 6 {
				vs.slurs++
				num := vs.slurs
				add(func() { w.empty("slur", attr{"type", "start"}, attr{"number", itoa(num)}) })
			}
		}
		if glissIn != "" && !rest {
			lt := glissIn
			add(func() { w.empty("glissando", attr{"type", "stop"}, attr{"line-type", lt}, attr{"number", "1"}) })
		}
		for _, mk := range e.Marks {
			if mk.Name == "gliss" && !rest {
				lt := "solid"
				if mk.Value == "wavy" {
					lt = "wavy"
				}
				add(func() { w.empty("glissando", attr{"type", "start"}, attr{"line-type", lt}, attr{"number", "1"}) })
			}
		}
		// Ornaments.
		var orn []func()
		for _, mk := range e.Marks {
			mk := mk
			if el, ok := ornamentXML[mk.Name]; ok && !rest {
				orn = append(orn, func() {
					w.empty(el, attr{"placement", "above"})
					up, low, hasUp, hasLow, ok := score.OrnamentAccidentals(mk.Value)
					if !ok {
						return
					}
					if mk.Name == "mord" && hasUp && !hasLow {
						// A lower mordent's one auxiliary is below.
						up, hasUp, low, hasLow = 0, false, up, true
					}
					if hasUp {
						w.leaf("accidental-mark", accMark(up), attr{"placement", "above"})
					}
					if hasLow {
						w.leaf("accidental-mark", accMark(low), attr{"placement", "below"})
					}
				})
			}
			switch mk.Name {
			case "trem":
				if n, ok := score.TremoloStrokes(mk.Value); ok && !rest {
					orn = append(orn, func() { w.leaf("tremolo", itoa(n), attr{"type", "single"}) })
				}
			case "trem2":
				if n, ok := score.TremoloStrokes(mk.Value); ok && !rest {
					orn = append(orn, func() { w.leaf("tremolo", itoa(n), attr{"type", "start"}) })
				}
			}
		}
		if tremIn != "" && !rest {
			orn = append(orn, func() { w.leaf("tremolo", "3", attr{"type", "stop"}) })
		}
		if len(orn) > 0 {
			add(func() {
				w.open("ornaments")
				for _, f := range orn {
					f()
				}
				w.close()
			})
		}
		var arts []string
		legato := false
		for _, mk := range e.Marks {
			if a, ok := articulationXML[mk.Name]; ok {
				arts = append(arts, a)
			}
			legato = legato || mk.Name == "legato"
		}
		if len(arts) > 0 || legato {
			add(func() {
				w.open("articulations")
				for _, a := range arts {
					w.empty(a)
				}
				if legato {
					w.leaf("other-articulation", "legato")
				}
				w.close()
			})
		}
		var tech []func()
		for _, mk := range e.Marks {
			switch mk.Name {
			case "open":
				tech = append(tech, func() { w.empty("open") })
			case "stopped":
				tech = append(tech, func() { w.empty("stopped") })
			}
		}
		if len(tech) > 0 && !rest {
			add(func() {
				w.open("technical")
				for _, f := range tech {
					f()
				}
				w.close()
			})
		}
		for _, mk := range e.Marks {
			if mk.Name == "ferm" {
				shape := fermataShapes[mk.Value]
				if shape == "" {
					shape = "normal"
				}
				add(func() { w.leaf("fermata", shape, attr{"type", "upright"}) })
			}
		}
	}
	if place != nil {
		// Every note's own place on the strings.
		st, fr := place.String, place.Fret
		add(func() {
			w.open("technical")
			w.leaf("string", itoa(st))
			w.leaf("fret", itoa(fr))
			w.close()
		})
	}
	if !rest {
		for _, mk := range e.Marks {
			if mk.Name == "arp" {
				dir := ""
				switch mk.Value {
				case "down", "up":
					dir = mk.Value
				}
				add(func() { w.empty("arpeggiate", attr{"direction", dir}) })
			}
		}
	}
	if len(body) == 0 {
		return
	}
	w.open("notations")
	for _, f := range body {
		f()
	}
	w.close()
}
