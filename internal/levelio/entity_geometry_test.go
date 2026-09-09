package levelio

// entity_geometry_test.go — an entity type's own solid, from the definition
// that holds it to the level it ends up in and back out again.
//
// The two rules worth pinning are opposites of each other. The materialised
// chunk has to be *fully* in the level — bound to the entity's frame, uploaded,
// indexed — or a ship's deck is not something the player can stand on. And it
// has to be *entirely* absent from the saved file, or the level freezes a copy
// of the hull into itself and stops tracking the definition.

import (
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/testenv"
	"foundry/internal/world"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// squareRawChunk is one hollow 10x10x4 room in raw form — the fixture the
// migration tests share.
func squareRawChunk(_ bool) world.RawChunk {
	brushes := make([]world.RawBrush, 0, 6)
	for _, b := range roomBrushes() {
		brushes = append(brushes, world.BrushToRaw(&b))
	}
	return world.RawChunk{ID: "chunk", Brushes: brushes}
}

// roomBrushes is that same room as live brushes, for the entities that carry
// one directly.
func roomBrushes() []world.Brush {
	return world.NewHollowRoom("room", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{10, 4, 10}, 0.5, "concrete")
}

// withInstallRoot points the install root at a temporary directory and makes
// chunks/ inside it, for the tests that write files.
func withInstallRoot(t *testing.T) string {
	t.Helper()
	testenv.WithoutGeometryUploads(t)

	prev := fileutil.ExecutablePath
	root := t.TempDir()
	fileutil.ExecutablePath = root
	t.Cleanup(func() { fileutil.ExecutablePath = prev })

	if err := os.MkdirAll(filepath.Join(root, ChunksDirName), 0o755); err != nil {
		t.Fatalf("mkdir chunks: %v", err)
	}
	return root
}

// withChunkFile writes one standalone chunk file into chunks/ and returns its
// name — what the entity editor's import reads, now that nothing references one
// at run time.
func withChunkFile(t *testing.T, name string) string {
	t.Helper()
	withInstallRoot(t)
	data, err := yaml.Marshal(squareRawChunk(false))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(ChunkFilePath(name), data, 0o644); err != nil {
		t.Fatalf("write chunk file: %v", err)
	}
	return name
}

// shipWithDeck is an entity whose own brushes are a room.
func shipWithDeck(id string) world.Entity {
	e := world.NewBlankEntity(id)
	e.Brushes = roomBrushes()
	return e
}

func TestMaterializeEntityGeometryBindsTheFrame(t *testing.T) {
	withInstallRoot(t)

	lvl := &world.Level{}
	lvl.Entities = append(lvl.Entities, shipWithDeck("shuttle_1"))
	if err := MaterializeEntityGeometry(lvl, &lvl.Entities[0], map[string]bool{}); err != nil {
		t.Fatalf("MaterializeEntityGeometry: %v", err)
	}

	if len(lvl.Chunks) != 1 {
		t.Fatalf("materialised %d chunks, want 1", len(lvl.Chunks))
	}
	ch := &lvl.Chunks[0]
	if ch.ID != "shuttle_1.geometry" {
		t.Errorf("chunk ID %q, want shuttle_1.geometry", ch.ID)
	}
	if ch.FrameOwner != "shuttle_1" {
		t.Errorf("FrameOwner %q — the solid is not carried by anything", ch.FrameOwner)
	}
	if ch.FromEntity != "shuttle_1" {
		t.Errorf("FromEntity %q — saving would write this chunk into the level", ch.FromEntity)
	}
	// The entity's own position places the geometry, through the frame; the
	// chunk must not also carry a placement of its own, or the two compose.
	if ch.Origin != (mgl32.Vec3{}) {
		t.Errorf("origin %v, want the entity's frame to be the only placement", ch.Origin)
	}
	// The frame the solid hangs off has to actually exist, or a level with one
	// refuses to open.
	if _, err := world.BuildFrames(lvl); err != nil {
		t.Fatalf("BuildFrames: %v", err)
	}
}

// TestTwoInstancesGetDistinctChunkIDs is why the ID is qualified by the entity:
// `@id` has to name exactly one thing, and two ships from one definition would
// otherwise both call their solid "geometry".
func TestTwoInstancesGetDistinctChunkIDs(t *testing.T) {
	withInstallRoot(t)

	lvl := &world.Level{}
	lvl.Entities = append(lvl.Entities, shipWithDeck("shuttle_1"), shipWithDeck("shuttle_2"))
	for i := range lvl.Entities {
		if err := MaterializeEntityGeometry(lvl, &lvl.Entities[i], map[string]bool{}); err != nil {
			t.Fatalf("MaterializeEntityGeometry: %v", err)
		}
	}
	if len(lvl.Chunks) != 2 {
		t.Fatalf("materialised %d chunks, want 2", len(lvl.Chunks))
	}
	if lvl.Chunks[0].ID == lvl.Chunks[1].ID {
		t.Errorf("both solids are called %q", lvl.Chunks[0].ID)
	}
	// Brush IDs too: they are what a script addresses, and what a `dynamic`
	// write has to resolve to exactly one of.
	a, b := lvl.Chunks[0].Brushes[0].ID, lvl.Chunks[1].Brushes[0].ID
	if a == b {
		t.Errorf("both solids' brushes are called %q", a)
	}
	if !strings.HasPrefix(a, "shuttle_1.") {
		t.Errorf("brush %q is not qualified by the entity that carries it", a)
	}
}

// TestSaveSkipsCarriedGeometry is the rule that keeps a definition
// authoritative.
func TestSaveSkipsCarriedGeometry(t *testing.T) {
	withInstallRoot(t)

	lvl := &world.Level{}
	lvl.Entities = append(lvl.Entities, shipWithDeck("shuttle_1"))
	if err := MaterializeEntityGeometry(lvl, &lvl.Entities[0], map[string]bool{}); err != nil {
		t.Fatalf("MaterializeEntityGeometry: %v", err)
	}
	// A chunk the level itself authored, to prove the skip is selective.
	own, _, err := loadRawChunk(squareRawChunk(false))
	if err != nil {
		t.Fatalf("loadRawChunk: %v", err)
	}
	own.ID = "ground"
	lvl.Chunks = append(lvl.Chunks, own)

	raw, err := LevelToRaw(lvl)
	if err != nil {
		t.Fatalf("LevelToRaw: %v", err)
	}
	if len(raw.Chunks) != 1 {
		t.Fatalf("wrote %d chunks, want only the level's own", len(raw.Chunks))
	}
	if raw.Chunks[0].ID != "ground" {
		t.Errorf("wrote %q, want ground", raw.Chunks[0].ID)
	}
	// The entity is typeless, so its brushes ARE the durable copy and are
	// written with it.
	if len(raw.Entities) != 1 || len(raw.Entities[0].Brushes) == 0 {
		t.Fatalf("the entity's own brushes were not written: %+v", raw.Entities)
	}
	out, err := yaml.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "shuttle_1.geometry") {
		t.Error("the materialised chunk leaked into the saved level")
	}
}

