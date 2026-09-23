package world

import (
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// contextTestLevel is a room with walls, a lamp, a crate and a trigger in it,
// and a second room outside the region that must not be gathered.
func contextTestLevel() *Level {
	box := func(id string, cx, cy, cz, h float32, tex string) Brush {
		b := Brush{ID: id}
		for _, s := range [][3]float32{
			{-1, -1, -1}, {1, -1, -1}, {1, 1, -1}, {-1, 1, -1},
			{-1, -1, 1}, {1, -1, 1}, {1, 1, 1}, {-1, 1, 1},
		} {
			b.Verts = append(b.Verts, mgl32.Vec3{cx + s[0]*h, cy + s[1]*h, cz + s[2]*h})
		}
		b.Faces = []BrushFace{{Indices: []int{0, 1, 2, 3}, Texture: tex}}
		return b
	}
	lvl := &Level{
		Chunks: []Chunk{{ID: "deck", Brushes: []Brush{
			box("mess_floor", 0, 0, 0, 4, "sf_deck"),
			box("mess_wall_n", 0, 2, -4, 4, "sf_panel"),
			box("galley_floor", 40, 0, 0, 4, "sf_deck"), // the next room along
		}}},
		Entities: []Entity{
			{ID: "crate_01", Type: "crate", Position: mgl32.Vec3{1, 1, 1}, Tags: []string{"loot"}},
			{ID: "far_crate", Type: "crate", Position: mgl32.Vec3{40, 1, 0}},
		},
		Sounds: []Sound{{ID: "hum", Clip: "vent_hum", Position: mgl32.Vec3{0, 2, 0}}},
		Volumes: []Volume{
			{
				ID: "mess_hall", Enabled: true, Shape: VolumeBox,
				Position: mgl32.Vec3{0, 2, 0}, HalfX: 8, HalfY: 6, HalfZ: 8,
				Context: &VolumeContext{Note: "the crew's mess — lived-in"},
			},
			{ID: "mess_door", Enabled: true, Position: mgl32.Vec3{0, 1, 6}, HalfX: 1, HalfY: 2, HalfZ: 1,
				ScriptData: ScriptData{Script: "door.flang"}},
		},
	}
	lvl.Lights.Points = []PointLight{
		{ID: "mess_lamp", Position: mgl32.Vec3{0, 4, 0}, Color: mgl32.Vec3{1, 0.8, 0.6}, Radius: 10},
		{ID: "galley_lamp", Position: mgl32.Vec3{40, 4, 0}},
	}
	return lvl
}

func TestGatherContextFindsWhatIsInTheRoomAndNothingElse(t *testing.T) {
	lvl := contextTestLevel()
	v := FindContextVolume(lvl, "mess_hall")
	if v == nil {
		t.Fatal("the context volume was not found")
	}
	c := GatherContext(lvl, v)

	ids := map[string]bool{}
	for _, it := range c.Items {
		ids[it.ID] = true
	}
	for _, want := range []string{"mess_floor", "mess_wall_n", "crate_01", "mess_lamp", "hum", "mess_door"} {
		if !ids[want] {
			t.Errorf("%s is in the room but was not gathered:\n%s", want, c.Describe())
		}
	}
	for _, unwanted := range []string{"galley_floor", "far_crate", "galley_lamp"} {
		if ids[unwanted] {
			t.Errorf("%s is in the next room and should not have been gathered", unwanted)
		}
	}
	// The region itself is not one of its own contents.
	if ids["mess_hall"] {
		t.Error("the context volume gathered itself")
	}
}

func TestGatherContextNamesTheTexturesAndThePlaces(t *testing.T) {
	lvl := contextTestLevel()
	got := GatherContext(lvl, FindContextVolume(lvl, "mess_hall")).Describe()

	// The textures are most of what "the style of this room" means.
	if !strings.Contains(got, "sf_panel") || !strings.Contains(got, "sf_deck") {
		t.Errorf("the description does not name the textures:\n%s", got)
	}
	// The note the author wrote, and the bounds.
	if !strings.Contains(got, "lived-in") {
		t.Errorf("the author's note is missing:\n%s", got)
	}
	if !strings.Contains(got, "world bounds") {
		t.Errorf("the region's bounds are missing:\n%s", got)
	}
	// Entities keep their type and tags: "the crates" is how a request names them.
	if !strings.Contains(got, "crate #loot") {
		t.Errorf("the entity's type and tags are missing:\n%s", got)
	}
	if !strings.Contains(got, "script door.flang") {
		t.Errorf("the trigger inside the room is not described:\n%s", got)
	}
}

func TestADisabledContextVolumeStillDescribesItsRegion(t *testing.T) {
	// Enabled is about triggers firing. This one never fires, and an author who
	// switched it off should not find it silently empty.
	lvl := contextTestLevel()
	v := FindContextVolume(lvl, "mess_hall")
	v.Enabled = false
	if n := len(GatherContext(lvl, v).Items); n == 0 {
		t.Error("a disabled context volume gathered nothing")
	}
}

func TestAContextVolumeWithNoSizeSaysSo(t *testing.T) {
	lvl := contextTestLevel()
	v := FindContextVolume(lvl, "mess_hall")
	v.HalfX, v.HalfY, v.HalfZ = 0, 0, 0
	got := GatherContext(lvl, v).Describe()
	if !strings.Contains(got, "no extents") {
		t.Errorf("an unbounded volume said %q, want it to explain itself", got)
	}
}

func TestOnlyContextVolumesResolve(t *testing.T) {
	lvl := contextTestLevel()
	if FindContextVolume(lvl, "mess_door") != nil {
		t.Error("an ordinary trigger resolved as a context volume")
	}
	// A prompt writes the @ that a script would.
	if FindContextVolume(lvl, "@mess_hall") == nil {
		t.Error("a leading @ should be accepted, since that is how it is written")
	}
	if got := ContextVolumes(lvl); len(got) != 1 || got[0].ID != "mess_hall" {
		t.Errorf("ContextVolumes = %v, want just the one", got)
	}
}

func TestContextBlockRoundTrips(t *testing.T) {
	v := Volume{ID: "mess_hall", Enabled: true, HalfX: 1, HalfY: 1, HalfZ: 1,
		Context: &VolumeContext{Note: "the crew's mess"}}
	back, err := VolumeFromRaw(VolumeToRaw(&v))
	if err != nil {
		t.Fatal(err)
	}
	if back.Context == nil || back.Context.Note != "the crew's mess" {
		t.Errorf("context = %+v, want it round-tripped", back.Context)
	}
	// A volume that never had one must not acquire one by being saved.
	plain := Volume{ID: "trigger", Enabled: true, HalfX: 1, HalfY: 1, HalfZ: 1}
	if raw := VolumeToRaw(&plain); raw.Context != nil {
		t.Error("an ordinary volume wrote a context block")
	}
}

func TestKindsArePluralisedProperly(t *testing.T) {
	// A description is read by a person as often as by a model.
	for kind, want := range map[string]string{
		"brush": "brushes", "entity": "entities", "light": "lights",
		"sound": "sounds", "volume": "volumes", "sprite": "sprites",
	} {
		if got := pluralKind(kind); got != want {
			t.Errorf("pluralKind(%q) = %q, want %q", kind, got, want)
		}
	}
	lvl := contextTestLevel()
	got := GatherContext(lvl, FindContextVolume(lvl, "mess_hall")).Describe()
	for _, bad := range []string{"Brushs", "Entitys", "entitys", "brushs"} {
		if strings.Contains(got, bad) {
			t.Errorf("the description contains %q:\n%s", bad, got)
		}
	}
	if !strings.Contains(got, "Brushes:") || !strings.Contains(got, "2 brushes") {
		t.Errorf("headings or counts read wrong:\n%s", got)
	}
}

func TestContextLoadsFromHandWrittenYAML(t *testing.T) {
	// The block is small enough that it will be typed as often as it is ticked.
	var raw RawVolume
	if err := yaml.Unmarshal([]byte("id: bridge\nhalf: {x: 4, y: 3, z: 4}\ncontext:\n  note: the bridge\n"), &raw); err != nil {
		t.Fatal(err)
	}
	got, err := VolumeFromRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Context == nil || got.Context.Note != "the bridge" {
		t.Fatalf("context = %+v, want the note", got.Context)
	}

	// An empty block is the ordinary case — a named place with nothing said
	// about it — and must still mark the volume.
	if err := yaml.Unmarshal([]byte("id: hold\nhalf: {x: 1, y: 1, z: 1}\ncontext: {}\n"), &raw); err != nil {
		t.Fatal(err)
	}
	got, _ = VolumeFromRaw(raw)
	if !got.IsContext() {
		t.Error("`context: {}` did not mark the volume")
	}

	// And a volume that never had one writes no key, so every level file in the
	// project round-trips unchanged.
	plain := Volume{ID: "trigger", Enabled: true, HalfX: 1, HalfY: 1, HalfZ: 1}
	out, err := yaml.Marshal(VolumeToRaw(&plain))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "context") {
		t.Errorf("an ordinary volume wrote a context key:\n%s", out)
	}
}
