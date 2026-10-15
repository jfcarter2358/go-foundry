package stages

// grass_field_run.go — the grass_field stage's run, Load and preview
// (grass_field.go has the schema and the design).
//
// RUN
//  1. The KIT. Every enabled rule's plants stage's variants, filtered by
//     `pieces` and `min_size`, each one a kind (a variant two rules share
//     is one kind; the first rule's wind, range and shadows win). A kind's
//     meshes come from the import's own glTF (phimport.LoadPlantParts —
//     the very mesh the import cooked, in the same frame): LOD 0 capped at
//     the rule's max_triangles, LODs 1 and 2 thinned to lod_ratios of it
//     (grassfield.Thin: whole blades kept and widened); a LOD that thins no
//     further than the one before is left out. Its texture is the piece's
//     albedo (the `_cov` cutout a thin_foliage import writes).
//  2. The FIELDS, one per terrain tile (the terrain's tile grid), side by
//     side: place.Grass over the tile's rectangle, every rule at once, with
//     the region's share, the standing water (no clump deeper than the
//     rule's max_depth; the shore band) and the avoid stage's footprints;
//     grassfield.BuildField sorts them into cells and ranks; the file is
//     grass_<i>_<j>.fgrass. A tile with no clump writes nothing.
//  3. One VOLUME per field (world/volume_grass.go) and grass.yaml, the
//     summary Load reads back.

import (
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"foundry/internal/grassfield"
	"foundry/internal/polyhaven/phimport"
	"foundry/internal/procgen"
	"foundry/internal/procgen/place"
	"foundry/internal/procgen/terrain"

	"gopkg.in/yaml.v2"
)

// GrassKitFile and GrassSummaryFile are the stage's fixed outputs.
const (
	GrassKitFile     = "grass" + grassfield.KitExt
	GrassSummaryFile = "grass.yaml"
)

// GrassFieldArtifact is the stage's product: its kit and fields.
type GrassFieldArtifact struct {
	Kit       string   `yaml:"kit"`
	Fields    []string `yaml:"fields"`
	Kinds     []string `yaml:"kinds"`
	Instances int      `yaml:"instances"`
	// PerRule is each rule's clump count, by name.
	PerRule map[string]int `yaml:"per_rule"`
	// Area is the ground the stage covered (m²), for the density log.
	Area float64 `yaml:"area_m2"`
}

func loadGrassField(res *procgen.StageResult) (any, error) {
	data, err := os.ReadFile(filepath.Join(res.Dir, GrassSummaryFile))
	if err != nil {
		return nil, err
	}
	a := &GrassFieldArtifact{}
	if err := yaml.Unmarshal(data, a); err != nil {
		return nil, fmt.Errorf("%s: %w", GrassSummaryFile, err)
	}
	return a, nil
}

// grassTerrain is the run's terrain stage's terrain.
func grassTerrain(rc *procgen.RunContext, id string) (*terrain.Terrain, error) {
	a, err := rc.UpstreamArtifact(id)
	if err != nil {
		return nil, err
	}
	t, ok := a.(*terrain.Terrain)
	if !ok {
		return nil, fmt.Errorf("stage %s is not a terrain stage", id)
	}
	return t, nil
}

// grassPieceFilter keeps the variants whose names hold one of words.
func grassPieceFilter(vs []place.Variant, words string) []place.Variant {
	ws := strings.Fields(words)
	if len(ws) == 0 {
		return vs
	}
	var out []place.Variant
	for _, v := range vs {
		for _, w := range ws {
			if strings.Contains(v.Name, w) {
				out = append(out, v)
				break
			}
		}
	}
	return out
}

// grassKitBuilder gathers the kit's kinds.
type grassKitBuilder struct {
	resources string
	ratios    [2]float64
	kit       grassfield.Kit
	index     map[string]int
	parts     map[string][]phimport.PlantPart
}

// partFor is the imported part a variant names.
func (b *grassKitBuilder) partFor(v place.Variant) (*phimport.PlantPart, error) {
	id := path.Base(path.Dir(v.Model))
	ps, ok := b.parts[id]
	if !ok {
		var err error
		if ps, err = phimport.LoadPlantParts(b.resources, id); err != nil {
			return nil, err
		}
		b.parts[id] = ps
	}
	name := strings.TrimSuffix(v.Name, "_lite")
	for i := range ps {
		if ps[i].Name == name {
			return &ps[i], nil
		}
	}
	if len(ps) == 1 {
		return &ps[0], nil
	}
	return nil, fmt.Errorf("%s: no part %s in the import", id, name)
}

