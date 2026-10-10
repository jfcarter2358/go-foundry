# Nanite Phase 6: Nanite foliage

Status: every required item is done, with the costs and gaps listed at the
end. One of the
spec's two *optional* items is done: crowns can feed the GI field, opt-in. The
other, trunk capsules for collision, is not, and the reasons are below.
Nothing is committed. Phase 6 is in the working tree on top of `22be53c`
(Phase 5). Other sessions' uncommitted work was in the tree throughout:
installer/release/setup, levelio, the music tracker, the cascade change in
`shadow.go`/`fragment.glsl`/`biome_*.glsl`, and Phase 5's own guide
paragraphs. None of it was touched. In guide pages 07 and 19 this phase's
hunks are separate paragraphs.

Phase 6 is UE 5.7-style Nanite foliage on top of the terrain foliage system,
in three parts:

- **voxels:** a second kind of DAG node;
- **assemblies:** plants as part templates placed by transforms;
- **skinning:** procedural bones posed by the wind on the GPU.

Placement, thinning and the unit tree are unchanged. Only what an instance is
drawn *as* changes.

## Findings on the way

- **Voxel coverage needs seven directions, not three.** The first version
  kept the area projected along the axes and mixed it by the ray's
  direction. For leaves facing every way, that over-states what a diagonal
  ray meets by up to √3. On a broadleaf row, against a 4x4-supersampled
  full-detail reference, the forest came out 10–30% too covered. The
  projected area along the four body diagonals is now kept as well
  (`cluster.VoxelDirs`), each over the cube's own cross-section that way.
- **Leaves overlap, walls don't.** A voxel's coverage is Beer–Lambert
  (`1−exp(−a)`) for its thin share and `min(a,1)` for its solid share
  (`nfCoverage`). Beer–Lambert is also what makes a coarse voxel's chance
  equal the two fine voxels a ray crosses in its place.
- **Mip-level grey.** Triangles far off read the leaf atlas at a high mip,
  where bleed-filled texels darken it. Voxels read the full-size atlas and
  came out 20–45% brighter where they took over. A voxel now reads the atlas
  at the mip the hardware would use for a card seen at the voxel's size, so
  the hand-over has no brightness step.
- **Mesa's D3D12 compiler and indexed local arrays.** The first bone pass
  kept a `mat4[64]` per thread indexed by parent. It took **103 s** to
  translate at first draw and ran at 37 ms a frame. It now reads each
  parent's pose back from the buffer it wrote: 0.5 ms, and 0.06 ms
  natively.
- **Point sprites don't beat boxes for bricks.** One vertex per brick,
  sized to the footprint, cost the same as 12-triangle boxes at 4 px and more
  above (natively, 4–12 px bricks: 15.5 vs 5.1 ms on the meadow). They are
  gone.
- **The compute raster's cost was idle lanes, not atomics.** Measured
  natively:
  - With the atomics off: 3.65 vs 4.11 ms.
  - With the brick placement off: 4.18 ms.
  - With no voxels at all: 0.11 ms.

  A workgroup per brick idled on empty voxels, and a thread per brick ran as
  long as its warp's densest brick. A brick pass that lists the occupied
  voxels, then a thread per listed voxel, took it from 4.1 to 1.3 ms. The
  splat footprint was also three voxel edges wide, writing about nine pixels
  per one-pixel voxel; it is now the cube's silhouette, point-sampled at
  pixel centres.
- **Casting the camera's cut was 3–4× the classic shadows.** Full-detail
  trees went into every virtual-map rectangle, and coarser cuts were worse,
  because voxels in the map's finest texels are expensive. Shadow passes now
  cast a **proxy**: the classic far mesh, bound to the same bones (Phase 4
  did the same for classic-path cooked meshes). Foliage shadow GPU went from
  2.95 to 1.0 ms on the forest floor.
- **Ground cover is not where Nanite foliage pays.** Thinned and grown grass
  tufts at one-pixel voxels made 480k bricks on the meadow view, several
  times the cost of their clump cards. By default the Nanite path takes the
  shapes that had impostors: trees, shrubs, rocks, deadwood, palms, cacti,
  acacias, jungle plants, dead bushes and ice. That is where the impostor
  pops and the hand-over band were. Ground cover keeps its tiers, and
  `FOUNDRY_NANITE_FOLIAGE_GROUND=1` puts it on Nanite as well.
