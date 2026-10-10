package tracker

// ftrk.go — the .ftrk song file.
//
// YAML, with each pattern's grid stored as a block of tracker text (see
// notation.go). The format is text so that a song diffs in git, can be read
// and written by hand or by Claude, and needs no tool to inspect. Samples are
// referenced by library name rather than embedded, so songs stay a few
// kilobytes and share their sounds.

import (
	"fmt"
	"os"
	"sort"

	"gopkg.in/yaml.v2"
)

// FileExt is a song's extension.
const FileExt = ".ftrk"

// FormatVersion is written into every file and bumped on breaking changes.
const FormatVersion = 1

type rawSong struct {
	Version     int             `yaml:"version"`
	Name        string          `yaml:"name,omitempty"`
	BPM         float32         `yaml:"bpm"`
	LPB         int             `yaml:"lpb,omitempty"`
	TicksPerRow int             `yaml:"ticks_per_row,omitempty"`
	BeatsPerBar int             `yaml:"beats_per_bar,omitempty"`
	FXColumns   int             `yaml:"fx_columns,omitempty"`
	Gain        *float32        `yaml:"gain,omitempty"`
	Loop        *bool           `yaml:"loop,omitempty"`
	BeatEvents  bool            `yaml:"beat_events,omitempty"`
	Interp      string          `yaml:"interp,omitempty"`
	Channels    []rawChannel    `yaml:"channels"`
	Instruments []rawInstrument `yaml:"instruments,omitempty"`
	Order       []int           `yaml:"order,flow"`
	Sections    []rawSection    `yaml:"sections,omitempty"`
	Params      []rawParam      `yaml:"params,omitempty"`
	Bindings    []rawBinding    `yaml:"bindings,omitempty"`
	Markers     map[int]string  `yaml:"markers,omitempty"`
	Reverb      *ReverbSpec     `yaml:"reverb,omitempty"`
	Delay       *rawDelay       `yaml:"delay,omitempty"`
	Compressor  *rawCompressor  `yaml:"compressor,omitempty"`
	Patterns    []rawPattern    `yaml:"patterns"`
}

type rawChannel struct {
	Name   string   `yaml:"name"`
	Volume *float32 `yaml:"volume,omitempty"`
	Pan    float32  `yaml:"pan,omitempty"`
	Mute   bool     `yaml:"mute,omitempty"`
	SendA  float32  `yaml:"send_a,omitempty"`
	SendB  float32  `yaml:"send_b,omitempty"`
	Drive  float32  `yaml:"drive,omitempty"`
	Crush  int      `yaml:"crush,omitempty"`
}

type rawEnv struct {
	Points    [][2]float32 `yaml:"points,flow"`
	Sustain   *int         `yaml:"sustain,omitempty"`
	LoopStart *int         `yaml:"loop_start,omitempty"`
	LoopEnd   *int         `yaml:"loop_end,omitempty"`
}

type rawZone struct {
	Sample int    `yaml:"sample"`
	Low    string `yaml:"low"`
	High   string `yaml:"high"`
	Base   string `yaml:"base,omitempty"`
}

type rawInstrument struct {
	Name      string    `yaml:"name"`
	Samples   []string  `yaml:"samples,flow"`
	Zones     []rawZone `yaml:"zones,omitempty"`
	Volume    *float32  `yaml:"volume,omitempty"`
	Pan       float32   `yaml:"pan,omitempty"`
	Transpose int       `yaml:"transpose,omitempty"`
	FineTune  float32   `yaml:"fine_tune,omitempty"`
	VolEnv    *rawEnv   `yaml:"volume_env,omitempty"`
	PanEnv    *rawEnv   `yaml:"pan_env,omitempty"`
	PitchEnv  *rawEnv   `yaml:"pitch_env,omitempty"`
	FilterEnv *rawEnv   `yaml:"filter_env,omitempty"`
	Filter    string    `yaml:"filter,omitempty"`
	Cutoff    float32   `yaml:"cutoff,omitempty"`
	Resonance float32   `yaml:"resonance,omitempty"`
	FadeOut   *float32  `yaml:"fade_out,omitempty"`
	VibRate   float32   `yaml:"vibrato_rate,omitempty"`
	VibDepth  float32   `yaml:"vibrato_depth,omitempty"`
	VibDelay  float32   `yaml:"vibrato_delay,omitempty"`
	Polyphony int       `yaml:"polyphony,omitempty"`
	Interp    string    `yaml:"interp,omitempty"`
}

