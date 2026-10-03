# Nanite Phase 4: visibility buffer, deferred materials and TAA

Status: **complete**, with the measured costs and gaps listed at the end.
Nothing is committed; Phase 4 is in the working tree on top of `5cfb2db`
(phase 3). Other sessions' uncommitted work was in the tree throughout
(installer/release/setup, levelio version stamping, the sun-shadow cascade
change in `shadow.go`/`fragment.glsl`/`biome_*.glsl`, and a music tracker in
`audio`, `editor`, `script`, `tracker`). Where this phase had to edit one of
those files (`fragment.glsl`, `shadow.go`) its hunks are separate from
theirs; nothing of theirs was changed.

Phase 4 covers component 8 (visibility buffer + deferred materials) and the
spec's switch of anti-aliasing from MSAA to TAA. It starts with the two
prerequisites the task set.

## Prerequisite 1: the bench levels captured only the clear colour

**Cause.** `render.StandUpWorldUniforms` set the world program's projection
(and every uniform default after it) with `gl.Uniform*`, which writes the
program *in use* — and `StandUpWorld` reaches it after `InitShadows` and
`InitWorldPasses` have bound programs of their own. So the world program kept
a zero projection. Nothing re-sent it unless the field of view changed or the
viewmodel pass ran (its `defer gfx.UploadProjection(gfx.Projection)` at the end
of a frame uploads the right matrix). Level1-3 have a `player:` block, so from
their second frame on they drew; `nanite_bench.yaml` and `nanite_stream.yaml`
have none, so every world draw collapsed to a point and only the clear colour
and the sky (its own program) reached the picture — headless and native
alike, while the profiler showed the passes running. The Nanite debug views
(their own program, `gfx.Projection` uploaded directly) drew the rocks fine,
which is how it was pinned down.

It also made every *single-frame* headless capture of any level empty: the
HEAD build's first `shot` of a level is the clear colour; only from the second
does the world appear.

