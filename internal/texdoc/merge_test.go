package texdoc

import (
	"bytes"
	"testing"
)

// TestMergeLayerDownMatchesFlatten: merging must not change what the texture
// looks like, and must leave texels the upper layer doesn't cover untouched.
func TestMergeLayerDownMatchesFlatten(t *testing.T) {
	d := bumpDoc(t, 4, 4)
	for i := range d.Layers[0].Idx {
		d.Layers[0].Idx[i] = 5
	}
	if err := d.AddLayer("Paint"); err != nil {
		t.Fatal(err)
	}
	d.Layers[1].Idx[0] = 9
	d.Layers[1].Idx[3] = 12
	d.Active = 1
	want := d.Flatten().Pix

	if err := d.MergeLayerDown(1); err != nil {
		t.Fatalf("MergeLayerDown: %v", err)
	}
	if len(d.Layers) != 1 || d.Active != 0 {
		t.Fatalf("layers = %d, active = %d; want 1 layer, active 0", len(d.Layers), d.Active)
	}
	if got := d.Flatten().Pix; !bytes.Equal(got, want) {
		t.Error("merged texture looks different from the unmerged one")
	}
	if got := d.Layers[0].Idx; got[0] != 9 || got[3] != 12 || got[1] != 5 {
		t.Errorf("merged texels = %v, want 9 and 12 over 5s", got[:4])
	}
}

func TestMergeLayerDownRefusals(t *testing.T) {
	d := bumpDoc(t, 2, 2)
	if err := d.MergeLayerDown(0); err == nil {
		t.Error("merged the bottom layer into nothing")
	}
	_ = d.AddLayer("hidden")
	d.Layers[1].Visible = false
	if err := d.MergeLayerDown(1); err == nil {
		t.Error("merged a hidden layer")
	}
	if len(d.Layers) != 2 {
		t.Errorf("a refused merge changed the stack to %d layers", len(d.Layers))
	}
}

// TestMergeLayerDownHeights merges on the bump channel with the height
// arithmetic, not as palette indices.
func TestMergeLayerDownHeights(t *testing.T) {
	d := bumpDoc(t, 2, 2)
	d.SetChannel(ChanBump)
	d.Layers[0].Idx[0] = 100
	_ = d.AddLayer("h")
	d.Layers[1].Idx[0] = 200
	d.Layers[1].Idx[1] = 50
	d.Layers[1].Opacity = 0.5
	want := d.Height()

	if err := d.MergeLayerDown(1); err != nil {
		t.Fatalf("MergeLayerDown: %v", err)
	}
	got := d.Height()
	for i := range want {
		if diff := got[i] - want[i]; diff > 1.0/255 || diff < -1.0/255 {
			t.Errorf("height[%d] = %v after merge, want %v", i, got[i], want[i])
		}
	}
}
