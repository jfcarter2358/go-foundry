package place

// scatter.go — rule lists of rocks over the ground.
//
// A rule is a family of rocks (one rocks stage's variants) and where they
// go. Scatter runs the rules IN ORDER — list the big ones first: a boulder
// field is laid before the rubble that fills round it — and each in three
// steps:
//
//  1. WEIGHT. Every point of the ground gets a weight in 0..1, the product of
//     soft bands on its masks — slope (degrees), height (a fraction of the
//     terrain's range), flow, deposition, curvature and cliff — times the
//     cliff-base term (near the FOOT of a wall: the cliff field, ground.go)
//     and a clumping noise (Cluster: patches of ground carry the rocks and
//     the ground between them is bare, which is what keeps a field from
//     reading as sprinkled).
//  2. CANDIDATES. The ground is cut into blocks (32 m); each block's weight
//     is integrated over 2 m cells, which gives its expected count at the
//     rule's density (per 100 m² of full weight), and that many candidates
//     are dropped into its cells in proportion to their weight. Blocks run
//     side by side, each from its own RNG stream (seed, rule, block), so the
//     candidates are the same on any worker count. SATELLITES add
//     candidates round the rocks earlier rules kept — rubble skirting a
//     boulder, chips under a fallen block — still weighed by the masks.
//     Every candidate draws its variant, scale (a truncated power law,
//     exponent ScaleBias: many small, few big), yaw, tilt and sink up front.
//  3. KEEP. Candidates are taken biggest first and kept only if their
//     footprint keeps clear of every footprint already kept, this rule's and
//     the earlier rules' and the Avoid list's (cliff walls), at Spacing times
//     the sum of the radii — a Poisson-disk test with variable radii — and
//     only if the ground under them is not so rough the rock would be mostly
//     buried. Serial, in a fixed order, so deterministic; it is a hash-grid
//     lookup a candidate and costs little next to step 1.
//
// SEATING (seat): the rock is turned toward the ground's normal (averaged
// over its footprint) by Align, tilted by up to Tilt about a random
// horizontal axis and yawed at random; then the ring of its footprint — the
// rock's base, at half its base band — is carried through the rotation and
// the rock lowered until no point of the ring is above the ground (so its
// downhill side does not float), then sunk a further Sink of its height
// (less for a flat-based variant).
//
// COLLISION by size: a rock smaller than CollisionMin collides as nothing
// (pebbles: a body would only snag on them), one up to HullMax as its hulls
// when its cook built them, anything larger as its mesh.

import (
	"fmt"
	"math"
	"sort"

	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/terrain"
)

// Rule is one scatter rule.
type Rule struct {
	Name     string
	Variants []Variant
	// Density is candidates per 100 m² of ground at full weight.
	Density float64
	// Spacing is how far apart footprints keep: centres at least Spacing ×
	// the sum of the radii (1: footprints just touch).
	Spacing float64
	// Scale is the placement scale's range; ScaleBias the power law's
	// exponent (0 uniform; 2–3 is a natural rock size distribution).
	Scale     [2]float64
	ScaleBias float64
	// The mask bands: Slope in degrees, Height a fraction of the ground's
	// range, the rest 0..1 masks. Soft is the fade beyond a unit band's
	// ends, SlopeSoft a slope band's (degrees).
	Slope, Height, Flow, Deposition, Curvature, Cliff [2]float64
	Soft, SlopeSoft                                   float64
	// CliffBase (0..1) restricts the rule toward the foot of cliffs: 0
	// ignores them, 1 only within CliffBaseDistance metres below one.
	CliffBase, CliffBaseDistance float64
	// Cluster (0..1) clumps the rule in patches about ClusterScale metres
	// across.
	Cluster, ClusterScale float64
	// Satellites is candidates per metre of footprint radius round each
	// rock the earlier rules kept, out to SatelliteSpread radii beyond it.
	Satellites, SatelliteSpread float64
	// Align (0..1) turns rocks toward the ground's normal; Tilt (degrees)
	// is a random tilt on top; Sink (a fraction of the height) how far
	// they sit in the ground.
	Align, Tilt, Sink float64
	// MaxBury (a fraction of the height): a candidate whose footprint's
	// ground rises more than this across it is dropped.
	MaxBury float64
	// Collision is auto, hulls, mesh, box or none; auto picks by size with
	// CollisionMin and HullMax (metres, the scaled largest extent).
	Collision             string
	CollisionMin, HullMax float64
	// MaxInstances caps the rule (0: no cap).
	MaxInstances int
}

