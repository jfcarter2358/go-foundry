package play

// click.go — the metronome: a click on every beat while the transport
// plays, and a count-in before it starts.
//
// Both are for recording from a MIDI keyboard (the editor's real-time
// entry): the player knows exactly when each beat falls, so its own clicks
// land on them sample-accurately, and a count-in that starts the
// transport on the frame its last bar ends needs nothing to line two
// clocks up. A click is a short sine blip mixed straight into the output,
// after the master: it is a cue for the player at the keyboard, not part
// of the music, and no fader or effect touches it. The downbeat's is
// higher. Like the rest of Render it allocates nothing.

import "math"

// maxClicks is how many clicks can ring at once (a click is 40 ms; even
// a very fast count-in has two going).
const maxClicks = 8

// The click's sound: a sine blip with a fast exponential decay.
const (
	clickHz       = 1320.0
	clickAccentHz = 1760.0
	clickSeconds  = 0.04
	clickDecay    = 0.008
)

type clickVoice struct {
	on     bool
	accent bool
	delay  int // frames into the coming block it starts at
	t      int // frames played
	level  float32
}

// countIn is a count-in under way: bars of the bar at the start's beats,
// at the score's tempo there, then the transport plays from startSec.
type countIn struct {
	on bool
	// bar indexes the performance's bars: the bar whose beats are counted.
	bar int
	// beat0 is the bar's first beat (Performance.Beats); n its beats.
	beat0, n int
	bars     int
	// elapsed and total are score seconds into and of the count-in.
	elapsed, total float64
	startSec       float64
	// next is the next beat to click (0..bars*n-1); heard the beats so far.
	next, heard int
	level       float32
}

// SetClick turns the metronome on or off: a click on every beat while the
// transport plays, louder on the downbeat, at level (0..1).
func (p *Player) SetClick(on bool, level float32) bool {
	return p.send(command{kind: cmdClick, b: on, f: float64(clamp32(level, 0, 1))})
}

// CountIn clicks bars of the meter of the bar holding tick, at the tempo
// there, then plays from tick — on the frame the count ends. bars <= 0
// plays at once.
func (p *Player) CountIn(tick int64, bars int, level float32) bool {
	return p.send(command{kind: cmdCountIn, tick: tick, i: bars, f: float64(clamp32(level, 0, 1))})
}

// startCountIn sets a count-in up (the mixer's side of CountIn).
func (p *Player) startCountIn(tick int64, bars int, level float32) {
	perf := p.prog.Perf
	start := perf.Tempo.Seconds(float64(clampTick(tick, perf.Length)))
	p.playing = false
	p.releaseAll(0)
	p.count = countIn{}
	if bars <= 0 || len(perf.Bars) == 0 {
		p.jump(0, start)
		p.playing, p.finished = true, false
		return
	}
	bi := perf.BarAt(float64(clampTick(tick, perf.Length)))
	b := perf.Bars[bi]
	c := countIn{on: true, bar: bi, bars: bars, startSec: start, level: level, beat0: -1}
	for k, bt := range perf.Beats {
		if bt.Bar == bi {
			if c.beat0 < 0 {
				c.beat0 = k
			}
			c.n++
		}
	}
	if c.n == 0 {
		c.beat0, c.n = -1, max(len(b.Meter.Beats()), 1)
	}
	c.total = float64(bars) * (b.End - b.Start)
	p.count = c
	// The play head sits at the start while the count runs, so a position
	// read during it already says where the music will begin.
	p.reindex(start)
}

// beatTime is count-in beat j's time, score seconds from the count's start.
func (p *Player) beatTime(j int) float64 {
	c := &p.count
	b := p.prog.Perf.Bars[c.bar]
	barLen := b.End - b.Start
	k := j % c.n
	off := barLen * float64(k) / float64(c.n)
	if c.beat0 >= 0 {
		off = p.prog.Perf.Beats[c.beat0+k].Start - b.Start
	}
	return float64(j/c.n)*barLen + off
}

// countFrames clicks this block's count-in beats and, when the count ends
// inside it, starts the transport there. It returns the frame the
// transport starts on (frames when it does not start in this block).
func (p *Player) countFrames(frames int) float64 {
	c := &p.count
	rate := float64(p.rate)
	scale := math.Max(p.scale, 1e-6)
	dt := float64(frames) / rate * scale
	for c.next < c.bars*c.n {
		t := p.beatTime(c.next)
		if t >= c.elapsed+dt || t >= c.total {
			break
		}
		f := int(math.Round((t - c.elapsed) / scale * rate))
		p.addClick(max(f, 0), c.next%c.n == 0, c.level)
		c.next++
		c.heard = c.next
	}
	if c.elapsed+dt < c.total {
		c.elapsed += dt
		return float64(frames)
	}
	fpos := math.Max((c.total-c.elapsed)/scale*rate, 0)
	start := c.startSec
	p.count = countIn{}
	p.jump(int(math.Round(fpos)), start)
	p.playing, p.finished = true, false
	return fpos
}

// addClick starts a click at a frame of the coming block.
func (p *Player) addClick(frame int, accent bool, level float32) {
	if level <= 0 {
		return
	}
	for k := range p.clicks {
		if !p.clicks[k].on {
			p.clicks[k] = clickVoice{on: true, accent: accent, delay: frame, level: level}
			return
		}
	}
}

// clicking reports a click ringing or a count-in running.
func (p *Player) clicking() bool {
	if p.count.on {
		return true
	}
	for k := range p.clicks {
		if p.clicks[k].on {
			return true
		}
	}
	return false
}

// mixClicks adds the ringing clicks into out.
func (p *Player) mixClicks(out []float32, frames int) {
	rate := float64(max(p.rate, 1))
	length := int(clickSeconds * rate)
	for k := range p.clicks {
		v := &p.clicks[k]
		if !v.on {
			continue
		}
		if v.delay >= frames {
			v.delay -= frames
			continue
		}
		hz, amp := clickHz, 0.35*float64(v.level)
		if v.accent {
			hz, amp = clickAccentHz, 0.5*float64(v.level)
		}
		w := 2 * math.Pi * hz / rate
		for i := v.delay; i < frames && v.t < length; i++ {
			s := float32(amp * math.Sin(w*float64(v.t)) * math.Exp(-float64(v.t)/(clickDecay*rate)))
			out[2*i] += s
			out[2*i+1] += s
			v.t++
		}
		v.delay = 0
		if v.t >= length {
			*v = clickVoice{}
		}
	}
}
