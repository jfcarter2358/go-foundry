# Nanite Phase 1: clusters and GPU occlusion culling

Status: **complete**, with the gaps listed at the end. Nothing is committed;
Phase 0 and Phase 1 are both in the working tree on top of `c4009b6`.

Phase 1 covers components 1 (clustering), 6 (two-pass occlusion culling) and
the first part of 5 (GPU selection, without LOD yet). The opaque world now
draws through a GPU-driven cull:

1. A compute pass culls instances.
2. A compute pass culls clusters by frustum, normal cone and a two-pass Hi-Z.
3. One indirect multi-draw goes out per material.

This applies to compiled level surfaces, live chunk brushes, and a new kind of
asset: cooked Nanite meshes imported from glTF. The shadow passes cull per
cluster too.

The first implementer was interrupted before writing these notes. A second
"finish" run fixed the package layering and was then stopped. This document is
from the third run. That run found and fixed two hangs and the shadow-cost
report, added the shadow-pass cone test, and measured everything below.

## What was built

### Clusters (`internal/cluster`)

- **`Build`** cuts an indexed mesh into meshlets of up to 128 triangles and 64
  vertices. It grows them greedily over shared edges.
- **Per cluster** it stores bounds (box, sphere) and a normal cone. The cone
  has an apex, an axis and a cutoff, with `NoCone` when the triangles face too
  many ways.
- **The cone test is conservative.** If the eye lies inside the cone's
  cutoff, every triangle faces away. The proof is in `coneOf`.
- **`BuildSequential`** cuts without reordering, for double-sided surfaces
  whose coplanar copies settle a depth tie by draw order.
- **Tests:** partition within limits, fill, soup vs indexed, determinism, a
  conservative cone on a sphere, a flat patch, `Reorder`. `BenchmarkBuild` is
  the benchmark.

### Compiled levels (`.fbsp` version 8)

- **`world/static_clusters.go`** groups a compiled level's surfaces exactly as
  `render/static_batch.go` draws them (`StaticGroups`). It cuts each group in
  `CutStaticGroup` and signs the cut over its members' triangle counts.
- **`levelio.CompileLevel`** stores the cut in the `.fbsp`, in
  `CompiledLevel.Clusters`. That is why `CompiledVersion` went from 7 to 8.
- **At load** a group finds its stored cut by key. A cut whose signature does
  not match (a different Smooth Lighting grouping, say) is made again rather
  than trusted.
- **Version 7 files are refused** with the usual message to recompile.
  docs/guide/23 says so.

### glTF import and the cooked mesh (`.fnm`)

- **`model/model_gltf.go`** imports glTF 2.0 into an `ImportMesh`:
  - `.gltf` with sidecar or `data:` buffers, and `.glb`
  - node transforms, and strips and fans
  - `POSITION`, `NORMAL` and `TEXCOORD_0`
  - one material slot per glTF material, with its base-colour texture name and
    `doubleSided`

  It does not read skins, morph targets, animations, sparse accessors or
  extensions. `EncodeGLB` writes one.
- **`internal/nanite`** is the cooked file and nothing else: format v1, reader,
  writer and load cache. It is pure CPU, so `internal/pack` can follow a cooked
  mesh's textures without linking OpenGL.
- **`internal/nanitecook`** is the cook: import (glTF, OBJ or `.fmdl`), weld,
  cut each slot into clusters, bounds, and a mesh SDF.
  - A cook is keyed on the SHA-256 of the source, its sidecars and
    `CookVersion`, so an up-to-date cook is skipped.
  - `foundry nanite cook|inspect|sample` is the command line.
  - **Window > Cook Nanite Meshes**, and the scenery panel's
    **Nanite > Cook and use**, are the editor (`editor/editor_nanite.go`).
- **`internal/meshsdf`** is the mesh SDF bake. It moved out of
  `render/gi_meshsdf.go` so the cook can use it. Cooked placements are live GI
  sources: `gi_sources.go` uses kind `'n'` with the baked grid.
- **`.fmdl` and `.obj` cooks are double-sided.** The engine draws models
  without back-face culling, so their clusters carry no cone. Only glTF
  materials can be single-sided.

