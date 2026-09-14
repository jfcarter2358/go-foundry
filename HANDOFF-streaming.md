# Handoff: cheap chunk switching, then streaming the ground

This is a separate piece of work from `HANDOFF.md`, which covers the
first-person viewmodel and has uncommitted changes on this branch. Don't edit
that file, and don't revert anything under `internal/app`, `internal/editor`,
`internal/hooks`, `internal/script` or `docs/` that you didn't write: the
branch is carrying someone else's work in progress.

The goal: switching a region of a level on (or off) during play must not stall
the frame loop, and a big region must not drag the frame rate down once it is
on. The first level that hits this is `level1`: its `ground` chunk switches on
mid-sequence and the game freezes for about 3.6 s, then runs at about 15 FPS.
The work is five steps, each shippable on its own, in the order below.

Everything in this file was read from the code or measured on 2026-09-14.
Line numbers drift, so treat a `file:line` as "look here first", not as
gospel.

---

## 0. Context you need before touching anything

### Where things live

- **Engine:** this repo, `/home/jfcarter2358/personal/code/main/foundry`
  (Go, OpenGL 4.1 core, SDL2). Branch `feature/jc/brush-editing`.
- **Game project:** `/mnt/c/Users/jcart/OneDrive/Documents/Foundry`, which is
  the Windows side of OneDrive, reached through WSL. It holds `resources/`
  (levels, scripts, entities, models) and `foundry.exe`, the dev build the user
  plays. Rebuild that with `make windows`.
- **The level:** `resources/levels/level1.yaml` in the OneDrive project.
  Its `ground`, `ground_breach` and `ground_breach_rubble` chunks, every
  volume and entity whose id starts with `ground_`, and the `seal:` block are
  **generated** by `tools/level1_ground_gen.py` in the same project:

      python3 tools/level1_ground_gen.py resources/levels/level1.yaml

  It text-splices only what it owns and leaves the rest of the file alone.
  Change the generator, never the generated YAML. `tools/` sits outside
  `resources/`, so the packer doesn't ship it.

### The level1 sequence (what switches on, and when)

1. **Boarding.** The player walks into the ship entity's `trigger` volume
   (`scripts/level1/dropship_trigger.flang`), and `scripts/level1/ship.flang`
   takes over.
2. **Flight.** The ship flies. `@hangar.prop.velocity = <0,10,0>` moves the
   **hangar chunk** upward for 10 s, then `:atmosphere:` disables it.
3. **`:skybox:`.** Enables `@ground` and `@ground_breach`, then turns on the
   ground's 17 enemies with
   `#ground_enemy.prop.visible / .collision_gravity / .ai_enabled = true`.
   They are authored hidden, weightless and with AI off, because the ground
   doesn't exist until this moment.
4. **The ground.** A landing zone, then about 220 m of canyon, then a basin
   with a walled compound. The compound's front-wall section is chunk
   `ground_breach`. `scripts/level1/breach.flang` (on volume
   `ground_breach_charge`) switches it off and switches `ground_breach_rubble`
   on after a planted charge's fuse runs out.

Two facts about the geometry:

- **Size.** The ground chunk is 734 convex brushes and 6,851 drawable faces.
  Brushes are convex hulls with points snapped to 1/8 m.
- **Overlap.** The hangar and the ground overlap in XZ; the hangar hovers above
  the landing zone.

### What was measured

Measured on a Linux release build (`go build -tags release`) under WSLg, on a
GTX 1070 through Mesa's D3D12 driver. Windows-native GL timings will differ,
but the shape won't:

| Frame | Cost | Breakdown |
|---|---|---|
| `@ground` enabled | **2.78 s** | `SyncChunkGeometry` 2.21 s (building meshes plus creating about 13,700 VAOs and VBOs, one per face), first shadow draws 0.42 s, first main draw 0.13 s |
| the next frame | **0.85 s** | `ai.Update` 0.80 s, rebuilding the navigation heightfield and grid for the whole level |
| every frame after | **50–70 ms** | shadow passes about 24 ms, main pass about 21 ms, `EntityUpdate` about 7 ms (was about 20–40 ms before the drop) |

- **CPU side of the mesh build.** Building the face vertex arrays with uploads
  switched off takes about **5 ms** for the whole chunk. The 2.2 s is GL object
  creation plus per-face uniform and attribute name lookups.
- **Ruled out already.** Two engine fixes are already in the tree from the same
  investigation, and neither is the remaining problem:
  - **Grass bake.** The scatter bake used to cost 13 s here. It now uses
    `biome.NewGroundQuery` with per-brush boxes.
  - **Collision broadphase.** `pushOutOfChunkBrushes` got a per-brush box
    reject, `bodyBoxTouchesBrush`.
- **Content fixes already made.** The grass volume was removed from level1, and
  a slope-bounce bug in `collision.StepUp` was fixed (`TestWalkingASlopeDoesNotBounce`).

### How to measure and verify (do this before and after each step)

**Headless (no GL).**
- **Loading.** `levelio.LoadLevelHeadless(path)` loads a level with uploads off.
  Set `fileutil.ResourcesPath = fileutil.ResourcesDir(root)` first, with `root`
  being the directory that contains `resources/`.
- **What it's good for.** Every CPU-side system: nav, collision,
  `SyncChunkGeometry` with uploads off, and scripts via `script.ScriptEventsReset`,
  `script.ScriptRuntimeLoad` and `script.ScriptTick`.
- **Where harnesses go.** Throwaway programs go in `cmd/zz_*/main.go`, which
  can import `internal/...`. Delete them before finishing.
- **Package tests.** `internal/collision` tests can't import `levelio`
  (import cycle). Parse `world.RawLevel` with `gopkg.in/yaml.v2` and build
  brushes by hand instead.

