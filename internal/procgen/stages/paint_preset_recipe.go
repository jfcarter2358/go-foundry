package stages

// paint_preset_recipe.go — a brush preset's RECIPE: what a document needs so
// that the layers a preset paints grow something (paint_preset.go;
// docs/terrain-paint/PHASE_P3.md).
//
//	recipe:
//	  paint:                          # layer declarations (paint: in the .fgen)
//	    - {name: forest_litter, kind: material}
//	    - {name: forest_twigs, kind: density}
//	  textures:                       # Poly Haven texture layers
//	    - {name: forest_litter, id: forest_leaves_04, scale: 2.5}
//	  material:                       # splat paint entries
//	    - {layer: forest_litter, texture: forest_litter, slots: [ash, snow, wet], fallback: cracked}
//	    - {mask: river_bed, texture: river_gravel, slots: [ash, snow], fallback: gravel}  # a terrain mask (W3)
//	  exclude:                        # stage exclude lists to join
//	    - {layer: grass_clear, kind: grass_field, except: [cover]}
//	  stages:                         # .fgen stage syntax, merged by ID
//	    - stage: polyhaven_plants
//	      id: cover_forest
//	      params: {assets: [{id: dry_branches_medium_01}], thin_foliage: true}
//	    - stage: grass_field
//	      id: cover
//	      params: {terrain: $terrain, rules: [...]}
//
// APPLYING it (ApplyRecipe) to a document, every step idempotent — applying
// twice changes nothing the second time:
//
//  1. PAINT: each layer the document does not declare is declared.
//  2. TEXTURES go into the polyhaven_textures stage the document's
//     terrain_material reads (so the material can name them), or, with no
//     material stage, into a stage `cover_tex` the recipe makes. A layer of
//     that name already there is kept, and a layer of the same Poly Haven
//     ID is used under its own name (no second copy of one scan).
//  3. STAGES are merged by ID. A stage the document lacks is appended (after
//     every existing stage, in recipe order, so it can read any of them). A
//     stage it has gains the recipe's list items it lacks: rules (by name)
//     for grass_field and scatter, assets (by id) for the Poly Haven stages.
//     Stage refs written `$terrain`, `$textures` and `$scatter` become the
//     document's terrain stage, the textures stage of step 2 and its first
//     scatter stage (or nothing). `$terrain` is the document's ground_edit
//     when it has one (or the recipe brings one): the ground the stages
//     should stand on. `$source` is always the terrain or region_terrain
//     stage itself (what a ground_edit edits). ground_edit's water and
//     height lists merge by layer, as rules do by name.
//  3b. A GROUND_EDIT the recipe adds (a pond, lake or marsh preset) is
//     slotted in: every stage that read the terrain directly is pointed at
//     it instead, so the material, the grass and the scatters stand on the
//     carved ground and see its water (grass never stands in water deeper
//     than a rule's max_depth, 0 by default: that is the grass kept out of
//     the ponds).
//  4. MATERIAL entries join the terrain_material's `paint` list as
//     overrides. The target is a splat slot: a role already wearing the same
//     Poly Haven texture; else the first of `slots` that is free (a cover —
//     wet, ash, snow — the document leaves empty), which is then set to the
//     texture (a snow slot used this way gets a snow line out of reach, so
//     only the paint lays it); else `fallback`, a role whose texture the
//     document already has (a note says so: terrain_material has seven
//     slots, four roles and three covers, and a document can run out). A
//     region_material is given the entry with the fallback role.
//  5. EXCLUDE entries add the layer to the `exclude` list of every stage of
//     that kind but those listed in `except` (so "less grass" can thin the
//     document's own sward and not the cover stage's litter).
//
// Phase W3 (the river and stream presets): a material entry may name a
// terrain `mask` instead of a layer (river_bed, river_bar — the item reads
// the terrain's mask, terrain_material_paint.go); a ground_edit the
// document already has takes the recipe's top-level params it does not set
// (the river presets' river_rocks), and its `rivers` list merges by name.
//
// Phase P4 (the path presets): an exclude entry may name a terrain `mask`
// (path_clear, path_wear) with a `strength` and a grass `shrink`; the
// `paths` list merges by name; path_rocks is hoisted as river_rocks is.
//
// It returns notes, one line per change, for the editor and the CLI.

