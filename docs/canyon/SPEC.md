# Foundry procedural canyon: implementation spec

Goal: let the user build an ultra-realistic desert canyon, in the style of UE5's
"Lumen in the Land of Nanite" demo, with very little hand modelling or sculpting.
Everything is generated, and the generators are documents the user edits, previews and
runs **inside the editor**. **No Python and no out-of-editor scripts.** Every generator
is Go code in the engine, driven by a `.fgen` document. A `foundry gen` CLI exists for
headless runs and tests, and runs exactly the code the editor runs.

Repo: /home/jfcarter2358/personal/code/main/foundry (Go, OpenGL 4.3 core, SDL2, ImGui
editor, shaders in resources/shaders, `#version 430`). Branch: feature/jc/brush-editing.

## Ground rules for every phase

- **Phases run IN ORDER** in the same working tree, one phase per agent. Before starting,
  read this spec and every earlier docs/canyon/PHASE_<n>.md.
- **Nobody commits, pushes, stashes, resets or switches branches.** The user has not asked
  for commits.
- **ANOTHER SESSION is building the music Score editor in this same working tree.**
  - Never edit, revert or reformat these paths: internal/tracker/**, internal/score/**,
    internal/editor/editor_score*.go, internal/editor/editor_tracker*.go,
    internal/world/music.go, internal/script/script_actions_song.go, resources/sounds/**,
    docs/score/**, docs/guide/31-*, docs/guide/32-*.
  - These are SHARED files: make only small ADDITIVE edits, on lines of their own, never
    touching the other session's hunks.
    - internal/editor/editor_menubar.go: add our editor kind/entry AFTER the Score entry
      (or at the end of the list).
    - internal/editor/editor_save_state.go
    - internal/editor/editor_claude_reference.go: append our rows/cases, never modify theirs.
    - internal/pack/refs.go
    - internal/app/devtools_dev.go: add one `case` line for our command.
    - docs/guide/README.md: add our rows only.
    - internal/editor/imgui.ini: do not commit window-move noise; leave it alone.
  - If a build or test fails in their packages, it is their work in progress. Note it in
    your report and move on; do not "fix" it.
  - Never use `git add -A`, `git add .`, `git checkout -- <their paths>`, or `git stash`.
- **Must pass at the end of each phase:**
  - `go build ./...` and `go vet ./...`.
  - `go test` for every package you touched plus ./internal/world/... ./internal/render/...
    ./internal/editor/... ./internal/procgen/...
  - GL tests: `FOUNDRY_GL_TEST=1 go test ./internal/render/... ./internal/gfx/...`. The dev
    box is WSLg with Mesa d3d12. If no GL context is available, say so rather than
    claiming they passed.
  - Known failure that predates this work: internal/harness
    TestAPlaySessionDrawsTheRoomItIsStandingIn.
- Respect internal/layering_test.go: low-level packages (internal/pack etc.) must not
  import internal/gfx, directly or transitively. internal/procgen's core must not import
  gfx/render/editor. GL-side pieces live in render or editor.
- **Match the house style:** long explanatory file-header comments, the comment density of
  neighbouring files, small focused files, tests beside code.
- Every new render pass gets F3 profiler sections (render/profile.go ProfileBegin/End).
  Shaders that sample new samplers inside fragment.glsl must stay compatible with the
  Nanite visibility-buffer rewrites (see "Engine facts").
- Use the headless tools rather than scratch harnesses: `foundry check`, `foundry sim`,
  `foundry render model|screen`, `foundry profile-load`, `foundry nanite`. Caveat: the
  harness reads the project's own resources/shaders. Test against a scratch project
  whose resources/shaders points at the repo's, or the repo's own resources/.
- Write docs/canyon/PHASE_<n>.md: what was built, file formats, flags, files, tests,
  measured numbers (cook times, run times, frame cost), and known gaps. Be honest about
  anything stubbed or untested.
- New behaviour defaults OFF for existing content. Existing levels must render
  identically unless they opt in.
- **RESOURCE LIMITS.** The dev box is WSL2 with 32 GB RAM, shared with the score
  session's agents. Exhausting it thrashes the VM and drops the Claude Code connection;
  this happened on 2026-10-11.
  - Never run more than ONE heavy job at a time: a full `go test ./...`, the GL render
    suite, a `foundry gen run`, or a big cook.
  - Run builds and tests with `GOFLAGS=-p=6 GOMEMLIMIT=6GiB` (set in
    .claude/settings.local.json; export them yourself if they are missing).
  - Prefer targeted `go test ./internal/<pkg>/...` over `./...`.
  - Generator runs: `-workers 8` and `cook_memory` ≤ 2000 MB.
  - Check `free -g` before a heavy job; wait if available memory is under 8 GB.
  - Always put a `timeout` on long commands (e.g. `timeout 1800 go test ...`) so a
    WSLg GL deadlock cannot pin memory forever.
  - A user-level `memguard` service kills the largest go/test/foundry process when
    memory runs out. If a run dies with SIGKILL, that is why: reduce parallelism and
    retry. Its log is `~/.cache/memguard.log`.
- **ASSET SOURCING (user direction).** Prefer downloaded Poly Haven CC0 assets over
  generating meshes or textures whenever a suitable asset exists. Scaling them down
  (lower texture resolution, decimated meshes) is fine. Generate only what Poly Haven
  can't supply: the terrain heightfield and its masks, the layout, and odd shapes.
- Performance target: the user's GTX 1070 (and WSLg for dev). Long CPU work runs on
  worker goroutines with progress and cancellation, never on the UI thread.

## Engine facts (as surveyed before phase 0)

### Editor
- **Windows.** Every document editor is its own OS window: `uiwin.NewUIWindow(title, w, h,
  draw, onClose)` (internal/uiwin/window.go:99), with its own GL and imgui context.
  - `RenderSecondaryWindows` (window.go:254) calls each window's Draw.
  - Never open a window inside a Draw; use `uiwin.RequestWindowOpen(fn)` (:403).
- **Template to copy:** the material editor.
  - internal/editor/editor_material_window.go: globals, `MaterialEditorOpen`,
    `OpenMaterialEditorWindow`, `OpenMaterialEditorOn(name)`, onClose frees VAOs,
    ensureDoc/load/save/saveCurrent, orbit camera, input (Ctrl+S/O/N/Z/Y), draw.
  - editor_material_panel.go: toolbar/menus, the File>New modal via
    `imguibackend.WithModalPopup`, the file list, and the param panel.
  - editor_material_undo.go: whole-doc snapshot undo on `boundedStack[T]`
    (editor_undo.go:28), pushed BEFORE the mutation. Drags push once (the latch pattern
    in editor_particle_panel.go:42-60).
  - editor_material_render.go: preview through the real world program
    (`gfx.CompileWorldProgram`, `render.PrepareWorldProgramForPreview`
    (render/material_preview.go:73), `g.DrawThroughProgram`).
- **Switcher.** internal/editor/editor_menubar.go has `editorKind` (:45-59) and
  `editorSwitchTargets()` (:74-107). Shared bar helpers: `editorTopBarBegin`,
  `editorMenuBarShared`, `editorMenuBarDoc`, `editorSaveAllMenuItem`,
  `editorUndoRedoItems`, `editorStatusText`.
- **Save state.** internal/editor/editor_save_state.go compares fingerprints (`savedState`,
  `refresh`, `dirty`). Register the editor in `openEditors()` for Save All, and call
  `MarkSaved` after load/new/save.
- **Background job pattern.** internal/editor/editor_compile_job.go: a goroutine with
  recover, a mutex-guarded stage/fraction, a non-blocking main-thread step that does the
  GL finish work, and a `WithModalPopup` modal with `imgui.ProgressBarV`.
  `compile.Progress func(stage string, done float32)` (internal/compile/progress.go:20).
  `EditorCompiling()` must be respected by per-frame level-mutating work.
- **The Nanite cook from the editor is synchronous today** (editor_nanite.go:57).
  `nanitecook.CookFile(source, out, force, opt) (CookResult, error)` (cook.go:585) is
  CPU-only and safe off-thread.
- **Widgets.**
  - `editorDragFloatStep` (editor_ui.go:105), `textureCombo` (:232), `modelFileComboOf`.
  - `imguibackend.BeginCombo/SelectableV/EndCombo` (combo_filter.go); every combo MUST
    go through these.
  - `EditorPushDisabled/PopDisabled`, `WithModalPopup`.
  - Schema-driven params: `material.ParamDef` (material_kinds.go:67) plus
    `materialParamWidget` (editor_material_panel.go:462-521). Copy this pattern.
- **2D images.** Draw an image in ImGui with `dl.AddImageV(imgui.TextureID(glTex), ...)`.
  Offscreen targets: `glctx.NewTarget(w, h)`.
- **Claude panel hooks.**
  - `claudeAssetKinds()` and the `list_assets` enum (editor_claude_reference.go).
  - Content dirs and extensions in `claudePrimer()` (editor_claude_context.go:38-41).
  - Hot reload per extension in `claudeApplyFileChange` (editor_claude_reload.go:185).
  - The prune walk's `textExts` (pack/refs.go:47-56).
- **CLI.**
  - Dispatch is in internal/app/devtools_dev.go:106. Pattern:
    `case nanitecook.Command: return nanitecook.Main(commandProject(), args[1:], os.Stdout), true`.
  - Mains follow nanitecook/cmd.go: `Main(root, args, out) int`, flag sets with `-root`,
    `cliflag.Parse`, `fileutil.ResourcesPath = fileutil.ResourcesDir(*project)`.
- **Guide.** docs/guide/NN-slug.md with an index table in docs/guide/README.md (the Claude
  docs tool parses its rows). 31 is taken and 32 is the score session's; **ours start at 33**.

### World, scenery, Nanite
- **Placements.** Nanite meshes are placed as `world.Scenery` (world/scenery.go:43).
  - YAML is `RawScenery` (level_schema.go:295): id, model (.fmdl/.obj/.fnm), position,
    rotation (Euler radians, Y then X then Z), scale, solid, materials{slot: texture},
    collision (auto|box|hulls|mesh|none), wind, chunk.
  - Scenery is a top-level `scenery:` list on RawLevel. `Chunk` links a placement to a
    chunk's switch; `SceneryChunkAt(lvl, pos)` picks a chunk by footprint.
- **Load cost.** `nanite.Load` caches meshes by path, so N placements share one mesh.
  Level load calls `UniqueSceneryID` per placement, which scans every list: O(N²) for
  10k+ placements. **This must be fixed when we write thousands.**
- **Instancing.** One GPU instance per placement, one indirect multi-draw per (texture,
  smooth) group (render/nanite_scenery.go).
  - Tiles cost per placement times DAG leaves (`appendLeafTiles`). The 244-rock bench
    (82k triangles each) made 43k tiles, about 176 per placement.
  - So scattered small rocks must use LOW-poly variants. Hero cliffs can be dense.
- **Collision.** `collision: mesh` runs a BVH over a DAG cut at 3 cm. Nav includes
  hulls- and mesh-mode scenery; a WATERTIGHT mesh is one parity solid, while an open sheet
  is one solid per triangle (costly). So terrain tiles should be closed (skirts plus a
  bottom).
- **GI.** A cooked .fnm's SDF grid (24-64³, baked from the fallback mesh) feeds the live GI
  clipmap. Big tiles get coarse cells (~4 m for a 256 m tile), so keep tiles modest
  (≤128 m).
- **Shadows.** VSM handles cooked scenery and needs TAA.
- **Cook gaps.**
  - Cook input is a FILE (.gltf/.glb/.obj/.fmdl). `model.EncodeGLB(*model.ImportMesh)`
    writes a GLB. `model.ImportMesh{Positions, Normals, UVs, Indices, TriSlot, Slots,
    Files}`. There are NO vertex colours (the cooked pool is xyz+uv+normal, plus an
    optional baked light record).
  - **No border locking.** `cluster.DAGOptions.Locked` exists, but cookSource only locks
    positions shared between slots, so adjacent tiles CRACK. We need an option that locks
    open-boundary vertices.
  - The cook hash covers CookVersion, collision settings and source bytes only.
- **Ground queries.** `biome.GroundQuery` (biome/ground.go:181) covers brushes and block
  terrain but NOT scenery, so biome volumes and lattice scatter will not land on .fnm
  terrain. Decals do not clip scenery either.
- **Block terrain** (world/terrain.go): a 3D block grid, at most 1024 per axis. It is not
  the tool for a kilometre canyon at 0.5 m. We add a heightfield path that cooks to Nanite
  tiles instead.
- **Noise.** world/terrain_noise.go exports `Hash32`, `ValueNoise2/3`, `FBM2/3`,
  `SmoothStep`. internal/noise3d is cloud-only. internal/meshsdf bakes SDF grids but does
  not mesh them. The only isosurface code is block terrain's unexported dual contouring.
- **Level file.** A level is one YAML file. 10k scenery entries is about 2-3 MB of YAML.

### Rendering
- **Textures.**
  - Runtime textures are `textures/*.png` (gfx/texture.go). Only PNG decodes. There is no
    size cap at runtime (the 2048 cap is .ftex/texdoc only).
  - Uploads are uncompressed RGBA8 with GenerateMipmap, trilinear plus 16x anisotropy. A
    4096² texture is about 85 MB. There is no compression and no VRAM budget.
  - A `<name>_normal.png` beside a texture is its normal map (unit 12).
- **Units in use:** 0, 1, 2-9, 10, 11, 12, 13, 15, 20-23, 32, 33, 44, 45, 46. Free units
  for new samplers: 14, 16-19, 24-31, 34-43.
- **.fmat** (internal/material): YAML with kind/texture/effects.
  - Effects are registered in material_kinds.go (`RegisterKind`, `ParamDef`), resolved in
    material_resolve.go (`Surface`), and applied by gfx/material_surface.go
    (`SurfaceState.overlay`, `Apply/ApplyLocs`) as uniforms in the one fragment.glsl.
  - Adding an effect = a kind row, a resolver line, a SurfaceState field, uniform
    locations (render/boot.go StandUpWorldUniforms, gfx/graphics.go globals), and a
    uniform branch in fragment.glsl.
- **Nanite scenery does not carry a MaterialName today** (render/nanite_scenery.go:279-290
  groups on texture+smooth), so a .fmat cannot apply to it. Compiled statics and chunk
  batches do.
- **Nanite visibility-buffer material pass** (render/nanite_vis_source.go):
  - It splices fragment.glsl and rewrites `dFdx`/`dFdy` to analytic derivatives.
  - It rewrites ONLY `texture(tex,` / `normalMap,` / `blendTex,` / `blendMask,` into
    `nvTexture` (textureGrad).
  - Any other derivative call fails the build and silently falls back to forward.
  - New samplers used in fragment.glsl must be added to that rewrite. Keep
    nanite_vis_source_test.go passing and add a test that the vis program still builds.
- **Lighting.**
  - Diffuse wrapped Lambert plus hemispheric ambient / DDGI GI (light_model.glsl), clamped
    to LDR at fragment.glsl:600.
  - The baker mirrors light_model.glsl; change both, or neither.
- **Fog** (fog.glsl): radial distance fog (linear/exp/exp2) plus placed fog volumes.
  NO height fog, NO aerial perspective, NO sun in-scattering. The Go side is world/fog.go
  (`Fog`, `PackFog`), spliced by gfx/fog_shader.go.
- **Terrain shader** (terrain_frag.glsl) has a good triplanar reference (82-112) and
  triplanarGround's strata-aligned v.
- **Shader includes:** `#include "x.glsl"` (gfx/shader_include.go). Injected snippets are
  cached; a shader change needs a restart.

## Architecture

### The generator document: `.fgen`
- Lives in `resources/generators/<name>.fgen`, as strict YAML:

  ```yaml
  doc: Desert canyon, red sandstone
  seed: 1234
  output: generated/desert_canyon      # dir under resources/; default generated/<name>
  level: desert_canyon                 # optional target level that receives placements
  stages:
    - stage: terrain                   # a registered StageKind
      id: ground                       # unique within the doc; names outputs and references
      enabled: true
      params: { size: 1024, resolution: 0.5, ... }
    - stage: rocks
      id: boulders
      params: { preset: boulder, variants: 12, ... }
    - stage: scatter
      id: talus
      params: { terrain: ground, rocks: boulders, ... }   # stage-ref params name upstream ids
  ```

- **Stage kinds** are registered like material effect kinds:
  `procgen.RegisterStage(StageKind{Name, Label, Doc, Params []ParamDef, Run, Preview})`.
- **ParamDef types:**
  - float, int, bool, enum, vec2, vec3, color, texture, string;
  - `seed`: int with a dice button;
  - `stage` (a ref to an upstream stage id, filtered by kind);
  - `spline` (a list of 2D/3D points, edited in the preview's top-down view);
  - `curve` (a 1D profile as points, for canyon cross-sections);
  - `list` (a repeated group of sub-params, e.g. strata layers or scatter rules).
- The schema drives the editor UI, validation, defaults, and docs (tooltips, the guide
  table, the Claude reference).
- **Determinism.** Same doc, same bytes out. Each stage's RNG seed = hash(doc seed,
  stage id, stage seed param).
- **Incremental runs.** Each stage's output hash = hash(kind, version, params, upstream
  output hashes). The output dir keeps `manifest.yaml` (stage id → hash and file list),
  and an unchanged stage is skipped. "Run" runs stale stages; "Force" reruns all.
- **Outputs** are files under the output dir: meshes (.glb then cooked .fnm), PNG
  masks/textures, generated .fmat files, and a placements file.
- **Placements reach the level** by a splice into the target level's `scenery:` (and,
  where needed, `volumes:`). Every generated entry's ID is prefixed `gen.<fgen
  name>.<stage id>.`. A rerun replaces exactly the entries with that prefix and leaves
  hand-made ones alone.
  - If the target level is open in the level editor, the splice goes into the in-memory
    level through the editor's normal mutation path (undoable, marks it dirty) instead
    of writing the file under it.
  - The splice is skipped while `EditorCompiling()`.
- **The run engine** (internal/procgen) is pure CPU Go.
  - `Run(ctx context.Context, doc, opts) (Result, error)`, with progress callbacks
    `func(stage string, done float32)` and log lines.
  - Stages may use all cores. Cancellation is checked often.
- **Nanite cooking happens in the run.** Export an in-memory entry point (e.g.
  `nanitecook.CookMesh(mesh *model.ImportMesh, out string, opt Options)`) or write the
  .glb and call CookFile.

### The Generator editor
A new editor window (editorKindGenerator, switcher label "Generator") modelled on the
material editor:
- a file list of resources/generators/*.fgen, with File > New / Open / Save / Save As /
  Save All, undo/redo, and the save chip;
- a stage list (add from a kind menu, remove, duplicate, reorder, enable toggle);
- a schema-driven param panel for the selected stage, with reset-to-default and
  tooltips;
- a viewport with two modes:
  - Top-down 2D: a heightmap / mask / placement-dot view. Splines are edited here (click
    to add, drag to move, right-click to delete).
  - 3D: the generated terrain tiles and meshes drawn through the world program with the
    level's lighting where possible, or a sensible preview light rig. Rocks get a
    turntable view of a variant.
- Run / Run Stage / Force / Cancel. The run is async behind a progress modal (the
  compile-job pattern) with per-stage progress and a log.
- **Preview runs.** Each StageKind may supply a fast preview, e.g. a heightmap at 1/4
  resolution with fewer erosion iterations. It re-runs automatically (debounced) when
  params change, off-thread, cancelling a stale preview.
- "Open level" button for the target level.
- Claude reference rows / reload case / primer line, and `foundry gen` CLI:
  - `foundry gen run <file.fgen> [-force] [-stage id]`
  - `foundry gen list` (stage kinds and their params)
  - `foundry gen new <name> -template desert_canyon`

## Phases

### Phase 0: foundation
1. **internal/procgen core.**
   - The .fgen doc (load/save strict YAML, Clone, Fingerprint, validation with friendly
     errors).
   - ParamDef schema with defaults/clamping, the stage registry, and the run engine
     (manifest, incremental hashing, cancellation, progress, logs, worker pool).
   - A deterministic noise library: exported value/gradient noise 2D/3D, FBM,
     ridged/billow, domain warp, cellular/Voronoi F1/F2 with cell IDs, and seeded RNG.
     Reuse world/terrain_noise.go where it fits, but procgen must not depend on world if
     that breaks layering. Keep noise in procgen/noise.
2. **Mesh output helpers.**
   - An ImportMesh builder (weld, normals).
   - The GLB write plus an in-memory cook entry point.
   - `nanitecook.Options.LockBorder` (lock open-boundary vertices so tiles do not
     crack), folded into the source hash.
   - A helper that builds a placement list and splices it into a RawLevel by ID prefix.
3. **Level load fix.** Make level load with 10k+ scenery entries not O(N²)
   (`UniqueSceneryID`), with a test/bench.
4. **The Generator editor window** as above. Since no real stages exist yet, ship a tiny
   `noise_preview` stage, or similar, that exercises params, preview and run. It can stay
   as a debugging aid.
5. **`foundry gen` CLI**, Claude hooks, save state, docs/guide/33-generators.md, and a
   README row.

### Phase 1: heightfield terrain and erosion (stage `terrain`)
1. **Base shape.**
   - Size (m), resolution (m/sample), and base elevation FBM with domain warp.
   - A mesa/plateau field: flat-topped regions from thresholded low-frequency noise with
     steep, eroded rims.
   - Terracing with noise-warped strata heights. The `list` of strata layers (thickness,
     hardness) feeds erosion resistance AND the material bands.
2. **Canyon carving.**
   - One or more `spline` paths with width/depth along the spline, and a cross-section
     `curve`: V, U, slot, or stepped-terrace profiles.
   - Meanders from noise. Side tributaries optional.
3. **Erosion** (multi-threaded, cancellable):
   - Droplet hydraulic erosion: inertia, capacity, deposition, evaporation, erosion
     radius. Strata hardness modulates erodibility, which gives ledges and benches.
   - Thermal erosion (talus angle) for scree slopes.
   - A fast low-iteration mode for preview.
4. **Outputs.**
   - The heightmap (16-bit PNG).
   - Masks (8-bit PNG, one per mask): flow/wetness, slope, deposition (sediment), cliff
     (slope > threshold), strata index/height, and curvature/cavity.
   - A ground albedo hint map for the material phases.
   - A metadata file (bounds, resolution, which mask is which).
5. **Tiles.**
   - Split into ≤128 m tiles. Each tile is a CLOSED mesh: the top surface, skirts down to
     a base, and a bottom, so nav sees one parity solid.
   - Cook each tile with LockBorder, in parallel, and place it as scenery with
     `collision: mesh` and an automatic `chunk:` when the level has chunks.
   - Verify no cracks between tiles at any LOD (a test over the cooked DAG cut at several
     errors).
6. **Ground queries.** Make `biome.GroundQuery` (and therefore biome volumes and lattice
   scatter) see mesh-collision scenery (collision BVH raycast). Decals on scenery:
   optional, note in gaps.
7. **Editor.**
   - A shaded-relief 2D preview with mask overlays.
   - Canyon spline editing in the top-down view.
   - The 3D preview of the tiles.
   - Time the full run at 1 km² / 0.5 m and record it.

### Phase 2: rock and cliff generator (stage `rocks`)
1. **SDF modelling library** (procgen/sdf):
   - primitives (box, rounded box, sphere, capsule, cylinder, plane), smooth
     union/subtract/intersect, transforms;
   - noise displacement (ridged, cellular);
   - "strata slab" stacking (horizontal layers with per-layer inset/offset and
     undercut);
   - Voronoi fracture planes (blocky joints);
   - erosion-like rounding of edges by curvature.
2. **Mesher:** dual contouring or surface nets with gradient normals on a sparse/blocked
   grid up to ~512³ effective, multi-threaded. Output: a welded ImportMesh with
   box-projected UVs.
3. **Presets:** boulder, cliff_chunk (tall, layered, flat back so it stacks against a
   wall), mesa_cap, ledge_slab, pillar/hoodoo, rubble, pebble.
   - Each has a `variants` count; every variant is a different seed.
   - Each has a `detail` level (target triangles), so scatter rocks are light (≤10-20k
     triangles) and hero cliffs dense (≥500k).
4. **Per-vertex colour/AO end-to-end.** This is an engine format change:
   - `model.ImportMesh.Colors` (RGBA8). glTF COLOR_0 read/write.
   - The cooked .fnm pool carries an optional colour channel (nanite FileVersion bump;
     old files still load).
   - Cluster pages encode it. The Nanite material pass decodes it, the forward/fallback
     path passes it, and fragment.glsl multiplies albedo by rgb and uses a for
     cavity/AO.
   - The generator bakes cavity/curvature/AO and a strata tint into it.
5. **Editor:** a 3D turntable preview of the selected variant, with triangle count and
   bake time.

### Phase 3: placement (stages `scatter`, `cliff_wall`, `vegetation`)
1. **`scatter`:** places instances of a rocks stage's variants over a terrain stage. A
   `list` of rules, each with:
   - mask filters/weights (slope range, height range, flow, deposition, cliff-base
     proximity);
   - density per 100 m², Poisson-disk spacing, and a scale range with a power-law bias
     (many small, few big);
   - align-to-normal amount, sink depth (a fraction of size), random yaw/tilt;
   - overlap rejection against already placed instances (bounding spheres or hull-ish
     radii);
   - a collision mode by size (hulls small / mesh large / none for pebbles).
   - It writes placements through the level splice.
2. **`cliff_wall`:** lines a `spline` (or the canyon wall edge found from the cliff mask)
   with stacked cliff_chunk variants.
   - Several tiers up the wall height, faces pointing out of the wall, small random
     overlaps and rotations.
   - Big flat rocks for ledges. This is what gives vertical/overhanging walls a
     heightfield cannot.
3. **`vegetation`:** emits `kind: biome` volumes (or world.Tree placements) for sparse
   desert shrubs/deadwood using the existing built-in desert/badlands/mesa biomes,
   filtered by masks. This relies on the phase-1 GroundQuery scenery support.
4. **Performance.** Record tile count, F3 frame time and load time for a level with ~5k
   rocks + tiles. Pick defaults that hold 60 fps on a GTX 1070 at 1080p if possible, and
   note the reality.

### Phase 4: the Poly Haven asset library (download, not generate)
**User direction (2026-10-11):** wherever possible, use DOWNLOADED Poly Haven CC0 scans
rather than generated meshes and textures. Scaling the assets down is fine: a lower
texture resolution and a simplified mesh. The generated `rocks` stage stays as a
fallback and for odd shapes, but every default uses Poly Haven.

**API facts** (verified 2026-10-11):
- Catalog: `https://api.polyhaven.com/assets?t=models|textures[&c=<category>]` returns
  `{id: {name, categories, tags, polycount, dimensions (mm), ...}}`.
- Info: `/info/<id>`. Files: `/files/<id>`.
  - Models: `gltf.<1k|2k|4k|8k>.gltf = {url, md5, size, include: {relative path: {url, md5, size}}}`.
    The .bin plus `textures/<id>_{diff,nor_gl,arm}_<res>.jpg`.
  - Textures: `Diffuse/nor_gl/Rough/AO/arm/Displacement.<res>.<jpg|png|exr>` = {url, md5, size}.
- Thumbnails: `https://cdn.polyhaven.com/asset_img/thumbs/<id>.png?width=256`.
- **ToS** (https://github.com/Poly-Haven/Public-API/blob/master/ToS.md):
  - free for any use, including commercial;
  - every request MUST send a unique User-Agent naming our software (e.g. `Foundry-Engine/<version>`);
  - any UI that surfaces Poly Haven content must visibly credit it. The window title
    "Poly Haven" plus a small "Assets from Poly Haven (CC0)" line is enough. Don't imply
    endorsement.
  - Don't hammer the API: cache the catalog (24 h), limit downloads to 4 at a time, and
    verify md5.
  - Downloaded assets are CC0 with no attribution required. Still write a
    `polyhaven.yaml` provenance record (id, name, authors, resolution, date, md5)
    beside each import.

**Desert-relevant ids:**
- Rocks and cliffs:
  - namaqualand_cliff_01 (8.3 m, 177k triangles)
  - namaqualand_cliff_02 (20 m, 364k)
  - namaqualand_boulder_02..06
  - namaqualand_boulders_01, namaqualand_rocks_01, namaqualand_stones_01
  - rock_face_01, rock_face_02
  - mountainside (10 m)
  - boulder_01
  - sand_rocks_small_01
  - rock_07, rock_09, stone_01
  - coast_rocks_05; the large coast_* / coastal_cliff_* as distant hero formations
- Desert plants:
  - quiver_tree_01/02, dead_quiver_trunk, dead_quiver_branch_01/02
  - dead_tree_trunk/_02, dry_branches_medium_01
  - shrub_02/03/04, wild_rooibos_bush, searsia_lucida, othonna_cerarioides
  - cheiridopsis_succulent, crystalline_iceplant, didelta_spinosa
- Textures:
  - red_sand, cracked_red_ground, terrain_red_01, red_laterite_soil_stones,
    red_mud_stones, red_dirt_mud_01
  - red_sandstone_wall, sandstone_cracks, rock_face, rock_face_03/04,
    rock_wall_02..17, cliff_side
  - dry_riverbed_rock, dry_river_pebbles, gravelly_sand, sandy_gravel, rocky_terrain*,
    rocky_trail*, dry_ground_rocks, mud_cracked_dry_03, aerial_sand
  - tiger_rock, worn_rock_natural_01

**Build:**
1. **internal/polyhaven** (pure Go, no gfx): the client, with
   - the User-Agent;
   - a catalog/info/files cache in `os.UserCacheDir()/foundry/polyhaven` with a TTL;
   - downloads with md5 verification, resume/retry, concurrency 4 and cancellation;
   - offline mode, using the cache only.

   Unit tests use an httptest server, never the live API.
2. **Import** (internal/polyhaven/importer or similar):
   - models go to `resources/models/polyhaven/<id>/` (the glTF + bin as downloaded),
     and their textures are converted to engine PNGs:
     - `textures/ph_<id>.png` (diffuse)
     - `ph_<id>_normal.png` (nor_gl)
     - keep the ARM / roughness / displacement as `ph_<id>_arm.png`, `_rough`, `_height`
       for the material phase;
   - the glTF's material image references are re-pointed to the engine texture names;
   - textures (from the textures category) go to `textures/ph_<id>*.png` the same way;
   - add JPEG decode (image/jpeg) where import needs it. Runtime stays PNG unless
     adding JPG to gfx is trivial.
   - **Scale-down options:**
     - texture resolution 1k/2k/4k (default 2k; the user is fine with lower);
     - optional mesh decimation to a target triangle count before the cook (Nanite
       handles density, but per-placement tile cost and disk size matter for scattered
       pieces), using the cluster simplifier or meshout decimation from phase 2.
   - Cook to .fnm with the right collision: hulls when small, mesh when big, none for
     pebbles and plants.
   - Normalise each import into the phase 2 placement frame:
     - base at y = 0 and the footprint centred;
     - real-world size from the glTF (Poly Haven is metres; check the `dimensions` mm
       field agrees);
     - estimate `back` for cliff pieces: the flattest/thinnest horizontal side, or let
       the user set it.
   - Produce the RocksArtifact-compatible manifest so phase 3's scatter and cliff_wall
     consume it unchanged.
   - Plants need alpha cutout and double-sided leaves. Check how glTF alphaMode
     MASK/doubleSided import today and make cutout foliage work through Nanite
     (nanite_vis_frag tests alpha). Quiver trees and shrubs must render correctly. Wind
     via Scenery.Wind is optional.
3. **Procgen stages:**
   - `polyhaven_rocks` (a list of ids, or a query: category + tags + count; plus
     resolution and max triangles) gives a RocksArtifact.
   - `polyhaven_plants` gives the same artifact shape, for vegetation scatter.
     Phase 3's `vegetation` stage, or `scatter` with plant rules, must accept it.
   - `polyhaven_textures` (named layers → ids, resolution) gives a texture-set
     artifact for the phase-5 materials: albedo/normal/rough/height/AO per layer.
   - Downloads happen inside the stage run (cached across runs).
4. **Editor:**
   - A "Poly Haven" asset browser window (switcher/Window menu entry):
     - category/tag search and a thumbnail grid;
     - asset details: size, polycount, authors, licence;
     - a resolution picker;
     - Download & Import, with progress;
     - "Add to stage": appends the id to the selected generator stage's list.
   - The visible credit line.
   - The Generator param panel shows an id-list param with thumbnails and a "Browse…"
     button that opens the browser.
5. **Texture memory:**
   - Imported sets are 2k-4k, so implement upload compression: BPTC/BC7 for albedo and
     RGTC2/BC5 for normals, through driver compression or a Go encoder, with a disk cache
     of compressed mips keyed by file hash. A `gfx.TextureCompression` setting, and a
     fallback when unsupported.
   - A "skip top N mips" quality setting.
   - Show texture VRAM in F3. Measure before/after.
6. **Verify for real.** Download (live, the small set needed: 2k textures, a handful of
   models) into a SCRATCH project, never the repo's resources/. Import, cook and place
   them on a phase-1 terrain, take headless renders, LOOK at them, and record download,
   cook and load times and disk sizes.

### Phase 5: materials (Poly Haven texture sets + generated terrain)
1. **.fmat on Nanite scenery:** carry a MaterialName in the nanite scenery groups key
   (texture, smooth, material). `Scenery.Materials` may name a .fmat per slot.
2. **New surface effects** (all uniform branches in fragment.glsl, nv-rewrite safe,
   GL-tested in both the forward and visibility-buffer paths):
   - `triplanar(scale, sharpness)`: world- or object-space triplanar albedo + normal for
     UV-less generated meshes, with the strata v aligned to world height like
     terrain_frag.
   - `splat(masks..., layers[≤4] with texture/normal/height/scale, height_blend)`:
     height-blended layers driven by mask textures projected over the terrain's world
     bounds. This is the terrain tiles' material.
   - `ground_blend(albedo_map, height_map, bounds, band, falloff)`: rocks fade toward the
     terrain colour underneath within a band above the ground, which hides rock/terrain
     seams.
   - `streaks(strength, scale, colour)`: world-space vertical desert-varnish streaks on
     steep faces, darkening under overhang edges.
   - `detail_normal(map, scale, strength)`: close-up micro detail.
   - `macro_variation(scale, strength)`: kills tiling at distance.
3. **The generators write .fmat files** for their outputs, referencing their textures
   and masks; placements use them.
   - The terrain tile material splats `polyhaven_textures` layers (sand, cracked ground,
     gravel, sandstone/cliff rock) by the terrain masks (flow, deposition, slope, cliff,
     strata).
   - Imported Poly Haven rocks keep their own UV-mapped scan textures, plus
     `ground_blend` and optional `streaks`.
   - Generated (fallback) rocks use `triplanar` with a Poly Haven rock texture.
   - Use the height/displacement maps for height blending, and the AO from ARM where it
     helps.
4. Keep the baker and light_model in sync if lighting math changes (it should not).

### Phase 6: atmosphere
1. **Fog additions** in fog.glsl / world.Fog / the level YAML `fog:` block. All default
   OFF so existing levels are unchanged.
   - Exponential height fog (base height, falloff, density), integrated analytically
     along the view ray.
   - Aerial perspective: distance extinction tinting toward a sky colour, with a
     Rayleigh-like bluish tint at range.
   - Sun in-scattering: a forward-scatter lobe toward the sun's direction, coloured by
     the sun.
   - The sky (sky_frag) takes a matching horizon haze so terrain melts into the sky.
2. **A dust-haze preset** using existing volumetrics (no new march unless needed).
3. Scripts can ease the new fog fields like existing ones, if the existing fog has
   script props.
4. **Fix the live-GI black patches** phase 2 found on large cooked meshes (pillars, cliff
   chunks). Black patches show with GI on, vanish with `-gi off`, and appear on
   uncoloured textured cooks too. Likely a DDGI/SDF issue such as probes inside thick
   meshes or the SDF sign. Verify on large Poly Haven cliffs.

### Phase 7: the desert-canyon template and integration
1. **A `desert_canyon` template** (File > New in the Generator editor, and
   `foundry gen new -template`) that wires every stage:
   - polyhaven_textures → terrain (spline canyon, mesas, strata, erosion) →
     polyhaven_rocks (namaqualand cliffs/boulders, rock faces, stones) → scatter (talus
     at cliff bases, boulder fields, pebbles in washes) → cliff_wall along the canyon →
     polyhaven_plants vegetation (quiver trees, shrubs, dead wood) →
     materials → a level with a spawn point, sun/sky (sky_canyon or a matching sky),
     the atmosphere settings, TAA + VSM + GI on.
   - Also ship `resources/generators/desert_canyon.fgen`.
2. **Run it end to end** into a scratch project (do NOT write multi-hundred-MB
   generated output into the repo's resources/).
   - Headless renders from several viewpoints (`foundry render screen`), checked by
     eye, iterating on defaults until it looks like a real canyon.
   - Load time and F3 numbers on WSL. Note what needs the 1070 to judge.
3. **Docs.** Finish docs/guide/33-generators.md (stage reference generated from the
   schema, workflow, performance tips) and docs/canyon/PHASE_7.md with screenshots'
   paths and known gaps.