**Real GL, reproducing the stall end to end.**
1. **Build.** `GOOS=linux CGO_ENABLED=1 go build -tags release -o <scratch>/repro/foundry ./cmd/foundry`.
2. **Install tree.** Make `<scratch>/repro/resources/`: symlink `entities fonts
   models particles scripts shaders sounds textures ui video` from the OneDrive
   project, and copy in a `game.yaml` with `start_level: level1`.
3. **Level.** Copy in `levels/level1.yaml` with `spawn: position` changed to
   `{x: 1.5, y: 1.3, z: -36.756}`, which is inside the ship's boarding trigger,
   so the whole sequence plays out with no input.
4. **Compile.** A release build only loads `.fbsp`:
   `go run ./cmd/foundrycompile -root <scratch>/repro level1`.
5. **Run.** `cd <scratch>/repro && ./foundry`. `@ground` switches on about 27 s
   after the game loop starts. Log output goes to stdout.
6. **Stack dump.** To see where the main thread is, `kill -QUIT <pid>` prints
   every goroutine. Goroutine 1 is the frame loop.

**Frame profiling.**
- **The profiler.** Add a temporary `internal/app/zz_prof.go` with a
  `zzBegin`/`zzLap(name)`/`zzEnd` lap timer that logs frames over 25 ms. Call
  `gl.Finish()` before the laps that should include GPU time.
- **The laps.** Place laps in `app.go` around `ai.Update`,
  `collision.EntityUpdate`, `script.ScriptTick`, the
  `SyncChunkGeometry`/`RebuildIndexes` block, the shadow passes and `GLSwap`.
- **Protecting `app.go`.** It has the user's uncommitted edits, so snapshot it
  (`cp` plus `sha256sum`) before instrumenting. Restore it and verify the
  checksum as soon as the instrumented binary is built.

**Tests.** Run `go test ./internal/world ./internal/levelio ./internal/collision
./internal/nav ./internal/ai ./internal/render ./internal/biome
./internal/script ./internal/compile ./internal/editor` after each step.

---

## Step 1 — A disabled chunk keeps its geometry; enabling is a switch

### Today
`internal/world/chunk_enable.go`:

- **Fields.** `Chunk.Disabled` is authored (`disabled:` in YAML, and
  `@chunk.prop.enabled`). `Chunk.Unloaded` (runtime) says the GPU and CPU face
  data has been freed.
- **`SyncChunkGeometryFor` (:94).** Releases on `Disabled && !Unloaded`
  (`ReleaseGeometry` → `Brush.Unload` → `Geometry.Unload`, which frees the GL
  objects *and* `Geometry.Vertices`). Rebuilds on `!Disabled && Unloaded`
  (`ReloadGeometry` → `Brush.Load` for every `DrawsBrushLive` brush).
- **`SyncChunkGeometry` (:70).** Runs once per frame from `app.go:792`. When it
  returns true, `app.go` calls `world.RebuildIndexes`, which rebuilds the whole
  level's spatial hash plus the frame set.
- **Loader.** `internal/levelio/level_loader.go:86-94` never builds a chunk
  authored `disabled: true`, and :54 sets `Unloaded: rc.Disabled`. The compiled
  loader does the same (`compiled_load.go:95,101`).
- **Script setter.** `script_props_geom.go:277-292` only writes `ch.Disabled`;
  the next frame's sync does the rest.

Every *consumer* already gates on `IsEnabled()`, not on residency:

- the main pass (`app.go:966`) and shadows (`shadow.go:693`)
- the spatial hash (`spatial.go:79`, and the `FindChunk`/`FindChunkAmong`
  checks)
- raycasts (`raycast_accel.go:628`), nav (`nav/heightfield.go:129`,
  `nav/cache.go:124`) and biome (`biome/ground.go`)
- the compiler (`compile/csg.go:187`)
- editor picking and occlusion, and `AdvanceChunks`/`AdvanceBrushes`

So residency can be decoupled without touching any of those.

### Change
1. **Load everything.** Build every chunk at load, disabled or not, in both
   loaders. Keep the stepped load job (`level_load_job.go`) so the build stays
   behind the loading screen's progress bar.
2. **Stop releasing on disable.** Disabling becomes a pure flag.
3. **Keep real unloading as an explicit opt-in**, for later streaming of levels
   that need the memory. For example, an authored chunk field
   `unload_when_disabled: true` (default false), serialised in
   `world/level_schema.go`'s raw chunk and `level_save.go:227`. Or an
   engine-side `world.UnloadChunk(ch)` a script verb can call. Prefer the
   authored field; it keeps today's behaviour one YAML line away.
4. **Keep the index rebuild firing.** This is the trap. `SyncChunkGeometry`
   returning true is the *only* thing that rebuilds the spatial hash after a
   script toggle, and `FindChunkSH` trusts the hash without re-checking
   `IsEnabled` (`spatial.go:108`). Once toggling no longer changes residency,
   `SyncChunkGeometryFor` returns false and a disabled chunk stays solid
   forever.
   - **The fix.** Track the flag state the indexes were last built from, for
     example a runtime-only `Chunk.indexedDisabled bool`. Report a change
     whenever `Disabled != indexedDisabled`, and update it in the same place.
   - **Don't serialise it.** Runtime fields on `Chunk` must not reach the
     compiled gob; `TestTheGobEncodedHalfHoldsNoPointersToScalars` is the guard
     on the raw side.
   - **Keep it through clones.** `Chunk.Clone` must not reset it incorrectly
     (`TestCloneCarriesDisabled`).
