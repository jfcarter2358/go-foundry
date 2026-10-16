// Package fx is the audio effects a mixer is built from: a parametric EQ,
// chorus, phaser, drive, bit crusher, tempo-synced delay, reverb,
// compressor, limiter and a low/high-pass filter, each a stereo Effect with
// named parameters, and Chains of them — a Score line's inserts, a send
// bus, the master.
//
// Everything here runs on the mixer goroutine, so the rules are the
// Tracker's: an effect is sized when it is made (New allocates its delay
// lines) and Process never allocates; feedback state is flushed before it
// can go subnormal. Parameters are smoothed: SetParam glides to a new value
// over the frames it is given — a knob turned in the editor over a few
// milliseconds, an automation lane over exactly the stretch until its next
// point — so nothing zips or clicks, and a value that is not moving costs
// nothing. Frequencies and times glide on a log scale.
//
// The Tracker shares the kernels underneath (kernels.go): its channel
// filter, reverb, delay, compressor, drive and crush are these, unchanged.
package fx

import (
	"fmt"
	"math"
)

// Kind is an effect type.
type Kind uint8

const (
	KindEQ Kind = iota
	KindChorus
	KindPhaser
	KindDrive
	KindCrusher
	KindDelay
	KindReverb
	KindCompressor
	KindLimiter
	KindFilter
	numKinds
)

// MaxParams is the most parameters one effect has.
const MaxParams = 20

// ParamSpec describes one parameter.
type ParamSpec struct {
	// Name is the parameter as a file writes it ("cutoff"); Label as the
	// editor shows it ("Cutoff").
	Name, Label string
	// Unit is "Hz", "dB", "ms", "s", "beats", "%" (a 0..1 value shown as a
	// percentage) or "".
	Unit              string
	Min, Max, Default float32
	// Log glides and is shown on a log scale (frequencies, times, ratios).
	Log bool
	// Int takes whole numbers (bits, stages).
	Int bool
	// Choices name the values 0, 1, … of a switch (a filter's mode). A
	// switch does not glide.
	Choices []string
}

// Spec describes an effect type.
type Spec struct {
	Kind Kind
	// Name is the type as a file writes it ("eq"); Label as the editor
	// shows it ("EQ").
	Name, Label string
	Desc        string
	Params      []ParamSpec
	// BusMix is the "mix" an effect takes on a send bus when the file does
	// not say: a bus returns only what its effects make, so a reverb there
	// is all wet. Params' own "mix" default is the insert's.
	BusMix float32
}

// Placement is where a chain sits, which sets what "mix" defaults to.
type Placement uint8

const (
	// Insert is a line's own chain (or the master's): a wet/dry blend.
	Insert Placement = iota
	// Bus is a send bus's chain: wet only by default.
	Bus
)

func hz(name, label string, lo, hi, def float32) ParamSpec {
	return ParamSpec{Name: name, Label: label, Unit: "Hz", Min: lo, Max: hi, Default: def, Log: true}
}

func db(name, label string, lo, hi, def float32) ParamSpec {
	return ParamSpec{Name: name, Label: label, Unit: "dB", Min: lo, Max: hi, Default: def}
}

func amount(name, label string, def float32) ParamSpec {
	return ParamSpec{Name: name, Label: label, Unit: "%", Min: 0, Max: 1, Default: def}
}

func mixParam(def float32) ParamSpec { return amount("mix", "Mix", def) }

