package terrain

// river_falls.go — a waterfall's falling sheet, the notch it falls through,
// and cascades (docs/terrain-paint/PHASE_W4.md; the step itself is found
// by river_hydro.go's findFalls, the mesh and the mist are placed by the
// ground_edit stage, stages/ground_edit_falls.go).
//
// THE CURTAIN. W3 left a fall as a step in the river's surface: on the
// grid, a strip of ribbon cells pitched from the lip's level to the
// foot's. Seen from the plunge pool that is a tilted sheet of flat water,
// not a fall. The curtain is the sheet the water actually makes: a grid
// of vertices from a short level lead-in over the lip (so it meets the
// ribbon upstream with no gap), down a BALLISTIC profile to a little
// below the pool's surface:
//
//	along(t) = vx · t,   y(t) = lip − ½ g t²,   t ∈ [0, √(2·(drop + sink)/g)]
//
// vx is the current over the lip (the river's speed at the lip station,
// 0.6–4 m/s), so a fast river throws its sheet out and a trickle falls
// close to the rock; the landing distance is then held between 0.12 and
// 0.5 of the drop (plus a little), so a fast, low fall does not shoot
// out over the far side of its pool and a slow, tall one does not hug
// the face. Rows are spaced evenly in height, so a tall fall gets the
// rows its curve needs. The sheet spreads a little as it falls (up to a
// quarter of its width over a tall drop), as falling water does.
//
// Each vertex carries what the falls shader path wants (water.glsl,
// waterFallsShade): U across (m) and V down the sheet (m of arc from the
// lip, negative on the lead-in) for the streaks, which scroll down V; and
// three 0..1 values the stage puts in the vertex colour — the AERATION
// (glassy at a small fall's lip, white and opaque toward the foot), the
// EDGE (0 at the sheet's sides, so the sides fray instead of ending in a
// cut line) and the PROGRESS down the fall.
//
// THE NOTCH. The carve already lowers the channel between the lip and the
// foot to the pool (the surface holds the foot's level there), so the
// ground at a fall is a step. But the step is a slope over a cell or two
// of grid, and a curtain arcing off the lip can pass through it, or hang
// beside a bank that was cut only within the channel's width. So every
// sample under the curtain's footprint — from just past the lip to a cell
// past where the sheet lands, across its width plus a cell — is lowered
// to at least notchClear below the sheet over it: the fall drops through
// a notch whose back is the face behind the water and whose sides stand
// clear of the sheet. Only ever lowered, never below the floor, and
// standing water is left alone.
//
// CASCADES. W3 made a long steep stretch (longer than three widths and
// twice its drop: a stream down a valley side) RAPIDS — a sloping, foamy
// surface — because holding the surface to the top of the slope and
// dropping it at the bottom would cut a slot gorge the length of it. With
// RiverShape.CascadeStep > 0 such a stretch becomes a STAIRCASE instead:
// k steps of about CascadeStep metres each (at least two, each at least
// MinFall), the surface level from just past one step's lip to the next
// lip, so each pool is dug at most one step deep into the slope. Every
// step is a Waterfall (Cascade set) with its own small plunge pool,
// curtain, notch and mist. 0 (the default) keeps W3's rapids.

import (
	"math"

	"foundry/internal/procgen/noise"
)

// fallsGravity is g, m/s².
const fallsGravity = 9.81

// fallsSink is how far below the pool's surface the curtain ends, so its
// foot is never seen to stop short of the water as the pool's waves move.
const fallsSink = 0.25

// notchClear is how far below the falling sheet the notch keeps the
// ground (m).
const notchClear = 0.2

