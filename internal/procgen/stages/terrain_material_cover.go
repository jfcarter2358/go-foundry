package stages

// terrain_material_cover.go — COVER RULES: optional ground layers laid over
// a terrain_material's four (docs/regions/PRESETS.md, "Engine work",
// item 2).
//
// The four rules of terrain_material.go (sand, cracked, gravel, rock by
// slope, flow, deposition and cliff) paint canyon country, and with other
// textures a savannah. Other environments need ground that is not decided by
// how the land was ERODED but by what lies on it:
//
//   - SNOW (`snow`): by altitude — fading in between the two heights of
//     `snow_height` — and slope — holding below the first of `snow_slope`,
//     gone by the second, so cliffs stay dark rock — with noise bending
//     both edges (`snow_breakup`, `snow_breakup_scale`) into drifts and
//     bare patches;
//   - WET ground (`wet`): near standing water — within `wet_distance` of it
//     and at most `wet_height` above its surface — and under it (a shallow
//     pond's floor is mud, not grass). The water is the terrain's own
//     (`mask_water`, which a region terrain writes when a region's preset
//     fills water in; terrain/water.go). A terrain without water has no wet
//     ground;
//   - ASH and cinders (`ash`): round volcanic vents — full within the first
//     of `ash_radius` (in cone radii) of a vent, gone by the second — from
//     the vents the terrain's landforms placed (terrain.yaml `vents:`).
//     Not on cliffs.
//
// Each is a LAYER KEY like sand/cracked/gravel/rock: a layer of the textures
// stage. Empty (the default) is off; with none set nothing here runs and the
// stage's output is the four-layer material it always was, byte for byte.
//
// They apply in that order, each over everything before it: a cover's share
// c takes c of every layer below it (all scaled by 1 − c). Snow comes last,
// so it lies over ash and mud alike. The weights are 7 channels per sample
// then — the four, wet, ash, snow — and a material with any cover is an
// eight-layer splat (its covers are layers 5–7, weights2.png). A region
// material dedupes cover layers across environments like the others.
//
// The far backdrop takes snow and ash by the same rules from its own
// heights and slopes (seen from afar, a snow line on the ranges matters);
// wet ground needs the water mask, which it has none of.
//
// LAVA LOBES (`lava_lobes`, PRESETS_6). The terrain's volcanoes run lava
// tongues out over the field (terrain.yaml `lobes:`), but the four rules
// paint by slope and flow, so a lobe's flat top took the same patches of
// bare lava as the field round it and the tongues did not read. With
// lava_lobes > 0 the CRACKED layer (a volcanic preset's bare lava) takes
// that share of the ground on every lobe's outline — the very outline the
// heightfield raised (terrain.LobeMeta.Cover) — before the covers stack, so
// ash and snow still lie over it.
//
// THE SNOW LINE'S REFERENCE (`snow_reference`, docs/regions/PRESETS_6.md).
// `world` (the default, and what every existing document keeps) reads
// `snow_height` in world metres. That assumed the shared 0 m base, and in a
// multi-region world it is not robust: a massif's height depends on how far
// its footprint lets `edge_rise` climb and on its neighbours' blend, so the
// same preset showed no snow at all in a narrow region and would be white to
// the foot in a wide one, and the tundra's 1.5–4.5 m patches turned to a
// snowfield wherever its band rose towards a range. `region` reads
// `snow_height` as FRACTIONS of the environment's own height range — from
// the 2nd to the 98th percentile of the terrain's heights where its biome
// weight is at least 0.9 (its core, not the bands it shares; the whole
// terrain on a classic one-environment terrain) — so the line sits at the
// same share of the massif's relief whatever its size, base or neighbours,
// and the band towards a higher neighbour (heights past the core's top)
// takes snow as the slope climbs.

import (
	"math"
	"sort"

	"foundry/internal/procgen"
	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/terrain"
)

// Cover roles: weight channels after the four of splatLayerKeys.
const (
	roleWet  = 4
	roleAsh  = 5
	roleSnow = 6
	// groundRoles is every role: the four rules and the three covers.
	groundRoles = 7
)

// groundRoleKeys are the terrain_material layer keys of every role, in
// weight-channel order.
var groundRoleKeys = [groundRoles]string{"sand", "cracked", "gravel", "rock", "wet", "ash", "snow"}

