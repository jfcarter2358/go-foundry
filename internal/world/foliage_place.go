package world

// foliage_place.go — turning painted faces into plant instances.
//
// # Candidates
//
// Each species lays a jittered grid over every plane of faces, at its own
// spacing, aligned to the chunk: cell (i, j) of the plane holds one candidate
// at a hashed offset inside it. A candidate belongs to the face whose square
// it falls in, so neighbouring faces continue one pattern with no seam, no
// face gets two candidates too close together, and a tree's spacing holds
// without any search over the trees already placed. The candidate is then
// carried onto the face's real surface (TerrainFace.PointAt) — in smooth view
// the surface net, not the block's square.
//
// # Suitability
//
// A candidate takes with a probability that is the product of how well the
// spot suits the species:
//
//   - surface: ground, wall or ceiling, read from the surface normal there,
//     and for ground the slope;
//   - light: the fraction of a few rays up through the blocks that reach the
//     sky, from the air in front of the face — under an overhang, in a pit,
//     under a cliff;
//   - edge: how much wall rises around the air in front of the face (the
//     foot of a cliff, where stones fall and ferns shelter) and how much of
//     a drop lies beside the block (the lip, where trees do not stand);
//   - moisture: how low the ground is within the terrain and how sheltered;
//   - patchiness: a low-frequency noise field per species, so flowers come in
//     drifts and dry grass leaves bare ground between tufts;
//   - the biome boundary: how much of the ground around the candidate is
//     painted with the same biome, blurred by a noise jitter so the edge of a
//     painted area frays naturally rather than stopping at a block line.
//
// Better spots also grow bigger plants.

