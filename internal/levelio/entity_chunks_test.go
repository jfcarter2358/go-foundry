package levelio

// entity_chunks_test.go — an entity type's carried geometry, from the file it
// names to the level it ends up in and back out again.
//
// The two rules worth pinning are opposites of each other. A materialised chunk
// has to be *fully* in the level — bound to the entity's frame, uploaded,
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

// withChunkFile points the install root at a temporary directory and writes one
// standalone chunk file into chunks/, returning its name.
func withChunkFile(t *testing.T, name string) string {
	t.Helper()
	testenv.WithoutGeometryUploads(t)

	prev := fileutil.ExecutablePath
	root := t.TempDir()
	fileutil.ExecutablePath = root
	t.Cleanup(func() { fileutil.ExecutablePath = prev })

	if err := os.MkdirAll(filepath.Join(root, ChunksDirName), 0o755); err != nil {
		t.Fatalf("mkdir chunks: %v", err)
	}
	data, err := yaml.Marshal(squareRawChunk(false))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(ChunkFilePath(name), data, 0o644); err != nil {
		t.Fatalf("write chunk file: %v", err)
	}
	return name
}

// shipWithDeck is an entity carrying one chunk attachment.
func shipWithDeck(id, file string) world.Entity {
	e := world.NewBlankEntity(id)
	e.Chunks = []world.EntityChunk{{
		Name:   "hull",
		File:   file,
		Offset: mgl32.Vec3{0, 2, 0},
		Scale:  1,
	}}
	return e
}

func TestMaterializeEntityChunksBindsTheFrame(t *testing.T) {
	file := withChunkFile(t, "shuttle_hull")

	lvl := &world.Level{}
	lvl.Entities = append(lvl.Entities, shipWithDeck("shuttle_1", file))
	if errs := MaterializeEntityChunks(lvl, &lvl.Entities[0], map[string]bool{}); len(errs) > 0 {
		t.Fatalf("MaterializeEntityChunks: %v", errs)
	}

	if len(lvl.Chunks) != 1 {
		t.Fatalf("materialised %d chunks, want 1", len(lvl.Chunks))
	}
	ch := &lvl.Chunks[0]
	if ch.ID != "shuttle_1.hull" {
		t.Errorf("chunk ID %q, want shuttle_1.hull", ch.ID)
	}
	if ch.FrameOwner != "shuttle_1" {
		t.Errorf("FrameOwner %q — the deck is not carried by anything", ch.FrameOwner)
	}
	if ch.FromEntity != "shuttle_1" {
		t.Errorf("FromEntity %q — saving would write this chunk into the level", ch.FromEntity)
	}
	if ch.Origin != (mgl32.Vec3{0, 2, 0}) {
		t.Errorf("origin %v, want the attachment's offset {0 2 0}", ch.Origin)
	}
	// The frame the deck hangs off has to actually exist, or a level with one
	// refuses to open.
	if _, err := world.BuildFrames(lvl); err != nil {
		t.Fatalf("BuildFrames: %v", err)
	}
}

// TestTwoInstancesGetDistinctChunkIDs is why the ID is qualified by the entity:
// `@id` has to name exactly one thing, and two ships from one definition would
// otherwise both call their deck "hull".
func TestTwoInstancesGetDistinctChunkIDs(t *testing.T) {
	file := withChunkFile(t, "shuttle_hull")

	lvl := &world.Level{}
	lvl.Entities = append(lvl.Entities, shipWithDeck("shuttle_1", file), shipWithDeck("shuttle_2", file))
	for i := range lvl.Entities {
		if errs := MaterializeEntityChunks(lvl, &lvl.Entities[i], map[string]bool{}); len(errs) > 0 {
			t.Fatalf("MaterializeEntityChunks: %v", errs)
		}
	}
	if len(lvl.Chunks) != 2 {
		t.Fatalf("materialised %d chunks, want 2", len(lvl.Chunks))
	}
	if lvl.Chunks[0].ID == lvl.Chunks[1].ID {
		t.Errorf("both decks are called %q", lvl.Chunks[0].ID)
	}
	// Sector IDs too: they are what a script and the portal graph address.
	a, b := lvl.Chunks[0].Sectors[0].ID, lvl.Chunks[1].Sectors[0].ID
	if a == b {
		t.Errorf("both decks' sectors are called %q", a)
	}
}

