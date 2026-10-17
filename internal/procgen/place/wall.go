package place

// wall.go — cliff walls: cliff chunks stacked up the heightfield's walls.
//
// A heightfield can be steep but never vertical, and never undercut; its
// walls are a few samples stretched over tens of metres of height, which
// reads as smeared texture. A wall of cooked cliff chunks in front of it
// gives the faces, ledges and overhangs it cannot. Walls builds them along
// LINES — the feet of the cliff bands (TraceCliffFeet) or a spline the
// author drew — in COLUMNS a chunk's width apart, each column stacked in
// TIERS from the wall's foot to its rim.
//
// MEASURING A WALL (measureWall). At a point on the line, a transect is
// taken across it along the out direction: the steepest point on it is the
// wall; from there outward to where the slope eases below FootSlope is the
// foot (y0), inward to where it eases below RimSlope is the rim (y1). The
// transect's heights between are the wall's PROFILE: for each height y, how
// far in from the foot the heightfield's face is (faceAt).
//
// SEATING A CHUNK (seatChunk). A chunk's back is on z = 0 facing −Z, so it
// is turned to face OUT of the wall (its +Z along the out direction) and
// leaned back to follow the face (Lean of the face's own lean over the
// tier, up to MaxLean degrees). Its back must be buried in the face over its
// whole height (at least Inset behind it) and its front must stand at least
// Protrude of its depth in front of the face — so the stretched heightfield
// face is covered — over the tier's heights; the back's distance in is the
// middle of what those two allow (or as far in as the first needs, if a
// steeply leaning face allows neither). The face is read at three points
// across the chunk's width, so a chunk on a curving wall is seated against
// the face's worst point, not its centre's.
//
// THE BACK'S SILHOUETTE. A scanned cliff is rarely closed: it is a shell
// whose back is open, and its rim — the edge of the shell, which is what a
// viewer looking along the wall sees — need not lie on its box's back face.
// Seated by its box (SeatBox, phase 3's rule) such a piece can stand proud
// of the face with daylight between its rim and the rock behind. So when a
// variant carries a BackProfile (how far its back surface stands off the
// box's back plane, on a grid across its back), the chunk is pushed in until
// every cell on the OUTLINE of that profile is buried in the face too, read
// at the transect nearest the cell across the width. Interior cells are the
// shell's inside, hidden behind the rim, and are not asked to touch. A chunk
// that would have to go deeper than MaxBackGap of its depth past the box's
// seat to manage it is tried as another variant (up to three), and dropped
// if none fits.
//
// TIERS. A column's tiers are as many as fit the wall's height at the
// chunks' size with Overlap between them, at most Tiers; the scale is then
// set so they exactly span foot to rim (within Scale's range). Odd tiers are
// shifted half a column along the wall, so the joints do not line up like
// bricks in a stack. Every chunk gets its own variant, a little yaw and roll
// jitter and a scale jitter. A LEDGE (a ledge_slab variant) may sit on a
// tier's top where the next tier starts, jutting out of the face, and on the
// rim as an overhanging cap.
//
// Lines are walled in stretches (StretchLength), tallest first, so a capped
// set (MaxChunks) covers the big faces whole. Columns of different lines must not cross (two feet of a narrow slot
// facing each other): a chunk whose footprint runs into another line's is
// dropped.

import (
	"math"
	"sort"

	"foundry/internal/procgen/noise"
)

