# Phase F: the `adventure` level's frame time on the GTX 1070

Status: uncommitted. The ruins run at **55–57 fps static and moving** (17.9–20.4 ms GPU,
p99 21 ms moving). The slot canyon and the cave run at **~30–34 fps** (29–35 ms GPU).
60 fps was not reached anywhere at the user's 2560×1080 (the ruins are 1–4 ms short).
The slot and the cave are held to ~30 fps by their rock walls' per-pixel shading
(see "Why the slot and the cave are 30 fps").

Every number below is from the user's GTX 1070 under Windows, through the native
`FOUNDRY_BENCH` build, at 2560×1080 with the user's `graphics.yaml`
(`gi_quality: off`, `nanite_stream_pool_mb: 1024`, no `texture_compression`, so
textures are uncompressed). The lighting pass's settings (specular, reflections low,
AO, bloom, contact shadows, soft shadows) are at their defaults.

## Step 0: the dark ruins

**Cause.** The other session's change was not the cause: the trails' FX_TRAIL sampler,
the variant key and the material resolve are all correct. The ruins went dark through
the **PBR/specular session's** `gfx/material_pbr.go`. Since Poly Haven imports ship
`_arm` maps, every `ph_*` texture now has one.

- `applyPBR` binds a texture's ARM map whenever it exists.
- For a surface without a `pbr` effect, `applyPBR` also sets roughness and metallic to
  factors of **1**, expecting the map's channels to scale them.
- `fragment.glsl` never samples the ARM map under triplanar or splat
  (`(matFx & (FX_TRIPLANAR | FX_SPLAT)) == 0`).
- So the ruins' triplanar masonry and the ground splat shaded as **metallic = 1**:
  - no diffuse at all;
  - their colour only in the specular.
  - Result: the flat dark brown.

**Fix** (`gfx/material_pbr.go`, `material_surface.go`). `applyPBR` takes the surface's
FX. A triplanar or splat surface binds no ARM map, so it keeps the default dielectric.

- One function changed; the trails' feature is untouched.
- Shot `06_ledge_city` now matches the mid-phase-E2 tan sandstone (scratch
  `phf/fix1/city.png`, `phf/fin/city.png`).

**Regeneration.** The OneDrive `terrain_material` output was already Version 2 (written
13:36 by the trails session, with `layer1_soft: sand`). `foundry gen run adventure`
reports all 17 stages up to date and the ground looks right.

**Slot near-wall smear** (shot 02, right side): not fixed.

- The rock layer repeats every 10 m.
- With the third-person boom pressed under 1 m from the wall, it is magnified, with
  the varnish streaks over it.
- The fix is a close-range detail-normal layer on the rockface/cave rock material
  (`detail_normal`). That needs those stages regenerated (~20 min of OneDrive gen),
  so it is left for a content pass.

## Method

Same as [docs/canyon/PERF.md](../canyon/PERF.md):

- **Build.** A dev exe cross-built from this tree.
- **Project.** The OneDrive project robocopied to
  `%LOCALAPPDATA%\Temp\foundry-adventure-perf`, without the other levels' generated
  output. The tree's shaders are snapshotted beside it at each build, and
  `start_level: adventure`.
- **Run.** A `.bat` sets `FOUNDRY_BENCH` (`perf_bench/run.sh`).
- **Frames.** Static: 150 warm-up frames, then 120 measured frames per pose. Moving:
  `fly=1`, 120 warm-up frames, then 600 measured frames per segment.

**New bench keys** (`app/bench.go`):

| Key | What it does |
| --- | --- |
| `x,y,z,yaw,pitch@px,py,pz` | Holds the player at the given position as well as the camera. A flight interpolates it. `level.flang` streams the canyon, cave and ruins chunks by where the **player** is; without this the ruins poses drew the slot's chunk set. |
| `slow=<ms>` | Logs each profile section slower than that in one frame (`render.ProfileSlowMs`). This is how the hitches below were found. |
| `cursor=free` | The old behaviour. A play bench now pins a captured cursor for the scripts (`hooks.PinCursor`), as mouse-look play holds it. The bench frees the OS mouse so it does not grab the desktop, and a free cursor made the scripts pick the world under it every frame. That pick decoded collision cuts on the main thread: 70–260 ms spikes that play never has. |
| shot time | The shot's ReadPixels and PNG encode (≈0.8–1 s) are no longer counted in the last frame's time. Every pose's "max" had been that. |

**Measuring tools**, used as A/B only:

