# The generated canyon's frame time on the GTX 1070

The desert canyon the `desert_canyon` template generates (phase 7: 6,607 cooked
placements, a 3 km backdrop, splat terrain, VSM, TAA, GI) cost the user about
**100 ms of GPU a frame in the editor** and 20–40 fps in play. This is where that time
went, what was wrong, what changed, and what is left. Every number here is from the
user's GTX 1070 under Windows, through the native `FOUNDRY_BENCH` build. WSLg's Mesa
d3d12 timer queries read 0, and its frame times are not the card's.

## Method

- **Build.** A dev (editor) exe is cross-built from this tree:
  `GOOS=windows CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc CXX=x86_64-w64-mingw32-g++ go build -ldflags '-extldflags "-static-libgcc -static-libstdc++"' ./cmd/foundry`.
- **Project.** The phase-7 scratch project (`$S/p7b`) is copied with `cp -rL` to
  `%LOCALAPPDATA%\Temp\foundry-canyon-perf`, so nothing is read over the 9P bridge.
  `SDL2.dll` and the tree's shaders go beside it.
- **Run.** A `.bat` file sets `FOUNDRY_BENCH` and runs the exe through `cmd.exe`.
  Quoting `;` and `|` straight through `cmd.exe /c` from WSL loses the variable, and
  the editor then opens instead of the bench.
- **Settings.** `nvidia-smi.exe` showed 4–13% before each run. Two configurations:
  - **default**: no `graphics.yaml`. GI medium, page pool budget 256 MB before the
    fix, texture compression off, 1920×1080.
  - **user**: the user's own `graphics.yaml` from the OneDrive project
    (`gi_quality: off`, `nanite_stream_pool_mb: 1024`, compression off), at 2560×1080.
    The user's monitor is 2560×1080 and the editor runs maximised.
- **Measurement.** 60 warm-up frames, then 60 measured frames a pose (120 for the
  baseline). The "GPU" columns are the F3 profiler's timestamp sections, averaged.
- **Poses** (eye x,y,z, yaw, pitch):

  | Pose | Eye | Yaw | Pitch |
  | --- | --- | --- | --- |
  | SPAWN | −218.5, 23.2, −73.2 | 1.15 | 0 |
  | FLOOR | −180.2, 21.3, −46.0 | 1.145 | 0.03 |
  | OVER (230 m up) | −300, 230, −300 | 0.785 | −0.5 |
  | WALL | −31.9, 18.9, −24.4 | 0 | 0.22 |
  | SUN | 60.2, 17.7, 6.3 | −2.44 | 0.2 |

- **Moving camera.** A `fly=1` run over 300 frames through SPAWN → FLOOR →
  (−100, 30, −20) → WALL → SUN, about 70 m/s.
- **Editor view.** `editor=1` is a new bench key (app/bench.go). It installs the level
  under the editor, which stays open. Its own view, overlays and ImGui frame are
  timed. A multisampled window is resolved before a shot is read back, because it
  read back black.
- **Raw reports** are in [perf_bench/](perf_bench/):
  - before: `base0`, `pl1u`, `ed0`, `ed1u`;
  - after: `def11`, `fin_pl_user`, `fin_ed_user`, `fin_ed_def`, `fin_fly_*`;
  - A/B runs: `ab_*`, `hack*`, `v4fx*`, `def11off`.
- **File names.** The `perf_bench/` files keep the bench's own run names. The
  sections below use these names for them:

  | Name used below | Files in perf_bench/ |
  | --- | --- |
  | before, play, default | `base0` |
  | before, play, user | `pl1u` |
  | before, editor, default | `ed0` |
  | before, editor, user | `ed1u` |
  | after, play, default | `def11` |
  | after, play, user | `fin_pl_user` |
  | after, editor, default | `fin_ed_def` |
  | after, editor, user | `fin_ed_user` |
  | after, moving | `fin_fly_def2` (default), `fin_fly_user` (user) |
  | draw bound A/B | `def11` against `def11off` |
  | placement A/B levels | `ab_dc_nowalls`, `ab_dc_noscatter`, `ab_dc_nofar`, `ab_dc_ground` |
  | splat effect A/B | `v4fx0`, `v4fx125` |
  | shader A/B runs | `hackA`, `hackB`, `hackC`, `hackE`, `hackF`, `hackU` |

## Results

The raw report each table is read from is named in its heading (see Method).

### Editor, user settings, 2560×1080 (the reported ~100 ms)

