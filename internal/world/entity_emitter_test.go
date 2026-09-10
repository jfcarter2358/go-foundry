package world

// entity_emitter_test.go — the effects a type carries, through the boundaries
// that can lose them: the YAML, the instance diff, and the level a placement
// materialises into.

import (
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// emitterDef is the definition these tests instantiate: two attachments, one of
// them on a bone, so overriding by name and the socket both get exercised.
func emitterDef(t *testing.T) *EntityDef {
	t.Helper()
	const src = `
id: shuttle
models:
  - name: hull
    path: models/shuttle.fmdl
emitters:
  - name: thruster
    effect: plume
    offset: {x: 0, y: 0.4, z: -2}
    rotation: {x: 3.14159, y: 0, z: 0}
    rate_scale: 2
    tags: [engine]
  - name: torch
    effect: flame
    model: hull
    bone: mast
    emitting: false
`
	var raw RawEntityDef
	if err := yaml.Unmarshal([]byte(src), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	def, err := entityDefFromRaw(raw, "shuttle")
	if err != nil {
		t.Fatalf("entityDefFromRaw: %v", err)
	}
	return def
}

func TestEntityEmitterDefaultsAreRunning(t *testing.T) {
	def := emitterDef(t)
	if len(def.Emitters) != 2 {
		t.Fatalf("emitters: got %d, want 2", len(def.Emitters))
	}
	th := def.EmitterNamed("thruster")
	if th == nil {
		t.Fatal("thruster went missing")
	}
	// The three knobs with non-zero defaults: an attachment that mentions none
	// of them has to come back running, unscaled by anything it did not ask
	// for, and untinted.
	if !th.Enabled || !th.Emitting {
		t.Errorf("thruster: enabled=%v emitting=%v, want both true", th.Enabled, th.Emitting)
	}
	if th.EffectiveTint() != (mgl32.Vec3{1, 1, 1}) {
		t.Errorf("thruster tint: got %v, want white", th.EffectiveTint())
	}
	if th.RateScale != 2 {
		t.Errorf("thruster rate scale: got %v, want the 2 it asked for", th.RateScale)
	}
	// And one that DID say false has to keep it, which is the whole reason the
	// YAML field is a pointer.
	if tc := def.EmitterNamed("torch"); tc == nil || tc.Emitting {
		t.Error("torch: emitting:false did not survive the load")
	}
}

func TestEntityEmitterRoundTripsEveryField(t *testing.T) {
	want := EntityEmitter{
		Name:      "thruster",
		Effect:    "plume",
		Model:     "hull",
		Bone:      "nozzle",
		Offset:    mgl32.Vec3{1, 2, 3},
		Rotation:  mgl32.Vec3{0.1, 0.2, 0.3},
		Enabled:   false,
		Emitting:  false,
		RateScale: 0.25,
		Tint:      mgl32.Vec3{1, 0.5, 0.2},
		Tags:      []string{"engine"},
	}
	got := entityEmitterFromRaw(entityEmitterToRaw(&want))
	if got.Name != want.Name || got.Effect != want.Effect || got.Model != want.Model ||
		got.Bone != want.Bone || got.Offset != want.Offset || got.Rotation != want.Rotation ||
		got.Enabled != want.Enabled || got.Emitting != want.Emitting ||
		got.RateScale != want.RateScale || got.Tint != want.Tint ||
		!stringsEqual(got.Tags, want.Tags) {
		t.Fatalf("emitter changed across a round trip:\n got %+v\nwant %+v", got, want)
	}
}

func TestDefaultEntityEmitterWritesNoOptionalKeys(t *testing.T) {
	m := NewEntityEmitter("thruster")
	m.Effect = "plume"
	out, err := yaml.Marshal(entityEmitterToRaw(&m))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text := string(out)
	for _, key := range []string{
		"enabled:", "emitting:", "rate_scale:", "tint:", "offset:", "rotation:",
		"model:", "bone:", "tags:",
	} {
		if strings.Contains(text, key) {
			t.Errorf("a freshly added emitter wrote %q; a default must be silence:\n%s", key, text)
		}
	}
}

func TestInstantiateGivesEachEntityItsOwnEmitters(t *testing.T) {
	def := emitterDef(t)
	a := def.Instantiate("shuttle_1")
	b := def.Instantiate("shuttle_2")
	if len(a.Emitters) != 2 || len(b.Emitters) != 2 {
		t.Fatalf("emitters: got %d and %d, want 2 each", len(a.Emitters), len(b.Emitters))
	}
	a.EntityEmitter("thruster").Tags[0] = "changed"
	if b.EntityEmitter("thruster").Tags[0] != "engine" {
		t.Error("two placements share one tag slice: tagging one ship's plume tagged the other's")
	}
	if def.EmitterNamed("thruster").Tags[0] != "engine" {
		t.Error("a placement wrote back into the definition every level stamps from")
	}
}

func TestUntouchedInstanceWritesNoEmittersBlock(t *testing.T) {
	def := emitterDef(t)
	e := def.Instantiate("shuttle_1")
	e.Type = def.Name
	r := EntityToRaw(&e, def)
	if len(r.Emitters) != 0 {
		t.Fatalf("an instance that changed nothing wrote %d emitter override(s); it must keep tracking the type",
			len(r.Emitters))
	}
}

func TestInstanceEmitterOverrideIsPerFieldAndPerName(t *testing.T) {
	def := emitterDef(t)
	e := def.Instantiate("shuttle_1")
	e.Type = def.Name
	e.EntityEmitter("thruster").RateScale = 0.5

	r := EntityToRaw(&e, def)
	if len(r.Emitters) != 1 {
		t.Fatalf("overrides written: got %d, want just the one that changed", len(r.Emitters))
	}
	o := r.Emitters[0]
	if o.Name != "thruster" {
		t.Fatalf("override names %q, want thruster", o.Name)
	}
	if o.RateScale == nil || *o.RateScale != 0.5 {
		t.Errorf("rate scale not written: %+v", o)
	}
	if o.Effect != "" || o.Offset != nil || o.Rotation != nil || o.Enabled != nil {
		t.Errorf("an override restated fields it did not change: %+v", o)
	}

	// And back the other way: the override applies to that attachment and
	// leaves the rest of the type's alone.
	back, err := EntityFromRaw(r, func(string) (*EntityDef, error) { return def, nil })
	if err != nil {
		t.Fatalf("EntityFromRaw: %v", err)
	}
	if got := back.EntityEmitter("thruster"); got == nil || got.RateScale != 0.5 {
		t.Errorf("the override did not come back: %+v", got)
	}
	if got := back.EntityEmitter("thruster"); got == nil || got.Effect != "plume" {
		t.Error("the override wiped a field it never mentioned")
	}
	if got := back.EntityEmitter("torch"); got == nil || got.Bone != "mast" {
		t.Errorf("the untouched attachment did not survive: %+v", got)
	}
}

func TestInstanceCanAddAnEmitterTheTypeDoesNotHave(t *testing.T) {
	def := emitterDef(t)
	e := def.Instantiate("shuttle_1")
	e.Type = def.Name
	e.Emitters = append(e.Emitters, EntityEmitter{
		Name: "leak", Effect: "smoke", Enabled: true, Emitting: true, RateScale: 1,
	})

	r := EntityToRaw(&e, def)
	if len(r.Emitters) != 1 || r.Emitters[0].Name != "leak" || r.Emitters[0].Effect != "smoke" {
		t.Fatalf("an added attachment must be written in full, since nothing in the type would restore it: %+v", r.Emitters)
	}
	back, err := EntityFromRaw(r, func(string) (*EntityDef, error) { return def, nil })
	if err != nil {
		t.Fatalf("EntityFromRaw: %v", err)
	}
	if len(back.Emitters) != 3 {
		t.Fatalf("emitters after the round trip: got %d, want the type's two plus the added one", len(back.Emitters))
	}
}

// ---------- materialising ----------

func TestMaterializeAttachesEmittersToTheirEntity(t *testing.T) {
	def := emitterDef(t)
	lvl := &Level{}
	e := def.Instantiate("shuttle_1")
	lvl.Entities = append(lvl.Entities, e)

	// The .fpar documents do not exist under a test's working directory, which
	// is the point: a missing effect is reported and the placement kept.
	_ = MaterializeEntityEmitters(lvl, &lvl.Entities[0], nil)
	if len(lvl.Emitters) != 2 {
		t.Fatalf("emitters in the level: got %d, want 2", len(lvl.Emitters))
	}
	th := FindEmitter(lvl, "shuttle_1.thruster")
	if th == nil {
		t.Fatalf("no emitter called shuttle_1.thruster; got %q and %q",
			lvl.Emitters[0].ID, lvl.Emitters[1].ID)
	}
	if th.Attach.Entity != "shuttle_1" {
		t.Errorf("attach: got %+v, want the entity it rides", th.Attach)
	}
	if th.FromEntity != "shuttle_1" {
		t.Errorf("FromEntity: got %q; saving and the drop sweep both read it", th.FromEntity)
	}
	if th.Position != (mgl32.Vec3{0, 0.4, -2}) {
		t.Errorf("position: got %v, want the authored offset, in the entity's space", th.Position)
	}
	if !th.HasTag("engine") {
		t.Error("the attachment's tags did not reach the placement, so #engine reaches nothing")
	}
	tc := FindEmitter(lvl, "shuttle_1.torch")
	if tc == nil || tc.Attach.Bone != "mast" || tc.Attach.Model != "hull" {
		t.Errorf("the bone socket did not survive materialising: %+v", tc)
	}
}

func TestTwoPlacementsOfOneTypeGetDistinctEmitterIDs(t *testing.T) {
	def := emitterDef(t)
	lvl := &Level{}
	for _, id := range []string{"shuttle_1", "shuttle_2"} {
		e := def.Instantiate(id)
		lvl.Entities = append(lvl.Entities, e)
		_ = MaterializeEntityEmitters(lvl, &lvl.Entities[len(lvl.Entities)-1], nil)
	}
	seen := map[string]bool{}
	for i := range lvl.Emitters {
		if seen[lvl.Emitters[i].ID] {
			t.Fatalf("two emitters called %q: `@id` would name neither of them", lvl.Emitters[i].ID)
		}
		seen[lvl.Emitters[i].ID] = true
	}
	if len(seen) != 4 {
		t.Fatalf("emitters: got %d, want two per ship", len(seen))
	}
}

func TestDropEntityEmittersTakesOnlyThatEntitys(t *testing.T) {
	def := emitterDef(t)
	lvl := &Level{}
	for _, id := range []string{"shuttle_1", "shuttle_2"} {
		e := def.Instantiate(id)
		lvl.Entities = append(lvl.Entities, e)
		_ = MaterializeEntityEmitters(lvl, &lvl.Entities[len(lvl.Entities)-1], nil)
	}
	// One the level itself placed, which must survive: it is not anybody's
	// content but the level's.
	lvl.Emitters = append(lvl.Emitters, NewEmitter("hall_torch", mgl32.Vec3{}))

	if n := DropEntityEmitters(lvl, "shuttle_1"); n != 2 {
		t.Fatalf("dropped %d, want the 2 that ship carried", n)
	}
	if FindEmitter(lvl, "shuttle_1.thruster") != nil {
		t.Error("an emitter outlived the entity that carried it")
	}
	if FindEmitter(lvl, "shuttle_2.thruster") == nil {
		t.Error("the sweep took the other ship's emitters too")
	}
	if FindEmitter(lvl, "hall_torch") == nil {
		t.Error("the sweep took an emitter the level placed")
	}
	if n := DropEntityEmitters(lvl, "nobody"); n != 0 {
		t.Errorf("dropped %d for an entity that carries nothing", n)
	}
}