- **The classic forest has a measurable band.** Pulled away past the
  impostor hand-over (115 m for broadleaf), the classic forest's picture
  drops by about a third relative to a supersampled reference:
  - Mesa: 1.15 → 0.82, shift −0.362.
  - Native: shift −0.346.

  Nanite's shift is 0.002 on Mesa and 0.038 natively
  (`TestNaniteFoliageKeepsTheForestsLookToTheHorizon`).

## What was built

### Voxels: a DAG node kind (`internal/cluster/voxel.go`, `dag.go`)

- **The brick format.** A voxel cluster holds up to 128 bricks of 4×4×4
  voxels on a lattice shared by the whole DAG (edge `Unit·2^k`). Only
  occupied voxels are kept. A brick stores its position, a 64-bit
  occupancy mask, its first voxel, and a binding with a weight: the bone it
  rides and its place along it.
- **What a voxel stores:**
  - projected area along 7 directions (coverage);
  - weighted area;
  - the normal distribution, as an area-weighted normal sum: its length over
    the weight is how much the normals agree;
  - the caller's attributes; foliage uses colour, accent and bark shares,
    thin share, blade share, tone and atlas grey;
  - the binding that contributed most area.
- **Exact areas.** Triangles are clipped into voxels slab by slab, so each
  voxel's area is exact. Each piece is supersampled for alpha and
  attributes.
- **Coarsening is exact.** A coarse voxel is the sum of its eight children,
  so area is conserved at every level.
- **`BuildDAG` options.** `DAGOptions.Voxels` lets a group's replacement be
  voxels when `VoxelError(size) = size·√3/2` is smaller than the simplified
  triangles' error, or when the triangles cannot simplify (a card soup). A
  group holding voxels stays voxels. The voxel size is the finest that fits
  half the group's clusters, and at least twice every voxel child's.
  - Partition gets a spatial fallback, because cards and voxel clusters share
    no corners.
  - The build continues past one cluster until the root is
    `RootBricks` bricks.
  - `DAGOptions.Base` fixes level 0 as the caller cuts it (the assembly's
    parts).
  - With neither option set, the triangle DAG is built exactly as before.

### Assemblies (`render/foliage_assembly.go`)

- **Recording, not rebuilding.** `recordFoliageAssembly` runs a generator
  with every primitive helper noting the range of the merged list it wrote.
  A **card** becomes a **part**: a unit card template per atlas tile and
  colouring, plus an affine transform (base, right, up), a normal, a tone
  and a bone. Everything else is the **core**.
- **Exact by construction.** Flattening the assembly gives back the classic
  merged mesh bit for bit, checked for all 54 variants.
- **Level 0 is the parts.** A variant's level 0 is its part clusters (up to
  64 parts of one template, along a Morton walk), plus core clusters. The
  **cluster cull places parts by their transforms**: a part cluster's
  command draws its template once per (plant, part), and the vertex stage
  applies the part's transform.

### Skinning (`foliage_assembly.go`, `nfoliage_bones_comp.glsl`)

- **Bones from the plant's structure.** Every axis primitive (tube, blade,
  frond, fern strip) is a bone from its first point to its last. Its parent
  is the earlier bone it grows out of: the nearest axis, measured along that
  axis rather than at its root. Bone 0 is a static root. Cards, stones and
  other pieces ride the nearest bone, at their place along it.
- **Base and own pose.** A vertex blends its bone's base pose `B` (the
  parent's blended at the joint, so branches never leave their trunk) with
  its own pose `M = B · rotation about the pivot`.
- **Calibrated to the old sway.** Each bone's bend is set so its tip moves
  what the classic sway moved it (`0.35·sway²` in a full gust), net of what
  its parents contribute; a per-bone flutter rides on top.
- **The per-frame pose pass.** A compute pass poses every variant's bones
  for 8 gust levels × 4 flutter phases × 2 wind directions (the plant's +X
  and +Z). That is about 5 MB of poses, written into one of two buffers that
  swap each frame.
