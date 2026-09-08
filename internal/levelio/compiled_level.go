package levelio

// compiled_level.go — the artefact a release build loads instead of a level.
//
// An authored level is a list of brushes, and turning it into something
// drawable means running CSG, a BSP build, a visibility flood and a light bake,
// none of which a shipped game should be doing while the player waits. So the
// editor does it once and writes THIS: the geometry already reduced to
// world-space polygons with their lighting on them, the tree that locates a
// camera among them, and the bit array that says what to draw once it has been
// located.
//
// # What stays authored
//
// Not everything is compiled, and the parts that are not are carried through in
// exactly the form the ordinary loader already understands (Raw). Two kinds of
// chunk are kept whole rather than baked:
//
//   - FRAME-OWNED chunks, whose geometry moves with a vehicle and so cannot be
//     a static world-space list.
//   - DISABLED chunks, which are not in the world at compile time but which a
//     script can switch on later (@chunk.prop.enabled). Baking them in would
//     make the switch do nothing; leaving them authored means they appear when
//     asked, drawn the live way.
//
// The corollary is worth knowing before it surprises someone: a disabled chunk
// takes no part in sealing the level. If one of them is a wall, the level leaks
// through the hole it leaves, and the compile says so.
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

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"os"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/compile"
	"foundry/internal/fileutil"
	"foundry/internal/world"
)

// CompiledExt is the extension a compiled level carries, beside the .yaml it
// was built from.
const CompiledExt = ".fbsp"

// CompiledVersion is bumped whenever old bytes would load into the current
// structs without complaining and mean something different.
const CompiledVersion = 1

// compiledMagic is the first thing in the file, so that a truncated or
// unrelated file is rejected as such rather than as a gob decode failure
// halfway through.
const compiledMagic = "FOUNDRYBSP"

// CompiledLevel is a level with its static geometry already compiled.
type CompiledLevel struct {
	Version int

	// Faces are world-space, with baked per-vertex light. Indices into this
	// list are what the cells and nodes hold.
	Faces  []compile.Face
	Nodes  []compile.BSPNode
	Leaves []compile.BSPLeaf
	Root   compile.ChildRef
	Lo, Hi mgl32.Vec3

	PVS CompiledPVS

	// Raw is everything the compile did not consume: the level's entities,
	// volumes, lights, spawn, scripts and UI, plus the chunks that are still
	// built live. See the file comment on which those are.
	Raw world.RawLevel

	// Stats is what the compile reported, kept so that a level can be asked
	// what it cost without recompiling it.
	Stats compile.Stats
}

// CompiledPVS is the visibility bit array, flattened.
type CompiledPVS struct {
	Leaves   int
	RowBytes int
	Bits     []byte
}

// Visible reports whether anything in cell to could be seen from cell from.
func (p *CompiledPVS) Visible(from, to int) bool {
	if from < 0 || to < 0 || from >= p.Leaves || to >= p.Leaves || p.RowBytes == 0 {
		return false
	}
	return p.Bits[from*p.RowBytes+to/8]&(1<<uint(to%8)) != 0
}

// Tree rebuilds the BSP the compile produced, for the runtime queries that live
// on it — locating a camera, tracing a line.
//
// A view of the stored slices rather than a copy: the compiled level is loaded
// once and read from thereafter.
func (c *CompiledLevel) Tree() *compile.BSPTree {
	return &compile.BSPTree{
		Nodes: c.Nodes, Leaves: c.Leaves, Faces: c.Faces,
		Root: c.Root, Lo: c.Lo, Hi: c.Hi,
	}
}

// CompileLevel runs the compiler over a loaded level and returns the artefact.
//
// The level is not modified. Its chunks are partitioned here rather than in the
// compiler: the compiler's rule for what it bakes (enabled, not frame-owned) and
// this function's rule for what it carries through raw are the same rule read
// from opposite ends, and they have to agree exactly or geometry is either
// dropped or drawn twice.
func CompileLevel(lvl *world.Level, opt compile.Options) (*CompiledLevel, error) {
	res, err := compile.Compile(lvl, opt)
	if err != nil {
		return nil, err
	}
	raw, err := LevelToRaw(lvl)
	if err != nil {
		return nil, err
	}
	raw.Chunks = uncompiledChunks(lvl, raw.Chunks)

	t := res.Tree
	return &CompiledLevel{
		Version: CompiledVersion,
		Faces:   t.Faces, Nodes: t.Nodes, Leaves: t.Leaves,
		Root: t.Root, Lo: t.Lo, Hi: t.Hi,
		PVS:   CompiledPVS{Leaves: res.Vis.Leaves, RowBytes: res.Vis.RowBytes, Bits: res.Vis.Bits},
		Raw:   raw,
		Stats: res.Stats,
	}, nil
}

// uncompiledChunks keeps the raw chunks whose geometry the compile did not
// take, matched back to the level by ID.
//
// By ID rather than by position, because LevelToRaw already drops the chunks a
// level does not own — an entity's own decks, a prefab's contents — so the two
// lists are not parallel.
func uncompiledChunks(lvl *world.Level, raw []world.RawChunk) []world.RawChunk {
	keep := map[string]bool{}
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if !ch.IsEnabled() || ch.FrameOwner != "" {
			keep[ch.ID] = true
		}
	}
	out := raw[:0:0]
	for _, rc := range raw {
		if keep[rc.ID] {
			out = append(out, rc)
		}
	}
	return out
}

// SaveCompiledLevel writes a compiled level to path.
func SaveCompiledLevel(c *CompiledLevel, path string) error {
	var buf bytes.Buffer
	buf.WriteString(compiledMagic)
	if err := gob.NewEncoder(&buf).Encode(c); err != nil {
		return fmt.Errorf("encoding compiled level: %w", err)
	}
	return fileutil.WriteFileAtomic(path, buf.Bytes())
}

// LoadCompiledLevel reads one back.
func LoadCompiledLevel(path string) (*CompiledLevel, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < len(compiledMagic) || string(data[:len(compiledMagic)]) != compiledMagic {
		return nil, fmt.Errorf("%s is not a compiled level", path)
	}
	var c CompiledLevel
	if err := gob.NewDecoder(bytes.NewReader(data[len(compiledMagic):])).Decode(&c); err != nil {
		return nil, fmt.Errorf("decoding compiled level: %w", err)
	}
	if c.Version != CompiledVersion {
		return nil, fmt.Errorf("%s was compiled by version %d of the compiler; this build wants %d — recompile the level",
			path, c.Version, CompiledVersion)
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