### Cooked scenery (`render/nanite_scenery.go`)

- **One shared pool.** Every placement of every cooked mesh draws from one
  vertex, normal, index and cluster buffer.
- **One instance record per placement**, in an SSBO. It holds the matrix
  (which the vertex stage also reads, at locations 7-10), the sphere and flags.
- **One indirect multi-draw per resolved material.** A placement can override
  a slot's texture: `materials: {stone: granite}` in the level YAML, or the
  Nanite group in the panel.
- **The classic scenery paths skip `NaniteGPU` placements.** Their submeshes
  are CPU-only, kept for picking, raycasts and the point-shadow signature.

### The GPU cull (`render/nanite_cull.go` + `nanite_*.glsl`)

- **Instance stage** (`nanite_instance_cull_comp.glsl`): frustum, plus Hi-Z in
  pass 2.
- **Cluster stage** (`nanite_cluster_cull_comp.glsl`): the instance's verdict,
  the frustum, the normal cone, and in pass 2 the Hi-Z.
  - Every slot is written, a culled one as a zero count, so the order is fixed
    and captures are deterministic.
- **Merge** (`nanite_merge_comp.glsl`): joins each run of surviving neighbours
  into one command. A mesh in full view becomes about one command per tile,
  not one per cluster.
- **Pack** (`nanite_compact_comp.glsl`): moves survivors to the front of each
  group, and draws by count with `glMultiDrawElementsIndirectCount`. That is
  4.6 core or `ARB_indirect_parameters`, feature-detected in `internal/glext`;
  the fallback is the fixed list.
- **Two-pass occlusion.** Pass 1 draws what was visible last frame. Then the
  Hi-Z pyramid (`render/hiz.go`, shared with the foliage cull) is built, and
  pass 2 tests the rest and records history.
  - On by default for cooked meshes.
  - For brushes and compiled surfaces, only with
    `FOUNDRY_NANITE_OCCLUSION_BRUSHES=1`; measurement put it off.
- **PVS stays the first filter for compiled surfaces.** The tiles are rebuilt
  only when the PVS answer changes (`VisibleSerial`).
- **Depth prepass** (`render/nanite_prepass.go`): the culled draws go through
  a depth-only program, then shade at LEQUAL. Without it the cull's reordering
  made level1's hold 2 ms slower to draw.
  - `vertex.glsl` declares `invariant gl_Position` so both programs agree to
    the bit.
  - Off by default for cooked meshes (`FOUNDRY_NANITE_PREPASS_MESHES`), which
    are vertex-bound.
- **Chunk brushes** (`render/chunk_nanite.go`): each chunk batch's resting
  brushes are cut per brush and culled under the chunk's matrix. That covers
  the whole of level1's opening. Moved brushes, transparent ones and the
  editor's view stay on the CPU path.
- **Profiler sections:** `nanite: cull`, `nanite: prepass`, `nanite: draw`,
  `nanite: hi-z`, `nanite: occlusion` and `nanite: scenery`, in both the colour
  and shadow trees.

### Shadows

- **Compiled surfaces, chunk brushes and cooked scenery** cull per cluster
  against each shadow view's own frustum (cascade box or cube face), never the
  camera's PVS or occlusion.
- **Shadow cone (new in this run).** Shadow passes draw back faces, so the
  camera's cone test was off there. For a **closed** mesh it is exact: a ray
  from the light enters a solid before it leaves, so the nearest depth always
  comes from a light-facing triangle.
  - `nanite.Mesh.Closed()` checks that every edge is shared by exactly two
    oppositely wound triangles, matched by position so UV seams do not open
    it.
  - Placements of closed meshes get instance flag bit 2, and shadow passes
    cone-test only those (`CONE_CLOSED`).
  - The eye comes from the light's frustum itself (`frustumEye`): the side
    planes' apex for a point light's face, or the near-plane normal (w = 0,
    a direction) for an orthographic sun cascade.
  - Open meshes, compiled surfaces and brushes are never cone-tested in
    shadows, because a level's shell can be open.