import (
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// terrainFoliageReach is how many blocks around an edit can change the
// foliage of faces they do not touch (neighbours' paint, walls rising
// beside the ground, drops beside it).
const terrainFoliageReach = 3

// foliageSkyReach is how far, in blocks, the light rays look for sky.
const foliageSkyReach = 24

// FoliageInstance is one plant, in the terrain's chunk space.
type FoliageInstance struct {
	Pos mgl32.Vec3
	// Up is the unit axis it grows along.
	Up    mgl32.Vec3
	Yaw   float32
	Scale float32
	// Color is its main colour; Accent its flowers, caps or berries.
	Color, Accent mgl32.Vec3
	// Rank is a uniform random number: a batch is sorted by it, so drawing
	// its first n instances thins it evenly.
	Rank float32
}

// FoliageBatch is every instance of one mesh in one section, sorted by Rank.
type FoliageBatch struct {
	Shape     FoliageShape
	Variant   int
	Instances []FoliageInstance
}

// FoliageSection is the foliage growing on one terrain section's faces.
type FoliageSection struct {
	Batches []FoliageBatch
	// Lo, Hi are the chunk-space box every instance fits in.
	Lo, Hi mgl32.Vec3
	Count  int
}

// FoliageExtent is roughly how tall and how wide each shape grows at scale
// 1, for bounds.
var FoliageExtent = [FoliageShapeCount][2]float32{
	FoliageGrass: {0.6, 0.35}, FoliageTallGrass: {1.45, 0.7}, FoliageFlower: {0.75, 0.25},
	FoliageFern: {0.9, 0.9}, FoliageShrub: {1.6, 1.3}, FoliageBroadleaf: {9, 4.5},
	FoliageConifer: {12, 3.5}, FoliageRock: {1, 0.7}, FoliagePebbles: {0.2, 0.6},
	FoliageMoss: {0.3, 0.6}, FoliageVine: {2.6, 0.4}, FoliageReed: {2, 0.45},
	FoliageMushroom: {0.25, 0.25}, FoliageLitter: {0.05, 0.45}, FoliageDeadwood: {0.35, 1.6},
	FoliagePalm: {8, 4.8}, FoliageCactus: {4.2, 1.3}, FoliageAcacia: {6, 5.5},
	FoliageJunglePlant: {2.4, 1.8}, FoliageDeadBush: {1.2, 0.85},
	FoliageIce: {3.4, 1.6},
}

// foliageFaceEnv is what the ecology reads for one face.
type foliageFaceEnv struct {
	sky, wallNear, cliffEdge, moist float32
	// same[i][j] is how much the in-plane neighbour at (i-1, j-1) shares
	// this face's biome: 1, 0, or 1 where there is no neighbouring face.
	same [3][3]float32
}

type foliageBuild struct {
	t              *Terrain
	sky            map[int]float32
	topLo, topSpan float32
	byKey          map[[2]int]*FoliageBatch
}

// BuildFoliage places the foliage growing on section s's painted faces.
// Main thread only (it reads the surface).
func (t *Terrain) BuildFoliage(s *TerrainSection) *FoliageSection {
	out := &FoliageSection{}
	inf := float32(math.Inf(1))
	out.Lo, out.Hi = mgl32.Vec3{inf, inf, inf}, mgl32.Vec3{-inf, -inf, -inf}
	if len(t.Paint) == 0 || len(t.Biomes) == 0 || s.Empty() {
		return out
	}
	b := &foliageBuild{t: t, sky: map[int]float32{}, byKey: map[[2]int]*FoliageBatch{}}
	b.topLo, b.topSpan = t.surfaceRange()
	for fi := range s.Faces {
		f := &s.Faces[fi]
		if t.Paint[f.Key] == 0 {
			continue
		}
		b.face(f, out)
	}
	keys := make([][2]int, 0, len(b.byKey))
	for k := range b.byKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	for _, k := range keys {
		bt := b.byKey[k]
		sort.Slice(bt.Instances, func(i, j int) bool { return bt.Instances[i].Rank < bt.Instances[j].Rank })
		out.Batches = append(out.Batches, *bt)
		out.Count += len(bt.Instances)
	}
	return out
}

// surfaceRange is the lowest top-of-column height and the span up to the
// highest, in blocks, cached until the next edit.
func (t *Terrain) surfaceRange() (lo, span float32) {
	c := t.ensureMesh()
	if c.topGen != t.Generation {
		c.topGen = t.Generation
		mn, mx := t.SizeY, -1
		for z := 0; z < t.SizeZ; z++ {
			for x := 0; x < t.SizeX; x++ {
				for y := t.SizeY - 1; y >= 0; y-- {
					if t.Blocks[t.Index(x, y, z)] != 0 {
						mn, mx = min(mn, y), max(mx, y)
						break
					}
				}
			}
		}
		if mx < 0 {
			mn, mx = 0, 0
		}
		c.topLo, c.topSpan = float32(mn), float32(mx-mn+1)
	}
	return c.topLo, c.topSpan
}

// skyAt is the fraction of sky the air block (x, y, z) sees.
func (b *foliageBuild) skyAt(x, y, z int) float32 {
	t := b.t
	if !t.In(x, y, z) {
		if y >= t.SizeY {
			return 1
		}
	}
	key := ((z+2)*(t.SizeY+2)+(y+2))*(t.SizeX+4) + x + 2
	if v, ok := b.sky[key]; ok {
		return v
	}
	dirs := [5][2]float32{{0, 0}, {0.7, 0}, {-0.7, 0}, {0, 0.7}, {0, -0.7}}
	weights := [5]float32{0.4, 0.15, 0.15, 0.15, 0.15}
	total := float32(0)
	for i, d := range dirs {
		px, pz := float32(x)+0.5, float32(z)+0.5
		open := true
		for k := 1; k <= foliageSkyReach; k++ {
			py := y + k
			if py >= t.SizeY {
				break
			}
			px += d[0]
			pz += d[1]
			ix, iz := int(math.Floor(float64(px))), int(math.Floor(float64(pz)))
			if ix < 0 || iz < 0 || ix >= t.SizeX || iz >= t.SizeZ {
				break
			}
			if t.Blocks[t.Index(ix, py, iz)] != 0 {
				open = false
				break
			}
		}
		if open {
			total += weights[i]
		}
	}
	b.sky[key] = total
	return total
}

func (b *foliageBuild) env(f *TerrainFace) foliageFaceEnv {
	t := b.t
	x, y, z, d := t.FaceBlock(f.Key)
	st := TerrainDirs[d]
	ax, ay, az := x+st[0], y+st[1], z+st[2]
	var e foliageFaceEnv
	e.sky = b.skyAt(ax, ay, az)

	walls := 0
	for dz := -1; dz <= 1; dz++ {
		for dx := -1; dx <= 1; dx++ {
			if dx == 0 && dz == 0 {
				continue
			}
			for dy := 0; dy <= 1; dy++ {
				if t.Solid(ax+dx, ay+dy, az+dz) {
					walls++
				}
			}
		}
	}
	e.wallNear = float32(walls) / 16
	if d == FaceYPos {
		drops := 0
		for dz := -1; dz <= 1; dz++ {
			for dx := -1; dx <= 1; dx++ {
				if (dx != 0 || dz != 0) && !t.Solid(x+dx, y, z+dz) && !t.Solid(x+dx, y-1, z+dz) {
					drops++
				}
			}
		}
		e.cliffEdge = float32(drops) / 8
	}
	low := 1 - (float32(ay)-b.topLo)/max(b.topSpan, 1)
	e.moist = min(max(low*0.75+e.wallNear*0.5, 0), 1)

	// Biome coverage of the in-plane neighbours. A neighbour's face is looked
	// for level with this one, then a step behind it (the ground stepping
	// down) and a step out (stepping up).
	slot := t.Paint[f.Key]
	axes := terrainFaceAxes[d]
	for i := -1; i <= 1; i++ {
		for j := -1; j <= 1; j++ {
			if i == 0 && j == 0 {
				e.same[1][1] = 1
				continue
			}
			var p [3]int
			p[0], p[1], p[2] = x, y, z
			p[axes[1]] += i
			p[axes[2]] += j
			v := float32(1)
			for _, k := range [3]int{0, -1, 1} {
				q := p
				q[axes[0]] += k * faceSign(d)
				if !t.In(q[0], q[1], q[2]) {
					continue
				}
				nk := t.FaceKey(q[0], q[1], q[2], d)
				if !t.FaceExists(nk) {
					continue
				}
				if t.Paint[nk] == slot {
					v = 1
				} else {
					v = 0
				}
				break
			}
			e.same[i+1][j+1] = v
		}
	}
	return e
}

func faceSign(d int) int {
	if d%2 == 0 {
		return 1
	}
	return -1
}

// coverageAt is the tent-filtered neighbour coverage at (u, v) in the face.
func (e *foliageFaceEnv) coverageAt(u, v float32) float32 {
	su, sv := u-0.5, v-0.5
	sum := float32(0)
	for i := -1; i <= 1; i++ {
		wu := max(0, 1-absf(float32(i)-su))
		for j := -1; j <= 1; j++ {
			wv := max(0, 1-absf(float32(j)-sv))
			sum += e.same[i+1][j+1] * wu * wv
		}
	}
	return sum
}

func absf(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// band is 1 inside [lo, hi] and fades to 0 over margin either side; an
// unbounded end (lo <= 0, hi >= top) does not fade.
func band(x, lo, hi, margin, top float32) float32 {
	f := float32(1)
	if lo > 0 {
		f *= SmoothStep(lo-margin, lo+margin, x)
	}
	if hi < top {
		f *= 1 - SmoothStep(hi-margin, hi+margin, x)
	}
	return f
}

func (b *foliageBuild) face(f *TerrainFace, out *FoliageSection) {
	t := b.t
	biome := t.BiomeAt(f.Key)
	if biome == nil {
		return
	}
	def := biome.Def()
	x, y, z, d := t.FaceBlock(f.Key)
	axes := terrainFaceAxes[d]
	blk := [3]int{x, y, z}
	plane := blk[axes[0]]
	if d%2 == 0 {
		plane++
	}
	bu, bv := float32(blk[axes[1]]), float32(blk[axes[2]])
	var env *foliageFaceEnv
	tint := biome.EffectiveTint()
	density := biome.EffectiveDensity()
	up := mgl32.Vec3{0, 1, 0}
	seed := t.FoliageSeed ^ HashInts(0x51ed, len(def.Name), int(def.Name[0]))

	for si := range def.Species {
		sp := &def.Species[si]
		amount := biome.Amount(sp.Shape)
		if amount <= 0 {
			continue
		}
		spacing := sp.Spacing / float32(math.Sqrt(float64(density*amount))) / t.BlockSize
		spacing = max(spacing, 0.08)
		sseed := HashInts(seed, si, d, plane)
		i0, i1 := int(math.Floor(float64(bu/spacing))), int(math.Floor(float64((bu+1)/spacing)))
		j0, j1 := int(math.Floor(float64(bv/spacing))), int(math.Floor(float64((bv+1)/spacing)))
		for i := i0; i <= i1; i++ {
			for j := j0; j <= j1; j++ {
				h := HashInts(sseed, i, j)
				cu := (float32(i) + Unit(h)) * spacing
				cv := (float32(j) + Unit(Hash32(h^0x68bc))) * spacing
				if cu < bu || cu >= bu+1 || cv < bv || cv >= bv+1 {
					continue
				}
				u, v := cu-bu, cv-bv
				if sp.TopEdge {
					if d == FaceYPos || d == FaceYNeg || t.Solid(x, y+1, z) {
						continue
					}
					// Slide to the face's top edge.
					dyU := f.Corners[1].Add(f.Corners[2]).Sub(f.Corners[0]).Sub(f.Corners[3]).Y()
					dyV := f.Corners[3].Add(f.Corners[2]).Sub(f.Corners[0]).Sub(f.Corners[1]).Y()
					if absf(dyU) > absf(dyV) {
						u = 0.03
						if dyU > 0 {
							u = 0.97
						}
					} else {
						v = 0.03
						if dyV > 0 {
							v = 0.97
						}
					}
				}
				p, n := f.PointAt(u, v)
				var surf FoliageSurface
				switch {
				case n.Y() > 0.45:
					surf = OnGround
				case n.Y() < -0.45:
					surf = OnCeiling
				default:
					surf = OnWall
				}
				if sp.Where&surf == 0 {
					continue
				}
				if env == nil {
					e := b.env(f)
					env = &e
				}
				suit := b.suitability(sp, env, surf, n, p, u, v, sseed)
				h2 := Hash32(h ^ 0x2f1c3b)
				if suit <= 0 || Unit(h2) >= suit {
					continue
				}
				if sp.Clearance > 0 && surf == OnGround {
					ok := true
					for k := 1; k <= sp.Clearance; k++ {
						if t.Solid(x, y+k, z) {
							ok = false
							break
						}
					}
					if !ok {
						continue
					}
				}
				b.emit(sp, si, f, p, n, surf, min(suit, 1), h, tint, seed, out, up)
			}
		}
	}
}

func (b *foliageBuild) suitability(sp *FoliageSpecies, e *foliageFaceEnv, surf FoliageSurface, n, p mgl32.Vec3, u, v float32, sseed uint32) float32 {
	s := float32(1)
	if surf == OnGround && (sp.Slope[0] > 0 || sp.Slope[1] > 0) {
		ang := float32(math.Acos(float64(min(max(n.Y(), -1), 1)))) * 180 / math.Pi
		s *= band(ang, sp.Slope[0], sp.Slope[1], 6, 90)
	}
	if sp.Light[1] > 0 {
		s *= band(e.sky, sp.Light[0], sp.Light[1], 0.12, 1)
	}
	if sp.Edge > 0 {
		s *= 1 - 0.6*min(sp.Edge, 1) + e.wallNear*2.5*sp.Edge
	} else if sp.Edge < 0 {
		s *= max(1+sp.Edge*e.cliffEdge*1.5, 0)
	}
	if sp.Moisture > 0 {
		s *= 1 - 0.7*min(sp.Moisture, 1) + e.moist*1.6*sp.Moisture
	} else if sp.Moisture < 0 {
		s *= max(1+sp.Moisture*e.moist*1.4, 0)
	}
	if sp.Cluster > 0 {
		cs := max(sp.ClusterSize, 0.5)
		nv := FBM3(p.Mul(1/cs), sseed^0xc1a5, 3)
		thr := sp.Cluster * 0.7
		s *= SmoothStep(thr-0.08, thr+0.08, nv)
	}
	// Fray the biome's edge.
	jit := ValueNoise3(p.Mul(1.3), sseed^0xed6e) - 0.5
	s *= SmoothStep(0.35, 0.9, e.coverageAt(u, v)+jit*0.6)
	return s
}

func (b *foliageBuild) emit(sp *FoliageSpecies, si int, f *TerrainFace, p, n mgl32.Vec3, surf FoliageSurface, suit float32, h uint32, tint mgl32.Vec3, seed uint32, out *FoliageSection, up mgl32.Vec3) {
	r := func(k uint32) float32 { return Unit(Hash32(h ^ k)) }
	variant := 0
	nv := FoliageVariants[sp.Shape]
	allowed := make([]int, 0, nv)
	for k := 0; k < nv; k++ {
		if sp.Variants == 0 || sp.Variants&(1<<uint(k)) != 0 {
			allowed = append(allowed, k)
		}
	}
	if len(allowed) > 0 {
		pick := r(0x7a11)
		if sp.Shape == FoliageFlower {
			// A drift of flowers is one kind.
			pick = ValueNoise3(p.Mul(1/max(sp.ClusterSize, 1)), seed^uint32(si)^0x4f10)
			if r(0x57a7) < 0.1 {
				pick = r(0x7a11)
			}
		}
		variant = allowed[min(int(pick*float32(len(allowed))), len(allowed)-1)]
	}

	scale := lerp32(sp.Scale[0], sp.Scale[1], r(0x5ca1)) * (0.8 + 0.2*suit)
	var axis mgl32.Vec3
	switch {
	case sp.Shape == FoliageVine:
		axis = mgl32.Vec3{0, -1, 0}
	default:
		axis = n.Mul(1 - sp.Upright).Add(up.Mul(sp.Upright))
		if surf == OnCeiling && sp.Upright > 0 {
			axis = n
		}
		if axis.Len() < 1e-4 {
			axis = n
		}
		axis = axis.Normalize()
	}
	pos := p
	if sp.Sink > 0 {
		pos = pos.Sub(n.Mul(sp.Sink * scale * 0.5))
	}
	if sp.Shape == FoliageVine && surf == OnWall {
		pos = pos.Add(n.Mul(0.04))
	}

	col := sp.Colors[0].Add(sp.Colors[1].Sub(sp.Colors[0]).Mul(r(0xc010)))
	if sp.ColorNoise > 0 {
		dry := (FBM3(p.Mul(1.0/18), seed^0xd12e, 3) - 0.5) * 2 * sp.ColorNoise * 2
		if dry > 0 {
			dry = min(dry, 1)
			col = col.Add(mgl32.Vec3{col[0] * 0.4, col[1] * 0.1, -col[2] * 0.35}.Mul(dry))
		} else {
			dry = min(-dry, 1)
			col = col.Add(mgl32.Vec3{-col[0] * 0.2, -col[1] * 0.05, -col[2] * 0.15}.Mul(dry))
		}
	}
	col = mgl32.Vec3{col[0] * tint[0], col[1] * tint[1], col[2] * tint[2]}
	var accent mgl32.Vec3
	if len(sp.Accents) > 0 {
		pick := ValueNoise3(p.Mul(0.5/max(sp.ClusterSize, 1)), seed^uint32(si)^0xacc3)
		if r(0x57a8) < 0.08 {
			pick = r(0xacc4)
		}
		accent = sp.Accents[min(int(pick*float32(len(sp.Accents))), len(sp.Accents)-1)]
		accent = accent.Mul(0.9 + 0.2*r(0xacc5))
	} else {
		accent = col
	}

	key := [2]int{int(sp.Shape), variant}
	bt := b.byKey[key]
	if bt == nil {
		bt = &FoliageBatch{Shape: sp.Shape, Variant: variant}
		b.byKey[key] = bt
	}
	bt.Instances = append(bt.Instances, FoliageInstance{
		Pos: pos, Up: axis, Yaw: r(0x9a3) * 2 * math.Pi, Scale: scale,
		Color: col, Accent: accent, Rank: r(0x4a4c),
	})
	ext := FoliageExtent[sp.Shape]
	reach := max(ext[0], ext[1]) * scale
	for k := 0; k < 3; k++ {
		out.Lo[k] = min(out.Lo[k], pos[k]-reach)
		out.Hi[k] = max(out.Hi[k], pos[k]+reach)
	}
}