- **Per instance.** An instance takes its own gust (the classic travelling
  wave) between two levels and its flutter phase from its seed. It mixes the
  two wind directions by how its frame faces the wind, which is exact to
  first order in the bend.
- **This replaces per-vertex sway on this path.** Last frame's poses give
  each vertex (and each voxel hit) its previous position. In a TAA frame the
  path writes its own **per-vertex motion** into the scene target's motion
  attachment. The classic path writes none, so its swaying branches
  reproject with the camera's motion.

### The renderer (`render/nfoliage*.go`, `nfoliage_*.glsl`)

`nfoliage_asset.go` builds all 54 variants once at start-up: about 0.5–0.9 s
of CPU on this 20-thread box, run in parallel. The result is 538 clusters
(124 triangle, 41 part, 282 voxel, 91 shadow-proxy), 1,503 parts, 7,959
bricks, 108k voxels and 421 bones, about 3.3 MB static plus 5 MB of poses.

Each pass runs these stages:

1. **Cull** (`nfoliage_cull_comp.glsl`): a thread per candidate instance, in
   the classic GPU cull's tiles. It applies thinning, frustum and Hi-Z to the
   plant. Then it applies the **per-cluster DAG cut**: own error within
   `FOUNDRY_NANITE_FOLIAGE_PX` pixels and the parent's not, using the
   camera's `naniteLOD` k. Each cluster in the cut is tested against the
   frustum and Hi-Z with its sphere padded for wind, and each survivor
   becomes an item.
2. **Scan** (`nfoliage_scan_comp.glsl`): a parallel prefix over the cluster
   commands' reference counts, clamped to capacity.
3. **Scatter** (`nfoliage_scatter_comp.glsl`): references `(item, part or
   brick)` into each command's run. There is no `gl_BaseInstance` in GL 4.3,
   so the instanced attribute says which part or brick an instance is.
