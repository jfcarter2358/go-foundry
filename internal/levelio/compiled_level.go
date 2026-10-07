package levelio

// compiled_level.go — the artefact a shipped game loads instead of a level.
//
// An authored level is a list of brushes, and turning it into something
// drawable means running CSG, a BSP build, a visibility flood and a light bake,
// none of which a shipped game should be doing while the player waits. So the
// editor does it once and writes THIS: the geometry already reduced to
// world-space polygons with their lighting on them, the tree that locates a
// camera among them, and the bit array that says what to draw once it has been
// located.
//
// # What is compiled and what is not
//
// SURFACES are compiled. Brushes are not — they are still here, and they are
// the same brushes; they are just no longer what gets drawn. Collision resolves
// a body against a brush's own convex hull, a raycast wants its triangles, a
// script addresses it by name, and none of those has a compiled equivalent
// worth inventing (world/static.go argues this at length). Every chunk the
// compiler consumed is stored with Chunk.Compiled set, which is the draw loop's
// signal to leave it alone.
//
// Three kinds of chunk are deliberately NOT consumed, and are built the ordinary
// way at load — see compile.Bakes, which is the one place that rule is written:
//
//   - FRAME-OWNED chunks, whose geometry moves with a vehicle and so cannot be
//     a static world-space list.
//   - Chunks marked MOVES, which a script moves, for the same reason.
//   - DISABLED chunks, which a script can switch on later
//     (@chunk.prop.enabled) — unless marked STREAMED, in which case they are
//     baked, stored Compiled and disabled, and their surfaces start hidden.
//
// The corollary is worth knowing before it surprises someone: a disabled chunk
// that is not streamed takes no part in sealing the level. If one of them is a
// wall, the level leaks through the hole it leaves, and the compile says so.
//
// # What is stored as what
//
// Chunks are stored as RUNTIME values, not as the authored RawChunk form, so
// that loading a compiled level never goes near the authored brush format. An
// entity definition's own decks are the exception and are left out: they are
// stamped from that file at load exactly as a model or a texture is, which is
// what makes an entity type one thing rather than twenty frozen copies. (They
// are frame-owned, so the compiler never baked them either.)
//
// Prefabs, by contrast, are RESOLVED at compile time. A prefab's link exists so
// that editing one file updates twenty placements, which is an authoring
// concern; by the time a level is compiled it has done its job, and a build
// output that still had to go and read prefab files could compile differently
// from what was compiled.
//
// # How it is stored, and why a version stamp
//
// A level's YAML is a thing people read and edit; a compiled level is a thing
// only the engine reads, and it is mostly float triples and bits. It is stored
// as flat columns found by name (fbsp_binary.go) — the surfaces' points end to
// end in one array, their light in another — so that reading it back is a copy
// per column rather than a decode per float. It was one gob stream until
// version 10, and those files still load (readCompiledGob); gob was simply too
// slow for a level of any size, and wrote maps in a random order, so no two
// compiles of a level were the same bytes.
//
// What carried over from gob is matching by NAME: adding a column, or a field
// to the chunks' encoding, does not invalidate what is already on disk. The
// version stamp is for the changes that do — a field removed, or a meaning
// changed — where loading the old bytes into the new structs would succeed
// and be wrong.
//
// # Why the authored half is YAML inside the file
//
// gob does not send a field holding its zero value, and it flattens a pointer
// into what it points at — so a pointer to 0 or false is not sent either, and
// decodes as nil. The runtime values have no such fields. The authored form is
// full of them, and on purpose: a RawSprite's Alpha is a *float32 exactly so
// that `alpha: 0` and "no alpha, meaning 1" stay distinct, and gob collapses
// the two. So Raw goes to disk as the YAML a level file is written as — the
// encoding every one of those pointers was designed around — and comes back
// through the same yaml.Unmarshal the level loader uses. The chunks' encoding
// (internal/binenc) keeps gob's rules so that a file of either format decodes to
// the same values; compiledFile is the shape the gob format encoded.

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"io"
	"os"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"

	"foundry/internal/binenc"
	"foundry/internal/bsp"
	"foundry/internal/compile"
	"foundry/internal/fileutil"
	"foundry/internal/world"
)

// CompiledExt is the extension a compiled level carries, beside the .yaml it
// was built from.
const CompiledExt = ".fbsp"