- `FOUNDRY_NANITE_MAT_PROFILE=1`: one profile section per material variant.
- `FOUNDRY_DUMP_MAT_PROGRAMS=<dir>`: the material programs' sources and NVIDIA binaries.
  The binary carries the NV assembly; its `TEMP` and `lmem` counts showed the register
  pressure.
- `perf_bench/hack.py`: the shader bisection, applied to the bench copy's shader
  snapshot only, never to the tree.

**Noise.** The GPU is shared with the user's desktop and other sessions. Before a run,
other processes held 1–6.8 GB of the 8 GB (`gpu.log`). Above ~4.5 GB, the level's
textures (2.5 GB uncompressed) plus its 1 GB page pool oversubscribe the card.
Identical runs then varied from 22 to 60 ms.

- Development A/Bs were run with `texture_compression: true` (0.65 GB of textures).
  In calm conditions it gives the same means (`after_static_bc7` against
  `after_static1`).
- Every final number is from calm runs, other processes holding ≤ 2.5 GB.
- foundry-42's level2 A/B ran alone, between my batches.

**Poses** (`perf_bench/poses_static.txt`, generated by `perf_bench/poses.py`). Each
pose is the eye 4.2 m behind and 1.7 m over the player (3 m in the cave, 2 m at the
ledge), looking at a target.

| # | Pose | Player | Look at |
| --- | --- | --- | --- |
| 1 | SPAWN (slot, light shafts) | −335, 14.4, 131 | −300, 19, 131 |
| 2 | SLOT HEAD (02's shaft patch) | −129.5, 18.4, 114.2 | −112, 19, 122 |
| 3 | MID-SLOT | −228.4, 15.6, 134.5 | −160, 22, 122 |
| 4 | CAVE SKYLIGHT chamber | −96, 21.2, 127.1 | −78, 30, 120 |
| 5 | CAVE ROCK PILE chamber | −31, 37.3, 97.3 | −7.9, 41, 83.3 |
| 6 | UPPER MOUTH looking out | 67.6, 93.5, 41.4 | 100, 95, 37.5 |
| 7 | LEDGE VISTA over the city | 131.2, 94.3, 51.3 | 240, 30, 36 |
| 8 | RUINS STAIRS | 153, 83.9, 40.5 | 220, 45, 40 |
| 9 | GOAL (relic) | 306, 11.2, 30.5 | 314.2, 12, 34.8 |

**Moving runs.** Three flights (`perf_bench/poses_fly_*.txt`) along the scripted walk's
legs (`phe2/walk_od3.play`), third-person eye, player carried along:

| Segment | From → to | Length | Walk legs |
| --- | --- | --- | --- |
| slot | spawn → slot head | 230 m | 1–21 |
| cave | slot head → ledge, through both chambers | 370 m | 20–81 |
| ruins | ledge → every flight → relic | 260 m, 83 m down | 79–105 |

Each flight is 600 frames, about 11–20 m/s at the measured rates. That is faster than a
run and stresses streaming.

## Results

Three columns in each table:

- **Before (session start)**: `base0`, the tree at 14:40 with the step-0 fix.
- **Before (same tree, switches off)**: `fin_off1`/`fin_off2`. The final tree with every
  Phase F switch off. The lighting session's later additions (lightmaps, VSM PCSS, SSAO
  and contact shadows, SSR and reflection-volume code) landed between 15:20 and 16:10
  and cost **+11 ms** at the slot. So this is the fair before for the final tree.
- **After**: `after_static1`/`after_static2`, `after_fly_*`.

### Static, GPU ms per frame (p50 wall ms in brackets)

| Pose | Before (session start) | Before (same tree, switches off) | **After** | fps after |
| --- | --- | --- | --- | --- |
| 1 SPAWN | 31.9 (32.0) | 44.5 (44.6) | **29.2 (29.3)** | 34 |
| 2 SLOT HEAD | 31.8 | 44.6 | **29.3** | 34 |
| 3 MID-SLOT | 32.7 | 44.9 | **30.7** | 32 |
| 4 SKYLIGHT | 32.0 | 44.7 | **31.3** | 32 |
| 5 ROCK PILE | 33.7 | 47.6 | **33.6** | 30 |
| 6 UPPER MOUTH | 32.0 | 47.7 | **29.8** | 33 |
| 7 LEDGE VISTA | 25.4 † | 35.2 | **20.4** | 49 |
| 8 RUINS STAIRS | 27.1 | 32.7 | **18.5** | 54 |
| 9 GOAL | 25.9 | 29.1 | **17.9** | 56 |

† `base0`'s ledge eye was inside a merlon; its pose was moved 2 m. CPU is 5–10 ms
everywhere, after.

### Moving (p50 / p99 wall ms, GPU avg)

