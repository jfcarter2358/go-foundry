package fx

// eq.go — the parametric EQ: a low shelf, a high shelf and four bell
// bands, each an RBJ-cookbook biquad in double precision (a low shelf at
// 40 Hz in single precision rounds its poles audibly). A band at 0 dB is
// left out of the signal altogether, so a fresh EQ is an exact wire.

import "math"

// biquad is one second-order section, transposed direct form II, two
// channels.
type biquad struct {
	b0, b1, b2, a1, a2 float64
	z1, z2             [2]float64
	on                 bool
}

func (q *biquad) reset() { q.z1, q.z2 = [2]float64{}, [2]float64{} }

func (q *biquad) process(ch int, x float64) float64 {
	y := q.b0*x + q.z1[ch]
	q.z1[ch] = flush64(q.b1*x - q.a1*y + q.z2[ch])
	q.z2[ch] = flush64(q.b2*x - q.a2*y)
	return y
}

func (q *biquad) norm(b0, b1, b2, a0, a1, a2 float64) {
	q.b0, q.b1, q.b2, q.a1, q.a2 = b0/a0, b1/a0, b2/a0, a1/a0, a2/a0
}

type shelfOrBell uint8

const (
	lowShelf shelfOrBell = iota
	highShelf
	bell
)

// set designs the section: gain in dB at freq Hz, q for a bell (a shelf
// has slope 1).
func (q *biquad) set(kind shelfOrBell, gainDB, freq, qv float32, rate int) {
	f := math.Min(float64(freq), 0.45*float64(rate))
	A := math.Pow(10, float64(gainDB)/40)
	w := 2 * math.Pi * f / float64(rate)
	cw, sw := math.Cos(w), math.Sin(w)
	switch kind {
	case bell:
		alpha := sw / (2 * math.Max(float64(qv), 0.05))
		q.norm(1+alpha*A, -2*cw, 1-alpha*A, 1+alpha/A, -2*cw, 1-alpha/A)
	case lowShelf:
		sa := 2 * math.Sqrt(A) * sw / 2 * math.Sqrt2
		q.norm(A*((A+1)-(A-1)*cw+sa), 2*A*((A-1)-(A+1)*cw), A*((A+1)-(A-1)*cw-sa),
			(A+1)+(A-1)*cw+sa, -2*((A-1)+(A+1)*cw), (A+1)+(A-1)*cw-sa)
	case highShelf:
		sa := 2 * math.Sqrt(A) * sw / 2 * math.Sqrt2
		q.norm(A*((A+1)+(A-1)*cw+sa), -2*A*((A-1)+(A+1)*cw), A*((A+1)+(A-1)*cw-sa),
			(A+1)-(A-1)*cw+sa, 2*((A-1)-(A+1)*cw), (A+1)-(A-1)*cw-sa)
	}
}

// eqSections are the EQ's sections in signal order, with their
// parameters' indexes: gain, freq, q (-1 for a shelf).
var eqSections = [6]struct {
	kind            shelfOrBell
	gain, freq, qIx int
}{
	{lowShelf, 0, 1, -1}, {highShelf, 2, 3, -1},
	{bell, 4, 5, 6}, {bell, 7, 8, 9}, {bell, 10, 11, 12}, {bell, 13, 14, 15},
}

const eqOutput = 16

type eq struct {
	base
	sec [6]biquad
}

func newEQ(rate int) *eq { return &eq{base: newBase(KindEQ, rate)} }

func (e *eq) Reset() {
	for i := range e.sec {
		e.sec[i].reset()
	}
}

func (e *eq) Process(buf []float32) { runChunks(e, buf) }

func (e *eq) chunk(buf []float32) {
	n := len(buf) / 2
	e.advance(n)
	if e.dirty {
		e.dirty = false
		for i, s := range eqSections {
			q := &e.sec[i]
			g := e.val(s.gain)
			on := g != 0 || e.p[s.gain].left > 0
			if on && !q.on {
				q.reset()
			}
			q.on = on
			if on {
				qv := float32(1)
				if s.qIx >= 0 {
					qv = e.val(s.qIx)
				}
				q.set(s.kind, g, e.val(s.freq), qv, e.rate)
			}
		}
	}
	for i := range e.sec {
		q := &e.sec[i]
		if !q.on {
			continue
		}
		for f := 0; f < n; f++ {
			buf[f*2] = float32(q.process(0, float64(buf[f*2])))
			buf[f*2+1] = float32(q.process(1, float64(buf[f*2+1])))
		}
	}
	out := e.p[eqOutput]
	if out.prev == 0 && out.cur == 0 {
		return
	}
	r := newRamp(dbGain(out.prev), dbGain(e.val(eqOutput)), n)
	for f := 0; f < n; f++ {
		g := r.next()
		buf[f*2] *= g
		buf[f*2+1] *= g
	}
}
