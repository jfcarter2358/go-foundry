# Nanite Phase 2: cluster DAG and continuous LOD

Status: **complete**, with the gaps listed at the end. Nothing is committed;
Phase 2 is in the working tree on top of `4c3e776` (phases 0-1).

Phase 2 covers components 2 (the cluster DAG), 4 (the fallback mesh) and 5
(GPU LOD selection). A cooked Nanite mesh, and a compiled level's large opaque
draw groups, now carry every level of detail of themselves, and the GPU picks
per cluster, every pass, the coarsest one that strays by no more than a pixel
from the full detail as seen from the camera. The cut is crack-free by
construction and the picture does not pop.

## What was built

### The simplifier (`internal/cluster/simplify.go`)

- **Edge collapse under quadric error** (Garland-Heckbert), the shape of
  meshoptimizer's simplifier. A collapse moves one position onto a neighbour
  (a half-edge collapse), so a simplified mesh uses a subset of the input's
  vertices and never makes a new one. Every level of a DAG therefore indexes
  the one vertex pool the cook wrote.
- **Attributes are weighed**, any floats after x, y, z (`AttrWeights`): each
  triangle fits each attribute as a linear function over its plane, and a
  vertex record's attribute quadric measures how far the value it carries is
  from those fits. A collapse that keeps an attribute on its gradient is free;
  one that smears it costs, in distance units per attribute unit. Cooked meshes
  weigh UVs and normals; compiled levels weigh UVs, normals and **the 7-float
  baked light record** (`gfx.LightStride`).
- **What may move:** a locked position never does (the caller's locks, and any
  non-manifold or over-complicated neighbourhood). An interior position with
  one record collapses along any edge; a UV/crease **seam** (two records at one
  point) only along the seam, each record to its own side's; an open
  **border** only along the border. The link condition keeps the surface
  manifold, and a collapse that turns a triangle over (more than ~75°) or
  flattens it is refused.
- **Tests:** a flat grid with linear UVs simplifies at no error and keeps its
  outline; locked vertices survive and a closed mesh stays closed; a seam
  stays a seam (no triangle's u spans the wrap); a light step on a grid is kept
  when the light is weighed, and smeared (the mutation check) when it is not.

### The DAG (`internal/cluster/dag.go`)

- **Build:** level 0 is `cluster.Build`'s clusters. Then a level at a time:
  partition the level's clusters into groups of about 8 neighbours (greedy
  graph partition over shared corner positions, seeded along a Morton walk,
  small groups folded into a neighbour), lock every position two groups share,
  simplify each group to half, re-split with `cluster.Build`. A group that
  will not simplify by 15% is carried to the next level unconsumed. It stops
  at one cluster or when nothing simplifies.
- **Error:** a group's error is the largest of the simplifier's, a measured
  two-way deviation between the group's original and simplified triangles
  (point-to-triangle, box-pruned), and every child's error, so error never
  falls going up. Its sphere encloses every child's LOD sphere. Each cluster
  stores its own (error, sphere) and its parent group's; roots have parent
  error -1.
- **Selection** is a per-cluster test: draw when the own error, projected from
  the nearest point of its sphere, is within the threshold and the parent's is
  not (`LODCluster.SelectedFrom`, `cluster.LODWithin`). Monotonic error and
  spheres make exactly one cluster pass along every chain, and a group's
  clusters hold identical numbers, so the cut is complete and crack-free with
  no traversal. `DAG.Cut(threshold)` is the same with one world-space error.
- **Layout:** clusters by level, then by consuming group, then by making group,
  with indices laid out cluster by cluster. Groups run in parallel
  (`parallelFor`) and are committed in order, so a build is deterministic.
  `BaseOrder` maps level-0 triangles back to input triangles.
- **Speed:** an 82k-triangle rock builds in about 0.7 s (the deviation measure
  was 60% of an unpruned build; box pruning and parallel groups made it 10x
  faster). `BenchmarkBuildDAG`.

### The LOD tree (`internal/cluster/lod_tree.go`)

