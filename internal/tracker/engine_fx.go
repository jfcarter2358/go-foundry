package tracker

// engine_fx.go — the engine's effects path: what a Score's mixer plays
// through.
//
// The Tracker's mix is fixed: each strip's filter, drive and crush, a
// reverb on send bus A, a delay on B, a compressor on the master. A Score
// has a DAW's mixer instead — an insert chain on every line, a chain on
// each send bus, a master chain (internal/tracker/fx) — and moves its
// controls continuously, for automation lanes. SetBusChains switches an
// engine onto this path; a Tracker song never does, so its sound is the
// golden-tested one.
//
// On this path, per strip and per sub-block: the strip's own filter, drive
// and crush; its insert chain; then the fader and pan, ramped across the
// sub-block from where the last one ended (a control cued every few
// milliseconds is a smooth curve, a step is declicked over at most
// controlBlock frames); then the sends, pre- or post-fader. A strip with
// inserts keeps running them after its notes end, so a delay or reverb
// insert rings out, until its output has been silent a while. The buses
// run their chains (an empty chain returns its input as it is), are
// returned with their own level, pan and mute, and the master chain runs
// on the sum before the master gain.

import (
	"math"

	"foundry/internal/tracker/fx"
)

// Effect-chain targets other than a strip, for CueFX and Engine.Chain.
const (
	FXBusA   = -10
	FXBusB   = -11
	FXMaster = -12
)

// chainTailSeconds is how long a strip with inserts keeps running them
// once nothing plays into it and they have gone quiet: longer than the
// longest echo, so a slow delay's next repeat is not cut off.
const chainTailSeconds = fx.MaxEchoSeconds + 1

// busState is a send bus on the effects path.
type busState struct {
	chain    *fx.Chain
	ret, pan float32
	mute     bool
	gl0, gr0 float32
	primed   bool
	peak     float32
	// rampLeft is the frames the return has left to reach ret and pan.
	rampLeft int
}

// SetBusChains switches the engine onto the effects path with these send
// bus chains and master chain (nil chains are empty: a bus returns its
// input, the master is a wire). The chains are the engine's from now on;
// build them off the mixer goroutine.
func (e *Engine) SetBusChains(a, b, master *fx.Chain) {
	e.fxPath = true
	e.buses[0].chain, e.buses[1].chain, e.master = a, b, master
	a.SetTempo(e.bpm)
	b.SetTempo(e.bpm)
	master.SetTempo(e.bpm)
}

// SetInserts gives a strip its insert chain (nil: none). Effects path only.
func (e *Engine) SetInserts(i int, c *fx.Chain) {
	if i < 0 || i >= len(e.strips) {
		return
	}
	st := &e.strips[i]
	st.chain, st.quiet = c, 0
	c.SetTempo(e.bpm)
}

// Chain is the chain a target names: a strip's inserts (strip >= 0),
// FXBusA, FXBusB or FXMaster. Nil for none.
func (e *Engine) Chain(target int) *fx.Chain { return e.chainFor(target) }

func (e *Engine) chainFor(target int) *fx.Chain {
	switch {
	case target >= 0 && target < len(e.strips):
		return e.strips[target].chain
	case target == FXBusA:
		return e.buses[0].chain
	case target == FXBusB:
		return e.buses[1].chain
	case target == FXMaster:
		return e.master
	}
	return nil
}

// SetBus sets a send bus's return level, pan (balance, -1..1) and mute on
// the effects path. Cheap.
func (e *Engine) SetBus(bus int, ret, pan float32, mute bool) {
	if bus < 0 || bus > 1 {
		return
	}
	b := &e.buses[bus]
	b.ret, b.pan, b.mute = ret, clamp32(pan, -1, 1), mute
	b.rampLeft = controlBlock
	if bus == 0 {
		e.reverbSpec.Return = ret
	} else {
		e.delaySpec.Return = ret
	}
}

