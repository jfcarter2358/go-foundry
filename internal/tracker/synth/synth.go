// Package synth generates Foundry's stock sample library.
//
// synth.go — recipes, the registry, and the finishing pass every sample goes
// through.
//
// Every sample in resources/sounds/samples/ is a recipe in code rather than a
// recording, for three reasons: the library carries no licensing baggage, it
// regenerates byte-identically (each recipe owns a fixed-seed PRNG and never
// reads the clock), and the editor's Generate dialog can re-run a recipe with
// its knobs turned to make a variation.
//
// A recipe's body only makes sound. Everything that must be true of every
// sample — DC removed, click-free loop points, trimmed silence, peak at
// -1 dBFS, tags and description — is done once here, in finish, so a recipe
// cannot forget it.
package synth

import (
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"path/filepath"

	"foundry/internal/tracker"
)

// Param is one knob a recipe exposes.
type Param struct {
	Name              string
	Default, Min, Max float32
	Help              string
}

// Recipe is one sample's generator.
type Recipe struct {
	Name     string // e.g. "kick_808"
	Category string // the library folder: drums, bass, leads, ...
	Desc     string // one character line, also the sample's Description
	Params   []Param
	// Generate renders the sample at rate. Missing params take defaults;
	// out-of-range ones are clamped.
	Generate func(rate int, p map[string]float32) *tracker.Sample

	base string   // base note ("C-4"); "" for unpitched one-shots (C-4)
	tags []string // descriptors beyond the category
	body func(c *ctx) *out
}

// Categories are the library's folders, in display order.
var Categories = []string{"drums", "bass", "leads", "pads", "keys", "plucked", "winds", "chip", "vocal", "fx"}

// Recipes is the whole library in its numbered order.
var Recipes []Recipe

func init() {
	for _, group := range [][]Recipe{
		drumRecipes(), bassRecipes(), leadRecipes(), padRecipes(), keyRecipes(),
		pluckedRecipes(), windRecipes(), chipRecipes(), vocalRecipes(), fxRecipes(),
	} {
		Recipes = append(Recipes, group...)
	}
	for i := range Recipes {
		r := &Recipes[i]
		r.Generate = func(rate int, p map[string]float32) *tracker.Sample { return r.generate(rate, p) }
	}
}

// ByName finds a recipe by its bare name ("kick_808") or library name
// ("drums/kick_808").
func ByName(name string) *Recipe {
	for i := range Recipes {
		if Recipes[i].Name == name || Recipes[i].LibraryName() == name {
			return &Recipes[i]
		}
	}
	return nil
}

// Defaults is a fresh map of every knob at its default.
func (r *Recipe) Defaults() map[string]float32 {
	m := make(map[string]float32, len(r.Params))
	for _, p := range r.Params {
		m[p.Name] = p.Default
	}
	return m
}

// LibraryName is the name songs use for the sample.
func (r *Recipe) LibraryName() string { return r.Category + "/" + r.Name }

// GenerateAll renders every recipe into dir/<category>/<name>.wav + .fsmp.
func GenerateAll(dir string, rate int, progress func(i int, name string)) error {
	for i := range Recipes {
		r := &Recipes[i]
		if progress != nil {
			progress(i, r.LibraryName())
		}
		if err := writeRecipe(dir, rate, r, nil); err != nil {
			return err
		}
	}
	return nil
}

func writeRecipe(dir string, rate int, r *Recipe, p map[string]float32) error {
	s := r.Generate(rate, p)
	base := filepath.Join(dir, r.Category, r.Name)
	if err := os.MkdirAll(filepath.Dir(base), 0o755); err != nil {
		return err
	}
	if err := tracker.SaveSampleTo(base, s); err != nil {
		return fmt.Errorf("%s: %w", r.LibraryName(), err)
	}
	return nil
}

// ---------- rendering context ----------

// ctx is what a recipe body sees: the rate, its knobs, its own PRNG and its
// base frequency.
type ctx struct {
	rate int
	sr   float64
	p    map[string]float32
	r    *Recipe
	rng  *rng
	f0   float64 // the base note's frequency at A4 = 440
}

// v reads a knob: the caller's value clamped to range, else the default.
func (c *ctx) v(name string) float64 {
	for _, p := range c.r.Params {
		if p.Name != name {
			continue
		}
		val, ok := c.p[name]
		if !ok || val != val {
			return float64(p.Default)
		}
		return float64(clampf(val, p.Min, p.Max))
	}
	panic("synth: recipe " + c.r.Name + " reads unknown param " + name)
}

// n is a duration in samples.
func (c *ctx) n(sec float64) int { return int(math.Round(sec * c.sr)) }

// t is sample index i in seconds.
func (c *ctx) t(i int) float64 { return float64(i) / c.sr }

// out is a recipe body's result, before finishing.
type out struct {
	ch [][]float64 // one channel, or two (L, R)
	// loop asks for a loop: start is the earliest frame it may begin and
	// length its exact frame count (a recipe that designs its partials to
	// repeat every length frames gets a seamless loop for free).
	loop          bool
	start, length int
	xfade         float64 // seconds; 0 = 30 ms
	equalPower    bool    // crossfade uncorrelated material (noise beds)
	searchWindow  int     // frames to look for a quiet upward crossing; 0 = auto
}