// TestSaveSkipsCarriedChunks is the rule that keeps a definition authoritative.
func TestSaveSkipsCarriedChunks(t *testing.T) {
	file := withChunkFile(t, "shuttle_hull")

	lvl := &world.Level{}
	lvl.Entities = append(lvl.Entities, shipWithDeck("shuttle_1", file))
	if errs := MaterializeEntityChunks(lvl, &lvl.Entities[0], map[string]bool{}); len(errs) > 0 {
		t.Fatalf("MaterializeEntityChunks: %v", errs)
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
	// And the attachment itself is written, since that is the durable half.
	if len(raw.Entities) != 1 || len(raw.Entities[0].Chunks) != 1 {
		t.Fatalf("the entity's chunk attachment was not written: %+v", raw.Entities)
	}
	out, err := yaml.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "shuttle_1.hull") {
		t.Error("the materialised chunk leaked into the saved level")
	}
}

func TestDropEntityChunksRemovesOnlyThatEntitys(t *testing.T) {
	file := withChunkFile(t, "shuttle_hull")

	lvl := &world.Level{}
	lvl.Entities = append(lvl.Entities, shipWithDeck("shuttle_1", file), shipWithDeck("shuttle_2", file))
	for i := range lvl.Entities {
		MaterializeEntityChunks(lvl, &lvl.Entities[i], map[string]bool{})
	}
	if n := DropEntityChunks(lvl, "shuttle_1"); n != 1 {
		t.Fatalf("dropped %d chunks, want 1", n)
	}
	if len(lvl.Chunks) != 1 || lvl.Chunks[0].FromEntity != "shuttle_2" {
		t.Fatalf("left %+v behind", lvl.Chunks)
	}
	if n := DropEntityChunks(lvl, "nobody"); n != 0 {
		t.Errorf("dropping an unknown entity's chunks removed %d", n)
	}
}

// TestMissingChunkFileIsReportedNotFatal: a hole in the floor is fixable and
// visible; a level that will not open over one missing file is not.
func TestMissingChunkFileIsReportedNotFatal(t *testing.T) {
	withChunkFile(t, "shuttle_hull")

	lvl := &world.Level{}
	lvl.Entities = append(lvl.Entities, shipWithDeck("shuttle_1", "no_such_chunk"))
	errs := MaterializeEntityChunks(lvl, &lvl.Entities[0], map[string]bool{})
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1", len(errs))
	}
	if len(lvl.Chunks) != 0 {
		t.Errorf("a chunk was built from a file that is not there")
	}
}

// TestAttachmentWithNoFileIsSkipped covers the state an attachment starts in:
// added in the editor, not yet pointed at anything.
func TestAttachmentWithNoFileIsSkipped(t *testing.T) {
	withChunkFile(t, "shuttle_hull")

	lvl := &world.Level{}
	e := world.NewBlankEntity("lift")
	e.Chunks = []world.EntityChunk{{Name: "car"}}
	lvl.Entities = append(lvl.Entities, e)

	if errs := MaterializeEntityChunks(lvl, &lvl.Entities[0], map[string]bool{}); len(errs) != 0 {
		t.Fatalf("an empty attachment reported %v", errs)
	}
	if len(lvl.Chunks) != 0 {
		t.Errorf("built %d chunks from an attachment with no file", len(lvl.Chunks))
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

// TestLevelLoadMaterializesCarriedChunks is the end-to-end path: a definition
// on disk carrying geometry, a level that places one, and a load that produces
// a chunk the player can stand on.
//
// It goes through rawToLevel rather than calling the materialiser directly,
// because the wiring inside loadLevelObjects — materialising after every entity
// exists, and feeding the textures into the pass that loads them — is the part
// that has nowhere else to be tested.
func TestLevelLoadMaterializesCarriedChunks(t *testing.T) {
	file := withChunkFile(t, "shuttle_hull")

	// The definition, written where LoadEntityDef will find it.
	root := fileutil.ExecutablePath
	if err := os.MkdirAll(filepath.Join(root, world.EntitiesDirName), 0o755); err != nil {
		t.Fatalf("mkdir entities: %v", err)
	}
	def := "id: shuttle\nchunks:\n  - name: hull\n    file: " + file + "\n"
	if err := os.WriteFile(filepath.Join(root, world.EntitiesDirName, "shuttle.yaml"), []byte(def), 0o644); err != nil {
		t.Fatalf("write definition: %v", err)
	}
	world.ReloadEntityDefs()
	t.Cleanup(world.ReloadEntityDefs)

	// The fixture chunk names "concrete"; register it so the loader's existence
	// check passes without a textures/ folder, as the other end-to-end level
	// tests here do.
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
	// finishLevel builds the frames, so the deck is already carried.
	if lvl.Frames.Frame("shuttle_1") == nil {
		t.Error("the entity has no frame; nothing would carry its deck")
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
	if len(back.Entities[0].Chunks) != 0 {
		t.Errorf("an untouched instance restated the type's geometry: %+v", back.Entities[0].Chunks)
	}
}