4. **Draw:**
   - triangles and parts: two multi-draws, full and one-tap lighting, shaded
     per vertex as `foliage_vert.glsl` shades;
   - **voxel bricks:** ray-marched boxes (`nfoliage_vox_*.glsl`), a 3D DDA
     over the brick. Each voxel stops the pixel's ray with its directional
     coverage times the fade. The chance is drawn per pixel and per frame, so
     TAA averages it the way it averages cutout dithers. The normal is drawn
     from the voxel's distribution;
   - where the driver has **64-bit atomics**, bricks under 16 px skip the
     boxes and go to the **compute rasteriser** (`nfoliage_voxraster.go`): a
     brick pass places each brick and lists its occupied voxels, then a
     thread per listed voxel writes `atomicMax(depth<<32 | ref<<6 | voxel)`
     over its footprint, and a resolve over the written box shades each
     pixel once (`nfVoxelShade`, the ray march's own shading) under the depth
     test.
5. **Shadows** cast each variant's **shadow proxy**, culled per cluster with
   the light's frustum, on the same bones. Point lights are not cast into,
   as before.

Lists that run out of room are cut, never drawn as garbage, and grow from an
asynchronous readback a frame or two later.

### The GI canopy, opt-in (`render/nfoliage_gi.go`)

- **The canopy grid.** Each tree variant's canopy is its DAG cut at a tenth
  of its size: the trunk's triangles, plus the **outer faces** of the voxels
  with coverage ≥ 0.12. It is baked into a 24³ distance grid by
  `meshsdf.Bake`. Internal faces would make the whole crown "shell", with
  nothing inside.
- **Sources.** Every tree of a unit within 120 m becomes a GI mesh source,
  placed by its instance frame. Units keep their tall plants' placements on
  the CPU for this.
- **Effect.** The field reads −0.79 inside a crown with canopies and 1.0
  (open) without.
- **Off by default** (`FOUNDRY_NANITE_FOLIAGE_GI=1`). It makes a natively
  captured forest floor 18% darker overall: the mean colour went from 50.6
  to 41.8 with it on, and 50.8 with it off. That is a lighting change, not
  parity.

### Smaller changes

- **`foliage.go` / `foliage_gpu.go`:** the classic paths skip the shapes the
  Nanite path draws (`nan.takes`), and the GPU cull returns early with no
  tiles.
- **`foliage_mesh.go`:** recording hooks in the primitive helpers. They do
  nothing unless an assembly is being recorded, and the fern frond strip is
  declared as an axis.
- **`hiz.go`:** `release`.
- **`DebugStats`:** `NaniteFoliage`, items, references, bricks and overflow.
  The F3 overlay gets a `Foliage: Nanite …` line, and the bench reports a
  `foliage  Nanite …` line.
- **Profiler sections:** `foliage: bones`, `foliage: nanite cull`,
  `foliage: nanite draw`, `foliage: nanite voxels`, `foliage: voxel raster`
  and `foliage: voxel resolve`.
- **Docs:** guide 30 (terrain foliage), 19 (switches, overlay and profiler
  rows) and 07 (the opt-in GI canopy).

## Flags

| Flag | Default | Effect |
| --- | --- | --- |
| `FOUNDRY_NANITE_FOLIAGE` | on | Off: the classic far meshes, impostors and clump cards for everything, exactly as before. |
| `FOUNDRY_NANITE_FOLIAGE_GROUND` | off | On: grass, flowers, ferns, moss, pebbles, litter, reeds and vines on the Nanite path too. |
| `FOUNDRY_NANITE_FOLIAGE_PX` | 1 | The cut's error, in pixels. |
| `FOUNDRY_NANITE_FOLIAGE_SWRASTER` | `auto` | `auto`/`1`: small bricks by the compute rasteriser where 64-bit atomics exist (`1` logs if not); `0`: every brick ray-marched. |
| `FOUNDRY_NANITE_FOLIAGE_GI` | off | Tree crowns into the GI field. |
| `FOUNDRY_NANITE_FOLIAGE_FLUSH` | on for Mesa's D3D12 driver only | A `glFlush` before the camera pass and after each shadow pass (see *Independent verification*). `0` off, `1` on everywhere. |

## Tests

| Test | What it checks |
| --- | --- |
| `cluster/voxel_test.go` | A flat square: exact area, full coverage from above, a third along a diagonal, mean normal unit. Coarsening keeps all seven areas and the weight. Disagreeing normals give a short mean. A brick takes its leading binding. A card soup becomes voxels from level 1, errors are monotonic, the root is ≤2 bricks, every view cut crosses every path once, and fewer bricks are drawn further off. A closed sphere keeps triangles for its first levels. A lone cluster still coarsens. `Base` is level 0. A triangle-only DAG has no voxels. |
| `render/foliage_assembly_test.go` | All 54 assemblies flatten to the merged plant bit for bit. Trees have parts and bones, parents come first, grass blades hang off the root, and a broadleaf's twigs hang off branches. In a full gust the bones move the plant within 0.4–2.5× of the classic sway, with no rooted vertex moving and no child leaving its parent. Still air is the identity. |
| `render/nfoliage_asset_test.go` | The packed asset is consistent (indices, parts, bricks, voxels, bones, one root per variant, monotonic errors). Trees end in a one- or two-brick voxel root. A broadleaf's cut goes from 554 triangles to voxels to one brick as it recedes. |
| `render/nfoliage_gl_test.go` (Mesa and native) | **Near field:** the Nanite forest within the triangle range is the classic forest, 0 pixels differing past 8 levels. **The look to the horizon:** from 4 m to 400 m the forest's picture is within 0.8–1.25 of supersampled full detail (measured 1.01–1.16), never jumping more than 0.1 between 15% steps. Across the classic impostor hand-over Nanite shifts 0.002 against the classic −0.36, and a forest a kilometre off is drawn in voxels. **Bones:** the GPU's poses equal `poseFoliageSlot` within 1e-4. **Shadow:** in still air the proxy shadow is the classic shadow, IoU 1.0 and 0 texels at a different depth. **The split:** trees on Nanite and ground cover classic; ground cover on Nanite with its flag; nothing with the path off. **Lists grow:** lists of 32 items and 128 refs are cut, grow, and draw the roomy picture. **Motion:** in wind ~7,200 pixels carry their own motion, in still air none move, and the classic path writes none. **GI canopies:** every tree near the camera is a source, the canopy grid is solid inside, and the field reads −0.79 inside a crown against 1.0 without. **Compute rasteriser** (native only; skipped on Mesa, no 64-bit atomics): within 0.98–1.14 of the reference and within 0.03 of the march at every distance. |
| Existing foliage tests | Unchanged and passing with the path on and with it off. |

### Test runs

**On this box** (WSL2, Mesa 26.2 D3D12 on the GTX 1070):

- `go build ./...`, `go vet ./...` and `go test ./...` are clean.
- `FOUNDRY_GL_TEST=1 go test ./internal/render/... ./internal/gfx/...
  ./internal/devui/... ./internal/editor/... ./internal/harness/...` passes,
  except the known `TestAPlaySessionDrawsTheRoomItIsStandingIn`.
- The render GL suite also passes with `FOUNDRY_AA=msaa` and with
  `FOUNDRY_NANITE_FOLIAGE=0`.
- `internal/editor/imgui.ini` was restored afterwards.

**Natively** (the render test binary cross-built and run in
`%LOCALAPPDATA%\Temp\foundry-p6test`): all 208 render GL tests pass, the
compute rasteriser's included (`phase6_bench/render_gl_native.txt`).

## Visual parity

### Headless (Mesa D3D12, 640x360, `-gi off`, TAA-converged `foundry play` shots)

`zz_foliage`, classic tiers against the Nanite path:

| View | Mean colour, classic → Nanite | Pixels past 8 / past 32 levels |
| --- | --- | --- |
| Meadow, from the hill | 93.1/117.4/74.8 → 92.4/116.6/73.7 | 1.8% / 1.0% |
| Over the meadow to the broadleaf forest | identical means | 1.5% / 0.2% |
| Over the meadow to the pine forest | 94.4/119.0/79.3 → 93.7/118.2/78.2 | 1.8% / 1.1% |
| Broadleaf forest floor | 61.2/68.6/33.2 → 61.0/68.3/33.2 | 12.4% / 3.2% |
| Pine forest floor | identical means | 2.1% / 0.4% |

The differences are the bones' sway against the classic vertex sway (the
forest floor is mostly swaying leaves), and voxels against impostors in the
far forest. The means agree within a level. Natively the pine band's sky
coverage is within 3% of the impostors'.

