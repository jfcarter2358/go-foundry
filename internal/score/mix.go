package score

// mix.go — the DAW layer's model: effect chains, automation lane targets,
// swing and humanise.
//
// Effects are named by internal/tracker/fx's kinds and parameters ("eq",
// "low_freq"); a chain is the effects in order with a bypass each. A lane
// is named for what it moves: "volume" (times the fader), "pan", "cutoff"
// (the line's low-pass, Hz), "send_a" and "send_b", or an insert's
// parameter, "<fx>.<param>" for the line's first insert of that kind and
// "<fx>#<n>.<param>" for its n-th ("eq.low", "delay#2.feedback"), so a
// lane follows its effect when the chain is reordered. Points carry the
// shape of the segment leaving them: a straight line, a step (hold, then
// jump at the next point) or a curve.
//
// The send buses and the master have lanes too (Bus.Lanes,
// Score.MasterLanes): a bus's "return" level, its "pan" and its effects'
// settings; the master's "volume" (times Gain) and its effects'. A lane's
// owner (LaneOwner) is a line's index, or OwnerBusA, OwnerBusB or
// OwnerMaster, so editing and playing them is one path for all.

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"foundry/internal/tracker/fx"
)

// Swing delays every second eighth (or sixteenth) note: Amount is how much
// of each pair the first note takes, 0.5 straight, 2/3 a triplet feel, up
// to 0.75. Zero is no swing set.
type Swing struct {
	Amount float32
	// Unit is 8 (eighths) or 16 (sixteenths); 0 reads as 8.
	Unit int
}

// On reports whether the swing moves anything.
func (w Swing) On() bool { return w.Amount > 0.5001 }

// OrStraight is the swing with its zero values read: no amount is
// straight (0.5), no unit eighths.
func (w Swing) OrStraight() Swing {
	if w.Amount == 0 {
		w.Amount = 0.5
	}
	if w.Unit == 0 {
		w.Unit = 8
	}
	return w
}

// UnitLength is the swung note's length in whole notes.
func (w Swing) UnitLength() Frac {
	if w.Unit == 16 {
		return F(1, 16)
	}
	return F(1, 8)
}

// Humanize nudges each note's timing and velocity by a little, the same
// way every time it plays (Seed): Timing is the most a note moves, in
// milliseconds either way; Velocity the most its velocity moves, as a
// fraction of itself.
type Humanize struct {
	Timing   float32
	Velocity float32
	Seed     int64
}

// On reports whether the humanise moves anything.
func (h Humanize) On() bool { return h.Timing > 0 || h.Velocity > 0 }

// SwingFor is the swing a line plays with: its own, else the score's.
func (s *Score) SwingFor(l *Line) Swing {
	if l != nil && l.Swing != nil {
		return *l.Swing
	}
	return s.Playback.Swing
}

// HumanizeFor is the humanise a line plays with: its own (with the score's
// seed when it gives none), else the score's.
func (s *Score) HumanizeFor(l *Line) Humanize {
	if l != nil && l.Humanize != nil {
		h := *l.Humanize
		if h.Seed == 0 {
			h.Seed = s.Playback.Humanize.Seed
		}
		return h
	}
	return s.Playback.Humanize
}

// LaneShape is how a lane moves from a point to the next one.
type LaneShape uint8

const (
	// LaneLinear is a straight line.
	LaneLinear LaneShape = iota
	// LaneStep holds the point's value and jumps at the next point.
	LaneStep
	// LaneCurve bends the line by the point's Curve: above 0 it starts
	// slowly and finishes fast, below 0 the other way.
	LaneCurve
)

// LaneKind is what a lane moves.
type LaneKind uint8

const (
	LaneVolume LaneKind = iota
	LanePan
	LaneCutoff
	LaneSendA
	LaneSendB
	LaneFX
	// LaneReturn is a send bus's return level.
	LaneReturn
)

// LaneScope is what kind of thing owns a lane, which sets the lanes it
// may have.
type LaneScope uint8

const (
	// ScopeLine: volume, pan, cutoff, send_a, send_b and the inserts'.
	ScopeLine LaneScope = iota
	// ScopeBus: return, pan and the bus's effects'.
	ScopeBus
	// ScopeMaster: volume and the master chain's effects'.
	ScopeMaster
)

