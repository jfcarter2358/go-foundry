package terrain

// river.go — rivers carved into a finished heightfield (docs/terrain-paint/
// PHASE_W3.md; the `ground_edit` stage, stages/ground_edit_river*.go, is
// what calls it, and the user's page is docs/guide/36-terrain-paint.md,
// "Rivers and streams").
//
// A river arrives as a SPEC: a centreline of world (x, z) points with a
// width and a depth at each, from one of two sources that the stage turns
// into the same thing —
//
//   - PAINTED: a stroke of the river brush, simplified to a spline the
//     author edits afterwards (each point's width and depth too);
//   - DRAINAGE: traced down the ground's own drainage network above a
//     threshold (river_drainage.go), its width growing with the area it
//     drains.
//
// CarveRivers is then a pure function of the heights, the specs and the
// standing water the basins already hold (ponds, lakes):
//
//  1. THE PATH (river_path.go). Each spec is resampled by arc length
//     through a Catmull-Rom spline, with a mild meander only where the spec
//     asks for one (a painted river keeps exactly the wiggles its author
//     drew; a drainage river may be given some). It is ORIENTED DOWNHILL:
//     if the ground under its first stretch is lower than under its last,
//     the path is reversed, so an author can drag the brush either way.
//     Where it runs into standing water it ends there; where it leaves
//     standing water it starts at the water's level (the spill).
//
//  2. THE SURFACE (river_hydro.go). The ground along the channel's middle,
//     less a bank height, is turned into a water surface that NEVER RISES
//     downstream: a running minimum, smoothed by box passes whose minimum
//     with the unsmoothed line keeps it monotone. A tributary that ends on
//     another river (a CONFLUENCE) ends at that river's surface there, which
//     is why the rivers are solved main stems first. A stretch steeper than
//     MaxSlope that drops more than MinFall becomes a WATERFALL: the surface
//     holds its level to the lip and steps down to the foot, a plunge pool
//     is dug below, and a marker (lip, foot, heading, width) is recorded for
//     phase W4's falls mesh and mist.
//
//  3. THE CHARACTER. Pools and riffles alternate every few widths (deeper
//     and slow, shallower and fast and foamy); on a bend the thalweg swings
//     toward the outer bank, leaving a shelving point bar on the inside.
//     The speed follows a discharge held steady along the river (smoothed
//     width × depth), so it is faster through narrows and riffles and slow
//     in pools; the foam is high at riffles, at boulders, at confluences
//     and at a fall's lip and foot. Boulders are dropped in the riffles
//     (when asked for), and their positions feed the foam.
//
//  4. THE CARVE (river_carve.go). Every sample near a river is given its
//     nearest point on the centreline and its signed distance across. Inside
//     the banks the bed is cut to surface − depth × a parabolic section
//     about the (offset) thalweg; outside, ground above a bank profile — a
//     bank lip, a gentle floodplain, then BankSlope — is cut down to it,
//     faded out at the reach (basin.go's bank cut, measured from a line
//     instead of an outline). Where the ground beside the channel is LOWER
//     than the water (a river across a side slope), a low levee holds it.
//     The ground is otherwise only ever lowered, and standing water is left
//     alone.
//
//  5. THE FIELDS, per sample, for the stage: the water level where wet
//     (the water mask and wet ground follow rivers as they follow ponds),
//     the surface carried a few cells past the banks for the ribbon to run
//     under them, the flow (direction × speed), the foam, the across/along
//     coordinates (the ribbon's UVs), and the bed and point-bar shares the
//     dressing reads (masks river_bed and river_bar).
//
// Everything is deterministic: rivers are solved in a fixed order, the
// noise is seeded per river, and ties break by index.

import (
	"math"
)

