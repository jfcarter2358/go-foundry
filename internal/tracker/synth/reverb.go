package synth

// reverb.go — a feedback delay network for tails baked into samples.
//
// Most samples stay dry: the tracker's own effects add space, and a baked
// room cannot be taken out. A few sounds are their room, though — an 80s
// gated snare is a plate cut off mid-bloom, a taiko is a hall, a cinematic
// impact is mostly tail — and those get this.
//
// Eight delay lines of mutually unrelated lengths, mixed through a Hadamard
// matrix (lossless, maximally diffuse), each with a lowpass so the highs die
// first as they do in air. Four allpasses before it smear the input so a
// sharp hit does not come back as a flutter of discrete echoes.

import "math"

type verb struct {
	t60      float64 // seconds
	size     float64 // scales the line lengths: 0.3 small room .. 1.5 hall
	damp     float64 // Hz: line lowpass cutoff
	predelay float64 // seconds
}

type delayLine struct {
	buf []float64
	i   int
}

func newDelay(n int) *delayLine {
	if n < 1 {
		n = 1
	}
	return &delayLine{buf: make([]float64, n)}
}

func (d *delayLine) out() float64 { return d.buf[d.i] }

func (d *delayLine) push(v float64) {
	d.buf[d.i] = v
	d.i++
	if d.i == len(d.buf) {
		d.i = 0
	}
}

// allpass is a Schroeder allpass diffuser.
type allpass struct {
	d *delayLine
	g float64
}

func (a *allpass) tick(x float64) float64 {
	z := a.d.out()
	v := x + a.g*z
	a.d.push(v)
	return z - a.g*v
}

// reverb returns the wet stereo signal for a mono or stereo input; the
// caller mixes it. The output is as long as the input, so leave room in the
// input for the tail.
func reverb(l, r []float64, sr float64, p verb) (wl, wr []float64) {
	if r == nil {
		r = l
	}
	lens := [8]float64{0.0297, 0.0371, 0.0411, 0.0437, 0.0533, 0.0599, 0.0671, 0.0733}
	var lines [8]*delayLine
	var gains [8]float64
	var lp [8]onePole
	for i, s := range lens {
		n := int(s * p.size * sr)
		lines[i] = newDelay(n)
		gains[i] = math.Pow(10, -3*float64(n)/(sr*p.t60))
		lp[i] = newOnePole(p.damp, sr)
	}
	diff := func(scale float64) []*allpass {
		var aps []*allpass
		for _, s := range []float64{0.0047, 0.0036, 0.0127, 0.0093} {
			aps = append(aps, &allpass{d: newDelay(int(s * scale * sr)), g: 0.7})
		}
		return aps
	}
	dl, dr := diff(1), diff(1.13)
	pre := int(p.predelay * sr)
	wl, wr = make([]float64, len(l)), make([]float64, len(l))
	var v [8]float64
	for i := range l {
		var inL, inR float64
		if j := i - pre; j >= 0 {
			inL, inR = l[j], r[j]
		}
		for _, a := range dl {
			inL = a.tick(inL)
		}
		for _, a := range dr {
			inR = a.tick(inR)
		}
		for k := range v {
			v[k] = lines[k].out()
		}
		wl[i] = (v[0] - v[2] + v[4] - v[6]) * 0.5
		wr[i] = (v[1] - v[3] + v[5] - v[7]) * 0.5
		hadamard8(&v)
		for k := range v {
			in := inL
			if k&1 == 1 {
				in = inR
			}
			lines[k].push(lp[k].lp(v[k]*gains[k]) + in*0.35)
		}
	}
	return wl, wr
}

// hadamard8 mixes eight lines losslessly (orthonormal butterflies).
func hadamard8(v *[8]float64) {
	for h := 1; h < 8; h <<= 1 {
		for i := 0; i < 8; i += h * 2 {
			for j := i; j < i+h; j++ {
				a, b := v[j], v[j+h]
				v[j], v[j+h] = a+b, a-b
			}
		}
	}
	const s = 0.35355339059327373 // 1/sqrt(8)
	for i := range v {
		v[i] *= s
	}
}
