# Nanite Phase 5: software raster and virtual shadow maps

Status: **complete**, with the measured costs and gaps listed at the end.
Nothing is committed; Phase 5 is in the working tree on top of `7106cad`
(phase 4). Other sessions' uncommitted work was in the tree throughout
(installer/release/setup, levelio version stamping, a music tracker in
`audio`/`editor`/`script`/`tracker`, and the sun-shadow cascade change in
`shadow.go`/`fragment.glsl`/`biome_*.glsl`). Where this phase had to edit one
of those files (`shadow.go`, `fragment.glsl`, `biome_light.glsl`) its hunks are
separate from theirs and theirs are unchanged: the cascades' texel matching,
the 0.25 blend band and the rest of their work stand, and are what a frame
without the virtual map draws.

Phase 5 covers components 7 (the compute rasteriser) and 10 (virtual shadow
maps). It also rewrites the command packing's prefix sum, which the
rasteriser needed and which turned out to be worth a millisecond on its own.

## Findings on the way

- **Mesa's D3D12 driver breaks a program's cascades if it has a second
  comparison sampler array.** With `vsm.glsl`'s atlas declared as a
  `sampler2DArrayShadow`, `fragment.glsl`'s `shadowMapArray` read as lit
  everywhere — with the virtual map off, and whether or not the second sampler
  was ever reached (a branch returning a constant instead of the call brought
  the shadows back). So the atlas is a plain `sampler2DArray` compared by hand
  (`vsmCompare`: a `textureGather` and the bilinear weights a comparison
  sampler uses). Natively on NVIDIA the comparison sampler worked; the hand
  comparison is what runs everywhere.
- **On Mesa D3D12 the cascades do not shadow the biome terrain** of the
  composite level (Phase 4's `phase4_composite`): its 12 m wall casts nothing
  on the ground there, at HEAD too. Natively it does (`n5k_shots0`). The
  virtual map's shadow reaches the terrain on both. Not fixed here; it is the
  cascade path and driver-specific.
- **The cascades leave a ring pattern of partial shadow ("acne") on lit
  ground** at a 40° sun in the test scene: 72-81% of the open ground darkened
  by more than 4 levels, Mesa and native alike, and on a compiled floor too.
  The virtual map's slope- and texel-scaled bias leaves 0.3-0.7%
  (`TestVSMShadowsWhereTheCascadesDo`). Measured, not changed.
- **Mesa's D3D12 driver hangs in `glBufferData` after a frame that draws many
  pages.** With the virtual map on, headless runs of the composite level hung
  in the foliage cull's `glBufferData` (8 of 9 runs; none with the map off;
  none natively): a frame that draws many pages walks the scene once a
  rectangle, each walk with its own cluster culls of the streamed cooked
  meshes, and with that much in flight the driver's re-specification never
  returned — the family Phase 1 met (`passStore`). Turning the streamer, LOD,
  the Nanite shadow culls or persistent mapping off each avoided it; so does
  waiting for the GPU after a frame's pages are drawn, which the map now does
  on that driver only (`FOUNDRY_NANITE_VSM_FINISH`, `auto`; 6 of 6 runs clean
  since). A frame that draws no page does not wait.
- **The command packing's scan was the expensive part of packing.** Phase 2
  measured packing at a millisecond on the rock field and turned it off for
  dense views. Its prefix sum ran one workgroup per material group, walking
  the group's tiles 64 at a time — on the rock field, one group of 43,000
  tiles on one multiprocessor. As a parallel scan (below) packing costs about
  0.1 ms, and packing the cooked meshes' camera passes always made the rock
  field behind its wall a millisecond faster before anything else here.

## What was built

### The compute rasteriser (`render/nanite_swraster.go`, `nanite_swraster_comp.glsl`, `nanite_swresolve_*.glsl`)

Only in a visibility-buffer frame (Phase 4), only for cooked meshes:

1. **Routing**, in the cluster cull (`nanite_cluster_common.glsl`, `swTake`).
   In a camera pass the rasteriser was admitted to, a cluster that is drawn
   and whose sphere is under `FOUNDRY_NANITE_SWRASTER_PX` (32) pixels across
   — measured at its nearest view depth, which must be past twice the near
   plane, so no corner needs clipping — goes onto the batch's list (an
   atomic append, up to `naniteSWListMax` = 65,536; past it the hardware draws
   the rest) instead of into a command. Both the cluster tiles' stage and the
   range tiles' stage do this (a range tile's join may still cover a cluster
   the list took: drawn twice, harmlessly). Its history and streaming
   feedback are a drawn cluster's either way.
