package stages

// environment.go — the `environment` stage: the target level's air and light
// (docs/canyon/PHASE_6.md).
//
// The other stages place things; this one sets what they are seen through.
// It writes level-wide settings (procgen.LevelSettings), which the splice
// puts into the target level as it does placements — owned by the
// generator, rewritten by every run (genlevel/settings.go):
//
//   - `fog`: the atmosphere — exponential height fog lying in the canyon
//     bottom, aerial perspective (a warm haze thickening with range, bluish
//     at distance), the sun's in-scattering through both, the share of it the
//     sky takes — and, optionally, a distance fade of the old kind;
//   - `sky`: the canyon skybox (sky_canyon), or a gradient, or left alone;
//   - `sun`: the level's first directional light's angles, colour and
//     brightness;
//   - `view_distance`, when set.
//
// With a terrain stage named, heights are measured from the terrain's
// lowest point, so "height fog 0 m up" is the canyon floor whatever the
// terrain's elevation; without one they are world heights.
//
// It can also place a DUST HAZE: one wide, shallow box volume over the
// terrain filled with the dust_haze cloud preset (world.CloudLookPreset) —
// drifting banks of dust in the volumetric march, where the height fog is
// smooth. Off by default: it costs a march over a large part of the screen.
//
// Every default is the desert canyon's, tuned by looking at the phase-4
// canyon (PHASE_6.md has the renders).

import (
	"fmt"
	"math"

	"foundry/internal/procgen"
	"foundry/internal/procgen/terrain"
)

// EnvironmentKind is the stage's name.
const EnvironmentKind = "environment"

func init() {
	procgen.RegisterStage(procgen.StageKind{
		Name:     EnvironmentKind,
		Label:    "Environment",
		Category: "Level",
		// Served by the level generation doc's shared stages for every
		// region; never expanded per region (docs/regions/SPEC.md).
		Regional: procgen.RegionalShared,
		Version:  1,
		Doc: "The target level's air and light: height fog in the low ground, aerial perspective (haze that " +
			"thickens with range and turns bluish), the sun's glow through the haze, the sky (the canyon skybox) " +
			"and the sun — written into the level's fog, sky and light settings by the splice, as placements are. " +
			"Optionally a drifting dust haze over the terrain, as a volumetric cloud volume.",
		Params:   environmentParams(),
		Run:      runEnvironment,
		Preview:  previewEnvironment,
		Validate: validateEnvironment,
	})
}