// RiverShape is how one river is cut and what it looks like (lengths and
// heights in metres).
type RiverShape struct {
	// BankHeight is how far below the ground along the channel's middle the
	// water stands (the bank's lip above the water).
	BankHeight float64
	// Floodplain is the gentle shelf beyond each bank; BankSlope the rise
	// per metre after it; Reach how far past the floodplain the bank cut
	// may reach (faded over its last 40%).
	Floodplain, BankSlope, Reach float64
	// Meander is how far the path may wander off the spline (0: not at
	// all), over MeanderLength.
	Meander, MeanderLength float64
	// Pools is the pool/riffle strength (0 none, 1 strong); RiffleSpacing
	// the distance between riffles (0: six widths).
	Pools, RiffleSpacing float64
	// MaxSlope is the steepest the surface may fall (rise per metre of run)
	// before the stretch becomes a waterfall; MinFall the least drop a fall
	// is worth.
	MaxSlope, MinFall float64
	// Speed is the current at a typical section, m/s.
	Speed float64
	// Boulders is boulders per riffle per 4 m of width (0 none).
	Boulders float64
	// CascadeStep, when above 0, makes a long steep stretch a staircase of
	// falls about this high instead of rapids (river_falls.go; phase W4).
	CascadeStep float64
}

// DefaultRiverShape is a lowland river's.
func DefaultRiverShape() RiverShape {
	return RiverShape{BankHeight: 0.35, Floodplain: 3, BankSlope: 0.35, Reach: 10, MeanderLength: 40, Pools: 0.6,
		MaxSlope: 0.35, MinFall: 0.6, Speed: 0.9}
}

// RiverSpec is one river to carve.
type RiverSpec struct {
	// Name keys it: rivers.yaml, the confluences, the placements.
	Name string
	// Points is the centreline (world x, z), in either direction; Width
	// (bank to bank) and Depth (the thalweg below the surface) are per
	// point (a missing entry repeats the last).
	Points       [][2]float64
	Width, Depth []float64
	// Drainage marks a river traced from the drainage network.
	Drainage bool
	Shape    RiverShape
	Seed     uint64
}

// RiverStation is one resampled point of a river's centreline, downstream
// order.
type RiverStation struct {
	X, Z float64
	// S is the distance from the river's source (m); TX, TZ the unit
	// heading downstream.
	S, TX, TZ float64
	// Ground is the ground along the channel's middle before carving.
	Ground float64
	// Surface is the water surface; Width the bank-to-bank width; Depth
	// the thalweg depth here (pools deeper, riffles shallower, a plunge
	// pool deeper still).
	Surface, Width, Depth float64
	// Offset is the thalweg's offset across (toward the outer bank of a
	// bend; + is left of the heading); Bar the point bar's strength on the
	// inside (0..1).
	Offset, Bar float64
	// Riffle is 0 in a pool .. 1 on a riffle crest; Speed m/s; Foam 0..1.
	Riffle, Speed, Foam float64
}

// Waterfall is a step in a river's surface: W4 builds its falls mesh and
// mist from this.
type Waterfall struct {
	River string
	// X, Z is the lip's centre; Lip and Foot the surface above and below;
	// Drop their difference; DirX, DirZ the heading over the lip; Width the
	// river's width there.
	X, Z, Lip, Foot, Drop, DirX, DirZ, Width float64
	// S is the lip's distance along the river.
	S float64
	// Speed is the current over the lip (m/s): how far the falling sheet
	// is thrown out (river_falls.go, phase W4).
	Speed float64
	// Cascade marks one step of a cascade (a staircase down a long steep
	// stretch, RiverShape.CascadeStep).
	Cascade bool
}

// Confluence is where a tributary ends on another river.
type Confluence struct {
	Main, Tributary string
	// X, Z is the tributary's mouth and Level the surface there.
	X, Z, Level float64
}

// Boulder is a rock dropped in a riffle (the stage places a rock variant
// there; the foam already knows about it).
type Boulder struct {
	River string
	// X, Z its centre, Y the bed under it, Size its diameter (m).
	X, Z, Y, Size float64
}

// River is one carved river (or one run of a river that crosses standing
// water: such a river is split, the runs named <name>, <name>.2...).
type River struct {
	Name     string
	Drainage bool
	// Reversed: the spec ran uphill and was turned round.
	Reversed bool
	// Stations are the centreline, source to mouth.
	Stations []RiverStation
	// Into names the river this one ends on ("" none); IntoWater the
	// standing water body it ends in (-1 none), FromWater the one it
	// leaves (-1 none).
	Into                 string
	IntoWater, FromWater int
	// Length, Drop and the surface at the ends.
	Length, Drop, Source, Mouth float64
	// Spec is the index of the spec it came from.
	Spec int
}

