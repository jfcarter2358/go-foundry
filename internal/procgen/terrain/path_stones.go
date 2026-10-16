package terrain

// path_stones.go — the stones a path lays (path.go, step 3): where each
// goes, how big it is and which way it faces. The stage seats each on the
// final ground as a rock variant (procgen/stages ground_edit_path_stones.go).
//
// FLAGSTONES (rock_path). Rows across the path, a row every pitch along it.
// A row holds n = round(width / (0.7 m × StoneSize)) stones (at least
// one; a quarter of the rows one fewer and wider, a sixth one more and
// narrower, so the joints do not line up row on row), each in a cell of
// width / n; a stone is 90–100% of its cell across
// and of the pitch along (a scanned rock is rounded, so its rectangle's
// corners are empty: the joints come from the rocks' own outlines), shifted
// a little each way and turned up to ±9° off the path's heading, so the
// joints are irregular rather than a grid. A stone where the worn edge has eaten the
// surface (the style's surface mask under 0.5 at its centre) is left out,
// so the stones follow the ragged edge.
//
// STEPPING STONES (stepping_stones, and every FORD). One line down the
// middle, a stride apart (Stride ×0.9–1.1, 0.6–0.8 m at the default), each
// stone alternately a little left and right of the centreline (a zig-zag
// of up to 12 cm, jittered), 0.4–0.5 m along and 0.5–0.62 m across ×
// StoneSize, turned up to ±14°.
//
// A FORD is where a path with Ford crosses water: stepping stones over
// every wet stretch and a metre either side. Each knows the water's
// level, so the stage makes it tall enough to stand 7 cm above the surface
// from the bed, and bigger the deeper it stands (×(1 + 0.6·depth)); where
// the water is deeper than FordMaxDepth the stone is left out (wade, or
// build a bridge). Flagstones stop within a metre of
// water: the ford takes over.
//
// NO OVERLAPS. Every stone is an oriented rectangle (taken at stoneCore of
// its size: the rock inside it is rounded); a new one that overlaps an
// earlier (of any path, its own included — paths may cross) is
// shrunk by 15% up to twice, and dropped if it still does.
//
// Randomness comes from a hash of the path's seed and the stone's index, so
// a stone's place depends only on its path.

import (
	"math"

	"foundry/internal/procgen/noise"
)

// FordMaxDepth is the deepest water a ford stone stands in (m).
const FordMaxDepth = 0.75

// FordProud is how far above the water a ford stone's top stands (m).
const FordProud = 0.07

// PathStone is one stone a path lays.
type PathStone struct {
	// Path is the path's name; Kind StonesFlag, StonesStep or StonesFord.
	Path, Kind string
	// X, Z is its centre; Yaw the turn of its length (+Z in the rock's
	// frame) from +Z toward +X — atan2 of the heading, as RotY reads it.
	X, Z, Yaw float64
	// Length is its size along the path, Width across, Thick its height
	// (m; a ford stone's is worked out from Level by the stage).
	Length, Width, Thick float64
	// Level is the water's surface a ford stone must stand above (NaN on
	// dry ground).
	Level float64
}

// obbAxes are a stone's footprint axes: along its length, and across.
func (s *PathStone) obbAxes() (ax, az [2]float64) {
	sn, cs := math.Sincos(s.Yaw)
	return [2]float64{sn, cs}, [2]float64{cs, -sn} // along (model +Z), across (model +X)
}

// stoneCore is the share of a stone's rectangle its rock fills for the
// overlap test: a scanned rock is rounded, so rectangles whose corners
// cross still leave the rocks a joint apart.
const stoneCore = 0.85

// stonesOverlap reports whether two stones' footprints (their rectangles
// at stoneCore) overlap: separating axes of two rectangles.
func stonesOverlap(a, b *PathStone) bool {
	if math.Hypot(a.X-b.X, a.Z-b.Z) > 0.5*stoneCore*(math.Hypot(a.Length, a.Width)+math.Hypot(b.Length, b.Width)) {
		return false
	}
	aAl, aAc := a.obbAxes()
	bAl, bAc := b.obbAxes()
	d := [2]float64{b.X - a.X, b.Z - a.Z}
	dot := func(u, v [2]float64) float64 { return u[0]*v[0] + u[1]*v[1] }
	for _, axis := range [][2]float64{aAl, aAc, bAl, bAc} {
		ra := stoneCore * (math.Abs(dot(aAl, axis))*a.Length/2 + math.Abs(dot(aAc, axis))*a.Width/2)
		rb := stoneCore * (math.Abs(dot(bAl, axis))*b.Length/2 + math.Abs(dot(bAc, axis))*b.Width/2)
		if math.Abs(dot(d, axis)) > ra+rb {
			return false
		}
	}
	return true
}

// stoneSet holds the stones kept so far, binned for the overlap test.
type stoneSet struct {
	all  []PathStone
	bins map[[2]int][]int
}

func (s *stoneSet) bin(x, z float64) [2]int { return [2]int{int(math.Floor(x / 2)), int(math.Floor(z / 2))} }