func environmentParams() []procgen.ParamDef {
	f, b, c, e := procgen.Float, procgen.Bool, procgen.Color, procgen.Enum
	return []procgen.ParamDef{
		{Name: "terrain", Type: procgen.ParamStage, StageKinds: TerrainSources, Default: procgen.StageRef(""),
			Doc: "A terrain stage: heights below are measured from its lowest point, and the dust haze covers it. Empty: heights are world heights."},

		{Name: "fog_mode", Type: procgen.ParamEnum, Label: "Distance fog", Default: e("none"),
			Choices: []string{"none", "linear", "exp", "exp2"},
			Doc:     "The old distance fade on top of the atmosphere: none leaves the atmosphere alone, which is what a canyon wants."},
		{Name: "fog_color", Type: procgen.ParamColor, Label: "Fog colour", Default: c(0.8, 0.78, 0.74), Min: 0, Max: 1,
			Doc: "The distance fade's colour, and the sky's horizon blend's."},
		{Name: "fog_start", Type: procgen.ParamFloat, Label: "Fog start (m)", Default: f(400), Min: 0, Max: 5000, Step: 10,
			Doc: "Where the distance fade begins (linear, exp, exp2)."},
		{Name: "fog_end", Type: procgen.ParamFloat, Label: "Fog end (m)", Default: f(3000), Min: 0, Max: 5000, Step: 10,
			Doc: "Where the linear fade is complete."},
		{Name: "max_opacity", Type: procgen.ParamFloat, Label: "Max opacity", Default: f(1), Min: 0, Max: 1, Step: 0.01,
			Doc: "The most of anything the fog and the atmosphere may hide."},

		{Name: "height_fog", Type: procgen.ParamBool, Label: "Height fog", Default: b(true),
			Doc: "Exponential height fog: densest at its base, thinning upward — dust lying in the canyon bottom."},
		{Name: "height_density", Type: procgen.ParamFloat, Label: "Height fog density", Default: f(0.006), Min: 0, Max: 1, Step: 0.0005,
			Doc: "Extinction per metre at the base: 0.006 hides about 45% of the floor 100 m off."},
		{Name: "height_base", Type: procgen.ParamFloat, Label: "Height fog base (m)", Default: f(0), Min: -1000, Max: 1000, Step: 0.5,
			Doc: "The height the density is measured at, above the terrain's lowest point (or a world height)."},
		{Name: "height_falloff", Type: procgen.ParamFloat, Label: "Height fog falloff", Default: f(0.12), Min: 0.0001, Max: 10, Step: 0.005,
			Doc: "Per metre: the fog thins by e every 1/falloff metres up (0.12: every 8 m)."},
		{Name: "height_color", Type: procgen.ParamColor, Label: "Height fog colour", Default: c(0.84, 0.74, 0.62), Min: 0, Max: 1,
			Doc: "The ground haze's colour: a warm dust."},

		{Name: "aerial", Type: procgen.ParamBool, Label: "Aerial perspective", Default: b(true),
			Doc: "Haze that thickens with range, tinted bluish at distance by a Rayleigh-like per-channel extinction."},
		{Name: "aerial_distance", Type: procgen.ParamFloat, Label: "Aerial distance (m)", Default: f(1100), Min: 1, Max: 1e6, Step: 10,
			Doc: "How far the haze takes 63% of what is behind it (at its base). Short for a hazy day; a kilometre makes a 250 m canyon read as deep."},
		{Name: "aerial_color", Type: procgen.ParamColor, Label: "Aerial colour", Default: c(0.8, 0.78, 0.74), Min: 0, Max: 1,
			Doc: "The haze's colour at range: match the sky's horizon, so far rock melts into it."},
		{Name: "aerial_tint", Type: procgen.ParamColor, Label: "Aerial tint", Default: c(0.75, 1, 1.35), Min: 0, Max: 8,
			Doc: "Per-channel extinction weights. Blue above red is Rayleigh's: far rock goes bluish before it goes pale. (1, 1, 1) is a grey dust haze."},
		{Name: "aerial_scale_height", Type: procgen.ParamFloat, Label: "Aerial scale height (m)", Default: f(300), Min: 1, Max: 1e6, Step: 10,
			Doc: "How far up the haze thins by e: low keeps the sky clear overhead."},

		{Name: "sun_glow", Type: procgen.ParamBool, Label: "Sun glow", Default: b(true),
			Doc: "The sun's forward scattering through the haze: looking toward the sun, the air glows."},
		{Name: "sun_glow_strength", Type: procgen.ParamFloat, Label: "Sun glow strength", Default: f(0.05), Min: 0, Max: 16, Step: 0.005,
			Doc: "How much of the sun's light the lobe moves toward looking into it."},
		{Name: "sun_glow_anisotropy", Type: procgen.ParamFloat, Label: "Sun glow anisotropy", Default: f(0.7), Min: -0.95, Max: 0.95, Step: 0.01,
			Doc: "Henyey-Greenstein g: near 1 a tight glow round the sun, 0 none."},
		{Name: "sky_haze", Type: procgen.ParamFloat, Label: "Sky haze", Default: f(0.6), Min: 0, Max: 1, Step: 0.01,
			Doc: "How much of the atmosphere the sky takes. A skybox with its own haze painted in wants less than 1."},

		{Name: "sky", Type: procgen.ParamEnum, Label: "Sky", Default: e("canyon"), Choices: []string{"keep", "canyon", "gradient"},
			Doc: "keep: the level's own; canyon: the sky_canyon skybox (desert sky over canyon country); gradient: a warm desert gradient."},
		{Name: "sun", Type: procgen.ParamBool, Label: "Set the sun", Default: b(true),
			Doc: "Set the level's first directional light (made if there is none) from the four below."},
		{Name: "sun_yaw", Type: procgen.ParamFloat, Label: "Sun yaw (°)", Default: f(50), Min: -360, Max: 360, Step: 1,
			Doc: "The way the sunlight travels, degrees about +Y from +X."},
		{Name: "sun_pitch", Type: procgen.ParamFloat, Label: "Sun pitch (°)", Default: f(-30), Min: -90, Max: 0, Step: 1,
			Doc: "Its elevation, negative down: -30 is a late-morning sun that rakes the walls."},
		{Name: "sun_color", Type: procgen.ParamColor, Label: "Sun colour", Default: c(1, 0.95, 0.85), Min: 0, Max: 1,
			Doc: "The sunlight's colour: a little warm."},
		{Name: "sun_brightness", Type: procgen.ParamFloat, Label: "Sun brightness", Default: f(1), Min: 0, Max: 16, Step: 0.05,
			Doc: "The sunlight's strength."},
		{Name: "view_distance", Type: procgen.ParamFloat, Label: "View distance (m)", Default: f(0), Min: 0, Max: 100000, Step: 50,
			Doc: "The level's far plane. 0 leaves it as it is."},
		{Name: "hdr", Added: true, Type: procgen.ParamBool, Label: "HDR", Default: b(false),
			Doc: "Shade the level in high dynamic range with a filmic (ACES) tone curve: light above 1 is kept and rolled off " +
				"instead of clipped, so a strong sun makes bright, contrasty rock. Writes the level's tonemap block; off leaves the level's own. " +
				"Takes effect in frames drawn with TAA (the scene target); the editor's view stays LDR."},
		{Name: "exposure", Added: true, Type: procgen.ParamFloat, Label: "Exposure", Default: f(1), Min: 0.05, Max: 16, Step: 0.01,
			Doc: "With HDR: multiplies the scene's light before the curve."},
		{Name: "ambient_brightness", Added: true, Type: procgen.ParamFloat, Label: "Ambient brightness", Default: f(-1), Min: -1, Max: 16, Step: 0.01,
			Doc: "The level's global (ambient) light's brightness; -1 leaves it as it is. With HDR, a sun well above 1 over an ambient near 0.5 is the look of a clear desert day."},
		{Name: "spawn", Added: true, Type: procgen.ParamBool, Label: "Set the spawn", Default: b(false),
			Doc: "Put the level's spawn point (the player's start) on the terrain at Spawn at, facing Spawn yaw."},
		{Name: "spawn_at", Added: true, Type: procgen.ParamVec2, Label: "Spawn at", Default: procgen.Vec2(0, 0),
			Doc: "Where the spawn stands, world X and Z — moved onto the nearest canyon centreline within 80 m, so it lands on the floor; its height is the terrain's there."},
		{Name: "spawn_yaw", Added: true, Type: procgen.ParamFloat, Label: "Spawn yaw (°)", Default: f(0), Min: -360, Max: 360, Step: 1,
			Doc: "Which way the player starts facing, degrees about +Y from +X (the sun's yaw convention)."},
		{Name: "ambient_color", Added: true, Type: procgen.ParamColor, Label: "Ambient colour", Default: c(0.62, 0.7, 0.85), Min: 0, Max: 1,
			Doc: "The ambient light's colour, set with Ambient brightness: the blue sky's light in the shadows."},
		{Name: "shafts", Added: true, Type: procgen.ParamBool, Label: "Light shafts", Default: b(false),
			Doc: "Shadowed sunlight in dusty air near the eye: beams through a slot canyon's gaps and a cave mouth (the level fog's shafts block). " +
				"Marched at the volumetrics' resolution, a shadow-map tap a step."},
		{Name: "shafts_density", Added: true, Type: procgen.ParamFloat, Label: "Shaft dust density", Default: f(0.01), Min: 0, Max: 1, Step: 0.0005,
			Doc: "The dust's extinction per metre at its base: 0.01 takes away 63% over 100 m."},
		{Name: "shafts_distance", Added: true, Type: procgen.ParamFloat, Label: "Shaft distance (m)", Default: f(150), Min: 1, Max: 2000, Step: 5,
			Doc: "How far from the eye the march reaches."},
		{Name: "shafts_steps", Added: true, Type: procgen.ParamInt, Label: "Shaft steps", Default: procgen.Int(24), Min: 4, Max: 64, Step: 1,
			Doc: "Samples a ray takes (half below the High quality tier under TAA)."},
		{Name: "shafts_color", Added: true, Type: procgen.ParamColor, Label: "Shaft tint", Default: c(1, 1, 1), Min: 0, Max: 4,
			Doc: "Tints the light the dust scatters."},
		{Name: "shafts_falloff", Added: true, Type: procgen.ParamFloat, Label: "Shaft dust falloff", Default: f(0), Min: 0, Max: 10, Step: 0.005,
			Doc: "Per metre above the terrain's lowest point: the dust thins by e every 1/falloff metres up. 0 is uniform."},
		{Name: "shafts_anisotropy", Added: true, Type: procgen.ParamFloat, Label: "Shaft anisotropy", Default: f(0.5), Min: -0.95, Max: 0.95, Step: 0.01,
			Doc: "Henyey-Greenstein g: toward 1 the beams glow looking toward the sun and fade looking away."},
		{Name: "shafts_ambient", Added: true, Type: procgen.ParamFloat, Label: "Shaft ambient", Default: f(0.5), Min: 0, Max: 4, Step: 0.01,
			Doc: "How much of the level's ambient light the dust scatters (the shafts block's ambient): low keeps shade clear so only the sunbeams glow."},
		{Name: "shafts_strength", Added: true, Type: procgen.ParamFloat, Label: "Shaft strength", Default: f(1), Min: 0, Max: 10, Step: 0.01,
			Doc: "Scales the sunlight the dust scatters (the shafts block's strength)."},

		{Name: "dust", Type: procgen.ParamBool, Label: "Dust haze", Default: b(false),
			Doc: "A drifting dust haze over the terrain: one box volume of the dust_haze cloud preset, marched by the volumetrics. Costs a march over much of the screen."},
		{Name: "dust_height", Type: procgen.ParamFloat, Label: "Dust height (m)", Default: f(18), Min: 1, Max: 1000, Step: 0.5,
			Doc: "How deep the dust layer is, from the base up."},
		{Name: "dust_density", Type: procgen.ParamFloat, Label: "Dust density", Default: f(0.006), Min: 0, Max: 10, Step: 0.0005,
			Doc: "The dust's extinction per metre in its thickest parts."},
		{Name: "dust_coverage", Type: procgen.ParamFloat, Label: "Dust coverage", Default: f(0.8), Min: 0, Max: 1, Step: 0.01,
			Doc: "How much of the layer the banks fill."},
	}
}