// WallSettings configure Walls.
type WallSettings struct {
	Seed uint64
	// Chunks are the cliff chunk variants (flat backs); Ledges the ledge
	// slab variants (none: no ledges).
	Chunks, Ledges []Variant
	// Scale is the chunks' scale range; ScaleJitter the per-chunk ±fraction.
	Scale       [2]float64
	ScaleJitter float64
	// Tiers is the most a column stacks; Overlap the fraction of a chunk's
	// height the next tier overlaps it by; Spacing the column step as a
	// fraction of a chunk's width.
	Tiers   int
	Overlap float64
	Spacing float64
	// MinHeight is the lowest wall worth a chunk, as a fraction of the
	// smallest chunk's height at the scale's low end; MinWall the lowest
	// in metres (the higher of the two holds).
	MinHeight, MinWall float64
	// The transect: FootSlope and RimSlope (degrees) end the wall; Search
	// is how far across the line it looks (metres).
	FootSlope, RimSlope, Search float64
	// WallSlope (degrees) is the steepest slope a transect must reach to be
	// a wall at all: a bank under it is talus country, and chunks stood
	// against it read as a facade in front of a hill.
	WallSlope float64
	// Seating: Inset (metres) the back is buried by, Protrude the fraction
	// of the depth the front stands proud of the face, Lean how much of the
	// face's lean a chunk follows, MaxLean (degrees) at most.
	Inset, Protrude, Lean, MaxLean float64
	// Sink is how far (a fraction of a chunk's height) the bottom tier sits
	// below the foot.
	Sink float64
	// SeatBox seats every chunk by its bounding box alone, ignoring the
	// variants' BackProfiles; MaxBackGap is the most (a fraction of a
	// chunk's depth) the silhouette seating may push past the box's seat
	// (see the file comment).
	SeatBox    bool
	MaxBackGap float64
	// BackCover is the fraction of the outline the silhouette seating
	// buries (the rest is the odd lip it would bury the piece to hide).
	BackCover float64
	// YawJitter and RollJitter (degrees) and AlongJitter (a fraction of a
	// column step) vary each chunk.
	YawJitter, RollJitter, AlongJitter float64
	// DropJitter (a fraction of a chunk's height) moves each chunk up or
	// down, so a tier's joints do not run level like courses of brick.
	DropJitter float64
	// LedgeChance is the chance of a ledge between two tiers, RimLedge of
	// one capping the rim; LedgeScale scales a ledge to the chunk's width.
	LedgeChance, RimLedge, LedgeScale float64
	// Collision is the chunks' and ledges' collision (auto: the variant's).
	Collision string
	// MaxChunks caps the whole wall set (0: no cap). The tallest stretches
	// (StretchLength metres of a line, walled whole) go first.
	MaxChunks     int
	StretchLength float64
	// MinContact is the share of a chunk's back (its covered back-profile
	// cells, or a grid over its box's back) that must touch the face — be
	// buried in it, or within ContactGap of it — for the seat to stand; a
	// piece with less hangs off the wall with daylight behind it (a rock
	// stuck onto the flank of a pillar) and is tried as another variant,
	// then dropped. 0 turns the test off.
	MinContact float64
	ContactGap float64
	// Backing is how much rock a column's wall must have behind its rim, in
	// chunk widths at the column's scale: inward from the rim the ground
	// must hold above the wall's upper two-thirds for that far, or the
	// "wall" is the flank of a pillar or a fin too thin to stack against.
	// 0 turns it off.
	Backing float64
	// HeightNorm evens the variants' sizes in a column (heightNorm): 0 scales
	// them all alike, 1 brings each to the mean height.
	HeightNorm float64
	// TierJitter varies each tier's overlap with the one below by up to this
	// fraction, so a column's joints do not run at one fixed pitch.
	TierJitter float64
	// Focus is a point the walls are seen from (the spawn, the canyon's
	// floor) and FocusRange the distance over which a wall's priority halves:
	// with a cap, the stretches nearest it are walled first. FocusRange 0
	// ignores it (tallest first, as before).
	Focus      [2]float64
	FocusRange float64
	// Keep, when set, says which columns this wall set may build: an
	// environment region's cliff_wall keeps the columns where its weight is
	// at least a half, so two regions' walls meet along the middle of their
	// band and never both clad one face (region.go). Nil keeps every
	// column.
	Keep func(x, z float64) bool
}

// DefaultWall is the stage's defaults.
func DefaultWall() WallSettings {
	return WallSettings{
		Scale: [2]float64{0.8, 2.5}, ScaleJitter: 0.18, Tiers: 10, Overlap: 0.25, Spacing: 0.62, MinHeight: 0.8, MinWall: 8,
		FootSlope: 40, RimSlope: 48, Search: 24, WallSlope: 60,
		Inset: 0.4, Protrude: 0.2, Lean: 0.8, MaxLean: 30, Sink: 0.06, MaxBackGap: 0.75, BackCover: 0.6,
		YawJitter: 14, RollJitter: 6, AlongJitter: 0.3, DropJitter: 0.12,
		LedgeChance: 0.18, RimLedge: 0.12, LedgeScale: 0.9, Collision: "auto", MaxChunks: 4000, StretchLength: 60,
	}
}

// DefaultWallPhase7 are the settings the phase-7 parameters add, at the
// values a stage gets when it sets them (their own defaults are off, so a
// stage from before them places what it did).
var DefaultWallPhase7 = WallSettings{MinContact: 0.65, ContactGap: 0.6, Backing: 0.8, TierJitter: 0.35, HeightNorm: 0.7, FocusRange: 0}