| Flight | Before (same tree, switches off) | **After** | fps after (p50) |
| --- | --- | --- | --- |
| slot | 46.2 / 53.4, GPU 46.2 | **31.8 / 38.8, GPU 31.8** | 31 |
| cave | 46.6 / 77.8, GPU 47.0 | **34.0 / 38.0, GPU 32.9** | 29 |
| ruins | 27.5 / 68.3, GPU 28.0 | **18.0 / 21.1, GPU 17.8** | 55 |

Each flight still has one 90–100 ms frame, in `update` (a chunk switch: see below).
The off runs' p99 includes the free-cursor picks.

### Where the after frame goes

| Pass | Slot / cave poses | Ruins poses |
| --- | --- | --- |
| nanite: material | 20–24 (the rock walls 16–19) | 9.7–10.7 |
| volumetrics: shafts | 2.6–3.1 | 1.5–1.8 |
| nanite raster (scenery + occlusion) | 2.5 | 3.5–4 |
| entities (the third-person body, forward) | 0.7 | 0.7–1.9 |
| VSM, AO, TAA, bloom, trails | ~2.5 | ~2.5 |

## What cost, and what changed

Each switch's off (`=0`) is the old behaviour. All are `gfx.EnvFlag`.

| # | Fix | Gain | Files | Switch |
| --- | --- | --- | --- | --- |
| 1 | **Frame folds in the material variants.** The variant key gains frame-wide bits: GI off by setting, no water caustics, every shadowed sun on the VSM (no cascades), reflections below High (no SSR march), no reflection volume in the level. For each bit, those tests are folded out of the **whole** material source. | material 33 → 22 ms at the slot, 12.7 → 9.2 at the ruins | `render/nanite_vis_variants.go`, `nanite_vis.go` | `FOUNDRY_NANITE_MAT_FRAME_FOLD` |
| 2 | **Ambient-volume view cull.** Volumes whose box misses the camera frustum, and volumes that cut nothing, are not packed. Exact for the camera's passes. Off while GI traces probes. | material −4 to −6 ms and shafts 3.8 → 1.6 ms at the ruins, where the cave's 8 volumes were all packed | `world/volume_ambient.go`, `render/world_frame.go` | `FOUNDRY_AMBIENT_VIEW_CULL` |
| 3 | **Per-tile ambient-volume masks.** After the vis buffer's depth, one compute pass (0.06 ms) marks for each 32 px tile the volumes whose boxes reach into its frustum up to its farthest depth. The material pass's ambient and fog loops and the shafts' density loop skip the rest. A point finds its tile by projecting itself, so any stage can ask. Programs not opted in see every bit set. | slot: material 23.7 → 19.8 ms, shafts 4.1 → 2.7 (the cave's volumes ahead, behind the rock) | `shaders/amb_tiles.glsl`, `amb_tiles_comp.glsl` (new), `light_model.glsl`, `fog.glsl`, `shafts_frag.glsl`, `render/amb_tiles.go` (new), `nanite_vis.go`, `shafts.go`, `world_frame.go`, `world/volume_ambient.go` (`AmbientPacked`) | `FOUNDRY_AMBIENT_TILES` |
| 4 | **Variant warm-up.** Every material variant the level's scenery can need, in every chunk, is built on the level's first shaded frame. The driver takes ~2.7 s per program; built lazily, a chunk switched on in play stalled its first frame. | cave flight: a 20 s second frame and mid-flight compiles gone | `render/nanite_vis_variants.go` | `FOUNDRY_NANITE_MAT_WARM` |
| 5 | **Whole-level page pool.** The scenery page pool is built from every Nanite mesh the level places, chunk on or off. The `ruins` chunk switching on mid-cave had rebuilt it: every resident page released and reloaded. | cave flight: a 0.75 s CPU / 2.0 s GPU frame gone | `render/nanite_scenery.go` | `FOUNDRY_NANITE_POOL_WHOLE_LEVEL` |
| 6 | **Splat layout fold.** Each splat layer's triplanar, anti-tile, relief and strata switches become literals in the variant. | Measured ≈0: the NV compiler had already shared the layer code as subroutines. Kept; exact and harmless. | `render/nanite_vis_variants.go` | `FOUNDRY_NANITE_MAT_SPLAT_FOLD` |
| 7 | **Specialiser expands `#include`s.** The variants' fold text in `light_model.glsl`, `specular.glsl` and `surface_fx.glsl` was out of its reach. This is why fix 1 first measured nothing. | — | `render/nanite_vis_variants.go` | — |
| 8 | **CPU.** Each material program gets its own skinning-location cache, where one shared cache re-queried both locations at every program switch. `UpdateEnvMaps` reads and restores GL state only when it captures. | ≈1–2 ms of driver round trip, which moved to the next sync point. CPU was not the limit. | `render/nanite_vis.go`, `render/envmap.go` | — |