// The owners of lanes that are not a line's (a line's is its index).
const (
	OwnerBusA   = -1
	OwnerBusB   = -2
	OwnerMaster = -3
)

// OwnerScope is the scope of a lane owner.
func OwnerScope(owner int) LaneScope {
	switch owner {
	case OwnerBusA, OwnerBusB:
		return ScopeBus
	case OwnerMaster:
		return ScopeMaster
	}
	return ScopeLine
}

// HasOwner reports whether the score has a lane owner: a line, a bus, the
// master.
func (s *Score) HasOwner(owner int) bool {
	return owner >= 0 && owner < len(s.Lines) || owner == OwnerBusA || owner == OwnerBusB || owner == OwnerMaster
}

// OwnerName is a lane owner as the editor names it: the line's name, "Bus
// A", "Bus B", "Master".
func (s *Score) OwnerName(owner int) string {
	switch owner {
	case OwnerBusA:
		return "Bus A"
	case OwnerBusB:
		return "Bus B"
	case OwnerMaster:
		return "Master"
	}
	if owner >= 0 && owner < len(s.Lines) {
		return s.Lines[owner].Name
	}
	return ""
}

// LanesOf is the lanes an owner holds, nil for no such owner.
func (s *Score) LanesOf(owner int) *[]Lane {
	switch owner {
	case OwnerBusA:
		return &s.Buses.A.Lanes
	case OwnerBusB:
		return &s.Buses.B.Lanes
	case OwnerMaster:
		return &s.MasterLanes
	}
	if owner >= 0 && owner < len(s.Lines) {
		return &s.Lines[owner].Lanes
	}
	return nil
}

// ChainOf is the effect chain an owner's effect lanes move: a line's
// inserts, a bus's effects, the master's.
func (s *Score) ChainOf(owner int) *[]FX {
	switch owner {
	case OwnerBusA:
		return &s.Buses.A.FX
	case OwnerBusB:
		return &s.Buses.B.FX
	case OwnerMaster:
		return &s.Master
	}
	if owner >= 0 && owner < len(s.Lines) {
		return &s.Lines[owner].Inserts
	}
	return nil
}

// LaneOf finds an owner's lane by name.
func (s *Score) LaneOf(owner int, name string) *Lane {
	if ls := s.LanesOf(owner); ls != nil {
		return laneIn(*ls, name)
	}
	return nil
}

// Placement is where an owner's chain sits, for its effects' defaults.
func ownerPlacement(owner int) fx.Placement {
	if OwnerScope(owner) == ScopeBus {
		return fx.Bus
	}
	return fx.Insert
}

// LaneTarget is a lane's name, read.
type LaneTarget struct {
	Kind LaneKind
	// For LaneFX: the effect kind, which of the line's inserts of that kind
	// (1-based), and the parameter.
	FX    fx.Kind
	Nth   int
	Param int
}

// laneNames are the lanes that are not an effect's parameter.
var laneNames = map[string]LaneKind{"volume": LaneVolume, "pan": LanePan, "cutoff": LaneCutoff, "send_a": LaneSendA, "send_b": LaneSendB, "return": LaneReturn}

// scopeLanes are the lanes each scope may have besides its effects'.
var scopeLanes = [...][]LaneKind{
	ScopeLine:   {LaneVolume, LanePan, LaneCutoff, LaneSendA, LaneSendB},
	ScopeBus:    {LaneReturn, LanePan},
	ScopeMaster: {LaneVolume},
}

// scopeWant is the error's list of a scope's lane names.
var scopeWant = [...]string{
	ScopeLine:   "volume, pan, cutoff, send_a, send_b or <effect>.<parameter>",
	ScopeBus:    "return, pan or <effect>.<parameter>",
	ScopeMaster: "volume or <effect>.<parameter>",
}

// ScopeLaneKinds are the lanes a scope may have besides its effects', in
// the order the editor offers them.
func ScopeLaneKinds(sc LaneScope) []LaneKind { return scopeLanes[sc] }

