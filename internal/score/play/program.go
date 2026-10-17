package play

// program.go — a score made ready to play.
//
// Everything that allocates or touches the disk happens here, on the game
// thread: the score is interpreted, every line's instrument is loaded from
// the library, each note's articulation is looked up in its instrument,
// and the score's buses become the engine's. The mixer only ever swaps one
// finished Program for another.

import (
	"fmt"
	"math"

	"foundry/internal/score"
	"foundry/internal/score/interpret"
	"foundry/internal/tracker"
)

// Program is a score with its performance and instruments resolved.
type Program struct {
	Score *score.Score
	Perf  *interpret.Performance

	insts   []*tracker.Instrument // per line; nil plays nothing
	release []float32             // per line: the instrument's Playback.Release
	// chordInsts, chordRelease and chordVol are per line what its chord
	// symbols play on (score.Line.ChordPlay): its own instrument, a
	// release, a volume; nil insts where the line's own plays them.
	chordInsts   []*tracker.Instrument
	chordRelease []float32
	chordVol     []float32

	arts []int // per note: 1-based articulation, 0 the default
	// follow is per note what the scheduler moves while it sounds:
	// followLevel its level along the line's dynamics (a held note under a
	// hairpin, an accent falling back), followBend its pitch (a slide).
	follow []uint8

	dynCurve []int // per line: index into Perf.Dynamics, -1 none

	// The mixer as plans (mixer.go): each line's inserts, the buses'
	// chains and returns, the master chain; each line's lanes, and the
	// buses' and the master's (mixA, mixB, mixMaster).
	inserts            [][]fxSlot
	busA, busB, master []fxSlot
	buses              [2]busMix
	lanes              [][]laneAuto
	mixLanes           [3][]laneAuto
}

// chordLevel is how loud a line's chord symbols play at ChordPlay.Volume
// 1, against one of its notes: a voiced chord is five or six notes, so this
// sets the whole chord a little under the line rather than over it.
const chordLevel = 0.4

// LoadInstrument resolves a line's instrument. It is a variable so tests
// can play scores without the library.
var LoadInstrument = tracker.LoadInstrument

// Prepare interprets a score and resolves its instruments. A missing
// instrument is reported in errs and leaves its line silent; err is a
// score that cannot be performed at all.
func Prepare(s *score.Score) (prog *Program, errs []error, err error) {
	prog = &Program{Score: s}
	prog.insts = make([]*tracker.Instrument, len(s.Lines))
	prog.release = make([]float32, len(s.Lines))
	prog.chordInsts = make([]*tracker.Instrument, len(s.Lines))
	prog.chordRelease = make([]float32, len(s.Lines))
	prog.chordVol = make([]float32, len(s.Lines))
	for i, l := range s.Lines {
		prog.chordVol[i] = 1
		if cp := l.ChordPlay; cp != nil && cp.Style != score.ChordsSilent {
			prog.chordVol[i] = cp.Volume * chordLevel
			if cp.Instrument != "" {
				if in, err := LoadInstrument(cp.Instrument); err != nil {
					errs = append(errs, fmt.Errorf("line %s: chord symbols: %w", l.Name, err))
				} else {
					prog.chordInsts[i], prog.chordRelease[i] = in, in.Playback.Release
				}
			}
		}
		if l.Instrument == "" {
			errs = append(errs, fmt.Errorf("line %s: no instrument", l.Name))
			continue
		}
		in, err := LoadInstrument(l.Instrument)
		if err != nil {
			errs = append(errs, fmt.Errorf("line %s: %w", l.Name, err))
			continue
		}
		prog.insts[i] = in
		prog.release[i] = in.Playback.Release
	}
	if err := prog.perform(); err != nil {
		return nil, errs, err
	}
	errs = append(errs, prog.planMix()...)
	return prog, errs, nil
}

const (
	followLevel uint8 = 1 << iota
	followBend
)

// perform interprets the score for the program's instruments and works
// out what each note needs of the scheduler.
func (p *Program) perform() error {
	s := p.Score
	perf, err := interpret.Interpret(s, interpret.Options{Instruments: p.insts})
	if err != nil {
		return err
	}
	p.Perf = perf
	p.arts = make([]int, len(perf.Notes))
	for i := range perf.Notes {
		p.arts[i] = articulation(p.insts, &perf.Notes[i])
	}
	p.dynCurve = make([]int, len(s.Lines))
	for i := range s.Lines {
		p.dynCurve[i] = -1
	}
	p.planLanes()
	for k, d := range perf.Dynamics {
		p.dynCurve[d.Line] = k
	}
	p.follow = make([]uint8, len(perf.Notes))
	for i := range perf.Notes {
		p.follow[i] = p.noteFollow(&perf.Notes[i])
	}
	return nil
}

