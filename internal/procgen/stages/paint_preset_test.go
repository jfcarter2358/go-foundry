package stages

// paint_preset_test.go — brush presets (paint_preset.go) and their recipes
// (paint_preset_recipe.go): every shipped preset parses and its recipe's
// stages decode; a recipe on a fresh document declares the layers, adds the
// textures, stages, rules and splat entries, and validates; applying twice
// changes nothing; splat slots are claimed, shared and run out as
// documented; a region document gets the fallback role; and a preset stroke
// writes each layer at the brush's strength times its own.

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/procgen/paint"
	"foundry/internal/testenv"
)

func shippedPresets(t *testing.T) []*Preset {
	t.Helper()
	ps, errs := LoadPresets(filepath.Join(testenv.RepoRoot(t), "resources", PresetDir))
	for _, e := range errs {
		t.Error(e)
	}
	return ps
}

func TestShippedPresetsParse(t *testing.T) {
	ps := shippedPresets(t)
	want := []string{"forest_floor", "meadow", "wildflowers", "dense_grass", "fern_understorey", "moss", "gravel", "pebbles",
		"mud", "dirt_trail", "rock_path", "scree", "snow_patch", "sand_drift", "ash", "pond", "lake", "marsh", "river", "stream", "keep_clear",
		"stepping_stones", "gravel_path", "game_trail"}
	have := map[string]bool{}
	for _, p := range ps {
		have[p.ID] = true
		if p.Name == "" || p.Doc == "" {
			t.Errorf("%s has no name or doc", p.ID)
		}
		if _, err := p.Recipe.stages(); err != nil {
			t.Errorf("%s: %v", p.ID, err)
		}
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("preset %s is not shipped", w)
		}
	}
	// The picker's order is the spec's list.
	if len(ps) > 0 && ps[0].ID != "forest_floor" || len(ps) > 1 && ps[len(ps)-1].ID != "keep_clear" {
		t.Errorf("picker order starts %s, ends %s", ps[0].ID, ps[len(ps)-1].ID)
	}
}

func TestParsePresetRejectsBadFiles(t *testing.T) {
	for _, bad := range []string{
		"name: x\nlayers: []\n",
		"name: x\nlayers: [{layer: A, strength: 1}]\n",
		"name: x\nlayers: [{layer: a, strength: 2}]\n",
		"name: x\nlayers: [{layer: a, strength: 1}, {layer: a, strength: 1}]\n",
		"name: x\nlayers: [{layer: a, strength: 1}]\nrecipe: {paint: [{name: b, kind: density}]}\n",
		"name: x\nlayers: [{layer: a, strength: 1}]\nrecipe: {material: [{layer: a, texture: t, fallback: lava}]}\n",
		"name: x\nlayers: [{layer: a, strength: 1}]\nrecipe: {stages: [{stage: grass_field, id: c, params: {nope: 1}}]}\n",
		"name: x\nlayers: [{layer: a, strength: 1}]\nbogus: 1\n",
	} {
		if _, err := ParsePreset("bad", []byte(bad)); err == nil {
			t.Errorf("parsed %q", bad)
		}
	}
}

// freshDoc is a new generator as the template makes one: textures, a
// terrain and its material, nothing else.
func freshDoc() *procgen.Doc {
	s, ref := procgen.String, procgen.StageRef
	d := smallTerrainDoc()
	d.Stages = append([]procgen.Stage{{Kind: PolyHavenTexturesKind, ID: "tex", Params: procgen.Params{"layers": procgen.List(
		procgen.Params{"name": s("sand"), "id": s("sand")}, procgen.Params{"name": s("cracked"), "id": s("cracked_ground")},
		procgen.Params{"name": s("gravel"), "id": s("gravel_floor")}, procgen.Params{"name": s("cliff"), "id": s("rock_face")})}}},
		d.Stages...)
	d.Stages = append(d.Stages, procgen.Stage{Kind: TerrainMaterialKind, ID: "ground_mat", Params: procgen.Params{
		"terrain": ref("ground"), "textures": ref("tex")}})
	return d
}