// FallsCurtain is one waterfall's falling sheet: Rows × Cols vertices,
// row-major, row 0 the lead-in upstream of the lip, the last row a little
// below the pool's surface (file comment).
type FallsCurtain struct {
	Rows, Cols int
	// P is each vertex in world space.
	P [][3]float64
	// U across (m, + left of the heading) and V down the sheet (m of arc
	// from the lip; negative on the lead-in).
	U, V []float64
	// Aeration, Edge and Progress per vertex, 0..1 (file comment).
	Aeration, Edge, Progress []float64
	// Out is how far past the lip (along the heading) the sheet meets the
	// pool; VX the horizontal speed its profile uses; Sink how far below
	// the lip its last row is; LipWidth and FootWidth its width at the lip
	// and at the pool.
	Out, VX, Sink, LipWidth, FootWidth float64
	// ColVX is each column's own throw (VX varied along the lip: an uneven
	// lip throws some of the water further than the rest) and ColLip its
	// lip height offset (m); VXMin is the least of ColVX, OutMax the
	// furthest any column lands (fixes after W5, the file comment's
	// VARIATION).
	ColVX, ColLip []float64
	VXMin, OutMax float64
}

// SheetY is the curtain's lowest height a horizontal distance `along` past
// the lip (the lip's level at and before it): the column thrown least, at
// the lowest lip, which is what the notch must clear.
func (c *FallsCurtain) SheetY(lip, along float64) float64 {
	vx := c.VXMin
	if vx <= 0 {
		vx = c.VX
	}
	low := 0.0
	for _, o := range c.ColLip {
		low = math.Min(low, o)
	}
	if along <= 0 || vx <= 0 {
		return lip + low
	}
	t := along / vx
	return lip + low - math.Min(0.5*fallsGravity*t*t, c.Sink)
}

// HalfWidth is the curtain's half-width `along` past the lip.
func (c *FallsCurtain) HalfWidth(along float64) float64 {
	f := noise.Clamp(along/math.Max(c.Out, 1e-3), 0, 1)
	return 0.5 * (c.LipWidth + (c.FootWidth-c.LipWidth)*f)
}

