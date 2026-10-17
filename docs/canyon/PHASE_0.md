# Phase 0: foundation — what was built

Phase 0 builds the machinery every later phase plugs into: the `.fgen`
generator document, its parameter schema, the stage registry, the incremental
run engine, the noise library, mesh output and the in-memory Nanite cook with
border locking, the level splice, the Generator editor, and `foundry gen`.
One demo stage (`noise_preview`) exercises all of it.

The user-facing guide is [docs/guide/33-generators.md](../guide/33-generators.md).

## Packages

| Package | What | May import |
| --- | --- | --- |
| `internal/procgen` | the core: `.fgen` doc, `ParamDef` schema, stage/template registry, run engine, manifest, preview runner, placements | `fileutil`, `procgen/noise`, yaml — **not** gfx/world/model/nanitecook/render/editor (`internal/layering_test.go` asserts it) |
| `internal/procgen/noise` | deterministic RNG and noise | nothing of the engine (layering-asserted) |
| `internal/procgen/meshout` | `Builder` (ImportMesh weld/normals), `HeightGrid` (closed heightfield tiles), `WriteGLB`, `Cook` | `model`, `nanitecook` (so gfx, transitively) |
| `internal/procgen/genlevel` | the splice by ID prefix into a `world.RawLevel` or a level file | `world` |
| `internal/procgen/stages` | the built-in stage kinds; import it (blank) to register them | everything above |
| `internal/procgen/gencli` | `foundry gen` | everything above, `levelio` |

Later phases add stage kinds as **new files in `internal/procgen/stages/`** (or
packages of their own registered from a file there). No shared switch
statement grows a case: the editor, the CLI, the Claude reference and the run
engine all work from `procgen.Kinds()` and each kind's schema and hooks.

## The public API later phases use

### Registering a stage

```go
func RegisterStage(k StageKind)          // from init; panics on a bad schema or duplicate
func RegisterTemplate(t Template)        // File > New / `gen new -template`
func KindByName(name string) *StageKind
func Kinds() []*StageKind                // sorted by Category, then Name

type StageKind struct {
	Name, Label, Doc, Category string
	Version  int                    // bump when Run's output changes for the same params
	Params   []ParamDef
	Run      func(rc *RunContext) error                         // required
	Preview  func(pc *PreviewContext) (*PreviewData, error)     // optional
	Load     func(res *StageResult) (any, error)                // optional: rebuild Artifact from files
	Validate func(d *Doc, s *Stage, p Params) []string          // optional extra checks
}

type Template struct {
	Name, Label, Doc string
	New func(name string) *Doc
}
```

### The schema

```go
type ParamDef struct {
	Name, Label, Doc string
	Type     ParamType   // ParamFloat Int Bool Enum Vec2 Vec3 Color Texture String Seed Stage Spline Curve List
	Default  Value       // its Type must equal Type
	Min, Max float64     // equal = unbounded; applies to every component, a curve's y
	Step     float64     // editor drag speed
	Choices  []string    // enum
	StageKinds []string  // stage ref: acceptable upstream kinds
	Fields   []ParamDef  // list item schema (recursive)
	Spline3D bool        // spline points (x,y,z) instead of (x,z)
	Channels []string; ChannelDefaults []float64 // spline per-point extras (width, depth...)
	CurveX   [2]float64  // curve domain; zero = [0,1]
	MinItems, MaxItems int
}

// Value constructors: Float Int Bool Enum Texture String Seed StageRef Vec2 Vec3 Color Spline Curve List
func Resolve(defs []ParamDef, in Params) Params      // complete + clamped; what Run sees
func (v Value) CurveAt(x float64) float64            // linear, flat past the ends
// typed reads on resolved Params:
func (p Params) Float/Int/Bool/Str/Vec2/Vec3/Points/Items/Curve(name string) ...
```

A spline point is `[]float64` of `ParamDef.Stride()` numbers: `x, z` (or
`x, y, z`) then the channels, in order. The YAML keys are `PointKeys()`.

### Run and Preview contexts