5. **Editor toggles.** They call `world.SetChunkEnabled` and then only
   `RebuildSpatialHash` (`editor_panel_shared.go:77-86`,
   `editor_panel_explorer.go:283/320`, `editor_3d.go:586`). That keeps working.
   Make sure `SetChunkEnabled` still returns "changed" when only the flag
   changed.
6. **Stale ends.**
   - `editor_compile.go:141` (`dropCompiledPreview`) reloads every Compiled
     chunk without checking `Disabled`, which is harmless once disabled chunks
     are resident anyway.
   - The stale comments at `app.go:968` and `editor_panel_explorer.go:244`
     mention a 2D map that no longer exists; fix them while you're there.

### Things that get better for free
- **Raycasts.** The raycast accel fingerprints each face's
  `Geometry.Vertices` pointer (`raycast_accel.go:378-418`). With vertices kept,
  a toggle is a re-stamp, not a BVH rebuild.
- **Editor edits.** Edit paths that already call `Brush.Load` on disabled
  chunks (`editor_brush_uv_texture.go:139/203`, `editor_brush_panel.go`,
  `script_runtime.go:849`) stop leaving "Unloaded but has meshes" chunks
  around.

### Tests to update or add
- **Now wrong** (they assert release on disable or no build at load):
  `world/chunk_enable_test.go` `TestDisableReleasesGeometryAndEnableRebuildsIt`,
  `script/script_chunk_enable_test.go` `TestChunkEnabledWriteDefersTheGeometryWork`,
  and `levelio/chunk_disabled_test.go` `TestDisabledChunkIsNeverBuiltAtLoad`.
  Rewrite them around the new contract, and keep a variant for
  `unload_when_disabled`.
- **Still valid:** `TestDisabledChunkIsNotFound`, `TestRaycastSkipsDisabledChunks`,
  `TestDynamicBrushesAndDisabledChunksAreNotBaked` (nav),
  `TestSurfaceUnderSkipsDisabledChunks`, and
  `compile/csg_test.go` `TestDisabledChunksAreLeftOut` (until step 5).
- **New:** a script disables a chunk mid-play and the next frame's
  `FindChunkSH` no longer returns it (the index trap above). Also: toggling a
  resident chunk doesn't bump `world.BrushMeshGeneration()`.

### Acceptance
- **The spike.** With the repro, the `@ground` frame drops from about 2.8 s to
  about `RebuildIndexes` cost (milliseconds). The 2.2 s moves to level load.
- **Nav is separate.** The 0.8 s nav frame remains; that's step 2.

---

## Step 2 — Navigation builds off the main thread, and per chunk

### Today
All in `internal/nav`:

- **The cache.** A package-global single-level cache (`cache.go:32-38`, no
  mutex).
- **`Sync(lvl)` (:48).** Hashes `Signature(lvl)` and drops the heightfield and
  every grid when it changes.
- **`For(lvl, params)` (:62).** Lazily rebuilds the heightfield, shared by all
  profiles, with `buildHeightfield(gatherSolids(lvl), DefaultCellSize, MaxSlopeCos)`
  (:79), then one `buildGrid` per quantised agent profile. `For` itself does
  not re-check the signature.
- **Signature (:110-152).** Covers every enabled world chunk's id and
  **ModelMatrix**, plus every static solid brush's verts and face sizes.
- **Heightfield.** The origin, W, D and **cell** all come from the union AABB of
  all solids (`heightfield.go:200-212`). Above `maxColumns = 1<<20` the cell
  grows (`:206-209`). Column indices therefore shift whenever anything is added
  or removed.
- **Grid (`grid.go`).** `buildGrid` makes nodes (walkable span plus headroom ≥
  height), then `link` (4 neighbours with step and headroom checks, plus
  `linkDiagonals`, which looks two hops away), then `erode` (a two-pass chamfer
  over **global node order**, then renumbering every node), then `label`
  (global flood-fill regions).
- **Queries mutate the grid.** A* scratch lives on `Grid`
  (`grid.go:59-74`, "nothing here is safe to share").
- **Callers.**
  - `nav.For`: `ai/ai.go:378` (wander), `ai/move.go:118` (plan),
    `ai/query.go:44` (MoveTo) and `:173` (FindPath), `ai/objective.go:204`,
    `ai/route.go:312` (cover), `editor/editor_ai_panel.go:557` (overlay).
  - `nav.Sync`: `ai/ai.go:85`, only when some entity has `AI != nil`, and in
    the editor overlay.
  - `nav.Invalidate`: `ai.ResetSession` (`ai/ai.go:63`), called from
    `script.ScriptEventsReset` at level load.
  - Scripts reach it via `find_path` (`script/script_actions_ai.go:215`).

What agents do when the grid is **nil or empty**:

- **`plan` walks a straight line.** It installs a straight-line path to the goal
  (`move.go:119-122`) with `HasPath=true`, and keeps it until the goal jumps or
  drifts or the agent gets stuck for 1 s.
- **Everything else degrades.** Wander goes idle, objectives aim at the volume
  centre, and cover fails.

**An async build must not look like "no navigation"**, or every agent will
start walking straight at walls while a build runs.

### Change, part A: asynchronous build (do this first)
1. **Snapshot on the main thread.** `gatherSolids` already returns fresh
   world-space copies (`heightfield.go:122-173`), so it is the snapshot
   boundary. `Signature` also stays on the main thread.
2. **Build on a goroutine.** Run `buildHeightfield` and `buildGrid` there. The
   codebase has no gameplay goroutines yet; `internal/noise3d` has a worker
   pool to copy style from.
3. **Hand over finished grids on the main thread.** Poll a channel, or check a
   `sync/atomic` pointer in `Sync`. Queries run only on the main thread because
   of the A* scratch.