## The two hangs, and the "huge vertex count"

### 1. `TestIndexedModelDrawsTheDeindexedPicture` hanging (a Mesa D3D12 driver deadlock)

**This was not a Nanite bug, and not a big draw.** The test sat in
`glReadPixels` until `go test` killed it. The vertex-shader dump before it was
the normal compile log of an earlier test.

On this box the GL context is **Mesa 26.2.3 D3D12 (GTX 1070)**. Phase 0's runs
had llvmpipe. That driver deadlocks the **second synchronous readback** in a
context when nothing finished the GPU's work in between:
- `clear; readPixels; clear; readPixels` hangs, with no draw at all.
- A `glFinish` before each readback avoids it.
- llvmpipe (`GALLIUM_DRIVER=llvmpipe`) never hangs.
- Clean `c4009b6` hangs in exactly the same place.

The fix is `internal/glext/readback.go`: `glext.ReadPixels`, `GetTexImage` and
`GetBufferSubData` finish first. A synchronous readback waits for the GPU
anyway, so this costs nothing it was not already paying. Every readback in
`internal/` and `cmd/` now goes through them. That includes the engine's own:
- `glctx.Target.Image`, used by `foundry render`, `play shot` and the harness
- the bench's shots
- the editor's Claude screenshot
- the foliage impostor bake, which read back in a loop
- the Nanite stats readback

A source test (`glext/readback_test.go`) fails on any bare `gl.ReadPixels` /
`GetTexImage` / `GetBufferSubData`.

**A second D3D12 stall of the same family** was in
`TestAPaintedBiomeGrowsFoliage`. It hangs after any other terrain test in the
same process, with or without a finish before the readback, and also at
`c4009b6`. The terrain probe now finishes after `SyncTerrainMeshes`
(`terrain_gl_test.go`, comment there). This is a test-side workaround: the
real game on Windows never showed it.

**Result:** the whole render GL suite (about 150 tests) runs in about 60 s on D3D12.
Before, it timed out at 9 minutes.

### 2. `foundry play` on level1 hanging in `glBufferData` (D3D12, Phase 1 code)

This was found while capturing for parity. `cullBatch.cull` orphaned its
command buffer every pass (`glBufferData(nil)`). Chunk brush shadows ran that
once per cascade and per point-light cube face, and on Mesa D3D12 one of those
calls never returned.

The per-pass stores (commands, packed commands, counts) are now sized once and
reused (`passStore`). GL already orders the compute write after the draws that
read the store. Natively on NVIDIA it measured the same or slightly faster: see
`*_on_final.txt` against `*_on.txt`.

### 3. "RenderSceneryShadow draws a huge number of vertices"

**The counts are correct.** `render/scenery_shadow_count_gl_test.go` counts
with `GL_PRIMITIVES_GENERATED` around each scenery shadow walk:
- **Classic scenery** (per placement and instanced, indexed and de-indexed)
  draws exactly each placement's triangles once, and one placement's alone
  under a frustum that holds only it.
- **The Nanite shadow** draws exactly placements × triangles with no frustum,
  fewer under a tight one, and the classic walker draws none of the cooked
  placements.
- **Mutation checks** confirm the test can fail. Dropping the instanced path's
  cull gave 1080 against 360. Drawing the Nanite groups twice gave 122,880
  against 61,440.

**What is huge is the scene.** The rock bench is 244 rocks of 81,920 triangles,
about 20 M triangles. A sun cascade draws every rock inside its box, three
cascades a frame, with no LOD, so up to about 60 M triangles go into shadows.
That is 12 to 14 ms of the GTX 1070, classic or Nanite. Phase 2's LOD is the
real fix.

**What this run did about it:**
- the exact shadow cone above: Nanite scenery shadows 13.96 → 11.36 ms, now
  about 10% under classic's 12.64
- the tests that pin the counts

## Flags

All are read at startup (cooked placements: at load). Each "off" puts that
piece back on the pre-Phase-1 path.