// DefaultRule is a rule with every field at the stage's default: boulders
// on gentle ground, a little clumped.
func DefaultRule() Rule {
	return Rule{
		Name: "rocks", Density: 1, Spacing: 1, Scale: [2]float64{0.6, 1.6}, ScaleBias: 2.2,
		Slope: [2]float64{0, 40}, Height: [2]float64{0, 1}, Flow: [2]float64{0, 1}, Deposition: [2]float64{0, 1},
		Curvature: [2]float64{0, 1}, Cliff: [2]float64{0, 0.6}, Soft: 0.1, SlopeSoft: 6,
		CliffBaseDistance: 25, Cluster: 0.5, ClusterScale: 25, SatelliteSpread: 2.5,
		Align: 0.6, Tilt: 12, Sink: 0.1, MaxBury: 0.6,
		Collision: "auto", CollisionMin: 0.35, HullMax: 4, MaxInstances: 20000,
	}
}

// ScatterSettings is one scatter.
type ScatterSettings struct {
	Rules []Rule
	Seed  uint64
	// CliffThreshold is the cliff mask level that counts as cliff for the
	// cliff-base term.
	CliffThreshold float64
	// Margin keeps footprints this far in from the ground's edge.
	Margin float64
	// Avoid is footprints already on the ground (a cliff wall's chunks).
	Avoid []Disc
	// Block is the candidate blocks' side (0: 32 m); Cell the weight cells'
	// (0: max(2 m, the ground's cell)).
	Block, Cell float64
	// Progress, when set, is called with 0..1.
	Progress func(float32)
}

// RuleStats is what a rule did.
type RuleStats struct {
	Name string
	// Expected is the candidates its density asked for over its weight;
	// Candidates what were thrown (with satellites); Kept, and why the rest
	// were not.
	Expected                                  float64
	Candidates, Satellites, Kept              int
	TooClose, TooRough, OffGround, OverTheCap int
	// Area is the ground its weight covers, m² (the weight integrated).
	Area float64
}

// ScatterResult is the instances, every rule's in rule order, and the stats.
type ScatterResult struct {
	Instances []Instance
	Stats     []RuleStats
}

// candidate is a rock that might be placed, its randomness drawn.
type candidate struct {
	x, z    float64
	variant int
	scale   float64
	r       float64 // footprint radius, scaled
	yaw     float64
	tiltAx  float64 // the tilt axis' heading
	tilt    float64 // radians
	sink    float64 // 0.6..1.4: the sink's jitter
	order   int     // stable tie-break: where it was drawn
}

// Scatter places every rule over g.
func Scatter(g *Ground, s ScatterSettings, pool terrain.Pool) (*ScatterResult, error) {
	if s.Block <= 0 {
		s.Block = 32
	}
	if s.Cell <= 0 {
		s.Cell = math.Max(2, g.Grid.Cell)
	}
	if s.CliffThreshold <= 0 {
		s.CliffThreshold = 0.5
	}
	for i := range s.Rules {
		if len(s.Rules[i].Variants) == 0 {
			return nil, fmt.Errorf("rule %d (%s) has no variants", i+1, s.Rules[i].Name)
		}
	}
	needCliff := false
	maxSp := 1.0
	for _, r := range s.Rules {
		needCliff = needCliff || r.CliffBase > 0
		maxSp = math.Max(maxSp, r.Spacing)
	}
	var cf *CliffField
	if needCliff && g.HasMask(terrain.MaskCliff) {
		cf = g.CliffField(s.CliffThreshold)
	}
	grid := newDiscGrid(2, maxSp)
	for _, d := range s.Avoid {
		grid.add(d, 1)
	}
	res := &ScatterResult{}
	for ri := range s.Rules {
		if err := pool.Err(); err != nil {
			return nil, err
		}
		r := &s.Rules[ri]
		st, err := scatterRule(g, cf, &s, ri, r, grid, res, pool)
		if err != nil {
			return nil, err
		}
		res.Stats = append(res.Stats, st)
		if s.Progress != nil {
			s.Progress(float32(ri+1) / float32(len(s.Rules)))
		}
	}
	return res, nil
}

