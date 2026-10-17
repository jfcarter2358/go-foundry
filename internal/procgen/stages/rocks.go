package stages

// rocks.go — the `rocks` stage: a family of weathered rock variants of one
// form (boulder, cliff chunk, mesa cap, ledge slab, hoodoo, rubble, pebble),
// each cooked to its own Nanite mesh with baked vertex colour, and a manifest
// that later stages place them from.
//
// The generation is internal/procgen/rocks (its package comment has the
// pipeline; presets.go the forms and what they are modelled on). This file
// is the schema and its mapping onto rocks.Settings; rocks_run.go the run
// (variants, cook, manifest, an optional gallery placement);
// rocks_preview.go the turntable preview and the Load.
//
// Most parameters are 0..1 characters the presets read in their own terms
// (Fracture opens a cliff's joints and cuts a boulder's faces); the sizes,
// bed thickness and joint spacing are metres, 0 taking the preset's
// default from its size. The preview_* parameters change only the preview
// (procgen.ParamDef.PreviewOnly): choosing another variant to look at never
// makes the run stale.

import (
	"fmt"

	"foundry/internal/procgen"
	"foundry/internal/procgen/rocks"
)

// RocksKind is the stage's name.
const RocksKind = "rocks"

func init() {
	procgen.RegisterStage(procgen.StageKind{
		Name:     RocksKind,
		Label:    "Rocks",
		Category: "Rocks",
		Version:  1,
		// Per environment region of a level generation doc, its variants
		// made once into a shared cache (place_region.go).
		Regional: procgen.RegionalPerRegion,
		OnDemand: true,
		Doc: "A family of weathered rock variants — boulders, cliff chunks, mesa caps, ledges, hoodoos, rubble or " +
			"pebbles in sandstone or granite — modelled as signed distance fields (fracture faces, joints, undercut " +
			"strata, chips), meshed, decimated to a triangle budget and baked with vertex colour (strata tint, cavity, " +
			"varnish, dust, ambient occlusion). Each variant is cooked to a Nanite mesh; rocks.yaml describes them for " +
			"the placement stages.",
		Params:   rocksParams(),
		Run:      runRocks,
		Preview:  previewRocks,
		Load:     loadRocks,
		Validate: validateRocks,
	})
	procgen.RegisterTemplate(procgen.Template{
		Name:  "rocks_demo",
		Label: "Rock variants",
		Doc:   "Three rocks stages: sandstone boulders, a hero cliff chunk and pebbles.",
		New: func(name string) *procgen.Doc {
			return &procgen.Doc{Name: name, Seed: 1234, Doc: "Rock variants", Stages: []procgen.Stage{
				{Kind: RocksKind, ID: "boulders", Params: procgen.Params{"preset": procgen.Enum("boulder"), "variants": procgen.Int(8)}},
				{Kind: RocksKind, ID: "cliffs", Params: procgen.Params{"preset": procgen.Enum("cliff_chunk"), "variants": procgen.Int(3)}},
				{Kind: RocksKind, ID: "pebbles", Params: procgen.Params{"preset": procgen.Enum("pebble"), "variants": procgen.Int(12)}},
			}}
		},
	})
}