// BusLevel is a send bus's recent return peak, for meters.
func (e *Engine) BusLevel(bus int) float32 {
	if bus < 0 || bus > 1 {
		return 0
	}
	return math.Float32frombits(e.busLevels[bus].Load())
}

func (e *Engine) chainsTempo() {
	for i := range e.strips {
		e.strips[i].chain.SetTempo(e.bpm)
	}
	e.buses[0].chain.SetTempo(e.bpm)
	e.buses[1].chain.SetTempo(e.bpm)
	e.master.SetTempo(e.bpm)
}

// balance is the strip's pan law: the far side turned down, the near one
// left at the level.
func balance(g, pan float32) (float32, float32) {
	gl, gr := g, g
	if pan < 0 {
		gr *= 1 + pan
	} else if pan > 0 {
		gl *= 1 - pan
	}
	return gl, gr
}

// lin is a per-sample ramp from a to b over n frames (exact when a == b).
type lin struct{ v, d float32 }

func newLin(a, b float32, n int) lin {
	if a == b || n <= 0 {
		return lin{v: b}
	}
	return lin{v: a, d: (b - a) / float32(n)}
}

func (r *lin) next() float32 {
	r.v += r.d
	return r.v
}

// mixStripFX is mixStrip on the effects path.
func (e *Engine) mixStripFX(ci, n int) {
	st := &e.strips[ci]
	nn := n * 2
	gl, gr := balance(st.vol*st.mute, st.pan)
	sa, sb := st.sendA, st.sendB
	if !st.primed {
		st.gl0, st.gr0, st.sa0, st.sb0, st.primed = gl, gr, sa, sb, true
	}
	hasChain := st.chain.Len() > 0
	if !e.used[ci] {
		if !hasChain || st.quiet > e.rate*chainTailSeconds {
			st.peak *= 0.9
			if ci < MaxStrips {
				e.levels[ci].Store(math.Float32bits(st.peak))
			}
			st.gl0, st.gr0, st.sa0, st.sb0 = gl, gr, sa, sb
			return
		}
		// The chain rings on into a silent buffer, which must be cleared
		// before the next block.
		e.used[ci] = true
	}
	buf := e.stripBuf[ci][:nn]
	drive := st.drive > 0
	dk := fx.DriveK(st.drive)
	dnorm := 1 / dk
	crush := st.crush > 0 && st.crush < 16
	steps := fx.CrushSteps(st.crush)
	if st.filtOn || drive || crush {
		for f := 0; f < n; f++ {
			l, r := buf[f*2], buf[f*2+1]
			if st.filtOn {
				l, r = st.filt.Process(0, l), st.filt.Process(1, r)
			}
			if drive {
				l, r = fx.Drive(l, dk, dnorm), fx.Drive(r, dk, dnorm)
			}
			if crush {
				l, r = fx.Crush(l, steps), fx.Crush(r, steps)
			}
			buf[f*2], buf[f*2+1] = l, r
		}
	}
	if hasChain {
		st.chain.Process(buf)
		loud := false
		for _, v := range buf {
			if v > 1e-6 || v < -1e-6 {
				loud = true
				break
			}
		}
		if loud {
			st.quiet = 0
		} else {
			st.quiet += n
		}
	}
	// This sub-block's share of the ramp to the controls: all of it when
	// the ramp ends inside, else in proportion.
	if st.rampLeft > n {
		k := float32(n) / float32(st.rampLeft)
		gl, gr = st.gl0+(gl-st.gl0)*k, st.gr0+(gr-st.gr0)*k
		sa, sb = st.sa0+(sa-st.sa0)*k, st.sb0+(sb-st.sb0)*k
	}
	st.rampLeft = max(st.rampLeft-n, 0)
	rl, rr := newLin(st.gl0, gl, n), newLin(st.gr0, gr, n)
	ra, rb := newLin(st.sa0, sa, n), newLin(st.sb0, sb, n)
	sendA := sa > 0 || st.sa0 > 0
	sendB := sb > 0 || st.sb0 > 0
	mute := st.mute
	peak := st.peak * 0.9
	for f := 0; f < n; f++ {
		x, y := buf[f*2], buf[f*2+1]
		l, r := x*rl.next(), y*rr.next()
		e.dry[f*2] += l
		e.dry[f*2+1] += r
		if sendA {
			g := ra.next()
			if st.preA {
				e.a[f*2] += x * mute * g
				e.a[f*2+1] += y * mute * g
			} else {
				e.a[f*2] += l * g
				e.a[f*2+1] += r * g
			}
		}
		if sendB {
			g := rb.next()
			if st.preB {
				e.b[f*2] += x * mute * g
				e.b[f*2+1] += y * mute * g
			} else {
				e.b[f*2] += l * g
				e.b[f*2+1] += r * g
			}
		}
		if a := absf(l) + absf(r); a*0.5 > peak {
			peak = a * 0.5
		}
	}
	st.gl0, st.gr0, st.sa0, st.sb0 = gl, gr, sa, sb
	st.peak = peak
	if ci < MaxStrips {
		e.levels[ci].Store(math.Float32bits(peak))
	}
}