// WallResult is what Walls placed.
type WallResult struct {
	// Instances: Kind "chunk" or "ledge"; Rule is the line's index.
	Instances []Instance
	// Ground is the bottom tier's footprints (what scatter keeps off).
	Ground []Disc
	// Columns, Lines and the reasons columns were skipped.
	Columns, Lines           int
	TooLow, NoWall, Crossing int
	// Reseated counts chunks placed as another variant because the first
	// one's back would not seat; GapDropped the tiers none would.
	Reseated, GapDropped int
	// ContactDropped counts tiers no variant touched the face enough for
	// (MinContact); Pillars the columns whose wall had too little rock
	// behind it (Backing).
	ContactDropped, Pillars int
	Capped                  bool
	// Profiles are the walls measured, for the preview: the foot point and
	// the wall's foot and rim heights.
	Feet [][3]float64
}

// wall is one measured transect.
type wall struct {
	foot   [2]float64 // where the foot is, XZ
	out    [2]float64 // unit, out of the wall
	y0, y1 float64
	// prof is the face's distance in from the foot (metres) at heights
	// y0 + k·step.
	prof []float64
	step float64
}

// faceAt is how far in from the foot the face is at height y.
func (w *wall) faceAt(y float64) float64 {
	if len(w.prof) == 0 {
		return 0
	}
	f := (y - w.y0) / w.step
	if f <= 0 {
		return w.prof[0]
	}
	k := int(f)
	if k >= len(w.prof)-1 {
		return w.prof[len(w.prof)-1]
	}
	t := f - float64(k)
	return w.prof[k] + (w.prof[k+1]-w.prof[k])*t
}

// measureWall takes the transect through p along out (see the file
// comment). False when there is no wall there.
func measureWall(g *Ground, s *WallSettings, p, out [2]float64) (wall, bool) {
	const dt = 0.25
	n := int(s.Search / dt)
	at := func(t float64) [2]float64 { return [2]float64{p[0] + out[0]*t, p[1] + out[1]*t} }
	h := make([]float64, 2*n+1)
	for k := range h {
		q := at(float64(k-n) * dt)
		h[k] = g.Height(q[0], q[1])
	}
	// Slope along the transect, going IN (−out): rising heights.
	slope := func(k int) float64 {
		a, b := max(k-1, 0), min(k+1, len(h)-1)
		return math.Atan2(h[a]-h[b], float64(b-a)*dt) * 180 / math.Pi
	}
	steep, sk := -1.0, -1
	for k := 1; k < len(h)-1; k++ {
		if sl := slope(k); sl > steep {
			steep, sk = sl, k
		}
	}
	if sk < 0 || steep < math.Max(s.WallSlope, s.FootSlope+5) {
		return wall{}, false
	}
	// Outward (increasing k) to the foot, inward to the rim.
	fk := sk
	for fk < len(h)-1 && slope(fk) >= s.FootSlope {
		fk++
	}
	rk := sk
	for rk > 0 && slope(rk) >= s.RimSlope {
		rk--
	}
	w := wall{out: out, y0: h[fk], y1: h[rk], foot: at(float64(fk-n) * dt)}
	if w.y1-w.y0 <= 0 {
		return wall{}, false
	}
	// The profile: going in from the foot, the first distance each height
	// is reached at (heights only rise toward the rim by construction of
	// the search; a dip is held at the last height reached).
	w.step = math.Max((w.y1-w.y0)/64, 0.05)
	m := int(math.Ceil((w.y1-w.y0)/w.step)) + 1
	w.prof = make([]float64, m)
	k := fk
	for i := 0; i < m; i++ {
		y := w.y0 + float64(i)*w.step
		for k > rk && h[k-1] < y {
			k--
		}
		// Between sample k (below y) and k-1 (at or above it).
		t := float64(fk-k) * dt
		if k > rk && h[k-1] > h[k] {
			t += (y - h[k]) / (h[k-1] - h[k]) * dt
		}
		w.prof[i] = t
	}
	return w, true
}

