package score

// fscore.go — the .fscore file.
//
// YAML, with each voice's music stored as text in the grammar of
// grammar.go, one bar to a line:
//
//	version: 1
//	title: Hangar Theme
//	bars: 16
//	tempo: [{bar: 1, bpm: 92, text: Andante}]
//	meter: [{bar: 1, sig: 4/4}, {bar: 9, sig: 3+2+2/8}]
//	key: [{bar: 1, fifths: -2, mode: major}]
//	sections: [{name: intro, bar: 1}, {name: theme, bar: 9, loop: true}]
//	lines:
//	  - name: Violin I
//	    instrument: strings/violin_section
//	    volume: 0.8
//	    pan: -0.3
//	    sends: {a: 0.3}
//	    staves:
//	      - clef: treble
//	        voices:
//	          - |
//	            | q.Bb4(p) eC5 hD5
//	            | qEb5 qD5 hC5
//	            |
//	    lanes:
//	      volume: [{at: "5:1", to: "5:3", value: 0.6}]
//	buses:
//	  a: {fx: [{fx: reverb, size: 0.7}], return: 0.25}
//	  b: {fx: [{fx: delay, beats: 0.75}], return: 0.2}
//	master: [{fx: compressor, threshold: -12}]
//
// Positions are "bar" or "bar:beat" (Score.ParsePos). An effect is a map
// whose "fx" key names it, the rest its settings in order. Instruments are
// named, never embedded: one .finst plays the same in every score.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v2"

	"foundry/internal/tracker"
)

// FileExt is a score's extension.
const FileExt = ".fscore"

// FormatVersion is written into every file and bumped on breaking changes.
const FormatVersion = 1

type rawScore struct {
	Version  int          `yaml:"version"`
	Name     string       `yaml:"name,omitempty"`
	Title    string       `yaml:"title,omitempty"`
	Composer string       `yaml:"composer,omitempty"`
	Bars     int          `yaml:"bars"`
	Pickup   string       `yaml:"pickup,omitempty"`
	Loop     bool         `yaml:"loop,omitempty"`
	Gain     *float32     `yaml:"gain,omitempty"`
	Tempo    []rawTempo   `yaml:"tempo,flow,omitempty"`
	Meter    []rawMeter   `yaml:"meter,flow,omitempty"`
	Key      []rawKey     `yaml:"key,flow,omitempty"`
	Sections []rawSection `yaml:"sections,omitempty"`
	Markers  []rawMarker  `yaml:"markers,flow,omitempty"`
	Repeats  []rawRepeat  `yaml:"repeats,flow,omitempty"`
	Params   []rawParam   `yaml:"params,flow,omitempty"`
	Bindings []rawBinding `yaml:"bindings,omitempty"`
	Lines    []rawLine    `yaml:"lines"`
	Buses    *rawBuses    `yaml:"buses,omitempty"`
	Master   []FX         `yaml:"master,flow,omitempty"`
}

type rawTempo struct {
	Bar  int     `yaml:"bar"`
	Beat string  `yaml:"beat,omitempty"`
	BPM  float64 `yaml:"bpm"`
	Unit string  `yaml:"unit,omitempty"`
	Text string  `yaml:"text,omitempty"`
	Ramp bool    `yaml:"ramp,omitempty"`
}

type rawMeter struct {
	Bar int    `yaml:"bar"`
	Sig string `yaml:"sig"`
}

type rawKey struct {
	Bar    int    `yaml:"bar"`
	Fifths int    `yaml:"fifths"`
	Mode   string `yaml:"mode,omitempty"`
}

type rawSection struct {
	Name string `yaml:"name"`
	Bar  int    `yaml:"bar"`
	Loop bool   `yaml:"loop,omitempty"`
	Next string `yaml:"next,omitempty"`
}

type rawMarker struct {
	Name string `yaml:"name"`
	Bar  int    `yaml:"bar"`
	Beat string `yaml:"beat,omitempty"`
}

type rawRepeat struct {
	Start int `yaml:"start"`
	End   int `yaml:"end"`
	Times int `yaml:"times,omitempty"`
}

