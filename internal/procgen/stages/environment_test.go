package stages

import (
	"context"
	"math"
	"strings"
	"testing"

	"foundry/internal/procgen"
	"foundry/internal/procgen/genlevel"
	"foundry/internal/world"
)

// The environment stage end to end: its settings ride the run's result and its
// placements file, and the splice turns them into a level whose fog has the
// desert defaults' atmosphere, whose sky is the canyon box and whose sun is
// set — and the hand-made sun keeps its ID.
func TestEnvironmentStageSetsTheLevelsAir(t *testing.T) {
	d, err := procgen.DecodeDoc([]byte(`doc: env
seed: 1
stages:
  - stage: environment
    id: air
    params: {dust: true}
`))
	if err != nil {
		t.Fatal(err)
	}
	d.Name = "env"
	dir := t.TempDir()
	res, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	set := res.LevelSettings()
	for _, k := range []string{"fog", "sky", "sun"} {
		if set[k] == nil {
			t.Errorf("no %s setting", k)
		}
	}
	if _, ok := set["view_distance"]; ok {
		t.Error("view_distance 0 should leave the level's alone")
	}
	if v := res.Volumes(); len(v) != 1 || v[0]["id"] != "gen.env.air.dust" {
		t.Fatalf("dust volume: %v", v)
	}

	// A rerun with nothing changed reads the settings back from the file.
	again, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Skipped) != 1 || len(again.LevelSettings()) != len(set) {
		t.Fatalf("rerun: skipped %v, settings %v", again.Skipped, again.LevelSettings())
	}

	yaw := float32(10)
	raw := world.RawLevel{Lights: world.RawLights{Directional: []world.RawDirLight{{ID: "my_sun", Yaw: &yaw}}}}
	st, err := genlevel.SpliceResult(&raw, again, genlevel.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.String(), "settings fog sky sun") {
		t.Errorf("stats %q", st)
	}
	f := world.FogFromRaw(raw.Fog)
	if f == nil || !f.HasAtmosphere() || f.Mode != world.FogNone {
		t.Fatalf("fog %+v", f)
	}
	if !f.Height.Enabled || !f.Aerial.Enabled || !f.Sun.Enabled || f.Aerial.Distance != 1100 || f.SkyHaze != 0.6 {
		t.Errorf("atmosphere %+v %+v %+v haze %v", f.Height, f.Aerial, f.Sun, f.SkyHaze)
	}
	if raw.Sky == nil || raw.Sky.Kind != "cubemap" || len(raw.Sky.Faces) != 6 || raw.Sky.Faces[0] != "sky_canyon_px" {
		t.Errorf("sky %+v", raw.Sky)
	}
	sun := raw.Lights.Directional[0]
	if sun.ID != "my_sun" || sun.Yaw == nil || *sun.Yaw != 50 || *sun.Pitch != -30 || sun.Brightness != 1 {
		t.Errorf("sun %+v", sun)
	}
	// Direction follows the angles: yaw 50, pitch -30.
	want := [3]float64{math.Cos(50*math.Pi/180) * math.Cos(30*math.Pi/180), -0.5, math.Sin(50*math.Pi/180) * math.Cos(30*math.Pi/180)}
	got := [3]float64{float64(sun.Direction.X), float64(sun.Direction.Y), float64(sun.Direction.Z)}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-3 {
			t.Errorf("sun direction %v, want %v", got, want)
			break
		}
	}
	if len(raw.Volumes) != 1 || raw.Volumes[0].Cloud == nil || raw.Volumes[0].Cloud.Preset != "dust_haze" {
		t.Fatalf("dust volume in the level: %+v", raw.Volumes)
	}
	if c := world.VolumeCloudFromRaw(raw.Volumes[0].Cloud); c.CastShadows || c.Density != 0.006 {
		t.Errorf("dust cloud %+v", c)
	}
}

// A setting the level format cannot hold fails the splice before anything is
// written.
func TestLevelSettingsAreDecodedStrictly(t *testing.T) {
	raw := world.RawLevel{}
	_, err := genlevel.SpliceRaw(&raw, "g", nil, nil, genlevel.Options{Settings: procgen.LevelSettings{
		"fog": map[string]interface{}{"mode": "none", "hieght": map[string]interface{}{}},
	}})
	if err == nil || raw.Fog != nil {
		t.Fatalf("a misspelt fog key was accepted (err %v, fog %+v)", err, raw.Fog)
	}
	_, err = genlevel.SpliceRaw(&raw, "g", nil, nil, genlevel.Options{Settings: procgen.LevelSettings{"music": "x"}})
	if err == nil {
		t.Fatal("an unknown level key was accepted")
	}
	// No sun in the level: one is made.
	if _, err := genlevel.SpliceRaw(&raw, "g", nil, nil, genlevel.Options{Settings: procgen.LevelSettings{
		"sun": map[string]interface{}{"pitch": -45.0}}}); err != nil {
		t.Fatal(err)
	}
	if len(raw.Lights.Directional) != 1 || *raw.Lights.Directional[0].Pitch != -45 {
		t.Errorf("made sun %+v", raw.Lights.Directional)
	}
}
