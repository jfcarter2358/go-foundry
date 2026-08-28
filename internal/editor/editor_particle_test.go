package editor

// editor_particle_test.go — the particle editor's testable half.
//
// The ramp strip is the interesting part. Its stops are dragged by T rather
// than by index precisely because a drag can carry one past its neighbour and
// re-sort the list; an index taken at mouse-down then names a different stop by
// mouse-up. That bug is invisible in a screenshot and obvious in a test.

import (
	"foundry/internal/particle"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// withRampLayer installs a document with one layer and selects it.
func withRampLayer(t *testing.T, l particle.Layer) *particle.Layer {
	t.Helper()
	prevDoc, prevIdx, prevRamp := particleDoc, particleActiveLayer, particleActiveRamp
	t.Cleanup(func() {
		particleDoc, particleActiveLayer, particleActiveRamp = prevDoc, prevIdx, prevRamp
		particleDrag = particleDragStop{}
	})
	particleDoc = &particle.Doc{ID: "t", Gravity: particle.DefaultGravity, Layers: []particle.Layer{l}}
	particleActiveLayer = 0
	return &particleDoc.Layers[0]
}

// TestDraggingAStopPastItsNeighbourKeepsHoldOfIt is the reason stops are keyed
// by T. Dragging the first stop to the far end re-sorts the list; the drag must
// still be carrying the same point afterwards.
func TestDraggingAStopPastItsNeighbourKeepsHoldOfIt(t *testing.T) {
	lay := withRampLayer(t, particle.Layer{
		Name:      "l",
		AlphaRamp: particle.Ramp{{T: 0, V: 0.1}, {T: 0.5, V: 0.5}, {T: 1, V: 0.9}},
	})
	particleActiveRamp = rampAlpha

	// Grab the stop at T=0 and drag it to T=0.9, past the one at 0.5.
	idx := particleStopIndexAt(lay, 0)
	if idx != 0 {
		t.Fatalf("stop at T=0 is at index %d, want 0", idx)
	}
	particleMoveStop(lay, idx, 0.9, 0.1)

	// The list is re-sorted, so the dragged stop is no longer index 0 — but it
	// must still be findable by its new T, which is what the drag tracks.
	moved := particleStopIndexAt(lay, 0.9)
	if moved < 0 {
		t.Fatal("the dragged stop could not be found at its new T")
	}
	if moved == 0 {
		t.Error("the list did not re-sort; the test is not exercising the case it is for")
	}
	if got := lay.AlphaRamp[moved].V; got < 0.09 || got > 0.11 {
		t.Errorf("the drag landed on a different stop: value %v, want ~0.1", got)
	}
	// And the ramp is still sorted, which Ramp.At depends on.
	for i := 1; i < len(lay.AlphaRamp); i++ {
		if lay.AlphaRamp[i-1].T > lay.AlphaRamp[i].T {
			t.Fatalf("ramp left unsorted: %+v", lay.AlphaRamp)
		}
	}
}

// TestRampLayoutRoundTrips — the shared layout is what makes a stop grabbable
// where it was painted, so its two directions have to agree.
func TestRampLayoutRoundTrips(t *testing.T) {
	l := particleRampLayout{x0: 100, x1: 500, y0: 20, y1: 80}
	for _, v := range []float32{0, 0.25, 0.5, 1} {
		if got := l.xToT(l.tToX(v)); got < v-1e-4 || got > v+1e-4 {
			t.Errorf("T %v round-tripped to %v", v, got)
		}
		if got := l.yToV(l.vToY(v)); got < v-1e-4 || got > v+1e-4 {
			t.Errorf("V %v round-tripped to %v", v, got)
		}
	}
	// And it clamps rather than running off the strip.
	if got := l.xToT(0); got != 0 {
		t.Errorf("a click left of the strip gave T=%v, want 0", got)
	}
	if got := l.xToT(9999); got != 1 {
		t.Errorf("a click right of the strip gave T=%v, want 1", got)
	}
}

// TestDeletingAStopLeavesTheRestSorted.
func TestDeletingAStop(t *testing.T) {
	lay := withRampLayer(t, particle.Layer{
		Name:      "l",
		AlphaRamp: particle.Ramp{{T: 0, V: 0}, {T: 0.5, V: 1}, {T: 1, V: 0}},
	})
	particleActiveRamp = rampAlpha
	particleDeleteStop(lay, 1)
	if len(lay.AlphaRamp) != 2 {
		t.Fatalf("got %d stops, want 2", len(lay.AlphaRamp))
	}
	if lay.AlphaRamp[0].T != 0 || lay.AlphaRamp[1].T != 1 {
		t.Errorf("the wrong stop was deleted: %+v", lay.AlphaRamp)
	}
}

// TestColorStopsSortToo — the gradient is a separate list and needs the same
// treatment; Gradient.At walks it assuming sorted order.
func TestColorStopsSort(t *testing.T) {
	lay := withRampLayer(t, particle.Layer{
		Name: "l",
		ColorRamp: particle.Gradient{
			{T: 0, C: mgl32.Vec3{1, 0, 0}},
			{T: 1, C: mgl32.Vec3{0, 0, 1}},
		},
	})
	particleActiveRamp = rampColor
	particleMoveStop(lay, 0, 0.9, 0)
	for i := 1; i < len(lay.ColorRamp); i++ {
		if lay.ColorRamp[i-1].T > lay.ColorRamp[i].T {
			t.Fatalf("gradient left unsorted: %+v", lay.ColorRamp)
		}
	}
}

// TestSizeRampDisplayRescales — a size ramp routinely exceeds 1 (smoke grows),
// and the strip has to fit it rather than clip the curve off its own top.
func TestSizeRampDisplayRescales(t *testing.T) {
	r := particle.Ramp{{T: 0, V: 1}, {T: 1, V: 4}}
	if got := scaleForDisplay(r, 4); got < 0.999 || got > 1.001 {
		t.Errorf("the peak should sit at the top of the strip, got %v", got)
	}
	if got := scaleForDisplay(r, 2); got < 0.49 || got > 0.51 {
		t.Errorf("half the peak should sit halfway, got %v", got)
	}
	// A ramp entirely below 1 is not stretched up to fill the strip: 1 stays 1
	// so alpha and size read on the same scale.
	flat := particle.Ramp{{T: 0, V: 0.5}}
	if got := scaleForDisplay(flat, 0.5); got < 0.49 || got > 0.51 {
		t.Errorf("a sub-1 ramp was rescaled: %v", got)
	}
}

// TestParticleShortcuts covers the key map, which is why it returns a value
// rather than acting inline.
func TestParticleShortcuts(t *testing.T) {
	for _, c := range []struct {
		sc          sdl.Scancode
		ctrl, shift bool
		want        particleShortcut
	}{
		{sdl.SCANCODE_SPACE, false, false, particleShortcutPlayPause},
		{sdl.SCANCODE_R, false, false, particleShortcutRestart},
		{sdl.SCANCODE_PERIOD, false, false, particleShortcutStep},
		{sdl.SCANCODE_F, false, false, particleShortcutFrameCamera},
		{sdl.SCANCODE_B, false, false, particleShortcutToggleShapes},
		{sdl.SCANCODE_S, true, false, particleShortcutSave},
		{sdl.SCANCODE_O, true, false, particleShortcutOpen},
		{sdl.SCANCODE_N, true, false, particleShortcutNew},
		{sdl.SCANCODE_Z, true, false, particleShortcutUndo},
		{sdl.SCANCODE_Z, true, true, particleShortcutRedo},
		{sdl.SCANCODE_Y, true, false, particleShortcutRedo},
		// A bare S is not Save: the panel has text fields, and a bare-key
		// shortcut that fired mid-word would be maddening.
		{sdl.SCANCODE_S, false, false, particleShortcutNone},
		{sdl.SCANCODE_SPACE, true, false, particleShortcutNone},
	} {
		if got := particleShortcutFor(c.sc, c.ctrl, c.shift); got != c.want {
			t.Errorf("scancode %v ctrl=%v shift=%v: got %v, want %v", c.sc, c.ctrl, c.shift, got, c.want)
		}
	}
}

// TestUndoRestoresTheDocumentAndFixesTheIndex — a snapshot may hold fewer
// layers than the document it replaces, and a stale active index would read off
// the end on the next draw.
func TestParticleUndoFixesTheLayerIndex(t *testing.T) {
	prevDoc, prevIdx := particleDoc, particleActiveLayer
	t.Cleanup(func() {
		particleDoc, particleActiveLayer = prevDoc, prevIdx
		ParticleClearUndoHistory()
	})
	ParticleClearUndoHistory()

	particleDoc = particle.NewDoc("e")
	particleActiveLayer = 0

	ParticlePushUndo()
	particleActiveLayer = particleDoc.AddLayer("second")
	if particleActiveLayer != 1 {
		t.Fatalf("AddLayer selected %d, want 1", particleActiveLayer)
	}

	ParticleUndo()
	if len(particleDoc.Layers) != 1 {
		t.Fatalf("undo left %d layers, want 1", len(particleDoc.Layers))
	}
	if particleActiveLayer >= len(particleDoc.Layers) {
		t.Errorf("undo left the active layer at %d with only %d layers",
			particleActiveLayer, len(particleDoc.Layers))
	}

	ParticleRedo()
	if len(particleDoc.Layers) != 2 {
		t.Errorf("redo left %d layers, want 2", len(particleDoc.Layers))
	}
}

// TestPreviewColliderIsAFlatFloor, and is off when the ground plane is hidden —
// otherwise a bounce layer would skip off an invisible surface.
func TestPreviewGroundCollider(t *testing.T) {
	prev := particleShowGround
	t.Cleanup(func() { particleShowGround = prev })

	particleShowGround = true
	pos, n, hit := previewGroundCollide(mgl32.Vec3{0, 1, 0}, mgl32.Vec3{0, -0.5, 0}, 0.1)
	if !hit {
		t.Fatal("a step through the floor did not register")
	}
	if pos.Y() < 0.09 || pos.Y() > 0.11 {
		t.Errorf("stopped at y=%v, want the radius above the floor (0.1)", pos.Y())
	}
	if n != (mgl32.Vec3{0, 1, 0}) {
		t.Errorf("normal %v, want straight up", n)
	}

	if _, _, hit := previewGroundCollide(mgl32.Vec3{0, 3, 0}, mgl32.Vec3{0, 2, 0}, 0.1); hit {
		t.Error("a step well above the floor registered a hit")
	}

	particleShowGround = false
	if _, _, hit := previewGroundCollide(mgl32.Vec3{0, 1, 0}, mgl32.Vec3{0, -0.5, 0}, 0.1); hit {
		t.Error("particles bounced off a floor that is not being drawn")
	}
}

// TestLayerVisibilityIsMutingAndSoloingTogether.
func TestLayerVisibility(t *testing.T) {
	prevDoc, prevSolo := particleDoc, particleSolo
	t.Cleanup(func() { particleDoc, particleSolo = prevDoc, prevSolo })

	particleDoc = &particle.Doc{Layers: []particle.Layer{
		{Name: "a"}, {Name: "b", Muted: true}, {Name: "c"},
	}}

	particleSolo = -1
	if !particleLayerVisible(0) || particleLayerVisible(1) || !particleLayerVisible(2) {
		t.Error("with nothing soloed, muting alone should decide visibility")
	}

	particleSolo = 1
	if particleLayerVisible(0) || !particleLayerVisible(1) || particleLayerVisible(2) {
		t.Error("soloing should show exactly the soloed layer, muted or not")
	}
}