// Walls lines every line with chunks (see the file comment).
func Walls(g *Ground, lines []WallLine, s WallSettings, pool interface{ Err() error }) (*WallResult, error) {
	res := &WallResult{Lines: len(lines)}
	if len(s.Chunks) == 0 {
		return res, nil
	}
	if s.Tiers < 1 {
		s.Tiers = 1
	}
	meanH, minH, meanW := 0.0, math.Inf(1), 0.0
	for _, v := range s.Chunks {
		meanH += v.Height
		minH = math.Min(minH, v.Height)
	}
	meanH /= float64(len(s.Chunks))
	// The mean width as the chunks are placed: each evened toward the mean
	// height first (heightNorm), so a 40 m scan brought down to the others'
	// height does not space every column by its width.
	for _, v := range s.Chunks {
		_, _, _, _, wd := backFrame(&v)
		meanW += wd * heightNorm(&s, &v, meanH)
	}
	meanW /= float64(len(s.Chunks))
	// Footprints, and the line each is on, to keep lines from crossing.
	grid := newDiscGrid(4, 1)
	var lineOf []int
	crosses := func(d Disc, line int) bool {
		bad := false
		grid.near(d.X, d.Z, d.R+meanW*2, func(id int, o Disc) {
			if !bad && lineOf[id] != line && math.Hypot(d.X-o.X, d.Z-o.Z) < 0.7*(d.R+o.R) {
				bad = true
			}
		})
		return bad
	}
	// Survey every column of every line first — its wall's height at its
	// spot — then wall the TALLEST STRETCHES FIRST: a capped set (MaxChunks) covers
	// the canyon's big stretched faces, not whichever line is longest or
	// was traced first. Each column draws from its own stream (seed, line,
	// column), so the order changes nothing else.
	nominal := (s.Scale[0] + s.Scale[1]) / 2
	step := math.Max(meanW*nominal*s.Spacing, 0.5)
	type column struct {
		line, k int
		a, h    float64
		p       [2]float64
	}
	var cols []column
	for li := range lines {
		if err := pool.Err(); err != nil {
			return nil, err
		}
		l := &lines[li]
		total := l.Length()
		for k, a := 0, step/2; a < total; k, a = k+1, a+step {
			p, out := l.at(a)
			if s.Keep != nil && !s.Keep(p[0], p[1]) {
				continue
			}
			h := 0.0
			if w, ok := measureWall(g, &s, p, out); ok {
				h = w.y1 - w.y0
			}
			cols = append(cols, column{li, k, a, h, p})
		}
	}
	// By STRETCH, not column by column: a column taken alone among bare
	// neighbours is a stack of crates on a heightfield face. Each line is
	// cut into stretches of StretchLength; a stretch scores its columns'
	// mean height, and the stretches are walled highest first, each whole
	// and in order along its line.
	type key struct{ line, w int }
	sum, cnt := map[key]float64{}, map[key]int{}
	stretch := math.Max(s.StretchLength, step)
	win := func(c column) key { return key{c.line, int(c.a / stretch)} }
	// Seen from Focus, a wall's worth falls off with distance: the area it
	// shows is its height over the distance, roughly.
	seen := func(c column) float64 {
		if s.FocusRange <= 0 {
			return 1
		}
		return 1 / (1 + math.Hypot(c.p[0]-s.Focus[0], c.p[1]-s.Focus[1])/s.FocusRange)
	}
	for _, c := range cols {
		sum[win(c)] += c.h * seen(c)
		cnt[win(c)]++
	}
	score := func(c column) float64 { k := win(c); return sum[k] / float64(cnt[k]) }
	sort.SliceStable(cols, func(i, j int) bool {
		si, sj := score(cols[i]), score(cols[j])
		if si != sj {
			return si > sj
		}
		if cols[i].line != cols[j].line {
			return cols[i].line < cols[j].line
		}
		return cols[i].k < cols[j].k
	})
	for ci, col := range cols {
		if ci&255 == 0 {
			if err := pool.Err(); err != nil {
				return nil, err
			}
		}
		li, a := col.line, col.a
		l := &lines[li]
		rng := noise.NewRNG(noise.Hash64(s.Seed, 0x77616c6c, uint64(li), uint64(col.k)))
		{
			res.Columns++
			// acc is how far up from the wall's foot this tier's bottom is
			// (before the sink and the drop jitter): each tier starts at the
			// last one's top less a jittered overlap, so the pitch varies
			// with the variant placed and the jitter, not one fixed step.
			acc := 0.0
			for tier := 0; tier < s.Tiers; tier++ {
				off := 0.0
				if tier%2 == 1 {
					off = step / 2
				}
				pos := a + off + rng.Range(-1, 1)*s.AlongJitter*step
				p, out := l.at(pos)
				w, ok := measureWall(g, &s, p, out)
				if !ok {
					if tier == 0 {
						res.NoWall++
					}
					break
				}
				height := w.y1 - w.y0
				if tier == 0 {
					res.Feet = append(res.Feet, [3]float64{w.foot[0], w.foot[1], height})
				}
				if height < math.Max(s.MinHeight*minH*s.Scale[0], s.MinWall) {
					if tier == 0 {
						res.TooLow++
					}
					break
				}
				// The tiers this wall takes at the nominal size, and the
				// scale that makes them span it.
				pitch := 1 - s.Overlap
				nt := int(math.Ceil((height/(meanH*nominal) - s.Overlap) / pitch))
				nt = min(max(nt, 1), s.Tiers)
				sc := height / (meanH * (float64(nt)*pitch + s.Overlap))
				sc = math.Max(s.Scale[0], math.Min(s.Scale[1], sc))
				if tier >= nt {
					break
				}
				// A wall with too little rock behind its rim is a pillar's
				// flank or a fin: chunks stacked on it hang in the air.
				if tier == 0 && s.Backing > 0 && !backed(g, &w, s.Backing*meanW*sc) {
					res.Pillars++
					break
				}
				vi := rng.Intn(len(s.Chunks))
				v := &s.Chunks[vi]
				base := sc * (1 + rng.Range(-1, 1)*s.ScaleJitter)
				csc := base * heightNorm(&s, v, meanH)
				jit := 0.0
				if tier > 0 {
					jit = rng.Range(-1, 1)
				}
				bottom := func(v *Variant) float64 {
					y := w.y0 + acc - s.Sink*v.Height*csc
					if tier > 0 {
						y += jit * s.DropJitter * v.Height * csc
					}
					return y
				}
				yb := bottom(v)
				if tier > 0 && yb+0.5*v.Height*csc > w.y1 {
					break // the rest of the wall is shorter than half a chunk
				}
				in, tb, tanL, depth, seated, contact := seatChunk(g, &s, &w, l, pos, v, csc, yb, rng)
				touched := seated && contact >= s.MinContact
				// A back that will not seat, or one that hangs: the next
				// variants, in order.
				for try := 1; !touched && try < min(4, len(s.Chunks)); try++ {
					vi = (vi + 1) % len(s.Chunks)
					v = &s.Chunks[vi]
					csc = base * heightNorm(&s, v, meanH)
					yb = bottom(v)
					in, tb, tanL, depth, seated, contact = seatChunk(g, &s, &w, l, pos, v, csc, yb, rng)
					if touched = seated && contact >= s.MinContact; touched {
						res.Reseated++
					}
				}
				// A tier no variant fits leaves a gap in the column, not the
				// end of it: the wall above may well take one (a ledge or a
				// bulge under this tier only).
				if !seated || !touched {
					if !seated {
						res.GapDropped++
					} else {
						res.ContactDropped++
					}
					acc += meanH * sc * (1 - s.Overlap)
					continue
				}
				over := s.Overlap
				if s.TierJitter > 0 {
					over *= 1 + rng.Range(-1, 1)*s.TierJitter
				}
				acc += v.Height * csc * (1 - over)
				_, _, _, _, wd := backFrame(v)
				half := wd * csc / 2
				in.Variant, in.Rule, in.Kind = vi, li, "chunk"
				// The chunk's base: from its back plane out by its depth.
				bx, bz := w.foot[0]-w.out[0]*tb, w.foot[1]-w.out[1]*tb
				d := Disc{bx + w.out[0]*depth/2, bz + w.out[1]*depth/2, 0.5 * math.Max(2*half, depth)}
				if crosses(d, li) {
					res.Crossing++
					break
				}
				if s.MaxChunks > 0 && len(res.Instances) >= s.MaxChunks {
					res.Capped = true
					return res, nil
				}
				res.Instances = append(res.Instances, in)
				grid.add(d, 1)
				lineOf = append(lineOf, li)
				if tier == 0 {
					// The bottom tier's footprint, as discs along its width.
					right := [2]float64{w.out[1], -w.out[0]}
					hw := half
					r := math.Min(depth, hw) / 2
					for k := -1; k <= 1; k++ {
						res.Ground = append(res.Ground, Disc{d.X + right[0]*hw*0.6*float64(k), d.Z + right[1]*hw*0.6*float64(k), r})
					}
				}
				// A ledge on this tier's top, where the next begins; or on
				// the rim above the last.
				last := tier == nt-1 || w.y0+acc+0.5*meanH*sc > w.y1
				chance := s.LedgeChance
				if last {
					chance = s.RimLedge
				}
				if len(s.Ledges) > 0 && rng.Chance(chance) {
					top := yb + v.Top*csc
					if last {
						top = math.Max(top, w.y1)
					}
					// Where the wall's front is at that height, in from the
					// foot: on the chunk, its front (its back leaned in by
					// the lean, less its depth) or the heightfield's face,
					// whichever is further out; above it (a rim the tiers
					// fall short of), the face.
					front := w.faceAt(top)
					if chunkTop := yb + v.Height*csc; top <= chunkTop {
						front = math.Min(tb+(top-yb)*tanL-depth, front)
					}
					res.Instances = append(res.Instances, ledge(&s, &w, front, 2*half, top, li, rng))
				}
			}
		}
	}
	return res, nil
}

