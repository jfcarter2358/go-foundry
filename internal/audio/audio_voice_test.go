package audio

// audio_voice_test.go — the pool under pressure.
//
// Voice stealing is the part of an audio system that is only ever exercised in
// the situation nobody tests by hand: a busy fight, thirty sounds deep, where
// the wrong policy makes the scene sound broken rather than merely busy. All
// of it is decided in ordinary Go with no device, so all of it can be asserted.

import "testing"

func TestThePoolFillsBeforeItSteals(t *testing.T) {
	m := newTestMixer(t)
	for i := 0; i < MaxVoices; i++ {
		if id := playConst(m, constClip(0.1, 1<<16, 1), 1, false, PriorityNormal); id == 0 {
			t.Fatalf("voice %d was refused with the pool not yet full", i)
		}
	}
	if m.live() != MaxVoices {
		t.Fatalf("%d voices live, want the full pool of %d", m.live(), MaxVoices)
	}
}

func TestStealingTakesTheQuietestVoiceOfEqualPriority(t *testing.T) {
	m := newTestMixer(t)
	// Fill the pool with loud voices, except one deliberately quiet one.
	var quiet VoiceID
	for i := 0; i < MaxVoices; i++ {
		g := float32(1)
		if i == 7 {
			g = 0.001
		}
		id := playConst(m, constClip(0.5, 1<<16, 1), g, false, PriorityNormal)
		if i == 7 {
			quiet = id
		}
	}
	// Prime the gains so the pool knows which voice is quiet.
	out := make([]float32, 128*2)
	m.render(out, 128)

	newID := playConst(m, constClip(0.5, 1<<16, 1), 1, false, PriorityNormal)
	if newID == 0 {
		t.Fatal("the new sound was dropped rather than stealing")
	}
	if _, still := m.byID[quiet]; still {
		t.Error("the quiet voice survived; something louder was stolen instead")
	}
	if _, ok := m.byID[newID]; !ok {
		t.Error("the new voice did not take a slot")
	}
}

func TestALowPriorityRequestIsDroppedRatherThanCuttingOffSomethingImportant(t *testing.T) {
	// Dropping the new sound is better than silencing the one the player is
	// listening to. This is the rule that keeps dialogue intact in a firefight.
	m := newTestMixer(t)
	for i := 0; i < MaxVoices; i++ {
		playConst(m, constClip(0.5, 1<<16, 1), 1, false, PriorityHigh)
	}
	out := make([]float32, 128*2)
	m.render(out, 128)

	before := m.dropped.Load()
	playConst(m, constClip(0.5, 1<<16, 1), 1, false, PriorityLow)

	if m.dropped.Load() != before+1 {
		t.Error("a low-priority sound displaced a high-priority one")
	}
	if m.live() != MaxVoices {
		t.Errorf("%d voices live, want %d — the pool changed size", m.live(), MaxVoices)
	}
}

func TestAHighPriorityRequestCanStealFromALowerOne(t *testing.T) {
	m := newTestMixer(t)
	for i := 0; i < MaxVoices; i++ {
		playConst(m, constClip(0.5, 1<<16, 1), 1, false, PriorityLow)
	}
	out := make([]float32, 128*2)
	m.render(out, 128)

	id := playConst(m, constClip(0.5, 1<<16, 1), 1, false, PriorityHigh)
	if _, ok := m.byID[id]; !ok {
		t.Error("a high-priority sound could not displace a pool of low-priority ones")
	}
}

func TestTheLoopingCapStopsAScriptFromEatingThePool(t *testing.T) {
	// A script bug that starts a loop every frame would otherwise fill the
	// pool in half a second and silence every one-shot in the game.
	m := newTestMixer(t)
	for i := 0; i < maxLoopingVoices+10; i++ {
		playConst(m, constClip(0.5, 1<<16, 1), 1, true, PriorityNormal)
	}
	if got := m.loopingVoices(); got > maxLoopingVoices {
		t.Errorf("%d looping voices live, cap is %d", got, maxLoopingVoices)
	}
	// One-shots must still be servable.
	if id := playConst(m, constClip(0.5, 100, 1), 1, false, PriorityNormal); id == 0 {
		t.Error("a one-shot was refused while the looping cap was full")
	}
}

func TestTheLoopingCapLeavesRoomForTheMusic(t *testing.T) {
	// level2 starts ten ambience and engine loops at load, and its song used
	// to be the voice the cap turned away — silently, for the whole level.
	m := newTestMixer(t)
	for i := 0; i < maxLoopingVoices+2; i++ {
		playConst(m, constClip(0.5, 1<<16, 1), 1, true, PriorityNormal)
	}
	id := newVoiceID()
	m.apply(command{kind: cmdPlay, id: id, req: PlayRequest{
		Gen: &constGen{level: 0.25, blocks: 1 << 20}, Bus: BusMusic, Gain: 1, Loop: true, Priority: PriorityHigh,
	}})
	if _, ok := m.byID[id]; !ok {
		t.Fatal("the song was refused because the looping cap was full")
	}
	// And the music does not use up the cap the effects loops share.
	if got := m.loopingVoices(); got > maxLoopingVoices {
		t.Errorf("%d capped looping voices live, cap is %d", got, maxLoopingVoices)
	}
}

func TestAStaleHandleCannotSteerAVoiceThatReplacedIt(t *testing.T) {
	// Handles come from a monotonic counter and are never reused, so a Stop
	// arriving after its sound already ended is a no-op rather than silencing
	// whatever inherited the slot.
	m := newTestMixer(t)
	old := playConst(m, constClip(0.5, 10, 1), 1, false, PriorityNormal)

	out := make([]float32, 64*2)
	m.render(out, 64) // the short clip ends and frees its slot

	fresh := playConst(m, constClip(0.5, 1<<16, 1), 1, true, PriorityNormal)
	m.apply(command{kind: cmdStop, id: old})

	if _, ok := m.byID[fresh]; !ok {
		t.Fatal("the fresh voice is gone")
	}
	if m.voices[m.byID[fresh]].stopping {
		t.Error("a stale handle stopped the voice that took its slot")
	}
}

func TestStopAllSilencesEveryVoice(t *testing.T) {
	// levelio.ApplyRawLevel rebuilds the level wholesale for undo. Without
	// this, undoing the placement of a sound leaves it playing forever,
	// attached to an object that no longer exists.
	m := newTestMixer(t)
	for i := 0; i < 5; i++ {
		playConst(m, constClip(0.5, 1<<16, 1), 1, true, PriorityNormal)
	}
	m.apply(command{kind: cmdStopAll})

	out := make([]float32, 4096*2)
	m.render(out, 4096)
	if m.live() != 0 {
		t.Errorf("%d voices survived StopAll", m.live())
	}
}

func TestPlayingAnEmptyClipDoesNotTakeASlot(t *testing.T) {
	m := newTestMixer(t)
	m.apply(command{kind: cmdPlay, id: newVoiceID(), req: PlayRequest{
		Clip: &Clip{Name: "empty", Rate: testRate, Channels: 1}, Gain: 1, Pitch: 1,
	}})
	if m.live() != 0 {
		t.Error("a clip with no samples took a voice")
	}
}
