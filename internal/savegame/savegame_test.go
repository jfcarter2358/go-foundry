package savegame

// savegame_test.go — a save made of a played level, put back over the same
// level loaded fresh, is the level as it was played.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/fileutil"
	"foundry/internal/flang"
	"foundry/internal/gfx"
	"foundry/internal/leveltest"
	"foundry/internal/savefile"
	"foundry/internal/script"
	"foundry/internal/testenv"
	"foundry/internal/world"
)

// authored is the level as its file has it: two rooms, two guards, a crate, a
// trigger and a lamp. Every call builds a new one, as a load would.
func authored(t *testing.T) *world.Level {
	t.Helper()
	a := leveltest.RoomChunk(t)
	a.ID = "room_a"
	b := leveltest.RoomChunk(t)
	b.ID = "room_b"
	lvl := &world.Level{Name: "test", Chunks: []world.Chunk{*a, *b}}
	guard := func(id string) world.Entity {
		return world.Entity{
			ID: id, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
			Attributes: world.AttrSet{"health": world.IntAttr(100)},
			// Switched off, and every other field at its default: the block
			// gob cannot tell from no block at all.
			AI: &world.AISpec{},
		}
	}
	lvl.Entities = []world.Entity{guard("guard_1"), guard("guard_2"),
		{ID: "crate", Visible: true, Scale: mgl32.Vec3{1, 1, 1}}}
	v := world.NewVolume("trigger", mgl32.Vec3{})
	v.HalfX, v.HalfY, v.HalfZ = 1, 1, 1
	lvl.Volumes = []world.Volume{v}
	lvl.Lights.Points = []world.PointLight{{ID: "lamp", Brightness: 1, Dynamic: true}}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	return lvl
}

// load is what a restore has in hand before Apply: the level fresh from its
// file, with the script runtime and events started on it.
func load(t *testing.T) *world.Level {
	t.Helper()
	lvl := authored(t)
	script.ScriptEventsReset()
	script.ScriptRuntimeLoad(lvl)
	world.ApplySpawnPoint(lvl)
	return lvl
}

func TestAPlayedLevelComesBackAsItWasPlayed(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	prevPlayer := world.PlayerEntity
	world.PlayerEntity = world.NewPlayer(mgl32.Vec3{})
	t.Cleanup(func() { world.PlayerEntity = prevPlayer })

	// A spawnable type, for the grenade play throws.
	prevRes := fileutil.ResourcesPath
	fileutil.ResourcesPath = t.TempDir()
	t.Cleanup(func() { fileutil.ResourcesPath = prevRes; world.ReloadEntityDefs() })
	dir := filepath.Join(fileutil.ResourcesPath, world.EntitiesDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "grenade.yaml"), []byte("id: grenade\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	world.ReloadEntityDefs()

	// ---- play ----
	lvl := load(t)
	g1 := world.FindEntity(lvl, "guard_1")
	g1.Position = mgl32.Vec3{3, 0, -4}
	g1.SetAttr("health", world.IntAttr(40))
	g1.AI.Enabled = true
	g1.HitHeight = 0.55 // ducked behind cover (enemy_combat.flang's hit_height)
	if !removeEntity(lvl, "crate") {
		t.Fatal("could not destroy the crate")
	}
	thrown, err := script.SpawnForRestore(lvl, "grenade", "grenade")
	if err != nil {
		t.Fatal(err)
	}
	thrown.Position = mgl32.Vec3{1, 2, 3}
	lvl.Chunks[1].Disabled = true
	world.SyncChunkGeometry(lvl)
	lvl.Volumes[0].Enabled = false
	lvl.Lights.Points[0].Brightness = 0.25
	world.PlayerEntity.Position = mgl32.Vec3{5, 1, 5}
	world.PlayerEntity.SetAttr("health", world.IntAttr(17))
	gfx.Cam.Yaw, gfx.Cam.Pitch = 1.25, -0.5

	data, err := savefile.Encode(Capture(lvl, savefile.KindCheckpoint, "gate"))
	if err != nil {
		t.Fatal(err)
	}

	// ---- restore, into a level fresh from its file ----
	world.PlayerEntity = world.NewPlayer(mgl32.Vec3{})
	gfx.Cam.Yaw, gfx.Cam.Pitch = 0, 0
	snap, err := savefile.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Kind != savefile.KindCheckpoint || snap.Checkpoint != "gate" || snap.Level != "test" {
		t.Errorf("header came back as %+v", snap.Header)
	}
	back := load(t)
	if err := Apply(back, snap); err != nil {
		t.Fatal(err)
	}

	g := world.FindEntity(back, "guard_1")
	if g == nil || g.Position != (mgl32.Vec3{3, 0, -4}) {
		t.Fatalf("guard_1 came back as %+v", g)
	}
	if h, _ := g.Attr("health"); h.I != 40 {
		t.Errorf("guard_1's health came back as %d, want 40", h.I)
	}
	if g.HitHeight != 0.55 {
		t.Errorf("guard_1's crouch came back as hit_height %v, want 0.55", g.HitHeight)
	}
	if g.AI == nil || !g.AI.Enabled {
		t.Errorf("guard_1's AI came back as %+v, want switched on", g.AI)
	}
	if g2 := world.FindEntity(back, "guard_2"); g2 == nil || g2.AI == nil || g2.AI.Enabled {
		t.Errorf("guard_2's switched-off AI block came back as %+v", g2)
	}
	if world.FindEntity(back, "crate") != nil {
		t.Error("the destroyed crate is back")
	}
	if gr := world.FindEntity(back, "grenade"); gr == nil || gr.Position != (mgl32.Vec3{1, 2, 3}) {
		t.Errorf("the thrown grenade came back as %+v", gr)
	}
	if back.Chunks[0].Disabled || !back.Chunks[1].Disabled {
		t.Errorf("chunks came back enabled %v/%v, want on/off", !back.Chunks[0].Disabled, !back.Chunks[1].Disabled)
	}
	if back.Volumes[0].Enabled {
		t.Error("the disarmed trigger came back armed")
	}
	if b := back.Lights.Points[0].Brightness; b != 0.25 {
		t.Errorf("the lamp came back at %v, want 0.25", b)
	}
	p := world.PlayerEntity
	if p.Position != (mgl32.Vec3{5, 1, 5}) {
		t.Errorf("the player came back at %v", p.Position)
	}
	if h, _ := p.Attr("health"); h.I != 17 {
		t.Errorf("the player's health came back as %d, want 17", h.I)
	}
	if gfx.Cam.Yaw != 1.25 || gfx.Cam.Pitch != -0.5 {
		t.Errorf("the view came back at yaw %v pitch %v", gfx.Cam.Yaw, gfx.Cam.Pitch)
	}

	// And the rest: saved again at once, the restored game is the save.
	again, err := savefile.Encode(Capture(back, savefile.KindCheckpoint, "gate"))
	if err != nil {
		t.Fatal(err)
	}
	resaved, err := savefile.Decode(again)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range Diff(snap, resaved) {
		t.Errorf("differs after restoring: %s", d)
	}
}

// A save of another level is refused rather than laid over the wrong one.
func TestASaveIsOnlyAppliedToItsOwnLevel(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	lvl := authored(t)
	s := Fresh("elsewhere", savefile.KindManual)
	if err := Apply(lvl, s); err == nil {
		t.Error("a save of \"elsewhere\" was applied to \"test\"")
	}
}

// removeEntity destroys an entity the way the destroy verb does.
func removeEntity(lvl *world.Level, id string) bool {
	return script.RemoveForRestore(lvl, flang.RefEntity, id)
}