type rawParam struct {
	Name    string  `yaml:"name"`
	Default float32 `yaml:"default"`
}

type rawBinding struct {
	Param  string  `yaml:"param"`
	Line   string  `yaml:"line"`
	Target string  `yaml:"target"`
	From   float32 `yaml:"from"`
	To     float32 `yaml:"to"`
}

type rawLine struct {
	Name       string                    `yaml:"name"`
	Instrument string                    `yaml:"instrument"`
	Volume     *float32                  `yaml:"volume,omitempty"`
	Pan        float32                   `yaml:"pan,omitempty"`
	Mute       bool                      `yaml:"mute,omitempty"`
	Solo       bool                      `yaml:"solo,omitempty"`
	Sends      *rawSends                 `yaml:"sends,flow,omitempty"`
	Inserts    []FX                      `yaml:"inserts,flow,omitempty"`
	Staves     []rawStaff                `yaml:"staves"`
	Lanes      map[string][]rawLanePoint `yaml:"lanes,omitempty"`
}

type rawSends struct {
	A float32 `yaml:"a,omitempty"`
	B float32 `yaml:"b,omitempty"`
}

type rawStaff struct {
	Clef      string   `yaml:"clef"`
	Transpose string   `yaml:"transpose,omitempty"`
	Voices    []string `yaml:"voices"`
}

type rawLanePoint struct {
	At    string  `yaml:"at"`
	To    string  `yaml:"to,omitempty"`
	Value float32 `yaml:"value"`
}

type rawBuses struct {
	A rawBus `yaml:"a"`
	B rawBus `yaml:"b"`
}

type rawBus struct {
	FX     []FX    `yaml:"fx,flow,omitempty"`
	Return float32 `yaml:"return"`
}

// MarshalYAML writes an effect as {fx: kind, setting: value, …}.
func (f FX) MarshalYAML() (interface{}, error) {
	ms := yaml.MapSlice{{Key: "fx", Value: f.Kind}}
	for _, p := range f.Params {
		ms = append(ms, yaml.MapItem{Key: p.Name, Value: p.Value})
	}
	return ms, nil
}

// UnmarshalYAML reads what MarshalYAML writes; true and false read as 1, 0.
func (f *FX) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var ms yaml.MapSlice
	if err := unmarshal(&ms); err != nil {
		return err
	}
	*f = FX{}
	for _, it := range ms {
		k, ok := it.Key.(string)
		if !ok {
			return fmt.Errorf("effect setting %v is not a name", it.Key)
		}
		if k == "fx" {
			s, ok := it.Value.(string)
			if !ok || s == "" {
				return fmt.Errorf("effect kind %v is not a name", it.Value)
			}
			f.Kind = s
			continue
		}
		var v float64
		switch x := it.Value.(type) {
		case int:
			v = float64(x)
		case float64:
			v = x
		case bool:
			if x {
				v = 1
			}
		default:
			return fmt.Errorf("effect setting %s: %v is not a number", k, it.Value)
		}
		for _, p := range f.Params {
			if p.Name == k {
				return fmt.Errorf("effect setting %s given twice", k)
			}
		}
		f.Params = append(f.Params, FXParam{Name: k, Value: v})
	}
	if f.Kind == "" {
		return fmt.Errorf("an effect needs an fx: name")
	}
	return nil
}

// LoadFile reads a score from disk; its Name is the file's library name
// when it is under sounds/, otherwise the file's base name.
func LoadFile(path string) (*Score, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	s.Name = nameFor(path)
	return s, nil
}