```go
type Env struct {                       // shared by Run and Preview
	Ctx context.Context; Doc *Doc; Stage *Stage; Kind *StageKind
	Params Params                       // resolved
	Seed uint64                         // Hash64(doc seed, stage id, seed params)
	Workers int
}
func (e *Env) RNG() *noise.RNG                      // fresh stream from Seed; Fork(item) per work item
func (e *Env) Err() error / Cancelled() bool        // check in loops
func (e *Env) Progress(done float32)
func (e *Env) Logf(format string, args ...any)
func (e *Env) Parallel(n int, fn func(i int) error) error          // worker pool; stops on error/cancel; panics -> error
func (e *Env) ParallelProgress(n int, fn func(i int) error) error

type RunContext struct { Env; Dir, RelDir string; Artifact any }
func (rc *RunContext) WriteFile(rel string, data []byte) error   // atomic, recorded in the manifest
func (rc *RunContext) Path(rel string) string                    // records rel; for libraries that write their own files
func (rc *RunContext) AssetPath(rel string) string               // resources-relative, for Placement.Model
func (rc *RunContext) Place(p ...Placement)
func (rc *RunContext) PlaceVolume(v ...VolumeEntry)
func (rc *RunContext) Upstream(id string) *StageResult
func (rc *RunContext) UpstreamArtifact(id string) (any, error)   // this run's Artifact, or the kind's Load

type PreviewContext struct { Env }
func (pc *PreviewContext) Upstream(id string) *PreviewData
```

### Preview data (what the editor draws, generically)

```go
type PreviewData struct {
	Bounds Rect                 // world XZ the 2D view frames (empty: union of layers)
	Images []ImageLayer         // RGBA over a world rect, optional raw Values for the readout
	Points []PointLayer         // XZ dots, optional radii
	Lines  []LineLayer          // XZ polylines
	Meshes []PreviewMesh        // world-space triangles for the 3D view
	Notes  []string
	Artifact any                // handed to downstream previews
}
func GrayImage(name string, w, h int, vals []float32, lo, hi float32, b Rect) ImageLayer
func RampImage(name string, w, h int, vals []float32, lo, hi float32, b Rect, ramp [][4]uint8) ImageLayer
```

Top-down convention: looking down −Y, +X right, +Z down the screen; image row
0 is at MinZ. Previews are cached by stage hash (`PreviewCache`), and a
stage's upstream previews are computed (and cached) first.

### Running

```go
func Run(ctx context.Context, d *Doc, opts Options) (*Result, error)
type Options struct { OutDir string; Force bool; Only []string; Workers int
	Progress func(stage string, done float32); Log func(line string) }
func (r *Result) Placements() []Placement      // IDs prefixed gen.<doc>.<stage>.
func (r *Result) Volumes() []VolumeEntry
func StaleStages(d *Doc, opts Options) ([]string, error)
func Preview(ctx, d *Doc, stageID string, cache *PreviewCache, opts PreviewOptions) (*PreviewData, error)
```

### Meshes out

```go
type meshout.Builder                         // Slot, Vertex, Tri, Quad, SoupTri, Weld(tol, keepUVs), Normals, Mesh, OpenEdges, Bounds
type meshout.HeightGrid struct { X0, Z0, SizeX, SizeZ float32; NX, NZ int; Height func(i, j int) float32; UVScale float32; Closed bool; Base float32 }
func (g HeightGrid) Build(b *Builder)        // top (+ skirts + bottom when Closed), welded keeping UVs
func (g HeightGrid) Rect() [4]float32        // for LockRect
func meshout.WriteGLB(path string, m *model.ImportMesh) error
func meshout.Cook(m *model.ImportMesh, out string, opt CookOptions) (skipped bool, err error)
type meshout.CookOptions struct { LockBorder bool; LockRect *[4]float32; Force, Serial bool }

// nanitecook additions
type nanitecook.Options struct { ...; LockBorder bool; LockRect *[4]float32; LockRectTolerance float32; Serial bool }
func nanitecook.CookMesh(m *model.ImportMesh, source string, opt Options) (*nanite.Mesh, error)
func nanitecook.CookMeshFile(m *model.ImportMesh, out string, force bool, opt Options) (CookResult, error)
func nanitecook.HashMesh(m *model.ImportMesh, opt Options) ([32]byte, error)
```

### Placements into levels