2. **Which groups**: the batch's owner marks them (`cullBatch.setSWGroups`).
   The cooked-mesh pool marks a group when its material is what the ID pass
   draws (`prepassable`), has no per-surface alpha or blend, and its texture
   has no texel under full alpha (`textureOpaque`: one `glGetTexImage` per
   texture, the first time a pool rebuild asks, cached by GL name) or no
   texture — since the rasteriser decides no coverage. Compiled surfaces and
   chunks are never routed: their groups have a buffer each (one dispatch
   reads one set) and their triangles are big.
3. **Admission** (`cullBatch.admitSW` → `naniteVisPass.admits`): before the
   cull lists anything, the same question `drawBatch` asks — bins, records,
   storage-block sizes — with the list's records counted in. A cluster listed
   for a batch the frame then refused would be drawn nowhere.
4. **Raster**, right after the batch's hardware ID draws: 512 workgroups of
   128 threads, each workgroup taking every 512th listed cluster. The first
   thread writes the cluster's record (reserved after the batch's commands'
   records) and builds its `projection · camera · model`; then a thread per
   triangle snaps its corners to 1/256 pixel, walks its box with 32-bit
   integer edge functions (inclusive edges: never a crack), interpolates the
   window depth and writes the nearest. With **64-bit atomics**
   (`GL_NV_shader_atomic_int64` with `GL_ARB_gpu_shader_int64` or
   `GL_NV_gpu_shader5`, detected in `glext`): `atomicMax` of
   `(0xffffffff - depthbits) << 32 | ID` into one word a pixel. **Without
   them**, asked for: two passes of 32-bit atomics, the key then the ID where
   the key won. Ties go to the larger ID in both. Back faces are drawn, as the
   ID pass draws them, except on closed meshes.
5. **Resolve**: a quad over the screen box the raster wrote in (gathered per
   workgroup in shared memory, read back through a buffer texture by the
   vertex stage) writes each covered pixel's depth (`gl_FragDepth`) and ID into
   the scene target under the depth test and sets its stencil to 1 — the ID
   pass's own rule — clearing the words as it reads them.

Everything after — the Hi-Z build between the two occlusion passes, the
material pass, TAA's motion, the later passes' depth — reads the scene target
and cannot tell which rasteriser drew a pixel.

**The ID invariant is checked.** `naniteVisClusterTris` (128) times
`naniteVisMergeClusters` (32) must be exactly the 12 bits of triangle, and the
clusterer's `DefaultMaxTriangles` must fit: constant expressions that fail the
build. Every cluster uploaded to a cull goes through `noteClusterTris`; one
over 128 triangles (a cook or compiler option could make one) turns the
visibility buffer off for the run, logged, rather than number a triangle into
its neighbour's record.

### The packing's prefix sum (`nanite_compact_comp.glsl`, `cullBatch.compactCommands`)