// ParseLaneTarget reads a line's lane name.
func ParseLaneTarget(name string) (LaneTarget, error) { return ParseLaneTargetIn(ScopeLine, name) }

// ParseLaneTargetIn reads a lane name of a scope.
func ParseLaneTargetIn(sc LaneScope, name string) (LaneTarget, error) {
	if k, ok := laneNames[name]; ok {
		for _, kk := range scopeLanes[sc] {
			if kk == k {
				return LaneTarget{Kind: k}, nil
			}
		}
		return LaneTarget{}, fmt.Errorf("lane %q: want %s", name, scopeWant[sc])
	}
	kind, param, ok := strings.Cut(name, ".")
	if !ok {
		return LaneTarget{}, fmt.Errorf("lane %q: want %s", name, scopeWant[sc])
	}
	nth := 1
	if k, n, ok := strings.Cut(kind, "#"); ok {
		v, err := strconv.Atoi(n)
		if err != nil || v < 1 {
			return LaneTarget{}, fmt.Errorf("lane %q: %q is not an insert number", name, n)
		}
		kind, nth = k, v
	}
	k, ok := fx.Lookup(kind)
	if !ok {
		return LaneTarget{}, fmt.Errorf("lane %q: no effect %q", name, kind)
	}
	pi := k.Spec().Param(param)
	if pi < 0 {
		return LaneTarget{}, fmt.Errorf("lane %q: %s has no parameter %q", name, kind, param)
	}
	return LaneTarget{Kind: LaneFX, FX: k, Nth: nth, Param: pi}, nil
}

// Name writes a lane target as a lane name.
func (t LaneTarget) Name() string {
	for n, k := range laneNames {
		if k == t.Kind && t.Kind != LaneFX {
			return n
		}
	}
	sp := t.FX.Spec()
	kind := sp.Name
	if t.Nth > 1 {
		kind += "#" + strconv.Itoa(t.Nth)
	}
	return kind + "." + sp.Params[t.Param].Name
}

// Range is the values a lane of this target takes.
func (t LaneTarget) Range() (lo, hi float32) {
	switch t.Kind {
	case LaneVolume, LaneReturn:
		return 0, 4
	case LanePan:
		return -1, 1
	case LaneCutoff:
		return 16, 20000
	case LaneSendA, LaneSendB:
		return 0, 1
	}
	p := t.FX.Spec().Params[t.Param]
	return p.Min, p.Max
}

// InsertSlot is the index in a line's inserts of the effect a lane target
// names, -1 when the line has no such insert.
func (l *Line) InsertSlot(t LaneTarget) int { return ChainSlot(l.Inserts, t) }

// ChainSlot is the index in a chain of the effect a lane target names,
// -1 when the chain has no such effect.
func ChainSlot(chain []FX, t LaneTarget) int {
	if t.Kind != LaneFX {
		return -1
	}
	n := 0
	for i, f := range chain {
		if k, ok := fx.Lookup(f.Kind); ok && k == t.FX {
			n++
			if n == t.Nth {
				return i
			}
		}
	}
	return -1
}

// LaneTargetForSlot is the lane target for parameter param of the line's
// insert at slot (its kind and its number among inserts of the kind).
func (l *Line) LaneTargetForSlot(slot, param int) (LaneTarget, bool) {
	return ChainTargetForSlot(l.Inserts, slot, param)
}

// ChainTargetForSlot is the lane target for parameter param of a chain's
// effect at slot.
func ChainTargetForSlot(chain []FX, slot, param int) (LaneTarget, bool) {
	if slot < 0 || slot >= len(chain) {
		return LaneTarget{}, false
	}
	k, ok := fx.Lookup(chain[slot].Kind)
	if !ok || param < 0 || param >= len(k.Spec().Params) {
		return LaneTarget{}, false
	}
	nth := 0
	for i := 0; i <= slot; i++ {
		if kk, ok := fx.Lookup(chain[i].Kind); ok && kk == k {
			nth++
		}
	}
	return LaneTarget{Kind: LaneFX, FX: k, Nth: nth, Param: param}, true
}