import (
	"fmt"
	"sort"
	"strings"

	"foundry/internal/procgen"
	"foundry/internal/procgen/paint"

	"gopkg.in/yaml.v2"
)

// RecipeTexture is a Poly Haven texture layer a recipe needs.
type RecipeTexture struct {
	Name  string  `yaml:"name"`
	ID    string  `yaml:"id"`
	Scale float64 `yaml:"scale,omitempty"`
}

// RecipeMaterial is a splat paint entry.
type RecipeMaterial struct {
	Layer string `yaml:"layer"`
	// Mask names a terrain mask instead of a layer (phase W3).
	Mask     string   `yaml:"mask,omitempty"`
	Texture  string   `yaml:"texture"`
	Slots    []string `yaml:"slots,omitempty"`
	Fallback string   `yaml:"fallback"`
	Strength float64  `yaml:"strength,omitempty"`
}

// RecipeExclude joins a layer to the exclude lists of every stage of a
// kind but those in Except. Phase P4: Mask names a terrain mask instead
// (a ground_edit's path_clear, path_wear), with a Strength (default 1) and,
// for grass, a Shrink.
type RecipeExclude struct {
	Layer    string   `yaml:"layer"`
	Mask     string   `yaml:"mask,omitempty"`
	Strength float64  `yaml:"strength,omitempty"`
	Shrink   float64  `yaml:"shrink,omitempty"`
	Kind     string   `yaml:"kind"`
	Except   []string `yaml:"except,omitempty"`
}

// Recipe is what a preset's layers need (file comment).
type Recipe struct {
	Paint    []recipeLayer    `yaml:"paint"`
	Textures []RecipeTexture  `yaml:"textures"`
	Material []RecipeMaterial `yaml:"material"`
	Exclude  []RecipeExclude  `yaml:"exclude"`
	Stages   []interface{}    `yaml:"stages"`
}

// recipeLayer is a layer declaration as the .fgen writes it.
type recipeLayer struct {
	Name       string  `yaml:"name"`
	Kind       string  `yaml:"kind"`
	Resolution float64 `yaml:"resolution,omitempty"`
}

func (l recipeLayer) def() paint.LayerDef {
	return paint.LayerDef{Name: l.Name, Kind: l.Kind, Resolution: l.Resolution}
}

// recipeRefs are the placeholders a recipe's stage refs may use.
const (
	refTerrain  = "$terrain"
	refSource   = "$source"
	refTextures = "$textures"
	refScatter  = "$scatter"
)

// coverTexturesID is the textures stage a recipe makes when the document
// has no material stage to share one with.
const coverTexturesID = "cover_tex"

func (r *Recipe) validate(layers []PresetLayer) error {
	declared := map[string]bool{}
	for _, l := range r.Paint {
		if err := l.def().Validate(); err != nil {
			return fmt.Errorf("recipe: %w", err)
		}
		declared[l.Name] = true
	}
	for _, l := range layers {
		if len(r.Paint) > 0 && !declared[l.Layer] {
			return fmt.Errorf("recipe declares no layer %s, which the preset paints", l.Layer)
		}
	}
	for _, e := range r.Exclude {
		if (e.Layer == "") == (e.Mask == "") || e.Kind == "" {
			return fmt.Errorf("recipe: an exclude entry needs a layer or a mask, and a kind")
		}
	}
	for _, m := range r.Material {
		if (m.Layer == "") == (m.Mask == "") || m.Texture == "" || m.Fallback == "" {
			return fmt.Errorf("recipe: a material entry needs a layer or a mask, a texture and a fallback")
		}
		if !roleKey(m.Fallback) {
			return fmt.Errorf("recipe: material fallback %q is not a role", m.Fallback)
		}
		for _, s := range m.Slots {
			if !roleKey(s) {
				return fmt.Errorf("recipe: material slot %q is not a role", s)
			}
		}
	}
	_, err := r.stages()
	return err
}

