package procgen

// paint_edit_test.go — renaming and deleting a paint layer, and the base
// hash and paint keys a per-tile rerun is keyed by.

import (
	"testing"

	"foundry/internal/procgen/paint"
)

func TestRenameAndDeletePaintLayer(t *testing.T) {
	d := paintDoc()
	d.EnsurePaintLayer(*d.PaintDef("meadow")).Apply(paint.Stroke{X: 5, Z: 5, Radius: 3, Strength: 1})
	n, err := d.RenamePaintLayer("meadow", "lawn")
	if err != nil || n != 1 {
		t.Fatalf("rename: %d params, %v", n, err)
	}
	if d.PaintDef("meadow") != nil || d.PaintDef("lawn") == nil {
		t.Fatal("the declaration was not renamed")
	}
	if got := d.Stages[1].Params["rules"].Items[0]["paint"].Str; got != "lawn" {
		t.Fatalf("the rule reads %q", got)
	}
	l := d.PaintLayer("lawn")
	if l == nil || l.Empty() {
		t.Fatal("the pixels did not follow the rename")
	}
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems after rename: %v", probs)
	}
	if users := d.PaintLayerUsers("lawn"); len(users) != 1 || users[0] != "reads_meadow" {
		t.Errorf("users %v", users)
	}
	if _, err := d.RenamePaintLayer("lawn", "clear"); err == nil {
		t.Error("renamed onto an existing layer")
	}
	if _, err := d.RenamePaintLayer("lawn", "Bad Name"); err == nil {
		t.Error("renamed to an invalid name")
	}
	n, err = d.DeletePaintLayer("clear")
	if err != nil || n != 1 || d.PaintDef("clear") != nil {
		t.Fatalf("delete: %d, %v", n, err)
	}
	if d.Stages[2].Params["layer"].Str != "" {
		t.Error("a mask still names the deleted layer")
	}
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems after delete: %v", probs)
	}
}

func TestBaseHashIgnoresPaintAndPaintKeyFollowsIt(t *testing.T) {
	d := paintDoc()
	env, err := PlanEnv(d, "reads_meadow")
	if err != nil {
		t.Fatal(err)
	}
	base := env.BaseHash
	hs, _ := StageHashes(d)
	h0 := hs["reads_meadow"]
	if !env.ReadsPaint() || !env.ReadsOwnPaint() {
		t.Fatal("a stage naming a layer does not read paint")
	}
	rect := [4]float64{0, 0, 64, 64}
	if k := env.PaintKey(rect, 0); k != "" {
		t.Fatalf("nothing painted, key %q", k)
	}
	d.EnsurePaintLayer(*d.PaintDef("meadow")).Apply(paint.Stroke{X: 10, Z: 10, Radius: 3, Strength: 1})
	env, _ = PlanEnv(d, "reads_meadow")
	hs, _ = StageHashes(d)
	if env.BaseHash != base {
		t.Error("painting moved the base hash")
	}
	if hs["reads_meadow"] == h0 {
		t.Error("painting left the stage hash alone")
	}
	if env.PaintKey(rect, 0) == "" {
		t.Error("the painted tile's key is empty")
	}
	if env.PaintKey([4]float64{200, 200, 264, 264}, 0) != "" {
		t.Error("a far tile's key follows the stroke")
	}
	none, _ := PlanEnv(d, "reads_none")
	if none.ReadsPaint() || none.PaintKey(rect, 0) != "" {
		t.Error("a stage naming no layer reads paint")
	}
}