// Parse decodes a .fscore.
func Parse(b []byte) (s *Score, err error) {
	defer RecoverOverflow(&err)
	var r rawScore
	if err := yaml.UnmarshalStrict(b, &r); err != nil {
		return nil, err
	}
	if r.Version > FormatVersion {
		return nil, fmt.Errorf("score is format version %d; this build reads up to %d", r.Version, FormatVersion)
	}
	s = &Score{Name: r.Name, Title: r.Title, Composer: r.Composer, Bars: r.Bars, Loop: r.Loop, Gain: 1}
	if r.Gain != nil {
		s.Gain = *r.Gain
	}
	if r.Bars < 0 || r.Bars > MaxBars {
		return nil, fmt.Errorf("bars: %d (0..%d)", r.Bars, MaxBars)
	}
	if r.Pickup != "" {
		if s.Pickup, err = ParseFrac(r.Pickup); err != nil || s.Pickup.Sign() <= 0 {
			return nil, fmt.Errorf("pickup %q: want a length in whole notes, e.g. 1/4", r.Pickup)
		}
	}
	// Meters first: positions are counted in beats of the bar's meter.
	for _, rm := range r.Meter {
		m, err := ParseMeter(rm.Sig)
		if err != nil {
			return nil, fmt.Errorf("meter at bar %d: %w", rm.Bar, err)
		}
		if rm.Bar < 1 {
			return nil, fmt.Errorf("meter %s at bar %d: bars count from 1", rm.Sig, rm.Bar)
		}
		s.Meter = append(s.Meter, MeterMark{Bar: rm.Bar, Meter: m})
	}
	for _, rk := range r.Key {
		if rk.Bar < 1 || rk.Fifths < -7 || rk.Fifths > 7 {
			return nil, fmt.Errorf("key at bar %d: fifths %d (want -7..7, from bar 1)", rk.Bar, rk.Fifths)
		}
		s.Key = append(s.Key, KeyMark{Bar: rk.Bar, Fifths: rk.Fifths, Mode: rk.Mode})
	}
	sorted := func(n int, bar func(int) int) bool {
		for i := 1; i < n; i++ {
			if bar(i) < bar(i-1) {
				return false
			}
		}
		return true
	}
	if !sorted(len(s.Meter), func(i int) int { return s.Meter[i].Bar }) || !sorted(len(s.Key), func(i int) int { return s.Key[i].Bar }) {
		return nil, fmt.Errorf("meter and key changes must be in bar order")
	}
	pos := func(bar int, beat string) (Pos, error) {
		if beat == "" {
			return s.ParsePos(fmt.Sprint(bar))
		}
		return s.ParsePos(fmt.Sprintf("%d:%s", bar, beat))
	}
	for _, rt := range r.Tempo {
		p, err := pos(rt.Bar, rt.Beat)
		if err != nil {
			return nil, fmt.Errorf("tempo: %w", err)
		}
		if rt.BPM <= 0 || rt.BPM > 2000 {
			return nil, fmt.Errorf("tempo at bar %d: bpm %g", rt.Bar, rt.BPM)
		}
		tm := TempoMark{At: p, BPM: rt.BPM, Unit: Duration{Base: Quarter}, Text: rt.Text, Ramp: rt.Ramp}
		if rt.Unit != "" {
			if tm.Unit, err = parseDuration(rt.Unit); err != nil {
				return nil, fmt.Errorf("tempo at bar %d: unit: %w", rt.Bar, err)
			}
		}
		s.Tempo = append(s.Tempo, tm)
	}
	for _, rs := range r.Sections {
		if rs.Name == "" || rs.Bar < 1 {
			return nil, fmt.Errorf("section %q at bar %d: a section needs a name and a bar", rs.Name, rs.Bar)
		}
		s.Sections = append(s.Sections, Section{Name: rs.Name, Bar: rs.Bar, Loop: rs.Loop, Next: rs.Next})
	}
	for _, rm := range r.Markers {
		p, err := pos(rm.Bar, rm.Beat)
		if err != nil {
			return nil, fmt.Errorf("marker %s: %w", rm.Name, err)
		}
		s.Markers = append(s.Markers, Marker{Name: rm.Name, At: p})
	}
	for _, rr := range r.Repeats {
		s.Repeats = append(s.Repeats, Repeat{Start: rr.Start, End: rr.End, Times: rr.Times})
	}
	for _, rp := range r.Params {
		s.Params = append(s.Params, Param{Name: rp.Name, Default: rp.Default})
	}
	for _, rb := range r.Bindings {
		s.Bindings = append(s.Bindings, Binding{Param: rb.Param, Line: rb.Line, Target: tracker.BindTarget(rb.Target), From: rb.From, To: rb.To})
	}
	for li, rl := range r.Lines {
		l, err := lineFromRaw(s, rl)
		if err != nil {
			name := rl.Name
			if name == "" {
				name = fmt.Sprint(li + 1)
			}
			return nil, fmt.Errorf("line %s: %w", name, err)
		}
		s.Lines = append(s.Lines, l)
	}
	if r.Buses == nil {
		s.Buses = DefaultBuses()
	} else {
		s.Buses = Buses{A: Bus{FX: r.Buses.A.FX, Return: r.Buses.A.Return}, B: Bus{FX: r.Buses.B.FX, Return: r.Buses.B.Return}}
	}
	s.Master = r.Master
	return s, nil
}

