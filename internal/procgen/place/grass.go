package place

// grass.go — a dense sward of grass clumps over the ground, for the grass
// instancer (procgen/stages grass_field, render/grass*.go;
// docs/regions/PRESETS_2B.md).
//
// Scatter (scatter.go) keeps footprints apart and seats each rock with care
// — right for thousands of rocks, far too slow and too careful for the
// hundreds of thousands of overlapping clumps a prairie needs. This is the
// other end: a JITTERED LATTICE per rule at its density (one candidate per
// 1/density m², nudged at random within its lattice cell), each candidate
// kept with probability equal to its weight. The weight is the scatter
// rule's own (Rule.Weight: slope, height, masks, cliff base and the
// clumping noise) times the region's share and the shore band, so a grass
// rule is filtered exactly as a scatter rule with the same bands would be.
//
// EVERYTHING IS KEYED BY WORLD POSITION. The lattice is counted from the
// world origin and each candidate's randomness (jitter, keep, kind, yaw,
// scale, tint) is a hash of the rule's seed and its world lattice cell, so
// a tile's grass does not depend on which tile is asked for or in what
// order (the stage asks per terrain tile, in parallel), and a candidate
// belongs to exactly one tile (the one its jittered point falls in) — no
// seams, no doubles. A region's share and seed come from the run, as for
// scatter (region.go).
//
// SIZE AND TINT. A clump's scale is drawn from the rule's range and
// shortened toward the edges of what the weight allows (EdgeShrink: a
// patch's margin is shorter, sparser grass, not a hedge), and its tint
// mixes two colours by a slow noise (TintScale metres) with a little
// per-clump jitter — the grass instancer multiplies the scan's albedo by it.
//
// ORIENTATION (ground cover, docs/terrain-paint/PHASE_P3.md). A rule with
// Ground set stands its clumps on the terrain's normal under them (a twig
// lies down a bank, a leaf card flat on it), and TiltJitter leans each one
// at random by up to that angle; the axis is the instance's Up, and stays
// zero (upright, the grass instancer's old 12-byte clump) for every other
// rule. The extra randomness is drawn from hash slots no older field used,
// so a rule without either places exactly the clumps it always did.

import (
	"math"

	"foundry/internal/procgen/noise"
)

// GrassRule is one grass rule.
type GrassRule struct {
	Name string
	// Kinds are the kit kinds the rule picks from, uniformly.
	Kinds []int
	// Density is candidates per square metre at full weight.
	Density float64
	// Scale is the clumps' scale range; EdgeShrink (0..1) how much shorter
	// a clump is where its weight is low.
	Scale      [2]float64
	EdgeShrink float64
	// Band carries the mask bands, cliff base and clumping (Rule.Weight);
	// its other fields are unused.
	Band Rule
	// Shore, when Shore[1] > 0, is a band of distance from standing water,
	// metres (reeds at a pond's edge: {0, 6}); ShoreSoft its fade.
	Shore     [2]float64
	ShoreSoft float64
	// MaxDepth is how deep in standing water a clump may still stand,
	// metres (0: never in water).
	MaxDepth float64
	// TintA and TintB are the two colours the tint mixes by a noise of
	// TintScale metres; TintJitter is each clump's own variation (0..1).
	TintA, TintB          [3]float64
	TintScale, TintJitter float64
	// Ground stands the clumps on the terrain's normal (`orient: ground`);
	// TiltJitter leans each by up to this many radians at random.
	Ground     bool
	TiltJitter float64
	// Orient turns each clump's yaw along a direction field (Rule.Orient,
	// OrientYaw: a direction layer, the paths' tangents, downhill) with
	// OrientJitter and OrientOffset (radians); KindYaw, when as long as
	// Kinds, adds a turn per kind (a quarter turn for a piece whose length
	// lies along its X). Nil: random yaws, as before. Shrink, when set,
	// multiplies a clump's scale at (x, z) (a path's wear band: shorter
	// grass). Both read no randomness of their own: positions, kinds, tints
	// and every other clump are unchanged.
	Orient                     func(x, z float64) (dx, dz, strength float64)
	OrientJitter, OrientOffset float64
	KindYaw                    []float64
	Shrink                     func(x, z float64) float64
}