### Root causes, measured

All at 2560×1080. "Strip" means the bench copy's shaders were edited, never the tree.

- **The material pass is register-bound, not fetch-bound.**
  - Anisotropy 16→1: no change.
  - BC7: no change in calm.
  - Everything stripped but one texture fetch: 2.2 ms. So the vis reconstruction is
    cheap.
  - Full: 22 ms at f2, 33 ms at f4.
  - Every uniform-gated path is compiled into every program; NVIDIA sizes registers for
    the worst path, then spills.
  - The NV assembly of the simplest variant went from 54 to 71 vec4 temporaries when
    the lighting pass's PCSS, SSAO and lightmap code landed (15:52–15:56). The splat
    variants went from 134 to 150. That step cost +11 ms at the slot, whatever the
    runtime settings: soft shadows, AO, specular, reflections and bloom all off saved
    only 1–3 ms.
  - Folding the dead frame-wide paths (fix 1) won those back, and more.
- **Ambient volumes cost ≈0.6 ms per packed volume per frame.** That is about
  0.2 ns/px per volume in the material pass, plus two per shaft step. Fixes 2–3.
  Moving the loops to the top of main (low pressure) changed nothing; their count did.
- **The rock walls**, variant `0x5f02e` (splat + ground blend + streaks + macro +
  trail), are 16–19 ms of the slot's frame: 1.9 Mpx at ~9 ns each.

  | Strip | Walls, ms |
  | --- | --- |
  | none | 28 |
  | no sun shadow | 18.5 |
  | no specular/env | 18.4 (both before fix 1) |
  | no ambient-volume fog | 23 |
  | volumes capped at 2 | 23 |
  | no PCSS | 26.8 |
  | no SSAO | 27.4 |
  | bare triplanar splat | 23.6 |
  | minimal lighting | 11.2 |
  | minimal lighting and bare triplanar | 7.8 |

## Why the slot and the cave are 30 fps

- Their screen is rock wall from edge to edge. Each wall pixel runs the terrain's full
  rock material:
  - a 4-layer splat with a triplanar, anti-tiled rock layer, strata and bedding;
  - ground blend, streaks and macro variation;
  - the ground-trail and ripple code for the sand layer.
- It then runs the full lighting model:
  - the VSM's 9-tap filter plus PCSS;
  - GGX specular with the environment's split sum;
  - SSAO, contact shadows;
  - the ambient-volume fog.
- That is ~9 ns a pixel on the 1070, about 20 ms for the walls alone at 2560×1080.
  Halving it means taking features off the walls or shading fewer pixels.
- Not done, because each changes the look or the player's settings:
  - a cheaper wall material: the rock layer and its sand foot only, no relief, ripple or
    trail;
  - the lighting session's PCSS, SSAO or specular off for this tier of card;
  - a render scale with TAA upsampling (not in the engine);
  - `texture_compression: true`, which does not change the mean but removes the
    oversubscription swings: 2.5 GB → 0.65 GB of textures.
- The ruins are mostly lighter triplanar masonry plus sky. They are 1–4 ms short of 60;
  the biggest remaining items are the material pass (≈10 ms) and the shafts (1.6 ms).

## Remaining costs and gaps

- **60 fps**: reached nowhere. The ruins run at 49–56 fps static and 55 moving; the
  slot and cave at 30–34. The reasons are above.
- **VRAM.** Uncompressed, the level's textures are 2.5 GB:
  - 740 MB of those are `_arm` maps, which the PBR pass loads for every texture,
    including the triplanar/splat ones it never samples;
  - the splat layers are also loaded individually beside their atlas.
  - With a busy desktop the card oversubscribes and frames double.
  - Recommendation to the user: `texture_compression: true`.
  - For the PBR session: load ARM maps on first bind, or not for textures only ever
    seen through triplanar or splat.
- **One ~90 ms `update` frame per flight**, at a chunk switch (`level.flang`). It does
  not reach p99.
- **First-frame build.** Fix 4 builds the variants up front: ~19 s on the level's first
  frame on this driver with a cold shader cache. The driver's own cache shortens later
  runs.
