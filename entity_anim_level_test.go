package main

// entity_anim_level_test.go — the level-file seam: an `animation:` field on a
// placed entity has to reach a playing clip, and survive a save/load round trip.
//
// The GL half is opt-in like the other GL tests, because LoadLevel uploads
// geometry:
//
//	FOUNDRY_GL_TEST=1 go test -run TestLevelStartsAnEntityAnimation .

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// TestEntityAnimationFieldsRoundTripThroughTheLevelFile is headless: it checks
// the schema carries the fields, and that a nil loop flag stays distinguishable
// from an explicit false.
func TestEntityAnimationFieldsRoundTripThroughTheLevelFile(t *testing.T) {
	off := false
	e := Entity{
		ID:             "walker",
		ModelPath:      "models/walker.fmdl",
		Animation:      "walk",
		AnimationSpeed: 1.5,
		AnimationLoop:  &off,
		Scale:          mgl32.Vec3{1, 1, 1},
	}
	raw := entityToRaw(&e)
	if raw.Animation != "walk" || raw.AnimationSpeed != 1.5 {
		t.Errorf("animation fields lost on save: %+v", raw)
	}
	if raw.AnimationLoop == nil || *raw.AnimationLoop {
		t.Error("an explicit loop:false was not preserved")
	}

	body, err := yaml.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back RawEntity
	if err := yaml.Unmarshal(body, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Animation != "walk" || back.AnimationSpeed != 1.5 || back.AnimationLoop == nil || *back.AnimationLoop {
		t.Errorf("animation fields did not survive YAML: %+v", back)
	}

	// A static entity must not write the block at all.
	plain := entityToRaw(&Entity{ID: "crate", Scale: mgl32.Vec3{1, 1, 1}})
	out, err := yaml.Marshal(plain)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, field := range []string{"animation:", "animation_speed:", "animation_loop:"} {
		if strings.Contains(string(out), field) {
			t.Errorf("a static entity wrote %q:\n%s", field, out)
		}
	}
}

// TestLevelStartsAnEntityAnimation runs the whole runtime path: a .fmdl with a
// clip, referenced from a level file, loaded through LoadLevel, ticked by the
// same call the main loop makes, with the palette reaching the GPU buffers.
func TestLevelStartsAnEntityAnimation(t *testing.T) {
	glTestContext(t)

	prevExe, prevNames := executablePath, textureNames
	executablePath = "resources"
	textureNames = ScanTextures("resources/textures")
	t.Cleanup(func() { executablePath, textureNames = prevExe, prevNames })

	if err := LoadProgram("resources/shaders/vertex.glsl", "resources/shaders/fragment.glsl"); err != nil {
		t.Fatalf("LoadProgram: %v", err)
	}

	dir := t.TempDir()
	doc, idx := boneChain(t)
	doc.AssignVertsToBone(modelAllVerts(doc), idx[0], 1)
	c := doc.Clip(doc.AddClip("walk"))
	c.Duration, c.FPS, c.Loop = 1, 24, true
	c.SetKey(idx[0], 0, identBonePose())
	c.SetKey(idx[0], 1, ModelBonePose{
		Rot:   mgl32.QuatRotate(mgl32.DegToRad(90), mgl32.Vec3{1, 0, 0}),
		Scale: mgl32.Vec3{1, 1, 1},
	})
	modelFile := filepath.Join(dir, "walker"+modelExt)
	if err := SaveModel(doc, modelFile); err != nil {
		t.Fatal(err)
	}

	levelFile := filepath.Join(dir, "anim.yaml")
	body := "chunks: []\nentities:\n" +
		"  - id: walker\n" +
		"    position: { x: 0, y: 0, z: 0 }\n" +
		"    model: " + modelFile + "\n" +
		"    texture: brick\n" +
		"    bounding: aabb\n" +
		"    bound_half_x: 0.5\n    bound_half_y: 0.5\n    bound_half_z: 0.5\n" +
		"    animation: walk\n" +
		"    animation_speed: 2.0\n"
	if err := os.WriteFile(levelFile, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	lvl, err := LoadLevel(levelFile)
	if err != nil {
		t.Fatalf("LoadLevel: %v", err)
	}
	if len(lvl.Entities) != 1 {
		t.Fatalf("loaded %d entities, want 1", len(lvl.Entities))
	}
	e := &lvl.Entities[0]
	if e.Anim == nil {
		t.Fatal("the level's animation field did not produce playback state")
	}
	if !e.Anim.Playing {
		t.Error("the named clip did not start playing")
	}
	if e.Anim.Speed != 2 {
		t.Errorf("animation_speed = %v, want 2", e.Anim.Speed)
	}

	// The main loop's tick, and the palette it produces must reach the buffers.
	before := e.Submeshes[0].BonePalette[idx[0]]
	UpdateEntityAnimations(&lvl, 0.25)
	after := e.Submeshes[0].BonePalette[idx[0]]
	if mat4Close(before, after, 1e-6) {
		t.Error("ticking the level did not change the palette the submesh draws from")
	}
	for i := range e.Submeshes {
		if !e.Submeshes[i].IsSkinned() {
			t.Errorf("submesh %d did not upload bone attributes", i)
		}
	}
}

// TestLevelWithAMissingClipStillLoads: a level naming a clip the model lost is a
// content error, not a reason to refuse the level.
func TestLevelWithAMissingClipStillLoads(t *testing.T) {
	doc, idx := boneChain(t)
	doc.AssignVertsToBone(modelAllVerts(doc), idx[0], 1)
	c := doc.Clip(doc.AddClip("walk"))
	c.SetKey(idx[0], 0, identBonePose())

	e := Entity{ID: "walker", Animation: "sprint"}
	e.InitAnimation(doc)
	if e.Anim == nil {
		t.Fatal("playback state was not created")
	}
	if e.Anim.Playing {
		t.Error("a clip that does not exist started playing")
	}
	for i, m := range e.Anim.Palette {
		if !mat4Close(m, mgl32.Ident4(), 1e-6) {
			t.Errorf("bone %d is not at rest after a missing clip: %v", i, m)
		}
	}
}
