package tracker

// instrument.go — what a note in a cell actually plays.
//
// An instrument wraps one or more samples with everything that shapes a note
// after it starts: a keymap choosing the sample per note, envelopes for
// volume, pan, pitch and filter, a filter, auto-vibrato and a release. Times
// are in seconds rather than ticks, so an instrument sounds the same whatever
// tempo the song runs at.

import (
	"math"
	"strings"
)

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
	// RoundRobin are alternates of Sample, played in turn one note each so a
	// repeated note does not machine-gun. Used when the zone has no Layers.
	RoundRobin []int
	// Layers split the zone by velocity. Empty plays Sample (and its round
	// robin) at every velocity.
	Layers []Layer
}

// Layer is one velocity layer of a zone: a sample, and its round-robin
// alternates, over a velocity range. Layers whose ranges overlap crossfade
// across the overlap at equal power, so a swell through the velocities never
// steps from one recording to the next.
type Layer struct {
	Sample     int
	RoundRobin []int
	// VelLow and VelHigh are the range in MIDI velocity, 0..127, inclusive.
	VelLow, VelHigh uint8
	// Gain trims the layer in dB.
	Gain float32
}

// Articulation is a named way of playing the instrument — sustain,
// staccato, pizzicato, tremolo, muted, legato — chosen by a notation mark or
// technique text in the Score and by the @ effect in the Tracker.
type Articulation struct {
	Name string
	// Triggers are further words that pick it — technique text ("pizz.",
	// "con sord.") or mark names ("staccato") — matched without case.
	Triggers []string
	// Zones replace the instrument's while it plays. Empty keeps them: an
	// articulation can be only a shorter envelope over the same samples.
	Zones []Zone
	// Gain trims the articulation in dB.
	Gain float32
	// VolEnv, when on, replaces the instrument's volume envelope.
	VolEnv Envelope
	// FadeOut, above 0, replaces the instrument's release.
	FadeOut float32
}

// GlissStyle is how a glissando between two notes plays.
type GlissStyle uint8

const (
	// GlissSlide is a continuous pitch slide: a trombone, a fretless string.
	GlissSlide GlissStyle = iota
	// GlissChromatic steps through every semitone between: a piano's black
	// and white keys.
	GlissChromatic
	// GlissDiatonic steps through the key's scale: a harp, a piano's white keys.
	GlissDiatonic
)

func (g GlissStyle) String() string {
	switch g {
	case GlissChromatic:
		return "chromatic"
	case GlissDiatonic:
		return "diatonic"
	}
	return "slide"
}

// ParseGlissStyle reads a style's name; anything else is a slide.
func ParseGlissStyle(s string) GlissStyle {
	switch strings.ToLower(s) {
	case "chromatic":
		return GlissChromatic
	case "diatonic":
		return GlissDiatonic
	}
	return GlissSlide
}

// Clefs an instrument may default to. "grand" is a treble and bass pair
// (piano, harp, organ); "percussion" is the neutral clef of a drum staff.
var Clefs = []string{"treble", "bass", "alto", "tenor", "treble_8vb", "treble_8va", "bass_8vb", "bass_8va", "percussion", "grand"}

// Playback is what notation needs to know about an instrument beyond its
// sound: how it is written, and how its marks play.
type Playback struct {
	// Low and High are the playable range, sounding pitch; 0 is unbounded.
	Low, High uint8
	// Transposition is written to sounding, in semitones: a part written
	// for a B-flat clarinet sounds -2, a horn in F -7, a piccolo +12, a
	// contrabass -12. Sounding = written + Transposition.
	Transposition int
	// Clef is the staff a new line for the instrument starts with.
	Clef string
	// Gliss is how a glissando plays.
	Gliss GlissStyle
	// Staccato is the fraction of its written length a staccato note
	// sounds; 0 reads as a half.
	Staccato float32
	// Release, above 0, is the release in seconds the Score gives a note
	// at its written end, in place of the instrument's fade-out.
	Release float32
}

// StaccatoLength is Staccato with its default.
func (p Playback) StaccatoLength() float32 {
	if p.Staccato <= 0 || p.Staccato > 1 {
		return 0.5
	}
	return p.Staccato
}

