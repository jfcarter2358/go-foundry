package world

// sky_test.go — BeginTransition, TransitionBlend and SkyUpdate: the ease a
// script's ease_sky verb rides on.
//
// RenderSky is what actually draws the two states blended together, and that
// half is proven with real pixels in internal/render's own GL test. What
// belongs here is the half with no GL in it at all: that arming a transition
// snapshots the state it was IN at that moment, that the blend fraction moves
// from 0 to 1 over exactly the seconds asked for and clamps rather than
// overshooting, and that a settled transition retires itself so a sky that
// never eases keeps costing one cubemap sample rather than two forever after.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func TestBeginTransitionSnapshotsTheCurrentLook(t *testing.T) {
	s := DefaultSky()
	s.Kind = SkyGradient
	s.Zenith = mgl32.Vec3{0, 1, 0}

	s.BeginTransition(2)

	if s.TransitionFrom == nil {
		t.Fatal("BeginTransition did not arm a transition")
	}
	if got := s.TransitionFrom.Zenith; got != s.Zenith {
		t.Errorf("the snapshot's zenith is %v, want the sky's own %v", got, s.Zenith)
	}
	if s.TransitionFrom.TransitionFrom != nil {
		t.Error("the snapshot itself carries a transition; it should be a flat, settled state")
	}
	if got := s.TransitionElapsed; got != 0 {
		t.Errorf("elapsed is %v right after arming, want 0", got)
	}
}

func TestTransitionBlendMovesFromZeroToOneOverItsDuration(t *testing.T) {
	s := DefaultSky()
	s.BeginTransition(2)

	if got := s.TransitionBlend(); got != 0 {
		t.Errorf("blend right after arming is %v, want 0", got)
	}

	s.TransitionElapsed = 1
	if got := s.TransitionBlend(); got != 0.5 {
		t.Errorf("blend halfway through is %v, want 0.5", got)
	}

	// Past the duration it clamps rather than reporting more than fully
	// eased-in — a late tick (a long frame, a paused game resuming) must not
	// read as the blend running backward past 1.
	s.TransitionElapsed = 5
	if got := s.TransitionBlend(); got != 1 {
		t.Errorf("blend past the duration is %v, want 1 (clamped)", got)
	}
}

func TestSettledSkyReportsFullBlendWithNoTransition(t *testing.T) {
	s := DefaultSky()
	if got := s.TransitionBlend(); got != 1 {
		t.Errorf("a sky that never eased reports blend %v, want 1", got)
	}
}

func TestNonPositiveDurationCancelsRatherThanArms(t *testing.T) {
	s := DefaultSky()
	s.BeginTransition(2)
	if s.TransitionFrom == nil {
		t.Fatal("setup: transition did not arm")
	}

	s.BeginTransition(0)
	if s.TransitionFrom != nil {
		t.Error("BeginTransition(0) should cancel an in-flight transition, not arm a zero-length one")
	}
	if got := s.TransitionBlend(); got != 1 {
		t.Errorf("after cancelling, blend is %v, want 1", got)
	}
}

func TestSkyUpdateAdvancesAndThenRetiresTheTransition(t *testing.T) {
	s := DefaultSky()
	s.BeginTransition(2)
	lvl := &Level{Sky: &s}

	SkyUpdate(lvl, 1, true)
	if lvl.Sky.TransitionFrom == nil {
		t.Fatal("one second into a two second ease, the transition should still be running")
	}
	if got := lvl.Sky.TransitionBlend(); got != 0.5 {
		t.Errorf("blend after 1s of 2s is %v, want 0.5", got)
	}

	SkyUpdate(lvl, 1, true)
	if lvl.Sky.TransitionFrom != nil {
		t.Error("the transition should have retired once elapsed reached the duration")
	}
	if got := lvl.Sky.TransitionBlend(); got != 1 {
		t.Errorf("a retired transition reports blend %v, want 1", got)
	}
}

// SkyUpdate is gated the same way EmitterUpdate and SoundUpdate are: a
// transition already running should hold rather than keep easing while the
// editor has stepped in.
func TestSkyUpdateHoldsWhenNotPlaying(t *testing.T) {
	s := DefaultSky()
	s.BeginTransition(2)
	lvl := &Level{Sky: &s}

	SkyUpdate(lvl, 1, false)
	if got := lvl.Sky.TransitionElapsed; got != 0 {
		t.Errorf("elapsed advanced to %v while not playing, want 0", got)
	}
}

func TestSkyUpdateIsANoOpWithNothingToAdvance(t *testing.T) {
	// Neither of these should panic, and neither should conjure a sky or a
	// transition out of nothing.
	SkyUpdate(nil, 1, true)
	SkyUpdate(&Level{}, 1, true)

	s := DefaultSky()
	lvl := &Level{Sky: &s}
	SkyUpdate(lvl, 1, true) // no transition armed
	if lvl.Sky.TransitionFrom != nil {
		t.Error("SkyUpdate armed a transition that was never begun")
	}
}