// mixBusesFX runs the buses through their chains, returns them into the
// dry mix, runs the master chain and writes out.
func (e *Engine) mixBusesFX(out []float32, n int) {
	nn := n * 2
	e.buses[0].chain.Process(e.a[:nn])
	e.buses[1].chain.Process(e.b[:nn])
	var ret [2][2]lin
	for k := range e.buses {
		b := &e.buses[k]
		g := b.ret
		if b.mute {
			g = 0
		}
		gl, gr := balance(g, b.pan)
		if !b.primed {
			b.gl0, b.gr0, b.primed = gl, gr, true
		}
		// This sub-block's share of the ramp, as a strip's.
		if b.rampLeft > n {
			share := float32(n) / float32(b.rampLeft)
			gl, gr = b.gl0+(gl-b.gl0)*share, b.gr0+(gr-b.gr0)*share
		}
		b.rampLeft = max(b.rampLeft-n, 0)
		ret[k][0], ret[k][1] = newLin(b.gl0, gl, n), newLin(b.gr0, gr, n)
		b.gl0, b.gr0 = gl, gr
	}
	pa, pb := e.buses[0].peak*0.9, e.buses[1].peak*0.9
	for f := 0; f < n; f++ {
		l, r := e.dry[f*2], e.dry[f*2+1]
		al, ar := e.a[f*2]*ret[0][0].next(), e.a[f*2+1]*ret[0][1].next()
		bl, br := e.b[f*2]*ret[1][0].next(), e.b[f*2+1]*ret[1][1].next()
		l += al
		r += ar
		l += bl
		r += br
		out[f*2], out[f*2+1] = l, r
		if v := (absf(al) + absf(ar)) * 0.5; v > pa {
			pa = v
		}
		if v := (absf(bl) + absf(br)) * 0.5; v > pb {
			pb = v
		}
	}
	e.buses[0].peak, e.buses[1].peak = pa, pb
	e.busLevels[0].Store(math.Float32bits(pa))
	e.busLevels[1].Store(math.Float32bits(pb))
	e.master.Process(out[:nn])
	// The master gain, ramped like a return (a lane fading the mix out is
	// a curve, not a staircase).
	gain := e.gain
	if !e.gainPrimed {
		e.gain0, e.gainPrimed = gain, true
	}
	if e.gainLeft > n {
		gain = e.gain0 + (gain-e.gain0)*float32(n)/float32(e.gainLeft)
	}
	e.gainLeft = max(e.gainLeft-n, 0)
	rg := newLin(e.gain0, gain, n)
	e.gain0 = gain
	for f := 0; f < n; f++ {
		l, r := out[f*2], out[f*2+1]
		g := rg.next()
		out[f*2], out[f*2+1] = l*g, r*g
		if f&3 == 0 {
			e.scope[e.scopePos].Store(math.Float32bits((l + r) * 0.5 * g))
			e.scopePos = (e.scopePos + 1) % scopeLen
		}
	}
}
