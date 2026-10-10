package levelio

// prefabs_test.go — a reusable group, from the file it lives in to the level it
// is stamped into and back out again.
//
// The two rules worth pinning are opposites of each other, exactly as they are
// for an entity's carried geometry. A materialised instance has to be *fully*
// in the level — placed, uploaded, addressable — or a prefab is a thing you can
// place and never see. And it has to be *entirely* absent from the saved file,
// or the level freezes a copy and stops tracking the original, which is the one
// property that makes it a prefab rather than a paste.

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// lampPrefab is one brush, one point light and one volume, authored around the
// origin — a street lamp, which is the shape this feature exists for.
func lampPrefab() world.Prefab {
	post := world.NewBoxBrush("post", mgl32.Vec3{0, 2, 0}, mgl32.Vec3{0.2, 2, 0.2}, "concrete")
	return world.Prefab{
		Brushes: []world.RawBrush{world.BrushToRaw(&post)},
		Lights: []world.RawPointLight{{
			ID:         "bulb",
			Brightness: 1,
			Radius:     8,
		}},
		Volumes: []world.RawVolume{{
			ID:       "pool",
			Position: world.RawVec3{X: 0, Y: 0, Z: 0},
			Half:     world.RawVec3{X: 4, Y: 1, Z: 4},
		}},
	}
}

// withPrefabFile points the install root at a temporary directory and writes
// one prefab into prefabs/, returning its name.
func withPrefabFile(t *testing.T, name string, p world.Prefab) string {
	t.Helper()
	testenv.WithoutGeometryUploads(t)

	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = t.TempDir()
	t.Cleanup(func() { fileutil.ResourcesPath = prev })

	if err := SavePrefab(name, &p); err != nil {
		t.Fatalf("SavePrefab: %v", err)
	}
	return name
}

// anInstance is one placement of a prefab file.
func anInstance(id, file string, pos mgl32.Vec3) world.PrefabInstance {
	return world.PrefabInstance{ID: id, File: file, Position: pos, Scale: 1}
}

// ---------- materialising ----------

func TestMaterializePrefabStampsEveryKindOfContent(t *testing.T) {
	file := withPrefabFile(t, "street_lamp", lampPrefab())

	lvl := &world.Level{}
	lvl.Prefabs = append(lvl.Prefabs, anInstance("lamp1", file, mgl32.Vec3{100, 0, -40}))
	if err := MaterializePrefab(lvl, &lvl.Prefabs[0], map[string]bool{}); err != nil {
		t.Fatalf("MaterializePrefab: %v", err)
	}

	if len(lvl.Chunks) != 1 {
		t.Fatalf("got %d chunks, want the instance's one", len(lvl.Chunks))
	}
	ch := &lvl.Chunks[0]
	if ch.FromPrefab != "lamp1" {
		t.Errorf("chunk FromPrefab = %q, want the instance", ch.FromPrefab)
	}
	if len(ch.Brushes) != 1 {
		t.Fatalf("got %d brushes, want 1", len(ch.Brushes))
	}
	// The brushes ride the chunk's own placement rather than having it baked
	// into their verts, so the chunk carries the position and the verts do not.
	if ch.Origin != (mgl32.Vec3{100, 0, -40}) {
		t.Errorf("chunk origin = %v, want the instance's position", ch.Origin)
	}
	if got := ch.Brushes[0].Verts[0]; got.X() > 50 {
		t.Errorf("the instance's position was baked into the verts: %v", got)
	}

	if len(lvl.Lights.Points) != 1 {
		t.Fatalf("got %d lights, want 1", len(lvl.Lights.Points))
	}
	// A light is level-scoped with no parent to inherit from, so its position
	// IS carried.
	if got := lvl.Lights.Points[0].Position; got != (mgl32.Vec3{100, 0, -40}) {
		t.Errorf("light position = %v, want the instance's own", got)
	}
	if lvl.Lights.Points[0].FromPrefab != "lamp1" {
		t.Error("the light is not tagged with the instance that made it")
	}

	if len(lvl.Volumes) != 1 {
		t.Fatalf("got %d volumes, want 1", len(lvl.Volumes))
	}
	if lvl.Volumes[0].FromPrefab != "lamp1" {
		t.Error("the volume is not tagged with the instance that made it")
	}
}