// GrassInstance is one clump.
type GrassInstance struct {
	Kind       int
	Pos        [3]float64
	Yaw, Scale float64
	Tint       [3]float64
	// Up is the axis the clump stands on: zero (upright) unless its rule
	// orients it.
	Up [3]float64
}

// grassUp is the up axis of a clump of rule r at (x, z): the ground's
// normal for a ground rule (else +Y), leaned by up to TiltJitter in a
// direction drawn from the clump's own hash.
func grassUp(g *Ground, r *GrassRule, cell uint64, x, z float64) [3]float64 {
	up := [3]float64{0, 1, 0}
	if r.Ground {
		up = g.Normal(x, z, g.Grid.Cell)
	}
	if r.TiltJitter > 0 {
		// A uniform direction, and an angle weighted toward the full lean
		// (sqrt: uniform over the cap's area).
		phi := unitHash(cell, 8) * 2 * math.Pi
		th := r.TiltJitter * math.Sqrt(unitHash(cell, 9))
		// A tangent basis about up, then up leaned along it.
		t := [3]float64{1, 0, 0}
		if math.Abs(up[0]) > 0.9 {
			t = [3]float64{0, 0, 1}
		}
		b := normalize(cross(up, t))
		t = cross(b, up)
		sp, cp := math.Sincos(phi)
		st, ct := math.Sincos(th)
		for i := range up {
			up[i] = up[i]*ct + (t[i]*cp+b[i]*sp)*st
		}
		up = normalize(up)
	}
	return up
}


// GrassSettings is one grass run over a rectangle of ground.
type GrassSettings struct {
	Rules []GrassRule
	Seed  uint64
	// Region, when set, scales every weight by the region's share.
	Region *Region
	// Avoid is footprints the grass keeps out of (rocks), each shrunk by
	// AvoidShrink (0.7: a clump may touch a boulder's skirt).
	Avoid       []Disc
	AvoidShrink float64
	// Water is standing water's depth (metres, 0 dry) and the distance to
	// it (metres) at a point; nil: no water.
	Water func(x, z float64) (depth, dist float64)
	// CliffThreshold is the cliff level the cliff-base band reads (0: 0.5).
	CliffThreshold float64
	// Margin keeps clumps this far in from the ground's edge.
	Margin float64
}

// grassAvoid is a hash grid of the avoid discs.
type grassAvoid struct {
	cell  float64
	cells map[[2]int64][]Disc
}

func newGrassAvoid(discs []Disc, shrink float64) *grassAvoid {
	if len(discs) == 0 {
		return nil
	}
	if shrink <= 0 {
		shrink = 0.7
	}
	a := &grassAvoid{cell: 8, cells: map[[2]int64][]Disc{}}
	for _, d := range discs {
		d.R *= shrink
		x0, x1 := int64(math.Floor((d.X-d.R)/a.cell)), int64(math.Floor((d.X+d.R)/a.cell))
		z0, z1 := int64(math.Floor((d.Z-d.R)/a.cell)), int64(math.Floor((d.Z+d.R)/a.cell))
		for i := x0; i <= x1; i++ {
			for j := z0; j <= z1; j++ {
				a.cells[[2]int64{i, j}] = append(a.cells[[2]int64{i, j}], d)
			}
		}
	}
	return a
}

func (a *grassAvoid) hit(x, z float64) bool {
	if a == nil {
		return false
	}
	for _, d := range a.cells[[2]int64{int64(math.Floor(x / a.cell)), int64(math.Floor(z / a.cell))}] {
		if dx, dz := x-d.X, z-d.Z; dx*dx+dz*dz < d.R*d.R {
			return true
		}
	}
	return false
}

// unitHash is a hash as a float in [0, 1).
func unitHash(words ...uint64) float64 {
	return float64(noise.Hash64(words...)>>11) / float64(1<<53)
}

