package place

// scatter_tiled.go — scatter in independent world tiles, so a paint stroke
// re-places only the tiles it reaches (docs/terrain-paint/PHASE_P2.md).
//
// The classic scatter (scatter.go) keeps candidates biggest first across
// the WHOLE ground: a candidate at one end can, through a chain of
// footprints, decide whether one at the other end is kept, and a rule's cap
// is counted over everything. Nothing about it is local, so no part of it
// can be redone without redoing all of it. TILED mode (ScatterSettings.
// Tiles > 0) is a scatter whose every decision is local by construction:
//
//   - TILES are world-aligned squares of Tiles metres (64, the paint
//     tiles), counted from the origin — the ground's extent does not move
//     them. Each tile runs every rule in order, as the classic scatter
//     does, over just its own ground: its 32 m blocks' candidates (seeded
//     by world block, as a region's are), satellites round the rocks it
//     kept itself (kept inside the tile), biggest first, a cap of the
//     rule's MaxInstances shared out by area.
//   - PHASES. Tiles run in four phases by the parity of (i, j): the tiles
//     of one phase are a whole tile apart, farther than any two footprints
//     can interact (checked: a scatter whose largest footprint reach is not
//     under half a tile is refused, and the stage falls back to the classic
//     scatter), so they run side by side. A tile keeps clear of the
//     footprints its EARLIER-phase neighbours kept (and of the Avoid list);
//     its later-phase neighbours keep clear of it.
//
// So a tile's rocks are a pure function of its own inputs — its ground, its
// paint, the rules — and of what its earlier-phase neighbours kept. That is
// what the CACHE keys: TileKey(i, j) is the caller's hash of the tile's own
// inputs, and the tile's key adds the hash of each earlier-phase
// neighbour's output. A tile whose key the cache knows is not scattered at
// all; a tile re-scattered to the same rocks leaves its neighbours' keys
// as they were, so a stroke's change stops spreading as soon as the rocks
// stop changing (at most three tiles out, through the phases).
//
// The result is the same whether every tile is scattered or most come from
// the cache, by construction; procgen/stages pins it (a stroke and an
// incremental run against a run from nothing, placement for placement).

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"

	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/terrain"
)

// ErrTiledReach is why a scatter cannot be tiled: a footprint (with its
// spacing, or a satellite's spread) reaches half a tile or more.
var ErrTiledReach = errors.New("a footprint reaches half a scatter tile or more")

// TileCache keeps scattered tiles across runs.
type TileCache interface {
	// Load is the tile's instances and stats when key is what it was
	// stored under.
	Load(i, j int, key string) (*ScatterTile, bool)
	// Store keeps a tile just scattered (key is "" when the caller gave no
	// TileKey for it).
	Store(t *ScatterTile, key string) error
}

// ScatterTile is one tile's output.
type ScatterTile struct {
	I, J int
	// Instances are the tile's rocks, rule by rule, each rule biggest
	// first.
	Instances []Instance
	// Stats are the tile's own counts, one per rule.
	Stats []RuleStats
	// Reused: from the cache, not scattered.
	Reused bool

	hash string
}

// Hash is the tile's output hash (its encoding's).
func (t *ScatterTile) Hash() string {
	if t.hash == "" {
		sum := sha256.Sum256(EncodeScatterTile(t))
		t.hash = hex.EncodeToString(sum[:])
	}
	return t.hash
}

// tilePhase is a tile's phase, 0..3, by the parity of its coordinates.
func tilePhase(i, j int) int { return (i & 1) | (j&1)<<1 }

// tiledReach is the farthest any footprint of s reaches: two radii times
// the spacing for a pair of the largest, and a satellite's spread.
func tiledReach(s *ScatterSettings) float64 {
	reach := 0.0
	for i := range s.Rules {
		r := &s.Rules[i]
		maxR := 0.0
		for _, v := range r.Variants {
			maxR = math.Max(maxR, v.FootprintRadius*math.Max(r.Scale[0], r.Scale[1]))
		}
		reach = math.Max(reach, 2*maxR*math.Max(r.Spacing, 1))
	}
	for _, d := range s.Avoid {
		reach = math.Max(reach, 2*d.R)
	}
	return reach
}