type rawSection struct {
	Name  string `yaml:"name"`
	Start int    `yaml:"start"`
	End   int    `yaml:"end"`
	Loop  bool   `yaml:"loop,omitempty"`
	Next  string `yaml:"next,omitempty"`
}

type rawParam struct {
	Name    string  `yaml:"name"`
	Default float32 `yaml:"default,omitempty"`
}

type rawBinding struct {
	Param   string  `yaml:"param"`
	Channel string  `yaml:"channel"`
	Target  string  `yaml:"target"`
	From    float32 `yaml:"from"`
	To      float32 `yaml:"to"`
}

type rawDelay struct {
	Beats    float32 `yaml:"beats"`
	Feedback float32 `yaml:"feedback"`
	Tone     float32 `yaml:"tone"`
	PingPong bool    `yaml:"ping_pong,omitempty"`
	Return   float32 `yaml:"return"`
}

type rawCompressor struct {
	Threshold float32 `yaml:"threshold"`
	Ratio     float32 `yaml:"ratio"`
	Attack    float32 `yaml:"attack"`
	Release   float32 `yaml:"release"`
	Makeup    float32 `yaml:"makeup,omitempty"`
}

type rawPattern struct {
	Name string `yaml:"name,omitempty"`
	Rows int    `yaml:"rows"`
	Data string `yaml:"data,omitempty"`
}

