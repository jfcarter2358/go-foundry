// Package cave is the cave system behind the `cave` stage
// (docs/adventure/PHASE_C.md): a void carved through sandstone along an
// authored 3D path, as a signed-distance function, and the rock around it.
//
// It is pure computation (sdf, noise and the terrain's grid; no files, no
// meshes): the stage meshes it in sections, cooks it, and cuts the terrain's
// tiles where the void comes through the ground.
//
// # The void
//
// The path's points sit on the cave's FLOOR and carry its width and its
// height (floor to roof). The path is densified (Catmull-Rom, a metre apart),
// made to wander sideways and breathe by noise, and binned on a plan grid so a
// point finds the few segments near it. At a point p the nearest segment of
// the centreline (a half height above the floor) gives
//
//   - a = the lateral offset, along the horizontal side of the path;
//   - b = the height above the floor there;
//   - the width W and height H there, and how far p lies past an end.
//
// The cross-section is a sandstone cave's: vertical walls up to (1 − Arch)·H,
// an elliptic vault over them, and a flat floor at b = 0 — the floor is sand,
// settled flat, rippled a little. Then, in order:
//
//  1. ROUGHNESS: two octaves of 3D noise push the walls and the roof in and
//     out (never the floor), so no two metres are the same width;
//  2. LEDGES: sdf.Strata beds, soft ones weathered back (the void grows into
//     them), hard ones standing as ledges and flat roof steps — the bedding a
//     sandstone cave is made of;
//  3. DRAPERY: thin vertical fins and curtains hanging from the roof, their
//     plan a ridged noise's crest lines (sheets), each hanging further at its
//     middle than its edges and the whole drapery patchy along the cave;
//  4. BREAKDOWN: angular blocks fallen from the roof, on two lattices (big
//     slabs and the rubble between), tilted and half buried in the sand, with
//     a lane down the middle kept clearer (Clear) so the cave can be walked.
//
// At(p) is negative inside the void. It is a bound on the distance, not the
// distance itself (Lip says how far from one it is), which is what the mesher
// needs.
package cave

import (
	"math"

	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/sdf"
)

// Point is one control point of the path.
type Point struct {
	// X, Y, Z is the floor under the path, metres.
	X, Y, Z float64
	// Width is wall to wall and Height floor to roof, metres.
	Width, Height float64
}

// Settings shape a cave.
type Settings struct {
	Path []Point
	// Arch is the share of the height that is vaulted roof (0: a box, 1: an
	// ellipse down to the floor).
	Arch float64
	// Wander is how far the cave wanders off its drawn path, metres.
	Wander float64
	// Rough is the walls' and roof's noise displacement, metres.
	Rough float64
	// Ledges is how far a soft bed weathers back, metres; BedSpacing the
	// beds' typical thickness.
	Ledges, BedSpacing float64
	// Drapery is the share of the roof hung with fins, 0..1;
	// DraperyLength how far the longest hang (m, capped at half the
	// height); DraperySpacing the fins' typical spacing.
	Drapery, DraperyLength, DraperySpacing float64
	// Blocks is the floor's cover of fallen blocks, 0..1; BlockSize the big
	// blocks' typical size (m); Clear how much clearer the middle lane is.
	Blocks, BlockSize, Clear float64
	// Sand is the floor's ripple, metres.
	Sand float64
	Seed uint64
}

// Defaults is a sandstone cave.
func Defaults() Settings {
	return Settings{Arch: 0.6, Wander: 2, Rough: 0.6, Ledges: 0.7, BedSpacing: 1.6,
		Drapery: 0.5, DraperyLength: 3, DraperySpacing: 2.2, Blocks: 0.45, BlockSize: 2.2, Clear: 0.6, Sand: 0.08}
}

// dense is a point of the densified path.
type dense struct {
	x, y, z, w, h float64 // the floor point, width, height
	s             float64 // arc length (plan)
}

// block is a fallen block: a rotated box.
type block struct {
	c    sdf.Vec3
	half sdf.Vec3
	rot  sdf.Mat3 // world → block
}