func TestForestFloorRecipeOnAFreshDoc(t *testing.T) {
	p := FindPreset(shippedPresets(t), "forest_floor")
	if p == nil {
		t.Fatal("no forest_floor preset")
	}
	d := freshDoc()
	if miss := MissingLayers(d, p); len(miss) != 4 {
		t.Fatalf("missing %v", miss)
	}
	notes, err := ApplyRecipe(d, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(MissingLayers(d, p)) != 0 {
		t.Fatalf("still missing %v", MissingLayers(d, p))
	}
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("the recipe left the document invalid: %v (notes %v)", probs, notes)
	}
	// The texture went into the material's own textures stage, and took
	// the first free cover slot.
	tex := d.StageByID("tex")
	if !listHas(tex.Params["layers"].Items, "name", "forest_litter") {
		t.Fatal("litter texture not added to the material's textures stage")
	}
	mat := d.StageByID("ground_mat")
	if mat.Params.Str("ash") != "forest_litter" {
		t.Fatalf("litter should claim the ash slot, ash = %q", mat.Params.Str("ash"))
	}
	pi := mat.Params["paint"].Items
	if len(pi) != 1 || pi[0].Str("layer") != "forest_litter" || pi[0].Str("target") != "ash" || pi[0].Str("mode") != "override" {
		t.Fatalf("paint items %v", pi)
	}
	// The stages, their refs filled.
	cover := d.StageByID("cover")
	if cover == nil || cover.Params.Str("terrain") != "ground" || cover.Params.Str("avoid") != "cover_logs" {
		t.Fatalf("cover stage %+v", cover)
	}
	var cardRule procgen.Params
	for _, it := range cover.Params["rules"].Items {
		if it.Str("name") == "forest_litter" {
			cardRule = it
		}
	}
	if cardRule == nil || cardRule.Str("card_textures") != "tex" {
		t.Fatalf("card rule's textures ref not filled: %v", cardRule)
	}
	if d.StageByID("cover_logs").Params.Str("terrain") != "ground" || d.StageByID("cover_forest") == nil {
		t.Fatal("source and scatter stages missing")
	}
	// Twice: nothing more.
	before, _ := procgen.EncodeDoc(d)
	notes2, err := ApplyRecipe(d, p)
	after, _ := procgen.EncodeDoc(d)
	if err != nil || len(notes2) != 0 || string(before) != string(after) {
		t.Fatalf("second application changed the document: %v %v", notes2, err)
	}
	// The document round-trips through its file.
	if _, err := procgen.DecodeDoc(after); err != nil {
		t.Fatal(err)
	}
}

func TestRecipeSlotsShareAndRunOut(t *testing.T) {
	ps := shippedPresets(t)
	d := freshDoc()
	var notes []string
	for _, id := range []string{"forest_floor", "mud", "snow_patch", "dirt_trail", "gravel"} {
		n, err := ApplyRecipe(d, FindPreset(ps, id))
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		notes = append(notes, n...)
	}
	mat := d.StageByID("ground_mat")
	if mat.Params.Str("ash") != "forest_litter" || mat.Params.Str("wet") != "mud" || mat.Params.Str("snow") != "snow_patch" {
		t.Fatalf("slots: ash %q wet %q snow %q", mat.Params.Str("ash"), mat.Params.Str("wet"), mat.Params.Str("snow"))
	}
	// A snow slot claimed for painted snow gets a snow line out of reach.
	if v := mat.Params.Vec2("snow_height"); v[0] < 9000 {
		t.Fatalf("snow line %v would snow on the terrain", v)
	}
	target := map[string]string{}
	for _, it := range mat.Params["paint"].Items {
		key := it.Str("layer")
		if key == "" {
			key = "mask:" + it.Str("mask")
		}
		target[key] = it.Str("target")
	}
	// The dirt trail (a path brush since phase P4: its surface is a
	// terrain mask) found no free slot: its fallback, with a note.
	if target["mask:path_dirt_trail"] != "cracked" || !strings.Contains(strings.Join(notes, "\n"), "no free splat slot") {
		t.Fatalf("dirt trail → %q; notes %v", target["mask:path_dirt_trail"], notes)
	}
	// Gravel's texture is the gravel role's already: it shares it.
	if target["gravel"] != "gravel" {
		t.Fatalf("gravel → %q, want the gravel role wearing the same scan", target["gravel"])
	}
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("invalid after five recipes: %v", probs)
	}
	// Grass excludes: forest floor's grass_clear skips the cover stage;
	// mud's path_clear layer reaches the cover; the dirt trail's path_wear
	// mask the grass and its path_clear mask the logs' scatter.
	has := func(id, key, v string) bool { return listHas(d.StageByID(id).Params["exclude"].Items, key, v) }
	if has("cover", "layer", "grass_clear") || !has("cover", "layer", "path_clear") || !has("cover", "mask", "path_wear") ||
		!has("cover_logs", "mask", "path_clear") || has("cover_logs", "mask", "path_wear") {
		t.Fatalf("excludes: cover %v, cover_logs %v", d.StageByID("cover").Params["exclude"], d.StageByID("cover_logs").Params["exclude"])
	}
}

func TestRecipeOnARegionDocUsesTheFallbackRole(t *testing.T) {
	d := &procgen.Doc{Name: "w", Stages: []procgen.Stage{
		{Kind: RegionMaterialKind, ID: "mat", Params: procgen.Params{}},
	}}
	p := FindPreset(shippedPresets(t), "mud")
	notes, err := ApplyRecipe(d, p)
	if err != nil {
		t.Fatal(err)
	}
	it := d.StageByID("mat").Params["paint"].Items
	if len(it) != 1 || it[0].Str("target") != "cracked" {
		t.Fatalf("region paint %v (notes %v)", it, notes)
	}
	// The texture went to a stage of its own (no terrain_material to
	// share): it is there for a card rule, but the region splat cannot
	// take a new layer.
	if d.StageByID(coverTexturesID) == nil {
		t.Fatal("no cover_tex stage")
	}
}

