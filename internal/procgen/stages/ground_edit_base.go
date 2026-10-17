package stages

// ground_edit_base.go — the ground a height layer is added to, for the
// paint brush's FLATTEN mode (paint.ModeFlatten; docs/guide/36-terrain-
// paint.md, "Height and flatten").
//
// Flatten paints a height layer toward the offset that brings the ground to
// one absolute height: offset = target − base, where base is what the layer
// is added to — the source terrain's heights plus the stage's OTHER height
// layers (the layer's own scale taken as 1, which is what a flatten layer
// should have). The editor and `foundry gen paint -mode flatten` both ask
// HeightLayerBase for it, reading the source terrain's last run from its
// output folder; with no run yet there is nothing to level against, and
// flatten falls back to setting the layer toward the value.

import (
	"fmt"
	"path/filepath"

	"foundry/internal/procgen"
	"foundry/internal/procgen/terrain"
)

// HeightLayerBase is the ground under height layer `layer` (file comment):
// the first enabled ground_edit stage that lists it, its source terrain's
// heights from outDir (the document's output folder) and its other height
// layers. It also returns the stage's ID.
func HeightLayerBase(d *procgen.Doc, outDir, layer string) (func(x, z float64) float64, string, error) {
	k := procgen.KindByName(GroundEditKind)
	for i := range d.Stages {
		st := &d.Stages[i]
		if st.Kind != GroundEditKind || st.Disabled {
			continue
		}
		p := procgen.Resolve(k.Params, st.Params)
		found := false
		var others []procgen.Params
		for _, it := range p.Items("height") {
			if it.Str("layer") == layer {
				found = true
			} else {
				others = append(others, it)
			}
		}
		if !found {
			continue
		}
		t, err := terrain.Load(filepath.Join(outDir, p.Str("terrain")))
		if err != nil {
			return nil, st.ID, fmt.Errorf("the terrain %s has no output to flatten against (run it first): %w", p.Str("terrain"), err)
		}
		base := func(x, z float64) float64 {
			h := float64(t.HeightAt(x, z))
			for _, it := range others {
				if l := d.PaintLayer(it.Str("layer")); l != nil {
					h += it.Float("scale") * l.Sample(x, z)
				}
			}
			return h
		}
		return base, st.ID, nil
	}
	return nil, "", fmt.Errorf("no ground_edit stage lists the height layer %s", layer)
}
