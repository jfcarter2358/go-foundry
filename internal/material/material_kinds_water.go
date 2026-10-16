package material

// material_kinds_water.go — the `water` effect's parameters (docs/terrain-
// paint/SPEC.md, "Water > Look"; built in phase W1, docs/terrain-paint/
// PHASE_W1.md).
//
// The effect began as five numbers lifted off a sector's `water:` block: a
// swell (amplitude, length, speed) and a fresnel mix toward one flat
// reflection colour. W1 rebuilt the look around what is actually behind and
// above the surface — the scene under it (depth colour, refraction, the
// shoreline), the sky and the scene over it (reflection) and the sun (a GGX
// glint) — and these are the numbers that steer it. The five old ones keep
// their names, so every .fmat written before still parses:
//
//   - wave_amplitude / wave_length / wave_speed: the swell, as before
//     (vertex.glsl displaces with it, the fragment stage differentiates it).
//   - reflectivity: now a multiplier on the PHYSICAL fresnel of water
//     (F0 = 0.02), with 0.5 being physical — so the 0.45 most old files carry
//     is still close to what they meant.
//   - reflect_color: now a TINT on the reflection (its hue; the brightest
//     channel is taken as 1), and the colour the reflection falls back to
//     when the level has no sky at all.
//
// The new ones:
//
//   - absorption: Beer–Lambert density, 1/m. How quickly the bed is lost
//     with depth. Clear alpine water ~0.15, a lake ~0.35, a swamp ~2.
//   - shallow_color: what a (1/absorption) metre of water does to the light
//     passing through it — the transmittance's colour. Water drinks red
//     first, so clear water is a pale cyan here.
//   - scatter_color: the colour deep water tends to — light scattered back
//     out of the volume, lit by the sky and the sun.
//   - subsurface: how strongly a back-lit wave crest glows with
//     scatter_color (a cheap stand-in for light through a thin crest).
//   - roughness: GGX roughness of the surface, for the sun glint and the
//     blur of the sky reflection.
//   - refraction: how far the detail normals bend the view of the bed.
//   - foam: shoreline foam amount (0 none). The band widens with it.
//   - shore_fade: metres of depth over which the water's edge fades in, so
//     the shoreline is soft rather than a hard line.
//   - flow_speed / flow_direction: the detail normals' drift, m/s along a
//     world-XZ heading (a lake's wind drift, a river's current until W3's
//     flow maps).
//   - detail_scale / detail_strength: metres per detail-normal tile, and
//     how strongly the detail ripples the surface.
//   - swell / swell_length: a Gerstner-profile swell (sharp crests, flat
//     troughs) on top of the sines, for open water. 0 is none.
//   - caustics: strength of the caustics on the bed (high water quality).
//   - ssr: how much of the reflection may come from the screen-space march
//     (high water quality); 0 keeps a surface on its sky reflection.
//   - flow_map (phase W3, docs/terrain-paint/PHASE_W3.md): read the mesh's
//     vertex colours as a flow map — green and blue the current along X and
//     Z (0.5 still, ±4 m/s at the ends), red the foam — and move the ripples
//     and the foam with it, two-phase (two copies offset by half a cycle,
//     cross-faded, so the advection never stretches). A generated river's
//     ribbons carry it; 0 (the default) leaves every other surface on its
//     one flow_direction.
//   - falls / falls_speed (phase W4, docs/terrain-paint/PHASE_W4.md): shade
//     the surface as FALLING water — a waterfall's sheet (ground_edit
//     places one over every fall, wearing water_falls): streaks scrolling
//     down the mesh's V at falls_speed m/s, white and opaque where the
//     sheet's vertex colour says it is aerated, translucent where it is
//     glassy, its sides frayed. The vertex colour is the sheet's own (red
//     aeration, green edge, blue progress down the fall), not a flow map,
//     so flow_map stays 0 on it. 0 (the default) is ordinary water.
//
// DefaultWater is the one source of every default: the ParamDefs below
// read it, and gfx.DefaultWaterParams copies it.

import "github.com/go-gl/mathgl/mgl32"

// DefaultWater is the default water — a clear temperate lake. A bare
// `effect: water` is this.
func DefaultWater() Water {
	return Water{
		WaveAmplitude: 0.03, WaveLength: 4, WaveSpeed: 0.3,
		Reflectivity: 0.5, ReflectColor: mgl32.Vec3{1, 1, 1},
		Absorption:     0.6,
		ShallowColor:   mgl32.Vec3{0.55, 0.8, 0.82},
		ScatterColor:   mgl32.Vec3{0.025, 0.11, 0.14},
		Subsurface:     0.5,
		Roughness:      0.06,
		Refraction:     0.35,
		Foam:           0.5,
		ShoreFade:      0.12,
		FlowSpeed:      0.25,
		FlowDirection:  mgl32.Vec2{1, 0.35},
		DetailScale:    3,
		DetailStrength: 1,
		Swell:          0,
		SwellLength:    24,
		Caustics:       1,
		SSR:            1,
		FlowMap:        0,
		Falls:          0,
		FallsSpeed:     4,
	}
}