Before: `ed1u`. After: `fin_ed_user`. Each cell is before → after, in ms of GPU.

| Pass | SPAWN | FLOOR | OVER | WALL | SUN |
| --- | --- | --- | --- | --- | --- |
| **GPU frame** | **101.2 → 16.6** | **88.4 → 16.0** | **163.0 → 19.5** | **114.8 → 12.7** | **170.7 → 14.7** |
| p50 wall frame | 101.7 → 16.7 | 88.7 → 16.2 | 163.6 → 19.8 | 115.2 → 13.0 | 171.0 → 15.0 |
| shadows: sun (cascades → VSM) | 27.5 → 0.04 | 29.8 → 0.04 | 19.8 → 0.04 | 32.7 → 0.04 | 31.9 → 0.03 |
| nanite: scenery (forward → vis buffer) | 69.7 → 4.1 | 55.1 → 3.2 | 135.8 → 5.9 | 78.9 → 3.4 | 135.1 → 4.2 |
| nanite: occlusion | 3.8 → 2.5 | 3.4 → 2.4 | 7.1 → 3.1 | 2.9 → 2.0 | 3.5 → 2.5 |
| nanite: material | — → 8.5 | — → 8.9 | — → 9.1 | — → 5.9 | — → 6.4 |
| ui (ImGui) | 0.03 | 0.03 | 0.03 | 0.03 | 0.03 |

### Play, user settings, 2560×1080

Before: `pl1u`. After: `fin_pl_user`.

| Pass | SPAWN | FLOOR | OVER | WALL | SUN |
| --- | --- | --- | --- | --- | --- |
| **GPU frame** | **31.4 → 16.2** | **29.9 → 15.4** | **48.0 → 19.1** | **22.7 → 12.3** | **21.9 → 14.3** |
| nanite: scenery (cull + ID raster) | 6.1 → 3.8 | 4.9 → 3.2 | 17.0 → 5.9 | 5.9 → 3.3 | 6.4 → 4.2 |
| nanite: occlusion (pass 2) | 4.4 → 2.5 | 3.8 → 2.4 | 8.1 → 3.1 | 3.2 → 2.0 | 3.6 → 2.5 |
| nanite: material | 19.6 → 8.7 | 19.8 → 8.7 | 21.6 → 9.0 | 12.6 → 5.8 | 10.7 → 6.4 |
| shadows: sun (VSM, still camera) | 0.04 | 0.04 | 0.04 | 0.04 | 0.04 |
| CPU frame | 9.4 → 9.5 | 9.3 → 9.4 | 10.5 → 10.8 | 10.0 → 9.8 | 8.8 → 9.1 |

### Play, default settings, 1920×1080

Before: `base0`. After: `def11`.

| Pass | SPAWN | FLOOR | OVER | WALL | SUN |
| --- | --- | --- | --- | --- | --- |
| **GPU frame** | **26.0 → 17.0** | **25.3 → 16.2** | **41.5 → 20.2** | **19.0 → 12.7** | **20.6 → 14.8** |
| nanite: scenery | 5.9 → 3.3 | 5.1 → 2.7 | 16.2 → 5.7 | 5.4 → 2.6 | 6.4 → 3.5 |
| nanite: occlusion | 3.8 → 2.4 | 3.4 → 2.2 | 11.0 → 3.0 | 2.6 → 1.6 | 3.1 → 1.9 |
| nanite: material | 13.9 → 8.8 | 14.4 → 8.9 | 12.7 → 9.8 | 8.4 → 5.8 | 8.4 → 6.5 |
| gi (probes) | 1.5 → 1.6 | 1.5 → 1.6 | 0.7 → 0.7 | 1.7 → 1.8 | 1.8 → 1.9 |
| CPU frame | 12.5 → 9.6 | 12.4 → 9.3 | 12.0 → 10.7 | 12.5 → 9.8 | 12.4 → 9.5 |
| pages uploaded / starved per pose | ~3,300 / 0–360 → 0 / 0 | | | | |

### Editor, default settings, 1920×1080

Before: `ed0`. After: `fin_ed_def`.

The editor was only ~20 ms here before, because the 256 MB pool starved the dense
meshes into coarse clusters. With the user's 1024 MB pool it was 88–171 ms (the first
table).

