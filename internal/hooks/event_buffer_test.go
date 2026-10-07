package hooks

import "testing"

// TestEventBufferDrainsOnceAndReusesItsArrays pins the double buffer's
// contract: a take returns exactly what was recorded since the last one, nil
// when nothing was, the slice it returned survives the next frame's recording,
// and after the first two frames no take allocates.
func TestEventBufferDrainsOnceAndReusesItsArrays(t *testing.T) {
	var b eventBuffer[int]
	if got := b.take(); got != nil {
		t.Fatalf("empty take = %v, want nil", got)
	}
	b.record(1)
	b.record(2)
	first := b.take()
	b.record(3)
	if len(first) != 2 || first[0] != 1 || first[1] != 2 {
		t.Fatalf("first take = %v, overwritten by the next frame's recording", first)
	}
	second := b.take()
	if len(second) != 1 || second[0] != 3 {
		t.Fatalf("second take = %v, want [3]", second)
	}
	if got := b.take(); got != nil {
		t.Fatalf("take after drain = %v, want nil", got)
	}
	allocs := testing.AllocsPerRun(100, func() {
		b.record(4)
		b.record(5)
		_ = b.take()
	})
	if allocs != 0 {
		t.Errorf("steady-state record/take allocates %.1f times", allocs)
	}
}