// CompiledVersion is bumped whenever old bytes would load into the current
// structs without complaining and mean something different.
//
// 3: the authored half is stored as YAML. A version-2 file decodes with that
// half missing entirely, rather than failing.
//
// 4: Compiled can be set on a disabled chunk (a streamed one, baked hidden),
// and a chunk marked moves is no longer baked. A version-3 file would load a
// streamed area as a live disabled chunk and a moving one frozen in place.
//
// 5: the lighting model is one-sided, wrapped and hemispheric, and faces carry
// their smoothed normals and a bake per side and per shading (compile.Face's
// SmoothPoly/SmoothCorner, SmoothLight, BackLight). A version-4 file would
// load lit by abs(N·L) and could not follow the Smooth Lighting setting.
//
// 6: faces carry the indirect part of their bake (compile.Face.Indirect and its
// siblings), which live GI takes back off before adding its own. A version-5
// file would load with none, and every surface would count its baked ambient
// and bounce twice with GI on — so it is refused, and recompiled.
//
// 7: a face's sun share (compile.Face.SunShare) is -1 where the sun was not
// baked into its chunk (bake_exclude), where it used to be 0 — which now reads
// as "the bake shadowed the sun here", and the renderer puts the sun back
// wherever its cascades see the point lit. A version-6 file would light every
// excluded chunk with the sun its author kept out of it.
//
// 8: the surfaces' draw groups carry their cut into clusters (Clusters), for
// the GPU-driven cull (docs/nanite/PHASE_1.md). A version-7 file would still
// draw — the renderer cuts any group it has no cut for at load — but would pay
// for the cut on every load, so it is asked to recompile like any other.
//
// 9: a large opaque draw group carries a cluster DAG instead of a flat cut
// (StaticClusterGroup.DAG; docs/nanite/PHASE_2.md), so a compiled level draws
// its far surfaces simplified. A version-8 file would draw every surface at
// full detail at any distance.
//
// 10: the file is flat little-endian columns behind a directory rather than
// one gob stream (fbsp_binary.go), which is what took a large level's read
// from seconds to a fraction of one. Nothing it MEANS changed, so a version-9
// gob file is still read (legacyGobVersion) — it just reads slowly until the
// level is next compiled.
const CompiledVersion = 10

// CompiledVersionStamp is CompiledVersion spelled where a release build can
// find it in a game binary's bytes (release.PlayerLevelVersion): a prebuilt
// player left in a project's runtimes/ from an older Foundry would otherwise
// ship, refuse every level the editor compiled, and close on the player. It is
// a literal because Go cannot format a constant at compile time; a test holds
// it to CompiledVersion, and ReadCompiledLevel uses it so the linker keeps it.
const CompiledVersionStamp = "FOUNDRY-FBSP-VERSION=10;"

// compiledMagic is the first thing in the file, so that a truncated or
// unrelated file is rejected as such rather than as a gob decode failure
// halfway through.
const compiledMagic = "FOUNDRYBSP"

// CompiledLevel is a level with its static geometry already compiled.
type CompiledLevel struct {
	Version int

	// Faces are world-space, with baked per-vertex light. The cells of Tree
	// index into this list.
	Faces []compile.Face
	Tree  *bsp.Tree
	PVS   *bsp.PVS

	// Chunks are the level's chunks as runtime values. See the file comment on
	// which are here and why they are here at all.
	Chunks []world.Chunk

	// Raw is everything else the level is made of: entities, volumes, lights,
	// the spawn, scripts, UI, sprites, emitters, scenery, sounds, sky and fog.
	// Its own Chunks and Prefabs are empty — both have been resolved into the
	// fields around it.
	Raw world.RawLevel

	// Stats is what the compile reported, kept so a level can be asked what it
	// cost without recompiling it.
	Stats compile.Stats

	// Clusters is each draw group of the surfaces cut into clusters
	// (world/static_clusters.go): what the GPU-driven cull culls, made once
	// here rather than on every load.
	Clusters []world.StaticClusterGroup
}

// CompileLevel runs the compiler over a loaded level and returns the artefact.
// The level is not modified.
func CompileLevel(lvl *world.Level, opt compile.Options) (*CompiledLevel, error) {
	res, err := compile.Compile(lvl, opt)
	if err != nil {
		return nil, err
	}
	raw, err := LevelToRawExpanded(lvl)
	if err != nil {
		return nil, err
	}
	raw.Chunks = nil
	raw.Prefabs = nil

	c := &CompiledLevel{
		Version: CompiledVersion,
		Faces:   res.Tree.Faces,
		Tree:    res.Tree.Tree,
		PVS:     res.Vis,
		Chunks:  storableChunks(lvl),
		Raw:     raw,
		Stats:   res.Stats,
	}
	clusters, err := CutCompiledClusters(c)
	if err != nil {
		return nil, err
	}
	c.Clusters = clusters
	return c, nil
}

