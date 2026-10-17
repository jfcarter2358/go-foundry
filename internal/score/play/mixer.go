package play

// mixer.go — the score's mixer on the engine's effects path: insert chains,
// the two send buses, the master chain, and the automation lanes that move
// them while the score plays.
//
// A Program holds each chain as a plan — the effects' kinds, every
// parameter's value, bypass — read once from the file. The chains
// themselves hold state (a delay line, a reverb tail), so each Player
// builds its own from the plan, on the game thread, and hands them to the
// mixer with the program. A new program (an edit) whose chain has the same
// effects in the same order keeps the playing chain and only moves its
// settings (fx.Chain.Take), so editing a note or turning a knob never cuts
// a reverb tail.
//
// Automation: each line's lanes are curves over the performance
// (interpret.Curve). Once a block their values at the play head are set
// with the fader; while the transport runs, a lane that moves inside a
// block is cued (tracker.CueStrip, tracker.CueFX) every laneStep frames
// and on each of its breakpoints, each cue carrying the value the lane
// reaches at the next one — the engine ramps a strip's gains and an
// effect glides its parameter across exactly that stretch, so a lane is
// followed sample by sample in straight pieces at most laneStep long and
// a step lands on its frame.

import (
	"fmt"
	"math"
	"sort"

	"foundry/internal/score"
	"foundry/internal/tracker"
	"foundry/internal/tracker/fx"
)

// laneStep is the longest straight piece a moving lane is cued in, frames.
const laneStep = 64

// laneReserve is the room in the engine's cue queue lanes leave for notes.
// A block with more moving lanes than fit at laneStep (a long device
// buffer under many automated lines) is cued on a wider grid instead —
// every breakpoint still while they fit — so a lane is never left behind
// by a cue the queue refused.
const laneReserve = 256

// knobGlide is how long a parameter turned live (the editor's knobs)
// takes to get there, seconds.
const knobGlide = 0.02

// fxSlot is one effect of a chain as a plan.
type fxSlot struct {
	kind   fx.Kind
	params [fx.MaxParams]float32
	bypass bool
}

// busMix is a send bus's return as a plan.
type busMix struct {
	ret, pan float32
	mute     bool
}

// laneAuto is one automation lane resolved: the curve that drives it and
// what it moves (for an effect parameter, the insert slot).
type laneAuto struct {
	curve  int
	target score.LaneTarget
	slot   int
}

// planChain reads a chain from the file. Effects this build does not have
// are left out (and reported); settings they do not take are ignored.
func planChain(where string, chain []score.FX, place fx.Placement) (plan []fxSlot, errs []error) {
	for i, f := range chain {
		k, ok := fx.Lookup(f.Kind)
		if !ok {
			errs = append(errs, fmt.Errorf("%s %d: no effect %q; it plays nothing", where, i+1, f.Kind))
			continue
		}
		sp := k.Spec()
		sl := fxSlot{kind: k, bypass: f.Bypass}
		for pi := range sp.Params {
			sl.params[pi] = sp.Default(pi, place)
		}
		for _, p := range f.Params {
			if pi := sp.Param(p.Name); pi >= 0 {
				sl.params[pi] = sp.Params[pi].Clamp(float32(p.Value))
			}
		}
		plan = append(plan, sl)
	}
	return plan, errs
}

// buildChain makes a chain from a plan. Allocates.
func buildChain(plan []fxSlot, rate int, place fx.Placement) *fx.Chain {
	c := fx.NewChain(len(plan))
	for _, sl := range plan {
		e := fx.New(sl.kind, rate, place)
		for pi := range sl.kind.Spec().Params {
			e.SetParam(pi, sl.params[pi], 0)
		}
		c.Add(e, sl.bypass)
	}
	return c
}

// chainSet is a player's live chains.
type chainSet struct {
	rate         int
	inserts      []*fx.Chain
	a, b, master *fx.Chain
}

// planMix reads every chain, the buses' returns and the lanes' targets
// from the program's score.
func (p *Program) planMix() (errs []error) {
	s := p.Score
	p.inserts = make([][]fxSlot, len(s.Lines))
	for i, l := range s.Lines {
		var e []error
		p.inserts[i], e = planChain("line "+l.Name+" insert", l.Inserts, fx.Insert)
		errs = append(errs, e...)
	}
	var e []error
	p.busA, e = planChain("bus a effect", s.Buses.A.FX, fx.Bus)
	errs = append(errs, e...)
	p.busB, e = planChain("bus b effect", s.Buses.B.FX, fx.Bus)
	errs = append(errs, e...)
	p.master, e = planChain("master effect", s.Master, fx.Insert)
	errs = append(errs, e...)
	p.buses = [2]busMix{
		{clamp32(s.Buses.A.Return, 0, 4), clamp32(s.Buses.A.Pan, -1, 1), s.Buses.A.Mute},
		{clamp32(s.Buses.B.Return, 0, 4), clamp32(s.Buses.B.Pan, -1, 1), s.Buses.B.Mute},
	}
	return errs
}