// Grass places every rule's clumps whose points fall in rect (min x, min z,
// max x, max z — half-open), in rule then lattice order.
func Grass(g *Ground, s GrassSettings, rect [4]float64) []GrassInstance {
	var out []GrassInstance
	var cf *CliffField
	avoid := newGrassAvoid(s.Avoid, s.AvoidShrink)
	thr := s.CliffThreshold
	if thr <= 0 {
		thr = 0.5
	}
	for ri := range s.Rules {
		r := &s.Rules[ri]
		if r.Density <= 0 || len(r.Kinds) == 0 {
			continue
		}
		if r.Band.CliffBase > 0 && cf == nil {
			cf = g.CliffField(thr)
		}
		seed := noise.Hash64(s.Seed, 0x6772_6173, uint64(ri))
		wseed := noise.Hash64(seed, 0x636c)
		tseed := noise.Hash64(seed, 0x7469)
		h := 1 / math.Sqrt(r.Density)
		i0, i1 := int64(math.Floor(rect[0]/h))-1, int64(math.Ceil(rect[2]/h))+1
		j0, j1 := int64(math.Floor(rect[1]/h))-1, int64(math.Ceil(rect[3]/h))+1
		for j := j0; j <= j1; j++ {
			for i := i0; i <= i1; i++ {
				cell := noise.Hash64(seed, uint64(i), uint64(j))
				x := (float64(i) + unitHash(cell, 1)) * h
				z := (float64(j) + unitHash(cell, 2)) * h
				if x < rect[0] || x >= rect[2] || z < rect[1] || z >= rect[3] || !g.Contains(x, z, s.Margin) {
					continue
				}
				w := r.Band.Weight(g, cf, wseed, x, z)
				if w <= 0 {
					continue
				}
				w *= s.Region.At(x, z)
				if w <= 0 {
					continue
				}
				if s.Water != nil {
					depth, dist := s.Water(x, z)
					if depth > r.MaxDepth {
						continue
					}
					if r.Shore[1] > 0 {
						w *= band(dist, r.Shore[0], r.Shore[1], math.Max(r.ShoreSoft, 1e-3), 0, math.Inf(1))
					}
				} else if r.Shore[1] > 0 {
					continue // a shore rule with no water grows nowhere
				}
				w = math.Min(w, 1)
				if unitHash(cell, 3) >= w || avoid.hit(x, z) {
					continue
				}
				sc := r.Scale[0] + (r.Scale[1]-r.Scale[0])*unitHash(cell, 4)
				sc *= 1 - r.EdgeShrink*(1-w)
				n := 0.5
				if r.TintScale > 0 {
					n = noise.FBM2(tseed, x/r.TintScale, z/r.TintScale, 3, 0.5)*0.5 + 0.5
					n = math.Min(math.Max(n, 0), 1)
				}
				jit := 1 + r.TintJitter*(unitHash(cell, 6)*2-1)
				var tint [3]float64
				for c := range tint {
					tint[c] = (r.TintA[c] + (r.TintB[c]-r.TintA[c])*n) * jit
				}
				ki := int(noise.Hash64(cell, 5) % uint64(len(r.Kinds)))
				if r.Shrink != nil {
					sc *= r.Shrink(x, z)
				}
				in := GrassInstance{
					Kind: r.Kinds[ki],
					Pos:  [3]float64{x, g.Height(x, z), z},
					Yaw:  unitHash(cell, 7) * 2 * math.Pi, Scale: sc, Tint: tint,
				}
				if r.Orient != nil {
					off := r.OrientOffset
					if len(r.KindYaw) == len(r.Kinds) {
						off += r.KindYaw[ki]
					}
					in.Yaw = OrientYaw(r.Orient, x, z, in.Yaw, r.OrientJitter, off, false)
				}
				if r.Ground || r.TiltJitter > 0 {
					in.Up = grassUp(g, r, cell, x, z)
				}
				out = append(out, in)
			}
		}
	}
	return out
}