func lineFromRaw(s *Score, rl rawLine) (*Line, error) {
	l := &Line{Name: rl.Name, Instrument: rl.Instrument, Volume: 1, Pan: rl.Pan, Mute: rl.Mute, Solo: rl.Solo, Inserts: rl.Inserts}
	if rl.Volume != nil {
		l.Volume = *rl.Volume
	}
	if rl.Sends != nil {
		l.SendA, l.SendB = rl.Sends.A, rl.Sends.B
	}
	if len(rl.Staves) == 0 {
		return nil, fmt.Errorf("no staves")
	}
	for si, rs := range rl.Staves {
		if !validStaffClef(rs.Clef) {
			return nil, fmt.Errorf("staff %d: clef %q (want one of %s)", si+1, rs.Clef, strings.Join(StaffClefs(), ", "))
		}
		st := &Staff{Clef: rs.Clef}
		if rs.Transpose != "" {
			iv, err := ParseInterval(rs.Transpose)
			if err != nil {
				return nil, fmt.Errorf("staff %d: transpose: %w", si+1, err)
			}
			st.Transpose = iv
		}
		if len(rs.Voices) > MaxVoices {
			return nil, fmt.Errorf("staff %d: %d voices (at most %d)", si+1, len(rs.Voices), MaxVoices)
		}
		for vi, text := range rs.Voices {
			v, err := ParseVoice(text)
			if err != nil {
				return nil, fmt.Errorf("staff %d, voice %d: %w", si+1, vi+1, err)
			}
			st.Voices = append(st.Voices, v)
		}
		if len(st.Voices) == 0 {
			st.Voices = []*Voice{{}}
		}
		l.Staves = append(l.Staves, st)
	}
	names := make([]string, 0, len(rl.Lanes))
	for n := range rl.Lanes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		ln := Lane{Name: n}
		for _, rp := range rl.Lanes[n] {
			at, err := s.ParsePos(rp.At)
			if err != nil {
				return nil, fmt.Errorf("lane %s: %w", n, err)
			}
			lp := LanePoint{At: at, Value: rp.Value}
			if rp.To != "" {
				if lp.To, err = s.ParsePos(rp.To); err != nil {
					return nil, fmt.Errorf("lane %s: %w", n, err)
				}
				lp.HasTo = true
			}
			ln.Points = append(ln.Points, lp)
		}
		l.Lanes = append(l.Lanes, ln)
	}
	return l, nil
}

// StaffClefs are the clefs a staff may have: the instrument clefs but the
// two-staff "grand".
func StaffClefs() []string {
	var out []string
	for _, c := range tracker.Clefs {
		if c != "grand" {
			out = append(out, c)
		}
	}
	return out
}

func validStaffClef(c string) bool {
	for _, v := range StaffClefs() {
		if v == c {
			return true
		}
	}
	return false
}

// parseDuration reads a bare duration: "q", "q.", "h".
func parseDuration(s string) (Duration, error) {
	if s == "" {
		return Duration{}, fmt.Errorf("empty duration")
	}
	k := strings.IndexByte(durLetters, s[0])
	if k < 0 || strings.Trim(s[1:], ".") != "" || len(s)-1 > 3 {
		return Duration{}, fmt.Errorf("bad duration %q (want b w h q e s t x y z and dots)", s)
	}
	return Duration{Base: DurBase(k - 1), Dots: uint8(len(s) - 1)}, nil
}