func mono(x []float64) *out      { return &out{ch: [][]float64{x}} }
func stereo(l, r []float64) *out { return &out{ch: [][]float64{l, r}} }
func (o *out) looped(p plan) *out {
	o.loop, o.start, o.length, o.searchWindow = true, p.start, p.L, p.win
	return o
}
func (o *out) fade(sec float64) *out { o.xfade = sec; return o }
func (o *out) noisy() *out           { o.equalPower = true; return o }

const peakTarget = 0.891 // -1 dBFS

func (r *Recipe) generate(rate int, p map[string]float32) *tracker.Sample {
	if p == nil {
		p = map[string]float32{}
	}
	h := fnv.New64a()
	h.Write([]byte(r.Name))
	c := &ctx{rate: rate, sr: float64(rate), p: p, r: r, rng: newRNG(h.Sum64())}
	base := r.base
	if base == "" {
		base = "C-4"
	}
	note, err := tracker.ParseNote(base)
	if err != nil {
		panic("synth: " + r.Name + ": " + err.Error())
	}
	c.f0 = noteHz(note)
	o := r.body(c)
	s := finish(c, o)
	s.BaseNote = note
	return s
}

// noteHz is a tracker note's frequency: A-4 (58) is 440 Hz.
func noteHz(n uint8) float64 { return 440 * math.Pow(2, (float64(n)-58)/12) }

// finish turns a body's raw channels into a library sample.
func finish(c *ctx, o *out) *tracker.Sample {
	chs := o.ch
	for _, x := range chs {
		for i, v := range x {
			if v != v || math.IsInf(v, 0) {
				x[i] = 0
			}
		}
	}
	ls, le := 0, 0
	if o.loop {
		ls, le = loopify(c, o)
		// A periodic loop's mean is its DC exactly; removing it from the
		// whole sample keeps the attack and the loop on the same baseline.
		for _, x := range chs {
			var m float64
			for i := ls; i < le; i++ {
				m += x[i]
			}
			m /= float64(le - ls)
			for i := range x {
				x[i] -= m
			}
		}
		// Hermite interpolation reads two frames past the end; give it the
		// frames the loop would play next so the seam interpolates cleanly.
		for k, x := range chs {
			x = x[:le]
			for j := 0; j < 4; j++ {
				x = append(x, x[ls+j])
			}
			chs[k] = x
		}
	} else {
		for _, x := range chs {
			dcBlock(x, c.sr, 5)
		}
		trimTail(c, chs)
	}
	normalize(chs, peakTarget)

	frames := len(chs[0])
	data := make([]float32, frames*len(chs))
	for i := 0; i < frames; i++ {
		for k, x := range chs {
			data[i*len(chs)+k] = float32(x[i])
		}
	}
	s := &tracker.Sample{
		Name: c.r.LibraryName(), Data: data, Channels: len(chs), Rate: c.rate,
		Volume: 1, Description: c.r.Desc,
		Tags: append([]string{c.r.Category}, c.r.tags...),
	}
	if o.loop {
		s.Loop, s.LoopStart, s.LoopEnd = tracker.LoopForward, ls, le
	}
	return s
}

// dcBlock is a one-pole high-pass, run forward. At 5 Hz it leaves a 45 Hz
// kick's body alone and takes out the offset a fast pitch sweep's lopsided
// first cycle leaves behind.
func dcBlock(x []float64, sr, hz float64) {
	r := math.Exp(-2 * math.Pi * hz / sr)
	var x1, y1 float64
	for i, v := range x {
		y := v - x1 + r*y1
		x1, y1 = v, y
		x[i] = y
	}
}

// trimTail cuts a one-shot where it falls below -80 dB of its peak and fades
// the last few milliseconds, so the file ends on silence without carrying
// seconds of it.
func trimTail(c *ctx, chs [][]float64) {
	peak := peakOf(chs)
	if peak == 0 {
		return
	}
	last := 0
	for _, x := range chs {
		for i := len(x) - 1; i > last; i-- {
			if math.Abs(x[i]) > peak*1e-4 {
				last = i
				break
			}
		}
	}
	end := last + c.n(0.005)
	if end > len(chs[0]) {
		end = len(chs[0])
	}
	f := c.n(0.01)
	if f > end/4 {
		f = end / 4
	}
	for k, x := range chs {
		x = x[:end]
		for j := 0; j < f; j++ {
			g := float64(j) / float64(f)
			x[end-1-j] *= g * g
		}
		chs[k] = x
	}
}

func peakOf(chs [][]float64) float64 {
	var p float64
	for _, x := range chs {
		for _, v := range x {
			if a := math.Abs(v); a > p {
				p = a
			}
		}
	}
	return p
}

func normalize(chs [][]float64, target float64) {
	p := peakOf(chs)
	if p == 0 {
		return
	}
	g := target / p
	for _, x := range chs {
		for i := range x {
			x[i] *= g
		}
	}
}

func clampf(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// rec builds a recipe; the per-category files read as a table of these.
func rec(name, cat, base, desc string, tags []string, params []Param, body func(c *ctx) *out) Recipe {
	return Recipe{Name: name, Category: cat, Desc: desc, Params: params, base: base, tags: tags, body: body}
}

// par is shorthand for a Param in the recipe tables.
func par(name string, def, lo, hi float32, help string) Param {
	return Param{Name: name, Default: def, Min: lo, Max: hi, Help: help}
}