// coverParams are terrain_material's cover parameters (Added).
func coverParams() []procgen.ParamDef {
	f, s, v2 := procgen.Float, procgen.String, procgen.Vec2
	return []procgen.ParamDef{
		{Name: "snow", Added: true, Type: procgen.ParamString, Default: s(""),
			Doc: "The textures stage's layer for snow, laid over everything above the snow line on gentle ground. Empty: none."},
		{Name: "snow_height", Added: true, Type: procgen.ParamVec2, Label: "Snow height (m)", Default: v2(300, 400), Min: -5000, Max: 10000, Step: 0.01,
			Doc: "Snow fades in between these heights: world metres, or with snow_reference region fractions (0–1) of the environment's own height range."},
		{Name: "snow_reference", Added: true, Type: procgen.ParamEnum, Label: "Snow reference", Default: procgen.Enum("world"),
			Choices: []string{"world", "region"},
			Doc: "What snow_height is measured in: world metres (world), or fractions of the environment's own height range — " +
				"2nd to 98th percentile where its biome weight is at least 0.9 — so a region's snow line does not depend on its size or base (region)."},
		{Name: "snow_slope", Added: true, Type: procgen.ParamVec2, Label: "Snow slope (°)", Default: v2(35, 50), Min: 0, Max: 90, Step: 0.5,
			Doc: "Snow holds on ground gentler than the first and is gone by the second: cliffs stay rock."},
		{Name: "snow_breakup", Added: true, Type: procgen.ParamFloat, Label: "Snow breakup", Default: f(0.5), Min: 0, Max: 1, Step: 0.01,
			Doc: "How much noise bends the snow line and its slope limit into drifts and bare patches."},
		{Name: "snow_breakup_scale", Added: true, Type: procgen.ParamFloat, Label: "Snow breakup scale (m)", Default: f(40), Min: 1, Max: 5000, Step: 1,
			Doc: "The size of the drifts and bare patches."},
		{Name: "wet", Added: true, Type: procgen.ParamString, Default: s(""),
			Doc: "The textures stage's layer for wet ground (mud, wet sand) under and beside standing water — the terrain's own " +
				"(a region terrain fills seas and ponds when a preset asks). Empty: none."},
		{Name: "wet_distance", Added: true, Type: procgen.ParamFloat, Label: "Wet distance (m)", Default: f(8), Min: 0, Max: 1000, Step: 0.5,
			Doc: "How far from the water's edge the ground is wet."},
		{Name: "wet_height", Added: true, Type: procgen.ParamFloat, Label: "Wet height (m)", Default: f(1.5), Min: 0, Max: 100, Step: 0.1,
			Doc: "How far above the water's surface it is wet."},
		{Name: "ash", Added: true, Type: procgen.ParamString, Default: s(""),
			Doc: "The textures stage's layer for ash and cinders round volcanic vents (the terrain's volcanoes). Empty: none."},
		{Name: "lava_lobes", Added: true, Type: procgen.ParamFloat, Label: "Lava on lobes", Default: f(0), Min: 0, Max: 1, Step: 0.01,
			Doc: "The share of the ground the cracked layer (bare lava) takes on the terrain's lava lobes, so the tongues read against the field. 0: off."},
		{Name: "ash_cover", Added: true, Type: procgen.ParamFloat, Label: "Ash cover", Default: f(1), Min: 0, Max: 1, Step: 0.01,
			Doc: "The most of the ground the ash takes: below 1 the cinders show through it (a dusting rather than a blanket)."},
		{Name: "ash_hole", Added: true, Type: procgen.ParamFloat, Label: "Ash hole (cones)", Default: f(0), Min: 0, Max: 50, Step: 0.05,
			Doc: "No ash within this distance from a vent, in cone radii, fading in over the last fifth: with ash_radius past it the ash is an apron round the cone's foot, not a cap. 0: none."},
		{Name: "ash_radius", Added: true, Type: procgen.ParamVec2, Label: "Ash radius (cones)", Default: v2(1, 2.2), Min: 0, Max: 50, Step: 0.05,
			Doc: "Ash covers the ground within the first of these distances from a vent and is gone by the second, in cone radii."},
	}
}

// coverRules is a terrain_material's active covers.
type coverRules struct {
	// on is each cover role's switch (index role-roleWet).
	on               [3]bool
	snowH, snowS     [2]float64
	// snowRel: snowH are fractions of [snowLo, snowHi], the environment's
	// own height range (snow_reference region; snowRangeOf).
	snowRel          bool
	snowLo, snowHi   float64
	snowBrk, snowSc  float64
	wetDist, wetH    float64
	ashR             [2]float64
	ashHole, ashCover float64
	vents            []terrain.VentMeta
	// lava is lava_lobes' share; lobes the terrain's lava lobes.
	lava             float64
	lobes            []terrain.LobeMeta
	breakup, breakSc float64
	seed             uint64
}