// Weight is rule r's weight at (x, z) (cf may be nil: no cliff term).
func (r *Rule) Weight(g *Ground, cf *CliffField, seed uint64, x, z float64) float64 {
	w := band(g.SlopeDeg(x, z), r.Slope[0], r.Slope[1], r.SlopeSoft, 0, 90)
	if w == 0 {
		return 0
	}
	if span := g.Hi - g.Lo; span > 0 && (r.Height[0] > 0 || r.Height[1] < 1) {
		w *= band((g.Height(x, z)-g.Lo)/span, r.Height[0], r.Height[1], r.Soft, 0, 1)
	}
	unit := func(name string, b [2]float64) {
		if w > 0 && (b[0] > 0 || b[1] < 1) && g.HasMask(name) {
			w *= band(g.Mask(name, x, z), b[0], b[1], r.Soft, 0, 1)
		}
	}
	unit(terrain.MaskFlow, r.Flow)
	unit(terrain.MaskDeposition, r.Deposition)
	unit(terrain.MaskCurvature, r.Curvature)
	unit(terrain.MaskCliff, r.Cliff)
	if w == 0 {
		return 0
	}
	if r.CliffBase > 0 {
		near := 0.0
		if cf != nil {
			d, below := cf.At(x, z)
			near = (1 - smooth(0, math.Max(r.CliffBaseDistance, 0.1), d)) * below
		}
		w *= 1 - r.CliffBase + r.CliffBase*near
	}
	if r.Cluster > 0 {
		sc := math.Max(r.ClusterScale, 0.5)
		n := noise.FBM2(seed, x/sc, z/sc, 3, 0.5)*0.5 + 0.5
		// Patches: the noise's upper part, sharper as Cluster grows, scaled
		// so the mean weight stays about the same.
		c := smooth(0.5-0.5*(1-r.Cluster)-0.05, 0.5+0.1*r.Cluster, n)
		w *= (1 - r.Cluster) + r.Cluster*c*1.8
	}
	return math.Min(w, 1.5)
}

// powerLaw draws from a truncated power law p(s) ∝ s^-a on [lo, hi].
func powerLaw(rng *noise.RNG, lo, hi, a float64) float64 {
	lo = math.Max(lo, 1e-4)
	hi = math.Max(hi, lo)
	u := rng.Float64()
	if hi == lo {
		return lo
	}
	if math.Abs(a-1) < 1e-6 {
		return lo * math.Pow(hi/lo, u)
	}
	e := 1 - a
	l, h := math.Pow(lo, e), math.Pow(hi, e)
	return math.Pow(l+u*(h-l), 1/e)
}

// draw fills a candidate's randomness.
func (r *Rule) draw(rng *noise.RNG, x, z float64, order int) candidate {
	c := candidate{x: x, z: z, order: order}
	c.variant = rng.Intn(len(r.Variants))
	c.scale = powerLaw(rng, r.Scale[0], r.Scale[1], r.ScaleBias)
	c.r = r.Variants[c.variant].FootprintRadius * c.scale
	c.yaw = rng.Range(0, 2*math.Pi)
	c.tiltAx = rng.Range(0, 2*math.Pi)
	c.tilt = rng.Float64() * r.Tilt * math.Pi / 180
	c.sink = rng.Range(0.6, 1.4)
	return c
}