4. **Serve the old grid while a new one builds.** Add an explicit readiness
   state, e.g. `nav.ForReady(lvl, p) (grid *Grid, fresh bool)`, where `grid`
   may be the previous level shape.
   - **Nothing built yet.** Only when no grid has ever existed for this level do
     agents *hold still* instead of taking the straight-line fallback.
   - **Where to route it.** Through `plan`, wander, objectives and cover in
     `internal/ai`.
5. **Keep a synchronous `nav.For`** for tests, the editor overlay and
   `find_path`. Every test in `nav/nav_test.go`, `ai/ai_test.go`,
   `ai/route_test.go`, `script/script_ai_test.go` and
   `editor/editor_ai_panel_test.go` expects a finished grid immediately.
   `TestSyncRebuildsWhenTheShapeChanges` even checks grid pointer identity.
   Use async only from `ai.Update` in play, e.g. behind a package switch the
   app sets.
6. **Ignore moving chunks.** Exclude moving world chunks from the signature and
   the solids, the same way dynamic brushes are excluded. A moving chunk is one
   with `Velocity != 0` or an active `OriginTween`. Today every frame the
   hangar flies, its ModelMatrix changes the signature and would throw away
   any grid. A moving chunk can't be navigated reliably anyway; agents already
   can't walk vehicle interiors.
7. **Level replacement.** `*level = *built` (`app/level_load.go:100`) replaces
   the level struct in place. Build jobs must be tagged with a generation
   (bumped in `Invalidate`) and their results discarded when it doesn't match.

### Change, part B: per-chunk tiles (do after step 4's split, or measure first)
**Measure before designing.** Time `buildHeightfield` against `buildGrid` for
level1's ground (write a benchmark next to `nav/bench_test.go`, whose
`bigLevel` is 200×200 m with 400 pillars and no recorded numbers). The right
design depends on which half dominates.

- **If rasterising the heightfield dominates** (likely, with 734 brushes
  clipped per column):
  - **Tiles.** Cache column spans per fixed, world-aligned tile, e.g. 32 m
    squares at a fixed 0.25 m cell. Drop the extent-derived origin and coarse
    cell for tiled levels.
  - **What a tile contains.** It rasterises *every* solid whose AABB overlaps
    it, not just its "own" chunk's, because chunks overlap and spans merge
    across solids.
  - **Cache key.** A hash of those solids (the per-brush part of today's
    signature).
  - **Assembly.** Build the global heightfield by copying tiles, then run
    `buildGrid` globally as today.
  - **Result.** Toggling a chunk re-rasterises only the tiles its solids touch.
    This keeps every whole-level grid invariant intact.
- **If `buildGrid` dominates**, make the grid itself tiled. Whole-level coupling
  to handle:
  - **Links cross tiles.** `link` reaches ±1 column and `linkDiagonals` two
    hops.
  - **Erosion at tile edges.** It seeds from missing links, so an unloaded
    neighbour looks like a wall. Erode each tile with an apron of
    `ceil(maxRadius/cell)+1` columns from its neighbours, and never erode
    against a tile that is merely unloaded.
  - **Regions are global.** They drive `Reachable`, `search`'s early-out
    (`path.go:212`) and `RandomPoint`/`SamplePoints` region filtering.
    Recompute them with union-find over per-tile regions plus border links.
  - **Node IDs are global int32s.** They appear in `Links`, `colNodes` and the
    A* scratch. Either renumber at stitch time or make node IDs `(tile, local)`.

### Keep
- **`nav.MaxSlopeCos == collision.MaxSlopeCos`**
  (`TestNavigationAndCollisionAgreeOnTheSteepestFloor`).
- **Disabled chunks, dynamic brushes and frame chunks stay out of navigation.**
- **The editor overlay** (`drawNavigationOverlay`, `editor_ai_panel.go:556`)
  calls `Sync` on every draw while it's on. It should keep showing the grid
  being edited, so give it the synchronous path.

### Acceptance
- **No nav frame.** In the repro, the frame after `@ground` switches on no
  longer contains an 0.8 s `ai.Update`, and no frame during the build exceeds
  budget.
- **Agents hold rather than stray.** Agents don't walk through walls during the
  build window.
- **Tests.** All nav and ai tests pass unchanged, plus new tests for "stale grid
  served while building" and "no grid yet ⇒ hold".

---

## Step 3 — One vertex buffer per chunk, not one per face

### Today
- **Load: one GL object set per face.** Every face is its own `gfx.Geometry`.
  `BrushMesh.Load` (`world/brush.go:344-371`) calls `Geometry.Load`
  (`gfx/geometry.go:185-326`) per face, which:
  - de-indexes into `Vertices` (5 floats per vertex: XYZ plus UV)
  - does two `gl.GetUniformLocation`s by name with no program bound
  - creates a VAO and VBO, and runs `GetAttribLocation` for `vert` and
    `vertTexCoord`
  - optionally adds an NBO for smooth normals, skin buffers and an LBO for baked
    light
- **The name lookups are pointless.** Attribute layouts are fixed in
  `resources/shaders/vertex.glsl`: `vert`=0, `vertTexCoord`=1, `vertNormal`=2,
  skin 3/4, `vertLight`=5.
- **Draw: one call per face.** `Draw`/`DrawWithTransform` (:420-464) runs once
  per face and sets `UseProgram`, model, alpha, smooth, baked-light,
  normal-colour (untextured ⇒ painted with its normal), water, blend
  texture/mask and normal-map uniforms, plus skinning off. Then it binds the
  VAO and texture and calls `DrawArrays`.
