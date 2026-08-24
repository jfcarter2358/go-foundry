package world

import (
	"fmt"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// testDef is the definition every test here instantiates: two attachments so
// per-model overriding is actually exercised, and attributes and tags so the
// merge rules are.
func testDef(t *testing.T) *EntityDef {
	t.Helper()
	const src = `
id: barrel
description: A barrel.
tags: [prop, breakable]
bounding: cylinder
bound_half_x: 0.4
bound_half_y: 0.5
bound_half_z: 0.4
bound_offset: {x: 0, y: 0.5, z: 0}
models:
  - name: body
    path: models/barrel.fmdl
    texture: wood
    offset: {x: 0, y: 0.5, z: 0}
    animations:
      - name: idle
        clip: idle_cycle
        loop: true
        autoplay: true
      - name: break
        clip: shatter
        speed: 1.5
  - name: lid
    path: models/lid.fmdl
    offset: {x: 0, y: 1.0, z: 0}
attributes:
  health: {type: int, value: 25}
  label:  {type: str, value: barrel}
`
	var raw RawEntityDef
	if err := yaml.Unmarshal([]byte(src), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	def, err := entityDefFromRaw(raw, "barrel")
	if err != nil {
		t.Fatalf("entityDefFromRaw: %v", err)
	}
	return def
}

// lookup resolves a single definition, standing in for the on-disk registry so
// these tests never touch the filesystem.
func lookup(def *EntityDef) func(string) (*EntityDef, error) {
	return func(name string) (*EntityDef, error) {
		if name == def.Name {
			return def, nil
		}
		return nil, fmt.Errorf("unknown entity type %q", name)
	}
}

func TestEntityDefParsesModelsAndAnimations(t *testing.T) {
	def := testDef(t)

	if def.BoundingKind != BoundingCylinder {
		t.Errorf("bounding: got %v, want cylinder", def.BoundingKind)
	}
	if def.BoundOffset != (mgl32.Vec3{0, 0.5, 0}) {
		t.Errorf("bound offset: got %v", def.BoundOffset)
	}
	// Absent `visible:` means visible — a definition that had to say so would
	// make every file noisier for the sake of the rare hidden one.
	if !def.Visible {
		t.Error("visible defaulted to false")
	}
	if len(def.Models) != 2 {
		t.Fatalf("got %d models, want 2", len(def.Models))
	}

	body := def.Models[0]
	if body.Name != "body" || body.Texture != "wood" {
		t.Errorf("body: got %+v", body)
	}
	if len(body.Animations) != 2 {
		t.Fatalf("got %d animations, want 2", len(body.Animations))
	}
	if got := body.Animations[0]; got.Name != "idle" || got.EffectiveClip() != "idle_cycle" || !got.Autoplay {
		t.Errorf("idle: got %+v", got)
	}
	// An animation that names no clip plays the clip of the same name, and one
	// that names no speed plays at the authored rate.
	if got := body.Animations[1].EffectiveSpeed(); got != 1.5 {
		t.Errorf("break speed: got %v, want 1.5", got)
	}
	if got := def.Models[1].Animations; got != nil {
		t.Errorf("lid should have no animations, got %+v", got)
	}
}

func TestEntityAnimationDefaultsToItsOwnName(t *testing.T) {
	a := EntityAnimation{Name: "walk"}
	if got := a.EffectiveClip(); got != "walk" {
		t.Errorf("got %q, want %q", got, "walk")
	}
	if got := a.EffectiveSpeed(); got != 1 {
		t.Errorf("speed: got %v, want 1", got)
	}
}

func TestInstantiateSharesNothingWithDefinition(t *testing.T) {
	def := testDef(t)
	a := def.Instantiate("barrel_01")
	b := def.Instantiate("barrel_02")

	// Editing one instance must not reach the definition or its siblings.
	a.SetAttr("health", IntAttr(999))
	a.AddTag("on_fire")
	a.Models[0].Texture = "charred"
	a.Models[0].Animations[0].Name = "renamed"

	if v := def.Attributes["health"]; v != IntAttr(25) {
		t.Errorf("definition attribute changed: %+v", v)
	}
	if v := b.Attributes["health"]; v != IntAttr(25) {
		t.Errorf("sibling attribute changed: %+v", v)
	}
	if b.HasTag("on_fire") {
		t.Error("sibling gained a tag")
	}
	if def.Models[0].Texture != "wood" || b.Models[0].Texture != "wood" {
		t.Error("model texture leaked out of the instance")
	}
	if def.Models[0].Animations[0].Name != "idle" || b.Models[0].Animations[0].Name != "idle" {
		t.Error("animation list is shared between instances")
	}
}

func TestEntityFromRawAppliesOverrides(t *testing.T) {
	def := testDef(t)
	raw := RawEntity{
		Type:            "barrel",
		ID:              "barrel_01",
		Position:        RawVec3{X: 4, Y: 0, Z: 2},
		Rotation:        RawVec3{Y: 1.5},
		Velocity:        RawVec3{X: 1},
		AngularVelocity: RawVec3{Y: 0.5},
		Tags:            []string{"quest_item"},
		Attributes: map[string]RawAttribute{
			"health": {Type: "int", Value: 50},
			"owner":  {Type: "str", Value: "player"},
		},
		Models: []RawEntityModel{{
			Name:    "lid",
			Texture: "gold",
			Offset:  &RawVec3{X: 0, Y: 1.2, Z: 0},
		}},
	}

	e, err := EntityFromRaw(raw, lookup(def))
	if err != nil {
		t.Fatalf("EntityFromRaw: %v", err)
	}

	if e.Position != (mgl32.Vec3{4, 0, 2}) || e.Rotation != (mgl32.Vec3{0, 1.5, 0}) {
		t.Errorf("transform: %v %v", e.Position, e.Rotation)
	}
	if e.Velocity != (mgl32.Vec3{1, 0, 0}) || e.AngularVelocity != (mgl32.Vec3{0, 0.5, 0}) {
		t.Errorf("motion: %v %v", e.Velocity, e.AngularVelocity)
	}
	// Absent scale is identity, not zero.
	if e.Scale != (mgl32.Vec3{1, 1, 1}) {
		t.Errorf("scale: got %v, want identity", e.Scale)
	}

	// Tags union; attributes override by name.
	for _, want := range []string{"prop", "breakable", "quest_item"} {
		if !e.HasTag(want) {
			t.Errorf("missing tag %q (have %v)", want, e.Tags)
		}
	}
	if v := e.Attributes["health"]; v != IntAttr(50) {
		t.Errorf("health: got %+v, want the override", v)
	}
	if v := e.Attributes["label"]; v != StringAttr("barrel") {
		t.Errorf("label: got %+v, want the definition's", v)
	}
	if v := e.Attributes["owner"]; v != StringAttr("player") {
		t.Errorf("owner: got %+v", v)
	}

	// Only the named attachment is touched, and only its named fields.
	lid := e.Model("lid")
	if lid == nil || lid.Texture != "gold" || lid.Offset != (mgl32.Vec3{0, 1.2, 0}) {
		t.Errorf("lid override: %+v", lid)
	}
	if lid.Path != "models/lid.fmdl" {
		t.Errorf("lid path changed: %q", lid.Path)
	}
	if body := e.Model("body"); body.Texture != "wood" {
		t.Errorf("body was touched by the lid override: %+v", body)
	}
}

func TestEntityFromRawRejectsBadReferences(t *testing.T) {
	def := testDef(t)

	if _, err := EntityFromRaw(RawEntity{Type: "ghost", ID: "x"}, lookup(def)); err == nil {
		t.Error("an unknown type was accepted")
	}
	// An entry that names an attachment the type does not have, and gives it no
	// mesh of its own, is an override of something that is not there. Silently
	// ignoring it would leave the author's edit doing nothing with no sign why.
	_, err := EntityFromRaw(RawEntity{
		Type:   "barrel",
		ID:     "x",
		Models: []RawEntityModel{{Name: "turret", Texture: "steel"}},
	}, lookup(def))
	if err == nil {
		t.Error("an override for a nonexistent attachment was accepted")
	}
}

// A level may place an entity with no type at all — the editor's blank entity.
// It carries its whole description, so nothing is looked up and nothing is
// inherited.
func TestEntityFromRawAcceptsATypelessInstance(t *testing.T) {
	def := testDef(t)

	e, err := EntityFromRaw(RawEntity{
		ID:       "crate_01",
		Position: RawVec3{X: 1, Y: 2, Z: 3},
		Models: []RawEntityModel{{
			Name: "body",
			Path: "models/box.obj",
		}},
	}, lookup(def))
	if err != nil {
		t.Fatalf("EntityFromRaw: %v", err)
	}
	if e.Type != "" {
		t.Errorf("type: got %q, want none", e.Type)
	}
	if !e.Visible || !e.HasBounds() {
		t.Errorf("a blank entity should arrive visible and clickable: %+v", e)
	}
	if len(e.Models) != 1 || e.Models[0].Path != "models/box.obj" || !e.Models[0].Visible {
		t.Errorf("models: %+v", e.Models)
	}

	// And it round-trips without a definition to lean on.
	back, err := EntityFromRaw(EntityToRaw(&e, nil), lookup(def))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if back.Type != "" || len(back.Models) != 1 || back.Models[0].Path != "models/box.obj" {
		t.Errorf("drifted: %+v", back)
	}
	if back.Position != e.Position || back.BoundHalfY != e.BoundHalfY {
		t.Errorf("placement drifted: got %+v, want %+v", back, e)
	}
}

// An instance may swap an attachment's mesh, and may add one the definition
// does not have. Both used to be refused; both are what the editor's model
// picker writes.
func TestEntityModelPathOverrides(t *testing.T) {
	def := testDef(t)
	e := def.Instantiate("barrel_01")

	e.Model("lid").Path = "models/rock.obj"
	e.Models = append(e.Models, EntityModel{
		Name:    "hat",
		Path:    "models/box.obj",
		Visible: true,
	})

	raw := EntityToRaw(&e, def)
	if len(raw.Models) != 2 {
		t.Fatalf("models written: %+v", raw.Models)
	}

	back, err := EntityFromRaw(raw, lookup(def))
	if err != nil {
		t.Fatalf("EntityFromRaw: %v", err)
	}
	if m := back.Model("lid"); m == nil || m.Path != "models/rock.obj" {
		t.Errorf("swapped mesh did not survive: %+v", m)
	}
	if m := back.Model("hat"); m == nil || m.Path != "models/box.obj" {
		t.Errorf("added attachment did not survive: %+v", m)
	}
	// The definition itself is untouched — an instance's mesh is the instance's.
	if def.Models[0].Path == "models/rock.obj" {
		t.Error("the override reached the definition")
	}
}

func TestEntityToRawWritesOnlyTheDifference(t *testing.T) {
	def := testDef(t)
	e := def.Instantiate("barrel_01")
	e.Position = mgl32.Vec3{4, 0, 2}

	// Untouched: nothing but placement should be written, so the level keeps
	// tracking the definition instead of freezing a copy of it.
	raw := EntityToRaw(&e, def)
	if raw.Type != "barrel" || raw.ID != "barrel_01" {
		t.Errorf("identity: %+v", raw)
	}
	if len(raw.Tags) != 0 {
		t.Errorf("tags written despite matching the definition: %v", raw.Tags)
	}
	if len(raw.Attributes) != 0 {
		t.Errorf("attributes written despite matching: %v", raw.Attributes)
	}
	if len(raw.Models) != 0 {
		t.Errorf("model overrides written despite matching: %v", raw.Models)
	}
	if raw.Visible != nil {
		t.Error("visible written despite matching the definition")
	}

	// Touched: exactly the changes, and nothing else.
	e.SetAttr("health", IntAttr(50))
	e.AddTag("quest_item")
	e.Model("lid").Texture = "gold"

	raw = EntityToRaw(&e, def)
	if len(raw.Tags) != 1 || raw.Tags[0] != "quest_item" {
		t.Errorf("tags: got %v, want only the added one", raw.Tags)
	}
	if len(raw.Attributes) != 1 {
		t.Errorf("attributes: got %v, want only health", raw.Attributes)
	}
	if got := raw.Attributes["health"]; got.Type != "int" {
		t.Errorf("health: got %+v", got)
	}
	if len(raw.Models) != 1 || raw.Models[0].Name != "lid" || raw.Models[0].Texture != "gold" {
		t.Errorf("models: got %+v, want only the lid's texture", raw.Models)
	}
}

func TestEntityRoundTripThroughRaw(t *testing.T) {
	def := testDef(t)
	orig := RawEntity{
		Type:            "barrel",
		ID:              "barrel_01",
		Position:        RawVec3{X: 4, Y: 1, Z: 2},
		Rotation:        RawVec3{Y: 1.5},
		Scale:           &RawVec3{X: 2, Y: 2, Z: 2},
		Velocity:        RawVec3{X: 1},
		AngularVelocity: RawVec3{Y: 0.5},
		Tags:            []string{"quest_item"},
		Attributes:      map[string]RawAttribute{"health": {Type: "int", Value: 50}},
		Models:          []RawEntityModel{{Name: "lid", Texture: "gold"}},
	}

	e, err := EntityFromRaw(orig, lookup(def))
	if err != nil {
		t.Fatalf("EntityFromRaw: %v", err)
	}
	// Through YAML as well as through the structs, since that is the trip a
	// real save and reload makes.
	data, err := yaml.Marshal(EntityToRaw(&e, def))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back RawEntity
	if err := yaml.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, data)
	}
	e2, err := EntityFromRaw(back, lookup(def))
	if err != nil {
		t.Fatalf("EntityFromRaw (second): %v\n%s", err, data)
	}

	if e2.Position != e.Position || e2.Rotation != e.Rotation || e2.Scale != e.Scale {
		t.Errorf("transform drifted: %v %v %v", e2.Position, e2.Rotation, e2.Scale)
	}
	if e2.Velocity != e.Velocity || e2.AngularVelocity != e.AngularVelocity {
		t.Errorf("motion drifted: %v %v", e2.Velocity, e2.AngularVelocity)
	}
	if len(e2.Tags) != len(e.Tags) {
		t.Errorf("tags drifted: %v vs %v", e2.Tags, e.Tags)
	}
	if e2.Attributes["health"] != IntAttr(50) {
		t.Errorf("health drifted: %+v", e2.Attributes["health"])
	}
	if e2.Model("lid").Texture != "gold" {
		t.Errorf("lid texture drifted: %q", e2.Model("lid").Texture)
	}
	if e2.Model("body").Texture != "wood" {
		t.Errorf("body texture drifted: %q", e2.Model("body").Texture)
	}
}