func scatterRule(g *Ground, cf *CliffField, s *ScatterSettings, ri int, r *Rule, grid *discGrid,
	res *ScatterResult, pool terrain.Pool) (RuleStats, error) {
	st := RuleStats{Name: r.Name}
	b := g.Bounds()
	nbx := int(math.Ceil((b[2] - b[0]) / s.Block))
	nbz := int(math.Ceil((b[3] - b[1]) / s.Block))
	per := int(math.Ceil(s.Block / s.Cell))
	cell := s.Block / float64(per)
	seed := noise.Hash64(s.Seed, 0x7363, uint64(ri))
	wseed := noise.Hash64(seed, 0x636c)
	type blockOut struct {
		cands    []candidate
		expected float64
		area     float64
	}
	outs := make([]blockOut, nbx*nbz)
	err := pool.Parallel(nbx*nbz, func(bi int) error {
		bx, bz := bi%nbx, bi/nbx
		x0, z0 := b[0]+float64(bx)*s.Block, b[1]+float64(bz)*s.Block
		ws := make([]float64, per*per)
		sum := 0.0
		for j := 0; j < per; j++ {
			for i := 0; i < per; i++ {
				x, z := x0+(float64(i)+0.5)*cell, z0+(float64(j)+0.5)*cell
				if !g.Contains(x, z, 0) {
					continue
				}
				w := r.Weight(g, cf, wseed, x, z)
				ws[j*per+i] = w
				sum += w
			}
		}
		o := &outs[bi]
		o.area = sum * cell * cell
		o.expected = r.Density / 100 * o.area
		if sum == 0 || r.Density <= 0 {
			return nil
		}
		rng := noise.NewRNG(noise.Hash64(seed, uint64(bi)))
		n := int(o.expected)
		if rng.Chance(o.expected - float64(n)) {
			n++
		}
		// The cells' cumulative weight, to drop candidates in proportion.
		cum := make([]float64, len(ws))
		acc := 0.0
		for k, w := range ws {
			acc += w
			cum[k] = acc
		}
		for k := 0; k < n; k++ {
			u := rng.Float64() * acc
			ci := sort.SearchFloat64s(cum, u)
			ci = min(ci, len(cum)-1)
			i, j := ci%per, ci/per
			x := x0 + (float64(i)+rng.Float64())*cell
			z := z0 + (float64(j)+rng.Float64())*cell
			o.cands = append(o.cands, r.draw(rng, x, z, bi<<20|k))
		}
		return nil
	})
	if err != nil {
		return st, err
	}
	var cands []candidate
	for i := range outs {
		st.Expected += outs[i].expected
		st.Area += outs[i].area
		cands = append(cands, outs[i].cands...)
	}
	// Satellites round what the earlier rules kept.
	if r.Satellites > 0 && len(res.Instances) > 0 {
		hosts := res.Instances
		sats := make([][]candidate, len(hosts))
		err := pool.Parallel(len(hosts), func(hi int) error {
			h := &hosts[hi]
			rng := noise.NewRNG(noise.Hash64(seed, 0x736174, uint64(hi)))
			e := r.Satellites * h.Radius
			n := int(e)
			if rng.Chance(e - float64(n)) {
				n++
			}
			for k := 0; k < n; k++ {
				a := rng.Range(0, 2*math.Pi)
				// Denser near the host, thinning out to the spread.
				d := h.Radius * (1 + r.SatelliteSpread*math.Pow(rng.Float64(), 1.6))
				x, z := h.Pos[0]+d*math.Cos(a), h.Pos[2]+d*math.Sin(a)
				if !g.Contains(x, z, 0) || !rng.Chance(math.Min(r.Weight(g, cf, wseed, x, z), 1)) {
					continue
				}
				sats[hi] = append(sats[hi], r.draw(rng, x, z, 1<<40|hi<<12|k))
			}
			return nil
		})
		if err != nil {
			return st, err
		}
		for _, sc := range sats {
			st.Satellites += len(sc)
			cands = append(cands, sc...)
		}
	}
	st.Candidates = len(cands)
	sort.SliceStable(cands, func(a, b int) bool {
		if cands[a].r != cands[b].r {
			return cands[a].r > cands[b].r
		}
		return cands[a].order < cands[b].order
	})
	for k := range cands {
		c := &cands[k]
		if k&4095 == 0 {
			if err := pool.Err(); err != nil {
				return st, err
			}
		}
		if r.MaxInstances > 0 && st.Kept >= r.MaxInstances {
			st.OverTheCap = len(cands) - k
			break
		}
		if !g.Contains(c.x, c.z, s.Margin+c.r) {
			st.OffGround++
			continue
		}
		d := Disc{c.x, c.z, c.r}
		if !grid.clear(d, r.Spacing) {
			st.TooClose++
			continue
		}
		in, ok := seat(g, r, c)
		if !ok {
			st.TooRough++
			continue
		}
		in.Rule = ri
		grid.add(d, r.Spacing)
		res.Instances = append(res.Instances, in)
		st.Kept++
	}
	return st, nil
}

