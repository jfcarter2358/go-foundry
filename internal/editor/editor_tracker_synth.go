package editor

// editor_tracker_synth.go — plugs the synth package's recipes into the
// tracker's Generate dialog (editor_tracker_generate.go).
//
// Behind the trackersynth build tag until internal/tracker/synth compiles on
// its own; delete the tag line above to wire the dialog for good.

import (
	"foundry/internal/tracker"
	"foundry/internal/tracker/synth"
)

func init() { trkGenerator = synthGenerator{} }

type synthGenerator struct{}

func (synthGenerator) Recipes() []trkRecipe {
	out := make([]trkRecipe, 0, len(synth.Recipes))
	for i := range synth.Recipes {
		r := &synth.Recipes[i]
		tr := trkRecipe{Name: r.Name, Category: r.Category, Desc: r.Desc, LibraryName: r.LibraryName()}
		for _, p := range r.Params {
			tr.Params = append(tr.Params, trkRecipeParam{Name: p.Name, Default: p.Default, Min: p.Min, Max: p.Max, Help: p.Help})
		}
		out = append(out, tr)
	}
	return out
}

func (synthGenerator) Generate(name string, rate int, p map[string]float32) *tracker.Sample {
	r := synth.ByName(name)
	if r == nil {
		return nil
	}
	return r.Generate(rate, p)
}