// roleKey reports whether k is one of terrain_material's seven slots.
func roleKey(k string) bool {
	for _, r := range groundRoleKeys {
		if r == k {
			return true
		}
	}
	return false
}

// stages decodes the recipe's stages through the .fgen decoder.
func (r *Recipe) stages() ([]procgen.Stage, error) {
	if len(r.Stages) == 0 {
		return nil, nil
	}
	data, err := yaml.Marshal(map[string]interface{}{"stages": r.Stages})
	if err != nil {
		return nil, err
	}
	d, err := procgen.DecodeDoc(data)
	if err != nil {
		return nil, fmt.Errorf("recipe stages: %w", err)
	}
	return d.Stages, nil
}

// MissingLayers is the layers preset p paints that d does not declare.
func MissingLayers(d *procgen.Doc, p *Preset) []string {
	var out []string
	for _, l := range p.Layers {
		if d.PaintDef(l.Layer) == nil {
			out = append(out, l.Layer)
		}
	}
	return out
}

// firstStageOf is the first stage of one of kinds, or nil.
func firstStageOf(d *procgen.Doc, kinds ...string) *procgen.Stage {
	for i := range d.Stages {
		for _, k := range kinds {
			if d.Stages[i].Kind == k && !d.Stages[i].Disabled {
				return &d.Stages[i]
			}
		}
	}
	return nil
}

