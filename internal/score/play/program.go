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
	arts    []int                 // per note: 1-based articulation, 0 the default

	volCurve, panCurve []int // per line: index into Perf.Curves, -1 none

	reverb tracker.ReverbSpec
	delay  tracker.DelaySpec
	comp   tracker.CompressorSpec
}

// LoadInstrument resolves a line's instrument. It is a variable so tests
// can play scores without the library.
var LoadInstrument = tracker.LoadInstrument

// Prepare interprets a score and resolves its instruments. A missing
// instrument is reported in errs and leaves its line silent; err is a
// score that cannot be performed at all.
func Prepare(s *score.Score) (prog *Program, errs []error, err error) {
	perf, err := interpret.Interpret(s, interpret.Options{})
	if err != nil {
		return nil, nil, err
	}
	prog = &Program{Score: s, Perf: perf}
	prog.insts = make([]*tracker.Instrument, len(s.Lines))
	prog.release = make([]float32, len(s.Lines))
	for i, l := range s.Lines {
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
	prog.arts = make([]int, len(perf.Notes))
	for i := range perf.Notes {
		prog.arts[i] = articulation(prog.insts, &perf.Notes[i])
	}
	prog.volCurve = make([]int, len(s.Lines))
	prog.panCurve = make([]int, len(s.Lines))
	for i := range s.Lines {
		prog.volCurve[i], prog.panCurve[i] = -1, -1
	}
	for k, c := range perf.Curves {
		switch c.Target {
		case "volume":
			prog.volCurve[c.Line] = k
		case "pan":
			prog.panCurve[c.Line] = k
		}
	}
	prog.reverb, prog.delay, prog.comp = Buses(s)
	return prog, errs, nil
}

func articulation(insts []*tracker.Instrument, n *interpret.Note) int {
	in := insts[n.Line]
	if in == nil {
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
	c.arts = append([]int(nil), p.arts...)
	c.insts[line] = in
	c.release[line] = in.Playback.Release
	for i := range c.Perf.Notes {
		if c.Perf.Notes[i].Line == line {
			c.arts[i] = articulation(c.insts, &c.Perf.Notes[i])
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

// Buses turns a score's send buses and master chain into the engine's:
// bus A's reverb, bus B's delay and the master compressor. Other effects
// are kept in the file but not played yet (the effects rack is phase 4);
// Unplayed lists them.
func Buses(s *score.Score) (tracker.ReverbSpec, tracker.DelaySpec, tracker.CompressorSpec) {
	var rev tracker.ReverbSpec
	for _, f := range s.Buses.A.FX {
		if f.Kind == "reverb" {
			rev = tracker.ReverbSpec{Size: float32(f.Param("size", 0.6)), Damping: float32(f.Param("damping", 0.4)),
				Width: float32(f.Param("width", 1)), Return: s.Buses.A.Return}
		}
	}
	var dly tracker.DelaySpec
	for _, f := range s.Buses.B.FX {
		if f.Kind == "delay" {
			dly = tracker.DelaySpec{Beats: float32(f.Param("beats", 0.75)), Feedback: float32(f.Param("feedback", 0.35)),
				Tone: float32(f.Param("tone", 0.6)), PingPong: f.Param("ping_pong", 1) != 0, Return: s.Buses.B.Return}
		}
	}
	comp := tracker.CompressorSpec{Threshold: -12, Ratio: 3, Attack: 0.005, Release: 0.15}
	for _, f := range s.Master {
		if f.Kind == "compressor" {
			comp = tracker.CompressorSpec{Enabled: f.Param("enabled", 1) != 0, Threshold: float32(f.Param("threshold", -12)),
				Ratio: float32(f.Param("ratio", 3)), Attack: float32(f.Param("attack", 0.005)), Release: float32(f.Param("release", 0.15)),
				Makeup: float32(f.Param("makeup", 0))}
		}
	}
	return rev, dly, comp
}

// Unplayed lists the effects in a score that this build stores but does
// not play: line inserts, and anything on the buses or the master chain
// beyond bus A's reverb, bus B's delay and a master compressor.
func Unplayed(s *score.Score) []string {
	var out []string
	for _, l := range s.Lines {
		for _, f := range l.Inserts {
			out = append(out, fmt.Sprintf("%s insert %s", l.Name, f.Kind))
		}
	}
	for _, f := range s.Buses.A.FX {
		if f.Kind != "reverb" {
			out = append(out, "bus a "+f.Kind)
		}
	}
	for _, f := range s.Buses.B.FX {
		if f.Kind != "delay" {
			out = append(out, "bus b "+f.Kind)
		}
	}
	for _, f := range s.Master {
		if f.Kind != "compressor" {
			out = append(out, "master "+f.Kind)
		}
	}
	return out
}
