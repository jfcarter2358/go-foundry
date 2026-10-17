package terrain

// field.go — what the SHARED passes run on.
//
// Since the environment-regions split (docs/regions/PHASE_2.md) the pipeline
// is two halves:
//
//   - PROVIDERS (provider.go): one per region, pre-erosion — the base shape,
//     the mesa field, the canyons, the strata offsets — each evaluated over
//     its own part of the world and blended into one grid by the regions'
//     weights (union.go);
//   - SHARED PASSES over that one grid: drainage (fluvial.go), terrace
//     (strata.go), droplets (droplet.go), thermal (thermal.go), detail
//     (detail.go) and the masks (masks.go).
//
// A field is the shared passes' whole input: the heights, the per-sample
// material model (model.go), the deposit tally, and the KNOBS — the numbers
// that are one per pass rather than one per region (how many drainage
// iterations, how far a droplet runs, the seed the droplets are keyed by).
// Per-region numbers (erodibility, drainage strength, terrace, talus angle,
// detail, cliff mask angle, colours) live in the model and are blended per
// sample.
//
// Generate (terrain.go) is the one-provider case: one material owning every
// sample, the knobs straight from its Settings.

// knobs are the shared passes' per-pass numbers.
type knobs struct {
	seed     uint64
	drainage Drainage
	droplets Droplets
	thermal  Thermal
	// edgeAware spreads the drainage's coarse change back onto the fine
	// grid against height as well as by distance (fluvial.go,
	// edgeAwareChange):
	// a union of several providers turns it on, the classic field never
	// (its heights are pinned by golden tests).
	edgeAware bool
}

func knobsOf(s *Settings) knobs {
	return knobs{seed: s.Seed, drainage: s.Drainage, droplets: s.Droplets, thermal: s.Thermal}
}

// field is a grid and everything the shared passes read and write with it.
type field struct {
	g       *Grid
	m       *fieldModel
	deposit []float32
	k       knobs
	// mfdFlow: the flow mask by multiple flow directions (Union.FlowMFD).
	mfdFlow bool
	// hold is 0..1 per sample: how far applyHold takes the eroded heights
	// back to held, the heights as carved (stamps.go). Nil: none.
	hold, held []float32
}

// drains reports whether any material has a drainage strength.
func (f *field) drains() bool {
	for _, mat := range f.m.mats {
		if mat.drainK > 0 {
			return true
		}
	}
	return false
}
