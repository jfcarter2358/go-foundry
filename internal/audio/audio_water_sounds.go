package audio

// audio_water_sounds.go — the water clips, synthesised
// (docs/terrain-paint/PHASE_W5.md, items 3 and 5).
//
// No CC0 water recordings were at hand when phase W5 was built, so the
// river, rapids, falls and lapping loops and the three splashes the engine
// ships under resources/sounds/ (water_*.wav) are GENERATED here, from
// seeded noise and decaying sines — deterministic, so the files can always
// be made again and a test can tell when they drift from the code. To
// regenerate after changing a recipe:
//
//	FOUNDRY_WRITE_WATER_SOUNDS=1 go test ./internal/audio/ -run TestWaterSounds
//
// The recipes, briefly:
//
//   - water_river: the hiss of a stream (noise through a two-pole low-pass
//     at ~1.4 kHz, slowly swelling) and its bubbling — short sine chirps,
//     each rising in pitch as a bubble's resonance does when it forms at the
//     surface, scattered at random.
//   - water_rapids: brighter, busier water: noise band-passed higher, more
//     and quicker bubbles, a faster swell.
//   - water_falls: a waterfall's roar: deep brown noise with a band of mid
//     hiss, steady.
//   - water_lap: still water against a shore: soft slaps every second or two
//     (a burst of low-passed noise with a quick attack and a slow tail) and a
//     few lazy bubbles.
//   - water_step / water_splash / water_impact: one-shots for a foot in the
//     shallows, a body dropping in, a small thing striking the surface — a
//     noise burst with a falling filter, and a scatter of bubbles after.
//
// Loops are made SEAMLESS: rendered a little longer than their length, the
// overrun cross-faded into the head, and every bubble wrapped round the end,
// so the last sample runs into the first with no click. Loops are 16 kHz
// (water is noise, and noise past 8 kHz is not missed; it halves the files),
// one-shots 22.05 kHz.

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/rand"
)

// WaterSound is one generated clip.
type WaterSound struct {
	Name    string
	Rate    int
	Loop    bool
	Samples []float32
}

// WaterSoundNames lists the generated clips, in a fixed order.
var WaterSoundNames = []string{"water_river", "water_rapids", "water_falls", "water_lap",
	"water_step", "water_splash", "water_impact"}

// waterLoopRate and waterShotRate are the loops' and one-shots' rates.
const (
	waterLoopRate = 16000
	waterShotRate = 22050
)

// GenerateWaterSound renders one named clip (the file comment); ok is false
// for a name it does not know.
func GenerateWaterSound(name string) (WaterSound, bool) {
	switch name {
	case "water_river":
		return waterLoop(name, 3, 11, waterLoopSpec{lp: 1400, hp: 120, noise: 0.32, swell: 0.25, swellHz: 0.33,
			bubbles: 34, bubbleLo: 380, bubbleHi: 1300, bubbleGain: 0.22, bubbleLen: 0.05}), true
	case "water_rapids":
		return waterLoop(name, 3, 23, waterLoopSpec{lp: 3200, hp: 300, noise: 0.42, swell: 0.3, swellHz: 0.66,
			bubbles: 90, bubbleLo: 500, bubbleHi: 2200, bubbleGain: 0.2, bubbleLen: 0.035}), true
	case "water_falls":
		return waterLoop(name, 3, 37, waterLoopSpec{lp: 520, hp: 25, noise: 0.62, swell: 0.12, swellHz: 0.33,
			mid: 0.16, midLo: 900, midHi: 3000, bubbles: 30, bubbleLo: 250, bubbleHi: 700, bubbleGain: 0.1, bubbleLen: 0.06}), true
	case "water_lap":
		return waterLap(name, 5, 41), true
	case "water_step":
		return waterShot(name, 0.45, 53, waterShotSpec{gain: 0.6, lpFrom: 3500, lpTo: 600, decay: 0.09, bubbles: 6, bubbleLo: 500, bubbleHi: 1400}), true
	case "water_splash":
		return waterShot(name, 1.3, 67, waterShotSpec{gain: 0.85, lpFrom: 5000, lpTo: 400, decay: 0.28, bubbles: 26, bubbleLo: 300, bubbleHi: 1200, body: 0.5}), true
	case "water_impact":
		return waterShot(name, 0.55, 79, waterShotSpec{gain: 0.55, lpFrom: 6000, lpTo: 900, decay: 0.05, bubbles: 9, bubbleLo: 700, bubbleHi: 1800, plop: 0.5}), true
	}
	return WaterSound{}, false
}

