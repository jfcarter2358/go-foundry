package synth

import (
	"math"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"foundry/internal/tracker"
)

// The whole library is rendered once and shared by every test.
var (
	libOnce sync.Once
	lib     map[string]*tracker.Sample
	libTime map[string]time.Duration
)

func library(t *testing.T) map[string]*tracker.Sample {
	t.Helper()
	libOnce.Do(func() {
		lib = map[string]*tracker.Sample{}
		libTime = map[string]time.Duration{}
		var wg sync.WaitGroup
		var mu sync.Mutex
		sem := make(chan struct{}, 8)
		for i := range Recipes {
			r := &Recipes[i]
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				start := time.Now()
				s := r.Generate(Rate, nil)
				mu.Lock()
				lib[r.Name] = s
				libTime[r.Name] = time.Since(start)
				mu.Unlock()
			}()
		}
		wg.Wait()
	})
	return lib
}

func TestRegistry(t *testing.T) {
	if len(Recipes) != 128 {
		t.Fatalf("%d recipes, want 128", len(Recipes))
	}
	cats := map[string]bool{}
	for _, c := range Categories {
		cats[c] = true
	}
	seen := map[string]bool{}
	for _, r := range Recipes {
		if seen[r.Name] {
			t.Errorf("duplicate recipe %q", r.Name)
		}
		seen[r.Name] = true
		if !cats[r.Category] {
			t.Errorf("%s: bad category %q", r.Name, r.Category)
		}
		if r.Desc == "" || r.Generate == nil {
			t.Errorf("%s: missing description or generator", r.Name)
		}
		if len(r.Params) < 2 || len(r.Params) > 5 {
			t.Errorf("%s: %d params, want 2-5", r.Name, len(r.Params))
		}
		for _, p := range r.Params {
			if p.Default < p.Min || p.Default > p.Max || p.Help == "" {
				t.Errorf("%s.%s: default %v outside [%v, %v] or no help", r.Name, p.Name, p.Default, p.Min, p.Max)
			}
		}
		if got := ByName(r.LibraryName()); got == nil || got.Name != r.Name {
			t.Errorf("ByName(%q) failed", r.LibraryName())
		}
	}
	if Recipes[0].Name != "kick_808" || Recipes[127].Name != "fx_vinyl" {
		t.Errorf("recipe order: first %q, last %q", Recipes[0].Name, Recipes[127].Name)
	}
}

// channel extracts one channel of a sample as float64.
func channel(s *tracker.Sample, ch int) []float64 {
	n := s.Frames()
	x := make([]float64, n)
	for i := range x {
		x[i] = float64(s.Data[i*s.Channels+ch])
	}
	return x
}

func TestEverySampleIsSane(t *testing.T) {
	lib := library(t)
	for _, r := range Recipes {
		s := lib[r.Name]
		if s == nil || s.Frames() < 100 {
			t.Errorf("%s: empty", r.Name)
			continue
		}
		if s.Rate != Rate || (s.Channels != 1 && s.Channels != 2) {
			t.Errorf("%s: %d ch at %d Hz", r.Name, s.Channels, s.Rate)
		}
		if s.Name != r.LibraryName() || s.Description != r.Desc || len(s.Tags) < 2 || s.Tags[0] != r.Category {
			t.Errorf("%s: metadata name=%q tags=%v", r.Name, s.Name, s.Tags)
		}
		var peak float64
		for _, v := range s.Data {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("%s: NaN/Inf", r.Name)
			}
			peak = math.Max(peak, math.Abs(float64(v)))
		}
		if peak < 0.85 || peak > 0.90 {
			t.Errorf("%s: peak %.3f outside [0.85, 0.90]", r.Name, peak)
		}
		for ch := 0; ch < s.Channels; ch++ {
			x := channel(s, ch)
			var m float64
			for _, v := range x {
				m += v
			}
			m /= float64(len(x))
			if math.Abs(m) > 0.02*peak {
				t.Errorf("%s ch%d: DC offset %.4f", r.Name, ch, m)
			}
		}
		secs := float64(s.Frames()) / float64(s.Rate)
		limit := 6.0
		if r.Category == "fx" || r.Category == "pads" {
			limit = 8
		}
		if secs > limit+0.01 {
			t.Errorf("%s: %.2f s, over %.0f s", r.Name, secs, limit)
		}
	}
}

func TestLoopsAreClickFree(t *testing.T) {
	lib := library(t)
	loops := 0
	for _, r := range Recipes {
		s := lib[r.Name]
		if s.Loop == tracker.LoopNone {
			continue
		}
		loops++
		if !s.Looped() || s.LoopEnd-s.LoopStart < s.Rate/20 {
			t.Errorf("%s: unusable loop %d..%d of %d", r.Name, s.LoopStart, s.LoopEnd, s.Frames())
			continue
		}
		for ch := 0; ch < s.Channels; ch++ {
			x := channel(s, ch)
			peak := 0.0
			var steps []float64
			for i := s.LoopStart + 1; i < s.LoopEnd; i++ {
				peak = math.Max(peak, math.Abs(x[i]))
				steps = append(steps, math.Abs(x[i]-x[i-1]))
			}
			sort.Float64s(steps)
			p99 := steps[len(steps)*99/100]
			seam := math.Abs(x[s.LoopEnd-1] - x[s.LoopStart])
			// A seam is clean if it is small or no bigger than the steps
			// the material takes anyway (noise beds step a lot).
			if seam > 0.05*peak && seam > p99 {
				t.Errorf("%s ch%d: loop seam jumps %.4f (peak %.3f, p99 step %.4f)", r.Name, ch, seam, peak, p99)
			}
		}
	}
	if loops < 50 {
		t.Errorf("only %d looped samples", loops)
	}
}

