# Nanite Phase 3: compression, paging and streaming

Status: **complete**, with the gaps listed at the end. Nothing is committed;
Phase 3 is in the working tree on top of `8822251` (phase 2b). Another session's
uncommitted changes (setup, pack, release, `app.go`, `startup_release.go`,
`shadow.go`, `fragment.glsl`, `biome_light.glsl`, `docs/guide/20`) were in the
tree throughout. None of those files was edited here, and every run below
included them.

Phase 3 covers components 3 (compression and paging) and 9 (streaming).

Before this phase:

- A cooked mesh was one gob of float arrays. A load read all of it.
- Every placement's mesh went to the GPU whole, at load, as one buffer.

Now:

- **The cook quantises the mesh** onto a per-mesh integer grid and bit-packs it
  a cluster at a time, into fixed-size **pages**.
- **A load reads only the small part.** That is the clusters' bounds and
  errors, the LOD tree, the page table, the fallback mesh, the distance field
  and the hulls.
- **The renderer streams pages.** They go through a fixed GPU **page pool**
  within a memory budget. The GPU's own feedback picks what to load, and the
  coarser version above a missing page is drawn until it arrives.
- **Collision streams too.** A mesh-mode prop's collision cut is read from
  disk when a body comes near.

## What was built

### Quantisation and bit packing (`internal/nanite/quant.go`)

- **One grid per mesh.** Every position is an integer on it:
  `q = round(p / Step)`, with `Step` a power of two.
  - The grid is at least 2^18 steps across the mesh's largest extent. That is
    7.6 µm on the 2 m bench rock.
  - It is never finer than float32 can rebuild exactly: |q| < 2^23, so `q·Step`
    is an exact float32 on any machine.
  - Two clusters sharing a corner therefore share its integers, and the paged
    mesh is as crack-free as the float pool it came from.
- **UVs** sit on a grid of 2^-14 of a unit.
- **Normals** are octahedral, 12 bits a component, worst 0.06° (tested).
  - The decode converts every product before summing, so the compiler cannot
    fuse a multiply-add.
  - The same bits come out on every machine.
- **The cook quantises its welded pool before it does anything else.**
  - Every attribute is replaced by what its integers decode to, and the
    integers are kept.
  - Everything after works from exactly the floats the runtime will decode:
    the clusters, the DAG and its errors, the collision cut, the SDF.
  - The page writer packs the kept integers. It never re-quantises floats,
    since an octahedral normal is not always stable through a second round
    trip.
- **`bitWriter` / `bitReader`** pack fields least significant bit first, a
  cluster to a byte boundary. A short or damaged stream is an error, never a
  panic: a page is read from a file that may have changed under a running
  game.

### Pages (`internal/nanite/pages.go`) and the file (format 4, cook version 5)

**What a page holds.** A run of clusters, in cluster order, that decodes into
one slot of the pool: at most `PageMaxVertices` (4096) vertices and
`PageMaxTriangles` (4096) triangles.

- Each cluster stores its own distinct vertices (at most 128). They are packed
  relative to its lowest corner, at the bits its own extent needs per axis:
  15.1 bits per position axis on average on the bench rock (9 to 18), instead
  of 32.
- Triangles are local indices at the bits the cluster's vertex count needs.
- Decoded, a page is one vertex array plus page-relative indices. A run of a
  page's clusters is one contiguous index range, which is what the cull draws
  as one command.

**How pages are packed.** From the renderer's own RANGE WINDOWS. The rule
moved from `render` to `cluster.RangeWindows`, so the cook and the renderer
share it. Windows are packed whole, a new page started when the next would
not fit.

- **The first version packed by DAG group** (a group's input whole on one
  page). Page boundaries then fell inside windows, and the renderer had to
  split them: 187 windows instead of 167 on the bench rock. Every window's
  command slots cost on this GPU (Phase 2), so the fully resident rock field
  was about 3.5% slower.