func TestDropEntityGeometryRemovesOnlyThatEntitys(t *testing.T) {
	withInstallRoot(t)

	lvl := &world.Level{}
	lvl.Entities = append(lvl.Entities, shipWithDeck("shuttle_1"), shipWithDeck("shuttle_2"))
	for i := range lvl.Entities {
		if err := MaterializeEntityGeometry(lvl, &lvl.Entities[i], map[string]bool{}); err != nil {
			t.Fatalf("MaterializeEntityGeometry: %v", err)
		}
	}
	if n := DropEntityGeometry(lvl, "shuttle_1"); n != 1 {
		t.Fatalf("dropped %d chunks, want 1", n)
	}
	if len(lvl.Chunks) != 1 || lvl.Chunks[0].FromEntity != "shuttle_2" {
		t.Fatalf("left %+v behind", lvl.Chunks)
	}
	if n := DropEntityGeometry(lvl, "nobody"); n != 0 {
		t.Errorf("dropping an unknown entity's geometry removed %d", n)
	}
}

// TestEntityWithNoBrushesMaterialisesNothing covers the ordinary case — a
// barrel — where an empty chunk would be a frame nothing needs and one more
// thing for BuildFrames to resolve.
func TestEntityWithNoBrushesMaterialisesNothing(t *testing.T) {
	withInstallRoot(t)

	lvl := &world.Level{}
	lvl.Entities = append(lvl.Entities, world.NewBlankEntity("barrel"))
	if err := MaterializeEntityGeometry(lvl, &lvl.Entities[0], map[string]bool{}); err != nil {
		t.Fatalf("an entity with no brushes reported %v", err)
	}
	if len(lvl.Chunks) != 0 {
		t.Errorf("built %d chunks for an entity with no brushes", len(lvl.Chunks))
	}
}

