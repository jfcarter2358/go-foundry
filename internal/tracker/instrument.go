package tracker

// instrument.go — what a note in a cell actually plays.
//
// An instrument wraps one or more samples with everything that shapes a note
// after it starts: a keymap choosing the sample per note, envelopes for
// volume, pan, pitch and filter, a filter, auto-vibrato and a release. Times
// are in seconds rather than ticks, so an instrument sounds the same whatever
// tempo the song runs at.

// Interp is how a voice reads between sample frames.
type Interp uint8

const (
	// InterpSong takes the song's default.
	InterpSong Interp = iota
	// InterpNearest is the crunchy, aliased sound of an Amiga.
	InterpNearest
	InterpLinear
	// InterpCubic is 4-point Hermite, the default.
	InterpCubic
)

func (i Interp) String() string {
	switch i {
	case InterpNearest:
		return "nearest"
	case InterpLinear:
		return "linear"
	case InterpCubic:
		return "cubic"
	}
	return ""
}

func parseInterp(s string) Interp {
	switch s {
	case "nearest":
		return InterpNearest
	case "linear":
		return InterpLinear
	case "cubic":
		return InterpCubic
	}
	return InterpSong
}

// FilterMode is the instrument filter's shape.
type FilterMode uint8

const (
	FilterOff FilterMode = iota
	FilterLowPass
	FilterHighPass
	FilterBandPass
)

func (m FilterMode) String() string {
	switch m {
	case FilterLowPass:
		return "lowpass"
	case FilterHighPass:
		return "highpass"
	case FilterBandPass:
		return "bandpass"
	}
	return "off"
}

func parseFilterMode(s string) FilterMode {
	switch s {
	case "lowpass", "lp":
		return FilterLowPass
	case "highpass", "hp":
		return FilterHighPass
	case "bandpass", "bp":
		return FilterBandPass
	}
	return FilterOff
}

// EnvPoint is one breakpoint: a time in seconds and a value.
type EnvPoint struct {
	T, V float32
}

// Envelope is a breakpoint curve. Empty means off.
//
// Sustain holds the curve at that point while the note is held; LoopStart and
// LoopEnd repeat a stretch while it is held. Both are point indices, -1 off.
// Values mean: volume 0..1, pan -1..1, pitch semitones, filter octaves.
type Envelope struct {
	Points    []EnvPoint
	Sustain   int
	LoopStart int
	LoopEnd   int
}

// Off returns an empty envelope with its indices unset.
func EnvelopeOff() Envelope { return Envelope{Sustain: -1, LoopStart: -1, LoopEnd: -1} }

// On reports whether the envelope does anything.
func (e *Envelope) On() bool { return len(e.Points) > 0 }

// Advance moves an envelope clock on by dt, honouring sustain and loop while
// the note is held.
func (e *Envelope) Advance(t, dt float32, released bool) float32 {
	if len(e.Points) == 0 {
		return t + dt
	}
	t += dt
	if released {
		return t
	}
	if e.LoopStart >= 0 && e.LoopEnd > e.LoopStart && e.LoopEnd < len(e.Points) {
		ls, le := e.Points[e.LoopStart].T, e.Points[e.LoopEnd].T
		if le > ls {
			for t >= le {
				t -= le - ls
			}
			return t
		}
	}
	if e.Sustain >= 0 && e.Sustain < len(e.Points) {
		if st := e.Points[e.Sustain].T; t > st {
			t = st
		}
	}
	return t
}

// Value reads the curve at t, linearly between points, holding the ends.
func (e *Envelope) Value(t float32) float32 {
	p := e.Points
	if len(p) == 0 {
		return 0
	}
	if t <= p[0].T {
		return p[0].V
	}
	for i := 1; i < len(p); i++ {
		if t < p[i].T {
			a, b := p[i-1], p[i]
			if b.T <= a.T {
				return b.V
			}
			return a.V + (b.V-a.V)*(t-a.T)/(b.T-a.T)
		}
	}
	return p[len(p)-1].V
}