// at is the point a metres along the line and its out direction.
func (l *WallLine) at(a float64) ([2]float64, [2]float64) {
	if len(l.Points) == 1 || a <= 0 {
		return l.Points[0], l.Out[0]
	}
	for k := 1; k < len(l.Points); k++ {
		p, q := l.Points[k-1], l.Points[k]
		d := math.Hypot(q[0]-p[0], q[1]-p[1])
		if a <= d && d > 0 {
			t := a / d
			o := [2]float64{l.Out[k-1][0] + (l.Out[k][0]-l.Out[k-1][0])*t, l.Out[k-1][1] + (l.Out[k][1]-l.Out[k-1][1])*t}
			return [2]float64{p[0] + (q[0]-p[0])*t, p[1] + (q[1]-p[1])*t}, unit2(o)
		}
		a -= d
	}
	n := len(l.Points) - 1
	return l.Points[n], l.Out[n]
}

// seatChunk places chunk v at scale s with its bottom at yb against wall w
// (see the file comment). It answers the instance, how far in from the foot
// its back's bottom is, its depth, and false when its back's silhouette
// could not be seated within MaxBackGap.
func seatChunk(g *Ground, s *WallSettings, w *wall, l *WallLine, pos float64, v *Variant, sc, yb float64,
	rng *noise.RNG) (in Instance, tb, tanL, depth float64, ok bool, contact float64) {
	hc := v.Height * sc
	// The chunk in its WALL frame: turned so its front (away from its
	// back) is +Z; there its box gives the back's approximate plane (the
	// box's −Z face), its depth and its lateral centre.
	head, bz, depth0, cx, width := backFrame(v)
	depth = depth0 * sc
	half := width * sc / 2
	right := [2]float64{w.out[1], -w.out[0]}
	// The face at three points across the width, each its own transect;
	// lat is each one's place across the width (−1 left edge, 1 right).
	walls := []*wall{w}
	lat := []float64{0}
	for _, k := range []float64{-0.8, 0.8} {
		q := [2]float64{w.foot[0] + right[0]*half*k, w.foot[1] + right[1]*half*k}
		if o, ok := measureWall(g, s, q, w.out); ok {
			// Measured from its own foot: carry it to this wall's.
			o.prof = append([]float64(nil), o.prof...)
			shift := (o.foot[0]-w.foot[0])*(-w.out[0]) + (o.foot[1]-w.foot[1])*(-w.out[1])
			for i := range o.prof {
				o.prof[i] += shift
			}
			oo := o
			walls = append(walls, &oo)
			lat = append(lat, k)
		}
	}
	face := func(y float64) (lo, hi float64) {
		lo, hi = math.Inf(1), math.Inf(-1)
		for _, ww := range walls {
			f := ww.faceAt(y)
			lo, hi = math.Min(lo, f), math.Max(hi, f)
		}
		return
	}
	// The lean that follows the face over the tier.
	f0, _ := face(yb)
	f1, _ := face(yb + hc)
	tanL = s.Lean * (f1 - f0) / hc
	tanL = math.Max(0, math.Min(math.Tan(s.MaxLean*math.Pi/180), tanL))
	// Bounds on the back's distance in at the tier's foot, tb: buried by
	// Inset everywhere (the face's farthest-in point at each height), and
	// the front Protrude·depth proud of the face's nearest-out point.
	need, allow := math.Inf(-1), math.Inf(1)
	for k := 0; k <= 8; k++ {
		y := yb + hc*float64(k)/8
		lo, hi := face(y)
		lean := (y - yb) * tanL
		need = math.Max(need, hi+s.Inset-lean)
		allow = math.Min(allow, lo+depth*(1-s.Protrude)-lean)
	}
	// The silhouette: every outline cell of the back profile buried too, at
	// the transect nearest it across the width.
	ok = true
	if bp := v.BackProfile; bp != nil && !s.SeatBox && len(bp.Outline) > 0 {
		boxNeed := need
		inset := s.Inset * 0.5
		// Each outline cell's own need; the seat is the BackCover quantile
		// of them, so the odd lip curling far forward does not bury the
		// whole piece to hide itself.
		needs := make([]float64, 0, len(bp.Outline))
		for _, c := range bp.Outline {
			y := yb + c.Y*sc
			k := 0
			for q := range walls {
				if math.Abs(lat[q]-c.Lateral) < math.Abs(lat[k]-c.Lateral) {
					k = q
				}
			}
			f := walls[k].faceAt(y)
			needs = append(needs, f+inset+c.Gap*sc-(y-yb)*tanL)
		}
		sort.Float64s(needs)
		q := min(int(math.Ceil(s.BackCover*float64(len(needs))))-1, len(needs)-1)
		need = math.Max(need, needs[max(q, 0)])
		tb = need
		if allow > need {
			tb = (need + allow) / 2
		}
		// Pushed that far, does enough of its front still stand out of the
		// face? A piece buried past MaxBackGap of its depth beyond where the
		// box would have put it reads as a lump in the wall, not a chunk.
		ok = need-boxNeed <= s.MaxBackGap*depth
	}
	tb = need
	if allow > need {
		tb = (need + allow) / 2
	}
	yaw := math.Atan2(w.out[0], w.out[1]) + rng.Range(-1, 1)*s.YawJitter*math.Pi/180
	roll := rng.Range(-1, 1) * s.RollJitter * math.Pi / 180
	wr := RotY(yaw).Mul(RotX(-math.Atan(tanL))).Mul(RotZ(roll))
	rot := wr.Mul(RotY(-head))
	// The back plane's bottom centre goes to tb in from the foot; the mesh's
	// origin is wherever that puts it.
	off := wr.Apply([3]float64{cx * sc, 0, bz * sc})
	px := w.foot[0] - w.out[0]*tb - off[0]
	pz := w.foot[1] - w.out[1]*tb - off[2]
	in = Instance{Pos: [3]float64{px, yb - off[1], pz}, Rot: rot, Scale: sc, Radius: math.Max(half, depth/2)}
	in.Collision = wallCollision(s.Collision, v)
	contact = 1
	if s.MinContact > 0 {
		contact = backContact(s, v, sc, yb, tb, tanL, walls, lat)
	}
	return in, tb, tanL, depth, ok, contact
}