// add keeps st unless it overlaps a kept stone after two shrinks.
func (s *stoneSet) add(st PathStone) bool {
	for try := 0; try < 3; try++ {
		hit := false
		b := s.bin(st.X, st.Z)
		for di := -1; di <= 1 && !hit; di++ {
			for dj := -1; dj <= 1 && !hit; dj++ {
				for _, k := range s.bins[[2]int{b[0] + di, b[1] + dj}] {
					if stonesOverlap(&s.all[k], &st) {
						hit = true
						break
					}
				}
			}
		}
		if !hit {
			s.bins[b] = append(s.bins[b], len(s.all))
			s.all = append(s.all, st)
			return true
		}
		st.Length *= 0.85
		st.Width *= 0.85
	}
	return false
}

// PathStones lays out every path's stones (file comment). specs are the
// specs res was laid from; surface is res.Masks (the style surfaces read
// to drop edge stones), sampled on g.
func PathStones(g *Grid, specs []PathSpec, res *PathResult) []PathStone {
	set := &stoneSet{bins: map[[2]int][]int{}}
	for pi := range res.Paths {
		p := &res.Paths[pi]
		sp := &specs[p.Spec]
		sty := PathStyles[p.Style]
		sh := sp.Shape
		if sh.Stones <= 0 {
			continue
		}
		size := math.Max(sh.StoneSize, 0.2)
		var n uint64
		rnd := func() float64 {
			n++
			return float64(noise.Hash64(sp.Seed, 0x73746f6e, n)>>11) / float64(1<<53)
		}
		keep := func() bool { return sh.Stones >= 1 || rnd() < sh.Stones }
		surf := res.Masks[PathSurfaceMask(p.Style)]
		mg := *g
		mg.H = surf
		// Flagstones.
		if sty.Stones == StonesFlag {
			for s := 0.0; s < p.Length; {
				st := p.at(s)
				w := st.Width
				nc := max(int(math.Round(w/(0.7*size))), 1)
				pitch := math.Min(w/float64(nc), 0.85*size) * (0.95 + 0.1*rnd())
				if s == 0 {
					s = pitch / 2
					continue
				}
				// A row of one stone fewer (wider stones) or one more now
				// and then, so the joints do not line up row on row.
				if r := rnd(); nc >= 2 && r < 0.25 && w/float64(nc-1) <= 1.05*size {
					nc--
				} else if nc >= 2 && r > 0.85 {
					nc++
				}
				cw := w / float64(nc)
				if st.WetDist > 1 {
					for c := 0; c < nc; c++ {
						u := -w/2 + cw*(float64(c)+0.5) + (rnd()*2-1)*0.08*cw
						a := s + (rnd()*2-1)*0.1*pitch
						at := p.at(a)
						x, z := at.X-at.TZ*u, at.Z+at.TX*u
						ps := PathStone{Path: p.Name, Kind: StonesFlag, X: x, Z: z,
							Yaw:    math.Atan2(at.TX, at.TZ) + (rnd()*2-1)*0.16,
							Length: pitch * (0.9 + 0.1*rnd()), Width: cw * (0.9 + 0.1*rnd()),
							Thick: (0.09 + 0.05*rnd()) * size, Level: math.NaN()}
						if float64(mg.Sample(x, z)) < 0.5*sty.Surface || !keep() {
							continue
						}
						set.add(ps)
					}
				}
				s += pitch
			}
		}
		// Stepping stones, and fords.
		stride := math.Max(sh.Stride, 0.3)
		side := 1.0
		for s := stride / 2; s < p.Length; s += stride * (0.9 + 0.2*rnd()) {
			st := p.at(s)
			ford := sty.Ford && st.WetDist <= 1
			if sty.Stones != StonesStep && !ford {
				continue
			}
			kind := StonesStep
			if st.Wet {
				kind = StonesFord
			}
			level, grow := math.NaN(), 1.0
			if st.Wet {
				level = st.Level
				depth := level - float64(g.Sample(st.X, st.Z))
				if depth > FordMaxDepth {
					side = -side
					continue
				}
				// A stone standing in deeper water is a bigger one (it
				// would be a pillar otherwise).
				grow = 1 + 0.6*math.Max(depth, 0)
			}
			zig := math.Min(0.12, 0.15*st.Width) * (0.6 + 0.4*rnd())
			u := side*zig + (rnd()*2-1)*0.04
			side = -side
			x, z := st.X-st.TZ*u, st.Z+st.TX*u
			ps := PathStone{Path: p.Name, Kind: kind, X: x, Z: z,
				Yaw:    math.Atan2(st.TX, st.TZ) + (rnd()*2-1)*0.25,
				Length: (0.4 + 0.1*rnd()) * size * grow, Width: math.Min((0.5+0.12*rnd())*size, 0.9*math.Max(st.Width, 0.5)) * grow,
				Thick: (0.12 + 0.06*rnd()) * size, Level: level}
			if !keep() {
				continue
			}
			set.add(ps)
		}
	}
	return set.all
}
