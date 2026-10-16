package procgen

// paint_doc_test.go — paint layers in the document: the `paint:` list and a
// mask param through the file, validation, ReadDoc/WriteDoc carrying the
// pixels, and the hash fold (a stage reruns when a layer it reads is
// painted, and only then).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/procgen/paint"
)

func init() {
	RegisterStage(StageKind{
		Name: "tpaint", Category: "Test", Version: 1,
		Params: []ParamDef{
			{Name: "layer", Type: ParamMask, Default: Mask("")},
			{Name: "rules", Type: ParamList, Default: List(), Fields: []ParamDef{
				{Name: "paint", Type: ParamMask, Default: Mask("")},
			}},
		},
		Run: func(rc *RunContext) error { return nil },
	})
}

func paintDoc() *Doc {
	return &Doc{Name: "p", Seed: 3,
		Paint: []paint.LayerDef{{Name: "meadow", Kind: paint.KindDensity}, {Name: "clear", Kind: paint.KindExclude, Resolution: 1}},
		Stages: []Stage{
			{Kind: "tsource", ID: "src", Params: Params{}},
			{Kind: "tpaint", ID: "reads_meadow", Params: Params{"rules": List(Params{"paint": Mask("meadow")})}},
			{Kind: "tpaint", ID: "reads_clear", Params: Params{"layer": Mask("clear")}},
			{Kind: "tpaint", ID: "reads_none", Params: Params{}},
		}}
}

func TestPaintLayersRoundTripThroughTheFile(t *testing.T) {
	d := paintDoc()
	data, err := EncodeDoc(d)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "paint:\n- name: meadow\n  kind: density\n- name: clear\n  kind: exclude\n  resolution: 1\n") {
		t.Fatalf("the paint list is not as written:\n%s", s)
	}
	if !strings.Contains(s, "paint: meadow") || !strings.Contains(s, "layer: clear") {
		t.Fatalf("mask params missing:\n%s", s)
	}
	back, err := DecodeDoc(data)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := EncodeDoc(back)
	if string(again) != s {
		t.Fatalf("not stable:\n%s\nvs\n%s", s, again)
	}
	if back.Stages[1].Params["rules"].Items[0]["paint"].Str != "meadow" {
		t.Error("the list item's mask did not decode")
	}
	// A document without paint writes no paint key at all.
	plain := &Doc{Name: "x", Seed: 1, Stages: []Stage{{Kind: "tsource", ID: "a", Params: Params{}}}}
	if b, _ := EncodeDoc(plain); strings.Contains(string(b), "paint") {
		t.Errorf("a document with no layers mentions paint:\n%s", b)
	}
}

func TestPaintValidation(t *testing.T) {
	d := paintDoc()
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	d.Stages[3].Params["layer"] = Mask("nowhere")
	d.Paint = append(d.Paint, paint.LayerDef{Name: "Bad", Kind: paint.KindDensity}, paint.LayerDef{Name: "meadow", Kind: paint.KindWater})
	var msgs []string
	for _, p := range d.Validate() {
		msgs = append(msgs, p.String())
	}
	all := strings.Join(msgs, "\n")
	for _, want := range []string{`names paint layer "nowhere"`, `paint layer "Bad"`, "meadow is declared twice"} {
		if !strings.Contains(all, want) {
			t.Errorf("no problem saying %q in:\n%s", want, all)
		}
	}
}

// TestReadAndWriteDocCarryThePixels: WriteDoc saves the tiles beside the
// file, ReadDoc reads them back, and a clone is a snapshot.
func TestReadAndWriteDocCarryThePixels(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.fgen")
	d := paintDoc()
	d.EnsurePaintLayer(d.Paint[0]).Apply(paint.Stroke{X: 3, Z: 4, Radius: 6, Strength: 1})
	if err := WriteDoc(path, d); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "p.paint", "meadow", "0_0.png")); err != nil {
		t.Fatalf("no tile written: %v", err)
	}
	back, err := ReadDoc(path)
	if err != nil {
		t.Fatal(err)
	}
	if v := back.PaintLayer("meadow").Sample(3.25, 4.25); v != 1 {
		t.Errorf("read back %.3f at the stroke's centre", v)
	}
	if back.PaintLayer("clear") == nil || !back.PaintLayer("clear").Empty() {
		t.Error("a declared, unpainted layer should read as an empty layer")
	}
	c := back.Clone()
	back.PaintLayer("meadow").Apply(paint.Stroke{X: 3, Z: 4, Radius: 6, Strength: 1, Mode: paint.ModeErase})
	if c.PaintLayer("meadow").Sample(3.25, 4.25) != 1 {
		t.Error("painting the document changed its clone")
	}
}

// TestPaintHashing: no paint, the old hash; painting a layer reruns the
// stages reading it and no others.
func TestPaintHashing(t *testing.T) {
	d := paintDoc()
	h0, err := StageHashes(d)
	if err != nil {
		t.Fatal(err)
	}
	// A stage that names no layer hashes exactly as it would in a document
	// with no paint at all.
	plain := &Doc{Name: "p", Seed: 3, Stages: []Stage{{Kind: "tsource", ID: "src", Params: Params{}},
		{Kind: "tpaint", ID: "reads_none", Params: Params{}}}}
	hp, _ := StageHashes(plain)
	if hp["src"] != h0["src"] || hp["reads_none"] != h0["reads_none"] {
		t.Error("declaring paint layers moved the hash of stages that read none")
	}
	d.EnsurePaintLayer(d.Paint[0]).Apply(paint.Stroke{X: 100, Z: -20, Radius: 5, Strength: 0.5})
	h1, _ := StageHashes(d)
	if h1["reads_meadow"] == h0["reads_meadow"] {
		t.Error("painting meadow did not change the stage reading it")
	}
	for _, id := range []string{"src", "reads_clear", "reads_none"} {
		if h1[id] != h0[id] {
			t.Errorf("painting meadow changed %s", id)
		}
	}
	// Painting it back to nothing is the unpainted hash again.
	l := d.PaintLayer("meadow")
	for i := 0; i < 20 && !l.Empty(); i++ {
		l.Apply(paint.Stroke{X: 100, Z: -20, Radius: 7, Strength: 1, Mode: paint.ModeErase})
	}
	h2, _ := StageHashes(d)
	if h2["reads_meadow"] != h0["reads_meadow"] {
		t.Error("erasing every stroke did not bring the hash back")
	}
	// The preview's hash follows too.
	before := lenientPlan(d)["reads_clear"].hash
	d.EnsurePaintLayer(d.Paint[1]).Apply(paint.Stroke{X: 0, Z: 0, Radius: 3, Strength: 1})
	if lenientPlan(d)["reads_clear"].hash == before {
		t.Error("the preview's hash ignores paint")
	}
}