```go
type procgen.Placement struct { ID, Model string; Position, Rotation Point3; Scale *Point3;
	Solid *bool; Collision string; Materials map[string]string; Chunk string; Wind *PlacementWind }
const procgen.ChunkAuto = "auto"
type procgen.VolumeEntry map[string]interface{}       // a level volume's YAML map, decoded strictly by genlevel

func genlevel.SpliceRaw(raw *world.RawLevel, generator string, sc []procgen.Placement, vol []procgen.VolumeEntry, opt Options) (Stats, error)
func genlevel.SpliceResult(raw *world.RawLevel, res *procgen.Result, opt Options) (Stats, error)
func genlevel.SpliceFile(path, generator string, sc []procgen.Placement, vol []procgen.VolumeEntry, opt Options) (Stats, error)
type genlevel.Options struct { ChunkAt func(x, y, z float32) string }   // resolves ChunkAuto
```

### Noise

```go
noise.NewRNG(seed) *RNG  // Uint64 Float64 Range Intn Bool Chance Normal Shuffle Fork(key)
noise.Mix64, Hash64(words...), HashString(s)
noise.Value2/3, Gradient2/3(seed, x, y[, z])                 // about [-1,1]
noise.Fractal{Seed, Octaves, Frequency, Lacunarity, Gain, Basis, Shape}.At2/At3   // fbm | ridged | billow
noise.FBM2/3, Ridged2, Billow2
noise.Warp2/Warp3(f Fractal, x, y[, z], amount)
noise.Cellular2/3(seed, x, y[, z], jitter, metric) Cell{F1, F2, ID, Point}; Cell.Edge(); CellUnit(id)
noise.SmoothStep, Clamp
```

## File formats

- `resources/generators/<name>.fgen` — strict YAML; see the guide. Params at
  their default are omitted on write; an unchanged document saves to the same
  bytes. A stage of an unknown kind is kept verbatim and refuses to run.
- `resources/generated/<name>/manifest.yaml` — `version: 1`, `generator`, and
  per stage `kind`, `hash` (sha256 hex), `files` (relative to the output dir).
  Rewritten after every stage.
- `resources/generated/<name>/<stage>/placements.yaml` — `scenery:` (keys as
  `RawScenery`) and `volumes:` (level volume maps), stage-local IDs.
- `noise_preview` writes `heightmap.png` (16-bit grey), `slope.png` (8-bit),
  `meta.yaml` (size, cells, height range), `tile_<i>_<j>.fnm` (or `.glb`).

The stage hash covers: kind name, kind `Version`, the stage seed (doc seed,
stage ID, every top-level `seed` param), the fully resolved params (canonical
YAML), and the upstream stages' hashes in ref order. A stage is skipped when
its hash matches the manifest and every listed file exists.

## Engine changes outside procgen

- **Level load O(N²) fix.** `world.LevelIDIndex` (identity_index.go):
  `NewLevelIDIndex(lvl)`, `Unique(base, fallback)`, `Taken`, `Add`. The loader
  mints scenery and tree IDs through one snapshot. `LevelIDTaken` and the index
  now share one walker (`eachLevelID`) so they cannot disagree about the
  namespace. Measured minting 10k distinct IDs the old way: 272 ms (quadratic:
  ~4.4 s at 40k); 20k IDs where half collide the old way: did not finish in
  10 minutes (cubic, through the suffix search). Through the loader now:
  20k placements, half colliding, 43 ms (`TestManySceneryLoadsInLinearTime`,
  `BenchmarkLoadManyScenery`).