// CutCompiledClusters cuts a compiled level's surfaces into clusters — the
// last stage of a compile. It stands the surfaces up exactly as a load would
// (CPU-only, so no GL context is needed) and cuts the groups the renderer will
// draw them in.
func CutCompiledClusters(c *CompiledLevel) ([]world.StaticClusterGroup, error) {
	static, err := StaticGeometryFrom(c)
	if err != nil {
		return nil, err
	}
	return world.BuildStaticClusters(static), nil
}

// CompileLevelFile reads a level from disk with no GL context and compiles it.
//
// The entry point for the command-line compiler and for anything else running
// as a build step. It goes through the real loader (LoadLevelHeadless), so the
// level it compiles is exactly the one the engine would have loaded — prefabs
// stamped, entity decks materialised, IDs uniqued — rather than a second,
// simpler reading of the same file that would eventually disagree with it.
func CompileLevelFile(path string, opt compile.Options) (*CompiledLevel, error) {
	lvl, err := LoadLevelHeadless(path)
	if err != nil {
		return nil, err
	}
	return CompileLevel(&lvl, opt)
}

// storableChunks is the level's chunks as the compiled file keeps them.
//
// The runtime-only fields are cleared rather than carried: geometry handles
// belong to the process that made them, and a frame transform is republished
// every tick by RefreshFrames.
func storableChunks(lvl *world.Level) []world.Chunk {
	var out []world.Chunk
	for ci := range lvl.Chunks {
		src := &lvl.Chunks[ci]
		if src.FromEntity != "" {
			continue // stamped from the entity definition at load; see above
		}
		ch := *src
		ch.Compiled = compile.Bakes(src)
		ch.Unloaded = false
		ch.Xform, ch.FrameScale = mgl32.Mat4{}, 0
		// The prefab that put it here has been resolved away, so the marker
		// would name a placement the compiled level no longer has.
		ch.FromPrefab = ""
		out = append(out, ch)
	}
	return out
}

// compiledFile is a CompiledLevel as the gob format (version 9 and before)
// encoded it: the same fields, with the authored half as YAML. See the file
// comment on why. Only read now; nothing writes it.
type compiledFile struct {
	Version  int
	Faces    []compile.Face
	Tree     *bsp.Tree
	PVS      *bsp.PVS
	Chunks   []world.Chunk
	RawYAML  []byte
	Stats    compile.Stats
	Clusters []world.StaticClusterGroup
}

// SaveCompiledLevel writes a compiled level to path, in the columnar format.
func SaveCompiledLevel(c *CompiledLevel, path string) error {
	data, err := encodeCompiledLevel(c)
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, data)
}

// encodeCompiledLevel is the file SaveCompiledLevel writes. The same level
// always encodes to the same bytes.
func encodeCompiledLevel(c *CompiledLevel) ([]byte, error) {
	rawYAML, err := yaml.Marshal(c.Raw)
	if err != nil {
		return nil, fmt.Errorf("encoding compiled level's authored half: %w", err)
	}
	chunks, err := binenc.Marshal(&c.Chunks)
	if err != nil {
		return nil, fmt.Errorf("encoding compiled level's chunks: %w", err)
	}
	stats, err := binenc.Marshal(&c.Stats)
	if err != nil {
		return nil, fmt.Errorf("encoding compiled level's stats: %w", err)
	}
	w := &colWriter{strs: newStringTable()}
	writeFaces(w, c.Faces)
	if err := writeTree(w, c.Tree); err != nil {
		return nil, err
	}
	writePVS(w, c.PVS)
	if err := writeClusters(w, c.Clusters); err != nil {
		return nil, err
	}
	w.bytesCol("chunks", chunks)
	w.bytesCol("stats", stats)
	w.bytesCol("raw.yaml", rawYAML)
	// Last, because the columns above are what fill it.
	w.writeStrings()
	return w.assemble(c.Version), nil
}

// ReadCompiledLevel reads one back, without touching the GPU.
func ReadCompiledLevel(path string) (*CompiledLevel, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return decodeCompiledLevel(path, data)
}

// CheckCompiledLevel is the part of ReadCompiledLevel that costs nothing:
// that path exists, is a compiled level and — for the columnar format, whose
// version is in the header — was compiled by a version this build reads. It
// reads only the header.
//
// It exists for BeginLoadCompiled, which decodes the rest off the main thread
// but still owes its caller an immediate error for a level that is plainly not
// going to load, before a loading screen goes up over it.
func CheckCompiledLevel(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, binaryHeaderSize)
	n, _ := io.ReadFull(f, head)
	return checkCompiledHeader(path, head[:n])
}