// planLanes resolves the performance's lane curves to what they move:
// each line's, and the buses' and the master's (mixLanes).
func (p *Program) planLanes() {
	s := p.Score
	p.lanes = make([][]laneAuto, len(s.Lines))
	p.mixLanes = [3][]laneAuto{}
	for k, c := range p.Perf.Curves {
		if !s.HasOwner(c.Line) {
			continue
		}
		t, err := score.ParseLaneTargetIn(score.OwnerScope(c.Line), c.Target)
		if err != nil {
			continue // Validate reports it
		}
		a := laneAuto{curve: k, target: t, slot: -1}
		if t.Kind == score.LaneFX {
			chain := *s.ChainOf(c.Line)
			if a.slot = score.ChainSlot(chain, t); a.slot < 0 {
				continue
			}
			// The plan's slot: effects this build lacks are not in it.
			a.slot = planSlot(chain, a.slot)
		}
		if c.Line >= 0 {
			p.lanes[c.Line] = append(p.lanes[c.Line], a)
		} else {
			m := mixIndex(c.Line)
			p.mixLanes[m] = append(p.mixLanes[m], a)
		}
	}
}

// mixIndex is a mix owner's index into mixLanes: bus A, bus B, master.
func mixIndex(owner int) int { return score.OwnerBusA - owner }

// A lane unit is what the scheduler moves for one owner's lanes: a line
// (its strip, unit = line index), then bus A, bus B and the master (units
// nLines, +1, +2).
func (p *Program) unitLanes(u int) []laneAuto {
	if u < len(p.lanes) {
		return p.lanes[u]
	}
	return p.mixLanes[u-len(p.lanes)]
}

// unitChain is a unit's effect chain as the engine names it.
func (p *Program) unitChain(u int) int {
	if u < len(p.lanes) {
		return u
	}
	return [3]int{tracker.FXBusA, tracker.FXBusB, tracker.FXMaster}[u-len(p.lanes)]
}

// planSlot is where the file's insert i sits in its plan (unknown effects
// are left out of the plan).
func planSlot(chain []score.FX, i int) int {
	n := 0
	for k := 0; k < i; k++ {
		if _, ok := fx.Lookup(chain[k].Kind); ok {
			n++
		}
	}
	return n
}

// chains builds a set of chains from the program's plans at a rate.
func (p *Program) chains(rate int) *chainSet {
	cs := &chainSet{rate: rate, inserts: make([]*fx.Chain, len(p.inserts))}
	for i, plan := range p.inserts {
		if len(plan) > 0 {
			cs.inserts[i] = buildChain(plan, rate, fx.Insert)
		}
	}
	cs.a = buildChain(p.busA, rate, fx.Bus)
	cs.b = buildChain(p.busB, rate, fx.Bus)
	cs.master = buildChain(p.master, rate, fx.Insert)
	return cs
}

// WithMix is the program with the mixer of s — line faders, pans, sends,
// inserts, the buses and the master chain — and the same performance.
// s must have the same music (notes, lanes, form) as the program's score:
// it is what the editor sends when only the mixer changed.
func (p *Program) WithMix(s *score.Score) (*Program, []error) {
	c := *p
	c.Score = s
	errs := c.planMix()
	c.planLanes()
	return &c, errs
}

// ---------- on the mixer ----------

// adoptChains hands the engine a program's chains, keeping each playing
// chain that has the same effects (taking the new one's settings) unless
// fresh (a new sample rate: every chain is replaced).
func (p *Player) adoptChains(cs *chainSet, fresh bool) {
	if cs == nil {
		return
	}
	glide := int(knobGlide * float64(max(cs.rate, 1)))
	keep := func(old, nw *fx.Chain) *fx.Chain {
		if !fresh && old != nil && old.SameKinds(nw) {
			old.Take(nw, glide)
			return old
		}
		return nw
	}
	for i := range cs.inserts {
		old, nw := p.eng.Chain(i), cs.inserts[i]
		if nw == nil {
			p.eng.SetInserts(i, nil)
			continue
		}
		p.eng.SetInserts(i, keep(old, nw))
	}
	a := keep(p.eng.Chain(tracker.FXBusA), cs.a)
	b := keep(p.eng.Chain(tracker.FXBusB), cs.b)
	m := keep(p.eng.Chain(tracker.FXMaster), cs.master)
	p.eng.SetBusChains(a, b, m)
	p.chainRate = cs.rate
}

