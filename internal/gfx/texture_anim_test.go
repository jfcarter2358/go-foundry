package gfx

import (
	"image"
	"testing"
)

// resetAnimState clears every package-level animation var a test might touch,
// both before and after, so these tests don't leak state into each other or
// into anything else in the package that happens to run in the same process.
func resetAnimState(t *testing.T) {
	t.Helper()
	reset := func() {
		animatedTextures = map[string]*animatedTexture{}
		animTick = 0
		touchedAnimatedTexture = ""
		touchedAnimatedTextureAt = 0
		pendingTextureUploads = nil
	}
	reset()
	t.Cleanup(reset)
}

func rgbaFrames(n int) []*image.RGBA {
	frames := make([]*image.RGBA, n)
	for i := range frames {
		frames[i] = image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	return frames
}

func TestRegisterAnimatedTextureIgnoresSingleFrame(t *testing.T) {
	resetAnimState(t)
	RegisterAnimatedTexture("torch", rgbaFrames(1))
	if _, ok := animatedTextures["torch"]; ok {
		t.Error("a single-frame stack should not register as animated")
	}
}

func TestRegisterAnimatedTextureReplacesPriorStack(t *testing.T) {
	resetAnimState(t)
	RegisterAnimatedTexture("torch", rgbaFrames(4))
	if a := animatedTextures["torch"]; a == nil || len(a.frames) != 4 {
		t.Fatalf("torch registered with %v frames, want 4", animatedTextures["torch"])
	}
	RegisterAnimatedTexture("torch", rgbaFrames(2))
	if a := animatedTextures["torch"]; a == nil || len(a.frames) != 2 {
		t.Fatalf("torch registered with %v frames, want 2 after re-registering", animatedTextures["torch"])
	}
	// Demoting back to one frame should un-register it entirely.
	RegisterAnimatedTexture("torch", rgbaFrames(1))
	if _, ok := animatedTextures["torch"]; ok {
		t.Error("re-registering with a single frame should remove the animated entry")
	}
}

func TestUnregisterAnimatedTexture(t *testing.T) {
	resetAnimState(t)
	RegisterAnimatedTexture("torch", rgbaFrames(3))
	UnregisterAnimatedTexture("torch")
	if _, ok := animatedTextures["torch"]; ok {
		t.Error("UnregisterAnimatedTexture left an entry behind")
	}
}

func TestAdvanceAnimatedTexturesPicksFrameByClock(t *testing.T) {
	resetAnimState(t)
	RegisterAnimatedTexture("water", rgbaFrames(4)) // 4 fps: 0.25s/frame

	AdvanceAnimatedTextures(0)
	if got := animatedTextures["water"].current; got != 0 {
		t.Fatalf("at t=0, current = %d, want 0", got)
	}
	if len(pendingTextureUploads) != 1 {
		t.Fatalf("t=0 should queue one upload, got %d", len(pendingTextureUploads))
	}
	pendingTextureUploads = nil

	AdvanceAnimatedTextures(0.6) // 0.6*4 = 2.4 -> frame 2
	if got := animatedTextures["water"].current; got != 2 {
		t.Fatalf("at t=0.6, current = %d, want 2", got)
	}
	if len(pendingTextureUploads) != 1 {
		t.Fatalf("a frame change should queue exactly one upload, got %d", len(pendingTextureUploads))
	}

	pendingTextureUploads = nil
	AdvanceAnimatedTextures(0.6) // same frame again — no redundant upload
	if len(pendingTextureUploads) != 0 {
		t.Errorf("an unchanged frame should not queue another upload, got %d", len(pendingTextureUploads))
	}

	AdvanceAnimatedTextures(1.0) // a full cycle (4 frames) always takes 1 second
	if got := animatedTextures["water"].current; got != 0 {
		t.Errorf("at t=1.0 (one full cycle), current = %d, want back to 0", got)
	}
}

func TestTouchAnimatedTextureExemptsItBriefly(t *testing.T) {
	resetAnimState(t)
	RegisterAnimatedTexture("lava", rgbaFrames(4))
	TouchAnimatedTexture("lava")

	// Immediately after a touch, a clock-driven frame change must not
	// overwrite whatever the touch itself queued.
	AdvanceAnimatedTextures(0.6)
	if got := animatedTextures["lava"].current; got != -1 {
		t.Errorf("a just-touched texture should not advance yet, current = %d, want -1 (untouched)", got)
	}
	if len(pendingTextureUploads) != 0 {
		t.Errorf("a just-touched texture should not queue a playback upload, got %d", len(pendingTextureUploads))
	}

	// The exemption expires on its own a few ticks later, with no further
	// touch — this is what keeps "editor window still open" from pausing
	// playback indefinitely.
	for i := 0; i < animTouchGraceTicks; i++ {
		AdvanceAnimatedTextures(0.6)
	}
	if got := animatedTextures["lava"].current; got != 2 {
		t.Errorf("lava should resume once the touch's grace period lapses, current = %d, want 2", got)
	}
}

func TestTouchAnimatedTextureExemptsOnlyOneName(t *testing.T) {
	resetAnimState(t)
	RegisterAnimatedTexture("lava", rgbaFrames(4))
	RegisterAnimatedTexture("water", rgbaFrames(4))
	TouchAnimatedTexture("lava")

	AdvanceAnimatedTextures(0.6)
	if got := animatedTextures["water"].current; got != 2 {
		t.Errorf("touching lava should not exempt an unrelated texture, water.current = %d, want 2", got)
	}
}