// environmentFrame is where the stage measures heights from and what the
// dust covers: the terrain's lowest point and rectangle, or the world's zero
// and a 512 m square.
type environmentFrame struct {
	floor                  float64
	minX, minZ, maxX, maxZ float64
	top                    float64
}

func environmentFrameOf(t *terrain.Terrain) environmentFrame {
	if t == nil {
		return environmentFrame{minX: -256, minZ: -256, maxX: 256, maxZ: 256, top: 100}
	}
	b := t.Meta.Bounds
	return environmentFrame{floor: float64(b.MinY), minX: b.MinX, minZ: b.MinZ, maxX: b.MaxX, maxZ: b.MaxZ, top: float64(b.MaxY)}
}

func rgbMap(v [3]float64) map[string]interface{} {
	return map[string]interface{}{"r": round4(v[0]), "g": round4(v[1]), "b": round4(v[2])}
}

func round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }

// environmentFog is the level's `fog:` block for p.
func environmentFog(p procgen.Params, fr environmentFrame) map[string]interface{} {
	fog := map[string]interface{}{
		"mode":        p.Str("fog_mode"),
		"color":       rgbMap(p.Vec3("fog_color")),
		"start":       p.Float("fog_start"),
		"end":         p.Float("fog_end"),
		"max_opacity": p.Float("max_opacity"),
		"sky_blend":   0.0,
		"sky_haze":    p.Float("sky_haze"),
	}
	if p.Bool("height_fog") {
		fog["height"] = map[string]interface{}{
			"density": p.Float("height_density"),
			"base":    round4(fr.floor + p.Float("height_base")),
			"falloff": p.Float("height_falloff"),
			"color":   rgbMap(p.Vec3("height_color")),
		}
	}
	if p.Bool("aerial") {
		fog["aerial"] = map[string]interface{}{
			"distance":     p.Float("aerial_distance"),
			"color":        rgbMap(p.Vec3("aerial_color")),
			"tint":         rgbMap(p.Vec3("aerial_tint")),
			"scale_height": p.Float("aerial_scale_height"),
			"base":         round4(fr.floor),
		}
	}
	if p.Bool("sun_glow") {
		fog["sun"] = map[string]interface{}{
			"strength":   p.Float("sun_glow_strength"),
			"anisotropy": p.Float("sun_glow_anisotropy"),
		}
	}
	if p.Bool("shafts") {
		fog["shafts"] = map[string]interface{}{
			"density":    p.Float("shafts_density"),
			"distance":   p.Float("shafts_distance"),
			"steps":      p.Int("shafts_steps"),
			"color":      rgbMap(p.Vec3("shafts_color")),
			"base":       round4(fr.floor),
			"falloff":    p.Float("shafts_falloff"),
			"anisotropy": p.Float("shafts_anisotropy"),
		}
		if a := p.Float("shafts_ambient"); a != 0.5 {
			fog["shafts"].(map[string]interface{})["ambient"] = a
		}
		if st := p.Float("shafts_strength"); st != 1 {
			fog["shafts"].(map[string]interface{})["strength"] = st
		}
	}
	return fog
}