// ApplyRecipe applies p's recipe to d (file comment) and returns what it
// changed, a line each.
func ApplyRecipe(d *procgen.Doc, p *Preset) ([]string, error) {
	r := &p.Recipe
	stages, err := r.stages()
	if err != nil {
		return nil, err
	}
	var notes []string
	notef := func(f string, a ...any) { notes = append(notes, fmt.Sprintf(f, a...)) }

	// 1. Layers.
	for _, l := range r.Paint {
		if d.PaintDef(l.Name) == nil {
			d.EnsurePaintLayer(l.def())
			notef("layer %s (%s)", l.Name, l.Kind)
		}
	}

	// 2. Textures: the material's own textures stage, or cover_tex. alias
	// maps a recipe texture name to the layer already holding its scan.
	alias := map[string]string{}
	mat := firstStageOf(d, TerrainMaterialKind)
	texID := ""
	if mat != nil {
		texID = mat.Params.Str("textures")
	}
	if len(r.Textures) > 0 && (texID == "" || d.StageByID(texID) == nil) {
		texID = coverTexturesID
		if d.StageByID(texID) == nil {
			d.Stages = append(d.Stages, procgen.Stage{Kind: PolyHavenTexturesKind, ID: texID,
				Params: procgen.Params{"layers": procgen.List()}})
			notef("stage %s (polyhaven_textures)", texID)
		}
	}
	if len(r.Textures) > 0 {
		ts := d.StageByID(texID)
		items, ok := ts.Params["layers"]
		if !ok {
			items = resolvedList(ts, "layers")
		}
		for _, t := range r.Textures {
			if listHas(items.Items, "name", t.Name) {
				continue
			}
			if name := listFind(items.Items, "id", t.ID, "name"); name != "" {
				alias[t.Name] = name
				continue
			}
			it := procgen.Params{"name": procgen.String(t.Name), "id": procgen.String(t.ID)}
			if t.Scale > 0 {
				it["scale"] = procgen.Float(t.Scale)
			}
			items.Items = append(items.Items, it)
			notef("texture %s (%s) in %s", t.Name, t.ID, texID)
		}
		ts.Params["layers"] = items
	}

	// 3. Stages, merged by ID. $terrain is the ground the stages stand
	// on: the document's ground_edit, or the one the recipe brings.
	sourceID := ""
	if s := firstStageOf(d, TerrainKind, RegionTerrainKind); s != nil {
		sourceID = s.ID
	}
	terrainID := sourceID
	addedGround := ""
	if s := firstStageOf(d, GroundEditKind); s != nil {
		terrainID = s.ID
	} else {
		for _, rs := range stages {
			if rs.Kind == GroundEditKind && d.StageByID(rs.ID) == nil {
				terrainID, addedGround = rs.ID, rs.ID
				break
			}
		}
	}
	scatterID := ""
	if s := firstStageOf(d, ScatterKind); s != nil {
		scatterID = s.ID
	}
	for _, rs := range stages {
		fillRecipeRefs(rs.Params, map[string]string{refTerrain: terrainID, refSource: sourceID, refTextures: texID, refScatter: scatterID})
		renameCards(rs.Params, alias)
		have := d.StageByID(rs.ID)
		if have == nil {
			d.Stages = append(d.Stages, rs)
			notef("stage %s (%s)", rs.ID, rs.Kind)
			continue
		}
		if have.Kind != rs.Kind {
			return notes, fmt.Errorf("the document's stage %s is a %s, the recipe's a %s", rs.ID, have.Kind, rs.Kind)
		}
		if have.Kind == GroundEditKind {
			// The recipe's top-level params the document's ground_edit
			// leaves unset (a river preset's river_rocks).
			keys := make([]string, 0, len(rs.Params))
			for k := range rs.Params {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if v := rs.Params[k]; v.Type != procgen.ParamList {
					if _, set := have.Params[k]; !set {
						have.Params[k] = v
						notef("%s in %s", k, rs.ID)
					}
				}
			}
		}
		for _, list := range []struct{ name, key string }{{"rules", "name"}, {"assets", "id"}, {"layers", "name"}, {"water", "layer"}, {"height", "layer"}, {"rivers", "name"}, {"paths", "name"}} {
			add, ok := rs.Params[list.name]
			if !ok {
				continue
			}
			cur, ok := have.Params[list.name]
			if !ok {
				cur = resolvedList(have, list.name)
			}
			for _, it := range add.Items {
				if !listHas(cur.Items, list.key, it.Str(list.key)) {
					cur.Items = append(cur.Items, it)
					notef("%s %s in %s", strings.TrimSuffix(list.name, "s"), it.Str(list.key), rs.ID)
				}
			}
			have.Params[list.name] = cur
		}
	}

	// 3b. A ground_edit the recipe added stands between the terrain and
	// every stage that read it.
	if addedGround != "" && sourceID != "" {
		rewireToGround(d, sourceID, addedGround, notef)
	}

	// A stage the recipe appended that an earlier stage now reads (a rule
	// merged into the document's own grass stage naming a new plants
	// stage) moves up to just above its first reader.
	hoistRecipeStages(d)

	// 4. Material entries. The stages moved (appended, hoisted): find the
	// material stage again rather than trust a pointer into the old list.
	mat = firstStageOf(d, TerrainMaterialKind)
	for _, m := range r.Material {
		if a, ok := alias[m.Texture]; ok {
			m.Texture = a
		}
		applyRecipeMaterial(d, mat, texID, m, notef)
	}

	// 5. Exclude lists.
	for _, e := range r.Exclude {
		for i := range d.Stages {
			s := &d.Stages[i]
			if s.Kind != e.Kind || listed(e.Except, s.ID) {
				continue
			}
			cur, ok := s.Params["exclude"]
			if !ok {
				cur = procgen.List()
			}
			if e.Mask != "" {
				if listHas(cur.Items, "mask", e.Mask) {
					continue
				}
				it := procgen.Params{"mask": procgen.String(e.Mask)}
				if e.Strength > 0 && e.Strength != 1 {
					it["strength"] = procgen.Float(e.Strength)
				}
				if e.Shrink > 0 && s.Kind == GrassFieldKind {
					it["shrink"] = procgen.Float(e.Shrink)
				}
				cur.Items = append(cur.Items, it)
				s.Params["exclude"] = cur
				notef("exclude mask %s in %s", e.Mask, s.ID)
				continue
			}
			if listHas(cur.Items, "layer", e.Layer) {
				continue
			}
			cur.Items = append(cur.Items, procgen.Params{"layer": procgen.Mask(e.Layer)})
			s.Params["exclude"] = cur
			notef("exclude %s in %s", e.Layer, s.ID)
		}
	}
	return notes, nil
}