// heightNorm is variant v's share of the column's scale: (meanH / its
// height)^HeightNorm, so with HeightNorm 1 every variant stands the tier's
// height whatever its own, and with 0 (the old rule) all share one scale
// and a 40 m scan placed beside a 5 m one comes out eight times as big.
func heightNorm(s *WallSettings, v *Variant, meanH float64) float64 {
	if s.HeightNorm <= 0 || v.Height <= 0 {
		return 1
	}
	return math.Pow(meanH/v.Height, s.HeightNorm)
}

// backContact is the share of the seated chunk's back that touches the face
// (see WallSettings.MinContact): each covered cell of its back profile —
// or, without one, a 5 × 6 grid over its box's back — is where the rock's
// back surface is, tb in from the foot leaned by tanL and stood forward by
// the cell's gap; it touches when the face at that height, read at the
// transect nearest it across the width, is no more than ContactGap behind
// it. A side where the face could not be measured at all (the wall ends: a
// pillar narrower than the chunk) touches nowhere.
func backContact(s *WallSettings, v *Variant, sc, yb, tb, tanL float64, walls []*wall, lat []float64) float64 {
	tol := math.Max(s.ContactGap, 0.05)
	face := func(lateral, y float64) float64 {
		k := 0
		for q := range walls {
			if math.Abs(lat[q]-lateral) < math.Abs(lat[k]-lateral) {
				k = q
			}
		}
		// The nearest transect is the centre's for a cell well out to a
		// side: that side's own could not be measured.
		if math.Abs(lat[k]-lateral) > 0.5 {
			return math.Inf(1)
		}
		return walls[k].faceAt(y)
	}
	touched, total := 0, 0
	try := func(lateral, y, gap float64) {
		total++
		back := tb + (y-yb)*tanL - gap
		if face(lateral, y)-back <= tol {
			touched++
		}
	}
	if bp := v.BackProfile; bp != nil && bp.NX > 0 && bp.NY > 0 {
		for row := 0; row < bp.NY; row++ {
			y := yb + (float64(row)+0.5)/float64(bp.NY)*v.Height*sc
			for i := 0; i < bp.NX; i++ {
				g := bp.Gap[row*bp.NX+i]
				if g < 0 {
					continue
				}
				try(-1+(2*float64(i)+1)/float64(bp.NX), y, g*sc)
			}
		}
	} else {
		for row := 0; row < 6; row++ {
			y := yb + (float64(row)+0.5)/6*v.Height*sc
			for i := 0; i < 5; i++ {
				try(-0.8+0.4*float64(i), y, 0)
			}
		}
	}
	if total == 0 {
		return 1
	}
	return float64(touched) / float64(total)
}

