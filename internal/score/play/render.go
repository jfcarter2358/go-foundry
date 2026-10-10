package play

// render.go — playing a score with no device: WAV export and tests.
//
// The scheduler is deterministic given a program, a start and a command
// log, so an offline render is the audio a game would hear, and a hash of
// it is a golden test of the whole path from notation to samples.

import (
	"math"
	"os"

	"foundry/internal/score"
	"foundry/internal/tracker"
)

// RenderOptions controls an offline render.
type RenderOptions struct {
	Rate int // default 48000
	// Section starts there; otherwise From, a tick.
	Section string
	From    int64
	// Seconds caps the length. 0 renders to the end of the piece (one pass
	// of a looping one) plus Tail.
	Seconds float64
	// Tail is extra time for reverb and releases after the end, seconds.
	Tail float64
	// TempoScale plays faster or slower, 0.25..2 like Player.TempoScale;
	// 0 is 1.
	TempoScale float64
	// Params are set before playback starts.
	Params map[string]float32
	// Block is the frames handed to each Render; 0 is 480 (10 ms at 48 k).
	Block int
}

// Render plays a prepared score offline and returns interleaved stereo.
func Render(prog *Program, o RenderOptions) []float32 {
	if o.Rate <= 0 {
		o.Rate = 48000
	}
	if o.Block <= 0 {
		o.Block = 480
	}
	if o.TempoScale <= 0 {
		o.TempoScale = 1
	}
	o.TempoScale = math.Max(0.25, math.Min(2, o.TempoScale))
	p := NewFromProgram(prog)
	for k, v := range o.Params {
		p.SetParamNow(k, v)
	}
	if o.Section != "" {
		p.Start(o.Section)
	} else {
		p.StartAt(o.From)
	}
	p.scale, p.scaleTarget = o.TempoScale, o.TempoScale
	secs := o.Seconds
	if secs <= 0 {
		secs = (prog.Perf.Seconds-p.sec)/o.TempoScale + o.Tail
	}
	total := int(math.Ceil(secs * float64(o.Rate)))
	out := make([]float32, total*2)
	p.eng.SetRate(o.Rate)
	p.rate = o.Rate
	for i := 0; i < total; i += o.Block {
		n := min(o.Block, total-i)
		if !p.Render(out[i*2:(i+n)*2], n, o.Rate) {
			out = out[:(i+n)*2]
			break
		}
	}
	return out
}

// RenderScore prepares and renders a score; errs are instruments that did
// not load (their lines are silent).
func RenderScore(s *score.Score, o RenderOptions) (data []float32, errs []error, err error) {
	prog, errs, err := Prepare(s)
	if err != nil {
		return nil, errs, err
	}
	return Render(prog, o), errs, nil
}

// WriteWAV writes rendered audio as a 24-bit stereo WAV file.
func WriteWAV(path string, data []float32, rate int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := tracker.WriteWAV(f, data, 2, rate, 24); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// FitGain lowers a score's master gain until an offline render of it peaks
// at no more than target (0.9 leaves headroom): what an importer does, so a
// piece that arrives with every line at full volume and a forte piano does
// not clip. It never raises the gain. It returns the peak at the gain the
// score came with.
func FitGain(s *score.Score, target float64) (peak float64, err error) {
	if s.Gain <= 0 {
		s.Gain = 1
	}
	prog, _, err := Prepare(s)
	if err != nil {
		return 0, err
	}
	first := 0.0
	// The master compressor (when on) makes level not quite linear in
	// gain; a second pass settles it.
	for pass := 0; pass < 3; pass++ {
		pk := float64(tracker.Peak(Render(prog, RenderOptions{Rate: 32000, Tail: 1})))
		if pass == 0 {
			first = pk
		}
		if pk <= target || pk == 0 {
			break
		}
		g := float64(s.Gain) * target / pk
		s.Gain = float32(math.Floor(g*100) / 100)
		if prog, _, err = Prepare(s); err != nil {
			return first, err
		}
	}
	return first, nil
}