| Flag | Default | Effect |
| --- | --- | --- |
| `FOUNDRY_NANITE_CLUSTERS` | on | Compiled surfaces through the GPU cull; off means the CPU cluster cull as before. |
| `FOUNDRY_NANITE_CHUNKS` | on | Chunk brushes through the GPU cull. |
| `FOUNDRY_NANITE_MESHES` | on | Cooked `.fnm` placements through the GPU cull; off means drawn as an ordinary indexed model. |
| `FOUNDRY_NANITE_SHADOWS` | on | Shadow passes cull compiled surfaces and chunk brushes per cluster on the GPU. |
| `FOUNDRY_NANITE_SHADOW_CONE` | on | Cone test in shadow passes, for closed cooked meshes only (new). |
| `FOUNDRY_NANITE_CONE` | on | Normal-cone test (the camera; gates the shadow cone too). |
| `FOUNDRY_NANITE_OCCLUSION` | on | Two-pass Hi-Z occlusion (cooked meshes). |
| `FOUNDRY_NANITE_OCCLUSION_BRUSHES` | off | The same for compiled surfaces and chunk brushes. Off on measurement: many groups, few triangles, and pass 2's binds cost more than it saved. |
| `FOUNDRY_NANITE_PREPASS` | on | Depth prepass for the culled draws. |
| `FOUNDRY_NANITE_PREPASS_MESHES` | off | The prepass for cooked meshes too. Off on measurement: vertex-bound, 2-4 ms slower. |
| `FOUNDRY_NANITE_COMPACT` | on | Pack survivors and draw by count where the driver has it. |
| `FOUNDRY_NANITE_STATS` / `_STATS_LOG` | off | Read the commands back to count triangles for F3 (a stall), or log the count. |
| `FOUNDRY_NANITE_INDEXED` | on | Phase 0's indexed models. |

The bench's "off" runs set `CLUSTERS`, `CHUNKS`, `SHADOWS`, `PREPASS` and
`MESHES` to 0.

## Tests

| Test | What it checks |
| --- | --- |
| `cluster/cluster_test.go` | Meshlet limits, fill, determinism, a conservative cone, `BuildSequential`. |
| `nanite/closed_test.go` (new) | A seam-split tetrahedron is closed. A missing face, an inward face or a three-way edge is not. Degenerates are ignored. |
| `nanitecook/nanite_test.go` | glTF cook validity and slots, encode/decode round trip, up-to-date skip, SDF resolution, a cooked `.fmdl` equal to the scenery mesh, and (new) a cooked rock stays closed. |
| `model/model_gltf_test.go` | GLB round trip, node transforms, strips and normalised UVs, sidecar buffers and errors. |
| `world/static_clusters_test.go`, `levelio/compiled_clusters_test.go` | Grouping by what a draw shares. The compiler's cut survives the `.fbsp`, every loaded group finds it, each triangle is placed once, and a mismatched cut is recut. `VisibleSerial`. |
| `world/scenery_nanite_test.go`, `pack/refs_nanite_test.go`, `editor/editor_nanite_test.go` | Cooked placements load, the packer follows cooked textures, and Cook-and-use, Cook-all and material overrides round-trip. |
| `render/nanite_cull_test.go` (new) | `frustumEye` for perspective and orthographic frusta. |
| `render/nanite_gl_test.go` (GL) | Static surfaces match the CPU path over frames, occlusion culls what a wall hides, the cone culls from behind, shadows match the CPU path, the cooked dropship matches classic scenery, dense rocks match and cast the same shadow, chunk brushes and their shadows match, packed and fixed lists match, and the prepass uses the world program's matrices. |
| `render/scenery_shadow_count_gl_test.go` (GL, new) | The primitive counts above. The shadow cone, for a sun cascade and a point-light face, leaves the shadow map identical to classic's sample for sample while drawing fewer triangles (92-93% on the bumpy sample rock). Mutation-checked: flipping the sun direction changes 793 samples. |
| `glext/readback_test.go` (new) | No bare readbacks. |
| `layering_test.go` | Rules for `cluster`, `meshsdf`, `nanite` (pure CPU, below `gfx`/`model`), `nanitecook` (new: below `world`/`levelio`/everything that draws) and `glext`. The `pack` entry got its comment back. |

