package hooks

import "testing"

// A click released and the next one pressed inside one frame must reach the
// script in that order — read back the other way round, the trigger is let go
// while the player is holding it.
func TestFireEdgesKeepTheirOrder(t *testing.T) {
	TakeScriptPrimaryFireEdges()
	ScriptPrimaryFireRequest(false)
	ScriptPrimaryFireRequest(true)
	got := TakeScriptPrimaryFireEdges()
	if len(got) != 2 || got[0] || !got[1] {
		t.Fatalf("edges = %v, want [false true]", got)
	}
	if again := TakeScriptPrimaryFireEdges(); len(again) != 0 {
		t.Errorf("drain left %v behind", again)
	}
}

// Two whole clicks inside one frame are two presses, not one.
func TestFireEdgesKeepEveryClick(t *testing.T) {
	TakeScriptSecondaryFireEdges()
	for i := 0; i < 2; i++ {
		ScriptSecondaryFireRequest(true)
		ScriptSecondaryFireRequest(false)
	}
	if got := TakeScriptSecondaryFireEdges(); len(got) != 4 {
		t.Fatalf("edges = %v, want four", got)
	}
}