// environmentSky is the level's `sky:` block for the sky param, or nil to
// leave it.
func environmentSky(kind string) map[string]interface{} {
	switch kind {
	case "canyon":
		faces := []string{}
		for _, f := range []string{"px", "nx", "py", "ny", "pz", "nz"} {
			faces = append(faces, "sky_canyon_"+f)
		}
		return map[string]interface{}{"kind": "cubemap", "faces": faces}
	case "gradient":
		return map[string]interface{}{
			"kind":    "gradient",
			"zenith":  rgbMap([3]float64{0.36, 0.55, 0.82}),
			"horizon": rgbMap([3]float64{0.82, 0.8, 0.76}),
			"ground":  rgbMap([3]float64{0.55, 0.45, 0.36}),
		}
	}
	return nil
}

// environmentDust is the dust haze's volume entry.
func environmentDust(p procgen.Params, fr environmentFrame) procgen.VolumeEntry {
	h := p.Float("dust_height")
	base := fr.floor + p.Float("height_base")
	cx, cz := (fr.minX+fr.maxX)/2, (fr.minZ+fr.maxZ)/2
	hx, hz := (fr.maxX-fr.minX)/2, (fr.maxZ-fr.minZ)/2
	return procgen.VolumeEntry{
		"id":       "dust",
		"shape":    "box",
		"position": map[string]interface{}{"x": round4(cx), "y": round4(base + h/2), "z": round4(cz)},
		"half":     map[string]interface{}{"x": round4(hx), "y": round4(h / 2), "z": round4(hz)},
		"cloud": map[string]interface{}{
			"preset":   "dust_haze",
			"density":  p.Float("dust_density"),
			"coverage": p.Float("dust_coverage"),
			"softness": 0.6,
		},
	}
}