- **With window packing:**
  - A window never straddles a page, except where a run of root leaves
    begins: roots are kept on pages of their own, one or two a mesh.
  - A group's input is split only where a page ends. Its refinement then
    needs both pages, which `Residency.PagesNeededWith` knows.

**Roots and parents.**

- **Root pages** hold only clusters no group consumed. They are always
  resident: the roots alone are a whole mesh.
- **`Page.Parents`** are the pages holding what a page's groups made. A page
  is of no use without them, and its eviction must not leave them stranded
  above it.

**The `.fnm` layout:**

| Part | Contents |
| --- | --- |
| header | `FNM\0`, version 4, source hash (40 bytes) |
| length | the metadata's length (8 bytes) |
| metadata | the Mesh minus its geometry, gob + flate |
| data | the packed pages, then the collision's mesh part (gob + flate) |

**Reading it:**

- `Read` loads the header and the metadata only, and keeps the path.
- `LoadPage` is one `ReadAt` and a decode. It is safe from any goroutine. It
  checks the header's hash first, so a file re-cooked under a running game is
  an error rather than garbage.
- `Decode` (from a reader) keeps the data section in memory.
- `Flat` decodes every page into the old single pool, for the classic path
  and the tools.
- **The cook builds the pages last** and replaces its in-memory pool with the
  pages decoded. It checks every corner bit for bit, so what a cook holds and
  what a load sees are the same floats.

**Other changes to the mesh:**

- **The fallback mesh** has its own small pool (`FallbackVertices` /
  `FallbackNormals`). A GPU placement's CPU-side submeshes (picking, the
  point-shadow signature) never need the pages.
- **`ClosedSolid`** is the cook's `Closed()`, kept for meshes whose geometry
  is on disk.

**Sizes on the bench rock** (81,920 triangles, 163,582 over all LOD levels):

| Measure | Value |
| --- | --- |
| Pages | 52, 2 of them root (8.4 KB packed) |
| Packed size | 1.8 MB, 32% of the decoded floats (5.4 MB) |
| `.fnm` file | 3.27 MB, against 3.46 MB for format 3 |
| Collision cut | now outside the metadata (above) |

### Residency (`internal/nanite/residency.go`)

The cut stays the Phase 2 per-cluster test. Streaming only changes the numbers
it reads.

**When a group may be refined.** A DAG group is REFINABLE when two things hold:

- every page holding a cluster it consumed is resident
- every group that consumed something it made is refinable too

The second rule is what stops a fine and a coarse version of one surface both
being drawn.

**How a group that cannot be refined is handled.** Its clusters' records get
error 0:

- The clusters it made always count as fine enough, so they are drawn.
- The clusters it consumed always see a parent fine enough, so they are never
  drawn. That is also why it does not matter that they are not loaded.

**Why the cut stays whole.** The effective errors still never fall going up.
So the per-cluster test, the tree walk and the crack-freedom argument hold for
any set of resident pages, and `cluster.RefreshErrors` recomputes the tree's
bounds.

**What to request.** A cluster drawn although its cooked own error is too
coarse asks for `RequestPage`, the page of what its group consumed.

### The page pool (`internal/render/nanite_stream.go`)

**The pool.** One vertex, normal and index buffer for every cooked mesh of the
level, in fixed slots, each holding one page.

- A cluster's record (`gpuCluster.Draw`) points at its page's slot:
  `firstIndex = slot·12288 + 3·offset` and `baseVertex = slot·4096`.
- A cluster whose page is not resident draws nothing and can never be in the
  cut.
- **Size:** as many slots as the level's pages, up to the budget. The budget
  is `gfx.NaniteStreamPoolMB`: 256 MB by default, saved to `graphics.yaml` as
  `nanite_stream_pool_mb`, set by scripts as
  `@level.prop.nanite_stream_pool_mb`, or overridden by
  `FOUNDRY_NANITE_STREAM_POOL_MB`.