The deliberate difference: the classic **far tier** draws ground-cover clumps
and tree impostors with a dithered hand-over band, measured at −0.35 of the
forest's picture across 115 m. On the Nanite path tree-like shapes have no
tier, so the band is gone (measured 0.002–0.04).

## Measurements (native, GTX 1070)

**Setup.** As in Phases 1–5:

- `foundry-nanite6.exe` cross-built from this tree, run in the bench copy
  `%LOCALAPPDATA%\Temp\foundry-bench-n1f` with this tree's shaders (Phase 5's
  are kept in `resources/shaders.p5`) and `zz_foliage.yaml` copied in.
- 1600x900, TAA, GI as picked by the GPU (medium), 90 warm-up and 240
  measured frames per pose, `SDL_AUDIODRIVER=dummy`.
- Raw reports are in `docs/nanite/phase6_bench/`.

Frame **p50** in ms. The averages include one shot frame, so p50 is the fair
number. Foliage GPU is the `foliage` section, plus its share under
`shadows: sun`.

| `zz_foliage` pose | Classic | Nanite (default) | Nanite, boxes only | Nanite + ground cover | Foliage GPU classic → Nanite | Foliage shadow GPU |
| --- | --- | --- | --- | --- | --- | --- |
| Meadow `0,17.9,0` | 8.86 | 12.09 | 15.55 | 14.73 | 1.86 → 4.29 | 0.18 → 1.00 |
| To the broadleaf forest `150,60,0` | 7.27 | 8.75 | 16.92 | 9.27 | 1.09 → 2.11 | 0.10 → 0.50 |
| To the pine forest `-100,50,0` | 7.14 | 8.84 | 20.35 | 9.78 | 1.65 → 2.93 | 0.13 → 0.50 |
| Broadleaf forest floor `-380,30,20` | 8.42 | 10.37 | 13.13 | 11.54 | 1.63 → 2.99 | 0.54 → 1.03 |
| Pine forest floor `300,25.7,0` | 9.11 | 10.69 | 11.55 | 12.32 | 1.37 → 2.26 | 0.96 → 1.58 |