- **nanitecook.** `Options.LockBorder` (locks open-boundary vertices, edges
  counted by position so UV seams do not lock), `Options.LockRect` (locks
  vertices on an XZ rectangle's edge, for closed tiles with skirts), both
  folded into the source hash **only when set** (every existing cook keeps its
  hash), `CookMesh`/`CookMeshFile`/`HashMesh` (in-memory cook through the same
  `fromImport`+`cookSource` path; hash = the mesh's GLB bytes framed like a file
  cook's), and an exported `Options.Serial`.
- **Release packing.** `generated` added to `pack.contentDirs`: without it a
  release would ship a level whose generated scenery is missing. Only what a
  level names ships. `FindUnused` never offers files under `generated/`.
- **Pre-existing test build fix.** `internal/levelio/volume_scatter_save_test.go`
  compared a struct holding a non-comparable field with `!=` and did not
  compile, so no levelio test ran; now `reflect.DeepEqual`.

## Editor

New files `internal/editor/editor_generator_*.go`: window (lifecycle, load/save,
save chip state, input), panel (top bar, lists, stage actions, doc settings,
footer, dialogs), params (schema-driven widgets, param paths into list items,
curve graph, spline table), undo, job (async run + debounced preview +
staleness), view2d (layers, grid, readout, spline editing), view3d (world
program preview, orbit camera), splice, claude.

- Switcher entry "Generator" (`editorKindGenerator`), Save All, save chip,
  undo/redo (whole-doc snapshots; drags latch to one step), Ctrl+S/O/N/Z/Y,
  F5 run, Ctrl+F5 force, Tab 2D/3D.
- Run modal with per-stage progress, timings, log and Cancel; runs a clone.
- Preview: debounced 180 ms, off-thread, stale preview cancelled; upstream
  previews cached by hash.
- 2D view: base + overlay image layers, points with radii, lines, grid,
  world readout with the base layer's value; spline click-to-add (insert into
  the nearest segment or extend the nearer end), drag, right-click delete.
- 3D view: preview meshes through `gfx.CompileWorldProgram` +
  `render.PrepareWorldProgramForPreview` (the lookdev rig, not the level's
  lights), optional lookdev texture, ground grid, orbit/pan/zoom.
- Splice: into the open level via `editorBeginReloadLevel` (one undo step,
  level left dirty, `chunk: auto` resolved with `world.SceneryChunkAt`), or the
  level file when it is not open; held while compiling, loading or playing.
- Claude: `generator_stages` reference tool, a `generators` row in
  `claudeAssetKinds`, a primer line, a `.fgen` reload case.

### Shared files touched (additive only)

| File | Edit |
| --- | --- |
| internal/editor/editor_menubar.go | `editorKindGenerator` const after `editorKindTracker`; a Generator entry at the end of `editorSwitchTargets` |
| internal/editor/editor_save_state.go | one `if GeneratorEditorOpen()` block in `openEditors`, before Script (the savedState var lives in editor_generator_window.go) |
| internal/editor/editor_claude_reference.go | `procgen` import line; `claudeGeneratorTool(),` after `claudeDocsTool(),`; a `generators` row in `claudeAssetKinds` |
| internal/pack/refs.go | `".fgen": true` row in `textExts` |
| internal/app/devtools_dev.go | `gencli` import line; one `case gencli.Command:` |
| docs/guide/README.md | one row (Generators) after Music tracker |

Also touched, not on the shared list: internal/editor/editor_claude_context.go
(primer lines), internal/editor/editor_claude_reload.go (import + case),
internal/layering_test.go (procgen rules), internal/pack/pack.go and unused.go
(generated/), internal/levelio/level_loader.go, internal/world/identity.go,
internal/nanitecook/cook.go.

## Tests

| Package | What |
| --- | --- |
| procgen | round trip of every param type, defaults omitted, every decode problem reported with location and did-you-mean, strict top-level keys, unknown kinds kept, Resolve/Clamp, CurveAt, ref validation, deep Clone, seed/hash sensitivity, templates; incremental runs (skip, downstream-only rerun reading upstream via Load, deleted output, Force), determinism across worker counts, Only + stale upstream, StaleStages, failure keeps earlier work, cancellation mid-stage leaves no entry, pruning removed stages, disabled stages, Parallel error/panic, preview chaining + cache |
| procgen/noise | RNG determinism/uniformity/Fork, noise ranges, continuity, seed sensitivity, cellular F1/F2/IDs, warp |
| procgen/meshout | weld, degenerates, closed tile watertight + outward (signed volume), open tile boundary count, cooked tile loads as a closed solid, skip on unchanged |
| procgen/genlevel | prefix-exact replacement keeping hand-made order, every placement field, chunk auto, volumes through the strict schema, refusal of unprefixed/bad entries, file splice writes only on change |
| procgen/stages | noise_preview end to end (files, placements, closed tiles, Load round trip within 16-bit), determinism, seed change, preview layers + cap, Validate, every kind/param documented, every template valid |
| procgen/gencli | new / check / run / splice / rerun against a scratch project, list, bad document reporting |
| nanitecook | LockBorder keeps the seam whole at every LOD cut (and proves the unlocked cook drops seam vertices), LockRect, locks hashed only when set, CookMesh matches the file cook, CookMeshFile skip/recook |
| world, levelio | LevelIDIndex matches uniqueLevelID step for step, sees every kind, linear time; 20k-placement load |
| pack | generated meshes named by a level are kept, working files are not, none offered as unused |
| editor (no GL) | undo/redo, stage actions as undo steps, param paths into list items, ref renaming, save chip through save/undo/redo/reload, a full run through the job with the splice into a level file; the whole window drawn for every param type (trees forced open) and the run modal; spline add/insert/drag/delete from simulated mouse input, one undo step per gesture |
| editor (GL) | layer texture upload at an odd width; the 3D preview mesh draws through the preview program into an offscreen target (centre pixel and >1/4 coverage) |

Results at the end of the phase: `go build ./...` and `go vet ./...` clean;
`go test` passes for procgen/..., world, levelio, nanitecook, pack, editor,
render, app and internal (layering). GL (`FOUNDRY_GL_TEST=1`): gfx and editor
pass. render does NOT pass, for reasons that predate this phase and touch no
file it changed: every test that stands the world up panics with "cgo argument
has Go pointer to unpinned Go pointer" in render/foliage_impostor.go:251
(bakeFoliageImpostors' DrawElementsBaseVertex); with `GODEBUG=cgocheck=0` the
whole suite runs (562 s, so it needs `-timeout 45m`) and the only failure is
TestNaniteFoliageDrawsTheClassicNearField ("the forest covers only 4588
pixels").

## Measured numbers (dev box: 20 threads, WSL2, Mesa d3d12)

| | |
| --- | --- |
| noise_preview preview, 96 cells a side | ~7 ms |
| sampling 2049² with 5 octaves + warp | 0.30 s |
| `gen run`, 256 m at 2 m, 4 tiles (9k tris each), cooked | 0.9 s |
| `gen run`, 1024 m at 1 m, 64 tiles (33k tris each), cooked, 20 workers | 31 s, 3.6 GB peak RSS, 110 MB of output |
| the same, `-workers 4` | 40 s, 1.2 GB |
| the same, each cook `Serial` | 96 s, 4.9 GB (so Serial stays off) |
| a cooked 128 m tile | 8960 tris, 161 clusters, 5 LOD levels, 0.49 mm position step |

## Known gaps and notes for later phases

- **list_assets enum.** The `list_assets` tool's `kind` enum is on a line the
  score session has modified; adding `"generators"` there would edit their
  hunk, so it was not added. `kind: all` (the default) lists generators; once
  their edit lands, append `"generators"` to that enum.
- **Memory on big runs.** Concurrent cooks hold their meshes; phase 1 at
  0.5 m will need to bound cook concurrency by memory (e.g. `Workers` from a
  budget) — see the numbers above.
- **Tile seams and quantisation.** LockBorder/LockRect pin the seam through
  the DAG. Cooked positions are also quantised on a power-of-two grid from the
  origin whose step depends on the mesh's largest extent: tiles of equal size
  (and whose height range does not exceed it) get the same step, so their
  shared vertices quantise identically. A tile whose height range is larger
  than its side would get a coarser step and could open sub-millimetre gaps;
  phase 1 should keep tiles square and test the seam after quantisation.
- **Collision mode vs cook.** A placement's `collision:` is honoured at load;
  `nanite inspect` on a tile shows the cook's auto mode, which is expected.
  `nanite inspect` also says "up to date with its source: false" for in-memory
  cooks (there is no source file to compare) — harmless.
- **`chunk: auto`** resolves only when splicing into a level open in the
  editor; `foundry gen run` (file splice) leaves such placements chunkless.
- **The editor window must be open for a run to finish its splice.** The run
  goroutine continues if the window is closed, but the result and splice are
  applied when the window next draws.
- **3D preview lighting** is the material editor's lookdev rig, not the target
  level's lights. Preview meshes are plain triangles (no instancing yet —
  phase 3's scatter preview will want an `Instances` field on `PreviewMesh`,
  an additive change).
- **`gfx.Geometry.Load` leaves a GL_INVALID_VALUE** in the gltest context for
  any mesh (pre-existing; the GL test clears it before checking the draw).
- **vec2 YAML** writes `"y"` quoted (YAML 1.1 reads bare `y` as a bool); it
  reads back fine.
- **CLI splice and an open editor.** `foundry gen run` rewrites the level file
  even if an editor has it open; the guide says not to.
- **Determinism** holds per architecture (Go may fuse multiply-adds on arm64,
  never on amd64).
- **No `.fgen` shipped in resources/**: the `noise_demo` template stands in
  (`foundry gen new <name> -template noise_demo`).