// Everything a prefab names is unique only within the prefab. Twenty lamps from
// one file would otherwise be twenty lights called "bulb", and `@bulb` would
// mean nothing.
func TestTwoInstancesDoNotShareNames(t *testing.T) {
	file := withPrefabFile(t, "street_lamp", lampPrefab())

	lvl := &world.Level{}
	lvl.Prefabs = append(lvl.Prefabs,
		anInstance("lamp1", file, mgl32.Vec3{0, 0, 0}),
		anInstance("lamp2", file, mgl32.Vec3{10, 0, 0}))
	for i := range lvl.Prefabs {
		if err := MaterializePrefab(lvl, &lvl.Prefabs[i], map[string]bool{}); err != nil {
			t.Fatalf("MaterializePrefab: %v", err)
		}
	}

	seen := map[string]bool{}
	for _, pl := range lvl.Lights.Points {
		if seen[pl.ID] {
			t.Fatalf("two lights share the ID %q", pl.ID)
		}
		seen[pl.ID] = true
	}
	if !seen["lamp1_bulb"] || !seen["lamp2_bulb"] {
		t.Errorf("light IDs = %v, want them prefixed by their instance", seen)
	}

	brushes := map[string]bool{}
	for i := range lvl.Chunks {
		for j := range lvl.Chunks[i].Brushes {
			id := lvl.Chunks[i].Brushes[j].ID
			if brushes[id] {
				t.Fatalf("two brushes share the ID %q", id)
			}
			brushes[id] = true
		}
	}
}

// Scale reaches everything with a size: a lamp shrunk to half must not keep a
// full-size trigger or light the same room.
func TestAScaledInstanceScalesWhatHasASize(t *testing.T) {
	file := withPrefabFile(t, "street_lamp", lampPrefab())

	lvl := &world.Level{}
	inst := anInstance("lamp1", file, mgl32.Vec3{0, 0, 0})
	inst.Scale = 0.5
	lvl.Prefabs = append(lvl.Prefabs, inst)
	if err := MaterializePrefab(lvl, &lvl.Prefabs[0], map[string]bool{}); err != nil {
		t.Fatalf("MaterializePrefab: %v", err)
	}

	if got := lvl.Volumes[0].HalfX; got != 2 {
		t.Errorf("volume half-X = %v, want the authored 4 halved", got)
	}
	if got := lvl.Lights.Points[0].Radius; got != 4 {
		t.Errorf("light radius = %v, want the authored 8 halved", got)
	}
	if got := lvl.Chunks[0].Scale; got != 0.5 {
		t.Errorf("chunk scale = %v, want the instance's", got)
	}
}

// A missing file costs the level that instance's content, not the level. A
// level that refuses to open over one missing prefab is worse than one that
// opens with a hole in it and says so.
func TestAMissingPrefabFileIsReportedNotFatal(t *testing.T) {
	withPrefabFile(t, "street_lamp", lampPrefab())

	lvl := &world.Level{}
	lvl.Prefabs = append(lvl.Prefabs, anInstance("lamp1", "no_such_prefab", mgl32.Vec3{}))
	err := MaterializePrefab(lvl, &lvl.Prefabs[0], map[string]bool{})
	if err == nil {
		t.Fatal("a missing prefab file was not reported")
	}
	if len(lvl.Chunks) != 0 || len(lvl.Volumes) != 0 {
		t.Error("a failed materialise left content behind")
	}
}

// ---------- dropping and re-stamping ----------

func TestDropPrefabContentTakesEverythingAndNothingElse(t *testing.T) {
	file := withPrefabFile(t, "street_lamp", lampPrefab())

	lvl := &world.Level{
		Chunks:  []world.Chunk{{ID: "level_own"}},
		Volumes: []world.Volume{{ID: "level_own_trigger", Enabled: true}},
	}
	lvl.Lights.Points = []world.PointLight{{ID: "level_own_light"}}

	lvl.Prefabs = append(lvl.Prefabs, anInstance("lamp1", file, mgl32.Vec3{}))
	if err := MaterializePrefab(lvl, &lvl.Prefabs[0], map[string]bool{}); err != nil {
		t.Fatalf("MaterializePrefab: %v", err)
	}

	if n := DropPrefabContent(lvl, "lamp1"); n != 3 {
		t.Errorf("dropped %d objects, want the chunk, the volume and the light", n)
	}
	if len(lvl.Chunks) != 1 || lvl.Chunks[0].ID != "level_own" {
		t.Errorf("the level's own chunk went with the prefab's: %v", lvl.Chunks)
	}
	if len(lvl.Volumes) != 1 || lvl.Volumes[0].ID != "level_own_trigger" {
		t.Errorf("the level's own volume went with the prefab's: %v", lvl.Volumes)
	}
	if len(lvl.Lights.Points) != 1 || lvl.Lights.Points[0].ID != "level_own_light" {
		t.Errorf("the level's own light went with the prefab's: %v", lvl.Lights.Points)
	}
}