- **Main pass** (`app.go:955-1049`). Chunk frustum cull → per-brush `WorldAABB`
  cull → `drawOrQueue(&f.Geometry, brushModel, f.DoubleSided)` per face.
  - **Transparent faces are queued.** `IsTransparent()` =
    `Alpha in (0,1)` or `textureHasAlpha[Texture]`, which **can change at
    runtime** when the texture editor edits a texture. They go into a
    back-to-front sorted pass keyed on the model matrix translation.
  - **Moved brushes.** A brush that isn't at rest multiplies
    `brush.Placement()` into the model matrix.
  - **Detail meshes.** Draw `brush.DrawFaces()` (the detail mesh when present).
- **Shadows** (`render/shadow.go:653-767`). Same walk per directional cascade
  and 6× per point light: per-brush cull, then `drawGeometryShadow` per face
  (position only; shadows ignore alpha textures).
- **No backface culling in the opaque pass.** `GL_CULL_FACE` is only enabled
  for double-sided *transparent* draws (`app.go:1136-1149`), even though the
  comment at `app.go:1040-1045` says the GPU culls back faces. Double-sided
  faces already carry a reverse-wound copy in the mesh (`brush.go:325-338`).
- **Nothing batches brush geometry today.** Compiled `StaticSurface`s are also
  one `Geometry` each (`world/static.go:45-57`,
  `levelio/compiled_load.go:130-183`). The closest precedent for instanced or
  batched drawing is sprites (`render/sprite_render.go`: shared VAO, batches
  keyed by texture, blend and billboard) and scatter.

### Consumers of per-face CPU geometry (these must keep working)
- **Raycasts.** `collision/raycast_accel.go`: fingerprints each face's
  `Geometry.Vertices` pointer and length (:385-407), builds BVH boxes from it
  (:465-494) and tests triangles via `rayGeometry` (`raycast.go:366`).
- **Scatter ground.** `biome/ground.go` `GroundQuery.SurfaceUnder` uses per-face
  triangles (`f.Geometry.Vertices`).
- **Editor.**
  - Picking: `editor_3d_pick.go:257-273`, which falls back to `FacePoints`
    when there are no vertices.
  - Handle occlusion: `editor_3d_visibility.go:147-166`.
  - The entity-editor preview renderer: `world/chunk_preview.go`,
    `editor_entity_editor_render.go:244-288`, which draws each face's `Vao`
    directly.

### Change
1. **Quick win first:** cache uniform locations once per program
   (`gfx.Program` is fixed after init) and use the fixed attribute locations.
   Delete the per-face `GetUniformLocation`, `GetAttribLocation` and
   `Uniform*` calls from `Geometry.Load`. Measure the enable spike before and
   after; this alone may be a large part of the 2.2 s under D3D12 translation.
2. **Per-chunk batches for world chunks** (`FrameOwner == ""`), e.g.
   `render.ChunkBatch`, owned by render and keyed by chunk ID.
   - **Groups.** One group per *material key*: `Texture`, `BlendTexture`,
     `BlendMask`, `Alpha`, `Smooth`/`SmoothAngle` (smooth normals are per face
     today: `ComputeSmoothNormals` is called with one face's vertices, so keep
     that), and `DoubleSided`.
   - **Buffers.** One VAO plus VBO (plus NBO when smooth, plus LBO for baked
     light in compiled chunks) per group.
   - **Per-brush ranges.** Record `(first, count)` for every brush, in brush
     order, so frustum culling stays per brush.
   - **Drawing.** Cull brushes as today, collect contiguous visible ranges, and
     issue one `gl.MultiDrawArrays` per group (available in go-gl v4.1-core).
     Set material uniforms once per group, not per face.
   - **Dynamic or moved brushes** stay on today's per-face path. They need
     their own model matrix, and `AdvanceBrushes` moves them every frame.
   - **Transparency is decided at draw time,** because `textureHasAlpha` can
     flip live. If a group is transparent, push its visible brush ranges into
     the transparent queue: extend `TransparentDraw` (`app.go:34-39`) to carry
     a batch-plus-range alternative to `*gfx.Geometry`. Never cache texture
     handles; they're looked up by name every draw today (`Textures[name]`).
3. **Keep CPU vertices per face** for the consumers listed above. Let
   `Geometry.Load` do its CPU half and skip the GL upload for batched faces.
   The existing `gfx.GeometryUploadsEnabled` split shows where the halves
   separate, but add a per-geometry flag rather than flipping the global. The
   raycast fingerprint then keeps working unchanged.
4. **Invalidation.**
   - **What triggers a rebuild.** Rebuild a chunk's batch when any brush in it
     is (re)loaded. Every mesh change goes through `BrushMesh.Load`, `Release`
     or `Unload`, which bump `world.BrushMeshGeneration()`.
   - **Checking cheaply.** Copy the raycast accel's approach: a per-chunk
     fingerprint over face `Vertices` pointers and lengths, checked only when
     the generation changed (`raycast_accel.go:378-421`).
   - **Chunk slice reallocation.** `level.Chunks` is a value slice that gets
     compacted and re-appended (`levelio/prefabs.go:397-405`,
     `levelio/entity_geometry.go:192-201`, `editor_3d.go:935`). Key batches by
     chunk ID plus slice identity, never by `*Chunk`.
   - **Editor edits.** They call `Brush.Load` per edited brush (list in the
     research notes: `editor_brush_panel.go:181`, `editor_sel_transform.go:152`,
     `editor_mesh_topology.go:167`, UV unwrap, detail, and more). They must end
     up rebuilding only that chunk's batch, ideally only the affected group,
     not the whole level.
5. **Shadows** use the same batches with the shadow program (position only,
   `shadow_vert.glsl` location 0).
6. **Optional, measure separately:** enable `GL_CULL_FACE` (back) for opaque
   single-sided brush faces in the main pass. Brush faces are wound CCW from
   outside. Check for visual regressions (inverted brushes, detail meshes,
   entity interiors) before keeping it.
