package stages

// ground_edit_river.go — ground_edit's rivers (docs/terrain-paint/
// PHASE_W3.md): the specs from the hand rivers and the drainage trace, the
// carve (terrain.CarveRivers), the masks and terrain.yaml section they
// leave, rivers.yaml, and the boulders. The ribbon meshes are
// ground_edit_river_mesh.go; the parameters and why rivers live in this
// stage ground_edit_river_params.go.
//
// ORDER IN THE RUN. After the height layers and the basins (so a river
// sees the ponds it runs into and the ground the author shaped), before
// the quantisation — so the carve, like every other edit, lands on the
// heightmap's 16 bits and the moved tiles are recooked by the one tile
// pass.
//
// THE SPECS. Each `rivers` item with two or more points is a spec: its
// points (x, z, width, depth), its shape and a seed of the stage's seed and
// its index. With `drainage` on, the ground as the edits so far left it —
// with the hand rivers cut into a copy, so the drainage runs into their
// channels rather than beside them — is traced (terrain.TraceDrainage, on
// the flow mask's scale), trimmed where a hand river runs
// (terrain.TrimAgainst: an adopted river replaces its traced one; a traced
// tributary then ends on the hand river), and each
// traced river is a spec with the default shape but a mild meander and the
// drainage boulders. Names are unique: an item's own name, or river_<n>;
// traced ones drainage_<n>.
//
// WHAT IT WRITES (beside what ground_edit always writes):
//
//   - the water mask covers the rivers' wet samples (their depth under the
//     sloping surface), so wet ground, the reeds' shore band and every grass
//     rule's max_depth follow rivers as they follow ponds;
//   - the FLOW mask is rewritten: the channel's share pushes it up to 0.9,
//     so the material's wash rule and the placement stages' flow bands read
//     a river as running water;
//   - the river masks (terrain/river_meta.go): the flow map (current along X
//     and along Z), the foam, the bed and the point bars;
//   - terrain.yaml `rivers:` — every river, waterfall and confluence;
//   - rivers.yaml — the same for the editor's rivers panel, and every
//     traced river's points, width and depth, which Adopt copies into a
//     hand river.
//
// BOULDERS. With `river_rocks` naming a rocks stage, each boulder the carve
// dropped in a riffle (terrain.Boulder: it already threw foam) is placed as
// one of that stage's variants, scaled to the boulder's size, turned by a
// hash, sunk a third of its height into the bed: boulder_<river>_<n>.

import (
	"fmt"
	"math"
	"os"
	"sort"

	"foundry/internal/procgen"
	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/place"
	"foundry/internal/procgen/terrain"

	"gopkg.in/yaml.v2"
)

// RiversFile is ground_edit's table of rivers.
const RiversFile = "rivers.yaml"

// groundRivers is what the rivers carved.
type groundRivers struct {
	res *terrain.RiverResult
	// mats and entries are per carved river: its water material and the
	// `rivers` item it came from (-1: traced from drainage).
	mats    []string
	entries []int
	// traced is every drainage river as traced (after the trim).
	traced []terrain.TracedRiver
	// obstacles is the boulders placed (ground_obstacles.go).
	obstacles [][3]float64
}

func (r *groundRivers) any() bool { return r != nil && r.res != nil && len(r.res.Rivers) > 0 }