// Done reports a clock past the last point.
func (e *Envelope) Done(t float32) bool {
	return len(e.Points) > 0 && t >= e.Points[len(e.Points)-1].T
}

func (e Envelope) clone() Envelope {
	e.Points = append([]EnvPoint(nil), e.Points...)
	return e
}

// Zone maps a note range to one of the instrument's samples.
type Zone struct {
	Sample   int
	Low      uint8
	High     uint8
	BaseNote uint8 // 0 takes the sample's own
}

// Instrument is a playable sound built from samples.
type Instrument struct {
	Name    string
	Samples []string
	// Zones pick a sample per note. Empty plays Samples[0] for everything.
	Zones []Zone

	Volume    float32 // 0..1
	Pan       float32 // -1..1
	Transpose int     // semitones
	FineTune  float32 // cents

	VolEnv, PanEnv, PitchEnv, FilterEnv Envelope

	Filter    FilterMode
	Cutoff    float32 // Hz
	Resonance float32 // 0..1

	// FadeOut is the release in seconds after a note-off, used when the
	// volume envelope has no sustain to release from.
	FadeOut float32

	VibRate  float32 // Hz
	VibDepth float32 // semitones
	VibDelay float32 // seconds before it starts

	// Polyphony > 1 lets a new note on the channel leave the old one to
	// release instead of cutting it, up to this many at once.
	Polyphony int
	Interp    Interp

	// loaded is Samples resolved against the library. Runtime only.
	loaded []*Sample
}

// NewInstrument returns an instrument playing one sample.
func NewInstrument(name, sample string) *Instrument {
	in := &Instrument{
		Name: name, Volume: 1, FadeOut: 0.05, Cutoff: 20000, Polyphony: 1,
		VolEnv: EnvelopeOff(), PanEnv: EnvelopeOff(), PitchEnv: EnvelopeOff(), FilterEnv: EnvelopeOff(),
	}
	if sample != "" {
		in.Samples = []string{sample}
	}
	return in
}

// Clone copies the instrument; decoded samples are shared.
func (in *Instrument) Clone() *Instrument {
	c := *in
	c.Samples = append([]string(nil), in.Samples...)
	c.Zones = append([]Zone(nil), in.Zones...)
	c.VolEnv, c.PanEnv = in.VolEnv.clone(), in.PanEnv.clone()
	c.PitchEnv, c.FilterEnv = in.PitchEnv.clone(), in.FilterEnv.clone()
	c.loaded = append([]*Sample(nil), in.loaded...)
	return &c
}

// Resolve loads the instrument's samples through lookup, reporting the ones
// that would not load. A missing sample leaves a silent slot, not a failure.
func (in *Instrument) Resolve(lookup func(string) (*Sample, error)) []error {
	var errs []error
	in.loaded = make([]*Sample, len(in.Samples))
	for i, n := range in.Samples {
		s, err := lookup(n)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		in.loaded[i] = s
	}
	return errs
}

// SetLoaded installs decoded samples directly (tests, the editor).
func (in *Instrument) SetLoaded(i int, s *Sample) {
	for len(in.loaded) <= i {
		in.loaded = append(in.loaded, nil)
	}
	in.loaded[i] = s
}

// Loaded returns the decoded sample at i, or nil.
func (in *Instrument) Loaded(i int) *Sample {
	if i < 0 || i >= len(in.loaded) {
		return nil
	}
	return in.loaded[i]
}

// SampleFor picks the sample and its base note for a note.
func (in *Instrument) SampleFor(note uint8) (*Sample, uint8) {
	idx, base := 0, uint8(0)
	for _, z := range in.Zones {
		if note >= z.Low && note <= z.High {
			idx, base = z.Sample, z.BaseNote
			break
		}
	}
	s := in.Loaded(idx)
	if s == nil {
		return nil, 0
	}
	if base == 0 {
		base = s.BaseNote
	}
	return s, base
}