7. **Out of scope but noted:** a script writing a dynamic brush's
   `position`/`rotation` marks the brush `Dirty`, and `DrainScriptWork` re-runs
   `b.Load()` (`script_runtime.go:815-874`) even though drawing only needs the
   placement matrix. Worth removing once batches exist.

### Acceptance
- **Object count.** The ground chunk creates a few dozen GL buffers instead of
  about 13,700.
- **Frame time.** In the repro, the steady frame with the ground on is close to
  the pre-drop baseline. Main and shadow passes should both drop sharply; count
  draw calls before and after.
- **Visuals.** Textured, untextured (normal-colour), transparent, blended and
  smooth faces look identical.
- **Tests.** Raycast, editor pick and biome tests pass. New tests cover batch
  rebuild after a brush edit, a texture flipping to transparent, and a chunk
  slice reallocation.

---

## Step 4 — Split the ground into streamed chunks

### Prerequisite: collision must see more than one chunk
**Today, a body is only solid against one chunk.**

- **How the chunk is picked.** `collision.MovePlayer`
  (`collision/player.go:135-162`) and `resolveEntityWorld`
  (`collision/entity_collision.go:97-124`; this covers AI agents, grenades and
  vehicles) choose `frame.ChunkAt(lvl, intended)`, falling back to the current
  position. `pushOutOfChunkBrushes` then runs against **that chunk's brushes
  only**, and `StepUp` retries against the same closure.
- **How `ChunkAt` breaks ties.** In the world frame it is `FindChunkSH`
  (`world/spatial.go:108-136`): it checks the single 8 m hash cell that
  contains the body's **centre**, requires the centre inside a chunk's bounds
  (X/Z inclusive, Y with about 1 mm tolerance), and among several matches
  prefers "the chunk whose local Y is closest to its own vertical centre".
- **Particles too.** `ParticleCollide` (`particle_collide.go:48`) does the same
  from the particle's start point.
- **What is already fine.** Raycasts, `LineBlocked`/`ai.LineOfSight`, nav and
  biome all walk every enabled chunk.
- **Tests.** Nothing tests a body crossing two neighbouring world chunks. The
  multi-chunk tests pair a world chunk with an entity's frame chunk.

Consequences once the ground is split:

- **Seams.** A body on a seam ignores the neighbour's floor and walls, so it can
  fall through or walk into a cliff.
- **Overlapping bounds.** Chunk boxes along a winding canyon overlap heavily, so
  the tie-break can pick a chunk with nothing under the body.
- **Low chunks.** A chunk whose tallest brush is walkable doesn't contain a body
  standing on it at all.
- **Already live in level1.** `ground_breach` sits inside `ground`'s box.

**Fix before splitting.**
- **Collect every overlapping chunk.** Add something like
  `world.ChunksOverlapping(lvl, lo, hi, out []*Chunk)`. It walks every hash cell
  the body's AABB covers (padded by the step height for `StepUp`) and returns
  every enabled world chunk whose `WorldBounds` overlaps.
- **Resolve against all of them.** `resolve` runs `pushOutOfChunkBrushes` for
  each, in that chunk's local space via `chunkLocal`/`chunkWorld`, and ORs
  `hitFloor`/`hitCeiling`.
- **Apply it everywhere a single chunk is used today:** `MovePlayer`,
  `resolveEntityWorld`, and `ParticleCollide` (probe every chunk the particle's
  segment overlaps).
- **Leave `UpdateLocation`/`CurrentChunk` single.** It's informational: script
  `current_chunk`, debug overlay, frame stepping-off.
- **Keep frame behaviour.** Frame chunks still resolve through `FindChunkAmong`
  for the frame, and entity brushes through `resolveAgainstEntityBrushes`.
