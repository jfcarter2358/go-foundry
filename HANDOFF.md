# Handoff: live chunk transforms (Phases 1–4 done)

## Where this started

The user wanted to move a chunk in the editor without rewriting every point
in it, and observed that the 2D editor treats a chunk's points as if they sit
at world (0,0) even when the chunk's `Origin` is far from there. Root cause:
`Chunk.Points` (and every other per-chunk coordinate) was baked into world
space once at load time, using the chunk's `Origin/Rotation/Scale`, and the
editor's "move chunk" gizmo worked by literally rewriting every point by a
delta rather than changing a transform.

The agreed fix, confirmed with the user across several scoping questions, is
architectural: make `Chunk.Points` (and everything derived from it) stay
**chunk-local always**, and make `Origin/Rotation/Scale` a **live runtime
transform** — the same idea the engine already used for vehicle-interior
("frame-owned") chunks via `Chunk.Xform`, now generalized to every chunk,
including biome scatter, procedural detail, and water (the user explicitly
chose the fully-general option over a narrower one that would have kept those
three features restricted the way frame-owned chunks are today).

The work was split into four phases. **All four are now done and sitting in
the working tree** (not yet committed — see `git status`/`git diff`). Phase 4
(biome scatter / procedural detail / water) is written up below in the same
level of detail Phases 1–3 got, including a few things the original Phase 4
plan (preserved below, in the "what's left" section, for the record) got
wrong or missed — the plan assumed the CPU generation for all three
subsystems was already fully local-and-transform-agnostic; that turned out to
be true for water and procedural detail but not for biome scatter, whose
GPU-side lattice is anchored to the *world* camera position, not the sector's
local frame. See "Phase 4 — what actually happened" for the corrected story.

## The on-disk format: unchanged by the transform work, one field added later

