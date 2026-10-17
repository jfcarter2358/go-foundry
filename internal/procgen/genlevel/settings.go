package genlevel

// settings.go — the level-wide settings a generator owns: the environment
// stage's fog, sky, view distance, sun and ambient (procgen.LevelSettings).
//
// Placements are spliced by ID prefix, so a generator owns exactly the
// entries it made. A level has one fog, one sky and one view distance, so
// there is nothing to prefix: a generator whose run sets one of those keys
// owns that whole setting, and each splice writes it again. The sun is the
// level's FIRST directional light — its angles, colour and brightness are
// set, everything else about it (its ID, its script, its bake switches) is
// left as authored, and a level with no directional light gets one. The
// ambient is the global light's colour and brightness, the same way.
//
// A stage that stops setting a key (or is removed) leaves the level with the
// last value it wrote: unlike a placement, a setting has no prefix to say it
// was generated. The guide says so.
//
// Every value goes through the level's own YAML schema, strictly, as a
// volume entry does: a key the level format does not have is an error, not a
// setting that silently does nothing.

import (
	"fmt"
	"sort"

	"foundry/internal/procgen"
	"foundry/internal/world"
	"gopkg.in/yaml.v2"
)

// SettingKeys are the level keys a generator may own.
var SettingKeys = []string{"fog", "sky", "view_distance", "sun", "ambient", "tonemap", "spawn"}

// rawSun is the "sun" setting: the first directional light's look.
type rawSun struct {
	Yaw            *float32        `yaml:"yaw,omitempty"`   // degrees about +Y; 0 = toward +X
	Pitch          *float32        `yaml:"pitch,omitempty"` // degrees; -90 straight down
	Color          *world.RawColor `yaml:"color,omitempty"`
	Brightness     *float32        `yaml:"brightness,omitempty"`
	CastShadows    *bool           `yaml:"cast_shadows,omitempty"`
	ShadowStrength *float32        `yaml:"shadow_strength,omitempty"`
}

// rawAmbient is the "ambient" setting: the global light's colour and level.
type rawAmbient struct {
	Color      *world.RawColor `yaml:"color,omitempty"`
	Brightness *float32        `yaml:"brightness,omitempty"`
}

// decodeStrict re-reads a loose YAML value into out through the schema.
func decodeStrict(key string, v interface{}, out interface{}) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("level setting %s: %w", key, err)
	}
	if err := yaml.UnmarshalStrict(data, out); err != nil {
		return fmt.Errorf("level setting %s: %w", key, err)
	}
	return nil
}

// SpliceSettings writes settings into raw, and returns the keys it set, in
// SettingKeys order. Every key is decoded before any is written, so a bad one
// changes nothing.
func SpliceSettings(raw *world.RawLevel, settings procgen.LevelSettings) ([]string, error) {
	if len(settings) == 0 {
		return nil, nil
	}
	known := map[string]bool{}
	for _, k := range SettingKeys {
		known[k] = true
	}
	var unknown []string
	for k := range settings {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("level settings %v: a generator may set only %v", unknown, SettingKeys)
	}

	var (
		fog     *world.RawFog
		sky     *world.RawSky
		view    float32
		sun     *rawSun
		ambient *rawAmbient
		tone    *world.RawTonemap
		spawn   *world.RawSpawn
	)
	if v, ok := settings["spawn"]; ok {
		spawn = &world.RawSpawn{}
		if err := decodeStrict("spawn", v, spawn); err != nil {
			return nil, err
		}
	}
	if v, ok := settings["tonemap"]; ok {
		tone = &world.RawTonemap{}
		if err := decodeStrict("tonemap", v, tone); err != nil {
			return nil, err
		}
	}
	if v, ok := settings["fog"]; ok {
		fog = &world.RawFog{}
		if err := decodeStrict("fog", v, fog); err != nil {
			return nil, err
		}
	}
	if v, ok := settings["sky"]; ok {
		sky = &world.RawSky{}
		if err := decodeStrict("sky", v, sky); err != nil {
			return nil, err
		}
	}
	if v, ok := settings["view_distance"]; ok {
		if err := decodeStrict("view_distance", v, &view); err != nil {
			return nil, err
		}
	}
	if v, ok := settings["sun"]; ok {
		sun = &rawSun{}
		if err := decodeStrict("sun", v, sun); err != nil {
			return nil, err
		}
	}
	if v, ok := settings["ambient"]; ok {
		ambient = &rawAmbient{}
		if err := decodeStrict("ambient", v, ambient); err != nil {
			return nil, err
		}
	}

	var set []string
	for _, k := range SettingKeys {
		if _, ok := settings[k]; ok {
			set = append(set, k)
		}
	}
	if fog != nil {
		raw.Fog = fog
	}
	if sky != nil {
		raw.Sky = sky
	}
	if tone != nil {
		raw.Tonemap = tone
	}
	if spawn != nil {
		raw.Spawn = spawn
	}
	if _, ok := settings["view_distance"]; ok {
		raw.ViewDistance = view
	}
	if sun != nil {
		applySun(&raw.Lights, sun)
	}
	if ambient != nil {
		if ambient.Color != nil {
			raw.Lights.Global.Color = *ambient.Color
		}
		if ambient.Brightness != nil {
			raw.Lights.Global.Brightness = *ambient.Brightness
		}
	}
	return set, nil
}

// applySun sets the first directional light's look, making one if there is
// none.
func applySun(l *world.RawLights, s *rawSun) {
	if len(l.Directional) == 0 {
		yaw, pitch := world.DefaultDirLightYaw, world.DefaultDirLightPitch
		l.Directional = append(l.Directional, world.RawDirLight{ID: "sun", Yaw: &yaw, Pitch: &pitch,
			Color: world.RawColor{R: 1, G: 1, B: 1}, Brightness: 1, CastShadows: true, ShadowStrength: 0.7,
			Dynamic: true})
	}
	d := &l.Directional[0]
	if s.Yaw != nil {
		y := *s.Yaw
		d.Yaw = &y
	}
	if s.Pitch != nil {
		p := *s.Pitch
		d.Pitch = &p
	}
	if d.Yaw != nil && d.Pitch != nil {
		dl := world.DirectionalLight{Yaw: *d.Yaw, Pitch: *d.Pitch}
		dl.SyncDirection()
		d.Direction.X, d.Direction.Y, d.Direction.Z = dl.Direction[0], dl.Direction[1], dl.Direction[2]
	}
	if s.Color != nil {
		d.Color = *s.Color
	}
	if s.Brightness != nil {
		d.Brightness = *s.Brightness
	}
	if s.CastShadows != nil {
		d.CastShadows = *s.CastShadows
	}
	if s.ShadowStrength != nil {
		d.ShadowStrength = *s.ShadowStrength
	}
}