// seat stands candidate c on the ground (see the file comment). False when
// the ground under it is too rough.
func seat(g *Ground, r *Rule, c *candidate) (Instance, bool) {
	v := &r.Variants[c.variant]
	s := c.scale
	n := g.Normal(c.x, c.z, math.Max(c.r, g.Grid.Cell))
	up := [3]float64{0, 1, 0}
	nu := normalize(add(scale(up, 1-r.Align), scale(n, r.Align)))
	tax := [3]float64{math.Cos(c.tiltAx), 0, math.Sin(c.tiltAx)}
	rot := FromTo(up, nu).Mul(RotAxis(tax, c.tilt)).Mul(RotY(c.yaw))
	y, lo, hi := seatHeight(g, rot, c.x, c.z, v.FootprintRadius*s, 0.5*v.BaseBand*s)
	h := v.Height * s
	if hi-lo > r.MaxBury*h {
		return Instance{}, false
	}
	sink := r.Sink * h * c.sink
	if v.FlatBase {
		sink *= 0.4
	}
	in := Instance{Variant: c.variant, Pos: [3]float64{c.x, y - sink, c.z}, Rot: rot, Scale: s, Radius: c.r}
	in.Collision = collisionFor(r.Collision, v, s, r.CollisionMin, r.HullMax)
	return in, true
}

// seatHeight is the height to put a rock's origin at so that no point of
// its base ring — radius rad at height band in its own frame, carried
// through rot — stands above the ground, nor its centre; and the lowest and
// highest of the ground-minus-ring heights (their difference is how much of
// the ring the uphill side buries).
func seatHeight(g *Ground, rot Mat3, x, z, rad, band float64) (y, lo, hi float64) {
	y = g.Height(x, z)
	lo, hi = y, y
	for k := 0; k < 12; k++ {
		sn, cs := math.Sincos(float64(k) * math.Pi / 6)
		o := rot.Apply([3]float64{rad * cs, band, rad * sn})
		need := g.Height(x+o[0], z+o[2]) - o[1]
		y = math.Min(y, need)
		lo, hi = math.Min(lo, need), math.Max(hi, need)
	}
	return y, lo, hi
}

// collisionFor is a placement's collision for variant v at scale s.
func collisionFor(choice string, v *Variant, s, minSize, hullMax float64) string {
	if choice != "auto" && choice != "" {
		return choice
	}
	ext := math.Max(v.Extent[0], math.Max(v.Extent[1], v.Extent[2])) * s
	switch {
	case v.PlaceCollision == "none" || ext < minSize:
		return "none"
	case v.Collision == "hulls" && ext <= hullMax:
		return "hulls"
	}
	return "mesh"
}