**Runs on this box** (WSL2, Mesa D3D12 on the GTX 1070):
- `go build ./...`, `go vet ./...` and `go test ./...` all pass, 54 packages.
- `FOUNDRY_GL_TEST=1 go test ./internal/render/... ./internal/gfx/... ./internal/devui/... ./internal/editor/...`
  passes, render in about 57 s.
- `FOUNDRY_GL_TEST=1 go test ./internal/harness/` fails only the known
  pre-existing `TestAPlaySessionDrawsTheRoomItIsStandingIn`.
- **Flags off.** The same render GL suite also passes with
  `FOUNDRY_NANITE_CLUSTERS/CHUNKS/SHADOWS/PREPASS/MESHES/INDEXED=0` in the
  environment: everything outside the Nanite tests runs on the classic path.
  The Nanite GL tests pin every switch to its default (`pinNaniteFlags`, called
  from `naniteProbe`), so the environment cannot turn them into tests of the
  classic path; before the verifier added that, six of them failed under the
  flags-off run. `world`, `levelio`, `editor`, `gfx`, `pack` and `collision`
  pass flags-off too.
- **Verifier's parity check:** headless `foundry play run -compiled -gi off`
  on level1 (two shots), Phase 1 on against off: 0 pixels differ, and the run
  did not hang (the `glBufferData` hang above).

## Measurements

**Setup.**
- Native Windows exe cross-built from this working tree (`foundry-nanite1g.exe`,
  with every fix above).
- GTX 1070, 1600x900, 8x MSAA, 90 warm-up and 240 measured frames per pose,
  compiled `.fbsp` (v8).
- Run from a separate project copy, `%LOCALAPPDATA%\Temp\foundry-bench-n1f`,
  so the other session's bench copy was untouched.
- GPU idle beforehand (`nvidia-smi`: 2-11%).
- Raw reports are in `docs/nanite/phase1_bench/`.

**Phase 0's absolute numbers are not comparable.** The other session's GI work
landed in between: `gi: probes` went from 15-29 ms to about 2 ms. Compare the
on/off pairs.

### Levels (ms; "off" = Phase 1 flags off)

| Level / pose | Frame avg off → on | GPU frame off → on | Opaque world GPU off → on | Opaque world CPU off → on | Sun shadows GPU / CPU off → on |
| --- | --- | --- | --- | --- | --- |
| level1 spawn (ship hold) | 9.73 → 6.57 | 9.57 → 6.41 | chunks 5.87 → 2.58 | 0.34 → 0.56 | 0.14 → 0.31 / 0.30 → 0.21 |
| level1 hangar `1.5,102,-20,-1.5708,-0.12` | 10.35 → 7.78 | 10.20 → 7.62 | chunks 5.95 → 3.24 | 0.34 → 0.54 | 0.15 → 0.32 / 0.19 → 0.22 |
| level1 ground `1.5,4,-77,3.14,0` | 2.66 → 2.70 | 2.49 → 2.54 | 0.00 → 0.00 | — | 0.07 → 0.14 |
| level2 spawn | 15.44 → 9.73 | 9.52 → 8.11 | compiled 4.41 → 3.66 | 4.89 → 1.31 | 0.31 → 0.60 / 2.97 → 1.00 |
| level2 `0,8,10,-1.5708,-0.12` | 14.75 → 9.45 | 9.10 → 8.06 | compiled 4.65 → 3.66 | 4.89 → 1.26 | 0.31 → 0.61 / 2.80 → 1.02 |
| level3 spawn | 17.75 → 13.24 | 9.17 → 9.29 | compiled 4.88 → 4.74 | 5.84 → 3.45 | (no sun) |

