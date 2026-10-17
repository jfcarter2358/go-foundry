// Command genstepsounds synthesises the soft-ground step sounds the ground
// trails' footsteps play (internal/sim/footsteps.go, docs/trails/SPEC.md).
//
//	go run ./cmd/genstepsounds [-out resources/sounds]
//
// Outputs, 44.1 kHz mono 16-bit WAV:
//
//	step_sand.wav   a soft scuff: a boot's thump under a short rush of
//	                filtered noise, with a few grains ticking through it
//	step_snow.wav   a crunch: the same thump under a dense run of tiny
//	                crackles — packed crystals giving way, a few dozen
//	                breaks in a fifth of a second — rising and falling as
//	                the foot rolls heel to toe
//
// Each step is played with its pitch varied (±10%), so one file of each is
// enough not to sound like a loop. Deterministic: the same seed writes the
// same bytes.
package main

import (
	"encoding/binary"
	"flag"
	"log"
	"math"
	"math/rand"
	"os"
	"path/filepath"
)

const rate = 44100

func main() {
	out := flag.String("out", filepath.Join("resources", "sounds"), "directory to write into")
	flag.Parse()
	for name, s := range map[string][]float64{
		"step_sand": sand(rand.New(rand.NewSource(11))),
		"step_snow": snow(rand.New(rand.NewSource(17))),
	} {
		path := filepath.Join(*out, name+".wav")
		if err := writeWAV(path, s); err != nil {
			log.Fatal(err)
		}
		log.Printf("wrote %s (%.0f ms)", path, 1000*float64(len(s))/rate)
	}
}

// thump is the boot meeting the ground: a low sine dropping in pitch under a
// fast decay.
func thump(n int, f0, f1, decay, gain float64) []float64 {
	s := make([]float64, n)
	ph := 0.0
	for i := range s {
		t := float64(i) / rate
		f := f1 + (f0-f1)*math.Exp(-t*40)
		ph += 2 * math.Pi * f / rate
		s[i] = gain * math.Sin(ph) * math.Exp(-t/decay) * math.Min(t/0.002, 1)
	}
	return s
}

// onePole is a one-pole low-pass at fc (Hz), in place.
func onePole(s []float64, fc float64) {
	a := math.Exp(-2 * math.Pi * fc / rate)
	y := 0.0
	for i, x := range s {
		y = (1-a)*x + a*y
		s[i] = y
	}
}

// highPass is a one-pole high-pass at fc (Hz), in place.
func highPass(s []float64, fc float64) {
	a := math.Exp(-2 * math.Pi * fc / rate)
	lp := 0.0
	for i, x := range s {
		lp = (1-a)*x + a*lp
		s[i] = x - lp
	}
}

// grain adds a crackle at sample at: a short burst of noise rung through a
// decaying resonance at f (a crystal breaking, a grain ticking).
func grain(s []float64, r *rand.Rand, at int, f, dur, gain float64) {
	n := int(dur * rate)
	w := 2 * math.Pi * f / rate
	y1, y2 := 0.0, 0.0
	q := math.Exp(-1 / (dur * rate * 0.3))
	for i := 0; i < n && at+i < len(s); i++ {
		x := 0.0
		if i < 24 {
			x = (r.Float64()*2 - 1) * (1 - float64(i)/24)
		}
		y := x + 2*q*math.Cos(w)*y1 - q*q*y2
		y2, y1 = y1, y
		s[at+i] += gain * y * 0.12
	}
}

func sand(r *rand.Rand) []float64 {
	n := int(0.28 * rate)
	s := thump(n, 140, 70, 0.03, 0.16)
	// The scuff: noise under an envelope that swells over 15 ms and dies
	// over a tenth of a second, low-passed soft and high-passed thin.
	noise := make([]float64, n)
	for i := range noise {
		t := float64(i) / rate
		env := math.Min(t/0.015, 1) * math.Exp(-math.Max(t-0.015, 0)/0.07)
		noise[i] = (r.Float64()*2 - 1) * env
	}
	onePole(noise, 2600)
	onePole(noise, 3400)
	highPass(noise, 380)
	for i := range s {
		s[i] += 0.55 * noise[i]
	}
	// A few grains ticking through it.
	for k := 0; k < 14; k++ {
		at := int((0.005 + 0.12*math.Pow(r.Float64(), 1.6)) * rate)
		grain(s, r, at, 2500+4000*r.Float64(), 0.004, 0.8+r.Float64())
	}
	return finish(s)
}

func snow(r *rand.Rand) []float64 {
	n := int(0.34 * rate)
	s := thump(n, 120, 60, 0.04, 0.3)
	// The crunch: crackles at a rate that rises with the heel and falls with
	// the toe, each a crystal giving way somewhere between 1.5 and 7 kHz.
	for t := 0.0; t < 0.24; {
		density := 260 * math.Sin(math.Pi*math.Min(t/0.22, 1)) // breaks a second
		if density < 20 {
			density = 20
		}
		t += r.ExpFloat64() / density
		at := int(t * rate)
		g := (0.35 + 0.65*r.Float64()) * (1.1 - t*2.2)
		grain(s, r, at, 1500+5500*r.Float64(), 0.003+0.004*r.Float64(), 3*math.Max(g, 0.1))
	}
	// Under it, the squeak of the pack compressing: band-limited noise.
	body := make([]float64, n)
	for i := range body {
		t := float64(i) / rate
		env := math.Min(t/0.03, 1) * math.Exp(-math.Max(t-0.03, 0)/0.09)
		body[i] = (r.Float64()*2 - 1) * env
	}
	onePole(body, 1800)
	highPass(body, 500)
	for i := range s {
		s[i] += 0.5 * body[i]
	}
	return finish(s)
}

// finish removes any DC, fades the tail and normalises to -3 dBFS.
func finish(s []float64) []float64 {
	highPass(s, 30)
	fade := int(0.03 * rate)
	for i := 0; i < fade && i < len(s); i++ {
		s[len(s)-1-i] *= float64(i) / float64(fade)
	}
	peak := 0.0
	for _, v := range s {
		peak = math.Max(peak, math.Abs(v))
	}
	if peak > 0 {
		g := 0.708 / peak
		for i := range s {
			s[i] *= g
		}
	}
	return s
}

func writeWAV(path string, s []float64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	data := make([]byte, 2*len(s))
	for i, v := range s {
		binary.LittleEndian.PutUint16(data[2*i:], uint16(int16(math.Round(math.Max(-1, math.Min(1, v))*32767))))
	}
	hdr := make([]byte, 44)
	copy(hdr[0:], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:], uint32(36+len(data)))
	copy(hdr[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(hdr[16:], 16)
	binary.LittleEndian.PutUint16(hdr[20:], 1) // PCM
	binary.LittleEndian.PutUint16(hdr[22:], 1) // mono
	binary.LittleEndian.PutUint32(hdr[24:], rate)
	binary.LittleEndian.PutUint32(hdr[28:], rate*2)
	binary.LittleEndian.PutUint16(hdr[32:], 2)
	binary.LittleEndian.PutUint16(hdr[34:], 16)
	copy(hdr[36:], "data")
	binary.LittleEndian.PutUint32(hdr[40:], uint32(len(data)))
	if _, err := f.Write(hdr); err != nil {
		return err
	}
	_, err = f.Write(data)
	return err
}