// The live link, which is the whole point: saving the file updates every
// placement of it, and leaves placements of other files alone.
func TestSavingAPrefabRestampsEveryPlacement(t *testing.T) {
	file := withPrefabFile(t, "street_lamp", lampPrefab())

	lvl := &world.Level{}
	lvl.Prefabs = append(lvl.Prefabs,
		anInstance("lamp1", file, mgl32.Vec3{0, 0, 0}),
		anInstance("lamp2", file, mgl32.Vec3{10, 0, 0}))
	for i := range lvl.Prefabs {
		if err := MaterializePrefab(lvl, &lvl.Prefabs[i], map[string]bool{}); err != nil {
			t.Fatalf("MaterializePrefab: %v", err)
		}
	}
	if got := len(lvl.Lights.Points); got != 2 {
		t.Fatalf("got %d lights before the edit, want 2", got)
	}

	// The lamp grows a second light.
	edited := lampPrefab()
	edited.Lights = append(edited.Lights, world.RawPointLight{ID: "bulb2", Brightness: 1, Radius: 4})
	if err := SavePrefab(file, &edited); err != nil {
		t.Fatalf("SavePrefab: %v", err)
	}

	n, errs := RestampPrefabsOfFile(lvl, file)
	if len(errs) > 0 {
		t.Fatalf("RestampPrefabsOfFile: %v", errs)
	}
	if n != 2 {
		t.Errorf("re-stamped %d placements, want 2", n)
	}
	if got := len(lvl.Lights.Points); got != 4 {
		t.Errorf("got %d lights after the edit, want 2 placements × 2 lights", got)
	}
}

// ---------- the round trip ----------

// The rule that makes it a prefab rather than a paste: a saved level writes the
// PLACEMENT and none of the content.
func TestASavedLevelWritesThePlacementNotTheContent(t *testing.T) {
	file := withPrefabFile(t, "street_lamp", lampPrefab())

	lvl := &world.Level{}
	lvl.Prefabs = append(lvl.Prefabs, anInstance("lamp1", file, mgl32.Vec3{7, 0, 3}))
	if err := MaterializePrefab(lvl, &lvl.Prefabs[0], map[string]bool{}); err != nil {
		t.Fatalf("MaterializePrefab: %v", err)
	}

	raw, err := LevelToRaw(lvl)
	if err != nil {
		t.Fatalf("LevelToRaw: %v", err)
	}
	if len(raw.Prefabs) != 1 || raw.Prefabs[0].ID != "lamp1" || raw.Prefabs[0].File != file {
		t.Fatalf("the placement was not written: %+v", raw.Prefabs)
	}
	if raw.Prefabs[0].Position.X != 7 || raw.Prefabs[0].Position.Z != 3 {
		t.Errorf("the placement's position was not written: %+v", raw.Prefabs[0].Position)
	}
	if len(raw.Chunks) != 0 {
		t.Errorf("the prefab's geometry was written into the level: %d chunks", len(raw.Chunks))
	}
	if len(raw.Volumes) != 0 {
		t.Errorf("the prefab's volume was written into the level: %d", len(raw.Volumes))
	}
	if len(raw.Lights.Point) != 0 {
		t.Errorf("the prefab's light was written into the level: %d", len(raw.Lights.Point))
	}
}