**What the table says:**
- **level1's opaque GPU time drops** by 2.7 to 3.3 ms in the ship (`world:
  chunks` 5.9 → 2.6 / 3.2), and the frame goes from about 10 ms to 6.6 / 7.8.
  This is the spec's "done when".
- **level2 and level3 were CPU-bound, and the cull mostly removes CPU:**
  `world: compiled` CPU drops 3.6 / 2.4 ms and level2's sun-shadow walk 2 ms.
  Frame time drops 4.5 to 5.7 ms on level2 and 4.5 ms on level3.
- **Sun shadows cost more GPU** (+0.15 to +0.3 ms) where they cost less CPU.
  The GPU cull of many small brush groups is not free. It is a net win
  everywhere measured, but a known item (gaps).
- A repeat run (`l1_on2`) agrees with `l1_on` to 0.04 ms.

### The dense-mesh bench (`nanite_bench`, 244 placements × 81,920 triangles, authored YAML)

| Pose | Classic instanced | Nanite, shadow cone off | Nanite, final |
| --- | --- | --- | --- |
| `0,2,6,-1.5708,0` frame | 18.17 | 21.42 | 18.71 |
| sun shadows: scenery GPU | 12.64 | 13.96 | 11.36 |
| colour: scenery GPU | 3.34 | 3.95 | 3.90 |
| `0,20,2,-1.5708,-0.5` frame | 17.28 | 20.39 | 17.74 |
| sun shadows: scenery GPU | 12.49 | 13.74 | 11.16 |
| colour: scenery GPU | 3.39 | 3.92 | 3.86 |

**What it says:**
- Without LOD a scene that is mostly in view is vertex-bound whichever path
  draws it.
- The Nanite shadow now beats classic by about 10%. The colour pass is about
  0.5 ms behind (cull + occlusion overhead with almost nothing to occlude
  here), for 3% more frame time overall.
- The cooked path's wins are culling: a field behind a hill, or half out of
  view. This run did not bench such a pose.

### Visual parity (headless, deterministic)

Run with `foundry play run -compiled -gi off -size 960x540` on WSL D3D12,
three shots per level (spawn view, turned 90°, turned again and pitched),
comparing Phase 1 on against off.

| Level | Shot a | Shot b | Shot c |
| --- | --- | --- | --- |
| level1 (on vs off) | 0 px differ | 0 | 0 |
| level2 (on vs off) | 10 px, all ≤ 8/255 | 0 | 0 |
| noise floor: off vs off | level1 0 / 0 / 0; level2 10 px (one > 8/255) / 0 / 0 | | |

level2 shot a differs by distant particles, exactly as much as two "off" runs
differ from each other. The GL tests compare paths exactly, pixel for pixel
and sample for sample, with small stated tolerances where cluster order flips
a coplanar depth tie.

## Known gaps / notes

- **No LOD.** A cooked mesh in view draws every triangle at any distance
  (Phase 2). The guide's scenery page said otherwise ("costs by how much is on
  screen, not by how many triangles"); that text is corrected.
- **Sun-shadow GPU time is up** 0.15 to 0.3 ms on brush and compiled levels
  under the GPU cull, though CPU time drops more. Not investigated further. A
  first suspect is per-group compute dispatches for many small groups per
  cascade.
- **The cooked-mesh colour pass is about 0.5 ms behind classic** on the
  all-in-view rock bench.
- **The shadow cone only applies to closed glTF meshes.** `.fmdl`/`.obj` cooks
  are double-sided and carry no cones at all. On the very bumpy sample rock it
  drops 7-8% of triangles; on `bench_rock` 17% of the shadow time.
- **Cooked meshes draw opaque** (no sorted translucency) and static (no
  skinning). Collision is still the bounding box (Phase 2b).
- **`.fbsp` is version 8:** every compiled level in the game project must be
  recompiled. The bench copy was. The user's own project files were not
  touched.
- **Mesa D3D12 workarounds:**
  - Finish-before-readback in the engine.
  - Terrain-probe finish in the GL test.
  - No per-pass orphaning in the cull.

  The first two hang at `c4009b6` too. Native Windows drivers never showed
  them.
- **Editor UI** (Cook Nanite Meshes, the Nanite panel group) is covered by
  unit tests but was not driven interactively in this run.
- **Scratch output** is outside the repo, in
  `%LOCALAPPDATA%\Temp\foundry-bench-n1f` (a 1.1 GB project copy plus exes).
  Safe to delete.