**Fix.** `StandUpWorldUniforms` binds the world program first
(`render/boot.go`), and `DrawWorld` now uploads `gfx.Projection` to the world
program every frame after `SyncProjection` (it has to anyway for TAA's jitter).
The bench levels now capture their rocks, headless and native (the native
`shots=` picture of the rock field is in this phase's runs).

## Prerequisite 2: classic-path cooked meshes cast their full detail

A cooked placement the GPU cull does not draw (no compute, `FOUNDRY_NANITE_
MESHES=0`, `NoNanite`) is uploaded whole by `world.Scenery.loadNanite`, and
every sun cascade and every point-light face drew all ~82k triangles of a
bench rock.

- `gfx.Geometry.ShadowLOD` (new): a coarser stand-in the shadow passes draw
  instead (`Geometry.ShadowCaster`). `Delete`/`Unload` release it.
- `world.Scenery.loadShadowLOD` (new): on the classic path, each slot's
  **fallback cut** (Phase 2's mid-level DAG cut, its own small pool) is
  uploaded as that slot's `ShadowLOD`. The colour pass keeps the full detail.
- `drawGeometryShadow` (per placement, cascades and cubes) and the instanced
  scenery shadow (`renderSceneryShadowInstanced`, with the instance matrices
  attached to the stand-in's VAO too) draw `ShadowCaster()`.
- `FOUNDRY_NANITE_CLASSIC_SHADOW_LOD=0` (`world.NaniteClassicShadowLOD`) casts
  the full detail as before. The Phase 1 shadow comparisons, which compare the
  classic path's map against the cull's sample for sample, pin it off.

**Test** (`render/scenery_shadow_count_gl_test.go`,
`TestClassicCookedSceneryCastsItsFallbackCut`, `GL_PRIMITIVES_GENERATED`):
eight classic placements of a 20,480-triangle rock cast exactly
8 × 2,550 = 20,400 triangles into a sun cascade and a point-light face, per
placement and instanced, against 163,840 with the switch off; the fallback is
required to be under a quarter of the full detail, and the shadow it writes is
within 3% of the full detail's samples (1.4% measured). Mutation-checked:
`ShadowCaster` returning the full mesh fails all four counts.

## What was built

### Settings (`gfx/antialias_settings.go`, `app/graphics_settings.go`, script props)

- `gfx.AntiAliasMode`: `taa` (default), `msaa` (the fallback: the window's
  multisampling, as before), `off`. Saved to `graphics.yaml` as
  `anti_aliasing` only once chosen (`AntiAliasModeChosen`, like `gi_quality`),
  so a player who never picked one follows the engine's default.
  `FOUNDRY_AA=taa|msaa|off` overrides it for a run.
- `gfx.TAASharpen` (0-1, default 0.25), saved as `taa_sharpen`.
- Scripts: `@level.prop.anti_aliasing`, `@level.prop.taa_sharpen`.
- `F10` (`AntiAliasEnabled`): in a TAA frame it turns the jitter and the
  resolve off and shows the frame as drawn.
- A release build in TAA mode makes its window **without** multisampling
  (`render.CreateAntiAliasedWindow`): only the resolved picture reaches it.
  A development build keeps the window's MSAA (`render.KeepWindowMSAA`, set in
  `app/devtools_dev.go`) because the editor's 3D views and gizmos draw into it;
  the benchmark clears that (`app/bench.go`), so it measures what a player
  gets. MSAA chosen at run time in a game started without samples takes effect
  at the next start. `off` makes its window without samples too, and a game
  frame in `off` drawn into a window that has them (a development build's)
  turns the multisampling and alpha-to-coverage off for the frame.

### The scene target (`render/scene_target.go`)

In a TAA frame (`sceneFrameWanted`: TAA mode, not the editor's own view, not
wireframe) `DrawWorld` draws into a framebuffer of the renderer's own, sized to
the caller's viewport:

| Attachment | Format | What |
| --- | --- | --- |
| `COLOR0` | RGBA16F | the frame's colour (the only buffer drawn to outside the passes below) |
| `COLOR1` | R32UI | visibility-buffer triangle IDs |
| `COLOR2` | RG16F | moving objects' motion vectors |
| `COLOR3` | R32UI | the material pass's bin per pixel |
| depth-stencil | DEPTH24_STENCIL8 | the window's format: decal offsets and the volumetrics' depth copy behave as before; the stencil says which pixels the ID pass still owns |

`sceneFrameBegin` declares it as the frame target (`frame_target.go`), so
every pass that steps out to a shadow map, the Hi-Z build or the volumetrics'
depth copy steps back into it; `sceneFrameEnd` resolves into the caller's
target and hands that back. The forward pass's final picture is the scene
target's within 1/255 (half-float rounding): headless, the composite level's
MSAA-mode frame against the scene target with the resolve off differs by at
most one level in any pixel.

Two traps found on the way, both in this file now:
- `glClearBuffer*` names a *draw buffer*, not an attachment: with only
  `COLOR0` drawn to, clearing "buffer 2" cleared nothing, and last frame's
  bins and motion survived. `clear()` now makes every attachment a draw
  buffer for the clear.
- A remade texture can come back under the same name; a framebuffer of the old
  one then draws into a texture nothing reads. `sceneTarget.gen` counts
  remakes, and the material pass's framebuffers are rebuilt on a change.

### TAA (`render/taa.go`, `taa_*.glsl`)

1. **Jitter.** After `SyncProjection`, `gfx.Projection` is moved by the next
   point of an 8-point Halton(2,3) sequence (`gfx.JitterProjection`, in clip
   space so the offset is the same on screen at any depth) and uploaded; the
   viewmodel's own lens takes the same jitter (`gfx.ProjectionJitter`). The
   unjittered projection is put back at the end of the frame.
2. **Motion vectors** (`taaMotionPass`, before the viewmodel clears the depth):
   - moving objects rasterise their own (`taa_object_*.glsl`, skinning
     injected, the position computed exactly as `vertex.glsl` computes it so
     the LEQUAL test against the colour pass's depth passes): entities'
     opaque attachments, chunks whose matrix changed (a frame carrying a
     ship, `Origin`), and brushes a script moved — recorded as the opaque pass
     draws them (`taaNoteGeometry`, `taaNoteChunk`, `taaNoteBrush`), drawn only
     when their matrix differs from last frame's;
   - everything else gets the camera's motion from the depth
     (`taa_velocity_frag.glsl`: the jittered inverse to rebuild the position,
     the unjittered matrices to measure it), dilated to the nearest surface of
     each 3x3 so an edge keeps its own motion;
   - the viewmodel's pixels move with the camera: zero (`taaViewmodelMotion`).
3. **Resolve** (`taa_resolve_frag.glsl`): history read along the motion
   through a 5-tap Catmull-Rom, clipped toward the 3x3 neighbourhood's mean
   within 1.25σ (YCoCg, intersected with its min/max), blended 10% new (more
   for fast-moving pixels). Off-screen history or none takes the new frame.
   **No luminance weighting:** Karis's inverse-luma weights darkened thin
   bright features — a dithered cutout's edge lost 0.8% of its area — and the
   frame is LDR.
4. **Sharpen and output** (`taa_output_frag.glsl`): an unsharp mask of
   `taa_sharpen`, clamped to the five taps' range so it cannot ring. Written
   at the caller's viewport, whose origin it subtracts from `gl_FragCoord`
   to read the source.
5. **History is forgotten** on the first frame, a resize, a new level, a lens
   change, a camera jump over 4 m in a frame, and any frame drawn the forward
   way in between (the editor's view between two stretches of play).

**Cutouts.** With one sample a pixel there is nothing for alpha-to-coverage to
spend, so in a TAA frame the cutout shaders turn their coverage ramp into the
chance a pixel is kept, a stratified threshold that changes each frame
(`coverageNoise`: interleaved gradient noise plus frame/8), and the history
averages it into a soft edge: `fragment.glsl` (its `alphaSharpen` path),
`foliage_frag.glsl`, `foliage_imp_frag.glsl`, `sprite_frag.glsl`'s cutout mode
and the ID pass. A uniform `coverageDither` (0 = off, what every other program
holds) switches it; `SetAlphaToCoverage` never enables alpha-to-coverage in a
scene-target frame.

**Headless shots** (`harness/shot.go`) draw the frame `render.TAAShotFrames()`
(16) times at the same instant first, so a `foundry play` picture is the
converged one, deterministically.

### The visibility buffer (`render/nanite_vis.go`, `nanite_vis_source.go`, `nanite_vis_*.glsl`, `nanite_material_*.glsl`)

In a scene-target frame, with `FOUNDRY_NANITE_VISBUFFER` on (default), outside
the Nanite debug views:

1. **Numbering** (`nanite_vis_number_comp.glsl`), after each camera pass's
   cull, merge and packing: each command of a group the material pass shades
   gets the next **record** of the frame's list (first index, base vertex,
   instance, material bin; 32 bytes) and its base instance becomes the
   record's number. Water or blended groups (what `prepassable` refuses) keep
   their commands and draw the forward way right after.
2. **The ID pass**: the same indirect draws, through `vertex.glsl` itself
   (`visVertexSource`: the instance matrix read from the cull's instance
   buffer by the record an instanced attribute at 11-12 fetches; everything
   else the file's own, `invariant gl_Position` included, so the depth is the
   forward path's to the bit) and `nanite_vis_frag.glsl`, which decides
   coverage exactly as the Phase 1 depth prepass did and writes
   `(record + 1) << 12 | gl_PrimitiveID`. A camera pass in such a frame caps a
   merged command at 32 clusters (`mergeMax` in `nanite_merge_comp.glsl`) so
   12 bits number its triangles. **This replaces the Phase 1 depth prepass on
   this path.**
3. **Who owns a pixel** is the stencil: from `naniteVisBegin` to the material
   pass, every opaque draw sets the stencil where it wins the depth test, an
   ID draw to 1 and everything else (terrain, entities, classic scenery, moved
   brushes, chunks under 200 triangles) to 0. The last to win decides.
4. **The material pass** (`naniteVisShade`), after the cull's last pass
   (`naniteOcclusionPass`) and before foliage, scatter, cutout sprites,
   decals, sky, transparents, volumetrics and the viewmodel:
   - classify (full screen, stencil = 1): each pixel's bin from its record;
   - tiles (compute, 64x64): each bin's list of tiles and its indirect
     draw's instance count;
   - bin depth (full screen): bin *b* written as depth *b*/2^20, exact in a
     32-bit float depth;
   - per bin (one indirect instanced draw of its tiles, at its own depth,
     `GL_EQUAL`, `early_fragment_tests`): the material program, whose
     fragment stage is `fragment.glsl` with its `main` renamed and its
     varyings made globals, preceded by `vertex.glsl` as a function
     (`vx_main`) and followed by `nanite_material_frag.glsl`, which fetches
     the triangle (records, the group's own index, position/UV, normal and
     baked-light buffers bound as storage blocks), runs the three corners
     through `vx_main`, reconstructs every varying at the pixel from 2D
     homogeneous barycentrics (perspective-correct, no per-corner divide),
     and calls `fragment.glsl`'s main. The four screen-space derivatives
     `fragment.glsl` takes are replaced by the barycentrics' analytic ones,
     its four material samples by `textureGrad` with them, and
     `gl_FrontFacing` by the triangle's homogeneous determinant. The
     material program gets the frame's lights, cascades, cloud shadow, live
     GI, point shadows and fog by the same `Upload*To` calls every lit
     program uses.
   - **Shaded once a pixel**, however many surfaces the ID pass drew.
5. Everything drawn after composites against the ID pass's depth — which is
   the forward path's depth — so decals, the sky, transparents, volumetrics,
   foliage and sprites behave as they did.

**The rewrites are checked.** `nanite_vis_source.go` rewrites the sources it
is given rather than keeping copies (`fragment.glsl` had three other sessions'
edits while this was written); a pattern it no longer finds, or a derivative it
cannot reconstruct, is an error and the visibility buffer is not used.

**What a frame cannot number** — over 2^20 commands, over 8,192 bins, or a
buffer beyond the driver's largest storage block (the bins' tile lists, a
screen's tiles a bin, included) — is drawn the forward way (logged once).

**F3**: `Nanite shade: visibility buffer, N material bins`. Profiler sections:
`nanite: vis` (numbering + ID draws, under each cull pass), `nanite: material`
with `nanite: classify`, `taa: motion`, `taa: resolve`, `taa: output`.

### Smaller changes

- `gfx.SurfaceLocs` / `SurfaceState.ApplyLocs`: `ApplyTo` with locations
  resolved once (the material pass applies hundreds of materials a frame).
- `cullBatch` carries the visibility buffer's group table (`visGroupBuf`).
- `app/bench.go` reports the AA mode, the window's samples and whether the
  visibility buffer is on.
- Docs: guide 19 (new "Anti-aliasing" section), 17 (settings), 15 (cutouts),
  SCRIPTING.md (the two props).

## Flags and settings

| Flag / setting | Default | Effect |
| --- | --- | --- |
| `anti_aliasing` (graphics.yaml), `@level.prop.anti_aliasing`, `FOUNDRY_AA` | `taa` | `msaa` is the Phase 0-3 frame exactly (forward into the window, alpha-to-coverage); `off` likewise without samples (a window made in `off` has none; in one that has them — a development build's — a game frame turns `GL_MULTISAMPLE` and alpha-to-coverage off and puts them back after, `beginAAModeFrame` in `antialias.go`). |
| `taa_sharpen`, `@level.prop.taa_sharpen` | 0.25 | Output sharpening, 0-1. |
| `F10` / `FOUNDRY_TAA_RESOLVE=0` | on | In a TAA frame: off draws into the scene target with no jitter and no resolve — the A/B the parity checks use. |
| `FOUNDRY_NANITE_VISBUFFER` | on | Off: in a TAA frame the cull's draws shade forward, with the Phase 1 prepass. |
| `FOUNDRY_NANITE_CLASSIC_SHADOW_LOD` | on | Off: a classic-path cooked mesh casts its full detail. |
| Phase 0-3 flags | unchanged | |

## Tests

| Test | What it checks |
| --- | --- |
| `render/nanite_vis_source_test.go` | The rewrites run on the repository's own `vertex.glsl`/`fragment.glsl`: every pattern found, no derivative, facing, material `texture()` or discard left, one `#version`, one `main`, shared uniforms declared once; a stage with an unreconstructed derivative or without the UV derivative is refused. The TAA jitter is 8 distinct offsets inside the pixel, averaging near its centre. |
| `render/nanite_vis_gl_test.go` (GL, through `DrawWorld`) | **`TestVisBufferShadesTheForwardPicture`**: a level with compiled surfaces (baked light), a live chunk (double-sided, moved and translucent brushes), seven cooked rocks, a classic rock in front, a translucent pane, ambient + shadowing sun + shadowing point light; three poses, visibility buffer on against off, resolve off: at most 2 pixels of 36,864 past 8 levels, none past 32, worst 10 (allowed: 0.2% past 8, none past 32); the material pass must have shaded bins with it on and none with it off. **`TestVisBufferShadesEachPixelOnce`**: one wall or eight stacked, the material pass shades exactly 36,864 pixels (`GL_SAMPLES_PASSED`) — the forward frame without a prepass passes 73,728 and 184,320. **`TestTAAConvergesTowardTheSupersampledPicture`**: 16 frames of TAA against the frame drawn at 4x and boxed down: mean error 1.70 vs one sample's 2.13, pixels past 32 levels 11 vs 546; sharpening at 0.5 changes it (and, the reference being a soft supersampled picture, moves it to 2.06 — still under one sample's error, which the test requires). **`TestTAAMotionVectorsFollowTheCameraAndAMovingChunk`**: a 0.1 m sideways camera move reads −0.03017 at the centre against −0.0302 analytic; a chunk moved 0.1 m in front of a still camera reads its own motion (0.0175 analytic, 0.1 m at 6.9 m) while the wall round it reads zero; a 5 m jump is resolved without history and the frame after with it. **`TestTAAResolvesDitheredCutoutCoverage`**: a cutout disc's single frame has a hard edge (0 partial pixels), the resolved one 387 against the supersampled 334, its error 0.86 → 0.39 and its area within 0.6% of the supersampled disc's (0.5% measured). Mutation: with the dither off the area is 0.9% off and the test fails. |
| `render/scenery_shadow_count_gl_test.go` | Prerequisite 2 above. |
| `render/antialias_mode_gl_test.go` | `off` in a window with samples: a game frame has `GL_MULTISAMPLE` and alpha-to-coverage off and puts them back; the editor's view and `msaa` keep them. Mutation-checked (the frame hook removed fails it). |
| `app/graphics_settings_test.go`, `script/script_nanite_props_test.go` | The mode round-trips once chosen, an unknown name is ignored, the sharpening clamps; the script props set, clamp and mark for saving. |
| Phase 1-3 GL tests | Unchanged and passing (they draw pieces of the frame, not `DrawWorld`, so they run the forward path). |

**Runs on this box** (WSL2, Mesa 26.2 D3D12 on the GTX 1070):

- `go build ./...`, `go vet ./...`, `go test ./...`: clean (at the time of the
  last run; another session's editor work did not compile for a stretch of
  this phase, and the headless binaries were built with HEAD's editor then).
- `FOUNDRY_GL_TEST=1 go test ./internal/render/... ./internal/gfx/...
  ./internal/devui/... ./internal/editor/... ./internal/harness/...`: all pass
  except the known `TestAPlaySessionDrawsTheRoomItIsStandingIn`. Render
  takes about 100 s.
- The render GL suite also passes with `FOUNDRY_AA=msaa`, with
  `FOUNDRY_NANITE_VISBUFFER=0 FOUNDRY_TAA_RESOLVE=0`, and with every Nanite
  switch off plus `FOUNDRY_AA=msaa`.
- **The known harness failure, diagnosed:** the scratch project's room is
  lit by its global light alone, the session draws only when it shoots, and
  with live GI on (picked by GPU) the ambient comes from probes that were never
  traced — so the room is black. With `gfx.GIQuality = GIOff` in the test it
  passes. Left as it was (not this phase's test); noted here.

## Visual parity (headless, WSL D3D12, 640x360, `-gi off` unless said)

Compared with straight colour. (The forward path leaves the target's alpha
under 1 behind a translucent surface; the scene target's output writes 1. A
PNG viewer composites by that alpha, which looked like a difference in the
glass and is not one: the colours agree within one level.)

| Scene | Visibility buffer vs forward (both scene target, resolve off) |
| --- | --- |
| Rock field from above (`nanite_bench`, `teleport 0,20,2; look -90 -28`) | 2 px past 8 levels, none past 24, worst 20, mean 0.03 |
| level1 spawn (live chunks through the GPU cull, hold, viewmodel) | none past 8, worst 3 |
| level1 hangar, compiled (`teleport 1.5,102,-20`): compiled level, skinned marines, dropship, viewmodel, stars | none past 8, worst 5 |
| Composite level (below): terrain, grass foliage, cooked rocks, decals, glass, cloud volume, fire, skinned marine, classic prop, viewmodel | none past 8, worst 7 |
| Composite, live GI low | none past 8, worst 6 (GI changes the picture by 44 levels on average, so it is in play) |
| `nanite_stream` at a 64 MB pool (streamed rocks) | none past 8, worst 7 |
| Forward MSAA mode vs scene target, composite | worst 1 (half-float rounding) |

**The composite level** (`phase4_bench/make_composite.py` makes it from a game
project's `nanite_bench.yaml`/`level1.yaml` and the repo's `zz_foliage.yaml`)
exists because no game level has terrain, foliage and decals beside cooked
meshes; it holds every pass the spec lists.

**TAA against 4x4 supersampling** (the forward frame at 2560x1440 boxed down):

| Scene | One sample: mean error / px past 24 | TAA converged |
| --- | --- | --- |
| Rock field | 3.76 / 4,463 | 2.48 / 416 |
| Composite | 2.05 / 5,616 | 1.40 / 698 |

TAA does not match the old MSAA 8x picture pixel for pixel — a different
filter, and supersampled shading rather than edges only — and is not meant
to. Native, the MSAA path's picture is unchanged (it is the Phase 3 frame).

## Measurements (native, GTX 1070)

**Setup.** As Phase 1-3: `foundry-nanite4.exe` cross-built from this tree,
1600x900, 90 warm-up + 240 measured frames a pose, the bench copy
`%LOCALAPPDATA%\Temp\foundry-bench-n1f` (its shaders replaced with this tree's;
the Phase 3 ones kept in `resources/shaders.p3`), `SDL_AUDIODRIVER=dummy`
(no sound). GPU 7% busy before. Raw reports: `docs/nanite/phase4_bench/`
(`msaa_*`, `taanovis_*`, `taa_*`; `taa_first_*` is the first visibility-buffer
build, before the early-Z bin depth and the CPU savings).

**MSAA** = MSAA 8x, the Phase 3 frame. **TAA** = TAA, window without samples.
**+VB** = TAA with the visibility buffer (the new default).

| Scene / pose | Frame avg ms MSAA / TAA / +VB | GPU frame MSAA / TAA / +VB | Opaque cull draws GPU: forward → ID + material |
| --- | --- | --- | --- |
| level1 spawn | 6.70 / 7.09 / 7.92 | 6.55 / 6.99 / 7.82 | chunks 2.67 → 0.29 + 3.19 |
| level1 hangar | 7.81 / 8.01 / 8.44 | 7.65 / 7.91 / 8.35 | chunks 3.16 → 0.36 + 3.16 |
| level1 ground | 2.66 / 2.98 / 3.01 | 2.52 / 2.88 / 2.91 | — |
| level2 spawn | 10.27 / 10.04 / 11.65 | 8.34 / 8.47 / 9.07 | compiled 3.40 → 0.39 + 3.28 |
| level2 `0,8,10` | 10.08 / 10.09 / 11.48 | 8.27 / 8.48 / 9.10 | compiled 3.45 → 0.43 + 3.38 |
| level3 spawn | 13.53 / 13.64 / 15.43 | 9.49 / 9.63 / 8.41 | compiled 4.79 → 0.50 + 2.94 |
| rock field behind the wall | 15.00 / 15.37 / 15.55 | 14.87 / 15.29 / 15.46 | — |
| **rock field from above** | **34.90 / 18.30 / 16.95** | 34.73 / 18.21 / 16.86 | scenery 4.91 → 2.59 + 1.04 |
| **composite** | **17.56 / 14.55 / 13.61** | 17.38 / 14.46 / 13.52 | scenery 1.96 → 0.36 + 0.67 |

**What it says:**

- **TAA itself costs about 0.36 ms** (motion 0.09, resolve 0.20, output 0.07)
  and the scene target a little more, against 8x MSAA's resolve and
  per-sample rasterisation. Where geometry is dense, MSAA is the expensive
  one: the rock field from above halves (34.9 → 18.3), the composite drops 3 ms.
- **The visibility buffer wins where many triangles reach few pixels** (dense
  cooked meshes): rock field 18.3 → 17.0, composite 14.6 → 13.6, its
  material pass under a millisecond.
- **On brush levels it is about even on the GPU and costs on the CPU.**
  Their surfaces are a few big triangles, the Phase 1 prepass already shaded
  each pixel about once, and the material pass's shading (lights, 5x5 PCF
  cascades, live GI — the same work) is now preceded by the triangle fetch and
  three `vertex.glsl` runs a pixel: GPU +0.4 ms (level1 hangar), +0.6 (level2),
  −1.2 (level3). On the CPU, numbering and binding a thousand material bins
  is +0.5 ms on level3 and about even on level2; level2/level3 are CPU-bound
  and swing ±1 ms run to run (their frame avg moved more than their nanite
  sections did — `gi: field`, `update` and `entity update` rows differ by
  0.3-0.5 ms between runs of the same build).
- **Shading cost stays flat with overdraw**: shown exactly by the GL test
  (36,864 shaded pixels for one wall or eight), and on the rock field from
  above, where the forward colour pass costs 4.9 ms and the material pass
  1.0.

## Known gaps / notes

- **Brush-heavy levels are not faster** with the visibility buffer (above).
  The obvious next steps: skip the corners' normal transform
  (`transpose(inverse(mat3(M)))` per corner per pixel) for an identity
  instance, and pack a bin's group state so the CPU binds less per bin.
- **The editor's own 3D views stay on the forward path** (MSAA), by design;
  Play is the game's path.
- **Object motion is rigid.** A skinned attachment's motion is its matrix's,
  not its bones' (the current pose is used for both ends), so a limb swinging
  in place leans on the neighbourhood clamp. Foliage sway and particles are
  likewise camera-motion only.
- **A moved brush inside a moving chunk** gets the brush's own matrix change,
  which includes the chunk's; fine. A chunk under 200 triangles is drawn
  forward (CPU path) and still gets object motion.
- **Fast-moving thin features** can ghost a little before the clamp catches
  them, as with any TAA without reactive masks.
- **`gl_PrimitiveID` across a multi-draw** is assumed to restart per command
  (the GL spec's "as if separate draws"); the parity tests would show a
  driver that did otherwise. Checked on Mesa D3D12 and natively on NVIDIA
  (the native captures draw correctly).
- **Bins are per (batch, group)**, not per material: the same texture in two
  chunks is two bins. Cheap with the early-Z bin depth, but it is why level3
  has ~1,000 bins.
- **Mesa D3D12 flake**: twice in this phase a headless `foundry play` of the
  compiled level1 hung in `glBufferData` during the level load (an entity's
  model upload, before any Phase 4 code runs; once with the visibility buffer
  off). A rerun succeeded both times. Phase 1 recorded the same family.
- **Single-frame captures before this phase were empty** (prerequisite 1):
  earlier phases' level1/level2 headless parity numbers came from later shots
  of a run, which is why they were not.
- **Scratch output** in the bench copy, safe to delete: `n4*` reports and logs,
  `n4*.sh`, `run_n4.cmd`, `_n4.cmd`, `n4cmp.py`, `n4sum.sh`,
  `foundry-nanite4.exe`, `shots_n4_test/`, `resources/levels/phase4_composite.yaml`,
  `resources/biomes/` (copied from the repo for the composite's terrain), and
  `resources/shaders.p3` (the Phase 3 shaders).

## Independent verification

A separate pass re-checked the phase against the spec, with these results:

- **Fixed.** `anti_aliasing: off` was parsed, saved and scriptable, but
  nothing read it. "off" drew exactly like `msaa` (the window's samples,
  alpha-to-coverage). It is now honoured, as above, with
  `antialias_mode_gl_test.go`.
- **Fixed.** `taa_output_frag.glsl` indexed its source by `gl_FragCoord`.
  A caller's viewport with a non-zero origin would have read the wrong texels.
  The current callers (the window, a headless shot) start at 0,0, so no
  picture changed: the rock field is identical before and after.
- **Fixed.** The bins' tile lists (`bins × screen tiles × 4` bytes, one
  storage block) were not checked against the driver's largest block, and
  were sized by the largest bin count seen rather than this frame's. Both are
  fixed.
- Comments in `fragment.glsl` and `taa.go` named `ground_frag.glsl` (the biome
  ground) as a dithering cutout. No runtime program compiles that shader
  (`ground_frag.glsl` is referenced only by tests), so the comments now list
  the shaders that actually dither.

Headless captures, rebuilt from this tree (640x360, `-gi off`, worst channel
difference in straight colour):

| Scene | HEAD (5cfb2db) vs `FOUNDRY_AA=msaa` | msaa vs scene target, resolve off | forward vs visibility buffer |
| --- | --- | --- | --- |
| level1 compiled, second shot (turned 90°) | 0 px differ | worst 1 | worst 5 |
| level1 hangar (animated mechanics) | 185 px past 8, all on the animated characters | as left | 98 px past 8, the same characters; walls worst 3 |
| composite, three poses | 0 px (2nd), worst 4 (3rd) | worst 1 | worst 0 / 0 / 6 |
| rock field, two poses | HEAD blank (prerequisite 1) | worst 1 | worst 8 / 1 |

The first shot of every HEAD run is the clear colour (prerequisite 1), so it
is not compared. `FOUNDRY_AA=off` on the rock field is pixel-identical to
`msaa` in a headless target, which has no samples.

GL suites: render and gfx pass in the default mode, with `FOUNDRY_AA=msaa`,
and with every Nanite switch off plus `msaa`. devui and editor pass. harness
fails only the known `TestAPlaySessionDrawsTheRoomItIsStandingIn`.