func rocksParams() []procgen.ParamDef {
	d := rocks.Defaults()
	f := procgen.Float
	i := procgen.Int
	unit := func(name, label string, def float64, doc string) procgen.ParamDef {
		return procgen.ParamDef{Name: name, Type: procgen.ParamFloat, Label: label, Default: f(def), Min: 0, Max: 1, Step: 0.01, Doc: doc}
	}
	presetDoc := "The rock form:"
	for _, n := range rocks.PresetNames() {
		doc, size, of, tris := rocks.PresetDoc(n)
		presetDoc += fmt.Sprintf(" %s — %s (%g m %s, %d triangles);", n, doc, size, of, tris)
	}
	return []procgen.ParamDef{
		{Name: "preset", Type: procgen.ParamEnum, Default: procgen.Enum(d.Preset), Choices: rocks.PresetNames(), Doc: presetDoc},
		{Name: "variants", Type: procgen.ParamInt, Default: i(6), Min: 1, Max: 64,
			Doc: "How many variants: each a different seed, cooked to its own mesh."},
		{Name: "seed", Type: procgen.ParamSeed, Default: procgen.Seed(0), Doc: "Changes every variant without changing anything else."},
		{Name: "size", Type: procgen.ParamFloat, Label: "Size (m)", Default: f(0), Min: 0, Max: 500, Step: 0.05,
			Doc: "The preset's main dimension (a boulder's length, a cliff chunk's or hoodoo's height, a mesa cap's width). 0 is the preset's."},
		unit("size_jitter", "Size jitter", d.SizeJitter, "How much the size varies from variant to variant (±, as a fraction; at most 0.5)."),
		{Name: "triangles", Type: procgen.ParamInt, Default: i(0), Min: 0, Max: 4000000,
			Doc: "The triangle budget of each variant before the cook (the cook's LODs go down from it). 0 is the preset's: " +
				"light for scattered rocks (≤ 15k), dense for hero pieces (600k for a cliff chunk). Scattered rocks must stay light: " +
				"every placement costs its mesh's clusters."},
		{Name: "lithology", Type: procgen.ParamEnum, Default: procgen.Enum("sandstone"), Choices: []string{"sandstone", "granite"},
			Doc: "Sandstone keeps its beds (strata, laminations, cross-bedding); granite has none and weathers rounder, with a speckled mottle."},
		{Name: "palette", Type: procgen.ParamEnum, Default: procgen.Enum(d.Palette), Choices: rocks.Palettes,
			Doc: "The colours: auto (Entrada red for sandstone, grey for granite), formations of the Colorado Plateau, granite, or custom (the two colours below)."},
		{Name: "color", Type: procgen.ParamColor, Default: procgen.Color(d.Color[0], d.Color[1], d.Color[2]), Min: 0, Max: 1,
			Doc: "The custom palette's base colour."},
		{Name: "band_color", Type: procgen.ParamColor, Label: "Band colour", Default: procgen.Color(d.BandColor[0], d.BandColor[1], d.BandColor[2]), Min: 0, Max: 1,
			Doc: "The custom palette's band colour, which beds mix toward."},
		unit("color_variation", "Colour variation", d.ColorVariation, "How far each bed (and each joint block) strays from the base colour."),
		unit("fracture", "Fracture", d.Fracture, "Broken faces and open joints: a boulder's fracture planes, a cliff's joint sets and blocks."),
		{Name: "joint_spacing", Type: procgen.ParamFloat, Label: "Joint spacing (m)", Default: f(0), Min: 0, Max: 200, Step: 0.05,
			Doc: "The joint network's spacing. 0 is the preset's, from the size."},
		unit("weathering", "Weathering", d.Weathering, "How rounded the edges and corners are (convex places weather first)."),
		unit("roughness", "Roughness", d.Roughness, "Grooves, lumps and grain on the faces."),
		unit("chips", "Chips", d.Chips, "Spalls knocked off the edges."),
		{Name: "strata_thickness", Type: procgen.ParamFloat, Label: "Bed thickness (m)", Default: f(0), Min: 0, Max: 100, Step: 0.01,
			Doc: "The mean bed thickness. 0 is the preset's, from the size."},
		unit("strata_relief", "Bed relief", d.StrataRelief, "How far soft beds weather back behind the hard ones."),
		unit("undercut", "Undercut", d.Undercut, "How a soft bed under a hard one recedes: flush at its base, deepest under the overhang."),
		unit("cavity", "Cavity", d.Cavity, "Darker crevices, paler worn edges (baked into the colour)."),
		unit("ao", "Ambient occlusion", d.AO, "The baked occlusion's strength (the colour's alpha, which the shader takes off the ambient light)."),
		{Name: "ao_radius", Type: procgen.ParamFloat, Label: "AO radius (m)", Default: f(0), Min: 0, Max: 50, Step: 0.01,
			Doc: "How far the occlusion looks. 0 is the preset's, from the size."},
		unit("varnish", "Varnish", d.Varnish, "Desert varnish: dark streaks down steep faces, heavier under overhangs."),
		unit("dust", "Dust", d.Dust, "Dust settled on the up-facing faces."),
		{Name: "sharpness", Type: procgen.ParamFloat, Default: f(-1), Min: -1, Max: 1, Step: 0.01,
			Doc: "How sharp the mesher keeps edges: 0 rounds every vertex to its cell's centroid, 1 keeps creases exact. −1 is the preset's."},
		{Name: "texture", Type: procgen.ParamTexture, Default: procgen.Texture(""),
			Doc: "A texture under the vertex colour (box-mapped). None: the vertex colour is the albedo."},
		{Name: "uv_scale", Type: procgen.ParamFloat, Label: "UV scale", Default: f(0.5), Min: 0.001, Max: 100, Step: 0.01,
			Doc: "Texture repeats per metre of the box mapping."},
		{Name: "textures", Added: true, Type: procgen.ParamStage, StageKinds: []string{PolyHavenTexturesKind}, Default: procgen.StageRef(""),
			Doc: "A Poly Haven textures stage: the rocks wear its Texture layer projected TRIPLANAR (a material the stage writes), " +
				"under their vertex colour's cavity and strata, instead of the box-mapped texture. Empty: none."},
		{Name: "texture_layer", Added: true, Type: procgen.ParamString, Label: "Texture layer", Default: procgen.String("cliff"),
			Doc: "The textures stage's layer the rocks wear."},
		{Name: "triplanar_scale", Added: true, Type: procgen.ParamFloat, Label: "Triplanar scale (m)", Default: f(0), Min: 0, Max: 1000, Step: 0.05,
			Doc: "Metres a repeat of the triplanar texture (0: the layer's own)."},
		{Name: "collision", Type: procgen.ParamEnum, Default: procgen.Enum("auto"), Choices: []string{"auto", "hulls", "mesh", "none"},
			Doc: "How the variants collide: hulls (a convex decomposition, for rocks a body rolls off), mesh (the mesh itself; " +
				"the cook skips the decomposition), none. Auto: hulls up to the hull size, mesh above, none for pebbles."},
		{Name: "hull_size", Type: procgen.ParamFloat, Label: "Hull size (m)", Default: f(4), Min: 0, Max: 100, Step: 0.1,
			Doc: "Auto collision's limit: a variant whose largest extent is under it gets hulls."},
		{Name: "cook", Type: procgen.ParamBool, Default: procgen.Bool(true),
			Doc: "Cook each variant to a Nanite mesh (.fnm). Off writes .glb files instead."},
		{Name: "cook_memory", Type: procgen.ParamInt, Label: "Cook memory (MB)", Default: i(3000), Min: 100, Max: 64000,
			Doc: "Variants are made and cooked side by side only as far as this budget allows."},
		{Name: "gallery", Type: procgen.ParamBool, Default: procgen.Bool(false),
			Doc: "Also place every variant in a row in the target level, for looking at them (the placement stages place them for real)."},
		{Name: "gallery_origin", Type: procgen.ParamVec3, Label: "Gallery origin", Default: procgen.Vec3(0, 0, 0),
			Doc: "Where the gallery row starts; it runs along +X."},
		{Name: "preview_variant", Type: procgen.ParamInt, Label: "Preview variant", Default: i(0), Min: 0, Max: 63, PreviewOnly: true,
			Doc: "Which variant the turntable shows (the preview only: not part of the run)."},
		{Name: "preview_triangles", Type: procgen.ParamInt, Label: "Preview triangles", Default: i(30000), Min: 500, Max: 4000000, PreviewOnly: true,
			Doc: "The most triangles the preview makes the variant with (the run uses Triangles)."},
	}
}