// kind is the kit index of variant v, building it on first sight.
func (b *grassKitBuilder) kind(v place.Variant, it procgen.Params) (int, error) {
	if k, ok := b.index[v.Name]; ok {
		return k, nil
	}
	part, err := b.partFor(v)
	if err != nil {
		return 0, err
	}
	m := part.Mesh
	src := &grassfield.SourceMesh{Indices: m.Indices}
	for i, p := range m.Positions {
		src.Positions = append(src.Positions, [3]float32{p[0], p[1], p[2]})
		if i < len(m.Normals) {
			n := m.Normals[i]
			src.Normals = append(src.Normals, [3]float32{n[0], n[1], n[2]})
		}
		if i < len(m.UVs) {
			src.UVs = append(src.UVs, [2]float32{m.UVs[i][0], m.UVs[i][1]})
		}
	}
	tex := ""
	for _, s := range m.Slots {
		if s.Texture != "" {
			tex = s.Texture
			break
		}
	}
	if tex == "" {
		return 0, fmt.Errorf("%s has no albedo texture", v.Name)
	}
	seed := uint64(len(b.kit.Kinds)) + 1
	tris := len(src.Indices) / 3
	f0 := math.Min(1, float64(it.Int("max_triangles"))/float64(max(tris, 1)))
	lod0 := grassfield.Thin(src, f0, seed)
	h, r := lod0.Extent()
	bend, flutter := float32(it.Float("wind_bend")), float32(it.Float("wind_flutter"))
	kd := grassfield.Kind{Name: v.Name, Texture: tex, Height: h, Radius: r, Bend: bend, Flutter: flutter,
		Range: float32(it.Float("range"))}
	if it.Bool("shadows") {
		kd.Flags |= grassfield.KindCasts
	}
	kd.LODs = append(kd.LODs, grassfield.KindMesh(lod0, h, r, bend, flutter))
	last := len(lod0.Indices)
	for li, ratio := range b.ratios {
		if ratio <= 0 {
			continue
		}
		lm := grassfield.Thin(src, f0*ratio, seed+uint64(li+1)*0x9e37)
		if len(lm.Indices) >= last*9/10 {
			continue // no thinner than the LOD before it
		}
		kd.LODs = append(kd.LODs, grassfield.KindMesh(lm, h, r, bend, flutter))
		last = len(lm.Indices)
	}
	b.index[v.Name] = len(b.kit.Kinds)
	b.kit.Kinds = append(b.kit.Kinds, kd)
	return len(b.kit.Kinds) - 1, nil
}

// grassTileGrid is the terrain's tile grid: origin, tile side and count a
// side (128 m tiles over the ground when the terrain has none).
func grassTileGrid(t *terrain.Terrain, g *place.Ground) (x0, z0, size float64, nx, nz int) {
	b := g.Bounds()
	if ts := t.Meta.Tiles; ts.Size > 0 && ts.Count > 0 {
		return t.Meta.Bounds.MinX, t.Meta.Bounds.MinZ, ts.Size, ts.Count, ts.Count
	}
	size = 128
	return b[0], b[1], size, int(math.Ceil((b[2] - b[0]) / size)), int(math.Ceil((b[3] - b[1]) / size))
}

// grassWater is the terrain's standing water as place reads it: depth and
// distance at a point, bilinear; nil without a water mask.
func grassWater(t *terrain.Terrain, reach float64) func(x, z float64) (float64, float64) {
	m, err := t.Mask(terrain.MaskWater)
	if err != nil {
		return nil
	}
	dist, _ := waterProximity(t, reach)
	g := t.Grid
	s := g.S()
	sample := func(v []float32, x, z float64) float64 {
		fx := math.Min(math.Max((x-g.MinX)/g.Cell, 0), float64(s-1))
		fz := math.Min(math.Max((z-g.MinZ)/g.Cell, 0), float64(s-1))
		i, j := min(int(fx), s-2), min(int(fz), s-2)
		tx, tz := fx-float64(i), fz-float64(j)
		at := func(i, j int) float64 { return float64(v[j*s+i]) }
		a := at(i, j) + (at(i+1, j)-at(i, j))*tx
		c := at(i, j+1) + (at(i+1, j+1)-at(i, j+1))*tx
		return a + (c-a)*tz
	}
	near := func(v []float32, x, z float64) float64 {
		i := min(max(int(math.Round((x-g.MinX)/g.Cell)), 0), s-1)
		j := min(max(int(math.Round((z-g.MinZ)/g.Cell)), 0), s-1)
		return float64(v[j*s+i])
	}
	return func(x, z float64) (float64, float64) {
		d := near(dist, x, z)
		if math.IsInf(d, 1) {
			d = math.MaxFloat64
		}
		return sample(m, x, z) * terrain.WaterMaskDepth, d
	}
}