// backed reports whether wall w has at least dist metres of rock behind its
// rim: going in from the rim, the ground holds above the wall's lower third
// that far. A pillar or a fin falls away again on its far side.
func backed(g *Ground, w *wall, dist float64) bool {
	if dist <= 0 || len(w.prof) == 0 {
		return true
	}
	rim := w.prof[len(w.prof)-1]
	floor := w.y1 - 0.65*(w.y1-w.y0)
	for t := 1.0; t <= dist; t += math.Max(dist/24, 0.5) {
		d := rim + t
		x, z := w.foot[0]-w.out[0]*d, w.foot[1]-w.out[1]*d
		if g.Height(x, z) < floor {
			return false
		}
	}
	return true
}

// backFrame is a chunk's wall frame (see seatChunk): the heading of its
// front in its own frame (radians about +Y; 0 for a back facing −Z), and in
// the frame turned by it the back plane's z, the depth, the lateral centre
// and the width of its bounding box.
func backFrame(v *Variant) (head, backZ, depth, cx, width float64) {
	lo, hi := v.Min, v.Max
	if lo == hi {
		lo = [3]float64{-v.Extent[0] / 2, 0, 0}
		hi = [3]float64{v.Extent[0] / 2, v.Extent[1], v.Extent[2]}
	}
	d := v.BackDir
	if d[0] == 0 && d[2] == 0 {
		d = [3]float64{0, 0, -1}
	}
	head = math.Atan2(-d[0], -d[2])
	r := RotY(-head)
	minX, maxX, minZ, maxZ := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
	for c := 0; c < 8; c++ {
		q := lo
		if c&1 != 0 {
			q[0] = hi[0]
		}
		if c&2 != 0 {
			q[1] = hi[1]
		}
		if c&4 != 0 {
			q[2] = hi[2]
		}
		a := r.Apply(q)
		minX, maxX = math.Min(minX, a[0]), math.Max(maxX, a[0])
		minZ, maxZ = math.Min(minZ, a[2]), math.Max(maxZ, a[2])
	}
	return head, minZ, maxZ - minZ, (minX + maxX) / 2, maxX - minX
}