- **Leaves** are runs of up to 32 clusters made by one group and consumed by
  one, so the LOD test answers alike for a whole leaf (`LeafRanges`;
  `TestALeafIsCutWhole`). Nodes are a 4-ary median-split tree per DAG level,
  under a node per material slot, under a mesh root. Nodes are laid out
  breadth first (children contiguous); leaves are numbered depth first (each
  node's leaves are one run).
- **Each node bounds** the cull spheres, the largest parent error with a
  sphere round every parent sphere, and the smallest own error with a sphere
  round every own sphere. A subtree is skipped when its parents are all fine
  enough or its own errors all too coarse (`TreeSkips`, with 1e-4 slack).
  `SelectThroughTree` is the GPU walk on the CPU; a test checks it finds
  exactly the flat test's cut from 40 random eyes.

### Cooked meshes (`.fnm` format v2, cook version 3)

- `nanite.Mesh.Clusters` is now every level (`cluster.LODCluster`), plus
  `Levels`, `Nodes`, `ClusterLeaf`, `Leaves`, and per slot `BaseCount`
  (level-0 clusters come first in each slot's run).
- **The cook** (`nanitecook/cook.go`) builds a DAG per material slot. Positions
  two slots share are locked, so the slots never crack against each other. It
  weighs normals and UVs (`Options.NormalWeight` 0.2, `UVWeight` 0.1, fractions
  of the mesh radius per unit), clears cones on double-sided slots, builds the
  tree, and cuts the **fallback mesh**.
- **The fallback** is a fixed mid-level cut: the threshold is the largest error
  at level `(Levels-1)/2`, stored as `Fallback` with per-slot ranges.
  - It is what the CPU keeps for a GPU-drawn placement: the editor's picking
    and the point-shadow signature (`world/scenery.go`).
  - The GI distance field is baked from it.
  - For the bench rock it is 3,480 of 81,920 triangles.
- `nanite.Mesh.TriangleCount` is level 0; `StoredTriangles` is every level;
  `SlotTriangles` and `BaseIndices` are level 0, so the classic path and
  `Closed()` see the full detail.
- `foundry nanite inspect` prints each level's clusters, triangles and error,
  the tree, and the fallback. The editor's Nanite group shows the level count.

### Compiled levels (`.fbsp` version 9)

- **The compiler builds a DAG** for every opaque, single-sided draw group of
  at least 256 triangles (`world.BuildStaticGroupDAG`). Double-sided groups
  keep the flat in-order cut, because their coplanar copies settle depth ties
  by draw order.
- **Welding:** a group's corner stream is a soup, so corners equal in all 15
  floats (position, UV, normal, baked light) are welded to the first of them,
  and the DAG indexes those: coarse triangles draw from the same buffer.
- **Seams between groups are locked** (`world.StaticSeams`): every position
  that a corner of more than one draw group sits on is locked in each group's
  DAG, at every level. Groups split by chunk and material, so a floor meets
  the next chunk's floor and a wall of another texture at such positions.
  Without the lock, each side collapses along its own open border on its own,
  and the two cuts keep different corners there. That leaves gaps on a curved
  or bumpy seam and T-junctions on a straight one. A small, double-sided or
  see-through group is drawn at full detail, so it counts too.
  `world/static_lod_seam_test.go` pins this: a bumpy floor split across two
  chunks keeps all 41 seam corners in both groups at every threshold. Before
  the lock, at a 0.2 m threshold one side kept 27 and the other 11.
- **Weights** (`StaticLODWeights`): UV 0.25 m, normal 0.25 m, light 1 m per
  unit. A shadow edge keeps its triangles until it is hundreds of metres off;
  an evenly lit floor collapses at no cost.
- **Stored** as `StaticClusterGroup.DAG` (indices, clusters, `BaseOrder`,
  tree). A group cut again at load gets the flat Phase 1 cut, never a DAG.
- **At runtime** (`render/static_nanite.go`) each DAG group is an instance of
  its own (identity, always visible) carrying its tree. The PVS marks level-0
  clusters by surface as before (via `BaseOrder`), and `propagate` marks a
  coarser cluster when anything its group consumed is marked.
- **Cost:** compile time is +5 s on level1 (57 → 62 s), +4 s on level2 and +3 s
  on level3. Files are 2-5% larger.
- **Coverage** (measured before the seam lock above, which keeps a few more
  triangles along group borders): level1 has 88 of 151 groups with a DAG (310k triangles at full
  detail, 136k at the roots); level2 135 of 261 (174k → 70k); level3 318 of
  1061 (164k → 51k).
- docs/guide/23 says version 9 requires a recompile.

### GPU LOD selection (`render/nanite_cull.go`, `nanite_lod.go`, shaders)

The pipeline is now: instance stage → **tree walk** → **cluster stage** →
merge → (pack) → indirect draws.

- **`nanite_lod_traverse_comp.glsl`** (new): one workgroup per DAG instance
  walks its tree breadth first.
  - The frontier is in shared memory, 1024 nodes. On overflow it marks the
    subtree's leaf run instead of descending.
  - It marks reached leaves with the pass's stamp (`passStamp`), so the marks
    are never cleared.
  - Profiler section: `nanite: lod`.
- **`nanite_cluster_common.glsl`** (new) holds the cluster and tile records and
  the tests: `lodSelects` (the exact test, or `level == 0` with LOD off) and
  `clusterDraws` (frustum, cone, occlusion and history).
- **The camera chooses the cut for every pass**, shadows included. A shadow cut
  coarser than what the camera sees would shadow the surface by its own
  difference. `beginNaniteLODFrame` fixes the eye and
  `k = H·P[1][1]/2/τ` (H is the frame height, τ the threshold) at the top of
  `DrawWorld`, so every pass of a frame agrees.
- **Range tiles** (`tileRange`) are the main performance change.
  - **Why.** A DAG holds about twice the clusters of the full detail, and the
    first version gave every cluster a command slot as Phase 1 does. On the
    GTX 1070 an indirect command costs the draw whether it draws or not, so the
    rock field got **slower** with LOD (22.7 ms against 18.6).
  - **What.** A DAG's tiles are now windows of up to 3 whole leaves and up to
    32 clusters, and each window has 2 command slots. One thread per window
    (`nanite_range_cull_comp.glsl`) tests each cluster, builds the survivors'
    runs, and joins the nearest runs while there are more runs than slots.
  - **The join rule.** A join may only cross a gap that is all in the cut:
    clusters that the frustum, cone or occlusion dropped, which costs drawing
    them and nothing else. It never crosses a cluster of another level. A leaf
    is taken or left whole, so 3 leaves leave at most 2 runs that cannot be
    joined.
  - **Where tiles are dispatched.** Cluster tiles and range tiles are listed by
    kind (`kindBuf`, binding 13). Merge and packing run over the cluster tiles;
    range tiles pack by a thread each (`COMPACT_SCATTER_RANGE`).
  - **Cost.** The shadow cone still drops 5-6% of triangles with range tiles,
    against 7-8% per cluster (`TestSceneryShadowConeDropsBackClusters...`).
- **Packing** for cooked meshes now follows the shadows' Phase 1 rule in the
  camera passes too: pack only when most placements are out of view. With
  range tiles the list is short, and on the rock field packing cost the two
  camera passes 1 ms more than it saved. Compiled surfaces and chunks keep
  Phase 1's rule (the camera always packs).
- **`markLive`** tests a sphere per tile before testing that tile's clusters,
  so a group out of view costs one test per tile rather than one per cluster
  of every level.
- **`gpuCluster`** is now 112 bytes: it adds the LOD sphere, the parent sphere,
  and the errors and level. `Draw.w` is the cluster's leaf. `gpuNode` is 80
  bytes. Instance flag bit 3 (`instLOD`) marks an instance with a DAG;
  `Flags[1]` is then its tree root and `Flags[2]` its first leaf mark.

### Debug views (`render/nanite_debug.go`, `nanite_debug_vert/frag.glsl`)

- **The views:**
  - `clusters`: each cluster its own colour.
  - `lod`: colour by DAG level. White is level 0, then blue, cyan, green,
    yellow, orange, red and magenta.
  - `overdraw`: rasterised fragments added up with no depth test.
- **How to get them:** `render.SetView`, `foundry play -view`, a play script's
  `view`, or the editor's viewport tool. `ViewNames` and the docs list them.
- **How they work:**
  - They draw what the GPU cull draws (cooked meshes, compiled surfaces, chunk
    brushes) through a small program in place of the material. Everything else
    in the frame draws as usual.
  - In a debug view, DAG batches use per-cluster tiles. Each command is one
    cluster with its slot as its base instance, and the merge pass is skipped.
  - A per-slot (instance, cluster) table is bound as an instanced attribute at
    location 11 for the debug draw only.
  - Profiler section: `nanite: debug view`.

### Setting

- **`gfx.NaniteLODError`** is the threshold in pixels: default 1, clamped to
  0.25-8.
  - It is saved to `graphics.yaml` as `nanite_lod_error`.
  - Scripts set it with `@level.prop.nanite_lod_error`.
  - docs/guide/17 and SCRIPTING.md mention it.
- **`FOUNDRY_NANITE_LOD=0`** draws the full detail only, on the GPU path.

## Flags

| Flag | Default | Effect |
| --- | --- | --- |
| `FOUNDRY_NANITE_LOD` | on | LOD selection; off draws level 0 only (Phase 1's picture). |
| `FOUNDRY_NANITE_LOD_STATIC` | on | Read by the compiler: whether large opaque compiled groups get a DAG. |
| `nanite_lod_error` (graphics.yaml) | 1 | Pixels of error allowed. |
| Phase 1's flags | unchanged | See PHASE_1.md. |

## Tests

| Test | What it checks |
| --- | --- |
| `cluster/dag_test.go` (new) | The simplifier cases above. The DAG is monotonic and ends at a root (20,480 triangles → 9 levels, one 78-triangle root). Every world-space cut at 8 thresholds is closed and keeps the surface area. 40 random view cuts are closed, and the tree walk finds exactly the flat test's clusters. Cost falls with distance (18k / 4k / 78 triangles at 3 / 30 / 300 m for 81,920). Determinism, `BaseOrder`, and leaves being LOD-uniform. |
| `nanitecook/nanite_test.go` (new case) | A cook builds the DAG and tree. Cuts of both slots together at 4 thresholds are closed. The fallback is closed and under a quarter of the triangles. |
| `world/static_lod_seam_test.go` (new, verifier) | Two compiled groups meeting along a bumpy seam keep every seam corner in every cut, from full detail to the roots. |
| `levelio/compiled_lod_test.go` (new) | A compiled hall gets a DAG that survives the `.fbsp` round trip, its level 0 covers the group exactly once, and its indices stay in range. |
| `app/graphics_settings_test.go` (new case) | `nanite_lod_error` round-trips, is clamped, and defaults. |
| `render/nanite_lod_gl_test.go` (GL, new) | **No cracks:** a closed rock field at τ 1, 4 and 8 shows no back face through a hole. The mutation check (no border locks) shows 22-193 holed pixels. **Close to full detail:** at τ 1, ≤ 2% of 4x4 blocks off by > 24/255 (1 of 352). **Cost by screen size:** 81,920-triangle rock at 2.5 / 10 / 40 / 90 m draws 66k / 35k / 5.7k / 2.6k, identical with and without the tree walk. **Shadows draw the camera's cut:** 66k near, 2.6k far, of 123k. **No popping:** 24 camera steps from 2 to 98 m, each frame within 2% of blocks of the full detail at that spot. **Debug views.** **Compiled wall:** no holes at 6, 14 and 30 m, light within tolerance, 5.4k → 570 → 282 triangles (with prepass). **Light is weighed:** the light weight at 0 smears 1,055 of 2,304 blocks. |
| Phase 1 GL tests | Unchanged and passing. They pin LOD off (they compare exactly against the full-detail classic path). |

**Runs on this box** (WSL2, Mesa D3D12 on the GTX 1070):

- **`go build ./...`, `go vet ./...` and `go test ./...` are clean.**
- **`FOUNDRY_GL_TEST=1 go test ./internal/render/... ./internal/gfx/... ./internal/devui/... ./internal/editor/...` passes.** The render suite takes about 78 s.
- **The render GL suite also passes in two flags-off runs.** One sets
  `FOUNDRY_NANITE_LOD=0 FOUNDRY_NANITE_LOD_STATIC=0`. The other is Phase 1's
  full flags-off set plus `LOD=0`.
- **The harness fails only the known `TestAPlaySessionDrawsTheRoomItIsStandingIn`.**
- **`TestIndexedModelDrawsTheDeindexedPicture` does not hang** with the GPU to
  itself. The verifier ran it 6 times on its own, under a 120 s timeout, and in
  three full render runs, finishing in about 3.5 s each time. The earlier hang
  came from two sessions sharing the GPU.
- **The scenery shadow's draw count is pinned** by
  `scenery_shadow_count_gl_test.go`, which counts with GL_PRIMITIVES_GENERATED,
  and by the LOD shadow test. `RenderSceneryShadow` skips GPU-drawn Nanite
  placements, and a cooked mesh drawn the classic way loads only its level 0
  (`SlotTriangles`). The coarser DAG levels never reach it.

## Measurements

**Setup.** The setup matches Phase 1's:

- GTX 1070, 1600x900, 8x MSAA, GI low
- 90 warm-up and 240 measured frames per pose
- native Windows exes, in the bench copy `%LOCALAPPDATA%\Temp\foundry-bench-n1f`
- GPU idle beforehand (`nvidia-smi` 3%)

**The baseline** is `foundry-nanite1x.exe`. It is HEAD (`4c3e776`) plus the
shadow session's uncommitted `shadow.go` change, built from a `git archive`
copy. The bench copy's shaders already carry that session's `fragment.glsl`,
so this is the like-for-like Phase 1. The baseline runs used HEAD's Nanite
shaders, the v1 rock, and `.fbsp` v8 compiled from the same YAML by Phase 1's
compiler.

Raw reports are in `docs/nanite/phase2_bench/`. Their names:

- `f1_*`: Phase 1
- `f2_*_on`: Phase 2
- `f2_*_off`: Phase 2 with `FOUNDRY_NANITE_LOD=0`

| Scene / pose | Phase 1 frame | Phase 2 LOD off | Phase 2 LOD on | Sun shadows GPU P1 → on | Cooked colour GPU P1 → on |
| --- | --- | --- | --- | --- | --- |
| rock bench `0,2,6` | 18.87 | 18.79 | **13.63** (-28%) | 11.58 → 8.24 | 3.91 → 1.93 |
| rock bench `0,20,2` | 17.87 | 17.77 | **12.82** (-28%) | 11.36 → 8.17 | 3.91 → 1.91 |
| rock bench `0,2,6`, τ = 2 | | | **10.11** (-46%) | → 5.30 | → 1.33 |
| level1 spawn | 6.71 | 6.64 | 6.63 | | |
| level1 hangar | 7.92 | 7.86 | 7.85 | | |
| level1 ground | 2.74 | 2.71 | 2.71 | | |
| level2 spawn | 9.65 | 10.03 | 10.64 | | |
| level2 `0,8,10` | 9.75 | 9.77 | 9.85 | | |
| level3 spawn | 13.34 | 12.88 | 12.88 | | |

**What it says:**

- **The dense mesh now costs by screen size.** The rock field is 28% cheaper at
  the default 1 px, and 46% cheaper at 2 px.
  - Most of the saving is in the sun's cascades, which draw the camera's cut.
  - The colour pass halves.
  - At 1 px with the default normal weight, the visible rocks 20-95 m off stay
    near full detail, and their pictures are identical with LOD on and off
    (below). The colour pass's saving is in the rocks that are hidden or far.
- **Levels 1-3 are unchanged within noise.** Their frames are CPU-bound; GPU
  time per section is the same as Phase 1's (compiled 3.73 vs 3.71 on level2,
  4.84 vs 4.83 on level3).
  - level2 spawn's 10.64 is run noise: a rerun of the same configuration
    moved every CPU section by 10%.
  - The compiled DAGs do not change these poses: the camera is near the
    walls, so level 0 is the cut.
  - They pay off on far surfaces. The GL wall test draws 282 of 4,608
    triangles at 30 m.
- **A trap found on the way.** An early baseline run swapped back only some of
  Phase 1's Nanite shaders, so Phase 2's merge and pack shaders ran under the
  Phase 1 exe. Level2's walls vanished, and its GPU time read as 1 ms instead
  of 3.7. Any baseline must restore every `nanite_*.glsl`.

### Visual parity

**Headless `foundry play run -compiled -gi off -size 960x540` on WSL D3D12**,
three shots each, LOD on against off:

- level1: 0 pixels differ in all three shots.
- level2: 0 pixels differ in all three. The spawn view holds little static
  geometry.
- Two LOD-off runs differ by 0 pixels as well.
- The `nanite_bench` YAML level has no spawn camera in front of the rocks, so
  its headless shots are empty. It was compared natively instead.

**Native (bench `shots`), LOD on against off:**

- rock bench: 0 pixels differ at both poses.
- level2 `0,8,10`: 61 pixels differ by more than 24/255, which is 1 4x4 block
  of 90,000 (the distant spire).
- level2 spawn: 0 pixels differ by more than 24/255.
- Phase 1 against Phase 2 at `0,8,10`: 87 pixels.

**A stale-cook bug, found by these captures.** A level compiled while the
leaves were still runs by (level, parent) only showed a hole in that spire.
Those leaves are not LOD-uniform, and a range join crossed a cluster of
another level. The leaf rule is now (level, made-by group, consumed-by group).
The cook version went to 3 so any such cook is redone. The recompiled level
draws the spire whole.

## Known gaps / notes

- **Chunk brushes (live, uncompiled) have no DAG.** They are cut per brush at
  load and drawn at full detail, as in Phase 1. Compiled levels and cooked
  meshes have LOD.
- **Coarse levels reuse the full detail's vertex records.** That is what
  half-edge collapse means, so a coarse triangle interpolates the kept
  vertices' fine normals.
  - The normal weight (0.2 of the radius) keeps that within the threshold. On
    the bumpy sample rock in its normal colours, that is ≤ 2% of 4x4 blocks
    off by more than 24/255.
  - It also makes the LOD conservative on very bumpy meshes: the bench rock's
    first simplification already costs 5 cm of error.
  - τ = 2 is the knob, measured above.
- **Locked borders:**
  - A cooked mesh's DAG is per material slot, and where slots meet is locked at
    every level, so a two-slot rock's root is ~250 triangles.
  - A compiled level's DAG is per draw group. Positions shared with another
    group are locked (`StaticSeams`), and the group's other open borders only
    collapse along themselves, so its roots keep ~40% of the triangles. That
    figure is from before the seam lock, so it is now somewhat higher.
- **Collision is unchanged.** It still uses the proxy box: Phase 2b builds the
  collision cut. The fallback mesh serves editor picking and the GI bake.
  Compiled-level raycasts use the full surfaces.
- **The point-shadow static cache** hashes geometry, not the camera. A cached
  cube face may hold the cut of an earlier eye until something else
  invalidates it. Any cut is complete, so this means a different detail in
  that light's shadow, never a hole.
- **The tree walk's frontier** is 1024 nodes per instance. On overflow it
  marks whole subtrees, which is correct but costs more. No measured scene
  comes near it.
- **Debug views** show only what the GPU cull draws. `overdraw` counts
  rasterised fragments with no depth test, not shaded ones. The depth prepass
  makes shading ~1x.
- **Range-tile joins draw** frustum-, cone- or occlusion-culled clusters lying
  between two survivors of one window. That is cost, never a wrong picture.
- **Every `.fnm` must be re-cooked** (cook version 3, format 2:
  `foundry nanite cook -all`), and every `.fbsp` recompiled (version 9).
  - The bench copy was re-cooked and recompiled.
  - The user's own project files were not touched.
- **Scratch output** is in the bench copy: `n2_*`, `f1_*` and `f2_*` reports,
  `foundry-nanite2.exe`, `foundry-nanite1x.exe`, `.fbsp.v8` / `.v9`, and
  `bench_rock.fnm.v1`. All of it is safe to delete.