// applyBuses sets the engine's bus returns, and the master gain, from
// what the player has for them with their lanes at the play head.
func (p *Player) applyBuses() {
	tick := p.prog.Perf.Tempo.Tick(p.sec)
	for k := range p.busBase {
		ret, pan := p.busNow(k, tick, false)
		p.eng.SetBus(k, ret, pan, p.busBase[k].mute)
	}
	p.eng.SetGain(p.gainNow(tick, false))
}

// busNow is bus k's return and pan at tick: the bus's own, or its lanes'.
func (p *Player) busNow(k int, tick float64, before bool) (ret, pan float32) {
	ret, pan = p.busBase[k].ret, p.busBase[k].pan
	for _, a := range p.prog.mixLanes[k] {
		cv := &p.prog.Perf.Curves[a.curve]
		v := cv.At(tick)
		if before {
			v = cv.Before(tick)
		}
		switch a.target.Kind {
		case score.LaneReturn:
			ret = clamp32(v, 0, 4)
		case score.LanePan:
			pan = clamp32(v, -1, 1)
		}
	}
	return ret, pan
}

// gainNow is the master gain at tick: the player's, times the master's
// volume lane.
func (p *Player) gainNow(tick float64, before bool) float32 {
	g := p.gainBase
	for _, a := range p.prog.mixLanes[2] {
		if a.target.Kind != score.LaneVolume {
			continue
		}
		cv := &p.prog.Perf.Curves[a.curve]
		v := cv.At(tick)
		if before {
			v = cv.Before(tick)
		}
		g *= clamp32(v, 0, 4)
	}
	return g
}

// mixControls sets, once a block, what the buses' and the master's lanes
// say at tick (only where they have lanes, so a mix without them is left
// exactly as set).
func (p *Player) mixControls(tick float64) {
	n := len(p.prog.lanes)
	for k := 0; k < 3; k++ {
		if len(p.prog.mixLanes[k]) == 0 {
			continue
		}
		switch k {
		case 2:
			p.eng.SetGain(p.gainNow(tick, false))
		default:
			ret, pan := p.busNow(k, tick, false)
			p.eng.SetBus(k, ret, pan, p.busBase[k].mute)
		}
		p.setFXLanes(n+k, tick, laneStep, false)
	}
}

// laneControls lays line li's lanes at tick over its strip controls and
// returns them; fx lanes are left to fxLanes.
func (p *Player) laneControls(li int, c tracker.StripControls, tick float64, before bool) tracker.StripControls {
	prog := p.prog
	for _, a := range prog.lanes[li] {
		cv := &prog.Perf.Curves[a.curve]
		v := cv.At(tick)
		if before {
			v = cv.Before(tick)
		}
		switch a.target.Kind {
		case score.LaneVolume:
			c.Volume *= clamp32(v, 0, 4)
		case score.LanePan:
			c.Pan = clamp32(v, -1, 1)
		case score.LaneCutoff:
			if hz := clamp32(v, 16, 20000); c.CutoffHz == 0 || hz < c.CutoffHz {
				c.CutoffHz = hz
			}
		case score.LaneSendA:
			c.SendA = clamp32(v, 0, 1)
		case score.LaneSendB:
			c.SendB = clamp32(v, 0, 1)
		}
	}
	return c
}

// stripBase is line li's strip before lanes: fader, pan, sends, bindings.
func (p *Player) stripBase(li int) tracker.StripControls {
	f := p.faders[li]
	c := tracker.StripControls{Volume: f.Volume, Pan: f.Pan, SendA: f.SendA, SendB: f.SendB, PreA: f.PreA, PreB: f.PreB}
	for _, b := range p.binds {
		if b.line != li {
			continue
		}
		v := p.params[b.param].cur
		lin := b.from + (b.to-b.from)*v
		switch b.target {
		case tracker.BindVolume:
			c.Volume *= lin
		case tracker.BindPan:
			c.Pan = clamp32(c.Pan+lin, -1, 1)
		case tracker.BindSendA:
			c.SendA = lin
		case tracker.BindSendB:
			c.SendB = lin
		case tracker.BindCutoff:
			from, to := max(b.from, 16), max(b.to, 16)
			hz := from * float32(math.Pow(float64(to/from), float64(v)))
			if c.CutoffHz == 0 || hz < c.CutoffHz {
				c.CutoffHz = hz
			}
		}
	}
	return c
}

// setFXLanes sets every effect lane of lane unit u (a line, a bus, the
// master) to its value at tick, the effect gliding there over frames.
func (p *Player) setFXLanes(u int, tick float64, frames int, before bool) {
	prog := p.prog
	ch := p.eng.Chain(prog.unitChain(u))
	if ch == nil {
		return
	}
	for _, a := range prog.unitLanes(u) {
		if a.target.Kind != score.LaneFX || a.slot >= ch.Len() {
			continue
		}
		cv := &prog.Perf.Curves[a.curve]
		v := cv.At(tick)
		if before {
			v = cv.Before(tick)
		}
		ch.SetParam(a.slot, a.target.Param, v, frames)
	}
}