// tuned are recipes with a clear, stable fundamental, where autocorrelation
// is a fair judge of tuning.
var tuned = []string{
	"bass_sub", "bass_moog", "bass_acid", "bass_fm", "bass_wobble", "bass_pulse", "bass_dist",
	"lead_square", "lead_pwm", "lead_fm", "lead_whistle", "lead_ocarina", "lead_overdrive", "lead_acid",
	"lead_crushed", "lead_sync", "pad_pwm", "organ_drawbar", "organ_combo", "trumpet", "french_horn",
	"flute", "clarinet", "oboe", "pan_flute", "chip_pulse_12", "chip_pulse_25", "chip_pulse_50",
	"chip_triangle", "chip_noise_metal", "chip_wave", "chip_sid", "bass_finger", "bass_pick", "cello",
}

func TestTuning(t *testing.T) {
	lib := library(t)
	for _, name := range tuned {
		r, s := ByName(name), lib[name]
		if r == nil || s == nil {
			t.Fatalf("no recipe %q", name)
		}
		f := 440 * math.Pow(2, (float64(s.BaseNote)-58)/12)
		x := channel(s, 0)
		if s.Looped() {
			x = x[s.LoopStart:s.LoopEnd]
		}
		got := measurePitch(x, Rate, f)
		cents := 1200 * math.Log2(got/f)
		if math.Abs(cents) > 10 {
			t.Errorf("%s: %.2f Hz, %+.1f cents from %s", name, got, cents, tracker.NoteName(s.BaseNote))
		}
	}
}

func TestBaseNotes(t *testing.T) {
	lib := library(t)
	want := map[string]string{"kick_trap": "C-1", "tom_floor": "C-2", "bass_sub": "C-2", "lead_fm": "C-4",
		"cello": "C-3", "glockenspiel": "C-5", "flute": "C-5", "vox_chant": "C-2", "drone_scifi": "C-2"}
	for name, note := range want {
		if got := tracker.NoteName(lib[name].BaseNote); got != note {
			t.Errorf("%s: base %s, want %s", name, got, note)
		}
	}
}

// TestDefaultsAndDeterminism renders everything a second time with an
// explicit Defaults() map. Equality with the nil-map render proves both that
// missing knobs fall back to defaults and that rendering is deterministic.
func TestDefaultsAndDeterminism(t *testing.T) {
	lib := library(t)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i := range Recipes {
		r := &Recipes[i]
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			s := r.Generate(Rate, r.Defaults())
			a := lib[r.Name]
			if len(s.Data) != len(a.Data) || s.LoopStart != a.LoopStart || s.LoopEnd != a.LoopEnd {
				t.Errorf("%s: second render differs in shape", r.Name)
				return
			}
			for j := range s.Data {
				if s.Data[j] != a.Data[j] {
					t.Errorf("%s: second render differs at %d", r.Name, j)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestParamsChangeTheSound(t *testing.T) {
	r := ByName("kick_808")
	a := r.Generate(Rate, nil)
	b := r.Generate(Rate, map[string]float32{"decay": 0.4})
	if a.Frames() == b.Frames() {
		t.Errorf("decay knob had no effect on length")
	}
	// Out-of-range values clamp rather than explode.
	c := r.Generate(Rate, map[string]float32{"decay": 1000, "drive": -5})
	if c.Frames() > 4*Rate {
		t.Errorf("decay did not clamp: %d frames", c.Frames())
	}
}

// TestLibraryStats prints a per-recipe table when FOUNDRY_SYNTH_STATS=1.
func TestLibraryStats(t *testing.T) {
	if os.Getenv("FOUNDRY_SYNTH_STATS") == "" {
		t.Skip("set FOUNDRY_SYNTH_STATS=1")
	}
	lib := library(t)
	var bytes int
	for _, r := range Recipes {
		s := lib[r.Name]
		bytes += len(s.Data) * 3
		f := 440 * math.Pow(2, (float64(s.BaseNote)-58)/12)
		x := channel(s, 0)
		from, to := len(x)/5, len(x)
		if s.Looped() {
			from, to = s.LoopStart, s.LoopEnd
		}
		got := measurePitch(x[from:to], Rate, f)
		t.Logf("%-20s %dch %5.2fs loop=%-5v %+7.1fc  %v", r.Name, s.Channels, float64(s.Frames())/Rate,
			s.Looped(), 1200*math.Log2(got/f), libTime[r.Name].Round(time.Millisecond))
	}
	t.Logf("total %.1f MB", float64(bytes)/(1<<20))
}
