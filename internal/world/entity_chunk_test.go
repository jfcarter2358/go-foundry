package world

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// chunkDef is a definition that carries geometry: two decks, one of them
// authored switched off, and a vehicle block binding one of them as walkable.
func chunkDef(t *testing.T) *EntityDef {
	t.Helper()
	const src = `
id: shuttle
chunks:
  - name: hull
    file: shuttle_hull
    offset: {x: 0, y: 0.5, z: 0}
    rotation: 1.5707963
    scale: 2
  - name: hold
    file: shuttle_hold
    disabled: true
vehicle:
  mode: air
  interiors: [hull]
  seats:
    - name: pilot
      driver: true
      position: {x: 0, y: 0.2, z: 0}
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

func TestEntityDefChunksParse(t *testing.T) {
	def := chunkDef(t)
	if len(def.Chunks) != 2 {
		t.Fatalf("parsed %d chunk attachments, want 2", len(def.Chunks))
	}
	hull := def.Chunks[0]
	if hull.Name != "hull" || hull.File != "shuttle_hull" {
		t.Errorf("first attachment is %+v", hull)
	}
	if hull.Offset != (mgl32.Vec3{0, 0.5, 0}) {
		t.Errorf("offset %v, want {0 0.5 0}", hull.Offset)
	}
	if hull.EffectiveScale() != 2 {
		t.Errorf("scale %v, want 2", hull.EffectiveScale())
	}
	if def.Chunks[1].Disabled != true {
		t.Error("the second deck was authored disabled and did not come back that way")
	}
	// An unmentioned scale must load as full size, not as a point.
	if def.Chunks[1].EffectiveScale() != 1 {
		t.Errorf("absent scale read as %v, want 1", def.Chunks[1].EffectiveScale())
	}
}

func TestEntityDefChunksRoundTrip(t *testing.T) {
	def := chunkDef(t)
	raw := entityDefToRaw(def)
	back, err := entityDefFromRaw(raw, "shuttle")
	if err != nil {
		t.Fatalf("entityDefFromRaw: %v", err)
	}
	if !entityChunksEqual(def.Chunks, back.Chunks) {
		t.Errorf("chunks did not survive a round trip:\n got %+v\nwant %+v", back.Chunks, def.Chunks)
	}
}

// TestInstantiateCopiesChunks is the rule that keeps one placed ship from
// rewriting the type every other level places: the instance's attachment list
// must be its own array, not the definition's.
func TestInstantiateCopiesChunks(t *testing.T) {
	def := chunkDef(t)
	e := def.Instantiate("shuttle_1")
	if len(e.Chunks) != len(def.Chunks) {
		t.Fatalf("instance carries %d attachments, want %d", len(e.Chunks), len(def.Chunks))
	}
	e.Chunks[0].Offset = mgl32.Vec3{99, 99, 99}
	if def.Chunks[0].Offset == e.Chunks[0].Offset {
		t.Error("moving the instance's deck moved the definition's")
	}
}

// TestEntityChunksWriteOnlyWhenChanged is why a level that places a ship stays
// four lines long: an instance that never touched the geometry writes none.
func TestEntityChunksWriteOnlyWhenChanged(t *testing.T) {
	def := chunkDef(t)
	e := def.Instantiate("shuttle_1")
	if raw := EntityToRaw(&e, def); len(raw.Chunks) != 0 {
		t.Errorf("untouched instance wrote %d chunk entries, want none", len(raw.Chunks))
	}

	e.Chunks[0].Offset = mgl32.Vec3{0, 2, 0}
	raw := EntityToRaw(&e, def)
	if len(raw.Chunks) != len(def.Chunks) {
		t.Fatalf("a moved deck wrote %d entries, want the whole list (%d)",
			len(raw.Chunks), len(def.Chunks))
	}
	back, err := EntityFromRaw(raw, lookup(def))
	if err != nil {
		t.Fatalf("EntityFromRaw: %v", err)
	}
	if back.Chunks[0].Offset != (mgl32.Vec3{0, 2, 0}) {
		t.Errorf("override came back as %v, want {0 2 0}", back.Chunks[0].Offset)
	}
	// And the deck it did not touch still tracks the definition.
	if back.Chunks[1] != def.Chunks[1] {
		t.Errorf("untouched deck came back as %+v, want %+v", back.Chunks[1], def.Chunks[1])
	}
}

// TestBlankEntityChunksRoundTrip covers the typeless case: an entity authored
// in a level carries its whole description, chunk attachments included.
func TestBlankEntityChunksRoundTrip(t *testing.T) {
	e := NewBlankEntity("lift")
	e.Chunks = []EntityChunk{{Name: "car", File: "lift_car", Offset: mgl32.Vec3{0, 1, 0}, Scale: 1}}

	raw := EntityToRaw(&e, nil)
	if len(raw.Chunks) != 1 {
		t.Fatalf("blank entity wrote %d chunk entries, want 1", len(raw.Chunks))
	}
	back, err := EntityFromRaw(raw, func(string) (*EntityDef, error) {
		t.Fatal("a typeless entity must not look a definition up")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("EntityFromRaw: %v", err)
	}
	if len(back.Chunks) != 1 || back.Chunks[0].File != "lift_car" {
		t.Errorf("chunks came back as %+v", back.Chunks)
	}
}

func TestEntityChunkIDQualifiesByEntity(t *testing.T) {
	// Two ships from one definition must not both call their deck "hull", or
	// `@id` would name two things.
	if a, b := EntityChunkID("ship_1", "hull"), EntityChunkID("ship_2", "hull"); a == b {
		t.Errorf("both instances produced %q", a)
	}
	if got := EntityChunkID("", "hull"); got != "hull" {
		t.Errorf("with no entity the ID is %q, want %q", got, "hull")
	}
}

func TestUniqueDefChunkName(t *testing.T) {
	chunks := []EntityChunk{{Name: "hull"}, {Name: "hull_2"}}
	if got := UniqueDefChunkName(chunks, "hull"); got != "hull_3" {
		t.Errorf("UniqueDefChunkName = %q, want hull_3", got)
	}
	if got := UniqueDefChunkName(chunks, "hold"); got != "hold" {
		t.Errorf("UniqueDefChunkName = %q, want hold", got)
	}
}

// TestEntityDefCloneIsDeep is the assertion the entity editor's undo rests on:
// a snapshot that shared a slice with the live document would record nothing.
func TestEntityDefCloneIsDeep(t *testing.T) {
	def := chunkDef(t)
	def.Tags = []string{"vehicle"}
	def.Attributes = AttrSet{"fuel": IntAttr(100)}
	def.Models = []EntityModel{{Name: "body", Path: "models/shuttle.fmdl",
		Animations: []EntityAnimation{{Name: "idle"}}}}

	c := def.Clone()
	c.Tags[0] = "changed"
	c.Attributes["fuel"] = IntAttr(0)
	c.Chunks[0].File = "changed"
	c.Models[0].Animations[0].Name = "changed"
	c.Vehicle.Seats[0].Name = "changed"
	c.Vehicle.Interiors[0] = "changed"

	if def.Tags[0] != "vehicle" {
		t.Error("tags are shared with the clone")
	}
	if def.Attributes["fuel"] != IntAttr(100) {
		t.Error("attributes are shared with the clone")
	}
	if def.Chunks[0].File != "shuttle_hull" {
		t.Error("chunk attachments are shared with the clone")
	}
	if def.Models[0].Animations[0].Name != "idle" {
		t.Error("animation lists are shared with the clone")
	}
	if def.Vehicle.Seats[0].Name != "pilot" {
		t.Error("vehicle seats are shared with the clone")
	}
	if def.Vehicle.Interiors[0] != "hull" {
		t.Error("vehicle interiors are shared with the clone")
	}
}

// TestDuplicateChunkAttachmentRefused: a name has to address exactly one deck,
// since a script and the vehicle's interior list both use it.
func TestDuplicateChunkAttachmentRefused(t *testing.T) {
	_, err := entityChunksFromRaw([]RawEntityChunk{{Name: "hull"}, {Name: "hull"}})
	if err == nil {
		t.Fatal("two attachments called hull were accepted")
	}
}