Inside the Nanite figure on the meadow (4.29 ms): voxels 1.84 (raster 1.34,
resolve 0.49), triangles and parts 0.69, cull 0.20. Bones cost 0.06 ms a
frame.

| Other levels | Classic | Nanite |
| --- | --- | --- |
| level1 spawn / hangar (no terrain foliage) | 8.12 / 9.21 | 8.10 / 9.24 |
| Phase 4's composite level (a few trees, much grass) | 13.15 | 14.22 (CPU 2.30 → 3.08) |

**What it says:**

- **The Nanite forest costs 1.5–3.3 ms more a frame on the GTX 1070** than
  impostors did. The classic far tier is one quad per tree. Here a tree a few
  pixels across is still its voxels, at roughly a pixel a voxel. In return
  there is no band, no impostor pop, real geometry where the impostor began,
  and per-vertex motion.
- **The compute rasteriser is what makes voxels affordable.** Without it the
  same views cost 3–11 ms more. That is the fallback on drivers without 64-bit
  atomics: Mesa's D3D12, and many non-NVIDIA GL drivers.
- **Ground cover on Nanite** costs another 0.6–2.6 ms. That is why it is off
  by default.
- **A level with few trees pays a fixed price,** about +1 ms on the
  composite. Every virtual-map rectangle runs its own small cull, scan and
  draw, and each pass re-binds the programs' lighting and parameters, so the
  CPU cost is per pass.

**Measured on the way** (`phase6_bench/`):

| Change | Before → after |
| --- | --- |
| Voxels as boxes, all shapes (meadow: foliage / voxels) | 20.8 / 17.6 ms |
| Point sprites for bricks ≤ 48 px (meadow, voxels) | 30.6 ms |
| Compute raster, a workgroup per brick | 14.1 ms |
| Ground cover left to its tiers | 7.4 ms (242k bricks) |
| Brick-placement pass | 4.5 ms |
| A thread per brick | 4.1 ms |
| A brick pass listing voxels, a thread per voxel, true footprint | **1.3 ms** |
| Shadows: camera cut ×1 / ×2 / ×4 coarser (forest floor, foliage shadow GPU) | 2.0 / 2.1 / 3.0 ms |
| Shadows: part clusters flat instead of instanced | 1.97 ms (no gain) |
| Shadows: proxy (classic far mesh on the bones) | **1.0 ms** |

## Independent verification

A second pass over the phase re-ran everything and compared headless
captures against a build of this tree with Phase 6 taken out (the
`foliage*.go`, `gi_sources.go`, `hiz.go`, `world_frame.go`, `debug.go` and
`cluster/dag.go` of HEAD, the new files removed, the bench lines dropped).

**Found and fixed: a hang on Mesa's D3D12 driver.** With the path on and
TAA, `foundry play` on `zz_foliage` hung for good at the broadleaf forest
floor (`teleport -380,30,20; look 180 -10`): every run of six, the main
thread stuck in the camera cull's `glBufferData` of its commands. Natively
the same views pass (with the compute rasteriser and with boxes only). The
bisection, on Mesa:

- no hang with the shadow passes' culls left out, nor with the camera's
  triangle and part draws left out; it needed both;
- no hang with `FOUNDRY_NANITE_FOLIAGE=0`, with MSAA, or with
  `FOUNDRY_TAA_RESOLVE=0` (a single draw of the shot frame);
- still hung without the motion vectors, without Hi-Z, without the
  proxies' draw, with the cascades instead of the virtual map, and with
  `glBufferSubData` in place of the orphaning `glBufferData`;
- a `glFinish` after the shadow pass hung too; a `glFlush` there, or one
  ahead of the camera pass, ended it.

It is the family Phase 5 met (`NaniteVSMFinish`): too much queued on that
driver, then a call that waits on it never returns. The path now flushes
ahead of its camera pass and after each shadow pass on a renderer string
containing `D3D12` (`nfFlushD3D12`, `FOUNDRY_NANITE_FOLIAGE_FLUSH`), never
natively. After it, 12 of 13 runs through those views passed (seven
alone, the rest two processes sharing the GPU), against 1 of 2 without it.
The one hang was a run with `FOUNDRY_NANITE_FOLIAGE_GI=1` beside another
process; it passed alone and beside another on re-runs. So the flush
removes the reproducible hang, but the D3D12 driver is not proven
hang-free under this path.

