package world

// volume_plants.go — a biome growing inside a scatter volume.
//
// The lattice kinds (grass, rock, wildflower, cattail, moss) are generated in
// a vertex shader from gl_InstanceID: there is no list of blades anywhere, so
// nothing can cull, LOD or skin them one by one. That is right for a lawn and
// wrong for a forest. `kind: biome` instead PLACES a terrain biome's species
// on the CPU, once, into ordinary FoliageBatches — the same instances terrain
// paint makes — and the foliage renderer draws them as it draws a terrain's
// or a placed tree's (render/foliage_volumes.go). Trees, logs and boulders
// therefore get Nanite foliage, wind bones, shadow proxies and GI; ground
// cover gets the classic tiers, exactly as on terrain.
//
// # What carries over from terrain placement
//
// The candidate grid (one jittered candidate per cell at the species'
// spacing, thinned by density and the per-shape amounts), the slope range,
// patchiness, and every look of the plant itself (growFoliage: variant, size,
// axis, colours, rank) — so a rainforest volume reads as rainforest terrain.
//
// # What does not
//
// A volume's ground is found by casting down through whatever geometry is
// under it, so there is no block grid to read the rest of the ecology from:
// sky exposure, the foot of walls, the lip of drops, moisture and clearance
// are all taken as neutral (ignored), and only species that grow on ground
// take part — wall and ceiling growers, and vines hung from a cliff's lip,
// have no surface here to grow from. The biome boundary's fray is the
// volume's own EdgeFadeDistance instead of painted coverage.

import (
	"hash/fnv"
	"log"
	"math"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/go-gl/mathgl/mgl32"
)

// VolumeGround is the ground under (x, z) in a volume's own frame: the
// surface point and its normal, or ok false where the column meets nothing.
// It is called from several goroutines at once.
type VolumeGround func(x, z float32) (p, n mgl32.Vec3, ok bool)

// MaxVolumePlantCandidates bounds one volume's candidates. A biome's grass at
// a few tenths of a unit's spacing over a large volume would otherwise be
// millions of ground casts and instances; past the cap no species is spaced
// closer than a floor chosen to fit, so the ground cover thins and the trees
// stand where they would have.
const MaxVolumePlantCandidates = 400_000

// HasPlants reports whether v grows a biome (and so draws through the
// foliage renderer rather than the lattice).
func (v *Volume) HasPlants() bool {
	s := v.Scatter
	return s != nil && s.Kind == ScatterBiome
}

// VolumeFootprint is the XZ box v's plants can stand in, in its own frame:
// its bounds, widened to the extents' length when rotated so the box holds
// at any turn.
func VolumeFootprint(v *Volume) (lo, hi mgl32.Vec2) {
	h := v.HalfExtents()
	if v.Rotation != (mgl32.Vec3{}) {
		r := h.Len()
		h = mgl32.Vec3{r, r, r}
	}
	return mgl32.Vec2{v.Position.X() - h.X(), v.Position.Z() - h.Z()},
		mgl32.Vec2{v.Position.X() + h.X(), v.Position.Z() + h.Z()}
}

// volumeInset is how far (x, z) lies inside v's footprint, and whether it
// does — the lattice shader's pointInVolume/volEdgeDist (biome_scatter_common
// .glsl): the shape's cross-section through its own centre plane, so a
// volume need not be tall enough to enclose the ground it covers.
func volumeInset(v *Volume, inv mgl32.Mat4, x, z float32) (float32, bool) {
	rel := mgl32.Vec3{x - v.Position.X(), 0, z - v.Position.Z()}
	q4 := inv.Mul4x1(rel.Vec4(0))
	qx, qz := q4.X(), q4.Z()
	h := v.HalfExtents()
	var d float32
	switch v.Shape {
	case VolumeSphere:
		r := max(h.X(), h.Y(), h.Z())
		d = r - float32(math.Hypot(float64(qx), float64(qz)))
	case VolumeCylinder:
		r := max(h.X(), h.Z())
		d = r - float32(math.Hypot(float64(qx), float64(qz)))
	default:
		d = min(h.X()-absf(qx), h.Z()-absf(qz))
	}
	return d, d >= 0
}

