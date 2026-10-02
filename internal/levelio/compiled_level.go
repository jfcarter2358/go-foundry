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
// # Why gob, and why a version stamp
//
// A level's YAML is a thing people read and edit; a compiled level is a thing
// only the engine reads, and it is mostly float triples and bits. gob keeps it
// to one dependency-free encode of the structs that already exist, and encodes
// by field NAME, so adding a field to any of them does not invalidate what is
// already on disk. The version stamp is for the changes that do — a field
// removed, or a meaning changed — where loading the old bytes into the new
// structs would succeed and be wrong.
//
// # Why the authored half is YAML inside the gob
//
// gob does not send a field holding its zero value, and it flattens a pointer
// into what it points at — so a pointer to 0 or false is not sent either, and
// decodes as nil. The runtime values have no such fields. The authored form is
// full of them, and on purpose: a RawSprite's Alpha is a *float32 exactly so
// that `alpha: 0` and "no alpha, meaning 1" stay distinct, and gob collapses
// the two. So Raw goes to disk as the YAML a level file is written as — the
// encoding every one of those pointers was designed around — and comes back
// through the same yaml.Unmarshal the level loader uses. compiledFile is the
// shape that is actually encoded.

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"os"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"

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
const CompiledVersion = 9

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

// compiledFile is a CompiledLevel as it is encoded: the same fields, with the
// authored half as YAML. See the file comment on why.
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

// SaveCompiledLevel writes a compiled level to path.
func SaveCompiledLevel(c *CompiledLevel, path string) error {
	rawYAML, err := yaml.Marshal(c.Raw)
	if err != nil {
		return fmt.Errorf("encoding compiled level's authored half: %w", err)
	}
	f := compiledFile{
		Version:  c.Version,
		Faces:    c.Faces,
		Tree:     c.Tree,
		PVS:      c.PVS,
		Chunks:   c.Chunks,
		RawYAML:  rawYAML,
		Stats:    c.Stats,
		Clusters: c.Clusters,
	}
	var buf bytes.Buffer
	buf.WriteString(compiledMagic)
	if err := gob.NewEncoder(&buf).Encode(&f); err != nil {
		return fmt.Errorf("encoding compiled level: %w", err)
	}
	return fileutil.WriteFileAtomic(path, buf.Bytes())
}

// ReadCompiledLevel reads one back, without touching the GPU.
func ReadCompiledLevel(path string) (*CompiledLevel, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < len(compiledMagic) || string(data[:len(compiledMagic)]) != compiledMagic {
		return nil, fmt.Errorf("%s is not a compiled level", path)
	}
	var f compiledFile
	if err := gob.NewDecoder(bytes.NewReader(data[len(compiledMagic):])).Decode(&f); err != nil {
		return nil, fmt.Errorf("decoding compiled level: %w", err)
	}
	if f.Version != CompiledVersion {
		return nil, fmt.Errorf("%s was compiled by version %d of the compiler; this build wants %d — recompile the level",
			path, f.Version, CompiledVersion)
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
