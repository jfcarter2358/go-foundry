package fx

import (
	"math"
	"math/rand"
	"testing"
)

// noise is full-scale stereo white noise with a few hot transients: the
// hardest ordinary input an effect sees.
func noise(r *rand.Rand, frames int) []float32 {
	b := make([]float32, frames*2)
	for i := range b {
		b[i] = float32(r.Float64()*2 - 1)
	}
	for k := 0; k < 8; k++ {
		f := r.Intn(frames)
		b[f*2], b[f*2+1] = 4, -4
	}
	return b
}

// TestExtremeSettingsStayFinite runs every effect at every corner of its
// parameter space (each parameter at its minimum, maximum and default,
// the rest random) and then under random automation — glides of every
// length, switches flipped mid-block, blocks of odd sizes — and asks that
// nothing it produces is NaN, infinite or runs away.
func TestExtremeSettingsStayFinite(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	in := noise(r, testRate/2)
	check := func(k Kind, what string, out []float32) {
		t.Helper()
		peak := 0.0
		for i, v := range out {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("%v %s: sample %d is %v", k, what, i, v)
			}
			peak = math.Max(peak, math.Abs(float64(v)))
		}
		// Input peaks at 4 and an EQ with every band, both shelves and
		// its output at +24 dB on one frequency is asked for 168 dB — but
		// nothing should run away past what it was asked.
		if peak > 1e8 {
			t.Errorf("%v %s: peak %g ran away", k, what, peak)
		}
	}
	for k := Kind(0); k < numKinds; k++ {
		sp := k.Spec()
		for i := range sp.Params {
			for _, corner := range []float32{sp.Params[i].Min, sp.Params[i].Max} {
				e := New(k, testRate, Insert)
				for j := range sp.Params {
					ps := &sp.Params[j]
					e.SetParam(j, ps.Min+float32(r.Float64())*(ps.Max-ps.Min), 0)
				}
				e.SetParam(i, corner, 0)
				check(k, sp.Params[i].Name+" at its corner", run(e, in))
			}
		}
		// Every parameter at its maximum at once, then every one at its
		// minimum: the settings most likely to make feedback ring.
		for _, hi := range []bool{true, false} {
			e := New(k, testRate, Bus)
			for j := range sp.Params {
				v := sp.Params[j].Min
				if hi {
					v = sp.Params[j].Max
				}
				e.SetParam(j, v, 0)
			}
			out := run(e, in)
			check(k, "all at one end", out)
			// Then silence: whatever rings must die away, never grow.
			tail := run(e, make([]float32, 4*testRate))
			check(k, "ringing out", tail)
			if p, q := peakFrom(out, 0), peakFrom(tail, 3*testRate); q > p*0.5 && q > 1e-3 {
				t.Errorf("%v all at one end (max %v): the tail's last second peaks at %g against %g while playing", k, hi, q, p)
			}
		}
		// Random automation in blocks of odd lengths.
		e := New(k, testRate, Insert)
		out := append([]float32(nil), in...)
		for f := 0; f < len(out)/2; {
			n := 1 + r.Intn(700)
			if f+n > len(out)/2 {
				n = len(out)/2 - f
			}
			for c := 0; c < 2; c++ {
				j := r.Intn(len(sp.Params))
				ps := &sp.Params[j]
				e.SetParam(j, ps.Min+float32(r.Float64())*(ps.Max-ps.Min), r.Intn(3000))
			}
			e.SetTempo(float32(30 + r.Intn(300)))
			e.Process(out[f*2 : (f+n)*2])
			f += n
		}
		check(k, "under random automation", out)
		// Out-of-range and NaN settings are clamped, never stored.
		for j := range sp.Params {
			e.SetParam(j, float32(math.NaN()), 10)
			if v := e.Param(j); v != v {
				t.Errorf("%v.%s took a NaN", k, sp.Params[j].Name)
			}
			e.SetParam(j, 1e9, 0)
			if v := e.Param(j); v > sp.Params[j].Max {
				t.Errorf("%v.%s took %g past its max %g", k, sp.Params[j].Name, v, sp.Params[j].Max)
			}
		}
		check(k, "after clamped settings", run(e, in))
	}
}

// TestChainSurvivesExtremes runs a chain of one of everything with
// bypasses toggled every block.
func TestChainSurvivesExtremes(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	c := NewChain(int(numKinds))
	for k := Kind(0); k < numKinds; k++ {
		c.Add(New(k, testRate, Insert), false)
	}
	in := noise(r, testRate/2)
	for f := 0; f < len(in)/2; f += 333 {
		n := min(333, len(in)/2-f)
		c.SetBypass(r.Intn(c.Len()), r.Intn(2) == 0)
		c.Process(in[f*2 : (f+n)*2])
	}
	for i, v := range in {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Fatalf("sample %d is %v", i, v)
		}
	}
}
