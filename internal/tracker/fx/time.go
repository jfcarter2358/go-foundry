package fx

// time.go — the delay and the reverb: effects made of memory. Both are the
// Tracker's kernels (kernels.go) with a wet/dry mix; on a send bus they
// return only the wet signal, as the Tracker's buses do.

// ---------- delay ----------

const (
	dlBeats = iota
	dlFeedback
	dlTone
	dlPingPong
	dlMix
)

// delayGlide is how long the delay takes to follow a new time (a tempo
// change, the time turned): long enough not to click, short enough to keep
// up with a ritardando.
const delayGlide = 0.06

type delay struct {
	base
	echo *Echo
	bpm  float32
	dry  [2 * ChunkFrames]float32
}

func newDelay(rate int) *delay {
	d := &delay{base: newBase(KindDelay, rate), echo: NewEcho(rate), bpm: 120}
	d.echo.Flush = true
	return d
}

func (d *delay) SetTempo(bpm float32) {
	if bpm <= 0 {
		bpm = 120
	}
	if bpm != d.bpm {
		d.bpm, d.dirty = bpm, true
	}
}

func (d *delay) Reset() { d.echo.Reset() }

func (d *delay) Process(buf []float32) { runChunks(d, buf) }

// length is the delay in samples the time and tempo give (the Tracker's
// rounding: whole samples, cut to the line).
func (d *delay) length() float64 {
	n := int(float64(d.val(dlBeats)) * 60 / float64(d.bpm) * float64(d.rate))
	return float64(clampInt(n, 1, d.echo.MaxLength()))
}

func (d *delay) chunk(buf []float32) {
	n := len(buf) / 2
	d.advance(n)
	if d.dirty {
		d.dirty = false
		d.echo.GlideTo(d.length(), delayGlide, d.rate)
		d.echo.SetVoice(d.val(dlFeedback), d.val(dlTone), d.val(dlPingPong) >= 0.5)
	}
	m0, m1 := d.p[dlMix].prev, d.val(dlMix)
	if m0 == 0 && m1 == 0 {
		// Dry: keep the line running so turning the mix up finds echoes.
		for f := 0; f < n; f++ {
			d.echo.Process(buf[f*2], buf[f*2+1])
		}
		return
	}
	copy(d.dry[:len(buf)], buf)
	for f := 0; f < n; f++ {
		buf[f*2], buf[f*2+1] = d.echo.Process(buf[f*2], buf[f*2+1])
	}
	blend(buf, d.dry[:len(buf)], m0, m1)
}

// ---------- reverb ----------

const (
	rvSize = iota
	rvDamping
	rvWidth
	rvMix
)

type reverb struct {
	base
	fv  *Freeverb
	dry [2 * ChunkFrames]float32
}

func newReverb(rate int) *reverb {
	r := &reverb{base: newBase(KindReverb, rate), fv: NewFreeverb(rate)}
	r.fv.Flush = true
	return r
}

func (r *reverb) Reset() { r.fv.Reset() }

func (r *reverb) Process(buf []float32) { runChunks(r, buf) }

func (r *reverb) chunk(buf []float32) {
	n := len(buf) / 2
	r.advance(n)
	if r.dirty {
		r.dirty = false
		r.fv.Set(r.val(rvSize), r.val(rvDamping), r.val(rvWidth))
	}
	copy(r.dry[:len(buf)], buf)
	for f := 0; f < n; f++ {
		buf[f*2], buf[f*2+1] = r.fv.Process(buf[f*2], buf[f*2+1])
	}
	blend(buf, r.dry[:len(buf)], r.p[rvMix].prev, r.val(rvMix))
}
