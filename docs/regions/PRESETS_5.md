# Environment presets — step 5: draw distances and a far impostor tier

Status: 2026-10-14. Scanned Nanite scenery now has a per-placement draw
distance, shadow distance and — for trees — a far impostor tier, cross-faded
with a stable per-pixel dither; the region generator writes all of it by size;
the biome showcase was regenerated with it in the user's OneDrive project and
measured natively on the GTX 1070 before and after. **Every pose got 1.0×–3.7×
faster; none reaches 16.7 ms.** What remains is mostly not the scans (below).
Nothing was clicked in a running editor.

`$S` is this session's scratchpad,
`/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/9afc65f5-e4b2-4b3e-9788-2e06870a0755/scratchpad`.

## Design

### Windows (world/scenery_draw.go)

Four optional `Scenery` keys (YAML `draw_distance`, `shadow_distance`,
`impostor`, `impostor_distance`; all absent = drawn everywhere, as before —
existing levels load, save and render identically). What a placement draws
is a **window** of camera distance `[From, To)` per thing it draws:

| | window | shadow window |
| --- | --- | --- |
| mesh | `[0, impostor_distance)` with an impostor, else `[0, draw_distance)` | clipped to `shadow_distance` |
| impostor | `[impostor_distance, draw_distance)` | clipped; none if `shadow_distance ≤ impostor_distance` |

Distance is camera → the placement's **origin** (not its bounds' centre), so
a mesh and its impostor agree on it to the bit. Each window fades over the
last tenth before each end (`DrawFadeBand`), keyed by `DrawHash` — a hash of
the placement's position, shared by mesh and impostor, stable across list
reorders.

Two fades:
- **Per pixel** (visibility-buffer ID pass and the compute rasteriser): the
  vertex stage computes the instance's noise band `[lo, hi)`
  (`drawFadeWindow`), the fragment keeps a pixel whose
  `fract(coverageNoise(pixel, TAA frame) + hash)` is inside it. The mesh keeps
  noise `< f`, its impostor noise `≥ f` (the same `f`), so in the hand-over
  band every pixel is drawn by exactly one of them; TAA resolves the dissolve
  into a fade.
- **Per instance** (shadow passes; a forward camera pass; the classic path):
  drawn while `d < To·(0.9 + 0.1·hash)` and from `d ≥ From·(0.9+0.1·hash)` —
  each instance drops at its own distance in the band, no wave; mesh and
  impostor cut at one distance.

Shadows always cut per instance (a VSM page keeps what it drew). The CPU
decides it each frame (`instShadowHidden`) and the VSM tracker hashes the
same bit, so a caster crossing its shadow distance redraws its pages.

### GPU cull (render/nanite_draw_distance.go)