| | SPAWN | FLOOR | OVER | WALL | SUN |
| --- | --- | --- | --- | --- | --- |
| GPU frame | 20.3 → 17.3 | 20.7 → 16.6 | 20.5 → 20.5 | 21.6 → 13.0 | 21.3 → 14.9 |
| shadows: sun (cascades → VSM) | 12.1 → 0.05 | 13.2 → 0.04 | 6.9 → 0.04 | 14.1 → 0.04 | 13.7 → 0.04 |

### Moving camera (after only)

Default settings at 1920×1080 (`fin_fly_def2`); user settings at 2560×1080
(`fin_fly_user`).

| | GPU frame | p50 wall | p99 wall | shadows: sun (VSM) | nanite: material | CPU frame |
| --- | --- | --- | --- | --- | --- | --- |
| default 1920 | 19.9 | 20.4 | 71.8 | 3.9 (15 pages in 4.7 rects a frame) | 8.1 | 13.6 |
| user 2560 | 19.0 | 19.7 | 73.2 | 4.2 | 7.7 | 14.3 |

There is no true "before" for the moving camera. The flight was added after the old
build's shaders had been replaced in the bench copy. Its one-off hitches (p99, max
270 ms) are collision cuts decoded on the main thread: `collision.residentFor`, 11% of
the flight's CPU profile. The bench flies at ~70 m/s, which outruns `prefetchNear`.

## Root causes, ranked by what they cost

1. **The editor drew the level the forward way** (−70 to −150 ms, editor only).
   - `WorldDraw.Editor` kept the editor's view off the scene target, so it got no
     visibility buffer, no VSM and no TAA. It went straight into the 8x-MSAA window.
   - Every micro-triangle of the dense cooked meshes was shaded forward at every
     overlap: 55–136 ms of `nanite: scenery`.
   - The sun fell back to three 2048² cascades that walk the whole canyon at a
     texel-sized cut: 20–33 ms.
   - That is the ~100 ms the user saw. Play was a different, much cheaper path.
2. **The material pass's uber-shader** (−5 to −11 ms, play and editor).
   - The visibility buffer shades every pixel with fragment.glsl's whole main, which
     also contains water, blends, baked light, decals and seven surface effects
     behind uniform branches. The 1070 sizes every pixel's registers for the worst
     path.
   - surface_fx.glsl's splat looped over 4 layers × 3 projections with `continue`s.
     The loops indexed vec4 uniforms and local arrays by the loop counter. NVIDIA
     kept those loops rolled and spilled the arrays to local memory.
   - Measured at 2560×1080 with compression on:
     - generic program: 15.4 ms;
     - minimal textured shader: 1.4 ms;
     - all dead paths stripped by hand: 4.6 ms;
     - splat effect off: 5.8 ms (the splat was 7 ms of it);
     - `#pragma optionNV(unroll all)`: 7.9 ms.
   - The splat's fetch count, its bedding and its strata were each within noise.
3. **The scanned cliffs' LOD error was inflated by their size** (−7 ms in the overview,
   −1 to −2 ms elsewhere).
   - The cook weighs normals and UVs by `0.2·radius` and `0.1·radius`, tuned on a
     metre-sized rock.
   - On a 20 m scan with bumpy normals, every simplification cost about a metre of
     "error". The LOD test kept the 570 cliff_wall chunks at full detail out to
     ~800 m.
   - From the overview the walls drew 22.8 M of the frame's 23.7 M triangles (stats
     run; removing the walls alone took the overview's raster from 15.7 to 6.0 ms).
4. **Indirect-count draws walked to every in-view slot** (−7 ms in the overview, −1 to
   −2 ms elsewhere).
   - The 1070 walks an indirect-count draw to its maximum (see nanite-draw-bound).
   - The bound was every command slot of every instance in view: 817k slots for 64k
     packed commands in the overview, in both camera passes.
5. **The visibility buffer's record budget overflowed**.
   - Records were reserved per slot. The overview's first pass reserved more than the
     2^20 an ID can name.
   - The occlusion pass then went the forward way, and the compute rasteriser was
     refused every cluster.
6. **The 256 MB page pool thrashed** (default settings).
   - The canyon's pages are 580 MB. A still camera streamed ~15 pages a frame and
     starved up to 360 a pose.
   - The starved coarse clusters drew big blotchy shadows on the canyon walls
     (`base0` shots). They were an artifact, and they are gone.
   - Each pose also cost ~4 ms of stream updates.
7. **Texture compression switched the compute rasteriser off.**
   - `swEligible` read the texture's top level back to check its alpha.
   - BC7 blocks do not give back exactly 255, so with `texture_compression` on every
     textured cooked mesh went to the hardware rasteriser.