// coverKeys reports whether params p set any cover layer key.
func coverKeys(p procgen.Params) bool {
	return p.Str("wet") != "" || p.Str("ash") != "" || p.Str("snow") != "" || p.Float("lava_lobes") > 0
}

// coverRulesOf is p's covers over terrain t (vents from its meta), nil when
// p sets none.
func coverRulesOf(p procgen.Params, vents []terrain.VentMeta, seed uint64) *coverRules {
	if !coverKeys(p) {
		return nil
	}
	return &coverRules{
		on:    [3]bool{p.Str("wet") != "", p.Str("ash") != "", p.Str("snow") != ""},
		snowH: p.Vec2("snow_height"), snowS: p.Vec2("snow_slope"), snowRel: p.Str("snow_reference") == "region",
		snowBrk: p.Float("snow_breakup"), snowSc: math.Max(p.Float("snow_breakup_scale"), 1),
		wetDist: p.Float("wet_distance"), wetH: p.Float("wet_height"),
		ashR: p.Vec2("ash_radius"), ashHole: p.Float("ash_hole"), ashCover: p.Float("ash_cover"), vents: vents,
		lava: noise.Clamp(p.Float("lava_lobes"), 0, 1),
		breakup: p.Float("breakup"), breakSc: math.Max(p.Float("breakup_scale"), 0.5),
		seed: noise.Hash64(seed, 0x434F_5652), // "COVR"
	}
}

// coverSample is what a cover rule reads at one sample.
type coverSample struct {
	x, z, h, slope float64
	// water: the distance to the nearest standing water (0 in it) and the
	// height above its surface there; hasWater false where the terrain has
	// none within reach.
	waterDist, waterAbove float64
	hasWater              bool
}

// shares is each cover's share at s (index role-roleWet), before stacking.
func (cr *coverRules) shares(s *coverSample) (out [3]float64) {
	sm := noise.SmoothStep
	n := func(k uint64, scale float64) float64 {
		return noise.FBM2(noise.Hash64(cr.seed, k), s.x/scale, s.z/scale, 3, 0.5)
	}
	if cr.on[0] && s.hasWater {
		d := s.waterDist + n(1, cr.breakSc)*cr.breakup*cr.wetDist*0.35
		out[0] = (1 - sm(0, math.Max(cr.wetDist, 1e-3), d)) * (1 - sm(cr.wetH*0.5, math.Max(cr.wetH, 1e-3), s.waterAbove))
	}
	if cr.on[1] && len(cr.vents) > 0 {
		best := math.Inf(1)
		for _, v := range cr.vents {
			best = math.Min(best, math.Hypot(s.x-v.X, s.z-v.Z)/math.Max(v.Radius, 1))
		}
		d := best + n(2, cr.breakSc*2)*cr.breakup*0.3
		out[1] = (1 - sm(cr.ashR[0], math.Max(cr.ashR[1], cr.ashR[0]+1e-3), d)) * (1 - sm(42, 55, s.slope))
		if cr.ashHole > 0 {
			out[1] *= sm(cr.ashHole*0.8, cr.ashHole, d)
		}
		out[1] *= noise.Clamp(cr.ashCover, 0, 1)
	}
	if cr.on[2] {
		b := n(3, cr.snowSc) * cr.snowBrk
		h, minSpan := s.h, 1.0
		if cr.snowRel {
			h, minSpan = (s.h-cr.snowLo)/math.Max(cr.snowHi-cr.snowLo, 1), 0.01
		}
		span := math.Max(cr.snowH[1]-cr.snowH[0], minSpan)
		alt := sm(cr.snowH[0], cr.snowH[1], h+b*span*0.6)
		steep := sm(cr.snowS[0], math.Max(cr.snowS[1], cr.snowS[0]+1e-3), s.slope+n(4, cr.snowSc*0.5)*cr.snowBrk*8)
		out[2] = alt * (1 - steep)
	}
	return out
}

// lavaAt is the lava lobes' share at (x, z): the strongest lobe's outline
// times lava_lobes.
func (cr *coverRules) lavaAt(x, z float64) float64 {
	if cr.lava <= 0 {
		return 0
	}
	best := 0.0
	for i := range cr.lobes {
		best = math.Max(best, cr.lobes[i].Cover(x, z))
	}
	return best * cr.lava
}