// waterLoopSpec is a flowing-water loop's recipe.
type waterLoopSpec struct {
	lp, hp             float64 // the hiss's band, Hz
	noise              float64 // its level
	swell, swellHz     float64 // slow level swell: depth, rate (a whole number of cycles a loop)
	mid, midLo, midHi  float64 // a second band of hiss (the falls)
	bubbles            int     // how many bubbles a loop
	bubbleLo, bubbleHi float64 // their pitch range, Hz
	bubbleGain         float64
	bubbleLen          float64 // their length, seconds
}

// onePole is a one-pole low-pass coefficient for corner fc at rate.
func onePole(fc float64, rate int) float64 {
	return 1 - math.Exp(-2*math.Pi*fc/float64(rate))
}

// bandNoise is n samples of white noise through a two-pole low-pass at lp
// and a one-pole high-pass at hp.
func bandNoise(r *rand.Rand, n, rate int, lp, hp float64) []float64 {
	out := make([]float64, n)
	a := onePole(lp, rate)
	h := onePole(hp, rate)
	var s1, s2, lo float64
	for i := 0; i < n; i++ {
		x := r.Float64()*2 - 1
		s1 += (x - s1) * a
		s2 += (s1 - s2) * a
		lo += (s2 - lo) * h
		out[i] = s2 - lo
	}
	return normalise(out)
}

// normalise scales v to a unit RMS (a few loudest samples may pass 1).
func normalise(v []float64) []float64 {
	s := 0.0
	for _, x := range v {
		s += x * x
	}
	if s == 0 {
		return v
	}
	k := 1 / math.Sqrt(s/float64(len(v)))
	for i := range v {
		v[i] *= k
	}
	return v
}

// addBubble adds one bubble at sample at (wrapped round n when wrap): a
// sine chirping up from f by half again, decaying over its length.
func addBubble(dst []float64, at int, rate int, f, length, gain float64, wrap bool) {
	m := int(length * float64(rate) * 3)
	ph := 0.0
	for k := 0; k < m; k++ {
		t := float64(k) / float64(rate)
		env := math.Exp(-t/length) * math.Min(1, t*2000)
		freq := f * (1 + 0.5*math.Min(t/length, 2))
		ph += 2 * math.Pi * freq / float64(rate)
		i := at + k
		if i >= len(dst) {
			if !wrap {
				break
			}
			i %= len(dst)
		}
		dst[i] += gain * env * math.Sin(ph)
	}
}

// seamless cross-fades the fade-long overrun past n into the head and
// returns the first n samples.
func seamless(v []float64, n, fade int) []float64 {
	out := make([]float64, n)
	copy(out, v[:n])
	for i := 0; i < fade && n+i < len(v); i++ {
		w := float64(i) / float64(fade)
		out[i] = v[i]*w + v[n+i]*(1-w)
	}
	return out
}

// toClip scales v under peak 0.85 and converts it.
func toClip(name string, rate int, loop bool, v []float64) WaterSound {
	p := 0.0
	for _, x := range v {
		p = math.Max(p, math.Abs(x))
	}
	k := 1.0
	if p > 0.85 {
		k = 0.85 / p
	}
	s := make([]float32, len(v))
	for i, x := range v {
		s[i] = float32(x * k)
	}
	return WaterSound{Name: name, Rate: rate, Loop: loop, Samples: s}
}

// waterLoop renders a flowing-water loop of secs seconds.
func waterLoop(name string, secs float64, seed int64, sp waterLoopSpec) WaterSound {
	r := rand.New(rand.NewSource(seed))
	rate := waterLoopRate
	n := int(secs * float64(rate))
	fade := rate / 4
	total := n + fade
	hiss := bandNoise(r, total, rate, sp.lp, sp.hp)
	var mid []float64
	if sp.mid > 0 {
		mid = bandNoise(r, total, rate, sp.midHi, sp.midLo)
	}
	v := make([]float64, total)
	for i := range v {
		t := float64(i) / float64(rate)
		// A whole number of swell cycles a loop: it meets itself.
		cyc := math.Round(sp.swellHz * secs)
		sw := 1 - sp.swell*0.5*(1+math.Sin(2*math.Pi*cyc*t/secs+1.3))
		v[i] = sp.noise * hiss[i] * sw
		if mid != nil {
			v[i] += sp.mid * mid[i]
		}
	}
	body := seamless(v, n, fade)
	for b := 0; b < sp.bubbles; b++ {
		at := r.Intn(n)
		f := sp.bubbleLo * math.Pow(sp.bubbleHi/sp.bubbleLo, r.Float64())
		addBubble(body, at, rate, f, sp.bubbleLen*(0.6+0.8*r.Float64()), sp.bubbleGain*(0.4+0.6*r.Float64()), true)
	}
	return toClip(name, rate, true, body)
}