8. **CPU: each placement's model matrix was rebuilt four times a frame** (−2.5 ms CPU).
   - `Scenery.ModelMatrix` does sin/cos plus three products, and was called by the
     instance upload, the VSM tracker (twice) and GI's walk.
   - That was 14% of a moving frame's CPU.

Checked and found **not** to be costs:
- splat fetch count, bedding or strata (each within noise);
- VSM sampling in the material pass (0);
- anisotropy (16x vs 1x: no change);
- the backdrop's 192 ring tiles (`dc_nofar`: no raster change);
- dust volumetrics (0: the level has none);
- TAA/HDR (0.7 ms in total);
- GI (1.5–1.9 ms with default settings);
- ImGui (0.03 ms);
- VSM on a still camera (0.04 ms: pages are cached).

## Fixes

Each fix is listed with its A/B switch. Every switch's "off" is the old behaviour.

| Fix | Files | Switch (off = old) |
| --- | --- | --- |
| The editor's filled view takes the scene-target path (vis buffer, VSM, TAA, HDR). The output pass also writes the scene's depth into the window, every sample, so gizmos and outlines depth-test as before. Wireframe stays forward. | render/editor_scene_frame.go (new), render/taa.go, shaders/taa_output_frag.glsl | `FOUNDRY_EDITOR_SCENE_FRAME=0` |
| Material-pass variants: each bin's program is fragment.glsl's main with the tests its surface cannot use rewritten `(false && …)`. The key is water, blend, baked, modulate and the seven fx bits the material declares. `(true && …)` keeps the uniform deciding, so the picture is the generic program's. | render/nanite_vis_variants.go (new), render/nanite_vis.go | `FOUNDRY_NANITE_MAT_VARIANTS=0` |
| Splat, triplanar and detail effects with no runtime indexing: one call per layer and axis with constant arguments. The arithmetic is unchanged. A provably-zero splat layer is not sampled. | shaders/surface_fx.glsl | — |
| `nanitecook.Options.AttrRadiusCap` caps the radius the attribute weights scale by. It is hashed only when set, so existing cooks keep their hashes and bytes. Poly Haven imports cook with `phimport.ScanAttrRadiusCap` = 1 m (namaqualand_cliff_02 at τ 0.4: 149k → 1.9k triangles). The polyhaven_rocks/plants stage Version goes 1 → 2, so a `gen run` recooks. | nanitecook/cook.go, nanitecook/attrcap.go (new), nanitecook/mesh.go, procgen/meshout/write.go, polyhaven/phimport/model.go, procgen/stages/polyhaven.go | not set |
| The draw bound is tightened by LOD. Per instance and group, the tiles are sorted by their clusters' largest parent error. A tile whose every parent is fine from the instance's nearest possible distance is not counted. This is conservative against the shader's own test: a property test over 400 random eyes and scales, plus pixel-identical shots (`def11` against `def11off`). | render/nanite_draw_lod_bound.go (new), render/nanite_cull.go, render/nanite_scenery.go | `FOUNDRY_NANITE_DRAW_LOD_BOUND=0` |
| Dense visibility-buffer numbering: only commands that draw take a record, from one GPU counter shared by every pass and the compute rasteriser. Past 2^20 a command is dropped, not numbered wrong. | shaders/nanite_vis_number_comp.glsl, shaders/nanite_swraster_comp.glsl, render/nanite_vis.go, render/nanite_swraster.go | `FOUNDRY_NANITE_VIS_DENSE=0` |
| The page-pool budget a player has not set is an eighth of the card's memory, 256..1024 MB (`GL_NVX_gpu_memory_info` / `GL_ATI_meminfo`; unknown keeps 256). A pool is still only as big as its level needs, so small levels are unchanged. | gfx/nanite_pool_default.go (new), gfx/nanite_settings.go, app/graphics_settings.go, render/boot.go | set `nanite_stream_pool_mb` |
| Texture opacity is read from the decoded image, so BC7 no longer refuses the compute rasteriser. The readback stays as the fallback. | gfx/texture_opaque.go (new), gfx/texture.go, render/nanite_swraster.go | — |
| Each frame, every placement's matrix is worked out once, before any goroutine starts, then only read. `Scenery.WorldAABBFrom` / `BoundCenterFrom` take it. | render/scenery_xform.go (new), render/vsm_track.go, render/gi_sources.go, render/nanite_scenery.go, world/scenery.go | — |
| A/B tools: `FOUNDRY_FX_MASK` (AND into matFx), `FOUNDRY_TEXTURE_ANISOTROPY` (lower the anisotropy), and the `editor=1` bench key, which also resolves MSAA before a shot. | gfx/material_fx.go, gfx/texture_filter.go, app/bench.go | — |