// waterParamDefs is the water effect's parameter list.
func waterParamDefs() []ParamDef {
	d := DefaultWater()
	f := func(name, label, doc string, def, lo, hi float32) ParamDef {
		return ParamDef{Name: name, Type: ParamFloat, Label: label, Doc: doc, Default: FloatValue(def), Min: lo, Max: hi}
	}
	c := func(name, label, doc string, def mgl32.Vec3) ParamDef {
		return ParamDef{Name: name, Type: ParamColor, Label: label, Doc: doc, Default: ColorValue(def), Min: 0, Max: 1}
	}
	return []ParamDef{
		f("wave_amplitude", "Wave amplitude", "Peak vertical displacement of the rolling swell, world units.",
			d.WaveAmplitude, 0, 10),
		f("wave_length", "Wave length", "World units per wave cycle.", d.WaveLength, 0.01, 500),
		f("wave_speed", "Wave speed", "Wave cycles a second.", d.WaveSpeed, 0, 20),
		f("reflectivity", "Reflectivity",
			"Strength of the reflection: 0.5 is water's physical fresnel, 1 twice it, 0 none.",
			d.Reflectivity, 0, 1),
		c("reflect_color", "Reflect tint",
			"Tints the reflection (its hue; the brightest channel counts as 1). Also the reflection's colour on a level with no sky.",
			d.ReflectColor),
		f("absorption", "Absorption",
			"How fast light is lost with depth, per metre: 0.15 clear mountain water, 0.35 a lake, 2 a swamp.",
			d.Absorption, 0, 10),
		c("shallow_color", "Shallow colour",
			"The colour light keeps after passing through the water — clear water is a pale cyan (red goes first).",
			d.ShallowColor),
		c("scatter_color", "Deep colour",
			"The colour deep water tends to: light scattered back out of the water, lit by the sky and the sun.",
			d.ScatterColor),
		f("subsurface", "Crest glow", "How much a back-lit wave crest glows with the deep colour.", d.Subsurface, 0, 2),
		f("roughness", "Roughness", "GGX roughness: the sun glint's spread and the reflection's blur.",
			d.Roughness, 0.01, 1),
		f("refraction", "Refraction", "How far the ripples bend the view of the bed.", d.Refraction, 0, 2),
		f("foam", "Shore foam", "Foam along the shoreline (0 none); the band widens with it.", d.Foam, 0, 2),
		f("shore_fade", "Shore fade", "Metres of depth over which the water's edge fades in.", d.ShoreFade, 0, 5),
		f("flow_speed", "Flow speed", "How fast the ripples drift, metres a second.", d.FlowSpeed, 0, 10),
		{Name: "flow_direction", Type: ParamVec2, Label: "Flow direction",
			Doc: "The ripples' drift heading in world X/Z (normalised).", Default: Vec2Value(d.FlowDirection)},
		f("detail_scale", "Ripple size", "Metres per tile of the fine ripple normals.", d.DetailScale, 0.1, 100),
		f("detail_strength", "Ripple strength", "How strongly the fine ripples tilt the surface.",
			d.DetailStrength, 0, 3),
		f("swell", "Swell", "Height of a sharp-crested (Gerstner-profile) open-water swell, metres. 0 none.",
			d.Swell, 0, 10),
		f("swell_length", "Swell length", "The swell's wavelength, metres.", d.SwellLength, 1, 500),
		f("caustics", "Caustics", "Strength of the dancing light on the bed (high water quality only).",
			d.Caustics, 0, 3),
		f("ssr", "Screen reflections",
			"How much of the reflection may come from the screen-space march (high water quality only).",
			d.SSR, 0, 1),
		f("flow_map", "Flow map",
			"Steer the ripples and foam by the mesh's vertex-colour flow map (a generated river's ribbons carry one): "+
				"how strongly. 0: off, the surface drifts along flow_direction.",
			d.FlowMap, 0, 2),
		f("falls", "Falling water",
			"Shade the surface as a waterfall's falling sheet (streaks down the mesh's V, aerated white where its vertex "+
				"colour's red says so, frayed sides): how strongly. 0: ordinary water.", d.Falls, 0, 1),
		f("falls_speed", "Fall speed", "How fast a falling sheet's streaks run down it, metres a second.", d.FallsSpeed, 0, 30),
	}
}

// resolveWater reads a water effect into a Water.
func resolveWater(e *Effect) Water {
	return Water{
		WaveAmplitude:  e.Float("wave_amplitude"),
		WaveLength:     e.Float("wave_length"),
		WaveSpeed:      e.Float("wave_speed"),
		Reflectivity:   e.Float("reflectivity"),
		ReflectColor:   e.Color("reflect_color"),
		Absorption:     e.Float("absorption"),
		ShallowColor:   e.Color("shallow_color"),
		ScatterColor:   e.Color("scatter_color"),
		Subsurface:     e.Float("subsurface"),
		Roughness:      e.Float("roughness"),
		Refraction:     e.Float("refraction"),
		Foam:           e.Float("foam"),
		ShoreFade:      e.Float("shore_fade"),
		FlowSpeed:      e.Float("flow_speed"),
		FlowDirection:  e.Vec2("flow_direction"),
		DetailScale:    e.Float("detail_scale"),
		DetailStrength: e.Float("detail_strength"),
		Swell:          e.Float("swell"),
		SwellLength:    e.Float("swell_length"),
		Caustics:       e.Float("caustics"),
		SSR:            e.Float("ssr"),
		FlowMap:        e.Float("flow_map"),
		Falls:          e.Float("falls"),
		FallsSpeed:     e.Float("falls_speed"),
	}
}