**Captures (Mesa, 640x360).**

| Comparison | Result |
| --- | --- |
| `zz_foliage` four views, Phase 6 removed vs `FOUNDRY_NANITE_FOLIAGE=0`, TAA | max 3/255, none past 8 |
| the same under MSAA | one pixel past 8 |
| the same with live GI (medium) | max 2/255 |
| level1 compiled, Phase 6 removed vs on (no terrain foliage) | identical |
| `zz_foliage`, Phase 6 removed vs the Nanite path | 1.1–2.2% of pixels past 24 on the meadow and the far forests; the forest floor 13% past 8 (bones' sway against vertex sway), same picture by eye |

**Smaller.** `cluster.LODCluster`'s new `Kind`, `Voxel` and `Base` are
gob-encoded into cooked `.fnm` meshes too. A mesh cooked before them
decodes with all three 0, not -1; nothing outside the foliage reads them,
and the foliage tests `Kind` first. The field comment now says so.

## Known gaps / notes

- **Cost.** +1.5 to +3.3 ms per frame on the forest bench, and about +1 ms
  (mostly CPU) on a level with few trees. The fixed per-pass cost under the
  virtual map is the obvious next saving: one cull for all of a frame's
  rectangles instead of one each.
- **Ground cover keeps its tiers by default,** so the meadow's far
  clump-card hand-over is still there. The spec's done-when is about forests,
  which have no band. With `FOUNDRY_NANITE_FOLIAGE_GROUND=1` it goes too, at
  a cost.
- **Trunk capsules for collision are not done** (optional). The trunk shapes
  come from the renderer's generators, which `internal/collision` cannot
  import (layering). A body would collide with trees only in runs that had
  brought the renderer up: never in `foundry sim -draw=false`. Solid trunks
  would also block players and AI on existing forested levels, which the nav
  grid does not know about. That needs the generators to move below `world`,
  or a baked trunk table.
- **The GI canopy is opt-in.** It is correct (a crown is solid in the field),
  but it darkens forests noticeably.
- **Shadows do not use the camera's cut** (the Nanite rule from Phase 1).
  They use the classic far mesh on the same bones, as the classic path did.
  Soft foliage shadows show no self-shadowing mismatch in the captures.
- **Voxels approximate at one pixel.** Against supersampled full detail, the
  forest's picture is 1.0–1.16 of what it should be: a voxel's square
  silhouette is a little more than the leaves in it.
- **The bend is first-order in the wind direction.** The two-direction mix is
  exact for small bends, which is all the generators' plants make. Flutter is
  per bone per phase slot (four phases), so neighbours with the same seed
  slot shiver together.
- **Start-up:** building the 54 variants takes about 0.5–0.9 s of CPU on
  20 threads, every start. A cache keyed on the generator's code would remove
  it.
- **GPU memory:** about 3.3 MB of assets and 5 MB of poses, plus lists that
  start at 1M references (8 MB) and 128k items (2 MB) and grow on demand.
  The compute rasteriser keeps an 8-byte word per pixel (11.5 MB at
  1600x900), a voxel list that starts at 1M entries, and 80 B per small
  brick.
- **Stats and list growth arrive a frame or two late** (asynchronous
  readback). A frame that outgrows its lists draws less for that frame.
- **The voxel raster's splat is capped at 4×4 pixels per voxel.** Bricks
  over 16 px go to the boxes, so the cap is never reached.
- **Mesa D3D12** has no 64-bit atomics, so voxels are ray-marched boxes:
  correct, and slow on dense views.
- **Scratch output** in the bench copy, safe to delete: `n6*` reports, logs
  and scripts, `_n6.cmd`, `foundry-nanite6.exe`, the `n6*_shots*/` folders,
  `resources/shaders.p5` and `resources/levels/zz_foliage.yaml`. The native
  test copy `%LOCALAPPDATA%\Temp\foundry-p6test` can go too.
