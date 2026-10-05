package audio

import "testing"

// constGen plays a constant level for a number of blocks, then ends.
type constGen struct {
	level  float32
	blocks int
}

func (g *constGen) Render(dst []float32, frames, rate int) bool {
	for i := range dst[:frames*2] {
		dst[i] = g.level
	}
	g.blocks--
	return g.blocks > 0
}

func TestGeneratorVoicePlaysAtBusGainAndEnds(t *testing.T) {
	m := newMixer(48000, 2)
	g := &constGen{level: 0.25, blocks: 3}
	m.apply(command{kind: cmdPlay, id: 7, req: PlayRequest{Gen: g, Bus: BusMusic, Gain: 1, Loop: true}})
	out := make([]float32, 256*2)

	m.render(out, 256)
	want := 0.25 * MusicGain() * MasterGain()
	if d := out[100] - want; d > 1e-4 || d < -1e-4 {
		t.Fatalf("generator mixed at %v, want %v", out[100], want)
	}
	if m.live() != 1 {
		t.Fatalf("%d live voices, want 1", m.live())
	}
	m.render(out, 256)
	m.render(out, 256)
	if m.live() != 0 {
		t.Fatal("a generator that reported no more audio kept its voice")
	}
}

func TestGeneratorVoiceStopsOnRamp(t *testing.T) {
	m := newMixer(48000, 2)
	g := &constGen{level: 0.5, blocks: 1 << 30}
	m.apply(command{kind: cmdPlay, id: 9, req: PlayRequest{Gen: g, Bus: BusMusic, Gain: 1, Loop: true}})
	out := make([]float32, 480*2)
	m.render(out, 480)
	m.apply(command{kind: cmdStop, id: 9})
	m.render(out, 480)
	if m.live() != 0 {
		t.Fatal("stopped generator still live after the declick ramp")
	}
	if out[len(out)-2] != 0 {
		t.Fatalf("tail of a stopped generator is %v, want silence", out[len(out)-2])
	}
}