Worth saying up front: `.yaml` level files already stored chunk-local points
plus a separate `origin`/`rotation`/`scale` block. The transform work itself
(Phases 1–4) is an in-memory/runtime representation change, not a file-format
migration. A level file written before any of it landed loads and saves
identically (confirmed by the round-trip tests in
`internal/levelio/chunk_transform_test.go` and by grepping the repo's own
`.yaml` files for `scale`/`origin.y` — none of them use anything but the
identity, so there's no legacy-data migration concern in this repo).

One field was added afterwards, in follow-up 8: `drape_sector` on a biome
sector, recording which structural surface the paint was drawn over. It is
`omitempty` and every consumer falls back to the previous behaviour when it is
absent, so old files still round-trip byte-identical and still render exactly
as they did — but it is a genuine format addition, and a file written by this
build will not be understood by an older one.

---

## The new API (start here)

Everything downstream of Phase 1 depends on this. It lives in
`internal/world/chunk.go`:

```go
func (ch *Chunk) LocalPlacementMatrix() mgl32.Mat4   // this chunk's own Origin/Rotation/Scale, no frame Xform
func (ch *Chunk) ModelMatrix() mgl32.Mat4            // LocalPlacementMatrix, composed under Xform if frame-owned
func (ch *Chunk) LocalToWorld(p mgl32.Vec3) mgl32.Vec3
func (ch *Chunk) WorldToLocal(p mgl32.Vec3) mgl32.Vec3
func (ch *Chunk) LocalToWorldDirection(d mgl32.Vec3) mgl32.Vec3  // no translation — for offsets/deltas, not points
func (ch *Chunk) WorldToLocalDirection(d mgl32.Vec3) mgl32.Vec3
func (ch *Chunk) WorldBounds() (min, max mgl32.Vec3) // MinBound/MaxBound (chunk-local) carried through ModelMatrix
```

`transformAABB(m mgl32.Mat4, lo, hi mgl32.Vec3) (min, max mgl32.Vec3)` is the
private 8-corner-refit helper both `WorldBounds()` and `Sector.WorldAABB(ch)`
use — reuse it if Phase 4 needs another AABB-through-a-matrix operation.

**Composition rule, memorize this**: for a chunk with no `FrameOwner`,
`ModelMatrix() == LocalPlacementMatrix()` — they're the same matrix. For a
frame-owned chunk (vehicle interior), `ModelMatrix() = Xform · LocalPlacementMatrix()`
— the chunk keeps its own authored placement *within* the frame, composed
under the frame's own live transform. `RefreshFrames` (`internal/world/frame.go`)
only ever writes `Xform`; it has no idea `LocalPlacementMatrix` exists, and
doesn't need to.

`Sector` gained the matching pair in `internal/world/sector.go`:

```go
func (s *Sector) LocalAABB() (min, max mgl32.Vec3)        // was called WorldAABB() before Phase 1 — renamed
func (s *Sector) WorldAABB(ch *Chunk) (min, max mgl32.Vec3) // LocalAABB carried through ch.ModelMatrix
```

Every place that used to read `Sector.WorldAABB()` with no chunk argument
either now calls `LocalAABB()` (if it only ever needed local bounds — e.g.
`Chunk.RecomputeBounds`) or `WorldAABB(ch)` (if it needed a true world box —
e.g. `internal/world/spatial.go`'s spatial-hash bucketing). **Grep for
`Sector) WorldAABB\|\.LocalAABB()\|\.WorldAABB(ch` if you need to find every
caller** — `internal/render/detail_render.go` and `internal/render/grass.go`
and `internal/render/biome_ground.go` currently call `LocalAABB()` (i.e. they
do **not** yet compose the chunk transform for culling) — that's exactly the
gap Phase 4 closes.

`Chunk.Points`, `Chunk.MinBound`/`MaxBound`, `Sector.MinBound`/`MaxBound`,
`Floor.Height`/`Ceiling.Height`/`VertexOffsets`, `Wall`/`Portal`
`StartPoint`/`EndPoint`, extrusion and terrain points, and
`BiomeSector.Points` are now **all chunk-local, unconditionally, for every
chunk** — frame-owned or not. There is no more "world chunk vs. frame chunk"
distinction in what space geometry lives in; there's only ever "chunk-local",
and `ModelMatrix()` is the one thing that carries it out.

---

## Phase 1 — data model, loader/saver, structural rendering

**Files:** `internal/world/chunk.go`, `internal/world/sector.go`,
`internal/world/level_schema.go` (doc comments only),
`internal/levelio/level_loader.go`, `internal/levelio/level_save.go`,
`internal/app/app.go`, `internal/render/shadow.go`,
`internal/render/detail_render.go` (one rename fixup),
`internal/script/script_props_geom.go` (doc comments).

- `loadRawChunk`/`chunkToRaw` no longer bake/unbake `Origin/Rotation/Scale`
  into points — `transformPoint2D`/`reverseTransformPoint2D` are deleted
  entirely. Points, extrusion points, terrain points, biome-sector points are
  now straight field copies both directions. This made the loader/saver
  *smaller* (more deletions than insertions) — that's an intentional, checked
  signal in the commit that the bake/unbake round-trip complexity is gone.
- `ModelMatrix()` stopped being conditionally identity based on `Xform`'s
  zero-value and became a real composed matrix (see API section above).
- **Scale is a true uniform 3D scale**, not XZ-only (the user's explicit
  choice over keeping the old XZ-only behavior). This required no extra work:
  since `Floor.Height`/`Ceiling.Height` are baked straight into local vertex
  Y (same as X/Z), one uniform-scale matrix scales all three together with no
  special-casing.
- `app.go`'s render loop and `shadow.go`'s shadow pass dropped their
  `if chunk.InFrame()` gates around `TransformAABB`/culling calls — those
  paths already read `chunk.ModelMatrix()` unconditionally (this was a
  pleasant discovery: rendering's model-matrix plumbing was already generic,
  it just never got a non-identity matrix for a non-frame chunk before).
  `app.go`'s `chunkCam` (used for backface culling) now inverse-transforms
  through the chunk's full `ModelMatrix()`, not just the frame's.
- **Origin.Y "migration" turned out to be a non-issue for this repo**: the
  plan called for either zeroing legacy `Origin.Y` on load or subtracting it
  out of baked heights (since `Origin.Y` used to be dead — never applied to
  geometry). A grep of every `.yaml` file in the repo confirmed `origin.y` is
  `0` everywhere already, so **no migration code was written**. If a level
  file from outside this repo has a non-zero `origin.y`, loading it under the
  new code will apply that Y as a real vertical offset for the first time —
  worth a mention if that ever comes up, but it isn't a Phase 4 concern.

**Tests:** `internal/world/chunk_transform_test.go` (ModelMatrix/WorldBounds
composition, including the frame-Xform case),
`internal/levelio/chunk_transform_test.go` (load doesn't bake, save/load
round-trips bit-identical).

---

## Phase 2 — spatial hash and collision

**Files:** `internal/world/spatial.go`, `internal/collision/collision.go`,
`internal/collision/player.go`, `internal/collision/entity_collision.go`,
`internal/collision/frame_portal.go`.

### Spatial hash (`spatial.go`)

`BuildSpatialHash` now buckets each sector by `Sector.WorldAABB(ch)` (world
space) instead of the chunk-local `Sector.MinBound/MaxBound`. Bucketing is
necessarily conservative for a rotated sector (its axis-aligned world box is
bigger than the actual polygon); `FindChunkAndSectorSH` resolves each
candidate exactly by inverse-transforming the query position into *that
candidate's own* local space (`chunk.WorldToLocal`) before the
point-in-polygon test — cached per chunk index since several sectors of one
chunk commonly share a cell. `FindChunk`/`FindChunkAndSector` (the linear
fallbacks) got the matching fix.

`FindChunkAndSectorAmong` (used by `Frame.ChunkAndSectorAt` for a
frame-riding query) is subtly different on purpose: its `pos` argument
arrives **already carried out of the frame** by the caller
(`Frame.FromWorld`), so it only needs to undo the *candidate chunk's own*
`LocalPlacementMatrix()` — not the full `ModelMatrix()`, which would
double-subtract the frame's `Xform`. This is the one place in the whole
change where `LocalPlacementMatrix()` (not `ModelMatrix()`) is the correct
conversion, and it's the reason that method exists as a separate,
exported thing.

### Collision (`collision.go` + callers)

`collision.go`'s low-level resolvers (`ResolveWallCollision`,
`ResolveSlopeCollision`, `ResolveFloorCollision`, `sectorFloorHeightAt`,
`sectorCeilingHeightAt`, `resolveWallsForSector`) needed **zero changes** —
they're frame-agnostic by construction, trusting that whatever position
they're handed is already in the same space as the `chunk`/`sector` geometry
they're given.

The actual bug (not anticipated by the original plan, found while
implementing) was one level up: `player.go`/`entity_collision.go`/
`frame_portal.go` would correctly *find* the right chunk+sector via the fixed
spatial-hash lookup, then hand the resolvers a position still in
world/frame space — while `SectorPoints`/wall/portal geometry is chunk-local.
Fixed by adding `chunkLocal`/`chunkWorld` helpers to `collision.go` (using
`LocalPlacementMatrix()`, for the same "already carried out of the frame"
reason `FindChunkAndSectorAmong` does) and threading them through every
resolve call site: `MovePlayer`'s two wall/slope/floor resolves,
`resolveEntityWorld`'s matching pair, `FindFramePortalCrossing`, and
`ResolveSealedPortals`.

**Tests:** `internal/world/spatial_transform_test.go` (moved/rotated/scaled
chunk resolves correctly through the hash and every linear fallback; two
differently-transformed chunks sharing a spatial-hash cell each resolve only
their own query; a frame-owned chunk's own placement is honored on top of its
frame). `internal/collision/player_test.go` gained
`TestMovePlayerTreatsChunkOriginTheSameAsShiftedPoints` and
`...AndRotationTheSameAsShiftedRotatedPoints` — these compare a player
walking against a chunk placed via live `Origin`/`Rotation` to the same walk
against a chunk whose raw points were shifted by hand, and assert bit-for-bit
agreement. These are the strongest tests in the whole change: they prove
real gameplay collision (wall pushes, floor landing) works through the new
architecture end to end, not just that the right sector gets found.

---

## Phase 3 — editor

**Files (the wide-but-shallow part):** `internal/editor/editor_2d.go`,
`editor_2d_select.go`, `editor_2d_context_menu.go`, `editor_2d_objects.go`,
`editor_biome.go`, `editor_extrusion.go`, `editor_3d_pick.go`,
`editor_3d_face_select.go`, `editor_sel_transform.go`,
`editor_translate_gizmo.go`, `editor_rotate_gizmo.go`,
`editor_scale_gizmo.go`, `editor_panel_shared.go`, `editor_3d.go`,
`internal/levelio/level_save.go` (`ImportChunkIntoLevel` signature).

Two bugs turned out to be the real cost center, both found during
implementation rather than anticipated by the plan:

1. **Picking/gizmo code was reading chunk-local coordinates as world
   coordinates, everywhere.** `pick3D`'s wall/floor/ceiling ray tests used
   raw (chunk-local) vertex buffers against a world-space ray — fixed by
   routing every wall/floor/ceiling pick through `pickModelT(..., ch.ModelMatrix())`,
   the same helper spawn/water picks already used. The whole gizmo system
   (2D map's own gizmo *and* the 3D translate/rotate/scale gizmos) funnels
   through one shared abstraction — `vertexSelPos`/`selectedVerticesWorld`
   (read side, `editor_3d_pick.go`) and `writeSelectedVertexWorld` (write
   side, `editor_sel_transform.go`) — which is good news: fixing that one
   seam (wrap the read in `ch.LocalToWorld`, the write in `ch.WorldToLocal`)
   fixed both gizmo systems and every caller of `setSelectedVertexWorld` at
   once, rather than needing per-caller fixes.
2. **The 3D translate gizmo's axis-drag had a second, separate vertex-write
   path** (`dragVerticesAlongAxis` → `setSelectedVertexPoints`, using
   `Editor.GizmoDragPoints`) that bypassed the `vertexSelPos`/
   `writeSelectedVertexWorld` abstraction entirely and applied a
   world-space delta straight to local coordinates — wrong under rotation or
   scale. Fixed to convert through `ch.WorldToLocalDirection`, with an
   explicit fast path for `ch.Rotation == 0` that reproduces the *exact* old
   per-axis-untouched behavior (this matters for grid-snap correctness: the
   old code left the non-dragged axis completely untouched rather than
   passing it through a snap it was never asked for; a rotated chunk can't
   preserve that property since a world-axis drag isn't a single local axis
   anymore, so it snaps both components together there instead — flagged in
   a comment at the fix site).

Beyond those two: `moveChunk` (`editor_3d.go`) collapsed from ~35 lines of
point-rewriting to `ch.Origin = newOrigin` — the biggest before/after
contrast in the whole change, and exactly the behavior the user originally
asked for. Rotate (Y-axis-only — `Chunk.Rotation` is a single scalar) and
scale (uniform-only — `Chunk.Scale` is a single scalar) gizmo support was
*added* for chunks (didn't exist before at all), gated by
`gizmoChunkRotates()`/`gizmoChunkScales()` so only the meaningful
handle/ring is shown. `editorChunkTransform` in `editor_panel_shared.go`
adds a numeric Origin/Rotation/Scale block to the chunk properties panel.
`createChunkAtCamera` and `ImportChunkIntoLevel` (new `target mgl32.Vec3`
parameter) now build/place geometry via `Origin` instead of baking the
camera/cursor position into points.

**Tests:** `internal/editor/editor_transform_test.go`
(`TestVertexSelPosMatchesChunkModelMatrix`,
`TestSetSelectedVertexWorldWritesBackToLocalSpace` — the central
gizmo-abstraction round trip through a rotated+offset chunk).
`internal/editor/biome_anchor_test.go`'s `TestChunkMoveCarriesAllItsPaint`
was rewritten as `TestChunkMoveLeavesItsPaintAlone` to assert the new (and
opposite) invariant: moving a chunk no longer touches biome points at all,
because they're chunk-local and ride the transform automatically. **If you
touch `moveChunk` or biome paint in Phase 4, re-read this test — its whole
point is now the opposite of what it originally checked.**

**A gotcha if you add editor tests that call `ReloadSector`/`Sector.Load`
with `Floor.Enabled`/`Ceiling.Enabled` set to `true`:** that path makes a
real OpenGL call and segfaults without a context. Wrap the test body in
`testenv.WithoutGeometryUploads(t)` — see any of the new test files for the
pattern. Existing fixtures that don't hit this (e.g.
`internal/editor/biome_anchor_test.go`'s `anchorTestLevel`) deliberately keep
Floor/Ceiling disabled instead.

---

## Phase 4 — original plan (historical record; corrected below)

This section is preserved as it was written before Phase 4 started. It got
the water and procedural-detail stories right; it got biome scatter's "just
add a model uniform at the end" story wrong. See "Phase 4 — what actually
happened" further down for the corrected, implemented version.

**Scope, per the user's explicit choice:** generalize biome scatter,
procedural face detail, and water so they respect a chunk's live
`Origin/Rotation/Scale` — *and* lift the `InFrame()` restriction so
frame-owned (vehicle-interior) chunks get these features too, now that the
underlying machinery will support a live matrix for them as well. This is
the larger of the two options that were on the table; the user chose it over
the smaller "leave these three features restricted like frame chunks are
today" alternative.

### The current state (confirmed while implementing Phases 1–3, still true)

All three subsystems already follow "generate CPU-side mesh/instance data
once from `s.Points`/`bs.Points` (now chunk-local, same as everywhere else),
cache it, rebuild on a dirty flag" — this is *good news*: the expensive part
(CPU generation) doesn't need to become transform-aware, only the *draw
call* does. Concretely, per subsystem:

**Water — smallest change, no new shader.**
- `internal/biome/biome_water.go`'s `BuildBiomeWaterMesh`/
  `BiomeWaterCentroidAndFloor` already operate on chunk-local `s.Points`
  and local sector floor heights (this became true automatically once Phase
  1 landed — no separate fix was needed for water's *mesh generation*).
  `WaterSurface.Position`/`Geometry` (`internal/world/water_surface.go`) end
  up chunk-local as a result.
- The gap is purely at draw/cull time. `internal/app.go`'s water draw loop
  (search `chunk.WaterSurfaces` — right after the sector loop, inside the
  same per-chunk block that already computes `chunkModel := chunk.ModelMatrix()`
  for structural geometry) currently does
  `drawOrQueue(&w.Geometry, w.ModelMatrix(), true)` — change to
  `drawOrQueue(&w.Geometry, chunkModel.Mul4(w.ModelMatrix()), true)`.
  `WaterSurface.WorldAABB()` (`internal/world/water_surface.go`) needs the
  same composition for its cull box — mirror how `app.go`'s structural
  sector cull already does `sectorMin, sectorMax = render.TransformAABB(chunkModel, ...)`.
  Water already draws through the shared `drawOrQueue`/main-shader pipeline
  (confirmed: `vertex.glsl` already has a `model` uniform), so **no GLSL
  changes are needed for water at all.**
- `internal/biome/biome_water.go:45` has `if level.Chunks[ci].InFrame() { continue }`
  — remove it (per the "lift the restriction" scope decision).
- `internal/editor/editor_3d_face_select.go`'s `editorOutlineWaterSurface`
  currently has a comment noting it inherits water's not-yet-fixed
  limitation for a moved chunk — once this lands, that comment is stale and
  the function's existing `ch.LocalToWorld` call becomes fully correct
  (no editor-side change needed there, just delete the now-wrong comment).

**Procedural face detail — needs a shader change, but scoped.**
- `internal/render/detail_render.go`: `forEachDetailFace` (~line 227) has
  `if ch.InFrame() { continue }` at ~line 246 with a comment explaining detail
  is a "world-space pass... no model matrix anywhere in the pipeline" — that
  comment will be wrong once this lands; remove the gate and update/delete
  the comment.
- `detail.tesc`/`detail.tese` (tessellation control/evaluation — the color
  pass) and `detail_shadow.tese`/`detail_shadow_cube.tese` (the two shadow
  variants) do **not** have a `model` uniform today (confirmed by grep —
  contrast with `vertex.glsl`/`shadow_vert.glsl`/`shadow_cube_vert.glsl`,
  which do). Add one, applied **once**, to the final displaced local
  position (base position + the computed displacement offset along the
  face's own `dispNormal`) — mirror `vertex.glsl`'s existing pattern for
  both position and normal (`transpose(inverse(mat3(model)))`). The
  displacement math itself (`detail_common.glsl`, and `FaceBasisFor`/
  `BoundaryEdgesFor`/`DispNormalFor` in `internal/world/detail.go`) works
  entirely in local space already and needs **no change** — it's internally
  self-consistent (comparing local positions against local positions); the
  transform only needs to apply at the very end, once.
- Go side: `RenderDetailColor`/`RenderDetailDirShadow`/`RenderDetailCubeShadow`
  (`detail_render.go`, ~lines 422/501/547) each drive `forEachDetailFace`
  per-chunk already — add a `ch.ModelMatrix()` lookup and
  `gl.UniformMatrix4fv` upload per chunk in each, mirroring exactly what
  `app.go:589` and `shadow.go` (`renderSceneShadow`, ~line 667) already do
  for structural geometry. You'll need to locate/cache the new uniform's
  location the same way `detail_render.go:46`'s existing
  `gl.GetUniformLocation` pattern does for the program's other uniforms.

**Biome scatter (grass/rocks/wildflowers/cattails/moss) and ground
patches — same shader-uniform pattern, larger shader surface.**
- `internal/render/grass.go`: `forEachScatterSector` (~line 1058) has the
  identical `InFrame()` gate + "world-space pass" comment at ~line 1068 —
  same fix. `RenderBiomes`/`RenderBiomeScatterDirShadow`/
  `RenderBiomeScatterCubeShadow` (~lines 1117/1153/1196) are the per-chunk
  draw drivers to add the uniform upload to.
- `internal/render/biome_ground.go`: `RenderBiomeGround` (~line 250) has the
  same gate at ~line 265; it's a single pass (no separate shadow variant to
  find — ground patches are lit like ordinary geometry, check whether they
  cast shadows through the *structural* shadow path already, or need their
  own — verify before assuming).
- Shaders needing a `model` uniform (none have one today, confirmed by
  grep): `biome_vert.glsl`, `biome_cube_vert.glsl`, `biome_shadow_vert.glsl`
  (scatter — color/point-shadow/dir-shadow), `ground_vert.glsl` (ground
  patches — check if it has its own shadow variant or reuses another pass).
  These generate instance positions/orientations **procedurally in the
  vertex shader** (per `grass.go`'s own package doc comment) — the
  transform needs to apply to the *final* computed per-instance position and
  normal, not to any of the intermediate procedural placement math, which
  stays entirely local and unaffected. `detail_scatter_vert.glsl`/
  `detail_scatter_cube_vert.glsl`/`detail_scatter_shadow_vert.glsl` and
  `detail_scatter_common.glsl` are also detail-related scatter files (rock
  chips from procedural face detail, distinct from biome grass) — check
  whether they're driven by the same `forEachDetailFace`/`InFrame()` gate or
  need their own fix; not fully traced during Phases 1–3.
- `internal/biome/biome_displace.go` (`BiomeMeshPoints`/`CcwPolygon` —
  ground-patch triangulation and scatter placement bounds) needs **no
  change** — it already consumes chunk-local `s.Points`/`bs.Points` and its
  math (triangulation, point-in-polygon) is coordinate-system-agnostic.

### Open question to resolve before or during Phase 4

The original research (before Phases 1–3 started) flagged: *"does the
editor actually refuse to let you paint biome/detail on a frame-owned
chunk today?"* — this was never confirmed by grep in this codebase; a
`grep -rn "InFrame" internal/editor/editor_biome.go` at the time found
nothing, suggesting the editor may not enforce this at all today (only the
*rendering* code skips frame chunks). Re-verify before assuming there's an
editor-side restriction to also lift.

### Suggested phase-4 sub-order

1. Water first — smallest, no shader work, good warm-up to re-confirm the
   `ch.ModelMatrix()`-composition pattern before touching GLSL.
2. Procedural detail — one shader family (tesc/tese ×2 shadow variants),
   scoped, well-understood displacement math to leave alone.
3. Biome scatter + ground patches — most shader surface, do last.
4. Drop every remaining `InFrame()` gate in the three areas above once each
   is verified working for a *non-frame* transformed chunk, then specifically
   test a frame-owned chunk with biome paint/detail (a vehicle interior) —
   this is new behavior with no existing test coverage, and per-frame
   perf should be sanity-checked since these are now doing a uniform
   upload every frame for a chunk whose `Xform` changes every tick (should be
   cheap — one 4x4 upload per chunk per pass — but hasn't been measured).

### Test pattern to follow

Every phase so far added a small, targeted regression test proving the new
behavior against a deliberately-transformed (moved/rotated/scaled) chunk,
usually by comparing against an equivalent chunk whose raw geometry was
shifted by hand (see `TestMovePlayerTreatsChunkOriginTheSameAsShiftedPoints`
in `internal/collision/player_test.go` for the strongest example of this
pattern). For Phase 4, the equivalent would be a headless test that builds a
biome-painted/detail-enabled/water sector on a transformed chunk and asserts
the generated mesh/instance data, once carried through `ch.ModelMatrix()`,
lands where a manually-shifted equivalent chunk's would — GPU-side shader
changes themselves aren't unit-testable, but the Go-side matrix composition
and upload wiring is.

---

## Phase 4 — what actually happened

Implemented in the order the plan above suggested (water, then detail, then
biome scatter), plus two things the plan didn't anticipate.

### Water — went exactly as planned

`internal/world/water_surface.go` gained the `Sector`-style pair:
`LocalAABB()` (renamed from the old `WorldAABB()`) and `WorldAABB(ch *Chunk)`
(new — `LocalAABB` carried through `ch.ModelMatrix()`, using the same private
`transformAABB` helper `Sector.WorldAABB(ch)` already uses). `ModelMatrix()`
stayed a translation-only local matrix, composed by the caller — exactly as
the plan called for. Every call site that read the old no-arg `WorldAABB()`
was updated to whichever of the two it actually meant:
`Chunk.RecomputeBounds` (wants local, for chunk-local `MinBound`/`MaxBound`)
→ `LocalAABB()`; `app.go`'s water draw loop, `editor_3d_pick.go`'s water
ray-pick, and `editor_3d_camera.go`'s water-framing camera cut → `WorldAABB(ch)`
composed with `chunkModel`/`model`. `biome_water.go`'s `InFrame()` gate is
gone. `editor_3d_face_select.go`'s stale "water rendering is identity-chunk-only"
comment is deleted, per the original plan's own note that this would happen.

### Procedural face detail — went exactly as planned, GLSL included

`detail_render.go`'s `detailFace` struct gained a `model mgl32.Mat4` field
(the owning chunk's `ModelMatrix()`, captured once per chunk in
`forEachDetailFace`); the `InFrame()` gate and its stale "world-space pass"
comment are gone, and the chunk/sector cull test (previously comparing
chunk-local bounds straight against the world-space cull `Frustum` — silently
correct only because every chunk let through was identity) now carries the
sector's `LocalAABB()` through the chunk's model matrix first, mirroring
`shadow.go`'s `renderSceneShadow`. `RenderDetailColor`/`DirShadow`/`CubeShadow`
each track a `current` matrix (new helper `uploadDetailModel`) and upload
`model` only when a face's chunk differs from the last one drawn — a level of
identity chunks still pays for exactly one upload per program per frame, not
one per face.

GLSL: `detail.tese`, `detail_shadow.tese`, `detail_shadow_cube.tese` each
gained a `model` uniform, applied once to the *displaced* local position
(`detailDisplace(w)`'s result) — the displacement math itself
(`detail_common.glsl`) is untouched, exactly as planned. `detail_vert.glsl`'s
stale "model is identity" comment is gone. The rock-chip scatter side
(`detail_scatter_vert.glsl`/`_shadow_vert.glsl`/`_cube_vert.glsl`,
`detail_scatter_common.glsl`) — which the original plan flagged as "not fully
traced" — turned out to follow the exact same pattern: `scatterPlaceN`'s
placement math is entirely chunk-local (built from `faceOrigin`/`faceU`/
`faceV`/`faceNrm`, themselves chunk-local since they come from
`detail.FaceBasisFor` on chunk-local geometry), so each of the three vertex
stages just applies `model` once to the returned position/normal before using
them. No changes needed in `detail_scatter_common.glsl`'s placement logic
itself, only its doc comments (now say chunk-local instead of world).

Added `internal/render/detail_render_test.go`
(`TestForEachDetailFaceComposesChunkTransform`) — the Phase 4 equivalent of
the collision package's shifted-points tests: builds a chunk moved 500 units
along X with a detail-enabled floor, and proves (a) the emitted `detailFace`
carries `ch.ModelMatrix()`, and (b) a cull frustum framing world X∈[400,600]
sees the face while one framing the origin does not — i.e. the cull box is
actually composed through the transform, not tested against raw local
coordinates.

### Biome scatter + ground patches — the plan's "just add model at the end" was wrong for scatter

**Ground patches matched the plan exactly.** `ground_vert.glsl`'s baked,
already-height-conformed `aPos`/`aNormal` are chunk-local; it gained a
`model` uniform and now transforms both (inverse-transpose for the normal,
same reasoning `vertex.glsl` documents) before `biomeViewDepth`/anything
downstream reads them. `biome_ground.go`'s `RenderBiomeGround` lost its
`InFrame()` gate and now tracks/uploads `model` the same way the detail
passes do (new `groundPatches.uModel` location, resolved once in
`GroundInit`).

**Biome scatter (grass/rock/wildflower/cattail/moss) needed a real
architecture fix, not just a uniform.** Tracing `biome_scatter_common.glsl`
(the file shared by `biome_vert.glsl`/`biome_shadow_vert.glsl`/
`biome_cube_vert.glsl`) turned up something the original research missed:
unlike detail's scatter, biome scatter's instance *lattice* is generated
directly around the **world-space camera position** (`camXZ`/`baseIdx =
floor((camXZ - range)/spacing)` in `biomePlace`) — not from the sector's own
local basis. That world-anchored lattice is then point-in-polygon tested
against `polyPts` and sampled against `heightTex`, both of which are
chunk-local (`s.Points`, the height/coverage texture built in
`biome_ground.go`'s `ensureBiomeHeight`). Applying `model` only to the
*final* placed position, per the original plan, would have left the
lattice/polygon test comparing a world-space candidate position against a
chunk-local polygon for any non-identity chunk — instances would be
generated in the wrong place (or, for a chunk far from the origin, nowhere
near the sector at all).

The fix: `camXZ` is no longer uploaded once per frame. `grass.go` gained
`uploadBiomeChunk(p, current *mgl32.Mat4, ch, camPos)`, called once per chunk
inside `forEachScatterSector`'s callback (same `current`-tracking pattern as
detail): it uploads `model` **and** `camXZ = ch.WorldToLocal(camPos)`
together, whenever the chunk changes. That puts the camera position into the
*same* chunk-local frame every other placement uniform (`polyPts`,
`heightOrigin`/`heightSize`) already lives in, so the whole lattice/polygon/
height pipeline in `biome_scatter_common.glsl` runs self-consistently in one
frame — chunk-local — exactly as detail's scatter already did. Only the
*final* `bWorldPos`/`bNormal` `biomePlace` returns then get carried out to
true world space, once, by `model` — in `biome_vert.glsl` this has to happen
**before** `biomeViewDepth`/`biomeLighting` are called (they need real world
positions for shadow-cascade/point-light math, unlike detail's tese stage
which only needs the transform right before `gl_Position`); in
`biome_shadow_vert.glsl`/`biome_cube_vert.glsl` it's the same one-line
`model *` insert as every other shadow vertex stage in the codebase.
`camPos` (a separate uniform, the *real* untransformed world camera position,
used only by `biome_frag.glsl`'s rock specular) is untouched — it was never
part of the lattice math despite being declared next to `camXZ`.

Spacing/range/jitter/edge-fade config values (all authored as "world units")
are deliberately **not** rescaled by the chunk's `Scale` — they're applied as
chunk-local quantities and scale naturally when `model` carries the result
out, the same precedent `Chunk.Scale` already sets for `Floor.Height`/
`ScatterScale` (Phase 1) and detail's own `ScatterScale`. A scaled chunk's
grass gets proportionally denser/sparser in world terms, matching how
everything else about a scaled chunk scales together.

`grass.go`'s `forEachScatterSector` lost its `InFrame()` gate; its callback
signature grew a `ch *world.Chunk` parameter (every caller updated).
`biomeScatterBounds` (the shadow-cull box) previously clipped the sector's
*chunk-local* polygon bounds directly against the *world-space* camera
position and cull frustum — silently correct only for an identity chunk, the
same latent bug class the detail cull fix above hit. Fixed to carry the
polygon bounds through `ch.ModelMatrix()` via `TransformAABB` before
clipping, and to use `ch.WorldBounds()` (not the chunk-local
`MinBound`/`MaxBound`) for the vertical span. Added
`TestBiomeScatterBoundsComposesChunkTransform` in
`internal/render/biome_light_test.go`, right next to the existing
(identity-chunk) `TestBiomeScatterBoundsClipsToCameraRing` — same ring-clip
geometry, moved 200/300 units by a chunk `Origin`, asserting the returned box
shifts by exactly that offset (the collision package's
`TestMovePlayerTreatsChunkOriginTheSameAsShiftedPoints` pattern, applied
here).

### Two extra fixes found while implementing, beyond the plan's checklist

1. **`internal/biome/biome_anchor.go`'s `FloorHeightNormalUnderLevel`** (the
   function every one of the three subsystems' floor-height sampling funnels
   through — `BiomeWaterCentroidAndFloor`, `BiomeSurfaceAt`) had its own
   `InFrame()` skip, independent of the three gates above, with the comment
   "not in world space; biome anchoring is a world query". Left alone, it
   would have meant a frame-owned chunk's own biome sector could never find
   its own floor even after every render-side gate was lifted — it would
   always fall through to "open ground, height 0, no sector", flattening any
   vehicle-interior biome paint to a dead plane at y=0. Removed the gate.
   This function has a real, separate, pre-existing correctness gap (it
   tests `(wx,wz)` against every chunk's `SectorPoints` without first
   carrying it into that candidate chunk's own local space via
   `WorldToLocal`, unlike `FindChunk`/`FindChunkAndSector` in `spatial.go` —
   every caller happens to pass coordinates local to the *same* chunk being
   queried, which resolves correctly by construction for that one chunk, but
   two chunks whose local coordinate ranges happen to overlap could collide)
   — documented in a comment at the function rather than fixed, since fixing
   it properly means threading an owning-chunk parameter through
   `BiomeWaterCentroidAndFloor`/`BiomeSurfaceAt` and both their render-side
   callers, a larger, separate piece of work. Not hit by anything in this
   repo today (every chunk's transform is identity); removing the `InFrame()`
   skip makes it *more* likely to matter for frame-owned chunks specifically,
   since distinct vehicle interiors conventionally sit near local `(0,0,0)`
   and so are more likely to share overlapping local ranges than open-world
   chunks are. Worth a follow-up if vehicle-interior biome paint ships for
   real.
2. **`internal/editor/editor_3d_camera.go`'s `editorSelectionBounds`** (camera
   "frame selection" command) had the same chunk-local-vs-world mixup for its
   water/sector/chunk-fallback branches — introduced by Phase 1 turning
   `Sector.MinBound`/`MaxBound`/`Chunk.MinBound`/`MaxBound` chunk-local, but
   never caught because this file wasn't part of Phase 3's file list. Fixed
   the water branch only (`WorldAABB()` → `WorldAABB(ch)`, required anyway by
   the signature change above); the sector and chunk-fallback branches
   *below* it in the same function have the identical bug (returning
   chunk-local bounds where the function's own doc comment promises world
   ones) but are outside this phase's stated scope, so left as found — same
   fix shape (`transformAABB(ch.ModelMatrix(), ...)`) if it's worth doing.

### Confirmed: no editor-side restriction ever existed

The original plan's "open question" — whether the editor refuses to let you
paint biome/detail on a frame-owned chunk — is resolved: `grep -rn "InFrame"
internal/editor/*.go` (excluding tests) returns nothing at all, in this
revision or before it. Every `InFrame()` check that ever gated biome/detail
lived in the *renderer* (now removed); the "editor refuses to paint one"
comments that used to justify each `InFrame()` gate as low-risk were
describing a restriction that never existed. Lifting the render-side gates
is therefore the complete fix — there is no matching editor-side change to
make.

### Verifying the GLSL: a real GL context turned out to be available here

The original plan's own verification-status note (preserved below) assumed
"this sandbox has no display server" — true for whatever environment ran
Phases 1–3, **not true here**: `FOUNDRY_GL_TEST=1 go test ./internal/render/...`
actually stands up a real hidden GL 4.1 context via SDL and compiles/links
every shader touched in this phase (previously only exercised for
`vertex.glsl`/`model_vert.glsl`/`shadow_vert.glsl` and friends — nothing
here compiled the tessellation or biome/ground programs before). One
pre-existing wrinkle, **since fixed**: `biomeShaderPath`/the hardcoded
`"resources/shaders/…"` paths in the opt-in GL tests resolved relative to
`fileutil.ExecutablePath`/the process CWD, which for `go test ./internal/render`
is the package directory (`internal/render`), not the repo root — so these
tests needed either a `resources` symlink inside `internal/render` or to be run
with that as CWD. All three now set `fileutil.ExecutablePath` from
`testenv.RepoRoot(t)`, which walks up to the go.mod and is what that helper
exists for, so `FOUNDRY_GL_TEST=1 go test ./internal/render/` passes from
anywhere with no symlink.

Added `internal/render/detail_biome_shader_gl_test.go`
(`TestDetailBiomeGroundShadersCompile`), mirroring
`model_shader_gl_test.go`/`shadow_shader_gl_test.go`'s pattern: calls the real
`DetailInit()`/`GrassInit()` (which also calls `GroundInit()`) and asserts
every program's `model` uniform actually resolves (not -1) — this is new
coverage; nothing previously compiled the tessellation-stage programs at all.
Confirmed passing, with the symlink workaround above, before writing this up.

### Phase 4 follow-ups from user testing

Two things surfaced when the user actually drove the editor with a
multi-chunk, transformed level. Both were latent **Phase 1** regressions
(chunk-local geometry arriving without the frame conversions to match), not
Phase 4 changes — but both only became reachable once chunks could really be
moved, so they belong here.

**1. The 2D map now anchors to the active chunk instead of to the world.**

Symptom, in the user's words: *"The local origin of the chunk should be the
origin in the 2D editor, I should not drag a chunk's origin around and see
the 2d layout change in the 2d editor."* Phase 3 had made the 2D map draw in
world space (`wp3 := ch.LocalToWorld`), so moving a chunk slid its whole
layout across the map — but the 2D map is where you *author a chunk's
layout*, and where you *place that chunk in the world* is a different job.

The fix is in the projection, not in what is drawn: `editor2DOrtho()` now
composes the inverse of the active chunk's `ModelMatrix` (`editor2DAnchor`),
so the map's plane **is** that chunk's local space — its local origin at the
map origin, its local axes as the map axes, and Origin/Rotation/Scale
invisible to the layout. Everything queued into the gizmo line renderer, and
every coordinate through `worldToScreen`/`screenToWorld`, still speaks true
**world** space, which is what the hit tests, the 2D gizmo, the spawn point,
the entities, the lights and the whole 3D editor already speak — so none of
them needed touching. Only four things did: the two `worldToScreen`/
`screenToWorld` wrappers (which now convert through the anchor),
`screenToWorldSize` (multiplies by the anchor's scale, so it keeps returning
a *world* distance and `chunkLocalSize(ch, screenToWorldSize(px))` still
lands on a pixel-accurate local radius), `drawEditorGrid` (gained a
plane→world parameter, since it is the one thing described in plane
coordinates — the surface/UV view passes nil), and two `v.worldToScreen`
calls in `pickGizmo2DHandle` that bypassed the wrapper.

Covered by `internal/editor/editor_2d_anchor_test.go`: layout invariance
under Origin, and under Rotation+Scale; the chunk's local origin landing on
the map origin; `screenToWorld`/`worldToScreen` round-tripping through the
anchor; clicks resolving to the chunk-local point actually drawn there; and a
pixel click radius staying pixel-sized across chunk scales. All six were
confirmed to fail with the anchor stubbed back to the identity before being
kept.

**2. Editing a sector point on a second chunk no longer melts the frame rate.**

Symptom: *"moving a sector point in a secondary chunk causes the engine to lag
like crazy."* Root cause was the same mismatched-frame comparison as
everywhere else in this effort, in `MarkBiomesDirtyOverBox`
(`internal/world/level_resync.go`): the edited box arrives in the *edited*
chunk's local space, each biome sector's `Bounds()` is in *its own* chunk's
local space, and the two were compared raw. Because chunks are all authored
around their own local origin, nearly every painted sector in the level then
looked like it overlapped nearly every edit — so a vertex drag marked all of
them dirty on **every mouse-move**, and each one rebuilt its height texture
and ground patch on the next frame.

The cost of one such spurious rebuild, measured: **~15 ms and ~436,000
allocations** for a single modest biome sector, because `ensureBiomeHeight`
resolves every texel's floor through `FloorHeightNormalUnderLevel` →
`FindSector`, and `FindSector` called `SectorPoints`, which **allocates a
fresh polygon slice per sector per call**.

Two fixes, both kept:
- `MarkBiomesDirtyOverBox` now refits both boxes into world space before
  testing (`ForEachBiomeSector` gained an `owner *Chunk` callback parameter to
  make that possible). Cross-chunk draping still marks correctly — that is a
  real thing, since `FloorHeightNormalUnderLevel` samples every chunk — it is
  only the false positives that stop. `CarryBiomesWithPoints`
  (`internal/biome/biome_anchor.go`) had the *identical* mismatch and was
  silently warping paint on other chunks by edits nowhere near it in the
  world; it now converts through `crossChunkPointMap`, which collapses to the
  identity for the ordinary same-chunk case.
- `world.PointInSector` (`internal/world/sector.go`) is a new allocation-free
  restatement of `SectorPoints` + `geom.PointInPolygon`, identical in
  semantics down to how it treats an out-of-range point index.
  `FindSector`/`FindChunkAndSector`/`FindChunkAndSectorSH` use it. The same
  sweep drops to **~7 ms and zero allocations** — and this one also helps
  every per-frame collision lookup, not just the editor.

Covered by `internal/world/level_resync_test.go` (far-away chunk not dirtied;
genuinely overlapping chunk still dirtied; own-chunk edit still dirtied;
rotation+scale honored) and `internal/world/sector_pip_test.go`
(`PointInSector` agreeing with the pair it replaced across 25k randomized
points including a concave ring and out-of-range indices, plus an
allocation-count assertion).

**3. Biome paint now drapes on its own chunk's floor, not on a chunk hanging
above it.**

Symptom: *"I have a floating chunk and the water in the chunk below it is
being removed underneath the floating chunk."*

`FloorHeightNormalUnderLevel` — the single floor sample every biome subsystem
funnels through (the water plane's reference height, the lakebed/ground patch
per vertex, the scatter height texture per texel) — took **the first chunk in
the level whose sector contained the XZ point**, with no notion of which floor
was actually relevant. A platform chunk floating over a lake overlaps it in
XZ, so depending on nothing but level-file order it could win the sample
outright. The lakebed patch under the platform was then built at the
platform's floor height and poked straight up through the water surface: the
water looked deleted exactly where the platform covered it. Reproduced
headlessly before fixing — a platform at height 25 over a lake at 0 returned
`25` for the floor, and a lakebed vertex at `24.99`.

The fix threads the **owning chunk** (the chunk the biome sector was painted
on) down through `BiomeSurfaceAt` / `BiomeWaterCentroidAndFloor` /
`BuildGroundPatchMesh` / `ensureBiomeHeight` / `renderBiomeSector` into
`FloorHeightNormalUnderLevel`, which now tests the owner's own sectors first.
Paint drapes over the ground it was painted on; a chunk in the air above it is
not that ground. Every call site already had the owning chunk to hand, so this
was plumbing rather than redesign.

The same change closes the coordinate-space gap this document flagged as
deliberately-unfixed two sections above: the owner needs **no** conversion
(its geometry is already in the frame `(wx,wz)` is expressed in, which is also
why testing it first is the cheap path), and every *other* candidate now has
the point carried into its own local frame — and its answering height carried
back into the owner's — instead of being compared raw. Cross-chunk draping
still works (paint spilling past its own chunk's floors onto a neighbour's is
a real, supported thing) and is covered by its own test.

Covered by `internal/render/biome_floating_chunk_test.go`: the floor under the
platform resolving to the ground in **both** chunk orders; the water plane's
centroid reference; a sweep of the whole lakebed including the covered part;
the built ground-patch mesh end to end; cross-chunk fallback still finding a
neighbour's floor; and the fallback honoring a far-away candidate's transform.
Five of the six were confirmed to fail against the old first-chunk-wins
behavior before being kept (the sixth, the fallback, is unchanged by design).

**Later superseded:** the fallback's first-match order-dependence, left open
here, was resolved together with the warp gate — see item 7.

**4. The 3D view's chunk box, camera framing and occlusion now use world
bounds.**

Symptom: *"The blue box showing the chunk position is not moving with the chunk
when I move it in the 3D editor and it appears the visual location is different
from the actual chunk origin's location."*

`Chunk.MinBound/MaxBound` and `Sector.MinBound/MaxBound` became chunk-local in
Phase 1. Three places in the 3D view kept treating them as world:

- `Editor3DRenderGizmos` drew the selected-chunk highlight with
  `gizmo.GizmoAABB(ch.MinBound, ch.MaxBound, cyan)` — so the outline sat at
  the world origin no matter where the chunk was moved to. This is the
  reported bug. It now draws an **oriented** box via new `chunkWorldOBB(ch)` +
  `gizmo.GizmoOBB`: a refitted world AABB of a rotated chunk is looser than
  the chunk itself and would visibly float off the geometry it outlines. The
  basis is read out of `ModelMatrix`'s columns rather than rebuilt from
  `Rotation`/`Scale`, which keeps it correct for a frame-owned chunk whose
  matrix also carries the vehicle's orientation.
- `editorSelectionBounds` (`editor_3d_camera.go`) — the sector and
  whole-chunk branches this document already flagged two sections above as
  "left as found". They returned chunk-local bounds from a function whose
  contract is world-space, so "frame selection" flew the camera to where the
  chunk would be if it had never been moved. Now `s.WorldAABB(ch)` and
  `ch.WorldBounds()`.
- `editorPointOccluded` (`editor_3d_visibility.go`) — the vertex-handle
  occlusion test walked every chunk testing a **world** ray against
  chunk-local bounds *and* chunk-local vertex buffers. For a moved chunk that
  occludes from where it used to be: handles offered behind walls, handles
  hidden in plain sight. Both the bounds rejects and the triangle tests now go
  through the chunk's `ModelMatrix`. Notably `modelGeometryBlocks` — the
  model-space triangle test this needed — already existed in that file and was
  **dead code**, never called by anything; it is now wired up, with a matching
  `modelGeometriesBlock` for the extrusion slices, and the world-space
  `geometriesBlock` it replaced was removed rather than left as a second
  unused helper.

One nearby thing checked and deliberately **not** changed:
`editor2DFrameActiveChunk` (`editor_2d_window.go`) pans the 2D map using
chunk-local bounds, which is correct *because* of fix 1 above — `Pan2D` is a
position on the map's plane and that plane is the active chunk's local space.
Converting it to world would pan the view off the chunk by exactly its Origin.
Commented in place so it does not get "fixed" later.

Covered by `internal/editor/editor_3d_worldbounds_test.go`: the box following
a moved chunk, rotating with a rotated one, and scaling with a scaled one;
selection framing being world-space for both the sector and whole-chunk
branches; and the occlusion bounds reject finding a moved chunk at its real
position while explicitly *not* matching its stale local bounds. Four of the
five were confirmed to fail against the pre-fix code.

**5. Editing a chunk that floats above painted ground dragged that paint
around (and re-baked it every mouse-move).**

Symptom: *"I have a chunk floating above a grass biome layer in another chunk,
however when I edit one of the points of the floating chunk it starts
expanding the biome layer underneath it to areas that are not part of the
biome layer bounds and it causes a ton of lag."*

`CarryBiomesWithPoints` re-fits every painted polygon whose vertices sat
inside an edited sector, using mean-value coordinates, so paint follows the
floor it is draped over when that floor is reshaped. The containment test was
purely XZ — it never asked whether the paint was draped on *that* floor. A
platform floating above painted ground overlaps it in XZ, so dragging one of
the platform's corners hauled the grass vertices with it, expanding the
polygon far outside its own bounds (reproduced: a corner at (10,10) pulled to
(-7.7,-7.7)). Each warp then set HeightDirty/GroundDirty, so the enlarged
polygon's height texture was re-baked on **every mouse-move** — and it grows
as the polygon grows, up to the 2048x2048 cap, each texel re-scanning the
level. That is the "ton of lag", and it compounds as the drag continues.

Pre-existing rather than introduced by the chunk-local work: when biome points
and chunk points were both world-space the same XZ overlap matched just as
wrongly. It needed a *floating* chunk to be visible, which is why nothing had
hit it.

Fix: a vertex follows only the floor it is actually draped on, resolved the
same way `FloorHeightNormalUnderLevel` resolves it — the owning chunk first.
Paint belonging to another chunk is warped by this edit only where it
genuinely spills off its own chunk's floors. Costs one `FindSector` on the
owner, and only for foreign paint.

Covered by `internal/biome/biome_anchor_drape_test.go`: the floating platform
leaving the grass alone (and not dirtying it); paint on the edited chunk still
following its own floor; and paint spilling onto a neighbour still following
that neighbour, with the corner over its own chunk left alone. The first was
confirmed to fail against the ungated code while the other two pass either
way, so the gate is precise rather than just restrictive.

**Later superseded:** the chunk-granularity residual noted here — two stacked
sectors of ONE chunk being indistinguishable — was resolved together with the
floor lookup's own order-dependence. See item 7.

**6. Systematic audit of the whole local/world bug class.**

After four separate user-reported bugs traced to the same root — Phase 1 made
geometry chunk-local, and consumers written when local *was* world were never
all audited — the remaining surface was swept deliberately rather than waiting
for the next symptom. Four parallel passes: `internal/script`+`internal/flang`,
`internal/world`, `internal/editor`, and
`internal/render`+`app`+`levelio`+`biome`. What follows is what they turned up
and what was done about it.

**Fixed — regressions introduced by fix 1 (the 2D anchor) in this same
session.** Making the map's plane the active chunk's local space silently
changed the meaning of `Editor.Pan2D`, and four places still treated it as
world: `editor_panel_shared.go`'s "Switch to Sector Editor" (panned by the
chunk's Origin), `editor_2d_window.go`'s open-the-map and `editor.go`'s
play→edit transition (both wrote a world camera XZ straight in), and
`createChunkAtCamera`'s 2D branch (placed a new chunk's world `Origin` from a
plane-space pan). A reminder that changing what a field *means* is a
cross-cutting edit even when the type does not change.

**Fixed — a moved chunk fell out of every spatial query.** `BuildSpatialHash`
now keys on world cells derived from `ch.ModelMatrix`, but nothing rebuilt it
when a chunk's transform changed — and the editor writes those on every frame
of a gizmo drag plus three numeric panel fields. The chunk became invisible to
collision, entity location and the script location verbs until some unrelated
edit happened to call `RebuildIndexes`. Structurally impossible before, since
`Origin` was consumed at load and never moved again. New
`world.RebuildSpatialHash(lvl)` (measured at ~40us on a 56-sector level, so it
just runs inline) plus `editorChunkTransformChanged()` at all five write sites.

**Fixed — `ModelMatrix` could return the zero matrix and spread NaN.**
`Chunk.Xform`'s zero value is the ZERO matrix, not the identity. Phase 1
removed the old zero-value short-circuit, so a frame-owned chunk whose
`BuildFrames` failed composed against a singular matrix: `LocalToWorld`
collapsed every vertex to the origin and `WorldToLocal` inverted a singular
matrix and returned NaN. `RebuildIndexes` leaves exactly that state on its
error path, and three editor call sites discard that error. Now treated as
"not yet refreshed" and falls back to the chunk's own placement.

**Fixed — script positions.** `refPosition` reported a sector's chunk-local
centre and an entity's frame-local position as world, and `distance` subtracts
those from genuine world positions. The entity `position` property and
`move_to` had the same frame-space mismatch against their own documented
"world-space" contract. All now convert; identity for unframed entities and
unmoved chunks. Chunk `origin`/`rotation` prop docs no longer claim world
space (they are within the chunk's frame). `floor/ceiling.height` was left
chunk-local *deliberately* — see the open question below.

**Fixed — the colour pass culled sectors against a tighter box than the shadow
and detail passes.** `app.go` hand-built a `Floor.Height..Ceiling.Height` box
while `shadow.go` and `detail_render.go` were both moved to
`LocalAABB`+`TransformAABB` in this changeset. `LocalAABB` exists precisely to
widen that by vertex offsets, terrain points, extrusions and wall tops, so a
sector with a deep extrusion could have its shadow drawn while the sector
itself was culled. Now `sector.WorldAABB(chunk)`.

**Fixed — two translate-gizmo space errors.** The axis-drag fast path was
gated on `ch.Rotation == 0`, which is not the same as "a world delta is a
local delta": a scaled chunk moved at the wrong rate, and a frame-owned chunk
can be rotated in the world by its vehicle with its own `Rotation` still 0.
Gate is now `ModelMatrix() == Ident4()`. The vertical drag added a world Y
delta straight to chunk-local `VertexOffsets`, same rate error.
`gizmoSnapOffset`'s comment claimed it snapped to the world grid; it snaps to
the chunk's, which is the right grid for an authored vertex — the comment was
corrected rather than the behaviour.

**Fixed — smaller items.** `Chunk.Clone` dropped `FrameOwner`, so duplicating
a vehicle interior produced a world chunk whose geometry, still authored
around the ship's origin, was read as world geometry at (0,0,0);
`duplicateActiveChunk` now also registers the copy in the vehicle's interior
list and rebuilds indexes. `biomeScatterBounds` clipped a world-space box with
a ring radius the shader spends in local units, so at `Scale > 1` the shadow
passes dropped sectors whose scatter genuinely reached the cascade. The
transparent sort key was `model * g.Position` while every caller's model
matrix already contains that translation — water, entity attachments and the
spawn marker all sorted at twice their offset (pre-existing, unrelated to
chunk space). `Geometry.Position` is now read by nothing and is documented as
such; `Geometry`'s type comment no longer claims "all geometry is stored in
world space; model matrix is always identity".

**Fixed — doc comments that would mislead the next reader.**
`InterpolateSurfaceHeight`, `FindSector`, `BiomeSector.Bounds`,
`DetailSurfaceAt` and `Geometry` all now state which space they work in. Two
of the bugs above were introduced by trusting exactly such a comment.

**Deliberately NOT fixed — needs a display to verify.** Three shader-side
issues were identified and left alone because they are visual and this
environment can compile shaders but not look at them:
- `vertex.glsl` seeds the water wave phase from world XZ and displaces along
  world +Y. Correct for a world chunk; on a vehicle chunk (newly possible
  since Phase 4 lifted the `InFrame` gate) the pattern stays nailed to the
  world while the pool slides through it, and crests rise vertically rather
  than normal to a tilted plane. The minimal correct fix needs `chunkModel`
  passed separately from the water's own matrix, so the phase can be seeded in
  chunk space — seeding from the raw mesh-local position instead would shift
  the pattern on *every* existing water surface.
- `biome_frag.glsl:75` seeds rock/moss bump noise from `vWorldPos` while the
  instance is placed chunk-locally, so the crevice pattern swims across a rock
  riding a moving chunk. Wants a local varying.
- The grass LOD split distances have the same local-vs-world unit question the
  cull ring did; only the cull ring was fixed.

**Resolved — `floor/ceiling.height` keeps its local meaning and gained a
world-space companion.** `height` stays the authored chunk-LOCAL Y: get/set
are symmetric so `height = height + 1` round-trips exactly, and it matches the
number the editor panel shows. The footgun was that comparing it against
`player().prop.position.y` mixes spaces, and that is the natural thing to
write — so `world_height` was added alongside it (read-only; `height` remains
the single writable height, so a script cannot set the two against each
other). It is sampled at the sector's own centre rather than at the chunk's
local origin: for an ordinary chunk the two agree (Y-only rotation and uniform
scale mean world Y is `origin.Y + height*scale` wherever you sample), but a
chunk on a tilted frame has no single world Y for a whole surface and its
centre is the honest answer — the same point `refPosition` reports as the
sector's position. Both floor and ceiling get it from the one `surfaceProps`
table.

**Also flagged, not acted on.** "Snap selection to grid" snaps vertices on the
chunk's grid but the spawn point, entities and volumes on the world grid —
one menu command meaning two things. Defensible either way; left as found.
And a biome polygon that spills onto a *different* chunk keeps a stale baked
height if that other chunk later moves (cache staleness, not a space error).

**7. One definition of "what is this paint draped on", used by everything that
asks.**

The two residuals left by items 3 and 5 above turned out to be the same
question asked in two places, so they were fixed together:

- the floor/height sample resolved the owner chunk first but fell back to the
  *first* other chunk that matched — level-file order, not anything meaningful;
- the warp gate distinguished only by owning *chunk*, so two stacked sectors of
  one chunk (a mezzanine over a floor) could not be told apart.

Both now go through `biome.DrapeTarget(level, owner, wx, wz) (*Chunk,
*Sector)`, which is the single definition. Its rule, in order:

1. **The owning chunk wins.** Paint drapes on the ground it was painted on. A
   chunk hanging above it is not that ground — and it matters in the other
   direction too: paint on a bridge stays on the bridge instead of dropping to
   the terrain below, which is why owner-preference has to outrank the height
   tiebreak rather than the reverse.
2. **Among overlapping candidates, the lowest floor wins.** Sectors only
   compete when they genuinely overlap in XZ — a point inside exactly one
   sector resolves to it regardless — so this decides *only* the stacked cases,
   and there the lower surface is the ground while the upper one is a structure
   over it. Applied uniformly within a chunk and across chunks (comparing
   **world** floor heights across chunks, since each one's own `Floor.Height`
   is in its own frame and those are not comparable numbers).

Because both consumers now share it they cannot disagree about a given point,
which was the deeper problem: a platform could reshape paint while the height
sample for that same paint came from somewhere else entirely.

**The one subtlety worth knowing.** `CarryBiomesWithPoints` runs *after* its
caller has written the moved points, but the paint it is deciding about is
still where the pre-edit floor put it. Resolving against live geometry asks
"which sector contains this vertex now" when the question is "which sector was
it draped on" — and for a whole-sector move the answer is "none", which
silently stopped paint following its own floor (caught by the existing
`TestBiomeFollowsWholeSectorMove`). So the resolution is parameterised by its
containment test: `DrapeTarget` uses live geometry, and the warp gate passes
one that consults each edited sector's recorded pre-edit shape. Only warped
sectors take that path; everything else stays on the allocation-free
`PointInSector`, which matters because this runs per biome vertex per
mouse-move.

Covered by `internal/biome/biome_anchor_drape_test.go` (mezzanine above paint
leaves it alone; the ground floor's own edit still carries it; plus the
floating-chunk, same-chunk and cross-chunk-spill cases from item 5) and
`internal/render/biome_floating_chunk_test.go` (stacked sectors resolving to
the lower one in **both** storage orders; no change where sectors do not
overlap; the cross-chunk fallback preferring the lowest world floor over the
first listed). The mezzanine test was confirmed to fail against the previous
chunk-granularity gate while the other four pass either way — the gate is
precise, not merely stricter.

**8. The paint now records which surface it was drawn over.**

The heuristic above still had one case it could not get right — grass on a
raised terrace with a basement directly beneath it, where "lowest wins"
chooses the basement. That is not a tuning problem: two sectors stacked at the
same XZ look identical from above, and above is the only view the paint was
ever drawn in, so the information simply is not in the geometry. It is now
recorded instead.

`BiomeSector.DrapeSector` holds the ID of the structural sector the polygon
was drawn over, persisted as `drape_sector` in the chunk's YAML (omitted when
unbound, so a level written before this round-trips byte-identical). Sector IDs
are unique level-wide (identity.go), so no chunk ID is needed alongside it.

`DrapeTarget`'s rule 2 becomes "the recorded surface wins", with the height
tiebreak demoted to rule 3 — the fallback for paint that is unbound (written
earlier) or bound to a sector since deleted. Both degrade to exactly the
previous behaviour, so this is a refinement rather than a replacement. When
the bound sector is not itself a candidate at some point — a polygon spanning
off it onto a neighbouring surface — the candidate whose floor sits NEAREST the
bound one's is taken, which keeps a span on the level it belongs to rather
than dropping it to whatever is lowest.

The binding is captured in `finishBiomeSectorDraw`: the **selected sector**
wins when the polygon's centre lies on it, because on stacked geometry that
selection is the user's only statement of which level they meant; otherwise
the centre resolves the ordinary way, so painting with nothing selected still
binds sensibly. Empty when the centre is over open ground, which is a
legitimate state rather than a failure.

Two places had to follow it. `Chunk.Clone` renames every sector it copies, so
the binding is remapped through the same `sectorIDRemap` the portal targets
use — left alone it would name the ORIGINAL chunk's surface, a different
height in a different place; a binding to another chunk's sector (spilled
paint) is not in that map and is left as it is. And the warp gate reads the
same field, which is the whole point of one resolver: paint bound to the
terrace follows the terrace's edit and ignores the basement's, the exact
reverse of what the unbound heuristic does.

Covered by `internal/render/biome_floating_chunk_test.go` (bound paint
resolving to its recorded surface, both ways round; unbound paint still
falling back to lowest; a *stale* binding degrading to the fallback rather
than resolving to nothing; a bound span landing on the neighbouring surface
nearest its own level), `internal/levelio/chunk_transform_test.go` (the field
surviving save/load, and staying absent when unbound),
`internal/world/spatial_invalidation_test.go` (Clone remapping it, and leaving
a foreign binding alone) and `internal/biome/biome_anchor_drape_test.go` (the
warp gate honouring it in both directions).

**9. The remaining drag lag: the dirty gate, and the triangulation behind it.**

Reported after 5 and 7 landed: dragging a point on the floating chunk was
still "unbelievably laggy". Two causes, both measured rather than guessed.

**The dirty gate was the same bug in a second place.** Item 5 fixed the *warp*
gate — which paint gets reshaped — but `MarkBiomesDirtyOverBox` was still a
pure XZ overlap test. A platform directly above painted ground overlaps it
exactly, so the paint was marked dirty on every mouse-move and rebuilt on
every frame. Renamed to `MarkBiomesDirtyOverSector`, which now takes the
sector being edited and applies the same drape test the other two callers use
(`paintDrapesOn`). Its soundness argument is written out at the function: an
edit to s can matter three ways — the paint is BOUND to s, some vertex
currently resolves to s (post-edit, which catches s having just moved under
it), or some vertex is over open ground. The case that looks missing (paint
that was on s, is not bound to it, and has since resolved elsewhere) cannot
arise, because unbound paint resolves by "lowest floor wins" and so was never
on the upper of two stacked surfaces to begin with.

**The rebuild itself was far too expensive even when legitimate.** Profiling
one rebuild of a modest painted polygon: **134ms and 44MB allocated**, with
**~90% inside `geom.DelaunayTriangulate`**. The patch was re-triangulated from
scratch every time — but "dirty" almost always means the FLOOR moved, and the
triangulation depends only on `s.Points`. New
`biome.BiomeMeshTriangulation` caches the point cloud and its triangulation,
keyed on a hash of the polygon's own contents, so it is self-invalidating:
any edit to the points changes the key, and there is no dirty flag to keep in
step. Both consumers share it — the ground patch and the water plane mesh the
same polygon. Measured after: **2.2ms and 2.2MB**, a 60x improvement, which
matters independently of the gate because dragging the ground floor *under*
paint is a legitimate rebuild that paid the same 134ms.

Covered by `internal/world/level_resync_test.go` (a floating chunk above the
paint no longer dirties it; paint genuinely spilled onto another chunk's floor
still does; the far-away-chunk and own-chunk cases unchanged) and
`internal/biome/biome_mesh_cache_test.go` (an unchanged polygon reusing the
exact slices; a moved vertex, an added vertex and an ID reused with a
different shape all rebuilding; two sectors not sharing an entry; and a
degenerate polygon not caching an empty mesh that would then be returned
forever once it became valid).

Note one test premise had to be corrected rather than the code: a fixture
asserting "editing a chunk sitting exactly over the paint must dirty it" was
written for the old chunk-level logic. With owner-first drape resolution, paint
on chunk A over A's own floor does *not* rest on a coincident floor in chunk B,
so not dirtying is right. It was replaced with a real cross-chunk-spill
fixture, where the painted chunk has no floor of its own.

**Nothing is left open in this area.**

**Still outstanding on this path:** a *legitimate* rebuild — dragging a vertex
directly under painted ground — still costs that ~7 ms per affected sector per
mouse-move, because the height texture is rebuilt from scratch and every texel
re-scans the level. The obvious next lever is an AABB pre-reject in
`FindSector` using `Sector.MinBound/MaxBound`; it was deliberately **not**
done here because those bounds are only ever written by `Sector.Load`, so a
sector built by any path that skips Load (several tests do) would silently
stop matching. Making the bounds an invariant first, then rejecting on them,
is the safe order.

### Files changed, Phase 4

```
internal/app/app.go                              (water draw composition)
internal/biome/biome_anchor.go                   (InFrame lift + doc fixes)
internal/biome/biome_water.go                    (InFrame lift)
internal/editor/editor_3d_camera.go              (water framing fix)
internal/editor/editor_3d_face_select.go         (stale comment removed)
internal/editor/editor_3d_pick.go                (water pick composition)
internal/render/biome_ground.go                  (model uniform + InFrame lift)
internal/render/detail_render.go                 (model uniform + cull fix + InFrame lift)
internal/render/grass.go                         (model/camXZ uniform + cull fix + InFrame lift)
internal/world/biome_sector.go                   (doc fix)
internal/world/chunk.go                          (RecomputeBounds fix)
internal/world/water_surface.go                  (LocalAABB/WorldAABB split)

# follow-ups from user testing (see the section above)
internal/editor/editor_2d.go                     (2D map anchored to the active chunk)
internal/editor/editor_2d_gizmo.go               (handle picking through the anchored wrapper)
internal/editor/editor_2d_window.go              (note: local bounds are correct here)
internal/editor/editor_surface2d.go              (drawEditorGrid's new parameter)
internal/editor/editor_3d.go                     (chunkWorldOBB; chunk highlight box)
internal/editor/editor_3d_camera.go              (selection framing in world space)
internal/editor/editor_3d_visibility.go          (occlusion through the chunk transform)
internal/biome/biome_anchor.go                   (cross-chunk warp fix; owner-first floor lookup)
internal/biome/biome_displace.go                 (BiomeSurfaceAt takes the owning chunk)
internal/biome/biome_water.go                    (water floor sample takes the owning chunk)
internal/render/biome_ground.go                  (ground patch takes the owning chunk)
internal/render/grass.go                         (height texture takes the owning chunk)
internal/world/level_resync.go                   (world-space + drape-aware biome-dirty test)
internal/world/drape.go                          (new — the one drape resolver)
internal/biome/biome_mesh_cache.go               (new — triangulation cache)
internal/world/sector.go                         (PointInSector, allocation-free)
internal/world/spatial.go                        (FindSector & friends use it)

resources/shaders/biome_cube_vert.glsl
resources/shaders/biome_scatter_common.glsl
resources/shaders/biome_shadow_vert.glsl
resources/shaders/biome_vert.glsl
resources/shaders/detail.tese
resources/shaders/detail_scatter_common.glsl
resources/shaders/detail_scatter_cube_vert.glsl
resources/shaders/detail_scatter_shadow_vert.glsl
resources/shaders/detail_scatter_vert.glsl
resources/shaders/detail_shadow.tese
resources/shaders/detail_shadow_cube.tese
resources/shaders/detail_vert.glsl
resources/shaders/ground_vert.glsl

# new/updated tests
internal/render/biome_light_test.go              (+TestBiomeScatterBoundsComposesChunkTransform)
internal/render/detail_render_test.go            (new)
internal/render/detail_biome_shader_gl_test.go   (new)
internal/world/water_test.go                     (WorldAABB → LocalAABB call fixups)
internal/editor/editor_2d_anchor_test.go         (new — 2D map anchoring)
internal/world/level_resync_test.go              (new — cross-chunk biome dirtying)
internal/world/sector_pip_test.go                (new — PointInSector equivalence)
internal/render/biome_floating_chunk_test.go     (new — floor sample prefers the owning chunk)
internal/editor/editor_3d_worldbounds_test.go    (new — 3D box/framing/occlusion in world space)
internal/script/script_spaces_test.go            (new — script positions/world_height)
internal/world/spatial_invalidation_test.go      (new — hash rebuild, zero-Xform guard, Clone)
internal/biome/biome_anchor_drape_test.go        (new — paint follows only the floor it drapes on)
internal/render/biome_floating_chunk_test.go     (+drape resolution; the recorded binding)
internal/biome/biome_mesh_cache_test.go          (new — the triangulation cache's key)
internal/levelio/chunk_transform_test.go         (+drape binding save/load round trip)
internal/biome/biome_displace_test.go            (owning-chunk argument)
internal/render/biome_displace_surface_test.go   (owning-chunk argument)
internal/render/biome_ground_water_test.go       (owning-chunk argument)
internal/editor/world_biome_test.go              (owning-chunk argument)
```

---

## Verification status

`go build ./...`, `go vet ./...`, `gofmt -l .`, and the full test suite are
clean as of the end of Phase 4, with the same one pre-existing, unrelated
failure noted since Phase 3 (`TestShippedButtonScriptUsesItsParams` — a
missing shipped resource file, `resources/scripts/button.flang`, confirmed
failing on a clean `git stash` too, nothing to do with this work).

**Shader compilation was verified with a real GL context in this
environment** (`FOUNDRY_GL_TEST=1 go test ./internal/render/...` — see "Phase
4 — what actually happened" above): every program touched this phase
(detail's tessellation + scatter, biome scatter, ground patch) compiles and
links, and every new `model` uniform resolves. That was not possible in
whatever environment ran Phases 1–3 (this document used to say so here) —
it's worth re-checking `FOUNDRY_GL_TEST=1` at the start of a future session
in case that was environment-specific rather than a property of this
repo/sandbox generally.

**No live *visual* verification was possible even so** — GL context creation
worked, but there's still no windowing/compositor to actually launch the
editor and look at pixels, only to compile shaders offscreen. Every existing
chunk in the repo has `Origin=0/Rotation=0/Scale=1`, so `ModelMatrix()` is the
identity everywhere real content exists today — the risk this leaves is low
for the *Go-side* matrix composition (every changed code path collapses to
its old behavior for an identity matrix, and the test suite added across all
four phases checks exactly that on real geometry), but shader-side visual
correctness — does a moved+rotated chunk's grass/rock/detail actually land
in the right place and light correctly, does a scaled chunk's scatter density
read sensibly — has only been reasoned through by hand (see the biome-scatter
lattice write-up above) and compile-checked, not eyeballed. If a display/
compositor becomes available: move a chunk, rotate one, scale one, and add
biome paint + procedural detail + a water sector to it; then repeat with a
frame-owned (vehicle-interior) chunk, which has no existing content to
compare against at all.

## Files changed, Phases 1–3 (for reference)

```
internal/app/app.go
internal/collision/collision.go
internal/collision/entity_collision.go
internal/collision/frame_portal.go
internal/collision/player.go
internal/collision/player_test.go
internal/editor/biome_anchor_test.go
internal/editor/editor_2d.go
internal/editor/editor_2d_context_menu.go
internal/editor/editor_2d_objects.go
internal/editor/editor_2d_select.go
internal/editor/editor_3d.go
internal/editor/editor_3d_face_select.go
internal/editor/editor_3d_pick.go
internal/editor/editor_biome.go
internal/editor/editor_extrusion.go
internal/editor/editor_panel_shared.go
internal/editor/editor_rotate_gizmo.go
internal/editor/editor_scale_gizmo.go
internal/editor/editor_sel_transform.go
internal/editor/editor_translate_gizmo.go
internal/levelio/level_loader.go
internal/levelio/level_save.go
internal/render/detail_render.go
internal/render/shadow.go
internal/render/shadow_test.go
internal/script/script_props_geom.go
internal/world/chunk.go
internal/world/level_schema.go
internal/world/sector.go
internal/world/spatial.go

# new test files
internal/editor/editor_transform_test.go
internal/levelio/chunk_transform_test.go
internal/world/chunk_transform_test.go
internal/world/spatial_transform_test.go
```

None of this has been committed yet — it's sitting in the working tree.
