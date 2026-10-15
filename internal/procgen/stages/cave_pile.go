package stages

// cave_pile.go — the cave's rock piles (docs/adventure/PHASE_E.md, "Phase
// E2"): the reference's "Nanite rock pile", a chamber floor heaped with
// broken rock at every scale — a few huge fallen slabs, blocks leaning on
// them and on each other, chips filling round their feet, sand only in the
// gaps — rather than single rocks sunk into a flat sheet of sand.
//
// Piles stand in the chambers only (where the cave is at least
// pile_min_width wide), off the clear lane down the middle. Each pile is a
// core (a slab from pile_slab_rocks, or the biggest block), a ring of
// blocks round it (pile_rocks) and a skirt of chips (pile_chip_rocks), the
// sizes falling off from the core outward. Every piece rests on what is
// already under it: the floor (the void's own fallen blocks included) or
// the pieces placed before it, each a dome over its footprint — so the
// pile stacks up rather than sinking every piece into the sand.
//
// All of it is Added and off (piles 0), so an existing document's output
// does not move.

import (
	"fmt"
	"math"

	"foundry/internal/procgen"
	"foundry/internal/procgen/cave"
	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/rocks"
	"foundry/internal/procgen/sdf"
)

// cavePileParams are the cave stage's pile parameters (all Added).
func cavePileParams() []procgen.ParamDef {
	f, v2, i := procgen.Float, procgen.Vec2, procgen.Int
	ref := func(name, label, doc string) procgen.ParamDef {
		return procgen.ParamDef{Name: name, Added: true, Type: procgen.ParamStage, StageKinds: RockSources, Default: procgen.StageRef(""),
			Label: label, Doc: doc}
	}
	return []procgen.ParamDef{
		{Name: "piles", Added: true, Type: procgen.ParamFloat, Label: "Rock piles (per 100 m²)", Default: f(0), Min: 0, Max: 20, Step: 0.05,
			Doc: "Heaps of broken rock in the chambers: a slab or big block, blocks stacked round it, chips at their feet. 0: none."},
		{Name: "pile_min_width", Added: true, Type: procgen.ParamFloat, Label: "Pile min width (m)", Default: f(12), Min: 0, Max: 100, Step: 0.5,
			Doc: "Piles stand only where the cave is at least this wide (the chambers, not the passages)."},
		{Name: "pile_size", Added: true, Type: procgen.ParamVec2, Label: "Pile radius (m)", Default: v2(2, 4.5), Min: 0.3, Max: 30, Step: 0.1,
			Doc: "A pile's reach from its core."},
		ref("pile_rocks", "Pile blocks", "The rock set the blocks come from (empty: the floor's rocks)."),
		ref("pile_chip_rocks", "Pile chips", "The rock set the chips come from (empty: the blocks')."),
		ref("pile_slab_rocks", "Pile slabs", "The rock set the huge fallen slabs come from (empty: the blocks')."),
		{Name: "pile_blocks", Added: true, Type: procgen.ParamFloat, Label: "Blocks per m²", Default: f(0.5), Min: 0, Max: 10, Step: 0.01,
			Doc: "Blocks per square metre of a pile."},
		{Name: "pile_chips", Added: true, Type: procgen.ParamFloat, Label: "Chips per m²", Default: f(3), Min: 0, Max: 50, Step: 0.1,
			Doc: "Chips per square metre of a pile (and its skirt)."},
		{Name: "pile_block_size", Added: true, Type: procgen.ParamVec2, Label: "Block size (m)", Default: v2(0.35, 1.4), Min: 0.02, Max: 20, Step: 0.01},
		{Name: "pile_chip_size", Added: true, Type: procgen.ParamVec2, Label: "Chip size (m)", Default: v2(0.06, 0.3), Min: 0.01, Max: 5, Step: 0.01},
		{Name: "pile_slab_size", Added: true, Type: procgen.ParamVec2, Label: "Slab size (m)", Default: v2(2.5, 5), Min: 0.1, Max: 30, Step: 0.05},
		{Name: "pile_slab_chance", Added: true, Type: procgen.ParamFloat, Label: "Slab chance", Default: f(0.4), Min: 0, Max: 1, Step: 0.01,
			Doc: "The share of piles whose core is a huge fallen slab."},
		{Name: "pile_sink", Added: true, Type: procgen.ParamFloat, Label: "Pile sink", Default: f(0.08), Min: 0, Max: 0.8, Step: 0.01,
			Doc: "How far each piece settles into what it rests on, as a share of its height."},
		{Name: "pile_tilt", Added: true, Type: procgen.ParamFloat, Label: "Pile tilt (rad)", Default: f(0.45), Min: 0, Max: 1.6, Step: 0.01,
			Doc: "How far blocks and slabs lean (chips lie any way up)."},
		{Name: "pile_max_triangles", Added: true, Type: procgen.ParamInt, Label: "Pile max triangles", Default: i(0), Min: 0, Max: 10000000,
			Doc: "The blocks' and chips' variants of at most this many triangles (the light copies). 0: all."},
	}
}