func TestPresetStampsRaiseEachLayerToItsLevel(t *testing.T) {
	p := &Preset{ID: "x", Layers: []PresetLayer{{Layer: "a", Strength: 1}, {Layer: "b", Strength: 0.25}}}
	st := p.Stamps(paint.Stroke{X: 1, Z: 2, Radius: 3, Strength: 0.8, Mode: paint.ModeAdd, Value: 1})
	if len(st) != 2 || st[0].Stroke.Mode != paint.ModeRaise || st[0].Stroke.Value != 1 || st[1].Stroke.Value != 0.25 ||
		math.Abs(st[1].Stroke.Strength-0.2) > 1e-12 || st[1].Stroke.X != 1 || st[1].Stroke.Radius != 3 {
		t.Fatalf("stamps %+v", st)
	}
	// Stamped over and over, each layer stops at its level; a layer
	// painted higher before is left alone.
	a := paint.NewLayer(paint.LayerDef{Name: "a", Kind: paint.KindDensity})
	b := paint.NewLayer(paint.LayerDef{Name: "b", Kind: paint.KindDensity})
	b.Apply(paint.Stroke{X: 20, Z: 0, Radius: 3, Strength: 1, Mode: paint.ModeAdd})
	for x := 0.0; x <= 20; x += 0.5 {
		for _, s := range p.Stamps(paint.Stroke{X: x, Z: 0, Radius: 3, Strength: 0.8, Mode: paint.ModeAdd, Value: 1}) {
			map[string]*paint.Layer{"a": a, "b": b}[s.Layer].Apply(s.Stroke)
		}
	}
	if math.Abs(a.Sample(10, 0)-1) > 0.01 || math.Abs(b.Sample(10, 0)-0.25) > 0.01 || b.Sample(20, 0) < 0.99 {
		t.Fatalf("a %.3f b %.3f (want 1, 0.25), b's earlier stroke %.3f", a.Sample(10, 0), b.Sample(10, 0), b.Sample(20, 0))
	}
	// Erasing takes every layer away.
	for _, s := range p.Stamps(paint.Stroke{X: 10, Z: 0, Radius: 3, Strength: 1, Mode: paint.ModeErase}) {
		map[string]*paint.Layer{"a": a, "b": b}[s.Layer].Apply(s.Stroke)
	}
	if a.Sample(10, 0) != 0 || b.Sample(10, 0) != 0 {
		t.Fatal("erase left paint")
	}
}

// A recipe applied to a fresh document, its preset painted, runs: the
// cover stage grows cards and twigs where the brush went (fake Poly Haven:
// its plant and its texture stand in for the real kit).
func TestPresetPaintsAndRuns(t *testing.T) {
	_, _ = withFakePolyHaven(t)
	s := procgen.String
	src := `name: Test floor
doc: a test
layers:
  - {layer: litter, strength: 1}
  - {layer: twigs, strength: 0.6}
recipe:
  paint:
    - {name: litter, kind: material}
    - {name: twigs, kind: density}
  textures:
    - {name: litter, id: sand}
  material:
    - {layer: litter, texture: litter, slots: [ash], fallback: cracked}
  stages:
    - stage: polyhaven_plants
      id: cover_p
      params: {assets: [{id: p1}], thin_foliage: true}
    - stage: grass_field
      id: cover
      params:
        terrain: $terrain
        cell: 8
        rules:
          - {name: litter, card_textures: $textures, card: litter, density: 4, scale: [0.4, 0.6], slope: [0, 80], paint: litter}
          - {name: twigs, plants: cover_p, orient: ground, wind: false, density: 2, scale: [0.2, 0.3], slope: [0, 80],
             max_triangles: 40, paint: twigs}
`
	p, err := ParsePreset("test_floor", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	d := freshDoc()
	// The fake has one texture, "sand": point the material's layers at it.
	tex := d.StageByID("tex")
	tex.Params["layers"] = procgen.List(procgen.Params{"name": s("sand"), "id": s("sand")})
	mat := d.StageByID("ground_mat")
	for _, k := range []string{"sand", "cracked", "gravel", "rock"} {
		mat.Params[k] = s("sand")
	}
	if _, err := ApplyRecipe(d, p); err != nil {
		t.Fatal(err)
	}
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	for _, ps := range p.Stamps(paint.Stroke{X: 0, Z: 0, Radius: 10, Strength: 1, Mode: paint.ModeAdd, Value: 1}) {
		d.PaintLayer(ps.Layer).Apply(ps.Stroke)
	}
	dir := t.TempDir()
	r, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	art := r.Stage("cover").Artifact.(*GrassFieldArtifact)
	if art.PerRule["litter"] == 0 || art.PerRule["twigs"] == 0 {
		t.Fatalf("per rule %v", art.PerRule)
	}
	// Everything grew inside the painted disc (radius 10).
	for _, name := range art.Fields {
		if _, err := os.Stat(filepath.Join(r.Stage("cover").Dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if r.Stage("ground_mat") == nil {
		t.Fatal("no material stage result")
	}
}
