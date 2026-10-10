package levelio

// entity_emitters_test.go — an entity type's own particle effects, from the
// definition that holds them to the level they end up in and back out again.
//
// The rules are the ones the carried geometry beside them keeps, and they pull
// in opposite directions for the same reason. A carried emitter has to be
// *fully* in the level — a real Emitter, its document read, riding the entity —
// or the plume is not something the game will ever draw. And it has to be
// *entirely* absent from the saved file, or the level freezes a copy of the
// placement into itself and stops tracking the type.

import (
	"foundry/internal/gfx"
	"foundry/internal/particle"
	"foundry/internal/world"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// withEffectFile writes a minimal one-layer .fpar under particles/ and returns
// its bare name, which is how an emitter refers to one.
func withEffectFile(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, particle.ParticlesDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir particles: %v", err)
	}
	const src = `
id: plume
layers:
  - name: smoke
    texture: spark
    rate: 20
`
	if err := os.WriteFile(filepath.Join(dir, name+particle.ParticleExt), []byte(src), 0o644); err != nil {
		t.Fatalf("write effect: %v", err)
	}
	return name
}

// TestLevelLoadMaterializesCarriedEffects is the end-to-end path: a definition
// on disk holding an emitter, a level that places one, and a load that produces
// a running emitter attached to the entity — and a save that writes neither.
//
// It goes through rawToLevel rather than calling the materialiser directly,
// because the wiring inside loadLevelObjects — materialising after every entity
// exists, and feeding the effect's textures into the pass that loads them — is
// the part that has nowhere else to be tested.
func TestLevelLoadMaterializesCarriedEffects(t *testing.T) {
	root := withInstallRoot(t)
	withEffectFile(t, root, "plume")

	if err := os.MkdirAll(filepath.Join(root, world.EntitiesDirName), 0o755); err != nil {
		t.Fatalf("mkdir entities: %v", err)
	}
	const def = `
id: shuttle
emitters:
  - name: thruster
    effect: plume
    offset: {x: 0, y: 0.5, z: -2}
    tags: [engine]
`
	if err := os.WriteFile(filepath.Join(root, world.EntitiesDirName, "shuttle.yaml"), []byte(def), 0o644); err != nil {
		t.Fatalf("write definition: %v", err)
	}
	world.ReloadEntityDefs()
	t.Cleanup(world.ReloadEntityDefs)

	// The layer names "spark"; register it so the loader's texture pass has
	// something to resolve, as the other end-to-end level tests here do.
	if gfx.Textures == nil {
		gfx.Textures = map[string]uint32{}
	}
	if gfx.TextureNames == nil {
		gfx.TextureNames = map[string]string{}
	}
	gfx.Textures["spark"], gfx.TextureNames["spark"] = 1, "spark"

	raw := world.RawLevel{Entities: []world.RawEntity{{Type: "shuttle", ID: "shuttle_1"}}}
	lvl, err := rawToLevel(raw)
	if err != nil {
		t.Fatalf("rawToLevel: %v", err)
	}

	if len(lvl.Emitters) != 1 {
		t.Fatalf("loaded %d emitters, want the one the type carries", len(lvl.Emitters))
	}
	em := &lvl.Emitters[0]
	if em.ID != "shuttle_1.thruster" {
		t.Errorf("emitter ID %q, want it qualified by the placement", em.ID)
	}
	if em.Attach.Entity != "shuttle_1" || em.FromEntity != "shuttle_1" {
		t.Errorf("emitter came back as %+v", em.Attach)
	}
	if !em.Loaded() {
		t.Error("the effect document was not read; the emitter would produce nothing")
	}
	if !em.HasTag("engine") {
		t.Error("the attachment's tags did not reach the placement")
	}

	// And saving gives back exactly what was loaded: the placement, not the
	// effect.
	back, err := LevelToRaw(&lvl)
	if err != nil {
		t.Fatalf("LevelToRaw: %v", err)
	}
	if len(back.Emitters) != 0 {
		t.Errorf("saving wrote %d emitters, want none — the type owns them", len(back.Emitters))
	}
	if len(back.Entities) != 1 || len(back.Entities[0].Emitters) != 0 {
		t.Errorf("an untouched instance restated its type's effects: %+v", back.Entities)
	}
}

// TestSavingKeepsTheLevelsOwnEmitters is the other half of the rule above: the
// FromEntity filter must take out only what an entity put in, or a level that
// happens to contain a ship would lose every torch in its corridors.
func TestSavingKeepsTheLevelsOwnEmitters(t *testing.T) {
	withInstallRoot(t)

	lvl := &world.Level{}
	own := world.NewEmitter("hall_torch", mgl32.Vec3{})
	own.Effect = "torch"
	lvl.Emitters = append(lvl.Emitters, own)

	carried := world.NewEmitter("shuttle_1.thruster", mgl32.Vec3{})
	carried.FromEntity = "shuttle_1"
	lvl.Emitters = append(lvl.Emitters, carried)

	raw, err := LevelToRaw(lvl)
	if err != nil {
		t.Fatalf("LevelToRaw: %v", err)
	}
	if len(raw.Emitters) != 1 || raw.Emitters[0].ID != "hall_torch" {
		out, _ := yaml.Marshal(raw.Emitters)
		t.Fatalf("emitters written:\n%s", out)
	}
}
