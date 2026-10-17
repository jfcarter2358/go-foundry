package terrain

// provider.go — one region's field, before anything erodes.
//
// A provider is the PRE-EROSION half of the classic pipeline, run for one
// region over just the part of the union it has a share of (its SUPPORT,
// measured by weigh in union.go):
//
//   - the base shape and the mesa field (shape.go) — with the mesa field's
//     tier thresholds and rim gradient measured over the REGION's footprint
//     bounds (Provider.Measure), so `mesa_coverage` is the share of that
//     region its mesas cover, wherever the region sits and however big the
//     union around it is;
//   - the strata offsets (strata.go) — the region's own beds, warped and
//     dipped its own way, kept per sample for the material model;
//   - the canyons (canyon.go), graded on the region's own uncarved surface.
//
// All of it runs in the region's OWN coordinates: world minus the anchor.
// A preset's canyon path drawn from (-300, -120) to (300, 150) lands that
// far from wherever the region's anchor is, and moving a region volume moves
// its mesas with it. The carved paths are handed back in world coordinates.
//
// The sub-grid is a square (the Grid type is square) whose corner is the
// support's corner and whose side is the support's longer side, on the
// union's sample lattice: sample (a, b) of the sub-grid IS union sample
// (i0 + a, j0 + b), so the blend reads it without resampling. Where the
// square runs past the union (a long, thin support) the extra samples cost
// time and are never read.

// providerField is a provider's evaluated sub-grid.
type providerField struct {
	// g is in the provider's own coordinates (world - anchor).
	g      *Grid
	off    []float32
	i0, j0 int
	// measure is the footprint rectangle in the provider's coordinates.
	measure Rect
}

// index is the sub-grid sample of union sample (i, j).
func (p *providerField) index(i, j int) int {
	return (j-p.j0)*p.g.S() + (i - p.i0)
}

// shapeProvider evaluates p's base shape and strata offsets over its
// support on the union grid g (the canyons are carved after, by the
// caller, so the phases time as Generate's do).
func shapeProvider(p *Provider, g *Grid, sup supportRect, pool Pool) (*providerField, error) {
	n := max(sup.i1-sup.i0, sup.j1-sup.j0, 1)
	pg := &Grid{N: n, Cell: g.Cell, MinX: g.X(sup.i0) - p.Anchor[0], MinZ: g.Z(sup.j0) - p.Anchor[1]}
	pg.H = make([]float32, (n+1)*(n+1))
	over := p.Measure
	if over.MaxX <= over.MinX || over.MaxZ <= over.MinZ {
		// No footprint given: measure over the support itself.
		over = Rect{MinX: g.X(sup.i0), MinZ: g.Z(sup.j0), MaxX: g.X(sup.i1), MaxZ: g.Z(sup.j1)}
	}
	over = Rect{MinX: over.MinX - p.Anchor[0], MinZ: over.MinZ - p.Anchor[1], MaxX: over.MaxX - p.Anchor[0], MaxZ: over.MaxZ - p.Anchor[1]}
	if err := baseShape(&p.Settings, pg, over, pool); err != nil {
		return nil, err
	}
	pf := &providerField{g: pg, off: make([]float32, len(pg.H)), i0: sup.i0, j0: sup.j0, measure: over}
	if err := strataOffsets(&p.Settings, pg, pf.off, pool); err != nil {
		return nil, err
	}
	return pf, nil
}