func environmentTerrain(rc *procgen.RunContext) (*terrain.Terrain, error) {
	id := rc.Params.Str("terrain")
	if id == "" {
		return nil, nil
	}
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

func runEnvironment(rc *procgen.RunContext) error {
	p := rc.Params
	t, err := environmentTerrain(rc)
	if err != nil {
		return err
	}
	fr := environmentFrameOf(t)
	rc.SetLevel("fog", environmentFog(p, fr))
	if sky := environmentSky(p.Str("sky")); sky != nil {
		rc.SetLevel("sky", sky)
	}
	if p.Bool("sun") {
		rc.SetLevel("sun", map[string]interface{}{
			"yaw": p.Float("sun_yaw"), "pitch": p.Float("sun_pitch"),
			"color": rgbMap(p.Vec3("sun_color")), "brightness": p.Float("sun_brightness"),
			"cast_shadows": true,
		})
	}
	if v := p.Float("view_distance"); v > 0 {
		rc.SetLevel("view_distance", v)
	}
	if p.Bool("spawn") {
		at := p.Vec2("spawn_at")
		y := 0.0
		if t != nil {
			// Onto the nearest canyon floor within 80 m: a spawn asked for
			// "about here" in a canyon wants the floor, not the wall the
			// meander moved in front of it.
			best := 80.0
			for _, pm := range t.Meta.Paths {
				for _, q := range pm.Points {
					if d := math.Hypot(q[0]-at[0], q[1]-at[1]); d < best && t.Contains(q[0], q[1]) {
						best, at = d, [2]float64{q[0], q[1]}
					}
				}
			}
			y = float64(t.HeightAt(at[0], at[1]))
		}
		rc.SetLevel("spawn", map[string]interface{}{
			"position": map[string]interface{}{"x": round4(at[0]), "y": round4(y + 0.05), "z": round4(at[1])},
			"rotation": map[string]interface{}{"x": 0, "y": round4(p.Float("spawn_yaw") * math.Pi / 180), "z": 0},
		})
	}
	if p.Bool("hdr") {
		rc.SetLevel("tonemap", map[string]interface{}{"exposure": p.Float("exposure")})
	}
	if v := p.Float("ambient_brightness"); v >= 0 {
		rc.SetLevel("ambient", map[string]interface{}{"color": rgbMap(p.Vec3("ambient_color")), "brightness": v})
	}
	if p.Bool("dust") {
		if t == nil {
			rc.Logf("dust haze: no terrain named, so it covers a 512 m square round the origin")
		}
		rc.PlaceVolume(environmentDust(p, fr))
	}
	rc.Logf("fog %s, height fog %v (base %.1f m), aerial %v (%.0f m), sun glow %v, sky %s, dust %v",
		p.Str("fog_mode"), p.Bool("height_fog"), fr.floor+p.Float("height_base"), p.Bool("aerial"),
		p.Float("aerial_distance"), p.Bool("sun_glow"), p.Str("sky"), p.Bool("dust"))
	return nil
}

// previewEnvironment is a profile of what the air does: the share of a
// surface left at range along the floor, and at the height fog's base and
// 30 m up — as notes, since the 2D view has nothing to draw.
func previewEnvironment(pc *procgen.PreviewContext) (*procgen.PreviewData, error) {
	p := pc.Params
	out := &procgen.PreviewData{}
	left := func(y, d float64) [3]float64 {
		var tau [3]float64
		if p.Bool("height_fog") {
			th := p.Float("height_density") * d * math.Exp(-p.Float("height_falloff")*(y-p.Float("height_base")))
			for i := range tau {
				tau[i] += th
			}
		}
		if p.Bool("aerial") {
			tint := p.Vec3("aerial_tint")
			k := 1 / max(p.Float("aerial_scale_height"), 1)
			for i := range tau {
				tau[i] += tint[i] / max(p.Float("aerial_distance"), 1) * d * math.Exp(-k*y)
			}
		}
		var r [3]float64
		for i := range r {
			r[i] = max(math.Exp(-tau[i]), 1-p.Float("max_opacity"))
		}
		return r
	}
	for _, y := range []float64{p.Float("height_base") + 2, p.Float("height_base") + 30} {
		line := fmt.Sprintf("%.0f m up:", y)
		for _, d := range []float64{50, 100, 250, 500} {
			r := left(y, d)
			line += fmt.Sprintf("  %.0f m %.0f%%", d, 100*(r[0]+r[1]+r[2])/3)
		}
		out.Notes = append(out.Notes, line+" of a surface left")
	}
	far := left(p.Float("height_base")+2, 500)
	out.Notes = append(out.Notes, fmt.Sprintf("at 500 m: red %.0f%%, green %.0f%%, blue %.0f%% left (the aerial tint)",
		100*far[0], 100*far[1], 100*far[2]))
	return out, nil
}

func validateEnvironment(_ *procgen.Doc, _ *procgen.Stage, p procgen.Params) []string {
	var out []string
	if p.Str("fog_mode") == "linear" && p.Float("fog_end") <= p.Float("fog_start") {
		out = append(out, "fog_end must be beyond fog_start")
	}
	return out
}