// carveGroundRivers builds the specs and carves them into g (file comment).
func carveGroundRivers(rc *procgen.RunContext, g *terrain.Grid, src *terrain.Terrain, ws *groundWater) (*groundRivers, error) {
	p := rc.Params
	out := &groundRivers{}
	var specs []terrain.RiverSpec
	var mats []string
	var entries []int
	seen := map[string]bool{}
	unique := func(name string) string {
		base, n := name, 2
		for seen[name] {
			name = fmt.Sprintf("%s_%d", base, n)
			n++
		}
		seen[name] = true
		return name
	}
	for i, it := range p.Items("rivers") {
		pts := it.Points("path")
		if len(pts) < 2 {
			continue
		}
		name := it.Str("name")
		if name == "" {
			name = fmt.Sprintf("river_%d", i+1)
		}
		sp := terrain.RiverSpec{Name: unique(safeName(name, "river")), Shape: riverShape(it), Seed: noise.Hash64(rc.Seed, 0x72697672, uint64(i))}
		for _, q := range pts {
			sp.Points = append(sp.Points, [2]float64{q[0], q[1]})
			w, d := 4.0, 1.0
			if len(q) > 2 {
				w = q[2]
			}
			if len(q) > 3 {
				d = q[3]
			}
			sp.Width = append(sp.Width, w)
			sp.Depth = append(sp.Depth, d)
		}
		mat := it.Str("material")
		if mat == "" {
			mat = defaultRiverMaterial
		}
		specs = append(specs, sp)
		mats = append(mats, mat)
		entries = append(entries, i)
	}
	if p.Bool("drainage") {
		dw, dd := p.Vec2("drainage_width"), p.Vec2("drainage_depth")
		set := terrain.DrainageSettings{Threshold: p.Float("drainage_threshold"), MinLength: p.Float("drainage_min_length"),
			WidthMin: dw[0], WidthMax: dw[1], DepthMin: dd[0], DepthMax: dd[1], MaxRivers: p.Int("drainage_max")}
		// Traced on the ground with the hand rivers already cut (a copy):
		// the drainage then runs into their channels instead of beside
		// them, and the trim takes what is left of it inside them.
		tg := g
		if len(specs) > 0 {
			tg = g.Clone()
			var water *terrain.WaterResult
			if ws.wet() {
				water = ws.water
			}
			terrain.CarveRivers(tg, specs, water, float64(src.Meta.Tiles.Base)+0.25)
		}
		traced := terrain.TraceDrainage(tg, set)
		traced = terrain.TrimAgainst(traced, specs, set.MinLength)
		out.traced = traced
		mat := p.Str("drainage_material")
		if mat == "" {
			mat = defaultRiverMaterial
		}
		for i, tr := range traced {
			sh := terrain.DefaultRiverShape()
			sh.Meander = p.Float("drainage_meander")
			sh.MeanderLength = math.Max(8*mean(tr.Width), 12)
			sh.Boulders = p.Float("drainage_boulders")
			specs = append(specs, terrain.RiverSpec{Name: unique(tr.Name), Points: tr.Points, Width: tr.Width, Depth: tr.Depth,
				Drainage: true, Shape: sh, Seed: noise.Hash64(rc.Seed, 0x64726e, uint64(i))})
			mats = append(mats, mat)
			entries = append(entries, -1)
		}
		rc.Logf("drainage: %d rivers traced above flow %.2f", len(traced), set.Threshold)
	}
	if len(specs) == 0 {
		return out, nil
	}
	var water *terrain.WaterResult
	if ws.wet() {
		water = ws.water
	}
	res := terrain.CarveRivers(g, specs, water, float64(src.Meta.Tiles.Base)+0.25)
	out.res = res
	for _, r := range res.Rivers {
		out.mats = append(out.mats, mats[r.Spec])
		out.entries = append(out.entries, entries[r.Spec])
	}
	wet := 0
	for _, s := range res.Samples {
		if s.Wet {
			wet++
		}
	}
	rc.Logf("rivers: %d carved (%d samples moved, %d wet), %d waterfalls, %d confluences, %d boulders", len(res.Rivers),
		res.Changed, wet, len(res.Falls), len(res.Confluences), len(res.Boulders))
	return out, nil
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

// riverMasks is the river masks over the grid (terrain/river_meta.go),
// quantised as their files hold them.
func riverMasks(g *terrain.Grid, rv *groundRivers) map[string][]float32 {
	n := len(g.H)
	out := map[string][]float32{}
	for _, name := range terrain.RiverMaskNames {
		out[name] = make([]float32, n)
	}
	fx, fz := out[terrain.MaskRiverFlowX], out[terrain.MaskRiverFlowZ]
	for k := range fx {
		fx[k], fz[k] = 0.5, 0.5
	}
	for k, s := range rv.res.Samples {
		if s.Surface == s.Surface {
			fx[k] = 0.5 + 0.5*s.FlowX/terrain.RiverFlowMax
			fz[k] = 0.5 + 0.5*s.FlowZ/terrain.RiverFlowMax
			out[terrain.MaskRiverFoam][k] = s.Foam
		}
		out[terrain.MaskRiverBed][k] = s.Bed
		out[terrain.MaskRiverBar][k] = s.Bar
	}
	for _, m := range out {
		terrain.QuantizeUnit(m)
	}
	return out
}

// riversMeta is terrain.yaml's rivers section.
func riversMeta(rv *groundRivers) *terrain.RiversMeta {
	if !rv.any() {
		return nil
	}
	m := &terrain.RiversMeta{}
	for i, r := range rv.res.Rivers {
		m.Rivers = append(m.Rivers, terrain.RiverMeta{Name: r.Name, Drainage: r.Drainage, Material: rv.mats[i], Length: r.Length,
			Source: r.Source, Mouth: r.Mouth, Into: r.Into})
	}
	for _, f := range rv.res.Falls {
		m.Falls = append(m.Falls, terrain.WaterfallMeta{River: f.River, At: [2]float64{f.X, f.Z}, Lip: f.Lip, Foot: f.Foot,
			Dir: [2]float64{f.DirX, f.DirZ}, Width: f.Width})
	}
	for _, c := range rv.res.Confluences {
		m.Confluences = append(m.Confluences, terrain.ConfluenceMeta{Main: c.Main, Tributary: c.Tributary, At: [2]float64{c.X, c.Z},
			Level: c.Level})
	}
	return m
}

// GroundRiver is one carved river as rivers.yaml lists it.
type GroundRiver struct {
	Name string `yaml:"name"`
	// Kind is painted, adopted or drainage; Entry the `rivers` item (-1
	// for a traced river).
	Kind     string  `yaml:"kind"`
	Entry    int     `yaml:"entry"`
	Material string  `yaml:"material"`
	Length   float64 `yaml:"length"`
	Source   float64 `yaml:"source"`
	Mouth    float64 `yaml:"mouth"`
	Into     string  `yaml:"into,omitempty"`
	Reversed bool    `yaml:"reversed,omitempty"`
	// Width is the narrowest and widest it runs.
	Width [2]float64 `yaml:"width,flow"`
}

// Drop is how far its surface falls, source to mouth.
func (r GroundRiver) Drop() float64 { return r.Source - r.Mouth }

// AdoptedRiver is the `rivers` item Adopt makes of a traced river: its
// points with their widths and depths, marked adopted, no meander (the
// trace's own wander is in the points already), wearing mat when set.
func AdoptedRiver(tr *GroundTraced, mat string) procgen.Params {
	var pts [][]float64
	for i, p := range tr.Points {
		w, d := 4.0, 1.0
		if i < len(tr.Width) {
			w = tr.Width[i]
		}
		if i < len(tr.Depth) {
			d = tr.Depth[i]
		}
		pts = append(pts, []float64{p[0], p[1], w, d})
	}
	it := procgen.Params{"name": procgen.String(tr.Name + "_adopted"), "from": procgen.Enum("adopted"), "path": procgen.Spline(pts...)}
	if mat != "" {
		it["material"] = procgen.String(mat)
	}
	return it
}

// GroundTraced is one traced drainage river (what Adopt copies).
type GroundTraced struct {
	Name   string       `yaml:"name"`
	Points [][2]float64 `yaml:"points,flow"`
	Width  []float64    `yaml:"width,flow"`
	Depth  []float64    `yaml:"depth,flow"`
}

// GroundRivers is rivers.yaml.
type GroundRivers struct {
	Rivers      []GroundRiver            `yaml:"rivers"`
	Traced      []GroundTraced           `yaml:"traced,omitempty"`
	Falls       []terrain.WaterfallMeta  `yaml:"falls,omitempty"`
	Confluences []terrain.ConfluenceMeta `yaml:"confluences,omitempty"`
	Boulders    int                      `yaml:"boulders"`
}

// writeRiversFile writes rivers.yaml (always, so the editor's panel reads
// an empty table rather than last run's when the rivers are removed).
func writeRiversFile(rc *procgen.RunContext, rv *groundRivers) error {
	f := GroundRivers{}
	if rv.any() {
		items := rc.Params.Items("rivers")
		for i, r := range rv.res.Rivers {
			kind := "drainage"
			if e := rv.entries[i]; e >= 0 {
				kind = items[e].Str("from")
			}
			lo, hi := math.Inf(1), math.Inf(-1)
			for _, s := range r.Stations {
				lo, hi = math.Min(lo, s.Width), math.Max(hi, s.Width)
			}
			f.Rivers = append(f.Rivers, GroundRiver{Name: r.Name, Kind: kind, Entry: rv.entries[i], Material: rv.mats[i],
				Length: r.Length, Source: r.Source, Mouth: r.Mouth, Into: r.Into, Reversed: r.Reversed,
				Width: [2]float64{round2(lo), round2(hi)}})
		}
		m := riversMeta(rv)
		f.Falls, f.Confluences = m.Falls, m.Confluences
		f.Boulders = len(rv.res.Boulders)
	}
	for _, t := range rv.traced {
		f.Traced = append(f.Traced, GroundTraced{Name: t.Name, Points: t.Points, Width: t.Width, Depth: t.Depth})
	}
	data, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	return rc.WriteFile(RiversFile, data)
}

// ReadGroundRivers reads a ground_edit stage's rivers.yaml.
func ReadGroundRivers(path string) (*GroundRivers, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f GroundRivers
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", RiversFile, err)
	}
	return &f, nil
}