// LoadFile reads a song from disk. Samples are not resolved; see Load in
// library.go for the cached, resolved path a game uses.
func LoadFile(path string) (*Song, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Parse decodes a song from .ftrk bytes.
func Parse(b []byte) (*Song, error) {
	var r rawSong
	if err := yaml.UnmarshalStrict(b, &r); err != nil {
		return nil, err
	}
	if r.Version > FormatVersion {
		return nil, fmt.Errorf("song is format version %d; this build reads up to %d", r.Version, FormatVersion)
	}
	s := NewSong(r.Name)
	s.BPM = r.BPM
	s.LPB = orInt(r.LPB, 4)
	s.TicksPerRow = orInt(r.TicksPerRow, 6)
	s.BeatsPerBar = orInt(r.BeatsPerBar, 4)
	s.FXColumns = orInt(r.FXColumns, 2)
	if r.Gain != nil {
		s.Gain = *r.Gain
	}
	if r.Loop != nil {
		s.Loop = *r.Loop
	}
	s.BeatEvents = r.BeatEvents
	if in := parseInterp(r.Interp); in != InterpSong {
		s.Interp = in
	}

	s.Channels = nil
	for _, c := range r.Channels {
		s.Channels = append(s.Channels, Channel{
			Name: c.Name, Volume: orF(c.Volume, 1), Pan: c.Pan, Mute: c.Mute, SendA: c.SendA, SendB: c.SendB,
			Drive: c.Drive, Crush: clampInt(c.Crush, 0, 16),
		})
	}
	if len(s.Channels) == 0 {
		return nil, fmt.Errorf("song has no channels")
	}
	if len(s.Channels) > MaxChannels {
		return nil, fmt.Errorf("song has %d channels; the limit is %d", len(s.Channels), MaxChannels)
	}

	for i, ri := range r.Instruments {
		in, err := instrumentFromRaw(ri)
		if err != nil {
			return nil, fmt.Errorf("instrument %d (%s): %w", i+1, ri.Name, err)
		}
		s.Instruments = append(s.Instruments, in)
	}
	if len(s.Instruments) > MaxInstruments {
		return nil, fmt.Errorf("%d instruments; the limit is %d", len(s.Instruments), MaxInstruments)
	}

	s.Patterns = nil
	for i, rp := range r.Patterns {
		rows := rp.Rows
		if rows == 0 {
			rows = 64
		}
		if rows < 1 || rows > MaxRows {
			return nil, fmt.Errorf("pattern %d has %d rows; 1..%d allowed", i, rows, MaxRows)
		}
		p := NewPattern(rp.Name, rows, len(s.Channels))
		if err := ParsePatternText(p, rp.Data); err != nil {
			return nil, fmt.Errorf("pattern %d: %w", i, err)
		}
		s.Patterns = append(s.Patterns, p)
	}
	s.Order = append([]int(nil), r.Order...)

	for _, rs := range r.Sections {
		s.Sections = append(s.Sections, Section{Name: rs.Name, Start: rs.Start, End: rs.End, Loop: rs.Loop, Next: rs.Next})
	}
	for _, rp := range r.Params {
		s.Params = append(s.Params, Param{Name: rp.Name, Default: rp.Default})
	}
	for _, rb := range r.Bindings {
		s.Bindings = append(s.Bindings, Binding{Param: rb.Param, Channel: rb.Channel, Target: BindTarget(rb.Target), From: rb.From, To: rb.To})
	}
	if len(r.Markers) > 0 {
		s.Markers = map[uint8]string{}
		for k, v := range r.Markers {
			if k < 0 || k > 255 {
				return nil, fmt.Errorf("marker %d out of range 0..255", k)
			}
			s.Markers[uint8(k)] = v
		}
	}
	if r.Reverb != nil {
		s.Reverb = *r.Reverb
	}
	if d := r.Delay; d != nil {
		s.Delay = DelaySpec{Beats: d.Beats, Feedback: d.Feedback, Tone: d.Tone, PingPong: d.PingPong, Return: d.Return}
	}
	if c := r.Compressor; c != nil {
		s.Compressor = CompressorSpec{Enabled: true, Threshold: c.Threshold, Ratio: c.Ratio, Attack: c.Attack, Release: c.Release, Makeup: c.Makeup}
	}
	s.Validate()
	return s, nil
}

func instrumentFromRaw(ri rawInstrument) (*Instrument, error) {
	in := NewInstrument(ri.Name, "")
	in.Samples = append([]string(nil), ri.Samples...)
	for _, z := range ri.Zones {
		lo, err := ParseNote(z.Low)
		if err != nil {
			return nil, err
		}
		hi, err := ParseNote(z.High)
		if err != nil {
			return nil, err
		}
		var base uint8
		if z.Base != "" {
			if base, err = ParseNote(z.Base); err != nil {
				return nil, err
			}
		}
		if z.Sample < 0 || z.Sample >= len(in.Samples) {
			return nil, fmt.Errorf("zone names sample %d of %d", z.Sample, len(in.Samples))
		}
		in.Zones = append(in.Zones, Zone{Sample: z.Sample, Low: lo, High: hi, BaseNote: base})
	}
	in.Volume = orF(ri.Volume, 1)
	in.Pan = ri.Pan
	in.Transpose = ri.Transpose
	in.FineTune = ri.FineTune
	in.VolEnv = envFromRaw(ri.VolEnv)
	in.PanEnv = envFromRaw(ri.PanEnv)
	in.PitchEnv = envFromRaw(ri.PitchEnv)
	in.FilterEnv = envFromRaw(ri.FilterEnv)
	in.Filter = parseFilterMode(ri.Filter)
	if ri.Cutoff > 0 {
		in.Cutoff = ri.Cutoff
	}
	in.Resonance = ri.Resonance
	in.FadeOut = orF(ri.FadeOut, 0.05)
	in.VibRate, in.VibDepth, in.VibDelay = ri.VibRate, ri.VibDepth, ri.VibDelay
	in.Polyphony = orInt(ri.Polyphony, 1)
	in.Interp = parseInterp(ri.Interp)
	return in, nil
}

func envFromRaw(r *rawEnv) Envelope {
	e := EnvelopeOff()
	if r == nil {
		return e
	}
	for _, p := range r.Points {
		e.Points = append(e.Points, EnvPoint{T: p[0], V: p[1]})
	}
	if r.Sustain != nil {
		e.Sustain = *r.Sustain
	}
	if r.LoopStart != nil && r.LoopEnd != nil {
		e.LoopStart, e.LoopEnd = *r.LoopStart, *r.LoopEnd
	}
	return e
}

func envToRaw(e Envelope) *rawEnv {
	if !e.On() {
		return nil
	}
	r := &rawEnv{}
	for _, p := range e.Points {
		r.Points = append(r.Points, [2]float32{p.T, p.V})
	}
	if e.Sustain >= 0 {
		v := e.Sustain
		r.Sustain = &v
	}
	if e.LoopStart >= 0 && e.LoopEnd > e.LoopStart {
		a, b := e.LoopStart, e.LoopEnd
		r.LoopStart, r.LoopEnd = &a, &b
	}
	return r
}

// Marshal encodes a song as .ftrk bytes.
func Marshal(s *Song) ([]byte, error) {
	r := rawSong{
		Version: FormatVersion, Name: s.Name, BPM: s.BPM,
		LPB: s.LPB, TicksPerRow: s.TicksPerRow, BeatsPerBar: s.BeatsPerBar, FXColumns: s.FXColumns,
		BeatEvents: s.BeatEvents, Order: s.Order,
	}
	if s.Interp != InterpCubic {
		r.Interp = s.Interp.String()
	}
	if s.Gain != 1 {
		g := s.Gain
		r.Gain = &g
	}
	if !s.Loop {
		f := false
		r.Loop = &f
	}
	for _, c := range s.Channels {
		rc := rawChannel{Name: c.Name, Pan: c.Pan, Mute: c.Mute, SendA: c.SendA, SendB: c.SendB, Drive: c.Drive, Crush: c.Crush}
		if c.Volume != 1 {
			v := c.Volume
			rc.Volume = &v
		}
		r.Channels = append(r.Channels, rc)
	}
	for _, in := range s.Instruments {
		ri := rawInstrument{
			Name: in.Name, Samples: in.Samples, Pan: in.Pan, Transpose: in.Transpose, FineTune: in.FineTune,
			VolEnv: envToRaw(in.VolEnv), PanEnv: envToRaw(in.PanEnv), PitchEnv: envToRaw(in.PitchEnv), FilterEnv: envToRaw(in.FilterEnv),
			Resonance: in.Resonance, VibRate: in.VibRate, VibDepth: in.VibDepth, VibDelay: in.VibDelay,
			Interp: in.Interp.String(),
		}
		for _, z := range in.Zones {
			rz := rawZone{Sample: z.Sample, Low: NoteName(z.Low), High: NoteName(z.High)}
			if z.BaseNote != 0 {
				rz.Base = NoteName(z.BaseNote)
			}
			ri.Zones = append(ri.Zones, rz)
		}
		if in.Volume != 1 {
			v := in.Volume
			ri.Volume = &v
		}
		if in.Filter != FilterOff {
			ri.Filter = in.Filter.String()
			ri.Cutoff = in.Cutoff
		}
		if in.FadeOut != 0.05 {
			v := in.FadeOut
			ri.FadeOut = &v
		}
		if in.Polyphony > 1 {
			ri.Polyphony = in.Polyphony
		}
		r.Instruments = append(r.Instruments, ri)
	}
	for _, sec := range s.Sections {
		r.Sections = append(r.Sections, rawSection{Name: sec.Name, Start: sec.Start, End: sec.End, Loop: sec.Loop, Next: sec.Next})
	}
	for _, p := range s.Params {
		r.Params = append(r.Params, rawParam{Name: p.Name, Default: p.Default})
	}
	for _, b := range s.Bindings {
		r.Bindings = append(r.Bindings, rawBinding{Param: b.Param, Channel: b.Channel, Target: string(b.Target), From: b.From, To: b.To})
	}
	if len(s.Markers) > 0 {
		r.Markers = map[int]string{}
		keys := make([]int, 0, len(s.Markers))
		for k := range s.Markers {
			keys = append(keys, int(k))
		}
		sort.Ints(keys)
		for _, k := range keys {
			r.Markers[k] = s.Markers[uint8(k)]
		}
	}
	rv := s.Reverb
	r.Reverb = &rv
	r.Delay = &rawDelay{Beats: s.Delay.Beats, Feedback: s.Delay.Feedback, Tone: s.Delay.Tone, PingPong: s.Delay.PingPong, Return: s.Delay.Return}
	if c := s.Compressor; c.Enabled {
		r.Compressor = &rawCompressor{Threshold: c.Threshold, Ratio: c.Ratio, Attack: c.Attack, Release: c.Release, Makeup: c.Makeup}
	}
	for _, p := range s.Patterns {
		r.Patterns = append(r.Patterns, rawPattern{Name: p.Name, Rows: p.Rows(), Data: FormatPattern(p, s.FXColumns)})
	}
	return yaml.Marshal(r)
}

// SaveFile writes a song to disk.
func SaveFile(path string, s *Song) error {
	b, err := Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func orInt(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func orF(v *float32, def float32) float32 {
	if v == nil {
		return def
	}
	return *v
}