// ShapeValue is where a segment from a to b is at t (0..1 of the way)
// for a's shape and curve.
func ShapeValue(shape LaneShape, curve, a, b float32, t float64) float32 {
	switch shape {
	case LaneStep:
		if t >= 1 {
			return b
		}
		return a
	case LaneCurve:
		if curve != 0 {
			t = math.Pow(t, math.Exp2(float64(clampF(curve, -1, 1))*2))
		}
	}
	return a + (b-a)*float32(t)
}

func clampF(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ---------- effects ----------

// NewFX is an effect of a kind with every parameter at its default for
// the placement, written out (so the file says what plays).
func NewFX(kind fx.Kind, place fx.Placement) FX {
	sp := kind.Spec()
	f := FX{Kind: sp.Name}
	for i, p := range sp.Params {
		f.Params = append(f.Params, FXParam{Name: p.Name, Value: Float32Value(sp.Default(i, place))})
	}
	return f
}

// Float32Value is a float32 as the float64 its shortest decimal reads as
// (0.3, not 0.30000001192092896), for a file.
func Float32Value(v float32) float64 {
	x, _ := strconv.ParseFloat(strconv.FormatFloat(float64(v), 'g', -1, 32), 64)
	return x
}

// SetParam sets (or adds) a setting.
func (f *FX) SetParam(name string, v float64) {
	for i := range f.Params {
		if f.Params[i].Name == name {
			f.Params[i].Value = v
			return
		}
	}
	f.Params = append(f.Params, FXParam{Name: name, Value: v})
}

// validateChain reports effects this build does not have, settings they
// do not take and values outside their range.
func validateChain(where string, chain []FX, add func(string, ...any)) {
	for i, f := range chain {
		k, ok := fx.Lookup(f.Kind)
		if !ok {
			add("%s %d: no effect %q (this build has %s)", where, i+1, f.Kind, strings.Join(FXNames(), ", "))
			continue
		}
		sp := k.Spec()
		for _, p := range f.Params {
			pi := sp.Param(p.Name)
			if pi < 0 {
				add("%s %d (%s): no setting %q", where, i+1, f.Kind, p.Name)
				continue
			}
			ps := sp.Params[pi]
			if p.Value < float64(ps.Min) || p.Value > float64(ps.Max) {
				add("%s %d (%s): %s %g is outside %g..%g", where, i+1, f.Kind, p.Name, p.Value, ps.Min, ps.Max)
			}
		}
	}
}

// FXNames are the effect kinds' names.
func FXNames() []string {
	var out []string
	for i := range fx.Specs {
		out = append(out, fx.Specs[i].Name)
	}
	return out
}

// ---------- lanes as the editor shows them ----------

// Label is a lane target in words: "Volume", "Pan", "Cutoff", "Send A",
// "EQ 2 · Low".
func (t LaneTarget) Label() string {
	switch t.Kind {
	case LaneVolume:
		return "Volume"
	case LanePan:
		return "Pan"
	case LaneCutoff:
		return "Cutoff"
	case LaneSendA:
		return "Send A"
	case LaneSendB:
		return "Send B"
	case LaneReturn:
		return "Return"
	}
	sp := t.FX.Spec()
	kind := sp.Label
	if t.Nth > 1 {
		kind += " " + strconv.Itoa(t.Nth)
	}
	return kind + " · " + sp.Params[t.Param].Label
}

// Log reports a target shown and drawn on a log scale (a frequency, a
// time).
func (t LaneTarget) Log() bool {
	switch t.Kind {
	case LaneCutoff:
		return true
	case LaneFX:
		p := t.FX.Spec().Params[t.Param]
		return p.Log && p.Min > 0
	}
	return false
}

// Stepped reports a target that only takes whole values (a switch, a
// count): its lane holds each value to the next point.
func (t LaneTarget) Stepped() bool {
	if t.Kind != LaneFX {
		return false
	}
	p := t.FX.Spec().Params[t.Param]
	return p.Int || len(p.Choices) > 0
}

// Format writes a lane value as the editor shows it.
func (t LaneTarget) Format(v float32) string {
	switch t.Kind {
	case LaneVolume, LaneReturn:
		if v <= 0.0001 {
			return "-inf dB"
		}
		return fmt.Sprintf("%+.1f dB", 20*math.Log10(float64(v)))
	case LanePan:
		switch {
		case math.Abs(float64(v)) < 0.005:
			return "C"
		case v < 0:
			return fmt.Sprintf("L%.0f", -v*100)
		}
		return fmt.Sprintf("R%.0f", v*100)
	case LaneCutoff:
		if v >= 1000 {
			return fmt.Sprintf("%.2f kHz", v/1000)
		}
		return fmt.Sprintf("%.0f Hz", v)
	case LaneSendA, LaneSendB:
		return fmt.Sprintf("%.0f%%", v*100)
	}
	p := t.FX.Spec().Params[t.Param]
	return p.Format(v)
}

// Clamp keeps a lane value in the target's range (whole for a stepped
// target).
func (t LaneTarget) Clamp(v float32) float32 {
	lo, hi := t.Range()
	v = clampF(v, lo, hi)
	if t.Stepped() {
		v = float32(math.Round(float64(v)))
	}
	return v
}

// LaneDefault is where a new lane of a target starts on a line: what the
// line plays without it (the fader's unity for volume, the line's pan and
// sends, the effect's own setting).
func (l *Line) LaneDefault(t LaneTarget) float32 {
	switch t.Kind {
	case LaneVolume:
		return 1
	case LanePan:
		return l.Pan
	case LaneCutoff:
		return 20000
	case LaneSendA:
		return l.SendA
	case LaneSendB:
		return l.SendB
	}
	return chainDefault(l.Inserts, t, fx.Insert)
}

// chainDefault is an effect lane's starting value: the chain's effect's
// own setting, else its default.
func chainDefault(chain []FX, t LaneTarget, place fx.Placement) float32 {
	sp := t.FX.Spec()
	if slot := ChainSlot(chain, t); slot >= 0 {
		return float32(chain[slot].Param(sp.Params[t.Param].Name, float64(sp.Default(t.Param, place))))
	}
	return sp.Default(t.Param, place)
}

// LaneDefaultOf is where a new lane starts on any owner: what the owner
// plays without it (a bus's return and pan, the master's unity, an
// effect's own setting).
func (s *Score) LaneDefaultOf(owner int, t LaneTarget) float32 {
	if owner >= 0 {
		if owner < len(s.Lines) {
			return s.Lines[owner].LaneDefault(t)
		}
		return 0
	}
	var b *Bus
	switch owner {
	case OwnerBusA:
		b = &s.Buses.A
	case OwnerBusB:
		b = &s.Buses.B
	}
	switch t.Kind {
	case LaneVolume:
		return 1
	case LaneReturn:
		if b != nil {
			return b.Return
		}
	case LanePan:
		if b != nil {
			return b.Pan
		}
	case LaneFX:
		if ch := s.ChainOf(owner); ch != nil {
			return chainDefault(*ch, t, ownerPlacement(owner))
		}
	}
	return 0
}

// LaneValue is a lane's value at x (whole notes from the top, written
// order), as the interpreter plays it on the first pass: held before the
// first point and after the last, a point's hold kept to its To, then
// each segment as its shape says.
func (s *Score) LaneValue(ln *Lane, x Frac, starts []Frac) float32 {
	if len(ln.Points) == 0 {
		return 0
	}
	pts := ln.Points
	at := func(i int) Frac { return s.PosFrac(pts[i].At, starts) }
	from := func(i int) Frac {
		if pts[i].HasTo {
			return s.PosFrac(pts[i].To, starts)
		}
		return at(i)
	}
	if x.Less(at(0)) {
		return pts[0].Value
	}
	for i := range pts {
		if i+1 == len(pts) || x.Less(at(i+1)) {
			f := from(i)
			if i+1 == len(pts) || x.Less(f) {
				return pts[i].Value
			}
			to := at(i + 1)
			if !f.Less(to) {
				return pts[i+1].Value
			}
			t := x.Sub(f).Div(to.Sub(f)).Float()
			return ShapeValue(pts[i].Shape, pts[i].Curve, pts[i].Value, pts[i+1].Value, t)
		}
	}
	return pts[len(pts)-1].Value
}