// rocksSettings maps the stage's params onto rocks.Settings.
func rocksSettings(env *procgen.Env) rocks.Settings {
	p := env.Params
	s := rocks.Defaults()
	s.Preset, s.Lithology, s.Palette = p.Str("preset"), p.Str("lithology"), p.Str("palette")
	s.Size, s.SizeJitter, s.Triangles = p.Float("size"), p.Float("size_jitter"), p.Int("triangles")
	s.Color, s.BandColor, s.ColorVariation = p.Vec3("color"), p.Vec3("band_color"), p.Float("color_variation")
	s.Fracture, s.JointSpacing = p.Float("fracture"), p.Float("joint_spacing")
	s.Weathering, s.Roughness, s.Chips = p.Float("weathering"), p.Float("roughness"), p.Float("chips")
	s.StrataThickness, s.StrataRelief, s.Undercut = p.Float("strata_thickness"), p.Float("strata_relief"), p.Float("undercut")
	s.Cavity, s.AO, s.AORadius = p.Float("cavity"), p.Float("ao"), p.Float("ao_radius")
	s.Varnish, s.Dust, s.Sharpness = p.Float("varnish"), p.Float("dust"), p.Float("sharpness")
	s.UVScale = p.Float("uv_scale")
	s.Ctx = env.Ctx
	return s
}

// variantName is variant i's file stem and placement suffix.
func variantName(preset string, i int) string { return fmt.Sprintf("%s_%02d", preset, i) }

func validateRocks(_ *procgen.Doc, _ *procgen.Stage, p procgen.Params) []string {
	var out []string
	if p.Int("preview_variant") >= p.Int("variants") {
		out = append(out, fmt.Sprintf("Preview variant %d is past the last of %d variants", p.Int("preview_variant"), p.Int("variants")))
	}
	if p.Str("palette") == "custom" && p.Vec3("color") == [3]float64{} {
		out = append(out, "The custom palette's colour is black")
	}
	return out
}