// ledge is a ledge slab at height top jutting out of the wall, whose front
// is front metres in from the foot there; its length (X) runs along the
// wall, about width.
func ledge(s *WallSettings, w *wall, front, width, top float64, line int, rng *noise.RNG) Instance {
	vi := rng.Intn(len(s.Ledges))
	v := &s.Ledges[vi]
	sc := s.LedgeScale * width / math.Max(v.Extent[0], 1e-3) * rng.Range(0.75, 1.1)
	// Its centre a little out from the front: about two-thirds of it
	// jutting, the rest bedded in the wall.
	ld := v.Extent[2] * sc
	in := front - ld*0.3
	cx := w.foot[0] - w.out[0]*in
	cz := w.foot[1] - w.out[1]*in
	yaw := math.Atan2(w.out[0], w.out[1]) + rng.Range(-1, 1)*10*math.Pi/180
	tilt := rng.Range(-1, 1) * 4 * math.Pi / 180
	rot := RotY(yaw).Mul(RotX(tilt))
	out := Instance{Variant: vi, Rule: line, Kind: "ledge", Pos: [3]float64{cx, top - v.Height*sc*0.35, cz}, Rot: rot, Scale: sc,
		Radius: v.FootprintRadius * sc}
	out.Collision = wallCollision(s.Collision, v)
	return out
}

// wallCollision is a wall piece's collision: the choice, or (auto) what
// its variant's stage asks for.
func wallCollision(choice string, v *Variant) string {
	if choice != "auto" && choice != "" {
		return choice
	}
	if v.PlaceCollision != "" {
		return v.PlaceCollision
	}
	return "mesh"
}

// SplineLines turns an authored polyline into wall lines: resampled every
// metre, its out direction the line's normal on the given side (left,
// right, or auto: whichever side is downhill, decided per point and then
// for the line by majority).
func SplineLines(g *Ground, pts [][2]float64, side string) []WallLine {
	if len(pts) < 2 {
		return nil
	}
	l := WallLine{Points: pts, Out: make([][2]float64, len(pts))}
	for k := range pts {
		a, b := pts[max(k-1, 0)], pts[min(k+1, len(pts)-1)]
		t := unit2([2]float64{b[0] - a[0], b[1] - a[1]})
		l.Out[k] = [2]float64{-t[1], t[0]} // left of the direction of travel
	}
	flip := false
	switch side {
	case "right":
		flip = true
	case "auto":
		down := 0
		for k, p := range pts {
			o := l.Out[k]
			if g.Height(p[0]+o[0]*4, p[1]+o[1]*4) < g.Height(p[0]-o[0]*4, p[1]-o[1]*4) {
				down++
			}
		}
		flip = down*2 < len(pts)
	}
	if flip {
		for k := range l.Out {
			l.Out[k] = [2]float64{-l.Out[k][0], -l.Out[k][1]}
		}
	}
	return []WallLine{resampleLine(l, 1, 2)}
}