// waterLap renders still water lapping at a shore.
func waterLap(name string, secs float64, seed int64) WaterSound {
	r := rand.New(rand.NewSource(seed))
	rate := waterLoopRate
	n := int(secs * float64(rate))
	v := make([]float64, n)
	// A faint bed of low hiss under the slaps.
	bed := bandNoise(r, n+rate/4, rate, 500, 60)
	bedLoop := seamless(bed, n, rate/4)
	for i := range v {
		v[i] = 0.05 * bedLoop[i]
	}
	// The slaps, wrapped round the end.
	t := 0.2
	for t < secs {
		at := int(t * float64(rate))
		gain := 0.35 + 0.3*r.Float64()
		burst := bandNoise(r, rate, rate, 700+500*r.Float64(), 80)
		for k := 0; k < len(burst); k++ {
			tt := float64(k) / float64(rate)
			env := math.Min(1, tt/0.04) * math.Exp(-tt/0.22)
			v[(at+k)%n] += gain * env * burst[k] * 0.5
		}
		for b := 0; b < 3; b++ {
			addBubble(v, (at+r.Intn(rate/3))%n, rate, 300+400*r.Float64(), 0.05, 0.08, true)
		}
		t += 1.1 + 0.9*r.Float64()
	}
	return toClip(name, rate, true, v)
}

// waterShotSpec is a splash's recipe.
type waterShotSpec struct {
	gain               float64
	lpFrom, lpTo       float64 // the burst's filter sweep, Hz
	decay              float64 // its tail, seconds
	bubbles            int
	bubbleLo, bubbleHi float64
	body               float64 // a low thump under a body's splash
	plop               float64 // a pitched plop (a small thing's)
}

// waterShot renders a one-shot splash of secs seconds.
func waterShot(name string, secs float64, seed int64, sp waterShotSpec) WaterSound {
	r := rand.New(rand.NewSource(seed))
	rate := waterShotRate
	n := int(secs * float64(rate))
	v := make([]float64, n)
	var s1, s2 float64
	for i := 0; i < n; i++ {
		t := float64(i) / float64(rate)
		fc := sp.lpTo + (sp.lpFrom-sp.lpTo)*math.Exp(-t/(sp.decay*1.5))
		a := onePole(fc, rate)
		x := r.Float64()*2 - 1
		s1 += (x - s1) * a
		s2 += (s1 - s2) * a
		env := math.Min(1, t/0.004) * math.Exp(-t/sp.decay)
		v[i] = sp.gain * env * s2 * 3
		if sp.body > 0 {
			v[i] += sp.body * math.Exp(-t/0.12) * math.Min(1, t/0.01) * math.Sin(2*math.Pi*(70+40*math.Exp(-t/0.05))*t)
		}
		if sp.plop > 0 {
			v[i] += sp.plop * math.Exp(-t/0.06) * math.Min(1, t/0.002) * math.Sin(2*math.Pi*(420+500*math.Min(t/0.06, 1))*t)
		}
	}
	for b := 0; b < sp.bubbles; b++ {
		at := int((0.02 + r.Float64()*secs*0.6) * float64(rate))
		f := sp.bubbleLo * math.Pow(sp.bubbleHi/sp.bubbleLo, r.Float64())
		addBubble(v, at, rate, f, 0.03+0.04*r.Float64(), 0.15*(0.4+0.6*r.Float64()), false)
	}
	// A short fade at the very end: nothing is cut off mid-sample.
	for i := 0; i < rate/50 && i < n; i++ {
		v[n-1-i] *= float64(i) / float64(rate/50)
	}
	return toClip(name, rate, false, v)
}

// EncodeWAV16 is a mono 16-bit PCM WAV file of s at rate.
func EncodeWAV16(s []float32, rate int) []byte {
	var b bytes.Buffer
	data := len(s) * 2
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	w(uint32(36 + data))
	b.WriteString("WAVEfmt ")
	w(uint32(16))
	w(uint16(1)) // PCM
	w(uint16(1)) // mono
	w(uint32(rate))
	w(uint32(rate * 2))
	w(uint16(2))
	w(uint16(16))
	b.WriteString("data")
	w(uint32(data))
	for _, x := range s {
		v := math.Round(float64(x) * 32767)
		v = math.Max(-32768, math.Min(32767, v))
		w(int16(v))
	}
	return b.Bytes()
}