// Specs is every effect type, in the order the editor lists them.
var Specs = [numKinds]Spec{
	KindEQ: {Kind: KindEQ, Name: "eq", Label: "EQ", Desc: "Parametric EQ: a low and a high shelf and four bell bands. A band at 0 dB is out of the signal.",
		Params: []ParamSpec{
			db("low", "Low shelf", -24, 24, 0), hz("low_freq", "Low freq", 20, 1000, 120),
			db("high", "High shelf", -24, 24, 0), hz("high_freq", "High freq", 1000, 20000, 8000),
			db("b1", "Band 1", -24, 24, 0), hz("b1_freq", "Band 1 freq", 20, 20000, 250), {Name: "b1_q", Label: "Band 1 Q", Min: 0.1, Max: 18, Default: 1, Log: true},
			db("b2", "Band 2", -24, 24, 0), hz("b2_freq", "Band 2 freq", 20, 20000, 1000), {Name: "b2_q", Label: "Band 2 Q", Min: 0.1, Max: 18, Default: 1, Log: true},
			db("b3", "Band 3", -24, 24, 0), hz("b3_freq", "Band 3 freq", 20, 20000, 3000), {Name: "b3_q", Label: "Band 3 Q", Min: 0.1, Max: 18, Default: 1, Log: true},
			db("b4", "Band 4", -24, 24, 0), hz("b4_freq", "Band 4 freq", 20, 20000, 8000), {Name: "b4_q", Label: "Band 4 Q", Min: 0.1, Max: 18, Default: 1, Log: true},
			db("output", "Output", -24, 24, 0),
		}},
	KindChorus: {Kind: KindChorus, Name: "chorus", Label: "Chorus", Desc: "Two modulated delay lines a side: a wider, shimmering copy of the line.", BusMix: 1,
		Params: []ParamSpec{
			{Name: "rate", Label: "Rate", Unit: "Hz", Min: 0.05, Max: 8, Default: 0.8, Log: true},
			{Name: "depth", Label: "Depth", Unit: "ms", Min: 0, Max: 10, Default: 2.5},
			{Name: "delay", Label: "Delay", Unit: "ms", Min: 1, Max: 30, Default: 12, Log: true},
			{Name: "feedback", Label: "Feedback", Unit: "%", Min: -0.9, Max: 0.9, Default: 0},
			amount("spread", "Spread", 1),
			mixParam(0.5),
		}},
	KindPhaser: {Kind: KindPhaser, Name: "phaser", Label: "Phaser", Desc: "A chain of allpass stages swept by an LFO: moving notches.", BusMix: 1,
		Params: []ParamSpec{
			{Name: "rate", Label: "Rate", Unit: "Hz", Min: 0.05, Max: 8, Default: 0.5, Log: true},
			amount("depth", "Depth", 0.7),
			hz("freq", "Centre", 100, 8000, 800),
			{Name: "feedback", Label: "Feedback", Unit: "%", Min: -0.95, Max: 0.95, Default: 0.5},
			{Name: "stages", Label: "Stages", Min: 2, Max: 12, Default: 6, Int: true},
			amount("spread", "Spread", 0.5),
			mixParam(0.5),
		}},
	KindDrive: {Kind: KindDrive, Name: "drive", Label: "Drive", Desc: "Soft saturation at unity gain for quiet signals: only the peaks are squashed.", BusMix: 1,
		Params: []ParamSpec{
			amount("drive", "Drive", 0.3),
			amount("tone", "Tone", 1),
			mixParam(1),
			db("output", "Output", -24, 24, 0),
		}},
	KindCrusher: {Kind: KindCrusher, Name: "crusher", Label: "Bit crusher", Desc: "Fewer bits and a lower sample rate: digital grit.", BusMix: 1,
		Params: []ParamSpec{
			{Name: "bits", Label: "Bits", Min: 1, Max: 16, Default: 8, Int: true},
			{Name: "downsample", Label: "Downsample", Unit: "x", Min: 1, Max: 64, Default: 1, Log: true},
			mixParam(1),
		}},
	KindDelay: {Kind: KindDelay, Name: "delay", Label: "Delay", Desc: "Echoes in time with the music; the repeats darken and can cross sides.", BusMix: 1,
		Params: []ParamSpec{
			{Name: "beats", Label: "Time", Unit: "beats", Min: 0.0625, Max: 4, Default: 0.75, Log: true},
			{Name: "feedback", Label: "Feedback", Unit: "%", Min: 0, Max: 0.95, Default: 0.35}, // the echo's own ceiling
			amount("tone", "Tone", 0.6),
			{Name: "ping_pong", Label: "Ping-pong", Min: 0, Max: 1, Default: 1, Int: true, Choices: []string{"off", "on"}},
			mixParam(0.3),
		}},
	KindReverb: {Kind: KindReverb, Name: "reverb", Label: "Reverb", Desc: "A room around the sound (Freeverb).", BusMix: 1,
		Params: []ParamSpec{
			amount("size", "Size", 0.6),
			amount("damping", "Damping", 0.4),
			amount("width", "Width", 1),
			mixParam(0.25),
		}},
	KindCompressor: {Kind: KindCompressor, Name: "compressor", Label: "Compressor", Desc: "Turns peaks above the threshold down by the ratio.", BusMix: 1,
		Params: []ParamSpec{
			db("threshold", "Threshold", -60, 0, -12),
			{Name: "ratio", Label: "Ratio", Unit: ":1", Min: 1, Max: 20, Default: 3, Log: true},
			{Name: "attack", Label: "Attack", Unit: "s", Min: 0.0001, Max: 0.5, Default: 0.005, Log: true},
			{Name: "release", Label: "Release", Unit: "s", Min: 0.01, Max: 2, Default: 0.15, Log: true},
			db("makeup", "Makeup", 0, 24, 0),
			{Name: "enabled", Label: "On", Min: 0, Max: 1, Default: 1, Int: true, Choices: []string{"off", "on"}},
		}},
	KindLimiter: {Kind: KindLimiter, Name: "limiter", Label: "Limiter", Desc: "A brick wall at the ceiling, looking 1.5 ms ahead so no peak passes it.", BusMix: 1,
		Params: []ParamSpec{
			db("ceiling", "Ceiling", -24, 0, -0.3),
			db("gain", "Input gain", 0, 24, 0),
			{Name: "release", Label: "Release", Unit: "s", Min: 0.005, Max: 1, Default: 0.1, Log: true},
		}},
	KindFilter: {Kind: KindFilter, Name: "filter", Label: "Filter", Desc: "A resonant low-pass, high-pass or band-pass, 12 or 24 dB an octave.", BusMix: 1,
		Params: []ParamSpec{
			{Name: "mode", Label: "Mode", Min: 0, Max: 2, Default: 0, Int: true, Choices: []string{"lowpass", "highpass", "bandpass"}},
			hz("cutoff", "Cutoff", 20, 20000, 1000),
			amount("resonance", "Resonance", 0.2),
			{Name: "slope", Label: "Slope", Min: 0, Max: 1, Default: 0, Int: true, Choices: []string{"12 dB", "24 dB"}},
			mixParam(1),
		}},
}