func runGrassField(rc *procgen.RunContext) error {
	p := rc.Params
	t0 := time.Now()
	t, err := grassTerrain(rc, p.Str("terrain"))
	if err != nil {
		return err
	}
	g, err := runGround(rc, p.Str("terrain"))
	if err != nil {
		return err
	}
	rules, idx := grassRules(p)
	if len(rules) == 0 {
		return fmt.Errorf("no enabled rule names a plants stage")
	}
	items := p.Items("rules")
	kb := &grassKitBuilder{resources: PolyHavenResources(), index: map[string]int{}, parts: map[string][]phimport.PlantPart{}}
	lr := p.Vec2("lod_ratios")
	kb.ratios = [2]float64{lr[0], lr[1]}
	reach := 0.0
	for k := range rules {
		it := items[idx[k]]
		vs, _, err := runVariants(rc, it.Str("plants"))
		if err != nil {
			return fmt.Errorf("rule %s: %w", rules[k].Name, err)
		}
		// A plants stage's light copies (<piece>_lite) are the same parts:
		// the kit thins its own LODs.
		full := vs[:0:0]
		for _, v := range vs {
			if !strings.HasSuffix(v.Name, "_lite") {
				full = append(full, v)
			}
		}
		if len(full) > 0 {
			vs = full
		}
		vs = sizedVariants(grassPieceFilter(vs, it.Str("pieces")), it.Float("min_size"))
		if len(vs) == 0 {
			return fmt.Errorf("rule %s: no piece of %s matches %q", rules[k].Name, it.Str("plants"), it.Str("pieces"))
		}
		for _, v := range vs {
			ki, err := kb.kind(v, it)
			if err != nil {
				return fmt.Errorf("rule %s: %w", rules[k].Name, err)
			}
			rules[k].Kinds = append(rules[k].Kinds, ki)
		}
		reach = math.Max(reach, rules[k].Shore[1]+rules[k].ShoreSoft+1)
	}
	for _, kd := range kb.kit.Kinds {
		tris := []string{}
		for _, m := range kd.LODs {
			tris = append(tris, fmt.Sprint(m.Triangles()))
		}
		rc.Logf("kind %s: %s, %.2f m tall, %.2f m across, triangles %s", kd.Name, kd.Texture, kd.Height, 2*kd.Radius,
			strings.Join(tris, "/"))
	}
	kdata, err := grassfield.EncodeKit(&kb.kit)
	if err != nil {
		return err
	}
	if err := rc.WriteFile(GrassKitFile, kdata); err != nil {
		return err
	}
	kitPath := rc.AssetPath(GrassKitFile)

	avoid, err := avoidDiscs(rc, p.Str("avoid"))
	if err != nil {
		return err
	}
	set := place.GrassSettings{Rules: rules, Seed: rc.Seed, Region: placeRegion(&rc.Env), Avoid: avoid,
		AvoidShrink: p.Float("avoid_shrink"), Water: grassWater(t, reach), Margin: p.Float("margin")}
	for k := range rules {
		if rules[k].Band.CliffBase > 0 {
			g.CliffField(0.5) // built once, before the tiles read it side by side
			break
		}
	}
	x0, z0, size, nx, nz := grassTileGrid(t, g)
	gb := g.Bounds()
	cell := float32(p.Float("cell"))
	type tileOut struct {
		name string
		vol  procgen.VolumeEntry
		n    int
		rule map[int]int
	}
	outs := make([]*tileOut, nx*nz)
	err = rc.ParallelProgress(nx*nz, func(k int) error {
		if err := rc.Err(); err != nil {
			return err
		}
		i, j := k%nx, k/nx
		rect := [4]float64{x0 + float64(i)*size, z0 + float64(j)*size, x0 + float64(i+1)*size, z0 + float64(j+1)*size}
		rect[0], rect[1] = math.Max(rect[0], gb[0]), math.Max(rect[1], gb[1])
		rect[2], rect[3] = math.Min(rect[2], gb[2]), math.Min(rect[3], gb[3])
		if rect[2] <= rect[0] || rect[3] <= rect[1] {
			return nil
		}
		ins := place.Grass(g, set, rect)
		if len(ins) == 0 {
			return nil
		}
		gi := make([]grassfield.Instance, len(ins))
		top := float32(0)
		for q, in := range ins {
			gi[q] = grassfield.Instance{Kind: in.Kind, Pos: [3]float32{float32(in.Pos[0]), float32(in.Pos[1]), float32(in.Pos[2])},
				Yaw: float32(in.Yaw), Scale: float32(in.Scale),
				Tint: [3]float32{float32(in.Tint[0]), float32(in.Tint[1]), float32(in.Tint[2])}}
			top = max(top, kb.kit.Kinds[in.Kind].Height*float32(in.Scale))
		}
		f, err := grassfield.BuildField(grassfield.FieldSpec{Kit: kitPath,
			Min: [2]float32{float32(rect[0]), float32(rect[1])}, Max: [2]float32{float32(rect[2]), float32(rect[3])},
			Cell: cell, Kinds: len(kb.kit.Kinds)}, gi)
		if err != nil {
			return err
		}
		data, err := grassfield.EncodeField(f)
		if err != nil {
			return err
		}
		name := fmt.Sprintf("grass_%d_%d", i, j)
		if err := rc.WriteFile(name+grassfield.FieldExt, data); err != nil {
			return err
		}
		y0, y1 := float64(f.Origin[1]), float64(f.Origin[1]+f.Size[1]+top)
		o := &tileOut{name: name, n: len(ins), rule: map[int]int{}}
		for _, in := range ins {
			o.rule[in.Kind]++
		}
		o.vol = procgen.VolumeEntry{
			"id": name,
			"position": map[interface{}]interface{}{"x": round2((rect[0] + rect[2]) / 2), "y": round2((y0 + y1) / 2),
				"z": round2((rect[1] + rect[3]) / 2)},
			"half": map[interface{}]interface{}{"x": round2((rect[2] - rect[0]) / 2), "y": round2((y1-y0)/2 + 0.5),
				"z": round2((rect[3] - rect[1]) / 2)},
			"grass": map[interface{}]interface{}{"file": rc.AssetPath(name + grassfield.FieldExt), "chunk": "auto"},
		}
		outs[k] = o
		return nil
	})
	if err != nil {
		return err
	}
	art := &GrassFieldArtifact{Kit: kitPath, PerRule: map[string]int{}, Area: (gb[2] - gb[0]) * (gb[3] - gb[1])}
	for _, kd := range kb.kit.Kinds {
		art.Kinds = append(art.Kinds, kd.Name)
	}
	perKind := map[int]int{}
	var vols []procgen.VolumeEntry
	for _, o := range outs {
		if o == nil {
			continue
		}
		vols = append(vols, o.vol)
		art.Fields = append(art.Fields, o.name+grassfield.FieldExt)
		art.Instances += o.n
		for k, n := range o.rule {
			perKind[k] += n
		}
	}
	// Per rule: the kinds are the rules' own (a kind shared by two rules
	// counts for the first).
	owner := map[int]string{}
	for _, r := range rules {
		for _, k := range r.Kinds {
			if _, ok := owner[k]; !ok {
				owner[k] = r.Name
			}
		}
	}
	for k, n := range perKind {
		art.PerRule[owner[k]] += n
	}
	rc.PlaceVolume(vols...)
	sdata, _ := yaml.Marshal(art)
	if err := rc.WriteFile(GrassSummaryFile, sdata); err != nil {
		return err
	}
	rc.Artifact = art
	names := make([]string, 0, len(art.PerRule))
	for n := range art.PerRule {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		rc.Logf("%s: %d clumps", n, art.PerRule[n])
	}
	rc.Logf("%d clumps in %d fields (%d kinds, %.0f per 100 m² of ground) in %s", art.Instances, len(art.Fields),
		len(kb.kit.Kinds), 100*float64(art.Instances)/math.Max(art.Area, 1), time.Since(t0).Round(time.Millisecond))
	rc.Progress(1)
	return nil
}