// Void is a compiled cave.
type Void struct {
	S   Settings
	pts []dense
	// bins: a plan grid of segment lists.
	bin        float64
	bx0, bz0   float64
	bnx, bnz   int
	bins       [][]int32
	box        sdf.Box
	reach      float64 // how far past its width the void can reach
	rough      noise.Fractal
	rough2     noise.Fractal
	sand       noise.Fractal
	strata     *sdf.Strata
	fin        noise.Fractal
	finPatch   noise.Fractal
	finLen     noise.Fractal
	blockCells [2]float64
	blocks     [2]map[[2]int32]block
	// Lip is At's Lipschitz bound.
	Lip float64
	// Ground, when set, is the terrain's height at (x, z) — the lowest
	// within a couple of metres (GroundBelow), so a cliff face does not
	// read as ground over the floor: where the ground lies just over the
	// cave's floor — out of a mouth, on a ledge the path runs onto — the
	// floor rises to just under it (GroundSnap), so the ground itself is the
	// floor there and the void never digs pits in it. Nil: the path's floor
	// everywhere.
	Ground func(x, z float64) float64
}

// GroundSnap is how far over the cave's floor the ground may lie and still
// lift the floor to just under it (m); groundUnder how far under it.
const (
	GroundSnap  = 2.5
	groundUnder = 0.15
)