// RiverSample is what the rivers leave at one grid sample.
type RiverSample struct {
	// Owner is the river (an index into RiverResult.Rivers) the sample's
	// surface is; Wet whether water stands there (the surface above the
	// carved ground and within the banks).
	Owner int32
	Wet   bool
	// Surface is the water surface, carried a few cells past the banks so
	// the ribbon runs under them.
	Surface float32
	// FlowX, FlowZ is the current (m/s); Foam 0..1.
	FlowX, FlowZ, Foam float32
	// U across (m, + left of the heading) and V along (m from the source):
	// the ribbon's UVs.
	U, V float32
	// Bed is the channel share (1 in the bed, fading over the banks); Bar
	// the point bar's share.
	Bed, Bar float32
}

// RiverResult is what CarveRivers did.
type RiverResult struct {
	Rivers      []River
	Falls       []Waterfall
	Confluences []Confluence
	Boulders    []Boulder
	// Samples are the grid samples the rivers reach (index j·S + i): the
	// surface within reach of a river, wet or not. Sparse: a kilometre map
	// with one stream holds a few thousand.
	Samples map[int32]RiverSample
	// Changed is how many samples the carve moved.
	Changed int
}

// Wet reports whether sample k holds river water, and its level.
func (r *RiverResult) Wet(k int) (float32, bool) {
	if r == nil {
		return 0, false
	}
	s, ok := r.Samples[int32(k)]
	if !ok || !s.Wet {
		return 0, false
	}
	return s.Surface, true
}

// riverExtendCells is how many cells past its banks a river's surface runs
// on (the ribbon's skirt under the bank), as the pond surface's
// waterShoreCells.
const riverExtendCells = 2.5

// riverEps is the least fall per metre of a river's surface: it never runs
// level, so its direction is never ambiguous.
const riverEps = 1e-4

// CarveRivers carves every spec into g (in place) and returns the rivers
// (file comment). water is the standing water the basins hold (nil: none);
// floor is the lowest a bed may go (-Inf: none).
func CarveRivers(g *Grid, specs []RiverSpec, water *WaterResult, floor float64) *RiverResult {
	res := &RiverResult{Samples: map[int32]RiverSample{}}
	var runs []*riverRun
	for si := range specs {
		runs = append(runs, buildRuns(g, &specs[si], si, water)...)
	}
	if len(runs) == 0 {
		return res
	}
	order := solveConfluences(runs)
	for _, ri := range order {
		solveSurface(runs[ri], runs, water)
	}
	for _, r := range runs {
		shapeRiver(r)
		for fi := range r.falls {
			f := &r.falls[fi]
			for _, st := range r.st {
				if st.S >= f.S-1e-6 {
					f.Speed = round2(st.Speed)
					break
				}
			}
		}
	}
	carveRuns(g, runs, water, floor, res)
	for _, r := range runs {
		res.Rivers = append(res.Rivers, r.river())
		res.Falls = append(res.Falls, r.falls...)
		res.Boulders = append(res.Boulders, r.boulders...)
		if r.into >= 0 {
			m := runs[r.into]
			last := r.st[len(r.st)-1]
			res.Confluences = append(res.Confluences, Confluence{Main: m.name, Tributary: r.name, X: round2(last.X), Z: round2(last.Z),
				Level: round3(last.Surface)})
		}
	}
	return res
}

// river is the run as the result lists it.
func (r *riverRun) river() River {
	rv := River{Name: r.name, Drainage: r.spec.Drainage, Reversed: r.reversed, Stations: r.st, IntoWater: r.intoWater,
		FromWater: r.fromWater, Spec: r.specIdx}
	if r.into >= 0 {
		rv.Into = r.intoName
	}
	if n := len(r.st); n > 0 {
		rv.Length = round2(r.st[n-1].S)
		rv.Source, rv.Mouth = round3(r.st[0].Surface), round3(r.st[n-1].Surface)
		rv.Drop = round3(rv.Source - rv.Mouth)
	}
	return rv
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