// InRange reports whether a sounding note is in the playable range.
func (p Playback) InRange(note uint8) bool {
	return (p.Low == 0 || note >= p.Low) && (p.High == 0 || note <= p.High)
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

	// Articulations are the instrument's other ways of playing, beside its
	// own zones (the default, usually "sustain").
	Articulations []Articulation
	// Playback is how notation writes and plays it.
	Playback Playback

	// Ref, when set, names a library instrument (sounds/instruments/<ref>
	// .finst) this slot plays: resolving copies it in over everything above
	// but the Name, and a song saves only the name and the ref.
	Ref string
	// Category and Description describe a library instrument.
	Category    string
	Description string

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
	c.Zones = cloneZones(in.Zones)
	c.Articulations = make([]Articulation, len(in.Articulations))
	for i, a := range in.Articulations {
		a.Triggers = append([]string(nil), a.Triggers...)
		a.Zones = cloneZones(a.Zones)
		a.VolEnv = a.VolEnv.clone()
		c.Articulations[i] = a
	}
	if in.Articulations == nil {
		c.Articulations = nil
	}
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

func cloneZones(zs []Zone) []Zone {
	if zs == nil {
		return nil
	}
	out := make([]Zone, len(zs))
	for i, z := range zs {
		z.RoundRobin = append([]int(nil), z.RoundRobin...)
		if z.Layers != nil {
			ls := make([]Layer, len(z.Layers))
			for j, l := range z.Layers {
				l.RoundRobin = append([]int(nil), l.RoundRobin...)
				ls[j] = l
			}
			z.Layers = ls
		}
		out[i] = z
	}
	return out
}

// RemoveSample drops sample slot i. Every zone, layer and round-robin entry
// past it moves down with it; round-robin entries naming it go.
func (in *Instrument) RemoveSample(i int) {
	if i < 0 || i >= len(in.Samples) {
		return
	}
	in.Samples = append(in.Samples[:i], in.Samples[i+1:]...)
	if i < len(in.loaded) {
		in.loaded = append(in.loaded[:i], in.loaded[i+1:]...)
	}
	shift := func(k *int) {
		if *k > i {
			*k--
		}
	}
	dropRR := func(rr []int) []int {
		out := rr[:0]
		for _, r := range rr {
			if r != i {
				shift(&r)
				out = append(out, r)
			}
		}
		return out
	}
	fix := func(zs []Zone) {
		for zi := range zs {
			z := &zs[zi]
			shift(&z.Sample)
			z.RoundRobin = dropRR(z.RoundRobin)
			for li := range z.Layers {
				shift(&z.Layers[li].Sample)
				z.Layers[li].RoundRobin = dropRR(z.Layers[li].RoundRobin)
			}
		}
	}
	fix(in.Zones)
	for ai := range in.Articulations {
		fix(in.Articulations[ai].Zones)
	}
}

// ArticulationIndex finds an articulation by name or trigger word, without
// case, as NoteOptions.Articulation counts them (1-based); 0 when none
// matches. "sustain", "arco", "ord." and "normal" with no articulation of
// their own name the default.
func (in *Instrument) ArticulationIndex(word string) int {
	w := strings.ToLower(strings.TrimSpace(word))
	if w == "" {
		return 0
	}
	for i, a := range in.Articulations {
		if strings.ToLower(a.Name) == w {
			return i + 1
		}
	}
	for i, a := range in.Articulations {
		for _, t := range a.Triggers {
			if strings.ToLower(t) == w {
				return i + 1
			}
		}
	}
	return 0
}

// articulation is the 1-based articulation n, or nil for the default.
func (in *Instrument) articulation(n int) *Articulation {
	if n <= 0 || n > len(in.Articulations) {
		return nil
	}
	return &in.Articulations[n-1]
}

// voicePick is one sample a note-on sounds and how loud.
type voicePick struct {
	smp   *Sample
	base  uint8
	level float32
}

// SampleFor picks the sample and its base note for a note: the default
// articulation's zone at full velocity, on its first round-robin turn.
func (in *Instrument) SampleFor(note uint8) (*Sample, uint8) {
	var picks [2]voicePick
	if in.pick(note, 1, 0, nil, &picks) == 0 {
		return nil, 0
	}
	return picks[0].smp, picks[0].base
}

// pick fills dst with what a note plays — one sample, or two across a
// velocity crossfade — and returns how many. e advances the round robin;
// nil takes every layer's first turn.
func (in *Instrument) pick(note uint8, vel float32, art int, e *Engine, dst *[2]voicePick) int {
	zones := in.Zones
	if a := in.articulation(art); a != nil && len(a.Zones) > 0 {
		zones = a.Zones
	}
	zi := zoneFor(zones, note)
	if zi < 0 && len(zones) > 0 && &zones[0] != pointerOrNil(in.Zones) {
		// Past the articulation's own zones: the instrument's.
		zones, art = in.Zones, 0
		zi = zoneFor(zones, note)
	}
	if zi < 0 {
		// No zone: the first sample plays everything, as it always has.
		s := in.Loaded(0)
		if s == nil || s.Frames() == 0 {
			return 0
		}
		dst[0] = voicePick{smp: s, base: s.BaseNote, level: 1}
		return 1
	}
	z := &zones[zi]
	key := int32(art)<<16 | int32(zi)<<8
	if len(z.Layers) == 0 {
		return in.pickSample(z, z.Sample, z.RoundRobin, 1, key|0xFF, e, dst, 0)
	}
	// The layers this velocity falls in: the highest-starting two, which
	// crossfade over their overlap; none, and the nearest layer plays.
	v := clamp32(vel, 0, 1) * 127
	lo, hi := -1, -1
	for i := range z.Layers {
		l := &z.Layers[i]
		if v < float32(l.VelLow) || v > float32(l.VelHigh) {
			continue
		}
		switch {
		case hi < 0 || l.VelLow > z.Layers[hi].VelLow:
			lo, hi = hi, i
		case lo < 0 || l.VelLow > z.Layers[lo].VelLow:
			lo = i
		}
	}
	if hi < 0 {
		best, dist := 0, float32(math.MaxFloat32)
		for i := range z.Layers {
			l := &z.Layers[i]
			d := max(float32(l.VelLow)-v, v-float32(l.VelHigh))
			if d < dist {
				best, dist = i, d
			}
		}
		hi = best
	}
	wHi, wLo := float32(1), float32(0)
	if lo >= 0 {
		a, b := &z.Layers[lo], &z.Layers[hi]
		if w := float32(a.VelHigh) - float32(b.VelLow); w > 0 {
			t := float64(clamp32((v-float32(b.VelLow))/w, 0, 1))
			wHi, wLo = float32(math.Sin(t*math.Pi/2)), float32(math.Cos(t*math.Pi/2))
		}
	}
	l := &z.Layers[hi]
	n := in.pickSample(z, l.Sample, l.RoundRobin, wHi*dbGain(l.Gain), key|int32(hi), e, dst, 0)
	if lo >= 0 && wLo > 0.001 {
		l := &z.Layers[lo]
		n = in.pickSample(z, l.Sample, l.RoundRobin, wLo*dbGain(l.Gain), key|int32(lo), e, dst, n)
	}
	return n
}

func zoneFor(zones []Zone, note uint8) int {
	for i := range zones {
		if note >= zones[i].Low && note <= zones[i].High {
			return i
		}
	}
	return -1
}

func pointerOrNil(zs []Zone) *Zone {
	if len(zs) == 0 {
		return nil
	}
	return &zs[0]
}

// pickSample adds sample (or its round-robin turn) to dst at slot n.
func (in *Instrument) pickSample(z *Zone, sample int, rr []int, level float32, key int32, e *Engine, dst *[2]voicePick, n int) int {
	if len(rr) > 0 && e != nil {
		if t := e.rrNext(in, key, len(rr)+1); t > 0 {
			sample = rr[t-1]
		}
	}
	s := in.Loaded(sample)
	if s == nil || s.Frames() == 0 {
		return n
	}
	base := z.BaseNote
	if base == 0 {
		base = s.BaseNote
	}
	dst[n] = voicePick{smp: s, base: base, level: level}
	return n + 1
}

// dbGain is a dB trim as a gain; 0 dB is exactly 1.
func dbGain(db float32) float32 {
	if db == 0 {
		return 1
	}
	return float32(math.Pow(10, float64(db)/20))
}
