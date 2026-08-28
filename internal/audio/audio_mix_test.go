package audio

// audio_mix_test.go — the mixer, driven with no device.
//
// render() is an ordinary method that fills a slice, so everything the voice
// pool and the mix step do can be asserted by rendering audio into memory and
// looking at the samples. That is the entire reason the mixer is Go rather
// than SDL_mixer or OpenAL: the alternative is a black box whose only test is
// listening to it.
//
// The test worth reading is TestStopRampsRatherThanCutting. A hard stop on a
// non-zero sample is a step discontinuity, which is a click, and a click is
// findable by ear and by nothing else.

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

const testRate = 48000

// constClip is a clip of a constant value, which makes what the mixer did to
// its gain readable directly off the output.
func constClip(v float32, frames, channels int) *Clip {
	s := make([]float32, frames*channels)
	for i := range s {
		s[i] = v
	}
	return &Clip{Name: "const", Samples: s, Rate: testRate, Channels: channels}
}

func sineClip(hz float64, frames int) *Clip {
	s := make([]float32, frames)
	for i := range s {
		s[i] = float32(math.Sin(2 * math.Pi * hz * float64(i) / testRate))
	}
	return &Clip{Name: "sine", Samples: s, Rate: testRate, Channels: 1}
}

// newTestMixer returns a mixer with a listener published at the origin facing
// +X, which is yaw 0 in the engine's convention.
func newTestMixer(t *testing.T) *mixer {
	t.Helper()
	m := newMixer(testRate, 2)
	m.state.Store(&frameState{
		listener: BasisFrom(mgl32.Vec3{}, mgl32.Vec3{1, 0, 0}, mgl32.Vec3{0, 1, 0}),
	})
	return m
}

// playConst starts a non-spatial voice and returns its handle.
func playConst(m *mixer, c *Clip, gain float32, loop bool, prio Priority) VoiceID {
	id := newVoiceID()
	m.apply(command{kind: cmdPlay, id: id, req: PlayRequest{
		Clip: c, Bus: BusEffects, Gain: gain, Pitch: 1, Loop: loop, Priority: prio,
	}})
	return id
}

func peakOf(b []float32) float32 {
	p := float32(0)
	for _, v := range b {
		if a := absF(v); a > p {
			p = a
		}
	}
	return p
}

func TestASilentMixerProducesSilenceRatherThanGarbage(t *testing.T) {
	m := newTestMixer(t)
	out := make([]float32, 256*2)
	for i := range out {
		out[i] = 0.5 // render must overwrite, not add to, whatever was here
	}
	m.render(out, 256)
	if p := peakOf(out); p != 0 {
		t.Errorf("an empty mixer produced a peak of %v; render must clear its buffer", p)
	}
}

func TestAOneShotFreesItsVoiceWhenItEnds(t *testing.T) {
	m := newTestMixer(t)
	playConst(m, constClip(0.5, 100, 1), 1, false, PriorityNormal)
	if m.live() != 1 {
		t.Fatalf("the voice did not start")
	}
	out := make([]float32, 256*2)
	m.render(out, 256) // longer than the clip
	if m.live() != 0 {
		t.Errorf("%d voices still live after the clip ran out; the pool leaks", m.live())
	}
	if len(m.byID) != 0 {
		t.Errorf("the handle map still holds %d entries", len(m.byID))
	}
}

func TestALoopingVoiceKeepsGoingPastTheEndOfItsClip(t *testing.T) {
	m := newTestMixer(t)
	playConst(m, constClip(0.5, 100, 1), 1, true, PriorityNormal)
	out := make([]float32, 512*2)
	m.render(out, 512)
	if m.live() != 1 {
		t.Fatal("the looping voice ended")
	}
	// Constant clip, so every sample of the output must still be sounding.
	for i := 0; i < 512*2; i++ {
		if out[i] == 0 {
			t.Fatalf("sample %d is silent; the loop left a gap at the seam", i)
		}
	}
}

