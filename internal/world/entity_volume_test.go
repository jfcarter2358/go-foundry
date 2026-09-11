package world

// entity_volume_test.go — volumes that ride entities, and the ones a type
// carries: the YAML, the instance diff, materialising, and containment that
// follows the carrier wherever it goes.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// volumeDef is the definition these tests instantiate: one region with every
// knob set, and one left at its defaults but hung on a bone.
func volumeDef(t *testing.T) *EntityDef {
	t.Helper()
	const src = `
id: guard
models:
  - name: body
    path: models/guard.fmdl
volumes:
  - name: sight
    shape: sphere
    offset: {x: 0, y: 1, z: 3}
    half: {x: 4, y: 2, z: 4}
    script: guard_sight.flang
    tags: [vision]
  - name: grip
    model: body
    bone: hand_r
    enabled: false
`
	var raw RawEntityDef
	if err := yaml.Unmarshal([]byte(src), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	def, err := entityDefFromRaw(raw, "guard")
	if err != nil {
		t.Fatalf("entityDefFromRaw: %v", err)
	}
	return def
}

func TestEntityVolumeDefaultsAndAuthoredFields(t *testing.T) {
	def := volumeDef(t)
	if len(def.Volumes) != 2 {
		t.Fatalf("volumes: got %d, want 2", len(def.Volumes))
	}
	sight := def.VolumeNamed("sight")
	if sight == nil || sight.Shape != VolumeSphere || sight.Half != (mgl32.Vec3{4, 2, 4}) ||
		sight.Offset != (mgl32.Vec3{0, 1, 3}) || !sight.Enabled {
		t.Fatalf("sight did not load as authored: %+v", sight)
	}
	if sight.Script != "guard_sight.flang" || !sight.HasTag("vision") {
		t.Errorf("sight lost its scripting: script %q tags %v", sight.Script, sight.Tags)
	}
	grip := def.VolumeNamed("grip")
	// A volume that says nothing about its shape is a one-unit box, and only
	// the switch it does mention is off.
	if grip == nil || grip.Shape != VolumeBox || grip.Half != (mgl32.Vec3{1, 1, 1}) || grip.Enabled {
		t.Fatalf("grip defaults: %+v", grip)
	}
	if grip.Model != "body" || grip.Bone != "hand_r" {
		t.Errorf("grip socket: %q.%q", grip.Model, grip.Bone)
	}
}

func TestEntityVolumeRoundTripsThroughTheDefinition(t *testing.T) {
	def := volumeDef(t)
	raw := entityDefToRaw(def)
	if r := raw.Volumes[1]; r.Shape != "" || r.Half != nil || r.Offset != nil {
		t.Errorf("a default-shaped volume wrote keys it did not need: %+v", r)
	}
	back, err := entityDefFromRaw(raw, "guard")
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	for i := range def.Volumes {
		a, b := def.Volumes[i], back.Volumes[i]
		if a.Name != b.Name || a.Shape != b.Shape || a.Half != b.Half || a.Offset != b.Offset ||
			a.Rotation != b.Rotation || a.Enabled != b.Enabled || a.Model != b.Model ||
			a.Bone != b.Bone || a.Script != b.Script || !stringsEqual(a.Tags, b.Tags) {
			t.Errorf("volume %d changed in the round trip:\n got %+v\nwant %+v", i, b, a)
		}
	}
}

func TestInstantiateGivesEachEntityItsOwnVolumes(t *testing.T) {
	def := volumeDef(t)
	a := def.Instantiate("guard_1")
	b := def.Instantiate("guard_2")
	a.EntityVolume("sight").Tags[0] = "changed"
	if b.EntityVolume("sight").Tags[0] != "vision" {
		t.Error("two placements share one tag slice: tagging one guard's sight tagged the other's")
	}
	if def.VolumeNamed("sight").Tags[0] != "vision" {
		t.Error("a placement wrote back into the definition every level stamps from")
	}
}

func TestDefinitionCloneOwnsItsVolumesAndEmitters(t *testing.T) {
	def := volumeDef(t)
	def.Emitters = []EntityEmitter{NewEntityEmitter("plume")}
	c := def.Clone()
	c.Volumes[0].Half = mgl32.Vec3{9, 9, 9}
	c.Emitters[0].RateScale = 7
	// The entity editor's undo snapshots are clones; one that shared these
	// arrays would be rewritten by the very edit it was taken to undo.
	if def.Volumes[0].Half == (mgl32.Vec3{9, 9, 9}) {
		t.Error("the clone shares the volume list with the original")
	}
	if def.Emitters[0].RateScale == 7 {
		t.Error("the clone shares the emitter list with the original")
	}
}

func TestUntouchedInstanceWritesNoVolumesBlock(t *testing.T) {
	def := volumeDef(t)
	e := def.Instantiate("guard_1")
	e.Type = def.Name
	if r := EntityToRaw(&e, def); len(r.Volumes) != 0 {
		t.Fatalf("an instance that changed nothing wrote %d volume override(s)", len(r.Volumes))
	}
}

func TestInstanceVolumeOverrideIsPerField(t *testing.T) {
	def := volumeDef(t)
	e := def.Instantiate("guard_1")
	e.Type = def.Name
	e.EntityVolume("sight").Half = mgl32.Vec3{8, 2, 8}

	r := EntityToRaw(&e, def)
	if len(r.Volumes) != 1 || r.Volumes[0].Name != "sight" {
		t.Fatalf("overrides: %+v, want just sight", r.Volumes)
	}
	o := r.Volumes[0]
	if o.Half == nil || o.Shape != "" || o.Offset != nil || o.Script != "" || len(o.Tags) != 0 {
		t.Errorf("the override should hold the extents and nothing else: %+v", o)
	}

	back, err := EntityFromRaw(r, func(string) (*EntityDef, error) { return def, nil })
	if err != nil {
		t.Fatalf("EntityFromRaw: %v", err)
	}
	got := back.EntityVolume("sight")
	if got == nil || got.Half != (mgl32.Vec3{8, 2, 8}) {
		t.Fatalf("the override did not come back: %+v", got)
	}
	if got.Script != "guard_sight.flang" || got.Shape != VolumeSphere {
		t.Errorf("the override wiped fields it never mentioned: %+v", got)
	}
	if back.EntityVolume("grip") == nil {
		t.Error("the untouched volume did not survive")
	}
}

func TestMaterializeAttachesVolumesToTheirEntity(t *testing.T) {
	def := volumeDef(t)
	lvl := &Level{}
	lvl.Entities = append(lvl.Entities, def.Instantiate("guard_1"))
	if n := MaterializeEntityVolumes(lvl, &lvl.Entities[0]); n != 2 {
		t.Fatalf("materialised %d, want 2", n)
	}
	sight := FindVolume(lvl, "guard_1.sight")
	if sight == nil {
		t.Fatalf("no volume called guard_1.sight; have %d volumes", len(lvl.Volumes))
	}
	if sight.Attach.Entity != "guard_1" || sight.FromEntity != "guard_1" {
		t.Errorf("attach %+v, from %q: want both naming the entity", sight.Attach, sight.FromEntity)
	}
	if sight.Position != (mgl32.Vec3{0, 1, 3}) || sight.Shape != VolumeSphere || sight.HalfX != 4 {
		t.Errorf("placement: %+v", sight)
	}
	if sight.Script != "guard_sight.flang" || !sight.HasTag("vision") {
		t.Error("the volume's scripting did not reach the placement")
	}
	grip := FindVolume(lvl, "guard_1.grip")
	if grip == nil || grip.Attach.Model != "body" || grip.Attach.Bone != "hand_r" || grip.Enabled {
		t.Errorf("grip: %+v", grip)
	}
}

func TestDropEntityVolumesTakesOnlyThatEntitys(t *testing.T) {
	def := volumeDef(t)
	lvl := &Level{}
	for _, id := range []string{"guard_1", "guard_2"} {
		lvl.Entities = append(lvl.Entities, def.Instantiate(id))
		MaterializeEntityVolumes(lvl, &lvl.Entities[len(lvl.Entities)-1])
	}
	lvl.Volumes = append(lvl.Volumes, NewVolume("door", mgl32.Vec3{}))

	if n := DropEntityVolumes(lvl, "guard_1"); n != 2 {
		t.Fatalf("dropped %d, want the 2 that guard carried", n)
	}
	if FindVolume(lvl, "guard_1.sight") != nil {
		t.Error("a volume outlived the entity that carried it")
	}
	if FindVolume(lvl, "guard_2.sight") == nil || FindVolume(lvl, "door") == nil {
		t.Error("the sweep took volumes that were not that entity's")
	}
}

// ---------- riding ----------

func TestAVolumeRidingAnEntityContainsWhereTheEntityIs(t *testing.T) {
	// npc stands ten units forward on a ship at x=100, so it is at x=110.
	lvl := attachLevel(t)
	v := NewVolume("aura", mgl32.Vec3{0, 0, 0})
	v.Attach = Attach{Entity: "npc"}
	lvl.Volumes = []Volume{v}
	vol := &lvl.Volumes[0]

	if !vol.ContainsWorldPoint(lvl, mgl32.Vec3{110, 0, 0}) {
		t.Error("the volume is not where the entity it rides is")
	}
	if vol.ContainsWorldPoint(lvl, mgl32.Vec3{0, 0, 0}) {
		t.Error("the volume answered at its local offset, read as world coordinates")
	}
	if got := vol.WorldPosition(lvl); !vecNear(got, mgl32.Vec3{110, 0, 0}, 1e-4) {
		t.Errorf("world position %v, want {110 0 0}", got)
	}
	if got := VolumesContaining(lvl, mgl32.Vec3{110, 0.5, 0}); len(got) != 1 {
		t.Errorf("VolumesContaining found %d, want the riding volume", len(got))
	}
	if lo, hi, ok := vol.WorldAABB(lvl); !ok || lo.X() > 110 || hi.X() < 110 {
		t.Errorf("world bounds %v..%v do not hold the entity it rides", lo, hi)
	}

	// And it goes where the entity goes, with nothing to update.
	FindEntity(lvl, "npc").Position = mgl32.Vec3{20, 0, 0}
	if !vol.ContainsWorldPoint(lvl, mgl32.Vec3{120, 0, 0}) {
		t.Error("the volume stayed behind when the entity moved")
	}
}

func TestVolumeFrameIsOnlyTheFrameHalfOfTheBinding(t *testing.T) {
	v := NewVolume("hold", mgl32.Vec3{})
	v.Attach = Attach{Frame: "ship"}
	if v.Frame() != "ship" {
		t.Errorf("Frame() = %q, want ship", v.Frame())
	}
	// Attach.Matrix resolves the entity first, so a volume naming both rides
	// the entity and belongs to no frame's decks.
	v.Attach.Entity = "npc"
	if v.Frame() != "" {
		t.Errorf("Frame() = %q with an entity named, want empty", v.Frame())
	}
}

func TestVolumeLegacyFrameKeyLoadsAsAttach(t *testing.T) {
	const src = `
id: ramp
frame: ship
position: {x: 0, y: 1, z: 4}
half: {x: 1, y: 1, z: 1}
`
	var raw RawVolume
	if err := yaml.Unmarshal([]byte(src), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	v, err := VolumeFromRaw(raw)
	if err != nil {
		t.Fatalf("VolumeFromRaw: %v", err)
	}
	if v.Attach != (Attach{Frame: "ship"}) {
		t.Fatalf("a level written with `frame:` loaded as %+v", v.Attach)
	}
	out := VolumeToRaw(&v)
	if out.Frame != "" || out.Attach == nil || out.Attach.Frame != "ship" {
		t.Errorf("saved as frame %q attach %+v; want the attach block only", out.Frame, out.Attach)
	}
}