### Visual checks

- The draw bound, on against off: 0 pixels differ by more than 8 levels at any pose.
- Variants against the generic program: the `TestMaterialFxOnEveryPath` paths agree
  within 1 level.
- Editor shots (`perf_bench` runs `ed2u`, `fin_ed_user`) show the level, the spawn
  marker and the ImGui panels.
- The canyon now looks different in two ways, both intended:
  - The blotchy wall shadows of the starved pool are gone. They return with
    `FOUNDRY_NANITE_STREAM_POOL_MB=256`.
  - Distant cliff scans take coarser LODs, at the same 1 px of surface error. Fine
    normal detail comes from their normal maps.
- The editor view of every level now uses TAA, VSM and the HDR curve, as play does.

## Tests

New tests:
- render: `TestMaterialVariantsFindEveryTestTheyFix`,
  `TestMaterialVariantsTouchOnlyTheShadingMain`,
  `TestMaterialVariantKeyIsWhatTheSurfaceCanUse`,
  `TestDrawLODBoundCountsEveryTileTheCullCanDraw`;
- render (GL): `TestMaterialFxOnEveryPath` gained a "vis, generic program" path and a
  variant-against-generic check;
- nanitecook: `TestAttrRadiusCapIsHashedOnlyWhenSet`, `TestAttrRadiusCapBoundsTheWeights`,
  `TestAttrRadiusCapCoarsensABigBumpyMeshsCut`;
- gfx: `TestDefaultPoolIsAnEighthOfTheCardWithinBounds`,
  `TestTextureOpacityIsReadFromTheDecodedImage`.

Results are in the report that accompanies this change; see "Remaining costs" for
what is still open.

## Remaining costs and gaps

1. **The material pass is still the biggest pass**: 5.8–9.8 ms, about 3 ns a pixel.
   - Turning `texture_compression` on takes about 1 ms off at 2560 and 770 MB of VRAM
     (275 MB vs 1,047 MB). It costs ~10 s of encode on the first load only.
   - It is not the default: that is a player setting for every level, with a small
     look change.
   - Next steps: a cheaper far-terrain variant (the backdrop and distant tiles do not
     need bedding, strata or a 4-layer height blend), or a half-rate material for
     pixels past a distance.
2. **A moving camera costs ~20 ms GPU (≈50 fps), not 16.**
   - VSM redraws ~15 pages in ~5 rects a frame, 3.9–4.2 ms.
   - Its nanite cull walks all 408k tiles per rectangle, ~0.7 ms each. That is the
     per-rect cull the nanite-draw-bound note already lists.
3. **The CPU is close behind the GPU while moving** (13.6–14.3 ms).
   - The VSM tracker still walks every placement every frame.
   - Collision cuts decode synchronously when the camera outruns the prefetch.
4. **The overview is still ~20 ms.**
   - Its raster is now the backdrop and the terrain tiles.
   - 408k range tiles remain to be culled each pass.
5. **The editor view is now TAA'd.**
   - Thin overlays drawn inside DrawWorld (volume cages, markers) are jittered and
     resolved, and may look softer.
   - The viewmodel preview clears the scene depth before the copy, so with it open the
     gizmos do not depth-test against the world.
   - `FOUNDRY_EDITOR_SCENE_FRAME=0` brings the old view back.
6. **Not every cook takes the cap.** Only Poly Haven imports cook with
   `AttrRadiusCap`. Other big cooked meshes (chicago's props, generated rocks, hand
   cooks) keep the old weights until they opt in.
7. **Placement counts change on a rerun.** Rerunning the template after the recook
   moves a few placements: 580 chunks instead of 570, and 6,645 placements instead of
   6,607. The rocks manifest's measurements come from the new cooks. The benchmarks
   above swap only the `.fnm` files under the phase-7 layout.

## Windows copies

The bench copy is `%LOCALAPPDATA%\Temp\foundry-canyon-perf`: about 800 MB of project,
the exes `foundry-perf0..10.exe` and `out/` reports. It was deleted at the end of the
session.