// New compiles s. A path of fewer than two points is an empty cave.
func New(s Settings) *Void {
	v := &Void{S: s}
	if len(s.Path) < 2 {
		v.box = sdf.Box{Lo: sdf.Vec3{1, 1, 1}, Hi: sdf.Vec3{0, 0, 0}}
		return v
	}
	h := func(k uint64) uint64 { return noise.Hash64(s.Seed, 0x4341_5645, k) }
	v.pts = densify(s.Path, 1)
	wander(v.pts, s.Wander, h(1))
	maxW, maxH := 0.0, 0.0
	for _, p := range v.pts {
		maxW, maxH = math.Max(maxW, p.w), math.Max(maxH, p.h)
	}
	v.rough = noise.Fractal{Seed: h(2), Octaves: 3, Frequency: 1 / 3.5, Gain: 0.5}
	v.rough2 = noise.Fractal{Seed: h(3), Octaves: 2, Frequency: 1 / 0.9, Gain: 0.5}
	v.sand = noise.Fractal{Seed: h(4), Octaves: 2, Frequency: 1 / 3.0, Gain: 0.5}
	if s.Ledges > 0 && s.BedSpacing > 0 {
		st := &sdf.Strata{Base: s.Path[0].Y + 0.37*s.BedSpacing, Inset: s.Ledges, Undercut: 0.7, BedJitter: 0.5,
			MaxSlope: 2.5, Seed: h(5)}
		rng := noise.NewRNG(h(6))
		for i := 0; i < 9; i++ {
			hard := rng.Float64() < 0.55
			hd := 0.15 + 0.25*rng.Float64()
			if hard {
				hd = 0.75 + 0.25*rng.Float64()
			}
			st.Layers = append(st.Layers, sdf.Layer{Thickness: s.BedSpacing * (0.5 + rng.Float64()), Hardness: hd})
		}
		wf := sdf.FBM(h(7), 1/18.0, 2, 0.5)
		st.Warp, st.WarpAmp = &wf, 0.6
		vf := sdf.FBM(h(8), 1/9.0, 2, 0.5)
		st.Variation, st.VariationAmt = &vf, 0.45
		v.strata = st
	}
	sp := math.Max(s.DraperySpacing, 0.4)
	v.fin = noise.Fractal{Seed: h(9), Octaves: 2, Frequency: 1 / (2 * sp), Gain: 0.45, Shape: noise.ShapeRidged}
	v.finPatch = noise.Fractal{Seed: h(10), Octaves: 2, Frequency: 1 / 14.0, Gain: 0.5}
	v.finLen = noise.Fractal{Seed: h(11), Octaves: 2, Frequency: 1 / (1.3 * sp), Gain: 0.5}
	v.reach = s.Rough*1.6 + s.Ledges + 1
	// The bins.
	v.bin = 8
	lo := sdf.Vec3{math.Inf(1), math.Inf(1), math.Inf(1)}
	hi := sdf.Vec3{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	for _, p := range v.pts {
		r := p.w/2 + v.reach
		lo = sdf.Vec3{math.Min(lo[0], p.x-r), math.Min(lo[1], p.y-v.reach), math.Min(lo[2], p.z-r)}
		hi = sdf.Vec3{math.Max(hi[0], p.x+r), math.Max(hi[1], p.y+p.h+v.reach), math.Max(hi[2], p.z+r)}
	}
	v.box = sdf.Box{Lo: lo, Hi: hi}
	pad := maxW/2 + maxH + v.reach + 2
	v.bx0, v.bz0 = lo[0]-pad, lo[2]-pad
	v.bnx = int(math.Ceil((hi[0]-lo[0]+2*pad)/v.bin)) + 1
	v.bnz = int(math.Ceil((hi[2]-lo[2]+2*pad)/v.bin)) + 1
	v.bins = make([][]int32, v.bnx*v.bnz)
	for k := 0; k+1 < len(v.pts); k++ {
		a, b := v.pts[k], v.pts[k+1]
		r := math.Max(a.w, b.w)/2 + math.Max(a.h, b.h) + v.reach + 1
		i0, j0 := v.binOf(math.Min(a.x, b.x)-r, math.Min(a.z, b.z)-r)
		i1, j1 := v.binOf(math.Max(a.x, b.x)+r, math.Max(a.z, b.z)+r)
		for j := j0; j <= j1; j++ {
			for i := i0; i <= i1; i++ {
				v.bins[j*v.bnx+i] = append(v.bins[j*v.bnx+i], int32(k))
			}
		}
	}
	v.placeBlocks()
	// Lipschitz: the section's ellipse (about the aspect), the noise, the
	// beds' slope, the fins.
	v.Lip = 2 + s.Rough*(fractalLip(v.rough)+0.35*fractalLip(v.rough2)) + 2.5*boolF(v.strata != nil) + 2*s.Drapery
	return v
}

func boolF(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// fractalLip is about a fractal's gradient bound (sdf's estimate).
func fractalLip(f noise.Fractal) float64 { return sdf.FractalField(f).Lip }

func (v *Void) binOf(x, z float64) (int, int) {
	i := int(math.Floor((x - v.bx0) / v.bin))
	j := int(math.Floor((z - v.bz0) / v.bin))
	return min(max(i, 0), v.bnx-1), min(max(j, 0), v.bnz-1)
}

// Empty reports whether the cave has no path.
func (v *Void) Empty() bool { return len(v.pts) < 2 }

// Box bounds the void (its walls' farthest reach included).
func (v *Void) Box() sdf.Box { return v.box }

// densify samples the Catmull-Rom curve through ctrl every spacing metres.
func densify(ctrl []Point, spacing float64) []dense {
	at := func(k int) Point { return ctrl[min(max(k, 0), len(ctrl)-1)] }
	cr := func(p0, p1, p2, p3, t float64) float64 {
		t2, t3 := t*t, t*t*t
		return 0.5 * (2*p1 + (-p0+p2)*t + (2*p0-5*p1+4*p2-p3)*t2 + (-p0+3*p1-3*p2+p3)*t3)
	}
	var out []dense
	for k := 0; k+1 < len(ctrl); k++ {
		p0, p1, p2, p3 := at(k-1), at(k), at(k+1), at(k+2)
		l := math.Sqrt((p2.X-p1.X)*(p2.X-p1.X) + (p2.Y-p1.Y)*(p2.Y-p1.Y) + (p2.Z-p1.Z)*(p2.Z-p1.Z))
		n := max(int(math.Ceil(l/spacing)), 1)
		for q := 0; q < n; q++ {
			t := float64(q) / float64(n)
			// Smoothstep the size channels so a chamber swells and pinches
			// rather than tapering in straight cones.
			ts := t * t * (3 - 2*t)
			out = append(out, dense{
				x: cr(p0.X, p1.X, p2.X, p3.X, t), y: cr(p0.Y, p1.Y, p2.Y, p3.Y, t), z: cr(p0.Z, p1.Z, p2.Z, p3.Z, t),
				w: p1.Width + (p2.Width-p1.Width)*ts, h: p1.Height + (p2.Height-p1.Height)*ts,
			})
		}
	}
	last := ctrl[len(ctrl)-1]
	out = append(out, dense{x: last.X, y: last.Y, z: last.Z, w: last.Width, h: last.Height})
	arc(out)
	return out
}

func arc(pts []dense) {
	s := 0.0
	for i := range pts {
		if i > 0 {
			s += math.Hypot(pts[i].x-pts[i-1].x, pts[i].z-pts[i-1].z)
		}
		pts[i].s = s
	}
}

// wander pushes the path sideways by noise of arc length and lets its size
// breathe, tapering to nothing at the ends (the mouths stay where drawn).
func wander(pts []dense, amp float64, seed uint64) {
	if len(pts) < 3 {
		return
	}
	f := noise.Fractal{Seed: seed, Octaves: 2, Frequency: 1 / 40.0, Gain: 0.45}
	wf := noise.Fractal{Seed: noise.Hash64(seed, 1), Octaves: 2, Frequency: 1 / 15.0, Gain: 0.5}
	end := pts[len(pts)-1].s
	type off struct{ dx, dz float64 }
	offs := make([]off, len(pts))
	for i := range pts {
		a, b := pts[max(i-1, 0)], pts[min(i+1, len(pts)-1)]
		dx, dz := b.x-a.x, b.z-a.z
		l := math.Hypot(dx, dz)
		if l < 1e-9 {
			continue
		}
		taper := noise.SmoothStep(0, 12, pts[i].s) * noise.SmoothStep(0, 12, end-pts[i].s)
		m := amp * 1.5 * f.At2(pts[i].s, 0.31) * taper
		offs[i] = off{-dz / l * m, dx / l * m}
	}
	for i := range pts {
		pts[i].x += offs[i].dx
		pts[i].z += offs[i].dz
		taper := noise.SmoothStep(0, 6, pts[i].s) * noise.SmoothStep(0, 6, end-pts[i].s)
		pts[i].w *= 1 + 0.18*wf.At2(pts[i].s, 1.7)*taper
		pts[i].h *= 1 + 0.12*wf.At2(pts[i].s, 5.3)*taper
	}
	arc(pts)
}

// Local is a point's place in the cave.
type Local struct {
	// A is the lateral offset from the path, B the height above the floor.
	A, B float64
	// W and H are the width and height there; Floor the floor's height.
	W, H, Floor float64
	// End is how far past an end of the path the point lies (≤ 0: not).
	End float64
	// S is the arc length along the path (plan metres).
	S float64
	// Side is the path's horizontal side direction (x, z) there.
	Side [2]float64
}

// Local is p's place in the cave, and false when p is nowhere near it.
func (v *Void) Local(p sdf.Vec3) (Local, bool) {
	if v.Empty() {
		return Local{}, false
	}
	i, j := v.binOf(p[0], p[2])
	segs := v.bins[j*v.bnx+i]
	if len(segs) == 0 {
		return Local{}, false
	}
	best := math.Inf(1)
	var out Local
	last := len(v.pts) - 2
	for _, k := range segs {
		a, b := v.pts[k], v.pts[k+1]
		// The centreline: half a height up, but never more than
		// centreCap — measured from a tall chamber's half height, a point on
		// its floor was nearer the low tunnel's segment beyond it, whose
		// floor is higher, and the floor stepped up a few metres across the
		// chamber.
		ay, by := a.y+math.Min(a.h/2, centreCap), b.y+math.Min(b.h/2, centreCap)
		dx, dy, dz := b.x-a.x, by-ay, b.z-a.z
		l2 := dx*dx + dy*dy + dz*dz
		raw := 0.0
		if l2 > 0 {
			raw = ((p[0]-a.x)*dx + (p[1]-ay)*dy + (p[2]-a.z)*dz) / l2
		}
		t := noise.Clamp(raw, 0, 1)
		cx, cy, cz := a.x+dx*t, ay+dy*t, a.z+dz*t
		d2 := (p[0]-cx)*(p[0]-cx) + (p[1]-cy)*(p[1]-cy) + (p[2]-cz)*(p[2]-cz)
		if d2 >= best {
			continue
		}
		best = d2
		w, h := a.w+(b.w-a.w)*t, a.h+(b.h-a.h)*t
		floor := a.y + (b.y-a.y)*t
		hl := math.Hypot(dx, dz)
		sx, sz := 0.0, 1.0
		if hl > 1e-9 {
			sx, sz = -dz/hl, dx/hl
		}
		end := math.Inf(-1)
		l := math.Sqrt(l2)
		if int(k) == 0 && raw < 0 {
			end = -raw * l
		}
		if int(k) == last && raw > 1 {
			end = (raw - 1) * l
		}
		out = Local{A: (p[0]-cx)*sx + (p[2]-cz)*sz, B: p[1] - floor, W: w, H: h, Floor: floor, End: end,
			S: a.s + (b.s-a.s)*t, Side: [2]float64{sx, sz}}
	}
	if v.Ground != nil {
		if d := v.Ground(p[0], p[2]) - out.Floor; d > groundUnder && d < GroundSnap {
			lift := (d - groundUnder) * (1 - noise.SmoothStep(0.6*GroundSnap, GroundSnap, d))
			// Only to level ground: at the foot of a steep bank (a mouth cut
			// into a ledge's back wall) the bank's ground lay within reach of
			// the floor and lifted it into a hump a metre and more high; the
			// cut opens the bank instead.
			const e = 1.0
			gx := (v.Ground(p[0]+e, p[2]) - v.Ground(p[0]-e, p[2])) / (2 * e)
			gz := (v.Ground(p[0], p[2]+e) - v.Ground(p[0], p[2]-e)) / (2 * e)
			lift *= 1 - noise.SmoothStep(0.3, 0.6, math.Hypot(gx, gz))
			out.Floor += lift
			out.B -= lift
		}
	}
	return out, true
}

// centreCap caps how far above the floor the centreline that picks a
// point's nearest segment runs (Local).
const centreCap = 2.5

// far is what At returns well away from the cave: positive, a bound.
const far = 50

// section is the cross-section's signed distance at (a, b) for a half
// width hw and height h: vertical walls, a vault over them, open below (the
// floor is cut separately).
func section(a, b, hw, h, arch float64) float64 {
	arch = noise.Clamp(arch, 0.05, 1)
	bc := h * (1 - arch)
	bv := h - bc
	// The vault: an ellipse about (0, bc), first-order distance.
	ea, eb := a/hw, (b-bc)/bv
	q := math.Sqrt(ea*ea + eb*eb)
	var de float64
	if q < 1e-9 {
		de = -math.Min(hw, bv)
	} else {
		ga, gb := ea/hw/q, eb/bv/q
		de = (q - 1) / math.Max(math.Hypot(ga, gb), 1e-9)
	}
	// The walls below the vault's springing.
	dw := math.Max(math.Abs(a)-hw, b-bc)
	return math.Min(de, dw)
}

// At is the void's bound distance at p: negative inside.
func (v *Void) At(p sdf.Vec3) float64 {
	l, ok := v.Local(p)
	if !ok {
		return far
	}
	return v.at(p, l)
}

func (v *Void) at(p sdf.Vec3, l Local) float64 {
	s := &v.S
	hw := l.W / 2
	d := section(l.A, l.B, hw, l.H, s.Arch)
	// Walls and roof only: the floor's first third of a metre stays flat.
	wf := noise.SmoothStep(0.05*l.H, 0.3*l.H, l.B)
	if s.Rough > 0 && d < s.Rough*2+1 {
		d -= s.Rough * wf * (v.rough.At3(p[0], p[1], p[2]) + 0.35*v.rough2.At3(p[0], p[1], p[2]))
	}
	if v.strata != nil && d < s.Ledges+1 {
		d -= wf * v.strata.InsetAt(p)
	}
	// The floor: settled sand, rippled.
	fl := s.Sand * v.sand.At2(p[0], p[2])
	d = math.Max(d, fl-l.B)
	if l.End > 0 {
		d = math.Max(d, l.End)
	}
	if d > 1.5 {
		return d
	}
	// Drapery: rock hanging from the roof.
	if s.Drapery > 0 && s.DraperyLength > 0 {
		d = math.Max(d, -v.drapery(p, l))
	}
	// Breakdown blocks on the floor.
	if s.Blocks > 0 && l.B < s.BlockSize*1.5 {
		d = math.Max(d, -v.blockDist(p))
	}
	return d
}

// drapery is the hanging fins' signed distance at p (negative in the rock).
func (v *Void) drapery(p sdf.Vec3, l Local) float64 {
	s := &v.S
	hw := l.W / 2
	u := math.Abs(l.A) / hw
	if u >= 1 {
		return 1
	}
	arch := noise.Clamp(s.Arch, 0.05, 1)
	bc := l.H * (1 - arch)
	roof := bc + (l.H-bc)*math.Sqrt(math.Max(1-u*u, 0))
	// Patchy along the cave, so stretches of plain roof stand between the
	// draperies.
	t0 := 0.6 - 1.2*noise.Clamp(s.Drapery, 0, 1)
	patch := noise.SmoothStep(t0-0.15, t0+0.15, v.finPatch.At2(p[0], p[2]))
	if patch <= 0 {
		return 1
	}
	r := (v.fin.At2(p[0], p[2]) + 1) * 0.5 // 1 on a crest line
	const th = 0.22
	if r < 1-th-0.15 {
		return 1
	}
	core := noise.Clamp((r-(1-th))/th, 0, 1)
	hang := math.Min(s.DraperyLength, 0.5*l.H) * patch * (0.45 + 0.55*(v.finLen.At2(p[0], p[2])+1)*0.5) * math.Sqrt(core)
	sp := math.Max(s.DraperySpacing, 0.4)
	// Across the sheet: thin; down: to its hang.
	across := ((1 - th) - r) * sp * 0.6
	down := (roof - hang) - l.B
	return math.Max(across, down)
}

// StepHeight is the most a rubble block in the clear lane stands over the
// floor (m): what a walker steps over.
const StepHeight = 0.28

// placeBlocks lays the fallen blocks on their two lattices.
func (v *Void) placeBlocks() {
	s := &v.S
	if s.Blocks <= 0 || s.BlockSize <= 0 {
		return
	}
	v.blockCells = [2]float64{s.BlockSize * 1.7, s.BlockSize * 0.75}
	for lat := 0; lat < 2; lat++ {
		cell := v.blockCells[lat]
		m := map[[2]int32]block{}
		seen := map[[2]int32]bool{}
		for _, p := range v.pts {
			r := p.w/2 + cell
			i0, i1 := int32(math.Floor((p.x-r)/cell)), int32(math.Floor((p.x+r)/cell))
			j0, j1 := int32(math.Floor((p.z-r)/cell)), int32(math.Floor((p.z+r)/cell))
			for j := j0; j <= j1; j++ {
				for i := i0; i <= i1; i++ {
					k := [2]int32{i, j}
					if seen[k] {
						continue
					}
					seen[k] = true
					if b, ok := v.blockAt(lat, i, j); ok {
						m[k] = b
					}
				}
			}
		}
		v.blocks[lat] = m
	}
}

// blockAt is lattice lat's block in cell (i, j), if it has one.
func (v *Void) blockAt(lat int, i, j int32) (block, bool) {
	s := &v.S
	cell := v.blockCells[lat]
	rng := noise.NewRNG(noise.Hash64(s.Seed, 0x424C_4B00+uint64(lat), uint64(int64(i)), uint64(int64(j))))
	x := (float64(i) + rng.Range(0.15, 0.85)) * cell
	z := (float64(j) + rng.Range(0.15, 0.85)) * cell
	// The floor under it: from the path.
	l, ok := v.Local(sdf.Vec3{x, 0, z})
	if !ok {
		return block{}, false
	}
	// Local read at y = 0 gives the nearest segment in 3D; read again at
	// the floor's height there for the right one.
	l, ok = v.Local(sdf.Vec3{x, l.Floor + l.H*0.5, z})
	if !ok || l.End > 0 {
		return block{}, false
	}
	u := math.Abs(l.A) / (l.W / 2)
	if u > 1.05 {
		return block{}, false
	}
	// Fewer in the middle lane; more against the walls (fallen from the
	// vault's springing). In the lane the big blocks give way by Clear, and
	// the rubble lies low enough to step over.
	lane := 1 - noise.SmoothStep(0.25, 0.6, u)
	chance := s.Blocks * (1 - s.Clear*lane)
	if lat == 1 {
		chance = math.Min(1, s.Blocks*1.3*(1-0.5*s.Clear*lane))
	}
	if !rng.Chance(chance) {
		return block{}, false
	}
	size := s.BlockSize
	if lat == 1 {
		size *= 0.38
	}
	size *= rng.Range(0.55, 1.15)
	half := sdf.Vec3{size * rng.Range(0.35, 0.6), size * rng.Range(0.18, 0.38), size * rng.Range(0.3, 0.55)}
	// Sunk into the sand by a third of its height, more for the big ones.
	sink := rng.Range(0.1, 0.7)
	cy := l.Floor + half[1]*sink
	if lat == 1 && lane*s.Clear > 0.3 {
		// At most StepHeight proud of the floor.
		cy = math.Min(cy, l.Floor-half[1]+StepHeight)
	}
	yaw := rng.Range(0, 2*math.Pi)
	pitch := rng.Range(-0.3, 0.3)
	roll := rng.Range(-0.3, 0.3)
	rot := sdf.RotY(yaw).Mul(sdf.RotX(pitch)).Mul(sdf.RotZ(roll)).T()
	return block{c: sdf.Vec3{x, cy, z}, half: half, rot: rot}, true
}

// blockDist is the blocks' signed distance at p (negative in a block).
func (v *Void) blockDist(p sdf.Vec3) float64 {
	best := 2.0
	for lat := 0; lat < 2; lat++ {
		m := v.blocks[lat]
		if len(m) == 0 {
			continue
		}
		cell := v.blockCells[lat]
		ci, cj := int32(math.Floor(p[0]/cell)), int32(math.Floor(p[2]/cell))
		for dj := int32(-1); dj <= 1; dj++ {
			for di := int32(-1); di <= 1; di++ {
				b, ok := m[[2]int32{ci + di, cj + dj}]
				if !ok {
					continue
				}
				q := b.rot.Apply(p.Sub(b.c))
				// A box with its edges rounded a little.
				r := 0.06 * b.half[1] / 0.3
				r = math.Min(r, 0.5*math.Min(b.half[0], math.Min(b.half[1], b.half[2])))
				e := sdf.Vec3{math.Abs(q[0]) - b.half[0] + r, math.Abs(q[1]) - b.half[1] + r, math.Abs(q[2]) - b.half[2] + r}
				out := math.Sqrt(sq(math.Max(e[0], 0)) + sq(math.Max(e[1], 0)) + sq(math.Max(e[2], 0)))
				d := out + math.Min(math.Max(e[0], math.Max(e[1], e[2])), 0) - r
				best = math.Min(best, d)
			}
		}
	}
	if best < 0.4 {
		// Fractured faces: a little noise.
		best += 0.05 * v.rough2.At3(p[0]*2.3, p[1]*2.3, p[2]*2.3)
	}
	return best
}

func sq(x float64) float64 { return x * x }

// FloorAt is the cave floor's height at plan point (x, z), the lateral
// share |a|/(W/2) there, and whether (x, z) is over the cave at all. Where
// two passages cross in plan, the nearer to y is taken.
func (v *Void) FloorAt(x, y, z float64) (floor, u float64, ok bool) {
	l, ok := v.Local(sdf.Vec3{x, y, z})
	if !ok || l.End > 0 {
		return 0, 0, false
	}
	l, ok = v.Local(sdf.Vec3{x, l.Floor + l.H*0.5, z})
	if !ok || l.End > 0 {
		return 0, 0, false
	}
	return l.Floor, math.Abs(l.A) / (l.W / 2), true
}

// PlanDistance is the plan distance from (x, z) to the path's floor
// polyline, less the half width there: negative over the cave.
func (v *Void) PlanDistance(x, z float64) float64 {
	if v.Empty() {
		return math.Inf(1)
	}
	i, j := v.binOf(x, z)
	best := math.Inf(1)
	for _, k := range v.bins[j*v.bnx+i] {
		a, b := v.pts[k], v.pts[k+1]
		dx, dz := b.x-a.x, b.z-a.z
		l2 := dx*dx + dz*dz
		t := 0.0
		if l2 > 0 {
			t = noise.Clamp(((x-a.x)*dx+(z-a.z)*dz)/l2, 0, 1)
		}
		d := math.Hypot(x-a.x-dx*t, z-a.z-dz*t) - (a.w+(b.w-a.w)*t)/2
		best = math.Min(best, d)
	}
	if math.IsInf(best, 1) {
		return v.bin
	}
	return best
}

// Dense is the densified path: floor points, width, height (for the
// preview, the manifest and the stage's sections).
func (v *Void) Dense() [][5]float64 {
	out := make([][5]float64, len(v.pts))
	for i, p := range v.pts {
		out[i] = [5]float64{p.x, p.y, p.z, p.w, p.h}
	}
	return out
}

// Shape is the void as an sdf.Shape (negative inside the void).
func (v *Void) Shape() sdf.Shape {
	return sdf.Shape{F: v.At, Box: v.box, Lip: v.Lip}
}