func TestScanChunkNamesTrimsTheExtension(t *testing.T) {
	withChunkFile(t, "shuttle_hull")
	names := ScanChunkNames()
	if len(names) != 1 || names[0] != "shuttle_hull" {
		t.Fatalf("ScanChunkNames = %v, want [shuttle_hull]", names)
	}
	// Both spellings have to resolve: the picker stores the trimmed name, and
	// an author typing the file name should not be a silent miss.
	if ChunkFilePath("shuttle_hull") != ChunkFilePath("shuttle_hull.yaml") {
		t.Error("a name with and without its extension resolve to different files")
	}
}

// TestLevelLoadMaterializesCarriedGeometry is the end-to-end path: a definition
// on disk holding brushes, a level that places one, and a load that produces a
// chunk the player can stand on.
//
// It goes through rawToLevel rather than calling the materialiser directly,
// because the wiring inside loadLevelObjects — materialising after every entity
// exists, and feeding the textures into the pass that loads them — is the part
// that has nowhere else to be tested.
func TestLevelLoadMaterializesCarriedGeometry(t *testing.T) {
	root := withInstallRoot(t)

	// The definition, written where LoadEntityDef will find it. Its brushes are
	// written in the level's own brush format, which is the point of reusing
	// RawBrush rather than inventing a second schema.
	if err := os.MkdirAll(filepath.Join(root, world.EntitiesDirName), 0o755); err != nil {
		t.Fatalf("mkdir entities: %v", err)
	}
	rawDef := struct {
		ID      string           `yaml:"id"`
		Brushes []world.RawBrush `yaml:"brushes"`
	}{ID: "shuttle"}
	for _, b := range roomBrushes() {
		rawDef.Brushes = append(rawDef.Brushes, world.BrushToRaw(&b))
	}
	def, err := yaml.Marshal(rawDef)
	if err != nil {
		t.Fatalf("marshal definition: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, world.EntitiesDirName, "shuttle.yaml"), def, 0o644); err != nil {
		t.Fatalf("write definition: %v", err)
	}
	world.ReloadEntityDefs()
	t.Cleanup(world.ReloadEntityDefs)

	// The fixture names "concrete"; register it so the loader's existence check
	// passes without a textures/ folder, as the other end-to-end level tests
	// here do.
	if gfx.Textures == nil {
		gfx.Textures = map[string]uint32{}
	}
	if gfx.TextureNames == nil {
		gfx.TextureNames = map[string]string{}
	}
	gfx.Textures["concrete"], gfx.TextureNames["concrete"] = 1, "concrete"

	raw := world.RawLevel{Entities: []world.RawEntity{{Type: "shuttle", ID: "shuttle_1"}}}
	lvl, err := rawToLevel(raw)
	if err != nil {
		t.Fatalf("rawToLevel: %v", err)
	}

	if len(lvl.Chunks) != 1 {
		t.Fatalf("loaded %d chunks, want the one the type carries", len(lvl.Chunks))
	}
	if lvl.Chunks[0].FrameOwner != "shuttle_1" || lvl.Chunks[0].FromEntity != "shuttle_1" {
		t.Errorf("chunk came back as %+v", lvl.Chunks[0])
	}
	// finishLevel builds the frames, so the solid is already carried.
	if lvl.Frames.Frame("shuttle_1") == nil {
		t.Error("the entity has no frame; nothing would carry its solid")
	}

	// And saving gives back exactly what was loaded: the placement, not the
	// geometry.
	back, err := LevelToRaw(&lvl)
	if err != nil {
		t.Fatalf("LevelToRaw: %v", err)
	}
	if len(back.Chunks) != 0 {
		t.Errorf("saving wrote %d chunks, want none", len(back.Chunks))
	}
	if len(back.Entities) != 1 || back.Entities[0].Type != "shuttle" {
		t.Fatalf("entities came back as %+v", back.Entities)
	}
	if len(back.Entities[0].Brushes) != 0 {
		t.Errorf("an untouched instance restated its type's geometry: %d brushes",
			len(back.Entities[0].Brushes))
	}
}