func TestEntityToRawWithoutDefinitionWritesEverything(t *testing.T) {
	// A definition that has been deleted or renamed out from under a level must
	// cost a verbose entry, not a lost one.
	def := testDef(t)
	e := def.Instantiate("barrel_01")

	raw := EntityToRaw(&e, nil)
	if len(raw.Tags) != 2 {
		t.Errorf("tags: got %v, want both from the definition", raw.Tags)
	}
	if len(raw.Attributes) != 2 {
		t.Errorf("attributes: got %v, want both", raw.Attributes)
	}
}

func TestNormalizeTagsSortsAndDeduplicates(t *testing.T) {
	// `#tag` membership must not depend on authoring order, and a saved file
	// must not churn between runs.
	got := NormalizeTags([]string{"zeta", "alpha", "zeta", "", "mid"})
	want := []string{"alpha", "mid", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestUniqueEntityIDAvoidsCollisions(t *testing.T) {
	// IDs are how scripts address entities, so two sharing one would make `@id`
	// ambiguous.
	lvl := &Level{Entities: []Entity{{ID: "barrel"}, {ID: "barrel_2"}}}
	if got := UniqueEntityID(lvl, "barrel"); got != "barrel_3" {
		t.Errorf("got %q, want barrel_3", got)
	}
	if got := UniqueEntityID(lvl, "crate"); got != "crate" {
		t.Errorf("got %q, want crate", got)
	}
}

// The level file is where entities are actually placed, so the `entities:` key
// has to survive a marshal/unmarshal alongside the rest of the document.
func TestRawLevelCarriesEntities(t *testing.T) {
	raw := RawLevel{
		Chunks: []RawChunk{{ID: "ground", Scale: 1}},
		Spawn:  &RawSpawn{Position: RawVec3{Y: 1}},
		Entities: []RawEntity{
			{
				Type:     "barrel",
				ID:       "barrel_01",
				Position: RawVec3{X: 4, Y: 0, Z: 2},
				Tags:     []string{"quest_item"},
				Attributes: map[string]RawAttribute{
					"health": {Type: "int", Value: 50},
				},
			},
			{Type: "rock", ID: "rock_01", Position: RawVec3{X: -3}},
		},
	}

	data, err := yaml.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back RawLevel
	if err := yaml.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, data)
	}

	if len(back.Entities) != 2 {
		t.Fatalf("got %d entities, want 2\n%s", len(back.Entities), data)
	}
	if back.Entities[0].Type != "barrel" || back.Entities[0].ID != "barrel_01" {
		t.Errorf("first entity: %+v", back.Entities[0])
	}
	if back.Entities[0].Position != (RawVec3{X: 4, Y: 0, Z: 2}) {
		t.Errorf("position drifted: %+v", back.Entities[0].Position)
	}
	if got := back.Entities[0].Attributes["health"]; got.Type != "int" {
		t.Errorf("attribute drifted: %+v", got)
	}
	// A level with no entities must not grow an empty `entities:` key.
	empty, err := yaml.Marshal(RawLevel{})
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if containsKey(string(empty), "entities:") {
		t.Errorf("an entity-less level wrote an entities key:\n%s", empty)
	}
}