// previewGrassField is the rules' clumps over the terrain's preview, as
// dots thinned to preview_density (no kit: a preview never imports).
func previewGrassField(pc *procgen.PreviewContext) (*procgen.PreviewData, error) {
	p := pc.Params
	t0 := time.Now()
	g, up, err := previewGround(pc, p.Str("terrain"))
	if err != nil {
		return nil, err
	}
	rules, idx := grassRules(p)
	items := p.Items("rules")
	out := &procgen.PreviewData{Bounds: up.Bounds, Images: up.Images}
	out.Meshes = append(out.Meshes, terrainMeshes(up)...)
	for k := range rules {
		rules[k].Kinds = []int{k}
		rules[k].Density = math.Min(rules[k].Density, p.Float("preview_density"))
	}
	ins := place.Grass(g, place.GrassSettings{Rules: rules, Seed: pc.Seed, Margin: p.Float("margin")}, g.Bounds())
	layers := make([]procgen.PointLayer, len(rules))
	for k := range rules {
		layers[k] = procgen.PointLayer{Name: rules[k].Name, Color: ruleColor(items[idx[k]], idx[k])}
	}
	for _, in := range ins {
		l := &layers[in.Kind]
		l.Points = append(l.Points, [2]float64{in.Pos[0], in.Pos[2]})
	}
	out.Points = append(out.Points, layers...)
	for _, l := range layers {
		out.Notes = append(out.Notes, fmt.Sprintf("%s: %d dots", l.Name, len(l.Points)))
	}
	out.Notes = append(out.Notes, fmt.Sprintf("dots at most %g/m² (the run is denser); %s", p.Float("preview_density"),
		time.Since(t0).Round(time.Millisecond)))
	return out, nil
}