- **A slot is 176 KB.**
- **Root pages are always resident.** If they plus a frame's worth of pages do
  not fit the budget, the pool grows past it and says so in the log.
- **Built at level install** (`render.SyncNaniteScenery`, from
  `app.installLevel`), behind the loading screen, then `glFinish`ed.
- **Preloaded** coarsest first while slots last.
- **Immutable storage.** The pool's stores are `glBufferStorage` (flags 0)
  wherever the driver has buffer storage. The reason is below ("The stall that
  immutable storage fixed").

**Feedback.** Each camera pass's cluster stage (`streamNote` in
`nanite_cluster_common.glsl`) writes four arrays per page:

- used: the frame number
- asked: the frame number
- what the use is worth
- what the ask is worth

The worths are the error, in 1/64ths of the threshold, that would show were
the page gone (a cluster's parent error projected), or that shows now (a
too-coarse cluster's cooked error projected). They use `atomicMax`. The frame
stamps are never cleared; the worths are cleared after each copy
(`glClearBufferSubData`).

- Shadow passes write nothing. Their cut is the camera's, and what they alone
  would ask for is off screen.
- When every page is resident (the level is under budget), no pass writes
  feedback at all.

**One frame of streaming**, once a frame before the first cull (the shadow
passes come first, so every pass of a frame sees one residency):

1. **Read the feedback.**
   - The feedback is copied into the next of three readback buffers and
     fenced.
   - Earlier copies are read whose fences have passed. The read is
     persistently mapped where buffer storage exists, else
     `glMapBufferRange` after the fence. It never waits.
   - Used pages, and every page above them, are stamped with their worth.
   - Asked pages, plus whatever their groups need with them and every page
     above them, are queued.
2. **Hand pages to the loaders.** Queued pages go, most worth first, to three
   **loader goroutines**. Each `ReadAt`s and decodes off the main thread.
   - At most 32 are in flight.
   - At most as many as there is room for: free slots plus evictable pages.
   - Requests not repeated for 30 frames are dropped.
3. **Take in what arrived**, at most `streamPagesPerFrame` (16; env
   `FOUNDRY_NANITE_STREAM_PAGES_PER_FRAME`) a frame. Each needs a slot:
   - a free one
   - else the least recently used page the newest feedback did not use
   - else (Nanite's priority trade) the page in use worth least, if the ask is
     worth twice as much

   It is never a root page, nor one the new page needs above it. The page goes
   up through the **staging ring**: three fenced 4 MB regions, a persistently
   mapped buffer where the driver has `ARB_buffer_storage` and else a plain
   buffer written by `glBufferSubData`, copied into place on the GPU. A region
   whose fence has not passed is not written. That frame takes in nothing
   rather than wait.
4. **Rewrite the records.** Every mesh whose residency changed gets its records
   worked out again: draw ranges, effective errors and the tree's errors.
   - Only the runs that differ from a CPU mirror of the GPU's copy go up,
     through the same ring. That is about 25 KB a page.
   - It happens before this frame's first cull, which is also why a slot can
     be refilled the frame its page is evicted.
   - A change too big for the ring is written directly and counted
     (`DirectWrites`, 0 in every run below).

**Supporting changes:**

- **Range tiles never straddle pages.** `rangeWindows` breaks at page changes;
  with window packing that happens only at root runs.
- **`naniteStreaming()`** needs LOD selection on, since the pool may hold less
  than the full detail.
- **The F3 overlay** gets a `Nanite pages:` line: resident of slots, what came
  and went this frame, queued, loading, starved.
- **The profiler** gets a `nanite: stream` section. A frame whose streaming
  took over 8 ms is logged (at most 20 times).
- **The bench** gets `fly=1`: one flight through the poses over the measured
  frames. It also reports frame p99 and a `nanite pages` line, with the peak
  update time reset at the end of warm-up.
- **`foundry nanite inspect`** prints the pages and the quantisation, and the
  collision cut's node count when it is paged out.
- **`foundry nanite sample`** takes `-seed` (different rocks).

### Collision paging (`internal/meshcollide/paged.go`, `internal/collision/scenery_index.go`)

**What is paged.** The mesh mode's part of the collision data: quantised
vertices, triangles, tags and BVH. The cook writes it after the pages.

- A loaded `Data` is **stripped**: those fields are empty, `Paged` is set, and
  the counts are kept.
- `WithMesh` reads the part back through the loader the reader attached, and
  validates it.
- Hulls stay with the metadata: at most 16 × 128 points, needed whenever
  anything moves near a hulls-mode prop.

**Proximity prefetch.** `prefetchNear` runs from `EntityUpdate`, and
`MovePlayer` runs it for the player. Every world-space body's box, grown by
16 m plus one second of its velocity, goes through the placement index.

- Each mesh-mode prop it reaches starts reading on a goroutine, if it is not
  resident or already loading.
- A prop stays resident while a body stays that near. Phase 2b's TTL drops it
  600 ticks after the last.
- **A query that reaches a mesh not yet resident** (a long ray, a teleport)
  waits for its load or reads it there and then. It is counted as a
  `SyncLoad`.
- **F3:** the overlay's Prop collision line adds "N loading, M waited for".
- **`WaitForCollisionLoads`** is there for headless tools.

**Other consumers:**

- **Navigation** reads the part for its rasteriser, off the main thread, and
  lets it go.
- **The editor's Show collision** keeps the selected prop's part while it
  stays selected.

### Other changes

- **`world/scenery.go`:** a GPU placement's submeshes use the fallback pool.
  The classic path (`NoNanite`, `FOUNDRY_NANITE_MESHES=0`) calls `Flat()`
  first. A GPU placement whose slot has no fallback triangles also calls
  `Flat()` before falling back to the full pool (added in verification: it
  sliced the never-decoded `Indices` before).
- **`glext`:** `glBufferStorage`, detected (GL 4.4 or `ARB_buffer_storage`),
  with `BufferStorageAvailable`, `DisableBufferStorage` and `Reinit`.
- **`cluster`:** `RefreshErrors`, `RangeWindows`, `WindowMaxLeaves` and
  `WindowMaxClusters`.
- **Docs:**
  - **Guide pages:** 09 (cook: quantisation, pages, re-cook to format 4); 14
    (new "Streaming and the memory budget" section; collision paging); 17 (the
    budget setting).
  - **SCRIPTING.md** (`nanite_stream_pool_mb`).

## The stall that immutable storage fixed

The spec's design is a staging ring copied into the pool with
`glCopyBufferSubData`. It measured badly on the GTX 1070's driver. In a fast
flight over budget, every frame that took pages in, and some after it, waited
30-150 ms:

- in the first fence query of the next frame (`glClientWaitSync` with a zero
  timeout, and `glGetSynciv`'s status query alike)
- or at the swap

The same stalls appeared with each of these changes:

- the 4.3 ring
- a ring in video memory (`STATIC_DRAW`)
- 2 pages a frame instead of 16

They did not appear when the page was written into the pool with
`glBufferSubData` directly.

**The explanation that fits:** the driver renames, that is copies whole, a
MUTABLE buffer that a copy writes into while frames still in flight read it.
The vertex buffer alone is 119 MB at 256 MB.

**Making the pool immutable** (`glBufferStorage`, flags 0) leaves it nothing to
rename, and the stalls went to zero. In the fast flight at 256 MB:

| Pool storage | p95 | p99 | max |
| --- | --- | --- | --- |
| mutable | 33.75 ms | 35.35 ms | 35.83 ms |
| immutable | 6.76 ms | 7.16 ms | 7.30 ms |

**Where the driver has no buffer storage** (a strict 4.3 driver), the pool is
mutable, and page data goes in with `glBufferSubData`, which measured
stall-free on the mutable pool. Records still go through the ring: their
buffers are small.

**A deviation from the spec:** the spec's fallback is "orphaned buffers". The
fallback ring here is fenced and never orphaned. Phase 1 found per-pass
orphaning hung Mesa D3D12 in `glBufferData`.

**Also found here:**

- The first fence queries after a level load waited 28-98 ms each while the
  driver absorbed the preload's uploads.
- Building the pool at level install with a `glFinish` moved that wait behind
  the loading screen.

## Flags and settings

| Flag / setting | Default | Effect |
| --- | --- | --- |
| `FOUNDRY_NANITE_STREAM` | on | The page pool. Off: every cooked mesh decoded whole (`Flat`) into one buffer at load, the Phase 2 pool structure. The vertex layout is the paged one either way: there is no welded pool any more. |
| `nanite_stream_pool_mb` (graphics.yaml) / `@level.prop.nanite_stream_pool_mb` | 256 | The budget, MB, 8-8192. Taken at the next level load. |
| `FOUNDRY_NANITE_STREAM_POOL_MB` | — | Overrides the budget (tests, bench caps). Not clamped. |
| `FOUNDRY_NANITE_STREAM_PRELOAD` | on | Off: start from the root pages alone. |
| `FOUNDRY_NANITE_PERSISTENT` | on | Off: the 4.3 ring and readbacks even where buffer storage exists. |
| `FOUNDRY_NANITE_STREAM_PAGES_PER_FRAME` | 16 | Pages taken in a frame at most. |
| Phase 1-2b flags | unchanged | `FOUNDRY_NANITE_LOD=0` also turns streaming off (the pool may not hold the full detail). |

## Tests

| Test | What it checks |
| --- | --- |
| `nanite/quant_test.go` (new) | Bit packing round-trips fields of 0-32 bits, cluster by cluster, and a read past the end fails. Positions snap within half a step, re-quantise to their own integer and decode exactly, including a small mesh far from its origin. Octahedral normals are within 0.1° (worst measured 0.06°), unit length, and the axes and a zero normal survive. |
| `nanitecook/paging_test.go` (new) | See the rows below. |
| `nanitecook/nanite_test.go` (updated) | The round trip compares the decoded pool bit for bit. The `.fmdl` match quantises the scenery side the same way, and stays exact. The fallback's closed test uses its own pool. The collision survives the file paged out and reads back whole. |
| `render/nanite_stream_gl_test.go` (GL, new) | See the rows below. |
| `render/nanite_gl_test.go`, `nanite_lod_gl_test.go` (updated) | The dropship test compares the GPU path against the classic path drawing the same cooked file, exactly. Against its source model the worst is one channel step (1/255), with 0 pixels off by more than 3. The pop test stops at 8 covered blocks rather than 6 (see gaps). |
| `collision/scenery_mesh_test.go` (new case) | `TestPagedCollisionIsReadAheadOfABody`: a body 100 m off reads nothing. One within reach reads the mesh ahead, once, on a goroutine. Walking onto it then waits for nothing (0 sync loads) and stands on it. Bodies gone, it is evicted after the TTL. A query straight onto the unloaded mesh reads it there, holds the body up, and counts one sync load. |
| `app/graphics_settings_test.go`, `app/bench_fly_test.go`, `script/script_nanite_props_test.go` (new) | The budget round-trips through graphics.yaml, is clamped, and defaults. The bench flight parses and interpolates its waypoints. The script property sets and clamps. |

**`nanitecook/paging_test.go`:**

| Test | What it checks |
| --- | --- |
| `TestPagesFollowTheDAG` | Pages respect the limits. Root pages hold only roots. No leaf and no window straddles a page except at root runs (1 of 38). A group's input is split only at page ends (5 of 46). Parents hold what each group made. |
| `TestPagesReadBackFromTheFile` | Each page read alone from the file equals the cook's pool bit for bit, and `Flat` is the whole of it. 200 random damagings of the data never panic. A file re-cooked under a loaded mesh is refused. |
| `TestAnyResidencyGivesAWholeCut` | With the roots alone, the cut is exactly the roots. With everything resident it equals the cooked cut. Over 60 random resident sets (not closed under parents) at 5 thresholds, the cut is always closed (no crack, no surface twice) and on resident pages. |
| `TestEffectiveTreeWalkMatchesTheFlatTest` | The refreshed tree equals the cooked one when everything is resident. The GPU's tree walk finds exactly the flat test's clusters for random residencies and eyes. |
| `TestRequestsStreamTheCutIn` | Loading what the too-coarse clusters ask for reaches the cooked cut, every step whole. |
| `TestCollisionMeshIsPagedOut` | The collision stays in the file until asked for. |

**`render/nanite_stream_gl_test.go`** (GL):

| Test | What it checks |
| --- | --- |
| `TestNaniteStreamPoolMatchesTheFlatPool` | Fully resident, it draws the Phase 2 pool's picture exactly over three frames, with no hole. |
| `TestNaniteStreamStreamsInFromTheRoots` | From the 2 root pages of 52, both upload paths (persistent ring and 4.3 / direct) settle on the full picture exactly in 13 frames, every frame whole. No frame takes in more than 16 pages. Records are checked as below, and no record write misses the ring. |
| `TestNaniteStreamUnderABudgetCap` | 4 distinct rocks, 58 pages, a 24-slot pool: the camera visits each rock in turn. The pool never holds more than its slots or takes in more than 16 pages a frame. It never cracks and never writes past the ring. It settles at every stop (3-12 frames) on exactly the unbounded picture: 56 pages in, 56 out, 0 starved. |
| `TestNaniteStreamTradesPagesByWorth` | One mesh at 31 placements in an 18-slot pool preloaded full. The near rock's asks evict pages in use worth less, and it sharpens: 21 → 15 blocks off the unbounded picture, with no crack. |

**The record check** (`checkStreamRecords`, run every frame) reads the cluster
records back from the GPU and requires:

- each drawing record points at the slot that holds its own page, with that
  page's triangles
- a non-resident cluster draws nothing and has parent error 0

**Mutation checks:**

- Dropping the residency's "consumers must be refinable" rule fails
  `TestAnyResidencyGivesAWholeCut`. The GL tests do not see it, since the
  streamer itself never strands a page below a missing one.
- Skipping the record rewrite in a frame that evicted fails the budget test:
  "resident but its record draws nothing".
- Switching off the worth trade fails `TestNaniteStreamTradesPagesByWorth`:
  0 uploaded.
- Disabling the collision prefetch fails the paged collision test.

**Runs on this box** (WSL2, Mesa D3D12 on the GTX 1070):

- **`go build ./...`, `go vet ./...` and `go test ./...` are clean.**
  - `go test -race` is clean on the paging, collision and streaming tests
    (render's with `FOUNDRY_GL_TEST=1`).
- **`FOUNDRY_GL_TEST=1` passes for render (85 s), gfx, devui, editor,
  collision, nav and world.**
- **Render also passes in two flags-off runs:** with
  `FOUNDRY_NANITE_STREAM=0`, and with Phase 1's full flags-off set plus
  `LOD=0 STREAM=0`.
- **The harness fails only the known
  `TestAPlaySessionDrawsTheRoomItIsStandingIn`.**
- **CPU-side flags-off runs pass:** collision, nav, world, nanite, nanitecook,
  editor, sim and ai with `FOUNDRY_NANITE_STREAM=0 FOUNDRY_NANITE_MESHES=0`.
  Collision and nav also pass with `FOUNDRY_NANITE_COLLISION=0`.

## Measurements

**Setup.** The same as Phase 2's:

- native Windows exe (`foundry-nanite3.exe`) cross-built from this tree
- GTX 1070, 1600x900, 8x MSAA
- the bench copy `%LOCALAPPDATA%\Temp\foundry-bench-n1f`
- GPU 7% busy beforehand
- every mesh in the copy re-cooked to format 4 (`bench_rock.fnm.v3` kept
  beside it)

Raw reports are in `docs/nanite/phase3_bench/`.

**The over-budget level** is `nanite_stream.yaml` in the bench copy: 48
distinct sample rocks of 81,920 triangles in two rows 120 m long.

- The meshes were made with `foundry nanite sample -level 6 -seed s`, s from
  0.25 in steps of 0.37 mod 0.9.
- That is 2,482 pages, 427 MB of slots, against the default 256 MB budget.

**The flight** is `fly=1` from (0, 2, 26) to (0, 2, -106), looking down the
rows.

- **Slow:** 900 measured frames.
- **Fast:** 300 frames, about 0.44 m a frame (most streaming per frame).

| Run | Frame avg | p99 | max | Pages in / out | Starved | `nanite: stream` CPU avg / max | Longest update |
| --- | --- | --- | --- | --- | --- | --- | --- |
| slow, flat pool (`STREAM=0`) | 5.33 | 6.91 | 7.34 | — | — | — | — |
| slow, pool holds all (1024 MB) | 5.27 | 6.54 | 7.02 | 0 | 0 | 0.00 | 1.00 |
| slow, **256 MB (over budget)** | 5.29 | 6.69 | 7.24 | 2,668 | 20 | 0.65 / 3.96 | 4.20 ms |
| slow, **64 MB** | 4.99 | 6.52 | 7.09 | 11,890 | 231 | 1.78 / 3.97 | 4.52 ms |
| fast, flat pool | 5.49 | 7.02 | 7.29 | — | — | — | — |
| fast, pool holds all | 5.49 | 6.87 | 7.19 | 0 | 0 | 0.00 | 0.63 |
| fast, **256 MB** | 5.54 | 7.16 | 7.30 | 1,593 | 0 | 1.03 / 3.80 | 3.86 ms |
| fast, **64 MB** | 5.12 | 6.61 | 6.90 | 4,230 | 0 | 1.88 / 5.33 | 5.24 ms |

**What the table says:**

- **Over budget, it plays without stalls.**
  - No streaming update was slow (no logs).
  - No frame passed 7.3 ms.
  - p99 is within 0.3 ms of the unbounded pool.
  - The 64 MB pool is a little faster because it draws coarser detail.
- **Streaming costs CPU while pages move:**
  - 0.65-1.9 ms a frame on average, 4-5 ms at worst.
  - About half is rebuilding the touched meshes' records.
  - A quarter is the fence-status query (0.24 ms a frame on this driver).
  - These frames are GPU-bound, so it does not show in frame time.
- **At 64 MB the pool trades pages constantly** (12 a frame in the slow
  flight): the view wants six times what it holds.

**Loads** (the first number is what the bench reports, which includes building
the pool at install):

| Pool | Load | First frame after load |
| --- | --- | --- |
| page pool | 0.6-0.7 s | 173-226 ms |
| flat pool | 2.5 s | 776 ms |

The flat pool decodes every page of 48 meshes and uploads them at the first
frame.

**The rock field** (`nanite_bench`, 244 placements of one mesh, fully resident,
no feedback):

| Pose | Flat pool | Page pool |
| --- | --- | --- |
| `0,2,6` | 14.13 ms | 14.07 ms |
| `0,20,2` | 12.93 ms | 13.02 ms |

Equal within noise. An earlier build, before window packing and before
feedback was skipped when fully resident, measured +3.5%.

**level1 / level2** (no cooked meshes): 6.70 / 7.96 / 2.75 and 9.76 / 9.77
ms. Phase 2 measured 6.63 / 7.85 / 2.71 and 10.64 / 9.85. Unchanged within
noise.

### Visual parity

**These bench levels' native shots are blank.** `nanite_bench.yaml` and
`nanite_stream.yaml` capture only the clear colour (or, given a sky, only the
sky), headless and native alike, while the profiler shows the world drawn.

- The Phase 2 exe does the same, so this is not new.
- **Phase 2's "rock bench: 0 pixels differ" compared two blank pictures.**
  Its shots in the bench copy are 6,406-byte uniform PNGs.
- It was not chased further: level1's captures work, so parity was measured
  there.

**Measured on `level1_rocks`** (level1 with 256 cooked bench rocks), native,
four poses:

- `hide=viewmodel,ui,volumetrics,sprites: blended,sprites: cutout,gi`, though
  the first-person weapon still draws.
- With the weapon's box masked out (its sway differs run to run), the flat
  pool against itself is 0 pixels, and the flat pool against the page pool is
  **0 pixels at all four poses**.
- Unmasked, flat-vs-flat differs by 33-38k pixels at three of them.
- **Capped at 4 MB** (23 slots for 52 pages), the same views show the rocks
  coarser in their fine bumps, as a pool holding less than half the mesh must.
  Two poses' preloaded pages already met the 1-pixel cut; the other two took
  in 90 and 263 pages.

**On WSL D3D12, the GL tests compare exactly:**

- the page pool against the flat pool
- a stream from the roots against full residency
- a capped pool at each stop against an unbounded one

## Known gaps / notes

- **Video memory.**
  - Each cluster keeps its own copy of the corners it shares with its
    neighbours. The bench rock decodes to 116,704 vertices where the welded
    pool was 40,962, so its decoded geometry is 5.4 MB against the old pool's
    3.1 MB.
  - Fixed 176 KB slots average about 60% full of vertices, so fully resident
    it takes 52 slots (8.9 MB).
  - The `STREAM=0` path has the same per-cluster layout: the welded pool no
    longer exists anywhere.
  - Sharing vertices within a page, or sizing slots by what pages hold, would
    win much of this back.
- **CPU while streaming:** up to 5 ms a frame at worst (above). The records
  of a touched mesh are rebuilt whole, then diffed; tracking which clusters
  changed would cut it.
- **The first frame after a load** is 173-226 ms on the page pool. It is
  probably the driver committing the immutable stores. Still a quarter of the
  flat pool's.
- **The pop test's floor:** `TestNaniteLODDoesNotPopAsTheCameraMoves` now stops
  at 8 covered blocks, not 6.
  - At 6 blocks (a 24-pixel rock) its step 18 was 2 blocks off where Phase 2's
    DAG gave 1.
  - The quantised cook builds a slightly different DAG. With a finer grid
    (22 bits) the original floor passes again.
  - The GL test's comment says so.
- **LOD off turns streaming off.** With `FOUNDRY_NANITE_LOD=0` the flat pool
  is used, since a pool that holds less than the full detail cannot draw
  level 0.
- **Fallback upload path.** Without buffer storage (a strict 4.3 driver), page
  data goes in by `glBufferSubData`, not through the ring. That is the path
  measured free of stalls on a mutable pool; the GL tests exercise it.
- **Rays.** A ray reaching a mesh-mode prop no body is near reads its
  collision on the spot, and is counted as a sync load. Prefetch is by bodies,
  not by rays.
- **The bench levels' blank captures** are noted above; not investigated
  beyond showing the world is drawn.
- **Format bump.**
  - `.fnm` is format 4, cook version 5. Every cooked mesh must be re-cooked;
    an older one is refused with the message.
  - The bench copy's meshes were re-cooked; the user's own project files were
    not touched.
- **Scratch output** is in the bench copy and safe to delete:
  - `n3_*` reports and logs
  - `shots_n3_*`
  - `foundry-nanite3*.exe`
  - `resources/models/stream/`
  - `nanite_stream.yaml`
  - `zz_skytest.yaml`
  - `*.glsl.p2` (the Phase 2 shaders the copy had)