func containsKey(doc, key string) bool {
	for _, line := range splitLines(doc) {
		if len(line) >= len(key) && line[:len(key)] == key {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// The panel lets an instance's bounds be edited, so they have to survive a save.
// A bound offset of zero is also a legitimate authored value, which is why the
// override fields are pointers rather than plain floats.
func TestEntityBoundsOverrideRoundTrips(t *testing.T) {
	def := testDef(t)
	e := def.Instantiate("barrel_01")

	// Untouched bounds write nothing, so the level keeps tracking the type.
	if raw := EntityToRaw(&e, def); raw.Bounding != "" || raw.BoundHalfX != nil ||
		raw.BoundHalfY != nil || raw.BoundHalfZ != nil || raw.BoundOffset != nil {
		t.Errorf("bounds written despite matching the definition: %+v", raw)
	}

	e.BoundingKind = BoundingAABB // the definition says cylinder
	e.BoundHalfY = 2
	e.BoundOffset = mgl32.Vec3{}

	raw := EntityToRaw(&e, def)
	if raw.Bounding != "aabb" {
		t.Errorf("bounding: got %q, want aabb", raw.Bounding)
	}
	if raw.BoundHalfY == nil || *raw.BoundHalfY != 2 {
		t.Errorf("bound_half_y: got %v, want 2", raw.BoundHalfY)
	}
	// Only the axes that changed — widening one must not freeze the others.
	if raw.BoundHalfX != nil || raw.BoundHalfZ != nil {
		t.Errorf("untouched axes written: x=%v z=%v", raw.BoundHalfX, raw.BoundHalfZ)
	}
	// Zeroing an offset the definition sets is a real change, and a pointer is
	// what lets it be told apart from saying nothing.
	if raw.BoundOffset == nil || *raw.BoundOffset != (RawVec3{}) {
		t.Errorf("bound_offset: got %v, want an explicit zero", raw.BoundOffset)
	}

	back, err := EntityFromRaw(raw, lookup(def))
	if err != nil {
		t.Fatalf("EntityFromRaw: %v", err)
	}
	if back.BoundingKind != BoundingAABB || back.BoundHalfY != 2 {
		t.Errorf("bounds drifted: kind=%v y=%v", back.BoundingKind, back.BoundHalfY)
	}
	if back.BoundHalfX != def.BoundHalfX || back.BoundHalfZ != def.BoundHalfZ {
		t.Errorf("untouched axes did not follow the definition: x=%v z=%v",
			back.BoundHalfX, back.BoundHalfZ)
	}
	if back.BoundOffset != (mgl32.Vec3{}) {
		t.Errorf("the explicit zero offset was overwritten by the definition: %v", back.BoundOffset)
	}
}

// ---------- collision flags ----------

func TestEntityDefCollisionDefaultsAndParsing(t *testing.T) {
	// A definition that says nothing gets the inert default, so adding this
	// feature cannot have made anything already authored suddenly solid.
	def := testDef(t)
	if def.Collision != DefaultEntityCollision() {
		t.Errorf("got %+v, want the inert default", def.Collision)
	}

	var raw RawEntityDef
	if err := yaml.Unmarshal([]byte(`
id: crate
collision:
  world: true
  gravity: true
`), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got, err := entityDefFromRaw(raw, "crate")
	if err != nil {
		t.Fatalf("entityDefFromRaw: %v", err)
	}
	if !got.Collision.World || !got.Collision.Gravity {
		t.Errorf("flags not read: %+v", got.Collision)
	}
	// Unmentioned fields keep their defaults — including Movable, whose default
	// is the one that is not the zero value.
	if got.Collision.Entities {
		t.Error("entities turned itself on")
	}
	if !got.Collision.Movable {
		t.Error("movable lost its default of true")
	}
}

func TestEntityDefMovableCanBeTurnedOff(t *testing.T) {
	// The case a plain bool could not express: "absent" and "false" have to be
	// different for a field whose default is true.
	var raw RawEntityDef
	if err := yaml.Unmarshal([]byte("id: boulder\ncollision:\n  movable: false\n"), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got, err := entityDefFromRaw(raw, "boulder")
	if err != nil {
		t.Fatalf("entityDefFromRaw: %v", err)
	}
	if got.Collision.Movable {
		t.Error("movable: false was ignored")
	}
}

func TestEntityCollisionOverrideRoundTrips(t *testing.T) {
	def := testDef(t)
	def.Collision = EntityCollision{World: true, Gravity: true, Movable: true}

	e := def.Instantiate("barrel_01")
	if e.Collision != def.Collision {
		t.Fatalf("instance did not inherit: %+v", e.Collision)
	}

	// Untouched: no `collision:` key at all, so the level keeps tracking the
	// type rather than freezing a copy of its flags.
	if raw := EntityToRaw(&e, def); raw.Collision != nil {
		t.Errorf("collision written despite matching the definition: %+v", raw.Collision)
	}

	// This one barrel is nailed down and made solid to other entities.
	e.Collision.Movable = false
	e.Collision.Entities = true

	raw := EntityToRaw(&e, def)
	if raw.Collision == nil {
		t.Fatal("no collision override written")
	}
	if raw.Collision.Movable == nil || *raw.Collision.Movable {
		t.Errorf("movable: got %v, want an explicit false", raw.Collision.Movable)
	}
	if raw.Collision.Entities == nil || !*raw.Collision.Entities {
		t.Errorf("entities: got %v, want an explicit true", raw.Collision.Entities)
	}
	// Only what changed — the two flags matching the definition stay unwritten.
	if raw.Collision.World != nil || raw.Collision.Gravity != nil {
		t.Errorf("unchanged flags written: world=%v gravity=%v",
			raw.Collision.World, raw.Collision.Gravity)
	}

	back, err := EntityFromRaw(raw, lookup(def))
	if err != nil {
		t.Fatalf("EntityFromRaw: %v", err)
	}
	if back.Collision != e.Collision {
		t.Errorf("drifted: got %+v, want %+v", back.Collision, e.Collision)
	}
}