func TestStopRampsRatherThanCutting(t *testing.T) {
	// A hard stop mid-waveform is a step, and a step is a click. The ramp is
	// the only thing between this mixer and a bug that no assertion other
	// than this one would find.
	m := newTestMixer(t)
	id := playConst(m, constClip(1, testRate, 1), 1, true, PriorityNormal)

	out := make([]float32, 64*2)
	m.render(out, 64) // get it up to level

	m.apply(command{kind: cmdStop, id: id})

	ramp := make([]float32, 1024*2)
	m.render(ramp, 1024)

	// Walk the left channel and assert no single-sample jump larger than the
	// ramp could possibly produce.
	maxStep := float32(0)
	for i := 2; i < len(ramp); i += 2 {
		if d := absF(ramp[i] - ramp[i-2]); d > maxStep {
			maxStep = d
		}
	}
	// declickSeconds at testRate gives the per-sample slope; allow a little
	// headroom for the clip's own content.
	bound := 2 / (declickSeconds * float32(testRate))
	if maxStep > bound {
		t.Errorf("largest sample-to-sample step during stop was %.5f, bound %.5f — it is cutting, not ramping", maxStep, bound)
	}
	if m.live() != 0 {
		t.Errorf("the voice did not free itself after the ramp finished")
	}
	if tail := peakOf(ramp[len(ramp)-64:]); tail != 0 {
		t.Errorf("the tail is still at %.5f; the ramp did not reach silence", tail)
	}
}

func TestTwoIdenticalVoicesSumLouderThanOne(t *testing.T) {
	one := newTestMixer(t)
	playConst(one, constClip(0.25, 4096, 1), 1, true, PriorityNormal)
	a := make([]float32, 256*2)
	one.render(a, 256)

	two := newTestMixer(t)
	playConst(two, constClip(0.25, 4096, 1), 1, true, PriorityNormal)
	playConst(two, constClip(0.25, 4096, 1), 1, true, PriorityNormal)
	b := make([]float32, 256*2)
	two.render(b, 256)

	if peakOf(b) <= peakOf(a)*1.5 {
		t.Errorf("one voice peaked at %.4f, two at %.4f; they are not summing", peakOf(a), peakOf(b))
	}
}

func TestTheLimiterNeverLetsASampleLeaveTheUnitRange(t *testing.T) {
	// Twenty voices at full gain land well past unity. A hard clamp would
	// square off the tops; the limiter has to pull the block down instead.
	m := newTestMixer(t)
	for i := 0; i < 20; i++ {
		playConst(m, constClip(0.9, 4096, 1), 1, true, PriorityNormal)
	}
	out := make([]float32, 1024*2)
	for block := 0; block < 4; block++ {
		m.render(out, 1024)
		for i, v := range out {
			if v > 1.0001 || v < -1.0001 || math.IsNaN(float64(v)) {
				t.Fatalf("block %d sample %d is %v, outside [-1,1]", block, i, v)
			}
		}
	}
}

func TestMutingTheEffectsBusLeavesMusicAlone(t *testing.T) {
	// Called, not deferred: `defer restoreGains(t)` would run at the end of
	// the test and snapshot the gains this test had already zeroed, restoring
	// silence and taking every later test with it.
	restoreGains(t)

	m := newTestMixer(t)
	id := newVoiceID()
	m.apply(command{kind: cmdPlay, id: id, req: PlayRequest{
		Clip: constClip(0.5, 4096, 1), Bus: BusMusic, Gain: 1, Pitch: 1, Loop: true,
	}})
	playConst(m, constClip(0.5, 4096, 1), 1, true, PriorityNormal) // effects

	SetEffectsGain(0)
	SetMusicGain(1)

	out := make([]float32, 512*2)
	m.render(out, 512)
	withMusicOnly := peakOf(out)

	SetMusicGain(0)
	m2 := newTestMixer(t)
	m2.apply(command{kind: cmdPlay, id: newVoiceID(), req: PlayRequest{
		Clip: constClip(0.5, 4096, 1), Bus: BusMusic, Gain: 1, Pitch: 1, Loop: true,
	}})
	m2.render(out, 512)

	if withMusicOnly == 0 {
		t.Error("muting effects silenced music too")
	}
	if p := peakOf(out); p != 0 {
		t.Errorf("music still at %.4f with the music bus at zero", p)
	}
}