func checkCompiledHeader(path string, head []byte) error {
	if len(head) < len(compiledMagic) || string(head[:len(compiledMagic)]) != compiledMagic {
		return fmt.Errorf("%s is not a compiled level", path)
	}
	if v, ok := binaryVersion(head); ok && v != CompiledVersion {
		return staleCompiledError(path, v)
	}
	return nil
}

func staleCompiledError(path string, v int) error {
	return fmt.Errorf("%s was compiled by version %d of the compiler; this build wants %d (%s) — recompile the level, or rebuild the game binary with the editor that compiled it",
		path, v, CompiledVersion, CompiledVersionStamp)
}

// decodeCompiledLevel decodes a whole compiled-level file of either format.
func decodeCompiledLevel(path string, data []byte) (*CompiledLevel, error) {
	if err := checkCompiledHeader(path, data); err != nil {
		return nil, err
	}
	if isBinaryCompiled(data) {
		return readCompiledBinary(data)
	}
	return readCompiledGob(path, data)
}

// readCompiledBinary decodes the columnar format. The parts are independent —
// each reads its own columns into its own values — so they decode at once,
// which on a large level is the surfaces on one core while the chunks, the
// tree and the authored half take others.
func readCompiledBinary(data []byte) (*CompiledLevel, error) {
	f, err := parseColumns(data)
	if err != nil {
		return nil, err
	}
	c := &CompiledLevel{Version: f.version}
	var chunks, stats, raw []byte
	if chunks, err = f.bytes("chunks"); err != nil {
		return nil, err
	}
	if stats, err = f.bytes("stats"); err != nil {
		return nil, err
	}
	if raw, err = f.bytes("raw.yaml"); err != nil {
		return nil, err
	}
	parts := []func() error{
		func() (err error) { c.Faces, err = readFaces(f); return },
		func() (err error) { c.Tree, err = readTree(f); return },
		func() (err error) { c.PVS, err = readPVS(f); return },
		func() (err error) { c.Clusters, err = readClusters(f); return },
		func() error {
			if len(chunks) == 0 {
				return nil
			}
			if err := binenc.Unmarshal(chunks, &c.Chunks); err != nil {
				return fmt.Errorf("decoding compiled level's chunks: %w", err)
			}
			return nil
		},
		func() error {
			if len(stats) == 0 {
				return nil
			}
			if err := binenc.Unmarshal(stats, &c.Stats); err != nil {
				return fmt.Errorf("decoding compiled level's stats: %w", err)
			}
			return nil
		},
		func() error {
			if err := yaml.Unmarshal(raw, &c.Raw); err != nil {
				return fmt.Errorf("decoding compiled level's authored half: %w", err)
			}
			return nil
		},
	}
	if err := runAll(parts); err != nil {
		return nil, err
	}
	return c, nil
}

// readCompiledGob decodes a file from before the columnar format: one gob
// stream of compiledFile after the magic. Only legacyGobVersion is accepted —
// anything older means something else, and is asked to recompile as it always
// was.
func readCompiledGob(path string, data []byte) (*CompiledLevel, error) {
	var f compiledFile
	if err := gob.NewDecoder(bytes.NewReader(data[len(compiledMagic):])).Decode(&f); err != nil {
		return nil, fmt.Errorf("decoding compiled level: %w", err)
	}
	if f.Version != legacyGobVersion {
		return nil, staleCompiledError(path, f.Version)
	}
	c := CompiledLevel{
		Version:  f.Version,
		Faces:    f.Faces,
		Tree:     f.Tree,
		PVS:      f.PVS,
		Chunks:   f.Chunks,
		Stats:    f.Stats,
		Clusters: f.Clusters,
	}
	if err := yaml.Unmarshal(f.RawYAML, &c.Raw); err != nil {
		return nil, fmt.Errorf("decoding compiled level's authored half: %w", err)
	}
	return &c, nil
}

// CompiledPath is where the compiled form of a level file lives: beside it,
// under the same name. Keeping them together means "which level is this" has
// one answer, and a stale compiled file is visible in the same directory
// listing as the source it no longer matches.
func CompiledPath(levelPath string) string {
	return trimLevelExt(levelPath) + CompiledExt
}

func trimLevelExt(p string) string {
	if n := len(p) - len(LevelExt); n > 0 && p[n:] == LevelExt {
		return p[:n]
	}
	return p
}