// cueLanes cues, for every line (and bus, and the master) whose lanes
// move between score times from and to (frames f0 and f1 of the block),
// the lanes' values along the way: at every laneStep frames and every
// breakpoint, the value reached at the next cue. When that many cues would
// not fit in the engine's queue (beside laneReserve for notes), the grid
// is widened for this stretch, every moving unit alike.
func (p *Player) cueLanes(from, to, f0, f1 float64) {
	prog := p.prog
	if f1-f0 < 0.5 {
		return
	}
	tempo := prog.Perf.Tempo
	t0, t1 := tempo.Tick(from), tempo.Tick(to)
	rate := float64(p.rate)
	tickAt := func(f float64) float64 { return tempo.Tick(from + (f-f0)/rate*p.scale) }
	nl := len(prog.lanes)
	units := nl + 3
	moving := func(u int) bool {
		if u < nl && u >= len(p.base) {
			return false
		}
		for _, a := range prog.unitLanes(u) {
			if prog.Perf.Curves[a.curve].Varies(t0, t1) {
				return true
			}
		}
		return false
	}
	// How many cues a piece takes over every moving unit, and how many
	// breakpoints fall in the stretch (each one more piece).
	per, bps := 0, 0
	for u := 0; u < units; u++ {
		if !moving(u) {
			continue
		}
		lanes := prog.unitLanes(u)
		per++
		for _, a := range lanes {
			if a.target.Kind == score.LaneFX {
				per++
			}
			pts := prog.Perf.Curves[a.curve].Points
			k := sort.Search(len(pts), func(i int) bool { return pts[i].Tick > t0 })
			for ; k < len(pts) && pts[k].Tick < t1; k++ {
				bps += 1 + len(lanes)
			}
		}
	}
	if per == 0 {
		return
	}
	step := float64(laneStep)
	room := p.eng.CueRoom() - laneReserve
	exact := room >= bps+per // room for every breakpoint and one piece
	if pieces := math.Ceil((f1 - f0) / laneStep); float64(per)*pieces+float64(bps) > float64(room) {
		fit := float64(room-bps) / float64(per)
		if !exact {
			fit = float64(room) / float64(per)
		}
		step = f1 - f0 + laneStep // one piece
		if fit >= 1 {
			step = laneStep * math.Ceil(pieces/math.Floor(fit))
		}
	}
	for u := 0; u < units; u++ {
		if !moving(u) {
			continue
		}
		lanes := prog.unitLanes(u)
		target := prog.unitChain(u)
		x := f0
		for x < f1-0.5 {
			// The next cue: the next grid frame, the next breakpoint, or
			// the segment's end.
			next := math.Min(f1, (math.Floor(x/step)+1)*step)
			tx := tickAt(x)
			for _, a := range lanes {
				if !exact {
					break
				}
				pts := prog.Perf.Curves[a.curve].Points
				k := sort.Search(len(pts), func(i int) bool { return pts[i].Tick > tx })
				if k < len(pts) && pts[k].Tick < t1 {
					if f := f0 + (tempo.Seconds(pts[k].Tick)-from)/p.scale*rate; f > x+0.5 && f < next {
						next = f
					}
				}
			}
			at := int(math.Round(x))
			end := int(math.Round(next))
			frames := max(end-at, 1)
			tn := tickAt(next)
			switch {
			case u < nl:
				c := p.laneControls(u, p.base[u], tn, true)
				p.cue(tracker.Cue{At: at, Kind: tracker.CueStrip, Strip: u, Controls: c, Frames: frames})
			case u == nl+2:
				p.cue(tracker.Cue{At: at, Kind: tracker.CueGain, Value: p.gainNow(tn, true), Frames: frames})
			default:
				k := u - nl
				ret, pan := p.busNow(k, tn, true)
				p.cue(tracker.Cue{At: at, Kind: tracker.CueBus, Strip: k, Controls: tracker.StripControls{Volume: ret, Pan: pan}, On: p.busBase[k].mute, Frames: frames})
			}
			for _, a := range lanes {
				if a.target.Kind != score.LaneFX {
					continue
				}
				cv := &prog.Perf.Curves[a.curve]
				if !cv.Varies(tx, tn+1e-6) && cv.At(tx) == cv.Before(tn) {
					continue
				}
				p.cue(tracker.Cue{At: at, Kind: tracker.CueFX, Strip: target, Slot: a.slot, Param: a.target.Param, Value: cv.Before(tn), Frames: frames})
			}
			x = next
		}
	}
}