// VolumePlantsKey is everything about v that changes what GrowVolumePlants
// places, apart from the ground under it: the caller folds that in.
func VolumePlantsKey(v *Volume) uint64 {
	h := fnv.New64a()
	var b [4]byte
	put := func(f float32) {
		u := math.Float32bits(f)
		b[0], b[1], b[2], b[3] = byte(u), byte(u>>8), byte(u>>16), byte(u>>24)
		h.Write(b[:])
	}
	h.Write([]byte(v.ID))
	h.Write([]byte{0})
	for _, f := range []float32{v.Position[0], v.Position[1], v.Position[2], v.Rotation[0], v.Rotation[1], v.Rotation[2],
		v.HalfX, v.HalfY, v.HalfZ, float32(v.Shape)} {
		put(f)
	}
	if s := v.Scatter; s != nil {
		bi := &s.Biome
		h.Write([]byte(bi.Preset))
		h.Write([]byte{0})
		for _, f := range []float32{bi.Density, bi.Tint[0], bi.Tint[1], bi.Tint[2],
			s.MinSlopeAngle, s.MaxSlopeAngle, s.EdgeFadeDistance} {
			put(f)
		}
		keys := make([]string, 0, len(bi.Amounts))
		for k := range bi.Amounts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			h.Write([]byte(k))
			put(bi.Amounts[k])
		}
	}
	// A biome file edited on disk regrows every volume using it.
	put(float32(FoliagePresetGeneration()))
	return h.Sum64() | 1
}

// volumePlantCand is one candidate: its species, where, and its hash.
type volumePlantCand struct {
	si   int
	x, z float32
	h    uint32
	// p, n, ok are the ground under it, filled in by the casting pass.
	p, n mgl32.Vec3
	ok   bool
}