// Marshal encodes a score as a .fscore.
func Marshal(s *Score) ([]byte, error) {
	r := rawScore{Version: FormatVersion, Title: s.Title, Composer: s.Composer, Bars: s.Measures(), Loop: s.Loop}
	if s.Gain != 1 {
		g := s.Gain
		r.Gain = &g
	}
	if s.Pickup.Sign() > 0 {
		r.Pickup = s.Pickup.String()
	}
	beat := func(p Pos) string {
		t := s.FormatPos(p)
		_, b, _ := strings.Cut(t, ":")
		return b
	}
	for _, t := range s.Tempo {
		rt := rawTempo{Bar: t.At.Bar, Beat: beat(t.At), BPM: t.BPM, Text: t.Text, Ramp: t.Ramp}
		if t.Unit != (Duration{Base: Quarter}) && t.Unit != (Duration{}) {
			rt.Unit = t.Unit.String()
		}
		r.Tempo = append(r.Tempo, rt)
	}
	for _, m := range s.Meter {
		r.Meter = append(r.Meter, rawMeter{Bar: m.Bar, Sig: m.Meter.String()})
	}
	for _, k := range s.Key {
		r.Key = append(r.Key, rawKey{Bar: k.Bar, Fifths: k.Fifths, Mode: k.Mode})
	}
	for _, sec := range s.Sections {
		r.Sections = append(r.Sections, rawSection{Name: sec.Name, Bar: sec.Bar, Loop: sec.Loop, Next: sec.Next})
	}
	for _, m := range s.Markers {
		r.Markers = append(r.Markers, rawMarker{Name: m.Name, Bar: m.At.Bar, Beat: beat(m.At)})
	}
	for _, rp := range s.Repeats {
		r.Repeats = append(r.Repeats, rawRepeat{Start: rp.Start, End: rp.End, Times: rp.Times})
	}
	for _, p := range s.Params {
		r.Params = append(r.Params, rawParam{Name: p.Name, Default: p.Default})
	}
	for _, b := range s.Bindings {
		r.Bindings = append(r.Bindings, rawBinding{Param: b.Param, Line: b.Line, Target: string(b.Target), From: b.From, To: b.To})
	}
	for _, l := range s.Lines {
		rl := rawLine{Name: l.Name, Instrument: l.Instrument, Pan: l.Pan, Mute: l.Mute, Solo: l.Solo, Inserts: l.Inserts}
		if l.Volume != 1 {
			v := l.Volume
			rl.Volume = &v
		}
		if l.SendA != 0 || l.SendB != 0 {
			rl.Sends = &rawSends{A: l.SendA, B: l.SendB}
		}
		for _, st := range l.Staves {
			rs := rawStaff{Clef: st.Clef, Voices: []string{}}
			if st.Transpose != (Interval{}) {
				rs.Transpose = st.Transpose.String()
			}
			for _, v := range st.Voices {
				rs.Voices = append(rs.Voices, FormatVoice(v))
			}
			rl.Staves = append(rl.Staves, rs)
		}
		if len(l.Lanes) > 0 {
			rl.Lanes = map[string][]rawLanePoint{}
			for _, ln := range l.Lanes {
				pts := []rawLanePoint{}
				for _, p := range ln.Points {
					rp := rawLanePoint{At: s.FormatPos(p.At), Value: p.Value}
					if p.HasTo {
						rp.To = s.FormatPos(p.To)
					}
					pts = append(pts, rp)
				}
				rl.Lanes[ln.Name] = pts
			}
		}
		r.Lines = append(r.Lines, rl)
	}
	if r.Lines == nil {
		r.Lines = []rawLine{}
	}
	r.Buses = &rawBuses{A: rawBus{FX: s.Buses.A.FX, Return: s.Buses.A.Return}, B: rawBus{FX: s.Buses.B.FX, Return: s.Buses.B.Return}}
	r.Master = s.Master
	return yaml.Marshal(r)
}

// SaveFile writes a score to disk.
func SaveFile(path string, s *Score) error {
	b, err := Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