// pileDome is a placed piece as what the next one rests on: a dome of
// height h over a disc of radius r at (x, z), its foot at y.
type pileDome struct{ x, z, y, r, h float64 }

func (d pileDome) at(x, z float64) float64 {
	q := math.Hypot(x-d.x, z-d.z) / d.r
	if q >= 1 {
		return math.Inf(-1)
	}
	return d.y + d.h*math.Sqrt(1-q*q)
}

// pileSet is one rock set the piles draw on.
type pileSet struct {
	art   *RocksArtifact
	vs    []*rocks.VariantMeta
	shade func(float64) (string, error)
}

// cavePiles heaps the piles. Its own RNG: the floor's rocks do not move.
func cavePiles(rc *procgen.RunContext, v *cave.Void, sky *cave.Sky, chunkAt func(x, y, z float64) string) error {
	p := rc.Params
	density := p.Float("piles")
	if density <= 0 {
		return nil
	}
	maxTri := p.Int("pile_max_triangles")
	sets := map[string]*pileSet{}
	load := func(id string, light bool) (*pileSet, error) {
		key := fmt.Sprintf("%s/%v", id, light)
		if s := sets[key]; s != nil {
			return s, nil
		}
		ra, err := rc.UpstreamArtifact(id)
		if err != nil {
			return nil, err
		}
		art, ok := ra.(*RocksArtifact)
		if !ok || art.Manifest == nil || len(art.Manifest.Variants) == 0 {
			return nil, fmt.Errorf("stage %s has no rock variants", id)
		}
		s := &pileSet{art: art}
		least := 0
		for k := range art.Manifest.Variants {
			vm := &art.Manifest.Variants[k]
			if vm.Triangles < art.Manifest.Variants[least].Triangles {
				least = k
			}
			if !light || maxTri <= 0 || vm.Triangles <= maxTri {
				s.vs = append(s.vs, vm)
			}
		}
		if len(s.vs) == 0 {
			s.vs = []*rocks.VariantMeta{&art.Manifest.Variants[least]}
		}
		prefix := "rock_shade"
		if id != p.Str("rocks") {
			prefix = "pile_" + id + "_shade"
		}
		// One shader per stage, shared by its light and full sets.
		for k2, o := range sets {
			if o.art == art && k2 != key {
				s.shade = o.shade
			}
		}
		if s.shade == nil {
			s.shade = caveRockShader(rc, art, prefix)
		}
		sets[key] = s
		return s, nil
	}
	or := func(id, def string) string {
		if id == "" {
			return def
		}
		return id
	}
	blockID := or(p.Str("pile_rocks"), p.Str("rocks"))
	if blockID == "" {
		return fmt.Errorf("piles need a rock set: set rocks or pile_rocks")
	}
	blocks, err := load(blockID, true)
	if err != nil {
		return err
	}
	chips, err := load(or(p.Str("pile_chip_rocks"), blockID), true)
	if err != nil {
		return err
	}
	slabs, err := load(or(p.Str("pile_slab_rocks"), blockID), false)
	if err != nil {
		return err
	}

	rng := noise.NewRNG(noise.Hash64(rc.Seed, 0x5049_4C45))
	minW := p.Float("pile_min_width")
	clear := p.Float("clear")
	psz, bsz, csz, ssz := p.Vec2("pile_size"), p.Vec2("pile_block_size"), p.Vec2("pile_chip_size"), p.Vec2("pile_slab_size")
	sink, tilt := p.Float("pile_sink"), p.Float("pile_tilt")
	slabChance := p.Float("pile_slab_chance")
	nBlocks, nChips := p.Float("pile_blocks"), p.Float("pile_chips")
	draw, shadow := float32(p.Float("draw_distance")/5), float32(p.Float("shadow_distance")/5)
	darkness, gain := p.Float("darkness"), p.Float("sky_gain")

	var domes []pileDome
	// floorAt is the floor (the void's blocks included) under (x, z) near
	// height y, NaN where there is none.
	floorAt := func(x, y, z, hh float64) float64 {
		// Only from the air down: a start inside the rock is a wall.
		if v.At(sdf.Vec3{x, y + hh*0.35, z}) >= 0 {
			return math.NaN()
		}
		for yy := y + hh*0.35; yy > y-1.5; yy -= 0.04 {
			if v.At(sdf.Vec3{x, yy, z}) >= 0 {
				return yy
			}
		}
		return math.NaN()
	}
	support := func(x, z, base, r float64) float64 {
		top := base
		for k := -1; k < 8; k++ {
			sx, sz := x, z
			if k >= 0 {
				a := float64(k) * math.Pi / 4
				sx, sz = x+0.6*r*math.Cos(a), z+0.6*r*math.Sin(a)
			}
			w := 1.0
			if k >= 0 {
				w = 0.5 // the rim points hold it up less than its centre (it tips)
			}
			for _, d := range domes {
				if h := d.at(sx, sz); !math.IsInf(h, -1) {
					top = math.Max(top, base+(h-base)*w)
				}
			}
		}
		return top
	}
	placed := 0
	place := func(set *pileSet, x, z, floorY, size float64, tiltMax float64, anyWay bool) error {
		vm := set.vs[rng.Intn(len(set.vs))]
		ext := math.Max(float64(vm.Extent[0]), math.Max(float64(vm.Extent[2]), float64(vm.Extent[1])))
		if ext <= 0 {
			return nil
		}
		sc := size / ext
		r := 0.5 * math.Max(float64(vm.Extent[0]), float64(vm.Extent[2])) * sc
		h := float64(vm.Height) * sc
		// What the next piece rests on is the upper surface (Top: the
		// up-facing vertices' 90th percentile), not the bounding height — a
		// leaning plate's top edge holds nothing up.
		top := float64(vm.Top) * sc
		if top <= 0 || top > h {
			top = h
		}
		y := support(x, z, floorY, r) - sink*h
		rot := procgen.Point3{X: float32(rng.Range(-tiltMax, tiltMax)), Y: float32(rng.Range(0, 2*math.Pi)), Z: float32(rng.Range(-tiltMax, tiltMax))}
		if anyWay {
			rot.X, rot.Z = float32(rng.Range(-math.Pi, math.Pi)), float32(rng.Range(-math.Pi/2, math.Pi/2))
		}
		collision := "none"
		if size > 0.6 {
			collision = "hulls"
		}
		mat, err := set.shade(math.Max(caveShade(sky, sdf.Vec3{x, y + 0.4, z}, darkness, gain), 0.2))
		if err != nil {
			return err
		}
		dd, sd := draw, shadow
		if size < 0.35 {
			dd, sd = draw*0.4, shadow*0.4
		}
		scale := float32(sc)
		rc.Place(procgen.Placement{ID: fmt.Sprintf("pile_%d", placed), Model: set.art.ModelPath(vm),
			Position: procgen.Point3{X: float32(x), Y: float32(y), Z: float32(z)}, Rotation: rot,
			Scale: &procgen.Point3{X: scale, Y: scale, Z: scale}, Collision: collision, Chunk: chunkAt(x, y, z),
			Material: mat, DrawDistance: dd, ShadowDistance: sd})
		if anyWay {
			top = math.Min(top, r) // a chip on its side stands no taller than it is wide
		}
		domes = append(domes, pileDome{x: x, z: z, y: y, r: r * 0.8, h: top * (1 - sink) * 0.9})
		placed++
		return nil
	}

	dense := v.Dense()
	piles := 0
	for k := 0; k+1 < len(dense); k++ {
		a, b := dense[k], dense[k+1]
		w := 0.5 * (a[3] + b[3])
		if w < minW {
			continue
		}
		segLen := math.Hypot(b[0]-a[0], b[2]-a[2])
		want := segLen * w * density / 100
		cnt := int(want)
		if rng.Float64() < want-float64(cnt) {
			cnt++
		}
		dx, dz := b[0]-a[0], b[2]-a[2]
		l := math.Hypot(dx, dz)
		if l < 1e-9 {
			continue
		}
		sx, sz := -dz/l, dx/l
		for c := 0; c < cnt; c++ {
			t := rng.Float64()
			x, y, z := a[0]+dx*t, a[1]+(b[1]-a[1])*t, a[2]+dz*t
			hh := a[4] + (b[4]-a[4])*t
			R := rng.Range(psz[0], psz[1])
			// Off the lane: toward a wall, the pile's foot short of it.
			side := 1.0
			if rng.Bool() {
				side = -1
			}
			lane := 0.0
			if clear > 0 {
				lane = 0.3 + 0.15*clear
			}
			umax := math.Max(1-(R*0.6)/(w/2), lane+0.05)
			u := side * rng.Range(math.Min(lane+R/(w/2)*0.6, umax), umax)
			cx, cz := x+sx*u*w/2, z+sz*u*w/2
			laneOf := func(px, pz float64) bool {
				return clear > 0 && math.Abs((px-x)*sx+(pz-z)*sz) < lane*w/2
			}
			fy := floorAt(cx, y, cz, hh)
			if math.IsNaN(fy) || fy > y+hh*0.3 {
				continue
			}
			if l, ok := v.Local(sdf.Vec3{cx, fy + 0.1, cz}); !ok || l.End > 0 {
				continue
			}
			piles++
			// The core.
			if rng.Chance(slabChance) {
				if err := place(slabs, cx, cz, fy, rng.Range(ssz[0], ssz[1]), tilt*1.3, false); err != nil {
					return err
				}
			} else if err := place(blocks, cx, cz, fy, bsz[1]*rng.Range(1.1, 1.6), tilt, false); err != nil {
				return err
			}
			// Blocks round it, bigger near the core.
			nb := int(math.Round(nBlocks * math.Pi * R * R * rng.Range(0.7, 1.3)))
			for i := 0; i < nb; i++ {
				q := math.Sqrt(rng.Float64())
				ang := rng.Range(0, 2*math.Pi)
				px, pz := cx+R*q*math.Cos(ang), cz+R*q*math.Sin(ang)
				if laneOf(px, pz) {
					continue
				}
				f := floorAt(px, fy, pz, 3)
				if math.IsNaN(f) {
					continue
				}
				s := bsz[0] + (bsz[1]-bsz[0])*math.Pow(rng.Float64(), 1.6)*(1.15-0.6*q)
				if err := place(blocks, px, pz, f, s, tilt, false); err != nil {
					return err
				}
			}
			// Chips: through the pile and in a skirt round it, most at the
			// feet of the blocks.
			nc := int(math.Round(nChips * math.Pi * sq3(R*1.35) * rng.Range(0.7, 1.3)))
			for i := 0; i < nc; i++ {
				q := math.Pow(rng.Float64(), 0.35) * 1.35
				ang := rng.Range(0, 2*math.Pi)
				px, pz := cx+R*q*math.Cos(ang), cz+R*q*math.Sin(ang)
				f := floorAt(px, fy, pz, 3)
				if math.IsNaN(f) {
					continue
				}
				s := csz[0] + (csz[1]-csz[0])*math.Pow(rng.Float64(), 2)
				if laneOf(px, pz) {
					s = math.Min(s, cave.StepHeight)
				}
				if err := place(chips, px, pz, f, s, 0, true); err != nil {
					return err
				}
			}
		}
	}
	rc.Logf("%d rock piles, %d pieces", piles, placed)
	return nil
}
