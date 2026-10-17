package place

// region.go — placement for one environment region of a world
// (docs/regions/SPEC.md, "Biome-weighted placement"; docs/regions/PHASE_4.md).
//
// A level generation doc grows each region's placement stages once per
// region, over the ONE union terrain every region shares. Each such run
// places only its own region's share:
//
//   - every candidate's weight is multiplied by the region's weight at the
//     candidate (Region.At): 1 deep inside the footprint, falling across
//     the transition band, so the last desert boulders thin out among the
//     first savannah shrubs instead of stopping at a line;
//   - nothing is placed where that weight is 0. There is deliberately no
//     clip to the region's band rectangle: the regions package hands a
//     sample no footprint covers (the union's square padding, a gap
//     between footprints) to the nearest regions, smoothly, so a region's
//     weight can be well above 0 past its band — a clip there drew a
//     straight line where its rocks stopped (seen in the band-density
//     test). Blocks the region has no share of cost one weight lookup a
//     cell and place nothing;
//   - the randomness is keyed by WORLD position — a scatter's 32 m blocks
//     are counted from the world origin and seeded by their world
//     coordinates, a vegetation grid starts on a world multiple of its cell
//     — and by the region's own seed (the run engine derives it from the
//     doc's seed, the region's key and the preset stage), so a region's
//     placements depend only on its own inputs and the ground under it:
//     the union's extent, another region's preset or the order of the
//     regions change nothing.
//
// A nil *Region is the classic single-terrain run, unchanged to the byte.

// Region is the region a placement runs for.
type Region struct {
	// Weight is the region's share of world point (x, z), 0..1; safe for
	// concurrent use. Nil: 1 everywhere.
	Weight func(x, z float64) float64
}

// At is the region's weight at (x, z) (1 for a nil Region).
func (r *Region) At(x, z float64) float64 {
	if r == nil || r.Weight == nil {
		return 1
	}
	return r.Weight(x, z)
}
