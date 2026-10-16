package audio

import (
	"testing"
)

// rmsAfter renders blocks of m and returns the RMS of the last one.
func rmsAfter(m *mixer, blocks int) float64 {
	out := make([]float32, 512*2)
	for b := 0; b < blocks; b++ {
		m.render(out, 512)
	}
	s := 0.0
	for _, v := range out {
		s += float64(v) * float64(v)
	}
	return s / float64(len(out))
}

func TestMuffleCutsTheHighsOfTheEffectsBusOnly(t *testing.T) {
	t.Cleanup(func() { SetMuffle(0) })
	level := func(hz float64, bus Bus, muffle float32) float64 {
		SetMuffle(muffle)
		m := newTestMixer(t)
		c := sineClip(hz, testRate)
		m.apply(command{kind: cmdPlay, id: newVoiceID(), req: PlayRequest{Clip: c, Bus: bus, Gain: 0.5, Pitch: 1, Loop: true}})
		// Long enough for the glide to settle.
		return rmsAfter(m, 60)
	}
	dryHi, wetHi := level(6000, BusEffects, 0), level(6000, BusEffects, 1)
	dryLo, wetLo := level(120, BusEffects, 0), level(120, BusEffects, 1)
	if wetHi > dryHi*0.05 {
		t.Errorf("a 6 kHz tone under water kept %.3f of its power; want almost none", wetHi/dryHi)
	}
	if wetLo < dryLo*0.1 {
		t.Errorf("a 120 Hz tone under water kept %.3f of its power; want it heard (ducked)", wetLo/dryLo)
	}
	musicDry, musicWet := level(6000, BusMusic, 0), level(6000, BusMusic, 1)
	if musicWet < musicDry*0.99 {
		t.Errorf("the music bus was muffled: %.3f of its power", musicWet/musicDry)
	}
	// Back above: the effects are dry again.
	SetMuffle(0)
	m := newTestMixer(t)
	m.muffle.cur = 1
	c := sineClip(6000, testRate)
	m.apply(command{kind: cmdPlay, id: newVoiceID(), req: PlayRequest{Clip: c, Bus: BusEffects, Gain: 0.5, Pitch: 1, Loop: true}})
	if back := rmsAfter(m, 60); back < dryHi*0.99 {
		t.Errorf("after surfacing a 6 kHz tone kept %.3f of its power; want all of it", back/dryHi)
	}
}