// Spec is the description of a kind.
func (k Kind) Spec() *Spec {
	if k >= numKinds {
		return nil
	}
	return &Specs[k]
}

func (k Kind) String() string {
	if s := k.Spec(); s != nil {
		return s.Name
	}
	return fmt.Sprintf("fx%d", k)
}

// Lookup finds a kind by its file name.
func Lookup(name string) (Kind, bool) {
	for i := range Specs {
		if Specs[i].Name == name {
			return Kind(i), true
		}
	}
	return 0, false
}

// Param is the index of a kind's parameter by name, -1 for none.
func (s *Spec) Param(name string) int {
	for i := range s.Params {
		if s.Params[i].Name == name {
			return i
		}
	}
	return -1
}

// Default is a parameter's default where the effect is placed.
func (s *Spec) Default(i int, place Placement) float32 {
	p := &s.Params[i]
	if place == Bus && p.Name == "mix" && s.BusMix > 0 {
		return s.BusMix
	}
	return p.Default
}

// Clamp keeps a value in a parameter's range (and whole when it is Int).
func (p *ParamSpec) Clamp(v float32) float32 {
	if v != v { // NaN
		return p.Default
	}
	v = clamp32(v, p.Min, p.Max)
	if p.Int {
		v = float32(math.Round(float64(v)))
	}
	return v
}