// Curtain is the waterfall's falling sheet on a grid of the given cell size
// (file comment).
func (f Waterfall) Curtain(cell float64) FallsCurtain {
	drop := math.Max(f.Lip-f.Foot, 0.05)
	v0 := noise.Clamp(f.Speed, 0.6, 4)
	h := drop + fallsSink
	tEnd := math.Sqrt(2 * h / fallsGravity)
	out := noise.Clamp(v0*tEnd, 0.12*drop+0.1, 0.5*drop+0.4)
	c := FallsCurtain{VX: out / tEnd, Out: out, Sink: h}
	c.LipWidth = math.Max(f.Width*0.94, 0.4)
	c.FootWidth = c.LipWidth * (1 + math.Min(0.25, 0.06*drop))
	lead := math.Max(1.5*cell, 0.5)
	c.Rows = min(max(int(math.Ceil(drop/0.35))+2, 5), 28)
	// Columns every ~0.35 m: the variation along the lip lives in the
	// vertex colours and the per-column throw, so it needs the columns.
	// Odd, so one column runs down the middle.
	c.Cols = min(max(int(math.Ceil(c.FootWidth/0.35))+1, 3), 48) | 1
	tx, tz := f.DirX, f.DirZ
	if l := math.Hypot(tx, tz); l > 1e-9 {
		tx, tz = tx/l, tz/l
	} else {
		tx, tz = 1, 0
	}
	lx, lz := -tz, tx // left of the heading

	// VARIATION along the lip (fixes after W5: the sheet read as one
	// evenly tiled curtain side to side). Low-frequency noise across the
	// lip, seeded by where the fall is, so no two falls match and nothing
	// repeats across one: an uneven lip (a few centimetres up and down),
	// some of the water thrown further than the rest, thick white ropes and
	// thin glassy stretches (the aeration), and the odd gap where the sheet
	// parts and the rock shows (the edge share, which also frays the sides
	// more the further the water has fallen). The shader adds its own
	// per-column streaks on top (water.glsl, waterFallsShade).
	seed := noise.Hash64(math.Float64bits(math.Round(f.X*100)), math.Float64bits(math.Round(f.Z*100)), 0xfa11)
	nAt := func(o uint64, x, y float64) float64 { return 0.5 + 0.5*noise.Value2(seed+o, x, y) }
	c.ColVX = make([]float64, c.Cols)
	c.ColLip = make([]float64, c.Cols)
	c.VXMin = c.VX
	for k := 0; k < c.Cols; k++ {
		a := (float64(k)/float64(c.Cols-1)*2 - 1) * c.LipWidth / 2
		c.ColVX[k] = c.VX * (0.8 + 0.4*nAt(1, a/1.3, 0.5))
		c.ColLip[k] = -0.05 + 0.08*nAt(2, a/0.9, 2.5)
		c.VXMin = math.Min(c.VXMin, c.ColVX[k])
	}
	c.OutMax = c.Out
	for _, v := range c.ColVX {
		c.OutMax = math.Max(c.OutMax, v*tEnd)
	}

	arc, pa, py := -lead, -lead, f.Lip
	for r := 0; r < c.Rows; r++ {
		along, y, prog := -lead, f.Lip+0.01, 0.0
		if r > 0 {
			prog = float64(r-1) / float64(c.Rows-2)
			y = f.Lip - h*prog
			along = c.VX * math.Sqrt(2*(f.Lip-y)/fallsGravity)
			arc += math.Hypot(along-pa, y-py)
		}
		pa, py = along, y
		hw := c.HalfWidth(along)
		// Glassy over a small fall's lip, white by the foot; a tall fall
		// is white sooner.
		aer := 0.2
		if r > 0 {
			aer = 0.3 + 0.7*math.Sqrt(prog)
			aer = math.Max(aer, noise.Clamp(0.25+0.15*drop, 0, 0.85)*noise.SmoothStep(0, 0.3, prog))
		}
		for k := 0; k < c.Cols; k++ {
			a := (float64(k)/float64(c.Cols-1)*2 - 1) * hw
			// This column's throw and lip.
			ca := along
			if r > 0 {
				ca = c.ColVX[k] * math.Sqrt(2*(f.Lip-y)/fallsGravity)
			}
			cy := y + c.ColLip[k]*(1-0.5*prog)
			c.P = append(c.P, [3]float64{f.X + tx*ca + lx*a, cy, f.Z + tz*ca + lz*a})
			c.U = append(c.U, a)
			c.V = append(c.V, arc)
			// Thick and thin along the lip, drifting a little as it falls;
			// the foot's churn evens it out.
			thick := nAt(3, a/1.1, prog*0.8)
			ka := aer + (thick-0.5)*0.55*(1-prog*prog)
			c.Aeration = append(c.Aeration, noise.Clamp(ka, 0, 1))
			// Frayed sides, wider and more ragged lower down; the odd
			// gap opening below the lip.
			fw := math.Min(0.45, 0.3*hw) * (1 + 1.4*prog) * (0.6 + 0.8*nAt(4, a/0.7, prog*2))
			e := noise.SmoothStep(0, math.Min(fw, 0.9*hw), hw-math.Abs(a))
			// Gaps: rare and narrow, by the sides and toward the foot,
			// almost never in the middle of the top.
			gap := noise.SmoothStep(0.2, 0.34, nAt(5, a/0.6, prog*1.2))
			where := math.Max(noise.SmoothStep(0.45, 0.9, math.Abs(a)/math.Max(hw, 1e-3)), noise.SmoothStep(0.45, 0.85, prog))
			e *= 1 - (1-gap)*noise.SmoothStep(0.05, 0.35, prog)*where
			c.Edge = append(c.Edge, e)
			c.Progress = append(c.Progress, prog)
		}
	}
	return c
}