// GrowVolumePlants places the biome a ScatterBiome volume names over its
// footprint, with ground answering what is under each candidate. Batches
// come back sorted by (shape, variant), each one's instances by Rank, in the
// volume's own frame. Deterministic: the same volume over the same ground
// grows the same plants.
func GrowVolumePlants(v *Volume, ground VolumeGround) []FoliageBatch {
	if !v.HasPlants() || !v.HasBounds() || ground == nil {
		return nil
	}
	s := v.Scatter
	biome := &s.Biome
	def := biome.Def()
	density := biome.EffectiveDensity()
	tint := biome.EffectiveTint()
	lo, hi := VolumeFootprint(v)
	area := float64((hi.X() - lo.X()) * (hi.Y() - lo.Y()))

	// Each species' spacing, and the shared widening that keeps the total
	// under the cap.
	spacing := make([]float32, len(def.Species))
	var expect float64
	for si := range def.Species {
		sp := &def.Species[si]
		amount := biome.Amount(sp.Shape)
		if amount <= 0 || sp.Where&OnGround == 0 || sp.TopEdge || sp.Spacing <= 0 {
			continue
		}
		sc := max(sp.Spacing/float32(math.Sqrt(float64(density*amount))), 0.08)
		spacing[si] = sc
		expect += area / float64(sc*sc)
	}
	if expect > MaxVolumePlantCandidates {
		// A spacing floor, raised until the total fits: the densest species
		// (grass, litter) thin out and trees, whose spacing is already
		// above it, keep theirs.
		count := func(floor float32) float64 {
			n := 0.0
			for _, sc := range spacing {
				if sc > 0 {
					sc = max(sc, floor)
					n += area / float64(sc*sc)
				}
			}
			return n
		}
		lo, hi := float32(0), float32(math.Sqrt(area/MaxVolumePlantCandidates*float64(len(spacing))))+1
		for k := 0; k < 40; k++ {
			mid := (lo + hi) / 2
			if count(mid) > MaxVolumePlantCandidates {
				lo = mid
			} else {
				hi = mid
			}
		}
		for si, sc := range spacing {
			if sc > 0 {
				spacing[si] = max(sc, hi)
			}
		}
		log.Printf("volume %s: biome %s would place ~%.0f plants; nothing closer than %.2f units, to stay under %d",
			v.ID, def.Name, expect, hi, MaxVolumePlantCandidates)
	}

	seed := volumePlantSeed(v, def.Name)
	inv := EulerMatrixYXZ(v.Rotation).Transpose()
	var cands []volumePlantCand
	for si := range def.Species {
		sc := spacing[si]
		if sc <= 0 {
			continue
		}
		sseed := HashInts(seed, si)
		i0, i1 := int(math.Floor(float64(lo.X()/sc))), int(math.Floor(float64(hi.X()/sc)))
		j0, j1 := int(math.Floor(float64(lo.Y()/sc))), int(math.Floor(float64(hi.Y()/sc)))
		for i := i0; i <= i1; i++ {
			for j := j0; j <= j1; j++ {
				h := HashInts(sseed, i, j)
				x := (float32(i) + Unit(h)) * sc
				z := (float32(j) + Unit(Hash32(h^0x68bc))) * sc
				if _, in := volumeInset(v, inv, x, z); !in {
					continue
				}
				cands = append(cands, volumePlantCand{si: si, x: x, z: z, h: h})
			}
		}
	}
	castVolumeGround(cands, ground)

	minSlope, maxSlope := ResolvedMinSlope(s), ResolvedMaxSlope(s)
	fade := s.EdgeFadeDistance
	up := mgl32.Vec3{0, 1, 0}
	byKey := map[[2]int]*FoliageBatch{}
	for ci := range cands {
		c := &cands[ci]
		if !c.ok {
			continue
		}
		sp := &def.Species[c.si]
		n := c.n
		if l := n.Len(); l > 1e-8 {
			n = n.Mul(1 / l)
		} else {
			n = up
		}
		surf := OnGround
		if n.Y() <= 0.45 {
			surf = OnWall
		}
		if sp.Where&surf == 0 {
			continue
		}
		ang := float32(math.Acos(float64(min(max(n.Y(), -1), 1)))) * 180 / math.Pi
		suit := float32(1)
		if surf == OnGround && (sp.Slope[0] > 0 || sp.Slope[1] > 0) {
			suit *= band(ang, sp.Slope[0], sp.Slope[1], 6, 90)
		}
		// The volume's own gate narrows the species' ranges, never widens.
		suit *= band(ang, minSlope, maxSlope, 6, 90)
		sseed := HashInts(seed, c.si)
		if sp.Cluster > 0 {
			cs := max(sp.ClusterSize, 0.5)
			nv := FBM3(c.p.Mul(1/cs), sseed^0xc1a5, 3)
			thr := sp.Cluster * 0.7
			suit *= SmoothStep(thr-0.08, thr+0.08, nv)
		}
		if fade > 0 {
			d, _ := volumeInset(v, inv, c.x, c.z)
			jit := ValueNoise3(c.p.Mul(1.3), sseed^0xed6e) - 0.5
			suit *= SmoothStep(0.35, 0.9, d/fade+jit*0.6)
		}
		if suit <= 0 || Unit(Hash32(c.h^0x2f1c3b)) >= suit {
			continue
		}
		variant, in := growFoliage(sp, c.si, c.p, n, surf, min(suit, 1), c.h, tint, seed, up)
		key := [2]int{int(sp.Shape), variant}
		bt := byKey[key]
		if bt == nil {
			bt = &FoliageBatch{Shape: sp.Shape, Variant: variant}
			byKey[key] = bt
		}
		bt.Instances = append(bt.Instances, in)
	}

	keys := make([][2]int, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	out := make([]FoliageBatch, 0, len(keys))
	for _, k := range keys {
		bt := byKey[k]
		sort.SliceStable(bt.Instances, func(i, j int) bool { return bt.Instances[i].Rank < bt.Instances[j].Rank })
		out = append(out, *bt)
	}
	return out
}

// castVolumeGround fills in every candidate's ground, spread over the CPUs:
// each candidate is its own cast, so the result does not depend on how they
// were shared out.
func castVolumeGround(cands []volumePlantCand, ground VolumeGround) {
	workers := min(runtime.NumCPU(), max(len(cands)/512, 1))
	if workers <= 1 {
		for i := range cands {
			c := &cands[i]
			c.p, c.n, c.ok = ground(c.x, c.z)
		}
		return
	}
	const chunk = 256
	var next atomic.Int64
	var wg sync.WaitGroup
	for k := 0; k < workers; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				a := int(next.Add(chunk)) - chunk
				if a >= len(cands) {
					return
				}
				for i := a; i < min(a+chunk, len(cands)); i++ {
					c := &cands[i]
					c.p, c.n, c.ok = ground(c.x, c.z)
				}
			}
		}()
	}
	wg.Wait()
}

// volumePlantSeed decorrelates volumes from each other and biomes from each
// other: two overlapping volumes do not grow their trees on the same spots.
func volumePlantSeed(v *Volume, preset string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(v.ID))
	h.Write([]byte{0})
	h.Write([]byte(preset))
	return h.Sum32()
}