- **Tests to add:**
  - walking across a seam between two terrain chunks at different heights
    doesn't fall
  - a wall in the neighbouring chunk stops the body
  - a body standing on a chunk whose top is walkable (so its centre is above
    the chunk's max Y) still stands
  - `TestStackedChunksStayDistinct` semantics still hold
  - the ship boarding and step-up entity-interior tests still pass

### Split the generator
In `tools/level1_ground_gen.py` (OneDrive project):

1. **Zones.**
   - **The list.** Define an ordered list along the route: landing zone;
     canyon A (up to and including the arch at `CANYON[2]`); canyon B (to
     `CANYON[4]`); the pocket (`CANYON[5]`); canyon C (to the mouth at
     `CANYON[9]`); the basin; the compound interior (including `ground_breach`
     and `ground_breach_rubble`, which stay separate chunks).
   - **Assigning brushes.** Put each brush in the zone containing its XZ
     centroid, using the existing SDFs (`sdf_lz`, `canyon_query`, which
     returns distance, elevation and radius along the capsule chain, and
     `sdf_basin`) or an explicit per-zone boundary line.
   - **Boundary brushes.** A brush straddling a boundary belongs to one zone.
     The multi-chunk collision fix is what makes that safe; don't duplicate
     brushes across chunks.
2. **Chunk origins.** Give each chunk its own origin, at the zone centroid
   snapped to 1/8. The generator already writes chunk-local vertices as
   `world - ORIGIN` and snaps to 1/8 m; keeping local coordinates small also
   keeps float32 error down (see the convexity note below). Chunk IDs look like
   `ground_lz`, `ground_canyon_a`, …. Update `splice`'s `owned_chunks` so the
   old `ground` chunk is removed.
3. **Enemies.** Tag each one `ground_enemy_<zone>` as well as `ground_enemy`.
4. **Seal.** `grown_seal` already grows the saved `seal:` over everything the
   generator emits; keep that working.
5. **Occlusion.** Design zone boundaries at canyon bends, so zone *N+2* can't be
   seen from anywhere in zone *N*'s core. The generator has everything needed to
   check this offline by ray-marching the SDF between sample points. Consider
   adding that check and failing loudly.
6. **Convexity.** Keep the float32 test (`engine_convex`) in the generator.
   `world.Brush.IsConvex` uses the **unnormalised** `geom.TriangleNormal`
   against a fixed 1e-4 in float32, so large slanted faces with a coplanar
   fourth corner fail on rounding alone.

### Streaming triggers
- **No hysteresis on volumes.** `:enter:` and `:exit:` come from diffing
  whether the mover's **position point** is inside the volume this frame versus
  last frame (`script/script_events.go:332-429`). A body jittering on a face
  fires repeatedly.
- **So use one "core" volume per zone,** not boundary volumes. Cores cover each
  zone's interior and leave a gap at every boundary. Entering zone *Z*'s core
  enables *Z−1, Z, Z+1* and disables everything else. The gaps between cores
  change nothing, and the neighbours staying on provides the hysteresis.
- **One shared script.** Write `scripts/level1/stream_zone.flang` with `ref`
  params (`param prev ref`, `param here ref`, `param next ref`; `param` types
  are `ref int float bool str`). The generator writes the params per volume.
- **Where tags work.** Tag-broadcast property writes (`#tag.prop.x = v`) work
  for chunks too (chunks carry `ScriptData`), but explicit refs are clearer.
- **Waking and sleeping enemies.** Enabling a zone wakes that zone's enemies
  (`#ground_enemy_<zone>.prop.visible/.collision_gravity/.ai_enabled = true`).
  Disabling puts them back to sleep. Anything left sleeping with gravity on
  would fall through the missing chunk, which is why the dormant pattern exists.
- **`ship.flang`'s `:skybox:` handler** enables only the landing zone and
  canyon A and wakes their enemies, instead of `@ground`.
  - **The breach chunk.** `@ground_breach` switches on with the compound zone.
    Enabling the basin or compound zone must not undo a breach that already
    happened, so track that in the breach volume's `@self.state`, or simply
    never re-enable `ground_breach` after it has been disabled.
- **Timing.** A script chunk toggle takes effect for collision and drawing on
  the **next** frame: `SyncChunkGeometry` and `RebuildIndexes` run after
  `ScriptTick` (`app.go:721` then `:792`), and nav sees it at the next
  `ai.Update`.
- **Chunk-attached entities.** `Entity.AttachedChunk` riders are not carried,
  hidden or frozen when their chunk is disabled (`chunk_motion.go:43`). Nothing
  in level1 uses them, but don't start.

### Acceptance
- **Timing.** Walking the whole route in the repro (script the player's
  position, or use the headless harness for logic): no frame spends more than a
  few ms on chunk switching.
- **Seams.** No fall-through anywhere along the route, including exactly on
  seams (headless: walk `MovePlayer` along the route and assert the height
  never drops more than the terrain allows).
- **Enemies.** They wake when their zone switches on and path correctly.
- **Breach.** It still works end to end.
- **Verification to re-run.** Walkability and enemy placement, as in the check
  the generator's first version was verified with (headless `nav.For` over the
  enabled zones, `Reachable` checks from the landing zone to the breach front).

---

## Step 5 — Let the compiler bake streamed chunks for release builds

### Today
- **What gets baked.** `compile.Bakes(ch)` = `ch.IsEnabled() && ch.FrameOwner == ""`
  (`compile/csg.go:186-188`, "the one place the rule is written").
  `BakesBrush(b)` = `!b.Dynamic` (:206). Disabled chunks are never baked; the
  docs say so (`docs/guide/23-compiling-levels.md:166-180`). Everything below
  is **one level-wide** structure.
- **CSG.** `gatherSolids` puts every baked brush from every baked chunk into one
  pool (`csg.go:214-328`). Every face is clipped against every overlapping
  solid, with no chunk boundary: the only exclusion is the face's own brush
  (:430). Faces inside another chunk's solid are **deleted permanently**.
- **BSP, outside flood fill and PVS.** One tree built from all faces
  (`compile/bsp.go:94-130`), one PVS (`compile/pvs.go:546-551`).
- **Seal.** `FitSeal`/`eachBakedPoint` (`compile/seal.go:43-92`) only covers
  baked chunks plus the spawn and entities. The loader auto-fits a seal when a
  file has none (`levelio/level_loader.go:571-575`).
- **Light bake.** `compile/light.go:77-203`: static directional and point
  lights (`!Dynamic`, point lights also `Attach.IsZero()`). Shadow rays test the
  single tree, so every baked brush shadows everything.
- **Output.** `levelio.CompiledLevel` (`compiled_level.go:104-126`,
  `CompiledVersion = 3`, gob plus YAML).
  `storableChunks` sets `ch.Compiled = compile.Bakes(src)` (:181).
- **Runtime.** `world.StaticGeometry` (`world/static.go`) holds the surfaces
  (each recording its source `Chunk` and `Brush`), the tree and PVS, and
  `HiddenChunks`. `SyncChunkGeometry` calls `lvl.Static.SetChunkHidden(ch.ID, ch.Disabled)`
  for Compiled chunks **every frame** (`chunk_enable.go:86-88`). Nothing sets
  it at load, so a baked, disabled chunk would show for one frame.
  - **`VisibleSurfaces`** uses the PVS and falls back to *everything* when the
    camera isn't in an interior leaf (`static.go:116-150`).
  - **Shadows** draw every non-hidden static surface.
  - **Raycasts** have one static BVH that skips hidden surfaces.
  - **Collision** uses brushes, not surfaces.