// rewireToGround points every enabled stage whose `terrain` param names
// the terrain source (and accepts a ground_edit) at the ground_edit.
func rewireToGround(d *procgen.Doc, source, ground string, notef func(string, ...any)) {
	for i := range d.Stages {
		s := &d.Stages[i]
		if s.ID == ground || s.Disabled || s.Params.Str("terrain") != source {
			continue
		}
		k := procgen.KindByName(s.Kind)
		if k == nil {
			continue
		}
		def := k.Param("terrain")
		if def == nil || def.Type != procgen.ParamStage || !listed(def.StageKinds, GroundEditKind) {
			continue
		}
		s.Params["terrain"] = procgen.StageRef(ground)
		notef("%s now stands on %s (the edited ground)", s.ID, ground)
	}
}

// OrderStagesByRefs keeps d's stage order consistent with every stage
// reference: any stage named by an earlier one — through any ParamStage,
// top-level or inside a list item (a path's `rocks`, a ground_edit's
// `river_rocks`, a grass rule's `plants`) — moves up to just above its
// first reader. ApplyRecipe calls it, and so does every brush that adds an
// item referencing a stage after the recipe ran (AddPathStroke,
// AddRiverStroke): the rock path's stones stage used to be appended after
// the ground_edit and only named by the path item added later, and the
// run refused the document (found in phase W5).
func OrderStagesByRefs(d *procgen.Doc) { hoistRecipeStages(d) }

// hoistRecipeStages moves every stage above the first stage that reads it,
// keeping the order otherwise: stages only ever move up, and only past
// stages that do not read them, so a document that was in order stays in
// order. A reference cycle (which validation reports) is not untangled:
// the passes are capped.
func hoistRecipeStages(d *procgen.Doc) {
	limit := len(d.Stages)*len(d.Stages) + 1
	for moved := true; moved && limit > 0; limit-- {
		moved = false
		for i := range d.Stages {
			first := -1
			for j := 0; j < i; j++ {
				k := procgen.KindByName(d.Stages[j].Kind)
				if k == nil {
					continue
				}
				if listed(procgen.StageRefs(k.Params, procgen.Resolve(k.Params, d.Stages[j].Params)), d.Stages[i].ID) {
					first = j
					break
				}
			}
			if first < 0 {
				continue
			}
			st := d.Stages[i]
			copy(d.Stages[first+1:i+1], d.Stages[first:i])
			d.Stages[first] = st
			moved = true
			break
		}
	}
}