// And what the level DOES own is still written, so the skip is a filter rather
// than a switch.
func TestASavedLevelStillWritesItsOwnContent(t *testing.T) {
	file := withPrefabFile(t, "street_lamp", lampPrefab())

	post := world.NewBoxBrush("wall", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "concrete")
	lvl := &world.Level{
		Chunks:  []world.Chunk{{ID: "level_own", Brushes: []world.Brush{post}, Scale: 1}},
		Volumes: []world.Volume{{ID: "level_own_trigger", Enabled: true, HalfX: 1, HalfY: 1, HalfZ: 1}},
	}
	lvl.Lights.Points = []world.PointLight{{ID: "level_own_light", Brightness: 1}}
	lvl.Prefabs = append(lvl.Prefabs, anInstance("lamp1", file, mgl32.Vec3{}))
	if err := MaterializePrefab(lvl, &lvl.Prefabs[0], map[string]bool{}); err != nil {
		t.Fatalf("MaterializePrefab: %v", err)
	}

	raw, err := LevelToRaw(lvl)
	if err != nil {
		t.Fatalf("LevelToRaw: %v", err)
	}
	if len(raw.Chunks) != 1 || raw.Chunks[0].ID != "level_own" {
		t.Errorf("the level's own chunk was not written: %+v", raw.Chunks)
	}
	if len(raw.Volumes) != 1 || raw.Volumes[0].ID != "level_own_trigger" {
		t.Errorf("the level's own volume was not written: %+v", raw.Volumes)
	}
	if len(raw.Lights.Point) != 1 || raw.Lights.Point[0].ID != "level_own_light" {
		t.Errorf("the level's own light was not written: %+v", raw.Lights.Point)
	}
}

// The cache must never be what makes a save invisible. SavePrefab invalidates,
// so the re-stamp that follows it in the editor reads what was just written.
func TestTheCacheDoesNotDefeatTheLiveLink(t *testing.T) {
	file := withPrefabFile(t, "street_lamp", lampPrefab())

	// Warm the cache.
	if _, err := LoadPrefab(file); err != nil {
		t.Fatalf("LoadPrefab: %v", err)
	}

	edited := lampPrefab()
	edited.Lights = nil
	if err := SavePrefab(file, &edited); err != nil {
		t.Fatalf("SavePrefab: %v", err)
	}

	got, err := LoadPrefab(file)
	if err != nil {
		t.Fatalf("LoadPrefab: %v", err)
	}
	if len(got.Lights) != 0 {
		t.Errorf("the cache served the pre-save contents: %d lights", len(got.Lights))
	}
}

// And an explicit invalidate picks up a file changed underneath us — which is
// what the editor's "Re-stamp from file" action is for.
func TestInvalidatingTheCacheRereadsFromDisk(t *testing.T) {
	file := withPrefabFile(t, "street_lamp", lampPrefab())
	if _, err := LoadPrefab(file); err != nil {
		t.Fatalf("LoadPrefab: %v", err)
	}

	// Written behind SavePrefab's back, the way an external edit would be.
	edited := lampPrefab()
	edited.Lights = nil
	data, err := yaml.Marshal(world.RawPrefab{Brushes: edited.Brushes, Volumes: edited.Volumes})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(PrefabFilePath(file), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if got, _ := LoadPrefab(file); len(got.Lights) != 1 {
		t.Fatal("setup wrong: the cache should still be serving the old contents")
	}
	InvalidatePrefabCache(file)
	if got, _ := LoadPrefab(file); len(got.Lights) != 0 {
		t.Error("an invalidated cache did not re-read from disk")
	}
}

// A prefab file that has been written can be read back as what was written.
func TestPrefabFilesRoundTrip(t *testing.T) {
	file := withPrefabFile(t, "street_lamp", lampPrefab())

	got, err := LoadPrefab(file)
	if err != nil {
		t.Fatalf("LoadPrefab: %v", err)
	}
	if len(got.Brushes) != 1 || got.Brushes[0].ID != "post" {
		t.Errorf("brushes = %+v", got.Brushes)
	}
	if len(got.Lights) != 1 || got.Lights[0].Radius != 8 {
		t.Errorf("lights = %+v", got.Lights)
	}
	if len(got.Volumes) != 1 || got.Volumes[0].Half.X != 4 {
		t.Errorf("volumes = %+v", got.Volumes)
	}

	// And the picker sees it.
	names := ScanPrefabNames()
	if len(names) != 1 || names[0] != file {
		t.Errorf("ScanPrefabNames = %v, want [%s]", names, file)
	}
	if _, err := os.Stat(filepath.Join(PrefabsDir(), file+".yaml")); err != nil {
		t.Errorf("the prefab file is not where PrefabFilePath says: %v", err)
	}
}