// Format writes a value as the editor shows it: "1.2 kHz", "-3.0 dB", "35%".
func (p *ParamSpec) Format(v float32) string {
	if len(p.Choices) > 0 {
		i := int(math.Round(float64(v)))
		if i >= 0 && i < len(p.Choices) {
			return p.Choices[i]
		}
	}
	switch p.Unit {
	case "Hz":
		if v >= 1000 {
			return fmt.Sprintf("%.2f kHz", v/1000)
		}
		return fmt.Sprintf("%.0f Hz", v)
	case "dB":
		return fmt.Sprintf("%+.1f dB", v)
	case "%":
		return fmt.Sprintf("%.0f%%", v*100)
	case "ms":
		return fmt.Sprintf("%.1f ms", v)
	case "s":
		if v < 1 {
			return fmt.Sprintf("%.0f ms", v*1000)
		}
		return fmt.Sprintf("%.2f s", v)
	case "beats":
		return fmt.Sprintf("%.3g beats", v)
	case "":
		if p.Int {
			return fmt.Sprintf("%.0f", v)
		}
		return fmt.Sprintf("%.2f", v)
	}
	return fmt.Sprintf("%.3g%s", v, p.Unit)
}

// Effect is one effect instance. Process works in place on interleaved
// stereo frames; nothing it does allocates.
type Effect interface {
	Kind() Kind
	// Process runs len(buf)/2 frames through the effect in place.
	Process(buf []float32)
	// SetParam glides parameter i to v over frames (0: at once). The value
	// is clamped to the parameter's range.
	SetParam(i int, v float32, frames int)
	// Param is parameter i's value as last set (where a glide ends).
	Param(i int) float32
	// SetTempo tells a tempo-synced effect the tempo, in quarter notes a
	// minute.
	SetTempo(bpm float32)
	// Reset empties the effect's memory: delay lines, envelopes, filters.
	Reset()
}

// New makes an effect at a sample rate, every parameter at its default for
// the placement. Allocates (delay lines); call it off the mixer goroutine.
func New(k Kind, rate int, place Placement) Effect {
	if rate <= 0 {
		rate = 48000
	}
	var e Effect
	switch k {
	case KindEQ:
		e = newEQ(rate)
	case KindChorus:
		e = newChorus(rate)
	case KindPhaser:
		e = newPhaser(rate)
	case KindDrive:
		e = newDrive(rate)
	case KindCrusher:
		e = newCrusher(rate)
	case KindDelay:
		e = newDelay(rate)
	case KindReverb:
		e = newReverb(rate)
	case KindCompressor:
		e = newCompressor(rate)
	case KindLimiter:
		e = newLimiter(rate)
	case KindFilter:
		e = newFilter(rate)
	default:
		return nil
	}
	sp := k.Spec()
	for i := range sp.Params {
		e.SetParam(i, sp.Default(i, place), 0)
	}
	return e
}

// ---------- parameters ----------

// param is one smoothed parameter. A Log parameter glides in log2 space.
// now and want are in the parameter's own units, and a value that is not
// gliding is exactly what was set (no round trip through the log).
type param struct {
	cur, target, step float32 // in the glide's space
	now, want         float32 // natural units: the current value, the target
	prev              float32 // now before the last advance
	left              int
	log               bool
}

func (p *param) natural(x float32) float32 {
	if p.log {
		return float32(math.Exp2(float64(x)))
	}
	return x
}

// base holds what every effect shares: its spec, rate and parameters.
type base struct {
	kind Kind
	spec *Spec
	rate int
	p    [MaxParams]param
	// dirty is set when a parameter moves, for an effect that recomputes
	// coefficients only then.
	dirty bool
}