`COMPACT_SCAN` (a workgroup a group, serial over its tiles) is replaced by
`COMPACT_BLOCKS` (each 256 tiles' exclusive prefix and total),
`COMPACT_BLOCKSCAN` (the blocks' prefixes, one workgroup, 256 at a time) and
`COMPACT_FINISH` (each tile's prefix less its group's first tile's — the
group is now in `tile.b.w` — and each group's count). The order of the
packed commands is exactly what it was, so captures stay deterministic.
Since it is cheap, the cooked meshes' camera passes are now always packed
(`RenderNaniteScenery`); shadow passes keep Phase 2's rule.

### Virtual shadow maps (`render/vsm.go`, `vsm_pages.go`, `vsm_track.go`, `vsm.glsl`, `vsm_mark_comp.glsl`)

**The virtual map** is a clipmap of 11 levels in light space, level 0 two
metres across and each next twice that (the last 2 km), each 32×32 pages of
128² texels: 4096² a level, where the three cascades were 2048² each. A point
reads the level whose texel is about its own pixel's footprint
(`vsmLevelFor`: by distance from the camera, so turning on the spot keeps
every point on its page; `FOUNDRY_NANITE_VSM_RES` scales it). Each level's
page grid is fixed in light space; the window of 32×32 pages the page table
can address slides with the camera a page at a time.

**Pages** are physical only where asked for (`vsmPager`): a pool of 2048
pages (`FOUNDRY_NANITE_VSM_PAGES`), each a layer of two 128²×N depth arrays —
the page's **static layer** (compiled surfaces, chunks, terrain, props
classic and cooked) and its **whole depth** (the static layer with the
movers — entities, sprites, scatter, foliage — drawn over it), which the
shaders read. Allocation takes a free layer or evicts the least recently
requested page (never one requested this frame); coarse levels are served
first so a starved request falls back to a page that exists.

**Requests**: after the opaque world, `vsmMarkReceivers` runs a compute pass
over the frame's depth (the scene target's): each pixel's world position,
from the jittered inverse view-projection, asks for the pages its lookup's
filter (and normal offset) reaches on its level and the next coarser one, as
bits (a 32-page row a word) gathered in shared memory. The bits are copied
into a readback ring with a fence (Phase 3's `readbackSlot`) and read without
waiting a frame or two later, oldest fence first. The CPU adds a coarse level
(9: 32 m pages, 25 cm texels, the far cascade's) over the whole view to the
shadow distance, so a receiver whose own pages have not been asked for yet
reads a coarse shadow, never none.

**Drawing** (`vsmBegin`, inside `RenderShadowPass` where the light's cascades
would be drawn): requested pages whose static layer is stale are drawn
whole; requested pages whose whole depth alone is stale have their static
layer copied in and only the movers drawn. Pages go a rectangle at a time
(≤16×16 pages of one level, one per 16×16 block of the window), into a
2048² scratch target with an orthographic view of the rectangle and depth
clamp on (casters toward the light past the window still cast), and are
copied out (`glCopyImageSubData`) into their layers. The static walk is the
cascades' own — `renderSceneShadowLayers`' static half (the compiled
surfaces' GPU cluster cull, the chunks' GPU path, terrain), then
`RenderSceneryShadow` and `renderNaniteSceneryShadow` (the cooked meshes'
cluster cull, with the camera's LOD cut) — against the rectangle's frustum.
The movers' walk draws the frame's entities from a list the tracker made
(their attachments' matrices, worked out once a frame), each only where its
footprint across the light touches the rectangle, then the sprite, scatter
and foliage walkers if their footprints do.

**What changed** (`vsmTracker`, the point-shadow cache's question asked of
the whole level): every chunk, entity, prop and the casting sprites are
hashed on what their shadow draw reads; one whose hash changed, appeared or
went hands in its old and new world boxes, as static (chunks, props) or
movers (the rest). A posed (skinned) entity counts as moved every frame
rather than have its bones hashed; scatter and foliage, which sway and thin
by distance from the camera, hand in their boxes every frame while they cast.
The boxes' footprints across the light mark pages stale on every level,
looked up through each level's window (`vsmPager.slots`). Anything global —
the level, the brush mesh generation, the compiled level and what of it is
hidden, the casting switches, the light's direction, the depth window's
centre (snapped to 256 m) — marks everything stale. So does a frame drawn
without the map in between (a lapse: the switch, the light, the editor's
own view).

**Reading** (`vsm.glsl`, spliced into the world program by
`gfx.WorldProgramSources` and into the biome, sprite and foliage programs by
`biomeLightSnippet`): `calcDirShadowUnfaded` and `biomeDirShadow` answer from
the map for the light it shadows (`vsmLightSlot`, the light's index + 1, so a
program nobody told reads 0, none). A point's level, else the nearest coarser
level whose pages its filter needs are all resident, else lit; nine bilinear
taps a texel apart, each from its own page; normal offset and depth bias in
the level's texels, the bias growing with the slope. The page table is an
R32UI array (32×32×11, uploaded each frame); the atlas and table sit on
texture units 32 and 33.

**What the cascades did that it keeps:**
- **The baked sun on compiled surfaces.** `fragment.glsl`'s `bakedSunLight`
  path calls `calcDirShadowUnfaded`, which answers from the map, so a baked
  surface swaps the bake's sun share for the live shadow within the shadow
  distance exactly as before; `vsmShadowPass` sets `BakedSunCast` when the map's
  light is the baked sun (it is chosen first).
- **The shadow distance and its fade** (`dirShadowReach`, 300 m).
- **The cloud shadow**, multiplied on top, untouched.
- **The cascades themselves**, for every frame without the map: MSAA and
  "off" anti-aliasing (no scene target, so no depth to mark from), the
  editor's own view, `FOUNDRY_NANITE_VSM=0`, and every other shadowing
  directional light of a frame that has the map. Point-light cubes are
  unchanged.

The marking needs the frame's depth in a texture, which only the scene target
(TAA) has; hence the cascades in the other modes.

### Smaller changes

- `glext.Int64AtomicsAvailable` / `DisableInt64Atomics`.
- `DebugStats`: `NaniteSWMode`, `NaniteSWClusters` (with
  `FOUNDRY_NANITE_STATS=1`), `VSMOn`, `VSMPages`, `VSMDrawn`, `VSMRects`,
  `VSMStarved`; the F3 overlay's `Nanite raster:` and `Sun shadow: virtual`
  lines. Profiler sections: `nanite: sw raster`, `nanite: sw resolve` (under
  `nanite: vis`), `vsm` (under `shadows: sun`, with the walkers' own
  sections inside), `vsm: mark`.
- `app/bench.go` reports whether the map and the rasteriser ran and the
  map's page counts.
- Docs: guide 07 (the sun's shadow), 14 (cooked props), 19 (flags, overlay,
  profiler rows).

## Flags

| Flag | Default | Effect |
| --- | --- | --- |
| `FOUNDRY_NANITE_SWRASTER` | `auto` | `auto`: the compute rasteriser with 64-bit atomics, off without. `1`: 64-bit, else the two-pass 32-bit form. `32`: the two-pass form always. `64`: 64-bit or off. `0`: off. |
| `FOUNDRY_NANITE_SWRASTER_PX` | 32 | The largest cluster (sphere diameter, pixels) it takes; capped at 64. |
| `FOUNDRY_NANITE_VSM` | on | Off: the sun's three cascades, as before. |
| `FOUNDRY_NANITE_VSM_PAGES` | 2048 | Physical pages (64 KB each, twice: the depth and the static copy). |
| `FOUNDRY_NANITE_VSM_RES` | 1 | Texels a screen pixel across (0.5: a quarter of the pages). |
| `FOUNDRY_NANITE_VSM_FINISH` | `auto` | Wait for the GPU after a frame's pages are drawn: `auto` on Mesa's D3D12 driver only (see Findings), `1` always, `0` never. |
| Phase 0-4 flags | unchanged | |

## Tests

| Test | What it checks |
| --- | --- |
| `render/nanite_swraster_gl_test.go` | **`TestSWRasterDrawsTheHardwarePicture`** (Phase 4's parity scene, 32-bit form, three poses at full detail and three with the LOD cut — cluster and range tiles): 64-290 clusters a pose rasterised in compute; the IDs cover exactly the hardware's pixels and the picture is the hardware's (worst channel difference 0-1). Mutation-checked: an exclusive edge rule leaves 1-3 cracks a pose past 100 levels and fails. **`TestSWRaster64BitFormDrawsTheHardwarePicture`**: the same against the 64-bit form — skipped on Mesa D3D12 (no 64-bit atomics), **run natively on the GTX 1070** (cross-built test binary): coverage identical, worst difference 0-1 against the hardware and 0 against the 32-bit form. **`TestOversizedClustersRefuseTheVisibilityBuffer`**: the 128-triangle guard. |
| `render/nanite_compact_gl_test.go` | **`TestNanitePackingKeepsEveryLiveCommandInOrder`**: 48 cooked rocks, 528 range tiles in 3 groups (three scan blocks), two poses: each group's packed commands are exactly the fixed list's live ones in order, and its count their number. Mutation-checked (dropping the blocks' prefix fails it). |
| `render/vsm_pages_test.go` | The pager without GL: levels cover their windows and the shadow distance; windows centre on the camera; requests allocate, draw once and cache; a static change redraws its pages (and the coarser page over them) whole; a mover redraws only its pages' movers; LRU eviction never evicts a page requested this frame (starves instead); requests outside the window are ignored; a big box at a fine level; rectangles cover every page within the scratch; a page the window left is redrawn on return. |
| `render/vsm_gl_test.go` (through `DrawWorld`) | **`TestVSMShadowsWhereTheCascadesDo`**: a cube, a pole and posts on a slab, two poses: the full shadows (past 48 levels) overlap the cascades' by 0.95, and of the open ground 0.3-0.7% is darkened at all (the cascades: 72-81%). **`TestVSMShadowStaysSharpFarAway`**: a wall's shadow edge looked down on from 4 m and from 120 m (5° lens): 2-3 px wide under the map either way, 25 and 76 px under the cascades. **`TestVSMCachedPagesMatchAFreshMap`**: a still frame draws no page; moving a chunk, a classic prop, a cooked rock (static layer) or an entity (movers over the static copies) redraws 10-20 of 161 pages, and a frame drawn without the map in between redraws all 161; each time the picture equals a map drawn from nothing (0 px past 8). Mutation-checked: not copying the static layer in for the movers fails the entity case (1,147 px). **`TestVSMShadowsBakedSurfacesLikeTheCascades`**: a compiled floor baked with the sun's whole share, live casters on it: the swap takes the same shadow out (overlap 0.93). **`TestVSMKeepsTheCloudShadow`**: a cloud deck darkens the ground identically under the map and the cascades (50.4 vs 50.4). **`TestVSMLeavesForwardFramesOnCascades`**: an MSAA frame does not use the map and does not change with its switch. |
| Phase 0-4 tests | Unchanged and passing; `pinNaniteFlags` pins the rasteriser off for them (it would run natively otherwise). |

**Runs on this box** (WSL2, Mesa 26.2 D3D12 on the GTX 1070): see "Final
checks" below for the suites. **Natively** (the render test binary
cross-built and run on Windows in `%LOCALAPPDATA%\Temp\foundry-p5test`): the
rasteriser, packing and virtual-map tests above all pass, including the
64-bit form.

## Visual parity (headless, Mesa D3D12, 640x360, `-gi off`)

Worst channel difference in straight colour, and where pixels differ, which
way (+: the virtual map brighter). Captured from the bench copy with this
tree's shaders, `foundry play` (16 TAA frames a shot).

| Scene | Virtual map vs cascades | What differs |
| --- | --- | --- |
| level1 compiled spawn / turned / hangar | 5, 0, 44 px past 8; worst 9, 4, 23 | almost nothing: indoors, little sun |
| level2 compiled spawn / turned / `0,8,10` | 50k, 48k, 32k px past 8; none past 32; worst 32 (+6.6 levels) | the cascades' self-shadowing ring on the canyon walls, gone |
| rock field from above / near the wall | 86k, 92k px past 8; 501, 8 past 32; worst 54, 37 (+14, +8) | the rocks' sunlit faces lose the cascades' self-shadowing; every rock shadow is in both, sharper under the map |
| composite, three poses | 24k, 21k, 48k px past 8 (−20 to −26) | the terrain's shadows (the wall's, the rocks', the ship's): Mesa's cascades draw none there, the map does (see Findings) |

| Scene | Compute rasteriser (32-bit form) vs hardware | |
| --- | --- | --- |
| rock field from above, and from 60 m | 18 and 28 px past 8, worst 17 and 25 | single pixels where a tie between two triangles at one depth goes the other way |
| composite | identical (worst 5, particles) | its few rocks near the camera |

Natively the 64-bit form was checked by the GL tests (identical coverage,
worst 0-1 against the hardware); the native composite screenshots
(`n5k_shots0`/`n5k_shots1` in the bench copy, cascades / virtual map) show
the same shadows in the same places.

## Measurements (native, GTX 1070)

**Setup.** As Phases 1-4: `foundry-nanite5.exe` cross-built from this tree,
1600x900, TAA (the default), 90 warm-up + 240 measured frames a pose, the
bench copy `%LOCALAPPDATA%\Temp\foundry-bench-n1f` (its shaders replaced
with this tree's; the Phase 4 ones kept in `resources/shaders.p4`),
`SDL_AUDIODRIVER=dummy`, GPU idle before. Raw reports:
`docs/nanite/phase5_bench/` (`n5j_*` the final matrix, `n5k_*` the extras,
`n5e_*`/`n5g_*` earlier builds discussed below).

**base** = both new paths off (the Phase 4 frame, with Phase 5's packing).
**vsm** = the virtual map. **sw** = the compute rasteriser (64-bit). **both**
= the new defaults.

| Scene / pose | Frame avg ms base / vsm / sw / both | Sun shadow GPU ms base → vsm | `nanite: vis` GPU base → both |
| --- | --- | --- | --- |
| level1 spawn | 7.87 / 8.08 / 7.97 / 8.08 | 0.34 → 0.16 | — |
| level1 hangar | 8.48 / 9.13 / 8.59 / 9.14 | 0.36 → 0.59 | — |
| level1 ground | 2.96 / 3.19 / 2.99 / 3.17 | 0.15 → 0.06 | — |
| level2 spawn | 9.45 / 9.65 / 9.24 / 9.77 | 0.81 → 0.70 | — |
| level2 `0,8,10` | 9.36 / 9.87 / 9.41 / 9.80 | 0.81 → 0.83 | — |
| level3 spawn (its moon casts nothing) | 13.82 / 13.77 / 13.85 / 13.84 | — | — |
| **rock field behind the wall** | **13.88 / 6.72** / 14.07 / 6.77 | **7.77 → 0.04** | 1.07 → 1.10 |
| **rock field from above** | **16.69 / 10.08** / 16.33 / **9.78** | **7.56 → 0.03** | **3.41 → 2.92** |
| composite | 13.55 / 13.10 / 13.66 / 13.19 | 1.46 → 2.30 | 0.33 → 0.41 |
| **flight**, level2 (four poses, camera moving) | 7.66 / 7.97 | 0.66 → 0.54 | — |
| **flight**, rock field | 16.03 / 8.99 | 9.01 → 0.97 | — |

Pages a frame (the map's): level1 19 drawn of 231 resident (spawn), 101 of
551 (hangar: animated mechanics), 2 of 690 (ground); level2 77 of 419 and
101 of 527; rock field 0 of 356 and 563; composite 454 of 624 (its grass
sways, so its pages redraw their movers every frame); flights 31 of 1,417
(level2) and 5 of 774. Never a starved request.

**What it says:**

- **The virtual map caches what the cascades redrew.** The rock field's
  sun shadow — 256 dense rocks through three cascades every frame — cost 7.6
  to 9 ms of GPU; drawn once into pages and kept, it costs 0.03 ms, and the
  frame time halves (13.9 → 6.7 ms behind the wall, 16.7 → 10.1 from above,
  16.0 → 9.0 flying). Where little casts and much moves the map costs a
  little more than the cascades: +0.2 to +0.65 ms on level1 and level2 — the
  marking pass (0.06-0.17 ms), the movers redrawn round animated characters,
  and on level2 about 0.5 ms more in the material pass, where nine
  hand-compared taps across pages cost more than the cascades' hardware
  filter. Its CPU cost is below the cascades' on level2 (0.85
  vs 0.95-0.99 ms), about level with them on level1 (0.17-0.33 vs
  0.15-0.25 ms) — after the fixes below.
- **The compute rasteriser beats the hardware on dense views** — the
  visibility pass 3.41 → 2.92 ms on the rock field from above, its frame 10.08
  → 9.78 ms with the map, 16.69 → 16.33 without — and is idle elsewhere: no
  level has cooked meshes but the rock field and the composite, and the
  composite's few rocks near the camera make it about even (+0.09 ms).
  The two-pass 32-bit form, natively, is slower than the hardware (11.94 vs
  10.08 ms from above: it rasterises everything twice), which is why it is
  not what `auto` picks without 64-bit atomics.

**Measured on the way** (kept because they set the design):

| Change | Before → after |
| --- | --- |
| Packing camera passes, fast scan (rock field behind the wall, frame) | 7.77 → 6.73 ms |
| Compute rasteriser threshold 8 / 16 / 32 px (rock field from above, frame, packed) | 10.03 / 9.68 / 9.76 ms vs 10.07 hardware |
| Compute rasteriser before the packing rewrite | no win: the hardware's walk of 86,528 mostly-empty command slots stayed |
| A pixel's write as a plain store / no write at all (rock field from above, raster) | 1.48 / 1.36 ms vs 1.48 atomic: the atomics are not the cost |
| The map's CPU on level2 `0,8,10`: first build / static-dynamic split / window index / mover lists | 7.7 → 11.4 → 2.0 → 0.85 ms (the split alone did not help: half the time was invalidation walking the page map, which the window index removed) |
| The lookup as four gathers with a local array / without the array / nine gathers (level1 spawn, material pass, map **off**) | 8.66 / 4.18 / 3.24 ms — the lookup's shape costs every pixel of the world shader whether the map is on or not |

## Known gaps / notes

- **Requests arrive a frame or two late.** A newly visible region reads the
  next coarser resident level (or the coarse safety level) for those frames:
  a softer shadow for a moment, never a missing one inside the shadow
  distance. Flying measured no starvation and smooth frame times, but the
  softness is there.
- **One light.** The map shadows the baked sun, else the first shadowing
  directional light; any other shadowing sun of the same frame keeps three
  cascades.
- **Only TAA frames.** The marks come from the scene target's depth; MSAA,
  "off" and the editor's own view draw the cascades.
- **Swaying casters redraw their pages every frame** (scatter, foliage),
  and a skinned character counts as moved every frame. Cheap with the static
  copies, but a level carpeted in casting grass redraws most of its pages'
  movers each frame (the composite: 454 a frame).
- **A turning sun redraws everything** each frame it turns (a time-of-day
  script): every requested page, whole.
- **Cached static pages keep the LOD cut they were drawn with.** A cooked
  mesh's cut follows the camera; a page drawn from farther away keeps the
  coarser cut until something static in it changes. The cut's error is
  under a pixel of the camera's view, so this is a sub-pixel difference in
  the caster, but it is a difference.
- **Memory**: 2048 pages × 64 KB × 2 = 256 MB, plus a 16 MB scratch (set
  `FOUNDRY_NANITE_VSM_PAGES` lower; the levels here used at most 1,417).
- **The compute rasteriser is cooked meshes only**, opaque groups only, and
  needs 64-bit atomics to be on by default (NVIDIA's GL has them; Mesa's
  D3D12 does not). A texture edited live in the editor keeps its cached
  opacity until the texture is reloaded.
- **Its wins are modest** on this GPU: the LOD cut already keeps cooked
  triangles at a few pixels, which the hardware handles well; the rasteriser
  pays off where triangles are pixel-sized and many.
- **Mesa D3D12**: the page-drawing frames wait for the GPU there (above);
  on a level whose movers redraw pages every frame (the composite's grass)
  that is a wait every frame, on that driver only.
- **Scratch output** in the bench copy, safe to delete: `n5*` reports, logs
  and profiles, `n5*.sh`, `_n5*.cmd`, `foundry-nanite5*.exe`, `n5k_shots*/`,
  and `resources/shaders.p4` (the Phase 4 shaders). The native test copy
  `%LOCALAPPDATA%\Temp\foundry-p5test` can go too.

## Verifier's fixes

- **An entity's movers box now covers its meshes as well as its bounds**
  (`vsm_track.go`). It was the authored bounds alone when there were any; an
  attachment reaching past them (a rifle, a turret's barrel) was left out of
  the rectangles it fell in and, a page being a metre or two across at the
  fine levels, its shadow could be left behind when it moved. The box is now
  the union of the bounds and the meshes' padded extent.
- **The last sprite or emitter going away now redraws its pages.** The
  tracker only compared the sprites' hash while the sprite pass was live, so
  when a level's last emitter was removed, its particles' shadow stayed in
  the cached pages.

Verifier's runs: `go build/vet/test ./...` clean; render GL suite passes in
the default mode, with `FOUNDRY_NANITE_VSM=0 FOUNDRY_NANITE_SWRASTER=0`, with
`FOUNDRY_AA=msaa` and with `FOUNDRY_NANITE_SWRASTER=32`; gfx, devui and editor
GL pass, harness fails only the known `TestAPlaySessionDrawsTheRoomItIsStandingIn`.
Headless captures (640x360, `-gi off`, map on vs off) reproduce the table
above: level1 5 px past 8, level2 50k/32k px past 8 with none past 32, rock
field 86k past 8 and worst 54.

## Final checks

On this box (WSL2, Mesa 26.2 D3D12 on the GTX 1070), with the final tree:

- `go build ./...`, `go vet ./...`, `go test ./...`: clean.
- `FOUNDRY_GL_TEST=1 go test ./internal/render/... ./internal/gfx/...
  ./internal/devui/... ./internal/editor/... ./internal/harness/...`: all pass
  except the known `TestAPlaySessionDrawsTheRoomItIsStandingIn` (Phase 4
  diagnosed it: live GI's untraced probes leave the room black). Render takes
  about two minutes. (`internal/editor/imgui.ini` restored after.)
- The render GL suite also passes with `FOUNDRY_AA=msaa`, with
  `FOUNDRY_NANITE_VSM=0`, and with every Nanite switch off plus `msaa`.
- **Natively**, the whole render GL suite (191 tests), cross-built and run on
  Windows against NVIDIA's GL, passes — the 64-bit rasteriser test included.
