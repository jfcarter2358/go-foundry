package app

// script_reference_ui_test.go — the F1 reference must show every property table
// there is.
//
// refKindOrder is a hand-written list, and it has silently missed three kinds
// before: sprite, emitter and sound had property tables that no one could find
// in the reference, because nothing checked. This is that check.

import (
	"testing"

	"foundry/internal/script"
)

func TestReferenceListsEveryPropertyKind(t *testing.T) {
	shown := map[uint8]bool{}
	for _, k := range refKindOrder {
		shown[uint8(k.Kind)] = true
	}
	for _, k := range script.PropertyKinds() {
		if !shown[uint8(k)] {
			t.Errorf("%s has a property table that the F1 reference never shows; "+
				"add it to refKindOrder", k)
		}
	}
}

func TestReferenceKindsAllHaveProperties(t *testing.T) {
	// The other direction: a label in the list with nothing behind it is an
	// empty section in the reference.
	for _, k := range refKindOrder {
		if len(script.PropertyDocs(k.Kind)) == 0 {
			t.Errorf("the reference lists %q, which has no properties", k.Label)
		}
	}
}