// stack lays the covers over a sample's four weights w4 (summing to one),
// writing its seven into out. lava (lavaAt) first takes its share of the
// four for the cracked layer.
func (cr *coverRules) stack(w4 []float32, c [3]float64, out []float32, lava float64) {
	for i := 0; i < 4; i++ {
		out[i] = w4[i]
	}
	if lava > 0 {
		k := float32(1 - noise.Clamp(lava, 0, 1))
		for i := 0; i < 4; i++ {
			out[i] *= k
		}
		out[1] += float32(noise.Clamp(lava, 0, 1))
	}
	for i := 4; i < groundRoles; i++ {
		out[i] = 0
	}
	for r := 0; r < 3; r++ {
		if !cr.on[r] || c[r] <= 0 {
			continue
		}
		k := float32(1 - noise.Clamp(c[r], 0, 1))
		for i := 0; i < groundRoles; i++ {
			out[i] *= k
		}
		out[roleWet+r] += float32(noise.Clamp(c[r], 0, 1))
	}
}

// snowRangeOf is the height range a `snow_reference: region` snow line is
// measured over: the 2nd and 98th percentiles of t's heights where core (an
// environment's biome mask) is at least 0.9 — or 0.5, or anywhere, when a
// sliver of a region has no such core; core nil is the whole terrain. Zero
// when p's snow is not region-referenced (it is then never read).
func snowRangeOf(p procgen.Params, t *terrain.Terrain, core []float32) [2]float64 {
	if p.Str("snow") == "" || p.Str("snow_reference") != "region" {
		return [2]float64{}
	}
	h := t.Grid.H
	// Every 4th sample in each direction: the percentiles of a 1 m grid
	// need not read all of it.
	s := t.Grid.S()
	pick := func(min float32) []float64 {
		var v []float64
		for j := 0; j < s; j += 4 {
			for i := 0; i < s; i += 4 {
				k := j*s + i
				if core == nil || core[k] >= min {
					v = append(v, float64(h[k]))
				}
			}
		}
		return v
	}
	v := pick(0.9)
	if len(v) < 16 {
		v = pick(0.5)
	}
	if len(v) < 16 {
		v = pick(-1)
	}
	if len(v) == 0 {
		return [2]float64{}
	}
	sort.Float64s(v)
	at := func(q float64) float64 { return v[min(int(q*float64(len(v))), len(v)-1)] }
	return [2]float64{at(0.02), at(0.98)}
}

// withSnowRange sets a region-referenced snow line's height range (a no-op
// for a world-referenced one or no covers).
func (cr *coverRules) withSnowRange(r [2]float64) *coverRules {
	if cr != nil {
		cr.snowLo, cr.snowHi = r[0], r[1]
	}
	return cr
}

// coverGroundWeights is groundWeights with the covers of p laid over: seven
// weights a sample (the four, wet, ash, snow), or groundWeights' own four
// when p sets no cover (nc reports which: 4 or 7). A region-referenced snow
// line is measured over the whole terrain (coverGroundWeightsCore takes an
// environment's biome mask instead).
func coverGroundWeights(t *terrain.Terrain, p procgen.Params, seed uint64, pool terrain.Pool) (w []float32, nc int, err error) {
	return coverGroundWeightsCore(t, p, seed, pool, nil)
}

// coverGroundWeightsCore is coverGroundWeights with a region-referenced
// snow line measured over core (an environment's biome mask; snowRangeOf).
func coverGroundWeightsCore(t *terrain.Terrain, p procgen.Params, seed uint64, pool terrain.Pool, core []float32) (w []float32, nc int, err error) {
	w4, err := groundWeights(t, p, seed, pool)
	if err != nil {
		return nil, 0, err
	}
	cr := coverRulesOf(p, t.Meta.Vents, seed).withSnowRange(snowRangeOf(p, t, core))
	if cr == nil {
		return w4, 4, nil
	}
	cr.lobes = t.Meta.Lobes
	g := t.Grid
	sz := g.S()
	slope, err := t.Mask(terrain.MaskSlope)
	if err != nil {
		return nil, 0, err
	}
	var wd, wa []float32
	if cr.on[0] {
		wd, wa = waterProximity(t, cr.wetDist*2+cr.wetH)
	}
	out := make([]float32, groundRoles*sz*sz)
	err = pool.Parallel(sz, func(j int) error {
		for i := 0; i < sz; i++ {
			k := j*sz + i
			s := coverSample{x: g.X(i), z: g.Z(j), h: float64(g.H[k]), slope: float64(slope[k]) * 90}
			if wd != nil && !math.IsInf(float64(wd[k]), 1) {
				s.waterDist, s.waterAbove, s.hasWater = float64(wd[k]), float64(wa[k]), true
			}
			cr.stack(w4[4*k:4*k+4], cr.shares(&s), out[groundRoles*k:groundRoles*k+groundRoles], cr.lavaAt(s.x, s.z))
		}
		return nil
	})
	return out, groundRoles, err
}

