package world

// cloud_clear_test.go — Volume.ClearsClouds: the key, on a level's volume and
// on one a type carries, and the switches that decide whether it clears now.

import (
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

func TestLevelVolumeClearsCloudsRoundTrips(t *testing.T) {
	var raw RawVolume
	src := "id: hold\nhalf: {x: 2, y: 1, z: 3}\nclears_clouds: true\n"
	if err := yaml.Unmarshal([]byte(src), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	v, err := VolumeFromRaw(raw)
	if err != nil {
		t.Fatalf("from raw: %v", err)
	}
	if !v.ClearsClouds {
		t.Fatal("clears_clouds: true loaded as false")
	}

	data, err := yaml.Marshal(VolumeToRaw(&v))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), "clears_clouds: true") {
		t.Errorf("the saved volume lost the key:\n%s", data)
	}
	var again RawVolume
	if err := yaml.Unmarshal(data, &again); err != nil {
		t.Fatalf("unmarshal saved: %v", err)
	}
	if back, _ := VolumeFromRaw(again); !back.ClearsClouds {
		t.Errorf("the flag did not survive a save and reload:\n%s", data)
	}

	// And a volume that never asked writes nothing, so re-saving a level does
	// not sprinkle `clears_clouds: false` over every trigger in it.
	plain := NewVolume("trigger", mgl32.Vec3{})
	data, _ = yaml.Marshal(VolumeToRaw(&plain))
	if strings.Contains(string(data), "clears_clouds") {
		t.Errorf("a plain trigger wrote the key:\n%s", data)
	}
}

// The case the flag exists for: a ship type's hold, carried onto every
// placement, riding the ship.
func TestEntityVolumeClearsCloudsMaterialises(t *testing.T) {
	const src = `
id: dropship
volumes:
  - name: hold
    half: {x: 3, y: 2, z: 5}
    clears_clouds: true
  - name: ramp
`
	var raw RawEntityDef
	if err := yaml.Unmarshal([]byte(src), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	def, err := entityDefFromRaw(raw, "dropship")
	if err != nil {
		t.Fatalf("entityDefFromRaw: %v", err)
	}
	if !def.VolumeNamed("hold").ClearsClouds || def.VolumeNamed("ramp").ClearsClouds {
		t.Fatalf("hold %v, ramp %v: want only the hold clearing",
			def.VolumeNamed("hold").ClearsClouds, def.VolumeNamed("ramp").ClearsClouds)
	}

	// Through the definition's own save.
	back, err := entityDefFromRaw(entityDefToRaw(def), "dropship")
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if !back.VolumeNamed("hold").ClearsClouds || back.VolumeNamed("ramp").ClearsClouds {
		t.Error("the flag changed across the definition's round trip")
	}
	if r := entityDefToRaw(def).Volumes[1]; r.ClearsClouds != nil {
		t.Errorf("a volume that does not clear wrote the key: %+v", r)
	}

	lvl := &Level{}
	lvl.Entities = append(lvl.Entities, def.Instantiate("ship_1"))
	MaterializeEntityVolumes(lvl, &lvl.Entities[0])
	hold := FindVolume(lvl, "ship_1.hold")
	if hold == nil {
		t.Fatalf("no ship_1.hold; have %d volumes", len(lvl.Volumes))
	}
	if !hold.ClearsClouds || !hold.ClearsCloudsNow() || hold.Attach.Entity != "ship_1" {
		t.Errorf("the placed hold: clears %v, now %v, attach %+v", hold.ClearsClouds, hold.ClearsCloudsNow(), hold.Attach)
	}
	if ramp := FindVolume(lvl, "ship_1.ramp"); ramp == nil || ramp.ClearsClouds {
		t.Errorf("the ramp: %+v", ramp)
	}
}

// An instance can switch a type's clearing off, and only that is written.
func TestInstanceCanOverrideClearsClouds(t *testing.T) {
	const src = `
id: dropship
volumes:
  - name: hold
    clears_clouds: true
`
	var raw RawEntityDef
	if err := yaml.Unmarshal([]byte(src), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	def, err := entityDefFromRaw(raw, "dropship")
	if err != nil {
		t.Fatalf("entityDefFromRaw: %v", err)
	}
	e := def.Instantiate("ship_1")
	e.Type = def.Name
	if r := EntityToRaw(&e, def); len(r.Volumes) != 0 {
		t.Fatalf("an untouched ship wrote %d volume override(s)", len(r.Volumes))
	}

	e.EntityVolume("hold").ClearsClouds = false
	r := EntityToRaw(&e, def)
	if len(r.Volumes) != 1 || r.Volumes[0].ClearsClouds == nil || *r.Volumes[0].ClearsClouds {
		t.Fatalf("override: %+v, want clears_clouds: false and nothing else", r.Volumes)
	}
	back, err := EntityFromRaw(r, func(string) (*EntityDef, error) { return def, nil })
	if err != nil {
		t.Fatalf("EntityFromRaw: %v", err)
	}
	if back.EntityVolume("hold").ClearsClouds {
		t.Error("the instance's false did not beat the type's true")
	}
}

func TestClearsCloudsNowNeedsAnEnabledRegion(t *testing.T) {
	v := NewVolume("hold", mgl32.Vec3{})
	if v.ClearsCloudsNow() {
		t.Error("a volume without the flag clears cloud")
	}
	v.ClearsClouds = true
	if !v.ClearsCloudsNow() {
		t.Fatal("an enabled, bounded volume with the flag does not clear cloud")
	}
	v.Enabled = false
	if v.ClearsCloudsNow() {
		t.Error("a disabled volume still clears cloud")
	}
	v.Enabled, v.HalfY = true, 0
	if v.ClearsCloudsNow() {
		t.Error("a volume with no extent clears cloud")
	}
}