- **Frame-key flips.** Caustics (water quality and the level's bodies) and cascades
  (VSM covering the sun) are per-frame. A frame that flips one builds a new variant once.
- **The bench's player** is held in the air at each pose, so the body plays its fall
  clip with its arms up. Cosmetic: the on/off image diffs differ only by the body's
  animation frame and one tower edge (`fin_on*` against `fin_off*`).
- **Shaders shared with sessions in flight.** The lighting pass is still adding to
  `fragment.glsl` and its includes. Every addition is compiled into every material
  variant, so each one costs the slot again unless it is folded (fix 1's
  `visFrameSpecs`) when off.

## Raw reports

[perf_bench/](perf_bench/). The bench copy and its exes were deleted at the end.

| Files | What |
| --- | --- |
| `base0` | Before, session start |
| `fin_off1`, `fin_off2` | Before, same tree, all switches off |
| `after_static1`, `after_static2` | After, static |
| `after_fly_slot`, `after_fly_cave`, `after_fly_ruins` | After, moving |
| `ffly_off_*` | Moving, switches off |
| `after_static_bc7`, `after_fly_ruins_bc7` | Compressed textures |
| `k9c`, `k9c_off` | Per-variant profiles (fix 1) |
| `t9`, `t9off` | Tiles (fix 3) |
| `c9_cull1`, `c9_nocull1` | View cull (fix 2) |
| `g_*` | Fold and variant A/B |
| `hk*`, `w_*`, `h8_*`, `n_N1` | Shader bisections |
| `v_*` | Compression noise test |
| `l_*` | Lighting settings off |
| `texlist`, `texlist_textures.txt` | Textures in VRAM |

Tools: `perf_bench/run.sh`, `poses.py`, `hack.py`, and the pose files.

## Tests

- `render`: `TestMaterialVariantsFoldTheFrameSwitches`,
  `TestMaterialVariantsFoldTheSplatLayout`.
- `world`: `TestPackAmbientVolumesCullsToTheView`.
- `go test -short` of `world`, `gfx`, `app` and `render`: ok.
- GL `render -run 'MaterialFx|Variant|Shafts|Fog|Ambient|SceneBlock|Volumetric|GI'`:
  ok.
- `go vet` clean on the touched packages.

---

# Phase F2: 60 fps on the GTX 1070

Status: uncommitted. With the recommended settings below, **every pose and every
flight runs at 60 fps or better** on the user's GTX 1070 at 2560×1080:

- static: p50 11.8–14.9 ms, GPU ≤ 14.7 ms;
- moving: p50 11.4–13.7 ms (70–87 fps).

At native scale (render scale 1.0, the user's own `graphics.yaml`), the frame is what
Phase F left: the slot and the cave at 30–34 fps, the ruins at 53–55.

How 60 was reached: the slot's rock walls cost ~9 ns a pixel, and the fixes that
change nothing on screen are worth ~1 ms at most there (step 1). So 60 comes from
**shading fewer pixels**:

- a render scale with a TAA upsampling resolve;
- dynamic resolution, which holds the world's GPU time at 15 ms.

That is what UE5's own Nanite demo did, with TSR rendering ~1440p for a 4K output.
In the slot it settles at a scale of 0.50–0.60, in the ruins at 0.64–0.85.

Method and noise handling are as in Phase F: the native `FOUNDRY_BENCH` build, the
OneDrive project robocopied to `%LOCALAPPDATA%\Temp\foundry-adventure-perf`, the same
poses and flights, and `nvidia-smi` checked before every run. Every number below is
from runs with other processes holding ≤ 2.6 GB of VRAM.

## Final table

| Pose / flight | Native 1.0, user yaml: p50 (GPU) | fps | Recommended: p50 (GPU) | Scale | fps |
| --- | --- | --- | --- | --- | --- |
| 1 SPAWN | 29.7 (29.6) | 34 | **12.1 (11.8)** | 0.53 | 83 |
| 2 SLOT HEAD | 29.5 (29.4) | 34 | **12.1 (11.8)** | 0.53 | 83 |
| 3 MID-SLOT | 31.4 (31.2) | 32 | **14.9 (14.7)** | 0.60 | 67 |
| 4 SKYLIGHT | 31.0 (30.9) | 32 | **12.6 (12.4)** | 0.53 | 79 |
| 5 ROCK PILE | 33.5 (33.4) | 30 | **14.2 (14.0)** | 0.55 | 70 |
| 6 UPPER MOUTH | 30.0 (29.9) | 33 | **12.0 (12.1)** | 0.53 | 83 |
| 7 LEDGE VISTA | 20.9 (20.8) | 48 | **11.8 (11.7)** | 0.64 | 85 |
| 8 RUINS STAIRS | 18.9 (18.7) | 53 | **14.0 (13.9)** | 0.80 | 71 |
| 9 GOAL | 18.5 (18.3) | 54 | **14.6 (14.4)** | 0.85 | 68 |
| slot flight (p50 / p99 / max) | 32.2 / 39.6 / 98 | 31 | **13.2 / 25.1 / 44** | mean 0.51 | 76 |
| cave flight | 34.1 / 38.1 / 124 | 29 | **13.7 / 25.7 / 52** | mean 0.50 | 73 |
| ruins flight | 18.2 / 20.7 / 43 | 55 | **11.4 / 22.4 / 30** | mean 0.67 | 87 |

Notes on the table:

- The scale is the frame-mean the bench reports (`render scale` lines).
- The native flights' maxima predate two hitch fixes:
  - the soft-map decode (fix 5, F2);
  - foundry-42's ARM-map prefetch, landed during this phase. It took the slot flight's
    max from 66 to 49 ms (`arm_slot` against `hitch_slot3`).
- A fixed render scale of 0.75 without dynamic resolution (`fin_s075_*`) gives:
  - slot 19.3–22.3 ms (45–52 fps);
  - ruins 12.9–15.2 ms (66–78 fps).
- Reports: [perf_bench/f2/](perf_bench/f2/):
  - native 1.0: `fin_user_*`;
  - recommended: `fin4_rec_*`;
  - fixed 0.75: `fin_s075_*`.

### Looks

Shots: scratchpad `phf2/final_shots/pose<N>_native_s075_dyn.png`, each a centre crop
of native 1.0 | fixed 0.75 | recommended (dynamic), at the converged TAA frame.

PSNR against native 1.0:

| Pose | 0.75 (dB) | Dynamic (dB) |
| --- | --- | --- |
| 1 | 36.6 | 35.3 |
| 2 | 32.3 | 31.7 |
| 3 | 35.5 | 34.4 |
| 4 | 48.1 | 46.3 |
| 5 | 40.1 | 39.7 |
| 6 | 39.2 | 37.2 |
| 7 | 27.6 † | 22.7 † |
| 8 | 32.1 | 31.6 |
| 9 | 33.8 | 38.2 |

† Pose 7's low figures are the bench-held body: its fall clip is in a different frame
in each run.

- **At 0.75** the image is hard to tell from native. The ruins' masonry and the
  distant towers keep their edges, with no added shimmer in the converged frames.
- **At the slot's 0.5–0.6** the rock walls are visibly softer, though not smeared.
- This is the price of 60 fps there. A player who prefers sharpness raises
  `dynamic_resolution_min`, and gets fewer frames in the slot.

## What changed

Each switch's `=0` restores the old behaviour.

| # | Change | Gain | Files | Switch |
| --- | --- | --- | --- | --- |
| 1 | **Render scale + TAA upsampling.** See the list below. | slot 29.6 → 19.2 ms at 0.75; any pose ≤ 15 ms under dynamic resolution | `gfx/render_scale.go` (new), `render/dynres.go` (new), `render/taa.go`, `shaders/taa_resolve_frag.glsl`, `shaders/nanite_material_frag.glsl`, `render/nanite_vis.go` | `FOUNDRY_RENDER_SCALE_PATH`, `FOUNDRY_RENDER_SCALE_MIPBIAS`; `FOUNDRY_RENDER_SCALE`, `FOUNDRY_DYNAMIC_RES(_TARGET_MS, _MIN)` for a run |
| 2 | **Dynamic resolution.** See the list below. | holds GPU ≤ 15 ms | `render/dynres.go` | setting |
| 3 | **Settings folds.** A lighting feature the player turned off is folded out of every Nanite material variant (`visKeyNoSoftShadows`, `NoSSAO`, `NoContact`, `NoSpecular`, `NoEnv`), so off gives back its registers as well as its work. Agreed with foundry-42, who marked the five expressions `FOLDED BY TEXT`; `TestMaterialVariantsFoldTheFrameSwitches` checks they still exist. | 0.2–1 ms per feature turned off | `render/nanite_vis_variants.go` | `FOUNDRY_NANITE_MAT_SETTING_FOLD` |
| 4 | **`light_shafts` setting**: `high` (default, the old march), `low` (half the steps), `off`. | low: shafts 2.7 → 1.5 ms at the slot | `gfx/render_scale.go`, `render/shafts.go` | setting |
| 5 | **Soft-ground weights maps decoded in the background.** See below. | the flights' ~90 ms `update` frame gone (`fly_end` → `fly_end3`: max 99 → 33 ms) | `world/soft_ground.go`, `sim/footsteps.go` | `FOUNDRY_SOFT_MAP_PREFETCH` |
| 6 | **Close-range detail normal on the slot walls.** See below. | +0.1–0.3 ms at the slot head (`det_off`/`det_on5`) | `procgen/stages/wall_sheet.go`; OneDrive `generators/adventure.fgen` (rockface) and its regenerated output | stage parameter (empty = none) |

### 1. Render scale with TAA upsampling

- **The setting.** `graphics.yaml` `render_scale` takes 0.5–1.0; the default 1.0 is
  the old path, exactly. A TAA frame draws the world into a scene target of the
  scaled size. Every pass already sized itself from `scene.w/h`, so no pass needed
  changing.
- **The jitter** runs in render pixels, over 8/s² Halton points (at most 32), so
  every output pixel is visited by samples near its centre.
- **The history, bloom, exposure and output** stay at the output's size. A scale
  change keeps the history, because it is output-sized.
- **The resolve.** When the scene target is smaller than the output, a new
  `resolveUpscaled` runs (UE4 TAAU, after Karis):
  - each output pixel gathers the 3×3 render samples round it, at their jittered
    positions, under a Gaussian (exp(−2.29 d²), a Blackman-Harris fit);
  - the clamp box is those same nine samples;
  - the new frame's share is scaled by how near the nearest sample fell,
    normalised so its mean is the old blend.
- **Texture LOD.** The Nanite material pass scales its reconstructed derivatives by
  s (a LOD bias of log2 s), so the reconstruction gets the output's texture detail.
- **Sharpening.** The output's sharpen rises with 0.6·(1 − s).
- **Where it does not apply.** The editor's views and MSAA never scale. The HUD and
  UI draw after, at full size.
- **Settings tab.** OneDrive `ui/settings.fui` and `scripts/menus/settings.flang`
  gain a Graphics → Performance block: a Render scale slider, plus toggles for
  Dynamic resolution, Soft shadows, Ambient occlusion and Contact shadows.
  `foundry check` passes.
- **Other ways in.** Script props `render_scale`, `dynamic_resolution`,
  `dynamic_resolution_target_ms`, `dynamic_resolution_min`, `light_shafts`. Console
  `render_scale …`, and `lighting light_shafts …`.

### 2. Dynamic resolution

- **Measuring.** Two GL timestamp queries bracket the world frame. Each is read
  4 frames later, which never stalls, because PaceFrame has waited that frame out.
- **The controller:**
  - The scale moves in 0.05 steps (a step remakes the frame's targets).
  - Down: when the moving average passes 1.04 × the target, or after 3 frames in a
    row over 1.5 × the target. It goes straight to the scale that would have met
    the target.
  - Up: one step at a time, after 60 frames with room to spare. Room means the
    prediction, taking 30% of the frame as fixed cost, comes in under 0.92 × the
    target. Never within 2 s of a step down.
- **Range.** It moves between `dynamic_resolution_min` and `render_scale`, defaults
  0.5 and 15 ms.
- **Why the hysteresis.** A version that stepped up readily hunted, and every change
  is a hitch: p99 rose to 36 ms (`dyn_s1`, `fin2`).

### 5. Soft-ground weights maps

- **The cause.** Phase F's ~90 ms `update` frame (one per flight) was not the
  chunk switch. `foundry-prof` builds with finer profile sections put it in
  `sim.stepWalkers`. The first step onto a new soft tile decoded its splat-weights
  PNG on the main thread (`world.loadSoftMap`). In the bench the player is held in
  the air, so that step came mid-flight, near the ruins.
- **The fix.** The level's maps now start decoding on a goroutine at the level's
  first walker step (`world.WarmSoftGround`). A step that comes first waits for the
  decode, as it did before.

### 6. Slot near-wall smear

- **The change.** The wall_sheet stage has new `detail_normal` / `detail_scale` /
  `detail_strength` / `detail_distance` parameters (`Added`, so stages that do not
  set them stay up to date). They add the material's `detail_normal` effect.
- **Applied to the level.** `adventure.fgen`'s rockface sets ph_cliff_side, 0.8 m,
  0.6, 5 m, and `foundry gen run adventure -stage rockface` regenerated it in 4 m 43 s.
- **Cost.** 5 m keeps the cost within the 0.3 ms budget. At 8 m it was 0.3–0.6 ms.
- **The look.** The magnified smear on a wall under a metre away becomes rock grain
  (scratchpad `phf2/shots/det_cmp.png`).
- **Not done.** The cave's own rock materials (cave stage) do not have it yet.

### Step 1: what "lossless" was worth

- **Splat and triplanar skips were already there.** Splat layers below their blend
  cut, triplanar axes under 0.02 weight, anti-tile second taps outside the band and
  bedding past its footprint were all skipped per pixel by Phase F's and earlier
  code (`fxSplatCandidate`, `fxSplatAxis`, `needA/needB`).
- **The lighting early-outs were mostly in place too:** PCSS's no-blocker exit,
  facing-away lights skipping their shadow, and the SSR/reflection-volume folds.
- **Wall-variant bisection.** Each strip below was applied to the bench copy only
  (`hk_*`, slot spawn). It is what each piece costs on 16.9 ms of wall:

  | Piece | Cost on the walls, ms |
  | --- | --- |
  | sun shadow | 2.4 |
  | ambient-volume fog | 2.2 |
  | streaks | 1.6 |
  | macro | 1.3 |
  | PCSS | 1.2 |
  | spec/env | 1.2 |
  | bedding | 1.1 |
  | SSAO | 0.4 |
  | ground blend | 0.3 |
  | strata | 0.1 |

- **Nothing large to remove.** No single piece is large, and removing any of them
  changes the look. The cost is the sum, and the register pressure of carrying them
  all (the wall variant: 137 vec4 temporaries).
- **The material pass's corner loop** keeps its arrays in local memory
  (`lmem0[24]`). Unrolling it (`hk_UN`) was 1 ms *slower*, so it stays.

### Step 3: per-feature cost on this level

Native 1.0, slot spawn / rock pile / ruins stairs. A single run each, ±0.5 ms noise
(`ft_*`).

| Setting off | Saves, ms |
| --- | --- |
| soft_shadows | 0.2–0.7 |
| ambient_occlusion | ≈ 0 |
| contact_shadows | ≈ 0 |
| reflections off | 0.8–1.0 |
| specular | 0.4–1.0 |
| bloom | ≈ 0.3 |
| light_shafts low | shafts 2.7 → 1.5 |

- Every one of these has a player setting. `light_shafts` is new; the rest existed
  and gained the variant folds.
- **None of them gets the slot near 60 alone.**

## Recommended `graphics.yaml` for the user

This adds four lines to their current file. Their own settings stay as they are.

```yaml
smooth_lighting: true
foliage_distance: 0.6
foliage_shadows: true
nanite_lod_error: 1
nanite_stream_pool_mb: 1024
gi_quality: "off"
taa_sharpen: 0.25
texture_compression: true        # 2.5 GB -> 0.65 GB of textures: no oversubscription swings
dynamic_resolution: true         # holds the world at the target below
dynamic_resolution_target_ms: 15 # 60 fps with room for the HUD
dynamic_resolution_min: 0.5
```

Two optional changes trade looks for scale, so the slot settles higher:

- `light_shafts: low` (about +0.05 scale in the slot);
- `dynamic_resolution_min: 0.6` (sharper, but the slot drops to ~50 fps in its worst
  spots).

## Remaining

- **Chunk-switch frame.** The cave/canyon chunk switching on is still one ~45–50 ms
  frame over the scenery renderer's member rebuild (`nanite: scenery` CPU 35 ms,
  `rebuild`) and a page-stream burst. To fix: spread the rebuild, and keep the
  occlusion history.
- **A dynamic-resolution step is a small hitch:**
  - the scene target and the passes' own targets are remade;
  - so the recommended flights' p99 is 22–26 ms against a p50 of 11–14 ms.
  - To fix: allocate at the top scale and draw into a sub-rect. That needs every
    pass to take a viewport inside a larger texture.
- **The slot's walls** are still ~9 ns a pixel. A cheaper wall material (relief,
  ripple and trail code off the rock) or a deferred shadow-mask pass, to take the
  VSM/PCSS code out of the material programs, would let the slot settle above 0.6.
- **The cave stage's rock** has no detail normal yet.

## Tests

- New: `render` `TestDynamicResolutionSteps`; GL `TestTAAUpscaleResolveReconstructsTheOutput`
  (flat field exact, a step edge lands at the output's centre).
- New: `app` `TestRenderScaleSettingsRoundTrip`.
- `TestMaterialVariantsFoldTheFrameSwitches` now covers the setting folds.
- `go test -short`: `render`, `gfx`, `app`, `world`, `sim`, `devui`, `script`,
  `procgen/stages`, `harness`, `internal` all ok. GL `TAAUpscale|TAAResolve`: ok.
- `go vet` clean on the touched packages.
- Bench tools: [perf_bench/f2/](perf_bench/f2/):
  - `final.sh`, `feat.sh`, `bisect.sh`, `hack.py`, `run.sh`, `build.sh`;
  - the recommended yaml.