// waterProximity is each sample's distance (metres) to the nearest sample
// under standing water and its height above that water's surface, +Inf
// past reach metres or where the terrain has no water mask. A chamfer
// transform that carries the nearest water's level along with the distance.
func waterProximity(t *terrain.Terrain, reach float64) (dist, above []float32) {
	m, err := t.Mask(terrain.MaskWater)
	g := t.Grid
	n := len(g.H)
	dist = make([]float32, n)
	above = make([]float32, n)
	inf := float32(math.Inf(1))
	for k := range dist {
		dist[k] = inf
	}
	if err != nil {
		return dist, above
	}
	s := g.S()
	level := make([]float32, n)
	for k, v := range m {
		if v > 0 {
			dist[k] = 0
			level[k] = g.H[k] + v*float32(terrain.WaterMaskDepth)
		}
	}
	cell := float32(g.Cell)
	diag := cell * float32(math.Sqrt2)
	relax := func(k, q int, step float32) {
		if d := dist[q] + step; d < dist[k] {
			dist[k], level[k] = d, level[q]
		}
	}
	for j := 0; j < s; j++ {
		for i := 0; i < s; i++ {
			k := j*s + i
			if i > 0 {
				relax(k, k-1, cell)
			}
			if j > 0 {
				relax(k, k-s, cell)
				if i > 0 {
					relax(k, k-s-1, diag)
				}
				if i+1 < s {
					relax(k, k-s+1, diag)
				}
			}
		}
	}
	for j := s - 1; j >= 0; j-- {
		for i := s - 1; i >= 0; i-- {
			k := j*s + i
			if i+1 < s {
				relax(k, k+1, cell)
			}
			if j+1 < s {
				relax(k, k+s, cell)
				if i+1 < s {
					relax(k, k+s+1, diag)
				}
				if i > 0 {
					relax(k, k+s-1, diag)
				}
			}
		}
	}
	for k := range dist {
		if float64(dist[k]) > reach {
			dist[k] = inf
			continue
		}
		above[k] = g.H[k] - level[k]
	}
	return dist, above
}

// farCoverWeights lays the covers of p (snow and ash; the backdrop has no
// water) over the backdrop's four weights w4 (n² lattice of cell metres
// from fm's corner, heights h): seven a sample, or w4 itself (4) when p
// sets no cover. snowRange is the map's (snowRangeOf), so a region-
// referenced snow line on the backdrop is the map's own line.
func farCoverWeights(parallel func(n int, f func(i int) error) error, fm *terrain.FarMeta, h []float32, w4 []float32,
	p procgen.Params, vents []terrain.VentMeta, seed uint64, snowRange [2]float64) ([]float32, int, error) {
	cr := coverRulesOf(p, vents, seed).withSnowRange(snowRange)
	if cr == nil {
		return w4, 4, nil
	}
	n, c := fm.Samples, fm.Cell
	at := func(i, j int) float64 { return float64(h[min(max(j, 0), n-1)*n+min(max(i, 0), n-1)]) }
	out := make([]float32, groundRoles*n*n)
	err := parallel(n, func(j int) error {
		z := fm.Bounds.MinZ + float64(j)*c
		for i := 0; i < n; i++ {
			x := fm.Bounds.MinX + float64(i)*c
			gx := (at(i+1, j) - at(i-1, j)) / (2 * c)
			gz := (at(i, j+1) - at(i, j-1)) / (2 * c)
			// The lattice's slopes are gentler than the map's (as the rock
			// band's, farSlopeEase).
			s := coverSample{x: x, z: z, h: at(i, j), slope: math.Atan(math.Hypot(gx, gz))*180/math.Pi + farSlopeEase}
			k := j*n + i
			cr.stack(w4[4*k:4*k+4], cr.shares(&s), out[groundRoles*k:groundRoles*k+groundRoles], 0)
		}
		return nil
	})
	return out, groundRoles, err
}