// scatterTiled is Scatter in tiles (file comment).
func scatterTiled(g *Ground, s *ScatterSettings, cf *CliffField, maxSp float64, pool terrain.Pool) (*ScatterResult, error) {
	ts := s.Tiles
	if math.Mod(ts, s.Block) != 0 {
		return nil, fmt.Errorf("scatter tiles of %g m are not whole %g m blocks", ts, s.Block)
	}
	if tiledReach(s) >= ts/2 {
		return nil, ErrTiledReach
	}
	b := g.Bounds()
	i0, j0 := int(math.Floor(b[0]/ts)), int(math.Floor(b[1]/ts))
	i1, j1 := int(math.Ceil(b[2]/ts))-1, int(math.Ceil(b[3]/ts))-1
	nx, nz := i1-i0+1, j1-j0+1
	if nx <= 0 || nz <= 0 {
		return &ScatterResult{Stats: make([]RuleStats, len(s.Rules))}, nil
	}
	// Each rule's cap shared out by area (at least one rock a tile).
	area := (b[2] - b[0]) * (b[3] - b[1])
	caps := make([]int, len(s.Rules))
	for ri, r := range s.Rules {
		if r.MaxInstances > 0 {
			caps[ri] = max(int(math.Ceil(float64(r.MaxInstances)*ts*ts/math.Max(area, 1))), 1)
		}
	}
	tiles := make([]*ScatterTile, nx*nz)
	at := func(i, j int) *ScatterTile {
		if i < i0 || i > i1 || j < j0 || j > j1 {
			return nil
		}
		return tiles[(j-j0)*nx+(i-i0)]
	}
	done := 0
	for phase := 0; phase < 4; phase++ {
		var list []int
		for k := range tiles {
			if tilePhase(i0+k%nx, j0+k/nx) == phase {
				list = append(list, k)
			}
		}
		err := pool.Parallel(len(list), func(n int) error {
			k := list[n]
			i, j := i0+k%nx, j0+k/nx
			var nbrs []*ScatterTile
			for dj := -1; dj <= 1; dj++ {
				for di := -1; di <= 1; di++ {
					if (di != 0 || dj != 0) && tilePhase(i+di, j+dj) < phase {
						if t := at(i+di, j+dj); t != nil {
							nbrs = append(nbrs, t)
						}
					}
				}
			}
			key := ""
			if s.TileKey != nil {
				if own := s.TileKey(i, j); own != "" {
					h := sha256.New()
					fmt.Fprintf(h, "scatter-tile\x00%s\x00", own)
					for _, t := range nbrs {
						fmt.Fprintf(h, "%d,%d=%s\n", t.I, t.J, t.Hash())
					}
					key = hex.EncodeToString(h.Sum(nil))
				}
			}
			if key != "" && s.Cache != nil {
				if t, ok := s.Cache.Load(i, j, key); ok && t.I == i && t.J == j && len(t.Stats) == len(s.Rules) {
					t.Reused = true
					tiles[k] = t
					return nil
				}
			}
			t, err := scatterTile(g, cf, s, i, j, caps, maxSp, nbrs, pool)
			if err != nil {
				return err
			}
			tiles[k] = t
			if s.Cache != nil {
				return s.Cache.Store(t, key)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		done++
		if s.Progress != nil {
			s.Progress(float32(done) / 4)
		}
	}
	res := &ScatterResult{Stats: make([]RuleStats, len(s.Rules))}
	for ri := range s.Rules {
		res.Stats[ri].Name = s.Rules[ri].Name
	}
	for _, t := range tiles {
		res.Tiles = append(res.Tiles, t)
		res.Instances = append(res.Instances, t.Instances...)
		for ri, st := range t.Stats {
			a := &res.Stats[ri]
			a.Expected += st.Expected
			a.Candidates += st.Candidates
			a.Satellites += st.Satellites
			a.Kept += st.Kept
			a.TooClose += st.TooClose
			a.TooRough += st.TooRough
			a.OffGround += st.OffGround
			a.OverTheCap += st.OverTheCap
			a.Area += st.Area
		}
	}
	return res, nil
}

// scatterTile scatters every rule over tile (ti, tj), keeping clear of the
// earlier-phase neighbours' rocks and the Avoid list.
func scatterTile(g *Ground, cf *CliffField, s *ScatterSettings, ti, tj int, caps []int, maxSp float64,
	nbrs []*ScatterTile, pool terrain.Pool) (*ScatterTile, error) {
	ts := s.Tiles
	rect := [4]float64{float64(ti) * ts, float64(tj) * ts, float64(ti+1) * ts, float64(tj+1) * ts}
	grid := newDiscGrid(2, maxSp)
	reach := ts / 2
	for _, d := range s.Avoid {
		if d.X+d.R+reach > rect[0] && d.X-d.R-reach < rect[2] && d.Z+d.R+reach > rect[1] && d.Z-d.R-reach < rect[3] {
			grid.add(d, 1)
		}
	}
	for _, t := range nbrs {
		for k := range t.Instances {
			in := &t.Instances[k]
			grid.add(in.Disc(), s.Rules[in.Rule].Spacing)
		}
	}
	out := &ScatterTile{I: ti, J: tj}
	per := int(math.Ceil(s.Block / s.Cell))
	cell := s.Block / float64(per)
	nb := int(math.Round(ts / s.Block))
	reg := s.Region
	for ri := range s.Rules {
		if err := pool.Err(); err != nil {
			return nil, err
		}
		r := &s.Rules[ri]
		st := RuleStats{Name: r.Name}
		seed := noise.Hash64(s.Seed, 0x7363, uint64(ri))
		wseed := noise.Hash64(seed, 0x636c)
		weight := func(x, z float64) float64 {
			w := r.Weight(g, cf, wseed, x, z)
			if reg != nil && w > 0 {
				w *= reg.At(x, z)
			}
			return w
		}
		var cands []candidate
		ws := make([]float64, per*per)
		cum := make([]float64, per*per)
		for bz := 0; bz < nb; bz++ {
			for bx := 0; bx < nb; bx++ {
				wbx, wbz := int64(ti*nb+bx), int64(tj*nb+bz)
				x0, z0 := float64(wbx)*s.Block, float64(wbz)*s.Block
				sum := 0.0
				for q := range ws {
					ws[q] = 0
				}
				for j := 0; j < per; j++ {
					for i := 0; i < per; i++ {
						x, z := x0+(float64(i)+0.5)*cell, z0+(float64(j)+0.5)*cell
						if !g.Contains(x, z, 0) {
							continue
						}
						w := weight(x, z)
						ws[j*per+i] = w
						sum += w
					}
				}
				a := sum * cell * cell
				expected := r.Density / 100 * a
				st.Area += a
				st.Expected += expected
				if sum == 0 || r.Density <= 0 {
					continue
				}
				rng := noise.NewRNG(noise.Hash64(seed, 0x776f726c64, uint64(wbx), uint64(wbz)))
				n := int(expected)
				if rng.Chance(expected - float64(n)) {
					n++
				}
				acc := 0.0
				for q, w := range ws {
					acc += w
					cum[q] = acc
				}
				for k := 0; k < n; k++ {
					u := rng.Float64() * acc
					ci := min(sort.SearchFloat64s(cum, u), len(cum)-1)
					i, j := ci%per, ci/per
					x := x0 + (float64(i)+rng.Float64())*cell
					z := z0 + (float64(j)+rng.Float64())*cell
					c := r.draw(rng, x, z, (bz*nb+bx)<<20|k)
					if r.Mask != nil && r.Mask(x, z) <= 0 {
						continue
					}
					cands = append(cands, c)
				}
			}
		}
		// Satellites round the rocks this tile's earlier rules kept, kept
		// inside the tile.
		if r.Satellites > 0 && len(out.Instances) > 0 {
			for hi := range out.Instances {
				h := &out.Instances[hi]
				rng := noise.NewRNG(noise.Hash64(seed, 0x736174, uint64(int64(ti)), uint64(int64(tj)), uint64(hi)))
				e := r.Satellites * h.Radius
				n := int(e)
				if rng.Chance(e - float64(n)) {
					n++
				}
				for k := 0; k < n; k++ {
					a := rng.Range(0, 2*math.Pi)
					d := h.Radius * (1 + r.SatelliteSpread*math.Pow(rng.Float64(), 1.6))
					x, z := h.Pos[0]+d*math.Cos(a), h.Pos[2]+d*math.Sin(a)
					if x < rect[0] || x >= rect[2] || z < rect[1] || z >= rect[3] {
						continue
					}
					if !g.Contains(x, z, 0) || !rng.Chance(math.Min(weight(x, z), 1)) {
						continue
					}
					cands = append(cands, r.draw(rng, x, z, 1<<40|hi<<12|k))
					st.Satellites++
				}
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
			if caps[ri] > 0 && st.Kept >= caps[ri] {
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
			out.Instances = append(out.Instances, in)
			st.Kept++
		}
		out.Stats = append(out.Stats, st)
	}
	return out, nil
}

// ---------- the tile codec ----------
//
// A tile as a cache stores it: exact (every float as its bits), so a tile
// read back places byte-identical placements. Little-endian:
//
//	"FSCT1\0" i32 i, i32 j, u32 rules, per rule: str name, f64 expected,
//	area, u32 candidates, satellites, kept, tooClose, tooRough, offGround,
//	overTheCap; u32 instances, per instance: u32 rule, variant, f64 pos×3,
//	rot×9, scale, radius, str collision, str kind.

var scatterTileMagic = []byte("FSCT1\x00")

// EncodeScatterTile is a tile's cache bytes.
func EncodeScatterTile(t *ScatterTile) []byte {
	var b bytes.Buffer
	b.Write(scatterTileMagic)
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	str := func(s string) { w(uint32(len(s))); b.WriteString(s) }
	w(int32(t.I))
	w(int32(t.J))
	w(uint32(len(t.Stats)))
	for _, st := range t.Stats {
		str(st.Name)
		w(st.Expected)
		w(st.Area)
		for _, v := range []int{st.Candidates, st.Satellites, st.Kept, st.TooClose, st.TooRough, st.OffGround, st.OverTheCap} {
			w(uint32(v))
		}
	}
	w(uint32(len(t.Instances)))
	for k := range t.Instances {
		in := &t.Instances[k]
		w(uint32(in.Rule))
		w(uint32(in.Variant))
		w(in.Pos)
		w([3][3]float64(in.Rot))
		w(in.Scale)
		w(in.Radius)
		str(in.Collision)
		str(in.Kind)
	}
	return b.Bytes()
}

// DecodeScatterTile reads EncodeScatterTile's bytes.
func DecodeScatterTile(data []byte) (*ScatterTile, error) {
	if !bytes.HasPrefix(data, scatterTileMagic) {
		return nil, errors.New("not a scatter tile")
	}
	rd := bytes.NewReader(data[len(scatterTileMagic):])
	var err error
	r := func(v any) {
		if err == nil {
			err = binary.Read(rd, binary.LittleEndian, v)
		}
	}
	str := func() string {
		var n uint32
		r(&n)
		if err != nil || int(n) > rd.Len() {
			if err == nil {
				err = errors.New("truncated")
			}
			return ""
		}
		if n == 0 {
			return ""
		}
		buf := make([]byte, n)
		_, err = io.ReadFull(rd, buf)
		return string(buf)
	}
	t := &ScatterTile{}
	var i, j int32
	var nr, ni uint32
	r(&i)
	r(&j)
	r(&nr)
	if err != nil || nr > 1<<16 {
		return nil, fmt.Errorf("scatter tile: %v", err)
	}
	t.I, t.J = int(i), int(j)
	for k := 0; k < int(nr); k++ {
		var st RuleStats
		st.Name = str()
		r(&st.Expected)
		r(&st.Area)
		var v [7]uint32
		r(&v)
		st.Candidates, st.Satellites, st.Kept, st.TooClose = int(v[0]), int(v[1]), int(v[2]), int(v[3])
		st.TooRough, st.OffGround, st.OverTheCap = int(v[4]), int(v[5]), int(v[6])
		t.Stats = append(t.Stats, st)
	}
	r(&ni)
	if err != nil || int(ni) > rd.Len() {
		return nil, fmt.Errorf("scatter tile: %v", err)
	}
	t.Instances = make([]Instance, ni)
	for k := range t.Instances {
		in := &t.Instances[k]
		var rule, variant uint32
		var rot [3][3]float64
		r(&rule)
		r(&variant)
		r(&in.Pos)
		r(&rot)
		r(&in.Scale)
		r(&in.Radius)
		in.Rule, in.Variant, in.Rot = int(rule), int(variant), Mat3(rot)
		in.Collision = str()
		in.Kind = str()
	}
	if err != nil {
		return nil, fmt.Errorf("scatter tile: %w", err)
	}
	return t, nil
}