func newBase(k Kind, rate int) base {
	b := base{kind: k, spec: k.Spec(), rate: rate, dirty: true}
	for i := range b.spec.Params {
		ps := &b.spec.Params[i]
		b.p[i].log = ps.Log && ps.Min > 0
	}
	return b
}

func (b *base) Kind() Kind { return b.kind }

func (b *base) SetTempo(float32) {}

func (b *base) SetParam(i int, v float32, frames int) {
	if i < 0 || i >= len(b.spec.Params) {
		return
	}
	ps := &b.spec.Params[i]
	v = ps.Clamp(v)
	p := &b.p[i]
	x := v
	if p.log {
		x = float32(math.Log2(float64(v)))
	}
	if len(ps.Choices) > 0 || ps.Int {
		frames = 0 // a switch, or a count (bits, stages), steps
	}
	if frames <= 0 {
		p.cur, p.target, p.left, p.step = x, x, 0, 0
		p.now, p.want, p.prev = v, v, v
		b.dirty = true
		return
	}
	if v == p.want && p.left == 0 && p.now == v {
		return
	}
	p.target, p.want = x, v
	p.left = frames
	p.step = (x - p.cur) / float32(frames)
}

func (b *base) Param(i int) float32 {
	if i < 0 || i >= len(b.spec.Params) {
		return 0
	}
	return b.p[i].want
}

// val is parameter i's current value in its own units.
func (b *base) val(i int) float32 { return b.p[i].now }

// advance moves every gliding parameter on by n frames and reports whether
// any moved (dirty is then set too). prev keeps where each was.
func (b *base) advance(n int) bool {
	moved := false
	for i := range b.spec.Params {
		p := &b.p[i]
		p.prev = p.now
		if p.left == 0 {
			continue
		}
		if n >= p.left {
			p.cur, p.left, p.now = p.target, 0, p.want
		} else {
			p.cur += p.step * float32(n)
			p.left -= n
			p.now = p.natural(p.cur)
		}
		moved = true
	}
	if moved {
		b.dirty = true
	}
	return moved
}

// ramp is a per-sample linear move from a to b over n frames: a gain that
// glided this block reaches its new value without a step.
type ramp struct{ v, d float32 }

func newRamp(a, b float32, n int) ramp {
	if n <= 0 || a == b {
		return ramp{v: b}
	}
	return ramp{v: a, d: (b - a) / float32(n)}
}

func (r *ramp) next() float32 {
	r.v += r.d
	return r.v
}

// blend mixes the wet signal in buf with the dry copy at mix (a ramp from
// m0 to m1 over the frames); mix 1 leaves buf exactly the wet signal and
// 0 exactly the dry.
func blend(buf, dry []float32, m0, m1 float32) {
	n := len(buf) / 2
	if m0 == 1 && m1 == 1 {
		return
	}
	if m0 == 0 && m1 == 0 {
		copy(buf, dry)
		return
	}
	r := newRamp(m0, m1, n)
	for f := 0; f < n; f++ {
		m := r.next()
		buf[f*2] = dry[f*2] + (buf[f*2]-dry[f*2])*m
		buf[f*2+1] = dry[f*2+1] + (buf[f*2+1]-dry[f*2+1])*m
	}
}

// dbGain is decibels as a linear gain.
func dbGain(d float32) float32 { return float32(math.Pow(10, float64(d)/20)) }

// ChunkFrames is the most frames an effect processes between parameter
// updates: Process works through a longer buffer in chunks this long.
const ChunkFrames = 64

// chunker is an effect's per-chunk body.
type chunker interface{ chunk(buf []float32) }

// runChunks feeds buf to c at most ChunkFrames frames at a time.
func runChunks(c chunker, buf []float32) {
	for len(buf) >= 2 {
		n := min(len(buf), 2*ChunkFrames) &^ 1
		c.chunk(buf[:n])
		buf = buf[n:]
	}
}