- **Moving chunks aren't excluded — likely a live bug.** Nothing in
  `internal/compile` looks at `Velocity`/`OriginTween`. Baked surfaces are
  drawn in world space with identity and never move; collision follows the
  brushes. **level1's `hangar` chunk is moved by `ship.flang`
  (`@hangar.prop.velocity`) and is baked today**, so in a release build its
  drawing (and raycasts, lighting and PVS) most likely stays put while its
  collision rises. Nobody has confirmed this in a release build yet; do that
  first.

### Change (recommended shape)
1. **Authored chunk markers** (schema, save/load, editor chunk panel checkbox):
   - `streamed: true`: bake this chunk even though it's disabled in the file; it
     starts hidden and scripts show it.
   - `static: false` (or `moves: true`): never bake this chunk, and draw it live
     like a frame-owned chunk. Mark level1's hangar with it.
   - Update `Bakes` to `(ch.IsEnabled() || ch.Streamed) && ch.FrameOwner == "" && !ch.Moves`.
     Every reader in step 5's "Today" list (validate, CSG, source face count,
     seal, compiled chunk storage, editor preview) follows automatically,
     because they all call `Bakes`/`BakesBrush`.
   - A **warning** (or error under `-strict`) when a script writes `origin` or
     `velocity` on a Compiled chunk, like the existing refusal to write a
     placement on a non-dynamic brush.
2. **One rule, validated: baked chunks must be able to coexist.**
   - **Why.** CSG, BSP, PVS and lighting treat all baked chunks as present at
     once. Streamed ground zones are adjacent and fine. Chunks that are
     *mutually exclusive in time and overlap in space* (the hangar above the
     landing zone) must not both be baked: marking the hangar `moves` achieves
     that for level1.
   - **Validate it.** Add a compile-time check that reports any
     face that is clipped away by a solid from a *different* chunk when either
     chunk is `streamed`. That's the signature of two exclusive chunks being
     merged.
   - **The bigger alternative.** Per-group compilation (separate CSG, trees and
     PVS per `bake_group`, with `StaticGeometry` holding several) is a much
     bigger change touching `VisibleSurfaces`, the static raycast BVH and the
     light bake. Only go there if the user needs overlapping exclusive layers
     baked.
3. **Hide at load.** In `levelio/compiled_load.go`, call
   `SetChunkHidden(ch.ID, true)` for every Compiled and Disabled chunk as
   surfaces are built, so there's no first-frame flash.
4. **Lights.** Static lights bake onto every baked surface, and shadows come
   from the single tree.
   - **level1's hangar lights.** `point_light_0..2` are static, cast shadows,
     and sit over the landing zone. Baked, they'd light the canyon forever.
     Either make them `Dynamic` and switch them off in `:atmosphere:` with the
     hangar, or add a light field that excludes a light from the bake. Ask the
     user which they prefer.
   - **Visible today.** The same lights are also visible live after the hangar
     leaves. Worth switching off in `:atmosphere:` regardless.
5. **Bump `CompiledVersion` to 4.** `Compiled` now means something new for
   disabled chunks, and a stale `.fbsp` must be rejected
   (`TestAStaleCompiledLevelIsRejected`).
6. **Batching for release.** Once step 3 exists, batch `StaticSurface`s per
   `(source chunk, material)` the same way. They're one `Geometry` each today,
   so a release build has the same draw-call problem.
7. **Docs and tests to update.**
   - **Docs.** `docs/guide/23-compiling-levels.md` (the "disabled chunks aren't
     baked" section).
   - **Tests.**
     - `compile/csg_test.go` `TestDisabledChunksAreLeftOut`: add a
       `streamed` case.
     - `compile/seal_test.go` `TestFitSealWrapsWhatCompiles`: its disabled chunk
       `off` at x=500 still must not stretch the box, but a `streamed` one
       should.
     - `compile/validate_test.go` `TestValidationSkipsChunksTheCompileSkips`.
     - `levelio/compiled_level_test.go`
       `TestOnlyTheChunksTheCompileCouldNotBakeStayAuthored` (fixture chunk
       `later` is disabled).
     - `editor/editor_compile_test.go`
       `TestThePreviewPutsTheLevelInTheStateAReleaseBuildLoads`.
     - `world/static_test.go` `TestSwitchingAChunkOffHidesItsCompiledSurfaces`.
     - New: a moving chunk isn't baked; a streamed chunk is baked hidden; a
       script write of `velocity` on a compiled chunk warns.

### Acceptance
- **Release visuals.** In the release repro, the hangar visibly rises, and the
  ground zones draw from static surfaces with the PVS.
- **Streaming is a flag flip.** Enabling a zone creates no GL objects and costs
  microseconds.
- **Strict compile.** `foundrycompile -strict level1` still passes; the
  generator's grown seal covers the zones.

---

## Order and scope notes

- **Order.** 1 → 2A → 3 (start with the uniform-location cache) → 4's
  collision prerequisite → 2B (tiles, now that there are real zones to measure)
  → 4's split and triggers → 5.
- **Commit and ask as you go.**
  - **Commits.** Each step leaves the game working; commit per step if the user
    asks for commits (don't commit unprompted).
  - **Decisions.** Report the measured numbers back after each step. Anything
    that changes authored-file semantics (step 1's unload opt-in, step 5's chunk
    markers and the light policy) is a design choice to confirm with the user
    before building.
- **Pre-existing issues seen along the way (not part of this work):**
  - `scripts/level1/ship.flang`'s `do_despawn` writes `@self.prop.enabled` on an
    entity, which has no such property (runtime error when the ship despawns).
    It also has a `:despawn:` handler the linter warns about.
  - `foundrycompile` reports entities outside a level's saved seal as leaks;
    that's why the level1 generator grows the seal.
