package terrain

// thermal.go — talus: rock above its angle of repose slides down.
//
// Weathered rock falls until the slope it lands on can hold it. Each
// iteration, every pair of neighbouring samples (8-connected) whose drop
// exceeds what the higher one's angle allows moves Rate/4 of the excess
// downhill. The angle is the strata's: a soft layer (hardness 0) relaxes to
// TalusAngle, the scree slope; a hard layer (hardness toward 1) stands at up
// to CliffAngle. So the cliffs terrace made keep their faces, the soft
// slopes between them become smooth scree, and the material that comes off
// piles up at the bottom of each cliff as an apron — the talus a desert
// canyon wall stands in.
//
// Jacobi-style: each iteration reads one buffer and writes the other, and
// every sample computes the flux on each of its eight pairs from the same
// numbers its neighbour does, so the update is conservative, symmetric and
// independent of scheduling. Rows run in parallel.

// thermal relaxes the field. The iterations and rate are the knobs'; the
// angle at each sample is the material model's (each region's own talus and
// cliff angles, at its own strata).
func thermal(fd *field, pool Pool, progress func(float32)) error {
	g, deposit := fd.g, fd.deposit
	t := fd.k.thermal
	if t.Iterations <= 0 || t.Rate <= 0 {
		return nil
	}
	sz := g.S()
	start := append([]float32(nil), g.H...)
	src, dst := g.H, make([]float32, len(g.H))
	limit := make([]float32, len(g.H)) // allowed drop per cell of distance
	// A quarter of Rate per pair: a sample with all eight neighbours below
	// it sheds at most 2·Rate of its largest excess, which stays stable for
	// Rate ≤ 0.5 (the schema's range is clamped there).
	rate := float32(min(t.Rate, 0.5)) / 4
	cell := float32(g.Cell)
	for it := 0; it < t.Iterations; it++ {
		if err := rows(pool, sz, func(j int) {
			for i := 0; i < sz; i++ {
				k := j*sz + i
				limit[k] = fd.m.talus(k, src[k]) * cell
			}
		}); err != nil {
			return err
		}
		if err := rows(pool, sz, func(j int) {
			for i := 0; i < sz; i++ {
				k := j*sz + i
				h := src[k]
				net := float32(0)
				for d := 0; d < 8; d++ {
					ni, nj := i+nbDI[d], j+nbDJ[d]
					if ni < 0 || nj < 0 || ni >= sz || nj >= sz {
						continue
					}
					n := nj*sz + ni
					hn := src[n]
					if h > hn {
						if ex := (h - hn) - limit[k]*nbDist[d]; ex > 0 {
							net -= ex * rate
						}
					} else if hn > h {
						if ex := (hn - h) - limit[n]*nbDist[d]; ex > 0 {
							net += ex * rate
						}
					}
				}
				dst[k] = h + net
			}
		}); err != nil {
			return err
		}
		src, dst = dst, src
		if progress != nil {
			progress(float32(it+1) / float32(t.Iterations))
		}
	}
	if &src[0] != &g.H[0] {
		copy(g.H, src)
	}
	for k := range g.H {
		if dh := g.H[k] - start[k]; dh > 0 {
			deposit[k] += dh
		}
	}
	return nil
}