`gpuInstance` grew a `Draw vec4` (from, to, hash; 112 → 128 bytes; every
`Instance` struct in the shaders updated). The **instance stage** drops an
instance outside its window (`drawKeeps`, modes off / dither / hashed /
shadow) before the LOD walk, the live-tile list and every later stage: a far
bush costs one thread. The CPU's look-ahead (`inView` → draw bound) keeps a
superset (`placementMayDraw`, tested against the GPU's rule). An impostor is a
**second member** of the placement in `naniteSceneryRenderer` (`memberImp`,
its own mesh in the pool, no wind). The classic path (no GPU cull) takes the
per-instance cut on the CPU and in `scenery_cull_comp.glsl` (cuts carried in
the record's `lo.w`/`hi.w`); it draws no impostors.
`FOUNDRY_DRAW_DISTANCE=0` ignores all four keys (A/B).

### Impostors (internal/impostor)

A pure-CPU baker (no GL — a generator runs it headless): **9 cards** —
4 vertical planes through the trunk, each two one-sided quads back to back,
plus one horizontal card at 60 % height — each an orthographic view of the
cooked mesh's level 0, rasterised at 2× and resolved (coverage-weighted
colour, bleed into clear texels), with the cutout tested before depth like the
GPU. Per texel: albedo (× vertex colour/cavity) and the normal in the card's
**tangent space** (x = +u, y = +v down the image, z out), which
`fragment.glsl`'s `cotangentFrame` rebuilds from the card's UVs — so the
material pass lights the card as it lights the mesh (same shader, same VSM,
fog, TAA). Each card is its own slot → its own cluster, and its **normal
cone is rewritten** (`SetViewCones`) so the cull draws it only from within
35° of its normal (top card 65°): 1–2 cards from any side, the top card from
25°+ above. Impostor instances are also cone-tested from the light, so only
sun-facing cards cast. Alpha is rescaled so the cutout keeps the card's true
area coverage (`matchCoverage`), and the atlas is a `_cov` texture (coverage-
preserving mips).

Files beside the variant: `<stem>_imp.fnm`, `<stem>_imp.yaml` (provenance:
bake Version 3, source hash, options → rebaked only when stale), atlas
`textures/ph_imp_<stem>_cov.png` + `_normal.png` (3×3 cells of 256²).
~1.5–2.9 s a variant.

Why cards and not octahedral impostors: cards are ordinary cooked geometry,
so they go through the existing vis buffer / material pass / VSM unchanged and
are lit *by construction* like the mesh; an octahedral impostor needs its own
shading path. Iterations that were measured and replaced: all 9 cards drawn
(3–4 silhouettes stacked: dark solid cones) → view cones; all 9 casting
(self-shadowed into dark cones) → light-facing cards only.

### Generator (procgen/stages/scatter_draw.go)

Scatter rules gained `draw_distance`, `shadow_distance`, `impostor`
(auto/on/off), `impostor_distance` (all `Added`, so no existing stage goes
stale). In a **region world** (`Env.Region`) the auto values apply, from the
placement's bounding radius r (half box diagonal × scale):
draw `clamp(220·r, 120, 1500)` (impostor trees `≥ 800`), shadow `0.5×` draw
(impostor trees `0.6×`), impostor distance `clamp(25·r, 80, 300)`, impostor
auto = Poly Haven plant variants ≥ 3 m tall at the rule's largest scale.
**Classic documents write nothing unless a rule sets it** — the pinned
byte-identical tests (`TestClassicPlacementsPinned`,
`TestDesertCanyonHashesUnchangedByRegions`) forbid a default change there, so
the user's desert_canyon level regenerates exactly as before. `Placement`,
`ToRawScenery`, the level schema, `foundry check` (missing impostor / hand-over
past draw distance warnings) and the cooked-mesh prefetch carry the fields;
the packer already follows them (token scan + the .fnm's slot texture).

### CPU fixes found on the way

With the GPU unloaded the spawn pose went CPU-bound (25 ms). Three per-frame
walks over 14,800 placements were fixed: `fillSceneryXforms` recomputed every
matrix (now cached by position/rotation/scale, −2.7 ms); the Nanite scenery
signature hashed every slot name with fnv and a `[]byte` copy (now `maphash`,
−2 ms); `placedInstance`'s `coneSafe` ran per member per frame (cached by
matrix); the classic instancer's signature hashed the GPU-drawn placements'
CPU-only submeshes (skipped, −1.4 ms). Spawn CPU 25 → 19 ms.

## Regeneration (OneDrive project)

`foundry gen run biome_showcase_world -workers 8 -stage r_<region>_scatter ×11`
(1 m 40 s – 3 m 30 s a run, peak RSS 0.86 GB; scatter only — no cook_memory
stage ran). 11 impostors baked (fir_tree_01 a/b/c, fir_sapling_medium a/b/c,
quiver_tree_01 ×2, island_tree_01, jacaranda_tree, searsia_lucida_a);
2,803 placements carry an impostor, 11,736 a draw distance. Disk: **+14 MB**
(impostor .fnm/.yaml + 22 PNGs) and the level 5.4 → 6.2 MB; the existing
`biome_showcase.yaml.bak` was overwritten in place (no new .bak). `foundry
check biome_showcase -scripts biome_showcase`: **all clear** (only the
pre-existing collision-cap warnings).

## Native numbers (GTX 1070, 2560×1080, user graphics.yaml, warm 200 + 60 frames)

Raw: `docs/regions/perf_presets5/` (`before`, `final`; A/Bs `noscat`, `dd60`,
`imp150`, `impnoshadow`, `impcone`; intermediate `after1`, `after4`).
p50 wall ms:

| Pose | before | after | no-scatter floor |
| --- | --- | --- | --- |
| spawn (ground) | 59.5 | **23.0** | 16.7 |
| prairie ground | 52.8 | **22.8** | |
| pine ground | 91.7 | **36.6** | 24.0 |
| desert ground | 91.0 | **35.7** | |
| volcanic ground | 20.4 | 20.3 | |
| tundra 40 m | 25.2 | **21.8** | |
| prairie 40 m | 56.3 | **24.5** | |
| pine 40 m | 98.2 | **39.2** | 29.9 |
| overview 600 m | 171.0 | **45.8** | |
| spawn 600 m | 56.1 | **24.0** | |

GPU avg, before → after: spawn scenery 26.7 → 6.8, occlusion 19.6 → 4.1,
material 9.1 → 8.3; pine ground scenery 40.8 → 11.1, occlusion 36.1 → 11.7;
overview scenery 90.7 → 16.8, occlusion 64.3 → 13.3 (the 2^20 vis-record
overflow no longer happens there). CPU frame at spawn 30 → 19 ms.

**What remains (honest):** the scans now cost spawn ≈ +6 ms, pine ground ≈
+12 ms over the same views with no scatter at all, and those no-scatter views
are themselves 16.7 / 24 / 30 ms: the palette material pass (8–14 ms), the
terrain tiles' cull/raster in both occlusion passes, grass (1.5–1.8 ms) and
VSM page redraws (~170 pages a frame at a still camera: the page pool, 5,461
slots for 19,470 pages, keeps streaming). Halving every draw distance (`dd60`)
or capping the impostor hand-over at 150 m (`imp150`) changed ≤ 3 ms — the
remaining scan cost is the near field (firs within 300 m at 50k triangles; the
pass-2 Hi-Z cull over their live tiles is 7.6 ms at pine ground). 60 fps here
needs the terrain/material work, not more draw distance.

## Look (native frames, `$S/p5/sheets/`)

- `final_1.png`, `final_2.png` — every pose, before (left) / after (right).
- `impshadow_ab.png`, `impcone_ab.png` — pine 40 m with impostor shadows
  from all cards / none / light-facing cards (kept).
- `crop3_0.png`, `crop3_2.png` — horizon crops; `$S/p5/imp/fir_a_sheet.png`
  a baked atlas. Frames: `$S/p5/native/shots_{before,final,…}`.

Read and judged: no popping at the poses; the fade bands dissolve. The
distant pine stand now reads as **fuller and greener** than before: the far
mesh had thinned to wisps (its DAG roots lose sub-pixel twigs) while the
impostor keeps the full-detail crown's coverage — so the hand-over at
~300 m is a visible step from wispy to fuller trees (looking from 40 m up it
is the clearest). From 600 m the fir top cards show as pale-green crown
discs over the needle floor where the mesh had vanished. Impostor shadows
under a high sun are the top card's: rounded/blocky blobs (coarse VSM pages)
rather than tree silhouettes. Rocks and shrubs simply end at 120–400 m; at
those distances they were a few pixels. Overview/desert/volcanic frames
otherwise unchanged.

## Files

New: `internal/world/scenery_draw.go` (+test),
`internal/render/nanite_draw_distance.go` (+ `_test.go`, `_gl_test.go`),
`internal/impostor/{impostor,raster,resolve,cones,write}.go` (+test),
`internal/procgen/stages/scatter_draw.go` (+test).
Changed (additive): `world/scenery.go`, `world/level_schema.go`,
`render/nanite_cull.go` (Draw, instShadowHidden, uniforms),
`render/nanite_scenery.go` (impostor members, camMay, placed cache, maphash
signature), `render/nanite_vis.go`, `render/nanite_vis_source.go`,
`render/nanite_swraster.go`, `render/vsm_track.go`,
`render/scenery_{render,instanced,gpu_cull,xform}.go`; shaders
`nanite_{cull_common,instance_cull_comp,vis_frag,swraster_comp,material_frag,debug_vert}.glsl`,
`scenery_cull_comp.glsl`; `procgen/placement.go`, `genlevel/splice.go`,
`stages/{scatter,scatter_run}.go`, `harness/cmd_check.go`,
`levelio/model_prefetch.go`, `layering_test.go` (impostor entry),
`editor/editor_yaml_docs_gen.go` (regenerated), guide `14-scenery-brush.md`,
`33-generators.md`. **Shaders and exe must ship together** (instance stride).

## Tests

```
go build ./... ; go vet render world impostor procgen/... harness levelio   clean
go test impostor world/... levelio/... procgen/... render harness           ok
go test ./internal/ -run TestPackagesDoNotDependUpward                      ok
go test ./internal/editor/...   fails only TestScoreSibeliusKeymap, TestScoreRebinding (score session)
FOUNDRY_GL_TEST=1 go test ./internal/render/ -run 'TestDrawDistance'        ok (WSLg Mesa)
FOUNDRY_GL_TEST=1 … 'TestNaniteVis|VisBuffer|SWRaster|NaniteScenery|Cutout|Stream|DrawBound|VSM|
  SceneryGPU|SceneryInstanced|SceneryShadow|NaniteColor|Wind|Chunk|MaterialFx|NormalMap|TAA'
                                      all pass except TestSceneryGPUCullDrawsWhatTheCPUCullDrew in
                                      this long combined run (passes alone / in every subset)
```

- GL: `TestDrawDistanceCullsOnEveryPath` (vis / forward / classic, switch
  on/off), `TestDrawDistanceFadeDithersOnTheVisibilityBuffer` (half-faded card
  keeps 0.50 of its pixels; forward cuts whole),
  `TestDrawDistanceImpostorTakesOverAndCrossFades` (mesh near, impostor far,
  311 + 314 of 625 pixels in the band with **0** holes — complementary).
- No GL: windows/cuts/fade complementarity and hash spread (world), CPU
  look-ahead ⊇ GPU rule, VSM tracker redraws a caster crossing its shadow
  distance (render), bake: 9 cards wound to their normals, cone silhouette and
  tangent normals, cutout honoured, view cones cover every elevation with 1–2
  cards (impostor), auto distances / classic writes nothing / bake-once
  provenance (stages).
- The combined-run failure is a stale `GL_INVALID_VALUE` the test's final
  `glGetError` picks up; traced with a debug callback to
  `glGetSynciv` on a dead fence in `visOverflowState.note`
  (`nanite_vis_overflow.go`, PRESETS_4) after an earlier test's context
  reset — pre-existing, not fixed here.

## Still wrong / not done

- 16.7 ms is not reached at any ground pose (23–37 ms); see "What remains".
- Hand-over density step (wispy far mesh → fuller impostor) and blocky
  impostor shadows under a high sun (above).
- Impostors bake geometry normals, not the source's normal maps; no impostor
  wind; classic path draws no impostors; GI and point-light shadow caches
  ignore draw distances; the editor's Scenery panel has no fields for the
  keys (YAML editor and docs do).
- Only `scatter` writes distances; cliff_wall / wall_sheet / vegetation
  placements stay unlimited. A classic doc must set them per rule.
- Windows bench copy `%LOCALAPPDATA%\Temp\foundry-p5` deleted; nothing left
  running.