// notchFalls cuts the notch every fall drops through (file comment) into
// g, recording what it moved in changed.
func notchFalls(g *Grid, falls []Waterfall, water *WaterResult, floor float32, changed map[int32]bool) {
	s := g.S()
	for _, f := range falls {
		c := f.Curtain(g.Cell)
		tx, tz := f.DirX, f.DirZ
		if l := math.Hypot(tx, tz); l > 1e-9 {
			tx, tz = tx/l, tz/l
		} else {
			tx, tz = 1, 0
		}
		reach := math.Max(c.Out, c.OutMax) + g.Cell
		half := c.FootWidth/2 + g.Cell
		R := math.Hypot(reach, half)
		i0 := max(int(math.Floor((f.X-R-g.MinX)/g.Cell)), 0)
		i1 := min(int(math.Ceil((f.X+R-g.MinX)/g.Cell)), s-1)
		j0 := max(int(math.Floor((f.Z-R-g.MinZ)/g.Cell)), 0)
		j1 := min(int(math.Ceil((f.Z+R-g.MinZ)/g.Cell)), s-1)
		for j := j0; j <= j1; j++ {
			for i := i0; i <= i1; i++ {
				k := int32(j*s + i)
				if water != nil && water.Body[k] >= 0 {
					continue
				}
				rx, rz := g.X(i)-f.X, g.Z(j)-f.Z
				along := rx*tx + rz*tz
				across := math.Abs(tx*rz - tz*rx)
				if along <= 0.05 || along > reach || across > c.HalfWidth(math.Min(along, c.Out))+g.Cell {
					continue
				}
				lim := float32(c.SheetY(f.Lip, math.Min(along, math.Max(c.Out, c.OutMax))) - notchClear)
				lim = max(lim, floor)
				if g.H[k] > lim {
					g.H[k] = lim
					changed[k] = true
				}
			}
		}
	}
}

// cascade turns the rapids stretch lip..foot of z into a staircase (file
// comment) when the river's shape asks for one and the drop allows two
// steps; false leaves it rapids.
func (r *riverRun) cascade(z []float64, lip, foot int) bool {
	sh := r.spec.Shape
	drop := z[lip] - z[foot]
	if sh.CascadeStep <= 0 || foot-lip < 4 {
		return false
	}
	k := int(math.Ceil(drop / sh.CascadeStep))
	if minFall := math.Max(sh.MinFall, 0.05); float64(k)*minFall > drop {
		k = int(math.Floor(drop / minFall))
	}
	k = min(k, (foot-lip)/2)
	if k < 2 {
		return false
	}
	st := r.st
	zo := append([]float64(nil), z...)
	b := make([]int, k+1)
	for m := 0; m <= k; m++ {
		b[m] = lip + int(math.Round(float64(m)*float64(foot-lip)/float64(k)))
	}
	for m := 0; m < k; m++ {
		a, e := b[m], b[m+1]
		for i := a + 1; i <= e; i++ {
			z[i] = zo[e]
		}
		d := zo[a] - zo[e]
		s := st[a]
		r.falls = append(r.falls, Waterfall{River: r.name, X: round2(s.X), Z: round2(s.Z), Lip: round3(zo[a]), Foot: round3(zo[e]),
			Drop: round3(d), DirX: round3(s.TX), DirZ: round3(s.TZ), Width: round2(s.Width), S: round2(s.S), Cascade: true})
		// A small pool under each step, no longer than the step's run.
		extra := math.Min(0.35*d+0.3, 1.5)
		pool := math.Min(1.5*math.Max(s.Width, d), 0.8*(st[e].S-s.S))
		for i := a + 1; i <= e && st[i].S-s.S <= pool; i++ {
			fr := (st[i].S - s.S) / math.Max(pool, 1e-3)
			r.plunge[i] = math.Max(r.plunge[i], extra*math.Sin(math.Pi*math.Min(fr*0.5+0.5*math.Sqrt(fr), 1)))
		}
	}
	return true
}