// restoreGains snapshots the three bus gains and puts them back when the test
// ends. The gains are package globals, which is the house pattern for engine
// settings; the house test pattern for one is a with-helper on t.Cleanup.
func restoreGains(t *testing.T) {
	t.Helper()
	m, e, mu := MasterGain(), EffectsGain(), MusicGain()
	t.Cleanup(func() { SetMasterGain(m); SetEffectsGain(e); SetMusicGain(mu) })
}

func TestAClipAtHalfTheDeviceRatePlaysForTwiceAsLong(t *testing.T) {
	// The rate difference is folded into the mixer's step rather than being
	// resampled at load, so this is the test that the step is right.
	m := newTestMixer(t)
	c := constClip(0.5, 100, 1)
	c.Rate = testRate / 2
	playConst(m, c, 1, false, PriorityNormal)

	out := make([]float32, 150*2)
	m.render(out, 150)
	// 100 source frames at half rate is 200 device frames, so a 150-frame
	// block must not have exhausted it.
	if m.live() != 1 {
		t.Error("a half-rate clip ended after 150 device frames; it should last 200")
	}
	m.render(out, 150)
	if m.live() != 0 {
		t.Error("the half-rate clip outlasted 300 device frames")
	}
}

func TestAMovingSourceDoesNotZipper(t *testing.T) {
	// Gains interpolate across the block, not at its boundary. Applying a
	// pan change as a step is the classic source of zipper noise.
	m := newTestMixer(t)
	id := newVoiceID()
	m.apply(command{kind: cmdPlay, id: id, req: PlayRequest{
		Clip: constClip(1, 1<<16, 1), Bus: BusEffects, Gain: 1, Pitch: 1, Loop: true,
		Spatial: true, Position: mgl32.Vec3{0, 0, -10}, Falloff: DefaultFalloff(),
	}})

	out := make([]float32, 256*2)
	maxStep := float32(0)
	for b := 0; b < 20; b++ {
		// Sweep the source from hard right to hard left, 1 unit a block.
		z := float32(-10 + b)
		m.state.Store(&frameState{
			listener: BasisFrom(mgl32.Vec3{}, mgl32.Vec3{1, 0, 0}, mgl32.Vec3{0, 1, 0}),
			voices: []VoiceParams{{
				ID: id, Position: mgl32.Vec3{0, 0, z}, Gain: 1, Pitch: 1,
				Falloff: DefaultFalloff(), Spatial: true,
			}},
		})
		m.render(out, 256)
		for i := 2; i < len(out); i += 2 {
			if d := absF(out[i] - out[i-2]); d > maxStep {
				maxStep = d
			}
		}
	}
	if maxStep > 0.05 {
		t.Errorf("largest per-sample gain step while sweeping was %.4f; the pan is being applied as a jump", maxStep)
	}
}

func TestAStereoClipIsNotPannedIntoNonsense(t *testing.T) {
	// A stereo clip carries its own image. Played positionally it is
	// downmixed rather than having a pan fought on top of it.
	m := newTestMixer(t)
	s := make([]float32, 4096*2)
	for i := 0; i < 4096; i++ {
		s[i*2] = 0.5   // left
		s[i*2+1] = 0.5 // right
	}
	c := &Clip{Name: "st", Samples: s, Rate: testRate, Channels: 2}
	id := newVoiceID()
	m.apply(command{kind: cmdPlay, id: id, req: PlayRequest{
		Clip: c, Bus: BusEffects, Gain: 1, Pitch: 1, Loop: true,
		Spatial: true, Position: mgl32.Vec3{0, 0, -3}, Falloff: DefaultFalloff(),
	}})
	out := make([]float32, 256*2)
	m.render(out, 256)
	if peakOf(out) == 0 {
		t.Error("a positional stereo clip produced silence")
	}
}