// placeBoulders places the riffles' boulders as the river_rocks stage's
// variants (file comment).
func placeBoulders(rc *procgen.RunContext, rv *groundRivers) error {
	ref := rc.Params.Str("river_rocks")
	if ref == "" || !rv.any() || len(rv.res.Boulders) == 0 {
		return nil
	}
	vs, art, err := runVariants(rc, ref)
	if err != nil {
		return err
	}
	if len(vs) == 0 {
		return nil
	}
	mat := ""
	if art != nil && art.Manifest != nil {
		mat = art.Manifest.Material
	}
	counts := map[string]int{}
	bs := append([]terrain.Boulder(nil), rv.res.Boulders...)
	sort.SliceStable(bs, func(a, b int) bool { return bs[a].River < bs[b].River })
	var pls []procgen.Placement
	for _, b := range bs {
		h := noise.Hash64(rc.Seed, math.Float64bits(b.X), math.Float64bits(b.Z))
		v := vs[int(h%uint64(len(vs)))]
		ext := math.Max(math.Max(v.Extent[0], v.Extent[2]), 1e-3)
		sc := b.Size / ext
		counts[b.River]++
		in := place.Instance{Pos: [3]float64{b.X, b.Y - 0.33*v.Height*sc, b.Z}, Rot: place.RotY(float64(h>>20%6283) / 1000),
			Scale: sc, Radius: b.Size / 2, Collision: "auto"}
		pl := placementOf(fmt.Sprintf("boulder_%s_%03d", b.River, counts[b.River]), &in, &v)
		pl.Material = mat
		pls = append(pls, pl)
		rv.obstacles = append(rv.obstacles, obstacleDisc(b.X, b.Z, b.Size/2))
	}
	rc.Place(pls...)
	rc.Logf("boulders: %d placed from %s", len(pls), ref)
	return nil
}
