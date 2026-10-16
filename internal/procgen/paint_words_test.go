package procgen

// paint_words_test.go — a mask param's MaskWords (phase P4's orient_to:
// downhill, paths): a word validates without a layer, names no layer for
// the hash fold, and leaves a real layer name working as before.

import (
	"testing"

	"foundry/internal/procgen/paint"
)

func TestMaskWordsNameNoLayer(t *testing.T) {
	defs := []ParamDef{{Name: "rules", Type: ParamList, Default: List(), Fields: []ParamDef{
		{Name: "orient_to", Type: ParamMask, Default: Mask(""), MaskWords: []string{"downhill", "paths"}},
	}}}
	d := &Doc{Name: "w", Paint: []paint.LayerDef{{Name: "flow", Kind: paint.KindDirection}}}
	p := Params{"rules": List(Params{"orient_to": Mask("downhill")}, Params{"orient_to": Mask("flow")},
		Params{"orient_to": Mask("paths")})}
	if refs := MaskRefs(defs, p); len(refs) != 1 || refs[0] != "flow" {
		t.Fatalf("refs %v, want just the layer", refs)
	}
	if probs := validateParams(d, 0, "s", "", defs, p); len(probs) != 0 {
		t.Fatalf("words did not validate: %v", probs)
	}
	bad := Params{"rules": List(Params{"orient_to": Mask("uphill")})}
	if probs := validateParams(d, 0, "s", "", defs, bad); len(probs) != 1 {
		t.Fatalf("an undeclared name validated: %v", probs)
	}
	if !defs[0].Fields[0].IsMaskWord("paths") || defs[0].Fields[0].IsMaskWord("flow") {
		t.Fatal("IsMaskWord")
	}
}