// listed reports whether id is in ids.
func listed(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// applyRecipeMaterial is step 4 for one entry.
func applyRecipeMaterial(d *procgen.Doc, mat *procgen.Stage, texID string, m RecipeMaterial, notef func(string, ...any)) {
	strength := m.Strength
	if strength <= 0 {
		strength = 1
	}
	if mat == nil {
		reg := firstStageOf(d, RegionMaterialKind)
		if reg == nil {
			notef("no material stage: %s paints no splat", m.Layer)
			return
		}
		if addPaintItem(reg, m.Layer, m.Fallback, strength, m.Mask) {
			notef("splat %s → %s in %s (the region's own %s)", m.Layer, m.Fallback, reg.ID, m.Fallback)
		}
		return
	}
	if m.Mask != "" && listHas(mat.Params["paint"].Items, "mask", m.Mask) || m.Mask == "" && listHas(mat.Params["paint"].Items, "layer", m.Layer) {
		return
	}
	kind := procgen.KindByName(mat.Kind)
	p := procgen.Resolve(kind.Params, mat.Params)
	// The texture's Poly Haven ID, to find a role already wearing it.
	texPH := ""
	var texLayers []procgen.Params
	if ts := d.StageByID(texID); ts != nil {
		texLayers = procgen.Resolve(procgen.KindByName(ts.Kind).Params, ts.Params).Items("layers")
		for _, it := range texLayers {
			if it.Str("name") == m.Texture {
				texPH = it.Str("id")
			}
		}
	}
	phOf := func(layer string) string {
		for _, it := range texLayers {
			if it.Str("name") == layer {
				return it.Str("id")
			}
		}
		return ""
	}
	target := ""
	for _, k := range groundRoleKeys {
		if v := p.Str(k); v != "" && (v == m.Texture || texPH != "" && phOf(v) == texPH) {
			target = k
			break
		}
	}
	if target == "" {
		for _, slot := range m.Slots {
			if p.Str(slot) == "" {
				mat.Params[slot] = procgen.String(m.Texture)
				if slot == "snow" {
					// Painted only: the snow line out of reach.
					mat.Params["snow_height"] = procgen.Vec2(9000, 10000)
					mat.Params["snow_reference"] = procgen.Enum("world")
				}
				target = slot
				notef("splat slot %s = %s in %s", slot, m.Texture, mat.ID)
				break
			}
		}
	}
	if target == "" {
		target = m.Fallback
		what := m.Layer
		if what == "" {
			what = "mask " + m.Mask
		}
		notef("no free splat slot in %s: %s paints its %s layer (%s)", mat.ID, what, m.Fallback, p.Str(m.Fallback))
	}
	if addPaintItem(mat, m.Layer, target, strength, m.Mask) {
		what := m.Layer
		if m.Mask != "" {
			what = "mask " + m.Mask
		}
		notef("splat %s → %s in %s", what, target, mat.ID)
	}
}

// addPaintItem appends an override paint item; false when the layer
// already has one.
func addPaintItem(s *procgen.Stage, layer, target string, strength float64, mask string) bool {
	cur, ok := s.Params["paint"]
	if !ok {
		cur = procgen.List()
	}
	if mask != "" && listHas(cur.Items, "mask", mask) || mask == "" && listHas(cur.Items, "layer", layer) {
		return false
	}
	it := procgen.Params{"layer": procgen.Mask(layer), "target": procgen.String(target), "mode": procgen.Enum("override")}
	if mask != "" {
		it = procgen.Params{"mask": procgen.String(mask), "target": procgen.String(target), "mode": procgen.Enum("override")}
	}
	if strength != 1 {
		it["strength"] = procgen.Float(strength)
	}
	cur.Items = append(cur.Items, it)
	s.Params["paint"] = cur
	return true
}

// resolvedList is stage s's list param name as it resolves (its default
// when the document does not set it), copied.
func resolvedList(s *procgen.Stage, name string) procgen.Value {
	k := procgen.KindByName(s.Kind)
	if k == nil {
		return procgen.List()
	}
	v := procgen.Resolve(k.Params, s.Params)[name]
	out := procgen.List()
	for _, it := range v.Items {
		out.Items = append(out.Items, it.Clone())
	}
	return out
}

// listFind is the field out of the first item whose key is v ("" if none).
func listFind(items []procgen.Params, key, v, field string) string {
	for _, it := range items {
		if it.Str(key) == v {
			return it.Str(field)
		}
	}
	return ""
}

// renameCards points card rules at the layer an aliased texture is under.
func renameCards(p procgen.Params, alias map[string]string) {
	rules, ok := p["rules"]
	if !ok {
		return
	}
	for _, it := range rules.Items {
		if a, ok := alias[it.Str("card")]; ok {
			it["card"] = procgen.String(a)
		}
	}
}

// listHas reports whether a list holds an item whose key is v.
func listHas(items []procgen.Params, key, v string) bool {
	for _, it := range items {
		if it.Str(key) == v {
			return true
		}
	}
	return false
}

// fillRecipeRefs replaces the placeholders in p's stage refs, list items
// included.
func fillRecipeRefs(p procgen.Params, refs map[string]string) {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := p[k]
		switch {
		case v.Type == procgen.ParamStage:
			if r, ok := refs[v.Str]; ok {
				v.Str = r
				p[k] = v
			}
		case v.Type == procgen.ParamList:
			for _, it := range v.Items {
				fillRecipeRefs(it, refs)
			}
		}
	}
}