// noteFollow is what the scheduler must move while a note sounds: its
// pitch when it bends; its level when its instrument sustains and the
// note is accented or its line's dynamics move under it. A note that dies
// away by itself (a piano's) keeps the level it started at.
func (p *Program) noteFollow(n *interpret.Note) uint8 {
	var f uint8
	if len(n.Bend) > 0 {
		f |= followBend
	}
	in := p.instFor(n)
	if in == nil || !in.Sustains() {
		return f
	}
	if n.Accent > 1.001 || n.Accent < 0.999 {
		return f | followLevel
	}
	if k := p.dynCurve[n.Line]; k >= 0 && p.Perf.Dynamics[k].Effective.Varies(float64(n.Tick), float64(n.Tick+n.Ticks)) {
		f |= followLevel
	}
	return f
}

// levelAt is a following note's level at a tick: its line's effective
// dynamics, its accent falling back to 1 over interpret.AccentTicks.
func (p *Program) levelAt(i int, tick float64) float32 {
	n := &p.Perf.Notes[i]
	lv := n.Velocity
	if k := p.dynCurve[n.Line]; k >= 0 {
		acc := n.Accent
		if since := tick - float64(n.Tick); since > 0 {
			fall := float32(math.Max(0, 1-since/interpret.AccentTicks))
			acc = 1 + (acc-1)*fall
		}
		lv = p.Perf.Dynamics[k].Effective.At(tick) * acc
	}
	return VelocityGain(clamp32(lv, 0, 1)) * p.gain(i)
}

// instFor is the instrument a note plays on: its line's, or for the
// line's chord symbols the one they name.
func (p *Program) instFor(n *interpret.Note) *tracker.Instrument {
	if n.FromChord && p.chordInsts != nil && p.chordInsts[n.Line] != nil {
		return p.chordInsts[n.Line]
	}
	return p.insts[n.Line]
}

// noteInst is note i's instrument (instFor).
func (p *Program) noteInst(i int) *tracker.Instrument { return p.instFor(&p.Perf.Notes[i]) }

// releaseFor is note i's release: its instrument's Playback.Release.
func (p *Program) releaseFor(i int) float32 {
	n := &p.Perf.Notes[i]
	if n.FromChord && p.chordInsts != nil && p.chordInsts[n.Line] != nil {
		return p.chordRelease[n.Line]
	}
	return p.release[n.Line]
}

// gain is how loud note i plays against its velocity: 1, or a chord
// symbol's ChordPlay volume.
func (p *Program) gain(i int) float32 {
	if i < 0 || i >= len(p.Perf.Notes) || p.chordVol == nil {
		return 1
	}
	if n := &p.Perf.Notes[i]; n.FromChord {
		return p.chordVol[n.Line]
	}
	return 1
}

func articulation(insts []*tracker.Instrument, n *interpret.Note) int {
	in := insts[n.Line]
	if in == nil || n.FromChord {
		return 0
	}
	if a := in.ArticulationIndex(n.Articulation); a > 0 {
		return a
	}
	return in.ArticulationIndex(n.Technique)
}

// WithInstrument is the program with one line playing another library
// instrument: a copy sharing the performance.
func (p *Program) WithInstrument(line int, name string) (*Program, error) {
	if line < 0 || line >= len(p.insts) {
		return nil, fmt.Errorf("no line %d", line+1)
	}
	in, err := LoadInstrument(name)
	if err != nil {
		return nil, err
	}
	c := *p
	c.insts = append([]*tracker.Instrument(nil), p.insts...)
	c.release = append([]float32(nil), p.release...)
	c.insts[line] = in
	c.release[line] = in.Playback.Release
	if line < len(p.Perf.InstrumentDependent) && p.Perf.InstrumentDependent[line] {
		// The line's notes depend on what plays them (a staccato's length,
		// a glissando's style): perform the score again for the new one.
		if err := c.perform(); err != nil {
			return nil, err
		}
		return &c, nil
	}
	// Otherwise the performance is shared; only the line's articulations
	// and what its notes follow are the new instrument's.
	c.arts = append([]int(nil), p.arts...)
	c.follow = append([]uint8(nil), p.follow...)
	for i := range c.Perf.Notes {
		if c.Perf.Notes[i].Line == line {
			c.arts[i] = articulation(c.insts, &c.Perf.Notes[i])
			c.follow[i] = c.noteFollow(&c.Perf.Notes[i])
		}
	}
	return &c, nil
}

// Instrument is the instrument a line plays, nil when it did not load.
func (p *Program) Instrument(line int) *tracker.Instrument {
	if line < 0 || line >= len(p.insts) {
		return nil
	}
	return p.insts[line]
}
